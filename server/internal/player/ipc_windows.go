//go:build windows

package player

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ipcAddress is where mpv listens for commands: a named pipe.
func ipcAddress(name string) string {
	return `\\.\pipe\kumo-` + name
}

// mpvProgram picks mpv.exe for "mpv": the program search finds mpv.com, a
// console wrapper that also ships with mpv, first, and stopping the
// wrapper can leave mpv's window open.
func mpvProgram(path string) string {
	found, err := exec.LookPath(path)
	if err != nil || !strings.EqualFold(filepath.Ext(found), ".com") {
		return path
	}
	if exe := strings.TrimSuffix(found, filepath.Ext(found)) + ".exe"; isFile(exe) {
		return exe
	}
	return path
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
