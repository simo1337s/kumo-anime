package torrent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
)

// newTestAutoDownloader returns an auto downloader with its own database,
// whose torrent client is the qBittorrent at srv. No executable is
// configured, so nothing is ever launched.
func newTestAutoDownloader(t *testing.T, srv *httptest.Server) *AutoDownloader {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	s := settings.Get()
	s.Torrent.DefaultClient = "qbittorrent"
	s.Qbittorrent = cfgFor(t, srv)
	s.Library.Dir = t.TempDir()
	if _, err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	return &AutoDownloader{db: d, torrents: NewManager(settings, hub), hub: hub}
}

func (a *AutoDownloader) markSeen(t *testing.T, mediaID, ep int) {
	t.Helper()
	if _, err := a.db.Write(`INSERT INTO autodownload_items(key, rule_id, title, created_at) VALUES(?, 1, 'x', 0)`, episodeKey(mediaID, ep)); err != nil {
		t.Fatal(err)
	}
}

func TestAutoDownloaderPick(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	a := newTestAutoDownloader(t, srv)
	a.markSeen(t, 42, 3)
	episodes := 12
	media := &anilist.Media{ID: 42, Episodes: &episodes, Title: anilist.Title{Romaji: "Kaiju No. 8"}}
	var results []*SearchResult
	for _, x := range []struct {
		name    string
		seeders int
	}{
		{"[SubsPlease] Kaiju No. 8 - 01 (1080p) [AAAA0001].mkv", 50},    // watched
		{"[SubsPlease] Kaiju No. 8 - 02 (1080p) [AAAA0002].mkv", 50},    // in the library
		{"[SubsPlease] Kaiju No. 8 - 03 (1080p) [AAAA0003].mkv", 50},    // grabbed before
		{"[SubsPlease] Kaiju No. 8 - 04 (1080p) [AAAA0004].mkv", 10},    // fewer seeders than v2
		{"[SubsPlease] Kaiju No. 8 - 04v2 (1080p) [AAAA0014].mkv", 30},  // picked
		{"[SubsPlease] Kaiju No. 8 - 05 (720p) [AAAA0005].mkv", 50},     // resolution
		{"[Erai-raws] Kaiju No. 8 - 05 [1080p][Multiple Subtitle]", 50}, // group
		{"[SubsPlease] Kaiju No. 8 - 05 (1080p) [AAAA0015].mkv", 0},     // no seeders
		{"[SubsPlease] Kaiju No. 8 - 06 (1080p) [AAAA0006].mkv", 5},     // picked (was taken for a batch)
		{"[SubsPlease] Kaiju No. 8 - 06.5 (1080p) [AAAA0016].mkv", 99},  // recap, not episode 6
		{"[SubsPlease] Kaiju No. 8 (01-12) (1080p) [Batch]", 99},
		{"[SubsPlease] Kaiju No. 8 - 13 (1080p) [AAAA0013].mkv", 99}, // beyond the episode count
		{"[SubsPlease] Ore dake Level Up na Ken - 07 (1080p) [AAAA0007].mkv", 99},
	} {
		r := &SearchResult{Name: x.name, Seeders: x.seeders}
		enrich(r)
		results = append(results, r)
	}
	rule := &Rule{ID: 1, MediaID: 42, ReleaseGroups: []string{"SubsPlease"}, Resolutions: []string{"1080"}, EpisodeType: "recent", MinSeeders: 1}
	best := a.pick(rule, media, "Kaiju No. 8", 1, map[int]bool{2: true}, results)
	if len(best) != 2 || best[4] == nil || !strings.Contains(best[4].Name, "04v2") || best[6] == nil || !strings.Contains(best[6].Name, "- 06 ") {
		for ep, r := range best {
			t.Logf("picked %d: %s", ep, r.Name)
		}
		t.Fatalf("picked %d releases, want episodes 4 (v2) and 6", len(best))
	}
	// "Every missing episode" ignores the progress.
	rule.EpisodeType = "all"
	if best := a.pick(rule, media, "Kaiju No. 8", 1, map[int]bool{2: true}, results); len(best) != 3 || best[1] == nil {
		t.Fatalf("all: picked %d releases", len(best))
	}
}

