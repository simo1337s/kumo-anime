package library

import (
	"context"
	"errors"
	"github.com/simo1337s/animetest/server/internal/anilist"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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
		if runtime.GOOS == "windows" {
			t.Skip("Windows only lets administrators (or Developer Mode) make symlinks:", err)
		}
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

func TestFoldersExplainSkippedFolders(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "Anime")
	s1 := filepath.Join(lib, "[Lulu] Code Geass")
	touch(t, filepath.Join(s1, "Code Geass - 01.mkv"))
	touch(t, filepath.Join(s1, "Code Geass - 02.mkv"))
	touch(t, filepath.Join(s1, "Scans", "01.jpg")) // extras: not listed
	// Still downloading in qBittorrent.
	r2 := filepath.Join(lib, "Code Geass Hangyaku no Lelouch R2")
	touch(t, filepath.Join(r2, "Code Geass R2 - 01.mkv.!qB"))
	touch(t, filepath.Join(r2, "Code Geass R2 - 02.mkv.!qB"))
	touch(t, filepath.Join(lib, "Some Show Sample Clips", "clip.mkv"))
	touch(t, filepath.Join(lib, "Packed", "show.rar"))
	touch(t, filepath.Join(lib, "Packed", "show.r00"))
	touch(t, filepath.Join(lib, "Creditless", "Show NCOP.mkv"))
	touch(t, filepath.Join(lib, "Artbook", "Scans", "01.jpg"))
	touch(t, filepath.Join(lib, "Notes", "readme.txt"))
	touch(t, filepath.Join(lib, "Kumo Download", ".kumo-download-abc", "Show - 01 [SUB].mp4"))
	if err := os.MkdirAll(filepath.Join(lib, "Empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestScanner(t, lib)
	scan(t, s)

	byLabel := func() map[string]FolderInfo {
		t.Helper()
		fs, err := s.Folders()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]FolderInfo{}
		for _, f := range fs {
			out[f.Label] = f
		}
		return out
	}
	got := byLabel()
	if f := got["[Lulu] Code Geass"]; f.Videos != 2 || f.NotIndexed != 0 || f.Problem != "" {
		t.Errorf("season 1: %+v", f)
	}
	problems := map[string]string{
		"Code Geass Hangyaku no Lelouch R2": "Still downloading (2 unfinished files)",
		"Some Show Sample Clips":            "“*sample*”",
		"Packed":                            "archives (.rar)",
		"Creditless":                        "Ignore patterns skip its videos (*NCOP*)",
		"Artbook":                           "No video files in this folder or its subfolders",
		"Notes":                             "No video files here (1 .txt)",
		"Kumo Download":                     "Still downloading (1 unfinished file)",
		"Empty":                             "The folder is empty",
	}
	for label, want := range problems {
		if f, ok := got[label]; !ok || !strings.Contains(f.Problem, want) {
			t.Errorf("%s: want problem %q, got %+v (listed: %v)", label, want, f, ok)
		}
	}
	for _, label := range []string{"[Lulu] Code Geass/Scans", "Artbook/Scans", "Kumo Download/.kumo-download-abc"} {
		if f, ok := got[label]; ok {
			t.Errorf("%s shouldn't be listed: %+v", label, f)
		}
	}

	// The download finished, but no scan ran yet: index it on the spot.
	for _, n := range []string{"Code Geass R2 - 01.mkv", "Code Geass R2 - 02.mkv"} {
		if err := os.Rename(filepath.Join(r2, n+".!qB"), filepath.Join(r2, n)); err != nil {
			t.Fatal(err)
		}
	}
	if f := byLabel()["Code Geass Hangyaku no Lelouch R2"]; f.Videos != 2 || f.NotIndexed != 2 || f.Problem != "" {
		t.Errorf("finished, not indexed: %+v", f)
	}
	files, err := s.IndexFolders([]string{r2})
	if err != nil || len(files) != 2 || files[0].MediaID != 0 || files[0].Episode != 1 || files[1].Episode != 2 {
		t.Fatalf("index: %d files %v", len(files), err)
	}
	if f := byLabel()["Code Geass Hangyaku no Lelouch R2"]; f.NotIndexed != 0 {
		t.Errorf("after indexing: %+v", f)
	}
	// Indexing again changes nothing.
	if again, err := s.IndexFolders([]string{r2}); err != nil || len(again) != 2 {
		t.Fatalf("index again: %d files %v", len(again), err)
	}
	for _, dir := range []string{base, filepath.Join(lib, "..", "x"), "Anime"} {
		if _, err := s.IndexFolders([]string{dir}); err == nil {
			t.Errorf("indexing %s must be refused", dir)
		}
	}

	// Files ignored by hand.
	all, _ := s.Store.All()
	for _, f := range all {
		if f.Dir == s1 {
			f.Ignored = true
			if err := s.Store.Save(f); err != nil {
				t.Fatal(err)
			}
		}
	}
	if f := byLabel()["[Lulu] Code Geass"]; !strings.Contains(f.Problem, "ignore its 2 videos") {
		t.Errorf("ignored by hand: %+v", f)
	}
}

// Matching got better (matcherVersion): files matched before are matched
// again once. Season 1's files matched to R2 go to season 1 when AniList
// confirms it, keep their match while it can't, and aren't matched again
// after.
func TestScanRematchesAfterMatcherUpgrade(t *testing.T) {
	lib := t.TempDir()
	s, _ := newTestScanner(t, lib)
	ep := filepath.Join(lib, "Code Geass", "Code Geass - Hangyaku no Lelouch - 01.mkv")
	byHand := filepath.Join(lib, "Other", "Other - 01.mkv")
	touch(t, ep)
	touch(t, byHand)
	scan(t, s) // indexed, not matched
	files, _ := s.Store.All()
	for _, f := range files {
		f.MediaID, f.Episode, f.Kind, f.MatchScore = 2904, 1, "main", 0.86 // R2, by the old matcher
		if f.Path == byHand {
			f.MediaID, f.Locked = 777, true // matched by hand
		}
	}
	if err := s.Store.Save(files...); err != nil {
		t.Fatal(err)
	}

	s1 := testMedia(1575, "Code Geass: Hangyaku no Lelouch", "Code Geass: Lelouch of the Rebellion")
	r2 := testMedia(2904, "Code Geass: Hangyaku no Lelouch R2", "Code Geass: Lelouch of the Rebellion R2")
	aniListUp := false
	searches := 0
	s.newMatcher = func(threshold float64, outsideList bool) *Matcher {
		m := NewMatcher(nil, threshold, outsideList)
		m.listFn = func(context.Context) []*anilist.Media { return []*anilist.Media{r2} }
		m.searchFn = func(context.Context, string) ([]*anilist.Media, error) {
			searches++
			if !aniListUp {
				return nil, errors.New("AniList is down")
			}
			return []*anilist.Media{s1, r2}, nil
		}
		return m
	}
	matchedTo := func(path string) int {
		f, err := s.Store.Get(path)
		if err != nil {
			t.Fatal(err)
		}
		return f.MediaID
	}
	rescan := func() {
		t.Helper()
		if _, err := s.Scan(context.Background(), ScanOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	rescan() // AniList down: the old match stays, for now
	if got := matchedTo(ep); got != 2904 {
		t.Errorf("AniList down: matched to %d, want the old match (2904) kept", got)
	}
	aniListUp = true
	rescan()
	if got := matchedTo(ep); got != 1575 {
		t.Errorf("AniList up: matched to %d, want season 1 (1575)", got)
	}
	if got := matchedTo(byHand); got != 777 {
		t.Errorf("a match made by hand changed to %d", got)
	}
	searches = 0
	rescan()
	if searches != 0 || matchedTo(ep) != 1575 {
		t.Errorf("matched again after the upgrade (%d searches)", searches)
	}
}
