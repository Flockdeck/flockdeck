package server

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// The options of the two Flockdeck adapters: a shell command also offers allow
// always.
var (
	optAllow       = acp.PermissionOption{OptionID: "allow", Name: "Allow", Kind: "allow_once"}
	optAllowAlways = acp.PermissionOption{OptionID: "allow_always", Name: "Always", Kind: "allow_always"}
	optReject      = acp.PermissionOption{OptionID: "reject", Name: "Reject", Kind: "reject_once"}
	plainOptions   = []acp.PermissionOption{optAllow, optReject}
)

func execCard(command string) *bridge.Card {
	return &bridge.Card{Seq: 7, RequestID: "req-1", Kind: "execute", Tool: "Bash", Command: command, CommandText: command,
		Options: []acp.PermissionOption{optAllow, optAllowAlways, optReject}}
}

func editCard(path, diff string) *bridge.Card {
	return &bridge.Card{Seq: 8, RequestID: "req-2", Kind: "edit", Tool: "Edit", File: path, Diff: diff, Options: plainOptions}
}

func with(c *bridge.Card, f func(*bridge.Card)) *bridge.Card { f(c); return c }

func withOptions(c *bridge.Card, o ...acp.PermissionOption) *bridge.Card {
	c.Options = o
	return c
}

func echoLines(n int) string {
	var s []string
	for i := 0; i < n; i++ {
		s = append(s, "echo "+string(rune('a'+i)))
	}
	return strings.Join(s, "\n")
}

// A token the redactor takes out; the test after it checks that it does.
const fakeToken = "ghp_0123456789abcdefghijklmnopqrstuvwxyz"

func TestTheFakeTokenIsOneRedactTakesOut(t *testing.T) {
	if record.Redact("echo "+fakeToken) == "echo "+fakeToken {
		t.Fatal("record.Redact keeps the test token; the secret cases below test nothing")
	}
}

type phoneWant struct {
	answerable, canAllow bool
	kind, text, why      string
	added, removed       int
}

func (p phoneCard) want() phoneWant {
	return phoneWant{p.Answerable, p.CanAllow, p.Kind, p.Text, p.Why, p.Added, p.Removed}
}

