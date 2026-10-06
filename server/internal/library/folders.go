package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// FolderInfo is a library folder as found on disk, for the manual matching
// tools. Unlike the index it also lists show folders a scan finds nothing
// to use in, with the reason, so a missing show can be explained: still
// downloading, unreadable, packed in archives, ignored…
type FolderInfo struct {
	Dir        string `json:"dir"`
	Label      string `json:"label"`             // path below its library folder
	Videos     int    `json:"videos"`            // video files directly inside, as scans see them
	NotIndexed int    `json:"notIndexed"`        // of those, not in the library index yet
	Problem    string `json:"problem,omitempty"` // why it has nothing to match, if so
}

const maxFolders = 5000

// Extensions of downloads in progress: qBittorrent, µTorrent, BitComet,
// Transmission and browsers, yt-dlp, aria2.
var unfinishedExts = map[string]bool{
	".!qb": true, ".!ut": true, ".bc!": true, ".part": true, ".partial": true,
	".crdownload": true, ".download": true, ".ytdl": true, ".aria2": true,
}

var reArchive = regexp.MustCompile(`^\.(rar|zip|7z|tar|gz|tgz|xz|zst|bz2|r\d\d|\d\d\d)$`)

// folderContents is what a folder holds besides usable videos.
type folderContents struct {
	visible    int // entries that aren't hidden
	ignored    int // videos ignored by hand
	unfinished int // downloads in progress
	archives   []string
	discs      int
	patterned  int // videos skipped by an ignore pattern
	patterns   []string
	other      map[string]int // other files by extension
}

// Folders lists the folders of the library folders that hold videos, and
// the show folders (the ones directly in a library folder) that don't, with
// the reason.
func (s *Scanner) Folders() ([]FolderInfo, error) {
	cfg := s.settings.Get()
	patterns := cfg.Library.IgnorePatterns
	all, err := s.Store.All()
	if err != nil {
		return nil, err
	}
	index := make(map[string]*LocalFile, len(all))
	for _, f := range all {
		index[f.Path] = f
	}
	var out []FolderInfo
	add := func(info FolderInfo) bool {
		if len(out) >= maxFolders {
			return false
		}
		out = append(out, info)
		return true
	}
	for _, root := range cfg.LibraryDirs() {
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			add(FolderInfo{Dir: root, Label: root, Problem: readableError(err)})
			continue
		}
		seen := map[string]bool{}
		// walk lists dir and its subfolders, following symlinks like scans
		// do, and reports whether it listed any.
		var walk func(dir, realDir string, depth int) bool
		walk = func(dir, realDir string, depth int) bool {
			if seen[realDir] || len(out) >= maxFolders {
				return false
			}
			seen[realDir] = true
			info := FolderInfo{Dir: dir, Label: labelFor(root, dir)}
			entries, err := os.ReadDir(dir)
			if err != nil {
				info.Problem = readableError(err)
				return add(info)
			}
			c := folderContents{other: map[string]int{}}
			type sub struct{ dir, real string }
			var subs []sub
			for _, e := range entries {
				name := e.Name()
				p := filepath.Join(dir, name)
				isDir := e.IsDir()
				realChild := filepath.Join(realDir, name)
				if e.Type()&fs.ModeSymlink != 0 {
					st, err := os.Stat(p)
					if err != nil {
						continue
					}
					isDir = st.IsDir()
					if isDir {
						if realChild, err = filepath.EvalSymlinks(p); err != nil {
							continue
						}
					}
				}
				if strings.HasPrefix(name, ".") {
					// Hidden, like scans skip them, except Kumo's own
					// downloads in progress.
					if isDir && strings.HasPrefix(name, ".kumo-download-") {
						c.unfinished++
					}
					continue
				}
				c.visible++
				if isDir {
					if pat := ignorePattern(name, patterns); pat != "" {
						// A "Samples" folder inside a show is expected; a show
						// folder that is skipped isn't.
						if depth == 0 {
							add(FolderInfo{Dir: p, Label: labelFor(root, p), Problem: fmt.Sprintf("Its name matches the ignore pattern “%s” (Settings › Local Anime Library)", pat)})
						}
						continue
					}
					subs = append(subs, sub{p, realChild})
					continue
				}
				ext := strings.ToLower(filepath.Ext(name))
				switch {
				case IsVideo(name):
					if pat := ignorePattern(name, patterns); pat != "" {
						c.patterned++
						if !slices.Contains(c.patterns, pat) {
							c.patterns = append(c.patterns, pat)
						}
						break
					}
					info.Videos++
					if f := index[p]; f == nil {
						info.NotIndexed++
					} else if f.Ignored {
						c.ignored++
					}
				case unfinishedExts[ext]:
					c.unfinished++
				case reArchive.MatchString(ext):
					if strings.HasPrefix(ext, ".r") && ext != ".rar" {
						ext = ".rar" // split parts: .r00, .r01…
					}
					if !slices.Contains(c.archives, ext) {
						c.archives = append(c.archives, ext)
					}
				case ext == ".iso":
					c.discs++
				default:
					if ext == "" {
						ext = "no extension"
					}
					c.other[ext]++
				}
			}
			listed := false
			for _, sd := range subs {
				if walk(sd.dir, sd.real, depth+1) {
					listed = true
				}
			}
			info.Problem = c.problem(info.Videos, depth, listed, len(subs) > 0)
			if info.Videos > 0 || info.Problem != "" {
				listed = add(info) || listed
			}
			return listed
		}
		walk(root, real, 0)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label) })
	return out, nil
}

