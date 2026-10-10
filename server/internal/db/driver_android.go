//go:build android

package db

import _ "github.com/mattn/go-sqlite3"

// On Android, SQLite in C with Android's own C library: the pure Go one makes
// system calls that older Android versions (a Fire TV's) forbid, and Android
// ends a program that makes them (statx, on 32-bit ARM).
const driver = "sqlite3"

func source(path string) string {
	return "file:" + path + "?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=1&_synchronous=NORMAL"
}
