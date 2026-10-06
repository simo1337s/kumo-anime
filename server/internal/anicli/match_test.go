package anicli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/anilist"
)

func testMedia(id int, english, romaji string, episodes int) *anilist.Media {
	m := &anilist.Media{ID: id, Title: anilist.Title{English: english, Romaji: romaji}}
	if episodes > 0 {
		m.Episodes = &episodes
	}
	return m
}

const (
	cgTitle   = "Code Geass: Lelouch of the Rebellion"
	cgR2Title = "Code Geass: Lelouch of the Rebellion R2"
)

var codeGeassMedia = testMedia(1575, cgTitle, "Code Geass: Hangyaku no Lelouch", 25)

// hianime lists a new entry (a compilation movie) before the one Kumo
// remembered: the remembered position now holds another anime.
var codeGeassReordered = []string{
	entry("cg-initiation", "Code Geass: Lelouch of the Rebellion I - Initiation", 1),
	entry("code-geass", cgTitle, 25),
	entry("code-geass-r2", cgR2Title, 25),
}

func TestMatchFollowsTheTitleWhenResultsMove(t *testing.T) {
	h := newHarness(t, codeGeass[:2]...)
	ctx := context.Background()

	m, err := h.drv.Match(ctx, h.db, codeGeassMedia, "sub", false)
	if err != nil || m.Mapping == nil || m.Mapping.Index != 1 || m.Mapping.Title != cgTitle {
		t.Fatalf("match: %+v %v", m, err)
	}

	h.setCatalog(t, codeGeassReordered...)
	m, err = h.drv.Match(ctx, h.db, codeGeassMedia, "sub", false)
	if err != nil || m.Mapping == nil || m.Mapping.Index != 2 || m.Mapping.Title != cgTitle {
		t.Fatalf("match after reorder: %+v %v", m, err)
	}
	if saved := LoadMapping(h.db, codeGeassMedia.ID, "sub"); saved == nil || saved.Index != 2 || saved.Title != cgTitle {
		t.Fatalf("saved: %+v", saved)
	}
	eps, err := h.drv.Episodes(ctx, m.Mapping.Query, m.Mapping.Index, "sub")
	if err != nil || len(eps) != 25 {
		t.Fatalf("episodes: %q %v", eps, err)
	}
	st, err := h.drv.Resolve(ctx, m.Mapping.Query, m.Mapping.Index, "7", "sub", "")
	if err != nil || st.Title != cgTitle+" Episode 7" {
		t.Fatalf("resolve: %+v %v", st, err)
	}
}

func TestManualMatchFollowsItsTitle(t *testing.T) {
	h := newHarness(t, codeGeass[:2]...)
	ctx := context.Background()
	manual := &Mapping{Query: "lelouch", Index: 2, Title: cgR2Title, Score: 1, Manual: true}
	if err := SaveMapping(h.db, codeGeassMedia.ID, "sub", manual); err != nil {
		t.Fatal(err)
	}

	h.setCatalog(t, codeGeassReordered...)
	m, err := h.drv.Match(ctx, h.db, codeGeassMedia, "sub", false)
	want := Mapping{Query: "lelouch", Index: 3, Title: cgR2Title, Score: 1, Manual: true}
	if err != nil || m.Mapping == nil || *m.Mapping != want {
		t.Fatalf("match: %+v %v", m, err)
	}
	if saved := LoadMapping(h.db, codeGeassMedia.ID, "sub"); saved == nil || *saved != want {
		t.Fatalf("saved: %+v", saved)
	}

	// The entry is gone: say so rather than play whatever is at its place.
	h.setCatalog(t, codeGeassReordered[:2]...)
	m, err = h.drv.Match(ctx, h.db, codeGeassMedia, "sub", false)
	if !errors.Is(err, ErrMatchGone) || !strings.Contains(err.Error(), cgR2Title) {
		t.Fatalf("match without the entry: %+v %v", m, err)
	}
	if saved := LoadMapping(h.db, codeGeassMedia.ID, "sub"); saved == nil || *saved != want {
		t.Fatalf("the manual match was changed: %+v", saved)
	}
}

