package structured

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/acp/manifest"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/testiso"
)

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network (see docs/testing.md). They start only the fake
// agent, which is this test binary run again.
func TestMain(m *testing.M) {
	if acptest.RunIfAgent() {
		return
	}
	os.Exit(testiso.Main(m))
}

func fakeOptions(t *testing.T, aopts ...acptest.Option) Options {
	t.Helper()
	path, args, env := acptest.Command(t, aopts...)
	return Options{
		Spec:    agent.Spec{ID: "fake", ACP: agent.ACPSpec{Login: "sign in with the fake agent's own tool"}},
		Cwd:     acptest.SessionDir(t),
		Command: &Command{Path: path, Args: args, env: env, Rewind: true},
	}
}

func begin(t *testing.T, o Options) *Runtime {
	t.Helper()
	r, err := Start(context.Background(), o)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// until polls the snapshot until cond holds, and fails with the snapshot's
// state when it does not within 20 seconds.
func until(t *testing.T, r *Runtime, what string, cond func(Snapshot) bool) Snapshot {
	t.Helper()
	return untilFor(t, 20*time.Second, r, what, cond)
}

func untilFor(t *testing.T, d time.Duration, r *Runtime, what string, cond func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		s := r.Snapshot()
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; phase %q status %q err %q entries %d", what, s.Phase, s.Status, s.Err, len(s.Entries))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func idle(s Snapshot) bool { return s.Phase == PhaseReady && s.Status == bridge.StatusIdle }

func replies(s Snapshot) string {
	var b strings.Builder
	for _, e := range s.Entries {
		if e.Kind == transcript.KindReply {
			b.WriteString(e.Markdown)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func prompts(s Snapshot) []string {
	var out []string
	for _, e := range s.Entries {
		if e.Kind == transcript.KindPrompt {
			out = append(out, e.Text)
		}
	}
	return out
}

func notices(s Snapshot) string {
	var b strings.Builder
	for _, e := range s.Entries {
		if e.Kind == transcript.KindNotice {
			b.WriteString(e.Text + "\n")
		}
	}
	return b.String()
}

func TestAPlainTurn(t *testing.T) {
	r := begin(t, fakeOptions(t))
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the reply", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "Hello world") })
	if got := prompts(s); !slices.Equal(got, []string{"plain"}) {
		t.Errorf("prompts = %v", got)
	}
	if s.Err != "" || s.Setup != nil || s.Pending != nil || s.Queued != 0 {
		t.Errorf("unexpected state: %+v", s)
	}
}

// A prompt sent while the agent is still starting is kept and sent once there
// is a session.
func TestAPromptBeforeTheSessionIsKept(t *testing.T) {
	r := begin(t, fakeOptions(t))
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the reply", func(s Snapshot) bool { return strings.Contains(replies(s), "Hello world") })
}

// A second prompt waits behind a running turn and goes when it ends.
func TestAQueuedPromptWaitsForTheTurn(t *testing.T) {
	r := begin(t, fakeOptions(t))
	if err := r.Send("perm"); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the permission request", func(s Snapshot) bool { return s.Pending != nil })
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	if s = r.Snapshot(); s.Queued != 1 || len(prompts(s)) != 1 {
		t.Fatalf("the second prompt should wait: queued %d, prompts %v", s.Queued, prompts(s))
	}
	if err := r.Answer(s.Pending.RequestID, "allow-1"); err != nil {
		t.Fatal(err)
	}
	s = until(t, r, "both turns", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "Hello world") })
	if got := prompts(s); !slices.Equal(got, []string{"perm", "plain"}) {
		t.Errorf("prompts = %v", got)
	}
	if s.Queued != 0 {
		t.Errorf("queued = %d", s.Queued)
	}
}

func TestTheQueueIsBounded(t *testing.T) {
	// A mute agent never answers initialize, so the pane stays starting.
	r := begin(t, fakeOptions(t, acptest.WithMute()))
	for i := 0; i < maxQueue; i++ {
		if err := r.Send("p"); err != nil {
			t.Fatalf("prompt %d: %v", i, err)
		}
	}
	if err := r.Send("p"); !errors.Is(err, ErrQueueFull) {
		t.Errorf("the prompt past the bound: %v", err)
	}
	if err := r.Send("  "); err == nil {
		t.Error("an empty prompt was taken")
	}
	if err := r.Send(strings.Repeat("x", maxPrompt+1)); err == nil {
		t.Error("an oversize prompt was taken")
	}
}

func TestAPermissionAnsweredAllowRejectOrCancelled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer func(t *testing.T, r *Runtime, p *bridge.Card)
		want   string
	}{
		{"allow", func(t *testing.T, r *Runtime, p *bridge.Card) {
			if err := r.AnswerCard(p.Seq, p.RequestID, "allow-1"); err != nil {
				t.Fatal(err)
			}
		}, "outcome=selected option=allow-1"},
		{"reject", func(t *testing.T, r *Runtime, p *bridge.Card) {
			if err := r.Answer(p.RequestID, "reject-1"); err != nil {
				t.Fatal(err)
			}
		}, "outcome=selected option=reject-1"},
		{"cancelled", func(t *testing.T, r *Runtime, p *bridge.Card) {
			if err := r.Cancel(); err != nil {
				t.Fatal(err)
			}
		}, "outcome=cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := begin(t, fakeOptions(t))
			if err := r.Send("perm"); err != nil {
				t.Fatal(err)
			}
			s := until(t, r, "the request", func(s Snapshot) bool { return s.Pending != nil })
			if s.Status != bridge.StatusWaiting {
				t.Errorf("status = %q, want waiting", s.Status)
			}
			if len(s.Pending.Options) != 3 {
				t.Fatalf("options = %+v", s.Pending.Options)
			}
			tc.answer(t, r, s.Pending)
			s = until(t, r, "the turn to end", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "outcome=") })
			if !strings.Contains(replies(s), tc.want) {
				t.Errorf("the agent heard %q, want %q", replies(s), tc.want)
			}
			if s.Pending != nil {
				t.Errorf("the card is still open: %+v", s.Pending)
			}
		})
	}
}

