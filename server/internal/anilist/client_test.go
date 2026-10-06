package anilist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient()
	c.endpoint = srv.URL
	return c
}

func TestRetryAfter(t *testing.T) {
	for header, want := range map[string]time.Duration{
		"":     5 * time.Second,
		"3":    3 * time.Second,
		" 7 ":  7 * time.Second,
		"0":    5 * time.Second,
		"-2":   5 * time.Second,
		"soon": 5 * time.Second,
		"600":  65 * time.Second,
	} {
		h := http.Header{}
		if header != "" {
			h.Set("Retry-After", header)
		}
		if got := retryAfter(h); got != want {
			t.Errorf("Retry-After %q: wait %v, want %v", header, got, want)
		}
	}
}

// When AniList asks for a longer wait than the caller's deadline allows,
// give up right away instead of sleeping until the deadline.
func TestRateLimitBeyondDeadline(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	err := c.Query(ctx, qViewer, nil, nil)
	if !errors.Is(err, errRateLimited) {
		t.Fatalf("err = %v, want the rate-limit error", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v: waited for a retry that couldn't happen in time", d)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

// The wait between rate-limited attempts ends when the caller gives up.
func TestRateLimitWaitHonoursCancel(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	err := c.Query(ctx, qViewer, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v after the context was canceled", d)
	}
}

func TestRejectedToken(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"401": func(w http.ResponseWriter, r *http.Request) {
			reply(w, http.StatusUnauthorized, map[string]any{"data": nil, "errors": []any{map[string]any{"message": "Unauthorized.", "status": 401}}})
		},
		"invalid token": func(w http.ResponseWriter, r *http.Request) {
			reply(w, http.StatusBadRequest, rejectedToken)
		},
	} {
		c := testClient(t, h)
		c.SetToken("expired")
		if _, err := c.Viewer(context.Background()); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: err = %v, want ErrUnauthorized", name, err)
		}
	}
}
