package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/simo1337s/animetest/server/internal/anicli"
)

// fakeAniCli installs the ani-cli stand-in of the anicli tests, listing
// catalog ("id<TAB>title<TAB>episodes[<TAB>modes]" lines).
func fakeAniCli(t *testing.T, catalog ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"fake-ani-cli", "scrape-stubs.sh"} {
		b, err := os.ReadFile(filepath.Join("..", "anicli", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "fake-ani-cli" {
			name = "ani-cli"
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cat := filepath.Join(dir, "catalog.tsv")
	if err := os.WriteFile(cat, []byte(strings.Join(catalog, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUMO_FAKE_CATALOG", cat)
	t.Setenv("KUMO_FAKE_LOG", filepath.Join(dir, "runs.log"))
	return filepath.Join(dir, "ani-cli")
}

// A Kumo without ani-cli (a TV) streams through a computer that shares its
// library with it and has ani-cli.
func TestAniCliThroughHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the ani-cli stand-in is a shell script")
	}
	const local = "127.0.0.1:5000"
	host, hostSrv := sharingServer(t)
	guest, _ := sharingServer(t)
	script := fakeAniCli(t, "fr1\tFrieren: Beyond Journey's End\t28\tsub")
	setAniCli := func(s *Server, path string) {
		cfg := s.app.Settings.Get()
		cfg.AniCli.Path = path
		if _, err := s.app.Settings.Save(cfg); err != nil {
			t.Fatal(err)
		}
	}
	setAniCli(host, script)
	setAniCli(guest, filepath.Join(t.TempDir(), "no-ani-cli"))
	if !host.app.AniCli.LocalReady() || guest.app.AniCli.Available() {
		t.Fatal("ani-cli should run on the host only")
	}

	addr := strings.TrimPrefix(hostSrv.URL, "http://")
	if w := do(guest, "POST", "/api/sharing/connect", local, `{"address":"`+addr+`"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	eventually(t, "the host to list the guest", func() bool { return len(host.app.Share.Status().Peers) == 1 })
	asked := func(ok func() bool) func() bool {
		return func() bool { do(guest, "GET", "/api/sharing", local, "", nil); return ok() }
	}
	ctx := context.Background()

	// Not shared with: the host doesn't run ani-cli for it.
	if _, err := guest.app.AniCli.Search(ctx, "Frieren", "sub"); err == nil {
		t.Fatal("ani-cli ran for a Kumo the host doesn't share with")
	}

	if err := host.app.Share.SetAllowed(guest.app.Share.ID(), true); err != nil {
		t.Fatal(err)
	}
	hostName := ""
	eventually(t, "ani-cli to run on the host", asked(func() bool {
		hostName = guest.app.AniCli.RemoteName()
		return hostName != ""
	}))
	var st struct {
		Features featureStatus `json:"features"`
	}
	if w := do(guest, "GET", "/api/status", local, "", nil); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &st) != nil || !st.Features.AniCli || st.Features.AniCliHost != hostName {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}

	res, err := guest.app.AniCli.Search(ctx, "Frieren", "sub")
	if err != nil || len(res) != 1 || !strings.HasPrefix(res[0].Title, "Frieren") {
		t.Fatalf("search: %+v %v", res, err)
	}
	eps, err := guest.app.AniCli.Episodes(ctx, "Frieren", res[0].Index, "sub")
	if err != nil || len(eps) != 28 {
		t.Fatalf("episodes: %v %v", eps, err)
	}
	s, err := guest.app.AniCli.Resolve(ctx, "Frieren", res[0].Index, "3", "sub", "")
	if err != nil || !strings.HasPrefix(s.URL, "https://cdn.example/") || s.Episode != "3" {
		t.Fatalf("resolve: %+v %v", s, err)
	}
	// Not dubbed: the same error as ani-cli's here, so the dub button goes.
	if _, err := guest.app.AniCli.Resolve(ctx, "Frieren", res[0].Index, "3", "dub", ""); !errors.Is(err, anicli.ErrNoSources) {
		t.Fatalf("dub: %v", err)
	}

	// No longer shared with.
	if err := host.app.Share.SetAllowed(guest.app.Share.ID(), false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "ani-cli to stop running on the host", asked(func() bool { return guest.app.AniCli.RemoteName() == "" }))
	if _, err := guest.app.AniCli.Resolve(ctx, "Frieren", res[0].Index, "4", "sub", ""); err == nil {
		t.Fatal("ani-cli still ran on the host")
	}
}