func TestProjectCard(t *testing.T) {
	small := transcript.EditDiff("/p/a.go", "one\ntwo\nthree", "one\n2\nthree\nfour")
	write := transcript.WriteDiff("/p/new.txt", "a\nb\nc")
	multi := small + transcript.EditDiff("/p/a.go", "x", "y")
	long300 := "/" + strings.Repeat("d", 299)
	long301 := long300 + "d"
	tool := func(kind string) *bridge.Card {
		return &bridge.Card{Seq: 1, RequestID: "r", Kind: kind, File: "/p/a", Options: plainOptions}
	}

	cases := []struct {
		name string
		card *bridge.Card
		own  bool
		want phoneWant
	}{
		{"nothing", nil, true, phoneWant{}},

		{"a command", execCard("go test ./..."), true, phoneWant{true, true, "execute", "go test ./...", "", 0, 0}},
		{"a command on another agent", execCard("go test ./..."), false, phoneWant{true, false, "execute", "go test ./...", whyAgent, 0, 0}},
		{"unicode", execCard("echo 日本語 é ☃"), true, phoneWant{true, true, "execute", "echo 日本語 é ☃", "", 0, 0}},
		{"padding is not the command", execCard("  ls  \n"), true, phoneWant{true, true, "execute", "ls", "", 0, 0}},
		{"three lines", execCard(echoLines(3)), true, phoneWant{true, true, "execute", echoLines(3), "", 0, 0}},
		{"eight lines", execCard(echoLines(8)), true, phoneWant{true, true, "execute", echoLines(8), "", 0, 0}},
		{"nine lines", execCard(echoLines(9)), true, phoneWant{true, false, "execute", echoLines(8), whyLong, 0, 0}},
		{"500 bytes", execCard(strings.Repeat("a", 500)), true, phoneWant{true, true, "execute", strings.Repeat("a", 500), "", 0, 0}},
		{"501 bytes", execCard(strings.Repeat("a", 501)), true, phoneWant{true, false, "execute", strings.Repeat("a", 500), whyLong, 0, 0}},
		{"cut or cleaned by the bridge", with(execCard("ls"), func(c *bridge.Card) { c.CommandAltered = true }), true, phoneWant{true, false, "execute", "ls", whyOdd, 0, 0}},
		{"only the first line is known", with(execCard("ls"), func(c *bridge.Card) { c.CommandText = "" }), true, phoneWant{true, false, "execute", "", whyUnclear, 0, 0}},
		{"no command", execCard(""), true, phoneWant{true, false, "execute", "", whyUnclear, 0, 0}},
		{"a secret", execCard("curl -H 'X: " + fakeToken + "' x"), true, phoneWant{true, false, "execute", "curl -H 'X: [redacted]' x", whySecret, 0, 0}},
		{"a secret on another agent", execCard("curl " + fakeToken), false, phoneWant{true, false, "execute", "curl [redacted]", whyAgent, 0, 0}},
		{"bidi override", execCard("echo ‮gpj.txt"), true, phoneWant{true, false, "execute", "echo gpj.txt", whyOdd, 0, 0}},
		{"bidi isolate", execCard("a⁦b⁩"), true, phoneWant{true, false, "execute", "ab", whyOdd, 0, 0}},
		{"zero width space", execCard("ec​ho"), true, phoneWant{true, false, "execute", "echo", whyOdd, 0, 0}},
		{"no-break space", execCard("rm -rf x"), true, phoneWant{true, false, "execute", "rm-rf x", whyOdd, 0, 0}},
		{"escape", execCard("echo \x1b[2Jhi"), true, phoneWant{true, false, "execute", "echo [2Jhi", whyOdd, 0, 0}},
		{"nul and bell", execCard("a\x00b\x07"), true, phoneWant{true, false, "execute", "ab", whyOdd, 0, 0}},
		{"carriage return", execCard("echo a\r\necho b"), true, phoneWant{true, false, "execute", "echo a\necho b", whyOdd, 0, 0}},
		{"not utf-8", execCard("echo \xff\xfe"), true, phoneWant{true, false, "execute", "echo", whyOdd, 0, 0}},
		{"replacement character", execCard("echo �"), true, phoneWant{true, false, "execute", "echo", whyOdd, 0, 0}},
		{"variation selector", execCard("echo a️"), true, phoneWant{true, false, "execute", "echo a", whyOdd, 0, 0}},
		{"tag characters", execCard("echo \U000e0041"), true, phoneWant{true, false, "execute", "echo", whyOdd, 0, 0}},
		{"blank filler", execCard("echo ㅤ"), true, phoneWant{true, false, "execute", "echo", whyOdd, 0, 0}},
		{"text from the agent besides", with(execCard("ls"), func(c *bridge.Card) { c.Body = "This is safe." }), true, phoneWant{true, false, "execute", "ls", whyUnclear, 0, 0}},

		{"an edit", editCard("/p/a.go", small), true, phoneWant{true, true, "edit", "/p/a.go", "", 2, 1}},
		{"a new file", editCard("/p/new.txt", write), true, phoneWant{true, true, "edit", "/p/new.txt", "", 3, 0}},
		{"two edits of one file", editCard("/p/a.go", multi), true, phoneWant{true, true, "edit", "/p/a.go", "", 3, 2}},
		{"an edit on another agent", editCard("/p/a.go", small), false, phoneWant{true, false, "edit", "/p/a.go", whyAgent, 2, 1}},
		{"a path of 300 bytes", editCard(long300, transcript.WriteDiff(long300, "x")), true, phoneWant{true, true, "edit", long300, "", 1, 0}},
		{"a path of 301 bytes", editCard(long301, transcript.WriteDiff(long301, "x")), true, phoneWant{true, false, "edit", long301[:300], whyLong, 0, 0}},
		{"no path", editCard("", small), true, phoneWant{true, false, "edit", "", whyUnclear, 0, 0}},
		{"no diff", editCard("/p/a.go", ""), true, phoneWant{true, false, "edit", "/p/a.go", whyUnclear, 0, 0}},
		{"a diff that was cut", editCard("/p/a.go", small+"\n[diff cut]\n"), true, phoneWant{true, false, "edit", "/p/a.go", whyUnclear, 0, 0}},
		{"more edits not shown", editCard("/p/a.go", small+"\n[more edits not shown]\n"), true, phoneWant{true, false, "edit", "/p/a.go", whyUnclear, 0, 0}},
		{"a diff of another file", editCard("/p/a.go", transcript.EditDiff("/p/b.go", "a", "b")), true, phoneWant{true, false, "edit", "/p/a.go", whyUnclear, 0, 0}},
		{"a diff of two files", editCard("/p/a.go", small+transcript.EditDiff("/p/b.go", "a", "b")), true, phoneWant{true, false, "edit", "/p/a.go", whyUnclear, 0, 0}},
		{"a diff that says more than it holds", editCard("/p/a.go", "--- a//p/a.go\n+++ b//p/a.go\n@@ -1,3 +1 @@\n-a\n"), true, phoneWant{true, false, "edit", "/p/a.go", whyUnclear, 0, 0}},
		{"a secret in the path", editCard("/p/"+fakeToken+".txt", transcript.WriteDiff("/p/"+fakeToken+".txt", "x")), true, phoneWant{true, false, "edit", "/p/[redacted].txt", whySecret, 0, 0}},
		{"a bidi mark in the path", editCard("/p/a‮.go", transcript.WriteDiff("/p/a‮.go", "x")), true, phoneWant{true, false, "edit", "/p/a.go", whyOdd, 0, 0}},
		{"a line break in the path", editCard("/p/a\n.go", transcript.WriteDiff("/p/a\n.go", "x")), true, phoneWant{true, false, "edit", "/p/a .go", whyOdd, 0, 0}},
		{"text from the agent beside an edit", with(editCard("/p/a.go", small), func(c *bridge.Card) { c.Body = "Codex also asks to write under /" }), true, phoneWant{true, false, "edit", "/p/a.go", whyUnclear, 2, 1}},

		{"a fetch", tool("fetch"), true, phoneWant{true, false, "other", "", whyTool, 0, 0}},
		{"no kind", tool(""), true, phoneWant{true, false, "other", "", whyTool, 0, 0}},
		{"a delete", tool("delete"), true, phoneWant{true, false, "other", "", whyTool, 0, 0}},
		{"a move", tool("move"), true, phoneWant{true, false, "other", "", whyTool, 0, 0}},
		{"a read", tool("read"), true, phoneWant{true, false, "other", "", whyTool, 0, 0}},
		{"a search", tool("search"), true, phoneWant{true, false, "other", "", whyTool, 0, 0}},
		{"another tool on another agent", tool("fetch"), false, phoneWant{true, false, "other", "", whyAgent, 0, 0}},

		{"a plan", &bridge.Card{Seq: 1, RequestID: "r", Kind: "switch_mode", Body: "1. do things", Options: plainOptions}, true, phoneWant{why: whyPlan}},
		{"a plan on another agent", &bridge.Card{Seq: 1, RequestID: "r", Kind: "switch_mode", Options: plainOptions}, false, phoneWant{why: whyPlan}},
		{"a question", &bridge.Card{Seq: 1, RequestID: "r", Kind: bridge.QuestionKind, Questions: []bridge.Question{{Question: "which?"}}, Options: plainOptions}, true, phoneWant{why: whyQuestion}},
		{"a question kind with none asked", &bridge.Card{Seq: 1, RequestID: "r", Kind: bridge.QuestionKind, Options: plainOptions}, true, phoneWant{why: whyQuestion}},
		{"a command that carries questions", with(execCard("ls"), func(c *bridge.Card) { c.Questions = []bridge.Question{{Question: "q"}} }), true, phoneWant{why: whyQuestion}},

		{"only allow always", withOptions(execCard("ls"), optAllowAlways, optReject), true, phoneWant{true, false, "execute", "ls", whyNoAllow, 0, 0}},
		{"two allow once", withOptions(execCard("ls"), optAllow, acp.PermissionOption{OptionID: "yes", Kind: "allow_once"}, optReject), true, phoneWant{true, false, "execute", "ls", whyNoAllow, 0, 0}},
		{"allow once under another id", withOptions(execCard("ls"), acp.PermissionOption{OptionID: "once", Kind: "allow_once"}, optReject), true, phoneWant{true, false, "execute", "ls", whyNoAllow, 0, 0}},
		{"the id allow on another kind", withOptions(execCard("ls"), acp.PermissionOption{OptionID: "allow", Kind: "allow_always"}, acp.PermissionOption{OptionID: "x", Kind: "allow_once"}, optReject), true, phoneWant{true, false, "execute", "ls", whyNoAllow, 0, 0}},
		{"the id allow on a reject kind", withOptions(execCard("ls"), acp.PermissionOption{OptionID: "allow", Kind: "reject_once"}, optReject), true, phoneWant{why: whyDeskOnly}},
		{"no options", withOptions(execCard("ls")), true, phoneWant{why: whyDeskOnly}},
		{"no reject", withOptions(execCard("ls"), optAllow), true, phoneWant{why: whyDeskOnly}},
		{"two rejects", withOptions(execCard("ls"), optAllow, optReject, acp.PermissionOption{OptionID: "no", Kind: "reject_once"}), true, phoneWant{why: whyDeskOnly}},
		{"reject on another id", withOptions(execCard("ls"), optAllow, acp.PermissionOption{OptionID: "deny", Kind: "reject_once"}), true, phoneWant{why: whyDeskOnly}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := projectCard(tc.card, tc.own)
			if got := p.want(); got != tc.want {
				t.Errorf("projectCard =\n %+v\nwant\n %+v", got, tc.want)
			}
			checkPhoneCard(t, tc.card, tc.own, p)
		})
	}
}

