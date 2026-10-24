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
//
// A process table records each process's parent as a number, and a number can
// be given to another process once its holder has gone (Windows does it within
// moments). So the walk up does not trust a number blindly: it stops when it
// meets a process it has already passed (a loop), and when the "parent" started
// after its child, which a real parent never does.
package depth

import (
	"fmt"
	"os"
	"strings"
)

// Limit is the number of ancestors running the same program at which Guard
// exits.
const Limit = 3

// maxWalk bounds the walk up the process tree.
const maxWalk = 64

// Proc is what is known of a process: its parent's id, the name of the program
// it runs, and when it started. Start is in any unit that grows with time and
// is the same for every process on the machine, or 0 when it is not known.
type Proc struct {
	Parent int
	Name   string
	Start  int64
}

// Lookup answers for a process id. ok is false when the process is not known.
// Names are compared with each other, so they need only be the same for the
// same program.
type Lookup func(pid int) (p Proc, ok bool)

// Link is one process of a chain.
type Link struct {
	PID int
	Proc
}

// Chain is pid and its ancestors, nearest first, as far as they can be trusted:
// the walk ends at an unknown process, at one already in the chain (a loop made
// by a reused process id), and at a "parent" that started after its child.
func Chain(lookup Lookup, pid int) []Link {
	p, ok := lookup(pid)
	if !ok {
		return nil
	}
	chain := []Link{{pid, p}}
	seen := map[int]bool{pid: true}
	for len(chain) < maxWalk {
		cur := chain[len(chain)-1]
		if cur.Parent <= 0 || seen[cur.Parent] {
			break
		}
		next, ok := lookup(cur.Parent)
		if !ok {
			break
		}
		if cur.Start != 0 && next.Start != 0 && next.Start > cur.Start {
			break
		}
		seen[cur.Parent] = true
		chain = append(chain, Link{cur.Parent, next})
	}
	return chain
}

// Count is the number of ancestors of pid that run the same program as pid.
func Count(lookup Lookup, pid int) int {
	chain := Chain(lookup, pid)
	n := 0
	for i := 1; i < len(chain); i++ {
		if strings.EqualFold(chain[i].Name, chain[0].Name) {
			n++
		}
	}
	return n
}

// TooDeep reports whether pid has Limit or more ancestors running its program.
func TooDeep(lookup Lookup, pid int) bool {
	return Count(lookup, pid) >= Limit
}

// Describe writes a chain one process to a line, nearest first.
func Describe(chain []Link) string {
	var b strings.Builder
	for _, l := range chain {
		fmt.Fprintf(&b, "  pid %d %s (parent %d, started %d)\n", l.PID, l.Name, l.Parent, l.Start)
	}
	return b.String()
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
	fmt.Fprintf(os.Stderr, "testiso: this test binary has %d or more ancestors that are the same program; stopping rather than start another (see internal/testiso/depth). The chain:\n%s", Limit, Describe(Chain(lookup, pid)))
	exit(2)
}
