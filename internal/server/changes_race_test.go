package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/changes"
	"github.com/jmwri/flockdeck/internal/session"
)

func TestLiveBranchesOfSkipsADirectoryWithNoRepositoryButNotOtherFailures(t *testing.T) {
	ctx := context.Background()
	w, _ := commentRepo(t)

	live, err := liveBranchesOf(ctx, w.cwd)
	if err != nil || !live["main"] || !live["feat"] {
		t.Fatalf("a repository: %v, %v", live, err)
	}
	for name, dir := range map[string]string{
		"not a repository": t.TempDir(),
		"gone":             filepath.Join(t.TempDir(), "missing"),
	} {
		got, err := liveBranchesOf(ctx, dir)
		if err != nil || len(got) != 0 {
			t.Errorf("%s: %v, %v", name, got, err)
		}
	}
	// A .git that exists means a repository that git cannot use, which is unknown,
	// not gone: an agent that can write .git could otherwise make its branches vanish.
	pointing := t.TempDir()
	if err := os.WriteFile(filepath.Join(pointing, ".git"), []byte("gitdir: "+filepath.ToSlash(filepath.Join(pointing, "nowhere"))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	emptyGit := t.TempDir()
	if err := os.Mkdir(filepath.Join(emptyGit, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"a .git file pointing nowhere": pointing, "an empty .git directory": emptyGit} {
		if got, err := liveBranchesOf(ctx, dir); err == nil {
			t.Errorf("%s: %v, nil error, want an error", name, got)
		}
	}
	// A pane outside any repository no longer stops branches from being counted
	// as gone: the repository's own branches are still listed.
	union := unionBranches([]string{w.cwd, t.TempDir()}, func(d string) (map[string]bool, error) { return liveBranchesOf(ctx, d) })
	if union == nil || !union["feat"] {
		t.Errorf("union = %v", union)
	}
	// A listing that fails for another reason is still unknown, so nothing is gone.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if got := unionBranches([]string{w.cwd}, func(d string) (map[string]bool, error) { return liveBranchesOf(cancelled, d) }); got != nil {
		t.Errorf("a failed listing gave %v, want nil", got)
	}
}

// Meant to be run with -race: the project's pane list is read on request goroutines
// while the workspace changes on its own.
func TestProjectCwdsIsSafeWhilePanesChange(t *testing.T) {
	srv, ws := newTestServer(t)
	root, ok := ask(srv, func() string { return ws.ActiveRoot() })
	if !ok {
		t.Fatal("server closed")
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if _, ok := srv.projectCwds(root); !ok {
					t.Error("projectCwds failed")
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			srv.do(func() { ws.NewTab(session.KindShell, ws.ActiveRoot(), fmt.Sprintf("race-%d", i)) })
		}
	}()
	wg.Wait()
}

// Meant to be run with -race: reviews, comments and approvals of one project are
// written and read at once, as commands and pushes do, and every store must still
// load afterwards with everything that was written.
func TestTheReviewCommentAndGateStoresAreSafeUnderConcurrentUse(t *testing.T) {
	srv, _ := newTestServer(t)
	w, _ := commentRepo(t)
	const root, project = "race-root", "race-project"
	_, statePath, err := changesFiles(root, project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	w.store = commentsPath(statePath)
	ctx := context.Background()
	head := w.branch.HeadSha
	const n = 8

	var wg sync.WaitGroup
	run := func(f func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				f(i)
			}
		}()
	}
	run(func(i int) { // reviews
		r := changes.Review{Reviewer: fmt.Sprintf("rev%d", i), State: "requested", At: time.Now().UTC().Format(time.RFC3339), AtSha: head}
		if err := srv.recordReview(w.cwd, root, project, w.branch.Branch, r); err != nil {
			t.Errorf("review %d: %v", i, err)
		}
	})
	run(func(i int) { // comments
		if _, ref := addComment(ctx, w, commentCmd(w, "a.txt", "new", 1, fmt.Sprintf("comment %d", i))); ref != nil {
			t.Errorf("comment %d: %+v", i, ref)
		}
	})
	run(func(i int) { // approvals made and dropped
		gp := gatePath(statePath)
		reviewsMu.Lock()
		st, err := changes.LoadGate(gp)
		if err == nil {
			st.Approve(fmt.Sprintf("other%d", i), head, "you", time.Now().UTC().Format(time.RFC3339))
			err = st.Save(gp)
		}
		reviewsMu.Unlock()
		if err != nil {
			t.Errorf("approve %d: %v", i, err)
		}
		dropApprovals(gp, []changes.Approval{{Branch: fmt.Sprintf("other%d", i), Sha: head}})
	})
	run(func(i int) { // pushes
		b := w.branch
		checks, reviews, comments := []changes.Check{}, []changes.Review{}, []changes.Comment{}
		b.Checks, b.Reviews, b.Comments = &checks, &reviews, &comments
		team := changes.Team{Branches: []changes.Branch{b}}
		srv.withChecksAndReviews(&team, root, project)
	})
	wg.Wait()

	if st, err := changes.LoadReviews(statePath); err != nil || len(st.For(w.branch.Branch, head)) != n {
		t.Errorf("reviews: %d, %v", len(st.For(w.branch.Branch, head)), err)
	}
	if st, err := changes.LoadComments(w.store); err != nil || len(st.For(w.branch.Branch, w.pane, nil)) != n {
		t.Errorf("comments: %d, %v", len(st.Comments), err)
	}
	if _, err := changes.LoadGate(gatePath(statePath)); err != nil {
		t.Errorf("gate: %v", err)
	}
}
