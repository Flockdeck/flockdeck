//go:build linux

package helpers

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// A /proc tree made by the test: net/tcp with listeners, and processes whose
// stat names a parent and a group and whose fd folder holds socket links.

type fakeListener struct {
	addr  string // hex, as /proc prints it: "0100007F", "00000000", ...
	port  int
	inode uint64
}

type fakeProcess struct {
	ppid, pgrp int
	sockets    []uint64
	// fdIsAFile makes the descriptors unreadable, as another user's or a
	// non-dumpable process's are.
	fdIsAFile bool
}

func fakeProc(t *testing.T, listeners []fakeListener, procs map[int]fakeProcess) {
	t.Helper()
	root := t.TempDir()
	prev := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = prev })
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	for _, l := range listeners {
		hex := strconv.FormatUint(uint64(l.port), 16)
		for len(hex) < 4 {
			hex = "0" + hex
		}
		text += "   0: " + l.addr + ":" + hex + " 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 " + strconv.FormatUint(l.inode, 10) + " 1 0\n"
	}
	if err := os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	for pid, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		stat := strconv.Itoa(pid) + " (x) S " + strconv.Itoa(p.ppid) + " " + strconv.Itoa(p.pgrp) + " " + strconv.Itoa(p.pgrp) + " 0 -1\n"
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
		if p.fdIsAFile {
			if err := os.WriteFile(filepath.Join(dir, "fd"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil {
			t.Fatal(err)
		}
		for i, ino := range p.sockets {
			if err := os.Symlink("socket:["+strconv.FormatUint(ino, 10)+"]", filepath.Join(dir, "fd", strconv.Itoa(3+i))); err != nil {
				t.Fatal(err)
			}
		}
	}
}

const (
	loopback = "0100007F"
	wildcard = "00000000"
	// ::1, in /proc/net/tcp6's word-by-word form.
	v6loopback = "00000000000000000000000001000000"
)

func linuxVerdict(t *testing.T, port, leader int) ownerResult {
	t.Helper()
	group, ok := groupMembers(leader)
	if !ok {
		t.Fatal("the group could not be listed")
	}
	res, _ := platformListenerOwner(port, group)
	return res
}

func TestLinuxListenerOwner(t *testing.T) {
	// The helper's child holds the socket.
	fakeProc(t, []fakeListener{{loopback, 8080, 1001}}, map[int]fakeProcess{
		100: {ppid: 1, pgrp: 100},
		101: {ppid: 100, pgrp: 100, sockets: []uint64{1001}},
		200: {ppid: 1, pgrp: 200, sockets: []uint64{2002}},
	})
	if res := linuxVerdict(t, 8080, 100); res != ownerVerified {
		t.Fatalf("the helper's own socket: %v", res)
	}

	// A squatter on 127.0.0.1: its holder is found, and it is not the helper's.
	fakeProc(t, []fakeListener{{loopback, 8080, 2002}}, map[int]fakeProcess{
		100: {ppid: 1, pgrp: 100},
		200: {ppid: 1, pgrp: 200, sockets: []uint64{2002}},
	})
	if res := linuxVerdict(t, 8080, 100); res != ownerMismatch {
		t.Fatalf("a squatter's socket: %v", res)
	}

	// Nothing on the port.
	if res := linuxVerdict(t, 9999, 100); res != ownerUnknown {
		t.Fatalf("no listener: %v", res)
	}
}

// A listener on another address of the same port number is not who answered.
func TestLinuxOtherAddressesAreNotTheAnswerer(t *testing.T) {
	fakeProc(t, []fakeListener{
		{loopback, 8080, 1001},   // the helper
		{wildcard, 8080, 2002},   // another process, less specific
		{v6loopback, 8080, 3003}, // another process, on [::1]
	}, map[int]fakeProcess{
		100: {ppid: 1, pgrp: 100, sockets: []uint64{1001}},
		200: {ppid: 1, pgrp: 200, sockets: []uint64{2002}},
		300: {ppid: 1, pgrp: 300, sockets: []uint64{3003}},
	})
	if res := linuxVerdict(t, 8080, 100); res != ownerVerified {
		t.Fatalf("a wildcard and a [::1] listener beside the helper's own: %v", res)
	}
}

// Descriptors that cannot be read, and a holder that cannot be found, are
// "unknown", never a reason to stop a helper.
func TestLinuxUnreadableDescriptorsAreUnknownNotAMismatch(t *testing.T) {
	// The helper's own descriptors cannot be read, so its socket cannot be
	// seen to be its own.
	fakeProc(t, []fakeListener{{loopback, 8080, 1001}}, map[int]fakeProcess{
		100: {ppid: 1, pgrp: 100, fdIsAFile: true},
		200: {ppid: 1, pgrp: 200, fdIsAFile: true}, // and nobody else's can be read either
	})
	if res := linuxVerdict(t, 8080, 100); res != ownerUnknown {
		t.Fatalf("unreadable descriptors: %v", res)
	}
	// The socket is in nobody's descriptors that can be read.
	fakeProc(t, []fakeListener{{loopback, 8080, 1001}}, map[int]fakeProcess{
		100: {ppid: 1, pgrp: 100},
		200: {ppid: 1, pgrp: 200, sockets: []uint64{2002}},
	})
	if res := linuxVerdict(t, 8080, 100); res != ownerUnknown {
		t.Fatalf("a holder that is not found: %v", res)
	}
}

// A worker that moved itself out of the helper's group is still the helper's
// while its parent is.
func TestLinuxAWorkerOutOfTheGroupIsStillTheHelpers(t *testing.T) {
	fakeProc(t, []fakeListener{{loopback, 8080, 1001}}, map[int]fakeProcess{
		100: {ppid: 1, pgrp: 100},
		101: {ppid: 100, pgrp: 100},
		102: {ppid: 101, pgrp: 102, sockets: []uint64{1001}}, // setsid()
		200: {ppid: 1, pgrp: 200, sockets: []uint64{2002}},
	})
	group, _ := groupMembers(100)
	found := false
	for _, p := range group {
		found = found || p == 102
	}
	if !found {
		t.Fatalf("the worker is not among %v", group)
	}
	if res := linuxVerdict(t, 8080, 100); res != ownerVerified {
		t.Fatalf("a worker that left the group: %v", res)
	}
	// And a process that is only in the same table is not: 200 is no relation.
	for _, p := range group {
		if p == 200 {
			t.Fatal("an unrelated process is in the group")
		}
	}
}

func TestLinuxGroupMembersWithoutProc(t *testing.T) {
	prev := procRoot
	procRoot = filepath.Join(t.TempDir(), "missing")
	defer func() { procRoot = prev }()
	if _, ok := groupMembers(1); ok {
		t.Fatal("a missing /proc gave a group")
	}
}
