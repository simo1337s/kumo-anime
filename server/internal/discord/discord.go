// Package discord shows "Watching …" rich presence through the local Discord
// client's IPC socket (no network access needed).
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
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Client struct {
	mu       sync.Mutex
	conn     net.Conn
	clientID string
}

func socketPaths() []string {
	var bases []string
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		bases = append(bases, d, filepath.Join(d, "app", "com.discordapp.Discord"), filepath.Join(d, ".flatpak", "dev.vencord.Vesktop", "xdg-run"), filepath.Join(d, "snap.discord"))
	}
	bases = append(bases, os.TempDir(), "/tmp")
	var out []string
	for _, b := range bases {
		for i := 0; i < 10; i++ {
			out = append(out, filepath.Join(b, fmt.Sprintf("discord-ipc-%d", i)))
		}
	}
	return out
}

func (c *Client) send(op uint32, payload any) error {
	raw, _ := json.Marshal(payload)
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, op)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(raw)))
	buf.Write(raw)
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.conn.Write(buf.Bytes()); err != nil {
		return err
	}
	// read reply frame
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var hdr [8]byte
	if _, err := io.ReadFull(c.conn, hdr[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(hdr[4:])
	_, err := io.CopyN(io.Discard, c.conn, int64(n))
	return err
}

func (c *Client) connect(clientID string) error {
	if c.conn != nil && c.clientID == clientID {
		return nil
	}
	c.closeLocked()
	for _, p := range socketPaths() {
		conn, err := net.DialTimeout("unix", p, time.Second)
		if err != nil {
			continue
		}
		c.conn, c.clientID = conn, clientID
		if err := c.send(0, map[string]any{"v": 1, "client_id": clientID}); err != nil {
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
	act := map[string]any{"details": trim(a.Details), "state": trim(a.State), "type": 3}
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
	err := c.send(1, map[string]any{"cmd": "SET_ACTIVITY", "args": map[string]any{"pid": os.Getpid(), "activity": act}, "nonce": uuid.NewString()})
	if err != nil {
		c.closeLocked()
	}
	return err
}

func (c *Client) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return
	}
	if err := c.send(1, map[string]any{"cmd": "SET_ACTIVITY", "args": map[string]any{"pid": os.Getpid()}, "nonce": uuid.NewString()}); err != nil {
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
