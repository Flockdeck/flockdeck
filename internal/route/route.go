// Package route decides which model a piece of work runs on, from rules the
// user can read and edit in agents.json.
//
// Deciding is pure: it reads no file and makes no request. It is given the
// policy, the task, and the agent and model the work would run on without it,
// and answers with a model and a sentence saying why. The caller shows that
// before the work starts -- a fan-out row's select is pre-set to it, and the
// user confirms -- so a choice shown is the choice run. The one thing here
// that touches the disk is the routing log (log.go), which holds rule names
// and model ids and never the task.
package route

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/pricing"
)

// The kinds of work a rule's "kind" can name.
const (
	KindFanout = "fanout"
	KindSpawn  = "spawn"
	KindTurn   = "turn"
)

// SourceRule is the Source of a decision a rule made.
const SourceRule = "rule"

// SourceFallback is the Source of a decision made not by a rule but by
// Strategy == agent.StrategyCost, when no rule matched: see Router.fallback.
const SourceFallback = "fallback"

// Input is one piece of work to decide a model for.
type Input struct {
	Kind string // KindFanout, KindSpawn or KindTurn
	Task string // the row, the spawn task, or the prompt
	// Agent is the agent the work would run on without routing. A rule may
	// still move it to another agent -- see OtherAgent -- but routing never
	// does that on its own: another agent is another login, other tools and
	// another transcript, which is far more than a model.
	Agent agent.Spec
	// Current is the model the work would get without routing.
	Current string
	// NoDowngrade says the work may only be moved to a stronger model.
	NoDowngrade bool
	// OtherAgent resolves an agent id to its Spec, but only when work could
	// be handed to it here and now: installed or keyed, trusted for the
	// project, and, for an endpoint of its own, answering. It is the
	// caller's to supply, because deciding stays pure and does no I/O of its
	// own. Nil means work is never moved to another agent, whatever a rule
	// asks for -- which is also true for any Kind other than KindFanout and
	// KindSpawn, and for a policy whose CrossAgent is off: a running
	// conversation cannot change agent under it, and a switch nobody turned
	// on for this project is not one routing takes on its own.
	OtherAgent func(id string) (agent.Spec, bool)
}

// Decision is what routing made of one piece of work.
type Decision struct {
	// Model is the model to run, and Tier its tier; both are empty, and
	// Routed false, when the work is left alone.
	Model  string
	Tier   string
	Routed bool
	// Up says the work was moved to a stronger model than it would have had.
	Up bool
	// Agent is the agent the work moves to, and empty when it stays on the
	// one it was going to run on. Set only by a rule naming a model of
	// another agent, and only when Input.OtherAgent allowed it.
	Agent string
	// Source is what chose (SourceRule), and Rule the rule's name.
	Source string
	Rule   string
	// Reason is one line a person reads: why the model was chosen, or why a
	// rule that matched changed nothing.
	Reason string
	// Jev is the classification a fallback was shaped by, and nil for every
	// decision that was not: a rule's, and a fallback made without Jev --
	// which includes every one where Jev could not be asked or its answer was
	// not acted on, deliberately indistinguishable from a plain fallback.
	Jev *JevNote
}

// BuiltinRules are the rules a policy that names none of its own applies.
//
// They are deliberately timid. They move clearly mechanical work down and
// clearly hard work up, and leave everything else exactly where it would have
// been: a rule set that routes a third of a typical fan-out is doing well, and
// one that routes everything is a bug. The rules for hard work come first,
// because the first rule to match wins, and a task that reads as both -- "move
// the session store behind a lock to fix the race" -- is the one a cheap
// attempt would cost most to get wrong.
func BuiltinRules() []agent.RoutingRule {
	return []agent.RoutingRule{
		{Name: "hard work", Tier: agent.TierTop,
			When: agent.RuleMatch{Task: `\b(refactor|redesign|architect|concurren|race|deadlock|migration|security)`}},
		{Name: "schema changes", Tier: agent.TierTop,
			When: agent.RuleMatch{Files: []string{"**/migrations/**", "**/*.sql"}}},
		{Name: "run the tests", Tier: agent.TierSmall,
			When: agent.RuleMatch{Task: `^(re-?)?run (the |all )?(unit |integration )?tests?\b`}},
		{Name: "rename or move", Tier: agent.TierSmall,
			When: agent.RuleMatch{Task: `\b(rename|move)\b`, MaxWords: 25}},
		{Name: "changelog and docs wording", Tier: agent.TierSmall,
			When: agent.RuleMatch{Task: `\b(changelog|typo|spelling|wording|readme)\b`, MaxWords: 40}},
	}
}

