package team

import (
	"fmt"
	"time"
)

// This file derives the team graph from a snapshot of one project's panes and
// tasks. It reads nothing but its arguments: the caller fills the structs from
// the workspace, the task board and the pane views, and gets back teams, nodes
// and what each pane is waiting on.

// MaxWaitText is the most characters a waiting-on text keeps.
const MaxWaitText = 200

// Activity values the derivation looks at. They match workspace.Activity, which
// this package does not import.
const (
	activityWorking = "working"
	activityWaiting = "waiting"
	activityBlocked = "blocked"
)

// Task states the derivation looks at. They match tasks.State.
const (
	taskInReview   = "in-review"
	taskBlocked    = "blocked"
	taskInProgress = "in-progress"
)

// ManagerRole is the id of the role that leads a team.
const ManagerRole = "manager"

// Kinds of waiting-on, in the order they are tried.
const (
	WaitUser     = "user"
	WaitDown     = "down"
	WaitReviewer = "reviewer"
	WaitTask     = "task"
	WaitHelpers  = "helpers"
	WaitNone     = "none"
)

// GraphPane is one pane of the project as the caller sees it.
type GraphPane struct {
	ID string
	// Parent is the pane that started this one, empty for none.
	Parent string
	Role   string
	Name   string
	Peer   string
	Agent  string
	// Shell marks a plain terminal with no agent in it.
	Shell    bool
	Status   string
	Activity string
	// StatusSince is when Status began, zero if unknown.
	StatusSince time.Time
	// HasPrompt says the pane has an ask or permission view, and PromptText is
	// the question or the command it shows.
	HasPrompt  bool
	PromptText string
	// PromptKind is PromptPermission for a permission prompt and PromptAsk for a
	// question. Empty is read as a question.
	PromptKind string
	// SetupText is the fix for a pane that cannot run until the person signs in
	// to its agent, installs Node, or installs the agent. It is empty for every
	// other pane. The caller reads it from the pane's runtime.
	SetupText string
	// Exited and Failed say the process is gone, and whether it ended in error.
	Exited bool
	Failed bool
	// FailureReason says why a failed pane failed, such as "exit code 2" or "was
	// killed", or why it could not start. Empty when unknown.
	FailureReason string
	// ExitCode is the process's exit code when Exited.
	ExitCode int
	// ClosedByUser says the person closed or killed the pane, so its exit is not a
	// fault. The caller sets it.
	ClosedByUser bool
	Unread       int
	// UnreadMail is Unread without the messages Flockdeck wrote itself (a pane
	// finishing its turn, exiting). Those are read by an agent when it next looks,
	// and are never a reason to bother the person.
	UnreadMail int
	// UnreadSince is when the oldest unread message from an agent or the person
	// arrived, zero if unknown.
	UnreadSince time.Time
}

// GraphTask is one task of the board.
type GraphTask struct {
	ID        string
	State     string
	OwnerRole string
	OwnerPane string
	// ReviewerRole says who the task waits on while it is in review. Review is
	// bound to a role ("reviewer" or "tester"), not to a pane, so this is the
	// usual input. ReviewerPane is set only when a pane has claimed or been
	// given the review. With both empty an in-review task still shows as
	// waiting on a reviewer, with text saying nobody is set to take it.
	ReviewerPane string
	ReviewerRole string
	Title        string
	LastNote     string
}

// WaitingOn is what a pane is held up by.
type WaitingOn struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	// Ref is a task id for reviewer and task, the first busy helper's pane id
	// for helpers, and empty otherwise.
	Ref string `json:"ref,omitempty"`
}

// GraphNode is one pane placed in the graph.
type GraphNode struct {
	Pane     string   `json:"pane"`
	Parent   string   `json:"parent,omitempty"`
	Children []string `json:"children,omitempty"`
	// Depth is 0 for a root and for a pane in no team.
	Depth int `json:"depth"`
	// Team is the id of the team the pane is in, empty for a solo pane.
	Team string `json:"team,omitempty"`
	// Orphaned says the pane named a parent that is not in the snapshot, or is
	// where a parent loop was cut. It is treated as a root.
	Orphaned  bool      `json:"orphaned,omitempty"`
	WaitingOn WaitingOn `json:"waitingOn"`
}

// GraphTeam is a root pane and every pane below it.
type GraphTeam struct {
	// ID is the root pane's id.
	ID   string `json:"id"`
	Root string `json:"root"`
	// Members lists the panes in the team, root first, then breadth first.
	Members []string `json:"members"`
}

// Graph is the derived model.
type Graph struct {
	Teams []GraphTeam `json:"teams"`
	// Nodes has one entry for each pane id, in the order the panes were given.
	Nodes []GraphNode `json:"nodes"`
	// Solo lists the panes that are in no team.
	Solo []string `json:"solo"`
}

// Node returns the node for a pane.
func (g Graph) Node(pane string) (GraphNode, bool) {
	for _, n := range g.Nodes {
		if n.Pane == pane {
			return n, true
		}
	}
	return GraphNode{}, false
}

