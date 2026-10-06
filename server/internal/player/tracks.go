package player

import (
	"encoding/json"
	"math"
	"strconv"
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

// trackSel is an aid/sid value reported by mpv: a track id, or off.
type trackSel struct {
	id  int
	off bool
}

// parseTrackSel decodes an aid/sid property value. ok is false for values
// that say nothing about the selection: "auto" (what mpv reports when no file
// is playing), a missing/null value (property unavailable) and anything else
// unexpected. false and "no" mean off.
func parseTrackSel(raw json.RawMessage) (sel trackSel, ok bool) {
	var v any
	if !decodeProp(raw, &v) {
		return trackSel{}, false
	}
	switch x := v.(type) {
	case float64:
		if x >= 1 && x <= 1<<16 && x == math.Trunc(x) {
			return trackSel{id: int(x)}, true
		}
	case bool:
		if !x {
			return trackSel{off: true}, true
		}
	case string:
		if x == "no" {
			return trackSel{off: true}, true
		}
		if n, err := strconv.Atoi(x); err == nil && n >= 1 {
			return trackSel{id: n}, true
		}
	}
	return trackSel{}, false
}

// trackByID returns the track of the given type with that id and its 1-based
// position among the tracks of that type.
func trackByID(tracks []Track, typ string, id int) (Track, int, bool) {
	n := 0
	for _, t := range tracks {
		if t.Type != typ {
			continue
		}
		n++
		if t.ID == id {
			return t, n, true
		}
	}
	return Track{}, 0, false
}

// mergeSelection applies the selected audio/subtitle tracks to the saved
// preferences (old may be nil). Each part only counts when it names a track
// of that type in tracks, or for subtitles "off" while there are subtitle
// tracks to turn off; the other parts keep their saved values. ok is false
// when nothing applied.
func mergeSelection(mediaID int, old *TrackPrefs, tracks []Track, aid, sid trackSel) (p TrackPrefs, ok bool) {
	if old != nil {
		p = *old
	}
	p.MediaID = mediaID
	if !aid.off {
		if t, i, found := trackByID(tracks, "audio", aid.id); found {
			p.AudioLang, p.AudioTitle, p.AudioIndex = t.Lang, t.Title, i
			ok = true
		}
	}
	if sid.off {
		for _, t := range tracks {
			if t.Type == "sub" {
				p.SubOff, p.SubLang, p.SubTitle, p.SubIndex = true, "", "", 0
				ok = true
				break
			}
		}
	} else if t, i, found := trackByID(tracks, "sub", sid.id); found {
		p.SubOff, p.SubLang, p.SubTitle, p.SubIndex = false, t.Lang, t.Title, i
		ok = true
	}
	return p, ok
}

// trackWatch decides which aid/sid changes are the user's choice and worth
// remembering. It runs on the session's event loop.
//
// mpv changes tracks by itself too: while a file loads (and while we restore
// the saved choice), hence the guard; and while it unloads, when aid/sid fall
// back to their option values ("auto", or false for "no") and track-list
// empties. mpv doesn't promise those arrive after end-file, so a change only
// counts once it has stayed in place for settle while the file kept playing:
// the end-file (or shutdown) that comes with the unload cancels it.
type trackWatch struct {
	guard, settle time.Duration

	playing   bool      // between file-loaded and end-file/idle/shutdown
	guardTill time.Time // changes before this are mpv's or ours
	tracks    []Track
	aid, sid  trackSel

	pending   bool      // a user change is waiting to settle
	changedAt time.Time // when it was last changed
}

// loaded starts watching a freshly loaded (and restored) file.
func (w *trackWatch) loaded(now time.Time, tracks []Track) {
	w.playing, w.pending = true, false
	w.guardTill = now.Add(w.guard)
	w.tracks = tracks
}

// ended stops remembering anything until the next file is loaded.
func (w *trackWatch) ended() { w.playing, w.pending = false, false }

func (w *trackWatch) setTracks(tracks []Track) { w.tracks = tracks }

// selection records an aid/sid change and reports whether it is a user change
// that should be saved once it has settled (see due).
func (w *trackWatch) selection(name string, raw json.RawMessage, now time.Time) bool {
	sel, ok := parseTrackSel(raw)
	if !ok {
		return false // "auto" etc.: no change
	}
	if name == "aid" {
		w.aid = sel
	} else {
		w.sid = sel
	}
	if !w.playing || now.Before(w.guardTill) {
		return false
	}
	w.pending, w.changedAt = true, now
	return true
}

// due reports when the pending change may be saved.
func (w *trackWatch) due() (time.Time, bool) { return w.changedAt.Add(w.settle), w.pending }

// discard drops the pending change.
func (w *trackWatch) discard() { w.pending = false }

// take returns the preferences to save for the pending change (old are the
// saved ones, may be nil), if the current selection is still a valid choice
// for the playing file.
func (w *trackWatch) take(mediaID int, old *TrackPrefs) (TrackPrefs, bool) {
	pending := w.pending
	w.pending = false
	if !pending || !w.playing {
		return TrackPrefs{}, false
	}
	return mergeSelection(mediaID, old, w.tracks, w.aid, w.sid)
}
