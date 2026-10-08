package gitx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// This file is the only way the scan touches the disk. programs.go and the
// files beside it call the functions here and never os.Stat, os.Open,
// filepath.EvalSymlinks and the rest (a test fails if they do).
//
// The reason is Windows. Looking at a path that is, or leads through a link to,
// a network share makes Windows connect to the share and offer the user's
// credentials, and a repository can name one in a dozen places: a hooks path, a
// remote, a .git file, a link where a hook should be. Checking each place by
// hand missed some, so every call here first walks the path one component at a
// time, using Lstat on local components and reading each link without opening
// what it leads to, and refuses with errNetworkPath if any step is a share.
// Only a path that passes is handed to the call that follows links.
//
// Every call also gives up after statTimeout, and at most maxInFlight calls are
// outstanding at once: one that never returns holds its slot, and when none is
// free the answer is errNoAnswer, so a hung share cannot pile up goroutines.

// errNetworkPath is a path that is, or leads to, a network share. Callers turn
// it into an item and carry on.
var errNetworkPath = errors.New("a network share, which Flockdeck does not touch")

// errNoAnswer is a file system call that did not return in time, or could not
// be started because too many were already waiting.
var errNoAnswer = errors.New("no answer from the file system")

const (
	// maxLinkDepth is how many links deep a path is followed. Past it the path is
	// treated as a share: a chain that long is not an ordinary checkout, and
	// letting the call that follows links resolve it would open whatever is at
	// the end.
	maxLinkDepth = 8
	maxInFlight  = 16
)

// statTimeout bounds one look at the file system for something a repository
// names. It is a variable so a test can shorten it.
var statTimeout = 2 * time.Second

var inflight = make(chan struct{}, maxInFlight)

// readLink and evalSymlinks are variables so a test can stand in for a link to a
// share (making one needs a privilege, and connects to the share) and see that a
// share is never resolved.
var (
	readLink     = os.Readlink
	evalSymlinks = filepath.EvalSymlinks
)

func netErr(p string) error { return fmt.Errorf("%w: %s", errNetworkPath, p) }

// blocked reports whether err is the layer refusing or giving up, as opposed to
// the file system saying the path is not there or cannot be read.
func blocked(err error) bool {
	return errors.Is(err, errNetworkPath) || errors.Is(err, errNoAnswer)
}

// fsItem is the item for a path the layer refused or gave up on, or that could
// not be read.
func fsItem(err error, where, submodule string) Program {
	switch {
	case errors.Is(err, errNetworkPath):
		return unscannable(where, errNetworkPath.Error(), submodule)
	case errors.Is(err, errNoAnswer):
		return unscannable(where, "the file system did not answer", submodule)
	}
	return unreadable(where, err, submodule)
}

// boxed runs f and gives up on it after statTimeout. The call is left to finish
// on its own and keeps its slot until it does; what it returns then is dropped.
func boxed[T any](f func() (T, error)) (T, error) {
	var zero T
	select {
	case inflight <- struct{}{}:
	default:
		return zero, errNoAnswer
	}
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := f()
		<-inflight
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(statTimeout):
		return zero, errNoAnswer
	}
}

