package acp

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

// The agent is untrusted. These tests are an agent that stops reading, floods,
// sends garbage, asks for too much, or never answers.

// startWith is start with a say in the Config.
func startWith(t *testing.T, r *recorder, mod func(*Config), opts ...acptest.Option) (*Client, string) {
	t.Helper()
	path, args, env := acptest.Command(t, opts...)
	cfg := Config{Path: path, Args: args, Env: env, Dir: acptest.SessionDir(t), Handler: r}
	if mod != nil {
		mod(&cfg)
	}
	c, err := Start(cfg)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	return c, sid
}

// within fails the test if f takes longer than d.
func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s took more than %v", what, d)
	}
}

// TestAnAgentThatStopsReadingCannotHangTheClient covers an agent that asked
// for permission and then stopped reading its input, with a large write already
// stuck in the pipe. Cancel, a call and Close must all return.
func TestAnAgentThatStopsReadingCannotHangTheClient(t *testing.T) {
	r := newRecorder()
	asked := make(chan struct{})
	r.perm = func(ctx context.Context, req PermissionRequest) string {
		close(asked)
		<-ctx.Done()
		return ""
	}
	c, sid := start(t, r)

	prompted := make(chan error, 2)
	go func() { _, err := c.Prompt(context.Background(), sid, "deaf-perm"); prompted <- err }()
	select {
	case <-asked:
	case <-time.After(wait):
		t.Fatal("no permission request")
	}
	// Eight MiB into a pipe nobody reads: the writer is stuck in this write.
	go func() { _, err := c.Prompt(context.Background(), sid, strings.Repeat("y", 8<<20)); prompted <- err }()
	time.Sleep(200 * time.Millisecond)

	within(t, 2*time.Second, "Cancel", func() { _ = c.Cancel(sid) })

	within(t, 5*time.Second, "a call with a deadline", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if _, err := c.Prompt(ctx, sid, "plain"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("call error = %v, want the deadline", err)
		}
	})

	within(t, 10*time.Second, "Close", func() { _ = c.Close() })
	for i := 0; i < 2; i++ {
		select {
		case <-prompted:
		case <-time.After(wait):
			t.Fatal("a prompt in flight was left waiting by Close")
		}
	}
}

func TestALineWithNoEndIsALostPeer(t *testing.T) {
	r := newRecorder()
	c, sid := startWith(t, r, func(c *Config) { c.MaxLineBytes = 1 << 20 })
	_, err := c.Prompt(ctxT(t), sid, "flood")
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("prompt error = %v, want ErrLineTooLong", err)
	}
	select {
	case <-c.Done():
	case <-time.After(wait):
		t.Fatal("the read loop did not end")
	}
	if c.ProtocolErrors() == 0 {
		t.Error("the overflow was not counted")
	}
	within(t, 10*time.Second, "Close", func() { _ = c.Close() })
}

func TestAStderrLineWithNoEndIsCutAndTheTurnGoesOn(t *testing.T) {
	r := newRecorder()
	var mu sync.Mutex
	longest := 0
	c2, sid2 := startWith(t, r, func(cfg *Config) {
		cfg.Stderr = func(l string) {
			mu.Lock()
			longest = max(longest, len(l))
			mu.Unlock()
		}
	})
	if stop := prompt(t, c2, sid2, "stderr-flood"); stop != StopEndTurn {
		t.Fatalf("stop reason = %q", stop)
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if longest == 0 || longest > 64<<10 {
		t.Errorf("longest stderr line delivered is %d bytes, want 1 to 65536", longest)
	}
	if n := len(c2.StderrTail()); n > 4096 {
		t.Errorf("stderr tail is %d bytes", n)
	}
}

func TestTooManyRequestsFromTheAgentAreRefused(t *testing.T) {
	r := newRecorder()
	var running, peak atomic.Int64
	release := make(chan struct{})
	r.perm = func(ctx context.Context, req PermissionRequest) string {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		defer running.Add(-1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ""
	}
	c, sid := startWith(t, r, func(cfg *Config) { cfg.MaxAgentRequests = 4 })
	t.Cleanup(func() { close(release) })

	stop := make(chan string, 1)
	go func() { s, _ := c.Prompt(ctxT(t), sid, "spam-perm"); stop <- s }()
	r.waitFor(t, "the agent's count", func(us []Update) bool { return strings.Contains(joinText(us), "answered=") })

	if got := joinText(r.all()); got != "answered=46" {
		t.Errorf("the agent saw %q, want the 46 requests over the cap answered with an error", got)
	}
	if p := peak.Load(); p != 4 {
		t.Errorf("handlers running at once = %d, want the cap of 4", p)
	}
	if err := c.Cancel(sid); err != nil {
		t.Fatal(err)
	}
	if s := <-stop; s != StopCancelled {
		t.Errorf("stop reason = %q", s)
	}
}

func joinText(us []Update) string {
	var b strings.Builder
	for _, u := range us {
		if u.Content != nil {
			b.WriteString(u.Content.Text)
		}
	}
	return b.String()
}

// TestACallAfterTheAgentExitedReturns covers a call made once the agent's
// output has ended, with a context that never ends.
func TestACallAfterTheAgentExitedReturns(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	_, _ = c.Prompt(ctxT(t), sid, "exit")
	select {
	case <-c.Done():
	case <-time.After(wait):
		t.Fatal("Done was not closed")
	}
	within(t, 5*time.Second, "a call after exit", func() {
		_, err := c.Prompt(context.Background(), sid, "plain")
		var exit *ExitError
		if !errors.As(err, &exit) {
			t.Errorf("error = %v, want an ExitError", err)
		}
	})
}

func TestAnAgentThatNeverAnswersEndsWithTheContextAndWithClose(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)

	within(t, 5*time.Second, "a call with a deadline", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if _, err := c.Prompt(ctx, sid, "silent"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want the deadline", err)
		}
	})

	errc := make(chan error, 1)
	go func() { _, err := c.Prompt(context.Background(), sid, "silent"); errc <- err }()
	time.Sleep(200 * time.Millisecond)
	within(t, 10*time.Second, "Close", func() { _ = c.Close() })
	select {
	case err := <-errc:
		if err == nil {
			t.Error("a call to an agent that never answers returned success")
		}
	case <-time.After(wait):
		t.Fatal("Close left a call waiting")
	}
}

