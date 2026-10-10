package player

import (
	"path/filepath"
	"testing"

	"github.com/simo1337s/animetest/server/internal/db"
)

// The sub/dub saved by earlier versions whenever an episode played is
// forgotten once (the default applies again); choices made since stay.
func TestUnchosenModesForgottenOnce(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Write(`INSERT INTO track_prefs(media_id, audio_lang, audio_title, audio_index, sub_lang, sub_title, sub_index, sub_off, stream_mode, updated_at)
		VALUES(1, 'jpn', 'Japanese', 1, '', '', 0, 0, 'sub', 1)`); err != nil {
		t.Fatal(err)
	}
	s := NewTrackStore(d)
	p := s.Get(1)
	if p == nil || p.StreamMode != "" || p.AudioLang != "jpn" {
		t.Fatalf("after the first start: %+v (the mode forgotten, the tracks kept)", p)
	}
	if err := s.SetStreamMode(1, "dub"); err != nil {
		t.Fatal(err)
	}
	if p := NewTrackStore(d).Get(1); p.StreamMode != "dub" {
		t.Fatalf("a choice made since was forgotten: %+v", p)
	}
}

func TestAudioFitsMode(t *testing.T) {
	for _, c := range []struct {
		lang, title, mode string
		want              bool
	}{
		{"eng", "English", "dub", true},
		{"jpn", "Japanese", "dub", false},
		{"jpn", "Japanese", "sub", true},
		{"", "English Dub", "sub", false},
		{"und", "", "sub", true},
	} {
		if got := AudioFitsMode(c.lang, c.title, c.mode); got != c.want {
			t.Errorf("AudioFitsMode(%q, %q, %q) = %v", c.lang, c.title, c.mode, got)
		}
	}
}
