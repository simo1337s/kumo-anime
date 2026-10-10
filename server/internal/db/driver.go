//go:build !android

package db

import _ "modernc.org/sqlite"

// SQLite in pure Go: no C compiler needed for any of the systems.
const driver = "sqlite"

func source(path string) string {
	return "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
}
