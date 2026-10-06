// Package downloads runs the episode download queue (ani-cli and extension
// streams). Finished files land in the library and are matched immediately.
package downloads

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/library"
	"github.com/simo1337s/animetest/server/internal/util"
)

const (
	StatusQueued      = "queued"
	StatusResolving   = "resolving"
	StatusDownloading = "downloading"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusCanceled    = "canceled"
)

type Item struct {
	ID         string            `json:"id"`
	MediaID    int               `json:"mediaId"`
	Episode    int               `json:"episode"`
	AnimeTitle string            `json:"animeTitle"`
	Image      string            `json:"image"`
	Mode       string            `json:"mode"`   // sub | dub
	Source     string            `json:"source"` // anicli | stream
	Provider   string            `json:"provider"`
	Status     string            `json:"status"`
	Progress   float64           `json:"progress"` // 0-1
	Speed      string            `json:"speed"`
	ETA        string            `json:"eta"`
	Error      string            `json:"error,omitempty"`
	Output     string            `json:"output,omitempty"`
	Quality    string            `json:"quality,omitempty"`
	URL        string            `json:"url,omitempty"`
	Referrer   string            `json:"referrer,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	SubURL     string            `json:"subUrl,omitempty"`
	CreatedAt  int64             `json:"createdAt"`
	UpdatedAt  int64             `json:"updatedAt"`
}

// Resolved is what a resolver returns: a direct URL to fetch.
type Resolved struct {
	URL      string
	Referrer string
	Headers  map[string]string
	SubURL   string
}

// Resolver turns a queued item into a downloadable URL (e.g. via ani-cli).
type Resolver func(ctx context.Context, it *Item) (*Resolved, error)

type Manager struct {
	settings *config.Store
	db       *db.DB
	hub      *events.Hub
	files    *library.Store

	Resolvers map[string]Resolver

	mu      sync.Mutex
	items   map[string]*Item
	cancels map[string]context.CancelFunc
	queue   chan string
}

func NewManager(s *config.Store, d *db.DB, hub *events.Hub, files *library.Store) *Manager {
	m := &Manager{
		settings:  s,
		db:        d,
		hub:       hub,
		files:     files,
		Resolvers: map[string]Resolver{},
		items:     map[string]*Item{},
		cancels:   map[string]context.CancelFunc{},
		queue:     make(chan string, 1024),
	}
	m.load()
	for i := 0; i < 2; i++ {
		go m.worker()
	}
	return m
}

func (m *Manager) load() {
	rows, err := m.db.Query(`SELECT data FROM downloads ORDER BY created_at`)
	if err != nil {
		return
	}
	defer rows.Close()
	var requeue []string
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		var it Item
		if json.Unmarshal([]byte(raw), &it) != nil {
			continue
		}
		switch it.Status {
		case StatusQueued, StatusResolving, StatusDownloading:
			it.Status, it.Progress, it.Speed, it.ETA = StatusQueued, 0, "", ""
			requeue = append(requeue, it.ID)
		}
		m.items[it.ID] = &it
	}
	for _, id := range requeue {
		m.queue <- id
	}
}

func (m *Manager) persist(it *Item) {
	it.UpdatedAt = time.Now().Unix()
	raw, _ := json.Marshal(it)
	_, _ = m.db.Write(`INSERT INTO downloads(id, data, created_at) VALUES(?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET data = excluded.data`, it.ID, string(raw), it.CreatedAt)
}

func (m *Manager) publish(it *Item) {
	c := *it
	m.hub.Publish(events.DownloadProgress, &c)
}

// List returns all items, newest first.
func (m *Manager) List() []*Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Item, 0, len(m.items))
	for _, it := range m.items {
		c := *it
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// Enqueue adds an item to the queue (skipping duplicates still in progress).
func (m *Manager) Enqueue(it Item) (*Item, error) {
	if _, ok := m.Resolvers[it.Source]; !ok && it.URL == "" {
		return nil, fmt.Errorf("unknown download source %q", it.Source)
	}
	m.mu.Lock()
	for _, x := range m.items {
		if x.MediaID == it.MediaID && x.Episode == it.Episode && x.Mode == it.Mode &&
			(x.Status == StatusQueued || x.Status == StatusResolving || x.Status == StatusDownloading) {
			m.mu.Unlock()
			return x, nil
		}
	}
	it.ID = uuid.NewString()
	it.Status = StatusQueued
	it.CreatedAt = time.Now().UnixNano() / int64(time.Millisecond)
	m.items[it.ID] = &it
	m.mu.Unlock()
	m.persist(&it)
	m.publish(&it)
	m.queue <- it.ID
	return &it, nil
}

func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	it := m.items[id]
	cancel := m.cancels[id]
	if it != nil && (it.Status == StatusQueued || it.Status == StatusResolving || it.Status == StatusDownloading) {
		it.Status = StatusCanceled
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if it != nil {
		m.persist(it)
		m.publish(it)
	}
}

func (m *Manager) Retry(id string) error {
	m.mu.Lock()
	it := m.items[id]
	if it == nil {
		m.mu.Unlock()
		return errors.New("not found")
	}
	if it.Status != StatusFailed && it.Status != StatusCanceled {
		m.mu.Unlock()
		return nil
	}
	it.Status, it.Error, it.Progress = StatusQueued, "", 0
	m.mu.Unlock()
	m.persist(it)
	m.publish(it)
	m.queue <- id
	return nil
}

// Remove deletes an item from the list (cancelling it first).
func (m *Manager) Remove(id string) {
	m.Cancel(id)
	m.mu.Lock()
	delete(m.items, id)
	m.mu.Unlock()
	_, _ = m.db.Write(`DELETE FROM downloads WHERE id = ?`, id)
	m.hub.Publish(events.DownloadProgress, map[string]any{"id": id, "removed": true})
}

// ClearFinished removes completed/failed/canceled items.
func (m *Manager) ClearFinished() {
	m.mu.Lock()
	var ids []string
	for id, it := range m.items {
		if it.Status == StatusCompleted || it.Status == StatusFailed || it.Status == StatusCanceled {
			ids = append(ids, id)
			delete(m.items, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_, _ = m.db.Write(`DELETE FROM downloads WHERE id = ?`, id)
	}
	m.hub.Publish(events.DownloadProgress, map[string]any{"cleared": true})
}

func (m *Manager) worker() {
	for id := range m.queue {
		m.mu.Lock()
		it := m.items[id]
		if it == nil || it.Status != StatusQueued {
			m.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.cancels[id] = cancel
		m.mu.Unlock()

		err := m.process(ctx, it)

		m.mu.Lock()
		delete(m.cancels, id)
		if it.Status == StatusCanceled {
			// keep
		} else if err != nil {
			it.Status, it.Error = StatusFailed, err.Error()
		} else {
			it.Status, it.Progress, it.Speed, it.ETA = StatusCompleted, 1, "", ""
		}
		m.mu.Unlock()
		cancel()
		m.persist(it)
		m.publish(it)
		switch it.Status {
		case StatusCompleted:
			m.hub.Success(fmt.Sprintf("Downloaded %s — episode %d", it.AnimeTitle, it.Episode))
		case StatusFailed:
			m.hub.Error(fmt.Sprintf("Download failed (%s ep %d): %s", it.AnimeTitle, it.Episode, it.Error))
		}
	}
}

func (m *Manager) setStatus(it *Item, status string) {
	m.mu.Lock()
	if it.Status != StatusCanceled {
		it.Status = status
	}
	m.mu.Unlock()
	m.persist(it)
	m.publish(it)
}

func (m *Manager) process(ctx context.Context, it *Item) error {
	cfg := m.settings.Get()
	m.setStatus(it, StatusResolving)

	res := &Resolved{URL: it.URL, Referrer: it.Referrer, Headers: it.Headers, SubURL: it.SubURL}
	if res.URL == "" {
		resolver := m.Resolvers[it.Source]
		r, err := resolver(ctx, it)
		if err != nil {
			return err
		}
		res = r
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	baseDir := config.ExpandHome(util.FirstNonEmpty(cfg.AniCli.DownloadDir, cfg.Library.Dir))
	if baseDir == "" {
		return errors.New("no download directory configured")
	}
	dir := filepath.Join(baseDir, util.SanitizeFilename(it.AnimeTitle))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tag := strings.ToUpper(util.FirstNonEmpty(it.Mode, "sub"))
	name := util.SanitizeFilename(fmt.Sprintf("%s - %02d [%s]", it.AnimeTitle, it.Episode, tag))
	out := filepath.Join(dir, name+".mp4")
	it.Output = out
	m.setStatus(it, StatusDownloading)

	if res.SubURL != "" {
		ext := strings.ToLower(filepath.Ext(strings.Split(res.SubURL, "?")[0]))
		if ext == "" || len(ext) > 5 {
			ext = ".vtt"
		}
		headers := map[string]string{}
		if res.Referrer != "" {
			headers["Referer"] = res.Referrer
		}
		if b, err := util.GetBytes(ctx, res.SubURL, headers); err == nil {
			_ = os.WriteFile(filepath.Join(dir, name+".eng"+ext), b, 0o644)
		}
	}

	downloader := cfg.AniCli.Downloader
	_, hasYtdlp := util.LookPath("yt-dlp")
	if downloader == "" || downloader == "auto" {
		if hasYtdlp {
			downloader = "yt-dlp"
		} else {
			downloader = "ffmpeg"
		}
	}
	var err error
	if downloader == "yt-dlp" && hasYtdlp {
		err = m.ytdlp(ctx, it, res, out)
	} else {
		err = m.ffmpeg(ctx, it, res, out, cfg.Transcode.FfmpegPath, cfg.Transcode.FfprobePath)
	}
	if err != nil {
		_ = os.Remove(out)
		if ctx.Err() != nil {
			return errors.New("canceled")
		}
		return err
	}

	// Register the file as matched so it appears instantly.
	if st, err := os.Stat(out); err == nil {
		roots := cfg.LibraryDirs()
		f := &library.LocalFile{
			Path: out, Dir: dir, Name: filepath.Base(out), Size: st.Size(), ModTime: st.ModTime().Unix(),
			Parsed: library.Parse(out, roots), MediaID: it.MediaID, Episode: it.Episode, AiredEpisode: it.Episode,
			Kind: "main", Locked: true, MatchScore: 1,
		}
		if err := m.files.Save(f); err != nil {
			log.Printf("register download: %v", err)
		}
		m.hub.Publish(events.LibraryUpdated, nil)
	}
	return nil
}

var reYtdlp = regexp.MustCompile(`KUMO\|\s*([\d.]+)%\|([^|]*)\|(.*)$`)

func (m *Manager) ytdlp(ctx context.Context, it *Item, res *Resolved, out string) error {
	args := []string{
		"--newline", "--no-colors", "--no-playlist", "--no-mtime",
		"-N", "8", "--fragment-retries", "20", "--retries", "10",
		"--progress-template", "download:KUMO|%(progress._percent_str)s|%(progress._speed_str)s|%(progress._eta_str)s",
		"--merge-output-format", "mp4",
		"-o", strings.ReplaceAll(out, "%", "%%"),
	}
	if res.Referrer != "" {
		args = append(args, "--referer", res.Referrer)
	}
	for k, v := range res.Headers {
		if strings.EqualFold(k, "referer") && res.Referrer != "" {
			continue
		}
		args = append(args, "--add-header", k+":"+v)
	}
	args = append(args, res.URL)
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	var lastErr string
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if l := strings.TrimSpace(sc.Text()); strings.Contains(l, "ERROR") {
				lastErr = l
			}
		}
	}()
	sc := bufio.NewScanner(stdout)
	last := time.Time{}
	for sc.Scan() {
		mm := reYtdlp.FindStringSubmatch(sc.Text())
		if mm == nil {
			continue
		}
		p, _ := strconv.ParseFloat(mm[1], 64)
		m.mu.Lock()
		it.Progress = p / 100
		it.Speed = strings.TrimSpace(mm[2])
		it.ETA = strings.TrimSpace(mm[3])
		m.mu.Unlock()
		if time.Since(last) > 700*time.Millisecond {
			last = time.Now()
			m.publish(it)
		}
	}
	if err := cmd.Wait(); err != nil {
		if lastErr != "" {
			return errors.New(lastErr)
		}
		return fmt.Errorf("yt-dlp: %w", err)
	}
	return nil
}

func (m *Manager) ffmpeg(ctx context.Context, it *Item, res *Resolved, out, ffmpegPath, ffprobePath string) error {
	if _, ok := util.LookPath(ffmpegPath); !ok {
		return errors.New("neither yt-dlp nor ffmpeg is installed (sudo pacman -S yt-dlp ffmpeg)")
	}
	headerArgs := func() []string {
		var a []string
		var hdr strings.Builder
		for k, v := range res.Headers {
			if strings.EqualFold(k, "referer") || strings.EqualFold(k, "user-agent") {
				continue
			}
			hdr.WriteString(k + ": " + v + "\r\n")
		}
		if res.Referrer != "" {
			a = append(a, "-referer", res.Referrer)
		}
		a = append(a, "-user_agent", util.UserAgent)
		if hdr.Len() > 0 {
			a = append(a, "-headers", hdr.String())
		}
		return a
	}

	// Probe the duration to compute a percentage.
	var duration float64
	if p, ok := util.LookPath(ffprobePath); ok {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		args := append(headerArgs(), "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", res.URL)
		if b, err := exec.CommandContext(pctx, p, args...).Output(); err == nil {
			duration, _ = strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		}
		cancel()
	}

	args := []string{"-y", "-nostdin", "-hide_banner", "-loglevel", "error", "-progress", "pipe:1", "-extension_picky", "0"}
	args = append(args, headerArgs()...)
	args = append(args, "-i", res.URL, "-map", "0:v?", "-map", "0:a?", "-c", "copy", "-bsf:a", "aac_adtstoasc", "-movflags", "+faststart", out)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	errBuf := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(stderr, 64<<10))
		errBuf <- strings.TrimSpace(string(b))
	}()
	sc := bufio.NewScanner(stdout)
	last := time.Time{}
	for sc.Scan() {
		line := sc.Text()
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m.mu.Lock()
		switch k {
		case "out_time_us", "out_time_ms":
			us, _ := strconv.ParseFloat(v, 64)
			if duration > 0 {
				it.Progress = min(us/1e6/duration, 0.99)
			}
		case "speed":
			it.Speed = strings.TrimSpace(v)
		}
		m.mu.Unlock()
		if time.Since(last) > 700*time.Millisecond {
			last = time.Now()
			m.publish(it)
		}
	}
	if err := cmd.Wait(); err != nil {
		msg := <-errBuf
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ffmpeg: %s", lastLine(msg))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
