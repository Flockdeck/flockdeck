package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The consumers of a pane (the team graph, the needs-you list, a window's pane
// view, the agents list, helper counting, spend and recording start) ask the
// pane's runtime (workspace.PaneRuntime) and do not look at which agent runs in
// it, or at anything that exists only on a terminal pane. This file keeps that
// true. It parses every non-test file of internal/server, internal/team and
// internal/workspace, except the few listed in agentTypeExempt, and fails on
//
//   - a pane's agent id, kind, type, runner or id compared (== or !=), or used
//     as a switch tag, against a string literal or a constant ("claude",
//     session.KindShell, agentClaude, agent.RunnerACP), including a switch that
//     binds the value first (switch a := p.Agent; a),
//   - such a value passed to strings.HasPrefix, HasSuffix, Contains or EqualFold
//     with a literal or constant, to slices.Contains or Index, or used to index
//     a map,
//   - any use of the pane's terminal session (the Sess field).
//
// A window's command (a variable named cmd) has a Kind word of its own and is
// not a pane, so its fields are not counted. Comparing with "" asks only whether
// there is one and is not counted. A check on the transport (Transport(),
// Structured, the pane's Caps) is the runtime's own answer and is not counted:
// it is what a consumer should ask.
//
// The check is syntactic and so is fragile in two ways. It goes by names: any
// Kind-named (or Agent-, Type-, Runner-, ID-named) variable compared with a
// literal needs an entry below, whether it is a pane's or not. And it does not
// follow values past one step: a value copied into a variable (k := p.Kind;
// k == "shell") is followed, but one passed through a function, or an accessor
// that returns the session, is out of scope. A reviewer reads the consumer files
// for those; this catches the plain forms before they get there.
//
// A new file in those packages is scanned without being added anywhere. If a
// place in it must still know the agent or reach the session, add it to
// agentTypeAllowed under the file, keyed by the enclosing function and the
// expression as the failure prints them, with how many there are and why. The
// count means a second use of an allowed expression in the same function fails,
// and so does a count that is too high.

// agentTypePackages are the packages whose files are scanned, from the module
// root.
var agentTypePackages = []string{"internal/server", "internal/team", "internal/workspace"}

// agentTypeExempt are the files that are not scanned, each with why. They start,
// end and watch the process, are the terminal's own side of the runtime, or
// choose the agent from the catalog, which is terminal work by nature and is
// where an agent id is meant to be read.
//
// These files do contain real agent-type branches, and nothing guards them: for
// instance TerminalPermissionOptions in panekind.go (agentID != "claude"),
// Pane.IsAgent in workspace.go (p.Kind != session.KindShell) and the Runner
// checks in startPane. A violation planted in one is not seen. So the list is
// kept short (agentTypeExemptCount pins its length) and a new file does not
// belong on it because it has uses to allow: add those to agentTypeAllowed.
// agentTypeExemptCount is how many files are exempt. Adding one changes it, so a
// new exemption is a decision made here and not a side effect.
const agentTypeExemptCount = 9

var agentTypeExempt = map[string]string{
	"internal/workspace/reap.go":             "ends the processes of a pane and its worktree",
	"internal/workspace/baton.go":            "restarts a pane with a baton, which is a terminal restart; chooses the agent from the catalog",
	"internal/workspace/activity.go":         "reads the terminal's activity",
	"internal/workspace/background_check.go": "reads an agent's own background work files and transcript",
	"internal/workspace/spawn.go":            "chooses the agent from the catalog and starts the pane",
	"internal/workspace/workspace.go":        "the Pane type and its terminal operations: status, resize, restart, hooks, start. Unguarded: Pane.IsAgent compares the kind, startPane checks the runner",
	"internal/workspace/panekind.go":         "terminalRuntime, the terminal's implementation of PaneRuntime. Unguarded: TerminalPermissionOptions compares the agent id with \"claude\"",
	"internal/workspace/structuredpane.go":   "structuredRuntime, the structured implementation of PaneRuntime, and its start. Unguarded: it reads the pane's session to start",
	"internal/workspace/team.go":             "idle and exit notes read off a pane's session",
}

// agentTypeFiles are the files to scan: every non-test Go file of the packages
// in agentTypePackages that is not exempt, from the module root, slash form.
func agentTypeFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, pkg := range agentTypePackages {
		ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(pkg)))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			if _, skip := agentTypeExempt[pkg+"/"+name]; !skip {
				out = append(out, pkg+"/"+name)
			}
		}
	}
	return out
}

