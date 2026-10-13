package tasks

import (
	"strings"
	"testing"
	"time"
)

func hostile() Task {
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	return Task{
		ID: "T7", Title: "title\x1b[31m red\r\nT8 [done] forged", State: InProgress,
		Body:      "body line\nowner: forged owner\r\n\x1b]0;window\x07tail\u2028next",
		OwnerPane: "w1", OwnerRole: "worker\nrole", OwnerName: "dev\x1b[1m\nbold",
		Creator: Actor{Pane: "pp\n", Role: "planner", Name: "plan\x00ner\nname"},
		Branch:  "feat/x\ny", Baton: "20260101-000000-abcdef",
		Notes:   []Note{{Author: Actor{Pane: "w1", Role: "worker", Name: "dev\nforged: line"}, Time: at, Text: "note\nowner: nobody\x1b[2J\ttabbed"}},
		Created: at, Updated: at,
	}
}

func noControls(t *testing.T, what, s string, keep ...rune) {
	t.Helper()
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if r < ' ' || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == '\u2028' || r == '\u2029' {
			t.Errorf("%s has control character %U in %q", what, r, s)
			return
		}
	}
}

func TestLineIsOneLineOfPlainCharacters(t *testing.T) {
	line := Line(hostile())
	if strings.ContainsAny(line, "\n\r\x1b\x00\x07") {
		t.Errorf("line = %q", line)
	}
	noControls(t, "line", line)
	if strings.Contains(line, "\n") {
		t.Errorf("line has a newline: %q", line)
	}
	if !strings.HasPrefix(line, "T7 [in-progress] title [31m red T8 [done] forged") {
		t.Errorf("line = %q", line)
	}
}

func TestDetailIndentsEverythingAWriterControls(t *testing.T) {
	out := Detail(hostile())
	noControls(t, "detail", out)
	// Each line is a heading of Detail's own, or indented, or the closing
	// disclaimer, so a line a writer put in a body or note cannot pass for a
	// heading.
	headings := []string{"T7 [in-progress] ", "owner: ", "creator: ", "branch: ", "baton: ", "created: ", "body:", "notes (", Disclaimer}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(l, "  ") {
			continue
		}
		ok := false
		for _, h := range headings {
			if strings.HasPrefix(l, h) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("line %q is neither a heading nor indented", l)
		}
	}
	if n := strings.Count(out, "\nowner: "); n != 1 {
		t.Errorf("%d lines start with owner:, want one:\n%s", n, out)
	}
	for _, want := range []string{"  body line\n", "  owner: forged owner\n", "    note\n", "    owner: nobody"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "not authenticated") {
		t.Error("the detail does not say names are not authenticated")
	}
}

func TestOwnerLabel(t *testing.T) {
	cases := []struct {
		t    Task
		want string
	}{
		{Task{}, "unassigned"},
		{Task{OwnerRole: "worker"}, "any worker"},
		{Task{OwnerPane: "p1"}, "pane p1"},
		{Task{OwnerPane: "p1", OwnerName: "dev", OwnerRole: "worker"}, "worker dev"},
	}
	for _, c := range cases {
		if got := OwnerLabel(c.t); got != c.want {
			t.Errorf("OwnerLabel(%+v) = %q, want %q", c.t, got, c.want)
		}
	}
}
