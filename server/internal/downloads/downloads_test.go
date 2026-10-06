package downloads

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/library"
)

const testURL = "http://127.0.0.1:9/ep.m3u8" // never fetched: every downloader is faked

func newTestManager(t *testing.T, mutate func(*config.Settings)) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Get()
	cfg.Library.Dir = filepath.Join(dir, "library")
	cfg.AniCli.Downloader = "ffmpeg"
	cfg.Transcode.FfmpegPath = filepath.Join(dir, "missing-ffmpeg")
	cfg.Transcode.FfprobePath = filepath.Join(dir, "missing-ffprobe")
	if mutate != nil {
		mutate(&cfg)
	}
	if _, err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return NewManager(s, d, events.NewHub(), library.NewStore(d)), cfg.Library.Dir
}

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func enqueue(t *testing.T, m *Manager, title string) *Item {
	t.Helper()
	it, err := m.Enqueue(Item{MediaID: 1, Episode: 1, AnimeTitle: title, Mode: "sub", Source: "stream", URL: testURL})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func find(m *Manager, id string) *Item {
	for _, it := range m.List() {
		if it.ID == id {
			return it
		}
	}
	return nil
}

func waitFinal(t *testing.T, m *Manager, id string, timeout time.Duration) Item {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if it := find(m, id); it != nil && (it.Status == StatusCompleted || it.Status == StatusFailed || it.Status == StatusCanceled) {
			return *it
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("download %s did not finish within %s", id, timeout)
	return Item{}
}

// waitIdle waits until no run is left, canceled ones included.
func waitIdle(t *testing.T, m *Manager) {
	t.Helper()
	waitIdleWithin(t, m, 10*time.Second)
}

func waitIdleWithin(t *testing.T, m *Manager, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := len(m.runs)
		m.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("runs did not stop within %s", timeout)
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s was never written", path)
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// dirEntries lists a directory (nil if it doesn't exist).
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func dbRows(t *testing.T, m *Manager, id string) int {
	t.Helper()
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM downloads WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type event struct {
	Type    string `json:"type"`
	Payload struct {
		ID       string  `json:"id"`
		Status   string  `json:"status"`
		Progress float64 `json:"progress"`
		Speed    string  `json:"speed"`
		Removed  bool    `json:"removed"`
		Cleared  bool    `json:"cleared"`
	} `json:"payload"`
}

type recorder struct {
	mu     sync.Mutex
	events []event
}

func record(t *testing.T, hub *events.Hub) *recorder {
	ch, unsubscribe := hub.Subscribe()
	rec := &recorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for raw := range ch {
			var ev event
			_ = json.Unmarshal(raw, &ev)
			rec.mu.Lock()
			rec.events = append(rec.events, ev)
			rec.mu.Unlock()
		}
	}()
	t.Cleanup(func() { unsubscribe(); <-done })
	return rec
}

func (r *recorder) downloadEvents() []event {
	time.Sleep(50 * time.Millisecond) // let the collector catch up
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []event
	for _, ev := range r.events {
		if ev.Type == events.DownloadProgress {
			out = append(out, ev)
		}
	}
	return out
}

// A stream with lots of errors makes ffmpeg write megabytes to stderr: the
// download must not stall once the pipe is full.
func TestFfmpegStderrFloodDoesNotStall(t *testing.T) {
	bin := t.TempDir()
	ffmpeg := writeScript(t, bin, "ffmpeg", `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
for last; do :; done
yes 'Error while decoding stream #0:0: Invalid data found when processing input' | head -n 30000 >&2
echo 'out_time_us=5000000'
echo 'speed=2.5x'
echo 'progress=end'
printf 'video' > "$last"
`)
	ffprobe := writeScript(t, bin, "ffprobe", "echo 10.0\n")
	m, lib := newTestManager(t, func(c *config.Settings) {
		c.Transcode.FfmpegPath, c.Transcode.FfprobePath = ffmpeg, ffprobe
	})
	rec := record(t, m.hub)
	it := enqueue(t, m, "Flood")
	got := waitFinal(t, m, it.ID, 15*time.Second)
	if got.Status != StatusCompleted {
		t.Fatalf("status = %s (%s), want completed", got.Status, got.Error)
	}
	want := filepath.Join(lib, "Flood", "Flood - 01 [SUB].mp4")
	if got.Output != want {
		t.Fatalf("output = %q, want %q", got.Output, want)
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != "video" {
		t.Fatalf("downloaded file = %q, %v", b, err)
	}
	if names := dirEntries(t, filepath.Dir(want)); len(names) != 1 {
		t.Fatalf("anime folder holds %v, want only the video", names)
	}
	if f, err := m.files.Get(want); err != nil || f.MediaID != 1 || f.Episode != 1 {
		t.Fatalf("library entry = %+v, %v", f, err)
	}
	var sawProgress bool
	for _, ev := range rec.downloadEvents() {
		sawProgress = sawProgress || (ev.Payload.ID == it.ID && ev.Payload.Status == StatusDownloading && ev.Payload.Progress == 0.5)
	}
	if !sawProgress {
		t.Error("no progress event at 50%")
	}
}

// The error shown is ffmpeg's last stderr line, even after megabytes of
// noise, and the half-written file doesn't stay in the library.
func TestFfmpegFailureReportsLastErrorAndCleansUp(t *testing.T) {
	ffmpeg := writeScript(t, t.TempDir(), "ffmpeg", `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
for last; do :; done
printf 'half a video' > "$last"
yes 'Error while decoding stream #0:0: Invalid data found when processing input' | head -n 30000 >&2
echo '[https @ 0x5599] HTTP error 403 Forbidden' >&2
exit 1
`)
	m, lib := newTestManager(t, func(c *config.Settings) { c.Transcode.FfmpegPath = ffmpeg })
	it := enqueue(t, m, "Broken")
	got := waitFinal(t, m, it.ID, 15*time.Second)
	if got.Status != StatusFailed || got.Error != "ffmpeg: [https @ 0x5599] HTTP error 403 Forbidden" {
		t.Fatalf("got %s (%q), want failed with the last stderr line", got.Status, got.Error)
	}
	if names := dirEntries(t, filepath.Join(lib, "Broken")); len(names) != 0 {
		t.Fatalf("failed download left %v behind", names)
	}
}

func TestYtdlpStderrFlood(t *testing.T) {
	const head = `out=
while [ $# -gt 0 ]; do
	case "$1" in -o) out="$2"; shift ;; esac
	shift
done
yes 'WARNING: [generic] Falling back on generic information extractor' | head -n 30000 >&2
echo 'KUMO| 42.0%|1.50MiB/s|00:07'
printf 'partial' > "$out.part"
printf 'fragment' > "$out.part-Frag1"
`
	t.Run("success", func(t *testing.T) {
		bin := t.TempDir()
		writeScript(t, bin, "yt-dlp", head+`printf 'video' > "$out"
`)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		m, lib := newTestManager(t, func(c *config.Settings) { c.AniCli.Downloader = "yt-dlp" })
		rec := record(t, m.hub)
		it := enqueue(t, m, "Chatty")
		got := waitFinal(t, m, it.ID, 15*time.Second)
		if got.Status != StatusCompleted {
			t.Fatalf("status = %s (%s), want completed", got.Status, got.Error)
		}
		if names := dirEntries(t, filepath.Join(lib, "Chatty")); strings.Join(names, ",") != "Chatty - 01 [SUB].mp4" {
			t.Fatalf("anime folder holds %v, want only the video", names)
		}
		var sawProgress bool
		for _, ev := range rec.downloadEvents() {
			sawProgress = sawProgress || (ev.Payload.ID == it.ID && ev.Payload.Progress == 0.42 && ev.Payload.Speed == "1.50MiB/s")
		}
		if !sawProgress {
			t.Error("no progress event with 42% at 1.50MiB/s")
		}
	})
	t.Run("failure", func(t *testing.T) {
		bin := t.TempDir()
		writeScript(t, bin, "yt-dlp", head+`echo 'ERROR: [generic] Unable to download webpage: HTTP Error 404: Not Found' >&2
echo 'trailing noise' >&2
exit 1
`)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		m, lib := newTestManager(t, func(c *config.Settings) { c.AniCli.Downloader = "yt-dlp" })
		it := enqueue(t, m, "Gone")
		got := waitFinal(t, m, it.ID, 15*time.Second)
		if got.Status != StatusFailed || got.Error != "ERROR: [generic] Unable to download webpage: HTTP Error 404: Not Found" {
			t.Fatalf("got %s (%q), want failed with the ERROR line", got.Status, got.Error)
		}
		if names := dirEntries(t, filepath.Join(lib, "Gone")); len(names) != 0 {
			t.Fatalf("failed download left %v behind (.part files?)", names)
		}
	})
}

// Canceling stops the real downloader process right away (yt-dlp through an
// interrupt, well before it would be killed) and leaves no partial files.
func TestCancelStopsDownloader(t *testing.T) {
	for _, tc := range []struct{ name, partial, script string }{
		{"ffmpeg", ".mp4", `case "$*" in *"-h demuxer=hls"*) exit 0 ;; esac
for last; do :; done
printf 'half a video' > "$last"
exec sleep 30
`},
		{"yt-dlp", ".mp4.part", `out=
while [ $# -gt 0 ]; do
	case "$1" in -o) out="$2"; shift ;; esac
	shift
done
trap 'echo "ERROR: Interrupted by user" >&2; exit 1' INT
printf 'partial' > "$out.part"
while :; do sleep 0.05; done
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			script := writeScript(t, bin, tc.name, tc.script)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			m, lib := newTestManager(t, func(c *config.Settings) {
				c.AniCli.Downloader, c.Transcode.FfmpegPath = tc.name, script
			})
			it := enqueue(t, m, "Halt")
			waitFile(t, filepath.Join(lib, "Halt", ".kumo-download-"+it.ID, "Halt - 01 [SUB]"+tc.partial))
			m.Cancel(it.ID)
			waitIdleWithin(t, m, 5*time.Second)
			if got := find(m, it.ID); got.Status != StatusCanceled {
				t.Fatalf("status = %s (%s), want canceled", got.Status, got.Error)
			}
			if names := dirEntries(t, filepath.Join(lib, "Halt")); len(names) != 0 {
				t.Fatalf("canceled download left %v behind", names)
			}
		})
	}
}

// Removing (or clearing) a download while its run is still stopping must not
// let that run write the item back to the database or the UI.
func TestRemoveDuringRunDoesNotResurrect(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(m *Manager, id string)
		marker func(ev event, id string) bool
	}{
		{"remove", func(m *Manager, id string) { m.Remove(id) }, func(ev event, id string) bool { return ev.Payload.Removed && ev.Payload.ID == id }},
		{"cancel and clear", func(m *Manager, id string) { m.Cancel(id); m.ClearFinished() }, func(ev event, _ string) bool { return ev.Payload.Cleared }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, lib := newTestManager(t, nil)
			rec := record(t, m.hub)
			started, release := make(chan struct{}, 1), make(chan struct{})
			m.fetch = func(r *run, _ config.Settings, _ *Resolved, out string) error {
				_ = os.WriteFile(out+".part", []byte("partial"), 0o644)
				started <- struct{}{}
				<-r.ctx.Done()
				<-release // slow to stop
				return r.ctx.Err()
			}
			it := enqueue(t, m, "Gone")
			waitSignal(t, started, "the download to start")
			tc.remove(m, it.ID)
			close(release)
			waitIdle(t, m)

			if find(m, it.ID) != nil {
				t.Fatal("item is listed again")
			}
			if n := dbRows(t, m, it.ID); n != 0 {
				t.Fatalf("database still has %d row(s) for the item", n)
			}
			evs := rec.downloadEvents()
			marker := -1
			for i, ev := range evs {
				if tc.marker(ev, it.ID) {
					marker = i
				}
			}
			if marker < 0 {
				t.Fatal("no removal event")
			}
			for _, ev := range evs[marker+1:] {
				if ev.Payload.ID == it.ID {
					t.Fatalf("item published after its removal: %+v", ev.Payload)
				}
			}
			if names := dirEntries(t, filepath.Join(lib, "Gone")); len(names) != 0 {
				t.Fatalf("canceled download left %v behind", names)
			}
			restarted := NewManager(m.settings, m.db, events.NewHub(), m.files)
			if find(restarted, it.ID) != nil {
				t.Fatal("item came back after a restart")
			}
		})
	}
}

func TestCancelRunningDownload(t *testing.T) {
	m, _ := newTestManager(t, nil)
	started := make(chan struct{}, 1)
	m.fetch = func(r *run, _ config.Settings, _ *Resolved, _ string) error {
		started <- struct{}{}
		<-r.ctx.Done()
		return r.ctx.Err()
	}
	it := enqueue(t, m, "Stop")
	waitSignal(t, started, "the download to start")
	m.Cancel(it.ID)
	waitIdle(t, m)
	got := find(m, it.ID)
	if got == nil || got.Status != StatusCanceled || got.Error != "" {
		t.Fatalf("got %+v, want canceled without error", got)
	}
	var raw string
	if err := m.db.QueryRow(`SELECT data FROM downloads WHERE id = ?`, it.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored Item
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || stored.Status != StatusCanceled {
		t.Fatalf("stored status = %q (%v), want canceled", stored.Status, err)
	}
}

// Retrying right after a cancel must run the download again once the
// canceled run has stopped, and never both at once.
func TestCancelThenRetryWaitsForOldRun(t *testing.T) {
	m, _ := newTestManager(t, nil)
	var calls, running, maxRunning atomic.Int32
	started, release := make(chan struct{}, 4), make(chan struct{})
	m.fetch = func(r *run, _ config.Settings, _ *Resolved, out string) error {
		n := calls.Add(1)
		cur := running.Add(1)
		defer running.Add(-1)
		for old := maxRunning.Load(); cur > old && !maxRunning.CompareAndSwap(old, cur); old = maxRunning.Load() {
		}
		started <- struct{}{}
		if n == 1 {
			<-r.ctx.Done()
			<-release // slow to stop
			return r.ctx.Err()
		}
		return os.WriteFile(out, []byte("video"), 0o644)
	}
	it := enqueue(t, m, "Again")
	waitSignal(t, started, "the first run")
	m.Cancel(it.ID)
	if err := m.Retry(it.ID); err != nil {
		t.Fatal(err)
	}
	if got := find(m, it.ID); got.Status != StatusQueued {
		t.Fatalf("status after retry = %s, want queued", got.Status)
	}
	select {
	case <-started:
		t.Fatal("the retry started while the canceled run was still stopping")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	got := waitFinal(t, m, it.ID, 10*time.Second)
	if got.Status != StatusCompleted || got.Error != "" {
		t.Fatalf("got %s (%q), want completed", got.Status, got.Error)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("downloader ran %d times, want 2", n)
	}
	if n := maxRunning.Load(); n != 1 {
		t.Fatalf("%d runs overlapped", n)
	}
	if _, err := os.Stat(got.Output); err != nil {
		t.Fatal(err)
	}
}

// Downloads interrupted by a shutdown are queued again, but only start once
// Start says the resolvers are wired up.
func TestRestoredDownloadsWaitForStart(t *testing.T) {
	old, _ := newTestManager(t, nil)
	raw, _ := json.Marshal(Item{ID: "restored", MediaID: 7, Episode: 3, AnimeTitle: "Resume", Mode: "sub", Source: "stream",
		URL: testURL, Status: StatusDownloading, Progress: 0.4, CreatedAt: 1})
	if _, err := old.db.Write(`INSERT INTO downloads(id, data, created_at) VALUES(?, ?, 1)`, "restored", string(raw)); err != nil {
		t.Fatal(err)
	}
	m := NewManager(old.settings, old.db, old.hub, old.files)
	var calls atomic.Int32
	m.fetch = func(_ *run, _ config.Settings, _ *Resolved, out string) error {
		calls.Add(1)
		return os.WriteFile(out, []byte("video"), 0o644)
	}
	time.Sleep(100 * time.Millisecond)
	if got := find(m, "restored"); got == nil || got.Status != StatusQueued || got.Progress != 0 || calls.Load() != 0 {
		t.Fatalf("before Start: %+v (downloader calls: %d), want queued and untouched", got, calls.Load())
	}
	m.Start()
	if got := waitFinal(t, m, "restored", 10*time.Second); got.Status != StatusCompleted {
		t.Fatalf("got %s (%q), want completed", got.Status, got.Error)
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(l string) { lines = append(lines, l) }}
	long := strings.Repeat("x", maxLine+10)
	for _, chunk := range []string{"out_time", "_us=1\nspeed=", "2x\r\n\nfra", "me=3\r", long[:100], long[100:] + "\n", "tail"} {
		if n, err := w.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	w.flush()
	want := []string{"out_time_us=1", "speed=2x", "frame=3", long[:maxLine], "tail"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %.80q, want %.80q", lines, want)
	}
}
