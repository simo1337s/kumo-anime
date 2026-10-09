package stream

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/simo1337s/animetest/server/internal/anicli"
	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
)

// aniCliService is a Service whose ani-cli is the stand-in of the anicli
// tests, listing the catalog given ("id<TAB>title<TAB>episodes<TAB>modes"
// lines). It counts its runs.
func aniCliService(t *testing.T, catalog ...string) (*Service, func() int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in ani-cli is a shell script")
	}
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	t.Setenv("KUMO_DATA_DIR", tmp)
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fake-ani-cli", "scrape-stubs.sh"} {
		b, err := os.ReadFile(filepath.Join("..", "anicli", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, name), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cat := filepath.Join(tmp, "catalog.tsv")
	if err := os.WriteFile(cat, []byte(strings.Join(catalog, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(tmp, "runs.log")
	t.Setenv("KUMO_FAKE_CATALOG", cat)
	t.Setenv("KUMO_FAKE_LOG", log)
	d, err := db.Open(filepath.Join(tmp, "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	s := store.Get()
	s.AniCli.Path = filepath.Join(bin, "fake-ani-cli")
	if _, err := store.Save(s); err != nil {
		t.Fatal(err)
	}
	runs := func() int {
		b, _ := os.ReadFile(log)
		return strings.Count(string(b), "run\t")
	}
	return NewService(d, nil, anicli.New(store), nil), runs
}

// ani-cli lists the same anime and episodes in sub and in dub: whether it
// has a dub shows only when the stream is asked for.
func TestModesFromAniCli(t *testing.T) {
	svc, runs := aniCliService(t,
		"sub-only\tSub Only Show\t3\tsub",
		"dub-only\tDub Only Show\t2\tdub",
		"both\tBoth Ways Show\t12\tsub dub",
	)
	ctx := context.Background()
	for _, c := range []struct {
		id    int
		title string
		want  Modes
	}{
		{1, "Sub Only Show", Modes{Sub: true}},
		{2, "Dub Only Show", Modes{Dub: true}},
		{3, "Both Ways Show", Modes{Sub: true, Dub: true}},
		// Not on the site: both, to pick it by hand.
		{4, "Nowhere To Be Found", Modes{Sub: true, Dub: true}},
	} {
		media := &anilist.Media{ID: c.id, Title: anilist.Title{English: c.title}}
		if got := svc.Modes(ctx, AniCliProvider, media, false); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.title, got, c.want)
		}
	}

	// Remembered: no ani-cli run the second time.
	before := runs()
	media := &anilist.Media{ID: 1, Title: anilist.Title{English: "Sub Only Show"}}
	if got := svc.Modes(ctx, AniCliProvider, media, false); got != (Modes{Sub: true}) {
		t.Errorf("again: %+v", got)
	}
	if runs() != before {
		t.Errorf("ani-cli ran %d more times", runs()-before)
	}
}

// When ani-cli can't be asked (here it isn't installed), both are offered,
// and the answer isn't remembered.
func TestModesUnknown(t *testing.T) {
	svc, _ := aniCliService(t, "sub-only\tSub Only Show\t3\tsub")
	media := &anilist.Media{ID: 1, Title: anilist.Title{English: "Sub Only Show"}}
	if err := os.Remove(filepath.Join(os.Getenv("KUMO_DATA_DIR"), "bin", "fake-ani-cli")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if got := svc.Modes(ctx, AniCliProvider, media, false); got != (Modes{Sub: true, Dub: true}) {
		t.Fatalf("without ani-cli: %+v", got)
	}
	var cached Modes
	if svc.db.GetCache("os-eps:ani-cli:1:modes", &cached) {
		t.Fatalf("remembered %+v", cached)
	}
}
