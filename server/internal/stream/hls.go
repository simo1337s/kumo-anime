package stream

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/util"
)

// HLS serves converted (remuxed or transcoded) library files as HLS, a
// playlist of short fMP4 segments, for the browsers that can't play the
// progressive stream of ServeTranscode: Safari, and every browser on iPhone
// and iPad, only play video they can fetch in byte ranges.
//
// A session is one file for one player from one position. ffmpeg writes its
// segments into the session's folder while the player reads them. It's
// paused (suspended) when it gets far ahead of the player, and the session is removed
// when the player stops it, starts another one, or goes away.
type HLS struct {
	local *Local
	root  string

	mu       sync.Mutex
	sessions map[string]*hlsSession
	closed   bool
	quit     chan struct{}
}

type hlsSession struct {
	id, key, dir string
	cancel       context.CancelFunc
	cmd          *exec.Cmd
	stderr       *headWriter
	done         chan struct{} // closed when ffmpeg exits
	err          error         // how ffmpeg exited, once done

	mu        sync.Mutex
	lastUse   time.Time
	requested int // the furthest segment the player asked for
	paused    bool
}

const (
	hlsSegmentSeconds = 4
	hlsMaxSessions    = 4
	hlsIdle           = 20 * time.Minute
	hlsAhead          = 45 // segments ffmpeg may get ahead of the player (3 minutes)
	hlsReadyTimeout   = 60 * time.Second
)

// NewHLS keeps its sessions in root, which it empties first.
func NewHLS(local *Local, root string) *HLS {
	_ = os.RemoveAll(root) // sessions of a previous run
	h := &HLS{local: local, root: root, sessions: map[string]*hlsSession{}, quit: make(chan struct{})}
	go h.janitor()
	return h
}

type HLSRequest struct {
	Path   string   `json:"path"`
	Start  float64  `json:"start"`
	Audio  int      `json:"audio"`
	Method string   `json:"method"`
	Caps   []string `json:"caps"`
	// Client is one player. Its sessions are its own: when it starts another
	// one (after a seek, or to switch audio) the old one is stopped, and no
	// other device can be cut off.
	Client string `json:"client"`
}

type HLSSession struct {
	ID     string  `json:"id"`
	URL    string  `json:"url"`
	Start  float64 `json:"start"`  // where the stream really starts (see SeekPoint)
	Method string  `json:"method"` // remux | transcode
}

// Start starts a session, or returns the player's session for the same
// request, once its first segment is ready.
func (h *HLS) Start(ctx context.Context, req HLSRequest) (*HLSSession, error) {
	file, err := h.local.resolve(req.Path)
	if err != nil {
		return nil, err
	}
	p, err := h.local.Probe(ctx, file)
	if err != nil {
		return nil, err
	}
	pl := h.local.plan(p, req.Method, req.Audio, CapsOf(req.Caps), true)
	start := h.local.startFor(ctx, file, pl, req.Start)
	owner := req.Client + "\x00"
	key := fmt.Sprintf("%s%s\x00%.3f\x00%+v", owner, file, start, pl)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("Kumo is shutting down")
	}
	var s *hlsSession
	var old []*hlsSession
	for _, x := range h.sessions {
		switch {
		case x.key == key && !x.failed():
			s = x
		case req.Client != "" && strings.HasPrefix(x.key, owner):
			old = append(old, x)
		}
	}
	if s == nil {
		if s, err = h.spawn(key, file, p, pl, start); err != nil {
			h.mu.Unlock()
			return nil, err
		}
	}
	s.touch()
	h.mu.Unlock()
	for _, x := range old {
		h.remove(x)
	}
	h.evict()

	if err := s.waitReady(ctx); err != nil {
		h.remove(s)
		return nil, err
	}
	return &HLSSession{ID: s.id, URL: "/api/local/hls/" + s.id + "/index.m3u8", Start: start, Method: pl.method}, nil
}

// spawn starts ffmpeg for a new session. h.mu is held.
func (h *HLS) spawn(key, file string, p *Probe, pl plan, start float64) (*hlsSession, error) {
	id := newSessionID()
	dir := filepath.Join(h.root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	cfg := h.local.settings.Get()
	args := pl.args(cfg, p, file, start)
	if !pl.videoCopy {
		// A keyframe where each segment starts, so segments play on their own.
		args = append(args, "-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", hlsSegmentSeconds))
	}
	args = append(args,
		"-f", "hls", "-hls_time", strconv.Itoa(hlsSegmentSeconds), "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "seg_%05d.m4s"),
		"-hls_flags", "independent_segments+temp_file",
		filepath.Join(dir, "index.m3u8"))
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, cfg.Transcode.FfmpegPath, args...)
	s := &hlsSession{id: id, key: key, dir: dir, cancel: cancel, cmd: cmd, stderr: &headWriter{max: 8 << 10}, done: make(chan struct{}), lastUse: time.Now(), requested: -1}
	cmd.Stderr = s.stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("ffmpeg is not installed: %w", err)
	}
	go func() {
		s.err = cmd.Wait()
		close(s.done)
	}()
	h.sessions[id] = s
	return s, nil
}

func newSessionID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var reSegmentName = regexp.MustCompile(`^(?:init\.mp4|seg_\d{5,}\.m4s)$`)

