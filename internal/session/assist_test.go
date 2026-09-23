package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/jev"
)

// fakeJev is TypeSafe standing in for itself: it records every request it is
// sent, whole, and answers each with respond, or with a confident "waiting"
// when there is none.
type fakeJev struct {
	srv  *httptest.Server
	hits atomic.Int32

	mu      sync.Mutex
	bodies  [][]byte
	respond func(w http.ResponseWriter, r *http.Request, n int)
}

func newFakeJev(t *testing.T, respond func(w http.ResponseWriter, r *http.Request, n int)) *fakeJev {
	t.Helper()
	f := &fakeJev{respond: respond}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		n := int(f.hits.Add(1))
		if f.respond != nil {
			f.respond(w, r, n)
			return
		}
		answer(w, "waiting", 0.9, map[string]float64{"waiting": 0.95, "idle": 0.05}, 0.95)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJev) client() (*jev.Client, error) {
	return &jev.Client{Key: "test-key", APIBase: f.srv.URL}, nil
}

func (f *fakeJev) bodyAt(t *testing.T, i int) []byte {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.bodies) {
		t.Fatalf("only %d requests were made, wanted the one at %d", len(f.bodies), i)
	}
	return f.bodies[i]
}

// answer writes a well-formed Jev answer to the batch assistQuestions is.
func answer(w http.ResponseWriter, choice string, confidence float64, probs map[string]float64, asking float64) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model": "jev-test",
		"answers": map[string]any{
			keyState:  map[string]any{"type": "choice", "choice": choice, "confidence": confidence, "probabilities": probs},
			keyAsking: map[string]any{"type": "noul", "noul": asking},
		},
	})
}

func enabled() bool { return true }

// rig is a pane of an agent that reports nothing, on a fake pseudo-terminal, with
// an assistant pointed at fake. Each ask's end is signalled on done.
type rig struct {
	s    *Session
	a    *StatusAssist
	done chan struct{}
}

func newRig(t *testing.T, fake *fakeJev) *rig {
	t.Helper()
	a := &StatusAssist{Enabled: enabled, NewClient: fake.client, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s := fakeSession(newFakePTY())
	s.Kind = KindAgent
	s.ID = "pane-1"
	s.idleAfter = 20 * time.Millisecond
	s.assist = a
	r := &rig{s: s, a: a, done: make(chan struct{}, 8)}
	s.assistDone = func() { r.done <- struct{}{} }
	return r
}

// quiet prints text and returns once the pane has gone quiet, been called idle
// by the timer, and the ask that followed has ended.
func (r *rig) quiet(t *testing.T, text string) {
	t.Helper()
	r.s.publish([]byte(text))
	r.await(t)
}

func (r *rig) await(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the pane never finished asking")
	}
}

func (r *rig) status() Status {
	st, _ := r.s.Status()
	return st
}

func TestJevOffMeansNoCalls(t *testing.T) {
	fake := newFakeJev(t, nil)
	r := newRig(t, fake)
	r.a.Enabled = func() bool { return false }
	r.quiet(t, "Overwrite main.go? (y/n) ")
	if got := fake.hits.Load(); got != 0 {
		t.Fatalf("%d calls with the setting off", got)
	}
	if st := r.status(); st != StatusIdle {
		t.Errorf("status = %v, want the heuristic's idle", st)
	}

	// A setting that is not wired at all is off, too.
	r.a.Enabled = nil
	r.quiet(t, "again? (y/n) ")
	if got := fake.hits.Load(); got != 0 {
		t.Fatalf("%d calls with no setting at all", got)
	}
}

