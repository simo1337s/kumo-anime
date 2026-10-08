//go:build !windows

package macsetup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSetupRunsInTerminal(t *testing.T) {
	tmp := t.TempDir()
	var opens []string
	dir, open, goos = func() string { return tmp }, func(p string) error { opens = append(opens, p); return nil }, "darwin"
	t.Cleanup(func() { opened = time.Time{} })

	if Running() {
		t.Fatal("running before it was opened")
	}
	if err := Start(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmp, name)
	if len(opens) != 1 || opens[0] != path {
		t.Fatalf("opened %q, want %q", opens, path)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("script %v (%v): want it executable by its owner only", st.Mode(), err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, script) {
		t.Error("the script isn't install-tools.command")
	}
	// Until the script wrote its pid file, it counts as running.
	if !Running() || Start() != ErrRunning {
		t.Fatal("not running right after it was opened")
	}

	// The script runs (a stand-in process writes its pid file).
	sh := exec.Command("sleep", "30")
	if err := sh.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".pid", []byte(strconv.Itoa(sh.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opened = time.Now().Add(-time.Hour)
	if !Running() {
		t.Fatal("not running while its process is")
	}
	// The Terminal window was closed: the process is gone.
	_ = sh.Process.Kill()
	_ = sh.Wait()
	if Running() {
		t.Fatal("still running after its process ended")
	}
	if err := Start(); err != nil {
		t.Fatalf("can't open it again: %v", err)
	}
	if len(opens) != 2 {
		t.Errorf("opened %d times, want 2", len(opens))
	}
}

func TestSetupIsForMacOS(t *testing.T) {
	tmp := t.TempDir()
	dir, open, goos = func() string { return tmp }, func(string) error { t.Fatal("opened"); return nil }, "linux"
	if err := Start(); err == nil {
		t.Fatal("Start opened the setup on Linux")
	}
}
