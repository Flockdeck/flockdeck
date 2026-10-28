package server

import (
	"crypto/subtle"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/record"
)

// This file is what a phone may see of a structured pane's permission card and
// may answer with (docs/phone-approval-plan.md, "What the phone can answer"). It
// is pure: no socket, no preferences, no clock. The socket that carries it
// (/ws/approvals) is a later task and calls these.
//
// A phone is shown a card made here and nothing else of the agent's: the text of
// a command or the path of an edit, cleaned and tested, and fixed wording for
// everything that is not that. It answers allow once or deny, never an option id
// of its own, and the host finds the option.
//
// Allow is offered only when every one of these holds; the first that fails is
// the reason shown, and the phone then has Deny only:
//
//  1. the pane runs an adapter Flockdeck wrote (Claude, Codex);
//  2. the card is an execute or an edit;
//  3. the whole of what will run or change is shown: a command that is the one
//     the agent sent (nothing cleaned out of it, not cut), of at most 8 lines and
//     500 bytes, the size the desk shows it at; an edit whose path is whole and
//     whose diff reads as a diff of that path alone, with counts;
//  4. record.Redact changes nothing in it;
//  5. the card has no questions, no plan and no text from the agent besides that;
//  6. the card has exactly one allow-once option, called allow, and exactly one
//     reject-once option, called reject.
//
// A question and a plan are for the desk: nothing is offered for them, deny
// included.
const (
	// phoneTextLines and phoneTextBytes bound the command a phone is shown. The
	// byte bound is what the desk's card cuts a command at, so that a command the
	// phone may allow is one the desk shows whole.
	phoneTextLines = 8
	phoneTextBytes = structuredCommandCut
	// phonePathBytes is the same for the path of an edit.
	phonePathBytes = structuredCardCut
)

// The kinds of card a phone is shown.
const (
	phoneExecute = "execute"
	phoneEdit    = "edit"
	// phoneOther is any other tool: the phone is told only that it is one.
	phoneOther = "other"
)

// What the phone is told when it cannot allow. Fixed wording, one sentence, no
// text from the agent. The first six are the reasons for Deny only; the last two
// are for a card with nothing to answer.
const (
	whyAgent    = "Only Claude and Codex agents can be allowed from here. Deny, or answer on the desktop."
	whyTool     = "Only commands and file changes can be allowed from here. Deny, or answer on the desktop."
	whyLong     = "Too long to check here. Deny, or answer on the desktop."
	whySecret   = "This may hold a secret. Deny, or answer on the desktop."
	whyOdd      = "Has characters that cannot be shown. Deny, or answer on the desktop."
	whyUnclear  = "Could not be checked here. Deny, or answer on the desktop."
	whyNoAllow  = "This request has no plain Allow once. Deny, or answer on the desktop."
	whyPlan     = "Plan approval is on the desktop."
	whyQuestion = "Questions are answered on the desktop."
	whyDeskOnly = "This request is answered on the desktop."
)

// phoneCard is what a phone is shown of a pending card and what it may do.
type phoneCard struct {
	// Answerable is false for a card with nothing the phone may answer: a plan,
	// a question, or a card with no plain Reject. The phone is told Why and
	// nothing else.
	Answerable bool
	// Kind is phoneExecute, phoneEdit or phoneOther.
	Kind string
	// Text is the command, or the path of an edit; empty for phoneOther. It
	// holds only characters phoneRuneOK allows, at most phoneTextLines lines and
	// phoneTextBytes bytes. It is the card's text unchanged whenever CanAllow.
	Text string
	// Added and Removed are the lines an edit adds and removes.
	Added, Removed int
	// CanAllow says the phone may be offered Allow once.
	CanAllow bool
	// Why is the fixed sentence for a card the phone cannot allow; empty when it
	// can.
	Why string
}