func TestJevWithNoKeyMakesNoCallAndSaysNothing(t *testing.T) {
	fake := newFakeJev(t, nil)
	r := newRig(t, fake)
	r.a.NewClient = func() (*jev.Client, error) { return nil, jev.ErrNoKey }
	var logs bytes.Buffer
	r.a.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.quiet(t, "Overwrite main.go? (y/n) ")
	if got := fake.hits.Load(); got != 0 {
		t.Fatalf("%d calls with no key", got)
	}
	if st := r.status(); st != StatusIdle {
		t.Errorf("status = %v, want idle", st)
	}
	if logs.Len() != 0 {
		t.Errorf("a missing key was reported: %s", logs.String())
	}
}

func TestJevIsNotAskedWhenTheHeuristicIsCertain(t *testing.T) {
	fake := newFakeJev(t, nil)
	a := &StatusAssist{Enabled: enabled, NewClient: fake.client}
	spec := agent.Spec{Patterns: agent.Patterns{Waiting: []string{"(y/n)"}, Idle: []string{"> "}}}
	s := fakeSession(newFakePTY())
	s.Kind = KindAgent
	s.idleAfter = 20 * time.Millisecond
	s.patterns = foldPatterns(spec.Patterns)
	s.assist = assistFor(Config{Kind: KindAgent, Spec: spec, Assist: a})
	done := make(chan struct{}, 4)
	s.assistDone = func() { done <- struct{}{} }

	// The agent's own pattern says waiting, and then says idle: neither goes
	// through the quiet timer, so neither is a guess.
	s.publish([]byte("Overwrite main.go? (y/n) "))
	if st, _ := s.Status(); st != StatusWaiting {
		t.Fatalf("status = %v, want waiting from the pattern", st)
	}
	s.publish([]byte("\r\n> "))
	if st, _ := s.Status(); st != StatusIdle {
		t.Fatalf("status = %v, want idle from the pattern", st)
	}
	time.Sleep(100 * time.Millisecond) // five quiet periods: a timer that was going to ask has
	select {
	case <-done:
		t.Error("a pane whose pattern had spoken asked anyway")
	default:
	}
	if got := fake.hits.Load(); got != 0 {
		t.Fatalf("%d calls when the heuristic was certain", got)
	}
}

