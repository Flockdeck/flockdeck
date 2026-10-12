package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Worktree policies say what checkout a role works in. They are guidance the
// briefing gives the agent and the role list shows the user; Flockdeck does not
// yet cut or lock a checkout because of one.
const (
	// WorktreeOwn is a git worktree and branch of the role's own.
	WorktreeOwn = "own"
	// WorktreeReadOnly is a checkout of a branch the role reads and does not
	// change or commit to.
	WorktreeReadOnly = "readonly"
	// WorktreeMain is the project's main checkout, shared with whoever else is
	// working there.
	WorktreeMain = "main"
)

// Role is a job an agent can be started for: who it reports to, what it may do,
// and what it is told about its place before it starts.
//
// A role is not an agent. The agent and model it names are only the ones a
// spawn gets when it asks for none of its own.
type Role struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Agent and Model are the agent and model a spawn gets when it names
	// neither. Tier is used in place of Model: the first model of that tier
	// the chosen agent offers. A model written for one agent is not applied to
	// another, so Model with no Agent is read as the project's default agent's.
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
	Tier  string `json:"tier,omitempty"`
	// Briefing is the text the agent is given about its role, in the "Your
	// role" section of its briefing.
	Briefing string `json:"briefing,omitempty"`
	// Worktree is one of the Worktree constants. Empty is not set.
	Worktree string `json:"worktree,omitempty"`
	// Spawns are the ids of the roles an agent in this role may start. A role
	// with none may not start another role.
	Spawns []string `json:"spawns,omitempty"`
	// Plain lets an agent in this role start agents that have no role. It is
	// off unless set, because an agent with no role has no limits and could
	// start any role, so it would be a way round Spawns.
	Plain bool `json:"plain,omitempty"`
}

// BuiltinRoles are the roles Flockdeck ships. A user's agents.json can change one
// by id or add roles of its own; see mergeRoles.
func BuiltinRoles() []Role {
	return []Role{
		{
			ID: "manager", Name: "Manager", Worktree: WorktreeMain, Plain: true,
			Spawns: []string{"planner", "worker", "reviewer", "tester", "docs"},
			Briefing: "You coordinate the work and do not do it yourself. Break the request into tasks, " +
				"start an agent in the right role for each with `spawn -role`, and give every task enough " +
				"detail to stand on its own, since a new agent has none of your conversation. Keep track of " +
				"who has what, and read what each agent hands back before you pass it on.\n" +
				"Flockdeck does not cut a checkout for a role. Start each worker with " +
				"`flockdeck spawn -role worker -worktree <branch> <task>` so it has a worktree and branch of its " +
				"own, and put the branch name in its task. When you start a reviewer or tester for a worker's " +
				"change, name that branch in its task as well.\n" +
				"Do not edit code, tests or docs yourself, and do not start more agents than the work needs. " +
				"You report to the user: say what is done, what is blocked and what you need decided. " +
				"Ask each agent to finish by stating what it changed, where, and what it did not get to.",
		},
		{
			ID: "planner", Name: "Planner", Worktree: WorktreeReadOnly,
			Briefing: "You turn a request into a plan someone else can carry out. Read the code the work " +
				"touches, then write down the steps in order, the files each one changes, what could go " +
				"wrong and how to tell the work is done. Name the open questions instead of guessing.\n" +
				"Do not change code or commit. You report to whoever started you, usually the manager. " +
				"Hand off the plan as text in your last message, short enough to read in one go, with each " +
				"step written so it can be given to a worker as its task.",
		},
		{
			ID: "worker", Name: "Worker", Worktree: WorktreeOwn,
			Briefing: "You carry out one task. Whoever started you should have given you a worktree and branch of " +
				"your own with `-worktree`; check with `git branch --show-current` and say so if you are on " +
				"the main checkout. Make the change the task asks for, run the tests that cover it, and " +
				"commit on your branch.\n" +
				"Do not change code outside the task, do not touch other worktrees, and do not merge or " +
				"push unless you were told to. If the task is unclear or the plan does not fit the code, " +
				"stop and say so rather than working around it. You report to whoever started you. " +
				"Finish with the branch name, a summary of what changed, the tests you ran and their " +
				"results, and anything you left undone.",
		},
		{
			ID: "reviewer", Name: "Reviewer", Worktree: WorktreeReadOnly,
			Briefing: "You review a change someone else made. Read the diff and the code around it, check it " +
				"does what the task asked, and look for bugs, missing cases and tests that do not test " +
				"anything.\n" +
				"The task names the branch to review; if it does not, ask whoever started you. Read it with " +
				"`git diff` against the base branch or `git show`. Do not edit files, commit or push. " +
				"You report to whoever started you. Hand off a list of findings, each with the file and line, what is wrong and " +
				"why it matters, ordered by how serious it is. Say plainly if you found nothing.",
		},
		{
			ID: "tester", Name: "Tester", Worktree: WorktreeOwn,
			Briefing: "You test a change: write the missing tests, run the suite and try to break the code. " +
				"Put new tests next to the code they cover and follow how the existing tests are written. " +
				"The task names the branch to test; if it does not, ask whoever started you. Work in the " +
				"worktree you were started in and commit the tests there.\n" +
				"Do not fix bugs in the code under test; report them. You report to whoever started you. " +
				"Finish with the tests you added, the commands you ran with their results, and any failure " +
				"with the steps that reproduce it.",
		},
		{
			ID: "docs", Name: "Docs", Worktree: WorktreeOwn,
			Briefing: "You write and correct documentation for a change that already exists. Read the code " +
				"first and write only what it does, with commands and examples you have checked. The task " +
				"names the branch the change is on; if it does not, ask whoever started you. Commit the " +
				"docs in the worktree you were started in.\n" +
				"Do not change code behaviour. If the code and what you were told disagree, say so and " +
				"document the code. You report to whoever started you. Finish with the files you changed " +
				"and anything you could not confirm.",
		},
	}
}

