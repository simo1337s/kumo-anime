// Package db wraps the SQLite database that holds every piece of persistent
// state Kumo keeps on disk (settings, library files, watch history, caches,
// installed extensions, downloads...).
package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type DB struct {
	*sql.DB
	mu sync.Mutex // serialises writes; SQLite only allows one writer anyway
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS kv (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS cache (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		expires_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS local_files (
		path TEXT PRIMARY KEY,
		dir TEXT NOT NULL,
		name TEXT NOT NULL,
		size INTEGER NOT NULL DEFAULT 0,
		mod_time INTEGER NOT NULL DEFAULT 0,
		parsed TEXT NOT NULL DEFAULT '{}',
		media_id INTEGER NOT NULL DEFAULT 0,
		episode INTEGER NOT NULL DEFAULT 0,
		aired_episode INTEGER NOT NULL DEFAULT 0,
		kind TEXT NOT NULL DEFAULT 'main',
		locked INTEGER NOT NULL DEFAULT 0,
		ignored INTEGER NOT NULL DEFAULT 0,
		match_score REAL NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_local_files_media ON local_files(media_id)`,
	`CREATE TABLE IF NOT EXISTS watch_history (
		media_id INTEGER NOT NULL,
		episode INTEGER NOT NULL,
		position REAL NOT NULL DEFAULT 0,
		duration REAL NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (media_id, episode)
	)`,
	`CREATE TABLE IF NOT EXISTS track_prefs (
		media_id INTEGER PRIMARY KEY,
		audio_lang TEXT NOT NULL DEFAULT '',
		audio_title TEXT NOT NULL DEFAULT '',
		audio_index INTEGER NOT NULL DEFAULT 0,
		sub_lang TEXT NOT NULL DEFAULT '',
		sub_title TEXT NOT NULL DEFAULT '',
		sub_index INTEGER NOT NULL DEFAULT 0,
		sub_off INTEGER NOT NULL DEFAULT 0,
		stream_mode TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS local_list (
		media_id INTEGER PRIMARY KEY,
		type TEXT NOT NULL DEFAULT 'ANIME',
		status TEXT NOT NULL DEFAULT 'PLANNING',
		progress INTEGER NOT NULL DEFAULT 0,
		score REAL NOT NULL DEFAULT 0,
		repeat INTEGER NOT NULL DEFAULT 0,
		media TEXT NOT NULL DEFAULT '{}',
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS extensions (
		id TEXT PRIMARY KEY,
		manifest TEXT NOT NULL,
		payload TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		user_config TEXT NOT NULL DEFAULT '{}',
		installed_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS extension_storage (
		ext_id TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY (ext_id, key)
	)`,
	`CREATE TABLE IF NOT EXISTS downloads (
		id TEXT PRIMARY KEY,
		data TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS autodownload_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		data TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS autodownload_items (
		key TEXT PRIMARY KEY,
		rule_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS stream_mappings (
		provider TEXT NOT NULL,
		media_id INTEGER NOT NULL,
		mode TEXT NOT NULL DEFAULT '',
		value TEXT NOT NULL,
		PRIMARY KEY (provider, media_id, mode)
	)`,
	`CREATE TABLE IF NOT EXISTS manga_progress (
		media_id INTEGER NOT NULL,
		provider TEXT NOT NULL,
		chapter_id TEXT NOT NULL,
		chapter_number TEXT NOT NULL DEFAULT '',
		page INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (media_id)
	)`,
}

// Open opens (and migrates) the database at path.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	conn, err := sql.Open(driver, source(path))
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(4)
	for _, stmt := range schema {
		if _, err := conn.Exec(stmt); err != nil {
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}
	return &DB{DB: conn}, nil
}

// Exec runs a write statement while holding the writer lock.
func (d *DB) Write(query string, args ...any) (sql.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.DB.Exec(query, args...)
}

// Tx runs fn inside a write transaction.
func (d *DB) Tx(fn func(tx *sql.Tx) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.DB.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Key/value helpers

func (d *DB) GetKV(key string, out any) (bool, error) {
	var raw string
	err := d.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), out)
}

func (d *DB) SetKV(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = d.Write(`INSERT INTO kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(raw))
	return err
}

func (d *DB) DeleteKV(key string) error {
	_, err := d.Write(`DELETE FROM kv WHERE key = ?`, key)
	return err
}

// ---------------------------------------------------------------------------
// Expiring cache helpers

func (d *DB) GetCache(key string, out any) bool {
	var raw string
	var exp int64
	err := d.QueryRow(`SELECT value, expires_at FROM cache WHERE key = ?`, key).Scan(&raw, &exp)
	if err != nil {
		return false
	}
	if exp > 0 && time.Now().Unix() > exp {
		return false
	}
	return json.Unmarshal([]byte(raw), out) == nil
}

// GetStaleCache returns a cached value even if it has expired (used as an
// offline fallback when the network is unavailable).
func (d *DB) GetStaleCache(key string, out any) bool {
	var raw string
	err := d.QueryRow(`SELECT value FROM cache WHERE key = ?`, key).Scan(&raw)
	if err != nil {
		return false
	}
	return json.Unmarshal([]byte(raw), out) == nil
}

func (d *DB) SetCache(key string, value any, ttl time.Duration) {
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	var exp int64
	if ttl > 0 {
		exp = time.Now().Add(ttl).Unix()
	}
	_, _ = d.Write(`INSERT INTO cache(key, value, expires_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`, key, string(raw), exp)
}

func (d *DB) DeleteCachePrefix(prefix string) {
	_, _ = d.Write(`DELETE FROM cache WHERE key LIKE ? ESCAPE '\'`, escapeLike(prefix)+"%")
}

func (d *DB) ClearCache() error {
	_, err := d.Write(`DELETE FROM cache`)
	return err
}

func (d *DB) CacheSize() (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM cache`).Scan(&n)
	return n, err
}

func escapeLike(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '%' || r == '_' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
