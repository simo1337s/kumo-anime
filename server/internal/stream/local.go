package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/library"
	"github.com/simo1337s/animetest/server/internal/util"
)

// Local serves library files to the in-app player: direct play when the
// browser can decode them, otherwise an on-the-fly remux/transcode to
// fragmented MP4 with ffmpeg (so every video format plays).
type Local struct {
	settings *config.Store
	files    *library.Store
	db       *db.DB
}

func NewLocal(s *config.Store, f *library.Store, d *db.DB) *Local {
	return &Local{settings: s, files: f, db: d}
}

type ProbeStream struct {
	Index     int    `json:"index"`     // absolute stream index
	TypeIndex int    `json:"typeIndex"` // index among streams of the same type
	Type      string `json:"type"`      // video | audio | subtitle | attachment
	Codec     string `json:"codec"`
	Language  string `json:"language"`
	Title     string `json:"title"`
	Default   bool   `json:"default"`
	Forced    bool   `json:"forced"`
	Channels  int    `json:"channels,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Profile   string `json:"profile,omitempty"`
	PixFmt    string `json:"pixFmt,omitempty"`
	Bitmap    bool   `json:"bitmap,omitempty"`
}

type ExternalSub struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Language string `json:"language"`
}

type Probe struct {
	Path         string        `json:"path"`
	Container    string        `json:"container"`
	Duration     float64       `json:"duration"`
	Size         int64         `json:"size"`
	Video        []ProbeStream `json:"video"`
	Audio        []ProbeStream `json:"audio"`
	Subtitles    []ProbeStream `json:"subtitles"`
	ExternalSubs []ExternalSub `json:"externalSubs"`
	// Playback decision: direct | remux | transcode
	Method string `json:"method"`
	Reason string `json:"reason"`
}

var ErrNotInLibrary = errors.New("file is not part of the library")

// ErrFileGone is (wrapped in) the error for a library file that isn't on
// the disk: moved or deleted, or on a drive that isn't connected (its files
// are kept until it is, see library.Scanner).
var ErrFileGone = errors.New("the video file isn't there")

// resolve makes sure only indexed library files can be read.
func (l *Local) resolve(path string) (string, error) {
	path = filepath.Clean(path)
	f, err := l.files.Get(path)
	if err != nil || f == nil {
		return "", ErrNotInLibrary
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", goneError(path)
		}
		return "", err
	}
	return path, nil
}

// goneError says why an indexed file isn't on the disk.
func goneError(path string) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%w: it was moved or deleted (scan the library again)", ErrFileGone)
	}
	// Its folder is gone too: the topmost one missing is the drive's.
	for parent := filepath.Dir(dir); parent != dir; parent = filepath.Dir(dir) {
		if _, err := os.Stat(parent); err == nil {
			break
		}
		dir = parent
	}
	return fmt.Errorf("%w: %s is missing (is its drive connected?)", ErrFileGone, dir)
}

func (l *Local) Probe(ctx context.Context, path string) (*Probe, error) {
	path, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	cfg := l.settings.Get()
	key := probeKey(path)
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var p Probe
	if l.db.GetCache(key, &p) && p.Size == st.Size() {
		p.Method, p.Reason = decide(&p, cfg.Transcode.Mode, 0, nil)
		return &p, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cfg.Transcode.FfprobePath, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path).Output()
	if err != nil {
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return nil, errors.New("ffprobe took too long to read the file")
		case errors.As(err, &exit):
			// ffprobe is there but the file is unreadable (broken or unfinished).
			msg := strings.TrimSpace(string(exit.Stderr))
			if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
				msg = msg[i+1:]
			}
			if msg == "" {
				msg = err.Error()
			}
			return nil, fmt.Errorf("ffprobe couldn't read the file: %s", msg)
		}
		return nil, fmt.Errorf("ffprobe failed (install ffmpeg: %s): %w", util.InstallHint("ffmpeg"), err)
	}
	var raw struct {
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Index       int               `json:"index"`
			CodecType   string            `json:"codec_type"`
			CodecName   string            `json:"codec_name"`
			Channels    int               `json:"channels"`
			Width       int               `json:"width"`
			Height      int               `json:"height"`
			Profile     string            `json:"profile"`
			PixFmt      string            `json:"pix_fmt"`
			Tags        map[string]string `json:"tags"`
			Disposition map[string]int    `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	p = Probe{Path: path, Container: raw.Format.FormatName, Size: st.Size()}
	p.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	counts := map[string]int{}
	for _, s := range raw.Streams {
		ps := ProbeStream{
			Index: s.Index, Type: s.CodecType, Codec: s.CodecName, Channels: s.Channels, Width: s.Width, Height: s.Height, Profile: s.Profile, PixFmt: s.PixFmt,
			Language: s.Tags["language"], Title: s.Tags["title"], Default: s.Disposition["default"] == 1, Forced: s.Disposition["forced"] == 1,
		}
		if ps.Type == "video" && s.Disposition["attached_pic"] == 1 {
			continue
		}
		ps.TypeIndex = counts[ps.Type]
		counts[ps.Type]++
		switch ps.Type {
		case "video":
			p.Video = append(p.Video, ps)
		case "audio":
			p.Audio = append(p.Audio, ps)
		case "subtitle":
			ps.Bitmap = ps.Codec == "hdmv_pgs_subtitle" || ps.Codec == "dvd_subtitle" || ps.Codec == "dvb_subtitle"
			p.Subtitles = append(p.Subtitles, ps)
		}
	}
	p.ExternalSubs = externalSubs(path)
	l.db.SetCache(key, p, 30*24*time.Hour)
	p.Method, p.Reason = decide(&p, cfg.Transcode.Mode, 0, nil)
	return &p, nil
}

