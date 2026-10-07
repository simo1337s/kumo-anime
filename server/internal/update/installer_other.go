//go:build !windows

package update

import "errors"

// startInstaller is for Windows: elsewhere Kumo updates without an
// installer.
func startInstaller(string, ...string) error {
	return errors.New("installers only run on Windows")
}
