package player

import (
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/db"
)

// TrackPrefs remembers which audio and subtitle track the user picked for an
// anime so the next episodes open with the same choice.
type TrackPrefs struct {
	MediaID    int    `json:"mediaId"`
	AudioLang  string `json:"audioLang"`
	AudioTitle string `json:"audioTitle"`
	AudioIndex int    `json:"audioIndex"` // 1-based position among audio tracks
	SubLang    string `json:"subLang"`
	SubTitle   string `json:"subTitle"`
	SubIndex   int    `json:"subIndex"`
	SubOff     bool   `json:"subOff"`
	// Preferred online stream mode for this anime: sub | dub.
	StreamMode string `json:"streamMode"`
	UpdatedAt  int64  `json:"updatedAt"`
}

type TrackStore struct{ db *db.DB }

func NewTrackStore(d *db.DB) *TrackStore { return &TrackStore{db: d} }

func (s *TrackStore) Get(mediaID int) *TrackPrefs {
	var p TrackPrefs
	var off int
	err := s.db.QueryRow(`SELECT media_id, audio_lang, audio_title, audio_index, sub_lang, sub_title, sub_index, sub_off, stream_mode, updated_at
		FROM track_prefs WHERE media_id = ?`, mediaID).
		Scan(&p.MediaID, &p.AudioLang, &p.AudioTitle, &p.AudioIndex, &p.SubLang, &p.SubTitle, &p.SubIndex, &off, &p.StreamMode, &p.UpdatedAt)
	if err != nil {
		return nil
	}
	p.SubOff = off == 1
	return &p
}

func (s *TrackStore) Save(p TrackPrefs) error {
	if p.MediaID <= 0 {
		return nil
	}
	p.UpdatedAt = time.Now().Unix()
	off := 0
	if p.SubOff {
		off = 1
	}
	_, err := s.db.Write(`INSERT INTO track_prefs(media_id, audio_lang, audio_title, audio_index, sub_lang, sub_title, sub_index, sub_off, stream_mode, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(media_id) DO UPDATE SET audio_lang=excluded.audio_lang, audio_title=excluded.audio_title, audio_index=excluded.audio_index,
			sub_lang=excluded.sub_lang, sub_title=excluded.sub_title, sub_index=excluded.sub_index, sub_off=excluded.sub_off,
			stream_mode=excluded.stream_mode, updated_at=excluded.updated_at`,
		p.MediaID, p.AudioLang, p.AudioTitle, p.AudioIndex, p.SubLang, p.SubTitle, p.SubIndex, off, p.StreamMode, p.UpdatedAt)
	return err
}

// SetStreamMode only updates the preferred sub/dub mode.
func (s *TrackStore) SetStreamMode(mediaID int, mode string) error {
	p := s.Get(mediaID)
	if p == nil {
		p = &TrackPrefs{MediaID: mediaID}
	}
	p.StreamMode = mode
	return s.Save(*p)
}

// Track is a media track as reported by mpv (or the in-app player).
type Track struct {
	ID       int    `json:"id"`
	Type     string `json:"type"` // audio | sub | video
	Lang     string `json:"lang"`
	Title    string `json:"title"`
	Codec    string `json:"codec"`
	Default  bool   `json:"default"`
	Forced   bool   `json:"forced"`
	External bool   `json:"external"`
	Selected bool   `json:"selected"`
}

// PickTrack returns the id of the track that best matches the remembered
// preference: same language+title first, then language, then position.
func PickTrack(tracks []Track, typ, lang, title string, index int) (int, bool) {
	var list []Track
	for _, t := range tracks {
		if t.Type == typ {
			list = append(list, t)
		}
	}
	if len(list) == 0 {
		return 0, false
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	if lang != "" || title != "" {
		for _, t := range list {
			if norm(t.Lang) == norm(lang) && norm(t.Title) == norm(title) {
				return t.ID, true
			}
		}
		if title != "" {
			for _, t := range list {
				if norm(t.Title) == norm(title) {
					return t.ID, true
				}
			}
		}
		if lang != "" {
			for _, t := range list {
				// Prefer full subs over "signs & songs" for the same language.
				if langMatch(t.Lang, lang) && !t.Forced && !isSigns(t.Title) {
					return t.ID, true
				}
			}
			for _, t := range list {
				if langMatch(t.Lang, lang) {
					return t.ID, true
				}
			}
		}
	}
	if index > 0 && index <= len(list) {
		return list[index-1].ID, true
	}
	return 0, false
}

// PickByLanguages picks the first track matching a comma separated list of
// language codes (e.g. "jpn,ja").
func PickByLanguages(tracks []Track, typ, langs string) (int, bool) {
	for _, l := range strings.Split(langs, ",") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if id, ok := PickTrack(tracks, typ, l, "", 0); ok {
			return id, true
		}
	}
	return 0, false
}

func isSigns(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "sign") || strings.Contains(t, "song") || strings.Contains(t, "forced")
}

var langAliases = map[string]string{
	"ja": "jpn", "jp": "jpn", "jpn": "jpn", "japanese": "jpn",
	"en": "eng", "eng": "eng", "english": "eng", "en-us": "eng", "enus": "eng",
	"es": "spa", "spa": "spa", "spanish": "spa", "es-la": "spa",
	"fr": "fre", "fre": "fre", "fra": "fre", "french": "fre",
	"de": "ger", "ger": "ger", "deu": "ger", "german": "ger",
	"it": "ita", "ita": "ita", "italian": "ita",
	"pt": "por", "por": "por", "portuguese": "por", "pt-br": "por",
	"ru": "rus", "rus": "rus", "russian": "rus",
	"zh": "chi", "chi": "chi", "zho": "chi", "chinese": "chi",
	"ko": "kor", "kor": "kor", "korean": "kor",
	"ar": "ara", "ara": "ara", "arabic": "ara",
}

func langMatch(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == b {
		return a != ""
	}
	ca, okA := langAliases[a]
	cb, okB := langAliases[b]
	return okA && okB && ca == cb
}

// PrefsFromSelection builds a TrackPrefs from the currently selected tracks.
func PrefsFromSelection(mediaID int, tracks []Track, aid, sid int, subOff bool) TrackPrefs {
	p := TrackPrefs{MediaID: mediaID, SubOff: subOff}
	ai, si := 0, 0
	for _, t := range tracks {
		switch t.Type {
		case "audio":
			ai++
			if t.ID == aid {
				p.AudioLang, p.AudioTitle, p.AudioIndex = t.Lang, t.Title, ai
			}
		case "sub":
			si++
			if t.ID == sid && !subOff {
				p.SubLang, p.SubTitle, p.SubIndex = t.Lang, t.Title, si
			}
		}
	}
	return p
}
