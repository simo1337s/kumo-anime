package api

import (
	"context"
	"net/http"
	"time"

	"github.com/simo1337s/animetest/server/internal/lifecycle"
	"github.com/simo1337s/animetest/server/internal/update"
)

// Updates of Kumo itself (see package update). Devices on the LAN see the
// status, but only this computer checks, installs and restarts.

func (s *Server) updateStatus(r *http.Request) (any, error) {
	return updateFor(r, s.app.Update.Status()), nil
}

// updateFor leaves out what devices on the LAN can't do or use.
func updateFor(r *http.Request, st update.Status) update.Status {
	if !isTrusted(r) {
		st.CanApply, st.ApplyNote, st.ManualCommand = false, "", ""
	}
	return st
}

func (s *Server) checkUpdate(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("only available on this computer")
	}
	// The check carries on when the page goes away: its result is shown
	// next time.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	return s.app.Update.Check(ctx), nil
}

func (s *Server) applyUpdate(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("only available on this computer")
	}
	// Already running, nothing newer, or a copy that can't update itself
	// (the Arch package and the installed Windows app can).
	if err := s.app.Update.Apply(); err != nil {
		return nil, &apiError{http.StatusConflict, err.Error()}
	}
	return s.app.Update.Status(), nil
}

// restartKumo restarts Kumo into the version an update installed: it
// answers, then shuts down and starts again (see cmd/kumo).
func (s *Server) restartKumo(w http.ResponseWriter, r *http.Request) {
	if !isTrusted(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only available on this computer"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	s.app.Exits.Request(lifecycle.Exit{Restart: true})
}