func externalSubs(path string) []ExternalSub {
	dir := filepath.Dir(path)
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []ExternalSub
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !library.SubtitleExtensions[strings.ToLower(filepath.Ext(name))] || !strings.HasPrefix(name, base) {
			continue
		}
		lang := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(name, base), filepath.Ext(name)), ".-_ ")
		out = append(out, ExternalSub{Name: name, Path: filepath.Join(dir, name), Language: lang})
	}
	return out
}

// probeKey caches probes; v2 added the profile and pixel format.
func probeKey(path string) string { return "probe2:" + path }

// Caps are the video formats a player can decode, like "h264", "hevc" or
// "hevc-10" (10-bit), and "mkv" when it plays Matroska files as they are.
// Videos in them are played or copied as they are; others are converted to
// H.264.
type Caps map[string]bool

// DefaultCaps is what Chromium plays, the desktop app's browser.
var DefaultCaps = Caps{"h264": true, "vp8": true, "vp9": true, "vp9-10": true, "av1": true, "av1-10": true, "mkv": true}

// ParseCaps reads a comma-separated list of formats; none means DefaultCaps.
func ParseCaps(list string) Caps {
	return CapsOf(strings.Split(list, ","))
}

func CapsOf(list []string) Caps {
	c := Caps{}
	for _, f := range list {
		if f = strings.TrimSpace(strings.ToLower(f)); f != "" {
			c[f] = true
		}
	}
	if len(c) == 0 {
		return DefaultCaps
	}
	c["h264"] = true // every player decodes 8-bit H.264
	return c
}

// videoFormat names a video stream's format the way Caps do: its codec,
// with "-10" for 10-bit (or deeper) video, which many devices can't decode
// (10-bit H.264 plays nowhere in a browser).
func videoFormat(v ProbeStream) string {
	if strings.Contains(v.PixFmt, "p10") || strings.Contains(v.PixFmt, "p12") || strings.Contains(v.Profile, "10") {
		return v.Codec + "-10"
	}
	return v.Codec
}

