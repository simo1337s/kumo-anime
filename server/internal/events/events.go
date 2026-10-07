// Package events is a tiny pub/sub hub that pushes server events to the UI
// over Server-Sent Events.
package events

import (
	"encoding/json"
	"sync"
)

// Event types sent to the frontend.
const (
	Toast            = "toast"
	ScanProgress     = "scan-progress"
	ScanDone         = "scan-done"
	LibraryUpdated   = "library-updated"
	CollectionUpdate = "collection-updated"
	PlaybackStatus   = "playback-status"
	PlaybackEnded    = "playback-ended"
	DownloadProgress = "download-progress"
	TorrentCount     = "torrent-count"
	ExtensionsUpdate = "extensions-updated"
	PluginUI         = "plugin-ui"
	SettingsUpdated  = "settings-updated"
	UpdateStatus     = "update-status"
)

type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

type Hub struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: map[chan []byte]struct{}{}}
}

// Subscribe returns a channel of encoded events and an unsubscribe func.
func (h *Hub) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.clients[ch]; ok {
			delete(h.clients, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Publish broadcasts an event. Slow clients drop events instead of blocking.
func (h *Hub) Publish(typ string, payload any) {
	raw, err := json.Marshal(Event{Type: typ, Payload: payload})
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- raw:
		default:
		}
	}
}

// ToastPayload is the payload of a Toast event.
type ToastPayload struct {
	Level   string `json:"level"` // info | success | warning | error
	Message string `json:"message"`
}

func (h *Hub) Info(msg string)    { h.Publish(Toast, ToastPayload{"info", msg}) }
func (h *Hub) Success(msg string) { h.Publish(Toast, ToastPayload{"success", msg}) }
func (h *Hub) Warn(msg string)    { h.Publish(Toast, ToastPayload{"warning", msg}) }
func (h *Hub) Error(msg string)   { h.Publish(Toast, ToastPayload{"error", msg}) }