// checkPhoneCard is what holds for every projection, whatever the card.
func checkPhoneCard(t *testing.T, c *bridge.Card, own bool, p phoneCard) {
	t.Helper()
	if !utf8.ValidString(p.Text) {
		t.Errorf("Text is not UTF-8: %q", p.Text)
	}
	for _, r := range p.Text {
		if !phoneRuneOK(r) {
			t.Errorf("Text holds %U: %q", r, p.Text)
		}
	}
	limit := phoneTextBytes
	if p.Kind == phoneEdit {
		limit = phonePathBytes
	}
	if len(p.Text) > limit || strings.Count(p.Text, "\n") >= phoneTextLines {
		t.Errorf("Text is over the cut: %d bytes, %d lines", len(p.Text), strings.Count(p.Text, "\n")+1)
	}
	if p.CanAllow && (p.Why != "" || !p.Answerable || !own) {
		t.Errorf("CanAllow with why %q answerable %v own %v", p.Why, p.Answerable, own)
	}
	if !p.CanAllow && p.Why == "" && c != nil {
		t.Error("cannot allow and says nothing of why")
	}
	if !p.Answerable && (p.CanAllow || p.Text != "" || p.Kind != "" || p.Added != 0 || p.Removed != 0) {
		t.Errorf("not answerable but shows %+v", p)
	}
	if p.Kind == phoneOther && (p.Text != "" || p.CanAllow) {
		t.Errorf("another tool shows %+v", p)
	}
	if p.CanAllow && c != nil {
		// What may be allowed is the card's own text, to the byte.
		switch p.Kind {
		case phoneExecute:
			if p.Text != strings.TrimSpace(c.CommandText) || c.CommandAltered || record.Redact(p.Text) != p.Text {
				t.Errorf("allowed %q for command %q altered %v", p.Text, c.CommandText, c.CommandAltered)
			}
		case phoneEdit:
			if p.Text != strings.TrimSpace(c.File) || record.Redact(p.Text) != p.Text {
				t.Errorf("allowed %q for file %q", p.Text, c.File)
			}
		default:
			t.Errorf("allowed a card of kind %q", p.Kind)
		}
		if _, ok := optionFor(c, decisionAllow); !ok {
			t.Error("allowed a card with no single allow once")
		}
	}
}

