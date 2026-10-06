package torrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Apps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "qbittorrent")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		in   string
		prog string
		args []string
	}{
		{"  " + exe + "  ", exe, nil},
		{exe + " --profile=kumo  -x", exe, []string{"--profile=kumo", "-x"}},
		{"sh -c true", "sh", []string{"-c", "true"}},
	} {
		prog, args, err := splitCommand(tc.in)
		if err != nil || prog != tc.prog || strings.Join(args, "|") != strings.Join(tc.args, "|") {
			t.Errorf("%q: %q %q %v", tc.in, prog, args, err)
		}
	}
	for _, in := range []string{"", "   ", "\t\n", filepath.Join(dir, "missing") + " --x", "kumo-no-such-program-xyz"} {
		if _, _, err := splitCommand(in); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

// A blank executable used to index an empty slice: a panic in the auto
// downloader's goroutine, which took the whole app down.
func TestStartClientBlankExecutable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	a := newTestAutoDownloader(t, srv)
	srv.Close() // not running
	s := a.torrents.settings.Get()
	s.Qbittorrent.Executable = "   "
	if _, err := a.torrents.settings.Save(s); err != nil {
		t.Fatal(err)
	}
	err := a.torrents.StartClient(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no executable") {
		t.Fatalf("got %v", err)
	}
}
