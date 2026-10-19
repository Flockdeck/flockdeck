package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
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
	// maxGraphPanes bounds the nodes in a push. A project has no more panes
	// than a window can open, so this only guards against a runaway.
	maxGraphPanes = 500
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
	Name     string `json:"name"`
	Peer     string `json:"peer,omitempty"`
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
	// SpendUSD is the estimated spend of the team's panes that reported one.
	// SpendPartial says some pane reported none (or an unpriced one), so the
	// total is a floor.
	SpendUSD     float64 `json:"spendUSD"`
	SpendPartial bool    `json:"spendPartial,omitempty"`
	// Caps are the helper cap use of the manager and of every pane with children.
	Caps []graphCap `json:"caps"`
	// Needs is the needs-you list of the team, most urgent first, and
	// NeedsHidden counts the items cut by its bound.
	Needs       []team.NeedsItem `json:"needs"`
	NeedsHidden int              `json:"needsHidden,omitempty"`
}

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

// graphPayload reads the project at root, whole. It must run on the workspace
// goroutine.
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
	if len(panes) > maxGraphPanes {
		panes = panes[:maxGraphPanes]
	}
	members, err := ws.TeamMembers(root)
	if err != nil {
		return graphMsg{}, err
	}
	msgs, _, err := ws.TeamMessages(root)
	if err != nil {
		return graphMsg{}, err
	}
	out := graphMsg{Type: "graph", Root: root, Teams: []graphTeamView{}, Nodes: []graphNodeView{},
		Edges: []graphEdge{}, Solo: []string{}, SoloNeeds: []team.NeedsItem{}}
	for _, p := range ws.Projects() {
		if p.Root == root {
			out.Project = p.Name
		}
	}

	// The oldest unread message of each pane, for the stale-unread rule.
	unreadSince := map[string]time.Time{}
	for _, m := range msgs {
		if m.Read {
			continue
		}
		if t, ok := unreadSince[m.To]; !ok || m.Time.Before(t) {
			unreadSince[m.To] = m.Time
		}
	}

	now := time.Now()
	gp := make([]team.GraphPane, 0, len(panes))
	byID := map[string]*workspace.Pane{}
	for _, p := range panes {
		byID[p.ID] = p
		st, detail := session.StatusStarting, ""
		var since time.Time
		if p.Sess != nil && p.Err == nil {
			st, detail = p.Sess.Status()
			since = p.Sess.StatusSince()
		} else {
			st = session.StatusExited
		}
		g := team.GraphPane{
			ID: p.ID, Role: p.Role, Name: p.Name, Peer: ws.PeerNameOf(p), Agent: p.Agent,
			Shell: !p.IsAgent(), Status: st.String(), Activity: string(workspace.PaneActivity(p)),
			StatusSince: since, Unread: ws.Unread(p.ID), UnreadSince: unreadSince[p.ID],
		}
		if p.Parent != "" {
			g.Parent = p.Parent
		}
		if _, failed := p.Failed(); failed {
			g.Failed = true
		}
		g.Exited = st == session.StatusExited
		if st == session.StatusWaiting && p.Sess != nil {
			a, perm := waitingViews(detail, p.Sess.ToolInput())
			g.PromptText, g.PromptKind, g.HasPrompt = graphPrompt(a, perm)
		}
		gp = append(gp, g)
	}

	// The board: the tasks, and the moves out of review.
	var gt []team.GraphTask
	var moves []team.TaskMove
	chips := map[string]*tasks.Task{}
	var board []tasks.Task
	if b, err := ws.TeamBoard(root); err != nil {
		out.BoardError = err.Error()
	} else if board, _, err = b.List(tasks.Filter{All: true}); err != nil {
		out.BoardError = err.Error()
	}
	for i := range board {
		t := &board[i]
		g := team.GraphTask{ID: t.ID, State: string(t.State), OwnerRole: t.OwnerRole, OwnerPane: t.OwnerPane, Title: t.Title}
		if n := len(t.Notes); n > 0 {
			g.LastNote = t.Notes[n-1].Text
		}
		if t.State == tasks.InReview {
			g.ReviewerRole = graphReviewerRole(members, t.OwnerPane)
		}
		gt = append(gt, g)
		for _, e := range t.History {
			from, to, ok := parseMove(e.What)
			if !ok {
				continue
			}
			note := ""
			for _, n := range t.Notes {
				if !n.Time.Before(e.Time) {
					note = n.Text
					break
				}
			}
			moves = append(moves, team.TaskMove{Task: t.ID, From: from, To: to, Time: e.Time, Note: note})
		}
		if t.OwnerPane != "" && t.State.Open() {
			if cur := chips[t.OwnerPane]; cur == nil || chipRank(t.State) < chipRank(cur.State) {
				chips[t.OwnerPane] = t
			}
		}
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

	graph := team.BuildGraph(gp, gt)
	needs := team.NeedsYou(graph, gp, gt, conflicts, moves, now)

	gpByID := map[string]team.GraphPane{}
	for _, g := range gp {
		gpByID[g.ID] = g
	}
	for _, n := range graph.Nodes {
		p, g := byID[n.Pane], gpByID[n.Pane]
		nv := graphNodeView{
			GraphNode: n, Name: g.Name, Peer: g.Peer, Role: g.Role, Agent: g.Agent, Shell: g.Shell,
			Status: g.Status, Activity: g.Activity, Branch: p.Branch,
			Dirty: p.Git.Dirty, Untracked: p.Git.Untracked, Ahead: p.Git.Ahead, Behind: p.Git.Behind,
			Unread: g.Unread, AutoReview: p.AutoReview, AutoApproved: ws.AutoApprovedOf(p),
		}
		if !g.StatusSince.IsZero() {
			nv.Since = g.StatusSince.Format(time.RFC3339)
		}
		if p.Sess != nil && p.Err == nil {
			if _, detail := p.Sess.Status(); detail != "" {
				nv.Live = graphLine(detail, graphLiveCut)
			}
		}
		if nv.Live == "" && !g.Shell {
			if last, ok := s.preview.get(p.ID); ok {
				nv.Live = graphLine(last.Text, graphLiveCut)
			}
		}
		if t := chips[n.Pane]; t != nil {
			nv.Task = &graphTaskChip{ID: t.ID, Title: team.PlainText(t.Title, 120), State: string(t.State)}
		}
		out.Nodes = append(out.Nodes, nv)
		if n.Parent != "" {
			out.Edges = append(out.Edges, graphEdge{Kind: "spawn", From: n.Parent, To: n.Pane})
		}
	}
	out.Solo = graph.Solo
	if n, ok := needs[""]; ok {
		out.SoloNeeds, out.NeedsHidden = n.Items, n.Hidden
	}

	for _, tm := range graph.Teams {
		root := byID[tm.Root]
		tv := graphTeamView{ID: tm.ID, Manager: tm.Root, Members: tm.Members, Caps: []graphCap{}, Needs: []team.NeedsItem{}}
		tv.Goal = team.PlainText(root.Task, graphGoalCut)
		if root.Sess != nil && !root.Sess.StartedAt().IsZero() {
			tv.Started = root.Sess.StartedAt().Format(time.RFC3339)
		}
		for _, id := range tm.Members {
			if v := s.book.Pane(id, now); v != nil && v.USD > 0 && !v.Unpriced {
				tv.SpendUSD += v.USD
			} else {
				tv.SpendPartial = true
			}
			if n, _ := graph.Node(id); id == tm.Root || len(n.Children) > 0 {
				tv.Caps = append(tv.Caps, graphCap{Pane: id, Used: ws.HelpersBelow(id), Max: workspace.MaxHelpers})
			}
		}
		if n, ok := needs[tm.ID]; ok {
			tv.Needs, tv.NeedsHidden = n.Items, n.Hidden
		}
		out.Teams = append(out.Teams, tv)
	}

	// The last messages between panes, newest first. A message from the person
	// or from Flockdeck has no sending pane and is not an edge.
	sent := 0
	for i := len(msgs) - 1; i >= 0 && sent < maxGraphEdges; i-- {
		m := msgs[i]
		if m.From.Pane == "" || byID[m.From.Pane] == nil || byID[m.To] == nil {
			continue
		}
		sent++
		out.Edges = append(out.Edges, graphEdge{Kind: "message", From: m.From.Pane, To: m.To, ID: m.ID,
			Time: m.Time.Format(time.RFC3339), Text: graphLine(m.Text, graphEdgeCut)})
	}
	return out, nil
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
		} else {
			s.armGraph()
		}
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
		return
	}
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

// graphCommand answers the graph commands. A window reached through the relay
// is refused all of them, as it is for the Team panel.
func (s *Server) graphCommand(c *controlClient, cmd command) {
	if c.remote {
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, Error: teamDeskOnly})
		c.notify(teamDeskOnly, true)
		return
	}
	switch cmd.Cmd {
	case "graphSnapshot":
		s.graphSnapshot(c, cmd)
	case "graphWatch":
		s.graphWatch(c, cmd)
	case "graphUnwatch":
		s.do(func() { c.graphRoot, c.graphSent = "", nil })
	}
}
