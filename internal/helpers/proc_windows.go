//go:build windows

package helpers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// On Windows a helper is started in a process group of its own (so a
// CTRL_BREAK can be aimed at it alone) and put in a job object with
// KILL_ON_JOB_CLOSE, as a pane's process is in internal/session. The job is how
// a stop finds the launcher's children, and it ends them if Flockdeck dies
// without running a line of its own shutdown.

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW        = kernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJob      = kernel32.NewProc("AssignProcessToJobObject")
	procSetInformationJobObject = kernel32.NewProc("SetInformationJobObject")
	procQueryInformationJob     = kernel32.NewProc("QueryInformationJobObject")
	procTerminateJobObject      = kernel32.NewProc("TerminateJobObject")
	procFreeConsole             = kernel32.NewProc("FreeConsole")
	procAttachConsole           = kernel32.NewProc("AttachConsole")
	procSetConsoleCtrlHandler   = kernel32.NewProc("SetConsoleCtrlHandler")
	procGenerateConsoleCtrl     = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

const (
	createNewProcessGroup = 0x00000200

	jobObjectBasicAccountingInformation = 1
	jobObjectExtendedLimitInformation   = 9
	jobObjectLimitKillOnJobClose        = 0x2000

	processSetQuota  = 0x0100
	processTerminate = 0x0001

	ctrlBreakEvent = 1
)

type jobBasicLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobIOCounters struct{ a, b, c, d, e, f uint64 }

type jobExtendedLimit struct {
	Basic                 jobBasicLimit
	IO                    jobIOCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type jobBasicAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodUser, ThisPeriodKernel  int64
	TotalPageFaults, TotalProcesses, ActiveProcesses, TotalTerminated uint32
}

type procGroup struct {
	pid      int
	breakCmd func(pid int) *exec.Cmd
	mu       sync.Mutex
	job      syscall.Handle
	released bool
}

func configureProc(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// attachProc puts the new process in a job. If that cannot be done (security
// software that has put its own children in a job can refuse), there is no job
// and a stop falls back to the one process.
func attachProc(cmd *exec.Cmd, breakCmd func(int) *exec.Cmd) *procGroup {
	g := &procGroup{pid: cmd.Process.Pid, breakCmd: breakCmd}
	if g.breakCmd == nil {
		g.breakCmd = selfCtrlBreak
	}
	job, _, _ := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return g
	}
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return g
	}
	defer syscall.CloseHandle(h)
	if r, _, _ := procAssignProcessToJob.Call(job, uintptr(h)); r == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return g
	}
	var info jobExtendedLimit
	info.Basic.LimitFlags = jobObjectLimitKillOnJobClose
	procSetInformationJobObject.Call(job, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	g.job = syscall.Handle(job)
	return g
}

// selfCtrlBreak runs this executable's hidden ctrl-break subcommand.
func selfCtrlBreak(pid int) *exec.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe, "helpers", "ctrl-break", strconv.Itoa(pid))
	sysproc.NoWindow(cmd)
	return cmd
}

// interrupt sends CTRL_BREAK to the helper's process group. Flockdeck's GUI
// build has no console, and an event can only be sent from a process attached
// to the target's console, so a short-lived process does it (see CtrlBreak).
// An error means it did not work, and the caller kills instead.
func (g *procGroup) interrupt() error {
	g.mu.Lock()
	released := g.released
	g.mu.Unlock()
	if released {
		return errors.New("the process group is gone")
	}
	cmd := g.breakCmd(g.pid)
	sysproc.NoWindow(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return errors.New("sending CTRL_BREAK took too long")
	}
}

// kill ends the job, or the process if there is no job.
func (g *procGroup) kill() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released {
		return
	}
	if g.job != 0 {
		procTerminateJobObject.Call(uintptr(g.job), 1)
		return
	}
	if h, err := syscall.OpenProcess(processTerminate, false, uint32(g.pid)); err == nil {
		_ = syscall.TerminateProcess(h, 1)
		_ = syscall.CloseHandle(h)
	}
}

// drain waits until the job holds no process, or the deadline.
func (g *procGroup) drain(deadline time.Time) {
	for time.Now().Before(deadline) {
		g.mu.Lock()
		job, released := g.job, g.released
		g.mu.Unlock()
		if job == 0 || released {
			return
		}
		var acct jobBasicAccounting
		r, _, _ := procQueryInformationJob.Call(uintptr(job), jobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&acct)), unsafe.Sizeof(acct), 0)
		if r == 0 || acct.ActiveProcesses == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// release closes the job, which, with KILL_ON_JOB_CLOSE, ends anything the
// launcher left in it.
func (g *procGroup) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released {
		return
	}
	g.released = true
	if g.job != 0 {
		_ = syscall.CloseHandle(g.job)
		g.job = 0
	}
}

// terminateStale ends a process an earlier Flockdeck left, which on Windows
// only happens when the job could not be made. It ends the one process; the
// caller has already checked it is the helper it recorded.
func terminateStale(pid int, grace time.Duration) error {
	h, err := syscall.OpenProcess(processTerminate|0x00100000, false, uint32(pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	if err := syscall.TerminateProcess(h, 1); err != nil {
		return err
	}
	_, _ = syscall.WaitForSingleObject(h, uint32(grace/time.Millisecond))
	return nil
}

// commandMentions cannot say here: a record without a start time is not acted
// on.
func commandMentions(pid int, dir string) bool { return false }

// CtrlBreak sends CTRL_BREAK to the process group whose id is pid, from a
// process that is not attached to that group's console. It detaches from any
// console of its own, attaches to the target's, ignores the event itself, sends
// it, and detaches. It changes console state, so it is meant to run in a process
// of its own that then exits, never in Flockdeck.
func CtrlBreak(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("%d is not a process id", pid)
	}
	procFreeConsole.Call()
	if r, _, err := procAttachConsole.Call(uintptr(pid)); r == 0 {
		return fmt.Errorf("attach to the console of process %d: %w", pid, err)
	}
	defer procFreeConsole.Call()
	// A nil handler with TRUE makes this process ignore Ctrl+C, so the event it
	// is about to send does not end it too.
	procSetConsoleCtrlHandler.Call(0, 1)
	defer procSetConsoleCtrlHandler.Call(0, 0)
	if r, _, err := procGenerateConsoleCtrl.Call(ctrlBreakEvent, uintptr(pid)); r == 0 {
		return fmt.Errorf("send CTRL_BREAK to process group %d: %w", pid, err)
	}
	// The event is delivered by the console host, not by this call returning.
	time.Sleep(200 * time.Millisecond)
	return nil
}
