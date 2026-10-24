package server

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// The five fixtures of the week-10 test, built as the graph push message and
// not drawn. Each is a manager with six to eight helpers, which is what
// workspace.MaxHelpers allows (the cap counts nested helpers and exited panes
// still open), and one to three of them are waiting on the person. The builders
// run the same derivation the server does (team.BuildGraph and team.NeedsYou),
// so a node's waiting-on, a team's needs list and the answer key cannot drift.
//
// The messages are written to testdata/graph-fixtures, one .json and one .md
// each, for a reader that is not Go. TestGraphFixturesAreCurrent fails when they
// differ from the builders; GRAPH_FIXTURES_UPDATE=1 go test -run
// TestGraphFixturesAreCurrent ./internal/server writes them again.

// fxNow is the time every fixture is judged at, so the files do not change.
var fxNow = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

// A helper's state, as the week-10 test names them.
const (
	fxWorking   = "working"          // mid-turn, editing
	fxIdle      = "idle"             // finished its turn, nothing owed
	fxExited    = "exited"           // process ended cleanly
	fxCrashed   = "crashed"          // process ended with exit code 2
	fxPerm      = "permission"       // waiting on the person to allow a command
	fxAsk       = "question"         // waiting on the person's answer
	fxReturned  = "returned"         // task came back from review with a note
	fxInReview  = "waiting-reviewer" // task in review, waiting on a reviewer
	fxBlocked   = "blocked-task"     // task blocked, with a note saying why
	fxManagerID = "manager"
)

// fxHelper is one helper of a fixture.
type fxHelper struct {
	name, parent, state string
	// text is the command for fxPerm, the question for fxAsk, the note for
	// fxReturned and fxBlocked, and the tool detail for fxWorking.
	text string
	// task is the title of the task the helper owns, for the states that have one.
	task string
	// since is how many minutes ago the status began.
	since int
	// agent is the agent kind; empty means claude.
	agent string
	// usd is what the pane has spent, for a node's spend figure.
	usd float64
	// lastOut is how many minutes ago the terminal last wrote; zero leaves it unset.
	lastOut int
}

// fxMessage is a message from a helper to the manager, or to another helper
// when to is set, that has not been read.
type fxMessage struct {
	from, text string
	// to is the receiving helper; empty means the manager.
	to         string
	minutesAgo int
}

// fxConflict is two helpers that change the same files.
type fxConflict struct {
	a, b  string
	paths []string
}

// fxExpect is one thing that wants the person, as the answer key says it.
type fxExpect struct {
	pane, kind, why string
}

type fxSpec struct {
	name, description string
	helpers           []fxHelper
	message           *fxMessage
	// managerIdle makes the manager idle between turns, so unread mail it has not
	// read is a stale-unread item.
	managerIdle bool
	// conflict is a pair of helpers the conflict radar lists.
	conflict *fxConflict
	// expect is the answer key: every item that wants the person.
	expect []fxExpect
}

// fxID is the pane id: eight hex characters, as short as a real graph shows. The
// name field keeps the readable name.
func fxID(spec fxSpec, name string) string {
	sum := sha1.Sum([]byte(spec.name + "/" + name))
	return hex.EncodeToString(sum[:4])
}

