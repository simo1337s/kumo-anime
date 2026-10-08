package anilist

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// discoverSection names the Discover row a search request is for.
func discoverSection(vars map[string]any) string {
	sort, _ := vars["sort"].([]any)
	first, _ := sort[0].(string)
	season, _ := vars["season"].(string)
	switch {
	case first == "TRENDING_DESC":
		return "trending"
	case first == "SCORE_DESC":
		return "topRated"
	case season == "":
		return "popular"
	}
	if s, _ := CurrentSeason(time.Now()); season == s {
		return "thisSeason"
	}
	return "nextSeason"
}

// fakeDiscover answers Discover's searches with one anime per row whose ID
// tells the row (and the round: 100s), counting the requests. Rows in fail
// get an AniList error.
type fakeDiscover struct {
	requests atomic.Int32
	round    atomic.Int32
	delay    time.Duration
	mu       sync.Mutex
	fail     map[string]bool
}

var discoverRows = map[string]int{"trending": 1, "thisSeason": 2, "nextSeason": 3, "popular": 4, "topRated": 5}

func (f *fakeDiscover) handle(w http.ResponseWriter, req gqlRequest) {
	if op(req.Query) != "search" {
		reply(w, http.StatusBadRequest, nil)
		return
	}
	f.requests.Add(1)
	time.Sleep(f.delay)
	row := discoverSection(req.Variables)
	f.mu.Lock()
	failed := f.fail[row]
	f.mu.Unlock()
	if failed {
		reply(w, http.StatusOK, map[string]any{"data": nil, "errors": []any{map[string]any{"message": "boom", "status": 500}}})
		return
	}
	id := int(f.round.Load())*100 + discoverRows[row]
	reply(w, http.StatusOK, data(map[string]any{"Page": map[string]any{
		"pageInfo": map[string]any{"currentPage": 1},
		"media":    []any{map[string]any{"id": id}},
	}}))
}

func ids(rows ...[]*Media) []int {
	var out []int
	for _, r := range rows {
		for _, m := range r {
			out = append(out, m.ID)
		}
	}
	return out
}

func checkRows(t *testing.T, d *Discover, round int) {
	t.Helper()
	got := ids(d.Trending, d.ThisSeason, d.NextSeason, d.Popular, d.TopRated)
	want := []int{round*100 + 1, round*100 + 2, round*100 + 3, round*100 + 4, round*100 + 5}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	}
}

// saveDiscover stores a complete Discover page of the given round, fetched
// age ago, for the given season.
func saveDiscover(p *Platform, round int, age time.Duration, season string, year int) {
	m := func(row int) []*Media { return []*Media{{ID: round*100 + row}} }
	p.db.SetCache(discoverKey, &Discover{
		Trending: m(1), ThisSeason: m(2), NextSeason: m(3), Popular: m(4), TopRated: m(5),
		Season: season, Year: year, FetchedAt: time.Now().Add(-age).Unix(),
	}, discoverKeep)
}

// waitRefreshed waits for the background refresh to end.
func waitRefreshed(t *testing.T, p *Platform) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for p.discovering.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the refresh didn't end")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDiscoverFetchesRowsAtOnceAndKeepsThem(t *testing.T) {
	f := &fakeDiscover{delay: 150 * time.Millisecond}
	p := newTestPlatform(t, f.handle)

	start := time.Now()
	d, err := p.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// One after the other they'd take 5 × 150ms.
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("Discover took %v: the rows weren't fetched at once", took)
	}
	checkRows(t, d, 0)
	if season, year := CurrentSeason(time.Now()); d.Season != season || d.Year != year {
		t.Errorf("season = %s %d, want %s %d", d.Season, d.Year, season, year)
	}
	if d.FetchedAt == 0 {
		t.Error("FetchedAt not set")
	}
	if n := f.requests.Load(); n != 5 {
		t.Errorf("%d requests, want 5", n)
	}

	// Opened again: no waiting, no requests.
	f.round.Store(1)
	start = time.Now()
	d, err = p.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("the saved Discover took %v", took)
	}
	checkRows(t, d, 0)
	if n := f.requests.Load(); n != 5 {
		t.Errorf("%d requests, want still 5", n)
	}
}

func TestDiscoverOpenedTogetherFetchesOnce(t *testing.T) {
	f := &fakeDiscover{delay: 50 * time.Millisecond}
	p := newTestPlatform(t, f.handle)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := p.Discover(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			checkRows(t, d, 0)
		}()
	}
	wg.Wait()
	if n := f.requests.Load(); n != 5 {
		t.Errorf("%d requests, want 5", n)
	}
}

