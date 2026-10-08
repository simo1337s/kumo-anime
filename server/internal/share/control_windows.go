package share

import "syscall"

// reuseAddr does nothing on Windows, where SO_REUSEADDR would let another
// program take the port over: a second Kumo on the same computer isn't
// found.
func reuseAddr(_, _ string, _ syscall.RawConn) error { return nil }
