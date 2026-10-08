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
	"sync/atomic"
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

	// authMu serialises logins and logouts, so the token and viewer saved in
	// the database always match the ones in memory.
	authMu sync.Mutex

	mu     sync.RWMutex
	viewer *Viewer
	// session is incremented on every login and logout. Work that started in
	// an earlier session (a fetch, a list update, a rejected token) must not
	// change the state of the current one.
	session uint64
	// collections holds snapshots shared with every caller of Collection.
	// They are never modified: a change builds a new Collection and swaps
	// the pointer, so readers keep a consistent copy.
	collections map[string]*Collection
	fetchedAt   map[string]time.Time
	// versions counts the changes made to each cached collection, so that a
	// fetch already running (whose result may predate a change) isn't cached.
	versions map[string]uint64

	// discoverMu allows one Discover fetch at a time, and discovering one
	// refresh in the background (discover.go).
	discoverMu  sync.Mutex
	discovering atomic.Bool

	// fetchSem allows one collection fetch at a time. Unlike a mutex, the
	// wait for it ends when the caller's context does.
	fetchSem chan struct{}
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
		versions:    map[string]uint64{},
		fetchSem:    make(chan struct{}, 1),
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
	v, err := p.client.withToken(token).Viewer(ctx)
	if err != nil {
		return nil, err
	}
	p.authMu.Lock()
	defer p.authMu.Unlock()
	if err := p.db.SetKV(kvToken, token); err != nil {
		return nil, err
	}
	_ = p.db.SetKV(kvViewer, v)
	p.mu.Lock()
	p.session++
	p.viewer = v
	p.client.SetToken(token)
	p.collections = map[string]*Collection{}
	p.fetchedAt = map[string]time.Time{}
	p.mu.Unlock()
	return v, nil
}

// Logout forgets the token; the app switches to the local list.
func (p *Platform) Logout() { p.endSession(nil) }

// expire handles AniList rejecting the token of session s: the user is
// logged out, unless they already logged out or in again since (then the
// rejection is stale and must not end the newer session).
func (p *Platform) expire(s uint64) {
	if p.endSession(&s) {
		p.hub.Warn("Your AniList session expired, please log in again.")
	}
}

// endSession logs out; when only is set, only if the session is still *only.
// It reports whether a logged-in user was logged out.
func (p *Platform) endSession(only *uint64) bool {
	p.authMu.Lock()
	defer p.authMu.Unlock()
	p.mu.Lock()
	if only != nil && *only != p.session {
		p.mu.Unlock()
		return false
	}
	wasLoggedIn := p.viewer != nil
	p.session++
	p.viewer = nil
	p.client.SetToken("")
	p.collections = map[string]*Collection{}
	p.fetchedAt = map[string]time.Time{}
	p.mu.Unlock()
	_ = p.db.DeleteKV(kvToken)
	_ = p.db.DeleteKV(kvViewer)
	return wasLoggedIn
}

func (p *Platform) Viewer() *Viewer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.viewer
}

// state returns the current session and the logged-in viewer (nil when
// logged out), read together so they belong to the same session.
func (p *Platform) state() (uint64, *Viewer) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.viewer == nil || p.client.Token() == "" {
		return p.session, nil
	}
	return p.session, p.viewer
}

func (p *Platform) LoggedIn() bool {
	_, v := p.state()
	return v != nil
}

// RefreshViewer re-fetches the viewer (avatar/name changes) in the background.
func (p *Platform) RefreshViewer(ctx context.Context) {
	session, v := p.state()
	if v == nil {
		return
	}
	fresh, err := p.client.Viewer(ctx)
	if errors.Is(err, ErrUnauthorized) {
		p.expire(session)
		return
	}
	if err != nil {
		return
	}
	p.authMu.Lock()
	defer p.authMu.Unlock()
	p.mu.Lock()
	same := p.session == session // not logged out or in again meanwhile
	if same {
		p.viewer = fresh
	}
	p.mu.Unlock()
	if same {
		_ = p.db.SetKV(kvViewer, fresh)
	}
}

