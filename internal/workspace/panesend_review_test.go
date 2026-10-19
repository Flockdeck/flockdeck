package workspace

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/route"
	"github.com/jmwri/flockdeck/internal/session"
)

func newSendTestWorkspace(t *testing.T) (*Workspace, *Pane) {
	t.Helper()
	isolateConfig(t)
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	tab := ws.NewTab(session.KindShell, root, "shell")
	return ws, ws.Pane(tab.Tree.Panes()[0])
}

// Text that is only control characters is nothing once they are stripped, and
// must not be written as a bare Enter.
func TestSendToPaneRefusesTextThatIsOnlyControlCharacters(t *testing.T) {
	ws, p := newSendTestWorkspace(t)
	for _, text := range []string{"\x1b", "\x00", "\x1b\x7f", "\u0085\u009b", " \x1b \n"} {
		if _, err := ws.SendToPane(p.ID, text, true); err != ErrSendEmpty {
			t.Errorf("SendToPane(%q) err = %v, want ErrSendEmpty", text, err)
		}
	}
}

// A permission prompt is answered with its choice buttons, so text is refused;
// a question put to the user takes the text and says so.
func TestSendToPaneAndWaitingPanes(t *testing.T) {
	ws, p := newSendTestWorkspace(t)
	p.Kind = session.KindAgent

	p.Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"rm -rf x"}`)
	if _, err := ws.SendToPane(p.ID, "1", false); err != ErrSendPermission {
		t.Errorf("permission prompt: err = %v, want ErrSendPermission", err)
	}
	p.Sess.SetStatusFull(session.StatusWaiting, "AskUserQuestion", `{"questions":[{"question":"which?"}]}`)
	res, err := ws.SendToPane(p.ID, "the second", false)
	if err != nil || !res.Waiting {
		t.Errorf("question: res %+v err %v, want sent with Waiting", res, err)
	}
	for deadline := time.Now().Add(10 * time.Second); sendingNow(ws, p); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the write did not finish")
		}
	}
	// An idle nudge names no tool: it is not a prompt to refuse.
	p.Sess.SetStatus(session.StatusIdle, "")
	p.Sess.SetStatus(session.StatusWaiting, "")
	if res, err := ws.SendToPane(p.ID, "go on", false); err != nil || res.Waiting {
		t.Errorf("idle nudge: res %+v err %v, want sent, not Waiting", res, err)
	}
}

// The in-flight flag is cleared when the write panics, so one bad write does
// not leave the pane refusing every send after it.
func TestSendToPaneFlagIsClearedAfterAPanic(t *testing.T) {
	ws, p := newSendTestWorkspace(t)

	old := typeIntoPane
	typeIntoPane = func(*session.Session, string, bool) { panic("write failed") }
	t.Cleanup(func() { typeIntoPane = old })

	if !ws.claimSend(p) {
		t.Fatal("could not claim the pane")
	}
	func() {
		defer func() { _ = recover() }()
		ws.writeToPane(p, p.Sess, "hi")
	}()
	if sendingNow(ws, p) {
		t.Error("the in-flight flag was still set after the write panicked")
	}
}

// A pane opened by hand and sent its first prompt through SendToPane is routed
// the way the prompt bar would route it, and is left alone when no rule matches.
func TestSendToPaneRoutesAFirstTurn(t *testing.T) {
	ws, root := turnTestWorkspace(t, agent.RoutingPolicy{
		Mode: agent.RoutingAuto,
		Rules: []agent.RoutingRule{
			{Name: "hard turn work", Tier: agent.TierTop, When: agent.RuleMatch{Kind: route.KindTurn, Task: "race"}},
		},
	})
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "openai", Model: "gpt-5.6-luna"}, root, "")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	firstLaunch := p.launch

	res, err := ws.SendToPane(p.ID, "fix the race in the session store", false)
	if err != nil || !res.Routed {
		t.Fatalf("res %+v err %v, want sent and routed", res, err)
	}
	if p.Routed != "hard turn work" || p.launch == firstLaunch {
		t.Errorf("Routed = %q, restarted = %v; want the rule's name and a restart", p.Routed, p.launch != firstLaunch)
	}
	if sendingNow(ws, p) {
		t.Error("a routed send left the in-flight flag set")
	}

	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "openai", Model: "gpt-5.6-luna"}, root, "")
	q := ws.FocusedPane()
	launch := q.launch
	if res, err := ws.SendToPane(q.ID, "write the changelog entry", false); err != nil || res.Routed || q.launch != launch {
		t.Errorf("unmatched prompt: res %+v err %v restarted %v, want typed with no restart", res, err, q.launch != launch)
	}
}
