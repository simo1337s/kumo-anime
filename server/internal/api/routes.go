package api

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/stream"
	"github.com/simo1337s/animetest/server/internal/util"
)

func (s *Server) routes() {
	m := s.mux

	// --- core
	m.HandleFunc("GET /api/status", h(s.status))
	m.HandleFunc("GET /api/settings", h(func(r *http.Request) (any, error) { return settingsFor(r, s.app.Settings.Get()), nil }))
	m.HandleFunc("PUT /api/settings", h(s.saveSettings))
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("POST /api/auth/anilist", h(s.anilistLogin))
	m.HandleFunc("POST /api/auth/logout", h(func(r *http.Request) (any, error) { s.app.Platform.Logout(); return nil, nil }))
	m.HandleFunc("POST /api/auth/server-login", s.serverLogin)
	m.HandleFunc("GET /api/fs/dirs", h(s.listDirs))
	m.HandleFunc("POST /api/open", h(s.openPath))
	m.HandleFunc("GET /api/logs", h(func(r *http.Request) (any, error) { return s.app.Logs.Lines(), nil }))
	m.HandleFunc("POST /api/cache/clear", h(func(r *http.Request) (any, error) { return nil, s.app.DB.ClearCache() }))
	m.HandleFunc("GET /api/cache/size", h(func(r *http.Request) (any, error) {
		n, err := s.app.DB.CacheSize()
		return map[string]int{"entries": n}, err
	}))

	// --- anime / anilist
	m.HandleFunc("GET /api/anime/collection", h(s.animeCollection))
	m.HandleFunc("GET /api/anime/{id}", h(s.animeEntry))
	m.HandleFunc("POST /api/anime/{id}/entry", h(s.updateEntry))
	m.HandleFunc("POST /api/anime/{id}/episode", h(s.markEpisode))
	m.HandleFunc("DELETE /api/anime/{id}/entry", h(s.deleteEntry))
	m.HandleFunc("POST /api/anilist/search", h(s.search))
	m.HandleFunc("GET /api/anilist/discover", h(s.discover))
	m.HandleFunc("GET /api/anilist/schedule", h(s.schedule))
	m.HandleFunc("GET /api/anilist/list", h(s.rawList))

	// --- library
	m.HandleFunc("POST /api/library/scan", h(s.scan))
	m.HandleFunc("GET /api/library/unmatched", h(s.unmatched))
	m.HandleFunc("GET /api/library/ignored", h(func(r *http.Request) (any, error) { return s.app.Files.Ignored() }))
	m.HandleFunc("GET /api/library/files", h(func(r *http.Request) (any, error) { return s.app.Files.All() }))
	m.HandleFunc("POST /api/library/match", h(s.match))
	m.HandleFunc("POST /api/library/unmatch", h(s.unmatch))
	m.HandleFunc("POST /api/library/ignore", h(s.ignore))
	m.HandleFunc("PATCH /api/library/file", h(s.patchFile))
	m.HandleFunc("POST /api/library/open-folder", h(s.openFolder))

	// --- playback
	m.HandleFunc("POST /api/playback/local", h(s.playLocal))
	m.HandleFunc("POST /api/playback/stream", h(s.playStream))
	m.HandleFunc("GET /api/playback/status", h(func(r *http.Request) (any, error) { return s.app.Player.Status(), nil }))
	m.HandleFunc("POST /api/playback/command", h(s.playbackCommand))
	m.HandleFunc("POST /api/playback/progress", h(s.playbackProgress))
	m.HandleFunc("GET /api/playback/tracks/{mediaId}", h(s.getTracks))
	m.HandleFunc("PUT /api/playback/tracks/{mediaId}", h(s.saveTracks))
	m.HandleFunc("GET /api/playback/skips", h(s.skips))
	m.HandleFunc("GET /api/history/recent", h(func(r *http.Request) (any, error) { return s.app.History.Recent(50), nil }))
	m.HandleFunc("DELETE /api/history", h(s.clearHistory))

	// --- in-app player helpers
	m.HandleFunc("GET /api/local/probe", h(func(r *http.Request) (any, error) {
		return s.app.Local.Probe(r.Context(), r.URL.Query().Get("path"))
	}))
	m.HandleFunc("GET /api/local/file", func(w http.ResponseWriter, r *http.Request) {
		s.app.Local.ServeFile(w, r, r.URL.Query().Get("path"))
	})
	m.HandleFunc("GET /api/local/transcode", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		start, _ := strconv.ParseFloat(q.Get("start"), 64)
		audio, _ := strconv.Atoi(q.Get("audio"))
		s.app.Local.ServeTranscode(w, r, q.Get("path"), start, audio, q.Get("method"))
	})
	m.HandleFunc("GET /api/local/subtitle", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		idx, _ := strconv.Atoi(q.Get("index"))
		s.app.Local.ServeSubtitle(w, r, q.Get("path"), idx, q.Get("external"))
	})
	m.HandleFunc("GET /api/proxy", stream.ServeProxy)
	m.Handle("GET /api/img", s.app.Images)
	m.HandleFunc("GET /api/images/stats", h(func(r *http.Request) (any, error) { return s.app.Images.Stats(), nil }))
	m.HandleFunc("POST /api/images/clear", h(func(r *http.Request) (any, error) { return nil, s.app.Images.Clear() }))

	// --- online streaming & ani-cli
	m.HandleFunc("GET /api/onlinestream/providers", h(func(r *http.Request) (any, error) { return s.app.Stream.Providers(r.Context()), nil }))
	m.HandleFunc("GET /api/onlinestream/episodes", h(s.osEpisodes))
	m.HandleFunc("GET /api/onlinestream/sources", h(s.osSources))
	m.HandleFunc("GET /api/onlinestream/search", h(s.osSearch))
	m.HandleFunc("POST /api/onlinestream/mapping", h(s.osMapping))
	m.HandleFunc("POST /api/onlinestream/play", h(s.osPlay))
	m.HandleFunc("POST /api/onlinestream/download", h(s.osDownload))
	m.HandleFunc("GET /api/anicli/status", h(func(r *http.Request) (any, error) { return s.app.AniCli.Status(r.Context()), nil }))

	// --- downloads
	m.HandleFunc("GET /api/downloads", h(func(r *http.Request) (any, error) { return s.app.Downloads.List(), nil }))
	m.HandleFunc("POST /api/downloads/{id}/cancel", h(func(r *http.Request) (any, error) { s.app.Downloads.Cancel(r.PathValue("id")); return nil, nil }))
	m.HandleFunc("POST /api/downloads/{id}/retry", h(func(r *http.Request) (any, error) { return nil, s.app.Downloads.Retry(r.PathValue("id")) }))
	m.HandleFunc("DELETE /api/downloads/{id}", h(func(r *http.Request) (any, error) { s.app.Downloads.Remove(r.PathValue("id")); return nil, nil }))
	m.HandleFunc("POST /api/downloads/clear", h(func(r *http.Request) (any, error) { s.app.Downloads.ClearFinished(); return nil, nil }))

	// --- torrents
	m.HandleFunc("GET /api/torrents/providers", h(func(r *http.Request) (any, error) { return s.app.Torrents.Providers(), nil }))
	m.HandleFunc("POST /api/torrents/search", h(s.torrentSearch))
	m.HandleFunc("POST /api/torrents/download", h(s.torrentDownload))
	m.HandleFunc("GET /api/torrent-client/status", h(func(r *http.Request) (any, error) { return s.app.Torrents.Status(r.Context()), nil }))
	m.HandleFunc("GET /api/torrent-client/list", h(s.torrentList))
	m.HandleFunc("POST /api/torrent-client/action", h(s.torrentAction))
	m.HandleFunc("POST /api/torrent-client/start", h(func(r *http.Request) (any, error) { return nil, s.app.Torrents.StartClient(r.Context()) }))
	m.HandleFunc("POST /api/torrent-client/add", h(s.torrentAdd))
	m.HandleFunc("GET /api/autodownloader/rules", h(func(r *http.Request) (any, error) { return s.app.AutoDL.Rules() }))
	m.HandleFunc("POST /api/autodownloader/rules", h(s.saveRule))
	m.HandleFunc("DELETE /api/autodownloader/rules/{id}", h(func(r *http.Request) (any, error) {
		id, _ := strconv.Atoi(r.PathValue("id"))
		return nil, s.app.AutoDL.DeleteRule(id)
	}))
	m.HandleFunc("POST /api/autodownloader/run", h(func(r *http.Request) (any, error) {
		n, err := s.app.AutoDL.Run(r.Context())
		return map[string]int{"added": n}, err
	}))
	m.HandleFunc("GET /api/autodownloader/items", h(func(r *http.Request) (any, error) { return s.app.AutoDL.Items(), nil }))

	// --- extensions & plugins
	m.HandleFunc("GET /api/extensions", h(func(r *http.Request) (any, error) { return s.app.Extensions.List(), nil }))
	m.HandleFunc("GET /api/extensions/marketplace", h(func(r *http.Request) (any, error) {
		return s.app.Extensions.Marketplace(r.Context(), r.URL.Query().Get("url"), r.URL.Query().Get("refresh") == "1")
	}))
	m.HandleFunc("GET /api/extensions/updates", h(func(r *http.Request) (any, error) { return s.app.Extensions.CheckUpdates(r.Context()), nil }))
	m.HandleFunc("POST /api/extensions/install", h(s.installExtension))
	m.HandleFunc("POST /api/extensions/{id}/enable", h(s.enableExtension))
	m.HandleFunc("POST /api/extensions/{id}/config", h(s.configExtension))
	m.HandleFunc("POST /api/extensions/{id}/grant", h(func(r *http.Request) (any, error) { return nil, s.app.Extensions.Grant(r.PathValue("id")) }))
	m.HandleFunc("POST /api/extensions/{id}/update", h(func(r *http.Request) (any, error) { return s.app.Extensions.Update(r.Context(), r.PathValue("id")) }))
	m.HandleFunc("DELETE /api/extensions/{id}", h(func(r *http.Request) (any, error) { return nil, s.app.Extensions.Uninstall(r.PathValue("id")) }))
	m.HandleFunc("GET /api/extensions/{id}/logs", h(func(r *http.Request) (any, error) { return s.app.Extensions.Logs(r.PathValue("id")), nil }))
	m.HandleFunc("GET /api/plugins/ui", h(func(r *http.Request) (any, error) { return s.app.Extensions.PluginStates(), nil }))
	m.HandleFunc("POST /api/plugins/{id}/event", h(s.pluginEvent))
	m.HandleFunc("POST /api/plugins/navigate", h(func(r *http.Request) (any, error) {
		var body map[string]any
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		body["kind"] = "navigate"
		s.app.Extensions.BroadcastPluginEvent(body)
		return nil, nil
	}))

	// --- manga
	m.HandleFunc("GET /api/manga/collection", h(func(r *http.Request) (any, error) {
		return s.app.Platform.Collection(r.Context(), "MANGA", r.URL.Query().Get("refresh") == "1")
	}))
	m.HandleFunc("GET /api/manga/providers", h(s.mangaProviders))
	m.HandleFunc("GET /api/manga/{id}", h(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.app.Platform.Media(r.Context(), id, r.URL.Query().Get("refresh") == "1")
	}))
	m.HandleFunc("GET /api/manga/{id}/chapters", h(s.mangaChapters))
	m.HandleFunc("GET /api/manga/pages", h(func(r *http.Request) (any, error) {
		return s.app.Manga.Pages(r.Context(), r.URL.Query().Get("provider"), r.URL.Query().Get("chapterId"))
	}))
	m.HandleFunc("GET /api/manga/search", h(func(r *http.Request) (any, error) {
		return s.app.Manga.Search(r.Context(), r.URL.Query().Get("provider"), r.URL.Query().Get("q"))
	}))
	m.HandleFunc("POST /api/manga/mapping", h(s.mangaMapping))
	m.HandleFunc("POST /api/manga/{id}/progress", h(s.mangaProgress))

	// --- web UI (SPA)
	m.HandleFunc("/", s.serveUI)
}

