package chat

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// command runs a slash command, and returns true when the client should leave.
func (s *session) command(ctx context.Context, line string) bool {
	name, rest, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	rest = strings.TrimSpace(rest)
	switch strings.ToLower(name) {
	case "exit", "quit":
		return true
	case "help":
		s.out.line(ansiBold, "commands")
		for _, l := range []string{
			"/model        the models to choose from; /model 2, /model opus or /model <id>",
			"/output [n]   all of the last tool's output, or of the nth last",
			"/history [n]  the conversation so far, or its last n entries",
			"/retry        carry on a turn that failed, was interrupted or hit its limit",
			"/clear        start the conversation over, keeping the pane",
			"/status       what has been spent, the endpoint, the key, the transcript",
			"/forget       withdraw every \"always\" given in this session",
			"/exit         leave; the pane's own conversation ends with it",
			"",
			"A line ending in a backslash is continued on the next one.",
			"Ctrl+C stops the answer being written, not the client.",
			"At a tool's question, y runs it, Enter declines it, stop ends the turn.",
		} {
			s.out.line(ansiDim, "  "+l)
		}
	case "model":
		s.chooseModel(ctx, rest)
	case "output":
		s.showOutput(rest)
	case "history":
		s.history(rest)
	case "retry":
		s.retry(ctx)
	case "clear":
		s.clear()
	case "status":
		s.status()
	case "forget":
		s.forget()
	default:
		if near := nearestCommand(name); near != "" {
			s.out.line(ansiDim, "no such command: /"+name+" — did you mean /"+near+"?")
		} else {
			s.out.line(ansiDim, "no such command: /"+name+" — try /help")
		}
	}
	return false
}

// clear is /clear: the conversation starts over, in the same pane.
func (s *session) clear() {
	s.messages = nil
	// The transcript is marked rather than truncated: what was said was
	// said, and a reader that does not know this entry still shows a
	// conversation that really happened.
	s.record(Entry{Type: entryClear})
	s.reporter.sessionEnd()
	s.system = s.systemWith(s.reporter.sessionStart("clear"))
	// Said in full, because "cleared" alone leaves somebody wondering
	// whether what was said is gone.
	s.out.line(ansiDim, "(cleared: the model starts afresh; the transcript keeps what was said)")
	// Standing permission belongs to the pane, not the conversation, and
	// outlives it; somebody starting a new task after /clear would otherwise
	// take it that they had started clean.
	if allowed := s.allowed(); allowed != "" {
		s.out.line(ansiDim, "(still allowed without asking: "+allowed+"; /forget withdraws them)")
	}
}

