package torrent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/util"
)

// Single episodes, many of them of titles ending in a number, which the old
// range check took for batches (so the auto downloader skipped them).
func TestEnrichEpisodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		ep   int
	}{
		{"[SubsPlease] Steins;Gate 0 - 05 (1080p) [5B1E3C2A].mkv", 5},
		{"[SubsPlease] Kaiju No. 8 - 05 (1080p) [A1B2C3D4].mkv", 5},
		{"[SubsPlease] Spy x Family Season 3 - 05 (1080p) [0F1E2D3C].mkv", 5},
		{"[SubsPlease] Kaijuu 8-gou Season 2 - 05 (1080p) [9A8B7C6D].mkv", 5},
		{"[SubsPlease] Kono Subarashii Sekai ni Shukufuku wo! 3 - 05 (1080p) [1234ABCD].mkv", 5},
		{"[SubsPlease] Tensei shitara Slime Datta Ken 3 - 05 (1080p) [ABCD1234].mkv", 5},
		{"[SubsPlease] Yuru Camp Season 3 - 05 (1080p) [DEADBEEF].mkv", 5},
		{"[SubsPlease] Kaiju No. 8 - 12 [END] (1080p)", 12},
		{"[SubsPlease] Mob Psycho 100 III - 05 (1080p) [ABCD1234].mkv", 5},
		{"[SubsPlease] 86 - Eighty Six - 05 (1080p) [ABCD1234].mkv", 5},
		{"[SubsPlease] Megalobox 2 - Nomad - 05 (1080p)", 5},
		{"[SubsPlease] 2.5-jigen no Ririsa - 05 (1080p) [ABCD1234].mkv", 5},
		{"[SubsPlease] Mushoku Tensei S2 - 05 (1080p) [ABCD1234].mkv", 5},
		{"[SubsPlease] Spy x Family Season 3 - 05v2 (1080p) [ABCD1234].mkv", 5},
		{"[Erai-raws] Tensei shitara Slime Datta Ken 3rd Season - 05 [1080p][Multiple Subtitle][ABCD1234].mkv", 5},
		{"[Erai-raws] Kaiju No. 8 - 05 [1080p][Multiple Subtitle][ENG][POR-BR][SPA-LA][SPA][ARA][FRE][GER][ITA][RUS]", 5},
		{"[SubsPlease] Sousou no Frieren - 12 (1080p) [ABCD1234].mkv", 12},
		{"[SubsPlease] One Piece - 1100 (1080p) [ABCD1234].mkv", 1100},
		{"[DKB] Kaiju No. 8 - S01E05 [1080p][HEVC x265 10bit][Multi-Subs]", 5},
		{"Kaiju.No.8.S01E05.1080p.WEB.H264-SKYANiME", 5},
		{"[ToonsHub] Jujutsu Kaisen S02E23 1080p CR WEB-DL AAC2.0 H.264 (Multi-Subs)", 23},
		{"[Yameii] Kaiju No. 8 - S01E05 [English Dub] [CR WEB-DL 1080p] [ABCD1234]", 5},
		{"[Group] Steins;Gate 0 - 05 (1080p) [AAC 2.0-5.1]", 5},
	} {
		r := &SearchResult{Name: tc.name}
		enrich(r)
		if r.IsBatch || r.EpisodeNumber != tc.ep {
			t.Errorf("%q: batch=%v episode=%d, want episode %d", tc.name, r.IsBatch, r.EpisodeNumber, tc.ep)
		}
	}
}

