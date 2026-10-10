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

// The last sub/dub choice is the default for anime without one, and the
// audio track picked in the player is a choice too.
func TestLanguageChoiceIsTheDefault(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"
	mode := func(id string) (string, bool) {
		w := do(s, "GET", "/api/anime/"+id+"/language", local, "", nil)
		var out struct {
			Mode  string `json:"mode"`
			Saved bool   `json:"saved"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Mode, out.Saved
	}
	do(s, "PUT", "/api/anime/1/language", local, `{"mode":"dub"}`, nil)
	if m, saved := mode("2"); m != "dub" || saved {
		t.Fatalf("an anime without a choice: %s %v, want dub (the default now)", m, saved)
	}
	// Sub for one anime: the others start in sub, the dubbed one stays.
	do(s, "PUT", "/api/anime/3/language", local, `{"mode":"sub"}`, nil)
	if m, _ := mode("2"); m != "sub" {
		t.Fatalf("default after sub: %s", m)
	}
	if m, saved := mode("1"); m != "dub" || !saved {
		t.Fatalf("the dubbed anime: %s %v", m, saved)
	}

	// English audio picked in the player: dub, for it and as the default.
	if w := do(s, "PUT", "/api/playback/tracks/4", local, `{"audioLang":"eng","audioTitle":"English","audioIndex":2,"streamMode":"dub"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("tracks: %d %s", w.Code, w.Body)
	}
	if m, saved := mode("4"); m != "dub" || !saved {
		t.Fatalf("after the player's English audio: %s %v", m, saved)
	}
	if m, _ := mode("5"); m != "dub" {
		t.Fatalf("default after the player's choice: %s", m)
	}
	// Changing subtitles only keeps the choice.
	do(s, "PUT", "/api/playback/tracks/4", local, `{"audioLang":"eng","audioTitle":"English","audioIndex":2,"subOff":true}`, nil)
	if m, saved := mode("4"); m != "dub" || !saved {
		t.Fatalf("after a subtitle change: %s %v", m, saved)
	}
}
