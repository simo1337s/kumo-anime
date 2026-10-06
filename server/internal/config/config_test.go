package config

import (
	"path/filepath"
	"testing"

	"github.com/simo1337s/animetest/server/internal/db"
)

func TestMigrateDefaultPlayer(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Settings saved by a version before schemaVersion existed.
	old := map[string]any{
		"library":  map[string]any{"dir": "/mnt/big/Media/Anime"},
		"playback": map[string]any{"defaultPlayer": "mpv"},
		"aniCli":   map[string]any{"player": "mpv"},
	}
	if err := d.SetKV(settingsKey, old); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.Playback.DefaultPlayer != "builtin" || got.AniCli.Player != "builtin" || got.Library.Dir != "/mnt/big/Media/Anime" || got.SchemaVersion != schemaVersion {
		t.Fatalf("not migrated: %+v %+v %q v%d", got.Playback.DefaultPlayer, got.AniCli.Player, got.Library.Dir, got.SchemaVersion)
	}
	// Choosing mpv again afterwards sticks.
	got.Playback.DefaultPlayer = "mpv"
	if _, err := s.Save(got); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Get().Playback.DefaultPlayer != "mpv" {
		t.Fatalf("mpv choice lost after restart: %q", s2.Get().Playback.DefaultPlayer)
	}
	// Fresh installs default to the in-app player.
	if Defaults().Playback.DefaultPlayer != "builtin" || Defaults().AniCli.Player != "builtin" {
		t.Fatal("defaults should use the in-app player")
	}
}
