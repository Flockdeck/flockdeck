//go:build windows

package channel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func TestSDDLGrantsOnlyTheUser(t *testing.T) {
	self, err := selfOwner()
	if err != nil {
		t.Fatal(err)
	}
	s := SDDL(self)
	if want := "D:P(A;;GA;;;" + self + ")"; s != want {
		t.Fatalf("SDDL = %q, want %q", s, want)
	}
	// The string parses, and the result has one ACE, for this user.
	raw, err := winio.SddlToSecurityDescriptor(s)
	if err != nil {
		t.Fatalf("SDDL does not parse: %v", err)
	}
	sd := (*windows.SECURITY_DESCRIPTOR)(unsafe.Pointer(&raw[0]))
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("no DACL: %v", err)
	}
	if acl.AceCount != 1 {
		t.Fatalf("%d entries, want 1", acl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Errorf("entry type %d, not allow", ace.Header.AceType)
	}
	if got := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String(); got != self {
		t.Errorf("entry is for %s, want %s", got, self)
	}
	ctrl, _, _ := sd.Control()
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		t.Error("the DACL is not protected")
	}
}

func TestPipeConfigUsesTheSDDL(t *testing.T) {
	cfg := PipeConfig("S-1-5-21-1-2-3-1000")
	if cfg.SecurityDescriptor != "D:P(A;;GA;;;S-1-5-21-1-2-3-1000)" {
		t.Fatalf("descriptor %q", cfg.SecurityDescriptor)
	}
	if cfg.MessageMode {
		t.Fatal("message mode")
	}
}

// The pipe a running channel made has the list the SDDL says, as the system
// reports it, not only as the string parses.
func TestRunningPipeHasTheUserOnlyDACL(t *testing.T) {
	c := start(t, nil)
	self, _ := selfOwner()
	conn, err := Dial(c.Path(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	h := windows.Handle(conn.(interface{ Fd() uintptr }).Fd())
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("no DACL: %v", err)
	}
	if acl.AceCount != 1 {
		t.Fatalf("%d entries on the live pipe: %s", acl.AceCount, sd.String())
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if got := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String(); got != self {
		t.Fatalf("live pipe grants %s, want %s (%s)", got, self, sd.String())
	}
}

func TestSecondPipeOfTheSameNameFails(t *testing.T) {
	c := start(t, nil)
	self, _ := selfOwner()
	if ln, err := winio.ListenPipe(c.Path(), PipeConfig(self)); err == nil {
		ln.Close()
		t.Fatal("a second pipe was made under a name in use")
	}
}

func TestPipeNameIsPerInstance(t *testing.T) {
	at := time.Unix(1700000000, 0)
	if a, b := PipeName(ID(1, at)), PipeName(ID(2, at)); a == b || !strings.HasPrefix(a, pipePrefix+"flockdeck-") {
		t.Fatalf("%q %q", a, b)
	}
}

// A client on another machine is turned away by the pipe itself. go-winio asks
// for FILE_PIPE_REJECT_REMOTE_CLIENTS on every instance (pipe.go, v0.6.2).
// This reaches the pipe the way a remote client would, through the network
// name of this machine, and expects not to get in; it cannot tell a refusal
// from a machine with file sharing off, so it only fails if a connection is
// made.
func TestRemoteNameDoesNotReachThePipe(t *testing.T) {
	c := start(t, nil)
	host, err := os.Hostname()
	if err != nil {
		t.Skip(err)
	}
	remote := pipePrefix[:2] + host + strings.TrimPrefix(c.Path(), pipePrefix[:3])
	timeout := 2 * time.Second
	conn, err := winio.DialPipe(remote, &timeout)
	if err == nil {
		conn.Close()
		t.Fatal("reached the pipe by its network name")
	}
	t.Logf("network name refused: %v", err)
}

func TestPeerFromAnotherUserSIDIsRefused(t *testing.T) {
	self, _ := selfOwner()
	var calls atomic.Int32
	c := start(t, func(cfg *Config) {
		cfg.Seams.Self = self + "-9"
		cfg.Window = func() string { calls.Add(1); return "x" }
	})
	if sendWindow(t, c) {
		t.Fatal("answered a client whose SID is not the expected one")
	}
	waitFor(t, "the refusal", func() bool { return c.Denied() == 1 })
	time.Sleep(200 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("the window verb ran %d times for a refused client", calls.Load())
	}
}

func TestDialToAMissingPipeFails(t *testing.T) {
	if conn, err := Dial(PipeName("nosuchpipe"), 200*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("dialled a pipe that does not exist")
	} else if _, ok := err.(net.Error); ok {
		t.Logf("timeout class: %v", err)
	}
}

func newReq() (*http.Request, error) {
	return http.NewRequest(http.MethodPost, "http://channel/identify", nil)
}

// A client that connects anonymously cannot be identified, and is refused.
func TestAnonymousClientIsRefused(t *testing.T) {
	c := start(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := winio.DialPipeAccessImpLevel(ctx, c.Path(), windows.GENERIC_READ|windows.GENERIC_WRITE, winio.PipeImpLevelAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(conn, "POST /identify HTTP/1.1\r\nHost: channel\r\nContent-Length: 0\r\n\r\n")
	if resp, err := http.ReadResponse(bufio.NewReader(conn), nil); err == nil {
		resp.Body.Close()
		t.Fatalf("an anonymous client was answered: %d", resp.StatusCode)
	}
	waitFor(t, "the refusal to be counted", func() bool { return c.Denied() == 1 })
}

// The check happens on the first read, not on accept, so a client that never
// writes is not checked and is not served either.
func TestSilentClientIsNotServed(t *testing.T) {
	c := start(t, nil)
	conn, err := Dial(c.Path(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, _ := conn.Read(make([]byte, 1)); n != 0 {
		t.Fatal("the server spoke first")
	}
	if c.Denied() != 0 {
		t.Fatal("refused a client that had not sent anything")
	}
}

// If the thread cannot be taken back from the client it is left locked and
// the goroutine ends; the connection is refused and nothing runs.
func TestFailedRevertRefusesTheConnection(t *testing.T) {
	old := revertToSelf
	revertToSelf = func() error {
		windows.RevertToSelf() // really revert, so the test's threads are clean
		return errors.New("simulated failure")
	}
	t.Cleanup(func() { revertToSelf = old })

	var calls atomic.Int32
	var mu sync.Mutex
	var lines []string
	c := start(t, func(cfg *Config) {
		cfg.Window = func() string { calls.Add(1); return "x" }
		cfg.Logf = func(f string, a ...any) { mu.Lock(); lines = append(lines, fmt.Sprintf(f, a...)); mu.Unlock() }
	})
	if sendWindow(t, c) {
		t.Fatal("answered although the thread could not be reverted")
	}
	waitFor(t, "the refusal", func() bool { return c.Denied() == 1 })
	time.Sleep(200 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("the window verb ran %d times", calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || !strings.Contains(lines[0], "could not stop impersonating the client") {
		t.Fatalf("log = %q", lines)
	}
}
