package agent

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func raw(s string) []json.RawMessage { return []json.RawMessage{json.RawMessage(s)} }

func ids(specs []Spec) []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.ID)
	}
	return out
}

// The structured entries ship: opencode listed once opted in, Gemini and Codex
// hidden, and no entry for Claude.
func TestBuiltinStructuredEntries(t *testing.T) {
	c := Merge(&File{StructuredPanes: true})
	if s, ok := c.Find("opencode-acp"); !ok || s.Runner != RunnerACP || s.ACP.Adapter != "opencode" || s.ACP.Auth != AuthNone {
		t.Fatalf("opencode-acp = %+v, %v", s, ok)
	}
	for _, id := range []string{"gemini-acp", "codex-acp"} {
		s, ok := c.Find(id)
		if !ok || !s.Hidden || s.Runner != RunnerACP {
			t.Errorf("%s should exist and be hidden: %+v, %v", id, s, ok)
		}
		if slices.Contains(ids(c.Visible()), id) {
			t.Errorf("%s is offered although it is hidden", id)
		}
	}
	for _, s := range c.Specs {
		if s.Runner == RunnerACP && (strings.Contains(s.ID, "claude") || strings.Contains(s.ACP.Adapter, "claude")) {
			t.Errorf("%s: there is no structured Claude entry", s.ID)
		}
	}
}

// The hidden rule: structured entries are not offered unless the user opted in,
// and they are still found by id, since a saved layout names them.
func TestStructuredEntriesAreListedOnlyOnOptIn(t *testing.T) {
	off := Merge(&File{})
	if slices.Contains(ids(off.Visible()), "opencode-acp") {
		t.Error("opencode-acp is offered without the opt-in")
	}
	if _, ok := off.Find("opencode-acp"); !ok {
		t.Error("opencode-acp should still be found by id")
	}
	if _, ok := off.offered("opencode-acp"); ok {
		t.Error("offered() should refuse a structured entry without the opt-in")
	}
	on := Merge(&File{StructuredPanes: true})
	if !slices.Contains(ids(on.Visible()), "opencode-acp") {
		t.Error("opencode-acp should be offered with the opt-in")
	}
	// The terminal opencode is not touched by either.
	for _, c := range []*Catalog{off, on} {
		if !slices.Contains(ids(c.Visible()), "opencode") {
			t.Error("the terminal opencode entry should always be offered")
		}
	}
}

// The setting is read from agents.json and survives a round trip.
func TestStructuredPanesSettingRoundTrips(t *testing.T) {
	var f File
	if err := json.Unmarshal([]byte(`{"version":1,"structuredPanes":true,"future":1}`), &f); err != nil {
		t.Fatal(err)
	}
	if !f.StructuredPanes || !Merge(&f).Structured {
		t.Fatal("structuredPanes was not read")
	}
	if _, stray := f.Extra["structuredPanes"]; stray {
		t.Error("structuredPanes is a known key and should not be kept as an extra")
	}
	out, err := json.Marshal(f)
	if err != nil || !strings.Contains(string(out), `"structuredPanes":true`) || !strings.Contains(string(out), `"future":1`) {
		t.Errorf("round trip lost a key: %s, %v", out, err)
	}
}

// A user's catalog can carry an ACP entry of its own, and can edit a built-in
// one field by field.
func TestACPEntryParseAndMerge(t *testing.T) {
	c := Merge(&File{StructuredPanes: true, Agents: []json.RawMessage{
		json.RawMessage(`{"id":"mine","name":"Mine","runner":"ACP","acp":{"adapter":"gemini","auth":"none","passEnv":["HOME","MY_VAR"]}}`),
		json.RawMessage(`{"id":"opencode-acp","acp":{"passEnv":["HOME"]}}`),
	}})
	if c.Notice != "" {
		t.Fatalf("notice: %s", c.Notice)
	}
	mine, ok := c.Find("mine")
	if !ok || mine.Runner != RunnerACP || mine.ACP.Adapter != "gemini" || mine.ACP.Auth != AuthNone || !slices.Equal(mine.ACP.PassEnv, []string{"HOME", "MY_VAR"}) {
		t.Fatalf("mine = %+v, %v", mine, ok)
	}
	if mine.Exe != "" {
		t.Errorf("a structured entry has no Exe, got %q", mine.Exe)
	}
	oc, _ := c.Find("opencode-acp")
	if oc.ACP.Adapter != "opencode" || oc.ACP.Auth != AuthNone || !oc.ACP.MCP {
		t.Errorf("the merge lost the built-in's fields: %+v", oc.ACP)
	}
	if !slices.Equal(oc.ACP.PassEnv, []string{"HOME"}) {
		t.Errorf("the entry's list should replace the built-in's, got %v", oc.ACP.PassEnv)
	}
	// The built-in itself is not changed by the edit.
	if fresh := opencodeACPSpec(); len(fresh.ACP.PassEnv) != len(homeEnv()) {
		t.Errorf("an edit reached the shipped list: %v", fresh.ACP.PassEnv)
	}
}

