package anicli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/util"
)

// Mapping links an AniList entry to an ani-cli search result.
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

// Match finds the ani-cli result that corresponds to an AniList entry. The
// choice is remembered; pass refresh to search again.
func (d *Driver) Match(ctx context.Context, store *db.DB, media *anilist.Media, mode string, refresh bool) (*MatchResult, error) {
	if !refresh {
		if m := LoadMapping(store, media.ID, mode); m != nil {
			return &MatchResult{Mapping: m, Query: m.Query}, nil
		}
	}
	queries := []string{}
	for _, t := range []string{media.Title.English, media.Title.Romaji} {
		t = strings.TrimSpace(t)
		if t != "" && !containsFold(queries, t) {
			queries = append(queries, t)
		}
	}
	if len(queries) == 0 {
		return nil, errors.New("media has no title to search with")
	}
	total := media.TotalEpisodes()
	var best *Mapping
	var bestResults []Result
	var firstErr error
	for _, q := range queries {
		res, err := d.Search(ctx, q, mode)
		if err != nil {
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
			// The single-result shortcut has no real title.
			if len(res) == 1 && strings.EqualFold(r.Title, q) {
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
	if best == nil {
		if firstErr != nil {
			return nil, firstErr
		}
		return &MatchResult{Query: queries[0]}, nil
	}
	if best.Score >= 0.6 {
		_ = SaveMapping(store, media.ID, mode, best)
	}
	return &MatchResult{Mapping: best, Results: bestResults, Query: best.Query}, nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