// isNetworkPath reports whether p is spelled as a place on another machine's
// share. It is decided from the spelling alone, before anything is opened.
func isNetworkPath(p string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	p = filepath.FromSlash(p)
	if !strings.HasPrefix(p, `\\`) {
		return false
	}
	// \\?\C:\x is a local path spelled the long way.
	return !(strings.HasPrefix(p, `\\?\`) && len(p) >= 6 && p[5] == ':')
}

// guard walks p component by component and returns errNetworkPath if p, or any
// link on the way to it, is a share. A relative link target is read against the
// real directory the link is in. Nothing past a share is ever opened.
func guard(p string) error {
	_, err := walkPath(filepath.Clean(p), 0)
	return err
}

// walkPath returns p with every link on the way resolved by hand, or an error if
// one of them is a share. A relative link target is read against the real
// directory the link is in, which is the path resolved so far; Go's
// EvalSymlinks leaves a junction alone, so it cannot be asked for that.
func walkPath(p string, depth int) (string, error) {
	if isNetworkPath(p) {
		return "", netErr(p)
	}
	if runtime.GOOS != "windows" {
		return p, nil
	}
	if depth > maxLinkDepth {
		return "", netErr(p)
	}
	sep := string(filepath.Separator)
	vol := filepath.VolumeName(p)
	rest := strings.TrimPrefix(p[len(vol):], sep)
	real := vol + sep
	comps := strings.Split(rest, sep)
	for i, comp := range comps {
		if comp == "" {
			continue
		}
		at := filepath.Join(real, comp)
		fi, err := boxed(func() (fs.FileInfo, error) { return os.Lstat(at) })
		if errors.Is(err, errNoAnswer) {
			return "", err
		}
		if err != nil {
			// Not there, or not readable: the call the caller is about to make
			// reports that itself, and there is nothing past it to follow.
			return strings.Join(append([]string{at}, comps[i+1:]...), sep), nil
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
			real = at
			continue
		}
		t, err := boxed(func() (string, error) { return readLink(at) })
		if errors.Is(err, errNoAnswer) {
			return "", err
		}
		if err != nil {
			real = at
			continue
		}
		if isNetworkPath(t) {
			return "", netErr(t)
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(real, t)
		}
		resolved, err := walkPath(filepath.Clean(t), depth+1)
		if err != nil {
			return "", err
		}
		real = resolved
	}
	return real, nil
}

func fsStat(p string) (fs.FileInfo, error) {
	if err := guard(p); err != nil {
		return nil, err
	}
	return boxed(func() (fs.FileInfo, error) { return os.Stat(p) })
}

// fsLstat is for the entry itself. It still refuses an entry that is a link to a
// share, since that is something to report and not to look at further.
func fsLstat(p string) (fs.FileInfo, error) {
	if err := guard(p); err != nil {
		return nil, err
	}
	return boxed(func() (fs.FileInfo, error) { return os.Lstat(p) })
}

func fsReadFile(p string) ([]byte, error) {
	if err := guard(p); err != nil {
		return nil, err
	}
	return boxed(func() ([]byte, error) { return os.ReadFile(p) })
}

// dirReader reads a directory a few entries at a time.
type dirReader struct{ f *os.File }

func fsOpenDir(p string) (*dirReader, error) {
	if err := guard(p); err != nil {
		return nil, err
	}
	f, err := boxed(func() (*os.File, error) { return os.Open(p) })
	if err != nil {
		return nil, err
	}
	return &dirReader{f}, nil
}

func (d *dirReader) next(n int) ([]fs.DirEntry, error) {
	return boxed(func() ([]fs.DirEntry, error) { return d.f.ReadDir(n) })
}

func (d *dirReader) close() { d.f.Close() }

// fsHash hashes up to limit bytes of a file, streamed, and returns the hash and
// how many bytes were read. It stops when ctx ends.
func fsHash(ctx context.Context, p string, limit int64) (string, int64, error) {
	if err := guard(p); err != nil {
		return "", 0, err
	}
	select {
	case inflight <- struct{}{}:
	default:
		return "", 0, errNoAnswer
	}
	type result struct {
		sum string
		n   int64
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() { <-inflight }()
		f, err := os.Open(p)
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, limit))
		ch <- result{hex.EncodeToString(h.Sum(nil))[:16], n, err}
	}()
	select {
	case <-ctx.Done():
		return "", 0, ctx.Err()
	case r := <-ch:
		return r.sum, r.n, r.err
	}
}

// fsCanonical is p with symbolic links and, on Windows, short 8.3 names
// resolved, so the same directory is one name however it was reached: a
// worktree under /var on macOS is under /private/var as far as git is told, and
// a temporary folder on Windows has a long name and a short one. A path that
// cannot be resolved comes back cleaned, with no error; one that is or leads to
// a share comes back cleaned, with errNetworkPath, and is not resolved.
func fsCanonical(p string) (string, error) {
	p = filepath.Clean(p)
	if err := guard(p); err != nil {
		return p, err
	}
	real, err := boxed(func() (string, error) { return evalSymlinks(p) })
	if err != nil {
		if blocked(err) {
			return p, err
		}
		return p, nil
	}
	if isNetworkPath(real) {
		return p, netErr(real)
	}
	return real, nil
}

// canonical is fsCanonical for a caller that only wants a name to compare
// paths by. Whatever it is given is checked again by the call that opens it.
func canonical(p string) string {
	real, _ := fsCanonical(p)
	return real
}
