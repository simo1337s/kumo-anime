// Package downloads runs the episode download queue (ani-cli and extension
// streams). Finished files land in the library and are matched immediately.
package downloads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// saveMu orders the writes of an item (database row and UI event) with
	// its deletion, so a late update can't bring a removed item back.
	// Lock it before mu.
	saveMu sync.Mutex

	mu       sync.Mutex
	wake     *sync.Cond // signaled when pending grows
	items    map[string]*Item
	runs     map[string]*run // runs that haven't ended yet, by item id
	pending  []string        // queued item ids, oldest first (may hold ids since canceled or removed)
	restored []string        // downloads interrupted by the last shutdown, queued by Start

	// fetch downloads res into out (replaced in tests).
	fetch func(r *run, cfg config.Settings, res *Resolved, out string) error
}

// run is one attempt at downloading an item. Canceling or removing the item
// makes the run stale: it no longer touches the item from then on, but it
// stays in Manager.runs until it has stopped, so that the item is never
// downloaded twice at the same time.
type run struct {
	it     *Item
	ctx    context.Context
	cancel context.CancelFunc
	stale  bool // guarded by Manager.mu
}

func NewManager(s *config.Store, d *db.DB, hub *events.Hub, files *library.Store) *Manager {
	m := &Manager{
		settings:  s,
		db:        d,
		hub:       hub,
		files:     files,
		Resolvers: map[string]Resolver{},
		items:     map[string]*Item{},
		runs:      map[string]*run{},
	}
	m.wake = sync.NewCond(&m.mu)
	m.fetch = m.download
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
	m.mu.Lock()
	defer m.mu.Unlock()
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		var it Item
		if json.Unmarshal([]byte(raw), &it) != nil {
			continue
		}
		if active(it.Status) {
			it.Status, it.Progress, it.Speed, it.ETA = StatusQueued, 0, "", ""
			m.restored = append(m.restored, it.ID)
		}
		m.items[it.ID] = &it
	}
}

// Start resumes the downloads that were interrupted when Kumo last quit.
// Call it once the resolvers, and the extensions they rely on, are ready;
// new downloads don't wait for it.
func (m *Manager) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = append(m.pending, m.restored...)
	m.restored = nil
	m.wake.Broadcast()
}

func active(status string) bool {
	return status == StatusQueued || status == StatusResolving || status == StatusDownloading
}

// queue hands an item to the workers. The caller holds m.mu.
func (m *Manager) queue(id string) {
	m.pending = append(m.pending, id)
	m.wake.Signal()
}

// save publishes the current state of it to the UI, and stores it as well
// when persist is set. Nothing is written once the item has been removed.
func (m *Manager) save(it *Item, persist bool) {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.mu.Lock()
	if m.items[it.ID] != it {
		m.mu.Unlock()
		return
	}
	if persist {
		it.UpdatedAt = time.Now().Unix()
	}
	c := *it
	m.mu.Unlock()
	if persist {
		raw, _ := json.Marshal(&c)
		_, _ = m.db.Write(`INSERT INTO downloads(id, data, created_at) VALUES(?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET data = excluded.data`, c.ID, string(raw), c.CreatedAt)
	}
	m.hub.Publish(events.DownloadProgress, &c)
}

// update applies fn to the run's item and saves it, unless the run is stale.
func (m *Manager) update(r *run, fn func(it *Item)) {
	m.mu.Lock()
	if r.stale {
		m.mu.Unlock()
		return
	}
	fn(r.it)
	m.mu.Unlock()
	m.save(r.it, true)
}

// progress returns a func that records the run's progress (unless the run
// is stale) and publishes it at most every 700ms.
func (m *Manager) progress(r *run) func(fn func(it *Item)) {
	var last time.Time
	return func(fn func(it *Item)) {
		m.mu.Lock()
		if r.stale {
			m.mu.Unlock()
			return
		}
		fn(r.it)
		m.mu.Unlock()
		if time.Since(last) > 700*time.Millisecond {
			last = time.Now()
			m.save(r.it, false)
		}
	}
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
		if x.MediaID == it.MediaID && x.Episode == it.Episode && x.Mode == it.Mode && active(x.Status) {
			c := *x
			m.mu.Unlock()
			return &c, nil
		}
	}
	it.ID = uuid.NewString()
	it.Status = StatusQueued
	it.CreatedAt = time.Now().UnixNano() / int64(time.Millisecond)
	it.UpdatedAt = time.Now().Unix()
	p := &it
	m.items[it.ID] = p
	m.queue(it.ID)
	c := *p
	m.mu.Unlock()
	m.save(p, true)
	return &c, nil
}

