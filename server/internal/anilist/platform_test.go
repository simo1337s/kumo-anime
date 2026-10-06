package anilist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
)

// ---------------------------------------------------------------------------
// Test helpers: a fake AniList server and a Platform backed by a temp DB.

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// op names the operation of one of the queries in client.go.
func op(query string) string {
	for _, o := range []struct{ marker, name string }{
		{"SaveMediaListEntry(", "save"},
		{"DeleteMediaListEntry(", "delete"},
		{"MediaListCollection(", "collection"},
		{"airingSchedules(", "schedule"},
		{"Viewer {", "viewer"},
		{"Media(id:", "media"},
		{"Page(", "search"},
	} {
		if strings.Contains(query, o.marker) {
			return o.name
		}
	}
	return "unknown"
}

// newTestPlatform returns a logged-out Platform whose AniList requests go to
// handler (a nil handler fails the test on any request).
func newTestPlatform(t *testing.T, handler func(w http.ResponseWriter, req gqlRequest)) *Platform {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		if handler == nil {
			t.Errorf("unexpected AniList request: %s", op(req.Query))
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		handler(w, req)
	}))
	t.Cleanup(srv.Close)
	p := NewPlatform(d, events.NewHub())
	p.client.endpoint = srv.URL
	return p
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func data(v any) map[string]any { return map[string]any{"data": v} }

func viewerReply(id int, name string) map[string]any {
	return data(map[string]any{"Viewer": map[string]any{"id": id, "name": name}})
}

var rejectedToken = map[string]any{"data": nil, "errors": []any{map[string]any{"message": "Invalid token", "status": 400}}}

// setLoggedIn puts p in the logged-in state without asking AniList and
// returns the new session.
func setLoggedIn(p *Platform, userID int) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.session++
	p.viewer = &Viewer{ID: userID, Name: "tester"}
	p.client.SetToken("token")
	return p.session
}

// seed caches c as a just fetched collection.
func seed(p *Platform, mediaType string, c *Collection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.collections[mediaType] = c
	p.fetchedAt[mediaType] = time.Now()
}

func cached(p *Platform, mediaType string) *Collection {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.collections[mediaType]
}

func testMedia(id, episodes int) *Media {
	m := &Media{ID: id, Type: "ANIME", Title: Title{Romaji: fmt.Sprintf("Show %d", id)}}
	if episodes > 0 {
		m.Episodes = &episodes
	}
	return m
}

func listEntry(id, mediaID int, status string, progress int) *ListEntry {
	return &ListEntry{ID: id, MediaID: mediaID, Status: status, Progress: progress, Media: testMedia(mediaID, 12)}
}

// testCollection groups entries into one list per status, in order.
func testCollection(entries ...*ListEntry) *Collection {
	c := &Collection{}
	lists := map[string]*List{}
	for _, e := range entries {
		l := lists[e.Status]
		if l == nil {
			l = &List{Name: listNames[e.Status], Status: e.Status}
			lists[e.Status] = l
			c.Lists = append(c.Lists, l)
		}
		l.Entries = append(l.Entries, e)
	}
	return c
}

// layout lists the media ids of each status list, in order.
func layout(c *Collection) map[string][]int {
	out := map[string][]int{}
	for _, l := range c.Lists {
		ids := []int{}
		for _, e := range l.Entries {
			ids = append(ids, e.MediaID)
		}
		out[l.Status] = ids
	}
	return out
}

