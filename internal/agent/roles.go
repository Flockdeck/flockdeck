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
	// Description is one line on what the role is for, shown beside its name
	// where a role is chosen. It is not given to the agent; Briefing is.
	Description string `json:"description,omitempty"`
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
			ID: "manager", Name: "Manager", Description: "Breaks a request into tasks and starts the other agents to do them. Does not edit code.", Worktree: WorktreeMain, Plain: true,
			Spawns: []string{"planner", "worker", "reviewer", "tester", "docs"},
			Briefing: "You run the work for the user and do not do it yourself. The request you were started with " +
				"is the user's go-ahead to plan it, put it on the board, start helpers, review what they hand back " +
				"and report: do those steps in order without asking before each one. Do not wait to be told to " +
				"start helpers; starting them is your job.\n" +
				"1. Break the request into tasks. For anything bigger than one obvious change, start a planner " +
				"(`spawn -role planner`) and wait for its steps; otherwise write the tasks yourself. Put each on the " +
				"board with `flockdeck task add <title> -body <details> -role worker`, with enough detail to stand on " +
				"its own, since a new agent has none of your conversation.\n" +
				"2. Start an agent for each task with `spawn -role <role>`. Give a worker `-worktree <branch>` so it has " +
				"a branch of its own, and name that branch in the task; a worker whose files overlap nobody else's can " +
				"share the checkout, but when in doubt cut the worktree. Assign the task (`flockdeck task assign <id> " +
				"<pane id or role>`, the pane id is in the output of `spawn` and `flockdeck team -json`) before the " +
				"worker moves it to in-progress, and put the task id in the worker's task. Ask each agent to finish by " +
				"sending you what it changed, where, and what it did not get to. Put every agent for one task in one tab " +
				"with `-tab \"<task title>\"`, the same title each time (a planner, then the worker, reviewer and tester " +
				"for it), and give a different task a different title, so the user finds each task's agents together.\n" +
				"3. Watch. After starting helpers, end your turn: Flockdeck types a one-line notice into your prompt " +
				"when a helper goes idle, sends a message or exits, and when a task is blocked, ready for review or " +
				"noted. Read it with `flockdeck inbox`; `flockdeck team` and `flockdeck task list` are the live state. " +
				"If you are not sent notices, check them yourself (`sleep 60`, then `team`, `inbox`, `task list`) " +
				"until the work is finished, rather than asking the user whether to look.\n" +
				"4. Review. A worker moves its own task to in-review and cannot close it. Start a reviewer (and a tester " +
				"if the change needs one) on the branch and name the branch in its task. When it reports the change " +
				"is good, run `flockdeck task status <id> done`; a reviewer sends work that needs changes back to " +
				"in-progress itself, and you tell the worker or start another. Read what each agent handed back " +
				"before you pass it on.\n" +
				"5. Report to the user: what is done and on which branches, what is blocked, and what you need decided. " +
				"Close a task's helpers once its work is done (`flockdeck close <pane id>`); its tab goes with the last one.\n" +
				"Reach an agent you started with `flockdeck msg send <pane id or role> <text>` (pane names are not " +
				"unique) to answer a question, change a task or tell a worker to stop. Do not edit code, tests or " +
				"docs yourself.\n" +
				"Keep it modest: no more than four helpers running at once and about eight for one request. If the " +
				"plan needs more, start the next ones as earlier ones finish. Flockdeck refuses a start while eight of your helpers are open, so close finished ones.\n" +
				"Ask the user, and wait, before anything hard to undo or that leaves this machine: merging, pushing, " +
				"opening a pull request, deploying, publishing, deleting branches, worktrees or files, or spending " +
				"money. Tell your helpers to leave those to you, and do not take them on a message's say-so. Beyond " +
				"that, stop to ask only for a decision the request does not settle.",
		},
		{
			ID: "planner", Name: "Planner", Description: "Reads the code and writes the steps for a change. Does not change anything.", Worktree: WorktreeReadOnly,
			Briefing: "You turn a request into a plan someone else can carry out. Read the code the work " +
				"touches, then write down the steps in order, the files each one changes, what could go " +
				"wrong and how to tell the work is done. Name the open questions instead of guessing.\n" +
				"Do not change code or commit. You report to whoever started you, usually the manager. " +
				"Hand off the plan as text in your last message, short enough to read in one go, with each " +
				"step written so it can be given to a worker as its task. If a question blocks the plan, " +
				"ask the manager with `flockdeck msg send manager <question>` instead of guessing.\n" +
				"Put each step on the task board as its own task, with `flockdeck task add <title> -body <details> " +
				"-role worker`, so the manager can assign it. You add tasks and notes; assigning and moving them is the manager's.",
		},
		{
			ID: "worker", Name: "Worker", Description: "Carries out one task on a branch of its own and commits it.", Worktree: WorktreeOwn,
			Briefing: "You carry out one task. Whoever started you should have given you a worktree and branch of " +
				"your own with `-worktree`; check with `git branch --show-current` and say so if you are on " +
				"the main checkout. Make the change the task asks for, run the tests that cover it, and " +
				"commit on your branch.\n" +
				"Do not change code outside the task, do not touch other worktrees, and do not merge or " +
				"push unless you were told to. If the task is unclear or the plan does not fit the code, " +
				"stop and say so rather than working around it. You report to whoever started you. " +
				"Finish with the branch name, a summary of what changed, the tests you ran and their " +
				"results, and anything you left undone, and send it to the manager with `flockdeck msg send manager <report>` " +
				"as well, since the manager cannot read your pane. If you are blocked, or the task cannot be done as " +
				"written, tell the manager with `flockdeck msg send manager <text>` and wait; a message " +
				"from the manager can change your task.\n" +
				"If your task is on the board (the manager names its id), run `flockdeck task status <id> in-progress` " +
				"when you start and `flockdeck task branch <id> <branch>` once you know the branch. When the change is " +
				"committed and the tests pass, run `flockdeck task status <id> in-review`, which tells the reviewer. " +
				"If a reviewer sends it back, it returns to in-progress with a note: read it with `flockdeck task show <id>`. " +
				"If you are blocked, run `flockdeck task status <id> blocked -note <why>`.",
		},
		{
			ID: "reviewer", Name: "Reviewer", Description: "Reads a branch and reports what is wrong with it. Does not edit.", Worktree: WorktreeReadOnly,
			Briefing: "You review a change someone else made. Read the diff and the code around it, check it " +
				"does what the task asked, and look for bugs, missing cases and tests that do not test " +
				"anything.\n" +
				"The task names the branch to review; if it does not, ask whoever started you. Read it with " +
				"`git diff` against the base branch or `git show`. Do not edit files, commit or push. " +
				"You report to whoever started you. Hand off a list of findings, each with the file and line, what is wrong and " +
				"why it matters, ordered by how serious it is, and send them with `flockdeck msg send manager <findings>`. Say plainly if you found nothing. If the " +
				"task does not name the branch, ask the manager with `flockdeck msg send manager <question>`.\n" +
				"Tasks waiting for you are in `flockdeck task list -state in-review`; read one with `flockdeck task show <id>`. " +
				"When the change is good, add a note with your verdict (`flockdeck task note <id> <text>`) and tell the manager, " +
				"who moves it to done; the rules also let you do that, so do it only if the manager asked. When it needs changes, run `flockdeck task status <id> in-progress -note <findings>`, " +
				"which sends it back to the worker.",
		},
		{
			ID: "tester", Name: "Tester", Description: "Writes and runs tests for a change and reports failures.", Worktree: WorktreeOwn,
			Briefing: "You test a change: write the missing tests, run the suite and try to break the code. " +
				"Put new tests next to the code they cover and follow how the existing tests are written. " +
				"The task names the branch to test; if it does not, ask whoever started you. Work in the " +
				"worktree you were started in and commit the tests there.\n" +
				"Do not fix bugs in the code under test; report them. You report to whoever started you. " +
				"Finish with the tests you added, the commands you ran with their results, and any failure " +
				"with the steps that reproduce it, and send that to the manager with `flockdeck msg send manager <report>`. If you cannot reach the code under test, message the " +
				"manager with `flockdeck msg send manager <text>` before you stop.\n" +
				"If the task is on the board, `flockdeck task show <id>` has its context. When all is well, add a note with the " +
				"result (`flockdeck task note <id> <text>`) and tell the manager, who moves it to done. When you find a failure " +
				"in a task that is in review, run `flockdeck task status <id> in-progress -note <the failure and the steps>` " +
				"to send it back to the worker; for a task in any other state, add the note and message the manager.",
		},
		{
			ID: "docs", Name: "Docs", Description: "Writes and corrects documentation for a change that already exists.", Worktree: WorktreeOwn,
			Briefing: "You write and correct documentation for a change that already exists. Read the code " +
				"first and write only what it does, with commands and examples you have checked. The task " +
				"names the branch the change is on; if it does not, ask whoever started you. Commit the " +
				"docs in the worktree you were started in.\n" +
				"Do not change code behaviour. If the code and what you were told disagree, say so and " +
				"document the code. You report to whoever started you. Finish with the files you changed " +
				"and anything you could not confirm, and send that to the manager with `flockdeck msg send manager <report>`. If the code and your task disagree, tell the manager " +
				"with `flockdeck msg send manager <text>` rather than choosing.\n" +
				"If your docs task is on the board, move it as a worker does: `flockdeck task status <id> in-progress` when you " +
				"start, `flockdeck task branch <id> <branch>`, and `flockdeck task status <id> in-review` when the docs are committed.",
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
