package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

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
		if c.BadArgs != "" {
			s.out.line(ansiRed, "  not run: the model's arguments were not valid JSON")
			s.answer(c, "not run: the arguments were not valid JSON, so the call was not made. "+
				"They were: "+clipTo(c.BadArgs, 400)+"\nSend them again as one JSON object.")
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
			if instead == stopTurn {
				// Typed at the question, the turn ends here as Ctrl+C would end
				// it, rather than "stop" going to the model as what to do
				// instead while it carried on working.
				s.out.line(ansiDim, "  (stopped; /retry carries on)")
				s.answer(c, "the user stopped the turn here, so this was not run")
				s.decline(calls[i+1:], "not run: the user stopped the turn")
				s.unfinished = true
				return true
			}
			// A no is said back, a reply in words above all: "yes please" is
			// not one of the letters, and somebody who typed it would otherwise
			// go on believing the call ran.
			if !ok {
				// The question turned the pane amber, and the model is working
				// again from here; without an event to say so the pane stayed
				// waiting until the turn ended. The call is over, run or not.
				s.reporter.postTool(c.Name)
			}
			if !ok && instead != "" {
				// What the user said instead is the next thing the model has
				// to hear, and the calls it planned before hearing it are moot.
				s.out.line(ansiDim, "  not run; what you typed goes to the model instead")
				s.answer(c, "the user declined this, and said: "+instead)
				s.decline(calls[i+1:], "not run: the user asked for something else first")
				return false
			}
			if !ok {
				s.out.line(ansiDim, "  not run")
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
	s.record(Entry{Type: string(RoleTool), Tool: c.Name, Call: describeCall(c, 200), Text: text})
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
			return key, s.always[key]
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
	s.ahead = append(s.ahead, s.in.pending()...)
	s.reporter.notification(t.Name())
	always, canAlways := "", false
	if a, is := t.(AlwaysApprover); is {
		if key := a.AlwaysKey(c.Args); key != "" {
			always, canAlways = key, true
		}
	}
	s.out.blankLine()
	s.out.line(ansiBold, question)
	answers := []string{"[y] yes", "[n] no (Enter)", "or type what to do instead"}
	if canAlways {
		answers = []string{"[y] yes, once", "[a] always for `" + always + "`", "[n] no (Enter)", "or type what to do instead"}
	}
	// One line where it fits. In a narrow pane -- the usual thing with
	// several side by side -- the terminal would break that line mid-word,
	// in the one place the user is reading to choose, so each answer gets a
	// line of its own instead.
	if all := "  " + strings.Join(answers, "   "); utf8.RuneCountInString(all) <= s.opts.Width {
		s.out.line(ansiDim, all)
	} else {
		for _, a := range answers {
			s.out.line(ansiDim, "  "+a)
		}
	}
	s.out.bare(ansiBold, "  > ")
	select {
	case <-ctx.Done():
		return false, false, ""
	case <-s.in.closed:
		return false, false, ""
	case line := <-s.in.lines:
		reply := strings.TrimSpace(line)
		switch strings.ToLower(strings.TrimRight(reply, ".!")) {
		case "y", "yes", "ok", "okay", "sure", "yep", "yeah":
			// The single words nobody types to mean no. Taken as words for
			// the model, "ok" declined the call, and the model asked the same
			// question again; a longer reply -- "ok, but with -v" -- is still
			// what to do instead.
			return true, true, ""
		case "a", "always":
			if canAlways {
				s.always[always] = true
			}
			// Where there is no "always" to give, the one call in front of
			// them is the least of what was agreed to.
			return true, true, ""
		case "n", "no", "":
			return false, true, ""
		case "stop", "cancel", "abort":
			return false, true, stopTurn
		}
		return false, true, reply
	}
}

// stopTurn is what ask reports for a reply that ends the whole turn -- "stop",
// "cancel" -- in place of what to do instead. Nothing typed can be it.
const stopTurn = "\x00stop"

// describeCall is the one line a tool call is drawn as: its name and what it
// acts on -- the command, the path, the pattern and where it is looked for --
// which is what somebody watching needs to recognise it by. The arguments as
// JSON said the same less readably, and for a write they were the file itself,
// escaped onto one line. Arguments of any other shape are still shown as JSON.
func describeCall(c ToolCall, width int) string {
	var args map[string]any
	_ = json.Unmarshal(c.Args, &args)
	str := func(key string) string {
		v, _ := args[key].(string)
		return strings.Join(strings.Fields(v), " ")
	}
	var what string
	switch {
	case str("command") != "":
		what = str("command")
	case str("pattern") != "":
		what = strconv.Quote(str("pattern"))
		if where := str("path"); where != "" {
			what += " in " + where
		}
	case str("path") != "":
		what = str("path")
	default:
		what = strings.Join(strings.Fields(string(c.Args)), " ")
		if what == "{}" {
			what = ""
		}
	}
	return clipTo(c.Name+" "+what, width-4)
}

// summarise is what a tool's output is drawn as. The output itself goes to the
// model; what the user needs is enough to see that the right thing happened.
func summarise(out string, width int) string {
	lines := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1
	lead := clipTo(leadLine(out), width-12)
	if lines > 1 {
		return fmt.Sprintf("%s (%d lines; /output shows them)", lead, lines)
	}
	if lead == "" {
		return "(no output)"
	}
	return lead
}

// leadLine is the one line a tool's output is known by: its result, which the
// tools put last, where it has one, and otherwise its first line. It is the
// same line whether the output is drawn as the call runs or read back later
// from the history.
func leadLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); len(lines) > 1 && isResultLine(last) {
		return last
	}
	return strings.TrimSpace(lines[0])
}

// isResultLine reports whether the last line of a tool's output is its result,
// which the tools put last: a command's exit status, or how much a search
// found. The first line of such an output -- what a command printed first, a
// search's first match -- says nothing of how it went.
func isResultLine(s string) bool {
	return strings.HasPrefix(s, "[exit status") || strings.HasPrefix(s, "[killed after") ||
		strings.HasPrefix(s, "[stopped at") || searchTally.MatchString(s)
}

// searchTally is the line grep ends with: "2 matches in 2 files.", or "1
// match in 1 file.".
var searchTally = regexp.MustCompile(`^\d+ match(es)? in \d+ files?\.$`)

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