func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	it := m.items[id]
	if it == nil || !active(it.Status) {
		m.mu.Unlock()
		return
	}
	it.Status, it.Speed, it.ETA = StatusCanceled, "", ""
	r := m.runs[id]
	if r != nil {
		r.stale = true
	}
	m.mu.Unlock()
	if r != nil {
		r.cancel()
	}
	m.save(it, true)
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
	it.Status, it.Error, it.Progress, it.Speed, it.ETA = StatusQueued, "", 0, "", ""
	// A canceled run that is still stopping requeues the item once it's done.
	if m.runs[id] == nil {
		m.queue(id)
	}
	m.mu.Unlock()
	m.save(it, true)
	return nil
}

// Remove deletes an item from the list (cancelling it first).
func (m *Manager) Remove(id string) {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.mu.Lock()
	delete(m.items, id)
	r := m.runs[id]
	if r != nil {
		r.stale = true
	}
	m.mu.Unlock()
	if r != nil {
		r.cancel()
	}
	_, _ = m.db.Write(`DELETE FROM downloads WHERE id = ?`, id)
	m.hub.Publish(events.DownloadProgress, map[string]any{"id": id, "removed": true})
}

// ClearFinished removes completed/failed/canceled items.
func (m *Manager) ClearFinished() {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
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
	for {
		m.execute(m.next())
	}
}

// next waits for a queued item and claims it.
func (m *Manager) next() *run {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		for len(m.pending) > 0 {
			id := m.pending[0]
			m.pending = m.pending[1:]
			it := m.items[id]
			// Skip items canceled or removed since they were queued, and items
			// whose previous run is still stopping (it requeues them).
			if it == nil || it.Status != StatusQueued || m.runs[id] != nil {
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			r := &run{it: it, ctx: ctx, cancel: cancel}
			m.runs[id] = r
			return r
		}
		m.wake.Wait()
	}
}

func (m *Manager) execute(r *run) {
	err := m.process(r)
	r.cancel()

	m.mu.Lock()
	it := r.it
	delete(m.runs, it.ID)
	if r.stale {
		// Canceled or removed meanwhile. If it has been retried since, it was
		// waiting for this run to stop.
		if m.items[it.ID] == it && it.Status == StatusQueued {
			m.queue(it.ID)
		}
		m.mu.Unlock()
		return
	}
	if err != nil {
		it.Status, it.Error = StatusFailed, err.Error()
	} else {
		it.Status, it.Progress = StatusCompleted, 1
	}
	it.Speed, it.ETA = "", ""
	done := *it
	m.mu.Unlock()
	m.save(it, true)
	switch done.Status {
	case StatusCompleted:
		m.hub.Success(fmt.Sprintf("Downloaded %s — episode %d", done.AnimeTitle, done.Episode))
	case StatusFailed:
		m.hub.Error(fmt.Sprintf("Download failed (%s ep %d): %s", done.AnimeTitle, done.Episode, done.Error))
	}
}

