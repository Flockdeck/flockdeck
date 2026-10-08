//go:build windows

package channel

import (
	"errors"
	"fmt"
	"net"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// watches is false on Windows: a pipe is not a file that something can
// delete, it goes with the process that holds it.
const watches = false

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

var getClientPID = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetNamedPipeClientProcessId")

// readPeer finds the client's process and the user it runs as.
func readPeer(conn net.Conn) (Peer, error) {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return Peer{}, errors.New("not a pipe")
	}
	var pid uint32
	if r, _, err := getClientPID.Call(f.Fd(), uintptr(unsafe.Pointer(&pid))); r == 0 {
		return Peer{}, fmt.Errorf("GetNamedPipeClientProcessId: %w", err)
	}
	owner, err := ProcessUser(pid)
	if err != nil {
		return Peer{PID: int(pid)}, err
	}
	return Peer{PID: int(pid), Owner: owner}, nil
}

// ProcessUser is the SID, as text, of the user a process runs as.
func ProcessUser(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return "", fmt.Errorf("open token of process %d: %w", pid, err)
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// Dial connects to the pipe at path.
func Dial(path string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(path, &timeout)
}
