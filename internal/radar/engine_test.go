package radar

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// fake is an Engine whose merges are answered by the test, and which counts
// them, with a clock the test moves.
type fake struct {
	*Engine
	now      time.Time
	merges   [][2]string
	conflict map[[2]string][]string // by snapshot commits, in either order
	fail     error
}

func newFake() *fake {
	f := &fake{Engine: NewEngine(), now: time.Unix(1000, 0), conflict: map[[2]string][]string{}}
	f.Engine.now = func() time.Time { return f.now }
	f.Engine.predict = func(_ context.Context, _ string, _ *gitx.Scratch, a, b string) ([]string, bool, error) {
		f.merges = append(f.merges, [2]string{a, b})
		if f.fail != nil {
			return nil, false, f.fail
		}
		if p, ok := f.conflict[[2]string{a, b}]; ok {
			return p, false, nil
		}
		if p, ok := f.conflict[[2]string{b, a}]; ok {
			return p, false, nil
		}
		return nil, true, nil
	}
	return f
}

// cycle runs one refresh and lets enough time pass for the next to count.
func (f *fake) cycle(t *testing.T, in ...Input) Report {
	t.Helper()
	end, ok := f.Begin()
	if !ok {
		t.Fatal("Begin was refused")
	}
	r := f.Update(context.Background(), "repo", nil, in)
	end()
	f.now = f.now.Add(15 * time.Second)
	return r
}

func ready(id, tree string, paths ...string) Input {
	return Input{ID: id, Ready: true, Commit: "c-" + tree, Tree: tree, Paths: paths}
}

func TestPairsThatShareNoPathAreNeverMerged(t *testing.T) {
	f := newFake()
	var in []Input
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		in = append(in, ready(id, "t-"+id, id+".go"))
	}
	r := f.cycle(t, in...)
	if len(f.merges) != 0 || r.Merges != 0 {
		t.Errorf("%d merges for twelve panes on disjoint files, want none", len(f.merges))
	}
	if len(r.Conflicts) != 0 {
		t.Errorf("conflicts = %v", r.Conflicts)
	}
}

// Of three panes, two share a path: one merge, not three.
func TestOnlyPairsThatMeetAreMerged(t *testing.T) {
	f := newFake()
	f.cycle(t, ready("a", "ta", "x.go", "only-a.go"), ready("b", "tb", "x.go"), ready("c", "tc", "only-c.go"))
	if len(f.merges) != 1 {
		t.Errorf("merges = %v, want just the pair that shares x.go", f.merges)
	}
}

func TestAConflictIsShownOnlyOnTheSecondRefreshInARow(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")

	if r := f.cycle(t, a, b); len(r.Conflicts) != 0 {
		t.Fatalf("the first refresh showed %v, want nothing until it is seen twice", r.Conflicts)
	}
	r := f.cycle(t, a, b)
	want := []Conflict{{A: "a", B: "b", Paths: []string{"x.go"}}}
	if !reflect.DeepEqual(r.Conflicts, want) {
		t.Fatalf("second refresh = %v, want %v", r.Conflicts, want)
	}
	if got := f.Conflicts(); !reflect.DeepEqual(got, want) {
		t.Errorf("Conflicts() = %v, want %v", got, want)
	}

	// The edit is fixed in b: the pair is clean, and the chip goes at once.
	fixed := ready("b", "tb2", "x.go")
	if r := f.cycle(t, a, fixed); len(r.Conflicts) != 0 {
		t.Errorf("after a clean refresh = %v, want nothing", r.Conflicts)
	}
	// And a conflict seen again starts counting from one.
	if r := f.cycle(t, a, b); len(r.Conflicts) != 0 {
		t.Errorf("back at a conflict for one refresh = %v, want nothing", r.Conflicts)
	}
}

// A refresh asked for a moment after another is not a second sighting.
func TestRefreshesCloseTogetherCountOnce(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.Update(context.Background(), "repo", nil, []Input{a, b})
	f.now = f.now.Add(time.Second)
	r := f.Update(context.Background(), "repo", nil, []Input{a, b})
	if len(r.Conflicts) != 0 {
		t.Errorf("two refreshes a second apart showed %v", r.Conflicts)
	}
}

