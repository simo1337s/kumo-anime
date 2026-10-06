package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestHideContinueRoutes(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	events, unsub := s.app.Hub.Subscribe()
	defer unsub()
	hidden := func() map[string]map[string]int64 {
		var m map[string]map[string]int64
		_, _ = s.app.DB.GetKV("continue-hidden", &m)
		return m
	}
	published := func() bool {
		ok := false
		for len(events) > 0 {
			ok = ok || strings.Contains(string(<-events), `"collection-updated"`)
		}
		return ok
	}

	if w := do(s, "POST", "/api/continue/hide", local, `{"mediaId":21,"episode":5}`, nil); w.Code != http.StatusOK {
		t.Fatalf("hide: %d %s", w.Code, w.Body.String())
	}
	if h := hidden(); h["21"]["episode"] != 5 || h["21"]["hiddenAt"] == 0 {
		t.Fatalf("hide not saved: %v", h)
	}
	if !published() {
		t.Fatal("other windows weren't told to refresh")
	}
	if w := do(s, "POST", "/api/continue/unhide", local, `{"mediaId":21}`, nil); w.Code != http.StatusOK {
		t.Fatalf("unhide: %d %s", w.Code, w.Body.String())
	}
	if h := hidden(); len(h) != 0 || !published() {
		t.Fatalf("unhide: still hidden %v, or no event", h)
	}
	if w := do(s, "POST", "/api/continue/hide", local, `{"mediaId":21}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("hide without an episode: got %d", w.Code)
	}
}
