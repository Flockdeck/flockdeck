package chat

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The escape sequences the renderer uses. They are the ones every terminal
// worth drawing in has had for decades; nothing here needs a capability
// database.
const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiCyan  = "\x1b[36m"
	ansiBlue  = "\x1b[34m"
	ansiRed   = "\x1b[31m"
)

// defaultWidth is what the answer is wrapped to when the terminal's width
// cannot be discovered. Eighty columns is narrower than most panes, which is
// the right way to be wrong: text wrapped short is readable, text wrapped long
// is wrapped twice -- once by us and again by the terminal -- and every second
// line comes out a fragment.
const defaultWidth = 80

// resolveWidth works out how wide to wrap.
//
// FLOCKDECK_COLUMNS says outright; then the terminal itself is asked, which is
// the only answer that follows a pane being resized; then COLUMNS, which a
// shell sets once and never updates. Nothing sets FLOCKDECK_COLUMNS for a pane,
// so without asking the terminal every answer was wrapped at eighty columns,
// and in a pane narrower than that every line was wrapped a second time by the
// terminal into a line and a fragment.
func resolveWidth() int {
	if n := envWidth("FLOCKDECK_COLUMNS"); n > 0 {
		return n
	}
	if n := terminalWidth(); n >= 20 {
		return n
	}
	if n := envWidth("COLUMNS"); n > 0 {
		return n
	}
	return defaultWidth
}

func envWidth(name string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && n >= 20 {
		return n
	}
	return 0
}

// colourWanted reports whether to write escape sequences at all, honouring the
// two conventions for saying no -- and a third that needs no saying: output
// sent to a file or a pipe is read by something other than a terminal, and
// `flockdeck chat "task" > answer.md` should leave an answer in the file
// rather than the answer wrapped in escape codes.
func colourWanted() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return isConsole(os.Stdout) && enableColour()
}

// printer draws a streamed answer: wrapped to the terminal, with headings, code
// and emphasis picked out.
//
// It works a word at a time rather than a line at a time because the text
// arrives a few characters at a time, and an answer that appears only once each
// line is complete does not look like it is being written. Everything about a
// line that changes how it is drawn -- a heading marker, a fence -- is known
// from its first word, which is why one word of lookahead is enough.
type printer struct {
	w      *bufio.Writer
	width  int
	colour bool

	col     int    // how far along the current line the cursor is
	started bool   // something has been written on this line
	word    []rune // the word not yet placed
	space   bool   // a space is owed before the next word
	any     bool   // anything at all has been written
	blank   bool   // the last thing written was a blank line

	heading bool // the current line is a heading
	bold    bool // inline emphasis is open across words
	mono    bool
	dim     bool // everything drawn is background: replayed history, reasoning

	code    bool            // inside a fenced code block
	codeBuf strings.Builder // the code line being gathered
	swallow bool            // dropping the rest of a line, which is a fence's language

	lead int // spaces the current line began with, which is how a list nests
	hang int // the column a wrapped line goes on from: under a list item's text

	// raw is set for the rest of a table row, which is drawn as written: a
	// row wrapped like prose is a table nobody can read down.
	raw bool
}

func newPrinter(w io.Writer, width int, colour bool) *printer {
	if width < 20 {
		width = defaultWidth
	}
	return &printer{w: bufio.NewWriter(w), width: width, colour: colour}
}

// style wraps s in an escape sequence, or returns it untouched where colour is
// off. Every styled write is closed immediately, so a line cut short by a
// cancelled turn cannot leave the terminal bold for good.
func (p *printer) style(st, s string) string {
	if !p.colour || st == "" || s == "" {
		return s
	}
	return st + s + ansiReset
}

func (p *printer) put(s string) {
	p.w.WriteString(s)
	p.any = true
}

// text draws a piece of a streamed answer.
func (p *printer) text(s string) {
	for _, r := range s {
		switch {
		case p.swallow:
			if r == '\n' {
				p.swallow = false
			}
		case p.code:
			p.codeRune(r)
		case p.raw && r != '\n':
			if r != '\r' {
				p.put(string(r))
				p.col++
			}
		case r == '\n':
			p.endLine()
			// A fence recognised by this very newline has nothing left of its
			// line to drop, and swallowing the next one would eat the first
			// line of the code block.
			p.swallow = false
		case r == ' ' || r == '\t':
			if !p.started && len(p.word) == 0 && !p.heading {
				// Indentation at the start of a line is kept: it is how a
				// list says that one item belongs under another.
				if r == '\t' {
					p.lead += 4
				} else {
					p.lead++
				}
				continue
			}
			p.flushWord()
			if p.raw {
				// The word that began a table row has just been placed, and
				// this space is the first of the row's own spacing.
				p.put(string(r))
				p.col++
				continue
			}
			if p.started {
				p.space = true
			}
		case r == '\r':
			// A model that writes CRLF should not leave a stray column behind
			// on every line it writes.
		default:
			p.word = append(p.word, r)
		}
	}
	p.w.Flush()
}

