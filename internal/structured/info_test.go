package structured

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

func TestInfoHoldsNamesAndNeverAnMCPServersSecrets(t *testing.T) {
	o := fakeOptions(t)
	o.Spec.ID, o.Spec.ACP.Adapter, o.Spec.ACP.MCP = "fake-cat", "fake-adapter", true
	o.MCPServers = []acp.MCPServer{
		{Name: "flockdeck", Command: `C:\secret\path\flockdeck.exe`, Args: []string{"--token", "ARGSECRET"}, Env: []acp.EnvVar{{Name: "TOK", Value: "ENVSECRET"}}},
		{Name: "lint\nAgent: forged", Command: "lint"},
	}
	r := begin(t, o)
	until(t, r, "ready", func(s Snapshot) bool { return s.Phase == PhaseReady })
	i := r.Info()
	if i.Name != "acptest" || i.Version != "0.0.1" || i.Catalog != "fake-cat" || i.Adapter != "fake-adapter" || i.Phase != PhaseReady {
		t.Fatalf("info = %+v", i)
	}
	if i.SessionID == "" || i.Cwd != o.Cwd {
		t.Errorf("session %q cwd %q", i.SessionID, i.Cwd)
	}
	if len(i.MCP) != 2 || i.MCP[0] != "flockdeck" || strings.ContainsAny(i.MCP[1], "\n") {
		t.Errorf("mcp = %q", i.MCP)
	}
	if got := strings.Join(i.MCP, " "); strings.Contains(got, "SECRET") || strings.Contains(got, "secret") {
		t.Errorf("a server's command, argument or variable is in %q", got)
	}
}

func TestInfoNamesTheSignInMethodsOfAnAgentThatWantsALogin(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithAuth("new")))
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the setup state", func(s Snapshot) bool { return s.Phase == PhaseSetup })
	i := r.Info()
	if i.Phase != PhaseSetup || i.Setup == nil || len(i.Auth) != 2 || i.Auth[0] != "API key" || i.Auth[1] != "Log in" {
		t.Fatalf("info = %+v", i)
	}
}

func TestInfoListsAreBounded(t *testing.T) {
	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, strings.Repeat("n", 500))
	}
	got := names(append(many, ""))
	if len(got) != infoListMax || len([]rune(got[0])) > infoNameCap+1 {
		t.Errorf("%d names, first %d long", len(got), len([]rune(got[0])))
	}
}
