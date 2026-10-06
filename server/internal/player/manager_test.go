package player

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/history"
)

// newTestManager builds a Manager on a fresh database, with the network
// features (AniList progress, AniSkip) off and mpv replaced by fakes.
func newTestManager(t *testing.T, edit func(*config.Settings)) (*Manager, *fakeLauncher) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.Get()
	cfg.Playback.AutoUpdateProgress = false
	cfg.Playback.SkipIntroAniSkip = false
	cfg.Playback.RememberTracks = true
	cfg.Playback.AutoPlayNext = false
	if edit != nil {
		edit(&cfg)
	}
	if _, err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	m := NewManager(settings, history.NewStore(d), d, anilist.NewPlatform(d, hub), hub)
	m.trackGuard = 50 * time.Millisecond
	m.trackSettle = 200 * time.Millisecond
	l := newFakeLauncher(t)
	m.launch = l.launch
	t.Cleanup(func() {
		m.Stop()
		_ = d.Close()
	})
	return m, l
}

// liveSession returns the manager's current session itself (not a copy).
func liveSession(t *testing.T, m *Manager) *Session {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		t.Fatal("nothing is playing")
	}
	return m.cur
}

func waitLoopDone(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the session's event loop did not finish")
	}
}

func TestMpvKeepsTrackChoiceThroughUnload(t *testing.T) {
	saved := TrackPrefs{MediaID: 7, AudioLang: "jpn", AudioTitle: "Japanese", AudioIndex: 1,
		SubLang: "eng", SubTitle: "Full Subtitles", SubIndex: 1, StreamMode: "sub"}
	want := saved
	want.SubTitle, want.SubIndex = "Signs & Songs", 2

	// How mpv may report the unload: aid/sid fall back to their option
	// values ("auto", false for "no") and the track list empties, in any
	// order around end-file. linger() is mpv taking its time (slow shutdown,
	// --idle=yes); each case relies on a different safeguard.
	unloads := map[string]func(f *fakeMpv, linger func()){
		"fallbacks before end-file": func(f *fakeMpv, linger func()) {
			f.prop("sid", false)
			f.prop("aid", "auto")
			f.prop("time-pos", nil)
			f.event("end-file", "reason", "eof")
			linger()
			f.prop("track-list", []Track{})
		},
		"fallbacks after end-file": func(f *fakeMpv, linger func()) {
			f.event("end-file", "reason", "eof")
			f.prop("sid", false)
			f.prop("aid", false)
			linger()
			f.prop("track-list", []Track{})
			f.event("shutdown")
		},
		"track list emptied first": func(f *fakeMpv, linger func()) {
			f.prop("track-list", []Track{})
			f.prop("sid", false)
			f.prop("aid", "auto")
			linger()
			f.event("end-file", "reason", "eof")
		},
		"user quits": func(f *fakeMpv, linger func()) {
			f.prop("sid", "auto")
			f.prop("aid", "auto")
			linger()
			f.event("end-file", "reason", "quit")
			f.prop("track-list", nil)
		},
	}
	for name, unload := range unloads {
		t.Run(name, func(t *testing.T) {
			m, l := newTestManager(t, nil)
			if err := m.Tracks.Save(saved); err != nil {
				t.Fatal(err)
			}
			if _, err := m.PlayMpv(PlayRequest{MediaID: 7, Episode: 3, Source: "local", Target: "/anime/03.mkv"}); err != nil {
				t.Fatal(err)
			}
			s, f := liveSession(t, m), l.last()

			// Nothing plays yet right after mpv starts.
			f.prop("aid", "auto")
			f.prop("sid", "auto")
			f.prop("track-list", []Track{})
			// mpv's own pick while loading; on file-loaded the saved choice
			// is restored (the fake echoes our set_property calls).
			f.prop("aid", 1)
			f.prop("sid", 3)
			f.prop("track-list", testTracks)
			f.event("file-loaded")
			waitFor(t, "the saved choice to be restored", func() bool { return f.received("show-text") })
			if !f.received("set_property sid") {
				t.Fatal("the saved subtitle choice was not applied")
			}
			f.prop("duration", 1420.0)
			f.prop("time-pos", 1300.0)
			time.Sleep(3 * m.trackGuard)

			f.prop("sid", 2) // the user picks the signs track
			waitFor(t, "the user's pick to be saved", func() bool {
				p := m.Tracks.Get(7)
				return p != nil && p.SubTitle == "Signs & Songs"
			})

			unload(f, func() { time.Sleep(3 * m.trackSettle) })
			f.exit()
			waitLoopDone(t, s)

			got := m.Tracks.Get(7)
			if got == nil {
				t.Fatal("track prefs were deleted")
			}
			got.UpdatedAt = 0
			if *got != want {
				t.Errorf("after the episode ended the saved choice is %+v; want the user's %+v", *got, want)
			}
		})
	}
}

