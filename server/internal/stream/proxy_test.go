package stream

import (
	"net/url"
	"strings"
	"testing"
)

func TestSafeContentType(t *testing.T) {
	for in, want := range map[string]string{
		"video/mp4":                     "video/mp4",
		"application/vnd.apple.mpegurl": "application/vnd.apple.mpegurl",
		"image/jpeg":                    "image/jpeg",
		"text/vtt; charset=utf-8":       "text/vtt; charset=utf-8",
		"text/html; charset=utf-8":      "application/octet-stream",
		"image/svg+xml":                 "application/octet-stream",
		"application/xhtml+xml":         "application/octet-stream",
		"text/xml":                      "application/octet-stream",
		"application/javascript":        "application/octet-stream",
		"":                              "application/octet-stream",
		"application/x-shockwave-flash": "application/octet-stream",
		"video/mp2t":                    "video/mp2t",
		"application/octet-stream":      "application/octet-stream",
	} {
		if got := SafeContentType(in); got != want {
			t.Errorf("SafeContentType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRewritePlaylist(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.com/hls/ep7/index.m3u8")
	in := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:4.0,\nseg-1.ts\n\n#EXTINF:4.0,\nhttps://other.example.com/seg-2.ts\n"
	out := string(rewritePlaylist([]byte(in), base, map[string]string{"Referer": "https://site.example/"}))
	for _, want := range []string{
		"URI=\"" + ProxyURL("https://cdn.example.com/hls/ep7/key.bin", map[string]string{"Referer": "https://site.example/"}) + "\"",
		ProxyURL("https://cdn.example.com/hls/ep7/seg-1.ts", map[string]string{"Referer": "https://site.example/"}),
		ProxyURL("https://other.example.com/seg-2.ts", map[string]string{"Referer": "https://site.example/"}),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten playlist lacks %q:\n%s", want, out)
		}
	}
}

// Every URL hls.js would load goes through the proxy (none left to load
// from Kumo's own address), and a login header stays with its host.
func TestRewritePlaylistLeavesNothingOut(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.com/hls/index.m3u8")
	h := map[string]string{"Referer": "https://site.example/", "Authorization": "Bearer secret"}
	in := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=http://127.0.0.1:43211/api/settings\n#EXT-X-MAP:URI=http://192.168.1.1/init.mp4\n#EXTINF:4.0,\rhttp://127.0.0.1:43211/api/x\nseg.ts\nhttps://evil.example/seg.ts\njavascript:alert(1)\n"
	out := string(rewritePlaylist([]byte(in), base, h))
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "#EXTM3U") || strings.HasPrefix(line, "#EXTINF") {
			continue
		}
		if !strings.Contains(line, "/api/proxy?") && line != "" {
			t.Errorf("not proxied: %q", line)
		}
	}
	if strings.Contains(out, "127.0.0.1") || strings.Contains(out, "javascript") {
		t.Errorf("a raw URL is left:\n%s", out)
	}
	if !strings.Contains(out, ProxyURL("https://cdn.example.com/hls/seg.ts", h)) {
		t.Errorf("the playlist's host lost its headers:\n%s", out)
	}
	if !strings.Contains(out, ProxyURL("https://evil.example/seg.ts", map[string]string{"Referer": "https://site.example/"})) {
		t.Errorf("another host got the login, or lost the Referer:\n%s", out)
	}
}
