package agent

import (
	"slices"
	"testing"
)

// A bare agent id becomes the structured entry for it while structured panes
// are on, for the two agents that have one on offer, and for nothing else.
func TestABareAgentIDResolvesToItsStructuredEntry(t *testing.T) {
	c := optedIn(&File{})
	for id, want := range map[string]string{
		"claude":       "claude-acp",
		"opencode":     "opencode-acp",
		"codex":        "codex",        // its structured entry is hidden until T192 reports
		"gemini":       "gemini",       // same
		"aider":        "aider",        // no structured side
		"cursor-agent": "cursor-agent", // no structured side
		"claude-acp":   "claude-acp",   // already structured
		"nothing":      "nothing",      // not in the catalog
	} {
		if got := c.ForNewPane(id); got != want {
			t.Errorf("ForNewPane(%q) = %q, want %q", id, got, want)
		}
	}
}

// With structured panes off (terminal chosen, or a host remote devices reach)
// nothing moves and the structured entries are not offered.
func TestABareAgentIDStaysWhereStructuredPanesAreOff(t *testing.T) {
	c := Merge(&File{})
	for _, id := range []string{"claude", "opencode", "codex", "gemini"} {
		if got := c.ForNewPane(id); got != id {
			t.Errorf("ForNewPane(%q) = %q with structured panes off", id, got)
		}
	}
}

// The terminal request goes the other way through the same map.
func TestTerminalOfIsTheSiblingMapBackwards(t *testing.T) {
	c := optedIn(&File{})
	for id, want := range map[string]string{
		"claude-acp":   "claude",
		"opencode-acp": "opencode",
		"codex-acp":    "codex",
		"gemini-acp":   "gemini",
		"claude":       "claude",
		"aider":        "aider",
		"nothing":      "nothing",
	} {
		if got := c.TerminalOf(id); got != want {
			t.Errorf("TerminalOf(%q) = %q, want %q", id, got, want)
		}
	}
}

// Every structured built-in names a terminal sibling that is in the catalog and
// is not itself structured.
func TestEveryStructuredBuiltinNamesATerminalSibling(t *testing.T) {
	c := Merge(&File{})
	for _, s := range c.Specs {
		if s.Runner != RunnerACP {
			continue
		}
		term, ok := c.Find(s.Terminal)
		if !ok || term.Runner == RunnerACP {
			t.Errorf("%s names terminal %q: %+v, %v", s.ID, s.Terminal, term, ok)
		}
	}
}

// The picker names a terminal entry as terminal where it also lists a
// structured sibling, and only there.
func TestTheTerminalEntriesAreNamedWhereASiblingIsListed(t *testing.T) {
	names := func(c *Catalog) map[string]string {
		out := map[string]string{}
		for _, s := range c.Visible() {
			out[s.ID] = s.Name
		}
		return out
	}
	on := names(optedIn(&File{}))
	if on["claude"] != "Claude Code (terminal)" || on["opencode"] != "opencode (terminal)" {
		t.Errorf("terminal entries are %q and %q", on["claude"], on["opencode"])
	}
	if on["claude-acp"] != "Claude Code (structured)" {
		t.Errorf("claude-acp is %q", on["claude-acp"])
	}
	// Codex and Gemini have no listed structured entry, so nothing to tell apart.
	if on["codex"] != "Codex" || on["gemini"] != "Gemini CLI" || on["aider"] != "Aider" {
		t.Errorf("unpaired entries are named %q, %q, %q", on["codex"], on["gemini"], on["aider"])
	}
	off := names(Merge(&File{}))
	if off["claude"] != "Claude Code" || off["opencode"] != "opencode" {
		t.Errorf("names with structured panes off: %q, %q", off["claude"], off["opencode"])
	}
	if _, listed := off["claude-acp"]; listed {
		t.Error("claude-acp is listed with structured panes off")
	}
	// A name the user chose is theirs.
	renamed := names(optedIn(&File{Agents: raw(`{"id": "claude", "name": "My Claude"}`)}))
	if renamed["claude"] != "My Claude" {
		t.Errorf("a renamed claude is shown as %q", renamed["claude"])
	}
}

// An agents.json entry over a built-in terminal id runs as the user wrote it:
// a bare id is not moved off it. An entry that hides the structured sibling
// keeps the terminal one the default as well.
func TestAUserEntryOverABuiltinIDKeepsRunningAsWritten(t *testing.T) {
	c := optedIn(&File{Agents: raw(`{"id": "claude", "exe": "my-claude"}`)})
	if got := c.ForNewPane("claude"); got != "claude" {
		t.Errorf("ForNewPane(claude) = %q with the user's own claude entry", got)
	}
	spec, ok := c.Find("claude")
	if !ok || spec.Exe == "" || spec.Exe == "claude" {
		t.Errorf("the user's exe was lost: %+v", spec)
	}
	// Another built-in is unaffected by that entry.
	if got := c.ForNewPane("opencode"); got != "opencode-acp" {
		t.Errorf("ForNewPane(opencode) = %q", got)
	}
	// The terminal entry is not renamed over the user's head.
	if name := nameOf(c, "claude"); name != "Claude Code" {
		t.Errorf("an overridden claude is listed as %q", name)
	}

	hidden := optedIn(&File{Agents: raw(`{"id": "claude-acp", "hidden": true}`)})
	if got := hidden.ForNewPane("claude"); got != "claude" {
		t.Errorf("ForNewPane(claude) = %q with claude-acp hidden by the user", got)
	}
	if !slices.Contains(ids(hidden.Visible()), "claude") {
		t.Error("claude is not listed")
	}
	if name := nameOf(hidden, "claude"); name != "Claude Code" {
		t.Errorf("claude is listed as %q with no structured sibling", name)
	}
}

func nameOf(c *Catalog, id string) string {
	for _, s := range c.Visible() {
		if s.ID == id {
			return s.Name
		}
	}
	return ""
}

// A spawn in any built-in role still ends at an agent the catalog offers once
// the bare id is resolved, with structured panes on or off.
func TestEveryRoleChoosesAnAgentThatResolvesToOneOffered(t *testing.T) {
	for _, on := range []bool{true, false} {
		c := Merge(&File{})
		c.Structured = on
		for _, r := range c.Roles {
			id, _ := c.Choose(r, "", "", DefaultAgentID)
			if id == "" {
				id = DefaultAgentID
			}
			got := c.ForNewPane(id)
			if _, ok := c.Find(got); !ok {
				t.Errorf("role %s (structured %v): %q is not in the catalog", r.ID, on, got)
			}
			if !on && got != id {
				t.Errorf("role %s: %q moved to %q with structured panes off", r.ID, id, got)
			}
		}
	}
}
