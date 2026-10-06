//go:build windows

package anicli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/simo1337s/animetest/server/internal/util"
)

// findScript resolves the configured ani-cli. It is a shell script without
// an extension, which Windows' program search can't find: look for it by
// name on the PATH and where Scoop installs it.
func findScript(path string) (string, bool) {
	if strings.ContainsAny(path, `\/`) {
		return path, isFile(path)
	}
	var dirs []string
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		dirs = append(dirs, d)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "scoop", "apps", "ani-cli", "current"))
	}
	for _, d := range dirs {
		if p := filepath.Join(d, path); isFile(p) {
			return p, true
		}
	}
	return "", false
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// scriptCommand runs the ani-cli script with Git for Windows' bash, which
// also brings the curl, sed and grep it uses.
func scriptCommand(ctx context.Context, script string, args ...string) (*exec.Cmd, error) {
	bash, err := gitBash()
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, bash, append([]string{shellPath(script)}, args...)...), nil
}

var errNoBash = errors.New("ani-cli needs the bash of Git for Windows (" + util.InstallHint("ani-cli") + ")")

func gitBash() (string, error) {
	var candidates []string
	for _, env := range []string{"ProgramW6432", "ProgramFiles", "ProgramFiles(x86)"} {
		if d := os.Getenv(env); d != "" {
			candidates = append(candidates, filepath.Join(d, "Git", "bin", "bash.exe"))
		}
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		candidates = append(candidates, filepath.Join(d, "Programs", "Git", "bin", "bash.exe"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "scoop", "apps", "git", "current", "bin", "bash.exe"))
	}
	for _, c := range candidates {
		if isFile(c) {
			return c, nil
		}
	}
	// Any other bash on the PATH, but not WSL's (in System32): that's Linux.
	if p, err := exec.LookPath("bash"); err == nil && !strings.Contains(strings.ToLower(p), `\windows\system32\`) {
		return p, nil
	}
	return "", errNoBash
}

// shellPath is a path as the ani-cli script sees it: Git's bash takes
// C:/Users/… (backslashes would be escapes to it).
func shellPath(p string) string { return filepath.ToSlash(p) }
