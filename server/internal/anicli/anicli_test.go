package anicli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
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

// harness runs a Driver against a stand-in ani-cli (testdata/fake-ani-cli)
// whose search results come from a catalog the test controls. Set
// KUMO_REAL_ANICLI to a real ani-cli 5.x script to run the same tests against
// it instead: its scraping functions are replaced by testdata/scrape-stubs.sh,
// so the menus and argument parsing are the real ones but nothing touches
// the network.
type harness struct {
	drv     *Driver
	db      *db.DB
	dir     string
	catalog string
	log     string
}

func newHarness(t *testing.T, catalog ...string) *harness {
	t.Helper()
	return newHarnessWith(t, installAniCli(t), catalog...)
}

func newHarnessWith(t *testing.T, script string, catalog ...string) *harness {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	t.Setenv("LOCALAPPDATA", tmp) // the runtime dir on Windows
	t.Setenv("KUMO_DATA_DIR", tmp)
	h := &harness{dir: tmp, catalog: filepath.Join(tmp, "catalog.tsv"), log: filepath.Join(tmp, "runs.log")}
	t.Setenv("KUMO_FAKE_CATALOG", shellPath(h.catalog))
	t.Setenv("KUMO_FAKE_LOG", shellPath(h.log))
	h.setCatalog(t, catalog...)
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
	s.AniCli.Path = script
	if _, err := store.Save(s); err != nil {
		t.Fatal(err)
	}
	h.drv, h.db = New(store), d
	h.drv.searchTTL = 0 // the tests change what ani-cli lists between searches
	return h
}

