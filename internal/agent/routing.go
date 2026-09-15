package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// The routing modes.
const (
	// RoutingOff routes nothing and shows nothing. It is what a file that
	// says nothing about routing means.
	RoutingOff = "off"
	// RoutingSuggest pre-fills a routed choice and marks it, for the user to
	// confirm or change before anything starts.
	RoutingSuggest = "suggest"
	// RoutingAuto applies a routed choice and shows it, to be overridden after
	// the fact.
	RoutingAuto = "auto"
)

// The routing strategies. Strategy is orthogonal to Mode: Mode says whether a
// decision is shown, applied or ignored; Strategy says how aggressively it
// favors cost.
const (
	// StrategyBalanced is cost-first-but-quality-aware: rules move clearly
	// mechanical work down and clearly hard work up, and work no rule
	// recognises is left exactly where it was. It is what an empty Strategy
	// means, so a file from before this build reads as it always has.
	StrategyBalanced = "balanced"
	// StrategyCost is pure cost-minimisation: work no rule recognises is
	// still routed, to the cheapest model the floor allows, rather than left
	// alone. A rule that matches, and Floor, both still decide exactly as
	// they do under StrategyBalanced -- this only changes what happens when
	// nothing matches.
	StrategyCost = "cost"
)

// RoutingPolicy says whether, and how, Flockdeck chooses the model a piece of
// work runs on.
//
// It lives in agents.json and nowhere else: the top-level "routing" for every
// project, and a project entry's own "routing" for that project, which
// replaces the other whole so that what a project says is all there is to read
// about it. It is never read from a file inside a repository, because a cloned
// repository must not be able to decide what the user's key spends.
type RoutingPolicy struct {
	Mode string `json:"mode,omitempty"`
	// Floor is the lowest tier routing may choose; empty is small.
	Floor string `json:"floor,omitempty"`
	// Strategy is StrategyBalanced or StrategyCost; empty is StrategyBalanced,
	// so a file from before this build means what it always has.
	Strategy string `json:"strategy,omitempty"`
	// CrossAgent lets a rule that names another agent's model (Model and
	// Agent, below) move work to that agent, not only to that model. It is
	// its own switch, off by default, because writing such a rule is not by
	// itself the deliberate choice this is meant to be gated on: a rule pack
	// pasted from the help page, or a rule kept from before this build,
	// would otherwise start spending through a different login the moment
	// it was read. Cross-agent moves are further limited to a fan-out row or
	// a spawned helper -- never a chat turn or a pane already running -- and
	// only to an agent the caller reports as usable right now.
	CrossAgent bool `json:"crossAgent,omitempty"`
	// Rules are tried in order and the first that matches decides. Absent
	// means the built-in rules; an empty list means none at all.
	Rules []RoutingRule `json:"rules,omitempty"`
}

// RoutingRule is one rule: when a piece of work matches, it asks for a tier,
// or for one agent's model.
type RoutingRule struct {
	Name string `json:"name"`
	Tier string `json:"tier,omitempty"`
	// Model and Agent name one agent's model, for a rule that wants exactly
	// that rather than a tier.
	Model string `json:"model,omitempty"`
	Agent string `json:"agent,omitempty"`
	// SuggestInPane lets the rule suggest a switch in a pane already running.
	// Nothing reads it yet; it is kept so a file written for a later build
	// means the same here.
	SuggestInPane bool      `json:"suggestInPane,omitempty"`
	When          RuleMatch `json:"when"`
}

// RuleMatch is what a piece of work must be for a rule to match. Every field
// given must hold.
type RuleMatch struct {
	// Task is a regular expression, matched without regard to case, against
	// the task or prompt.
	Task     string `json:"task,omitempty"`
	MinWords int    `json:"minWords,omitempty"`
	MaxWords int    `json:"maxWords,omitempty"`
	// Files are globs, matched against the paths the task names.
	Files []string `json:"files,omitempty"`
	// Kind is "fanout", "spawn" or "turn".
	Kind  string `json:"kind,omitempty"`
	Agent string `json:"agent,omitempty"`
}

// parseRouting reads a policy as agents.json holds it, and puts right or names
// whatever in it cannot be used. where says whose policy it is, for the
// notice. None of it refuses the file: a rule that cannot be used is skipped
// and the rest apply.
func parseRouting(raw json.RawMessage, where string) (RoutingPolicy, []string) {
	var p RoutingPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return RoutingPolicy{Mode: RoutingOff}, []string{fmt.Sprintf("%s could not be read (%v), so routing is off there", where, explainValue(err))}
	}
	return p, checkRouting(&p, where)
}

