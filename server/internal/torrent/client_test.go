package torrent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/simo1337s/animetest/server/internal/config"
)

func cfgFor(t *testing.T, srv *httptest.Server) config.TorrentClientConfig {
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	return config.TorrentClientConfig{Host: u.Hostname(), Port: port, Username: "admin", Password: "pw"}
}

// Mock qBittorrent 5 (pause/resume removed in favour of stop/start).
func TestQbittorrent(t *testing.T) {
	var added url.Values
	var stopped string
	logins := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_ = r.ParseForm()
			if r.Form.Get("username") != "admin" || r.Form.Get("password") != "pw" {
				_, _ = io.WriteString(w, "Fails.")
				return
			}
			logins++
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s" + strconv.Itoa(logins), Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		if c, err := r.Cookie("SID"); err != nil || c.Value != "s"+strconv.Itoa(logins) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.2")
		case "/api/v2/torrents/info":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"hash": "abc", "name": "[SubsPlease] Frieren - 12 (1080p).mkv", "size": 1 << 30, "progress": 0.5,
				"dlspeed": 1000, "upspeed": 10, "eta": 8640000, "state": "stalledDL", "num_seeds": 3, "num_leechs": 1,
				"save_path": "/anime", "content_path": "/anime/x.mkv", "added_on": 1, "ratio": 0.1,
			}})
		case "/api/v2/torrents/add":
			_ = r.ParseForm()
			added = r.Form
		case "/api/v2/torrents/pause":
			w.WriteHeader(http.StatusNotFound)
		case "/api/v2/torrents/stop":
			_ = r.ParseForm()
			stopped = r.Form.Get("hashes")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cfg := cfgFor(t, srv)
	cfg.Category, cfg.Tags = "anime", "kumo"
	q := NewQbittorrent(cfg)
	ctx := context.Background()
	if v, err := q.Version(ctx); err != nil || v != "v5.0.2" {
		t.Fatalf("version %q %v", v, err)
	}
	list, err := q.List(ctx)
	if err != nil || len(list) != 1 || list[0].State != "stalled" || list[0].ETA != -1 {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := q.Add(ctx, []string{"magnet:?xt=urn:btih:abc", "magnet:?xt=urn:btih:def"}, "/anime/Frieren"); err != nil {
		t.Fatal(err)
	}
	if added.Get("savepath") != "/anime/Frieren" || added.Get("category") != "anime" || !strings.Contains(added.Get("urls"), "\n") {
		t.Fatalf("add form %v", added)
	}
	if err := q.Pause(ctx, []string{"abc", "def"}); err != nil || stopped != "abc|def" {
		t.Fatalf("pause via stop: %q %v", stopped, err)
	}
	// Session expiry: server forgets the cookie, client logs in again.
	logins++
	if _, err := q.Version(ctx); err != nil {
		t.Fatalf("re-login: %v", err)
	}
	bad := NewQbittorrent(config.TorrentClientConfig{Host: cfg.Host, Port: cfg.Port, Username: "admin", Password: "wrong"})
	if _, err := bad.Version(ctx); !IsAuthError(err) || !strings.Contains(err.Error(), "rejected the username or password") {
		t.Fatalf("expected auth error, got %v", err)
	}
}

// qBittorrent bans an IP after a few failed logins, so Kumo must not retry
// a failing login on every poll, and must not log in at all when
// qBittorrent bypasses authentication for localhost.
func TestQbittorrentLoginAttempts(t *testing.T) {
	attempts, bypass, ban := 0, false, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			attempts++
			if ban {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, "Fails.")
			return
		}
		if !bypass {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "v5.2.4")
	}))
	defer srv.Close()
	ctx := context.Background()
	cfg := cfgFor(t, srv)

	bypass = true
	q := NewQbittorrent(cfg)
	for i := 0; i < 5; i++ {
		if v, err := q.Version(ctx); err != nil || v != "v5.2.4" {
			t.Fatalf("bypass: %q %v", v, err)
		}
	}
	if attempts != 0 {
		t.Fatalf("logged in %d times although auth is bypassed", attempts)
	}

	bypass = false
	q = NewQbittorrent(cfg) // wrong password
	for i := 0; i < 10; i++ {
		if _, err := q.Version(ctx); !IsAuthError(err) {
			t.Fatalf("wrong password: %v", err)
		}
	}
	if attempts != 1 {
		t.Fatalf("wrong password: %d login attempts for 10 polls", attempts)
	}

	attempts, ban = 0, true
	q = NewQbittorrent(cfg)
	for i := 0; i < 10; i++ {
		if _, err := q.Version(ctx); !IsAuthError(err) || !strings.Contains(err.Error(), "blocked") {
			t.Fatalf("banned: %v", err)
		}
	}
	if attempts != 1 {
		t.Fatalf("banned: %d login attempts for 10 polls", attempts)
	}

	attempts = 0
	empty := cfg
	empty.Password = ""
	q = NewQbittorrent(empty)
	if _, err := q.Version(ctx); !IsAuthError(err) || attempts != 0 {
		t.Fatalf("empty password: attempts=%d err=%v", attempts, err)
	}
}

