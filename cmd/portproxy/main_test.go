package main

import (
	"net"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// isolateConfig points internal/store's state directory at a temporary
// location, the same three environment variables internal/store's own tests
// set (store.Dir goes through os.UserConfigDir, which reads whichever of
// these the platform running the test uses).
func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS and fallback
}

func TestHostFromInstanceURL(t *testing.T) {
	got, err := hostFromInstanceURL("http://127.0.0.1:54321")
	if err != nil {
		t.Fatalf("hostFromInstanceURL: %v", err)
	}
	if got != "127.0.0.1:54321" {
		t.Errorf("got %q, want 127.0.0.1:54321", got)
	}
}

func TestHostFromInstanceURLRejectsNoHost(t *testing.T) {
	if _, err := hostFromInstanceURL("not-a-url-at-all"); err == nil {
		t.Error("want an error for a URL with no host, got nil")
	}
}

// TestTargetAddrNoInstanceYet is the case a connection hits during the
// window before flockdeck has written its first instance record, or between
// one run ending and the next one's start -- targetAddr must say so plainly
// rather than panic or hang.
func TestTargetAddrNoInstanceYet(t *testing.T) {
	isolateConfig(t)
	if _, err := targetAddr(); err == nil {
		t.Error("want an error with no instance record saved, got nil")
	}
}

// TestTargetAddrReadsInstanceRecord pins that this program finds flockdeck's
// real address the same way internal/store.SaveInstance's other reader
// (docker/healthcheck.sh, and internal/server's own attach probe) does.
func TestTargetAddrReadsInstanceRecord(t *testing.T) {
	isolateConfig(t)
	if err := store.SaveInstance(&store.Instance{
		PID: 4321, URL: "http://127.0.0.1:53124", Token: "s3cret", Started: time.Now(),
	}); err != nil {
		t.Fatalf("save instance: %v", err)
	}
	got, err := targetAddr()
	if err != nil {
		t.Fatalf("targetAddr: %v", err)
	}
	if got != "127.0.0.1:53124" {
		t.Errorf("got %q, want 127.0.0.1:53124", got)
	}
}

// TestHandleRelaysBytesBothWays is the whole point of this program, checked
// without flockdeck itself: an upstream that echoes what it is sent stands in
// for it, and handleConn is given that upstream's address directly rather
// than through an instance record, since targetAddr's own lookup is already
// covered above.
func TestHandleRelaysBytesBothWays(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen (upstream): %v", err)
	}
	defer upstream.Close()
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if _, werr := conn.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// A real TCP pair stands in for the browser-facing side, so the half-close
	// path (net.Conn's CloseWrite) is exercised the same way it would be for
	// an actual client -- net.Pipe's conns do not implement it.
	frontend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen (frontend): %v", err)
	}
	defer frontend.Close()

	proxySide := make(chan net.Conn, 1)
	go func() {
		conn, err := frontend.Accept()
		if err == nil {
			proxySide <- conn
		}
	}()
	userSide, err := net.Dial("tcp", frontend.Addr().String())
	if err != nil {
		t.Fatalf("dial (frontend): %v", err)
	}
	defer userSide.Close()

	client := <-proxySide
	go handleConn(client, upstream.Addr().String())

	want := "hello through the sidecar"
	if _, err := userSide.Write([]byte(want)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = userSide.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(want))
	if _, err := readFull(userSide, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != want {
		t.Errorf("echoed %q, want %q", buf, want)
	}
}

// readFull reads until buf is filled, since a TCP read can return fewer
// bytes than were written in one call.
func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