func (m *Manager) process(r *run) error {
	ctx := r.ctx
	cfg := m.settings.Get()
	m.update(r, func(it *Item) { it.Status = StatusResolving })
	m.mu.Lock()
	it := *r.it // the fields read below never change
	m.mu.Unlock()

	res := &Resolved{URL: it.URL, Referrer: it.Referrer, Headers: it.Headers, SubURL: it.SubURL}
	if res.URL == "" {
		resolve := m.Resolvers[it.Source]
		if resolve == nil {
			return fmt.Errorf("unknown download source %q", it.Source)
		}
		got, err := resolve(ctx, &it)
		if err != nil {
			return err
		}
		if got == nil || got.URL == "" {
			return errors.New("no stream found for this episode")
		}
		res = got
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	dir, err := m.folderFor(cfg, it.MediaID, it.AnimeTitle)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tag := strings.ToUpper(util.FirstNonEmpty(it.Mode, "sub"))
	name := util.SanitizeFilename(fmt.Sprintf("%s - %02d [%s]", it.AnimeTitle, it.Episode, tag))
	out := filepath.Join(dir, name+".mp4")

	// Download into a hidden folder next to the destination (the library
	// scanner skips it) and move only finished files into place: a failed or
	// canceled download leaves nothing behind (half-written video, yt-dlp
	// .part/.ytdl/fragment files, subtitles without their video).
	work := filepath.Join(dir, ".kumo-download-"+it.ID)
	_ = os.RemoveAll(work) // left over by a crash
	if err := os.Mkdir(work, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(work)
	m.update(r, func(it *Item) { it.Status, it.Output = StatusDownloading, out })

	var subs []string
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
			sub := name + ".eng" + ext
			if os.WriteFile(filepath.Join(work, sub), b, 0o644) == nil {
				subs = append(subs, sub)
			}
		}
	}

	tmp := filepath.Join(work, name+".mp4")
	err = m.fetch(r, cfg, res, tmp)
	if ctx.Err() != nil {
		return errors.New("canceled")
	}
	if err != nil {
		return err
	}
	st, err := os.Stat(tmp)
	if err != nil {
		return errors.New("the download finished without writing a video file")
	}
	if err := os.Rename(tmp, out); err != nil {
		return err
	}
	for _, sub := range subs {
		_ = os.Rename(filepath.Join(work, sub), filepath.Join(dir, sub))
	}

	// Register the file as matched so it appears instantly.
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
	return nil
}

// download fetches res into out with yt-dlp or ffmpeg, as configured.
func (m *Manager) download(r *run, cfg config.Settings, res *Resolved, out string) error {
	downloader := cfg.AniCli.Downloader
	_, hasYtdlp := util.LookPath("yt-dlp")
	if downloader == "" || downloader == "auto" {
		if hasYtdlp {
			downloader = "yt-dlp"
		} else {
			downloader = "ffmpeg"
		}
	}
	if downloader == "yt-dlp" && hasYtdlp {
		return m.ytdlp(r, res, out)
	}
	return m.ffmpeg(r, res, out, cfg.Transcode.FfmpegPath, cfg.Transcode.FfprobePath)
}

// folderFor is where episodes of an anime are downloaded: the folder that
// already holds its episodes (when it's inside the download/library
// folder), otherwise a new folder named after the anime — never loose in
// the library folder itself.
func (m *Manager) folderFor(cfg config.Settings, mediaID int, animeTitle string) (string, error) {
	baseDir := config.ExpandHome(util.FirstNonEmpty(cfg.AniCli.DownloadDir, cfg.Library.Dir))
	if baseDir == "" {
		return "", errors.New("no download directory configured")
	}
	baseDir = filepath.Clean(baseDir)
	if mediaID > 0 && m.files != nil {
		if existing, _ := m.files.ByMedia(mediaID); len(existing) > 0 {
			counts := map[string]int{}
			best := ""
			for _, f := range existing {
				d := filepath.Clean(f.Dir)
				// Only a folder of its own below the base folder.
				if d == baseDir || !util.IsSubPath(baseDir, d) || strings.HasPrefix(filepath.Base(d), ".") {
					continue
				}
				counts[d]++
				if counts[d] > counts[best] {
					best = d
				}
			}
			if best != "" {
				if st, err := os.Stat(best); err == nil && st.IsDir() {
					return best, nil
				}
			}
		}
	}
	name := strings.TrimSpace(animeTitle)
	if name == "" {
		name = fmt.Sprintf("AniList %d", mediaID)
	}
	return filepath.Join(baseDir, util.SanitizeFilename(name)), nil
}

var reYtdlp = regexp.MustCompile(`KUMO\|\s*([\d.]+)%\|([^|]*)\|(.*)$`)

