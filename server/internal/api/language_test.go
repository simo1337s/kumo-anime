package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/simo1337s/animetest/server/internal/player"
)

func TestLanguageMode(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	get := func() map[string]any {
		w := do(s, "GET", "/api/anime/1575/language", local, "", nil)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	if m := get(); m["mode"] != "sub" || m["saved"] != false {
		t.Fatalf("default: %v", m)
	}
	// Japanese audio picked by hand earlier.
	_ = s.app.Player.Tracks.Save(player.TrackPrefs{MediaID: 1575, AudioLang: "jpn", AudioTitle: "Japanese", SubLang: "eng", SubTitle: "Full Subs"})
	if w := do(s, "PUT", "/api/anime/1575/language", local, `{"mode":"dub"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	if m := get(); m["mode"] != "dub" || m["saved"] != true {
		t.Fatalf("after dub: %v", m)
	}
	if p := s.app.Player.Tracks.Get(1575); p.AudioLang != "" || p.SubTitle != "" {
		t.Fatalf("Japanese track choice should be dropped when switching to dub: %+v", p)
	}
	// An English track picked by hand fits dub and is kept.
	_ = s.app.Player.Tracks.Save(player.TrackPrefs{MediaID: 1575, AudioLang: "eng", AudioTitle: "English Dub", StreamMode: "sub"})
	do(s, "PUT", "/api/anime/1575/language", local, `{"mode":"dub"}`, nil)
	if p := s.app.Player.Tracks.Get(1575); p.AudioTitle != "English Dub" || p.StreamMode != "dub" {
		t.Fatalf("fitting track should be kept: %+v", p)
	}
	if w := do(s, "PUT", "/api/anime/1575/language", local, `{"mode":"raw"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode: %d", w.Code)
	}
}
