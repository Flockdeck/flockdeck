package server

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/pricing"
	"github.com/jmwri/flockdeck/internal/route"
	"github.com/jmwri/flockdeck/internal/routejev"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// Model routing, as far as the server takes part in it: working out a routed
// model for each row of a fan-out, for the dialog to pre-fill; recording what
// became of those choices in the routing log; and the settings.
//
// Nothing is routed here that the user does not see. The dialog is sent the
// routed models, shows them marked, and sends back the rows as it started
// them, exactly as if they had been chosen by hand; the server does not route
// the fan-out it is asked to start.

// routeView is what routing chose for one row, as the dialog shows it.
type routeView struct {
	Model  string `json:"model"`
	Tier   string `json:"tier"`
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
	Up     bool   `json:"up,omitempty"`
	// Agent is set only when the rule moved the row to another agent, not
	// only another model. Empty means the row stays on the run's own.
	Agent string `json:"agent,omitempty"`
}

// routesMsg answers routeTasks: the routes for the rows as the dialog now has
// them, on the agent and model it now has chosen for the run.
type routesMsg struct {
	Type      string       `json:"type"`
	Agent     string       `json:"agent"`
	Model     string       `json:"model"`
	Tasks     []string     `json:"tasks"`
	Routes    []*routeView `json:"routes"`
	Routing   string       `json:"routing,omitempty"`
	RouteNote string       `json:"routeNote,omitempty"`
}

// routeOverride is a routed choice the user changed in the dialog before
// starting it. It is sent only so the routing log can count it.
type routeOverride struct {
	// Task is the row's text, sent so that a fallback choice -- which has no
	// rule to name it -- can be matched to what routing said of it. It is
	// used to look that up in memory and is never written to the log.
	Task   string `json:"task"`
	Rule   string `json:"rule"`
	Agent  string `json:"agent"`
	Routed string `json:"routed"`
	Chosen string `json:"chosen"`
}

// routeRows decides a model for each task of a fan-out running on an agent
// and model, positional against the tasks and nil where a row is left alone.
//
// mode is the policy's mode, empty when it routes nothing, and note says why
// no row can be routed where that is down to the agent or the model the run
// is on rather than to the tasks.
func routeRows(c *agent.Catalog, root, agentID, model string, tasks []string) (routes []*routeView, mode, note string) {
	policy, _ := c.RoutingFor(root)
	// Jev is asked here and for a fan-out's rows only: they are what the user
	// sees and can change before anything starts, and what the routing log
	// scores. routejev.For is nil unless the policy turned Jev on and a key is
	// set, and then WithClassifier changes nothing.
	r := route.New(policy).WithClassifier(routejev.For(policy))
	if !r.On() {
		return nil, "", ""
	}
	spec, ok := c.Find(agentID)
	if !ok {
		return nil, policy.Mode, ""
	}
	note = routeNote(spec, model)
	var other func(string) (agent.Spec, bool)
	if policy.CrossAgent {
		other = otherAgentFor(c, root)
	}
	inputs := make([]route.Input, 0, len(tasks))
	for _, task := range tasks {
		if task = strings.TrimSpace(task); task != "" {
			inputs = append(inputs, route.Input{Kind: route.KindFanout, Task: task, Agent: spec, Current: model, OtherAgent: other})
		}
	}
	// Every row that would ask Jev asks it now, together, so the rows wait
	// for one answer and not one each; Decide then finds the answers.
	r.Prewarm(inputs)
	routed := make([]*routeView, len(tasks))
	any := false
	for i, task := range tasks {
		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}
		d := r.Decide(route.Input{Kind: route.KindFanout, Task: task, Agent: spec, Current: model, OtherAgent: other})
		if !d.Routed {
			continue
		}
		any = true
		if d.Source == route.SourceFallback {
			rememberFallback(task, agentID, d)
		}
		reason := d.Reason
		if d.Agent == "" {
			reason += priceNote(spec, model, d.Model)
		} else if dest, ok := c.Find(d.Agent); ok {
			reason += crossAgentNote(dest)
		}
		routed[i] = &routeView{Model: d.Model, Tier: d.Tier, Rule: d.Rule, Up: d.Up, Agent: d.Agent, Reason: reason}
	}
	if any {
		routes = routed
	}
	return routes, policy.Mode, note
}

// routeSpawnChoice decides a model, and maybe an agent, for a helper spawned
// with `flockdeck spawn` and given neither --agent nor --model. It answers
// only in "auto" mode: a spawned helper has no dialog for "suggest" to fill
// in, and a choice nobody can see or change would be worse than not routing
// at all. baseAgent and baseModel are what the helper would have run without
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

// otherAgentFor is the route.Input.OtherAgent a fan-out or a spawned helper
// may use: an agent besides the one the work would run on, but only when it
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
// agent.Available trusts its own probes for: long enough that a fan-out
// asking about a dozen rows dials an endpoint once, short enough that
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
// short because it runs in line with every routed decision a fan-out asks
// about.
var dialTimeout = 500 * time.Millisecond

