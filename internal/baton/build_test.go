package baton

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// claudeSpec points Claude's state directory at one of the transcript package's
// own fixtures, so no test reads the real one.
func claudeSpec(t *testing.T, fixture string) agent.Spec {
	t.Helper()
	home, err := filepath.Abs(filepath.Join("..", "session", "transcript", "testdata", fixture, "home"))
	if err != nil {
		t.Fatal(err)
	}
	return agent.Spec{ID: "claude", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true, Resume: true}}
}

func fixtureEvents(t *testing.T) []transcript.ExportEvent {
	t.Helper()
	spec := claudeSpec(t, "claude_export")
	var evs []transcript.ExportEvent
	_, err := transcript.Claude{}.Follow(spec, "11111111-2222-3333-4444-555555555555").Poll(func(e transcript.ExportEvent) error {
		evs = append(evs, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

var when = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

func TestBuildFromAFollowedConversation(t *testing.T) {
	a := ActivityFromEvents(fixtureEvents(t))
	if len(a.Prompts) != 2 || a.LastReply != "Done: added a retry." {
		t.Fatalf("activity = %+v", a)
	}
	if len(a.Commands) != 1 || a.Commands[0].Text != "go test ./..." || a.Commands[0].Outcome != "interrupted" {
		t.Fatalf("commands = %+v", a.Commands)
	}
	b := Build(BuildInput{
		Pane:     Pane{ID: "p1", Name: "shop", Agent: "claude", Model: "opus", Cwd: `C:\work\shop`},
		Activity: a,
		Git: GitFacts{
			Branch: "feat/retry", Head: "a1b2c3d", Base: "main", Commits: 2,
			Committed:   []gitx.FileChange{{Path: "src/client.go", Added: 12, Removed: 3}},
			Uncommitted: []gitx.FileChange{{Path: "src/client_test.go", Label: "modified", Added: 4}, {Path: ".env", Label: "new"}},
		},
		Now: when,
	})
	// Goal is the first prompt, and the password it holds is gone.
	if g := b.Section(Goal); !strings.HasPrefix(g, "Add a retry to the fetch client.") || strings.Contains(g, "hunter2") {
		t.Errorf("goal = %q", g)
	}
	if !strings.HasPrefix(b.Title, "Add a retry") {
		t.Errorf("title = %q", b.Title)
	}
	if b.BaseCommit != "a1b2c3d (feat/retry, 2 commits ahead of main)" {
		t.Errorf("base = %q", b.BaseCommit)
	}
	if s := b.Section(Standing); !strings.Contains(s, "> Done: added a retry.") || !strings.Contains(s, "2 files uncommitted") {
		t.Errorf("standing = %q", s)
	}
	files := b.Section(Files)
	for _, want := range []string{"since main:", "`src/client.go` (+12 -3)", "`src/client_test.go` (modified, +4 -0)", "`.env` (new, secret file, contents not carried)"} {
		if !strings.Contains(files, want) {
			t.Errorf("files lack %q:\n%s", want, files)
		}
	}
	if c := b.Section(Commands); c != "- `go test ./...` - interrupted by the user" {
		t.Errorf("commands = %q", c)
	}
	if n := b.Section(NotInCheckout); !strings.Contains(n, "`src/client_test.go`") {
		t.Errorf("not in checkout = %q", n)
	}
	// What the builder cannot infer is left for a person.
	for _, s := range []Section{Decisions, Questions, Constraints} {
		if b.Section(s) != Placeholder {
			t.Errorf("%s = %q, want the placeholder", s, b.Section(s))
		}
	}
	if CountMarks(Render(b)) != RedactionCount(b.Redactions) || RedactionCount(b.Redactions) == 0 {
		t.Errorf("redactions = %+v", b.Redactions)
	}
}

func TestBuildFromStreamEntries(t *testing.T) {
	spec := claudeSpec(t, "claude_stream")
	st, ok := transcript.Claude{}.Stream(spec, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if !ok {
		t.Fatal("no stream")
	}
	st.Refresh()
	a := ActivityFromEntries(st.Snapshot())
	var texts []string
	for _, c := range a.Commands {
		texts = append(texts, c.Text+"="+c.Outcome)
	}
	if len(a.Prompts) == 0 || a.LastReply == "" {
		t.Errorf("activity = %+v", a)
	}
	if got := strings.Join(texts, "; "); !strings.Contains(got, "go test ./...") || !strings.Contains(got, "false=failed") {
		t.Errorf("commands = %s", got)
	}
}

func TestChatToolEntriesBecomeCommands(t *testing.T) {
	a := ActivityFromEntries([]transcript.Entry{
		{Kind: transcript.KindPrompt, Text: "build it"},
		{Kind: transcript.KindTool, Name: "read_file", Label: "Read a.go", Status: transcript.StatusOK},
		{Kind: transcript.KindTool, Name: "run_command", Label: "Ran go build ./...", Status: transcript.StatusError, Summary: "exit status 1"},
		{Kind: transcript.KindReply, Markdown: "it failed"},
	})
	if len(a.Commands) != 1 || a.Commands[0] != (Command{Text: "go build ./...", Outcome: "failed", Detail: "exit status 1"}) {
		t.Errorf("commands = %+v", a.Commands)
	}
}

func TestRepeatedCommandsCollapseAndFailuresStay(t *testing.T) {
	// A failure long ago, more distinct commands than the limit, then one
	// command run seven times.
	cmds := []Command{{Text: "make lint", Outcome: "failed", Detail: "lint error"}}
	for i := 0; i < 14; i++ {
		cmds = append(cmds, Command{Text: "echo " + string(rune('a'+i)), Outcome: "ok"})
	}
	for i := 0; i < 6; i++ {
		cmds = append(cmds, Command{Text: "go test ./...", Outcome: "failed", Detail: "FAIL"})
	}
	cmds = append(cmds, Command{Text: "go test ./...", Outcome: "ok"}, Command{Text: "gofmt -l ."})
	got := commandsSection(cmds, false, func(s string) string { return s })
	if !strings.Contains(got, "- `go test ./...` - run 7 times, last: ok") {
		t.Errorf("repeats not collapsed:\n%s", got)
	}
	if !strings.Contains(got, "- `make lint` - FAILED: lint error") {
		t.Errorf("an old failure was dropped:\n%s", got)
	}
	if strings.Contains(got, "`echo a`") || !strings.Contains(got, "1 older command") {
		t.Errorf("the oldest command was not left out and counted:\n%s", got)
	}
	if n := strings.Count(got, "\n") + 1; n > maxCommands+maxFailed+1 {
		t.Errorf("%d lines", n)
	}
}

func TestStrictBuildDropsCommandArguments(t *testing.T) {
	a := Activity{Commands: []Command{
		{Text: "go test ./internal/baton -run TestX", Outcome: "failed", Detail: "boom"},
		{Text: "curl -H 'X-Thing: not-a-known-secret-shape' https://example.com/hook", Outcome: "ok"},
		{Text: `C:\tools\deploy.exe --target prod`, Outcome: "ok"},
	}}
	b := Build(BuildInput{Activity: a, Strict: true, Now: when})
	got := b.Section(Commands)
	for _, want := range []string{"- `go test ...` - FAILED", "- `curl ...`", "- `deploy.exe ...`"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, leak := range []string{"not-a-known", "example.com", "TestX", "boom", "prod"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q survived the strict profile:\n%s", leak, got)
		}
	}
}

func TestHardenRewritesABatonBuiltLoosely(t *testing.T) {
	loose := sample().Set(Commands, "- `curl https://example.com/x?k=1` - FAILED: 500\n- `go test ./...` - run 3 times, last: ok\nfree text with a secret")
	got := Harden(loose).Section(Commands)
	want := "- `curl ...` - FAILED\n- `go test ...` - run 3 times, last: ok"
	if got != want {
		t.Errorf("Harden = %q, want %q", got, want)
	}
}

func TestBuildNotesALineageFromAnEarlierBaton(t *testing.T) {
	a := Activity{Prompts: []string{"<baton id=\"20260930-120000-ffffff\">\nstuff\n</baton>\n\nfinish the parser", "and the tests"}}
	b := Build(BuildInput{Activity: a, Pane: Pane{Task: "finish the parser"}, Now: when})
	if len(b.Derived) != 1 || b.Derived[0] != "20260930-120000-ffffff" {
		t.Errorf("derived = %v", b.Derived)
	}
	// With no task the first prompt is skipped, since it is the old baton.
	b = Build(BuildInput{Activity: a, Now: when})
	if b.Section(Goal) != "and the tests" {
		t.Errorf("goal = %q", b.Section(Goal))
	}
}

func TestBuildWithoutATranscriptOrGit(t *testing.T) {
	b := Build(BuildInput{Pane: Pane{Name: "scratch", Task: "tidy up"}, Git: GitFacts{Note: "git is not installed"}, Now: when})
	if b.Section(Goal) != "tidy up" || !strings.Contains(b.Section(Files), "git is not installed") {
		t.Errorf("baton = %+v", b.Sections)
	}
	if !strings.Contains(b.Section(Standing), "no reply") || b.Section(Commands) != "No commands recorded." {
		t.Errorf("standing = %q, commands = %q", b.Section(Standing), b.Section(Commands))
	}
}

func TestBuildScrubsWhatTheScrubberWasToldAndWhatItWasNot(t *testing.T) {
	a := Activity{Prompts: []string{"deploy with internal-token-value-99"}, LastReply: "I used gh" + "p_0123456789abcdefghijklmnopqrstuvwxyz"}
	b := Build(BuildInput{Activity: a, Scrubber: NewScrubber("internal-token-value-99"), Now: when})
	out := Render(b)
	if strings.Contains(out, "internal-token-value-99") || strings.Contains(out, "gh"+"p_0123") {
		t.Errorf("a secret reached the baton:\n%s", out)
	}
}
