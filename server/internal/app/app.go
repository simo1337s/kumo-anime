// Package app wires every service together.
package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/anicli"
	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/discord"
	"github.com/simo1337s/animetest/server/internal/downloads"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/extensions"
	"github.com/simo1337s/animetest/server/internal/history"
	"github.com/simo1337s/animetest/server/internal/images"
	"github.com/simo1337s/animetest/server/internal/library"
	"github.com/simo1337s/animetest/server/internal/manga"
	"github.com/simo1337s/animetest/server/internal/metadata"
	"github.com/simo1337s/animetest/server/internal/player"
	"github.com/simo1337s/animetest/server/internal/stream"
	"github.com/simo1337s/animetest/server/internal/torrent"
)

type App struct {
	DB         *db.DB
	Settings   *config.Store
	Hub        *events.Hub
	Platform   *anilist.Platform
	Meta       *metadata.Service
	Files      *library.Store
	Scanner    *library.Scanner
	Library    *library.Service
	History    *history.Store
	Player     *player.Manager
	AniCli     *anicli.Driver
	Downloads  *downloads.Manager
	Torrents   *torrent.Manager
	AutoDL     *torrent.AutoDownloader
	Extensions *extensions.Manager
	Stream     *stream.Service
	Local      *stream.Local
	HLS        *stream.HLS
	Manga      *manga.Service
	Discord    *discord.Client
	Images     *images.Cache
	Logs       *LogBuffer

	// ShellToken authenticates the desktop window (see api middleware).
	ShellToken string
	DataDir    string

	ctx    context.Context
	cancel context.CancelFunc
}

// LogBuffer keeps the last server log lines for the Logs page.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	out   *os.File
}

func (l *LogBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		l.lines = append(l.lines, string(line))
	}
	if len(l.lines) > 1000 {
		l.lines = l.lines[len(l.lines)-1000:]
	}
	l.mu.Unlock()
	return l.out.Write(p)
}

func (l *LogBuffer) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func New(dataDir string) (*App, error) {
	logs := &LogBuffer{out: os.Stderr}
	log.SetOutput(logs)
	log.SetFlags(log.LstdFlags)

	d, err := db.Open(filepath.Join(dataDir, "kumo.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	settings, err := config.NewStore(d)
	if err != nil {
		return nil, err
	}
	hub := events.NewHub()
	platform := anilist.NewPlatform(d, hub)
	meta := metadata.NewService(d)
	files := library.NewStore(d)
	scanner := library.NewScanner(files, platform, settings, hub)
	hist := history.NewStore(d)
	lib := &library.Service{Store: files, Scanner: scanner, Platform: platform, Meta: meta, History: hist}
	pl := player.NewManager(settings, hist, d, platform, hub)
	ani := anicli.New(settings)
	dls := downloads.NewManager(settings, d, hub, files)
	torrents := torrent.NewManager(settings, hub)
	exts := extensions.NewManager(d, settings, hub)
	ctx, cancel := context.WithCancel(context.Background())

	a := &App{
		DB: d, Settings: settings, Hub: hub, Platform: platform, Meta: meta, Files: files, Scanner: scanner,
		Library: lib, History: hist, Player: pl, AniCli: ani, Downloads: dls, Torrents: torrents,
		AutoDL:     torrent.NewAutoDownloader(d, torrents, platform, files, hub),
		Extensions: exts, Stream: stream.NewService(d, exts, ani, platform), Local: stream.NewLocal(settings, files, d),
		Manga: manga.NewService(d, exts, platform), Discord: &discord.Client{}, Logs: logs,
		Images: images.New(filepath.Join(dataDir, "images")),
		ctx:    ctx, cancel: cancel, DataDir: dataDir,
	}
	a.HLS = stream.NewHLS(a.Local, hlsDir(dataDir))
	a.ShellToken = randomToken()
	a.wire()
	return a, nil
}

// hlsDir holds the in-app player's HLS segments: on disk, in the cache
// folder, not in /tmp, which is in memory on most Linux systems.
func hlsDir(dataDir string) string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "kumo", "hls")
	}
	return filepath.Join(dataDir, "hls")
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WriteShellToken stores the desktop-window token where only this user can
// read it ($XDG_RUNTIME_DIR/kumo/shell-token).
func (a *App) WriteShellToken() string {
	p := filepath.Join(config.RuntimeDir(), "shell-token")
	_ = os.WriteFile(p, []byte(a.ShellToken), 0o600)
	return p
}

