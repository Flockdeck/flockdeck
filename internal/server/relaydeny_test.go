package server

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A relay window is sent a type only if it has a scrub or is on relayPass: any
// other type, one that cannot be read, and one with no type are dropped.
func TestRelayScrubIsDefaultDeny(t *testing.T) {
	for _, raw := range []string{
		`{"type":"somethingNew","text":"x"}`,
		`{"type":"","text":"x"}`,
		`{"text":"no type"}`,
		`not json`,
		``,
		`{"type":7}`,
	} {
		if got := scrubForRemote([]byte(raw)); got != nil {
			t.Errorf("%q was sent: %s", raw, got)
		}
	}
	for typ := range relayPass {
		raw := []byte(`{"type":"` + typ + `","x":1}`)
		if got := scrubForRemote(raw); !bytes.Equal(got, raw) {
			t.Errorf("%s on relayPass was changed or dropped: %s", typ, got)
		}
	}
	for typ := range relayDeskOnly {
		if got := scrubForRemote([]byte(`{"type":"` + typ + `","x":1}`)); got != nil {
			t.Errorf("%s is desk only and was sent: %s", typ, got)
		}
	}
	for typ := range relayPass {
		if _, ok := relayScrubs[typ]; ok {
			t.Errorf("%s is both scrubbed and passed as it is", typ)
		}
		if _, ok := relayDeskOnly[typ]; ok {
			t.Errorf("%s is both passed and desk only", typ)
		}
	}
}

// A gitWarn is the question a repository's programs raise for the window that ran
// the git command. It is not on the pass list and a relay window is sent nothing
// for it, whatever it holds.
func TestGitWarnIsNeverSentToARelayWindow(t *testing.T) {
	if _, ok := relayPass["gitWarn"]; ok {
		t.Error("gitWarn is on the pass list")
	}
	if _, ok := relayScrubs["gitWarn"]; !ok {
		t.Error("gitWarn has no scrub that drops it")
	}
	raw, err := json.Marshal(gitWarnMsg{Type: "gitWarn", Cwd: "/work/repo", Path: "/work/repo", Action: "commit"})
	if err != nil {
		t.Fatal(err)
	}
	if got := scrubForRemote(raw); got != nil {
		t.Errorf("a relay window was sent a gitWarn: %s", got)
	}
}

// The pass list is what the phone needs: the roster messages are scrubbed, and
// the settings, device and helper messages pass.
func TestRelayPassListHoldsWhatAPhoneNeeds(t *testing.T) {
	// Every type sent to a relay window as it is, each pinned here by name, so
	// moving one off the list (to desk only, or to a scrub) is a change to this
	// test. agentStarted and attachImageResult are what the phone's start-agent
	// flow and its chat read.
	pinned := []string{
		"hello", "prefs", "keys", "keyTable", "artifacts", "agentAddress", "radarSupport", "jevKey",
		"remotePair", "remoteOutcome", "remoteDevices", "versions", "helpers", "helperPlan",
		"batonSaved", "attachImageResult", "browse", "recents", "worktrees",
		"ghProgress", "ghStatus", "agentStarted", "detached",
		"changes", "diff", "ghPR", "ghPRs", "ghIssue", "ghIssues", "ghChecks",
	}
	for _, typ := range pinned {
		if _, ok := relayPass[typ]; !ok {
			t.Errorf("%s is not sent to a relay window", typ)
		}
	}
	if len(relayPass) != len(pinned) {
		t.Errorf("relayPass has %d types and %d are pinned here: pin the new one", len(relayPass), len(pinned))
	}
	for _, typ := range []string{"state", "agents", "notice", "batonError", "teamResult", "permissionAnswer", "conversationPage"} {
		if _, ok := relayScrubs[typ]; !ok {
			t.Errorf("%s has no scrub", typ)
		}
	}
}