func pathID(r *http.Request, name string) (int, error) {
	id, err := strconv.Atoi(r.PathValue(name))
	if err != nil || id <= 0 {
		return 0, badRequest("invalid id")
	}
	return id, nil
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

// ---------------------------------------------------------------------------
// Static UI

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if st, err := fs.Stat(s.ui, p); err != nil || st.IsDir() {
		p = "index.html" // SPA fallback
	}
	if p == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	data, err := fs.ReadFile(s.ui, p)
	if err != nil {
		http.Error(w, "The web UI was not built. Run `make` in the project root.", http.StatusNotFound)
		return
	}
	http.ServeContent(w, r, p, time.Time{}, strings.NewReader(string(data)))
}

// ---------------------------------------------------------------------------
// Core handlers

type featureStatus struct {
	Mpv     bool `json:"mpv"`
	Ffmpeg  bool `json:"ffmpeg"`
	Ffprobe bool `json:"ffprobe"`
	AniCli  bool `json:"aniCli"`
	YtDlp   bool `json:"ytDlp"`
	XdgOpen bool `json:"xdgOpen"`
}

func (s *Server) status(r *http.Request) (any, error) {
	cfg := s.app.Settings.Get()
	feat := featureStatus{}
	_, feat.Mpv = util.LookPath(cfg.Mpv.Path)
	_, feat.Ffmpeg = util.LookPath(cfg.Transcode.FfmpegPath)
	_, feat.Ffprobe = util.LookPath(cfg.Transcode.FfprobePath)
	_, feat.AniCli = util.LookPath(cfg.AniCli.Path)
	_, feat.YtDlp = util.LookPath("yt-dlp")
	_, feat.XdgOpen = util.LookPath("xdg-open")
	kind := map[clientKind]string{clientShell: "desktop", clientLocal: "local", clientLAN: "lan"}[kindOf(r)]
	host, _ := os.Hostname()
	return map[string]any{
		"version":        config.AppVersion,
		"appName":        config.AppName,
		"user":           s.app.Platform.Viewer(),
		"loggedIn":       s.app.Platform.LoggedIn(),
		"anilistAuthUrl": "https://anilist.co/api/v2/oauth/authorize?client_id=" + cfg.Anilist.ClientID + "&response_type=token",
		"features":       feat,
		"client":         kind,
		"hostname":       host,
		"scanning":       s.app.Scanner.Running(),
		"dataDir":        s.app.DataDir,
		"listenAddr":     s.addr,
		"webUiForced":    s.ForceWebUI,
		"settings":       settingsFor(r, cfg),
	}, nil
}

