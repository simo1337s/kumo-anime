package share

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/history"
	"github.com/simo1337s/animetest/server/internal/library"
)

const (
	beaconEvery = 5 * time.Second
	// A Kumo not heard from for this long is offline.
	onlineFor = 16 * time.Second
	// How often a guest asks a host whether its library changed.
	askEvery = 30 * time.Second
	// Failed requests in a row after which a host's library is gone.
	maxFails = 3
)

// Library is what a host shares: its library's files.
type Library interface {
	All() ([]*library.LocalFile, error)
}

// History is the watch history (where each episode was stopped), which
// Kumos on the same AniList account keep in step (see syncHistory).
type History interface {
	Since(t int64, limit int) []*history.Entry
	Merge(e history.Entry) (bool, error)
}

type Service struct {
	id       *Identity
	db       *db.DB
	settings *config.Store
	library  Library
	hub      *events.Hub
	api      *http.Client // short requests to the others
	media    *http.Client // forwarded video: no time limit
	slow     *http.Client // requests a host takes long to answer (ani-cli)

	// OnFiles runs when the files shared with this Kumo change (in the
	// background).
	OnFiles func()
	// User is the AniList account this Kumo is logged into (0: none), and
	// History its watch history: kept in step with the Kumos it shares
	// with on the same account. Both nil: not kept in step.
	User    func() int
	History History
	// OnWatchedElsewhere runs when another Kumo on the account says an
	// episode was finished there: AniList has the new progress.
	OnWatchedElsewhere func()
	// AniCliReady reports whether ani-cli runs here, for the Kumos this one
	// shares with (anicli.go).
	AniCliReady func() bool

	// The AniList login, for sharing it with a Kumo (as a host) and using a
	// host's (as a guest, see account.go): its token ("" logged out), and
	// logging in and out. Nil: not shared.
	AccountToken func() string
	UseAccount   func(ctx context.Context, token string) error
	DropAccount  func()

	accountMu sync.Mutex

	pushMu    sync.Mutex
	pending   map[[2]int]history.Entry
	pushTimer *time.Timer

	mu      sync.Mutex
	peers   map[string]*peer
	running *run
	// applyMu: one start or stop at a time (settings saved in a row).
	applyMu sync.Mutex
}

// run is sharing while it's on.
type run struct {
	cancel context.CancelFunc
	disc   *discovery // nil when the network can't be listened on
	wake   chan struct{}
	done   chan struct{}
}

type peer struct {
	id, name, version string
	pub               []byte
	addr              net.IP
	port              int
	seen              time.Time // last heard from (beacon, request or answer)
	lastSeen          time.Time // before this run: when it was last around
	manual            string    // the address it was added with by hand
	allowed           bool      // this Kumo shares its library with it
	// And with it (allowed), this Kumo's AniList login, and downloading
	// onto this Kumo.
	account, downloads bool

	// As a host: whether it shares its library with this Kumo, and its files.
	shares   bool
	files    []*library.LocalFile
	filesVer string
	asked    time.Time
	asking   bool
	fails    int
	// historySince: the newest of its history entries this Kumo has.
	historySince int64
	// user: the AniList account it's logged into, as it told (it shares
	// with this Kumo).
	user int
	// hostAccount: it shares its AniList login with this Kumo (a tag of it,
	// "" when not); hostDownloads: this Kumo may download onto it.
	hostAccount   string
	hostDownloads bool
	// hostAniCli: it runs ani-cli for this Kumo.
	hostAniCli bool
}

func (p *peer) online(now time.Time) bool { return now.Sub(p.seen) < onlineFor }

func (p *peer) lastAround() time.Time {
	if p.seen.After(p.lastSeen) {
		return p.seen
	}
	return p.lastSeen
}

func (p *peer) baseURL() string {
	return "http://" + net.JoinHostPort(p.addr.String(), strconv.Itoa(p.port))
}

// New loads this Kumo's identity and the Kumos it knows.
func New(d *db.DB, settings *config.Store, lib Library, hub *events.Hub) (*Service, error) {
	id, err := LoadIdentity(d)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	// Never through a proxy: the others are on this network.
	transport := func() *http.Transport {
		return &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ResponseHeaderTimeout: time.Minute, MaxIdleConnsPerHost: 4}
	}
	s := &Service{
		id: id, db: d, settings: settings, library: lib, hub: hub,
		api:   &http.Client{Timeout: 15 * time.Second, Transport: transport()},
		media: &http.Client{Transport: transport()},
		slow:  &http.Client{Timeout: 3 * time.Minute, Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, MaxIdleConnsPerHost: 2}},
		peers: map[string]*peer{},
	}
	s.load()
	return s, nil
}

