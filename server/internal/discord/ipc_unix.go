//go:build !windows

package discord

import (
	"fmt"
	"os"
	"path/filepath"
)

// socketPaths lists where Discord (native, Flatpak, Snap, Vesktop) may
// listen.
func socketPaths() []string {
	var bases []string
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		bases = append(bases, d, filepath.Join(d, "app", "com.discordapp.Discord"), filepath.Join(d, ".flatpak", "dev.vencord.Vesktop", "xdg-run"), filepath.Join(d, "snap.discord"))
	}
	bases = append(bases, os.TempDir(), "/tmp")
	var out []string
	for _, b := range bases {
		for i := 0; i < 10; i++ {
			out = append(out, filepath.Join(b, fmt.Sprintf("discord-ipc-%d", i)))
		}
	}
	return out
}
