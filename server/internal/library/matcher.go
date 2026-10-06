package library

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/util"
)

// Matcher links parsed files to AniList entries.
type Matcher struct {
	platform    *anilist.Platform
	threshold   float64
	outsideList bool

	mu          sync.Mutex
	searchCache map[string][]*anilist.Media
	detailCache map[int]*anilist.Media
}

func NewMatcher(p *anilist.Platform, threshold float64, outsideList bool) *Matcher {
	return &Matcher{
		platform:    p,
		threshold:   threshold,
		outsideList: outsideList,
		searchCache: map[string][]*anilist.Media{},
		detailCache: map[int]*anilist.Media{},
	}
}

type candidateScore struct {
	media *anilist.Media
	score float64
}

type group struct {
	title, folderTitle string
	season, part, year int
	kind               string
	files              []*LocalFile
}

// MatchFiles assigns MediaID/Episode on every file in place. progress is
// called after each group.
func (m *Matcher) MatchFiles(ctx context.Context, files []*LocalFile, progress func(done, total int, title string)) {
	if len(files) == 0 {
		return
	}
	var pool []*anilist.Media
	if coll, err := m.platform.Collection(ctx, "ANIME", false); err == nil {
		for _, e := range coll.Entries() {
			pool = append(pool, e.Media)
		}
	}

	groups := map[string]*group{}
	var order []string
	for _, f := range files {
		p := f.Parsed
		key := fmt.Sprintf("%s|%s|%d|%d", util.NormalizeTitle(p.Title), util.NormalizeTitle(p.FolderTitle), p.Season, p.Part)
		g, ok := groups[key]
		if !ok {
			g = &group{title: p.Title, folderTitle: p.FolderTitle, season: p.Season, part: p.Part, year: p.Year, kind: p.Kind}
			groups[key] = g
			order = append(order, key)
		}
		g.files = append(g.files, f)
	}

	for i, key := range order {
		if ctx.Err() != nil {
			return
		}
		g := groups[key]
		best := m.bestMatch(ctx, g, pool)
		if progress != nil {
			progress(i+1, len(order), util.FirstNonEmpty(g.title, g.folderTitle))
		}
		if best.media == nil || best.score < m.threshold {
			for _, f := range g.files {
				f.MediaID, f.Episode, f.MatchScore = 0, 0, best.score
			}
			continue
		}
		for _, f := range g.files {
			m.assign(ctx, f, best.media, best.score)
		}
	}
}

func (m *Matcher) bestMatch(ctx context.Context, g *group, pool []*anilist.Media) candidateScore {
	queries := buildQueries(g)
	if len(queries) == 0 {
		return candidateScore{}
	}
	best := scoreCandidates(queries, pool, g)
	// Good enough from the user's own list?
	if best.score >= 0.92 || !m.outsideList {
		return best
	}
	// Otherwise search AniList with the most specific queries.
	seen := map[int]bool{}
	var extra []*anilist.Media
	for _, q := range queries[:min(2, len(queries))] {
		for _, r := range m.search(ctx, q.text) {
			if !seen[r.ID] {
				seen[r.ID] = true
				extra = append(extra, r)
			}
		}
	}
	if alt := scoreCandidates(queries, extra, g); alt.score > best.score+0.02 {
		best = alt
	}
	return best
}

func (m *Matcher) search(ctx context.Context, q string) []*anilist.Media {
	key := util.NormalizeTitle(q)
	m.mu.Lock()
	if r, ok := m.searchCache[key]; ok {
		m.mu.Unlock()
		return r
	}
	m.mu.Unlock()
	page, err := m.platform.Search(ctx, anilist.SearchParams{Search: q, PerPage: 10, Type: "ANIME"})
	var res []*anilist.Media
	if err == nil && page != nil {
		res = page.Media
	}
	m.mu.Lock()
	m.searchCache[key] = res
	m.mu.Unlock()
	return res
}

type query struct {
	text   string
	weight float64
}

func ordinal(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return fmt.Sprintf("%dth", n)
	case n%10 == 1:
		return fmt.Sprintf("%dst", n)
	case n%10 == 2:
		return fmt.Sprintf("%dnd", n)
	case n%10 == 3:
		return fmt.Sprintf("%drd", n)
	}
	return fmt.Sprintf("%dth", n)
}

var romanNumerals = []string{"", "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X"}

