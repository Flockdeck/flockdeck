//go:build linux

package helpers

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// A /proc tree made by the test: net/tcp with listeners, and processes whose
// stat names a group and whose fd folder holds socket links.
func fakeProc(t *testing.T, listeners map[int]uint64, procs map[int]struct {
	pgrp    int
	sockets []uint64
}) {
	t.Helper()
	root := t.TempDir()
	prev := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = prev })
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	for port, inode := range listeners {
		hex := strconv.FormatUint(uint64(port), 16)
		for len(hex) < 4 {
			hex = "0" + hex
		}
		text += "   0: 0100007F:" + hex + " 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 " + strconv.FormatUint(inode, 10) + " 1 0\n"
	}
	if err := os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	for pid, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil {
			t.Fatal(err)
		}
		stat := strconv.Itoa(pid) + " (x) S 1 " + strconv.Itoa(p.pgrp) + " " + strconv.Itoa(p.pgrp) + " 0 -1\n"
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
		for i, ino := range p.sockets {
			if err := os.Symlink("socket:["+strconv.FormatUint(ino, 10)+"]", filepath.Join(dir, "fd", strconv.Itoa(3+i))); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type fakeProcs = map[int]struct {
	pgrp    int
	sockets []uint64
}

func TestLinuxListenerOwner(t *testing.T) {
	fakeProc(t, map[int]uint64{8080: 1001}, fakeProcs{
		100: {pgrp: 100},                          // the launcher
		101: {pgrp: 100, sockets: []uint64{1001}}, // its child holds the socket
		200: {pgrp: 200, sockets: []uint64{2002}}, // somebody else
	})
	group := groupMembers(100)
	if len(group) != 2 {
		t.Fatalf("group = %v", group)
	}
	if res, why := platformListenerOwner(8080, group); res != ownerVerified {
		t.Fatalf("the helper's own socket: %v %s", res, why)
	}
	// The socket is held by a process outside the group.
	fakeProc(t, map[int]uint64{8080: 2002}, fakeProcs{
		100: {pgrp: 100},
		200: {pgrp: 200, sockets: []uint64{2002}},
	})
	if res, _ := platformListenerOwner(8080, groupMembers(100)); res != ownerMismatch {
		t.Fatalf("a squatter's socket: %v", res)
	}
	// Two listeners, one of them the squatter's.
	fakeProc(t, map[int]uint64{8080: 1001}, fakeProcs{100: {pgrp: 100, sockets: []uint64{1001}}})
	if res, _ := platformListenerOwner(8080, []int{100}); res != ownerVerified {
		t.Fatalf("res = %v", res)
	}
	// Nothing listening on the port: no verdict.
	if res, _ := platformListenerOwner(9999, []int{100}); res != ownerUnknown {
		t.Fatalf("res = %v", res)
	}
}
