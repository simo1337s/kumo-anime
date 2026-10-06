package extensions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
	"github.com/simo1337s/animetest/server/internal/torrent"
	"github.com/simo1337s/animetest/server/internal/util"
)

// Loaded is an installed extension.
type Loaded struct {
	Manifest    *Manifest
	Enabled     bool
	UserConfig  SavedUserConfig
	InstalledAt int64
	UpdatedAt   int64

	mgr     *Manager
	payload string

	mu          sync.Mutex
	rt          *Runtime
	err         string
	configError string
}

// Runtime returns the running VM, starting it on first use.
func (l *Loaded) Runtime() (*Runtime, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rt != nil {
		return l.rt, nil
	}
	if !l.Enabled {
		return nil, fmt.Errorf("extension %s is disabled", l.Manifest.Name)
	}
	rt, err := l.mgr.start(l)
	if err != nil {
		l.err = err.Error()
		return nil, err
	}
	l.err = ""
	l.rt = rt
	return rt, nil
}

func (l *Loaded) stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rt != nil {
		l.rt.Close()
		l.rt = nil
	}
}

// Info is what the UI shows for an installed extension.
type Info struct {
	Manifest    *Manifest       `json:"manifest"`
	Enabled     bool            `json:"enabled"`
	UserConfig  SavedUserConfig `json:"userConfig"`
	Error       string          `json:"error,omitempty"`
	ConfigError string          `json:"configError,omitempty"`
	Running     bool            `json:"running"`
	InstalledAt int64           `json:"installedAt"`
	UpdatedAt   int64           `json:"updatedAt"`
	Supported   bool            `json:"supported"`
	Granted     bool            `json:"granted"`
	PluginUI    *PluginUIState  `json:"pluginUi,omitempty"`
}

type Manager struct {
	db       *db.DB
	settings *config.Store
	hub      *events.Hub
	fetcher  *Fetcher

	mu     sync.RWMutex
	exts   map[string]*Loaded
	stores map[string]*MemStore

	// Host services for plugins (wired by the app).
	Host PluginHostServices

	pluginMu sync.Mutex
	plugins  map[string]*PluginHost
}

func NewManager(d *db.DB, s *config.Store, hub *events.Hub) *Manager {
	return &Manager{
		db: d, settings: s, hub: hub, fetcher: NewFetcher(),
		exts: map[string]*Loaded{}, stores: map[string]*MemStore{}, plugins: map[string]*PluginHost{},
	}
}

func (m *Manager) store(id string) *MemStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stores[id]
	if !ok {
		s = NewMemStore()
		m.stores[id] = s
	}
	return s
}

// LoadAll reads installed extensions from the database and starts plugins.
func (m *Manager) LoadAll() {
	rows, err := m.db.Query(`SELECT manifest, payload, enabled, user_config, installed_at, updated_at FROM extensions`)
	if err != nil {
		log.Printf("extensions: %v", err)
		return
	}
	var loaded []*Loaded
	for rows.Next() {
		var manifest, payload, uc string
		var enabled int
		l := &Loaded{mgr: m}
		if rows.Scan(&manifest, &payload, &enabled, &uc, &l.InstalledAt, &l.UpdatedAt) != nil {
			continue
		}
		var man Manifest
		if json.Unmarshal([]byte(manifest), &man) != nil {
			continue
		}
		l.Manifest, l.payload, l.Enabled = &man, payload, enabled == 1
		_ = json.Unmarshal([]byte(uc), &l.UserConfig)
		loaded = append(loaded, l)
	}
	rows.Close()
	m.mu.Lock()
	for _, l := range loaded {
		m.exts[l.Manifest.ID] = l
	}
	m.mu.Unlock()
	for _, l := range loaded {
		if l.Manifest.Type == TypePlugin && l.Enabled && m.granted(l) {
			go m.startPlugin(l)
		}
	}
}

func (m *Manager) get(id string) (*Loaded, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.exts[id]
	return l, ok
}

func (m *Manager) info(l *Loaded) Info {
	l.mu.Lock()
	inf := Info{
		Manifest: l.Manifest, Enabled: l.Enabled, UserConfig: l.UserConfig, Error: l.err, ConfigError: l.configError,
		Running: l.rt != nil, InstalledAt: l.InstalledAt, UpdatedAt: l.UpdatedAt,
		Supported: l.Manifest.Type != TypeCustomSource,
	}
	l.mu.Unlock()
	if inf.UserConfig.Values == nil {
		inf.UserConfig.Values = map[string]string{}
	}
	if l.Manifest.Type == TypePlugin {
		inf.Granted = m.granted(l)
		m.pluginMu.Lock()
		if h := m.plugins[l.Manifest.ID]; h != nil {
			st := h.Snapshot()
			inf.PluginUI = &st
			inf.Running = true
			if h.err != "" {
				inf.Error = h.err
			}
		}
		m.pluginMu.Unlock()
	}
	return inf
}