// codeRune gathers a line of a fenced block.
//
// Code is the one thing not wrapped: a line broken in the middle is no longer
// the line the model wrote, and somebody about to copy it out needs it whole.
// It is gathered a line at a time because a closing fence is only recognisable
// once its line has ended.
func (p *printer) codeRune(r rune) {
	if r != '\n' {
		if r != '\r' {
			p.codeBuf.WriteRune(r)
		}
		return
	}
	line := p.codeBuf.String()
	p.codeBuf.Reset()
	if strings.HasPrefix(strings.TrimSpace(line), fence) {
		p.code = false
		return
	}
	p.put(p.style(p.codeStyle(), line))
	p.put("\n")
	p.col, p.started, p.blank = 0, false, line == ""
}

// codeStyle is how a line of code is drawn: picked out from the prose, and
// dimmed with it when the whole is background -- a replayed conversation or
// the model's reasoning -- so that code in the history does not stand out
// brighter than the answer being written now.
func (p *printer) codeStyle() string {
	if p.dim {
		return ansiDim + ansiCyan
	}
	return ansiCyan
}

// rowStyle is how the start of a table row is drawn: plain, or dimmed with
// the rest of a background conversation.
func (p *printer) rowStyle() string {
	if p.dim {
		return ansiDim
	}
	return ""
}

// flushWord places the word that has been gathered, wrapping where it will not
// fit.
func (p *printer) flushWord() {
	if len(p.word) == 0 {
		return
	}
	word := string(p.word)
	p.word = p.word[:0]

	if !p.started {
		switch {
		case isHeadingMarker(word):
			// The marker itself is not drawn: on a terminal a heading is bold,
			// and printing the hashes as well only tells the reader which
			// markup the model happened to use.
			p.heading = true
			p.space = false
			return
		case isFence(word):
			p.code = true
			p.codeBuf.Reset()
			// The fence's line never ends through endLine -- its newline is
			// swallowed with the language -- so its indentation is dropped
			// here, rather than left to indent the prose after the block.
			p.lead, p.hang = 0, 0
			// The rest of the line names the language, which is nothing to
			// draw.
			p.swallow = true
			return
		}
		// The first word of the line: its indentation goes in front of it,
		// and a list marker sets where the item's wrapped lines go on from.
		if p.lead > 0 {
			p.put(strings.Repeat(" ", p.lead))
			p.col = p.lead
		}
		p.hang = p.lead
		if isListMarker(word) {
			p.hang = p.lead + utf8.RuneCountInString(word) + 1
		}
		if strings.HasPrefix(word, "|") {
			// A table row: drawn as written from here to the end of the line,
			// its spacing and all, however wide.
			p.raw = true
			p.put(p.style(p.rowStyle(), word))
			p.col += utf8.RuneCountInString(word)
			p.started, p.space, p.blank = true, false, false
			return
		}
	}

	text, inline := p.inline(word)
	if text == "" {
		return
	}
	n := utf8.RuneCountInString(text)
	if p.started && p.col+1+n > p.width {
		p.newline()
	}
	if p.started && p.space {
		p.put(" ")
		p.col++
	}
	p.space = false
	st := inline
	if p.heading {
		st = ansiBold + inline
	}
	if p.dim {
		st = ansiDim + st
	}
	p.put(p.style(st, text))
	p.col += n
	p.started = true
	p.blank = false
}

// inline strips the markers for emphasis and monospace, returning the style
// they ask for.
//
// The markers toggle rather than being matched inside one word, so emphasis
// spanning several words is drawn across all of them -- which is what a phrase
// wrapped in asterisks needs. A word in which a marker opens or closes is drawn
// emphasised either way, since the alternative is drawing half a word.
func (p *printer) inline(word string) (string, string) {
	bold, mono := p.bold, p.mono
	var b strings.Builder
	for i := 0; i < len(word); {
		switch {
		case strings.HasPrefix(word[i:], "**"):
			p.bold = !p.bold
			bold = bold || p.bold
			i += 2
		case word[i] == '`':
			p.mono = !p.mono
			mono = mono || p.mono
			i++
		default:
			b.WriteByte(word[i])
			i++
		}
	}
	var st string
	if bold {
		st += ansiBold
	}
	if mono {
		st += ansiCyan
	}
	return b.String(), st
}