func TestPlayMpvSerializesSessions(t *testing.T) {
	m, l := newTestManager(t, nil)
	l.delay = 30 * time.Millisecond // like waiting for mpv's socket

	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.PlayMpv(PlayRequest{MediaID: 1, Episode: i + 1, Source: "local", Target: fmt.Sprintf("/anime/%02d.mkv", i+1)})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	launched, live, maxLive := l.stats()
	if launched != 3 || live != 1 || maxLive != 1 {
		t.Fatalf("launched=%d live=%d maxLive=%d; want 3 launches and never more than one mpv", launched, live, maxLive)
	}
	l.mu.Lock()
	lastTarget := l.targets[len(l.targets)-1]
	fakes := slices.Clone(l.launched)
	l.mu.Unlock()
	if st := m.Status(); st == nil || st.Target != lastTarget {
		t.Fatalf("current session = %+v; want the last one launched (%s)", st, lastTarget)
	}
	for i, f := range fakes[:len(fakes)-1] {
		if !f.received("quit") {
			t.Errorf("mpv #%d was not asked to quit", i+1)
		}
	}
}

func TestSessionCopiesDontRace(t *testing.T) {
	m, l := newTestManager(t, nil)
	sess, err := m.PlayMpv(PlayRequest{MediaID: 1, Episode: 1, Source: "local", Target: "/anime/01.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	f := l.last()
	waitFor(t, "the properties to be observed", func() bool { return f.count("observe_property") == 6 })
	go func() {
		for i := range 300 {
			f.prop("time-pos", float64(i+1))
			if i%50 == 0 {
				f.prop("track-list", testTracks)
			}
		}
	}()
	// What a handler does with the result (JSON-encode it) while mpv
	// reports. Nothing here synchronizes with the session loop, so the race
	// detector sees any write to the returned session.
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
		if _, err := json.Marshal(sess); err != nil {
			t.Fatal(err)
		}
	}
	if sess.Position != 0 || sess.Tracks != nil {
		t.Errorf("the returned session changed after PlayMpv returned: %+v", sess)
	}
	waitFor(t, "the position to reach 300", func() bool {
		st := m.Status()
		return st != nil && st.Position == 300 && len(st.Tracks) == len(testTracks)
	})
}

func TestAutoNextPlaysNextEpisode(t *testing.T) {
	m, l := newTestManager(t, func(c *config.Settings) { c.Playback.AutoPlayNext = true })
	m.NextResolver = func(_ context.Context, s *Session) (*PlayRequest, error) {
		return &PlayRequest{MediaID: s.MediaID, Episode: s.Episode + 1, Source: "local", Target: "/anime/02.mkv"}, nil
	}
	if _, err := m.PlayMpv(PlayRequest{MediaID: 4, Episode: 1, Source: "local", Target: "/anime/01.mkv"}); err != nil {
		t.Fatal(err)
	}
	f := l.last()
	f.prop("duration", 1420.0)
	f.prop("time-pos", 1419.0)
	// mpv sends end-file and exits right away.
	f.event("end-file", "reason", "eof")
	f.exit()

	waitFor(t, "episode 2 to play", func() bool {
		st := m.Status()
		return st != nil && st.Episode == 2
	})
	if e := m.history.Get(4, 1); e == nil || e.Position != e.Duration || e.Duration != 1420 {
		t.Errorf("episode 1 history = %+v; want it marked finished", e)
	}
	if _, live, maxLive := l.stats(); live != 1 || maxLive != 1 {
		t.Errorf("live=%d maxLive=%d; want a single mpv", live, maxLive)
	}
}

func TestAutoNextYields(t *testing.T) {
	cases := map[string]func(t *testing.T, m *Manager) (wantID string){
		"to a newer session": func(t *testing.T, m *Manager) string {
			other, err := m.PlayMpv(PlayRequest{MediaID: 9, Episode: 5, Source: "local", Target: "/other/05.mkv"})
			if err != nil {
				t.Fatal(err)
			}
			return other.ID
		},
		"to Stop": func(t *testing.T, m *Manager) string {
			m.Stop()
			return ""
		},
		"to the stop command": func(t *testing.T, m *Manager) string {
			_ = m.Command("stop", 0) // nothing plays anymore, but the stop still counts
			return ""
		},
	}
	for name, intervene := range cases {
		t.Run(name, func(t *testing.T) {
			m, l := newTestManager(t, func(c *config.Settings) { c.Playback.AutoPlayNext = true })
			resolving, release := make(chan struct{}), make(chan struct{})
			m.NextResolver = func(_ context.Context, s *Session) (*PlayRequest, error) {
				close(resolving)
				<-release
				return &PlayRequest{MediaID: s.MediaID, Episode: s.Episode + 1, Source: "local", Target: "/anime/02.mkv"}, nil
			}
			if _, err := m.PlayMpv(PlayRequest{MediaID: 4, Episode: 1, Source: "local", Target: "/anime/01.mkv"}); err != nil {
				t.Fatal(err)
			}
			f := l.last()
			f.event("end-file", "reason", "eof")
			f.exit()
			select {
			case <-resolving:
			case <-time.After(5 * time.Second):
				t.Fatal("auto play next did not start")
			}

			wantID := intervene(t, m)
			close(release)
			time.Sleep(200 * time.Millisecond) // let a wrong auto play next happen

			st := m.Status()
			if (wantID == "" && st != nil) || (wantID != "" && (st == nil || st.ID != wantID)) {
				t.Fatalf("playing %+v; auto play next must not replace what the user did", st)
			}
			l.mu.Lock()
			launchedNext := slices.Contains(l.targets, "/anime/02.mkv")
			l.mu.Unlock()
			if launchedNext {
				t.Fatal("auto play next launched episode 2")
			}
		})
	}
}

func TestBuiltinRewatchUpdatesProgressAgain(t *testing.T) {
	m, _ := newTestManager(t, nil)
	var updates atomic.Int32
	m.OnProgress = []ProgressHook{func(mediaID, episode int) {
		if mediaID == 3 && episode == 2 {
			updates.Add(1)
		}
	}}
	watch := func() {
		m.ResetBuiltinProgress(3, 2) // the in-app player opens the episode
		for _, pos := range []float64{600, 1250, 1300, 1390} {
			m.ReportProgress(ProgressReport{MediaID: 3, Episode: 2, Position: pos, Duration: 1400, Source: "local"})
		}
		m.ReportProgress(ProgressReport{MediaID: 3, Episode: 2, Position: 1400, Duration: 1400, Source: "local", Ended: true})
	}

	watch()
	waitFor(t, "the progress update", func() bool { return updates.Load() == 1 })
	watch() // rewatch
	waitFor(t, "the rewatch's progress update", func() bool { return updates.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	if n := updates.Load(); n != 2 {
		t.Fatalf("%d progress updates; want one per viewing", n)
	}
	if e := m.history.Get(3, 2); e == nil || e.Position != 1400 {
		t.Errorf("history = %+v; want the episode marked finished", e)
	}
}

func TestStatusHooksRunOneAtATime(t *testing.T) {
	m, _ := newTestManager(t, nil)
	var running, overlaps atomic.Int32
	var last atomic.Value
	m.OnStatus = []func(*Session){func(s *Session) {
		if running.Add(1) > 1 {
			overlaps.Add(1)
		}
		time.Sleep(time.Millisecond) // a slow hook (Discord IPC)
		last.Store(s.Position)
		running.Add(-1)
	}}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.ReportProgress(ProgressReport{MediaID: 1, Episode: 1, Position: float64(10 + i), Duration: 1400})
		}()
	}
	wg.Wait()
	m.ReportProgress(ProgressReport{MediaID: 1, Episode: 1, Position: 999, Duration: 1400})
	waitFor(t, "the newest status to reach the hook", func() bool { return last.Load() == 999.0 })
	if n := overlaps.Load(); n != 0 {
		t.Fatalf("status hooks overlapped %d times", n)
	}
}