// ID is this Kumo's ID.
func (s *Service) ID() string { return s.id.ID }

// Name is what the others call this Kumo.
func (s *Service) Name() string {
	if n := cleanName(s.settings.Get().Sharing.Name); n != "" {
		return n
	}
	// As the others see it (cleanName keeps names short).
	return cleanName(Hostname())
}

// Hostname is the computer's name, without its domain (the device's, as
// the Android app gives it).
func Hostname() string {
	if n := cleanName(os.Getenv("KUMO_DEVICE_NAME")); n != "" {
		return n
	}
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(h, ".")
	if h == "" {
		return "Kumo"
	}
	return h
}

// ---------------------------------------------------------------------------
// Saved peers

const peersKey = "share:peers"

type savedPeer struct {
	Name    string `json:"name"`
	Key     string `json:"key"`
	Addr    string `json:"addr"`
	Port    int    `json:"port"`
	Manual  string `json:"manual,omitempty"`
	Allowed bool   `json:"allowed,omitempty"`
	Shares  bool   `json:"shares,omitempty"`
	Seen    int64  `json:"seen"`
	// Granted to it (allowed): this Kumo's AniList login, downloading here.
	Account   bool `json:"account,omitempty"`
	Downloads bool `json:"downloads,omitempty"`
	// It takes this Kumo's downloads (it shares, see downloads.go).
	TakesDownloads bool `json:"takesDownloads,omitempty"`
}

func (s *Service) load() {
	var saved map[string]savedPeer
	if ok, _ := s.db.GetKV(peersKey, &saved); !ok {
		return
	}
	for id, sp := range saved {
		pub, err := parsePublicKey(id, sp.Key)
		if err != nil {
			continue
		}
		s.peers[id] = &peer{
			id: id, name: sp.Name, pub: pub, addr: net.ParseIP(sp.Addr), port: sp.Port,
			manual: sp.Manual, allowed: sp.Allowed, shares: sp.Shares, lastSeen: time.Unix(sp.Seen, 0),
			account: sp.Account, downloads: sp.Downloads, hostDownloads: sp.TakesDownloads,
		}
	}
}

// save keeps the Kumos worth knowing next time: those this one shares with,
// those that share with it, and those added by hand. Must be called with
// s.mu held.
func (s *Service) save() {
	out := map[string]savedPeer{}
	for id, p := range s.peers {
		if !p.allowed && !p.shares && p.manual == "" {
			continue
		}
		sp := savedPeer{
			Name: p.name, Key: base64.StdEncoding.EncodeToString(p.pub), Port: p.port, Manual: p.manual, Allowed: p.allowed, Shares: p.shares, Seen: p.lastAround().Unix(),
			Account: p.account, Downloads: p.downloads, TakesDownloads: p.hostDownloads,
		}
		if p.addr != nil {
			sp.Addr = p.addr.String()
		}
		out[id] = sp
	}
	if err := s.db.SetKV(peersKey, out); err != nil {
		log.Printf("sharing: %v", err)
	}
}

// ---------------------------------------------------------------------------
// On and off

// Start follows the settings: sharing runs while it's on.
func (s *Service) Start() {
	s.apply(s.settings.Get())
	s.settings.OnChange(func(old, cur config.Settings) {
		if old.Sharing.Enabled != cur.Sharing.Enabled || old.Sharing.Name != cur.Sharing.Name || old.Server.Port != cur.Server.Port {
			s.apply(cur)
		}
	})
}

// Stop ends sharing (Kumo is quitting).
func (s *Service) Stop() { s.apply(config.Settings{}) }

