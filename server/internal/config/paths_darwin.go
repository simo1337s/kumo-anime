package config

import (
	"os"
	"path/filepath"
)

// videosFolder is the home folder for videos (the library's default is its
// Anime folder).
const videosFolder = "Movies"

// defaultDataDir is ~/Library/Application Support/Kumo. The desktop app
// keeps its window's browser data in its electron folder (desktop/main.js).
func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "Kumo")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Kumo")
}

// runtimeDir is $TMPDIR/kumo ($TMPDIR is the user's own temporary folder),
// where the desktop app (desktop/main.js) looks too.
func runtimeDir() string {
	return filepath.Join(os.TempDir(), "kumo")
}

// findQbittorrent finds the qBittorrent app in /Applications or
// ~/Applications.
func findQbittorrent(home string) string {
	return findApp(home, "qbittorrent.app/Contents/MacOS/qbittorrent")
}

func defaultTransmission() string {
	home, _ := os.UserHomeDir()
	return findApp(home, "Transmission.app/Contents/MacOS/Transmission")
}

// findApp returns the program of an app in /Applications or ~/Applications
// (rel is below the folder), the first if neither has it.
func findApp(home, rel string) string {
	dirs := []string{"/Applications", filepath.Join(home, "Applications")}
	for _, d := range dirs {
		if p := filepath.Join(d, rel); fileExists(p) {
			return p
		}
	}
	return filepath.Join(dirs[0], rel)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
