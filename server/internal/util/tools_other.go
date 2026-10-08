//go:build !darwin

package util

// addToolsDir: only macOS has Kumo put programs in its own folder.
func addToolsDir(string) {}
