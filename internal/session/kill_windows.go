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
// runs. For the same reason endTree clears JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
// before it lets the job go: Flockdeck exiting in the ordinary way must not
// close somebody's browser either.
//
// The job is created with that limit set, though, and it stays set until
// endTree clears it. Flockdeck does not always get to run endTree: a crash, a
// `taskkill /F`, a wedged shutdown that forceQuit gives up on, a power cut,
// all end the process without a line of Go running first, the same way they
// would if the job had never been made. Windows itself closes every handle a
// dying process still holds, the job's included, and with the limit still set
// that alone is enough to take the whole job with it -- so a pane's process,
// and anything it started, cannot outlive Flockdeck by more than the moment it
// takes the operating system to notice Flockdeck is gone.
//
// That still leaves the case the job was never made to begin with:
// AssignProcessToJobObject can be refused by security software that has
// already put its own children in a job, or simply not exist on an old
// enough Windows. Nothing links the pane to what it started then, on a clean
// close or a crash alike, and KILL_ON_JOB_CLOSE has nothing to act on. See
// endTree's fallback to killDescendants for that case, and reap.go for the
// startup sweep that exists for whatever either safety net still misses.
var (
	procCreateJobObjectW           = kernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJobObject   = kernel32.NewProc("AssignProcessToJobObject")
	procSetInformationJobObject    = kernel32.NewProc("SetInformationJobObject")
	procQueryInformationJobObject  = kernel32.NewProc("QueryInformationJobObject")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procIsProcessInJob             = kernel32.NewProc("IsProcessInJob")
)

