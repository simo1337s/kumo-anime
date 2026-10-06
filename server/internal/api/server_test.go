package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/simo1337s/animetest/server/internal/app"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	a, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Shutdown)
	return New(a, fstest.MapFS{"index.html": {Data: []byte("<html>kumo</html>")}})
}

func do(s *Server, method, path, remote string, body string, mod func(r *http.Request)) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, "http://127.0.0.1:43211"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, "http://127.0.0.1:43211"+path, nil)
	}
	r.RemoteAddr = remote
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	s.middleware(s.mux).ServeHTTP(w, r)
	return w
}

func TestClosedNetwork(t *testing.T) {
	s := newTestServer(t)
	local, lan, public := "127.0.0.1:5000", "192.168.1.20:5000", "8.8.8.8:5000"

	if w := do(s, "GET", "/api/status", public, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("public client: got %d", w.Code)
	}
	if w := do(s, "GET", "/api/status", lan, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("LAN client with LAN disabled: got %d", w.Code)
	}
	if w := do(s, "GET", "/api/status", local, "", nil); w.Code != http.StatusOK {
		t.Fatalf("local client: got %d", w.Code)
	}
	// DNS rebinding: a public hostname pointing at 127.0.0.1
	if w := do(s, "GET", "/api/status", local, "", func(r *http.Request) { r.Host = "evil.example.com" }); w.Code != http.StatusForbidden {
		t.Fatalf("rebinding host: got %d", w.Code)
	}
	// CSRF from another website
	if w := do(s, "POST", "/api/library/scan", local, "{}", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example.com") }); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: got %d", w.Code)
	}

	// Web UI off: browsers are refused, the desktop window (shell token) isn't.
	cfg := s.app.Settings.Get()
	cfg.Server.WebUI = false
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(s, "GET", "/api/status", local, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("browser with web UI off: got %d", w.Code)
	}
	shell := func(r *http.Request) { r.Header.Set("X-Kumo-Shell", s.app.ShellToken) }
	if w := do(s, "GET", "/api/status", local, "", shell); w.Code != http.StatusOK {
		t.Fatalf("desktop window: got %d", w.Code)
	}

	// LAN with password
	cfg.Server.WebUI = true
	cfg.Server.AllowLAN = true
	cfg.Server.Password = "hunter2"
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(s, "GET", "/api/status", public, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("public client with LAN on: got %d", w.Code)
	}
	if w := do(s, "GET", "/api/status", lan, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("LAN without password: got %d", w.Code)
	}
	if w := do(s, "POST", "/api/auth/server-login", lan, `{"password":"wrong"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d", w.Code)
	}
	w := do(s, "POST", "/api/auth/server-login", lan, `{"password":"hunter2"}`, nil)
	if w.Code != http.StatusOK || len(w.Result().Cookies()) == 0 {
		t.Fatalf("login: got %d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	withCookie := func(r *http.Request) { r.AddCookie(cookie) }
	w = do(s, "GET", "/api/status", lan, "", withCookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"client":"lan"`) {
		t.Fatalf("LAN with cookie: %d %s", w.Code, w.Body.String()[:80])
	}
	// LAN devices can't change which programs run on this PC.
	next := s.app.Settings.Get()
	next.Mpv.Path = "/bin/sh"
	next.UI.AccentColor = "#22c3a6"
	raw, _ := json.Marshal(next)
	if w := do(s, "PUT", "/api/settings", lan, string(raw), withCookie); w.Code != http.StatusOK {
		t.Fatalf("LAN settings save: %d", w.Code)
	}
	got := s.app.Settings.Get()
	if got.Mpv.Path == "/bin/sh" || got.UI.AccentColor != "#22c3a6" {
		t.Fatalf("LAN settings filter: mpv=%q accent=%q", got.Mpv.Path, got.UI.AccentColor)
	}
	if w := do(s, "GET", "/api/fs/dirs?path=/", lan, "", withCookie); w.Code != http.StatusForbidden {
		t.Fatalf("LAN dir browsing: got %d", w.Code)
	}
}