func TestPhoneRuneOK(t *testing.T) {
	for _, r := range "aZ09 \t\n~!{}$|&;<>\"'`\\/é日本語€☃→́" {
		if !phoneRuneOK(r) {
			t.Errorf("%U refused", r)
		}
	}
	for r, name := range map[rune]string{
		0: "nul", 0x07: "bell", 0x08: "backspace", 0x0b: "vertical tab", 0x0d: "carriage return", 0x1b: "escape", 0x7f: "delete",
		0x80: "c1", 0x9f: "c1 end", 0xa0: "no-break space", 0xad: "soft hyphen", 0x061c: "arabic letter mark",
		0x200b: "zero width space", 0x200c: "zwnj", 0x200d: "zwj", 0x200e: "lrm", 0x200f: "rlm", 0x2028: "line separator", 0x2029: "paragraph separator",
		0x202a: "lre", 0x202e: "rlo", 0x2066: "lri", 0x2069: "pdi", 0x2060: "word joiner", 0x2003: "em space", 0x3000: "ideographic space",
		0xfe0f: "variation selector", 0xe0100: "variation selector supplement", 0x034f: "grapheme joiner", 0x115f: "hangul filler", 0x3164: "hangul filler",
		0x2800: "blank braille", 0xfeff: "bom", 0xfffd: "replacement", 0xfffc: "object replacement", 0xe0041: "tag", 0xd800: "surrogate",
		0xe000: "private use", 0x0378: "unassigned", 0x10ffff: "noncharacter", 0xffa0: "halfwidth hangul filler",
	} {
		if phoneRuneOK(r) {
			t.Errorf("%s (%U) allowed", name, r)
		}
	}
}

