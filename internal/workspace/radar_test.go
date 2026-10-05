package workspace

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/radar"
)

// radarRig stands in for git under the radar: the status of each checkout, the
// snapshot of each, and which snapshots conflict, with a clock the test moves.
// Nothing here starts a git process, and nothing reads the real configuration:
// whether the radar is on is the rig's to say, not the preferences'.
type radarRig struct {
	t           *testing.T
	mu          sync.Mutex
	on          bool
	status      map[string]gitx.Status
	snapshots   []string            // the checkouts snapshotted, in order
	paths       map[string][]string // changed paths by checkout; none means nothing to compare
	conflicts   map[[2]string][]string
	merges      int
	now         time.Time
	fail        map[string]error // a snapshot that fails, by checkout
	boom        map[string]bool  // a snapshot that panics, by checkout
	slow        map[string]bool  // a snapshot that never comes back before the radar's deadline
	baseCalls   int
	base        string
	fresh       bool // the candidate bases have not moved
	freshCalls  int
	chooseCalls int
	chooseErr   map[string]error // a base that cannot be chosen for a checkout, by checkout
	lazy        map[string]bool  // repositories that are partial clones on an old git, by checkout
	lazyErr     map[string]error // checkouts whose partial-clone check fails
	missing     map[string]bool  // checkouts whose folder is gone
	statusErr   map[string]error // checkouts git cannot read
	extraBase   string           // a second candidate base, when set
	unrelated   map[string]bool  // checkouts whose snapshot says they share no history with the base
	baseErr     error
	logs        []string
}

func newRadarRig(t *testing.T) *radarRig {
	t.Helper()
	if !gitx.Available() {
		t.Skip("git is not installed, so nothing is refreshed")
	}
	r := &radarRig{
		t:         t,
		on:        true,
		status:    map[string]gitx.Status{},
		paths:     map[string][]string{},
		conflicts: map[[2]string][]string{},
		now:       time.Unix(5000, 0),
		fresh:     true,
		chooseErr: map[string]error{},
		lazy:      map[string]bool{},
		lazyErr:   map[string]error{},
		missing:   map[string]bool{},
		statusErr: map[string]error{},
		base:      "base",
		fail:      map[string]error{},
		boom:      map[string]bool{},
		slow:      map[string]bool{},
		unrelated: map[string]bool{},
	}
	oldStatus, oldEnabled, oldCommon, oldBase, oldSnap, oldEngine, oldLogf, oldRoot, oldSweep := gitStatus, radarEnabled, radarCommon, radarBases, radarSnapshot, radarEngine, radarLogf, radarRoot, radarSweep
	oldFresh, oldChoose, oldLazy, oldMissing := radarFresh, radarChoose, radarLazy, radarDirMissing
	t.Cleanup(func() {
		gitStatus, radarEnabled, radarCommon, radarBases, radarSnapshot, radarEngine, radarLogf, radarRoot, radarSweep = oldStatus, oldEnabled, oldCommon, oldBase, oldSnap, oldEngine, oldLogf, oldRoot, oldSweep
		radarFresh, radarChoose, radarLazy, radarDirMissing = oldFresh, oldChoose, oldLazy, oldMissing
	})
	// The sweep lists the real temporary folder, which a test has no business with.
	radarSweep = func() {}
	radarLogf = func(format string, args ...any) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.logs = append(r.logs, fmt.Sprintf(format, args...))
	}
	gitStatus = func(dir string, _ time.Duration) (gitx.Status, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := r.statusErr[dir]; err != nil {
			return gitx.Status{}, err
		}
		return r.status[dir], nil
	}
	radarEnabled = func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.on }
	// Every checkout under /repo is one repository; /other is another.
	radarCommon = func(dir string) (string, error) {
		if strings.HasPrefix(dir, "/other") {
			return "/other/.git", nil
		}
		return "/repo/.git", nil
	}
	radarBases = func(context.Context, string) (*gitx.Bases, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.baseCalls++
		if r.baseErr != nil {
			return nil, r.baseErr
		}
		if r.extraBase != "" {
			return gitx.NewBases(r.base, r.extraBase), nil
		}
		return gitx.NewBases(r.base), nil
	}
	radarFresh = func(context.Context, *gitx.Bases) (bool, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.freshCalls++
		return r.fresh, nil
	}
	radarChoose = func(_ context.Context, dir, _ string, bs *gitx.Bases) (string, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.chooseCalls++
		if err := r.chooseErr[dir]; err != nil {
			return "", err
		}
		return bs.List[0].ID, nil
	}
	radarLazy = func(_ context.Context, dir string) (bool, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := r.lazyErr[dir]; err != nil {
			return true, err
		}
		return r.lazy[dir] || r.lazy["*"], nil
	}
	radarDirMissing = func(dir string) bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.missing[dir]
	}
	radarRoot = func(dir string) (string, error) { return dir, nil }
	radarSnapshot = func(ctx context.Context, dir string, _ *gitx.Scratch, _ string, _ bool) (gitx.Snap, error) {
		r.mu.Lock()
		slow := r.slow[dir]
		r.mu.Unlock()
		if slow {
			<-ctx.Done() // a snapshot that runs past the radar's own deadline
			// What gitx returns then does not say it was the deadline: a git killed by a
			// signal, say. The radar knows from its own context.
			return gitx.Snap{}, errors.New("git add: signal: killed")
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		r.snapshots = append(r.snapshots, dir)
		if r.boom[dir] {
			panic("a snapshot went wrong")
		}
		if err := r.fail[dir]; err != nil {
			return gitx.Snap{}, err
		}
		if len(r.paths[dir]) == 0 {
			return gitx.Snap{Empty: true, Unrelated: r.unrelated[dir]}, nil
		}
		return gitx.Snap{Commit: "c-" + dir, Tree: "t-" + dir, Head: "h-" + dir, Paths: r.paths[dir]}, nil
	}
	radarEngine = func() *radar.Engine {
		return radar.NewEngineWith(radar.Options{
			MinGap: time.Second,
			Now:    func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
			Predict: func(_ context.Context, _ string, _ *gitx.Scratch, a, b string) ([]string, bool, error) {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.merges++
				if p, ok := r.conflicts[[2]string{a, b}]; ok {
					return p, false, nil
				}
				if p, ok := r.conflicts[[2]string{b, a}]; ok {
					return p, false, nil
				}
				return nil, true, nil
			},
		})
	}
	return r
}

