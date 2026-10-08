package gitx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// This file is the only way the scan touches the disk. programs.go and the
// files beside it call the methods here and never os.Stat, os.Open,
// filepath.EvalSymlinks and the rest (a test fails if they do).
//
// The reason is Windows. Looking at a path that is, or leads through a link to,
// a network share makes Windows connect to the share and offer the user's
// credentials, and a repository can name one in a dozen places: a hooks path, a
// remote, a .git file, a link where a hook should be. Checking each place by
// hand missed some, so every call here first walks the path one component at a
// time, using Lstat on local components and reading each link without opening
// what it leads to, and refuses a share it can recognise with errNetworkPath
// before any call that follows links. Only a path that passes is handed on.
//
// What this does not do, and the help page says so: a mapped network drive
// (Z:\) and a subst drive look local and are not recognised; the path can change
// between the walk and the call that follows it; and git itself reads what it is
// pointed at (a .git file, core.worktree, an include.path) when Flockdeck asks
// it a question.
//
// Every call gives up after statTimeout. A call that never returns holds its
// goroutine, so each scan may have at most maxSlots outstanding (more are
// refused as "did not answer"), and the whole program tolerates maxLeaked of
// them at once before it refuses every call, which is logged once. They are
// counted down as they return, so a share that comes back frees them.

// errNetworkPath is a path that is, or leads to, a network share. Callers turn
// it into an item and carry on.
var errNetworkPath = errors.New("a network share, which Flockdeck does not touch")

// errNoAnswer is a file system call that did not return in time, or could not
// be started because too many were already waiting.
var errNoAnswer = errors.New("no answer from the file system")

// errTooManyLinks is a path whose links are too many, too deep or lead back to
// themselves to be followed by hand, or a scan that has already looked at as
// many paths as it will.
var errTooManyLinks = errors.New("too many links to follow, or a link that leads back to itself")

// errNotRegular is a file that is not an ordinary file (a pipe or a device),
// which a read could wait on for ever.
var errNotRegular = errors.New("not an ordinary file, so it is not opened")

// errNoGitDir says a folder is not a checked-out repository.
var errNoGitDir = errors.New("not a git directory")

const (
	// maxLinkDepth is how many links deep a path is followed.
	maxLinkDepth = 8
	// maxSlots is how many calls one scan may have outstanding.
	maxSlots = 16
)

// statTimeout bounds one look at the file system for something a repository
// names. maxLeaked is how many abandoned calls the program carries before it
// stops making them. Both are variables so a test can change them.
var (
	statTimeout = 2 * time.Second
	maxLeaked   = 64
	// maxWalkSteps is how many path components one guard call visits, links
	// followed included.
	maxWalkSteps = 256
	// maxScanOps is how many links and directory entries one scan reads that it
	// has not read already.
	maxScanOps = 8192
)

var (
	leaked  atomic.Int64
	leakLog sync.Once
)

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
	return errors.Is(err, errNetworkPath) || errors.Is(err, errNoAnswer) ||
		errors.Is(err, errTooManyLinks) || errors.Is(err, errNotRegular)
}

// fsItem is the item for a path the layer refused or gave up on, or that could
// not be read.
func fsItem(err error, where, submodule string) Program {
	switch {
	case errors.Is(err, errNetworkPath):
		return unscannable(where, errNetworkPath.Error(), submodule)
	case errors.Is(err, errNoAnswer):
		return unscannable(where, "the file system did not answer", submodule)
	case errors.Is(err, errTooManyLinks):
		return unscannable(where, errTooManyLinks.Error(), submodule)
	case errors.Is(err, errNotRegular):
		return unscannable(where, errNotRegular.Error(), submodule)
	}
	return unreadable(where, err, submodule)
}

// scanFS is one scan's view of the disk. It remembers what it has looked at, so
// a path shared by many hooks, submodules and remotes is walked once.
type scanFS struct {
	ctx    context.Context
	slots  chan struct{}
	lstats map[string]lstatResult
	links  map[string]linkResult
	guards map[string]error
	ops    int // links and entries read that were not read before
	calls  int // calls made to the file system, for tests and reports
}

