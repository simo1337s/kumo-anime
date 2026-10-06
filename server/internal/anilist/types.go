package anilist

import "strings"

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

// PreferredTitle returns the user preferred / romaji / english title.
func (m *Media) PreferredTitle() string {
	for _, t := range []string{m.Title.UserPreferred, m.Title.Romaji, m.Title.English, m.Title.Native} {
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
