package radar

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// BenchmarkCycle times what one radar refresh costs a repository: a snapshot
// of each of N worktrees that have edits, and an Update over them. It is the
// number the conflict radar's default (off today) waits on: what a refresh
// takes on a large checkout. gitx's BenchmarkSnapshot says what seeding the
// scratch index from the real one saves.
//
// It is not run by go test. Run it by hand:
//
//	go test ./internal/radar -run '^$' -bench Cycle -benchtime 5x
//
// With no environment it builds a synthetic repository of 2,000 files. To ask
// about a real one, point it at a clone you can spare; it only reads the clone
// and makes its worktrees, branches and edits in a temporary place of its own
// (the worktrees are removed again, and so are their branches):
//
//	FLOCKDECK_RADAR_BENCH_REPO=/path/to/clone \
//	FLOCKDECK_RADAR_BENCH_PANES=6 \
//	FLOCKDECK_RADAR_BENCH_EDITS=30 \
//	go test ./internal/radar -run '^$' -bench Cycle -benchtime 5x
//
// Each worktree is a full checkout of the clone's HEAD, so a clone of a
// hundred thousand files takes a while and a lot of disk to set up. The edits
// are to the first EDITS tracked files, the same ones in every worktree and a
// different line each, so every pair meets and is merged: the worst case for
// the engine. Reported beside the time are the merges a cycle ran and how long
// the snapshots took of it.
func BenchmarkCycle(b *testing.B) {
	if !gitx.Available() {
		b.Skip("git is not installed")
	}
	if ok, why := gitx.CheckMergeTree(); !ok {
		b.Skip("merge-tree --write-tree is not available: " + why)
	}
	panes := envInt("FLOCKDECK_RADAR_BENCH_PANES", 4)
	edits := envInt("FLOCKDECK_RADAR_BENCH_EDITS", 10)

	repo := os.Getenv("FLOCKDECK_RADAR_BENCH_REPO")
	if repo == "" {
		repo = syntheticRepo(b, 2000)
	}
	common, err := gitx.CommonDir(repo)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	names := strings.Fields(strings.ReplaceAll(gitOut(b, repo, "ls-files", "-z"), "\x00", "\n"))
	if len(names) < edits {
		edits = len(names)
	}
	base := gitx.BaseOf(ctx, repo)

	dirs := make([]string, panes)
	root := b.TempDir()
	for i := range dirs {
		branch := "radar-bench-" + strconv.Itoa(i)
		dirs[i] = filepath.Join(root, branch)
		if err := gitx.AddNewBranch(repo, dirs[i], branch); err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() {
			_ = gitx.Remove(repo, dirs[i], true)
			_ = gitx.DeleteBranch(repo, branch)
		})
		for _, name := range names[:edits] {
			path := filepath.Join(dirs[i], filepath.FromSlash(name))
			data, err := os.ReadFile(path)
			if err != nil {
				b.Fatal(err)
			}
			lines := strings.Split(string(data), "\n")
			lines[min(i*3, len(lines)-1)] = "radar bench edit " + strconv.Itoa(i)
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}

	var snapshots time.Duration
	var merges int
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := NewEngine()
		s, err := gitx.NewScratch(common)
		if err != nil {
			b.Fatal(err)
		}
		began := time.Now()
		var in []Input
		for _, dir := range dirs {
			snap, err := gitx.Snapshot(ctx, dir, s, base, true)
			if err != nil {
				b.Fatal(err)
			}
			in = append(in, Input{ID: dir, Ready: !snap.Empty, Commit: snap.Commit, Tree: snap.Tree, Paths: snap.Paths})
		}
		snapshots += time.Since(began)
		r := e.Update(ctx, dirs[0], s, in)
		merges += r.Merges
		_ = s.Close()
	}
	b.ReportMetric(float64(snapshots.Milliseconds())/float64(b.N), "snapshot-ms/op")
	b.ReportMetric(float64(merges)/float64(b.N), "merges/op")
}

func envInt(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return def
}

func gitOut(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		tb.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// syntheticRepo makes a repository of n small files.
func syntheticRepo(tb testing.TB, n int) string {
	tb.Helper()
	repo := tb.TempDir()
	run := func(args ...string) { gitOut(tb, repo, args...) }
	run("init", "-q", "--initial-branch=main")
	for i := 0; i < n; i++ {
		dir := filepath.Join(repo, "d"+strconv.Itoa(i%40))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			tb.Fatal(err)
		}
		body := strings.Repeat("line of text\n", 30)
		if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)+".txt"), []byte(body), 0o600); err != nil {
			tb.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "synthetic")
	return repo
}
