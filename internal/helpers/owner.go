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
//
// The check has to be right about what it condemns, since what it condemns is
// stopped. It looks only at sockets the probe could have reached, which are
// the ones bound to 127.0.0.1 and the wildcard addresses: another process
// listening on [::1] or on another interface's address on the same port number
// is not who answered. And among those, what would receive a connection to
// 127.0.0.1 is the most specific: a socket bound to 127.0.0.1 wins over one
// bound to the wildcard, so a wildcard listener beside the helper's own is not
// who answered either.

// ownerResult is what the check found.
type ownerResult int

const (
	// ownerUnknown: the platform cannot say, or what it said was unusable, or
	// a socket's holder could not be found.
	ownerUnknown ownerResult = iota
	// ownerVerified: every listener that could have answered belongs to the
	// helper's processes.
	ownerVerified
	// ownerMismatch: a listener that could have answered belongs to a process
	// that is positively not the helper's.
	ownerMismatch
)

// Owner words in a Status.
const (
	OwnerVerified   = "verified"
	OwnerUnverified = "unverified"
)

// checkListenerOwner reports whether the sockets that could answer on a
// loopback port belong to the processes in group. It is a variable so a test
// can stand in for a platform.
var checkListenerOwner = platformListenerOwner

// OwnerCheckSupported says whether this platform can verify a listener's owner.
func OwnerCheckSupported() bool { return ownerCheckSupported }

// bindClass is what an address a socket is bound to means to a probe of
// 127.0.0.1.
type bindClass int

const (
	// bindOther: an address the probe cannot reach (::1, another interface).
	bindOther bindClass = iota
	// bindLoopback: 127.0.0.1, or its IPv4-mapped IPv6 form.
	bindLoopback
	// bindWildcard: 0.0.0.0 or ::, which also take a connection to 127.0.0.1
	// when nothing is bound more specifically.
	bindWildcard
)

// listenerRow is one listening socket: what it is bound to and who holds it,
// the holder being a pid (Windows) or a socket inode (Linux).
type listenerRow struct {
	class  bindClass
	holder uint64
}

// holder says whose a socket's holder is.
type holder int

const (
	holderUnknown holder = iota
	holderOurs
	holderForeign
)

// decide gives the verdict for a port from its listening sockets. Among the
// sockets the probe could reach, the loopback-bound ones decide it if there are
// any, and the wildcard-bound ones otherwise. Any socket held by a process
// that is positively foreign is a mismatch; a socket whose holder cannot be
// found is unknown; with none of either, it is verified.
func decide(rows []listenerRow, holderOf func(uint64) holder) ownerResult {
	var loop, wild []listenerRow
	for _, r := range rows {
		switch r.class {
		case bindLoopback:
			loop = append(loop, r)
		case bindWildcard:
			wild = append(wild, r)
		}
	}
	chosen := loop
	if len(chosen) == 0 {
		chosen = wild
	}
	if len(chosen) == 0 {
		return ownerUnknown
	}
	unknown := false
	for _, r := range chosen {
		switch holderOf(r.holder) {
		case holderForeign:
			return ownerMismatch
		case holderUnknown:
			unknown = true
		}
	}
	if unknown {
		return ownerUnknown
	}
	return ownerVerified
}

// classifyV4 is the class of a bound IPv4 address, as the 32-bit value /proc
// and the Windows tables both give it: the bytes in network order read as a
// little-endian word.
func classifyV4(addr uint32) bindClass {
	switch addr {
	case 0x0100007F:
		return bindLoopback
	case 0:
		return bindWildcard
	}
	return bindOther
}

// classifyV6 is the class of a bound IPv6 address in network byte order.
func classifyV6(b [16]byte) bindClass {
	var zero [16]byte
	if b == zero {
		return bindWildcard
	}
	var mapped [16]byte
	mapped[10], mapped[11] = 0xff, 0xff
	mapped[12], mapped[13], mapped[14], mapped[15] = 127, 0, 0, 1
	if b == mapped {
		return bindLoopback
	}
	return bindOther
}

