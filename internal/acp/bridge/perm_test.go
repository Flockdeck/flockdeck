package bridge

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
)

// answerWith runs one permission request through the handler. pick chooses the
// answer from the card.
func answerWith(b *Bridge, opts []acp.PermissionOption, pick func(Card) string) (Card, string) {
	var shown Card
	h := b.Handler(func(_ context.Context, c Card) string {
		shown = c
		return pick(c)
	})
	got := h.Permission(context.Background(), acp.PermissionRequest{RequestID: "1", Options: opts})
	return shown, got
}

func TestCancelledStaysCancelledWhateverTheIDs(t *testing.T) {
	cases := map[string][]acp.PermissionOption{
		"id cleans to empty": {{OptionID: "\x1b[0m", Name: "Allow", Kind: "allow_once"}, {OptionID: "no", Name: "Reject"}},
		"bell":               {{OptionID: "\x07", Name: "Allow"}},
		"raw empty":          {{OptionID: "", Name: "Allow"}, {OptionID: "no", Name: "Reject"}},
		"bidi only":          {{OptionID: "‮​", Name: "Allow"}},
		"none":               nil,
	}
	for name, opts := range cases {
		b, _ := newBridge(Config{})
		_, got := answerWith(b, opts, func(Card) string { return "" })
		if got != "" {
			t.Errorf("%s: a dismissed card answered %q", name, got)
		}
		// An id that is not on the card is cancelled too.
		_, got = answerWith(b, opts, func(Card) string { return "not-an-option" })
		if got != "" {
			t.Errorf("%s: an unknown id answered %q", name, got)
		}
	}
}

func TestOptionsWithNoIDOfTheirOwnAreNotOffered(t *testing.T) {
	b, _ := newBridge(Config{})
	card, _ := answerWith(b, []acp.PermissionOption{{OptionID: "", Name: "ghost"}, {OptionID: "ok", Name: "real"}}, func(Card) string { return "" })
	if len(card.Options) != 1 || card.Options[0].Name != "real" {
		t.Errorf("options = %+v", card.Options)
	}
}

func TestCollidingIDsAnswerTheOptionChosen(t *testing.T) {
	long := strings.Repeat("x", labelCap+10)
	b, _ := newBridge(Config{})
	opts := []acp.PermissionOption{
		{OptionID: "go\x07", Name: "Allow", Kind: "allow_once"},
		{OptionID: "go", Name: "Reject", Kind: "reject_once"},
		{OptionID: long + "A", Name: "long A"},
		{OptionID: long + "B", Name: "long B"},
		{OptionID: "\x1b[0m", Name: "blank one"},
		{OptionID: "\x07", Name: "blank two"},
		{OptionID: "option-5", Name: "looks generated"},
	}
	card, _ := answerWith(b, opts, func(Card) string { return "" })
	seen := map[string]bool{}
	for _, o := range card.Options {
		if o.OptionID == "" || seen[o.OptionID] {
			t.Fatalf("shown ids are not unique and non-empty: %+v", card.Options)
		}
		seen[o.OptionID] = true
	}
	if len(card.Options) != len(opts) {
		t.Fatalf("options = %+v", card.Options)
	}
	for i := range opts {
		_, got := answerWith(b, opts, func(c Card) string { return c.Options[i].OptionID })
		if got != opts[i].OptionID {
			t.Errorf("chose %q (%s), the agent got %q", card.Options[i].OptionID, opts[i].Name, got)
		}
	}
}

// Whatever ids the agent sends, the option the person picks is the option the
// agent hears about, and nothing else is.
func TestTheChosenOptionIsTheOneAnswered(t *testing.T) {
	pieces := []string{"", "a", "go", "go\x07", "\x1b[0m", "\x07", "‮", "​", "é", "option-1", "~2", strings.Repeat("y", 250), "\xff", "\u009b", "️", "\U000e0041"}
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 300; round++ {
		n := rng.Intn(maxOptions + 6)
		var opts []acp.PermissionOption
		for i := 0; i < n; i++ {
			id := pieces[rng.Intn(len(pieces))] + pieces[rng.Intn(len(pieces))]
			opts = append(opts, acp.PermissionOption{OptionID: id, Name: fmt.Sprint("opt", i)})
		}
		b, _ := newBridge(Config{})
		card, got := answerWith(b, opts, func(Card) string { return "" })
		if got != "" {
			t.Fatalf("round %d: cancelled answered %q", round, got)
		}
		offered := map[string]string{} // name -> the agent's id
		for _, o := range opts {
			if o.OptionID != "" {
				offered[o.Name] = o.OptionID
			}
		}
		if len(card.Options) != min(len(offered), maxOptions) {
			t.Fatalf("round %d: %d options offered for %d usable", round, len(card.Options), len(offered))
		}
		seen := map[string]bool{}
		for _, shown := range card.Options {
			if shown.OptionID == "" || seen[shown.OptionID] {
				t.Fatalf("round %d: ids %+v", round, card.Options)
			}
			seen[shown.OptionID] = true
			_, got := answerWith(b, opts, func(Card) string { return shown.OptionID })
			if want := offered[shown.Name]; got != want {
				t.Fatalf("round %d: chose %q (%s), the agent got %q, want %q", round, shown.OptionID, shown.Name, got, want)
			}
		}
	}
}

func TestMoreInvisibleCharactersAreStripped(t *testing.T) {
	in := "a​b‌c‍d⁠e️f\U000e0041g\U000e0020h"
	if got := clean(in); got != "abcdefgh" {
		t.Errorf("clean = %q", got)
	}
	// An ESC before a multibyte rune takes only itself.
	if got := clean("a\x1bébc"); got != "aébc" {
		t.Errorf("clean = %q", got)
	}
	if got := clean("x\x1b"); got != "x" {
		t.Errorf("trailing ESC: %q", got)
	}
}

func TestTheUnchangedOutputShortcutIsUsed(t *testing.T) {
	b, _ := newBridge(Config{})
	b.TurnStart("go")
	b.Update(toolCall("c1", "execute", "t"))
	out := strings.Repeat("line\n", 1000)
	for i := 0; i < 5; i++ {
		b.Update(toolUpdate("c1", "in_progress", outText(out)))
	}
	if b.summaries != 1 {
		t.Errorf("output taken in %d times for five identical updates", b.summaries)
	}
	b.Update(toolUpdate("c1", "in_progress", outText(out+"more\n")))
	if b.summaries != 2 {
		t.Errorf("changed output not taken in: %d", b.summaries)
	}
	// The same output at a new status is taken in again: the status can change
	// what is shown.
	b.Update(toolUpdate("c1", "completed", outText(out+"more\n")))
	if b.summaries != 3 {
		t.Errorf("a status change skipped the output: %d", b.summaries)
	}
}
