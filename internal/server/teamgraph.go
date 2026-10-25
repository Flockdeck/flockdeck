package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/spend"
	"github.com/jmwri/flockdeck/internal/tasks"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// This file is the graph view of the Team subscription: a project's panes
// placed in teams, with what each is doing and waiting on, who messaged whom,
// and what needs the person. It is read-only. graphSnapshot, graphWatch and
// graphUnwatch are modelled on teamSnapshot, teamWatch and teamUnwatch and are
// refused the same way for a window reached through the relay.
//
// The derivation itself is in internal/team (BuildGraph and NeedsYou), which
// reads nothing but what is passed to it. Everything here is gathering that
// input from the workspace and encoding the answer.

// Limits on a graph push, in the way the Team push is bounded.
const (
	// maxGraphEdges is how many message edges a push lists, newest first.
	maxGraphEdges = 100
	// graphLiveCut and graphEdgeCut are how many characters the live line and
	// an edge's first line keep. The live line matches the inbox row's cap.
	graphLiveCut = 140
	graphEdgeCut = 140
	// graphGoalCut is how many characters a team's goal keeps.
	graphGoalCut = 300
	// graphNameCut, graphPeerCut, graphBranchCut and graphAgentCut are how many
	// characters a node's own name, the name its agent reported, its branch and
	// its agent id keep. They are text a person or an agent chose, so they are
	// bounded like the rest.
	graphNameCut   = 80
	graphPeerCut   = 80
	graphBranchCut = 120
	graphAgentCut  = 80
	// maxGraphPanes bounds the nodes in a push. A project has no more panes
	// than a window can open, so this only guards against a runaway.
	maxGraphPanes = 500
)

// The kinds of pane. Structured is reserved for panes an agent drives over a
// protocol rather than a terminal; nothing sets it yet.
const (
	graphKindTerminal   = string(workspace.TransportTerminal)
	graphKindStructured = string(workspace.TransportStructured)
)

