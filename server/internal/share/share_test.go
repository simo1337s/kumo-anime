package share

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/library"
	"github.com/simo1337s/animetest/server/internal/stream"
)

type fakeLibrary []*library.LocalFile

func (f fakeLibrary) All() ([]*library.LocalFile, error) { return f, nil }

func (f fakeLibrary) Get(path string) (*library.LocalFile, error) {
	for _, x := range f {
		if x.Path == path {
			return x, nil
		}
	}
	return nil, sql.ErrNoRows
}

func openDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func newService(t *testing.T, d *db.DB) *Service {
	t.Helper()
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(d, settings, fakeLibrary{}, events.NewHub())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIdentityStaysAndTokensCheck(t *testing.T) {
	d := openDB(t)
	a, err := LoadIdentity(d)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := LoadIdentity(d)
	if again.ID != a.ID || len(a.ID) != 16 {
		t.Fatalf("identity changed: %s, %s", a.ID, again.ID)
	}
	b, _ := LoadIdentity(openDB(t))
	if b.ID == a.ID {
		t.Fatal("two Kumos with one ID")
	}

	// a asks b: b checks it's a.
	bPub, _ := parsePublicKey(b.ID, b.PublicKey())
	aPub, _ := parsePublicKey(a.ID, a.PublicKey())
	now := time.Now()
	signed := func(method, target, body string, at time.Time, ticket bool) (*http.Request, *signature) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, _ := http.NewRequest(method, target, rd)
		sig, err := a.sign(req, b.ID, bPub, "Desk PC\n", 43211, 7, at, ticket)
		if err != nil {
			t.Fatal(err)
		}
		return req, sig
	}
	// As it reaches the host.
	received := func(req *http.Request) *http.Request {
		r := req.Clone(req.Context())
		if req.GetBody != nil {
			r.Body, _ = req.GetBody()
		}
		r.GetBody = nil
		return r
	}
	req, sig := signed("GET", "http://x/api/peer/hello", "", now, false)
	c, hostSig, err := b.verify(received(req), now)
	if err != nil {
		t.Fatal(err)
	}
	if c.id != a.ID || c.name != "Desk PC" || c.port != 43211 || c.user != 7 {
		t.Errorf("caller %+v", c)
	}
	// The host's answer proves it's b: a can check it, and only b's does.
	date := now.UTC().Format(http.TimeFormat)
	got, _ := hostSig.reply(date)
	if want, _ := sig.reply(date); got != want || got == "" {
		t.Error("the host's answer doesn't check")
	}
	third, _ := LoadIdentity(openDB(t))
	impostor := &signature{id: third, peerPub: aPub, guest: a.ID, host: b.ID, nonce: sig.nonce}
	if forged, _ := impostor.reply(date); forged == got {
		t.Error("another Kumo answered for b")
	}

	// Sent again: refused.
	if _, _, err := b.verify(received(req), now); err == nil {
		t.Error("a request was taken twice")
	}
	// Changed on the way: refused.
	req, _ = signed("POST", "http://x/api/peer/history", `[{"mediaId":1}]`, now, false)
	changed := received(req)
	changed.Body = io.NopCloser(strings.NewReader(`[{"mediaId":2}]`))
	if _, _, err := b.verify(changed, now); err == nil {
		t.Error("a changed body passed")
	}
	if _, _, err := b.verify(received(req), now); err != nil {
		t.Errorf("the body as sent: %v", err)
	}
	req, _ = signed("GET", "http://x/api/peer/hello", "", now, false)
	moved := received(req)
	moved.URL.Path = "/api/peer/files"
	if _, _, err := b.verify(moved, now); err == nil {
		t.Error("a request taken to another endpoint passed")
	}
	req, _ = signed("GET", "http://x/api/peer/hello", "", now, false)
	other := received(req)
	other.Header.Set(headerUser, "8")
	if _, _, err := b.verify(other, now); err == nil {
		t.Error("a changed account passed")
	}
	// Too old, or from the future: refused, though answered (with the
	// host's time, proven).
	req, _ = signed("GET", "http://x/api/peer/hello", "", now.Add(-time.Hour), false)
	if _, s, err := b.verify(received(req), now); !errors.Is(err, errClock) || s == nil {
		t.Errorf("an old request: %v", err)
	}
	// A ticket (mpv): only a file, for a while, and again.
	req, _ = signed("GET", "http://x/api/peer/local/file?path=%2Fa%2F1.mkv", "", now.Add(-time.Hour), true)
	for range 2 {
		if _, _, err := b.verify(received(req), now); err != nil {
			t.Errorf("ticket: %v", err)
		}
	}
	if _, _, err := b.verify(received(req), now.Add(ticketFor+time.Hour)); err == nil {
		t.Error("an expired ticket passed")
	}
	req, _ = signed("GET", "http://x/api/peer/files", "", now, true)
	if _, _, err := b.verify(received(req), now); err == nil {
		t.Error("a ticket for something else than a file passed")
	}

	// A third Kumo can't pass for a, nor a's token be used with another key.
	req, _ = signed("GET", "http://x/api/peer/hello", "", now, false)
	forged := received(req)
	forged.Header.Set(headerKey, third.PublicKey())
	if _, _, err := b.verify(forged, now); err == nil {
		t.Error("a key that isn't a's passed for a")
	}
	forged = received(req)
	forged.Header.Set(headerID, third.ID)
	forged.Header.Set(headerKey, third.PublicKey())
	if _, _, err := b.verify(forged, now); err == nil {
		t.Error("a's token passed for another Kumo")
	}
	// Made for b, it means nothing to a third Kumo.
	if _, _, err := third.verify(received(req), now); err == nil {
		t.Error("a token for b passed with another host")
	}
	// An older Kumo's request: told so.
	old := received(req)
	old.Header.Del(headerTime)
	if _, _, err := b.verify(old, now); !errors.Is(err, errOldPeer) {
		t.Errorf("older Kumo: %v", err)
	}
}

