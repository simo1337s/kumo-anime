//go:build !windows

package main

import (
	"os"
	"strings"
	"syscall"
)

// canReexec: the server can replace itself with a new run of its program.
const canReexec = true

// reexec runs Kumo's program again in this process, with the same
// arguments and environment, so it keeps its PID (systemd sees no exit)
// and its terminal. Kumo's own files and sockets close on exec. It only
// returns when that fails.
func reexec() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Where the old program was replaced, Linux names it "<path> (deleted)".
	exe = strings.TrimSuffix(exe, " (deleted)")
	return syscall.Exec(exe, os.Args, os.Environ())
}