func TestOnlyAnAgentWithoutALifecycleMayAsk(t *testing.T) {
	a := &StatusAssist{}
	for _, tc := range []struct {
		name string
		cfg  Config
		want bool
	}{
		{"an agent that reports nothing", Config{Kind: KindAgent, Assist: a}, true},
		{"an agent that reports its lifecycle", Config{Kind: KindAgent, Spec: agent.Spec{Caps: agent.Caps{Hooks: true}}, Assist: a}, false},
		{"a shell", Config{Kind: KindShell, Assist: a}, false},
		{"no assistant", Config{Kind: KindAgent}, false},
	} {
		if got := assistFor(tc.cfg) != nil; got != tc.want {
			t.Errorf("%s: may ask = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAHookThatReportsKeepsTheAssistantOut(t *testing.T) {
	fake := newFakeJev(t, nil)
	r := newRig(t, fake)
	r.s.publish([]byte("working..."))
	// The first lifecycle event arrives before the quiet timer fires.
	r.s.SetStatus(StatusWorking, "Bash")
	time.Sleep(100 * time.Millisecond)
	if got := fake.hits.Load(); got != 0 {
		t.Fatalf("%d calls for a pane whose hooks are reporting", got)
	}
	if st := r.status(); st != StatusWorking {
		t.Errorf("status = %v, want the hook's working", st)
	}
}

func TestAnUncertainPaneCostsOneBatchedCall(t *testing.T) {
	fake := newFakeJev(t, nil)
	r := newRig(t, fake)
	r.quiet(t, "Do you want to proceed?\r\n  1. Yes\r\n  2. No\r\n")
	if got := fake.hits.Load(); got != 1 {
		t.Fatalf("%d calls, want exactly one", got)
	}
	var req struct {
		State     any                       `json:"state"`
		Questions map[string]map[string]any `json:"questions"`
	}
	if err := json.Unmarshal(fake.bodyAt(t, 0), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Questions) != 2 || req.Questions[keyState]["type"] != "choice" || req.Questions[keyAsking]["type"] != "noul" {
		t.Errorf("the one call did not carry the choice and the noul: %+v", req.Questions)
	}
	opts, _ := req.Questions[keyState]["criteria"].(map[string]any)
	for _, want := range []string{"working", "waiting", "idle", "blocked"} {
		if _, ok := opts[want]; !ok {
			t.Errorf("the choice has no %q option: %v", want, opts)
		}
	}
	if st := r.status(); st != StatusWaiting {
		t.Errorf("status = %v, want waiting from a confident answer", st)
	}
	if !r.s.StatusSince().After(time.Now().Add(-time.Minute)) {
		t.Error("the wait was not given a start time")
	}
}

func TestAConfidentBlockedIsApplied(t *testing.T) {
	fake := newFakeJev(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		answer(w, "blocked", 0.9, map[string]float64{"blocked": 0.92, "idle": 0.08}, 0.1)
	})
	r := newRig(t, fake)
	r.quiet(t, "Permission to run rm was denied.\r\n")
	if st := r.status(); st != StatusBlocked {
		t.Errorf("status = %v, want blocked", st)
	}
}

func TestAnAnswerThatDoesNotClearTheBarIsIgnored(t *testing.T) {
	strong := map[string]float64{"waiting": 0.95, "idle": 0.05}
	for _, tc := range []struct {
		name       string
		choice     string
		confidence float64
		probs      map[string]float64
		asking     float64
	}{
		{"low confidence", "waiting", 0.5, strong, 0.95},
		{"confident but the pick is not likely enough", "waiting", 0.9, map[string]float64{"waiting": 0.6, "idle": 0.4}, 0.95},
		{"the yes/no does not agree", "waiting", 0.9, strong, 0.3},
		{"jev agrees it is idle", "idle", 0.99, map[string]float64{"idle": 0.99, "waiting": 0.01}, 0.01},
		{"jev says working, which is not acted on", "working", 0.99, map[string]float64{"working": 0.99, "idle": 0.01}, 0.01},
		{"blocked below the bar", "blocked", 0.5, map[string]float64{"blocked": 0.9, "idle": 0.1}, 0.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeJev(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				answer(w, tc.choice, tc.confidence, tc.probs, tc.asking)
			})
			r := newRig(t, fake)
			r.quiet(t, "Some output the timer will read as finished\r\n")
			if fake.hits.Load() != 1 {
				t.Fatalf("%d calls, want the one that was ignored", fake.hits.Load())
			}
			if st := r.status(); st != StatusIdle {
				t.Errorf("status = %v, want the heuristic's idle left alone", st)
			}
		})
	}
}

func TestAFailedAskLeavesTheHeuristicAlone(t *testing.T) {
	for _, tc := range []struct {
		name    string
		respond func(w http.ResponseWriter, r *http.Request, n int)
	}{
		{"a server error", func(w http.ResponseWriter, _ *http.Request, _ int) { http.Error(w, "boom", 500) }},
		{"a refused key", func(w http.ResponseWriter, _ *http.Request, _ int) { http.Error(w, "no", 401) }},
		{"a rate limit that stays", func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.Header().Set("Retry-After", "0.01")
			http.Error(w, "slow down", 429)
		}},
		{"an overload that stays", func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.Header().Set("Retry-After", "0.01")
			http.Error(w, "busy", 529)
		}},
		{"an answer that is not one", func(w http.ResponseWriter, _ *http.Request, _ int) { _, _ = io.WriteString(w, "{not json") }},
		{"an answer missing a question", func(w http.ResponseWriter, _ *http.Request, _ int) {
			_, _ = io.WriteString(w, `{"model":"m","answers":{}}`)
		}},
		{"a call that never comes back", func(_ http.ResponseWriter, r *http.Request, _ int) { <-r.Context().Done() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := assistTimeout
			assistTimeout = 300 * time.Millisecond
			t.Cleanup(func() { assistTimeout = old })
			fake := newFakeJev(t, tc.respond)
			r := newRig(t, fake)
			var logs bytes.Buffer
			r.a.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			r.quiet(t, "Overwrite main.go? (y/n) ")
			if fake.hits.Load() == 0 {
				t.Fatal("nothing was asked")
			}
			if st := r.status(); st != StatusIdle {
				t.Errorf("status = %v, want the heuristic's idle after a failure", st)
			}
			if strings.Contains(logs.String(), "test-key") {
				t.Errorf("the key was logged: %s", logs.String())
			}
		})
	}
}

