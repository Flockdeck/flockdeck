package workspace

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/store"
)

func mcpNames(l []acp.MCPServer) []string {
	out := []string{}
	for _, s := range l {
		out = append(out, s.Name)
	}
	return out
}

func mcpWorkspace(token func(string) string, user []store.MCPServerPref) *Workspace {
	w := &Workspace{}
	w.UseMCPToken(token)
	w.UseMCPSources(func() (string, error) { return "/opt/fd/flockdeck", nil }, func() []store.MCPServerPref { return user })
	return w
}

// Flockdeck's entry is a stdio command with the pane's token in the entry's own
// environment.
func TestFlockdeckEntryCarriesThePaneTokenInItsOwnEnv(t *testing.T) {
	w := mcpWorkspace(func(id string) string { return "tok-" + id }, nil)
	got := w.mcpServersFor(&Pane{ID: "p1"}, agent.Spec{ACP: agent.ACPSpec{MCP: true}})
	if len(got) != 1 {
		t.Fatalf("servers = %+v", got)
	}
	s := got[0]
	if s.Name != "flockdeck" || s.Command != "/opt/fd/flockdeck" || !slices.Equal(s.Args, []string{"mcp-server"}) {
		t.Errorf("entry = %+v", s)
	}
	if len(s.Env) != 1 || s.Env[0].Name != "FLOCKDECK_TOKEN" || s.Env[0].Value != "tok-p1" {
		t.Errorf("env = %+v", s.Env)
	}
}

// Until the server exists nothing registers a token source, and the entry is not
// sent; a token source that has no token for the pane leaves it out too.
func TestFlockdeckEntryIsOffUntilTheServerExists(t *testing.T) {
	w := mcpWorkspace(nil, nil)
	if got := w.mcpServersFor(&Pane{ID: "p1"}, agent.Spec{ACP: agent.ACPSpec{MCP: true}}); got == nil || len(got) != 0 {
		t.Errorf("servers = %#v, want an empty list", got)
	}
	w = mcpWorkspace(func(string) string { return "" }, nil)
	if got := w.mcpServersFor(&Pane{ID: "p1"}, agent.Spec{ACP: agent.ACPSpec{MCP: true}}); len(got) != 0 {
		t.Errorf("servers = %+v", got)
	}
	w = mcpWorkspace(func(string) string { return "t" }, nil)
	w.UseMCPSources(func() (string, error) { return "", errors.New("no") }, func() []store.MCPServerPref { return nil })
	if got := w.mcpServersFor(&Pane{ID: "p1"}, agent.Spec{ACP: agent.ACPSpec{MCP: true}}); len(got) != 0 {
		t.Errorf("servers = %+v without a program to run", got)
	}
}

// An agent whose entry says it takes no servers is sent none, the user's
// included.
func TestAnAgentThatTakesNoServersGetsNone(t *testing.T) {
	w := mcpWorkspace(func(string) string { return "t" }, []store.MCPServerPref{{Name: "a", Command: "x"}})
	got := w.mcpServersFor(&Pane{ID: "p1"}, agent.Spec{ACP: agent.ACPSpec{MCP: false}})
	if got == nil || len(got) != 0 {
		t.Errorf("servers = %#v", got)
	}
}

func TestUserServersFollowFlockdecksAndAreChecked(t *testing.T) {
	user := []store.MCPServerPref{
		{Name: "files", Command: "mcp-files", Args: []string{"--root", "."}, Env: map[string]string{"KEY": "v", "B": "2"}},
		{Name: "Flockdeck", Command: "evil"},                 // takes Flockdeck's name
		{Name: "files", Command: "dup"},                      // duplicate
		{Name: "off", Command: "x", Off: true},               // switched off
		{Name: "bad name", Command: "x"},                     // not a plain name
		{Name: "nocmd"},                                      // no command
		{Name: "nl", Command: "a\nb"},                        // line break in the program
		{Name: "arg", Command: "x", Args: []string{"a\x00"}}, // NUL in an argument
		{Name: "ok2", Command: "x"},
	}
	w := mcpWorkspace(func(string) string { return "t" }, user)
	got := w.mcpServersFor(&Pane{ID: "p"}, agent.Spec{ACP: agent.ACPSpec{MCP: true}})
	if names := mcpNames(got); !slices.Equal(names, []string{"flockdeck", "files", "ok2"}) {
		t.Fatalf("names = %v", names)
	}
	f := got[1]
	if f.Command != "mcp-files" || !slices.Equal(f.Args, []string{"--root", "."}) {
		t.Errorf("files = %+v", f)
	}
	if len(f.Env) != 2 || f.Env[0].Name != "B" || f.Env[1].Name != "KEY" {
		t.Errorf("env = %+v, want B and KEY in order", f.Env)
	}
	if got[2].Args == nil || got[2].Env == nil {
		t.Error("args and env must be empty lists and not nil")
	}
}

// A user entry never carries a variable of Flockdeck's own, in any case, and
// takes nothing from the process environment.
func TestUserServerEnvIsScrubbed(t *testing.T) {
	t.Setenv("FLOCKDECK_TOKEN", "process-secret")
	t.Setenv("OPENAI_API_KEY", "process-key")
	user := []store.MCPServerPref{{Name: "s", Command: "x", Env: map[string]string{
		"FLOCKDECK_TOKEN": "stolen", "flockdeck_api": "a", "Perch_Token": "p", "1BAD": "x", "A=B": "y", "SAFE": "ok",
	}}}
	w := mcpWorkspace(func(string) string { return "pane-token" }, user)
	got := w.mcpServersFor(&Pane{ID: "p"}, agent.Spec{ACP: agent.ACPSpec{MCP: true}})
	if len(got) != 2 {
		t.Fatalf("servers = %+v", got)
	}
	if len(got[1].Env) != 1 || got[1].Env[0] != (acp.EnvVar{Name: "SAFE", Value: "ok"}) {
		t.Errorf("user env = %+v", got[1].Env)
	}
	for _, s := range got {
		for _, e := range s.Env {
			if strings.Contains(e.Value, "process-") || e.Value == "stolen" {
				t.Errorf("%s carries %s=%s", s.Name, e.Name, e.Value)
			}
		}
	}
	if got[0].Env[0].Value != "pane-token" {
		t.Errorf("flockdeck env = %+v", got[0].Env)
	}
}

func TestTheServerListIsCapped(t *testing.T) {
	var user []store.MCPServerPref
	for i := 0; i < 40; i++ {
		user = append(user, store.MCPServerPref{Name: "s" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Command: "x"})
	}
	if got := buildMCPServers(nil, user); len(got) != maxMCPServers {
		t.Errorf("%d servers, want %d", len(got), maxMCPServers)
	}
}

// Windows never see the user's servers: their env may hold secrets.
func TestWindowsAreNotSentMCPServers(t *testing.T) {
	p := store.Prefs{MCPServers: []store.MCPServerPref{{Name: "s", Command: "x", Env: map[string]string{"K": "secret"}}}}
	if got := p.ForWindow(); got.MCPServers != nil {
		t.Errorf("ForWindow kept %+v", got.MCPServers)
	}
}
