package gitx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func quickGate(t *testing.T) {
	t.Helper()
	ow, od, ob, or := indexWriterWait, indexDrainWait, indexBusyBeforeDrain, indexReaderWait
	indexDrainWait = 400 * time.Millisecond
	indexReaderWait = 2 * time.Second
	t.Cleanup(func() { indexWriterWait, indexDrainWait, indexBusyBeforeDrain, indexReaderWait = ow, od, ob, or })
}

// Readers that overlap for ever starve a writer that only waits for a quiet moment.
// After a few refusals the readers that arrive wait a short time, the running ones
// finish, and the refresh gets in.
func TestOverlappingReadersDoNotStarveTheRefresh(t *testing.T) {
	quickGate(t)
	repo := newRepo(t)
	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			time.Sleep(time.Duration(i) * 40 * time.Millisecond)
			for {
				select {
				case <-stop:
					return
				default:
				}
				rel, err := readIndex(ctx, repo)
				if err != nil {
					return
				}
				time.Sleep(80 * time.Millisecond)
				rel()
			}
		}(i)
	}
	t.Cleanup(func() { close(stop); wg.Wait() })
	time.Sleep(100 * time.Millisecond)
	got := false
	tries := 0
	for ; tries < 12 && !got; tries++ {
		rel, err := tryWriteIndex(ctx, repo, 60*time.Millisecond)
		if err == nil {
			got = true
			rel()
		} else if !errors.Is(err, ErrIndexBusy) {
			t.Fatal(err)
		}
	}
	if !got {
		t.Errorf("the refresh was turned away %d times by overlapping readers", tries)
	}
}

// The drain holds readers back only for its bound, and a reader that waits is let
// in after the writer is done.
func TestADrainHoldsNewReadersUntilTheWriterIsDone(t *testing.T) {
	quickGate(t)
	indexBusyBeforeDrain = 0
	repo := newRepo(t)
	ctx := context.Background()
	first, _ := readIndex(ctx, repo)
	var wrote atomic.Bool
	wdone := make(chan struct{})
	go func() {
		rel, err := tryWriteIndex(ctx, repo, 2*time.Second)
		if err == nil {
			wrote.Store(true)
			time.Sleep(60 * time.Millisecond)
			rel()
		}
		close(wdone)
	}()
	time.Sleep(60 * time.Millisecond) // the writer is waiting, the drain is up
	entered := make(chan struct{})
	go func() {
		rel, err := readIndex(ctx, repo)
		if err == nil {
			rel()
		}
		close(entered)
	}()
	select {
	case <-entered:
		t.Fatal("a reader got in beside a waiting writer during the drain")
	case <-time.After(100 * time.Millisecond):
	}
	first() // the running reader finishes: the writer's turn
	select {
	case <-wdone:
	case <-time.After(3 * time.Second):
		t.Fatal("the writer did not get in")
	}
	if !wrote.Load() {
		t.Error("the writer did not run")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the waiting reader was never let in")
	}
}

// A reader that waited for one writer does not go ahead when another has taken
// over by the time it looks again.
func TestAReaderLooksAgainAfterAWriterEnds(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	relA, err := tryWriteIndex(ctx, repo, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan func(), 1)
	go func() {
		rel, err := readIndex(ctx, repo)
		if err == nil {
			entered <- rel
		}
	}()
	time.Sleep(100 * time.Millisecond)
	// A ends and B begins with nothing between them.
	g := gateFor(repo)
	g.mu.Lock()
	g.active = false
	close(g.activeDone)
	g.active = true
	g.activeDone = make(chan struct{})
	g.mu.Unlock()
	_ = relA
	select {
	case <-entered:
		t.Fatal("the reader went ahead beside the second writer")
	case <-time.After(300 * time.Millisecond):
	}
	g.endWrite()
	select {
	case rel := <-entered:
		rel()
	case <-time.After(3 * time.Second):
		t.Fatal("the reader never got in after the second writer")
	}
}

// The shipped waits, not shortened by a test: a reader behind a running writer goes
// ahead after about half a second, and a refresh that finds a reader that never lets
// go gives up after about two. It takes three seconds.
func TestTheShippedWaitsAreWhatTheyAreMeantToBe(t *testing.T) {
	if testing.Short() {
		t.Skip("takes three seconds")
	}
	repo := newRepo(t)
	ctx := context.Background()
	rel, err := tryWriteIndex(ctx, repo, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	r, err := readIndex(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	r()
	readerTook := time.Since(started)
	rel()

	held, _ := readIndex(ctx, repo) // never lets go
	defer held()
	started = time.Now()
	if _, err := tryWriteIndex(ctx, repo, indexWriterWait); !errors.Is(err, ErrIndexBusy) {
		t.Errorf("a writer behind a reader that never lets go = %v, want ErrIndexBusy", err)
	}
	writerTook := time.Since(started)
	// Under load a wait can only run long, so the windows are wide: each within five
	// times the constant either way, and the reader's shorter than the writer's.
	if readerTook < indexReaderWait/5 || readerTook > indexReaderWait*5 {
		t.Errorf("a reader waited %v behind a writer, the constant is %v", readerTook, indexReaderWait)
	}
	if writerTook < indexWriterWait/5 || writerTook > indexWriterWait*5 {
		t.Errorf("a writer waited %v for the readers, the constant is %v", writerTook, indexWriterWait)
	}
	if readerTook >= writerTook {
		t.Errorf("a reader waited %v and a writer %v, want the reader's wait the shorter", readerTook, writerTook)
	}
	if indexReaderWait != 500*time.Millisecond || indexWriterWait != 2*time.Second {
		t.Errorf("the waits are %v and %v, want 500ms and 2s", indexReaderWait, indexWriterWait)
	}
	if indexStopWaiting != time.Minute {
		t.Errorf("the refresh is waited for %v, want 1m", indexStopWaiting)
	}
}

// The diff of the panel's file view goes through the gate like Changes: it waits for
// a refresh that is writing, for at most the readers' bound, and then shows the diff.
func TestThePanelDiffWaitsForARefreshThatIsWriting(t *testing.T) {
	old := indexReaderWait
	indexReaderWait = 400 * time.Millisecond
	t.Cleanup(func() { indexReaderWait = old })
	repo, _ := staleRepo(t, 3)
	write(t, repo, "f000.txt", "changed\n")
	rel, err := tryWriteIndex(context.Background(), repo, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	started := time.Now()
	out, err := Diff(repo, "f000.txt")
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took < 300*time.Millisecond {
		t.Errorf("the diff took %v with a writer active, so it did not wait", took)
	}
	if out == "" {
		t.Error("the diff is empty")
	}
}
