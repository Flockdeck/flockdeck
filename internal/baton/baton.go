// Package baton is the handoff from one agent to the next: a short markdown
// document with fixed sections, built from what git and the transcript say
// happened, edited by a person or not at all, scrubbed of secrets, and given to
// a new agent as its opening prompt.
//
// It has no idea of panes or windows. The workspace gathers the facts, this
// package turns them into a Baton and a Baton into a prompt, and the workspace
// starts the agent. Nothing here starts a process or talks to an agent.
package baton

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Section is one of the fixed headed sections of a baton.
type Section string

const (
	Goal          Section = "Goal"
	Standing      Section = "Where things stand"
	Decisions     Section = "Decisions and why"
	Files         Section = "Files touched"
	Commands      Section = "Commands and tests run"
	Questions     Section = "Open questions"
	Constraints   Section = "Constraints"
	NotInCheckout Section = "Not in your checkout"
)

// Sections is every section, in the order a baton writes them.
var Sections = []Section{Goal, Standing, Decisions, Files, Commands, Questions, Constraints, NotInCheckout}

// Placeholder is what a section the builder cannot fill holds until a person
// writes it. Frame leaves a section that still holds it out of the prompt.
const Placeholder = "_Not filled in._"

// Redaction counts what the scrubber removed of one kind.
type Redaction struct {
	Kind  string
	Count int
}

// Baton is a handoff document.
type Baton struct {
	// ID names the baton on disk and on the command line. See NewID.
	ID    string
	Title string
	// FromPane, FromAgent and FromModel say where it was made. FromPane is the
	// pane's id and may be empty for a baton written by hand.
	FromPane, FromAgent, FromModel string
	// Cwd and Branch are the checkout it was made from, and BaseCommit the
	// commit that checkout was at.
	Cwd, Branch, BaseCommit string
	Created                 time.Time
	// Derived lists the ids of the batons this one was made from or edited
	// from, oldest first, so a chain can be followed.
	Derived []string
	// Sections holds each section's text. A section not in the map is empty.
	Sections map[Section]string
	// Redactions is what the scrubber removed from the text, by kind. It is
	// recomputed from the text by Scrub, not trusted from disk.
	Redactions []Redaction
}

// NewID makes an id: the time to the second, so ids sort by age, and a random
// suffix so two made in one second differ. It is safe as a file name.
func NewID(now time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

var idRe = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`)

// ValidID reports whether s has the shape NewID makes. A reference that does
// not is a pane id or a path to somebody else's file, never a name to join to
// the baton folder.
func ValidID(s string) bool { return idRe.MatchString(s) }

// Section returns one section's text, trimmed.
func (b Baton) Section(s Section) string { return strings.TrimSpace(b.Sections[s]) }

// Set returns a copy of b with one section's text replaced.
func (b Baton) Set(s Section, text string) Baton {
	m := make(map[Section]string, len(b.Sections)+1)
	for k, v := range b.Sections {
		m[k] = v
	}
	m[s] = text
	b.Sections = m
	return b
}

// Fork returns a copy of b under a new id that records b as what it was made
// from. A baton is not changed once something has started from it, so an edit
// is a new baton.
func (b Baton) Fork(now time.Time) Baton {
	out := b.Set(Goal, b.Sections[Goal])
	out.ID = NewID(now)
	out.Created = now
	out.Derived = append(append([]string(nil), b.Derived...), b.ID)
	return out
}

// filled reports whether a section has anything in it a reader needs.
func filled(text string) bool {
	t := strings.TrimSpace(text)
	return t != "" && t != Placeholder
}

// heading is the line that opens a section.
func heading(s Section) string { return "## " + string(s) }

// knownHeading maps a heading line to its section.
func knownHeading(line string) (Section, bool) {
	line = strings.TrimRight(line, " \t\r")
	for _, s := range Sections {
		if line == heading(s) {
			return s, true
		}
	}
	return "", false
}

// tagRe finds the start of the tag that fences a baton in a prompt, or the end
// of it.
// Spaces and line breaks around the slash and the name are allowed, since a
// reader that is not strict about them would take "< /baton>" for the tag.
var tagRe = regexp.MustCompile(`(?i)<(\s*/?\s*baton)`)
var escapedTagRe = regexp.MustCompile(`(?i)&lt;(\s*/?\s*baton)`)

// escapeTags keeps text from opening or closing the <baton> fence a baton is
// put in. Text that came out of a file, a web page or a command can say
// anything, including that the baton has ended and an instruction follows.
func escapeTags(s string) string { return tagRe.ReplaceAllString(s, "&lt;$1") }

// escapeBody keeps a line of a section's text that looks like a section
// heading from being read as one, and the text from naming the baton fence.
func escapeBody(text string) string {
	text = escapeTags(text)
	if !strings.Contains(text, "## ") {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if _, ok := knownHeading(l); ok {
			lines[i] = `\` + l
		}
	}
	return strings.Join(lines, "\n")
}

func unescapeBody(text string) string {
	text = escapedTagRe.ReplaceAllString(text, "<$1")
	if !strings.Contains(text, `\## `) {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if rest, ok := strings.CutPrefix(l, `\`); ok {
			if _, known := knownHeading(rest); known {
				lines[i] = rest
			}
		}
	}
	return strings.Join(lines, "\n")
}

