package api

import (
	"net/http"

	"github.com/simo1337s/animetest/server/internal/anilist"
)

// markEpisode marks one episode watched or unwatched. AniList only stores
// how far you got, so "watched" raises the progress to that episode (it
// never lowers it) and "unwatched" lowers it to the episode before (it
// never raises it). The current progress is read here rather than taken
// from the page, which may be out of date.
func (s *Server) markEpisode(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var body struct {
		Episode int  `json:"episode"`
		Watched bool `json:"watched"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.Episode <= 0 {
		return nil, badRequest("invalid episode")
	}
	media, err := s.app.Platform.MediaLite(r.Context(), id)
	if err != nil {
		return nil, err
	}
	mt := media.Type
	if mt == "" {
		mt = "ANIME"
	}
	coll, err := s.app.Platform.Collection(r.Context(), mt, false)
	if err != nil {
		return nil, err
	}
	var cur anilist.ListEntry
	if e := coll.Find(id); e != nil {
		cur = *e
	}
	total := 0
	if media.Episodes != nil {
		total = *media.Episodes
	}
	next, status, repeat := markedProgress(cur.Status, cur.Progress, cur.Repeat, total, body.Episode, body.Watched)
	if next == cur.Progress && status == cur.Status {
		return map[string]any{"changed": false, "progress": cur.Progress, "status": cur.Status}, nil
	}
	u := anilist.EntryUpdate{MediaID: id, Status: &status, Progress: &next}
	if repeat != cur.Repeat {
		u.Repeat = &repeat
	}
	if err := s.app.Platform.UpdateEntry(r.Context(), u); err != nil {
		return nil, err
	}
	return map[string]any{"changed": true, "progress": next, "status": status}, nil
}

// markedProgress works out the list entry after marking episode ep as
// watched or unwatched. status is "" when the anime isn't in the list;
// total is 0 when the episode count isn't known.
func markedProgress(status string, progress, repeat, total, ep int, watched bool) (int, string, int) {
	next := min(progress, ep-1)
	if watched {
		next = max(progress, ep)
	}
	if total > 0 {
		next = min(next, total)
	}
	switch {
	case total > 0 && next >= total && next > progress:
		// Finished: complete it (a rewatch counts once more).
		if status == "REPEATING" {
			repeat++
		}
		status = "COMPLETED"
	case status == "COMPLETED" && next < progress:
		status = "CURRENT" // reopened
	case next > progress && status != "REPEATING":
		status = "CURRENT" // not in the list, planned, paused or dropped
	}
	return next, status, repeat
}
