package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGitRunsNoFsmonitorCommandARepositoryNames covers a repository whose own
// .git/config names a command as its core.fsmonitor, which git status runs.
// Flockdeck reads the status of every checkout a pane opens in, in the
// background, so a repository unpacked from an archive had its command run
// before anybody had looked at it.
func TestGitRunsNoFsmonitorCommandARepositoryNames(t *testing.T) {
	repo := newRepo(t)
	marker := filepath.Join(repo, "fsmonitor-ran.log")
	gitRun(t, repo, "config", "core.fsmonitor", "echo ran >> fsmonitor-ran.log")

	// git on its own runs it, or there is nothing here to keep from running.
	plain := exec.Command("git", "status", "--porcelain")
	plain.Dir = repo
	_ = plain.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git runs no fsmonitor command, so there is nothing to stop")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	StatusOf(repo)
	if _, err := StatusWithin(repo, commandTimeout); err != nil {
		t.Fatalf("status: %v", err)
	}
	if _, err := Changes(repo); err != nil {
		t.Fatalf("changes: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("git run by Flockdeck ran the command the repository's core.fsmonitor names")
	}
}
