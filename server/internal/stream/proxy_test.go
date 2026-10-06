package stream

import "testing"

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
