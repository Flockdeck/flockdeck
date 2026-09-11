package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// scriptedWire answers with whatever the script says, one entry per request, and
// keeps what it was asked so a test can look at the conversation as the model
// would have seen it.
type scriptedWire struct {
	mu    sync.Mutex
	turns []turnFunc
	seen  []Request
	n     int
}

// turnFunc is one scripted answer. It is given the request's context because
// what a real stream does when the turn is interrupted is stop.
type turnFunc func(ctx context.Context, req Request, emit func(Event)) error

func (w *scriptedWire) Name() string { return "scripted" }

func (w *scriptedWire) Stream(ctx context.Context, req Request, emit func(Event)) error {
	w.mu.Lock()
	w.seen = append(w.seen, req)
	n := w.n
	w.n++
	w.mu.Unlock()
	if n >= len(w.turns) {
		emit(Event{Kind: EventText, Text: "nothing more to say"})
		return nil
	}
	return w.turns[n](ctx, req, emit)
}

func (w *scriptedWire) requests() []Request {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Request{}, w.seen...)
}

// says is a turn that answers with text and nothing else.
func says(text string) turnFunc {
	return func(_ context.Context, _ Request, emit func(Event)) error {
		emit(Event{Kind: EventText, Text: text})
		emit(Event{Kind: EventUsage, Usage: Usage{In: 10, Out: 3}})
		return nil
	}
}

// asksFor is a turn that calls tools and says nothing.
func asksFor(calls ...ToolCall) turnFunc {
	return func(_ context.Context, _ Request, emit func(Event)) error {
		for _, c := range calls {
			emit(Event{Kind: EventCall, Call: c})
		}
		return nil
	}
}

// fakeTool records what it was asked to do and answers with a fixed string.
type fakeTool struct {
	name     string
	question string
	always   string
	answer   string
	mu       sync.Mutex
	runs     int
}

func (f *fakeTool) Name() string { return f.name }
func (f *fakeTool) Describe() Schema {
	return Schema{Description: "a tool for a test"}
}
func (f *fakeTool) Approval(json.RawMessage) string { return f.question }
func (f *fakeTool) Run(context.Context, json.RawMessage) (string, error) {
	f.mu.Lock()
	f.runs++
	f.mu.Unlock()
	return f.answer, nil
}
func (f *fakeTool) ran() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

// alwaysTool can be approved once for a whole family of calls, the way
// run_command is meant to be.
type alwaysTool struct{ fakeTool }

func (a *alwaysTool) AlwaysKey(json.RawMessage) string { return a.always }

// paneServer stands in for the running application: it takes the lifecycle
// events a pane reports and answers the session's start with a briefing, which
// is exactly what the workspace does.
type paneServer struct {
	srv    *hooks.Server
	mu     sync.Mutex
	events []hooks.Event
}

func newPaneServer(t *testing.T, brief string) *paneServer {
	t.Helper()
	p := &paneServer{}
	srv, err := hooks.Serve(func(ev hooks.Event) {
		p.mu.Lock()
		p.events = append(p.events, ev)
		p.mu.Unlock()
	})
	if err != nil {
		t.Fatalf("hook server: %v", err)
	}
	srv.SetContextHandler(func(string) string { return brief })
	p.srv = srv
	t.Cleanup(func() { srv.Close() })
	return p
}

func (p *paneServer) names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, ev := range p.events {
		name := ev.Event
		if ev.Tool != "" {
			name += ":" + ev.Tool
		}
		out = append(out, name)
	}
	return out
}

func (p *paneServer) saw(name string) bool {
	for _, n := range p.names() {
		if n == name {
			return true
		}
	}
	return false
}

// run drives one chat session to the end, and returns what it drew.
func run(t *testing.T, o Options, input string, wire Wire) string {
	t.Helper()
	var out strings.Builder
	o.In = strings.NewReader(input)
	o.Out = &out
	o.Width = 70
	o.wire = wire
	if o.Dir == "" {
		o.Dir = t.TempDir()
	}
	if o.Session == "" {
		o.Session = "session-1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Run(ctx, o); err != nil {
		t.Fatalf("run: %v", err)
	}
	return out.String()
}

