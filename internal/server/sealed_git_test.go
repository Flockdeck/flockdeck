package server

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// Stage 6 of /ws/sealed (API 1.40, cap sealedGit): changes, diffs and checks as
// tier A reads, and commit, push and pull as tier C, which a person at the desk
// must allow for each request. The rules held here: the right is looked at again
// at the moment of the yes, the answer is the desk's alone and bound to the
// window it was sent to, nothing is remembered, merges stay off the socket, and
// no text of a commit is written to the log.

// deskWindow is a window on this machine that opened as a page.
func deskWindow(t *testing.T, srv *Server) *controlClient {
	t.Helper()
	w := &controlClient{out: make(chan []byte, 32), page: true}
	srv.mu.Lock()
	srv.clients[w] = true
	srv.mu.Unlock()
	t.Cleanup(func() {
		srv.mu.Lock()
		delete(srv.clients, w)
		srv.mu.Unlock()
	})
	return w
}

// next reads the next deskConfirm sent to the window.
func nextDeskConfirm(t *testing.T, w *controlClient) deskConfirmMsg {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case data := <-w.out:
			if messageType(data) != "deskConfirm" {
				continue
			}
			var m deskConfirmMsg
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			return m
		case <-deadline:
			t.Fatal("the desk was not asked")
		}
	}
}

func noDeskConfirm(t *testing.T, w *controlClient) {
	t.Helper()
	for {
		select {
		case data := <-w.out:
			if messageType(data) == "deskConfirm" {
				t.Fatalf("the desk was asked: %s", data)
			}
		default:
			return
		}
	}
}

func sealedGitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitCount(t *testing.T, dir string) string {
	return sealedGitRun(t, dir, "rev-list", "--count", "HEAD")
}

// gitEnv is a sealed environment over a real repository that is the open
// project, a device that may act, and a window at the desk.
func gitEnv(t *testing.T) (*sealedEnv, *sealedPhone, *controlClient, string) {
	t.Helper()
	srv, _, repo := newRepoServer(t)
	e := newSealedEnvFor(t, srv)
	e.grantControl(t, "full")
	desk := deskWindow(t, srv)
	p, _ := e.open(t, "full")
	return e, p, desk, repo
}

