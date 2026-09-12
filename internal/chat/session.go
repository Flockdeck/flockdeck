package chat

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
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

// KeyStore is asked for an API key when the environment does not have one. It
// is a variable so that the credential store can supply it without the chat
// client depending on it, and so that a key reaches exactly one process: this
// one.
var KeyStore func(agent string) string

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
	// rest of the session.
	always map[string]bool
	// interrupted is set from the goroutine watching for interrupts and read
	// back once the turn has ended, so nothing is drawn from two goroutines.
	interrupted atomic.Bool
	// autoWidth is set when the width is the terminal's rather than given.
	autoWidth bool
	// ahead are lines the user typed while the model was working, kept for
	// the prompt they were meant for.
	ahead []string
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
			if leave := s.command(prompt); leave {
				s.reporter.sessionEnd()
				return nil
			}
			continue
		}
		s.turn(ctx, prompt)
	}
}

// replay puts a resumed conversation back, on the screen and in the request.
//
// The whole conversation goes into the request, because that is what resuming
// means; only the tail is drawn, because a pane restored with a thousand lines
// of scrollback in front of the prompt is a pane nobody can see the prompt in,
// and /history is there for the rest.
func (s *session) replay() {
	// The log's own file rather than a lookup, so that what is replayed is
	// exactly what is being appended to.
	entries, err := ReadEntries(s.log.Path())
	if err != nil {
		s.out.line(ansiRed, "could not read the transcript: "+err.Error())
		return
	}
	s.messages = Messages(entries)
	if len(s.messages) == 0 {
		s.out.line(ansiDim, "(nothing recorded for this conversation yet)")
		return
	}
	shown := sinceClear(entries)
	const tail = 12
	if len(shown) > tail {
		s.out.line(ansiDim, fmt.Sprintf("(%d earlier entries; /history shows the whole conversation)", len(shown)-tail))
		shown = shown[len(shown)-tail:]
	}
	s.drawEntries(shown)
	s.out.blankLine()
}

// history is /history: the conversation since it was last started over, drawn
// the way it looked as it happened, for a pane whose scrollback does not reach
// back that far -- one that was resumed, or has been going all day.
func (s *session) history() {
	entries, err := ReadEntries(s.log.Path())
	if err != nil {
		s.out.line(ansiRed, "could not read the transcript: "+err.Error())
		return
	}
	entries = sinceClear(entries)
	if len(entries) == 0 {
		s.out.line(ansiDim, "(nothing has been said yet)")
		return
	}
	s.drawEntries(entries)
}

// drawEntries draws stored entries the way they looked when they were new: the
// user's prompts dimmed under "you", the answers as answers, and each tool's
// output as the one line it was drawn as then, not the whole of it.
func (s *session) drawEntries(entries []Entry) {
	for _, e := range entries {
		switch e.Type {
		case string(RoleUser):
			s.out.blankLine()
			s.out.line(ansiDim, "you")
			s.out.setDim(true)
			s.out.text(e.Text)
			s.out.endMessage()
			s.out.setDim(false)
		case string(RoleAssistant):
			s.out.blankLine()
			s.out.text(e.Text)
			s.out.endMessage()
		case string(RoleTool):
			summary := clipTo(firstLine(e.Text, 400), s.opts.Width-16)
			if n := strings.Count(strings.TrimRight(e.Text, "\n"), "\n") + 1; n > 1 {
				summary += fmt.Sprintf(" (%d lines)", n)
			}
			s.out.line(ansiDim, "  · "+firstNonEmpty(e.Tool, "tool")+"  "+summary)
		}
	}
}

