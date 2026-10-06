// Package torrent integrates torrent clients (qBittorrent, Transmission),
// torrent search providers and the RSS auto downloader.
package torrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/config"
)

// Torrent is a torrent as reported by a client.
type Torrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Size        int64   `json:"size"`
	Progress    float64 `json:"progress"` // 0-1
	DownSpeed   int64   `json:"downSpeed"`
	UpSpeed     int64   `json:"upSpeed"`
	ETA         int64   `json:"eta"`   // seconds, -1 unknown
	State       string  `json:"state"` // downloading | seeding | paused | stalled | checking | queued | error | completed | metadata
	Seeds       int     `json:"seeds"`
	Peers       int     `json:"peers"`
	SavePath    string  `json:"savePath"`
	ContentPath string  `json:"contentPath"`
	AddedOn     int64   `json:"addedOn"`
	Ratio       float64 `json:"ratio"`
}

type Client interface {
	Name() string
	Version(ctx context.Context) (string, error)
	List(ctx context.Context) ([]Torrent, error)
	Add(ctx context.Context, uris []string, savePath string) error
	Pause(ctx context.Context, hashes []string) error
	Resume(ctx context.Context, hashes []string) error
	Remove(ctx context.Context, hashes []string, deleteFiles bool) error
}

var ErrNoClient = errors.New("no torrent client configured (Settings › Torrent Client)")

func baseURL(c config.TorrentClientConfig) string {
	scheme := "http"
	if c.UseHTTPS {
		scheme = "https"
	}
	host := strings.TrimSpace(c.Host)
	host = strings.TrimPrefix(strings.TrimPrefix(host, "http://"), "https://")
	host = strings.TrimSuffix(host, "/")
	if c.Port > 0 && !strings.Contains(host, ":") {
		host = fmt.Sprintf("%s:%d", host, c.Port)
	}
	return scheme + "://" + host
}

// ---------------------------------------------------------------------------
// qBittorrent (WebUI API v2)

type Qbittorrent struct {
	cfg    config.TorrentClientConfig
	http   *http.Client
	mu     sync.Mutex
	logged bool
}

func NewQbittorrent(cfg config.TorrentClientConfig) *Qbittorrent {
	jar, _ := cookiejar.New(nil)
	return &Qbittorrent{cfg: cfg, http: &http.Client{Jar: jar, Timeout: 15 * time.Second}}
}

func (q *Qbittorrent) Name() string { return "qbittorrent" }

