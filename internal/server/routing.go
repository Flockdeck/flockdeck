package server

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/pricing"
	"github.com/jmwri/flockdeck/internal/route"
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
	r := route.New(policy)
	if !r.On() {
		return nil, "", ""
	}
	spec, ok := c.Find(agentID)
	if !ok {
		return nil, policy.Mode, ""
	}
	note = routeNote(spec, model)
	routed := make([]*routeView, len(tasks))
	any := false
	for i, task := range tasks {
		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}
		d := r.Decide(route.Input{Kind: route.KindFanout, Task: task, Agent: spec, Current: model})
		if !d.Routed {
			continue
		}
		any = true
		routed[i] = &routeView{Model: d.Model, Tier: d.Tier, Rule: d.Rule, Up: d.Up,
			Reason: d.Reason + priceNote(spec, model, d.Model)}
	}
	if any {
		routes = routed
	}
	return routes, policy.Mode, note
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

// fanoutRouteLog is the log's account of a fan-out: each routed row that
// started, and each routed choice the user changed before starting.
func fanoutRouteLog(req fanoutRequest, project string, started map[*fanoutJob]string) []route.LogEntry {
	now := time.Now()
	var out []route.LogEntry
	for j, pane := range started {
		if j.routed == "" {
			continue
		}
		out = append(out, route.LogEntry{At: now, Kind: route.KindFanout, Project: project, Pane: pane,
			Agent: j.agent, Source: route.SourceRule, Rule: j.routed,
			Baseline: req.Model, Routed: j.model, Outcome: route.OutcomeKept})
	}
	for _, o := range req.Overrides {
		out = append(out, route.LogEntry{At: now, Kind: route.KindFanout, Project: project,
			Agent: o.Agent, Source: route.SourceRule, Rule: o.Rule,
			Baseline: req.Model, Routed: o.Routed, Outcome: route.OutcomeOverridden + o.Chosen})
	}
	return out
}

// paneRoute is what a pane's header says of its routing: the rule that chose
// its model, the model it would otherwise have run, and whether that moved it
// "down" or "up" -- empty where the tiers no longer say.
func paneRoute(c *agent.Catalog, p *workspace.Pane) (rule, from, dir string) {
	if p == nil || !p.IsAgent() || p.Routed == "" {
		return "", "", ""
	}
	rule, from = p.Routed, p.RoutedFrom
	if spec, ok := c.Find(p.Agent); ok {
		to, was := route.TierOf(spec, p.Model), route.TierOf(spec, from)
		switch {
		case to > 0 && was > 0 && to < was:
			dir = "down"
		case to > was && was > 0:
			dir = "up"
		}
	}
	return rule, from, dir
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
	// Config is where agents.json is, since the rules are edited there.
	Config string `json:"config,omitempty"`
}

type routingChoice struct {
	Mode  string `json:"mode"`
	Floor string `json:"floor"`
}

// ruleView is one rule as Settings lists it, in words.
type ruleView struct {
	Name   string `json:"name"`
	Choice string `json:"choice"`
	When   string `json:"when"`
}

func routingOf(c *agent.Catalog, root string) *routingView {
	v := &routingView{Every: routingChoice{Mode: modeOr(c.Routing.Mode), Floor: c.Routing.Floor}}
	policy, own := c.RoutingFor(root)
	if own && root != "" {
		v.Project = &routingChoice{Mode: modeOr(policy.Mode), Floor: policy.Floor}
	}
	v.BuiltIn = policy.Rules == nil
	for _, r := range route.RulesOf(policy) {
		choice := r.Tier
		if r.Model != "" {
			choice = r.Agent + " · " + r.Model
		}
		v.Rules = append(v.Rules, ruleView{Name: r.Name, Choice: choice, When: describeWhen(r.When)})
	}
	if route.New(policy).On() {
		if spec, model, ok := c.Resolve(root, "", ""); ok {
			v.Note = routeNote(spec, model)
		}
	}
	if path, err := agent.ConfigPath(); err == nil {
		v.Config = path
	}
	return v
}

func modeOr(mode string) string {
	if mode == "" {
		return agent.RoutingOff
	}
	return mode
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