// Nothing answers by itself, and a wrong answer is refused without closing
// the request.
func TestNothingAnswersByItself(t *testing.T) {
	r := begin(t, fakeOptions(t))
	if err := r.Send("perm"); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the request", func(s Snapshot) bool { return s.Pending != nil })
	p := s.Pending

	time.Sleep(400 * time.Millisecond)
	if r.Snapshot().Pending == nil {
		t.Fatal("the request was answered by nobody")
	}
	if err := r.Answer(p.RequestID, "no-such-option"); !errors.Is(err, ErrBadOption) {
		t.Errorf("a bad option: %v", err)
	}
	if err := r.Answer("some-other-request", "allow-1"); !errors.Is(err, ErrStale) {
		t.Errorf("an unknown request: %v", err)
	}
	if err := r.AnswerCard(p.Seq+1, p.RequestID, "allow-1"); !errors.Is(err, ErrStale) {
		t.Errorf("another card with the same request id: %v", err)
	}
	if err := r.AnswerCard(0, p.RequestID, "allow-1"); !errors.Is(err, ErrStale) {
		t.Errorf("card 0: %v", err)
	}
	if r.Snapshot().Pending == nil {
		t.Fatal("a refused answer closed the request")
	}
	if err := r.Answer(p.RequestID, "allow-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Answer(p.RequestID, "allow-1"); !errors.Is(err, ErrStale) {
		t.Errorf("an answer to a closed request: %v", err)
	}
	until(t, r, "the turn to end", idle)
}