func (q *Qbittorrent) login(ctx context.Context, force bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.logged && !force {
		return nil
	}
	form := url.Values{"username": {q.cfg.Username}, "password": {q.cfg.Password}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(q.cfg)+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", baseURL(q.cfg))
	resp, err := q.http.Do(req)
	if err != nil {
		return fmt.Errorf("qBittorrent unreachable at %s (is the Web UI enabled?): %w", baseURL(q.cfg), err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusForbidden {
		return errors.New("qBittorrent: login forbidden (too many failed attempts?)")
	}
	if !strings.Contains(string(body), "Ok") && resp.StatusCode != http.StatusNoContent {
		return errors.New("qBittorrent: wrong username or password")
	}
	q.logged = true
	return nil
}

func (q *Qbittorrent) do(ctx context.Context, method, path string, form url.Values, out any) error {
	if err := q.login(ctx, false); err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		var body io.Reader
		u := baseURL(q.cfg) + path
		if method == http.MethodGet && form != nil {
			u += "?" + form.Encode()
		} else if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, _ := http.NewRequestWithContext(ctx, method, u, body)
		if body != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set("Referer", baseURL(q.cfg))
		resp, err := q.http.Do(req)
		if err != nil {
			return err
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden && attempt == 0 {
			if err := q.login(ctx, true); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			return errNotFound
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("qBittorrent %s: %s %s", path, resp.Status, strings.TrimSpace(string(raw)))
		}
		if out != nil {
			if s, ok := out.(*string); ok {
				*s = string(raw)
				return nil
			}
			return json.Unmarshal(raw, out)
		}
		return nil
	}
	return errors.New("qBittorrent: authentication failed")
}

var errNotFound = errors.New("not found")

func (q *Qbittorrent) Version(ctx context.Context) (string, error) {
	var v string
	err := q.do(ctx, http.MethodGet, "/api/v2/app/version", nil, &v)
	return strings.TrimSpace(v), err
}

func (q *Qbittorrent) List(ctx context.Context) ([]Torrent, error) {
	var raw []struct {
		Hash        string  `json:"hash"`
		Name        string  `json:"name"`
		Size        int64   `json:"size"`
		Progress    float64 `json:"progress"`
		Dlspeed     int64   `json:"dlspeed"`
		Upspeed     int64   `json:"upspeed"`
		Eta         int64   `json:"eta"`
		State       string  `json:"state"`
		NumSeeds    int     `json:"num_seeds"`
		NumLeechs   int     `json:"num_leechs"`
		SavePath    string  `json:"save_path"`
		ContentPath string  `json:"content_path"`
		AddedOn     int64   `json:"added_on"`
		Ratio       float64 `json:"ratio"`
	}
	form := url.Values{"filter": {"all"}, "sort": {"added_on"}, "reverse": {"true"}}
	if err := q.do(ctx, http.MethodGet, "/api/v2/torrents/info", form, &raw); err != nil {
		return nil, err
	}
	out := make([]Torrent, 0, len(raw))
	for _, t := range raw {
		eta := t.Eta
		if eta >= 8640000 {
			eta = -1
		}
		out = append(out, Torrent{
			Hash: t.Hash, Name: t.Name, Size: t.Size, Progress: t.Progress, DownSpeed: t.Dlspeed, UpSpeed: t.Upspeed,
			ETA: eta, State: qbitState(t.State), Seeds: t.NumSeeds, Peers: t.NumLeechs, SavePath: t.SavePath,
			ContentPath: t.ContentPath, AddedOn: t.AddedOn, Ratio: t.Ratio,
		})
	}
	return out, nil
}

func qbitState(s string) string {
	switch s {
	case "downloading", "forcedDL":
		return "downloading"
	case "metaDL", "forcedMetaDL":
		return "metadata"
	case "uploading", "forcedUP", "stalledUP":
		return "seeding"
	case "pausedDL", "stoppedDL":
		return "paused"
	case "pausedUP", "stoppedUP":
		return "completed"
	case "stalledDL":
		return "stalled"
	case "checkingDL", "checkingUP", "checkingResumeData", "moving":
		return "checking"
	case "queuedDL", "queuedUP":
		return "queued"
	case "error", "missingFiles":
		return "error"
	}
	return s
}

func (q *Qbittorrent) Add(ctx context.Context, uris []string, savePath string) error {
	form := url.Values{"urls": {strings.Join(uris, "\n")}}
	if savePath != "" {
		form.Set("savepath", savePath)
	}
	if q.cfg.Category != "" {
		form.Set("category", q.cfg.Category)
	}
	if q.cfg.Tags != "" {
		form.Set("tags", q.cfg.Tags)
	}
	return q.do(ctx, http.MethodPost, "/api/v2/torrents/add", form, nil)
}

func (q *Qbittorrent) action(ctx context.Context, names []string, hashes []string, extra url.Values) error {
	form := url.Values{"hashes": {strings.Join(hashes, "|")}}
	for k, v := range extra {
		form[k] = v
	}
	var err error
	for _, n := range names {
		err = q.do(ctx, http.MethodPost, "/api/v2/torrents/"+n, form, nil)
		if !errors.Is(err, errNotFound) {
			return err
		}
	}
	return err
}

// qBittorrent 5 renamed pause/resume to stop/start.
func (q *Qbittorrent) Pause(ctx context.Context, h []string) error {
	return q.action(ctx, []string{"stop", "pause"}, h, nil)
}
func (q *Qbittorrent) Resume(ctx context.Context, h []string) error {
	return q.action(ctx, []string{"start", "resume"}, h, nil)
}
func (q *Qbittorrent) Remove(ctx context.Context, h []string, del bool) error {
	return q.action(ctx, []string{"delete"}, h, url.Values{"deleteFiles": {fmt.Sprint(del)}})
}

// ---------------------------------------------------------------------------
// Transmission (RPC)

type Transmission struct {
	cfg     config.TorrentClientConfig
	http    *http.Client
	mu      sync.Mutex
	session string
}

func NewTransmission(cfg config.TorrentClientConfig) *Transmission {
	return &Transmission{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
}

func (t *Transmission) Name() string { return "transmission" }

func (t *Transmission) rpc(ctx context.Context, method string, args any, out any) error {
	body, _ := json.Marshal(map[string]any{"method": method, "arguments": args})
	for attempt := 0; attempt < 2; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(t.cfg)+"/transmission/rpc", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		t.mu.Lock()
		req.Header.Set("X-Transmission-Session-Id", t.session)
		t.mu.Unlock()
		if t.cfg.Username != "" {
			req.SetBasicAuth(t.cfg.Username, t.cfg.Password)
		}
		resp, err := t.http.Do(req)
		if err != nil {
			return fmt.Errorf("Transmission unreachable at %s: %w", baseURL(t.cfg), err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			t.mu.Lock()
			t.session = resp.Header.Get("X-Transmission-Session-Id")
			t.mu.Unlock()
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return errors.New("Transmission: wrong username or password")
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("Transmission: %s", resp.Status)
		}
		var r struct {
			Result    string          `json:"result"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if r.Result != "success" {
			return fmt.Errorf("Transmission: %s", r.Result)
		}
		if out != nil {
			return json.Unmarshal(r.Arguments, out)
		}
		return nil
	}
	return errors.New("Transmission: could not obtain session id")
}

func (t *Transmission) Version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	err := t.rpc(ctx, "session-get", map[string]any{"fields": []string{"version"}}, &out)
	return out.Version, err
}

func (t *Transmission) List(ctx context.Context) ([]Torrent, error) {
	var out struct {
		Torrents []struct {
			HashString         string  `json:"hashString"`
			Name               string  `json:"name"`
			TotalSize          int64   `json:"totalSize"`
			PercentDone        float64 `json:"percentDone"`
			RateDownload       int64   `json:"rateDownload"`
			RateUpload         int64   `json:"rateUpload"`
			Eta                int64   `json:"eta"`
			Status             int     `json:"status"`
			Error              int     `json:"error"`
			PeersSendingToUs   int     `json:"peersSendingToUs"`
			PeersGettingFromUs int     `json:"peersGettingFromUs"`
			DownloadDir        string  `json:"downloadDir"`
			AddedDate          int64   `json:"addedDate"`
			UploadRatio        float64 `json:"uploadRatio"`
		} `json:"torrents"`
	}
	fields := []string{"hashString", "name", "totalSize", "percentDone", "rateDownload", "rateUpload", "eta", "status", "error",
		"peersSendingToUs", "peersGettingFromUs", "downloadDir", "addedDate", "uploadRatio"}
	if err := t.rpc(ctx, "torrent-get", map[string]any{"fields": fields}, &out); err != nil {
		return nil, err
	}
	res := make([]Torrent, 0, len(out.Torrents))
	for _, x := range out.Torrents {
		state := "queued"
		switch x.Status {
		case 0:
			state = "paused"
			if x.PercentDone >= 1 {
				state = "completed"
			}
		case 1, 2:
			state = "checking"
		case 3, 5:
			state = "queued"
		case 4:
			state = "downloading"
			if x.RateDownload == 0 {
				state = "stalled"
			}
		case 6:
			state = "seeding"
		}
		if x.Error != 0 {
			state = "error"
		}
		res = append(res, Torrent{
			Hash: x.HashString, Name: x.Name, Size: x.TotalSize, Progress: x.PercentDone, DownSpeed: x.RateDownload,
			UpSpeed: x.RateUpload, ETA: x.Eta, State: state, Seeds: x.PeersSendingToUs, Peers: x.PeersGettingFromUs,
			SavePath: x.DownloadDir, ContentPath: x.DownloadDir + "/" + x.Name, AddedOn: x.AddedDate, Ratio: x.UploadRatio,
		})
	}
	return res, nil
}

func (t *Transmission) Add(ctx context.Context, uris []string, savePath string) error {
	for _, u := range uris {
		args := map[string]any{"filename": u}
		if savePath != "" {
			args["download-dir"] = savePath
		}
		if t.cfg.Tags != "" {
			args["labels"] = strings.Split(t.cfg.Tags, ",")
		}
		if err := t.rpc(ctx, "torrent-add", args, nil); err != nil {
			return err
		}
	}
	return nil
}

func (t *Transmission) Pause(ctx context.Context, h []string) error {
	return t.rpc(ctx, "torrent-stop", map[string]any{"ids": h}, nil)
}
func (t *Transmission) Resume(ctx context.Context, h []string) error {
	return t.rpc(ctx, "torrent-start", map[string]any{"ids": h}, nil)
}
func (t *Transmission) Remove(ctx context.Context, h []string, del bool) error {
	return t.rpc(ctx, "torrent-remove", map[string]any{"ids": h, "delete-local-data": del}, nil)
}