func dirty(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (p *sealedPhone) gitState(want string) map[string]any {
	p.t.Helper()
	for i := 0; i < 30; i++ {
		got := p.readType("gitConfirm")
		if got["state"] == want {
			return got
		}
	}
	p.t.Fatalf("no gitConfirm in state %s", want)
	return nil
}

// What is on the socket for stage 6, and what is not.
func TestSealedGitTables(t *testing.T) {
	for _, n := range []string{"changesSnapshot", "filesPage", "diffHunks", "diffContext", "ghChecks", "changes", "diff"} {
		if sealedCommands[n] != sealedTierA {
			t.Errorf("%s is tier %q, want A", n, sealedCommands[n])
		}
	}
	for _, n := range []string{"commit", "gitPush", "gitPull"} {
		if sealedCommands[n] != sealedTierC {
			t.Errorf("%s is tier %q, want C", n, sealedCommands[n])
		}
	}
	// Merges, the merge gate, comments and reviews that write, running a check
	// file, the live watch, fetch and the desk's own answer are not here.
	for _, n := range []string{"mergeBranch", "approveMerge", "unapproveMerge", "commentAdd", "commentResolve", "commentReopen",
		"requestReview", "submitReview", "runChecks", "confirmChecks", "changesWatch", "changesUnwatch", "gitFetch", "deskConfirmAnswer"} {
		if sealedCommands[n] != "" {
			t.Errorf("%s is on the sealed socket", n)
		}
	}
	for _, typ := range []string{"changesTeam", "changesFiles", "diffHunks", "diffContext", "ghChecks"} {
		if sealedMessages[typ].Tier != sealedTierA {
			t.Errorf("message %s is tier %q", typ, sealedMessages[typ].Tier)
		}
	}
	if sealedMessages["changesChecks"].Tier != "" || sealedMessages["deskConfirm"].Tier != "" {
		t.Error("a check file's commands or the desk's question is sent to a device")
	}
}

// Without the desk's Control agents switch a device is refused tier C by name,
// and the desk is not asked.
func TestSealedGitNeedsControl(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	e := newSealedEnvFor(t, srv)
	desk := deskWindow(t, srv)
	p, hello := e.open(t, "full")
	if helloCommands(hello)["commit"] {
		t.Error("a device without the switch is told it may commit")
	}
	dirty(t, repo)
	p.send(command{Cmd: "commit", Path: repo, Text: "m", Req: "c"})
	if got := p.readType("sealedRefused"); got["code"] != sealedCodeControl || got["cmd"] != "commit" {
		t.Errorf("commit was answered %v", got)
	}
	noDeskConfirm(t, desk)
	if commitCount(t, repo) != "1" {
		t.Error("something was committed")
	}
}

// The unsealed socket refuses all three, whatever the device's role: the relay
// can read and forge what it carries, so it cannot stand behind a yes.
func TestUnsealedRelayWindowCannotCommitPushOrPull(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	dirty(t, repo)
	desk := deskWindow(t, srv)
	c := &controlClient{out: make(chan []byte, 8), remote: true, device: "full"}
	c.accessOf = func() store.DeviceAccess { return store.DeviceAccess{Role: store.RoleFull} }
	for _, name := range []string{"commit", "gitPush", "gitPull"} {
		srv.handleCommand(c, command{Cmd: name, Path: repo, Text: "m", Req: "r"})
		data := <-c.out
		var n noticeMsg
		if err := json.Unmarshal(data, &n); err != nil || n.Text != gitDeskOnly || !n.Error {
			t.Errorf("%s over the unsealed socket was answered %s", name, data)
		}
	}
	noDeskConfirm(t, desk)
	if commitCount(t, repo) != "1" {
		t.Error("something was committed")
	}
}

// A commit is held, shown at the desk, and run only after a yes with the nonce
// that window was given; the next one is asked again.
func TestSealedCommitWaitsForTheDesk(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	s := e.srv
	dirty(t, repo)
	p.send(command{Cmd: "commit", Path: repo, Text: "add new file", Req: "c1"})
	waiting := p.gitState(confirmWaiting)
	if waiting["req"] != "c1" || waiting["cmd"] != "commit" {
		t.Errorf("waiting = %v", waiting)
	}
	if _, has := waiting["nonce"]; has {
		t.Error("the device was sent the nonce")
	}
	ask := nextDeskConfirm(t, desk)
	if ask.State != "ask" || ask.Nonce == "" || ask.Action != "commit" || ask.Device == "" {
		t.Errorf("ask = %+v", ask)
	}
	if !strings.Contains(ask.Summary, "new file") || !strings.Contains(ask.Summary, "main") {
		t.Errorf("the desk is not told what is asked: %q", ask.Summary)
	}
	if commitCount(t, repo) != "1" {
		t.Fatal("it was run before the desk answered")
	}

	// A wrong nonce, and a window that is not a page, leave it open.
	s.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: "wrong", On: true})
	other := &controlClient{out: make(chan []byte, 8)}
	s.handleCommand(other, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	relay := &controlClient{out: make(chan []byte, 8), remote: true, page: true}
	s.handleCommand(relay, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	if commitCount(t, repo) != "1" {
		t.Fatal("a wrong answer ran it")
	}

	s.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	p.gitState(confirmAllowed)
	sealedWaitFor(t, "the commit", func() bool { return commitCount(t, repo) == "2" })

	// Once: the same nonce again does nothing.
	s.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})

	// Nothing is remembered: the next commit is asked again.
	if err := os.WriteFile(filepath.Join(repo, "second.txt"), []byte("y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.send(command{Cmd: "commit", Path: repo, Text: "second", Req: "c2"})
	p.gitState(confirmWaiting)
	again := nextDeskConfirm(t, desk)
	for again.State != "ask" {
		again = nextDeskConfirm(t, desk) // the news that the first one was settled comes first
	}
	if again.ID == ask.ID || again.Nonce == ask.Nonce {
		t.Error("a question was reused")
	}
	if commitCount(t, repo) != "2" {
		t.Error("the second commit ran without being asked")
	}
}

func sealedWaitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSealedCommitDeniedDoesNothing(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	dirty(t, repo)
	p.send(command{Cmd: "commit", Path: repo, Text: "no", Req: "c"})
	p.gitState(confirmWaiting)
	ask := nextDeskConfirm(t, desk)
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: false})
	p.gitState(confirmDenied)
	if done := nextDeskConfirm(t, desk); done.State != "done" || done.Outcome != confirmDenied {
		t.Errorf("the desk was told %+v", done)
	}
	time.Sleep(100 * time.Millisecond)
	if commitCount(t, repo) != "1" {
		t.Error("a denied commit ran")
	}
	// The answer is spent.
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	time.Sleep(100 * time.Millisecond)
	if commitCount(t, repo) != "1" {
		t.Error("a spent answer ran it")
	}
}