// endpointReachable reports whether something answers at base's host and
// port, remembering the answer for reachTTL.
//
// agent.Available reports a local endpoint as available the moment its
// address is a loopback one, without asking whether anything is listening
// there -- which is right for the picker, where picking a stopped Ollama and
// finding out is the user's own choice, but wrong here: routing to it would
// cut a fan-out row's worktree, start its pane, and only then learn the pane
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

// priceNote compares what the routed model costs with what the run's would
// have, where both prices are known and are what the user pays -- the same
// rule the picker shows prices by (modelViews).
func priceNote(spec agent.Spec, from, to string) string {
	if spec.Runner != agent.RunnerAPI || spec.API.BaseURL != "" {
		return ""
	}
	today := time.Now()
	a, ok := pricing.Lookup(to, today)
	b, ok2 := pricing.Lookup(from, today)
	if !ok || !ok2 {
		return ""
	}
	checked := min(a.Checked, b.Checked)
	return fmt.Sprintf(". %s is %s / %s per M tokens, against %s / %s for %s (prices checked %s)",
		modelNameOf(spec, to), dollars(a.In), dollars(a.Out), dollars(b.In), dollars(b.Out), modelNameOf(spec, from), checked)
}

// crossAgentNote is what a row moved to another agent says about the cost of
// that, in place of priceNote's dollar figures: comparing a subscription
// CLI's tokens against a local model's makes no honest claim of a saving, so
// none is made. A local endpoint at least says plainly what it costs.
func crossAgentNote(dest agent.Spec) string {
	if agent.NeedsNoKey(dest) {
		return ". Runs on your own machine, no per-token cost"
	}
	return ""
}

// dollars writes a price per million tokens the way the providers do.
func dollars(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("$%d", int64(v))
	}
	return fmt.Sprintf("$%.2f", v)
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

// routeTasks answers the dialog's routeTasks: the rows as it now has them,
// routed for the agent and model it has chosen for the run. The rules run
// here and nowhere else, so the dialog never has a second copy of them to
// disagree with.
func (s *Server) routeTasks(c *controlClient, cmd command) {
	type facts struct{ root, def string }
	f, ok := ask(s, func() facts {
		_, def := s.ws.Agents()
		return facts{root: s.ws.ActiveRoot(), def: def}
	})
	if !ok {
		return
	}
	agentID := cmd.Agent
	if agentID == "" {
		agentID = f.def
	}
	routes, mode, note := routeRows(s.ws.Catalog(), f.root, agentID, cmd.Model, cmd.Tasks)
	c.sendJSON(routesMsg{Type: "routes", Agent: cmd.Agent, Model: cmd.Model, Tasks: cmd.Tasks,
		Routes: routes, Routing: mode, RouteNote: note})
}

// routingLogDir is where the routing log is kept: Flockdeck's state
// directory. The tests move it by moving the state directory itself, as they
// do for every other file kept there; nothing reassigns this.
var routingLogDir = store.Dir

// logRoutes adds a fan-out's routed rows to the routing log. The log is for the
// user's own numbers, and a failure to write it costs them one fan-out's worth
// of them, which is not worth interrupting the fan-out to say.
func logRoutes(entries []route.LogEntry) {
	if len(entries) == 0 {
		return
	}
	if dir, err := routingLogDir(); err == nil {
		_ = route.AppendLog(dir, entries...)
	}
}

// fallbacks is what routing chose for the rows of a fan-out dialog that no rule
// decided, kept for a moment in memory so the fan-out's log entries can say
// whether Jev shaped them. A fallback has no rule name for the dialog to send
// back, so the row's text finds it -- by hash, so the text is not kept -- and
// nothing here outlives the process or is ever written down.
var fallbacks = struct {
	sync.Mutex
	seen map[[sha256.Size]byte]fallbackNote
}{seen: map[[sha256.Size]byte]fallbackNote{}}

type fallbackNote struct {
	agent, model string
	jev          *route.JevNote
	at           time.Time
}

const (
	fallbackTTL = time.Hour
	fallbackCap = 512
)

func rememberFallback(task, agentID string, d route.Decision) {
	key := sha256.Sum256([]byte(strings.TrimSpace(task)))
	now := time.Now()
	fallbacks.Lock()
	defer fallbacks.Unlock()
	if len(fallbacks.seen) >= fallbackCap {
		for k, v := range fallbacks.seen {
			if now.Sub(v.at) >= fallbackTTL {
				delete(fallbacks.seen, k)
			}
		}
		if len(fallbacks.seen) >= fallbackCap {
			clear(fallbacks.seen)
		}
	}
	fallbacks.seen[key] = fallbackNote{agent: agentID, model: d.Model, jev: d.Jev, at: now}
}

// fallbackFor is what routing chose for a task, when it was a fallback onto
// model for agentID -- so that a row the user moved elsewhere, or handed to
// a rule, is not counted as the fallback's.
func fallbackFor(task, agentID, model string) (fallbackNote, bool) {
	key := sha256.Sum256([]byte(strings.TrimSpace(task)))
	fallbacks.Lock()
	defer fallbacks.Unlock()
	n, ok := fallbacks.seen[key]
	if !ok || time.Since(n.at) >= fallbackTTL || (agentID != "" && n.agent != agentID) || n.model != model {
		return fallbackNote{}, false
	}
	return n, true
}