func addLocal(t *testing.T, p *Platform, m *Media, status string, progress int) {
	t.Helper()
	raw, _ := json.Marshal(m)
	if _, err := p.db.Write(`INSERT INTO local_list(media_id, type, status, progress, media, updated_at) VALUES(?, ?, ?, ?, ?, ?)`,
		m.ID, m.Type, status, progress, string(raw), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
}

type localRow struct {
	Status   string
	Progress int
	Score    float64
	Repeat   int
}

func readLocal(t *testing.T, p *Platform, mediaID int) localRow {
	t.Helper()
	var r localRow
	if err := p.db.QueryRow(`SELECT status, progress, score, repeat FROM local_list WHERE media_id = ?`, mediaID).
		Scan(&r.Status, &r.Progress, &r.Score, &r.Repeat); err != nil {
		t.Fatal(err)
	}
	return r
}

// ---------------------------------------------------------------------------
// Sessions

// Several callers want the list at once (startup) and the token has expired:
// the first fetch is rejected and logs out, and the callers queued behind it
// must get the local list instead of crashing on the now missing viewer.
func TestCollectionTokenExpiredWhileQueued(t *testing.T) {
	var fetches atomic.Int32
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		switch op(req.Query) {
		case "viewer":
			reply(w, http.StatusOK, viewerReply(7, "tester"))
		case "collection":
			fetches.Add(1)
			time.Sleep(100 * time.Millisecond) // let the other callers queue up behind this fetch
			reply(w, http.StatusUnauthorized, map[string]any{"data": nil, "errors": []any{map[string]any{"message": "Unauthorized.", "status": 401}}})
		default:
			t.Errorf("unexpected request: %s", op(req.Query))
		}
	})
	ctx := context.Background()
	if _, err := p.Login(ctx, "soon-expired"); err != nil {
		t.Fatal(err)
	}
	addLocal(t, p, testMedia(1, 12), "CURRENT", 3)

	const callers = 8
	got := make([]*Collection, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got[i], errs[i] = p.Collection(ctx, "ANIME", false)
		}()
	}
	close(start)
	wg.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if e := got[i].Find(1); e == nil || e.Progress != 3 {
			t.Fatalf("caller %d didn't get the local list: %+v", i, got[i])
		}
	}
	if p.LoggedIn() {
		t.Error("still logged in after AniList rejected the token")
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("AniList was asked for the list %d times, want 1", n)
	}
}

// A rejection of a request sent before the user logged in again must not
// log the new session out.
func TestStaleRejectionKeepsNewSession(t *testing.T) {
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		reply(w, http.StatusOK, viewerReply(7, "tester"))
	})
	ctx := context.Background()
	if _, err := p.Login(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	old, _ := p.state()
	if _, err := p.Login(ctx, "new"); err != nil {
		t.Fatal(err)
	}
	p.expire(old)
	if !p.LoggedIn() || p.client.Token() != "new" {
		t.Fatal("a rejection meant for the old token logged the new session out")
	}

	current, _ := p.state()
	p.expire(current)
	if p.LoggedIn() || p.Viewer() != nil || p.client.Token() != "" {
		t.Fatal("still logged in after the current token was rejected")
	}
	var tok string
	if ok, _ := p.db.GetKV(kvToken, &tok); ok {
		t.Error("token still saved after logout")
	}
}

// A viewer refresh that was in flight during a logout must not log the user
// back in, in memory or in the database.
func TestRefreshViewerAfterLogout(t *testing.T) {
	var calls atomic.Int32
	inFlight, release := make(chan struct{}), make(chan struct{})
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		if calls.Add(1) == 2 { // the refresh (the first call is the login)
			close(inFlight)
			<-release
		}
		reply(w, http.StatusOK, viewerReply(7, "renamed"))
	})
	ctx := context.Background()
	if _, err := p.Login(ctx, "tok"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.RefreshViewer(ctx)
	}()
	<-inFlight
	p.Logout()
	close(release)
	<-done

	if v := p.Viewer(); v != nil {
		t.Fatalf("viewer %q is back after logout", v.Name)
	}
	var v Viewer
	if ok, _ := p.db.GetKV(kvViewer, &v); ok {
		t.Error("viewer saved again after logout")
	}
}

// The offline copy of the list must only ever be shown to its owner.
func TestSavedCollectionBelongsToItsUser(t *testing.T) {
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		http.Error(w, "AniList is down", http.StatusBadGateway)
	})
	setLoggedIn(p, 7)
	ctx := context.Background()
	list := testCollection(listEntry(100, 1, "CURRENT", 4))

	p.db.SetCache(collectionKey("ANIME"), savedCollection{UserID: 8, Collection: list}, 0)
	if c, err := p.Collection(ctx, "ANIME", false); err == nil {
		t.Fatalf("offline fallback returned another user's list: %+v", c)
	}
	p.db.SetCache(collectionKey("ANIME"), list, 0) // written by an older version: owner unknown
	if c, err := p.Collection(ctx, "ANIME", false); err == nil {
		t.Fatalf("offline fallback returned a list of unknown owner: %+v", c)
	}
	p.db.SetCache(collectionKey("ANIME"), savedCollection{UserID: 7, Collection: list}, 0)
	c, err := p.Collection(ctx, "ANIME", false)
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Find(1); e == nil || e.Progress != 4 {
		t.Fatalf("offline fallback = %+v, want the saved list", c)
	}
}

// ---------------------------------------------------------------------------
// Copy-on-write

