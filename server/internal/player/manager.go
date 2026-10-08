package player

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
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
//
// The Manager only hands out copies (see snapshot); the live session is
// owned by the Manager and guarded by its mu.
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

	finished bool // mpv reached the end of the file
	stopping bool // we asked mpv to quit (Stop, or something else is starting)
	watching bool // playback got going, the list status was checked (startWatching)
	lastSave time.Time
	lastEmit time.Time
	skipped  map[string]bool
	// skipsAsked: the AniSkip times were asked for (once the length is known).
	skipsAsked bool
	loopDone   chan struct{} // closed once the event loop has saved everything
}

// snapshot returns a copy of s that shares nothing mutable with it, safe to
// hand to other goroutines. Must be called with Manager.mu held.
func (s *Session) snapshot() *Session {
	c := *s
	c.Tracks = slices.Clone(s.Tracks)
	c.Skips = slices.Clone(s.Skips)
	c.skipped, c.loopDone = nil, nil
	return &c
}

// NextResolver finds what to play after a session ended (auto play next).
type NextResolver func(ctx context.Context, s *Session) (*PlayRequest, error)

// ProgressHook is called after the list progress was updated because an
// episode crossed the completion threshold.
type ProgressHook func(mediaID, episode int)

const (
	// Track changes this soon after a file loaded are mpv's own, or ours
	// restoring the saved choice; they aren't remembered.
	defaultTrackGuard = 2500 * time.Millisecond
	// A user's track change is remembered once it has stayed in place this
	// long while the file kept playing (see trackWatch).
	defaultTrackSettle = time.Second
	// How long stopping waits for the old session to save its state.
	stopWait = 10 * time.Second
	// Playback this far into an episode (in seconds) counts as watching the
	// anime: it goes on the "watching" list (see startWatching).
	watchingFrom = 10
)

type Manager struct {
	settings *config.Store
	history  *history.Store
	Tracks   *TrackStore
	platform *anilist.Platform
	hub      *events.Hub
	db       *db.DB

	NextResolver NextResolver
	OnProgress   []ProgressHook
	// OnWatching hooks are called after an anime was put on the "watching"
	// list because one of its episodes started playing.
	OnWatching []func(mediaID int)
	// viewed observes every viewing that crossed the completion threshold,
	// whether or not the list was updated (tests).
	viewed func(mediaID, episode int)
	// started observes every viewing that got going (see watchingFrom),
	// whether or not the list was updated (tests).
	started func(mediaID, episode int)
	// OnStatus hooks run one at a time, in order, on a background goroutine.
	// Statuses that arrive while a hook is busy collapse into the newest one.
	OnStatus []func(s *Session)

	// playMu serializes starting and stopping mpv (PlayMpv, Stop, auto play
	// next), so there is only ever one mpv session. It is held across the
	// launch. Lock order: playMu, then mu.
	playMu sync.Mutex

	// mu guards cur, mpv, gen and the fields of every Session.
	mu  sync.Mutex
	cur *Session
	mpv *Mpv
	gen uint64 // bumped whenever mpv is stopped; a pending auto play next checks it

	builtinMu      sync.Mutex
	builtinStarted map[episodeKey]bool // in-app player: list status already checked this viewing
	builtinSent    map[episodeKey]bool // in-app player: progress already sent this viewing

	// listMu serializes the list updates playback makes, so that the status
	// set when an episode starts can't land after (and undo) the update made
	// when it is finished.
	listMu sync.Mutex

	statusMu   sync.Mutex
	statusNext *Session
	statusBusy bool

	// Replaced in tests.
	launch      func(mpvPath string, opts LaunchOptions) (*Mpv, error)
	skipTimes   func(ctx context.Context, mediaID, episode int, length float64) []SkipInterval
	trackGuard  time.Duration
	trackSettle time.Duration
}

type episodeKey struct{ mediaID, episode int }

func NewManager(s *config.Store, h *history.Store, d *db.DB, p *anilist.Platform, hub *events.Hub) *Manager {
	m := &Manager{
		settings: s, history: h, Tracks: NewTrackStore(d), platform: p, hub: hub, db: d,
		builtinStarted: map[episodeKey]bool{}, builtinSent: map[episodeKey]bool{},
		launch: LaunchMpv, trackGuard: defaultTrackGuard, trackSettle: defaultTrackSettle,
	}
	m.skipTimes = m.aniSkipTimes
	return m
}

// Status returns a copy of the current session (nil if nothing plays).
func (m *Manager) Status() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return nil
	}
	return m.cur.snapshot()
}

