package server

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/route"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// Model routing, as far as the server takes part in it: working out a routed
// model for a spawned helper, the policy Settings shows and changes, and the
// routing log an earlier build kept.

// routeSpawnChoice decides a model, and maybe an agent, for a helper spawned
// with `flockdeck spawn` and given neither --agent nor --model. It answers
// only in "auto" mode, and the pane's header says what it chose.
// baseAgent and baseModel are what the helper would have run without
// routing, for the pane to record as RoutedFromAgent and RoutedFrom.
func routeSpawnChoice(c *agent.Catalog, cwd, task string) (d route.Decision, baseAgent, baseModel string) {
	policy, _ := c.RoutingFor(cwd)
	if policy.Mode != agent.RoutingAuto {
		return route.Decision{}, "", ""
	}
	spec, model, ok := c.Resolve(cwd, "", "")
	if !ok {
		return route.Decision{}, "", ""
	}
	r := route.New(policy)
	var other func(string) (agent.Spec, bool)
	if policy.CrossAgent {
		other = otherAgentFor(c, cwd)
	}
	return r.Decide(route.Input{Kind: route.KindSpawn, Task: task, Agent: spec, Current: model, OtherAgent: other}), spec.ID, model
}

// otherAgentFor is the route.Input.OtherAgent a spawned helper may use: an agent besides the one the work would run on, but only when it
// could actually take the work right now -- installed or keyed, trusted for
// root, and, for one with an address of its own, answering.
//
// This, and not route.Decide, is where routing does its I/O: deciding stays
// pure, and this closure is the one thing here that touches disk or network.
func otherAgentFor(c *agent.Catalog, root string) func(id string) (agent.Spec, bool) {
	return func(id string) (agent.Spec, bool) {
		spec, ok := c.Find(id)
		if !ok || !agent.Available(spec) || !session.TrustedFor(spec, root) {
			return agent.Spec{}, false
		}
		if spec.API.BaseURL != "" && !endpointReachable(spec.API.BaseURL) {
			return agent.Spec{}, false
		}
		return spec, true
	}
}

// reachTTL is how long endpointReachable trusts an answer, the same span
// agent.Available trusts its own probes for: long enough that a burst
// of spawns dials an endpoint once, short enough that
// starting the local model server and routing again finds it.
const reachTTL = 5 * time.Second

var reach = struct {
	sync.Mutex
	seen map[string]reachResult
}{seen: map[string]reachResult{}}

type reachResult struct {
	ok bool
	at time.Time
}

// dialTimeout bounds how long endpointReachable waits for a connection, kept
// short because it runs in line with every routed decision a spawn asks for.
var dialTimeout = 500 * time.Millisecond

// endpointReachable reports whether something answers at base's host and
// port, remembering the answer for reachTTL.
//
// agent.Available reports a local endpoint as available the moment its
// address is a loopback one, without asking whether anything is listening
// there -- which is right for the picker, where picking a stopped Ollama and
// finding out is the user's own choice, but wrong here: routing to it would
// cut the helper's worktree, start its pane, and only then learn the pane
// could never connect, with nothing left to discard the worktree afterwards.
func endpointReachable(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	now := time.Now()
	reach.Lock()
	if got, ok := reach.seen[host]; ok && now.Sub(got.at) < reachTTL {
		reach.Unlock()
		return got.ok
	}
	reach.Unlock()

	ok := dialReachable(host)
	reach.Lock()
	reach.seen[host] = reachResult{ok: ok, at: now}
	reach.Unlock()
	return ok
}

