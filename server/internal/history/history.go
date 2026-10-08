// Package history stores per-episode playback positions so every episode can
// be resumed exactly where it was left, from any player or source.
package history

import (
	"time"

	"github.com/simo1337s/animetest/server/internal/db"
)

type Entry struct {
	MediaID   int     `json:"mediaId"`
	Episode   int     `json:"episode"`
	Position  float64 `json:"position"` // seconds
	Duration  float64 `json:"duration"` // seconds
	Source    string  `json:"source"`   // local | anicli | stream | torrent
	UpdatedAt int64   `json:"updatedAt"`
}

// Percent returns the watched fraction 0-1.
func (e *Entry) Percent() float64 {
	if e == nil || e.Duration <= 0 {
		return 0
	}
	return min(e.Position/e.Duration, 1)
}

type Store struct {
	db *db.DB
	// OnChange is told about each position this Kumo saves (not those
	// merged from another Kumo): another Kumo on the same account gets it.
	OnChange func(Entry)
}

func NewStore(d *db.DB) *Store { return &Store{db: d} }

// changed tells OnChange about an entry this Kumo saved.
func (s *Store) changed(e Entry) {
	if s.OnChange != nil {
		s.OnChange(e)
	}
}

// Save records the current position. Positions under 5 seconds are ignored so
// accidentally opening an episode doesn't wipe a saved position.
func (s *Store) Save(e Entry) error {
	if e.MediaID <= 0 || e.Position < 5 {
		return nil
	}
	e.UpdatedAt = time.Now().Unix()
	_, err := s.db.Write(`INSERT INTO watch_history(media_id, episode, position, duration, source, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(media_id, episode) DO UPDATE SET position = excluded.position,
			duration = CASE WHEN excluded.duration > 0 THEN excluded.duration ELSE watch_history.duration END,
			source = excluded.source, updated_at = excluded.updated_at`,
		e.MediaID, e.Episode, e.Position, e.Duration, e.Source, e.UpdatedAt)
	if err == nil {
		if saved := s.Get(e.MediaID, e.Episode); saved != nil {
			s.changed(*saved)
		}
	}
	return err
}

// MarkFinished stores a "fully watched" position so the episode doesn't
// offer resume anymore but still counts as recently watched.
func (s *Store) MarkFinished(mediaID, episode int, duration float64, source string) error {
	if duration <= 0 {
		duration = 1
	}
	_, err := s.db.Write(`INSERT INTO watch_history(media_id, episode, position, duration, source, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(media_id, episode) DO UPDATE SET position = excluded.position, duration = excluded.duration,
			source = excluded.source, updated_at = excluded.updated_at`,
		mediaID, episode, duration, duration, source, time.Now().Unix())
	if err == nil {
		if saved := s.Get(mediaID, episode); saved != nil {
			s.changed(*saved)
		}
	}
	return err
}

func (s *Store) Get(mediaID, episode int) *Entry {
	var e Entry
	err := s.db.QueryRow(`SELECT media_id, episode, position, duration, source, updated_at FROM watch_history WHERE media_id = ? AND episode = ?`, mediaID, episode).
		Scan(&e.MediaID, &e.Episode, &e.Position, &e.Duration, &e.Source, &e.UpdatedAt)
	if err != nil {
		return nil
	}
	return &e
}

// ResumePosition returns where to start an episode (0 if it was finished or
// never started). Episodes watched past 95% start from the beginning.
func (s *Store) ResumePosition(mediaID, episode int) float64 {
	e := s.Get(mediaID, episode)
	if e == nil || e.Position < 5 {
		return 0
	}
	if e.Duration > 0 && e.Position/e.Duration > 0.95 {
		return 0
	}
	if e.Duration > 0 && e.Duration-e.Position < 30 {
		return 0
	}
	return e.Position
}

func (s *Store) ForMedia(mediaID int) map[int]*Entry {
	out := map[int]*Entry{}
	rows, err := s.db.Query(`SELECT media_id, episode, position, duration, source, updated_at FROM watch_history WHERE media_id = ?`, mediaID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		if rows.Scan(&e.MediaID, &e.Episode, &e.Position, &e.Duration, &e.Source, &e.UpdatedAt) == nil {
			out[e.Episode] = &e
		}
	}
	return out
}

// Recent returns the latest history row per media, newest first.
func (s *Store) Recent(limit int) []*Entry {
	rows, err := s.db.Query(`SELECT h.media_id, h.episode, h.position, h.duration, h.source, h.updated_at
		FROM watch_history h
		JOIN (SELECT media_id, MAX(updated_at) AS u FROM watch_history GROUP BY media_id) last
		  ON last.media_id = h.media_id AND last.u = h.updated_at
		ORDER BY h.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Entry
	seen := map[int]bool{}
	for rows.Next() {
		var e Entry
		if rows.Scan(&e.MediaID, &e.Episode, &e.Position, &e.Duration, &e.Source, &e.UpdatedAt) == nil && !seen[e.MediaID] {
			seen[e.MediaID] = true
			out = append(out, &e)
		}
	}
	return out
}

func (s *Store) LastWatched() map[int]int64 {
	out := map[int]int64{}
	rows, err := s.db.Query(`SELECT media_id, MAX(updated_at) FROM watch_history GROUP BY media_id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var t int64
		if rows.Scan(&id, &t) == nil {
			out[id] = t
		}
	}
	return out
}

// Since returns the entries updated after t (unix seconds), oldest first:
// what another Kumo on the same account hasn't seen yet.
func (s *Store) Since(t int64, limit int) []*Entry {
	rows, err := s.db.Query(`SELECT media_id, episode, position, duration, source, updated_at FROM watch_history
		WHERE updated_at > ? ORDER BY updated_at LIMIT ?`, t, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Entry
	for rows.Next() {
		var e Entry
		if rows.Scan(&e.MediaID, &e.Episode, &e.Position, &e.Duration, &e.Source, &e.UpdatedAt) == nil {
			out = append(out, &e)
		}
	}
	return out
}

// Merge takes an entry from another Kumo on the same account when it's
// newer than this one's, with its time, and reports whether it did.
func (s *Store) Merge(e Entry) (bool, error) {
	if e.MediaID <= 0 || e.Episode < 0 || e.UpdatedAt <= 0 || e.Position < 0 || e.Duration < 0 {
		return false, nil
	}
	res, err := s.db.Write(`INSERT INTO watch_history(media_id, episode, position, duration, source, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(media_id, episode) DO UPDATE SET position = excluded.position, duration = excluded.duration,
			source = excluded.source, updated_at = excluded.updated_at
		WHERE excluded.updated_at > watch_history.updated_at`,
		e.MediaID, e.Episode, e.Position, e.Duration, e.Source, e.UpdatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) Clear(mediaID, episode int) error {
	_, err := s.db.Write(`DELETE FROM watch_history WHERE media_id = ? AND episode = ?`, mediaID, episode)
	return err
}