func (s *Server) saveSettings(r *http.Request) (any, error) {
	// Two separate copies: decoding writes into next's slices, and cur must
	// keep the old values to restore the fields LAN devices may not change.
	cur, next := s.app.Settings.Get(), s.app.Settings.Get()
	if err := decode(r, &next); err != nil {
		return nil, err
	}
	// Devices on the LAN may not change network/security settings or
	// anything that decides which programs run or which folders are read
	// on this computer.
	if !isTrusted(r) {
		next.Server = cur.Server
		next.Mpv.Path, next.Mpv.ExtraArgs, next.Mpv.Socket = cur.Mpv.Path, cur.Mpv.ExtraArgs, cur.Mpv.Socket
		next.Transcode.FfmpegPath, next.Transcode.FfprobePath, next.Transcode.VaapiNode = cur.Transcode.FfmpegPath, cur.Transcode.FfprobePath, cur.Transcode.VaapiNode
		next.AniCli.Path, next.AniCli.DownloadDir = cur.AniCli.Path, cur.AniCli.DownloadDir
		// Torrent client logins too: they never see the passwords, and
		// pointing the client at another host would send it the password.
		next.Qbittorrent, next.Transmission = cur.Qbittorrent, cur.Transmission
		next.Library.Dir, next.Library.ExtraDirs = cur.Library.Dir, cur.Library.ExtraDirs
	}
	// Turning off the web UI from a browser would lock the browser out.
	if kindOf(r) != clientShell && !next.Server.WebUI && cur.Server.WebUI && !s.ForceWebUI {
		return nil, forbidden("turn off the Web UI from the desktop app, otherwise this browser would lose access")
	}
	saved, err := s.app.Settings.Save(next)
	return settingsFor(r, saved), err
}

