package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// useRoles gives the command line a set of roles of its own for one test.
func useRoles(t *testing.T, roles []agent.Role, notice string) {
	t.Helper()
	prev := roleCatalog
	t.Cleanup(func() { roleCatalog = prev })
	roleCatalog = func() ([]agent.Role, string) { return roles, notice }
}

func TestParseSpawnCarriesTheRole(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})
	useRoles(t, agent.BuiltinRoles(), "")

	req, err := parseSpawn([]string{"review the diff", "-role", "reviewer"})
	if err != nil {
		t.Fatalf("parseSpawn: %v", err)
	}
	if req.Role != "reviewer" || req.Task != "review the diff" || req.Agent != "" {
		t.Errorf("request = %+v, want the role and nothing chosen for the agent", req)
	}

	plain, err := parseSpawn([]string{"watch the build"})
	if err != nil || plain.Role != "" {
		t.Errorf("a spawn with no role carried %q, %v", plain.Role, err)
	}
}

func TestParseSpawnRefusesAnUnknownRole(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})
	useRoles(t, agent.BuiltinRoles(), "")

	_, err := parseSpawn([]string{"review the diff", "-role", "reviwer"})
	if err == nil {
		t.Fatal("an unknown role was accepted")
	}
	for _, want := range []string{`"reviwer"`, "reviewer", "manager"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}

	// A catalog the user's file was only partly read into says so, since the
	// role may be one it dropped.
	useRoles(t, agent.BuiltinRoles(), "agents.json: role 2 has no id")
	_, err = parseSpawn([]string{"x", "-role", "mine"})
	if err == nil || !strings.Contains(err.Error(), "role 2 has no id") {
		t.Errorf("error = %v, want the catalog's notice", err)
	}
}

func TestParseSpawnRoleDoesNotGoWithAShell(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})
	useRoles(t, agent.BuiltinRoles(), "")

	if _, err := parseSpawn([]string{"-shell", "-role", "worker", "tail the log"}); err == nil {
		t.Error("a shell was given a role")
	}
}

func TestAgentsListsTheRoles(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})
	useRoles(t, []agent.Role{
		{ID: "manager", Name: "Manager", Worktree: agent.WorktreeMain, Spawns: []string{"worker"}},
		{ID: "worker", Name: "Worker", Worktree: agent.WorktreeOwn, Agent: "codex", Model: "gpt-5"},
		{ID: "scout", Tier: agent.TierSmall},
	}, "")

	var buf bytes.Buffer
	printAgents(&buf)
	out := buf.String()
	for _, want := range []string{
		"manager - Manager  [main checkout]", "may start: worker",
		"worker - Worker  [own checkout, agent codex, model gpt-5]",
		"scout - scout  [small tier model]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("agents output does not say %q:\n%s", want, out)
		}
	}
}
