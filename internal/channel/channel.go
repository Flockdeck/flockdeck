// Package channel is a private local channel to a running instance: a Unix
// domain socket on Linux and macOS, a named pipe on Windows. It is served in
// addition to the instance's loopback HTTP server, with a mux and a listener
// of its own, and answers two requests: who is there (identify) and a
// one-time window link (window). It has no other verb.
//
// Access is decided by the operating system before a request is read: the
// directory and socket file permissions on Unix, the pipe's access list on
// Windows. As a second check, every connection's peer is looked up and refused
// unless it belongs to the user the instance runs as.
package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// AppName is what identify says the instance is.
	AppName = "flockdeck"

	// MaxConns is how many connections are served at once. A connection over
	// that is closed as it arrives.
	MaxConns = 8
	// MaxHeaderBytes limits the request line and headers.
	MaxHeaderBytes = 4 << 10
	// MaxBodyBytes limits a request body. No verb takes one; a client that
	// sends more than this is refused.
	MaxBodyBytes = 1 << 10

	// WatchEvery is how often the socket is checked, and a missing one put
	// back. The command line re-probes for 15 seconds after it finds nothing,
	// so a rebind lands inside that.
	WatchEvery = 5 * time.Second
	// LostAfter is how many rebinds in a row may fail before the windows are
	// told the instance cannot be reached from the command line.
	LostAfter = 3

	// WindowsPerMinute is how many window links the channel mints in a minute.
	// A person asks for one now and then; a loop asking for hundreds is not a
	// person.
	WindowsPerMinute = 10

	// DenyLogEvery is the longest a refused connection is logged for: more in
	// that time are counted and reported with the next one.
	DenyLogEvery = time.Minute

	// LostMessage is what OnLost is told. Nothing a person does depends on the
	// channel yet, so it says only what stopped.
	LostMessage = "The local channel stopped and could not be started again. It is not used for anything you can see yet."

	// maxPath is the longest socket path used on Unix. sun_path holds 104
	// bytes on macOS and 108 on Linux, with the terminating NUL.
	maxPath = 100
)

// Identity is what identify answers.
type Identity struct {
	App     string    `json:"app"`
	PID     int       `json:"pid"`
	Version string    `json:"version,omitempty"`
	Started time.Time `json:"started"`
}

// Peer is who is on the other end of a connection. Owner is the user id on
// Unix and the user's SID on Windows, as text.
type Peer struct {
	PID   int
	Owner string
}

// Config describes the channel to start. Only StateDir, PID and Window are
// needed; the rest have defaults, and the Seams are for tests.
type Config struct {
	// StateDir is the instance's state directory. On Unix the socket goes in
	// a directory called c below it.
	StateDir string
	PID      int
	Started  time.Time
	Version  string

	// Window makes a one-time window link, exactly as the HTTP server's own
	// window request does. The channel adds no rules of its own to it.
	Window func() string

	// Logf is told of anything that goes wrong after start-up: a failed
	// rebind, a refused peer.
	Logf func(format string, args ...any)
	// OnLost is called once, with a message for the windows, when the socket
	// could not be put back LostAfter times in a row.
	OnLost func(message string)

	Seams Seams
}

// Seams are the points a test replaces. All are optional.
type Seams struct {
	// Interval replaces WatchEvery.
	Interval time.Duration
	// Backoff is how long to wait before retry number n (1 is the first) after
	// a failed rebind. The default doubles Interval each time, up to a minute.
	Backoff func(n int) time.Duration
	// UID replaces the user id the Unix checks compare against.
	UID func() int
	// Tmp replaces the directory the fallback location is made in.
	Tmp string
	// MaxPath replaces the longest Unix socket path.
	MaxPath int
	// Bind replaces the call that makes the listener at a path.
	Bind func(path string) (net.Listener, error)
	// ReadPeer replaces the lookup of who is on a connection.
	ReadPeer func(net.Conn) (Peer, error)
	// Self replaces who the peer must be.
	Self string
	// Now replaces the clock used for the window limit and the refusal log.
	Now func() time.Time
	// Seen is called with the peer of each connection that was let in.
	Seen func(Peer)
}

// Channel is a running channel.
type Channel struct {
	cfg  Config
	logf func(string, ...any)
	srv  *http.Server
	self string

	mu       sync.Mutex
	raw      net.Listener
	path     string
	closed   bool
	failures int
	lost     bool

	sem  chan struct{}
	quit chan struct{}
	wg   sync.WaitGroup

	denied atomic.Int64

	limitMu sync.Mutex
	windows []time.Time

	denyMu     sync.Mutex
	denyLast   time.Time
	denyQueued int

	// id is the socket file as it was made, so that Close removes that file and
	// not one that has since taken its place.
	id fileID
}

