package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/lifecycle"
)

const (
	oldSHA  = "1111111111111111111111111111111111111111"
	headSHA = "2222222222222222222222222222222222222222"
)

// fakeGitHub is a stand-in for GitHub's API, for repository "o/r".
type fakeGitHub struct {
	*httptest.Server
	mux *http.ServeMux

	mu       sync.Mutex
	requests []*http.Request
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{mux: http.NewServeMux()}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Clone(context.Background()))
		f.mu.Unlock()
		f.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

// json answers pattern with v.
func (f *fakeGitHub) json(pattern string, v any) {
	f.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	})
}

// paths lists the requests' paths (with their queries).
func (f *fakeGitHub) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		out = append(out, r.URL.RequestURI())
	}
	return out
}

func (f *fakeGitHub) each(fn func(r *http.Request)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		fn(r)
	}
}

// branch serves a repository whose default branch "main" is at headSHA,
// the 57th commit.
func (f *fakeGitHub) branch() {
	f.json("GET /repos/o/r", map[string]any{"default_branch": "main", "private": true})
	f.json("GET /repos/o/r/commits/main", map[string]any{
		"sha":      headSHA,
		"html_url": "https://github.com/o/r/commit/" + headSHA,
		"commit":   map[string]any{"message": "Third", "committer": map[string]any{"date": "2026-10-01T10:00:00Z"}},
	})
	f.mux.HandleFunc("GET /repos/o/r/commits", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sha") != headSHA || r.URL.Query().Get("per_page") != "1" {
			http.Error(w, "unexpected query "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		next := fmt.Sprintf("http://%s/repositories/1/commits?sha=%s&per_page=1&page=2", r.Host, headSHA)
		last := fmt.Sprintf("http://%s/repositories/1/commits?sha=%s&per_page=1&page=57", r.Host, headSHA)
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next", <%s>; rel="last"`, next, last))
		_ = json.NewEncoder(w).Encode([]map[string]any{{"sha": headSHA}})
	})
}

// compare serves the comparison of oldSHA and headSHA.
func (f *fakeGitHub) compare(status string, subjects ...string) {
	var commits []map[string]any
	for _, s := range subjects {
		commits = append(commits, map[string]any{"sha": "x", "commit": map[string]any{"message": s + "\n\nMore about it."}})
	}
	f.json("GET /repos/o/r/compare/"+oldSHA+"..."+headSHA, map[string]any{
		"status": status, "ahead_by": len(subjects), "behind_by": 0, "total_commits": len(subjects), "commits": commits,
	})
}

func newTestChecker(t *testing.T, gh *fakeGitHub) *Checker {
	t.Helper()
	c := New(events.NewHub(), lifecycle.NewExits())
	c.API = gh.URL
	c.Repo, c.RepoID = "o/r", ""
	c.Client = gh.Client()
	c.CacheDir = filepath.Join(t.TempDir(), "update")
	c.Token = func(context.Context) string { return "secret-token" }
	c.Executable = func() (string, error) { return "/home/me/kumo/dist/kumo", nil }
	c.StartInstaller = func(string, ...string) error { return fmt.Errorf("no installer in tests") }
	c.Version, c.Commit, c.GOOS = "1.0.50", oldSHA, "linux"
	c.FirstCheck = time.Hour
	t.Cleanup(c.Stop)
	return c
}

func check(t *testing.T, c *Checker) Status {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.Check(ctx)
}

