package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// Conversation lines in Claude Code's stored shape, cut down to what a check
// reads (see the testdata of internal/session/transcript).
const (
	lineNotification = `{"type":"user","origin":{"kind":"task-notification"},"message":{"role":"user","content":"<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n<summary>Background command \"make\" completed (exit code 0)</summary>\n</task-notification>"}}`
	lineStopCall     = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_stop","name":"TaskStop","input":{"task_id":"b2"}}]}}`
	lineStopDone     = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_stop","content":"stopped"}]}}`
	lineStopFailCall = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_stop2","name":"KillShell","input":{"shell_id":"b3"}}]}}`
	lineStopFailed   = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_stop2","content":"no such shell","is_error":true}]}}`
	lineAgentCall    = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_fg","name":"Agent","input":{"description":"Explore","prompt":"Look","subagent_type":"general-purpose"}}]}}`
	lineAgentDone    = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_fg","content":"Found it."}]}}`
	lineBgAgentCall  = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_bg","name":"Agent","input":{"description":"Run tests","prompt":"Run","run_in_background":true}}]}}`
	lineBgAgentAck   = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bg","content":"Async agent launched."}]}}`
	lineQuoted       = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"<task-notification><task-id>b9</task-id></task-notification>"}]}}`
)

// TestAConversationScanFindsOnlyEvidence covers scanConversation: a task
// notification, a stop the tool answered, and a foreground Agent call
// returning are evidence of an end; a failed stop, a background Agent call's
// immediate answer, and the agent quoting a notification are not.
func TestAConversationScanFindsOnlyEvidence(t *testing.T) {
	var scan backgroundScan
	data := strings.Join([]string{lineNotification, lineStopCall, lineStopFailCall, lineAgentCall, lineBgAgentCall, "not json", lineQuoted, lineStopDone, lineStopFailed, lineAgentDone, lineBgAgentAck}, "\n") + "\n"
	found := scanConversation([]byte(data), &scan)
	ended := map[string]string{}
	for _, e := range found {
		if e.ended {
			ended[e.id] = e.what
		}
	}
	if len(ended) != 2 || !strings.Contains(ended["b1"], "task notification") || !strings.Contains(ended["b2"], "TaskStop") {
		t.Fatalf("ended = %v, want b1 by its notification and b2 by its stop", ended)
	}
	if !scan.returned["toolu_fg"] || scan.returned["toolu_bg"] {
		t.Errorf("returned calls = %v, want only the foreground one", scan.returned)
	}
}

// TestAConversationIsReadAWholeLineAtATime covers scanConversationFile's
// offset: a line still being written is left for the next check, what was
// read is not read again, and a file replaced by a shorter one is read anew.
func TestAConversationIsReadAWholeLineAtATime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.jsonl")
	var scan backgroundScan
	half := lineNotification[:40]
	if err := os.WriteFile(path, []byte(lineStopCall+"\n"+half), 0o600); err != nil {
		t.Fatal(err)
	}
	found, off := scanConversationFile(path, 0, &scan)
	if len(found) != 0 || off != int64(len(lineStopCall)+1) {
		t.Fatalf("first read: %v at %d", found, off)
	}
	if err := os.WriteFile(path, []byte(lineStopCall+"\n"+lineNotification+"\n"+lineStopDone+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, off2 := scanConversationFile(path, off, &scan)
	if len(found) != 2 {
		t.Fatalf("second read found %v, want the notification and the stop (its call read before)", found)
	}
	if found, _ := scanConversationFile(path, off2, &scan); len(found) != 0 {
		t.Errorf("a read with nothing new found %v", found)
	}
	if err := os.WriteFile(path, []byte(lineNotification+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, _ := scanConversationFile(path, off2, &scan); len(found) != 1 {
		t.Errorf("a replaced file was not read anew: %v", found)
	}
}

// claudeConversation makes a stored conversation for a pane where a Claude
// Code pane's reader looks for it, under CLAUDE_CONFIG_DIR in a temp dir.
func claudeConversation(t *testing.T, conv string, lines ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", "C--repo")
	if err := os.MkdirAll(filepath.Join(dir, conv, "subagents"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, conv+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestIdleBackgroundWorkIsCheckedAgainstTheConversation runs the check end to
// end on a fake clock: nothing is due before BackgroundCheckInterval; then
// work the conversation shows ended is dropped with its evidence, a subagent
// whose transcript is being written is confirmed running and kept, one whose
// foreground call returned is dropped, and a shell nothing says anything of
// is kept and, after the grace, marked unverified -- which, with nothing else
// verified, has the idle agent read idle rather than working while it is
// still counted.
func TestIdleBackgroundWorkIsCheckedAgainstTheConversation(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "a")
	conv := ws.conversationOf(p)
	path := claudeConversation(t, conv, lineNotification, lineAgentCall, lineAgentDone)
	subs := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(subs, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("agent-afg.meta.json", `{"agentType":"general-purpose","description":"Explore","toolUseId":"toolu_fg"}`)
	write("agent-afg.jsonl", "{}\n")
	write("agent-arun.meta.json", `{"agentType":"general-purpose","description":"Run tests","toolUseId":"toolu_bg"}`)
	write("agent-arun.jsonl", "{}\n")

	p.Sess.SetStatus(session.StatusIdle, "")
	for _, id := range []string{"shell:b1", "agent:afg", "agent:arun", "shell:quiet"} {
		p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, id)
	}
	if PaneActivity(p) != ActivityWorking {
		t.Fatalf("an idle agent with work just started reads %q", PaneActivity(p))
	}

	now := time.Now()
	if jobs := ws.DueBackgroundChecks(now); len(jobs) != 0 {
		t.Fatalf("a check was due at once: %v", jobs)
	}
	now = now.Add(BackgroundCheckInterval)
	jobs := ws.DueBackgroundChecks(now)
	if len(jobs) != 1 || jobs[0].PaneID != p.ID {
		t.Fatalf("due checks = %v, want this pane's", jobs)
	}
	if again := ws.DueBackgroundChecks(now); len(again) != 0 {
		t.Fatal("a pane already being checked was handed out again")
	}
	// The running subagent's transcript was just written.
	_ = os.Chtimes(filepath.Join(subs, "agent-arun.jsonl"), now, now)
	ws.RunBackgroundCheck(jobs[0], now)

	left := map[string]session.BackgroundWork{}
	for _, w := range p.Sess.BackgroundWork() {
		left[w.ID] = w
	}
	if len(left) != 2 || left["agent:arun"].ConfirmedBy != "its transcript is being written" || left["shell:quiet"].ID == "" {
		t.Fatalf("left counted = %+v, want the running subagent (confirmed) and the quiet shell", left)
	}
	ended := map[string]string{}
	for _, e := range p.Sess.BackgroundEnded() {
		ended[e.ID] = e.Evidence
	}
	if !strings.Contains(ended["shell:b1"], "task notification") || !strings.Contains(ended["agent:afg"], "Agent call returned") {
		t.Fatalf("ended = %v", ended)
	}

	// A check that found something is due again at the base interval; one
	// that finds nothing backs off.
	if jobs := ws.DueBackgroundChecks(now.Add(BackgroundCheckInterval - time.Second)); len(jobs) != 0 {
		t.Fatal("checked again before the interval")
	}
	now = now.Add(BackgroundCheckInterval)
	jobs = ws.DueBackgroundChecks(now)
	if len(jobs) != 1 {
		t.Fatal("not checked again after the interval")
	}
	ws.RunBackgroundCheck(jobs[0], now.Add(backgroundSubagentFresh))
	if jobs := ws.DueBackgroundChecks(now.Add(backgroundSubagentFresh + BackgroundCheckInterval)); len(jobs) != 0 {
		t.Fatal("a check that found nothing did not back off")
	}

	// Past the grace with the subagent's transcript gone quiet: everything
	// is unverified, still counted, and the pane reads idle.
	now = now.Add(session.BackgroundUnverifiedGrace + time.Minute)
	ws.DueBackgroundChecks(now)
	if n := p.Sess.BackgroundTasks(); n != 2 {
		t.Fatalf("unverified work was dropped: %d left", n)
	}
	if p.Sess.BackgroundVerified() != 0 {
		t.Fatalf("work with no sign for the grace is still verified: %+v", p.Sess.BackgroundWork())
	}
	if PaneActivity(p) != ActivityIdle {
		t.Errorf("an idle agent with only unverified work reads %q", PaneActivity(p))
	}

	// Claude Code's own list at a turn's end is the freshest word: what it
	// names is verified again, what it leaves out has ended.
	p.Sess.SetBackgroundWork([]string{"shell:quiet"}, nil)
	if PaneActivity(p) != ActivityWorking || p.Sess.BackgroundTasks() != 1 {
		t.Errorf("after a Stop's list: %q with %d counted", PaneActivity(p), p.Sess.BackgroundTasks())
	}
}

// TestBusyOrClearedPanesAreNotChecked covers what DueBackgroundChecks leaves
// alone: a pane that is working, and one whose conversation was cleared.
func TestBusyOrClearedPanesAreNotChecked(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "a")
	claudeConversation(t, ws.conversationOf(p), lineNotification)
	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	p.Sess.SetStatus(session.StatusWorking, "")
	now := time.Now().Add(time.Hour)
	if jobs := ws.DueBackgroundChecks(now); len(jobs) != 0 {
		t.Fatal("a working pane was checked")
	}
	p.Sess.SetStatus(session.StatusIdle, "")
	p.Sess.NoteBackground("SessionStart", "", "")
	if jobs := ws.DueBackgroundChecks(now.Add(time.Hour)); len(jobs) != 0 {
		t.Fatal("a pane with nothing counted was checked")
	}
}