// ---------------------------------------------------------------------------
// Collections

// Collection returns the user's anime ("ANIME") or manga ("MANGA") list. The
// result may be shared with other callers and must not be modified.
func (p *Platform) Collection(ctx context.Context, mediaType string, refresh bool) (*Collection, error) {
	if mediaType == "" {
		mediaType = "ANIME"
	}
	maxAge := 15 * time.Minute
	if refresh {
		maxAge = 5 * time.Second // only reuse a fetch that just finished
	}
	for attempt := 1; ; attempt++ {
		c, retry, err := p.collection(ctx, mediaType, maxAge)
		if !retry || attempt == 3 {
			return c, err
		}
	}
}

// collState is what Collection needs to know about the session and the
// cached copy, read under one lock so that it is consistent.
type collState struct {
	session uint64
	viewer  *Viewer     // nil when logged out
	cached  *Collection // last fetched copy (maybe patched since), or nil
	version uint64
	fresh   bool // cached is recent enough to be returned as is
}

func (p *Platform) collState(mediaType string, maxAge time.Duration) collState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	st := collState{session: p.session, cached: p.collections[mediaType], version: p.versions[mediaType]}
	if p.client.Token() != "" {
		st.viewer = p.viewer
	}
	at, ok := p.fetchedAt[mediaType]
	st.fresh = st.cached != nil && ok && time.Since(at) < maxAge
	return st
}

// collection makes one attempt at Collection. retry reports that AniList
// rejected the token: the session has ended (or another one started) since,
// so the call should be repeated against the new state.
func (p *Platform) collection(ctx context.Context, mediaType string, maxAge time.Duration) (_ *Collection, retry bool, _ error) {
	st := p.collState(mediaType, maxAge)
	if st.viewer == nil {
		c, err := p.localCollection(mediaType)
		return c, false, err
	}
	if st.fresh {
		return st.cached, false, nil
	}

	select {
	case p.fetchSem <- struct{}{}:
		defer func() { <-p.fetchSem }()
	case <-ctx.Done():
		if st.cached != nil {
			return st.cached, false, nil
		}
		return nil, false, ctx.Err()
	}
	// While we waited, someone else may have refreshed the list, or found
	// the token expired and logged the user out.
	st = p.collState(mediaType, maxAge)
	if st.viewer == nil {
		c, err := p.localCollection(mediaType)
		return c, false, err
	}
	if st.fresh {
		return st.cached, false, nil
	}

	got, err := p.client.Collection(ctx, st.viewer.ID, mediaType)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			p.expire(st.session)
			return nil, true, err
		}
		// Offline: fall back to the last copy we have.
		if st.cached != nil {
			return st.cached, false, nil
		}
		if saved := p.loadSavedCollection(st.viewer.ID, mediaType); saved != nil {
			return saved, false, nil
		}
		return nil, false, err
	}

	p.mu.Lock()
	// After a logout or login the result belongs to another session, and
	// after a list update it may predate the update: return it, but don't
	// cache it.
	current := p.session == st.session && p.versions[mediaType] == st.version
	if current {
		p.collections[mediaType] = got
		p.fetchedAt[mediaType] = time.Now()
	}
	p.mu.Unlock()
	if current {
		p.db.SetCache(collectionKey(mediaType), savedCollection{UserID: st.viewer.ID, Collection: got}, 0)
	}
	// Seed the media cache so detail lookups don't need extra requests.
	for _, e := range got.Entries() {
		p.db.SetCache(liteKey(e.MediaID), e.Media, 24*time.Hour)
	}
	return got, false, nil
}

// invalidateLocked marks the cached collection of a media type as outdated:
// the next read refetches it, and a fetch already running isn't cached.
// p.mu must be held.
func (p *Platform) invalidateLocked(mediaType string) {
	delete(p.fetchedAt, mediaType)
	p.versions[mediaType]++
}

