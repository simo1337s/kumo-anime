package api

import (
	"net/http"
	"strings"

	"github.com/simo1337s/animetest/server/internal/player"
)

// The sub/dub choice is remembered per anime (the streamMode of its track
// prefs) and used everywhere: streaming, "continue watching", next episode,
// and the audio track of local files.

func (s *Server) defaultLanguageMode() string {
	if s.app.Settings.Get().AniCli.DefaultMode == "dub" {
		return "dub"
	}
	return "sub"
}

// languageMode is the remembered sub/dub choice for an anime, or the default.
func (s *Server) languageMode(mediaID int) (mode string, saved bool) {
	if p := s.app.Player.Tracks.Get(mediaID); p != nil && (p.StreamMode == "sub" || p.StreamMode == "dub") {
		return p.StreamMode, true
	}
	return s.defaultLanguageMode(), false
}

// setLanguageMode remembers the sub/dub choice for an anime. An audio track
// picked by hand that doesn't fit the new choice is forgotten, so switching
// to dub really plays the dub next time (local files included).
func (s *Server) setLanguageMode(mediaID int, mode string) error {
	if mode != "sub" && mode != "dub" {
		return badRequest(`mode must be "sub" or "dub"`)
	}
	p := s.app.Player.Tracks.Get(mediaID)
	if p == nil {
		p = &player.TrackPrefs{MediaID: mediaID}
	}
	if p.StreamMode == mode {
		return nil
	}
	p.StreamMode = mode
	if (p.AudioLang != "" || p.AudioTitle != "" || p.AudioIndex > 0) && !audioFitsMode(p.AudioLang, p.AudioTitle, mode) {
		p.AudioLang, p.AudioTitle, p.AudioIndex = "", "", 0
		p.SubLang, p.SubTitle, p.SubIndex, p.SubOff = "", "", 0, false
	}
	return s.app.Player.Tracks.Save(*p)
}

// audioFitsMode reports whether an audio track (by language tag or title)
// matches sub (original audio) or dub (English) mode.
func audioFitsMode(lang, title, mode string) bool {
	l, t := strings.ToLower(strings.TrimSpace(lang)), strings.ToLower(title)
	english := l == "eng" || l == "en" || strings.Contains(t, "english") || strings.Contains(t, "dub")
	if mode == "dub" {
		return english
	}
	return !english
}

func (s *Server) getLanguage(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	mode, saved := s.languageMode(id)
	return map[string]any{"mode": mode, "saved": saved}, nil
}

func (s *Server) putLanguage(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if err := s.setLanguageMode(id, body.Mode); err != nil {
		return nil, err
	}
	return map[string]any{"mode": body.Mode, "saved": true}, nil
}
