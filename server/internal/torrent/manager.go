package torrent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/util"
)

// Manager wires the configured client and the available search providers.
type Manager struct {
	settings *config.Store
	hub      *events.Hub

	mu        sync.RWMutex
	providers map[string]Provider
	// ExtraProviders returns providers contributed by extensions.
	ExtraProviders func() []Provider

	clientMu  sync.Mutex
	client    Client
	clientKey string

	lastCount int
}

func NewManager(s *config.Store, hub *events.Hub) *Manager {
	m := &Manager{settings: s, hub: hub, providers: map[string]Provider{}, lastCount: -1}
	m.Register(&Nyaa{})
	m.Register(&AnimeTosho{})
	return m
}

func (m *Manager) Register(p Provider) {
	m.mu.Lock()
	m.providers[p.ID()] = p
	m.mu.Unlock()
}

type ProviderInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Extension bool   `json:"extension"`
}

func (m *Manager) Providers() []ProviderInfo {
	m.mu.RLock()
	out := []ProviderInfo{}
	for _, id := range []string{"nyaa", "animetosho"} {
		if p, ok := m.providers[id]; ok {
			out = append(out, ProviderInfo{ID: p.ID(), Name: p.Name()})
		}
	}
	m.mu.RUnlock()
	if m.ExtraProviders != nil {
		for _, p := range m.ExtraProviders() {
			out = append(out, ProviderInfo{ID: p.ID(), Name: p.Name(), Extension: true})
		}
	}
	return out
}

func (m *Manager) Provider(id string) (Provider, error) {
	if id == "" {
		id = m.settings.Get().Torrent.DefaultProvider
	}
	m.mu.RLock()
	p, ok := m.providers[id]
	m.mu.RUnlock()
	if ok {
		return p, nil
	}
	if m.ExtraProviders != nil {
		for _, p := range m.ExtraProviders() {
			if p.ID() == id {
				return p, nil
			}
		}
	}
	if id != "nyaa" {
		return m.Provider("nyaa")
	}
	return nil, fmt.Errorf("unknown torrent provider %q", id)
}

// Client returns the configured torrent client (cached per settings).
func (m *Manager) Client() (Client, error) {
	cfg := m.settings.Get()
	var key string
	var c Client
	switch cfg.Torrent.DefaultClient {
	case "qbittorrent":
		key = fmt.Sprintf("qb|%+v", cfg.Qbittorrent)
	case "transmission":
		key = fmt.Sprintf("tr|%+v", cfg.Transmission)
	default:
		return nil, ErrNoClient
	}
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.client != nil && m.clientKey == key {
		return m.client, nil
	}
	if cfg.Torrent.DefaultClient == "qbittorrent" {
		c = NewQbittorrent(cfg.Qbittorrent)
	} else {
		c = NewTransmission(cfg.Transmission)
	}
	m.client, m.clientKey = c, key
	return c, nil
}

// Status reports whether the client is reachable.
type Status struct {
	Client    string `json:"client"`
	Connected bool   `json:"connected"`
	Version   string `json:"version"`
	Error     string `json:"error,omitempty"`
	// NeedsAuth: the client runs but refuses the configured credentials.
	NeedsAuth bool `json:"needsAuth,omitempty"`
}

func (m *Manager) Status(ctx context.Context) Status {
	c, err := m.Client()
	if err != nil {
		return Status{Client: "none", Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, err := c.Version(ctx)
	st := Status{Client: c.Name(), Version: v, Connected: err == nil}
	if err != nil {
		st.Error = err.Error()
		st.NeedsAuth = IsAuthError(err)
	}
	return st
}

// StartClient launches the client executable if it isn't reachable, then
// waits for it to come up.
func (m *Manager) StartClient(ctx context.Context) error {
	if st := m.Status(ctx); st.Connected {
		return nil
	} else if st.NeedsAuth {
		// It's running; launching it again would only bring its window up.
		return errors.New(st.Error)
	}
	cfg := m.settings.Get()
	exe := cfg.Qbittorrent.Executable
	if cfg.Torrent.DefaultClient == "transmission" {
		exe = cfg.Transmission.Executable
	}
	if exe == "" {
		return errors.New("no executable path configured for the torrent client")
	}
	parts := strings.Fields(exe)
	if _, ok := util.LookPath(parts[0]); !ok {
		return fmt.Errorf("torrent client executable not found: %s", parts[0])
	}
	if err := util.Detach(parts[0], parts[1:]...); err != nil {
		return err
	}
	// Flatpak apps can take a while on their first start.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if st := m.Status(ctx); st.Connected {
			return nil
		} else if st.NeedsAuth {
			return errors.New(st.Error)
		}
	}
	return errors.New("the torrent client started but its Web UI/RPC is not reachable — check host/port/credentials")
}

// SavePathFor returns where a torrent for an anime should be saved.
func (m *Manager) SavePathFor(title string) string {
	cfg := m.settings.Get()
	dir := config.ExpandHome(cfg.Library.Dir)
	if dir == "" {
		return ""
	}
	if cfg.Torrent.CreateSubfolder && title != "" {
		return filepath.Join(dir, util.SanitizeFilename(title))
	}
	return dir
}

// Add sends magnets/URLs to the client, starting it if needed.
func (m *Manager) Add(ctx context.Context, uris []string, savePath string) error {
	c, err := m.Client()
	if err != nil {
		return err
	}
	if st := m.Status(ctx); !st.Connected {
		if err := m.StartClient(ctx); err != nil {
			return err
		}
	}
	return c.Add(ctx, uris, savePath)
}

// RunCounter publishes the number of active torrents for the sidebar badge.
func (m *Manager) RunCounter(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg := m.settings.Get()
		if !cfg.Torrent.ShowActiveCount || cfg.Torrent.DefaultClient == "none" || cfg.Torrent.DefaultClient == "" {
			continue
		}
		c, err := m.Client()
		if err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		list, err := c.List(cctx)
		cancel()
		count := 0
		if err == nil {
			for _, t := range list {
				if t.State == "downloading" || t.State == "stalled" || t.State == "metadata" || t.State == "queued" {
					count++
				}
			}
		}
		if count != m.lastCount {
			m.lastCount = count
			m.hub.Publish(events.TorrentCount, count)
		}
	}
}