// mergeRoles lays the user's role entries over the built-in roles: built-ins
// first, then new ones in the order the file lists them, with the problems it
// found for the catalog's notice.
//
// An entry whose id is a built-in's is merged field by field, as agents are: a
// field the user wrote wins and one they left out keeps the built-in's. The
// entries are raw JSON for the same reason as File.Agents.
func mergeRoles(raws []json.RawMessage) ([]Role, []string) {
	roles := BuiltinRoles()
	index := make(map[string]int, len(roles))
	for i, r := range roles {
		index[r.ID] = i
	}
	var problems []string
	for n, raw := range raws {
		var head struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			problems = append(problems, fmt.Sprintf("role %d has no id", n+1))
			continue
		}
		head.ID = strings.ToLower(strings.TrimSpace(head.ID))
		if head.ID == "" {
			problems = append(problems, fmt.Sprintf("role %d has no id", n+1))
			continue
		}
		var merged Role
		i, known := index[head.ID]
		if known {
			// A copy, because the decoder writes a list over the elements
			// already in the slice it is given.
			merged = roles[i]
			merged.Spawns = slices.Clone(merged.Spawns)
		}
		if err := json.Unmarshal(raw, &merged); err != nil {
			problems = append(problems, fmt.Sprintf("role %q: %v", head.ID, explainValue(err)))
			continue
		}
		merged.ID = head.ID
		for i, id := range merged.Spawns {
			merged.Spawns[i] = strings.ToLower(strings.TrimSpace(id))
		}
		problems = append(problems, checkRole(&merged)...)
		if known {
			roles[i] = merged
			continue
		}
		index[merged.ID] = len(roles)
		roles = append(roles, merged)
	}
	for _, r := range roles {
		for _, id := range r.Spawns {
			if _, ok := index[id]; !ok {
				problems = append(problems, fmt.Sprintf("role %q may spawn %q, which is not a role", r.ID, id))
			}
		}
	}
	return roles, problems
}

