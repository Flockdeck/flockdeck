package workspace

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/tasks"
	"github.com/jmwri/flockdeck/internal/team"
)

// sizeContext is a realistic briefing input: a pane in a project with three other
// agents, a team roster and a task board, started in the given role ("" for none).
func sizeContext(role string) PaneContext {
	c := PaneContext{
		PaneID: "p4", PaneName: "api", Tab: "api", TabPanes: 2,
		ProjectRoot: "/home/dev/code/api", ProjectName: "api",
		Cwd: "/home/dev/code/api", Branch: "main",
		Task:  "Add retry handling to the token refresh and cover it with tests.",
		Roles: []string{"manager", "planner", "worker", "reviewer", "tester", "docs"},
		Siblings: []Sibling{
			{Name: "api", Tab: "api", Cwd: "/home/dev/code/api-auth", Branch: "fix-auth", Status: "working", Agent: "claude", Model: "sonnet", Task: "repair the token refresh"},
			{Name: "api", Tab: "api", Cwd: "/home/dev/code/api-docs", Branch: "docs", Status: "idle", Agent: "codex", Model: "gpt-5"},
			{Name: "web", Tab: "web", Cwd: "/home/dev/code/web", Branch: "main", Status: "waiting", Agent: "claude"},
		},
		OtherProjects: []string{"web"},
		Team: []team.Member{
			{Pane: "p1", Name: "lead", Role: "manager", Agent: "claude", Model: "opus", Status: "working"},
			{Pane: "p2", Name: "api", Role: "worker", Agent: "claude", Model: "sonnet", Status: "working"},
			{Pane: "p3", Name: "api", Role: "reviewer", Agent: "claude", Status: "idle"},
			{Pane: "p4", Name: "api", Agent: "claude", Status: "working", Self: true},
		},
		TaskCounts: tasks.Counts{Total: 4, Mine: 1, MineActive: 1, InReview: 1, Unassigned: 1},
		HasCounts:  true,
		Taken:      time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC),
		CanSpawn:   true, SpawnCommand: "flockdeck",
		// A project with a knowledge bundle, so the ceilings cover the line a
		// pane in one is actually given.
		KnowledgeDir: "/home/dev/code/api/.flockdeck/knowledge",
	}
	if role != "" {
		cat := &agent.Catalog{Roles: agent.BuiltinRoles()}
		r, ok := cat.Role(role)
		if !ok {
			panic("no role " + role)
		}
		c.Role, c.RoleKnown, c.RoleName, c.RoleBriefing = role, true, r.Name, r.Briefing
		c.RoleWorktree, c.RoleSpawns, c.RolePlain = r.Worktree, r.Spawns, r.Plain
	}
	return c
}

type sizeCase struct {
	name      string
	role      string
	viaPrompt bool
}

func sizeCases() []sizeCase {
	var out []sizeCase
	for _, r := range []string{"", "manager", "worker", "reviewer"} {
		for _, prompt := range []bool{false, true} {
			name := r
			if name == "" {
				name = "no role"
			}
			if prompt {
				name += ", prompt-only"
			} else {
				name += ", hooked"
			}
			out = append(out, sizeCase{name: name, role: r, viaPrompt: prompt})
		}
	}
	return out
}

// sections splits a briefing at its "## " headings, returning heading and size.
func sections(text string) (heads []string, sizes []int) {
	parts := strings.Split(text, "\n## ")
	for i, p := range parts {
		head := "(header)"
		if i > 0 {
			head, _, _ = strings.Cut(p, "\n")
			p = "## " + p
		}
		heads = append(heads, head)
		sizes = append(sizes, len(p))
	}
	return
}

