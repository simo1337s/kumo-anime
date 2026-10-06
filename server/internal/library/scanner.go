package library

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/events"
)

type Scanner struct {
	Store    *Store
	platform *anilist.Platform
	settings *config.Store
	hub      *events.Hub

	mu      sync.Mutex
	running atomic.Bool
	rescan  atomic.Bool

	startMu   sync.Mutex // serializes StartWatcher (settings changes fire concurrently)
	watcherMu sync.Mutex
	watcher   *fsnotify.Watcher
	stopWatch chan struct{}
}

func NewScanner(store *Store, p *anilist.Platform, s *config.Store, hub *events.Hub) *Scanner {
	return &Scanner{Store: store, platform: p, settings: s, hub: hub}
}

type ScanOptions struct {
	// Re-match every unlocked file, not only new/unmatched ones.
	Full bool `json:"full"`
	// Skip the AniList matching step (just index files).
	SkipMatching bool `json:"skipMatching"`
}

type ScanResult struct {
	Total     int     `json:"total"`
	Added     int     `json:"added"`
	Removed   int     `json:"removed"`
	Matched   int     `json:"matched"`
	Unmatched int     `json:"unmatched"`
	Seconds   float64 `json:"seconds"`
}

type ScanProgress struct {
	Stage   string `json:"stage"` // walking | matching | saving
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Message string `json:"message"`
}

func (s *Scanner) Running() bool { return s.running.Load() }

var ErrScanRunning = errors.New("a library scan is already running")

// Scan indexes the library directories and matches new files.
func (s *Scanner) Scan(ctx context.Context, opts ScanOptions) (*ScanResult, error) {
	if !s.mu.TryLock() {
		return nil, ErrScanRunning
	}
	defer func() {
		s.mu.Unlock()
		// Files changed while we were busy: go again.
		if s.rescan.Swap(false) {
			go s.autoScan()
		}
	}()
	s.running.Store(true)
	defer s.running.Store(false)

	start := time.Now()
	cfg := s.settings.Get()
	roots := cfg.LibraryDirs()

	s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "walking", Message: "Looking for video files…"})
	found := map[string]fs.FileInfo{}
	var walked, failed []string // roots read successfully / folders that couldn't be read
	for _, root := range roots {
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			continue // missing (e.g. an unplugged drive): keep its files as they are
		}
		w, err := walkLibrary(root, cfg.Library.IgnorePatterns)
		if err != nil {
			log.Printf("scan: can't read %s: %v", root, err)
			continue
		}
		walked = append(walked, root)
		failed = append(failed, w.failed...)
		for p, info := range w.files {
			found[p] = info
		}
		s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "walking", Done: len(found), Message: "Found " + itoa(len(found)) + " files…"})
	}
	if len(walked) == 0 {
		return nil, errors.New("no library directory found — set one in Settings › Local Anime Library")
	}

	existing, err := s.Store.All()
	if err != nil {
		return nil, err
	}
	byPath := map[string]*LocalFile{}
	for _, f := range existing {
		byPath[f.Path] = f
	}

	res := &ScanResult{Total: len(found)}
	var changed []*LocalFile
	var toMatch []*LocalFile
	for path, info := range found {
		old := byPath[path]
		if old != nil && old.Size == info.Size() && old.ModTime == info.ModTime().Unix() && !opts.Full {
			if old.MediaID == 0 && !old.Locked && !old.Ignored {
				cp := *old
				toMatch = append(toMatch, &cp)
				changed = append(changed, &cp)
			}
			continue
		}
		f := &LocalFile{
			Path:    path,
			Dir:     filepath.Dir(path),
			Name:    filepath.Base(path),
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
			Parsed:  Parse(path, roots),
		}
		f.Kind = f.Parsed.Kind
		f.Episode = max(f.Parsed.Episode, 0)
		f.AiredEpisode = f.Parsed.Episode
		if old != nil {
			f.Locked, f.Ignored = old.Locked, old.Ignored
			if old.Locked {
				f.MediaID, f.Episode, f.Kind, f.MatchScore = old.MediaID, old.Episode, old.Kind, old.MatchScore
			}
		} else {
			res.Added++
		}
		if !f.Locked && !f.Ignored {
			toMatch = append(toMatch, f)
		}
		changed = append(changed, f)
	}
	removed := s.removedFiles(byPath, found, roots, walked, failed)
	res.Removed = len(removed)

	if !opts.SkipMatching && len(toMatch) > 0 {
		m := NewMatcher(s.platform, cfg.Library.MatchThreshold, cfg.Library.MatchOutsideList)
		m.MatchFiles(ctx, toMatch, func(done, total int, title string) {
			s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "matching", Done: done, Total: total, Message: "Matching " + title})
		})
	}

	s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "saving", Message: "Saving…"})
	// Matching can take minutes; don't overwrite what the user (or a
	// finished download) changed in the meantime.
	if err := s.Store.SaveScanned(changed, byPath); err != nil {
		return nil, err
	}
	if err := s.Store.Delete(removed...); err != nil {
		return nil, err
	}
	all, _ := s.Store.All()
	for _, f := range all {
		if f.Ignored {
			continue
		}
		if f.MediaID > 0 {
			res.Matched++
		} else {
			res.Unmatched++
		}
	}
	res.Seconds = time.Since(start).Seconds()
	s.hub.Publish(events.ScanDone, res)
	s.hub.Publish(events.LibraryUpdated, nil)
	return res, nil
}

