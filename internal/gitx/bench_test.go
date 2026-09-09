package gitx

import (
	"fmt"
	"strings"
	"testing"
)

// benchRepo builds a repository with n tracked files changed and n untracked
// ones, which is the shape the review panel is slowest on.
func benchRepo(b *testing.B, n int) string {
	repo := newRepo(b)
	for i := 0; i < n; i++ {
		write(b, repo, fmt.Sprintf("tracked-%03d.txt", i), strings.Repeat("line\n", 40))
	}
	gitRun(b, repo, "add", "-A")
	gitRun(b, repo, "commit", "-m", "bench base")
	for i := 0; i < n; i++ {
		write(b, repo, fmt.Sprintf("tracked-%03d.txt", i), strings.Repeat("line\n", 40)+"changed\n")
		write(b, repo, fmt.Sprintf("fresh-%03d.txt", i), strings.Repeat("new\n", 40))
	}
	return repo
}

// BenchmarkDiff times clicking one file in the review panel.
func BenchmarkDiff(b *testing.B) {
	if !Available() {
		b.Skip("git is not installed")
	}
	repo := benchRepo(b, 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Diff(repo, "tracked-000.txt"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChanges times one refresh of the review panel's file list.
func BenchmarkChanges(b *testing.B) {
	if !Available() {
		b.Skip("git is not installed")
	}
	repo := benchRepo(b, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Changes(repo); err != nil {
			b.Fatal(err)
		}
	}
}
