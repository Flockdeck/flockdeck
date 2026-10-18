package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The roles Flockdeck ships are all there, each with a briefing, and none of
// them may start a role that does not exist.
func TestBuiltinRolesAreComplete(t *testing.T) {
	c := Merge(nil)
	want := []string{"manager", "planner", "worker", "reviewer", "tester", "docs"}
	if got := c.RoleIDs(); !slices.Equal(got, want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	for _, r := range c.Roles {
		if strings.TrimSpace(r.Briefing) == "" || r.Name == "" {
			t.Errorf("role %q has no name or briefing: %+v", r.ID, r)
		}
		if checkRole(&r) != nil {
			t.Errorf("role %q does not pass its own check", r.ID)
		}
		for _, id := range r.Spawns {
			if _, ok := c.Role(id); !ok {
				t.Errorf("role %q may start %q, which is not a role", r.ID, id)
			}
		}
	}
	if c.Notice != "" {
		t.Errorf("the built-in roles alone gave a notice: %s", c.Notice)
	}
}

// A user entry with a built-in's id is merged over it field by field, and an
// entry with any other id is a new role after the built-ins.
func TestUserRolesOverrideAndExtendBuiltins(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"roles": [
		{"id": "worker", "model": "opus", "tier": "TOP"},
		{"id": "manager", "spawns": []},
		{"id": "security", "name": "Security", "briefing": "Look for holes.", "worktree": "readonly", "spawns": ["worker"]}
	]}`)
	c := LoadFrom(dir)
	if c.Notice != "" {
		t.Fatalf("notice = %q", c.Notice)
	}

	built := BuiltinRoles()
	var worker Role
	for _, r := range built {
		if r.ID == "worker" {
			worker = r
		}
	}
	got, ok := c.Role("worker")
	if !ok {
		t.Fatal("worker is gone")
	}
	if got.Model != "opus" || got.Tier != "top" {
		t.Errorf("worker model/tier = %q/%q, want the user's opus/top", got.Model, got.Tier)
	}
	if got.Briefing != worker.Briefing || got.Worktree != worker.Worktree || got.Name != worker.Name {
		t.Errorf("worker lost what the user did not set: %+v", got)
	}

	if m, _ := c.Role("manager"); len(m.Spawns) != 0 {
		t.Errorf("manager spawns = %v, want the empty list the user wrote", m.Spawns)
	}

	sec, ok := c.Role("security")
	if !ok || sec.Worktree != WorktreeReadOnly || !slices.Equal(sec.Spawns, []string{"worker"}) {
		t.Errorf("security = %+v, %v", sec, ok)
	}
	if ids := c.RoleIDs(); ids[len(ids)-1] != "security" || len(ids) != len(built)+1 {
		t.Errorf("roles = %v, want the new one after the built-ins", ids)
	}

	// Reading the catalog again must not have changed what the code ships.
	if again := BuiltinRoles(); again[2].Model != "" || len(again[0].Spawns) == 0 {
		t.Errorf("a merge changed the built-in roles: %+v", again)
	}
}

// A role entry that cannot be used costs that entry and nothing else, and the
// notice says why.
func TestBadRoleEntriesAreNamed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"roles": [
		{"name": "no id"},
		{"id": "odd", "worktree": "elsewhere", "tier": "huge"},
		{"id": "lost", "spawns": ["nobody"]},
		{"id": "worker", "model": 3}
	]}`)
	c := LoadFrom(dir)
	for _, want := range []string{"role 1 has no id", `"elsewhere"`, `"huge"`, `may spawn "nobody"`, `role "worker"`} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q does not mention %s", c.Notice, want)
		}
	}
	odd, _ := c.Role("odd")
	if odd.Worktree != "" || odd.Tier != "" {
		t.Errorf("bad values were kept: %+v", odd)
	}
	if w, _ := c.Role("worker"); w.Model != "" {
		t.Errorf("an entry that did not decode changed the built-in: %+v", w)
	}
}