func TestEnrichBatches(t *testing.T) {
	for _, name := range []string{
		"[Group] Show (01-12) [1080p]",
		"Show - 01~12",
		"Show S01 1080p BD (Batch)",
		"Show Complete",
		"[SubsPlease] Kaiju No. 8 (01-12) (1080p) [Batch]",
		"[SubsPlease] Steins;Gate 0 (01-23) (1080p) [Batch]",
		"[ASW] Dungeon Meshi - 01-24 [1080p HEVC][Batch]",
		"[Anime Time] Dungeon Meshi - 01 ~ 24 [1080p][HEVC 10bit x265][AAC][Eng Sub] (Batch)",
		"[EMBER] Dungeon Meshi (2024) (Season 1) [1080p] [Dual Audio HEVC WEBRip] (Delicious in Dungeon)",
		"[Judas] Frieren (Season 1) [1080p][HEVC x265 10bit][Dual-Audio] (Batch)",
		"[Judas] Mushoku Tensei S2 - 01-12 [1080p][HEVC x265 10bit][Multi-Subs] (Batch)",
		"[Moozzi2] Sousou no Frieren [ 01~28 ] [ BD 1080p ]",
		"Frieren - Beyond Journey's End - 01-28 [1080p] Complete",
		"Kaiju No. 8 S01 1080p WEBRip DD+ x265-EMBER",
		// The name parser finds a single episode in these two.
		"[Erai-raws] Spy x Family Season 3 - 01 ~ 12 [1080p][Multiple Subtitle]",
		"Kaiju.No.8.S01E01-E12.1080p.WEB.H264-SKYANiME",
		"[Erai-raws] Kaijuu 8-gou - 01 ~ 12 [1080p][Multiple Subtitle]",
		"[SubsPlease] Gintama - 01-03 (1080p)",
		"[Group] One Piece - 1000 ~ 1100 [1080p]",
		"[Group] Sousou no Frieren Vol.1-7 [BD 1080p]",
		"[Group] Show Season 1-2 [1080p]",
	} {
		r := &SearchResult{Name: name}
		enrich(r)
		if !r.IsBatch {
			t.Errorf("%q: not detected as a batch (episode %d)", name, r.EpisodeNumber)
		}
	}
}