// refresh runs one refresh and moves the clock on past the engine's gap.
func (r *radarRig) refresh(w *Workspace) {
	r.t.Cleanup(w.waitGitIdle) // after the rig's restores, so it runs before them
	w.RefreshGit(func(apply func()) { apply() })
	r.mu.Lock()
	r.now = r.now.Add(15 * time.Second)
	r.mu.Unlock()
}

func radarWorkspace() *Workspace {
	return &Workspace{panes: map[string]*Pane{
		"pa":  {ID: "pa", Cwd: "/repo/a", Name: "a"},
		"pa2": {ID: "pa2", Cwd: "/repo/a", Name: "a again"},
		"pb":  {ID: "pb", Cwd: "/repo/b", Name: "b"},
		"pc":  {ID: "pc", Cwd: "/repo/c", Name: "c"},
		"pd":  {ID: "pd", Cwd: "/repo/d", Name: "d"},
		"px":  {ID: "px", Cwd: "/other/x", Name: "x"},
	}, BroadcastSet: map[string]bool{}}
}

func dirty(branch string) gitx.Status {
	return gitx.Status{Branch: branch, Head: "abc1234", Commit: "abc1234" + branch + "00000000000000000000000000000", Dirty: 1}
}

func (r *radarRig) setUp() {
	r.status["/repo/a"] = dirty("a")
	r.status["/repo/b"] = dirty("b")
	r.status["/repo/c"] = gitx.Status{Branch: "c", Head: "abc1234", Commit: "abc1234000000000000000000000000000000aaa"}
	r.status["/repo/d"] = gitx.Status{Detached: true, Head: "abc1234", Dirty: 1}
	r.status["/other/x"] = dirty("x")
	r.paths["/repo/a"] = []string{"shared.go"}
	r.paths["/repo/b"] = []string{"shared.go", "b.go"}
	r.paths["/other/x"] = []string{"shared.go"}
	r.conflicts[[2]string{"c-/repo/a", "c-/repo/b"}] = []string{"shared.go"}
}

func conflictsOf(w *Workspace, id string) []PaneConflict {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.panes[id].Conflicts
}

