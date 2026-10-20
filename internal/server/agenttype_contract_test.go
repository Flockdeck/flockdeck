package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// The consumers of a pane (the team graph, the needs-you list, a window's pane
// view, the agents list, helper counting, spend and recording start) ask the
// pane's runtime (workspace.PaneRuntime) and do not look at which agent runs in
// it, or at anything that exists only on a terminal pane. This file keeps that
// true: it parses a named list of consumer files and fails on
//
//   - a comparison, or a switch case, that sets a pane's agent id or kind
//     against a string literal ("claude", "shell", "agent"). Comparing with ""
//     asks only whether there is one and is not counted,
//   - any use of the pane's terminal session (the Sess field), which a pane of
//     another transport does not have.
//
// To add a consumer file, put its path (from the module root) in
// agentTypeConsumers. If it must still know the agent, because it launches,
// spawns or reads the catalog, put the file and the reason in
// agentTypeAllowed, with the offending expression as it prints, so one
// allowed place cannot hide another in the same file. Nothing new should be
// added there without a reason a reviewer accepts.

// agentTypeConsumers are the files that read panes to show or count them.
var agentTypeConsumers = []string{
	"internal/server/teamgraph.go",
	"internal/server/waiting.go",
	"internal/server/paneinfo.go",
	"internal/server/agents.go",
	"internal/server/spend.go",
	"internal/team/graph.go",
	"internal/team/needs.go",
	"internal/workspace/teamui.go",
	"internal/workspace/paneinfo.go",
	"internal/workspace/recording.go",
}

// agentTypeAllowed are the expressions allowed to name an agent type or reach a
// session, by file, with why. See the comment above.
var agentTypeAllowed = map[string]map[string]string{
	"internal/server/agents.go": {
		// cmd is a window's command, not a pane: Kind is the word the window
		// sends for "set the default for every project".
		`cmd.Kind != "all"`: "a command's scope word, not a pane's kind",
	},
	"internal/workspace/paneinfo.go": {
		// The process id is a terminal fact the drawer shows. A pane of another
		// transport has none to show and the field stays 0.
		`p.Sess`: "reads the process id of a terminal pane for the drawer; a pane without a session shows none",
	},
	"internal/workspace/teamui.go": {
		// Closing finished helpers hands the session on to noteFinished, which
		// records how it ended. Task T33 left this one on the terminal path.
		`p.Sess`: "the session noteFinished reports on; nil for a pane without one",
	},
}

// agentTypeNames are the field and variable names that hold a pane's agent id
// or kind.
var agentTypeNames = map[string]bool{"Agent": true, "AgentID": true, "Kind": true, "agentID": true, "agent": true, "kind": true}

type agentTypeUse struct {
	Pos  token.Position
	Expr string
	Why  string
}

func (u agentTypeUse) String() string { return fmt.Sprintf("%s: %s (%s)", u.Pos, u.Expr, u.Why) }

// findAgentTypeUses parses src and returns each place it sets an agent id or
// kind against a literal, or uses a pane's session.
func findAgentTypeUses(filename string, src []byte) ([]agentTypeUse, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var out []agentTypeUse
	add := func(n ast.Node, why string) {
		out = append(out, agentTypeUse{Pos: fset.Position(n.Pos()), Expr: exprString(fset, src, n), Why: why})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				if (isAgentTypeName(x.X) && isStringLit(x.Y)) || (isAgentTypeName(x.Y) && isStringLit(x.X)) {
					add(x, "an agent id or kind is compared with a literal")
				}
			}
		case *ast.SwitchStmt:
			if x.Tag != nil && isAgentTypeName(x.Tag) {
				for _, s := range x.Body.List {
					cc, _ := s.(*ast.CaseClause)
					for _, e := range cc.List {
						if isStringLit(e) {
							add(e, "a switch on an agent id or kind has a literal case")
						}
					}
				}
			}
		case *ast.SelectorExpr:
			if x.Sel.Name == "Sess" {
				add(x, "the terminal session is read")
			}
		}
		return true
	})
	return out, nil
}

// isStringLit says e is a non-empty string literal.
func isStringLit(e ast.Expr) bool {
	l, ok := e.(*ast.BasicLit)
	return ok && l.Kind == token.STRING && l.Value != `""` && l.Value != "``"
}

// isAgentTypeName says e is a name, or a field selected from something, that
// holds an agent id or kind.
func isAgentTypeName(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return agentTypeNames[x.Name]
	case *ast.SelectorExpr:
		return agentTypeNames[x.Sel.Name]
	case *ast.ParenExpr:
		return isAgentTypeName(x.X)
	case *ast.CallExpr:
		// string(x.Kind) and the like.
		if len(x.Args) == 1 {
			return isAgentTypeName(x.Args[0])
		}
	}
	return false
}