// A team result reaches a relay window only as the refusal of a team command.
func TestRelayTeamResultIsOnlyTheRefusal(t *testing.T) {
	if got := scrubForRemote(mustJSON(t, teamResultMsg{Type: "teamResult", Req: "r", Op: "taskList", OK: true, Pane: planted})); got != nil {
		t.Errorf("an answer with data was sent: %s", got)
	}
	if got := scrubForRemote(mustJSON(t, teamResultMsg{Type: "teamResult", Req: "r", Op: "taskList", OK: true, Error: "also an error", Pane: planted})); got != nil {
		t.Errorf("an answer that says ok was sent: %s", got)
	}
	if out := scrubForRemote(mustJSON(t, teamResultMsg{Type: "teamResult", Op: "taskAdd", Error: "failed " + relaySecrets[0]})); len(relayLeaks(out)) > 0 {
		t.Errorf("the refusal holds a secret: %s", out)
	}
	out := scrubForRemote(mustJSON(t, teamResultMsg{Type: "teamResult", Req: "r", Op: "taskAdd", Error: teamDeskOnly, Code: teamDeskOnlyCode,
		Warning: planted, Skipped: []string{planted}, Pane: planted}))
	if bytes.Contains(out, []byte(planted)) {
		t.Errorf("the refusal holds the marker: %s", out)
	}
	var back teamResultMsg
	if err := json.Unmarshal(out, &back); err != nil || back.OK || back.Req != "r" || back.Op != "taskAdd" || back.Error != teamDeskOnly || back.Code != teamDeskOnlyCode {
		t.Errorf("refusal = %+v, %v", back, err)
	}
	if scrubForRemote([]byte(`{"type":"teamResult","ok":false}`)) != nil {
		t.Error("a result with no error was sent")
	}
}

