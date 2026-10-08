package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/library"
)

// newFfmpegLocal returns a Local using the real ffmpeg and a 30 second
// library video (keyframes every 5 seconds) encoded with vcodec and the
// extra ffmpeg output options.
func newFfmpegLocal(t *testing.T, vcodec string, extra ...string) (*Local, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "Show - 01.mkv")
	args := []string{"-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440", "-t", "30", "-c:v", vcodec, "-g", "120", "-keyint_min", "120", "-sc_threshold", "0",
		"-c:a", "aac"}
	args = append(append(args, extra...), "-shortest", video)
	out, err := exec.Command(ffmpeg, args...).CombinedOutput()
	if err != nil {
		t.Skipf("can't encode a test video with %s: %v %s", vcodec, err, out)
	}
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
	cfg.Transcode.FfmpegPath, cfg.Transcode.FfprobePath = ffmpeg, ffprobe
	cfg.Transcode.HwAccel, cfg.Transcode.Preset = "none", "ultrafast"
	if _, err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	files := library.NewStore(d)
	if err := files.Save(&library.LocalFile{Path: video, Dir: dir, Name: filepath.Base(video), Kind: "main"}); err != nil {
		t.Fatal(err)
	}
	return NewLocal(s, files, d), video
}

// Wherever ffmpeg's seek lands for different kinds of files, the stream
// starts where SeekPoint says.
func TestSeekPointAcrossFormats(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []string
		t     float64
		want  float64
	}{
		{"opus audio", []string{"-c:a", "libopus"}, 12, 10},
		{"no b-frames", []string{"-bf", "0"}, 12, 10},
		// Keyframes 0.2s apart (10.2 is frame 245, at 10.208): ffmpeg can't
		// be made to land on the first.
		{"scene cut", []string{"-g", "1000", "-force_key_frames", "0,5,10,10.2,15,20,25"}, 10.1, 10.208},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, video := newFfmpegLocal(t, "libx264", tc.extra...)
			got, err := l.SeekPoint(context.Background(), video, tc.t, 0, "remux", nil, false)
			if err != nil || got != tc.want {
				t.Fatalf("SeekPoint(%v) = %v, %v; want %v", tc.t, got, err, tc.want)
			}
			rec := httptest.NewRecorder()
			l.ServeTranscode(rec, httptest.NewRequest(http.MethodGet, "/", nil), video, got, 0, "remux", nil)
			if d := probeDuration(t, rec.Body.Bytes()); d < 30-got-0.2 || d > 30-got+0.2 {
				t.Errorf("stream from %v lasts %.2fs, want %.2f", got, d, 30-got)
			}
		})
	}
}

// Copied video starts at the keyframe before the requested time, or the one
// just after it, and the player must know it, or subtitles are off by up to
// a keyframe interval.
func TestSeekPointIsTheKeyframe(t *testing.T) {
	l, video := newFfmpegLocal(t, "libx264")
	ctx := context.Background()
	// Keyframes every 5 seconds.
	for _, tc := range []struct{ t, want float64 }{
		{0, 0}, {10, 10}, {12, 10}, {29, 25},
		{4, 5},     // the next one, 1s on, rather than the last, 4s back
		{3, 0},     // the next one is 2s on: too far
		{10.4, 10}, // the last one is right there
		{14.9, 15}, // a skip ending just before a scene cut
	} {
		got, err := l.SeekPoint(ctx, video, tc.t, 0, "remux", nil, false)
		if err != nil || got != tc.want {
			t.Errorf("SeekPoint(%v) = %v, %v; want %v", tc.t, got, err, tc.want)
		}
	}
	// The remux started there really begins with that keyframe.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/local/transcode", nil)
	l.ServeTranscode(rec, r, video, 10, 0, "remux", nil)
	if got := probeDuration(t, rec.Body.Bytes()); got < 19.8 || got > 20.2 {
		t.Errorf("remux from 10 lasts %.2fs, want 20 (from the keyframe at 10)", got)
	}
	// Converted video starts exactly where asked.
	if got, _ := l.SeekPoint(ctx, video, 12, 0, "remux", Caps{"vp9": true}, false); got != 12 {
		t.Errorf("SeekPoint for a player that can't decode the video = %v, want 12", got)
	}
}

