package transcript

import (
	"errors"
	"os"
	"path/filepath"
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
		{"an API agent", agent.Spec{ID: "openai", Runner: agent.RunnerAPI, Caps: agent.Caps{Transcript: true}}, Chat{}},
		{"a CLI that records nothing Flockdeck reads",
			agent.Spec{ID: "codex", Runner: agent.RunnerCLI, Caps: agent.Caps{Transcript: true}}, Null{}},
		{"a CLI with no transcript at all", agent.Spec{ID: "aider", Runner: agent.RunnerCLI}, Null{}},
		// Transcript is what gates all of this, so an agent that claims none
		// gets the null reader whatever else it is.
		{"an API agent with no transcript", agent.Spec{ID: "local", Runner: agent.RunnerAPI}, Null{}},
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

	chatDir := writeChats(t, map[string][]string{
		"22222222-2222-2222-2222-222222222222": {
			`{"type":"user","ts":1,"cwd":"` + jsonPath(cwd) + `","text":"the api one"}`,
		},
	})

	now := time.Now()
	touch(t, filepath.Join(claudeDir, "11111111-1111-1111-1111-111111111111.jsonl"), now.Add(-time.Hour))
	touch(t, filepath.Join(chatDir, "22222222-2222-2222-2222-222222222222.jsonl"), now)

	// An agent that records nothing contributes nothing, and must not stop the
	// two that do from being listed.
	specs := []agent.Spec{claudeSpec, chatSpec, {ID: "codex", Runner: agent.RunnerCLI}}
	got, err := All(specs, cwd)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d conversations, want 2: %+v", len(got), got)
	}
	if got[0].Agent != "anthropic" || got[0].Summary != "the api one" {
		t.Errorf("first row = %+v, want the most recent, from the API agent", got[0])
	}
	if got[1].Agent != "claude" || got[1].Summary != "the claude one" {
		t.Errorf("second row = %+v, want Claude's", got[1])
	}
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

// TestAgentsStartsAtClaude pins what the history overlay lists before anything
// points it at the catalog: exactly the conversations this build's users
// already have.
func TestAgentsStartsAtClaude(t *testing.T) {
	specs := Agents()
	if len(specs) != 1 || specs[0].ID != "claude" {
		t.Fatalf("Agents() = %+v, want Claude alone", specs)
	}
	if got := For(specs[0]); got != (Claude{}) {
		t.Errorf("the default agent reads with %T, want the Claude reader", got)
	}
}
