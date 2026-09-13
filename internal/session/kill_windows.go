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
// screen, and closing a terminal does not close it. Nor are the console
// programs such a program starts, since they are its and not the pane's: the
// terminal in an editor started with `code .`, the language servers and git it
// runs. For the same reason the job is not told to take its processes with it
// when it is closed: Flockdeck exiting must not close somebody's browser
// either.

var (
	procCreateJobObjectW           = kernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJobObject   = kernel32.NewProc("AssignProcessToJobObject")
	procQueryInformationJobObject  = kernel32.NewProc("QueryInformationJobObject")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procIsProcessInJob             = kernel32.NewProc("IsProcessInJob")
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

// jobMemberIDs lists the processes a job holds, as many as a pane's tree is
// ever walked to.
func jobMemberIDs(job syscall.Handle) []uint32 { return listJob(job, maxTreeProcs) }

// listJob lists up to room of the processes a job holds.
//
// The list is JOBOBJECT_BASIC_PROCESS_ID_LIST: two counts, then the ids. A
// job holding more than there is room for fails the call as ERROR_MORE_DATA,
// but still fills in as many as fit, and those are ended rather than none.
func listJob(job syscall.Handle, room int) []uint32 {
	const counts = 2 * 4
	word := int(unsafe.Sizeof(uintptr(0)))
	buf := make([]uintptr, counts/word+room)
	r, _, err := procQueryInformationJobObject.Call(uintptr(job), jobObjectBasicProcessIDList,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*word), 0)
	if r == 0 && err != syscall.ERROR_MORE_DATA {
		return nil
	}
	listed := (*[2]uint32)(unsafe.Pointer(&buf[0]))[1]
	ids := buf[counts/word:]
	out := make([]uint32, 0, min(int(listed), len(ids)))
	for _, pid := range ids[:min(int(listed), len(ids))] {
		out = append(out, uint32(pid))
	}
	return out
}

// openMember opens a process the job listed, to be ended, and reports whether
// it is still in the job once it is open.
//
// The job lists ids, and an id is let go of when its process ends: the
// process listed may have ended since, and its id have been handed to one of
// somebody else's. Once the process is open its id cannot be handed on, so
// asking the job then settles which process it is.
func openMember(job syscall.Handle, pid uint32) (syscall.Handle, bool) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation|processTerminate|synchronize, false, pid)
	if err != nil {
		return 0, false
	}
	var in int32
	if r, _, _ := procIsProcessInJob.Call(uintptr(h), uintptr(job), uintptr(unsafe.Pointer(&in))); r == 0 || in == 0 {
		_ = syscall.CloseHandle(h)
		return 0, false
	}
	return h, true
}

// programSubsystem reads, for an open process, whether its program runs in a
// console or draws windows of its own. ok is false when the program cannot be
// read, and then the process is known to be neither, and is left alone.
func programSubsystem(h syscall.Handle) (sub uint16, ok bool) {
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	n := uint32(len(buf))
	if r, _, _ := procQueryFullProcessImageNameW.Call(uintptr(h), 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return 0, false
	}
	return peSubsystem(syscall.UTF16ToString(buf[:n]))
}

// jobMember is what endTree has read about a process in the pane's job.
type jobMember struct {
	windowed bool
	// started is when the process was created, in file-time ticks.
	started int64
}

// startedAt returns when an open process was created, in file-time ticks, or
// zero when that cannot be read.
func startedAt(h syscall.Handle) int64 {
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	return filetimeTicks(creation)
}

// underWindow reports whether a process in the pane's job was started,
// directly or through others, by a program with windows of its own that is
// also in the job: a terminal in an editor started from the pane, or the
// language servers the editor runs.
//
// The walk goes from parent to parent through members of the job only, and
// stops at the pane's own process. A parent is only followed to a member
// started before its child. A process keeps its parent's id after the parent
// has ended, and by then the id may be another process's. A console program
// whose windowed parent has already gone is not known to be the window's any
// more, and is ended.
func underWindow(pid uint32, pane int, parents map[int]int, members map[uint32]jobMember) bool {
	child, ok := members[pid]
	if !ok {
		return false
	}
	for i := 0; i < maxTreeProcs; i++ {
		ppid, ok := parents[int(pid)]
		if !ok || ppid == pane || ppid == int(pid) {
			return false
		}
		parent, ok := members[uint32(ppid)]
		if !ok || parent.started > child.started {
			return false
		}
		if parent.windowed {
			return true
		}
		pid, child = uint32(ppid), parent
	}
	return false
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
		pane := s.cmd.Process.Pid
		seen := map[uint32]bool{}
		members := map[uint32]jobMember{}
		type console struct {
			pid uint32
			h   syscall.Handle
		}
		for pass := 0; pass < endPasses; pass++ {
			// Every new member is read before any is ended, so that a console
			// program's windowed parent is known whichever is listed first.
			var consoles []console
			for _, pid := range jobMemberIDs(job) {
				if seen[pid] {
					continue
				}
				seen[pid] = true
				h, ok := openMember(job, pid)
				if !ok {
					continue
				}
				sub, known := programSubsystem(h)
				m := jobMember{windowed: known && sub == imageSubsystemWindowsGUI, started: startedAt(h)}
				members[pid] = m
				if !known || m.windowed {
					_ = syscall.CloseHandle(h)
					continue
				}
				consoles = append(consoles, console{pid, h})
			}
			// Taken after the listing, so every process listed and still
			// running is in it.
			parents := procParents()
			more := false
			for _, c := range consoles {
				if underWindow(c.pid, pane, parents, members) {
					_ = syscall.CloseHandle(c.h)
					continue
				}
				_ = syscall.TerminateProcess(c.h, 1)
				ending = append(ending, c.h)
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
