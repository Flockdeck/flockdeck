package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/team"
)

// Nothing deprecated is removed: the old Graph view reads most of it for one more
// release. These tests keep the table, the schema, the page and the push in step.

// schemaSpot is where the schema describes a deprecated path: a definition and a
// property, or the top level when def is empty.
func schemaSpot(path string) (def, prop string) {
	switch {
	case strings.HasPrefix(path, "nodes[]."):
		return "node", strings.TrimPrefix(path, "nodes[].")
	case strings.HasPrefix(path, "teams[].caps[]."):
		return "cap", strings.TrimPrefix(path, "teams[].caps[].")
	case strings.HasPrefix(path, "teams[]."):
		return "team", strings.TrimPrefix(path, "teams[].")
	}
	return "", path
}

// The schema marks "deprecated" on exactly the fields the table calls deprecated
// as fields, and says so in the description of the others.
func TestSchemaMarksTheDeprecatedFields(t *testing.T) {
	schema := loadGraphSchema(t)
	defs, _ := schema["$defs"].(map[string]any)
	props := func(def string) map[string]any {
		if def == "" {
			p, _ := schema["properties"].(map[string]any)
			return p
		}
		d, _ := defs[def].(map[string]any)
		p, _ := d["properties"].(map[string]any)
		return p
	}
	marked := map[string]bool{}
	for _, d := range []string{"", "team", "node", "cap"} {
		for name, v := range props(d) {
			if m, _ := v.(map[string]any); m["deprecated"] == true {
				marked[d+"/"+name] = true
			}
		}
	}
	want := map[string]bool{}
	for _, f := range deprecatedFields {
		def, prop := schemaSpot(f.Path)
		v, ok := props(def)[prop].(map[string]any)
		if !ok {
			t.Errorf("%s is not in the schema", f.Path)
			continue
		}
		desc, _ := v["description"].(string)
		if !strings.Contains(desc, "deprecated since "+f.Since) && !strings.Contains(desc, "Deprecated since "+f.Since) {
			t.Errorf("%s: the schema description does not say it is deprecated since %s: %q", f.Path, f.Since, desc)
		}
		if f.Scope == "field" {
			want[def+"/"+prop] = true
		}
	}
	for k := range want {
		if !marked[k] {
			t.Errorf("%s is deprecated as a field and the schema does not mark it", k)
		}
	}
	for k := range marked {
		if !want[k] {
			t.Errorf("the schema marks %s deprecated and the table does not", k)
		}
	}
}

// Every deprecated path is still in a push that has the field set.
func TestDeprecatedFieldsAreStillSent(t *testing.T) {
	item := team.NeedsItem{ID: "ask:p1", Kind: team.NeedAsk, Pane: "p1"}
	msg := graphMsg{
		Type: "graph", Root: "/r", Project: "r",
		Teams: []graphTeamView{{
			ID: "p1", Manager: "p1", Members: []string{"p1"},
			SpendUSD: 1, SpendPartial: true, SpendIncludesPartial: true,
			Caps:  []graphCap{{Pane: "p1", Used: 1, Max: 8}},
			Needs: []team.NeedsItem{item},
		}},
		Nodes: []graphNodeView{{
			GraphNode: team.GraphNode{Pane: "p1", Depth: 1},
			Kind:      "terminal", Name: "n", Status: "working", Activity: "working",
			SpendPartial: true, SpendIncludesPartial: true,
			LastOutput: "2026-10-21T09:00:00Z", Stuck: true, Below: 1, NeedYou: 1,
		}},
		SoloNeeds: []team.NeedsItem{item},
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, f := range deprecatedFields {
		if !hasPath(v, strings.Split(f.Path, ".")) {
			t.Errorf("%s is deprecated and is no longer sent: %s", f.Path, raw)
		}
	}
}

// hasPath follows a path of names, a name ending in [] meaning each element of an
// array, and says whether some element has the whole path.
func hasPath(v any, path []string) bool {
	if len(path) == 0 {
		return true
	}
	name, each := strings.CutSuffix(path[0], "[]")
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	next, ok := m[name]
	if !ok {
		return false
	}
	if !each {
		return hasPath(next, path[1:])
	}
	list, _ := next.([]any)
	for _, e := range list {
		if hasPath(e, path[1:]) {
			return true
		}
	}
	return false
}

// The page's table names every deprecated path, and the catalog on disk lists them.
func TestDeprecatedFieldsAreOnThePageAndInTheCatalog(t *testing.T) {
	doc := readGraphDoc(t)
	for _, f := range deprecatedFields {
		if !strings.Contains(doc, "| `"+f.Path+"` |") {
			t.Errorf("docs/team-graph.md has no table row for %s", f.Path)
		}
	}
	raw, err := os.ReadFile(apiCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var c catalogFile
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Deprecated) != len(deprecatedFields) {
		t.Fatalf("the catalog lists %d deprecated fields, the server %d", len(c.Deprecated), len(deprecatedFields))
	}
	for i, f := range deprecatedFields {
		if c.Deprecated[i] != f {
			t.Errorf("catalog entry %d is %+v, the table has %+v", i, c.Deprecated[i], f)
		}
	}
}
