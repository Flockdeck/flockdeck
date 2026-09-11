package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/xtaci/smux"
)

// Subprotocol is what the tunnel is spoken as. A relay that does not know it
// is not one this build can talk to.
const Subprotocol = "flockdeck-relay.v1"

// The relay closes the tunnel with one of these when retrying would be wrong.
const (
	// CloseRevoked: the host's token is no longer accepted.
	CloseRevoked websocket.StatusCode = 4001
	// CloseReplaced: another connection has come in for the same host, and a
	// host has only one. Most often that is a second Flockdeck on this machine,
	// started with -solo; fighting it for the slot would have the two of them
	// knock each other off the relay every few seconds for as long as both run.
	CloseReplaced websocket.StatusCode = 4002
)

// State is where the connection to the relay stands.
type State string

const (
	StateOff        State = "off"
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateRevoked    State = "revoked"
	StateReplaced   State = "replaced"
	StateError      State = "error"
)

// Status is what the window and the command line are told about the tunnel.
type Status struct {
	State  State     `json:"state"`
	Detail string    `json:"detail,omitempty"`
	Since  time.Time `json:"since"`
	// RetryAt is when the next attempt is due, while one is being waited for.
	RetryAt time.Time `json:"retryAt,omitzero"`
	Relay   string    `json:"relay"`
	HostID  string    `json:"hostId"`
	Name    string    `json:"name,omitempty"`
}

// These are variables so that a test does not have to sit through them.
var (
	backoffMin  = time.Second
	backoffMax  = time.Minute
	dialTimeout = 20 * time.Second
	// stableAfter is how long a connection has to have lasted for the next
	// failure to count as a fresh one, rather than the latest in a run.
	stableAfter = time.Minute
	// closeWait bounds the goodbye when remote access is being turned off. The
	// relay learns of a dropped connection anyway; a polite close only lets it
	// say so sooner, and is not worth holding up a shutdown for.
	closeWait = time.Second
)

// smuxConfig is the multiplexer's configuration, which has to agree with the
// relay's: the version must match, and the buffers are what keep one busy
// terminal from starving every other stream on the tunnel.
func smuxConfig() *smux.Config {
	cfg := smux.DefaultConfig()
	cfg.Version = 2
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.KeepAliveTimeout = 45 * time.Second
	cfg.MaxReceiveBuffer = 4 << 20
	cfg.MaxStreamBuffer = 1 << 20
	return cfg
}

// Connector holds one host's tunnel to its relay open, and serves whatever
// arrives through it.
type Connector struct {
	cfg     Config
	version string
	// serve answers connections arriving through the tunnel. It is given a
	// listener that lasts as long as one tunnel does, and returns once that
	// listener is closed.
	serve func(net.Listener) error
	// changed is told whenever the status moves, so the window can show it.
	changed func()

	mu     sync.Mutex
	status Status
	cancel context.CancelFunc
	done   chan struct{}
}

// NewConnector prepares a tunnel for an enrolment; Start opens it.
func NewConnector(cfg Config, version string, serve func(net.Listener) error, changed func()) *Connector {
	return &Connector{
		cfg:     cfg,
		version: version,
		serve:   serve,
		changed: changed,
		status: Status{
			State: StateOff, Since: time.Now(),
			Relay: cfg.Relay, HostID: cfg.HostID, Name: cfg.Name,
		},
	}
}

// Status reports where the tunnel stands.
func (c *Connector) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Start opens the tunnel and keeps it open until Stop, or until the relay
// says there is no point.
func (c *Connector) Start() {
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	done := c.done
	c.mu.Unlock()
	go func() {
		defer close(done)
		c.run(ctx)
	}()
}

// Stop closes the tunnel, and every remote window with it, and waits for that
// to have happened.
func (c *Connector) Stop() {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// set records a new status and says so. A status that has not moved is not
// news, and the window is not woken for it.
func (c *Connector) set(state State, detail string, retryAt time.Time) {
	c.mu.Lock()
	st := c.status
	if st.State == state && st.Detail == detail && st.RetryAt.Equal(retryAt) {
		c.mu.Unlock()
		return
	}
	if st.State != state {
		st.Since = time.Now()
	}
	st.State, st.Detail, st.RetryAt = state, detail, retryAt
	c.status = st
	c.mu.Unlock()
	if c.changed != nil {
		c.changed()
	}
}

// errReplaced is the relay saying another connection has taken this host's
// place. This one does not come back of its own accord, even once the other
// has gone, so it says what does bring it back.
var errReplaced = errors.New("another Flockdeck has connected to the relay as this machine, so this one has stepped aside; restart this one to take remote access back")

// run is the life of the tunnel: connect, serve, and on losing it, wait and
// connect again — longer each time it keeps failing, and not at all once the
// relay has said that retrying is pointless.
func (c *Connector) run(ctx context.Context) {
	wait := backoffMin
	for {
		c.set(StateConnecting, "", time.Time{})
		began := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			c.set(StateOff, "", time.Time{})
			return
		}
		switch {
		case IsRevoked(err):
			c.set(StateRevoked, revokedDetail(err), time.Time{})
			return
		case errors.Is(err, errReplaced):
			c.set(StateReplaced, errReplaced.Error(), time.Time{})
			return
		}
		// A tunnel that held for a good while and then dropped is a new
		// problem, not the next failure of an old one, and is retried promptly.
		if time.Since(began) > stableAfter {
			wait = backoffMin
		}
		pause := jitter(wait)
		c.set(StateError, err.Error(), time.Now().Add(pause))
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			c.set(StateOff, "", time.Time{})
			return
		case <-timer.C:
		}
		wait = min(wait*2, backoffMax)
	}
}