func TestChatAnswersItsOpeningTaskAndReportsItsLifecycle(t *testing.T) {
	pane := newPaneServer(t, "you are the pane called one")
	wire := &scriptedWire{turns: []turnFunc{says("here is the answer")}}
	dir := t.TempDir()

	out := run(t, Options{
		Agent: "anthropic", Model: "claude-opus-5", Dir: dir,
		Task: "say something", API: pane.srv.BaseURL(), Token: pane.srv.Token(),
	}, "", wire)

	if !strings.Contains(out, "here is the answer") {
		t.Errorf("the answer was not drawn:\n%s", out)
	}
	// The pane's briefing arrives as the reply to the session's own start event,
	// and has to reach the model.
	reqs := wire.requests()
	if len(reqs) != 1 {
		t.Fatalf("made %d requests, want 1", len(reqs))
	}
	if !strings.Contains(reqs[0].System, "you are the pane called one") {
		t.Errorf("system prompt does not carry the briefing:\n%s", reqs[0].System)
	}
	if !strings.Contains(reqs[0].System, "<flockdeck-context>") {
		t.Errorf("the briefing is not fenced off from what we said:\n%s", reqs[0].System)
	}
	if reqs[0].Model != "claude-opus-5" {
		t.Errorf("model = %q", reqs[0].Model)
	}

	for _, want := range []string{"SessionStart", "UserPromptSubmit", "Stop", "SessionEnd"} {
		if !pane.saw(want) {
			t.Errorf("the pane never reported %s; it reported %q", want, pane.names())
		}
	}

	entries, err := ReadEntries(dir + string(os.PathSeparator) + "session-1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Type != "user" || entries[1].Type != "assistant" {
		t.Fatalf("transcript = %+v", entries)
	}
	if entries[1].Text != "here is the answer" || entries[1].Model != "claude-opus-5" {
		t.Errorf("the answer was recorded as %+v", entries[1])
	}
}

func TestChatAsksBeforeRunningAToolAndTellsThePaneItIsWaiting(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		wantRun int
		wantIn  string
	}{
		{name: "agreed", answer: "y\n", wantRun: 1, wantIn: "the file says hello"},
		{name: "declined", answer: "n\n", wantRun: 0, wantIn: "declined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pane := newPaneServer(t, "")
			tool := &fakeTool{
				name: "write_file", question: "write to a.txt?", answer: "the file says hello",
			}
			wire := &scriptedWire{turns: []turnFunc{
				asksFor(ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`{"path":"a.txt"}`)}),
				says("done"),
			}}
			// The task opens the turn, so everything typed is the answer to the
			// tool's question rather than a prompt of its own.
			out := run(t, Options{
				Agent: "anthropic", Tools: []Tool{tool}, Task: "write to a.txt",
				API: pane.srv.BaseURL(), Token: pane.srv.Token(),
			}, tc.answer, wire)

			if !strings.Contains(out, "write to a.txt?") {
				t.Errorf("the question was not put to the user:\n%s", out)
			}
			if tool.ran() != tc.wantRun {
				t.Errorf("the tool ran %d times, want %d", tool.ran(), tc.wantRun)
			}
			// The question is a Notification, which is what turns the pane amber
			// and tells the user which pane is waiting for them.
			if !pane.saw("Notification:write_file") {
				t.Errorf("the pane never said it was waiting; it reported %q", pane.names())
			}
			if tc.wantRun > 0 {
				for _, want := range []string{"PreToolUse:write_file", "PostToolUse:write_file"} {
					if !pane.saw(want) {
						t.Errorf("the pane never reported %s; it reported %q", want, pane.names())
					}
				}
			}
			// Whatever happened, the model is told about it in the next request.
			reqs := wire.requests()
			if len(reqs) != 2 {
				t.Fatalf("made %d requests, want 2", len(reqs))
			}
			last := reqs[1].Messages[len(reqs[1].Messages)-1]
			if last.Role != RoleTool || !strings.Contains(last.Text, tc.wantIn) {
				t.Errorf("the tool answer was %+v, want one mentioning %q", last, tc.wantIn)
			}
		})
	}
}

func TestChatAsksOnceWhenAToolCanBeApprovedForGood(t *testing.T) {
	pane := newPaneServer(t, "")
	tool := &alwaysTool{fakeTool: fakeTool{
		name: "run_command", question: "run `go test ./...`?", answer: "ok",
	}}
	tool.always = "go test"
	wire := &scriptedWire{turns: []turnFunc{
		asksFor(
			ToolCall{ID: "c1", Name: "run_command", Args: json.RawMessage(`{"cmd":"go test ./a"}`)},
			ToolCall{ID: "c2", Name: "run_command", Args: json.RawMessage(`{"cmd":"go test ./b"}`)},
		),
		says("both green"),
	}}

	// One answer for two calls: if the second call asked as well, it would read
	// the end of the input and stop the turn instead of running.
	out := run(t, Options{
		Agent: "anthropic", Tools: []Tool{tool}, Task: "run the tests",
		API: pane.srv.BaseURL(), Token: pane.srv.Token(),
	}, "a\n", wire)

	if tool.ran() != 2 {
		t.Errorf("the tool ran %d times, want both calls", tool.ran())
	}
	if !strings.Contains(out, "always for go test") {
		t.Errorf("the offer to agree for good was not made:\n%s", out)
	}
}

