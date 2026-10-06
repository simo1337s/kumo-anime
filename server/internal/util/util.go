// Package util contains small helpers shared across packages.
package util

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/adrg/strutil"
	"github.com/adrg/strutil/metrics"
)

const UserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"

// HTTP is the shared client for public services (AniList metadata, ani.zip,
// AniSkip, indexers, extension downloads). It refuses to connect to this
// computer or the LAN, so URLs coming from extensions or LAN clients can't
// be used to reach local services.
var HTTP = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           PublicDialContext(15 * time.Second),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	},
}

// GetJSON performs a GET request and decodes the JSON response.
func GetJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// GetBytes performs a GET request and returns the body.
func GetBytes(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// PostJSON sends a JSON body and decodes the JSON response.
func PostJSON(ctx context.Context, url string, headers map[string]string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---------------------------------------------------------------------------
// String matching

var (
	reBrackets   = regexp.MustCompile(`[\[\(\{【].*?[\]\)\}】]`)
	reNonAlnum   = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	reSeasonWord = regexp.MustCompile(`(?i)\b(season|s)\s*0*(\d+)\b`)
	reOrdinal    = regexp.MustCompile(`(?i)\b(\d+)(st|nd|rd|th)\s+season\b`)
)

// NormalizeTitle lowercases, strips brackets/punctuation and collapses
// whitespace so titles can be compared.
func NormalizeTitle(s string) string {
	s = strings.ToLower(s)
	s = reBrackets.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&", " and ", "×", "x", "½", "1/2", "'", "", "’", "", "`", "").Replace(s)
	s = reOrdinal.ReplaceAllString(s, "season $1")
	s = reSeasonWord.ReplaceAllString(s, "season $2")
	s = reNonAlnum.ReplaceAllString(s, " ")
	s = removeDiacritics(s)
	return strings.Join(strings.Fields(s), " ")
}

func removeDiacritics(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'ā', 'á', 'à', 'â', 'ä':
			r = 'a'
		case 'ē', 'é', 'è', 'ê', 'ë':
			r = 'e'
		case 'ī', 'í', 'ì', 'î', 'ï':
			r = 'i'
		case 'ō', 'ó', 'ò', 'ô', 'ö':
			r = 'o'
		case 'ū', 'ú', 'ù', 'û', 'ü':
			r = 'u'
		}
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

var (
	dice    = metrics.NewSorensenDice()
	jw      = metrics.NewJaroWinkler()
	leven   = metrics.NewLevenshtein()
	ngramSz = 2
)

func init() {
	dice.NgramSize = ngramSz
	dice.CaseSensitive = false
	jw.CaseSensitive = false
	leven.CaseSensitive = false
}

// Similarity returns a 0-1 similarity score between two titles, combining a
// few metrics so both short and long titles compare sensibly.
func Similarity(a, b string) float64 {
	a, b = NormalizeTitle(a), NormalizeTitle(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	d := strutil.Similarity(a, b, dice)
	j := strutil.Similarity(a, b, jw)
	l := strutil.Similarity(a, b, leven)
	score := d*0.5 + j*0.25 + l*0.25
	// Containment bonus: "one piece" vs "one piece film red" etc.
	if strings.HasPrefix(b, a+" ") || strings.HasPrefix(a, b+" ") {
		score = max(score, 0.75)
	}
	return score
}

// ---------------------------------------------------------------------------
// Misc

// LookPath resolves a program name or absolute path to an executable.
func LookPath(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	if strings.ContainsRune(p, os.PathSeparator) || strings.ContainsRune(p, '/') {
		return lookAbs(p)
	}
	found, err := exec.LookPath(p)
	return found, err == nil
}

// SanitizeFilename replaces characters that are invalid in file names.
func SanitizeFilename(s string) string {
	// Characters that are invalid on Linux or on NTFS/exFAT media drives.
	// Colons become a look-alike so titles like "Re:Zero" stay readable.
	s = strings.Map(func(r rune) rune {
		switch r {
		case ':':
			return '：'
		case '/', '\\', '*', '?', '"', '<', '>', '|', 0:
			return '_'
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	s = strings.Trim(s, ".")
	if len(s) > 180 {
		s = s[:180]
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

// IsSubPath reports whether child is inside (or equal to) parent.
func IsSubPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// FirstNonEmpty returns the first non empty string.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Detach starts a command that outlives the request that launched it, on
// its own: Ctrl+C on Kumo's terminal doesn't also close it.
func Detach(name string, args ...string) error {
	return start(exec.Command(name, args...))
}

// Open opens a folder in the file manager, or a web address in the browser.
func Open(target string) error {
	return start(openCommand(target))
}

func start(cmd *exec.Cmd) error {
	cmd.Stdout = nil
	cmd.Stderr = nil
	detachAttrs(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func isWebAddress(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}
