package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/anilist"
	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/downloads"
	"github.com/simo1337s/animetest/server/internal/library"
	"github.com/simo1337s/animetest/server/internal/manga"
	"github.com/simo1337s/animetest/server/internal/player"
	"github.com/simo1337s/animetest/server/internal/stream"
	"github.com/simo1337s/animetest/server/internal/torrent"
	"github.com/simo1337s/animetest/server/internal/util"
)

func constEq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// ---------------------------------------------------------------------------
// Server-sent events

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsub := s.app.Hub.Subscribe()
	defer unsub()
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

// ---------------------------------------------------------------------------
// Library

func (s *Server) scan(r *http.Request) (any, error) {
	var opts library.ScanOptions
	_ = decode(r, &opts)
	if s.app.Scanner.Running() {
		return nil, badRequest(library.ErrScanRunning.Error())
	}
	go func() {
		res, err := s.app.Scanner.Scan(s.app.Context(), opts)
		if err != nil {
			return // reported to the UI with the scan-done event
		}
		msg := fmt.Sprintf("Library scanned — %d files, %d matched", res.Total, res.Matched)
		if res.Unmatched > 0 {
			msg += fmt.Sprintf(", %d unmatched", res.Unmatched)
		}
		s.app.Hub.Success(msg)
	}()
	return map[string]bool{"started": true}, nil
}

