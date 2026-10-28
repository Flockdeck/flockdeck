// Package agentlogin signs an agent in without a terminal. It runs the agent's
// own login command (`claude auth login`, `codex login`) as a background
// process, reads the sign-in address that command prints, and waits for it to
// end. The adapters use it to answer ACP's authenticate method.
//
// What it keeps to:
//
//   - Nothing the command prints is passed on except the address, and only
//     after Clean has accepted it. The rest of the output is read and thrown
//     away, never logged, and an error says only which stage failed.
//   - Flockdeck holds no credential. The person signs in on the agent's own
//     page, and the command stores the result where the agent keeps it.
//   - The command and everything it starts end when the context is cancelled,
//     when the timeout passes, or when Run returns.
package agentlogin

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/proctree"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

const (
	// MaxURL is the longest sign-in address accepted, in bytes. Claude's and
	// Codex's are well under 1000.
	MaxURL = 2048
	// DefaultTimeout is how long a sign-in may take from start to exit.
	DefaultTimeout = 5 * time.Minute
	// maxLine is the longest output line looked at; a longer one is skipped.
	maxLine = 8192
)

var (
	// ErrTimeout: the command did not end in time.
	ErrTimeout = errors.New("the sign-in timed out")
	// ErrCancelled: the context was cancelled.
	ErrCancelled = errors.New("the sign-in was cancelled")
	// ErrFailed: the command could not start or ended with a failure.
	ErrFailed = errors.New("the sign-in did not finish")
)

// Spec says what to run.
type Spec struct {
	// Path and Args are the login command, such as claude with "auth login".
	Path string
	Args []string
	Dir  string
	// Env is the whole environment of the command; nil is the process's own.
	Env []string
	// Hosts are the sign-in hosts accepted: a name matches itself and its
	// subdomains. An address on any other host is not passed on.
	Hosts []string
	// Timeout bounds the whole sign-in. Zero is DefaultTimeout.
	Timeout time.Duration
}

// Run starts the command, calls onURL once with the first address that passes
// Clean, and returns when the command has ended and its tree is gone. It
// returns nil only when the command exited with status 0.
func Run(ctx context.Context, sp Spec, onURL func(string)) error {
	timeout := sp.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.Command(sp.Path, sp.Args...)
	cmd.Dir = sp.Dir
	cmd.Env = sp.Env
	sysproc.NoWindow(cmd)
	// A pipe that is never written to and stays open until the end: a command
	// that reads its input finds no end of file and no text.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ErrFailed
	}
	lines := make(chan string, 16)
	// One writer for both streams, so exec copies them on one goroutine. A
	// descendant that keeps the pipes open after the command ends is waited for
	// only briefly.
	w := &lineWriter{out: lines}
	cmd.Stdout, cmd.Stderr = w, w
	cmd.WaitDelay = 2 * time.Second
	tree, err := proctree.Start(cmd)
	if err != nil {
		return ErrFailed
	}

	reaped := make(chan struct{})
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		close(reaped)
		waitErr <- err
	}()

	sent := false
	finish := func(err error) error {
		_ = stdin.Close()
		tree.End(cmd.Process, reaped, 0)
		select {
		case <-waitErr:
		case <-time.After(5 * time.Second):
		}
		return err
	}
	for {
		select {
		case l := <-lines:
			if sent {
				continue
			}
			if u, ok := FindURL(l, sp.Hosts); ok {
				sent = true
				onURL(u)
			}
		case err := <-waitErr:
			_ = stdin.Close()
			tree.End(cmd.Process, reaped, 0)
			if err != nil {
				return ErrFailed
			}
			return nil
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return finish(ErrTimeout)
			}
			return finish(ErrCancelled)
		}
	}
}

// lineWriter splits what is written to it into lines and sends each to out,
// dropping a line longer than maxLine and any line a full channel cannot take.
type lineWriter struct {
	out  chan<- string
	buf  []byte
	skip bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' || b == '\r' {
			if !w.skip && len(w.buf) > 0 {
				select {
				case w.out <- string(w.buf):
				default:
				}
			}
			w.buf, w.skip = w.buf[:0], false
			continue
		}
		if w.skip {
			continue
		}
		if len(w.buf) >= maxLine {
			w.buf, w.skip = w.buf[:0], true
			continue
		}
		w.buf = append(w.buf, b)
	}
	return len(p), nil
}

