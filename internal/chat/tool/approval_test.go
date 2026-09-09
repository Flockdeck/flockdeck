package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// scriptedAsker answers whatever it was told to, and records what it was asked.
type scriptedAsker struct {
	answer Answer
	err    error
	asked  []Question
}

func (a *scriptedAsker) Ask(_ context.Context, q Question) (Answer, error) {
	a.asked = append(a.asked, q)
	return a.answer, a.err
}

func TestConfirm(t *testing.T) {
	root := newRoot(t)
	write(t, root, "a.txt", "alpha\n")

	tests := []struct {
		name string
		tool Tool
		args map[string]any
		// answer is what the user says; asked is whether they should be asked
		// at all; wantErr is whether the call is stopped.
		answer  Answer
		asked   bool
		wantErr bool
	}{
		{
			name: "a read is not asked about",
			tool: &readFile{root: root}, args: map[string]any{"path": "a.txt"},
		},
		{
			name: "a write is asked about and may be allowed",
			tool: &writeFile{root: root}, args: map[string]any{"path": "b.txt", "content": "x"},
			answer: AnswerYes, asked: true,
		},
		{
			name: "a write is asked about and may be refused",
			tool: &writeFile{root: root}, args: map[string]any{"path": "b.txt", "content": "x"},
			answer: AnswerNo, asked: true, wantErr: true,
		},
		{
			name: "an edit is asked about",
			tool: &editFile{root: root}, args: map[string]any{"path": "a.txt", "old_string": "alpha", "new_string": "beta"},
			answer: AnswerYes, asked: true,
		},
		{
			name: "a command is asked about",
			tool: &runCommand{root: root, allow: NewAllowlist()}, args: map[string]any{"command": "go test ./..."},
			answer: AnswerYes, asked: true,
		},
		{
			name: "a write outside the root is stopped without asking",
			tool: &writeFile{root: root}, args: map[string]any{"path": "../b.txt", "content": "x"},
			answer: AnswerYes, asked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asker := &scriptedAsker{answer: tt.answer}
			err := Confirm(context.Background(), asker, tt.tool, rawArgs(t, tt.args))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Confirm = %v, want an error: %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrDenied) {
				t.Errorf("a refusal should be ErrDenied, got %v", err)
			}
			if got := len(asker.asked) > 0; got != tt.asked {
				t.Errorf("asked = %v, want %v", got, tt.asked)
			}
		})
	}
}

// TestConfirmAlwaysRecordsThePrefix is the "always for the rest of this
// session" the design offers for run_command and for nothing else.
func TestConfirmAlwaysRecordsThePrefix(t *testing.T) {
	root := newRoot(t)
	allow := NewAllowlist()
	cmd := &runCommand{root: root, allow: allow}
	args := rawArgs(t, map[string]any{"command": "go test ./internal/..."})

	asker := &scriptedAsker{answer: AnswerAlways}
	if err := Confirm(context.Background(), asker, cmd, args); err != nil {
		t.Fatal(err)
	}
	if len(asker.asked) != 1 || asker.asked[0].Prefix != "go test" {
		t.Fatalf("the question should have offered the prefix: %+v", asker.asked)
	}
	if !allow.Allowed("go test") {
		t.Error("saying always should have recorded the prefix")
	}
	// The next call with the same prefix goes through without a question.
	asker.asked = nil
	if err := Confirm(context.Background(), asker, cmd, rawArgs(t, map[string]any{"command": "go test ./cmd/..."})); err != nil {
		t.Fatal(err)
	}
	if len(asker.asked) != 0 {
		t.Error("an allowed prefix should not be asked about again")
	}

	// Nothing but run_command offers a standing permission.
	write := &writeFile{root: root}
	asker = &scriptedAsker{answer: AnswerAlways}
	if err := Confirm(context.Background(), asker, write, rawArgs(t, map[string]any{"path": "b.txt", "content": "x"})); err != nil {
		t.Fatal(err)
	}
	if len(asker.asked) != 1 || asker.asked[0].Prefix != "" {
		t.Errorf("write_file must not offer a standing permission: %+v", asker.asked)
	}
}

