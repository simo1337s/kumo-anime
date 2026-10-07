package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMangaPosition(t *testing.T) {
	s := newTestServer(t)
	local := "127.0.0.1:5000"

	if w := do(s, "GET", "/api/manga/30013/position", local, "", nil); w.Code != http.StatusOK || w.Body.String() != "null\n" {
		t.Fatalf("no position yet: %d %q", w.Code, w.Body.String())
	}
	body := `{"provider":"mangadex","chapterId":"c-12","chapter":"12","page":7,"pages":20}`
	if w := do(s, "PUT", "/api/manga/30013/position", local, body, nil); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	w := do(s, "GET", "/api/manga/30013/position", local, "", nil)
	var p struct {
		Provider, ChapterID, Chapter string
		Page, Pages                  int
		UpdatedAt                    int64
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if p.Provider != "mangadex" || p.ChapterID != "c-12" || p.Chapter != "12" || p.Page != 7 || p.Pages != 20 || p.UpdatedAt == 0 {
		t.Fatalf("got %+v", p)
	}
	for _, bad := range []string{`{"provider":"mangadex","page":1}`, `{"provider":"mangadex","chapterId":"c","page":-1}`} {
		if w := do(s, "PUT", "/api/manga/30013/position", local, bad, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d", bad, w.Code)
		}
	}
}