func TestCancelEndsARunningTurn(t *testing.T) {
	r := begin(t, fakeOptions(t))
	if err := r.Send("slow"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the turn to run", func(s Snapshot) bool {
		return s.Status == bridge.StatusWorking && strings.Contains(replies(s), "working...")
	})
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel(); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the turn to end", idle)
	if !strings.Contains(notices(s), "Turn cancelled.") {
		t.Errorf("notices = %q", notices(s))
	}
	// Cancel ends the waiting prompts too: it means stop.
	time.Sleep(200 * time.Millisecond)
	if s = r.Snapshot(); s.Queued != 0 || len(prompts(s)) != 1 {
		t.Errorf("the queued prompt ran after a cancel: queued %d, prompts %v", s.Queued, prompts(s))
	}
}

// Closing with a request open answers it cancelled, ends the agent and leaves
// nothing running.
func TestCloseWithAnOpenRequest(t *testing.T) {
	before := runtime.NumGoroutine()
	marker := filepath.Join(t.TempDir(), "marker")
	r, err := Start(context.Background(), fakeOptions(t, acptest.WithMarker(marker)))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Send("hold-perm"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the request", func(s Snapshot) bool { return s.Pending != nil })

	started := time.Now()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("Close took %v", took)
	}
	s := r.Snapshot()
	if s.Phase != PhaseClosed || s.Pending != nil {
		t.Errorf("phase %q pending %v", s.Phase, s.Pending)
	}
	if err := r.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
	if err := r.Send("plain"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Send after Close: %v", err)
	}
	if err := r.AnswerCard(1, "x", "y"); !errors.Is(err, ErrStale) {
		t.Errorf("Answer after Close: %v", err)
	}
	// The agent heard the request cancelled before it was ended.
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(marker)
		if strings.Contains(string(b), "cancelled") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent never heard a cancelled answer; marker %q", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	settled(t, before)
}

// settled fails if goroutines started by the test are still running.
func settled(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= before {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines: %d before, %d after\n%s", before, n, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestNoGoroutinesRemainAfterClose(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 3; i++ {
		r, err := Start(context.Background(), fakeOptions(t))
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Send("plain")
		until(t, r, "the reply", func(s Snapshot) bool { return strings.Contains(replies(s), "Hello world") })
		ch, stop := r.Subscribe()
		_ = ch
		defer stop()
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	settled(t, before)
}

func TestTheAgentExitingMidTurn(t *testing.T) {
	r := begin(t, fakeOptions(t))
	if err := r.Send("exit"); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the exit", func(s Snapshot) bool { return s.Phase == PhaseExited })
	if s.Err != "the agent is unavailable" {
		t.Errorf("err = %q, want only that the agent is unavailable, never its stderr", s.Err)
	}
	if s.Status != "" {
		t.Errorf("status = %q after an exit", s.Status)
	}
	if err := r.Send("plain"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Send after an exit: %v", err)
	}
	if err := r.Cancel(); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Cancel after an exit: %v", err)
	}
}

// An agent that exits between turns, with nothing queued, still ends the pane:
// the runtime is waiting for the next prompt and has to notice on its own.
func TestTheAgentExitingWhileIdle(t *testing.T) {
	r := begin(t, fakeOptions(t))
	if err := r.Send("exit-idle"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the turn to end", func(s Snapshot) bool { return strings.Contains(replies(s), "bye") })
	s := until(t, r, "the exit", func(s Snapshot) bool { return s.Phase == PhaseExited })
	if s.Err != "the agent is unavailable" {
		t.Errorf("err = %q, want only that the agent is unavailable, never its stderr", s.Err)
	}
	if s.Status != "" || s.Pending != nil || s.Queued != 0 {
		t.Errorf("state after an idle exit: status %q, pending %v, queued %d", s.Status, s.Pending, s.Queued)
	}
	if err := r.Send("plain"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Send after an exit: %v", err)
	}
	if err := r.Cancel(); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Cancel after an exit: %v", err)
	}
}

