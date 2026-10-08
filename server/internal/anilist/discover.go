package anilist

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// Discover is the Discover page: AniList's trending anime, this and next
// season's, the most popular and the top rated.
type Discover struct {
	Trending   []*Media `json:"trending"`
	ThisSeason []*Media `json:"thisSeason"`
	NextSeason []*Media `json:"nextSeason"`
	Popular    []*Media `json:"popular"`
	TopRated   []*Media `json:"topRated"`
	Season     string   `json:"season"`
	Year       int      `json:"year"`
	// FetchedAt is when AniList gave it (unix seconds).
	FetchedAt int64 `json:"fetchedAt"`
}

const (
	// discoverFresh: a copy this young is shown as is.
	discoverFresh = 30 * time.Minute
	// discoverKeep: an older copy is shown right away while a fresh one is
	// fetched, for up to this long (also what's shown when AniList can't be
	// reached).
	discoverKeep = 7 * 24 * time.Hour
)

// Discover returns the Discover page. Only the first one waits for AniList:
// after that a copy is always there, and an old one is refreshed in the
// background.
func (p *Platform) Discover(ctx context.Context) (*Discover, error) {
	if d := p.savedDiscover(time.Now()); d != nil {
		if time.Since(time.Unix(d.FetchedAt, 0)) >= discoverFresh {
			p.refreshDiscover()
		}
		return d, nil
	}
	return p.fetchDiscover(ctx)
}

// WarmDiscover fetches the Discover page when there's no fresh copy, so that
// it opens at once.
func (p *Platform) WarmDiscover(ctx context.Context) (*Discover, error) {
	if d := p.savedDiscover(time.Now()); d != nil && time.Since(time.Unix(d.FetchedAt, 0)) < discoverFresh {
		return d, nil
	}
	return p.fetchDiscover(ctx)
}

// savedDiscover is the saved copy for the current season, unless it's older
// than discoverKeep.
func (p *Platform) savedDiscover(now time.Time) *Discover {
	var d Discover
	season, year := CurrentSeason(now)
	if !p.db.GetStaleCache(discoverKey, &d) || d.FetchedAt <= 0 || d.Season != season || d.Year != year {
		return nil
	}
	if now.Sub(time.Unix(d.FetchedAt, 0)) >= discoverKeep {
		return nil
	}
	return &d
}

const discoverKey = "discover"

// refreshDiscover fetches a fresh copy in the background, one at a time.
func (p *Platform) refreshDiscover() {
	if !p.discovering.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer p.discovering.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := p.fetchDiscover(ctx); err != nil {
			log.Printf("discover: %v", err)
		}
	}()
}

// fetchDiscover asks AniList for every section at once and saves the page
// when they all came. Callers arriving meanwhile get the same result.
func (p *Platform) fetchDiscover(ctx context.Context) (*Discover, error) {
	p.discoverMu.Lock()
	defer p.discoverMu.Unlock()
	// Fetched by the caller this one waited for.
	if d := p.savedDiscover(time.Now()); d != nil && time.Since(time.Unix(d.FetchedAt, 0)) < discoverFresh {
		return d, nil
	}
	now := time.Now()
	season, year := CurrentSeason(now)
	next, nextYear := CurrentSeason(now.AddDate(0, 3, 0))
	noAdult := false
	d := &Discover{Season: season, Year: year}
	sections := []struct {
		out    *[]*Media
		params SearchParams
	}{
		{&d.Trending, SearchParams{Sort: []string{"TRENDING_DESC", "POPULARITY_DESC"}, PerPage: 20}},
		{&d.ThisSeason, SearchParams{Season: season, Year: year, Sort: []string{"POPULARITY_DESC"}, PerPage: 20}},
		{&d.NextSeason, SearchParams{Season: next, Year: nextYear, Sort: []string{"POPULARITY_DESC"}, PerPage: 20}},
		{&d.Popular, SearchParams{Sort: []string{"POPULARITY_DESC"}, PerPage: 20}},
		{&d.TopRated, SearchParams{Sort: []string{"SCORE_DESC"}, PerPage: 20}},
	}
	errs := make([]error, len(sections))
	var wg sync.WaitGroup
	for i, sec := range sections {
		sec.params.IsAdult = &noAdult
		*sec.out = []*Media{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Straight from AniList: the page is saved whole, never with
			// rows older than the rest.
			res, err := p.client.Search(ctx, sec.params)
			if err != nil {
				errs[i] = err
				return
			}
			if res.Media != nil {
				*sec.out = res.Media
			}
			for _, m := range res.Media {
				p.db.SetCache(liteKey(m.ID), m.Lite(), 24*time.Hour)
			}
		}()
	}
	wg.Wait()
	err := errors.Join(errs...)
	if err == nil {
		d.FetchedAt = now.Unix()
		p.db.SetCache(discoverKey, d, discoverKeep)
		return d, nil
	}
	// Not saved incomplete: rather an older complete copy, else what came.
	if old := p.savedDiscover(now); old != nil {
		return old, nil
	}
	if len(d.Trending) == 0 && len(d.ThisSeason) == 0 && len(d.Popular) == 0 {
		return nil, errs[firstErr(errs)]
	}
	return d, nil
}

func firstErr(errs []error) int {
	for i, err := range errs {
		if err != nil {
			return i
		}
	}
	return 0
}

// CurrentSeason is AniList's season (and its year) at t: December counts as
// the next year's winter.
func CurrentSeason(t time.Time) (string, int) {
	y := t.Year()
	switch t.Month() {
	case time.December:
		return "WINTER", y + 1
	case time.January, time.February:
		return "WINTER", y
	case time.March, time.April, time.May:
		return "SPRING", y
	case time.June, time.July, time.August:
		return "SUMMER", y
	}
	return "FALL", y
}
