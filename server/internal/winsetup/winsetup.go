// Package winsetup installs the programs Kumo uses on Windows (Git and its
// bash, ani-cli, ffmpeg, mpv, yt-dlp, ...) with Scoop: it runs
// install-tools.ps1 in a PowerShell window, where the user follows it and
// answers it. The Windows installer ships the same script and offers to run
// it after installing Kumo.
package winsetup

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/simo1337s/animetest/server/internal/util"
)

//go:embed install-tools.ps1
var script []byte

var (
	mu      sync.Mutex
	running bool
)

// ErrRunning means the setup window is still open.
var ErrRunning = errors.New("the setup is already running: see its PowerShell window")

// Running reports whether the setup window is open.
func Running() bool {
	mu.Lock()
	defer mu.Unlock()
	return running
}

// Start opens the setup in a PowerShell window and returns at once.
func Start() error {
	if runtime.GOOS != "windows" {
		return errors.New("Kumo installs its programs itself on Windows only: install them with your package manager")
	}
	mu.Lock()
	defer mu.Unlock()
	if running {
		return ErrRunning
	}
	path := filepath.Join(os.TempDir(), "kumo-install-tools.ps1")
	if err := os.WriteFile(path, script, 0o600); err != nil {
		return err
	}
	ps := "powershell.exe"
	if root := os.Getenv("SystemRoot"); root != "" {
		ps = filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	cmd, err := util.StartInConsole(ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", path)
	if err != nil {
		return fmt.Errorf("couldn't start PowerShell: %w", err)
	}
	running = true
	go func() {
		_ = cmd.Wait()
		mu.Lock()
		running = false
		mu.Unlock()
	}()
	return nil
}
