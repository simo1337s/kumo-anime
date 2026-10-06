package torrent

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/5rahim/habari"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/util"
)

// SearchResult is a torrent found by a provider.
type SearchResult struct {
	Provider      string `json:"provider"`
	Name          string `json:"name"`
	Link          string `json:"link"` // page or .torrent link
	DownloadURL   string `json:"downloadUrl"`
	MagnetLink    string `json:"magnetLink"`
	InfoHash      string `json:"infoHash"`
	Size          int64  `json:"size"`
	FormattedSize string `json:"formattedSize"`
	Seeders       int    `json:"seeders"`
	Leechers      int    `json:"leechers"`
	DownloadCount int    `json:"downloadCount"`
	Date          string `json:"date"`
	Resolution    string `json:"resolution"`
	ReleaseGroup  string `json:"releaseGroup"`
	EpisodeNumber int    `json:"episodeNumber"` // -1 unknown
	IsBatch       bool   `json:"isBatch"`
	IsBestRelease bool   `json:"isBestRelease"`
	Confirmed     bool   `json:"confirmed"`
	Trusted       bool   `json:"trusted"`
	Remake        bool   `json:"remake"`
	Dub           bool   `json:"dub"`
}

// SmartQuery is what the UI asks for when searching from an anime page.
type SmartQuery struct {
	Media      *anilist.Media
	Query      string // free text overrides the generated one
	Episode    int
	Batch      bool
	Resolution string
	AnidbAID   int
	AnidbEID   int
}

// Provider searches for torrents.
type Provider interface {
	ID() string
	Name() string
	Search(ctx context.Context, query string) ([]*SearchResult, error)
	SmartSearch(ctx context.Context, q SmartQuery) ([]*SearchResult, error)
	// Magnet resolves the magnet link of a result (some providers only
	// expose it on the torrent page).
	Magnet(ctx context.Context, r *SearchResult) (string, error)
}

var Trackers = []string{
	"http://nyaa.tracker.wf:7777/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.torrent.eu.org:451/announce",
}

func MagnetFromHash(hash, name string) string {
	v := url.Values{}
	v.Set("dn", name)
	for _, t := range Trackers {
		v.Add("tr", t)
	}
	return "magnet:?xt=urn:btih:" + hash + "&" + v.Encode()
}

// Enrich parses the torrent name to fill resolution/group/episode/batch when
// the provider didn't set them.
func Enrich(r *SearchResult) {
	md := habari.Parse(r.Name)
	if r.Resolution == "" {
		r.Resolution = normRes(md.VideoResolution)
	} else {
		r.Resolution = normRes(r.Resolution)
	}
	if r.ReleaseGroup == "" {
		r.ReleaseGroup = md.ReleaseGroup
	}
	if r.EpisodeNumber <= 0 {
		r.EpisodeNumber = -1
		if len(md.EpisodeNumber) == 1 {
			r.EpisodeNumber = atoi(md.EpisodeNumber[0])
		}
	}
	if len(md.EpisodeNumber) > 1 {
		r.IsBatch = true
	}
	lower := strings.ToLower(r.Name)
	if strings.Contains(lower, "batch") || strings.Contains(lower, "complete") || reRange.MatchString(r.Name) {
		r.IsBatch = true
	}
	if len(md.EpisodeNumber) == 0 && len(md.SeasonNumber) > 0 {
		r.IsBatch = true
	}
	for _, a := range md.AudioTerm {
		if strings.Contains(strings.ToLower(a), "dual") {
			r.Dub = true
		}
	}
	if strings.Contains(lower, "dub") || strings.Contains(lower, "dual audio") || strings.Contains(lower, "multi-audio") {
		r.Dub = true
	}
	if r.FormattedSize == "" && r.Size > 0 {
		r.FormattedSize = humanSize(r.Size)
	}
}

func enrich(r *SearchResult) {
	r.EpisodeNumber = -1
	Enrich(r)
}

var reRange = regexp.MustCompile(`\b\d{1,4}\s?[-~]\s?\d{1,4}\b`)

func normRes(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "2160") || strings.Contains(s, "4k"):
		return "2160p"
	case strings.Contains(s, "1080"):
		return "1080p"
	case strings.Contains(s, "720"):
		return "720p"
	case strings.Contains(s, "480"):
		return "480p"
	}
	return s
}

func atoi(s string) int {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return -1
	}
	return n
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

