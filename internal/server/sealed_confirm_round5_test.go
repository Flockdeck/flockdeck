package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Round 5: what a phone push or pull refuses at the ask, with the reason the
// phone is sent. Each of these ran (or was asked) before.
func TestSealedPinRefusals(t *testing.T) {
	setup := func(repo string) {
		sealedGitRun(t, repo, "config", "branch.main.remote", "origin")
		sealedGitRun(t, repo, "config", "branch.main.merge", "refs/heads/main")
	}
	for _, c := range []struct {
		name string
		cmd  string
		set  func(repo string)
		want string
	}{
		{"chained pushInsteadOf", "gitPush", func(repo string) {
			sealedGitRun(t, repo, "config", "url.https://b.example/.pushInsteadOf", "https://example.com/")
			sealedGitRun(t, repo, "config", "url.https://c.example/.pushInsteadOf", "https://b.example/")
		}, reasonPinProgram},
		{"sslVerify false", "gitPush", func(repo string) { sealedGitRun(t, repo, "config", "http.sslVerify", "false") }, reasonPinProgram},
		{"sslVerify false on a pull", "gitPull", func(repo string) { sealedGitRun(t, repo, "config", "http.sslVerify", "false") }, reasonPinProgram},
		{"submodule update command", "gitPull", func(repo string) { sealedGitRun(t, repo, "config", "submodule.x.update", "!touch pwned") }, reasonPinProgram},
		{"pre-push hook", "gitPush", func(repo string) {
			if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", "pre-push"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, reasonPushHook},
		{"pull with remote '.'", "gitPull", func(repo string) { sealedGitRun(t, repo, "config", "branch.main.remote", ".") }, reasonPullShape},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, p, desk, repo := repoWithRemote(t)
			setup(repo)
			c.set(repo)
			p.send(command{Cmd: c.cmd, Path: repo, Req: "r"})
			got := p.gitState(confirmBad)
			if r, _ := got["reason"].(string); !strings.HasPrefix(r, c.want) {
				t.Errorf("the reason is %q, want it to start %q", r, c.want)
			}
			noDeskConfirm(t, desk)
		})
	}
}

// The card says hooks are not run, for the push and pull that are asked.
func TestSealedCardSaysHooksAreNotRun(t *testing.T) {
	_, p, desk, repo := repoWithRemote(t)
	ask := askCommit(t, p, desk, command{Cmd: "gitPush", Path: repo, Req: "h"})
	if !strings.Contains(ask.Summary, "Hooks are not run for a push from the phone") {
		t.Errorf("the summary: %q", ask.Summary)
	}
}
