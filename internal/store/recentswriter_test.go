package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// slowDisk replaces the writer of the recent projects file with one that takes
// delay for each write and says what it was given, and puts it back.
func slowDisk(t *testing.T, delay time.Duration) (written func() [][]Project) {
	t.Helper()
	var mu sync.Mutex
	var seen [][]Project
	real := recentsFileWriter
	recentsFileWriter = func(path string, list []Project) error {
		time.Sleep(delay)
		mu.Lock()
		seen = append(seen, append([]Project(nil), list...))
		mu.Unlock()
		return real(path, list)
	}
	t.Cleanup(func() {
		FlushRecents()
		recentsFileWriter = real
	})
	return func() [][]Project {
		mu.Lock()
		defer mu.Unlock()
		return append([][]Project(nil), seen...)
	}
}

func rootsOf(list []Project) map[string]bool {
	m := map[string]bool{}
	for _, p := range list {
		m[p.Root] = true
	}
	return m
}

// TestRecentWritesAreInOrderAndTheLastStateWins covers many changes made at
// once while the disk is slow. Each change builds on the state before it, so
// every write holds everything an earlier one did, writes are not reordered,
// and what is on disk when they end is the state the last change made.
func TestRecentWritesAreInOrderAndTheLastStateWins(t *testing.T) {
	isolateConfig(t)
	written := slowDisk(t, 3*time.Millisecond)
	base := t.TempDir()

	const workers, each = 6, 5
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				root := filepath.Join(base, fmt.Sprintf("w%d-%d", w, i))
				if err := TouchRecent(root); err != nil {
					t.Errorf("touch: %v", err)
				}
				// A rename of what was just recorded, waited for: it has to
				// find the project in the state the touch made.
				if err := SetProjectName(root, fmt.Sprintf("n%d-%d", w, i)); err != nil {
					t.Errorf("rename: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	FlushRecents()

	final, err := Recents()
	if err != nil {
		t.Fatal(err)
	}
	if len(final) != workers*each {
		t.Fatalf("%d projects after %d were recorded", len(final), workers*each)
	}
	for _, p := range final {
		if p.Name == "" {
			t.Errorf("%s lost its name", p.Root)
		}
	}

	writes := written()
	if len(writes) == 0 {
		t.Fatal("nothing was written")
	}
	for i := 1; i < len(writes); i++ {
		prev, now := rootsOf(writes[i-1]), rootsOf(writes[i])
		for root := range prev {
			if !now[root] {
				t.Errorf("write %d dropped %s, which write %d had", i, filepath.Base(root), i-1)
			}
		}
	}
	// What the file holds is the last write, and it is the final state.
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, recentsFile))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []Project
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	for _, p := range final {
		if q, ok := findProject(onDisk, p.Root); !ok || q.Name != p.Name {
			t.Errorf("the file does not hold %s as %q", p.Root, p.Name)
		}
	}
	t.Logf("%d changes, %d writes", workers*each*2, len(writes))
}

// TestASwitchIsRecordedWhileARenameWaitsForTheDisk covers the workspace's
// goroutine, which records every switch between projects. With the disk stopped
// it gets its answer at once, sees its own change, and is not held up by a
// rename that is waiting for the disk on another goroutine.
func TestASwitchIsRecordedWhileARenameWaitsForTheDisk(t *testing.T) {
	isolateConfig(t)
	release := make(chan struct{})
	real := recentsFileWriter
	started := make(chan struct{}, 1)
	recentsFileWriter = func(path string, list []Project) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return real(path, list)
	}
	var once sync.Once
	let := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		let()
		FlushRecents()
		recentsFileWriter = real
	})
	base := t.TempDir()
	one, two := filepath.Join(base, "one"), filepath.Join(base, "two")

	renamed := make(chan error, 1)
	go func() { renamed <- SetProjectName(one, "One") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the write never started")
	}
	select {
	case err := <-renamed:
		t.Fatalf("the rename returned with the disk stopped: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// The workspace goroutine, with the disk stopped and a write waiting on it.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 20 {
			if err := TouchRecent(filepath.Join(base, fmt.Sprintf("p%d", i))); err != nil {
				t.Errorf("touch: %v", err)
			}
		}
		if err := TouchRecent(two); err != nil {
			t.Errorf("touch: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recording a switch waited for the disk")
	}
	list, err := Recents()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 || list[0].Root != filepath.Clean(two) {
		t.Errorf("the switch just recorded is not first: %+v", list)
	}
	if p, ok := findProject(list, one); !ok || p.Name != "One" {
		t.Errorf("the rename is not in the state the switches built on: %+v", p)
	}

	let()
	if err := <-renamed; err != nil {
		t.Errorf("rename: %v", err)
	}
	FlushRecents()
}

