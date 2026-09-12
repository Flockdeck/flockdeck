package server

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/pricing"
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
		names := map[string]string{}
		for _, pr := range s.ws.Projects() {
			names[pr.Root] = pr.Name
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
					Project: projectLabel(names, s.ws.RootOf(p.ID)),
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
//
// The list it is picked from was read when the overview opened, and a pane in
// it may have closed since. Every step below ignores a pane that is not there,
// so the click did nothing at all and nothing said why; it is answered the way
// a click on any other pane that has gone is.
func (s *Server) revealPane(c *controlClient, root, tabID, paneID string) {
	s.do(func() {
		if s.ws.Pane(paneID) == nil {
			c.notify(paneGone, true)
			return
		}
		// The list says where the pane was when it was drawn -- the overview
		// when it opened, a notification when it came -- and a pane can have
		// changed tab since: moved into another, or given one of its own when
		// the project it was borrowed into closed. Focusing a pane only works
		// in its own tab, so where it is now is what is shown.
		if id := s.ws.TabIDOf(paneID); id != "" {
			tabID, root = id, s.ws.Tab(id).Root
		}
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

// modelView is one model as the picker and the fan-out's selects draw it: the
// catalog's entry for it, and what it costs where that is known and true of
// what the user pays.
type modelView struct {
	agent.Model
	Price *priceView `json:"price,omitempty"`
}

// priceView is a model's published price per million tokens, with the day it
// was read, so a figure shown beside a model always says how old it is.
type priceView struct {
	In      float64 `json:"in"`
	Out     float64 `json:"out"`
	Checked string  `json:"checked"`
}

// modelViews is an agent's models as the window draws them.
//
// A price goes only on the models of an API agent talking to its vendor's own
// endpoint. There the id is exactly what the vendor bills, and the key is the
// user's. A command-line agent's model is often an alias the tool resolves for
// itself, and it is as likely to run on a subscription as on a key; an endpoint
// of the user's own -- a gateway, a proxy -- charges what it charges. A
// per-token price beside either would say something untrue about what the
// user pays.
func modelViews(spec agent.Spec) []modelView {
	if len(spec.Models) == 0 {
		return nil
	}
	priced := spec.Runner == agent.RunnerAPI && spec.API.BaseURL == ""
	today := time.Now()
	out := make([]modelView, len(spec.Models))
	for i, m := range spec.Models {
		out[i] = modelView{Model: m}
		if !priced {
			continue
		}
		if r, ok := pricing.Lookup(m.ID, today); ok {
			out[i].Price = &priceView{In: r.In, Out: r.Out, Checked: r.Checked}
		}
	}
	return out
}

// catalogAgent is one agent as the picker draws it.
type catalogAgent struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Runner string      `json:"runner,omitempty"`
	Models []modelView `json:"models,omitempty"`
	// DefaultModel is the one taken when nobody chooses, so the picker can
	// mark it rather than leaving the choice looking arbitrary.
	DefaultModel string `json:"defaultModel"`
	// Available is whether this machine could start it now. An agent that is
	// not installed is still listed, greyed, with Install beside it: somebody
	// who has not installed Codex should still learn that Flockdeck would run it.
	Available bool   `json:"available"`
	Install   string `json:"install,omitempty"`
	// Addressable is whether the picker offers a field for the agent's
	// address (agent.TakesAddress), and Address what that address is now.
	// Any name or password written into it by hand is masked: the picker
	// shows it, and through remote access the relay would carry it.
	Addressable bool   `json:"addressable,omitempty"`
	Address     string `json:"address,omitempty"`
}

// agentCatalog is the whole catalog as one snapshot carries it: what there is,
// what is chosen when nobody chooses, and what this project was set to.
type agentCatalog struct {
	Items []catalogAgent `json:"items"`
	// Default is the overall default and Project the active project's own,
	// which overrides it. Project is left out when the project has none.
	Default agentChoice  `json:"default"`
	Project *agentChoice `json:"project,omitempty"`
	// Routing is what Settings › Agents › Routing draws.
	Routing *routingView `json:"routing,omitempty"`
	// Err says why the user's agents.json was ignored. A damaged file is a
	// line in the picker and nothing more: the built-ins carry on, because
	// refusing to start over a preferences file would be absurd.
	Err string `json:"err,omitempty"`
}

// agentProbeInterval is how long the catalog a snapshot carries is believed
// for before it is worked out again in the background.
//
// It used to be five seconds, the same as the machine's own answers are kept
// for, so every background probe found them just expired and searched PATH
// afresh for every agent -- a third of a second, on a Windows machine with an
// ordinary PATH, every five seconds for as long as a window was open. The
// only thing that reads the answer is the picker, and the picker asks the
// machine again itself the moment it opens, which is when an agent installed
// a moment ago has to show up. So this only keeps a catalog nobody is
// looking at from going stale for ever.
const agentProbeInterval = time.Minute

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
		defer s.survive("asking which agents are installed")
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
		// What the machine was last asked is trusted for a few seconds, which
		// is the wait this exists to skip: the agent installed a moment ago,
		// or the key just saved, would otherwise go on reading as missing.
		agent.Refresh()
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
		Routing: routingOf(c, root),
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
			Models:       modelViews(sp),
			DefaultModel: sp.DefaultModel,
			Available:    agent.Available(sp),
			Install:      sp.Install,
			Addressable:  agent.TakesAddress(sp),
			Address:      maskAddress(sp.API.BaseURL),
		})
	}
	return out
}

// maskAddress hides the password in an address written into agents.json by
// hand. The picker refuses to save one, but it cannot stop one being typed
// into the file.
func maskAddress(address string) string {
	if u, err := url.Parse(address); err == nil && u.User != nil {
		return u.Redacted()
	}
	return address
}

