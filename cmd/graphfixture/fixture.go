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

// fixtureSubdir is where the graph fixtures live, from the root of
// the repository.
var fixtureSubdir = filepath.Join("internal", "server", "testdata", "graph-fixtures")

// fixture is one of the graph messages. raw is the file as it was written,
// and is what a graph command is answered with.
type fixture struct {
	name string
	raw  []byte
	root string
	proj string
	// nodes are the panes of the graph, in the order the file has them.
	nodes []fixtureNode
	// needs are the items the graph says need the person, by pane: the
	// permission and the question items the state push's views are made from.
	needs []fixtureNeed
}

// fixtureNeed is one item of a team's needs list, or of the solo list.
type fixtureNeed struct {
	Kind string `json:"kind"`
	Pane string `json:"pane"`
	Text string `json:"text"`
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
		Teams   []struct {
			Needs []fixtureNeed `json:"needs"`
		} `json:"teams"`
		SoloNeeds []fixtureNeed `json:"soloNeeds"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if g.Type != "graph" || g.Root == "" || len(g.Nodes) == 0 {
		return nil, fmt.Errorf("%s is not a graph message", name)
	}
	fx := &fixture{name: name, raw: raw, root: g.Root, proj: g.Project, nodes: g.Nodes}
	for _, t := range g.Teams {
		fx.needs = append(fx.needs, t.Needs...)
	}
	fx.needs = append(fx.needs, g.SoloNeeds...)
	return fx, nil
}

func (f *fixture) has(pane string) bool {
	for _, n := range f.nodes {
		if n.Pane == pane {
			return true
		}
	}
	return false
}

// The words of a terminal permission prompt's two options, with the ids and kinds
// the desk gives them for Claude's Bash prompt. They are copied here, and not
// read from internal/workspace, for the reason the limits in server.go are; a
// test pins them to workspace.
const (
	optionYesID    = "yes"
	optionYesName  = "Yes"
	optionNoID     = "no"
	optionNoName   = "No, and tell Claude what to do differently"
	kindAllowOnce  = "allow_once"
	kindRejectOnce = "reject_once"
)

// permissionView is the permission view the desk puts on a waiting terminal
// pane, made from the fixture's permission item: the item's text is the Bash
// command, as it is in the graph the desk sends. Its text there is cut to a
// line, so this is the whole of what the fixture holds.
func permissionView(command string) map[string]any {
	return map[string]any{
		"tool": "Bash", "summary": "Run: " + command, "command": command,
		"options": []any{
			map[string]any{"id": optionYesID, "name": optionYesName, "kind": kindAllowOnce},
			map[string]any{"id": optionNoID, "name": optionNoName, "kind": kindRejectOnce},
		},
	}
}

// askView is the ask view of a waiting pane: one question with the item's text.
// The fixture holds no options for a question, so the view carries none.
func askView(question string) map[string]any {
	return map[string]any{"questions": []any{map[string]any{"question": question, "options": []any{}}}}
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
