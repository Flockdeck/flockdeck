package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmwri/perch/internal/agent"
	"github.com/jmwri/perch/internal/session"
	"github.com/jmwri/perch/internal/store"
	"github.com/jmwri/perch/internal/workspace"
)

// agentView is one pane in the overview, across every open project.
type agentView struct {
	PaneID  string `json:"paneId"`
	TabID   string `json:"tabId"`
	Tab     string `json:"tab"`
	Root    string `json:"root"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Branch  string `json:"branch"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	For     string `json:"for"`
	Dirty   int    `json:"dirty"`
	Active  bool   `json:"active"`
}

type agentsMsg struct {
	Type  string      `json:"type"`
	Items []agentView `json:"items"`
}

// listAgents answers a request for every pane in every open project.
//
// The tab bar only shows the active project, so once several are open this is
// the only place that answers "where is the agent that needs me".
func (s *Server) listAgents(c *controlClient) {
	s.do(func() {
		msg := agentsMsg{Type: "agents"}
		focused := ""
		if t := s.ws.CurrentTab(); t != nil {
			focused = t.Focus
		}

		for _, t := range s.ws.Tabs {
			for _, id := range t.Tree.Panes() {
				p := s.ws.Pane(id)
				if p == nil {
					continue
				}
				st, detail := p.Status()
				av := agentView{
					PaneID: p.ID,
					TabID:  t.ID,
					Tab:    t.Title,
					// Root says where the pane is drawn, which is what
					// revealing it has to switch to; Project says which
					// project the agent is actually working in. They differ
					// for a pane borrowed onto another project's tab.
					Root:    t.Root,
					Project: filepath.Base(s.ws.RootOf(p.ID)),
					Name:    p.Name,
					Branch:  p.Branch,
					Kind:    kindName(p.Kind),
					Status:  st.String(),
					Detail:  detail,
					Dirty:   p.Git.Dirty + p.Git.Untracked,
					Active:  p.ID == focused,
				}
				if p.Sess != nil {
					av.For = humanAgo(time.Since(p.Sess.StatusSince()))
				}
				msg.Items = append(msg.Items, av)
			}
		}

		// Whatever needs a person comes first; that is the point of the list.
		rank := map[string]int{"waiting": 0, "working": 1, "idle": 2, "starting": 3, "exited": 4}
		sort.SliceStable(msg.Items, func(i, j int) bool {
			ri, rj := rank[msg.Items[i].Status], rank[msg.Items[j].Status]
			if ri != rj {
				return ri < rj
			}
			return msg.Items[i].Project < msg.Items[j].Project
		})
		c.sendJSON(msg)
	})
}

// revealPane brings a pane into view wherever it lives, switching project and
// tab as needed.
func (s *Server) revealPane(root, tabID, paneID string) {
	s.do(func() {
		if root != "" {
			s.ws.SelectProject(root)
		}
		if tabID != "" {
			s.ws.SelectTab(tabID)
		}
		s.ws.FocusPane(paneID)
		s.Wake()
	})
}

// ---------------------------------------------------------------------------
// The catalog
// ---------------------------------------------------------------------------

// agentChoice is one answer to "which agent, which model". An empty model
// means whatever the agent is already set to, which is how a CLI keeps the
// model its own configuration gives it.
type agentChoice struct {
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
}

// catalogAgent is one agent as the picker draws it.
type catalogAgent struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Runner string        `json:"runner,omitempty"`
	Models []agent.Model `json:"models,omitempty"`
	// DefaultModel is the one taken when nobody chooses, so the picker can
	// mark it rather than leaving the choice looking arbitrary.
	DefaultModel string `json:"defaultModel"`
	// Available is whether this machine could start it now. An agent that is
	// not installed is still listed, greyed, with Install beside it: somebody
	// who has not installed Codex should still learn that Perch would run it.
	Available bool   `json:"available"`
	Install   string `json:"install,omitempty"`
}

// agentCatalog is the whole catalog as one snapshot carries it: what there is,
// what is chosen when nobody chooses, and what this project was set to.
type agentCatalog struct {
	Items []catalogAgent `json:"items"`
	// Default is the overall default and Project the active project's own,
	// which overrides it. Project is left out when the project has none.
	Default agentChoice  `json:"default"`
	Project *agentChoice `json:"project,omitempty"`
	// Err says why the user's agents.json was ignored. A damaged file is a
	// line in the picker and nothing more: the built-ins carry on, because
	// refusing to start over a preferences file would be absurd.
	Err string `json:"err,omitempty"`
}

