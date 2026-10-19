package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tapLog keeps the lines Config.Tap is given. It copies them, as the contract
// says a Tap must.
type tapLog struct {
	mu    sync.Mutex
	lines []tapLine
	delay time.Duration
}

type tapLine struct {
	dir string
	raw string
}

func (l *tapLog) tap(dir string, raw []byte) {
	if l.delay > 0 {
		time.Sleep(l.delay)
	}
	l.mu.Lock()
	l.lines = append(l.lines, tapLine{dir, string(raw)})
	l.mu.Unlock()
}

func (l *tapLog) all() []tapLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]tapLine(nil), l.lines...)
}

func (l *tapLog) dir(dir string) []string {
	var out []string
	for _, x := range l.all() {
		if x.dir == dir {
			out = append(out, x.raw)
		}
	}
	return out
}

// methodOf is the method of a JSON-RPC line, "" for a response.
func methodOf(raw string) string {
	var m struct{ Method string }
	_ = json.Unmarshal([]byte(raw), &m)
	return m.Method
}

func TestTapSeesEveryLineInAndOutInWireOrder(t *testing.T) {
	log := &tapLog{}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	prompt(t, c, sid, "order")

	var methods []string
	for _, raw := range log.dir(TapOut) {
		methods = append(methods, methodOf(raw))
	}
	if strings.Join(methods, ",") != "initialize,session/new,session/prompt" {
		t.Errorf("lines written = %v", methods)
	}
	// The agent's lines: two responses, 200 updates in order, then the
	// prompt's response.
	in := log.dir(TapIn)
	if len(in) != 203 {
		t.Fatalf("%d lines read, want 203", len(in))
	}
	next := 0
	for _, raw := range in {
		if methodOf(raw) != "session/update" {
			continue
		}
		var m struct {
			Params struct {
				Update struct{ Content struct{ Text string } }
			}
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		if m.Params.Update.Content.Text != strconv.Itoa(next) {
			t.Fatalf("update %d was tapped as %q", next, m.Params.Update.Content.Text)
		}
		next++
	}
	if next != 200 {
		t.Errorf("%d updates tapped", next)
	}
	if methodOf(in[len(in)-1]) != "" || !strings.Contains(in[len(in)-1], "stopReason") {
		t.Errorf("the last line read = %.100s", in[len(in)-1])
	}
	for _, x := range log.all() {
		if strings.ContainsAny(x.raw, "\r\n") || x.raw == "" {
			t.Fatalf("a tapped line has a newline or is empty: %q", x.raw)
		}
	}
}

func TestTapSeesALineThatIsNotJSON(t *testing.T) {
	log := &tapLog{}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	prompt(t, c, sid, "garbage")
	var notJSON int
	for _, raw := range log.dir(TapIn) {
		if !json.Valid([]byte(raw)) {
			notJSON++
		}
	}
	if notJSON == 0 {
		t.Error("the line that is not JSON was not tapped")
	}
	// Every line the agent wrote was tapped, the bad ones too: it is what the
	// recording is for.
	if n := len(log.dir(TapIn)); n < 5 {
		t.Errorf("%d lines tapped", n)
	}
}

func TestAPanickingTapIsSurvivedAndCounted(t *testing.T) {
	var calls atomic.Int64
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) {
		cfg.Tap = func(string, []byte) { calls.Add(1); panic("tap") }
	})
	if stop := prompt(t, c, sid, "plain"); stop != StopEndTurn {
		t.Fatalf("stop = %q", stop)
	}
	if r.text() == "" {
		t.Error("no update reached the handler")
	}
	if calls.Load() < 6 {
		t.Errorf("the tap was called %d times", calls.Load())
	}
	if c.ProtocolErrors() < calls.Load() {
		t.Errorf("%d panics counted for %d calls", c.ProtocolErrors(), calls.Load())
	}
	// Still working.
	r.reset()
	prompt(t, c, sid, "plain")
}

// A line up to MaxLineBytes is tapped whole. A longer one is a lost peer and
// is not tapped, not even in part.
func TestTapAndTheLineLimit(t *testing.T) {
	log := &tapLog{}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	prompt(t, c, sid, "big")
	most := 0
	for _, raw := range log.dir(TapIn) {
		most = max(most, len(raw))
	}
	if most < 100<<10 {
		t.Errorf("the largest line tapped is %d bytes; the big result was not tapped whole", most)
	}
	_ = c

	log2 := &tapLog{}
	r2 := newRecorder()
	c2, sid2 := startWith(t, r2, func(cfg *Config) { cfg.Tap = log2.tap; cfg.MaxLineBytes = 1 << 20 })
	if _, err := c2.Prompt(ctxT(t), sid2, "flood"); err == nil {
		t.Fatal("the flood did not end the call")
	}
	for _, raw := range log2.dir(TapIn) {
		if len(raw) > 1<<20 {
			t.Errorf("a line of %d bytes was tapped, over the limit", len(raw))
		}
	}
}

// A slow tap slows the client but changes nothing else: updates reach the
// handler in order, and the limit on the agent's requests holds.
func TestASlowTapDoesNotReorderUpdates(t *testing.T) {
	log := &tapLog{delay: 2 * time.Millisecond}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	prompt(t, c, sid, "order")
	us := r.all()
	if len(us) != 200 {
		t.Fatalf("got %d updates", len(us))
	}
	for i, u := range us {
		if u.Content == nil || u.Content.Text != strconv.Itoa(i) {
			t.Fatalf("update %d is %+v", i, u.Content)
		}
	}
	if n := len(log.dir(TapIn)); n != 203 {
		t.Errorf("%d lines tapped", n)
	}
}

func TestASlowTapKeepsTheRequestLimit(t *testing.T) {
	log := &tapLog{delay: time.Millisecond}
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
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap; cfg.MaxAgentRequests = 4 })
	t.Cleanup(func() { close(release) })
	stop := make(chan string, 1)
	go func() { s, _ := c.Prompt(ctxT(t), sid, "spam-perm"); stop <- s }()
	r.waitFor(t, "the agent's count", func(us []Update) bool { return strings.Contains(joinText(us), "answered=") })
	if got := joinText(r.all()); got != "answered=46" {
		t.Errorf("the agent saw %q, want 46 refused", got)
	}
	if p := peak.Load(); p != 4 {
		t.Errorf("handlers running at once = %d, want 4", p)
	}
	// The refusals were written, and tapped, as errors.
	var errs int
	for _, raw := range log.dir(TapOut) {
		if bytes.Contains([]byte(raw), []byte("too many requests")) {
			errs++
		}
	}
	if errs != 46 {
		t.Errorf("%d refusals tapped, want 46", errs)
	}
	if err := c.Cancel(sid); err != nil {
		t.Fatal(err)
	}
	<-stop
}