// dialReachable is the dial itself, a variable so a test can answer without
// a real socket.
var dialReachable = func(host string) bool {
	conn, err := net.DialTimeout("tcp", host, dialTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// routeNote says why routing can do nothing for work on a model, or "" when
// it can.
func routeNote(spec agent.Spec, model string) string {
	tiered := false
	for _, m := range spec.Models {
		tiered = tiered || agent.TierRank(m.Tier) > 0
	}
	switch {
	case !tiered:
		return "None of " + spec.Name + "'s models has a tier, so routing has nothing to choose between. A model's tier can be given in agents.json."
	case route.TierOf(spec, model) == 0:
		return "Routing leaves " + spec.Name + "'s " + modelNameOf(spec, model) +
			" alone, since how capable it is is not known here. Choose a model for the run to let routing choose smaller or stronger ones."
	}
	return ""
}

// modelNameOf is a model as a person reads it: its name where the catalog
// gives one, and "Default" for the empty model.
func modelNameOf(spec agent.Spec, id string) string {
	for _, m := range spec.Models {
		if m.ID == id && m.Name != "" {
			return m.Name
		}
	}
	if id == "" {
		return "Default"
	}
	return id
}

// routingLogDir is where the routing log is kept: Flockdeck's state
// directory. The tests move it by moving the state directory itself, as they
// do for every other file kept there; nothing reassigns this.
var routingLogDir = store.Dir

// paneRoute is what a pane's header says of its routing: the rule that chose
// its model, the model it would otherwise have run, and whether that moved it
// "down" or "up" -- empty where the tiers no longer say. RoutedFromAgent
// names the agent that model belongs to, when routing moved the pane to
// another agent, so the pane's tier is not read off the wrong catalog entry.
func paneRoute(c *agent.Catalog, p *workspace.Pane) (rule, from, fromAgent, dir string) {
	if p == nil || !p.IsAgent() || p.Routed == "" {
		return "", "", "", ""
	}
	rule, from, fromAgent = p.Routed, p.RoutedFrom, p.RoutedFromAgent
	toSpec, ok := c.Find(p.Agent)
	if !ok {
		return rule, from, fromAgent, ""
	}
	fromSpec := toSpec
	if fromAgent != "" && fromAgent != p.Agent {
		if s, ok := c.Find(fromAgent); ok {
			fromSpec = s
		}
	}
	to, was := route.TierOf(toSpec, p.Model), route.TierOf(fromSpec, from)
	switch {
	case to > 0 && was > 0 && to < was:
		dir = "down"
	case to > was && was > 0:
		dir = "up"
	}
	return rule, from, fromAgent, dir
}

// routingView is Settings › Routing: the mode and floor for
// every project and, where it has its own, for this one; the rules this
// project is routed by; and why routing can do nothing here, where that is so.
type routingView struct {
	Every   routingChoice  `json:"every"`
	Project *routingChoice `json:"project,omitempty"`
	Rules   []ruleView     `json:"rules"`
	BuiltIn bool           `json:"builtIn"`
	Note    string         `json:"note,omitempty"`
	// CrossAgent is the policy's own switch for moving a rule's work to
	// another agent, not only another model. It is read from the same
	// policy Every or Project names, never mixed between the two.
	CrossAgent bool `json:"crossAgent,omitempty"`
	// Config is where agents.json is, since the rules are edited there.
	Config string `json:"config,omitempty"`
}

type routingChoice struct {
	Mode     string `json:"mode"`
	Floor    string `json:"floor"`
	Strategy string `json:"strategy"`
}

// ruleView is one rule as Settings lists it, in words, with the log's own
// account of how often what it chose was kept, where the log has any.
type ruleView struct {
	Name       string `json:"name"`
	Choice     string `json:"choice"`
	When       string `json:"when"`
	Kept       int    `json:"kept,omitempty"`
	Overridden int    `json:"overridden,omitempty"`
}

func routingOf(c *agent.Catalog, root string) *routingView {
	v := &routingView{Every: routingChoice{Mode: modeOr(c.Routing.Mode), Floor: c.Routing.Floor, Strategy: strategyOr(c.Routing.Strategy)}}
	policy, own := c.RoutingFor(root)
	if own && root != "" {
		v.Project = &routingChoice{Mode: modeOr(policy.Mode), Floor: policy.Floor, Strategy: strategyOr(policy.Strategy)}
	}
	v.BuiltIn = policy.Rules == nil
	v.CrossAgent = policy.CrossAgent
	stats := map[string]route.RuleStat{}
	if dir, err := routingLogDir(); err == nil {
		if entries, err := route.ReadLog(dir); err == nil {
			for _, s := range route.Stats(entries) {
				stats[s.Rule] = s
			}
		}
	}
	for _, r := range route.RulesOf(policy) {
		choice := r.Tier
		if r.Model != "" {
			choice = r.Agent + " · " + r.Model
		}
		rv := ruleView{Name: r.Name, Choice: choice, When: describeWhen(r.When)}
		if s, ok := stats[r.Name]; ok {
			rv.Kept, rv.Overridden = s.Kept, s.Overridden
		}
		v.Rules = append(v.Rules, rv)
	}
	if route.New(policy).On() {
		if spec, model, ok := c.Resolve(root, "", ""); ok {
			v.Note = routeNote(spec, model)
		}
		if v.Note == "" {
			v.Note = crossAgentNoteFor(c, policy)
		}
	}
	if path, err := agent.ConfigPath(); err == nil {
		v.Config = path
	}
	return v
}

// crossAgentNoteFor names, for the rules a policy applies, another agent a
// cross-agent rule points at that could never be routed to as things stand --
// no address, a model it does not offer, or one with no tier -- so Settings
// says what is missing rather than routing quietly doing nothing about it.
func crossAgentNoteFor(c *agent.Catalog, policy agent.RoutingPolicy) string {
	if !policy.CrossAgent {
		return ""
	}
	for _, r := range route.RulesOf(policy) {
		if r.Model == "" || r.Agent == "" {
			continue
		}
		spec, ok := c.Find(r.Agent)
		if !ok {
			continue // named in the notice already, by Catalog.Notice
		}
		if agent.TakesAddress(spec) && spec.API.BaseURL == "" {
			return "Rule '" + r.Name + "' names " + spec.Name + ", which has no address yet; give it one in Settings › Agents."
		}
		m, ok := findModelIn(spec, r.Model)
		switch {
		case !ok:
			return "Rule '" + r.Name + "' names " + r.Model + ", which " + spec.Name + " does not offer."
		case agent.TierRank(m.Tier) == 0:
			return "Rule '" + r.Name + "' names " + spec.Name + "'s " + modelNameOf(spec, r.Model) + ", which has no tier yet; give it one in agents.json."
		}
	}
	return ""
}

func findModelIn(spec agent.Spec, id string) (agent.Model, bool) {
	for _, m := range spec.Models {
		if m.ID == id {
			return m, true
		}
	}
	return agent.Model{}, false
}

func modeOr(mode string) string {
	if mode == "" {
		return agent.RoutingOff
	}
	return mode
}

// strategyOr is a policy's Strategy as Settings shows it: StrategyBalanced
// for the empty string, which is what an unset Strategy already means.
func strategyOr(strategy string) string {
	if strategy == "" {
		return agent.StrategyBalanced
	}
	return strategy
}

// describeWhen writes what a rule matches as a person reads it.
func describeWhen(w agent.RuleMatch) string {
	var parts []string
	if w.Task != "" {
		parts = append(parts, "the task matches /"+w.Task+"/")
	}
	if w.MinWords > 0 {
		parts = append(parts, fmt.Sprintf("it is at least %d words", w.MinWords))
	}
	if w.MaxWords > 0 {
		parts = append(parts, fmt.Sprintf("it is at most %d words", w.MaxWords))
	}
	if len(w.Files) > 0 {
		parts = append(parts, "it names a file like "+strings.Join(w.Files, " or "))
	}
	if w.Kind != "" {
		parts = append(parts, "it is a "+w.Kind)
	}
	if w.Agent != "" {
		parts = append(parts, "it runs on "+w.Agent)
	}
	if len(parts) == 0 {
		return "always"
	}
	return strings.Join(parts, ", and ")
}

// setRouting changes the routing mode or floor, for the active project or,
// with kind "all", for every project, and says what that now means.
func (s *Server) setRouting(c *controlClient, cmd command) {
	root, ok := ask(s, func() string { return s.ws.ActiveRoot() })
	if !ok {
		return
	}
	project, where := "", "every project"
	if cmd.Kind != "all" {
		if root == "" {
			c.notify("There is no project open to set routing for", true)
			return
		}
		project, where = root, filepath.Base(root)
	}
	go func() {
		defer s.surviveFor(c, "saving routing")
		path, err := agent.ConfigPath()
		if err == nil {
			defaultWrites.Lock()
			err = agent.SetRouting(filepath.Dir(path), project, cmd.Target, cmd.Text)
			defaultWrites.Unlock()
		}
		if err != nil {
			c.notify("Could not save routing: "+err.Error(), true)
			return
		}
		c.notify(routingNotice(where, cmd.Target, cmd.Text), false)
		s.refreshAgents()
	}()
}

// routingNotice says what a routing setting just saved means.
func routingNotice(where, field, value string) string {
	if field == "floor" {
		if value == "" {
			value = agent.TierSmall
		}
		return "Routing will choose nothing below " + value + " for " + where
	}
	if field == "strategy" {
		if value == agent.StrategyCost {
			return "Routing now minimises cost for " + where + ": work no rule recognises is routed to the cheapest available model too"
		}
		return "Routing is now cost-first but quality-aware for " + where + ": work no rule recognises is left where it was"
	}
	switch value {
	case "":
		return where + " is now routed as every project is"
	case agent.RoutingAuto:
		return "Routing now chooses models for " + where
	}
	return "Routing is off for " + where
}

// clearRoutingLog deletes the routing log, which is the user's to delete.
func (s *Server) clearRoutingLog(c *controlClient) {
	go func() {
		defer s.surviveFor(c, "clearing the routing history")
		dir, err := routingLogDir()
		if err == nil {
			err = route.ClearLog(dir)
		}
		if err != nil {
			c.notify("Could not clear the routing history: "+err.Error(), true)
			return
		}
		c.notify("Cleared the routing history", false)
	}()
}
