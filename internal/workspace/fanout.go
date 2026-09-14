package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// recentOutputBytes is how much of a pane's output the task extractor reads.
// A plan is near the end of what an agent just said.
const recentOutputBytes = 64 << 10

// maxTasks caps a fan-out. Each task is a real agent with a real terminal, and
// beyond this the machine, not the idea, becomes the limit.
const maxTasks = 12

// MaxTasks is maxTasks for callers that start the agents themselves, so the
// limit is one number rather than one per fan-out path.
const MaxTasks = maxTasks

// cutMark ends a task that had to be cut short to fit within maxTaskBytes, so
// that the person editing the list can see the rest of it is missing.
//
// A task used to be held to six hundred characters as well, past which it was
// taken for a paragraph that happened to begin with a dash and dropped without
// a word. But the briefing asks an agent for one self-contained task per line,
// for helpers that inherit nothing else, and a line that carries its own
// context is often longer than that: the fullest steps of a plan were the ones
// that vanished from it.
const cutMark = " …"

// minTaskRunes is the shortest thing worth starting an agent for. Below it a
// line is a fragment: the tail of a wrapped row, or a one-word status.
const minTaskRunes = 8

// ExtractTasks finds the work items in a plan.
//
// A lead agent asked for a plan answers with a list, so list markers are the
// signal: bullets, numbers and checkboxes. Everything else is prose. The result
// is a suggestion the user edits before anything is started, so it is better to
// offer a few plausible lines than to guess cleverly.
//
// text is read as a pane's screen, which is the harder of the two things a plan
// is read from; see extractTasks.
func ExtractTasks(text string) []string { return extractTasks(text, true) }

// extractTasks is ExtractTasks, told whether text is a pane's screen or one of
// the agent's replies from its transcript.
//
// A screen is the tail of a terminal: it can begin part way through a code
// block, and it carries the interface the agent's CLI draws around what it
// says. A reply is neither -- it is the whole of one message, and nothing but
// what the agent wrote -- so the allowances made for a screen only cost a reply
// the tasks they misread.
func extractTasks(text string, screen bool) []string {
	items, cues := listItems(text, screen)

	// An agent's answer is rarely only its plan: it surveys what it read, notes
	// what it found, and then says what it would do. All of that is bulleted,
	// and only the last part is work. When the answer says where the plan
	// starts, take it at its word — but only while that still leaves something.
	//
	// The latest cue that does is the one to trust. A reply commonly closes on
	// another heading, "Next steps" over a sentence rather than a list, and
	// judging by the last cue alone would fall all the way back to every bullet
	// in the answer — the findings the cue was there to leave behind included.
	for i := len(cues) - 1; i >= 0; i-- {
		var planned []item
		for _, it := range items {
			if it.line > cues[i] {
				planned = append(planned, it)
			}
		}
		if len(planned) > 0 {
			items = planned
			break
		}
	}

	// Only the shallowest entries are tasks. Anything nested under one is
	// detail about how to do that job, not a job of its own, and fanning out
	// over both hands the same work to two agents.
	base := -1
	for _, it := range items {
		if base < 0 || it.indent < base {
			base = it.indent
		}
	}

	items = promoteUnderLabels(items, base, screen)

	var out []string
	seen := map[string]bool{}
	for i, it := range items {
		// A single space of drift is alignment, not nesting.
		if it.indent > base+1 {
			continue
		}
		task, ok := shallowTask(items, i, base, screen)
		if !ok {
			continue
		}
		key := strings.ToLower(task)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, task)
		if len(out) >= maxTasks {
			break
		}
	}
	return out
}

// shallowTask reads items[i], one of the shallowest entries, as the task it
// offers, and reports whether it offers one at all.
func shallowTask(items []item, i, base int, screen bool) (string, bool) {
	task := tidyTask(items[i].text)
	// A step that ends on a colon is handing over to what is nested under
	// it — "Fix the login bug:" over the bug itself — and dropped as
	// detail, that left the agent a heading and nothing it headed. A step
	// with no colon is whole without its detail, and keeps going without
	// it; a plan heading is dropped below, its steps being the work.
	if strings.HasSuffix(task, ":") && !isPlanCue(task) {
		if full := withDetail(task, items[i+1:], base); isTask(full, screen) {
			task = full
		}
	}
	// A lead-in written as an entry beside the steps, "Here's the plan:",
	// heads nothing but is no task either. The colon is what hands over to
	// a list: "Split this into two files" shares a phrase with a lead-in
	// and is still a task.
	if !isTask(task, screen) || isPlanHeading(task) || (strings.HasSuffix(task, ":") && isPlanCue(task)) {
		return "", false
	}
	return task, true
}

// promoteUnderLabels lifts the entries nested under a shallowest entry that is
// no task itself -- "1. Backend" over the steps for the backend -- to the depth
// of the steps, and drops the label. base is the shallowest depth.
//
// Only the shallowest entries are tasks, since whatever is nested under a job
// is detail about doing it. A plan grouped under labels puts the labels there,
// and they are a word each and no job at all: dropping them and their detail
// with them, the fan-out found nothing in a plan that was nothing but work. A
// label's own entries are the jobs, and deeper ones are still their detail, so
// the whole group moves up by the same amount.
func promoteUnderLabels(items []item, base int, screen bool) []item {
	for i := 0; i < len(items); i++ {
		if items[i].indent > base+1 {
			continue
		}
		end := i + 1
		for end < len(items) && items[end].indent > base+1 {
			end++
		}
		if end == i+1 {
			continue
		}
		if _, ok := shallowTask(items, i, base, screen); ok {
			continue
		}
		// Only a label gives way. An entry can be no task for being a question
		// or a finding, and what is nested under one of those is no more work
		// than it is: promoted, the options under "Which approach do you
		// prefer?" were each offered as a task to start.
		if !isGroupLabel(items[i].text) {
			continue
		}
		child := items[i+1].indent
		for _, it := range items[i+1 : end] {
			child = min(child, it.indent)
		}
		for j := i + 1; j < end; j++ {
			items[j].indent -= child - base
		}
		items = append(items[:i], items[i+1:]...)
		// The first of the label's entries is now at i, and may be a label
		// over entries of its own.
		i--
	}
	return items
}

