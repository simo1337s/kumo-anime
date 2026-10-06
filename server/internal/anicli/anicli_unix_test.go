//go:build !windows

package anicli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// slowAniCli is an ani-cli that waits for a slow child, as ani-cli waits for
// curl in a subshell; the child's pid is written to the returned file.
func slowAniCli(t *testing.T) (script, pidFile string) {
	t.Helper()
	pidFile = filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("KUMO_TEST_PIDFILE", pidFile)
	script = filepath.Join(t.TempDir(), "ani-cli")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
(sleep 20; echo late) &
echo $! >"$KUMO_TEST_PIDFILE"
wait
`), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, pidFile
}

// waitGone fails the test unless the process in pidFile ends soon.
func waitGone(t *testing.T, pidFile string) {
	t.Helper()
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("ani-cli's child %d is still running", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// alive reports whether a process exists and is not a zombie.
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) {
		return s[i+2] != 'Z'
	}
	return true
}

// When the request goes away, every process ani-cli started has to go, not
// just the shell: the others hold its output open.
func TestCanceledRunStopsEverything(t *testing.T) {
	script, pidFile := slowAniCli(t)
	h := newHarnessWith(t, script)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, err := h.drv.Search(ctx, "anything", "sub")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("Search returned after %v", d)
	}
	waitGone(t, pidFile)
}

func TestRunTimesOut(t *testing.T) {
	script, pidFile := slowAniCli(t)
	h := newHarnessWith(t, script)
	h.drv.timeout = 300 * time.Millisecond
	start := time.Now()
	_, err := h.drv.Episodes(context.Background(), "anything", 1, "sub")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want a time out", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("Episodes returned after %v", d)
	}
	waitGone(t, pidFile)
}
