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
	// WakeOK means the last line that matters is an idle, empty input prompt
	// and nothing in the tail forbids typing. It is the only verdict that
	// lets a line be typed.
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

// WakeRule says how to tell, from the tail of an agent's raw output, that it
// is sitting at an empty prompt and a line typed there would be read as a new
// message. It is data, edited in agents.json like Patterns, and for the same
// reason it holds plain text and not regular expressions: see containsAny in
// internal/session.
//
// The tail is not the rendered screen. A rule can say a pane is not safe to
// type into; it can only say it looks safe. That is why a rule has to be
// proved against a real recording before it is trusted.
type WakeRule struct {
	// Version names the build of the agent the rule was written against. It
	// goes into the audit record of every wake, so a wake can be traced to the
	// rule that allowed it.
	Version string `json:"version,omitempty"`
	// Tested is set once a fixture taken from a real recording of the agent
	// passes. A rule that is not tested never allows a wake: Judge turns its
	// WakeOK into WakeUnknown. It can still say WakeNo, which is the safe way
	// to be wrong.
	Tested bool `json:"tested,omitempty"`
	// Prompts are the idle input prompt with nothing typed after it. A line
	// matches when, with any box-drawing border and surrounding space
	// removed, it is one of these exactly (ignoring case). A line with text
	// after the prompt is a draft, and does not match.
	Prompts []string `json:"prompts,omitempty"`
	// Footers are lines an agent draws under its input box, such as a hint
	// about shortcuts. They are skipped, together with blank lines and lines
	// of box border, when looking for the prompt line from the end.
	Footers []string `json:"footers,omitempty"`
	// Forbid are strings that stop a wake if any line in the tail contains
	// one: a question, a confirmation, an editor, a trust or login screen, or
	// the hint an agent shows while it is working.
	Forbid []string `json:"forbid,omitempty"`
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

// Judge reads the tail of a pane's output, oldest line first with escape
// sequences already removed, and says whether a line may be typed. When the
// verdict is WakeOK the second result is the prompt line that was matched, as
// it appeared in the tail.
//
// A nil rule, or one with no prompts, is WakeUnknown.
func (r *WakeRule) Judge(tail []string) (WakeVerdict, string) {
	v, line := r.judge(tail)
	if v == WakeOK && !r.Tested {
		return WakeUnknown, ""
	}
	return v, line
}

// judge is Judge without the Tested gate, so that the engine can be tested
// before any rule is.
func (r *WakeRule) judge(tail []string) (WakeVerdict, string) {
	if r == nil || len(foldAll(r.Prompts)) == 0 {
		return WakeUnknown, ""
	}
	lines := make([]string, len(tail))
	for i, l := range tail {
		lines[i] = strings.ToLower(l)
	}
	if blank(lines) {
		return WakeNo, ""
	}
	forbid := foldAll(r.Forbid)
	for _, l := range lines {
		if containsAnyOf(l, forbid) {
			return WakeNo, ""
		}
	}
	footers, prompts := foldAll(r.Footers), foldAll(r.Prompts)
	for i := len(lines) - 1; i >= 0; i-- {
		core := unboxed(lines[i])
		if core == "" || borderOnly(lines[i]) || slices.Contains(footers, core) {
			continue
		}
		if slices.Contains(prompts, core) {
			return WakeOK, strings.TrimSpace(tail[i])
		}
		return WakeNo, ""
	}
	return WakeNo, ""
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

// boxEdge are the vertical border characters of an input box.
const boxEdge = "│┃║|"

// unboxed trims space and the vertical edges of a box off both ends of a line.
func unboxed(l string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(l), boxEdge+" \t"))
}

// borderOnly reports a line made of nothing but box-drawing characters and
// space, such as the rule above and below an input box.
func borderOnly(l string) bool {
	seen := false
	for _, r := range l {
		switch {
		case r == ' ' || r == '\t':
		case r >= 0x2500 && r <= 0x257f:
			seen = true
		default:
			return false
		}
	}
	return seen
}

// claudeWake is the rule for Claude Code. It is written from the documented
// shape of its input, an empty prompt in a box with a hint line under it, and
// not from a recording, so it is not yet tested and wakes nothing.
func claudeWake() *WakeRule {
	return &WakeRule{
		Version: "claude-code-untested-1",
		Tested:  false,
		Prompts: []string{">", "❯"},
		Footers: []string{"? for shortcuts"},
		Forbid: []string{
			"esc to interrupt", // the hint shown while it works
			"do you want", "(y/n)", "enter to confirm", "esc to cancel",
			"do you trust", "trust this folder",
			"select login method", "press enter to continue",
			"ctrl+g to edit", "-- insert --",
		},
	}
}

// chatWake is the rule for `flockdeck chat`, whose prompt Flockdeck controls:
// "you > " when idle and empty, "   … " for a continuation line, and "  > "
// under a tool's question, which the question's own text forbids.
func chatWake() *WakeRule {
	return &WakeRule{
		Version: "flockdeck-chat-untested-1",
		Tested:  false,
		Prompts: []string{"you >"},
		Forbid:  []string{"[y] yes", "[n] no (enter)", "or type what to do instead"},
	}
}