// maxLabelWords is the most words a group label is taken to have: "Backend",
// "Web UI", "Phase 2 cleanup".
const maxLabelWords = 4

// isGroupLabel reports whether an entry reads as the name of a group of steps
// rather than as anything said about them: a few words, neither asked as a
// question nor opening the way a finding does.
func isGroupLabel(s string) bool {
	label := strings.TrimSpace(strings.TrimSuffix(tidyTask(s), ":"))
	if label == "" || strings.HasSuffix(label, "?") || len(strings.Fields(label)) > maxLabelWords {
		return false
	}
	return !opensOnFinding(label)
}

// withDetail finishes a step that ends on a colon with the entries nested
// under it, joined in order — "Fix the login bug: the refresh races with
// logout; add a regression test" — for as many of them as keep the task
// within maxTaskBytes, ending on cutMark where the rest had to be left off.
// rest is every entry after the step; the step's own entries are those
// indented past base+1 before the next step.
func withDetail(task string, rest []item, base int) string {
	full := task
	added := false
	for _, d := range rest {
		if d.indent <= base+1 {
			break
		}
		part := tidyTask(d.text)
		if part == "" {
			continue
		}
		sep := " "
		if added {
			sep = "; "
		}
		if len(full)+len(sep+part) > maxTaskBytes-len(cutMark) {
			full += cutMark
			break
		}
		full += sep + part
		added = true
	}
	return full
}

// item is one list entry, how far it was indented, and which line it came from.
type item struct {
	indent int
	// textAt is where the entry's text starts, for one on the line an agent
	// marks as the start of what it says; for every other entry it is indent.
	textAt int
	line   int
	text   string
}

// listItems reads the list entries out of text, and reports the lines on which
// the text announced a plan.
//
// An entry that carries on over the lines below it is put back together: a
// terminal breaks a long bullet across rows, and markdown lets one run on over
// indented lines. Keeping only the first row of either would hand an agent a
// task that stops mid-sentence.
//
// screen says whether text is a pane's screen rather than a reply read from a
// transcript; see extractTasks.
func listItems(text string, screen bool) ([]item, []int) {
	// A terminal redraws a row by returning to the start of it and writing
	// over what was there, so one newline-delimited line of a pane history
	// can hold several renderings of the same row — the last of which is how
	// the row finally read. Each rendering is a line of its own here. The
	// pair is folded first: turning the carriage return of a CRLF into a
	// newline of its own would put a blank line under every row, and a blank
	// line ends the entry a wrapped bullet is still in the middle of.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(strings.ReplaceAll(text, "\r", "\n"), "\n")

	var items []item
	var cues []int // the lines announcing a plan, in the order they appear
	open := -1     // the entry a continuation line belongs to, if any
	// A pane screen is only the tail of its output, so it can begin part way
	// through a code block. An odd number of fences says that is what happened:
	// the first marker is a closing one, and reading it as an opening one would
	// bury the whole plan that follows it.
	//
	// A reply is a whole message, so its first marker is always an opening one.
	// One that ends in a code block it never closed -- cut off, or written
	// carelessly -- has an odd count too, and reading that as a screen's put
	// the plan above the block inside it and offered nothing at all.
	fenced := screen && countFences(lines)%2 == 1

	for n, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		body := strings.TrimLeft(line, " \t")
		indent := indentWidth(line)

		// A list inside a code block is sample text, not a plan.
		if isFence(body) {
			fenced = !fenced
			open = -1
			continue
		}
		if fenced || body == "" || isDecoration(body) {
			open = -1
			continue
		}
		if entry, textAt, ok := openingEntry(body, indent); ok {
			// A heading or lead-in written as a list entry with the steps nested
			// under it — "- Next steps:" over indented bullets, or Codex's bullet
			// in front of "Plan:" or "Here's the plan:" — announces the plan as
			// the same words on a line of their own do. Kept as an entry it was
			// the shallowest one, so the steps under it went as its detail, and
			// the dialog offered nothing, or the lead-in itself as the one task.
			// A heading beside the steps, at their own depth, heads nothing and
			// stays an entry, to be dropped as one.
			if last := len(items) - 1; last >= 0 && indent > items[last].indent && isPlanCue(items[last].text) {
				heading := items[last].line
				items = items[:last]
				// Cues are read latest first, so the heading goes in by line.
				at := len(cues)
				for at > 0 && cues[at-1] > heading {
					at--
				}
				cues = append(cues[:at], append([]int{heading}, cues[at:]...)...)
			}
			items = append(items, item{indent: indent, textAt: textAt, line: n, text: entry})
			open = len(items) - 1
			continue
		}
		if isPlanCue(body) {
			cues = append(cues, n)
			open = -1
			continue
		}
		// Text indented under the entry above it is the rest of that entry, but
		// only while the join still reads as a task. isTask throws away
		// anything past maxTaskBytes whole, so a continuation that tips an
		// entry over the limit would cost the plan a real job rather than the
		// tail of one. Stop at the last row that fits, say the entry was cut
		// there, and close it: a task that simply stops mid-sentence reads as
		// the whole of what was asked.
		if open >= 0 && indent >= items[open].indent+2 {
			joined := items[open].text + " " + body
			if len(joined) <= maxTaskBytes-len(cutMark) {
				items[open].text = joined
				continue
			}
			items[open].text += cutMark
		}
		open = -1
	}
	// A step on the agent's own line is one of the steps under it only when
	// they line up with its text. Moving it there regardless made a numbered
	// bullet beside a plain one the other's detail; detail indented under it
	// deeper still does not stop the rest lining up after.
	for i := range items {
		if items[i].textAt <= items[i].indent {
			continue
		}
		for _, later := range items[i+1:] {
			if later.indent <= items[i].indent {
				break
			}
			if later.indent == items[i].textAt {
				items[i].indent = items[i].textAt
				break
			}
		}
	}
	return items, cues
}