// Start makes the channel and begins serving it. It returns an error when no
// safe place for it can be made; the instance then runs without one.
func Start(cfg Config) (*Channel, error) {
	if cfg.Window == nil {
		return nil, errors.New("channel: no window verb")
	}
	c := &Channel{
		cfg:  cfg,
		logf: cfg.Logf,
		sem:  make(chan struct{}, MaxConns),
		quit: make(chan struct{}),
	}
	if c.logf == nil {
		c.logf = func(string, ...any) {}
	}
	c.self = cfg.Seams.Self
	if c.self == "" {
		var err error
		if c.self, err = selfOwner(); err != nil {
			return nil, err
		}
	}

	c.srv = &http.Server{
		Handler:           c.mux(),
		MaxHeaderBytes:    MaxHeaderBytes,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	ln, path, err := c.bind()
	if err != nil {
		return nil, err
	}
	c.raw, c.path = ln, path
	c.serve(ln)

	if watches {
		c.wg.Add(1)
		go c.watch()
	}
	return c, nil
}

// Path is where the channel listens: a socket path on Unix, a pipe name on
// Windows.
func (c *Channel) Path() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}

// Denied is how many connections were refused because of who was on the other
// end.
func (c *Channel) Denied() int64 { return c.denied.Load() }

// Lost reports whether the socket could not be put back and the windows were
// told.
func (c *Channel) Lost() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lost
}

// Close stops serving, drops every connection and removes the socket.
func (c *Channel) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.quit)
	raw := c.raw
	c.mu.Unlock()

	var err error
	if raw != nil {
		// The file at the path is removed below, and only if it is still the one
		// this channel made.
		if u, ok := raw.(interface{ SetUnlinkOnClose(bool) }); ok {
			u.SetUnlinkOnClose(false)
		}
		err = raw.Close()
	}
	c.removeSocket()
	if e := c.srv.Close(); err == nil {
		err = e
	}
	c.wg.Wait()
	return err
}

func (c *Channel) mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /identify", c.handleIdentify)
	mux.HandleFunc("POST /window", c.handleWindow)
	return mux
}

// drain reads and throws away the request body, and refuses one that is over
// the limit. It reports whether the request may go on.
func drain(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return false
	}
	return true
}