// problem explains why a folder has nothing to match. Only show folders
// (depth 1) without matchable folders below them get a reason for holding
// no videos at all: subtitles, fonts or scans in a show's subfolders are
// expected.
func (c *folderContents) problem(videos, depth int, listedBelow, hasSubs bool) string {
	switch {
	case videos > 0 && c.ignored == videos:
		if videos == 1 {
			return "You chose to ignore its video — undo that in Library tools › Ignored"
		}
		return fmt.Sprintf("You chose to ignore its %d videos — undo that in Library tools › Ignored", videos)
	case videos > 0:
		return ""
	case c.unfinished > 0:
		return fmt.Sprintf("Still downloading (%s) — it shows up when the download finishes", count(c.unfinished, "unfinished file", "unfinished files"))
	case depth != 1 || listedBelow:
		return ""
	case len(c.archives) > 0:
		return fmt.Sprintf("The videos are packed in archives (%s) — extract them first", strings.Join(c.archives, ", "))
	case c.discs > 0:
		return "Only disc images (.iso) — extract or remux the videos first"
	case c.patterned > 0:
		return fmt.Sprintf("Ignore patterns skip its videos (%s) — change them in Settings › Local Anime Library", strings.Join(c.patterns, ", "))
	case len(c.other) > 0:
		return "No video files here (" + c.summary() + ")"
	case hasSubs:
		return "No video files in this folder or its subfolders"
	case c.visible == 0:
		return "The folder is empty"
	}
	return ""
}

// summary lists the commonest other file types, like "12 .jpg, 3 .srt".
func (c *folderContents) summary() string {
	type kv struct {
		ext string
		n   int
	}
	var all []kv
	for ext, n := range c.other {
		all = append(all, kv{ext, n})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n || all[i].n == all[j].n && all[i].ext < all[j].ext })
	var parts []string
	for i, x := range all {
		if i == 3 {
			parts = append(parts, "…")
			break
		}
		if x.ext == "no extension" {
			parts = append(parts, count(x.n, "file without extension", "files without extension"))
		} else {
			parts = append(parts, fmt.Sprintf("%d %s", x.n, x.ext))
		}
	}
	return strings.Join(parts, ", ")
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func labelFor(root, dir string) string {
	if rel, err := filepath.Rel(root, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Base(dir)
}

func readableError(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "Kumo isn't allowed to read this folder (permission denied)"
	case errors.Is(err, fs.ErrNotExist):
		return "The folder doesn't exist (is the drive connected?)"
	}
	return err.Error()
}

// IndexFolders adds the videos in the given library folders (not their
// subfolders, which are listed on their own) to the index right away,
// without waiting for a scan, and returns the folders' indexed files. New
// files start unmatched.
func (s *Scanner) IndexFolders(dirs []string) ([]*LocalFile, error) {
	cfg := s.settings.Get()
	roots := cfg.LibraryDirs()
	existing, err := s.Store.All()
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(existing))
	for _, f := range existing {
		known[f.Path] = true
	}
	want := map[string]bool{}
	var added []*LocalFile
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if !filepath.IsAbs(dir) || !underAny(dir, roots) {
			return nil, fmt.Errorf("%s is not inside a library folder", dir)
		}
		want[dir] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %s", filepath.Base(dir), readableError(err))
		}
		for _, e := range entries {
			name := e.Name()
			p := filepath.Join(dir, name)
			if known[p] || strings.HasPrefix(name, ".") || !IsVideo(name) || ignored(name, cfg.Library.IgnorePatterns) {
				continue
			}
			info, err := os.Stat(p) // follows symlinks, like scans
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			f := &LocalFile{Path: p, Dir: dir, Name: name, Size: info.Size(), ModTime: info.ModTime().Unix(), Parsed: Parse(p, roots)}
			f.Kind = f.Parsed.Kind
			f.Episode = max(f.Parsed.Episode, 0)
			f.AiredEpisode = f.Parsed.Episode
			added = append(added, f)
		}
	}
	// Inserted only if a scan didn't add them meanwhile.
	if err := s.Store.SaveScanned(added, nil); err != nil {
		return nil, err
	}
	all, err := s.Store.All()
	if err != nil {
		return nil, err
	}
	out := []*LocalFile{}
	for _, f := range all {
		if !f.Ignored && want[f.Dir] {
			out = append(out, f)
		}
	}
	sortFiles(out)
	return out, nil
}
