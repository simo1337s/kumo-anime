// Package anicli drives the installed ani-cli script non-interactively.
//
// ani-cli only has an interactive interface, so Kumo runs it with small shim
// programs placed first in PATH: the fzf/rofi/dmenu shims record the menu
// entries ani-cli offers (search results, episode lists) and the player shim
// records the resolved stream URL, referrer and subtitle instead of playing.
// This keeps all the scraping logic inside ani-cli itself, so updating ani-cli
// (`ani-cli -U` or your AUR helper) keeps Kumo working when sites change.
package anicli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/util"
)

const menuShim = `#!/bin/sh
# Kumo menu shim for ani-cli: records the entries and selects nothing.
prompt=""
while [ $# -gt 0 ]; do
	case "$1" in
		--prompt | -p) shift; prompt="$1" ;;
		--prompt=*) prompt="${1#--prompt=}" ;;
	esac
	[ $# -gt 0 ] && shift
done
# "Playing episode 1 of <title>... " is the next/replay/quit menu ani-cli shows
# after playing. It comes first: the title in it can contain anything.
case "$prompt" in
	*laying*) kind=control ;;
	*nime*) kind=anime ;;
	*pisode*) kind=episodes ;;
	*uality*) kind=quality ;;
	*) kind=other ;;
esac
if [ -n "$KUMO_DUMP_DIR" ]; then
	cat >"$KUMO_DUMP_DIR/$kind.txt"
else
	cat >/dev/null
fi
exit 1
`

const playerShim = `#!/bin/sh
# Kumo player shim for ani-cli: records the arguments instead of playing.
out="${KUMO_DUMP_DIR:-/tmp}/player.txt"
: >"$out"
for a in "$@"; do
	printf '%s' "$a" | tr '\n' ' ' >>"$out"
	printf '\n' >>"$out"
done
exit 0
`

type Driver struct {
	settings *config.Store
	shimDir  string
	histDir  string
	timeout  time.Duration // for one ani-cli run
	once     sync.Once
	initErr  error

	// Search results are reused for a while: matches are checked against
	// a fresh search on every play, and each ani-cli search takes seconds.
	searchTTL time.Duration
	cacheMu   sync.Mutex
	searches  map[string]cachedSearch
}

type cachedSearch struct {
	res []Result
	at  time.Time
}

func New(s *config.Store) *Driver {
	return &Driver{
		settings:  s,
		shimDir:   filepath.Join(config.RuntimeDir(), "anicli-shims"),
		histDir:   filepath.Join(config.DataDir(), "ani-cli"),
		timeout:   90 * time.Second,
		searchTTL: 10 * time.Minute,
	}
}

func (d *Driver) init() error {
	d.once.Do(func() {
		if err := os.MkdirAll(d.shimDir, 0o700); err != nil {
			d.initErr = err
			return
		}
		_ = os.MkdirAll(d.histDir, 0o700)
		for _, name := range []string{"fzf", "rofi", "dmenu"} {
			if err := os.WriteFile(filepath.Join(d.shimDir, name), []byte(menuShim), 0o700); err != nil {
				d.initErr = err
				return
			}
		}
		d.initErr = os.WriteFile(filepath.Join(d.shimDir, "kumo-mpv-capture"), []byte(playerShim), 0o700)
	})
	return d.initErr
}

type Status struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
	Version   string `json:"version"`
	YtDlp     bool   `json:"ytDlp"`
	Ffmpeg    bool   `json:"ffmpeg"`
}

func (d *Driver) Status(ctx context.Context) Status {
	cfg := d.settings.Get()
	st := Status{}
	_, st.YtDlp = util.LookPath("yt-dlp")
	_, st.Ffmpeg = util.LookPath(cfg.Transcode.FfmpegPath)
	path, ok := util.LookPath(cfg.AniCli.Path)
	if !ok {
		return st
	}
	st.Installed, st.Path = true, path
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-V").Output()
	if err == nil {
		st.Version = strings.TrimSpace(string(out))
	}
	return st
}

type run struct {
	dir    string
	stdout string
	stderr string
}

func (r *run) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.dir, name))
	return string(b)
}

func (r *run) cleanup() { _ = os.RemoveAll(r.dir) }

var ErrNotInstalled = errors.New("ani-cli is not installed (install it from the AUR: `yay -S ani-cli`) or set its path in Settings › Online Streaming")