func collectionKey(mediaType string) string { return "collection:" + mediaType }

// savedCollection is the copy of a collection kept in the database as an
// offline fallback. It records whose list it is, so that after switching
// accounts the previous user's list is never shown.
type savedCollection struct {
	UserID     int         `json:"userId"`
	Collection *Collection `json:"collection"`
}

func (p *Platform) loadSavedCollection(userID int, mediaType string) *Collection {
	var s savedCollection
	if !p.db.GetStaleCache(collectionKey(mediaType), &s) || s.UserID != userID {
		return nil
	}
	return s.Collection
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

// HasDetails reports an anime whose full page data is saved (and fresh).
func (p *Platform) HasDetails(id int) bool {
	var m Media
	return p.db.GetCache(detailKey(id), &m)
}

// Media returns full media details (description, relations, characters...).
func (p *Platform) Media(ctx context.Context, id int, refresh bool) (*Media, error) {
	var m Media
	if !refresh && p.db.GetCache(detailKey(id), &m) {
		p.attachListEntry(ctx, &m)
		return &m, nil
	}
	session, _ := p.state()
	fresh, err := p.client.Media(ctx, id)
	if errors.Is(err, ErrUnauthorized) {
		// The token was rejected: log out, then retry as a guest.
		p.expire(session)
		fresh, err = p.client.Media(ctx, id)
	}
	if err != nil {
		if p.db.GetStaleCache(detailKey(id), &m) {
			p.attachListEntry(ctx, &m)
			return &m, nil
		}
		return nil, err
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

// cachedEntry returns the media's entry in the cached AniList lists, even
// one due for a refresh, without asking AniList. It is nil when they don't
// have it, or there are none (when logged out).
func (p *Platform) cachedEntry(mediaID int) *ListEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, c := range p.collections {
		if e := c.Find(mediaID); e != nil {
			return e
		}
	}
	return nil
}

// MediaLite returns the lightweight media object, looking in the collection
// and cache before hitting the network.
func (p *Platform) MediaLite(ctx context.Context, id int) (*Media, error) {
	if id <= 0 {
		return nil, errors.New("invalid media id")
	}
	if e := p.cachedEntry(id); e != nil {
		return e.Media, nil
	}
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
	if err != nil {
		// A page failed: prefer an older complete copy, else show what we
		// got, but never cache an incomplete schedule.
		if p.db.GetStaleCache(key, &out) {
			return out, nil
		}
		if len(res) == 0 {
			return nil, err
		}
		return res, nil
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
	if session, v := p.state(); v != nil {
		saved, err := p.client.SaveEntry(ctx, u)
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				p.expire(session)
			}
			return err
		}
		p.patchCachedEntry(session, mt, media, u, saved)
		p.hub.Publish(events.CollectionUpdate, map[string]any{"mediaId": u.MediaID})
		return nil
	}

	// Local list. A single statement, so concurrent updates of one entry
	// can't undo each other: fields the update leaves out keep their value.
	raw, _ := json.Marshal(media.Lite())
	_, err = p.db.Write(`INSERT INTO local_list(media_id, type, status, progress, score, repeat, media, updated_at)
		VALUES(?1, ?2, COALESCE(?3, 'PLANNING'), COALESCE(?4, 0), COALESCE(?5, 0), COALESCE(?6, 0), ?7, ?8)
		ON CONFLICT(media_id) DO UPDATE SET status = COALESCE(?3, status), progress = COALESCE(?4, progress),
			score = COALESCE(?5, score), repeat = COALESCE(?6, repeat), media = excluded.media, updated_at = excluded.updated_at`,
		u.MediaID, mt, orNull(u.Status), orNull(u.Progress), orNull(u.Score), orNull(u.Repeat), string(raw), time.Now().Unix())
	if err == nil {
		p.hub.Publish(events.CollectionUpdate, map[string]any{"mediaId": u.MediaID})
	}
	return err
}

// orNull is v's value, or nil (SQL NULL) when v is nil.
func orNull[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}

// patchCachedEntry applies an update AniList accepted to the cached
// collection, so the UI reflects it before the next refresh, and marks the
// cache outdated. saved is the entry as AniList returned it, if it did. The
// change goes into a new Collection: readers keep the snapshot they have.
func (p *Platform) patchCachedEntry(session uint64, mt string, media *Media, u EntryUpdate, saved *ListEntryLite) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != session {
		return // logged out, or into another account, since the update
	}
	p.invalidateLocked(mt)
	c := p.collections[mt]
	if c == nil {
		return
	}
	e := ListEntry{MediaID: u.MediaID, Media: media, Status: "PLANNING"}
	if old := c.Find(u.MediaID); old != nil {
		e = *old
	}
	u.applyTo(&e)
	if saved != nil {
		if saved.ID != 0 {
			e.ID = saved.ID
		}
		if saved.Status != "" {
			e.Status = saved.Status
		}
		e.Progress, e.Score, e.Repeat = saved.Progress, saved.Score, saved.Repeat
	}
	e.UpdatedAt = time.Now().Unix()
	p.collections[mt] = c.withEntry(&e)
}

