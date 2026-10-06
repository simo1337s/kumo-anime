// Package metadata fetches per-episode metadata (titles, thumbnails,
// summaries, air dates) and ID mappings from api.ani.zip, with caching.
package metadata

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/util"
)

type Episode struct {
	Key            string `json:"key"` // "1", "S1"...
	Number         int    `json:"number"`
	AbsoluteNumber int    `json:"absoluteNumber"`
	SeasonNumber   int    `json:"seasonNumber"`
	Title          string `json:"title"`
	Image          string `json:"image"`
	Summary        string `json:"summary"`
	AirDate        string `json:"airDate"`
	Runtime        int    `json:"runtime"`
	IsSpecial      bool   `json:"isSpecial"`
}

type Mappings struct {
	MalID     int    `json:"malId"`
	AnidbID   int    `json:"anidbId"`
	KitsuID   int    `json:"kitsuId"`
	TvdbID    int    `json:"tvdbId"`
	TmdbID    string `json:"tmdbId"`
	ImdbID    string `json:"imdbId"`
	AnilistID int    `json:"anilistId"`
}

type Images struct {
	Banner    string `json:"banner"`
	Poster    string `json:"poster"`
	Fanart    string `json:"fanart"`
	Clearlogo string `json:"clearlogo"`
}

type Anime struct {
	Episodes     map[string]*Episode `json:"episodes"`
	EpisodeCount int                 `json:"episodeCount"`
	SpecialCount int                 `json:"specialCount"`
	Images       Images              `json:"images"`
	Mappings     Mappings            `json:"mappings"`
}

// Main returns regular episodes sorted by number.
func (a *Anime) Main() []*Episode {
	var out []*Episode
	for _, e := range a.Episodes {
		if !e.IsSpecial {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// Get returns the metadata of episode n (or nil).
func (a *Anime) Get(n int) *Episode {
	if a == nil {
		return nil
	}
	return a.Episodes[strconv.Itoa(n)]
}

type Service struct {
	db *db.DB
}

func NewService(d *db.DB) *Service { return &Service{db: d} }

type rawAnizip struct {
	Titles   map[string]string `json:"titles"`
	Episodes map[string]struct {
		Episode               string            `json:"episode"`
		EpisodeNumber         int               `json:"episodeNumber"`
		AbsoluteEpisodeNumber int               `json:"absoluteEpisodeNumber"`
		SeasonNumber          int               `json:"seasonNumber"`
		Title                 map[string]string `json:"title"`
		AirDate               string            `json:"airDate"`
		Airdate               string            `json:"airdate"`
		Runtime               int               `json:"runtime"`
		Length                int               `json:"length"`
		Overview              string            `json:"overview"`
		Summary               string            `json:"summary"`
		Image                 string            `json:"image"`
	} `json:"episodes"`
	EpisodeCount int `json:"episodeCount"`
	SpecialCount int `json:"specialCount"`
	Images       []struct {
		CoverType string `json:"coverType"`
		URL       string `json:"url"`
	} `json:"images"`
	Mappings map[string]any `json:"mappings"`
}

func cacheKey(id int) string { return "anizip:" + strconv.Itoa(id) }

// Get fetches metadata for an AniList id. Failures are not fatal for the
// caller: an empty result is returned together with the error.
func (s *Service) Get(ctx context.Context, anilistID int) (*Anime, error) {
	var a Anime
	if s.db.GetCache(cacheKey(anilistID), &a) {
		return &a, nil
	}
	var raw rawAnizip
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := util.GetJSON(ctx, fmt.Sprintf("https://api.ani.zip/mappings?anilist_id=%d", anilistID), nil, &raw)
	if err != nil {
		if s.db.GetStaleCache(cacheKey(anilistID), &a) {
			return &a, nil
		}
		return &Anime{Episodes: map[string]*Episode{}}, err
	}
	out := convert(raw)
	out.Mappings.AnilistID = anilistID
	// Airing shows change often; finished ones rarely.
	ttl := 7 * 24 * time.Hour
	if out.EpisodeCount == 0 || len(out.Main()) < out.EpisodeCount {
		ttl = 6 * time.Hour
	}
	s.db.SetCache(cacheKey(anilistID), out, ttl)
	return out, nil
}

func convert(raw rawAnizip) *Anime {
	out := &Anime{Episodes: map[string]*Episode{}, EpisodeCount: raw.EpisodeCount, SpecialCount: raw.SpecialCount}
	for key, e := range raw.Episodes {
		ep := &Episode{
			Key:            key,
			AbsoluteNumber: e.AbsoluteEpisodeNumber,
			SeasonNumber:   e.SeasonNumber,
			Image:          e.Image,
			Summary:        util.FirstNonEmpty(e.Overview, e.Summary),
			AirDate:        util.FirstNonEmpty(e.AirDate, e.Airdate),
			Runtime:        max(e.Runtime, e.Length),
		}
		if n, err := strconv.Atoi(key); err == nil {
			ep.Number = n
		} else {
			ep.IsSpecial = true
			ep.Number, _ = strconv.Atoi(strings.TrimLeft(key, "SsCcTtPpOo"))
		}
		ep.Title = util.FirstNonEmpty(e.Title["en"], e.Title["x-jat"], e.Title["ja"])
		out.Episodes[key] = ep
	}
	for _, img := range raw.Images {
		switch strings.ToLower(img.CoverType) {
		case "banner":
			out.Images.Banner = img.URL
		case "poster":
			out.Images.Poster = img.URL
		case "fanart":
			out.Images.Fanart = img.URL
		case "clearlogo":
			out.Images.Clearlogo = img.URL
		}
	}
	m := raw.Mappings
	out.Mappings.MalID = toInt(m["mal_id"])
	out.Mappings.AnidbID = toInt(m["anidb_id"])
	out.Mappings.KitsuID = toInt(m["kitsu_id"])
	out.Mappings.TvdbID = toInt(m["thetvdb_id"])
	out.Mappings.TmdbID = toStr(m["themoviedb_id"])
	out.Mappings.ImdbID = toStr(m["imdb_id"])
	return out
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func toStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.Itoa(int(x))
	}
	return ""
}