// emit publishes the session's status. It must be called with m.mu held,
// which keeps the updates in order; it never blocks.
func (m *Manager) emit(s *Session, force bool) {
	if !force && time.Since(s.lastEmit) < 900*time.Millisecond {
		return
	}
	s.lastEmit = time.Now()
	c := s.snapshot()
	m.hub.Publish(events.PlaybackStatus, c)
	m.notifyStatus(c)
}

// notifyStatus hands s to the OnStatus hooks without waiting for them: they
// run on a single goroutine, so they never race each other and a slow hook
// (Discord IPC, network) never holds up playback.
func (m *Manager) notifyStatus(s *Session) {
	if len(m.OnStatus) == 0 {
		return
	}
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	m.statusNext = s
	if !m.statusBusy {
		m.statusBusy = true
		go m.runStatusHooks()
	}
}

func (m *Manager) runStatusHooks() {
	for {
		m.statusMu.Lock()
		s := m.statusNext
		m.statusNext = nil
		if s == nil {
			m.statusBusy = false
			m.statusMu.Unlock()
			return
		}
		m.statusMu.Unlock()
		for _, fn := range m.OnStatus {
			fn(s)
		}
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

// PlayMpv opens the request in mpv (closing the current mpv first) and tracks
// it. It returns a copy of the new session.
func (m *Manager) PlayMpv(req PlayRequest) (*Session, error) {
	m.playMu.Lock()
	defer m.playMu.Unlock()
	return m.playLocked(req)
}

// playLocked is PlayMpv; m.playMu must be held.
func (m *Manager) playLocked(req PlayRequest) (*Session, error) {
	m.stopLocked()

	cfg := m.settings.Get()
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
	mpv, err := m.launch(cfg.Mpv.Path, opts)
	if err != nil {
		return nil, err
	}
	s := &Session{
		ID: uuid.NewString(), MediaID: req.MediaID, Episode: req.Episode, Title: req.Title,
		Source: req.Source, Player: "mpv", Target: req.Target, Position: start, Active: true,
		StartedAt: time.Now().Unix(), skipped: map[string]bool{}, loopDone: make(chan struct{}),
	}
	m.mu.Lock()
	m.cur, m.mpv = s, mpv
	m.emit(s, true)
	c := s.snapshot()
	m.mu.Unlock()

	go m.loop(s, mpv)
	return c, nil
}

// loadSkips fetches the session's AniSkip intervals, for an episode length
// seconds long.
func (m *Manager) loadSkips(s *Session, length float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	skips := m.skipTimes(ctx, s.MediaID, s.Episode, length)
	m.mu.Lock()
	s.Skips = skips
	m.mu.Unlock()
}

// aniSkipTimes looks up an episode's AniSkip intervals.
func (m *Manager) aniSkipTimes(ctx context.Context, mediaID, episode int, length float64) []SkipInterval {
	media, err := m.platform.MediaLite(ctx, mediaID)
	if err != nil || media.IDMal == nil {
		return nil
	}
	return SkipTimes(ctx, m.db, *media.IDMal, episode, length)
}

// loop handles the session's mpv events until mpv exits.
func (m *Manager) loop(s *Session, mpv *Mpv) {
	defer close(s.loopDone)
	for i, p := range []string{"time-pos", "duration", "pause", "aid", "sid", "track-list"} {
		mpv.Observe(i+1, p)
	}
	w := &trackWatch{guard: m.trackGuard, settle: m.trackSettle}
	saveTracks := time.NewTimer(time.Hour)
	saveTracks.Stop()
	defer saveTracks.Stop()
	// handleQueued handles the events that have already arrived.
	handleQueued := func() {
		for n := len(mpv.Events); n > 0; n-- {
			m.handle(s, mpv, w, saveTracks, <-mpv.Events)
		}
	}
	for {
		select {
		case <-mpv.Done():
			handleQueued() // what mpv sent before exiting (end-file, …)
			m.finish(s)
			return
		case ev := <-mpv.Events:
			m.handle(s, mpv, w, saveTracks, ev)
		case <-saveTracks.C:
			// An end-file already waiting in the queue must cancel the save.
			handleQueued()
			m.saveTrackChoice(s, w, saveTracks)
		}
	}
}

// handle processes one mpv event. It runs on the session's loop goroutine.
func (m *Manager) handle(s *Session, mpv *Mpv, w *trackWatch, saveTracks *time.Timer, ev MpvEvent) {
	switch ev.Event {
	case "file-loaded":
		m.applyTrackPrefs(s, mpv, w)
	case "end-file":
		w.ended()
		if ev.Reason == "eof" {
			m.mu.Lock()
			s.finished = true
			m.mu.Unlock()
		}
	case "start-file", "idle", "shutdown":
		w.ended()
	case "property-change":
		if (ev.Name == "aid" || ev.Name == "sid") && w.selection(ev.Name, ev.Data, time.Now()) {
			saveTracks.Reset(w.settle)
		}
		m.onProperty(s, mpv, w, ev)
	}
}

func (m *Manager) onProperty(s *Session, mpv *Mpv, w *trackWatch, ev MpvEvent) {
	var tracks []Track
	if ev.Name == "track-list" {
		tracks = parseTrackList(ev.Data)
		w.setTracks(tracks)
	}
	m.mu.Lock()
	switch ev.Name {
	case "time-pos":
		var f float64
		if decodeProp(ev.Data, &f) {
			s.Position = f
			m.maybeSkip(s, mpv)
		}
	case "duration":
		var f float64
		if decodeProp(ev.Data, &f) {
			s.Duration = f
			// AniSkip's times depend on the episode's length: ask once mpv
			// knows it.
			p := m.settings.Get().Playback
			if f > 0 && !s.skipsAsked && (p.SkipIntroAniSkip || p.SkipOutroAniSkip) && s.MediaID > 0 && s.Episode > 0 {
				s.skipsAsked = true
				go m.loadSkips(s, f)
			}
		}
	case "pause":
		var b bool
		if decodeProp(ev.Data, &b) {
			s.Paused = b
			s.lastSave = time.Time{} // save right away on pause
		}
	case "track-list":
		s.Tracks = tracks
	}
	save, start, fire := m.tick(s)
	m.mu.Unlock()

	if save != nil {
		_ = m.history.Save(*save)
	}
	if start {
		m.startWatching(s.MediaID, s.Episode)
	}
	if fire {
		m.fireProgress(s.MediaID, s.Episode)
	}
}

// saveTrackChoice remembers the user's audio/subtitle choice once the change
// has settled (see trackWatch).
func (m *Manager) saveTrackChoice(s *Session, w *trackWatch, saveTracks *time.Timer) {
	at, pending := w.due()
	if !pending {
		return
	}
	if wait := time.Until(at); wait > 0 {
		saveTracks.Reset(wait)
		return
	}
	m.mu.Lock()
	stopping := s.stopping
	m.mu.Unlock()
	if stopping || s.MediaID <= 0 || !m.settings.Get().Playback.RememberTracks {
		w.discard()
		return
	}
	prefs, ok := w.take(s.MediaID, m.Tracks.Get(s.MediaID))
	if !ok {
		return
	}
	if err := m.Tracks.Save(prefs); err != nil {
		log.Printf("save track prefs: %v", err)
	}
}

// applyTrackPrefs selects the remembered audio/subtitle tracks once the file
// is loaded (track ids differ between files, so we match by language/title).
func (m *Manager) applyTrackPrefs(s *Session, mpv *Mpv, w *trackWatch) {
	tracks := mpv.Tracks()
	cfg := m.settings.Get()
	var prefs, saved *TrackPrefs
	if s.MediaID > 0 {
		saved = m.Tracks.Get(s.MediaID)
	}
	if cfg.Playback.RememberTracks {
		prefs = saved
	}
	// The sub/dub choice for this anime (or a "dub" default) picks the audio
	// of dual-audio files, unless a track was picked by hand.
	mode := ""
	if saved != nil {
		mode = saved.StreamMode
	}
	if mode == "" && cfg.AniCli.DefaultMode == "dub" {
		mode = "dub"
	}
	handAudio := prefs != nil && (prefs.AudioLang != "" || prefs.AudioTitle != "" || prefs.AudioIndex > 0)
	handSub := prefs != nil && (prefs.SubOff || prefs.SubLang != "" || prefs.SubTitle != "" || prefs.SubIndex > 0)
	if mode != "" && s.Source == "local" && !handAudio {
		if pick := PickByMode(tracks, mode); pick.HasAudio {
			_ = mpv.Set("aid", pick.Audio)
			if !handSub {
				if pick.HasSub {
					_ = mpv.Set("sid", pick.Sub)
				} else if pick.SubOff {
					_ = mpv.Set("sid", "no")
				}
			}
			mpv.ShowText(map[string]string{"dub": "Dub: English audio", "sub": "Sub: original audio"}[mode], 2000)
		}
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
	m.mu.Unlock()
	// The track changes caused by loading/applying are ignored for a moment
	// so only user choices are remembered.
	w.loaded(time.Now(), tracks)
	if handAudio || handSub {
		mpv.ShowText("Restored your audio & subtitle choice", 2000)
	}
}

// maybeSkip skips the opening or the ending, as the settings say. Must be
// called with m.mu held.
func (m *Manager) maybeSkip(s *Session, mpv *Mpv) {
	if len(s.Skips) == 0 {
		return
	}
	p := m.settings.Get().Playback
	for _, sk := range s.Skips {
		what, on := skipSetting(p, sk.Type)
		if on && !s.skipped[sk.Type] && s.Position >= sk.Start && s.Position < sk.End-1 {
			s.skipped[sk.Type] = true
			end := sk.End
			go func() {
				_ = mpv.Set("time-pos", end)
				mpv.ShowText("Skipped "+what, 1500)
			}()
		}
	}
}

// skipSetting names an AniSkip interval type and says whether the settings
// skip it. Recaps play (AniSkip calls the next episode's preview one too).
func skipSetting(p config.PlaybackSettings, typ string) (what string, on bool) {
	switch typ {
	case "op", "mixed-op":
		return "opening", p.SkipIntroAniSkip
	case "ed", "mixed-ed":
		return "ending", p.SkipOutroAniSkip
	}
	return "", false
}

// tick decides whether to save the position, whether playback just got far
// enough to count as watching the anime and whether the episode just crossed
// the completion threshold, and publishes the status. It must be called with
// m.mu held; the caller does the returned work after unlocking.
func (m *Manager) tick(s *Session) (save *history.Entry, start, fire bool) {
	cfg := m.settings.Get()
	if s.MediaID > 0 && time.Since(s.lastSave) > 5*time.Second && s.Position > 0 {
		s.lastSave = time.Now()
		save = &history.Entry{MediaID: s.MediaID, Episode: s.Episode, Position: s.Position, Duration: s.Duration, Source: s.Source}
	}
	// Until mpv knows the duration the file isn't loaded, and Position is
	// only where we asked it to start.
	if !s.watching && s.MediaID > 0 && s.Episode > 0 && s.Duration > 0 && s.Position >= watchingFrom {
		s.watching = true
		start = true
	}
	if !s.ProgressUpdated && s.MediaID > 0 && s.Episode > 0 && s.Duration > 0 &&
		s.Position/s.Duration >= cfg.Playback.CompletionThreshold {
		s.ProgressUpdated = true
		fire = true
	}
	m.emit(s, false)
	return save, start, fire
}

func (m *Manager) fireProgress(mediaID, episode int) {
	if m.viewed != nil {
		m.viewed(mediaID, episode)
	}
	if !m.settings.Get().Playback.AutoUpdateProgress {
		return
	}
	go func() {
		m.listMu.Lock()
		ok, err := m.platform.UpdateProgress(context.Background(), mediaID, episode)
		m.listMu.Unlock()
		if err != nil {
			m.hub.Error("Could not update progress: " + err.Error())
			return
		}
		if ok {
			m.hub.Success(fmt.Sprintf("Progress updated — episode %d", episode))
			for _, h := range m.OnProgress {
				go h(mediaID, episode)
			}
		}
	}()
}

// startWatching puts an anime the user started an episode of on the
// "watching" list, unless the list already has it there, as rewatching or as
// completed (see Platform.StartWatching). The update runs in the background
// and a failure is only logged: it never holds up playback.
func (m *Manager) startWatching(mediaID, episode int) {
	if m.started != nil {
		m.started(mediaID, episode)
	}
	if !m.settings.Get().Playback.AutoUpdateProgress {
		return
	}
	go func() {
		m.listMu.Lock()
		changed, err := m.platform.StartWatching(context.Background(), mediaID)
		m.listMu.Unlock()
		if err != nil {
			log.Printf("player: could not put media %d on the watching list: %v", mediaID, err)
			return
		}
		if changed {
			m.hub.Success("Added to Currently watching")
			for _, h := range m.OnWatching {
				go h(mediaID)
			}
		}
	}()
}

// finish saves the session after mpv exited. It runs on the loop goroutine.
func (m *Manager) finish(s *Session) {
	m.mu.Lock()
	finished := s.finished
	fire := finished && !s.ProgressUpdated && s.MediaID > 0 && s.Episode > 0
	if fire {
		s.ProgressUpdated = true
	}
	e := history.Entry{MediaID: s.MediaID, Episode: s.Episode, Position: s.Position, Duration: s.Duration, Source: s.Source}
	m.mu.Unlock()

	// Save before announcing the end: the UI reloads the history then. The
	// session stays current meanwhile, so anything starting now waits for it
	// (stopLocked) and its status comes after ours.
	if e.MediaID > 0 && e.Position > 0 {
		if finished {
			_ = m.history.MarkFinished(e.MediaID, e.Episode, e.Duration, e.Source)
		} else {
			_ = m.history.Save(e)
		}
	}
	if fire {
		m.fireProgress(e.MediaID, e.Episode)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	s.Active = false
	if m.cur != s {
		return // replaced already; don't clear the newer session's status
	}
	m.cur, m.mpv = nil, nil
	c := s.snapshot()
	m.hub.Publish(events.PlaybackEnded, c)
	m.notifyStatus(c)
	if finished && !s.stopping && m.NextResolver != nil && m.settings.Get().Playback.AutoPlayNext {
		go m.autoNext(m.gen, c)
	}
}

// autoNext plays what NextResolver picks after the ended session, unless mpv
// was started or stopped in the meantime (gen changed).
func (m *Manager) autoNext(gen uint64, ended *Session) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	next, err := m.NextResolver(ctx, ended)
	if err != nil || next == nil {
		return
	}
	m.playMu.Lock()
	defer m.playMu.Unlock()
	m.mu.Lock()
	stale := m.gen != gen || m.cur != nil
	m.mu.Unlock()
	if stale {
		return
	}
	m.hub.Info(fmt.Sprintf("Playing episode %d", next.Episode))
	if _, err := m.playLocked(*next); err != nil {
		m.hub.Error(err.Error())
	}
}

// Stop closes the current mpv instance and cancels a pending auto play next.
func (m *Manager) Stop() {
	m.playMu.Lock()
	defer m.playMu.Unlock()
	m.stopLocked()
}

// stopLocked quits the current mpv, waits until its session has saved its
// state and cancels a pending auto play next. m.playMu must be held.
func (m *Manager) stopLocked() {
	m.mu.Lock()
	m.gen++
	s, mpv := m.cur, m.mpv
	if s != nil {
		s.stopping = true
	}
	m.mu.Unlock()
	if mpv == nil {
		return
	}
	mpv.Quit()
	select {
	case <-s.loopDone:
	case <-time.After(stopWait):
		log.Printf("player: mpv session %s did not shut down in time", s.ID)
	}
}

// stopInstance stops mpv if it is still the current instance; something
// newer may have started since the stop was requested.
func (m *Manager) stopInstance(mpv *Mpv) {
	m.playMu.Lock()
	defer m.playMu.Unlock()
	m.mu.Lock()
	same := m.mpv == mpv
	m.mu.Unlock()
	if same {
		m.stopLocked()
	}
}

// Command controls the running mpv.
func (m *Manager) Command(cmd string, value float64) error {
	m.mu.Lock()
	mpv := m.mpv
	if cmd == "stop" {
		m.gen++ // also cancels an auto play next about to start
		if m.cur != nil {
			m.cur.stopping = true
		}
	}
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
		go m.stopInstance(mpv)
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
	if r.Episode > 0 && r.Position >= watchingFrom && m.claimBuiltin(m.builtinStarted, r.MediaID, r.Episode) {
		m.startWatching(r.MediaID, r.Episode)
	}
	if r.Episode > 0 && r.Duration > 0 && (r.Ended || r.Position/r.Duration >= cfg.Playback.CompletionThreshold) &&
		m.claimBuiltin(m.builtinSent, r.MediaID, r.Episode) {
		m.fireProgress(r.MediaID, r.Episode)
	}
	m.notifyStatus(&Session{MediaID: r.MediaID, Episode: r.Episode, Title: r.Title, Source: r.Source, Player: "builtin",
		Position: r.Position, Duration: r.Duration, Paused: r.Paused, Active: !r.Ended})
}

// claimBuiltin reports whether the in-app player's current viewing of the
// episode has yet to do what claims records (builtinStarted, builtinSent),
// and marks it done.
func (m *Manager) claimBuiltin(claims map[episodeKey]bool, mediaID, episode int) bool {
	m.builtinMu.Lock()
	defer m.builtinMu.Unlock()
	k := episodeKey{mediaID, episode}
	if claims[k] {
		return false
	}
	claims[k] = true
	return true
}

// ResetBuiltinProgress starts a new viewing of an episode in the in-app
// player, so a rewatch updates the list again. Call it when the player opens
// the episode.
func (m *Manager) ResetBuiltinProgress(mediaID, episode int) {
	k := episodeKey{mediaID, episode}
	m.builtinMu.Lock()
	delete(m.builtinStarted, k)
	delete(m.builtinSent, k)
	m.builtinMu.Unlock()
}
