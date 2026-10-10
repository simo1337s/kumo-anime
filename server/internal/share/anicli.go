package share

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/simo1337s/animetest/server/internal/anicli"
)

// A Kumo where ani-cli doesn't run (Android: it's a shell script) streams
// through a computer that shares its library with it and has ani-cli: that
// one searches, lists the episodes and finds the stream (POST
// /api/peer/anicli/{op}), and this Kumo plays it. Matching an anime, its
// history and the sub/dub choice stay here.

// AniCliRequest is what a guest asks a host's ani-cli.
type AniCliRequest struct {
	Query   string `json:"query"`
	Mode    string `json:"mode"`
	Index   int    `json:"index,omitempty"`
	Episode string `json:"episode,omitempty"`
	Quality string `json:"quality,omitempty"`
}

// AniCliAnswer is its answer. Error: ani-cli ran and failed (NoSources: it
// found the episode, not a stream of it).
type AniCliAnswer struct {
	Results   []anicli.Result `json:"results,omitempty"`
	Episodes  []string        `json:"episodes,omitempty"`
	Stream    *anicli.Stream  `json:"stream,omitempty"`
	Error     string          `json:"error,omitempty"`
	NoSources bool            `json:"noSources,omitempty"`
}

// AniCliOps are the requests a host answers.
const (
	AniCliSearch   = "search"
	AniCliEpisodes = "episodes"
	AniCliResolve  = "resolve"
)

// AniCliRemote runs ani-cli on a host, for anicli.Driver.SetRemote.
func (s *Service) AniCliRemote() anicli.Remote { return aniCliRemote{s} }

type aniCliRemote struct{ s *Service }

// aniCliHost is the host that runs ani-cli for this Kumo: one that shares
// with it, is around and has ani-cli. Where downloads go first.
func (s *Service) aniCliHost() *peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	ok := func(p *peer) bool {
		return p != nil && p.shares && p.hostAniCli && p.addr != nil && p.port != 0 && p.online(now)
	}
	if p := s.peers[s.settings.Get().Sharing.DownloadTo]; ok(p) {
		return p
	}
	var best *peer
	for _, p := range s.peers {
		if ok(p) && (best == nil || p.seen.After(best.seen)) {
			best = p
		}
	}
	return best
}

func (r aniCliRemote) Name() string {
	if p := r.s.aniCliHost(); p != nil {
		return r.s.nameOf(p)
	}
	return ""
}

// waitHost is the host that runs ani-cli, asking the hosts again when none
// is known (this Kumo just started, or hasn't heard from them lately) and
// waiting a little for their answers.
func (s *Service) waitHost(ctx context.Context) *peer {
	if p := s.aniCliHost(); p != nil {
		return p
	}
	// Only a Kumo something shares with can be answered.
	s.mu.Lock()
	hosts := false
	for _, p := range s.peers {
		hosts = hosts || p.shares
	}
	s.mu.Unlock()
	if !hosts {
		return nil
	}
	s.Refresh()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			return nil
		case <-tick.C:
			if p := s.aniCliHost(); p != nil {
				return p
			}
		}
	}
}

func (r aniCliRemote) call(ctx context.Context, op string, req AniCliRequest) (*AniCliAnswer, error) {
	p := r.s.waitHost(ctx)
	if p == nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if runtime.GOOS == "android" {
			return nil, errors.New("ani-cli doesn't run on Android, and no computer sharing its library with this one runs it now (is it on, with Kumo running?)")
		}
		return nil, anicli.ErrNotInstalled
	}
	body, _ := json.Marshal(req)
	hr, err := r.s.request(ctx, p, http.MethodPost, "/api/peer/anicli/"+op, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := r.s.slow.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s doesn't answer: %v", r.s.nameOf(p), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(msg, &e) == nil && e.Error != "" {
			msg = []byte(e.Error)
		}
		return nil, fmt.Errorf("%s: %s", r.s.nameOf(p), strings.TrimSpace(string(msg)))
	}
	var a AniCliAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&a); err != nil {
		return nil, err
	}
	switch {
	case a.NoSources:
		return nil, anicli.NoSources(a.Error)
	case a.Error != "":
		return nil, errors.New(a.Error)
	}
	return &a, nil
}

func (r aniCliRemote) Search(ctx context.Context, query, mode string) ([]anicli.Result, error) {
	a, err := r.call(ctx, AniCliSearch, AniCliRequest{Query: query, Mode: mode})
	if err != nil {
		return nil, err
	}
	if a.Results == nil {
		a.Results = []anicli.Result{}
	}
	return a.Results, nil
}

func (r aniCliRemote) Episodes(ctx context.Context, query string, index int, mode string) ([]string, error) {
	a, err := r.call(ctx, AniCliEpisodes, AniCliRequest{Query: query, Index: index, Mode: mode})
	if err != nil {
		return nil, err
	}
	return a.Episodes, nil
}

func (r aniCliRemote) Resolve(ctx context.Context, query string, index int, episode, mode, quality string) (*anicli.Stream, error) {
	a, err := r.call(ctx, AniCliResolve, AniCliRequest{Query: query, Index: index, Episode: episode, Mode: mode, Quality: quality})
	if err != nil {
		return nil, err
	}
	if a.Stream == nil {
		return nil, errors.New("ani-cli did not return a stream URL")
	}
	return a.Stream, nil
}

// AnswerAniCli runs a guest's request with this Kumo's ani-cli.
func AnswerAniCli(ctx context.Context, d *anicli.Driver, op string, req AniCliRequest) (*AniCliAnswer, error) {
	if !d.LocalReady() {
		return nil, anicli.ErrNotInstalled
	}
	a := &AniCliAnswer{}
	var err error
	switch op {
	case AniCliSearch:
		a.Results, err = d.Search(ctx, req.Query, req.Mode)
	case AniCliEpisodes:
		a.Episodes, err = d.Episodes(ctx, req.Query, req.Index, req.Mode)
	case AniCliResolve:
		a.Stream, err = d.Resolve(ctx, req.Query, req.Index, req.Episode, req.Mode, req.Quality)
	default:
		return nil, fmt.Errorf("unknown ani-cli request %q", op)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		a = &AniCliAnswer{Error: err.Error(), NoSources: errors.Is(err, anicli.ErrNoSources)}
	}
	return a, nil
}