// A permission request the agent opened and never closed does not outlive the
// agent: after the exit nothing is pending, and answering it is refused.
func TestAnOpenRequestIsGoneWhenTheAgentExitsWhileIdle(t *testing.T) {
	// The agent exits only once this file exists, which is after the request
	// has been seen.
	seen := filepath.Join(t.TempDir(), "seen")
	r := begin(t, fakeOptions(t, acptest.WithMarker(seen)))
	if err := r.Send("perm-then-exit"); err != nil {
		t.Fatal(err)
	}
	open := until(t, r, "the request", func(s Snapshot) bool { return s.Pending != nil })
	id, seq := open.Pending.RequestID, open.Pending.Seq
	if err := os.WriteFile(seen, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the exit", func(s Snapshot) bool { return s.Phase == PhaseExited })
	if s.Pending != nil {
		t.Errorf("request %q still pending after the exit", s.Pending.RequestID)
	}
	if err := r.AnswerCard(seq, id, "allow"); err == nil {
		t.Error("an answer to a request of an exited agent was accepted")
	}
}

func TestAMissingLoginIsASetupState(t *testing.T) {
	for _, at := range []string{"new", "prompt"} {
		t.Run(at, func(t *testing.T) {
			r := begin(t, fakeOptions(t, acptest.WithAuth(at)))
			if err := r.Send("plain"); err != nil {
				t.Fatal(err)
			}
			s := until(t, r, "the setup state", func(s Snapshot) bool { return s.Phase == PhaseSetup })
			if s.Setup == nil || s.Setup.Kind != SetupLogin || len(s.Setup.Methods) != 2 || s.Setup.Login != "sign in with the fake agent's own tool" {
				t.Fatalf("setup = %+v", s.Setup)
			}
			for _, m := range s.Setup.Methods {
				if m.ID == "" || m.Name == "" {
					t.Errorf("method %+v", m)
				}
			}
			if s.Err == "" {
				t.Error("the state should say what the agent said")
			}
			if err := r.Send("plain"); !errors.Is(err, ErrNotRunning) {
				t.Errorf("Send in the setup state: %v", err)
			}
		})
	}
}

// A program that is not on PATH is only ever unavailable: the state is the
// failed one with the fixed text, there is no setup state, and nothing of the
// program's name, the Spec's install line or the manifest's is in it.
func TestAMissingProgramIsUnavailableInFixedText(t *testing.T) {
	for _, adapter := range []string{"opencode", "gemini"} {
		o := fakeOptions(t)
		o.Command = nil
		o.Spec.Runner, o.Spec.ACP.Adapter = agent.RunnerACP, adapter
		o.Spec.Install = "Install it from the vendor with npm."
		o.Probe = &manifest.Probe{LookPath: func(string) (string, error) { return "", errors.New("node not found on PATH") }}
		r := begin(t, o)
		s := until(t, r, "the failure", func(s Snapshot) bool { return s.Phase == PhaseFailed })
		if s.Setup != nil || s.Err != agent.UnavailableText {
			t.Errorf("%s: setup = %+v, err = %q, want no setup and only %q", adapter, s.Setup, s.Err, agent.UnavailableText)
		}
		for _, bad := range []string{"node", "Node", "npm", "PATH", "vendor", adapter} {
			if strings.Contains(s.Err, bad) {
				t.Errorf("%s: the unavailable text mentions %q: %q", adapter, bad, s.Err)
			}
		}
	}

	o := fakeOptions(t)
	o.Command = &Command{Path: filepath.Join(t.TempDir(), "nothing-here"), env: []string{}}
	r2 := begin(t, o)
	until(t, r2, "the failure", func(s Snapshot) bool { return s.Phase == PhaseFailed })

	o.Command = nil
	o.Spec.Runner, o.Spec.ACP.Adapter = agent.RunnerACP, "not-in-the-manifest"
	r3 := begin(t, o)
	s := until(t, r3, "the failure", func(s Snapshot) bool { return s.Phase == PhaseFailed })
	if s.Err != unavailable {
		t.Errorf("err = %q", s.Err)
	}
}

func TestStartRefusesWhatCannotRun(t *testing.T) {
	o := fakeOptions(t)
	o.Cwd = "relative/dir"
	if _, err := Start(context.Background(), o); err == nil {
		t.Error("a relative working directory was taken")
	}
	o = fakeOptions(t)
	o.Command = nil
	if _, err := Start(context.Background(), o); err == nil {
		t.Error("an entry with no adapter was taken")
	}
}

// The child sees MinimalEnv and what the entry lists, and nothing of a
// poisoned parent: not Flockdeck's tokens under either prefix, not a key.
func TestThePoisonedParentEnvironmentDoesNotReachTheChild(t *testing.T) {
	t.Setenv("FLOCKDECK_TOKEN", "poison-flockdeck")
	t.Setenv("PERCH_TOKEN", "poison-perch")
	t.Setenv("perch_api", "poison-lower")
	t.Setenv("ANTHROPIC_API_KEY", "poison-key")
	t.Setenv("OPENAI_API_KEY", "poison-openai")
	t.Setenv("KEEP_ME", "kept-value")

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	o := Options{
		// Listing the reserved names must not pass them either.
		Spec: agent.Spec{ID: "fake", ACP: agent.ACPSpec{PassEnv: []string{"KEEP_ME", "FLOCKDECK_TOKEN", "PERCH_TOKEN", "perch_api", "PATH"}}},
		Cwd:  acptest.SessionDir(t),
		// A nil Env is the environment built from the entry.
		Command: &Command{Path: exe, Args: []string{acptest.ArgAgent}},
	}
	r := begin(t, o)
	if err := r.Send("env"); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the environment", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "=") })
	got := replies(s)
	for _, poison := range []string{"poison-flockdeck", "poison-perch", "poison-lower", "poison-key", "poison-openai", "FLOCKDECK_TOKEN", "PERCH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "FLOCKDECK_"} {
		if strings.Contains(got, poison) {
			t.Errorf("the child saw %q:\n%s", poison, got)
		}
	}
	if !strings.Contains(got, "KEEP_ME=kept-value") {
		t.Errorf("the child did not get the variable the entry lists:\n%s", got)
	}
	// Only MinimalEnv's variables and the one listed.
	allowed := map[string]bool{"KEEP_ME": true}
	for _, kv := range acp.MinimalEnv() {
		name, _, _ := strings.Cut(kv, "=")
		allowed[strings.ToUpper(name)] = true
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(got, "\n") {
		name, _, ok := strings.Cut(line, "=")
		if !ok || name == "" {
			continue
		}
		seen[strings.ToUpper(name)] = true
		if !allowed[strings.ToUpper(name)] {
			// The Go runtime may add a few of its own on Windows.
			if runtime.GOOS == "windows" && strings.HasPrefix(name, "=") {
				continue
			}
			t.Errorf("the child has %q, which neither MinimalEnv nor the entry gives", name)
		}
	}
	// And the lower bound: everything MinimalEnv gives reaches the child.
	for _, kv := range acp.MinimalEnv() {
		name, _, _ := strings.Cut(kv, "=")
		if !seen[strings.ToUpper(name)] {
			t.Errorf("the child did not report %s, which MinimalEnv gives", name)
		}
	}
}

// Both directions of the traffic reach the recorder, through a real one.
func TestTheRecorderGetsBothDirections(t *testing.T) {
	dir := t.TempDir()
	m := record.NewManager(func() (string, error) { return dir, nil })
	defer m.Close()
	o := fakeOptions(t)
	o.Records = m
	o.Meta = record.Meta{Project: "demo", ProjectRoot: filepath.Join(dir, "demo"), Agent: "fake", Conversation: "conv-1"}
	r := begin(t, o)
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the reply", func(s Snapshot) bool { return strings.Contains(replies(s), "Hello world") })
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	var path string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && record.IsRPC(info.Name()) {
			path = p
		}
		return nil
	})
	if path == "" {
		t.Fatal("no stream recording was made")
	}
	f, err := record.ReadRPCFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var in, out int
	var methods []string
	for _, l := range f.Lines {
		if l.Type != record.TypeRPCMessage {
			continue
		}
		switch l.Dir {
		case record.RPCIn:
			in++
		case record.RPCOut:
			out++
			if strings.Contains(string(l.Msg), `"session/prompt"`) {
				methods = append(methods, "prompt")
			}
		}
	}
	if in == 0 || out == 0 || !slices.Contains(methods, "prompt") {
		t.Errorf("in %d, out %d, prompts sent %v", in, out, methods)
	}
	if last := f.Lines[len(f.Lines)-1]; last.Type != record.TypeRPCStopped {
		t.Errorf("the recording ends with %q", last.Type)
	}
}