func TestTransmission(t *testing.T) {
	var lastArgs map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != "sess" {
			w.Header().Set("X-Transmission-Session-Id", "sess")
			w.WriteHeader(http.StatusConflict)
			return
		}
		var body struct {
			Method    string         `json:"method"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastArgs = body.Arguments
		switch body.Method {
		case "session-get":
			_, _ = io.WriteString(w, `{"result":"success","arguments":{"version":"4.0.6"}}`)
		case "torrent-get":
			_, _ = io.WriteString(w, `{"result":"success","arguments":{"torrents":[{"hashString":"h1","name":"Show","totalSize":10,"percentDone":1,"status":6,"downloadDir":"/a"}]}}`)
		default:
			_, _ = io.WriteString(w, `{"result":"success","arguments":{}}`)
		}
	}))
	defer srv.Close()
	tr := NewTransmission(cfgFor(t, srv))
	ctx := context.Background()
	if v, err := tr.Version(ctx); err != nil || v != "4.0.6" {
		t.Fatalf("version %q %v", v, err)
	}
	list, err := tr.List(ctx)
	if err != nil || len(list) != 1 || list[0].State != "seeding" {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := tr.Add(ctx, []string{"magnet:?x"}, "/anime/Show"); err != nil || lastArgs["download-dir"] != "/anime/Show" {
		t.Fatalf("add %v %v", lastArgs, err)
	}
}

// qBittorrent answers 409 (WebAPI 2.14+) or 200 "Fails." (older) when
// nothing was added, e.g. because the torrent is already in the client.
func TestQbittorrentAddAlreadyPresent(t *testing.T) {
	answer := http.StatusConflict
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(answer)
		if answer == http.StatusUnsupportedMediaType {
			_, _ = io.WriteString(w, "Torrent file is not valid.")
			return
		}
		_, _ = io.WriteString(w, "Fails.")
	}))
	defer srv.Close()
	q := NewQbittorrent(cfgFor(t, srv))
	ctx := context.Background()
	if err := q.Add(ctx, []string{"magnet:?xt=urn:btih:abc"}, ""); err != nil {
		t.Fatalf("409: %v", err)
	}
	answer = http.StatusOK
	if err := q.Add(ctx, []string{"magnet:?xt=urn:btih:abc"}, ""); err != nil {
		t.Fatalf("200 Fails.: %v", err)
	}
	answer = http.StatusUnsupportedMediaType
	if err := q.Add(ctx, []string{"https://example.org/x.torrent"}, ""); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("415: %v", err)
	}
}

func TestTransmissionStatesAndAdd(t *testing.T) {
	var added []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method    string         `json:"method"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body.Method {
		case "torrent-get":
			// error: 0 none, 1 tracker warning, 2 tracker error, 3 local error.
			_, _ = io.WriteString(w, `{"result":"success","arguments":{"torrents":[
				{"hashString":"h0","name":"ok","status":4,"rateDownload":100,"error":0},
				{"hashString":"h1","name":"warning","status":4,"rateDownload":100,"error":1},
				{"hashString":"h2","name":"tracker","status":6,"percentDone":1,"error":2},
				{"hashString":"h3","name":"local","status":0,"error":3}]}}`)
		case "torrent-add":
			name, _ := body.Arguments["filename"].(string)
			switch {
			case strings.Contains(name, "dupe"):
				// Transmission 2.x/3.x
				_, _ = io.WriteString(w, `{"result":"duplicate torrent","arguments":{"torrent-duplicate":{"hashString":"h0","id":1,"name":"ok"}}}`)
			case strings.Contains(name, "bad"):
				_, _ = io.WriteString(w, `{"result":"invalid or corrupt torrent file","arguments":{}}`)
			default:
				added = append(added, name)
				_, _ = io.WriteString(w, `{"result":"success","arguments":{"torrent-added":{"hashString":"h9","id":9,"name":"new"}}}`)
			}
		default:
			_, _ = io.WriteString(w, `{"result":"success","arguments":{}}`)
		}
	}))
	defer srv.Close()
	tr := NewTransmission(cfgFor(t, srv))
	ctx := context.Background()
	list, err := tr.List(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("list %+v %v", list, err)
	}
	for i, want := range []string{"downloading", "downloading", "seeding", "error"} {
		if list[i].State != want {
			t.Errorf("%s: state %q, want %q", list[i].Name, list[i].State, want)
		}
	}
	if err := tr.Add(ctx, []string{"magnet:?dupe"}, ""); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	err = tr.Add(ctx, []string{"https://x/bad.torrent", "magnet:?good"}, "")
	if err == nil || !strings.Contains(err.Error(), "invalid or corrupt") || len(added) != 1 || added[0] != "magnet:?good" {
		t.Fatalf("one bad torrent: %v, added %v", err, added)
	}
}

// A host that doesn't make a valid URL is an error, not a crash of the
// goroutines polling the client.
func TestInvalidClientHost(t *testing.T) {
	cfg := config.TorrentClientConfig{Host: "127.0.0. 1", Port: 8080, Username: "admin", Password: "pw"}
	ctx := context.Background()
	if _, err := NewQbittorrent(cfg).Version(ctx); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("qBittorrent: %v", err)
	}
	if err := NewQbittorrent(cfg).Add(ctx, []string{"magnet:?x"}, ""); err == nil {
		t.Fatal("qBittorrent add: no error")
	}
	if _, err := NewTransmission(cfg).Version(ctx); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("Transmission: %v", err)
	}
}

func TestEnrich(t *testing.T) {
	r := &SearchResult{Name: "[SubsPlease] Sousou no Frieren - 12 (1080p) [ABCD1234].mkv"}
	enrich(r)
	if r.ReleaseGroup != "SubsPlease" || r.Resolution != "1080p" || r.EpisodeNumber != 12 || r.IsBatch {
		t.Fatalf("%+v", r)
	}
	b := &SearchResult{Name: "[Judas] Frieren (Season 1) [1080p][HEVC x265 10bit][Dual-Audio] (Batch)"}
	enrich(b)
	if !b.IsBatch || !b.Dub {
		t.Fatalf("batch %+v", b)
	}
}
