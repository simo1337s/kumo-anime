package stream

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/util"
)

// The proxy lets the in-app player load streams that need special headers
// (Referer/Origin) or lack CORS. HLS playlists are rewritten so every
// segment/key/sub-playlist also goes through the proxy. It refuses to
// connect to private addresses so it can't be used to reach the LAN.

var proxyClient = &http.Client{
	Timeout: 0, // streams can be long
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           util.PublicDialContext(15 * time.Second),
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConnsPerHost:   16,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 10 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// ProxyURL builds the URL the browser should use for a remote resource.
func ProxyURL(target string, headers map[string]string) string {
	q := url.Values{}
	q.Set("u", base64.RawURLEncoding.EncodeToString([]byte(target)))
	if len(headers) > 0 {
		raw, _ := json.Marshal(headers)
		q.Set("h", base64.RawURLEncoding.EncodeToString(raw))
	}
	return "/api/proxy?" + q.Encode()
}

var reURIAttr = regexp.MustCompile(`URI="([^"]+)"`)

// IsDocumentRequest reports a navigation or frame load. Proxied and cached
// remote content is only ever loaded as media/images/XHR by the app, never
// opened as a page on Kumo's origin.
func IsDocumentRequest(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "document", "iframe", "frame", "embed", "object":
		return true
	}
	return false
}

// SafeContentType passes through media, image, playlist and subtitle types
// and turns anything a browser could run as a page (HTML, SVG, XML, JS…)
// into a plain download.
func SafeContentType(ct string) string {
	mt := strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	if mt == "" || strings.Contains(mt, "html") || strings.Contains(mt, "xml") || strings.Contains(mt, "svg") || strings.Contains(mt, "script") {
		return "application/octet-stream"
	}
	switch {
	case strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "audio/"), strings.HasPrefix(mt, "image/"):
		return ct
	case mt == "application/vnd.apple.mpegurl", mt == "application/x-mpegurl", mt == "application/mp4",
		mt == "text/vtt", mt == "text/plain", mt == "application/octet-stream":
		return ct
	}
	return "application/octet-stream"
}

