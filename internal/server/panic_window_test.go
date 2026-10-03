package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestAPanicIsToldOnlyToTheWindowThatAsked covers a panic in work done for one
// window. Every window was told of it -- the others had asked for nothing, and
// a phone reached through the relay was shown an error for a click made at the
// desk -- and its stack went only to the console, which a Flockdeck started
// from a shortcut does not have.
func TestAPanicIsToldOnlyToTheWindowThatAsked(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.conversations = func([]agent.Spec, string) ([]transcript.Conversation, error) {
		panic("a transcript nobody expected")
	}
	asking := dialControl(t, srv)
	nextState(t, asking, nil)
	other := dialControl(t, srv)
	nextState(t, other, nil)

	sendCmd(t, asking, command{Cmd: "conversations", Path: srv.activeRoot()})
	var note noticeMsg
	readUntil(t, asking, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "a transcript nobody expected") {
		t.Fatalf("the window that asked was told %+v, want the panic", note)
	}

	// The other window asks for something, and is answered with nothing about
	// the panic ahead of it.
	sendCmd(t, other, command{Cmd: "recents"})
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, data, err := other.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("waiting for the recent projects: %v", err)
		}
		var m noticeMsg
		_ = json.Unmarshal(data, &m)
		if m.Type == "notice" && strings.Contains(m.Text, "Something went wrong") {
			t.Errorf("a window that asked for nothing was told %q", m.Text)
		}
		if m.Type == "recents" {
			break
		}
	}

	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "error.log"))
	if err != nil || !strings.Contains(string(data), "a transcript nobody expected") || !strings.Contains(string(data), "goroutine ") {
		t.Errorf("error.log holds %q, %v; want the panic and its stack", data, err)
	}
}

// TestACommitThatPanicsStillAnswersThePanel covers the Changes panel's commit.
// Its buttons wait, disabled, until the working tree is sent again, and a
// commit that panicked sent nothing: they said "Committing…" until the panel
// was closed.
func TestACommitThatPanicsStillAnswersThePanel(t *testing.T) {
	old := commitReviewed
	commitReviewed = func(string, string, gitx.Reviewed) error { panic("a commit nobody expected") }
	t.Cleanup(func() { commitReviewed = old })

	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "commit", Path: srv.activeRoot(), Text: "a message", Files: []string{"a.txt"}})
	// The two answers are sent by two deferred calls in the commit's goroutine,
	// the tree first and the panic after, and the tree takes however long
	// reading it takes. Which of the two a window gets first is not the
	// commit's to promise, so each is waited for wherever it falls: reading for
	// the notice and then for the tree dropped the tree whenever it came first,
	// and then waited for it until the test gave up.
	var note noticeMsg
	var sawNote, sawTree bool
	for deadline := time.Now().Add(20 * time.Second); !(sawNote && sawTree); {
		if time.Now().After(deadline) {
			t.Fatalf("a commit that panicked answered with a notice: %v, the changes: %v; want both", sawNote, sawTree)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control (a notice: %v, the changes: %v): %v", sawNote, sawTree, err)
		}
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(data, &probe)
		switch probe.Type {
		case "notice":
			var n noticeMsg
			_ = json.Unmarshal(data, &n)
			if n.Error && strings.Contains(n.Text, "a commit nobody expected") {
				note, sawNote = n, true
			}
		case "changes":
			sawTree = true
		}
	}
	if !note.Error || !strings.Contains(note.Text, "a commit nobody expected") {
		t.Errorf("a commit that panicked was told %+v, want the panic", note)
	}
}
