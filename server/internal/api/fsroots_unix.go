//go:build !windows

package api

// fsRoots lists the roots the folder picker offers besides parent folders:
// none, everything is below /.
func fsRoots() []string { return nil }
