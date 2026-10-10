package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/db"
)

func TestMergeKeepsTheNewest(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := NewStore(d)
	if err := s.Save(Entry{MediaID: 1, Episode: 2, Position: 100, Duration: 1400, Source: "local"}); err != nil {
		t.Fatal(err)
	}
	here := s.Get(1, 2).UpdatedAt

	// Older, from another Kumo: this one's stays.
	if ok, _ := s.Merge(Entry{MediaID: 1, Episode: 2, Position: 900, Duration: 1400, Source: "local", UpdatedAt: here - 60}); ok {
		t.Error("an older position replaced a newer one")
	}
	// Newer: taken, with its time.
	if ok, _ := s.Merge(Entry{MediaID: 1, Episode: 2, Position: 700, Duration: 1400, Source: "local", UpdatedAt: here + 60}); !ok {
		t.Error("a newer position wasn't taken")
	}
	if e := s.Get(1, 2); e.Position != 700 || e.UpdatedAt != here+60 {
		t.Errorf("entry %+v", e)
	}
	// One this Kumo hadn't.
	if ok, _ := s.Merge(Entry{MediaID: 5, Episode: 1, Position: 300, Duration: 1400, UpdatedAt: here + 120}); !ok {
		t.Error("a new entry wasn't taken")
	}
	if got := s.Since(here+60, 10); len(got) != 1 || got[0].MediaID != 5 {
		t.Errorf("since: %+v", got)
	}
	if got := s.Since(0, 10); len(got) != 2 || got[0].UpdatedAt > got[1].UpdatedAt {
		t.Errorf("since 0, oldest first: %+v", got)
	}
	// From the future: taken as from now, so what's saved here next wins.
	if ok, _ := s.Merge(Entry{MediaID: 1, Episode: 2, Position: 5, Duration: 1400, UpdatedAt: here + 10*365*24*3600}); !ok {
		t.Error("a newer position wasn't taken")
	}
	if e := s.Get(1, 2); e.UpdatedAt > time.Now().Add(maxAhead).Unix() {
		t.Errorf("a time from the future was kept: %+v", e)
	}
	if ok, _ := s.Merge(Entry{MediaID: 0, Episode: 1, UpdatedAt: here}); ok {
		t.Error("a bad entry was taken")
	}
}
