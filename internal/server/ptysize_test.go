package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/session"
)

// sizeNote is the text frame that tells a window the size of its pane's pty.
type sizeNote struct {
	Size *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"size"`
}

// waitForSize reads a terminal socket until the pty's size is announced as
// cols x rows, and fails if it never is. It returns every size it was told.
func waitForSize(t *testing.T, conn *websocket.Conn, cols, rows int) [][2]int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var told [][2]int
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting to be told the pane is %dx%d, was told %v: %v", cols, rows, told, err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var n sizeNote
		if json.Unmarshal(data, &n) != nil || n.Size == nil {
			continue
		}
		told = append(told, [2]int{n.Size.Cols, n.Size.Rows})
		if n.Size.Cols == cols && n.Size.Rows == rows {
			return told
		}
	}
}

// TestEveryWindowIsToldTheSizeThePaneReallyIs covers a pane shown in two
// windows -- the desk's own and one through the relay, say. There is one pty
// behind it and it can only be one size, but each window fits its own terminal
// to its own box and never learned what the pty was. A program that addresses
// the screen by row and column -- Claude Code redrawing, and ConPTY on Windows
// repainting it with only the cells that changed -- then wrote into the wrong
// cells of every window the pane was not sized for: letters dropped or
// swapped, rows on top of each other. Until a window has been used the pane is
// the least of each dimension, which is no window's size at all, so it was both.
//
// Each window is now told the pty's size, ahead of the replay and whenever it
// changes, and keeps its terminal that size.
func TestEveryWindowIsToldTheSizeThePaneReallyIs(t *testing.T) {
	srv, _ := newTestServer(t)
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane

	desk := dialResumable(t, srv, paneID, "&from=-1&size=1")
	phone := dialResumable(t, srv, paneID, "&from=-1&size=1")

	// Two windows of different shapes, neither of them used: the pane is the
	// least of each, 70 columns by 20 rows, which neither of them is.
	sendResize(t, desk, 100, 20)
	sendResize(t, phone, 70, 40)

	waitForSize(t, desk, 70, 20)
	waitForSize(t, phone, 70, 20)

	// Using the phone's window takes the pane to its size, and the other
	// window is told, rather than being left at the size of its own box.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := phone.Write(ctx, websocket.MessageBinary, []byte("x")); err != nil {
		t.Fatalf("type in the phone's window: %v", err)
	}
	waitForSize(t, desk, 70, 40)
	waitForSize(t, phone, 70, 40)
}

// TestAWindowThatDoesNotAskIsNotToldTheSize keeps a window from before this
// existed working. It reads every text frame as the opening of a run, and
// starts its terminal afresh for one, so a size frame it did not ask for would
// clear its screen.
func TestAWindowThatDoesNotAskIsNotToldTheSize(t *testing.T) {
	srv, _ := newTestServer(t)
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane

	old := dialResumable(t, srv, paneID, "&from=-1")
	sendResize(t, old, 90, 30)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	headers := 0
	for {
		typ, data, err := old.Read(ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		var n sizeNote
		if json.Unmarshal(data, &n) == nil && n.Size != nil {
			t.Fatalf("a window that did not ask was sent a size: %s", data)
		}
		headers++
	}
	if headers != 1 {
		t.Fatalf("the window was sent %d text frames, want only the header", headers)
	}
}

// fakeSizes is a pane's record of its sizes, with the marks and the channel a
// test chooses.
type fakeSizes struct {
	mu      sync.Mutex
	marks   []session.SizeMark
	changed chan struct{}
}

func newFakeSizes(marks ...session.SizeMark) *fakeSizes {
	f := &fakeSizes{marks: marks, changed: make(chan struct{})}
	for i := range f.marks {
		f.marks[i].Seq = int64(i + 1)
	}
	return f
}

func (f *fakeSizes) SizeMarks(after int64) ([]session.SizeMark, <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []session.SizeMark
	for _, m := range f.marks {
		if m.Seq > after {
			out = append(out, m)
		}
	}
	return out, f.changed
}

// resize adds a mark, and wakes the feed as the session does.
func (f *fakeSizes) resize(at int64, cols, rows int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marks = append(f.marks, session.SizeMark{Seq: int64(len(f.marks) + 1), At: at, Cols: cols, Rows: rows})
	close(f.changed)
	f.changed = make(chan struct{})
}

// sizedPair streams replay and out to a real websocket as streamOutputSized
// does for a window told the size, and returns what that window receives as a
// list: "<cols>x<rows>" for a size, and the bytes themselves for output.
func sizedPair(t *testing.T, src sizeSource, start int64, replay []byte, out <-chan []byte, frame int) func() []string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if streamOutputSized(r.Context(), conn, replay, out, frame, nil, newSizeFeed(src, start)) {
			_ = conn.Close(websocket.StatusNormalClosure, "stream ended")
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return func() []string {
		var got []string
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return got
			}
			if typ == websocket.MessageText {
				var n sizeNote
				if json.Unmarshal(data, &n) != nil || n.Size == nil {
					t.Fatalf("unexpected text frame %s", data)
				}
				got = append(got, fmt.Sprintf("%dx%d", n.Size.Cols, n.Size.Rows))
				continue
			}
			// Frames are cut anywhere; the order is what is checked.
			if last := len(got) - 1; last >= 0 && !strings.Contains(got[last], "x") && !isSizeText(got[last]) {
				got[last] += string(data)
			} else {
				got = append(got, string(data))
			}
		}
	}
}

func isSizeText(s string) bool {
	var c, r int
	n, _ := fmt.Sscanf(s, "%dx%d", &c, &r)
	return n == 2
}

// TestTheReplayIsDrawnAtTheSizesItWasWrittenAt covers a window attaching to a
// pane whose buffer was written across several sizes -- a phone opened on an
// agent that has been resized as windows came and went. The history was drawn
// by the program for one size after another, and the whole of it was replayed
// at the current one: cells addressed for 100 columns written into a terminal
// of 60, until the program next redrew everything. Each part of the replay is
// sent at the size it was drawn for.
func TestTheReplayIsDrawnAtTheSizesItWasWrittenAt(t *testing.T) {
	src := newFakeSizes(
		session.SizeMark{At: 0, Cols: 80, Rows: 24},
		session.SizeMark{At: 10, Cols: 100, Rows: 30},
		session.SizeMark{At: 20, Cols: 60, Rows: 20},
	)
	out := make(chan []byte)
	close(out)
	read := sizedPair(t, src, 0, []byte("0123456789ABCDEFGHIJabcdefghij"), out, replayFrame)
	got := read()
	want := []string{"80x24", "0123456789", "100x30", "ABCDEFGHIJ", "60x20", "abcdefghij"}
	if !slices.Equal(got, want) {
		t.Fatalf("the window was sent %q, want %q", got, want)
	}
}

// TestAReplayBeginsAtTheSizeInForceThere is a replay that starts part way
// through the history, as one does once the buffer has wrapped: the sizes
// before it are not sent, only the one in force where it begins.
func TestAReplayBeginsAtTheSizeInForceThere(t *testing.T) {
	src := newFakeSizes(
		session.SizeMark{At: 0, Cols: 80, Rows: 24},
		session.SizeMark{At: 50, Cols: 90, Rows: 30},
		session.SizeMark{At: 100, Cols: 70, Rows: 25},
		session.SizeMark{At: 106, Cols: 71, Rows: 26},
	)
	out := make(chan []byte)
	close(out)
	got := sizedPair(t, src, 100, []byte("0123456789"), out, replayFrame)()
	want := []string{"70x25", "012345", "71x26", "6789"}
	if !slices.Equal(got, want) {
		t.Fatalf("the window was sent %q, want %q", got, want)
	}
}

// TestASizeComesBetweenTheOutputBeforeItAndAfterIt forces the interleaving
// that left the size and the output to a coin toss: a resize, with output
// already queued behind it and output queued before it, all ready at once. The
// size goes where the session says it began, whichever the loop looks at first,
// and however the frames are cut.
func TestASizeComesBetweenTheOutputBeforeItAndAfterIt(t *testing.T) {
	for _, frame := range []int{replayFrame, 3} {
		src := newFakeSizes(session.SizeMark{At: 0, Cols: 80, Rows: 24})
		out := make(chan []byte, 8)
		out <- []byte("before-")
		out <- []byte("-still-before")
		// Resized after 20 bytes, with the next output already waiting.
		src.resize(int64(len("before--still-before")), 120, 40)
		out <- []byte("after")
		close(out)
		got := sizedPair(t, src, 0, nil, out, frame)()
		joined := strings.Join(got, "|")
		if frame == replayFrame {
			want := []string{"80x24", "before--still-before", "120x40", "after"}
			if !slices.Equal(got, want) {
				t.Fatalf("frame %d: the window was sent %q, want %q", frame, got, want)
			}
		} else if !strings.HasPrefix(joined, "80x24|") || !strings.Contains(joined, "before--still-before|120x40|after") {
			t.Fatalf("frame %d: the window was sent %q, want the size between the two", frame, got)
		}
	}
}

// TestASizeInTheMiddleOfAFrameSplitsIt is a resize that fell inside one read of
// the pty, whose bytes the stream merged into one frame.
func TestASizeInTheMiddleOfAFrameSplitsIt(t *testing.T) {
	src := newFakeSizes(session.SizeMark{At: 0, Cols: 80, Rows: 24})
	out := make(chan []byte, 2)
	out <- []byte("123456789")
	src.resize(4, 100, 30)
	close(out)
	got := sizedPair(t, src, 0, nil, out, coalesceLimit)()
	want := []string{"80x24", "1234", "100x30", "56789"}
	if !slices.Equal(got, want) {
		t.Fatalf("the window was sent %q, want %q", got, want)
	}
}

// TestAWindowCannotSizeAPaneBeyondWhatAScreenShows is the bound on what a
// window may claim. Every other window's terminal is made the size of the pty,
// with its scrollback, so one window saying 2000 by 2000 -- or 1 by 1 -- is
// every window's cost.
func TestAWindowCannotSizeAPaneBeyondWhatAScreenShows(t *testing.T) {
	v := &viewerSizes{panes: map[string]map[int64]viewerState{}}
	v.set("p", 1, 2000, 2000)
	if c, r := v.size("p"); c != maxViewCols || r != maxViewRows {
		t.Fatalf("a window claiming 2000x2000 sized the pane %dx%d, want %dx%d", c, r, maxViewCols, maxViewRows)
	}
	v.set("p", 2, 1, 1)
	v.set("p", 3, 2, 80)
	if c, r := v.size("p"); c != maxViewCols || r != maxViewRows {
		t.Fatalf("windows measuring 1x1 and 2x80 took the pane to %dx%d", c, r)
	}
	if sizes, _ := v.snapshot("p"); len(sizes) != 1 {
		t.Fatalf("a size too small to run a pane at was recorded: %v", sizes)
	}
}

// TestWindowsDisagreeingAboutAPaneAreNoted is the diagnostic for the next time
// a pane is garbled: it says how many windows were showing it, what each
// measured and what the pty was set to, once per distinct state.
func TestWindowsDisagreeingAboutAPaneAreNoted(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	was := diagLog
	diagLog = func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() }
	t.Cleanup(func() { diagLog = was })

	pane := "disagree-pane-xyz"
	t.Cleanup(func() { viewers.drop(pane, 91); viewers.drop(pane, 92) })
	viewers.set(pane, 91, 100, 30)
	noteSizes(pane, 100, 30)
	viewers.set(pane, 92, 70, 40)
	noteSizes(pane, 70, 30)
	noteSizes(pane, 70, 30)

	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 {
		t.Fatalf("%d notes %q, want one for the one disagreement", len(lines), lines)
	}
	for _, want := range []string{"2 windows", "[100 30]", "[70 40]", "70x30"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("the note %q does not say %q", lines[0], want)
		}
	}
}
