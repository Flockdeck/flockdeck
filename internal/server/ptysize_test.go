package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
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
