package anicli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/util"
)

// Mapping links an AniList entry to an ani-cli search result. The result is
// the one titled Title among those found for Query; Index is only where the
// search listed it last time, as the list changes order when the site adds
// entries.
type Mapping struct {
	Query  string  `json:"query"`
	Index  int     `json:"index"`
	Title  string  `json:"title"`
	Score  float64 `json:"score"`
	Manual bool    `json:"manual"`
}

type MatchResult struct {
	Mapping *Mapping `json:"mapping"`
	Results []Result `json:"results"`
	Query   string   `json:"query"`
}

const provider = "ani-cli"

func LoadMapping(d *db.DB, mediaID int, mode string) *Mapping {
	var raw string
	err := d.QueryRow(`SELECT value FROM stream_mappings WHERE provider = ? AND media_id = ? AND mode = ?`, provider, mediaID, mode).Scan(&raw)
	if err != nil {
		return nil
	}
	var m Mapping
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	return &m
}

func SaveMapping(d *db.DB, mediaID int, mode string, m *Mapping) error {
	raw, _ := json.Marshal(m)
	_, err := d.Write(`INSERT INTO stream_mappings(provider, media_id, mode, value) VALUES(?, ?, ?, ?)
		ON CONFLICT(provider, media_id, mode) DO UPDATE SET value = excluded.value`, provider, mediaID, mode, string(raw))
	return err
}

func DeleteMapping(d *db.DB, mediaID int, mode string) error {
	_, err := d.Write(`DELETE FROM stream_mappings WHERE provider = ? AND media_id = ? AND mode = ?`, provider, mediaID, mode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// ErrMatchGone is returned (wrapped) when the search result picked by hand
// for an anime can no longer be found.
var ErrMatchGone = errors.New("ani-cli no longer lists the anime picked for this entry")

// Match finds the ani-cli result that corresponds to an AniList entry and
// remembers it. A remembered result is looked up again, by its title, every
// time, and its position updated if it moved. refresh searches for the best
// match again, except when the result was picked by hand (or is a perfect
// match): that one is kept, and refreshing just re-reads its episode list.
func (d *Driver) Match(ctx context.Context, store *db.DB, media *anilist.Media, mode string, refresh bool) (*MatchResult, error) {
	s := &searcher{drv: d, ctx: ctx, mode: mode, done: map[string][]Result{}}
	stored := LoadMapping(store, media.ID, mode)
	skipped := stored != nil && refresh && !stored.Manual && stored.Score < 1
	if stored != nil && !skipped {
		m, res, err := s.find(stored)
		if err != nil {
			return nil, err
		}
		if m != nil {
			return remember(store, media.ID, mode, stored, m, res), nil
		}
		if stored.Manual {
			return nil, fmt.Errorf("%w: “%s” is not among the results for “%s” any more; pick it again manually", ErrMatchGone, stored.Title, stored.Query)
		}
		// It is gone: match again.
	}

	queries := searchQueries(media)
	if len(queries) == 0 {
		return nil, errors.New("media has no title to search with")
	}
	best, bestResults, err := s.bestMatch(media, queries)
	if err != nil {
		return nil, err
	}
	if best != nil && best.Score >= 0.6 {
		_ = SaveMapping(store, media.ID, mode, best)
		return &MatchResult{Mapping: best, Results: bestResults, Query: best.Query}, nil
	}
	if skipped {
		// Nothing good enough to replace the remembered match, which is what
		// Sources plays: keep it if it is still there.
		if m, res, err := s.find(stored); err == nil && m != nil {
			return remember(store, media.ID, mode, stored, m, res), nil
		}
	}
	if best == nil {
		return &MatchResult{Query: queries[0]}, nil
	}
	return &MatchResult{Mapping: best, Results: bestResults, Query: best.Query}, nil
}

// remember saves m, the stored mapping found again, if anything changed.
func remember(store *db.DB, mediaID int, mode string, stored, m *Mapping, res []Result) *MatchResult {
	if *m != *stored {
		_ = SaveMapping(store, mediaID, mode, m)
	}
	return &MatchResult{Mapping: m, Results: res, Query: m.Query}
}

// searchQueries returns the titles to search for an anime with.
func searchQueries(media *anilist.Media) []string {
	var queries []string
	for _, t := range []string{media.Title.English, media.Title.Romaji} {
		t = strings.TrimSpace(t)
		same := func(q string) bool { return strings.EqualFold(cleanQuery(q), cleanQuery(t)) }
		if cleanQuery(t) != "" && !slices.ContainsFunc(queries, same) {
			queries = append(queries, t)
		}
	}
	return queries
}

// searcher runs the ani-cli searches of one Match, each query once.
type searcher struct {
	drv  *Driver
	ctx  context.Context
	mode string
	done map[string][]Result
}

func (s *searcher) search(q string) ([]Result, error) {
	q = strings.TrimSpace(q)
	if res, ok := s.done[q]; ok {
		return res, nil
	}
	res, err := s.drv.Search(s.ctx, q, s.mode)
	if err == nil {
		s.done[q] = res
	}
	return res, err
}

// find searches for a remembered result again. It returns the mapping with
// the result's current position, or nil if the result is no longer there.
func (s *searcher) find(m *Mapping) (*Mapping, []Result, error) {
	res, err := s.search(m.Query)
	if err != nil {
		return nil, nil, err
	}
	var hits []Result
	for _, r := range res {
		if sameTitle(r.Title, m.Title) {
			hits = append(hits, r)
		}
	}
	var hit *Result
	switch {
	case len(hits) == 1:
		hit = &hits[0]
	case len(hits) > 1: // only the position tells them apart
		for i := range hits {
			if hits[i].Index == m.Index {
				hit = &hits[i]
			}
		}
	case len(res) == 1 && sameTitle(m.Title, m.Query) && util.Similarity(res[0].Title, m.Title) >= 0.6:
		// The result was the only one of its search, which ani-cli picks
		// without showing it, so the query stood in for its title. Now that
		// its title is known (it has a single episode, which ani-cli
		// played), that looks like the same anime.
		hit = &res[0]
	}
	if hit == nil {
		return nil, res, nil
	}
	found := *m
	found.Index, found.Title = hit.Index, hit.Title
	return &found, res, nil
}

// bestMatch searches for an anime's titles and picks the result that looks
// most like one of them.
func (s *searcher) bestMatch(media *anilist.Media, queries []string) (*Mapping, []Result, error) {
	total := media.TotalEpisodes()
	var best *Mapping
	var bestResults []Result
	var firstErr error
	for _, q := range queries {
		res, err := s.search(q)
		if err != nil {
			if ctxErr := s.ctx.Err(); ctxErr != nil {
				return nil, nil, ctxErr
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, r := range res {
			score := 0.0
			for _, t := range media.AllTitles() {
				score = max(score, util.Similarity(t, r.Title))
			}
			// A single result has no real title (unless it was played): the
			// query stands in for it.
			if len(res) == 1 && sameTitle(r.Title, q) {
				score = max(score, 0.9)
			}
			if total > 0 && r.Episodes > 0 && r.Episodes == total {
				score += 0.05
			}
			if best == nil || score > best.Score {
				best = &Mapping{Query: q, Index: r.Index, Title: r.Title, Score: score}
				bestResults = res
			}
		}
		if best != nil && best.Score >= 0.9 {
			break
		}
	}
	if best == nil && firstErr != nil {
		return nil, nil, firstErr
	}
	return best, bestResults, nil
}

// sameTitle compares titles ignoring case and spacing. Nothing more: the site
// has distinct entries like "Gintama", "Gintama'" and "Gintama.".
func sameTitle(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}
