package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tapLog keeps the lines Config.Tap is given. It copies them, as the contract
// says a Tap must. A tap runs on a goroutine of its own, so a test reads the log
// after Close, which waits for the queue to empty.
type tapLog struct {
	mu    sync.Mutex
	lines []tapLine
	delay time.Duration
	// hold, if set, is waited on before the first line is kept: a tap that is
	// stuck until the test lets go.
	hold chan struct{}
	once sync.Once
}

type tapLine struct {
	dir string
	raw string
}

func (l *tapLog) tap(dir string, raw []byte) {
	if l.hold != nil {
		l.once.Do(func() { <-l.hold })
	}
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
	_ = c.Close()

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
		if strings.ContainsAny(x.raw, "\r\n") || strings.TrimSpace(x.raw) == "" {
			t.Fatalf("a tapped line has a newline or is empty: %q", x.raw)
		}
	}
	if c.TapDropped() != 0 || c.TapPanics() != 0 {
		t.Errorf("dropped %d, panics %d", c.TapDropped(), c.TapPanics())
	}
}

func TestTapSeesALineThatIsNotJSON(t *testing.T) {
	log := &tapLog{}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	prompt(t, c, sid, "garbage")
	_ = c.Close()
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

// Blank lines are not messages and are not tapped.
func TestBlankLinesAreNotTapped(t *testing.T) {
	log := &tapLog{}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	prompt(t, c, sid, "blank")
	_ = c.Close()
	// Two responses, one update, the prompt's response.
	if in := log.dir(TapIn); len(in) != 4 {
		t.Errorf("%d lines tapped, want 4: %q", len(in), in)
	}
	for _, x := range log.all() {
		if strings.TrimSpace(x.raw) == "" {
			t.Errorf("a blank line was tapped: %q", x.raw)
		}
	}
}

// With no Tap nothing is queued, counted or dropped.
func TestWithNoTapNothingIsCounted(t *testing.T) {
	r := newRecorder()
	c, sid := startWith(t, r, nil)
	prompt(t, c, sid, "plain")
	if c.ProtocolErrors() != 0 || c.TapDropped() != 0 || c.TapPanics() != 0 {
		t.Errorf("protocol errors %d, dropped %d, panics %d", c.ProtocolErrors(), c.TapDropped(), c.TapPanics())
	}
}

// A panic in Tap is counted on its own, not with what the agent did.
func TestAPanickingTapIsSurvivedAndCountedApart(t *testing.T) {
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
	_ = c.Close()
	if calls.Load() < 6 {
		t.Errorf("the tap was called %d times", calls.Load())
	}
	if c.TapPanics() != calls.Load() {
		t.Errorf("%d panics counted for %d calls", c.TapPanics(), calls.Load())
	}
	if c.ProtocolErrors() != 0 {
		t.Errorf("a tap's panic was counted as the agent's: %d protocol errors", c.ProtocolErrors())
	}
}

// A line of MaxLineBytes, newline included, is tapped whole. One byte more is a
// lost peer and is not tapped, not even in part.
func TestTapAndTheLineLimit(t *testing.T) {
	const limit = 100_000
	log := &tapLog{}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap; cfg.MaxLineBytes = limit })
	if stop := prompt(t, c, sid, "line "+strconv.Itoa(limit-1)); stop != StopEndTurn {
		t.Fatalf("stop = %q", stop)
	}
	if _, err := c.Prompt(ctxT(t), sid, "line "+strconv.Itoa(limit)); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("a line one byte over the limit: err = %v, want ErrLineTooLong", err)
	}
	_ = c.Close()
	sizes := map[int]int{}
	for _, raw := range log.dir(TapIn) {
		if len(raw) > limit-1 {
			t.Errorf("a line of %d bytes was tapped, over the limit", len(raw))
		}
		sizes[len(raw)]++
	}
	if sizes[limit-1] != 1 {
		t.Errorf("the line of exactly the limit was tapped %d times", sizes[limit-1])
	}
	if sizes[limit] != 0 {
		t.Error("the line over the limit was tapped")
	}
}

// A big line below the limit is tapped whole.
func TestTapGetsABigLineWhole(t *testing.T) {
	log := &tapLog{}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	prompt(t, c, sid, "big")
	_ = c.Close()
	most := 0
	for _, raw := range log.dir(TapIn) {
		most = max(most, len(raw))
	}
	if most < 5<<20 {
		t.Errorf("the largest line tapped is %d bytes; the 5 MiB result was not tapped whole", most)
	}
}

// A slow tap changes nothing else: updates reach the handler in order.
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
	_ = c.Close()
	// The tap was behind and the queue held it: nothing was dropped, and the
	// lines it kept are in order.
	if n := len(log.dir(TapIn)); n+int(c.TapDropped()) != 203 {
		t.Errorf("%d lines tapped, %d dropped", n, c.TapDropped())
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
	if err := c.Cancel(sid); err != nil {
		t.Fatal(err)
	}
	<-stop
}

// A tap that is stuck must not cost the agent a reply. Every Cancel is a line
// written; with the tap held they would fill the writer's queue if the writer
// waited for the tap, and enqueue gives up on a full queue.
func TestAStuckTapCostsNoReplies(t *testing.T) {
	log := &tapLog{hold: make(chan struct{})}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	released := false
	release := func() {
		if !released {
			released = true
			close(log.hold)
		}
	}
	t.Cleanup(release)
	// More than the writer's queue (256) and, below, more than the tap's own.
	for i := 0; i < 600; i++ {
		if err := c.Cancel(sid); err != nil {
			t.Fatalf("Cancel %d with the tap stuck: %v", i, err)
		}
	}
	// The agent still answers: a turn runs to the end with the tap stuck.
	if stop := prompt(t, c, sid, "order"); stop != StopEndTurn {
		t.Fatalf("stop = %q", stop)
	}
	if len(r.all()) != 200 {
		t.Errorf("%d updates reached the handler", len(r.all()))
	}
	if c.ProtocolErrors() != 0 {
		t.Errorf("%d protocol errors; a reply was lost", c.ProtocolErrors())
	}
	release()
}

// Past what the tap's queue holds, lines are dropped and counted, and the tap
// carries on from where it caught up.
func TestATapThatIsFarBehindDropsLinesAndCountsThem(t *testing.T) {
	log := &tapLog{hold: make(chan struct{})}
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	for i := 0; i < 3*tapQueueLines; i++ {
		if err := c.Cancel(sid); err != nil {
			t.Fatalf("Cancel %d: %v", i, err)
		}
	}
	if c.TapDropped() == 0 {
		t.Error("nothing was dropped although the tap was stuck")
	}
	close(log.hold)
	_ = c.Close()
	if n := int64(len(log.all())); n+c.TapDropped() < 3*tapQueueLines {
		t.Errorf("%d tapped and %d dropped for at least %d lines", n, c.TapDropped(), 3*tapQueueLines)
	}
}

// A tap that never returns delays Close by no more than the grace, and Close
// still ends the agent.
func TestAStuckTapDoesNotHoldUpClose(t *testing.T) {
	log := &tapLog{hold: make(chan struct{})}
	t.Cleanup(func() { close(log.hold) })
	r := newRecorder()
	c, sid := startWith(t, r, func(cfg *Config) { cfg.Tap = log.tap })
	prompt(t, c, sid, "plain")
	within(t, 6*time.Second, "Close", func() { _ = c.Close() })
	select {
	case <-c.Done():
	case <-time.After(wait):
		t.Fatal("the read loop did not end")
	}
}
