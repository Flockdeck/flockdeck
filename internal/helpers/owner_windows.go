//go:build windows

package helpers

import (
	"syscall"
	"unsafe"
)

const ownerCheckSupported = true

var (
	iphlpapi              = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTCPTbl = iphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	afInet                  = 2
	afInet6                 = 23
	tcpTableOwnerPIDListen  = 3
	errorInsufficientBuffer = 122
)

// listenerTable asks Windows for the TCP listeners and their owning processes.
func listenerTable(family uint32) ([]byte, bool) {
	var size uint32
	for try := 0; try < 6; try++ {
		var p unsafe.Pointer
		var buf []byte
		if size > 0 {
			buf = make([]byte, size)
			p = unsafe.Pointer(&buf[0])
		}
		r, _, _ := procGetExtendedTCPTbl.Call(uintptr(p), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListen, 0)
		switch r {
		case 0:
			return buf[:size], true
		case errorInsufficientBuffer:
			continue
		default:
			return nil, false
		}
	}
	return nil, false
}

// platformListenerOwner reads the IPv4 and IPv6 listener tables and gives the
// verdict for the sockets that could have answered (see decide).
func platformListenerOwner(port int, group []int) (ownerResult, string) {
	t4, ok4 := listenerTable(afInet)
	t6, ok6 := listenerTable(afInet6)
	if !ok4 && !ok6 {
		return ownerUnknown, "the TCP table could not be read"
	}
	rows := append(parseTCPTable4(t4, port), parseTCPTable6(t6, port)...)
	res := decide(rows, pidHolder(group))
	if res == ownerMismatch {
		return res, "a process outside the helper holds the port"
	}
	return res, ""
}

// members lists the processes in the helper's job. It says false when it
// cannot give the whole list, which is when there is no job (it could not be
// made, and then only the launcher is known) or the job holds more than fits
// the buffer: a partial list would make a socket held by a process that is the
// helper's own look foreign.
func (g *procGroup) members() ([]int, bool) {
	g.mu.Lock()
	job, released := g.job, g.released
	g.mu.Unlock()
	if job == 0 || released {
		return nil, false
	}
	const room = 256
	word := int(unsafe.Sizeof(uintptr(0)))
	const counts = 2 * 4
	buf := make([]uintptr, counts/word+room)
	r, _, err := procQueryInformationJob.Call(uintptr(job), 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*word), 0)
	if r == 0 && err != syscall.Errno(234) { // ERROR_MORE_DATA still fills in what fits
		return nil, false
	}
	counts32 := (*[2]uint32)(unsafe.Pointer(&buf[0]))
	assigned, listed := int(counts32[0]), int(counts32[1])
	ids := buf[counts/word:]
	if assigned > listed || listed > len(ids) {
		return nil, false
	}
	out := make([]int, 0, listed)
	for _, id := range ids[:listed] {
		out = append(out, int(id))
	}
	return out, true
}
