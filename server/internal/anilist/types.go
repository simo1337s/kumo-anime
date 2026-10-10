package anilist

import (
	"strings"
	"sync/atomic"
)

// The JSON field names mirror AniList's GraphQL schema so responses decode
// straight into these structs and the frontend sees the familiar names.

type FuzzyDate struct {
	Year  *int `json:"year"`
	Month *int `json:"month"`
	Day   *int `json:"day"`
}

type Title struct {
	Romaji        string `json:"romaji"`
	English       string `json:"english"`
	Native        string `json:"native"`
	UserPreferred string `json:"userPreferred"`
}

type CoverImage struct {
	ExtraLarge string `json:"extraLarge"`
	Large      string `json:"large"`
	Medium     string `json:"medium"`
	Color      string `json:"color"`
}

type AiringEpisode struct {
	ID              int    `json:"id,omitempty"`
	AiringAt        int    `json:"airingAt"`
	TimeUntilAiring int    `json:"timeUntilAiring"`
	Episode         int    `json:"episode"`
	Media           *Media `json:"media,omitempty"`
}

type Studio struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Tag struct {
	Name           string `json:"name"`
	Rank           int    `json:"rank"`
	IsMediaSpoiler bool   `json:"isMediaSpoiler"`
}

type Trailer struct {
	ID        string `json:"id"`
	Site      string `json:"site"`
	Thumbnail string `json:"thumbnail"`
}

type Person struct {
	ID   int `json:"id"`
	Name struct {
		Full string `json:"full"`
	} `json:"name"`
	Image struct {
		Large string `json:"large"`
	} `json:"image"`
}

type CharacterEdge struct {
	Role        string   `json:"role"`
	Node        Person   `json:"node"`
	VoiceActors []Person `json:"voiceActors"`
}

type RelationEdge struct {
	RelationType string `json:"relationType"`
	Node         *Media `json:"node"`
}

type Ranking struct {
	Rank    int    `json:"rank"`
	Type    string `json:"type"`
	AllTime bool   `json:"allTime"`
	Year    *int   `json:"year"`
	Season  string `json:"season"`
	Context string `json:"context"`
}

type StreamingEpisode struct {
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
	URL       string `json:"url"`
	Site      string `json:"site"`
}

type ListEntryLite struct {
	ID       int     `json:"id"`
	Status   string  `json:"status"`
	Progress int     `json:"progress"`
	Score    float64 `json:"score"`
	Repeat   int     `json:"repeat"`
}

type Media struct {
	ID                int            `json:"id"`
	IDMal             *int           `json:"idMal"`
	Type              string         `json:"type"`
	Format            string         `json:"format"`
	Status            string         `json:"status"`
	Season            string         `json:"season"`
	SeasonYear        *int           `json:"seasonYear"`
	Episodes          *int           `json:"episodes"`
	Chapters          *int           `json:"chapters"`
	Volumes           *int           `json:"volumes"`
	Duration          *int           `json:"duration"`
	IsAdult           bool           `json:"isAdult"`
	Title             Title          `json:"title"`
	Synonyms          []string       `json:"synonyms"`
	CoverImage        CoverImage     `json:"coverImage"`
	BannerImage       string         `json:"bannerImage"`
	Genres            []string       `json:"genres"`
	AverageScore      *int           `json:"averageScore"`
	MeanScore         *int           `json:"meanScore"`
	Popularity        int            `json:"popularity"`
	StartDate         FuzzyDate      `json:"startDate"`
	EndDate           FuzzyDate      `json:"endDate"`
	NextAiringEpisode *AiringEpisode `json:"nextAiringEpisode"`
	Description       string         `json:"description,omitempty"`
	CountryOfOrigin   string         `json:"countryOfOrigin,omitempty"`
	Source            string         `json:"source,omitempty"`
	SiteURL           string         `json:"siteUrl,omitempty"`

	// Detail-only fields
	Trailer *Trailer `json:"trailer,omitempty"`
	Studios *struct {
		Nodes []Studio `json:"nodes"`
	} `json:"studios,omitempty"`
	Tags      []Tag `json:"tags,omitempty"`
	Relations *struct {
		Edges []RelationEdge `json:"edges"`
	} `json:"relations,omitempty"`
	Recommendations *struct {
		Nodes []struct {
			MediaRecommendation *Media `json:"mediaRecommendation"`
		} `json:"nodes"`
	} `json:"recommendations,omitempty"`
	Characters *struct {
		Edges []CharacterEdge `json:"edges"`
	} `json:"characters,omitempty"`
	Rankings          []Ranking          `json:"rankings,omitempty"`
	StreamingEpisodes []StreamingEpisode `json:"streamingEpisodes,omitempty"`
	MediaListEntry    *ListEntryLite     `json:"mediaListEntry,omitempty"`
}

