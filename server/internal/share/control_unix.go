//go:build unix

package share

import (
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// reuseAddr lets another Kumo on this computer listen for beacons too.
func reuseAddr(_, _ string, c syscall.RawConn) error {
	var err error
	_ = c.Control(func(fd uintptr) {
		err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if err == nil && runtime.GOOS != "linux" {
			// macOS and the BSDs share a UDP port only with SO_REUSEPORT.
			err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	})
	return err
}