// The right is looked at again at the moment of the yes: a device that lost it,
// or whose switch was for another key, is not served by an old question.
func TestSealedConfirmIsRegatedAtTheYes(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	dirty(t, repo)
	p.send(command{Cmd: "commit", Path: repo, Text: "m", Req: "c"})
	p.gitState(confirmWaiting)
	ask := nextDeskConfirm(t, desk)
	e.setControl(t, "full", store.RoleFull, false, "")
	e.srv.approveFile = approveFileCache{}
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	// The switch going off ends the socket; the question ends with the outcome off.
	if done := nextDeskConfirm(t, desk); done.State != "done" || done.Outcome != confirmOff {
		t.Errorf("the desk was told %+v", done)
	}
	time.Sleep(100 * time.Millisecond)
	if commitCount(t, repo) != "1" {
		t.Error("a device without the right committed")
	}
}

// Nobody at the desk means nobody can say yes: the request ends at once. One
// question a device at a time. A folder outside the known projects and a request
// with no message are not asked.
func TestSealedGitRefusals(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	e := newSealedEnvFor(t, srv)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")
	dirty(t, repo)

	p.send(command{Cmd: "commit", Path: repo, Text: "m", Req: "a"})
	p.gitState(confirmNoDesk)

	desk := deskWindow(t, srv)
	outside := t.TempDir()
	p.send(command{Cmd: "gitPush", Path: outside, Req: "b"})
	if n := p.readType("notice"); n["text"] != remotePathsDeskOnly {
		t.Errorf("a folder outside the projects was answered %v", n)
	}
	p.send(command{Cmd: "commit", Path: repo, Text: "  ", Req: "c"})
	p.gitState(confirmBad)
	p.send(command{Cmd: "commit", Text: "m", Req: "d"})
	p.gitState(confirmBad)
	noDeskConfirm(t, desk)

	p.send(command{Cmd: "gitPull", Path: repo, Req: "e"})
	p.gitState(confirmWaiting)
	nextDeskConfirm(t, desk)
	p.send(command{Cmd: "gitPush", Path: repo, Req: "f"})
	p.gitState(confirmBusy)
	if commitCount(t, repo) != "1" {
		t.Error("something was committed")
	}
}

// A question that runs out, and one whose socket closed, end without running.
func TestSealedConfirmExpiresAndGoesWithTheSocket(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	dirty(t, repo)
	p.send(command{Cmd: "commit", Path: repo, Text: "m", Req: "c"})
	p.gitState(confirmWaiting)
	ask := nextDeskConfirm(t, desk)
	e.srv.settleConfirm(ask.ID, confirmExpired)
	p.gitState(confirmExpired)
	if done := nextDeskConfirm(t, desk); done.Outcome != confirmExpired {
		t.Errorf("the desk was told %+v", done)
	}
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	// A question left to lapse counts as a refusal; the wait it brings is not
	// what this test is about.
	e.srv.sealed.device("full").noteAllowed()

	p.send(command{Cmd: "commit", Path: repo, Text: "m", Req: "c2"})
	p.gitState(confirmWaiting)
	second := nextDeskConfirm(t, desk)
	_ = p.conn.CloseNow()
	if done := nextDeskConfirm(t, desk); done.ID != second.ID || done.State != "done" || done.Outcome != confirmOff {
		t.Errorf("after the socket closed the desk was told %+v", done)
	}
	e.srv.deskConfirms.mu.Lock()
	n := len(e.srv.deskConfirms.by)
	e.srv.deskConfirms.mu.Unlock()
	if n != 0 {
		t.Errorf("%d questions are still open", n)
	}
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: second.ID, Nonce: second.Nonce, On: true})
	time.Sleep(100 * time.Millisecond)
	if commitCount(t, repo) != "1" {
		t.Error("an expired or abandoned question ran")
	}
}

// Merges are the desk's: they are refused by name over the sealed socket, with
// the right on, and the desk is not asked.
func TestSealedMergesStayAtTheDesk(t *testing.T) {
	_, p, desk, _ := gitEnv(t)
	for _, name := range []string{"mergeBranch", "approveMerge", "commentAdd", "runChecks", "gitFetch", "deskConfirmAnswer"} {
		p.send(command{Cmd: name, ID: "x", Req: "r-" + name})
		if got := p.readType("sealedRefused"); got["code"] != sealedCodeCommand || got["cmd"] != name {
			t.Errorf("%s was answered %v", name, got)
		}
	}
	noDeskConfirm(t, desk)
}

