//go:build windows

package util

import (
	"os/exec"
	"syscall"
)

const createNewConsole = 0x00000010

// StartInConsole starts a console program in a window of its own, where the
// user follows it and answers it. It keeps running when Kumo quits: it
// breaks away from Kumo's job (see BindChildren) when that's allowed.
func StartInConsole(name string, args ...string) (*exec.Cmd, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole | createBreakawayFromJob}
	err := cmd.Start()
	if err == nil {
		return cmd, nil
	}
	retry := exec.Command(name, args...)
	retry.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	if retry.Start() == nil {
		return retry, nil
	}
	return nil, err
}
