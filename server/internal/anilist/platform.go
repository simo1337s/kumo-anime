package anilist

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
)

// Platform is the list/metadata service the rest of the app talks to. When
// the user is logged in it reads and writes their AniList account; otherwise
// it keeps an equivalent list in the local database so the app works fully
// without an account.
type Platform struct {
	client *Client
	db     *db.DB
	hub    *events.Hub

	mu          sync.RWMutex
	viewer      *Viewer
	collections map[string]*Collection
	fetchedAt   map[string]time.Time
	fetchMu     sync.Mutex
}

const (
	kvToken  = "anilist_token"
	kvViewer = "anilist_viewer"
)

var Statuses = []string{"CURRENT", "REPEATING", "PLANNING", "PAUSED", "COMPLETED", "DROPPED"}

func NewPlatform(d *db.DB, hub *events.Hub) *Platform {
	p := &Platform{
		client:      NewClient(),
		db:          d,
		hub:         hub,
		collections: map[string]*Collection{},
		fetchedAt:   map[string]time.Time{},
	}
	var tok string
	if ok, _ := d.GetKV(kvToken, &tok); ok && tok != "" {
		p.client.SetToken(tok)
		var v Viewer
		if ok, _ := d.GetKV(kvViewer, &v); ok && v.ID != 0 {
			p.viewer = &v
		}
	}
	return p
}

func (p *Platform) Client() *Client { return p.client }

// Login validates a token (from the AniList PIN page) and stores it.
func (p *Platform) Login(ctx context.Context, token string) (*Viewer, error) {
	if token == "" {
		return nil, errors.New("empty token")
	}
	tmp := NewClient()
	tmp.SetToken(token)
	v, err := tmp.Viewer(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.db.SetKV(kvToken, token); err != nil {
		return nil, err
	}
	_ = p.db.SetKV(kvViewer, v)
	p.client.SetToken(token)
	p.mu.Lock()
	p.viewer = v
	p.collections = map[string]*Collection{}
	p.fetchedAt = map[string]time.Time{}
	p.mu.Unlock()
	return v, nil
}

func (p *Platform) Logout() {
	_ = p.db.DeleteKV(kvToken)
	_ = p.db.DeleteKV(kvViewer)
	p.client.SetToken("")
	p.mu.Lock()
	p.viewer = nil
	p.collections = map[string]*Collection{}
	p.fetchedAt = map[string]time.Time{}
	p.mu.Unlock()
}

func (p *Platform) Viewer() *Viewer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.viewer
}

func (p *Platform) LoggedIn() bool { return p.Viewer() != nil && p.client.Token() != "" }

// RefreshViewer re-fetches the viewer (avatar/name changes) in the background.
func (p *Platform) RefreshViewer(ctx context.Context) {
	if !p.LoggedIn() {
		return
	}
	v, err := p.client.Viewer(ctx)
	if errors.Is(err, ErrUnauthorized) {
		p.hub.Warn("Your AniList session expired, please log in again.")
		p.Logout()
		return
	}
	if err == nil {
		p.mu.Lock()
		p.viewer = v
		p.mu.Unlock()
		_ = p.db.SetKV(kvViewer, v)
	}
}

// ---------------------------------------------------------------------------
// Collections

// Collection returns the user's anime ("ANIME") or manga ("MANGA") list.
func (p *Platform) Collection(ctx context.Context, mediaType string, refresh bool) (*Collection, error) {
	if mediaType == "" {
		mediaType = "ANIME"
	}
	if !p.LoggedIn() {
		return p.localCollection(mediaType)
	}
	p.mu.RLock()
	c, ok := p.collections[mediaType]
	at := p.fetchedAt[mediaType]
	p.mu.RUnlock()
	if ok && !refresh && time.Since(at) < 15*time.Minute {
		return c, nil
	}

	p.fetchMu.Lock()
	defer p.fetchMu.Unlock()
	// Someone else may have refreshed while we waited.
	p.mu.RLock()
	c, ok = p.collections[mediaType]
	at = p.fetchedAt[mediaType]
	p.mu.RUnlock()
	if ok && time.Since(at) < 5*time.Second {
		return c, nil
	}

	v := p.Viewer()
	fresh, err := p.client.Collection(ctx, v.ID, mediaType)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			p.hub.Warn("Your AniList session expired, please log in again.")
			p.Logout()
			return p.localCollection(mediaType)
		}
		// Offline: fall back to the last saved copy.
		var stale Collection
		if c != nil {
			return c, nil
		}
		if p.db.GetStaleCache("collection:"+mediaType, &stale) {
			return &stale, nil
		}
		return nil, err
	}
	p.mu.Lock()
	p.collections[mediaType] = fresh
	p.fetchedAt[mediaType] = time.Now()
	p.mu.Unlock()
	p.db.SetCache("collection:"+mediaType, fresh, 0)
	// Seed the media cache so detail lookups don't need extra requests.
	for _, e := range fresh.Entries() {
		p.db.SetCache(liteKey(e.MediaID), e.Media, 24*time.Hour)
	}
	return fresh, nil
}

