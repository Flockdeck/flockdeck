//go:build !windows

package channel

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

// watches is whether the socket can go missing and is looked for.
const watches = true

func selfOwner() (string, error) { return strconv.Itoa(os.Geteuid()), nil }

func (c *Channel) uid() int {
	if f := c.cfg.Seams.UID; f != nil {
		return f()
	}
	return os.Geteuid()
}

func (c *Channel) limit() int {
	if c.cfg.Seams.MaxPath > 0 {
		return c.cfg.Seams.MaxPath
	}
	return maxPath
}

func (c *Channel) tmp() string {
	if c.cfg.Seams.Tmp != "" {
		return c.cfg.Seams.Tmp
	}
	return defaultTmp()
}

// place is one of the two places a socket may go.
type place struct {
	dir  string
	path string
}

// places are the primary location, in the state directory, and the fallback
// below the temporary directory. The socket is never put in
// $XDG_RUNTIME_DIR: it is removed when the user logs out, and a detached
// instance goes on running after that.
func (c *Channel) places() []place {
	name := ID(c.cfg.PID, c.cfg.Started) + ".sock"
	primary := filepath.Join(c.cfg.StateDir, "c")
	fallback := filepath.Join(c.tmp(), "flockdeck-"+strconv.Itoa(c.uid()))
	return []place{
		{primary, filepath.Join(primary, name)},
		{fallback, filepath.Join(fallback, name)},
	}
}

// errTooLong is a path that does not fit in sun_path.
var errTooLong = errors.New("path is too long for a socket")

// bind makes the listener in the first place that works. The same checks are
// made in both; a place that fails any of them is skipped, and if both fail
// there is no channel.
func (c *Channel) bind() (net.Listener, string, error) {
	var errs []error
	for _, p := range c.places() {
		ln, err := c.bindIn(p)
		if err == nil {
			return ln, p.path, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.path, err))
	}
	return nil, "", fmt.Errorf("no safe place for the local channel: %w", errors.Join(errs...))
}

func (c *Channel) bindIn(p place) (net.Listener, error) {
	if len(p.path) > c.limit() {
		return nil, fmt.Errorf("%w (%d bytes, the limit is %d)", errTooLong, len(p.path), c.limit())
	}
	if err := ensureDir(p.dir, c.uid()); err != nil {
		return nil, err
	}
	return c.listenAt(p.path)
}

// rebindAt makes a new listener at path again, after checking the directory.
func (c *Channel) rebindAt(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := ensureDir(dir, c.uid()); err != nil {
		return nil, err
	}
	return c.listenAt(path)
}

// listenAt removes a socket of ours left at path, listens, and checks what it
// made.
func (c *Channel) listenAt(path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		// Only a socket owned by this user is ours to remove. The name is
		// per instance, so this is a leftover or a replaced file.
		if fi.Mode()&os.ModeSocket == 0 || ownerOf(fi) != uint32(c.uid()) {
			return nil, fmt.Errorf("something that is not this instance's socket is in the way (mode %v)", fi.Mode())
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var ln net.Listener
	var err error
	if bind := c.cfg.Seams.Bind; bind != nil {
		ln, err = bind(path)
	} else {
		ln, err = net.Listen("unix", path)
	}
	if err != nil {
		return nil, err
	}
	// The directory is 0700 and ours, so nobody else can reach the file
	// between the bind and this.
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	if err := checkSocket(path, c.uid()); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// verify says whether the socket is still there and still ours, and the
// directory still as it should be.
func (c *Channel) verify() error {
	c.mu.Lock()
	path := c.path
	c.mu.Unlock()
	if err := checkDir(filepath.Dir(path), c.uid()); err != nil {
		return err
	}
	return checkSocket(path, c.uid())
}

// ownerOf is a variable so that a test can make a file look like it belongs to
// somebody else.
var ownerOf = ownerFromStat

func ownerFromStat(fi os.FileInfo) uint32 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Uid
	}
	return ^uint32(0)
}

// checkSocket wants a socket, owned by uid, with mode 0600.
func checkSocket(path string, uid int) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s is not a socket", path)
	}
	if got := ownerOf(fi); got != uint32(uid) {
		return fmt.Errorf("%s is owned by user %d, not %d", path, got, uid)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		return fmt.Errorf("%s has mode %04o, not 0600", path, perm)
	}
	return nil
}

// checkDir wants a real directory (not a link), owned by uid, with mode
// exactly 0700.
func checkDir(dir string, uid int) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a link", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if got := ownerOf(fi); got != uint32(uid) {
		return fmt.Errorf("%s is owned by user %d, not %d", dir, got, uid)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		return fmt.Errorf("%s has mode %04o, not 0700", dir, perm)
	}
	return nil
}

// ensureDir makes dir with mode 0700 if it is missing and checks it with
// lstat, so that something planted between the two is seen. A directory that
// exists is checked and never changed.
func ensureDir(dir string, uid int) error {
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return err
		}
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	} else if err != nil {
		return err
	}
	return checkDir(dir, uid)
}

// defaultTmp is /tmp on Linux and the temporary directory elsewhere.
func defaultTmp() string {
	if runtime.GOOS == "linux" {
		return "/tmp"
	}
	return os.TempDir()
}

// Dial connects to the channel at path.
func Dial(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, timeout)
}
