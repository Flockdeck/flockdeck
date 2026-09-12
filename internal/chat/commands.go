package chat

import (
	"fmt"
	"strconv"
	"strings"
)

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
			"/history [n]  the conversation so far, or its last n entries",
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
		s.history(rest)
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
