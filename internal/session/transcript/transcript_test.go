package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// claudeSpec is the catalog's Claude entry as far as this package cares about
// it. What is not here -- the argv, the models, the install line -- decides
// how a pane is started, not where its conversation was written.
var claudeSpec = agent.Spec{ID: "claude", Runner: agent.RunnerCLI, Exe: "claude",
	Caps: agent.Caps{Hooks: true, Resume: true, Transcript: true, Trust: true, Context: agent.ContextHook}}

// TestForChoosesAReaderFromTheSpec covers the decision every caller makes
// before it reads anything, and in particular that an agent Flockdeck knows
// nothing about is given the null reader rather than a guess at where its
// conversations might be.
func TestForChoosesAReaderFromTheSpec(t *testing.T) {
	cases := []struct {
		name string
		spec agent.Spec
		want Reader
	}{
		{"claude", claudeSpec, Claude{}},
		{"a CLI that records nothing Flockdeck reads",
			agent.Spec{ID: "codex", Runner: agent.RunnerCLI, Caps: agent.Caps{Transcript: true}}, Null{}},
		{"a CLI with no transcript at all", agent.Spec{ID: "aider", Runner: agent.RunnerCLI}, Null{}},
		// Transcript is what gates all of this, so an agent that claims none
		// gets the null reader whatever else it is.
		{"an agent that claims no transcript", agent.Spec{ID: "claude", Runner: agent.RunnerCLI, Exe: "claude"}, Null{}},
		{"a program with no reader that says it has a transcript", agent.Spec{ID: "local", Runner: agent.RunnerCLI, Exe: "local", Caps: agent.Caps{Transcript: true}}, Null{}},
		{"a shell pane, which is no agent at all", agent.Spec{}, Null{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := For(c.spec); got != c.want {
				t.Errorf("For(%+v) = %T, want %T", c.spec, got, c.want)
			}
		})
	}
}

// TestNullReaderAnswersNothing pins the answers every caller already copes
// with: a fan-out falls back to the screen, the history overlay lists nothing,
// and resume is not attempted.
func TestNullReaderAnswersNothing(t *testing.T) {
	spec := agent.Spec{ID: "codex"}
	if got := (Null{}).Path(spec, "11111111-1111-1111-1111-111111111111"); got != "" {
		t.Errorf("Path = %q, want nothing", got)
	}
	if got := (Null{}).Replies(spec, "11111111-1111-1111-1111-111111111111", 4); got != nil {
		t.Errorf("Replies = %#v, want nothing", got)
	}
	got, err := (Null{}).Conversations(spec, t.TempDir())
	if err != nil {
		t.Errorf("Conversations reported %v; having no transcript is not a failure", err)
	}
	if len(got) != 0 {
		t.Errorf("Conversations = %#v, want nothing", got)
	}
}

// TestExistsDecidesWhetherResumeIsWorthAttempting covers what a restored
// layout hangs on. An agent asked to resume a conversation that is not there
// refuses and exits, so a wrong answer here kills every pane in the layout at
// once.
func TestExistsDecidesWhetherResumeIsWorthAttempting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", "C--code-repo")

	const held = "11111111-1111-1111-1111-111111111111"
	writeTranscript(t, dir, held, `{"type":"user","message":{"role":"user","content":"hello"}}`)

	// A session interrupted before it recorded anything leaves an empty file,
	// which Claude Code refuses exactly as it refuses a missing one.
	const empty = "22222222-2222-2222-2222-222222222222"
	if err := os.WriteFile(filepath.Join(dir, empty+".jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if !Exists(claudeSpec, held) {
		t.Error("a stored conversation was reported as not there")
	}
	if Exists(claudeSpec, empty) {
		t.Error("an empty transcript was offered as a conversation to resume")
	}
	if Exists(claudeSpec, "33333333-3333-3333-3333-333333333333") {
		t.Error("a conversation that was never held was reported as stored")
	}

	noResume := claudeSpec
	noResume.Caps.Resume = false
	if Exists(noResume, held) {
		t.Error("an agent that cannot reattach a conversation was told it had one")
	}
}