func TestCutLine(t *testing.T) {
	long := strings.Repeat("word ", 200)
	for _, c := range []struct{ in, want string }{
		{"short", "short"},
		{"a\nb", "a b"},
		{"a\r\n\r\n  b\t c", "a b c"},
		{"  lead and trail \n", "lead and trail"},
		{"a\x00b", "a b"},
		{"", ""},
	} {
		if got := safeLine(c.in, 50); got != c.want {
			t.Errorf("safeLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	got := safeLine(long, 100)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > 100 || strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") || !strings.HasSuffix(strings.TrimSuffix(got, "…"), "word") {
		t.Errorf("cut at a word = %q (%d characters)", got, len([]rune(got)))
	}
	solid := safeLine(strings.Repeat("x", 500), 100)
	if len([]rune(solid)) != 100 || !strings.HasSuffix(solid, "…") {
		t.Errorf("cut of one long word = %d characters", len([]rune(solid)))
	}
	if got := safeLine(strings.Repeat("日", 100), 100); got != strings.Repeat("日", 100) {
		t.Errorf("a line of exactly the limit was cut")
	}
}

// A notice with a line break in it keeps its words apart, and one too long ends in
// an ellipsis at a word, not in the middle of a sentence.
func TestRelayNoticeIsCutAtAWord(t *testing.T) {
	out := scrubForRemote(mustJSON(t, noticeMsg{Type: "notice", Error: true, Text: "disk full:\nC:\\state\\layout.json " + strings.Repeat("because it is full ", 60)}))
	var n noticeMsg
	if err := json.Unmarshal(out, &n); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(n.Text, "disk full: C:\\state\\layout.json because") || !strings.HasSuffix(n.Text, "full…") || len([]rune(n.Text)) > maxRemoteNotice {
		t.Errorf("notice = %q", n.Text)
	}
}

// No fixed notice is cut on its way to a relay window: the fixed words of every
// notify, notifyAll and notifyApproval call fit in maxRemoteNotice with room for
// the error text some of them add. A longer notice is shortened here, not cut
// mid-sentence on the phone.
func TestNoFixedNoticeIsLongerThanTheRelayLimit(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no files: %v", err)
	}
	consts := map[string]string{}
	var parsed []*ast.File
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, file)
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, d := range file.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, s := range g.Specs {
				v, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range v.Names {
					if i < len(v.Values) {
						if bl, ok := v.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
							if s, err := strconv.Unquote(bl.Value); err == nil {
								consts[name.Name] = s
							}
						}
					}
				}
			}
		}
	}
	// textOf counts the fixed characters in an expression that makes a notice: a
	// literal, a constant, a format string given to Sprintf or Errorf, and the
	// longest return of a function in this package that returns a string (called
	// by name or as a method). Anything else is text that is not known.
	funcText := map[string]int{}
	var textOf func(e ast.Expr, depth int) (int, bool)
	textOf = func(e ast.Expr, depth int) (int, bool) {
		switch x := e.(type) {
		case *ast.BasicLit:
			if s, err := strconv.Unquote(x.Value); err == nil {
				return len([]rune(s)), false
			}
		case *ast.Ident:
			if s, ok := consts[x.Name]; ok {
				return len([]rune(s)), false
			}
		case *ast.ParenExpr:
			return textOf(x.X, depth)
		case *ast.BinaryExpr:
			a, da := textOf(x.X, depth)
			b, db := textOf(x.Y, depth)
			return a + b, da || db
		case *ast.CallExpr:
			name := ""
			switch fn := x.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			if (name == "Sprintf" || name == "Errorf") && len(x.Args) > 0 {
				n, _ := textOf(x.Args[0], depth)
				return n, true
			}
			if n, ok := funcText[name]; ok && depth < 3 {
				return n, true
			}
		}
		return 0, true
	}
	for pass := 0; pass < 3; pass++ {
		for _, file := range parsed {
			for _, d := range file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil || fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
					continue
				}
				if id, ok := fd.Type.Results.List[0].Type.(*ast.Ident); !ok || id.Name != "string" {
					continue
				}
				best := 0
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) == 1 {
						if n, _ := textOf(r.Results[0], pass); n > best {
							best = n
						}
					}
					return true
				})
				funcText[fd.Name.Name] = best
			}
		}
	}
	// Room left for the error text a notice appends. The layout-save notice adds
	// an operating system error, which is a path and a few words.
	const room = 150
	var checked, longest int
	for _, file := range parsed {
		pos := fset.Position(file.Pos()).Filename
		if strings.HasSuffix(pos, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "notify" && sel.Sel.Name != "notifyAll" && sel.Sel.Name != "notifyApproval" && sel.Sel.Name != "noticeDesk") {
				return true
			}
			// A notice that is only ever sent from a command a relay window is
			// refused first is long on purpose and never reaches one.
			if c, ok := call.Args[0].(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "artifactAckText" {
					// setRemoteArtifacts returns at refusedArtifactsThroughRelay before it.
					return true
				}
			}
			total, dynamic := textOf(call.Args[0], 0)
			checked++
			limit := maxRemoteNotice
			if dynamic {
				limit -= room
			}
			if total > longest {
				longest = total
			}
			if total > limit {
				t.Errorf("%s:%d: a notice with %d fixed characters (dynamic text after it: %v) would be cut at %d for a relay window: shorten it", pos, fset.Position(call.Pos()).Line, total, dynamic, maxRemoteNotice)
			}
			return true
		})
	}
	if checked < 50 {
		t.Fatalf("checked only %d notices: the scan is looking in the wrong place", checked)
	}
	t.Logf("%d notices checked, the longest has %d fixed characters", checked, longest)
}