// ServeProxy implements GET /api/proxy.
func ServeProxy(w http.ResponseWriter, r *http.Request) {
	// Remote content must never run as a page on Kumo's origin.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if IsDocumentRequest(r) {
		http.Error(w, "not a page", http.StatusForbidden)
		return
	}
	rawU, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("u"))
	if err != nil {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	target := string(rawU)
	tu, err := url.Parse(target)
	if err != nil || (tu.Scheme != "http" && tu.Scheme != "https") {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	if strings.EqualFold(tu.Hostname(), "localhost") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	headers := map[string]string{}
	if h := r.URL.Query().Get("h"); h != "" {
		if raw, err := base64.RawURLEncoding.DecodeString(h); err == nil {
			_ = json.Unmarshal(raw, &headers)
		}
	}
	format := r.URL.Query().Get("fmt")

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", util.UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if rg := r.Header.Get("Range"); rg != "" && format == "" {
		req.Header.Set("Range", rg)
	}
	resp, err := proxyClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	ctype := resp.Header.Get("Content-Type")
	path := strings.ToLower(tu.Path)
	if resp.StatusCode >= 400 && (format != "" || strings.HasSuffix(path, ".m3u8") || strings.Contains(strings.ToLower(ctype), "mpegurl")) {
		// Pass errors on as errors: an HTML error page rewritten into a
		// "playlist" or "subtitle" would hide the real cause (an expired
		// link, a missing Referer…) behind a parse error.
		http.Error(w, "upstream: "+resp.Status, resp.StatusCode)
		return
	}
	isPlaylist := strings.Contains(strings.ToLower(ctype), "mpegurl") || strings.HasSuffix(path, ".m3u8")
	if !isPlaylist && resp.StatusCode < 400 && format == "" {
		// Some hosts serve playlists as text/plain or octet-stream (also
		// gzipped or chunked, so the length is often unknown).
		peek := bufio.NewReader(resp.Body)
		head, _ := peek.Peek(7)
		if string(head) == "#EXTM3U" {
			isPlaylist = true
		}
		resp.Body = io.NopCloser(peek)
	}

	w.Header().Set("Cache-Control", "no-store")

	switch {
	case isPlaylist:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		base := resp.Request.URL
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write(rewritePlaylist(body, base, headers))
	case format == "vtt":
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		_, _ = w.Write(ToVTT(body, path))
	default:
		for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.Header().Set("Content-Type", SafeContentType(ctype))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

func rewritePlaylist(body []byte, base *url.URL, headers map[string]string) []byte {
	abs := func(ref string) string {
		u, err := base.Parse(strings.TrimSpace(ref))
		if err != nil {
			return ref
		}
		return ProxyURL(u.String(), headers)
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			out.WriteString(line)
		case strings.HasPrefix(trimmed, "#"):
			out.WriteString(reURIAttr.ReplaceAllStringFunc(line, func(m string) string {
				sub := reURIAttr.FindStringSubmatch(m)
				return `URI="` + abs(sub[1]) + `"`
			}))
		default:
			out.WriteString(abs(trimmed))
		}
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// ---------------------------------------------------------------------------
// Subtitle conversion (SRT/ASS -> WebVTT) for the in-app player.

var (
	reSrtTime = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)
	reAssTag  = regexp.MustCompile(`\{[^}]*\}`)
)

// ToVTT converts subtitles to WebVTT based on their content/extension.
func ToVTT(body []byte, name string) []byte {
	text := strings.ReplaceAll(string(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))), "\r\n", "\n")
	switch {
	case strings.HasPrefix(strings.TrimSpace(text), "WEBVTT"):
		return []byte(text)
	case strings.Contains(text, "[Script Info]") || strings.HasSuffix(name, ".ass") || strings.HasSuffix(name, ".ssa"):
		return assToVTT(text)
	default:
		return []byte("WEBVTT\n\n" + reSrtTime.ReplaceAllString(text, "$1.$2"))
	}
}

func assTime(t string) string {
	// h:mm:ss.cc -> hh:mm:ss.mmm
	parts := strings.Split(strings.TrimSpace(t), ":")
	if len(parts) != 3 {
		return "00:00:00.000"
	}
	h := parts[0]
	if len(h) < 2 {
		h = "0" + h
	}
	sec := parts[2]
	if i := strings.IndexByte(sec, '.'); i >= 0 {
		cs := sec[i+1:]
		for len(cs) < 3 {
			cs += "0"
		}
		sec = sec[:i] + "." + cs[:3]
	} else {
		sec += ".000"
	}
	return h + ":" + parts[1] + ":" + sec
}

func assToVTT(text string) []byte {
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	format := []string{}
	inEvents := false
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "[") {
			inEvents = strings.EqualFold(l, "[Events]")
			continue
		}
		if !inEvents {
			continue
		}
		if strings.HasPrefix(l, "Format:") {
			for _, f := range strings.Split(strings.TrimPrefix(l, "Format:"), ",") {
				format = append(format, strings.ToLower(strings.TrimSpace(f)))
			}
			continue
		}
		if !strings.HasPrefix(l, "Dialogue:") || len(format) == 0 {
			continue
		}
		fields := strings.SplitN(strings.TrimPrefix(l, "Dialogue:"), ",", len(format))
		if len(fields) < len(format) {
			continue
		}
		get := func(name string) string {
			for i, f := range format {
				if f == name {
					return strings.TrimSpace(fields[i])
				}
			}
			return ""
		}
		txt := fields[len(format)-1]
		txt = reAssTag.ReplaceAllString(txt, "")
		txt = strings.NewReplacer(`\N`, "\n", `\n`, "\n", `\h`, " ").Replace(txt)
		if strings.TrimSpace(txt) == "" {
			continue
		}
		out.WriteString(assTime(get("start")) + " --> " + assTime(get("end")) + "\n" + strings.TrimSpace(txt) + "\n\n")
	}
	return []byte(out.String())
}