// agentProbeInterval is how long an availability probe is believed for. An
// agent installed while Perch is running is rare enough that a few seconds'
// lag is no worse than the install itself.
const agentProbeInterval = 5 * time.Second

// catalog returns the catalog for the active project, arranging for a fresh
// probe when the last answer has gone stale. It must run on the workspace
// goroutine.
//
// Only the very first probe is taken here. Searching PATH for an agent that is
// not installed means walking every directory on it -- and on Windows every
// extension in PATHEXT as well -- and doing that for a catalog of agents takes
// long enough to be felt, on the one goroutine that owns the workspace and
// dispatches every command from every window. So the answer that is already in
// hand is returned and a new one is worked out beside it.
func (s *Server) catalog() agentCatalog {
	root := s.ws.ActiveRoot()
	if s.agentsAt.IsZero() {
		// The first window is about to draw a picker with nothing in it, and
		// has nothing else to draw it from. This one is worth waiting for.
		s.agents, s.agentsRoot, s.agentsAt = buildCatalog(root), root, time.Now()
		return s.agents
	}
	if s.agentsRoot != root || time.Since(s.agentsAt) >= agentProbeInterval {
		s.probeAgents(root)
	}
	return s.agents
}

// probeAgents works the catalog out away from the workspace goroutine and
// hands it back. A probe already running is left to finish rather than joined
// by a second one. It must be called on the workspace goroutine.
func (s *Server) probeAgents(root string) {
	if s.agentsProbing {
		return
	}
	s.agentsProbing = true
	go func() {
		built := buildCatalog(root)
		s.do(func() {
			s.agents, s.agentsRoot, s.agentsAt = built, root, time.Now()
			s.agentsProbing = false
		})
		// Queued behind the assignment above, so the snapshot this asks for is
		// built from the answer that has just landed.
		s.wakeAsked()
	}()
}

// refreshAgents asks the machine again out of turn. The picker sends this as
// it opens: an agent installed a moment ago should be offered without waiting
// the cache out.
func (s *Server) refreshAgents() {
	s.do(func() {
		// Nothing has been probed at all yet, so the first snapshot will do it
		// anyway and a second probe beside it would only duplicate the work.
		if s.agentsAt.IsZero() {
			return
		}
		s.probeAgents(s.ws.ActiveRoot())
	})
}

// buildCatalog merges the user's file over the built-ins and probes each one.
func buildCatalog(root string) agentCatalog {
	file, err := readAgentsFile()
	out := agentCatalog{Default: file.Defaults}
	if err != nil {
		out.Err = err.Error()
	}
	if out.Default.Agent == "" {
		out.Default.Agent = agentIDClaude
	}
	if c, ok := file.projectDefault(root); ok {
		out.Project = &c
	}
	for _, sp := range mergeAgents(builtinAgents(), file.Agents) {
		if sp.Hidden {
			continue
		}
		out.Items = append(out.Items, catalogAgent{
			ID:           sp.ID,
			Name:         sp.Name,
			Runner:       string(sp.Runner),
			Models:       sp.Models,
			DefaultModel: sp.DefaultModel,
			Available:    availableAgent(sp),
			Install:      sp.Install,
		})
	}
	return out
}

// mergeAgents overlays the user's entries on the built-ins, matched by id.
//
// Each entry is decoded onto the built-in it names rather than replacing it,
// so a field the user did not write keeps the built-in's answer -- which is
// what lets `{"id":"claude","defaultModel":"sonnet"}` change the model and
// nothing else. An id matching no built-in is a new agent.
func mergeAgents(builtin []agent.Spec, entries []json.RawMessage) []agent.Spec {
	out := append([]agent.Spec(nil), builtin...)
	at := make(map[string]int, len(out))
	for i, sp := range out {
		at[sp.ID] = i
	}
	for _, raw := range entries {
		var named struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &named) != nil || named.ID == "" {
			continue
		}
		if i, ok := at[named.ID]; ok {
			_ = json.Unmarshal(raw, &out[i])
			continue
		}
		var sp agent.Spec
		if json.Unmarshal(raw, &sp) != nil {
			continue
		}
		if sp.Name == "" {
			sp.Name = sp.ID
		}
		at[sp.ID] = len(out)
		out = append(out, sp)
	}
	return out
}