func TestRadarIsOffByDefault(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	r.on = false
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	if len(r.snapshots) != 0 || r.merges != 0 {
		t.Errorf("with the radar off, %d snapshots and %d merges were made", len(r.snapshots), r.merges)
	}
	for id, p := range w.panes {
		if len(p.Conflicts) != 0 {
			t.Errorf("pane %s has conflicts %v with the radar off", id, p.Conflicts)
		}
	}
}

func TestRadarShowsAPairOnTheSecondRefresh(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()

	r.refresh(w)
	if got := conflictsOf(w, "pa"); len(got) != 0 {
		t.Fatalf("the first refresh showed %v, want nothing until the pair is seen twice", got)
	}
	r.refresh(w)

	want := []PaneConflict{{With: "pb", Paths: []string{"shared.go"}}}
	if got := conflictsOf(w, "pa"); !reflect.DeepEqual(got, want) {
		t.Errorf("pane a: %v, want %v", got, want)
	}
	// Two panes share a's checkout, so both are told, and b names both.
	if got := conflictsOf(w, "pa2"); !reflect.DeepEqual(got, want) {
		t.Errorf("a pane in the same checkout: %v, want %v", got, want)
	}
	wantB := []PaneConflict{{With: "pa", Paths: []string{"shared.go"}}, {With: "pa2", Paths: []string{"shared.go"}}}
	if got := conflictsOf(w, "pb"); !reflect.DeepEqual(got, wantB) {
		t.Errorf("pane b: %v, want %v", got, wantB)
	}
	for _, id := range []string{"pc", "pd", "px"} {
		if got := conflictsOf(w, id); len(got) != 0 {
			t.Errorf("pane %s: %v, want none", id, got)
		}
	}
}

// Only checkouts that can be snapshotted are, and only ones in the same
// repository are paired: the other repository's checkout shares a path with
// both and is never merged with either.
func TestRadarSnapshotsOnlyWhatIsWorthIt(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)

	snapped := map[string]bool{}
	for _, d := range r.snapshots {
		snapped[d] = true
	}
	if snapped["/repo/d"] {
		t.Error("a detached checkout was snapshotted")
	}
	if !snapped["/repo/a"] || !snapped["/repo/b"] || !snapped["/repo/c"] {
		t.Errorf("snapshots = %v, want a, b and c", r.snapshots)
	}
	if r.merges != 1 {
		t.Errorf("%d merges, want the one pair in /repo that shares a path", r.merges)
	}
}

func TestRadarSkipsDetachedRebasingUnbornAndHugeCheckouts(t *testing.T) {
	for name, st := range map[string]gitx.Status{
		"detached":  {Detached: true, Head: "abc1234", Dirty: 1},
		"rebasing":  {Branch: "a", Operation: "rebasing", Dirty: 1},
		"unborn":    {Branch: "a", Unborn: true, Untracked: 1},
		"unnamed":   {Dirty: 1},
		"too large": {Branch: "a", Untracked: gitx.MaxSnapshotFiles + 1},
	} {
		if radarWorthy(st) {
			t.Errorf("%s checkout is worth snapshotting", name)
		}
	}
	if !radarWorthy(gitx.Status{Branch: "a", Dirty: 2, Untracked: 3}) {
		t.Error("an ordinary dirty checkout is not worth snapshotting")
	}
	if !radarWorthy(gitx.Status{Branch: "a"}) {
		// Clean, but it may have commits ahead of the base, which only the
		// snapshot can tell.
		t.Error("a clean checkout is skipped before the snapshot can see whether it is ahead")
	}
}

func TestRadarChipGoesWhenAPaneHasNothingLeft(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	if len(conflictsOf(w, "pb")) == 0 {
		t.Fatal("setup: no chip")
	}
	// b is merged away: nothing against the base any more.
	delete(r.paths, "/repo/b")
	r.refresh(w)
	for _, id := range []string{"pa", "pa2", "pb"} {
		if got := conflictsOf(w, id); len(got) != 0 {
			t.Errorf("pane %s still has %v", id, got)
		}
	}
}

func TestRadarChipsComeDownWhenItIsTurnedOff(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	if len(conflictsOf(w, "pa")) == 0 {
		t.Fatal("setup: no chip")
	}
	r.mu.Lock()
	r.on = false
	r.mu.Unlock()
	before := len(r.snapshots)
	r.refresh(w)
	for id, p := range w.panes {
		if len(p.Conflicts) != 0 {
			t.Errorf("pane %s still has %v after the radar was turned off", id, p.Conflicts)
		}
	}
	if len(r.snapshots) != before {
		t.Error("a snapshot was made with the radar off")
	}
}