// Nothing changed between two refreshes: the second asks git nothing.
func TestAnUnchangedPairIsNotMergedAgain(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.cycle(t, a, b)
	r := f.cycle(t, a, b)
	if len(f.merges) != 1 || r.Merges != 0 {
		t.Errorf("merges = %d in total, %d in the second refresh, want 1 and 0", len(f.merges), r.Merges)
	}
	if len(r.Conflicts) != 1 {
		t.Errorf("the cached conflict was not counted: %v", r.Conflicts)
	}
	// Either way round is the same pair of trees.
	f.cycle(t, b, a)
	if len(f.merges) != 1 {
		t.Errorf("merges = %d after the inputs swapped places, want 1", len(f.merges))
	}
	// A pane's tree changing is asked about afresh.
	f.cycle(t, ready("a", "ta2", "x.go"), b)
	if len(f.merges) != 2 {
		t.Errorf("merges = %d after a tree changed, want 2", len(f.merges))
	}
}

func TestACheckoutWithNothingLeftDropsItsPairs(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.cycle(t, a, b)
	if r := f.cycle(t, a, b); len(r.Conflicts) != 1 {
		t.Fatalf("setup: %v", r.Conflicts)
	}
	// b committed and merged, say: nothing against the base any more.
	if r := f.cycle(t, a, Input{ID: "b"}); len(r.Conflicts) != 0 {
		t.Errorf("a pair with a clean pane is still shown: %v", r.Conflicts)
	}
}

// A refresh of the panes on screen leaves the others out, and what was found
// about them stays.
func TestAPairWithAPaneNotRefreshedIsLeftAlone(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.cycle(t, a, b)
	f.cycle(t, a, b)
	r := f.cycle(t, a)
	if len(r.Conflicts) != 1 {
		t.Errorf("conflicts after only a was refreshed = %v, want the pair kept", r.Conflicts)
	}
}

// With b left out of a refresh, a pair cannot be merged again, but it can be
// seen to have stopped overlapping.
func TestAPairWhoseFilesStoppedMeetingIsDroppedWithoutB(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.cycle(t, a, b)
	f.cycle(t, a, b)
	merges := len(f.merges)
	// a reverts its edit to x.go and edits y.go instead.
	r := f.cycle(t, ready("a", "ta2", "y.go"))
	if len(r.Conflicts) != 0 {
		t.Errorf("conflicts = %v, want the pair dropped: a no longer touches x.go", r.Conflicts)
	}
	if len(f.merges) != merges {
		t.Error("a merge was asked for with b's snapshot gone")
	}
}

func TestAMergeThatFailsLeavesThePairAsItWas(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.cycle(t, a, b)
	f.fail = errors.New("git: boom")
	a2 := ready("a", "ta2", "x.go")
	if r := f.cycle(t, a2, b); len(r.Conflicts) != 0 {
		t.Errorf("a failed merge produced %v", r.Conflicts)
	}
	f.fail = nil
	f.conflict[[2]string{"c-ta2", "c-tb"}] = []string{"x.go"}
	r := f.cycle(t, a2, b)
	if len(r.Conflicts) != 1 {
		t.Errorf("after git answered again = %v, want the pair on its second sighting", r.Conflicts)
	}
}

func TestARefreshOutOfTimeSaysItIsBehind(t *testing.T) {
	f := newFake()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := f.Update(ctx, "repo", nil, []Input{ready("a", "ta", "x.go"), ready("b", "tb", "x.go")})
	if !r.Behind || len(f.merges) != 0 {
		t.Errorf("behind = %v, merges = %d, want a refresh that gave up before merging", r.Behind, len(f.merges))
	}
}

func TestOnlyOneRefreshRunsAtATime(t *testing.T) {
	f := newFake()
	started, release := make(chan struct{}), make(chan struct{})
	f.Engine.predict = func(context.Context, string, *gitx.Scratch, string, string) ([]string, bool, error) {
		close(started)
		<-release
		return nil, true, nil
	}
	done := make(chan struct{})
	go func() {
		end, _ := f.Begin()
		f.Update(context.Background(), "repo", nil, []Input{ready("a", "ta", "x.go"), ready("b", "tb", "x.go")})
		end()
		close(done)
	}()
	<-started
	if _, ok := f.Begin(); ok {
		t.Error("a second refresh began beside the first")
	}
	// What was already known can still be read while it runs.
	if got := f.Conflicts(); len(got) != 0 {
		t.Errorf("Conflicts() = %v", got)
	}
	close(release)
	<-done
}

