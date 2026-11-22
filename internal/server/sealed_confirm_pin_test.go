package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What a tier C request runs is what the desk was shown. The host reads the
// repository itself; the device's Files, Stamps and Omitted are not used.

func askCommit(t *testing.T, p *sealedPhone, desk *controlClient, c command) deskConfirmMsg {
	t.Helper()
	p.send(c)
	p.gitState(confirmWaiting)
	ask := nextDeskConfirm(t, desk)
	for ask.State != "ask" {
		ask = nextDeskConfirm(t, desk)
	}
	return ask
}

func TestSealedCommitShowsTheFilesTheHostFound(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "hidden-secrets.env"), []byte("TOKEN=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The device lists one file and says one was left out: the shape that used to
	// let an unlisted file ride along unseen.
	ask := askCommit(t, p, desk, command{Cmd: "commit", Path: repo, Text: "docs", Files: []string{"README.md"}, Omitted: 1, Req: "c"})
	for _, want := range []string{"README.md", "hidden-secrets.env", "2 changed files", "Message: docs"} {
		if !strings.Contains(ask.Summary, want) {
			t.Errorf("the desk is not shown %q: %q", want, ask.Summary)
		}
	}
	if !strings.Contains(ask.Summary, repo) {
		t.Errorf("the desk is not shown the full folder %q: %q", repo, ask.Summary)
	}
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	p.gitState(confirmAllowed)
	sealedWaitFor(t, "the commit", func() bool { return commitCount(t, repo) == "2" })
	if got := sealedGitRun(t, repo, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(got, "hidden-secrets.env") || !strings.Contains(got, "README.md") {
		t.Errorf("the commit holds %q, want exactly what the desk was shown", got)
	}
}

func TestSealedCommitIsRefusedWhenTheTreeMovedAfterTheAsk(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, repo string){
		"a file arrived": func(t *testing.T, repo string) {
			if err := os.WriteFile(filepath.Join(repo, "late.txt"), []byte("z\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"a listed file was written": func(t *testing.T, repo string) {
			if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("a much longer line than before\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, p, desk, repo := gitEnv(t)
			dirty(t, repo)
			ask := askCommit(t, p, desk, command{Cmd: "commit", Path: repo, Text: "m", Req: "c"})
			change(t, repo)
			e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
			p.gitState(confirmMoved)
			time.Sleep(100 * time.Millisecond)
			if commitCount(t, repo) != "1" {
				t.Error("a commit of a tree that moved ran")
			}
		})
	}
}

// A checkout inside the window must not push or pull another branch than the one
// shown.
func TestSealedGitIsRefusedWhenTheBranchChangedAfterTheAsk(t *testing.T) {
	for _, cmd := range []string{"gitPush", "gitPull", "commit"} {
		t.Run(cmd, func(t *testing.T) {
			e, p, desk, repo := gitEnv(t)
			dirty(t, repo)
			ask := askCommit(t, p, desk, command{Cmd: cmd, Path: repo, Text: "m", Req: "c"})
			if !strings.Contains(ask.Summary, "on branch main") {
				t.Errorf("the branch is not shown: %q", ask.Summary)
			}
			sealedGitRun(t, repo, "checkout", "-q", "-b", "other")
			e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
			p.gitState(confirmMoved)
			time.Sleep(100 * time.Millisecond)
			if commitCount(t, repo) != "1" {
				t.Error("something ran on the other branch")
			}
		})
	}
}

func TestSealedPushShowsTheRemoteAndRefusesWhenItChanged(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	sealedGitRun(t, repo, "remote", "set-url", "origin", "https://example.com/team/one.git")
	ask := askCommit(t, p, desk, command{Cmd: "gitPush", Path: repo, Req: "c"})
	if !strings.Contains(ask.Summary, "example.com/team/one.git") || !strings.Contains(ask.Summary, repo) {
		t.Errorf("the remote or folder is not shown: %q", ask.Summary)
	}
	sealedGitRun(t, repo, "remote", "set-url", "origin", "https://example.com/other/two.git")
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	p.gitState(confirmMoved)
}

// The whole message and comment are shown, and what the desk's redaction would
// change is refused, not posted as it was sent.
func TestSealedConfirmShowsTheWholeTextOrRefuses(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	dirty(t, repo)
	long := strings.Repeat("word ", 150) + "THE-END"
	ask := askCommit(t, p, desk, command{Cmd: "commit", Path: repo, Text: "first line\n\n" + long, Req: "c"})
	if !strings.Contains(ask.Summary, "THE-END") || !strings.Contains(ask.Summary, "first line") {
		t.Errorf("the message is not shown whole: %q", ask.Summary)
	}
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: false})
	p.gitState(confirmDenied)
	e.srv.sealed.device("full").noteAllowed()

	body := strings.Repeat("c", confirmMaxMessage-10) + "COMMENT-END"
	if len(body) <= confirmMaxMessage {
		body = strings.Repeat("c", confirmMaxMessage-11) + "COMMENT-END"
	}
	p.send(command{Cmd: "ghPRComment", Path: repo, GHNumber: 3, GHBody: body, Req: "g"})
	p.gitState(confirmBad) // over the cap: refused, not cut
	ok := strings.Repeat("c", confirmMaxMessage-20) + "COMMENT-END"
	ask = askCommit(t, p, desk, command{Cmd: "ghPRComment", Path: repo, GHNumber: 3, GHBody: ok, Req: "g2"})
	if !strings.Contains(ask.Summary, "COMMENT-END") || !strings.Contains(ask.Summary, "#3") {
		t.Errorf("the comment is not shown whole: %q", ask.Summary)
	}
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: false})
	p.gitState(confirmDenied)
	e.srv.sealed.device("full").noteAllowed()

	for _, c := range []command{
		{Cmd: "commit", Path: repo, Text: "token " + sealedSecret, Req: "r1"},
		{Cmd: "ghPRComment", Path: repo, GHNumber: 3, GHBody: "see " + sealedSecret, Req: "r2"},
		{Cmd: "ghIssueComment", Path: repo, GHNumber: 3, GHBody: "bidi ‮ text", Req: "r3"},
	} {
		p.send(c)
		p.gitState(confirmBad)
	}
	noNewAsk(t, desk)
}

// A refused question lengthens the wait before the device may ask again.
func TestSealedDenyWaitsBeforeTheNextAsk(t *testing.T) {
	e, p, desk, repo := gitEnv(t)
	dirty(t, repo)
	ask := askCommit(t, p, desk, command{Cmd: "gitPull", Path: repo, Req: "a"})
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: false})
	p.gitState(confirmDenied)
	p.send(command{Cmd: "gitPull", Path: repo, Req: "b"})
	p.gitState(confirmWait)
	noNewAsk(t, desk)
}

// Unpairing itself removes the device's Control agents record.
func TestSealedSelfRevokeClearsControl(t *testing.T) {
	e, _ := newSettingsEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")
	p.send(command{Cmd: "remoteRevoke", ID: "full", Req: "me"})
	p.readType("deviceRevoked")
	sealedWaitFor(t, "control cleared", func() bool {
		a := e.srv.deviceAccess("full")
		return !a.Control && a.ControlKey == ""
	})
}

// noNewAsk fails if the desk was sent a new question; the news that an earlier one
// was settled is not one.
func noNewAsk(t *testing.T, w *controlClient) {
	t.Helper()
	for {
		select {
		case data := <-w.out:
			if messageType(data) == "deskConfirm" && strings.Contains(string(data), `"state":"ask"`) {
				t.Fatalf("the desk was asked: %s", data)
			}
		default:
			return
		}
	}
}
