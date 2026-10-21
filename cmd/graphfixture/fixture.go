package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jmwri/flockdeck/internal/help"
)

// fixtureSubdir is where the five week-10 graph fixtures live, from the root of
// the repository.
var fixtureSubdir = filepath.Join("internal", "server", "testdata", "graph-fixtures")

// fixture is one of the five graph messages. raw is the file as it was written,
// and is what a graph command is answered with.
type fixture struct {
	name string
	raw  []byte
	root string
	proj string
	// nodes are the panes of the graph, in the order the file has them.
	nodes []fixtureNode
}

type fixtureNode struct {
	Pane      string `json:"pane"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Agent     string `json:"agent"`
	Parent    string `json:"parent"`
	Status    string `json:"status"`
	Activity  string `json:"activity"`
	Since     string `json:"since"`
	Branch    string `json:"branch"`
	Live      string `json:"live"`
	Shell     bool   `json:"shell"`
	Unread    int    `json:"unread"`
	WaitingOn struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"waitingOn"`
}

// findFixtureDir looks for the fixtures from dir upwards.
func findFixtureDir(dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		p := filepath.Join(d, fixtureSubdir)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p, nil
		}
		if filepath.Dir(d) == d {
			return "", errors.New("no " + fixtureSubdir + " above " + dir + "; pass -fixtures")
		}
	}
}

// fixtureNames lists the fixtures in dir, by file name without the extension.
func fixtureNames(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// loadFixture reads the fixture a user named: its number (1 to 5) or its file
// name, with or without the extension.
func loadFixture(dir, which string) (*fixture, error) {
	names, err := fixtureNames(dir)
	if err != nil {
		return nil, err
	}
	name := ""
	which = strings.TrimSuffix(which, ".json")
	if n, err := strconv.Atoi(which); err == nil {
		for _, c := range names {
			if strings.HasPrefix(c, fmt.Sprintf("%02d-", n)) {
				name = c
			}
		}
	} else {
		for _, c := range names {
			if c == which {
				name = c
			}
		}
	}
	if name == "" {
		return nil, fmt.Errorf("no fixture %q; there are %s", which, strings.Join(names, ", "))
	}
	raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		return nil, err
	}
	var g struct {
		Type    string        `json:"type"`
		Root    string        `json:"root"`
		Project string        `json:"project"`
		Nodes   []fixtureNode `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if g.Type != "graph" || g.Root == "" || len(g.Nodes) == 0 {
		return nil, fmt.Errorf("%s is not a graph message", name)
	}
	return &fixture{name: name, raw: raw, root: g.Root, proj: g.Project, nodes: g.Nodes}, nil
}

func (f *fixture) has(pane string) bool {
	for _, n := range f.nodes {
		if n.Pane == pane {
			return true
		}
	}
	return false
}

// helloMsg is what a window is greeted with: the key table as the desk has it
// with nothing overridden. The prefs are the defaults but for helpSeen, which
// keeps the old page from opening its first-run help over the fixture (the tool
// serves no help pages).
func helloMsg() map[string]any {
	type keyView struct {
		help.Key
		Overridden bool `json:"overridden,omitempty"`
	}
	keys := make([]keyView, len(help.Keys))
	for i, k := range help.Keys {
		keys[i] = keyView{Key: k}
	}
	return map[string]any{"type": "hello", "keys": keys, "prefs": map[string]any{"helpSeen": true}}
}

// stateMsg is the state push the old page draws from, built from the graph's
// panes: one project, one tab holding every pane in a column, and a pane entry
// for each node. The new page reads the same message for its rail and for the
// panes' names.
func (f *fixture) stateMsg() map[string]any {
	waiting, working := 0, 0
	panes := map[string]any{}
	var leaves []map[string]any
	for i, n := range f.nodes {
		act := n.Activity
		if act == "" {
			act = n.Status
		}
		switch act {
		case "waiting":
			waiting++
		case "working":
			working++
		}
		kind := n.Kind
		if kind == "" {
			kind = "terminal"
		}
		p := map[string]any{
			"id": n.Pane, "kind": kind, "name": n.Name, "cwd": f.root, "branch": n.Branch,
			"status": n.Status, "activity": act, "detail": n.Live,
		}
		if n.Role != "" {
			p["role"] = n.Role
		}
		if n.Parent != "" {
			p["parent"] = n.Parent
		}
		if n.Since != "" {
			p["statusSince"] = n.Since
		}
		if n.Unread > 0 {
			p["unread"] = n.Unread
		}
		panes[n.Pane] = p
		leaves = append(leaves, map[string]any{"id": fmt.Sprintf("n%d", i), "pane": n.Pane, "weight": 1.0})
	}
	activity := ""
	switch {
	case waiting > 0:
		activity = "waiting"
	case working > 0:
		activity = "working"
	}
	proj := map[string]any{
		"root": f.root, "name": f.proj, "active": true, "tabs": 1,
		"waiting": waiting, "working": working, "panes": len(f.nodes),
	}
	tab := map[string]any{
		"id": "t1", "title": f.proj, "focus": f.nodes[0].Pane, "zoom": false, "attention": waiting > 0,
		"root": map[string]any{"id": "root", "dir": "v", "weight": 1.0, "children": leaves},
	}
	if activity != "" {
		proj["activity"] = activity
		tab["activity"] = activity
	}
	return map[string]any{
		"type": "state", "root": f.root, "claudeAvailable": true, "activeTab": "t1", "broadcast": false,
		"waiting": waiting, "working": working,
		"projects": []any{proj}, "tabs": []any{tab}, "panes": panes,
		"agents": map[string]any{"items": []any{}, "default": map[string]any{}},
	}
}
