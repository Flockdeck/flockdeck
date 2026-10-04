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
