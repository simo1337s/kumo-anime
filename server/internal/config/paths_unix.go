//go:build !windows && !darwin

package config

import (
	"os"
	"path/filepath"
)

// videosFolder is the home folder for videos (the library's default is its
// Anime folder).
const videosFolder = "Videos"

func defaultDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "kumo")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "kumo")
}

func runtimeDir() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "kumo")
}

// findQbittorrent prefers a native qBittorrent and falls back to the
// Flatpak export (system-wide or per-user install).
func findQbittorrent(home string) string {
	for _, p := range []string{
		"/usr/bin/qbittorrent",
		"/var/lib/flatpak/exports/bin/org.qbittorrent.qBittorrent",
		filepath.Join(home, ".local/share/flatpak/exports/bin/org.qbittorrent.qBittorrent"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/usr/bin/qbittorrent"
}

func defaultTransmission() string { return "/usr/bin/transmission-gtk" }
