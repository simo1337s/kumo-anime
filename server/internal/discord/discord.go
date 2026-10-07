// Package discord shows "Watching <anime>" rich presence through the local
// Discord client's IPC socket or named pipe (no network access needed).
package discord

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/simo1337s/animetest/server/internal/util"
)

const (
	// The activity type: "Watching". (Discord's purple status is its
	// "Streaming" type, which it keeps for Twitch and YouTube: rich
	// presence can't set it.)
	typeWatching = 3
	// status_display_type: the status under your name (member list, DMs)
	// shows the activity's details, the anime ("Watching Frieren"), rather
	// than the Discord application's name ("Watching Kumo").
	statusShowsDetails = 2
)

type Client struct {
	mu       sync.Mutex
	conn     net.Conn
	clientID string
	// This Discord refused status_display_type (an old version): activities
	// go without it until the next connection.
	noStatusDisplay bool
}

// frame is what Discord answers a command with.
type frame struct {
	op   uint32
	Evt  string `json:"evt"` // "ERROR" when the command failed
	Data struct {
		Message string `json:"message"`
	} `json:"data"`
	Message string `json:"message"` // why Discord closes the connection (op 2)
}

// err is the error Discord answered with, if any.
func (f *frame) err() error {
	switch {
	case f.op == 2:
		return fmt.Errorf("Discord closed the connection: %s", f.Message)
	case f.Evt == "ERROR":
		return fmt.Errorf("Discord: %s", f.Data.Message)
	}
	return nil
}

func (c *Client) send(op uint32, payload any) (*frame, error) {
	raw, _ := json.Marshal(payload)
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, op)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(raw)))
	buf.Write(raw)
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.conn.Write(buf.Bytes()); err != nil {
		return nil, err
	}
	// read reply frame
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var hdr [8]byte
	if _, err := io.ReadFull(c.conn, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(hdr[4:])
	if n > 1<<20 {
		return nil, errors.New("Discord sent an oversized frame")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return nil, err
	}
	f := &frame{op: binary.LittleEndian.Uint32(hdr[:4])}
	_ = json.Unmarshal(body, f)
	return f, nil
}

func (c *Client) connect(clientID string) error {
	if c.conn != nil && c.clientID == clientID {
		return nil
	}
	c.closeLocked()
	for _, p := range socketPaths() {
		conn, err := util.DialIPC(p, time.Second)
		if err != nil {
			continue
		}
		c.conn, c.clientID, c.noStatusDisplay = conn, clientID, false
		if _, err := c.send(0, map[string]any{"v": 1, "client_id": clientID}); err != nil {
			c.closeLocked()
			continue
		}
		return nil
	}
	return errors.New("Discord is not running")
}

func (c *Client) closeLocked() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

type Activity struct {
	Details    string
	State      string
	LargeImage string
	LargeText  string
	Start      time.Time
	End        time.Time
}

// SetActivity updates the presence (clientID is a Discord application id).
func (c *Client) SetActivity(clientID string, a Activity) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if clientID == "" {
		return errors.New("no Discord application id configured")
	}
	if err := c.connect(clientID); err != nil {
		return err
	}
	act := map[string]any{"details": trim(a.Details), "state": trim(a.State), "type": typeWatching}
	if !c.noStatusDisplay {
		act["status_display_type"] = statusShowsDetails
	}
	assets := map[string]any{}
	if a.LargeImage != "" {
		assets["large_image"] = a.LargeImage
		assets["large_text"] = trim(a.LargeText)
	}
	act["assets"] = assets
	ts := map[string]any{}
	if !a.Start.IsZero() {
		ts["start"] = a.Start.UnixMilli()
	}
	if !a.End.IsZero() {
		ts["end"] = a.End.UnixMilli()
	}
	if len(ts) > 0 {
		act["timestamps"] = ts
	}
	f, err := c.setActivity(act)
	if err == nil && f.err() != nil && act["status_display_type"] != nil {
		// Too old a Discord for status_display_type may refuse it: without
		// it, the status shows the application's name.
		delete(act, "status_display_type")
		if f, err = c.setActivity(act); err == nil && f.err() == nil {
			c.noStatusDisplay = true
		}
	}
	if err != nil {
		c.closeLocked()
		return err
	}
	return f.err()
}

func (c *Client) setActivity(act map[string]any) (*frame, error) {
	return c.send(1, map[string]any{"cmd": "SET_ACTIVITY", "args": map[string]any{"pid": os.Getpid(), "activity": act}, "nonce": uuid.NewString()})
}

func (c *Client) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return
	}
	if _, err := c.send(1, map[string]any{"cmd": "SET_ACTIVITY", "args": map[string]any{"pid": os.Getpid()}, "nonce": uuid.NewString()}); err != nil {
		c.closeLocked()
	}
}

func trim(s string) string {
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120])
	}
	if len(r) == 1 {
		return s + " "
	}
	return s
}