// agentTypeAllow is one allowed expression in one function.
type agentTypeAllow struct {
	N   int
	Why string
}

// agentTypeAllowed are the places allowed to name an agent type or reach a
// session, by file, then "function | expression". See the comment above.
var agentTypeAllowed = map[string]map[string]agentTypeAllow{
	"internal/server/artifacts_consent.go": {
		"Server.setArtifactKind | artifactOffered[kind]": {1, "an artifact kind, not an agent type"},
		"artifactAckText | \"recordings\"":               {1, "an artifact kind, not an agent type"},
	},
	"internal/server/artifacts_recordings.go": {
		"Server.openRecording | e.Kind != recordingsKind": {1, "an artifact kind, not an agent type"},
	},
	"internal/server/artifacts_socket.go": {
		"Server.artifactList | req.Kind == recordingsKind": {1, "an artifact kind, not an agent type"},
	},
	"internal/server/background.go": {
		"backgroundViews | kind == \"agent\"": {1, "a background item's kind (agent or task), not a pane's"},
		"backgroundViews | kind == \"task\"":  {1, "a background item's kind (agent or task), not a pane's"},
	},
	"internal/server/conversation_search.go": {
		"matchEntryText | transcript.KindPrompt": {1, "a transcript entry's kind"},
		"matchEntryText | transcript.KindReply":  {1, "a transcript entry's kind"},
		"matchEntryText | transcript.KindTool":   {1, "a transcript entry's kind"},
	},
	"internal/server/jevkey.go": {
		"Server.jevKey | \"clear\"": {1, "the subcommand word of jevKey, not an agent type"},
		"Server.jevKey | \"set\"":   {1, "the subcommand word of jevKey, not an agent type"},
	},
	"internal/server/preview.go": {
		"Server.notePreview | changed[i].Kind == transcript.KindReply": {1, "a transcript entry's kind"},
	},
	"internal/server/pty.go": {
		"Server.paneSession | p.Sess": {1, "the terminal's own pane session, for the terminal socket; a pane without one is not attached"},
		"Server.repaintSize | p.Sess": {2, "the terminal's own pane session, for the terminal socket; a pane without one is not attached"},
	},
	"internal/server/relayteams.go": {
		"relayProjectFromGraph | k != team.WaitNone":            {1, "a wait, need or graph kind, not an agent type"},
		"relayProjectFromGraph | kind == \"permission\"":        {1, "a wait, need or graph kind, not an agent type"},
		"relayProjectFromGraph | kind == \"question\"":          {1, "a wait, need or graph kind, not an agent type"},
		"relayProjectFromGraph | n.Kind != graphKindStructured": {1, "the relay form gives a structured pane its kind and status and nothing else, by design"},
		"relayProjectFromGraph | relayNeedKinds[it.Kind]":       {1, "a wait, need or graph kind, not an agent type"},
		"relayProjectFromGraph | team.WaitHelpers":              {1, "a wait, need or graph kind, not an agent type"},
		"relayProjectFromGraph | team.WaitUser":                 {1, "a wait, need or graph kind, not an agent type"},
		"scrubRelayProject | relayKinds[rp.Kind]":               {1, "a wait, need or graph kind, not an agent type"},
		"scrubRelayProject | relayWaitKinds[w.Kind]":            {1, "a wait, need or graph kind, not an agent type"},
		"scrubRelayProject | sp.Kind != graphKindStructured":    {1, "the relay form gives a structured pane its kind and status and nothing else, by design"},
		"scrubRelayProject | w.Kind == team.WaitHelpers":        {1, "a wait, need or graph kind, not an agent type"},
		"scrubRelayProject | w.Kind == team.WaitUser":           {1, "a wait, need or graph kind, not an agent type"},
	},
	"internal/server/remote.go": {
		"Server.remotePair | kind != remote.KindHost":   {1, "a paired device's kind, not an agent type"},
		"Server.remotePair | kind == remote.KindHost":   {1, "a paired device's kind, not an agent type"},
		"Server.remoteRename | kind != remote.KindHost": {1, "a paired device's kind, not an agent type"},
		"Server.remoteRename | kind == remote.KindHost": {1, "a paired device's kind, not an agent type"},
	},
	"internal/server/viewerrole.go": {
		"controlClient.mayReceive | viewerMessageTypes[probe.Type]":  {1, "the message type of a push, not an agent type"},
		"controlClient.mayReceive | watcherMessageTypes[probe.Type]": {1, "the message type of a push, not an agent type"},
	},
	"internal/team/events.go": {
		"EventLog.Load | e.Kind != EventDelivery": {1, "an event's kind, not a pane's"},
		"EventLog.Load | e.Kind != EventMessage":  {1, "an event's kind, not a pane's"},
		"EventLog.Load | e.Kind != EventWake":     {1, "an event's kind, not a pane's"},
		"EventLog.Load | e.Kind == EventWake":     {1, "an event's kind, not a pane's"},
	},
	"internal/workspace/claudelink.go": {
		"resolveClaudeLink | e.Type != \"tree\"": {1, "a git tree entry's type"},
	},
	"internal/workspace/context.go": {
		"Workspace.PaneContext | p.Kind == session.KindShell":   {1, "whether a pane is a shell or an agent, to brief it; not which agent"},
		"Workspace.PaneContext | sib.Kind == session.KindShell": {1, "whether a pane is a shell or an agent, to brief it; not which agent"},
	},
	"internal/workspace/panesend.go": {
		"Workspace.SendToPaneThen | p.Sess": {1, "types into a terminal session; a structured pane is sent its prompt through its runtime just above"},
	},
	"internal/workspace/provider.go": {
		"BatonProviderWith | spec.Runner == agent.RunnerAPI": {1, "the catalog entry's runner"},
	},
	"internal/server/agents.go": {
		// The agents list describes the catalog, which is where an agent's runner
		// and id are meant to be read. It does not read a pane.
		"modelViews | spec.Runner == agent.RunnerAPI":         {1, "the catalog entry's runner, to list its models"},
		"addressNotice | spec.ID == agent.OpenAICompatibleID": {1, "the catalog entry's id, for the endpoint address notice"},
	},
	"internal/server/control.go": {
		"Server.snapshot | pv.Kind != \"shell\"": {1, "the pane view's own kind word, shell or agent, set from the pane; T51 moves it to the runtime"},
		"Server.snapshot | p.Sess":               {3, "the terminal's size and usage figures; a pane without a session shows none"},
	},
	"internal/server/paneinfo.go": {
		"paneInfoFields | map[bool]string{true: \"Agent\", false: \"Shell\"}[d.Agent]": {1, "d.Agent is a bool here, and the map is a label"},
	},
	"internal/server/push.go": {
		"Server.pushDue | p.Sess": {1, "skips a pane that has no session. A waiting structured pane gets no phone push: it is answered at the desk only (decided in #378)"},
	},
	"internal/team/needs.go": {
		// Kind here is a needs item's kind (ask, permission, down), which names
		// what the person has to do. It is not an agent's.
		"needRank | NeedAsk":                    {1, "a needs item's kind, not a pane's"},
		"needRank | NeedPermission":             {1, "a needs item's kind, not a pane's"},
		"needRank | NeedReturned":               {1, "a needs item's kind, not a pane's"},
		"needRank | NeedConflict":               {1, "a needs item's kind, not a pane's"},
		"needRank | NeedDown":                   {1, "a needs item's kind, not a pane's"},
		"needRank | NeedSetup":                  {1, "a needs item's kind, not a pane's"},
		"needRank | NeedStaleUnread":            {1, "a needs item's kind, not a pane's"},
		"compareNeeds | a.Kind == NeedReturned": {1, "a needs item's kind, not a pane's"},
		"needRank | NeedStuck":                  {1, "a needs item's kind, not a pane's"},
		"IsStuck | kind == NeedStuck":           {1, "a needs item's kind, not a pane's"},
		"AsksPerson | kind != NeedStuck":        {1, "a needs item's kind, not a pane's"},
		"AsksPerson | kind != NeedStaleUnread":  {1, "a needs item's kind, not a pane's"},
	},
	"internal/workspace/paneinfo.go": {
		// The process id is a terminal fact the drawer shows. A pane of another
		// transport has none to show and the field stays 0.
		"Workspace.PaneDetailsOf | p.Sess": {2, "reads the process id of a terminal pane for the drawer; a pane without a session shows none"},
	},
	"internal/workspace/teamui.go": {
		// Closing finished helpers hands the session on to noteFinished, which
		// records how it ended.
		"Workspace.CloseFinishedIn | p.Sess": {1, "the session noteFinished reports on; nil for a pane without one"},
	},
	"internal/workspace/wake.go": {
		// Typed wake reads and writes a terminal's input and screen through
		// wakeSession. A structured pane implements it too (wakeStructured) and is
		// sent its line through wakeCandidate.Deliver, so the c.Sess reads are not
		// terminal-only. wakeCandidates branches on the kind of runtime with a type
		// assertion (w.Runtime(p).(*structuredRuntime)), which this checker does not
		// catch.
		"wakeService.consider | c.Sess":                          {7, "the status, input times and reading of a wakeSession, a terminal or a structured pane"},
		"wakeService.fire | c.Sess":                              {5, "types into a terminal; a structured pane is delivered to before this (wakeCandidate.Deliver)"},
		"Workspace.wakeCandidates | p.Sess":                      {2, "the skip of a pane with no session, which a structured pane passes because it has a runtime, and the session of a terminal candidate"},
		"Workspace.wakeCandidates | p.Kind != session.KindAgent": {1, "only agent panes are woken"},
	},
}

