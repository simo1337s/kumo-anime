package update

// macOS: Kumo is the app Kumo.app, wherever the user put it (/Applications
// usually), and the server is its Contents/Resources/kumo. The update
// downloads the newest release's zip of the app (Kumo-<v>-macos-universal.zip)
// and unpacks it next to the app, in macStaging. Once Kumo has shut down,
// the new app takes the old one's place (two renames, so nothing is ever
// half copied), and a small helper waits for the desktop app to quit, then
// removes the old app and opens the new one.
//
// Kumo's own download isn't quarantined, so macOS opens the new app without
// asking again.

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/simo1337s/animetest/server/internal/lifecycle"
)

// macBundle returns the app that the server program exe is in, or "" when
// it isn't in one (a build from source).
func macBundle(exe string) string {
	res := filepath.Dir(exe)
	contents := filepath.Dir(res)
	app := filepath.Dir(contents)
	if filepath.Base(res) != "Resources" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(app, ".app") {
		return ""
	}
	return app
}

// macStaging is where an update unpacks the new app: next to the app, on
// the same disk, so that it moves into place with a rename.
func macStaging(app string) string {
	return filepath.Join(filepath.Dir(app), ".kumo-update")
}

// macApplyable says whether this copy can update itself (see applyable).
func (c *Checker) macApplyable(exe string, err error) (bool, string) {
	app := ""
	if err == nil {
		app = macBundle(exe)
	}
	dir := filepath.Dir(app)
	switch {
	case app == "":
		return false, "This copy of Kumo isn't the macOS app: update it with git pull and make, or get the app from Kumo's releases."
	case strings.Contains(app, "/AppTranslocation/"):
		// macOS runs apps opened from a download's folder from a read-only
		// copy, until they're moved.
		return false, "macOS runs this copy of Kumo from a temporary copy: drag Kumo to your Applications folder and open it from there, then it can update itself."
	case !c.Writable(dir):
		if strings.HasPrefix(app, "/Volumes/") {
			return false, "Kumo runs from its disk image: drag it to your Applications folder and open it from there, then it can update itself."
		}
		return false, "Kumo can't write to " + dir + ", where it is: download the new version from its page."
	}
	return true, "Kumo downloads the new version and puts it in this one's place: Kumo closes and opens again."
}