func (m *Manager) List() []Info {
	m.mu.RLock()
	list := make([]*Loaded, 0, len(m.exts))
	for _, l := range m.exts {
		list = append(list, l)
	}
	m.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Manifest.Name) < strings.ToLower(list[j].Manifest.Name)
	})
	out := make([]Info, 0, len(list))
	for _, l := range list {
		out = append(out, m.info(l))
	}
	return out
}

func (m *Manager) Get(id string) (Info, error) {
	l, ok := m.get(id)
	if !ok {
		return Info{}, errors.New("extension not installed")
	}
	return m.info(l), nil
}

// start compiles and launches a provider runtime.
func (m *Manager) start(l *Loaded) (*Runtime, error) {
	payload, prefs, cfgErr := ApplyUserConfig(l.Manifest, l.payload, &l.UserConfig)
	if cfgErr != nil {
		l.configError = cfgErr.Error()
	} else {
		l.configError = ""
	}
	prog, err := Compile(l.Manifest, payload)
	if err != nil {
		return nil, err
	}
	return NewRuntime(l.Manifest, prog, RuntimeOptions{
		Prefs:   prefs,
		Store:   m.store(l.Manifest.ID),
		Storage: NewStorage(m.db, l.Manifest.ID),
		Fetcher: m.fetcher,
	})
}