// sinceClear is the entries after the last /clear: the conversation the model
// is still being shown.
func sinceClear(entries []Entry) []Entry {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == entryClear {
			return entries[i+1:]
		}
	}
	return entries
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
			}
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
	s.reporter.userPrompt(prompt)
	s.messages = append(s.messages, Message{Role: RoleUser, Text: prompt})
	s.record(Entry{Type: string(RoleUser), Text: prompt})

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
		calls, err := s.stream(turnCtx)
		if e, ok := busy(err); ok && retries < len(busyBackoff) {
			// Nothing of an answer arrived, so asking again repeats nothing;
			// and a busy API is the one failure that asking again fixes.
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
			switch {
			case s.interrupted.Load() || errors.Is(err, context.Canceled):
				s.out.line(ansiDim, "(interrupted)")
			case refusedKey(err):
				agent := firstNonEmpty(s.opts.Agent, "<agent>")
				s.out.line(ansiRed, "the API refused the key: "+err.Error())
				s.out.line(ansiDim, "set another with `flockdeck keys set "+agent+"` in any terminal, then send the prompt again")
			default:
				s.out.line(ansiRed, "the model could not answer: "+err.Error())
			}
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

// runCalls runs the tools the model asked for, in the order it asked. It
// returns true when the turn should stop rather than go back to the model.
func (s *session) runCalls(ctx context.Context, calls []ToolCall) bool {
	for i, c := range calls {
		if ctx.Err() != nil {
			// Ctrl+C stops the turn, and a tool that has not started yet is part
			// of the turn: running a write after the user asked for everything to
			// stop would be the opposite of what they asked.
			s.decline(calls[i:], "not run: the user interrupted the turn")
			return true
		}
		tool := s.tools[c.Name]
		if tool == nil {
			// Answering rather than failing is deliberate: a model that asked
			// for a tool it does not have can be told so and carry on, and
			// killing the turn over it teaches nobody anything.
			s.out.line(ansiRed, "  there is no tool called "+c.Name)
			s.answer(c, fmt.Sprintf("there is no tool called %q in this pane", c.Name))
			continue
		}
		question := tool.Approval(c.Args)
		if key, ok := s.approved(tool, c); question != "" && ok {
			// Not asked, and said so: a command running without a question
			// should say whose earlier yes it is running on.
			s.out.line(ansiDim, "  allowed for the rest of the session: `"+key+"`")
		} else if question != "" {
			ok, asked, instead := s.ask(ctx, tool, c, question)
			if !asked {
				// The user never answered: the turn was interrupted, or the
				// pane has gone.
				s.answer(c, "the user did not answer, so this was not run")
				s.decline(calls[i+1:], "not run: the user did not answer an earlier question")
				return true
			}
			if !ok && instead != "" {
				// What the user said instead is the next thing the model has
				// to hear, and the calls it planned before hearing it are moot.
				s.answer(c, "the user declined this, and said: "+instead)
				s.decline(calls[i+1:], "not run: the user asked for something else first")
				return false
			}
			if !ok {
				s.answer(c, "the user declined this")
				continue
			}
		}
		s.reporter.preTool(c.Name)
		out, err := tool.Run(ctx, c.Args)
		s.reporter.postTool(c.Name)
		if err != nil {
			// Drawn as well as answered: a refusal -- a path outside the
			// pane, an edit that does not match -- is otherwise invisible, and
			// the user watching the model try again cannot tell why.
			s.out.line(ansiRed, "  failed: "+summarise(err.Error(), s.opts.Width))
			s.answer(c, "the tool failed: "+err.Error())
			continue
		}
		s.out.line(ansiDim, "  "+summarise(out, s.opts.Width))
		s.answer(c, out)
	}
	return false
}

// answer records a tool's output and adds it to the conversation.
//
// Silence is turned into words because every one of the wires refuses an empty
// answer to a call, and a tool that legitimately has nothing to say -- a write
// that succeeded, a search that found nothing -- would otherwise take the turn
// down with it.
func (s *session) answer(c ToolCall, text string) {
	if strings.TrimSpace(text) == "" {
		text = "(the tool produced no output)"
	}
	s.messages = append(s.messages, Message{Role: RoleTool, Text: text, Call: c})
	s.record(Entry{Type: string(RoleTool), Tool: c.Name, Text: text})
}

// rekey looks for the key again and, where a different one is set now than the
// wire was built with, builds the wire again with it. It reports whether it did.
func (s *session) rekey() bool {
	if s.opts.wire != nil {
		return false
	}
	key, err := resolveKey(s.opts)
	if err != nil || key == s.key {
		return false
	}
	wire, err := NewWire(s.opts.Wire, s.opts.BaseURL, key)
	if err != nil {
		return false
	}
	s.wire, s.key = wire, key
	return true
}

// decline answers calls that will not be run.
//
// Every call the model makes has to be answered before the conversation can go
// on: all three wires refuse a request in which a call has no answer, so one
// left hanging would fail not just this turn but every turn after it.
func (s *session) decline(calls []ToolCall, why string) {
	for _, c := range calls {
		s.answer(c, why)
	}
}

// approved reports whether the user has already agreed to this family of
// calls, and names the family.
func (s *session) approved(t Tool, c ToolCall) (string, bool) {
	if a, ok := t.(AlwaysApprover); ok {
		if key := a.AlwaysKey(c.Args); key != "" {
			return key, s.always[t.Name()+"\x00"+key]
		}
	}
	return "", false
}

// ask puts a tool's question to the user, and returns whether they agreed,
// whether they answered at all, and anything they said instead of agreeing.
//
// The question is also a Notification event, which is what turns the pane amber
// and tells the user which pane is waiting for them -- the whole reason for
// running agents side by side rather than one at a time.
//
// It is answered in one line and never asked twice. Anything but a yes is a
// no, which is the safe way to misread somebody, and a reply that is not one
// of the letters is taken as what they would rather the model did: the one
// thing a person saying no usually has to add.
func (s *session) ask(ctx context.Context, t Tool, c ToolCall, question string) (ok, asked bool, instead string) {
	// A line already waiting was typed before the question was on the screen
	// -- the next prompt, typed while the model worked -- and taken as the
	// answer it would decline the call with the prompt as the reason. It is
	// kept for the prompt instead.
	s.ahead = append(s.ahead, s.in.typedAhead()...)
	s.reporter.notification(t.Name())
	always, canAlways := "", false
	if a, is := t.(AlwaysApprover); is {
		if key := a.AlwaysKey(c.Args); key != "" {
			always, canAlways = key, true
		}
	}
	s.out.blankLine()
	s.out.line(ansiBold, question)
	if canAlways {
		s.out.line(ansiDim, "  [y] yes, once   [a] always for `"+always+"`   [n] no (Enter)   or type what to do instead")
	} else {
		s.out.line(ansiDim, "  [y] yes   [n] no (Enter)   or type what to do instead")
	}
	s.out.bare(ansiBold, "  > ")
	select {
	case <-ctx.Done():
		return false, false, ""
	case <-s.in.closed:
		return false, false, ""
	case line := <-s.in.lines:
		reply := strings.TrimSpace(line)
		switch strings.ToLower(reply) {
		case "y", "yes":
			return true, true, ""
		case "a", "always":
			if canAlways {
				s.always[t.Name()+"\x00"+always] = true
			}
			// Where there is no "always" to give, the one call in front of
			// them is the least of what was agreed to.
			return true, true, ""
		case "n", "no", "":
			return false, true, ""
		}
		return false, true, reply
	}
}

// record writes one entry to the transcript, saying so if it cannot: the
// transcript is what resume, the history overlay and a fan-out read, so losing
// it silently would leave three things quietly not working.
func (s *session) record(e Entry) {
	if err := s.log.Append(e); err != nil {
		s.out.line(ansiRed, "could not write the transcript: "+err.Error())
	}
}

// command runs a slash command, and returns true when the client should leave.
func (s *session) command(line string) bool {
	name, rest, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	rest = strings.TrimSpace(rest)
	switch strings.ToLower(name) {
	case "exit", "quit":
		return true
	case "help":
		s.out.line(ansiBold, "commands")
		for _, l := range []string{
			"/model        the models to choose from; /model 2 or /model <id> switches",
			"/output [n]   all of the last tool's output, or of the nth last",
			"/history      the whole conversation so far, for when scrollback ends",
			"/clear        start the conversation over, keeping the pane",
			"/status       what has been spent, and where the transcript is",
			"/exit         leave; the pane's own conversation ends with it",
			"",
			"A line ending in a backslash is continued on the next one.",
			"Ctrl+C stops the answer being written, not the client.",
		} {
			s.out.line(ansiDim, "  "+l)
		}
	case "model":
		s.chooseModel(rest)
	case "output":
		s.showOutput(rest)
	case "history":
		s.history()
	case "clear":
		s.messages = nil
		// The transcript is marked rather than truncated: what was said was
		// said, and a reader that does not know this entry still shows a
		// conversation that really happened.
		s.record(Entry{Type: entryClear})
		s.reporter.sessionEnd()
		s.system = compose(systemPrompt, s.reporter.sessionStart("clear"))
		s.out.line(ansiDim, "(cleared)")
	case "status":
		s.out.line(ansiDim, statusLine(s.model, s.total, s.spent))
		s.out.line(ansiDim, "session "+s.opts.Session)
		s.out.line(ansiDim, "transcript "+s.log.Path())
	default:
		s.out.line(ansiDim, "no such command: /"+name+" — try /help")
	}
	return false
}

// chooseModel is /model. With nothing after it, it lists the models there are
// to choose from and which one is answering, because switching is otherwise a
// matter of already knowing the exact id; with a number, it takes the model at
// that place in the list; with anything else, it takes that as the id.
func (s *session) chooseModel(arg string) {
	if arg == "" {
		s.out.line(ansiDim, "answering with "+firstNonEmpty(s.model, "whatever the endpoint is set to"))
		if len(s.opts.Models) == 0 {
			s.out.line(ansiDim, "switch with /model <id>")
			return
		}
		for i, m := range s.opts.Models {
			mark := " "
			if m.ID == s.model {
				mark = "*"
			}
			label := m.ID
			if m.Name != "" && m.Name != m.ID {
				label += "  " + m.Name
			}
			if m.Note != "" {
				label += " — " + m.Note
			}
			s.out.line(ansiDim, fmt.Sprintf("  %s %d  %s", mark, i+1, label))
		}
		s.out.line(ansiDim, "switch with /model <number>, or /model <id> for any other")
		return
	}
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(s.opts.Models) {
			s.out.line(ansiRed, fmt.Sprintf("there is no model %d in the list; /model shows it", n))
			return
		}
		arg = s.opts.Models[n-1].ID
	}
	s.model = arg
	s.out.line(ansiDim, "answering with "+arg+" from here on")
}