func (s *Service) apply(cfg config.Settings) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	old := s.running
	s.running = nil
	s.mu.Unlock()
	if old != nil {
		old.cancel()
		if old.disc != nil {
			old.disc.close()
		}
		<-old.done
	}
	if !cfg.Sharing.Enabled {
		if old != nil {
			s.forgetLibraries()
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &run{cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	disc, err := listen()
	if err != nil {
		// Kumos added by address still work.
		log.Printf("sharing: can't listen for other Kumo apps on the network: %v", err)
	} else {
		r.disc = disc
		go disc.read(s.heard)
	}
	s.mu.Lock()
	s.running = r
	s.mu.Unlock()
	go s.loop(ctx, r)
}

// forgetLibraries drops what the hosts shared: sharing is off.
func (s *Service) forgetLibraries() {
	s.mu.Lock()
	changed := false
	for _, p := range s.peers {
		if p.files != nil {
			p.files, p.filesVer = nil, ""
			changed = true
		}
	}
	s.mu.Unlock()
	s.publish(changed)
}

func (s *Service) loop(ctx context.Context, r *run) {
	defer close(r.done)
	announce := time.NewTicker(beaconEvery)
	defer announce.Stop()
	check := time.NewTicker(2 * time.Second)
	defer check.Stop()
	s.announce(r)
	s.askHosts(ctx)
	for beacons := 1; ; {
		select {
		case <-ctx.Done():
			return
		case <-announce.C:
			if beacons++; r.disc != nil && beacons%6 == 0 {
				r.disc.join() // interfaces that came up (every 30s)
			}
			s.announce(r)
		case <-check.C:
		case <-r.wake:
		}
		s.askHosts(ctx)
	}
}

func (s *Service) beacon() beacon {
	cfg := s.settings.Get()
	return beacon{Kumo: protocol, ID: s.id.ID, Name: s.Name(), Port: cfg.Server.Port, Key: s.id.PublicKey(), Version: config.AppVersion}
}

func (s *Service) announce(r *run) {
	if r.disc != nil {
		r.disc.announce(s.beacon())
	}
}

func (s *Service) wake() {
	s.mu.Lock()
	r := s.running
	s.mu.Unlock()
	if r != nil {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}

// heard handles a beacon from another Kumo.
func (s *Service) heard(b beacon, from net.IP) {
	if b.ID == s.id.ID || !isLocalNetwork(from) || b.Port <= 0 || b.Port > 65535 {
		return
	}
	pub, err := parsePublicKey(b.ID, b.Key)
	if err != nil {
		return
	}
	s.mu.Lock()
	r := s.running
	p, known := s.peers[b.ID]
	if !known {
		p = &peer{id: b.ID, pub: pub}
		s.peers[b.ID] = p
	}
	now := time.Now()
	wasOnline := p.online(now)
	p.name, p.version, p.seen = cleanName(b.Name), b.Version, now
	// One added by hand keeps its address.
	if p.manual == "" && (!p.addr.Equal(from) || p.port != b.Port) {
		p.addr, p.port = from, b.Port
		if p.allowed || p.shares {
			s.save()
		}
	}
	// Anyone can repeat a beacon: at most every 2 seconds.
	ask := b.Ask && now.Sub(p.asked) > 2*time.Second
	if ask {
		p.asked = time.Time{}
	}
	s.mu.Unlock()
	if ask {
		s.wake()
	}
	if !wasOnline {
		// Just came: tell it about this one, and ask what it shares.
		if r != nil && r.disc != nil && !b.Reply {
			r.disc.reply(s.beacon(), from)
		}
		s.mu.Lock()
		p.asked = time.Time{}
		s.mu.Unlock()
		s.wake()
		s.hub.Publish("sharing-updated", nil)
	}
}

// ---------------------------------------------------------------------------
// The host's side: who asks

// Caller is another Kumo that made a request, checked.
type Caller struct {
	ID, Name string
	// Allowed: this Kumo shares its library with it.
	Allowed bool
	// SameUser: it's logged into the same AniList account as this one.
	SameUser bool
	// Account: this Kumo shares its AniList login with it; Downloads: it
	// may download onto this Kumo. Both only with Allowed.
	Account, Downloads bool
}

// Verify checks a request from another Kumo, and notes it as seen.
func (s *Service) Verify(r *http.Request) (Caller, error) {
	if !s.settings.Get().Sharing.Enabled {
		return Caller{}, errors.New("library sharing is off")
	}
	ip := remoteIP(r)
	if !isLocalNetwork(ip) {
		return Caller{}, errors.New("not from the local network")
	}
	c, err := s.id.verify(r)
	if err != nil {
		return Caller{}, err
	}
	s.mu.Lock()
	p, known := s.peers[c.id]
	if !known {
		p = &peer{id: c.id, pub: c.pub}
		s.peers[c.id] = p
	}
	wasOnline := p.online(time.Now())
	if c.name != "" {
		p.name = c.name
	}
	// Where it answers, unless it was added by hand.
	if c.port > 0 && c.port <= 65535 && p.manual == "" {
		p.addr, p.port = ip, c.port
	}
	p.seen = time.Now()
	caller := Caller{ID: p.id, Name: p.name, Allowed: p.allowed, Account: p.allowed && p.account, Downloads: p.allowed && p.downloads}
	s.mu.Unlock()
	if !wasOnline {
		s.hub.Publish("sharing-updated", nil)
	}
	me := s.user()
	caller.SameUser = me > 0 && c.user == me
	return caller, nil
}

func (s *Service) user() int {
	if s.User == nil {
		return 0
	}
	return s.User()
}

// HistorySince is this Kumo's watch history after t, for another Kumo on
// the same account (checked by the caller).
func (s *Service) HistorySince(t int64) []*history.Entry {
	if s.History == nil {
		return []*history.Entry{}
	}
	out := s.History.Since(t, historyBatch)
	if out == nil {
		out = []*history.Entry{}
	}
	return out
}

const historyBatch = 5000

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// Hello is the host's answer to a guest asking what it shares.
type Hello struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Key     string `json:"key"`
	Version string `json:"version"` // Kumo's
	Shares  bool   `json:"shares"`
	// Files changes when the shared files do.
	Files string `json:"files,omitempty"`
	// User: the AniList account it's logged into, told to those it shares
	// with (their watch histories are kept in step on the same account).
	User int `json:"user,omitempty"`
	// Account: it shares its AniList login with the guest. A tag of it, which
	// changes with it; the login itself is asked for, sealed (account.go).
	Account string `json:"account,omitempty"`
	// Downloads: the guest may download onto it (downloads.go).
	Downloads bool `json:"downloads,omitempty"`
	// AniCli: it runs ani-cli for the guest (anicli.go).
	AniCli bool `json:"aniCli,omitempty"`
}

// Hello answers a guest (verified, see Verify).
func (s *Service) Hello(c Caller) Hello {
	h := Hello{ID: s.id.ID, Name: s.Name(), Key: s.id.PublicKey(), Version: config.AppVersion, Shares: c.Allowed}
	if c.Allowed {
		files, _ := s.SharedFiles()
		h.Files = filesVersion(files)
		h.User = s.user()
		if c.Account {
			h.Account = accountTag(s.accountToken())
		}
		h.Downloads = c.Downloads
		h.AniCli = s.AniCliReady != nil && s.AniCliReady()
	}
	return h
}

// Whoami is what anyone on the network may ask: who this Kumo is (what its
// beacons say).
func (s *Service) Whoami() Hello {
	return Hello{ID: s.id.ID, Name: s.Name(), Key: s.id.PublicKey(), Version: config.AppVersion}
}

// SharedFiles are the library's files this Kumo shares: those matched to an
// anime.
func (s *Service) SharedFiles() ([]*library.LocalFile, error) {
	all, err := s.library.All()
	if err != nil {
		return nil, err
	}
	out := make([]*library.LocalFile, 0, len(all))
	for _, f := range all {
		if f.MediaID != 0 && !f.Ignored && !IsRemote(f.Path) {
			out = append(out, f)
		}
	}
	return out, nil
}

func filesVersion(files []*library.LocalFile) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s|%d|%d|%d|%s|%d\n", f.Path, f.MediaID, f.Episode, f.Size, f.Kind, f.ModTime)
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// ---------------------------------------------------------------------------
// The guest's side: asking the hosts

func (s *Service) askHosts(ctx context.Context) {
	now := time.Now()
	s.mu.Lock()
	var due []*peer
	for _, p := range s.peers {
		if p.asking || p.addr == nil || p.port == 0 {
			continue
		}
		// Hosts that are around, or that were: until they stop answering.
		if !p.online(now) && p.manual == "" && p.files == nil && !p.shares {
			continue
		}
		wait := askEvery
		if p.fails > 0 {
			wait = 10 * time.Second
		}
		if now.Sub(p.asked) >= wait {
			p.asking, p.asked = true, now
			due = append(due, p)
		}
	}
	s.mu.Unlock()
	for _, p := range due {
		go s.ask(ctx, p)
	}
}

// ask asks a host whether it shares its library with this Kumo, and for its
// files when they changed.
func (s *Service) ask(ctx context.Context, p *peer) {
	h, err := s.hello(ctx, p)
	var files []*library.LocalFile
	if err == nil && h.Shares {
		s.mu.Lock()
		same := h.Files == p.filesVer && p.files != nil
		s.mu.Unlock()
		if !same {
			files, err = s.fetchFiles(ctx, p)
		}
	}
	s.mu.Lock()
	p.asking = false
	if ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	changed, visible := false, false
	if err != nil {
		p.fails++
		if p.fails == maxFails {
			visible = true
			if p.files != nil {
				p.files, p.filesVer, changed = nil, "", true
			}
		}
		first, name, base := p.fails == 1, p.name, p.baseURL()
		s.mu.Unlock()
		if first {
			log.Printf("sharing: %s (%s): %v", name, base, err)
		}
		s.publish(changed)
		if visible {
			s.hub.Publish("sharing-updated", nil)
		}
		return
	}
	visible = p.fails > 0 || p.shares != h.Shares || (p.name != h.Name && h.Name != "") || !p.online(time.Now())
	p.fails, p.seen = 0, time.Now()
	if h.Name != "" {
		p.name = cleanName(h.Name)
	}
	p.version = h.Version
	if p.shares != h.Shares {
		p.shares = h.Shares
		s.save()
	}
	switch {
	case !h.Shares && p.files != nil:
		p.files, p.filesVer, changed = nil, "", true
	case h.Shares && files != nil:
		p.files, p.filesVer, changed = remoteFiles(p, files), h.Files, true
	}
	s.mu.Unlock()
	s.publish(changed)
	if visible || changed {
		s.hub.Publish("sharing-updated", nil)
	}
	s.mu.Lock()
	p.user = 0
	tag, takes := "", false
	if h.Shares {
		p.user = h.User
		tag, takes = h.Account, h.Downloads
	}
	p.hostAniCli = h.Shares && h.AniCli
	accountChanged := p.hostAccount != tag
	took := p.hostDownloads
	p.hostAccount, p.hostDownloads = tag, takes
	if took != takes {
		s.save()
	}
	s.mu.Unlock()
	s.followAccount(ctx, p, tag)
	if took != takes {
		s.downloadsGranted(p, takes)
	}
	if accountChanged || took != takes {
		s.hub.Publish("sharing-updated", nil)
	}
	if h.Shares && h.User > 0 && h.User == s.user() {
		s.syncHistory(ctx, p)
	}
}

// HistoryChanged sends a position this Kumo saved to the Kumos that share
// with it on the same AniList account (the one whose files are played,
// most of all), as it happens: their Continue watching goes on from it.
// Gathered for 2 seconds; at once when the episode is over.
func (s *Service) HistoryChanged(e history.Entry) {
	s.mu.Lock()
	on := s.running != nil
	s.mu.Unlock()
	if !on || s.user() == 0 {
		return
	}
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if s.pending == nil {
		s.pending = map[[2]int]history.Entry{}
	}
	s.pending[[2]int{e.MediaID, e.Episode}] = e
	wait := 2 * time.Second
	if e.Percent() >= 0.85 {
		wait = 0
	}
	if s.pushTimer == nil {
		s.pushTimer = time.AfterFunc(wait, s.pushHistory)
	} else if wait == 0 {
		s.pushTimer.Reset(0)
	}
}

func (s *Service) pushHistory() {
	s.pushMu.Lock()
	entries := make([]history.Entry, 0, len(s.pending))
	for _, e := range s.pending {
		entries = append(entries, e)
	}
	s.pending, s.pushTimer = nil, nil
	s.pushMu.Unlock()
	me := s.user()
	if len(entries) == 0 || me == 0 {
		return
	}
	s.mu.Lock()
	var to []*peer
	for _, p := range s.peers {
		if p.shares && p.user == me && p.addr != nil && p.fails < maxFails {
			to = append(to, p)
		}
	}
	s.mu.Unlock()
	body, _ := json.Marshal(entries)
	for _, p := range to {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, err := s.request(ctx, p, http.MethodPost, "/api/peer/history", bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if resp, err := s.api.Do(req); err == nil {
				resp.Body.Close()
			}
		}()
	}
}

// TakeHistory merges positions another Kumo on the same account sent
// (checked by the caller), the newer ones.
func (s *Service) TakeHistory(entries []history.Entry) {
	if s.History == nil {
		return
	}
	merged, finished := 0, false
	for _, e := range entries {
		if ok, err := s.History.Merge(e); err == nil && ok {
			merged++
			finished = finished || e.Percent() >= 0.85
		}
	}
	if merged > 0 {
		s.hub.Publish("history-updated", nil)
	}
	if finished && s.OnWatchedElsewhere != nil {
		go s.OnWatchedElsewhere()
	}
}

// syncHistory takes what's newer in a host's watch history, both being
// logged into the same AniList account: where each episode was stopped,
// and when it was watched. Continue watching then goes on from there,
// to the second; the list itself (progress) is AniList's.
func (s *Service) syncHistory(ctx context.Context, p *peer) {
	if s.History == nil {
		return
	}
	s.mu.Lock()
	since := p.historySince
	s.mu.Unlock()
	var entries []*history.Entry
	if err := s.getJSON(ctx, p, "/api/peer/history?since="+strconv.FormatInt(since, 10), &entries); err != nil {
		return
	}
	merged := 0
	newest := since
	for _, e := range entries {
		if e == nil {
			continue
		}
		if ok, err := s.History.Merge(*e); err == nil && ok {
			merged++
		}
		newest = max(newest, e.UpdatedAt)
	}
	s.mu.Lock()
	p.historySince = max(p.historySince, newest)
	s.mu.Unlock()
	if merged > 0 {
		s.hub.Publish("history-updated", nil)
	}
}

// publish tells the app the shared files changed.
func (s *Service) publish(changed bool) {
	if changed {
		s.hub.Publish("library-updated", nil)
		if s.OnFiles != nil {
			go s.OnFiles()
		}
	}
}

// request makes a signed request to a host.
func (s *Service) request(ctx context.Context, p *peer, method, path string, body io.Reader) (*http.Request, error) {
	s.mu.Lock()
	base, host, pub := p.baseURL(), p.id, p.pub
	s.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	if err := s.id.sign(req.Header, host, pub, s.Name(), s.settings.Get().Server.Port, s.user()); err != nil {
		return nil, err
	}
	return req, nil
}

// Sign signs a request to the Kumo with that ID and public key (as its
// whoami tells them).
func (s *Service) Sign(req *http.Request, hostID, hostKey string) error {
	pub, err := parsePublicKey(hostID, hostKey)
	if err != nil {
		return err
	}
	return s.id.sign(req.Header, hostID, pub, s.Name(), s.settings.Get().Server.Port, s.user())
}

func (s *Service) getJSON(ctx context.Context, p *peer, path string, out any) error {
	req, err := s.request(ctx, p, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	resp, err := s.api.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

func (s *Service) hello(ctx context.Context, p *peer) (*Hello, error) {
	var h Hello
	if err := s.getJSON(ctx, p, "/api/peer/hello", &h); err != nil {
		return nil, err
	}
	// Another Kumo at that address now (a new IP address from the router).
	if h.ID != p.id {
		return nil, fmt.Errorf("another Kumo (%s) answers there", h.Name)
	}
	return &h, nil
}

func (s *Service) fetchFiles(ctx context.Context, p *peer) ([]*library.LocalFile, error) {
	var files []*library.LocalFile
	if err := s.getJSON(ctx, p, "/api/peer/files", &files); err != nil {
		return nil, err
	}
	if files == nil {
		files = []*library.LocalFile{}
	}
	return files, nil
}

// remoteFiles names a host's files after it (kumo://<host>/<path>).
func remoteFiles(p *peer, files []*library.LocalFile) []*library.LocalFile {
	out := make([]*library.LocalFile, 0, len(files))
	for _, f := range files {
		if f == nil || f.Path == "" || f.MediaID == 0 {
			continue
		}
		c := *f
		c.Path = RemotePath(p.id, f.Path)
		c.Dir = ""
		c.Host, c.HostName = p.id, p.name
		out = append(out, &c)
	}
	return out
}

// ---------------------------------------------------------------------------
// Shared files' paths

const scheme = "kumo://"

// RemotePath is the path of a host's file.
func RemotePath(host, path string) string { return scheme + host + "/" + path }

// IsRemote reports a path of another Kumo's file.
func IsRemote(path string) bool { return strings.HasPrefix(path, scheme) }

// ParsePath splits a shared file's path into its host and its path there.
func ParsePath(path string) (host, onHost string, ok bool) {
	rest, ok := strings.CutPrefix(path, scheme)
	if !ok {
		return "", "", false
	}
	host, onHost, ok = strings.Cut(rest, "/")
	if !ok || host == "" || onHost == "" {
		return "", "", false
	}
	return host, onHost, true
}

// ---------------------------------------------------------------------------
// What the app shows

// PeerView is another Kumo on the network, for Settings.
type PeerView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"lastSeen"`
	Version  string `json:"version"`
	// Allowed: this Kumo shares its library with it.
	Allowed bool `json:"allowed"`
	// Shares: it shares its library with this Kumo (Files of them).
	Shares bool   `json:"shares"`
	Files  int    `json:"files"`
	Manual bool   `json:"manual"`
	Error  string `json:"error,omitempty"`
	// Granted to it (with Allowed): this Kumo's AniList login, downloading
	// onto this Kumo.
	Account   bool `json:"account"`
	Downloads bool `json:"downloads"`
	// Granted by it (it shares): its AniList login, and whether this Kumo
	// is using it; downloading onto it.
	SharesAccount  bool `json:"sharesAccount"`
	UsingAccount   bool `json:"usingAccount"`
	TakesDownloads bool `json:"takesDownloads"`
}

func (s *Service) view(p *peer, now time.Time) PeerView {
	v := PeerView{
		ID: p.id, Name: p.name, Online: p.online(now), LastSeen: p.lastAround().Unix(), Version: p.version,
		Allowed: p.allowed, Shares: p.shares, Files: len(p.files), Manual: p.manual != "",
		Account: p.account, Downloads: p.downloads,
		SharesAccount: p.shares && p.hostAccount != "", TakesDownloads: p.shares && p.hostDownloads,
	}
	if p.addr != nil {
		v.Address = net.JoinHostPort(p.addr.String(), strconv.Itoa(p.port))
	}
	if v.Name == "" {
		v.Name = "Kumo " + p.id[:6]
	}
	if p.fails >= maxFails {
		v.Error = "Not answering"
	}
	return v
}

// Status is sharing as Settings shows it.
type Status struct {
	Enabled bool       `json:"enabled"`
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Peers   []PeerView `json:"peers"`
	// Listening: other Kumos can find this one (the network port could be
	// opened).
	Listening bool `json:"listening"`
	// Addresses: where the others reach this Kumo (to add it by hand).
	Addresses []string `json:"addresses"`
}

func (s *Service) Status() Status {
	now := time.Now()
	using := s.AccountHost()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Enabled: s.running != nil, ID: s.id.ID, Name: s.Name(), Listening: s.running != nil && s.running.disc != nil, Peers: []PeerView{}, Addresses: addresses(s.settings.Get().Server.Port)}
	for _, p := range s.peers {
		// Those never heard from again, nor useful, aren't listed.
		if !p.online(now) && !p.allowed && !p.shares && p.manual == "" {
			continue
		}
		v := s.view(p, now)
		v.UsingAccount = v.SharesAccount && p.id == using
		st.Peers = append(st.Peers, v)
	}
	sort.Slice(st.Peers, func(i, j int) bool {
		a, b := st.Peers[i], st.Peers[j]
		if a.Online != b.Online {
			return a.Online
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return st
}

// addresses are this computer's addresses on the home network.
func addresses(port int) []string {
	out := []string{}
	for _, ifi := range interfaces() {
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && isLocalNetwork(ipn.IP) && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, net.JoinHostPort(ipn.IP.String(), strconv.Itoa(port)))
			}
		}
	}
	return out
}

// Grants is what this Kumo shares with another one: its library, and with
// it its AniList login and downloading onto this Kumo. Nil: unchanged.
type Grants struct {
	Allowed   *bool `json:"allowed"`
	Account   *bool `json:"account"`
	Downloads *bool `json:"downloads"`
}

// SetAllowed shares this Kumo's library with another one, or stops.
func (s *Service) SetAllowed(id string, allowed bool) error {
	return s.SetGrants(id, Grants{Allowed: &allowed})
}

// SetGrants changes what this Kumo shares with another one.
func (s *Service) SetGrants(id string, g Grants) error {
	s.mu.Lock()
	p, ok := s.peers[id]
	if ok {
		if g.Allowed != nil {
			p.allowed = *g.Allowed
		}
		if g.Account != nil {
			p.account = *g.Account
		}
		if g.Downloads != nil {
			p.downloads = *g.Downloads
		}
		s.save()
	}
	s.mu.Unlock()
	if !ok {
		return errors.New("no Kumo app with that ID")
	}
	s.hub.Publish("sharing-updated", nil)
	// Tell it to ask again now (it does every 30s anyway).
	s.mu.Lock()
	r := s.running
	s.mu.Unlock()
	if r != nil && r.disc != nil && p.addr != nil {
		b := s.beacon()
		b.Ask = true
		r.disc.reply(b, p.addr)
	}
	return nil
}

// Refresh asks the hosts again what they share, now (Settings or the
// library page is open).
func (s *Service) Refresh() {
	now := time.Now()
	s.mu.Lock()
	for _, p := range s.peers {
		if now.Sub(p.asked) > 3*time.Second {
			p.asked = time.Time{}
		}
	}
	s.mu.Unlock()
	s.wake()
}

// Forget removes a Kumo from the list (one added by hand, or gone).
func (s *Service) Forget(id string) {
	s.mu.Lock()
	p, ok := s.peers[id]
	if ok {
		delete(s.peers, id)
		s.save()
	}
	s.mu.Unlock()
	if ok {
		s.publish(p.files != nil)
		s.hub.Publish("sharing-updated", nil)
	}
}

// Connect adds a Kumo by its address, for networks where they can't find
// each other by themselves (it needs sharing on too).
func (s *Service) Connect(ctx context.Context, address string) (PeerView, error) {
	address = strings.TrimSpace(address)
	address = strings.TrimPrefix(strings.TrimPrefix(address, "http://"), "https://")
	address = strings.TrimSuffix(address, "/")
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		host, portStr = address, strconv.Itoa(config.DefaultPort)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return PeerView{}, errors.New("that isn't an address like 192.168.1.20 or 192.168.1.20:43211")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return PeerView{}, fmt.Errorf("can't find %s on the network", host)
	}
	ip := ips[0]
	if !isLocalNetwork(ip) {
		return PeerView{}, errors.New("only computers on your home network can share")
	}
	probe := &peer{addr: ip, port: port}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.baseURL()+"/api/peer/whoami", nil)
	if err != nil {
		return PeerView{}, err
	}
	resp, err := s.api.Do(req)
	if err != nil {
		return PeerView{}, fmt.Errorf("no answer from %s: is Kumo open there, with library sharing on?", address)
	}
	defer resp.Body.Close()
	var h Hello
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&h) != nil {
		return PeerView{}, fmt.Errorf("%s isn't a Kumo app with library sharing on", address)
	}
	if h.ID == s.id.ID {
		return PeerView{}, errors.New("that's this Kumo")
	}
	pub, err := parsePublicKey(h.ID, h.Key)
	if err != nil {
		return PeerView{}, err
	}
	s.mu.Lock()
	p, ok := s.peers[h.ID]
	if !ok {
		p = &peer{id: h.ID, pub: pub}
		s.peers[h.ID] = p
	}
	p.name, p.version, p.addr, p.port, p.seen = cleanName(h.Name), h.Version, ip, port, time.Now()
	p.manual = net.JoinHostPort(host, strconv.Itoa(port))
	p.asked, p.fails = time.Time{}, 0
	s.save()
	v := s.view(p, time.Now())
	s.mu.Unlock()
	s.wake()
	s.hub.Publish("sharing-updated", nil)
	return v, nil
}

// SharedLibrary is a host's shared library.
type SharedLibrary struct {
	Host  PeerView             `json:"host"`
	Files []*library.LocalFile `json:"files"`
}

// Libraries are the libraries shared with this Kumo.
func (s *Service) Libraries() []SharedLibrary {
	now := time.Now()
	using := s.AccountHost()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []SharedLibrary{}
	for _, p := range s.peers {
		if p.files != nil {
			v := s.view(p, now)
			v.UsingAccount = v.SharesAccount && p.id == using
			out = append(out, SharedLibrary{Host: v, Files: p.files})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Host.Name) < strings.ToLower(out[j].Host.Name) })
	return out
}

// RemoteFiles are all the files shared with this Kumo.
func (s *Service) RemoteFiles() []*library.LocalFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*library.LocalFile
	for _, p := range s.peers {
		out = append(out, p.files...)
	}
	return out
}

// File is a shared file, by its kumo:// path.
func (s *Service) File(path string) (*library.LocalFile, bool) {
	host, _, ok := ParsePath(path)
	if !ok {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.peers[host]
	if p == nil {
		return nil, false
	}
	i := slices.IndexFunc(p.files, func(f *library.LocalFile) bool { return f.Path == path })
	if i < 0 {
		return nil, false
	}
	return p.files[i], true
}

// nameOf is a peer's name.
func (s *Service) nameOf(p *peer) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return p.name
}
