//go:build !windows

package update

import (
	"archive/tar"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeArch puts stand-ins for makepkg, pacman and pkexec on PATH (and
// nothing else). makepkg writes down how it ran and makes a package (and a
// debug one) in $PKGDEST; pkexec writes down its arguments and exits with
// $FAKE_PKEXEC_EXIT. It returns the folder they write to.
func fakeArch(t *testing.T, withPkexec bool) string {
	t.Helper()
	bin, out := t.TempDir(), t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("FAKE_OUT", out)
	t.Setenv("FAKE_PKEXEC_EXIT", "0")
	t.Setenv("FAKE_MAKEPKG_FAIL", "")
	script := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("makepkg", `printf '%s\n' "$PWD" "$*" "$KUMO_COMMIT" "$KUMO_VERSION" "$PKGDEST" "$LC_ALL" >"$FAKE_OUT/makepkg"
echo "==> Making package: kumo 1.0.0-1 (Wed Oct  7 10:00:00 2026)"
if [ -n "$FAKE_MAKEPKG_FAIL" ]; then
    echo "==> Missing dependencies:"
    echo "  -> go"
    echo "==> ERROR: Could not resolve all dependencies."
    exit 8
fi
echo "==> Checking buildtime dependencies..."
echo "==> Starting build()..."
echo "cd web && npm ci --no-audit --no-fund && npm run build"
echo "> tsc --noEmit && vite build" >&2
echo "cd server && CGO_ENABLED=0 go build -trimpath -o ../dist/kumo ./cmd/kumo"
echo "==> Starting package()..."
: >"$PKGDEST/kumo-1.0.0-1-x86_64.pkg.tar.zst"
: >"$PKGDEST/kumo-debug-1.0.0-1-x86_64.pkg.tar.zst"
echo "==> Finished making: kumo 1.0.0-1 (Wed Oct  7 10:01:00 2026)"
`)
	script("pacman", "exit 0\n")
	if withPkexec {
		script("pkexec", `printf '%s\n' "$@" >"$FAKE_OUT/pkexec"
echo "loading packages..."
exit "$FAKE_PKEXEC_EXIT"
`)
	}
	return out
}

// linuxUpdate serves a branch two commits ahead of the running build, with
// its source.
func linuxUpdate(t *testing.T) *fakeGitHub {
	t.Helper()
	gh := newFakeGitHub(t)
	gh.branch()
	gh.compare("ahead", "Second", "Third")
	source := tarball(t,
		entry{hdr: tar.Header{Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": headSHA}}},
		dirEntry("o-r-2222222/"),
		dirEntry("o-r-2222222/packaging/arch/"),
		fileEntry("o-r-2222222/packaging/arch/PKGBUILD", "pkgname=kumo\n", 0o644),
		fileEntry("o-r-2222222/Makefile", "server:\n", 0o644),
	)
	// Like GitHub: the API redirects to codeload, with a token in the query.
	gh.mux.HandleFunc("GET /repos/o/r/tarball/"+headSHA, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/codeload/o/r/legacy.tar.gz/"+headSHA+"?token=CODELOAD", http.StatusFound)
	})
	gh.mux.HandleFunc("GET /codeload/o/r/legacy.tar.gz/"+headSHA, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(source) })
	return gh
}

func archChecker(t *testing.T, gh *fakeGitHub) *Checker {
	t.Helper()
	c := newTestChecker(t, gh)
	c.Executable = func() (string, error) { return "/usr/lib/kumo/kumo", nil }
	st := check(t, c)
	if !st.Available || !st.CanApply {
		t.Fatalf("available %v, canApply %v (%s), error %q", st.Available, st.CanApply, st.ApplyNote, st.Error)
	}
	return c
}

func readLines(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// realPath resolves the links in a path, when it exists.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func TestApplyLinux(t *testing.T) {
	out := fakeArch(t, true)
	gh := linuxUpdate(t)
	c := archChecker(t, gh)
	// Leftovers of an earlier update go first.
	if err := os.MkdirAll(filepath.Join(c.CacheDir, "src", "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, c)
	if st.State != StateReady || st.ManualCommand != "" || !strings.Contains(st.Message, "Restart Kumo") || st.Progress != 100 {
		t.Fatalf("state %q (%d%%): %s / %q", st.State, st.Progress, st.Message, st.ManualCommand)
	}

	mk := readLines(t, filepath.Join(out, "makepkg"))
	pkgdest := filepath.Join(c.CacheDir, "pkg")
	want := []string{filepath.Join(c.CacheDir, "src", "o-r-2222222", "packaging", "arch"), "-f --noconfirm --nocheck", headSHA, "1.0.57", pkgdest, "C"}
	// The folder with its links resolved, as the shell may give it: on
	// macOS, /var is /private/var.
	if len(mk) > 0 {
		mk[0], want[0] = realPath(mk[0]), filepath.Join(realPath(c.CacheDir), "src", "o-r-2222222", "packaging", "arch")
	}
	if strings.Join(mk, "\n") != strings.Join(want, "\n") {
		t.Fatalf("makepkg ran as\n%q\nwant\n%q", mk, want)
	}
	pk := readLines(t, filepath.Join(out, "pkexec"))
	if want := []string{"pacman", "-U", "--noconfirm", filepath.Join(pkgdest, "kumo-1.0.0-1-x86_64.pkg.tar.zst")}; strings.Join(pk, "\n") != strings.Join(want, "\n") {
		t.Fatalf("pkexec ran with %q, want %q", pk, want)
	}
	// The source is gone once built; the package stays until Kumo restarts.
	if _, err := os.Stat(filepath.Join(c.CacheDir, "src")); !os.IsNotExist(err) {
		t.Fatalf("the build tree is still there: %v", err)
	}
	if !strings.Contains(strings.Join(st.Log, "\n"), "==> Finished making") || !strings.Contains(strings.Join(st.Log, "\n"), "loading packages") {
		t.Fatalf("log: %q", st.Log)
	}
	// The download's address (with codeload's token) went nowhere else.
	gh.each(func(r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/codeload/") && r.URL.Query().Get("token") != "CODELOAD" {
			t.Errorf("codeload request %s", r.URL)
		}
	})
	// It doesn't clean up the package that's waiting for a restart.
	c.cleanup()
	if _, err := os.Stat(filepath.Join(pkgdest, "kumo-1.0.0-1-x86_64.pkg.tar.zst")); err != nil {
		t.Fatalf("cleanup removed the package: %v", err)
	}
}

func TestApplyLinuxPasswordDialogCancelled(t *testing.T) {
	fakeArch(t, true)
	t.Setenv("FAKE_PKEXEC_EXIT", "126")
	c := archChecker(t, linuxUpdate(t))
	if err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	if st := waitState(t, c); st.State != StateFailed || st.Message != "Cancelled" || st.ManualCommand != "" {
		t.Fatalf("state %q: %q / %q", st.State, st.Message, st.ManualCommand)
	}
	// Retry works.
	t.Setenv("FAKE_PKEXEC_EXIT", "0")
	if err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	if st := waitState(t, c); st.State != StateReady {
		t.Fatalf("retry: state %q: %s", st.State, st.Message)
	}
}

// No polkit agent (pkexec exits 127), or no pkexec: the user gets the
// command to run.
func TestApplyLinuxInstallByHand(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pkexec bool
	}{{"no agent", true}, {"no pkexec", false}} {
		t.Run(tc.name, func(t *testing.T) {
			fakeArch(t, tc.pkexec)
			t.Setenv("FAKE_PKEXEC_EXIT", "127")
			c := archChecker(t, linuxUpdate(t))
			if err := c.Apply(); err != nil {
				t.Fatal(err)
			}
			st := waitState(t, c)
			pkg := filepath.Join(c.CacheDir, "pkg", "kumo-1.0.0-1-x86_64.pkg.tar.zst")
			if st.State != StateReady || st.ManualCommand != "sudo pacman -U --noconfirm '"+pkg+"'" || !strings.Contains(st.Message, "terminal") {
				t.Fatalf("state %q: %q / %q", st.State, st.Message, st.ManualCommand)
			}
		})
	}
}

func TestApplyLinuxMissingBuildTools(t *testing.T) {
	fakeArch(t, true)
	t.Setenv("FAKE_MAKEPKG_FAIL", "1")
	c := archChecker(t, linuxUpdate(t))
	if err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, c)
	if st.State != StateFailed || st.ManualCommand != "sudo pacman -S --needed base-devel go nodejs npm" || !strings.Contains(st.Message, "aren't installed") {
		t.Fatalf("state %q: %q / %q", st.State, st.Message, st.ManualCommand)
	}
}

// Only the Arch package updates itself.
func TestApplyLinuxOnlyThePackage(t *testing.T) {
	fakeArch(t, true)
	c := newTestChecker(t, linuxUpdate(t))
	c.Executable = func() (string, error) { return "/home/me/kumo/dist/kumo", nil }
	if st := check(t, c); !st.Available || st.CanApply {
		t.Fatalf("available %v, canApply %v", st.Available, st.CanApply)
	}
	if err := c.Apply(); err == nil || !strings.Contains(err.Error(), "git pull") {
		t.Fatalf("Apply = %v", err)
	}
}
