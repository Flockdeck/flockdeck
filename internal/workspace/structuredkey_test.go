package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/creds"
	"github.com/jmwri/flockdeck/internal/session"
)

// environmentOf starts a structured pane for the agent in the crew's project,
// has it report its environment, and returns what it said.
func environmentOf(t *testing.T, ws *Workspace, lead, agentID string) string {
	t.Helper()
	id, err := ws.Spawn(lead, SpawnOptions{Task: "plain", Role: "worker", Agent: agentID, Kind: session.KindClaude, SpawnedByAgent: true})
	if err != nil {
		t.Fatalf("spawn %s: %v", agentID, err)
	}
	statusWithin(t, ws, id, session.StatusIdle)
	rt := ws.Runtime(ws.Pane(id))
	if err := rt.Wake("env"); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
		if r, ok := rt.LastReply(); ok && strings.Contains(r.Text, "PATH=") {
			return r.Text
		}
	}
	t.Fatal("the agent never reported its environment")
	return ""
}

// A key stored in Settings for an agent reaches that agent's process, under the
// variable the catalog names, and the key stored for another agent does not.
func TestAStoredKeyReachesOnlyItsAgentsProcess(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	onPath(t, "gemini")
	if err := creds.Set("gemini-acp", "sk-for-gemini"); err != nil {
		t.Fatal(err)
	}
	if err := creds.Set("codex-acp", "sk-for-codex"); err != nil {
		t.Fatal(err)
	}
	got := environmentOf(t, ws, lead, "gemini-acp")
	if !strings.Contains(got, "GEMINI_API_KEY=sk-for-gemini") {
		t.Errorf("the gemini agent did not get its key:\n%s", got)
	}
	if strings.Contains(got, "sk-for-codex") || strings.Contains(got, "OPENAI_API_KEY") {
		t.Errorf("the gemini agent was given the other agent's key:\n%s", got)
	}
}

// With no key stored the agent has no key variable at all: it uses its login.
func TestNoKeyMeansNoKeyVariable(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	onPath(t, "gemini")
	got := environmentOf(t, ws, lead, "gemini-acp")
	if strings.Contains(got, "API_KEY") {
		t.Errorf("the agent has a key variable with none stored:\n%s", got)
	}
}
