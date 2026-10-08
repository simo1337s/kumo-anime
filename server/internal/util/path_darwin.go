package util

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// toolDirs are where Homebrew (Apple silicon, then Intel) and MacPorts put
// programs.
var toolDirs = []string{"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/local/sbin", "/opt/local/bin", "/opt/local/sbin"}

// Apps opened from the Finder or the Dock get the PATH /usr/bin:/bin:
// /usr/sbin:/sbin, without the folders of the programs Kumo uses (ffmpeg, mpv,
// yt-dlp, ani-cli from Homebrew): Kumo puts those first on its PATH, like a
// Terminal has them, for its own lookups and the programs it starts (ani-cli
// runs other tools in turn).
func init() {
	_ = os.Setenv("PATH", withToolDirs(os.Getenv("PATH"), toolDirs, isDir))
}

// addToolsDir puts Kumo's own programs folder first on the PATH.
func addToolsDir(dir string) {
	if dir != "" {
		_ = os.Setenv("PATH", withToolDirs(os.Getenv("PATH"), []string{dir}, func(string) bool { return true }))
	}
}

// withToolDirs puts the dirs that exist and that path lacks in front of it,
// in order.
func withToolDirs(path string, dirs []string, exists func(string) bool) string {
	have := filepath.SplitList(path)
	var add []string
	for _, d := range dirs {
		if !slices.Contains(have, d) && !slices.Contains(add, d) && exists(d) {
			add = append(add, d)
		}
	}
	if len(add) == 0 {
		return path
	}
	if path == "" {
		return strings.Join(add, string(os.PathListSeparator))
	}
	return strings.Join(add, string(os.PathListSeparator)) + string(os.PathListSeparator) + path
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
