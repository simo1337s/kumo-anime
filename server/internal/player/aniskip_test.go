package player

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/util"
)

type fakeResult struct {
	typ        string
	start, end float64
	length     float64 // of the version the times were recorded on
}

// fakeAniSkip answers every skip-times request with results, and records the
// requests' URLs.
func fakeAniSkip(t *testing.T, results ...fakeResult) (*db.DB, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.String())
		var out []map[string]any
		for _, res := range results {
			out = append(out, map[string]any{
				"interval":      map[string]any{"startTime": res.start, "endTime": res.end},
				"skipType":      res.typ,
				"skipId":        "x",
				"episodeLength": res.length,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"found": len(out) > 0, "results": out, "statusCode": 200})
	}))
	t.Cleanup(srv.Close)
	oldAPI, oldHTTP := aniskipAPI, util.HTTP
	aniskipAPI, util.HTTP = srv.URL, srv.Client() // util.HTTP refuses local addresses
	t.Cleanup(func() { aniskipAPI, util.HTTP = oldAPI, oldHTTP })
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, &asked
}

func TestSkipTimesFitTheEpisodesLength(t *testing.T) {
	// Recorded on a version 9s shorter than the one playing (1431s).
	d, asked := fakeAniSkip(t,
		fakeResult{"op", 80, 170, 1422},
		fakeResult{"mixed-op", 85, 175, 1422},
		fakeResult{"ed", 1300, 1390, 1422},
		fakeResult{"recap", 1395, 1430, 1422},
	)
	got := SkipTimes(context.Background(), d, 34572, 1, 1430.6)
	want := []SkipInterval{
		// The mixed opening wins over the plain one, like in AniSkip's
		// extension; the times move by the difference in length.
		{Type: "mixed-op", Start: 85 + 8.6, End: 175 + 8.6},
		{Type: "ed", Start: 1300 + 8.6, End: 1390 + 8.6},
		// Up to the end of the episode, not past it.
		{Type: "recap", Start: 1395 + 8.6, End: 1430.6},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Type != want[i].Type || !near(got[i].Start, want[i].Start) || !near(got[i].End, want[i].End) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	if len(*asked) != 1 {
		t.Fatalf("%d requests, want 1", len(*asked))
	}
	u := (*asked)[0]
	if !strings.HasPrefix(u, "/v2/skip-times/34572/1?") || !strings.Contains(u, "episodeLength=1431") {
		t.Errorf("asked %s: want the times for an episode 1431s long", u)
	}

	// Saved for that length: no new request.
	SkipTimes(context.Background(), d, 34572, 1, 1430.9)
	if len(*asked) != 1 {
		t.Errorf("%d requests, want the saved times", len(*asked))
	}
	// Another version of the episode asks again.
	SkipTimes(context.Background(), d, 34572, 1, 1440)
	if len(*asked) != 2 || !strings.Contains((*asked)[1], "episodeLength=1440") {
		t.Errorf("requests %v: want one for the 1440s version", *asked)
	}
}

func TestSkipTimesNeedTheLength(t *testing.T) {
	d, asked := fakeAniSkip(t, fakeResult{"op", 80, 170, 1422})
	for _, length := range []float64{0, -1} {
		if got := SkipTimes(context.Background(), d, 34572, 1, length); got != nil {
			t.Errorf("length %v: got %+v, want none", length, got)
		}
	}
	if len(*asked) != 0 {
		t.Errorf("asked AniSkip without a length: %v", *asked)
	}
}

func TestSkipTimesNoneFound(t *testing.T) {
	d, asked := fakeAniSkip(t)
	if got := SkipTimes(context.Background(), d, 34572, 1, 1420); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
	// Remembered for a while too.
	SkipTimes(context.Background(), d, 34572, 1, 1420)
	if len(*asked) != 1 {
		t.Errorf("%d requests, want 1", len(*asked))
	}
}

func near(a, b float64) bool { return a-b < 0.001 && b-a < 0.001 }
