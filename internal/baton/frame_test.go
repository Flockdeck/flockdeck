package baton

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/jmwri/flockdeck/internal/agent"
)

// bigBaton is a baton far over the budget in every section.
func bigBaton() Baton {
	var files, cmds, notIn strings.Builder
	for i := 0; i < 300; i++ {
		files.WriteString("- `internal/pkg/file_number_" + strings.Repeat("x", 20) + ".go` (modified, +10 -2)\n")
		cmds.WriteString("- `go test ./internal/pkg" + strings.Repeat("y", 10) + " -run TestSomething` - FAILED: assertion failed\n")
		notIn.WriteString("- `internal/other/file_" + strings.Repeat("z", 20) + ".go`\n")
	}
	b := sample()
	b.Sections = map[Section]string{
		Goal:          "Finish the retry work.",
		Standing:      "The last thing the agent said:\n\n> " + strings.Repeat("it is nearly done and ", 80),
		Decisions:     "- Cap the backoff at 5 s: the proxy times out at 6.",
		Files:         strings.TrimSpace(files.String()),
		Commands:      strings.TrimSpace(cmds.String()),
		Questions:     "- Does the proxy retry on its own?",
		Constraints:   "- Do not touch the migration.",
		NotInCheckout: strings.TrimSpace(notIn.String()),
	}
	return b
}

func TestFrameShortBatonIsWholeAndTaskFollows(t *testing.T) {
	b := sample()
	f := Frame(b, FrameOptions{Task: "The token refresh part is yours."})
	if f.Truncated {
		t.Error("a short baton was cut")
	}
	for _, want := range []string{`<baton id="` + b.ID + `">`, "claims to check", "## Goal", "Keep the public API.", "## Decisions and why", "flockdeck baton show " + b.ID} {
		if !strings.Contains(f.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, f.Prompt)
		}
	}
	// The task is after the baton and outside it, whole.
	i, j := strings.Index(f.Prompt, "</baton>"), strings.Index(f.Prompt, "The token refresh part is yours.")
	if i < 0 || j < i {
		t.Errorf("task is not after the baton: baton ends %d, task at %d", i, j)
	}
	// A section that still holds the placeholder is not sent.
	f = Frame(b.Set(Questions, Placeholder), FrameOptions{})
	if strings.Contains(f.Prompt, "Open questions") || strings.Contains(f.Prompt, Placeholder) {
		t.Errorf("a placeholder was sent:\n%s", f.Prompt)
	}
}

func TestFrameStaysInBudgetAndKeepsWhatMattersMost(t *testing.T) {
	b := bigBaton()
	f := Frame(b, FrameOptions{Task: "continue", Pointer: "/repo/.git/flockdeck/baton-x.md"})
	if !f.Truncated {
		t.Fatal("an oversize baton was not marked cut")
	}
	if n := len(f.Prompt); n > BudgetBaton+len("\ncontinue\n") {
		t.Errorf("prompt is %d bytes, budget %d", n, BudgetBaton)
	}
	for _, want := range []string{"Finish the retry work.", "Do not touch the migration.", "Does the proxy retry", "Cap the backoff"} {
		if !strings.Contains(f.Prompt, want) {
			t.Errorf("the cut dropped %q", want)
		}
	}
	for _, want := range []string{leftOutMark, "Files touched (shortened)", "/repo/.git/flockdeck/baton-x.md", "flockdeck baton show"} {
		if !strings.Contains(f.Prompt, want) {
			t.Errorf("prompt does not say %q", want)
		}
	}
	// The full text is what the overflow file gets.
	if !strings.Contains(f.Full, strings.Repeat("x", 20)) || strings.Count(f.Full, "file_number_") != 300 {
		t.Error("Full is not the whole baton")
	}
	// Lists are cut at a line, never in the middle of one.
	for _, line := range strings.Split(f.Prompt, "\n") {
		if strings.HasPrefix(line, "- `internal/pkg/file_number_") && !strings.HasSuffix(line, "(modified, +10 -2)") {
			t.Errorf("a line was cut: %q", line)
		}
	}
}

func TestFrameDropsWholeSectionsWhenListsAreNotEnough(t *testing.T) {
	b := bigBaton().Set(Standing, strings.Repeat("long prose that goes on. ", 400))
	b = b.Set(Decisions, strings.Repeat("- a decision with its reason\n", 100))
	f := Frame(b, FrameOptions{})
	if len(f.Prompt) > BudgetBaton {
		t.Fatalf("prompt is %d bytes", len(f.Prompt))
	}
	if strings.Contains(f.Prompt, "## Where things stand") {
		t.Error("the standing prose should go before the goal and constraints")
	}
	if !strings.Contains(f.Prompt, "## Goal") || !strings.Contains(f.Prompt, "## Constraints") {
		t.Error("goal or constraints were dropped first")
	}
	// A section is there whole or not at all.
	if strings.Contains(f.Prompt, "## Decisions and why") {
		d := f.Prompt[strings.Index(f.Prompt, "## Decisions and why"):]
		if got := strings.Count(d[:strings.Index(d, "## Open")], "- a decision"); got != 100 {
			t.Errorf("decisions were cut to %d items", got)
		}
	}
}

