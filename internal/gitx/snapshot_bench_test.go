package gitx

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// BenchmarkSnapshot times one snapshot of a checkout with a few edited files,
// with the scratch index seeded from a copy of the real one and, for
// comparison, built from HEAD. The difference is what git add -A saves by
// having a stat cache to skip unchanged files with, which is the open
// question for a large checkout: unseeded, git add hashes every file in it.
//
// Run it by hand; go test does not:
//
//	go test ./internal/gitx -run '^$' -bench Snapshot -benchtime 10x
//
// With no environment it builds a repository of 2,000 files. To ask about a
// real one, point it at a clone. It only reads it: the snapshot is made in a
// scratch directory, the clone's index is copied and not opened for writing,
// and nothing is added to its objects.
//
//	FLOCKDECK_RADAR_BENCH_REPO=/path/to/clone \
//	go test ./internal/gitx -run '^$' -bench Snapshot -benchtime 10x
func BenchmarkSnapshot(b *testing.B) {
	if !Available() {
		b.Skip("git is not installed")
	}
	repo := os.Getenv("FLOCKDECK_RADAR_BENCH_REPO")
	if repo == "" {
		repo = newRepo(b)
		for i := 0; i < 2000; i++ {
			dir := fmt.Sprintf("d%02d", i%40)
			if err := os.MkdirAll(repo+"/"+dir, 0o700); err != nil {
				b.Fatal(err)
			}
			write(b, repo, fmt.Sprintf("%s/f%d.txt", dir, i), strings.Repeat("line of text\n", 30))
		}
		gitRun(b, repo, "add", "-A")
		gitRun(b, repo, "commit", "-m", "synthetic")
		write(b, repo, "d00/f0.txt", "edited\n")
		write(b, repo, "fresh.txt", "new\n")
	}
	common, err := CommonDir(repo)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	base := BaseOf(ctx, repo)

	for _, mode := range []struct {
		name string
		seed func(context.Context, string, []string, string) (bool, bool)
	}{
		{"seeded", seedIndex},
		{"unseeded", func(context.Context, string, []string, string) (bool, bool) { return false, false }},
	} {
		b.Run(mode.name, func(b *testing.B) {
			seedFromIndex = mode.seed
			b.Cleanup(func() { seedFromIndex = seedIndex })
			for i := 0; i < b.N; i++ {
				s, err := NewScratch(common)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := Snapshot(ctx, repo, s, base, true); err != nil {
					b.Fatal(err)
				}
				_ = s.Close()
			}
		})
	}
}
