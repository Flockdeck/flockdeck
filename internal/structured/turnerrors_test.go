package structured

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/record"
)

// An error answering a prompt that is not an exit, a login or a close ends that
// turn with a notice and leaves the session ready: the prompts behind it still
// run.
func TestARefusedPromptEndsOnlyTheTurn(t *testing.T) {
	r := begin(t, fakeOptions(t))
	for _, p := range []string{"ratelimit", "plain"} {
		if err := r.Send(p); err != nil {
			t.Fatal(err)
		}
	}
	s := until(t, r, "the second prompt to run", func(s Snapshot) bool {
		return idle(s) && strings.Contains(replies(s), "Hello world")
	})
	if !strings.Contains(notices(s), "Turn ended: failed: rpc error -32603: rate limit exceeded") {
		t.Errorf("the failed turn left no notice: %q", notices(s))
	}
	if got := prompts(s); !slices.Equal(got, []string{"ratelimit", "plain"}) {
		t.Errorf("prompts = %v; the queued prompt should not be dropped", got)
	}
	if s.Phase != PhaseReady || !strings.Contains(s.Err, "rate limit") {
		t.Errorf("phase %q, err %q", s.Phase, s.Err)
	}
	// And the session still takes more.
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "a third prompt", func(s Snapshot) bool {
		return idle(s) && strings.Count(replies(s), "Hello world") == 2
	})
}

func dirty(extra string) string {
	return "a" + string(rune(0x1b)) + "[2J" + string(rune(7)) + "b" + string(rune(0x202e)) + "c" + string(rune(0x200b)) + "d" + extra
}

// Everything the agent says in a setup state is cleaned before it is kept.
func TestSetupTextFromTheAgentIsCleaned(t *testing.T) {
	r := &Runtime{
		ctx:   context.Background(),
		phase: PhaseStarting,
		subs:  map[int]chan struct{}{},
		br:    bridge.New(bridge.Config{}),
		opts:  Options{Spec: agent.Spec{ACP: agent.ACPSpec{Login: dirty("")}}},
	}
	r.fail(nil, &acp.AuthRequiredError{
		Method:  "session/new",
		Message: dirty(" message"),
		Methods: []acp.AuthMethod{{ID: dirty(" id"), Name: dirty(" name"), Description: dirty(" description")}},
	})
	s := r.Snapshot()
	if s.Phase != PhaseSetup || s.Setup == nil || len(s.Setup.Methods) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	m := s.Setup.Methods[0]
	for what, got := range map[string]string{"id": m.ID, "name": m.Name, "description": m.Description, "login": s.Setup.Login} {
		if strings.ContainsAny(got, "\x1b\x07") || strings.ContainsRune(got, 0x202e) || strings.ContainsRune(got, 0x200b) {
			t.Errorf("%s was kept with control or format characters: %q", what, got)
		}
		if !strings.HasPrefix(got, "a[2Jbcd") {
			t.Errorf("%s = %q, want the text cleaned and otherwise kept", what, got)
		}
	}
}

// The agent's own words for a login or a failure are never shown: a sign-in
// request says our fixed text, and any other error from initialize or
// session/new says only that the agent is unavailable.
func TestAgentErrorTextNeverReachesTheUser(t *testing.T) {
	const secret = "token sk-ant-123 at C:/Users/someone"
	run := func(err error) Snapshot {
		r := &Runtime{
			ctx:   context.Background(),
			phase: PhaseStarting,
			subs:  map[int]chan struct{}{},
			br:    bridge.New(bridge.Config{}),
			opts:  Options{Spec: agent.Spec{}},
		}
		r.fail(&acp.Client{}, err)
		return r.Snapshot()
	}
	s := run(&acp.AuthRequiredError{Method: "session/new", Message: secret})
	if s.Phase != PhaseSetup || s.Setup == nil || s.Setup.Message != signIn || s.Err != signIn {
		t.Errorf("auth: %+v", s)
	}
	s = run(&acp.RPCError{Code: -32603, Message: secret})
	if s.Phase != PhaseFailed || s.Err != unavailable {
		t.Errorf("default branch: phase %v, err %q", s.Phase, s.Err)
	}
}

func TestCleanRemovesFormatCharacters(t *testing.T) {
	if got := clean("zero"+string(rune(0x200b))+"width"+string(rune(0xfeff))+" "+string(rune(0x202e))+"bidi", 100); got != "zerowidth bidi" {
		t.Errorf("clean = %q", got)
	}
}

// An exit says only that the agent is unavailable. What the agent wrote to
// stderr is never shown, whatever it holds.
func TestExitTextNeverShowsStderr(t *testing.T) {
	for _, in := range []string{"", "only", "Error: Cannot find module 'node-fetch', needs Node 20, key sk-ant-123", strings.Repeat("a", 5000)} {
		if got := exitText(in); got != "the agent is unavailable" {
			t.Errorf("exitText(%.30q) = %q", in, got)
		}
	}
}

// stuckTap is a real recording whose Tap is held up, so the client's queue for
// it overflows; Gap goes to the real recorder.
type stuckTap struct {
	*record.RPCRecorder
	release chan struct{}
}

func (s *stuckTap) Tap(dir string, raw []byte) { <-s.release }

// The lines the client could not hand to the recording are written into it as
// rpc_gap lines, and they add up to the count the client kept.
func TestLinesTheRecordingMissedAreWrittenAsGaps(t *testing.T) {
	dir := t.TempDir()
	m := record.NewManager(func() (string, error) { return dir, nil })
	defer m.Close()
	rec, err := m.StartRPC(record.RPCConfig{Meta: record.Meta{Project: "demo", ProjectRoot: filepath.Join(dir, "demo"), Agent: "fake", Conversation: "c"}})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	o := fakeOptions(t)
	o.Recorder = &stuckTap{RPCRecorder: rec, release: release}
	r, err := Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Send("storm"); err != nil {
		t.Fatal(err)
	}
	untilFor(t, 3*time.Minute, r, "the turn to end", func(s Snapshot) bool { return idle(s) && s.TapDropped > 0 })
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	lost := r.Snapshot().TapDropped

	f, err := record.ReadRPCFile(rec.Path())
	if err != nil {
		t.Fatal(err)
	}
	var noted int64
	for _, l := range f.Lines {
		if l.Type == record.TypeRPCGap {
			noted += l.Dropped
		}
	}
	if noted != lost {
		t.Errorf("the recording notes %d lost lines, the client dropped %d", noted, lost)
	}
}
