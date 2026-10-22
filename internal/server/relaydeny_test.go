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

// The pass list is what the phone needs: the roster messages are scrubbed, and
// the settings, device and helper messages pass.
func TestRelayPassListHoldsWhatAPhoneNeeds(t *testing.T) {
	for _, typ := range []string{"hello", "prefs", "keys", "helpers", "worktrees", "recents", "remoteDevices", "jevKey", "changes", "diff", "ghPR"} {
		if _, ok := relayPass[typ]; !ok {
			t.Errorf("%s is not sent to a relay window", typ)
		}
	}
	for _, typ := range []string{"state", "agents", "notice", "batonError", "teamResult", "conversationPage"} {
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
		if got := cutLine(c.in, 50); got != c.want {
			t.Errorf("cutLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	got := cutLine(long, 100)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > 100 || strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") || !strings.HasSuffix(strings.TrimSuffix(got, "…"), "word") {
		t.Errorf("cut at a word = %q (%d characters)", got, len([]rune(got)))
	}
	solid := cutLine(strings.Repeat("x", 500), 100)
	if len([]rune(solid)) != 100 || !strings.HasSuffix(solid, "…") {
		t.Errorf("cut of one long word = %d characters", len([]rune(solid)))
	}
	if got := cutLine(strings.Repeat("日", 100), 100); got != strings.Repeat("日", 100) {
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
			var total int
			var dynamic bool
			var walk func(e ast.Expr)
			walk = func(e ast.Expr) {
				switch x := e.(type) {
				case *ast.BasicLit:
					if s, err := strconv.Unquote(x.Value); err == nil {
						total += len([]rune(s))
					}
				case *ast.Ident:
					if s, ok := consts[x.Name]; ok {
						total += len([]rune(s))
					} else {
						dynamic = true
					}
				case *ast.BinaryExpr:
					walk(x.X)
					walk(x.Y)
				default:
					dynamic = true
				}
			}
			walk(call.Args[0])
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