func (a *App) wire() {
	// Torrent search can use extension providers.
	a.Torrents.ExtraProviders = a.Extensions.TorrentProviders

	// Downloads resolved through ani-cli or an online streaming extension.
	a.Downloads.Resolvers["anicli"] = func(ctx context.Context, it *downloads.Item) (*downloads.Resolved, error) {
		media, err := a.Platform.MediaLite(ctx, it.MediaID)
		if err != nil {
			return nil, err
		}
		res, err := a.Stream.Sources(ctx, stream.AniCliProvider, media, float64(it.Episode), it.Mode == "dub", "", it.Quality)
		if err != nil {
			return nil, err
		}
		src := res.Sources[0]
		r := &downloads.Resolved{URL: src.URL, Referrer: src.Referrer, Headers: src.Headers}
		if len(src.Subtitles) > 0 {
			r.SubURL = src.Subtitles[0].URL
		}
		return r, nil
	}
	a.Downloads.Resolvers["stream"] = func(ctx context.Context, it *downloads.Item) (*downloads.Resolved, error) {
		media, err := a.Platform.MediaLite(ctx, it.MediaID)
		if err != nil {
			return nil, err
		}
		res, err := a.Stream.Sources(ctx, it.Provider, media, float64(it.Episode), it.Mode == "dub", "", it.Quality)
		if err != nil {
			return nil, err
		}
		src := pickSource(res.Sources, it.Quality)
		r := &downloads.Resolved{URL: src.URL, Headers: src.Headers}
		for _, s := range src.Subtitles {
			if s.IsDefault || strings.Contains(strings.ToLower(s.Language), "eng") {
				r.SubURL = s.URL
				break
			}
		}
		return r, nil
	}

	// Auto play next local episode.
	a.Player.NextResolver = func(ctx context.Context, s *player.Session) (*player.PlayRequest, error) {
		if s.Source != "local" {
			return nil, nil
		}
		files, err := a.Files.ByMedia(s.MediaID)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.Kind == "main" && f.Episode == s.Episode+1 {
				media, _ := a.Platform.MediaLite(ctx, s.MediaID)
				title := f.Name
				if media != nil {
					title = fmt.Sprintf("%s — Episode %d", media.PreferredTitle(), f.Episode)
				}
				return &player.PlayRequest{MediaID: s.MediaID, Episode: f.Episode, Title: title, Source: "local", Target: f.Path}, nil
			}
		}
		return nil, nil
	}

	// Plugins hooks after progress updates.
	a.Player.OnProgress = append(a.Player.OnProgress, func(mediaID, episode int) {
		a.Extensions.FireHook("onPostUpdateEntryProgress", map[string]any{"mediaId": mediaID, "progress": episode})
	})

	// Discord rich presence.
	var (
		presenceMu   sync.Mutex
		lastPresence time.Time
		showing      bool // a presence is set and must be cleared eventually
	)
	clearPresence := func() {
		presenceMu.Lock()
		was := showing
		showing, lastPresence = false, time.Time{}
		presenceMu.Unlock()
		if was {
			a.Discord.Clear()
		}
	}
	a.Player.OnStatus = append(a.Player.OnStatus, func(s *player.Session) {
		cfg := a.Settings.Get()
		if !cfg.Discord.RichPresence || cfg.Discord.ClientID == "" || !s.Active {
			clearPresence()
			return
		}
		presenceMu.Lock()
		if time.Since(lastPresence) < 15*time.Second {
			presenceMu.Unlock()
			return
		}
		lastPresence, showing = time.Now(), true
		presenceMu.Unlock()
		media, err := a.Platform.MediaLite(context.Background(), s.MediaID)
		act := discord.Activity{Details: s.Title, State: fmt.Sprintf("Episode %d", s.Episode)}
		if err == nil {
			act.Details = media.PreferredTitle()
			act.LargeImage = media.CoverImage.Large
			act.LargeText = media.PreferredTitle()
		}
		if s.Duration > 0 && !s.Paused {
			act.Start = time.Now().Add(-time.Duration(s.Position) * time.Second)
			act.End = act.Start.Add(time.Duration(s.Duration) * time.Second)
		}
		_ = a.Discord.SetActivity(cfg.Discord.ClientID, act)
	})

	a.Extensions.Host = a.pluginServices()

	a.Settings.OnChange(func(old, cur config.Settings) {
		if !cur.Discord.RichPresence || cur.Discord.ClientID != old.Discord.ClientID {
			clearPresence() // turned off: don't leave "Watching…" behind
		}
		if old.Library.AutoRefresh != cur.Library.AutoRefresh || fmt.Sprint(old.LibraryDirs()) != fmt.Sprint(cur.LibraryDirs()) {
			a.Scanner.StartWatcher()
		}
		// A new library folder: index it right away.
		if fmt.Sprint(old.LibraryDirs()) != fmt.Sprint(cur.LibraryDirs()) && a.Scanner.HasLibrary() {
			a.Scanner.ScanSoon()
		}
		a.Hub.Publish(events.SettingsUpdated, nil)
	})
}

