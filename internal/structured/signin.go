package structured

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/agentlogin"
)

// Signing in from the pane. A pane in PhaseSetup whose adapter listed the login
// method (agentlogin.MethodID) can start the agent's own login command through
// a second, short-lived adapter process: SignIn starts it, the sign-in address
// it sends back is kept for the window to show as a link, and when the command
// ends the owner is called to restart the pane. The login belongs to the
// adapter, so what the agent is and what its command is called are not known
// here. Flockdeck holds no credential; the person signs in on the agent's page.
//
// Only the address is passed on, and only after the adapter's host check and
// the shape check here (agentlogin.CleanShape). The agent's words, in a
// notification or an error, never reach the snapshot.

// States of a sign-in, as the snapshot says them.
const (
	// SignInStarting: the adapter is starting and the command has not printed an
	// address yet.
	SignInStarting = "starting"
	// SignInWaiting: the address is known and the command waits for the person.
	SignInWaiting = "waiting"
	// SignInDone: the command ended well. The owner restarts the pane.
	SignInDone = "done"
	// SignInFailed: the command failed, timed out or could not start.
	SignInFailed = "failed"
)

// signInTimeout bounds a sign-in from the first start of the adapter. The
// adapter's own bound (agentlogin.DefaultTimeout) is shorter; this one is for an
// adapter that does not keep it.
const signInTimeout = agentlogin.DefaultTimeout + time.Minute

var (
	// ErrNoSignIn: the pane is not waiting for a login, or its adapter has no
	// login method of Flockdeck's.
	ErrNoSignIn = errors.New("structured: this pane cannot be signed in from here")
	// ErrSignInBusy: a sign-in is already running in this pane.
	ErrSignInBusy = errors.New("structured: a sign-in is already running")
)

// SignIn is the state of a sign-in.
type SignIn struct {
	State string
	// URL is the address to open; empty until the command printed one.
	URL string
}

type signInRun struct {
	cancel    context.CancelFunc
	done      chan struct{}
	state     string
	url       string
	cancelled bool
}

// CanSignIn says the pane waits for a login and its adapter can run one.
func (s *Setup) CanSignIn() bool {
	return s != nil && s.Kind == SetupLogin && slices.ContainsFunc(s.Methods, func(m AuthMethod) bool { return m.ID == agentlogin.MethodID })
}

// SignIn starts the sign-in. onDone is called, on a goroutine of its own, once
// when the command ended well and the pane should be restarted; it is not
// called for a failure or a cancel. Only one sign-in runs at a time.
func (r *Runtime) SignIn(onDone func()) error {
	r.mu.Lock()
	if r.signinOff {
		r.mu.Unlock()
		return ErrNotRunning
	}
	if r.phase != PhaseSetup || !r.setup.CanSignIn() {
		r.mu.Unlock()
		return ErrNoSignIn
	}
	if run := r.signin; run != nil && (run.state == SignInStarting || run.state == SignInWaiting) {
		r.mu.Unlock()
		return ErrSignInBusy
	}
	ctx, cancel := context.WithTimeout(context.Background(), signInTimeout)
	run := &signInRun{cancel: cancel, done: make(chan struct{}), state: SignInStarting}
	r.signin = run
	r.mu.Unlock()
	r.notify()
	go r.runSignIn(ctx, run, onDone)
	return nil
}

// SignInCancel ends a running sign-in and forgets it. The command and the
// adapter are gone when it returns.
func (r *Runtime) SignInCancel() error {
	r.mu.Lock()
	run := r.signin
	if run == nil || (run.state != SignInStarting && run.state != SignInWaiting) {
		r.mu.Unlock()
		return ErrNoSignIn
	}
	run.cancelled = true
	r.mu.Unlock()
	run.cancel()
	<-run.done
	return nil
}

// stopSignIn ends a sign-in that is running, when the pane closes.
func (r *Runtime) stopSignIn() {
	r.mu.Lock()
	run := r.signin
	if run != nil {
		run.cancelled = true
	}
	r.mu.Unlock()
	if run != nil {
		run.cancel()
		select {
		case <-run.done:
		case <-time.After(closeWait):
		}
	}
}

func (r *Runtime) setSignIn(run *signInRun, state, url string) {
	r.mu.Lock()
	run.state = state
	if url != "" {
		run.url = url
	}
	r.mu.Unlock()
	r.notify()
}

// quiet is the handler of the sign-in's adapter: it asks for nothing.
type quiet struct{}

func (quiet) Update(acp.Update)                                        {}
func (quiet) Permission(context.Context, acp.PermissionRequest) string { return "" }

func (r *Runtime) runSignIn(ctx context.Context, run *signInRun, onDone func()) {
	defer close(run.done)
	defer run.cancel()
	err := r.signInProcess(ctx, run)
	r.mu.Lock()
	cancelled := run.cancelled
	r.mu.Unlock()
	switch {
	case cancelled:
		r.mu.Lock()
		if r.signin == run {
			r.signin = nil
		}
		r.mu.Unlock()
		r.notify()
	case err != nil:
		r.setSignIn(run, SignInFailed, "")
	default:
		r.setSignIn(run, SignInDone, "")
		if onDone != nil {
			go onDone()
		}
	}
}

// signInProcess runs the adapter, asks it to authenticate with the login method
// and returns when it answers. The adapter and its command are ended before it
// returns.
func (r *Runtime) signInProcess(ctx context.Context, run *signInRun) error {
	cmd, _, err := r.resolve()
	if err != nil {
		return err
	}
	cfg := acp.Config{
		Path:       cmd.Path,
		Args:       cmd.Args,
		Dir:        r.opts.Cwd,
		Env:        cmd.env,
		Handler:    quiet{},
		ClientName: "flockdeck",
		OnExtension: func(method string, params json.RawMessage) {
			if method != agentlogin.URLNotification {
				return
			}
			var p struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(params, &p) != nil {
				return
			}
			u, ok := agentlogin.CleanShape(p.URL)
			if !ok {
				return
			}
			r.mu.Lock()
			first := run.url == ""
			r.mu.Unlock()
			if first {
				r.setSignIn(run, SignInWaiting, u)
			}
		},
	}
	if r.opts.Client != nil {
		r.opts.Client(&cfg)
	}
	c, err := acp.Start(cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	init, err := c.Initialize(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(init.AuthMethods, func(m acp.AuthMethod) bool { return m.ID == agentlogin.MethodID }) {
		return ErrNoSignIn
	}
	return c.AuthenticateWait(ctx, agentlogin.MethodID)
}