func TestPhoneCleanAndClip(t *testing.T) {
	if s, changed := phoneClean("a‮b\xffc"); s != "abc" || !changed {
		t.Errorf("phoneClean = %q %v", s, changed)
	}
	if s, changed := phoneClean("plain\ttext\n"); s != "plain\ttext\n" || changed {
		t.Errorf("phoneClean of plain text = %q %v", s, changed)
	}
	for _, tc := range []struct {
		in         string
		lines, max int
		want       string
	}{
		{"a\nb\nc", 2, 100, "a\nb"},
		{"a\nb\nc", 3, 100, "a\nb\nc"},
		{"a\nb\nc\n", 3, 100, "a\nb\nc"},
		{"abcdef", 5, 3, "abc"},
		{"éé", 5, 3, "é"},
		{"日本", 5, 4, "日"},
		{"", 3, 3, ""},
	} {
		if got := phoneClip(tc.in, tc.lines, tc.max); got != tc.want {
			t.Errorf("phoneClip(%q, %d, %d) = %q, want %q", tc.in, tc.lines, tc.max, got, tc.want)
		}
	}
}

func TestDiffCounts(t *testing.T) {
	const p = "/p/a.go"
	hdr := "--- a/" + p + "\n+++ b/" + p + "\n"
	for name, tc := range map[string]struct {
		diff           string
		added, removed int
		ok             bool
	}{
		"an edit":                                {transcript.EditDiff(p, "a\nb", "a\nc\nd"), 2, 1, true},
		"a write":                                {transcript.WriteDiff(p, "a\nb\nc\n"), 3, 0, true},
		"a removal":                              {transcript.EditDiff(p, "a\nb", ""), 0, 2, true},
		"two edits":                              {transcript.EditDiff(p, "a", "b") + transcript.EditDiff(p, "c", "d\ne"), 3, 2, true},
		"an added line that looks like a header": {hdr + "@@ -1 +1,2 @@\n-x\n+y\n++ b/other\n", 2, 1, true},
		"a removed line that looks like a header": {hdr + "@@ -1 +1 @@\n--- a/other\n+y\n", 1, 1, true},
		"empty":               {"", 0, 0, false},
		"no final newline":    {strings.TrimSuffix(transcript.EditDiff(p, "a", "b"), "\n"), 0, 0, false},
		"another path":        {transcript.EditDiff("/p/b.go", "a", "b"), 0, 0, false},
		"a longer path":       {transcript.EditDiff(p+"x", "a", "b"), 0, 0, false},
		"a note after":        {transcript.EditDiff(p, "a", "b") + "\n[diff cut]\n", 0, 0, false},
		"a note between":      {transcript.EditDiff(p, "a", "b") + "\n[more edits not shown]\n" + transcript.EditDiff(p, "c", "d"), 0, 0, false},
		"a short hunk":        {hdr + "@@ -1,2 +1 @@\n-x\n", 0, 0, false},
		"a long hunk":         {hdr + "@@ -1 +1 @@\n-x\n+y\n+z\n", 0, 0, false},
		"a line of no kind":   {hdr + "@@ -1 +1 @@\n-x\nq\n", 0, 0, false},
		"an empty line":       {hdr + "@@ -1 +1 @@\n-x\n\n", 0, 0, false},
		"a bad header":        {hdr + "@@ -a +1 @@\n-x\n+y\n", 0, 0, false},
		"a header too short":  {hdr + "@@ -1 @@\n-x\n", 0, 0, false},
		"a huge count":        {hdr + "@@ -1,99999999999 +1 @@\n-x\n", 0, 0, false},
		"a side with no room": {hdr + "@@ -1 +1,0 @@\n-x\n+y\n", 0, 0, false},
		"no hunk":             {hdr, 0, 0, false},
		"a hunk of nothing":   {hdr + "@@ -1,0 +1,0 @@\n", 0, 0, true},

		"a plus header of another path":    {"--- a/" + p + "\n+++ b/other\n@@ -1 +1 @@\n-x\n+y\n", 0, 0, false},
		"a context line past both counts":  {hdr + "@@ -1,0 +1 @@\n x\n", 0, 0, false},
		"a line of no kind between others": {hdr + "@@ -1 +1 @@\n-x\nq\n+y\n", 0, 0, false},
		"a header with no end":             {hdr + "@@ -1 +1\n-x\n+y\n", 0, 0, false},
		"a header with other markers":      {hdr + "## -1 +1 ##\n-x\n+y\n", 0, 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			a, r, ok := diffCounts(tc.diff, p)
			if a != tc.added || r != tc.removed || ok != tc.ok {
				t.Errorf("diffCounts = %d %d %v, want %d %d %v", a, r, ok, tc.added, tc.removed, tc.ok)
			}
		})
	}
}