func TestCheckLinuxAhead(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.branch()
	gh.compare("ahead", "First", "Second", "Third")
	c := newTestChecker(t, gh)
	st := check(t, c)
	if st.Error != "" || st.Checking || st.CheckedAt == 0 {
		t.Fatalf("error %q, checking %v, checked at %d", st.Error, st.Checking, st.CheckedAt)
	}
	if !st.Available || st.Latest == nil || st.Latest.Version != "1.0.57" || st.Latest.Commit != headSHA || st.Latest.Date != "2026-10-01T10:00:00Z" {
		t.Fatalf("available %v, latest %+v", st.Available, st.Latest)
	}
	if want := []string{"Third", "Second", "First"}; !slices.Equal(st.Changes, want) || st.ChangesTotal != 3 {
		t.Fatalf("changes %q (%d), want %q", st.Changes, st.ChangesTotal, want)
	}
	if st.Current.Version != "1.0.50" || st.Current.Commit != oldSHA {
		t.Fatalf("current %+v", st.Current)
	}
	// Not the Arch package: it can't install the update itself.
	if st.CanApply || !strings.Contains(st.ApplyNote, "git pull") {
		t.Fatalf("canApply %v: %q", st.CanApply, st.ApplyNote)
	}
	gh.each(func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Errorf("%s: headers %v", r.URL, r.Header)
		}
	})
}

func TestCheckLinuxIdentical(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.branch()
	gh.compare("identical")
	c := newTestChecker(t, gh)
	st := check(t, c)
	if st.Error != "" || st.Available || len(st.Changes) != 0 || st.Latest == nil || st.Latest.Commit != headSHA {
		t.Fatalf("error %q, available %v, changes %q, latest %+v", st.Error, st.Available, st.Changes, st.Latest)
	}
}

// A build that doesn't know its commit is offered the newest version.
func TestCheckLinuxUnknownCommit(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.branch()
	c := newTestChecker(t, gh)
	c.Commit = ""
	st := check(t, c)
	if st.Error != "" || !st.Available || st.Latest == nil || st.Latest.Commit != headSHA || !strings.Contains(st.Note, "can't tell") {
		t.Fatalf("error %q, available %v, latest %+v, note %q", st.Error, st.Available, st.Latest, st.Note)
	}
	for _, p := range gh.paths() {
		if strings.Contains(p, "/compare/") {
			t.Fatalf("compared without a commit: %s", p)
		}
	}
}

// A commit GitHub doesn't have (local changes, a rewritten branch).
func TestCheckLinuxCommitNotOnGitHub(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.branch()
	gh.mux.HandleFunc("GET /repos/o/r/compare/{basehead...}", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	c := newTestChecker(t, gh)
	st := check(t, c)
	if st.Error != "" || !st.Available || !strings.Contains(st.Note, "doesn't have") {
		t.Fatalf("error %q, available %v, note %q", st.Error, st.Available, st.Note)
	}
}

// Without access to the private repository, the status says how to sign in.
func TestCheckPrivateRepository(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		gh := newFakeGitHub(t)
		gh.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		})
		c := newTestChecker(t, gh)
		c.GOOS = goos
		st := check(t, c)
		if !strings.Contains(st.Error, "can't read github.com/o/r") || !strings.Contains(st.Hint, "gh auth login") || st.Available {
			t.Fatalf("%s: error %q, hint %q, available %v", goos, st.Error, st.Hint, st.Available)
		}
		if goos == "windows" && !strings.Contains(st.Hint, "Install missing programs") {
			t.Fatalf("windows hint: %q", st.Hint)
		}
	}
}

func windowsReleases(installer []byte) []map[string]any {
	sum := sha256.Sum256(installer)
	rel := func(tag string, draft, pre bool) map[string]any {
		v := strings.TrimPrefix(tag, "windows-v")
		return map[string]any{
			"tag_name": tag, "draft": draft, "prerelease": pre,
			"html_url":         "https://github.com/o/r/releases/tag/" + tag,
			"target_commitish": headSHA, "published_at": "2026-10-02T08:00:00Z",
			"assets": []map[string]any{
				{"id": 41, "name": "Kumo-" + v + "-windows-x64-portable.zip", "size": 10, "digest": "sha256:00"},
				{"id": 42, "name": "Kumo-Setup-" + v + "-windows-x64.exe", "size": len(installer), "digest": "sha256:" + hex.EncodeToString(sum[:])},
			},
		}
	}
	return []map[string]any{
		rel("windows-v1.0.9", false, false),
		rel("windows-v1.0.10", false, false), // the newest: 10 > 9
		rel("windows-v1.0.12", true, false),  // a draft
		rel("windows-v1.0.11", false, true),  // a prerelease
		rel("v9.9.9", false, false),          // not a Windows release
		rel("windows-v2.0.0-beta", false, false),
		rel("windows-v", false, false),
	}
}

