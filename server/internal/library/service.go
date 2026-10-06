package library

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/history"
	"github.com/simo1337s/animetest/server/internal/metadata"
)

// Service builds the views the UI needs by combining the AniList list,
// local files, episode metadata and watch history.
type Service struct {
	Store    *Store
	Scanner  *Scanner
	Platform *anilist.Platform
	Meta     *metadata.Service
	History  *history.Store
	DB       *db.DB // items removed from "Continue watching"

	hiddenMu sync.Mutex
}

type EpisodeView struct {
	Number   int            `json:"number"`
	Title    string         `json:"title"`
	Image    string         `json:"image"`
	Summary  string         `json:"summary"`
	AirDate  string         `json:"airDate"`
	Runtime  int            `json:"runtime"`
	Kind     string         `json:"kind"` // main | special | nc
	File     *LocalFile     `json:"file"`
	Watched  bool           `json:"watched"`
	Aired    bool           `json:"aired"`
	History  *history.Entry `json:"history"`
	ResumeAt float64        `json:"resumeAt"`
	HasFile  bool           `json:"hasFile"`
}

type EntryView struct {
	Media        *anilist.Media     `json:"media"`
	ListEntry    *anilist.ListEntry `json:"listEntry"`
	Episodes     []*EpisodeView     `json:"episodes"`
	Specials     []*EpisodeView     `json:"specials"`
	Others       []*EpisodeView     `json:"others"`
	NextEpisode  *EpisodeView       `json:"nextEpisode"`
	LocalCount   int                `json:"localCount"`
	Images       metadata.Images    `json:"images"`
	Mappings     metadata.Mappings  `json:"mappings"`
	MetadataNote string             `json:"metadataNote,omitempty"`
}

// Entry returns everything the anime page needs.
func (s *Service) Entry(ctx context.Context, mediaID int, refresh bool) (*EntryView, error) {
	media, err := s.Platform.Media(ctx, mediaID, refresh)
	if err != nil {
		return nil, err
	}
	coll, _ := s.Platform.Collection(ctx, "ANIME", false)
	var entry *anilist.ListEntry
	if coll != nil {
		entry = coll.Find(mediaID)
	}
	meta, metaErr := s.Meta.Get(ctx, mediaID)
	files, _ := s.Store.ByMedia(mediaID)
	hist := s.History.ForMedia(mediaID)

	v := &EntryView{Media: media, ListEntry: entry, LocalCount: len(files)}
	if meta != nil {
		v.Images, v.Mappings = meta.Images, meta.Mappings
	}
	if metaErr != nil {
		v.MetadataNote = "Episode metadata unavailable: " + metaErr.Error()
	}
	progress := 0
	if entry != nil {
		progress = entry.Progress
	}

	streamTitles := map[int]anilist.StreamingEpisode{}
	for i, se := range media.StreamingEpisodes {
		streamTitles[i+1] = se
	}

	mainFiles := map[int]*LocalFile{}
	for _, f := range files {
		switch {
		case f.Kind == "main" && f.Episode > 0:
			if old, ok := mainFiles[f.Episode]; !ok || f.Size > old.Size {
				mainFiles[f.Episode] = f
			}
		case f.Kind == "nc":
			v.Others = append(v.Others, &EpisodeView{Number: f.Episode, Title: f.Name, Kind: "nc", File: f, HasFile: true, Aired: true})
		default:
			sp := &EpisodeView{Number: f.Episode, Title: f.Name, Kind: "special", File: f, HasFile: true, Aired: true}
			if f.Parsed.EpisodeTitle != "" {
				sp.Title = f.Parsed.EpisodeTitle
			}
			v.Specials = append(v.Specials, sp)
		}
	}

	total := media.TotalEpisodes()
	aired := media.AiredEpisodes()
	if media.Status == "FINISHED" || media.Status == "CANCELLED" {
		aired = total
	}
	maxEp := total
	for n := range mainFiles {
		maxEp = max(maxEp, n)
	}
	if meta != nil {
		maxEp = max(maxEp, len(meta.Main()))
	}
	if total > 0 {
		// Don't invent episodes past the known total unless files exist.
		maxEp = max(total, maxLocal(mainFiles))
	}
	for n := 1; n <= maxEp; n++ {
		ep := &EpisodeView{Number: n, Kind: "main", Watched: n <= progress, Aired: aired == 0 || n <= aired}
		if md := meta.Get(n); md != nil {
			ep.Title, ep.Image, ep.Summary, ep.AirDate, ep.Runtime = md.Title, md.Image, md.Summary, md.AirDate, md.Runtime
		}
		if se, ok := streamTitles[n]; ok {
			if ep.Title == "" {
				ep.Title = se.Title
			}
			if ep.Image == "" {
				ep.Image = se.Thumbnail
			}
		}
		if ep.Image == "" {
			ep.Image = firstNonEmpty(media.BannerImage, media.CoverImage.ExtraLarge)
		}
		if ep.Runtime == 0 && media.Duration != nil {
			ep.Runtime = *media.Duration
		}
		if f, ok := mainFiles[n]; ok {
			ep.File, ep.HasFile, ep.Aired = f, true, true
		}
		if h, ok := hist[n]; ok {
			ep.History = h
			ep.ResumeAt = s.History.ResumePosition(mediaID, n)
		}
		v.Episodes = append(v.Episodes, ep)
	}
	if media.Format == "MOVIE" && len(v.Episodes) == 0 {
		v.Episodes = append(v.Episodes, &EpisodeView{Number: 1, Kind: "main", Title: media.PreferredTitle(), Image: firstNonEmpty(media.BannerImage, media.CoverImage.ExtraLarge), Aired: true})
	}

	// Next episode to watch: first unwatched one after progress.
	for _, ep := range v.Episodes {
		if ep.Number == progress+1 {
			v.NextEpisode = ep
			break
		}
	}
	if v.NextEpisode == nil && entry != nil && entry.Status == "COMPLETED" && len(v.Episodes) > 0 {
		v.NextEpisode = v.Episodes[0]
	}
	if v.NextEpisode == nil && entry == nil && len(v.Episodes) > 0 {
		v.NextEpisode = v.Episodes[0]
	}
	return v, nil
}

