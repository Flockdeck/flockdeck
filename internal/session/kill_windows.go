//go:build windows

package session

import (
	"syscall"
	"time"
	"unsafe"
)

// A pane's process is put in a job object as soon as it starts, and the job is
// what closing the pane ends. Windows has no process groups to signal, and
// killing the one process left everything it had started: a program started
// with no console of its own ran on after the pane was gone, and one attached
// to the pane's console only began to end when the console did, after Close
// had returned -- which for an agent run from a .cmd shim is the agent itself,
// holding its worktree's folder open while the folder was being removed.
//
// A job is also closed with Flockdeck, however Flockdeck ends, and the job is
// told to take its processes with it then.

var (
	procCreateJobObjectW          = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject   = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject  = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject        = kernel32.NewProc("TerminateJobObject")
	procQueryInformationJobObject = kernel32.NewProc("QueryInformationJobObject")
)

const (
	jobObjectBasicAccountingInformation = 1
	jobObjectExtendedLimitInformation   = 9
	jobObjectLimitKillOnJobClose        = 0x2000

	processSetQuota  = 0x0100
	processTerminate = 0x0001
)

// jobBasicLimits is JOBOBJECT_BASIC_LIMIT_INFORMATION, jobExtendedLimits
// JOBOBJECT_EXTENDED_LIMIT_INFORMATION. Only LimitFlags is set; the rest is
// declared because the call is given the size of the whole structure.
type jobBasicLimits struct {
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

type jobExtendedLimits struct {
	Basic                 jobBasicLimits
	IO                    [6]uint64
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, read for how many
// processes the job still holds.
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

// procTree is the job a pane's process was put in, or zero when it could not
// be. A pane without one is ended the way it always was, one process at a
// time.
type procTree struct{ job syscall.Handle }

// containTree puts a newly started process in a job of its own, which
// everything it starts from then on joins.
//
// What it starts before this runs is not in the job, but this runs the moment
// the process has been created, and what a program starts first is not
// started that soon.
func containTree(pid int) procTree {
	job, _, _ := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return procTree{}
	}
	fail := func() procTree {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return procTree{}
	}
	var limits jobExtendedLimits
	limits.Basic.LimitFlags = jobObjectLimitKillOnJobClose
	if r, _, _ := procSetInformationJobObject.Call(job, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); r == 0 {
		return fail()
	}
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(pid))
	if err != nil {
		return fail()
	}
	defer syscall.CloseHandle(h)
	if r, _, _ := procAssignProcessToJobObject.Call(job, uintptr(h)); r == 0 {
		return fail()
	}
	return procTree{job: syscall.Handle(job)}
}

// jobProcessIDs is JOBOBJECT_BASIC_PROCESS_ID_LIST with room for as many
// processes as a pane's tree is ever walked to.
type jobProcessIDs struct {
	Assigned uint32
	Listed   uint32
	IDs      [maxTreeProcs]uintptr
}

const jobObjectBasicProcessIDList = 3

// jobMembers opens every process a job holds, for waiting on. Holding one open
// also keeps its id from being handed to another process while it is waited
// for.
func jobMembers(job syscall.Handle) []syscall.Handle {
	const synchronize = 0x00100000
	list := new(jobProcessIDs)
	if r, _, _ := procQueryInformationJobObject.Call(uintptr(job), jobObjectBasicProcessIDList,
		uintptr(unsafe.Pointer(list)), unsafe.Sizeof(*list), 0); r == 0 {
		return nil
	}
	var out []syscall.Handle
	for _, pid := range list.IDs[:min(int(list.Listed), len(list.IDs))] {
		if h, err := syscall.OpenProcess(synchronize, false, uint32(pid)); err == nil {
			out = append(out, h)
		}
	}
	return out
}

// jobProcesses reports how many processes a job still holds, or 0 when it
// cannot be asked.
func jobProcesses(job syscall.Handle) uint32 {
	var info jobAccounting
	r, _, _ := procQueryInformationJobObject.Call(uintptr(job), jobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0)
	if r == 0 {
		return 0
	}
	return info.ActiveProcesses
}

// endTree ends the pane's process and everything it started, and waits for
// all of it to be gone, within closeGrace.
func (s *Session) endTree() {
	deadline := time.Now().Add(closeGrace)
	// Taken, so that a second Close neither ends the job again nor closes a
	// handle whose number may have been given to something else since.
	s.mu.Lock()
	job := s.tree.job
	s.tree.job = 0
	s.mu.Unlock()

	var members []syscall.Handle
	if job != 0 {
		members = jobMembers(job)
		_, _, _ = procTerminateJobObject.Call(uintptr(job), 1)
	}
	_ = s.cmd.Process.Kill()
	waitClosed(s.reaped, time.Until(deadline))
	if job == 0 {
		return
	}
	// Terminating a job starts the end of every process in it, as killing one
	// process does, and they hold what they had open until it is over. What
	// says it is over is each process being signalled: the job stops counting
	// one a moment before that, and a child reported gone by the count was
	// still there to be found, one time in twenty.
	for _, h := range members {
		if left := time.Until(deadline); left > 0 {
			_, _ = syscall.WaitForSingleObject(h, uint32(left/time.Millisecond))
		}
		_ = syscall.CloseHandle(h)
	}
	// Anything started between the list being taken and the job being ended.
	for jobProcesses(job) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.CloseHandle(job)
}
