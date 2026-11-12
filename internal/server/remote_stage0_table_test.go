package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"
)

// remotePathsLeftAlone is every command whose handler reads a folder field
// (Root, Roots or Path) and is deliberately not in remotePathFields, with the
// reason. A command added later that reads one of them fails the test below
// until it is gated or listed here.
var remotePathsLeftAlone = map[string]string{
	"browse":              "has its own rule (browse.go): a relay window lists only inside known projects",
	"selectProject":       "Root is looked up among the open projects; an unknown one selects nothing",
	"selectRepo":          "Root is looked up among the open projects' repos; an unknown one selects nothing",
	"closeProject":        "Root is looked up among the open projects; nothing is opened or read",
	"revealPane":          "Root, Node and ID are looked up among the open panes",
	"groupProjects":       "Roots are looked up among the open projects",
	"reorderProjects":     "Roots only reorder the stored list of known projects",
	"renameGroup":         "Root is looked up among the open projects; only a name is written",
	"removeRepoFromGroup": "Root is looked up among the member repos of an open group",
	"renameProject":       "knownProject() refuses a Root that is not a project; only a name is written",
	"archiveProject":      "knownProject() refuses a Root that is not a project; only a flag is written",
	"forgetRecent":        "Root is a key into the recent list; nothing is read or run in the folder (full-access windows only, viewerrole.go)",
	"removeProject":       "Root is a key into the open and recent lists; nothing is read or run in the folder (full-access windows only, viewerrole.go)",
}

// The command table and the gate have to agree: the commands that name a
// folder are found in the dispatch switches, not remembered.
func TestEveryCommandThatReadsAFolderIsGatedOrListed(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "control.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string][]string{} // command name -> folder fields its case reads
	isCommandCase := func(n ast.Node) ([]string, *ast.CaseClause) {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			return nil, nil
		}
		var names []string
		for _, e := range cc.List {
			if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					names = append(names, s)
				}
			}
		}
		if len(names) == 0 || len(names) != len(cc.List) {
			return nil, nil
		}
		return names, cc
	}
	ast.Inspect(file, func(n ast.Node) bool {
		names, cc := isCommandCase(n)
		if cc == nil {
			return true
		}
		var fields []string
		for _, st := range cc.Body {
			ast.Inspect(st, func(m ast.Node) bool {
				if inner, _ := isCommandCase(m); inner != nil {
					return false // its own case, looked at on its own
				}
				if sel, ok := m.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "cmd" {
						switch sel.Sel.Name {
						case "Root", "Roots", "Path":
							fields = append(fields, sel.Sel.Name)
						}
					}
				}
				return true
			})
		}
		if len(fields) > 0 {
			for _, name := range names {
				reads[name] = append(reads[name], fields...)
			}
		}
		return true
	})
	if len(reads) < 10 {
		t.Fatalf("found only %d commands that read a folder field in control.go: the scan is broken", len(reads))
	}
	var missing []string
	for name := range reads {
		_, gated := remotePathFields[name]
		_, listed := remotePathsLeftAlone[name]
		if !gated && !listed {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("command %q reads %v but is neither in remotePathFields nor in remotePathsLeftAlone", name, reads[name])
	}
	for name := range remotePathsLeftAlone {
		if _, ok := reads[name]; !ok {
			t.Errorf("remotePathsLeftAlone lists %q, which no longer reads a folder field", name)
		}
	}
}