// AllTitles returns every known title of the media (deduplicated).
func (m *Media) AllTitles() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range append([]string{m.Title.English, m.Title.Romaji, m.Title.UserPreferred, m.Title.Native}, m.Synonyms...) {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

// The language of the anime titles Kumo shows (Settings › Interface):
// English, where AniList has an English title, or Japanese in Latin letters
// (romaji). AniList's own "preferred" title is romaji unless the account says
// otherwise, and without an account.
var romajiTitles atomic.Bool

// SetTitleLanguage sets the language of PreferredTitle: "romaji", or English.
func SetTitleLanguage(lang string) { romajiTitles.Store(lang == "romaji") }

// PreferredTitle returns the title in the language chosen in the settings,
// or another when AniList has none in it.
func (m *Media) PreferredTitle() string {
	order := []string{m.Title.English, m.Title.Romaji, m.Title.UserPreferred, m.Title.Native}
	if romajiTitles.Load() {
		order = []string{m.Title.Romaji, m.Title.UserPreferred, m.Title.English, m.Title.Native}
	}
	return firstTitle(order)
}

// FolderTitle names an anime's folder for torrents. It doesn't follow the
// title language: changing it would start a second folder for the same anime.
func (m *Media) FolderTitle() string {
	return firstTitle([]string{m.Title.UserPreferred, m.Title.Romaji, m.Title.English, m.Title.Native})
}

func firstTitle(titles []string) string {
	for _, t := range titles {
		if t != "" {
			return t
		}
	}
	return ""
}

// EnglishOrRomaji prefers the English title (used for search providers).
func (m *Media) EnglishOrRomaji() string {
	if m.Title.English != "" {
		return m.Title.English
	}
	return m.PreferredTitle()
}

// TotalEpisodes returns the known episode count, falling back to the latest
// aired episode for ongoing shows.
func (m *Media) TotalEpisodes() int {
	if m.Episodes != nil && *m.Episodes > 0 {
		return *m.Episodes
	}
	if m.NextAiringEpisode != nil && m.NextAiringEpisode.Episode > 0 {
		return m.NextAiringEpisode.Episode - 1
	}
	return 0
}

// AiredEpisodes returns how many episodes are currently available.
func (m *Media) AiredEpisodes() int {
	if m.NextAiringEpisode != nil && m.NextAiringEpisode.Episode > 0 {
		return m.NextAiringEpisode.Episode - 1
	}
	if m.Status == "NOT_YET_RELEASED" {
		return 0
	}
	return m.TotalEpisodes()
}

// Year returns the start year or 0.
func (m *Media) Year() int {
	if m.SeasonYear != nil {
		return *m.SeasonYear
	}
	if m.StartDate.Year != nil {
		return *m.StartDate.Year
	}
	return 0
}

// Lite returns a copy without the heavy detail fields, suitable for lists.
func (m *Media) Lite() *Media {
	if m == nil {
		return nil
	}
	c := *m
	c.Description = ""
	c.Trailer = nil
	c.Studios = nil
	c.Tags = nil
	c.Relations = nil
	c.Recommendations = nil
	c.Characters = nil
	c.Rankings = nil
	c.MediaListEntry = nil
	c.StreamingEpisodes = nil
	return &c
}

type ListEntry struct {
	ID          int       `json:"id"`
	MediaID     int       `json:"mediaId"`
	Status      string    `json:"status"`
	Progress    int       `json:"progress"`
	Score       float64   `json:"score"`
	Repeat      int       `json:"repeat"`
	Notes       string    `json:"notes"`
	Private     bool      `json:"private"`
	StartedAt   FuzzyDate `json:"startedAt"`
	CompletedAt FuzzyDate `json:"completedAt"`
	UpdatedAt   int64     `json:"updatedAt"`
	Media       *Media    `json:"media"`
}

type List struct {
	Name         string       `json:"name"`
	Status       string       `json:"status"`
	IsCustomList bool         `json:"isCustomList"`
	Entries      []*ListEntry `json:"entries"`
}

// Collection is a user's list. The Platform shares one Collection between
// all its readers, so it is never modified in place: withEntry and without
// return a changed copy instead.
type Collection struct {
	Lists []*List `json:"lists"`
}

// Entries returns all non-custom-list entries, deduplicated by media id.
func (c *Collection) Entries() []*ListEntry {
	if c == nil {
		return nil
	}
	seen := map[int]bool{}
	var out []*ListEntry
	for _, l := range c.Lists {
		if l.IsCustomList {
			continue
		}
		for _, e := range l.Entries {
			if e.Media == nil || seen[e.MediaID] {
				continue
			}
			seen[e.MediaID] = true
			out = append(out, e)
		}
	}
	return out
}

// Find returns the entry for a media id.
func (c *Collection) Find(mediaID int) *ListEntry {
	for _, e := range c.Entries() {
		if e.MediaID == mediaID {
			return e
		}
	}
	return nil
}

// withEntry returns a copy of c in which e replaces the entry of the same
// media: in place if its status is unchanged, otherwise moved to the end of
// the list for its new status. It is added there if c doesn't have it yet.
// Custom lists keep the entry, with the new data. c is not modified.
func (c *Collection) withEntry(e *ListEntry) *Collection {
	out := &Collection{Lists: make([]*List, 0, len(c.Lists)+1)}
	placed := false
	var target *List
	for _, l := range c.Lists {
		nl := *l
		nl.Entries = make([]*ListEntry, 0, len(l.Entries)+1)
		for _, x := range l.Entries {
			switch {
			case x.MediaID != e.MediaID:
				nl.Entries = append(nl.Entries, x)
			case l.IsCustomList:
				nl.Entries = append(nl.Entries, e)
			case !placed && l.Status == e.Status:
				nl.Entries = append(nl.Entries, e)
				placed = true
			}
		}
		if target == nil && !l.IsCustomList && l.Status == e.Status {
			target = &nl
		}
		out.Lists = append(out.Lists, &nl)
	}
	if !placed {
		if target == nil {
			target = &List{Name: listNames[e.Status], Status: e.Status}
			out.Lists = append(out.Lists, target)
		}
		target.Entries = append(target.Entries, e)
	}
	return out
}

// without returns a copy of c without the entries of a media; c is not
// modified.
func (c *Collection) without(mediaID int) *Collection {
	out := &Collection{Lists: make([]*List, 0, len(c.Lists))}
	for _, l := range c.Lists {
		nl := *l
		nl.Entries = make([]*ListEntry, 0, len(l.Entries))
		for _, x := range l.Entries {
			if x.MediaID != mediaID {
				nl.Entries = append(nl.Entries, x)
			}
		}
		out.Lists = append(out.Lists, &nl)
	}
	return out
}

type Viewer struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Avatar struct {
		Large  string `json:"large"`
		Medium string `json:"medium"`
	} `json:"avatar"`
	BannerImage string `json:"bannerImage"`
	Options     struct {
		DisplayAdultContent bool `json:"displayAdultContent"`
	} `json:"options"`
	MediaListOptions struct {
		ScoreFormat string `json:"scoreFormat"`
	} `json:"mediaListOptions"`
}