func maxLocal(m map[int]*LocalFile) int {
	n := 0
	for k := range m {
		n = max(n, k)
	}
	return n
}

// ---------------------------------------------------------------------------
// Home / library collection view

type CollectionItem struct {
	Media       *anilist.Media     `json:"media"`
	ListEntry   *anilist.ListEntry `json:"listEntry"`
	LocalFiles  int                `json:"localFiles"`
	Downloaded  []int              `json:"downloaded"`
	NextEpisode int                `json:"nextEpisode"`
	NextHasFile bool               `json:"nextHasFile"`
	LastWatched int64              `json:"lastWatched"`
}

type CollectionList struct {
	Status string            `json:"status"`
	Name   string            `json:"name"`
	Items  []*CollectionItem `json:"items"`
}

type ContinueItem struct {
	Media       *anilist.Media `json:"media"`
	Episode     int            `json:"episode"`
	Total       int            `json:"total"`
	Title       string         `json:"title"`
	Image       string         `json:"image"`
	Runtime     int            `json:"runtime"`
	HasFile     bool           `json:"hasFile"`
	FilePath    string         `json:"filePath,omitempty"`
	ResumeAt    float64        `json:"resumeAt"`
	Duration    float64        `json:"duration"`
	LastWatched int64          `json:"lastWatched"`
	Source      string         `json:"source"`
}

type CollectionView struct {
	Lists            []*CollectionList `json:"lists"`
	ContinueWatching []*ContinueItem   `json:"continueWatching"`
	UnmatchedCount   int               `json:"unmatchedCount"`
	IgnoredCount     int               `json:"ignoredCount"`
	LocalOnly        []*CollectionItem `json:"localOnly"` // matched files not in the list
	Genres           []string          `json:"genres"`
}

var statusNames = map[string]string{
	"CURRENT": "Currently watching", "REPEATING": "Rewatching", "PLANNING": "Planning",
	"PAUSED": "Paused", "COMPLETED": "Completed", "DROPPED": "Dropped",
}