// tabWidth is how wide a tab is taken to be when measuring indentation. Any
// value above one would do: what matters is that a tab outranks the single
// space that separates alignment from nesting.
const tabWidth = 4

// indentWidth measures how far a line is indented, in columns rather than in
// bytes. A markdown list is as often indented with tabs as with spaces, and
// counting bytes puts a tabbed sub-item one column in, which reads as drift
// rather than as nesting — so an agent is handed the detail of a job as a job.
func indentWidth(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += tabWidth
		default:
			return n
		}
	}
	return n
}

// isFence reports whether a line opens or closes a code block.
func isFence(body string) bool {
	return strings.HasPrefix(body, "```") || strings.HasPrefix(body, "~~~")
}

// countFences counts the code block markers in a set of lines.
func countFences(lines []string) int {
	n := 0
	for _, raw := range lines {
		if isFence(strings.TrimLeft(raw, " \t")) {
			n++
		}
	}
	return n
}

// planHeadings are the whole line, once its markup is stripped: a heading over
// the list that follows.
var planHeadings = []string{
	"plan", "the plan", "tasks", "the tasks", "work", "the work",
	"next steps", "steps", "proposed work", "what i would do", "what i'd do",
	// A reply that reports progress lists what is done before what is not,
	// and only the second list is work to hand out.
	"remaining", "remaining work", "remaining tasks", "what's left",
	"still to do", "left to do", "to do", "todo",
}

// planPhrases announce a plan in the middle of a sentence, so they are looked
// for anywhere in the line. Each is long enough not to appear by accident.
var planPhrases = []string{
	// "the plan is" is deliberately absent: it also ends sentences like "let me
	// read this first, so the plan is concrete", which announces nothing.
	"here's the plan", "here is the plan",
	"here's what i'd do", "here's what i would do",
	"i'll do the following", "i will do the following",
	"split this into", "fan this out",
}

// isPlanCue reports whether a line says that what follows is the work.
func isPlanCue(line string) bool {
	lower := strings.ToLower(tidyTask(line))
	for _, phrase := range planPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return isPlanHeading(line)
}

// isPlanHeading reports whether a line is nothing but a heading over the list
// below it. Written as prose it announces the plan; written as a bullet it is
// still a heading, and handing "Next steps" to an agent as its whole brief
// tells it nothing at all.
func isPlanHeading(line string) bool {
	heading := trimLeadGlyph(strings.TrimSpace(strings.Trim(strings.ToLower(tidyTask(line)), "#*_:. ")))
	for _, h := range planHeadings {
		if heading == h {
			return true
		}
	}
	return false
}

// tidyTask turns an extracted entry into the prompt an agent would be given:
// one line, and without the markdown emphasis that only meant anything while it
// was being rendered.
func tidyTask(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.Join(strings.Fields(s), " ")
	// Emphasis written with one mark rather than two is left over at the ends.
	// Kept, it makes the entry open on punctuation, and isTask reads that as
	// the middle of a wrapped line and throws the whole task away.
	return trimLeadGlyph(strings.Trim(s, "*_"))
}

// trimLeadGlyph drops the decorative symbol an agent so often puts at the head
// of a list entry — ✅, 🔧, ⚠️, 📝 and the rest of them.
//
// It is ornament, not part of the job. Left on, it makes the entry open on
// something that is neither a letter nor a digit, which isTask reads as the
// middle of a wrapped line and throws away whole — so a plan written in that
// style yielded not a shortened list of tasks but none at all.
//
// Only a symbol standing on its own before a space goes. That keeps the
// backtick an entry may legitimately open on, and leaves alone the rare line
// where the symbol is what the line is about.
func trimLeadGlyph(s string) string {
	rest := s
	trimmed := false
	for {
		r, n := utf8.DecodeRuneInString(rest)
		if n == 0 || !isGlyphRune(r) {
			break
		}
		rest = rest[n:]
		trimmed = true
	}
	if !trimmed || !strings.HasPrefix(rest, " ") {
		return s
	}
	return strings.TrimLeft(rest, " ")
}

// isGlyphRune reports whether r is one of the marks that make up a decorative
// symbol: the symbol itself, and the selectors and joiners that dress it.
//
// Unicode's modifier symbols are deliberately left out. The backtick is one of
// them, and an entry naming a function in backticks is a task like any other.
func isGlyphRune(r rune) bool {
	switch r {
	case 0x200D, 0xFE0E, 0xFE0F: // zero-width joiner, variation selectors
		return true
	}
	return unicode.Is(unicode.So, r) || unicode.Is(unicode.Mn, r)
}