func TestEpisodeLimit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	twelve := 12
	for _, tc := range []struct {
		media anilist.Media
		want  int
	}{
		{anilist.Media{Episodes: &twelve}, 12},
		{anilist.Media{}, 0},
		{anilist.Media{NextAiringEpisode: &anilist.AiringEpisode{Episode: 6, AiringAt: int(now.Unix()) + 3600}}, 5},
		// Cached before episode 6 aired: it is out now.
		{anilist.Media{NextAiringEpisode: &anilist.AiringEpisode{Episode: 6, AiringAt: int(now.Unix()) - 3600}}, 6},
		{anilist.Media{NextAiringEpisode: &anilist.AiringEpisode{Episode: 6}}, 5},
		{anilist.Media{Episodes: &twelve, NextAiringEpisode: &anilist.AiringEpisode{Episode: 13, AiringAt: int(now.Unix()) - 3600}}, 12},
	} {
		if got := episodeLimit(&tc.media, now); got != tc.want {
			t.Errorf("%+v: limit %d, want %d", tc.media, got, tc.want)
		}
	}
}

// A torrent already in qBittorrent (409) is recorded, and a failing one
// doesn't stop the episodes after it.
func TestAutoDownloaderGrab(t *testing.T) {
	var mu sync.Mutex
	var adds []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.1.2")
		case "/api/v2/torrents/add":
			_ = r.ParseForm()
			u := r.Form.Get("urls")
			mu.Lock()
			adds = append(adds, u)
			mu.Unlock()
			switch {
			case strings.Contains(u, "dupe"):
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, "Fails.")
			case strings.Contains(u, "bad"):
				w.WriteHeader(http.StatusUnsupportedMediaType)
				_, _ = io.WriteString(w, "Torrent file is not valid.")
			default:
				_, _ = io.WriteString(w, "Ok.")
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	a := newTestAutoDownloader(t, srv)
	media := &anilist.Media{ID: 42, Title: anilist.Title{Romaji: "Kaiju No. 8"}}
	best := map[int]*SearchResult{
		6: {Name: "[SubsPlease] Kaiju No. 8 - 06 (1080p)", MagnetLink: "magnet:?xt=urn:btih:six"},
		3: {Name: "[SubsPlease] Kaiju No. 8 - 03 (1080p)", MagnetLink: "magnet:?xt=urn:btih:dupe3"},
		4: {Name: "[SubsPlease] Kaiju No. 8 - 04 (1080p)", MagnetLink: "magnet:?xt=urn:btih:bad4"},
		5: {Name: "[SubsPlease] Kaiju No. 8 - 05 (1080p)", MagnetLink: "magnet:?xt=urn:btih:five"},
		7: {Name: "[SubsPlease] Kaiju No. 8 - 07 (1080p)"}, // no magnet
	}
	n, err := a.grab(context.Background(), &Rule{ID: 1, MediaID: 42}, media, &Nyaa{}, best)
	if err != nil || n != 3 {
		t.Fatalf("grab: %d added, %v", n, err)
	}
	if strings.Join(adds, " ") != "magnet:?xt=urn:btih:dupe3 magnet:?xt=urn:btih:bad4 magnet:?xt=urn:btih:five magnet:?xt=urn:btih:six" {
		t.Fatalf("adds %v", adds)
	}
	for ep, want := range map[int]bool{3: true, 4: false, 5: true, 6: true, 7: false} {
		if got := a.seen(episodeKey(42, ep)); got != want {
			t.Errorf("episode %d recorded=%v, want %v", ep, got, want)
		}
	}
}

type countingProvider struct {
	Nyaa
	mu    sync.Mutex
	calls int
}

func (p *countingProvider) Magnet(_ context.Context, r *SearchResult) (string, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return r.MagnetLink, nil
}

// When the client is down the rule stops at the first episode instead of
// failing (and trying to start the client) once per episode.
func TestAutoDownloaderGrabClientDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	a := newTestAutoDownloader(t, srv)
	srv.Close()
	best := map[int]*SearchResult{
		1: {Name: "a", MagnetLink: "magnet:?xt=urn:btih:1"},
		2: {Name: "b", MagnetLink: "magnet:?xt=urn:btih:2"},
		3: {Name: "c", MagnetLink: "magnet:?xt=urn:btih:3"},
	}
	p := &countingProvider{}
	n, err := a.grab(context.Background(), &Rule{ID: 1, MediaID: 42}, &anilist.Media{ID: 42}, p, best)
	if err == nil || n != 0 || p.calls != 1 {
		t.Fatalf("client down: %d added, %d tries, %v", n, p.calls, err)
	}
	if a.seen(episodeKey(42, 1)) {
		t.Fatal("recorded an episode that wasn't added")
	}
}

// Unknown progress would make every missing episode look new.
func TestAutoDownloaderNeedsProgress(t *testing.T) {
	a := &AutoDownloader{}
	if _, err := a.runRule(context.Background(), &Rule{ID: 1, MediaID: 42, EpisodeType: "recent"}, nil); err == nil {
		t.Fatal("ran a 'recent' rule without the anime list")
	}
}
