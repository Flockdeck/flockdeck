package server

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/gitx"
)

func bareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "--bare")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	return dir
}

func refsOf(t *testing.T, bare string) string {
	t.Helper()
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname)")
	cmd.Dir = bare
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// The scan before a push can take seconds, and the repository is the agent's to
// write to meanwhile. Whatever it writes in that gap, an approved push sends the
// pinned commit to the pinned URL and nothing else.
func TestAnApprovedPushIsNotChangedByConfigWrittenDuringTheScan(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	good, evil := bareRepo(t), bareRepo(t)
	setConfig(t, repo, "remote.origin.url", good)
	edit(t, repo, "one\n")
	answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })

	pin, err := gitx.ResolvePin(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	pp := &pinnedRun{pin: pin, head: gitx.HeadSHA(repo)}

	was := scanPrograms
	t.Cleanup(func() { scanPrograms = was })

	// Widening settings written in the gap change nothing that is sent.
	scanPrograms = func(string) (gitx.Report, error) {
		setConfig(t, repo, "push.default", "matching")
		setConfig(t, repo, "--add", "remote.origin.push", "refs/heads/*:refs/heads/*")
		setConfig(t, repo, "push.followTags", "true")
		return gitx.Report{}, nil
	}
	_, notes := answers(t, func(c *controlClient) { srv.runRemotePinned(c, "push", repo, gitAnswer{}, pp) })
	for _, n := range notes {
		if n.Error {
			t.Fatalf("the pinned push failed: %s", n.Text)
		}
	}
	if got := refsOf(t, good); got != "refs/heads/main" {
		t.Fatalf("the pinned remote has %q, want only refs/heads/main", got)
	}

	// A push URL written in the gap is refused, and the other remote gets nothing.
	setConfig(t, repo, "--unset-all", "remote.origin.push")
	scanPrograms = func(string) (gitx.Report, error) {
		setConfig(t, repo, "remote.origin.pushurl", evil)
		return gitx.Report{}, nil
	}
	_, notes = answers(t, func(c *controlClient) { srv.runRemotePinned(c, "push", repo, gitAnswer{}, pp) })
	if len(notes) == 0 || !notes[len(notes)-1].Error || !strings.Contains(notes[len(notes)-1].Text, "changed after this was approved") {
		t.Fatalf("the change was not refused: %+v", notes)
	}
	if got := refsOf(t, evil); got != "" {
		t.Fatalf("the URL written in the gap received %q", got)
	}
}

// The first commit of a branch with nothing on it yet, pushed: the ask is made
// (what is sent is counted as none before it, and the commit makes it one).
func TestSealedFirstCommitWithPushOnAnUnbornBranchIsAsked(t *testing.T) {
	_, p, desk, repo := gitEnv(t)
	sealedGitRun(t, repo, "remote", "add", "origin", "https://example.com/team/new.git")
	sealedGitRun(t, repo, "checkout", "-q", "--orphan", "fresh")
	sealedGitRun(t, repo, "rm", "-rfq", ".")
	dirty(t, repo)
	if gitx.HeadSHA(repo) != "" {
		t.Fatal("the branch is not unborn")
	}
	ask := askCommit(t, p, desk, command{Cmd: "commit", Push: true, Path: repo, Text: "first", Req: "f"})
	if !strings.Contains(ask.Summary, "Sends 1 commit") {
		t.Errorf("the summary: %q", ask.Summary)
	}
}
