package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
)

// recordingsState is a state directory of a test's own. The recordings command
// is given it, so nothing here reads or writes the real one.
func recordingsState(t *testing.T) func() (string, error) {
	t.Helper()
	dir := t.TempDir()
	return func() (string, error) { return dir, nil }
}

func TestRecordingsCommandListsAndNamesTheFolder(t *testing.T) {
	state := recordingsState(t)
	var out bytes.Buffer
	if err := listRecordings(nil, &out, state); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no recordings") {
		t.Errorf("an empty list says %q", out.String())
	}

	m := record.NewManager(state)
	t.Cleanup(m.Close)
	meta := record.Meta{Pane: "abcdef0123456789", PaneName: "api", Project: "shop", ProjectRoot: "/work/shop", Agent: "claude"}
	if err := m.Write(meta, transcript.ExportEvent{Time: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Kind: transcript.ExportPrompt, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	path := m.Path(meta.Pane)
	m.Finish(meta)

	out.Reset()
	if err := listRecordings(nil, &out, state); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "shop") || !strings.Contains(got, "api") || !strings.Contains(got, path) {
		t.Errorf("list = %q", got)
	}

	out.Reset()
	if err := listRecordings([]string{"-json"}, &out, state); err != nil {
		t.Fatal(err)
	}
	var info record.Info
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &info); err != nil || info.Path != path || info.Agent != "claude" {
		t.Errorf("json = %q (%v)", out.String(), err)
	}

	out.Reset()
	if err := listRecordings([]string{"-dir"}, &out, state); err != nil {
		t.Fatal(err)
	}
	dir, _ := state()
	if got := strings.TrimSpace(out.String()); got != filepath.Join(dir, "recordings") {
		t.Errorf("-dir = %q", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
}

func TestSpawnRecordFlagIsOffByDefault(t *testing.T) {
	req, err := parseSpawn([]string{"do the thing"})
	if err != nil || req.Record {
		t.Fatalf("default spawn: %+v, %v", req, err)
	}
	req, err = parseSpawn([]string{"-record", "do the thing"})
	if err != nil || !req.Record {
		t.Fatalf("-record: %+v, %v", req, err)
	}
	// After the task, like the other flags.
	if req, err = parseSpawn([]string{"do the thing", "--record"}); err != nil || !req.Record || req.Task != "do the thing" {
		t.Fatalf("trailing --record: %+v, %v", req, err)
	}
	if _, err := parseSpawn([]string{"-shell", "-record"}); err == nil {
		t.Error("a shell was allowed -record")
	}
}

// exportFixture is Claude Code's state directory in the transcript package's
// test data, and a catalog that has Claude, which stores its conversations
// there, and Codex, which stores none Flockdeck can read.
func exportFixture(t *testing.T, panes map[string]savedPane) exportEnv {
	t.Helper()
	home, err := filepath.Abs("internal/session/transcript/testdata/claude_export/home")
	if err != nil {
		t.Fatal(err)
	}
	specs := []agent.Spec{
		{ID: "claude", Name: "Claude Code", Exe: "claude", Env: []string{"CLAUDE_CONFIG_DIR=" + home}, Caps: agent.Caps{Transcript: true, Resume: true}},
		{ID: "codex", Name: "Codex", Exe: "codex"},
	}
	return exportEnv{
		stateDir: recordingsState(t),
		catalog:  func() ([]agent.Spec, string) { return specs, "claude" },
		pane: func(id string) (savedPane, bool) {
			p, ok := panes[id]
			return p, ok
		},
	}
}

const exportFixtureID = "11111111-2222-3333-4444-555555555555"

func TestExportCommandExportsAConversationById(t *testing.T) {
	env := exportFixture(t, nil)
	var out bytes.Buffer
	if err := exportRecording([]string{exportFixtureID}, &out, env); err != nil {
		t.Fatal(err)
	}
	dir, _ := env.stateDir()
	files, _ := filepath.Glob(filepath.Join(dir, "recordings", "*", "exports", "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("files = %v\noutput: %s", files, out.String())
	}
	for _, want := range []string{"exported 16 lines", "2 prompts", "4 messages", files[0], "secrets", "1 entries"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output lacks %q: %s", want, out.String())
		}
	}
	raw, _ := os.ReadFile(files[0])
	if strings.Contains(string(raw), "hunter2") {
		t.Error("a secret is in the export")
	}
	// The project is the directory the conversation says it was in.
	if first := strings.SplitN(string(raw), "\n", 2)[0]; !strings.Contains(first, `"type":"recording_started"`) {
		t.Errorf("first line = %s", first)
	}
}

func TestExportCommandTakesAPaneAndAPathAfterTheId(t *testing.T) {
	env := exportFixture(t, map[string]savedPane{"pane-1": {Pane: store.Pane{ID: "pane-1", Name: "shop", Agent: "claude", Model: "opus", Conversation: exportFixtureID}, Root: "/work/shop"}})
	target := filepath.Join(t.TempDir(), "mine.jsonl")
	var out bytes.Buffer
	if err := exportRecording([]string{"pane-1", "-o", target}, &out, env); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(string(raw), "\n", 2)[0]
	// The conversation's, as a recording has it: not the saved layout's pane
	// name or model, which can be stale, and the pane's id is the conversation's.
	for _, want := range []string{`"pane":"` + exportFixtureID + `"`, `"conversation":"` + exportFixtureID + `"`, `"agent":"claude"`} {
		if !strings.Contains(first, want) {
			t.Errorf("first line lacks %s: %s", want, first)
		}
	}
	for _, not := range []string{"paneName", `"model"`} {
		if strings.Contains(first, not) {
			t.Errorf("first line has %s, which is the saved layout's and not the conversation's: %s", not, first)
		}
	}
	// Byte for byte what the same conversation exports as with another saved name
	// and model, and as by its id alone.
	other := filepath.Join(t.TempDir(), "other.jsonl")
	env2 := exportFixture(t, map[string]savedPane{"pane-9": {Pane: store.Pane{ID: "pane-9", Name: "renamed", Agent: "claude", Model: "sonnet", Conversation: exportFixtureID}, Root: "/elsewhere"}})
	if err := exportRecording([]string{"-o", other, "pane-9"}, &out, env2); err != nil {
		t.Fatal(err)
	}
	if raw2, _ := os.ReadFile(other); string(raw2) != string(raw) {
		t.Errorf("the same conversation exported differently by a pane with another name and model:\n%s\n%s", raw, raw2)
	}
	byID := filepath.Join(t.TempDir(), "byid.jsonl")
	if err := exportRecording([]string{"-o", byID, exportFixtureID}, &out, env2); err != nil {
		t.Fatal(err)
	}
	if raw3, _ := os.ReadFile(byID); string(raw3) != string(raw) {
		t.Error("the conversation exported by its id differs from the same exported by its pane")
	}
	// Never over a file.
	if err := exportRecording([]string{"-o", target, "pane-1"}, &out, env); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second export to the same file: %v", err)
	}
}