func TestCheckWindowsPicksNewestRelease(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.json("GET /repos/o/r/releases", windowsReleases([]byte("installer")))
	gh.json("GET /repos/o/r/compare/"+oldSHA+"...windows-v1.0.10", map[string]any{
		"status": "ahead", "ahead_by": 2,
		"commits": []map[string]any{{"commit": map[string]any{"message": "Older"}}, {"commit": map[string]any{"message": "Newer"}}},
	})
	c := newTestChecker(t, gh)
	c.GOOS, c.Version = "windows", "1.0.9"
	st := check(t, c)
	if st.Error != "" || !st.Available || st.Latest == nil || st.Latest.Version != "1.0.10" || st.Latest.Commit != headSHA ||
		st.Latest.URL != "https://github.com/o/r/releases/tag/windows-v1.0.10" {
		t.Fatalf("error %q, available %v, latest %+v", st.Error, st.Available, st.Latest)
	}
	if !slices.Equal(st.Changes, []string{"Newer", "Older"}) || st.ChangesTotal != 2 {
		t.Fatalf("changes %q (%d)", st.Changes, st.ChangesTotal)
	}
	if c.asset == nil || c.asset.ID != 42 || c.asset.Name != "Kumo-Setup-1.0.10-windows-x64.exe" || !strings.HasPrefix(c.asset.Digest, "sha256:") {
		t.Fatalf("installer %+v", c.asset)
	}
	if !slices.Contains(gh.paths(), "/repos/o/r/releases?per_page=50") {
		t.Fatalf("requests: %q", gh.paths())
	}
	// A portable copy (no uninstaller next to it) can't install it.
	if st.CanApply || !strings.Contains(st.ApplyNote, "portable") {
		t.Fatalf("canApply %v: %q", st.CanApply, st.ApplyNote)
	}

	c.Version = "1.0.10"
	if st := check(t, c); st.Available || st.Latest.Version != "1.0.10" {
		t.Fatalf("up to date: available %v, latest %+v", st.Available, st.Latest)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.0.10", "1.0.9", 1},
		{"1.0.9", "1.0.10", -1},
		{"1.0.0", "1.0", 0},
		{"1.1", "1.0.99", 1},
		{"2.0.0", "10.0.0", -1},
		{"1.0.5", "1.0.5", 0},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestLastPage(t *testing.T) {
	link := `<https://api.github.com/repositories/1/commits?sha=x&per_page=1&page=2>; rel="next", <https://api.github.com/repositories/1/commits?sha=x&per_page=1&page=53>; rel="last"`
	if n := lastPage(link); n != 53 {
		t.Fatalf("lastPage = %d", n)
	}
	if n := lastPage(""); n != 0 {
		t.Fatalf("lastPage of nothing = %d", n)
	}
}

// The environment's tokens come first, in this order.
func TestTokenFromEnvironment(t *testing.T) {
	t.Setenv("KUMO_GITHUB_TOKEN", "kumo")
	t.Setenv("GH_TOKEN", "gh")
	t.Setenv("GITHUB_TOKEN", "github")
	ctx := context.Background()
	if tok := githubToken(ctx); tok != "kumo" {
		t.Fatalf("token %q, want KUMO_GITHUB_TOKEN's", tok)
	}
	t.Setenv("KUMO_GITHUB_TOKEN", "")
	if tok := githubToken(ctx); tok != "gh" {
		t.Fatalf("token %q, want GH_TOKEN's", tok)
	}
	t.Setenv("GH_TOKEN", "")
	if tok := githubToken(ctx); tok != "github" {
		t.Fatalf("token %q, want GITHUB_TOKEN's", tok)
	}
}

// Then the GitHub CLI's, then git's credential helpers', without prompts.
func TestTokenFromPrograms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in programs are shell scripts")
	}
	for _, k := range []string{"KUMO_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(k, "")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	record := filepath.Join(t.TempDir(), "git.txt")
	t.Setenv("FAKE_GIT_RECORD", record)
	script := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("gh", `[ "$*" = "auth token --hostname github.com" ] || exit 2
echo gh-token
`)
	script("git", `[ "$*" = "credential fill" ] || exit 2
input=""
while IFS= read -r line; do input="$input$line;"; done
printf 'stdin=%s\nprompt=%s\ngcm=%s\naskpass=%s/%s\n' "$input" "$GIT_TERMINAL_PROMPT" "$GCM_INTERACTIVE" "${GIT_ASKPASS-unset}" "${GIT_ASKPASS:+set}" >"$FAKE_GIT_RECORD"
printf 'protocol=https\nhost=github.com\nusername=me\npassword=git-token\n'
`)
	ctx := context.Background()
	if tok := githubToken(ctx); tok != "gh-token" {
		t.Fatalf("token %q, want gh's", tok)
	}
	// gh isn't signed in: git's credentials.
	script("gh", "echo 'not logged in' >&2; exit 1\n")
	if tok := githubToken(ctx); tok != "git-token" {
		t.Fatalf("token %q, want git's", tok)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if want := "stdin=protocol=https;host=github.com;;\nprompt=0\ngcm=never\naskpass=/\n"; string(got) != want {
		t.Fatalf("git credential fill got:\n%s\nwant:\n%s", got, want)
	}
	// Nothing anywhere: anonymous.
	_ = os.Remove(filepath.Join(bin, "gh"))
	script("git", "exit 128\n")
	if tok := githubToken(ctx); tok != "" {
		t.Fatalf("token %q, want none", tok)
	}
}

// ---------------------------------------------------------------------------
// Archives

type entry struct {
	hdr  tar.Header
	body string
}

func tarball(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if h.Mode == 0 && h.Typeflag != tar.TypeXGlobalHeader {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func dirEntry(name string) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: 0o755}}
}
func fileEntry(name, body string, mode int64) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode}, body: body}
}

