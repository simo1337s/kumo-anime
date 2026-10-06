package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/library"
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

// resolve makes sure only indexed library files can be read.
func (l *Local) resolve(path string) (string, error) {
	path = filepath.Clean(path)
	f, err := l.files.Get(path)
	if err != nil || f == nil {
		return "", ErrNotInLibrary
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

func (l *Local) Probe(ctx context.Context, path string) (*Probe, error) {
	path, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	cfg := l.settings.Get()
	key := "probe:" + path
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var p Probe
	if l.db.GetCache(key, &p) && p.Size == st.Size() {
		p.Method, p.Reason = decide(&p, cfg.Transcode.Mode, 0)
		return &p, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cfg.Transcode.FfprobePath, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed (install ffmpeg: sudo pacman -S ffmpeg): %w", err)
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
			Index: s.Index, Type: s.CodecType, Codec: s.CodecName, Channels: s.Channels, Width: s.Width, Height: s.Height,
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
	p.Method, p.Reason = decide(&p, cfg.Transcode.Mode, 0)
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

var (
	browserVideo = map[string]bool{"h264": true, "vp8": true, "vp9": true, "av1": true}
	browserAudio = map[string]bool{"aac": true, "mp3": true, "opus": true, "vorbis": true, "flac": true}
)

// decide picks direct play, remux (copy into fMP4) or transcode.
func decide(p *Probe, mode string, audioIdx int) (string, string) {
	if mode == "transcode" {
		return "transcode", "forced by settings"
	}
	if len(p.Video) == 0 {
		return "transcode", "no video stream"
	}
	v := p.Video[0]
	if !browserVideo[v.Codec] {
		if mode == "direct" {
			return "direct", "forced by settings"
		}
		return "transcode", fmt.Sprintf("video codec %s isn't supported by the browser", v.Codec)
	}
	audioOK := true
	if len(p.Audio) > 0 {
		a := p.Audio[min(max(audioIdx, 0), len(p.Audio)-1)]
		audioOK = browserAudio[a.Codec] && a.Channels <= 2 || a.Codec == "aac" || a.Codec == "opus"
	}
	containerOK := strings.Contains(p.Container, "mp4") || strings.Contains(p.Container, "webm") || strings.Contains(p.Container, "mov")
	if mode == "direct" || (containerOK && audioOK && audioIdx == 0 && len(p.Audio) <= 1) {
		return "direct", "the browser can play this file as is"
	}
	if audioOK {
		return "remux", "repackaging into MP4 (no quality loss)"
	}
	return "transcode", "converting the audio track for the browser"
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

// ServeTranscode pipes ffmpeg output as fragmented MP4 starting at `start`.
func (l *Local) ServeTranscode(w http.ResponseWriter, r *http.Request, path string, start float64, audio int, method string) {
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
	if method == "" || method == "direct" {
		method, _ = decide(p, cfg.Transcode.Mode, audio)
		if method == "direct" {
			method = "remux"
		}
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	videoCopy := method == "remux" && len(p.Video) > 0 && browserVideo[p.Video[0].Codec]
	if method == "transcode" && len(p.Video) > 0 && browserVideo[p.Video[0].Codec] && p.Video[0].Codec == "h264" {
		videoCopy = true // only audio needs converting
	}
	hw := cfg.Transcode.HwAccel
	if !videoCopy && hw == "vaapi" {
		args = append(args, "-vaapi_device", cfg.Transcode.VaapiNode)
	}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 2, 64))
	}
	args = append(args, "-i", path, "-map", "0:v:0?")
	if len(p.Audio) > 0 {
		args = append(args, "-map", fmt.Sprintf("0:a:%d?", min(max(audio, 0), len(p.Audio)-1)))
	}
	if videoCopy {
		args = append(args, "-c:v", "copy")
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
	audioCopy := method == "remux" && len(p.Audio) > 0 && browserAudio[p.Audio[min(max(audio, 0), len(p.Audio)-1)].Codec]
	if audioCopy {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "192k")
	}
	args = append(args, "-sn", "-dn", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")

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
