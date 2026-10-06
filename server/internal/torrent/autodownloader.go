package torrent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/5rahim/habari"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/library"
	"github.com/simo1337s/animetest/server/internal/util"
)

// Rule tells the auto downloader which releases to grab for an anime.
type Rule struct {
	ID              int      `json:"id"`
	Enabled         bool     `json:"enabled"`
	MediaID         int      `json:"mediaId"`
	Title           string   `json:"title"`
	ReleaseGroups   []string `json:"releaseGroups"`
	Resolutions     []string `json:"resolutions"`
	AdditionalTerms []string `json:"additionalTerms"`
	EpisodeType     string   `json:"episodeType"` // recent | all
	MinSeeders      int      `json:"minSeeders"`
	Provider        string   `json:"provider"`
}

type AutoItem struct {
	Key       string `json:"key"`
	RuleID    int    `json:"ruleId"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
}

type AutoDownloader struct {
	db       *db.DB
	torrents *Manager
	platform *anilist.Platform
	files    *library.Store
	hub      *events.Hub
	mu       sync.Mutex
}

func NewAutoDownloader(d *db.DB, t *Manager, p *anilist.Platform, f *library.Store, hub *events.Hub) *AutoDownloader {
	return &AutoDownloader{db: d, torrents: t, platform: p, files: f, hub: hub}
}

func (a *AutoDownloader) Rules() ([]*Rule, error) {
	rows, err := a.db.Query(`SELECT id, data FROM autodownload_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Rule{}
	for rows.Next() {
		var id int
		var raw string
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		var r Rule
		if json.Unmarshal([]byte(raw), &r) == nil {
			r.ID = id
			out = append(out, &r)
		}
	}
	return out, nil
}

func (a *AutoDownloader) SaveRule(r Rule) (*Rule, error) {
	if r.MediaID <= 0 {
		return nil, fmt.Errorf("a rule needs an anime")
	}
	if r.EpisodeType == "" {
		r.EpisodeType = "recent"
	}
	raw, _ := json.Marshal(r)
	if r.ID > 0 {
		_, err := a.db.Write(`UPDATE autodownload_rules SET data = ? WHERE id = ?`, string(raw), r.ID)
		return &r, err
	}
	res, err := a.db.Write(`INSERT INTO autodownload_rules(data) VALUES(?)`, string(raw))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	r.ID = int(id)
	return &r, nil
}

func (a *AutoDownloader) DeleteRule(id int) error {
	_, err := a.db.Write(`DELETE FROM autodownload_rules WHERE id = ?`, id)
	return err
}

func (a *AutoDownloader) Items() []AutoItem {
	rows, err := a.db.Query(`SELECT key, rule_id, title, created_at FROM autodownload_items ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []AutoItem{}
	for rows.Next() {
		var it AutoItem
		if rows.Scan(&it.Key, &it.RuleID, &it.Title, &it.CreatedAt) == nil {
			out = append(out, it)
		}
	}
	return out
}

func (a *AutoDownloader) seen(key string) bool {
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM autodownload_items WHERE key = ?`, key).Scan(&n)
	return n > 0
}

// Run checks every enabled rule once. Returns how many torrents were added.
func (a *AutoDownloader) Run(ctx context.Context) (int, error) {
	if !a.mu.TryLock() {
		return 0, nil
	}
	defer a.mu.Unlock()
	rules, err := a.Rules()
	if err != nil {
		return 0, err
	}
	coll, _ := a.platform.Collection(ctx, "ANIME", false)
	added := 0
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		n, err := a.runRule(ctx, r, coll)
		if err != nil {
			log.Printf("auto downloader rule %d: %v", r.ID, err)
			continue
		}
		added += n
	}
	return added, nil
}

func (a *AutoDownloader) runRule(ctx context.Context, r *Rule, coll *anilist.Collection) (int, error) {
	media, err := a.platform.MediaLite(ctx, r.MediaID)
	if err != nil {
		return 0, err
	}
	title := util.FirstNonEmpty(r.Title, media.Title.Romaji, media.Title.English)
	progress := 0
	if coll != nil {
		if e := coll.Find(r.MediaID); e != nil {
			progress = e.Progress
		}
	}
	have := map[int]bool{}
	if files, err := a.files.ByMedia(r.MediaID); err == nil {
		for _, f := range files {
			if f.Kind == "main" {
				have[f.Episode] = true
			}
		}
	}
	prov, err := a.torrents.Provider(util.FirstNonEmpty(r.Provider, "nyaa"))
	if err != nil {
		return 0, err
	}
	query := title
	if len(r.ReleaseGroups) == 1 {
		query = r.ReleaseGroups[0] + " " + query
	}
	if len(r.Resolutions) == 1 {
		query += " " + r.Resolutions[0]
	}
	results, err := prov.Search(ctx, query)
	if err != nil {
		return 0, err
	}
	best := map[int]*SearchResult{}
	for _, res := range results {
		if res.IsBatch || res.EpisodeNumber <= 0 || res.Seeders < r.MinSeeders {
			continue
		}
		md := habari.Parse(res.Name)
		if util.Similarity(md.Title, title) < 0.75 && !strings.Contains(util.NormalizeTitle(res.Name), util.NormalizeTitle(title)) {
			continue
		}
		if len(r.ReleaseGroups) > 0 && !containsFold(r.ReleaseGroups, res.ReleaseGroup) {
			continue
		}
		if len(r.Resolutions) > 0 {
			ok := false
			for _, want := range r.Resolutions {
				if strings.TrimSuffix(normRes(want), "p") == strings.TrimSuffix(res.Resolution, "p") {
					ok = true
				}
			}
			if !ok {
				continue
			}
		}
		missingTerm := false
		for _, term := range r.AdditionalTerms {
			if term != "" && !strings.Contains(strings.ToLower(res.Name), strings.ToLower(term)) {
				missingTerm = true
			}
		}
		if missingTerm {
			continue
		}
		ep := res.EpisodeNumber
		if total := media.TotalEpisodes(); total > 0 && ep > total {
			continue
		}
		if r.EpisodeType != "all" && ep <= progress {
			continue
		}
		if have[ep] || a.seen(fmt.Sprintf("%d:%d", r.MediaID, ep)) {
			continue
		}
		if cur, ok := best[ep]; !ok || res.Seeders > cur.Seeders {
			best[ep] = res
		}
	}
	added := 0
	for ep, res := range best {
		magnet, err := prov.Magnet(ctx, res)
		if err != nil || magnet == "" {
			continue
		}
		if err := a.torrents.Add(ctx, []string{magnet}, a.torrents.SavePathFor(media.PreferredTitle())); err != nil {
			return added, err
		}
		_, _ = a.db.Write(`INSERT OR IGNORE INTO autodownload_items(key, rule_id, title, created_at) VALUES(?, ?, ?, ?)`,
			fmt.Sprintf("%d:%d", r.MediaID, ep), r.ID, res.Name, time.Now().Unix())
		a.hub.Success("Auto downloader: added " + res.Name)
		added++
	}
	return added, nil
}

// Loop runs the auto downloader periodically while enabled.
func (a *AutoDownloader) Loop(ctx context.Context, enabled func() (bool, time.Duration)) {
	for {
		on, every := enabled()
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		if on {
			if n, err := a.Run(ctx); err != nil {
				log.Printf("auto downloader: %v", err)
			} else if n > 0 {
				log.Printf("auto downloader: added %d torrents", n)
			}
		}
	}
}
