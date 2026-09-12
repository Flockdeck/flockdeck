package chat

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"time"
)

// Options are what one run of the chat client needs. Everything here is either
// a command line flag or something the pane's environment already carries, so
// that `flockdeck chat` behaves the same run from a pane and run by hand.
type Options struct {
	// Agent is the catalog id of the agent this pane was started as. It names
	// the key to look for and is what the header says.
	Agent string
	Model string
	// Models are what the agent's catalog entry offers, which /model lists and
	// picks from by number. Any other id can still be named.
	Models  []ModelChoice
	Session string
	Resume  bool
	// Task is the opening prompt, which arrives in the argv rather than being
	// typed, because typing into a program means guessing when it is ready.
	Task string

	// Wire, BaseURL and KeyEnv are the agent's APISpec, passed in rather than
	// looked up: what the pane runs is decided by whoever built the argv, and
	// the chat client does not need a catalog of its own to be told.
	Wire      string
	BaseURL   string
	KeyEnv    []string
	MaxTokens int

	Cwd string
	// Dir is where transcripts are kept. Empty means the state directory,
	// which is where they belong; a test says otherwise.
	Dir string

	// API and Token are the pane's callback address and secret.
	API   string
	Token string

	In     io.Reader
	Out    io.Writer
	Width  int
	Colour bool

	// Tools are what the model can do besides talk. The loop below already
	// handles asking for a call, the approval, the lifecycle events and feeding
	// the answer back; the tools themselves are supplied from outside so that
	// they and the loop can be built apart.
	Tools []Tool

	// Signals delivers interrupts. Empty means the client subscribes to them
	// itself, which is what it does in a pane.
	Signals <-chan os.Signal

	// wire, when set, is used instead of building one from Wire and BaseURL. It
	// is how the loop is tested without a model to talk to.
	wire Wire
}

// ModelChoice is one model /model offers.
type ModelChoice struct {
	ID   string
	Name string
	Note string
}

// defaultMaxTokens is the ceiling on one answer where the API insists on one
// and the user named none. It is high because this is a streamed conversation,
// where a long answer costs patience rather than a timeout, and an answer cut
// off mid-sentence is worth nothing.
//
// Only the Anthropic wire sends it. The others take no ceiling to mean the
// model's own, and a figure sent where none was asked for is refused outright
// by a model whose limit is lower -- an older OpenAI model, or a local one
// served with a short context.
const defaultMaxTokens = 32000

// maxToolSteps bounds how many times one turn may call tools before the loop
// insists the model say something to the user.
//
// Without a bound a model that keeps asking for the same thing turns a pane
// into a machine that spends money in a loop while nobody is watching. Thirty
// is well past what real work needs in one turn.
const maxToolSteps = 30

// busyBackoff is how long to wait before asking a busy API again, once for each
// attempt, where it did not say how long itself. Twice is enough to ride out a
// moment's overload and few enough that a real outage is reported promptly.
var busyBackoff = []time.Duration{2 * time.Second, 6 * time.Second}