func (s *Service) Collection(ctx context.Context, refresh bool) (*CollectionView, error) {
	coll, err := s.Platform.Collection(ctx, "ANIME", refresh)
	if err != nil {
		return nil, err
	}
	all, _ := s.Store.All()
	filesByMedia := map[int][]*LocalFile{}
	view := &CollectionView{}
	for _, f := range all {
		if f.Ignored {
			view.IgnoredCount++
			continue
		}
		if f.MediaID == 0 {
			view.UnmatchedCount++
			continue
		}
		filesByMedia[f.MediaID] = append(filesByMedia[f.MediaID], f)
	}
	last := s.History.LastWatched()

	makeItem := func(e *anilist.ListEntry, media *anilist.Media) *CollectionItem {
		it := &CollectionItem{Media: media, ListEntry: e, LastWatched: last[media.ID]}
		progress := 0
		if e != nil {
			progress = e.Progress
		}
		it.NextEpisode = progress + 1
		seen := map[int]bool{}
		for _, f := range filesByMedia[media.ID] {
			if f.Kind == "main" && f.Episode > 0 && !seen[f.Episode] {
				seen[f.Episode] = true
				it.Downloaded = append(it.Downloaded, f.Episode)
				if f.Episode == it.NextEpisode {
					it.NextHasFile = true
				}
			}
		}
		sort.Ints(it.Downloaded)
		it.LocalFiles = len(filesByMedia[media.ID])
		return it
	}

	genres := map[string]int{}
	inList := map[int]bool{}
	byStatus := map[string]*CollectionList{}
	for _, e := range coll.Entries() {
		inList[e.MediaID] = true
		for _, g := range e.Media.Genres {
			genres[g]++
		}
		l, ok := byStatus[e.Status]
		if !ok {
			l = &CollectionList{Status: e.Status, Name: statusNames[e.Status]}
			byStatus[e.Status] = l
		}
		l.Items = append(l.Items, makeItem(e, e.Media))
	}
	for _, st := range anilist.Statuses {
		if l, ok := byStatus[st]; ok {
			sort.SliceStable(l.Items, func(i, j int) bool {
				a, b := l.Items[i], l.Items[j]
				if a.LastWatched != b.LastWatched {
					return a.LastWatched > b.LastWatched
				}
				if a.ListEntry != nil && b.ListEntry != nil && a.ListEntry.UpdatedAt != b.ListEntry.UpdatedAt {
					return a.ListEntry.UpdatedAt > b.ListEntry.UpdatedAt
				}
				return a.Media.PreferredTitle() < b.Media.PreferredTitle()
			})
			view.Lists = append(view.Lists, l)
		}
	}

	// Matched files whose anime isn't in the list.
	var missing []int
	for id := range filesByMedia {
		if !inList[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		medias, _ := s.Platform.MediaBatch(ctx, missing)
		for _, id := range missing {
			if m, ok := medias[id]; ok {
				view.LocalOnly = append(view.LocalOnly, makeItem(nil, m))
			}
		}
		sort.Slice(view.LocalOnly, func(i, j int) bool {
			return view.LocalOnly[i].Media.PreferredTitle() < view.LocalOnly[j].Media.PreferredTitle()
		})
	}

	view.ContinueWatching = s.continueWatching(ctx, coll, filesByMedia)
	for g := range genres {
		view.Genres = append(view.Genres, g)
	}
	sort.Slice(view.Genres, func(i, j int) bool {
		if genres[view.Genres[i]] != genres[view.Genres[j]] {
			return genres[view.Genres[i]] > genres[view.Genres[j]]
		}
		return view.Genres[i] < view.Genres[j]
	})
	return view, nil
}

func (s *Service) continueWatching(ctx context.Context, coll *anilist.Collection, files map[int][]*LocalFile) []*ContinueItem {
	var out []*ContinueItem
	seen := map[int]bool{}
	last := s.History.LastWatched()
	hidden := s.hiddenContinue()

	add := func(media *anilist.Media, ep int, src string) {
		if media == nil || seen[media.ID] || ep <= 0 {
			return
		}
		// Removed by the user: stays away until the episode to continue
		// with changes or the anime is watched again.
		if h, ok := hidden[media.ID]; ok && h.Episode == ep && last[media.ID] <= h.HiddenAt {
			seen[media.ID] = true // not offered again from the history below
			return
		}
		total := media.TotalEpisodes()
		if total > 0 && ep > total {
			return
		}
		if aired := media.AiredEpisodes(); media.Status == "RELEASING" && aired > 0 && ep > aired {
			return
		}
		it := &ContinueItem{Media: media, Episode: ep, Total: total, LastWatched: last[media.ID], Source: src}
		for _, f := range files[media.ID] {
			if f.Kind == "main" && f.Episode == ep {
				it.HasFile, it.FilePath = true, f.Path
				break
			}
		}
		if h := s.History.Get(media.ID, ep); h != nil {
			it.ResumeAt = s.History.ResumePosition(media.ID, ep)
			it.Duration = h.Duration
		}
		if media.Duration != nil {
			it.Runtime = *media.Duration
		}
		it.Image = firstNonEmpty(media.BannerImage, media.CoverImage.ExtraLarge)
		if meta, err := s.Meta.Get(ctx, media.ID); err == nil {
			if md := meta.Get(ep); md != nil {
				it.Title = md.Title
				if md.Image != "" {
					it.Image = md.Image
				}
			}
		}
		seen[media.ID] = true
		out = append(out, it)
	}

	// 1) Shows being watched, ordered by most recent activity.
	var watching []*anilist.ListEntry
	for _, e := range coll.Entries() {
		if e.Status == "CURRENT" || e.Status == "REPEATING" {
			watching = append(watching, e)
		}
	}
	sort.SliceStable(watching, func(i, j int) bool {
		li, lj := last[watching[i].MediaID], last[watching[j].MediaID]
		if li != lj {
			return li > lj
		}
		return watching[i].UpdatedAt > watching[j].UpdatedAt
	})
	for _, e := range watching {
		ep := e.Progress + 1
		// A partially watched episode takes priority over the next one.
		if h := s.History.Get(e.MediaID, e.Progress+1); h == nil {
			if hc := s.History.Get(e.MediaID, e.Progress); hc != nil && hc.Percent() < 0.85 && e.Progress > 0 {
				ep = e.Progress
			}
		}
		add(e.Media, ep, "list")
		if len(out) >= 20 {
			break
		}
	}
	// 2) Recently played things not on the list (local-only, streams).
	for _, h := range s.History.Recent(30) {
		if seen[h.MediaID] || len(out) >= 24 {
			continue
		}
		media, err := s.Platform.MediaLite(ctx, h.MediaID)
		if err != nil {
			continue
		}
		ep := h.Episode
		if h.Percent() >= 0.85 {
			ep++
		}
		add(media, ep, h.Source)
	}
	// Most recently watched first, list-only items keep their order after.
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastWatched > out[j].LastWatched })
	return out
}