// RulesOf is the rules a policy applies: its own, or the built-in ones when it
// names none. An empty list is a policy that deliberately has no rules.
func RulesOf(p agent.RoutingPolicy) []agent.RoutingRule {
	if p.Rules == nil {
		return BuiltinRules()
	}
	return p.Rules
}

// Router is a policy made ready to decide with: its rules compiled once, for a
// fan-out that asks about a dozen rows at a time.
type Router struct {
	mode       string
	strategy   string
	floor      int
	crossAgent bool
	rules      []rule
	// jev and classifier are Jev-assisted fallback: see jev.go.
	jev        bool
	classifier Classifier
}

type rule struct {
	agent.RoutingRule
	task *regexp.Regexp
}

// New makes a Router of a policy. A rule that cannot be used -- a pattern that
// does not compile, a tier that is not one -- is left out; the catalog has
// already named it in its notice when agents.json was read.
func New(p agent.RoutingPolicy) *Router {
	r := &Router{mode: p.Mode, strategy: p.Strategy, floor: max(agent.TierRank(p.Floor), 1), crossAgent: p.CrossAgent, jev: p.Jev}
	if p.Floor != "" && agent.TierRank(p.Floor) == 0 {
		// A floor nobody can read is taken at its most cautious.
		r.floor = agent.TierRank(agent.TierTop)
	}
	for _, rr := range RulesOf(p) {
		ru := rule{RoutingRule: rr}
		if rr.When.Task != "" {
			re, err := regexp.Compile("(?i)" + rr.When.Task)
			if err != nil {
				continue
			}
			ru.task = re
		}
		if rr.Model == "" && agent.TierRank(rr.Tier) == 0 {
			continue
		}
		r.rules = append(r.rules, ru)
	}
	return r
}

// On reports whether the policy routes anything at all.
func (r *Router) On() bool {
	return r.mode == agent.RoutingSuggest || r.mode == agent.RoutingAuto
}

// Decide is New(p).Decide(in), for one piece of work.
func Decide(p agent.RoutingPolicy, in Input) Decision {
	return New(p).Decide(in)
}

// Decide applies the rules to one piece of work, in order: the first that
// matches decides, and no match leaves the work exactly as it was -- unless
// the policy's Strategy is agent.StrategyCost, which routes unmatched work
// too (see fallback). Then the floor, "never downgrade" and what the agent
// offers bound what that rule, or the fallback, may choose.
func (r *Router) Decide(in Input) Decision {
	if !r.On() {
		return Decision{}
	}
	words := len(strings.Fields(in.Task))
	files := filesIn(in.Task)
	// Cross-agent matching needs all three: the policy's own switch, a fresh
	// pane to put it in rather than one already running or a chat turn, and
	// a caller that can actually say whether another agent is ready. Without
	// any one of them a rule naming another agent's model is judged exactly
	// as it always was: about that agent's own work, and nobody else's.
	crossOK := r.crossAgent && in.OtherAgent != nil && (in.Kind == KindFanout || in.Kind == KindSpawn)
	for _, ru := range r.rules {
		if ru.matches(in, words, files, crossOK) {
			return r.apply(ru, in)
		}
	}
	if r.strategy == agent.StrategyCost {
		return r.fallback(in)
	}
	return Decision{}
}