type PageInfo struct {
	Total       int  `json:"total"`
	CurrentPage int  `json:"currentPage"`
	LastPage    int  `json:"lastPage"`
	HasNextPage bool `json:"hasNextPage"`
}

type MediaPage struct {
	PageInfo PageInfo `json:"pageInfo"`
	Media    []*Media `json:"media"`
}

type SchedulePage struct {
	PageInfo        PageInfo         `json:"pageInfo"`
	AiringSchedules []*AiringEpisode `json:"airingSchedules"`
}

// SearchParams are the filters supported by the Discover/Search page.
type SearchParams struct {
	Page    int      `json:"page"`
	PerPage int      `json:"perPage"`
	Search  string   `json:"search"`
	Type    string   `json:"type"`
	Genres  []string `json:"genres"`
	Tags    []string `json:"tags"`
	Season  string   `json:"season"`
	Year    int      `json:"year"`
	Formats []string `json:"formats"`
	Status  string   `json:"status"`
	Sort    []string `json:"sort"`
	IsAdult *bool    `json:"isAdult"`
	IDs     []int    `json:"ids"`
}

// EntryUpdate is the payload to create/update a list entry.
type EntryUpdate struct {
	MediaID     int        `json:"mediaId"`
	Status      *string    `json:"status,omitempty"`
	Progress    *int       `json:"progress,omitempty"`
	Score       *float64   `json:"score,omitempty"` // 0-100
	Repeat      *int       `json:"repeat,omitempty"`
	StartedAt   *FuzzyDate `json:"startedAt,omitempty"`
	CompletedAt *FuzzyDate `json:"completedAt,omitempty"`
}

// applyTo sets the fields the update carries on e.
func (u EntryUpdate) applyTo(e *ListEntry) {
	if u.Status != nil {
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
	if u.StartedAt != nil {
		e.StartedAt = *u.StartedAt
	}
	if u.CompletedAt != nil {
		e.CompletedAt = *u.CompletedAt
	}
}
