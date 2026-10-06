package util

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	reMarkerPrefixed = regexp.MustCompile(`^[rs](\d{1,2})$`) // "R2" (Code Geass R2), "S2"
	romanMarkers     = map[string]string{"ii": "2", "iii": "3", "iv": "4", "vi": "6", "vii": "7", "viii": "8", "ix": "9"}
	wordMarkers      = map[string]string{"final": "final", "movie": "movie", "film": "movie", "gekijouban": "movie", "gekijoban": "movie", "ova": "ova", "oad": "ova", "recap": "recap"}
)

// SequelMarkers returns what tells an entry of a franchise apart from the
// others in its title: numbers ("2", "2nd Season", "II", "R2", "S2", "0" of
// Steins;Gate 0) and words like "final", "movie" and "ova". Titles that only
// differ in these ("Code Geass: Hangyaku no Lelouch" and "… R2") look almost
// the same to Similarity, but aren't the same anime. A "1" doesn't count
// (first seasons leave it out), nor do years.
func SequelMarkers(title string) []string {
	set := map[string]bool{}
	for _, w := range strings.Fields(NormalizeTitle(title)) {
		m := ""
		switch {
		case wordMarkers[w] != "":
			m = wordMarkers[w]
		case romanMarkers[w] != "":
			m = romanMarkers[w]
		default:
			if s := reMarkerPrefixed.FindStringSubmatch(w); s != nil {
				w = s[1]
			}
			if n, err := strconv.Atoi(w); err == nil && !(n >= 1950 && n <= 2099) {
				m = strconv.Itoa(n)
			}
		}
		if m != "" && m != "1" {
			set[m] = true
		}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