func pickSource(srcs []stream.Source, quality string) stream.Source {
	if quality != "" && quality != "best" {
		for _, s := range srcs {
			if strings.Contains(s.Quality, strings.TrimSuffix(quality, "p")) {
				return s
			}
		}
	}
	return srcs[0]
}

// Start runs the background jobs.
func (a *App) Start() {
	a.Extensions.LoadAll()
	a.Downloads.Start()
	a.Scanner.StartWatcher()
	go a.Torrents.RunCounter(a.ctx)
	go a.AutoDL.Loop(a.ctx, func() (bool, time.Duration) {
		cfg := a.Settings.Get()
		return cfg.Torrent.AutoDownloader, time.Duration(cfg.Torrent.AutoDownloadMinutes) * time.Minute
	})
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		a.Platform.RefreshViewer(ctx)
		if view, err := a.Library.Collection(ctx, false); err == nil {
			a.PrefetchCollectionArt(view)
		}
		if a.Settings.Get().Library.RefreshOnStartup && a.Scanner.HasLibrary() {
			if _, err := a.Scanner.Scan(a.ctx, library.ScanOptions{}); err != nil && !errors.Is(err, library.ErrScanRunning) {
				log.Printf("startup scan: %v", err)
			}
		}
	}()
}

func (a *App) Shutdown() {
	a.cancel()
	a.HLS.Close()
	a.Scanner.StopWatcher()
	a.Player.Stop()
	a.Discord.Clear()
	a.Extensions.Shutdown()
	_ = a.DB.Close()
}

func (a *App) Context() context.Context { return a.ctx }

// PrefetchCollectionArt downloads covers and banners of everything in the
// list and library so they are available offline.
func (a *App) PrefetchCollectionArt(view *library.CollectionView) {
	if view == nil {
		return
	}
	var urls []string
	add := func(items []*library.CollectionItem) {
		for _, it := range items {
			if it.Media == nil {
				continue
			}
			urls = append(urls, it.Media.CoverImage.ExtraLarge, it.Media.CoverImage.Large, it.Media.BannerImage)
		}
	}
	for _, l := range view.Lists {
		add(l.Items)
	}
	add(view.LocalOnly)
	for _, c := range view.ContinueWatching {
		urls = append(urls, c.Image)
	}
	a.Images.Prefetch(urls...)
}

// PrefetchEntryArt downloads an anime's artwork and episode thumbnails.
func (a *App) PrefetchEntryArt(e *library.EntryView) {
	if e == nil || e.Media == nil {
		return
	}
	urls := []string{e.Media.CoverImage.ExtraLarge, e.Media.CoverImage.Large, e.Media.BannerImage, e.Images.Fanart, e.Images.Banner, e.Images.Poster, e.Images.Clearlogo}
	for _, ep := range e.Episodes {
		urls = append(urls, ep.Image)
	}
	a.Images.Prefetch(urls...)
}