func TestChatTellsTheModelAboutAToolThatIsNotThere(t *testing.T) {
	wire := &scriptedWire{turns: []turnFunc{
		asksFor(ToolCall{ID: "c1", Name: "delete_everything"}),
		says("understood"),
	}}
	run(t, Options{Agent: "anthropic", Task: "delete everything"}, "", wire)

	reqs := wire.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	last := reqs[1].Messages[len(reqs[1].Messages)-1]
	if last.Role != RoleTool || !strings.Contains(last.Text, "no tool called") {
		t.Errorf("the model was told %+v", last)
	}
}

func TestChatStopsAToolLoopThatWillNotEnd(t *testing.T) {
	var turns []turnFunc
	for i := 0; i < maxToolSteps+5; i++ {
		turns = append(turns, asksFor(ToolCall{ID: "c", Name: "list_dir"}))
	}
	tool := &fakeTool{name: "list_dir", answer: "a b c"}
	wire := &scriptedWire{turns: turns}

	out := run(t, Options{Agent: "anthropic", Tools: []Tool{tool}, Task: "look around"}, "", wire)

	if !strings.Contains(out, "stopping:") {
		t.Errorf("a runaway tool loop was not stopped:\n%s", out)
	}
	if got := len(wire.requests()); got > maxToolSteps+2 {
		t.Errorf("made %d requests before stopping", got)
	}
}