// agentAddressMsg answers an address typed into the picker: the address as it
// was sent, and why it was refused where it was. The picker shows the refusal
// under the field, beside what was typed, so it can be corrected there.
type agentAddressMsg struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Address string `json:"address"`
	Error   string `json:"error,omitempty"`
}

// setAgentAddress records the address an API agent talks to, typed into the
// picker. It was the one setting the OpenAI-compatible entry needs before it
// can be used, and it could be given only by editing agents.json.
//
// The catalog is asked afresh once it is saved. An address on this machine
// needs no key, so a local model server is offered the moment its address is,
// rather than when the cached answer happens to run out.
func (s *Server) setAgentAddress(c *controlClient, id, address string) {
	address = strings.TrimSpace(address)
	reply := func(problem string) {
		c.sendJSON(agentAddressMsg{Type: "agentAddress", ID: id, Address: address, Error: problem})
	}
	spec, ok := ask(s, func() agent.Spec {
		sp, _ := s.ws.Catalog().Find(id)
		return sp
	})
	if !ok {
		return
	}
	builtin := false
	for _, b := range agent.Builtins() {
		builtin = builtin || b.ID == id
	}
	switch {
	case spec.ID == "":
		reply(fmt.Sprintf("there is no agent called %q", id))
		return
	case !agent.TakesAddress(spec):
		// The picker offers the field to none of these, but anything else
		// speaking to this socket could ask.
		reply(spec.Name + " talks to its vendor's own endpoint; its address is changed with `flockdeck keys endpoint " + id + " <address>`")
		return
	case address == "" && !builtin:
		// An agent of the user's own is the address it was given; without one
		// it would be talking to whichever vendor its wire names.
		reply(spec.Name + " is an agent of your own and has no other address to fall back on; type the one it should use, as in http://127.0.0.1:11434/v1")
		return
	}
	go func() {
		defer s.survive("saving an agent's address")
		// The address is written into the same agents.json as the defaults
		// and the routing, read and written back whole, so it waits for
		// those saves as they wait for one another. Saved beside one of them
		// from another window, whichever wrote second put back a copy without
		// the other's change, after both windows were told theirs was saved.
		path, err := agent.ConfigPath()
		if err == nil {
			defaultWrites.Lock()
			err = agent.SetBaseURL(filepath.Dir(path), id, address)
			defaultWrites.Unlock()
		}
		if err != nil {
			reply(err.Error())
			return
		}
		reply("")
		c.notify(addressNotice(spec, address), false)
		s.refreshAgents()
	}()
}

// addressNotice says what an address just saved means for the agent: whether
// it can be used now, or still needs a key, and that a pane already running
// goes on talking to wherever it started with.
func addressNotice(spec agent.Spec, address string) string {
	name := spec.Name
	if address == "" {
		if spec.ID == agent.OpenAICompatibleID {
			return name + " has no address now, and is not offered until it is given one"
		}
		return name + " talks to its vendor's own endpoint again; panes already running keep the old address until they are restarted"
	}
	spec.API.BaseURL = address
	text := name + " now talks to " + maskAddress(address)
	switch {
	case agent.NeedsNoKey(spec):
		text += ", which is on this machine and needs no key"
	case !agent.KeyProbe(spec):
		text += ", and needs a key before it can be used: set one under API keys…"
	}
	return text + "; panes already running keep the old address until they are restarted"
}

// defaultWrites keeps two defaults saved at once -- from two windows -- from
// each reading agents.json, changing its own entry, and writing back a copy
// without the other's, after both windows were told theirs was saved. The keys
// dialog guards its file the same way (keyWrites).
var defaultWrites sync.Mutex

// setAgentDefault records a choice in the user's agents.json: for one project
// where root names one, and for every project otherwise.
func setAgentDefault(root string, choice agentChoice) error {
	path, err := agent.ConfigPath()
	if err != nil {
		return err
	}
	defaultWrites.Lock()
	defer defaultWrites.Unlock()
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
// nothing but editing agents.json. The picker sends it as kind, and target is
// read too, so either spelling reaches the same place.
//
// No agent at all clears the default, which then falls back to the one above
// it. An agent the catalog has never heard of is refused rather than written:
// the picker only offers real ones, but anything else speaking to this socket
// could make a typo the default, and every pane started afterwards would fail.
func (s *Server) applyAgentDefault(c *controlClient, cmd command) {
	type facts struct {
		root  string
		known bool
	}
	f, _ := ask(s, func() facts {
		_, known := s.ws.Catalog().Find(cmd.Agent)
		return facts{root: s.ws.ActiveRoot(), known: known || cmd.Agent == ""}
	})
	if !f.known {
		c.notify(fmt.Sprintf("there is no agent called %q", cmd.Agent), true)
		return
	}
	root, where := "", "every project"
	if cmd.Target != "all" && cmd.Kind != "all" {
		root = f.root
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
	switch {
	case cmd.Agent == "" && root == "":
		c.notify("cleared the default agent for every project", false)
	case cmd.Agent == "":
		// A project that forgets its own choice is not left with none: it
		// runs the one every project falls back on, and saying so is clearer
		// than saying only what went.
		c.notify("cleared the default agent for "+where+", which now runs the default for every project", false)
	default:
		c.notify(describeChoice(cmd.Agent, cmd.Model)+" is now the default for "+where, false)
	}
	s.refreshAgents()
}

// describeChoice writes an agent and model the way the pane header does.
func describeChoice(agentID, model string) string {
	if model == "" {
		return agentID
	}
	return agentID + " · " + model
}
