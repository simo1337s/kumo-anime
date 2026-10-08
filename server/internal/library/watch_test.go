package library

import (
	"path/filepath"
	"testing"
)

func TestOverBudget(t *testing.T) {
	base := t.TempDir()
	var dirs []string
	for _, show := range []string{"Frieren", "Bocchi"} {
		dir := filepath.Join(base, show)
		dirs = append(dirs, dir)
		for _, ep := range []string{"01.mkv", "02.mkv", "02.ass"} {
			touch(t, filepath.Join(dir, ep))
		}
	}
	// Each folder and the 3 files in it: 8 to watch.
	if n, over := overBudget(dirs, 8); over || n != 8 {
		t.Errorf("budget 8: %d entries, over %v; want 8, not over", n, over)
	}
	if n, over := overBudget(dirs, 7); !over || n != 8 {
		t.Errorf("budget 7: %d entries, over %v; want over at 8", n, over)
	}
	// A folder that's gone counts as itself only.
	if n, over := overBudget([]string{filepath.Join(base, "gone")}, 5); over || n != 1 {
		t.Errorf("missing folder: %d, %v", n, over)
	}
}
