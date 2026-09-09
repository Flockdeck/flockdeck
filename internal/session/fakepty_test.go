package session

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"
)

// fakePTY stands in for a pseudo-terminal, so the parts of a session that do
// not depend on a real process -- resizing, fanning output out, the reader
// reaching the end of the stream -- can be driven exactly rather than by
// starting a shell and hoping it says something.
type fakePTY struct {
	// resizeDelay, when set, is how long a resize of this width takes to
	// apply. It exists to widen the window in which two resizes overlap.
	resizeDelay func(cols int) time.Duration

	reads chan []byte

	mu      sync.Mutex
	applied [][2]int
	written []byte
	closed  bool
}

func newFakePTY() *fakePTY { return &fakePTY{reads: make(chan []byte, 64)} }

// feed queues a chunk for the reader to pick up. A chunk offered to a closed
// pseudo-terminal, or to one nothing is draining, is dropped rather than
// blocking the test that offered it.
func (f *fakePTY) feed(p []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	select {
	case f.reads <- p:
	default:
	}
}

func (f *fakePTY) Read(p []byte) (int, error) {
	chunk, ok := <-f.reads
	if !ok {
		return 0, io.EOF
	}
	return copy(p, chunk), nil
}

func (f *fakePTY) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	f.written = append(f.written, p...)
	return len(p), nil
}

func (f *fakePTY) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	close(f.reads)
	return nil
}

func (f *fakePTY) Resize(cols, rows int) error {
	if f.resizeDelay != nil {
		time.Sleep(f.resizeDelay(cols))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// A real pseudo-terminal does not politely refuse this. On Windows the
	// handle points into the console host and closing it frees what it points
	// at, while go-pty goes on holding the stale value, so a resize after the
	// close reaches the operating system with a pointer to memory that has
	// been given back. Panicking here is the nearest a test can get to that.
	if f.closed {
		panic("resized a pseudo-terminal that had been released")
	}
	f.applied = append(f.applied, [2]int{cols, rows})
	return nil
}

// lastApplied returns the size the pseudo-terminal is actually left at.
func (f *fakePTY) lastApplied() [2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.applied) == 0 {
		return [2]int{}
	}
	return f.applied[len(f.applied)-1]
}

func (f *fakePTY) Name() string { return "fake-pty" }
func (f *fakePTY) Fd() uintptr  { return 0 }

func (f *fakePTY) Command(string, ...string) *pty.Cmd { return nil }

func (f *fakePTY) CommandContext(context.Context, string, ...string) *pty.Cmd { return nil }

// fakeSession builds a session around a fake pseudo-terminal, with no process
// and no reader unless the test starts one.
func fakeSession(f *fakePTY) *Session {
	return &Session{
		ID:          "fake",
		Kind:        KindShell,
		pty:         f,
		status:      StatusStarting,
		statusSince: time.Now(),
		cols:        80,
		rows:        24,
		idleAfter:   time.Minute,
		history:     newRing(4096),
		subs:        map[int]chan []byte{},
		pumped:      make(chan struct{}),
	}
}
