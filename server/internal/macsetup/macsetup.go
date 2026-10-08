//go:build !windows

// Package macsetup installs the programs Kumo uses on macOS: it opens
// install-tools.command in Terminal, where the user follows it. The script
// downloads ready-made ffmpeg, ffprobe, yt-dlp and ani-cli for this Mac
// (Apple silicon or Intel) into Kumo's own folder (util.ToolsDir), with no
// Homebrew and no password, and installs mpv with Homebrew on Apple silicon
// when Homebrew is there.
package macsetup

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/util"
)

//go:embed install-tools.command
var script []byte

// ErrRunning means the setup's Terminal window is still open.
var ErrRunning = errors.New("the setup is already running: see its Terminal window")

var (
	mu sync.Mutex
	// opened is when Start opened the setup: it counts as running until
	// the script has written its pid file.
	opened time.Time
	// dir holds the script and its pid file (a variable for the tests).
	dir = config.RuntimeDir
	// open opens the script in Terminal, and goos is the system (variables
	// for the tests).
	open     = func(path string) error { return exec.Command("open", path).Run() }
	goos     = runtime.GOOS
	toolsDir = util.ToolsDir
)

const (
	name = "install-tools.command"
	// startGrace is how long the setup counts as running after Start
	// before its pid file shows up.
	startGrace = 15 * time.Second
	// maxRun: a pid file older than this is stale (the process ID may
	// have been reused).
	maxRun = 6 * time.Hour
)

// Running reports whether the setup is open.
func Running() bool {
	mu.Lock()
	defer mu.Unlock()
	return runningLocked()
}

func runningLocked() bool {
	if !opened.IsZero() && time.Since(opened) < startGrace {
		return true
	}
	pidFile := filepath.Join(dir(), name+".pid")
	st, err := os.Stat(pidFile)
	if err != nil || time.Since(st.ModTime()) > maxRun {
		return false
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	// Signal 0 only checks that the process exists.
	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// shellQuote quotes s for the shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Start opens the setup in Terminal and returns at once.
func Start() error {
	if goos != "darwin" {
		return errors.New("Kumo installs its programs itself on macOS and Windows only: install them with your package manager")
	}
	mu.Lock()
	defer mu.Unlock()
	if runningLocked() {
		return ErrRunning
	}
	path := filepath.Join(dir(), name)
	_ = os.Remove(path + ".pid")
	bin := toolsDir()
	if bin == "" {
		bin = filepath.Join(config.DataDir(), "bin")
	}
	text := strings.Replace(string(script), "'@KUMO_BIN@'", shellQuote(bin), 1)
	if err := os.WriteFile(path, []byte(text), 0o700); err != nil {
		return err
	}
	// WriteFile keeps the mode of a file that was there.
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	if err := open(path); err != nil {
		return fmt.Errorf("couldn't open Terminal: %w", err)
	}
	opened = time.Now()
	return nil
}
