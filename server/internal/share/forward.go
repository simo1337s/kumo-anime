package share

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/simo1337s/animetest/server/internal/stream"
)

// A host's files are played through this Kumo's server: the player asks it
// as for a local file, and it forwards the request to the host (signed),
// and the answer back. The host converts the video for this Kumo's player
// when needed, like for one of its own.

var errNotShared = errors.New("that library isn't shared with this Kumo any more")

// host is a Kumo that shares its library with this one.
func (s *Service) host(id string) *peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.peers[id]
	if p == nil || !p.shares || p.addr == nil || p.port == 0 {
		return nil
	}
	return p
}

// hostOf is the host of a shared file, and the file's path there.
func (s *Service) hostOf(path string) (*peer, string, error) {
	host, onHost, ok := ParsePath(path)
	if !ok {
		return nil, "", errors.New("not a shared file")
	}
	p := s.host(host)
	if p == nil {
		return nil, "", errNotShared
	}
	return p, onHost, nil
}

// Forward forwards a player's request about a shared file (GET
// /api/local/<endpoint>?path=kumo://…) to its host.
func (s *Service) Forward(w http.ResponseWriter, r *http.Request, endpoint string) {
	q := r.URL.Query()
	p, onHost, err := s.hostOf(q.Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	q.Set("path", onHost)
	s.forward(w, r, p, http.MethodGet, "/api/peer/local/"+endpoint+"?"+q.Encode(), nil)
}

func (s *Service) forward(w http.ResponseWriter, r *http.Request, p *peer, method, path string, body []byte) {
	s.forwardEdit(w, r, p, method, path, body, nil)
}

// forwardEdit forwards a request, and passes a successful answer through
// edit when it's given (a small one: it's read whole).
func (s *Service) forwardEdit(w http.ResponseWriter, r *http.Request, p *peer, method, path string, body []byte, edit func([]byte) []byte) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := s.request(r.Context(), p, method, path, rd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.media.Do(req)
	if err != nil {
		if r.Context().Err() == nil {
			http.Error(w, fmt.Sprintf("%s doesn't answer: %v", s.nameOf(p), err), http.StatusBadGateway)
		}
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag", "Cache-Control"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", stream.SafeContentType(resp.Header.Get("Content-Type")))
	if edit != nil && resp.StatusCode == http.StatusOK {
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		data = edit(data)
		w.Header().Del("Content-Range")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(data)
		return
	}
	w.WriteHeader(resp.StatusCode)
	copyFlushing(w, resp.Body)
}

// copyFlushing copies a stream as it comes (a video being converted comes
// slowly at first).
func copyFlushing(w http.ResponseWriter, r io.Reader) {
	rc := http.NewResponseController(w)
	buf := make([]byte, 128<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// Probe asks a shared file's host what's in it.
func (s *Service) Probe(ctx context.Context, path string) (*stream.Probe, error) {
	p, onHost, err := s.hostOf(path)
	if err != nil {
		return nil, err
	}
	var pr stream.Probe
	if err := s.getJSON(ctx, p, "/api/peer/local/probe?"+url.Values{"path": {onHost}}.Encode(), &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// MediaURL is where mpv plays a shared file from, and the headers it sends.
func (s *Service) MediaURL(path string) (string, map[string]string, error) {
	p, onHost, err := s.hostOf(path)
	if err != nil {
		return "", nil, err
	}
	req, err := s.request(context.Background(), p, http.MethodGet, "/api/peer/local/file?"+url.Values{"path": {onHost}}.Encode(), nil)
	if err != nil {
		return "", nil, err
	}
	headers := map[string]string{}
	for k := range req.Header {
		headers[k] = req.Header.Get(k)
	}
	return req.URL.String(), headers, nil
}

// HLS sessions on a host are named <host>.<its session ID> here.
var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func remoteSession(id string) (host, session string, ok bool) {
	host, session, ok = strings.Cut(id, ".")
	return host, session, ok && sessionID.MatchString(host) && sessionID.MatchString(session)
}

// IsRemoteSession reports an HLS session on another Kumo.
func IsRemoteSession(id string) bool {
	_, _, ok := remoteSession(id)
	return ok
}

// StartHLS starts an HLS session for a shared file on its host.
func (s *Service) StartHLS(ctx context.Context, req stream.HLSRequest) (*stream.HLSSession, error) {
	p, onHost, err := s.hostOf(req.Path)
	if err != nil {
		return nil, err
	}
	req.Path = onHost
	body, _ := json.Marshal(req)
	hr, err := s.request(ctx, p, http.MethodPost, "/api/peer/local/hls", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := s.media.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("%s doesn't answer: %w", s.nameOf(p), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return nil, errors.New(e.Error)
	}
	var sess stream.HLSSession
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&sess); err != nil {
		return nil, err
	}
	if !sessionID.MatchString(sess.ID) {
		return nil, errors.New("bad session from " + s.nameOf(p))
	}
	id := p.id + "." + sess.ID
	sess.URL = strings.Replace(sess.URL, "/api/local/hls/"+sess.ID+"/", "/api/local/hls/"+id+"/", 1)
	sess.ID = id
	return &sess, nil
}

// ForwardHLS forwards a request for an HLS session's playlist or segment.
func (s *Service) ForwardHLS(w http.ResponseWriter, r *http.Request, id, name string) {
	host, session, ok := remoteSession(id)
	p := s.host(host)
	if !ok || p == nil {
		http.Error(w, errNotShared.Error(), http.StatusNotFound)
		return
	}
	var edit func([]byte) []byte
	if strings.HasSuffix(name, ".m3u8") {
		// The playlist names its segments by the host's session ID.
		edit = func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte("/api/local/hls/"+session+"/"), []byte("/api/local/hls/"+id+"/"))
		}
	}
	s.forwardEdit(w, r, p, http.MethodGet, "/api/peer/local/hls/"+url.PathEscape(session)+"/"+url.PathEscape(name), nil, edit)
}

// StopHLS ends an HLS session on its host.
func (s *Service) StopHLS(ctx context.Context, id string) {
	host, session, ok := remoteSession(id)
	p := s.host(host)
	if !ok || p == nil {
		return
	}
	req, err := s.request(ctx, p, http.MethodDelete, "/api/peer/local/hls/"+url.PathEscape(session), nil)
	if err != nil {
		return
	}
	if resp, err := s.api.Do(req); err == nil {
		resp.Body.Close()
	}
}