// agentTypeNames are the names a pane's agent id, kind, type, runner or id goes
// by. mapKeyNames are the ones a map index or a slice search is counted for; ID
// is left out of those, since maps of panes by id are everywhere.
var (
	agentTypeNames = map[string]bool{
		"Agent": true, "AgentID": true, "Kind": true, "Type": true, "Runner": true, "ID": true,
		"agent": true, "agentID": true, "kind": true, "agentType": true, "runner": true,
	}
	mapKeyNames = map[string]bool{
		"Agent": true, "AgentID": true, "Kind": true, "Type": true, "Runner": true,
		"agent": true, "agentID": true, "kind": true, "agentType": true, "runner": true,
	}
)

type agentTypeUse struct {
	Pos  token.Position
	Func string
	Expr string
	Why  string
}

func (u agentTypeUse) key() string { return u.Func + " | " + u.Expr }

func (u agentTypeUse) String() string {
	return fmt.Sprintf("%s: %s (%s)", u.Pos, u.key(), u.Why)
}

// packageConsts are the names declared by const at the top level of the Go files
// in dir, tests left out.
func packageConsts(dir string) map[string]bool {
	out := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			continue
		}
		addFileConsts(f, out)
	}
	return out
}

func addFileConsts(f *ast.File, into map[string]bool) {
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			for _, n := range s.(*ast.ValueSpec).Names {
				into[n.Name] = true
			}
		}
	}
}

