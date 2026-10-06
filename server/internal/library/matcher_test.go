package library

import (
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
