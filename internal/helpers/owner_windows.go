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

// platformListenerOwner finds every listener on the port, IPv4 and IPv6, and
// checks each one's owning process is in the helper's group.
func platformListenerOwner(port int, group []int) (ownerResult, string) {
	t4, ok4 := listenerTable(afInet)
	t6, ok6 := listenerTable(afInet6)
	if !ok4 && !ok6 {
		return ownerUnknown, "the TCP table could not be read"
	}
	owners := append(parseTCPTable4(t4, port), parseTCPTable6(t6, port)...)
	res := ownersWithin(owners, group)
	if res == ownerMismatch {
		return res, "a process outside the helper holds the port"
	}
	return res, ""
}

// members lists the processes in the helper's job, or just the launcher when
// there is no job.
func (g *procGroup) members() []int {
	g.mu.Lock()
	job, released := g.job, g.released
	g.mu.Unlock()
	out := []int{g.pid}
	if job == 0 || released {
		return out
	}
	const room = 64
	word := int(unsafe.Sizeof(uintptr(0)))
	const counts = 2 * 4
	buf := make([]uintptr, counts/word+room)
	r, _, err := procQueryInformationJob.Call(uintptr(job), 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*word), 0)
	if r == 0 && err != syscall.Errno(234) { // ERROR_MORE_DATA still fills in what fits
		return out
	}
	listed := int((*[2]uint32)(unsafe.Pointer(&buf[0]))[1])
	ids := buf[counts/word:]
	for _, id := range ids[:min(listed, len(ids))] {
		if int(id) != g.pid {
			out = append(out, int(id))
		}
	}
	return out
}