// availableAgent reports whether this machine could start the agent now.
//
// A CLI is available when its program is on PATH. An API agent is available
// when a key can be found for it, or when it needs none: an endpoint on
// loopback is somebody's own model server, and those ask for nothing.
//
// SHIM: the key store of section 10 belongs to internal/creds, which task 10
// is writing, so only the environment is consulted here. A key that is only in
// keys.json therefore reads as "not installed" until that arrives.
func availableAgent(sp agent.Spec) bool {
	if sp.Runner == agent.RunnerAPI {
		for _, name := range sp.API.KeyEnv {
			if os.Getenv(name) != "" {
				return true
			}
		}
		return loopback(sp.API.BaseURL)
	}
	if sp.Exe == "" {
		return false
	}
	_, err := exec.LookPath(sp.Exe)
	return err == nil
}

// loopback reports whether a base URL names this machine.
func loopback(base string) bool {
	if base == "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// agents.json
// ---------------------------------------------------------------------------

// agentsFile is the user's `agents.json`, kept beside the rest of the state.
//
// The agents themselves are left undecoded: they are merged onto the built-ins
// field by field, which is decoding them onto something that already exists.
type agentsFile struct {
	Version  int                    `json:"version"`
	Defaults agentChoice            `json:"defaults"`
	Projects map[string]agentChoice `json:"projects"`
	Agents   []json.RawMessage      `json:"agents"`
}

const agentsFileName = "agents.json"

// projectDefault returns what this project was set to, if anything. The keys
// are paths as they were when they were written, so they are compared the way
// every other path in this application is.
func (f agentsFile) projectDefault(root string) (agentChoice, bool) {
	if root == "" {
		return agentChoice{}, false
	}
	want := filepath.Clean(root)
	for k, v := range f.Projects {
		if strings.EqualFold(filepath.Clean(k), want) {
			return v, v.Agent != "" || v.Model != ""
		}
	}
	return agentChoice{}, false
}

// readAgentsFile reads the user's file. A missing one is not an error; a
// damaged one is reported alongside an empty file, so the built-ins carry on
// and the picker can say why the entries are not there.
func readAgentsFile() (agentsFile, error) {
	var f agentsFile
	dir, err := store.Dir()
	if err != nil {
		return f, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, agentsFileName))
	if err != nil {
		return f, nil
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return agentsFile{}, fmt.Errorf("%s could not be read (%v), so only the built-in agents are offered", agentsFileName, err)
	}
	return f, nil
}