type fakeRecorder struct {
	mu      sync.Mutex
	dirs    map[string]int
	gaps    int64
	release chan struct{}
	block   bool
	closed  bool
}

func (f *fakeRecorder) Tap(dir string, raw []byte) {
	f.mu.Lock()
	f.dirs[dir]++
	block := f.block
	f.mu.Unlock()
	if block {
		<-f.release
	}
}
func (f *fakeRecorder) Flush() error { return nil }
func (f *fakeRecorder) Close() error { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }
func (f *fakeRecorder) Gap(n int)    { f.mu.Lock(); f.gaps += int64(n); f.mu.Unlock() }
func (f *fakeRecorder) gapCount() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gaps
}

// A recorder that cannot keep up costs lines, not the turn, and the lines
// lost are noted in the recording.
func TestLinesTheRecorderMissedAreNoted(t *testing.T) {
	before := runtime.NumGoroutine()
	rec := &fakeRecorder{dirs: map[string]int{}, release: make(chan struct{}), block: true}
	o := fakeOptions(t)
	o.Recorder = rec
	r, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Send("storm"); err != nil {
		t.Fatal(err)
	}
	untilFor(t, 3*time.Minute, r, "the turn to end", func(s Snapshot) bool { return idle(s) && s.TapDropped > 0 })

	deadline := time.Now().Add(10 * time.Second)
	for rec.gapCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no gap was noted; the client dropped %d lines", r.Snapshot().TapDropped)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	close(rec.release)
	// Close notes what the last poll had not: every line lost, counted once.
	if got, lost := rec.gapCount(), r.Snapshot().TapDropped; got != lost {
		t.Errorf("noted %d lines, but the client dropped %d", got, lost)
	}
	if !rec.closed {
		t.Error("the recorder was not closed")
	}
	settled(t, before)
}

