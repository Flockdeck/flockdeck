package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestWaitingViewsBuildsTheQuestionAskUserQuestionIsAsking covers what reaches
// the phone for an AskUserQuestion call: the question, its header, every
// option with its description, and whether it takes several -- straight from
// the same input carried on the state push (see paneView.Ask), not from
// reading a screen.
func TestWaitingViewsBuildsTheQuestionAskUserQuestionIsAsking(t *testing.T) {
	toolInput := `{"questions":[{"header":"Colour","question":"Which colour should the Save button be?",
		"multiSelect":false,"options":[{"label":"Red","description":"Warm, and it stands out"},
		{"label":"Blue","description":"The current default"}]}]}`
	ask, perm := waitingViews("AskUserQuestion", toolInput)
	if perm != nil {
		t.Errorf("permission = %+v, want nil for an AskUserQuestion call", perm)
	}
	if ask == nil || len(ask.Questions) != 1 {
		t.Fatalf("ask = %+v, want one question", ask)
	}
	q := ask.Questions[0]
	if q.Header != "Colour" || q.Question != "Which colour should the Save button be?" {
		t.Errorf("question = %+v, want the header and text carried through", q)
	}
	if len(q.Options) != 2 || q.Options[0].Label != "Red" || q.Options[0].Description != "Warm, and it stands out" {
		t.Errorf("options = %+v, want both options with their descriptions", q.Options)
	}
}

// TestWaitingViewsCarriesEveryQuestionOfAMultiQuestionCall covers
// AskUserQuestion's own "tabs" form -- several questions asked in one call,
// Claude Code's own strip of tabs across the top of the screen -- which the
// phone needs every one of, with its own header, options and whether it
// takes several, to answer the whole call rather than only the first tab.
// The tool_input below is shaped exactly as Claude Code 2.1.270 sent it for
// a real three-question call, one of them multiSelect, made against a
// scratch pane while confirming the terminal's own key sequence (see the
// scratchpad's multi-ask/NOTES.md).
func TestWaitingViewsCarriesEveryQuestionOfAMultiQuestionCall(t *testing.T) {
	toolInput := `{"questions":[
		{"header":"Fruit","question":"Pick a fruit","multiSelect":false,
		 "options":[{"label":"Apple"},{"label":"Pear"}]},
		{"header":"Toppings","question":"Pick toppings","multiSelect":true,
		 "options":[{"label":"Nuts"},{"label":"Cream"}]},
		{"header":"Drink","question":"Pick a drink","multiSelect":false,
		 "options":[{"label":"Tea"},{"label":"Coffee"}]}
	]}`
	ask, perm := waitingViews("AskUserQuestion", toolInput)
	if perm != nil {
		t.Errorf("permission = %+v, want nil for an AskUserQuestion call", perm)
	}
	if ask == nil || len(ask.Questions) != 3 {
		t.Fatalf("ask = %+v, want all three questions carried through", ask)
	}
	if ask.Questions[0].Header != "Fruit" || ask.Questions[0].MultiSelect {
		t.Errorf("question 1 = %+v, want Fruit, single-select", ask.Questions[0])
	}
	if ask.Questions[1].Header != "Toppings" || !ask.Questions[1].MultiSelect {
		t.Errorf("question 2 = %+v, want Toppings, multiSelect", ask.Questions[1])
	}
	if len(ask.Questions[1].Options) != 2 || ask.Questions[1].Options[1].Label != "Cream" {
		t.Errorf("question 2 options = %+v, want Nuts and Cream", ask.Questions[1].Options)
	}
	if ask.Questions[2].Header != "Drink" || ask.Questions[2].Question != "Pick a drink" {
		t.Errorf("question 3 = %+v, want Drink carried through", ask.Questions[2])
	}
}

// TestWaitingViewsBuildsABashPermission covers a Bash permission prompt: the
// command it wants to run, straight from the PreToolUse call, before the
// tool has even run.
func TestWaitingViewsBuildsABashPermission(t *testing.T) {
	ask, perm := waitingViews("Bash", `{"command":"echo second > out2.txt","description":"Write second to out2.txt"}`)
	if ask != nil {
		t.Errorf("ask = %+v, want nil for a Bash permission", ask)
	}
	if perm == nil {
		t.Fatal("permission is nil, want a view of the Bash call")
	}
	if perm.Tool != "Bash" || perm.Command != "echo second > out2.txt" || perm.Description != "Write second to out2.txt" {
		t.Errorf("permission = %+v, want the command and description carried through", perm)
	}
}