// Readers (e.g. the HTTP handler encoding the list) keep the snapshot they
// got: patches build a new collection instead of editing it.
func TestPatchKeepsEarlierSnapshot(t *testing.T) {
	p := newTestPlatform(t, nil)
	session := setLoggedIn(p, 7)
	seed(p, "ANIME", testCollection(
		listEntry(101, 1, "CURRENT", 1),
		listEntry(102, 2, "CURRENT", 4),
		listEntry(103, 3, "PLANNING", 0),
	))
	before := cached(p, "ANIME")
	want, _ := json.Marshal(before)
	entry1 := before.Find(1)

	completed, planning, twelve, seven := "COMPLETED", "PLANNING", 12, 7
	p.patchCachedEntry(session, "ANIME", entry1.Media, EntryUpdate{MediaID: 1, Status: &completed, Progress: &twelve}, nil)
	p.patchCachedEntry(session, "ANIME", before.Find(2).Media, EntryUpdate{MediaID: 2, Progress: &seven}, nil)
	p.patchCachedEntry(session, "ANIME", testMedia(4, 24), EntryUpdate{MediaID: 4, Status: &planning},
		&ListEntryLite{ID: 104, Status: "PLANNING"})

	if got, _ := json.Marshal(before); string(got) != string(want) {
		t.Fatalf("a patch changed a snapshot a reader already had:\nbefore %s\nafter  %s", want, got)
	}
	if entry1.Status != "CURRENT" || entry1.Progress != 1 {
		t.Fatalf("a patch changed an entry a reader already had: %+v", entry1)
	}

	after := cached(p, "ANIME")
	wantLayout := map[string][]int{"CURRENT": {2}, "PLANNING": {3, 4}, "COMPLETED": {1}}
	if got := layout(after); !reflect.DeepEqual(got, wantLayout) {
		t.Fatalf("lists after patching = %v, want %v", got, wantLayout)
	}
	if e := after.Find(1); e.Status != "COMPLETED" || e.Progress != 12 || e.ID != 101 {
		t.Errorf("moved entry = %+v", e)
	}
	if e := after.Find(2); e.Status != "CURRENT" || e.Progress != 7 {
		t.Errorf("updated entry = %+v", e)
	}
	if e := after.Find(4); e.ID != 104 || e.Media == nil || e.Media.ID != 4 {
		t.Errorf("added entry = %+v (want the entry id AniList returned)", e)
	}
	p.mu.RLock()
	_, fresh := p.fetchedAt["ANIME"]
	p.mu.RUnlock()
	if fresh {
		t.Error("the cache wasn't marked outdated after a patch")
	}

	// An update made in an earlier session must not touch this one's list.
	p.patchCachedEntry(session-1, "ANIME", entry1.Media, EntryUpdate{MediaID: 1, Progress: &seven}, nil)
	if cached(p, "ANIME") != after {
		t.Error("a patch from an earlier session was applied")
	}
}

func TestDeleteEntryKeepsEarlierSnapshot(t *testing.T) {
	var deleted atomic.Int64
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		if op(req.Query) != "delete" {
			t.Errorf("unexpected request: %s", op(req.Query))
			return
		}
		id, _ := req.Variables["id"].(float64)
		deleted.Store(int64(id))
		reply(w, http.StatusOK, data(map[string]any{"DeleteMediaListEntry": map[string]any{"deleted": true}}))
	})
	setLoggedIn(p, 7)
	seed(p, "ANIME", testCollection(listEntry(101, 1, "CURRENT", 1), listEntry(102, 2, "CURRENT", 4)))
	before := cached(p, "ANIME")
	want, _ := json.Marshal(before)

	if err := p.DeleteEntry(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if id := deleted.Load(); id != 101 {
		t.Fatalf("deleted list entry %d, want 101", id)
	}
	if got, _ := json.Marshal(before); string(got) != string(want) {
		t.Fatalf("deleting changed a snapshot a reader already had:\nbefore %s\nafter  %s", want, got)
	}
	if got := layout(cached(p, "ANIME")); !reflect.DeepEqual(got, map[string][]int{"CURRENT": {2}}) {
		t.Fatalf("lists after deleting = %v", got)
	}
}

// If the token turns out expired while deleting, the entry must not be
// "deleted" from AniList using the id of the local list's entry.
func TestDeleteEntryWhenTokenExpires(t *testing.T) {
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		switch op(req.Query) {
		case "collection":
			reply(w, http.StatusOK, rejectedToken)
		default:
			t.Errorf("unexpected request: %s", op(req.Query))
		}
	})
	setLoggedIn(p, 7)
	m := testMedia(1, 12)
	p.db.SetCache(liteKey(1), m, time.Hour)
	addLocal(t, p, m, "CURRENT", 2)

	if err := p.DeleteEntry(context.Background(), 1); err == nil {
		t.Fatal("DeleteEntry succeeded although the login changed under it")
	}
	if p.LoggedIn() {
		t.Error("still logged in after AniList rejected the token")
	}
	if r := readLocal(t, p, 1); r.Status != "CURRENT" {
		t.Errorf("local entry changed: %+v", r)
	}
}