// findAgentTypeUses parses src and returns each place it sets an agent id or
// kind against a literal or constant, or uses a pane's session. consts are the
// constants of the file's package; the file's own are added.
func findAgentTypeUses(filename string, src []byte, consts map[string]bool) ([]agentTypeUse, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for k := range consts {
		known[k] = true
	}
	addFileConsts(f, known)
	imports := map[string]bool{}
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		name := path.Base(p)
		if im.Name != nil {
			name = im.Name.Name
		}
		imports[name] = true
	}

	var out []agentTypeUse
	fn := "<package>"
	add := func(n ast.Node, why string) {
		out = append(out, agentTypeUse{Pos: fset.Position(n.Pos()), Func: fn, Expr: exprString(fset, src, n), Why: why})
	}
	// namedLit says e is a non-empty string literal, or names a constant: one of
	// the package's or the file's, or a name from another package.
	namedLit := func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.BasicLit:
			return x.Kind == token.STRING && x.Value != `""` && x.Value != "``"
		case *ast.Ident:
			return known[x.Name]
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			return ok && imports[id.Name]
		}
		return false
	}

	for _, d := range f.Decls {
		fn = "<package>"
		if fd, ok := d.(*ast.FuncDecl); ok {
			fn = fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				fn = recvName(fd.Recv.List[0].Type) + "." + fn
			}
		}
		aliases := map[string]bool{}
		typed := func(e ast.Expr, names map[string]bool) bool {
			if id, ok := unparen(e).(*ast.Ident); ok && aliases[id.Name] {
				return true
			}
			return isNamed(e, names)
		}
		ast.Inspect(d, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				// A value copied into a variable is the same value: k := p.Kind.
				if len(x.Lhs) == len(x.Rhs) {
					for i, rhs := range x.Rhs {
						if id, ok := x.Lhs[i].(*ast.Ident); ok && typed(rhs, agentTypeNames) {
							aliases[id.Name] = true
						}
					}
				}
			case *ast.ValueSpec:
				if len(x.Names) == len(x.Values) {
					for i, v := range x.Values {
						if typed(v, agentTypeNames) {
							aliases[x.Names[i].Name] = true
						}
					}
				}
			case *ast.BinaryExpr:
				if x.Op == token.EQL || x.Op == token.NEQ {
					if (typed(x.X, agentTypeNames) && namedLit(x.Y)) || (typed(x.Y, agentTypeNames) && namedLit(x.X)) {
						add(x, "an agent id or kind is compared with a literal or constant")
					}
				}
			case *ast.SwitchStmt:
				if as, ok := x.Init.(*ast.AssignStmt); ok && len(as.Lhs) == 1 && len(as.Rhs) == 1 && isNamed(as.Rhs[0], agentTypeNames) {
					if id, ok := as.Lhs[0].(*ast.Ident); ok {
						aliases[id.Name] = true
					}
				}
				if x.Tag != nil && typed(x.Tag, agentTypeNames) {
					for _, s := range x.Body.List {
						cc, _ := s.(*ast.CaseClause)
						for _, e := range cc.List {
							if namedLit(e) {
								add(e, "a switch on an agent id or kind has a literal or constant case")
							}
						}
					}
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok {
					break
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					break
				}
				switch pkg.Name + "." + sel.Sel.Name {
				case "strings.HasPrefix", "strings.HasSuffix", "strings.Contains", "strings.EqualFold":
					hasType, hasLit := false, false
					for _, a := range x.Args {
						hasType = hasType || typed(a, agentTypeNames)
						hasLit = hasLit || namedLit(a)
					}
					if hasType && hasLit {
						add(x, "an agent id or kind is matched against a literal by a strings function")
					}
				case "slices.Contains", "slices.Index":
					for _, a := range x.Args {
						if typed(a, mapKeyNames) {
							add(x, "an agent id or kind is searched for in a list")
							break
						}
					}
				}
			case *ast.IndexExpr:
				if typed(x.Index, mapKeyNames) {
					add(x, "a map is indexed by an agent id or kind")
				}
			case *ast.SelectorExpr:
				if x.Sel.Name == "Sess" {
					add(x, "the terminal session is read")
				}
			}
			return true
		})
	}
	return out, nil
}

func recvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvName(x.X)
	}
	return "?"
}

func rootIdent(e ast.Expr) (*ast.Ident, bool) {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x, true
		case *ast.SelectorExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return nil, false
		}
	}
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// isNamed says e is a name, or a field selected from something, in names.
func isNamed(e ast.Expr, names map[string]bool) bool {
	switch x := unparen(e).(type) {
	case *ast.Ident:
		return names[x.Name]
	case *ast.SelectorExpr:
		// A window's command (cmd) has a Kind word of its own, and is not a pane.
		if root, ok := rootIdent(x); ok && root.Name == "cmd" {
			return false
		}
		return names[x.Sel.Name]
	case *ast.CallExpr:
		// string(x.Kind) and the like.
		if len(x.Args) == 1 {
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "string" {
				return isNamed(x.Args[0], names)
			}
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
	found := map[string]map[string]int{}
	var problems []string
	for _, rel := range agentTypeFiles(t, root) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		src, err := os.ReadFile(full)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		uses, err := findAgentTypeUses(rel, src, packageConsts(filepath.Dir(full)))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		for _, u := range uses {
			if found[rel] == nil {
				found[rel] = map[string]int{}
			}
			found[rel][u.key()]++
			if found[rel][u.key()] > agentTypeAllowed[rel][u.key()].N {
				problems = append(problems, u.String())
			}
		}
	}
	// An allowance that is not used up is lowered or dropped, so the list cannot
	// hold a place for a new violation.
	for rel, keys := range agentTypeAllowed {
		for key, a := range keys {
			if n := found[rel][key]; n < a.N {
				problems = append(problems, fmt.Sprintf("%s: %q is allowed %d times but found %d; lower or remove it in agentTypeAllowed", rel, key, a.N, n))
			}
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// The checker finds each form in a copy of a real consumer with one planted at
// the end, and finds nothing in the copy without it.
func TestAgentTypeCheckFindsAPlantedViolation(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, "internal", "server")
	orig, err := os.ReadFile(filepath.Join(dir, "teamgraph.go"))
	if err != nil {
		t.Fatal(err)
	}
	consts := packageConsts(dir)
	count := func(planted string) int {
		t.Helper()
		src := append(append([]byte{}, orig...), []byte("\n"+planted+"\n")...)
		// The planted code may name packages the consumer does not import.
		src = []byte(strings.Replace(string(src), "\nimport (", "\nimport agent \"github.com/jmwri/flockdeck/internal/agent\"\nimport (", 1))
		uses, err := findAgentTypeUses("teamgraph.go", src, consts)
		if err != nil {
			t.Fatalf("%v in:\n%s", err, planted)
		}
		return len(uses)
	}
	if n := count(""); n != 0 {
		t.Fatalf("the real consumer has %d uses; the planted ones below are counted against none", n)
	}
	for name, planted := range map[string]string{
		"literal":        `func planted(p paneView) bool { return p.Agent == "claude" }`,
		"not equal":      `func planted(p paneView) bool { return p.Kind != "shell" }`,
		"reversed":       `func planted(agentID string) bool { return "aider" == agentID }`,
		"switch":         `func planted(p paneView) int { switch p.Agent { case "claude": return 1 }; return 0 }`,
		"session":        `func planted(p *workspace.Pane) int { return p.Sess.Pid() }`,
		"conversion":     `func planted(p paneView) bool { return string(p.Kind) == "agent" }`,
		"package const":  `func planted(p paneView) bool { return p.Kind == session.KindShell }`,
		"local const":    "const agentClaude = \"claude\"\nfunc planted(p paneView) bool { return p.Agent == agentClaude }",
		"switch const":   `func planted(p paneView) int { switch p.Kind { case session.KindShell: return 1 }; return 0 }`,
		"switch binding": `func planted(p paneView) int { switch a := p.Agent; a { case "claude": return 1 }; return 0 }`,
		"alias":          `func planted(p paneView) bool { k := p.Kind; return k == "shell" }`,
		"alias, var":     `func planted(p paneView) bool { var a = p.Agent; return a == "claude" }`,
		"alias, assign":  `func planted(p paneView) bool { var k string; k = p.Kind; return k == "shell" }`,
		"alias, switch":  `func planted(p paneView) int { k := p.Kind; switch k { case "shell": return 1 }; return 0 }`,
		"runner":         `func planted(spec agentSpec) bool { return spec.Runner == agent.RunnerACP }`,
		"type name":      `func planted(c conv) bool { return c.Type == "acp" }`,
		"id name":        `func planted(spec agentSpec) bool { return spec.ID == "claude" }`,
		"has prefix":     `func planted(p paneView) bool { return strings.HasPrefix(p.Agent, "cla") }`,
		"contains":       `func planted(p paneView) bool { return strings.Contains(p.Agent, "cla") }`,
		"equal fold":     `func planted(p paneView) bool { return strings.EqualFold(p.Agent, "Claude") }`,
		"slices":         `func planted(p paneView, ids []string) bool { return slices.Contains(ids, p.Agent) }`,
		"map index":      `func planted(p paneView, m map[string]int) int { return m[p.Agent] }`,
	} {
		if n := count(planted); n != 1 {
			t.Errorf("%s: %d violations found in the planted copy, want 1", name, n)
		}
	}
	// A second use of the same expression in the same function is a second
	// violation, which a count in the allowlist is checked against.
	if n := count(`func planted(p *workspace.Pane) bool { return p.Sess.Exited() || p.Sess.Closing() }`); n != 2 {
		t.Errorf("two reads of the session in one function: %d violations, want 2", n)
	}
	// Not violations: an emptiness check, a literal against another name, a
	// comparison of two panes, a transport check, a plain variable.
	for name, fine := range map[string]string{
		"empty":     `func ok(p paneView) bool { return p.Agent == "" }`,
		"other":     `func ok(p paneView) bool { return p.Status == "waiting" }`,
		"variable":  `func ok(p paneView, k string) bool { return p.Kind == k }`,
		"two panes": `func ok(p, q paneView) bool { return p.Kind == q.Kind }`,
		"transport": `func ok(rt workspace.PaneRuntime) bool { return rt.Transport() == workspace.TransportStructured && rt.Caps().Structured }`,
		"command":   `func ok(cmd command) bool { return cmd.Kind == "off" }`,
		"nil":       `func ok(p *workspace.Pane) bool { return p.Agent != "" && p != nil }`,
		"pane map":  `func ok(p paneView, m map[string]int) int { return m[p.ID] }`,
	} {
		if n := count(fine); n != 0 {
			t.Errorf("%s: %d violations found in a copy with none planted", name, n)
		}
	}
}

// Every non-test file of the three packages is scanned, with only the exempt
// ones left out, so a file nobody listed is checked too.
func TestAgentTypeCheckScansEveryFileButTheExemptOnes(t *testing.T) {
	root := moduleRoot(t)
	scanned := map[string]bool{}
	for _, f := range agentTypeFiles(t, root) {
		scanned[f] = true
	}
	for _, f := range []string{
		"internal/workspace/panesend.go", "internal/server/pty.go", "internal/team/events.go",
		"internal/server/teamgraph.go", "internal/workspace/wake.go",
	} {
		if !scanned[f] {
			t.Errorf("%s is not scanned", f)
		}
	}
	for f, why := range agentTypeExempt {
		if scanned[f] {
			t.Errorf("%s is exempt and scanned", f)
		}
		if why == "" {
			t.Errorf("%s is exempt with no reason", f)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s is exempt but not there: %v", f, err)
		}
	}
	for f := range agentTypeAllowed {
		if !scanned[f] {
			t.Errorf("%s has allowances but is not scanned", f)
		}
	}
	if len(agentTypeExempt) != agentTypeExemptCount {
		t.Errorf("%d files are exempt, agentTypeExemptCount says %d: an exemption is added on purpose", len(agentTypeExempt), agentTypeExemptCount)
	}
	// Every Go file in the three packages is either scanned or exempt.
	total := 0
	for _, pkg := range agentTypePackages {
		ents, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(pkg)))
		for _, e := range ents {
			if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
				total++
			}
		}
	}
	if total != len(scanned)+len(agentTypeExempt) {
		t.Errorf("%d files, %d scanned and %d exempt", total, len(scanned), len(agentTypeExempt))
	}
}

