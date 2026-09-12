package chat

import (
	"context"
	"os"
	"strings"
	"testing"
)

// widestWordLine is the longest line of the repeated test text in s.
func widestWordLine(s string) int {
	widest := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "word") && len(line) > widest {
			widest = len(line)
		}
	}
	return widest
}

// A pane is resized while it runs, and an answer wrapped to the width the pane
// had when it started is wrapped again by the terminal into fragments. The
// width is asked for before each answer.
func TestEachAnswerIsWrappedToTheWidthAtTheTime(t *testing.T) {
	t.Setenv("FLOCKDECK_COLUMNS", "100")
	long := strings.TrimSpace(strings.Repeat("word ", 30))
	wire := &scriptedWire{turns: []turnFunc{
		func(_ context.Context, _ Request, emit func(Event)) error {
			emit(Event{Kind: EventText, Text: long})
			// The pane is made narrower while this answer is on the screen.
			os.Setenv("FLOCKDECK_COLUMNS", "30")
			return nil
		},
		says(long),
	}}
	var out strings.Builder
	err := Run(context.Background(), Options{
		Agent: "anthropic", Session: "s", Dir: t.TempDir(), Task: "one",
		In: strings.NewReader("two\n/exit\n"), Out: &out, wire: wire,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The first answer comes before the first prompt, the second after it.
	first, second, found := strings.Cut(out.String(), "you > ")
	if !found {
		t.Fatalf("no prompt was drawn:\n%s", out.String())
	}
	if w := widestWordLine(first); w <= 30 {
		t.Errorf("the first answer was wrapped at %d, narrower than the pane it was written in", w)
	}
	if w := widestWordLine(second); w == 0 || w > 30 {
		t.Errorf("the second answer was wrapped at %d columns in a pane of 30", w)
	}
}

func TestAnExplicitWidthWinsOverTheTerminal(t *testing.T) {
	t.Setenv("FLOCKDECK_COLUMNS", "44")
	if got := resolveWidth(); got != 44 {
		t.Errorf("resolveWidth = %d, want the width the pane was given", got)
	}
}