func TestSetupCallsHaveTheirOwnTimeout(t *testing.T) {
	r := newRecorder()
	path, args, env := acptest.Command(t, acptest.WithMute())
	c, err := Start(Config{Path: path, Args: args, Env: env, Handler: r, SetupTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	// A mute agent reads and never answers.
	within(t, 5*time.Second, "initialize", func() {
		if _, err := c.Initialize(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want the setup timeout", err)
		}
	})
}

func TestGarbageFromTheAgentIsSurvivedAndCounted(t *testing.T) {
	r := newRecorder()
	var mu sync.Mutex
	var seen []string
	c, sid := startWith(t, r, func(cfg *Config) {
		cfg.OnProtocolError = func(d string) { mu.Lock(); seen = append(seen, d); mu.Unlock() }
	})
	if stop := prompt(t, c, sid, "garbage"); stop != StopEndTurn {
		t.Fatalf("stop reason = %q", stop)
	}
	if r.text() != "after garbage" {
		t.Errorf("text = %q", r.text())
	}
	// Not JSON, a response to nothing, a malformed update, then a duplicate
	// response to the prompt that already returned.
	deadline := time.Now().Add(wait)
	for c.ProtocolErrors() < 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := c.ProtocolErrors(); n != 4 {
		mu.Lock()
		t.Errorf("protocol errors = %d (%v), want 4", n, seen)
		mu.Unlock()
	}
	// Still working.
	r.reset()
	prompt(t, c, sid, "plain")
}

func TestARequestTheClientDoesNotKnowIsAnsweredWithAnError(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	prompt(t, c, sid, "unknown-method")
	if got := r.text(); got != "unknown-method error=-32601" {
		t.Errorf("the agent saw %q", got)
	}
}

func TestAResponseAfterCloseIsHarmless(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	errc := make(chan error, 1)
	go func() { _, err := c.Prompt(context.Background(), sid, "silent"); errc <- err }()
	time.Sleep(200 * time.Millisecond)
	_ = c.Close()
	<-errc

	// The agent's answer turns up anyway, and so does one to nothing.
	c.dispatch(rpcMsg{ID: rawID("3"), Result: []byte(`{"stopReason":"end_turn"}`)})
	c.dispatch(rpcMsg{ID: rawID("999")})
	c.dispatch(rpcMsg{Method: "session/update", Params: []byte(`{"sessionId":"s","update":{"sessionUpdate":"plan"}}`)})
	if c.ProtocolErrors() == 0 {
		t.Error("a response to nothing was not counted")
	}
}

func TestAPanickingHandlerDoesNotTakeTheClientDown(t *testing.T) {
	r := newRecorder()
	r.perm = func(context.Context, PermissionRequest) string { panic("handler bug") }
	c, sid := start(t, r)
	prompt(t, c, sid, "perm")
	if got := r.text(); got != "outcome=cancelled option=" {
		t.Errorf("a panicking Permission was answered as %q, want cancelled", got)
	}

	p := &panicOnUpdate{recorder: newRecorder()}
	c2, sid2 := startWithHandler(t, p)
	if stop := prompt(t, c2, sid2, "plain"); stop != StopEndTurn {
		t.Errorf("stop reason = %q", stop)
	}
	if c2.ProtocolErrors() != 2 {
		t.Errorf("handler panics counted = %d, want 2", c2.ProtocolErrors())
	}
}

type panicOnUpdate struct{ *recorder }

func (p *panicOnUpdate) Update(Update) { panic("handler bug") }

func startWithHandler(t *testing.T, h Handler) (*Client, string) {
	t.Helper()
	path, args, env := acptest.Command(t)
	c, err := Start(Config{Path: path, Args: args, Env: env, Dir: acptest.SessionDir(t), Handler: h})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	sid, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, sid
}

// TestABlockedStderrCallbackDoesNotStallTheAgent covers a callback that never
// returns.
func TestABlockedStderrCallbackDoesNotStallTheAgent(t *testing.T) {
	r := newRecorder()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Stderr = func(string) { <-block } })
	within(t, 20*time.Second, "a turn that writes 1 MiB to stderr", func() {
		if stop := prompt(t, c, sid, "stderr"); stop != StopEndTurn {
			t.Errorf("stop reason = %q", stop)
		}
	})
}

