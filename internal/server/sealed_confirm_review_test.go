package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Findings from the second review of the sealed commit, push and pull flow.

// deskNotice waits for a notice to the window whose text holds want.
func deskNotice(t *testing.T, w *controlClient, want string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case data := <-w.out:
			if messageType(data) != "notice" {
				continue
			}
			var n struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(data, &n)
			if strings.Contains(n.Text, want) {
				return
			}
		case <-deadline:
			t.Fatalf("the desk was not told %q", want)
		}
	}
}

func repoWithRemote(t *testing.T) (*sealedEnv, *sealedPhone, *controlClient, string) {
	t.Helper()
	e, p, desk, repo := gitEnv(t)
	sealedGitRun(t, repo, "remote", "add", "origin", "https://example.com/team/one.git")
	return e, p, desk, repo
}

// A yes that is refused tells the desk that nothing ran.
func TestSealedARefusedYesTellsTheDesk(t *testing.T) {
	t.Run("moved", func(t *testing.T) {
		e, p, desk, repo := repoWithRemote(t)
		ask := askCommit(t, p, desk, command{Cmd: "gitPush", Path: repo, Req: "c"})
		sealedGitRun(t, repo, "remote", "set-url", "origin", "https://example.com/other/two.git")
		e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
		p.gitState(confirmMoved)
		deskNotice(t, desk, "Nothing ran")
	})
	t.Run("off", func(t *testing.T) {
		e, p, desk, repo := repoWithRemote(t)
		ask := askCommit(t, p, desk, command{Cmd: "gitPush", Path: repo, Req: "c"})
		// The right ends while the socket is still open: the yes finds it gone.
		e.srv.deskConfirms.mu.Lock()
		link := e.srv.deskConfirms.by[ask.ID].link
		e.srv.deskConfirms.mu.Unlock()
		link.cut()
		e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
		deskNotice(t, desk, "Nothing ran")
	})
}

// What a push sends is the commits shown, at the commit shown, by the settings shown.
func TestSealedPushIsHeldToTheHeadTheAheadCountAndTheSettings(t *testing.T) {
	t.Run("shows the head and the count", func(t *testing.T) {
		e, p, desk, repo := repoWithRemote(t)
		head := strings.TrimSpace(sealedGitRun(t, repo, "rev-parse", "HEAD"))
		ask := askCommit(t, p, desk, command{Cmd: "gitPush", Path: repo, Req: "c"})
		for _, want := range []string{head[:12], "Sends 1 commit"} {
			if !strings.Contains(ask.Summary, want) {
				t.Errorf("the desk is not shown %q: %q", want, ask.Summary)
			}
		}
		e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: false})
		p.gitState(confirmDenied)
	})
	t.Run("a commit made after the ask is not sent", func(t *testing.T) {
		e, p, desk, repo := repoWithRemote(t)
		ask := askCommit(t, p, desk, command{Cmd: "gitPush", Path: repo, Req: "c"})
		dirty(t, repo)
		sealedGitRun(t, repo, "add", "new.txt")
		sealedGitRun(t, repo, "commit", "-q", "-m", "agent commit after the ask")
		e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
		p.gitState(confirmMoved)
	})
	t.Run("a commit with a push shows the commit it makes in the count", func(t *testing.T) {
		_, p, desk, repo := repoWithRemote(t)
		dirty(t, repo)
		ask := askCommit(t, p, desk, command{Cmd: "commit", Push: true, Path: repo, Text: "m", Req: "c"})
		if !strings.Contains(ask.Summary, "Sends 2 commits, this commit included") {
			t.Errorf("the count does not include the new commit: %q", ask.Summary)
		}
	})
	t.Run("settings that send more are refused at the ask", func(t *testing.T) {
		for name, kv := range map[string][2]string{
			"push.default":       {"push.default", "matching"},
			"remote.origin.push": {"remote.origin.push", "refs/heads/*:refs/heads/*"},
			"pushRemote":         {"branch.main.pushRemote", "elsewhere"},
		} {
			t.Run(name, func(t *testing.T) {
				_, p, desk, repo := repoWithRemote(t)
				sealedGitRun(t, repo, "config", kv[0], kv[1])
				p.send(command{Cmd: "gitPush", Path: repo, Req: "c"})
				got := p.gitState(confirmBad)
				if got["reason"] != reasonPushShape {
					t.Errorf("the reason is %v, want %q", got["reason"], reasonPushShape)
				}
				noDeskConfirm(t, desk)
			})
		}
	})
	t.Run("settings changed after the ask make the yes moved", func(t *testing.T) {
		e, p, desk, repo := repoWithRemote(t)
		ask := askCommit(t, p, desk, command{Cmd: "gitPush", Path: repo, Req: "c"})
		sealedGitRun(t, repo, "config", "remote.origin.push", "refs/heads/*:refs/heads/*")
		e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
		p.gitState(confirmMoved)
	})
}

