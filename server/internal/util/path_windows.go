//go:build windows

package util

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

// lookBare finds a program by name on the PATH. Programs installed while
// Kumo runs (with Scoop, winget or an installer) are only on the PATH
// Windows gives the programs started afterwards: when the name isn't found,
// Kumo takes that PATH on (also for the programs it starts) and looks again.
func lookBare(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	if !refreshPath() {
		return "", false
	}
	p, err := exec.LookPath(name)
	return p, err == nil
}

var (
	pathMu      sync.Mutex
	pathChecked time.Time
	// newPathDirs lists the folders programs started now get on their PATH
	// (a variable for the tests).
	newPathDirs = registryPathDirs
)

// refreshPath adds to Kumo's PATH the folders of the current Windows PATH
// that it lacks, and reports whether there were any. It looks at most every
// few seconds.
func refreshPath() bool {
	pathMu.Lock()
	defer pathMu.Unlock()
	if time.Since(pathChecked) < 3*time.Second {
		return false
	}
	pathChecked = time.Now()
	cur := os.Getenv("PATH")
	have := map[string]bool{}
	for _, d := range filepath.SplitList(cur) {
		have[pathKey(d)] = true
	}
	var add []string
	for _, d := range newPathDirs() {
		if k := pathKey(d); d != "" && !have[k] {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				have[k] = true
				add = append(add, d)
			}
		}
	}
	if len(add) == 0 {
		return false
	}
	if cur != "" {
		add = append([]string{cur}, add...)
	}
	_ = os.Setenv("PATH", strings.Join(add, string(os.PathListSeparator)))
	return true
}

func pathKey(dir string) string {
	return strings.ToLower(strings.TrimRight(filepath.Clean(dir), `\`))
}

// registryPathDirs is the PATH Windows gives new programs: the system's and
// the user's, as installers and Scoop leave them in the registry, and the
// folders where Scoop and winget put the programs they install.
func registryPathDirs() []string {
	var dirs []string
	for _, k := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	} {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, typ, err := key.GetStringValue("Path")
		_ = key.Close()
		if err != nil {
			continue
		}
		if typ == registry.EXPAND_SZ {
			if x, err := registry.ExpandString(v); err == nil {
				v = x
			}
		}
		dirs = append(dirs, filepath.SplitList(v)...)
	}
	scoop := os.Getenv("SCOOP")
	if home, err := os.UserHomeDir(); err == nil && scoop == "" {
		scoop = filepath.Join(home, "scoop")
	}
	if scoop != "" {
		dirs = append(dirs, filepath.Join(scoop, "shims"))
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		dirs = append(dirs, filepath.Join(d, "Microsoft", "WinGet", "Links"))
	}
	return dirs
}