// A result that arrives after the pane has moved on is an answer about how it
// was, and is dropped. The server holds its answer until released, so each case
// changes the pane while the ask is in flight.
func TestAStaleAnswerIsDropped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(s *Session)
		want   Status
	}{
		{"the pane printed again", func(s *Session) {
			s.mu.Lock()
			s.idleAfter = time.Hour // so the new output stays "working" for the check
			s.mu.Unlock()
			s.publish([]byte("more output\r\n"))
		}, StatusWorking},
		{"the pane exited", func(s *Session) {
			// What wait() does.
			s.mu.Lock()
			s.status = StatusExited
			s.statusSince = time.Now()
			s.mu.Unlock()
		}, StatusExited},
		{"a lifecycle hook took over", func(s *Session) { s.SetStatus(StatusWorking, "Bash") }, StatusWorking},
		{"the pane was idle again, afresh", func(s *Session) {
			s.mu.Lock()
			s.statusSince = s.statusSince.Add(time.Second)
			s.mu.Unlock()
		}, StatusIdle},
		{"the pane was closed", func(s *Session) {
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
		}, StatusIdle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			arrived := make(chan struct{}, 1)
			fake := newFakeJev(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				arrived <- struct{}{}
				<-release
				answer(w, "waiting", 0.95, map[string]float64{"waiting": 0.99, "idle": 0.01}, 0.99)
			})
			r := newRig(t, fake)
			r.s.publish([]byte("Overwrite main.go? (y/n) "))
			select {
			case <-arrived:
			case <-time.After(10 * time.Second):
				t.Fatal("the ask never reached the server")
			}
			tc.change(r.s)
			close(release)
			r.await(t)
			if st := r.status(); st != tc.want {
				t.Errorf("status = %v, want %v: a stale answer was applied", st, tc.want)
			}
		})
	}
}

func TestAnExitedPaneIsNeverBroughtBackByAnAnswer(t *testing.T) {
	// The class of bug in TestClosingASettledFanOutTabRecordsItsHistory: a write
	// landing after the exit. Here the write is the assistant's, and it must be
	// the one that is dropped.
	release := make(chan struct{})
	arrived := make(chan struct{}, 1)
	fake := newFakeJev(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		arrived <- struct{}{}
		<-release
		answer(w, "waiting", 0.95, map[string]float64{"waiting": 0.99}, 0.99)
	})
	r := newRig(t, fake)
	r.s.reaped = make(chan struct{})
	r.s.publish([]byte("Proceed? "))
	<-arrived
	r.s.mu.Lock()
	r.s.status = StatusExited
	r.s.mu.Unlock()
	close(r.s.reaped) // the process is gone: the ask is cancelled
	close(release)
	r.await(t)
	if st := r.status(); st != StatusExited {
		t.Fatalf("status = %v, want exited to stay exited", st)
	}
}

func TestASettledWaitIsAnsweredLikeAnyOther(t *testing.T) {
	fake := newFakeJev(t, nil)
	r := newRig(t, fake)
	r.quiet(t, "Proceed? (y/n) ")
	if st := r.status(); st != StatusWaiting {
		t.Fatalf("status = %v, want waiting", st)
	}
	// Typing answers it, as it does for a wait the bell put there.
	if _, err := r.s.Write([]byte("y\r")); err != nil {
		t.Fatal(err)
	}
	if st := r.status(); st != StatusWorking && st != StatusIdle {
		t.Errorf("status = %v after typing, want the wait cleared", st)
	}
}

