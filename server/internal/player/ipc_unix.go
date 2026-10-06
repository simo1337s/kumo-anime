//go:build !windows

package player

import (
	"path/filepath"

	"github.com/simo1337s/animetest/server/internal/config"
)

// ipcAddress is where mpv listens for commands: a Unix socket.
func ipcAddress(name string) string {
	return filepath.Join(config.RuntimeDir(), name+".sock")
}

func mpvProgram(path string) string { return path }
