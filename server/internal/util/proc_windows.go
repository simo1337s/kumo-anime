//go:build windows

package util

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
)

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
	processSuspendResume  = 0x0800
)

func detachAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
}

// OwnProcessGroup makes cmd the root of a process tree that KillGroup stops
// as a whole.
func OwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup, HideWindow: true}
}

// KillGroup kills a process and every process it started.
func KillGroup(p *os.Process) error {
	if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run() == nil {
		return nil
	}
	// taskkill failed: the process is gone, or taskkill is missing.
	if err := p.Kill(); err != nil {
		return os.ErrProcessDone
	}
	return nil
}

var (
	ntdll            = syscall.NewLazyDLL("ntdll.dll")
	ntSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	ntResumeProcess  = ntdll.NewProc("NtResumeProcess")
)

// Suspend pauses a process until Resume.
func Suspend(p *os.Process) error { return ntProcess(ntSuspendProcess, p.Pid) }

// Resume continues a process paused with Suspend.
func Resume(p *os.Process) error { return ntProcess(ntResumeProcess, p.Pid) }

func ntProcess(proc *syscall.LazyProc, pid int) error {
	if err := proc.Find(); err != nil {
		return err
	}
	h, err := syscall.OpenProcess(processSuspendResume, false, uint32(pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	if status, _, _ := proc.Call(uintptr(h)); status != 0 {
		return fmt.Errorf("%s: NTSTATUS %#x", proc.Name, status)
	}
	return nil
}

// openCommand opens a folder in Explorer, or a web address in the browser.
func openCommand(target string) *exec.Cmd {
	if isWebAddress(target) {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	}
	return exec.Command("explorer.exe", target)
}

// DialIPC connects to a local IPC endpoint: a named pipe here, a Unix socket
// elsewhere.
func DialIPC(addr string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(addr, &timeout)
}

// lookAbs checks a program given by its path (Windows has no executable
// bit: LookPath goes by the extension, and tries .exe & co. when there's
// none).
func lookAbs(p string) (string, bool) {
	found, err := exec.LookPath(p)
	return found, err == nil
}
