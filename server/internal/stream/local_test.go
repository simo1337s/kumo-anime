package stream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/library"
)

// newTestLocal returns a Local whose ffmpeg is the given script and a library
// video it may serve (already probed, so ffprobe is never run).
func newTestLocal(t *testing.T, ffmpeg string) (*Local, string) {
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
	cfg.Transcode.FfmpegPath = ffmpeg
	cfg.Transcode.FfprobePath = filepath.Join(dir, "missing-ffprobe")
	if _, err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(dir, "Show - 01.mkv")
	if err := os.WriteFile(video, []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := library.NewStore(d)
	if err := files.Save(&library.LocalFile{Path: video, Dir: dir, Name: filepath.Base(video), Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	d.SetCache("probe:"+video, Probe{
		Path: video, Container: "matroska,webm", Duration: 60, Size: int64(len("not really a video")),
		Video: []ProbeStream{{Index: 0, Type: "video", Codec: "h264"}},
		Audio: []ProbeStream{{Index: 1, Type: "audio", Codec: "aac", Channels: 2}},
	}, time.Hour)
	return NewLocal(s, files, d), video
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// serve runs ServeTranscode and fails the test if it doesn't return in time.
func serve(t *testing.T, l *Local, w http.ResponseWriter, video string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/local/transcode", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.ServeTranscode(w, r, video, 0, 0, "remux")
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		cancel() // kills ffmpeg so the goroutine can end
		<-done
		t.Fatal("ServeTranscode did not return within 15s")
	}
}

// A damaged file makes ffmpeg print an error per frame; playback must not
// freeze once the stderr pipe is full.
func TestServeTranscodeStderrFloodDoesNotStall(t *testing.T) {
	ffmpeg := writeScript(t, `yes '[h264 @ 0x55d0c0] error while decoding MB 12 34, bytestream -5' | head -c 2000000 >&2
printf 'fragmented-mp4'
`)
	l, video := newTestLocal(t, ffmpeg)
	rec := httptest.NewRecorder()
	serve(t, l, rec, video)
	if got := rec.Body.String(); got != "fragmented-mp4" {
		t.Fatalf("body = %q, want %q", got, "fragmented-mp4")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

// goneWriter is a response whose client has disconnected.
type goneWriter struct{ h http.Header }

func (g *goneWriter) Header() http.Header       { return g.h }
func (g *goneWriter) WriteHeader(int)           {}
func (g *goneWriter) Write([]byte) (int, error) { return 0, errors.New("write: broken pipe") }

// When the player goes away ffmpeg must be stopped right away, even before
// the request context is canceled, instead of blocking on a full pipe.
func TestServeTranscodeStopsFfmpegWhenPlayerGoesAway(t *testing.T) {
	l, video := newTestLocal(t, writeScript(t, "exec yes fragmented-mp4\n"))
	serve(t, l, &goneWriter{h: http.Header{}}, video)
}

func TestDecideOutOfRangeAudioTrack(t *testing.T) {
	p := &Probe{
		Container: "matroska,webm",
		Video:     []ProbeStream{{Codec: "h264"}},
		Audio:     []ProbeStream{{Codec: "aac", Channels: 2}, {Codec: "ac3", Channels: 6}},
	}
	for _, idx := range []int{-1, 0, 1, 5} {
		if method, _ := decide(p, "auto", idx); method == "" || method == "direct" {
			t.Errorf("decide(audio %d) = %q, want remux or transcode", idx, method)
		}
	}
}