func exprString(fset *token.FileSet, src []byte, n ast.Node) string {
	a, b := fset.Position(n.Pos()).Offset, fset.Position(n.End()).Offset
	if a < 0 || b > len(src) || a >= b {
		return ""
	}
	return strings.Join(strings.Fields(string(src[a:b])), " ")
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			t.Fatalf("no go.mod above %s", dir)
		}
	}
}

func TestConsumersDoNotBranchOnAgentType(t *testing.T) {
	root := moduleRoot(t)
	var bad []string
	used := map[string]map[string]bool{}
	for _, rel := range agentTypeConsumers {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v (it is in agentTypeConsumers)", rel, err)
			continue
		}
		uses, err := findAgentTypeUses(rel, src)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		for _, u := range uses {
			if _, ok := agentTypeAllowed[rel][u.Expr]; !ok {
				bad = append(bad, u.String())
				continue
			}
			if used[rel] == nil {
				used[rel] = map[string]bool{}
			}
			used[rel][u.Expr] = true
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
	// An allowed expression that is no longer there is dropped from the list,
	// so the list cannot grow into a place for a new violation to hide.
	for rel, exprs := range agentTypeAllowed {
		for expr := range exprs {
			if !used[rel][expr] {
				t.Errorf("%s: %q is allowed but no longer found; remove it from agentTypeAllowed", rel, expr)
			}
		}
	}
}

// The checker finds each kind of violation in a copy of a real consumer with
// one planted at the end, and finds nothing in the copy without it.
func TestAgentTypeCheckFindsAPlantedViolation(t *testing.T) {
	root := moduleRoot(t)
	orig, err := os.ReadFile(filepath.Join(root, "internal", "server", "teamgraph.go"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	count := func(src []byte) int {
		t.Helper()
		path := filepath.Join(dir, "teamgraph.go")
		if err := os.WriteFile(path, src, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		uses, err := findAgentTypeUses("teamgraph.go", got)
		if err != nil {
			t.Fatal(err)
		}
		return len(uses)
	}
	base := count(orig)
	if base != 0 {
		t.Fatalf("the real consumer has %d uses; the planted ones below are counted against none", base)
	}
	for name, planted := range map[string]string{
		"equal":      "func planted(p paneView) bool { return p.Agent == \"claude\" }",
		"not equal":  "func planted(p paneView) bool { return p.Kind != \"shell\" }",
		"reversed":   "func planted(agentID string) bool { return \"aider\" == agentID }",
		"switch":     "func planted(p paneView) int { switch p.Agent { case \"claude\": return 1 }; return 0 }",
		"session":    "func planted(p *workspace.Pane) int { return p.Sess.Pid() }",
		"conversion": "func planted(p paneView) bool { return string(p.Kind) == \"agent\" }",
	} {
		if n := count(append(append([]byte{}, orig...), []byte("\n"+planted+"\n")...)); n != 1 {
			t.Errorf("%s: %d violations found in the planted copy, want 1", name, n)
		}
	}
	// Not violations: an emptiness check, a literal against another name, a
	// name against a constant.
	for name, fine := range map[string]string{
		"empty":    "func ok(p paneView) bool { return p.Agent == \"\" }",
		"other":    "func ok(p paneView) bool { return p.Status == \"waiting\" }",
		"constant": "func ok(p paneView, k string) bool { return p.Kind == k }",
	} {
		if n := count(append(append([]byte{}, orig...), []byte("\n"+fine+"\n")...)); n != 0 {
			t.Errorf("%s: %d violations found in a copy with none planted", name, n)
		}
	}
}

// The consumers the graph test does not reach are run through a fake pane too:
// the helper count and cap, which follow the tree of panes, and recording start,
// which asks the runtime where the conversation is kept. The pane below is told
// to be Claude Code, whose conversation can be recorded, and its runtime says it
// keeps none, so recording is refused: the runtime is read and the agent id is
// not.
func TestHelperCapAndRecordingReadTheRuntime(t *testing.T) {
	e := newGraphEnv(t)
	start := time.Now().Add(-time.Minute).Truncate(time.Second)
	for _, id := range []string{e.boss, e.dev, e.eyes, e.nested} {
		e.detach(id, fakeRuntime{status: session.StatusWorking, started: start, ran: true})
	}
	ask(e.srv, func() bool { e.ws.Pane(e.dev).Agent = "claude"; return true })

	if n, _ := ask(e.srv, func() int { return e.ws.HelpersBelow(e.boss) }); n != 3 {
		t.Errorf("%d helpers below the manager, want 3 whatever runs in them", n)
	}
	if err, _ := ask(e.srv, func() error { return e.ws.HelperRoom(e.boss) }); err != nil {
		t.Errorf("no room for a helper below a manager with three: %v", err)
	}

	found, err := e.ws.SetPaneRecording(e.dev, true)
	if !found || err == nil || !strings.Contains(err.Error(), "nothing to record") {
		t.Errorf("recording a pane whose runtime keeps no conversation: found %v, error %v", found, err)
	}
}