// oneLine collapses a value for the front matter, which is one line a key.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Render writes a baton as markdown: a front matter block, then the title and
// each section that has text. It is what is stored and what the editor shows,
// and Parse reads it back.
func Render(b Baton) string {
	var w strings.Builder
	w.WriteString("---\n")
	kv := func(k, v string) {
		if v = oneLine(v); v != "" {
			fmt.Fprintf(&w, "%s: %s\n", k, v)
		}
	}
	kv("id", b.ID)
	if !b.Created.IsZero() {
		kv("created", b.Created.UTC().Format(time.RFC3339))
	}
	kv("pane", b.FromPane)
	kv("agent", b.FromAgent)
	kv("model", b.FromModel)
	kv("cwd", b.Cwd)
	kv("branch", b.Branch)
	kv("base", b.BaseCommit)
	kv("derived", strings.Join(b.Derived, ", "))
	if len(b.Redactions) > 0 {
		parts := make([]string, 0, len(b.Redactions))
		for _, r := range b.Redactions {
			parts = append(parts, r.Kind+"="+strconv.Itoa(r.Count))
		}
		kv("redacted", strings.Join(parts, ", "))
	}
	w.WriteString("---\n\n")
	title := oneLine(b.Title)
	if title == "" {
		title = "Untitled"
	}
	fmt.Fprintf(&w, "# Baton: %s\n", title)
	for _, s := range Sections {
		text := strings.TrimSpace(b.Sections[s])
		if text == "" {
			continue
		}
		fmt.Fprintf(&w, "\n%s\n\n%s\n", heading(s), escapeBody(text))
	}
	return w.String()
}

// ErrNotBaton is what Parse returns for text with no baton front matter.
var ErrNotBaton = errors.New("not a baton: no front matter")

// Parse reads what Render wrote, and what a person made of it in an editor.
// Text before the first known heading, other than the title, is dropped; a
// heading it does not know is kept as text of the section above it.
func Parse(text string) (Baton, error) {
	// Escapes and control characters are taken out here, as they are from
	// everything a baton is built from: Parse reads files anyone could have
	// written, and what it gives back is printed in a terminal.
	text = CleanText(strings.ReplaceAll(text, "\r\n", "\n"))
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return Baton{}, ErrNotBaton
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return Baton{}, ErrNotBaton
	}
	var b Baton
	for _, line := range strings.Split(front, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		// One line, whatever was in it: a line separator or a stray control
		// character is not a way to put a second line in a header value.
		v = oneLine(v)
		switch strings.TrimSpace(k) {
		case "id":
			b.ID = v
		case "created":
			b.Created, _ = time.Parse(time.RFC3339, v)
		case "pane":
			b.FromPane = v
		case "agent":
			b.FromAgent = v
		case "model":
			b.FromModel = v
		case "cwd":
			b.Cwd = v
		case "branch":
			b.Branch = v
		case "base":
			b.BaseCommit = v
		case "derived":
			for _, d := range strings.Split(v, ",") {
				if d = strings.TrimSpace(d); d != "" {
					b.Derived = append(b.Derived, d)
				}
			}
		case "redacted":
			for _, p := range strings.Split(v, ",") {
				kind, n, ok := strings.Cut(strings.TrimSpace(p), "=")
				if c, err := strconv.Atoi(n); ok && err == nil && kind != "" {
					b.Redactions = append(b.Redactions, Redaction{kind, c})
				}
			}
		}
	}
	b.Sections = map[Section]string{}
	var cur Section
	var buf []string
	flush := func() {
		if cur != "" {
			b.Sections[cur] = strings.TrimSpace(unescapeBody(strings.Join(buf, "\n")))
		}
		buf = buf[:0]
	}
	for _, line := range strings.Split(body, "\n") {
		if t, ok := strings.CutPrefix(line, "# Baton:"); ok && cur == "" {
			b.Title = oneLine(t)
			continue
		}
		if s, ok := knownHeading(line); ok {
			flush()
			cur = s
			continue
		}
		if cur != "" {
			buf = append(buf, line)
		}
	}
	flush()
	return b, nil
}

// SortRedactions orders redactions by kind, so the same text always reports
// the same list.
func SortRedactions(r []Redaction) {
	sort.Slice(r, func(i, j int) bool { return r[i].Kind < r[j].Kind })
}

// RedactionCount is the total number of values removed.
func RedactionCount(r []Redaction) int {
	n := 0
	for _, x := range r {
		n += x.Count
	}
	return n
}
