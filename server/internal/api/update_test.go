package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/update"
)

func TestUpdateRoutes(t *testing.T) {
	s := newTestServer(t)
	local, lan := "127.0.0.1:5000", "192.168.1.20:5000"
	cfg := s.app.Settings.Get()
	cfg.Server.AllowLAN = true
	if _, err := s.app.Settings.Save(cfg); err != nil {
		t.Fatal(err)
	}

	// Devices on the network see the status, but can't install.
	w := do(s, "GET", "/api/update", lan, "", nil)
	var st update.Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); w.Code != http.StatusOK || err != nil {
		t.Fatalf("LAN status: %d %v", w.Code, err)
	}
	if st.CanApply || st.Current.Version != config.AppVersion || st.State != update.StateIdle {
		t.Fatalf("LAN status: %+v", st)
	}
	// Only this computer checks, installs and restarts.
	for _, path := range []string{"/api/update/check", "/api/update/apply", "/api/update/restart"} {
		if w := do(s, "POST", path, lan, "{}", nil); w.Code != http.StatusForbidden {
			t.Errorf("LAN %s: got %d", path, w.Code)
		}
	}
	select {
	case e := <-s.app.Exits.C():
		t.Fatalf("a device on the network restarted Kumo: %+v", e)
	default:
	}

	// Nothing newer is known yet.
	if w := do(s, "POST", "/api/update/apply", local, "{}", nil); w.Code != http.StatusConflict {
		t.Fatalf("apply without an update: got %d %s", w.Code, w.Body.String())
	}
	// Restart answers, then asks cmd/kumo to shut down and start again.
	if w := do(s, "POST", "/api/update/restart", local, "{}", nil); w.Code != http.StatusOK {
		t.Fatalf("restart: got %d", w.Code)
	}
	select {
	case e := <-s.app.Exits.C():
		if !e.Restart {
			t.Fatalf("restart asked for %+v", e)
		}
	default:
		t.Fatal("restart didn't ask for a restart")
	}
}
