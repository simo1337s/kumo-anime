package extensions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
)

func loadSample(t *testing.T, dir string) (*Manifest, string) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Skipf("sample not available: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	payload := m.Payload
	if b, err := os.ReadFile(filepath.Join(dir, "payload.src")); err == nil {
		payload = string(b)
	}
	return &m, payload
}

// KUMO_EXT_SAMPLES points at a folder laid out as <type>/<id>/{manifest.json,payload.src}.
func TestMarketplaceProvidersLoad(t *testing.T) {
	root := os.Getenv("KUMO_EXT_SAMPLES")
	if root == "" {
		t.Skip("KUMO_EXT_SAMPLES not set")
	}
	for _, typ := range []string{TypeOnlineStream, TypeTorrent, TypeManga} {
		dirs, _ := os.ReadDir(filepath.Join(root, typ))
		for _, d := range dirs {
			dir := filepath.Join(root, typ, d.Name())
			t.Run(typ+"/"+d.Name(), func(t *testing.T) {
				m, payload := loadSample(t, dir)
				if err := m.Validate(); err != nil {
					t.Fatalf("validate: %v", err)
				}
				payload, prefs, _ := ApplyUserConfig(m, payload, nil)
				prog, err := Compile(m, payload)
				if err != nil {
					t.Fatalf("compile: %v", err)
				}
				rt, err := NewRuntime(m, prog, RuntimeOptions{Prefs: prefs})
				if err != nil {
					t.Fatalf("runtime: %v", err)
				}
				defer rt.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				raw, err := rt.CallProvider(ctx, "getSettings")
				if err != nil {
					t.Fatalf("getSettings: %v", err)
				}
				t.Logf("settings: %s", raw)
			})
		}
	}
}

func TestPluginsStart(t *testing.T) {
	root := os.Getenv("KUMO_EXT_SAMPLES")
	if root == "" {
		t.Skip("KUMO_EXT_SAMPLES not set")
	}
	tmp := t.TempDir()
	d, _ := db.Open(filepath.Join(tmp, "t.db"))
	st, _ := config.NewStore(d)
	hub := events.NewHub()
	mgr := NewManager(d, st, hub)
	mgr.Host = PluginHostServices{
		Collection: func(ctx context.Context, mt string, r bool) (any, error) {
			return map[string]any{"MediaListCollection": map[string]any{"lists": []any{}}}, nil
		},
		GetAnime: func(ctx context.Context, id int) (any, error) { return map[string]any{"id": id}, nil },
	}
	dirs, _ := os.ReadDir(filepath.Join(root, TypePlugin))
	for _, dd := range dirs {
		dir := filepath.Join(root, TypePlugin, dd.Name())
		t.Run(dd.Name(), func(t *testing.T) {
			m, payload := loadSample(t, dir)
			if err := m.Validate(); err != nil {
				t.Skipf("validate: %v", err)
			}
			l := &Loaded{mgr: mgr, Manifest: m, payload: payload, Enabled: true}
			mgr.startPlugin(l)
			time.Sleep(300 * time.Millisecond)
			mgr.pluginMu.Lock()
			h := mgr.plugins[m.ID]
			mgr.pluginMu.Unlock()
			if h == nil {
				t.Fatal("no host")
			}
			if h.err != "" {
				t.Errorf("plugin error: %s", h.err)
			}
			snap := h.Snapshot()
			s := string(snap.State)
			if len(s) > 300 {
				s = s[:300] + "…"
			}
			t.Logf("state: %s", s)
			if h.rt != nil {
				for _, l := range h.rt.Logs() {
					if l.Level == "error" {
						t.Logf("log %s: %s", l.Level, strings.Split(l.Message, "\n")[0])
					}
				}
			}
			mgr.stopPlugin(m.ID)
		})
	}
}
