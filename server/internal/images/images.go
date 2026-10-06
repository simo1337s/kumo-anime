// Package images downloads artwork (covers, banners, fanart, episode
// thumbnails) once and serves it from disk, so the library looks complete
// even offline and pages load instantly.
package images

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/simo1337s/animetest/server/internal/util"
)

type Cache struct {
	dir    string
	client *http.Client

	mu       sync.Mutex
	inflight map[string]chan struct{}
	queue    chan string
	queued   sync.Map
}

func New(dir string) *Cache {
	_ = os.MkdirAll(dir, 0o755)
	c := &Cache{
		dir: dir,
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: util.PublicDialContext(10 * time.Second), MaxIdleConnsPerHost: 8},
		},
		inflight: map[string]chan struct{}{},
		queue:    make(chan string, 4096),
	}
	for i := 0; i < 4; i++ {
		go c.worker()
	}
	return c
}

func key(u string) string {
	sum := sha1.Sum([]byte(u))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) path(u string) string {
	k := key(u)
	return filepath.Join(c.dir, k[:2], k)
}

func valid(u string) bool {
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "http" || p.Scheme == "https") && p.Host != "" && !strings.EqualFold(p.Hostname(), "localhost")
}

// fetch downloads u into the cache (deduplicating concurrent requests).
func (c *Cache) fetch(ctx context.Context, u string) (string, error) {
	p := c.path(u)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	c.mu.Lock()
	if ch, ok := c.inflight[u]; ok {
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", errors.New("download failed")
	}
	ch := make(chan struct{})
	c.inflight[u] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.inflight, u)
		c.mu.Unlock()
		close(ch)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", util.UserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/*,*/*;q=0.8")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "application/octet-stream") && !strings.HasPrefix(ct, "binary/") {
		return "", errors.New("not an image: " + ct)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".dl-*")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, 20<<20))
	_ = tmp.Close()
	if err != nil || n == 0 {
		_ = os.Remove(tmp.Name())
		if err == nil {
			err = errors.New("empty image")
		}
		return "", err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return p, nil
}

// ServeHTTP implements GET /api/img?u=<url>.
func (c *Cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	if !valid(u) {
		http.Error(w, "invalid image url", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	p, err := c.fetch(ctx, u)
	if err != nil {
		if errors.Is(err, util.ErrPrivateAddress) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		// Fall back to letting the browser load the original.
		http.Redirect(w, r, u, http.StatusTemporaryRedirect)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// Prefetch downloads images in the background (skipping cached ones).
func (c *Cache) Prefetch(urls ...string) {
	for _, u := range urls {
		if !valid(u) {
			continue
		}
		if _, err := os.Stat(c.path(u)); err == nil {
			continue
		}
		if _, loaded := c.queued.LoadOrStore(u, true); loaded {
			continue
		}
		select {
		case c.queue <- u:
		default:
			c.queued.Delete(u)
		}
	}
}

func (c *Cache) worker() {
	for u := range c.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		_, _ = c.fetch(ctx, u)
		cancel()
		c.queued.Delete(u)
	}
}

type Stats struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

func (c *Cache) Stats() Stats {
	var s Stats
	_ = filepath.WalkDir(c.dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				s.Files++
				s.Bytes += info.Size()
			}
		}
		return nil
	})
	return s
}

func (c *Cache) Clear() error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(c.dir, e.Name()))
	}
	return nil
}