func formatName(f string) string {
	codec, deep := strings.CutSuffix(f, "-10")
	name := map[string]string{"h264": "H.264", "hevc": "HEVC", "av1": "AV1", "vp9": "VP9", "vp8": "VP8"}[codec]
	if name == "" {
		name = strings.ToUpper(codec)
	}
	if deep {
		return "10-bit " + name
	}
	return name
}

var browserAudio = map[string]bool{"aac": true, "mp3": true, "opus": true, "vorbis": true, "flac": true}

// decide picks direct play, remux (copy into fMP4) or transcode for a
// player that decodes caps (nil: DefaultCaps).
func decide(p *Probe, mode string, audioIdx int, caps Caps) (string, string) {
	if caps == nil {
		caps = DefaultCaps
	}
	if mode == "transcode" {
		return "transcode", "forced by settings"
	}
	if len(p.Video) == 0 {
		return "transcode", "no video stream"
	}
	v := p.Video[0]
	if f := videoFormat(v); !caps[f] {
		if mode == "direct" {
			return "direct", "forced by settings"
		}
		return "transcode", fmt.Sprintf("this device can't play %s video", formatName(f))
	}
	audioOK := true
	if len(p.Audio) > 0 {
		a := p.Audio[min(max(audioIdx, 0), len(p.Audio)-1)]
		audioOK = browserAudio[a.Codec] && a.Channels <= 2 || a.Codec == "aac" || a.Codec == "opus"
	}
	// ffprobe calls both Matroska and WebM "matroska,webm".
	mkv := strings.Contains(p.Container, "matroska") && !strings.EqualFold(filepath.Ext(p.Path), ".webm")
	containerOK := strings.Contains(p.Container, "mp4") || strings.Contains(p.Container, "mov") || strings.Contains(p.Container, "webm") && (!mkv || caps["mkv"])
	if mode == "direct" || (containerOK && audioOK && audioIdx == 0 && len(p.Audio) <= 1) {
		return "direct", "the browser can play this file as is"
	}
	if audioOK {
		return "remux", "repackaging into MP4 (no quality loss)"
	}
	return "transcode", "converting the audio track for the browser"
}

// Decide redoes the playback decision of a probe for a player.
func (l *Local) Decide(p *Probe, caps Caps) {
	p.Method, p.Reason = decide(p, l.settings.Get().Transcode.Mode, 0, caps)
}

// plan is how ffmpeg converts a file for one player.
type plan struct {
	method    string // remux | transcode
	videoCopy bool
	audioCopy bool
	hevc      bool // copied HEVC: tagged so Apple devices play it
	audio     int  // the audio track, among the file's audio tracks
}

// plan works out the conversion for method (direct or "" means decide),
// audio track and player. HLS players only get AAC audio copied, the one
// codec every HLS player takes.
func (l *Local) plan(p *Probe, method string, audio int, caps Caps, hls bool) plan {
	if caps == nil {
		caps = DefaultCaps
	}
	if method == "" || method == "direct" {
		method, _ = decide(p, l.settings.Get().Transcode.Mode, audio, caps)
		if method == "direct" {
			method = "remux"
		}
	}
	pl := plan{method: method}
	// Video the player decodes is copied, also when transcoding (that's for
	// the audio, e.g. TrueHD next to HEVC a Mac plays as is).
	if len(p.Video) > 0 {
		v := p.Video[0]
		pl.videoCopy = caps[videoFormat(v)]
		pl.hevc = pl.videoCopy && v.Codec == "hevc"
	}
	if len(p.Audio) > 0 {
		pl.audio = min(max(audio, 0), len(p.Audio)-1)
		codec := p.Audio[pl.audio].Codec
		pl.audioCopy = method == "remux" && (codec == "aac" || !hls && browserAudio[codec])
	}
	if !pl.videoCopy || len(p.Audio) > 0 && !pl.audioCopy {
		pl.method = "transcode"
	}
	return pl
}