// TestWaitingViewsBuildsAnEditPermissionWithADiff covers an Edit permission
// prompt: the file and a diff built from the call's own before/after text,
// the same diff a finished Edit row would show, available before the edit
// has run at all.
func TestWaitingViewsBuildsAnEditPermissionWithADiff(t *testing.T) {
	_, perm := waitingViews("Edit", `{"filePath":"push.go","oldString":"a","newString":"b"}`)
	if perm == nil || perm.File != "push.go" {
		t.Fatalf("permission = %+v, want the file named", perm)
	}
	if perm.Diff == "" {
		t.Fatal("diff is empty, want the change rendered inline")
	}
	for _, want := range []string{"-a", "+b"} {
		if !contains(perm.Diff, want) {
			t.Errorf("diff = %q, want it to contain %q", perm.Diff, want)
		}
	}
}

// TestWaitingViewsIsNilForAnIdleNudge covers a pane simply nudged that
// nobody has answered it in a while: no tool named, no input carried, so
// there is nothing to build a card from -- the chat view falls back to the
// screen-read choices in that case.
func TestWaitingViewsIsNilForAnIdleNudge(t *testing.T) {
	ask, perm := waitingViews("", "")
	if ask != nil || perm != nil {
		t.Errorf("ask = %+v, permission = %+v, want both nil", ask, perm)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestAWaitingPaneCarriesItsAskAndPermissionInTheStatePush is the shape test
// the brief asks for: a pane's Ask/Permission reach the phone as fields on
// its state-push pane view, built from the same PreToolUse input the hooks
// package carries on Session -- this is the desktop-side half of the
// protocol both repos are built to (see convo-protocol.md).
func TestAWaitingPaneCarriesItsAskAndPermissionInTheStatePush(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	id := firstPane(t, srv, ws)

	p, _ := ask(srv, func() *workspace.Pane { return ws.Pane(id) })
	p.Sess.SetStatusFull(session.StatusWaiting, "AskUserQuestion",
		`{"questions":[{"header":"Colour","question":"Which colour?","options":[{"label":"Red"}]}]}`)

	st := nextState(t, conn, func(s stateMsg) bool {
		pv, ok := s.Panes[id]
		return ok && pv.Ask != nil
	})
	pv := st.Panes[id]
	if pv.Status != "waiting" {
		t.Errorf("status = %q, want waiting", pv.Status)
	}
	if pv.Permission != nil {
		t.Errorf("permission = %+v, want nil alongside an Ask", pv.Permission)
	}
	if len(pv.Ask.Questions) != 1 || pv.Ask.Questions[0].Question != "Which colour?" {
		t.Errorf("ask = %+v, want the question carried through", pv.Ask)
	}

	// A permission prompt for an ordinary tool instead.
	p.Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"echo hi"}`)
	st = nextState(t, conn, func(s stateMsg) bool {
		pv, ok := s.Panes[id]
		return ok && pv.Permission != nil
	})
	pv = st.Panes[id]
	if pv.Ask != nil {
		t.Errorf("ask = %+v, want nil alongside a Permission", pv.Ask)
	}
	if pv.Permission.Tool != "Bash" || pv.Permission.Command != "echo hi" {
		t.Errorf("permission = %+v, want the Bash command carried through", pv.Permission)
	}

	// Leaving the wait drops both.
	p.Sess.SetStatusFull(session.StatusWorking, "", "")
	st = nextState(t, conn, func(s stateMsg) bool {
		pv, ok := s.Panes[id]
		return ok && pv.Status == "working"
	})
	pv = st.Panes[id]
	if pv.Ask != nil || pv.Permission != nil {
		t.Errorf("ask = %+v, permission = %+v, want both nil once the pane is working again", pv.Ask, pv.Permission)
	}
}