// Run is the chat client: it holds one conversation until the user leaves, the
// input ends, or the context is cancelled.
func Run(ctx context.Context, o Options) error {
	if o.In == nil {
		o.In = os.Stdin
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	// A width the caller gave is kept; otherwise it is asked for again before
	// each answer, so a pane that has been resized is wrapped to its new size.
	autoWidth := o.Width <= 0
	if autoWidth {
		o.Width = resolveWidth()
	}
	if o.Cwd == "" {
		o.Cwd, _ = os.Getwd()
	}
	if o.Session == "" {
		id, err := newSessionID(func(b []byte) error { _, err := rand.Read(b); return err })
		if err != nil {
			return fmt.Errorf("make a session id: %w", err)
		}
		o.Session = id
	}

	wire := o.wire
	var key string
	if wire == nil {
		var err error
		key, err = resolveKey(o)
		if err != nil {
			return err
		}
		wire, err = NewWire(o.Wire, o.BaseURL, key)
		if err != nil {
			return err
		}
	}

	dir := o.Dir
	if dir == "" {
		d, err := Dir()
		if err != nil {
			return err
		}
		dir = d
	}
	log, err := OpenLog(dir, o.Session, o.Cwd)
	if err != nil {
		return err
	}
	defer log.Close()
	// Every entry says which agent it was written by: the chats of every API
	// agent share one folder, and one reopened with no agent recorded is
	// reopened as whichever API agent comes first.
	log.agent = o.Agent

	s := &session{
		opts:      o,
		wire:      wire,
		key:       key,
		log:       log,
		out:       newPrinter(o.Out, o.Width, o.Colour),
		in:        newInput(o.In, isConsole(o.In)),
		reporter:  newReporter(o.API, o.Token, o.Session, o.Cwd),
		model:     o.Model,
		tools:     map[string]Tool{},
		always:    map[string]bool{},
		autoWidth: autoWidth,
	}
	for _, t := range o.Tools {
		s.tools[t.Name()] = t
	}

	signals := o.Signals
	if signals == nil {
		ch := make(chan os.Signal, 4)
		// A pane sends an interrupt when the user presses Ctrl+C, and the
		// promise here is that it ends the turn rather than the client: a chat
		// whose window closes on a mistyped question is not a chat.
		signal.Notify(ch, os.Interrupt)
		defer signal.Stop(ch)
		signals = ch
	}
	s.signals = signals

	return s.run(ctx)
}

// session is one conversation in progress.
type session struct {
	opts Options
	wire Wire
	// key is the one the wire was built with, so that a refused one can be
	// told from a new one set since.
	key      string
	log      *Log
	out      *printer
	in       *input
	reporter *reporter
	signals  <-chan os.Signal

	model    string
	system   string
	messages []Message
	total    Usage
	spent    spend
	tools    map[string]Tool
	// always remembers the families of calls the user has agreed to for the
	// rest of the session. A family is named by the tools themselves and may
	// span more than one of them: every write and every edit are one family.
	always map[string]bool
	// interrupted is set from the goroutine watching for interrupts and read
	// back once the turn has ended, so nothing is drawn from two goroutines.
	interrupted atomic.Bool
	// autoWidth is set when the width is the terminal's rather than given.
	autoWidth bool
	// ahead are lines the user typed while the model was working, kept for
	// the prompt they were meant for.
	ahead []string
	// listed are the models the endpoint said it offers, for an agent whose
	// catalog entry lists none.
	listed []ModelChoice
	// unfinished is set when the last turn ended without its answer --
	// interrupted, failed, cut short -- which is what /retry carries on.
	unfinished bool
}

// systemPrompt is what the model is told about where it is before anything
// else. The pane's own briefing follows it, fenced, so the model can tell what
// Flockdeck said from what we said.
const systemPrompt = `You are a coding agent working in a terminal pane, talking to a developer.
Answer in plain prose and short paragraphs; use markdown sparingly, and code
fences for code. Say what you did and what you would do next, not what you are
about to start doing. When you are unsure, say so and ask.`

func (s *session) run(ctx context.Context) error {
	source := "startup"
	if s.opts.Resume {
		source = "resume"
	}
	// The reply to this is the pane's briefing -- which pane this is, who else
	// is working, what it can ask Flockdeck for.
	s.system = compose(systemPrompt, s.reporter.sessionStart(source))

	s.out.line(ansiDim, fmt.Sprintf("flockdeck chat · %s · %s · /help for what it can do",
		firstNonEmpty(s.opts.Agent, s.wire.Name()), firstNonEmpty(s.model, "the endpoint's own model")))

	if s.opts.Resume {
		s.replay()
	}
	if s.model == "" {
		// Most endpoints want a model named, a local model server above all,
		// and the first sign of it would otherwise be the first answer
		// failing with "model is required".
		s.out.line(ansiDim, "no model is named for this agent; /model shows the ones to choose from")
	}

	queued := strings.TrimSpace(s.opts.Task)
	for {
		if queued == "" {
			line, ok := s.readPrompt(ctx)
			if !ok {
				s.reporter.sessionEnd()
				return nil
			}
			queued = line
		}
		prompt := queued
		queued = ""
		if prompt == "" {
			continue
		}
		if strings.HasPrefix(prompt, "/") {
			if leave := s.command(ctx, prompt); leave {
				s.reporter.sessionEnd()
				return nil
			}
			continue
		}
		s.turn(ctx, prompt)
	}
}

// readPrompt draws the status line and waits for what the user types.
//
// A line ending in a backslash is continued on the next one, which is how a
// prompt with a paragraph in it is typed without raw mode.
func (s *session) readPrompt(ctx context.Context) (string, bool) {
	var gathered []string
	var interruptedAt time.Time
	for {
		if len(gathered) == 0 {
			s.out.blankLine()
			s.out.line(ansiDim, statusLine(s.model, s.total, s.spent))
			s.out.bare(ansiBold, "you > ")
		} else {
			s.out.bare(ansiBold, "   … ")
		}
		var line string
		if len(s.ahead) > 0 {
			// Typed while the model worked; shown again after the prompt it
			// now answers, since it went by in the middle of the answer.
			line, s.ahead = s.ahead[0], s.ahead[1:]
			s.out.line("", line)
		} else {
			select {
			case <-ctx.Done():
				return "", false
			case <-s.in.closed:
				s.out.line("", "")
				return "", false
			case <-s.signals:
				s.in.interrupt()
				// Nothing is running, so there is no turn to interrupt. Leaving
				// on the first Ctrl+C would throw away a conversation over a
				// stray keystroke; leaving on none of them would trap somebody
				// whose habit it is.
				if !interruptedAt.IsZero() && time.Since(interruptedAt) < 2*time.Second {
					s.out.line("", "")
					return "", false
				}
				interruptedAt = time.Now()
				gathered = nil
				s.out.line(ansiDim, "(press Ctrl+C again to leave, or type /exit)")
				continue
			case line = <-s.in.lines:
				// A paste arrives as lines in a burst, and each would
				// otherwise be sent as a prompt of its own: a stack trace
				// pasted to ask about it went as twenty questions.
				if rest := s.in.pending(); len(rest) > 0 {
					line = strings.Join(append([]string{line}, rest...), "\n")
					s.out.line(ansiDim, fmt.Sprintf("(%d pasted lines, sent as one prompt)", len(rest)+1))
				}
			}
		}
		if strings.Contains(line, lineTooLong) {
			s.out.line(ansiRed, fmt.Sprintf("(that line was longer than %d MB and was not sent; put it in a file and ask for the file instead)", maxLine>>20))
			gathered = nil
			continue
		}
		if strings.HasSuffix(line, "\\") {
			gathered = append(gathered, strings.TrimSuffix(line, "\\"))
			continue
		}
		gathered = append(gathered, line)
		return strings.TrimSpace(strings.Join(gathered, "\n")), true
	}
}

// turn runs one exchange: the user's prompt, the model's answer, and any tools
// it asks for along the way.
func (s *session) turn(ctx context.Context, prompt string) {
	s.messages = append(s.messages, Message{Role: RoleUser, Text: prompt})
	s.record(Entry{Type: string(RoleUser), Text: prompt})
	s.carryOn(ctx, prompt)
}

// carryOn asks the model to go on from where the conversation stands, which is
// a prompt the user has just added, or -- for /retry -- one that was never
// answered.
func (s *session) carryOn(ctx context.Context, prompt string) {
	s.reporter.userPrompt(prompt)
	s.unfinished = false
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.interrupted.Store(false)
	watching := make(chan struct{})
	go func() {
		for {
			select {
			case <-s.signals:
				s.in.interrupt()
				// Interrupting is drawn once the turn has stopped: the printer
				// belongs to the goroutine below, and two goroutines writing to
				// a terminal produce one unreadable line.
				s.interrupted.Store(true)
				cancel()
			case <-watching:
				return
			}
		}
	}()
	defer close(watching)

	rekeyed, retries := false, 0
	for step := 0; ; step++ {
		before := len(s.messages)
		calls, err := s.stream(turnCtx)
		if e, ok := busy(err); ok && retries < len(busyBackoff) && len(s.messages) == before {
			// Nothing of an answer arrived, so asking again repeats nothing;
			// and a busy API is the one failure that asking again fixes. An
			// answer the API gave up on part-way is not asked for again: what
			// was drawn of it would be drawn twice.
			wait := busyBackoff[retries]
			if e.RetryAfter > 0 && e.RetryAfter <= time.Minute {
				wait = e.RetryAfter
			}
			retries++
			s.out.line(ansiDim, fmt.Sprintf("(%s; trying again in %s)", e.Status, wait.Round(time.Second)))
			select {
			case <-time.After(wait):
				continue
			case <-turnCtx.Done():
				err = turnCtx.Err()
			}
		}
		if err != nil && refusedKey(err) && !rekeyed && s.rekey() {
			// Somebody who has just set a new key should not have to restart
			// the pane for it, or type the prompt again.
			rekeyed = true
			s.out.line(ansiDim, "(the key was refused; trying again with the one now set)")
			continue
		}
		if err != nil {
			s.sayWhyItStopped(err)
			s.unfinished = true
			break
		}
		if len(calls) == 0 {
			break
		}
		if step >= maxToolSteps {
			s.out.line(ansiRed, fmt.Sprintf("stopping: the model has asked for tools %d times in one turn", step))
			s.decline(calls, "not run: the turn reached its limit of tool calls")
			break
		}
		if stop := s.runCalls(turnCtx, calls); stop {
			break
		}
	}
	// Whatever happened, the pane is no longer working: an interrupted turn and
	// a finished one both leave the user at the prompt.
	s.reporter.stop()
}

// sayWhyItStopped draws why a turn ended without an answer, and what the user
// can do about it: each failure has its own way on, and the one line that
// names it is the only place the user learns which.
func (s *session) sayWhyItStopped(err error) {
	switch {
	case s.interrupted.Load() || errors.Is(err, context.Canceled):
		s.out.line(ansiDim, "(interrupted; /retry carries on)")
	case refusedKey(err):
		agent := firstNonEmpty(s.opts.Agent, "<agent>")
		s.out.line(ansiRed, "the API refused the key: "+err.Error())
		s.out.line(ansiDim, "set another with `flockdeck keys set "+agent+"` in any terminal, then /retry")
	case forbidden(err):
		s.out.line(ansiRed, "the API would not do this with this key: "+err.Error())
		s.out.line(ansiDim, "(the key was accepted, but the account behind it may not have this model; /model switches to another)")
	case contextFull(err):
		s.out.line(ansiRed, "the model could not answer: "+err.Error())
		s.out.line(ansiDim, "(the conversation is longer than the model can read; /clear starts it over, and /history still shows what was said)")
	default:
		s.out.line(ansiRed, "the model could not answer: "+err.Error())
		s.out.line(ansiDim, "(/retry asks again)")
	}
}

// stream asks for one answer and draws it as it arrives, returning the tools the
// model asked for.
func (s *session) stream(ctx context.Context) ([]ToolCall, error) {
	req := Request{
		Model:     s.model,
		System:    s.system,
		Messages:  s.messages,
		Tools:     s.opts.Tools,
		MaxTokens: s.opts.MaxTokens,
	}

	if s.autoWidth {
		s.opts.Width = resolveWidth()
		s.out.width = s.opts.Width
	}
	var said strings.Builder
	var calls []ToolCall
	var thoughts []Thinking
	var usage Usage
	thinking := false
	s.out.blankLine()
	err := s.wire.Stream(ctx, req, func(ev Event) {
		switch ev.Kind {
		case EventText:
			if thinking {
				s.out.endMessage()
				s.out.setDim(false)
				thinking = false
			}
			said.WriteString(ev.Text)
			s.out.text(ev.Text)
		case EventThinking:
			if !thinking {
				s.out.line(ansiDim, "thinking")
				s.out.setDim(true)
				thinking = true
			}
			s.out.text(ev.Text)
		case EventCall:
			calls = append(calls, ev.Call)
		case EventReasoning:
			thoughts = append(thoughts, ev.Thinking)
		case EventNotice:
			s.out.line(ansiDim, "("+ev.Text+")")
		case EventUsage:
			usage = ev.Usage
		}
	})
	if thinking {
		s.out.setDim(false)
	}
	s.out.endMessage()
	s.total.Add(usage)
	s.spent.add(s.model, usage)

	text := strings.TrimSpace(said.String())
	if text != "" || len(calls) > 0 {
		s.messages = append(s.messages, Message{Role: RoleAssistant, Text: text, Calls: calls, Thinking: thoughts})
	}
	if text != "" {
		s.record(Entry{
			Type: string(RoleAssistant), Text: text,
			Model: s.model, In: usage.In, Out: usage.Out,
		})
	}
	for _, c := range calls {
		s.out.line(ansiBlue, "· "+describeCall(c, s.opts.Width))
	}
	return calls, err
}

// record writes one entry to the transcript, saying so if it cannot: the
// transcript is what resume, the history overlay and a fan-out read, so losing
// it silently would leave three things quietly not working.
func (s *session) record(e Entry) {
	if err := s.log.Append(e); err != nil {
		s.out.line(ansiRed, "could not write the transcript: "+err.Error())
	}
}

// compose puts the pane's briefing after the system prompt, fenced so the model
// can tell the two apart.
func compose(prompt, brief string) string {
	if strings.TrimSpace(brief) == "" {
		return prompt
	}
	return prompt + "\n\n<flockdeck-context>\n" + strings.TrimSpace(brief) + "\n</flockdeck-context>"
}