// installAniCli copies the ani-cli the tests run into a temporary directory.
func installAniCli(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stubs, err := os.ReadFile(filepath.Join("testdata", "scrape-stubs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ani-cli")
	if real := os.Getenv("KUMO_REAL_ANICLI"); real != "" {
		src, err := os.ReadFile(real)
		if err != nil {
			t.Fatal(err)
		}
		i := bytes.Index(src, []byte("\n# MAIN\n"))
		if i < 0 {
			t.Fatalf("%s has no \"# MAIN\" line to put the scraping stubs before", real)
		}
		script := append(append(append([]byte{}, src[:i+1]...), stubs...), src[i+1:]...)
		if err := os.WriteFile(path, script, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	fake, err := os.ReadFile(filepath.Join("testdata", "fake-ani-cli"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scrape-stubs.sh"), stubs, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fake, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// setCatalog replaces the search catalog: "id<TAB>title<TAB>episodes" lines,
// in the order the site lists them.
func (h *harness) setCatalog(t *testing.T, entries ...string) {
	t.Helper()
	if err := os.WriteFile(h.catalog, []byte(strings.Join(entries, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func entry(id, title string, episodes int, modes ...string) string {
	e := id + "\t" + title + "\t" + strconv.Itoa(episodes)
	if len(modes) > 0 {
		e += "\t" + strings.Join(modes, " ")
	}
	return e
}

// runs returns the arguments of every ani-cli run so far.
func (h *harness) runs(t *testing.T) [][]string {
	t.Helper()
	b, err := os.ReadFile(h.log)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Split(line, "\t"); f[0] == "run" {
			out = append(out, f[1:])
		}
	}
	return out
}

// logged reports whether a stub logged a line starting with prefix.
func (h *harness) logged(t *testing.T, prefix string) bool {
	t.Helper()
	b, _ := os.ReadFile(h.log)
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

var codeGeass = []string{
	entry("code-geass", "Code Geass: Lelouch of the Rebellion", 25),
	entry("code-geass-r2", "Code Geass: Lelouch of the Rebellion R2", 25),
	entry("code-geass-movie", "Code Geass: Lelouch of the Re;surrection", 1),
}

// A movie (or an OVA) has a single episode: ani-cli picks it without showing
// the episode menu, plays it, and then shows its "Playing episode 1 of ..."
// menu. That menu must not be mistaken for the episode list.
func TestSingleEpisodeEntries(t *testing.T) {
	h := newHarness(t, append([]string{
		entry("your-name", "Your Name.", 1, "sub"),
		entry("anime-movie", "Gekijouban Anime Movie", 1),
	}, codeGeass...)...)
	ctx := context.Background()

	res, err := h.drv.Search(ctx, "your name", "sub")
	if err != nil {
		t.Fatal(err)
	}
	if want := []Result{{Index: 1, Title: "Your Name.", Episodes: 1}}; !reflect.DeepEqual(res, want) {
		t.Fatalf("search: got %+v, want %+v", res, want)
	}
	eps, err := h.drv.Episodes(ctx, "your name", 1, "sub")
	if err != nil || !reflect.DeepEqual(eps, []string{"1"}) {
		t.Fatalf("episodes: %q %v", eps, err)
	}
	st, err := h.drv.Resolve(ctx, "your name", 1, "1", "sub", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://cdn.example/sub/ep1/1080.m3u8" || st.Title != "Your Name. Episode 1" {
		t.Fatalf("resolve: %+v", st)
	}

	// The title of the "Playing episode ..." prompt contains "nime".
	res, err = h.drv.Search(ctx, "anime movie", "sub")
	if err != nil {
		t.Fatal(err)
	}
	if want := []Result{{Index: 1, Title: "Gekijouban Anime Movie", Episodes: 1}}; !reflect.DeepEqual(res, want) {
		t.Fatalf("search: got %+v, want %+v", res, want)
	}
	eps, err = h.drv.Episodes(ctx, "anime movie", 1, "dub")
	if err != nil || !reflect.DeepEqual(eps, []string{"1"}) {
		t.Fatalf("episodes: %q %v", eps, err)
	}

	// A movie among several results.
	res, err = h.drv.Search(ctx, "code geass", "sub")
	if err != nil || len(res) != 3 || res[2].Title != "Code Geass: Lelouch of the Re;surrection" {
		t.Fatalf("search: %+v %v", res, err)
	}
	eps, err = h.drv.Episodes(ctx, "code geass", 3, "sub")
	if err != nil || !reflect.DeepEqual(eps, []string{"1"}) {
		t.Fatalf("episodes: %q %v", eps, err)
	}
	eps, err = h.drv.Episodes(ctx, "code geass", 2, "sub")
	if err != nil || len(eps) != 25 {
		t.Fatalf("episodes: %q %v", eps, err)
	}

	// No dub: the error says so instead of listing bogus episodes.
	if eps, err := h.drv.Episodes(ctx, "your name", 1, "dub"); err == nil || !strings.Contains(err.Error(), "No sources found for dub") {
		t.Fatalf("dub episodes: %q %v", eps, err)
	}
}

// A single search result is picked without a menu, so its title is unknown
// unless ani-cli plays it.
func TestSearchSingleResult(t *testing.T) {
	h := newHarness(t, entry("single-show", "Single Result Show", 12))
	res, err := h.drv.Search(context.Background(), "single", "sub")
	if want := []Result{{Index: 1, Title: "single", Episodes: 12}}; err != nil || !reflect.DeepEqual(res, want) {
		t.Fatalf("search: got %+v %v, want %+v", res, err, want)
	}
	if res, err := h.drv.Search(context.Background(), "nothing like it", "sub"); err != nil || len(res) != 0 {
		t.Fatalf("no results: %+v %v", res, err)
	}
}

// Titles go into ani-cli's search URL as they are: curl expands [] and {} as
// globs, and & # % break the query string.
func TestQueriesWithSpecialCharacters(t *testing.T) {
	h := newHarness(t,
		entry("oshi-no-ko", "[Oshi no Ko]", 11),
		entry("oshi-no-ko-2", "[Oshi no Ko] Season 2", 13),
		entry("fate-ubw", "Fate/stay night: Unlimited Blade Works", 12),
		entry("tom-and-jerry", "Tom & Jerry Kids", 4),
		entry("tomodachi", "Tomodachi Game", 12),
		entry("pascal", "100% Pascal-sensei", 12),
	)
	ctx := context.Background()

	res, err := h.drv.Search(ctx, "[Oshi no Ko]", "sub")
	if err != nil || len(res) != 2 || res[1].Title != "[Oshi no Ko] Season 2" {
		t.Fatalf("search: %+v %v", res, err)
	}
	eps, err := h.drv.Episodes(ctx, "[Oshi no Ko]", 2, "sub")
	if err != nil || len(eps) != 13 {
		t.Fatalf("episodes: %q %v", eps, err)
	}
	st, err := h.drv.Resolve(ctx, "[Oshi no Ko]", 2, "3", "sub", "")
	if err != nil || st.Title != "[Oshi no Ko] Season 2 Episode 3" {
		t.Fatalf("resolve: %+v %v", st, err)
	}
	res, err = h.drv.Search(ctx, "Fate/stay night [Unlimited Blade Works]", "dub")
	if err != nil || len(res) != 1 || res[0].Episodes != 12 {
		t.Fatalf("search: %+v %v", res, err)
	}
	res, err = h.drv.Search(ctx, "Tom & Jerry", "sub")
	if err != nil || len(res) != 1 || res[0].Episodes != 4 {
		t.Fatalf("search: %+v %v", res, err)
	}
	res, err = h.drv.Search(ctx, "100% Pascal-sensei", "sub")
	if err != nil || len(res) != 1 || res[0].Episodes != 12 {
		t.Fatalf("search: %+v %v", res, err)
	}
	runs := h.runs(t)
	if len(runs) == 0 {
		t.Fatal("no ani-cli runs were logged")
	}
	for _, run := range runs {
		if len(run) == 0 {
			t.Fatal("ani-cli was run without arguments")
		}
		if q := run[len(run)-1]; strings.ContainsAny(q, "[]{}()&#%?\"\\") || strings.HasPrefix(q, "-") {
			t.Errorf("ani-cli was run with %q", run)
		}
	}
}

// A query that looks like one of ani-cli's options must not run it: -U
// updates the script and -D deletes the history.
func TestQueryIsNeverAnOption(t *testing.T) {
	h := newHarness(t, entry("u-show", "Urusei Yatsura", 46), entry("d-show", "Dororo", 24))
	hist := filepath.Join(h.dir, "ani-cli", "ani-hsts")
	if err := os.MkdirAll(filepath.Dir(hist), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hist, []byte("3\tsome-id\tSome Anime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, q := range []string{"-U", "--update", "-D", "--delete", "- -D", "-h"} {
		if _, err := h.drv.Search(ctx, q, "sub"); err != nil {
			t.Errorf("search %q: %v", q, err)
		}
		_, _ = h.drv.Episodes(ctx, q, 1, "sub")
		_, _ = h.drv.Resolve(ctx, q, 1, "1", "sub", "")
	}
	if h.logged(t, "update") {
		t.Error("a search ran ani-cli -U")
	}
	if b, _ := os.ReadFile(hist); !strings.Contains(string(b), "Some Anime") {
		t.Error("a search ran ani-cli -D")
	}
	if res, err := h.drv.Search(ctx, "--", "sub"); err == nil {
		t.Errorf("search --: %+v", res)
	}
}

func TestCleanQuery(t *testing.T) {
	for in, want := range map[string]string{
		"[Oshi no Ko]": "Oshi no Ko",
		"Fate/stay night [Unlimited Blade Works]": "Fate/stay night Unlimited Blade Works",
		"Tom & Jerry":                           "Tom Jerry",
		"100% Pascal-sensei":                    "100 Pascal-sensei",
		"#1 {Fan} (2011)?":                      "1 Fan 2011",
		"say \"hi\" \\o/ <b>|^`x`":              "say hi o/ b x",
		"  spaced\tout\nquery\x00  ":            "spaced out query",
		"-U":                                    "U",
		"--delete":                              "delete",
		"- -D":                                  "D",
		"--":                                    "",
		"[]":                                    "",
		"Re:Zero kara Hajimeru Isekai Seikatsu": "Re:Zero kara Hajimeru Isekai Seikatsu",
		"JoJo's Bizarre Adventure":              "JoJo's Bizarre Adventure",
		"K-On!":                                 "K-On!",
		"Steins;Gate 0":                         "Steins;Gate 0",
		"Kaguya-sama: Love Is War -Ultra Romantic-": "Kaguya-sama: Love Is War -Ultra Romantic-",
		"葬送のフリーレン":                                  "葬送のフリーレン",
	} {
		if got := cleanQuery(in); got != want {
			t.Errorf("cleanQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

// The menu shim files each menu by its prompt. The one ani-cli shows after
// playing an episode is never an episode list or a list of search results.
func TestMenuShim(t *testing.T) {
	h := newHarnessWith(t, "/bin/true")
	if err := h.drv.init(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ menu, flag, prompt, kind string }{
		{"fzf", "--prompt", "Select anime: ", "anime"},
		{"rofi", "-p", "Select episode: ", "episodes"},
		{"dmenu", "-p", "Playing episode 1 of Your Name.... ", "control"},
		{"rofi", "-p", "Playing episode 1 of Gekijouban Anime Movie... ", "control"},
		{"fzf", "--prompt", "Playing episode 12 of Some Show... ", "control"},
		{"rofi", "-p", "Select Quality: ", "quality"},
	} {
		dump := t.TempDir()
		cmd := exec.Command(filepath.Join(h.drv.shimDir, c.menu), "-sort", c.flag, c.prompt, "-multi-select")
		cmd.Env = append(os.Environ(), "KUMO_DUMP_DIR="+dump)
		cmd.Stdin = strings.NewReader("next\nreplay\nquit\n")
		if out, err := cmd.Output(); err == nil || len(out) > 0 {
			t.Errorf("%s %q: selected %q (%v)", c.menu, c.prompt, out, err)
		}
		files, _ := filepath.Glob(filepath.Join(dump, "*.txt"))
		if want := []string{filepath.Join(dump, c.kind+".txt")}; !reflect.DeepEqual(files, want) {
			t.Errorf("%s %q: wrote %v, want %v", c.menu, c.prompt, files, want)
		}
	}
}

func TestPlayed(t *testing.T) {
	for _, c := range []struct{ args, title, episode string }{
		{"--referrer=https://embed.example/\n--force-media-title=Your Name. Episode 1\nhttps://cdn.example/1.m3u8\n", "Your Name.", "1"},
		{"--force-media-title=Gintama: The Final Episode Episode 1\n", "Gintama: The Final Episode", "1"},
		{"--mpv-force-media-title=Some Show Episode 12.5\n", "Some Show", "12.5"},
		{"https://cdn.example/1.m3u8\n", "", ""},
	} {
		r := &run{dir: t.TempDir()}
		if err := os.WriteFile(filepath.Join(r.dir, "player.txt"), []byte(c.args), 0o600); err != nil {
			t.Fatal(err)
		}
		title, ep, ok := r.played()
		if !ok || title != c.title || ep != c.episode {
			t.Errorf("played(%q) = %q, %q, %v", c.args, title, ep, ok)
		}
	}
	if _, _, ok := (&run{dir: t.TempDir()}).played(); ok {
		t.Error("played() without a player run")
	}
}
