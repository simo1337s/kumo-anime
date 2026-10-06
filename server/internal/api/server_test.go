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

func TestCrossSiteRequests(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	set := func(kv ...string) func(r *http.Request) {
		return func(r *http.Request) {
			for i := 0; i+1 < len(kv); i += 2 {
				r.Header.Set(kv[i], kv[i+1])
			}
		}
	}

	// Our own pages work.
	if w := do(s, "POST", "/api/library/scan", local, "{}", set("Origin", "http://127.0.0.1:43211", "Sec-Fetch-Site", "same-origin")); w.Code == http.StatusForbidden {
		t.Fatalf("same-origin POST refused: %s", w.Body.String())
	}
	// Sandboxed frames / file: pages send Origin: null.
	if w := do(s, "POST", "/api/library/scan", local, "{}", set("Origin", "null")); w.Code != http.StatusForbidden {
		t.Fatalf("Origin null POST: got %d", w.Code)
	}
	// Another local web app (different port) is a different origin.
	if w := do(s, "POST", "/api/library/scan", local, "{}", set("Origin", "http://127.0.0.1:8080")); w.Code != http.StatusForbidden {
		t.Fatalf("other-port POST: got %d", w.Code)
	}
	// Websites can't load API resources or navigate to them.
	if w := do(s, "GET", "/api/settings", local, "", set("Sec-Fetch-Site", "cross-site")); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site GET: got %d", w.Code)
	}
	if w := do(s, "GET", "/api/proxy?u=aHR0cHM6Ly9leGFtcGxlLmNvbS8", local, "", set("Sec-Fetch-Site", "same-site", "Sec-Fetch-Dest", "document")); w.Code != http.StatusForbidden {
		t.Fatalf("same-site proxy navigation: got %d", w.Code)
	}
	// ...but may link to the app itself.
	if w := do(s, "GET", "/", local, "", set("Sec-Fetch-Site", "cross-site", "Sec-Fetch-Dest", "document")); w.Code != http.StatusOK {
		t.Fatalf("cross-site link to the app: got %d", w.Code)
	}
	// Proxied/cached remote content is never opened as a page on our origin.
	w := do(s, "GET", "/api/proxy?u=aHR0cHM6Ly9leGFtcGxlLmNvbS8", local, "", set("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "document"))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("proxy navigation: got %d csp=%q", w.Code, w.Header().Get("Content-Security-Policy"))
	}
	// A JSON body sent as text/plain (no CORS preflight) is refused.
	r := httptest.NewRequest("PUT", "http://127.0.0.1:43211/api/settings", strings.NewReader(`{"ui":{"accentColor":"#000000"}}`))
	r.RemoteAddr = local
	r.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	s.middleware(s.mux).ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("text/plain JSON body: got %d", rec.Code)
	}
}

// Settings slices must not be shared with the live settings: decoding a
// LAN device's request used to write its extraDirs straight into them.
func TestSettingsSlicesNotShared(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Library.ExtraDirs = []string{"/srv/anime-a", "/srv/anime-b"}
	cfg.Server.AllowLAN, cfg.Server.Password = true, ""
	cfg.Qbittorrent.Password = "secret"
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	lan := "192.168.1.20:5000"
	evil := s.app.Settings.Get()
	evil.Library.ExtraDirs = []string{"/home/u/.ssh", "/etc"}
	evil.Qbittorrent.Host = "203.0.113.9"
	raw, _ := json.Marshal(evil)
	if w := do(s, "PUT", "/api/settings", lan, string(raw), nil); w.Code != http.StatusOK {
		t.Fatalf("LAN save: %d %s", w.Code, w.Body.String())
	} else if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("LAN device received the qBittorrent password")
	}
	got := s.app.Settings.Get()
	if got.Library.ExtraDirs[0] != "/srv/anime-a" || got.Library.ExtraDirs[1] != "/srv/anime-b" || got.Qbittorrent.Host == "203.0.113.9" {
		t.Fatalf("LAN device changed protected settings: %q %q", got.Library.ExtraDirs, got.Qbittorrent.Host)
	}
	if w := do(s, "GET", "/api/settings", lan, "", nil); strings.Contains(w.Body.String(), "secret") {
		t.Fatal("LAN device can read the qBittorrent password")
	}
	// A copy from Get can be changed freely.
	c := s.app.Settings.Get()
	c.Library.ExtraDirs[0] = "/changed"
	if s.app.Settings.Get().Library.ExtraDirs[0] != "/srv/anime-a" {
		t.Fatal("Get returned a slice shared with the live settings")
	}
}
