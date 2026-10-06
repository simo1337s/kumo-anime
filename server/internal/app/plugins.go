package app

import (
	"context"
	"errors"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/extensions"
)

// pluginServices exposes app data to plugins in Seanime-like shapes.
func (a *App) pluginServices() extensions.PluginHostServices {
	return extensions.PluginHostServices{
		GetAnime: func(ctx context.Context, id int) (any, error) { return a.Platform.MediaLite(ctx, id) },
		GetAnimeDetails: func(ctx context.Context, id int) (any, error) {
			return a.Platform.Media(ctx, id, false)
		},
		GetManga: func(ctx context.Context, id int) (any, error) { return a.Platform.MediaLite(ctx, id) },
		Collection: func(ctx context.Context, mediaType string, refresh bool) (any, error) {
			c, err := a.Platform.Collection(ctx, mediaType, refresh)
			if err != nil {
				return nil, err
			}
			return map[string]any{"MediaListCollection": c}, nil
		},
		UpdateEntry: func(ctx context.Context, mediaID int, status *string, score *float64, progress *int) error {
			return a.Platform.UpdateEntry(ctx, anilist.EntryUpdate{MediaID: mediaID, Status: status, Score: score, Progress: progress})
		},
		DeleteEntry: func(ctx context.Context, mediaID int) error { return a.Platform.DeleteEntry(ctx, mediaID) },
		CustomQuery: func(ctx context.Context, body map[string]any, token string) (any, error) {
			q, _ := body["query"].(string)
			if q == "" {
				return nil, errors.New("missing query")
			}
			vars, _ := body["variables"].(map[string]any)
			var out any
			// The query only carries the token it is given: without one it is
			// anonymous, never the logged-in user's session (any GraphQL
			// mutation would run as the user). The extension manager fills in
			// the user's token for plugins granted "anilist-token".
			c := anilist.NewClient()
			c.SetToken(token)
			err := c.Query(ctx, q, vars, &out)
			return out, err
		},
		ListAnime: func(ctx context.Context, page int, search string, perPage int) (any, error) {
			res, err := a.Platform.Search(ctx, anilist.SearchParams{Page: page, Search: search, PerPage: perPage})
			if err != nil {
				return nil, err
			}
			return map[string]any{"Page": res}, nil
		},
		Token: func() string { return a.Platform.Client().Token() },
		Viewer: func() (string, string) {
			if v := a.Platform.Viewer(); v != nil {
				return v.Name, v.Avatar.Large
			}
			return "", ""
		},
		AnimeEntry: func(ctx context.Context, id int) (any, error) {
			e, err := a.Library.Entry(ctx, id, false)
			if err != nil {
				return nil, err
			}
			out := map[string]any{"mediaId": id, "media": e.Media, "episodes": e.Episodes}
			if e.ListEntry != nil {
				out["listData"] = map[string]any{"progress": e.ListEntry.Progress, "score": e.ListEntry.Score, "status": e.ListEntry.Status, "repeat": e.ListEntry.Repeat}
			}
			if e.NextEpisode != nil {
				out["nextEpisode"] = e.NextEpisode
			}
			files, _ := a.Files.ByMedia(id)
			out["localFiles"] = files
			return out, nil
		},
		LocalFiles: func() (any, error) { return a.Files.All() },
	}
}