func (m *Manager) persist(l *Loaded) error {
	man, _ := json.Marshal(l.Manifest)
	uc, _ := json.Marshal(l.UserConfig)
	now := time.Now().Unix()
	if l.InstalledAt == 0 {
		l.InstalledAt = now
	}
	l.UpdatedAt = now
	_, err := m.db.Write(`INSERT INTO extensions(id, manifest, payload, enabled, user_config, installed_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET manifest = excluded.manifest, payload = excluded.payload, enabled = excluded.enabled,
			user_config = excluded.user_config, updated_at = excluded.updated_at`,
		l.Manifest.ID, string(man), l.payload, b2i(l.Enabled), string(uc), l.InstalledAt, l.UpdatedAt)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FetchManifest downloads a manifest and its payload.
func (m *Manager) FetchManifest(ctx context.Context, manifestURI string) (*Manifest, string, error) {
	manifestURI = strings.TrimSpace(manifestURI)
	if !strings.HasPrefix(manifestURI, "https://") && !strings.HasPrefix(manifestURI, "http://") {
		return nil, "", errors.New("the manifest URL must start with https://")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := util.GetBytes(ctx, manifestURI, nil)
	if err != nil {
		return nil, "", fmt.Errorf("download manifest: %w", err)
	}
	var man Manifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return nil, "", fmt.Errorf("invalid manifest JSON: %w", err)
	}
	if err := man.Validate(); err != nil {
		return nil, "", err
	}
	if man.ManifestURI == "" {
		man.ManifestURI = manifestURI
	}
	payload := man.Payload
	if payload == "" && man.PayloadURI != "" {
		b, err := util.GetBytes(ctx, man.PayloadURI, nil)
		if err != nil {
			return nil, "", fmt.Errorf("download payload: %w", err)
		}
		payload = string(b)
	}
	if strings.TrimSpace(payload) == "" {
		return nil, "", errors.New("the extension has no code (payload)")
	}
	man.Payload = ""
	return &man, payload, nil
}

// Install installs (or reinstalls/updates) an extension from its manifest URL.
func (m *Manager) Install(ctx context.Context, manifestURI string) (Info, error) {
	man, payload, err := m.FetchManifest(ctx, manifestURI)
	if err != nil {
		return Info{}, err
	}
	// Make sure it compiles before saving.
	if _, err := Compile(man, payload); err != nil {
		return Info{}, fmt.Errorf("the extension code doesn't compile: %w", err)
	}
	old, exists := m.get(man.ID)
	l := &Loaded{mgr: m, Manifest: man, payload: payload, Enabled: man.Type != TypePlugin}
	if exists {
		old.stop()
		m.stopPlugin(man.ID)
		l.Enabled, l.UserConfig, l.InstalledAt = old.Enabled, old.UserConfig, old.InstalledAt
	}
	if err := m.persist(l); err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	m.exts[man.ID] = l
	m.mu.Unlock()
	if man.Type == TypePlugin && l.Enabled && m.granted(l) {
		go m.startPlugin(l)
	}
	m.hub.Publish(events.ExtensionsUpdate, nil)
	return m.info(l), nil
}

func (m *Manager) Uninstall(id string) error {
	l, ok := m.get(id)
	if !ok {
		return errors.New("extension not installed")
	}
	l.stop()
	m.stopPlugin(id)
	m.mu.Lock()
	delete(m.exts, id)
	delete(m.stores, id)
	m.mu.Unlock()
	if _, err := m.db.Write(`DELETE FROM extensions WHERE id = ?`, id); err != nil {
		return err
	}
	_, _ = m.db.Write(`DELETE FROM extension_storage WHERE ext_id = ?`, id)
	_ = m.db.DeleteKV("ext_grant:" + id)
	m.hub.Publish(events.ExtensionsUpdate, nil)
	return nil
}

func (m *Manager) SetEnabled(id string, enabled bool) error {
	l, ok := m.get(id)
	if !ok {
		return errors.New("extension not installed")
	}
	l.mu.Lock()
	l.Enabled = enabled
	l.mu.Unlock()
	if !enabled {
		l.stop()
		m.stopPlugin(id)
	}
	if err := m.persist(l); err != nil {
		return err
	}
	if enabled && l.Manifest.Type == TypePlugin && m.granted(l) {
		go m.startPlugin(l)
	}
	m.hub.Publish(events.ExtensionsUpdate, nil)
	return nil
}

func (m *Manager) SaveUserConfig(id string, values map[string]string) error {
	l, ok := m.get(id)
	if !ok {
		return errors.New("extension not installed")
	}
	version := 0
	if l.Manifest.UserConfig != nil {
		version = l.Manifest.UserConfig.Version
	}
	l.mu.Lock()
	l.UserConfig = SavedUserConfig{Version: version, Values: values}
	l.mu.Unlock()
	l.stop()
	if err := m.persist(l); err != nil {
		return err
	}
	if l.Manifest.Type == TypePlugin {
		m.stopPlugin(id)
		if l.Enabled && m.granted(l) {
			go m.startPlugin(l)
		}
	}
	m.hub.Publish(events.ExtensionsUpdate, nil)
	return nil
}

// Logs returns the console output of an extension.
func (m *Manager) Logs(id string) []LogLine {
	m.pluginMu.Lock()
	if h := m.plugins[id]; h != nil && h.rt != nil {
		m.pluginMu.Unlock()
		return h.rt.Logs()
	}
	m.pluginMu.Unlock()
	l, ok := m.get(id)
	if !ok {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rt == nil {
		return nil
	}
	return l.rt.Logs()
}

// ---------------------------------------------------------------------------
// Plugin permissions

func permissionHash(man *Manifest) string {
	if man.Plugin == nil {
		return ""
	}
	raw, _ := json.Marshal(man.Plugin.Permissions)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) granted(l *Loaded) bool {
	var h string
	ok, _ := m.db.GetKV("ext_grant:"+l.Manifest.ID, &h)
	return ok && h == permissionHash(l.Manifest)
}

// Grant records that the user accepted a plugin's permissions and starts it.
func (m *Manager) Grant(id string) error {
	l, ok := m.get(id)
	if !ok {
		return errors.New("extension not installed")
	}
	if err := m.db.SetKV("ext_grant:"+id, permissionHash(l.Manifest)); err != nil {
		return err
	}
	l.mu.Lock()
	l.Enabled = true
	l.mu.Unlock()
	if err := m.persist(l); err != nil {
		return err
	}
	m.stopPlugin(id)
	go m.startPlugin(l)
	m.hub.Publish(events.ExtensionsUpdate, nil)
	return nil
}

// ---------------------------------------------------------------------------
// Updates & marketplace

type UpdateInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
}

func (m *Manager) CheckUpdates(ctx context.Context) []UpdateInfo {
	m.mu.RLock()
	var list []*Loaded
	for _, l := range m.exts {
		list = append(list, l)
	}
	m.mu.RUnlock()
	out := []UpdateInfo{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, l := range list {
		uri := l.Manifest.ManifestURI
		if !strings.HasPrefix(uri, "http") {
			continue
		}
		wg.Add(1)
		go func(l *Loaded) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			raw, err := util.GetBytes(cctx, uri, nil)
			if err != nil {
				return
			}
			var man Manifest
			if json.Unmarshal(raw, &man) != nil || man.Version == "" {
				return
			}
			if man.Version != l.Manifest.Version {
				mu.Lock()
				out = append(out, UpdateInfo{ID: l.Manifest.ID, Name: l.Manifest.Name, CurrentVersion: l.Manifest.Version, LatestVersion: man.Version})
				mu.Unlock()
			}
		}(l)
	}
	wg.Wait()
	return out
}

func (m *Manager) Update(ctx context.Context, id string) (Info, error) {
	l, ok := m.get(id)
	if !ok {
		return Info{}, errors.New("extension not installed")
	}
	return m.Install(ctx, l.Manifest.ManifestURI)
}

// MarketEntry is one entry of the marketplace index.
type MarketEntry struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Author        string `json:"author"`
	Description   string `json:"description"`
	Type          string `json:"type"`
	Language      string `json:"language"`
	Lang          string `json:"lang"`
	Icon          string `json:"icon"`
	ManifestURI   string `json:"manifestURI"`
	PayloadURI    string `json:"payloadURI"`
	Website       string `json:"website"`
	Flags         string `json:"flags"`
	Permalink     string `json:"permalink"`
	WorkingTag    bool   `json:"workingTag"`
	BrokenTag     bool   `json:"brokenTag"`
	DeprecatedTag bool   `json:"deprecatedTag"`
	Official      bool   `json:"official"`
	Stars         int    `json:"stars"`
	UpdatedAt     string `json:"updatedAt"`
	AddedAt       string `json:"addedAt"`

	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	HasUpdate        bool   `json:"hasUpdate"`
	Supported        bool   `json:"supported"`
}

