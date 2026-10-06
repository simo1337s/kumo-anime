package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/history"
)

// PlayRequest describes something to play.
type PlayRequest struct {
	MediaID  int               `json:"mediaId"`
	Episode  int               `json:"episode"`
	Title    string            `json:"title"`
	Source   string            `json:"source"` // local | anicli | stream | torrent
	Target   string            `json:"target"` // file path or URL
	Headers  map[string]string `json:"headers,omitempty"`
	Referrer string            `json:"referrer,omitempty"`
	SubFiles []string          `json:"subFiles,omitempty"`
	// Explicit start position; nil means "resume from history".
	Start *float64 `json:"start,omitempty"`
}

// Session is the state of the current playback (sent to the UI).
type Session struct {
	ID              string         `json:"id"`
	MediaID         int            `json:"mediaId"`
	Episode         int            `json:"episode"`
	Title           string         `json:"title"`
	Source          string         `json:"source"`
	Player          string         `json:"player"`
	Target          string         `json:"target"`
	Position        float64        `json:"position"`
	Duration        float64        `json:"duration"`
	Paused          bool           `json:"paused"`
	Active          bool           `json:"active"`
	ProgressUpdated bool           `json:"progressUpdated"`
	Tracks          []Track        `json:"tracks"`
	Skips           []SkipInterval `json:"skips"`
	StartedAt       int64          `json:"startedAt"`

	finished  bool
	lastSave  time.Time
	lastEmit  time.Time
	skipped   map[string]bool
	guardTill time.Time
	req       PlayRequest
}

// NextResolver finds what to play after a session ended (auto play next).
type NextResolver func(ctx context.Context, s *Session) (*PlayRequest, error)

// ProgressHook is called when an episode crosses the completion threshold.
type ProgressHook func(mediaID, episode int)

type Manager struct {
	settings *config.Store
	history  *history.Store
	Tracks   *TrackStore
	platform *anilist.Platform
	hub      *events.Hub
	db       *db.DB

	NextResolver NextResolver
	OnProgress   []ProgressHook
	OnStatus     []func(s *Session)

	mu  sync.Mutex
	cur *Session
	mpv *Mpv
}

func NewManager(s *config.Store, h *history.Store, d *db.DB, p *anilist.Platform, hub *events.Hub) *Manager {
	return &Manager{settings: s, history: h, Tracks: NewTrackStore(d), platform: p, hub: hub, db: d}
}

// Status returns a copy of the current session (nil if nothing plays).
func (m *Manager) Status() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return nil
	}
	c := *m.cur
	return &c
}

func (m *Manager) emit(s *Session, force bool) {
	if !force && time.Since(s.lastEmit) < 900*time.Millisecond {
		return
	}
	s.lastEmit = time.Now()
	c := *s
	c.Tracks = append([]Track(nil), s.Tracks...)
	m.hub.Publish(events.PlaybackStatus, &c)
	for _, fn := range m.OnStatus {
		go fn(&c)
	}
}

// resumeFor returns the start position for a request.
func (m *Manager) resumeFor(req PlayRequest) float64 {
	if req.Start != nil {
		return *req.Start
	}
	if !m.settings.Get().Playback.ResumePlayback || req.MediaID <= 0 {
		return 0
	}
	return m.history.ResumePosition(req.MediaID, req.Episode)
}

// PlayMpv opens the request in mpv and tracks it.
func (m *Manager) PlayMpv(req PlayRequest) (*Session, error) {
	cfg := m.settings.Get()
	m.Stop()

	start := m.resumeFor(req)
	opts := LaunchOptions{
		Target:     req.Target,
		Title:      req.Title,
		Start:      start,
		Headers:    req.Headers,
		Referrer:   req.Referrer,
		SubFiles:   req.SubFiles,
		AudioLang:  cfg.Playback.PreferredAudioLang,
		SubLang:    cfg.Playback.PreferredSubLang,
		Fullscreen: cfg.Mpv.Fullscreen,
		ExtraArgs:  strings.Fields(cfg.Mpv.ExtraArgs),
	}
	mpv, err := LaunchMpv(cfg.Mpv.Path, opts)
	if err != nil {
		return nil, err
	}
	s := &Session{
		ID: uuid.NewString(), MediaID: req.MediaID, Episode: req.Episode, Title: req.Title,
		Source: req.Source, Player: "mpv", Target: req.Target, Position: start, Active: true,
		StartedAt: time.Now().Unix(), skipped: map[string]bool{}, req: req,
	}
	m.mu.Lock()
	m.cur, m.mpv = s, mpv
	m.mu.Unlock()

	if cfg.Playback.SkipIntroAniSkip && req.MediaID > 0 && req.Episode > 0 {
		go func() {
			media, err := m.platform.MediaLite(context.Background(), req.MediaID)
			if err != nil || media.IDMal == nil {
				return
			}
			skips := SkipTimes(context.Background(), m.db, *media.IDMal, req.Episode)
			m.mu.Lock()
			s.Skips = skips
			m.mu.Unlock()
		}()
	}

	go m.loop(s, mpv)
	m.emit(s, true)
	return s, nil
}