func TestTheRateLimitsHold(t *testing.T) {
	newAssist := func(fake *fakeJev, clock *time.Time) *StatusAssist {
		return &StatusAssist{Enabled: enabled, NewClient: fake.client, Now: func() time.Time { return *clock },
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	}
	ctx := context.Background()

	t.Run("one pane, changing tails", func(t *testing.T) {
		fake := newFakeJev(t, nil)
		now := time.Unix(1_000_000, 0)
		a := newAssist(fake, &now)
		a.Assess(ctx, "p", "tail 0")
		now = now.Add(5 * time.Second)
		a.Assess(ctx, "p", "tail 1") // too soon after the last
		if got := fake.hits.Load(); got != 1 {
			t.Fatalf("%d calls within the pane's interval, want 1", got)
		}
		for i := 2; fake.hits.Load() < assistPaneHourly; i++ {
			now = now.Add(assistPaneInterval + time.Second)
			a.Assess(ctx, "p", fmt.Sprintf("tail %d", i))
			if i > 100 {
				t.Fatal("never reached the hourly cap")
			}
		}
		// Twelve in about three minutes: the thirteenth is over the hour's cap.
		now = now.Add(assistPaneInterval + time.Second)
		a.Assess(ctx, "p", "one too many")
		if got := fake.hits.Load(); got != assistPaneHourly {
			t.Fatalf("%d calls in the hour, want the cap of %d to hold", got, assistPaneHourly)
		}
		now = now.Add(time.Hour)
		a.Assess(ctx, "p", "an hour later")
		if got := fake.hits.Load(); got != assistPaneHourly+1 {
			t.Errorf("%d calls, want the cap to lift after the hour", got)
		}
	})

	t.Run("all panes together", func(t *testing.T) {
		fake := newFakeJev(t, nil)
		now := time.Unix(1_000_000, 0)
		a := newAssist(fake, &now)
		for i := 0; i < assistGlobalMinute+5; i++ {
			a.Assess(ctx, fmt.Sprintf("pane-%d", i), "tail")
		}
		if got := fake.hits.Load(); got != assistGlobalMinute {
			t.Fatalf("%d calls in a minute across panes, want %d", got, assistGlobalMinute)
		}
		now = now.Add(61 * time.Second)
		a.Assess(ctx, "another", "tail")
		if got := fake.hits.Load(); got != assistGlobalMinute+1 {
			t.Errorf("%d calls, want the minute's cap to lift", got)
		}
	})

	t.Run("the same tail is asked once", func(t *testing.T) {
		fake := newFakeJev(t, nil)
		now := time.Unix(1_000_000, 0)
		a := newAssist(fake, &now)
		st, ok := a.Assess(ctx, "p", "same tail")
		now = now.Add(time.Hour)
		st2, ok2 := a.Assess(ctx, "p", "same tail")
		if got := fake.hits.Load(); got != 1 {
			t.Fatalf("%d calls for one tail, want 1", got)
		}
		if st != st2 || ok != ok2 || !ok || st != StatusWaiting {
			t.Errorf("the cached answer (%v, %v) is not the first (%v, %v)", st2, ok2, st, ok)
		}
	})

	t.Run("a refusal holds every pane back", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			status int
			pause  time.Duration
		}{
			{"a rate limit", 429, assistPauseTransient},
			{"a refused key", 401, assistPauseRefused},
		} {
			fake := newFakeJev(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				w.Header().Set("Retry-After", "0.01")
				http.Error(w, "no", tc.status)
			})
			now := time.Unix(1_000_000, 0)
			a := newAssist(fake, &now)
			a.Assess(ctx, "p", "tail")
			calls := fake.hits.Load()
			now = now.Add(tc.pause - time.Second)
			a.Assess(ctx, "other", "tail")
			if got := fake.hits.Load(); got != calls {
				t.Errorf("%s: a call was made while paused", tc.name)
			}
			now = now.Add(2 * time.Second)
			a.Assess(ctx, "other", "tail")
			if got := fake.hits.Load(); got == calls {
				t.Errorf("%s: no call was made after the pause", tc.name)
			}
		}
	})

	t.Run("forgetting a pane clears what is kept", func(t *testing.T) {
		fake := newFakeJev(t, nil)
		now := time.Unix(1_000_000, 0)
		a := newAssist(fake, &now)
		a.Assess(ctx, "p", "tail")
		a.Forget("p")
		a.mu.Lock()
		n := len(a.panes)
		a.mu.Unlock()
		if n != 0 {
			t.Errorf("%d panes still remembered", n)
		}
		var none *StatusAssist
		none.Forget("p") // a pane with no assistant exits too
	})
}