// TestAPaneOnAnotherAccountIsReadFromThatAccount covers a catalog entry for
// Claude that sets CLAUDE_CONFIG_DIR, which is how a second account is run.
// The pane's transcripts are written under that folder, and reading
// Flockdeck's own instead found nothing: a restart started the conversation
// afresh and sent the task again, the history listed the other account's, and
// a fan-out could not read the plan.
func TestAPaneOnAnotherAccountIsReadFromThatAccount(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "repo")
	own := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", own)
	other := t.TempDir()
	const id = "44444444-4444-4444-4444-444444444444"
	path := writeTranscript(t, filepath.Join(other, "projects", projectSlug(cwd)), id,
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"on the other account"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"- the plan"}]}}`)

	spec := claudeSpec
	// The last setting of a name is the one the pane gets.
	spec.Env = []string{"CLAUDE_CONFIG_DIR=" + own, "CLAUDE_CONFIG_DIR=" + other}

	if got := For(spec).Path(spec, id); got != path {
		t.Errorf("Path = %q, want the pane's account's %q", got, path)
	}
	if !Exists(spec, id) {
		t.Error("the pane's conversation was not found to resume")
	}
	if got := For(spec).Replies(spec, id, 1); len(got) != 1 || !strings.Contains(got[0], "the plan") {
		t.Errorf("Replies = %#v, want what the agent said on the pane's account", got)
	}
	convs, err := For(spec).Conversations(spec, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 1 || convs[0].ID != id {
		t.Errorf("Conversations = %+v, want the pane's account's", convs)
	}

	// An entry that does not move the folder is read where Flockdeck's own is.
	if Exists(claudeSpec, id) {
		t.Error("a conversation on another account was found on Flockdeck's own")
	}
}

// TestAllLabelsEveryConversationWithItsAgent covers the history overlay's
// list. It draws several agents' conversations in one place, so each row has
// to say whose it is, and the whole list has to be in one order rather than
// one order per agent.
func TestAllLabelsEveryConversationWithItsAgent(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "myrepo")

	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	claudeDir := filepath.Join(home, "projects", projectSlug(cwd))
	writeTranscript(t, claudeDir, "11111111-1111-1111-1111-111111111111",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"the claude one"}}`)

	now := time.Now()
	touch(t, filepath.Join(claudeDir, "11111111-1111-1111-1111-111111111111.jsonl"), now.Add(-time.Hour))

	other := agent.Spec{ID: "other", Runner: agent.RunnerCLI, Exe: "other", Caps: agent.Caps{Transcript: true}}
	readers["other"] = fixedReader{Conversation{ID: "22222222-2222-2222-2222-222222222222", Summary: "the other one", Modified: now}}
	t.Cleanup(func() { delete(readers, "other") })

	// An agent that records nothing contributes nothing, and must not stop the
	// two that do from being listed.
	specs := []agent.Spec{claudeSpec, other, {ID: "codex", Runner: agent.RunnerCLI}}
	got, err := All(specs, cwd)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d conversations, want 2: %+v", len(got), got)
	}
	if got[0].Agent != "other" || got[0].Summary != "the other one" {
		t.Errorf("first row = %+v, want the most recent, from the other agent", got[0])
	}
	if got[1].Agent != "claude" || got[1].Summary != "the claude one" {
		t.Errorf("second row = %+v, want Claude's", got[1])
	}
}

// fixedReader stands in for an agent whose store holds the conversations it is
// made from.
type fixedReader []Conversation

func (fixedReader) Path(agent.Spec, string) string           { return "" }
func (fixedReader) Replies(agent.Spec, string, int) []string { return nil }
func (r fixedReader) Conversations(spec agent.Spec, _ string) ([]Conversation, error) {
	return label(spec.ID, append([]Conversation(nil), r...)), nil
}

// failingReader stands in for an agent whose store cannot be read at all.
type failingReader struct{}

func (failingReader) Path(agent.Spec, string) string           { return "" }
func (failingReader) Replies(agent.Spec, string, int) []string { return nil }
func (failingReader) Conversations(agent.Spec, string) ([]Conversation, error) {
	return nil, errors.New("read chats: permission denied")
}

// TestAllKeepsWhatItFoundWhenOneAgentCannotBeRead covers a store that cannot
// be opened -- a folder whose permissions have been changed, a state directory
// on a disconnected drive. Saying so is right; saying so instead of the
// conversations the other agents found leaves somebody looking for work they
// know they did.
func TestAllKeepsWhatItFoundWhenOneAgentCannotBeRead(t *testing.T) {
	broken := agent.Spec{ID: "broken", Runner: agent.RunnerCLI, Caps: agent.Caps{Transcript: true}}
	readers[broken.ID] = failingReader{}
	t.Cleanup(func() { delete(readers, broken.ID) })

	cwd := filepath.Join(t.TempDir(), "myrepo")
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	writeTranscript(t, filepath.Join(home, "projects", projectSlug(cwd)), "11111111-1111-1111-1111-111111111111",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"still here"}}`)

	got, err := All([]agent.Spec{broken, claudeSpec}, cwd)
	if err == nil {
		t.Error("an agent whose store could not be read was passed over in silence")
	}
	if len(got) != 1 || got[0].Summary != "still here" {
		t.Errorf("listed %+v, want the conversations the readable agent found", got)
	}
}

// TestAgentsIsTheCatalog covers what the history overlay lists: the catalog's
// agents, so that one with a reader is offered.
func TestAgentsIsTheCatalog(t *testing.T) {
	base := t.TempDir()
	t.Setenv("APPDATA", base)
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)

	var claude bool
	for _, spec := range Agents() {
		if _, ok := For(spec).(Claude); ok {
			claude = true
		}
	}
	if !claude {
		t.Error("Agents() does not read Claude's store")
	}
}