func buildQueries(g *group) []query {
	var bases []string
	for _, t := range []string{g.title, g.folderTitle} {
		t = strings.TrimSpace(reSeasonAny.ReplaceAllString(t, ""))
		t = strings.Trim(t, " -:")
		if t != "" && !containsFold(bases, t) {
			bases = append(bases, t)
		}
	}
	var out []query
	for _, b := range bases {
		if g.season > 1 {
			out = append(out,
				query{fmt.Sprintf("%s Season %d", b, g.season), 1},
				query{fmt.Sprintf("%s %s Season", b, ordinal(g.season)), 1},
				query{fmt.Sprintf("%s %d", b, g.season), 0.97},
			)
			if g.season < len(romanNumerals) {
				out = append(out, query{fmt.Sprintf("%s %s", b, romanNumerals[g.season]), 0.95})
			}
			if g.part > 0 {
				out = append(out, query{fmt.Sprintf("%s Season %d Part %d", b, g.season, g.part), 1})
			}
			out = append(out, query{b, 0.8})
		} else {
			if g.part > 0 {
				out = append(out, query{fmt.Sprintf("%s Part %d", b, g.part), 1})
			}
			out = append(out, query{b, 1})
		}
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

func scoreCandidates(queries []query, pool []*anilist.Media, g *group) candidateScore {
	var best candidateScore
	titleMarkers, season := fileMarkers(g)
	for _, media := range pool {
		if media == nil {
			continue
		}
		var s float64
		for _, t := range media.AllTitles() {
			penalty := markerPenalty(titleMarkers, season, util.SequelMarkers(t))
			for _, q := range queries {
				if v := util.Similarity(q.text, t)*q.weight - penalty; v > s {
					s = v
				}
			}
		}
		if s == 0 {
			continue
		}
		if g.year > 0 {
			if y := media.Year(); y == g.year {
				s += 0.05
			} else if y > 0 && abs(y-g.year) > 1 {
				s -= 0.05
			}
		}
		switch {
		case g.kind == "special" && (media.Format == "OVA" || media.Format == "SPECIAL" || media.Format == "ONA"):
			s += 0.02
		case g.kind == "main" && (media.Format == "TV" || media.Format == "TV_SHORT"):
			s += 0.01
		}
		if s > best.score {
			best = candidateScore{media, s}
		}
	}
	if best.score > 1 {
		best.score = 1
	}
	return best
}

// fileMarkers returns the sequel markers (util.SequelMarkers) in the
// group's own titles, like the "R2" of "Code Geass Hangyaku no Lelouch R2",
// and the season number its name gave, if any.
func fileMarkers(g *group) (title []string, season string) {
	for _, t := range []string{g.title, g.folderTitle} {
		for _, m := range util.SequelMarkers(reSeasonAny.ReplaceAllString(t, "")) {
			if !slices.Contains(title, m) {
				title = append(title, m)
			}
		}
	}
	if g.season > 1 {
		season = strconv.Itoa(g.season)
	}
	return title, season
}

// markerPenalty lowers the score of a candidate title whose sequel markers
// differ from the file's. Titles that differ only in those look almost the
// same to util.Similarity ("Code Geass: Hangyaku no Lelouch" and its "R2"
// score 0.95), but a file named after a sequel isn't the first season, and
// a file named after the first season is less likely a sequel. Seasons the
// name gave as such ("S2") are scored by the queries, like before.
func markerPenalty(fileTitle []string, season string, candidate []string) float64 {
	for _, m := range fileTitle {
		if !slices.Contains(candidate, m) {
			return 0.5
		}
	}
	for _, m := range candidate {
		if !slices.Contains(fileTitle, m) && m != season {
			return 0.1
		}
	}
	return 0
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// assign sets the media and normalised episode number of a file, resolving
// absolute episode numbers ("One Piece - 1050", "JJK - 30") across seasons.
func (m *Matcher) assign(ctx context.Context, f *LocalFile, media *anilist.Media, score float64) {
	f.MediaID = media.ID
	f.MatchScore = score
	ep := f.Parsed.Episode
	f.AiredEpisode = ep
	total := media.TotalEpisodes()

	switch {
	case f.Kind == "nc":
		f.Episode = 0
		return
	case ep < 0:
		// Movies and single-episode entries.
		if media.Format == "MOVIE" || total == 1 {
			f.Episode = 1
		} else {
			f.Episode = 0
		}
		return
	case ep == 0:
		f.Episode = 0
		if f.Kind == "main" {
			f.Kind = "special"
		}
		return
	}
	f.Episode = ep
	if total > 0 && ep > total && f.Kind == "main" {
		if id, rel, ok := m.resolveAbsolute(ctx, media, ep); ok {
			f.MediaID, f.Episode = id, rel
		}
	}
}

func (m *Matcher) detail(ctx context.Context, id int) *anilist.Media {
	m.mu.Lock()
	if d, ok := m.detailCache[id]; ok {
		m.mu.Unlock()
		return d
	}
	m.mu.Unlock()
	d, err := m.platform.Media(ctx, id, false)
	if err != nil {
		return nil
	}
	m.mu.Lock()
	m.detailCache[id] = d
	m.mu.Unlock()
	return d
}

func isSeries(format string) bool {
	return format == "TV" || format == "TV_SHORT" || format == "ONA"
}

func relation(d *anilist.Media, rel string) *anilist.Media {
	if d == nil || d.Relations == nil {
		return nil
	}
	for _, e := range d.Relations.Edges {
		if e.RelationType == rel && e.Node != nil && e.Node.Type == "ANIME" && isSeries(e.Node.Format) {
			return e.Node
		}
	}
	return nil
}

func (m *Matcher) resolveAbsolute(ctx context.Context, media *anilist.Media, ep int) (int, int, bool) {
	total := media.TotalEpisodes()
	// 1) The numbering may continue from the prequels (S2 starting at 25).
	offset := 0
	cur := m.detail(ctx, media.ID)
	for i := 0; i < 8 && cur != nil; i++ {
		prev := relation(cur, "PREQUEL")
		if prev == nil {
			break
		}
		offset += prev.TotalEpisodes()
		cur = m.detail(ctx, prev.ID)
	}
	if offset > 0 && ep-offset >= 1 && ep-offset <= total {
		return media.ID, ep - offset, true
	}
	// 2) Or the file belongs to a later season of the matched show.
	e := ep
	cur = media
	for i := 0; i < 8 && cur != nil; i++ {
		t := cur.TotalEpisodes()
		if t == 0 || e <= t {
			return cur.ID, e, true
		}
		next := relation(m.detail(ctx, cur.ID), "SEQUEL")
		if next == nil {
			break
		}
		e -= t
		cur = next
	}
	return 0, 0, false
}