// continueHiddenKey holds the items removed from "Continue watching", by
// media id: the episode the item offered and when it was removed.
const continueHiddenKey = "continue-hidden"

type hiddenItem struct {
	Episode  int   `json:"episode"`
	HiddenAt int64 `json:"hiddenAt"` // unix seconds, like the watch history
}

func (s *Service) hiddenContinue() map[int]hiddenItem {
	out := map[int]hiddenItem{}
	_, _ = s.DB.GetKV(continueHiddenKey, &out)
	return out
}

// updateHidden changes the removed items under a lock, so requests at the
// same time can't overwrite each other's changes.
func (s *Service) updateHidden(fn func(items map[int]hiddenItem)) error {
	s.hiddenMu.Lock()
	defer s.hiddenMu.Unlock()
	items := map[int]hiddenItem{}
	if _, err := s.DB.GetKV(continueHiddenKey, &items); err != nil {
		return err
	}
	fn(items)
	if len(items) == 0 {
		return s.DB.DeleteKV(continueHiddenKey)
	}
	return s.DB.SetKV(continueHiddenKey, items)
}

// HideContinue removes an anime from "Continue watching" for as long as it
// offers that episode: it comes back by itself once the episode changes
// (progress made elsewhere) or the anime is watched again.
func (s *Service) HideContinue(mediaID, episode int) error {
	last := s.History.LastWatched()
	now := time.Now().Unix()
	return s.updateHidden(func(items map[int]hiddenItem) {
		for id, it := range items {
			if last[id] > it.HiddenAt {
				delete(items, id) // watched again since: back for good
			}
		}
		items[mediaID] = hiddenItem{Episode: episode, HiddenAt: now}
	})
}

// UnhideContinue puts a removed anime back in "Continue watching".
func (s *Service) UnhideContinue(mediaID int) error {
	return s.updateHidden(func(items map[int]hiddenItem) { delete(items, mediaID) })
}