// removedFiles picks the indexed files that are really gone. A file is only
// dropped when the folder it lives in was read successfully and the file
// itself no longer exists, so an unplugged drive, an unmounted share or an
// unreadable folder never wipes matches. Files outside the library folders
// (e.g. a separate download folder) are dropped only when their folder still
// exists but the file doesn't.
func (s *Scanner) removedFiles(byPath map[string]*LocalFile, found map[string]fs.FileInfo, roots, walked, failed []string) []string {
	// A root that is readable but suddenly empty while we know files in it
	// is most likely a mount point whose drive isn't mounted.
	foundIn := map[string]int{}
	knownIn := map[string]int{}
	for p := range found {
		for _, r := range walked {
			if underAny(p, []string{r}) {
				foundIn[r]++
			}
		}
	}
	for p := range byPath {
		for _, r := range walked {
			if underAny(p, []string{r}) {
				knownIn[r]++
			}
		}
	}
	var trusted []string
	for _, r := range walked {
		if foundIn[r] == 0 && knownIn[r] > 0 {
			log.Printf("scan: %s is empty but %d files were indexed there — is the drive mounted? Keeping them.", r, knownIn[r])
			s.hub.Error("Library folder " + r + " looks empty — is the drive connected? Kumo kept its " + itoa(knownIn[r]) + " files.")
			continue
		}
		trusted = append(trusted, r)
	}

	var removed []string
	for p := range byPath {
		if _, ok := found[p]; ok {
			continue
		}
		if underAny(p, failed) {
			continue
		}
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			continue // still there (e.g. written after the walk) or unknown
		}
		switch {
		case underAny(p, trusted):
			removed = append(removed, p)
		case underAny(p, roots):
			// in a missing or suspicious root: keep
		default:
			if st, err := os.Stat(filepath.Dir(p)); err == nil && st.IsDir() {
				removed = append(removed, p)
			}
		}
	}
	return removed
}

// autoScan runs an incremental scan for the folder watcher.
func (s *Scanner) autoScan() {
	if _, err := s.Scan(context.Background(), ScanOptions{}); err != nil {
		if errors.Is(err, ErrScanRunning) {
			s.rescan.Store(true)
			return
		}
		log.Printf("auto scan: %v", err)
	}
}

func ignored(name string, patterns []string) bool {
	lower := strings.ToLower(name)
	for _, p := range patterns {
		if ok, _ := filepath.Match(strings.ToLower(p), lower); ok {
			return true
		}
	}
	return false
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---------------------------------------------------------------------------
// File watcher (automatic library refresh)

// StartWatcher watches the library directories and triggers an incremental
// scan shortly after files are added, removed or renamed.
func (s *Scanner) StartWatcher() {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.StopWatcher()
	cfg := s.settings.Get()
	if !cfg.Library.AutoRefresh {
		return
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("library watcher: %v", err)
		return
	}
	for _, root := range cfg.LibraryDirs() {
		// Same walk as the scanner, so symlinked folders are watched too.
		if res, err := walkLibrary(root, cfg.Library.IgnorePatterns); err == nil {
			for _, dir := range res.dirs {
				_ = w.Add(dir)
			}
		}
	}
	stop := make(chan struct{})
	s.watcherMu.Lock()
	s.watcher, s.stopWatch = w, stop
	s.watcherMu.Unlock()

	go func() {
		var timer *time.Timer
		trigger := make(chan struct{}, 1)
		for {
			select {
			case <-stop:
				_ = w.Close()
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&fsnotify.Create != 0 {
					if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
						_ = w.Add(ev.Name)
					}
				}
				if ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename|fsnotify.Write) == 0 {
					continue
				}
				if !IsVideo(ev.Name) && filepath.Ext(ev.Name) != "" {
					continue
				}
				// Debounce: torrents/downloads write files for a while.
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(8*time.Second, func() {
					select {
					case trigger <- struct{}{}:
					default:
					}
				})
			case <-trigger:
				go s.autoScan()
			case err := <-w.Errors:
				if err != nil {
					log.Printf("library watcher: %v", err)
				}
			}
		}
	}()
}

func (s *Scanner) StopWatcher() {
	s.watcherMu.Lock()
	defer s.watcherMu.Unlock()
	if s.stopWatch != nil {
		close(s.stopWatch)
		s.stopWatch = nil
		s.watcher = nil
	}
}
