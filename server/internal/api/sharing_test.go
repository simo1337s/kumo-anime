package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/library"
	"github.com/simo1337s/animetest/server/internal/share"
)

// sharingServer is a Kumo with library sharing on, serving on 127.0.0.1.
func sharingServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := newTestServer(t)
	s.app.Share.Start()
	cfg := s.app.Settings.Get()
	cfg.Sharing.Enabled = true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.middleware(s.mux))
	t.Cleanup(srv.Close)
	return s, srv
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// addEpisode puts a file in s's library.
func addEpisode(t *testing.T, s *Server, mediaID, episode int) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Show - 01.mkv")
	data := bytes.Repeat([]byte("kumo-video-"), 2000)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &library.LocalFile{Path: path, Dir: dir, Name: filepath.Base(path), Size: int64(len(data)), MediaID: mediaID, Episode: episode, Kind: "main"}
	if err := s.app.Files.Save(f); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestLibrarySharing(t *testing.T) {
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	path, data := addEpisode(t, host, 21, 1)
	hostID, guestID := host.app.Share.ID(), guest.app.Share.ID()

	// The guest adds the host by its address (no multicast needed).
	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	// The host hears of the guest when it asks, and shares nothing yet.
	eventually(t, "the host to list the guest", func() bool {
		st := host.app.Share.Status()
		return len(st.Peers) == 1 && st.Peers[0].ID == guestID && !st.Peers[0].Allowed
	})
	if libs := guest.app.Share.Libraries(); len(libs) != 0 {
		t.Fatalf("shared before the host allowed it: %+v", libs)
	}
	remote := share.RemotePath(hostID, path)
	if w := do(guest, "GET", "/api/local/file?"+url.Values{"path": {remote}}.Encode(), local, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("a file not shared: got %d", w.Code)
	}

	// The host shares its library with the guest.
	if w := do(host, "POST", "/api/sharing/peers/"+guestID, local, `{"allowed":true}`, nil); w.Code != http.StatusOK {
		t.Fatalf("allow: %d %s", w.Code, w.Body)
	}
	var libs []share.SharedLibrary
	eventually(t, "the guest to get the host's library", func() bool {
		w := do(guest, "GET", "/api/sharing/libraries", local, "", nil) // asks again
		_ = json.Unmarshal(w.Body.Bytes(), &libs)
		return len(libs) == 1 && len(libs[0].Files) == 1
	})
	f := libs[0].Files[0]
	if f.Path != remote || f.Host != hostID || f.HostName == "" || f.MediaID != 21 || f.Episode != 1 || f.Dir != "" {
		t.Fatalf("shared file: %+v", f)
	}

	// The guest's player gets the file through the guest, ranges too.
	r := httptest.NewRequest("GET", "http://127.0.0.1:43211/api/local/file?"+url.Values{"path": {remote}}.Encode(), nil)
	r.RemoteAddr = local
	r.Header.Set("Range", "bytes=11-21")
	w := httptest.NewRecorder()
	guest.middleware(guest.mux).ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), data[11:22]) {
		t.Fatalf("range through the guest: %d %q", w.Code, w.Body.Bytes())
	}
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "html") {
		t.Errorf("content type %q", ct)
	}

	// Watching on the guest never touches the host's history or player.
	if h := host.app.History.Recent(10); len(h) != 0 {
		t.Errorf("the host's history changed: %+v", h)
	}
	if st := host.app.Player.Status(); st != nil {
		t.Errorf("the host's player changed: %+v", st)
	}

	// Remembered after a restart of the host's sharing.
	host.app.Share.Stop()
	host.app.Share.Start()
	if st := host.app.Share.Status(); len(st.Peers) != 1 || !st.Peers[0].Allowed {
		t.Fatalf("the host forgot it shares with the guest: %+v", st.Peers)
	}

	// The host stops sharing: the guest's library goes.
	if w := do(host, "POST", "/api/sharing/peers/"+guestID, local, `{"allowed":false}`, nil); w.Code != http.StatusOK {
		t.Fatalf("disallow: %d", w.Code)
	}
	eventually(t, "the guest to lose the host's library", func() bool {
		w := do(guest, "GET", "/api/sharing/libraries", local, "", nil)
		_ = json.Unmarshal(w.Body.Bytes(), &libs)
		return len(libs) == 0
	})
	if w := do(guest, "GET", "/api/local/file?"+url.Values{"path": {remote}}.Encode(), local, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("after sharing stopped: got %d", w.Code)
	}
}

