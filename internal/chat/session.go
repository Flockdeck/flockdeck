package chat

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
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
	// DefaultModel is the one the agent's catalog entry falls back to when
	// nobody picks one. A resumed conversation that had gone on with another,
	// chosen with /model, is taken up with that one rather than put back on
	// the default.
	DefaultModel string
	// Models are what the agent's catalog entry offers, which /model lists and
	// picks from by number. Any other id can still be named.
	Models  []ModelChoice
	Session string
	Resume  bool
	// Task is the opening prompt, which arrives in the argv rather than being
	// typed, because typing into a program means guessing when it is ready.
	Task string

	// Wire, BaseURL and KeyEnv are the agent's APISpec, passed in rather than
	// looked up here: the chat client has no catalog of its own. A flag or a
	// variable can say them; otherwise `flockdeck chat` reads them from the
	// agent's catalog entry before it gets here.
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
	// waitForKey waits for a key to be stored where there is none, as the
	// chat does when a person is at the terminal. It is how that is tested
	// without one.
	waitForKey bool
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
	var key, keyFrom string
	if wire == nil {
		var err error
		key, err = resolveKey(o)
		if err != nil {
			// Somebody at a terminal is told how to set a key and the chat
			// waits for one, rather than ending: a pane that has ended does
			// not pick a key up later, and has to be restarted after it,
			// which is the step people miss. A script is told at once.
			if !o.waitForKey && !isConsole(o.In) {
				return err
			}
			if key, err = waitForKey(ctx, o); err != nil {
				return err
			}
		}
		_, keyFrom = lookupKey(o)
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
		keyFrom:   keyFrom,
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
	s.reporter.unpriced = o.BaseURL != ""

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
	// told from a new one set since; refused are the keys the API has
	// refused in this session, which are not tried again. keyFrom is where
	// the key came from, in the words /status says it in.
	key      string
	refused  map[string]bool
	keyFrom  string
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
	s.system = s.systemWith(s.reporter.sessionStart(source))

	// Short enough for a pane of the width a dozen side by side leave, where
	// the longer "/help for what it can do" broke to leave "do" on a line of
	// its own.
	s.out.line(ansiDim, fmt.Sprintf("flockdeck chat · %s · %s · /help for commands",
		firstNonEmpty(s.opts.Agent, s.wire.Name()), firstNonEmpty(s.model, "the endpoint's own model")))

	if s.opts.Resume {
		s.replay()
	}
	if s.model == "" {
		// Most endpoints want a model named, a local model server above all,
		// and the first sign of it would otherwise be the first answer
		// failing with "model is required".
		s.pickOnlyModel(ctx)
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
		if isCommand(prompt) {
			if leave := s.command(ctx, prompt); leave {
				s.reporter.sessionEnd()
				return nil
			}
			continue
		}
		s.turn(ctx, prompt)
	}
}

// isCommand reports whether a line is a slash command rather than a prompt
// that happens to begin with a slash. A path is the usual one -- "/usr/lib/
// libssl.so is missing", "/c/Users/me/app.log says ..." -- and taken for a
// command, it was answered "no such command" and the question was gone, with
// no way to send it: a leading space is trimmed off like any other.
func isCommand(line string) bool {
	if !strings.HasPrefix(line, "/") {
		return false
	}
	name := line[1:]
	if i := strings.IndexAny(name, " \t\r\n"); i >= 0 {
		name = name[:i]
	}
	return !strings.ContainsAny(name, `/\.:`)
}

