//go:build windows

package channel

import (
	"net"
	"net/http"
	"os"
	"strings"
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
	c := start(t, func(cfg *Config) { cfg.Seams.Self = self + "-9" })
	req, _ := newReq()
	if resp, err := Client(c.Path()).Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("answered a client whose SID is not the expected one: %d", resp.StatusCode)
	}
	if c.Denied() == 0 {
		t.Fatal("not counted")
	}
}

func TestProcessUserOfThisProcess(t *testing.T) {
	self, _ := selfOwner()
	got, err := ProcessUser(uint32(os.Getpid()))
	if err != nil || got != self {
		t.Fatalf("ProcessUser = %q, %v; want %q", got, err, self)
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
