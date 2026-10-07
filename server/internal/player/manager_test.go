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
	m.viewed = func(mediaID, episode int) {
		if mediaID == 3 && episode == 2 {
			updates.Add(1)
		}
	}
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

// The in-app player reports every few seconds from 3 s in: the anime goes
// on the watching list once per viewing, from 10 s in.
func TestBuiltinStartsWatchingOncePerViewing(t *testing.T) {
	m, _ := newTestManager(t, nil)
	var (
		mu     sync.Mutex
		starts = map[episodeKey]int{}
	)
	m.started = func(mediaID, episode int) {
		mu.Lock()
		starts[episodeKey{mediaID, episode}]++
		mu.Unlock()
	}
	count := func(mediaID, episode int) int {
		mu.Lock()
		defer mu.Unlock()
		return starts[episodeKey{mediaID, episode}]
	}
	report := func(mediaID, episode int, positions ...float64) {
		for _, pos := range positions {
			m.ReportProgress(ProgressReport{MediaID: mediaID, Episode: episode, Position: pos, Duration: 1400, Source: "stream"})
		}
	}

	m.ResetBuiltinProgress(3, 1) // the in-app player opens the episode
	report(3, 1, 3, 8)
	if n := count(3, 1); n != 0 {
		t.Fatalf("%d starts 8 s in; want none before 10 s", n)
	}
	report(3, 1, 13, 18, 600, 1250, 1300)
	m.ReportProgress(ProgressReport{MediaID: 3, Episode: 1, Position: 1400, Duration: 1400, Source: "stream", Ended: true})
	if n := count(3, 1); n != 1 {
		t.Fatalf("%d starts; want one per viewing", n)
	}

	m.ResetBuiltinProgress(3, 1) // watched again
	report(3, 1, 4, 9, 14, 19)
	if n := count(3, 1); n != 2 {
		t.Fatalf("%d starts after watching it again; want 2", n)
	}

	m.ResetBuiltinProgress(3, 2) // resumed halfway
	report(3, 2, 700, 705)
	if n := count(3, 2); n != 1 {
		t.Fatalf("%d starts of a resumed episode; want 1", n)
	}

	report(3, 0, 30, 60) // no episode (creditless opening, extra)
	report(0, 1, 30, 60) // not matched to an anime
	if n := count(3, 0) + count(0, 1); n != 0 {
		t.Fatalf("%d starts without an anime episode", n)
	}
}

// mpv reports the position all the time: the anime goes on the watching
// list once per viewing (mpv session), from 10 s in. A resumed session
// starts out at its resume position, which counts once mpv has loaded the
// file (it knows the duration), not before.
func TestMpvStartsWatchingOncePerViewing(t *testing.T) {
	m, l := newTestManager(t, nil)
	var (
		mu     sync.Mutex
		starts []string
	)
	m.started = func(mediaID, episode int) {
		st := m.Status()
		mu.Lock()
		defer mu.Unlock()
		starts = append(starts, fmt.Sprintf("%d/%d at %v of %v", mediaID, episode, st.Position, st.Duration))
	}
	got := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(starts)
	}
	at := func(pos float64) func() bool {
		return func() bool {
			st := m.Status()
			return st != nil && st.Position == pos
		}
	}

	if _, err := m.PlayMpv(PlayRequest{MediaID: 5, Episode: 1, Source: "anicli", Target: "https://cdn.example/1.m3u8"}); err != nil {
		t.Fatal(err)
	}
	f := l.last()
	f.prop("duration", 1420.0)
	for _, pos := range []float64{0.5, 4, 9.5} {
		f.prop("time-pos", pos)
	}
	waitFor(t, "the position to reach 9.5", at(9.5))
	if s := got(); len(s) != 0 {
		t.Fatalf("started before 10 s: %v", s)
	}
	for _, pos := range []float64{10, 15, 600, 1300, 1419} {
		f.prop("time-pos", pos)
	}
	waitFor(t, "the position to reach 1419", at(1419))
	if s := got(); !slices.Equal(s, []string{"5/1 at 10 of 1420"}) {
		t.Fatalf("starts %v; want one, at 10 s", s)
	}

	resume := 142.0
	if _, err := m.PlayMpv(PlayRequest{MediaID: 5, Episode: 1, Source: "anicli", Target: "https://cdn.example/1.m3u8", Start: &resume}); err != nil {
		t.Fatal(err)
	}
	f = l.last()
	f.prop("pause", false) // mpv reports observed properties before loading the file
	f.prop("time-pos", nil)
	f.prop("duration", 1420.0)
	f.prop("time-pos", 142.0)
	f.prop("time-pos", 150.0)
	waitFor(t, "the position to reach 150", at(150))
	if s := got(); !slices.Equal(s, []string{"5/1 at 10 of 1420", "5/1 at 142 of 1420"}) {
		t.Fatalf("starts %v; want the resumed viewing to start once, after the file loaded", s)
	}
}

func TestMpvSkipsWhatTheSettingsSay(t *testing.T) {
	skips := []SkipInterval{{Type: "op", Start: 90, End: 180}, {Type: "mixed-ed", Start: 1290, End: 1380}}
	cases := []struct {
		name         string
		intro, outro bool
		want         []string // what mpv was told: "<new position> <text>"
	}{
		{"openings", true, false, []string{"180 Skipped opening"}},
		{"endings", false, true, []string{"1380 Skipped ending"}},
		{"both", true, true, []string{"180 Skipped opening", "1380 Skipped ending"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, l := newTestManager(t, func(c *config.Settings) {
				c.Playback.SkipIntroAniSkip, c.Playback.SkipOutroAniSkip = tc.intro, tc.outro
			})
			m.skipTimes = func(_ context.Context, mediaID, episode int) []SkipInterval {
				if mediaID != 6 || episode != 2 {
					t.Errorf("skip times asked for %d/%d", mediaID, episode)
				}
				return skips
			}
			if _, err := m.PlayMpv(PlayRequest{MediaID: 6, Episode: 2, Source: "local", Target: "/anime/02.mkv"}); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the skip times", func() bool {
				st := m.Status()
				return st != nil && len(st.Skips) == len(skips)
			})
			f := l.last()
			told := func() []string {
				var out []string
				seeks, texts := f.sent("set_property time-pos"), f.sent("show-text")
				for i := range min(len(seeks), len(texts)) {
					out = append(out, fmt.Sprint(seeks[i][0], " ", texts[i][0]))
				}
				return out
			}
			f.prop("duration", 1420.0)
			// The opening, then the ending; a seek here doesn't move the
			// position, as no time-pos comes back.
			for i, pos := range []float64{10, 95, 600, 1300} {
				f.prop("time-pos", pos)
				waitFor(t, fmt.Sprint("the position to reach ", pos), func() bool {
					st := m.Status()
					return st != nil && st.Position == pos
				})
				if i == 1 && tc.intro {
					waitFor(t, "the opening to be skipped", func() bool { return len(told()) == 1 })
				}
			}
			waitFor(t, "the skips", func() bool { return len(told()) >= len(tc.want) })
			time.Sleep(100 * time.Millisecond) // a wrong skip would arrive by now
			if got := told(); !slices.Equal(got, tc.want) {
				t.Errorf("mpv was told %q; want %q", got, tc.want)
			}
		})
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