// showOutput is /output: the whole of what a tool returned, of which the pane
// showed one line as the call ran. The one line is right while the work goes
// by; when something went wrong, the rest is what the user scrolls back for,
// and it was never on the screen to scroll back to.
func (s *session) showOutput(arg string) {
	n := 1
	if arg != "" {
		v, err := strconv.Atoi(arg)
		if err != nil || v < 1 {
			s.out.line(ansiDim, "/output takes a number: 1 is the last tool's output, 2 the one before")
			return
		}
		n = v
	}
	seen := 0
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if m.Role != RoleTool {
			continue
		}
		if seen++; seen == n {
			// As it came from the tool, not reflowed: it is code and logs.
			s.out.line(ansiBlue, "· "+describeCall(m.Call, s.opts.Width))
			s.out.line("", strings.TrimRight(m.Text, "\n"))
			return
		}
	}
	if seen == 0 {
		s.out.line(ansiDim, "no tool has run in this conversation yet")
		return
	}
	s.out.line(ansiDim, fmt.Sprintf("there have been %d tool outputs so far", seen))
}

// compose puts the pane's briefing after the system prompt, fenced so the model
// can tell the two apart.
func compose(prompt, brief string) string {
	if strings.TrimSpace(brief) == "" {
		return prompt
	}
	return prompt + "\n\n<flockdeck-context>\n" + strings.TrimSpace(brief) + "\n</flockdeck-context>"
}

