package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Answer is what the user said to one question.
type Answer int

const (
	// AnswerNo declines this call. It is the zero value because it is what
	// every way of not answering -- a closed input, an unreadable terminal, a
	// reply nobody recognises -- has to mean.
	AnswerNo Answer = iota
	// AnswerYes permits this call and nothing else.
	AnswerYes
	// AnswerAlways permits this call and everything sharing its prefix for the
	// rest of the session. Offered for run_command only.
	AnswerAlways
)

// Question is one approval put to the user.
type Question struct {
	// Tool is the name of the tool asking, so the notification can say what
	// the pane wants without repeating the whole question.
	Tool string
	// Text is what Approval returned: a sentence ending in a question mark,
	// occasionally with a line or two of detail under it.
	Text string
	// Prefix is the standing permission on offer, or "" when there is none.
	Prefix string
}

// Asker puts an approval to the person at the terminal.
//
// It is an interface because the chat loop, not this package, owns the pane's
// terminal: it knows what is on the screen, whether a turn is streaming, and
// how the input is being read. The tools know only what needs asking.
type Asker interface {
	Ask(ctx context.Context, q Question) (Answer, error)
}

// PrefixApprover is a tool that can offer standing permission for a class of
// calls. run_command is the only one, and the reasoning is on Allowlist.
type PrefixApprover interface {
	Prefix(args json.RawMessage) string
	Allow(prefix string)
}

// Confirm asks about one call and reports whether it may go ahead.
//
// It returns nil when the tool needs no approval, which is the common case and
// the whole reason Approval answers "" rather than the loop deciding for
// itself which tools are dangerous.
func Confirm(ctx context.Context, a Asker, t Tool, args json.RawMessage) error {
	text := t.Approval(args)
	if text == "" {
		return nil
	}
	if a == nil {
		// Nobody to ask means nobody said yes. A chat client with no terminal
		// to ask in -- a test, a pipe -- must not be a chat client in which
		// every write is approved.
		return ErrDenied
	}
	q := Question{Tool: t.Name(), Text: text}
	pa, canAlways := t.(PrefixApprover)
	if canAlways {
		q.Prefix = pa.Prefix(args)
	}
	answer, err := a.Ask(ctx, q)
	if err != nil {
		return err
	}
	switch answer {
	case AnswerAlways:
		if canAlways && q.Prefix != "" {
			pa.Allow(q.Prefix)
		}
		return nil
	case AnswerYes:
		return nil
	default:
		return ErrDenied
	}
}

// TerminalAsker asks in the pane, on the terminal the agent is running in.
type TerminalAsker struct {
	// In and Out are the pane's terminal.
	In  io.Reader
	Out io.Writer
	// Notify is called with a one-line summary the moment the question goes
	// up, before anything is read back.
	//
	// This is the point of asking in Perch rather than in a terminal on its
	// own: the chat client wires it to a Notification hook event, so the pane
	// turns amber in the window and whoever is looking at eleven other panes
	// is told which one is waiting for them. A nil Notify simply asks
	// quietly, which is what a test wants.
	Notify func(message string)

	once sync.Once
	br   *bufio.Reader
}

// Ask writes the question and reads one line back.
func (t *TerminalAsker) Ask(ctx context.Context, q Question) (Answer, error) {
	if err := ctx.Err(); err != nil {
		return AnswerNo, err
	}
	if t.Notify != nil {
		t.Notify(notice(q))
	}
	out := t.Out
	if out == nil {
		out = io.Discard
	}
	choices := "[y/N]"
	if q.Prefix != "" {
		choices = fmt.Sprintf("[y/N/a = always allow `%s`]", q.Prefix)
	}
	fmt.Fprintf(out, "\n%s\n%s ", q.Text, choices)

	t.once.Do(func() {
		in := t.In
		if in == nil {
			in = strings.NewReader("")
		}
		// The reader is kept for the life of the asker: it buffers, and a new
		// one each time would swallow whatever the user typed ahead.
		t.br = bufio.NewReader(in)
	})
	line, err := t.br.ReadString('\n')
	answer := parseAnswer(line, q.Prefix != "")
	if err != nil && strings.TrimSpace(line) == "" {
		// A closed input is not a yes.
		fmt.Fprintf(out, "no\n")
		return AnswerNo, nil
	}
	switch answer {
	case AnswerAlways:
		fmt.Fprintf(out, "always\n")
	case AnswerYes:
		fmt.Fprintf(out, "yes\n")
	default:
		fmt.Fprintf(out, "no\n")
	}
	return answer, nil
}

// parseAnswer reads a reply generously in the two directions that are safe and
// strictly otherwise: anything unrecognised is a no, because the cost of
// misreading a no as a yes is a file the user did not want written.
func parseAnswer(line string, always bool) Answer {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return AnswerYes
	case "a", "always":
		if always {
			return AnswerAlways
		}
		return AnswerNo
	default:
		return AnswerNo
	}
}

// notice is the one line the pane's notification carries. It is the first line
// of the question, because the detail under an edit's question is a diff and a
// notification is read out of the corner of an eye.
func notice(q Question) string {
	text := q.Text
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}