// settingsFor hides passwords from devices on the LAN.
func settingsFor(r *http.Request, cfg config.Settings) config.Settings {
	if !isTrusted(r) {
		cfg.Server.Password = ""
		cfg.Qbittorrent.Password, cfg.Transmission.Password = "", ""
	}
	return cfg
}

func (s *Server) anilistLogin(r *http.Request) (any, error) {
	var body struct {
		Token string `json:"token"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	tok := strings.TrimSpace(body.Token)
	// Accept a pasted redirect URL too (…#access_token=XYZ&token_type=…).
	if i := strings.Index(tok, "access_token="); i >= 0 {
		tok = tok[i+len("access_token="):]
		if j := strings.IndexAny(tok, "&# "); j >= 0 {
			tok = tok[:j]
		}
	}
	v, err := s.app.Platform.Login(r.Context(), tok)
	if err != nil {
		return nil, badRequest("AniList rejected the token: " + err.Error())
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = s.app.Platform.Collection(ctx, "ANIME", true)
	}()
	return v, nil
}

func (s *Server) serverLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cfg := s.app.Settings.Get()
	if cfg.Server.Password == "" || constEq(body.Password, cfg.Server.Password) {
		http.SetCookie(w, &http.Cookie{Name: "kumo_session", Value: s.sessionToken(), Path: "/", HttpOnly: true,
			SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(365 * 24 * time.Hour)})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	time.Sleep(time.Second) // slow down guessing
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong password"})
}

func startDetached(name string, args ...string) error {
	return util.Detach(name, args...)
}

func (s *Server) listDirs(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("browsing folders is only available on this computer")
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		p, _ = os.UserHomeDir()
	}
	p = filepath.Clean(config.ExpandHome(p))
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	dirs := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		} else if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(p, e.Name())); err == nil && st.IsDir() {
				dirs = append(dirs, e.Name())
			}
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i]) < strings.ToLower(dirs[j]) })
	return map[string]any{"path": p, "parent": filepath.Dir(p), "dirs": dirs}, nil
}

func (s *Server) openPath(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("only available on this computer")
	}
	var body struct {
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	target := body.Path
	if body.URL != "" {
		if !strings.HasPrefix(body.URL, "https://") && !strings.HasPrefix(body.URL, "http://") {
			return nil, badRequest("invalid URL")
		}
		target = body.URL
	} else {
		st, err := os.Stat(target)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		// Only ever open folders: xdg-open on a file could run it
		// (.desktop files, scripts).
		if !st.IsDir() {
			target = filepath.Dir(target)
		}
	}
	return nil, startDetached("xdg-open", target)
}

// ---------------------------------------------------------------------------
// Anime

func (s *Server) animeCollection(r *http.Request) (any, error) {
	view, err := s.app.Library.Collection(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err == nil {
		go s.app.PrefetchCollectionArt(view)
	}
	return view, err
}

func (s *Server) animeEntry(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	e, err := s.app.Library.Entry(r.Context(), id, r.URL.Query().Get("refresh") == "1")
	if err == nil {
		go s.app.PrefetchEntryArt(e)
	}
	return e, err
}

func (s *Server) updateEntry(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var u anilist.EntryUpdate
	if err := decode(r, &u); err != nil {
		return nil, err
	}
	u.MediaID = id
	return nil, s.app.Platform.UpdateEntry(r.Context(), u)
}

func (s *Server) deleteEntry(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.app.Platform.DeleteEntry(r.Context(), id)
}

func (s *Server) search(r *http.Request) (any, error) {
	var p anilist.SearchParams
	if err := decode(r, &p); err != nil {
		return nil, err
	}
	if !s.app.Settings.Get().UI.ShowAdult {
		f := false
		p.IsAdult = &f
	}
	return s.app.Platform.Search(r.Context(), p)
}

func currentSeason(t time.Time) (string, int) {
	y := t.Year()
	switch t.Month() {
	case time.December:
		return "WINTER", y + 1
	case time.January, time.February:
		return "WINTER", y
	case time.March, time.April, time.May:
		return "SPRING", y
	case time.June, time.July, time.August:
		return "SUMMER", y
	}
	return "FALL", y
}

func (s *Server) discover(r *http.Request) (any, error) {
	noAdult := false
	season, year := currentSeason(time.Now())
	nextSeason, nextYear := currentSeason(time.Now().AddDate(0, 3, 0))
	type section struct {
		key    string
		params anilist.SearchParams
	}
	sections := []section{
		{"trending", anilist.SearchParams{Sort: []string{"TRENDING_DESC", "POPULARITY_DESC"}, PerPage: 20}},
		{"thisSeason", anilist.SearchParams{Season: season, Year: year, Sort: []string{"POPULARITY_DESC"}, PerPage: 20}},
		{"nextSeason", anilist.SearchParams{Season: nextSeason, Year: nextYear, Sort: []string{"POPULARITY_DESC"}, PerPage: 20}},
		{"popular", anilist.SearchParams{Sort: []string{"POPULARITY_DESC"}, PerPage: 20}},
		{"topRated", anilist.SearchParams{Sort: []string{"SCORE_DESC"}, PerPage: 20}},
	}
	out := map[string]any{"season": season, "year": year}
	var firstErr error
	for _, sec := range sections {
		sec.params.IsAdult = &noAdult
		res, err := s.app.Platform.Search(r.Context(), sec.params)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			out[sec.key] = []any{}
			continue
		}
		out[sec.key] = res.Media
	}
	if firstErr != nil && out["trending"] == nil {
		return nil, firstErr
	}
	return out, nil
}

func (s *Server) schedule(r *http.Request) (any, error) {
	days := queryInt(r, "days")
	if days <= 0 || days > 14 {
		days = 7
	}
	now := time.Now()
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
	items, err := s.app.Platform.Schedule(r.Context(), startDay.Unix(), startDay.AddDate(0, 0, days+1).Unix())
	if err != nil {
		return nil, err
	}
	coll, _ := s.app.Platform.Collection(r.Context(), "ANIME", false)
	inList := map[int]string{}
	if coll != nil {
		for _, e := range coll.Entries() {
			inList[e.MediaID] = e.Status
		}
	}
	type item struct {
		*anilist.AiringEpisode
		ListStatus string `json:"listStatus"`
	}
	out := []item{}
	showAdult := s.app.Settings.Get().UI.ShowAdult
	for _, it := range items {
		if it.Media == nil || (it.Media.IsAdult && !showAdult) {
			continue
		}
		out = append(out, item{it, inList[it.Media.ID]})
	}
	return out, nil
}

func (s *Server) rawList(r *http.Request) (any, error) {
	t := strings.ToUpper(r.URL.Query().Get("type"))
	if t == "" {
		t = "ANIME"
	}
	return s.app.Platform.Collection(r.Context(), t, r.URL.Query().Get("refresh") == "1")
}
