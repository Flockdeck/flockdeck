package helpers

import (
	"encoding/binary"
	"strconv"
	"strings"
)

// Who is listening on the helper's port.
//
// A helper is told a port that Flockdeck found free and closed again, so there
// is a gap in which another program can take it. The banner check catches a
// program that is not the helper; it does not catch one that waits for the
// real helper to print the banner and answers /readyz itself, with a helper
// that never bound. Asking the operating system who owns the listening socket
// closes that. It is done where it can be done soundly (Windows, from the
// owner-pid TCP table; Linux, from /proc) and is "not verified" elsewhere, which
// is said in the status and not hidden. Nothing here asks the helper to prove
// anything, and no protocol is added to it.

// ownerResult is what the check found.
type ownerResult int

const (
	// ownerUnknown: the platform cannot say, or what it said was unusable.
	ownerUnknown ownerResult = iota
	// ownerVerified: every listener on the port belongs to the helper's group.
	ownerVerified
	// ownerMismatch: some listener on the port belongs to something else.
	ownerMismatch
)

// Owner words in a Status.
const (
	OwnerVerified   = "verified"
	OwnerUnverified = "unverified"
)

// checkListenerOwner reports whether the sockets listening on a loopback port
// belong to the processes in group. It is a variable so a test can stand in for
// a platform.
var checkListenerOwner = platformListenerOwner

// OwnerCheckSupported says whether this platform can verify a listener's owner.
func OwnerCheckSupported() bool { return ownerCheckSupported }

// parseProcNetTCP reads the text of /proc/net/tcp or /proc/net/tcp6 and returns
// the inodes of the sockets that are listening on port. A line is
// "sl local rem st tx:rx tr:when retrnsmt uid timeout inode ...".
func parseProcNetTCP(text string, port int) []uint64 {
	var inodes []uint64
	for i, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 10 {
			continue
		}
		const listen = "0A"
		if f[3] != listen {
			continue
		}
		at := strings.LastIndexByte(f[1], ':')
		if at < 0 {
			continue
		}
		p, err := strconv.ParseUint(f[1][at+1:], 16, 16)
		if err != nil || int(p) != port {
			continue
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil || inode == 0 {
			continue
		}
		inodes = append(inodes, inode)
	}
	return inodes
}

// parseStatPgrp reads the process group out of a /proc/<pid>/stat line. The
// name is in parentheses and may hold spaces and parentheses itself, so the
// fields are counted from the last closing one: state, parent, group.
func parseStatPgrp(stat string) (int, bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, false
	}
	f := strings.Fields(stat[end+1:])
	if len(f) < 3 {
		return 0, false
	}
	n, err := strconv.Atoi(f[2])
	return n, err == nil
}

// tcpPort is a port as the Windows tables hold it: the low two bytes of a
// DWORD, in network byte order.
func tcpPort(v uint32) int { return int(v&0xff)<<8 | int(v>>8&0xff) }

// parseTCPTable4 reads a MIB_TCPTABLE_OWNER_PID (GetExtendedTcpTable, IPv4,
// listeners): a count, then rows of state, local address, local port, remote
// address, remote port and owning pid, each a DWORD. It returns the owners of
// listeners on port.
func parseTCPTable4(buf []byte, port int) []uint32 {
	const row = 24
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	var pids []uint32
	for i := 0; i < n && 4+(i+1)*row <= len(buf); i++ {
		r := buf[4+i*row:]
		if tcpPort(binary.LittleEndian.Uint32(r[8:])) == port {
			pids = append(pids, binary.LittleEndian.Uint32(r[20:]))
		}
	}
	return pids
}

// parseTCPTable6 reads a MIB_TCP6TABLE_OWNER_PID: rows of local address (16
// bytes), scope id, local port, remote address (16), scope id, remote port,
// state and owning pid. A listener on any IPv6 address counts: a socket on
// [::] also accepts the IPv4 loopback address.
func parseTCPTable6(buf []byte, port int) []uint32 {
	const row = 56
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	var pids []uint32
	for i := 0; i < n && 4+(i+1)*row <= len(buf); i++ {
		r := buf[4+i*row:]
		if tcpPort(binary.LittleEndian.Uint32(r[20:])) == port {
			pids = append(pids, binary.LittleEndian.Uint32(r[52:]))
		}
	}
	return pids
}

// ownersWithin reports whether every pid in owners is in group. With no owners
// at all there is nothing to say.
func ownersWithin(owners []uint32, group []int) ownerResult {
	if len(owners) == 0 {
		return ownerUnknown
	}
	in := make(map[uint32]bool, len(group))
	for _, p := range group {
		in[uint32(p)] = true
	}
	for _, o := range owners {
		if !in[o] {
			return ownerMismatch
		}
	}
	return ownerVerified
}