// A flood of updates ends within the bridge's bounds and the turn completes.
func TestAFloodStaysWithinTheBridgesBounds(t *testing.T) {
	o := fakeOptions(t)
	o.Bridge = bridge.Config{MaxEntries: 50, TextBudget: 1 << 20, DetailBudget: 1 << 20}
	r := begin(t, o)
	if err := r.Send("storm"); err != nil {
		t.Fatal(err)
	}
	// The storm is 72 MiB of updates; the race detector slows it a good deal.
	s := untilFor(t, 3*time.Minute, r, "the flood to end", idle)
	if len(s.Entries) > 50 {
		t.Errorf("%d entries, the bridge allows 50", len(s.Entries))
	}
	var text int
	for _, e := range s.Entries {
		text += len(e.Text) + len(e.Markdown) + len(e.Summary) + len(e.Diff) + len(e.Label)
	}
	if text > 2<<20 {
		t.Errorf("%d bytes of text held, the budget is 1 MiB", text)
	}
}

func TestSubscribersAreSignalled(t *testing.T) {
	r := begin(t, fakeOptions(t))
	ch, stop := r.Subscribe()
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no signal after Send")
	}
	until(t, r, "the reply", func(s Snapshot) bool { return strings.Contains(replies(s), "Hello world") })
	// Drain, then expect nothing more once the pane is quiet.
	for len(ch) > 0 {
		<-ch
	}
	stop()
	_ = r.Send("plain")
	until(t, r, "two replies", func(s Snapshot) bool { return strings.Count(replies(s), "Hello world") == 2 })
	select {
	case <-ch:
		t.Error("a stopped subscription was signalled")
	default:
	}
}