// A sensitive file in the tail of a long list is not hidden behind "and N more".
func TestSealedCommitCountsSecretLookingFilesThatAreNotNamed(t *testing.T) {
	_, p, desk, repo := gitEnv(t)
	for i := 0; i < confirmShownFiles+5; i++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("f%03d.txt", i)), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"zz-deploy.pem", "zz.env", "id_rsa"} {
		if err := os.WriteFile(filepath.Join(repo, n), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ask := askCommit(t, p, desk, command{Cmd: "commit", Path: repo, Text: "m", Req: "c"})
	if !strings.Contains(ask.Summary, "3 have names that look like secrets") {
		t.Errorf("the tail holds three secret-looking names and the desk is not told: %q", ask.Summary)
	}
	if !strings.Contains(ask.Summary, "and 8 more") {
		t.Errorf("the count of the rest is gone: %q", ask.Summary)
	}
}

func TestSecretLookingNames(t *testing.T) {
	for _, n := range []string{".env", "app/.env.local", "prod.env", "a/b/server.pem", "tls.key", "id_rsa", "dir/id_ed25519.pub", "x.p12", ".netrc"} {
		if !secretLookingName(n) {
			t.Errorf("%q is not taken for a secret", n)
		}
	}
	for _, n := range []string{"README.md", "environment.go", "keyboard.txt", "monkey.js", "src/envelope.go"} {
		if secretLookingName(n) {
			t.Errorf("%q is taken for a secret", n)
		}
	}
	long := strings.Repeat("d", 130) + "/.env"
	if unseenSecrets([]string{long}) != 1 {
		t.Error("a secret whose name is cut at the cap is not counted")
	}
	if unseenSecrets([]string{".env"}) != 0 {
		t.Error("a secret named in full was counted as unseen")
	}
}

// A "bad" refusal says why, in fixed words.
func TestSealedBadRefusalsSayWhy(t *testing.T) {
	_, p, _, repo := gitEnv(t)
	dirty(t, repo)
	for _, c := range []struct {
		cmd  command
		want string
	}{
		{command{Cmd: "commit", Path: repo, Text: "token " + sealedSecret, Req: "1"}, reasonRedaction},
		{command{Cmd: "commit", Path: repo, Text: "bidi ‮ text", Req: "2"}, reasonControlChar},
		{command{Cmd: "commit", Path: repo, Text: strings.Repeat("a", confirmMaxMessage+1), Req: "3"}, reasonTooLong},
		{command{Cmd: "commit", Path: repo, Text: "  ", Req: "4"}, reasonEmpty},
		{command{Cmd: "commit", Text: "m", Req: "5"}, reasonNotRequest},
	} {
		p.send(c.cmd)
		got := p.gitState(confirmBad)
		if got["reason"] != c.want {
			t.Errorf("%q: reason %v, want %q", c.cmd.Req, got["reason"], c.want)
		}
		if strings.Contains(fmt.Sprint(got), sealedSecret) {
			t.Errorf("the refusal quotes the request: %v", got)
		}
	}
}

// Only a no counts toward the doubling; a lapse costs the base wait and no more.
func TestSealedLapsesDoNotDoubleTheWait(t *testing.T) {
	d := &sealedDevice{}
	now := time.Now()
	for i := 0; i < 12; i++ {
		d.noteLapsed(now)
	}
	if w := d.waitAt(now); w != sealedDenyBase {
		t.Fatalf("after many lapses the wait is %v, want the base %v", w, sealedDenyBase)
	}
	d.noteDenied(now)
	if w := d.waitAt(now); w != sealedDenyBase {
		t.Fatalf("the first no after lapses waits %v, want the base %v", w, sealedDenyBase)
	}
	d.noteDenied(now)
	if w := d.waitAt(now); w != 2*sealedDenyBase {
		t.Fatalf("the second no waits %v, want double", w)
	}
}

// The phone is told it must wait, and for how long, rather than "busy".
func TestSealedWaitIsAStateWithSeconds(t *testing.T) {
	for _, outcome := range []string{confirmDenied, confirmExpired} {
		t.Run(outcome, func(t *testing.T) {
			e, p, desk, repo := gitEnv(t)
			dirty(t, repo)
			ask := askCommit(t, p, desk, command{Cmd: "gitPull", Path: repo, Req: "a"})
			if outcome == confirmDenied {
				e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: false})
			} else {
				e.srv.settleConfirm(ask.ID, confirmExpired)
			}
			p.gitState(outcome)
			p.send(command{Cmd: "gitPull", Path: repo, Req: "b"})
			got := p.gitState(confirmWait)
			secs, _ := got["waitSecs"].(float64)
			if secs < 1 || secs > float64(sealedDenyBase/time.Second) {
				t.Errorf("waitSecs = %v, want 1..%d", got["waitSecs"], int(sealedDenyBase/time.Second))
			}
		})
	}
}
