//go:build windows

package helpers

import (
	"net"
	"os"
	"testing"
)

// With no job there is only the launcher, and a list that short would make a
// socket the helper's own children hold look foreign.
func TestMembersAreUnknownWithoutAJob(t *testing.T) {
	g := &procGroup{pid: os.Getpid()}
	if pids, ok := g.members(); ok || pids != nil {
		t.Fatalf("members = %v, %v", pids, ok)
	}
}

func TestTheRealTCPTable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	// This process holds the socket.
	if res, why := platformListenerOwner(port, []int{os.Getpid()}); res != ownerVerified {
		t.Fatalf("the holder in the group: %v %s", res, why)
	}
	// A group that does not include it: somebody else's socket.
	if res, _ := platformListenerOwner(port, []int{1}); res != ownerMismatch {
		t.Fatalf("the holder outside the group: %v", res)
	}
	ln.Close()
	if res, _ := platformListenerOwner(port, []int{os.Getpid()}); res != ownerUnknown {
		t.Fatalf("no listener: %v", res)
	}
}