var graphFixtureSpecs = []fxSpec{
	{
		name:        "01-six-helpers-one-permission",
		description: "Six helpers on a checkout rewrite. One is asking to run a command, and two of the helpers change the same file.",
		helpers: []fxHelper{
			{name: "cart", state: fxWorking, text: "Edit cart/total.go", since: 2},
			{name: "tax", state: fxPerm, text: "go test ./tax/... -run TestRounding -count=1", since: 4},
			{name: "docs", state: fxIdle, since: 11},
			{name: "migrate", state: fxExited, since: 30},
			{name: "api", state: fxInReview, task: "Add the totals endpoint", since: 9},
			{name: "search", state: fxBlocked, task: "Index the new fields", text: "needs the staging index, which only the person can create", since: 16},
		},
		conflict: &fxConflict{a: "cart", b: "tax", paths: []string{"cart/total.go"}},
		expect: []fxExpect{
			{"tax", "permission", "asks to run go test ./tax/... -run TestRounding -count=1"},
			{"cart", "conflict", "cart and tax both change cart/total.go"},
		},
	},
	{
		name:        "02-eight-helpers-permission-and-question",
		description: "Eight helpers on a billing migration. One asks to run a command, one asks a question, and one ended in error.",
		helpers: []fxHelper{
			{name: "schema", state: fxPerm, text: "psql -f migrations/0042_invoices.sql", since: 3},
			{name: "backfill", state: fxAsk, text: "Backfill invoices older than 2024, or leave them as they are?", since: 7},
			{name: "ledger", state: fxWorking, text: "Edit ledger/post.go", since: 1},
			{name: "reports", state: fxWorking, text: "Bash go build ./...", since: 1},
			{name: "webhooks", state: fxIdle, since: 14},
			{name: "fixtures", state: fxCrashed, since: 25},
			{name: "emails", state: fxInReview, task: "Receipt email copy", since: 6},
			{name: "retries", state: fxBlocked, task: "Retry failed charges", text: "waiting on the payment sandbox keys", since: 20},
		},
		expect: []fxExpect{
			{"backfill", "ask", "asks: Backfill invoices older than 2024, or leave them as they are?"},
			{"schema", "permission", "asks to run psql -f migrations/0042_invoices.sql"},
			{"fixtures", "down", "exited with code 2, so the helper ended in error"},
		},
	},
	{
		name:        "03-nested-helper-question-and-returned-task",
		description: "Seven helpers on a search feature, one of them with a helper of its own. One asks a question and one has a task back from review.",
		helpers: []fxHelper{
			{name: "indexer", state: fxWorking, text: "Edit search/index.go", since: 2},
			{name: "shards", parent: "indexer", state: fxWorking, text: "Bash go test ./search/shard", since: 1},
			{name: "ranking", state: fxAsk, text: "Boost exact title matches over body matches?", since: 5},
			{name: "ui", state: fxReturned, task: "Result list keyboard focus", text: "Focus is lost after a page change. Please fix before it goes in.", since: 12},
			{name: "analytics", state: fxIdle, since: 18},
			{name: "synonyms", state: fxExited, since: 40},
			{name: "perf", state: fxInReview, task: "Cut query latency", since: 8},
		},
		expect: []fxExpect{
			{"ranking", "ask", "asks: Boost exact title matches over body matches?"},
			{"ui", "returned", "task returned from review: Focus is lost after a page change. Please fix before it goes in."},
		},
	},
	{
		name:        "04-eight-helpers-three-waiting",
		description: "Eight helpers on a release. One asks to run a command, one asks a question and one has a task back from review. Three need the person but only two are waiting: the one with the returned task (qa) is working, so the tiled page marks two panes and the graph lists three.",
		helpers: []fxHelper{
			{name: "build", state: fxPerm, text: "git push origin release/2.4", since: 2},
			{name: "notes", state: fxAsk, text: "Name the breaking change in the headline?", since: 10},
			{name: "qa", state: fxReturned, task: "Smoke test the installer", text: "The installer fails on a path with a space. Reproduce and fix.", since: 15},
			{name: "deps", state: fxWorking, text: "Edit go.mod", since: 1},
			{name: "site", state: fxWorking, text: "Edit docs/download.md", since: 3},
			{name: "signing", state: fxBlocked, task: "Sign the Windows build", text: "the signing certificate has not arrived", since: 45},
			{name: "changelog", state: fxIdle, since: 22},
			{name: "old-branch", state: fxExited, since: 90},
		},
		expect: []fxExpect{
			{"build", "permission", "asks to run git push origin release/2.4"},
			{"notes", "ask", "asks: Name the breaking change in the headline?"},
			{"qa", "returned", "task returned from review: The installer fails on a path with a space. Reproduce and fix."},
		},
	},
	{
		name:        "05-six-helpers-undelivered-message",
		description: "Six helpers on an onboarding flow. One asks a question. The manager is idle with an unread message from a helper that has waited forty-five minutes.",
		helpers: []fxHelper{
			{name: "forms", state: fxAsk, text: "Make the phone number optional?", since: 6},
			{name: "copy", state: fxWorking, text: "Edit onboarding/welcome.md", since: 2},
			{name: "auth", state: fxInReview, task: "Magic link sign-in", since: 13},
			{name: "tests", state: fxIdle, since: 9},
			{name: "icons", state: fxExited, since: 35},
			{name: "i18n", state: fxBlocked, task: "Extract the new strings", text: "waiting on the translation vendor's key format", since: 25},
		},
		message:     &fxMessage{from: "tests", text: "The sign-up form submits twice on a slow connection. Needs a decision before I write the fix.", minutesAgo: 45},
		managerIdle: true,
		expect: []fxExpect{
			{"forms", "ask", "asks: Make the phone number optional?"},
			{"manager", "stale-unread", "1 unread: the message from tests has waited forty-five minutes and the manager is idle"},
		},
	},
	{
		name:        "06-rate-limiting-tree-home",
		description: "The prototype's team \"rate limiting\": a manager and an api lead with tests and bench nested under it, eight helpers in all (the cap). The lead asks to run a command, ci asks a question, api and cfg change the same file, and lint ended in error. cdx has written nothing for twelve minutes while working, which is a quiet stuck item. The nodes carry the per-node figures: spend, last output, undelivered messages and the rollups below and needYou. The prototype's docs helper is left out to stay at the cap, and its cdx is stuck at seven minutes where the real rule waits for ten.",
		helpers: []fxHelper{
			{name: "api", state: fxPerm, text: "go test ./... -race -count=1", since: 2, usd: 3.82},
			{name: "tests", parent: "api", state: fxWorking, text: "Write limiter_test.go", since: 4, usd: 0.73, lastOut: 1},
			{name: "bench", parent: "api", state: fxWorking, text: "Bash go test -bench Limiter ./mw", since: 1, usd: 0.3, lastOut: 1},
			{name: "ci", state: fxAsk, text: "T2 came back with \"races on map\". Send it back a third time, or try a different approach?", since: 9, usd: 2.4},
			{name: "cfg", state: fxInReview, task: "Config flags", since: 3, usd: 1.35},
			{name: "rev", state: fxWorking, text: "Read diff, 2 files", since: 1, usd: 0.9, lastOut: 1},
			{name: "cdx", state: fxWorking, text: "Bash go build ./store/...", since: 7, usd: 1.5, agent: "codex", lastOut: 12},
			{name: "lint", state: fxCrashed, since: 6, usd: 0.4},
		},
		conflict: &fxConflict{a: "api", b: "cfg", paths: []string{"cfg/limits.yaml"}},
		message:  &fxMessage{from: "tests", to: "api", text: "I need the signature of Allow before I can finish the table tests.", minutesAgo: 8},
		expect: []fxExpect{
			{"api", "permission", "asks to run go test ./... -race -count=1"},
			{"ci", "ask", "asks: T2 came back with \"races on map\". Send it back a third time, or try a different approach?"},
			{"api", "conflict", "api and cfg both change cfg/limits.yaml"},
			{"lint", "down", "exited with code 2, so the helper ended in error"},
			{"cdx", "stuck", "no output for 12m while working; a quiet item, it does not ask anything of the person"},
		},
	},
}