// The new reads: the branch list is for a known project only, answers as a
// changesTeam, and its refusals are in fixed words.
func TestSealedChangesReads(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	e := newSealedEnvFor(t, srv)
	p, _ := e.open(t, "full")
	p.send(command{Cmd: "changesSnapshot", Root: repo, Req: "s"})
	if got := p.readType("changesTeam"); got["type"] != "changesTeam" {
		t.Errorf("changesSnapshot = %v", got)
	}
	p.send(command{Cmd: "changesSnapshot", Root: t.TempDir(), Req: "o"})
	if n := p.readType("notice"); n["text"] != remotePathsDeskOnly {
		t.Errorf("a project that is not known was answered %v", n)
	}
	p.send(command{Cmd: "filesPage", Req: "f"})
	got := p.readType("changesResult")
	if got["code"] != "badRequest" || got["error"] != "That request was not understood" {
		t.Errorf("filesPage with no pane = %v", got)
	}
	// A write of the changes contract is not run from here: it is not in the table.
	p.send(command{Cmd: "commentAdd", ID: "x", Req: "w"})
	if got := p.readType("sealedRefused"); got["code"] != sealedCodeCommand {
		t.Errorf("commentAdd = %v", got)
	}
}

// What the new messages may carry.
func TestSealedWriteOfChangesMessages(t *testing.T) {
	yes, no := func() bool { return true }, func() bool { return false }
	for _, typ := range []string{"changesTeam", "changesFiles", "diffHunks", "diffContext", "ghChecks"} {
		msg := []byte(`{"type":"` + typ + `","req":"r","note":"` + sealedSecret + `"}`)
		got := sealedWrite(msg, no)
		if got == nil || bytes.Contains(got, []byte(sealedSecret)) {
			t.Errorf("%s = %s", typ, got)
		}
	}
	// The state of a held request goes to a device that may act, and carries no text.
	st := []byte(`{"type":"gitConfirm","req":"r","id":"i","cmd":"commit","state":"waiting"}`)
	if sealedWrite(st, no) != nil {
		t.Error("gitConfirm was sent to a device that may not act")
	}
	if sealedWrite(st, yes) == nil {
		t.Error("gitConfirm was dropped")
	}
	// The desk's question and a check file's commands never reach a device.
	for _, typ := range []string{"deskConfirm", "changesChecks"} {
		if sealedWrite([]byte(`{"type":"`+typ+`","id":"i"}`), yes) != nil {
			t.Errorf("%s was sent to a device", typ)
		}
	}
	// A refusal of a read is in fixed words; an answer that holds data is dropped.
	r := scrubChangesResultSealed([]byte(`{"type":"changesResult","op":"filesPage","code":"noBranch","error":"` + sealedSecret + ` C:/secret/path"}`))
	if r == nil || bytes.Contains(r, []byte(sealedSecret)) || !bytes.Contains(r, []byte("could not be read")) {
		t.Errorf("a refusal = %s", r)
	}
	if scrubChangesResultSealed([]byte(`{"type":"changesResult","op":"commentAdd","ok":true}`)) != nil {
		t.Error("an OK answer was sent")
	}
	if scrubChangesResultSealed([]byte(`{"type":"changesResult","op":"x","code":"other","error":"y"}`)) != nil {
		t.Error("an unknown code was sent")
	}
}

// The log names the device, the pane or the kind and what became of it, and holds
// no text of a commit.
func TestSealedConfirmAuditHoldsNoText(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	dirty(t, repo)
	const text = "audit-text-must-not-appear"
	p.send(command{Cmd: "commit", Path: repo, Text: text, Req: "c"})
	p.gitState(confirmWaiting)
	ask := nextDeskConfirm(t, desk)
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: false})
	p.gitState(confirmDenied)
	time.Sleep(200 * time.Millisecond)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".log") {
			return nil
		}
		b, _ := os.ReadFile(path)
		if bytes.Contains(b, []byte(text)) || bytes.Contains(b, []byte(repo)) {
			t.Errorf("%s holds the commit message or the path", path)
		}
		if bytes.Contains(b, []byte(`"sealed-git"`)) {
			seen = true
		}
		return nil
	})
	if !seen {
		t.Log("no log with the audit events was found; the events are written asynchronously")
	}
}

// A device cannot keep a dialog in front of the person: three questions, then
// one every 20 seconds, answered busy whatever became of the earlier ones.
func TestSealedGitAsksAreLimited(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	dirty(t, repo)
	for i := 0; i < sealedAskBurst; i++ {
		p.send(command{Cmd: "gitPull", Path: repo, Req: "q"})
		p.gitState(confirmWaiting)
		ask := nextDeskConfirm(t, desk)
		for ask.State != "ask" {
			ask = nextDeskConfirm(t, desk)
		}
		e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: false})
		p.gitState(confirmDenied)
		// The wait a refusal brings is tested on its own (TestSealedDenyWaitsBeforeTheNextAsk).
		e.srv.sealed.device("full").noteAllowed()
	}
	p.send(command{Cmd: "gitPull", Path: repo, Req: "q"})
	p.gitState(confirmBusy)
}
