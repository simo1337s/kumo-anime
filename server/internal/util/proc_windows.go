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
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	createNewProcessGroup  = 0x00000200
	detachedProcess        = 0x00000008
	createBreakawayFromJob = 0x01000000
	processSuspendResume   = 0x0800
)

// BindChildren puts Kumo in a job object that ends every process in it when
// it closes, which it does when Kumo exits, also when it's killed (the
// desktop app can't do anything else on Windows) or crashes: the ffmpeg,
// ani-cli and mpv processes it started end with it. The handle stays open
// for as long as Kumo runs. Detached programs (a torrent client, Explorer)
// break away from the job.
func BindChildren() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	return nil
}

func detachAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess | createBreakawayFromJob}
}

// startDetached starts a detached command, and returns the one started.
// Breaking away from Kumo's job fails when a job Kumo runs in doesn't allow
// it: then it starts in the job.
func startDetached(cmd *exec.Cmd) (*exec.Cmd, error) {
	err := cmd.Start()
	if err == nil {
		return cmd, nil
	}
	retry := exec.Command(cmd.Path, cmd.Args[1:]...)
	retry.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
	if retry.Start() == nil {
		return retry, nil
	}
	return nil, err
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
