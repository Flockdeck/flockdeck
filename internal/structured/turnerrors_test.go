package structured

import (
	"context"
	"errors"
	"strconv"
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
	if !strings.Contains(notices(s), "Turn ended: failed: the agent reported an error") {
		t.Errorf("the failed turn left no notice: %q", notices(s))
	}
	if got := prompts(s); !slices.Equal(got, []string{"ratelimit", "plain"}) {
		t.Errorf("prompts = %v; the queued prompt should not be dropped", got)
	}
	if s.Phase != PhaseReady || s.Err != agentError {
		t.Errorf("phase %q, err %q", s.Phase, s.Err)
	}
	// What the agent wrote in its error is the agent's text and never reaches
	// the user: not the state's error, not a transcript entry of any kind.
	seen := s.Err
	for _, e := range s.Entries {
		seen += " " + e.Text + " " + e.Markdown
	}
	for _, marker := range []string{"rate limit exceeded", "-32603", "rpc error"} {
		if strings.Contains(seen, marker) {
			t.Errorf("the agent's error text %q reached the user: %q", marker, seen)
		}
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

// A prompt error whose data names one of the fixed classes ends the turn with
// that class's wording, and a usage limit shows its reset time in the local
// zone. Nothing the agent wrote in the error reaches the user.
func TestFailureClassEndsTheTurnWithFixedText(t *testing.T) {
	r := begin(t, fakeOptions(t))
	if err := r.Send("limit-class"); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the failed turn", func(s Snapshot) bool { return idle(s) && s.Err != "" })
	if !strings.HasPrefix(s.Err, "Usage limit reached, resets at ") || strings.Contains(s.Err, "SECRET") {
		t.Errorf("Err = %q", s.Err)
	}
	if !strings.Contains(notices(s), "Turn ended: failed: "+s.Err) || strings.Contains(notices(s), "SECRET") {
		t.Errorf("notices = %q, Err = %q", notices(s), s.Err)
	}
	if s.Phase != PhaseReady {
		t.Errorf("phase = %q", s.Phase)
	}

	// A class that is not ours is the generic line, and its reset time is ignored.
	if err := r.Send("forged-class"); err != nil {
		t.Fatal(err)
	}
	s = until(t, r, "the forged class", func(s Snapshot) bool { return idle(s) && s.Err == agentError })
	if strings.Contains(notices(s), "SECRET") || strings.Contains(s.Err, "SECRET") {
		t.Errorf("agent text reached the user: %q %q", notices(s), s.Err)
	}
}

func TestFailureText(t *testing.T) {
	now := time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC)
	rpc := func(data string) error {
		return &acp.RPCError{Code: -32603, Message: "SECRET", Data: []byte(data)}
	}
	at := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).Unix(), 10) }
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"no data", &acp.RPCError{Code: -32603, Message: "SECRET"}, agentError},
		{"not an rpc error", errors.New("SECRET"), agentError},
		{"bad json", rpc(`{"class":`), agentError},
		{"unknown class", rpc(`{"class":"SECRET"}`), agentError},
		{"a class is not an object key of ours", rpc(`{"class":"__proto__"}`), agentError},
		{"context", rpc(`{"class":"context_full"}`), failureTexts[ClassContext]},
		{"outage", rpc(`{"class":"outage","resetsAt":` + at(time.Hour) + `}`), failureTexts[ClassOutage]},
		{"refused", rpc(`{"class":"refused"}`), failureTexts[ClassRefused]},
		{"limit without a time", rpc(`{"class":"usage_limit"}`), "Usage limit reached"},
		{"limit later today", rpc(`{"class":"usage_limit","resetsAt":` + at(90*time.Minute) + `}`), "Usage limit reached, resets at 15:30"},
		{"limit another day", rpc(`{"class":"usage_limit","resetsAt":` + at(30*time.Hour) + `}`), "Usage limit reached, resets at Wed 28 Oct 20:00"},
		{"limit already past", rpc(`{"class":"usage_limit","resetsAt":` + at(-time.Minute) + `}`), "Usage limit reached"},
		{"limit too far away", rpc(`{"class":"usage_limit","resetsAt":` + at(40*24*time.Hour) + `}`), "Usage limit reached"},
		{"limit in milliseconds", rpc(`{"class":"usage_limit","resetsAt":` + at(time.Hour) + `000}`), "Usage limit reached"},
		{"limit time as text", rpc(`{"class":"usage_limit","resetsAt":"3pm SECRET"}`), agentError},
		{"limit negative", rpc(`{"class":"usage_limit","resetsAt":-5}`), "Usage limit reached"},
	}
	for _, tc := range tests {
		if got := failureText(tc.err, now); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// the data is bounded
	big := rpc(`{"class":"context_full","pad":"` + strings.Repeat("x", 600) + `"}`)
	if got := failureText(big, now); got != agentError {
		t.Errorf("oversized data: %q", got)
	}
}
