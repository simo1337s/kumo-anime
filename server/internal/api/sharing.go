package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/simo1337s/animetest/server/internal/history"
	"github.com/simo1337s/animetest/server/internal/player"
	"github.com/simo1337s/animetest/server/internal/share"
	"github.com/simo1337s/animetest/server/internal/stream"
)

// Library sharing with other Kumo apps on the home network (package share).
//
// As a host, this Kumo answers the others under /api/peer/: who it is, its
// shared files, and playing them. Those requests are checked by servePeer,
// apart from the browsers': no login or Web UI needed, but a valid token
// from a Kumo, and only from the home network. Playing a shared file on the
// other Kumo updates that Kumo's list and history only: the host just sends
// the video.
//
// As a guest, its own player plays the files shared with it (kumo:// paths)
// through the usual /api/local/ endpoints, which forward them to the host.

type peerKey struct{}

// servePeer checks a request from another Kumo.
func (s *Server) servePeer(w http.ResponseWriter, r *http.Request, next http.Handler) {
	ip := clientIP(r)
	if !s.app.Settings.Get().Sharing.Enabled || ip == nil || !(ip.IsLoopback() || isLANIP(ip)) {
		http.Error(w, "library sharing is off", http.StatusForbidden)
		return
	}
	if !hostAllowed(r.Host) {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	// Kumo apps don't send it; websites in a browser do.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if r.URL.Path == "/api/peer/whoami" {
		next.ServeHTTP(w, r)
		return
	}
	c, err := s.app.Share.Verify(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "not a Kumo app this one knows: " + err.Error()})
		return
	}
	next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerKey{}, c)))
}

func peerOf(r *http.Request) share.Caller {
	c, _ := r.Context().Value(peerKey{}).(share.Caller)
	return c
}

// peerAllowed reports a Kumo this one shares its library with.
func peerAllowed(r *http.Request) bool { return peerOf(r).Allowed }

// shared wraps the endpoints only the Kumos this one shares with may use.
func shared(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !peerAllowed(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "this library isn't shared with you"})
			return
		}
		fn(w, r)
	}
}

func (s *Server) peerRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/peer/whoami", h(func(r *http.Request) (any, error) { return s.app.Share.Whoami(), nil }))
	m.HandleFunc("GET /api/peer/hello", h(func(r *http.Request) (any, error) { return s.app.Share.Hello(peerAllowed(r)), nil }))
	m.HandleFunc("GET /api/peer/files", shared(h(func(r *http.Request) (any, error) { return s.app.Share.SharedFiles() })))
	// The watch history, for a Kumo it shares with on the same AniList
	// account: Continue watching goes on there where it stopped here.
	m.HandleFunc("GET /api/peer/history", shared(h(func(r *http.Request) (any, error) {
		if !peerOf(r).SameUser {
			return nil, forbidden("not logged into the same AniList account")
		}
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		return s.app.Share.HistorySince(since), nil
	})))
	// And where it's watching this Kumo's files, as it happens.
	m.HandleFunc("POST /api/peer/history", shared(h(func(r *http.Request) (any, error) {
		if !peerOf(r).SameUser {
			return nil, forbidden("not logged into the same AniList account")
		}
		var entries []history.Entry
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&entries); err != nil {
			return nil, badRequest(err.Error())
		}
		s.app.Share.TakeHistory(entries)
		return nil, nil
	})))
	m.HandleFunc("GET /api/peer/local/probe", shared(h(func(r *http.Request) (any, error) {
		return s.app.Local.Probe(r.Context(), r.URL.Query().Get("path"))
	})))
	m.HandleFunc("GET /api/peer/local/file", shared(s.localFile))
	m.HandleFunc("GET /api/peer/local/transcode", shared(s.localTranscode))
	m.HandleFunc("GET /api/peer/local/seekpoint", shared(h(s.localSeekPoint)))
	m.HandleFunc("GET /api/peer/local/subtitle", shared(s.localSubtitle))
	m.HandleFunc("POST /api/peer/local/hls", shared(h(func(r *http.Request) (any, error) {
		var req stream.HLSRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return s.app.HLS.Start(r.Context(), req)
	})))
	m.HandleFunc("GET /api/peer/local/hls/{id}/{name}", shared(func(w http.ResponseWriter, r *http.Request) {
		s.app.HLS.Serve(w, r, r.PathValue("id"), r.PathValue("name"))
	}))
	m.HandleFunc("DELETE /api/peer/local/hls/{id}", shared(h(func(r *http.Request) (any, error) {
		s.app.HLS.Stop(r.PathValue("id"))
		return nil, nil
	})))

	// This Kumo's settings and the libraries shared with it.
	// Opened: what the others share is asked for again.
	m.HandleFunc("GET /api/sharing", h(func(r *http.Request) (any, error) {
		s.app.Share.Refresh()
		return s.app.Share.Status(), nil
	}))
	m.HandleFunc("GET /api/sharing/libraries", h(func(r *http.Request) (any, error) {
		s.app.Share.Refresh()
		return s.app.Share.Libraries(), nil
	}))
	m.HandleFunc("POST /api/sharing/peers/{id}", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this computer decides who its library is shared with")
		}
		var body struct {
			Allowed bool `json:"allowed"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if err := s.app.Share.SetAllowed(r.PathValue("id"), body.Allowed); err != nil {
			return nil, notFound(err.Error())
		}
		return s.app.Share.Status(), nil
	}))
	m.HandleFunc("DELETE /api/sharing/peers/{id}", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this computer can change library sharing")
		}
		s.app.Share.Forget(r.PathValue("id"))
		return s.app.Share.Status(), nil
	}))
	m.HandleFunc("POST /api/sharing/connect", h(func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, forbidden("only this computer can change library sharing")
		}
		var body struct {
			Address string `json:"address"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if !s.app.Settings.Get().Sharing.Enabled {
			return nil, badRequest("turn on library sharing first")
		}
		v, err := s.app.Share.Connect(r.Context(), body.Address)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		return v, nil
	}))
}