// parseProcNetTCP reads the text of /proc/net/tcp or /proc/net/tcp6 and returns
// the sockets that are listening on port, with the inode of each as its holder.
// A line is "sl local rem st tx:rx tr:when retrnsmt uid timeout inode ...".
// The local address is hex: one little-endian word for IPv4, four for IPv6.
func parseProcNetTCP(text string, port int) []listenerRow {
	var rows []listenerRow
	for i, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 10 || f[3] != "0A" { // 0A is LISTEN
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
		class := bindOther
		switch addr := f[1][:at]; len(addr) {
		case 8:
			v, err := strconv.ParseUint(addr, 16, 32)
			if err != nil {
				continue
			}
			class = classifyV4(uint32(v))
		case 32:
			var b [16]byte
			ok := true
			for w := 0; w < 4 && ok; w++ {
				v, err := strconv.ParseUint(addr[w*8:w*8+8], 16, 32)
				if err != nil {
					ok = false
					break
				}
				// Each word is the kernel's native (little-endian) reading of four
				// address bytes, so putting it back little-endian restores them.
				binary.LittleEndian.PutUint32(b[w*4:], uint32(v))
			}
			if !ok {
				continue
			}
			class = classifyV6(b)
		default:
			continue
		}
		rows = append(rows, listenerRow{class: class, holder: inode})
	}
	return rows
}

// parseStat reads the parent and the process group out of a /proc/<pid>/stat
// line. The name is in parentheses and may hold spaces and parentheses itself,
// so the fields are counted from the last closing one: state, parent, group.
func parseStat(stat string) (ppid, pgrp int, ok bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, 0, false
	}
	f := strings.Fields(stat[end+1:])
	if len(f) < 3 {
		return 0, 0, false
	}
	pp, err1 := strconv.Atoi(f[1])
	pg, err2 := strconv.Atoi(f[2])
	return pp, pg, err1 == nil && err2 == nil
}

// parseStatPgrp is parseStat's group alone.
func parseStatPgrp(stat string) (int, bool) {
	_, pg, ok := parseStat(stat)
	return pg, ok
}

// tcpPort is a port as the Windows tables hold it: the low two bytes of a
// DWORD, in network byte order.
func tcpPort(v uint32) int { return int(v&0xff)<<8 | int(v>>8&0xff) }

// parseTCPTable4 reads a MIB_TCPTABLE_OWNER_PID (GetExtendedTcpTable, IPv4,
// listeners): a count, then rows of state, local address, local port, remote
// address, remote port and owning pid, each a DWORD. It returns the listeners
// on port, each with its owning pid as the holder.
func parseTCPTable4(buf []byte, port int) []listenerRow {
	const row = 24
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	var rows []listenerRow
	for i := 0; i < n && 4+(i+1)*row <= len(buf); i++ {
		r := buf[4+i*row:]
		if tcpPort(binary.LittleEndian.Uint32(r[8:])) == port {
			rows = append(rows, listenerRow{
				class:  classifyV4(binary.LittleEndian.Uint32(r[4:])),
				holder: uint64(binary.LittleEndian.Uint32(r[20:])),
			})
		}
	}
	return rows
}

// parseTCPTable6 reads a MIB_TCP6TABLE_OWNER_PID: rows of local address (16
// bytes), scope id, local port, remote address (16), scope id, remote port,
// state and owning pid.
func parseTCPTable6(buf []byte, port int) []listenerRow {
	const row = 56
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	var rows []listenerRow
	for i := 0; i < n && 4+(i+1)*row <= len(buf); i++ {
		r := buf[4+i*row:]
		if tcpPort(binary.LittleEndian.Uint32(r[20:])) == port {
			var addr [16]byte
			copy(addr[:], r[:16])
			rows = append(rows, listenerRow{class: classifyV6(addr), holder: uint64(binary.LittleEndian.Uint32(r[52:]))})
		}
	}
	return rows
}

// pidHolder is decide's holderOf for tables that give pids: a pid is the
// helper's or it is somebody else's, never unknown.
func pidHolder(group []int) func(uint64) holder {
	in := make(map[uint64]bool, len(group))
	for _, p := range group {
		in[uint64(p)] = true
	}
	return func(pid uint64) holder {
		if in[pid] {
			return holderOurs
		}
		return holderForeign
	}
}
