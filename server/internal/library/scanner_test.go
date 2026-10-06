package library

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
)

func newTestScanner(t *testing.T, dirs ...string) (*Scanner, *config.Store) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	settings, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.Get()
	cfg.Library.Dir = dirs[0]
	cfg.Library.ExtraDirs = dirs[1:]
	cfg.Library.AutoRefresh = false
	if _, err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return NewScanner(NewStore(d), nil, settings, events.NewHub()), settings
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(t *testing.T, s *Scanner) []string {
	t.Helper()
	all, err := s.Store.All()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range all {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

func scan(t *testing.T, s *Scanner) *ScanResult {
	t.Helper()
	res, err := s.Scan(context.Background(), ScanOptions{SkipMatching: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestScanFollowsSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "hdd", "Anime")
	touch(t, filepath.Join(real, "Frieren", "[SubsPlease] Sousou no Frieren - 01 (1080p).mkv"))
	other := filepath.Join(base, "other", "Bocchi")
	touch(t, filepath.Join(other, "Bocchi the Rock! - 01.mkv"))
	if err := os.Symlink(other, filepath.Join(real, "Bocchi")); err != nil { // symlinked show folder
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(real, "Frieren", "loop")); err != nil { // loop back to the root
		t.Fatal(err)
	}
	root := filepath.Join(base, "Videos", "Anime")
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, root); err != nil { // symlinked library root
		t.Fatal(err)
	}
	s, _ := newTestScanner(t, root)
	scan(t, s)
	got := paths(t, s)
	want := []string{
		filepath.Join(root, "Bocchi", "Bocchi the Rock! - 01.mkv"),
		filepath.Join(root, "Frieren", "[SubsPlease] Sousou no Frieren - 01 (1080p).mkv"),
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestScanKeepsFilesOfMissingDrives(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "Anime")
	extra := filepath.Join(base, "usb", "Anime")
	mount := filepath.Join(base, "mnt") // a mount point that stays behind empty
	a := filepath.Join(main, "Show A", "Show A - 01.mkv")
	b := filepath.Join(main, "Show A", "Show A - 02.mkv")
	c := filepath.Join(extra, "Show B", "Show B - 01.mkv")
	d := filepath.Join(mount, "Show C", "Show C - 01.mkv")
	for _, p := range []string{a, b, c, d} {
		touch(t, p)
	}
	s, _ := newTestScanner(t, main, extra, mount)
	if res := scan(t, s); res.Total != 4 {
		t.Fatalf("first scan found %d files", res.Total)
	}

	// The user deletes an episode, unplugs the USB drive and the other
	// drive gets unmounted (its mount point stays as an empty folder).
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(base, "usb")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(mount, "Show C")); err != nil {
		t.Fatal(err)
	}
	res := scan(t, s)
	got := paths(t, s)
	want := []string{a, d, c}
	sort.Strings(want)
	if res.Removed != 1 || len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("removed=%d got %q\nwant %q", res.Removed, got, want)
	}
}

func TestScanKeepsDownloadsOutsideLibrary(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "Anime")
	touch(t, filepath.Join(lib, "Show - 01.mkv"))
	s, _ := newTestScanner(t, lib)
	dl := filepath.Join(base, "Downloads", "Show - 02.mp4")
	touch(t, dl)
	// Registered by the downloader, outside the library folders.
	if err := s.Store.Save(&LocalFile{Path: dl, Dir: filepath.Dir(dl), Name: filepath.Base(dl), MediaID: 7, Episode: 2, Kind: "main", Locked: true}); err != nil {
		t.Fatal(err)
	}
	scan(t, s)
	if f, _ := s.Store.Get(dl); f == nil || f.MediaID != 7 {
		t.Fatalf("download was dropped by the scan: %+v", f)
	}
	if err := os.Remove(dl); err != nil {
		t.Fatal(err)
	}
	scan(t, s)
	if f, _ := s.Store.Get(dl); f != nil {
		t.Fatalf("deleted download is still indexed")
	}
}

func TestSaveScannedKeepsConcurrentChanges(t *testing.T) {
	s, _ := newTestScanner(t, t.TempDir())
	orig := &LocalFile{Path: "/lib/x.mkv", Dir: "/lib", Name: "x.mkv", Kind: "main"}
	if err := s.Store.Save(orig); err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]*LocalFile{orig.Path: orig}
	// While the scan was matching, the user matched the file by hand…
	manual := *orig
	manual.MediaID, manual.Episode, manual.Locked = 5, 3, true
	if err := s.Store.Save(&manual); err != nil {
		t.Fatal(err)
	}
	// …and a download registered a new file the scan also found.
	dl := &LocalFile{Path: "/lib/y.mkv", Dir: "/lib", Name: "y.mkv", MediaID: 9, Episode: 1, Kind: "main", Locked: true}
	if err := s.Store.Save(dl); err != nil {
		t.Fatal(err)
	}

	scanned := *orig
	scanned.MediaID, scanned.Episode = 8, 1
	newer := &LocalFile{Path: "/lib/y.mkv", Dir: "/lib", Name: "y.mkv", Kind: "main"}
	if err := s.Store.SaveScanned([]*LocalFile{&scanned, newer}, snapshot); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Store.Get("/lib/x.mkv"); f.MediaID != 5 || f.Episode != 3 || !f.Locked {
		t.Fatalf("manual match overwritten: %+v", f)
	}
	if f, _ := s.Store.Get("/lib/y.mkv"); f.MediaID != 9 || !f.Locked {
		t.Fatalf("download overwritten: %+v", f)
	}

	// Without concurrent changes the scan result is stored.
	snapshot = map[string]*LocalFile{"/lib/x.mkv": &manual}
	rescanned := manual
	rescanned.Size = 42
	if err := s.Store.SaveScanned([]*LocalFile{&rescanned}, snapshot); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Store.Get("/lib/x.mkv"); f.Size != 42 {
		t.Fatalf("scan result not saved: %+v", f)
	}
}