// describeCall is the one line a tool call is drawn as: its name and enough of
// its arguments to recognise it by.
func describeCall(c ToolCall, width int) string {
	args := strings.Join(strings.Fields(string(c.Args)), " ")
	if args == "{}" {
		args = ""
	}
	return clipTo(c.Name+" "+args, width-4)
}

// summarise is what a tool's output is drawn as. The output itself goes to the
// model; what the user needs is enough to see that the right thing happened.
func summarise(out string, width int) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	first := clipTo(strings.TrimSpace(lines[0]), width-12)
	if len(lines) > 1 {
		return fmt.Sprintf("%s (%d lines; /output shows them)", first, len(lines))
	}
	if first == "" {
		return "(no output)"
	}
	return first
}

// clipTo cuts s to n columns, on a rune boundary, marking that it was cut.
func clipTo(s string, n int) string {
	s = strings.TrimSpace(s)
	if n < 8 {
		n = 8
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// entryClear marks the point in a transcript where the conversation was started
// over. It is a fourth entry type, and a reader that only knows the three
// carries on: it sees the whole conversation, which is true, rather than the
// part of it the model was still being shown.
const entryClear = "clear"

// resolveKey finds the API key for this agent: the environment first, under the
// names the agent's spec gives and then the conventional ones, and the
// credential store after that.
//
// A key never appears in an error message, only the name of the place it was
// looked for, because an error is the one string in a program that gets pasted
// into a bug report.
func resolveKey(o Options) (string, error) {
	// The spec usually names the conventional variable itself, and an error
	// that tells somebody to set X or X is one they read twice.
	var names []string
	seen := map[string]bool{}
	for _, name := range append(append(append([]string{}, o.KeyEnv...), defaultKeyEnv(o.Wire)...), "FLOCKDECK_API_KEY") {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	if KeyStore != nil {
		if v := strings.TrimSpace(KeyStore(o.Agent)); v != "" {
			return v, nil
		}
	}
	// An endpoint on this machine is usually a local model, which wants no key
	// at all; refusing to start would be refusing over nothing.
	if isLoopback(o.BaseURL) {
		return "", nil
	}
	// The chat cannot start without one, so the error ends it -- and a pane
	// that has ended does not pick a key up later: it has to be started again,
	// which is the step people miss.
	then := "then run this again"
	if o.API != "" {
		then = "then restart this pane (Restart pane, in the command palette)"
	}
	return "", fmt.Errorf("no API key for %s: set %s, or run `flockdeck keys set %s`; %s",
		firstNonEmpty(o.Agent, o.Wire, "this agent"), strings.Join(names, " or "),
		firstNonEmpty(o.Agent, o.Wire, "<agent>"), then)
}

// defaultKeyEnv is the conventional variable for a wire, used when the agent's
// spec names none.
func defaultKeyEnv(wire string) []string {
	switch strings.ToLower(strings.TrimSpace(wire)) {
	case "openai", "openai-compatible":
		return []string{"OPENAI_API_KEY"}
	case "gemini", "google":
		return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
	default:
		return []string{"ANTHROPIC_API_KEY"}
	}
}

// isLoopback reports whether a base URL points at this machine.
func isLoopback(base string) bool {
	if base == "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