// What leaves the machine. This is the privacy guarantee: a great deal of
// scrollback is printed, in colour, with a secret near the top, and the request
// that reaches TypeSafe must hold none of it but the bounded, plain tail.
func TestOnlyTheBoundedPlainTailIsEverSent(t *testing.T) {
	fake := newFakeJev(t, nil)
	r := newRig(t, fake)
	r.s.history = newRing(replayBytes)
	r.s.Cwd = "/home/somebody/secret-project"
	r.s.name = "secret-pane-name"

	var out bytes.Buffer
	out.WriteString("\x1b[31mexport AWS_SECRET=hunter2-OLDEST\x1b[0m\r\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&out, "\x1b[32mscrollback line %03d\x1b[0m with some padding text to make it long\r\n", i)
	}
	// The last screenful, in colour, with a title sequence and a bell.
	out.WriteString("\x1b]0;title\x07")
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&out, "\x1b[1mrecent line %d\x1b[0m\r\n", i)
	}
	out.WriteString("\x1b[33mProceed with the change? (y/n)\x1b[0m \x1b[?25h")
	r.quiet(t, out.String())
	if fake.hits.Load() != 1 {
		t.Fatalf("%d calls, want 1", fake.hits.Load())
	}

	body := fake.bodyAt(t, 0)
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	// Only the state and the questions are in it: not the pane, its name or where it is.
	for k := range req {
		if k != "state" && k != "model" && k != "questions" {
			t.Errorf("the request carries a field %q it has no need of", k)
		}
	}
	tail, ok := req["state"].(string)
	if !ok {
		t.Fatalf("state is a %T, want the plain text of the tail", req["state"])
	}
	if len(tail) > assistTailBytes {
		t.Errorf("%d bytes of terminal output were sent, want at most %d", len(tail), assistTailBytes)
	}
	if n := strings.Count(tail, "\n") + 1; n > assistTailLines {
		t.Errorf("%d lines were sent, want at most %d", n, assistTailLines)
	}
	if strings.ContainsRune(tail, 0x1b) || strings.ContainsRune(tail, 0x07) {
		t.Errorf("escape sequences were sent: %q", tail)
	}
	if !utf8.ValidString(tail) {
		t.Errorf("the tail is not valid UTF-8: %q", tail)
	}
	for _, leaked := range []string{"hunter2", "AWS_SECRET", "OLDEST", "scrollback line 000", "scrollback line 100", "secret-project", "secret-pane-name", "pane-1", "test-key"} {
		if strings.Contains(string(body), leaked) {
			t.Errorf("%q reached the request body", leaked)
		}
	}
	for _, want := range []string{"recent line 4", "Proceed with the change? (y/n)"} {
		if !strings.Contains(tail, want) {
			t.Errorf("the tail is missing the end of the screen, %q: %q", want, tail)
		}
	}
	if len(body) > 8<<10 {
		t.Errorf("the request is %d bytes", len(body))
	}
}