func TestDiscoverShowsAnOldCopyAndRefreshesIt(t *testing.T) {
	f := &fakeDiscover{delay: 100 * time.Millisecond}
	f.round.Store(1)
	p := newTestPlatform(t, f.handle)
	season, year := CurrentSeason(time.Now())
	saveDiscover(p, 0, 2*time.Hour, season, year)

	// The old copy comes at once, however many times it's opened; one
	// refresh starts.
	for i := 0; i < 3; i++ {
		start := time.Now()
		d, err := p.Discover(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took > 50*time.Millisecond {
			t.Errorf("the old copy took %v", took)
		}
		checkRows(t, d, 0)
	}
	waitRefreshed(t, p)
	if n := f.requests.Load(); n != 5 {
		t.Errorf("%d requests, want one refresh (5)", n)
	}
	d, err := p.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, d, 1)
	if n := f.requests.Load(); n != 5 {
		t.Errorf("%d requests after the refresh, want still 5", n)
	}
}

func TestDiscoverIncompleteIsNotSaved(t *testing.T) {
	f := &fakeDiscover{fail: map[string]bool{"topRated": true}}
	p := newTestPlatform(t, f.handle)

	// Nothing saved: what came is shown...
	d, err := p.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(d.Trending, d.TopRated); len(got) != 1 || got[0] != 1 {
		t.Errorf("trending + top rated = %v, want [1]", got)
	}
	if d.TopRated == nil {
		t.Error("a failed row should be empty, not null")
	}
	// ...but not saved: the next one asks again.
	var saved Discover
	if p.db.GetStaleCache(discoverKey, &saved) {
		t.Error("an incomplete Discover was saved")
	}

	// An older complete copy beats an incomplete fresh one, and stays.
	season, year := CurrentSeason(time.Now())
	saveDiscover(p, 7, 2*time.Hour, season, year)
	f.round.Store(1)
	d, err = p.WarmDiscover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, d, 7)
	if !p.db.GetStaleCache(discoverKey, &saved) || saved.Trending[0].ID != 701 {
		t.Error("the older complete copy was replaced")
	}

	// AniList fixed: the next refresh saves it.
	f.mu.Lock()
	f.fail = nil
	f.mu.Unlock()
	if d, err = p.WarmDiscover(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkRows(t, d, 1)
}

func TestDiscoverUnreachable(t *testing.T) {
	f := &fakeDiscover{fail: map[string]bool{"trending": true, "thisSeason": true, "nextSeason": true, "popular": true, "topRated": true}}
	p := newTestPlatform(t, f.handle)
	if _, err := p.Discover(context.Background()); err == nil {
		t.Fatal("no error with AniList unreachable and nothing saved")
	}

	// Saved earlier: shown, old as it is.
	season, year := CurrentSeason(time.Now())
	saveDiscover(p, 3, 3*24*time.Hour, season, year)
	d, err := p.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, d, 3)
	waitRefreshed(t, p)
}

func TestDiscoverIgnoresLastSeasonsCopy(t *testing.T) {
	f := &fakeDiscover{}
	f.round.Store(2)
	p := newTestPlatform(t, f.handle)
	season, year := CurrentSeason(time.Now().AddDate(0, -3, 0))
	saveDiscover(p, 0, time.Minute, season, year)
	d, err := p.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, d, 2)

	// Too old to show, even in its season.
	season, year = CurrentSeason(time.Now())
	saveDiscover(p, 0, discoverKeep+time.Hour, season, year)
	f.round.Store(3)
	if d, err = p.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkRows(t, d, 3)
}

func TestCurrentSeason(t *testing.T) {
	for _, c := range []struct {
		date   string
		season string
		year   int
	}{
		{"2026-01-15", "WINTER", 2026},
		{"2026-03-01", "SPRING", 2026},
		{"2026-06-30", "SUMMER", 2026},
		{"2026-10-08", "FALL", 2026},
		{"2026-12-01", "WINTER", 2027},
	} {
		at, _ := time.Parse("2006-01-02", c.date)
		if s, y := CurrentSeason(at); s != c.season || y != c.year {
			t.Errorf("%s: %s %d, want %s %d", c.date, s, y, c.season, c.year)
		}
	}
}
