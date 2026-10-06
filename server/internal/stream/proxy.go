package stream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/simo1337s/animetest/server/internal/util"
)

// The proxy lets the in-app player load streams that need special headers
// (Referer/Origin) or lack CORS. HLS playlists are rewritten so every
// segment/key/sub-playlist also goes through the proxy. It refuses to
// connect to private addresses so it can't be used to reach the LAN.

func isPrivate(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()
}

var proxyClient = &http.Client{
	Timeout: 0, // streams can be long
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout: 15 * time.Second,
			Control: func(network, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if ip := net.ParseIP(host); ip != nil && isPrivate(ip) {
					return errors.New("refusing to proxy a private address")
				}
				return nil
			},
		}).DialContext,
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

// ServeProxy implements GET /api/proxy.
func ServeProxy(w http.ResponseWriter, r *http.Request) {
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
	isPlaylist := strings.Contains(strings.ToLower(ctype), "mpegurl") || strings.HasSuffix(path, ".m3u8")
	if !isPlaylist && resp.ContentLength >= 0 && resp.ContentLength < 4<<20 && format == "" {
		// Some hosts serve playlists as text/plain or octet-stream.
		peek := bufio.NewReader(resp.Body)
		head, _ := peek.Peek(7)
		if string(head) == "#EXTM3U" {
			isPlaylist = true
		}
		resp.Body = io.NopCloser(peek)
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
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
		for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
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

// FetchText is a helper used by the subtitle endpoint.
func FetchText(ctx context.Context, u string, headers map[string]string) ([]byte, error) {
	return util.GetBytes(ctx, u, headers)
}
