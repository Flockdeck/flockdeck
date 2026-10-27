// Package mdexport writes a structured pane's conversation as Markdown.
//
// What is included: the person's prompts, the agent's replies, a tool call as
// its label, status and summary, a helper agent's description and report, mail
// between agents (marked with its direction, peer and delivery state), notices
// and compactions. What is left out: a tool's full output and diffs, images
// (a line names the type and size), and thinking, which is included only when
// the caller asks and the text is still held.
//
// Everything in the file that did not come from Flockdeck is a quoted block:
// every line of a body starts with "> ", so a reply that contains "## You" or
// a fence cannot start a heading or leave its block, and so cannot pass as a
// turn of the person's. Headings and markers are written here, and a label
// taken from an entry is one cleaned line inside a code span.
package mdexport

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

const (
	// MaxBytes is the most Markdown Render returns. Entries past it are left out
	// and the file says so.
	MaxBytes = 8 << 20
	// bodyCut bounds one body before it is scrubbed, in bytes.
	bodyCut = 256 << 10
	// lineCut bounds a label, a summary line or a peer name, in runes.
	lineCut = 300
)

// ErrNoScrub is returned when Options has no Scrub: nothing is written without
// a redactor.
var ErrNoScrub = errors.New("no redactor given")

// Options say what Render does. Scrub is required and returns the text with
// secrets replaced and the number of values it replaced. ThinkingText returns
// the held text of a thinking entry; it is read only when Thinking is set.
type Options struct {
	Title        string
	Agent        string
	Exported     string
	Thinking     bool
	Scrub        func(string) (string, int)
	ThinkingText func(id string) (string, bool)
}

// Result is the Markdown and a count of what it holds.
type Result struct {
	Markdown      string
	Entries       int
	Redactions    int
	ThinkingShown int
	// Cut says entries were left out to stay inside MaxBytes.
	Cut bool
}

var tsRe = regexp.MustCompile(`^[0-9TZ:.+-]{1,40}$`)

// Render writes the entries, oldest first.
func Render(entries []transcript.Entry, o Options) (Result, error) {
	if o.Scrub == nil {
		return Result{}, ErrNoScrub
	}
	var r Result
	var b strings.Builder
	scrub := func(s string) string {
		out, n := o.Scrub(s)
		r.Redactions += n
		return out
	}
	line := func(s string) string { return code(scrub(oneLine(s, lineCut))) }

	b.WriteString("# " + code(plain(oneLine(scrub(oneLine(o.Title, 120)), 120), "Flockdeck conversation")) + "\n\n")
	if a := oneLine(o.Agent, 60); a != "" {
		b.WriteString("Agent: " + code(a) + "  \n")
	}
	if t := oneLine(o.Exported, 40); tsRe.MatchString(t) {
		b.WriteString("Exported: " + t + "  \n")
	}
	b.WriteString("\nIncluded: prompts, replies, tool calls with their summaries, helper reports, mail between agents and notices. Not included: tool output and diffs, images")
	if o.Thinking {
		b.WriteString(". Thinking is included where it was still held")
	} else {
		b.WriteString(", thinking")
	}
	b.WriteString(". Secrets the redactor knows were replaced with [REDACTED: kind] marks. It is a filter, so read the file before sharing it.\n\n")
	b.WriteString("Every line that begins with \"> \" is quoted from the conversation, with images and HTML neutralised: \"![\" is written \"\\![\" and \"<\" is written \"&lt;\". Links and code fences are left as they were. Headings are written by Flockdeck.\n")

	for _, e := range entries {
		block := renderEntry(e, o, scrub, line, &r)
		if block == "" {
			continue
		}
		if b.Len()+len(block) > MaxBytes {
			r.Cut = true
			break
		}
		b.WriteString(block)
		r.Entries++
	}
	if r.Cut {
		b.WriteString(fmt.Sprintf("\n---\n\nThe export stopped here: the file is limited to %d MiB and later entries are left out.\n", MaxBytes>>20))
	}
	r.Markdown = b.String()
	return r, nil
}