func (m *Manager) loop(s *Session, mpv *Mpv) {
	props := []string{"time-pos", "duration", "pause", "aid", "sid", "track-list"}
	for i, p := range props {
		mpv.Observe(i+1, p)
	}
	var aid, sid int
	var subOff bool
	for {
		select {
		case <-mpv.Done():
			m.finish(s)
			return
		case ev := <-mpv.Events:
			switch ev.Event {
			case "file-loaded":
				m.applyTrackPrefs(s, mpv)
			case "end-file":
				if ev.Reason == "eof" {
					m.mu.Lock()
					s.finished = true
					m.mu.Unlock()
				}
			case "property-change":
				m.mu.Lock()
				switch ev.Name {
				case "time-pos":
					var f float64
					if json.Unmarshal(ev.Data, &f) == nil {
						s.Position = f
						m.maybeSkip(s, mpv)
					}
				case "duration":
					var f float64
					if json.Unmarshal(ev.Data, &f) == nil {
						s.Duration = f
					}
				case "pause":
					var b bool
					_ = json.Unmarshal(ev.Data, &b)
					s.Paused = b
					s.lastSave = time.Time{} // save right away on pause
				case "track-list":
					s.Tracks = parseTrackList(ev.Data)
				case "aid", "sid":
					id, off := parseTrackID(ev.Data)
					if ev.Name == "aid" {
						aid = id
					} else {
						sid, subOff = id, off
					}
					if time.Now().After(s.guardTill) && !s.guardTill.IsZero() && m.settings.Get().Playback.RememberTracks && s.MediaID > 0 {
						prefs := PrefsFromSelection(s.MediaID, s.Tracks, aid, sid, subOff)
						if old := m.Tracks.Get(s.MediaID); old != nil {
							prefs.StreamMode = old.StreamMode
						}
						go func() {
							if err := m.Tracks.Save(prefs); err != nil {
								log.Printf("save track prefs: %v", err)
							}
						}()
					}
				}
				m.tick(s)
				m.mu.Unlock()
			}
		}
	}
}

func parseTrackID(raw json.RawMessage) (int, bool) {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n, false
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		if str == "no" || str == "" {
			return 0, true
		}
		var id int
		_, _ = fmt.Sscanf(str, "%d", &id)
		return id, false
	}
	return 0, true // false / null
}

// applyTrackPrefs selects the remembered audio/subtitle tracks once the file
// is loaded (track ids differ between files, so we match by language/title).
func (m *Manager) applyTrackPrefs(s *Session, mpv *Mpv) {
	tracks := mpv.Tracks()
	cfg := m.settings.Get()
	var prefs *TrackPrefs
	if cfg.Playback.RememberTracks && s.MediaID > 0 {
		prefs = m.Tracks.Get(s.MediaID)
	}
	if prefs != nil {
		if prefs.AudioLang != "" || prefs.AudioTitle != "" || prefs.AudioIndex > 0 {
			if id, ok := PickTrack(tracks, "audio", prefs.AudioLang, prefs.AudioTitle, prefs.AudioIndex); ok {
				_ = mpv.Set("aid", id)
			}
		}
		if prefs.SubOff {
			_ = mpv.Set("sid", "no")
		} else if prefs.SubLang != "" || prefs.SubTitle != "" || prefs.SubIndex > 0 {
			if id, ok := PickTrack(tracks, "sub", prefs.SubLang, prefs.SubTitle, prefs.SubIndex); ok {
				_ = mpv.Set("sid", id)
			}
		}
	}
	m.mu.Lock()
	s.Tracks = tracks
	// Ignore the track changes caused by loading/applying for a moment so
	// only user choices are remembered.
	s.guardTill = time.Now().Add(2500 * time.Millisecond)
	m.mu.Unlock()
	if prefs != nil {
		mpv.ShowText("Restored your audio & subtitle choice", 2000)
	}
}

func (m *Manager) maybeSkip(s *Session, mpv *Mpv) {
	for _, sk := range s.Skips {
		if (sk.Type == "op" || sk.Type == "mixed-op" || sk.Type == "recap") && !s.skipped[sk.Type] &&
			s.Position >= sk.Start && s.Position < sk.End-1 {
			s.skipped[sk.Type] = true
			end := sk.End
			go func() {
				_ = mpv.Set("time-pos", end)
				mpv.ShowText("Skipped "+strings.ToUpper(sk.Type), 1500)
			}()
		}
	}
}

// tick persists the position and fires the progress update when needed.
// Must be called with m.mu held.
func (m *Manager) tick(s *Session) {
	cfg := m.settings.Get()
	if s.MediaID > 0 && time.Since(s.lastSave) > 5*time.Second && s.Position > 0 {
		s.lastSave = time.Now()
		e := history.Entry{MediaID: s.MediaID, Episode: s.Episode, Position: s.Position, Duration: s.Duration, Source: s.Source}
		go func() { _ = m.history.Save(e) }()
	}
	if !s.ProgressUpdated && s.MediaID > 0 && s.Episode > 0 && s.Duration > 0 &&
		s.Position/s.Duration >= cfg.Playback.CompletionThreshold {
		s.ProgressUpdated = true
		m.fireProgress(s.MediaID, s.Episode)
	}
	m.emit(s, false)
}

