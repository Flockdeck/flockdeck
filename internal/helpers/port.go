package helpers

import (
	"fmt"
	"net"
)

// ChoosePort picks the port a helper is told to listen on. The last port it
// used is tried first, if it is free, so a bookmark keeps working; otherwise
// the system is asked for one on the loopback address. The listener is closed
// before the port is returned, so there is a gap in which something else can
// take it. The supervisor's check of the helper's first line is what catches
// that.
func ChoosePort(preferred int) (int, error) {
	return choosePort(preferred, net.Listen)
}

func choosePort(preferred int, listen func(network, addr string) (net.Listener, error)) (int, error) {
	// Ports below 1024 are privileged on Unix and mostly taken everywhere; a
	// record that holds one is not a port Flockdeck chose.
	if preferred >= 1024 && preferred <= 65535 {
		if ln, err := listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred)); err == nil {
			ln.Close()
			return preferred, nil
		}
	}
	ln, err := listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find a free port: %w", err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("find a free port: unexpected address %v", ln.Addr())
	}
	return addr.Port, nil
}
