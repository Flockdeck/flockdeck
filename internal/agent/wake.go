package agent

import (
	"slices"
	"strings"
)

// WakeVerdict is what a WakeRule says about the tail of a pane's output.
//
// The zero value is WakeUnknown on purpose: a verdict nobody filled in must
// never read as permission to type into a pane.
type WakeVerdict int

const (
	// WakeUnknown means there is no rule to judge by, or the rule is not yet
	// trusted (see WakeRule.Tested). Nothing is typed. The mail waits, and the
	// graph shows it as waiting with the amber arrow.
	WakeUnknown WakeVerdict = iota
	// WakeNo means the tail shows something other than an idle, empty prompt:
	// output in progress, a draft in the input, a question, an editor, a
	// login.
	WakeNo
	// WakeOK means the tail ends in an idle, empty input prompt of the shape
	// the rule describes and nothing in the tail forbids typing. It is one
	// gate of several, never the only one: the wake service also requires the
	// agent's hooks to say it is idle, a quiet period measured against
	// ScreenTail's ChangedAt, and no typing by the user (Session.LastUserInput).
	WakeOK
)

func (v WakeVerdict) String() string {
	switch v {
	case WakeNo:
		return "no"
	case WakeOK:
		return "ok"
	}
	return "unknown"
}

// Where a rule came from, for the audit record of a wake.
const (
	// RuleBuiltIn is a rule shipped in the catalog, unchanged by the user.
	RuleBuiltIn = "built-in"
	// RuleEdited is a built-in rule the user's agents.json changed.
	RuleEdited = "built-in, edited"
	// RuleUser is a rule written in agents.json for an agent of the user's.
	RuleUser = "user"
)

// WakeRule says how to tell, from the tail of an agent's raw output, that it
// is sitting at an empty prompt and a line typed there would be read as a new
// message. It is data, edited in agents.json like Patterns, and for the same
// reason it holds plain text and not regular expressions: see containsAny in
// internal/session.
//
// The tail is not the live screen. It is the last of the bytes the pane
// printed, kept in a ring and cleaned of escape sequences, with no cursor
// movement applied: a program that repaints in place, or that draws over text
// already on screen, leaves a tail that can differ from what the user sees. A
// rule can say a pane is not safe to type into; it can only say it looks
// safe. That is why no rule is trusted until a fixture taken from a real
// recording passes, and why Tested stays false until then.
type WakeRule struct {
	// Version names the build of the agent the rule was written against. It
	// goes into the audit record of every wake, so a wake can be traced to the
	// rule that allowed it.
	Version string `json:"version,omitempty"`
	// Tested is set once a fixture taken from a real recording of the agent
	// passes. A rule that is not tested never allows a wake by its screen reading:
	// Judge turns its WakeOK into WakeUnknown. (A Claude pane is woken on the word
	// in Claude's session file instead, which does not use this.) It can still say WakeNo, which is the safe way
	// to be wrong. Merge takes it only from the catalog's own rule: an entry
	// in agents.json cannot mark a built-in rule tested, because the file is
	// not what proves a rule.
	//
	// Only a rule whose Source is RuleBuiltIn can allow a wake at all. A
	// user's rule, and a built-in one the user edited, say WakeUnknown whatever
	// they carry here, until an explicit setting in the app allows them. The
	// gate is otherwise only as strong as who can write agents.json.
	Tested bool `json:"tested,omitempty"`
	// Prompts are the idle input prompt with nothing typed after it. A line
	// matches when, with the vertical edges of a box and ordinary spaces
	// removed, it is one of these exactly (ignoring case). Any other
	// character after the prompt, a tab or a no-break space included, makes
	// it a draft or something else.
	Prompts []string `json:"prompts,omitempty"`
	// Footers are lines an agent draws under its input box, such as a hint
	// about shortcuts. They are skipped, with blank lines, when looking for
	// the prompt line from the end.
	Footers []string `json:"footers,omitempty"`
	// Boxed says the prompt is drawn between two lines of box border (rule
	// lines of box-drawing characters), and is judged only in that shape. A
	// ">" at the end of a line of output, or a row of a table, is then not a
	// prompt.
	Boxed bool `json:"boxed,omitempty"`
	// RequireFooter says one of Footers must be present under the prompt.
	RequireFooter bool `json:"requireFooter,omitempty"`
	// StatusLine says the prompt is drawn under a status line that has a
	// blank line above it. Output that merely ends
	// in the prompt text is then not a prompt.
	StatusLine bool `json:"statusLine,omitempty"`
	// Forbid are strings that stop a wake if the tail contains one: a
	// question, a confirmation, an editor, a trust or login screen, or the
	// hint an agent shows while it is working. The tail is joined into one
	// line first, so a phrase the terminal wrapped is still found. An entry in
	// agents.json adds to the built-in list; it cannot remove from it.
	Forbid []string `json:"forbid,omitempty"`

	// Source says where the rule came from, for the audit record. It is set
	// by the catalog and never read from a file.
	Source string `json:"-"`
}

