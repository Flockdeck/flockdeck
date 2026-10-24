package depth

import (
	"os"
	"testing"
)

type proc struct {
	parent int
	name   string
}

// chain is a fake process table: each process id maps to its parent and program.
type chain map[int]proc

func (c chain) lookup(pid int) (int, string, bool) {
	e, ok := c[pid]
	return e.parent, e.name, ok
}

// nested is n copies of the program "x.test" one inside the other under a shell
// and a go command, and the process id of the innermost.
func nested(n int) (chain, int) {
	c := chain{1: {0, "init"}, 2: {1, "go"}}
	parent := 2
	for i := 0; i < n; i++ {
		pid := 3 + i
		c[pid] = proc{parent, "x.test"}
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
	c := chain{1: {0, "x.test"}, 2: {1, "y.test"}, 3: {2, "X.TEST"}, 4: {3, "z.test"}, 5: {4, "x.test"}}
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
	c = chain{5: {4, "x.test"}}
	if Count(c.lookup, 5) != 0 {
		t.Error("a missing parent was counted")
	}
	// A loop in the answers does not hold the walk.
	c = chain{5: {6, "x.test"}, 6: {5, "x.test"}}
	if n := Count(c.lookup, 5); n > maxWalk {
		t.Errorf("a loop counted %d", n)
	}
	// A process that is its own parent is not its own ancestor.
	c = chain{5: {5, "x.test"}}
	if Count(c.lookup, 5) != 0 {
		t.Error("a process counted as its own ancestor")
	}
	guard(nil, 5, func(int) { t.Error("exited with no way to read the process tree") })
}

// The real lookup answers for this process with its real parent, and the
// process running this test is not deep.
func TestTheRealLookupKnowsThisProcess(t *testing.T) {
	lookup := osLookup()
	if lookup == nil {
		t.Skip("the process tree cannot be read here")
	}
	parent, name, ok := lookup(os.Getpid())
	if !ok || name == "" {
		t.Fatalf("lookup of this process: %d %q %v", parent, name, ok)
	}
	if parent != os.Getppid() {
		t.Errorf("parent = %d, want %d", parent, os.Getppid())
	}
	if TooDeep(lookup, os.Getpid()) {
		t.Error("this test binary is counted as too deep")
	}
}
