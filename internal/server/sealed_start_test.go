package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// Stage 5 of /ws/sealed (API 1.39, cap sealedStart): the start flows, history
// and worktrees. These tests hold the tiers, the full-role rule for the new
// reads, the per-device limit on what starts a process, the known-projects rule
// that the relay path already had, and what a reply is allowed to carry.

// What is on the socket for stage 5, and what is not.
func TestSealedStartTables(t *testing.T) {
	reads := []string{"conversations", "worktrees", "recents", "browse", "structuredSessions"}
	acts := []string{"resumeConversation", "worktreeAdd", "structuredResume", "structuredNewConversation"}
	for _, n := range reads {
		if sealedCommands[n] != sealedTierA || !sealedFullOnly[n] {
			t.Errorf("%s: tier %q, full only %v, want A and full only", n, sealedCommands[n], sealedFullOnly[n])
		}
	}
	for _, n := range acts {
		if sealedCommands[n] != sealedTierB {
			t.Errorf("%s is tier %q, want B", n, sealedCommands[n])
		}
	}
	for n := range sealedFullOnly {
		if sealedCommands[n] != sealedTierA {
			t.Errorf("%s is full only but not tier A", n)
		}
	}
	for n := range sealedSpawning {
		if sealedCommands[n] != sealedTierB {
			t.Errorf("%s is limited as a spawn but is not tier B", n)
		}
	}
	// Destroying work, rewinding it and the git that runs programs are not here:
	// they wait for a confirm at the desk or stay with it.
	for _, n := range []string{"worktreeRemove", "worktreePrune", "structuredRewind", "structuredFork", "structuredRewindFiles",
		"gitFetch", "mergeBranch", "approveMerge", "openProject", "newTab", "splitPane", "spawn", "structuredSet"} {
		if sealedCommands[n] != "" {
			t.Errorf("%s is on the sealed socket", n)
		}
	}
	for _, typ := range []string{"conversations", "worktrees", "recents", "browse"} {
		if sealedMessages[typ].Tier != sealedTierA {
			t.Errorf("message %s is tier %q", typ, sealedMessages[typ].Tier)
		}
	}
}

// A viewer, even one let watch panes, is not let list a folder's conversations,
// the repo's branches, the recent projects or the folders of the machine; a
// full device with no switch is let, since they are reads.
func TestSealedStartReadsAreForFullDevices(t *testing.T) {
	e := newSealedEnv(t)
	p, _ := e.open(t, "watcher")
	for _, n := range []string{"conversations", "worktrees", "recents", "browse", "structuredSessions"} {
		p.send(command{Cmd: n, ID: "x", Req: "r-" + n})
		got := p.readType("sealedRefused")
		if got["code"] != sealedCodeRole || got["cmd"] != n || got["req"] != "r-"+n {
			t.Errorf("a watcher's %s was answered %v", n, got)
		}
	}

	f, hello := e.open(t, "full")
	if hello["tier"] != sealedTierA {
		t.Fatalf("tier = %v", hello["tier"])
	}
	for _, n := range []string{"conversations", "worktrees", "recents", "browse", "structuredSessions"} {
		if !helloCommands(hello)[n] {
			t.Errorf("%s is not in a full device's hello", n)
		}
	}
	f.send(command{Cmd: "recents", Req: "rc"})
	if got := f.readType("recents"); got["type"] != "recents" {
		t.Errorf("recents = %v", got)
	}
}

// A folder outside the known projects is the desk's: the listing of its history
// and branches, a worktree in it and the resuming of a conversation in it are
// refused, and nothing runs.
func TestSealedStartKeepsToKnownProjects(t *testing.T) {
	e := newSealedEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")
	outside := t.TempDir()
	for _, c := range []command{
		{Cmd: "conversations", Path: outside},
		{Cmd: "worktrees", Root: outside},
		{Cmd: "worktreeAdd", Root: outside, Text: "x"},
		{Cmd: "resumeConversation", ID: "c1", Path: outside},
	} {
		c.Req = "k-" + c.Cmd
		p.send(c)
		n := p.readType("notice")
		if txt, _ := n["text"].(string); txt != remotePathsDeskOnly {
			t.Errorf("%s outside the projects was answered %q", c.Cmd, txt)
		}
	}
	// browse answers in its own message, and says why.
	p.send(command{Cmd: "browse", Path: outside, Req: "b"})
	if got := p.readType("browse"); got["error"] != browseDeskOnly {
		t.Errorf("browse outside the projects = %v", got)
	}
}

