package util

import (
	"reflect"
	"testing"
)

func TestSequelMarkers(t *testing.T) {
	for title, want := range map[string][]string{
		"Code Geass: Hangyaku no Lelouch":          {},
		"Code Geass Hangyaku no Lelouch R2":        {"2"},
		"Shingeki no Kyojin Season 2":              {"2"},
		"Shingeki no Kyojin 2nd Season":            {"2"},
		"Shingeki no Kyojin: The Final Season":     {"final"},
		"Mob Psycho 100 II":                        {"100", "2"},
		"Steins;Gate 0":                            {"0"},
		"86 Eighty-Six":                            {"86"},
		"Hunter x Hunter (2011)":                   {},
		"Sword Art Online Season 1":                {},
		"Kimetsu no Yaiba Movie: Mugen Ressha-hen": {"movie"},
		"Re:Zero kara Hajimeru Isekai Seikatsu":    {},
		"Fate/Zero 2nd Season":                     {"2"},
		"Kaguya-sama S3":                           {"3"},
	} {
		if got := SequelMarkers(title); !reflect.DeepEqual(got, want) {
			t.Errorf("SequelMarkers(%q) = %v, want %v", title, got, want)
		}
	}
}

// File names from titles: never a path out of the folder, nor one of
// Windows' device names.
func TestSanitizeFilename(t *testing.T) {
	for in, want := range map[string]string{
		"Re:Zero":       "Re：Zero",
		"../../.bashrc": "_.._.bashrc",
		`..\..\win.ini`: "_.._win.ini",
		"/etc/passwd":   "_etc_passwd",
		"....":          "untitled",
		"CON":           "_CON",
		"nul.txt":       "_nul.txt",
		"Com1":          "_Com1",
		"Console Wars":  "Console Wars",
		"LPT10":         "LPT10",
	} {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
