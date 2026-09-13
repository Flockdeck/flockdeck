//go:build windows

package session

import (
	"encoding/binary"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// A pane's process is put in a job object as soon as it starts, and the job is
// how closing the pane finds what the process started. Windows has no process
// groups to signal, and killing the one process left everything it had
// started: a program started with no console of its own ran on after the pane
// was gone, and one attached to the pane's console only began to end when the
// console did, after Close had returned -- which for an agent started from a
// .cmd shim is the agent itself, holding its worktree's folder open while the
// folder was being removed.
//
// Only console programs are ended. A program with windows of its own -- the
// browser an agent opened for a login when none was running yet, an editor
// started with `code .`, an Explorer window -- is the user's once it is on
// screen, and closing a terminal does not close it. For the same reason the
// job is not told to take its processes with it when it is closed: Flockdeck
// exiting must not close somebody's browser either.

var (
	procCreateJobObjectW           = kernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJobObject   = kernel32.NewProc("AssignProcessToJobObject")
	procQueryInformationJobObject  = kernel32.NewProc("QueryInformationJobObject")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	jobObjectBasicProcessIDList = 3

	processTerminate               = 0x0001
	processSetQuota                = 0x0100
	processQueryLimitedInformation = 0x1000
	synchronize                    = 0x00100000

	// imageSubsystemWindowsGUI is what a program's PE header says when it
	// draws windows of its own rather than running in a console.
	imageSubsystemWindowsGUI = 2

	// endPasses bounds how many times the job is listed again, for what was
	// started while the processes already listed were being ended.
	endPasses = 3
)

// jobProcessIDs is JOBOBJECT_BASIC_PROCESS_ID_LIST with room for as many
// processes as a pane's tree is ever walked to.
type jobProcessIDs struct {
	Assigned uint32
	Listed   uint32
	IDs      [maxTreeProcs]uintptr
}

// procTree is the job a pane's process was put in, or zero when it could not
// be. A pane without one is ended the way it always was, one process alone.
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
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(pid))
	if err != nil {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return procTree{}
	}
	defer syscall.CloseHandle(h)
	if r, _, _ := procAssignProcessToJobObject.Call(job, uintptr(h)); r == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return procTree{}
	}
	return procTree{job: syscall.Handle(job)}
}

// jobMemberIDs lists the processes a job holds.
func jobMemberIDs(job syscall.Handle) []uint32 {
	list := new(jobProcessIDs)
	if r, _, _ := procQueryInformationJobObject.Call(uintptr(job), jobObjectBasicProcessIDList,
		uintptr(unsafe.Pointer(list)), unsafe.Sizeof(*list), 0); r == 0 {
		return nil
	}
	out := make([]uint32, 0, list.Listed)
	for _, pid := range list.IDs[:min(int(list.Listed), len(list.IDs))] {
		out = append(out, uint32(pid))
	}
	return out
}

// consoleProgram reports whether an open process is known to run in a console
// rather than draw windows of its own. A process whose program cannot be read
// is not known to be either, and is left alone.
func consoleProgram(h syscall.Handle) bool {
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	n := uint32(len(buf))
	if r, _, _ := procQueryFullProcessImageNameW.Call(uintptr(h), 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return false
	}
	sub, ok := peSubsystem(syscall.UTF16ToString(buf[:n]))
	return ok && sub != imageSubsystemWindowsGUI
}

// peSubsystem reads the subsystem out of a program's PE header: the field of
// the optional header that says whether it is a console program or a windowed
// one, which is at the same place in the 32 and 64-bit layouts.
func peSubsystem(path string) (uint16, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	var dos [64]byte
	if _, err := f.ReadAt(dos[:], 0); err != nil || dos[0] != 'M' || dos[1] != 'Z' {
		return 0, false
	}
	at := int64(binary.LittleEndian.Uint32(dos[0x3c:]))
	if at <= 0 || at > 1<<20 {
		return 0, false
	}
	// The signature, the file header, and the optional header as far as the
	// subsystem.
	const fileHeader, subsystemAt = 20, 68
	var hdr [4 + fileHeader + subsystemAt + 2]byte
	if _, err := f.ReadAt(hdr[:], at); err != nil || string(hdr[:4]) != "PE\x00\x00" {
		return 0, false
	}
	opt := hdr[4+fileHeader:]
	if magic := binary.LittleEndian.Uint16(opt); magic != 0x10b && magic != 0x20b {
		return 0, false
	}
	return binary.LittleEndian.Uint16(opt[subsystemAt:]), true
}

// endTree ends the pane's process and every console program it started, and
// waits for them to be gone, within closeGrace. Programs with windows of their
// own are left running.
func (s *Session) endTree() {
	deadline := time.Now().Add(closeGrace)
	// Taken, so that a second Close neither ends the tree again nor closes a
	// handle whose number may have been given to something else since.
	s.mu.Lock()
	job := s.tree.job
	s.tree.job = 0
	s.mu.Unlock()

	// Each process ended is held open until it is gone, which also keeps its
	// id from being handed to another process in the meantime. Terminating a
	// process only starts its end, and it holds what it had open until that
	// is over.
	var ending []syscall.Handle
	if job != 0 {
		seen := map[uint32]bool{}
		for pass := 0; pass < endPasses; pass++ {
			more := false
			for _, pid := range jobMemberIDs(job) {
				if seen[pid] {
					continue
				}
				seen[pid] = true
				h, err := syscall.OpenProcess(processQueryLimitedInformation|processTerminate|synchronize, false, pid)
				if err != nil {
					continue
				}
				if !consoleProgram(h) {
					_ = syscall.CloseHandle(h)
					continue
				}
				_ = syscall.TerminateProcess(h, 1)
				ending = append(ending, h)
				more = true
			}
			if !more {
				break
			}
		}
	}
	// The pane's own process ends whatever it is.
	_ = s.cmd.Process.Kill()
	waitClosed(s.reaped, time.Until(deadline))
	for _, h := range ending {
		if left := time.Until(deadline); left > 0 {
			_, _ = syscall.WaitForSingleObject(h, uint32(left/time.Millisecond))
		}
		_ = syscall.CloseHandle(h)
	}
	if job != 0 {
		_ = syscall.CloseHandle(job)
	}
}