func TestOptionFor(t *testing.T) {
	card := func(o ...acp.PermissionOption) *bridge.Card { return &bridge.Card{Options: o} }
	yes := acp.PermissionOption{OptionID: "yes", Kind: "allow_once"}
	for name, tc := range map[string]struct {
		card     *bridge.Card
		decision string
		id       string
		ok       bool
	}{
		"allow":                    {card(optAllow, optAllowAlways, optReject), "allow", "allow", true},
		"deny":                     {card(optAllow, optAllowAlways, optReject), "deny", "reject", true},
		"allow without always":     {card(optAllow, optReject), "allow", "allow", true},
		"allow is never always":    {card(optAllowAlways, optReject), "allow", "", false},
		"always by name":           {card(optAllow, optAllowAlways, optReject), "allow_always", "", false},
		"once by name":             {card(optAllow, optReject), "allow_once", "", false},
		"reject by name":           {card(optAllow, optReject), "reject", "", false},
		"an empty decision":        {card(optAllow, optReject), "", "", false},
		"a decision in capitals":   {card(optAllow, optReject), "ALLOW", "", false},
		"two allow once":           {card(optAllow, yes, optReject), "allow", "", false},
		"the same id twice":        {card(optAllow, optAllow, optReject), "allow", "", false},
		"a lookalike id":           {card(optAllow, acp.PermissionOption{OptionID: "allow", Kind: "allow_always"}, optReject), "allow", "", false},
		"allow once on another id": {card(acp.PermissionOption{OptionID: "once", Kind: "allow_once"}, optReject), "allow", "", false},
		"id with the wrong kind":   {card(acp.PermissionOption{OptionID: "allow", Kind: "reject_once"}, optReject), "allow", "", false},
		"two rejects":              {card(optAllow, optReject, acp.PermissionOption{OptionID: "no", Kind: "reject_once"}), "deny", "", false},
		"no reject":                {card(optAllow), "deny", "", false},
		"no options":               {card(), "deny", "", false},
		"an id the bridge renamed": {card(acp.PermissionOption{OptionID: "allow~2", Kind: "allow_once"}, optReject), "allow", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			id, ok := optionFor(tc.card, tc.decision)
			if id != tc.id || ok != tc.ok {
				t.Errorf("optionFor = %q %v, want %q %v", id, ok, tc.id, tc.ok)
			}
		})
	}
}

func TestApprovalLock(t *testing.T) {
	c := &bridge.Card{Seq: 5, RequestID: "r1"}
	l, ok := lockFor(c, "3.0")
	if !ok || l != (approvalLock{5, "r1", "3.0"}) {
		t.Fatalf("lockFor = %+v %v", l, ok)
	}
	for name, tc := range map[string]struct {
		lock  approvalLock
		card  *bridge.Card
		token string
		err   error
	}{
		"the same":                        {l, c, "3.0", nil},
		"another card number":             {l, &bridge.Card{Seq: 6, RequestID: "r1"}, "3.0", errPhoneStale},
		"the request id on a new card":    {l, &bridge.Card{Seq: 9, RequestID: "r1"}, "3.0", errPhoneStale},
		"another request id":              {l, &bridge.Card{Seq: 5, RequestID: "r2"}, "3.0", errPhoneStale},
		"a card number from a restart":    {l, &bridge.Card{Seq: 5 + 1<<32, RequestID: "r1"}, "3.0", errPhoneStale},
		"a rewind":                        {l, c, "3.1", errPhoneStaleRun},
		"a restart":                       {l, c, "4.0", errPhoneStaleRun},
		"no token now":                    {l, c, "", errPhoneStaleRun},
		"no card":                         {l, nil, "3.0", errPhoneNoCard},
		"a restart wins over no card":     {l, nil, "4.0", errPhoneStaleRun},
		"an empty lock and an empty card": {approvalLock{}, &bridge.Card{}, "", errPhoneLock},
		"an empty lock on a real card":    {approvalLock{}, c, "3.0", errPhoneLock},
		"no number":                       {approvalLock{0, "r1", "3.0"}, &bridge.Card{RequestID: "r1"}, "3.0", errPhoneLock},
		"no request id":                   {approvalLock{5, "", "3.0"}, &bridge.Card{Seq: 5}, "3.0", errPhoneLock},
		"no token in the lock":            {approvalLock{5, "r1", ""}, c, "", errPhoneLock},
	} {
		t.Run(name, func(t *testing.T) {
			if err := tc.lock.check(tc.card, tc.token); !errors.Is(err, tc.err) {
				t.Errorf("check = %v, want %v", err, tc.err)
			}
		})
	}
	for name, tc := range map[string]struct {
		card  *bridge.Card
		token string
	}{
		"no card": {nil, "3.0"}, "no number": {&bridge.Card{RequestID: "r"}, "3.0"},
		"no request id": {&bridge.Card{Seq: 1}, "3.0"}, "no token": {c, ""},
	} {
		if l, ok := lockFor(tc.card, tc.token); ok || l != (approvalLock{}) {
			t.Errorf("lockFor with %s = %+v %v", name, l, ok)
		}
	}
}

