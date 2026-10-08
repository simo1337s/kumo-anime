// Package update finds newer versions of Kumo on GitHub and installs them.
//
// Kumo ships three ways, and updates each its own way:
//   - Arch Linux builds it from source with packaging/arch/PKGBUILD, so the
//     newest version is the head of the repository's default branch. The
//     update downloads that source, builds it with makepkg and installs the
//     package with pacman (through pkexec, which asks for the password).
//   - Windows installs the GitHub Release "Kumo <v> for Windows" (tag
//     windows-v<v>). The update downloads the newest one's installer and
//     runs it once Kumo has quit; the installer starts the new version.
//   - macOS has the app from the GitHub Release "Kumo <v> for macOS" (tag
//     macos-v<v>). The update downloads the newest one's zip of the app,
//     unpacks it next to the app and, once Kumo has quit, puts it in the
//     app's place and opens it (apply_mac.go).
//
// The requests carry the user's GitHub token when one can be found without
// asking (see githubToken): a private repository needs one, and it raises
// GitHub's limit on requests.
package update

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/lifecycle"
	"github.com/simo1337s/animetest/server/internal/util"
)

// Version is a build of Kumo.
type Version struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date,omitempty"` // when it was made (RFC 3339)
	URL     string `json:"url,omitempty"`  // its page on GitHub
}

// States of an update being installed.
const (
	StateIdle        = "idle"
	StateDownloading = "downloading"
	StateBuilding    = "building"
	StateInstalling  = "installing"
	StateReady       = "ready" // installed: Kumo restarts into it
	StateFailed      = "failed"
)

// Status is what the UI shows: GET /api/update, and the update-status event
// at every change.
type Status struct {
	Current Version `json:"current"`
	// Latest is the newest version, once a check found it.
	Latest       *Version `json:"latest"`
	Available    bool     `json:"available"`
	Changes      []string `json:"changes"` // commit subjects, newest first
	ChangesTotal int      `json:"changesTotal"`
	Note         string   `json:"note,omitempty"`
	CheckedAt    int64    `json:"checkedAt"` // unix seconds, 0 before the first check
	Checking     bool     `json:"checking"`
	Error        string   `json:"error,omitempty"` // the last check failed
	Hint         string   `json:"hint,omitempty"`  // what to do about it

	// CanApply: this copy can update itself (ApplyNote says how), or not
	// (ApplyNote says why).
	CanApply  bool   `json:"canApply"`
	ApplyNote string `json:"applyNote,omitempty"`

	State    string   `json:"state"`
	Progress int      `json:"progress"` // 0-100, -1 when unknown
	Message  string   `json:"message,omitempty"`
	Log      []string `json:"log"` // the last output lines
	// ManualCommand is a command to run in a terminal: to install the
	// update when Kumo can't ask for the password, or what the build needs.
	ManualCommand string `json:"manualCommand,omitempty"`
}

const (
	defaultAPI = "https://api.github.com"
	// packagedExe is where packaging/arch/PKGBUILD installs the server.
	packagedExe = "/usr/lib/kumo/kumo"
	// versionPrefix + the number of commits is a build's version, as the
	// PKGBUILD and the Windows release workflow make it.
	versionPrefix = "1.0."
	maxChanges    = 30
	maxLogLines   = 20
	// failureFile keeps why an update failed after Kumo had quit for it
	// (in CacheDir), for the next run to show.
	failureFile = "last-failure.txt"
)

