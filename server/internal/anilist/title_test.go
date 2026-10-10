package anilist

import "testing"

func TestPreferredTitleLanguage(t *testing.T) {
	t.Cleanup(func() { SetTitleLanguage("english") })
	both := &Media{Title: Title{Romaji: "Shingeki no Kyojin", English: "Attack on Titan", Native: "進撃の巨人", UserPreferred: "Shingeki no Kyojin"}}
	romajiOnly := &Media{Title: Title{Romaji: "Mushishi", Native: "蟲師", UserPreferred: "Mushishi"}}
	nativeOnly := &Media{Title: Title{Native: "蟲師"}}

	for _, c := range []struct {
		lang string
		m    *Media
		want string
	}{
		{"english", both, "Attack on Titan"},
		{"english", romajiOnly, "Mushishi"}, // no English title on AniList
		{"english", nativeOnly, "蟲師"},
		{"romaji", both, "Shingeki no Kyojin"},
		{"romaji", nativeOnly, "蟲師"},
		{"", both, "Attack on Titan"}, // English unless romaji is chosen
	} {
		SetTitleLanguage(c.lang)
		if got := c.m.PreferredTitle(); got != c.want {
			t.Errorf("%q: %q, want %q", c.lang, got, c.want)
		}
	}

	// Folders keep their name whatever the language.
	for _, lang := range []string{"english", "romaji"} {
		SetTitleLanguage(lang)
		if got := both.FolderTitle(); got != "Shingeki no Kyojin" {
			t.Errorf("folder in %s: %q", lang, got)
		}
	}
}
