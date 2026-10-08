//go:build !windows

package update

import "golang.org/x/sys/unix"

// writable reports whether Kumo may create and rename files in dir.
func writable(dir string) bool {
	return unix.Access(dir, unix.W_OK|unix.X_OK) == nil
}
