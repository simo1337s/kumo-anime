package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
)

const fakeMpvEnv = "KUMO_PLAYER_TEST_FAKE_MPV"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeMpvEnv); mode != "" {
		os.Exit(fakeMpvProcess(mode))
	}
	os.Exit(m.Run())
}

// fakeMpvProcess stands in for the mpv binary (the test binary run with
// fakeMpvEnv set): it serves the IPC socket like mpv, then either plays the
// file to its end and exits at once ("eof"), or plays until told to quit
// ("play").
func fakeMpvProcess(mode string) int {
	var sock string
	for _, a := range os.Args[1:] {
		if v, ok := strings.CutPrefix(a, "--input-ipc-server="); ok {
			sock = v
		}
	}
	ln, err := listenIPC(sock)
	if err != nil {
		return 2
	}
	conn, err := ln.Accept()
	if err != nil {
		return 2
	}
	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = conn.Write(append(b, '\n'))
	}
	observed := 0
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		var req struct {
			Command   []any `json:"command"`
			RequestID int64 `json:"request_id"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.Command) == 0 {
			continue
		}
		name, arg := fmt.Sprint(req.Command[0]), ""
		if len(req.Command) > 1 {
			arg = fmt.Sprint(req.Command[1])
		}
		reply := map[string]any{"request_id": req.RequestID, "error": "success"}
		switch {
		case name == "observe_property":
			send(reply)
			if observed++; observed == 6 {
				send(map[string]any{"event": "file-loaded"})
			}
		case name == "get_property" && arg == "track-list":
			reply["data"] = trackListJSON(testTracks)
			send(reply)
			if mode == "eof" {
				// Reach the end and exit right away, like mpv --idle=no.
				send(map[string]any{"event": "property-change", "name": "duration", "data": 1420.0})
				send(map[string]any{"event": "property-change", "name": "time-pos", "data": 1419.0})
				send(map[string]any{"event": "end-file", "reason": "eof"})
				return 0
			}
		case name == "quit":
			send(reply)
			send(map[string]any{"event": "end-file", "reason": "quit"})
			return 0
		default:
			send(reply)
		}
	}
	return 0
}

func TestLaunchMpvProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := shortTempDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOCALAPPDATA", runtimeDir)
	newManager := func(t *testing.T, mode string) *Manager {
		t.Setenv(fakeMpvEnv, mode)
		m, _ := newTestManager(t, func(c *config.Settings) { c.Mpv.Path = exe })
		m.launch = LaunchMpv
		return m
	}
	noSockets := func(t *testing.T) {
		t.Helper()
		if left := leftoverIPC(runtimeDir); len(left) > 0 {
			t.Errorf("IPC sockets left behind: %v", left)
		}
	}

	t.Run("plays to the end", func(t *testing.T) {
		m := newManager(t, "eof")
		if _, err := m.PlayMpv(PlayRequest{MediaID: 4, Episode: 1, Source: "local", Target: "/anime/01.mkv"}); err != nil {
			t.Fatal(err)
		}
		// The end-file mpv wrote just before exiting must not get lost.
		waitFor(t, "the episode to be marked finished", func() bool {
			e := m.history.Get(4, 1)
			return e != nil && e.Duration == 1420 && e.Position == e.Duration
		})
		waitFor(t, "the session to end", func() bool { return m.Status() == nil })
		noSockets(t)
	})

	t.Run("quits on Stop", func(t *testing.T) {
		m := newManager(t, "play")
		if _, err := m.PlayMpv(PlayRequest{MediaID: 4, Episode: 2, Source: "local", Target: "/anime/02.mkv"}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the file to load", func() bool {
			st := m.Status()
			return st != nil && len(st.Tracks) == len(testTracks)
		})
		start := time.Now()
		m.Stop()
		if st := m.Status(); st != nil {
			t.Fatalf("still playing after Stop: %+v", st)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("Stop took %v; mpv should quit when asked", d)
		}
		noSockets(t)
	})
}