// A write that fails is reported to the caller who waited for it, and the list
// is what the file holds again, as it was when the write was made inline.
func TestAFailedRecentWriteIsReportedAndForgotten(t *testing.T) {
	isolateConfig(t)
	real := recentsFileWriter
	recentsFileWriter = func(string, []Project) error { return errors.New("the disk is full") }
	t.Cleanup(func() {
		FlushRecents()
		recentsFileWriter = real
	})
	root := filepath.Join(t.TempDir(), "p")
	if err := SetProjectName(root, "Name"); err == nil {
		t.Fatal("a rename that could not be written said it had been")
	}
	list, err := Recents()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("the list shows a change that was not written: %+v", list)
	}
}

// stubWrites replaces the writer of the recent projects file with one that
// waits for gate[i] before its i-th write (a nil gate does not wait) and fails
// it when fail[i] is set; the others go to the real file.
func stubWrites(t *testing.T, gate []chan struct{}, fail []bool) (started chan int) {
	t.Helper()
	real := recentsFileWriter
	started = make(chan int, 16)
	var mu sync.Mutex
	n := 0
	recentsFileWriter = func(path string, list []Project) error {
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		started <- i
		if i < len(gate) && gate[i] != nil {
			<-gate[i]
		}
		if i < len(fail) && fail[i] {
			return fmt.Errorf("write %d failed", i)
		}
		return real(path, list)
	}
	t.Cleanup(func() {
		for _, g := range gate {
			if g != nil {
				select {
				case <-g:
				default:
					close(g)
				}
			}
		}
		FlushRecents()
		recentsFileWriter = real
	})
	return started
}

func onDiskRoots(t *testing.T) map[string]string {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, recentsFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var list []Project
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, p := range list {
		m[filepath.Base(p.Root)] = p.Name
	}
	return m
}

// TestACallerIsToldHowItsOwnChangeWasWritten covers two renames made one after
// the other while the disk is slow, the second on top of the first. When the
// write of the first fails, the second was built on it and would put it on disk
// if it were written, so both fail and neither is: each caller is told so, the
// list goes back to the file, and a later change that succeeds does not carry
// either of them.
func TestACallerIsToldHowItsOwnChangeWasWritten(t *testing.T) {
	isolateConfig(t)
	gate := []chan struct{}{make(chan struct{})}
	started := stubWrites(t, gate, []bool{true})
	base := t.TempDir()
	a, b, c := filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "c")

	errA, errB := make(chan error, 1), make(chan error, 1)
	go func() { errA <- SetProjectName(a, "A") }()
	<-started // the write of A's state is under way, and fails once let go
	go func() { errB <- SetProjectName(b, "B") }()
	// B is built on A's state, which Recents shows while it is pending.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if list, _ := Recents(); len(list) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second rename never became the newest state")
		}
	}
	close(gate[0])

	if err := <-errA; err == nil {
		t.Error("the caller whose write failed was told it had been written")
	}
	if err := <-errB; err == nil {
		t.Error("the caller whose change was built on a write that failed was told it had been written")
	}
	if list, _ := Recents(); len(list) != 0 {
		t.Errorf("the list shows changes that were told to have failed: %+v", list)
	}
	if err := SetProjectName(c, "C"); err != nil {
		t.Fatalf("a later change: %v", err)
	}
	FlushRecents()
	got := onDiskRoots(t)
	if len(got) != 1 || got["c"] != "C" {
		t.Errorf("the file holds %v, want only c, as the later change that succeeded", got)
	}
}