var reSize = regexp.MustCompile(`(?i)([\d.]+)\s*([KMGT]i?B)`)

func parseSize(s string) int64 {
	m := reSize.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	mult := map[byte]float64{'K': 1 << 10, 'M': 1 << 20, 'G': 1 << 30, 'T': 1 << 40}[strings.ToUpper(m[2])[0]]
	return int64(f * mult)
}

// smartQueries builds the text queries used by smart search.
func smartQueries(q SmartQuery) []string {
	if q.Query != "" {
		return []string{q.Query}
	}
	var titles []string
	for _, t := range []string{q.Media.Title.Romaji, q.Media.Title.English} {
		t = strings.TrimSpace(t)
		if t != "" && !containsFold(titles, t) {
			titles = append(titles, t)
		}
	}
	var out []string
	for _, t := range titles {
		// Nyaa's search doesn't like some punctuation.
		t = strings.NewReplacer(":", "", "!", "", "?", "", "\"", "", "(", "", ")", "").Replace(t)
		s := t
		if !q.Batch && q.Episode > 0 && q.Media.Format != "MOVIE" {
			s = fmt.Sprintf("%s %02d", t, q.Episode)
		} else if q.Batch {
			s = t + " batch"
		}
		if q.Resolution != "" {
			s += " " + q.Resolution
		}
		out = append(out, s)
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// filterSmart drops results that are clearly for another episode.
func filterSmart(results []*SearchResult, q SmartQuery) []*SearchResult {
	if q.Query != "" {
		return results
	}
	var out []*SearchResult
	for _, r := range results {
		if q.Batch {
			if r.IsBatch || q.Media.Format == "MOVIE" {
				out = append(out, r)
			}
			continue
		}
		if q.Episode > 0 && r.EpisodeNumber >= 0 && r.EpisodeNumber != q.Episode && !r.IsBatch {
			continue
		}
		out = append(out, r)
	}
	return out
}

func dedupeSort(results []*SearchResult) []*SearchResult {
	seen := map[string]bool{}
	var out []*SearchResult
	for _, r := range results {
		key := strings.ToLower(r.InfoHash)
		if key == "" {
			key = r.Link
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seeders > out[j].Seeders })
	return out
}

// ---------------------------------------------------------------------------
// Nyaa

type Nyaa struct{ Base string }

func (n *Nyaa) ID() string   { return "nyaa" }
func (n *Nyaa) Name() string { return "Nyaa" }

type nyaaRSS struct {
	Channel struct {
		Items []struct {
			Title     string `xml:"title"`
			Link      string `xml:"link"`
			GUID      string `xml:"guid"`
			PubDate   string `xml:"pubDate"`
			Seeders   int    `xml:"seeders"`
			Leechers  int    `xml:"leechers"`
			Downloads int    `xml:"downloads"`
			InfoHash  string `xml:"infoHash"`
			Size      string `xml:"size"`
			Trusted   string `xml:"trusted"`
			Remake    string `xml:"remake"`
		} `xml:"item"`
	} `xml:"channel"`
}

func (n *Nyaa) base() string {
	if n.Base != "" {
		return n.Base
	}
	return "https://nyaa.si"
}

func (n *Nyaa) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	u := n.base() + "/?page=rss&c=1_0&f=0&q=" + url.QueryEscape(query)
	b, err := util.GetBytes(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	var rss nyaaRSS
	if err := xml.Unmarshal(b, &rss); err != nil {
		return nil, fmt.Errorf("nyaa: %w", err)
	}
	var out []*SearchResult
	for _, it := range rss.Channel.Items {
		r := &SearchResult{
			Provider: "nyaa", Name: it.Title, Link: it.GUID, DownloadURL: it.Link, InfoHash: it.InfoHash,
			Seeders: it.Seeders, Leechers: it.Leechers, DownloadCount: it.Downloads, FormattedSize: it.Size,
			Size: parseSize(it.Size), Trusted: it.Trusted == "Yes", Remake: it.Remake == "Yes",
		}
		if t, err := time.Parse(time.RFC1123Z, it.PubDate); err == nil {
			r.Date = t.Format(time.RFC3339)
		}
		if it.InfoHash != "" {
			r.MagnetLink = MagnetFromHash(it.InfoHash, it.Title)
		}
		enrich(r)
		out = append(out, r)
	}
	return out, nil
}

func (n *Nyaa) SmartSearch(ctx context.Context, q SmartQuery) ([]*SearchResult, error) {
	var all []*SearchResult
	var firstErr error
	for _, s := range smartQueries(q) {
		res, err := n.Search(ctx, s)
		if err != nil {
			firstErr = err
			continue
		}
		all = append(all, res...)
	}
	if len(all) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return dedupeSort(filterSmart(all, q)), nil
}

func (n *Nyaa) Magnet(_ context.Context, r *SearchResult) (string, error) {
	if r.MagnetLink != "" {
		return r.MagnetLink, nil
	}
	if r.InfoHash != "" {
		return MagnetFromHash(r.InfoHash, r.Name), nil
	}
	return r.DownloadURL, nil
}

// ---------------------------------------------------------------------------
// AnimeTosho

type AnimeTosho struct{}

func (a *AnimeTosho) ID() string   { return "animetosho" }
func (a *AnimeTosho) Name() string { return "AnimeTosho" }

type toshoItem struct {
	Title      string `json:"title"`
	Link       string `json:"link"`
	TorrentURL string `json:"torrent_url"`
	MagnetURI  string `json:"magnet_uri"`
	InfoHash   string `json:"info_hash"`
	Seeders    int    `json:"seeders"`
	Leechers   int    `json:"leechers"`
	TotalSize  int64  `json:"total_size"`
	Timestamp  int64  `json:"timestamp"`
	NumFiles   int    `json:"num_files"`
	AnidbEID   int    `json:"anidb_eid"`
}

func (a *AnimeTosho) fetch(ctx context.Context, params url.Values) ([]*SearchResult, error) {
	var items []toshoItem
	if err := util.GetJSON(ctx, "https://feed.animetosho.org/json?"+params.Encode(), nil, &items); err != nil {
		return nil, err
	}
	var out []*SearchResult
	for _, it := range items {
		r := &SearchResult{
			Provider: "animetosho", Name: it.Title, Link: it.Link, DownloadURL: it.TorrentURL, MagnetLink: it.MagnetURI,
			InfoHash: it.InfoHash, Seeders: it.Seeders, Leechers: it.Leechers, Size: it.TotalSize,
		}
		if r.Seeders > 30000 { // tosho reports unknown as a huge number
			r.Seeders = 0
		}
		if it.Timestamp > 0 {
			r.Date = time.Unix(it.Timestamp, 0).Format(time.RFC3339)
		}
		enrich(r)
		if it.NumFiles > 1 {
			r.IsBatch = true
		}
		out = append(out, r)
	}
	return out, nil
}

func (a *AnimeTosho) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	return a.fetch(ctx, url.Values{"q": {query}, "qx": {"1"}})
}

func (a *AnimeTosho) SmartSearch(ctx context.Context, q SmartQuery) ([]*SearchResult, error) {
	// Exact episode lookups via AniDB ids when available.
	if q.Query == "" && q.AnidbEID > 0 && !q.Batch {
		res, err := a.fetch(ctx, url.Values{"eid": {strconv.Itoa(q.AnidbEID)}})
		if err == nil && len(res) > 0 {
			return dedupeSort(filterRes(res, q.Resolution)), nil
		}
	}
	if q.Query == "" && q.AnidbAID > 0 {
		res, err := a.fetch(ctx, url.Values{"aid": {strconv.Itoa(q.AnidbAID)}})
		if err == nil && len(res) > 0 {
			return dedupeSort(filterRes(filterSmart(res, q), q.Resolution)), nil
		}
	}
	var all []*SearchResult
	var firstErr error
	for _, s := range smartQueries(q) {
		res, err := a.Search(ctx, s)
		if err != nil {
			firstErr = err
			continue
		}
		all = append(all, res...)
	}
	if len(all) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return dedupeSort(filterSmart(all, q)), nil
}

func filterRes(res []*SearchResult, resolution string) []*SearchResult {
	if resolution == "" {
		return res
	}
	var out []*SearchResult
	for _, r := range res {
		if strings.Contains(r.Resolution, strings.TrimSuffix(resolution, "p")) {
			out = append(out, r)
		}
	}
	return out
}

func (a *AnimeTosho) Magnet(_ context.Context, r *SearchResult) (string, error) {
	if r.MagnetLink != "" {
		return r.MagnetLink, nil
	}
	if r.InfoHash != "" {
		return MagnetFromHash(r.InfoHash, r.Name), nil
	}
	return r.DownloadURL, nil
}