var (
	// ansi matches terminal escape sequences: CSI, OSC (ended by BEL or ST,
	// which is how hyperlinks are written) and two-character escapes.
	ansi = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")
	// candidate runs from "https://" to the next space, quote or bracket.
	candidate = regexp.MustCompile("https://[^\\s\"'`<>()\\[\\]{}\\\\^|]+")
	// osc8 is a terminal hyperlink; its address is what the text links to.
	osc8 = regexp.MustCompile("\x1b\\]8;[^;\x07\x1b]*;([^\x07\x1b]*)(?:\x07|\x1b\\\\)")
)

// FindURL returns the first address in a line of output that Clean accepts.
func FindURL(line string, hosts []string) (string, bool) {
	line = osc8.ReplaceAllString(line, " $1 ")
	line = ansi.ReplaceAllString(line, "")
	for _, c := range candidate.FindAllString(line, -1) {
		// Sentence punctuation after the address is not part of it.
		c = strings.TrimRight(c, ".,;:!?")
		if u, ok := Clean(c, hosts); ok {
			return u, true
		}
	}
	return "", false
}

// Clean returns raw as an address that is safe to show as a link, or false.
// The address must be https, on a host named by hosts (the name or a subdomain
// of it; every address is refused when hosts is empty), have no user name or
// password, no port and no IP address for a host, no control, space, format or
// non-ASCII character, and be at most MaxURL bytes. The result is the parsed
// address written out again.
func Clean(raw string, hosts []string) (string, bool) { return clean(raw, hosts, true) }

// CleanShape is Clean without the host check: for a client that gets an
// address from an adapter, which has checked the host against its agent's list.
func CleanShape(raw string) (string, bool) { return clean(raw, nil, false) }

func clean(raw string, hosts []string, checkHost bool) (string, bool) {
	if raw == "" || len(raw) > MaxURL || !utf8.ValidString(raw) {
		return "", false
	}
	for _, r := range raw {
		if r > unicode.MaxASCII || unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil {
		return "", false
	}
	if u.Port() != "" || strings.Contains(u.Host, ":") {
		return "", false
	}
	h := strings.ToLower(u.Hostname())
	if h == "" || strings.HasSuffix(h, ".") || net.ParseIP(h) != nil {
		return "", false
	}
	ok := false
	for _, a := range hosts {
		a = strings.ToLower(a)
		if a != "" && (h == a || strings.HasSuffix(h, "."+a)) {
			ok = true
			break
		}
	}
	if checkHost && !ok {
		return "", false
	}
	out := u.String()
	if len(out) > MaxURL {
		return "", false
	}
	return out, true
}

const (
	// MethodID is the id of the one authentication method the adapters list.
	// Calling authenticate with it starts the agent's login command.
	MethodID = "flockdeck-login"
	// MethodName is the method's name, fixed text.
	MethodName = "Sign in with the agent's login command"
	// URLNotification is the ACP extension notification the adapter sends with
	// the sign-in address: params {"url": "..."}. It is sent at most once per
	// sign-in, only after Clean accepted the address.
	URLNotification = "_flockdeck/signin_url"
)

// ErrBusy: a sign-in is already running in this adapter.
var ErrBusy = errors.New("a sign-in is already running")

// Runner lets one sign-in run at a time and ends it when closed.
type Runner struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	closed bool
}

// Do runs sp as Run does, unless a sign-in is running (ErrBusy) or the Runner
// is closed (ErrCancelled).
func (r *Runner) Do(ctx context.Context, sp Spec, onURL func(string)) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrCancelled
	}
	if r.cancel != nil {
		r.mu.Unlock()
		return ErrBusy
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.cancel, r.done = cancel, done
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		r.cancel, r.done = nil, nil
		r.mu.Unlock()
		close(done)
	}()
	return Run(ctx, sp, onURL)
}

// Close cancels a running sign-in, waits until its command's tree has ended,
// and refuses later ones.
func (r *Runner) Close() {
	r.mu.Lock()
	r.closed = true
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