func (p *Platform) invalidate(mediaType string) {
	p.mu.Lock()
	delete(p.fetchedAt, mediaType)
	p.mu.Unlock()
}

var listNames = map[string]string{
	"CURRENT":   "Watching",
	"REPEATING": "Rewatching",
	"PLANNING":  "Planning",
	"PAUSED":    "Paused",
	"COMPLETED": "Completed",
	"DROPPED":   "Dropped",
}

func (p *Platform) localCollection(mediaType string) (*Collection, error) {
	rows, err := p.db.Query(`SELECT media_id, status, progress, score, repeat, media, updated_at FROM local_list WHERE type = ? ORDER BY updated_at DESC`, mediaType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lists := map[string]*List{}
	for rows.Next() {
		var e ListEntry
		var raw string
		if err := rows.Scan(&e.MediaID, &e.Status, &e.Progress, &e.Score, &e.Repeat, &raw, &e.UpdatedAt); err != nil {
			continue
		}
		e.ID = e.MediaID
		var m Media
		if json.Unmarshal([]byte(raw), &m) != nil || m.ID == 0 {
			continue
		}
		e.Media = &m
		l, ok := lists[e.Status]
		if !ok {
			l = &List{Name: listNames[e.Status], Status: e.Status}
			lists[e.Status] = l
		}
		l.Entries = append(l.Entries, &e)
	}
	c := &Collection{}
	for _, s := range Statuses {
		if l, ok := lists[s]; ok {
			c.Lists = append(c.Lists, l)
		}
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Media

func liteKey(id int) string   { return "media-lite:" + strconv.Itoa(id) }
func detailKey(id int) string { return "media:" + strconv.Itoa(id) }

// Media returns full media details (description, relations, characters...).
func (p *Platform) Media(ctx context.Context, id int, refresh bool) (*Media, error) {
	var m Media
	if !refresh && p.db.GetCache(detailKey(id), &m) {
		p.attachListEntry(ctx, &m)
		return &m, nil
	}
	fresh, err := p.client.Media(ctx, id)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			p.Logout()
			fresh, err = p.client.Media(ctx, id)
		}
		if err != nil {
			if p.db.GetStaleCache(detailKey(id), &m) {
				p.attachListEntry(ctx, &m)
				return &m, nil
			}
			return nil, err
		}
	}
	// The list entry is per-user state, don't cache it with the media.
	fresh.MediaListEntry = nil
	p.db.SetCache(detailKey(id), fresh, 12*time.Hour)
	p.db.SetCache(liteKey(id), fresh.Lite(), 24*time.Hour)
	p.attachListEntry(ctx, fresh)
	return fresh, nil
}

func (p *Platform) attachListEntry(ctx context.Context, m *Media) {
	c, err := p.Collection(ctx, typeOr(m.Type), false)
	if err != nil || c == nil {
		return
	}
	if e := c.Find(m.ID); e != nil {
		m.MediaListEntry = &ListEntryLite{ID: e.ID, Status: e.Status, Progress: e.Progress, Score: e.Score, Repeat: e.Repeat}
	} else {
		m.MediaListEntry = nil
	}
}

func typeOr(t string) string {
	if t == "" {
		return "ANIME"
	}
	return t
}

// MediaLite returns the lightweight media object, looking in the collection
// and cache before hitting the network.
func (p *Platform) MediaLite(ctx context.Context, id int) (*Media, error) {
	if id <= 0 {
		return nil, errors.New("invalid media id")
	}
	p.mu.RLock()
	for _, c := range p.collections {
		if e := c.Find(id); e != nil {
			p.mu.RUnlock()
			return e.Media, nil
		}
	}
	p.mu.RUnlock()
	var m Media
	if p.db.GetCache(liteKey(id), &m) {
		return &m, nil
	}
	full, err := p.Media(ctx, id, false)
	if err != nil {
		if p.db.GetStaleCache(liteKey(id), &m) {
			return &m, nil
		}
		return nil, err
	}
	return full.Lite(), nil
}

// MediaBatch fetches several media objects in one request (max 50 ids).
func (p *Platform) MediaBatch(ctx context.Context, ids []int) (map[int]*Media, error) {
	out := map[int]*Media{}
	var missing []int
	for _, id := range ids {
		var m Media
		if p.db.GetCache(liteKey(id), &m) {
			out[id] = &m
		} else {
			missing = append(missing, id)
		}
	}
	for len(missing) > 0 {
		chunk := missing
		if len(chunk) > 50 {
			chunk = chunk[:50]
		}
		missing = missing[len(chunk):]
		page, err := p.client.Search(ctx, SearchParams{IDs: chunk, PerPage: 50, Sort: []string{"ID"}})
		if err != nil {
			return out, err
		}
		for _, m := range page.Media {
			out[m.ID] = m
			p.db.SetCache(liteKey(m.ID), m.Lite(), 24*time.Hour)
		}
	}
	return out, nil
}

// Search queries AniList with caching.
func (p *Platform) Search(ctx context.Context, params SearchParams) (*MediaPage, error) {
	raw, _ := json.Marshal(params)
	sum := sha1.Sum(raw)
	key := "search:" + hex.EncodeToString(sum[:])
	var page MediaPage
	if p.db.GetCache(key, &page) {
		return &page, nil
	}
	res, err := p.client.Search(ctx, params)
	if err != nil {
		if p.db.GetStaleCache(key, &page) {
			return &page, nil
		}
		return nil, err
	}
	p.db.SetCache(key, res, 30*time.Minute)
	for _, m := range res.Media {
		p.db.SetCache(liteKey(m.ID), m.Lite(), 24*time.Hour)
	}
	return res, nil
}

// Schedule returns airing episodes between two unix timestamps.
func (p *Platform) Schedule(ctx context.Context, start, end int64) ([]*AiringEpisode, error) {
	key := fmt.Sprintf("schedule:%d:%d", start/3600, end/3600)
	var out []*AiringEpisode
	if p.db.GetCache(key, &out) {
		return out, nil
	}
	res, err := p.client.Schedule(ctx, start, end)
	if err != nil && len(res) == 0 {
		if p.db.GetStaleCache(key, &out) {
			return out, nil
		}
		return nil, err
	}
	p.db.SetCache(key, res, time.Hour)
	return res, nil
}

// ---------------------------------------------------------------------------
// Updating the list

// UpdateEntry creates or updates a list entry.
func (p *Platform) UpdateEntry(ctx context.Context, u EntryUpdate) error {
	media, err := p.MediaLite(ctx, u.MediaID)
	if err != nil {
		return err
	}
	mt := typeOr(media.Type)
	if p.LoggedIn() {
		if _, err := p.client.SaveEntry(ctx, u); err != nil {
			return err
		}
		p.patchCachedEntry(mt, media, u)
		p.invalidate(mt)
		p.hub.Publish(events.CollectionUpdate, map[string]any{"mediaId": u.MediaID})
		return nil
	}

	// Local list
	cur := struct {
		status   string
		progress int
		score    float64
		repeat   int
	}{status: "PLANNING"}
	_ = p.db.QueryRow(`SELECT status, progress, score, repeat FROM local_list WHERE media_id = ?`, u.MediaID).
		Scan(&cur.status, &cur.progress, &cur.score, &cur.repeat)
	if u.Status != nil {
		cur.status = *u.Status
	}
	if u.Progress != nil {
		cur.progress = *u.Progress
	}
	if u.Score != nil {
		cur.score = *u.Score
	}
	if u.Repeat != nil {
		cur.repeat = *u.Repeat
	}
	raw, _ := json.Marshal(media.Lite())
	_, err = p.db.Write(`INSERT INTO local_list(media_id, type, status, progress, score, repeat, media, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(media_id) DO UPDATE SET status = excluded.status, progress = excluded.progress, score = excluded.score,
			repeat = excluded.repeat, media = excluded.media, updated_at = excluded.updated_at`,
		u.MediaID, mt, cur.status, cur.progress, cur.score, cur.repeat, string(raw), time.Now().Unix())
	if err == nil {
		p.hub.Publish(events.CollectionUpdate, map[string]any{"mediaId": u.MediaID})
	}
	return err
}

// patchCachedEntry applies an update to the in-memory collection so the UI
// reflects it immediately, before the next full refresh.
func (p *Platform) patchCachedEntry(mt string, media *Media, u EntryUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.collections[mt]
	if c == nil {
		return
	}
	e := c.Find(u.MediaID)
	if e == nil {
		e = &ListEntry{MediaID: u.MediaID, Media: media, Status: "PLANNING"}
		status := "PLANNING"
		if u.Status != nil {
			status = *u.Status
		}
		var list *List
		for _, l := range c.Lists {
			if l.Status == status && !l.IsCustomList {
				list = l
			}
		}
		if list == nil {
			list = &List{Name: listNames[status], Status: status}
			c.Lists = append(c.Lists, list)
		}
		list.Entries = append(list.Entries, e)
	}
	if u.Status != nil && *u.Status != e.Status {
		// move between lists
		for _, l := range c.Lists {
			for i, x := range l.Entries {
				if x == e {
					l.Entries = append(l.Entries[:i], l.Entries[i+1:]...)
					break
				}
			}
		}
		var target *List
		for _, l := range c.Lists {
			if l.Status == *u.Status && !l.IsCustomList {
				target = l
			}
		}
		if target == nil {
			target = &List{Name: listNames[*u.Status], Status: *u.Status}
			c.Lists = append(c.Lists, target)
		}
		target.Entries = append(target.Entries, e)
		e.Status = *u.Status
	}
	if u.Progress != nil {
		e.Progress = *u.Progress
	}
	if u.Score != nil {
		e.Score = *u.Score
	}
	if u.Repeat != nil {
		e.Repeat = *u.Repeat
	}
	e.UpdatedAt = time.Now().Unix()
}

// DeleteEntry removes a media from the list.
func (p *Platform) DeleteEntry(ctx context.Context, mediaID int) error {
	if p.LoggedIn() {
		media, _ := p.MediaLite(ctx, mediaID)
		mt := "ANIME"
		if media != nil {
			mt = typeOr(media.Type)
		}
		c, err := p.Collection(ctx, mt, false)
		if err != nil {
			return err
		}
		e := c.Find(mediaID)
		if e == nil {
			return nil
		}
		if err := p.client.DeleteEntry(ctx, e.ID); err != nil {
			return err
		}
		p.mu.Lock()
		for _, l := range c.Lists {
			for i, x := range l.Entries {
				if x.MediaID == mediaID {
					l.Entries = append(l.Entries[:i], l.Entries[i+1:]...)
					break
				}
			}
		}
		p.mu.Unlock()
		p.invalidate(mt)
	} else {
		if _, err := p.db.Write(`DELETE FROM local_list WHERE media_id = ?`, mediaID); err != nil {
			return err
		}
	}
	p.hub.Publish(events.CollectionUpdate, map[string]any{"mediaId": mediaID})
	return nil
}

// UpdateProgress records that the user finished `episode` of a media. It only
// ever moves progress forward, and handles status transitions:
// not in list/planning -> watching, last episode -> completed.
func (p *Platform) UpdateProgress(ctx context.Context, mediaID, episode int) (bool, error) {
	media, err := p.MediaLite(ctx, mediaID)
	if err != nil {
		return false, err
	}
	c, err := p.Collection(ctx, typeOr(media.Type), false)
	if err != nil {
		return false, err
	}
	entry := c.Find(mediaID)
	total := 0
	if media.Episodes != nil {
		total = *media.Episodes
	}
	status := "CURRENT"
	progress := episode
	u := EntryUpdate{MediaID: mediaID}
	now := time.Now()
	y, mth, d := now.Year(), int(now.Month()), now.Day()
	today := &FuzzyDate{Year: &y, Month: &mth, Day: &d}

	if entry != nil {
		if entry.Status == "COMPLETED" && (total == 0 || episode >= total) {
			return false, nil
		}
		if entry.Status == "COMPLETED" && episode < total {
			// Rewatching a completed show.
			status = "REPEATING"
		} else if entry.Status == "REPEATING" {
			status = "REPEATING"
		}
		if entry.Progress >= episode && entry.Status != "COMPLETED" && entry.Status != "PLANNING" {
			return false, nil
		}
		if entry.Status == "PLANNING" || entry.Progress == 0 {
			u.StartedAt = today
		}
	} else {
		u.StartedAt = today
	}
	if total > 0 && episode >= total {
		progress = total
		if entry != nil && entry.Status == "REPEATING" {
			r := entry.Repeat + 1
			u.Repeat = &r
		}
		status = "COMPLETED"
		u.CompletedAt = today
	}
	u.Status = &status
	u.Progress = &progress
	if err := p.UpdateEntry(ctx, u); err != nil {
		return false, err
	}
	return true, nil
}