// BuildGraph derives the teams, nodes and waiting-on kinds of one project. It
// does not change its arguments.
//
// A team is a root pane and every pane below it by parent. A root is a pane
// with no parent, or whose parent is not in the snapshot, that has children or
// the manager role. Other panes with no team are solo.
func BuildGraph(panes []GraphPane, tasks []GraphTask) Graph {
	// A pane id counts once: the first pane with it is kept.
	seen := make(map[string]bool, len(panes))
	uniq := make([]GraphPane, 0, len(panes))
	for _, p := range panes {
		if !seen[p.ID] {
			seen[p.ID] = true
			uniq = append(uniq, p)
		}
	}
	panes = uniq

	g := Graph{Teams: []GraphTeam{}, Nodes: make([]GraphNode, len(panes)), Solo: []string{}}
	index := make(map[string]int, len(panes))
	for i, p := range panes {
		index[p.ID] = i
	}
	for i, p := range panes {
		n := &g.Nodes[i]
		n.Pane = p.ID
		if _, ok := index[p.Parent]; ok && p.Parent != p.ID {
			n.Parent = p.Parent
		} else if p.Parent != "" {
			n.Orphaned = true
		}
	}
	breakLoops(g.Nodes, index)

	for i := range g.Nodes {
		if parent := g.Nodes[i].Parent; parent != "" {
			pi := index[parent]
			g.Nodes[pi].Children = append(g.Nodes[pi].Children, g.Nodes[i].Pane)
		}
	}

	for i, p := range panes {
		n := &g.Nodes[i]
		if n.Parent != "" || len(n.Children) == 0 && p.Role != ManagerRole {
			continue
		}
		team := GraphTeam{ID: p.ID, Root: p.ID}
		queue := []int{i}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			g.Nodes[cur].Team = p.ID
			team.Members = append(team.Members, g.Nodes[cur].Pane)
			for _, c := range g.Nodes[cur].Children {
				ci := index[c]
				g.Nodes[ci].Depth = g.Nodes[cur].Depth + 1
				queue = append(queue, ci)
			}
		}
		g.Teams = append(g.Teams, team)
	}
	for i := range g.Nodes {
		if g.Nodes[i].Team == "" {
			g.Solo = append(g.Solo, g.Nodes[i].Pane)
		}
	}

	for i, p := range panes {
		g.Nodes[i].WaitingOn = waitingOn(p, g.Nodes[i], panes, index, tasks)
	}
	return g
}

// breakLoops cuts every parent loop at the first of its panes in input order,
// which becomes an orphaned root, so each pane has a root above it.
func breakLoops(nodes []GraphNode, index map[string]int) {
	const (
		unseen = iota
		onPath
		done
	)
	state := make([]int, len(nodes))
	for start := range nodes {
		var path []int
		i := start
		for i >= 0 && state[i] == unseen {
			state[i] = onPath
			path = append(path, i)
			if nodes[i].Parent == "" {
				i = -1
			} else {
				i = index[nodes[i].Parent]
			}
		}
		if i >= 0 && state[i] == onPath {
			// The loop is the path from i on. Cut it at its lowest input index.
			first := i
			for j := len(path) - 1; path[j] != i; j-- {
				first = min(first, path[j])
			}
			nodes[first].Parent = ""
			nodes[first].Orphaned = true
		}
		for _, j := range path {
			state[j] = done
		}
	}
}

// failedText is "failed", with the reason after it when it is known.
func failedText(p GraphPane) string {
	if p.FailureReason == "" {
		return "failed"
	}
	return PlainText("failed: "+p.FailureReason, MaxWaitText)
}

func waitingOn(p GraphPane, n GraphNode, panes []GraphPane, index map[string]int, tasks []GraphTask) WaitingOn {
	if p.Exited || p.Failed {
		text := "exited"
		if p.Failed {
			text = failedText(p)
		}
		return WaitingOn{Kind: WaitDown, Text: text, Ref: p.ID}
	}
	if (p.Activity == activityWaiting || p.Activity == activityBlocked) && p.HasPrompt {
		return WaitingOn{Kind: WaitUser, Text: PlainText(p.PromptText, MaxWaitText)}
	}
	if p.SetupText != "" {
		return WaitingOn{Kind: WaitUser, Text: PlainText(p.SetupText, MaxWaitText)}
	}
	if !p.Shell {
		for _, t := range tasks {
			if t.OwnerPane != p.ID || t.State != taskInReview {
				continue
			}
			text := t.ID + " is in review, and nobody is set to review it"
			if by := reviewerLabel(t, p.ID, panes, index); by != "" {
				text = t.ID + " is in review by " + by
			}
			return WaitingOn{Kind: WaitReviewer, Text: PlainText(text, MaxWaitText), Ref: t.ID}
		}
		for _, t := range tasks {
			if t.OwnerPane == p.ID && t.State == taskBlocked {
				return WaitingOn{Kind: WaitTask, Text: PlainText(t.LastNote, MaxWaitText), Ref: t.ID}
			}
		}
	}
	busy, first := 0, ""
	for _, c := range n.Children {
		h := panes[index[c]]
		if h.Exited || h.Failed {
			continue
		}
		if a := h.Activity; a == activityWorking || a == activityWaiting {
			if busy == 0 {
				first = c
			}
			busy++
		}
	}
	if busy > 0 {
		noun := "helpers are"
		if busy == 1 {
			noun = "helper is"
		}
		return WaitingOn{Kind: WaitHelpers, Text: fmt.Sprintf("%d %s busy", busy, noun), Ref: first}
	}
	return WaitingOn{Kind: WaitNone}
}

// reviewerLabel names who a task in review waits on, or returns "" when it
// names nobody but the owner itself.
func reviewerLabel(t GraphTask, owner string, panes []GraphPane, index map[string]int) string {
	if t.ReviewerPane != "" && t.ReviewerPane != owner {
		if i, ok := index[t.ReviewerPane]; ok {
			return paneLabel(panes[i])
		}
		return t.ReviewerPane
	}
	return t.ReviewerRole
}

func paneLabel(p GraphPane) string {
	if p.Peer != "" {
		return p.Peer
	}
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}