// isTask reports whether a line reads as work rather than as the interface drawn
// around it. screen says whether it was read off a pane's screen, the only
// place that interface can appear.
func isTask(s string, screen bool) bool {
	r := []rune(s)
	if len(r) < minTaskRunes || len(s) > maxTaskBytes {
		return false
	}
	// A task is a phrase. One word is a spinner frame or the tail of a row that
	// was wrapped away from its own beginning.
	//
	// That is a rule about languages which separate their words with spaces.
	// Chinese and Japanese do not, so an entire sentence in either is one field
	// and the rule threw away every line of the plan rather than some of it.
	// Where there are no words to count, length is the measure, and the minimum
	// above is already it.
	if len(strings.Fields(s)) < 2 && !spaceless(s) {
		return false
	}
	// Something that opens on punctuation is the middle of a line, not a task.
	switch r[0] {
	case '`', '"', '\'':
	default:
		if !unicode.IsLetter(r[0]) && !unicode.IsDigit(r[0]) {
			return false
		}
	}
	// A question is something to answer, not something to go and do.
	if strings.HasSuffix(s, "?") {
		return false
	}
	// The interface is only ever drawn on the screen. A reply read from a
	// transcript is what the agent wrote and nothing else, and "show the
	// context left in the status bar" is a job there like any other.
	if screen && isChrome(s) {
		return false
	}
	// An agent bullets its findings as readily as its plan. A finding opens by
	// naming the thing it is about — "The Go process owns the panes" — where an
	// instruction opens with the doing, or with what is to be done to. Note
	// that "I'll add …" and "We should …" are instructions, and stay.
	return !opensOnFinding(s)
}

// opensOnFinding reports whether a line opens the way a finding does, by
// naming the thing it is about: "The Go process owns the panes".
func opensOnFinding(s string) bool {
	lower := strings.ToLower(s)
	for _, opener := range []string{"the ", "there ", "this ", "that ", "these ", "those ", "it "} {
		if strings.HasPrefix(lower, opener) {
			return true
		}
	}
	return false
}

// isChrome reports whether a line is part of the interface an agent's CLI draws
// around what it says: its status line, key hints and counters.
func isChrome(s string) bool {
	lower := strings.ToLower(s)
	for _, phrase := range terminalChrome {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	for _, prefix := range terminalChromeOpeners {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return isProgress(s)
}

// spaceless reports whether s is written in a script that does not put spaces
// between its words, which is what makes counting them meaningless.
func spaceless(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai) {
			return true
		}
	}
	return false
}

// terminalChrome is what Claude Code's interface draws around what its agent
// says: the status line, the key hints, the token counters. A fan-out reading a
// pane's screen rather than its transcript sees all of it, and a status line
// beginning with a bullet glyph looks exactly like a list item.
var terminalChrome = []string{
	"esc to interrupt",
	"for shortcuts",
	"tokens ·",
	"tokens)",
	"thought for",
	"expand thinking",
	"context left",
	"auto-accept",
	"bypass permissions",
}

// terminalChromeOpeners is chrome that fills the line it opens, so it is only
// looked for at the start. A key hint stands alone on its own row, where the
// same text inside a sentence — "add a ctrl+k command palette" — is a job to
// do, and this application's plans are full of them.
var terminalChromeOpeners = []string{
	"ctrl+",
	"shift+tab",
}

// isProgress reports whether a line is Claude Code's status line: a word, an
// ellipsis and an elapsed time, as in "Perambulating… (26s".
func isProgress(s string) bool {
	i := strings.Index(s, "… (")
	if i < 0 {
		return false
	}
	rest := s[i+len("… ("):]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits < len(rest) && rest[digits] == 's'
}

// listItem strips a leading list marker, reporting whether there was one.
//
// The glyphs Claude Code spins through while it works — · ▸ → among them — are
// deliberately not markers. On screen they sit at the head of a line with a
// space after them, which is indistinguishable from a bullet.
func listItem(line string) (string, bool) {
	// Bullets, and the checkbox one may carry.
	for _, marker := range []string{"* ", "- ", "• "} {
		if strings.HasPrefix(line, marker) {
			return trimCheckbox(strings.TrimPrefix(line, marker)), true
		}
	}
	// "1. text", "2) text", "10 - text", "(3) text", and each of those written
	// as a heading or in bold.
	if entry, ok := numberedItem(unheaded(line)); ok {
		return entry, true
	}
	// "TODO: text"
	for _, prefix := range []string{"TODO: ", "TODO ", "Task: "} {
		if strings.HasPrefix(line, prefix) {
			return line[len(prefix):], true
		}
	}
	// "Task 2: text", "Step 3 — text", "## Step 4: text"
	return labelledItem(unheaded(line))
}

// openingEntry reads a line as a list entry, and says how far in the entry
// starts, which for most lines is where the line does.
//
// An agent marks the start of what it says — Claude Code with ⏺, Gemini with
// ✦, Codex with a bullet — and a reply that opens on its list puts the first
// step on that line, the rest indented to line up under it. Read as written,
// that step was no entry at all, or for Codex a bullet whose text was "1. …",
// the shallowest entry, with the steps below dropped as its detail. So a line
// at the left edge whose mark leaves an entry behind it is read as that entry,
// measured from where its text starts. The glyphs an agent spins through while
// it works leave no entry behind, so they are still not a list; and one
// indented under a message, as Claude Code's tool output is, is left alone.
func openingEntry(body string, indent int) (string, int, bool) {
	width := func(s string) int { return utf8.RuneCountInString(s) }
	if entry, ok := listItem(body); ok {
		if inner, numbered := numberedItem(entry); numbered && strings.HasPrefix(body, "• ") {
			return inner, indent + width(body) - width(entry), true
		}
		return entry, indent, true
	}
	if indent == 0 {
		if rest := trimLeadGlyph(body); rest != body {
			if entry, ok := listItem(rest); ok {
				return entry, width(body) - width(rest), true
			}
		}
	}
	return "", indent, false
}

// unheaded strips what a numbered line of a plan may be dressed in: a markdown
// heading, and bold.
//
// A plan long enough to put a paragraph under each step is as often numbered
// with headings as with a list, and a plan written that way offered nothing to
// fan out. Only the number makes such a line a task, so a heading without one
// is still read as a heading.
func unheaded(line string) string {
	if h := strings.TrimLeft(line, "#"); h != line && strings.HasPrefix(h, " ") {
		line = strings.TrimLeft(h, " ")
	}
	for _, mark := range []string{"**", "__"} {
		line = strings.TrimPrefix(line, mark)
	}
	return line
}

