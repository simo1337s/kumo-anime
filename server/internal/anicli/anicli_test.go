package anicli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
)

// Runs the real ani-cli script (with its scraping functions stubbed) through
// the shim driver. Set KUMO_FAKE_ANICLI to the stubbed script to enable.
func TestDriverWithStubbedAniCli(t *testing.T) {
	fake := os.Getenv("KUMO_FAKE_ANICLI")
	if fake == "" {
		t.Skip("KUMO_FAKE_ANICLI not set")
	}
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	t.Setenv("KUMO_DATA_DIR", tmp)
	d, err := db.Open(filepath.Join(tmp, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, _ := config.NewStore(d)
	s := store.Get()
	s.AniCli.Path = fake
	store.Save(s)
	drv := New(store)
	ctx := context.Background()

	res, err := drv.Search(ctx, "code geass", "sub")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[1].Index != 2 || res[1].Title != "Code Geass: Lelouch of the Rebellion R2" {
		t.Fatalf("search: %+v", res)
	}
	single, err := drv.Search(ctx, "single", "sub")
	if err != nil || len(single) != 1 {
		t.Fatalf("single: %+v %v", single, err)
	}
	eps, err := drv.Episodes(ctx, "code geass", 1, "sub")
	if err != nil || len(eps) != 25 || eps[6] != "7" {
		t.Fatalf("episodes: %v %v", eps, err)
	}
	st, err := drv.Resolve(ctx, "code geass", 1, "7", "dub", "720")
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://cdn.example/dub/ep7/720.m3u8" || st.Referrer != "https://embed.example/" || st.SubFile != "https://subs.example/7.vtt" {
		t.Fatalf("resolve: %+v", st)
	}
	if st.Title != "Code Geass: Lelouch of the Rebellion Episode 7" {
		t.Fatalf("title: %q", st.Title)
	}
	t.Logf("resolved: %+v", st)
}
