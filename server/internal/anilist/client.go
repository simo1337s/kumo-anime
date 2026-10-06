package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/util"
)

const endpoint = "https://graphql.anilist.co"

var ErrUnauthorized = errors.New("anilist: invalid or expired token")

// Client is a minimal AniList GraphQL client with rate-limit handling.
type Client struct {
	http  *http.Client
	mu    sync.RWMutex
	token string
}

func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: 25 * time.Second}}
}

func (c *Client) SetToken(t string) {
	c.mu.Lock()
	c.token = strings.TrimSpace(t)
	c.mu.Unlock()
}

func (c *Client) Token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

type gqlError struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// Query runs a GraphQL query; out receives the "data" object.
func (c *Client) Query(ctx context.Context, query string, vars map[string]any, out any) error {
	return c.query(ctx, query, vars, out, true)
}

// QueryAnon runs a query without the user token (public data only).
func (c *Client) QueryAnon(ctx context.Context, query string, vars map[string]any, out any) error {
	return c.query(ctx, query, vars, out, false)
}

func (c *Client) query(ctx context.Context, query string, vars map[string]any, out any, auth bool) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", util.UserAgent)
		if tok := c.Token(); auth && tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if attempt < 2 && ctx.Err() == nil {
				time.Sleep(time.Second)
				continue
			}
			return fmt.Errorf("anilist: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := 5
			if ra, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && ra > 0 {
				wait = ra
			}
			if wait > 65 {
				wait = 65
			}
			select {
			case <-time.After(time.Duration(wait) * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		var gr gqlResponse
		if err := json.Unmarshal(raw, &gr); err != nil {
			return fmt.Errorf("anilist: bad response (%s)", resp.Status)
		}
		if len(gr.Errors) > 0 {
			msg := gr.Errors[0].Message
			if gr.Errors[0].Status == 401 || resp.StatusCode == 401 || strings.Contains(strings.ToLower(msg), "invalid token") {
				return ErrUnauthorized
			}
			if gr.Data == nil || string(gr.Data) == "null" {
				return fmt.Errorf("anilist: %s", msg)
			}
		}
		if resp.StatusCode == 401 {
			return ErrUnauthorized
		}
		if out != nil {
			return json.Unmarshal(gr.Data, out)
		}
		return nil
	}
	return errors.New("anilist: rate limited, try again in a minute")
}

// ---------------------------------------------------------------------------
// Queries

const mediaFields = `
	id idMal type format status season seasonYear episodes chapters volumes duration isAdult
	title { romaji english native userPreferred }
	synonyms
	coverImage { extraLarge large medium color }
	bannerImage
	genres
	averageScore meanScore popularity
	countryOfOrigin
	startDate { year month day }
	endDate { year month day }
	nextAiringEpisode { airingAt timeUntilAiring episode }
`

const qViewer = `query { Viewer { id name avatar { large medium } bannerImage options { displayAdultContent } mediaListOptions { scoreFormat } } }`

const qCollection = `query ($userId: Int, $type: MediaType) {
	MediaListCollection(userId: $userId, type: $type, forceSingleCompletedList: true) {
		lists {
			name status isCustomList
			entries {
				id mediaId status progress score(format: POINT_100) repeat notes private updatedAt
				startedAt { year month day } completedAt { year month day }
				media { ` + mediaFields + ` }
			}
		}
	}
}`

const qMedia = `query ($id: Int) {
	Media(id: $id) {
		` + mediaFields + `
		description(asHtml: false)
		source(version: 3)
		siteUrl
		trailer { id site thumbnail }
		streamingEpisodes { title thumbnail url site }
		studios(isMain: true) { nodes { id name } }
		tags { name rank isMediaSpoiler }
		rankings { rank type allTime year season context }
		mediaListEntry { id status progress score(format: POINT_100) repeat }
		relations { edges { relationType(version: 2) node { ` + mediaFields + ` } } }
		recommendations(page: 1, perPage: 12, sort: RATING_DESC) { nodes { mediaRecommendation { ` + mediaFields + ` } } }
		characters(sort: [ROLE, RELEVANCE, ID], perPage: 16) {
			edges { role node { id name { full } image { large } } voiceActors(language: JAPANESE, sort: RELEVANCE) { id name { full } image { large } } }
		}
	}
}`

const qSearch = `query ($page: Int, $perPage: Int, $search: String, $type: MediaType, $genres: [String], $tags: [String], $season: MediaSeason, $seasonYear: Int, $formats: [MediaFormat], $status: MediaStatus, $sort: [MediaSort], $isAdult: Boolean, $ids: [Int]) {
	Page(page: $page, perPage: $perPage) {
		pageInfo { total currentPage lastPage hasNextPage }
		media(search: $search, type: $type, genre_in: $genres, tag_in: $tags, season: $season, seasonYear: $seasonYear, format_in: $formats, status: $status, sort: $sort, isAdult: $isAdult, id_in: $ids) {
			` + mediaFields + `
			description(asHtml: false)
		}
	}
}`

const qSchedule = `query ($page: Int, $start: Int, $end: Int) {
	Page(page: $page, perPage: 50) {
		pageInfo { hasNextPage currentPage }
		airingSchedules(airingAt_greater: $start, airingAt_lesser: $end, sort: TIME) {
			id airingAt timeUntilAiring episode
			media { ` + mediaFields + ` }
		}
	}
}`

const mSaveEntry = `mutation ($mediaId: Int, $status: MediaListStatus, $progress: Int, $scoreRaw: Int, $repeat: Int, $startedAt: FuzzyDateInput, $completedAt: FuzzyDateInput) {
	SaveMediaListEntry(mediaId: $mediaId, status: $status, progress: $progress, scoreRaw: $scoreRaw, repeat: $repeat, startedAt: $startedAt, completedAt: $completedAt) {
		id mediaId status progress score(format: POINT_100) repeat
	}
}`

const mDeleteEntry = `mutation ($id: Int) { DeleteMediaListEntry(id: $id) { deleted } }`

func (c *Client) Viewer(ctx context.Context) (*Viewer, error) {
	var out struct {
		Viewer *Viewer `json:"Viewer"`
	}
	if err := c.Query(ctx, qViewer, nil, &out); err != nil {
		return nil, err
	}
	if out.Viewer == nil {
		return nil, ErrUnauthorized
	}
	return out.Viewer, nil
}

func (c *Client) Collection(ctx context.Context, userID int, mediaType string) (*Collection, error) {
	var out struct {
		MediaListCollection *Collection `json:"MediaListCollection"`
	}
	if err := c.Query(ctx, qCollection, map[string]any{"userId": userID, "type": mediaType}, &out); err != nil {
		return nil, err
	}
	if out.MediaListCollection == nil {
		return &Collection{}, nil
	}
	return out.MediaListCollection, nil
}

func (c *Client) Media(ctx context.Context, id int) (*Media, error) {
	var out struct {
		Media *Media `json:"Media"`
	}
	if err := c.Query(ctx, qMedia, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Media == nil {
		return nil, fmt.Errorf("anilist: media %d not found", id)
	}
	return out.Media, nil
}

func (c *Client) Search(ctx context.Context, p SearchParams) (*MediaPage, error) {
	vars := map[string]any{
		"page":    max(p.Page, 1),
		"perPage": clamp(p.PerPage, 1, 50, 30),
	}
	if p.Type == "" {
		p.Type = "ANIME"
	}
	vars["type"] = p.Type
	if p.Search != "" {
		vars["search"] = p.Search
	}
	if len(p.Genres) > 0 {
		vars["genres"] = p.Genres
	}
	if len(p.Tags) > 0 {
		vars["tags"] = p.Tags
	}
	if p.Season != "" {
		vars["season"] = p.Season
	}
	if p.Year > 0 {
		vars["seasonYear"] = p.Year
	}
	if len(p.Formats) > 0 {
		vars["formats"] = p.Formats
	}
	if p.Status != "" {
		vars["status"] = p.Status
	}
	if len(p.IDs) > 0 {
		vars["ids"] = p.IDs
	}
	if len(p.Sort) > 0 {
		vars["sort"] = p.Sort
	} else if p.Search != "" {
		vars["sort"] = []string{"SEARCH_MATCH"}
	} else {
		vars["sort"] = []string{"TRENDING_DESC", "POPULARITY_DESC"}
	}
	if p.IsAdult != nil {
		vars["isAdult"] = *p.IsAdult
	}
	var out struct {
		Page *MediaPage `json:"Page"`
	}
	if err := c.Query(ctx, qSearch, vars, &out); err != nil {
		return nil, err
	}
	if out.Page == nil {
		return &MediaPage{}, nil
	}
	return out.Page, nil
}

func (c *Client) Schedule(ctx context.Context, start, end int64) ([]*AiringEpisode, error) {
	var all []*AiringEpisode
	for page := 1; page <= 12; page++ {
		var out struct {
			Page *SchedulePage `json:"Page"`
		}
		if err := c.Query(ctx, qSchedule, map[string]any{"page": page, "start": start, "end": end}, &out); err != nil {
			return all, err
		}
		if out.Page == nil {
			break
		}
		all = append(all, out.Page.AiringSchedules...)
		if !out.Page.PageInfo.HasNextPage {
			break
		}
	}
	return all, nil
}

func (c *Client) SaveEntry(ctx context.Context, u EntryUpdate) (*ListEntryLite, error) {
	vars := map[string]any{"mediaId": u.MediaID}
	if u.Status != nil {
		vars["status"] = *u.Status
	}
	if u.Progress != nil {
		vars["progress"] = *u.Progress
	}
	if u.Score != nil {
		vars["scoreRaw"] = int(*u.Score)
	}
	if u.Repeat != nil {
		vars["repeat"] = *u.Repeat
	}
	if u.StartedAt != nil {
		vars["startedAt"] = u.StartedAt
	}
	if u.CompletedAt != nil {
		vars["completedAt"] = u.CompletedAt
	}
	var out struct {
		SaveMediaListEntry *ListEntryLite `json:"SaveMediaListEntry"`
	}
	if err := c.Query(ctx, mSaveEntry, vars, &out); err != nil {
		return nil, err
	}
	return out.SaveMediaListEntry, nil
}

func (c *Client) DeleteEntry(ctx context.Context, entryID int) error {
	return c.Query(ctx, mDeleteEntry, map[string]any{"id": entryID}, nil)
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
