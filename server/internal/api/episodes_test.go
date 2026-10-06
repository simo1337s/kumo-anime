package api

import "testing"

func TestMarkedProgress(t *testing.T) {
	for _, c := range []struct {
		name                  string
		status                string
		progress, repeat, tot int
		ep                    int
		watched               bool
		wantProg              int
		wantStatus            string
		wantRepeat            int
	}{
		{"not in list", "", 0, 0, 12, 3, true, 3, "CURRENT", 0},
		{"planned", "PLANNING", 0, 0, 12, 1, true, 1, "CURRENT", 0},
		{"never lowers when marking watched", "CURRENT", 9, 0, 25, 1, true, 9, "CURRENT", 0},
		{"never raises when marking unwatched", "CURRENT", 3, 0, 25, 7, false, 3, "CURRENT", 0},
		{"unwatch", "CURRENT", 9, 0, 25, 9, false, 8, "CURRENT", 0},
		{"unwatch an earlier one", "CURRENT", 9, 0, 25, 4, false, 3, "CURRENT", 0},
		{"finish", "CURRENT", 11, 0, 12, 12, true, 12, "COMPLETED", 0},
		{"finish a rewatch", "REPEATING", 5, 1, 12, 12, true, 12, "COMPLETED", 2},
		{"rewatch keeps repeating", "REPEATING", 5, 1, 12, 7, true, 7, "REPEATING", 1},
		{"reopen a completed show", "COMPLETED", 12, 0, 12, 12, false, 11, "CURRENT", 0},
		{"completed: already watched", "COMPLETED", 12, 0, 12, 3, true, 12, "COMPLETED", 0},
		{"airing, unknown total", "CURRENT", 4, 0, 0, 6, true, 6, "CURRENT", 0},
		{"paused resumes", "PAUSED", 4, 0, 24, 5, true, 5, "CURRENT", 0},
	} {
		p, st, r := markedProgress(c.status, c.progress, c.repeat, c.tot, c.ep, c.watched)
		if p != c.wantProg || st != c.wantStatus || r != c.wantRepeat {
			t.Errorf("%s: got progress=%d status=%s repeat=%d, want %d %s %d", c.name, p, st, r, c.wantProg, c.wantStatus, c.wantRepeat)
		}
	}
}
