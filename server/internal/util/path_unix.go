//go:build !windows

package util

import "os/exec"

// lookBare finds a program by name on the PATH.
func lookBare(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}
