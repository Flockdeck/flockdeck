package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
)

// mcpCrew wires the workspace to its own hook server the way internal/server
// does for the MCP tools, without the workspace goroutine the server hops to.
func mcpCrew(t *testing.T) (crew, *hooks.Server) {
	t.Helper()
	c := newCrew(t)
	srv := c.ws.HookServer()
	if srv == nil {
		t.Fatal("the test workspace has no hook server")
	}
	srv.SetMsgHandler(func(r hooks.MsgRequest) (hooks.MsgResult, error) {
		return c.ws.SendMessage(r.Pane, r.Target, r.Text)
	})
	srv.SetTeamHandler(func(r hooks.TeamRequest) (hooks.TeamResult, error) {
		m, err := c.ws.Roster(r.Pane)
		return hooks.TeamResult{Members: m}, err
	})
	c.ws.UseMCPToken(srv.IssuePaneToken)
	return c, srv
}

// The entry a pane's agent gets holds a token that acts as that pane, and an
// address to use it at.
func TestEachPanesEntryTokenActsAsThatPaneOnly(t *testing.T) {
	c, srv := mcpCrew(t)
	c.ws.UseMCPSources(func() (string, error) { return "/opt/fd/flockdeck", nil }, nil)
	spec := agent.Spec{ACP: agent.ACPSpec{MCP: true}}
	entry := func(id string) (token, api string) {
		for _, s := range c.ws.mcpServersFor(c.ws.Pane(id), spec) {
			if s.Name != FlockdeckMCPName {
				continue
			}
			for _, e := range s.Env {
				switch e.Name {
				case MCPTokenEnv:
					token = e.Value
				case MCPAPIEnv:
					api = e.Value
				}
			}
		}
		return
	}
	lead, leadAPI := entry(c.lead)
	w1, w1API := entry(c.w1)
	if lead == "" || w1 == "" || lead == w1 || leadAPI != srv.BaseURL() || w1API != srv.BaseURL() {
		t.Fatalf("entries: %q %q, %q %q, api %s", lead, leadAPI, w1, w1API, srv.BaseURL())
	}
	if lead == srv.Token() || w1 == srv.Token() {
		t.Fatal("a pane was given the run's shared token")
	}

	ctx := context.Background()
	if _, err := hooks.MCPSend(ctx, srv.BaseURL(), w1, c.reviewer, "from w1"); err != nil {
		t.Fatal(err)
	}
	msgs := c.inbox(t, c.reviewer, true)
	if len(msgs) != 1 || msgs[0].From.Pane != c.w1 || msgs[0].Text != "from w1" {
		t.Fatalf("reviewer's inbox = %+v, want one message from w1", msgs)
	}
	// w1's token cannot send as the manager, whatever the body says.
	body := `{"pane":"` + c.lead + `","to":"` + c.reviewer + `","text":"as lead"}`
	if code := postRaw(t, srv, hooks.PathMCPSend, w1, body); code != 400 {
		t.Errorf("a send that names the manager = %d, want 400", code)
	}
	if got := len(c.inbox(t, c.reviewer, true)); got != 1 {
		t.Errorf("reviewer has %d messages, want only the first", got)
	}
	if _, err := hooks.MCPSend(ctx, srv.BaseURL(), lead, c.reviewer, "from lead"); err != nil {
		t.Fatal(err)
	}
	last := c.inbox(t, c.reviewer, true)
	if len(last) != 2 || last[1].From.Pane != c.lead {
		t.Errorf("the second message = %+v, want one from the manager", last)
	}
}