// "10.5" is a recap: grabbing it as episode 10 would mark 10 as done.
func TestEnrichFractionalEpisode(t *testing.T) {
	for _, name := range []string{
		"[SubsPlease] Sousou no Frieren - 10.5 (1080p) [ABCD1234].mkv",
		"[SubsPlease] Tensei shitara Slime Datta Ken 3 - 48.5 (1080p) [ABCD1234].mkv",
	} {
		r := &SearchResult{Name: name}
		enrich(r)
		if r.EpisodeNumber != -1 || r.IsBatch {
			t.Errorf("%q: episode %d batch=%v, want -1 and not a batch", name, r.EpisodeNumber, r.IsBatch)
		}
	}
	for in, want := range map[string]int{"05": 5, "12.0": 12, "10.5": -1, "": -1, "x": -1} {
		if got := episodeNumber(in); got != want {
			t.Errorf("episodeNumber(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSearchText(t *testing.T) {
	for in, want := range map[string]string{
		"Ore dake Level Up na Ken: Season 2 -Arise from the Shadow-": "Ore dake Level Up na Ken Season 2 Arise from the Shadow-",
		"Kono Subarashii Sekai ni Shukufuku wo! 3":                   "Kono Subarashii Sekai ni Shukufuku wo 3",
		"Bleach: Sennen Kessen-hen - Soukoku-tan":                    "Bleach Sennen Kessen-hen Soukoku-tan",
		"Hunter x Hunter (2011)":                                     "Hunter x Hunter 2011",
	} {
		if got := searchText(in); got != want {
			t.Errorf("searchText(%q) = %q, want %q", in, got, want)
		}
	}
}

// useTestHTTP lets the providers reach srv: the shared client refuses
// loopback addresses.
func useTestHTTP(t *testing.T, srv *httptest.Server) {
	old := util.HTTP
	util.HTTP = srv.Client()
	t.Cleanup(func() { util.HTTP = old })
}

const nyaaFeed = `<?xml version="1.0" encoding="utf-8"?>
<rss xmlns:atom="http://www.w3.org/2005/Atom" xmlns:nyaa="https://nyaa.si/xmlns/nyaa" version="2.0">
<channel>
<title>Nyaa - Torrent File RSS</title>
<item>
<title>[SubsPlease] Steins;Gate 0 - 05 (1080p) [5B1E3C2A].mkv</title>
<link>https://nyaa.si/download/1.torrent</link>
<guid isPermaLink="true">https://nyaa.si/view/1</guid>
<pubDate>Mon, 07 Oct 2024 14:01:23 -0000</pubDate>
<nyaa:seeders>120</nyaa:seeders>
<nyaa:leechers>3</nyaa:leechers>
<nyaa:downloads>1000</nyaa:downloads>
<nyaa:infoHash>0123456789abcdef0123456789abcdef01234567</nyaa:infoHash>
<nyaa:size>1.4 GiB</nyaa:size>
<nyaa:trusted>Yes</nyaa:trusted>
<nyaa:remake>No</nyaa:remake>
</item>
<item>
<title>[SubsPlease] Steins;Gate 0 - 04 (1080p) [6C2F4D3B].mkv</title>
<link>https://nyaa.si/download/2.torrent</link>
<guid isPermaLink="true">https://nyaa.si/view/2</guid>
<nyaa:seeders>90</nyaa:seeders>
<nyaa:infoHash>1123456789abcdef0123456789abcdef01234567</nyaa:infoHash>
<nyaa:size>1.3 GiB</nyaa:size>
</item>
<item>
<title>[SubsPlease] Steins;Gate 0 (01-23) (1080p) [Batch]</title>
<link>https://nyaa.si/download/3.torrent</link>
<guid isPermaLink="true">https://nyaa.si/view/3</guid>
<nyaa:seeders>300</nyaa:seeders>
<nyaa:infoHash>2123456789abcdef0123456789abcdef01234567</nyaa:infoHash>
<nyaa:size>30.1 GiB</nyaa:size>
</item>
</channel>
</rss>`

func TestNyaa(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	body := nyaaFeed
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("q"))
		b := body
		mu.Unlock()
		_, _ = io.WriteString(w, b)
	}))
	defer srv.Close()
	useTestHTTP(t, srv)
	n := &Nyaa{Base: srv.URL}
	ctx := context.Background()

	res, err := n.Search(ctx, "Steins;Gate 0")
	if err != nil || len(res) != 3 {
		t.Fatalf("search: %d results, %v", len(res), err)
	}
	r := res[0]
	if r.EpisodeNumber != 5 || r.IsBatch || r.Seeders != 120 || !r.Trusted || r.Resolution != "1080p" || r.ReleaseGroup != "SubsPlease" ||
		!strings.HasPrefix(r.MagnetLink, "magnet:?xt=urn:btih:0123") || r.Size == 0 || r.Date == "" {
		t.Fatalf("episode 5: %+v", r)
	}
	if !res[2].IsBatch {
		t.Fatalf("batch not detected: %+v", res[2])
	}

	media := &anilist.Media{Format: "TV", Title: anilist.Title{Romaji: "Steins;Gate 0", English: "Steins;Gate 0"}}
	ep, err := n.SmartSearch(ctx, SmartQuery{Media: media, Episode: 5, Resolution: "1080"})
	if err != nil {
		t.Fatal(err)
	}
	// Episode 4 is dropped; the batch holds episode 5 too.
	if len(ep) != 2 || ep[0].EpisodeNumber != -1 || ep[1].EpisodeNumber != 5 {
		t.Fatalf("smart search episode 5: %+v", ep)
	}
	if q := queries[len(queries)-1]; q != "Steins;Gate 0 05 1080" {
		t.Fatalf("query %q", q)
	}
	batch, err := n.SmartSearch(ctx, SmartQuery{Media: media, Batch: true})
	if err != nil || len(batch) != 1 || !batch[0].IsBatch {
		t.Fatalf("smart search batch: %+v %v", batch, err)
	}

	// Empty and broken answers: no results or an error, never a panic.
	for _, b := range []string{`<rss><channel></channel></rss>`, ``, `not xml`} {
		mu.Lock()
		body = b
		mu.Unlock()
		res, err := n.Search(ctx, "x")
		if len(res) != 0 || (b != `<rss><channel></channel></rss>` && err == nil) {
			t.Fatalf("body %q: %v %v", b, res, err)
		}
		if _, err := n.SmartSearch(ctx, SmartQuery{Media: media, Episode: 1}); b == `<rss><channel></channel></rss>` && err != nil {
			t.Fatalf("empty feed: %v", err)
		}
	}
}

func toshoItemJSON(title string, seeders int, files int) map[string]any {
	return map[string]any{
		"title": title, "link": "https://animetosho.org/view/" + strings.ReplaceAll(title, " ", "-"), "torrent_url": "https://animetosho.org/t.torrent",
		"magnet_uri": "magnet:?xt=urn:btih:" + title, "info_hash": title, "seeders": seeders, "leechers": 1,
		"total_size": 1 << 30, "timestamp": 1700000000, "num_files": files,
	}
}

