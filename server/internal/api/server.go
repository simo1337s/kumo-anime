// Package api exposes the HTTP API and serves the web UI.
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/app"
	"github.com/simo1337s/animetest/server/internal/config"
)

type Server struct {
	app *app.App
	ui  fs.FS
	mux *http.ServeMux

	mu   sync.Mutex
	srv  *http.Server
	addr string
	// Wrong server passwords, by address (serverLogin).
	logins loginGuard
	// ForceWebUI is set when no desktop shell can exist (headless mode).
	ForceWebUI bool
}

func New(a *app.App, ui fs.FS) *Server {
	s := &Server{app: a, ui: ui, mux: http.NewServeMux()}
	s.routes()
	return s
}

// ListenAddr computes the bind address from the settings: loopback only
// unless LAN access or library sharing is enabled.
func ListenAddr(cfg config.Settings) string {
	host := "127.0.0.1"
	if cfg.Server.AllowLAN || cfg.Sharing.Enabled {
		host = "0.0.0.0"
	}
	return fmt.Sprintf("%s:%d", host, cfg.Server.Port)
}

// Start begins serving and rebinds when the network settings change.
func (s *Server) Start() error {
	addr := ListenAddr(s.app.Settings.Get())
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.serve(ln, addr)
	s.app.Settings.OnChange(func(old, cur config.Settings) {
		next := ListenAddr(cur)
		if next == ListenAddr(old) {
			return
		}
		log.Printf("network settings changed, rebinding to %s", next)
		time.Sleep(500 * time.Millisecond) // let the settings response reach the client
		if err := s.rebind(next); err != nil {
			log.Printf("rebind failed: %v", err)
			s.app.Hub.Error("Could not listen on " + next + ": " + err.Error())
		}
	})
	return nil
}

func (s *Server) serve(ln net.Listener, addr string) {
	srv := &http.Server{Handler: s.middleware(s.mux), ReadHeaderTimeout: 15 * time.Second}
	s.mu.Lock()
	s.srv, s.addr = srv, addr
	s.mu.Unlock()
	log.Printf("Kumo listening on http://%s", addr)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http: %v", err)
		}
	}()
}

func stopServer(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = srv.Close() // event streams never go idle
}

// rebind moves the server to a new address. Switching between 127.0.0.1
// and 0.0.0.0 on the same port requires closing the old listener first;
// if the new address can't be bound the old one is restored.
func (s *Server) rebind(next string) error {
	s.mu.Lock()
	old, prev := s.srv, s.addr
	s.mu.Unlock()
	if ln, err := net.Listen("tcp", next); err == nil {
		s.serve(ln, next)
		if old != nil {
			stopServer(old)
		}
		return nil
	}
	if old != nil {
		stopServer(old)
	}
	ln, err := net.Listen("tcp", next)
	if err != nil {
		if back, err2 := net.Listen("tcp", prev); err2 == nil {
			s.serve(back, prev)
		}
		return err
	}
	s.serve(ln, next)
	return nil
}

// Addr returns the current listen address.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

func (s *Server) Shutdown() {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// ---------------------------------------------------------------------------
// Security ("closed network")
//
//  * Requests from public IP addresses are always refused.
//  * LAN (private) addresses are accepted only when "Allow LAN access" is on,
//    and must log in when a server password is set.
//  * Browsers on this machine can use the Web UI only when the Web UI toggle
//    is on; the desktop window authenticates with a per-run shell token.
//  * Host header checks block DNS-rebinding, Origin checks block other
//    websites from driving the API (CSRF).

type clientKind int

const (
	clientShell clientKind = iota
	clientLocal
	clientLAN
)

type ctxKey struct{}

func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func isLANIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	// Tailscale / CGNAT 100.64.0.0/10
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	return false
}

// hostAllowed checks a request's Host against the names this computer goes
// by, against DNS rebinding (a website's own name, pointed at this computer,
// must not reach it): an IP address, localhost, or this computer's name,
// alone or as a home network or Tailscale names it (mypc, mypc.local,
// mypc.lan, mypc.<tailnet>.ts.net…).
func hostAllowed(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(strings.Trim(strings.ToLower(h), "[]"), ".")
	if h == "localhost" || net.ParseIP(h) != nil {
		return true
	}
	name := machineName()
	first, rest, _ := strings.Cut(h, ".")
	if name == "" || first != name {
		return false
	}
	switch {
	case rest == "", slices.Contains([]string{"local", "lan", "home", "internal", "home.arpa", "localdomain"}, rest):
		return true
	case strings.HasSuffix(rest, ".ts.net") && strings.Count(rest, ".") == 2:
		return true
	}
	return false
}

// machineName is this computer's name, lowercase, without its domain.
var machineName = sync.OnceValue(func() string {
	n, err := os.Hostname()
	if err != nil {
		return ""
	}
	n, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(n)), ".")
	return n
})