// fallback is what Decide does when Strategy is agent.StrategyCost and no
// rule matched: rather than leave the work exactly as it was, it moves it to
// the agent's own cheapest model at the floor -- the lowest tier routing may
// ever choose here, reusing pick() the same way a rule naming a tier does.
// It never crosses agents: a rule is the only thing that does that, chosen by
// hand, and cost-minimisation of unclassified work does not get to guess at
// it. Floor and "never downgrade" bind it exactly as they bind a rule.
func (r *Router) fallback(in Input) Decision {
	cur := tierOf(in.Agent, in.Current)
	if cur == 0 {
		// As with a rule: a model of no known size might already be the
		// smallest there is, so it is never routed from.
		return Decision{Source: SourceFallback, Reason: modelName(in.Agent, in.Current) + " is a model of no known size, so routing leaves it alone"}
	}
	// want is the tier asked for: the floor, as it always was, or where Jev
	// answered, what it asked for, but never above the work's own model (or the
	// floor, if that is higher). Jev moves the choice between those two and no
	// further, so cost-minimisation still never spends more than the work
	// would have without routing, and never goes below the floor.
	want := r.floor
	var note *JevNote
	var jd Difficulty
	asked := 0
	if r.wantsJev(in) {
		if d, err := r.classifier.Classify(in.Task); err == nil {
			if rank, ok := tierFor(d); ok {
				asked, jd = rank, d
				want = min(max(rank, r.floor), max(cur, r.floor))
				note = &JevNote{Score: d.Score, Confidence: d.Confidence, Mechanical: d.Mechanical,
					MultiFile: d.MultiFile, Tier: tierName(rank), Model: d.Model}
			}
		}
	}
	m, ok := pick(in.Agent, want, in.Current)
	for !ok && want > r.floor {
		// The agent has no model in the tier asked for: the next one down.
		want--
		m, ok = pick(in.Agent, want, in.Current)
	}
	if !ok {
		return Decision{Source: SourceFallback, Reason: in.Agent.Name + " has no " + tierName(r.floor) + " model, so cost-minimisation leaves the work alone"}
	}
	t := agent.TierRank(m.Tier)
	switch {
	case m.ID == in.Current:
		return Decision{Source: SourceFallback, Reason: "no rule matched; the work is already on " + modelName(in.Agent, in.Current) + ", the cheapest tier cost-minimisation would choose"}
	case in.NoDowngrade && t < cur:
		return Decision{Source: SourceFallback, Reason: "no rule matched, and this work may not be moved to a smaller model"}
	}
	reason := fmt.Sprintf("no rule matched; cost-minimisation routes unclassified work to the cheapest available tier anyway → %s", m.Tier)
	if note != nil {
		reason = jevReason(jd, asked, t)
	}
	return Decision{Model: m.ID, Tier: m.Tier, Routed: true, Up: t > cur, Source: SourceFallback, Reason: reason, Jev: note}
}

func (ru rule) matches(in Input, words int, files []string, crossOK bool) bool {
	w := ru.When
	switch {
	// A rule for another agent's model is not about this work, so it does
	// not match it, unless cross-agent routing is allowed here -- in which
	// case naming that agent is exactly what makes the rule about this work.
	// Taken as a match that changed nothing, it was the first match, and
	// every rule after it went untried: a Codex rule placed above the rest
	// left every Claude row alone.
	case ru.Model != "" && ru.Agent != in.Agent.ID && !crossOK,
		w.Kind != "" && w.Kind != in.Kind,
		w.Agent != "" && w.Agent != in.Agent.ID,
		w.MinWords > 0 && words < w.MinWords,
		w.MaxWords > 0 && words > w.MaxWords,
		ru.task != nil && !ru.task.MatchString(in.Task),
		len(w.Files) > 0 && !anyFileMatches(w.Files, files):
		return false
	}
	return true
}

