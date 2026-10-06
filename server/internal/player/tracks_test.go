package player

import (
	"encoding/json"
	"testing"
	"time"
)

var testTracks = []Track{
	{ID: 1, Type: "video"},
	{ID: 1, Type: "audio", Lang: "jpn", Title: "Japanese"},
	{ID: 2, Type: "audio", Lang: "eng", Title: "English"},
	{ID: 1, Type: "sub", Lang: "eng", Title: "Full Subtitles"},
	{ID: 2, Type: "sub", Lang: "eng", Title: "Signs & Songs"},
	{ID: 3, Type: "sub", Lang: "spa", Title: "Español"},
}

func raw(v any) json.RawMessage {
	if v == nil {
		return nil // property unavailable: mpv sends no data
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestParseTrackSel(t *testing.T) {
	cases := []struct {
		in   json.RawMessage
		want trackSel
		ok   bool
	}{
		{raw(3), trackSel{id: 3}, true},
		{raw(false), trackSel{off: true}, true},
		{raw("no"), trackSel{off: true}, true},
		{raw("2"), trackSel{id: 2}, true},
		{raw("auto"), trackSel{}, false}, // what mpv reports once nothing plays
		{json.RawMessage("null"), trackSel{}, false},
		{nil, trackSel{}, false},
		{raw(true), trackSel{}, false},
		{raw(0), trackSel{}, false},
		{raw(-2), trackSel{}, false},
		{raw(1.5), trackSel{}, false},
		{raw(""), trackSel{}, false},
	}
	for _, c := range cases {
		got, ok := parseTrackSel(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseTrackSel(%s) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestMergeSelection(t *testing.T) {
	old := &TrackPrefs{MediaID: 7, AudioLang: "jpn", AudioTitle: "Japanese", AudioIndex: 1,
		SubLang: "eng", SubTitle: "Full Subtitles", SubIndex: 1, StreamMode: "dub"}

	p, ok := mergeSelection(7, old, testTracks, trackSel{id: 2}, trackSel{id: 2})
	want := TrackPrefs{MediaID: 7, AudioLang: "eng", AudioTitle: "English", AudioIndex: 2,
		SubLang: "eng", SubTitle: "Signs & Songs", SubIndex: 2, StreamMode: "dub"}
	if !ok || p != want {
		t.Errorf("both valid: got %+v, %v; want %+v", p, ok, want)
	}

	p, ok = mergeSelection(7, old, testTracks, trackSel{}, trackSel{off: true})
	want = TrackPrefs{MediaID: 7, AudioLang: "jpn", AudioTitle: "Japanese", AudioIndex: 1, SubOff: true, StreamMode: "dub"}
	if !ok || p != want {
		t.Errorf("subs off, audio unknown: got %+v, %v; want %+v", p, ok, want)
	}

	// Ids missing from the track list (or of the wrong type) change nothing.
	if p, ok = mergeSelection(7, old, testTracks, trackSel{id: 9}, trackSel{id: 4}); ok || p != *old {
		t.Errorf("unknown ids: got %+v, %v; want the old prefs unchanged", p, ok)
	}
	// "Off" without any subtitle track (e.g. the list emptied on unload) isn't a choice.
	if _, ok = mergeSelection(7, old, nil, trackSel{off: true}, trackSel{off: true}); ok {
		t.Error("off with an empty track list must not count")
	}
	if _, ok = mergeSelection(7, old, testTracks[:3], trackSel{}, trackSel{off: true}); ok {
		t.Error("subs off without subtitle tracks must not count")
	}

	p, ok = mergeSelection(8, nil, testTracks, trackSel{id: 1}, trackSel{id: 3})
	want = TrackPrefs{MediaID: 8, AudioLang: "jpn", AudioTitle: "Japanese", AudioIndex: 1, SubLang: "spa", SubTitle: "Español", SubIndex: 3}
	if !ok || p != want {
		t.Errorf("no saved prefs: got %+v, %v; want %+v", p, ok, want)
	}
}

// trackScript replays aid/sid/track-list/end-file events into a trackWatch on
// a fake clock, saving the way the session loop does (once a change is due),
// and returns what ended up saved.
type trackScript struct {
	t     *testing.T
	w     *trackWatch
	now   time.Time
	saved *TrackPrefs
	saves int
}

func newTrackScript(t *testing.T, saved *TrackPrefs) *trackScript {
	return &trackScript{t: t, w: &trackWatch{guard: 2500 * time.Millisecond, settle: time.Second},
		now: time.Unix(1_700_000_000, 0), saved: saved}
}

// advance moves the clock, saving a due change first (the loop's timer).
func (s *trackScript) advance(d time.Duration) {
	if at, ok := s.w.due(); ok && !s.now.Add(d).Before(at) {
		if p, ok := s.w.take(7, s.saved); ok {
			s.saved = &p
			s.saves++
		}
	}
	s.now = s.now.Add(d)
}

func (s *trackScript) prop(name string, v any) {
	switch name {
	case "aid", "sid":
		s.w.selection(name, raw(v), s.now)
	case "track-list":
		s.w.setTracks(parseTrackList(raw(v)))
	default:
		s.t.Fatalf("unknown property %s", name)
	}
	s.now = s.now.Add(time.Millisecond)
}

func TestTrackWatchKeepsChoiceThroughUnload(t *testing.T) {
	restored := &TrackPrefs{MediaID: 7, AudioLang: "jpn", AudioTitle: "Japanese", AudioIndex: 1,
		SubLang: "eng", SubTitle: "Full Subtitles", SubIndex: 1, StreamMode: "sub"}
	userChoice := TrackPrefs{MediaID: 7, AudioLang: "jpn", AudioTitle: "Japanese", AudioIndex: 1,
		SubLang: "eng", SubTitle: "Signs & Songs", SubIndex: 2, StreamMode: "sub"}

	// Ways mpv may report the unload; the end-file can come before, between
	// or after the aid/sid fallbacks and the emptied track list.
	unloads := map[string]func(s *trackScript){
		"fallbacks before end-file": func(s *trackScript) {
			s.prop("sid", false)
			s.prop("aid", "auto")
			s.prop("track-list", []any{})
			s.w.ended()
		},
		"fallbacks after end-file": func(s *trackScript) {
			s.w.ended()
			s.prop("sid", "auto")
			s.prop("aid", false)
			s.prop("track-list", []any{})
		},
		"track list emptied first": func(s *trackScript) {
			s.prop("track-list", nil)
			s.prop("sid", false)
			s.prop("aid", "auto")
			s.w.ended()
		},
		"unavailable values": func(s *trackScript) {
			s.prop("sid", nil)
			s.prop("aid", nil)
			s.w.ended()
		},
	}
	for name, unload := range unloads {
		t.Run(name, func(t *testing.T) {
			s := newTrackScript(t, restored)
			// Right after observing, nothing plays yet.
			s.prop("aid", "auto")
			s.prop("sid", "auto")
			s.prop("track-list", []any{})
			// mpv's own selection while loading, then file-loaded + our restore.
			s.prop("aid", 1)
			s.prop("sid", 3)
			s.w.loaded(s.now, testTracks)
			s.prop("sid", 1) // echo of restoring the saved choice: inside the guard
			s.advance(3 * time.Second)
			if s.saves != 0 {
				t.Fatalf("saved %d times while loading/restoring", s.saves)
			}

			s.prop("sid", 2) // the user picks the signs track
			s.advance(1500 * time.Millisecond)
			if s.saves != 1 || *s.saved != userChoice {
				t.Fatalf("after the user's pick: saves=%d prefs=%+v; want %+v", s.saves, s.saved, userChoice)
			}

			unload(s)
			s.advance(5 * time.Second)
			if s.saves != 1 || *s.saved != userChoice {
				t.Fatalf("after unload: saves=%d prefs=%+v; want %+v", s.saves, s.saved, userChoice)
			}
		})
	}
}

func TestTrackWatchSettles(t *testing.T) {
	s := newTrackScript(t, nil)
	s.w.loaded(s.now, testTracks)
	s.prop("aid", 1)
	s.prop("sid", 1)
	s.advance(3 * time.Second)

	// Quick successive changes are saved once, as the last one.
	s.prop("sid", 3)
	s.advance(300 * time.Millisecond)
	s.prop("sid", false)
	s.advance(900 * time.Millisecond)
	if s.saves != 0 {
		t.Fatalf("saved before the change settled")
	}
	s.advance(200 * time.Millisecond)
	if s.saves != 1 || !s.saved.SubOff || s.saved.AudioLang != "jpn" {
		t.Fatalf("saves=%d prefs=%+v; want one save with subtitles off and Japanese audio", s.saves, s.saved)
	}

	// A change made just before the file ends is dropped, not half-saved.
	s.prop("aid", 2)
	s.advance(500 * time.Millisecond)
	s.w.ended()
	s.advance(5 * time.Second)
	if s.saves != 1 || s.saved.AudioLang != "jpn" {
		t.Fatalf("saves=%d prefs=%+v; the change cut short by end-file must not be saved", s.saves, s.saved)
	}

	// The next file starts a new guard.
	s.w.loaded(s.now, testTracks)
	s.prop("aid", 2)
	s.advance(5 * time.Second)
	if s.saves != 1 {
		t.Fatalf("a change inside the new guard was saved")
	}
}