// indexFolders adds library folders the scan skipped (or hasn't reached)
// to the index right away, so they can be matched by hand.
func (s *Server) indexFolders(r *http.Request) (any, error) {
	var body struct {
		Dirs []string `json:"dirs"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if len(body.Dirs) == 0 {
		return nil, badRequest("no folders given")
	}
	files, err := s.app.Scanner.IndexFolders(body.Dirs)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	s.app.Hub.Publish("library-updated", nil)
	if files == nil {
		files = []*library.LocalFile{}
	}
	return files, nil
}

func (s *Server) unmatched(r *http.Request) (any, error) {
	files, err := s.app.Files.Unmatched()
	if err != nil {
		return nil, err
	}
	return library.GroupUnmatched(files), nil
}

type pathsBody struct {
	Paths   []string `json:"paths"`
	MediaID int      `json:"mediaId"`
	Ignored bool     `json:"ignored"`
	// Optional: shift episode numbers (e.g. -12 when a folder uses absolute numbering).
	EpisodeOffset int `json:"episodeOffset"`
	// Optional: number the episodes 1, 2, 3… in file-name order instead
	// (for files whose names carry no usable episode number).
	Renumber bool `json:"renumber"`
}

func (s *Server) loadFiles(paths []string) ([]*library.LocalFile, error) {
	var out []*library.LocalFile
	for _, p := range paths {
		f, err := s.app.Files.Get(p)
		if err != nil {
			return nil, notFound("file not in library: " + p)
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *Server) match(r *http.Request) (any, error) {
	var body pathsBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.MediaID <= 0 || len(body.Paths) == 0 {
		return nil, badRequest("mediaId and paths are required")
	}
	media, err := s.app.Platform.MediaLite(r.Context(), body.MediaID)
	if err != nil {
		return nil, err
	}
	files, err := s.loadFiles(body.Paths)
	if err != nil {
		return nil, err
	}
	order := map[string]int{}
	if body.Renumber {
		var main []*library.LocalFile
		for _, f := range files {
			if f.Kind != "nc" && f.Kind != "special" {
				main = append(main, f)
			}
		}
		sort.SliceStable(main, func(i, j int) bool { return naturalLess(main[i].Name, main[j].Name) })
		for i, f := range main {
			order[f.Path] = i + 1
		}
	}
	for _, f := range files {
		f.MediaID, f.Locked, f.Ignored, f.MatchScore = body.MediaID, true, false, 1
		ep := f.Parsed.Episode + body.EpisodeOffset
		if n, ok := order[f.Path]; ok {
			ep = n + body.EpisodeOffset
		}
		switch {
		case f.Kind == "nc":
			f.Episode = 0
		case f.Parsed.Episode < 0 && (media.Format == "MOVIE" || media.TotalEpisodes() == 1):
			f.Episode = 1
		case ep > 0:
			f.Episode = ep
		default:
			f.Episode = 0
		}
	}
	if err := s.app.Files.Save(files...); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("library-updated", nil)
	return nil, nil
}

// torrentFolder is where a torrent for an anime is saved: the folder that
// already holds its episodes (inside the library), else the usual new
// per-anime folder.
func (s *Server) torrentFolder(mediaID int, title string) string {
	def := s.app.Torrents.SavePathFor(title)
	cfg := s.app.Settings.Get()
	lib := filepath.Clean(config.ExpandHome(cfg.Library.Dir))
	if mediaID <= 0 || cfg.Library.Dir == "" || !cfg.Torrent.CreateSubfolder {
		return def
	}
	files, _ := s.app.Files.ByMedia(mediaID)
	counts := map[string]int{}
	best := ""
	for _, f := range files {
		d := filepath.Clean(f.Dir)
		if d == lib || !util.IsSubPath(lib, d) || strings.HasPrefix(filepath.Base(d), ".") {
			continue
		}
		counts[d]++
		if counts[d] > counts[best] {
			best = d
		}
	}
	if best != "" {
		return best
	}
	return def
}

// naturalLess compares file names with numbers in numeric order
// ("Ep 2" < "Ep 10").
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := leadingDigits(a), leadingDigits(b)
		if da != "" && db != "" {
			na, _ := strconv.Atoi(da)
			nb, _ := strconv.Atoi(db)
			if na != nb {
				return na < nb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && i < 9 && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

func (s *Server) unmatch(r *http.Request) (any, error) {
	var body pathsBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	files, err := s.loadFiles(body.Paths)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		f.MediaID, f.Locked, f.MatchScore = 0, true, 0
	}
	if err := s.app.Files.Save(files...); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("library-updated", nil)
	return nil, nil
}

func (s *Server) ignore(r *http.Request) (any, error) {
	var body pathsBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	files, err := s.loadFiles(body.Paths)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		f.Ignored = body.Ignored
		if !body.Ignored {
			f.Locked = false
		}
	}
	if err := s.app.Files.Save(files...); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("library-updated", nil)
	return nil, nil
}

func (s *Server) patchFile(r *http.Request) (any, error) {
	var body struct {
		Path    string  `json:"path"`
		Episode *int    `json:"episode"`
		Kind    *string `json:"kind"`
		MediaID *int    `json:"mediaId"`
		Locked  *bool   `json:"locked"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	f, err := s.app.Files.Get(body.Path)
	if err != nil {
		return nil, notFound("file not in library")
	}
	if body.Episode != nil {
		f.Episode = *body.Episode
		f.Locked = true
	}
	if body.Kind != nil {
		switch *body.Kind {
		case "main", "special", "nc":
			f.Kind = *body.Kind
			f.Locked = true
		default:
			return nil, badRequest("invalid kind")
		}
	}
	if body.MediaID != nil {
		f.MediaID = *body.MediaID
		f.Locked = true
	}
	if body.Locked != nil {
		f.Locked = *body.Locked
	}
	if err := s.app.Files.Save(f); err != nil {
		return nil, err
	}
	s.app.Hub.Publish("library-updated", nil)
	return f, nil
}

func (s *Server) openFolder(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("only available on this computer")
	}
	var body struct {
		MediaID int `json:"mediaId"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	files, _ := s.app.Files.ByMedia(body.MediaID)
	dir := ""
	if len(files) > 0 {
		dir = files[0].Dir
	} else {
		dir = s.app.Settings.Get().Library.Dir
	}
	if dir == "" {
		return nil, badRequest("no folder")
	}
	return nil, startDetached("xdg-open", dir)
}

// ---------------------------------------------------------------------------
// Playback

type playLocalBody struct {
	Path    string   `json:"path"`
	MediaID int      `json:"mediaId"`
	Episode int      `json:"episode"`
	Player  string   `json:"player"` // mpv | builtin ("" = settings)
	Start   *float64 `json:"start"`
	Caps    []string `json:"caps"` // video formats the in-app player decodes
}

func (s *Server) episodeTitle(ctx context.Context, mediaID, episode int, fallback string) string {
	media, err := s.app.Platform.MediaLite(ctx, mediaID)
	if err != nil {
		return fallback
	}
	if media.Format == "MOVIE" {
		return media.PreferredTitle()
	}
	return fmt.Sprintf("%s — Episode %d", media.PreferredTitle(), episode)
}

func (s *Server) playLocal(r *http.Request) (any, error) {
	var body playLocalBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	f, err := s.app.Files.Get(body.Path)
	if err != nil {
		return nil, notFound("file not in library")
	}
	if body.MediaID == 0 {
		body.MediaID, body.Episode = f.MediaID, f.Episode
	}
	playerName := body.Player
	if playerName == "" {
		playerName = s.app.Settings.Get().Playback.DefaultPlayer
	}
	// LAN devices can't see this computer's mpv window.
	if !isTrusted(r) {
		playerName = "builtin"
	}
	title := s.episodeTitle(r.Context(), body.MediaID, body.Episode, f.Name)
	if playerName == "mpv" {
		sess, err := s.app.Player.PlayMpv(player.PlayRequest{
			MediaID: body.MediaID, Episode: body.Episode, Title: title, Source: "local", Target: f.Path, Start: body.Start,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"player": "mpv", "session": sess}, nil
	}
	probe, err := s.app.Local.Probe(r.Context(), f.Path)
	if err != nil {
		return nil, err
	}
	s.app.Local.Decide(probe, stream.CapsOf(body.Caps))
	// The in-app player opens the episode: a new viewing, which may update
	// progress again (rewatch).
	s.app.Player.ResetBuiltinProgress(body.MediaID, body.Episode)
	resume := s.app.History.ResumePosition(body.MediaID, body.Episode)
	if body.Start != nil {
		resume = *body.Start
	}
	return map[string]any{
		"player": "builtin", "probe": probe, "title": title, "resumeAt": resume,
		"mediaId": body.MediaID, "episode": body.Episode, "tracks": s.app.Player.Tracks.Get(body.MediaID),
	}, nil
}

type playStreamBody struct {
	MediaID   int               `json:"mediaId"`
	Episode   int               `json:"episode"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Referrer  string            `json:"referrer"`
	Subtitles []string          `json:"subtitles"`
	Title     string            `json:"title"`
	Source    string            `json:"source"`
	Start     *float64          `json:"start"`
}

func (s *Server) playStream(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("mpv can only be started from this computer")
	}
	var body playStreamBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.URL == "" {
		return nil, badRequest("missing url")
	}
	if body.Title == "" {
		body.Title = s.episodeTitle(r.Context(), body.MediaID, body.Episode, "Stream")
	}
	return s.app.Player.PlayMpv(player.PlayRequest{
		MediaID: body.MediaID, Episode: body.Episode, Title: body.Title, Source: util2(body.Source, "stream"),
		Target: body.URL, Headers: body.Headers, Referrer: body.Referrer, SubFiles: body.Subtitles, Start: body.Start,
	})
}