// Many readers encode the list while it is patched: run with -race. Every
// snapshot must be internally consistent (each media listed exactly once).
func TestReadersSeeConsistentSnapshots(t *testing.T) {
	const shows = 10
	base := func() *Collection {
		var entries []*ListEntry
		for id := 1; id <= shows; id++ {
			entries = append(entries, listEntry(100+id, id, "CURRENT", 1))
		}
		return testCollection(entries...)
	}
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		reply(w, http.StatusOK, data(map[string]any{"MediaListCollection": base()}))
	})
	session := setLoggedIn(p, 7)
	seed(p, "ANIME", base())
	ctx := context.Background()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				c, err := p.Collection(ctx, "ANIME", false)
				if err != nil {
					t.Error(err)
					return
				}
				raw, err := json.Marshal(c)
				if err != nil {
					t.Error(err)
					return
				}
				var decoded Collection
				_ = json.Unmarshal(raw, &decoded)
				seen := map[int]bool{}
				for _, l := range decoded.Lists {
					for _, e := range l.Entries {
						if seen[e.MediaID] {
							t.Errorf("media %d listed twice in one snapshot", e.MediaID)
							return
						}
						seen[e.MediaID] = true
					}
				}
				if len(seen) != shows {
					t.Errorf("snapshot lists %d shows, want %d", len(seen), shows)
					return
				}
				if _, err := p.MediaLite(ctx, 1+(r+i)%shows); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	statuses := []string{"COMPLETED", "PAUSED", "CURRENT"}
	for i := range 300 {
		id, status, progress := 1+i%shows, statuses[i%len(statuses)], i
		p.patchCachedEntry(session, "ANIME", testMedia(id, 12), EntryUpdate{MediaID: id, Status: &status, Progress: &progress}, nil)
		runtime.Gosched()
	}
	close(stop)
	wg.Wait()
}

// A fetch that started before an update and answers after it carries the
// old data: it must not replace the patched copy as "fresh".
func TestFetchOlderThanUpdateIsNotCached(t *testing.T) {
	var (
		mu       sync.Mutex
		progress = 1 // what AniList has for media 1
		fetches  int
	)
	inFlight, release := make(chan struct{}), make(chan struct{})
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		switch op(req.Query) {
		case "collection":
			mu.Lock()
			fetches++
			n, current := fetches, progress
			mu.Unlock()
			if n == 2 { // the slow fetch read the list, and answers after the update
				close(inFlight)
				<-release
			}
			reply(w, http.StatusOK, data(map[string]any{"MediaListCollection": testCollection(listEntry(101, 1, "CURRENT", current))}))
		case "save":
			mu.Lock()
			progress = int(req.Variables["progress"].(float64))
			saved := map[string]any{"id": 101, "mediaId": 1, "status": "CURRENT", "progress": progress}
			mu.Unlock()
			reply(w, http.StatusOK, data(map[string]any{"SaveMediaListEntry": saved}))
		default:
			t.Errorf("unexpected request: %s", op(req.Query))
		}
	})
	setLoggedIn(p, 7)
	ctx := context.Background()
	if _, err := p.Collection(ctx, "ANIME", false); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.fetchedAt["ANIME"] = time.Now().Add(-time.Hour) // due for a refresh
	p.mu.Unlock()

	slow := make(chan error, 1)
	go func() {
		_, err := p.Collection(ctx, "ANIME", false)
		slow <- err
	}()
	<-inFlight
	five := 5
	err := p.UpdateEntry(ctx, EntryUpdate{MediaID: 1, Progress: &five})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-slow; err != nil {
		t.Fatal(err)
	}

	if e := cached(p, "ANIME").Find(1); e == nil || e.Progress != 5 {
		t.Fatalf("cached entry = %+v, want progress 5: an older fetch overwrote the update", e)
	}
	c, err := p.Collection(ctx, "ANIME", false)
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Find(1); e == nil || e.Progress != 5 {
		t.Fatalf("Collection = %+v, want progress 5", e)
	}
	mu.Lock()
	defer mu.Unlock()
	if fetches != 3 {
		t.Errorf("%d fetches, want 3: the list must be refetched after the update", fetches)
	}
}

// ---------------------------------------------------------------------------
// Progress

