package server

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// docs/team-graph.md is the reference for the graph push. These tests keep it
// beside the schema and the code: it names every field, enum value, command and
// message type, quotes the limits the code has, and its examples are valid.

const graphDocPath = "../../docs/team-graph.md"

func readGraphDoc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(graphDocPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// schemaNames returns every property name and every enum value the schema has,
// sorted.
func schemaNames(schema map[string]any) (props, enums []string) {
	seenP, seenE := map[string]bool{}, map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if p, ok := x["properties"].(map[string]any); ok {
				for name := range p {
					seenP[name] = true
				}
			}
			if e, ok := x["enum"].([]any); ok {
				for _, v := range e {
					if s, ok := v.(string); ok {
						seenE[s] = true
					}
				}
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(schema)
	for n := range seenP {
		props = append(props, n)
	}
	for n := range seenE {
		enums = append(enums, n)
	}
	sort.Strings(props)
	sort.Strings(enums)
	return props, enums
}

// The page names every field and every enumerated value the schema has.
func TestGraphPageNamesEveryFieldAndValue(t *testing.T) {
	doc := readGraphDoc(t)
	props, enums := schemaNames(loadGraphSchema(t))
	if len(props) < 40 {
		t.Fatalf("read only %d property names from the schema", len(props))
	}
	for _, name := range append(props, enums...) {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("docs/team-graph.md does not name %q", name)
		}
	}
}

// The page names every command and every message type it describes.
func TestGraphPageNamesEveryCommandAndMessage(t *testing.T) {
	doc := readGraphDoc(t)
	for _, name := range []string{
		"graphSnapshot", "graphWatch", "graphUnwatch", "paneSend", "permissionAnswer",
		"conversationOpen", "conversationOlder", "conversationDetail", "conversationClose", "conversationSearch",
		"graph", "teamResult", "teamGone", "notice", "conversationPage", "conversationAppend",
	} {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("docs/team-graph.md does not name %q", name)
		}
	}
	// Every graph command is in the access table, so the page's account of who may
	// send them has something to be true of.
	for _, name := range []string{"graphSnapshot", "graphWatch", "graphUnwatch", "paneSend", "permissionAnswer"} {
		if commandAccess[name] != accessFull {
			t.Errorf("%s is not accessFull: update the table in section 1 of docs/team-graph.md", name)
		}
	}
	// The conversation commands are the entries of the access table named
	// conversationX (not "conversations", the saved-session list), so a new one
	// cannot be left off the page.
	conv := 0
	for name, a := range commandAccess {
		if len(name) <= len("conversation") || !strings.HasPrefix(name, "conversation") || name[len("conversation")] < 'A' || name[len("conversation")] > 'Z' {
			continue
		}
		conv++
		if a != accessWatcher {
			t.Errorf("%s is not accessWatcher: update section 1 of docs/team-graph.md", name)
		}
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("docs/team-graph.md does not name %q", name)
		}
	}
	if conv < 5 {
		t.Errorf("found %d conversation commands in commandAccess", conv)
	}
}

// Every field the page's field tables name is in the schema, so a field removed
// from the schema cannot stay on the page. Sections 3 and 6 are the ones whose
// first column holds field names; `req`, `cmd` and the like in section 1 are the
// commands' own, not the graph's.
func TestGraphPageTablesNameOnlySchemaFields(t *testing.T) {
	doc := readGraphDoc(t)
	props, enums := schemaNames(loadGraphSchema(t))
	known := map[string]bool{}
	for _, n := range append(props, enums...) {
		known[n] = true
	}
	section := ""
	rows := 0
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "## ") {
			section = strings.Fields(line)[1]
			continue
		}
		if section != "3." && section != "6." || !strings.HasPrefix(line, "| `") {
			continue
		}
		cell := strings.TrimPrefix(line, "| `")
		end := strings.Index(cell, "`")
		if end < 0 {
			continue
		}
		rows++
		if name := cell[:end]; !known[name] {
			t.Errorf("section %s names %q, which the schema does not have", section, name)
		}
	}
	if rows < 30 {
		t.Errorf("read only %d field rows from sections 3 and 6", rows)
	}
}