func writeArchive(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.tar.gz")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtract(t *testing.T) {
	archive := writeArchive(t, tarball(t,
		entry{hdr: tar.Header{Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": headSHA}}},
		dirEntry("o-r-2222222/"),
		dirEntry("o-r-2222222/packaging/arch/"),
		fileEntry("o-r-2222222/packaging/arch/PKGBUILD", "pkgname=kumo\n", 0o644),
		fileEntry("o-r-2222222/packaging/kumo.sh", "#!/bin/sh\n", 0o755),
		entry{hdr: tar.Header{Typeflag: tar.TypeSymlink, Name: "o-r-2222222/passwd", Linkname: "/etc/passwd"}},
		entry{hdr: tar.Header{Typeflag: tar.TypeLink, Name: "o-r-2222222/hard", Linkname: "o-r-2222222/packaging/kumo.sh"}},
		entry{hdr: tar.Header{Typeflag: tar.TypeChar, Name: "o-r-2222222/tty", Devmajor: 5}},
		entry{hdr: tar.Header{Typeflag: tar.TypeFifo, Name: "o-r-2222222/fifo"}},
	))
	dest := filepath.Join(t.TempDir(), "src")
	top, err := extract(archive, dest)
	if err != nil {
		t.Fatal(err)
	}
	if top != filepath.Join(dest, "o-r-2222222") {
		t.Fatalf("top %q", top)
	}
	if b, err := os.ReadFile(filepath.Join(top, "packaging", "arch", "PKGBUILD")); err != nil || string(b) != "pkgname=kumo\n" {
		t.Fatalf("PKGBUILD: %q %v", b, err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(top, "packaging", "kumo.sh"))
		if err != nil || st.Mode().Perm()&0o111 == 0 {
			t.Fatalf("kumo.sh lost its exec bit: %v %v", st, err)
		}
		st, err = os.Stat(filepath.Join(top, "packaging", "arch", "PKGBUILD"))
		if err != nil || st.Mode().Perm()&0o111 != 0 {
			t.Fatalf("PKGBUILD became executable: %v %v", st, err)
		}
	}
	for _, name := range []string{"passwd", "hard", "tty", "fifo"} {
		if _, err := os.Lstat(filepath.Join(top, name)); !os.IsNotExist(err) {
			t.Errorf("%s was extracted (%v)", name, err)
		}
	}
	// The folder must be new: an update never unpacks over old files.
	if _, err := extract(archive, dest); err == nil {
		t.Fatal("extracted into an existing folder")
	}
}

func TestExtractRefusesPathsOutside(t *testing.T) {
	for _, name := range []string{"o-r-2222222/../../evil", "../evil", "/tmp/evil", "o-r-2222222/a/../../../evil"} {
		archive := writeArchive(t, tarball(t, dirEntry("o-r-2222222/"), fileEntry(name, "pwned", 0o644)))
		parent := t.TempDir()
		dest := filepath.Join(parent, "a", "src")
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := extract(archive, dest); err == nil {
			t.Errorf("%s: extracted", name)
		}
		_ = filepath.WalkDir(parent, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Name() == "evil" {
				t.Errorf("%s: wrote %s", name, p)
			}
			return nil
		})
	}
}

// ---------------------------------------------------------------------------
// Windows

func TestDownloadChecksDigest(t *testing.T) {
	installer := []byte("MZ the new Kumo installer")
	gh := newFakeGitHub(t)
	gh.mux.HandleFunc("GET /repos/o/r/releases/assets/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/octet-stream" {
			http.Error(w, "wrong Accept", http.StatusNotAcceptable)
			return
		}
		// Like GitHub: a redirect to a signed address elsewhere.
		http.Redirect(w, r, "/files/installer.exe?sig=abc", http.StatusFound)
	})
	gh.mux.HandleFunc("GET /files/installer.exe", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(installer) })
	c := newTestChecker(t, gh)
	g := &github{api: gh.URL, repo: "o/r", client: gh.Client(), token: "secret-token", userAgent: "Kumo/test"}
	dir := t.TempDir()
	file := filepath.Join(dir, "Kumo-Setup-1.0.10-windows-x64.exe")
	var last int64
	if err := c.download(context.Background(), g, "/repos/o/r/releases/assets/42", "application/octet-stream", file, int64(len(installer)), func(done, total int64) {
		if total != int64(len(installer)) {
			t.Errorf("total %d", total)
		}
		last = done
	}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); !bytes.Equal(b, installer) || last != int64(len(installer)) {
		t.Fatalf("downloaded %q, progress %d", b, last)
	}
	sum := sha256.Sum256(installer)
	if err := verifyDigest(file, "sha256:"+hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("right digest: %v", err)
	}
	if err := verifyDigest(file, "sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong digest accepted")
	}
	if err := verifyDigest(file, ""); err != nil {
		t.Fatalf("no digest: %v", err)
	}
	// A short download fails.
	if err := c.download(context.Background(), g, "/repos/o/r/releases/assets/42", "application/octet-stream", file+"2", 1000, func(int64, int64) {}); err == nil {
		t.Fatal("incomplete download accepted")
	}
}

