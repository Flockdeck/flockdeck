package session

import (
	"os"
	"testing"
	"time"

	"github.com/aymanbagabas/go-pty"
)

// fakeProc is one process in a process table a test puts up.
type fakeProc struct {
	ppid int
	cpu  time.Duration
	rss  uint64
	// gone marks a process that is in the table but has ended by the time it
	// is asked for its figures, which is what an agent's children do
	// constantly.
	gone bool
}

// fakeProcs puts a process table of the test's own behind the platform
// readers, and counts how many times the table is built.
func fakeProcs(t *testing.T, table map[int]*fakeProc) *int {
	t.Helper()
	oldParents, oldMetrics := readParents, readMetrics
	t.Cleanup(func() {
		readParents, readMetrics = oldParents, oldMetrics
		resetProcTable()
	})

	builds := 0
	readParents = func() map[int]int {
		builds++
		out := make(map[int]int, len(table))
		for pid, p := range table {
			out[pid] = p.ppid
		}
		return out
	}
	readMetrics = func(pid int) (time.Duration, uint64, bool) {
		p, ok := table[pid]
		if !ok || p.gone {
			return 0, 0, false
		}
		return p.cpu, p.rss, true
	}
	resetProcTable()
	return &builds
}

func resetProcTable() {
	procs.mu.Lock()
	procs.table = nil
	procs.mu.Unlock()
}

// usagePane builds a session standing in for a pane with a process, with no
// pseudo-terminal behind it.
func usagePane(pid int) *Session {
	return &Session{
		ID:          "usage-pane",
		Kind:        KindClaude,
		cmd:         &pty.Cmd{Process: &os.Process{Pid: pid}},
		status:      StatusWorking,
		statusSince: time.Now(),
		idleAfter:   time.Minute,
		history:     newRing(1024),
		subs:        map[int]chan []byte{},
	}
}

// TestUsageCoversTheWholeProcessTree is the point of the reading: the `claude`
// CLI does its work in children, so a figure covering only the process Perch
// started would say a busy agent was costing nothing.
func TestUsageCoversTheWholeProcessTree(t *testing.T) {
	fakeProcs(t, map[int]*fakeProc{
		1:   {ppid: 0},
		100: {ppid: 1, rss: 10 << 20},
		200: {ppid: 100, rss: 20 << 20},
		300: {ppid: 200, rss: 30 << 20},
		400: {ppid: 1, rss: 999 << 20}, // another pane's, or the shell's
	})

	u := usagePane(100).usageAsOf(time.Now())
	if !u.Known {
		t.Fatal("the reading should be known")
	}
	if u.Procs != 3 {
		t.Errorf("Procs = %d, want the process and its two descendants", u.Procs)
	}
	if want := uint64(60 << 20); u.RSSBytes != want {
		t.Errorf("RSSBytes = %d, want %d", u.RSSBytes, want)
	}
}

// TestUsageSkipsAProcessThatEndsMidSample covers the ordinary case rather than
// an unusual one: an agent running tools starts and reaps children constantly,
// so some of what the process table lists is gone by the time it is asked
// about. Its own children stay reachable, because the table is what says where
// they are.
func TestUsageSkipsAProcessThatEndsMidSample(t *testing.T) {
	fakeProcs(t, map[int]*fakeProc{
		100: {ppid: 1, rss: 10 << 20},
		200: {ppid: 100, rss: 20 << 20, gone: true},
		300: {ppid: 200, rss: 30 << 20},
	})

	u := usagePane(100).usageAsOf(time.Now())
	if u.Procs != 2 {
		t.Errorf("Procs = %d, want the two that answered", u.Procs)
	}
	if want := uint64(40 << 20); u.RSSBytes != want {
		t.Errorf("RSSBytes = %d, want %d", u.RSSBytes, want)
	}
}

// TestUsageCPUSettlesQuicklyThenSmoothly covers the number a person reads to
// find which agent is eating the machine. It has to mean something within a
// reading or two of a pane opening, and then stop swinging: one interval on
// its own goes from nothing to a whole core as an agent thinks, calls a tool
// and thinks again.
func TestUsageCPUSettlesQuicklyThenSmoothly(t *testing.T) {
	root := &fakeProc{ppid: 1, rss: 1 << 20}
	fakeProcs(t, map[int]*fakeProc{100: root})
	s := usagePane(100)

	now := time.Now()
	if u := s.usageAsOf(now); u.CPUPercent != 0 {
		t.Errorf("the first reading has nothing to compare against, got %v%%", u.CPUPercent)
	}

	// Half a core, steadily. The first interval that can be measured is the
	// answer, not a first step towards it.
	step := 10 * time.Second
	now = now.Add(step)
	root.cpu += step / 2
	if u := s.usageAsOf(now); u.CPUPercent < 45 || u.CPUPercent > 55 {
		t.Errorf("the first measured interval reads %v%%, want the 50%% it measured", u.CPUPercent)
	}

	var last Usage
	for i := 0; i < 6; i++ {
		now = now.Add(step)
		root.cpu += step / 2
		last = s.usageAsOf(now)
	}
	if last.CPUPercent < 45 || last.CPUPercent > 55 {
		t.Errorf("a steady half core reads %v%%", last.CPUPercent)
	}

	// A single quiet interval must not wipe the reading out, and a single busy
	// one must not take it straight to the top.
	now = now.Add(step)
	if quiet := s.usageAsOf(now); quiet.CPUPercent < 20 {
		t.Errorf("one idle interval dropped the average to %v%%", quiet.CPUPercent)
	}
	now = now.Add(step)
	root.cpu += 4 * step // four cores flat out
	if busy := s.usageAsOf(now); busy.CPUPercent > 250 {
		t.Errorf("one busy interval took the average to %v%%", busy.CPUPercent)
	}
}