// A refresh of one pane's checkout leaves the pair it is in with a checkout that
// was not refreshed.
func TestRadarKeepsAPairWhoseOtherPaneWasNotRefreshed(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)

	t.Cleanup(w.waitGitIdle) // after the restores above, so it runs before them
	w.RefreshGitOf(func(apply func()) { apply() }, []string{"pa"})
	if got := conflictsOf(w, "pa"); len(got) != 1 || got[0].With != "pb" {
		t.Errorf("pane a after refreshing only a: %v, want the pair with b kept", got)
	}
}

// The gate as shipped reads the preferences, which TestMain points at an empty
// configuration directory, so it is what a person who never touched the setting
// has.
func TestRadarIsOffForAnUntouchedSetting(t *testing.T) {
	if radarEnabled() {
		t.Error("the conflict radar is on for a setting nobody changed")
	}
}

func (r *radarRig) setOn(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.on = on
}

func (r *radarRig) snapshotCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.snapshots)
}

// A snapshot that fails, or runs out of time, says nothing about the pair: the
// chip stays where it is, and one not yet shown keeps the sighting it had.
func TestRadarKeepsAPairWhoseSnapshotFailed(t *testing.T) {
	for name, failure := range map[string]error{
		"a failure": errors.New("git: add failed"),
		"a timeout": context.DeadlineExceeded,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRadarRig(t)
			r.setUp()
			w := radarWorkspace()
			r.refresh(w)
			r.refresh(w)
			if len(conflictsOf(w, "pa")) == 0 {
				t.Fatal("setup: no chip")
			}
			r.fail["/repo/b"] = failure
			r.refresh(w)
			r.refresh(w)
			if got := conflictsOf(w, "pa"); len(got) != 1 {
				t.Errorf("pane a after b could not be read: %v, want the chip kept", got)
			}
			// Mended, the pair is read again and is still there.
			delete(r.fail, "/repo/b")
			r.refresh(w)
			if got := conflictsOf(w, "pa"); len(got) != 1 {
				t.Errorf("pane a after b was read again: %v", got)
			}
		})
	}
}

func TestRadarFailureBeforeTheSecondSightingKeepsTheFirst(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w) // first sighting
	r.fail["/repo/b"] = errors.New("timed out")
	r.refresh(w)
	delete(r.fail, "/repo/b")
	r.refresh(w) // the second sighting, whatever came between
	if got := conflictsOf(w, "pa"); len(got) != 1 {
		t.Errorf("pane a = %v, want the chip on the second good read", got)
	}
}

// A snapshot that panics is one unknown checkout, not a dead window.
func TestRadarSurvivesASnapshotThatPanics(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	r.boom["/repo/b"] = true
	r.refresh(w)
	if got := conflictsOf(w, "pa"); len(got) != 1 {
		t.Errorf("pane a after b's snapshot panicked: %v, want the chip kept", got)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.logs) == 0 || !strings.Contains(r.logs[len(r.logs)-1], "panicked") {
		t.Errorf("the panic was not logged: %v", r.logs)
	}
}

// A refresh that finds another still working in a repository does none of the
// work: no scratch directory, no snapshots, no merges.
func TestRadarOverlappingRefreshDoesNothing(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	w.gitMu.Lock()
	engine := w.radar.engines["/repo/.git"]
	w.gitMu.Unlock()
	if engine == nil {
		t.Fatal("setup: no engine for the repository")
	}
	end, ok := engine.Begin()
	if !ok {
		t.Fatal("setup: the engine was busy")
	}
	before, merges := r.snapshotCount(), r.merges
	r.refresh(w)
	if got := r.snapshotCount(); got != before || r.merges != merges {
		t.Errorf("an overlapping refresh made %d snapshots and %d merges", got-before, r.merges-merges)
	}
	end()
	r.refresh(w)
	if r.snapshotCount() == before {
		t.Error("the refresh after the first finished made no snapshots")
	}
}

