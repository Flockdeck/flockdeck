package baton

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Chains of a name and a value that never ends took time that grew with the square of the
// text: each name read to the end of the text.
func TestChainsOfBareNamesAreLinear(t *testing.T) {
	if slowRun() {
		t.Skip("wall-clock limits do not hold under the race detector")
	}
	for _, unit := range []string{"{key=", "?key=", "(pw:", "[pass=", "<key:", "&pwd=", `"key":`} {
		in := strings.Repeat(unit, 100000/len(unit))
		start := time.Now()
		NewScrubber().Scrub(in)
		if d := time.Since(start); d > time.Second {
			t.Errorf("100 KB of %q took %s", unit, d)
		}
	}
}

// A value that ends in a backslash was looked at against the whole rest of the text, white
// space trimmed from its far end, for each match.
func TestManyTrailingBackslashValuesBeforeALongTailOfSpacesAreLinear(t *testing.T) {
	if slowRun() {
		t.Skip("wall-clock limits do not hold under the race detector")
	}
	in := strings.Repeat("pass=Hunter2abcdefgh\\ \n", 40000) + strings.Repeat(" ", 1<<20)
	start := time.Now()
	passNameSpans(in) // not through Scrub, which would cut the tail of spaces off
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s", d)
	}
}

// A text of any size is bounded: over the limit its middle is clipped, and past the deadline
// what is left is replaced and never passed through. A secret that is in the part that is not
// reached is not in the output.
func TestAHostileBlobIsBoundedAndNothingUnscrubbedIsPassedOn(t *testing.T) {
	secret := "Hunter2abcdefghXYZ"
	line := "password=" + secret + "\n"
	blob := strings.Repeat(line, scaled(5<<20)/len(line))
	start := time.Now()
	sc := NewScrubber().WithDeadline(time.Now().Add(300 * time.Millisecond))
	got, _ := sc.Scrub(blob)
	if d := time.Since(start); !slowRun() && d > 6*time.Second {
		t.Errorf("took %s", d)
	}
	if strings.Contains(got, secret) {
		t.Error("a secret was passed on")
	}
	// A text begun after the deadline is replaced, not passed on.
	if late, _ := sc.Scrub("password=" + secret); late != tooLargeMark {
		t.Errorf("a text begun after the deadline was %q", late)
	}
	// With no deadline reached, a text over the limit is clipped in the middle.
	got2, _ := NewScrubber().WithDeadline(time.Now().Add(time.Minute)).Scrub(strings.Repeat("a line of words\n", 200000))
	if !strings.Contains(got2, "[clipped]") || len(got2) > scrubMaxText+64 {
		t.Errorf("a long text was not clipped: %d bytes", len(got2))
	}
	// Scrubbing the output again changes nothing.
	if again, _ := NewScrubber().Scrub(got2); again != got2 {
		t.Error("the bounded output is not stable")
	}
}

// What a baton is built from is cut to its own maximum before it is scrubbed: a reply of
// megabytes does not cost megabytes.
func TestABatonBuiltFromAHugeReplyIsCutBeforeItIsScrubbed(t *testing.T) {
	// dotted names are slow to scrub, so a reply that was not cut first would spend the budget
	huge := strings.Repeat("a.key=b,c.pass=d;e={key:f,pw:g};", 5<<20/32)
	start := time.Now()
	b := Build(BuildInput{Activity: Activity{LastReply: huge, Commands: nil}, Pane: Pane{Task: huge}, Now: time.Now()})
	if d := time.Since(start); d > time.Second {
		t.Errorf("Build took %s", d)
	}
	if len(b.Sections[Goal]) > maxGoal+64 || len(b.Sections[Standing]) > maxReply+2048 {
		t.Errorf("goal %d bytes, standing %d bytes", len(b.Sections[Goal]), len(b.Sections[Standing]))
	}
}

// frozenBareRe is the expression the bare names were first found with. A quote in front of a
// name is itself the boundary, as in it; the hand scan has to find everything it did.
var frozenBareRe = regexp.MustCompile(`(?m)(?:^|[\s,;{?&"'(])["']?((?i:pass|passwd|pwd|pw|key))["']?[ \t]*[:=][ \t]*("[^"\n]*"|'[^'\n]*'|[^\s"'&,;]+)`)

// A name after a quote is found wherever the quote is.
func TestABareNameAfterAQuoteIsFoundWhateverComesBeforeTheQuote(t *testing.T) {
	for _, in := range []string{`$"key":"Hunter2abcdefgh"`, `x."pw": "Hunter2abcdefgh"`, `cfg."pass":"Hunter2abcdefgh"`, `a@'key'='Hunter2abcdefgh'`} {
		taken(t, in, []string{"Hunter2abcdefgh"}, nil)
	}
}