// TestUsageCPUDoesNotGoNegativeWhenAChildEnds covers what summing the tree and
// differencing the sums would do. A child's whole accumulated CPU leaves with
// it, so the total drops, and the pane would report having used less than
// nothing.
func TestUsageCPUDoesNotGoNegativeWhenAChildEnds(t *testing.T) {
	root := &fakeProc{ppid: 1, cpu: time.Second, rss: 1 << 20}
	child := &fakeProc{ppid: 100, cpu: 4 * time.Second, rss: 1 << 20}
	fakeProcs(t, map[int]*fakeProc{100: root, 200: child})
	s := usagePane(100)

	now := time.Now()
	s.usageAsOf(now)

	// The child, and the four seconds of CPU it had accumulated, are gone.
	child.gone = true
	root.cpu += 500 * time.Millisecond
	now = now.Add(10 * time.Second)
	u := s.usageAsOf(now)

	if u.CPUPercent < 0 {
		t.Errorf("CPUPercent = %v after a child ended", u.CPUPercent)
	}
	if u.CPUPercent > 10 {
		t.Errorf("CPUPercent = %v; only half a second of work happened in ten", u.CPUPercent)
	}
	if u.Procs != 1 {
		t.Errorf("Procs = %d, want just the process that is left", u.Procs)
	}
}

// TestUsageStopsAtACycle covers a parent map that disagrees with itself, which
// a table assembled while processes start and exit underneath it can.
func TestUsageStopsAtACycle(t *testing.T) {
	fakeProcs(t, map[int]*fakeProc{
		100: {ppid: 200, rss: 1 << 20},
		200: {ppid: 100, rss: 1 << 20},
	})

	done := make(chan Usage, 1)
	go func() { done <- usagePane(100).usageAsOf(time.Now()) }()
	select {
	case u := <-done:
		if u.Procs != 2 {
			t.Errorf("Procs = %d, want both, counted once each", u.Procs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("walking a cycle in the process table never finished")
	}
}

// TestUsageBuildsOneTableForEveryPane is what keeps the cost flat as panes are
// opened: reading the process table is the expensive part, and fifteen panes
// asking at once must not do it fifteen times.
func TestUsageBuildsOneTableForEveryPane(t *testing.T) {
	table := map[int]*fakeProc{}
	for pid := 100; pid < 115; pid++ {
		table[pid] = &fakeProc{ppid: 1, rss: 1 << 20}
	}
	builds := fakeProcs(t, table)

	now := time.Now()
	panes := make([]*Session, 0, 15)
	for pid := 100; pid < 115; pid++ {
		panes = append(panes, usagePane(pid))
	}
	for _, p := range panes {
		p.usageAsOf(now)
	}
	if *builds != 1 {
		t.Errorf("fifteen panes read the process table %d times", *builds)
	}

	// And asking again before the interval is up reads nothing at all.
	for _, p := range panes {
		p.usageAsOf(now.Add(usageInterval / 2))
	}
	if *builds != 1 {
		t.Errorf("the table was rebuilt %d times inside one interval", *builds)
	}
}

// TestUsageOfARealProcessTree checks the platform reading itself against this
// test binary, which is a real process with a real parent.
func TestUsageOfARealProcessTree(t *testing.T) {
	resetProcTable()
	t.Cleanup(resetProcTable)

	s := usagePane(os.Getpid())
	u := s.usageAsOf(time.Now())
	if !u.Known {
		t.Skipf("no process table on %s; the reading is left out rather than guessed", os.Getenv("GOOS"))
	}
	if u.Procs < 1 {
		t.Errorf("Procs = %d, want at least this process", u.Procs)
	}
	if u.RSSBytes == 0 {
		t.Error("a running process has resident memory")
	}
}

// BenchmarkUsageSample measures one whole reading -- building the process
// table and walking one pane's tree -- which is what the periodic refresh
// pays.
func BenchmarkUsageSample(b *testing.B) {
	s := usagePane(os.Getpid())
	if !s.usageAsOf(time.Now()).Known {
		b.Skip("no process table on this platform")
	}
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(usageInterval)
		s.usageAsOf(now)
	}
}

// BenchmarkUsagePerExtraPane measures what one more pane adds once the table
// has been read, which is the figure that decides whether fifteen of them is
// affordable.
func BenchmarkUsagePerExtraPane(b *testing.B) {
	s := usagePane(os.Getpid())
	now := time.Now()
	if !s.usageAsOf(now).Known {
		b.Skip("no process table on this platform")
	}
	panes := make([]*Session, b.N)
	for i := range panes {
		panes[i] = usagePane(os.Getpid())
	}
	now = now.Add(usageInterval)
	b.ResetTimer()
	for _, p := range panes {
		p.usageAsOf(now)
	}
}

// BenchmarkProcParents measures reading the process table on its own, which is
// the part of a sample that does not depend on how many panes are open.
func BenchmarkProcParents(b *testing.B) {
	if procParents() == nil {
		b.Skip("no process table on this platform")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		procParents()
	}
}
