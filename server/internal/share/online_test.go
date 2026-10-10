package share

import (
	"testing"
	"time"
)

// A host is asked every askEvery: it stays online between its answers, even
// when its beacons don't come through (TVs whose Wi-Fi drops them).
func TestHostOnlineBetweenAnswers(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name string
		p    peer
		want bool
	}{
		{"heard just now", peer{seen: now.Add(-time.Second)}, true},
		{"a guest not heard for 20s", peer{seen: now.Add(-20 * time.Second)}, false},
		{"a host that answered 20s ago", peer{shares: true, seen: now.Add(-20 * time.Second)}, true},
		{"a host that answered 40s ago", peer{shares: true, seen: now.Add(-40 * time.Second)}, true},
		{"a host that stopped answering", peer{shares: true, fails: 1, seen: now.Add(-20 * time.Second)}, false},
		{"a host gone for a minute", peer{shares: true, seen: now.Add(-time.Minute)}, false},
	} {
		if got := c.p.online(now); got != c.want {
			t.Errorf("%s: online %v, want %v", c.name, got, c.want)
		}
	}
}
