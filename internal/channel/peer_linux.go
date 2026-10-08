//go:build linux

package channel

import (
	"errors"
	"net"
	"strconv"
	"syscall"
)

// readPeer asks the kernel who connected, with SO_PEERCRED. The credentials
// are those of the process that called connect.
func readPeer(conn net.Conn) (Peer, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return Peer{}, errors.New("not a socket")
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var cred *syscall.Ucred
	var serr error
	if err := rc.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return Peer{}, err
	}
	if serr != nil {
		return Peer{}, serr
	}
	return Peer{PID: int(cred.Pid), Owner: strconv.FormatUint(uint64(cred.Uid), 10)}, nil
}
