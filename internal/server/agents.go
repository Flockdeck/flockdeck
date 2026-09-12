package server

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/workspace"
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
		s.wakeAsked()
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
	// who has not installed Codex should still learn that Flockdeck would run it.
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
// agent installed while Flockdeck is running is rare enough that a few seconds'
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
		s.agents, s.agentsRoot, s.agentsAt = buildCatalog(s.ws.Catalog(), root), root, time.Now()
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
		// Read agents.json again on the way past. A probe happens when the
		// picker opens and when the last answer has gone stale, which is
		// exactly when an edit made by hand should start counting — and doing it
		// here means the catalog the picker offers and the catalog a pane is
		// started from stay the same object rather than two reads of one file.
		s.ws.ReloadAgents()
		built := buildCatalog(s.ws.Catalog(), root)
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

// buildCatalog turns the workspace's catalog into what the picker draws, and
// asks the machine about each agent in it.
//
// The catalog is passed in rather than read here, because the workspace holds
// the one panes are actually started from. Reading a second copy is how the
// picker comes to offer an agent that starting a pane then says it has never
// heard of.
func buildCatalog(c *agent.Catalog, root string) agentCatalog {
	base := c.DefaultsFor("")
	out := agentCatalog{
		Default: agentChoice{Agent: base.Agent, Model: base.Model},
		Err:     c.Notice,
	}
	// A project is reported as having a choice of its own only where it
	// actually differs. Marking the installation's default as this project's
	// says something untrue about a project nobody has set anything for.
	if proj := c.DefaultsFor(root); proj != base {
		out.Project = &agentChoice{Agent: proj.Agent, Model: proj.Model}
	}
	for _, sp := range c.Visible() {
		out.Items = append(out.Items, catalogAgent{
			ID:           sp.ID,
			Name:         sp.Name,
			Runner:       string(sp.Runner),
			Models:       sp.Models,
			DefaultModel: sp.DefaultModel,
			Available:    agent.Available(sp),
			Install:      sp.Install,
		})
	}
	return out
}

// setAgentDefault records a choice in the user's agents.json: for one project
// where root names one, and for every project otherwise.
func setAgentDefault(root string, choice agentChoice) error {
	path, err := agent.ConfigPath()
	if err != nil {
		return err
	}
	return agent.SetDefaults(filepath.Dir(path), root, agent.Defaults{
		Agent: choice.Agent,
		Model: choice.Model,
	})
}

// ---------------------------------------------------------------------------
// Carrying a choice into the workspace
// ---------------------------------------------------------------------------

// paneAgent reports the agent and model a pane is running, for the header
// badge and the overview. Both are empty for a shell, which has neither.
func paneAgent(p *workspace.Pane) (string, string) {
	if p == nil || !p.IsAgent() {
		return "", ""
	}
	return p.Agent, p.Model
}

// choiceOf reads what a window asked a new pane to run. A command that names
// no agent is every keystroke that does not go through the picker, and means
// the project's default -- so it is passed on empty rather than resolved here,
// where the pane's own directory is not yet known.
func choiceOf(cmd command) workspace.Choice {
	return workspace.Choice{Kind: parseKind(cmd.Kind), Agent: cmd.Agent, Model: cmd.Model}
}

// newTabFor opens a tab running the agent and model the window asked for.
func (s *Server) newTabFor(cmd command) {
	// The title is cleaned the same way a rename is. The worktree panel opens
	// a tab named after a branch, and a branch name has no length to it -- one
	// written out of a ticket title is long enough to push every other tab off
	// the bar, and it is written to the layout that way too. An empty title
	// still means "name it yourself", which is what an agent tab does until it
	// has been asked something.
	s.ws.NewTabWith(choiceOf(cmd), cmd.Path, tabTitle(cmd.Text))
}

// splitPaneFor splits the focused pane, running the agent and model the window
// asked for.
func (s *Server) splitPaneFor(cmd command) {
	// A root names another open project to split into, which is how a tab
	// comes to hold agents from two projects at once. A path names a
	// directory, which is how the worktree panel puts an agent into another
	// checkout of the project it is already in.
	if cmd.Root != "" {
		s.ws.SplitPaneInProjectWith(parseDir(cmd.Dir), choiceOf(cmd), cmd.Root)
		return
	}
	s.ws.SplitPaneInWith(parseDir(cmd.Dir), choiceOf(cmd), cmd.Path)
}

// applyAgentDefault stores what the active project should run when nobody
// chooses, and says so: the picker's tick box is otherwise the only sign that
// anything happened at all.
//
// A target of "all" sets the default every project falls back on instead. The
// picker offers only the project's own, which left that one reachable by
// nothing but editing agents.json.
func (s *Server) applyAgentDefault(c *controlClient, cmd command) {
	root, where := "", "every project"
	if cmd.Target != "all" {
		root = s.activeRoot()
		if root == "" {
			c.notify("there is no project open to set a default for", true)
			return
		}
		where = filepath.Base(root)
	}
	if err := setAgentDefault(root, agentChoice{Agent: cmd.Agent, Model: cmd.Model}); err != nil {
		c.notify("could not save the default agent: "+err.Error(), true)
		return
	}
	c.notify(describeChoice(cmd.Agent, cmd.Model)+" is now the default for "+where, false)
	s.refreshAgents()
}

// describeChoice writes an agent and model the way the pane header does.
func describeChoice(agentID, model string) string {
	if model == "" {
		return agentID
	}
	return agentID + " · " + model
}