func (m *Manager) ytdlp(r *run, res *Resolved, out string) error {
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
	cmd := exec.CommandContext(r.ctx, "yt-dlp", args...)
	// An interrupt lets yt-dlp stop the ffmpeg it may have started; it gets
	// killed if it's still running 10s later.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	progress := m.progress(r)
	stdout := &lineWriter{fn: func(line string) {
		mm := reYtdlp.FindStringSubmatch(line)
		if mm == nil {
			return
		}
		p, _ := strconv.ParseFloat(mm[1], 64)
		progress(func(it *Item) {
			it.Progress = p / 100
			it.Speed = strings.TrimSpace(mm[2])
			it.ETA = strings.TrimSpace(mm[3])
		})
	}}
	var lastErr string
	stderr := &lineWriter{fn: func(line string) {
		if strings.Contains(line, "ERROR") {
			lastErr = strings.TrimSpace(line)
		}
	}}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	// Run returns once both writers have consumed all the output, so lastErr
	// is final (and no longer written) from here on.
	stdout.flush()
	stderr.flush()
	if err != nil {
		if lastErr != "" {
			return errors.New(lastErr)
		}
		return fmt.Errorf("yt-dlp: %w", err)
	}
	return nil
}

func (m *Manager) ffmpeg(r *run, res *Resolved, out, ffmpegPath, ffprobePath string) error {
	ctx := r.ctx
	if _, ok := util.LookPath(ffmpegPath); !ok {
		return fmt.Errorf("neither yt-dlp nor ffmpeg is installed (%s, or %s)", util.InstallHint("yt-dlp"), util.InstallHint("ffmpeg"))
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

	args := []string{"-y", "-nostdin", "-hide_banner", "-loglevel", "error", "-progress", "pipe:1"}
	// ffmpeg >= 7.1 refuses HLS segments with unusual extensions unless told
	// otherwise (ani-cli passes the same flag); older versions don't know it.
	if hlsExtensionPicky(ffmpegPath) {
		args = append(args, "-extension_picky", "0")
	}
	args = append(args, headerArgs()...)
	args = append(args, "-i", res.URL, "-map", "0:v?", "-map", "0:a?", "-c", "copy", "-bsf:a", "aac_adtstoasc", "-movflags", "+faststart", out)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.WaitDelay = 10 * time.Second
	progress := m.progress(r)
	stdout := &lineWriter{fn: func(line string) {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return
		}
		switch k {
		case "out_time_us", "out_time_ms":
			us, _ := strconv.ParseFloat(v, 64)
			if duration > 0 {
				progress(func(it *Item) { it.Progress = min(us/1e6/duration, 0.99) })
			}
		case "speed":
			progress(func(it *Item) { it.Speed = strings.TrimSpace(v) })
		}
	}}
	var lastErr string
	stderr := &lineWriter{fn: func(line string) {
		if line = strings.TrimSpace(line); line != "" {
			lastErr = line
		}
	}}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	// Run returns once both writers have consumed all the output.
	stdout.flush()
	stderr.flush()
	if err != nil {
		if lastErr == "" {
			lastErr = err.Error()
		}
		return fmt.Errorf("ffmpeg: %s", lastErr)
	}
	return nil
}

var (
	pickyOnce sync.Once
	pickyOK   bool
)

func hlsExtensionPicky(ffmpegPath string) bool {
	pickyOnce.Do(func() {
		out, _ := exec.Command(ffmpegPath, "-hide_banner", "-h", "demuxer=hls").CombinedOutput()
		pickyOK = strings.Contains(string(out), "extension_picky")
	})
	return pickyOK
}

// maxLine caps the length of a line kept by lineWriter.
const maxLine = 64 << 10

// lineWriter hands every non-empty line written to it to fn, without the
// line break (\r ends a line too). It accepts everything, so a command's
// output pipe can never fill up and block it; lines over maxLine are cut.
type lineWriter struct {
	fn   func(line string)
	buf  []byte
	long bool // the line in buf was cut: drop the rest of it
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexAny(p, "\r\n")
		if i < 0 {
			w.add(p)
			break
		}
		w.add(p[:i])
		w.flush()
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) add(b []byte) {
	if w.long {
		return
	}
	if room := maxLine - len(w.buf); len(b) > room {
		b, w.long = b[:room], true
	}
	w.buf = append(w.buf, b...)
}

// flush ends the current line (also used for a last line without a break).
func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
	}
	w.buf, w.long = w.buf[:0], false
}