func TestFrameWithOnlyAHugeGoalCutsAtALine(t *testing.T) {
	var lines []string
	for i := 0; i < 2000; i++ {
		lines = append(lines, "goal line "+strings.Repeat("g", 30))
	}
	b := sample()
	b.Sections = map[Section]string{Goal: strings.Join(lines, "\n")}
	f := Frame(b, FrameOptions{})
	if len(f.Prompt) > BudgetBaton || !f.Truncated {
		t.Fatalf("prompt %d bytes, truncated %v", len(f.Prompt), f.Truncated)
	}
	for _, l := range strings.Split(f.Prompt, "\n") {
		if strings.HasPrefix(l, "goal line") && l != "goal line "+strings.Repeat("g", 30) {
			t.Errorf("a line was cut: %q", l)
		}
	}
}

func TestFrameShrinksToFitALargeTask(t *testing.T) {
	task := strings.Repeat("t", 20<<10)
	f := Frame(bigBaton(), FrameOptions{Task: task})
	// The task is kept whole; the baton gets what is left, and no less than
	// the floor.
	if !strings.Contains(f.Prompt, task) {
		t.Error("the task was cut")
	}
	if n := len(f.Prompt) - len(task); n > BudgetTotal-len(task) && n > minRoom+64 {
		t.Errorf("baton part is %d bytes with a %d byte task", n, len(task))
	}
}

// winCommandLineLen is the length of a command line as Windows is given it,
// quoting each argument the way syscall.EscapeArg does, counted in UTF-16 code
// units, which is what CreateProcess limits.
func winCommandLineLen(argv []string) int {
	n := 0
	for i, a := range argv {
		if i > 0 {
			n++
		}
		n += len(utf16.Encode([]rune(winEscape(a))))
	}
	return n
}

func winEscape(s string) string {
	if s == "" {
		return `""`
	}
	needs := strings.ContainsAny(s, " \t\"")
	var b strings.Builder
	if needs {
		b.WriteByte('"')
	}
	slashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
			b.WriteRune(r)
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, slashes+1))
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
		slashes = 0
	}
	if needs {
		b.WriteString(strings.Repeat(`\`, slashes))
		b.WriteByte('"')
	}
	return b.String()
}

// Windows refuses a command line over 32,767 UTF-16 units. The largest task a
// spawn accepts (maxTaskBytes in internal/workspace, 16 KiB) with the largest
// baton still has to leave the agent's own arguments and a pane briefing room.
func TestWindowsCommandLineStaysUnderTheLimit(t *testing.T) {
	const maxTaskBytes = 16 << 10
	const limit = 32767
	// What an agent with no hooks is briefed with in front of the task, and
	// the rest of its argument list, with room to spare.
	const briefing = 6 << 10
	var claude agent.Spec
	for _, s := range agent.Builtins() {
		if s.ID == "claude" {
			claude = s
		}
	}
	if claude.ID == "" {
		t.Fatal("no claude agent in the catalog")
	}
	// Prose with the quote density of real text.
	task := strings.Repeat(`He said "go" and left, so use C:\work\dir\ next. `, 400)[:maxTaskBytes]
	for name, b := range map[string]Baton{"big": bigBaton(), "huge goal": sample().Set(Goal, strings.Repeat(`a "quoted" line `+"\n", 4000))} {
		f := Frame(b, FrameOptions{Task: task, Pointer: `C:\Users\someone\Documents\repos\project\.git\flockdeck\baton-` + b.ID + ".md"})
		argv := agent.BuildArgv(claude, false, agent.Tokens{Prompt: f.Prompt, Session: "11111111-2222-3333-4444-555555555555", Model: "opus", Cwd: `C:\work`, Pane: "p"})
		got := winCommandLineLen(argv) + briefing
		if got > limit {
			t.Errorf("%s: command line is %d units with the briefing, limit %d", name, got, limit)
		}
		if len(f.Prompt) > BudgetTotal+64 {
			t.Errorf("%s: prompt is %d bytes, total budget %d", name, len(f.Prompt), BudgetTotal)
		}
	}
}

func TestOverflowFileGoesInAPrivateGitFolder(t *testing.T) {
	if p := OverflowPath(t.TempDir(), sample().ID); p != "" {
		t.Errorf("a folder outside any repository got %q", p)
	}
	path := filepath.Join(t.TempDir(), "flockdeck", "baton-x.md")
	if err := WriteOverflow(path, "full text"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "full text" {
		t.Errorf("read back %q, %v", data, err)
	}
}
