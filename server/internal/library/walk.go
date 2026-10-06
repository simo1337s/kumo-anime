package library

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// walkResult is what a walk of one library root found.
type walkResult struct {
	files map[string]fs.FileInfo
	dirs  []string
	// failed lists folders that couldn't be read (permissions, I/O errors,
	// a network mount dropping out). Their files must not be treated as
	// deleted.
	failed []string
}

// walkLibrary walks a library root and returns its video files. Unlike
// filepath.WalkDir it follows symlinks — a symlinked root
// (~/Videos/Anime -> /mnt/hdd/Anime) and symlinked show folders — with
// loop protection. Paths are reported under root as configured, not under
// the symlink targets, so they stay stable.
func walkLibrary(root string, ignorePatterns []string) (*walkResult, error) {
	res := &walkResult{files: map[string]fs.FileInfo{}}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var walk func(dir, realDir string) error
	walk = func(dir, realDir string) error {
		if seen[realDir] {
			return nil
		}
		seen[realDir] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			res.failed = append(res.failed, dir)
			return err
		}
		res.dirs = append(res.dirs, dir)
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			p := filepath.Join(dir, name)
			isDir := e.IsDir()
			var info fs.FileInfo
			realChild := filepath.Join(realDir, name)
			if e.Type()&fs.ModeSymlink != 0 {
				st, err := os.Stat(p)
				if err != nil {
					// Broken link, e.g. to a show folder on an unplugged
					// drive: whatever was indexed under it isn't "deleted".
					res.failed = append(res.failed, p)
					continue
				}
				isDir, info = st.IsDir(), st
				if isDir {
					if realChild, err = filepath.EvalSymlinks(p); err != nil {
						continue
					}
				}
			}
			if isDir {
				if !ignored(name, ignorePatterns) {
					_ = walk(p, realChild)
				}
				continue
			}
			if !IsVideo(name) || ignored(name, ignorePatterns) {
				continue
			}
			if info == nil {
				if info, err = e.Info(); err != nil {
					continue
				}
			}
			if !info.Mode().IsRegular() {
				continue
			}
			res.files[p] = info
		}
		return nil
	}
	if err := walk(root, real); err != nil {
		return nil, err
	}
	return res, nil
}

// underAny reports whether path is inside one of the dirs.
func underAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if path == d || strings.HasPrefix(path, d+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