// Checker looks for updates (30 seconds after Start, then every 6 hours, and
// when asked) and installs them.
type Checker struct {
	// API is GitHub's REST API (tests use a fake one) and Repo the
	// repository, "owner/name".
	API    string
	Repo   string
	Client *http.Client
	Hub    *events.Hub
	// Exits ends Kumo when an update needs it to restart or to quit.
	Exits *lifecycle.Exits
	// The running build.
	Version string
	Commit  string
	// GOOS decides how Kumo updates: "windows" and "darwin" from
	// releases, otherwise from the default branch.
	GOOS string
	// Executable is Kumo's server program, symlinks resolved.
	Executable func() (string, error)
	// PackagedExe is where the Arch package installs the server: only that
	// copy updates itself on Linux.
	PackagedExe string
	// CacheDir holds the downloads and the build (<user cache>/kumo/update).
	CacheDir string
	// Token returns a GitHub token, or "" (see githubToken).
	Token func(ctx context.Context) string
	// StartInstaller starts the Windows installer, on its own, outside
	// Kumo's job object (see startInstaller).
	StartInstaller func(path string, args ...string) error
	// macOS: StartHelper starts the program that opens the new app once
	// the desktop app (the process ParentPID) has quit, and Writable says
	// whether Kumo may put files in a folder.
	StartHelper func(name string, args ...string) error
	ParentPID   func() int
	Writable    func(dir string) bool
	// FirstCheck is how long after Start the first check runs, Interval the
	// time between checks.
	FirstCheck time.Duration
	Interval   time.Duration

	mu       sync.Mutex
	st       Status
	asset    *asset        // the newest release's installer (Windows) or app (macOS)
	inflight chan struct{} // closed when the running check ends
	applying bool
	pending  bool // a publish is scheduled (publishSoonLocked)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New makes the checker for this copy of Kumo. It does nothing until Start
// or a call.
func New(hub *events.Hub, exits *lifecycle.Exits) *Checker {
	repo := config.UpdateRepo
	if r := strings.TrimSpace(os.Getenv("KUMO_UPDATE_REPO")); r != "" {
		repo = r
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	c := &Checker{
		API:            defaultAPI,
		Repo:           repo,
		Client:         defaultClient,
		Hub:            hub,
		Exits:          exits,
		Version:        config.AppVersion,
		Commit:         config.BuildCommit(),
		GOOS:           runtime.GOOS,
		Executable:     executable,
		PackagedExe:    packagedExe,
		CacheDir:       filepath.Join(cache, "kumo", "update"),
		Token:          githubToken,
		StartInstaller: startInstaller,
		StartHelper:    util.Detach,
		ParentPID:      os.Getppid,
		Writable:       writable,
		FirstCheck:     30 * time.Second,
		Interval:       6 * time.Hour,
	}
	c.st.State = StateIdle
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}

// defaultClient talks to GitHub (and the hosts it redirects downloads to):
// public addresses only, like util.HTTP, but without its 30-second limit,
// which a download could need more than. Contexts bound every request.
var defaultClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           util.PublicDialContext(15 * time.Second),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	},
}

// executable is the server's program, symlinks resolved (/usr/bin/kumo-server
// is a link to /usr/lib/kumo/kumo).
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// Linux names a program that was replaced while it ran "<path> (deleted)".
	exe = strings.TrimSuffix(exe, " (deleted)")
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// Start runs the checks in the background until Stop. It also shows why an
// update failed after Kumo had quit for it.
func (c *Checker) Start() {
	if b, err := os.ReadFile(filepath.Join(c.CacheDir, failureFile)); err == nil {
		c.mu.Lock()
		c.st.State, c.st.Message, c.st.Progress = StateFailed, strings.TrimSpace(string(b)), 0
		c.mu.Unlock()
		_ = os.Remove(filepath.Join(c.CacheDir, failureFile))
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.loop()
	}()
}

func (c *Checker) loop() {
	select {
	case <-c.ctx.Done():
		return
	case <-time.After(c.FirstCheck):
	}
	c.cleanup()
	for {
		ctx, cancel := context.WithTimeout(c.ctx, 2*time.Minute)
		c.Check(ctx)
		cancel()
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(c.Interval):
		}
	}
}

