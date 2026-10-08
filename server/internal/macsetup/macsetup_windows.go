package macsetup

import "errors"

// ErrRunning means the setup's Terminal window is still open.
var ErrRunning = errors.New("the setup is already running: see its Terminal window")

// Running reports whether the setup is open: never on Windows.
func Running() bool { return false }

// Start opens the setup: macOS only (Windows has winsetup).
func Start() error { return errors.New("this setup is for macOS: Windows has its own") }