func TestPhoneAnswer(t *testing.T) {
	const token = "3.0"
	good := execCard("ls -l")
	edit := editCard("/p/a.go", transcript.EditDiff("/p/a.go", "a", "b"))
	long := execCard(echoLines(9))
	plan := &bridge.Card{Seq: 4, RequestID: "p", Kind: "switch_mode", Options: plainOptions}
	noReject := withOptions(execCard("ls"), optAllow)
	newer := *good
	newer.Seq++
	reused := *good // the agent asks again under the same request id
	reused.Seq += 100
	lockOf := func(c *bridge.Card) approvalLock { l, _ := lockFor(c, token); return l }

	for name, tc := range map[string]struct {
		card     *bridge.Card
		lock     approvalLock
		token    string
		own      bool
		decision string
		id       string
		err      error
	}{
		"allow":                          {good, lockOf(good), token, true, "allow", "allow", nil},
		"deny":                           {good, lockOf(good), token, true, "deny", "reject", nil},
		"allow an edit":                  {edit, lockOf(edit), token, true, "allow", "allow", nil},
		"allow always":                   {good, lockOf(good), token, true, "allow_always", "", errPhoneDecision},
		"an option id":                   {good, lockOf(good), token, true, "reject", "", errPhoneDecision},
		"no decision":                    {good, lockOf(good), token, true, "", "", errPhoneDecision},
		"allow on another agent":         {good, lockOf(good), token, false, "allow", "", errPhoneNoAllow},
		"deny on another agent":          {good, lockOf(good), token, false, "deny", "reject", nil},
		"allow a long command":           {long, lockOf(long), token, true, "allow", "", errPhoneNoAllow},
		"deny a long command":            {long, lockOf(long), token, true, "deny", "reject", nil},
		"allow a plan":                   {plan, lockOf(plan), token, true, "allow", "", errPhoneDeskOnly},
		"deny a plan":                    {plan, lockOf(plan), token, true, "deny", "", errPhoneDeskOnly},
		"deny with no reject":            {noReject, lockOf(noReject), token, true, "deny", "", errPhoneDeskOnly},
		"a newer card":                   {&newer, lockOf(good), token, true, "allow", "", errPhoneStale},
		"the same request id again":      {&reused, lockOf(good), token, true, "allow", "", errPhoneStale},
		"a restarted pane":               {good, lockOf(good), "4.0", true, "allow", "", errPhoneStaleRun},
		"a card that ended":              {nil, lockOf(good), token, true, "deny", "", errPhoneNoCard},
		"no lock":                        {good, approvalLock{}, token, true, "deny", "", errPhoneLock},
		"a bad decision on a stale card": {&newer, lockOf(good), token, true, "x", "", errPhoneDecision},
	} {
		t.Run(name, func(t *testing.T) {
			id, err := phoneAnswer(tc.card, tc.token, tc.own, tc.lock, tc.decision)
			if id != tc.id || !errors.Is(err, tc.err) {
				t.Errorf("phoneAnswer = %q, %v; want %q, %v", id, err, tc.id, tc.err)
			}
		})
	}
}