// Roles are one more key of agents.json: writing a default keeps them, and
// keeps keys this build does not know, in the file and inside a role entry.
func TestRolesRoundTripWithUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{
		"version": 1,
		"roles": [{"id": "security", "briefing": "Look for holes.", "futureField": {"kept": true}}],
		"somethingNewer": {"kept": true}
	}`)
	if err := SetDefaults(dir, "", Defaults{Agent: "codex"}); err != nil {
		t.Fatalf("SetDefaults: %v", err)
	}
	f, err := ReadConfig(dir)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if len(f.Roles) != 1 || !strings.Contains(string(f.Roles[0]), `"futureField"`) {
		t.Errorf("roles = %s, want the entry exactly as written", f.Roles)
	}
	if _, kept := f.Extra["somethingNewer"]; !kept {
		t.Error("a key this build does not know about was deleted")
	}
	if _, stolen := f.Extra["roles"]; stolen {
		t.Error("roles was read as an unknown key")
	}
	if f.Defaults.Agent != "codex" {
		t.Errorf("default agent = %q", f.Defaults.Agent)
	}
}

func TestCheckRoleNamesTheRolesThereAre(t *testing.T) {
	c := Merge(nil)
	if _, err := c.CheckRole("worker"); err != nil {
		t.Errorf("worker: %v", err)
	}
	_, err := c.CheckRole("wrker")
	if err == nil || !strings.Contains(err.Error(), `"wrker"`) || !strings.Contains(err.Error(), "worker, reviewer") {
		t.Errorf("error = %v, want the typo and the roles there are", err)
	}
}

func TestMayStart(t *testing.T) {
	c := Merge(nil)
	for _, tt := range []struct {
		parent, child string
		ok            bool
	}{
		{"", "worker", true},        // a pane with no role is not limited
		{"manager", "worker", true}, // listed
		{"manager", "", true},       // a Plain role may start an agent with no role
		{"worker", "", false},       // others may not: it would have no limits
		{"planner", "", false},
		{"worker", "reviewer", false},    // a worker lists none
		{"planner", "worker", false},     // likewise
		{"gone-from-file", "docs", true}, // a role taken out since the pane began
	} {
		err := c.MayStart(tt.parent, tt.child)
		if (err == nil) != tt.ok {
			t.Errorf("MayStart(%q, %q) = %v, want ok=%v", tt.parent, tt.child, err, tt.ok)
		}
	}
	c.Roles = append(c.Roles, Role{ID: "lead", Spawns: []string{"worker"}})
	if err := c.MayStart("lead", "docs"); err == nil || !strings.Contains(err.Error(), "may start worker, not docs") {
		t.Errorf("error = %v, want the roles it may start", err)
	}
}

// A role fills in only what the spawn left out.
func TestChooseAppliesRoleDefaultsOnlyWhereNothingWasAsked(t *testing.T) {
	c := &Catalog{Specs: []Spec{
		{ID: "claude", Models: []Model{{ID: "haiku", Tier: TierSmall}, {ID: "opus", Tier: TierTop}}},
		{ID: "codex", Models: []Model{{ID: "gpt-small", Tier: TierSmall}}},
	}}
	pinned := Role{ID: "pinned", Agent: "codex", Model: "gpt-5"}
	tiered := Role{ID: "tiered", Tier: TierTop}
	modelOnly := Role{ID: "model-only", Model: "opus"}

	for _, tt := range []struct {
		name              string
		role              Role
		agent, model      string
		wantAgent, wantMd string
	}{
		{"role's agent and model", pinned, "", "", "codex", "gpt-5"},
		{"the spawn's agent wins", pinned, "claude", "", "claude", ""},
		{"the spawn's model wins, and keeps the default agent", pinned, "", "haiku", "", "haiku"},
		{"the spawn's agent and model win", pinned, "claude", "opus", "claude", "opus"},
		{"a tier picks the default agent's model", tiered, "", "", "", "opus"},
		{"a tier picks the named agent's model", tiered, "codex", "", "codex", ""},
		{"a model with no agent is the default agent's", modelOnly, "", "", "", "opus"},
		{"a model is not handed to another agent", pinned, "claude", "", "claude", ""},
		{"a model-only role's model is not handed to a named agent", modelOnly, "codex", "", "codex", ""},
		{"a model-only role's model goes to the agent named if it is the default", modelOnly, "claude", "", "claude", "opus"},
	} {
		agentID, model := c.Choose(tt.role, tt.agent, tt.model, "claude")
		if agentID != tt.wantAgent || model != tt.wantMd {
			t.Errorf("%s: got %q/%q, want %q/%q", tt.name, agentID, model, tt.wantAgent, tt.wantMd)
		}
	}
}

// Role ids are trimmed and lowercased, an id that is empty afterwards is
// reported and dropped, and a Spawns entry that names no role stays reported.
func TestMergeRolesNormalisesIDs(t *testing.T) {
	raws := []json.RawMessage{
		json.RawMessage(`{"id": "  Worker ", "tier": "mid"}`),
		json.RawMessage(`{"id": "  Security", "spawns": ["Ghost"]}`),
		json.RawMessage(`{"id": "   "}`),
		json.RawMessage(`{"name": "no id"}`),
	}
	roles, problems := mergeRoles(raws)
	n := len(BuiltinRoles())
	if len(roles) != n+1 {
		t.Fatalf("got %d roles, want the built-ins and one new", len(roles))
	}
	for _, r := range roles {
		if r.ID == "  Worker " || r.ID == "Worker" || strings.TrimSpace(r.ID) == "" {
			t.Errorf("role id %q was not normalised", r.ID)
		}
	}
	var worker Role
	for _, r := range roles {
		if r.ID == "worker" {
			worker = r
		}
	}
	if worker.Tier != "mid" || worker.Briefing == "" {
		t.Errorf("Worker did not merge over the built-in worker: %+v", worker)
	}
	if roles[n].ID != "security" {
		t.Errorf("new role id = %q, want security", roles[n].ID)
	}
	all := strings.Join(problems, "; ")
	for _, want := range []string{"role 3 has no id", "role 4 has no id", `may spawn "ghost"`} {
		if !strings.Contains(all, want) {
			t.Errorf("problems %q lack %q", all, want)
		}
	}
}

// The description is a line for the person choosing a role, so each built-in
// has one and a user's entry can write or replace it.
func TestRoleDescriptions(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"roles": [
		{"id": "worker", "description": "Does the small jobs."},
		{"id": "security"}]}`)
	c := LoadFrom(dir)
	for _, r := range BuiltinRoles() {
		if r.Description == "" || strings.Contains(r.Description, "\n") {
			t.Errorf("built-in role %q needs a one-line description, has %q", r.ID, r.Description)
		}
	}
	if w, _ := c.Role("worker"); w.Description != "Does the small jobs." {
		t.Errorf("worker description = %q, want the user's", w.Description)
	}
	if m, _ := c.Role("manager"); m.Description == "" {
		t.Error("manager lost its description when another role was changed")
	}
	if s, _ := c.Role("security"); s.Description != "" {
		t.Errorf("a role with none has %q", s.Description)
	}
}

// Every built-in role tells its agent to arm the inbox stream, and the manager
// names the Monitor tool and the fallback for when it is missing.
func TestBuiltinRolesTellTheAgentToArmTheInboxStream(t *testing.T) {
	for _, r := range Merge(nil).Roles {
		if !strings.Contains(r.Briefing, "inbox -follow") {
			t.Errorf("role %q never mentions `inbox -follow`", r.ID)
		}
	}
	m, _ := Merge(nil).Role("manager")
	for _, want := range []string{"Monitor tool", "inbox -wait 90s", "inbox read <id>"} {
		if !strings.Contains(m.Briefing, want) {
			t.Errorf("the manager briefing lacks %q", want)
		}
	}
}
