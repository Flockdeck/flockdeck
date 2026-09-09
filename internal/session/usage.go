package session

import (
	"math"
	"sync"
	"time"
)

const (
	// usageInterval is the least time between two readings of the process
	// table. Every pane shares one reading, so this is the whole cadence of
	// the feature rather than a per-pane one. Reading the table is the only
	// part that costs anything measurable -- some milliseconds on Windows,
	// where it means a Toolhelp snapshot of every process on the machine --
	// and a reading averaged over fifteen seconds does not need taking more
	// often than this.
	usageInterval = 5 * time.Second
	// cpuWindow is how far back the reported CPU share looks. A figure taken
	// from one interval alone swings between nothing and a whole core as an
	// agent thinks, writes a file and thinks again, and a number that jumps
	// every time it is read answers no question. Fifteen seconds is long
	// enough to settle and short enough to notice when an agent stops.
	cpuWindow = 15 * time.Second
	// maxTreeProcs bounds a walk of the process tree, so a parent map that
	// disagrees with itself -- which a table assembled from processes that are
	// starting and exiting underneath it can -- costs a bounded amount rather
	// than everything on the machine.
	maxTreeProcs = 512
)

// Usage is what a pane is costing the machine: its process and everything that
// process has spawned, because the `claude` CLI does its work in children and
// a figure covering only the process Perch started would be the wrong one.
type Usage struct {
	// CPUPercent is the share of a single core, averaged over cpuWindow. A
	// pane using two cores flat out reads 200.
	CPUPercent float64
	// RSSBytes is resident memory across the tree.
	RSSBytes uint64
	// Procs is how many processes the reading covers.
	Procs int
	// Known is false when the platform cannot answer, or the pane has no
	// process, or nothing has been read yet. There is no figure to show then,
	// which is better than showing a wrong one.
	Known bool
}

// instantCPU is the share of one core used over a single interval.
func instantCPU(used, over time.Duration) float64 {
	return 100 * used.Seconds() / over.Seconds()
}

// procTable is a view of the processes on the machine, taken once and shared
// by every pane. Building it is the part that costs something, and it does not
// cost more because there are more panes.
type procTable struct {
	at   time.Time
	kids map[int][]int
}

var procs struct {
	mu    sync.Mutex
	table *procTable
}

// readParents and readMetrics are how the platform is read. They are variables
// so a test can put a process table of its own behind them; nothing else
// replaces them.
var (
	readParents = procParents
	readMetrics = procMetrics
)

// currentProcTable returns the shared process table, rebuilding it when what
// it holds is older than usageInterval. It returns nil where the platform
// cannot enumerate processes.
func currentProcTable(now time.Time) *procTable {
	procs.mu.Lock()
	defer procs.mu.Unlock()

	if procs.table != nil && now.Sub(procs.table.at) < usageInterval {
		return procs.table
	}
	parents := readParents()
	if parents == nil {
		procs.table = nil
		return nil
	}
	kids := make(map[int][]int, len(parents))
	for pid, ppid := range parents {
		if ppid > 0 && ppid != pid {
			kids[ppid] = append(kids[ppid], pid)
		}
	}
	procs.table = &procTable{at: now, kids: kids}
	return procs.table
}

// tree returns a process and its descendants, the root first.
func (t *procTable) tree(root int) []int {
	out := []int{root}
	seen := map[int]bool{root: true}
	for i := 0; i < len(out) && len(out) < maxTreeProcs; i++ {
		for _, kid := range t.kids[out[i]] {
			if seen[kid] {
				continue
			}
			seen[kid] = true
			out = append(out, kid)
		}
	}
	return out
}

// Pid returns the process id of the pane's process, or zero when it has none.
func (s *Session) Pid() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// Usage reports what this pane's process tree is costing the machine.
//
// It reads the operating system rather than returning something already
// gathered, so it must not be called per keystroke or per chunk of output; it
// is meant for the periodic refresh that also updates status and branch
// labels. Repeated calls between two readings of the shared process table cost
// nothing and give the same answer.
func (s *Session) Usage() Usage { return s.usageAsOf(time.Now()) }

// usageAsOf is Usage with the clock passed in, so a test can space its
// readings out the way a running application does.
func (s *Session) usageAsOf(now time.Time) Usage {
	pid := s.Pid()
	if pid <= 0 {
		return Usage{}
	}

	table := currentProcTable(now)
	if table == nil {
		return Usage{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == StatusExited {
		return Usage{}
	}
	// One reading per pane per table. Every pane is asked for its usage each
	// time the interface rebuilds its state, which is far more often than the
	// table is rebuilt, and walking the tree is the part that calls into the
	// operating system per process.
	if !table.at.After(s.usageAt) {
		return s.usage
	}

	cpu := make(map[int]time.Duration, len(s.usageCPU)+4)
	var rss uint64
	var delta time.Duration
	count := 0
	for _, p := range table.tree(pid) {
		used, bytes, ok := readMetrics(p)
		if !ok {
			// The process went away between the table being built and being
			// asked about, which for an agent's children is the ordinary case
			// rather than an error.
			continue
		}
		count++
		rss += bytes
		cpu[p] = used
		// Summing the tree and subtracting the previous sum would go negative
		// every time a child exited, because its share of the total leaves
		// with it -- and an agent running tools exits children constantly.
		// Accumulating per process instead only ever adds what was actually
		// used, and counts a process first seen here from when it started,
		// which is inside this interval.
		if before, seen := s.usageCPU[p]; seen && used > before {
			delta += used - before
		} else if !seen {
			delta += used
		}
	}

	prevAt := s.usageAt
	s.usageCPU = cpu
	s.usageAt = table.at
	if count == 0 {
		s.usage = Usage{}
		s.cpuSeeded = false
		return s.usage
	}

	next := Usage{RSSBytes: rss, Procs: count, Known: true}
	dt := table.at.Sub(prevAt)
	switch {
	case prevAt.IsZero() || dt <= 0:
		// The first reading has nothing to compare against: report the memory,
		// which is true straight away, and no CPU share yet.
		next.CPUPercent = 0
	case !s.cpuSeeded:
		// The first interval that can be measured is taken as it stands.
		// Averaging it against the nothing that came before would start every
		// pane at zero and creep towards the truth over a minute, which is a
		// minute of the reading saying the opposite of what is happening --
		// and an agent's first minute is exactly when someone is watching to
		// see whether it has got going.
		next.CPUPercent = instantCPU(delta, dt)
		s.cpuSeeded = true
	default:
		// Readings are not evenly spaced -- the interface refreshes when
		// something happens as well as on its timer -- so the weight of a new
		// one has to follow how much time it covers, or a burst of refreshes
		// would drag the average about far faster than the window says.
		alpha := 1 - math.Exp(-dt.Seconds()/cpuWindow.Seconds())
		next.CPUPercent = s.usage.CPUPercent + alpha*(instantCPU(delta, dt)-s.usage.CPUPercent)
	}
	if next.CPUPercent < 0 {
		next.CPUPercent = 0
	}
	s.usage = next
	return s.usage
}