type lstatResult struct {
	fi  fs.FileInfo
	err error
}

type linkResult struct {
	real string
	err  error
}

// lastScanCalls is how many file system calls the most recent scan made. It is
// there for tests that guard against a scan getting expensive.
var lastScanCalls atomic.Int64

func newScanFS(ctx context.Context) *scanFS {
	return &scanFS{
		ctx:    ctx,
		slots:  make(chan struct{}, maxSlots),
		lstats: map[string]lstatResult{},
		links:  map[string]linkResult{},
		guards: map[string]error{},
	}
}

// boxed runs fn and gives up on it after statTimeout, or when the scan ends. A
// call given up on is left to finish: it keeps its slot and is counted as
// leaked until it does.
func boxed[T any](f *scanFS, fn func() (T, error)) (T, error) {
	var zero T
	if leaked.Load() >= int64(maxLeaked) {
		leakLog.Do(func() {
			log.Printf("gitx: %d file system calls that never returned are still waiting; refusing more until they do", maxLeaked)
		})
		return zero, errNoAnswer
	}
	select {
	case f.slots <- struct{}{}:
	default:
		return zero, errNoAnswer
	}
	f.calls++
	type result struct {
		v   T
		err error
	}
	var state atomic.Int32 // 0 running, 1 returned, 2 given up on
	ch := make(chan result, 1)
	go func() {
		v, err := fn()
		<-f.slots
		if !state.CompareAndSwap(0, 1) {
			leaked.Add(-1) // given up on, and back at last
		}
		ch <- result{v, err}
	}()
	timer := time.NewTimer(statTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-timer.C:
	case <-f.ctx.Done():
	}
	if state.CompareAndSwap(0, 2) {
		leaked.Add(1)
		return zero, errNoAnswer
	}
	r := <-ch // it returned just as the wait ended
	return r.v, r.err
}