// An entry that names a runner nobody has, or an adapter the manifest lacks, is
// refused with a message that says why.
func TestUnknownRunnerAndAdapterAreRefused(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id":"a","runner":"mcp"}`),
		json.RawMessage(`{"id":"b","runner":"acp","acp":{"adapter":"nothing"}}`),
		json.RawMessage(`{"id":"c","runner":"acp"}`),
		json.RawMessage(`{"id":"opencode-acp","acp":{"adapter":"nothing"}}`),
	}})
	for _, want := range []string{
		`runner "mcp" is none of "cli", "api" and "acp"`,
		`"nothing" is not an adapter this build knows`,
		`agent "c": a structured agent needs`,
	} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q should say %s", c.Notice, want)
		}
	}
	if _, ok := c.Find("b"); ok {
		t.Error("an entry naming an unknown adapter should not be added")
	}
	if oc, _ := c.Find("opencode-acp"); oc.ACP.Adapter != "opencode" {
		t.Errorf("a bad edit should leave the built-in as it was: %+v", oc.ACP)
	}
}

// The environment is exactly what the entry lists, from what is there, and
// never a FLOCKDECK_ variable, whatever the entry says.
func TestACPEnvironmentIsExactlyTheAllowlist(t *testing.T) {
	environ := []string{"PATH=/bin", "HOME=/h", "ANTHROPIC_API_KEY=secret", "FLOCKDECK_TOKEN=tok", "Appdata=C:\\a", "BROKEN"}
	s := Spec{ACP: ACPSpec{PassEnv: []string{"HOME", "APPDATA"}}}
	got := s.ACPPassEnv(environ)
	if !slices.Equal(got, []string{"HOME=/h", "Appdata=C:\\a"}) {
		t.Errorf("got %v", got)
	}
	if len(Spec{}.ACPPassEnv(environ)) != 0 {
		t.Error("an entry that lists nothing passes nothing")
	}

	c := Merge(&File{Agents: raw(`{"id":"x","runner":"acp","acp":{"adapter":"opencode","passEnv":["HOME","FLOCKDECK_TOKEN","flockdeck_relay","A B","1X"]}}`)})
	x, _ := c.Find("x")
	if !slices.Equal(x.ACP.PassEnv, []string{"HOME"}) {
		t.Errorf("PassEnv = %v", x.ACP.PassEnv)
	}
	for _, want := range []string{`"FLOCKDECK_TOKEN" is dropped`, `"flockdeck_relay" is dropped`, `"A B" is not a variable name`, `"1X" is not a variable name`} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q should say %s", c.Notice, want)
		}
	}
	for _, s := range Builtins() {
		if s.Runner != RunnerACP {
			continue
		}
		for _, name := range s.ACP.PassEnv {
			up := strings.ToUpper(name)
			if strings.HasPrefix(up, "FLOCKDECK_") || strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") {
				t.Errorf("%s passes %s", s.ID, name)
			}
		}
	}
}

// A catalog written before structured entries existed loads as it did.
func TestAnOldCatalogLoadsUnchanged(t *testing.T) {
	old := &File{Version: 1, Defaults: Defaults{Agent: "codex"}, Agents: raw(`{"id":"codex","defaultModel":"gpt-5"}`)}
	c := Merge(old)
	if c.Notice != "" || c.Structured {
		t.Fatalf("notice %q, structured %v", c.Notice, c.Structured)
	}
	for _, id := range ids(c.Visible()) {
		if s, _ := c.Find(id); s.Runner == RunnerACP {
			t.Errorf("%s is structured but was offered without the opt-in", id)
		}
	}
	if s, _ := c.Find("codex"); s.Runner != RunnerCLI || s.DefaultModel != "gpt-5" {
		t.Errorf("codex = %+v", s)
	}
}

// Availability of a structured entry is whether the manifest's command is on PATH.
func TestStructuredAvailabilityFollowsTheManifestCommand(t *testing.T) {
	var asked string
	old := lookPath
	lookPath = func(name string) (string, error) {
		asked = name
		return "", errors.New("not found")
	}
	defer func() { lookPath = old }()
	if probe(opencodeACPSpec()) {
		t.Error("available although nothing is installed")
	}
	if asked != "opencode" {
		t.Errorf("looked for %q, want the manifest's command", asked)
	}
}
