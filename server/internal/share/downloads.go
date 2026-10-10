package share

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// A host can let a Kumo it shares its library with download onto it: the
// episodes and torrents that Kumo downloads go to the host's library (and
// come back shared). Made for a TV, which has no room for them, nor ani-cli
// or a torrent client. When a host allows it, it becomes where that Kumo's
// downloads go (Settings › Library sharing can change it back); when it
// stops, they're saved on that Kumo again.

// What a guest sends a host to download.
const (
	DownloadStream  = "stream"  // episodes from a streaming provider (ani-cli, an extension)
	DownloadTorrent = "torrent" // magnet links or .torrent URLs
)

// StreamDownload is episodes to download from a streaming provider.
type StreamDownload struct {
	Provider string    `json:"provider"`
	MediaID  int       `json:"mediaId"`
	Episodes []float64 `json:"episodes"`
	Dub      bool      `json:"dub"`
	Quality  string    `json:"quality"`
}

// TorrentDownload is torrents to add to the host's torrent client.
type TorrentDownload struct {
	MediaID int      `json:"mediaId"`
	URIs    []string `json:"uris"`
}

// downloadsGranted follows a host allowing this Kumo to download onto it,
// or no longer.
func (s *Service) downloadsGranted(p *peer, takes bool) {
	cfg := s.settings.Get()
	name := s.nameOf(p)
	switch {
	case takes && cfg.Sharing.DownloadTo != p.id:
		cfg.Sharing.DownloadTo = p.id
		if _, err := s.settings.Save(cfg); err != nil {
			log.Printf("sharing: %v", err)
			return
		}
		s.hub.Info("Downloads from this device now go to " + name + ".")
	case !takes && cfg.Sharing.DownloadTo == p.id:
		cfg.Sharing.DownloadTo = ""
		if _, err := s.settings.Save(cfg); err != nil {
			log.Printf("sharing: %v", err)
			return
		}
		s.hub.Warn(name + " no longer takes downloads from this device: they're saved here again.")
	}
}

// DownloadHost is the Kumo this one's downloads go to: its name ("" when
// they're saved here). An error when it's set but can't take them now.
func (s *Service) DownloadHost() (string, error) {
	id := s.settings.Get().Sharing.DownloadTo
	if id == "" {
		return "", nil
	}
	s.mu.Lock()
	p := s.peers[id]
	ok := p != nil && p.shares && p.hostDownloads
	name := ""
	if p != nil {
		name = p.name
	}
	s.mu.Unlock()
	if !ok {
		if name == "" {
			name = "The other Kumo"
		}
		return "", fmt.Errorf("%s doesn't take downloads from this device any more (Settings › Library sharing)", name)
	}
	return name, nil
}

// SendDownload has the Kumo this one's downloads go to download something
// (a StreamDownload or a TorrentDownload), and returns its name.
func (s *Service) SendDownload(ctx context.Context, kind string, what any) (string, error) {
	name, err := s.DownloadHost()
	if err != nil || name == "" {
		return "", err
	}
	p := s.host(s.settings.Get().Sharing.DownloadTo)
	if p == nil {
		return "", errNotShared
	}
	body, err := json.Marshal(what)
	if err != nil {
		return "", err
	}
	req, err := s.request(ctx, p, http.MethodPost, "/api/peer/downloads/"+kind, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.do(s.api, req)
	if err != nil {
		return "", fmt.Errorf("%s doesn't answer: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		if e.Error == "" {
			e.Error = resp.Status
		}
		return "", errors.New(name + ": " + e.Error)
	}
	return name, nil
}

// TorrentsHost asks the Kumo this one's downloads go to about its torrent
// client (op: status, list, action, start; GET without body, else POST):
// its torrents are this one's then, a TV's are its computer's. ok is false
// when the downloads are this one's own.
func (s *Service) TorrentsHost(ctx context.Context, op string, body any) (raw json.RawMessage, host string, ok bool, err error) {
	name, err := s.DownloadHost()
	if err != nil {
		return nil, name, true, err
	}
	if name == "" {
		return nil, "", false, nil
	}
	p := s.host(s.settings.Get().Sharing.DownloadTo)
	if p == nil {
		return nil, name, true, errNotShared
	}
	method := http.MethodGet
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, name, true, err
		}
		method, rd = http.MethodPost, bytes.NewReader(b)
	}
	req, err := s.request(ctx, p, method, "/api/peer/torrents/"+op, rd)
	if err != nil {
		return nil, name, true, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.do(s.api, req)
	if err != nil {
		return nil, name, true, fmt.Errorf("%s doesn't answer: %w", name, err)
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, name, true, err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		if e.Error == "" {
			e.Error = resp.Status
		}
		return nil, name, true, errors.New(name + ": " + e.Error)
	}
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	return raw, name, true, nil
}