// The limits the page quotes are the code's. If one changes, the page changes in
// the same commit.
func TestGraphPageQuotesTheCodesLimits(t *testing.T) {
	doc := readGraphDoc(t)
	for _, c := range []struct {
		got  bool
		want string
	}{
		{maxGraphEdges == 100, "100 `message` edges and 100 `sent` edges"},
		{maxGraphPanes == 500, "500 nodes"},
		{team.MaxNeeds == 50, "50 for each team"},
		{team.MaxTaps == 200, "The last 200"},
		{teamInterval == 500*time.Millisecond, "500 milliseconds"},
		{graphTickInterval == 30*time.Second, "Every 30 seconds"},
		{team.StaleUnreadAfter == 30*time.Minute, "more than 30 minutes"},
		{team.ReturnedWindow == 24*time.Hour, "last 24 hours"},
		{team.MaxWaitText == 200, "waiting-on text 200 characters"},
		{graphLiveCut == 140 && graphEdgeCut == 140, "a live line 140; an edge's first line 140"},
		{graphGoalCut == 300, "a team's goal 300"},
		{graphNameCut == 80 && graphPeerCut == 80 && graphBranchCut == 120 && graphAgentCut == 80, "a pane's name 80, its peer name 80, its branch 120 and its agent id 80"},
		{team.MaxConflictPaths == 5, "up to 5 paths"},
		{workspace.MaxHelpers == 8, "`max` is 8"},
		{workspace.MaxPaneSendBytes == 16*1024, "16 KiB (16384 bytes"},
		{maxPermissionDiff == 64<<10, "cut at 64 KiB"},
		{conversationPageSize == 50, "newest 50 entries"},
		{conversationSearchMaxMatches == 50, "up to 50 matches"},
		{conversationSearchMaxQueryRunes == 200, "cut to 200 characters"},
		{conversationPollInterval == 2*time.Second, "every 2 seconds"},
		{conversationIdleDowngradeDelay == 30*time.Second, "Nobody watching for 30 seconds"},
		{transcript.ToolOutputCap == 4<<10 && transcript.DiffCap == 64<<10, "output over\n  4 KiB, a diff over 64 KiB"},
	} {
		if !c.got {
			t.Errorf("a limit changed: update docs/team-graph.md (it quotes %q) and this test", c.want)
		}
		if !strings.Contains(doc, c.want) {
			t.Errorf("docs/team-graph.md does not contain %q", c.want)
		}
	}
}

// The page's wording for the kinds the code has is the code's: every waiting-on
// kind and needs-you kind, in the order the code tries them.
func TestGraphPageListsTheKindsInOrder(t *testing.T) {
	doc := readGraphDoc(t)
	waiting := []string{team.WaitDown, team.WaitUser, team.WaitReviewer, team.WaitTask, team.WaitHelpers, team.WaitNone}
	last := -1
	for n, k := range waiting {
		i := strings.Index(doc, fmt.Sprintf("| %d | `%s` |", n+1, k))
		if i < 0 {
			t.Errorf("the waiting-on table has no row %d for %q", n+1, k)
			continue
		}
		if i < last {
			t.Errorf("the waiting-on table lists %q out of order", k)
		}
		last = i
	}
	needs := []string{team.NeedPermission, team.NeedAsk, team.NeedReturned, team.NeedConflict, team.NeedDown, team.NeedSetup, team.NeedStaleUnread, team.NeedStuck}
	last = -1
	for _, k := range needs {
		i := strings.LastIndex(doc, "| `"+k+"` |")
		if i < 0 {
			t.Errorf("the needs-you table has no row for %q", k)
			continue
		}
		if i < last {
			t.Errorf("the needs-you table lists %q out of order", k)
		}
		last = i
	}
}

// Every example on the page validates against the schema.
func TestGraphPageExamplesMatchTheSchema(t *testing.T) {
	doc := readGraphDoc(t)
	schema := loadGraphSchema(t)
	seen := map[string]bool{}
	in := false
	for _, line := range strings.Split(doc, "\n") {
		switch {
		case line == "```json":
			in = true
		case line == "```":
			in = false
		case in:
			var probe map[string]any
			if err := json.Unmarshal([]byte(line), &probe); err != nil {
				t.Errorf("example is not JSON: %v\n%s", err, line)
				continue
			}
			def := ""
			switch probe["type"] {
			case "graph":
				// The document root.
				if errs := schema.Validate([]byte(line)); len(errs) != 0 {
					t.Errorf("the graph example does not match the schema: %v", errs)
				}
				seen["graph"] = true
				continue
			case "paneSend":
				def = "paneSend"
			default:
				if _, ok := probe["tool"]; ok {
					def = "permission"
				}
			}
			if def == "" {
				t.Errorf("example of an unknown shape: %s", line)
				continue
			}
			if errs := validateDef(schema, def, []byte(line)); len(errs) != 0 {
				t.Errorf("the %s example does not match the schema: %v", def, errs)
			}
			seen[def] = true
		}
	}
	for _, def := range []string{"graph", "paneSend", "permission"} {
		if !seen[def] {
			t.Errorf("docs/team-graph.md has no example of %s", def)
		}
	}
}

// The status enum in the schema is session.Status.String over the Status
// constants: each string a constant gives is in the enum, and each value in the
// enum is given by some constant. A value past the last constant gives "unknown".
func TestGraphStatusEnumMatchesSessionStatus(t *testing.T) {
	node, _ := loadGraphSchema(t)["$defs"].(map[string]any)["node"].(map[string]any)
	status, _ := node["properties"].(map[string]any)["status"].(map[string]any)
	list, _ := status["enum"].([]any)
	if len(list) == 0 {
		t.Fatal("the schema has no enum for a node's status")
	}
	inEnum := map[string]bool{}
	for _, v := range list {
		inEnum[v.(string)] = true
	}
	produced := map[string]bool{}
	for s := session.StatusStarting; s <= session.StatusExited+1; s++ {
		produced[s.String()] = true
	}
	for name := range produced {
		if !inEnum[name] {
			t.Errorf("session.Status gives %q, which the schema's status enum lacks", name)
		}
	}
	for name := range inEnum {
		if !produced[name] {
			t.Errorf("the schema's status enum has %q, which no session.Status gives", name)
		}
	}
	if session.Status(session.StatusExited+1).String() != "unknown" {
		t.Error("a Status past StatusExited no longer reads \"unknown\"; update the enum and this test")
	}
}