// numberedItem strips a leading number and its separator.
//
// The parenthesised spelling is here for the same reason the other three are:
// an agent numbering its plan writes "(1)" as readily as "1." or "1)", and a
// plan written in the one form the reader did not know went through as no plan
// at all. Only ")" closes a "(", so nothing else is read as a number in
// brackets.
func numberedItem(line string) (string, bool) {
	rest := line
	bracketed := strings.HasPrefix(rest, "(")
	if bracketed {
		rest = rest[1:]
	}
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits > 3 || digits >= len(rest) {
		return "", false
	}
	seps := []string{". ", ") ", "- ", ": "}
	if bracketed {
		seps = []string{") "}
	}
	// The separator is allowed one leading space, which is how a dash is
	// written after a number: "10 - text" rather than "10- text".
	tail := strings.TrimPrefix(rest[digits:], " ")
	for _, sep := range seps {
		if strings.HasPrefix(tail, sep) {
			return tail[len(sep):], true
		}
	}
	return "", false
}

// labelledItem strips a "Task 2:" or "Step 3:" label.
//
// An agent asked to lay out an order of work sometimes numbers its steps in
// words instead of with a list marker, which leaves a plan with no list in it
// for the extractor to find. The number is required: without it "Step through
// the reconnect path" would be read as an instruction to do "through the
// reconnect path".
func labelledItem(line string) (string, bool) {
	for _, word := range []string{"Task", "Step"} {
		if !strings.HasPrefix(line, word+" ") {
			continue
		}
		rest := strings.TrimLeft(line[len(word):], " ")
		digits := 0
		for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
			digits++
		}
		if digits == 0 || digits > 3 {
			continue
		}
		rest = strings.TrimLeft(rest[digits:], " ")
		for _, sep := range []string{": ", "- ", "— ", ". ", ") "} {
			if strings.HasPrefix(rest, sep) {
				return rest[len(sep):], true
			}
		}
	}
	return "", false
}

// trimCheckbox drops the box a task-list bullet carries: "[ ]", "[x]", and the
// "[X]" and "[-]" that get written just as often. Leaving it on would make the
// entry open on punctuation, which isTask reads as the middle of a line and
// throws away — so an unticked plan lost every item it had.
func trimCheckbox(s string) string {
	if len(s) >= 4 && s[0] == '[' && s[2] == ']' && s[3] == ' ' {
		return s[4:]
	}
	return s
}

// isDecoration reports whether a line is terminal furniture rather than words.
func isDecoration(line string) bool {
	letters := 0
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters++
		}
	}
	return letters*3 < len([]rune(line))
}

// maxTaskBytes bounds the opening prompt a child agent can be given.
//
// The task is handed to the agent as a command-line argument, and Windows will
// not take a command line beyond about thirty-two thousand characters. Past
// that the pane does not start at all, and what the user is shown is whatever
// the operating system has to say about command lines — which names neither
// the task nor the length as the problem. The same text is also written into
// the saved layout, which is no place for a document.
//
// The limit is far above any brief anyone writes, and it is the only one a task
// a fan-out proposes is held to: a step past it is offered cut short, and says
// so, rather than offered whole only to be refused here.
const maxTaskBytes = 16 << 10

// AgentSpec resolves the agent a pane was asked to run, and says why it cannot
// be run when it cannot. An empty id means the default agent of the project on
// screen, which is a field only the goroutine that owns the workspace may read;
// AgentSpecByID asks the same about an agent named by id, from anywhere.
//
// The question asked here is about one spec rather than about Claude. A
// fan-out may now give four of its tasks to one agent and eight to another,
// and the half whose agent is installed should start whatever is true of the
// other half -- which it cannot while the only question Flockdeck knows how to ask
// is whether Claude Code is on this machine.
func (w *Workspace) AgentSpec(id string) (agent.Spec, error) {
	if id == "" {
		id = w.defaultAgent()
	}
	return w.AgentSpecByID(id)
}

// AgentSpecByID is AgentSpec for an agent named by its id, and is safe to call
// from any goroutine. It reads the catalog, which has a lock of its own, and
// the claude CLI found when the workspace was made, and nothing else: not the
// active project, which is written on the workspace goroutine at every project
// switch. AgentSpec read that for every id -- empty or not, since it was an
// argument -- so the fan-out dialog probing agents, and a fan-out deciding
// which rows ask about folder trust, raced every switch. An empty id names no
// agent here.
func (w *Workspace) AgentSpecByID(id string) (agent.Spec, error) {
	spec, ok := w.agents().Find(id)
	if !ok {
		return agent.Spec{}, fmt.Errorf("there is no agent called %q", id)
	}
	if w.agentAvailable(spec) {
		return spec, nil
	}
	if spec.Exe == "" {
		return spec, fmt.Errorf("%s cannot be started on this machine", spec.Name)
	}
	// Word for word what the pane that would have failed says, how to install
	// the agent included: a refused spawn goes back to the agent that asked
	// for it, and a fan-out's notice to the user, and either is where somebody
	// learns the agent is missing.
	return spec, missingCLI(spec)
}

// missingCLI is what somebody is told when the CLI an agent runs is not
// installed — in the pane that could not start, and in the refusal of a spawn
// or a fan-out that would have needed it: what is wrong, and where the catalog
// knows it, how to put it right, worded as the session package words the same
// failure.
//
// Being told only "not found on PATH" left them to go and look up what the
// picker beside them already says.
func missingCLI(spec agent.Spec) error {
	if spec.Install == "" {
		return fmt.Errorf("the `%s` CLI was not found on PATH", spec.Exe)
	}
	return fmt.Errorf("the `%s` CLI was not found on PATH; install %s first (%s)", spec.Exe, spec.Name, spec.Install)
}

