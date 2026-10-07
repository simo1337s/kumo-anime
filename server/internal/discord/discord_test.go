//go:build !windows

package discord

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeDiscord answers like the Discord app's IPC socket and keeps the
// activities it was sent. With oldVersion, it refuses status_display_type
// like a Discord from before it existed.
type fakeDiscord struct {
	oldVersion bool

	mu   sync.Mutex
	acts []map[string]any
}

func (d *fakeDiscord) activities() []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]map[string]any(nil), d.acts...)
}

func writeFrame(w io.Writer, op uint32, v any) error {
	raw, _ := json.Marshal(v)
	hdr := make([]byte, 8)
	binary.LittleEndian.PutUint32(hdr, op)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(raw)))
	_, err := w.Write(append(hdr, raw...))
	return err
}

func (d *fakeDiscord) serve(conn net.Conn) {
	defer conn.Close()
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		body := make([]byte, binary.LittleEndian.Uint32(hdr[4:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		if binary.LittleEndian.Uint32(hdr[:4]) == 0 { // handshake
			_ = writeFrame(conn, 1, map[string]any{"cmd": "DISPATCH", "evt": "READY", "data": map[string]any{"v": 1}})
			continue
		}
		var cmd struct {
			Args struct {
				Activity map[string]any `json:"activity"`
			} `json:"args"`
		}
		_ = json.Unmarshal(body, &cmd)
		if _, ok := cmd.Args.Activity["status_display_type"]; ok && d.oldVersion {
			_ = writeFrame(conn, 1, map[string]any{"cmd": "SET_ACTIVITY", "evt": "ERROR", "data": map[string]any{"code": 4000, "message": `child "activity" fails because ["status_display_type" is not allowed]`}})
			continue
		}
		d.mu.Lock()
		d.acts = append(d.acts, cmd.Args.Activity)
		d.mu.Unlock()
		_ = writeFrame(conn, 1, map[string]any{"cmd": "SET_ACTIVITY", "evt": nil, "data": cmd.Args.Activity})
	}
}

// startFakeDiscord listens where the client looks first for Discord.
func startFakeDiscord(t *testing.T, d *fakeDiscord) {
	t.Helper()
	dir, err := os.MkdirTemp("", "kd") // short: socket paths are limited to ~108 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	ln, err := net.Listen("unix", filepath.Join(dir, "discord-ipc-0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(conn)
		}
	}()
}

func TestSetActivityShowsWatchingTheAnime(t *testing.T) {
	d := &fakeDiscord{}
	startFakeDiscord(t, d)
	c := &Client{}
	defer c.Clear()
	if err := c.SetActivity("123", Activity{Details: "Frieren", State: "Episode 3"}); err != nil {
		t.Fatal(err)
	}
	acts := d.activities()
	if len(acts) != 1 {
		t.Fatalf("Discord got %d activities, want 1", len(acts))
	}
	got := acts[0]
	// JSON numbers decode as float64.
	if got["type"] != float64(typeWatching) || got["status_display_type"] != float64(statusShowsDetails) {
		t.Errorf("type %v, status_display_type %v: want Watching (3), with the details in the status (2)", got["type"], got["status_display_type"])
	}
	if got["details"] != "Frieren" || got["state"] != "Episode 3" {
		t.Errorf("details %q, state %q", got["details"], got["state"])
	}
}

func TestSetActivityOnAnOldDiscord(t *testing.T) {
	d := &fakeDiscord{oldVersion: true}
	startFakeDiscord(t, d)
	c := &Client{}
	defer c.Clear()
	for range 2 {
		if err := c.SetActivity("123", Activity{Details: "Frieren", State: "Episode 3"}); err != nil {
			t.Fatal(err)
		}
	}
	acts := d.activities()
	if len(acts) != 2 {
		t.Fatalf("Discord got %d activities, want 2", len(acts))
	}
	for _, a := range acts {
		if a["type"] != float64(typeWatching) || a["details"] != "Frieren" {
			t.Errorf("got %v", a)
		}
	}
	if !c.noStatusDisplay {
		t.Error("the client keeps sending status_display_type to a Discord that refuses it")
	}
}
