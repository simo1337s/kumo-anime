package api

import (
	"net/http"

	"github.com/simo1337s/animetest/server/internal/player"
)

// The sub/dub choice is remembered per anime (the streamMode of its track
// prefs) and used everywhere: streaming, "continue watching", next episode,
// and the audio track of local files. Only what's picked is remembered
// (Sub/Dub buttons, the audio track in the player); the rest follows the
// default, which is the last choice.

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
	if (p.AudioLang != "" || p.AudioTitle != "" || p.AudioIndex > 0) && !player.AudioFitsMode(p.AudioLang, p.AudioTitle, mode) {
		p.AudioLang, p.AudioTitle, p.AudioIndex = "", "", 0
		p.SubLang, p.SubTitle, p.SubIndex, p.SubOff = "", "", 0, false
	}
	return s.app.Player.Tracks.Save(*p)
}

// followLanguage makes a sub/dub choice the default: anime without one of
// their own start in the language picked last.
func (s *Server) followLanguage(mode string) error {
	cfg := s.app.Settings.Get()
	if cfg.AniCli.DefaultMode == mode {
		return nil
	}
	cfg.AniCli.DefaultMode = mode
	_, err := s.app.Settings.Save(cfg)
	return err
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
	// Picked: the default for the anime without a choice too.
	if err := s.followLanguage(body.Mode); err != nil {
		return nil, err
	}
	return map[string]any{"mode": body.Mode, "saved": true}, nil
}
