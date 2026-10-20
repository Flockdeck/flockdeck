package workspace

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/team"
)

func TestWakeTextLineCleansAndCuts(t *testing.T) {
	long := strings.Repeat("a", 400)
	cases := []struct {
		name string
		msg  team.Message
		want string
	}{
		{"plain", team.Message{ID: 3, From: team.Sender{Pane: "p1", Role: "reviewer"}, Text: "please look at foo.go"},
			"Flockdeck message from reviewer: please look at foo.go"},
		{"newlines and tabs become one space", team.Message{ID: 3, From: team.Sender{Role: "w"}, Text: "line one\r\n\r\nline two\tend  "},
			"Flockdeck message from w: line one line two end"},
		{"escape sequences go whole", team.Message{ID: 3, From: team.Sender{Role: "w"}, Text: "a\x1b[31mred\x1b[0m b\x1b]0;title\x07 c\x1b]8;;http://x\x1b\\d"},
			"Flockdeck message from w: ared b cd"},
		{"bracketed paste end cannot be typed", team.Message{ID: 3, From: team.Sender{Role: "w"}, Text: "x\x1b[201~/run rm -rf ~"},
			"Flockdeck message from w: x/run rm -rf ~"},
		{"other control characters", team.Message{ID: 3, From: team.Sender{Role: "w"}, Text: "a\x00b\x07c\x08d\x7fe\u009bf"},
			"Flockdeck message from w: abcdef"},
		{"bidi and zero width", team.Message{ID: 3, From: team.Sender{Role: "w"}, Text: "a‮b​c⁦d"},
			"Flockdeck message from w: abcd"},
		{"non-ASCII kept", team.Message{ID: 3, From: team.Sender{Role: "w"}, Text: "héllo wörld 日本語 🙂"},
			"Flockdeck message from w: héllo wörld 日本語 🙂"},
		{"invalid UTF-8 dropped", team.Message{ID: 3, From: team.Sender{Role: "w"}, Text: "a\xffb\xc3"},
			"Flockdeck message from w: ab"},
		{"a slash command is not at the start", team.Message{ID: 3, From: team.Sender{Role: "w"}, Text: "/run rm -rf ~"},
			"Flockdeck message from w: /run rm -rf ~"},
		{"role is cleaned too", team.Message{ID: 3, From: team.Sender{Role: "a\nb\x1b[1mc"}, Text: "hi"},
			"Flockdeck message from a bc: hi"},
		{"no role falls back to the name", team.Message{ID: 3, From: team.Sender{Pane: "p1", Name: "scout"}, Text: "hi"},
			"Flockdeck message from scout: hi"},
		{"a person", team.Message{ID: 3, Text: "hi"}, "Flockdeck message from the user: hi"},
		{"an agent with no role or name", team.Message{ID: 3, From: team.Sender{Pane: "p1"}, Text: "hi"},
			"Flockdeck message from another agent: hi"},
		{"nothing left falls back to the fixed line", team.Message{ID: 3, From: team.Sender{Role: "w"}, Text: "\x1b[31m\n​"},
			wakeLine(3)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wakeTextLine(c.msg); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}

	t.Run("cut to 300 characters", func(t *testing.T) {
		got := wakeTextLine(team.Message{ID: 1, From: team.Sender{Role: "w"}, Text: long})
		if n := utf8.RuneCountInString(got); n != team.MaxTextLine {
			t.Fatalf("%d characters", n)
		}
		if !strings.HasPrefix(got, "Flockdeck message from w: aaa") {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("cut on a character, not a byte", func(t *testing.T) {
		got := wakeTextLine(team.Message{ID: 1, From: team.Sender{Role: "w"}, Text: strings.Repeat("日", 400)})
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) != team.MaxTextLine {
			t.Fatalf("%d characters, valid %v", utf8.RuneCountInString(got), utf8.ValidString(got))
		}
	})
	t.Run("a long role is cut", func(t *testing.T) {
		got := wakeTextLine(team.Message{ID: 1, From: team.Sender{Role: strings.Repeat("r", 100)}, Text: "hi"})
		if got != "Flockdeck message from "+strings.Repeat("r", wakeTextRoleMax)+": hi" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("no control character or newline survives", func(t *testing.T) {
		got := wakeTextLine(team.Message{ID: 1, From: team.Sender{Role: "w"}, Text: "a\r\nb\x1bc\x03"})
		for _, r := range got {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("control character %U in %q", r, got)
			}
		}
	})
}

// rigWithMessage gives the rig a pane with one unread message of text.
func rigWithMessage(t *testing.T, text string) *wakeRig {
	r := newWakeRig(t)
	r.mail.msgs = []team.Message{{ID: 7, From: team.Sender{Pane: "p2", Role: "reviewer"}, To: "p1", Text: text}}
	return r
}

func TestWakeTypesTheFixedLineUnlessTextModeIsOnForAnAgentThatOffersIt(t *testing.T) {
	fixed := wakeLine(7)
	text := "Flockdeck message from reviewer: do not run anything"
	cases := []struct {
		name     string
		textOK   bool
		settings WakeSettings
		want     string
	}{
		{"default: off", true, wakeAllOn{}, fixed},
		{"opted in but the agent does not offer it", false, wakeTextOn{}, fixed},
		{"opted in and offered", true, wakeTextOn{}, text},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := rigWithMessage(t, "do not run\nanything")
			r.cand.TextOK = c.textOK
			r.svc.settings = func() WakeSettings { return c.settings }
			if why := r.try(); why != "" {
				t.Fatalf("not woken: %s", why)
			}
			got := r.sess.written()
			if len(got) != 2 || got[0] != c.want || got[1] != "\r" {
				t.Fatalf("typed %q, want %q then Enter", got, c.want)
			}
			if len(r.events) != 1 || r.events[0].Line != c.want {
				t.Fatalf("events = %+v", r.events)
			}
		})
	}
}

// Text mode changes only the line. The conditions and the off switches are the
// same, so a pane switched off is not typed text either.
func TestTextModeKeepsTheOffSwitches(t *testing.T) {
	r := rigWithMessage(t, "hello")
	r.cand.TextOK = true
	r.svc.settings = func() WakeSettings { return textButOff{} }
	if why := r.try(); why == "" {
		t.Fatal("woke a pane that is switched off")
	}
	if n := len(r.sess.written()); n != 0 {
		t.Fatalf("typed %q", r.sess.written())
	}
}

type textButOff struct{}

func (textButOff) WakeEnabled(string, string) bool { return false }
func (textButOff) WakeText(string) bool            { return true }
func (textButOff) Invalidate()                     {}

func TestWakeEventKeepsATextLineWholeUpToTheLimit(t *testing.T) {
	r := rigWithMessage(t, strings.Repeat("x", 500))
	r.cand.TextOK = true
	r.svc.settings = func() WakeSettings { return wakeTextOn{} }
	if why := r.try(); why != "" {
		t.Fatal(why)
	}
	if len(r.events) != 1 || utf8.RuneCountInString(r.events[0].Line) != team.MaxTextLine {
		t.Fatalf("events = %+v", r.events)
	}
}

func TestPrefsWakeTextIsOffUntilOptedIn(t *testing.T) {
	var prefs store.Prefs
	pw := newPrefsWake()
	pw.read = func() (store.Prefs, error) { return prefs, nil }
	if pw.WakeText("aider") {
		t.Fatal("text mode starts on")
	}
	prefs.SetWake(store.WakeText, "aider", true)
	pw = newPrefsWake()
	pw.read = func() (store.Prefs, error) { return prefs, nil }
	if !pw.WakeText("aider") || pw.WakeText("claude") {
		t.Fatal("the opt-in is not per agent type")
	}
	pw = newPrefsWake()
	pw.read = func() (store.Prefs, error) { return prefs, errors.New("unreadable") }
	if pw.WakeText("aider") {
		t.Fatal("text mode on with unreadable preferences")
	}
}