func TestAnimeToshoSmartSearch(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	newest := []map[string]any{ // the "aid" feed: only the latest releases
		toshoItemJSON("[SubsPlease] Kaiju No. 8 - 12 (1080p)", 50, 1),
		toshoItemJSON("[SubsPlease] Kaiju No. 8 - 11 (1080p)", 40, 1),
		toshoItemJSON("[Erai-raws] Kaiju No. 8 - 11 [1080p][Multiple Subtitle]", 30, 2),
		toshoItemJSON("[SubsPlease] Kaiju No. 8 (01-12) (1080p) [Batch]", 20, 12),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		mu.Lock()
		requests = append(requests, q.Encode())
		mu.Unlock()
		switch {
		case q.Get("aid") == "18287":
			_ = json.NewEncoder(w).Encode(newest)
		case q.Get("aid") == "1":
			_, _ = io.WriteString(w, `[]`)
		case q.Get("aid") == "2":
			_, _ = io.WriteString(w, `null`)
		case q.Get("eid") == "300":
			_ = json.NewEncoder(w).Encode([]map[string]any{toshoItemJSON("[SubsPlease] Kaiju No. 8 - 03 (720p)", 5, 1)})
		case strings.Contains(q.Get("q"), "03"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				toshoItemJSON("[SubsPlease] Kaiju No. 8 - 03 (1080p)", 10, 1),
				toshoItemJSON("[SubsPlease] Kaiju No. 8 - 13 (1080p)", 10, 1),
			})
		default:
			_, _ = io.WriteString(w, `[]`)
		}
	}))
	defer srv.Close()
	useTestHTTP(t, srv)
	a := &AnimeTosho{Base: srv.URL}
	ctx := context.Background()
	media := &anilist.Media{Format: "TV", Title: anilist.Title{Romaji: "Kaiju No. 8", English: "Kaiju No. 8"}}
	reset := func() {
		mu.Lock()
		requests = nil
		mu.Unlock()
	}

	// A recent episode is on the AniDB feed: no title search needed.
	reset()
	res, err := a.SmartSearch(ctx, SmartQuery{Media: media, Episode: 11, Resolution: "1080", AnidbAID: 18287})
	if err != nil || len(res) != 3 || len(requests) != 1 {
		t.Fatalf("episode 11: %d results %v, requests %v", len(res), err, requests)
	}
	for _, r := range res {
		if r.EpisodeNumber != 11 && !r.IsBatch {
			t.Fatalf("episode 11: unexpected %+v", r)
		}
	}
	// Two files with an episode's name is still that episode.
	if res[1].Name != "[Erai-raws] Kaiju No. 8 - 11 [1080p][Multiple Subtitle]" || res[1].IsBatch {
		t.Fatalf("multi-file episode: %+v", res[1])
	}

	// An older episode isn't on it (only the batch matches): the title
	// search runs too, and the batch is kept.
	reset()
	res, err = a.SmartSearch(ctx, SmartQuery{Media: media, Episode: 3, Resolution: "1080", AnidbAID: 18287})
	if err != nil || len(res) != 2 || !res[0].IsBatch || res[1].EpisodeNumber != 3 || len(requests) != 2 {
		t.Fatalf("episode 3: %+v %v (requests %v)", res, err, requests)
	}

	// Batches: found on the feed.
	reset()
	res, err = a.SmartSearch(ctx, SmartQuery{Media: media, Batch: true, AnidbAID: 18287})
	if err != nil || len(res) != 1 || !res[0].IsBatch || len(requests) != 1 {
		t.Fatalf("batch: %+v %v", res, err)
	}

	// Empty or null feeds fall back to the title search.
	for _, aid := range []int{1, 2} {
		reset()
		res, err = a.SmartSearch(ctx, SmartQuery{Media: media, Episode: 3, AnidbAID: aid})
		if err != nil || len(res) != 1 || res[0].EpisodeNumber != 3 || len(requests) < 2 {
			t.Fatalf("aid %d: %+v %v (requests %v)", aid, res, err, requests)
		}
	}

	// Episode id lookup whose releases don't have the resolution asked for.
	reset()
	res, err = a.SmartSearch(ctx, SmartQuery{Media: media, Episode: 3, Resolution: "1080", AnidbEID: 300})
	if err != nil || len(res) != 1 || res[0].Resolution != "1080p" {
		t.Fatalf("eid: %+v %v (requests %v)", res, err, requests)
	}
	reset()
	res, err = a.SmartSearch(ctx, SmartQuery{Media: media, Episode: 3, Resolution: "720", AnidbEID: 300})
	if err != nil || len(res) != 1 || res[0].Resolution != "720p" || len(requests) != 1 {
		t.Fatalf("eid 720p: %+v %v (requests %v)", res, err, requests)
	}
}
