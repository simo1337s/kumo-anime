package api

import (
	"context"
	"errors"
	"testing"

	"github.com/simo1337s/animetest/server/internal/torrent"
)

type fakeProvider struct {
	id  string
	res []*torrent.SearchResult
	err error
}

func (f *fakeProvider) ID() string   { return f.id }
func (f *fakeProvider) Name() string { return f.id }
func (f *fakeProvider) Search(context.Context, string) ([]*torrent.SearchResult, error) {
	return f.res, f.err
}
func (f *fakeProvider) SmartSearch(context.Context, torrent.SmartQuery) ([]*torrent.SearchResult, error) {
	return f.res, f.err
}
func (f *fakeProvider) Magnet(_ context.Context, r *torrent.SearchResult) (string, error) {
	return "magnet:?xt=urn:btih:" + r.InfoHash, nil
}

func TestSearchAllProviders(t *testing.T) {
	s := newTestServer(t)
	// Replace the built-in providers (they'd need the internet) with fakes.
	s.app.Torrents.Register(&fakeProvider{id: "nyaa", res: []*torrent.SearchResult{
		{Name: "[Judas] Show (Batch)", InfoHash: "AAAA", Seeders: 300},
		{Name: "[SubsPlease] Show - 07 (1080p)", InfoHash: "bbbb", Seeders: 50},
	}})
	s.app.Torrents.Register(&fakeProvider{id: "animetosho", res: []*torrent.SearchResult{
		{Name: "[Judas] Show (Batch)", MagnetLink: "magnet:?xt=urn:btih:aaaa&dn=x", Seeders: 310}, // same torrent
		{Name: "[Erai-raws] Show - 07 [1080p]", InfoHash: "cccc", Seeders: 80, IsBestRelease: true},
	}})
	search := func(ctx context.Context, p torrent.Provider) ([]*torrent.SearchResult, error) {
		return p.SmartSearch(ctx, torrent.SmartQuery{})
	}
	res, err := s.searchAllProviders(context.Background(), search)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range res {
		names = append(names, r.Name+"@"+r.Provider)
	}
	want := []string{"[Erai-raws] Show - 07 [1080p]@animetosho", "[Judas] Show (Batch)@animetosho", "[SubsPlease] Show - 07 (1080p)@nyaa"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Fatalf("got %q\nwant %q", names, want)
	}

	// One provider failing doesn't fail the search; all failing does.
	s.app.Torrents.Register(&fakeProvider{id: "nyaa", err: errors.New("nyaa is down")})
	if res, err := s.searchAllProviders(context.Background(), search); err != nil || len(res) != 2 {
		t.Fatalf("one provider down: %d results, %v", len(res), err)
	}
	s.app.Torrents.Register(&fakeProvider{id: "animetosho", err: errors.New("down too")})
	if _, err := s.searchAllProviders(context.Background(), search); err == nil {
		t.Fatal("expected an error when every provider fails")
	}
}