// fanoutRouteLog is the log's account of a fan-out: each routed row that
// started, and each routed choice the user changed before starting. A row a
// rule chose is named by the rule the dialog sent back; one that started on a
// cost strategy's fallback is found by its text (see fallbacks) and logged
// with the source "fallback", carrying Jev's classification where Jev shaped
// it.
func fanoutRouteLog(req fanoutRequest, project string, started map[*fanoutJob]string) []route.LogEntry {
	now := time.Now()
	var out []route.LogEntry
	for j, pane := range started {
		if j.routed == "" {
			if n, ok := fallbackFor(j.task, j.agent, j.model); ok {
				out = append(out, route.LogEntry{At: now, Kind: route.KindFanout, Project: project, Pane: pane,
					Agent: j.agent, Source: route.SourceFallback, Baseline: req.Model, Routed: j.model,
					Outcome: route.OutcomeKept, Jev: n.jev})
			}
			continue
		}
		e := route.LogEntry{At: now, Kind: route.KindFanout, Project: project, Pane: pane,
			Agent: j.agent, Source: route.SourceRule, Rule: j.routed,
			Baseline: req.Model, Routed: j.model, Outcome: route.OutcomeKept}
		if j.agent != req.Agent {
			e.BaselineAgent = req.Agent
		}
		out = append(out, e)
	}
	for _, o := range req.Overrides {
		e := route.LogEntry{At: now, Kind: route.KindFanout, Project: project,
			Agent: o.Agent, Source: route.SourceRule, Rule: o.Rule,
			Baseline: req.Model, Routed: o.Routed, Outcome: route.OutcomeOverridden + o.Chosen}
		if o.Rule == "" {
			// A fallback's choice, changed: with no record of one, it was
			// logged as a rule decision with no rule, which nothing counts.
			n, ok := fallbackFor(o.Task, o.Agent, o.Routed)
			if !ok {
				continue
			}
			e.Source, e.Jev = route.SourceFallback, n.jev
		}
		if o.Agent != req.Agent {
			e.BaselineAgent = req.Agent
		}
		out = append(out, e)
	}
	return out
}

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

// routingView is the routing part of Settings › Agents: the mode and floor for
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
	// Jev is the policy's switch for asking Jev to rate work no rule matched,
	// and JevKey whether TYPESAFE_API_KEY is set, since it does nothing
	// without one. Fallback is the log's account of the fallback's decisions,
	// with and without Jev, for comparison.
	Jev      bool           `json:"jev,omitempty"`
	JevKey   bool           `json:"jevKey,omitempty"`
	Fallback []fallbackView `json:"fallback,omitempty"`
	// Config is where agents.json is, since the rules are edited there.
	Config string `json:"config,omitempty"`
}

type routingChoice struct {
	Mode     string `json:"mode"`
	Floor    string `json:"floor"`
	Strategy string `json:"strategy"`
}

// fallbackView is one group of the fallback's decisions as Settings lists it,
// with how often what was chosen was kept or overridden.
type fallbackView struct {
	Name       string `json:"name"`
	Kept       int    `json:"kept"`
	Overridden int    `json:"overridden"`
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
	v.Jev = policy.Jev
	v.JevKey = routejev.Enabled()
	stats := map[string]route.RuleStat{}
	if dir, err := routingLogDir(); err == nil {
		if entries, err := route.ReadLog(dir); err == nil {
			for _, s := range route.Stats(entries) {
				stats[s.Rule] = s
			}
			for _, s := range route.FallbackStats(entries) {
				v.Fallback = append(v.Fallback, fallbackView{Name: s.Rule, Kept: s.Kept, Overridden: s.Overridden})
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
			c.notify("there is no project open to set routing for", true)
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
			c.notify("could not save routing: "+err.Error(), true)
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
		return "routing will choose nothing below " + value + " for " + where
	}
	if field == "jev" {
		if value == "true" {
			return "routing may now send the text of a fan-out row no rule matched to TypeSafe, for " + where + " -- it also needs TYPESAFE_API_KEY, and only applies to Minimise cost"
		}
		return "routing no longer sends anything to TypeSafe for " + where
	}
	if field == "strategy" {
		if value == agent.StrategyCost {
			return "routing now minimises cost for " + where + ": work no rule recognises is routed to the cheapest available model too"
		}
		return "routing is now cost-first but quality-aware for " + where + ": work no rule recognises is left where it was"
	}
	switch value {
	case "":
		return where + " is now routed as every project is"
	case agent.RoutingSuggest:
		return "routing now suggests models for " + where + ": a fan-out's rows come pre-set, to change before they start"
	case agent.RoutingAuto:
		return "routing now chooses models for " + where
	}
	return "routing is off for " + where
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
			c.notify("could not clear the routing history: "+err.Error(), true)
			return
		}
		c.notify("cleared the routing history", false)
	}()
}