func renderEntry(e transcript.Entry, o Options, scrub func(string) string, line func(string) string, r *Result) string {
	when := ""
	if tsRe.MatchString(e.TS) {
		when = " · " + e.TS
	}
	heading := func(h string) string { return "\n## " + h + when + "\n\n" }
	switch e.Kind {
	case transcript.KindPrompt:
		h := "You"
		if e.Replayed {
			h = "Earlier prompt replayed by the agent"
		}
		return heading(h) + quote(scrub(cut(e.Text, bodyCut)))
	case transcript.KindReply:
		t := e.Markdown
		if t == "" {
			t = e.Text
		}
		return heading("Agent") + quote(scrub(cut(t, bodyCut)))
	case transcript.KindThinking:
		if !o.Thinking || o.ThinkingText == nil {
			return ""
		}
		t, ok := o.ThinkingText(e.ID)
		if !ok || t == "" {
			return ""
		}
		r.ThinkingShown++
		return heading("Agent thinking") + quote(scrub(cut(t, bodyCut)))
	case transcript.KindTool:
		s := heading("Tool call") + "Tool: " + line(plain(e.Label, e.Name))
		if st := status(e.Status); st != "" {
			s += "  \nStatus: " + st
		}
		if e.Summary != "" {
			s += "\n\n" + quote(scrub(cut(e.Summary, 2<<10)))
		} else {
			s += "\n"
		}
		return s
	case transcript.KindSubagent:
		s := heading("Helper agent") + "Task: " + line(plain(e.Description, e.Name))
		if st := status(e.Status); st != "" {
			s += "  \nStatus: " + st
		}
		if e.Report != "" {
			s += "\n\n" + quote(scrub(cut(e.Report, bodyCut)))
		} else {
			s += "\n"
		}
		return s
	case transcript.KindCompaction:
		return heading("Conversation compacted") + "The agent summarised earlier turns here.\n"
	case transcript.KindNotice:
		return heading("Notice") + quote(scrub(cut(e.Text, 4<<10)))
	case transcript.KindImage:
		return heading("Image") + fmt.Sprintf("An image (%s, %d bytes) was here. Images are not exported.\n", mediaType(e.MediaType), max(e.Bytes, 0))
	case transcript.KindMessage:
		dir := "Received from "
		if e.Dir == transcript.MessageOut {
			dir = "Sent to "
		}
		s := heading("Mail between agents, not the person") + dir + line(plain(oneLine(e.Peer, lineCut), "an agent"))
		if d := delivery(e.Delivery); d != "" {
			s += "  \nDelivery: " + d
		}
		return s + "\n\n" + quote(scrub(cut(e.Text, bodyCut)))
	}
	return ""
}

// ScrubbedAway counts the marks that say the redactor gave up on a text for its
// size or time (the scrubber's too-large-to-scrub kind).
func ScrubbedAway(md string) int { return strings.Count(md, "[REDACTED: too-large-to-scrub]") }

func status(s string) string {
	switch s {
	case transcript.StatusRunning, transcript.StatusOK, transcript.StatusError:
		return s
	}
	return ""
}

func delivery(s string) string {
	switch s {
	case "queued", "delivered", "handled", "undeliverable":
		return s
	}
	return ""
}

var mediaRe = regexp.MustCompile(`^image/[a-z0-9.+-]{1,30}$`)

func mediaType(s string) string {
	if mediaRe.MatchString(s) {
		return s
	}
	return "unknown type"
}

func plain(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// cut limits s to n bytes on a rune boundary.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "\n[cut]"
}

// oneLine is s as a single line without control, format or separator
// characters, cut to n runes.
func oneLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == utf8.RuneError:
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if rs := []rune(s); len(rs) > n {
		s = string(rs[:n])
	}
	return s
}

// code puts one line in a code span, with backticks inside it turned to
// apostrophes so the span cannot end early.
func code(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}

// quote prefixes every line of s with "> ", blank ones too, so no line of it
// starts a heading, list or fence of its own. Format and control characters
// are removed first, and every kind of line break becomes a newline, so a line
// cannot be hidden or split in a way the quote prefix would miss.
func quote(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r' || r == ' ' || r == ' ' || r == '\u0085' || r == '\v' || r == '\f':
			return '\n'
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	// Image syntax would load a remote picture when the file is opened in a viewer,
	// and raw HTML can do more; both are written as the text they are.
	s = strings.ReplaceAll(strings.ReplaceAll(s, "![", "\\!["), "<", "&lt;")
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if l == "" {
			b.WriteString(">\n")
		} else {
			b.WriteString("> " + l + "\n")
		}
	}
	return b.String()
}