func TestTheDefaultEnvironmentIsMinimal(t *testing.T) {
	t.Setenv("FLOCKDECK_ACP_TEST_SECRET", "s3cret")
	t.Setenv("ANTHROPIC_API_KEY", "key")
	for _, kv := range MinimalEnv() {
		if strings.HasPrefix(kv, "FLOCKDECK_") || strings.HasPrefix(kv, "ANTHROPIC_") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "USERPROFILE=") {
			t.Errorf("MinimalEnv includes %q", kv)
		}
	}
	found := false
	for _, kv := range MinimalEnv() {
		found = found || strings.HasPrefix(kv, "PATH=")
	}
	if !found {
		t.Error("MinimalEnv has no PATH")
	}
}

// TestNothingIsLeftRunningAfterClose checks for goroutines that outlive Close.
func TestNothingIsLeftRunningAfterClose(t *testing.T) {
	settle := func() int {
		var n int
		for i := 0; i < 100; i++ {
			runtime.GC()
			n = runtime.NumGoroutine()
			time.Sleep(20 * time.Millisecond)
			if runtime.NumGoroutine() == n {
				break
			}
		}
		return n
	}
	before := settle()

	for i := 0; i < 3; i++ {
		r := newRecorder()
		r.perm = func(context.Context, PermissionRequest) string { return "allow-1" }
		c, sid := start(t, r)
		prompt(t, c, sid, "perm")
		prompt(t, c, sid, "tool")
		_ = c.Close()
	}
	// A client closed with a prompt in flight and a permission open.
	r := newRecorder()
	asked := make(chan struct{})
	r.perm = func(ctx context.Context, req PermissionRequest) string { close(asked); <-ctx.Done(); return "" }
	c, sid := start(t, r)
	go func() { _, _ = c.Prompt(context.Background(), sid, "hold-perm") }()
	<-asked
	_ = c.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		after := settle()
		if after <= before+1 {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			t.Fatalf("goroutines: %d before, %d after Close\n%s", before, after, buf)
		}
	}
}

func rawID(s string) *json.RawMessage {
	m := json.RawMessage(s)
	return &m
}

// TestTheWriteQueueIsBounded covers a client that keeps writing to an agent
// that never reads. The writer is stuck on a large message, and the queue
// behind it must fill and refuse, not grow or block the caller.
func TestTheWriteQueueIsBounded(t *testing.T) {
	r := newRecorder()
	c := startRaw(t, r, nil, acptest.WithDeaf())

	// Queue the big line here, not from another goroutine, so it is known to be
	// in the queue when notify returns. The queue is then empty only once the
	// writer has taken the line, and it stays blocked in that write because
	// the agent never reads.
	if err := c.notify("big", map[string]any{"text": strings.Repeat("z", 8<<20)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(c.outbox) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the writer never took the big line: %d waiting", len(c.outbox))
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Send until one is refused, however many that takes within the limit,
	// then check how many the queue took: its size, not much more.
	accepted, refused := 0, false
	limit := time.Now().Add(10 * time.Second)
	for i := 0; i < 5000 && time.Now().Before(limit); i++ {
		var err error
		started := time.Now()
		within(t, 2*time.Second, "Cancel", func() { err = c.Cancel("s") })
		if took := time.Since(started); took > 500*time.Millisecond {
			t.Fatalf("Cancel %d took %v", i, took)
		}
		if errors.Is(err, errOutboxFull) {
			refused = true
			break
		}
		accepted++
	}
	if !refused {
		t.Fatalf("%d notifications queued behind a stuck write and none was refused", accepted)
	}
	// A literal, so that raising outboxSize has to change this test too.
	if accepted > 256+2 {
		t.Errorf("%d notifications were accepted before the first refusal; the queue holds 256", accepted)
	}
	within(t, 10*time.Second, "Close", func() { _ = c.Close() })
}

// TestALateAnswerToAnAbandonedCallIsNotAnError covers a caller whose context
// ended while the agent was still working: the answer that follows is expected.
func TestALateAnswerToAnAbandonedCallIsNotAnError(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.Prompt(ctx, sid, "delayed"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the deadline", err)
	}
	r.waitFor(t, "the late chunk", func(us []Update) bool { return len(us) == 1 })
	time.Sleep(300 * time.Millisecond) // the response to the abandoned prompt follows the chunk
	if n := c.ProtocolErrors(); n != 0 {
		t.Errorf("a late answer to an abandoned call counted %d protocol errors", n)
	}
	// A response to nothing is still one.
	c.dispatch(rpcMsg{ID: rawID("12345")})
	if n := c.ProtocolErrors(); n != 1 {
		t.Errorf("protocol errors = %d, want 1", n)
	}
}