// readPrompt draws the status line and waits for what the user types.
//
// A line ending in a backslash is continued on the next one, which is how a
// prompt with a paragraph in it is typed without raw mode.
func (s *session) readPrompt(ctx context.Context) (string, bool) {
	// Lines typed while the model was answering were echoed by the terminal
	// in the middle of the answer, and were sent from a prompt that showed
	// nothing after it. They are taken now and drawn after the prompt, as the
	// lines held back from a tool's question are.
	s.ahead = append(s.ahead, s.in.pending()...)
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
			// now answers, since it went by in the middle of the answer. All
			// of it is one prompt, as the same lines arriving at the prompt
			// are: several lines typed ahead are a paste far more often than
			// they are several questions, and each sent alone is a turn paid
			// for.
			line, s.ahead = strings.Join(s.ahead, "\n"), nil
			s.out.line("", line)
			if n := strings.Count(line, "\n") + 1; n > 1 {
				s.out.line(ansiDim, fmt.Sprintf("(%d lines typed while the model worked, sent as one prompt)", n))
			}
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

	rekeyed, readdressed, retries := false, false, 0
	for step := 0; ; step++ {
		before := len(s.messages)
		calls, err := s.stream(turnCtx)
		e, isBusy := busy(err)
		// An API that asks to be left for longer than a minute is not asked
		// again on its own: asked sooner, it refuses again, and a pane waiting
		// out ten minutes nobody is told of looks hung. It is reported, and
		// /retry is for whoever comes back to it.
		longWait := isBusy && e.RetryAfter > time.Minute
		if (isBusy || dropped(err)) && !longWait && retries < len(busyBackoff) && len(s.messages) == before {
			// A connection that dropped before a word of the answer came is as
			// passing as a busy API, and asking again as safe: nothing was
			// drawn to be drawn twice.
			// Nothing of an answer arrived, so asking again repeats nothing;
			// and a busy API is the one failure that asking again fixes. An
			// answer the API gave up on part-way is not asked for again: what
			// was drawn of it would be drawn twice.
			wait := busyBackoff[retries]
			why := "the connection dropped before any of the answer came"
			if isBusy {
				why = busyWords(e)
				if e.RetryAfter > 0 {
					wait = e.RetryAfter
				}
			}
			retries++
			s.out.line(ansiDim, fmt.Sprintf("(%s; trying again in %s)", why, wait.Round(time.Second)))
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
		if err != nil && unreachable(err) && !readdressed && s.readdress() {
			// The address was changed since the pane started, which is what
			// the pane itself advises when one cannot be reached.
			readdressed = true
			s.out.line(ansiDim, "(the address is now "+endpointOf(s.opts)+"; asking there)")
			continue
		}
		if err != nil {
			s.sayWhyItStopped(err)
			s.unfinished = true
			break
		}
		if len(calls) == 0 {
			if len(s.messages) == before {
				// Nothing came back but the end of the answer -- a model that
				// only thought, or a local one that stopped at once -- and the
				// pane went back to the prompt with nothing on the screen to
				// say whether it had been answered at all.
				s.out.line(ansiDim, "(the model answered with nothing; /retry asks again)")
				s.unfinished = true
			}
			break
		}
		if step >= maxToolSteps {
			s.out.line(ansiRed, fmt.Sprintf("stopping: the model has asked for tools %d times in one turn", step))
			s.decline(calls, "not run: the turn reached its limit of tool calls")
			// A long piece of work reaches the limit honestly -- twenty files
			// edited and the tests run -- and whoever is watching decides
			// whether it goes on, with one word rather than a prompt that
			// explains where it had got to.
			s.out.line(ansiDim, "(/retry lets it carry on for as many again)")
			s.unfinished = true
			break
		}
		if stop := s.runCalls(turnCtx, calls); stop {
			if s.interrupted.Load() {
				// Ctrl+C at a tool's question, or while a tool ran: the turn
				// stopped as surely as one interrupted mid-answer, and is
				// carried on the same way, from the calls' answers.
				s.out.line(ansiDim, "(interrupted; /retry carries on)")
				s.unfinished = true
			}
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
	be, isBusy := busy(err)
	switch {
	case s.interrupted.Load() || errors.Is(err, context.Canceled):
		s.out.line(ansiDim, "(interrupted; /retry carries on)")
	case refusedKey(err):
		agent := firstNonEmpty(s.opts.Agent, "<agent>")
		s.out.line(ansiRed, "the API refused the key: "+err.Error())
		s.out.line(ansiDim, "set another with `flockdeck keys set "+agent+"` in any terminal, then /retry")
	case outOfCredit(err):
		// Not asked again, and not called busy: what fixes it is money or
		// another key, and neither is in the pane.
		s.out.line(ansiRed, "the account behind this key is out of credit: "+err.Error())
		s.out.line(ansiDim, "(add credit with the vendor, or set another key with `flockdeck keys set "+keyAgent(s.opts)+"`, then /retry)")
	case modelUnknown(err):
		s.out.line(ansiRed, "the model could not answer: "+err.Error())
		if names := s.localModels(); len(names) > 0 {
			// The names are what somebody reading this needs next, and a
			// server on this machine can say them at once.
			s.out.line(ansiDim, "(the endpoint has no model called "+firstNonEmpty(s.model, "that")+"; it has "+
				strings.Join(names, ", ")+" -- /model <part of a name> switches)")
		} else {
			s.out.line(ansiDim, "(the endpoint has no model called "+firstNonEmpty(s.model, "that")+"; /model shows the ones to choose from)")
		}
	case forbidden(err):
		s.out.line(ansiRed, "the API would not do this with this key: "+err.Error())
		s.out.line(ansiDim, "(the key was accepted, but the account behind it may not have this model; /model switches to another)")
	case cutOff(err):
		// What was written is the start of the answer and is kept; asking
		// again throws it away and is cut off at the same length.
		s.out.line(ansiRed, err.Error())
		s.out.line(ansiDim, "(say \"go on\" for the rest; /retry would start the answer again from the beginning)")
	case refused(err):
		// /retry asks the same again, which is refused the same way: the way
		// on is to ask differently.
		s.out.line(ansiRed, err.Error())
		s.out.line(ansiDim, "(asking the same again is refused the same way; put it differently, or /clear to start over)")
	case proxyUnreached(err) != "":
		// Before unreachable: the endpoint was never asked, and changing its
		// address, which is what that case advises, changes nothing.
		env := "HTTPS_PROXY"
		if strings.HasPrefix(strings.ToLower(endpointOf(s.opts)), "http://") {
			env = "HTTP_PROXY"
		}
		s.out.line(ansiRed, "could not reach the proxy at "+proxyUnreached(err)+" ("+env+"); is it running?")
		s.out.line(ansiDim, "(/retry asks again)")
	case untrustedCert(err):
		s.out.line(ansiRed, "the endpoint's certificate is not trusted on this machine")
		s.out.line(ansiDim, "(a proxy or antivirus inspecting HTTPS is the usual cause; its certificate needs trusting, or the endpoint leaving out of its inspection)")
	case unreachable(err):
		s.out.line(ansiRed, "could not reach the endpoint: "+err.Error())
		change := "`flockdeck keys endpoint " + keyAgent(s.opts) + " <url>` changes the address"
		if isLoopback(s.opts.BaseURL) {
			// A server on this machine that is not answering is almost always
			// one that has not been started.
			s.out.line(ansiDim, "(nothing is answering at "+s.opts.BaseURL+"; is the model server running? "+change+"; /retry asks again)")
		} else {
			s.out.line(ansiDim, "(check the connection, or the address: "+change+"; /retry asks again)")
		}
	case dropped(err):
		// The socket's own words -- "wsarecv: An existing connection was
		// forcibly closed by the remote host" -- say nothing a person can
		// act on; what happened is one line, and the way on is to ask again.
		s.out.line(ansiRed, "the connection to the endpoint dropped part-way through the answer")
		s.out.line(ansiDim, "(/retry asks again)")
	case contextFull(err):
		s.out.line(ansiRed, "the model could not answer: "+err.Error())
		s.out.line(ansiDim, "(the conversation is longer than the model can read; /clear starts it over, and /history still shows what was said)")
	case isBusy:
		// Said in the words the retries are announced in, rather than as
		// "overloaded_error: Overloaded" at the one moment it matters. It
		// does not say whether it was asked again: an answer that failed
		// part-way is not, and one that failed at once was.
		s.out.line(ansiRed, busyWords(be)+": "+redactKeys(be.Msg))
		if be.RetryAfter > time.Minute {
			s.out.line(ansiDim, "(the API asked to be left for "+be.RetryAfter.Round(time.Second).String()+"; /retry asks again after that)")
		} else {
			s.out.line(ansiDim, "(/retry asks again, once it is less busy)")
		}
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
	if s.opts.BaseURL == "" {
		s.spent.add(s.model, usage)
	} else if usage != (Usage{}) {
		// Through an address of its own the vendor's list price is not what
		// is paid, and a figure from it would be a guess shown as a cost.
		s.spent.unpriced = true
	}
	// The pane header shows what the conversation has cost, as the line
	// under it does; this is what it is told.
	s.reporter.usage(s.model, usage)

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

// systemWith is the system prompt with where the chat is working said in it,
// and the pane's briefing after.
//
// The directory and the system are said because everything the model does
// with the tools depends on them, and a chat run by hand has no briefing to
// say either: a model not told it is on Windows reaches for ls and /tmp.
func (s *session) systemWith(brief string) string {
	where := fmt.Sprintf("%s\n\nYou are working in %s, on %s.", systemPrompt, s.opts.Cwd, osName())
	return compose(where, brief)
}

// osName is the operating system as the model is told it.
func osName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	}
	return runtime.GOOS
}

// compose puts the pane's briefing after the system prompt, fenced so the model
// can tell the two apart.
func compose(prompt, brief string) string {
	if strings.TrimSpace(brief) == "" {
		return prompt
	}
	return prompt + "\n\n<flockdeck-context>\n" + strings.TrimSpace(brief) + "\n</flockdeck-context>"
}