// TestConfirmWithNobodyToAsk guards the failure that would be silent: a chat
// client with no terminal must refuse, not assume yes.
func TestConfirmWithNobodyToAsk(t *testing.T) {
	root := newRoot(t)
	err := Confirm(context.Background(), nil, &writeFile{root: root}, rawArgs(t, map[string]any{"path": "b.txt", "content": "x"}))
	if !errors.Is(err, ErrDenied) {
		t.Errorf("got %v, want ErrDenied", err)
	}
}

func TestTerminalAsker(t *testing.T) {
	tests := []struct {
		name  string
		typed string
		// prefix being set is what puts "always" on offer.
		prefix string
		want   Answer
	}{
		{name: "y", typed: "y\n", want: AnswerYes},
		{name: "yes", typed: "YES\n", want: AnswerYes},
		{name: "n", typed: "n\n", want: AnswerNo},
		{name: "just enter", typed: "\n", want: AnswerNo},
		{name: "something else entirely", typed: "maybe\n", want: AnswerNo},
		{name: "nothing at all, because the input closed", typed: "", want: AnswerNo},
		{name: "always, when it is on offer", typed: "a\n", prefix: "go test", want: AnswerAlways},
		{name: "always, when it is not on offer", typed: "a\n", want: AnswerNo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			var notified []string
			asker := &TerminalAsker{
				In:     strings.NewReader(tt.typed),
				Out:    &out,
				Notify: func(m string) { notified = append(notified, m) },
			}
			q := Question{Tool: "run_command", Text: "Run `go test` in /repo?\nmore detail", Prefix: tt.prefix}
			got, err := asker.Ask(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Ask = %v, want %v", got, tt.want)
			}
			if !strings.Contains(out.String(), "Run `go test` in /repo?") {
				t.Errorf("the question should be on the terminal:\n%s", out.String())
			}
			if (tt.prefix != "") != strings.Contains(out.String(), "always allow") {
				t.Errorf("the standing permission was offered wrongly:\n%s", out.String())
			}
			// The notification is what turns the pane amber, so it goes up
			// every time, before anything is read back.
			if len(notified) != 1 || notified[0] != "Run `go test` in /repo?" {
				t.Errorf("notified = %q", notified)
			}
		})
	}
}

// TestTerminalAskerKeepsItsReader guards against losing input typed ahead: a
// fresh bufio.Reader for each question would swallow whatever came after the
// line it read.
func TestTerminalAskerKeepsItsReader(t *testing.T) {
	asker := &TerminalAsker{In: strings.NewReader("y\ny\n"), Out: &bytes.Buffer{}}
	for i := 0; i < 2; i++ {
		got, err := asker.Ask(context.Background(), Question{Tool: "write_file", Text: "Create a.txt?"})
		if err != nil {
			t.Fatal(err)
		}
		if got != AnswerYes {
			t.Fatalf("question %d answered %v", i+1, got)
		}
	}
}

func TestConfirmPassesTheAskersError(t *testing.T) {
	root := newRoot(t)
	boom := errors.New("the terminal went away")
	asker := &scriptedAsker{err: boom}
	err := Confirm(context.Background(), asker, &writeFile{root: root}, rawArgs(t, map[string]any{"path": "b.txt", "content": "x"}))
	if !errors.Is(err, boom) {
		t.Errorf("got %v, want the asker's own error", err)
	}
}

// TestApprovalSurvivesNonsense keeps a malformed call from becoming an
// approval question nobody can read.
func TestApprovalSurvivesNonsense(t *testing.T) {
	root := newRoot(t)
	set, err := New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range set.Tools() {
		for _, args := range []json.RawMessage{nil, json.RawMessage(`{`), json.RawMessage(`{"path": 5}`)} {
			if q := tl.Approval(args); q != "" {
				t.Errorf("%s asked about arguments it could not read: %q", tl.Name(), q)
			}
		}
	}
}
