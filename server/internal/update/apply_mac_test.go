//go:build !windows

package update

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/lifecycle"
)

// zipEntry is a file of a test zip: a link when link is set.
type zipEntry struct {
	name, data, link string
	mode             fs.FileMode
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch {
		case e.link != "":
			h.SetMode(fs.ModeSymlink | 0o755)
			e.data = e.link
		case strings.HasSuffix(e.name, "/"):
			h.SetMode(fs.ModeDir | 0o755)
		default:
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// newMacApp is the zip of the new version, like electron-builder makes it.
func newMacApp(t *testing.T) []byte {
	return makeZip(t,
		zipEntry{name: "Kumo.app/"},
		zipEntry{name: "Kumo.app/Contents/Info.plist", data: "<plist>1.0.10</plist>", mode: 0o644},
		zipEntry{name: "Kumo.app/Contents/MacOS/Kumo", data: "new shell", mode: 0o755},
		zipEntry{name: "Kumo.app/Contents/Resources/kumo", data: "new server", mode: 0o755},
		zipEntry{name: "Kumo.app/Contents/Frameworks/Electron Framework.framework/Versions/A/Electron Framework", data: "framework", mode: 0o755},
		zipEntry{name: "Kumo.app/Contents/Frameworks/Electron Framework.framework/Versions/Current", link: "A"},
		zipEntry{name: "Kumo.app/Contents/Frameworks/Electron Framework.framework/Electron Framework", link: "Versions/Current/Electron Framework"},
		zipEntry{name: "__MACOSX/Kumo.app/._Info.plist", data: "resource fork", mode: 0o644},
	)
}

func macReleases(app []byte) []map[string]any {
	sum := sha256.Sum256(app)
	rel := func(tag string) map[string]any {
		v := tag[strings.Index(tag, "-v")+2:]
		return map[string]any{
			"tag_name": tag, "html_url": "https://github.com/o/r/releases/tag/" + tag,
			"target_commitish": headSHA, "published_at": "2026-10-08T08:00:00Z",
			"assets": []map[string]any{
				{"id": 51, "name": "Kumo-" + v + "-macos-universal.dmg", "size": 10, "digest": "sha256:00"},
				{"id": 52, "name": "Kumo-" + v + "-macos-universal.zip", "size": len(app), "digest": "sha256:" + hex.EncodeToString(sum[:])},
				{"id": 53, "name": "Kumo-Setup-" + v + "-windows-x64.exe", "size": 10, "digest": "sha256:00"},
			},
		}
	}
	return []map[string]any{
		rel("macos-v1.0.9"),
		rel("macos-v1.0.10"),   // the newest for macOS
		rel("windows-v1.0.11"), // Windows only
		rel("v9.9.9"),
	}
}

// installedMac makes c look like the macOS app in a folder of its own (its
// Applications folder), and returns the app.
func installedMac(t *testing.T, c *Checker) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "Applications", "Kumo.app")
	for name, data := range map[string]string{"Contents/MacOS/Kumo": "old shell", "Contents/Resources/kumo": "old server"} {
		p := filepath.Join(app, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c.GOOS, c.Version = "darwin", "1.0.9"
	c.Executable = func() (string, error) { return filepath.Join(app, "Contents", "Resources", "kumo"), nil }
	return app
}

func TestCheckMacPicksNewestRelease(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.json("GET /repos/o/r/releases", macReleases([]byte("app")))
	c := newTestChecker(t, gh)
	installedMac(t, c)
	st := check(t, c)
	if st.Error != "" || !st.Available || st.Latest == nil || st.Latest.Version != "1.0.10" || st.Latest.URL != "https://github.com/o/r/releases/tag/macos-v1.0.10" {
		t.Fatalf("error %q, available %v, latest %+v", st.Error, st.Available, st.Latest)
	}
	if c.asset == nil || c.asset.ID != 52 || c.asset.Name != "Kumo-1.0.10-macos-universal.zip" {
		t.Fatalf("app %+v", c.asset)
	}
	if !st.CanApply {
		t.Fatalf("can't apply: %s", st.ApplyNote)
	}

	// Windows doesn't take the macOS releases, nor macOS the Windows ones.
	win := newTestChecker(t, gh)
	installedWindows(t, win)
	if st := check(t, win); st.Latest == nil || st.Latest.Version != "1.0.11" {
		t.Fatalf("Windows: latest %+v", st.Latest)
	}
}

func TestMacApplyable(t *testing.T) {
	c := newTestChecker(t, newFakeGitHub(t))
	c.GOOS = "darwin"
	writable := true
	c.Writable = func(string) bool { return writable }
	for _, tc := range []struct {
		exe, note    string
		ok, writable bool
	}{
		{"/Applications/Kumo.app/Contents/Resources/kumo", "puts it in this one's place", true, true},
		{"/Users/me/Applications/Kumo.app/Contents/Resources/kumo", "puts it in this one's place", true, true},
		{"/Users/me/kumo/dist/kumo", "isn't the macOS app", false, true},
		{"/private/var/folders/x/T/AppTranslocation/1234/d/Kumo.app/Contents/Resources/kumo", "Applications folder", false, false},
		{"/Volumes/Kumo 1.0.10/Kumo.app/Contents/Resources/kumo", "disk image", false, false},
		{"/Applications/Kumo.app/Contents/Resources/kumo", "can't write to /Applications", false, false},
	} {
		writable = tc.writable
		c.Executable = func() (string, error) { return tc.exe, nil }
		ok, note := c.applyable()
		if ok != tc.ok || !strings.Contains(note, tc.note) {
			t.Errorf("%s (writable %v): %v %q, want %v and %q", tc.exe, tc.writable, ok, note, tc.ok, tc.note)
		}
	}
}

func TestApplyMac(t *testing.T) {
	app := newMacApp(t)
	gh := newFakeGitHub(t)
	gh.json("GET /repos/o/r/releases", macReleases(app))
	gh.mux.HandleFunc("GET /repos/o/r/releases/assets/52", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(app) })
	c := newTestChecker(t, gh)
	installed := installedMac(t, c)
	var helper []string
	c.StartHelper = func(name string, args ...string) error {
		helper = append([]string{name}, args...)
		return nil
	}
	c.ParentPID = func() int { return 4321 }
	if st := check(t, c); !st.Available || !st.CanApply {
		t.Fatalf("available %v, canApply %v (%s), error %q", st.Available, st.CanApply, st.ApplyNote, st.Error)
	}
	if err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	var exit lifecycle.Exit
	select {
	case exit = <-c.Exits.C():
	case <-time.After(10 * time.Second):
		t.Fatalf("Kumo wasn't asked to quit: %+v", c.Status())
	}
	if exit.Code != lifecycle.CodeInstalling || exit.Restart || exit.Before == nil {
		t.Fatalf("exit %+v", exit)
	}
	if st := waitState(t, c); st.State != StateInstalling {
		t.Fatalf("state %q: %s", st.State, st.Message)
	}
	// Until Kumo has shut down, the old app stays.
	if b, _ := os.ReadFile(filepath.Join(installed, "Contents", "Resources", "kumo")); string(b) != "old server" || helper != nil {
		t.Fatalf("the app was replaced before Kumo quit (server %q, helper %q)", b, helper)
	}
	staging := macStaging(installed)
	if err := exit.Before(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(installed, "Contents", "Resources", "kumo")); string(b) != "new server" {
		t.Fatalf("server %q after the update", b)
	}
	if st, err := os.Stat(filepath.Join(installed, "Contents", "Resources", "kumo")); err != nil || st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("the new server isn't executable: %v %v", st.Mode(), err)
	}
	framework := filepath.Join(installed, "Contents", "Frameworks", "Electron Framework.framework")
	if link, err := os.Readlink(filepath.Join(framework, "Versions", "Current")); err != nil || link != "A" {
		t.Fatalf("Versions/Current -> %q (%v)", link, err)
	}
	if b, err := os.ReadFile(filepath.Join(framework, "Electron Framework")); err != nil || string(b) != "framework" {
		t.Fatalf("the framework through its links: %q (%v)", b, err)
	}
	if _, err := os.Stat(filepath.Join(staging, "__MACOSX")); !os.IsNotExist(err) {
		t.Fatalf("__MACOSX unpacked: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(staging, "old.app", "Contents", "Resources", "kumo")); string(b) != "old server" {
		t.Fatalf("old app: %q", b)
	}
	want := []string{"/bin/sh", "-c", reopen, "kumo-update", "4321", installed, staging}
	if !slices.Equal(helper, want) {
		t.Fatalf("helper %q, want %q", helper, want)
	}

	// The helper, with the desktop app gone already: it removes the old
	// app and opens the new one (with a stand-in for open).
	bin := t.TempDir()
	opened := filepath.Join(bin, "opened")
	if err := os.WriteFile(filepath.Join(bin, "open"), []byte("#!/bin/sh\necho \"$1\" > "+strconv.Quote(opened)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(want[0], append(want[1:4], strconv.Itoa(gone.Process.Pid), installed, staging)...)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v: %s", err, out)
	}
	if b, _ := os.ReadFile(opened); strings.TrimSpace(string(b)) != installed {
		t.Fatalf("the helper opened %q, want %q", b, installed)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("the old app is still there: %v", err)
	}
}

// When the new app can't take the old one's place, the old one stays and
// Kumo restarts into it, then says what happened.
func TestApplyMacSwapFails(t *testing.T) {
	app := newMacApp(t)
	gh := newFakeGitHub(t)
	gh.json("GET /repos/o/r/releases", macReleases(app))
	gh.mux.HandleFunc("GET /repos/o/r/releases/assets/52", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(app) })
	c := newTestChecker(t, gh)
	installed := installedMac(t, c)
	c.StartHelper = func(string, ...string) error { t.Error("the helper started"); return nil }
	check(t, c)
	if err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	exit := <-c.Exits.C()
	waitState(t, c)
	// Something took the new app away meanwhile.
	if err := os.RemoveAll(filepath.Join(macStaging(installed), "Kumo.app")); err != nil {
		t.Fatal(err)
	}
	if err := exit.Before(); err == nil {
		t.Fatal("Before succeeded")
	}
	if b, _ := os.ReadFile(filepath.Join(installed, "Contents", "Resources", "kumo")); string(b) != "old server" {
		t.Fatalf("the old app isn't back: server %q", b)
	}
	if _, err := os.Stat(macStaging(installed)); !os.IsNotExist(err) {
		t.Fatalf("the update's files are left: %v", err)
	}
	next := newTestChecker(t, gh)
	next.CacheDir = c.CacheDir
	next.Start()
	if st := next.Status(); st.State != StateFailed || !strings.Contains(st.Message, "couldn't put the new version (1.0.10) in its place") {
		t.Fatalf("next run: state %q: %s", st.State, st.Message)
	}
}

func TestUnzipAppRefusesUnsafeZips(t *testing.T) {
	for name, z := range map[string][]byte{
		"link out of the app": makeZip(t, zipEntry{name: "Kumo.app/Contents/Resources/kumo", data: "x", mode: 0o755}, zipEntry{name: "Kumo.app/evil", link: "../../etc"}),
		"absolute link":       makeZip(t, zipEntry{name: "Kumo.app/evil", link: "/etc/passwd"}),
		// Each link points inside, but one through the other goes out.
		"links through links": makeZip(t,
			zipEntry{name: "Kumo.app/Contents/Resources/kumo", data: "x", mode: 0o755},
			zipEntry{name: "Kumo.app/Contents/Resources/b", link: "../.."},
			zipEntry{name: "Kumo.app/Contents/Resources/b/c", link: "../../.."},
			zipEntry{name: "Kumo.app/Contents/Resources/b/c/PWNED.txt", data: "x", mode: 0o644}),
		"path out of it": makeZip(t, zipEntry{name: "../evil", data: "x", mode: 0o644}),
		"two apps":       makeZip(t, zipEntry{name: "Kumo.app/a", data: "x", mode: 0o644}, zipEntry{name: "Other.app/a", data: "x", mode: 0o644}),
		"no app":         makeZip(t, zipEntry{name: "Kumo/a", data: "x", mode: 0o644}),
	} {
		dir := t.TempDir()
		archive := filepath.Join(dir, "app.zip")
		if err := os.WriteFile(archive, z, 0o644); err != nil {
			t.Fatal(err)
		}
		if app, err := unzipApp(archive, filepath.Join(dir, "out")); err == nil {
			t.Errorf("%s: unpacked %s", name, app)
		}
		if _, err := os.Stat(filepath.Join(dir, "PWNED.txt")); err == nil {
			t.Errorf("%s: wrote out of the folder", name)
		}
	}
}

// TestReleasedMacApp unpacks the app built for a release the way an update
// does and checks that macOS still takes its signature, and that its server
// runs. The macOS release workflow runs it on the zip it made.
func TestReleasedMacApp(t *testing.T) {
	archive := os.Getenv("KUMO_MAC_APP_ZIP")
	if archive == "" {
		t.Skip("KUMO_MAC_APP_ZIP isn't set")
	}
	app, err := unzipApp(archive, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", "--verbose=2", app).CombinedOutput(); err != nil {
			t.Fatalf("codesign --verify: %v\n%s", err, out)
		}
	}
	out, err := exec.Command(filepath.Join(app, "Contents", "Resources", "kumo"), "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("kumo --version: %v\n%s", err, out)
	}
	if want := os.Getenv("KUMO_MAC_APP_VERSION"); want != "" && strings.TrimSpace(string(out)) != "Kumo "+want {
		t.Fatalf("kumo --version says %q, want Kumo %s", out, want)
	}
	t.Logf("%s", out)
}
