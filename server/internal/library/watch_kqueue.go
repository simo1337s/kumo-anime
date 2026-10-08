//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package library

import "syscall"

// watchBudget is how many files and folders the watcher may watch: kqueue
// opens each one, so half of the files Kumo may have open (Go raises that
// limit at startup), the rest staying for its connections and its own
// files.
var watchBudget = func() int {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil || lim.Cur < 2048 {
		return 1024
	}
	return int(min(lim.Cur/2, 1<<20))
}