// Serve serves a session's playlist or one of its segments.
func (h *HLS) Serve(w http.ResponseWriter, r *http.Request, id, name string) {
	s := h.get(id)
	if s == nil {
		http.Error(w, "this stream has ended", http.StatusNotFound)
		return
	}
	s.touch()
	if name == "index.m3u8" {
		b, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			http.Error(w, "the stream isn't ready", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(sessionPlaylist(b, "/api/local/hls/"+id+"/"))
		return
	}
	if !reSegmentName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "seg_"), ".m4s")); err == nil {
		s.mu.Lock()
		s.requested = max(s.requested, n)
		s.mu.Unlock()
		s.throttle()
	}
	f, err := os.Open(filepath.Join(s.dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

var reMapURI = regexp.MustCompile(`URI="([^"]*)"`)

// sessionPlaylist points the playlist at the session's URLs and has players
// start at its beginning: the playlist grows while ffmpeg works, and players
// otherwise start a growing (live) playlist at its end.
func sessionPlaylist(b []byte, prefix string) []byte {
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(b), "\r\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "#EXTM3U":
			line += "\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES"
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			line = reMapURI.ReplaceAllStringFunc(line, func(m string) string {
				return `URI="` + prefix + path.Base(reMapURI.FindStringSubmatch(m)[1]) + `"`
			})
		case line != "" && !strings.HasPrefix(line, "#"):
			line = prefix + path.Base(line)
		}
		out.WriteString(line + "\n")
	}
	return []byte(out.String())
}

// Stop ends a session.
func (h *HLS) Stop(id string) {
	if s := h.get(id); s != nil {
		h.remove(s)
	}
}

// Close ends every session, when Kumo quits.
func (h *HLS) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	close(h.quit)
	all := make([]*hlsSession, 0, len(h.sessions))
	for _, s := range h.sessions {
		all = append(all, s)
	}
	h.mu.Unlock()
	for _, s := range all {
		h.remove(s)
	}
}

func (h *HLS) get(id string) *hlsSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[id]
}

func (h *HLS) remove(s *hlsSession) {
	h.mu.Lock()
	if h.sessions[s.id] != s {
		h.mu.Unlock()
		return
	}
	delete(h.sessions, s.id)
	h.mu.Unlock()
	s.cancel() // kills ffmpeg, also when it's paused
	<-s.done
	_ = os.RemoveAll(s.dir)
}

// evict removes the least recently used sessions over the limit.
func (h *HLS) evict() {
	for {
		h.mu.Lock()
		var oldest *hlsSession
		if len(h.sessions) > hlsMaxSessions {
			for _, s := range h.sessions {
				if oldest == nil || s.lastUsed().Before(oldest.lastUsed()) {
					oldest = s
				}
			}
		}
		h.mu.Unlock()
		if oldest == nil {
			return
		}
		h.remove(oldest)
	}
}

// janitor removes sessions nobody uses any more and keeps ffmpeg throttled
// while the player is paused.
func (h *HLS) janitor() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-h.quit:
			return
		case <-t.C:
		}
		h.mu.Lock()
		var idle, active []*hlsSession
		for _, s := range h.sessions {
			if time.Since(s.lastUsed()) > hlsIdle {
				idle = append(idle, s)
			} else {
				active = append(active, s)
			}
		}
		h.mu.Unlock()
		for _, s := range idle {
			h.remove(s)
		}
		for _, s := range active {
			s.throttle()
		}
	}
}

func (s *hlsSession) touch() {
	s.mu.Lock()
	s.lastUse = time.Now()
	s.mu.Unlock()
}

func (s *hlsSession) lastUsed() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUse
}

// failed reports whether ffmpeg exited with an error.
func (s *hlsSession) failed() bool {
	select {
	case <-s.done:
		return s.err != nil
	default:
		return false
	}
}

// segments counts the segments in the playlist so far, and reports whether
// it's complete.
func (s *hlsSession) segments() (int, bool) {
	b, err := os.ReadFile(filepath.Join(s.dir, "index.m3u8"))
	if err != nil {
		return 0, false
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			n++
		}
	}
	return n, bytes.Contains(b, []byte("#EXT-X-ENDLIST"))
}

// waitReady waits for the first segment, and then a moment for a few more:
// players only read the growing playlist again after a segment's length,
// which for copied video is a whole keyframe interval (often 10 seconds).
func (s *hlsSession) waitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, hlsReadyTimeout)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var first time.Time
	for {
		n, complete := s.segments()
		if n > 0 && first.IsZero() {
			first = time.Now()
		}
		if complete || n >= 3 || n > 0 && time.Since(first) > 1500*time.Millisecond {
			return nil
		}
		select {
		case <-s.done:
			if n, complete := s.segments(); n > 0 || complete {
				return nil
			}
			msg := strings.TrimSpace(string(s.stderr.buf))
			if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
				msg = msg[i+1:]
			}
			if msg == "" && s.err != nil {
				msg = s.err.Error()
			}
			return fmt.Errorf("ffmpeg couldn't convert the video: %s", msg)
		case <-ctx.Done():
			return errors.New("ffmpeg took too long to start the video")
		case <-tick.C:
		}
	}
}

// throttle pauses ffmpeg when it's far ahead of the player and resumes it
// when the player catches up, so a paused or abandoned player doesn't have
// it convert, and store, the whole episode.
func (s *hlsSession) throttle() {
	select {
	case <-s.done:
		return
	default:
	}
	written, _ := s.segments()
	s.mu.Lock()
	defer s.mu.Unlock()
	ahead := written - 1 - s.requested
	switch {
	case !s.paused && ahead > hlsAhead:
		if util.Suspend(s.cmd.Process) == nil {
			s.paused = true
		}
	case s.paused && ahead < hlsAhead/2:
		if util.Resume(s.cmd.Process) == nil {
			s.paused = false
		}
	}
}