// graphTaskChip is the task a node names as its current one.
type graphTaskChip struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// graphNodeView is one pane in the graph: the derived placement and waiting-on
// from team.BuildGraph, with the facts a node draws.
type graphNodeView struct {
	team.GraphNode
	// Kind is how the pane is run, as its runtime says: every pane is a terminal today.
	Kind string `json:"kind"`
	Name string `json:"name"`
	Peer string `json:"peer,omitempty"`
	// Label is what the pane is called for its work (spawn -name, flockdeck
	// pane-name), cut to team.MaxPaneLabel characters. Absent until it has one.
	Label    string `json:"label,omitempty"`
	Role     string `json:"role,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Shell    bool   `json:"shell,omitempty"`
	Status   string `json:"status"`
	Activity string `json:"activity"`
	// Since is when the status began, RFC 3339, left out when unknown.
	Since string `json:"since,omitempty"`
	// Live is the current tool detail, or else the latest reply, on one line of
	// at most graphLiveCut characters.
	Live      string         `json:"live,omitempty"`
	Branch    string         `json:"branch,omitempty"`
	Dirty     int            `json:"dirty,omitempty"`
	Untracked int            `json:"untracked,omitempty"`
	Ahead     int            `json:"ahead,omitempty"`
	Behind    int            `json:"behind,omitempty"`
	Task      *graphTaskChip `json:"task,omitempty"`
	Unread    int            `json:"unread,omitempty"`
	// AutoReview says the pane's tool calls are put to auto-review, and
	// AutoApproved how many that let through unasked.
	AutoReview   bool `json:"autoReview,omitempty"`
	AutoApproved int  `json:"autoApproved,omitempty"`
	// SpendUSD is this pane's own estimated spend, and SpendPartial says it has
	// run and reported no priced figure, so the number is a floor. SpendIncludes
	// Partial says it reports its spend in part (see graphTeamView).
	SpendUSD             float64 `json:"spendUSD,omitempty"`
	SpendPartial         bool    `json:"spendPartial,omitempty"`
	SpendIncludesPartial bool    `json:"spendIncludesPartial,omitempty"`
	// Undelivered is how many messages this pane sent to another pane of the
	// project that have not been told to their pane yet. It counts every such
	// message, not only the newest maxGraphEdges the edges list.
	Undelivered int `json:"undelivered,omitempty"`
	// LastOutput is when a terminal pane's process last wrote to its terminal,
	// RFC 3339. Absent for a structured pane, which has no byte stream, and for a
	// pane that has not written.
	LastOutput string `json:"lastOutput,omitempty"`
	// Stuck says the pane is working and has written nothing for team.StuckAfter:
	// the pane's own flag for the quiet item of that kind.
	Stuck bool `json:"stuck,omitempty"`
	// Below counts the panes under this one (children, their children and so on),
	// and NeedYou how many of them are waiting on the person. Absent at 0.
	Below   int `json:"below,omitempty"`
	NeedYou int `json:"needYou,omitempty"`
}

// graphCap is the helper cap use of one pane: how many panes are open below it
// and the most it may have (workspace.MaxHelpers).
type graphCap struct {
	Pane string `json:"pane"`
	Used int    `json:"used"`
	Max  int    `json:"max"`
}

// graphTeamView is one team.
type graphTeamView struct {
	ID      string   `json:"id"`
	Manager string   `json:"manager"`
	Members []string `json:"members"`
	// Goal is the manager's opening task, and Started when its process began.
	Goal    string `json:"goal,omitempty"`
	Started string `json:"started,omitempty"`
	// TaskID is the board task the team works on: the one given to it with
	// taskTeam, else the first open one the manager owns. Left out when there
	// is none.
	TaskID string `json:"taskId,omitempty"`
	// SpendUSD is the estimated spend of the team's panes that reported one.
	// SpendPartial says some pane reported none (or an unpriced one), so the
	// total is a floor.
	SpendUSD     float64 `json:"spendUSD"`
	SpendPartial bool    `json:"spendPartial,omitempty"`
	// SpendIncludesPartial says a pane of the team reports its spend in part (a
	// structured agent: context use and perhaps a cost, no token split), so the
	// total includes panes that report partly and a window should say so.
	SpendIncludesPartial bool `json:"spendIncludesPartial,omitempty"`
	// Caps are the helper cap use of the manager and of every pane with children.
	Caps []graphCap `json:"caps"`
	// Needs is the needs-you list of the team, most urgent first, and
	// NeedsHidden counts the items cut by its bound.
	Needs       []team.NeedsItem `json:"needs"`
	NeedsHidden int              `json:"needsHidden,omitempty"`
}

// The kinds of graphEdge.
const (
	graphEdgeSpawn   = "spawn"
	graphEdgeMessage = "message"
	// graphEdgeSent is a SendMessage call seen between two Claude sessions. It is
	// drawn as sent and never as read: Flockdeck does not carry the message.
	graphEdgeSent = "sent"
)

// graphEdge is a link between two panes: "spawn" from a parent to a helper it
// started, or "message" from a sender to a recipient.
type graphEdge struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
	// Message edges carry the message id, its time and the first line of it.
	ID   int    `json:"id,omitempty"`
	Time string `json:"time,omitempty"`
	Text string `json:"text,omitempty"`
	// Delivery is the newest delivery state of a message: queued, notified,
	// woken or read.
	Delivery string `json:"delivery,omitempty"`
}

// graphMsg is the graph view of a project: what graphSnapshot is answered with
// and what a window subscribed with graphWatch is pushed.
type graphMsg struct {
	Type    string          `json:"type"`
	Root    string          `json:"root"`
	Project string          `json:"project"`
	Teams   []graphTeamView `json:"teams"`
	Nodes   []graphNodeView `json:"nodes"`
	Edges   []graphEdge     `json:"edges"`
	// Solo lists the panes in no team, and SoloNeeds what needs the person among
	// them.
	Solo        []string         `json:"solo"`
	SoloNeeds   []team.NeedsItem `json:"soloNeeds"`
	NeedsHidden int              `json:"soloNeedsHidden,omitempty"`
	// BoardError is why the board could not be read, if it could not.
	BoardError string `json:"boardError,omitempty"`
	// SendMessageUnread counts the SendMessage calls of the project's panes that
	// were seen and not understood, so their arrows are missing.
	SendMessageUnread int `json:"sendMessageUnread,omitempty"`
	// PanesHidden counts the panes left out because a push holds only
	// maxGraphPanes nodes.
	PanesHidden int `json:"panesHidden,omitempty"`
}

// graphLine is the first non-empty line of s, cleaned and cut to n characters.
func graphLine(s string, n int) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return team.PlainText(l, n)
		}
	}
	return ""
}

// graphPrompt is the text of the question or permission prompt a pane is
// waiting on. It is the same view paneView carries (waitingViews), reduced to
// one line; the clipping happens in team.PlainText.
func graphPrompt(ask *askView, perm *permissionView) (text, kind string, ok bool) {
	switch {
	case ask != nil:
		for _, q := range ask.Questions {
			if q.Question != "" {
				return q.Question, team.PromptAsk, true
			}
		}
		return "a question", team.PromptAsk, true
	case perm != nil:
		switch {
		case perm.Command != "":
			return perm.Command, team.PromptPermission, true
		case perm.Summary != "":
			return perm.Summary, team.PromptPermission, true
		}
		return perm.Tool, team.PromptPermission, true
	}
	return "", "", false
}

// graphReviewerRole is the role that would be told a task is ready for review
// (see tasks.notify): the first of reviewer and tester with a live pane other
// than the owner's. Empty when there is none.
func graphReviewerRole(members []team.Member, owner string) string {
	for _, role := range []string{"reviewer", "tester"} {
		for _, m := range members {
			if m.Role == role && m.Pane != owner && !m.Exited() {
				return role
			}
		}
	}
	return ""
}

// graphStatic is the part of a graph that comes from the task board and the
// inboxes: it changes when they do, not with every tool call, so it is kept
// until the board file, the inboxes or the set of agents and their roles change.
type graphStatic struct {
	stamp string
	tasks []team.GraphTask
	moves []team.TaskMove
	chips map[string]graphTaskChip
	// givenTask is, for each team a task was given with taskTeam, the first open
	// such task, else the first. ownTask is the first open task each pane owns.
	givenTask   map[string]string
	ownTask     map[string]string
	unreadSince map[string]time.Time
	// unreadMail counts a pane's unread messages that an agent or the person
	// wrote. Flockdeck's own notices (a helper finished its turn) are left out:
	// they never make a pane need the person.
	unreadMail map[string]int
	// undelivered counts, for each sending pane, the messages to another agent of
	// the project that are still only queued. It is not bounded like edges.
	undelivered map[string]int
	// edges are the last maxGraphEdges messages between agents of the project.
	edges      []graphEdge
	boardError string
}

// graphStaticFor returns the board and message part of the graph of the project
// at root, rebuilding it only when its stamp changed. The stamp is the board
// file's stamp, the inboxes' revision, and the agents with their roles and
// whether they are gone, which is what the reviewer role and the message edges
// depend on. It must run on the workspace goroutine.
func (s *Server) graphStaticFor(root string, members []team.Member) *graphStatic {
	ws := s.ws
	var k strings.Builder
	board := "none"
	b, berr := ws.TeamBoard(root)
	if berr == nil {
		board = b.Stamp()
	}
	fmt.Fprintf(&k, "%s#%d#", board, ws.TeamMessageRev())
	for _, m := range members {
		fmt.Fprintf(&k, "%s|%s|%v;", m.Pane, m.Role, m.Exited())
	}
	stamp := k.String()
	if c := s.graphStatics[root]; c != nil && c.stamp == stamp {
		return c
	}
	s.graphBuilds++
	st := &graphStatic{stamp: stamp, chips: map[string]graphTaskChip{}, givenTask: map[string]string{}, ownTask: map[string]string{}, unreadSince: map[string]time.Time{}, unreadMail: map[string]int{}, undelivered: map[string]int{}}

	var board2 []tasks.Task
	if berr != nil {
		st.boardError = berr.Error()
	} else if l, _, err := b.List(tasks.Filter{All: true}); err != nil {
		st.boardError = err.Error()
	} else {
		board2 = l
	}
	chipOf := map[string]*tasks.Task{}
	openIDs := map[string]bool{}
	for i := range board2 {
		t := &board2[i]
		g := team.GraphTask{ID: t.ID, State: string(t.State), OwnerRole: t.OwnerRole, OwnerPane: t.OwnerPane, Title: t.Title}
		if n := len(t.Notes); n > 0 {
			g.LastNote = t.Notes[n-1].Text
		}
		if t.State == tasks.InReview {
			g.ReviewerRole = graphReviewerRole(members, t.OwnerPane)
		}
		st.tasks = append(st.tasks, g)
		if t.Team != "" {
			if cur, ok := st.givenTask[t.Team]; !ok || t.State.Open() && !openIDs[cur] {
				st.givenTask[t.Team] = t.ID
			}
			openIDs[t.ID] = t.State.Open()
		}
		if t.OwnerPane != "" && t.State.Open() && st.ownTask[t.OwnerPane] == "" {
			st.ownTask[t.OwnerPane] = t.ID
		}
		for _, e := range t.History {
			from, to, ok := parseMove(e.What)
			if !ok {
				continue
			}
			// The note a move came with is written with the same time and by the
			// same actor as the history entry; no other note is its note.
			note := ""
			for _, n := range t.Notes {
				if n.Time.Equal(e.Time) && n.Author == e.Actor {
					note = n.Text
					break
				}
			}
			st.moves = append(st.moves, team.TaskMove{Task: t.ID, From: from, To: to, Time: e.Time, Note: note})
		}
		if t.OwnerPane != "" && t.State.Open() {
			if cur := chipOf[t.OwnerPane]; cur == nil || chipRank(t.State) < chipRank(cur.State) {
				chipOf[t.OwnerPane] = t
			}
		}
	}
	for pane, t := range chipOf {
		st.chips[pane] = graphTaskChip{ID: t.ID, Title: team.PlainText(t.Title, 120), State: string(t.State)}
	}

	msgs, _, err := ws.TeamMessages(root)
	if err != nil && st.boardError == "" {
		st.boardError = err.Error()
	}
	agent := map[string]bool{}
	for _, m := range members {
		agent[m.Pane] = true
	}
	for _, m := range msgs {
		if !m.Read && !m.System() {
			st.unreadMail[m.To]++
			if t, ok := st.unreadSince[m.To]; !ok || m.Time.Before(t) {
				st.unreadSince[m.To] = m.Time
			}
		}
	}
	for _, m := range msgs {
		if m.From.Pane != "" && m.From.Pane != m.To && agent[m.From.Pane] && agent[m.To] && m.Delivery() == "queued" {
			st.undelivered[m.From.Pane]++
		}
	}
	// The last messages between panes, newest first. A message from the person
	// or from Flockdeck has no sending pane and is not an edge.
	for i := len(msgs) - 1; i >= 0 && len(st.edges) < maxGraphEdges; i-- {
		m := msgs[i]
		if m.From.Pane == "" || !agent[m.From.Pane] || !agent[m.To] {
			continue
		}
		st.edges = append(st.edges, graphEdge{Kind: graphEdgeMessage, From: m.From.Pane, To: m.To, ID: m.ID,
			Time: m.Time.Format(time.RFC3339), Text: graphLine(m.Text, graphEdgeCut), Delivery: m.Delivery()})
	}
	if s.graphStatics == nil {
		s.graphStatics = map[string]*graphStatic{}
	}
	s.graphStatics[root] = st
	return st
}

// graphPayload reads the project at root, whole. The pane part is read each
// time; the board and message part comes from graphStaticFor. It must run on the
// workspace goroutine.
func (s *Server) graphPayload(root string) (graphMsg, error) {
	ws := s.ws
	root, err := ws.TeamRoot(root)
	if err != nil {
		return graphMsg{}, err
	}
	panes, err := ws.TeamPanes(root)
	if err != nil {
		return graphMsg{}, err
	}
	out := graphMsg{Type: "graph", Root: root, Teams: []graphTeamView{}, Nodes: []graphNodeView{},
		Edges: []graphEdge{}, Solo: []string{}, SoloNeeds: []team.NeedsItem{}}
	if len(panes) > maxGraphPanes {
		out.PanesHidden = len(panes) - maxGraphPanes
		panes = panes[:maxGraphPanes]
	}
	members, err := ws.TeamMembers(root)
	if err != nil {
		return graphMsg{}, err
	}
	for _, p := range ws.Projects() {
		if p.Root == root {
			out.Project = p.Name
		}
	}
	st := s.graphStaticFor(root, members)
	out.BoardError = st.boardError

	now := s.graphClock()
	gp := make([]team.GraphPane, 0, len(panes))
	byID := map[string]*workspace.Pane{}
	live := map[string]string{}
	rts := map[string]workspace.PaneRuntime{}
	for _, p := range panes {
		byID[p.ID] = p
		rt := ws.Runtime(p)
		rts[p.ID] = rt
		// One read of the status serves the pane, its live line and its prompt.
		stat, detail := rt.Status()
		since := rt.StatusSince()
		g := team.GraphPane{
			ID: p.ID, Parent: p.Parent, Role: p.Role, Name: p.Name, Peer: ws.PeerNameOf(p), Label: ws.LabelOf(p), Agent: p.Agent,
			Shell: !p.IsAgent(), Status: stat.String(), Activity: string(rt.Activity()),
			StatusSince: since, Unread: ws.Unread(p.ID), UnreadSince: st.unreadSince[p.ID],
			UnreadMail: min(st.unreadMail[p.ID], ws.Unread(p.ID)),
			Exited:     stat == session.StatusExited,
		}
		if q, ok := rt.(interface{ LastOutput() time.Time }); ok {
			g.LastOutput = q.LastOutput()
		}
		switch why, failed := p.Failed(); {
		case p.Err != nil:
			// A pane that could not start has no session and nothing to run.
			g.Failed, g.FailureReason = true, team.PlainText(p.Err.Error(), 120)
		case failed && g.Shell:
			// A shell that ends with a status after a command the person typed
			// is not a fault, so it is not listed as down.
		case failed:
			g.Failed, g.FailureReason = true, team.PlainText(why, 120)
		}
		if stat == session.StatusBlocked {
			if su, ok := rt.Setup(); ok {
				g.SetupText = su.Text
			}
		}
		if stat == session.StatusWaiting {
			on := rt.Waiting()
			a, perm := waitingViews(on.Detail, on.ToolInput)
			g.PromptText, g.PromptKind, g.HasPrompt = graphPrompt(a, perm)
		}
		if detail != "" && stat != session.StatusExited {
			live[p.ID] = graphLine(detail, graphLiveCut)
		}
		gp = append(gp, g)
	}

	// The radar's pairs, once for each, with both panes in the project.
	var conflicts []team.PaneConflict
	for _, p := range panes {
		for _, c := range p.Conflicts {
			if byID[c.With] != nil {
				conflicts = append(conflicts, team.PaneConflict{A: p.ID, B: c.With, Paths: c.Paths})
			}
		}
	}

	graph := team.BuildGraph(gp, st.tasks)
	needs := team.NeedsYou(graph, gp, st.tasks, conflicts, st.moves, now)

	gpByID := map[string]team.GraphPane{}
	for _, g := range gp {
		gpByID[g.ID] = g
	}
	// Each pane's spend, read once for its node and for its team's total.
	type paneSpendFigure struct {
		usd               float64
		partial, includes bool
	}
	figures := map[string]paneSpendFigure{}
	for _, g := range gp {
		var f paneSpendFigure
		v := s.paneSpend(g.ID, rts[g.ID], now)
		if v != nil && v.Partial {
			f.includes = true
		}
		// A figure that is not finite or is past what a session can cost is
		// not added, so no pane can make a total overflow.
		if v != nil && v.USD > 0 && v.USD <= spend.MaxSessionCost && !v.Unpriced {
			f.usd = v.USD
		} else if rts[g.ID].HasRun() && g.Status != session.StatusStarting.String() {
			// An agent that has run and has no priced figure makes a total a
			// floor. A shell, and an agent that has not started, report none by
			// nature.
			f.partial = true
		}
		figures[g.ID] = f
	}
	stuck, below := graphRollup(graph, needs)
	for _, n := range graph.Nodes {
		p, g := byID[n.Pane], gpByID[n.Pane]
		nv := graphNodeView{
			GraphNode: n, Kind: string(rts[n.Pane].Transport()), Name: team.PlainText(g.Name, graphNameCut), Peer: team.PlainText(g.Peer, graphPeerCut), Label: team.PaneLabelText(g.Label), Role: g.Role, Agent: team.PlainText(g.Agent, graphAgentCut), Shell: g.Shell,
			Status: g.Status, Activity: g.Activity, Branch: team.PlainText(p.Branch, graphBranchCut), Live: live[n.Pane],
			Dirty: p.Git.Dirty, Untracked: p.Git.Untracked, Ahead: p.Git.Ahead, Behind: p.Git.Behind,
			Unread: g.Unread, AutoReview: p.AutoReview, AutoApproved: ws.AutoApprovedOf(p),
		}
		if !g.StatusSince.IsZero() {
			nv.Since = g.StatusSince.Format(time.RFC3339)
		}
		f := figures[n.Pane]
		nv.SpendUSD, nv.SpendPartial, nv.SpendIncludesPartial = f.usd, f.partial, f.includes
		nv.Undelivered = st.undelivered[n.Pane]
		if !g.LastOutput.IsZero() {
			nv.LastOutput = g.LastOutput.Format(time.RFC3339)
		}
		nv.Stuck = stuck[n.Pane]
		nv.Below, nv.NeedYou = below(n.Pane)
		if nv.Live == "" && !g.Shell {
			if last, ok := rts[p.ID].LastReply(); ok {
				nv.Live = graphLine(last.Text, graphLiveCut)
			}
		}
		if chip, ok := st.chips[n.Pane]; ok {
			c := chip
			nv.Task = &c
		}
		out.Nodes = append(out.Nodes, nv)
		if n.Parent != "" {
			out.Edges = append(out.Edges, graphEdge{Kind: graphEdgeSpawn, From: n.Parent, To: n.Pane})
		}
	}
	out.Edges = append(out.Edges, st.edges...)
	out.Edges, out.SendMessageUnread = appendSentEdges(out.Edges, ws.Taps(), out.Nodes)
	out.Solo = graph.Solo
	if n, ok := needs[""]; ok {
		out.SoloNeeds, out.NeedsHidden = n.Items, n.Hidden
	}

	for _, tm := range graph.Teams {
		rootPane := byID[tm.Root]
		tv := graphTeamView{ID: tm.ID, Manager: tm.Root, Members: tm.Members, Caps: []graphCap{}, Needs: []team.NeedsItem{}}
		tv.Goal = team.PlainText(rootPane.Task, graphGoalCut)
		if id := st.givenTask[tm.ID]; id != "" {
			tv.TaskID = id
		} else {
			tv.TaskID = st.ownTask[tm.Root]
		}
		if at := rts[tm.Root].StartedAt(); !at.IsZero() {
			tv.Started = at.Format(time.RFC3339)
		}
		for _, id := range tm.Members {
			f := figures[id]
			tv.SpendIncludesPartial = tv.SpendIncludesPartial || f.includes
			tv.SpendUSD += f.usd
			tv.SpendPartial = tv.SpendPartial || f.partial
			if n, _ := graph.Node(id); id == tm.Root || len(n.Children) > 0 {
				tv.Caps = append(tv.Caps, graphCap{Pane: id, Used: ws.HelpersBelow(id), Max: workspace.MaxHelpers})
			}
		}
		if n, ok := needs[tm.ID]; ok {
			tv.Needs, tv.NeedsHidden = n.Items, n.Hidden
		}
		out.Teams = append(out.Teams, tv)
	}
	return out, nil
}

// graphRollup is the set of panes the stuck rule flags and a function that, for
// a pane, counts the panes below it and how many of those are waiting on the
// person. A quiet item (old unread mail, a pane gone quiet) does not ask
// anything of the person.
func graphRollup(graph team.Graph, needs map[string]team.TeamNeeds) (stuck map[string]bool, below func(root string) (count, asking int)) {
	needers := map[string]bool{}
	stuck = map[string]bool{}
	for _, tn := range needs {
		for _, it := range tn.Items {
			if team.IsStuck(it.Kind) {
				stuck[it.Pane] = true
			}
			if team.AsksPerson(it.Kind) {
				needers[it.Pane] = true
				if it.Other != "" {
					needers[it.Other] = true
				}
			}
		}
	}
	below = func(root string) (count, asking int) {
		seen := map[string]bool{root: true}
		stack := []string{root}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			n, _ := graph.Node(id)
			for _, c := range n.Children {
				if seen[c] {
					continue
				}
				seen[c] = true
				count++
				if needers[c] {
					asking++
				}
				stack = append(stack, c)
			}
		}
		return count, asking
	}
	return stuck, below
}

// chipRank orders the tasks of a pane for the one its node names: the one in
// progress, else in review, else assigned, else blocked.
func chipRank(s tasks.State) int {
	switch s {
	case tasks.InProgress:
		return 0
	case tasks.InReview:
		return 1
	case tasks.Assigned:
		return 2
	case tasks.Blocked:
		return 3
	}
	return 4
}

// parseMove reads the history entry the board writes for a state change,
// "moved the task from A to B" (see tasks.Board).
func parseMove(what string) (from, to string, ok bool) {
	rest, found := strings.CutPrefix(what, "moved the task from ")
	if !found {
		return "", "", false
	}
	from, to, found = strings.Cut(rest, " to ")
	return from, to, found
}

// graphData is the encoded graph of the project at root. It must run on the
// workspace goroutine.
func (s *Server) graphData(root string) (resolved string, data []byte, err error) {
	msg, err := s.graphPayload(root)
	if err != nil {
		return "", nil, err
	}
	data, err = json.Marshal(msg)
	return msg.Root, data, err
}

// graphSnapshot answers with the whole graph of the project.
func (s *Server) graphSnapshot(c *controlClient, cmd command) {
	type out struct {
		data []byte
		err  error
	}
	r, ok := ask(s, func() out {
		_, data, err := s.graphData(cmd.Root)
		return out{data, err}
	})
	if !ok {
		return
	}
	if r.err != nil {
		refuse(c, cmd, r.err)
		return
	}
	c.send(r.data)
}

// graphWatch subscribes the window to the graph of the project: it is sent the
// graph now and again whenever it changes, at most every teamInterval. One
// project at a time; watching another replaces it. This is independent of
// teamWatch.
func (s *Server) graphWatch(c *controlClient, cmd command) {
	s.do(func() {
		root, data, err := s.graphData(cmd.Root)
		if err != nil {
			c.graphRoot, c.graphSent = "", nil
			refuse(c, cmd, err)
			return
		}
		c.graphRoot, c.graphSent = root, nil
		if c.trySend(data) {
			c.graphSent = data
			// This counts as a push, so a change in the next interval waits for it
			// instead of making a second one at once. Nothing is armed for it: a
			// change arms its own push, and the tick covers the clock.
			s.graphAt = time.Now()
		} else {
			// The window's queue was full, so it has not been sent the graph.
			s.armGraph()
		}
		s.dropUnwatchedStatics()
		s.armGraphTick()
	})
}

// armGraph arranges for a push in a moment, once.
func (s *Server) armGraph() {
	if s.graphArmed {
		return
	}
	s.graphArmed = true
	wait := max(teamInterval-time.Since(s.graphAt), 50*time.Millisecond)
	time.AfterFunc(wait, func() {
		s.do(func() {
			s.graphArmed = false
			s.pushGraph()
		})
	})
}

// pushGraph sends every window subscribed to a graph its project's graph when
// it has changed, at most every teamInterval; a push that comes too soon is made
// when the interval is up. It must run on the workspace goroutine, and is called
// with each state push. A window whose queue was full is sent the graph again
// rather than the push being skipped as unchanged, and a project that can no
// longer be read is told to the window once, as teamGone.
func (s *Server) pushGraph() {
	watching := false
	clients := s.greetedClients()
	for _, c := range clients {
		if c.graphRoot != "" {
			watching = true
		}
	}
	if !watching {
		if !s.relayWatching {
			s.graphStatics = nil
		}
		return
	}
	s.dropUnwatchedStatics()
	s.armGraphTick()
	if time.Since(s.graphAt) < teamInterval {
		s.armGraph()
		return
	}
	s.graphAt = time.Now()
	type built struct {
		data []byte
		err  error
	}
	cache := map[string]built{}
	retry := false
	for _, c := range clients {
		if c.graphRoot == "" {
			continue
		}
		b, ok := cache[c.graphRoot]
		if !ok {
			_, data, err := s.graphData(c.graphRoot)
			b = built{data, err}
			cache[c.graphRoot] = b
		}
		data := b.data
		if b.err != nil {
			data = teamGone(c.graphRoot, b.err)
		}
		if bytes.Equal(data, c.graphSent) {
			continue
		}
		if c.trySend(data) {
			c.graphSent = data
		} else {
			retry = true
		}
	}
	if retry {
		s.armGraph()
	}
}

// dropUnwatchedStatics forgets the board and message part of every project no
// window watches, so a project the last window moved away from does not stay
// in memory until nothing is watched at all. It must run on the workspace
// goroutine.
func (s *Server) dropUnwatchedStatics() {
	if len(s.graphStatics) == 0 {
		return
	}
	watched := map[string]bool{}
	for _, c := range s.greetedClients() {
		if c.graphRoot != "" {
			watched[c.graphRoot] = true
		}
	}
	// The relay form of the teams reads every project.
	if s.relayWatching {
		for _, p := range s.ws.Projects() {
			watched[p.Root] = true
		}
	}
	for root := range s.graphStatics {
		if !watched[root] {
			delete(s.graphStatics, root)
		}
	}
}

// graphTickInterval is how often a watched graph is looked at when nothing else
// has woken the server. Some of what it shows depends on the clock alone: mail
// that has waited thirty minutes in an idle pane, and a returned task that is
// older than a day. A server holds its own, graphTickEvery, which a test sets.
const graphTickInterval = 30 * time.Second

// graphClock is the time the graph is read at: the real one, unless a test has
// set graphNow. It must run on the workspace goroutine.
func (s *Server) graphClock() time.Time {
	if s.graphNow != nil {
		return s.graphNow()
	}
	return time.Now()
}

// armGraphTick arranges for a look at the graph in graphTickEvery, once.
func (s *Server) armGraphTick() {
	if s.graphTickArmed {
		return
	}
	s.graphTickArmed = true
	time.AfterFunc(s.graphTickEvery, func() {
		s.do(func() {
			s.graphTickArmed = false
			s.pushGraph()
		})
	})
}

// graphCommand answers the graph commands. A window reached through the relay
// is refused all of them, as it is for the Team panel. Decision: the phone scope
// of the graph is not decided, so the desk is the only place it is shown; this
// refusal is the one place to lift when it is. The graph message is dropped for
// a relay window as well (relayscrub.go): a node's live line is a pane's last
// reply and an edge holds the text of a message. If a later change opens the
// graph to the relay, it must first write a scrub for that text, with the
// sealed channel the plan asks for.
// Plan v8 locks this: structured (ACP) panes stay desktop-only until a sealed
// team socket exists; a terminal pane can be approved from a phone only through
// the drawer's choice buttons on the sealed terminal path; merges are desk-only.
func (s *Server) graphCommand(c *controlClient, cmd command) {
	if c.remote {
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, Error: teamDeskOnly, Code: teamDeskOnlyCode})
		c.notify(teamDeskOnly, true)
		return
	}
	switch cmd.Cmd {
	case "graphSnapshot":
		s.graphSnapshot(c, cmd)
	case "graphWatch":
		s.graphWatch(c, cmd)
	case "graphUnwatch":
		s.do(func() {
			c.graphRoot, c.graphSent = "", nil
			s.dropUnwatchedStatics()
		})
	}
}

// appendSentEdges adds an edge for each SendMessage call seen between two panes
// of the graph, newest first, at most maxGraphEdges, and counts the calls of
// the graph's panes that were not understood.
func appendSentEdges(edges []graphEdge, taps []team.Tap, nodes []graphNodeView) ([]graphEdge, int) {
	in := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		in[n.Pane] = true
	}
	unread, added := 0, 0
	for i := len(taps) - 1; i >= 0; i-- {
		t := taps[i]
		if !in[t.From] {
			continue
		}
		if !t.Understood {
			unread++
			continue
		}
		if t.To == "" || !in[t.To] || added >= maxGraphEdges {
			continue
		}
		edges = append(edges, graphEdge{Kind: graphEdgeSent, From: t.From, To: t.To, Time: t.Time.Format(time.RFC3339)})
		added++
	}
	return edges, unread
}