func TestMCPSendKeepsTheTeamRules(t *testing.T) {
	c, srv := mcpCrew(t)
	tok := srv.IssuePaneToken(c.w1)
	ctx := context.Background()
	for name, to := range map[string]string{"an unknown name": "nobody-by-this-name", "a pane id that is not open": "no-such-pane"} {
		if _, err := hooks.MCPSend(ctx, srv.BaseURL(), tok, to, "hi"); err == nil {
			t.Errorf("a message to %s was accepted", name)
		}
	}
	if _, err := hooks.MCPSend(ctx, srv.BaseURL(), tok, c.reviewer, strings.Repeat("x", team.MaxText+1)); err == nil {
		t.Error("an oversize message was accepted")
	}
	leadTok := srv.IssuePaneToken(c.lead)
	res, err := hooks.MCPSend(ctx, srv.BaseURL(), leadTok, "worker", "to every worker")
	if err != nil || len(res.Sent) != 2 {
		t.Errorf("a send to a role = %+v, %v, want both workers", res, err)
	}
	// A pane that is not an agent pane is not on the roster and cannot send.
	c.ws.NewTabWith(Choice{Kind: session.KindShell}, c.root, "")
	shell := c.ws.FocusedPane().ID
	shellTok := srv.IssuePaneToken(shell)
	if _, err := hooks.MCPSend(ctx, srv.BaseURL(), shellTok, c.reviewer, "hi"); err == nil {
		t.Error("a shell pane sent a message")
	}
	if _, err := hooks.MCPSend(ctx, srv.BaseURL(), tok, c.reviewer, "after close"); err != nil {
		t.Fatal(err)
	}
	c.ws.destroyPane(c.w1)
	if _, err := hooks.MCPSend(ctx, srv.BaseURL(), tok, c.reviewer, "from a closed pane"); err == nil {
		t.Error("the token of a closed pane still worked")
	}
}

func TestListPeersIsTheCallersProjectAndNothingSecret(t *testing.T) {
	c, srv := mcpCrew(t)
	other := t.TempDir()
	if err := c.ws.OpenProject(other); err != nil {
		t.Fatal(err)
	}
	c.ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "sleeper"}, other, "")
	foreign := c.ws.FocusedPane().ID
	tok := srv.IssuePaneToken(c.w1)

	res, err := hooks.MCPListPeers(context.Background(), srv.BaseURL(), tok)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]hooks.MCPPeer{}
	for _, p := range res.Peers {
		got[p.Pane] = p
	}
	if len(got) != 4 || got[foreign].Pane != "" {
		t.Errorf("peers = %+v, want the four panes of the project and not %s", res.Peers, foreign)
	}
	if !got[c.w1].Self || got[c.lead].Self || got[c.lead].Role != "manager" {
		t.Errorf("self or roles wrong: %+v", got)
	}
	b, _ := json.Marshal(res)
	for _, secret := range []string{srv.Token(), tok, c.root} {
		if strings.Contains(string(b), secret) {
			t.Errorf("list_peers shows %q: %s", secret, b)
		}
	}
	// The other project's pane sees its own project.
	ftok := srv.IssuePaneToken(foreign)
	res, err = hooks.MCPListPeers(context.Background(), srv.BaseURL(), ftok)
	if err != nil || len(res.Peers) == 0 {
		t.Fatalf("the other project's peers = %+v, %v", res.Peers, err)
	}
	selfSeen := false
	for _, p := range res.Peers {
		if p.Pane == c.lead || p.Pane == c.w1 || p.Pane == c.w2 || p.Pane == c.reviewer {
			t.Errorf("the other project sees %s", p.Pane)
		}
		selfSeen = selfSeen || (p.Self && p.Pane == foreign)
	}
	if !selfSeen {
		t.Errorf("the other project's roster does not mark its caller: %+v", res.Peers)
	}
	// And cannot message across the project line.
	if _, err := hooks.MCPSend(context.Background(), srv.BaseURL(), ftok, c.reviewer, "hi"); err == nil {
		t.Error("a message crossed into another project")
	}
}

// postRaw posts body to one of the MCP endpoints with a bearer token and
// returns the status.
func postRaw(t *testing.T, srv *hooks.Server, path, token, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
