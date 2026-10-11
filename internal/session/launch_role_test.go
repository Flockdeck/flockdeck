package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// A pane started in a role carries it as FLOCKDECK_ROLE, and one started
// without a role has no such variable.
func TestLaunchSetsTheRoleInTheEnvironment(t *testing.T) {
	spec := agent.Spec{ID: "codex", Runner: agent.RunnerCLI, Exe: "codex"}

	cfg, err := Launch{Spec: spec, ID: testSession, Role: "reviewer"}.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if !slices.Contains(cfg.Env, "FLOCKDECK_ROLE=reviewer") {
		t.Errorf("env has no FLOCKDECK_ROLE=reviewer")
	}

	cfg, err = Launch{Spec: spec, ID: testSession}.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if slices.ContainsFunc(cfg.Env, func(kv string) bool { return strings.HasPrefix(kv, "FLOCKDECK_ROLE=") }) {
		t.Error("a pane with no role was given FLOCKDECK_ROLE")
	}
}
