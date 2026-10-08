//go:build windows

package channel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// watches is false on Windows: a pipe is not a file that something can
// delete, it goes with the process that holds it.
const watches = false

// deferredPeer is whether the peer is checked on the first read, not on accept:
// a pipe's client can only be impersonated once it has written.
const deferredPeer = true

// fileID is not used on Windows.
type fileID struct{}

func (c *Channel) removeSocket() {}

// pipePrefix is where named pipes live.
const pipePrefix = `\\.\pipe\`

// PipeName is the pipe's name for an instance.
func PipeName(id string) string { return pipePrefix + "flockdeck-" + id }

// SDDL is the access list the pipe is made with: one entry, giving the user
// the pipe is made for full access, and nothing for anyone else. The P makes
// the list protected, so nothing is inherited into it. Without an explicit
// list a pipe grants Everyone read access.
func SDDL(userSID string) string { return "D:P(A;;GA;;;" + userSID + ")" }

// PipeConfig is what the pipe is made with. go-winio's ListenPipe always asks
// for FILE_PIPE_REJECT_REMOTE_CLIENTS and makes the first instance with
// FILE_CREATE, so a name that is taken fails and a squatter is never joined.
func PipeConfig(userSID string) *winio.PipeConfig {
	return &winio.PipeConfig{
		SecurityDescriptor: SDDL(userSID),
		InputBufferSize:    MaxHeaderBytes + MaxBodyBytes,
		OutputBufferSize:   MaxHeaderBytes,
	}
}

func selfOwner() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("find the current user: %w", err)
	}
	return u.User.Sid.String(), nil
}

func (c *Channel) bind() (net.Listener, string, error) {
	name := PipeName(ID(c.cfg.PID, c.cfg.Started))
	var ln net.Listener
	var err error
	if bind := c.cfg.Seams.Bind; bind != nil {
		ln, err = bind(name)
	} else {
		// The list is for the user this process runs as, whatever a test says
		// the peer must be.
		var user string
		if user, err = selfOwner(); err == nil {
			ln, err = winio.ListenPipe(name, PipeConfig(user))
		}
	}
	if err != nil {
		return nil, "", fmt.Errorf("no safe place for the local channel: %w", err)
	}
	return ln, name, nil
}

func (c *Channel) rebindAt(string) (net.Listener, error) {
	return nil, errors.New("a named pipe is not rebound")
}

func (c *Channel) verify() error { return nil }

var (
	getClientPID = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetNamedPipeClientProcessId")
	impersonate  = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")
)

// readPeer finds the user the client of a pipe runs as. It impersonates the
// client on this thread for as long as it takes to read the user from the
// thread's token, which ties the answer to the connection itself: no process
// id is looked up afterwards, so a reused id cannot be mistaken for the
// client, and a client whose own process token is closed to us (an elevated
// one, say) is still read. It needs the client to have written something, and
// the client to have connected at identification level or above (see Dial).
func readPeer(conn net.Conn) (Peer, error) {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return Peer{}, errors.New("not a pipe")
	}
	h := f.Fd()

	var pid uint32
	_, _, _ = getClientPID.Call(h, uintptr(unsafe.Pointer(&pid))) // for the log only

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, err := impersonate.Call(h); r == 0 {
		return Peer{PID: int(pid)}, fmt.Errorf("ImpersonateNamedPipeClient: %w", err)
	}
	defer windows.RevertToSelf()
	var tok windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &tok); err != nil {
		return Peer{PID: int(pid)}, fmt.Errorf("read the client's token: %w", err)
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return Peer{PID: int(pid)}, err
	}
	return Peer{PID: int(pid), Owner: u.User.Sid.String()}, nil
}

// Dial connects to the pipe at path.
func Dial(path string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Identification is the lowest level at which the server can read who is
	// connecting. The default, anonymous, is refused.
	return winio.DialPipeAccessImpLevel(ctx, path, windows.GENERIC_READ|windows.GENERIC_WRITE, winio.PipeImpLevelIdentification)
}