func TestCtrlCEndsTheTurnAndNotTheClient(t *testing.T) {
	started := make(chan struct{})
	wire := &scriptedWire{turns: []turnFunc{
		func(ctx context.Context, _ Request, emit func(Event)) error {
			emit(Event{Kind: EventText, Text: "thinking about it"})
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}}
	signals := make(chan os.Signal, 1)
	go func() {
		<-started
		signals <- os.Interrupt
	}()

	out := run(t, Options{
		Agent: "anthropic", Task: "take your time", Signals: signals,
	}, "/exit\n", wire)

	if !strings.Contains(out, "(interrupted)") {
		t.Errorf("the interruption was not shown:\n%s", out)
	}
	if !strings.Contains(out, "thinking about it") {
		t.Errorf("what had been written was thrown away:\n%s", out)
	}
}

func TestSlashCommands(t *testing.T) {
	pane := newPaneServer(t, "the briefing")
	wire := &scriptedWire{turns: []turnFunc{
		says("with the new model"),
	}}
	out := run(t, Options{
		Agent: "anthropic", Model: "first-model",
		API: pane.srv.BaseURL(), Token: pane.srv.Token(),
	}, strings.Join([]string{
		"/help",
		"/nonsense",
		"/model second-model",
		"ask something",
		"/status",
		"/exit",
	}, "\n")+"\n", wire)

	for _, want := range []string{"commands", "no such command: /nonsense", "session session-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	reqs := wire.requests()
	if len(reqs) != 1 {
		t.Fatalf("made %d requests, want 1", len(reqs))
	}
	if reqs[0].Model != "second-model" {
		t.Errorf("asked %q, want the model /model chose", reqs[0].Model)
	}
}

func TestClearStartsOverAndAsksForTheBriefingAgain(t *testing.T) {
	pane := newPaneServer(t, "the briefing")
	wire := &scriptedWire{turns: []turnFunc{
		says("first"), says("second"),
	}}
	dir := t.TempDir()
	run(t, Options{
		Agent: "anthropic", Dir: dir,
		API: pane.srv.BaseURL(), Token: pane.srv.Token(),
	}, "one\n/clear\ntwo\n/exit\n", wire)

	reqs := wire.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	if len(reqs[1].Messages) != 1 || reqs[1].Messages[0].Text != "two" {
		t.Errorf("the cleared conversation still carried %+v", reqs[1].Messages)
	}
	// Two starts and one end: a cleared conversation is a new one, which is
	// what Claude Code's own hooks report.
	starts := 0
	for _, name := range pane.names() {
		if name == "SessionStart" {
			starts++
		}
	}
	if starts != 2 {
		t.Errorf("the pane reported %d session starts, want 2: %q", starts, pane.names())
	}
}

func TestResumePutsTheConversationBack(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "session-1", "/work")
	if err != nil {
		t.Fatal(err)
	}
	log.Append(Entry{Type: "user", Text: "what did we decide?"})
	log.Append(Entry{Type: "assistant", Text: "we decided to wait"})
	log.Close()

	wire := &scriptedWire{turns: []turnFunc{says("still waiting")}}
	out := run(t, Options{
		Agent: "anthropic", Dir: dir, Resume: true, Task: "and now?",
	}, "", wire)

	if !strings.Contains(out, "we decided to wait") {
		t.Errorf("the earlier conversation was not drawn:\n%s", out)
	}
	reqs := wire.requests()
	if len(reqs) != 1 {
		t.Fatalf("made %d requests, want 1", len(reqs))
	}
	if len(reqs[0].Messages) != 3 {
		t.Fatalf("resumed with %d messages, want 3: %+v", len(reqs[0].Messages), reqs[0].Messages)
	}
	if reqs[0].Messages[0].Text != "what did we decide?" {
		t.Errorf("the conversation came back as %+v", reqs[0].Messages)
	}
}

func TestChatWithoutAnApplicationToReportTo(t *testing.T) {
	// Somebody can run `flockdeck chat` in a plain terminal, where there is no pane
	// and nothing listening: it must be a chat client all the same.
	wire := &scriptedWire{turns: []turnFunc{says("no pane here")}}
	out := run(t, Options{Agent: "anthropic", Task: "hello"}, "", wire)
	if !strings.Contains(out, "no pane here") {
		t.Errorf("output = %q", out)
	}
}

// TestChatTalksToAnEndpointForReal is the one test that builds the wire from
// the options the way the subcommand does, so that the whole path -- the key,
// the endpoint, the request, the stream, the drawing -- is exercised once.
func TestChatTalksToAnEndpointForReal(t *testing.T) {
	t.Setenv("MY_TEST_KEY", "sk-test")
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"# Right here"}}` +
			"\n\n"))
	}))
	defer srv.Close()

	var out strings.Builder
	err := Run(context.Background(), Options{
		Agent: "anthropic", Wire: "anthropic", BaseURL: srv.URL, KeyEnv: []string{"MY_TEST_KEY"},
		Model: "claude-opus-5", Session: "session-1", Dir: t.TempDir(), Task: "are you there?",
		In: strings.NewReader(""), Out: &out, Width: 40,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotKey != "sk-test" {
		t.Errorf("the endpoint was given the key %q", gotKey)
	}
	if !strings.Contains(out.String(), "Right here") {
		t.Errorf("output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "sk-test") {
		t.Errorf("the key was drawn on the screen:\n%s", out.String())
	}
}

func TestKeyIsFoundInTheEnvironmentTheSpecNames(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		env     map[string]string
		want    string
		wantErr string
	}{
		{
			name: "the name the spec gives",
			opts: Options{Wire: "openai", KeyEnv: []string{"MY_OWN_KEY"}},
			env:  map[string]string{"MY_OWN_KEY": "sk-one"},
			want: "sk-one",
		},
		{
			name: "the conventional name for the wire",
			opts: Options{Wire: "gemini"},
			env:  map[string]string{"GEMINI_API_KEY": "sk-two"},
			want: "sk-two",
		},
		{
			name: "a model on this machine needs no key",
			opts: Options{Wire: "openai", BaseURL: "http://127.0.0.1:11434/v1"},
			want: "",
		},
		{
			name:    "nothing anywhere is explained, without the key",
			opts:    Options{Agent: "openai", Wire: "openai"},
			wantErr: "OPENAI_API_KEY",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{
				"MY_OWN_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
				"GEMINI_API_KEY", "GOOGLE_API_KEY", "FLOCKDECK_API_KEY",
			} {
				t.Setenv(name, "")
			}
			for name, v := range tc.env {
				t.Setenv(name, v)
			}
			got, err := resolveKey(tc.opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKeyStoreIsAskedLast(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "FLOCKDECK_API_KEY"} {
		t.Setenv(name, "")
	}
	KeyStore = func(agent string) string {
		if agent == "anthropic" {
			return "sk-from-the-store"
		}
		return ""
	}
	defer func() { KeyStore = nil }()

	got, err := resolveKey(Options{Agent: "anthropic", Wire: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-from-the-store" {
		t.Errorf("key = %q", got)
	}
}
