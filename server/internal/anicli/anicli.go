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
	"strconv"
	"strings"
	"sync"
	"time"

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
case "$prompt" in
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
	once     sync.Once
	initErr  error
}

func New(s *config.Store) *Driver {
	return &Driver{
		settings: s,
		shimDir:  filepath.Join(config.RuntimeDir(), "anicli-shims"),
		histDir:  filepath.Join(config.DataDir(), "ani-cli"),
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
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
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
	_ = cmd.Run() // ani-cli "fails" on purpose when the shim selects nothing
	if ctx.Err() == context.DeadlineExceeded {
		os.RemoveAll(dir)
		return nil, errors.New("ani-cli timed out")
	}
	return &run{dir: dir, stdout: stdout.String(), stderr: stderr.String()}, nil
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

// Search returns the search results ani-cli shows for a query.
func (d *Driver) Search(ctx context.Context, query, mode string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty query")
	}
	r, err := d.exec(ctx, append(modeArgs(mode), query)...)
	if err != nil {
		return nil, err
	}
	defer r.cleanup()
	list := r.read("anime.txt")
	if strings.TrimSpace(list) == "" {
		// Exactly one result: ani-cli skipped the menu and went on to the
		// episode list.
		if strings.TrimSpace(r.read("episodes.txt")) != "" {
			return []Result{{Index: 1, Title: query, Episodes: len(splitLines(r.read("episodes.txt")))}}, nil
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
	args := append(modeArgs(mode), "-S", strconv.Itoa(index), query)
	r, err := d.exec(ctx, args...)
	if err != nil {
		return nil, err
	}
	defer r.cleanup()
	eps := splitLines(r.read("episodes.txt"))
	if len(eps) == 0 {
		return nil, fmt.Errorf("ani-cli: %s", r.lastError())
	}
	return eps, nil
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
	if quality == "" {
		quality = d.settings.Get().AniCli.Quality
	}
	args := append(modeArgs(mode), "--exit-after-play", "-S", strconv.Itoa(index), "-e", episode)
	if quality != "" && quality != "best" {
		args = append(args, "-q", quality)
	}
	args = append(args, query)
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
