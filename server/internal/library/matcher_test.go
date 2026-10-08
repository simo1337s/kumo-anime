package library

import (
	"context"
	"errors"
	"testing"

	"github.com/simo1337s/animetest/server/internal/anilist"
)

func testMedia(id int, romaji, english string) *anilist.Media {
	m := &anilist.Media{ID: id, Format: "TV"}
	m.Title.Romaji, m.Title.English = romaji, english
	return m
}

// A sequel whose title only adds "R2" (or "0", "II"…) to the first season's
// looked 0.95 alike, so it was matched to the first season on the user's
// list without even searching AniList for the sequel.
func TestSequelsMatchTheirOwnEntry(t *testing.T) {
	s1 := testMedia(1575, "Code Geass: Hangyaku no Lelouch", "Code Geass: Lelouch of the Rebellion")
	r2 := testMedia(2904, "Code Geass: Hangyaku no Lelouch R2", "Code Geass: Lelouch of the Rebellion R2")
	score := func(g *group, pool ...*anilist.Media) candidateScore {
		return scoreCandidates(buildQueries(g), pool, g)
	}

	r2Files := &group{title: "Code Geass Hangyaku no Lelouch R2", folderTitle: "Code Geass Hangyaku no Lelouch R2", kind: "main"}
	if b := score(r2Files, s1); b.score >= 0.5 {
		t.Errorf("R2 files vs season 1 alone: %.2f, want below the match threshold (and a search of AniList)", b.score)
	}
	if b := score(r2Files, s1, r2); b.media != r2 || b.score < 0.92 {
		t.Errorf("R2 files matched %v (%.2f), want R2", b.media.Title.Romaji, b.score)
	}

	s1Files := &group{title: "Code Geass - Lelouch of the Rebellion", folderTitle: "Code Geass Lelouch of the Rebellion", kind: "main"}
	if b := score(s1Files, s1, r2); b.media != s1 {
		t.Errorf("season 1 files matched %v, want season 1", b.media.Title.Romaji)
	}
	if b := score(s1Files, r2); b.score >= 0.92 {
		t.Errorf("season 1 files vs R2 alone: %.2f, want AniList searched for a better match", b.score)
	}

	sg := testMedia(9253, "Steins;Gate", "")
	sg0 := testMedia(21127, "Steins;Gate 0", "")
	if b := score(&group{title: "Steins Gate 0", folderTitle: "Steins Gate 0", kind: "main"}, sg, sg0); b.media != sg0 {
		t.Errorf("Steins;Gate 0 files matched %v", b.media.Title.Romaji)
	}
	mp := testMedia(21507, "Mob Psycho 100", "")
	mp2 := testMedia(101338, "Mob Psycho 100 II", "")
	if b := score(&group{title: "Mob Psycho 100 II", kind: "main"}, mp, mp2); b.media != mp2 {
		t.Errorf("Mob Psycho 100 II files matched %v", b.media.Title.Romaji)
	}

	// A season the name gives ("S2") is scored by the queries as before: a
	// second season whose titles carry no number still matches.
	kg := testMedia(101921, "Kaguya-sama wa Kokurasetai: Tensai-tachi no Renai Zunousen", "Kaguya-sama: Love is War")
	kg2 := testMedia(112641, "Kaguya-sama wa Kokurasetai? Tensai-tachi no Renai Zunousen", "Kaguya-sama: Love is War Season 2")
	if b := score(&group{title: "Kaguya-sama Love is War", folderTitle: "Kaguya-sama Love is War", season: 2, kind: "main"}, kg, kg2); b.media != kg2 {
		t.Errorf("Kaguya-sama S2 files matched %v", b.media.Title.Romaji)
	}
}

// When two files are matched to one episode (a sequel's wrongly matched to
// the first season, say), the one played is the hand-matched one, then the
// surer match, then the bigger file.
func TestEpisodeFile(t *testing.T) {
	s1 := &LocalFile{Path: "/a/[Lulu] Code Geass/11.mkv", Kind: "main", Episode: 11, MatchScore: 1, Size: 1}
	r2 := &LocalFile{Path: "/a/Code Geass R2/11.mkv", Kind: "main", Episode: 11, MatchScore: 0.96, Size: 2}
	if f := EpisodeFile([]*LocalFile{r2, s1}, 11); f != s1 {
		t.Errorf("got %s, want the surer match", f.Path)
	}
	r2.Locked = true
	if f := EpisodeFile([]*LocalFile{s1, r2}, 11); f != r2 {
		t.Errorf("got %s, want the match made by hand", f.Path)
	}
	if f := EpisodeFile([]*LocalFile{s1, r2}, 12); f != nil {
		t.Errorf("episode 12: %v", f)
	}
}

// Season 1's files, with only R2 on the list: AniList finds season 1. When
// AniList can't be searched (offline, rate limited), or matching is limited
// to the list, R2 isn't taken instead: watching season 1 would have marked
// R2's episodes watched.
func TestOtherSeasonNeverTakenForLackOfBetter(t *testing.T) {
	s1 := testMedia(1575, "Code Geass: Hangyaku no Lelouch", "Code Geass: Lelouch of the Rebellion")
	r2 := testMedia(2904, "Code Geass: Hangyaku no Lelouch R2", "Code Geass: Lelouch of the Rebellion R2")
	s1Files := &group{title: "Code Geass - Hangyaku no Lelouch", folderTitle: "Code Geass", kind: "main"}
	matcher := func(outsideList bool, search func(string) ([]*anilist.Media, error)) *Matcher {
		m := NewMatcher(nil, 0.5, outsideList)
		m.searchFn = func(_ context.Context, q string) ([]*anilist.Media, error) { return search(q) }
		return m
	}

	found := matcher(true, func(string) ([]*anilist.Media, error) { return []*anilist.Media{s1, r2}, nil })
	if b := found.bestMatch(context.Background(), s1Files, []*anilist.Media{r2}); b.media != s1 {
		t.Errorf("AniList answering: matched %v, want season 1", b.media)
	}

	down := matcher(true, func(string) ([]*anilist.Media, error) { return nil, errors.New("429 Too Many Requests") })
	if b := down.bestMatch(context.Background(), s1Files, []*anilist.Media{r2}); b.media != nil {
		t.Errorf("AniList not answering: matched %s, want unmatched (tried again next scan)", b.media.Title.Romaji)
	}
	if down.SearchFailures() == 0 {
		t.Error("the failed search isn't counted")
	}
	// A failure isn't remembered: the next file asks again.
	if _, ok := down.search(context.Background(), "Code Geass"); ok || len(down.searchCache) != 0 {
		t.Error("a failed search was cached")
	}

	listOnly := matcher(false, func(string) ([]*anilist.Media, error) {
		t.Error("searched AniList with matching limited to the list")
		return nil, nil
	})
	if b := listOnly.bestMatch(context.Background(), s1Files, []*anilist.Media{r2}); b.media != nil {
		t.Errorf("list only: matched %s, want unmatched", b.media.Title.Romaji)
	}
	// R2's own files still match R2 from the list, without a search.
	r2Files := &group{title: "Code Geass Hangyaku no Lelouch R2", folderTitle: "Code Geass R2", kind: "main"}
	if b := listOnly.bestMatch(context.Background(), r2Files, []*anilist.Media{s1, r2}); b.media != r2 {
		t.Errorf("R2 files: matched %v, want R2", b.media)
	}
}
