package api

import (
	"path/filepath"
	"testing"

	"github.com/simo1337s/animetest/server/internal/library"
)

func TestTorrentFolderReusesAnimeFolder(t *testing.T) {
	s := newTestServer(t)
	lib := t.TempDir()
	cfg := s.app.Settings.Get()
	cfg.Library.Dir, cfg.Torrent.CreateSubfolder = lib, true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if got, want := s.torrentFolder(7, "Frieren"), filepath.Join(lib, "Frieren"); got != want {
		t.Fatalf("new anime: %q, want %q", got, want)
	}
	own := filepath.Join(lib, "Sousou no Frieren [BD]")
	if err := s.app.Files.Save(&library.LocalFile{Path: filepath.Join(own, "01.mkv"), Dir: own, Name: "01.mkv", MediaID: 7, Episode: 1, Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	if got := s.torrentFolder(7, "Frieren"); got != own {
		t.Fatalf("existing folder: %q, want %q", got, own)
	}
}
