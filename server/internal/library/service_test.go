package library

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/history"
	"github.com/simo1337s/animetest/server/internal/metadata"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return &Service{Store: NewStore(d), Meta: metadata.NewService(d), History: history.NewStore(d), DB: d}
}

// watching is a list of 12-episode anime being watched: media id → progress.
func watching(progress map[int]int) *anilist.Collection {
	twelve := 12
	l := &anilist.List{Name: "Watching", Status: "CURRENT"}
	for id, p := range progress {
		m := &anilist.Media{ID: id, Status: "FINISHED", Episodes: &twelve}
		l.Entries = append(l.Entries, &anilist.ListEntry{MediaID: id, Status: "CURRENT", Progress: p, Media: m})
	}
	return &anilist.Collection{Lists: []*anilist.List{l}}
}

func TestHideContinue(t *testing.T) {
	s := newTestService(t)
	// Cancelled: episode metadata lookups fail right away instead of going
	// to the network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// offered returns the episode offered for each anime.
	offered := func(progress map[int]int) map[int]int {
		out := map[int]int{}
		for _, it := range s.continueWatching(ctx, watching(progress), nil) {
			out[it.Media.ID] = it.Episode
		}
		return out
	}
	hide := func(id, ep int) {
		t.Helper()
		if err := s.HideContinue(id, ep); err != nil {
			t.Fatal(err)
		}
	}
	watch := func(id, ep int) {
		t.Helper()
		if err := s.History.Save(history.Entry{MediaID: id, Episode: ep, Position: 300, Duration: 1400}); err != nil {
			t.Fatal(err)
		}
	}

	list := map[int]int{1: 3, 2: 5}
	// Last watched of the first one: a rewatch of episode 2. Once hidden,
	// its history must not offer it again with that episode. (s has no
	// Platform, so even looking it up for that would panic.)
	watch(1, 2)
	if got := offered(list); len(got) != 2 || got[1] != 4 || got[2] != 6 {
		t.Fatalf("before hiding: %v", got)
	}
	hide(1, 4)
	if got := offered(list); len(got) != 1 || got[2] != 6 {
		t.Fatalf("hidden anime still offered: %v", got)
	}

	// Progress made elsewhere (another device, marked by hand): it offers
	// another episode, so it is back.
	if got := offered(map[int]int{1: 4, 2: 5}); got[1] != 5 {
		t.Fatalf("not back for the next episode: %v", got)
	}

	// Watching another anime doesn't bring it back, watching it does.
	watch(2, 6)
	if got := offered(list); got[1] != 0 {
		t.Fatalf("back after watching another anime: %v", got)
	}
	// Hidden a minute ago: the history has whole seconds, and watching in
	// the same second as hiding counts as before.
	_ = s.updateHidden(func(items map[int]hiddenItem) {
		it := items[1]
		it.HiddenAt -= 60
		items[1] = it
	})
	watch(1, 4)
	if got := offered(list); got[1] != 4 {
		t.Fatalf("not back after watching it again: %v", got)
	}

	// Undo. Hiding also forgets the anime that came back by being watched.
	hide(2, 6)
	if got := offered(list); got[2] != 0 {
		t.Fatalf("hidden anime still offered: %v", got)
	}
	if _, ok := s.hiddenContinue()[1]; ok {
		t.Fatalf("anime watched again is still stored: %v", s.hiddenContinue())
	}
	if err := s.UnhideContinue(2); err != nil {
		t.Fatal(err)
	}
	if got := offered(list); got[2] != 6 {
		t.Fatalf("not back after unhiding: %v", got)
	}
}

func TestHideContinueConcurrent(t *testing.T) {
	s := newTestService(t)
	var wg sync.WaitGroup
	for id := 1; id <= 20; id++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.HideContinue(id, 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(s.hiddenContinue()); n != 20 {
		t.Fatalf("%d of 20 hidden anime saved", n)
	}
}

// Files other Kumo apps share fill in the episodes this computer doesn't
// have; its own always win.
func TestSharedFilesFillIn(t *testing.T) {
	s := newTestService(t)
	local := []*LocalFile{
		{Path: "/a/Show - 01.mkv", Name: "Show - 01.mkv", MediaID: 5, Episode: 1, Kind: "main", Size: 100},
		{Path: "/a/Show - OVA.mkv", Name: "Show - OVA.mkv", MediaID: 5, Kind: "special"},
	}
	shared := []*LocalFile{
		{Path: "kumo://h//b/Show - 01.mkv", Name: "Show - 01.mkv", MediaID: 5, Episode: 1, Kind: "main", Size: 900, Host: "h", MatchScore: 1},
		{Path: "kumo://h//b/Show - 02.mkv", Name: "Show - 02.mkv", MediaID: 5, Episode: 2, Kind: "main", Host: "h"},
		{Path: "kumo://h//b/Show - OVA.mkv", Name: "Show - OVA.mkv", MediaID: 5, Kind: "special", Host: "h"},
		{Path: "kumo://h//b/Show - NCOP.mkv", Name: "Show - NCOP.mkv", MediaID: 5, Kind: "nc", Host: "h"},
		{Path: "kumo://h//b/Other - 01.mkv", Name: "Other - 01.mkv", MediaID: 6, Episode: 1, Kind: "main", Host: "h"},
	}
	s.Remote = func() []*LocalFile { return shared }

	got := map[string]bool{}
	for _, f := range s.sharedFor(5, local) {
		got[f.Name] = true
	}
	if !got["Show - 01.mkv"] || !got["Show - 02.mkv"] || !got["Show - NCOP.mkv"] || got["Show - OVA.mkv"] || got["Other - 01.mkv"] {
		t.Errorf("shared files of the anime: %v", got)
	}

	all := append(append([]*LocalFile{}, local...), s.sharedFor(5, local)...)
	if f := EpisodeFile(all, 1); f == nil || f.Host != "" {
		t.Errorf("episode 1 plays from %+v, want this computer's file", f)
	}
	if f := EpisodeFile(all, 2); f == nil || f.Host != "h" {
		t.Errorf("episode 2 plays from %+v, want the shared file", f)
	}

	items := s.continueWatching(context.Background(), watching(map[int]int{5: 1}), map[int][]*LocalFile{5: all})
	if len(items) != 1 || items[0].Episode != 2 || !items[0].HasFile || items[0].FilePath != "kumo://h//b/Show - 02.mkv" {
		t.Errorf("continue watching: %+v", items)
	}
}

// Continue watching follows the AniList account: an anime watched on
// another device (its list entry just updated) comes before one watched
// here long ago.
func TestContinueWatchingFollowsTheAccount(t *testing.T) {
	s := newTestService(t)
	coll := watching(map[int]int{5: 3, 6: 7})
	for _, e := range coll.Entries() {
		e.UpdatedAt = 1000
		if e.MediaID == 6 {
			e.UpdatedAt = time.Now().Unix() // an episode watched on another device
		}
	}
	if err := s.History.Save(history.Entry{MediaID: 5, Episode: 4, Position: 100, Duration: 1400, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	// Watched here, but before the other device.
	if _, err := s.DB.Write(`UPDATE watch_history SET updated_at = ?`, time.Now().Unix()-3600); err != nil {
		t.Fatal(err)
	}
	items := s.continueWatching(context.Background(), coll, nil)
	if len(items) != 2 || items[0].Media.ID != 6 || items[0].Episode != 8 || items[1].Media.ID != 5 {
		var got []int
		for _, it := range items {
			got = append(got, it.Media.ID)
		}
		t.Errorf("continue watching %v, want 6 (episode 8) first", got)
	}
}