// DeleteEntry removes a media from the list.
func (p *Platform) DeleteEntry(ctx context.Context, mediaID int) error {
	session, v := p.state()
	if v == nil {
		if _, err := p.db.Write(`DELETE FROM local_list WHERE media_id = ?`, mediaID); err != nil {
			return err
		}
		p.hub.Publish(events.CollectionUpdate, map[string]any{"mediaId": mediaID})
		return nil
	}
	media, _ := p.MediaLite(ctx, mediaID)
	mt := "ANIME"
	if media != nil {
		mt = typeOr(media.Type)
	}
	c, err := p.Collection(ctx, mt, false)
	if err != nil {
		return err
	}
	if now, _ := p.state(); now != session {
		// Logged out (the token expired) or into another account meanwhile:
		// c isn't the list the entry was meant to leave.
		return errors.New("your AniList login changed, please try again")
	}
	e := c.Find(mediaID)
	if e == nil {
		return nil
	}
	if err := p.client.DeleteEntry(ctx, e.ID); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			p.expire(session)
		}
		return err
	}
	p.mu.Lock()
	if p.session == session {
		p.invalidateLocked(mt)
		if c := p.collections[mt]; c != nil {
			p.collections[mt] = c.without(mediaID)
		}
	}
	p.mu.Unlock()
	p.hub.Publish(events.CollectionUpdate, map[string]any{"mediaId": mediaID})
	return nil
}

// UpdateProgress records that the user finished `episode` of a media; see
// progressUpdate for the rules. It reports whether the list was changed.
func (p *Platform) UpdateProgress(ctx context.Context, mediaID, episode int) (bool, error) {
	media, err := p.MediaLite(ctx, mediaID)
	if err != nil {
		return false, err
	}
	c, err := p.Collection(ctx, typeOr(media.Type), false)
	if err != nil {
		return false, err
	}
	total := 0
	if media.Episodes != nil {
		total = *media.Episodes
	}
	u, ok := progressUpdate(c.Find(mediaID), mediaID, episode, total, time.Now())
	if !ok {
		return false, nil
	}
	if err := p.UpdateEntry(ctx, u); err != nil {
		return false, err
	}
	return true, nil
}

