//go:build linux

package helpers

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const ownerCheckSupported = true

// procRoot is where /proc is, a variable so a test can point it at a tree it
// made.
var procRoot = "/proc"

// socketsHeldBy lists the socket inodes a process holds open, and false when
// its descriptors cannot be read (another user's process, hidepid, a process
// that is not dumpable).
func socketsHeldBy(pid int) ([]uint64, bool) {
	dir := filepath.Join(procRoot, strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	var out []uint64
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil || !strings.HasPrefix(target, "socket:[") {
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out, true
}

// platformListenerOwner finds the sockets listening on the port in
// /proc/net/tcp and tcp6, and for the ones that could have answered (see
// decide) finds who holds each: a member of the helper's group or one of its
// descendants, a process that is positively somebody else's, or nobody that
// can be found. A holder that cannot be found, or descriptors that cannot be
// read, give "unknown" and never a mismatch, so a process this one is not
// allowed to look into cannot cause a helper to be stopped.
func platformListenerOwner(port int, group []int) (ownerResult, string) {
	var rows []listenerRow
	read := false
	for _, name := range []string{"tcp", "tcp6"} {
		b, err := os.ReadFile(filepath.Join(procRoot, "net", name))
		if err != nil {
			continue
		}
		read = true
		rows = append(rows, parseProcNetTCP(string(b), port)...)
	}
	if !read {
		return ownerUnknown, "/proc/net/tcp could not be read"
	}
	ours := map[uint64]bool{}
	for _, pid := range group {
		inodes, ok := socketsHeldBy(pid)
		if !ok {
			continue
		}
		for _, in := range inodes {
			ours[in] = true
		}
	}
	// Who else holds a socket, for the inodes that are not the helper's.
	foreign := map[uint64]bool{}
	inGroup := map[int]bool{}
	for _, p := range group {
		inGroup[p] = true
	}
	if entries, err := os.ReadDir(procRoot); err == nil {
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || inGroup[pid] {
				continue
			}
			inodes, ok := socketsHeldBy(pid)
			if !ok {
				continue
			}
			for _, in := range inodes {
				foreign[in] = true
			}
		}
	}
	holderOf := func(inode uint64) holder {
		switch {
		case ours[inode]:
			return holderOurs
		case foreign[inode]:
			return holderForeign
		}
		return holderUnknown
	}
	res := decide(rows, holderOf)
	switch res {
	case ownerMismatch:
		return res, "a socket on the port is held by a process outside the helper"
	case ownerUnknown:
		return res, "who holds the socket on the port could not be found"
	}
	return res, ""
}

// groupMembers lists the helper's process group and every descendant of its
// leader: a worker that moved itself to a session of its own is still the
// helper's while its parent is. It says false when /proc cannot be listed.
func groupMembers(pgid int) ([]int, bool) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, false
	}
	parent := map[int]int{}
	group := map[int]bool{pgid: true}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "stat"))
		if err != nil {
			continue
		}
		if pp, pg, ok := parseStat(string(b)); ok {
			parent[pid] = pp
			if pg == pgid {
				group[pid] = true
			}
		}
	}
	// Descendants, to a fixed depth so a loop in a damaged table ends.
	for round := 0; round < 32; round++ {
		grew := false
		for pid, pp := range parent {
			if !group[pid] && group[pp] {
				group[pid] = true
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	out := make([]int, 0, len(group))
	for pid := range group {
		out = append(out, pid)
	}
	return out, true
}
