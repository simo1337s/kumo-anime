package api

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/torrent"
)

// allProviders is the provider id that searches every torrent provider.
const allProviders = "all"

// searchAllProviders runs a search on every torrent provider (built-in and
// extensions) at once and merges the results: the same torrent found by
// several providers is listed once, best releases and most seeders first.
// It fails only when every provider fails.
func (s *Server) searchAllProviders(ctx context.Context, search func(ctx context.Context, p torrent.Provider) ([]*torrent.SearchResult, error)) ([]*torrent.SearchResult, error) {
	var providers []torrent.Provider
	for _, info := range s.app.Torrents.Providers() {
		// Provider falls back to Nyaa for unknown ids; skip those.
		if p, err := s.app.Torrents.Provider(info.ID); err == nil && p.ID() == info.ID {
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 {
		return []*torrent.SearchResult{}, nil
	}
	type result struct {
		id  string
		res []*torrent.SearchResult
		err error
	}
	ch := make(chan result, len(providers))
	for _, p := range providers {
		go func(p torrent.Provider) {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			res, err := search(cctx, p)
			for _, r := range res {
				if r != nil && r.Provider == "" {
					r.Provider = p.ID()
				}
			}
			ch <- result{p.ID(), res, err}
		}(p)
	}
	merged := []*torrent.SearchResult{}
	seen := map[string]int{}
	var firstErr error
	succeeded := 0
	for range providers {
		r := <-ch
		if r.err != nil {
			log.Printf("torrent search (%s): %v", r.id, r.err)
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", r.id, r.err)
			}
			continue
		}
		succeeded++
		for _, x := range r.res {
			if x == nil {
				continue
			}
			k := torrentKey(x)
			if i, dup := seen[k]; dup {
				if x.Seeders > merged[i].Seeders {
					merged[i] = x
				}
				continue
			}
			seen[k] = len(merged)
			merged = append(merged, x)
		}
	}
	if succeeded == 0 {
		return nil, firstErr
	}
	sort.SliceStable(merged, func(i, j int) bool {
		a, b := merged[i], merged[j]
		if a.IsBestRelease != b.IsBestRelease {
			return a.IsBestRelease
		}
		return a.Seeders > b.Seeders
	})
	return merged, nil
}

// torrentKey identifies a torrent across providers: its info hash, else
// its name and size.
func torrentKey(r *torrent.SearchResult) string {
	if h := strings.ToLower(strings.TrimSpace(r.InfoHash)); h != "" {
		return "h:" + h
	}
	if m := strings.ToLower(r.MagnetLink); m != "" {
		if i := strings.Index(m, "btih:"); i >= 0 {
			h := m[i+5:]
			if j := strings.IndexByte(h, '&'); j >= 0 {
				h = h[:j]
			}
			return "h:" + h
		}
	}
	return "n:" + strings.ToLower(strings.TrimSpace(r.Name)) + "|" + strconv.FormatInt(r.Size, 10)
}