// progressUpdate works out the list update for having finished `episode` of
// a media with `total` episodes (0 when unknown), given its list entry (nil
// when it isn't in the list). ok is false when nothing needs to change.
//
// Progress only moves forward: finishing an episode at or before the
// recorded progress changes nothing, except that a planned show becomes
// "watching" (keeping its progress). The one exception is watching a
// completed show again, which starts a rewatch from that episode.
//
//	not in list, PLANNING, PAUSED, DROPPED -> CURRENT
//	COMPLETED, before the last episode     -> REPEATING
//	reaching the last episode              -> COMPLETED (a rewatch: repeat+1)
//
// The start and finish dates are filled in when they are still empty.
func progressUpdate(entry *ListEntry, mediaID, episode, total int, now time.Time) (u EntryUpdate, ok bool) {
	u.MediaID = mediaID
	if episode <= 0 {
		return u, false
	}
	status, progress := "CURRENT", episode
	if entry != nil {
		switch entry.Status {
		case "COMPLETED":
			if total == 0 || episode >= total {
				return u, false // already finished
			}
			status = "REPEATING"
		case "REPEATING":
			if episode <= entry.Progress {
				return u, false
			}
			status = "REPEATING"
		case "PLANNING":
			progress = max(episode, entry.Progress)
		default: // CURRENT, PAUSED, DROPPED
			if episode <= entry.Progress {
				return u, false
			}
		}
	}
	today := fuzzyDate(now)
	if needsStartDate(entry) {
		u.StartedAt = today
	}
	if total > 0 && progress >= total {
		progress = total
		if entry != nil && entry.Status == "REPEATING" {
			r := entry.Repeat + 1
			u.Repeat = &r
		}
		status = "COMPLETED"
		if entry == nil || entry.CompletedAt.Year == nil {
			u.CompletedAt = today
		}
	}
	u.Status, u.Progress = &status, &progress
	return u, true
}

// StartWatching records that the user started playing a media; see
// watchingUpdate for the rules. It reports whether the list was changed.
// AniList isn't asked when the cached list already has the media as
// watching, rewatching or completed.
func (p *Platform) StartWatching(ctx context.Context, mediaID int) (bool, error) {
	if e := p.cachedEntry(mediaID); e != nil {
		if _, ok := watchingUpdate(e, mediaID, time.Now()); !ok {
			return false, nil
		}
	}
	media, err := p.MediaLite(ctx, mediaID)
	if err != nil {
		return false, err
	}
	c, err := p.Collection(ctx, typeOr(media.Type), false)
	if err != nil {
		return false, err
	}
	u, ok := watchingUpdate(c.Find(mediaID), mediaID, time.Now())
	if !ok {
		return false, nil
	}
	if err := p.UpdateEntry(ctx, u); err != nil {
		return false, err
	}
	return true, nil
}

// watchingUpdate works out the list update for having started to play a
// media, given its list entry (nil when it isn't in the list). ok is false
// when nothing needs to change. Only the status (and start date) changes,
// the progress is kept:
//
//	not in list, PLANNING, PAUSED, DROPPED -> CURRENT
//	CURRENT, REPEATING, COMPLETED          -> unchanged
//
// A completed show is left as it is: finishing one of its episodes starts
// a rewatch (see progressUpdate). The start date is filled in like there.
func watchingUpdate(entry *ListEntry, mediaID int, now time.Time) (u EntryUpdate, ok bool) {
	u.MediaID = mediaID
	if entry != nil {
		switch entry.Status {
		case "CURRENT", "REPEATING", "COMPLETED":
			return u, false
		}
	}
	status := "CURRENT"
	u.Status = &status
	if needsStartDate(entry) {
		u.StartedAt = fuzzyDate(now)
	}
	return u, true
}

// needsStartDate reports whether a media the user starts watching now gets
// today as its start date: it has none, and wasn't being watched already
// (progress without a start date means it started on an unknown day).
func needsStartDate(entry *ListEntry) bool {
	return entry == nil || (entry.StartedAt.Year == nil && (entry.Status == "PLANNING" || entry.Progress == 0))
}

// fuzzyDate returns the day of t.
func fuzzyDate(t time.Time) *FuzzyDate {
	y, m, d := t.Date()
	month := int(m)
	return &FuzzyDate{Year: &y, Month: &month, Day: &d}
}