// projectCard is the card as a phone sees it. own says the pane runs an adapter
// Flockdeck wrote (StructuredPane.OwnAdapter). A nil card is shown nothing.
func projectCard(c *bridge.Card, own bool) phoneCard {
	if c == nil {
		return phoneCard{}
	}
	switch {
	case len(c.Questions) > 0 || c.Kind == bridge.QuestionKind:
		return phoneCard{Why: whyQuestion}
	case c.Kind == "switch_mode":
		return phoneCard{Why: whyPlan}
	}
	if _, ok := optionFor(c, decisionDeny); !ok {
		return phoneCard{Why: whyDeskOnly}
	}
	out := phoneCard{Answerable: true, Kind: phoneOther}
	why := ""
	switch c.Kind {
	case phoneExecute:
		out.Kind = phoneExecute
		out.Text, why = projectCommand(c)
	case phoneEdit:
		out.Kind = phoneEdit
		out.Text, out.Added, out.Removed, why = projectEdit(c)
	default:
		why = whyTool
	}
	if why == "" && c.Body != "" {
		why = whyUnclear
	}
	if why == "" {
		if _, ok := optionFor(c, decisionAllow); !ok {
			why = whyNoAllow
		}
	}
	if !own {
		// Whatever else is wrong, the reason is the agent: the others would be
		// beside the point on a card that could not be allowed anyway.
		why = whyAgent
	}
	out.Why, out.CanAllow = why, why == ""
	return out
}

// projectCommand is the text shown for an execute card and the reason it cannot
// be allowed, or "".
func projectCommand(c *bridge.Card) (text, why string) {
	raw := strings.TrimSpace(c.CommandText)
	if raw == "" {
		return "", whyUnclear
	}
	text, odd := phoneClean(raw)
	text = strings.TrimSpace(text)
	redacted := record.Redact(text)
	secret := redacted != text
	text = redacted
	long := len(text) > phoneTextBytes || strings.Count(text, "\n") >= phoneTextLines
	text = phoneClip(text, phoneTextLines, phoneTextBytes)
	switch {
	case long:
		return text, whyLong
	case secret:
		return text, whySecret
	case odd || c.CommandAltered:
		return text, whyOdd
	}
	return text, ""
}

// projectEdit is the path shown for an edit card, the lines it adds and removes,
// and the reason it cannot be allowed, or "".
func projectEdit(c *bridge.Card) (path string, added, removed int, why string) {
	raw := strings.TrimSpace(c.File)
	if raw == "" {
		return "", 0, 0, whyUnclear
	}
	path, odd := phoneClean(raw)
	path = strings.TrimSpace(path)
	if strings.ContainsAny(path, "\n\t") {
		path, odd = strings.Join(strings.Fields(path), " "), true
	}
	redacted := record.Redact(path)
	secret := redacted != path
	path = phoneClip(redacted, 1, phonePathBytes)
	long := len(redacted) > phonePathBytes
	a, r, ok := diffCounts(c.Diff, c.File)
	switch {
	case long:
		return path, 0, 0, whyLong
	case secret:
		return path, 0, 0, whySecret
	case odd:
		return path, 0, 0, whyOdd
	case !ok:
		// A diff that was cut, a note about more edits, a path that is not this
		// one, a count that does not add up.
		return path, 0, 0, whyUnclear
	}
	return path, a, r, ""
}

// phoneRuneOK says a phone may be shown r. Letters, marks, numbers, punctuation
// and symbols are, with the ASCII space, a tab and a line break. Everything that
// shows as nothing or changes how text reads is not: controls, any other space
// (a no-break space looks like the one that separates arguments and is not), the
// format characters (bidi marks and overrides, zero-width characters, tags),
// variation selectors, the fillers that draw as blanks, unassigned and private
// characters, and the replacement character.
func phoneRuneOK(r rune) bool {
	switch {
	case r == '\n', r == '\t', r == ' ':
		return true
	case r == utf8.RuneError, r < 0x20, r == 0x7f:
		return false
	case unicode.IsSpace(r), !unicode.IsGraphic(r):
		return false
	}
	switch {
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef: // variation selectors
		return false
	case r >= 0xfff9 && r <= 0xfffc: // interlinear annotation, object replacement
		return false
	case r == 0x034f, r == 0x115f, r == 0x1160, r == 0x2800, r == 0x3164, r == 0xffa0: // blank fillers
		return false
	}
	return true
}