// installedWindows makes c look like an installed Windows copy.
func installedWindows(t *testing.T, c *Checker) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Kumo")
	if err := os.MkdirAll(filepath.Join(dir, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Uninstall Kumo.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c.GOOS, c.Version = "windows", "1.0.9"
	c.Executable = func() (string, error) { return filepath.Join(dir, "resources", "kumo.exe"), nil }
}

// waitState waits until the update is ready or failed.
func waitState(t *testing.T, c *Checker) Status {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		applying := c.applying
		c.mu.Unlock()
		if st := c.Status(); !applying && st.State != StateDownloading && st.State != StateBuilding {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the update didn't finish: %+v", c.Status())
	return Status{}
}

func TestApplyWindows(t *testing.T) {
	installer := []byte("MZ the new Kumo installer")
	gh := newFakeGitHub(t)
	gh.json("GET /repos/o/r/releases", windowsReleases(installer))
	gh.mux.HandleFunc("GET /repos/o/r/releases/assets/42", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(installer) })
	c := newTestChecker(t, gh)
	installedWindows(t, c)
	var started []string
	c.StartInstaller = func(path string, args ...string) error {
		started = append([]string{path}, args...)
		return nil
	}
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
	// The installer starts once Kumo has shut down.
	if started != nil {
		t.Fatal("the installer started before Kumo quit")
	}
	if err := exit.Before(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.CacheDir, "Kumo-Setup-1.0.10-windows-x64.exe")
	if want := []string{path, "--updated", "/S", "--force-run"}; !slices.Equal(started, want) {
		t.Fatalf("started %q, want %q", started, want)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, installer) {
		t.Fatalf("installer %q", b)
	}
	if err := c.Apply(); err == nil {
		t.Fatal("a second update started")
	}
}

func TestApplyWindowsDamagedDownload(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.json("GET /repos/o/r/releases", windowsReleases([]byte("MZ installer A")))
	gh.mux.HandleFunc("GET /repos/o/r/releases/assets/42", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("MZ installer B")) })
	c := newTestChecker(t, gh)
	installedWindows(t, c)
	check(t, c)
	if err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, c)
	if st.State != StateFailed || !strings.Contains(st.Message, "damaged") {
		t.Fatalf("state %q: %s", st.State, st.Message)
	}
	select {
	case e := <-c.Exits.C():
		t.Fatalf("Kumo was asked to quit: %+v", e)
	default:
	}
}

