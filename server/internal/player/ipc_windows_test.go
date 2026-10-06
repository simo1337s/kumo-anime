//go:build windows

package player

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Microsoft/go-winio"
)

func listenIPC(addr string) (net.Listener, error) { return winio.ListenPipe(addr, nil) }

// testIPCAddress is a named pipe unique to the test directory.
func testIPCAddress(dir, name string) string {
	return fmt.Sprintf(`\\.\pipe\kumo-test-%d-%d-%s-%s`, os.Getpid(), time.Now().UnixNano(), filepath.Base(dir), name)
}

// leftoverIPC: named pipes go away with their last handle.
func leftoverIPC(string) []string { return nil }