func TestProgressUpdate(t *testing.T) {
	now := time.Date(2026, time.October, 6, 20, 30, 0, 0, time.Local)
	year := 2025
	someDate := FuzzyDate{Year: &year}
	e := func(status string, progress int) *ListEntry {
		return &ListEntry{MediaID: 1, Status: status, Progress: progress}
	}
	type want struct {
		ok        bool
		status    string
		progress  int
		repeat    int // 0: not changed
		started   bool
		completed bool
	}
	cases := []struct {
		name    string
		entry   *ListEntry
		episode int
		total   int
		want    want
	}{
		{"not in list", nil, 1, 12, want{ok: true, status: "CURRENT", progress: 1, started: true}},
		{"not in list, last episode", nil, 12, 12, want{ok: true, status: "COMPLETED", progress: 12, started: true, completed: true}},
		{"planned show starts", e("PLANNING", 0), 3, 12, want{ok: true, status: "CURRENT", progress: 3, started: true}},
		{"planned show keeps its progress", e("PLANNING", 5), 2, 12, want{ok: true, status: "CURRENT", progress: 5, started: true}},
		{"planned show, same episode", e("PLANNING", 5), 5, 12, want{ok: true, status: "CURRENT", progress: 5, started: true}},
		{"planned show, last episode", e("PLANNING", 5), 12, 12, want{ok: true, status: "COMPLETED", progress: 12, started: true, completed: true}},
		{"watching, next episode", e("CURRENT", 5), 6, 12, want{ok: true, status: "CURRENT", progress: 6}},
		{"watching, first episode", e("CURRENT", 0), 1, 12, want{ok: true, status: "CURRENT", progress: 1, started: true}},
		{"watching, same episode", e("CURRENT", 5), 5, 12, want{}},
		{"watching, earlier episode", e("CURRENT", 5), 3, 12, want{}},
		{"watching, last episode", e("CURRENT", 11), 12, 12, want{ok: true, status: "COMPLETED", progress: 12, completed: true}},
		{"past the last episode", e("CURRENT", 11), 14, 12, want{ok: true, status: "COMPLETED", progress: 12, completed: true}},
		{"unknown total never completes", e("CURRENT", 30), 31, 0, want{ok: true, status: "CURRENT", progress: 31}},
		{"paused show resumes", e("PAUSED", 2), 3, 12, want{ok: true, status: "CURRENT", progress: 3}},
		{"dropped show, earlier episode", e("DROPPED", 4), 2, 12, want{}},
		{"completed show, rewatch starts", e("COMPLETED", 12), 1, 12, want{ok: true, status: "REPEATING", progress: 1}},
		{"completed show, last episode", e("COMPLETED", 12), 12, 12, want{}},
		{"completed show, unknown total", e("COMPLETED", 12), 3, 0, want{}},
		{"rewatching, next episode", e("REPEATING", 3), 4, 12, want{ok: true, status: "REPEATING", progress: 4}},
		{"rewatching, earlier episode", e("REPEATING", 3), 2, 12, want{}},
		{"rewatch finished", &ListEntry{MediaID: 1, Status: "REPEATING", Progress: 11, Repeat: 1}, 12, 12,
			want{ok: true, status: "COMPLETED", progress: 12, repeat: 2, completed: true}},
		{"start date kept", &ListEntry{MediaID: 1, Status: "PLANNING", StartedAt: someDate}, 1, 12,
			want{ok: true, status: "CURRENT", progress: 1}},
		{"finish date kept", &ListEntry{MediaID: 1, Status: "REPEATING", Progress: 11, Repeat: 1, CompletedAt: someDate}, 12, 12,
			want{ok: true, status: "COMPLETED", progress: 12, repeat: 2}},
		{"no episode", nil, 0, 12, want{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := progressUpdate(tc.entry, 1, tc.episode, tc.total, now)
			if ok != tc.want.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.want.ok)
			}
			if !ok {
				return
			}
			if u.MediaID != 1 || u.Status == nil || u.Progress == nil {
				t.Fatalf("incomplete update %+v", u)
			}
			if *u.Status != tc.want.status || *u.Progress != tc.want.progress {
				t.Errorf("got %s at %d, want %s at %d", *u.Status, *u.Progress, tc.want.status, tc.want.progress)
			}
			repeat := 0
			if u.Repeat != nil {
				repeat = *u.Repeat
			}
			if repeat != tc.want.repeat {
				t.Errorf("repeat = %d, want %d", repeat, tc.want.repeat)
			}
			if (u.StartedAt != nil) != tc.want.started {
				t.Errorf("start date set = %v, want %v", u.StartedAt != nil, tc.want.started)
			}
			if (u.CompletedAt != nil) != tc.want.completed {
				t.Errorf("finish date set = %v, want %v", u.CompletedAt != nil, tc.want.completed)
			}
			for _, d := range []*FuzzyDate{u.StartedAt, u.CompletedAt} {
				if d != nil && (*d.Year != 2026 || *d.Month != 10 || *d.Day != 6) {
					t.Errorf("date = %d-%d-%d, want today", *d.Year, *d.Month, *d.Day)
				}
			}
		})
	}
}

