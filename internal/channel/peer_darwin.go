//go:build darwin

package channel

import (
	"errors"
	"net"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// readPeer asks the kernel who connected: LOCAL_PEERCRED gives the user id
// (what getpeereid reads) and LOCAL_PEERPID the process id.
func readPeer(conn net.Conn) (Peer, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return Peer{}, errors.New("not a socket")
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var cred *unix.Xucred
	var pid int
	var serr error
	if err := rc.Control(func(fd uintptr) {
		cred, serr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if serr != nil {
			return
		}
		pid, serr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return Peer{}, err
	}
	if serr != nil {
		return Peer{}, serr
	}
	return Peer{PID: pid, Owner: strconv.FormatUint(uint64(cred.Uid), 10)}, nil
}
