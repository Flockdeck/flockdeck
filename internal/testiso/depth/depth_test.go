package depth

import (
	"os"
	"strings"
	"testing"
)

// chain is a fake process table: each process id maps to what is known of it.
type chain map[int]Proc

func (c chain) lookup(pid int) (Proc, bool) {
	p, ok := c[pid]
	return p, ok
}

// nested is n copies of the program "x.test" one inside the other under a shell
// and a go command, each started later than its parent, and the process id of
// the innermost.
func nested(n int) (chain, int) {
	c := chain{1: {0, "init", 10}, 2: {1, "go", 20}}
	parent := 2
	for i := 0; i < n; i++ {
		pid := 3 + i
		c[pid] = Proc{parent, "x.test", int64(100 + i)}
		parent = pid
	}
	return c, parent
}

func TestCountIsTheAncestorsRunningTheSameProgram(t *testing.T) {
	for n := 1; n <= 6; n++ {
		c, pid := nested(n)
		if got := Count(c.lookup, pid); got != n-1 {
			t.Errorf("%d nested: counted %d ancestors, want %d", n, got, n-1)
		}
	}
}

func TestTheGuardAllowsTheNestingTheSuitesUseAndStopsDeeperOnes(t *testing.T) {
	// A test binary, the fake program it starts, and what that starts: 3 deep.
	for n, want := range map[int]bool{1: false, 2: false, 3: false, 4: true, 5: true, 40: true} {
		c, pid := nested(n)
		code := 0
		guard(c.lookup, pid, func(c int) { code = c })
		if (code == 2) != want {
			t.Errorf("%d nested: exit code %d, want an exit: %v", n, code, want)
		}
		if TooDeep(c.lookup, pid) != want {
			t.Errorf("%d nested: TooDeep = %v, want %v", n, !want, want)
		}
	}
}

func TestOnlyTheSameProgramCounts(t *testing.T) {
	c := chain{1: {0, "x.test", 1}, 2: {1, "y.test", 2}, 3: {2, "X.TEST", 3}, 4: {3, "z.test", 4}, 5: {4, "x.test", 5}}
	if got := Count(c.lookup, 5); got != 2 {
		t.Errorf("counted %d, want 2 (the same name in another case counts, others do not)", got)
	}
}

func TestAnUnknownProcessOrABrokenChainIsNotDeep(t *testing.T) {
	c := chain{}
	if Count(c.lookup, 9) != 0 {
		t.Error("an unknown process was counted")
	}
	// A parent that is missing from the table ends the walk.
	c = chain{5: {4, "x.test", 5}}
	if Count(c.lookup, 5) != 0 {
		t.Error("a missing parent was counted")
	}
	// A process that is its own parent is not its own ancestor.
	c = chain{5: {5, "x.test", 5}}
	if Count(c.lookup, 5) != 0 {
		t.Error("a process counted as its own ancestor")
	}
	guard(nil, 5, func(int) { t.Error("exited with no way to read the process tree") })
}

// A process id given to a new process after its holder went leaves a table
// whose parent links can run in a circle. Each process is counted once, and the
// walk ends where it meets one it has passed.
func TestALoopMadeByAReusedProcessIdIsCountedOnce(t *testing.T) {
	// 5 was started by 6, which has gone, and 6 is now the process 5 started.
	c := chain{5: {6, "x.test", 50}, 6: {5, "x.test", 60}}
	if got := Count(c.lookup, 6); got != 1 {
		t.Errorf("a two-process loop counted %d ancestors, want 1", got)
	}
	if TooDeep(c.lookup, 6) || TooDeep(c.lookup, 5) {
		t.Error("a loop of two was too deep")
	}
	// A longer loop through a shell.
	c = chain{5: {7, "x.test", 50}, 6: {5, "x.test", 60}, 7: {6, "sh", 40}}
	if got := Count(c.lookup, 6); got != 1 {
		t.Errorf("a loop through a shell counted %d, want 1", got)
	}
	if n := len(Chain(c.lookup, 6)); n != 3 {
		t.Errorf("the chain has %d links, want 3", n)
	}
}

// A loop among the ancestors that does not pass through the process itself.
func TestALoopAboveTheProcessEndsTheWalk(t *testing.T) {
	c := chain{9: {5, "y.test", 0}, 5: {6, "x.test", 0}, 6: {5, "x.test", 0}} // no start times, so only the loop ends the walk
	if n := len(Chain(c.lookup, 9)); n != 3 {
		t.Errorf("the chain has %d links, want 3 (the process and the two in the loop)", n)
	}
}

// A parent that started after its child is not its parent: the number was reused.
func TestAParentThatStartedAfterItsChildIsAReusedNumber(t *testing.T) {
	// 9 is x.test with a recorded parent 8; 8 is now an unrelated x.test started
	// later, whose own parent is another x.test and whose parent is another.
	c := chain{
		9: {8, "x.test", 100},
		8: {7, "x.test", 200},
		7: {6, "x.test", 150},
		6: {5, "x.test", 120},
		5: {1, "go", 10},
		1: {0, "init", 1},
	}
	if got := Count(c.lookup, 9); got != 0 {
		t.Errorf("walked into a younger parent: counted %d, want 0", got)
	}
	// The same table with honest start times is a real chain of four.
	c[8] = Proc{7, "x.test", 90}
	c[7] = Proc{6, "x.test", 80}
	c[6] = Proc{5, "x.test", 70}
	if got := Count(c.lookup, 9); got != 3 {
		t.Errorf("a real chain counted %d, want 3", got)
	}
	if !TooDeep(c.lookup, 9) {
		t.Error("a real chain of four was not too deep")
	}
}

// Where a start time cannot be read (0) the check is skipped, not failed.
func TestAnUnknownStartTimeSkipsTheYoungerParentCheck(t *testing.T) {
	c := chain{
		4: {3, "x.test", 0},
		3: {2, "x.test", 500},
		2: {1, "x.test", 0},
		1: {0, "go", 0},
	}
	if got := Count(c.lookup, 4); got != 2 {
		t.Errorf("counted %d, want 2", got)
	}
}

func TestWhatTheGuardSaysNamesTheWholeChain(t *testing.T) {
	c, pid := nested(5)
	var out strings.Builder
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	guard(c.lookup, pid, func(int) {})
	w.Close()
	os.Stderr = old
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out.Write(buf[:n])
	for _, want := range []string{"pid 7 x.test (parent 6, started 104)", "pid 3 x.test (parent 2, started 100)", "pid 2 go (parent 1, started 20)", "pid 1 init"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the message lacks %q:\n%s", want, out.String())
		}
	}
}

// The real lookup answers for this process with its real parent, and the
// process running this test is not deep.
func TestTheRealLookupKnowsThisProcess(t *testing.T) {
	lookup := osLookup()
	if lookup == nil {
		t.Skip("the process tree cannot be read here")
	}
	p, ok := lookup(os.Getpid())
	if !ok || p.Name == "" {
		t.Fatalf("lookup of this process: %+v %v", p, ok)
	}
	if p.Parent != os.Getppid() {
		t.Errorf("parent = %d, want %d", p.Parent, os.Getppid())
	}
	if p.Start == 0 {
		t.Error("the start time of this process is not known")
	}
	parent, ok := lookup(p.Parent)
	if ok && parent.Start != 0 && parent.Start > p.Start {
		t.Errorf("the parent started (%d) after this process (%d)", parent.Start, p.Start)
	}
	if TooDeep(lookup, os.Getpid()) {
		t.Error("this test binary is counted as too deep")
	}
}
