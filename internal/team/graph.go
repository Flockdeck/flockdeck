package team

import (
	"fmt"
	"strings"
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
	taskInReview = "in-review"
	taskBlocked  = "blocked"
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
	// Exited and Failed say the process is gone, and whether it ended in error.
	Exited bool
	Failed bool
	Unread int
}

// GraphTask is one task of the board.
type GraphTask struct {
	ID        string
	State     string
	OwnerRole string
	OwnerPane string
	// ReviewerPane and ReviewerRole say who the task waits on while it is in
	// review. Either may be empty.
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
	// Orphaned says the pane named a parent that is not in the snapshot, or sits
	// in a parent loop. It is treated as a root.
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
	// Nodes has one entry for each pane, in the order the panes were given.
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
	g := Graph{Teams: []GraphTeam{}, Nodes: make([]GraphNode, len(panes)), Solo: []string{}}
	index := make(map[string]int, len(panes))
	for i, p := range panes {
		if _, dup := index[p.ID]; !dup {
			index[p.ID] = i
		}
	}

	for i, p := range panes {
		n := &g.Nodes[i]
		n.Pane = p.ID
		if _, ok := index[p.Parent]; ok && p.Parent != "" && p.Parent != p.ID {
			n.Parent = p.Parent
		} else if p.Parent != "" {
			n.Orphaned = true
		}
	}
	for i := range g.Nodes {
		if parent := g.Nodes[i].Parent; parent != "" && index[g.Nodes[i].Pane] == i {
			pi := index[parent]
			g.Nodes[pi].Children = append(g.Nodes[pi].Children, g.Nodes[i].Pane)
		}
	}

	placed := make([]bool, len(panes))
	for i, p := range panes {
		n := &g.Nodes[i]
		if n.Parent != "" || index[p.ID] != i {
			continue
		}
		placed[i] = true
		if len(n.Children) == 0 && p.Role != ManagerRole {
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
				placed[ci] = true
				queue = append(queue, ci)
			}
		}
		g.Teams = append(g.Teams, team)
	}

	// Panes in a parent loop, or repeating another pane's id, have no root
	// above them. Each is a root of its own and solo.
	for i := range g.Nodes {
		if !placed[i] {
			g.Nodes[i].Orphaned = true
			g.Nodes[i].Parent = ""
			g.Nodes[i].Depth = 0
		}
		if g.Nodes[i].Team == "" {
			g.Solo = append(g.Solo, g.Nodes[i].Pane)
		}
	}

	for i, p := range panes {
		g.Nodes[i].WaitingOn = waitingOn(p, g.Nodes[i], panes, index, tasks)
	}
	return g
}

func waitingOn(p GraphPane, n GraphNode, panes []GraphPane, index map[string]int, tasks []GraphTask) WaitingOn {
	if (p.Activity == activityWaiting || p.Activity == activityBlocked) && p.HasPrompt {
		return WaitingOn{Kind: WaitUser, Text: clip(p.PromptText, MaxWaitText)}
	}
	if p.Exited || p.Failed {
		text := "exited"
		if p.Failed {
			text = "failed"
		}
		return WaitingOn{Kind: WaitDown, Text: text}
	}
	if !p.Shell {
		for _, t := range tasks {
			if t.OwnerPane != p.ID || t.State != taskInReview {
				continue
			}
			if by := reviewerLabel(t, p.ID, panes, index); by != "" {
				return WaitingOn{Kind: WaitReviewer, Text: clip(t.ID+" is in review by "+by, MaxWaitText), Ref: t.ID}
			}
		}
		for _, t := range tasks {
			if t.OwnerPane == p.ID && t.State == taskBlocked {
				return WaitingOn{Kind: WaitTask, Text: clip(t.LastNote, MaxWaitText), Ref: t.ID}
			}
		}
	}
	busy, first := 0, ""
	for _, c := range n.Children {
		if a := panes[index[c]].Activity; a == activityWorking || a == activityWaiting {
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

// clip folds white space in s so a multi-line note reads as one line, then cuts
// it to at most max characters, ending in an ellipsis when it cut.
func clip(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
