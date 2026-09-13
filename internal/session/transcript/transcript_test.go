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

// TestAgentsIsTheCatalog covers what the history overlay lists. It was Claude
// alone until something pointed it at the catalog, and nothing did, so an API
// agent's chats were recorded and never offered.
func TestAgentsIsTheCatalog(t *testing.T) {
	base := t.TempDir()
	t.Setenv("APPDATA", base)
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)

	var claude, api bool
	for _, spec := range Agents() {
		switch For(spec).(type) {
		case Claude:
			claude = true
		case Chat:
			api = true
		}
	}
	if !claude || !api {
		t.Errorf("Agents() reads Claude's store: %v, the chat client's: %v; want both", claude, api)
	}
}

// TestChatRepliesStartAtAClear covers /clear in a chat pane, which the chat
// client marks in the file rather than starting a new one. A fan-out asked
// after it read the plan the user had just cleared away.
func TestChatRepliesStartAtAClear(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	writeChats(t, map[string][]string{id: {
		`{"type":"user","text":"plan it"}`,
		`{"type":"assistant","text":"- the old plan"}`,
		`{"type":"clear"}`,
	}})
	if got := (Chat{}).Replies(chatSpec, id, 4); len(got) != 0 {
		t.Errorf("Replies = %q straight after a clear, want nothing", got)
	}

	writeChats(t, map[string][]string{id: {
		`{"type":"user","text":"plan it"}`,
		`{"type":"assistant","text":"- the old plan"}`,
		`{"type":"clear"}`,
		`{"type":"user","text":"plan again"}`,
		`{"type":"assistant","text":"- the new plan"}`,
	}})
	if got := (Chat{}).Replies(chatSpec, id, 4); len(got) != 1 || got[0] != "- the new plan" {
		t.Errorf("Replies = %q, want only what was said since the clear", got)
	}
}

// TestAChatWithNoModelGoesToAnAgentWithoutOne covers a chat whose pane asked
// for no model, which it only does when its agent has no default to ask for --
// a local endpoint left to choose. Put under the first API agent instead, it
// offered to carry on a local model's conversation through Anthropic.
func TestAChatWithNoModelGoesToAnAgentWithoutOne(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "myrepo")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const id = "11111111-1111-1111-1111-111111111111"
	writeChats(t, map[string][]string{id: {
		`{"type":"user","cwd":"` + jsonPath(cwd) + `","text":"hi"}`,
		`{"type":"assistant","cwd":"` + jsonPath(cwd) + `","text":"hello"}`,
	}})
	api := func(id, model string) agent.Spec {
		return agent.Spec{ID: id, Runner: agent.RunnerAPI, DefaultModel: model, Caps: agent.Caps{Transcript: true, Resume: true}}
	}
	got, err := All([]agent.Spec{api("anthropic", "claude-sonnet-5"), api("openai", "gpt-5"), api("local", "")}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Agent != "local" {
		t.Errorf("listed %+v, want the chat under the agent with no default model", got)
	}
}

// TestAllListsEachChatOnceUnderItsAgent covers the chat folder every API agent
// shares. Asked once per API agent, it listed every chat under every one of
// them, each row offering to resume it through a different endpoint.
func TestAllListsEachChatOnceUnderItsAgent(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "myrepo")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	line := func(typ, extra string) string {
		return `{"type":"` + typ + `","cwd":"` + jsonPath(cwd) + `","text":"hi"` + extra + `}`
	}
	writeChats(t, map[string][]string{
		// The model that answered says which agent offers it.
		"11111111-1111-1111-1111-111111111111": {line("user", ""), line("assistant", `,"model":"gpt-5"`)},
		"22222222-2222-2222-2222-222222222222": {line("user", ""), line("assistant", `,"model":"claude-sonnet-5"`)},
		// A recorded agent wins over the model: two agents can offer one.
		"33333333-3333-3333-3333-333333333333": {line("user", `,"agent":"openai"`), line("assistant", `,"model":"claude-sonnet-5"`)},
		// Nothing to go on leaves the first API agent.
		"44444444-4444-4444-4444-444444444444": {line("user", "")},
	})

	anthropic := chatSpec
	anthropic.Models = []agent.Model{{ID: "claude-sonnet-5"}}
	openai := agent.Spec{ID: "openai", Runner: agent.RunnerAPI, DefaultModel: "gpt-5", Caps: agent.Caps{Transcript: true, Resume: true}}
	got, err := All([]agent.Spec{claudeSpec, anthropic, openai}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]string{}
	for _, c := range got {
		if _, twice := held[c.ID]; twice {
			t.Errorf("%s is listed more than once", c.ID)
		}
		held[c.ID] = c.Agent
	}
	want := map[string]string{
		"11111111-1111-1111-1111-111111111111": "openai",
		"22222222-2222-2222-2222-222222222222": "anthropic",
		"33333333-3333-3333-3333-333333333333": "openai",
		"44444444-4444-4444-4444-444444444444": "anthropic",
	}
	for id, agentID := range want {
		if held[id] != agentID {
			t.Errorf("%s is listed under %q, want %q", id, held[id], agentID)
		}
	}
}

// BenchmarkFirstPromptOfAPaste measures tidying an opening prompt that was a
// pasted log a megabyte long, of which the history overlay shows 160
// characters.
func BenchmarkFirstPromptOfAPaste(b *testing.B) {
	paste := "here is the log:\n" + strings.Repeat("2026-09-13 12:00:00 INFO  request served in 3ms\n", 1<<20/48)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := firstPrompt(paste); len([]rune(got)) != 161 {
			b.Fatalf("got %d runes", len([]rune(got)))
		}
	}
}

// TestFirstPromptIsTidiedAsItWas holds the prompt shown for a row to what it
// was when the whole prompt was split into words and joined again: runs of
// white space of any kind made one space, and anything past 160 characters
// cut off with an ellipsis.
func TestFirstPromptIsTidiedAsItWas(t *testing.T) {
	joined := func(s string) string {
		s = strings.TrimSpace(s)
		if s == "" || isSyntheticPrompt(s) {
			return ""
		}
		s = strings.Join(strings.Fields(s), " ")
		if len([]rune(s)) > 160 {
			s = string([]rune(s)[:160]) + "…"
		}
		// A byte that is not UTF-8 was kept as it was in a short prompt and
		// made the replacement character in a long one; encoded as JSON for the
		// window it was the replacement character either way, and now is here.
		return string([]rune(s))
	}
	word := strings.Repeat("é", 159)
	for _, in := range []string{
		"", "   ", "fix the build", "  fix\tthe \n\n build  ",
		"a b c　d", // spaces a person does not type as such
		strings.Repeat("x", 160), strings.Repeat("x", 161), strings.Repeat("x", 500),
		word + " y", word + "  yz", word + "y", word + "\n",
		strings.Repeat("ab ", 100),
		"bad \xff\xfe bytes", strings.Repeat("\xff", 200),
		"<system-reminder>be nice</system-reminder>", "  <command-name>/cost</command-name>",
	} {
		if got, want := firstPrompt(in), joined(in); got != want {
			t.Errorf("firstPrompt(%q)\n = %q\nwant %q", in, got, want)
		}
	}
}