// What starts a process or makes a folder is let through five at a time per
// device, and the answer past that is a refusal by name.
func TestSealedSpawningIsLimited(t *testing.T) {
	e := newSealedEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")
	outside := t.TempDir()
	for i := 0; i < sealedSpawnBurst; i++ {
		p.send(command{Cmd: "resumeConversation", ID: "c1", Path: outside, Req: "n"})
		p.readType("notice")
	}
	p.send(command{Cmd: "resumeConversation", ID: "c1", Path: outside, Req: "over"})
	if got := p.readType("sealedRefused"); got["code"] != sealedCodeBusy || got["req"] != "over" {
		t.Errorf("the sixth spawn was answered %v", got)
	}
	// A request that is not a spawn is not held by it.
	p.send(command{Cmd: "recents", Req: "rc"})
	p.readType("recents")
}

// The acts without the desk's switch are refused by name.
func TestSealedStartActsNeedTheSwitch(t *testing.T) {
	e := newSealedEnv(t)
	p, _ := e.open(t, "full")
	for _, n := range []string{"resumeConversation", "worktreeAdd", "structuredResume", "structuredNewConversation"} {
		p.send(command{Cmd: n, ID: "x", Path: t.TempDir(), Text: "b", Req: "q-" + n})
		got := p.readType("sealedRefused")
		if got["code"] != sealedCodeControl || got["cmd"] != n {
			t.Errorf("%s without the switch was answered %v", n, got)
		}
	}
}

// The list of past conversations a pane could move to is a read, kept for a
// device that may read, with each title made one line and redacted; no other
// success is passed with data.
func TestSealedWriteOfSessions(t *testing.T) {
	yes, no := func() bool { return true }, func() bool { return false }
	title := "fix the build\n" + sealedSecret + " and more"
	msg, _ := json.Marshal(teamResultMsg{Type: "teamResult", Req: "r", Op: "structuredSessions", OK: true,
		Sessions: []sessionView{{ID: "s1", Title: title, Updated: "2026-11-16", Current: true}}})
	for _, act := range []func() bool{yes, no} {
		got := sealedWrite(msg, act)
		if got == nil {
			t.Fatal("the sessions were dropped")
		}
		if bytes.Contains(got, []byte(sealedSecret)) || bytes.Contains(got, []byte(`\n`)) {
			t.Errorf("a session title was sent with a secret or a line break: %s", got)
		}
		var back teamResultMsg
		if err := json.Unmarshal(got, &back); err != nil || len(back.Sessions) != 1 || back.Sessions[0].ID != "s1" || !back.Sessions[0].Current {
			t.Errorf("sessions = %s (%v)", got, err)
		}
	}
	many := make([]sessionView, 80)
	for i := range many {
		many[i] = sessionView{ID: "s", Title: strings.Repeat("word ", 200)}
	}
	big, _ := json.Marshal(teamResultMsg{Type: "teamResult", Op: "structuredSessions", OK: true, Sessions: many})
	var back teamResultMsg
	if err := json.Unmarshal(sealedWrite(big, no), &back); err != nil || len(back.Sessions) != maxSessionsSealed {
		t.Fatalf("%d sessions (%v), want %d", len(back.Sessions), err, maxSessionsSealed)
	}
	if n := len([]rune(back.Sessions[0].Title)); n > maxSessionTitle {
		t.Errorf("a title of %d characters was sent", n)
	}
	// The success of the two moves goes only to a device that may act, and
	// carries nothing else.
	for _, op := range []string{"structuredResume", "structuredNewConversation"} {
		ok := []byte(`{"type":"teamResult","req":"r","op":"` + op + `","ok":true,"sessions":[{"id":"x","title":"secret"}]}`)
		if got := sealedWrite(ok, no); got != nil {
			t.Errorf("%s success went to a device that may not act: %s", op, got)
		}
		if got := sealedWrite(ok, yes); got == nil || bytes.Contains(got, []byte("secret")) {
			t.Errorf("%s success = %s", op, got)
		}
	}
	other := []byte(`{"type":"teamResult","req":"r","op":"taskList","ok":true,"sessions":[{"id":"x","title":"secret"}]}`)
	if got := sealedWrite(other, yes); got != nil {
		t.Errorf("another op's success was sent: %s", got)
	}
}

// What is written for history and worktrees is redacted.
func TestSealedWriteOfHistoryAndWorktrees(t *testing.T) {
	no := func() bool { return false }
	conv := []byte(`{"type":"conversations","cwd":"C:/p","items":[{"id":"c1","agent":"claude","summary":"use ` + sealedSecret + `","ago":"1 hour ago"}]}`)
	got := sealedWrite(conv, no)
	if got == nil || bytes.Contains(got, []byte(sealedSecret)) || !bytes.Contains(got, []byte(`"c1"`)) {
		t.Errorf("conversations = %s", got)
	}
	wts := []byte(`{"type":"worktrees","root":"C:/p","items":[{"path":"C:/p-x","branch":"x"}],"branches":[{"name":"main"}]}`)
	if got := sealedWrite(wts, no); got == nil || !bytes.Contains(got, []byte(`"main"`)) {
		t.Errorf("worktrees = %s", got)
	}
	if got := sealedWrite([]byte(`{"type":"agentStarted","paneId":"p","id":"r"}`), no); got != nil {
		t.Errorf("agentStarted = %s", got)
	}
}