func checkRouting(p *RoutingPolicy, where string) []string {
	var problems []string
	p.Mode = strings.ToLower(strings.TrimSpace(p.Mode))
	switch p.Mode {
	case "", RoutingOff, RoutingSuggest, RoutingAuto:
	default:
		problems = append(problems, fmt.Sprintf("%s: mode %q is not off, suggest or auto, so routing is off there", where, p.Mode))
		p.Mode = RoutingOff
	}
	p.Floor = strings.ToLower(strings.TrimSpace(p.Floor))
	if p.Floor != "" && TierRank(p.Floor) == 0 {
		// The cautious reading of a floor nobody can read is the highest.
		problems = append(problems, fmt.Sprintf("%s: floor %q is not small, mid or top, so nothing there is moved below top", where, p.Floor))
		p.Floor = TierTop
	}
	p.Strategy = strings.ToLower(strings.TrimSpace(p.Strategy))
	switch p.Strategy {
	case "", StrategyBalanced, StrategyCost:
	default:
		// The cautious reading of a strategy nobody can read is the one that
		// never routes work it was not asked to, the same reasoning "off" is
		// mode's cautious default.
		problems = append(problems, fmt.Sprintf("%s: strategy %q is not balanced or cost, so routing stays quality-aware there", where, p.Strategy))
		p.Strategy = StrategyBalanced
	}
	if p.Rules == nil {
		return problems
	}
	kept := make([]RoutingRule, 0, len(p.Rules))
	for i, r := range p.Rules {
		if strings.TrimSpace(r.Name) == "" {
			r.Name = fmt.Sprintf("rule %d", i+1)
		}
		if why := ruleProblem(&r); why != "" {
			problems = append(problems, fmt.Sprintf("%s: rule %q %s, so it is skipped", where, r.Name, why))
			continue
		}
		kept = append(kept, r)
	}
	p.Rules = kept
	return problems
}

// ruleProblem says what is wrong with a rule, or "" when nothing is.
func ruleProblem(r *RoutingRule) string {
	r.Tier = strings.ToLower(strings.TrimSpace(r.Tier))
	switch {
	case r.Tier == "" && r.Model == "":
		return "names neither a tier nor a model"
	case r.Tier != "" && r.Model != "":
		return "names both a tier and a model, and should name one"
	case r.Tier != "" && TierRank(r.Tier) == 0:
		return fmt.Sprintf("asks for tier %q; the tiers are small, mid and top", r.Tier)
	case r.Model != "" && r.Agent == "":
		return fmt.Sprintf("names the model %q but not the agent it belongs to", r.Model)
	}
	if r.When.Task != "" {
		if _, err := regexp.Compile("(?i)" + r.When.Task); err != nil {
			return fmt.Sprintf("has a task pattern that is not a regular expression (%v)", err)
		}
	}
	for _, g := range r.When.Files {
		for _, part := range strings.Split(g, "/") {
			if _, err := path.Match(part, ""); part != "**" && err != nil {
				return fmt.Sprintf("has a file pattern %q that is not a glob", g)
			}
		}
	}
	// Case is forgiven, as it is for a tier: "Fanout" skipped the whole rule.
	r.When.Kind = strings.ToLower(strings.TrimSpace(r.When.Kind))
	switch r.When.Kind {
	case "", "fanout", "spawn", "turn":
	default:
		return fmt.Sprintf("matches kind %q; the kinds are fanout, spawn and turn", r.When.Kind)
	}
	return ""
}

// parseAllRouting reads the installation's policy and every project's.
func (c *Catalog) parseAllRouting(f *File) []string {
	var problems []string
	if len(bytes.TrimSpace(f.Routing)) > 0 {
		var more []string
		c.Routing, more = parseRouting(f.Routing, "routing")
		problems = append(problems, more...)
		problems = append(problems, c.unknownAgents(c.Routing, "routing")...)
	}
	projects := make([]string, 0, len(f.Projects))
	for p, d := range f.Projects {
		if d.Routing != nil {
			projects = append(projects, p)
		}
	}
	sort.Strings(projects)
	for _, p := range projects {
		policy, more := parseRouting(*f.Projects[p].Routing, "routing for "+p)
		problems = append(problems, more...)
		problems = append(problems, c.unknownAgents(policy, "routing for "+p)...)
		if c.ProjectRouting == nil {
			c.ProjectRouting = map[string]RoutingPolicy{}
		}
		c.ProjectRouting[p] = policy
	}
	return problems
}