// apply is what a rule that matched makes of the work.
func (r *Router) apply(ru rule, in Input) Decision {
	named := "rule '" + ru.Name + "'"
	left := func(why string) Decision {
		return Decision{Source: SourceRule, Rule: ru.Name, Reason: named + " matched, but " + why}
	}
	cur := tierOf(in.Agent, in.Current)
	if cur == 0 {
		// A model whose size is not known -- Claude Code's Default, which is
		// whatever the CLI is set to -- might already be the smallest there
		// is, or the largest. It is never routed from.
		return left(modelName(in.Agent, in.Current) + " is a model of no known size, so routing leaves it alone")
	}

	// A rule whose Agent differs from the work's own reaches this point only
	// when Decide judged cross-agent routing allowed here (matches, above),
	// so what remains is asking the caller whether that agent can actually
	// take the work right now.
	dest, crossing := in.Agent, false
	if ru.Model != "" && ru.Agent != in.Agent.ID {
		other, ok := in.OtherAgent(ru.Agent)
		if !ok {
			return left(ru.Agent + " is not ready to take work right now")
		}
		dest, crossing = other, true
	}

	var target agent.Model
	raised := false
	if ru.Model != "" {
		m, ok := findModel(dest, ru.Model)
		switch {
		case !ok:
			return left(dest.Name + " does not offer " + ru.Model)
		case agent.TierRank(m.Tier) == 0:
			return left(modelName(dest, m.ID) + " is a model of no known size, and nothing is routed to one")
		case agent.TierRank(m.Tier) < r.floor:
			return left(modelName(dest, m.ID) + " is below the lowest tier routing may choose here")
		}
		target = m
	} else {
		want := agent.TierRank(ru.Tier)
		if want < r.floor {
			want, raised = r.floor, true
		}
		m, ok := pick(in.Agent, want, in.Current)
		if !ok {
			return left(in.Agent.Name + " has no " + tierName(want) + " model")
		}
		target = m
	}

	t := agent.TierRank(target.Tier)
	switch {
	case !crossing && target.ID == in.Current:
		return left("the work is already on " + modelName(in.Agent, in.Current))
	case in.NoDowngrade && t < cur:
		return left("this work may not be moved to a smaller model")
	}
	reason := fmt.Sprintf("%s → %s", named, target.Tier)
	if crossing {
		reason = fmt.Sprintf("%s → %s · %s", named, dest.Name, modelName(dest, target.ID))
	}
	if raised {
		reason += fmt.Sprintf(" (%s, raised to the lowest tier routing may choose here)", ru.Tier)
	}
	d := Decision{
		Model: target.ID, Tier: target.Tier, Routed: true, Up: t > cur,
		Source: SourceRule, Rule: ru.Name, Reason: reason,
	}
	if crossing {
		d.Agent = dest.ID
	}
	return d
}

// pick is the agent's model for a tier: the current one where it is already in
// that tier, and otherwise the cheapest of those that are, where every one of
// them has a price, and the first listed where they do not.
func pick(spec agent.Spec, rank int, current string) (agent.Model, bool) {
	var in []agent.Model
	for _, m := range spec.Models {
		if agent.TierRank(m.Tier) != rank {
			continue
		}
		if m.ID == current {
			return m, true
		}
		in = append(in, m)
	}
	if len(in) == 0 {
		return agent.Model{}, false
	}
	best, bestCost := in[0], -1.0
	today := time.Now()
	for _, m := range in {
		r, ok := pricing.Lookup(m.ID, today)
		if !ok {
			return in[0], true
		}
		if c := r.In + r.Out; bestCost < 0 || c < bestCost {
			best, bestCost = m, c
		}
	}
	return best, true
}

// TierOf is the rank of a model among the agent's own, and 0 where the agent
// does not list it or gives it no tier.
func TierOf(spec agent.Spec, model string) int { return tierOf(spec, model) }

func tierOf(spec agent.Spec, model string) int {
	if m, ok := findModel(spec, model); ok {
		return agent.TierRank(m.Tier)
	}
	return 0
}

func findModel(spec agent.Spec, id string) (agent.Model, bool) {
	for _, m := range spec.Models {
		if m.ID == id {
			return m, true
		}
	}
	return agent.Model{}, false
}

// modelName is a model as a person reads it: its name where the catalog gives
// one, and "Default" for the empty model.
func modelName(spec agent.Spec, id string) string {
	if m, ok := findModel(spec, id); ok && m.Name != "" {
		return m.Name
	}
	if id == "" {
		return "Default"
	}
	return id
}

func tierName(rank int) string {
	switch rank {
	case 1:
		return agent.TierSmall
	case 2:
		return agent.TierMid
	}
	return agent.TierTop
}