// checkRole settles a role written by hand: a worktree policy or tier that is
// not one of the known ones is named and dropped, as a bad tier on a model is.
func checkRole(r *Role) []string {
	var problems []string
	switch w := strings.ToLower(strings.TrimSpace(r.Worktree)); w {
	case "", WorktreeOwn, WorktreeReadOnly, WorktreeMain:
		r.Worktree = w
	default:
		problems = append(problems, fmt.Sprintf("role %q has worktree %q; the policies are own, readonly and main", r.ID, r.Worktree))
		r.Worktree = ""
	}
	tier := strings.ToLower(strings.TrimSpace(r.Tier))
	if tier != "" && TierRank(tier) == 0 {
		problems = append(problems, fmt.Sprintf("role %q has tier %q; the tiers are small, mid and top", r.ID, r.Tier))
		tier = ""
	}
	r.Tier = tier
	return problems
}

// Role returns the role with an id.
func (c *Catalog) Role(id string) (Role, bool) {
	for _, r := range c.Roles {
		if r.ID == id {
			return r, true
		}
	}
	return Role{}, false
}

// RoleIDs lists the ids of every role, in catalog order.
func (c *Catalog) RoleIDs() []string {
	ids := make([]string, len(c.Roles))
	for i, r := range c.Roles {
		ids[i] = r.ID
	}
	return ids
}

// CheckRole returns the role with an id, or an error naming the ids there are,
// so a typo can be corrected from the message.
func (c *Catalog) CheckRole(id string) (Role, error) {
	r, ok := c.Role(id)
	if !ok {
		return Role{}, fmt.Errorf("no role called %q; the roles are %s", id, strings.Join(c.RoleIDs(), ", "))
	}
	return r, nil
}

// MayStart checks that a pane in the role parent may start one in the role
// child, where an empty child is an agent with no role. A parent with no role
// is not limited: spawn rights restrict the agents that have a role. A pane in
// a role may start an agent with no role only if its role is Plain, since that
// agent would have no limits.
func (c *Catalog) MayStart(parent, child string) error {
	if parent == "" {
		return nil
	}
	p, ok := c.Role(parent)
	if !ok {
		// A role taken out of agents.json after the pane began is not a reason
		// to stop it working.
		return nil
	}
	if child == "" {
		if p.Plain {
			return nil
		}
		return fmt.Errorf("the %s role may not start an agent without a role; use -role", parent)
	}
	if slices.Contains(p.Spawns, child) {
		return nil
	}
	if len(p.Spawns) == 0 {
		return fmt.Errorf("the %s role may not start agents in a role", parent)
	}
	return fmt.Errorf("the %s role may start %s, not %s", parent, strings.Join(p.Spawns, ", "), child)
}

// Choose gives the agent and model a spawn in a role should ask for, from what
// the spawn itself asked for (either may be empty) and the agent a spawn with
// no agent would run.
//
// The role fills in only what the spawn left out. A spawn that names a model
// keeps the agent it would have had, since the model was written for that
// agent, and a role's model is not handed to an agent the role did not name.
func (c *Catalog) Choose(r Role, agentID, model, defaultAgent string) (string, string) {
	if agentID == "" && model == "" {
		agentID = r.Agent
	}
	// The role's model was written for the agent the role resolves to: its own
	// Agent, or the default's when it names none. A spawn that names another
	// agent does not get it.
	home := r.Agent
	if home == "" {
		home = defaultAgent
	}
	if model == "" && r.Model != "" && (agentID == "" || agentID == home) {
		model = r.Model
	}
	if model == "" && r.Tier != "" {
		id := agentID
		if id == "" {
			id = defaultAgent
		}
		if spec, ok := c.Find(id); ok {
			model = spec.ModelOfTier(r.Tier)
		}
	}
	return agentID, model
}

// ModelOfTier is the id of the first model the agent lists in a tier, or "".
func (s Spec) ModelOfTier(tier string) string {
	for _, m := range s.Models {
		if m.Tier == tier && m.ID != "" {
			return m.ID
		}
	}
	return ""
}