func TestPaths(t *testing.T) {
	for _, path := range []string{"/home/me/Anime/Show - 01.mkv", `C:\Anime\Show - 01.mkv`, `\\nas\anime\x.mkv`} {
		p := RemotePath("abc123", path)
		host, onHost, ok := ParsePath(p)
		if !ok || host != "abc123" || onHost != path || !IsRemote(p) {
			t.Errorf("%q: %q %q %v", path, host, onHost, ok)
		}
	}
	for _, bad := range []string{"/home/me/x.mkv", "kumo://", "kumo://abc", "kumo:///x"} {
		if _, _, ok := ParsePath(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	if _, _, ok := remoteSession("abc.def-123"); !ok {
		t.Error("a remote HLS session")
	}
	for _, bad := range []string{"0b5f4a2e-9c1d-4f7a-8e3b-1a2b3c4d5e6f", "abc./x", "../x.y", "a.b/c"} {
		if IsRemoteSession(bad) {
			t.Errorf("%q is a remote session", bad)
		}
	}
}

func TestBeaconsAndPeers(t *testing.T) {
	d := openDB(t)
	s := newService(t, d)
	other, _ := LoadIdentity(openDB(t))
	b := beacon{Kumo: protocol, ID: other.ID, Name: "Laptop", Port: 43211, Key: other.PublicKey(), Version: "1.0.90"}

	s.heard(b, net.ParseIP("8.8.8.8")) // from the internet
	s.heard(beacon{Kumo: protocol, ID: other.ID, Name: "Fake", Port: 43211, Key: s.id.PublicKey()}, net.ParseIP("192.168.1.9"))
	own := s.beacon()
	s.heard(own, net.ParseIP("192.168.1.5"))
	if st := s.Status(); len(st.Peers) != 0 {
		t.Fatalf("listed: %+v", st.Peers)
	}

	s.heard(b, net.ParseIP("192.168.1.9"))
	st := s.Status()
	if len(st.Peers) != 1 {
		t.Fatalf("peers %+v", st.Peers)
	}
	p := st.Peers[0]
	if p.ID != other.ID || p.Name != "Laptop" || p.Address != "192.168.1.9:43211" || !p.Online || p.Allowed {
		t.Errorf("peer %+v", p)
	}

	// Sharing with it is remembered, with its address.
	if err := s.SetAllowed(other.ID, true); err != nil {
		t.Fatal(err)
	}
	reloaded := newService(t, d)
	st = reloaded.Status()
	if len(st.Peers) != 1 || !st.Peers[0].Allowed || st.Peers[0].Online || st.Peers[0].Address != "192.168.1.9:43211" {
		t.Errorf("after a restart: %+v", st.Peers)
	}
	// Not useful, not around: not listed after a restart.
	_ = s.SetAllowed(other.ID, false)
	if st := newService(t, d).Status(); len(st.Peers) != 0 {
		t.Errorf("a Kumo gone and not shared with is listed: %+v", st.Peers)
	}
}

func TestRemoteFilesNamedAfterTheHost(t *testing.T) {
	p := &peer{id: "host1", name: "Desk PC"}
	files := remoteFiles(p, []*library.LocalFile{
		{Path: "/a/Show - 01.mkv", Dir: "/a", MediaID: 5, Episode: 1, Kind: "main"},
		{Path: "/a/unmatched.mkv"},
		nil,
	})
	if len(files) != 1 {
		t.Fatalf("files %+v", files)
	}
	f := files[0]
	if f.Path != "kumo://host1//a/Show - 01.mkv" || f.Dir != "" || f.Host != "host1" || f.HostName != "Desk PC" {
		t.Errorf("file %+v", f)
	}
}

func TestSharedFilesAreTheMatchedOnes(t *testing.T) {
	d := openDB(t)
	settings, _ := config.NewStore(d)
	lib := fakeLibrary{
		{Path: "/a/1.mkv", MediaID: 5, Episode: 1, Kind: "main"},
		{Path: "/a/2.mkv"}, // not matched
		{Path: "/a/3.mkv", MediaID: 5, Ignored: true}, // ignored
		{Path: "kumo://x//b/1.mkv", MediaID: 6},       // another's
	}
	s, _ := New(d, settings, lib, events.NewHub())
	files, _ := s.SharedFiles()
	if len(files) != 1 || files[0].Path != "/a/1.mkv" {
		t.Errorf("shared %+v", files)
	}
	h := s.Hello(Caller{Allowed: true})
	if !h.Shares || h.Files == "" || h.Files != filesVersion(files) {
		t.Errorf("hello %+v", h)
	}
	if h := s.Hello(Caller{}); h.Shares || h.Files != "" {
		t.Errorf("hello to a Kumo not allowed: %+v", h)
	}
}

// Two Kumos on this computer find each other by multicast, where the
// network allows it.
func TestDiscovery(t *testing.T) {
	a, b := newService(t, openDB(t)), newService(t, openDB(t))
	for _, s := range []*Service{a, b} {
		cfg := s.settings.Get()
		cfg.Sharing.Enabled = true
		if _, err := s.settings.Save(cfg); err != nil {
			t.Fatal(err)
		}
		s.Start()
		t.Cleanup(s.Stop)
	}
	if !a.Status().Listening || !b.Status().Listening {
		t.Skip("can't listen for beacons here")
	}
	if len(interfaces()) == 0 {
		t.Skip("no network interface for multicast")
	}
	deadline := time.Now().Add(12 * time.Second)
	for {
		sa, sb := a.Status(), b.Status()
		if len(sa.Peers) == 1 && sa.Peers[0].ID == b.ID() && len(sb.Peers) == 1 && sb.Peers[0].ID == a.ID() {
			return
		}
		if time.Now().After(deadline) {
			t.Skipf("no multicast between them here (%d, %d peers)", len(sa.Peers), len(sb.Peers))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// A guest forwards its player's requests to the host, signed, and names the
// host's HLS sessions after it.
func TestForwarding(t *testing.T) {
	guest := newService(t, openDB(t))
	hostID, _ := LoadIdentity(openDB(t))
	var got []string
	impostor := false
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		_, sig, err := hostID.verify(r, now)
		if sig != nil && !impostor {
			date := now.UTC().Format(http.TimeFormat)
			reply, _ := sig.reply(date)
			w.Header().Set("Date", date)
			w.Header().Set(HeaderReply, reply)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Range"))
		switch r.URL.Path {
		case "/api/peer/local/file":
			w.Header().Set("Content-Type", "text/html") // never served as a page
			w.Header().Set("Content-Range", "bytes 0-3/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("abcd"))
		case "/api/peer/local/hls":
			_, _ = w.Write([]byte(`{"id":"s1","url":"/api/local/hls/s1/index.m3u8","start":2,"method":"remux"}`))
		case "/api/peer/local/hls/s1/index.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-MAP:URI=\"/api/local/hls/s1/init.mp4\"\n/api/local/hls/s1/seg_00000.m4s\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer host.Close()
	u, _ := url.Parse(host.URL)
	port, _ := strconv.Atoi(u.Port())
	pub, _ := parsePublicKey(hostID.ID, hostID.PublicKey())
	guest.peers[hostID.ID] = &peer{id: hostID.ID, name: "Desk PC", pub: pub, addr: net.ParseIP("127.0.0.1"), port: port, shares: true}
	file := RemotePath(hostID.ID, "/anime/Show - 01.mkv")

	r := httptest.NewRequest("GET", "/api/local/file?"+url.Values{"path": {file}}.Encode(), nil)
	r.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	guest.Forward(w, r, "file")
	if w.Code != http.StatusPartialContent || w.Body.String() != "abcd" || w.Header().Get("Content-Range") != "bytes 0-3/10" || strings.Contains(w.Header().Get("Content-Type"), "html") {
		t.Errorf("file: %d %q %v", w.Code, w.Body, w.Header())
	}
	if len(got) != 1 || got[0] != "GET /api/peer/local/file?path=%2Fanime%2FShow+-+01.mkv bytes=0-3" {
		t.Errorf("the host was asked %q", got)
	}

	sess, err := guest.StartHLS(context.Background(), stream.HLSRequest{Path: file})
	if err != nil {
		t.Fatal(err)
	}
	id := hostID.ID + ".s1"
	if sess.ID != id || sess.URL != "/api/local/hls/"+id+"/index.m3u8" || sess.Start != 2 {
		t.Errorf("session %+v", sess)
	}
	w = httptest.NewRecorder()
	guest.ForwardHLS(w, httptest.NewRequest("GET", sess.URL, nil), id, "index.m3u8")
	want := "#EXTM3U\n#EXT-X-MAP:URI=\"/api/local/hls/" + id + "/init.mp4\"\n/api/local/hls/" + id + "/seg_00000.m4s\n"
	if w.Code != http.StatusOK || w.Body.String() != want {
		t.Errorf("playlist %d:\n%s", w.Code, w.Body)
	}

	// Something else at the host's address (it can't prove it's the host):
	// its answer isn't taken.
	impostor = true
	w = httptest.NewRecorder()
	guest.Forward(w, httptest.NewRequest("GET", "/api/local/file?"+url.Values{"path": {file}}.Encode(), nil), "file")
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "abcd") {
		t.Errorf("an answer that isn't the host's: %d %q", w.Code, w.Body)
	}
	impostor = false
	got = got[:3]

	// Not shared any more: nothing is forwarded.
	guest.peers[hostID.ID].shares = false
	w = httptest.NewRecorder()
	guest.Forward(w, httptest.NewRequest("GET", "/api/local/file?"+url.Values{"path": {file}}.Encode(), nil), "file")
	if w.Code != http.StatusNotFound || len(got) != 3 {
		t.Errorf("after sharing stopped: %d, host asked %q", w.Code, got)
	}
}
