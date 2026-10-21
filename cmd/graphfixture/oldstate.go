package main

import "fmt"

// The state push for a fixture. The tiled front end, and the new page's rail and
// panes' names, draw from it, so it is made to be what the desk would send for a
// team in the situation the graph describes (internal/server/control.go,
// snapshot and paneView), not a loose copy of the graph's fields:
//
//   - One project, with its own root as the only member, and one tab holding the
//     whole team. The tab is marked delegated, since a manager's helpers are
//     started into its tab, and every helper carries its parent, so the old page
//     does not fold the tab into a summary card (app.js, tabSettleInfo).
//   - A pane's kind is "agent", or "shell" for a node that is a shell. It is not
//     the graph's kind: the graph says "terminal" or "structured" for how the
//     agent is driven, which the desk sends as the separate structured flag.
//   - Counts and marks follow workspace.Tally: a pane that is waiting or blocked
//     counts as waiting, one that is working as working, nothing else counts.
//     The tab's and the project's activity, and the "attention" flag, come from
//     the same counts, as the desk derives them.
//   - A parent is sent only while the parent is a pane of the push.
//   - A terminal pane whose status is waiting carries a permission or an ask view,
//     made from the graph's item for it, as snapshot does. A structured pane's card does not come with the push.
//
// What the graph does not hold is made up and is the same on every run: the
// working directory (the root), the size of a terminal (80 by 24), and how the
// panes are tiled (the manager first, then the helpers, three to a row).

// tileColumns is how many panes a row of the tiling holds.
const tileColumns = 3

// activityRank is workspace.Activity's rank: waiting and blocked outrank working,
// and nothing else counts. A test pins it to workspace.
func activityRank(a string) int {
	switch a {
	case "waiting", "blocked":
		return 2
	case "working":
		return 1
	}
	return 0
}

// aggregateActivity is the activity of a group of panes given each one's own.
func aggregateActivity(acts []string) string {
	rank := 0
	for _, a := range acts {
		rank = max(rank, activityRank(a))
	}
	switch rank {
	case 2:
		return "waiting"
	case 1:
		return "working"
	}
	return ""
}

// nodeActivity is what a pane is drawn as doing: the node's activity, which is
// its status when the graph leaves it out.
func nodeActivity(n fixtureNode) string {
	if n.Activity != "" {
		return n.Activity
	}
	return n.Status
}

// tileTree lays the panes out as the tab's tree: rows of tileColumns, one under
// the next. A row of one pane is that pane's leaf, and so is a tree of one row.
func tileTree(panes []string) map[string]any {
	var rows []any
	for i := 0; i < len(panes); i += tileColumns {
		end := min(i+tileColumns, len(panes))
		var leaves []any
		for j := i; j < end; j++ {
			leaves = append(leaves, map[string]any{"id": fmt.Sprintf("n%d", j), "pane": panes[j], "weight": 1.0})
		}
		if len(leaves) == 1 {
			rows = append(rows, leaves[0])
			continue
		}
		rows = append(rows, map[string]any{"id": fmt.Sprintf("r%d", i/tileColumns), "dir": "h", "weight": 1.0, "children": leaves})
	}
	if len(rows) == 1 {
		if row, ok := rows[0].(map[string]any); ok {
			return row
		}
	}
	return map[string]any{"id": "root", "dir": "v", "weight": 1.0, "children": rows}
}

// paneState is one pane as the desk's paneView would send it. views are the
// permission and ask text by pane, from the graph's items.
func (f *fixture) paneState(n fixtureNode, perms, asks map[string]string) map[string]any {
	act := nodeActivity(n)
	kind := "agent"
	if n.Shell {
		kind = "shell"
	}
	agent := n.Agent
	if agent == "" {
		agent = "claude"
	}
	structured := n.Kind == "structured"
	p := map[string]any{
		"id": n.Pane, "kind": kind, "agent": agent, "name": n.Name, "cwd": f.root, "branch": n.Branch,
		"status": n.Status, "activity": act, "detail": n.Live,
		"cols": 80, "rows": 24,
	}
	if structured {
		p["structured"] = true
	}
	if n.Role != "" {
		p["role"] = n.Role
	}
	if n.Parent != "" && f.has(n.Parent) {
		p["parent"] = n.Parent
	}
	if n.Since != "" {
		p["statusSince"] = n.Since
	}
	if n.Unread > 0 {
		p["unread"] = n.Unread
	}
	// Only a pane whose status is waiting has the views (snapshot attaches them
	// under that condition), whatever the graph lists for it.
	if !structured && n.Status == "waiting" {
		if text, ok := perms[n.Pane]; ok {
			p["permission"] = permissionView(text)
		} else if text, ok := asks[n.Pane]; ok {
			p["ask"] = askView(text)
		}
	}
	return p
}

// stateMsg is the state push the old page draws from, built from the graph's
// panes as the comment at the top of this file describes.
func (f *fixture) stateMsg() map[string]any {
	// The first permission and the first question of each pane become its views.
	perms, asks := map[string]string{}, map[string]string{}
	for _, nd := range f.needs {
		switch nd.Kind {
		case "permission":
			if _, ok := perms[nd.Pane]; !ok {
				perms[nd.Pane] = nd.Text
			}
		case "ask":
			if _, ok := asks[nd.Pane]; !ok {
				asks[nd.Pane] = nd.Text
			}
		}
	}
	panes := map[string]any{}
	ids := make([]string, 0, len(f.nodes))
	acts := make([]string, 0, len(f.nodes))
	waiting, working := 0, 0
	for _, n := range f.nodes {
		panes[n.Pane] = f.paneState(n, perms, asks)
		ids = append(ids, n.Pane)
		act := nodeActivity(n)
		acts = append(acts, act)
		switch activityRank(act) {
		case 2:
			waiting++
		case 1:
			working++
		}
	}
	activity := aggregateActivity(acts)
	proj := map[string]any{
		"root": f.root, "name": f.proj, "active": true, "tabs": 1,
		"waiting": waiting, "working": working, "panes": len(f.nodes),
		"members": []any{map[string]any{"root": f.root, "name": f.proj}},
	}
	tab := map[string]any{
		"id": "t1", "title": f.proj, "focus": f.nodes[0].Pane, "zoom": false, "attention": waiting > 0,
		"delegated": true, "root": tileTree(ids),
	}
	if activity != "" {
		proj["activity"] = activity
		tab["activity"] = activity
	}
	return map[string]any{
		"type": "state", "root": f.root, "claudeAvailable": true, "activeTab": "t1", "broadcast": false,
		"waiting": waiting, "working": working,
		"projects": []any{proj}, "tabs": []any{tab}, "panes": panes,
		"agents": map[string]any{
			"items": []any{map[string]any{
				"id": "claude", "name": "Claude Code", "runner": "cli", "available": true, "defaultModel": "",
				"models": []any{map[string]any{"id": "", "name": "Default", "note": "whatever the CLI is set to"}},
			}},
			"default": map[string]any{"agent": "claude", "model": ""},
		},
	}
}