// End to end on the local list: watching an earlier episode of a planned
// show with recorded progress starts it without losing that progress.
func TestUpdateProgressLocalList(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	m := testMedia(1, 12)
	p.db.SetCache(liteKey(1), m, time.Hour)
	addLocal(t, p, m, "PLANNING", 5)

	if ok, err := p.UpdateProgress(ctx, 1, 2); err != nil || !ok {
		t.Fatalf("UpdateProgress = %v, %v", ok, err)
	}
	if r := readLocal(t, p, 1); r.Status != "CURRENT" || r.Progress != 5 {
		t.Fatalf("after episode 2: %+v, want CURRENT at 5", r)
	}
	if ok, err := p.UpdateProgress(ctx, 1, 4); err != nil || ok {
		t.Fatalf("an earlier episode changed the list: %v, %v", ok, err)
	}
	if ok, err := p.UpdateProgress(ctx, 1, 12); err != nil || !ok {
		t.Fatalf("UpdateProgress = %v, %v", ok, err)
	}
	if r := readLocal(t, p, 1); r.Status != "COMPLETED" || r.Progress != 12 {
		t.Fatalf("after the last episode: %+v, want COMPLETED at 12", r)
	}
}

func TestWatchingUpdate(t *testing.T) {
	now := time.Date(2026, time.October, 6, 20, 30, 0, 0, time.Local)
	year := 2025
	someDate := FuzzyDate{Year: &year}
	e := func(status string, progress int) *ListEntry {
		return &ListEntry{MediaID: 1, Status: status, Progress: progress}
	}
	cases := []struct {
		name    string
		entry   *ListEntry
		ok      bool // moved to CURRENT
		started bool // start date set
	}{
		{"not in list", nil, true, true},
		{"planned show", e("PLANNING", 0), true, true},
		{"planned show with progress", e("PLANNING", 5), true, true},
		{"paused show", e("PAUSED", 3), true, false},
		{"paused before the first episode", e("PAUSED", 0), true, true},
		{"dropped show", e("DROPPED", 4), true, false},
		{"start date kept", &ListEntry{MediaID: 1, Status: "PLANNING", StartedAt: someDate}, true, false},
		{"watching", e("CURRENT", 2), false, false},
		{"watching, nothing watched yet", e("CURRENT", 0), false, false},
		{"rewatching", e("REPEATING", 3), false, false},
		{"completed", e("COMPLETED", 12), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := watchingUpdate(tc.entry, 1, now)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if u.MediaID != 1 || u.Status == nil || *u.Status != "CURRENT" {
				t.Fatalf("update %+v, want status CURRENT", u)
			}
			if u.Progress != nil || u.Score != nil || u.Repeat != nil || u.CompletedAt != nil {
				t.Errorf("update %+v changes more than the status and start date", u)
			}
			if (u.StartedAt != nil) != tc.started {
				t.Errorf("start date set = %v, want %v", u.StartedAt != nil, tc.started)
			}
			if d := u.StartedAt; d != nil && (*d.Year != 2026 || *d.Month != 10 || *d.Day != 6) {
				t.Errorf("start date = %d-%d-%d, want today", *d.Year, *d.Month, *d.Day)
			}
		})
	}
}