func (c *Channel) handleIdentify(w http.ResponseWriter, r *http.Request) {
	if !drain(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(Identity{
		App: AppName, PID: c.cfg.PID, Version: c.cfg.Version, Started: c.cfg.Started,
	})
}

func (c *Channel) handleWindow(w http.ResponseWriter, r *http.Request) {
	if !drain(w, r) {
		return
	}
	if !c.allowWindow() {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many window links asked for", http.StatusTooManyRequests)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, c.cfg.Window())
}

// serve starts serving ln, wrapped in the connection checks.
func (c *Channel) serve(ln net.Listener) {
	g := &guard{Listener: ln, c: c}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		_ = c.srv.Serve(g)
	}()
}

// guard checks each connection as it arrives: there is room for it, and the
// peer is the user the instance runs as.
type guard struct {
	net.Listener
	c *Channel
}

func (g *guard) Accept() (net.Conn, error) {
	for {
		conn, err := g.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case g.c.sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		if deferredPeer && g.c.cfg.Seams.ReadPeer == nil {
			// The peer can only be known once it has sent something: see
			// lateConn.
			return &slotConn{Conn: &lateConn{Conn: conn, c: g.c}, release: func() { <-g.c.sem }}, nil
		}
		if err := g.c.checkPeer(conn); err != nil {
			<-g.c.sem
			g.c.refuse(err)
			conn.Close()
			continue
		}
		return &slotConn{Conn: conn, release: func() { <-g.c.sem }}, nil
	}
}

func (c *Channel) checkPeer(conn net.Conn) error {
	read := c.cfg.Seams.ReadPeer
	if read == nil {
		read = readPeer
	}
	p, err := read(conn)
	if err != nil {
		return fmt.Errorf("could not tell who is connecting: %w", err)
	}
	if p.Owner != c.self {
		return fmt.Errorf("peer %d belongs to %q, not to this user", p.PID, p.Owner)
	}
	if c.cfg.Seams.Seen != nil {
		c.cfg.Seams.Seen(p)
	}
	return nil
}

// slotConn gives its place back when it is closed.
type slotConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (s *slotConn) Close() error {
	s.once.Do(s.release)
	return s.Conn.Close()
}

// ID is the per-instance part of the socket or pipe name: eight hex digits of
// a hash of the process id and start time.
func ID(pid int, started time.Time) string {
	sum := sha256.Sum256([]byte(strconv.Itoa(pid) + "/" + strconv.FormatInt(started.UnixNano(), 10)))
	return hex.EncodeToString(sum[:4])
}

func (c *Channel) interval() time.Duration {
	if c.cfg.Seams.Interval > 0 {
		return c.cfg.Seams.Interval
	}
	return WatchEvery
}

func (c *Channel) backoff(n int) time.Duration {
	if f := c.cfg.Seams.Backoff; f != nil {
		return f(n)
	}
	d := c.interval() << n
	if d > time.Minute || d <= 0 {
		d = time.Minute
	}
	return d
}

// watch looks at the socket every interval and puts it back if it has gone.
func (c *Channel) watch() {
	defer c.wg.Done()
	wait := c.interval()
	for {
		t := time.NewTimer(wait)
		select {
		case <-c.quit:
			t.Stop()
			return
		case <-t.C:
		}
		wait = c.interval()
		if c.verify() == nil {
			continue
		}
		if err := c.rebind(); err != nil {
			c.mu.Lock()
			c.failures++
			n := c.failures
			tell := n >= LostAfter && !c.lost
			if tell {
				c.lost = true
			}
			c.mu.Unlock()
			c.logf("local channel: could not put the socket back (attempt %d): %v", n, err)
			if tell && c.cfg.OnLost != nil {
				c.cfg.OnLost(LostMessage)
			}
			wait = c.backoff(n)
			continue
		}
		c.mu.Lock()
		c.failures = 0
		c.lost = false
		c.mu.Unlock()
	}
}

// rebind replaces the listener with a new one at the same place. The old
// listener is told not to remove the path when it closes, because by now
// whatever is at the path may not be its own file.
func (c *Channel) rebind() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("closed")
	}
	old, path := c.raw, c.path
	c.mu.Unlock()

	if u, ok := old.(interface{ SetUnlinkOnClose(bool) }); ok {
		u.SetUnlinkOnClose(false)
	}
	_ = old.Close()

	ln, err := c.rebindAt(path)
	if err != nil {
		// Serving has stopped with the old listener; the next attempt starts
		// from a closed one, which Close tolerates.
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		ln.Close()
		return errors.New("closed")
	}
	c.raw = ln
	c.mu.Unlock()
	c.serve(ln)
	return nil
}

// Client is an HTTP client that sends its requests to the channel at path.
// The host in a URL is ignored; use http://channel/identify.
func Client(path string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return Dial(path, 5*time.Second)
			},
		},
	}
}

func (c *Channel) now() time.Time {
	if f := c.cfg.Seams.Now; f != nil {
		return f()
	}
	return time.Now()
}

// allowWindow counts a window request against WindowsPerMinute.
func (c *Channel) allowWindow() bool {
	now := c.now()
	c.limitMu.Lock()
	defer c.limitMu.Unlock()
	kept := c.windows[:0]
	for _, t := range c.windows {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	c.windows = kept
	if len(c.windows) >= WindowsPerMinute {
		return false
	}
	c.windows = append(c.windows, now)
	return true
}

// refuse counts a connection turned away because of who it came from, and
// logs it: the first one at once, then at most one line a DenyLogEvery that
// says how many there were.
func (c *Channel) refuse(err error) {
	now := c.now()
	c.denyMu.Lock()
	c.denyQueued++
	n := c.denyQueued
	say := c.denyLast.IsZero() || now.Sub(c.denyLast) >= DenyLogEvery
	if say {
		c.denyLast, c.denyQueued = now, 0
	}
	c.denyMu.Unlock()
	if say {
		if n == 1 {
			c.logf("local channel: refused a connection: %v", err)
		} else {
			c.logf("local channel: refused %d connections since the last report; the latest: %v", n, err)
		}
	}
	c.denied.Add(1)
}

// lateConn checks its peer when the first bytes arrive, for systems where the
// peer cannot be known before that (Windows: a pipe's client can only be
// impersonated once it has written). The bytes are dropped if the check fails.
type lateConn struct {
	net.Conn
	c    *Channel
	once sync.Once
	err  error
}

func (l *lateConn) Read(b []byte) (int, error) {
	n, err := l.Conn.Read(b)
	if n > 0 {
		l.once.Do(func() {
			if l.err = l.c.checkPeer(l.Conn); l.err != nil {
				l.c.refuse(l.err)
				l.Conn.Close()
			}
		})
		if l.err != nil {
			return 0, l.err
		}
	}
	return n, err
}
