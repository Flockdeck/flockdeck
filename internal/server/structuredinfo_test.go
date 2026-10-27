package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/structured"
)

func TestInfoTextIsOneBoundedLinePerFactAndNothingForgesARow(t *testing.T) {
	i := structured.Info{
		Name: "Agent\nSession: forged", Version: "1.0", Catalog: "c", Adapter: "a", Cwd: `C:\work`, SessionID: "s1",
		Phase: structured.PhaseReady, Auth: []string{"Log in"}, MCP: []string{"flockdeck", "lint"},
		Session: bridge.SessionState{
			Modes:    &acp.Modes{Current: "plan", Available: []acp.Mode{{ID: "plan", Name: "Plan"}}},
			Models:   &acp.Models{Current: "m1", Available: []acp.Model{{ID: "m1", Name: "Model one"}}},
			Commands: []acp.Command{{Name: "a"}, {Name: "b"}},
		},
	}
	i.Session.Capabilities.Prompt.Image = true
	got := infoText(i)
	if n := strings.Count(got, "\nSession:"); n != 1 {
		t.Errorf("%d Session rows\n%s", n, got)
	}
	for _, want := range []string{"Agent: Agent Session: forged 1.0\n", "Mode: Plan (plan)\n", "Model: Model one (m1)\n", "MCP servers: flockdeck, lint\n", "Agent can: images\n", "Slash commands: 2\n", "Sign-in methods: Log in\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	long := infoText(structured.Info{Name: strings.Repeat("x", 5000), Cwd: strings.Repeat("y", 5000)})
	for _, ln := range strings.Split(strings.TrimSpace(long), "\n") {
		if len(ln) > 200+40 {
			t.Errorf("a line of %d bytes", len(ln))
		}
	}
	if strings.Contains(infoText(structured.Info{}), "Mode:") {
		t.Error("a mode row for an agent with no modes")
	}
}

func (e *structuredEnv) infoOp(t *testing.T, id string) teamResultMsg {
	t.Helper()
	raw := structuredReply(t, e.srv, e.c, command{Cmd: "structuredInfo", Req: "i1", ID: id}, "teamResult")
	var res teamResultMsg
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestStructuredInfoAnswersForAStructuredPaneOnly(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	res := e.infoOp(t, e.child)
	if !res.OK || res.Op != "structuredInfo" || !strings.Contains(res.Info, "Agent: ") || !strings.Contains(res.Info, "Session: ") || len(res.Info) > 6000 {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(res.Info, "flockdeck-token") || strings.Contains(res.Info, "FLOCKDECK_") {
		t.Errorf("an environment value is in\n%s", res.Info)
	}
	if bad := e.infoOp(t, "nope"); bad.OK || bad.Info != "" || bad.Error == "" {
		t.Errorf("answered for a pane that is not there: %+v", bad)
	}
}