func TestExportCommandSaysSoForAnAgentThatStoresNothing(t *testing.T) {
	env := exportFixture(t, map[string]savedPane{"pane-2": {Pane: store.Pane{ID: "pane-2", Agent: "codex"}, Root: "/work/shop"}})
	var out bytes.Buffer
	err := exportRecording([]string{"pane-2"}, &out, env)
	if err == nil || !strings.Contains(err.Error(), "stores no conversation Flockdeck can read") {
		t.Fatalf("export said %v", err)
	}
	dir, _ := env.stateDir()
	if _, err := os.Stat(filepath.Join(dir, "recordings")); err == nil {
		t.Error("something was written for an agent with nothing stored")
	}
	if err := exportRecording([]string{"no-such-id"}, &out, env); err == nil || !strings.Contains(err.Error(), "was found") {
		t.Errorf("an unknown id said %v", err)
	}
	if err := exportRecording(nil, &out, env); err == nil {
		t.Error("no id was accepted")
	}
}

func TestExportCommandRefusesAPathInsideTheProject(t *testing.T) {
	project := t.TempDir()
	env := exportFixture(t, map[string]savedPane{"pane-1": {Pane: store.Pane{ID: "pane-1", Agent: "claude", Conversation: exportFixtureID}, Root: project}})
	var out bytes.Buffer
	if err := exportRecording([]string{"-o", filepath.Join(project, "t.jsonl"), "pane-1"}, &out, env); err == nil {
		t.Error("an export was written into the project")
	}
	if _, err := os.Stat(filepath.Join(project, "t.jsonl")); err == nil {
		t.Error("a file is in the project")
	}
}