// Marketplace downloads the marketplace index (cached for an hour).
func (m *Manager) Marketplace(ctx context.Context, url string, refresh bool) ([]MarketEntry, error) {
	if url == "" {
		url = m.settings.Get().Extensions.MarketplaceURL
	}
	key := "marketplace:" + url
	var entries []MarketEntry
	if refresh || !m.db.GetCache(key, &entries) {
		raw, err := util.GetBytes(ctx, url, nil)
		if err != nil {
			if !m.db.GetStaleCache(key, &entries) {
				return nil, fmt.Errorf("could not download the marketplace: %w", err)
			}
		} else {
			entries = nil
			if err := json.Unmarshal(raw, &entries); err != nil {
				// Some repositories wrap the list.
				var wrapped struct {
					Extensions []MarketEntry `json:"extensions"`
				}
				if err2 := json.Unmarshal(raw, &wrapped); err2 != nil || len(wrapped.Extensions) == 0 {
					return nil, fmt.Errorf("the marketplace file is not a valid extension list: %w", err)
				}
				entries = wrapped.Extensions
			}
			m.db.SetCache(key, entries, time.Hour)
		}
	}
	m.mu.RLock()
	for i := range entries {
		e := &entries[i]
		e.Supported = e.Type != TypeCustomSource
		if l, ok := m.exts[e.ID]; ok {
			e.Installed = true
			e.InstalledVersion = l.Manifest.Version
			e.HasUpdate = e.Version != "" && e.Version != l.Manifest.Version
		}
	}
	m.mu.RUnlock()
	return entries, nil
}

// ---------------------------------------------------------------------------
// Provider accessors

func (m *Manager) byType(t string) []*Loaded {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Loaded
	for _, l := range m.exts {
		if l.Manifest.Type == t && l.Enabled {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	return out
}

func (m *Manager) TorrentProviders() []torrent.Provider {
	var out []torrent.Provider
	for _, l := range m.byType(TypeTorrent) {
		out = append(out, &TorrentProvider{ext: l})
	}
	return out
}

func (m *Manager) OnlineStreamProviders() []*OnlineStreamProvider {
	var out []*OnlineStreamProvider
	for _, l := range m.byType(TypeOnlineStream) {
		out = append(out, &OnlineStreamProvider{ext: l})
	}
	return out
}

func (m *Manager) OnlineStreamProvider(id string) (*OnlineStreamProvider, error) {
	for _, p := range m.OnlineStreamProviders() {
		if p.ID() == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("online streaming extension %q is not installed or disabled", id)
}

func (m *Manager) MangaProviders() []*MangaProvider {
	var out []*MangaProvider
	for _, l := range m.byType(TypeManga) {
		out = append(out, &MangaProvider{ext: l})
	}
	return out
}

func (m *Manager) MangaProvider(id string) (*MangaProvider, error) {
	for _, p := range m.MangaProviders() {
		if p.ID() == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("manga extension %q is not installed or disabled", id)
}

// Shutdown stops every runtime.
func (m *Manager) Shutdown() {
	m.mu.RLock()
	list := make([]*Loaded, 0, len(m.exts))
	for _, l := range m.exts {
		list = append(list, l)
	}
	m.mu.RUnlock()
	for _, l := range list {
		l.stop()
		m.stopPlugin(l.Manifest.ID)
	}
}