// A violation planted in a file that was never on a list is found: panesend.go
// has one allowed read of the session and a second use is over its count.
func TestAViolationPlantedInAnUnlistedFileIsFound(t *testing.T) {
	root := moduleRoot(t)
	rel := "internal/workspace/panesend.go"
	full := filepath.Join(root, filepath.FromSlash(rel))
	orig, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	consts := packageConsts(filepath.Dir(full))
	over := func(planted string) int {
		src := append(append([]byte{}, orig...), []byte("\n"+planted+"\n")...)
		uses, err := findAgentTypeUses(rel, src, consts)
		if err != nil {
			t.Fatalf("%v in:\n%s", err, planted)
		}
		found := map[string]int{}
		for _, u := range uses {
			found[u.key()]++
		}
		n := 0
		for k, c := range found {
			if c > agentTypeAllowed[rel][k].N {
				n += c - agentTypeAllowed[rel][k].N
			}
		}
		return n
	}
	if n := over(""); n != 0 {
		t.Fatalf("panesend.go as it is has %d uses over its allowance", n)
	}
	for name, planted := range map[string]string{
		"literal": `func planted(p *Pane) bool { return p.Agent == "claude" }`,
		"alias":   `func planted(p *Pane) bool { a := p.Agent; return a == "claude" }`,
		"session": `func planted(p *Pane) int { return p.Sess.Pid() }`,
	} {
		if n := over(planted); n != 1 {
			t.Errorf("%s: %d violations over the allowance, want 1", name, n)
		}
	}
}