func (m *Manager) fireProgress(mediaID, episode int) {
	for _, h := range m.OnProgress {
		go h(mediaID, episode)
	}
	if !m.settings.Get().Playback.AutoUpdateProgress {
		return
	}
	go func() {
		ok, err := m.platform.UpdateProgress(context.Background(), mediaID, episode)
		if err != nil {
			m.hub.Error("Could not update progress: " + err.Error())
			return
		}
		if ok {
			m.hub.Success(fmt.Sprintf("Progress updated — episode %d", episode))
		}
	}()
}

func (m *Manager) finish(s *Session) {
	m.mu.Lock()
	s.Active = false
	finished := s.finished
	if m.cur == s {
		m.cur, m.mpv = nil, nil
	}
	if s.MediaID > 0 && s.Position > 0 {
		if finished {
			_ = m.history.MarkFinished(s.MediaID, s.Episode, s.Duration, s.Source)
		} else {
			_ = m.history.Save(history.Entry{MediaID: s.MediaID, Episode: s.Episode, Position: s.Position, Duration: s.Duration, Source: s.Source})
		}
	}
	if finished && !s.ProgressUpdated && s.MediaID > 0 && s.Episode > 0 {
		s.ProgressUpdated = true
		m.fireProgress(s.MediaID, s.Episode)
	}
	c := *s
	m.mu.Unlock()

	m.hub.Publish(events.PlaybackEnded, &c)
	for _, fn := range m.OnStatus {
		fn(&c)
	}

	if finished && m.settings.Get().Playback.AutoPlayNext && m.NextResolver != nil {
		next, err := m.NextResolver(context.Background(), &c)
		if err == nil && next != nil {
			m.hub.Info(fmt.Sprintf("Playing episode %d", next.Episode))
			if _, err := m.PlayMpv(*next); err != nil {
				m.hub.Error(err.Error())
			}
		}
	}
}

// Stop closes the current mpv instance.
func (m *Manager) Stop() {
	m.mu.Lock()
	mpv := m.mpv
	m.mu.Unlock()
	if mpv != nil {
		mpv.Quit()
		<-mpv.Done()
		// wait for finish() to run
		time.Sleep(50 * time.Millisecond)
	}
}

// Command controls the running mpv.
func (m *Manager) Command(cmd string, value float64) error {
	m.mu.Lock()
	mpv := m.mpv
	m.mu.Unlock()
	if mpv == nil {
		return errors.New("nothing is playing in mpv")
	}
	switch cmd {
	case "pause":
		return mpv.Set("pause", true)
	case "resume":
		return mpv.Set("pause", false)
	case "toggle":
		_, err := mpv.Command("cycle", "pause")
		return err
	case "seek":
		_, err := mpv.Command("seek", value, "relative")
		return err
	case "seek-to":
		return mpv.Set("time-pos", value)
	case "stop":
		go m.Stop()
		return nil
	case "fullscreen":
		_, err := mpv.Command("cycle", "fullscreen")
		return err
	}
	return fmt.Errorf("unknown command %q", cmd)
}

// ---------------------------------------------------------------------------
// In-app (browser) player

// ProgressReport is sent by the in-app player every few seconds.
type ProgressReport struct {
	MediaID  int     `json:"mediaId"`
	Episode  int     `json:"episode"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Source   string  `json:"source"`
	Ended    bool    `json:"ended"`
	Title    string  `json:"title"`
	Paused   bool    `json:"paused"`
}

// builtin sessions are keyed by media+episode so repeated reports update
// the same "progress already sent" flag.
var builtinProgress sync.Map

func (m *Manager) ReportProgress(r ProgressReport) {
	if r.MediaID <= 0 {
		return
	}
	cfg := m.settings.Get()
	if r.Ended {
		_ = m.history.MarkFinished(r.MediaID, r.Episode, r.Duration, r.Source)
	} else {
		_ = m.history.Save(history.Entry{MediaID: r.MediaID, Episode: r.Episode, Position: r.Position, Duration: r.Duration, Source: r.Source})
	}
	key := fmt.Sprintf("%d:%d", r.MediaID, r.Episode)
	if r.Episode > 0 && r.Duration > 0 && (r.Ended || r.Position/r.Duration >= cfg.Playback.CompletionThreshold) {
		if _, done := builtinProgress.LoadOrStore(key, time.Now()); !done {
			m.fireProgress(r.MediaID, r.Episode)
		}
	}
	s := &Session{MediaID: r.MediaID, Episode: r.Episode, Title: r.Title, Source: r.Source, Player: "builtin",
		Position: r.Position, Duration: r.Duration, Paused: r.Paused, Active: !r.Ended}
	for _, fn := range m.OnStatus {
		fn(s)
	}
}

// ResetBuiltinProgress lets a rewatch trigger the progress update again.
func (m *Manager) ResetBuiltinProgress(mediaID, episode int) {
	builtinProgress.Delete(fmt.Sprintf("%d:%d", mediaID, episode))
}