func probeDuration(t *testing.T, mp4 []byte) float64 {
	t.Helper()
	f := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(f, mp4, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", f).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return d
}

func TestHLSSession(t *testing.T) {
	l, video := newFfmpegLocal(t, "libx264")
	root := filepath.Join(t.TempDir(), "hls")
	h := NewHLS(l, root)
	t.Cleanup(h.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/local/hls/"), "/")
		h.Serve(w, r, parts[0], parts[1])
	}))
	defer srv.Close()
	ctx := context.Background()

	s, err := h.Start(ctx, HLSRequest{Path: video, Start: 12, Client: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Start != 10 || s.Method != "remux" {
		t.Errorf("session %+v, want a remux from the keyframe at 10", s)
	}
	waitFor(t, func() bool { _, complete := h.get(s.ID).segments(); return complete })
	res, err := http.Get(srv.URL + s.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readAll(res)
	pl := string(b)
	prefix := "/api/local/hls/" + s.ID + "/"
	for _, want := range []string{"#EXT-X-START:TIME-OFFSET=0", `#EXT-X-MAP:URI="` + prefix + `init.mp4"`, prefix + "seg_00000.m4s", "#EXT-X-ENDLIST"} {
		if !strings.Contains(pl, want) {
			t.Errorf("playlist lacks %q:\n%s", want, pl)
		}
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("playlist type %q", ct)
	}
	// A player can read it all: 20 seconds from the keyframe at 10.
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", srv.URL+s.URL).Output()
	if d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err != nil || d < 19.8 || d > 20.2 {
		t.Errorf("HLS duration %v (%v), want 20", d, err)
	}
	for _, name := range []string{"../index.m3u8", "x.m4s", "seg_1.m4s"} {
		if res, err := http.Get(srv.URL + prefix + name); err == nil && res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, res.StatusCode)
		}
	}

	// The same request reuses the session; a new one replaces the player's
	// old session but not another player's.
	again, err := h.Start(ctx, HLSRequest{Path: video, Start: 12, Client: "a"})
	if err != nil || again.ID != s.ID {
		t.Fatalf("same request: %+v %v, want session %s", again, err, s.ID)
	}
	other, err := h.Start(ctx, HLSRequest{Path: video, Start: 0, Client: "b"})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := h.Start(ctx, HLSRequest{Path: video, Start: 20, Client: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if h.get(s.ID) != nil {
		t.Error("the player's old session is still there")
	}
	if _, err := os.Stat(filepath.Join(root, s.ID)); !os.IsNotExist(err) {
		t.Errorf("old session folder not removed: %v", err)
	}
	if h.get(other.ID) == nil || h.get(moved.ID) == nil {
		t.Error("sessions of other players must stay")
	}
	h.Stop(moved.ID)
	if res, err := http.Get(srv.URL + moved.URL); err != nil || res.StatusCode != http.StatusNotFound {
		t.Errorf("stopped session still served: %v", err)
	}
}

// A player that can't decode the video gets it converted, with the audio
// in AAC (the one codec every HLS player takes).
func TestHLSTranscodes(t *testing.T) {
	l, video := newFfmpegLocal(t, "mpeg4")
	h := NewHLS(l, filepath.Join(t.TempDir(), "hls"))
	t.Cleanup(h.Close)
	s, err := h.Start(context.Background(), HLSRequest{Path: video, Start: 12, Client: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Start != 12 || s.Method != "transcode" {
		t.Errorf("session %+v, want a transcode from 12", s)
	}
	sess := h.get(s.ID)
	waitFor(t, func() bool { _, complete := sess.segments(); return complete })
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name", "-of", "csv=p=0", filepath.Join(sess.dir, "init.mp4")).Output()
	if err != nil || strings.Fields(string(out))[0] != "h264" {
		t.Errorf("init segment codecs %q (%v), want h264 and aac", out, err)
	}
}

func TestHLSThrottle(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "60")
	if runtime.GOOS == "windows" {
		cmd = exec.Command("ping", "-n", "60", "127.0.0.1")
	}
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	s := &hlsSession{dir: dir, cmd: cmd, done: make(chan struct{}), requested: -1}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	writePlaylist := func(n int) {
		var b strings.Builder
		b.WriteString("#EXTM3U\n")
		for i := 0; i < n; i++ {
			b.WriteString("#EXTINF:4.0,\nseg_" + strconv.Itoa(10000 + i)[1:] + ".m4s\n")
		}
		if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Linux shows whether a process is stopped; elsewhere only the calls'
	// success is checked.
	state := func() string {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/stat")
		if err != nil {
			return "-"
		}
		if f := strings.Fields(string(b)); len(f) > 2 {
			return f[2]
		}
		return "?"
	}
	writePlaylist(hlsAhead)
	s.throttle()
	if s.paused {
		t.Fatal("paused while the player is close behind")
	}
	writePlaylist(hlsAhead + 5)
	s.throttle()
	if !s.paused {
		t.Fatal("not paused far ahead of the player")
	}
	waitFor(t, func() bool { return state() == "T" || state() == "-" })
	s.requested = 30 // the player caught up
	s.throttle()
	if s.paused {
		t.Fatal("not resumed when the player caught up")
	}
	waitFor(t, func() bool { return state() == "S" || state() == "R" || state() == "-" })
}

func TestSessionPlaylist(t *testing.T) {
	in := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4.000000,\nseg_00000.m4s\n#EXTINF:4.000000,\n/abs/dir/seg_00001.m4s\n"
	got := string(sessionPlaylist([]byte(in), "/api/local/hls/x/"))
	want := "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-MAP:URI=\"/api/local/hls/x/init.mp4\"\n#EXTINF:4.000000,\n/api/local/hls/x/seg_00000.m4s\n#EXTINF:4.000000,\n/api/local/hls/x/seg_00001.m4s\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestCaps(t *testing.T) {
	hevc10 := ProbeStream{Codec: "hevc", Profile: "Main 10", PixFmt: "yuv420p10le"}
	hi10 := ProbeStream{Codec: "h264", Profile: "High 10", PixFmt: "yuv420p10le"}
	h264 := ProbeStream{Codec: "h264", Profile: "High", PixFmt: "yuv420p"}
	mkv := func(v ProbeStream) *Probe {
		return &Probe{Path: "/a/Show - 01.mkv", Container: "matroska,webm", Video: []ProbeStream{v}, Audio: []ProbeStream{{Codec: "aac", Channels: 2}}}
	}
	safari := CapsOf([]string{"h264", "hevc", "hevc-10"})
	for _, tc := range []struct {
		name   string
		v      ProbeStream
		caps   Caps
		decide string // what the player is told
		plan   string // what ffmpeg does when it converts
		vcopy  bool
	}{
		{"desktop plays Matroska", h264, nil, "direct", "remux", true},
		{"Safari doesn't", h264, safari, "remux", "remux", true},
		{"10-bit h264 plays nowhere", hi10, safari, "transcode", "transcode", false},
		{"hevc on a desktop browser", hevc10, nil, "transcode", "transcode", false},
		{"10-bit hevc on a Mac", hevc10, safari, "remux", "remux", true},
		{"8-bit hevc only", hevc10, CapsOf([]string{"hevc"}), "transcode", "transcode", false},
	} {
		method, _ := decide(mkv(tc.v), "auto", 0, tc.caps)
		l := &Local{settings: testSettings(t)}
		pl := l.plan(mkv(tc.v), "", 0, tc.caps, true)
		if method != tc.decide || pl.method != tc.plan || pl.videoCopy != tc.vcopy || pl.hevc != (tc.vcopy && tc.v.Codec == "hevc") {
			t.Errorf("%s: decide %s, plan %+v; want %s, %s, video copy %v", tc.name, method, pl, tc.decide, tc.plan, tc.vcopy)
		}
	}
	// HEVC with TrueHD on a Mac: the audio is converted, the video copied.
	bd := mkv(hevc10)
	bd.Audio = []ProbeStream{{Codec: "truehd", Channels: 8}}
	if m, _ := decide(bd, "auto", 0, safari); m != "transcode" {
		t.Errorf("TrueHD needs converting: %s", m)
	}
	if pl := (&Local{settings: testSettings(t)}).plan(bd, "transcode", 0, safari, true); !pl.videoCopy || !pl.hevc || pl.audioCopy || pl.method != "transcode" {
		t.Errorf("HEVC + TrueHD on a Mac: %+v, want the video copied and the audio converted", pl)
	}
	// HLS players get AAC: Opus is converted.
	opus := mkv(h264)
	opus.Audio = []ProbeStream{{Codec: "opus", Channels: 2}}
	if pl := (&Local{settings: testSettings(t)}).plan(opus, "remux", 0, safari, true); pl.audioCopy || pl.method != "transcode" || !pl.videoCopy {
		t.Errorf("Opus over HLS: %+v", pl)
	}
	webm := &Probe{Path: "/a/x.webm", Container: "matroska,webm", Video: []ProbeStream{{Codec: "vp9"}}, Audio: []ProbeStream{{Codec: "opus", Channels: 2}}}
	if m, _ := decide(webm, "auto", 0, CapsOf([]string{"vp9"})); m != "direct" {
		t.Errorf("WebM plays as is where VP9 does: %s", m)
	}
	if c := ParseCaps(""); !c["h264"] || !c["mkv"] || c["hevc"] {
		t.Errorf("no caps should mean the defaults: %v", c)
	}
	if c := ParseCaps("hevc, HEVC-10"); !c["h264"] || !c["hevc-10"] || c["vp9"] || c["mkv"] {
		t.Errorf("ParseCaps: %v", c)
	}
}

func testSettings(t *testing.T) *config.Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readAll(res *http.Response) ([]byte, error) {
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return []byte(b.String()), nil
		}
	}
}