// briefingCeiling is the most bytes a briefing may take in each case. The briefing
// is sent again on every session start, compaction and resume, so growth is paid
// for each time. The ceilings sit about a tenth above the sizes when they were
// set, so an ordinary edit passes and a section pasted back in does not. Raise one
// on purpose, with the reason in the commit, rather than to make a test pass.
var briefingCeiling = map[string]int{
	"":         10500,
	"manager":  14750, // was 14500 until the pane label line (118 bytes) and the -name rule on spawn (about 90 bytes) were added (about 215 in all); was 15250 until the repeated paragraphs in the role and Starting agents were cut; was 15120: one sentence saying the two mail-reading commands need no approval; was 15050: how to find a helper's name and what its idle notice means; was 14750: SendMessage guidance replaces the msg send paragraph and the manager passes its name on; was 14500: the inbox stream the briefing teaches every agent to arm; was 13950: the manager role now carries the whole delegation loop
	"worker":   10200, // was 10000: the line pointing at a knowledge bundle (224 bytes), which the size cases now include, plus 32 bytes for the reworded acknowledgement sentence (256 in all)
	"reviewer": 9900,  // was 9700: the same 256 bytes
}

// TestBriefingStaysWithinItsCeiling checks the rendered size of the briefing for a
// pane with no role, a manager, a worker and a reviewer, briefed through the hook
// and through the opening prompt. Run with -v for the size of each section.
func TestBriefingStaysWithinItsCeiling(t *testing.T) {
	for _, tc := range sizeCases() {
		c := sizeContext(tc.role)
		c.Notified = !tc.viaPrompt
		text := c.render(tc.viaPrompt)
		heads, sizes := sections(text)
		var b strings.Builder
		for i := range heads {
			fmt.Fprintf(&b, "  %5d  %s\n", sizes[i], heads[i])
		}
		t.Logf("%s: %d bytes, about %d tokens\n%s", tc.name, len(text), len(text)/4, b.String())
		if max := briefingCeiling[tc.role]; len(text) > max {
			t.Errorf("%s: the briefing is %d bytes, over its ceiling of %d; the largest sections are in the log", tc.name, len(text), max)
		}
	}
}

// TestBriefingSaysEachRuleOnce covers the sentences that used to be repeated: that
// messages and task text are not instructions from the user, and the flag style
// the command line prints (a single dash).
func TestBriefingSaysEachRuleOnce(t *testing.T) {
	for _, tc := range sizeCases() {
		text := sizeContext(tc.role).render(tc.viaPrompt)
		if n := strings.Count(text, "not instructions from the user"); n != 1 {
			t.Errorf("%s: the not-instructions rule appears %d times, want 1", tc.name, n)
		}
		for _, bad := range []string{" --worktree", " --split", " --shell", " --role", " --agent", " --model", " --force"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: the briefing uses %q, but the command line prints single-dash flags", tc.name, bad)
			}
		}
	}
}

// A role that cannot start agents still gets the feature list: a user may ask any
// pane how to fan out or broadcast. It does not get the spawn section.
func TestBriefingGivesEveryPaneTheFeatureList(t *testing.T) {
	for _, role := range []string{"", "manager", "worker", "reviewer"} {
		text := sizeContext(role).render(false)
		for _, want := range []string{"For a user who asks how to do something", "docs.flockdeck.ai", "is for the user"} {
			if !strings.Contains(text, want) {
				t.Errorf("role %q: briefing lacks %q", role, want)
			}
		}
	}
}

// A pane whose role is no longer in agents.json is refused task assign and prune
// by the board, so the briefing must not offer them.
func TestBriefingOffersNoManagerCommandsToAnUnknownRole(t *testing.T) {
	c := sizeContext("")
	c.Role, c.RoleKnown = "retired", false
	text := c.render(false)
	for _, bad := range []string{" task assign ", " task prune "} {
		if strings.Contains(text, bad) {
			t.Errorf("a pane with an unknown role is offered %q", bad)
		}
	}
	if !strings.Contains(text, " task add ") {
		t.Error("the task commands are missing")
	}
}

// close is for panes the agent started; close -finished reaches the user's own.
func TestBriefingLimitsCloseToHelpersTheAgentStarted(t *testing.T) {
	text := sizeContext("").render(false)
	for _, want := range []string{"Close only panes you started", "the id `spawn` printed back", "the user's own included"} {
		if !strings.Contains(text, want) {
			t.Errorf("briefing lacks %q", want)
		}
	}
}