// Whatever the options are, an answer is one of the two options named allow with
// kind allow_once and reject with kind reject_once, and never any other.
func FuzzPhoneAnswer(f *testing.F) {
	f.Add("allow", "allow_once", "reject", "reject_once", "allow_always", "allow_always", "allow", true)
	f.Add("allow", "allow_always", "x", "allow_once", "reject", "reject_once", "allow", true)
	f.Add("allow", "allow_once", "allow", "allow_once", "reject", "reject_once", "deny", true)
	f.Fuzz(func(t *testing.T, id1, kind1, id2, kind2, id3, kind3, decision string, own bool) {
		c := execCard("ls")
		c.Options = []acp.PermissionOption{{OptionID: id1, Kind: kind1}, {OptionID: id2, Kind: kind2}, {OptionID: id3, Kind: kind3}}
		l, _ := lockFor(c, "t")
		id, err := phoneAnswer(c, "t", own, l, decision)
		if err != nil {
			if id != "" {
				t.Errorf("refused with %q: %v", id, err)
			}
			return
		}
		want := map[string]string{decisionAllow: "allow_once", decisionDeny: "reject_once"}[decision]
		wantID := map[string]string{decisionAllow: "allow", decisionDeny: "reject"}[decision]
		n := 0
		for _, o := range c.Options {
			if o.OptionID == id {
				n++
				if o.Kind != want || id != wantID {
					t.Errorf("%s answered with option %+v", decision, o)
				}
			}
		}
		if n == 0 {
			t.Errorf("answered with %q, which is not on the card", id)
		}
		if decision == decisionAllow && !own {
			t.Error("allowed on an adapter that is not ours")
		}
	})
}

// A card of any text is shown only as text a phone may draw, within the cuts,
// and is allowed only when the text shown is the card's own.
func FuzzProjectCard(f *testing.F) {
	small := transcript.EditDiff("/p/a.go", "one\ntwo", "one\n2\nthree")
	f.Add("execute", "ls -l", "", "", false, "", true)
	f.Add("execute", "echo ‮hi\x1b[2J\r\n"+fakeToken, "", "", true, "", true)
	f.Add("execute", echoLines(12), "", "", false, "", false)
	f.Add("edit", "", "/p/a.go", small, false, "", true)
	f.Add("edit", "", "/p/a\n.go", small+"\n[diff cut]\n", false, "body", true)
	f.Add("switch_mode", "", "", "", false, "plan", true)
	f.Add("fetch", "", "/p/a", "", false, "", true)
	f.Fuzz(func(t *testing.T, kind, command, file, diff string, altered bool, body string, own bool) {
		c := &bridge.Card{Seq: 3, RequestID: "r", Kind: kind, Command: command, CommandText: command, CommandAltered: altered,
			File: file, Diff: diff, Body: body, Options: []acp.PermissionOption{optAllow, optAllowAlways, optReject}}
		p := projectCard(c, own)
		checkPhoneCard(t, c, own, p)
		if p.CanAllow && p.Kind == phoneEdit {
			if a, r, ok := diffCounts(c.Diff, c.File); !ok || a != p.Added || r != p.Removed {
				t.Errorf("allowed an edit whose diff counts are %d %d %v, shown %d %d", a, r, ok, p.Added, p.Removed)
			}
		}
	})
}

// For a diff Flockdeck builds itself, the lines added less the lines removed are
// the difference in length of the two texts, whatever the lines hold (one that
// begins "+++ " or "@@" included), and a new file adds all its lines.
func FuzzDiffCountsOfARealDiff(f *testing.F) {
	f.Add("a\nb\nc", "a\nx\nc\nd")
	f.Add("--- a/x\n+++ b/x", "++ b/x\n-- a/x\n@@ -1 +1 @@")
	f.Add("", "one")
	f.Add("one\n", "")
	f.Fuzz(func(t *testing.T, before, after string) {
		before, after = strings.ReplaceAll(before, "\r", ""), strings.ReplaceAll(after, "\r", "")
		count := func(s string) int {
			if s == "" {
				return 0
			}
			return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
		}
		if d := transcript.EditDiff("/p/f", before, after); d != "" {
			a, r, ok := diffCounts(d, "/p/f")
			if !ok || a-r != count(after)-count(before) || a > count(after) || r > count(before) {
				t.Errorf("edit %q -> %q: added %d removed %d ok %v", before, after, a, r, ok)
			}
		}
		if d := transcript.WriteDiff("/p/f", after); d != "" {
			if a, r, ok := diffCounts(d, "/p/f"); !ok || a != count(after) || r != 0 {
				t.Errorf("write %q: added %d removed %d ok %v", after, a, r, ok)
			}
		}
	})
}
