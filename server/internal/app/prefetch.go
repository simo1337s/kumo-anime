package app

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Every anime with files, this computer's or shared with it by another
// Kumo, gets all its data downloaded once: its AniList page, ani.zip's
// episode titles and summaries, its artwork and episode thumbnails. Its page
// then opens at once, also offline. One anime every few seconds when they
// have to be fetched, which leaves room under AniList's rate limit for the
// rest of the app.

const (
	// Between two anime fetched from AniList.
	prefetchEvery = 3 * time.Second
	// Done again after this long: new episodes, new artwork.
	prefetchedFor = 30 * 24 * time.Hour
)

type prefetcher struct {
	mu     sync.Mutex
	queue  []int
	queued map[int]bool
	wake   chan struct{}
}

func newPrefetcher() *prefetcher {
	return &prefetcher{queued: map[int]bool{}, wake: make(chan struct{}, 1)}
}

func prefetchedKey(id int) string { return fmt.Sprintf("prefetched:%d", id) }

// PrefetchAnime downloads the data of these anime, those that don't have it
// yet, in the background.
func (a *App) PrefetchAnime(ids ...int) {
	p := a.prefetch
	added := false
	p.mu.Lock()
	for _, id := range ids {
		var done bool
		if id <= 0 || p.queued[id] || a.DB.GetCache(prefetchedKey(id), &done) {
			continue
		}
		p.queued[id] = true
		p.queue = append(p.queue, id)
		added = true
	}
	p.mu.Unlock()
	if added {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

// prefetchLibrary queues every anime with files: after a scan, when the
// libraries shared with this Kumo change, at startup.
func (a *App) prefetchLibrary() {
	files, _ := a.Files.All()
	files = append(files, a.Share.RemoteFiles()...)
	seen := map[int]bool{}
	var ids []int
	for _, f := range files {
		if f.MediaID > 0 && !f.Ignored && !seen[f.MediaID] {
			seen[f.MediaID] = true
			ids = append(ids, f.MediaID)
		}
	}
	a.PrefetchAnime(ids...)
}

// runPrefetch downloads the queued anime's data, one at a time, until Kumo
// stops.
func (a *App) runPrefetch(ctx context.Context) {
	p := a.prefetch
	for {
		p.mu.Lock()
		id := 0
		if len(p.queue) > 0 {
			id, p.queue = p.queue[0], p.queue[1:]
		}
		p.mu.Unlock()
		if id == 0 {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			}
			continue
		}
		fetched := a.prefetchOne(ctx, id)
		p.mu.Lock()
		delete(p.queued, id)
		p.mu.Unlock()
		wait := 50 * time.Millisecond
		if fetched {
			wait = prefetchEvery
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// prefetchOne downloads an anime's data, and reports whether it had to ask
// AniList. Not marked done when a part couldn't be had (AniList or ani.zip
// not answering): it's tried again next time.
func (a *App) prefetchOne(ctx context.Context, id int) (fetched bool) {
	fetched = !a.Platform.HasDetails(id)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	e, err := a.Library.Entry(ctx, id, false)
	if err != nil || e == nil || e.Media == nil {
		return fetched
	}
	a.PrefetchEntryArt(e)
	if e.MetadataNote == "" {
		a.DB.SetCache(prefetchedKey(id), true, prefetchedFor)
	}
	return fetched
}