func TestPeerRequestsChecked(t *testing.T) {
	const lan, public = "192.168.1.30:5000", "8.8.8.8:5000"
	host, hostSrv := sharingServer(t)
	path, _ := addEpisode(t, host, 21, 1)
	hostID := host.app.Share.ID()
	var who share.Hello
	_ = json.Unmarshal(do(host, "GET", "/api/peer/whoami", lan, "", nil).Body.Bytes(), &who)
	if who.ID != hostID || who.Key == "" {
		t.Fatalf("whoami: %+v", who)
	}
	if w := do(host, "GET", "/api/peer/whoami", public, "", nil); w.Code != http.StatusForbidden {
		t.Errorf("from the internet: %d", w.Code)
	}
	// Sharing on doesn't open the rest to the network.
	if w := do(host, "GET", "/api/status", lan, "", nil); w.Code != http.StatusForbidden {
		t.Errorf("the app from the network with LAN access off: %d", w.Code)
	}
	if w := do(host, "GET", "/api/peer/files", lan, "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("unsigned: %d", w.Code)
	}
	if w := do(host, "GET", "/api/peer/whoami", lan, "", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }); w.Code != http.StatusForbidden {
		t.Errorf("from a website: %d", w.Code)
	}

	// Another Kumo, which the host doesn't share with: it may ask, not take.
	other, _ := sharingServer(t)
	signed := func(target string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", hostSrv.URL+target, nil)
		if err := other.app.Share.Sign(req, hostID, who.Key); err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := signed("/api/peer/hello")
	var hello share.Hello
	_ = json.NewDecoder(resp.Body).Decode(&hello)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || hello.Shares || hello.Files != "" {
		t.Errorf("hello from a Kumo not allowed: %d %+v", resp.StatusCode, hello)
	}
	for _, target := range []string{"/api/peer/files", "/api/peer/local/file?" + url.Values{"path": {path}}.Encode()} {
		resp := signed(target)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s from a Kumo not allowed: %d", target, resp.StatusCode)
		}
	}
	// A token for another host is refused.
	req, _ := http.NewRequest("GET", hostSrv.URL+"/api/peer/hello", nil)
	_ = other.app.Share.Sign(req, other.app.Share.ID(), other.app.Share.Whoami().Key)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("a token made for another Kumo: %d", resp.StatusCode)
		}
	}

	// Allowed: files, and only the library's.
	if err := host.app.Share.SetAllowed(other.app.Share.ID(), true); err != nil {
		t.Fatal(err)
	}
	resp = signed("/api/peer/files")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Show - 01.mkv") {
		t.Errorf("files when allowed: %d %s", resp.StatusCode, body)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(outside, []byte("secret"), 0o644)
	resp = signed("/api/peer/local/file?" + url.Values{"path": {outside}}.Encode())
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || strings.Contains(string(body), "secret") {
		t.Errorf("a file outside the library: %d %s", resp.StatusCode, body)
	}

	// Sharing off: nothing answers.
	cfg := host.app.Settings.Get()
	cfg.Sharing.Enabled = false
	if _, err := host.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if w := do(host, "GET", "/api/peer/whoami", lan, "", nil); w.Code != http.StatusForbidden {
		t.Errorf("whoami with sharing off: %d", w.Code)
	}
}

func TestSharingSettingsOnlyFromThisComputer(t *testing.T) {
	s := newTestServer(t)
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN = true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	const lan = "192.168.1.30:5000"
	if w := do(s, "POST", "/api/sharing/peers/abc", lan, `{"allowed":true}`, nil); w.Code != http.StatusForbidden {
		t.Errorf("allowing from the network: %d", w.Code)
	}
	if w := do(s, "PUT", "/api/settings", lan, `{"sharing":{"enabled":true,"name":"x"}}`, nil); w.Code != http.StatusOK {
		t.Fatalf("settings from the network: %d %s", w.Code, w.Body)
	}
	if got := s.app.Settings.Get().Sharing; got.Enabled || got.Name != "" {
		t.Errorf("the network turned sharing on: %+v", got)
	}
}
