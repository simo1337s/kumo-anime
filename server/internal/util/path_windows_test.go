//go:build windows

package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A program installed while Kumo runs (e.g. by Scoop) is found once it's on
// the PATH Windows gives new programs, and Kumo's own PATH gets its folder.
func TestLookPathFindsProgramsInstalledMeanwhile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kumo-test-tool.exe"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", os.Getenv("PATH")) // restored afterwards
	old := newPathDirs
	t.Cleanup(func() {
		newPathDirs = old
		pathChecked = time.Time{}
	})

	newPathDirs = func() []string { return nil }
	pathChecked = time.Time{}
	if _, ok := LookPath("kumo-test-tool"); ok {
		t.Fatal("found before it's on the PATH")
	}

	newPathDirs = func() []string { return []string{dir, filepath.Join(dir, "missing")} }
	pathChecked = time.Time{}
	p, ok := LookPath("kumo-test-tool")
	if !ok || !strings.EqualFold(filepath.Dir(p), dir) {
		t.Fatalf("LookPath = %q %v, want it in %s", p, ok, dir)
	}
	path := os.Getenv("PATH")
	if !strings.Contains(path, dir) || strings.Contains(path, filepath.Join(dir, "missing")) {
		t.Errorf("PATH %q should have %s, and not its missing folder", path, dir)
	}
	// Already there: nothing to add.
	pathChecked = time.Time{}
	if refreshPath() {
		t.Error("refreshPath added folders twice")
	}
}

func TestRegistryPathDirs(t *testing.T) {
	if len(registryPathDirs()) == 0 {
		t.Error("no PATH in the registry")
	}
}