// cleanup removes what the last update left (the source, the build, the
// installer, the new app), unless an update is using it: being installed,
// Kumo quitting for it, or its package waiting to be installed by hand.
func (c *Checker) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.applying || c.st.State == StateReady || c.st.State == StateInstalling {
		return
	}
	// The Windows installer may still be finishing: whatever it holds
	// stays until the next update.
	_ = removeAll(c.CacheDir)
	// macOS: what an update left next to the app (see apply_mac.go).
	if c.GOOS == "darwin" {
		if exe, err := c.Executable(); err == nil {
			if app := macBundle(exe); app != "" {
				_ = removeAll(macStaging(app))
			}
		}
	}
}

// Stop ends the checks and an update being installed (a build is stopped).
func (c *Checker) Stop() {
	c.cancel()
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		log.Printf("update: still stopping, not waiting any longer")
	}
}

// Status is the current status.
func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Checker) statusLocked() Status {
	s := c.st
	s.Current = Version{Version: c.Version, Commit: c.Commit}
	if s.Latest != nil {
		latest := *s.Latest
		s.Latest = &latest
	}
	s.Changes = append([]string{}, s.Changes...)
	s.Log = append([]string{}, s.Log...)
	s.CanApply, s.ApplyNote = c.applyable()
	if s.CanApply && fromReleases(c.GOOS) && s.Available && c.asset == nil {
		s.CanApply, s.ApplyNote = false, "The newest release has no "+releaseFile(c.GOOS)+": download Kumo from its page."
	}
	return s
}

// applyable says whether this copy of Kumo can install updates itself, and
// how it does, or why it can't.
func (c *Checker) applyable() (bool, string) {
	exe, err := c.Executable()
	switch c.GOOS {
	case "linux":
		if err != nil || exe != c.PackagedExe {
			return false, "This copy of Kumo isn't the Arch package: update it with git pull and make (or build the PKGBUILD again)."
		}
		for _, tool := range []string{"makepkg", "pacman"} {
			if _, ok := util.LookPath(tool); !ok {
				return false, "Updating needs " + tool + ", which isn't installed: sudo pacman -S --needed base-devel go nodejs npm"
			}
		}
		return true, "Kumo downloads the new version, builds it with makepkg and installs it with pacman, which asks for your password."
	case "windows":
		// <install dir>\resources\kumo.exe, next to <install dir>\Uninstall Kumo.exe.
		if err != nil || !isFile(filepath.Join(filepath.Dir(filepath.Dir(exe)), "Uninstall Kumo.exe")) {
			return false, "This is a portable copy of Kumo: to update it, download the new portable zip."
		}
		return true, "Kumo downloads the installer and runs it: Kumo closes, the new version installs and Kumo opens again."
	case "darwin":
		return c.macApplyable(exe, err)
	}
	return false, "Kumo can't update itself on this system."
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// publishLocked sends the status to the UI. Under c.mu, so that the UI gets
// the changes in order; Hub.Publish never blocks.
func (c *Checker) publishLocked() {
	if c.Hub != nil {
		c.Hub.Publish(events.UpdateStatus, c.statusLocked())
	}
}

// publishSoonLocked publishes within 250 ms: a burst of output lines or
// download progress becomes one event.
func (c *Checker) publishSoonLocked() {
	if c.pending {
		return
	}
	c.pending = true
	time.AfterFunc(250*time.Millisecond, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.pending = false
		c.publishLocked()
	})
}

// ---------------------------------------------------------------------------
// Checks

// result is what a check found.
type result struct {
	latest       *Version
	available    bool
	changes      []string
	changesTotal int
	note         string
	asset        *asset
}

// asset is a release file.
type asset struct {
	ID     int64
	Name   string
	Size   int64
	Digest string // "sha256:<hex>", or "" when GitHub has none
}