func TestACancelledContextClosesTheRuntime(t *testing.T) {
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := Start(ctx, fakeOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Send("plain")
	until(t, r, "the reply", func(s Snapshot) bool { return strings.Contains(replies(s), "Hello world") })
	cancel()
	until(t, r, "the close", func(s Snapshot) bool { return s.Phase == PhaseClosed })
	_ = r.Close()
	settled(t, before)
}

// What the agent says about itself is cleaned before it is shown.
func TestCleanAndExitText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"a\x1b[31mred\x07\x00", "a[31mred"},
		{"line one\nline two\ttab", "line one line two tab"},
		{"bad \xff\xfe bytes", "bad  bytes"},
		{"bidi \u202eevil\u202c ok", "bidi evil ok"},
		{"  padded  ", "padded"},
		{strings.Repeat("é", 400), strings.Repeat("é", 10) + "…"},
	} {
		n := 300
		if len([]rune(tc.want)) == 11 {
			n = 10
		}
		if got := clean(tc.in, n); got != tc.want {
			t.Errorf("clean(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Detail gives a tool's full output, and nothing for an entry that has none.
func TestDetailIsTheBridgesDetail(t *testing.T) {
	rt := begin(t, fakeOptions(t))
	if err := rt.Send("big"); err != nil {
		t.Fatal(err)
	}
	until(t, rt, "the big turn", func(s Snapshot) bool {
		for _, e := range s.Entries {
			if e.Kind == "reply" && e.Markdown != "" {
				return true
			}
		}
		return false
	})
	var withDetail string
	for _, e := range rt.Snapshot().Entries {
		if e.HasDetail {
			withDetail = e.ID
		}
	}
	if withDetail == "" {
		t.Fatal("no entry has detail after a tool with a 5 MiB result")
	}
	if d, ok := rt.Detail(withDetail); !ok || d.Text == "" {
		t.Errorf("detail = %d bytes, %v", len(d.Text), ok)
	}
	if _, ok := rt.Detail("no-such-entry"); ok {
		t.Error("detail for an entry that is not there")
	}
}

// An adapter's launchEnv reaches the child, after what the entry passes on, so
// the user's own environment cannot undo it; an adapter with none gets nothing.
func TestTheAdaptersLaunchEnvironmentReachesTheChildAndWins(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(adapter string) string {
		t.Helper()
		o := Options{
			Spec: agent.Spec{ID: "fake", Runner: agent.RunnerACP,
				ACP: agent.ACPSpec{Adapter: adapter, PassEnv: []string{"OPENCODE_CONFIG_CONTENT", "KEEP_ME"}}},
			Cwd:     acptest.SessionDir(t),
			Environ: []string{"OPENCODE_CONFIG_CONTENT={\"permission\":\"allow\"}", "KEEP_ME=kept"},
			Command: &Command{Path: exe, Args: []string{acptest.ArgAgent}},
		}
		r := begin(t, o)
		if err := r.Send("env"); err != nil {
			t.Fatal(err)
		}
		return replies(until(t, r, "the environment", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "=") }))
	}
	got := run("opencode")
	want := `OPENCODE_CONFIG_CONTENT={"permission":{"bash":"ask","edit":"ask"}}`
	if n := strings.Count(got, "OPENCODE_CONFIG_CONTENT="); n != 1 || !strings.Contains(got, want) {
		t.Errorf("the child's OPENCODE_CONFIG_CONTENT (%d of them) is not %s:\n%s", n, want, got)
	}
	if !strings.Contains(got, "KEEP_ME=kept") {
		t.Errorf("a passed variable was lost:\n%s", got)
	}
	// An adapter that sets nothing passes the user's own value on, as before.
	if got := run("gemini"); !strings.Contains(got, `OPENCODE_CONFIG_CONTENT={"permission":"allow"}`) {
		t.Errorf("an adapter with no launchEnv changed a passed variable:\n%s", got)
	}
}

// What the agent lists at session/new and sends as state updates reaches the
// snapshot, for the two shapes agents use.
func TestSessionStateReachesTheSnapshot(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithConfigOptions()))
	s := until(t, r, "ready with options", func(s Snapshot) bool { return s.Phase == PhaseReady && len(s.Session.ConfigOptions) == 2 })
	if c := s.Session.Capabilities; !c.LoadSession || !c.Prompt.Image || !c.List {
		t.Errorf("capabilities = %+v", c)
	}
	if s.Session.Modes != nil || s.Session.ConfigOptions[0].Current != "big" {
		t.Errorf("session = %+v", s.Session)
	}
	if err := r.Send("state"); err != nil {
		t.Fatal(err)
	}
	s = until(t, r, "the state", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "state sent") })
	ss := s.Session
	if len(ss.Commands) != 2 || ss.Title != "Fix the parser" || ss.PlanMarkdown != "1. Read the code\n2. Fix it" {
		t.Errorf("session = %+v", ss)
	}
	if ss.ConfigOptions[0].Current != "small" || len(ss.ConfigOptions) != 1 {
		t.Errorf("config options = %+v", ss.ConfigOptions)
	}
	if ss.Modes == nil || ss.Modes.Current != "yolo" {
		t.Errorf("modes = %+v", ss.Modes)
	}
	if got := prompts(s); !slices.Equal(got, []string{"state"}) {
		t.Errorf("the echoed user message made a prompt entry: %v", got)
	}
	if !strings.Contains(notices(s), "Compacted 80 messages.") {
		t.Errorf("no compaction notice in %q", notices(s))
	}
	if s.Usage.Used != 90 || s.Usage.Size != 1000 {
		t.Errorf("usage = %+v", s.Usage)
	}
}

