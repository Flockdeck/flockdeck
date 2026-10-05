package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/radar"
)

// isolatedGit keeps the git these tests start from the configuration of whoever
// runs them: an empty system configuration, a global one with only an identity,
// and a home folder of its own.
func isolatedGit(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	global := filepath.Join(dir, "gitconfig")
	cfg := "[user]\n\tname = Test\n\temail = test@example.com\n[commit]\n\tgpgsign = false\n"
	if err := os.WriteFile(global, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME"} {
		t.Setenv(name, dir)
	}
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

func radarGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// The radar end to end, with nothing stubbed but whether it is on and how long
// apart two refreshes must be to count as two: real status, real snapshots in a
// real scratch directory, a real merge-tree. Two linked worktrees edit line 2 of
// one file; after two refreshes each pane names the other. This is the path that
// died with a nil scratch the first time a conflict was found.
func TestRadarEndToEndWithRealGit(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	if ok, why := gitx.CheckMergeTree(); !ok {
		t.Skip("merge-tree --write-tree is not available: " + why)
	}
	isolatedGit(t)
	repo := t.TempDir()
	radarGitIn(t, repo, "init", "-q", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("1\n2\n3\n4\n5\n6\n7\n8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	radarGitIn(t, repo, "add", "-A")
	radarGitIn(t, repo, "commit", "-q", "-m", "base")
	var dirs []string
	for _, name := range []string{"wa", "wb"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := gitx.AddNewBranch(repo, dir, name); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	for i, text := range []string{"1\nfrom a\n3\n4\n5\n6\n7\n8\n", "1\nfrom b\n3\n4\n5\n6\n7\n8\n"} {
		if err := os.WriteFile(filepath.Join(dirs[i], "f.txt"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	oldEnabled, oldEngine, oldSweep := radarEnabled, radarEngine, radarSweep
	t.Cleanup(func() { radarEnabled, radarEngine, radarSweep = oldEnabled, oldEngine, oldSweep })
	radarEnabled = func() bool { return true }
	radarSweep = func() {}
	radarEngine = func() *radar.Engine { return radar.NewEngineWith(radar.Options{MinGap: time.Millisecond}) }

	w := &Workspace{panes: map[string]*Pane{
		"pa": {ID: "pa", Cwd: dirs[0], Name: "a"},
		"pb": {ID: "pb", Cwd: dirs[1], Name: "b"},
	}, BroadcastSet: map[string]bool{}}

	t.Cleanup(w.waitGitIdle) // after the restores above, so it runs before them
	w.RefreshGit(func(apply func()) { apply() })
	if got := conflictsOf(w, "pa"); len(got) != 0 {
		t.Fatalf("the first refresh showed %v", got)
	}
	time.Sleep(20 * time.Millisecond)
	w.RefreshGit(func(apply func()) { apply() })

	for id, other := range map[string]string{"pa": "pb", "pb": "pa"} {
		got := conflictsOf(w, id)
		if len(got) != 1 || got[0].With != other || len(got[0].Paths) != 1 || got[0].Paths[0] != "f.txt" {
			t.Errorf("pane %s: %+v, want a conflict with %s in f.txt", id, got, other)
		}
	}
}

// Two panes in different folders of one checkout are one checkout to the radar:
// it is snapshotted once, and both panes are told.
func TestRadarSnapshotsACheckoutOnceHoweverManyFoldersHavePanes(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	w.panes["pa2"].Cwd = "/repo/a/sub"
	// The status of a folder in a checkout is the checkout's.
	r.status["/repo/a/sub"] = r.status["/repo/a"]
	radarRoot = func(dir string) (string, error) {
		if dir == "/repo/a/sub" {
			return "/repo/a", nil
		}
		return dir, nil
	}
	r.refresh(w)
	r.refresh(w)
	n := 0
	for _, d := range r.snapshots {
		if d == "/repo/a" || d == "/repo/a/sub" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("the checkout was snapshotted %d times in two refreshes, want once each", n)
	}
	want := conflictsOf(w, "pb")
	if len(want) != 2 {
		t.Fatalf("pane b is told of %v, want both panes of a's checkout", want)
	}
	for _, id := range []string{"pa", "pa2"} {
		if got := conflictsOf(w, id); len(got) != 1 || got[0].With != "pb" {
			t.Errorf("pane %s: %v, want a conflict with b", id, got)
		}
	}
}

// A base that git could not be asked for is not an answer: no snapshot is made
// measured from nothing, the pair is kept, and the next refresh asks again.
func TestRadarBaseErrorIsUnknownAndNotCached(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	before := r.snapshotCount()
	r.mu.Lock()
	r.baseErr = errors.New("git timed out")
	r.mu.Unlock()
	r.fresh = false // the candidates are looked up again each refresh
	calls := r.baseCalls
	r.refresh(w)
	r.refresh(w)
	if got := r.snapshotCount(); got != before {
		t.Errorf("%d snapshots were made with no base", got-before)
	}
	if r.baseCalls-calls != 2 {
		t.Errorf("the base was asked for %d times in two refreshes, want each time", r.baseCalls-calls)
	}
	if got := conflictsOf(w, "pa"); len(got) != 1 {
		t.Errorf("pane a = %v, want the chip kept", got)
	}
	// And the failure is not remembered as a base.
	r.mu.Lock()
	r.baseErr = nil
	r.mu.Unlock()
	r.refresh(w)
	if r.snapshotCount() == before {
		t.Error("nothing was snapshotted once the base could be read again")
	}
}

// A clean checkout whose snapshot failed is not marked clean.
func TestRadarCleanMarkIsNotPlantedByAFailedSnapshot(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.fail["/repo/c"] = errors.New("git: merge-base failed")
	w := radarWorkspace()
	count := func() int {
		r.mu.Lock()
		defer r.mu.Unlock()
		n := 0
		for _, d := range r.snapshots {
			if d == "/repo/c" {
				n++
			}
		}
		return n
	}
	r.refresh(w)
	r.refresh(w)
	if got := count(); got != 2 {
		t.Errorf("the clean checkout was snapshotted %d times, want each refresh while it fails", got)
	}
}

// A clean mark is for the base it was found against: when the base moves, the
// checkout may have work against the new one.
func TestRadarCleanMarkDoesNotSurviveABaseMove(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	count := func() int {
		r.mu.Lock()
		defer r.mu.Unlock()
		n := 0
		for _, d := range r.snapshots {
			if d == "/repo/c" {
				n++
			}
		}
		return n
	}
	r.refresh(w)
	r.refresh(w)
	if got := count(); got != 1 {
		t.Fatalf("setup: the clean checkout was snapshotted %d times, want once", got)
	}
	r.mu.Lock()
	r.base, r.fresh = "a-newer-base", false
	r.mu.Unlock()
	r.refresh(w)
	if got := count(); got != 2 {
		t.Errorf("the clean checkout was snapshotted %d times after the base moved, want again", got)
	}
}

// Pruning does not drop an engine another refresh is working in.
func TestRadarPruneLeavesABusyEngineAlone(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	w.gitMu.Lock()
	engine := w.radar.engines["/repo/.git"]
	gen := w.radar.gen
	w.gitMu.Unlock()
	end, ok := engine.Begin()
	if !ok {
		t.Fatal("setup: the engine was busy")
	}
	w.mu.Lock()
	w.panes = map[string]*Pane{}
	w.mu.Unlock()
	w.pruneRadar(gen)
	w.gitMu.Lock()
	held := w.radar.engines["/repo/.git"] == engine
	w.gitMu.Unlock()
	end()
	if !held {
		t.Error("an engine another refresh was working in was dropped")
	}
	w.pruneRadar(gen)
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if w.radar.engines["/repo/.git"] != nil {
		t.Error("an idle engine with no panes was kept")
	}
}

// The sweep of old scratch directories runs once for the process, the first time
// the radar runs, and not while the workspace's git lock is held: it lists and
// removes directories.
func TestRadarSweepsOnceAndOutsideTheLock(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	old := sweepScratchOnce
	t.Cleanup(func() { sweepScratchOnce = old })
	sweepScratchOnce = new(sync.Once)
	calls, heldLock := 0, false
	radarSweep = func() {
		calls++
		if !w.gitMu.TryLock() {
			heldLock = true
			return
		}
		w.gitMu.Unlock()
	}
	r.refresh(w)
	r.refresh(w)
	if calls != 1 {
		t.Errorf("the sweep ran %d times, want once", calls)
	}
	if heldLock {
		t.Error("the sweep ran with gitMu held")
	}
}

// A repository with no base branch to be found is unknown: nothing is snapshotted
// measured from nothing, and the search is not made again for a few seconds, and
// then is.
func TestRadarNoBaseBranchIsRememberedBrieflyAndSnapshotsNothing(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.baseErr = gitx.ErrNoBase
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	r.refresh(w)
	if got := r.snapshotCount(); got != 0 {
		t.Errorf("%d snapshots were made with no base", got)
	}
	if r.baseCalls != 1 {
		t.Errorf("the base was searched for %d times in three refreshes, want once", r.baseCalls)
	}
	radarNoBaseTTL = 0
	t.Cleanup(func() { radarNoBaseTTL = 5 * time.Second })
	r.refresh(w)
	if r.baseCalls != 2 {
		t.Errorf("the base was searched for %d times once the short memory ran out, want again", r.baseCalls)
	}
}

// A clean checkout with too many files against the base is remembered as such,
// by its commit and the base, and not snapshotted again every refresh to find
// that out.
func TestRadarCleanCheckoutOverTheCapIsNotSnapshottedAgain(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.fail["/repo/c"] = gitx.ErrTooManyFiles
	w := radarWorkspace()
	for i := 0; i < 3; i++ {
		r.refresh(w)
	}
	n := 0
	r.mu.Lock()
	for _, d := range r.snapshots {
		if d == "/repo/c" {
			n++
		}
	}
	r.mu.Unlock()
	if n != 1 {
		t.Errorf("the clean checkout over the cap was snapshotted %d times in three refreshes, want once", n)
	}
}

func (r *radarRig) count(dir string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, d := range r.snapshots {
		if d == dir {
			n++
		}
	}
	return n
}

// A snapshot that shares no history with the base chosen says nothing was
// checked: the checkout is not marked clean, so it is looked at every refresh,
// and its pairs are kept. A base that cannot be chosen for it for the same reason
// (gitx.ErrUnrelated) is the same.
func TestRadarUnrelatedBaseIsUnknownNotClean(t *testing.T) {
	for name, set := range map[string]func(r *radarRig){
		"the snapshot says so": func(r *radarRig) { r.unrelated["/repo/c"] = true },
		"no candidate relates": func(r *radarRig) { r.chooseErr["/repo/c"] = gitx.ErrUnrelated },
	} {
		r := newRadarRig(t)
		r.setUp()
		set(r)
		w := radarWorkspace()
		r.refresh(w)
		r.refresh(w)
		r.refresh(w)
		asked := r.count("/repo/c")
		if name == "no candidate relates" {
			r.mu.Lock()
			asked = r.chooseCalls
			r.mu.Unlock()
		}
		if asked < 3 {
			t.Errorf("%s: the unrelated checkout was looked at %d times in three refreshes, want every refresh", name, asked)
		}
		if got := conflictsOf(w, "pa"); len(got) != 1 {
			t.Errorf("%s: pane a = %v, want its pair kept", name, got)
		}
	}
}

// A base looked up by a refresh from before the radar was switched off and on
// again is not kept: it belongs to the radar as it was.
func TestRadarBasesOfWritesNothingForAStaleGeneration(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	w.gitMu.Lock()
	old := w.radar.gen
	w.gitMu.Unlock()
	r.setOn(false)
	r.refresh(w)
	r.setOn(true)
	r.refresh(w)
	w.gitMu.Lock()
	w.radar.bases = nil
	w.gitMu.Unlock()

	if _, err := w.radarBasesOf(context.Background(), old, "/repo/.git", "/repo/a"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.baseErr = gitx.ErrNoBase
	r.mu.Unlock()
	if _, err := w.radarBasesOf(context.Background(), old, "/repo/.git", "/repo/a"); err == nil {
		t.Fatal("setup: no ErrNoBase")
	}
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if len(w.radar.bases) != 0 {
		t.Errorf("a stale generation wrote %v", w.radar.bases)
	}
}

func (r *radarRig) chooses() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chooseCalls
}

// The base chosen for a checkout is remembered for the commit it is on while the
// candidates stay where they are, and chosen again when one of them moves.
func TestRadarChoosesABaseOncePerCommitAndCandidates(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	for i := 0; i < 3; i++ {
		r.refresh(w)
	}
	if got := r.chooses(); got != 3 {
		t.Errorf("%d choices in three refreshes for three checkouts, want one each", got)
	}
	r.mu.Lock()
	r.extraBase, r.fresh = "another-candidate", false
	r.mu.Unlock()
	r.refresh(w)
	if got := r.chooses(); got != 6 {
		t.Errorf("%d choices after a candidate appeared, want them chosen again", got)
	}
}

// Whether the candidates moved is asked of git every refresh, and they are looked
// up again only when they have.
func TestRadarLooksUpBasesAgainOnlyWhenTheyMoved(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	for i := 0; i < 3; i++ {
		r.refresh(w)
	}
	if r.baseCalls != 1 || r.freshCalls != 2 {
		t.Errorf("%d lookups and %d freshness checks in three refreshes, want 1 and 2", r.baseCalls, r.freshCalls)
	}
	r.mu.Lock()
	r.fresh = false
	r.mu.Unlock()
	r.refresh(w)
	if r.baseCalls != 2 {
		t.Errorf("%d lookups after the base moved, want a second", r.baseCalls)
	}
}

// A checkout whose folder was deleted and which git cannot read for three
// refreshes in a row stops holding its peer's chip. A failure with the folder
// still there does not count.
func TestRadarDropsAPairWhoseFolderWasDeleted(t *testing.T) {
	for _, gone := range []bool{true, false} {
		r := newRadarRig(t)
		radarMissingGap = 0
		t.Cleanup(func() { radarMissingGap = 5 * time.Second })
		r.setUp()
		w := radarWorkspace()
		r.refresh(w)
		r.refresh(w)
		if len(conflictsOf(w, "pa")) == 0 {
			t.Fatal("setup: no chip")
		}
		r.mu.Lock()
		r.statusErr["/repo/b"] = errors.New("not a git repository")
		r.missing["/repo/b"] = gone
		r.mu.Unlock()
		r.refresh(w)
		r.refresh(w)
		if got := conflictsOf(w, "pa"); len(got) != 1 {
			t.Errorf("gone=%v: pane a after two failed reads = %v, want the chip kept", gone, got)
		}
		r.refresh(w)
		got := conflictsOf(w, "pa")
		if gone && len(got) != 0 {
			t.Errorf("pane a still has %v after its peer's folder was gone for three refreshes", got)
		}
		if !gone && len(got) != 1 {
			t.Errorf("pane a = %v, want the chip kept while the folder is there and git fails", got)
		}
	}
}

// A partial clone on a git that cannot be told not to fetch is not run, and
// that is said once.
func TestRadarLeavesAPartialCloneOnAnOldGitAlone(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.lazy["*"] = true
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	if got := r.snapshotCount(); got != 0 {
		t.Errorf("%d snapshots in a partial clone on an old git", got)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, l := range r.logs {
		if strings.Contains(l, "partial clone") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("a partial clone was logged %d times over two refreshes and two repositories, want once each: %v", n, r.logs)
	}
}

// Two commits can share their first seven characters; the clean mark is for the
// whole id.
func TestRadarCleanMarkComparesTheWholeCommitId(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	if got := r.count("/repo/c"); got != 1 {
		t.Fatalf("setup: the clean checkout was snapshotted %d times, want once", got)
	}
	r.mu.Lock()
	st := r.status["/repo/c"]
	st.Commit = "abc1234" + strings.Repeat("f", 33)
	r.status["/repo/c"] = st // the same short Head, another commit
	r.mu.Unlock()
	r.refresh(w)
	if got := r.count("/repo/c"); got != 2 {
		t.Errorf("a commit with the same short id was taken for the one marked clean: %d snapshots, want 2", got)
	}
}

// Closing every pane of a repository lets go of everything the radar learned of
// it, in the refresh that finds none left, and a reopened repository is looked at
// afresh: engines, repository and top-level lookups, bases and the partial-clone
// verdict are all gone.
func TestRadarLetsGoOfEverythingWhenTheLastPanesClose(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.lazy["*"] = false
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	w.gitMu.Lock()
	held := len(w.radar.engines) + len(w.radar.repoOf) + len(w.radar.roots) + len(w.radar.bases) + len(w.radar.lazy)
	w.gitMu.Unlock()
	if held == 0 {
		t.Fatal("setup: the radar holds nothing")
	}
	w.mu.Lock()
	w.panes = map[string]*Pane{}
	w.mu.Unlock()
	r.refresh(w)
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if n := len(w.radar.engines); n != 0 {
		t.Errorf("%d engines held with no panes", n)
	}
	if n := len(w.radar.repoOf); n != 0 {
		t.Errorf("%d repository lookups held with no panes", n)
	}
	if n := len(w.radar.roots) + len(w.radar.rootOf); n != 0 {
		t.Errorf("%d top-level lookups held with no panes", n)
	}
	if n := len(w.radar.bases); n != 0 {
		t.Errorf("%d bases held with no panes", n)
	}
	if n := len(w.radar.lazy); n != 0 {
		t.Errorf("%d partial-clone verdicts held with no panes", n)
	}
	if n := len(w.radar.choices) + len(w.radar.clean); n != 0 {
		t.Errorf("%d choices and clean marks held with no panes", n)
	}
}

// The candidates are looked up again when a ref hid behind another moves, with the
// real freshness check and real git: nothing about it is stubbed but the count.
func TestRadarLooksUpBasesAgainWhenAHiddenRefMovesRealGit(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	if ok, why := gitx.CheckMergeTree(); !ok {
		t.Skip("merge-tree --write-tree is not available: " + why)
	}
	isolatedGit(t)
	repo := t.TempDir()
	radarGitIn(t, repo, "init", "-q", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("1\n2\n3\n4\n5\n6\n7\n8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	radarGitIn(t, repo, "add", "-A")
	radarGitIn(t, repo, "commit", "-q", "-m", "base")
	var dirs []string
	for _, name := range []string{"wa", "wb"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := gitx.AddNewBranch(repo, dir, name); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	if err := os.WriteFile(filepath.Join(dirs[0], "extra.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	radarGitIn(t, dirs[0], "add", "-A")
	radarGitIn(t, dirs[0], "commit", "-q", "-m", "wa commits")
	// The main worktree is on dev, at main's commit: main is a ref hidden behind it.
	radarGitIn(t, repo, "checkout", "-q", "-b", "dev")
	for i, text := range []string{"1\nfrom a\n3\n4\n5\n6\n7\n8\n", "1\n2\n3\n4\n5\n6\nfrom b\n8\n"} {
		if err := os.WriteFile(filepath.Join(dirs[i], "f.txt"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	oldEnabled, oldEngine, oldSweep, oldBases := radarEnabled, radarEngine, radarSweep, radarBases
	t.Cleanup(func() { radarEnabled, radarEngine, radarSweep, radarBases = oldEnabled, oldEngine, oldSweep, oldBases })
	radarEnabled = func() bool { return true }
	radarSweep = func() {}
	radarEngine = func() *radar.Engine { return radar.NewEngineWith(radar.Options{MinGap: time.Millisecond}) }
	var lookups atomic.Int32
	radarBases = func(ctx context.Context, dir string) (*gitx.Bases, error) {
		lookups.Add(1)
		return oldBases(ctx, dir)
	}
	w := &Workspace{panes: map[string]*Pane{
		"pa": {ID: "pa", Cwd: dirs[0], Name: "a"},
		"pb": {ID: "pb", Cwd: dirs[1], Name: "b"},
	}, BroadcastSet: map[string]bool{}}
	t.Cleanup(w.waitGitIdle) // after the restores above, so it runs before them
	for i := 0; i < 3; i++ {
		w.RefreshGit(func(apply func()) { apply() })
	}
	if lookups.Load() != 1 {
		t.Fatalf("the candidates were looked up %d times in three refreshes with nothing moved, want once", lookups.Load())
	}
	// main moves while dev stays: only the real freshness check can see it.
	radarGitIn(t, repo, "branch", "-f", "main", "wa")
	w.RefreshGit(func(apply func()) { apply() })
	if lookups.Load() != 2 {
		t.Errorf("the candidates were looked up %d times after main moved behind dev, want again", lookups.Load())
	}
}

// A partial-clone check that cannot read the configuration fails closed: the
// radar is not run, the reason is logged once for each repository, and the check
// is made again at the next refresh and not remembered.
func TestRadarPartialCloneCheckThatFailsFailsClosed(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	for _, d := range []string{"/repo/a", "/repo/b", "/repo/c", "/repo/d", "/other/x"} {
		r.lazyErr[d] = errors.New("git config: timed out")
	}
	var checks atomic.Int32
	old := radarLazy
	radarLazy = func(ctx context.Context, dir string) (bool, error) { checks.Add(1); return old(ctx, dir) }
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	if got := r.snapshotCount(); got != 0 {
		t.Errorf("%d snapshots made when the partial-clone check failed", got)
	}
	if checks.Load() < 4 {
		t.Errorf("the check was made %d times over two refreshes and two repositories, want it again each refresh", checks.Load())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, l := range r.logs {
		if strings.Contains(l, "could not tell whether") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("the failure was logged %d times, want once for each of two repositories: %v", n, r.logs)
	}
}

func (r *radarRig) failFolder(dir string, gone bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusErr[dir] = errors.New("not a git repository")
	r.missing[dir] = gone
}

// Three misses in a burst of refreshes asked for out of turn are one: the radar
// waits for them to be apart before it lets go.
func TestRadarMissesMustBeApartToCount(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	r.failFolder("/repo/b", true)
	for i := 0; i < 6; i++ {
		r.refresh(w)
	}
	if got := conflictsOf(w, "pa"); len(got) != 1 {
		t.Errorf("pane a after six misses in a burst = %v, want the chip kept", got)
	}
	// With the gap gone, three more are three.
	radarMissingGap = 0
	t.Cleanup(func() { radarMissingGap = 5 * time.Second })
	r.refresh(w)
	r.refresh(w)
	r.refresh(w)
	if got := conflictsOf(w, "pa"); len(got) != 0 {
		t.Errorf("pane a after misses that were apart = %v, want the chip dropped", got)
	}
}

// A subfolder deleted from a checkout that is there says nothing about the
// checkout: only its top-level folder being gone counts.
func TestRadarDeletedSubfolderIsNotADeletedCheckout(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	radarMissingGap = 0
	t.Cleanup(func() { radarMissingGap = 5 * time.Second })
	w := radarWorkspace()
	w.panes["pb2"] = &Pane{ID: "pb2", Cwd: "/repo/b/sub", Name: "b in a subfolder"}
	r.status["/repo/b/sub"] = r.status["/repo/b"]
	radarRoot = func(dir string) (string, error) {
		if dir == "/repo/b/sub" {
			return "/repo/b", nil
		}
		return dir, nil
	}
	r.refresh(w)
	r.refresh(w)
	if len(conflictsOf(w, "pa")) == 0 {
		t.Fatal("setup: no chip")
	}
	// Only the pane in the subfolder is left on that checkout, so nothing else reads it.
	w.mu.Lock()
	delete(w.panes, "pb")
	w.mu.Unlock()
	// The subfolder is deleted, git cannot read it, and the checkout's top level is there.
	r.failFolder("/repo/b/sub", true)
	for i := 0; i < 6; i++ {
		r.refresh(w)
	}
	if got := conflictsOf(w, "pa"); len(got) == 0 {
		t.Error("a deleted subfolder dropped the pair with its checkout")
	}
}

// A refresh that cannot drop the pairs because another is working in the
// repository tries again at the next, and every refresh after the third miss
// drops them, not only the third.
func TestRadarForgetRetriesWhileTheEngineIsBusy(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	radarMissingGap = 0
	t.Cleanup(func() { radarMissingGap = 5 * time.Second })
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	w.gitMu.Lock()
	engine := w.radar.engines["/repo/.git"]
	w.gitMu.Unlock()
	end, ok := engine.Begin()
	if !ok {
		t.Fatal("setup: the engine was busy")
	}
	r.failFolder("/repo/b", true)
	for i := 0; i < 4; i++ {
		r.refresh(w) // the radar's own work is refused while it is held
	}
	if got := conflictsOf(w, "pa"); len(got) != 1 {
		t.Errorf("pane a = %v, want the chip kept while the engine is busy", got)
	}
	end()
	r.refresh(w)
	if got := conflictsOf(w, "pa"); len(got) != 0 {
		t.Errorf("pane a = %v, want the pairs dropped at the next refresh after the engine was free", got)
	}
}

// A snapshot failure that is not one of the known kinds is not silent: it is logged
// with the first line of what git said, once for each checkout in a while.
func TestRadarLogsASnapshotFailureOnceInAWhile(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.fail["/repo/b"] = errors.New("git add: fatal: something odd\nsecond line that is not shown")
	w := radarWorkspace()
	for i := 0; i < 3; i++ {
		r.refresh(w)
	}
	count := func() (n int, text string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, l := range r.logs {
			if strings.Contains(l, "could not snapshot") {
				n++
				text = l
			}
		}
		return
	}
	n, text := count()
	if n != 1 || !strings.Contains(text, "something odd") || strings.Contains(text, "second line") {
		t.Errorf("logged %d times, last %q, want once with the first line only", n, text)
	}
	radarErrLogEvery = 0
	t.Cleanup(func() { radarErrLogEvery = 10 * time.Minute })
	r.refresh(w)
	if n, _ := count(); n != 2 {
		t.Errorf("logged %d times once the interval was up, want again", n)
	}
}

// Closing the panes of a repository lets go of its partial-clone failure record too.
func TestRadarLetsGoOfThePartialCloneFailureRecordWhenPanesClose(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	for _, d := range []string{"/repo/a", "/repo/b", "/repo/c", "/repo/d", "/other/x"} {
		r.lazyErr[d] = errors.New("git config: timed out")
	}
	w := radarWorkspace()
	r.refresh(w)
	w.gitMu.Lock()
	held := len(w.radar.lazyErr)
	w.gitMu.Unlock()
	if held == 0 {
		t.Fatal("setup: no failure record")
	}
	w.mu.Lock()
	w.panes = map[string]*Pane{}
	w.mu.Unlock()
	r.refresh(w)
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if n := len(w.radar.lazyErr); n != 0 {
		t.Errorf("%d partial-clone failure records held with no panes", n)
	}
}

// With an empty repository in each pane's checkout, which makes git add -A fail
// outright, two panes editing the same line still get their chips, at the second
// refresh: the checkout is evaluated with the empty repository left out.
func TestRadarEndToEndWithAnEmptyNestedRepository(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	if ok, why := gitx.CheckMergeTree(); !ok {
		t.Skip("merge-tree --write-tree is not available: " + why)
	}
	isolatedGit(t)
	repo := t.TempDir()
	radarGitIn(t, repo, "init", "-q", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("1\n2\n3\n4\n5\n6\n7\n8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	radarGitIn(t, repo, "add", "-A")
	radarGitIn(t, repo, "commit", "-q", "-m", "base")
	var dirs []string
	for _, name := range []string{"wa", "wb"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := gitx.AddNewBranch(repo, dir, name); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
		radarGitIn(t, dir, "init", "-q", "--initial-branch=main", "emb") // no commits in it
	}
	for i, text := range []string{"1\nfrom a\n3\n4\n5\n6\n7\n8\n", "1\nfrom b\n3\n4\n5\n6\n7\n8\n"} {
		if err := os.WriteFile(filepath.Join(dirs[i], "f.txt"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldEnabled, oldEngine, oldSweep := radarEnabled, radarEngine, radarSweep
	t.Cleanup(func() { radarEnabled, radarEngine, radarSweep = oldEnabled, oldEngine, oldSweep })
	radarEnabled = func() bool { return true }
	radarSweep = func() {}
	radarEngine = func() *radar.Engine { return radar.NewEngineWith(radar.Options{MinGap: time.Millisecond}) }
	w := &Workspace{panes: map[string]*Pane{
		"pa": {ID: "pa", Cwd: dirs[0], Name: "a"},
		"pb": {ID: "pb", Cwd: dirs[1], Name: "b"},
	}, BroadcastSet: map[string]bool{}}
	t.Cleanup(w.waitGitIdle) // after the restores above, so it runs before them
	w.RefreshGit(func(apply func()) { apply() })
	time.Sleep(20 * time.Millisecond)
	w.RefreshGit(func(apply func()) { apply() })
	for id, other := range map[string]string{"pa": "pb", "pb": "pa"} {
		got := conflictsOf(w, id)
		if len(got) != 1 || got[0].With != other || len(got[0].Paths) != 1 || got[0].Paths[0] != "f.txt" {
			t.Errorf("pane %s: %+v, want a conflict with %s in f.txt", id, got, other)
		}
	}
}

// The candidate bases are looked up again after a while whatever the freshness check
// says, so that a branch created later is noticed at last.
func TestRadarLooksUpBasesAgainAfterTheirMaxAge(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	for i := 0; i < 3; i++ {
		r.refresh(w)
	}
	if r.baseCalls != 1 {
		t.Fatalf("setup: %d lookups in three refreshes, want one", r.baseCalls)
	}
	radarBasesMaxAge = 0
	t.Cleanup(func() { radarBasesMaxAge = 10 * time.Minute })
	r.refresh(w)
	r.refresh(w)
	if r.baseCalls != 3 {
		t.Errorf("%d lookups after the maximum age was up, want one at each refresh", r.baseCalls)
	}
}

// A failure to choose a base for a checkout is logged too, and one that is only a
// deadline running out, or the radar being switched off, is not logged as a failure.
func TestRadarLogsABaseThatCouldNotBeChosen(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.chooseErr["/repo/a"] = errors.New("git merge-base: fatal: not a valid commit name")
	w := radarWorkspace()
	for i := 0; i < 3; i++ {
		r.refresh(w)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	odd := 0
	for _, l := range r.logs {
		if strings.Contains(l, "not a valid commit name") {
			odd++
		}
	}
	if odd != 1 {
		t.Errorf("the base failure was logged %d times, want once: %v", odd, r.logs)
	}
}

func (r *radarRig) logged(substr string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, l := range r.logs {
		if strings.Contains(l, substr) {
			n++
		}
	}
	return n
}

// The radar's own time running out while a checkout's snapshot is the one running is
// that checkout's cost, every refresh, with no chip: it is counted like a command's
// timeout, said with what was running, and after three the checkout is left alone.
func TestRadarsOwnDeadlineInASnapshotIsCountedAndBacksOffTheCheckout(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.slow["/repo/b"] = true
	// The checkout has to have been running for radarMinRun when the deadline runs
	// out, so the deadline is long enough that a slow start of the refresh on a
	// loaded machine does not leave it less than that.
	radarDeadline = 300 * time.Millisecond
	radarBackoffStart = time.Hour
	radarMinRun = 10 * time.Millisecond
	t.Cleanup(func() {
		radarDeadline = 30 * time.Second
		radarBackoffStart = 10 * time.Minute
		radarMinRun = 15 * time.Second
	})
	w := radarWorkspace()
	r.refresh(w)
	if n := r.logged("was still being copied (copying it)"); n != 1 {
		t.Errorf("the deadline was said %d times after one refresh, want once: %v", n, r.logs)
	}
	r.refresh(w)
	r.refresh(w)
	if n := r.logged("is skipped for 60 minutes"); n != 1 || r.logged("copying it") < 1 {
		t.Fatalf("the back-off was said %d times after three deadlines, want once: %v", n, r.logs)
	}
	asked := r.count("/repo/b")
	r.refresh(w)
	r.refresh(w)
	if got := r.count("/repo/b"); got != asked {
		t.Errorf("the checkout was snapshotted %d more times while backed off", got-asked)
	}
}

// A checkout whose turn came only after the radar's time had run out did not use it,
// and is not counted.
func TestACheckoutThatStartedAfterTheRadarsTimeRanOutIsNotCounted(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.slow["/repo/b"] = true
	radarDeadline = 60 * time.Millisecond
	t.Cleanup(func() { radarDeadline = 30 * time.Second })
	w := radarWorkspace()
	c := radarCheckout{key: "/repo/a", cwd: "/repo/a", st: dirty("a")}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	for i := 0; i < 5; i++ {
		w.radarLogUnknown(ctx, c, ctx.Err(), 0, "copying it")
	}
	w.gitMu.Lock()
	n := len(w.radar.timeouts)
	w.gitMu.Unlock()
	if n != 0 || r.logged("skipped") != 0 {
		t.Errorf("a checkout that did not run was counted: %d marks, %v", n, r.logs)
	}
	// And a radar that was cancelled is not a deadline.
	cctx, cc := context.WithCancel(context.Background())
	cc()
	for i := 0; i < 5; i++ {
		w.radarLogUnknown(cctx, c, cctx.Err(), time.Minute, "copying it")
	}
	w.gitMu.Lock()
	n = len(w.radar.timeouts)
	w.gitMu.Unlock()
	if n != 0 {
		t.Errorf("a cancelled radar was counted: %d marks", n)
	}
}

// Each back-off is twice the one before, up to the most, and not the same every time.
func TestTheRadarBackOffDoubles(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	oldStart, oldMax := radarBackoffStart, radarBackoffMax
	radarBackoffStart, radarBackoffMax = 40*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { radarBackoffStart, radarBackoffMax = oldStart, oldMax })
	c := radarCheckout{key: "k", cwd: "/repo/b", st: dirty("b")}
	err := fmt.Errorf("git add: gave up after 20s: %w", context.DeadlineExceeded)
	step := func() time.Duration {
		for i := 0; i < radarTimeoutsBeforeBackoff; i++ {
			w.radarLogUnknown(context.Background(), c, err, time.Minute, "copying it")
		}
		w.gitMu.Lock()
		defer w.gitMu.Unlock()
		return w.radar.timeouts["k"].step
	}
	var got []time.Duration
	for i := 0; i < 3; i++ {
		got = append(got, step())
		time.Sleep(110 * time.Millisecond) // out of the back-off
	}
	want := []time.Duration{40 * time.Millisecond, 80 * time.Millisecond, 100 * time.Millisecond}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("back-off %d = %v, want %v (doubling, capped)", i+1, got[i], want[i])
		}
	}
}

// A back-off ends when the checkout's commit or its counts of changed and new files
// change, and when the radar is turned off and on.
func TestARadarBackOffEndsWhenTheCheckoutChangesOrTheRadarIsToggled(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	c := radarCheckout{key: "k", cwd: "/repo/b", st: dirty("b")}
	err := fmt.Errorf("git add: gave up after 20s: %w", context.DeadlineExceeded)
	for i := 0; i < radarTimeoutsBeforeBackoff; i++ {
		w.radarLogUnknown(context.Background(), c, err, time.Minute, "copying it")
	}
	if !w.radarBackedOff(c) {
		t.Fatal("setup: the checkout is not backed off")
	}
	moved := c
	moved.st.Untracked = 3
	if w.radarBackedOff(moved) {
		t.Error("the checkout got new files and is still backed off")
	}
	for i := 0; i < radarTimeoutsBeforeBackoff; i++ {
		w.radarLogUnknown(context.Background(), c, err, time.Minute, "copying it")
	}
	if !w.radarBackedOff(c) {
		t.Fatal("setup: the checkout is not backed off again")
	}
	newHead := c
	newHead.st.Commit = "def5678" + newHead.st.Commit[7:]
	if w.radarBackedOff(newHead) {
		t.Error("the checkout's HEAD moved and it is still backed off")
	}
	for i := 0; i < radarTimeoutsBeforeBackoff; i++ {
		w.radarLogUnknown(context.Background(), c, err, time.Minute, "copying it")
	}
	w.radarOff(func(f func()) { f() })
	if w.radarBackedOff(c) {
		t.Error("the radar was turned off and on and the checkout is still backed off")
	}
}

// A git command that gave up at its own deadline is a failure of the checkout: it
// is logged with what git said, once in a while.
func TestRadarLogsAGitCommandThatTimedOut(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.fail["/repo/b"] = fmt.Errorf("git add: gave up after 20s: %w", context.DeadlineExceeded)
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	if n := r.logged("gave up after 20s"); n != 1 {
		t.Errorf("a command timeout was logged %d times, want once: %v", n, r.logs)
	}
}

// Three timeouts in a row and the checkout is left alone, for longer each time, and
// a snapshot that gets through starts over.
func TestRadarBacksOffACheckoutWhoseGitCommandsKeepTimingOut(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.fail["/repo/b"] = fmt.Errorf("git add: gave up after 20s: %w", context.DeadlineExceeded)
	// An hour, which no slow test can outlast; the back-off is ended by hand below, not by
	// waiting for it, since waiting for a fraction of a second is a race on a slow machine.
	radarBackoffStart = time.Hour
	t.Cleanup(func() { radarBackoffStart = 10 * time.Minute })
	w := radarWorkspace()
	endBackoff := func() {
		w.gitMu.Lock()
		defer w.gitMu.Unlock()
		for _, m := range w.radar.timeouts {
			m.until = time.Now().Add(-time.Second)
		}
	}
	for i := 0; i < 3; i++ {
		r.refresh(w)
	}
	if n := r.logged("skipped for"); n != 1 {
		t.Fatalf("the back-off was logged %d times after three timeouts, want once: %v", n, r.logs)
	}
	asked := r.count("/repo/b")
	for i := 0; i < 3; i++ {
		r.refresh(w)
	}
	if got := r.count("/repo/b"); got != asked {
		t.Errorf("the checkout was snapshotted %d more times while backed off", got-asked)
	}
	endBackoff()
	r.refresh(w)
	if got := r.count("/repo/b"); got != asked+1 {
		t.Errorf("the checkout was snapshotted %d times after the back-off, want one more", got-asked)
	}
	// It gets through: the count starts over.
	delete(r.fail, "/repo/b")
	endBackoff()
	r.refresh(w)
	r.refresh(w)
	w.gitMu.Lock()
	left := len(w.radar.timeouts)
	w.gitMu.Unlock()
	if left != 0 {
		t.Errorf("%d timeout records after a snapshot that worked, want none", left)
	}
}

// A checkout is struck for the radar's time running out only when its own snapshot has
// been running for a good part of it: one that started two seconds before is not slow,
// and one that ran 25 seconds is.
func TestOnlyACheckoutThatRanLongIsStruckForTheRadarsTimeRunningOut(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	c := radarCheckout{key: "k", cwd: "/repo/b", st: dirty("b")}
	expired, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-expired.Done()
	err := errors.New("git add: signal: killed")
	strikes := func() int {
		w.gitMu.Lock()
		defer w.gitMu.Unlock()
		if m := w.radar.timeouts["k"]; m != nil {
			return m.n + int(m.step/time.Minute)
		}
		return 0
	}
	for i := 0; i < 5; i++ {
		w.radarLogUnknown(expired, c, err, 2*time.Second, "copying it")
	}
	if strikes() != 0 {
		t.Errorf("a checkout that had run 2 s was struck %d times", strikes())
	}
	w.radarLogUnknown(expired, c, err, 25*time.Second, "copying it")
	if strikes() != 1 {
		t.Errorf("a checkout that had run 25 s was struck %d times, want once", strikes())
	}
}

// Through the real call site: when two checkouts share one slot and the radar's time
// runs out while the first holds it, the first is struck and the one that only got the
// slot after is not.
func TestACheckoutThatNeverHadASlotBeforeTheDeadlineIsNotStruck(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.slow["/repo/a"] = true
	r.slow["/repo/b"] = true
	radarDeadline = 500 * time.Millisecond
	radarMinRun = 100 * time.Millisecond
	t.Cleanup(func() { radarDeadline = 30 * time.Second; radarMinRun = 15 * time.Second })
	w := radarWorkspace()
	noCommit := func(b string) gitx.Status { st := dirty(b); st.Commit = ""; return st } // no base to key
	todo := []radarCheckout{
		{key: "a", cwd: "/repo/a", st: noCommit("a")},
		{key: "b", cwd: "/repo/b", st: noCommit("b")},
	}
	ctx, cancel := context.WithTimeout(context.Background(), radarDeadline)
	defer cancel()
	slots := make(chan struct{}, 1)
	bs, _ := radarBases(ctx, "/repo/a")
	w.radarSnapshots(ctx, 0, nil, slots, bs, todo)
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	first, second := w.radar.timeouts["a"], w.radar.timeouts["b"]
	// Which of the two got the slot first is not fixed; exactly one of them did, and
	// only that one was running when the time ran out.
	if (first == nil) == (second == nil) {
		t.Errorf("strikes: a=%v b=%v, want exactly the one that held the slot struck: %v", first != nil, second != nil, r.logs)
	}
}

// Strikes are kept when the checkout's files change between refreshes, so that a
// checkout that is slow every time, with files that keep changing, still reaches the
// third and is left alone for longer each round.
func TestStrikesSurviveAChangeInTheCheckoutAndSuccessClearsThem(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	oldStart := radarBackoffStart
	radarBackoffStart = 40 * time.Millisecond
	t.Cleanup(func() { radarBackoffStart = oldStart })
	c := radarCheckout{key: "k", cwd: "/repo/b", st: dirty("b")}
	err := fmt.Errorf("git add: gave up after 20s: %w", context.DeadlineExceeded)
	round := func(base int) {
		for i := 0; i < radarTimeoutsBeforeBackoff; i++ {
			c.st.Untracked = base + i // an agent keeps adding files
			w.radarBackedOff(c)
			w.radarLogUnknown(context.Background(), c, err, time.Minute, "copying it")
		}
	}
	step := func() time.Duration {
		w.gitMu.Lock()
		defer w.gitMu.Unlock()
		return w.radar.timeouts["k"].step
	}
	round(0)
	if step() != 40*time.Millisecond {
		t.Fatalf("after three strikes with changing files the step is %v, want the first back-off", step())
	}
	c.st.Untracked = 100
	if w.radarBackedOff(c) {
		t.Error("a checkout whose files changed is still skipped")
	}
	round(200)
	if step() != 80*time.Millisecond {
		t.Errorf("after a second round the step is %v, want it doubled", step())
	}
	w.radarSnapshotWorked(c)
	w.gitMu.Lock()
	_, there := w.radar.timeouts["k"]
	w.gitMu.Unlock()
	if there {
		t.Error("a snapshot that worked did not clear the strikes")
	}
}

// Strikes are forgotten after a day without one, and a checkout that is no longer live
// is dropped with the rest of what was learned of it.
func TestStrikesExpireAndAreDroppedWithTheCheckout(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	oldExpire := radarStrikesExpire
	radarStrikesExpire = 20 * time.Millisecond
	t.Cleanup(func() { radarStrikesExpire = oldExpire })
	c := radarCheckout{key: "k", cwd: "/repo/b", st: dirty("b")}
	err := fmt.Errorf("git add: gave up after 20s: %w", context.DeadlineExceeded)
	w.radarLogUnknown(context.Background(), c, err, time.Minute, "copying it")
	w.radarLogUnknown(context.Background(), c, err, time.Minute, "copying it")
	time.Sleep(40 * time.Millisecond)
	w.radarLogUnknown(context.Background(), c, err, time.Minute, "copying it")
	w.gitMu.Lock()
	n := w.radar.timeouts["k"].n
	w.gitMu.Unlock()
	if n != 1 {
		t.Errorf("strikes = %d after a long quiet, want the count started again at one", n)
	}
	w.gitMu.Lock()
	w.radar.on = true
	gen := w.radar.gen
	w.gitMu.Unlock()
	w.pruneRadar(gen)
	w.gitMu.Lock()
	left := len(w.radar.timeouts)
	w.gitMu.Unlock()
	if left != 0 {
		t.Errorf("%d strike records for a checkout no pane is in", left)
	}
}

// A checkout whose changed files are too large to copy is unknown, not clean: it
// is looked at again each refresh, so the chip comes back when the file is gone.
func TestRadarTooBigACheckoutIsUnknownNotClean(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.fail["/repo/c"] = fmt.Errorf("%w: dump.bin is 30 MB", gitx.ErrTooBig)
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	r.refresh(w)
	if got := r.count("/repo/c"); got != 3 {
		t.Errorf("the checkout was snapshotted %d times in three refreshes, want every refresh", got)
	}
}