// "Refresh episode list" re-reads the episodes of a match the user picked; it
// does not replace it with the automatic match.
func TestRefreshKeepsManualMatch(t *testing.T) {
	h := newHarness(t, codeGeass[:2]...)
	ctx := context.Background()
	manual := &Mapping{Query: "lelouch", Index: 2, Title: cgR2Title, Score: 1, Manual: true}
	if err := SaveMapping(h.db, codeGeassMedia.ID, "sub", manual); err != nil {
		t.Fatal(err)
	}
	for _, refresh := range []bool{true, false} {
		m, err := h.drv.Match(ctx, h.db, codeGeassMedia, "sub", refresh)
		if err != nil || m.Mapping == nil || *m.Mapping != *manual {
			t.Fatalf("refresh=%v: %+v %v", refresh, m, err)
		}
	}
	if saved := LoadMapping(h.db, codeGeassMedia.ID, "sub"); saved == nil || *saved != *manual {
		t.Fatalf("saved: %+v", saved)
	}

	// A manual match whose auto-match alternative scores badly stays too.
	other := testMedia(99, "Something Else Entirely", "", 0)
	if err := SaveMapping(h.db, other.ID, "sub", manual); err != nil {
		t.Fatal(err)
	}
	m, err := h.drv.Match(ctx, h.db, other, "sub", true)
	if err != nil || m.Mapping == nil || *m.Mapping != *manual {
		t.Fatalf("low score: %+v %v", m, err)
	}
}

// An automatic match is matched again on refresh, and when its entry is gone.
func TestAutoMatchIsRematched(t *testing.T) {
	h := newHarness(t, codeGeass[:2]...)
	ctx := context.Background()
	stale := &Mapping{Query: cgTitle, Index: 2, Title: cgR2Title, Score: 0.8}
	if err := SaveMapping(h.db, codeGeassMedia.ID, "sub", stale); err != nil {
		t.Fatal(err)
	}
	m, err := h.drv.Match(ctx, h.db, codeGeassMedia, "sub", true)
	if err != nil || m.Mapping == nil || m.Mapping.Title != cgTitle || m.Mapping.Index != 1 || m.Mapping.Manual {
		t.Fatalf("refresh: %+v %v", m, err)
	}

	// The site renamed the entry.
	h.setCatalog(t,
		entry("code-geass", "Code Geass: Lelouch of the Rebellion (TV)", 25),
		entry("code-geass-r2", cgR2Title, 25),
	)
	m, err = h.drv.Match(ctx, h.db, codeGeassMedia, "sub", false)
	if err != nil || m.Mapping == nil || m.Mapping.Title != "Code Geass: Lelouch of the Rebellion (TV)" || m.Mapping.Index != 1 {
		t.Fatalf("renamed: %+v %v", m, err)
	}
	if saved := LoadMapping(h.db, codeGeassMedia.ID, "sub"); saved == nil || saved.Title != m.Mapping.Title {
		t.Fatalf("saved: %+v", saved)
	}
}

// When refreshing finds nothing better, the remembered automatic match, which
// Sources keeps playing, is what the episode list shows.
func TestRefreshWithoutBetterMatch(t *testing.T) {
	h := newHarness(t, codeGeass[:2]...)
	media := testMedia(7, "Zzyzx Unrelated", "", 0)
	auto := &Mapping{Query: "lelouch", Index: 2, Title: cgR2Title, Score: 0.7}
	if err := SaveMapping(h.db, media.ID, "sub", auto); err != nil {
		t.Fatal(err)
	}
	h.setCatalog(t, codeGeassReordered...)
	m, err := h.drv.Match(context.Background(), h.db, media, "sub", true)
	want := Mapping{Query: "lelouch", Index: 3, Title: cgR2Title, Score: 0.7}
	if err != nil || m.Mapping == nil || *m.Mapping != want {
		t.Fatalf("match: %+v %v", m, err)
	}
	if saved := LoadMapping(h.db, media.ID, "sub"); saved == nil || *saved != want {
		t.Fatalf("saved: %+v", saved)
	}
}