func TestSessionStateOfAModesAgent(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithModes()))
	s := until(t, r, "ready with modes", func(s Snapshot) bool { return s.Phase == PhaseReady && s.Session.Modes != nil })
	if s.Session.Modes.Current != "default" || len(s.Session.Models.Available) != 2 || s.Session.Models.Current != "gem-pro" || s.Session.ConfigOptions != nil && len(s.Session.ConfigOptions) != 0 {
		t.Errorf("session = %+v %+v", s.Session.Modes, s.Session.Models)
	}
}

// An update that names another session is not this pane's and is dropped.
func TestAnUpdateForAnotherSessionIsDropped(t *testing.T) {
	r := begin(t, fakeOptions(t))
	until(t, r, "a session", func(s Snapshot) bool { return s.Phase == PhaseReady })
	r.mu.Lock()
	sid := r.session
	r.mu.Unlock()
	h := handler{Handler: r.br.AnswerHandler(r.ask), r: r}
	chunk := func(id, text string) acp.Update {
		return acp.Update{SessionID: id, Kind: acp.UpdateAgentMessageChunk, Content: &acp.ContentBlock{Type: "text", Text: text}}
	}
	h.Update(chunk("someone-else", "forged"))
	h.Update(chunk(sid, "genuine"))
	got := replies(r.Snapshot())
	if strings.Contains(got, "forged") || !strings.Contains(got, "genuine") {
		t.Errorf("replies = %q", got)
	}
}