func (c *Checker) applyMac(ctx context.Context, g *github, target Version, zipAsset *asset) error {
	if zipAsset == nil {
		return errors.New("the newest release has no zip of the app")
	}
	exe, err := c.Executable()
	if err != nil {
		return err
	}
	app := macBundle(exe)
	if app == "" {
		return errors.New("this copy of Kumo isn't the macOS app")
	}
	dir := c.CacheDir
	if err := removeAll(dir); err != nil {
		return fmt.Errorf("couldn't remove the last update's files: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.Base(zipAsset.Name))
	err = c.download(ctx, g, "/repos/"+g.repo+"/releases/assets/"+strconv.FormatInt(zipAsset.ID, 10), "application/octet-stream", path, zipAsset.Size, func(done, total int64) {
		c.progress(StateDownloading, percent(done, total), "Downloading Kumo "+display(target)+"…")
	})
	if err != nil {
		return fmt.Errorf("couldn't download the new version: %w", err)
	}
	if err := verifyDigest(path, zipAsset.Digest); err != nil {
		_ = os.Remove(path)
		return err
	}

	c.step(StateInstalling, -1, "Unpacking Kumo "+display(target)+"…")
	staging := macStaging(app)
	if err := removeAll(staging); err != nil {
		return fmt.Errorf("couldn't remove the last update's files: %w", err)
	}
	newApp, err := unzipApp(path, staging)
	_ = os.Remove(path)
	if err != nil {
		_ = removeAll(staging)
		return fmt.Errorf("couldn't unpack the new version: %w", err)
	}
	if !isFile(filepath.Join(newApp, "Contents", "Resources", "kumo")) {
		_ = removeAll(staging)
		return errors.New("the new version's app has no Kumo server in it")
	}
	if err := ctx.Err(); err != nil {
		_ = removeAll(staging)
		return err
	}

	c.step(StateInstalling, -1, "Installing Kumo "+display(target)+": Kumo closes now and opens again when it's done.")
	exit := lifecycle.Exit{Code: lifecycle.CodeInstalling, Before: func() error {
		return c.swapApp(app, newApp, staging, target)
	}}
	if c.Exits == nil || !c.Exits.Request(exit) {
		_ = removeAll(staging)
		return errors.New("Kumo is already shutting down")
	}
	return nil
}

// reopen is the helper's script: once the desktop app (process $1) has quit,
// or after 30 seconds, it removes the old app with the rest of the staging
// folder ($3) and opens the new app ($2).
const reopen = `i=0
while kill -0 "$1" 2>/dev/null && [ "$i" -lt 300 ]; do sleep 0.1; i=$((i+1)); done
rm -rf "$3"
open "$2"`

// swapApp puts the new app in the old one's place, once Kumo has shut down
// (the desktop app is still running, from the old app's files). When that
// fails, Kumo restarts as it was and says why (see lifecycle.Exit).
func (c *Checker) swapApp(app, newApp, staging string, target Version) error {
	old := filepath.Join(staging, "old.app")
	if err := os.Rename(app, old); err != nil {
		_ = removeAll(staging)
		c.saveFailure(swapFailure(err, target))
		return fmt.Errorf("moving the old app: %w", err)
	}
	if err := os.Rename(newApp, app); err != nil {
		if back := os.Rename(old, app); back != nil {
			log.Printf("update: couldn't put the old app back in %s: %v", app, back)
		}
		_ = removeAll(staging)
		c.saveFailure(swapFailure(err, target))
		return fmt.Errorf("moving the new app: %w", err)
	}
	if err := c.StartHelper("/bin/sh", "-c", reopen, "kumo-update", strconv.Itoa(c.ParentPID()), app, staging); err != nil {
		// The new version is in place: Kumo restarts into it instead, and
		// its next update removes the old app.
		return fmt.Errorf("starting the helper that opens the new version: %w", err)
	}
	return nil
}

// swapFailure says why the new app couldn't take the old one's place.
func swapFailure(err error, target Version) string {
	msg := "Kumo couldn't put the new version (" + display(target) + ") in its place: " + err.Error() + "."
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EPERM) {
		msg += " If macOS said Kumo was prevented from modifying apps, allow it in System Settings › Privacy & Security › App Management and update again;"
	}
	return msg + " or download the new version from its release page."
}

// unzipApp unpacks a zip of an app into dir, which must not exist yet, and
// returns the app. Links are kept (an app's frameworks are made of them)
// but may not point out of the app; a path that would land outside dir
// fails it, and the zip must hold one app at its top.
func unzipApp(archive, dir string) (string, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	var written int64
	const maxSize = 4 << 30 // far more than the app
	tops := map[string]bool{}
	for _, f := range zr.File {
		rel, err := safePath(f.Name)
		if err != nil {
			return "", err
		}
		top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if rel == "" || top == "__MACOSX" {
			continue // the zip's own metadata (resource forks)
		}
		tops[top] = true
		target := filepath.Join(dir, rel)
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case mode&fs.ModeSymlink != 0:
			link, err := readLink(f)
			if err != nil {
				return "", err
			}
			// Relative, and inside the app.
			if filepath.IsAbs(link) || !strings.HasPrefix(filepath.Join(filepath.Dir(rel), link)+string(filepath.Separator), top+string(filepath.Separator)) {
				return "", fmt.Errorf("unsafe link in the archive: %q -> %q", f.Name, link)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			if err := os.Symlink(link, target); err != nil {
				return "", err
			}
		case mode.IsRegular():
			if written += int64(f.UncompressedSize64); written > maxSize {
				return "", errors.New("the archive is too big")
			}
			perm := os.FileMode(0o644)
			if mode&0o111 != 0 {
				perm = 0o755
			}
			r, err := f.Open()
			if err != nil {
				return "", err
			}
			err = writeFile(target, r, int64(f.UncompressedSize64), perm)
			r.Close()
			if err != nil {
				return "", err
			}
		default:
			continue // devices, FIFOs
		}
	}
	if len(tops) != 1 {
		return "", fmt.Errorf("unexpected archive layout (%d top-level entries)", len(tops))
	}
	for top := range tops {
		app := filepath.Join(dir, top)
		if st, err := os.Stat(app); err != nil || !st.IsDir() || !strings.HasSuffix(top, ".app") {
			return "", errors.New("unexpected archive layout (no app at its top)")
		}
		return app, nil
	}
	return "", nil
}

// readLink reads a link's target, which a zip stores as the entry's data.
func readLink(f *zip.File) (string, error) {
	if f.UncompressedSize64 > 4096 {
		return "", fmt.Errorf("unsafe link in the archive: %q", f.Name)
	}
	r, err := f.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, 4097))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