func TestTheTailIsBoundedWhateverThePaneWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"one enormous line", strings.Repeat("x", 100_000)},
		{"wide characters", strings.Repeat("世", 10_000)},
		{"nothing but escapes", strings.Repeat("\x1b[31m\x1b[0m", 5_000)},
		{"many short lines", strings.Repeat("a\n", 10_000)},
		{"no line breaks, colour throughout", strings.Repeat("\x1b[32mok\x1b[0m ", 20_000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := assistTail([]byte(tc.raw))
			if len(got) > assistTailBytes {
				t.Errorf("%d bytes", len(got))
			}
			if strings.Count(got, "\n")+1 > assistTailLines {
				t.Errorf("%d lines", strings.Count(got, "\n")+1)
			}
			if !utf8.ValidString(got) {
				t.Errorf("not valid UTF-8: %q", got[:min(len(got), 40)])
			}
			if strings.ContainsRune(got, 0x1b) {
				t.Error("an escape sequence survived")
			}
		})
	}
	if got := assistTail(nil); got != "" {
		t.Errorf("nothing printed gave %q", got)
	}

	// The last lines are the ones kept.
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	got := assistTail([]byte(strings.Join(lines, "\r\n")))
	if !strings.HasSuffix(got, "line 99") || !strings.HasPrefix(got, "line 70") {
		t.Errorf("the wrong lines were kept: %q...%q", got[:20], got[len(got)-20:])
	}
}

func TestAPaneWithNothingOnScreenIsNotAsked(t *testing.T) {
	fake := newFakeJev(t, nil)
	r := newRig(t, fake)
	r.quiet(t, "\x1b[2J\x1b[H   \r\n")
	if got := fake.hits.Load(); got != 0 {
		t.Errorf("%d calls for a blank screen", got)
	}
}

func TestEveryDisagreementIsLoggedForTuning(t *testing.T) {
	// Below the bar, so nothing is applied -- and it is still recorded.
	fake := newFakeJev(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		answer(w, "waiting", 0.4, map[string]float64{"waiting": 0.55, "idle": 0.45}, 0.6)
	})
	r := newRig(t, fake)
	var logs bytes.Buffer
	r.a.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.quiet(t, "Proceed? ")
	if st := r.status(); st != StatusIdle {
		t.Fatalf("status = %v, want idle", st)
	}
	out := logs.String()
	for _, want := range []string{"disagrees", "jev=waiting", "confidence=0.4", "asking=0.6", "pane=pane-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "Proceed") {
		t.Errorf("terminal content went into the log: %s", out)
	}
}

func TestAskingIsOffTheReadersGoroutine(t *testing.T) {
	// A server that never answers must not hold up the pane's output or its
	// status: the reader keeps publishing while an ask is in flight.
	release := make(chan struct{})
	arrived := make(chan struct{}, 1)
	fake := newFakeJev(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		arrived <- struct{}{}
		<-release
		http.Error(w, "late", 500)
	})
	r := newRig(t, fake)
	r.s.publish([]byte("Proceed? "))
	<-arrived
	published := make(chan struct{})
	go func() {
		r.s.publish([]byte("still printing\r\n"))
		_, _ = r.s.Status()
		close(published)
	}()
	select {
	case <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing was held up by an ask in flight")
	}
	close(release)
	r.await(t)
}

func TestAnAskEndsWithItsPane(t *testing.T) {
	arrived := make(chan struct{}, 1)
	fake := newFakeJev(t, func(_ http.ResponseWriter, r *http.Request, _ int) {
		arrived <- struct{}{}
		<-r.Context().Done()
	})
	r := newRig(t, fake)
	r.s.reaped = make(chan struct{})
	r.s.publish([]byte("Proceed? "))
	<-arrived
	close(r.s.reaped)
	// Ends long before assistTimeout: the pane's exit is what stopped it.
	start := time.Now()
	r.await(t)
	if d := time.Since(start); d > assistTimeoutDefault/2 {
		t.Errorf("the ask outlived its pane by %v", d)
	}
}
