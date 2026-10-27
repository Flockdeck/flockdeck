package workspace

import (
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/store"
)

// The MCP servers a structured pane's agent is asked to start are Flockdeck's
// own entry, when its server exists, and the ones the user configured in
// prefs.json. They are built once per session from Flockdeck's own state and
// the user's file, never from the process environment: a server gets exactly
// the variables written in its entry.

const (
	// FlockdeckMCPName is the name of Flockdeck's own entry. A user entry cannot
	// take it.
	FlockdeckMCPName = "flockdeck"
	// MCPTokenEnv is the one variable of Flockdeck's own that an entry carries,
	// on Flockdeck's entry only: the pane's token.
	MCPTokenEnv = "FLOCKDECK_TOKEN"

	maxMCPServers = 16
	maxMCPArgs    = 64
	maxMCPEnv     = 64
	maxMCPText    = 8192
)

var (
	mcpNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	mcpEnvRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// UseMCPToken makes structured panes start Flockdeck's own MCP server
// (`flockdeck mcp-server`) with the token fn returns for the pane. It is called
// once the server exists; until then no pane is given the entry. A token that
// is empty leaves the entry out for that pane.
func (w *Workspace) UseMCPToken(fn func(paneID string) string) {
	w.mu.Lock()
	w.mcpToken = fn
	w.mu.Unlock()
}

// UseMCPSources replaces where the running program's path and the user's
// servers are read from. It is for tests; nil keeps the default.
func (w *Workspace) UseMCPSources(exe func() (string, error), user func() []store.MCPServerPref) {
	w.mu.Lock()
	w.mcpExe, w.mcpUser = exe, user
	w.mu.Unlock()
}

// mcpServersFor is the list for session/new of the structured pane p. It is
// empty, and not nil, when the agent does not take servers.
func (w *Workspace) mcpServersFor(p *Pane, spec agent.Spec) []acp.MCPServer {
	if !spec.ACP.MCP {
		return []acp.MCPServer{}
	}
	w.mu.RLock()
	tokenFor, exe, user := w.mcpToken, w.mcpExe, w.mcpUser
	w.mu.RUnlock()
	var own *acp.MCPServer
	if tokenFor != nil {
		if tok := tokenFor(p.ID); tok != "" {
			if exe == nil {
				exe = os.Executable
			}
			if path, err := exe(); err == nil && path != "" {
				own = &acp.MCPServer{Name: FlockdeckMCPName, Command: path, Args: []string{"mcp-server"},
					Env: []acp.EnvVar{{Name: MCPTokenEnv, Value: tok}}}
			}
		}
	}
	var prefs []store.MCPServerPref
	if user != nil {
		prefs = user()
	} else {
		prefs = store.LoadPrefs().MCPServers
	}
	return buildMCPServers(own, prefs)
}

// buildMCPServers puts Flockdeck's entry first and then the user's, each
// checked. An entry that fails a check is left out and the rest stay: a typo in
// one server must not stop the agent. The environment of a user entry is
// exactly what it names, minus any variable of Flockdeck's own.
func buildMCPServers(own *acp.MCPServer, user []store.MCPServerPref) []acp.MCPServer {
	out := []acp.MCPServer{}
	if own != nil {
		out = append(out, *own)
	}
	seen := map[string]bool{strings.ToLower(FlockdeckMCPName): true}
	for _, u := range user {
		if len(out) >= maxMCPServers {
			break
		}
		if u.Off || !mcpNameRe.MatchString(u.Name) || seen[strings.ToLower(u.Name)] {
			continue
		}
		if !mcpText(u.Command) || len(u.Args) > maxMCPArgs {
			continue
		}
		s := acp.MCPServer{Name: u.Name, Command: u.Command, Args: []string{}, Env: []acp.EnvVar{}}
		ok := true
		for _, a := range u.Args {
			if !mcpText(a) && a != "" || len(a) > maxMCPText {
				ok = false
				break
			}
			s.Args = append(s.Args, a)
		}
		if !ok {
			continue
		}
		names := make([]string, 0, len(u.Env))
		for k := range u.Env {
			if mcpEnvRe.MatchString(k) && !agent.ReservedEnv(k) {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		if len(names) > maxMCPEnv {
			names = names[:maxMCPEnv]
		}
		for _, k := range names {
			v := u.Env[k]
			if len(v) > maxMCPText || strings.ContainsRune(v, 0) {
				continue
			}
			s.Env = append(s.Env, acp.EnvVar{Name: k, Value: v})
		}
		seen[strings.ToLower(u.Name)] = true
		out = append(out, s)
	}
	return out
}

// mcpText says s is a usable program or argument: not empty, not too long, and
// without a NUL or a line break.
func mcpText(s string) bool {
	return s != "" && len(s) <= maxMCPText && !strings.ContainsAny(s, "\x00\r\n")
}