// args are ffmpeg's arguments for the plan, up to the output format.
func (pl plan) args(cfg config.Settings, p *Probe, path string, start float64) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	hw := cfg.Transcode.HwAccel
	if !pl.videoCopy && hw == "vaapi" {
		args = append(args, "-vaapi_device", cfg.Transcode.VaapiNode)
	}
	if start > 0 {
		if pl.videoCopy {
			start += seekMargin // lands on the keyframe at start, see SeekPoint
		}
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", path, "-map", "0:v:0?")
	if len(p.Audio) > 0 {
		args = append(args, "-map", fmt.Sprintf("0:a:%d?", pl.audio))
	}
	if pl.videoCopy {
		args = append(args, "-c:v", "copy")
		if pl.hevc {
			args = append(args, "-tag:v", "hvc1")
		}
	} else {
		switch hw {
		case "vaapi":
			args = append(args, "-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi", "-qp", "23")
		case "nvenc":
			args = append(args, "-c:v", "h264_nvenc", "-preset", "p4", "-cq", "23")
		case "qsv":
			args = append(args, "-c:v", "h264_qsv", "-global_quality", "23")
		default:
			args = append(args, "-c:v", "libx264", "-preset", cfg.Transcode.Preset, "-crf", "21", "-pix_fmt", "yuv420p", "-profile:v", "high")
		}
	}
	if pl.audioCopy {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "192k")
	}
	return append(args, "-sn", "-dn")
}

// SeekPoint returns where a stream asked to start at t really starts, which
// the player needs to keep subtitles and the position right after seeking.
// Converted video starts at t exactly. Copied video can only start at a
// keyframe: the last one at or before t.
//
// Starting there takes care, as ffmpeg's -ss lands on the last keyframe
// some way before the time given: about 0.13s before with B-frames, plus
// the audio's pre-roll (up to 0.08s for Opus). So copies start at
// keyframe + seekMargin, and a keyframe followed that closely by another
// one (a scene cut) is passed over for the next.
func (l *Local) SeekPoint(ctx context.Context, path string, t float64, audio int, method string, caps Caps, hls bool) (float64, error) {
	path, err := l.resolve(path)
	if err != nil {
		return 0, err
	}
	p, err := l.Probe(ctx, path)
	if err != nil {
		return 0, err
	}
	return l.startFor(ctx, path, l.plan(p, method, audio, caps, hls), t), nil
}

const seekMargin = 0.4

// seekAhead: a keyframe this little after the time asked is taken over the
// last one before it, further back. Skips and resumes are mostly asked for
// at the end of a scene, and the next one starts with a keyframe: a skipped
// ending then isn't partly heard again.
const seekAhead = 1.5

func (l *Local) startFor(ctx context.Context, path string, pl plan, t float64) float64 {
	if t <= 0 {
		return 0
	}
	if !pl.videoCopy {
		return t
	}
	if k, ok := l.seekKeyframe(ctx, path, t); ok {
		return k
	}
	return t
}

// seekKeyframe finds the keyframe a copy asked to start at t starts at: the
// last one at or before t, or the next one when it's close (seekAhead) and
// the last one isn't.
func (l *Local) seekKeyframe(ctx context.Context, path string, t float64) (float64, bool) {
	// A disk waking up, or a file read for the first time, takes a while:
	// without its keyframe, a copy would start mid-picture (grey until the
	// next keyframe).
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	ffprobe := l.settings.Get().Transcode.FfprobePath
	for _, window := range []float64{20, 120} {
		from := max(0, t-window)
		out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
			"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0",
			"-read_intervals", fmt.Sprintf("%.3f%%%.3f", from, t+seekAhead+1), path).Output()
		if err != nil {
			return 0, false
		}
		var keys []float64
		for _, line := range strings.Split(string(out), "\n") {
			ts, flags, ok := strings.Cut(strings.TrimSpace(line), ",")
			if !ok || !strings.HasPrefix(flags, "K") {
				continue
			}
			if v, err := strconv.ParseFloat(ts, 64); err == nil {
				keys = append(keys, v)
			}
		}
		sort.Float64s(keys)
		i := -1
		for j, k := range keys {
			if k <= t+0.0005 {
				i = j
			}
		}
		if i < 0 && from > 0 {
			continue // look further back
		}
		if i+1 < len(keys) && keys[i+1]-t <= seekAhead && (i < 0 || t-keys[i] > 0.5) {
			i++
		}
		if i < 0 {
			return 0, true // no keyframe read before t: from the start
		}
		for i+1 < len(keys) && keys[i+1]-keys[i] < seekMargin+0.05 {
			i++
		}
		return keys[i], true
	}
	return 0, false
}