// agentAvailable reports whether a spec can be started on this machine.
//
// The catalog remembers what it probed for a few seconds, which is what keeps
// a twelve-row fan-out from walking PATH twelve times over to be told the same
// thing.
func (w *Workspace) agentAvailable(spec agent.Spec) bool {
	if spec.Exe == "claude" && w.claudeExe != "" {
		// Flockdeck looks the claude CLI up once at start-up, and that answer is
		// older and cheaper than any probe.
		return true
	}
	return agent.Available(spec)
}

// SpawnOptions describes a child agent to start.
type SpawnOptions struct {
	// Task is given to the agent as its opening prompt.
	Task string
	// Cwd is where it works. Empty means the parent's directory.
	Cwd string
	// Branch, when set, puts the child in its own git worktree on that branch
	// so parallel children cannot collide over one checkout.
	Branch string
	// Split places the child beside its parent instead of in a new tab.
	Split bool
	// Tab names an existing tab for the child to join, where it is arranged
	// into a grid with the panes already there. It is how a fan-out gathers a
	// dozen children into one tab rather than scattering them a tab each, and
	// it takes precedence over Split. A tab that has since been closed is
	// ignored, and the child is placed as it would have been without it.
	Tab   string
	Kind  session.Kind
	Title string
	// Agent names the agent.Spec the pane runs, and Model the model that agent
	// is asked for. Empty means the default, which is how a fan-out started
	// without a thought about either keeps running what Flockdeck already ran. An
	// empty model is not "no model": it leaves the choice to the agent, so a
	// CLI keeps whatever it was configured with.
	Agent string
	Model string
	// Routed names the routing rule that chose Model, and RoutedFrom the
	// model the child would otherwise have run. They are recorded on the pane
	// so its header says the model was routed, and why. RoutedFromAgent names
	// the agent RoutedFrom belongs to, set only when routing moved the child
	// to another agent, not only another model.
	Routed          string
	RoutedFrom      string
	RoutedFromAgent string
	// SpawnedByAgent marks a child started by its parent's own `flockdeck
	// spawn`, so the pane records the parent as its Pane.Parent -- as opposed
	// to a fan-out the user ran themselves from the window, whose children are
	// the user's own and carry no parent. Only installSpawnHandler sets it.
	SpawnedByAgent bool
}

// Spawn starts a child agent, optionally in a worktree of its own.
//
// The task is handed to the agent as its opening argument rather than typed into
// the terminal: typing into a TUI means guessing when it is ready, while an
// argument is submitted by the agent itself the moment it starts.
func (w *Workspace) Spawn(parentPaneID string, o SpawnOptions) (string, error) {
	if strings.TrimSpace(o.Task) == "" && o.Kind != session.KindShell {
		return "", fmt.Errorf("a task is required")
	}
	if len(o.Task) > maxTaskBytes {
		// The length said is in characters, which is what the person who wrote
		// the task can check it against; the limit is in bytes, so it is not
		// quoted, since in most scripts the two would never agree.
		return "", fmt.Errorf("the task is %d characters, too long to start an agent with; save the detail to a file in the checkout and give the agent a task that points at it",
			utf8.RuneCountInString(o.Task))
	}
	parent := w.Pane(parentPaneID)
	cwd := o.Cwd
	if cwd == "" && parent != nil {
		cwd = parent.Cwd
	}
	if cwd == "" {
		cwd = w.activeRoot
	}
	// A worktree sits beside its repository, under no open project, and a
	// helper working in one belongs to the agent that asked for it rather
	// than to whichever project happens to be on screen.
	home := w.activeRoot
	if parent != nil {
		home = w.rootOf(parentPaneID)
	}

	// Asked about the agent this pane will actually run rather than about
	// Claude, because a fan-out may now hand half its tasks to one agent and
	// half to another, and "the `claude` CLI was not found" is a nonsense
	// answer to a task that was never going to run Claude. One asked for no
	// agent runs its own project's default, which need not be the default of
	// the project on screen. It is asked before any worktree is made for a
	// helper that could not start in it.
	agentID, model := o.Agent, o.Model
	if o.Kind != session.KindShell {
		agentID, model = w.resolveChoice(w.helperProject(cwd, home), o.Agent, o.Model)
		if _, err := w.AgentSpec(agentID); err != nil {
			return "", err
		}
	}

	if o.Branch != "" {
		path, err := w.worktreeFor(cwd, o.Branch)
		if err != nil {
			return "", err
		}
		cwd = path
	}

	title := o.Title
	if title == "" {
		title = summarisePrompt(o.Task)
	}
	if title == "" {
		title = filepath.Base(cwd)
	}

	p := &Pane{
		ID:      uuid.NewString(),
		Kind:    o.Kind,
		Cwd:     cwd,
		Name:    filepath.Base(cwd),
		Root:    w.helperProject(cwd, home),
		Branch:  branchOf(cwd),
		initial: o.Task,
		Task:    o.Task,
	}
	if o.SpawnedByAgent && parent != nil {
		p.Parent = parentPaneID
	}
	if p.IsAgent() {
		p.Agent, p.Model = agentID, model
		// o.Agent is "" for same-agent routing, which never touches it -- a
		// fan-out row or a spawned helper left to the project's own default
		// agent -- so only an o.Agent that was actually given has to match
		// what this resolved to. Requiring an exact match unconditionally
		// dropped the badge on every same-agent route a helper had, since
		// spawn only ever routes a helper asked for no agent of its own.
		if o.Routed != "" && model == o.Model && (o.Agent == "" || agentID == o.Agent) {
			p.Routed, p.RoutedFrom, p.RoutedFromAgent = o.Routed, o.RoutedFrom, o.RoutedFromAgent
		}
	}
	w.mu.Lock()
	w.panes[p.ID] = p
	w.mu.Unlock()
	w.startPane(p, false)
	if p.Err != nil {
		err := p.Err
		w.destroyPane(p.ID)
		return "", err
	}

	// Place it: in the tab it was asked to join, beside its parent, or as a tab
	// of its own.
	placed := false
	if t := w.Tab(o.Tab); t != nil {
		// Rebuilt as a grid rather than split off whichever pane happens to be
		// there, because this is the path a fan-out arrives on: a dozen panes
		// added one at a time, each of which would otherwise halve the last.
		t.Tree = layout.Grid(append(t.Tree.Panes(), p.ID))
		t.Zoom = false
		placed = true
	}
	if !placed && o.Split && parent != nil {
		for _, t := range w.Tabs {
			if t.Tree.Find(parentPaneID) == nil {
				continue
			}
			if t.Tree.Split(parentPaneID, p.ID, layout.Vertical) {
				t.Zoom = false
				placed = true
			}
			break
		}
	}
	if !placed {
		root := w.activeRoot
		if parent != nil {
			for _, t := range w.Tabs {
				if t.Tree.Find(parentPaneID) != nil {
					root = t.Root
					break
				}
			}
		}
		t := &Tab{
			ID:    uuid.NewString(),
			Root:  root,
			Title: title,
			Tree:  layout.NewLeaf(p.ID),
			Focus: p.ID,
		}
		w.Tabs = append(w.Tabs, t)
	}
	w.wake()
	return p.ID, nil
}