// buildGraphFixture is the graph push for one spec.
func buildGraphFixture(spec fxSpec) graphMsg {
	mgr := fxID(spec, fxManagerID)
	panes := []team.GraphPane{{
		ID: mgr, Role: team.ManagerRole, Name: "manager", Agent: "claude", Status: "working", Activity: "working",
		StatusSince: fxNow.Add(-40 * time.Minute),
	}}
	if spec.managerIdle {
		panes[0].Status, panes[0].Activity = "idle", "idle"
	}
	byName := map[string]string{}
	var tasks []team.GraphTask
	var moves []team.TaskMove
	chips := map[string]graphTaskChip{}
	live := map[string]string{}
	taskN := 0

	for _, h := range spec.helpers {
		id := fxID(spec, h.name)
		byName[h.name] = id
		parent := mgr
		if h.parent != "" {
			parent = fxID(spec, h.parent)
		}
		since := fxNow.Add(-time.Duration(h.since) * time.Minute)
		agent := h.agent
		if agent == "" {
			agent = "claude"
		}
		p := team.GraphPane{ID: id, Parent: parent, Role: "worker", Name: h.name, Agent: agent,
			Status: "idle", Activity: "idle", StatusSince: since}
		if h.lastOut > 0 {
			p.LastOutput = fxNow.Add(-time.Duration(h.lastOut) * time.Minute)
		}
		addTask := func(state string) {
			taskN++
			tid := fmt.Sprintf("T%d", taskN)
			tasks = append(tasks, team.GraphTask{ID: tid, State: state, OwnerRole: "worker", OwnerPane: id,
				ReviewerRole: map[bool]string{true: "reviewer"}[state == "in-review"], Title: h.task, LastNote: h.text})
			chips[id] = graphTaskChip{ID: tid, Title: h.task, State: state}
			if state == "in-progress" {
				moves = append(moves, team.TaskMove{Task: tid, From: "in-review", To: "in-progress", Time: since, Note: h.text})
			}
		}
		switch h.state {
		case fxWorking:
			p.Status, p.Activity = "working", "working"
			live[id] = h.text
		case fxIdle:
		case fxExited:
			p.Status, p.Activity, p.Exited = "exited", "exited", true
		case fxCrashed:
			p.Status, p.Activity, p.Exited, p.ExitCode = "exited", "exited", true, 2
		case fxPerm:
			p.Status, p.Activity = "waiting", "waiting"
			p.HasPrompt, p.PromptKind, p.PromptText = true, team.PromptPermission, h.text
		case fxAsk:
			p.Status, p.Activity = "waiting", "waiting"
			p.HasPrompt, p.PromptKind, p.PromptText = true, team.PromptAsk, h.text
		case fxReturned:
			p.Status, p.Activity = "working", "working"
			live[id] = "Reading the review note"
			addTask("in-progress")
		case fxInReview:
			addTask("in-review")
		case fxBlocked:
			addTask("blocked")
		}
		panes = append(panes, p)
	}
	if m := spec.message; m != nil {
		to := mgr
		if m.to != "" {
			to = fxID(spec, m.to)
		}
		for i := range panes {
			if panes[i].ID == to {
				panes[i].Unread, panes[i].UnreadMail = 1, 1
				panes[i].UnreadSince = fxNow.Add(-time.Duration(m.minutesAgo) * time.Minute)
			}
		}
	}

	graph := team.BuildGraph(panes, tasks)
	var conflicts []team.PaneConflict
	if c := spec.conflict; c != nil {
		conflicts = append(conflicts, team.PaneConflict{A: fxID(spec, c.a), B: fxID(spec, c.b), Paths: c.paths, Since: fxNow.Add(-12 * time.Minute)})
	}
	needs := team.NeedsYou(graph, panes, tasks, conflicts, moves, fxNow)
	pane := map[string]team.GraphPane{}
	for _, p := range panes {
		pane[p.ID] = p
	}

	stuck, below := graphRollup(graph, needs)
	usd := map[string]float64{}
	priced := false
	for _, h := range spec.helpers {
		usd[fxID(spec, h.name)] = h.usd
		priced = priced || h.usd > 0
	}
	out := graphMsg{Type: "graph", Root: "/work/" + spec.name, Project: spec.name, Teams: []graphTeamView{},
		Nodes: []graphNodeView{}, Edges: []graphEdge{}, Solo: graph.Solo, SoloNeeds: []team.NeedsItem{}}
	for _, n := range graph.Nodes {
		g := pane[n.Pane]
		nv := graphNodeView{GraphNode: n, Kind: graphKindTerminal, Name: g.Name, Role: g.Role, Agent: g.Agent,
			Status: g.Status, Activity: g.Activity, Since: g.StatusSince.Format(time.RFC3339), Live: live[n.Pane],
			Branch: "work/" + g.Name, Unread: g.Unread, SpendUSD: usd[n.Pane], Stuck: stuck[n.Pane]}
		nv.Below, nv.NeedYou = below(n.Pane)
		if !g.LastOutput.IsZero() {
			nv.LastOutput = g.LastOutput.Format(time.RFC3339)
		}
		if n.Pane == mgr {
			nv.Branch = "main"
			nv.Live = ""
		}
		if c, ok := chips[n.Pane]; ok {
			c := c
			nv.Task = &c
		}
		out.Nodes = append(out.Nodes, nv)
		if n.Parent != "" {
			out.Edges = append(out.Edges, graphEdge{Kind: graphEdgeSpawn, From: n.Parent, To: n.Pane})
		}
	}
	if m := spec.message; m != nil {
		// The message is queued: it was not shown to the manager, woken or read.
		// Stage 0a had no delivery field. The schema now requires one on a message
		// edge, so the fixture says queued.
		to := mgr
		if m.to != "" {
			to = byName[m.to]
		}
		for i := range out.Nodes {
			if out.Nodes[i].Pane == byName[m.from] {
				out.Nodes[i].Undelivered = 1
			}
		}
		out.Edges = append(out.Edges, graphEdge{Kind: graphEdgeMessage, From: byName[m.from], To: to, ID: 1,
			Time: fxNow.Add(-time.Duration(m.minutesAgo) * time.Minute).Format(time.RFC3339), Text: m.text, Delivery: "queued"})
	}

	tm := graph.Teams[0]
	tv := graphTeamView{ID: tm.ID, Manager: mgr, Members: tm.Members, Goal: "Ship " + spec.name,
		Started: fxNow.Add(-2 * time.Hour).Format(time.RFC3339), SpendUSD: 3.5 + float64(len(spec.helpers))*0.75,
		Caps: []graphCap{{Pane: mgr, Used: len(spec.helpers), Max: workspace.MaxHelpers}}, Needs: []team.NeedsItem{}}
	for _, n := range graph.Nodes {
		if n.Pane != mgr && len(n.Children) > 0 {
			tv.Caps = append(tv.Caps, graphCap{Pane: n.Pane, Used: len(n.Children), Max: workspace.MaxHelpers})
		}
	}
	if priced {
		tv.SpendUSD = 6.1
		for _, v := range usd {
			tv.SpendUSD += v
		}
		tv.SpendUSD = math.Round(tv.SpendUSD*100) / 100
		for i := range out.Nodes {
			if out.Nodes[i].Pane == mgr {
				out.Nodes[i].SpendUSD = 6.1
			}
		}
	}
	if n, ok := needs[tm.ID]; ok {
		tv.Needs = n.Items
	}
	out.Teams = append(out.Teams, tv)
	return out
}