// Before single-episode entries were handled, a movie that was the only
// result of its search was saved under the query as its title.
func TestOldSingleResultMatch(t *testing.T) {
	h := newHarness(t, entry("kimi-no-na-wa", "Your Name.", 1))
	media := testMedia(21519, "Your Name.", "Kimi no Na wa.", 1)
	old := &Mapping{Query: "your name", Index: 1, Title: "your name", Score: 1, Manual: true}
	if err := SaveMapping(h.db, media.ID, "sub", old); err != nil {
		t.Fatal(err)
	}
	m, err := h.drv.Match(context.Background(), h.db, media, "sub", false)
	want := Mapping{Query: "your name", Index: 1, Title: "Your Name.", Score: 1, Manual: true}
	if err != nil || m.Mapping == nil || *m.Mapping != want {
		t.Fatalf("match: %+v %v", m, err)
	}
	if saved := LoadMapping(h.db, media.ID, "sub"); saved == nil || *saved != want {
		t.Fatalf("saved: %+v", saved)
	}

	// The search's only result is now another movie.
	if err := SaveMapping(h.db, media.ID, "sub", old); err != nil {
		t.Fatal(err)
	}
	h.setCatalog(t, entry("other", "The Name of the Wind Is Yours", 1))
	if m, err := h.drv.Match(context.Background(), h.db, media, "sub", false); !errors.Is(err, ErrMatchGone) {
		t.Fatalf("match: %+v %v", m, err)
	}
}

func TestMatchBracketedTitle(t *testing.T) {
	h := newHarness(t,
		entry("oshi-no-ko", "[Oshi no Ko]", 11),
		entry("oshi-no-ko-2", "[Oshi no Ko] Season 2", 13),
	)
	m, err := h.drv.Match(context.Background(), h.db, testMedia(150672, "[Oshi no Ko]", "[Oshi no Ko]", 11), "sub", false)
	if err != nil || m.Mapping == nil || m.Mapping.Title != "[Oshi no Ko]" || m.Mapping.Index != 1 {
		t.Fatalf("match: %+v %v", m, err)
	}
}

// A movie is the only result of its search: ani-cli plays it right away.
func TestMatchMovie(t *testing.T) {
	h := newHarness(t, entry("kimi-no-na-wa", "Your Name.", 1), entry("other", "Weathering With You", 1))
	media := testMedia(21519, "Your Name.", "Kimi no Na wa.", 1)
	m, err := h.drv.Match(context.Background(), h.db, media, "sub", false)
	if err != nil || m.Mapping == nil || m.Mapping.Title != "Your Name." || m.Mapping.Index != 1 || m.Mapping.Score < 0.9 {
		t.Fatalf("match: %+v %v", m, err)
	}
	eps, err := h.drv.Episodes(context.Background(), m.Mapping.Query, m.Mapping.Index, "sub")
	if err != nil || len(eps) != 1 || eps[0] != "1" {
		t.Fatalf("episodes: %q %v", eps, err)
	}
}

func TestSearchCache(t *testing.T) {
	d := &Driver{searchTTL: time.Minute}
	d.searches = map[string]cachedSearch{"sub\x00Frieren": {res: []Result{{Index: 1, Title: "Sousou no Frieren", Episodes: 28}}, at: time.Now()}}
	// A cached search doesn't run ani-cli (there is none configured here).
	res, err := d.Search(context.Background(), "  Frieren ", "sub")
	if err != nil || len(res) != 1 || res[0].Title != "Sousou no Frieren" {
		t.Fatalf("cached search: %v %v", res, err)
	}
	// Callers may change the returned slice without touching the cache.
	res[0].Title = "changed"
	if again, _ := d.Search(context.Background(), "Frieren", "sub"); again[0].Title != "Sousou no Frieren" {
		t.Fatal("cache was modified through a returned slice")
	}
}