// clone returns a copy that shares no slices with r, so that decoding a user's
// entry over a built-in cannot reach into the built-in.
func (r *WakeRule) clone() *WakeRule {
	if r == nil {
		return nil
	}
	c := *r
	c.Prompts = slices.Clone(r.Prompts)
	c.Footers = slices.Clone(r.Footers)
	c.Forbid = slices.Clone(r.Forbid)
	return &c
}

// sameShape reports whether two rules read a tail the same way, ignoring Tested
// and Source.
func (r *WakeRule) sameShape(o *WakeRule) bool {
	if r == nil || o == nil {
		return r == o
	}
	a, b := *r, *o
	a.Tested, b.Tested, a.Source, b.Source = false, false, "", ""
	return a.Version == b.Version && a.Boxed == b.Boxed && a.RequireFooter == b.RequireFooter &&
		a.StatusLine == b.StatusLine && slices.Equal(a.Prompts, b.Prompts) &&
		slices.Equal(a.Footers, b.Footers) && slices.Equal(a.Forbid, b.Forbid)
}

// Forbidden reports whether the tail holds one of the rule's forbidden strings: a
// question, a confirmation, an editor, a trust or login screen, or the hint an
// agent shows while it works. It is the one part of the rule that says "not now"
// without needing the screen to look like a prompt, and a nil rule forbids nothing.
func (r *WakeRule) Forbidden(tail []string) bool {
	if r == nil {
		return false
	}
	lines := make([]string, len(tail))
	for i, l := range tail {
		lines[i] = strings.ToLower(l)
	}
	joined := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
	return containsAnyOf(joined, foldAll(r.Forbid))
}

// Judge reads the tail of a pane's output, oldest line first with escape
// sequences already removed, and says whether a line may be typed. When the
// verdict is WakeOK the second result is the prompt line that was matched, as
// it appeared in the tail.
//
// A nil rule, or one with no prompts, is WakeUnknown. So is any rule that is
// untested or whose Source is not RuleBuiltIn: see Tested.
func (r *WakeRule) Judge(tail []string) (WakeVerdict, string) {
	v, line := r.judge(tail)
	if v == WakeOK && (!r.Tested || r.Source != RuleBuiltIn) {
		return WakeUnknown, ""
	}
	return v, line
}