// unknownAgents names each rule of a policy that names an agent the catalog
// does not have.
//
// Ids are matched exactly, so such a rule -- a hand-typed "Claude", an agent
// since taken out of agents.json -- never matches anything, and without a word
// that looks like routing choosing to leave the work alone. The rule is kept:
// it is harmless, and the agent may yet be added.
func (c *Catalog) unknownAgents(p RoutingPolicy, where string) []string {
	var problems []string
	for _, r := range p.Rules {
		for _, id := range []string{r.Agent, r.When.Agent} {
			if id == "" {
				continue
			}
			if _, ok := c.Find(id); !ok {
				problems = append(problems, fmt.Sprintf("%s: rule %q names the agent %q, which is not one of the agents here, so it never matches", where, r.Name, id))
				break
			}
		}
	}
	return problems
}

// RoutingFor is the policy work in a project is routed by, and whether it is
// the project's own rather than the one every project shares.
func (c *Catalog) RoutingFor(project string) (RoutingPolicy, bool) {
	if project != "" {
		key := projectKey(project)
		for recorded, p := range c.ProjectRouting {
			if projectKey(recorded) == key {
				return p, true
			}
		}
	}
	return c.Routing, false
}

// SetRouting changes one setting of a routing policy in agents.json: "mode",
// "floor", "strategy" or "crossAgent", for one project where project names
// one and for every project otherwise. Everything else the policy says -- its
// rules, and
// any key a later build wrote -- is kept as it was written.
//
// A project given a policy of its own starts from a copy of every project's,
// since a project's policy replaces that one whole: turning routing on for one
// project must not quietly drop the rules and the floor every project has. A
// mode of "" for a project takes its own policy away, so that it routes as
// every project does.
func SetRouting(dir, project, field, value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	switch field {
	case "mode":
		if value == "" && project == "" || value != "" && value != RoutingOff && value != RoutingSuggest && value != RoutingAuto {
			return fmt.Errorf("%q is not a routing mode; the modes are off, suggest and auto", value)
		}
	case "floor":
		if value != "" && TierRank(value) == 0 {
			return fmt.Errorf("%q is not a tier; the tiers are small, mid and top", value)
		}
	case "strategy":
		if value != "" && value != StrategyBalanced && value != StrategyCost {
			return fmt.Errorf("%q is not a routing strategy; the strategies are balanced and cost", value)
		}
	case "crossAgent":
		if value != "" && value != "true" && value != "false" {
			return fmt.Errorf("%q is not true or false", value)
		}
	default:
		return fmt.Errorf("routing has no setting called %q", field)
	}
	configMu.Lock()
	defer configMu.Unlock()
	f, err := readConfig(dir)
	if err != nil {
		// As with a default: a file that cannot be read is not written over.
		return err
	}
	if project == "" {
		if f.Routing, err = setRoutingField(f.Routing, field, value); err != nil {
			return err
		}
		return writeConfig(dir, f)
	}

	name, entry := project, Defaults{}
	key := projectKey(project)
	for recorded, d := range f.Projects {
		if projectKey(recorded) == key {
			name, entry = recorded, d
			break
		}
	}
	if field == "mode" && value == "" {
		if entry.Routing == nil {
			return nil
		}
		entry.Routing = nil
		if entry == (Defaults{}) {
			delete(f.Projects, name)
		} else {
			f.Projects[name] = entry
		}
		return writeConfig(dir, f)
	}
	from := f.Routing
	if entry.Routing != nil {
		from = *entry.Routing
	}
	raw, err := setRoutingField(from, field, value)
	if err != nil {
		return err
	}
	entry.Routing = &raw
	if f.Projects == nil {
		f.Projects = map[string]Defaults{}
	}
	f.Projects[name] = entry
	return writeConfig(dir, f)
}

// setRoutingField sets one key of a policy as written, leaving the rest of it
// byte for byte. An empty floor is taken out, since it means small anyway;
// "crossAgent" set to "false", and "strategy" set to "balanced", are taken
// out the same way, since that is what their absence already means.
func setRoutingField(raw json.RawMessage, field, value string) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if t := bytes.TrimSpace(raw); len(t) > 0 && !bytes.Equal(t, []byte("null")) {
		if err := json.Unmarshal(t, &obj); err != nil {
			return nil, fmt.Errorf(`"routing" in %s is not an object, so it was not changed`, ConfigName)
		}
	}
	if value == "" || (field == "crossAgent" && value == "false") || (field == "strategy" && value == StrategyBalanced) {
		delete(obj, field)
	} else {
		var toWrite any = value
		if field == "crossAgent" {
			toWrite = value == "true"
		}
		v, err := marshalUnescaped(toWrite)
		if err != nil {
			return nil, err
		}
		obj[field] = v
	}
	return marshalUnescaped(obj)
}
