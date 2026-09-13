//go:build windows

package gitx

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// The git on PATH is usually not git itself. Git for Windows puts cmd\git.exe
// there, a small program that sets up the environment and starts
// mingw64\bin\git.exe to do the work, and scoop and Chocolatey install shims
// that do the same. A deadline killed only the process it had started -- the
// wrapper -- and left the real git running, holding the checkout, the console
// Windows gave it and git's output. A checkout that hung gained one more every
// time the pane headers were refreshed, fifteen seconds apart, for as long as
// it stayed stuck.
//
// So git is started in a job object of its own, which everything it starts
// joins, and giving up on it ends the job: the wrapper, git, and whatever git
// had started -- a hook, ssh, a credential helper. The job is not told to end
// its processes when it is closed. Flockdeck exiting in the middle of a commit
// must not kill git halfway through writing the index and leave its lock
// behind for every git after it to trip over.

var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW          = kernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJobObject  = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject        = kernel32.NewProc("TerminateJobObject")
	procQueryInformationJobObject = kernel32.NewProc("QueryInformationJobObject")
	procThread32First             = kernel32.NewProc("Thread32First")
	procThread32Next              = kernel32.NewProc("Thread32Next")
	procOpenThread                = kernel32.NewProc("OpenThread")
	procResumeThread              = kernel32.NewProc("ResumeThread")

	procNtResumeProcess = syscall.NewLazyDLL("ntdll.dll").NewProc("NtResumeProcess")
)

const (
	createSuspended      = 0x00000004
	processTerminate     = 0x0001
	processSetQuota      = 0x0100
	processSuspendResume = 0x0800
	threadSuspendResume  = 0x0002

	jobObjectBasicAccountingInformation = 1
)

// threadEntry32 is THREADENTRY32.
type threadEntry32 struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePri        int32
	DeltaPri       int32
	Flags          uint32
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// procTree is the job one git command runs in. A zero job is none at all --
// the job could not be made, or git could not be put in it -- and git is then
// ended the way it always was, one process alone.
type procTree struct{ job syscall.Handle }

// newTree makes the job a command is about to be started in. The job is made
// before git is started, so that ending it, which may happen from another
// goroutine the moment git has been started, never races with making it.
func newTree() *procTree {
	job, _, _ := procCreateJobObjectW.Call(0, 0)
	return &procTree{job: syscall.Handle(job)}
}

// start starts cmd in the job.
//
// git is started suspended and let go only once it is in the job. The wrapper
// starts the real git as nearly the first thing it does, and a git started
// before the wrapper had joined would not be in the job either.
func (t *procTree) start(cmd *exec.Cmd) error {
	if t.job == 0 {
		return cmd.Start()
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createSuspended
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := uint32(cmd.Process.Pid)
	h, err := syscall.OpenProcess(processSetQuota|processTerminate|processSuspendResume, false, pid)
	if err == nil {
		// A process that cannot be put in the job still runs; only giving up
		// on it goes back to ending it alone.
		_, _, _ = procAssignProcessToJobObject.Call(uintptr(t.job), uintptr(h))
		err = resumeProcess(h, pid)
		_ = syscall.CloseHandle(h)
	} else {
		err = resumeThreads(pid)
	}
	if err != nil {
		// Left suspended it would never exit, and nothing would ever wait
		// for it again.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("git could not be started: %w", err)
	}
	return nil
}

// resumeProcess lets a process started suspended run.
//
// exec keeps no handle to the thread it was started with, so the process is
// resumed as a whole, which ntdll has done in one call since Windows XP.
// Finding the thread instead means a snapshot of every thread on the machine:
// on one with eight and a half thousand of them that took ninety milliseconds,
// more than the wrapper this is all for costs, on every git call. It is kept
// for a Windows where the one call is missing or refused.
func resumeProcess(h syscall.Handle, pid uint32) error {
	if procNtResumeProcess.Find() == nil {
		if status, _, _ := procNtResumeProcess.Call(uintptr(h)); status == 0 {
			return nil
		}
	}
	return resumeThreads(pid)
}

// resumeThreads resumes a suspended process by finding its thread.
func resumeThreads(pid uint32) error {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(snap)
	entry := threadEntry32{Size: uint32(unsafe.Sizeof(threadEntry32{}))}
	resumed := false
	r, _, callErr := procThread32First.Call(uintptr(snap), uintptr(unsafe.Pointer(&entry)))
	for ; r != 0; r, _, callErr = procThread32Next.Call(uintptr(snap), uintptr(unsafe.Pointer(&entry))) {
		if entry.OwnerProcessID != pid {
			continue
		}
		th, _, openErr := procOpenThread.Call(threadSuspendResume, 0, uintptr(entry.ThreadID))
		if th == 0 {
			return openErr
		}
		prev, _, resumeErr := procResumeThread.Call(th)
		_ = syscall.CloseHandle(syscall.Handle(th))
		if prev == 0xFFFFFFFF {
			return resumeErr
		}
		resumed = true
	}
	if !resumed {
		return fmt.Errorf("no thread of process %d was found to resume: %v", pid, callErr)
	}
	return nil
}

// end ends every process in the job. It may be called from exec's own
// goroutine while start is still running, which is why the job is made first.
func (t *procTree) end() {
	if t.job != 0 {
		_, _, _ = procTerminateJobObject.Call(uintptr(t.job), 1)
	}
}

// running counts the processes still in the job. One that cannot be asked is
// taken to be empty, so that nothing waits on it for ever.
func (t *procTree) running() uint32 {
	var info jobAccounting
	if r, _, _ := procQueryInformationJobObject.Call(uintptr(t.job), jobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0); r == 0 {
		return 0
	}
	return info.ActiveProcesses
}

// settle is called once git has been waited for, and returns a channel closed
// once every process in the job has exited; the job is closed then.
//
// A command that finished on its own is not waited on any further: whatever it
// left running in the background is left to run, as it always was. One that
// was ended is given up to pipeGrace here, which is ordinarily far more than
// it needs, and beyond that is watched in the background. Ending a process
// only starts its end, and one stuck on a network drive that has gone away
// ends only when that gives up, which is what the channel is for: the
// checkout is not asked about again until then (see Exited).
func (t *procTree) settle(ended bool) <-chan struct{} {
	if t.job == 0 {
		return alreadyGone
	}
	gone := make(chan struct{})
	release := func() {
		_ = syscall.CloseHandle(t.job)
		close(gone)
	}
	if !ended {
		release()
		return gone
	}
	for deadline := time.Now().Add(pipeGrace); t.running() > 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			go func() {
				for t.running() > 0 {
					time.Sleep(250 * time.Millisecond)
				}
				release()
			}()
			return gone
		}
	}
	release()
	return gone
}