// ServeFile streams the raw file with range support (direct play).
func (l *Local) ServeFile(w http.ResponseWriter, r *http.Request, path string) {
	path, err := l.resolve(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	// Set the type explicitly so a mislabelled file is never sniffed as HTML.
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if strings.EqualFold(filepath.Ext(path), ".mkv") {
		ct = "video/x-matroska"
	}
	if !strings.HasPrefix(ct, "video/") && !strings.HasPrefix(ct, "audio/") {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

// ServeTranscode pipes ffmpeg output as fragmented MP4 starting at `start`
// (see SeekPoint), for players that decode caps.
func (l *Local) ServeTranscode(w http.ResponseWriter, r *http.Request, path string, start float64, audio int, method string, caps Caps) {
	path, err := l.resolve(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	cfg := l.settings.Get()
	p, err := l.Probe(r.Context(), path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pl := l.plan(p, method, audio, caps, false)
	method = pl.method
	args := append(pl.args(cfg, p, path, start), "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")

	// ffmpeg is stopped when the request ends, or as soon as the player stops
	// reading (see below).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.Transcode.FfmpegPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// All of stderr must be read: a damaged file makes ffmpeg print an error
	// per frame, and once the pipe is full it blocks and playback freezes.
	// Wait returns only after this writer has consumed everything.
	stderr := &headWriter{max: 8 << 10}
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		http.Error(w, "ffmpeg is not installed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Kumo-Method", method)
	if _, err := io.Copy(w, stdout); err != nil {
		// The player went away (or the connection broke): stop ffmpeg now
		// instead of leaving it blocked on a pipe nobody reads.
		cancel()
	}
	_ = cmd.Wait()
	if s := strings.TrimSpace(string(stderr.buf)); s != "" && ctx.Err() == nil {
		log.Printf("ffmpeg: %s", s)
	}
}

// headWriter keeps the first max bytes written to it and discards the rest.
type headWriter struct {
	buf []byte
	max int
}

func (h *headWriter) Write(p []byte) (int, error) {
	if n := min(len(p), h.max-len(h.buf)); n > 0 {
		h.buf = append(h.buf, p[:n]...)
	}
	return len(p), nil
}

// ServeSubtitle extracts an embedded text subtitle (or reads an external
// subtitle file) and returns WebVTT.
func (l *Local) ServeSubtitle(w http.ResponseWriter, r *http.Request, path string, index int, external string) {
	path, err := l.resolve(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	if external != "" {
		ext := filepath.Clean(external)
		if filepath.Dir(ext) != filepath.Dir(path) || !library.SubtitleExtensions[strings.ToLower(filepath.Ext(ext))] {
			http.Error(w, "invalid subtitle", http.StatusForbidden)
			return
		}
		b, err := os.ReadFile(ext)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		_, _ = w.Write(ToVTT(b, strings.ToLower(ext)))
		return
	}
	cfg := l.settings.Get()
	key := fmt.Sprintf("subvtt:%s:%d", path, index)
	var cached string
	if l.db.GetCache(key, &cached) {
		_, _ = w.Write([]byte(cached))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, cfg.Transcode.FfmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", path, "-map", fmt.Sprintf("0:s:%d", index), "-f", "webvtt", "pipe:1").Output()
	if err != nil {
		http.Error(w, "could not extract subtitles (image-based subtitles need mpv)", http.StatusUnprocessableEntity)
		return
	}
	l.db.SetCache(key, string(out), 7*24*time.Hour)
	_, _ = w.Write(out)
}
