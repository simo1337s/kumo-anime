package images

import "testing"

func TestRasterType(t *testing.T) {
	cases := map[string]string{
		"\xff\xd8\xff\xe0\x00\x10JFIF\x00":                 "image/jpeg",
		"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR":              "image/png",
		"GIF89a\x01\x00\x01\x00":                           "image/gif",
		"RIFF\x00\x00\x00\x00WEBPVP8 ":                     "image/webp",
		"\x00\x00\x00\x1cftypavif\x00\x00\x00\x00":         "image/avif",
		"<!doctype html><script>alert(1)</script>":         "",
		"<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>": "",
		"<?xml version=\"1.0\"?><svg></svg>":               "",
		"":                                                 "",
	}
	for in, want := range cases {
		if got := rasterType([]byte(in)); got != want {
			t.Errorf("rasterType(%q) = %q, want %q", in, got, want)
		}
	}
}
