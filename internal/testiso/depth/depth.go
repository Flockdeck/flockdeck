// Package depth stops a test binary that is running as its own descendant,
// however deep, from going on.
//
// Several test suites start their own test binary again as a fake program (an
// agent, an adapter, a helper). The child is told what to be by an environment
// variable or an argument. If that is lost on the way, for instance because the
// code under test builds the child's environment and drops it, the child runs
// the whole suite instead, which starts the next child, and so on without end.
// That brought a machine down once.
//
// A variable cannot guard against that, since the loss is of the variable. What
// the loss cannot change is who started the process: Guard counts the
// ancestors of the process that are running the same program and exits when
// there are too many. The most a real suite nests is a test binary, a fake
// program it starts and a process that one starts (Limit - 1 ancestors).
package depth

import (
	"fmt"
	"os"
	"strings"
)

// Limit is the number of ancestors running the same program at which Guard
// exits.
const Limit = 3

// maxWalk bounds the walk up the process tree, so a loop in the answers cannot
// hold it.
const maxWalk = 64

// Lookup answers, for a process id, its parent's id and the name of the program
// it runs. ok is false when the process is not known. Names are compared with
// each other, so they need only be the same for the same program.
type Lookup func(pid int) (parent int, name string, ok bool)

// Count is the number of ancestors of pid that run the same program as pid.
func Count(lookup Lookup, pid int) int {
	parent, name, ok := lookup(pid)
	if !ok {
		return 0
	}
	n := 0
	for i := 0; i < maxWalk && parent > 0 && parent != pid; i++ {
		next, pname, ok := lookup(parent)
		if !ok {
			break
		}
		if strings.EqualFold(pname, name) {
			n++
		}
		pid, parent = parent, next
	}
	return n
}

// TooDeep reports whether pid has Limit or more ancestors running its program.
func TooDeep(lookup Lookup, pid int) bool {
	return Count(lookup, pid) >= Limit
}

// Guard exits the process with status 2, before it does anything else, when it
// has Limit or more ancestors running the same program. Call it first in a
// test binary's TestMain, and first in any init that acts before TestMain. It
// does nothing where the process tree cannot be read.
func Guard() {
	guard(osLookup(), os.Getpid(), os.Exit)
}

func guard(lookup Lookup, pid int, exit func(int)) {
	if lookup == nil || !TooDeep(lookup, pid) {
		return
	}
	fmt.Fprintf(os.Stderr, "testiso: this test binary has %d or more ancestors that are the same program; stopping rather than start another (see internal/testiso/depth)\n", Limit)
	exit(2)
}