// phoneClean is s with every character phoneRuneOK refuses taken out, and says
// whether any was.
func phoneClean(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	changed := false
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if !phoneRuneOK(r) {
			changed = true
		} else {
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String(), changed
}

// phoneClip is s cut to its first lines lines and then to at most max bytes, at
// a character. The cut is what makes a long text safe to draw; a caller that
// needs to know it was cut compares lengths.
func phoneClip(s string, lines, max int) string {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
			if n == lines {
				s = s[:i]
				break
			}
		}
	}
	if len(s) > max {
		s = s[:max]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// diffCounts reads the diff of a card as the unified diff Flockdeck builds
// (transcript.EditDiff, WriteDiff): one or more sections, each "--- a/PATH",
// "+++ b/PATH" and a hunk whose header counts the lines it holds. It returns the
// lines added and removed, and ok only when every section names path, every hunk
// holds exactly the lines its header says and nothing else is in the text. A
// line of the file that begins "+++ " is a line of a hunk, not a header, because
// the hunk is read by its counts.
func diffCounts(diff, path string) (added, removed int, ok bool) {
	if !strings.HasSuffix(diff, "\n") {
		return 0, 0, false
	}
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	for i := 0; i < len(lines); {
		if lines[i] != "--- a/"+path || i+2 >= len(lines) || lines[i+1] != "+++ b/"+path {
			return 0, 0, false
		}
		oldN, newN, ok := hunkHeader(lines[i+2])
		if !ok {
			return 0, 0, false
		}
		i += 3
		for ; oldN > 0 || newN > 0; i++ {
			if i >= len(lines) || lines[i] == "" {
				return 0, 0, false
			}
			switch lines[i][0] {
			case ' ':
				oldN, newN = oldN-1, newN-1
			case '-':
				oldN, removed = oldN-1, removed+1
			case '+':
				newN, added = newN-1, added+1
			default:
				return 0, 0, false
			}
			if oldN < 0 || newN < 0 {
				return 0, 0, false
			}
		}
	}
	return added, removed, true
}

// hunkHeader reads "@@ -A[,B] +C[,D] @@" and returns B and D, the lines the hunk
// holds on each side. A side with no count holds one line.
func hunkHeader(l string) (oldN, newN int, ok bool) {
	f := strings.Fields(l)
	if len(f) != 4 || f[0] != "@@" || f[3] != "@@" || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return 0, 0, false
	}
	count := func(s string) (int, bool) {
		start, n, found := strings.Cut(s, ",")
		if _, err := strconv.ParseUint(start, 10, 31); err != nil {
			return 0, false
		}
		if !found {
			return 1, true
		}
		c, err := strconv.ParseUint(n, 10, 31)
		return int(c), err == nil
	}
	if oldN, ok = count(f[1][1:]); !ok {
		return 0, 0, false
	}
	if newN, ok = count(f[2][1:]); !ok {
		return 0, 0, false
	}
	return oldN, newN, true
}

// The two answers a phone sends.
const (
	decisionAllow = "allow"
	decisionDeny  = "deny"
)

// The refusals of phoneAnswer. Each is the whole of what the phone is told.
var (
	errPhoneDecision  = errors.New("The answer must be allow or deny")
	errPhoneNoCard    = errors.New("That pane is not waiting for an answer")
	errPhoneStale     = errors.New("That request has already been answered or has ended")
	errPhoneStaleRun  = errors.New("That pane has restarted since the request was shown")
	errPhoneLock      = errors.New("The answer needs a card number, a request id and a pane token")
	errPhoneDeskOnly  = errors.New("That request is answered on the desktop")
	errPhoneNoAllow   = errors.New("That request cannot be allowed from here")
	errPhoneNoOptions = errors.New("That request has no single option for that answer")
)