// Check looks for a newer version now and returns the status. When a check
// is already running, it waits for that one instead.
func (c *Checker) Check(ctx context.Context) Status {
	c.mu.Lock()
	if running := c.inflight; running != nil {
		c.mu.Unlock()
		select {
		case <-running:
		case <-ctx.Done():
		}
		return c.Status()
	}
	done := make(chan struct{})
	c.inflight = done
	c.st.Checking = true
	c.publishLocked()
	c.mu.Unlock()

	res, err := c.check(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.inflight = nil
	close(done)
	c.st.Checking = false
	if errors.Is(err, context.Canceled) {
		// Kumo is shutting down: that says nothing about updates.
		c.publishLocked()
		return c.statusLocked()
	}
	c.st.CheckedAt = time.Now().Unix()
	if err != nil {
		// What the last good check found still stands.
		c.st.Error, c.st.Hint = c.describe(err)
		log.Printf("update check: %s", c.st.Error)
	} else {
		c.st.Error, c.st.Hint = "", ""
		c.st.Latest, c.st.Available, c.st.Note = res.latest, res.available, res.note
		c.st.Changes, c.st.ChangesTotal = res.changes, res.changesTotal
		c.asset = res.asset
	}
	c.publishLocked()
	return c.statusLocked()
}

// describe turns a failed check into a message and a hint at what to do.
func (c *Checker) describe(err error) (string, string) {
	var ae *apiError
	switch {
	case errors.As(err, &ae) && (ae.status == http.StatusNotFound || ae.status == http.StatusUnauthorized):
		hint := "If the repository is private, sign in with the GitHub CLI (gh auth login) and check again."
		if c.GOOS == "windows" {
			hint = "If the repository is private, sign in to GitHub: Settings › App › Programs › Install missing programs signs you in, or run gh auth login."
		}
		return fmt.Sprintf("Kumo can't read github.com/%s (HTTP %d).", c.Repo, ae.status), hint
	case errors.As(err, &ae) && ae.rateLimited:
		return "GitHub's limit on requests was reached: Kumo checks again later.", "Signing in to GitHub (gh auth login) raises the limit."
	case errors.Is(err, context.DeadlineExceeded):
		return "GitHub didn't answer in time.", ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "Kumo can't reach GitHub: is this computer online?", ""
	}
	return "The update check failed: " + err.Error(), ""
}

func (c *Checker) check(ctx context.Context) (result, error) {
	g := &github{api: strings.TrimRight(c.API, "/"), repo: c.Repo, client: c.Client, token: c.Token(ctx), userAgent: "Kumo/" + c.Version}
	if fromReleases(c.GOOS) {
		return c.checkReleases(ctx, g)
	}
	return c.checkBranch(ctx, g)
}

// checkBranch: on Linux the newest version is the head of the default
// branch, which the PKGBUILD builds.
func (c *Checker) checkBranch(ctx context.Context, g *github) (result, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := g.getJSON(ctx, "/repos/"+g.repo, &repo); err != nil {
		return result{}, err
	}
	if repo.DefaultBranch == "" {
		return result{}, errors.New("GitHub didn't say which branch is the repository's main one")
	}
	head, err := g.commit(ctx, repo.DefaultBranch)
	if err != nil {
		return result{}, err
	}
	latest := &Version{Commit: head.SHA, Date: head.Commit.Committer.Date, URL: head.HTMLURL}
	// The version is only for showing, so a failure here doesn't matter.
	if n := g.commitCount(ctx, head.SHA); n > 0 {
		latest.Version = fmt.Sprintf("%s%d", versionPrefix, n)
	}
	res := result{latest: latest}
	if c.Commit == "" {
		res.available = true
		res.note = "Kumo can't tell which version this copy is (it was built without its git commit), so it offers the newest one."
		return res, nil
	}
	// Against the head's commit, not the branch: the version, the changes
	// and the source an update builds then all match, even if the branch
	// moves meanwhile.
	cmp, err := g.compare(ctx, c.Commit, head.SHA)
	var ae *apiError
	if errors.As(err, &ae) && (ae.status == http.StatusNotFound || ae.status == http.StatusUnprocessableEntity) {
		res.available = true
		res.note = "This copy was built from a commit GitHub doesn't have (" + short(c.Commit) + "), so Kumo can't tell what changed."
		return res, nil
	}
	if err != nil {
		return result{}, err
	}
	switch cmp.Status {
	case "ahead":
		res.available = cmp.AheadBy > 0
	case "diverged":
		res.available = cmp.AheadBy > 0
		res.note = "This copy has changes that aren't on GitHub: updating replaces them with GitHub's " + repo.DefaultBranch + "."
	case "behind":
		res.note = "This copy is newer than GitHub's " + repo.DefaultBranch + "."
	}
	if res.available {
		res.changes, res.changesTotal = cmp.changes(), cmp.AheadBy
		if len(cmp.Commits) < cmp.AheadBy {
			// GitHub lists the first 250 commits of a comparison: the newest
			// ones come from the branch's history instead.
			if recent, err := g.recentSubjects(ctx, head.SHA); err == nil {
				res.changes = recent
			}
		}
	}
	return res, nil
}

// fromReleases: Windows and macOS update from GitHub Releases, Linux from
// the default branch.
func fromReleases(goos string) bool { return goos == "windows" || goos == "darwin" }

// releasePrefix starts the tags of a system's releases: <prefix><version>.
func releasePrefix(goos string) string {
	if goos == "darwin" {
		return "macos-v"
	}
	return "windows-v"
}

// releaseAsset names the release file an update installs.
func releaseAsset(goos, version string) string {
	if goos == "darwin" {
		return "Kumo-" + version + "-macos-universal.zip"
	}
	return "Kumo-Setup-" + version + "-windows-x64.exe"
}

// releaseFile says what that file is, for messages.
func releaseFile(goos string) string {
	if goos == "darwin" {
		return "zip of the app"
	}
	return "installer"
}

// checkReleases: on Windows the newest version is the newest release tagged
// windows-v<version>, on macOS macos-v<version>.
func (c *Checker) checkReleases(ctx context.Context, g *github) (result, error) {
	var releases []release
	if _, err := g.getJSON(ctx, "/repos/"+g.repo+"/releases?per_page=50", &releases); err != nil {
		return result{}, err
	}
	prefix := releasePrefix(c.GOOS)
	best := pickRelease(releases, prefix)
	if best == nil {
		system := "Windows"
		if c.GOOS == "darwin" {
			system = "macOS"
		}
		return result{note: "There's no Kumo release for " + system + " yet."}, nil
	}
	version := strings.TrimPrefix(best.TagName, prefix)
	latest := &Version{Version: version, Date: best.PublishedAt, URL: best.HTMLURL}
	if isSHA(best.TargetCommitish) {
		latest.Commit = best.TargetCommitish
	}
	res := result{latest: latest, available: compareVersions(version, c.Version) > 0}
	file := releaseAsset(c.GOOS, version)
	for _, a := range best.Assets {
		if a.Name == file {
			res.asset = &asset{ID: a.ID, Name: a.Name, Size: a.Size, Digest: a.Digest}
		}
	}
	// What changed since this build, when it knows its commit: a bonus,
	// which doesn't fail the check.
	if res.available && c.Commit != "" {
		if cmp, err := g.compare(ctx, c.Commit, best.TagName); err == nil && cmp.AheadBy > 0 {
			res.changes, res.changesTotal = cmp.changes(), cmp.AheadBy
		}
	}
	return res, nil
}

// pickRelease returns the published release with the highest version
// tagged <prefix><version> (windows-v1.0.9), or nil.
func pickRelease(releases []release, prefix string) *release {
	var best *release
	for i := range releases {
		r := &releases[i]
		v, ok := strings.CutPrefix(r.TagName, prefix)
		if r.Draft || r.Prerelease || !ok || !releaseVersion.MatchString(v) {
			continue
		}
		if best == nil || compareVersions(v, strings.TrimPrefix(best.TagName, prefix)) > 0 {
			best = r
		}
	}
	return best
}

// compareVersions compares dotted version numbers ("1.0.10" > "1.0.9"); a
// missing part counts as 0, and only a part's leading digits count.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x = leadingNumber(pa[i])
		}
		if i < len(pb) {
			y = leadingNumber(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func leadingNumber(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' || n > 1e8 {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func isSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) })
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// ---------------------------------------------------------------------------
// Installing

// Reasons Apply refuses.
var (
	ErrRunning  = errors.New("an update is already being installed")
	ErrNoUpdate = errors.New("there's no newer version to install")
)

// Apply starts installing the newest version, in the background: the
// status follows it. On Linux it ends "ready" (restart Kumo to use it); on
// Windows Kumo quits for the installer, which starts the new version, and on
// macOS to swap in the new app, which then opens.
func (c *Checker) Apply() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Installing: on Windows and macOS, Kumo is quitting for the new
	// version.
	if c.applying || c.st.State == StateInstalling {
		return ErrRunning
	}
	if !c.st.Available || c.st.Latest == nil {
		return ErrNoUpdate
	}
	st := c.statusLocked()
	if !st.CanApply {
		return errors.New(st.ApplyNote)
	}
	target := *c.st.Latest
	var inst *asset
	if c.asset != nil {
		a := *c.asset
		inst = &a
	}
	c.applying = true
	c.st.State, c.st.Progress, c.st.Message = StateDownloading, -1, "Downloading Kumo "+display(target)+"…"
	c.st.Log, c.st.ManualCommand = nil, ""
	c.publishLocked()
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		g := &github{api: strings.TrimRight(c.API, "/"), repo: c.Repo, client: c.Client, token: c.Token(c.ctx), userAgent: "Kumo/" + c.Version}
		var err error
		switch c.GOOS {
		case "windows":
			err = c.applyWindows(c.ctx, g, target, inst)
		case "darwin":
			err = c.applyMac(c.ctx, g, target, inst)
		default:
			err = c.applyLinux(c.ctx, g, target)
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.applying = false
		if err != nil {
			c.st.State, c.st.Progress = StateFailed, 0
			c.st.Message = failureMessage(err)
			var me *manualError
			if errors.As(err, &me) {
				c.st.ManualCommand = me.command
			}
			log.Printf("update: %s", c.st.Message)
		}
		c.publishLocked()
	}()
	return nil
}

// display names a version: its number, or else its commit.
func display(v Version) string {
	if v.Version != "" {
		return v.Version
	}
	return short(v.Commit)
}

func failureMessage(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "Cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "The update took too long and was stopped."
	}
	return err.Error()
}

// manualError is a failure the user can fix with a command.
type manualError struct {
	msg     string
	command string
}

func (e *manualError) Error() string { return e.msg }

// step moves the update to a new state and tells the UI. progress is
// 0-100, or -1 when unknown.
func (c *Checker) step(state string, progress int, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.State, c.st.Progress = state, progress
	if msg != "" {
		c.st.Message = msg
	}
	c.publishLocked()
}

// output adds a line of a program's output to the log the UI shows.
func (c *Checker) output(line string) {
	if len(line) > 300 {
		line = strings.ToValidUTF8(line[:300], "") + "…"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Log = append(c.st.Log, line)
	if n := len(c.st.Log); n > maxLogLines {
		c.st.Log = slices.Clone(c.st.Log[n-maxLogLines:])
	}
	c.publishSoonLocked()
}

// saveFailure keeps why an update failed after Kumo had shut down for it,
// for the next run to show (see Start).
func (c *Checker) saveFailure(msg string) {
	if err := os.MkdirAll(c.CacheDir, 0o700); err == nil {
		_ = os.WriteFile(filepath.Join(c.CacheDir, failureFile), []byte(msg), 0o600)
	}
}

// removeAll removes a folder, also when the build made parts of it
// read-only.
func removeAll(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