// End to end on the local list: starting a show puts it on the watching
// list keeping its progress, and tells the UI; a completed one stays so.
func TestStartWatchingLocalList(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	for id := 1; id <= 3; id++ {
		p.db.SetCache(liteKey(id), testMedia(id, 12), time.Hour)
	}
	addLocal(t, p, testMedia(1, 12), "PLANNING", 5)
	addLocal(t, p, testMedia(3, 12), "COMPLETED", 12)
	updates, unsubscribe := p.hub.Subscribe()
	defer unsubscribe()

	if ok, err := p.StartWatching(ctx, 1); err != nil || !ok {
		t.Fatalf("StartWatching(planned) = %v, %v", ok, err)
	}
	if r := readLocal(t, p, 1); r.Status != "CURRENT" || r.Progress != 5 {
		t.Fatalf("planned show after starting it: %+v, want CURRENT at 5", r)
	}
	select {
	case raw := <-updates:
		var ev struct {
			Type    string `json:"type"`
			Payload struct {
				MediaID int `json:"mediaId"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil || ev.Type != events.CollectionUpdate || ev.Payload.MediaID != 1 {
			t.Errorf("event %s, want %s for media 1", raw, events.CollectionUpdate)
		}
	default:
		t.Error("no event: the UI wouldn't show the change")
	}
	if ok, err := p.StartWatching(ctx, 1); err != nil || ok {
		t.Fatalf("starting it again changed the list: %v, %v", ok, err)
	}

	if ok, err := p.StartWatching(ctx, 2); err != nil || !ok {
		t.Fatalf("StartWatching(not in list) = %v, %v", ok, err)
	}
	if r := readLocal(t, p, 2); r.Status != "CURRENT" || r.Progress != 0 {
		t.Fatalf("new entry: %+v, want CURRENT at 0", r)
	}

	if ok, err := p.StartWatching(ctx, 3); err != nil || ok {
		t.Fatalf("StartWatching(completed) = %v, %v; want no change", ok, err)
	}
	if r := readLocal(t, p, 3); r.Status != "COMPLETED" || r.Progress != 12 {
		t.Fatalf("completed show after starting it again: %+v", r)
	}
}

// Starting and finishing the last episode of a planned show, one after the
// other in either order (the player serializes them), completes it.
func TestStartWatchingAndFinishing(t *testing.T) {
	for _, startFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("start first %v", startFirst), func(t *testing.T) {
			p := newTestPlatform(t, nil)
			ctx := context.Background()
			movie := testMedia(1, 1)
			p.db.SetCache(liteKey(1), movie, time.Hour)
			addLocal(t, p, movie, "PLANNING", 0)

			steps := []func() error{
				func() error { _, err := p.StartWatching(ctx, 1); return err },
				func() error { _, err := p.UpdateProgress(ctx, 1, 1); return err },
			}
			if !startFirst {
				steps[0], steps[1] = steps[1], steps[0]
			}
			for _, step := range steps {
				if err := step(); err != nil {
					t.Fatal(err)
				}
			}
			if r := readLocal(t, p, 1); r.Status != "COMPLETED" || r.Progress != 1 {
				t.Fatalf("got %+v, want COMPLETED at 1", r)
			}
		})
	}
}

// Logged in: a show the cached list has as watching, rewatching or completed
// costs no AniList request, even when the cache is due for a refresh. One
// that isn't watched yet is saved as watching with only its status and
// start date, and the cached list shows it.
func TestStartWatchingAniList(t *testing.T) {
	var (
		mu sync.Mutex
		// The list as AniList has it: media id -> status, progress.
		statuses = map[int]string{1: "CURRENT", 2: "REPEATING", 3: "COMPLETED", 4: "PLANNING"}
		progress = map[int]int{1: 3, 2: 4, 3: 12, 4: 2}
		ops      []string
		saves    []map[string]any
	)
	// list returns the list as AniList has it; mu must be held.
	list := func() *Collection {
		var entries []*ListEntry
		for id := 1; id <= 5; id++ {
			if s, ok := statuses[id]; ok {
				entries = append(entries, listEntry(100+id, id, s, progress[id]))
			}
		}
		return testCollection(entries...)
	}
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, op(req.Query))
		switch op(req.Query) {
		case "collection":
			reply(w, http.StatusOK, data(map[string]any{"MediaListCollection": list()}))
		case "save":
			saves = append(saves, req.Variables)
			id := int(req.Variables["mediaId"].(float64))
			statuses[id] = req.Variables["status"].(string)
			reply(w, http.StatusOK, data(map[string]any{"SaveMediaListEntry": map[string]any{
				"id": 100 + id, "mediaId": id, "status": statuses[id], "progress": progress[id]}}))
		default:
			t.Errorf("unexpected request: %s", op(req.Query))
		}
	})
	requests := func() ([]string, []map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(ops), slices.Clone(saves)
	}
	setLoggedIn(p, 7)
	mu.Lock()
	seed(p, "ANIME", list())
	mu.Unlock()
	p.mu.Lock()
	p.fetchedAt["ANIME"] = time.Now().Add(-time.Hour) // due for a refresh
	p.mu.Unlock()
	p.db.SetCache(liteKey(5), testMedia(5, 24), time.Hour)
	ctx := context.Background()

	for id := 1; id <= 3; id++ {
		if ok, err := p.StartWatching(ctx, id); err != nil || ok {
			t.Fatalf("StartWatching(%d) = %v, %v; want no change", id, ok, err)
		}
	}
	if got, _ := requests(); len(got) != 0 {
		t.Fatalf("AniList was asked %v about shows already on the list as watched", got)
	}

	// Planned (4) and not in the list (5): the list is refetched (it's due),
	// then the entry saved.
	for _, id := range []int{4, 5} {
		if ok, err := p.StartWatching(ctx, id); err != nil || !ok {
			t.Fatalf("StartWatching(%d) = %v, %v", id, ok, err)
		}
	}
	gotOps, saved := requests()
	if want := []string{"collection", "save", "collection", "save"}; !reflect.DeepEqual(gotOps, want) {
		t.Fatalf("requests %v, want %v", gotOps, want)
	}
	for _, vars := range saved {
		if vars["status"] != "CURRENT" {
			t.Errorf("saved %v, want status CURRENT", vars)
		}
		if _, ok := vars["progress"]; ok {
			t.Errorf("saved %v: the progress must be kept as is", vars)
		}
		if d, _ := vars["startedAt"].(map[string]any); d == nil || d["year"] == nil {
			t.Errorf("saved %v, want a start date", vars)
		}
	}
	c := cached(p, "ANIME")
	if e := c.Find(4); e == nil || e.Status != "CURRENT" || e.Progress != 2 {
		t.Errorf("cached entry of the planned show = %+v, want CURRENT at 2", e)
	}
	if e := c.Find(5); e == nil || e.Status != "CURRENT" || e.Progress != 0 {
		t.Errorf("cached entry of the new show = %+v, want CURRENT at 0", e)
	}

	// Watching them again: the cached list has them as watching now.
	for _, id := range []int{4, 5} {
		if ok, err := p.StartWatching(ctx, id); err != nil || ok {
			t.Fatalf("StartWatching(%d) again = %v, %v; want no change", id, ok, err)
		}
	}
	if got, _ := requests(); len(got) != len(gotOps) {
		t.Errorf("AniList was asked %v after the shows were on the list as watching", got[len(gotOps):])
	}
}

// A local update only changes the fields it carries.
func TestLocalUpdateKeepsOtherFields(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	p.db.SetCache(liteKey(1), testMedia(1, 12), time.Hour)

	score := 80.0
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: 1, Score: &score}); err != nil {
		t.Fatal(err)
	}
	if r := readLocal(t, p, 1); r != (localRow{Status: "PLANNING", Score: 80}) {
		t.Fatalf("new entry = %+v", r)
	}
	status, progress, repeat := "CURRENT", 3, 1
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: 1, Status: &status, Progress: &progress}); err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateEntry(ctx, EntryUpdate{MediaID: 1, Repeat: &repeat}); err != nil {
		t.Fatal(err)
	}
	if r := readLocal(t, p, 1); r != (localRow{Status: "CURRENT", Progress: 3, Score: 80, Repeat: 1}) {
		t.Fatalf("updated entry = %+v", r)
	}
	c, err := p.Collection(ctx, "ANIME", false)
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Find(1); e == nil || e.Status != "CURRENT" || e.Media == nil || e.Media.ID != 1 {
		t.Fatalf("local collection entry = %+v", e)
	}
}

// ---------------------------------------------------------------------------
// Schedule

func TestScheduleDoesNotCachePartialResults(t *testing.T) {
	var failSecondPage atomic.Bool
	failSecondPage.Store(true)
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		page := int(req.Variables["page"].(float64))
		if page == 2 && failSecondPage.Load() {
			reply(w, http.StatusOK, map[string]any{"data": nil, "errors": []any{map[string]any{"message": "boom", "status": 500}}})
			return
		}
		reply(w, http.StatusOK, data(map[string]any{"Page": map[string]any{
			"pageInfo":        map[string]any{"hasNextPage": page == 1, "currentPage": page},
			"airingSchedules": []any{map[string]any{"id": page, "episode": page, "airingAt": 1000 + page}},
		}}))
	})
	ctx := context.Background()
	res, err := p.Schedule(ctx, 0, 7200)
	if err != nil || len(res) != 1 {
		t.Fatalf("with page 2 failing: %d results, %v; want the first page", len(res), err)
	}
	failSecondPage.Store(false)
	res, err = p.Schedule(ctx, 0, 7200)
	if err != nil || len(res) != 2 {
		t.Fatalf("got %d results, %v; want 2 (the partial schedule must not be cached)", len(res), err)
	}
}