func (s *Server) sessionToken() string {
	cfg := s.app.Settings.Get()
	mac := hmac.New(sha256.New, []byte(s.app.ShellToken+"|"+cfg.Server.Password))
	mac.Write([]byte("kumo-session"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) webUIEnabled() bool {
	return s.ForceWebUI || s.app.Settings.Get().Server.WebUI
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Other Kumo apps (library sharing) are checked apart from browsers.
		if strings.HasPrefix(r.URL.Path, "/api/peer/") {
			s.servePeer(w, r, next)
			return
		}
		cfg := s.app.Settings.Get()
		ip := clientIP(r)
		var kind clientKind
		switch {
		case ip != nil && ip.IsLoopback():
			kind = clientLocal
		case cfg.Server.AllowLAN && isLANIP(ip):
			kind = clientLAN
		default:
			http.Error(w, "Kumo only accepts connections from this computer and your local network.", http.StatusForbidden)
			return
		}
		if !hostAllowed(r.Host) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		tok := r.Header.Get("X-Kumo-Shell")
		if tok == "" {
			if c, err := r.Cookie("kumo_shell"); err == nil {
				tok = c.Value
			}
		}
		if kind == clientLocal && tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.app.ShellToken)) == 1 {
			kind = clientShell
		}

		// Other websites may link to the app, but must not load or call the
		// API (Fetch Metadata; browsers send it to 127.0.0.1 and HTTPS).
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
				http.Error(w, "cross-site request refused", http.StatusForbidden)
				return
			}
		}
		// CSRF: state-changing requests must come from our own origin.
		// "null" (sandboxed frames, file: pages) is refused too.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
			// A browser that sends neither (an old one) lets other websites
			// post forms here: a POST that isn't JSON, which they can't send
			// (the app always does), must be said to come from this site.
			if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/") && !isJSON(r) {
				if site := r.Header.Get("Sec-Fetch-Site"); site != "same-origin" && site != "none" {
					http.Error(w, "cross-site request refused", http.StatusForbidden)
					return
				}
			}
		}

		if kind != clientShell && !s.webUIEnabled() {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(webUIDisabledPage))
			return
		}

		if kind == clientLAN && cfg.Server.Password != "" && !strings.HasPrefix(r.URL.Path, "/api/auth/server-login") {
			c, err := r.Cookie("kumo_session")
			if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.sessionToken())) != 1 {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "password required", "passwordRequired": true})
					return
				}
				// Let the SPA load; it shows the login screen.
			}
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// API responses (JSON, media, subtitles) are never pages.
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		} else {
			// The app's pages: never in another website's frame, where it
			// could get clicks on them (clickjacking).
			w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'; object-src 'none'; base-uri 'self'")
			w.Header().Set("X-Frame-Options", "DENY")
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, kind)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func kindOf(r *http.Request) clientKind {
	k, _ := r.Context().Value(ctxKey{}).(clientKind)
	return k
}

// isTrusted reports requests from this computer (desktop window or local
// browser), which may do host-level things like opening folders.
func isTrusted(r *http.Request) bool { return kindOf(r) != clientLAN }

const webUIDisabledPage = `<!doctype html><html><head><meta charset="utf-8"><title>Kumo</title>
<style>body{margin:0;height:100vh;display:grid;place-items:center;background:#0a0a0b;color:#ececee;font-family:system-ui,sans-serif}
.c{max-width:440px;padding:32px;border:1px solid #23232e;border-radius:16px;background:#12121a;text-align:center}
h1{font-size:20px;margin:0 0 8px}p{color:#9a9ab0;line-height:1.5}</style></head>
<body><div class="c"><h1>The Kumo Web UI is turned off</h1>
<p>Open the Kumo desktop app and enable <b>Settings › App › Web UI</b> to use Kumo from a browser.</p></div></body></html>`

// ---------------------------------------------------------------------------
// JSON helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(msg string) error { return &apiError{http.StatusBadRequest, msg} }
func forbidden(msg string) error  { return &apiError{http.StatusForbidden, msg} }
func notFound(msg string) error   { return &apiError{http.StatusNotFound, msg} }

// h adapts a handler returning (data, error) to http.HandlerFunc.
func h(fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := fn(r)
		if err != nil {
			status := http.StatusInternalServerError
			var ae *apiError
			if errors.As(err, &ae) {
				status = ae.status
			}
			if errors.Is(err, context.Canceled) {
				return
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		if out == nil {
			out = map[string]any{"ok": true}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// isJSON reports a request with a JSON body (Content-Type).
func isJSON(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	return strings.HasPrefix(ct, "application/json")
}

func decode(r *http.Request, v any) error {
	if r.Body == nil {
		return badRequest("missing body")
	}
	// A JSON content type can't be sent cross-site without a CORS
	// preflight (which is never granted), unlike text/plain form posts.
	if !isJSON(r) {
		return badRequest("expected a JSON body (Content-Type: application/json)")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	if err := dec.Decode(v); err != nil {
		return badRequest("invalid JSON: " + err.Error())
	}
	return nil
}