// A refresh in flight when the radar is turned off must not put the chips back
// after the next refresh has taken them down, whichever order their results
// reach the workspace in.
func TestRadarTurnedOffLeavesNoChipsWhateverTheOrder(t *testing.T) {
	for _, lateFirst := range []bool{false, true} {
		r := newRadarRig(t)
		r.setUp()
		w := radarWorkspace()
		r.refresh(w)
		r.refresh(w)
		if len(conflictsOf(w, "pa")) == 0 {
			t.Fatal("setup: no chip")
		}

		var mu sync.Mutex
		hold := func(into *[]func()) func(func()) {
			return func(f func()) { mu.Lock(); *into = append(*into, f); mu.Unlock() }
		}
		var late, off []func()
		// A refresh begins with the radar on, and its results are held back...
		t.Cleanup(w.waitGitIdle) // after the restores above, so it runs before them
		w.RefreshGit(hold(&late))
		// ...the radar is turned off, and the refresh after it takes the chips down.
		r.setOn(false)
		w.RefreshGit(hold(&off))

		run := func(fs []func()) {
			for _, f := range fs {
				f()
			}
		}
		if lateFirst {
			run(late)
			run(off)
		} else {
			run(off)
			run(late)
		}
		for id, p := range w.panes {
			if len(p.Conflicts) != 0 {
				t.Errorf("lateFirst=%v: pane %s has %v after the radar was turned off", lateFirst, id, p.Conflicts)
			}
		}
	}
}

// A checkout found to have nothing against the base is not snapshotted again
// until it moves, and the base is read once for a minute, not once per refresh.
func TestRadarDoesNotAskAboutACleanCheckoutOrTheBaseAgain(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	count := func(dir string) int {
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
	r.refresh(w)
	if count("/repo/c") != 1 {
		t.Fatalf("setup: the clean checkout was snapshotted %d times", count("/repo/c"))
	}
	r.refresh(w)
	r.refresh(w)
	if got := count("/repo/c"); got != 1 {
		t.Errorf("the clean checkout was snapshotted %d times in three refreshes, want once", got)
	}
	if count("/repo/a") != 3 {
		t.Errorf("a dirty checkout was snapshotted %d times, want every refresh", count("/repo/a"))
	}
	if r.baseCalls != 1 {
		t.Errorf("the base was read %d times in three refreshes, want once", r.baseCalls)
	}
	// It moves on, and is looked at again.
	r.mu.Lock()
	r.status["/repo/c"] = gitx.Status{Branch: "c", Head: "def5678"}
	r.mu.Unlock()
	r.refresh(w)
	if got := count("/repo/c"); got != 2 {
		t.Errorf("a clean checkout on a new commit was snapshotted %d times, want again", got)
	}
}

// The engine, the pairs and what was learned of a checkout go when its panes do.
func TestRadarLetsGoOfClosedPanes(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	w.mu.Lock()
	for _, id := range []string{"pa", "pa2", "pb", "pc", "pd"} {
		delete(w.panes, id)
	}
	w.mu.Unlock()
	r.refresh(w)
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if _, ok := w.radar.engines["/repo/.git"]; ok {
		t.Error("the engine of a repository with no panes is still held")
	}
	if _, ok := w.radar.repoOf[pathKey("/repo/a")]; ok {
		t.Error("what was learned of a closed checkout is still held")
	}
}

// The same, with the radar turned on again before the late refresh lands: the
// repository is known again by then, and only the generation says the late
// result belongs to the radar as it was.
func TestRadarLateResultFromBeforeAnOffAndOnLeavesNoChips(t *testing.T) {
	r := newRadarRig(t)
	r.setUp()
	w := radarWorkspace()
	r.refresh(w)
	r.refresh(w)
	if len(conflictsOf(w, "pa")) == 0 {
		t.Fatal("setup: no chip")
	}
	var mu sync.Mutex
	var late []func()
	t.Cleanup(w.waitGitIdle) // after the restores above, so it runs before them
	w.RefreshGit(func(f func()) { mu.Lock(); late = append(late, f); mu.Unlock() })
	r.setOn(false)
	r.refresh(w) // takes the chips down
	r.setOn(true)
	r.refresh(w) // a new run of the radar: its first sighting shows nothing
	for _, f := range late {
		f()
	}
	for id, p := range w.panes {
		if len(p.Conflicts) != 0 {
			t.Errorf("pane %s has %v: a result from before the radar was turned off was applied", id, p.Conflicts)
		}
	}
}