// setAgentDefault records what a project -- or the whole application, for an
// empty root -- should run when nobody chooses.
//
// The file belongs to the person using Perch and may well have agents of their
// own in it, so it is edited rather than rewritten: everything but the key
// being set is carried across exactly as it was found.
func setAgentDefault(root string, choice agentChoice) error {
	dir, err := store.Dir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, agentsFileName)

	doc := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &doc) != nil {
			return fmt.Errorf("%s could not be read, so it has been left alone", agentsFileName)
		}
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = json.RawMessage("1")
	}

	key := "defaults"
	var value any = choice
	if root != "" {
		key = "projects"
		projects := map[string]agentChoice{}
		if raw, ok := doc["projects"]; ok {
			_ = json.Unmarshal(raw, &projects)
		}
		// Written under the path as this run spells it, with any older
		// spelling of the same directory removed, so the file cannot end up
		// holding two answers for one project.
		want := filepath.Clean(root)
		for k := range projects {
			if strings.EqualFold(filepath.Clean(k), want) {
				delete(projects, k)
			}
		}
		projects[want] = choice
		value = projects
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	doc[key] = raw

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// writeFileAtomic replaces a file in one step, so an interrupted write cannot
// leave half a settings file behind for the next run to choke on.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// What this build can start
// ---------------------------------------------------------------------------

// agentIDClaude names the agent every pane ran before there was a choice.
const agentIDClaude = "claude"

// builtinAgents is the catalog Perch ships with.
//
// SHIM: the built-in table belongs to internal/agent, which task 1 is writing,
// and will have Codex, Gemini, Aider, Opencode, cursor-agent and the four API
// runners in it as well. Only the Claude entry is here, copied from section 3
// of the design, because it is the only agent this build can actually start --
// and an entry invented for a CLI nobody has checked against would be worse
// than no entry at all.
func builtinAgents() []agent.Spec {
	return []agent.Spec{{
		ID: agentIDClaude, Name: "Claude Code", Runner: agent.RunnerCLI, Exe: "claude",
		Args: []agent.Arg{
			agent.Group("session", "--session-id", "{{session}}"),
			agent.Group("settings", "--settings", "{{settings}}"),
			agent.Group("model", "--model", "{{model}}"),
			agent.Lit("{{prompt}}"),
		},
		ResumeArgs: []agent.Arg{
			agent.Group("session", "--resume", "{{session}}"),
			agent.Group("settings", "--settings", "{{settings}}"),
			agent.Group("model", "--model", "{{model}}"),
		},
		Caps: agent.Caps{Hooks: true, Resume: true, Transcript: true, Trust: true, Context: agent.ContextHook},
		Models: []agent.Model{
			{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
			{ID: "opus", Name: "Opus", Note: "most capable"},
			{ID: "sonnet", Name: "Sonnet", Note: "the everyday one"},
			{ID: "haiku", Name: "Haiku", Note: "fastest"},
		},
		StripEnv: []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_ENTRYPOINT",
			"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_DONT_INHERIT_ENV"},
		Install: "https://claude.com/claude-code",
	}}
}

// ---------------------------------------------------------------------------
// Carrying a choice into the workspace
// ---------------------------------------------------------------------------

/*
SHIM: these are where a chosen agent and model meet the workspace, and the
workspace does not take one yet -- `workspace.Pane` gains `Agent` and `Model`,
and NewTab, the splits and Spawn gain the pair, in task 3's files. They are
gathered here, in a file this task owns, so the merge has one place to change
rather than a dozen call sites scattered through control.go. Until then a
choice is accepted, carried across the protocol and shown, and an agent pane
runs Claude exactly as it always did.
*/

// paneAgent reports the agent and model a pane is running, for the header
// badge and the overview. Both are empty for a shell, which has neither.
func paneAgent(p *workspace.Pane) (string, string) {
	if p == nil || p.Kind == session.KindShell {
		return "", ""
	}
	return agentIDClaude, ""
}

// newTabFor opens a tab running the agent and model the window asked for.
func (s *Server) newTabFor(cmd command) {
	// The title is cleaned the same way a rename is. The worktree panel opens
	// a tab named after a branch, and a branch name has no length to it -- one
	// written out of a ticket title is long enough to push every other tab off
	// the bar, and it is written to the layout that way too. An empty title
	// still means "name it yourself", which is what an agent tab does until it
	// has been asked something.
	s.ws.NewTab(parseKind(cmd.Kind), cmd.Path, tabTitle(cmd.Text))
}

// splitPaneFor splits the focused pane, running the agent and model the window
// asked for.
func (s *Server) splitPaneFor(cmd command) {
	// A root names another open project to split into, which is how a tab
	// comes to hold agents from two projects at once. A path names a
	// directory, which is how the worktree panel puts an agent into another
	// checkout of the project it is already in.
	if cmd.Root != "" {
		s.ws.SplitPaneInProject(parseDir(cmd.Dir), parseKind(cmd.Kind), cmd.Root)
		return
	}
	s.ws.SplitPaneIn(parseDir(cmd.Dir), parseKind(cmd.Kind), cmd.Path)
}

// applyAgentDefault stores what the active project should run when nobody
// chooses, and says so: the picker's tick box is otherwise the only sign that
// anything happened at all.
func (s *Server) applyAgentDefault(c *controlClient, cmd command) {
	root := s.activeRoot()
	if root == "" {
		c.notify("there is no project open to set a default for", true)
		return
	}
	if err := setAgentDefault(root, agentChoice{Agent: cmd.Agent, Model: cmd.Model}); err != nil {
		c.notify("could not save the default agent: "+err.Error(), true)
		return
	}
	c.notify(describeChoice(cmd.Agent, cmd.Model)+" is now the default for "+filepath.Base(root), false)
	s.refreshAgents()
}

// describeChoice writes an agent and model the way the pane header does.
func describeChoice(agentID, model string) string {
	if model == "" {
		return agentID
	}
	return agentID + " · " + model
}
