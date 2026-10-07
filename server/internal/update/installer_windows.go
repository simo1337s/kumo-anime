//go:build windows

package update

import (
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup  = 0x00000200
	detachedProcess        = 0x00000008
	createBreakawayFromJob = 0x01000000
)

// startInstaller starts the installer on its own, outside Kumo's job object
// (util.BindChildren): everything in that job ends when Kumo exits, which
// would stop the installer in the middle of installing. util.Detach starts
// a program in the job when it can't break away; an installer must not be,
// so then this fails, and the user runs it.
func startInstaller(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess | createBreakawayFromJob}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
