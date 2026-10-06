package extensions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	mgr := newTestManager(t)
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
			l := installTestExtension(t, mgr, m, payload, true)
			mgr.startPlugin(l)
			time.Sleep(300 * time.Millisecond)
			if inf, _ := mgr.Get(m.ID); inf.Error != "" {
				t.Errorf("plugin error: %s", inf.Error)
			}
			mgr.pluginMu.Lock()
			h := mgr.plugins[m.ID]
			mgr.pluginMu.Unlock()
			if h == nil {
				t.Fatal("no host")
			}
			snap := h.Snapshot()
			s := string(snap.State)
			if len(s) > 300 {
				s = s[:300] + "…"
			}
			t.Logf("state: %s", s)
			for _, l := range h.rt.Logs() {
				if l.Level == "error" {
					t.Logf("log %s: %s", l.Level, strings.Split(l.Message, "\n")[0])
				}
			}
			mgr.stopPlugin(m.ID)
		})
	}
}
