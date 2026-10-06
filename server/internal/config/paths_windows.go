//go:build windows

package config

import (
	"os"
	"path/filepath"
)

func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil { // %APPDATA%
		return filepath.Join(d, "Kumo")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "AppData", "Roaming", "Kumo")
}

// runtimeDir is also where the desktop app (desktop/main.js) looks.
func runtimeDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "Kumo", "run")
	}
	return filepath.Join(os.TempDir(), "kumo")
}

// programFile returns the first of the paths below the Program Files
// folders (and the user's own programs folder) that exists, or the first
// one in Program Files.
func programFile(rel ...string) string {
	var bases []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if d := os.Getenv(env); d != "" {
			bases = append(bases, d)
		}
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		bases = append(bases, filepath.Join(d, "Programs"))
	}
	if len(bases) == 0 {
		bases = []string{`C:\Program Files`}
	}
	for _, b := range bases {
		for _, r := range rel {
			if p := filepath.Join(b, r); fileExists(p) {
				return p
			}
		}
	}
	return filepath.Join(bases[0], rel[0])
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func findQbittorrent(string) string {
	return programFile(`qBittorrent\qbittorrent.exe`)
}

func defaultTransmission() string {
	return programFile(`Transmission\transmission-qt.exe`)
}