// isNetworkSpelling reports whether a Windows path is spelled as a place on
// another machine's share, or in a namespace this does not recognise as local.
// It decides from the spelling alone, before anything is opened, and errs on
// the side of "network": the forms that reach the share through a device or an
// NT namespace (\??\UNC\, \\?\UNC\, \\?\GLOBALROOT\, \\.\UNC\, \Device\Mup\) all
// read as a share, and only a drive letter spelled one of the long ways is
// local.
func isNetworkSpelling(p string) bool {
	p = strings.ReplaceAll(p, "/", `\`)
	upper := strings.ToUpper(p)
	isDrive := func(s string) bool {
		return len(s) >= 2 && s[1] == ':' && (s[0] >= 'A' && s[0] <= 'Z' || s[0] >= 'a' && s[0] <= 'z') &&
			(len(s) == 2 || s[2] == '\\')
	}
	// The long and device forms: local only when a drive follows.
	for _, prefix := range []string{`\\?\`, `\\.\`, `\??\`} {
		if strings.HasPrefix(upper, prefix) {
			return !isDrive(p[len(prefix):])
		}
	}
	// \\host\share, and any other two-backslash spelling.
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	// A path rooted at the current drive is local, unless it names an NT
	// namespace or device, which Win32 hands on as they are.
	if strings.HasPrefix(p, `\`) {
		first, _, _ := strings.Cut(strings.TrimPrefix(upper, `\`), `\`)
		switch first {
		case "??", "GLOBAL??", "DOSDEVICES", "DEVICE", "GLOBALROOT", "UNC", "MUP", "OBJECTTYPES", "REGISTRY":
			return true
		}
	}
	return false
}

func isNetworkPath(p string) bool {
	return runtime.GOOS == "windows" && isNetworkSpelling(p)
}

// walkState counts what one guard call has visited.
type walkState struct{ steps int }

// guard walks p component by component and returns an error if p, or any link
// on the way to it, is a share, or if the walk cannot be finished. The answer is
// kept for the rest of the scan.
func (f *scanFS) guard(p string) error {
	p = filepath.Clean(p)
	if err, ok := f.guards[p]; ok {
		return err
	}
	_, err := f.walk(p, 0, &walkState{})
	f.guards[p] = err
	return err
}

// walk returns p with every link on the way resolved by hand, or an error. A
// relative link target is read against the real directory the link is in, which
// is the path resolved so far (Go's EvalSymlinks leaves a junction alone, so it
// cannot be asked for that). Each link is resolved once per scan, and one that
// is being resolved when it is met again is a cycle.
func (f *scanFS) walk(p string, depth int, st *walkState) (string, error) {
	if isNetworkPath(p) {
		return "", netErr(p)
	}
	if runtime.GOOS != "windows" {
		return p, nil
	}
	if depth > maxLinkDepth {
		return "", fmt.Errorf("%w: %s", errTooManyLinks, p)
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
		if st.steps++; st.steps > maxWalkSteps {
			return "", fmt.Errorf("%w: %s", errTooManyLinks, p)
		}
		if f.ctx.Err() != nil {
			return "", errNoAnswer
		}
		at := filepath.Join(real, comp)
		fi, err := f.lstat(at)
		if errors.Is(err, errNoAnswer) || errors.Is(err, errTooManyLinks) {
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
		resolved, err := f.link(at, real, depth, st)
		if err != nil {
			return "", err
		}
		real = resolved
	}
	return real, nil
}

// lstat is os.Lstat for the walk, remembered for the rest of the scan.
func (f *scanFS) lstat(at string) (fs.FileInfo, error) {
	if r, ok := f.lstats[at]; ok {
		return r.fi, r.err
	}
	if f.ops++; f.ops > maxScanOps {
		return nil, errTooManyLinks
	}
	fi, err := boxed(f, func() (fs.FileInfo, error) { return os.Lstat(at) })
	f.lstats[at] = lstatResult{fi, err}
	return fi, err
}

// link resolves the link at at, in the real directory realParent, to the real
// path it leads to.
func (f *scanFS) link(at, realParent string, depth int, st *walkState) (string, error) {
	if r, ok := f.links[at]; ok {
		return r.real, r.err
	}
	// Met again while being resolved is a cycle.
	f.links[at] = linkResult{err: fmt.Errorf("%w: %s", errTooManyLinks, at)}
	if f.ops++; f.ops > maxScanOps {
		return "", errTooManyLinks
	}
	t, err := boxed(f, func() (string, error) { return readLink(at) })
	var res linkResult
	switch {
	case errors.Is(err, errNoAnswer):
		res = linkResult{err: err}
	case err != nil:
		// A reparse point that is not a link (a cloud placeholder): it is where
		// it is.
		res = linkResult{real: at}
	case isNetworkPath(t):
		res = linkResult{err: netErr(t)}
	default:
		if !filepath.IsAbs(t) {
			t = filepath.Join(realParent, t)
		}
		real, werr := f.walk(filepath.Clean(t), depth+1, st)
		res = linkResult{real: real, err: werr}
	}
	f.links[at] = res
	return res.real, res.err
}

// Stat is os.Stat, after the guard.
func (f *scanFS) Stat(p string) (fs.FileInfo, error) {
	if err := f.guard(p); err != nil {
		return nil, err
	}
	return boxed(f, func() (fs.FileInfo, error) { return os.Stat(p) })
}

// Lstat is for the entry itself. It still refuses an entry that is a link to a
// share, since that is something to report and not to look at further.
func (f *scanFS) Lstat(p string) (fs.FileInfo, error) {
	if err := f.guard(p); err != nil {
		return nil, err
	}
	return boxed(f, func() (fs.FileInfo, error) { return os.Lstat(p) })
}

// ReadFile reads an ordinary file. Anything else (a pipe, a device) is
// errNotRegular without being opened: opening a pipe with nothing writing to it
// waits for ever.
func (f *scanFS) ReadFile(p string) ([]byte, error) {
	fi, err := f.Stat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", errNotRegular, p)
	}
	return boxed(f, func() ([]byte, error) { return os.ReadFile(p) })
}

// dirReader reads a directory a few entries at a time.
type dirReader struct {
	f *scanFS
	d *os.File
}

// OpenDir opens a folder; anything else is errNotRegular.
func (f *scanFS) OpenDir(p string) (*dirReader, error) {
	fi, err := f.Stat(p)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s", errNotRegular, p)
	}
	d, err := boxed(f, func() (*os.File, error) { return os.Open(p) })
	if err != nil {
		return nil, err
	}
	return &dirReader{f, d}, nil
}

func (d *dirReader) next(n int) ([]fs.DirEntry, error) {
	return boxed(d.f, func() ([]fs.DirEntry, error) { return d.d.ReadDir(n) })
}

func (d *dirReader) close() { d.d.Close() }

// Hash hashes up to limit bytes of an ordinary file, streamed, and returns the
// hash and how many bytes were read. It stops when the scan's context ends.
func (f *scanFS) Hash(p string, limit int64) (string, int64, error) {
	fi, err := f.Stat(p)
	if err != nil {
		return "", 0, err
	}
	if !fi.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%w: %s", errNotRegular, p)
	}
	type result struct {
		sum string
		n   int64
	}
	r, err := boxed(f, func() (result, error) {
		fh, err := os.Open(p)
		if err != nil {
			return result{}, err
		}
		defer fh.Close()
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(fh, limit))
		return result{hex.EncodeToString(h.Sum(nil))[:16], n}, err
	})
	return r.sum, r.n, err
}

// Canonical is p with symbolic links and, on Windows, short 8.3 names resolved,
// so the same directory is one name however it was reached: a worktree under
// /var on macOS is under /private/var as far as git is told, and a temporary
// folder on Windows has a long name and a short one. A path that cannot be
// resolved comes back cleaned, with no error; one that is or leads to a share
// comes back cleaned, with errNetworkPath, and is not resolved.
func (f *scanFS) Canonical(p string) (string, error) {
	p = filepath.Clean(p)
	if err := f.guard(p); err != nil {
		return p, err
	}
	real, err := boxed(f, func() (string, error) { return evalSymlinks(p) })
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

// canonical is Canonical for a caller that only wants a name to compare paths
// by. Whatever it is given is checked again by the call that opens it.
func (f *scanFS) canonical(p string) string {
	real, _ := f.Canonical(p)
	return real
}

// resolveGitDir is the git directory of a checked-out repository: its .git
// directory, or where a .git file says it is. Anything that leads to a share is
// errNetworkPath, a .git that is not an ordinary file or folder is
// errNotRegular, and a folder that is not a repository is errNoGitDir.
func (f *scanFS) resolveGitDir(work string) (string, error) {
	dot := filepath.Join(work, ".git")
	fi, err := f.Lstat(dot)
	if err != nil {
		if absent(err) {
			return "", errNoGitDir
		}
		return "", err
	}
	if fi.IsDir() {
		return dot, nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		// A .git that is a link to a folder is a git directory.
		if sfi, err := f.Stat(dot); err == nil && sfi.IsDir() {
			return dot, nil
		}
	}
	if !fi.Mode().IsRegular() && fi.Mode()&os.ModeSymlink == 0 {
		return "", fmt.Errorf("%w: %s", errNotRegular, dot)
	}
	if fi.Mode().IsRegular() && fi.Size() > 4096 {
		return "", errNoGitDir
	}
	data, err := f.ReadFile(dot)
	if err != nil {
		if blocked(err) {
			return "", err
		}
		return "", errNoGitDir
	}
	line, _, _ := strings.Cut(string(data), "\n")
	target, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return "", errNoGitDir
	}
	target = filepath.FromSlash(strings.TrimSpace(target))
	if !filepath.IsAbs(target) {
		target = filepath.Join(work, target)
	}
	target = filepath.Clean(target)
	if err := f.guard(target); err != nil {
		return "", err
	}
	return target, nil
}
