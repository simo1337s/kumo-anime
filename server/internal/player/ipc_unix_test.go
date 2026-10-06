//go:build !windows

package player

import (
	"net"
	"path/filepath"
)

// listenIPC listens where mpv would for its IPC.
func listenIPC(addr string) (net.Listener, error) { return net.Listen("unix", addr) }

// testIPCAddress is an IPC address for a fake mpv in dir.
func testIPCAddress(dir, name string) string { return filepath.Join(dir, name+".sock") }

// leftoverIPC lists the IPC sockets left in the runtime dir.
func leftoverIPC(runtimeDir string) []string {
	left, _ := filepath.Glob(filepath.Join(runtimeDir, "kumo", "mpv-*.sock"))
	return left
}