func (d *Driver) exec(ctx context.Context, args ...string) (*run, error) {
	if err := d.init(); err != nil {
		return nil, err
	}
	cfg := d.settings.Get()
	bin, ok := util.LookPath(cfg.AniCli.Path)
	if !ok {
		return nil, ErrNotInstalled
	}
	dir, err := os.MkdirTemp(d.shimDir, "run-")
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, args...)
	// ani-cli is a shell script: curl, sed & co. do its work, in subshells.
	// It gets a process group of its own so that a timeout, or a request that
	// went away, stops all of it. Killing only the shell would leave the rest
	// running, holding its output open, with Run waiting for them.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 3 * time.Second
	env := []string{
		"PATH=" + d.shimDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"KUMO_DUMP_DIR=" + dir,
		"ANI_CLI_PLAYER=" + filepath.Join(d.shimDir, "kumo-mpv-capture"),
		"ANI_CLI_HIST_DIR=" + d.histDir,
		"ANI_CLI_LOG=0",
		"ANI_CLI_MENU=fzf",
		"TERM=dumb",
	}
	for _, kv := range os.Environ() {
		k := kv[:max(strings.IndexByte(kv, '='), 0)]
		if k == "PATH" || strings.HasPrefix(k, "ANI_CLI_") || k == "TERM" {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = env
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Stdin = strings.NewReader("")
	err = cmd.Run() // ani-cli "fails" on purpose when the shim selects nothing
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil: // the caller gave up, e.g. the client went away
		err = ctx.Err()
	case runCtx.Err() != nil:
		err = fmt.Errorf("ani-cli timed out after %v", d.timeout)
	case err == nil, errors.As(err, &exitErr), errors.Is(err, exec.ErrWaitDelay):
		return &run{dir: dir, stdout: stdout.String(), stderr: stderr.String()}, nil
	default: // e.g. it could not be started
		err = fmt.Errorf("running ani-cli: %w", err)
	}
	_ = os.RemoveAll(dir)
	return nil, err
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b[()][A-Z0-9]|\x1b[>=]|\r`)

// lastError extracts the last error message ani-cli printed.
func (r *run) lastError() string {
	text := ansi.ReplaceAllString(r.stderr+"\n"+r.stdout, "")
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || strings.HasPrefix(l, "Checking dependencies") || strings.Contains(l, "tput") {
			continue
		}
		return l
	}
	return "no output"
}

func modeArgs(mode string) []string {
	if mode == "dub" {
		return []string{"--dub"}
	}
	return nil
}

// cleanQuery turns a title, or what the user typed, into a query ani-cli can
// search for. ani-cli 5 puts the query into its search URL as it is, with
// curl's URL globbing on: [] and {} make curl fail ("Connection error"),
// & # % ? break the URL's query string, and the other characters replaced
// here are not valid in a URL either. It takes an argument that starts with
// "-" for an option (-U updates the script, -D deletes the history), so the
// query never starts with one.
func cleanQuery(q string) string {
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune("[]{}()&#%?\"\\<>|^`", r) {
			return ' '
		}
		return r
	}, q)
	return strings.TrimLeft(strings.Join(strings.Fields(q), " "), "- ")
}

// queryArg returns the query argument for an ani-cli run (see cleanQuery).
func queryArg(query string) (string, error) {
	if q := cleanQuery(query); q != "" {
		return q, nil
	}
	return "", errors.New("empty query")
}

var reMediaTitle = regexp.MustCompile(`^(.*)\s+Episode\s+(\S+)$`)

// played returns the anime title and the episode ani-cli handed to the player,
// which it names "<title> Episode <n>"; ok is false if it played nothing.
func (r *run) played() (title, episode string, ok bool) {
	args := splitLines(r.read("player.txt"))
	if len(args) == 0 {
		return "", "", false
	}
	for _, a := range args {
		for _, flag := range []string{"--force-media-title=", "--mpv-force-media-title="} {
			if v, found := strings.CutPrefix(a, flag); found {
				if m := reMediaTitle.FindStringSubmatch(v); m != nil {
					return strings.TrimSpace(m[1]), m[2], true
				}
			}
		}
	}
	return "", "", true
}

// Result is one ani-cli search result.
type Result struct {
	Index    int    `json:"index"`
	Title    string `json:"title"`
	Episodes int    `json:"episodes"`
}

var (
	reResultLine = regexp.MustCompile(`^\s*(\d+)\s+(.+)$`)
	reEpCount    = regexp.MustCompile(`\((\d+)\s+episodes?\)\s*$`)
)

// Search returns the search results ani-cli shows for a query (from a
// short-lived cache when the same search ran recently).
func (d *Driver) Search(ctx context.Context, query, mode string) ([]Result, error) {
	key := mode + "\x00" + cleanQuery(query)
	if d.searchTTL > 0 {
		d.cacheMu.Lock()
		c, ok := d.searches[key]
		d.cacheMu.Unlock()
		if ok && time.Since(c.at) < d.searchTTL {
			return slices.Clone(c.res), nil
		}
	}
	res, err := d.search(ctx, query, mode)
	if err == nil && len(res) > 0 && d.searchTTL > 0 {
		d.cacheMu.Lock()
		if d.searches == nil || len(d.searches) > 500 {
			d.searches = map[string]cachedSearch{}
		}
		d.searches[key] = cachedSearch{res: slices.Clone(res), at: time.Now()}
		d.cacheMu.Unlock()
	}
	return res, err
}