// When the installer can't start, Kumo has already shut down: it restarts
// and the next run says what happened.
func TestApplyWindowsInstallerDidntStart(t *testing.T) {
	installer := []byte("MZ installer")
	gh := newFakeGitHub(t)
	gh.json("GET /repos/o/r/releases", windowsReleases(installer))
	gh.mux.HandleFunc("GET /repos/o/r/releases/assets/42", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(installer) })
	c := newTestChecker(t, gh)
	installedWindows(t, c)
	c.StartInstaller = func(string, ...string) error { return fmt.Errorf("access denied") }
	check(t, c)
	if err := c.Apply(); err != nil {
		t.Fatal(err)
	}
	exit := <-c.Exits.C()
	if err := exit.Before(); err == nil {
		t.Fatal("Before succeeded")
	}
	next := newTestChecker(t, gh)
	next.CacheDir = c.CacheDir
	next.Start()
	if st := next.Status(); st.State != StateFailed || !strings.Contains(st.Message, "access denied") || !strings.Contains(st.Message, "Kumo-Setup-1.0.10-windows-x64.exe") {
		t.Fatalf("next run: state %q: %s", st.State, st.Message)
	}
	if _, err := os.Stat(filepath.Join(c.CacheDir, failureFile)); !os.IsNotExist(err) {
		t.Fatalf("the failure is shown again next time: %v", err)
	}
}

func TestApplyNeedsAnUpdate(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.branch()
	gh.compare("identical")
	c := newTestChecker(t, gh)
	c.Executable = func() (string, error) { return packagedExe, nil }
	check(t, c)
	if err := c.Apply(); err != ErrNoUpdate {
		t.Fatalf("Apply = %v, want ErrNoUpdate", err)
	}
}

// Kumo asks GitHub for its repository by number: its old name, after a
// rename, could be taken by anyone, who'd then make the updates.
func TestRepositoryByNumber(t *testing.T) {
	c := New(events.NewHub(), lifecycle.NewExits())
	t.Cleanup(c.Stop)
	if c.RepoID == "" || c.Repo != config.UpdateRepo {
		t.Fatalf("repo %q, id %q", c.Repo, c.RepoID)
	}
	g := &github{repo: c.Repo, repoID: c.RepoID}
	if g.root() != "/repositories/"+config.UpdateRepoID {
		t.Errorf("root %q", g.root())
	}
	t.Setenv("KUMO_UPDATE_REPO", "me/fork")
	c2 := New(events.NewHub(), lifecycle.NewExits())
	t.Cleanup(c2.Stop)
	if g := (&github{repo: c2.Repo, repoID: c2.RepoID}); g.root() != "/repos/me/fork" {
		t.Errorf("override: %q", g.root())
	}
}