// forwardShared forwards a player's request about a shared file to its host.
func (s *Server) forwardShared(w http.ResponseWriter, r *http.Request, endpoint string) bool {
	if !share.IsRemote(r.URL.Query().Get("path")) {
		return false
	}
	s.app.Share.Forward(w, r, endpoint)
	return true
}

// The in-app player's endpoints, for this Kumo's files (and its guests').

func (s *Server) localFile(w http.ResponseWriter, r *http.Request) {
	s.app.Local.ServeFile(w, r, r.URL.Query().Get("path"))
}

func (s *Server) localTranscode(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, _ := strconv.ParseFloat(q.Get("start"), 64)
	audio, _ := strconv.Atoi(q.Get("audio"))
	s.app.Local.ServeTranscode(w, r, q.Get("path"), start, audio, q.Get("method"), stream.ParseCaps(q.Get("caps")))
}

func (s *Server) localSeekPoint(r *http.Request) (any, error) {
	q := r.URL.Query()
	t, _ := strconv.ParseFloat(q.Get("t"), 64)
	audio, _ := strconv.Atoi(q.Get("audio"))
	start, err := s.app.Local.SeekPoint(r.Context(), q.Get("path"), t, audio, q.Get("method"), stream.ParseCaps(q.Get("caps")), q.Get("hls") == "1")
	if err != nil {
		return nil, err
	}
	return map[string]float64{"start": start}, nil
}

func (s *Server) localSubtitle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	idx, _ := strconv.Atoi(q.Get("index"))
	s.app.Local.ServeSubtitle(w, r, q.Get("path"), idx, q.Get("external"))
}

// playShared opens a shared file: in mpv, which plays it from the host, or
// in the in-app player. Progress and history are this Kumo's, with its
// AniList account.
func (s *Server) playShared(r *http.Request, body playLocalBody) (any, error) {
	f, ok := s.app.Share.File(body.Path)
	if !ok {
		return nil, notFound("that library isn't shared with this Kumo any more")
	}
	if body.MediaID == 0 {
		body.MediaID, body.Episode = f.MediaID, f.Episode
	}
	playerName := body.Player
	if playerName == "" {
		playerName = s.app.Settings.Get().Playback.DefaultPlayer
	}
	if !isTrusted(r) {
		playerName = "builtin"
	}
	title := s.episodeTitle(r.Context(), body.MediaID, body.Episode, f.Name)
	if playerName == "mpv" {
		target, headers, err := s.app.Share.MediaURL(body.Path)
		if err != nil {
			return nil, notFound(err.Error())
		}
		sess, err := s.app.Player.PlayMpv(player.PlayRequest{
			MediaID: body.MediaID, Episode: body.Episode, Title: title, Source: "local", Target: target, Headers: headers, Start: body.Start,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"player": "mpv", "session": sess}, nil
	}
	probe, err := s.app.Share.Probe(r.Context(), body.Path)
	if err != nil {
		return nil, badRequest(strings.TrimSpace(f.HostName + ": " + err.Error()))
	}
	probe.Path = body.Path
	return s.builtinPlay(body, probe, title), nil
}
