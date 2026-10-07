//go:build !windows

package util

import (
	"errors"
	"os/exec"
)

// StartInConsole is for Windows, where it opens a console window for a
// program the user follows.
func StartInConsole(name string, args ...string) (*exec.Cmd, error) {
	return nil, errors.ErrUnsupported
}
