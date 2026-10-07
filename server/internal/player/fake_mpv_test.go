package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/util"
)

// shortTempDir returns a test directory whose path leaves room for unix
// socket names (paths are limited to ~108 bytes).
func shortTempDir(t *testing.T) string {
	t.Helper()
	if dir := t.TempDir(); len(dir) < 80 {
		return dir
	}
	dir, err := os.MkdirTemp("", "km")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// fakeMpv is the mpv end of an IPC connection: it answers commands like mpv
// does and lets the test send events. exit simulates the process ending.
type fakeMpv struct {
	conn   net.Conn
	tracks []Track
	onExit func()

	wmu sync.Mutex // serializes writes

	mu   sync.Mutex
	cmds []string // received commands, e.g. "set_property sid"
	args [][]any  // the rest of each command, e.g. the value set

	exitOnce sync.Once
	exited   chan struct{}
}

// startFakeMpv serves a fake mpv on an IPC socket (named pipe on Windows) at
// path and returns an Mpv connected to it. It may run on any goroutine.
func startFakeMpv(path string, tracks []Track, onExit func()) (*Mpv, *fakeMpv, error) {
	ln, err := listenIPC(path)
	if err != nil {
		return nil, nil, err
	}
	defer ln.Close()
	// Accept while dialing: a named pipe listener only lets a client in once
	// Accept runs (until then the pipe is "busy").
	type accepted struct {
		conn net.Conn
		err  error
	}
	acc := make(chan accepted, 1)
	go func() {
		conn, err := ln.Accept()
		acc <- accepted{conn, err}
	}()
	client, err := util.DialIPC(path, 5*time.Second)
	if err != nil {
		_ = ln.Close() // ends the Accept
		if a := <-acc; a.conn != nil {
			_ = a.conn.Close()
		}
		return nil, nil, err
	}
	a := <-acc
	if a.err != nil {
		_ = client.Close()
		return nil, nil, a.err
	}
	f := &fakeMpv{conn: a.conn, tracks: tracks, onExit: onExit, exited: make(chan struct{})}
	go f.serve()
	return newMpv(client, path, f.exit), f, nil
}

func (f *fakeMpv) serve() {
	defer f.exit()
	sc := bufio.NewScanner(f.conn)
	for sc.Scan() {
		var req struct {
			Command   []any `json:"command"`
			RequestID int64 `json:"request_id"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.Command) == 0 {
			continue
		}
		name, rest := fmt.Sprint(req.Command[0]), req.Command[1:]
		if len(req.Command) > 1 && (name == "set_property" || name == "get_property") {
			name += " " + fmt.Sprint(req.Command[1])
			rest = req.Command[2:]
		}
		f.mu.Lock()
		f.cmds = append(f.cmds, name)
		f.args = append(f.args, rest)
		f.mu.Unlock()

		reply := map[string]any{"request_id": req.RequestID, "error": "success"}
		switch name {
		case "get_property track-list":
			reply["data"] = trackListJSON(f.tracks)
		case "quit":
			f.send(reply)
			f.exit()
			return
		}
		f.send(reply)
		// Like mpv, report the change of an observed property.
		if (name == "set_property aid" || name == "set_property sid") && len(req.Command) > 2 {
			v := req.Command[2]
			if v == "no" {
				v = false
			}
			f.prop(fmt.Sprint(req.Command[1]), v)
		}
	}
}

func trackListJSON(tracks []Track) []map[string]any {
	out := []map[string]any{}
	for _, t := range tracks {
		out = append(out, map[string]any{"id": t.ID, "type": t.Type, "lang": t.Lang, "title": t.Title})
	}
	return out
}

func (f *fakeMpv) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_, _ = f.conn.Write(append(b, '\n'))
}

// event sends an mpv event, e.g. event("end-file", "reason", "eof").
func (f *fakeMpv) event(name string, kv ...any) {
	ev := map[string]any{"event": name}
	for i := 0; i+1 < len(kv); i += 2 {
		ev[kv[i].(string)] = kv[i+1]
	}
	f.send(ev)
}

// prop sends a property-change; nil means the property is unavailable (mpv
// then sends no data).
func (f *fakeMpv) prop(name string, v any) {
	if v == nil {
		f.event("property-change", "name", name)
		return
	}
	if tracks, ok := v.([]Track); ok {
		v = trackListJSON(tracks)
	}
	f.event("property-change", "name", name, "data", v)
}

// exit simulates the mpv process ending.
func (f *fakeMpv) exit() {
	f.exitOnce.Do(func() {
		if f.onExit != nil {
			f.onExit()
		}
		_ = f.conn.Close()
		close(f.exited)
	})
}

func (f *fakeMpv) received(cmd string) bool { return f.count(cmd) > 0 }

// sent returns the rest of each cmd received, in order: e.g. for
// "set_property time-pos", the positions.
func (f *fakeMpv) sent(cmd string) [][]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]any
	for i, c := range f.cmds {
		if c == cmd {
			out = append(out, f.args[i])
		}
	}
	return out
}

func (f *fakeMpv) count(cmd string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.cmds {
		if c == cmd {
			n++
		}
	}
	return n
}

// fakeLauncher replaces LaunchMpv: every launch starts a fakeMpv. It counts
// how many are running at once.
type fakeLauncher struct {
	dir    string
	tracks []Track
	delay  time.Duration // how long a launch takes

	mu       sync.Mutex
	launched []*fakeMpv
	targets  []string
	live     int
	maxLive  int
}

func newFakeLauncher(t *testing.T) *fakeLauncher {
	l := &fakeLauncher{dir: shortTempDir(t), tracks: testTracks}
	t.Cleanup(func() {
		l.mu.Lock()
		all := slices.Clone(l.launched)
		l.mu.Unlock()
		for _, f := range all {
			f.exit()
		}
	})
	return l
}

func (l *fakeLauncher) launch(_ string, opts LaunchOptions) (*Mpv, error) {
	time.Sleep(l.delay)
	l.mu.Lock()
	l.live++
	l.maxLive = max(l.maxLive, l.live)
	l.targets = append(l.targets, opts.Target)
	path := testIPCAddress(l.dir, fmt.Sprint(len(l.targets)))
	l.mu.Unlock()
	onExit := func() {
		l.mu.Lock()
		l.live--
		l.mu.Unlock()
	}
	mpv, f, err := startFakeMpv(path, l.tracks, onExit)
	if err != nil {
		onExit()
		return nil, err
	}
	l.mu.Lock()
	l.launched = append(l.launched, f)
	l.mu.Unlock()
	return mpv, nil
}

func (l *fakeLauncher) last() *fakeMpv {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.launched[len(l.launched)-1]
}

func (l *fakeLauncher) stats() (launched, live, maxLive int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.targets), l.live, l.maxLive
}

// waitFor polls cond until it holds, failing the test after a few seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
