package transcript

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// fixtureSessionID is the session id the checked-in fixture under
// testdata/claude_stream/home is filed under.
const fixtureSessionID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// copyFixtureHome copies the checked-in synthetic fixture -- never a real
// transcript, see testdata/claude_stream/home -- into a directory the test
// can grow, so appending the rest of the fixture's deliberately truncated
// last line never touches the checked-in copy.
func copyFixtureHome(t *testing.T) string {
	t.Helper()
	src := filepath.Join("testdata", "claude_stream", "home")
	dst := filepath.Join(t.TempDir(), "home")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dst
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// fixtureSpec is a Claude Code agent pointed at the fixture's copied home.
func fixtureSpec(home string) agent.Spec {
	return agent.Spec{
		ID: "claude", Exe: "claude",
		Caps: agent.Caps{Hooks: true, Resume: true, Transcript: true},
		Env:  []string{"CLAUDE_CONFIG_DIR=" + home},
	}
}

func entryByID(entries []Entry, id string) (Entry, bool) {
	for _, e := range entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

func hugeGrepText() string {
	lines := make([]string, 200)
	for i := range lines {
		n := i + 1
		lines[i] = fmt.Sprintf("push.go:%d: TODO retry case %d", n, n)
	}
	return strings.Join(lines, "\n")
}

// TestClaudeStreamTranslatesEveryLineKind is the core adapter test: without
// the Claude Code translator (claude_stream.go), StreamFor answers ok=false
// for a claude Spec and none of the assertions below have anything to run
// against -- this is what proves the fixture's every line kind (§1 of the
// design) actually turns into the shared Entry shape the protocol carries.
func TestClaudeStreamTranslatesEveryLineKind(t *testing.T) {
	home := copyFixtureHome(t)
	spec := fixtureSpec(home)

	stream, ok := StreamFor(spec, fixtureSessionID)
	if !ok {
		t.Fatal("StreamFor: claude Spec answered unsupported")
	}
	stream.Refresh()
	entries := stream.Snapshot()

	prompt, ok := entryByID(entries, "u-prompt-1")
	if !ok || prompt.Kind != KindPrompt || prompt.Text != "Add retry logic to push.go" {
		t.Errorf("prompt entry: got %+v, ok=%v", prompt, ok)
	}

	reply, ok := entryByID(entries, "a-reply-1:0")
	if !ok || reply.Kind != KindReply || reply.Markdown != "Sure, I'll take a look." {
		t.Errorf("reply entry: got %+v, ok=%v", reply, ok)
	}

	thinkingText := "Retries need backoff and jitter so a flaky network does not hammer the remote."
	thinking, ok := entryByID(entries, "a-thinking-1:0")
	if !ok || thinking.Kind != KindThinking || thinking.Chars != len([]rune(thinkingText)) {
		t.Errorf("thinking entry: got %+v, ok=%v, want chars=%d", thinking, ok, len([]rune(thinkingText)))
	}
	if detail, ok := stream.Detail("a-thinking-1:0"); !ok || detail.Text != thinkingText {
		t.Errorf("thinking detail: got %+v, ok=%v", detail, ok)
	}

	toolOK, ok := entryByID(entries, "toolu_bash_ok")
	if !ok || toolOK.Kind != KindTool || toolOK.Name != "Bash" || toolOK.Status != StatusOK ||
		toolOK.Label != "Ran go test ./..." || toolOK.Summary != "ok" || toolOK.HasDetail {
		t.Errorf("ok tool entry: got %+v, ok=%v", toolOK, ok)
	}

	toolErr, ok := entryByID(entries, "toolu_bash_err")
	if !ok || toolErr.Status != StatusError {
		t.Errorf("error tool entry: got %+v, ok=%v", toolErr, ok)
	}

	edit, ok := entryByID(entries, "toolu_edit_1")
	if !ok || edit.Kind != KindTool || edit.Name != "Edit" || edit.Label != "Edited push.go" || edit.Status != StatusOK {
		t.Errorf("edit entry: got %+v, ok=%v", edit, ok)
	}
	if !strings.Contains(edit.Diff, "-\treturn do()") || !strings.Contains(edit.Diff, "+\treturn retry(do)") {
		t.Errorf("edit diff missing expected lines: %q", edit.Diff)
	}
	if edit.HasDetail {
		t.Errorf("a small inline diff should not also claim hasDetail")
	}

	huge, ok := entryByID(entries, "toolu_grep_huge")
	if !ok || !huge.HasDetail || huge.Summary == "" {
		t.Errorf("huge tool result should summarize with hasDetail: got %+v, ok=%v", huge, ok)
	}
	want := hugeGrepText()
	if len(want) <= ToolOutputCap {
		t.Fatalf("test fixture no longer exceeds the cap: %d bytes", len(want))
	}
	detail, ok := stream.Detail("toolu_grep_huge")
	if !ok || detail.Text != want {
		t.Errorf("huge tool detail mismatch: ok=%v, got %d bytes, want %d", ok, len(detail.Text), len(want))
	}

	sub, ok := entryByID(entries, "toolu_agent_1")
	if !ok || sub.Kind != KindSubagent || sub.Description != "Explore repo structure" || sub.Status != StatusOK || !sub.HasDetail {
		t.Errorf("subagent entry: got %+v, ok=%v", sub, ok)
	}
	if !strings.Contains(sub.Report, "Found 3 relevant files") {
		t.Errorf("subagent report: got %q", sub.Report)
	}
	subDetail, ok := stream.Detail("toolu_agent_1")
	if !ok || len(subDetail.Entries) != 2 {
		t.Fatalf("subagent detail: ok=%v, entries=%+v", ok, subDetail.Entries)
	}
	if subDetail.Entries[0].Kind != KindPrompt || subDetail.Entries[0].Text != "Explore the repo and report what you find." {
		t.Errorf("subagent detail[0]: %+v", subDetail.Entries[0])
	}
	if subDetail.Entries[1].Kind != KindReply || !strings.Contains(subDetail.Entries[1].Markdown, "push.go, pull.go, retry.go") {
		t.Errorf("subagent detail[1]: %+v", subDetail.Entries[1])
	}

	if _, dropped := entryByID(entries, "u-reminder-1"); dropped {
		t.Error("a system-reminder entry should be dropped outright, not sent as a notice or prompt")
	}

	notice, ok := entryByID(entries, "u-slash-1")
	if !ok || notice.Kind != KindNotice || !strings.Contains(notice.Text, "<command-name>/help</command-name>") {
		t.Errorf("slash-command notice: got %+v, ok=%v", notice, ok)
	}

	compaction, ok := entryByID(entries, "c-compact-1")
	if !ok || compaction.Kind != KindCompaction {
		t.Errorf("compaction entry: got %+v, ok=%v", compaction, ok)
	}

	if _, found := entryByID(entries, "u-truncated-1"); found {
		t.Error("the deliberately truncated last line must not be parsed until it is completed")
	}
}

// TestClaudeStreamHoldsBackAPartialLastLine completes the fixture's
// deliberately truncated last line and checks it is only parsed once it is
// whole -- the core test for tailing a file that is still being written to.
func TestClaudeStreamHoldsBackAPartialLastLine(t *testing.T) {
	home := copyFixtureHome(t)
	spec := fixtureSpec(home)
	path := filepath.Join(home, "projects", "proj1", fixtureSessionID+".jsonl")

	stream, ok := StreamFor(spec, fixtureSessionID)
	if !ok {
		t.Fatal("StreamFor: unsupported")
	}
	stream.Refresh()
	if _, found := entryByID(stream.Snapshot(), "u-truncated-1"); found {
		t.Fatal("truncated line parsed before it was completed")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	changed := stream.Refresh()
	entry, found := entryByID(changed, "u-truncated-1")
	if !found {
		t.Fatalf("completed line not parsed on next refresh; changed=%+v", changed)
	}
	if entry.Kind != KindPrompt || entry.Text != "This line never finishes" {
		t.Errorf("completed entry: %+v", entry)
	}
	if _, found := entryByID(stream.Snapshot(), "u-truncated-1"); !found {
		t.Error("completed entry missing from Snapshot after Refresh")
	}
}

// TestUnsupportedAgentHasNoStream is the core test for "every other agent
// answers unsupported": without the Caps.Transcript / program-name gating in
// transcript.For, an agent nobody has written an adapter for would be handed
// a stream that reads nothing, rather than being told plainly that the phone
// has to fall back to the terminal for it.
func TestUnsupportedAgentHasNoStream(t *testing.T) {
	spec := agent.Spec{ID: "mystery-cli", Exe: "mystery"}
	if _, ok := StreamFor(spec, "any-session"); ok {
		t.Error("an agent with no adapter should answer unsupported")
	}
}

// TestClaudeStreamSupportedBeforeFirstPrompt covers a pane just opened, with
// no transcript on disk yet: it is still a Claude Code pane, so the chat view
// must not fall back to the terminal for it, only show nothing until the
// first line is written.
func TestClaudeStreamSupportedBeforeFirstPrompt(t *testing.T) {
	home := copyFixtureHome(t)
	spec := fixtureSpec(home)
	stream, ok := StreamFor(spec, "11111111-2222-3333-4444-555555555555")
	if !ok {
		t.Fatal("a claude Spec with no transcript yet should still be supported")
	}
	if got := stream.Refresh(); got != nil {
		t.Errorf("Refresh on a file that does not exist: got %v", got)
	}
	if got := stream.Snapshot(); len(got) != 0 {
		t.Errorf("Snapshot on a file that does not exist: got %v", got)
	}
}