func TestParseConflicts(t *testing.T) {
	tree := "0123456789012345678901234567890123456789"
	got := parseConflicts(tree + "\x00src/a.go\x00b file.txt\x00src/a.go\x00")
	if want := []string{"src/a.go", "b file.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("parseConflicts = %q, want %q", got, want)
	}
	if got := parseConflicts(tree + "\x00"); len(got) != 0 {
		t.Errorf("parseConflicts of a tree alone = %q", got)
	}
}

// A snapshot that failed or timed out says nothing about the pair: it keeps the
// refreshes it has conflicted in, and the next one that reads is the second.
func TestAnUnknownCheckoutKeepsItsPairsAndTheirCount(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.cycle(t, a, b)
	// The snapshot of b fails.
	if r := f.cycle(t, a, Input{ID: "b", Unknown: true}); len(r.Conflicts) != 0 {
		t.Fatalf("an unknown checkout produced %v", r.Conflicts)
	}
	// It reads again: this is the second sighting, not a first.
	r := f.cycle(t, a, b)
	if len(r.Conflicts) != 1 {
		t.Errorf("after the failed read, conflicts = %v, want the pair on its second sighting", r.Conflicts)
	}
	// And once shown, a failed read does not take it down.
	if r := f.cycle(t, a, Input{ID: "b", Unknown: true}); len(r.Conflicts) != 1 {
		t.Errorf("a failed read took the chip down: %v", r.Conflicts)
	}
}