func (d *Driver) search(ctx context.Context, query, mode string) ([]Result, error) {
	query = strings.TrimSpace(query)
	q, err := queryArg(query)
	if err != nil {
		return nil, err
	}
	r, err := d.exec(ctx, append(modeArgs(mode), "--exit-after-play", q)...)
	if err != nil {
		return nil, err
	}
	defer r.cleanup()
	list := r.read("anime.txt")
	if strings.TrimSpace(list) == "" {
		// Exactly one result: ani-cli skipped the menu and went on to the
		// episode list. Its title is unknown, so the query stands in for it...
		if eps := splitLines(r.read("episodes.txt")); len(eps) > 0 {
			return []Result{{Index: 1, Title: query, Episodes: len(eps)}}, nil
		}
		// ...unless it has a single episode, which ani-cli played right away.
		if title, _, ok := r.played(); ok {
			return []Result{{Index: 1, Title: util.FirstNonEmpty(title, query), Episodes: 1}}, nil
		}
		msg := r.lastError()
		if strings.Contains(strings.ToLower(msg), "no results") {
			return []Result{}, nil
		}
		return nil, fmt.Errorf("ani-cli: %s", msg)
	}
	var out []Result
	for _, line := range splitLines(list) {
		m := reResultLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		title := strings.TrimSpace(m[2])
		res := Result{Index: idx, Title: title}
		if em := reEpCount.FindStringSubmatch(title); em != nil {
			res.Episodes, _ = strconv.Atoi(em[1])
			res.Title = strings.TrimSpace(reEpCount.ReplaceAllString(title, ""))
		}
		out = append(out, res)
	}
	return out, nil
}

// Episodes returns the episode numbers available for a search result.
func (d *Driver) Episodes(ctx context.Context, query string, index int, mode string) ([]string, error) {
	q, err := queryArg(query)
	if err != nil {
		return nil, err
	}
	r, err := d.exec(ctx, append(modeArgs(mode), "--exit-after-play", "-S", strconv.Itoa(index), q)...)
	if err != nil {
		return nil, err
	}
	defer r.cleanup()
	if eps := splitLines(r.read("episodes.txt")); len(eps) > 0 {
		return eps, nil
	}
	// A single episode (a movie, most OVAs) gets no menu: ani-cli played it.
	if _, ep, ok := r.played(); ok {
		return []string{util.FirstNonEmpty(ep, "1")}, nil
	}
	return nil, fmt.Errorf("ani-cli: %s", r.lastError())
}

// Stream is a resolved, directly playable episode.
type Stream struct {
	URL      string `json:"url"`
	Referrer string `json:"referrer"`
	SubFile  string `json:"subFile"`
	Title    string `json:"title"`
	Episode  string `json:"episode"`
	Mode     string `json:"mode"`
}

// Resolve asks ani-cli for the stream of one episode.
func (d *Driver) Resolve(ctx context.Context, query string, index int, episode, mode, quality string) (*Stream, error) {
	q, err := queryArg(query)
	if err != nil {
		return nil, err
	}
	if quality == "" {
		quality = d.settings.Get().AniCli.Quality
	}
	args := append(modeArgs(mode), "--exit-after-play", "-S", strconv.Itoa(index), "-e", episode)
	if quality != "" && quality != "best" {
		args = append(args, "-q", quality)
	}
	args = append(args, q)
	r, err := d.exec(ctx, args...)
	if err != nil {
		return nil, err
	}
	defer r.cleanup()
	raw := r.read("player.txt")
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("ani-cli: %s", r.lastError())
	}
	s := &Stream{Episode: episode, Mode: mode}
	for _, a := range splitLines(raw) {
		switch {
		case strings.HasPrefix(a, "--referrer="):
			s.Referrer = strings.TrimPrefix(a, "--referrer=")
		case strings.HasPrefix(a, "--mpv-referrer="):
			s.Referrer = strings.TrimPrefix(a, "--mpv-referrer=")
		case strings.HasPrefix(a, "--http-referrer="):
			s.Referrer = strings.TrimPrefix(a, "--http-referrer=")
		case strings.HasPrefix(a, "--sub-file="):
			s.SubFile = strings.TrimPrefix(a, "--sub-file=")
		case strings.HasPrefix(a, "--sub-files="):
			s.SubFile = strings.Split(strings.TrimPrefix(a, "--sub-files="), ":http")[0]
		case strings.HasPrefix(a, "--force-media-title="):
			s.Title = strings.TrimPrefix(a, "--force-media-title=")
		case strings.HasPrefix(a, "--mpv-force-media-title="):
			s.Title = strings.TrimPrefix(a, "--mpv-force-media-title=")
		case strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://"):
			if s.URL == "" {
				s.URL = a
			}
		}
	}
	if s.URL == "" {
		return nil, errors.New("ani-cli did not return a stream URL")
	}
	return s, nil
}

func splitLines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	return out
}
