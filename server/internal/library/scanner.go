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
	defer s.mu.Unlock()
	s.running.Store(true)
	defer s.running.Store(false)

	start := time.Now()
	cfg := s.settings.Get()
	roots := cfg.LibraryDirs()
	var existingRoots []string
	for _, r := range roots {
		if st, err := os.Stat(r); err == nil && st.IsDir() {
			existingRoots = append(existingRoots, r)
		}
	}
	if len(existingRoots) == 0 {
		return nil, errors.New("no library directory found — set one in Settings › Local Anime Library")
	}

	s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "walking", Message: "Looking for video files…"})
	found := map[string]fs.FileInfo{}
	for _, root := range existingRoots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := d.Name()
			if d.IsDir() {
				if path != root && (strings.HasPrefix(name, ".") || ignored(name, cfg.Library.IgnorePatterns)) {
					return filepath.SkipDir
				}
				return nil
			}
			if !IsVideo(name) || strings.HasPrefix(name, ".") || ignored(name, cfg.Library.IgnorePatterns) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			// Follow symlinks to files.
			if info.Mode()&fs.ModeSymlink != 0 {
				if info, err = os.Stat(path); err != nil || info.IsDir() {
					return nil
				}
			}
			found[path] = info
			if len(found)%250 == 0 {
				s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "walking", Done: len(found), Message: "Found " + itoa(len(found)) + " files…"})
			}
			return nil
		})
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
				toMatch = append(toMatch, old)
				changed = append(changed, old)
			}
			continue
		}
		f := &LocalFile{
			Path:    path,
			Dir:     filepath.Dir(path),
			Name:    filepath.Base(path),
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
			Parsed:  Parse(path, existingRoots),
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
	var removed []string
	for path := range byPath {
		if _, ok := found[path]; !ok {
			removed = append(removed, path)
		}
	}
	res.Removed = len(removed)

	if !opts.SkipMatching && len(toMatch) > 0 {
		m := NewMatcher(s.platform, cfg.Library.MatchThreshold, cfg.Library.MatchOutsideList)
		m.MatchFiles(ctx, toMatch, func(done, total int, title string) {
			s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "matching", Done: done, Total: total, Message: "Matching " + title})
		})
	}

	s.hub.Publish(events.ScanProgress, ScanProgress{Stage: "saving", Message: "Saving…"})
	if err := s.Store.Save(changed...); err != nil {
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
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				_ = w.Add(path)
			}
			return nil
		})
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
				go func() {
					if _, err := s.Scan(context.Background(), ScanOptions{}); err != nil && !errors.Is(err, ErrScanRunning) {
						log.Printf("auto scan: %v", err)
					}
				}()
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