func util2(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Server) playbackCommand(r *http.Request) (any, error) {
	var body struct {
		Cmd   string  `json:"cmd"`
		Value float64 `json:"value"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return nil, s.app.Player.Command(body.Cmd, body.Value)
}

func (s *Server) playbackProgress(r *http.Request) (any, error) {
	var rep player.ProgressReport
	if err := decode(r, &rep); err != nil {
		return nil, err
	}
	s.app.Player.ReportProgress(rep)
	return nil, nil
}

func (s *Server) getTracks(r *http.Request) (any, error) {
	id, err := pathID(r, "mediaId")
	if err != nil {
		return nil, err
	}
	p := s.app.Player.Tracks.Get(id)
	if p == nil {
		return map[string]any{"mediaId": id}, nil
	}
	return p, nil
}

func (s *Server) saveTracks(r *http.Request) (any, error) {
	id, err := pathID(r, "mediaId")
	if err != nil {
		return nil, err
	}
	var p player.TrackPrefs
	if err := decode(r, &p); err != nil {
		return nil, err
	}
	p.MediaID = id
	if old := s.app.Player.Tracks.Get(id); old != nil && p.StreamMode == "" {
		p.StreamMode = old.StreamMode
	}
	return nil, s.app.Player.Tracks.Save(p)
}

func (s *Server) skips(r *http.Request) (any, error) {
	mediaID, ep := queryInt(r, "mediaId"), queryInt(r, "episode")
	media, err := s.app.Platform.MediaLite(r.Context(), mediaID)
	if err != nil || media.IDMal == nil {
		return []player.SkipInterval{}, nil
	}
	out := player.SkipTimes(r.Context(), s.app.DB, *media.IDMal, ep)
	if out == nil {
		out = []player.SkipInterval{}
	}
	return out, nil
}

func (s *Server) clearHistory(r *http.Request) (any, error) {
	return nil, s.app.History.Clear(queryInt(r, "mediaId"), queryInt(r, "episode"))
}

// ---------------------------------------------------------------------------
// Online streaming

func (s *Server) mediaFromQuery(r *http.Request) (*anilist.Media, error) {
	id := queryInt(r, "mediaId")
	if id <= 0 {
		return nil, badRequest("missing mediaId")
	}
	return s.app.Platform.MediaLite(r.Context(), id)
}

func (s *Server) osEpisodes(r *http.Request) (any, error) {
	media, err := s.mediaFromQuery(r)
	if err != nil {
		return nil, err
	}
	q := r.URL.Query()
	dub := q.Get("dub") == "1" || q.Get("dub") == "true"
	if mode := q.Get("dub"); mode != "" {
		mm := "sub"
		if dub {
			mm = "dub"
		}
		_ = s.setLanguageMode(media.ID, mm)
	}
	return s.app.Stream.Episodes(r.Context(), util2(q.Get("provider"), stream.AniCliProvider), media, dub, q.Get("refresh") == "1")
}

func proxied(src stream.Source) map[string]any {
	headers := map[string]string{}
	for k, v := range src.Headers {
		headers[k] = v
	}
	if src.Referrer != "" {
		headers["Referer"] = src.Referrer
	}
	subs := []map[string]any{}
	for _, sub := range src.Subtitles {
		u := stream.ProxyURL(sub.URL, headers) + "&fmt=vtt"
		subs = append(subs, map[string]any{"url": u, "language": sub.Language, "isDefault": sub.IsDefault, "original": sub.URL})
	}
	return map[string]any{
		"url": stream.ProxyURL(src.URL, headers), "originalUrl": src.URL, "type": src.Type, "quality": src.Quality,
		"label": src.Label, "server": src.Server, "subtitles": subs, "headers": src.Headers, "referrer": src.Referrer,
	}
}

func (s *Server) osSources(r *http.Request) (any, error) {
	media, err := s.mediaFromQuery(r)
	if err != nil {
		return nil, err
	}
	q := r.URL.Query()
	ep, _ := strconv.ParseFloat(q.Get("episode"), 64)
	dub := q.Get("dub") == "1" || q.Get("dub") == "true"
	res, err := s.app.Stream.Sources(r.Context(), util2(q.Get("provider"), stream.AniCliProvider), media, ep, dub, q.Get("server"), q.Get("quality"))
	if err != nil {
		return nil, err
	}
	if q.Get("dub") != "" { // playing in the in-app player: remember the choice
		mode := "sub"
		if dub {
			mode = "dub"
		}
		_ = s.setLanguageMode(media.ID, mode)
	}
	// Only the in-app player asks for sources, when it opens an episode: a
	// new viewing, which may update progress again (rewatch).
	s.app.Player.ResetBuiltinProgress(media.ID, int(ep))
	out := make([]map[string]any, 0, len(res.Sources))
	for _, src := range res.Sources {
		out = append(out, proxied(src))
	}
	return map[string]any{
		"provider": res.Provider, "episode": res.Episode, "sources": out, "errors": res.Errors, "title": res.Title,
		"resumeAt": s.app.History.ResumePosition(media.ID, int(ep)), "tracks": s.app.Player.Tracks.Get(media.ID),
	}, nil
}

func (s *Server) osSearch(r *http.Request) (any, error) {
	q := r.URL.Query()
	return s.app.Stream.Search(r.Context(), util2(q.Get("provider"), stream.AniCliProvider), q.Get("q"), q.Get("dub") == "1")
}

func (s *Server) osMapping(r *http.Request) (any, error) {
	var body struct {
		Provider string `json:"provider"`
		MediaID  int    `json:"mediaId"`
		Dub      bool   `json:"dub"`
		ID       string `json:"id"`
		Title    string `json:"title"`
		Query    string `json:"query"`
		Index    int    `json:"index"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	err := s.app.Stream.SetMapping(body.Provider, body.MediaID, body.Dub, stream.Mapping{ID: body.ID, Title: body.Title, Score: 1, Query: body.Query, Index: body.Index})
	if err == nil {
		mode := "sub"
		if body.Dub {
			mode = "dub"
		}
		s.app.DB.DeleteCachePrefix(fmt.Sprintf("os-eps:%s:%d:%s", body.Provider, body.MediaID, mode))
	}
	return nil, err
}

type osPlayBody struct {
	Provider string  `json:"provider"`
	MediaID  int     `json:"mediaId"`
	Episode  float64 `json:"episode"`
	Dub      bool    `json:"dub"`
	Server   string  `json:"server"`
	Quality  string  `json:"quality"`
	Source   int     `json:"source"`
}

// osPlay resolves an episode and opens it in mpv.
func (s *Server) osPlay(r *http.Request) (any, error) {
	if !isTrusted(r) {
		return nil, forbidden("mpv can only be started from this computer")
	}
	var body osPlayBody
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	media, err := s.app.Platform.MediaLite(r.Context(), body.MediaID)
	if err != nil {
		return nil, err
	}
	provider := util2(body.Provider, stream.AniCliProvider)
	res, err := s.app.Stream.Sources(r.Context(), provider, media, body.Episode, body.Dub, body.Server, body.Quality)
	if err != nil {
		return nil, err
	}
	idx := body.Source
	if idx < 0 || idx >= len(res.Sources) {
		idx = 0
	}
	src := res.Sources[idx]
	var subs []string
	for _, sub := range src.Subtitles {
		subs = append(subs, sub.URL)
	}
	source := "stream"
	if provider == stream.AniCliProvider {
		source = "anicli"
	}
	mode := "sub"
	if body.Dub {
		mode = "dub"
	}
	_ = s.setLanguageMode(media.ID, mode)
	title := fmt.Sprintf("%s — Episode %s [%s]", media.PreferredTitle(), strconv.FormatFloat(body.Episode, 'f', -1, 64), strings.ToUpper(mode))
	return s.app.Player.PlayMpv(player.PlayRequest{
		MediaID: media.ID, Episode: int(body.Episode), Title: title, Source: source, Target: src.URL,
		Headers: src.Headers, Referrer: src.Referrer, SubFiles: subs,
	})
}

func (s *Server) osDownload(r *http.Request) (any, error) {
	var body struct {
		Provider string    `json:"provider"`
		MediaID  int       `json:"mediaId"`
		Episodes []float64 `json:"episodes"`
		Dub      bool      `json:"dub"`
		Quality  string    `json:"quality"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	media, err := s.app.Platform.MediaLite(r.Context(), body.MediaID)
	if err != nil {
		return nil, err
	}
	provider := util2(body.Provider, stream.AniCliProvider)
	source := "stream"
	if provider == stream.AniCliProvider {
		source = "anicli"
	}
	mode := "sub"
	if body.Dub {
		mode = "dub"
	}
	var items []*downloads.Item
	for _, ep := range body.Episodes {
		it, err := s.app.Downloads.Enqueue(downloads.Item{
			MediaID: media.ID, Episode: int(ep), AnimeTitle: media.PreferredTitle(), Image: media.CoverImage.Large,
			Mode: mode, Source: source, Provider: provider, Quality: body.Quality,
		})
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	s.app.Hub.Info(fmt.Sprintf("Queued %d episode(s) for download", len(items)))
	return items, nil
}

// ---------------------------------------------------------------------------
// Torrents

func (s *Server) torrentSearch(r *http.Request) (any, error) {
	var body struct {
		Provider   string `json:"provider"`
		MediaID    int    `json:"mediaId"`
		Query      string `json:"query"`
		Episode    int    `json:"episode"`
		Batch      bool   `json:"batch"`
		Resolution string `json:"resolution"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	var search func(ctx context.Context, p torrent.Provider) ([]*torrent.SearchResult, error)
	if body.MediaID <= 0 {
		if body.Query == "" {
			return nil, badRequest("type something to search")
		}
		search = func(ctx context.Context, p torrent.Provider) ([]*torrent.SearchResult, error) {
			return p.Search(ctx, body.Query)
		}
	} else {
		media, err := s.app.Platform.MediaLite(r.Context(), body.MediaID)
		if err != nil {
			return nil, err
		}
		q := torrent.SmartQuery{Media: media, Query: body.Query, Episode: body.Episode, Batch: body.Batch, Resolution: body.Resolution}
		if meta, err := s.app.Meta.Get(r.Context(), media.ID); err == nil {
			q.AnidbAID = meta.Mappings.AnidbID
		}
		search = func(ctx context.Context, p torrent.Provider) ([]*torrent.SearchResult, error) {
			return p.SmartSearch(ctx, q)
		}
	}
	if body.Provider == allProviders {
		return s.searchAllProviders(r.Context(), search)
	}
	p, err := s.app.Torrents.Provider(body.Provider)
	if err != nil {
		return nil, err
	}
	res, err := search(r.Context(), p)
	if err != nil {
		return nil, err
	}
	if res == nil {
		res = []*torrent.SearchResult{}
	}
	return res, nil
}

func (s *Server) torrentDownload(r *http.Request) (any, error) {
	var body struct {
		Provider string                  `json:"provider"`
		MediaID  int                     `json:"mediaId"`
		Results  []*torrent.SearchResult `json:"results"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	title := ""
	if body.MediaID > 0 {
		if media, err := s.app.Platform.MediaLite(r.Context(), body.MediaID); err == nil {
			title = media.PreferredTitle()
		}
	}
	var uris []string
	for _, res := range body.Results {
		// Results of a multi-provider search each come from their own provider.
		id := body.Provider
		if res.Provider != "" && (id == allProviders || id == "") {
			id = res.Provider
		}
		p, err := s.app.Torrents.Provider(id)
		if err != nil {
			return nil, err
		}
		m, err := p.Magnet(r.Context(), res)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", res.Name, err)
		}
		uris = append(uris, m)
	}
	if len(uris) == 0 {
		return nil, badRequest("nothing to download")
	}
	save := s.torrentFolder(body.MediaID, title)
	if err := s.app.Torrents.Add(r.Context(), uris, save); err != nil {
		return nil, err
	}
	s.app.Hub.Success(fmt.Sprintf("Sent %d torrent(s) to the torrent client", len(uris)))
	return map[string]any{"savePath": save}, nil
}

func (s *Server) torrentList(r *http.Request) (any, error) {
	c, err := s.app.Torrents.Client()
	if err != nil {
		return nil, badRequest(err.Error())
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	list, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []torrent.Torrent{}
	}
	return list, nil
}

func (s *Server) torrentAction(r *http.Request) (any, error) {
	var body struct {
		Hashes []string `json:"hashes"`
		Action string   `json:"action"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	c, err := s.app.Torrents.Client()
	if err != nil {
		return nil, badRequest(err.Error())
	}
	switch body.Action {
	case "pause":
		return nil, c.Pause(r.Context(), body.Hashes)
	case "resume":
		return nil, c.Resume(r.Context(), body.Hashes)
	case "remove":
		return nil, c.Remove(r.Context(), body.Hashes, false)
	case "removeData":
		return nil, c.Remove(r.Context(), body.Hashes, true)
	case "open":
		if !isTrusted(r) {
			return nil, forbidden("only available on this computer")
		}
		list, err := c.List(r.Context())
		if err != nil {
			return nil, err
		}
		for _, t := range list {
			if len(body.Hashes) > 0 && t.Hash == body.Hashes[0] {
				target := t.ContentPath
				if st, err := os.Stat(target); err != nil || !st.IsDir() {
					target = filepath.Dir(target)
				}
				return nil, startDetached("xdg-open", target)
			}
		}
		return nil, notFound("torrent not found")
	}
	return nil, badRequest("unknown action")
}

func (s *Server) torrentAdd(r *http.Request) (any, error) {
	var body struct {
		Magnet  string `json:"magnet"`
		MediaID int    `json:"mediaId"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(body.Magnet, "magnet:") && !strings.HasPrefix(body.Magnet, "http") {
		return nil, badRequest("enter a magnet link or a .torrent URL")
	}
	title := ""
	if body.MediaID > 0 {
		if media, err := s.app.Platform.MediaLite(r.Context(), body.MediaID); err == nil {
			title = media.PreferredTitle()
		}
	}
	return nil, s.app.Torrents.Add(r.Context(), []string{body.Magnet}, s.app.Torrents.SavePathFor(title))
}

func (s *Server) saveRule(r *http.Request) (any, error) {
	var rule torrent.Rule
	if err := decode(r, &rule); err != nil {
		return nil, err
	}
	return s.app.AutoDL.SaveRule(rule)
}

// ---------------------------------------------------------------------------
// Extensions

func (s *Server) installExtension(r *http.Request) (any, error) {
	var body struct {
		ManifestURI string `json:"manifestURI"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	info, err := s.app.Extensions.Install(r.Context(), body.ManifestURI)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	s.app.Hub.Success("Installed " + info.Manifest.Name)
	return info, nil
}

func (s *Server) enableExtension(r *http.Request) (any, error) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return nil, s.app.Extensions.SetEnabled(r.PathValue("id"), body.Enabled)
}

func (s *Server) configExtension(r *http.Request) (any, error) {
	var body struct {
		Values map[string]string `json:"values"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	return nil, s.app.Extensions.SaveUserConfig(r.PathValue("id"), body.Values)
}

func (s *Server) pluginEvent(r *http.Request) (any, error) {
	var evt map[string]any
	if err := decode(r, &evt); err != nil {
		return nil, err
	}
	// Action clicks on media pages carry the media so plugins get {media}.
	if mid, ok := evt["mediaId"].(float64); ok && mid > 0 {
		if media, err := s.app.Platform.MediaLite(r.Context(), int(mid)); err == nil {
			evt["event"] = map[string]any{"media": media}
		}
	}
	return nil, s.app.Extensions.DispatchPluginEvent(r.PathValue("id"), evt)
}

// ---------------------------------------------------------------------------
// Manga

func (s *Server) mangaProviders(r *http.Request) (any, error) {
	out := []map[string]string{}
	for _, p := range s.app.Extensions.MangaProviders() {
		out = append(out, map[string]string{"id": p.ID(), "name": p.Name()})
	}
	return out, nil
}

func (s *Server) mangaChapters(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	media, err := s.app.Platform.MediaLite(r.Context(), id)
	if err != nil {
		return nil, err
	}
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = s.app.Settings.Get().Manga.DefaultProvider
	}
	return s.app.Manga.Chapters(r.Context(), provider, media, r.URL.Query().Get("refresh") == "1")
}

func (s *Server) mangaMapping(r *http.Request) (any, error) {
	var body struct {
		Provider string `json:"provider"`
		MediaID  int    `json:"mediaId"`
		ID       string `json:"id"`
		Title    string `json:"title"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if body.Provider == "" || body.MediaID <= 0 || body.ID == "" {
		return nil, badRequest("provider, mediaId and id are required")
	}
	s.app.Manga.SetMapping(body.Provider, body.MediaID, manga.Mapping{ID: body.ID, Title: body.Title, Score: 1})
	return nil, nil
}

func (s *Server) mangaProgress(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var body struct {
		Chapter string `json:"chapter"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if err := s.app.Manga.MarkRead(r.Context(), id, body.Chapter); err != nil {
		return nil, err
	}
	return nil, nil
}
