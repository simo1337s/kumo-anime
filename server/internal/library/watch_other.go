//go:build !(darwin || freebsd || netbsd || openbsd || dragonfly)

package library

// watchBudget is how many files and folders the watcher may watch: no limit
// here (inotify and Windows don't open the files they watch).
var watchBudget = func() int { return 0 }