// resolveStartAgent settles what StartAgent needs before it can run: root as
// an already open project, spelled as it was opened, and the agent and model
// that choice resolves to for that project. Nothing is created; a caller
// asks this first to refuse a bad request -- an unknown root or agent --
// before doing anything as slow as cutting a worktree for it, and StartAgent
// asks it again right before it creates the pane, so a project closed or an
// agents.json edited in between is still caught.
func (w *Workspace) resolveStartAgent(root, agentID, model string) (openRoot, resolvedAgent, resolvedModel string, err error) {
	open, ok := w.openRootFor(root)
	if !ok {
		return "", "", "", fmt.Errorf("%s is not an open project", filepath.Base(root))
	}
	agentID, model = w.resolveChoice(open, agentID, model)
	if _, err := w.AgentSpecByID(agentID); err != nil {
		return "", "", "", err
	}
	return open, agentID, model, nil
}

// ValidateStartAgent reports what StartAgent would run for root, agentID and
// model, or the error it would refuse with, without starting anything.
func (w *Workspace) ValidateStartAgent(root, agentID, model string) (openRoot, resolvedAgent string, err error) {
	open, agentID, _, err := w.resolveStartAgent(root, agentID, model)
	return open, agentID, err
}

// StartAgent starts a fresh agent in an already open project, in a new tab of
// its own, without moving the desk's focus: neither the active project nor
// the active tab changes, because a phone starting an agent cannot see the
// desk's window and must not move it out from under whoever is sitting there.
//
// root must already be open; StartAgent refuses to open one, unlike Spawn's
// caller newTabFor, which always works in the active project. cwd is where
// the pane actually works -- root itself, or a worktree cut from it, chosen
// by the caller, since cutting one is slow and best done, as a fan-out's are,
// before anything touches the workspace.
//
// Its Parent is left empty, so unlike a helper started by another agent's own
// `flockdeck spawn`, it is never held back from notifying its user while some
// other pane stays open; see Pane.Parent. It is the user's own pane, exactly
// as one opened by hand at the desk would be -- started from a phone instead.
func (w *Workspace) StartAgent(root, agentID, model, cwd, task string) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("a task is required")
	}
	if len(task) > maxTaskBytes {
		return "", fmt.Errorf("the task is %d characters, too long to start an agent with; save the detail to a file in the checkout and give the agent a task that points at it",
			utf8.RuneCountInString(task))
	}
	open, agentID, model, err := w.resolveStartAgent(root, agentID, model)
	if err != nil {
		return "", err
	}
	if cwd == "" {
		cwd = open
	}

	title := summarisePrompt(task)
	if title == "" {
		title = filepath.Base(cwd)
	}

	p := &Pane{
		ID:      uuid.NewString(),
		Kind:    session.KindClaude,
		Cwd:     cwd,
		Name:    filepath.Base(cwd),
		Root:    open,
		Branch:  branchOf(cwd),
		initial: task,
		Task:    task,
		Agent:   agentID,
		Model:   model,
	}
	w.mu.Lock()
	w.panes[p.ID] = p
	w.mu.Unlock()
	w.startPane(p, false)
	if p.Err != nil {
		err := p.Err
		w.destroyPane(p.ID)
		return "", err
	}

	w.Tabs = append(w.Tabs, &Tab{
		ID:    uuid.NewString(),
		Root:  open,
		Title: title,
		Tree:  layout.NewLeaf(p.ID),
		Focus: p.ID,
	})
	w.wake()
	return p.ID, nil
}

// PrepareWorktree creates (or reuses) a worktree for a branch and returns its
// path. It only runs git, so it is safe to call away from the goroutine that
// owns the workspace — which matters, because git is slow.
func (w *Workspace) PrepareWorktree(cwd, branch string) (string, error) {
	return w.worktreeFor(cwd, branch)
}

