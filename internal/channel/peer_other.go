//go:build !linux && !darwin && !windows

package channel

import (
	"errors"
	"net"
)

// readPeer has no way to tell who connected on this system, so every
// connection is refused unless a test supplies one.
func readPeer(net.Conn) (Peer, error) {
	return Peer{}, errors.New("peer credentials are not available on this system")
}