// fxAnswerKey is the description file of a fixture: the correct answer to "which
// agents are waiting on me, and why", in the order the needs list shows them.
func fxAnswerKey(spec fxSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", spec.name, spec.description)
	fmt.Fprintf(&b, "The team is a manager and %d helpers", len(spec.helpers))
	for _, h := range spec.helpers {
		if h.parent != "" {
			fmt.Fprintf(&b, ", of which %s is a helper of %s", h.name, h.parent)
		}
	}
	b.WriteString(". The graph is judged at 2026-10-21T09:00:00Z.\n\n")
	fmt.Fprintf(&b, "## Needs the person (%d)\n\n", len(spec.expect))
	for _, e := range spec.expect {
		fmt.Fprintf(&b, "- %s: %s\n", fxLabel(spec, e), e.why)
	}
	b.WriteString("\n## The other helpers\n\n")
	asked := map[string]bool{}
	for _, e := range spec.expect {
		asked[e.pane] = true
	}
	for _, h := range spec.helpers {
		if asked[h.name] {
			continue
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", h.name, fxID(spec, h.name), fxOtherState(h))
	}
	if m := spec.message; m != nil {
		if m.to != "" {
			fmt.Fprintf(&b, "\n## The message\n\n%s sent %s a message %d minutes before the graph's time, and it is still queued. The sender's node counts it in `undelivered`. %s is not idle, so it is not a stale-unread item.\n", m.from, m.to, m.minutesAgo, m.to)
			return b.String()
		}
		fmt.Fprintf(&b, "\n## The message\n\n%s sent the manager a message %d minutes before the graph's time, and it is still queued: the manager has not been shown it, woken for it or read it. The manager is idle and has not read it, so after thirty minutes it is a stale-unread item for the person. The message edge says `queued`; stage 0a had no delivery field, and the schema now requires one on a message edge.\n", m.from, m.minutesAgo)
	}
	return b.String()
}

// fxLabel names the pane or panes of a key entry with their ids.
func fxLabel(spec fxSpec, e fxExpect) string {
	if c := spec.conflict; c != nil && e.kind == "conflict" {
		return fmt.Sprintf("%s (%s) and %s (%s)", c.a, fxID(spec, c.a), c.b, fxID(spec, c.b))
	}
	return fmt.Sprintf("%s (%s)", e.pane, fxID(spec, e.pane))
}

func fxOtherState(h fxHelper) string {
	switch h.state {
	case fxWorking:
		return "working (" + h.text + ")"
	case fxIdle:
		return "idle, nothing owed"
	case fxExited:
		return "exited cleanly"
	case fxCrashed:
		return "exited with code 2"
	case fxInReview:
		return "idle, its task \"" + h.task + "\" is in review and waits on a reviewer"
	case fxBlocked:
		return "idle, its task \"" + h.task + "\" is blocked: " + h.text
	}
	return h.state
}

func fxPath(spec fxSpec, ext string) string {
	return filepath.Join("testdata", "graph-fixtures", spec.name+ext)
}

func fxJSON(t *testing.T, spec fxSpec) []byte {
	t.Helper()
	b, err := json.MarshalIndent(buildGraphFixture(spec), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// Each fixture matches the published schema, has the shape the week-10 test
// asks for, and its needs list is the answer key.
func TestGraphFixturesMatchTheSchemaAndTheirAnswers(t *testing.T) {
	schema := loadGraphSchema(t)
	seen := map[string]bool{}
	for _, spec := range graphFixtureSpecs {
		t.Run(spec.name, func(t *testing.T) {
			raw := fxJSON(t, spec)
			if errs := schema.Validate(raw); len(errs) != 0 {
				t.Fatalf("does not match the schema: %v", errs)
			}
			g := buildGraphFixture(spec)

			// A manager and six to eight helpers, in one team.
			if n := len(spec.helpers); n < 6 || n > workspace.MaxHelpers {
				t.Errorf("%d helpers", n)
			}
			if len(g.Teams) != 1 || len(g.Teams[0].Members) != len(spec.helpers)+1 || len(g.Solo) != 0 {
				t.Fatalf("teams %+v, solo %v", g.Teams, g.Solo)
			}
			if c := g.Teams[0].Caps[0]; c.Used != len(spec.helpers) || c.Max != workspace.MaxHelpers {
				t.Errorf("manager cap %+v", c)
			}
			// One to three helpers wait on the person for a prompt or a returned task;
			// the key may add down, conflict and stale-unread items.
			waiters := 0
			for _, e := range spec.expect {
				if e.kind == "permission" || e.kind == "ask" || e.kind == "returned" {
					waiters++
				}
			}
			if waiters < 1 || waiters > 3 {
				t.Errorf("%d helpers wait on the person", waiters)
			}

			// The answer key is what the needs list says: the same panes and kinds,
			// and each why contains the item's own text.
			name := map[string]string{fxID(spec, fxManagerID): fxManagerID}
			for _, h := range spec.helpers {
				name[fxID(spec, h.name)] = h.name
			}
			var got, want []fxExpect
			for _, it := range g.Teams[0].Needs {
				pane := name[it.Pane]
				if it.Kind == "conflict" {
					c := spec.conflict
					if c == nil || !(pane == c.a && name[it.Other] == c.b || pane == c.b && name[it.Other] == c.a) {
						t.Fatalf("conflict item %+v is not the fixture's pair", it)
					}
					pane = c.a
				}
				got = append(got, fxExpect{pane: pane, kind: it.Kind})
				for _, e := range spec.expect {
					if e.pane == pane && e.kind == it.Kind && it.Text != "" && !strings.Contains(e.why, it.Text) {
						t.Errorf("%s %s: the key says %q, the needs item says %q", pane, it.Kind, e.why, it.Text)
					}
				}
			}
			for _, e := range spec.expect {
				want = append(want, fxExpect{pane: e.pane, kind: e.kind})
			}
			sortFx(got)
			sortFx(want)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("needs list %v, answer key %v", got, want)
			}

			// Each node says what it waits on.
			userNodes, prompts := 0, 0
			for _, e := range spec.expect {
				if e.kind == "permission" || e.kind == "ask" {
					prompts++
				}
			}
			for _, n := range g.Nodes {
				if n.WaitingOn.Kind == team.WaitUser {
					userNodes++
				}
			}
			if userNodes != prompts {
				t.Errorf("%d nodes wait on the user, the key has %d prompts", userNodes, prompts)
			}
			for _, h := range spec.helpers {
				n, _ := g.node(fxID(spec, h.name))
				switch h.state {
				case fxInReview:
					if n.WaitingOn.Kind != team.WaitReviewer || n.Task == nil || n.WaitingOn.Ref != n.Task.ID || !strings.Contains(n.WaitingOn.Text, "in review") {
						t.Errorf("%s waits on %+v", h.name, n.WaitingOn)
					}
				case fxBlocked:
					if n.WaitingOn.Kind != team.WaitTask || n.WaitingOn.Text != h.text {
						t.Errorf("%s waits on %+v, want its task note", h.name, n.WaitingOn)
					}
				case fxCrashed:
					if n.WaitingOn.Kind != team.WaitDown {
						t.Errorf("%s waits on %+v", h.name, n.WaitingOn)
					}
				}
			}
			for _, it := range g.Teams[0].Needs {
				seen["need:"+it.Kind] = true
			}
			for _, n := range g.Nodes {
				seen[n.WaitingOn.Kind] = true
			}
			for _, h := range spec.helpers {
				seen["helper:"+h.state] = true
				if h.parent != "" {
					seen["nested"] = true
				}
			}
			if spec.message != nil {
				seen["message"] = true
			}
		})
	}
	// Between them the fixtures cover every state the test names.
	for _, k := range []string{"helper:" + fxWorking, "helper:" + fxIdle, "helper:" + fxExited, "helper:" + fxCrashed, "helper:" + fxPerm,
		"helper:" + fxAsk, "helper:" + fxReturned, "helper:" + fxInReview, "helper:" + fxBlocked,
		team.WaitUser, team.WaitDown, team.WaitReviewer, team.WaitTask, "nested", "message", "need:conflict", "need:stale-unread", "need:down", "need:returned", "need:permission", "need:ask"} {
		if !seen[k] {
			t.Errorf("no fixture has %s", k)
		}
	}
	if len(graphFixtureSpecs) != 6 {
		t.Errorf("%d fixtures, want six", len(graphFixtureSpecs))
	}
}

func sortFx(l []fxExpect) {
	sort.Slice(l, func(i, j int) bool { return l[i].pane < l[j].pane })
}

// Two fixtures have a nested helper and two an undelivered message.
func TestGraphFixturesHaveOneNestedAndOneUndelivered(t *testing.T) {
	nested, undelivered := 0, 0
	for _, spec := range graphFixtureSpecs {
		for _, h := range spec.helpers {
			if h.parent != "" {
				nested++
				break
			}
		}
		if spec.message != nil {
			undelivered++
			var edge *graphEdge
			g := buildGraphFixture(spec)
			for i := range g.Edges {
				if g.Edges[i].Kind == graphEdgeMessage {
					edge = &g.Edges[i]
				}
			}
			if edge == nil || edge.Delivery != "queued" {
				t.Errorf("%s: message edge %+v", spec.name, edge)
			} else if at, err := time.Parse(time.RFC3339, edge.Time); err != nil || fxNow.Sub(at) < 5*time.Minute {
				t.Errorf("%s: the message is not several minutes old", spec.name)
			}
		}
	}
	if nested != 2 || undelivered != 2 {
		t.Errorf("%d fixtures with a nested helper, %d with an undelivered message, want two of each", nested, undelivered)
	}
}

// The files in testdata are what the builders make.
func TestGraphFixturesAreCurrent(t *testing.T) {
	update := os.Getenv("GRAPH_FIXTURES_UPDATE") != ""
	if update {
		if err := os.MkdirAll(filepath.Join("testdata", "graph-fixtures"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, spec := range graphFixtureSpecs {
		for ext, want := range map[string][]byte{".json": fxJSON(t, spec), ".md": []byte(fxAnswerKey(spec))} {
			path := fxPath(spec, ext)
			if update {
				if err := os.WriteFile(path, want, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("%v (GRAPH_FIXTURES_UPDATE=1 writes the files)", err)
				continue
			}
			if strings.ReplaceAll(string(got), "\r\n", "\n") != string(want) {
				t.Errorf("%s is out of date (GRAPH_FIXTURES_UPDATE=1 writes the files)", path)
			}
		}
	}
}