// allowed is what "always" has been answered to in this session, named the way
// the question named it, or "" for nothing.
func (s *session) allowed() string {
	var keys []string
	for key := range s.always {
		keys = append(keys, "`"+key+"`")
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}

// status is /status: what has been spent, the settings behind the pane, and
// where its conversation is kept.
func (s *session) status() {
	s.out.line(ansiDim, statusLine(s.model, s.total, s.spent))
	// The two settings behind a pane that is failing, and where each is
	// changed, since neither is anywhere else on the screen.
	if s.opts.wire == nil {
		s.out.line(ansiDim, "endpoint "+endpointOf(s.opts)+"; `flockdeck keys endpoint "+agentNamed(s.opts)+" <url>` changes it")
		if strings.HasPrefix(s.keyFrom, "stored with") {
			// The place is the command; naming it twice in one line reads
			// as two different things.
			s.out.line(ansiDim, "key "+s.keyFrom+"; running it again changes it")
		} else {
			s.out.line(ansiDim, "key "+firstNonEmpty(s.keyFrom, "none")+"; `flockdeck keys set "+agentNamed(s.opts)+"` changes it")
		}
	}
	// What "always" has been answered to is standing permission nothing else
	// on the screen shows, for as long as the pane runs.
	if allowed := s.allowed(); allowed != "" {
		s.out.line(ansiDim, "allowed without asking: "+allowed+"; /forget withdraws them")
	}
	s.out.line(ansiDim, "session "+s.opts.Session)
	s.out.line(ansiDim, "transcript "+s.log.Path())
}

// forget is /forget: every "always" given in this session is withdrawn, and
// each call is asked about again. An "a" pressed by mistake -- for every edit
// to every file -- otherwise stood until the pane was restarted.
func (s *session) forget() {
	if len(s.always) == 0 {
		s.out.line(ansiDim, "nothing has been allowed without asking")
		return
	}
	n := len(s.always)
	clear(s.always)
	what := "permissions"
	if n == 1 {
		what = "permission"
	}
	s.out.line(ansiDim, fmt.Sprintf("withdrew %d standing %s; every call is asked about again", n, what))
}

// retry is /retry: it carries on a turn that ended without an answer -- a
// dropped connection, an overloaded API, Ctrl+C -- from where it stopped,
// rather than the prompt being typed or pasted again and so asked twice. A
// turn cut short among its tool calls carries on from their answers.
func (s *session) retry(ctx context.Context) {
	last := len(s.messages) - 1
	if last < 0 || !s.unfinished {
		s.out.line(ansiDim, "there is nothing to retry: the last turn was answered")
		return
	}
	// What was drawn of an answer cut short -- the words before Ctrl+C --
	// goes: the model is asked again, not asked to go on from half a
	// sentence, which the current models refuse outright. The transcript
	// keeps it, as it keeps everything that was said.
	if m := s.messages[last]; m.Role == RoleAssistant && len(m.Calls) == 0 {
		s.messages = s.messages[:last]
	}
	prompt := ""
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role == RoleUser {
			prompt = s.messages[i].Text
			break
		}
	}
	s.carryOn(ctx, prompt)
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
	// The transcript rather than the conversation in memory: a resumed one
	// holds its tools' output as part of what the user said, and /output found
	// none of the outputs /history had just listed.
	entries, err := ReadEntries(s.log.Path())
	if err != nil {
		s.out.line(ansiRed, "could not read the transcript: "+err.Error())
		return
	}
	entries = sinceClear(entries)
	seen := 0
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type != string(RoleTool) {
			continue
		}
		if seen++; seen == n {
			// As it came from the tool, not reflowed: it is code and logs.
			s.out.line(ansiBlue, "· "+clipTo(firstNonEmpty(e.Call, e.Tool, "tool"), s.opts.Width-4))
			s.out.line("", strings.TrimRight(e.Text, "\n"))
			return
		}
	}
	if seen == 0 {
		s.out.line(ansiDim, "no tool has run in this conversation yet")
		return
	}
	if seen == 1 {
		s.out.line(ansiDim, "there has been 1 tool output so far")
		return
	}
	s.out.line(ansiDim, fmt.Sprintf("there have been %d tool outputs so far", seen))
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
	// An agent with no model named has the user choose one with /model, and
	// a pane restarted without it would have them choose again every time.
	// The model the conversation last answered with is recorded, and is taken
	// up again -- only where nothing names one, so that a model the pane was
	// started with still wins.
	//
	// A pane is started with the agent's default model when nobody picked
	// one, so "nothing names one" is also a pane that names only the
	// default: restarted, it put a conversation switched to another model
	// with /model back on the default. A model picked for the pane itself
	// is still the one used.
	if s.model == "" || s.model == s.opts.DefaultModel {
		for i := len(entries) - 1; i >= 0; i-- {
			if e := entries[i]; e.Type == string(RoleAssistant) && e.Model != "" {
				if e.Model != s.model {
					s.model = e.Model
					s.out.line(ansiDim, "(answering with "+e.Model+", as this conversation last did; /model changes it)")
				}
				break
			}
		}
	}
	// A pane closed while the model was answering -- the application
	// restarted, the pane closed mid-turn -- comes back with a prompt that
	// has no answer. It is said, and /retry asks it: it was the one thing
	// /retry would otherwise have called answered.
	if n := len(s.messages); n > 0 && s.messages[n-1].Role == RoleUser {
		s.unfinished = true
		s.out.line(ansiDim, "(the last prompt had no answer when the pane closed; /retry asks it again)")
	}
}

// history is /history: the conversation since it was last started over, drawn
// the way it looked as it happened, for a pane whose scrollback does not reach
// back that far -- one that was resumed, or has been going all day. With a
// number it draws only that many of the latest entries, because the whole of
// a day's conversation scrolls away the part somebody wanted to see.
func (s *session) history(arg string) {
	last := 0
	if arg != "" {
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 {
			s.out.line(ansiDim, "/history takes a number: /history 10 draws the last ten entries")
			return
		}
		last = n
	}
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
	if last > 0 && len(entries) > last {
		s.out.line(ansiDim, fmt.Sprintf("(%d earlier entries; /history alone draws them all)", len(entries)-last))
		entries = entries[len(entries)-last:]
	}
	s.drawEntries(entries)
}

// drawEntries draws stored entries the way they looked when they were new: the
// user's prompts dimmed under "you", the answers as answers, and each tool's
// output as the one line it was drawn as then, not the whole of it.
func (s *session) drawEntries(entries []Entry) {
	// Each tool's output is numbered the way /output counts, back from the
	// latest, so that the step somebody scrolled back to find can be opened
	// whole: listed without its number, it could be seen and not reached.
	// The entries drawn always run to the end of the conversation, which is
	// where /output counts from.
	left := 0
	for _, e := range entries {
		if e.Type == string(RoleTool) {
			left++
		}
	}
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
			number := left
			left--
			summary := clipTo(leadLine(e.Text), s.opts.Width-16)
			if n := strings.Count(strings.TrimRight(e.Text, "\n"), "\n") + 1; n > 1 {
				summary += fmt.Sprintf(" (%d lines; /output %d)", n, number)
			}
			// What the call acted on, where the entry records it; a
			// transcript written before it did names only the tool.
			what := clipTo(firstNonEmpty(e.Call, e.Tool, "tool"), s.opts.Width/2)
			s.out.line(ansiDim, "  · "+what+"  "+summary)
		}
	}
}

// entryClear marks the point in a transcript where the conversation was started
// over. It is a fourth entry type, and a reader that only knows the three
// carries on: it sees the whole conversation, which is true, rather than the
// part of it the model was still being shown.
const entryClear = "clear"

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
