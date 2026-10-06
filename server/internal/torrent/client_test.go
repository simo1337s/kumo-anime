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
	if _, err := bad.Version(ctx); err == nil || !strings.Contains(err.Error(), "wrong username or password") {
		t.Fatalf("expected auth error, got %v", err)
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