// newline breaks the line without ending the paragraph, which is what wrapping
// is: the heading a continuation line belongs to is still the same heading, and
// a list item's continuation lines go on under its text rather than under its
// marker, which is what makes a list of long items readable as a list.
func (p *printer) newline() {
	p.put("\n")
	p.col, p.started, p.space, p.blank = 0, false, false, false
	if p.hang > 0 {
		p.put(strings.Repeat(" ", p.hang))
		p.col, p.started = p.hang, true
	}
}

// endLine ends the current line. Two in a row leave one blank line between
// paragraphs; more than that are the model's own spacing rather than the
// reader's, and are collapsed.
func (p *printer) endLine() {
	p.flushWord()
	p.lead, p.hang, p.raw = 0, 0, false
	if p.started {
		p.put("\n")
		p.col, p.started, p.space, p.blank = 0, false, false, false
		p.heading, p.bold, p.mono = false, false, false
		return
	}
	if p.any && !p.blank {
		p.put("\n")
		p.blank = true
	}
	p.heading, p.space = false, false
}

// endMessage finishes whatever was being drawn, including a code block the
// model never closed.
func (p *printer) endMessage() {
	if p.code {
		if line := p.codeBuf.String(); line != "" {
			p.put(p.style(p.codeStyle(), line))
			p.put("\n")
			p.codeBuf.Reset()
		}
		p.code = false
	}
	p.flushWord()
	if p.started {
		p.put("\n")
	}
	p.col, p.started, p.space = 0, false, false
	p.heading, p.bold, p.mono, p.swallow = false, false, false, false
	p.blank = false
	p.lead, p.hang, p.raw = 0, 0, false
	p.w.Flush()
}

// line writes one whole line in a style of its own: a label, a notice, a
// question.
//
// A notice or a question -- dim, red or bold, our own words -- has its first
// line wrapped at spaces to the pane, continuation lines indented as far as it
// was: left to the terminal, a long one is broken mid-word, and a pane
// narrower than it is the usual thing with several side by side. The lines
// after the first are left as they are, since under a question they are the
// code it is asking about. Anything else is drawn as it is, tool output above
// all, which is shown as it came.
func (p *printer) line(st, s string) {
	if p.started {
		p.put("\n")
		p.col, p.started = 0, false
	}
	if first, rest, more := strings.Cut(s, "\n"); (st == ansiDim || st == ansiRed || st == ansiBold) &&
		utf8.RuneCountInString(first) > p.width {
		for _, part := range wrapNotice(first, p.width) {
			p.put(p.style(st, part))
			p.put("\n")
		}
		if more {
			p.put(p.style(st, rest))
			p.put("\n")
		}
		p.blank = false
		p.w.Flush()
		return
	}
	p.put(p.style(st, s))
	p.put("\n")
	p.blank = s == ""
	p.w.Flush()
}

// wrapNotice breaks s at spaces into lines no wider than width where the words
// allow, each after the indentation s began with. A word wider than width --
// a path, a URL -- is left whole on a line of its own.
func wrapNotice(s string, width int) []string {
	pad := s[:len(s)-len(strings.TrimLeft(s, " "))]
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = pad + w
		case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) > width:
			lines = append(lines, cur)
			cur = pad + w
		default:
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// bare writes text with no line of its own and no newline after it, which is
// what an input prompt needs.
func (p *printer) bare(st, s string) {
	if p.started {
		p.put("\n")
		p.col, p.started = 0, false
	}
	p.put(p.style(st, s))
	p.w.Flush()
}

// blankLine leaves one empty line, and never two.
func (p *printer) blankLine() {
	if !p.any || p.blank {
		return
	}
	if p.started {
		p.put("\n")
		p.col, p.started = 0, false
	}
	p.put("\n")
	p.blank = true
	p.w.Flush()
}

// setDim draws what follows as background rather than as the answer: a
// conversation replayed from a transcript, or the model's own reasoning. Both
// are there to be glanced at, and neither should read as what was just said.
func (p *printer) setDim(on bool) { p.dim = on }

// isHeadingMarker reports whether a word is a markdown heading's hashes and
// nothing else. A hash on its own as a word in prose is rare; "#include" or
// "#1" is not, which is why the whole word has to be hashes.
func isHeadingMarker(word string) bool {
	if word == "" || len(word) > 6 {
		return false
	}
	for _, r := range word {
		if r != '#' {
			return false
		}
	}
	return true
}

// isListMarker reports whether a word opens a list item: a bullet, or a number
// followed by a full stop or a bracket.
func isListMarker(word string) bool {
	switch word {
	case "-", "*", "+", "•":
		return true
	}
	digits := strings.TrimRight(word, ".)")
	if len(digits) == 0 || len(digits) != len(word)-1 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// fence is the marker a code block opens and closes with.
const fence = "\x60\x60\x60"

// isFence reports whether a word opens or closes a fenced code block.
func isFence(word string) bool {
	return strings.HasPrefix(word, fence)
}
