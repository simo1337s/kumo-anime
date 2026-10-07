//go:build windows

package main

import "errors"

// canReexec: Windows can't replace a running program with a new one, so a
// restart is the desktop app's job (exit code lifecycle.CodeRestart).
const canReexec = false

func reexec() error { return errors.ErrUnsupported }