const (
	jobObjectBasicProcessIDList     = 3
	jobObjectExtendedLimitInfoClass = 9

	// jobObjectLimitKillOnJobClose is JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: every
	// process the job still holds is terminated the instant its last handle is
	// closed, with no cooperation needed from whoever held that handle.
	jobObjectLimitKillOnJobClose = 0x00002000

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

// jobobjectBasicLimitInformation is JOBOBJECT_BASIC_LIMIT_INFORMATION. Only
// LimitFlags is ever set; the rest stay zero, which asks for no limit.
type jobobjectBasicLimitInformation struct {
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

// jobobjectIOCounters is JOBOBJECT_IO_COUNTERS, an unused part of
// JOBOBJECT_EXTENDED_LIMIT_INFORMATION that still has to be the right size for
// SetInformationJobObject to read the rest of the struct correctly.
type jobobjectIOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// jobobjectExtendedLimitInformation is JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobobjectExtendedLimitInformation struct {
	BasicLimitInformation jobobjectBasicLimitInformation
	IoInfo                jobobjectIOCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// setKillOnClose sets or clears JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE on job, and
// reports whether it could.
func setKillOnClose(job syscall.Handle, on bool) bool {
	var info jobobjectExtendedLimitInformation
	if on {
		info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	}
	r, _, _ := procSetInformationJobObject.Call(uintptr(job), jobObjectExtendedLimitInfoClass,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	return r != 0
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
	// Asked for from the moment the process is in the job, not only once
	// something has gone wrong: Flockdeck dying is not an event it gets to
	// react to first. See the package doc above for why this has to be here
	// rather than only in endTree's ordinary path.
	setKillOnClose(syscall.Handle(job), true)
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
	if job == 0 {
		// containTree could not put the pane's process in a job at all --
		// AssignProcessToJobObject can be refused by security software that
		// has already put its own children in one, or simply not exist on an
		// old enough Windows -- so nothing here links the pane to what it
		// started. This is the failure the job object was brought in to fix
		// in the first place: killing the one process left everything under
		// it running, which on Windows meant a worktree's folder stayed open
		// long after the pane working in it had been closed. Rather than
		// fall back to that, the machine's whole process table stands in for
		// the job that could not be made; see killDescendants.
		ending = killDescendants(uint32(s.cmd.Process.Pid))
	} else {
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
		// This is the ordinary path -- endTree is running, which is what
		// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE is there for the absence of.
		// Cleared first, closing the job below takes only what the walk above
		// left running that was never meant to be spared: a windowed program
		// is deliberately left out of "ending", and closing the job with the
		// limit still on would end it anyway, the moment its last handle --
		// this one -- goes.
		setKillOnClose(job, false)
		_ = syscall.CloseHandle(job)
	}
}

// openLimited opens a process to be ended, without regard to any job: for
// killDescendants, which finds what to end by walking the machine's own
// process table rather than a job's membership.
func openLimited(pid uint32) (syscall.Handle, bool) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation|processTerminate|synchronize, false, pid)
	if err != nil {
		return 0, false
	}
	return h, true
}

// killDescendants finds every process the machine's whole process table shows
// descended from root, starts ending each one that is not left for the user
// to close by hand -- a program with windows of its own, or anything started
// under one, by the same rule endTree applies to a job's own members -- and
// returns their handles, still open, for the caller to wait on. root itself
// is not included; the caller ends that one its own way.
//
// It is where endTree falls back to when the pane's process could not be put
// in a job to track its children by, and it is how KillTree ends a past run's
// own orphaned pane, once nothing links it to anything any more. Either way
// the process table is all there is left to walk, the way Usage already
// walks it for CPU and memory.
//
// A process claiming root, or one already found under it, as its parent but
// created before it was is not really descended from it: the id it claims has
// been handed to something unrelated since, on a machine where process ids
// come round again quickly. Such a process, and anything under it, is left
// alone rather than guessed about.
func killDescendants(root uint32) []syscall.Handle {
	parents := procParents()
	if len(parents) == 0 {
		return nil
	}
	children := make(map[uint32][]uint32, len(parents))
	for pid, ppid := range parents {
		if ppid > 0 && uint32(ppid) != uint32(pid) {
			children[uint32(ppid)] = append(children[uint32(ppid)], uint32(pid))
		}
	}

	type queued struct {
		pid     uint32
		started int64
	}
	seen := map[uint32]bool{root: true}
	queue := []queued{{root, 0}}
	var ending []syscall.Handle
	for i := 0; i < len(queue) && len(queue) < maxTreeProcs; i++ {
		for _, kid := range children[queue[i].pid] {
			if seen[kid] {
				continue
			}
			seen[kid] = true
			h, ok := openLimited(kid)
			if !ok {
				continue
			}
			sub, known := programSubsystem(h)
			if known && sub == imageSubsystemWindowsGUI {
				_ = syscall.CloseHandle(h)
				continue
			}
			started := startedAt(h)
			if parent := queue[i].started; parent != 0 && started != 0 && started < parent {
				_ = syscall.CloseHandle(h)
				continue
			}
			_ = syscall.TerminateProcess(h, 1)
			ending = append(ending, h)
			queue = append(queue, queued{kid, started})
		}
	}
	return ending
}

// KillTree ends pid and every process the machine's process table shows
// descended from it -- the same thing endTree does for a pane's own tree when
// it is still in a job, except pid belongs to no job this run can query:
// whatever job it may once have held its children in died with the run that
// made it, or was never made at all.
//
// It is exported for a startup sweep to call directly, once it has already
// confirmed the pid still names the same process it was recorded under (see
// store.ProcessAlive and store.ProcessStartedAt): nothing here repeats that
// check, since ending the wrong process is not something to guess twice about.
// See killDescendants, and internal/workspace's reaper.
func KillTree(pid int) {
	root := uint32(pid)
	deadline := time.Now().Add(closeGrace)

	var ending []syscall.Handle
	if h, ok := openLimited(root); ok {
		_ = syscall.TerminateProcess(h, 1)
		ending = append(ending, h)
	}
	ending = append(ending, killDescendants(root)...)

	for _, h := range ending {
		if left := time.Until(deadline); left > 0 {
			_, _ = syscall.WaitForSingleObject(h, uint32(left/time.Millisecond))
		}
		_ = syscall.CloseHandle(h)
	}
}