// The unsealed socket is as it was: none of this is lifted for it.
func TestUnsealedKeepsRefusingHistory(t *testing.T) {
	srv, _ := newTestServer(t)
	relay := &controlClient{out: make(chan []byte, 16), ready: make(chan struct{}, 1), remote: true, device: "full"}
	for _, n := range []string{"conversations", "structuredSessions", "structuredResume", "structuredNewConversation"} {
		if relay.liftsRelayRefusal(n) {
			t.Errorf("a window reached through the relay is let %s", n)
		}
	}
	srv.handleCommand(relay, command{Cmd: "conversations", Path: t.TempDir()})
	select {
	case raw := <-relay.out:
		if !bytes.Contains(raw, []byte(conversationsOnDesktop)) {
			t.Errorf("conversations was answered %s", raw)
		}
	case <-time.After(2 * time.Second):
		t.Error("no answer")
	}
}

func TestPlainBranchName(t *testing.T) {
	ok := []string{"main", "feature/x", "agent/fix-2", "v1.2.3", "origin/main", "a_b", "a.b"}
	bad := []string{"", "-x", "--force", ".hidden", "/abs", "a..b", "a//b", "a/", "a.", "x.lock", "a/.b", "a b", "a~1", "a^", "a:b", "a?b", "a*", "a[0]", "a\\b", "a@{1}", "@",
		"na\u00efve", "a\nb", strings.Repeat("a", maxBranchName+1)}
	for _, s := range ok {
		if !plainBranchName(s) {
			t.Errorf("%q was refused", s)
		}
	}
	for _, s := range bad {
		if plainBranchName(s) {
			t.Errorf("%q was accepted", s)
		}
	}
}

// startAgent's branch and base: only with a worktree, only plain names, taken
// exactly, and refused (not renamed) when the branch exists.
func TestStartAgentBranchAndBase(t *testing.T) {
	srv, ws, repo := newRepoServer(t)
	writeAgents(t, revealAgents)
	ws.ReloadAgents()

	for name, c := range map[string]command{
		"branch without a worktree": {Root: repo, Agent: "gocli", Task: "t", Branch: "feature/x"},
		"base without a worktree":   {Root: repo, Agent: "gocli", Task: "t", Base: "main"},
		"a branch with a space":     {Root: repo, Agent: "gocli", Task: "t", Worktree: true, Branch: "a b"},
		"a base that is an option":  {Root: repo, Agent: "gocli", Task: "t", Worktree: true, Base: "--force"},
	} {
		if r := runStartAgent(t, srv, c); r.paneID != "" || r.notice == "" {
			t.Errorf("%s: %+v", name, r)
		}
	}

	r := runStartAgent(t, srv, command{Root: repo, Agent: "gocli", Task: "add a health endpoint", Worktree: true, Branch: "feature/health", Base: "main"})
	if r.notice != "" {
		t.Fatalf("startAgent said %q", r.notice)
	}
	p, ok := ask(srv, func() *workspace.Pane { return ws.Pane(r.paneID) })
	if !ok || p == nil {
		t.Fatal("no pane")
	}
	if p.Branch != "feature/health" {
		t.Errorf("branch = %q, want the one asked for", p.Branch)
	}
	// The branch is taken: the same name again is refused, not given a number.
	if r := runStartAgent(t, srv, command{Root: repo, Agent: "gocli", Task: "again", Worktree: true, Branch: "feature/health"}); r.paneID != "" || !strings.Contains(r.notice, "already exists") {
		t.Errorf("a second start on the same branch: %+v", r)
	}
	// A base that is not there is git's refusal.
	if r := runStartAgent(t, srv, command{Root: repo, Agent: "gocli", Task: "ghost", Worktree: true, Branch: "feature/ghost", Base: "no-such-base"}); r.paneID != "" || r.notice == "" {
		t.Errorf("a base that does not exist: %+v", r)
	}
}

// A viewer the desk let watch panes reads conversations, not repository files:
// the sealed table must refuse what the unsealed one marks accessFull.
func TestSealedWatcherCannotReadRepositoryFiles(t *testing.T) {
	e := newSealedEnv(t)
	p, _ := e.open(t, "watcher")
	for _, n := range []string{"diff", "changes", "changesSnapshot", "filesPage", "diffHunks", "diffContext", "ghChecks"} {
		if commandAccess[n] != accessFull {
			t.Errorf("%s: unsealed access is %v, the test assumes accessFull", n, commandAccess[n])
		}
		if !sealedFullOnly[n] {
			t.Errorf("%s is not full only", n)
		}
		p.send(command{Cmd: n, ID: "x", Req: "r-" + n})
		got := p.readType("sealedRefused")
		if got["code"] != sealedCodeRole || got["cmd"] != n {
			t.Errorf("a watcher's %s was answered %v", n, got)
		}
	}
}
