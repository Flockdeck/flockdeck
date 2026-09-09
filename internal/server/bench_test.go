package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// benchRepo builds a checkout with n modified and n untracked files, the shape
// the review panel is slowest on.
func benchRepo(b *testing.B, n int) string {
	b.Helper()
	repo := b.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--initial-branch=main")
	run("config", "user.email", "bench@example.com")
	run("config", "user.name", "Bench")
	run("config", "commit.gpgsign", "false")

	body := strings.Repeat("line\n", 40)
	for i := 0; i < n; i++ {
		name := filepath.Join(repo, fmt.Sprintf("tracked-%03d.txt", i))
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-m", "bench base")
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("tracked-%03d.txt", i)), []byte(body+"changed\n"), 0o600); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("fresh-%03d.txt", i)), []byte(body), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	return repo
}

// BenchmarkCollectWorktrees times one refresh of the worktree panel over a
// repository with a handful of checkouts, which is what running several agents
// at once looks like.
func BenchmarkCollectWorktrees(b *testing.B) {
	repo := benchRepo(b, 5)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	run("stash", "--include-untracked")
	for i := 0; i < 4; i++ {
		run("worktree", "add", "-b", fmt.Sprintf("agent-%d", i),
			filepath.Join(b.TempDir(), fmt.Sprintf("wt-%d", i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if msg := collectWorktrees(repo); msg.Error != "" {
			b.Fatal(msg.Error)
		}
	}
}

// BenchmarkCollectChanges times one refresh of the review panel.
func BenchmarkCollectChanges(b *testing.B) {
	repo := benchRepo(b, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if msg := collectChanges(repo); msg.Error != "" {
			b.Fatal(msg.Error)
		}
	}
}