// revokedDetail says what a revoked host means, and what to do about it.
func revokedDetail(err error) string {
	msg := "the relay no longer accepts this machine — it was removed, or its account was"
	var api *APIError
	if errors.As(err, &api) && api.Message != "" {
		msg += " (" + api.Message + ")"
	}
	return msg + "; run `flockdeck remote enable` to enrol it again"
}

// jitter spreads retries between half and all of d, so that every desktop that
// lost the relay when it restarted does not come back at the same instant.
func jitter(d time.Duration) time.Duration {
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + rand.N(half)
}

// dialFailure says why the relay could not be reached. The window puts it
// after "Cannot reach <relay>:", so it says what went wrong and not again
// where, and in words rather than as the error of a context or a handshake.
func dialFailure(err error, resp *http.Response) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("no answer within %s", dialTimeout)
	case resp != nil && resp.StatusCode != http.StatusSwitchingProtocols:
		return fmt.Errorf("it answered %s rather than opening the tunnel", resp.Status)
	}
	return unwrapURLError(err)
}

// session is one tunnel, from dialling to losing it.
func (c *Connector) session(ctx context.Context) error {
	dialCtx, cancelDial := context.WithTimeout(ctx, dialTimeout)
	defer cancelDial()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.cfg.Token)
	if c.version != "" {
		h.Set("Flockdeck-Version", c.version)
	}
	conn, resp, err := websocket.Dial(dialCtx, c.cfg.Relay+"/api/v1/host/connect", &websocket.DialOptions{
		HTTPHeader:   h,
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		// A refusal is the relay's to explain, and a 401 or 403 in particular
		// is the one answer that must stop the retrying.
		if resp != nil && resp.StatusCode >= 400 {
			var body []byte
			if resp.Body != nil {
				body, _ = io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			}
			return decodeError(resp.StatusCode, body)
		}
		return dialFailure(err, resp)
	}

	// The tunnel lives for as long as this session does, so the byte stream
	// over the WebSocket is bound to a context of its own rather than to ctx:
	// stopping is done below, in an order that lets the relay be told.
	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	rec := &recordingConn{
		Conn:   websocket.NetConn(streamCtx, conn, websocket.MessageBinary),
		failed: make(chan struct{}),
	}
	// smux never writes a frame near this size; the limit is here so that a
	// relay that has gone wrong is dropped rather than read without end. It
	// comes after NetConn, which lifts whatever limit was set before it.
	conn.SetReadLimit(1 << 20)
	sess, err := smux.Server(rec, smuxConfig())
	if err != nil {
		conn.CloseNow()
		return fmt.Errorf("start the tunnel: %w", err)
	}
	c.set(StateConnected, "", time.Time{})

	served := make(chan error, 1)
	go func() { served <- c.serve(listener{sess}) }()

	serving := true
	select {
	case <-ctx.Done():
		// Say goodbye, briefly. The relay would find out anyway; this only
		// lets it tell a watching device at once rather than at the next
		// keepalive.
		closed := make(chan struct{})
		go func() {
			_ = conn.Close(websocket.StatusNormalClosure, "remote access turned off")
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(closeWait):
		}
	case <-sess.CloseChan():
	// smux does not close a session whose connection has failed — it only
	// refuses to do anything more with it, and CloseChan stays open until
	// the keepalive gives up most of a minute later. The failure itself is
	// the news, so it is watched for directly.
	case <-rec.failed:
	case <-served:
		serving = false
	}
	_ = sess.Close()
	conn.CloseNow()
	cancelStream()
	if serving {
		// Closing the session closes the listener, which is what ends serve.
		select {
		case <-served:
		case <-time.After(5 * time.Second):
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return why(rec.err())
}

// why turns the way a tunnel ended into what to do next.
func why(err error) error {
	switch websocket.CloseStatus(err) {
	case CloseRevoked:
		// The close code is the relay's own, so it is a revocation with or
		// without a reason, and Revoked needs a message to see one.
		var ce websocket.CloseError
		errors.As(err, &ce)
		if ce.Reason == "" {
			ce.Reason = "it closed the tunnel as revoked"
		}
		return &APIError{Status: http.StatusUnauthorized, Message: ce.Reason}
	case CloseReplaced:
		return errReplaced
	}
	// The window shows this, so a close the relay gave a reason for says the
	// reason, and not the library's account of receiving it.
	var ce websocket.CloseError
	switch {
	case errors.As(err, &ce) && ce.Reason != "":
		return errors.New("the relay closed the connection: " + ce.Reason)
	case err == nil, errors.Is(err, io.EOF), errors.As(err, &ce):
		return errors.New("the relay closed the connection")
	}
	return fmt.Errorf("lost the relay: %w", err)
}

// recordingConn remembers the first thing that went wrong reading the tunnel.
//
// smux keeps the reason it stopped to itself, and the reason is the whole of
// what decides whether to come back: a WebSocket closed with CloseRevoked has
// to stop the retrying, and one that merely dropped has to start it.
type recordingConn struct {
	net.Conn
	// failed is closed on the first error, which is the moment the tunnel is
	// known to be gone.
	failed chan struct{}
	mu     sync.Mutex
	first  error
}

func (r *recordingConn) Read(p []byte) (int, error) {
	n, err := r.Conn.Read(p)
	if err != nil {
		r.mu.Lock()
		if r.first == nil {
			r.first = err
			close(r.failed)
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *recordingConn) err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.first
}

// listener is the tunnel seen as a listener: each stream the relay opens is a
// connection accepted, and serves one HTTP connection from a remote browser.
type listener struct{ sess *smux.Session }

func (l listener) Accept() (net.Conn, error) {
	st, err := l.sess.AcceptStream()
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (l listener) Close() error   { return l.sess.Close() }
func (l listener) Addr() net.Addr { return l.sess.LocalAddr() }