// optionFor is the id, as the card shows it, of the one option the phone's
// decision means, or false. Allow is the option with id "allow" and kind
// "allow_once"; deny is "reject" and "reject_once". There must be exactly one
// option that has either the id or the kind, and it must have both: a second
// allow-once option, a lookalike id on another kind, or a card with neither is
// refused rather than guessed at. allow_always is never chosen, and its being on
// the card changes nothing.
func optionFor(c *bridge.Card, decision string) (string, bool) {
	var id, kind string
	switch decision {
	case decisionAllow:
		id, kind = "allow", "allow_once"
	case decisionDeny:
		id, kind = "reject", "reject_once"
	default:
		return "", false
	}
	found := ""
	n := 0
	for _, o := range c.Options {
		if o.OptionID != id && o.Kind != kind {
			continue
		}
		n++
		if o.OptionID == id && o.Kind == kind {
			found = o.OptionID
		}
	}
	if n != 1 || found == "" {
		return "", false
	}
	return found, true
}

// approvalLock names the card a phone was shown and the run of the pane it was
// shown for. A decision is let through only against the same card of the same
// run: the card's number (Flockdeck's own, unique across panes and restarts of
// one run of the program), the agent's request id (which an agent can reuse for
// a different request), and the runtime's token (which changes when the pane
// restarts or is rewound). The pane's own AnswerPermission checks the first two
// again under its lock, so that two answers cannot both win; this is the check
// that needs no lock and refuses early.
type approvalLock struct {
	Seq       uint64
	RequestID string
	Token     string
}

// lockFor is the lock for a card shown now. It is false for no card, and for a
// card or token missing a part, since a lock with a part missing would match
// anything that is missing the same part.
func lockFor(c *bridge.Card, token string) (approvalLock, bool) {
	if c == nil || c.Seq == 0 || c.RequestID == "" || token == "" {
		return approvalLock{}, false
	}
	return approvalLock{Seq: c.Seq, RequestID: c.RequestID, Token: token}, true
}

// check says whether l still names the card c is pending now, under the runtime
// token now.
func (l approvalLock) check(c *bridge.Card, token string) error {
	switch {
	case l.Seq == 0 || l.RequestID == "" || l.Token == "":
		return errPhoneLock
	case token == "" || subtle.ConstantTimeCompare([]byte(l.Token), []byte(token)) != 1:
		return errPhoneStaleRun
	case c == nil:
		return errPhoneNoCard
	case c.Seq != l.Seq || subtle.ConstantTimeCompare([]byte(l.RequestID), []byte(c.RequestID)) != 1:
		return errPhoneStale
	}
	return nil
}

// phoneAnswer is the option id to give the pane for a phone's decision, or the
// reason it is refused. c and token are what the pane has now; own is its
// OwnAdapter. It refuses, in this order: a decision that is not allow or deny; a
// lock that is not for this card of this run; a card the phone may not answer;
// an allow the projection does not offer; and a card with no single option for
// the decision. The caller then gives AnswerPermission the lock's Seq and
// RequestID with the option id, and that call decides between two answers.
func phoneAnswer(c *bridge.Card, token string, own bool, l approvalLock, decision string) (string, error) {
	if decision != decisionAllow && decision != decisionDeny {
		return "", errPhoneDecision
	}
	if err := l.check(c, token); err != nil {
		return "", err
	}
	p := projectCard(c, own)
	switch {
	case !p.Answerable:
		return "", errPhoneDeskOnly
	case decision == decisionAllow && !p.CanAllow:
		return "", errPhoneNoAllow
	}
	id, ok := optionFor(c, decision)
	if !ok {
		return "", errPhoneNoOptions
	}
	return id, nil
}