// 300000 random texts, quotes included: nothing the frozen expression matches is lost.
func TestTheHandScanLosesNothingTheFrozenExpressionFound(t *testing.T) {
	r := rand.New(rand.NewSource(12))
	pieces := []string{"key", "KEY", "Pass", "pw", "pwd", "passwd", "pass", "=", ":", " ", "  ", "\t", "\n", "\"", "'", ",", ";", "{", "(", "?", "&", ".", "$", "@", "[", "a", "Zk3", "Hunter2", "value", "-", "_"}
	for i := 0; i < 300000; i++ {
		var b strings.Builder
		for k, n := 0, 2+r.Intn(10); k < n; k++ {
			b.WriteString(pieces[r.Intn(len(pieces))])
		}
		text := b.String()
		have := map[[4]int]bool{}
		for _, m := range passMatches(text) {
			have[[4]int{m[2], m[3], m[4], m[5]}] = true
		}
		for _, m := range frozenBareRe.FindAllStringSubmatchIndex(text, -1) {
			if !have[[4]int{m[2], m[3], m[4], m[5]}] {
				t.Fatalf("lost a match of the frozen expression in %q: name %q value %q", text, text[m[2]:m[3]], text[m[4]:m[5]])
			}
		}
	}
}

// A key wrapped over lines that a cut would fall in is still seen whole: a text of any size up
// to the limit is scrubbed as one, not in pieces. 276 wrapped keys are placed so that the
// 256 KB marks of the earlier piece size fall among them.
func TestWrappedKeysAcrossWhatWereOncePieceBoundariesAreAllTaken(t *testing.T) {
	// Keys that are only seen as keys together with the line that names them: the lines after
	// it, scrubbed alone, are kept. A cut between them is what the test is about.
	leaks := func(sc *Scrubber, text string, chunks []string) bool {
		got, _ := sc.Scrub(text)
		for _, ch := range chunks {
			if len(ch) >= 8 && strings.Contains(got, ch) {
				return true
			}
		}
		return false
	}
	var cases []wrapCase
	sc0 := NewScrubber()
	for _, c := range wrapCases(9000) {
		if len(c.chunks) < 3 || strings.Contains(c.text, "\r") || leaks(sc0, c.text, c.chunks) {
			continue
		}
		if rest := strings.SplitN(c.text, "\n", 2); len(rest) == 2 && leaks(sc0, rest[1], c.chunks[1:]) {
			cases = append(cases, c)
		}
	}
	if len(cases) < 30 {
		t.Fatalf("only %d keys that depend on their first line", len(cases))
	}
	cases = cases[:min(len(cases), 276)]
	var b strings.Builder
	filler := "an ordinary line of a long text that holds nothing secret\n"
	per := len(cases)/3 + 1
	for k, c := range cases {
		// filler up to half the keys short of each multiple of 256 KB, then the keys one after another:
		// the cut that pieces would make falls among them
		if k%per == 0 {
			for b.Len() < (k/per+1)*(256<<10)-per*55 {
				b.WriteString(filler)
			}
		}
		b.WriteString(c.text + "\n\n")
	}
	text := b.String()
	if len(text) > scrubMaxText {
		t.Skipf("the text is %d bytes", len(text))
	}
	for _, sc := range []*Scrubber{NewScrubber(), NewScrubber().WithDeadline(time.Now().Add(time.Minute))} {
		got, _ := sc.Scrub(text)
		leaks := 0
		for _, c := range cases {
			for _, part := range c.chunks {
				if len(part) >= 8 && strings.Contains(got, part) {
					leaks++
					break
				}
			}
		}
		if leaks > 0 {
			t.Errorf("%d of %d wrapped keys leaked", leaks, len(cases))
		}
	}
}

// When the budget of a build is spent before the end, the id, folder and branch are still
// there, and a goal that was not reached is not the title.
func TestABuildThatRanOutOfBudgetKeepsItsHeader(t *testing.T) {
	old := buildBudget
	buildBudget = -time.Second
	t.Cleanup(func() { buildBudget = old })
	b := Build(BuildInput{Pane: Pane{ID: "p1", Name: "worker", Cwd: "/work/repo", Branch: "feature", Task: "finish the parser"}, Now: time.Now()})
	if !ValidID(b.ID) {
		t.Errorf("the id is %q", b.ID)
	}
	if b.Cwd != "/work/repo" || b.Branch != "feature" || b.FromPane != "p1" {
		t.Errorf("header: cwd %q branch %q pane %q", b.Cwd, b.Branch, b.FromPane)
	}
	if b.Title != "Baton from worker" {
		t.Errorf("the title is %q", b.Title)
	}
	if strings.Contains(b.Sections[Goal], "finish the parser") {
		t.Error("a goal past the budget was passed on")
	}
}

// A value of any length has its first bareValueMax bytes taken and the search goes on after
// them: the cap acted, and did not take the whole of 10,000 bytes.
func TestABareValueIsTakenUpToTheCapAndNoFurther(t *testing.T) {
	text := "key=" + strings.Repeat("A1", 5000)
	var found [][]int
	for _, m := range passMatches(text) {
		if m[2] == 0 {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d matches for the name at the start", len(found))
	}
	if got := found[0][5] - found[0][4]; got != bareValueMax {
		t.Errorf("the value taken is %d bytes, want %d", got, bareValueMax)
	}
}