// worktreeFor creates (or reuses) a worktree for a branch and returns its path.
func (w *Workspace) worktreeFor(cwd, branch string) (string, error) {
	if !gitx.Available() {
		return "", fmt.Errorf("git is not installed, so a worktree cannot be created")
	}
	repo, err := gitx.Root(cwd)
	if err != nil {
		// Only git's plain "not a git repository (or any of the parent
		// directories)" means there is no repository here. Every other
		// refusal is about one that is here — a worktree whose repository
		// has moved, a .git file that is not what it should be, a checkout
		// git will not trust because another account owns it — and calling
		// all of them "not a git repository" sent the user looking for a
		// repository they could see was there. git's own words say which.
		if strings.Contains(err.Error(), "or any of the parent directories") {
			return "", fmt.Errorf("%s is not a git repository", cwd)
		}
		return "", fmt.Errorf("a worktree cannot be made from %s: %w", cwd, err)
	}
	// A branch that already has a worktree is reused rather than duplicated.
	if wts, err := gitx.List(repo); err == nil {
		for _, wt := range wts {
			if wt.Branch != branch {
				continue
			}
			// Reusing a sibling worktree is the point. Handing back the
			// checkout the request came from is not: the child would work in
			// the very directory the worktree was asked for to keep it out of,
			// and nothing would say so.
			if filepath.Clean(wt.Path) == filepath.Clean(repo) {
				return "", fmt.Errorf("%s is checked out in %s itself, so a worktree of its own cannot be made for it", branch, filepath.Base(repo))
			}
			return wt.Path, nil
		}
	}
	path := gitx.DefaultWorktreePath(repo, branch)
	if err := gitx.AddFrom(repo, path, branch, ""); err != nil {
		return "", err
	}
	return path, nil
}

// maxBranchName bounds the derived part of a branch name. A task description
// is a sentence, and a branch that long is unreadable in `git branch`.
const maxBranchName = 32

// BranchNameFor derives a git branch name from a task description.
//
// Letters are letters in every script. Keeping only a-z threw away every
// character of a task written in Cyrillic, Greek, Japanese or anything else,
// which left nothing at all — so each of those tasks came out as the fallback
// name, and a whole fan-out landed on agent/task, agent/task-2, agent/task-3,
// naming nothing. git takes UTF-8 in a ref, and the worktree directory derived
// from it keeps the same characters already.
func BranchNameFor(task string) string {
	var b strings.Builder
	lastDash := true
	written := 0
	for _, r := range strings.ToLower(task) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if lastDash {
				continue
			}
			b.WriteByte('-')
			lastDash = true
		}
		// One character past the limit, which is what tells a name that only
		// just fits from one that was cut short.
		written++
		if written > maxBranchName {
			break
		}
	}
	// Counted in characters rather than in bytes, because what the limit is for
	// is a name that can be read in `git branch` — and because measuring in
	// bytes would both halve a Cyrillic name and, worse, cut one in the middle
	// of a character, handing git a ref that is not UTF-8 at all.
	name := strings.Trim(b.String(), "-")
	if r := []rune(name); len(r) > maxBranchName {
		cut := maxBranchName
		// A cut that lands inside a word backs up to the boundary before it, so
		// the branch reads as words rather than as a fragment. A cut that lands
		// on one is already where it should be, and backing up from there would
		// throw away a word the name had room for.
		if r[cut] != '-' {
			if i := lastDashBefore(r, cut); i > 8 {
				cut = i
			}
		}
		name = strings.Trim(string(r[:cut]), "-")
	}
	if name == "" {
		name = "task"
	}
	return "agent/" + name
}

// lastDashBefore reports the index of the last dash in r before cut, or -1.
func lastDashBefore(r []rune, cut int) int {
	for i := cut - 1; i >= 0; i-- {
		if r[i] == '-' {
			return i
		}
	}
	return -1
}

// planTurns is how far back a fan-out looks for a plan. What the agent last
// said is usually it, but a plan followed by a short "done, shall I?" should
// still be found.
const planTurns = 4

// PlanSource is where a fan-out looks for the work it should propose.
type PlanSource struct {
	// SessionID names the pane's transcript, when its agent keeps one, and
	// Spec is that agent, which is what knows where the transcript is.
	SessionID string
	Spec      agent.Spec
	// Screen is the pane's recent terminal output. It is the fallback: a shell
	// pane has no transcript, and neither has an agent that has not yet spoken.
	Screen string
}

// PlanSourceFor gathers what a fan-out can read from a pane.
//
// It only touches state already in memory, so it is safe on the goroutine that
// owns the workspace. Opening the transcript is Tasks's job.
func (w *Workspace) PlanSourceFor(paneID string) PlanSource {
	p := w.Pane(paneID)
	if p == nil {
		return PlanSource{}
	}
	var src PlanSource
	// A pane's id is the session id its agent was started with, which is what
	// names its transcript. Where that transcript is kept is the agent's own
	// arrangement: asking Claude Code's store about every agent found nothing
	// for the built-in chat client, whose plan was then read off the screen.
	if p.IsAgent() {
		if spec, ok := w.specFor(p.Root, p.Agent); ok {
			src.SessionID, src.Spec = w.conversationOf(p), spec
		}
	}
	if p.Sess != nil {
		src.Screen = p.Sess.RecentText(recentOutputBytes)
	}
	return src
}

// Tasks proposes the work items in the plan, and reports whether they came from
// the agent's own words rather than from the screen.
//
// It reads from disk, so call it away from the goroutine that owns the
// workspace. The most recent reply that yields any work wins: an agent that has
// laid out a plan and then answered a follow-up should not lose the plan.
func (s PlanSource) Tasks() ([]string, bool) {
	for _, reply := range transcript.For(s.Spec).Replies(s.Spec, s.SessionID, planTurns) {
		if tasks := extractTasks(reply, false); len(tasks) > 0 {
			return tasks, true
		}
	}
	return ExtractTasks(s.Screen), false
}