// A file and a directory of one name conflict, though no path is in both sets.
func TestAFileAndADirectoryOfTheSameNameMeet(t *testing.T) {
	for _, tc := range []struct {
		a, b []string
		want bool
	}{
		{[]string{"foo"}, []string{"foo/bar"}, true},
		{[]string{"foo/bar/baz.go"}, []string{"foo"}, true},
		{[]string{"foo"}, []string{"foobar/x"}, false},
		{[]string{"foo"}, []string{"other/foo"}, false},
		{[]string{"a/b.go"}, []string{"a/b.go"}, true},
		{[]string{"a/b.go"}, []string{"a/c.go"}, false},
		{nil, []string{"a"}, false},
	} {
		if got := meet(tc.a, tc.b); got != tc.want {
			t.Errorf("meet(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	f := newFake()
	f.cycle(t, ready("a", "ta", "foo"), ready("b", "tb", "foo/bar"))
	if len(f.merges) != 1 {
		t.Errorf("merges = %d, want the file and the directory merged", len(f.merges))
	}
}

// merge-tree names the file in such a conflict as foo~<commit>.
func TestParseConflictsTakesOffTheCommitSuffix(t *testing.T) {
	tree := "0123456789012345678901234567890123456789"
	a, b := "aaaa", "bbbb"
	got := parseConflicts(tree+"\x00foo~"+a+"\x00dir/x~"+b+"\x00plain.go\x00foo\x00", a, b)
	if want := []string{"foo", "dir/x", "plain.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("parseConflicts = %q, want %q", got, want)
	}
}

// The same two trees on other commits have another merge base, so they are
// another merge.
func TestTheCacheKnowsTheCommitsATreeStandsOn(t *testing.T) {
	f := newFake()
	a := ready("a", "ta", "x.go")
	a.Head = "h1"
	b := ready("b", "tb", "x.go")
	b.Head = "h9"
	f.cycle(t, a, b)
	f.cycle(t, a, b)
	if len(f.merges) != 1 {
		t.Fatalf("merges = %d for an unchanged pair, want 1", len(f.merges))
	}
	a.Head = "h2"
	f.cycle(t, a, b)
	if len(f.merges) != 2 {
		t.Errorf("merges = %d after a moved to another commit, want 2", len(f.merges))
	}
}

// What belonged to a closed pane is let go, and an engine with nothing left says so.
func TestRetainForgetsClosedCheckouts(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.cycle(t, a, b)
	f.cycle(t, a, b)
	f.Retain(map[string]bool{"a": true})
	if len(f.Conflicts()) != 0 || f.Tracking() {
		t.Errorf("the pair outlived b: %v", f.Conflicts())
	}
	if f.Empty() {
		t.Error("the engine is empty while a is still held")
	}
	f.Retain(map[string]bool{})
	if !f.Empty() {
		t.Error("the engine holds something after every checkout went")
	}
}

// A path added under a directory the other pane moved a file out of meets it,
// though no path is in both. It also brings together a pane that touches another
// file of that directory, which costs a merge and nothing else.
func TestAPathUnderAMovedAwayDirectoryMeets(t *testing.T) {
	a := Input{ID: "a", Ready: true, Paths: []string{"d/x.txt", "e/x.txt"}, Dirs: []string{"d"}}
	b := Input{ID: "b", Ready: true, Paths: []string{"d/new.txt"}}
	if !meetInputs(a, b) || !meetInputs(b, a) {
		t.Error("a file added to a directory the other moved out of does not meet")
	}
	f := newFake()
	a.Tree, a.Commit, b.Tree, b.Commit = "ta", "c-ta", "tb", "c-tb"
	f.cycle(t, a, b)
	if len(f.merges) != 1 {
		t.Errorf("merges = %d, want the pair brought together by the moved directory", len(f.merges))
	}
}

func TestForgetDropsACheckoutAndItsPairs(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.cycle(t, a, b)
	f.cycle(t, a, b)
	f.Forget("b")
	if len(f.Conflicts()) != 0 || f.Tracking() {
		t.Errorf("the pair outlived b: %v", f.Conflicts())
	}
}

// An Update in progress must not be able to put back what Forget drops: Forget
// takes the run lock, and says false when it cannot.
func TestForgetWaitsForNoUpdateAndSaysSoWhenBusy(t *testing.T) {
	f := newFake()
	f.conflict[[2]string{"c-ta", "c-tb"}] = []string{"x.go"}
	a, b := ready("a", "ta", "x.go"), ready("b", "tb", "x.go")
	f.cycle(t, a, b)
	f.cycle(t, a, b)
	end, ok := f.Begin()
	if !ok {
		t.Fatal("setup: the engine was busy")
	}
	if f.Forget("b") {
		t.Error("Forget went ahead while an Update was running")
	}
	if len(f.Conflicts()) != 1 {
		t.Error("the pair was dropped by a Forget that said it did nothing")
	}
	end()
	if !f.Forget("b") || len(f.Conflicts()) != 0 {
		t.Error("Forget did not drop the pair once the engine was free")
	}
}

// Sixty panes on one file are 1,770 pairs. A refresh runs at most 50 of the merges,
// says it is behind, and the next goes on from the cache until all are done.
func TestAtMostFiftyMergesARefreshAndTheRestFollow(t *testing.T) {
	f := newFake()
	var in []Input
	for i := 0; i < 60; i++ {
		in = append(in, ready(fmt.Sprintf("p%02d", i), fmt.Sprintf("t%02d", i), "shared.go"))
	}
	first := f.cycle(t, in...)
	if first.Merges != 50 || !first.Behind {
		t.Errorf("first refresh: %d merges, behind %v, want 50 and behind", first.Merges, first.Behind)
	}
	total := first.Merges
	refreshes := 1
	for ; refreshes < 100; refreshes++ {
		r := f.cycle(t, in...)
		if r.Merges > 50 {
			t.Fatalf("refresh %d ran %d merges", refreshes+1, r.Merges)
		}
		total += r.Merges
		if !r.Behind {
			break
		}
	}
	if total != 60*59/2 {
		t.Errorf("%d merges in all after %d refreshes, want each of the 1770 pairs once", total, refreshes+1)
	}
	if again := f.cycle(t, in...); again.Merges != 0 || again.Behind {
		t.Errorf("once all are cached a refresh did %d merges (behind %v)", again.Merges, again.Behind)
	}
}

// A full cache drops the merge asked for least recently, not everything.
func TestAFullCacheDropsTheLeastRecentlyUsedNotEverything(t *testing.T) {
	e := NewEngineWith(Options{MaxCached: 4})
	key := func(i int) treePair { return treePair{fmt.Sprint("a", i), fmt.Sprint("b", i)} }
	for i := 1; i <= 4; i++ {
		e.remember(key(i), verdict{conflict: true})
	}
	if _, ok := e.cached(key(1)); !ok { // 1 is the newest asked for now; 2 is the oldest
		t.Fatal("an entry in the cache was not found")
	}
	e.remember(key(5), verdict{})
	for i, want := range map[int]bool{1: true, 2: false, 3: true, 4: true, 5: true} {
		if _, ok := e.cached(key(i)); ok != want {
			t.Errorf("entry %d cached = %v, want %v", i, ok, want)
		}
	}
	if len(e.cache) != 4 || e.order.Len() != 4 {
		t.Errorf("%d entries (%d in order), want 4", len(e.cache), e.order.Len())
	}
}

// With more uncached pairs than one refresh may merge, and a cache too small to
// remember them, a walk that always began at the same pair would never reach the
// last ones. The walk starts where the last stopped, so over a few rounds every
// pair is merged.
func TestEveryPairIsMergedEventuallyWhenTheCacheCannotHoldThem(t *testing.T) {
	seen := map[[2]string]bool{}
	e := NewEngineWith(Options{
		MaxCached: 2,
		Predict: func(_ context.Context, _ string, _ *gitx.Scratch, a, b string) ([]string, bool, error) {
			if a > b {
				a, b = b, a
			}
			seen[[2]string{a, b}] = true
			return nil, true, nil
		},
	})
	var in []Input
	for i := 0; i < 20; i++ { // 190 pairs, four rounds of 50
		in = append(in, ready(fmt.Sprintf("p%02d", i), fmt.Sprintf("t%02d", i), "shared.go"))
	}
	for round := 0; round < 8 && len(seen) < 190; round++ {
		end, ok := e.Begin()
		if !ok {
			t.Fatal("Begin was refused")
		}
		r := e.Update(context.Background(), "repo", nil, in)
		end()
		if r.Merges > maxMergesPerRefresh {
			t.Fatalf("round %d ran %d merges", round, r.Merges)
		}
	}
	if len(seen) != 190 {
		t.Errorf("%d of 190 pairs were merged over the rounds", len(seen))
	}
}

func TestPairAtWalksEveryPairOnce(t *testing.T) {
	const n = 7
	got := map[[2]int]bool{}
	for k := 0; k < n*(n-1)/2; k++ {
		i, j := pairAt(n, k)
		if i >= j || j >= n || got[[2]int{i, j}] {
			t.Fatalf("pairAt(%d, %d) = %d, %d", n, k, i, j)
		}
		got[[2]int{i, j}] = true
	}
}

// A refresh that is behind says so in the log, and says it once a minute and not
// on every refresh.
func TestBehindIsLoggedAtMostOnceAMinute(t *testing.T) {
	var lines int
	old := logf
	logf = func(string, ...any) { lines++ }
	t.Cleanup(func() { logf = old })

	f := newFake() // each cycle moves its clock on 15 seconds
	var in []Input
	for i := 0; i < 20; i++ {
		in = append(in, ready(fmt.Sprintf("p%02d", i), fmt.Sprintf("t%02d", i), "shared.go"))
	}
	f.Engine.maxCached = 2
	for i := 0; i < 8; i++ { // two minutes
		if r := f.cycle(t, in...); !r.Behind {
			t.Fatalf("refresh %d was not behind", i+1)
		}
	}
	if lines != 2 {
		t.Errorf("%d log lines over two minutes of refreshes that were behind, want 2", lines)
	}
}

// A pair whose merge keeps failing for a reason other than the deadline does not
// count toward the merge limit, so it must not be where the next refresh starts
// either: the pairs past it would never be reached.
func TestAPairThatAlwaysFailsDoesNotPinTheWalk(t *testing.T) {
	seen := map[[2]string]bool{}
	e := NewEngineWith(Options{
		MaxCached: 2,
		Predict: func(_ context.Context, _ string, _ *gitx.Scratch, a, b string) ([]string, bool, error) {
			if a > b {
				a, b = b, a
			}
			if a == "c-t00" && b == "c-t01" {
				return nil, false, errors.New("merge failed")
			}
			seen[[2]string{a, b}] = true
			return nil, true, nil
		},
	})
	var in []Input
	for i := 0; i < 20; i++ {
		in = append(in, ready(fmt.Sprintf("p%02d", i), fmt.Sprintf("t%02d", i), "shared.go"))
	}
	for round := 0; round < 20; round++ {
		end, ok := e.Begin()
		if !ok {
			t.Fatal("Begin was refused")
		}
		e.Update(context.Background(), "repo", nil, in)
		end()
	}
	if len(seen) != 189 {
		t.Errorf("%d of 189 pairs were merged around the failing one", len(seen))
	}
}
