package player

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
)

// Mpv controls one mpv process through its JSON IPC socket.
type Mpv struct {
	cmd    *exec.Cmd
	conn   net.Conn
	socket string

	writeMu sync.Mutex
	reqID   atomic.Int64
	pending sync.Map // request id -> chan ipcResponse

	Events chan MpvEvent
	done   chan struct{}
	once   sync.Once
}

type MpvEvent struct {
	Event  string          `json:"event"`
	Name   string          `json:"name,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	Reason string          `json:"reason,omitempty"`
	ID     int             `json:"id,omitempty"`
}

type ipcResponse struct {
	RequestID int64           `json:"request_id"`
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	Event     string          `json:"event"`
}

type LaunchOptions struct {
	Target     string            // file path or URL
	Title      string            // window/media title
	Start      float64           // resume position in seconds
	Headers    map[string]string // HTTP headers for streams
	Referrer   string
	SubFiles   []string
	AudioFiles []string
	AudioLang  string
	SubLang    string
	Fullscreen bool
	ExtraArgs  []string
}

// LaunchMpv starts mpv and connects to its IPC socket.
func LaunchMpv(mpvPath string, opts LaunchOptions) (*Mpv, error) {
	socket := filepath.Join(config.RuntimeDir(), fmt.Sprintf("mpv-%d.sock", time.Now().UnixNano()%1e9))
	args := []string{
		"--input-ipc-server=" + socket,
		"--force-window=immediate",
		"--keep-open=no",
		"--idle=no",
		"--term-playing-msg=",
	}
	if opts.Title != "" {
		args = append(args, "--force-media-title="+opts.Title, "--title="+opts.Title)
	}
	if opts.Start > 0 {
		args = append(args, fmt.Sprintf("--start=%.2f", opts.Start))
	}
	if opts.Referrer != "" {
		args = append(args, "--referrer="+opts.Referrer)
	}
	if len(opts.Headers) > 0 {
		var fields []string
		for k, v := range opts.Headers {
			if strings.EqualFold(k, "referer") && opts.Referrer == "" {
				args = append(args, "--referrer="+v)
				continue
			}
			if strings.EqualFold(k, "user-agent") {
				args = append(args, "--user-agent="+v)
				continue
			}
			// mpv splits this list on commas.
			fields = append(fields, strings.ReplaceAll(k+": "+v, ",", `\,`))
		}
		if len(fields) > 0 {
			args = append(args, "--http-header-fields="+strings.Join(fields, ","))
		}
	}
	for _, s := range opts.SubFiles {
		if s != "" {
			args = append(args, "--sub-file="+s)
		}
	}
	for _, a := range opts.AudioFiles {
		if a != "" {
			args = append(args, "--audio-file="+a)
		}
	}
	if opts.AudioLang != "" {
		args = append(args, "--alang="+opts.AudioLang)
	}
	if opts.SubLang != "" {
		args = append(args, "--slang="+opts.SubLang)
	}
	if opts.Fullscreen {
		args = append(args, "--fs")
	}
	args = append(args, opts.ExtraArgs...)
	args = append(args, "--", opts.Target)

	cmd := exec.Command(mpvPath, args...)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start mpv (%s): %w — install it with `sudo pacman -S mpv` or set the path in Settings", mpvPath, err)
	}
	m := &Mpv{cmd: cmd, socket: socket, Events: make(chan MpvEvent, 128), done: make(chan struct{})}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	// Wait for the socket to appear.
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			m.conn = conn
			break
		}
		select {
		case err := <-exited:
			_ = os.Remove(socket)
			return nil, fmt.Errorf("mpv exited immediately: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return nil, errors.New("timed out connecting to mpv")
		}
		time.Sleep(100 * time.Millisecond)
	}

	go m.readLoop()
	go func() {
		<-exited
		m.close()
	}()
	return m, nil
}

func (m *Mpv) readLoop() {
	sc := bufio.NewScanner(m.conn)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var r ipcResponse
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		if r.Event == "" && r.RequestID != 0 {
			if ch, ok := m.pending.LoadAndDelete(r.RequestID); ok {
				ch.(chan ipcResponse) <- r
			}
			continue
		}
		var ev MpvEvent
		if json.Unmarshal(line, &ev) == nil && ev.Event != "" {
			select {
			case m.Events <- ev:
			case <-m.done:
				return
			case <-time.After(2 * time.Second):
				// Consumer stuck; drop the event rather than block mpv.
			}
		}
	}
	m.close()
}

func (m *Mpv) close() {
	m.once.Do(func() {
		if m.conn != nil {
			_ = m.conn.Close()
		}
		_ = os.Remove(m.socket)
		// Events is intentionally never closed: readers select on Done().
		close(m.done)
	})
}

// Done is closed when mpv exits.
func (m *Mpv) Done() <-chan struct{} { return m.done }

// Command sends an IPC command and waits for the reply.
func (m *Mpv) Command(args ...any) (json.RawMessage, error) {
	select {
	case <-m.done:
		return nil, errors.New("mpv is not running")
	default:
	}
	id := m.reqID.Add(1)
	ch := make(chan ipcResponse, 1)
	m.pending.Store(id, ch)
	raw, _ := json.Marshal(map[string]any{"command": args, "request_id": id})
	m.writeMu.Lock()
	_ = m.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err := m.conn.Write(append(raw, '\n'))
	m.writeMu.Unlock()
	if err != nil {
		m.pending.Delete(id)
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Error != "success" {
			return nil, fmt.Errorf("mpv: %s", r.Error)
		}
		return r.Data, nil
	case <-time.After(5 * time.Second):
		m.pending.Delete(id)
		return nil, errors.New("mpv: command timed out")
	case <-m.done:
		return nil, errors.New("mpv exited")
	}
}

func (m *Mpv) Observe(id int, prop string) {
	_, _ = m.Command("observe_property", id, prop)
}

func (m *Mpv) Set(prop string, value any) error {
	_, err := m.Command("set_property", prop, value)
	return err
}

func (m *Mpv) GetFloat(prop string) float64 {
	raw, err := m.Command("get_property", prop)
	if err != nil {
		return 0
	}
	var f float64
	_ = json.Unmarshal(raw, &f)
	return f
}

func (m *Mpv) Tracks() []Track {
	raw, err := m.Command("get_property", "track-list")
	if err != nil {
		return nil
	}
	return parseTrackList(raw)
}

func parseTrackList(raw json.RawMessage) []Track {
	var list []struct {
		ID       int    `json:"id"`
		Type     string `json:"type"`
		Lang     string `json:"lang"`
		Title    string `json:"title"`
		Codec    string `json:"codec"`
		Default  bool   `json:"default"`
		Forced   bool   `json:"forced"`
		External bool   `json:"external"`
		Selected bool   `json:"selected"`
	}
	_ = json.Unmarshal(raw, &list)
	out := make([]Track, 0, len(list))
	for _, t := range list {
		out = append(out, Track{ID: t.ID, Type: t.Type, Lang: t.Lang, Title: t.Title, Codec: t.Codec, Default: t.Default, Forced: t.Forced, External: t.External, Selected: t.Selected})
	}
	return out
}

func (m *Mpv) ShowText(text string, ms int) {
	_, _ = m.Command("show-text", text, ms)
}

func (m *Mpv) Quit() {
	_, _ = m.Command("quit")
	select {
	case <-m.done:
	case <-time.After(3 * time.Second):
		if m.cmd.Process != nil {
			_ = m.cmd.Process.Kill()
		}
	}
}