// judge is Judge without the Tested gate, so that the engine can be tested
// before any rule is.
func (r *WakeRule) judge(tail []string) (WakeVerdict, string) {
	prompts := foldAll(r.promptsOrNil())
	if r == nil || len(prompts) == 0 {
		return WakeUnknown, ""
	}
	lines := make([]string, len(tail))
	for i, l := range tail {
		lines[i] = strings.ToLower(l)
	}
	if blank(lines) {
		return WakeNo, ""
	}

	// The forbidden strings are looked for in the whole tail with line breaks
	// folded to spaces, because a terminal wraps a long question wherever the
	// window ends.
	joined := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
	if containsAnyOf(joined, foldAll(r.Forbid)) {
		return WakeNo, ""
	}

	footers := foldAll(r.Footers)
	i, sawFooter := len(lines)-1, false
	for ; i >= 0; i-- {
		core := unboxed(lines[i])
		if core == "" {
			continue
		}
		if slices.Contains(footers, core) {
			sawFooter = true
			continue
		}
		break
	}
	if i < 0 || (r.RequireFooter && !sawFooter) {
		return WakeNo, ""
	}
	if r.Boxed {
		// The bottom border, then the prompt, then the top border.
		if !borderOnly(lines[i]) {
			return WakeNo, ""
		}
		i--
		if i < 1 || !borderOnly(lines[i-1]) {
			return WakeNo, ""
		}
	}
	core := unboxed(lines[i])
	if !slices.Contains(prompts, core) {
		return WakeNo, ""
	}
	if r.StatusLine {
		if i < 2 || strings.TrimSpace(lines[i-1]) == "" || strings.TrimSpace(lines[i-2]) != "" {
			return WakeNo, ""
		}
	}
	return WakeOK, strings.TrimSpace(tail[i])
}

func (r *WakeRule) promptsOrNil() []string {
	if r == nil {
		return nil
	}
	return r.Prompts
}

func blank(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}

// foldAll trims and lower-cases patterns and drops the empty ones. An empty
// forbid string would match every line and an empty prompt every blank one, so
// an entry left empty by a typo must not count.
func foldAll(pats []string) []string {
	var out []string
	for _, p := range pats {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containsAnyOf(lowered string, pats []string) bool {
	for _, p := range pats {
		if strings.Contains(lowered, p) {
			return true
		}
	}
	return false
}

// boxEdge are the vertical border characters of a drawn input box. The ASCII
// bar is not one: a table row in the agent's output would pass for a box.
const boxEdge = "│┃║"

// unboxed trims ordinary spaces and the vertical edges of a box off both ends
// of a line. Other white space, such as a tab or a no-break space, is left,
// so a line that has any is not a bare prompt.
func unboxed(l string) string {
	l = strings.Trim(l, " ")
	l = strings.Trim(l, boxEdge)
	return strings.Trim(l, " ")
}

// borderOnly reports a line made of nothing but box-drawing characters and
// space, such as the rule above and below an input box.
func borderOnly(l string) bool {
	seen := false
	for _, r := range l {
		switch {
		case r == ' ':
		case r >= 0x2500 && r <= 0x257f:
			seen = true
		default:
			return false
		}
	}
	return seen
}

// The strings in Forbid are checked against the last session.ScreenTailLines (12)
// lines of the pane, so a dialog taller than that is seen by its footer, which is
// what the footer strings are for. "would you like", "enter to select" and "to
// navigate" were found in the strings of Claude Code 2.1.310; "ready to proceed"
// and "esc to exit" were not, and are not listed.
// claudeWake is the rule for Claude Code. It is written from the documented
// shape of its input, an empty prompt between two border lines with a hint
// line under it, and not from a recording, so it is not tested and allows no
// wake by itself. A Claude pane is woken through the word in Claude's own
// session file (workspace/wake.go), and this rule contributes only its Forbid
// list, which stops a line being typed into a question or a dialog.
func claudeWake() *WakeRule {
	return &WakeRule{
		Version:       "claude-code-untested-1",
		Source:        RuleBuiltIn,
		Tested:        false,
		Prompts:       []string{">", "❯"},
		Footers:       []string{"? for shortcuts"},
		Boxed:         true,
		RequireFooter: true,
		Forbid: []string{
			"esc to interrupt", // the hint shown while it works
			"do you want", "would you like", "(y/n)", "enter to confirm", "esc to cancel",
			"enter to select", "to navigate", // footers of Claude Code's own pickers and dialogs
			"do you trust", "trust this folder",
			"select login method", "press enter to continue",
			"ctrl+g to edit", "-- insert --",
		},
	}
}