// And the other way round: the first write succeeds and a later one fails. The
// first caller is told it was written, and it is; the second is told it was not,
// and it is not.
func TestAnEarlierWriteThatSucceededIsNotReportedAsTheLaterOneThatFailed(t *testing.T) {
	isolateConfig(t)
	gate := []chan struct{}{make(chan struct{}), make(chan struct{})}
	started := stubWrites(t, gate, []bool{false, true})
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")

	errA, errB := make(chan error, 1), make(chan error, 1)
	go func() { errA <- SetProjectName(a, "A") }()
	<-started
	// B is made while A's write is still under way, and is written next.
	go func() { errB <- SetProjectName(b, "B") }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if list, _ := Recents(); len(list) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second rename never became the newest state")
		}
	}
	close(gate[0])
	if err := <-errA; err != nil {
		t.Errorf("a change that was written was reported as failed: %v", err)
	}
	<-started
	close(gate[1])
	if err := <-errB; err == nil {
		t.Error("a change whose write failed was reported as written")
	}
	FlushRecents()
	got := onDiskRoots(t)
	if len(got) != 1 || got["a"] != "A" {
		t.Errorf("the file holds %v, want only a: written, and then the failed write of b", got)
	}
	if list, _ := Recents(); len(list) != 1 || filepath.Base(list[0].Root) != "a" {
		t.Errorf("the list is %+v, want what the file holds", list)
	}
}

// scriptedWriter replaces the writer of the recent projects file with one whose
// i-th write waits for gate[i] and returns errs[i], and says when each starts.
func scriptedWriter(t *testing.T, gate []chan struct{}, errs []error) (started chan int) {
	t.Helper()
	real := recentsFileWriter
	started = make(chan int, 16)
	var mu sync.Mutex
	n := 0
	recentsFileWriter = func(string, []Project) error {
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		started <- i
		if i < len(gate) && gate[i] != nil {
			<-gate[i]
		}
		if i < len(errs) {
			return errs[i]
		}
		return nil
	}
	t.Cleanup(func() { recentsFileWriter = real })
	return started
}

// TestWaitReturnsTheResultOfTheCallersOwnWrite reads the results after a later
// write has failed. A wait that returned the latest write's result would give
// the caller of the first, whose change was written, the failure of the second.
// The callers here come for their results only once both writes are settled,
// so the order they wake in cannot hide it.
func TestWaitReturnsTheResultOfTheCallersOwnWrite(t *testing.T) {
	gate := []chan struct{}{make(chan struct{}), make(chan struct{})}
	started := scriptedWriter(t, gate, []error{nil, errors.New("the second write failed")})
	w := newRecentsWriter()
	path := filepath.Join(t.TempDir(), "projects.json")

	g1 := w.enqueue(path, []Project{{Root: "/a"}})
	<-started // the first write is under way
	g2 := w.enqueue(path, []Project{{Root: "/a"}, {Root: "/b"}})
	close(gate[0])
	<-started // the second
	close(gate[1])
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		w.mu.Lock()
		settled := w.written[path] >= g2
		w.mu.Unlock()
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second write was never settled")
		}
	}

	if err := w.wait(path, g1); err != nil {
		t.Errorf("the caller of the first write, which succeeded, was told: %v", err)
	}
	if err := w.wait(path, g2); err == nil {
		t.Error("the caller of the second write, which failed, was told it succeeded")
	}
}

// TestAResultThatIsNoLongerKeptIsNotReportedAsSuccess covers a caller that comes
// for its result after more than recentsResultsKept later ones have been
// settled. It is told the result is gone, not that its change was written.
func TestAResultThatIsNoLongerKeptIsNotReportedAsSuccess(t *testing.T) {
	scriptedWriter(t, nil, []error{errors.New("the first write failed")})
	old := recentsResultsKept
	recentsResultsKept = 2
	t.Cleanup(func() { recentsResultsKept = old })
	w := newRecentsWriter()
	path := filepath.Join(t.TempDir(), "projects.json")

	var gens []uint64
	for i := range 5 {
		g := w.enqueue(path, []Project{{Root: fmt.Sprint("/p", i)}})
		gens = append(gens, g)
		_ = w.wait(path, g) // settled before the next is made, so each is its own write
	}
	if err := w.wait(path, gens[0]); !errors.Is(err, errRecentsResultLost) {
		t.Errorf("a result dropped long ago gave %v, want errRecentsResultLost", err)
	}
	if err := w.wait(path, gens[4]); err != nil {
		t.Errorf("the newest result gave %v, want it kept and a success", err)
	}
	if err := w.wait(path, gens[3]); err != nil {
		t.Errorf("a result still kept gave %v", err)
	}
}