// A permission answer reaches a relay window only as a refusal with its reason
// redacted and cut; an answer that was given, or has no reason, is dropped.
func TestRelayPermissionAnswerIsOnlyTheRefusal(t *testing.T) {
	if scrubForRemote(mustJSON(t, permissionAnswerMsg{Type: "permissionAnswer", Req: "r", ID: "p", OK: true})) != nil {
		t.Error("an answer that was given was sent")
	}
	if scrubForRemote(mustJSON(t, permissionAnswerMsg{Type: "permissionAnswer", Req: "r", ID: "p", OK: true, Error: "x"})) != nil {
		t.Error("an answer that says ok and has an error was sent")
	}
	if scrubForRemote(mustJSON(t, permissionAnswerMsg{Type: "permissionAnswer", Req: "r", ID: "p"})) != nil {
		t.Error("a refusal with no reason was sent")
	}
	out := scrubForRemote(mustJSON(t, permissionAnswerMsg{Type: "permissionAnswer", Req: "r", ID: "p", CardSeq: 4, RequestID: "q",
		Error: "The answer could not be given: " + relaySecrets[0] + " " + strings.Repeat("because it is full ", 60)}))
	if leaks := relayLeaks(out); len(leaks) > 0 {
		t.Errorf("the refusal holds %q", leaks)
	}
	var back permissionAnswerMsg
	if err := json.Unmarshal(out, &back); err != nil || back.OK || back.Req != "r" || back.ID != "p" || back.CardSeq != 4 || back.RequestID != "q" ||
		!strings.HasPrefix(back.Error, "The answer could not be given: ") || len([]rune(back.Error)) > maxRemoteNotice {
		t.Errorf("refusal = %+v, %v", back, err)
	}
}

// A message sent as it is has exactly one key called type, whatever its case, so
// the type this side reads is the one the phone reads.
func TestRelayPassNeedsOneTypeKey(t *testing.T) {
	ok := `{"type":"prefs","a":1}`
	if got := scrubForRemote([]byte(ok)); string(got) != ok {
		t.Fatalf("a good message was changed or dropped: %s", got)
	}
	for _, bad := range []string{
		`{"type":"prefs","type":"state"}`,
		`{"type":"prefs","a":1,"type":"conversationAppend"}`,
		`{"type":"prefs","Type":"changes"}`,
		`{"type":"prefs","TYPE":"x"}`,
		`{"type":"prefs"} {"type":"x"}`,
		`{"type":"prefs"}x`,
		`[{"type":"prefs"}]`,
		`{"type":"prefs","a":`,
	} {
		if got := scrubForRemote([]byte(bad)); got != nil {
			t.Errorf("%s was sent: %s", bad, got)
		}
	}
	if hasOneTypeKey([]byte(`{"a":{"type":"x"},"type":"prefs"}`)) != true {
		t.Error("a type key inside a nested object was counted")
	}
}

// Format characters, such as a right-to-left override, are dropped from a notice.
func TestCutLineDropsFormatCharacters(t *testing.T) {
	if got := safeLine("a‮b‍c⁦d", 50); got != "abcd" {
		t.Errorf("cutLine = %q", got)
	}
	if got := safeLine("a ‮ b", 50); got != "a b" {
		t.Errorf("cutLine = %q", got)
	}
}

// A format character inside a secret must not hide it from the redactor: the
// line is normalised first, so the secret is whole when it is redacted.
func TestRelayRedactsASecretSplitByAFormatCharacter(t *testing.T) {
	split := func(s string, mark string) string { return s[:2] + mark + s[2:] }
	for _, mark := range []string{"​", "‮", "⁠"} {
		for _, secret := range relaySecrets[:3] {
			hidden := split(secret, mark)
			for name, raw := range map[string][]byte{
				"notice":           mustJSON(t, noticeMsg{Type: "notice", Text: "failed with " + hidden, Error: true}),
				"batonError":       mustJSON(t, batonErrorMsg{Type: "batonError", PaneID: "p", Req: "r", Cmd: "saveBaton", Error: "bad " + hidden}),
				"teamResult":       mustJSON(t, teamResultMsg{Type: "teamResult", Op: "taskAdd", Error: "failed " + hidden}),
				"permissionAnswer": mustJSON(t, permissionAnswerMsg{Type: "permissionAnswer", Req: "r", ID: "p", Error: "no " + hidden}),
			} {
				out := scrubForRemote(raw)
				if out == nil {
					t.Errorf("%s with %q was dropped", name, mark)
					continue
				}
				if leaks := relayLeaks(out); len(leaks) > 0 {
					t.Errorf("%s with %U inside %q sent %q", name, []rune(mark)[0], secret, leaks)
				}
			}
		}
	}
}
