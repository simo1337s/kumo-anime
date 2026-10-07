// Package manga implements reading manga through manga-provider extensions.
package manga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/extensions"
	"github.com/simo1337s/animetest/server/internal/util"
)

type Service struct {
	db       *db.DB
	exts     *extensions.Manager
	platform *anilist.Platform
}

func NewService(d *db.DB, e *extensions.Manager, p *anilist.Platform) *Service {
	return &Service{db: d, exts: e, platform: p}
}

type Mapping struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
}

func mapKey(provider string) string { return "manga:" + provider }

func (s *Service) loadMapping(provider string, mediaID int) *Mapping {
	var raw string
	if s.db.QueryRow(`SELECT value FROM stream_mappings WHERE provider = ? AND media_id = ? AND mode = ''`, mapKey(provider), mediaID).Scan(&raw) != nil {
		return nil
	}
	var m Mapping
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	return &m
}

func (s *Service) SetMapping(provider string, mediaID int, m Mapping) {
	raw, _ := json.Marshal(m)
	_, _ = s.db.Write(`INSERT INTO stream_mappings(provider, media_id, mode, value) VALUES(?, ?, '', ?)
		ON CONFLICT(provider, media_id, mode) DO UPDATE SET value = excluded.value`, mapKey(provider), mediaID, string(raw))
	s.db.DeleteCachePrefix(fmt.Sprintf("manga-ch:%s:%d", provider, mediaID))
}

func (s *Service) Search(ctx context.Context, provider, query string) ([]extensions.MangaSearchResult, error) {
	p, err := s.exts.MangaProvider(provider)
	if err != nil {
		return nil, err
	}
	return p.Search(ctx, query, 0)
}

type ChaptersResult struct {
	Provider string                    `json:"provider"`
	Mapping  *Mapping                  `json:"mapping"`
	Chapters []extensions.MangaChapter `json:"chapters"`
}

func (s *Service) Chapters(ctx context.Context, provider string, media *anilist.Media, refresh bool) (*ChaptersResult, error) {
	if provider == "" {
		ps := s.exts.MangaProviders()
		if len(ps) == 0 {
			return nil, errors.New("install a manga provider extension first (Extensions › Marketplace)")
		}
		provider = ps[0].ID()
	}
	p, err := s.exts.MangaProvider(provider)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("manga-ch:%s:%d", provider, media.ID)
	var cached ChaptersResult
	if !refresh && s.db.GetCache(key, &cached) {
		return &cached, nil
	}
	m := s.loadMapping(provider, media.ID)
	if m == nil || refresh && m.Score < 1 {
		var best *Mapping
		for _, t := range []string{media.Title.English, media.Title.Romaji} {
			if strings.TrimSpace(t) == "" {
				continue
			}
			res, err := p.Search(ctx, t, media.Year())
			if err != nil {
				continue
			}
			for _, r := range res {
				score := r.SearchRating
				if score == 0 {
					for _, title := range media.AllTitles() {
						score = math.Max(score, util.Similarity(title, r.Title))
						for _, syn := range r.Synonyms {
							score = math.Max(score, util.Similarity(title, syn))
						}
					}
				}
				if best == nil || score > best.Score {
					best = &Mapping{ID: r.ID, Title: r.Title, Score: score}
				}
			}
			if best != nil && best.Score > 0.9 {
				break
			}
		}
		if best == nil {
			return &ChaptersResult{Provider: provider, Chapters: []extensions.MangaChapter{}}, nil
		}
		m = best
		s.SetMapping(provider, media.ID, *m)
	}
	chapters, err := p.FindChapters(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		a, _ := strconv.ParseFloat(chapters[i].Chapter, 64)
		b, _ := strconv.ParseFloat(chapters[j].Chapter, 64)
		if a != b {
			return a < b
		}
		return chapters[i].Index < chapters[j].Index
	})
	res := &ChaptersResult{Provider: provider, Mapping: m, Chapters: chapters}
	if len(chapters) > 0 { // an empty list is often a temporary provider hiccup
		s.db.SetCache(key, res, time.Hour)
	}
	return res, nil
}

func (s *Service) Pages(ctx context.Context, provider, chapterID string) ([]extensions.MangaPage, error) {
	p, err := s.exts.MangaProvider(provider)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("manga-pages:%s:%s", provider, chapterID)
	var cached []extensions.MangaPage
	if s.db.GetCache(key, &cached) {
		return cached, nil
	}
	pages, err := p.FindChapterPages(ctx, chapterID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Index < pages[j].Index })
	// Page URLs often expire quickly (MangaDex@Home links last ~15 minutes),
	// so only keep them while a chapter is likely still being read.
	if len(pages) > 0 {
		s.db.SetCache(key, pages, 10*time.Minute)
	}
	return pages, nil
}

// MarkRead updates the AniList progress after finishing a chapter.
// Position is where reading a manga stopped: a provider's chapter, and the
// page in it (from 0). The reader saves it as pages turn; the manga's page
// continues from it.
type Position struct {
	Provider  string `json:"provider"`
	ChapterID string `json:"chapterId"`
	Chapter   string `json:"chapter"` // its number, e.g. "12" or "12.5"
	Page      int    `json:"page"`
	Pages     int    `json:"pages"` // in the chapter
	UpdatedAt int64  `json:"updatedAt"`
}

func positionKey(mediaID int) string { return fmt.Sprintf("manga-position:%d", mediaID) }

// Position returns where reading the manga stopped, or nil.
func (s *Service) Position(mediaID int) *Position {
	var p Position
	if ok, err := s.db.GetKV(positionKey(mediaID), &p); !ok || err != nil {
		return nil
	}
	return &p
}

// ErrBadPosition: a position without its chapter, or an impossible page.
var ErrBadPosition = errors.New("invalid reading position")

// SetPosition saves where reading the manga stopped.
func (s *Service) SetPosition(mediaID int, p Position) error {
	if mediaID <= 0 || p.Provider == "" || p.ChapterID == "" || p.Page < 0 || p.Pages < 0 ||
		len(p.Provider) > 256 || len(p.ChapterID) > 2048 || len(p.Chapter) > 64 {
		return ErrBadPosition
	}
	p.UpdatedAt = time.Now().Unix()
	return s.db.SetKV(positionKey(mediaID), p)
}

func (s *Service) MarkRead(ctx context.Context, mediaID int, chapter string) error {
	n, err := strconv.ParseFloat(chapter, 64)
	if err != nil || n <= 0 {
		return nil
	}
	_, err = s.platform.UpdateProgress(ctx, mediaID, int(math.Floor(n)))
	return err
}
