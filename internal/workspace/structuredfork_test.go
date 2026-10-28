package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/structured"
)

// A fork pane opens the forked session of its source, sits beside it, and does
// not fork again when it is restarted.
func TestAForkPaneOpensTheForkedSessionOnce(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	useFakeAgentWith(ws, "rewind")
	src := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, src, session.StatusIdle)
	rt := ws.StructuredRuntime(src)
	if err := rt.Send("plain second"); err != nil {
		t.Fatal(err)
	}
	var entry string
	deadline := time.Now().Add(20 * time.Second)
	for entry == "" {
		sn := rt.Snapshot()
		for _, e := range sn.Entries {
			for _, id := range sn.RewindPoints {
				if e.Kind == transcript.KindPrompt && e.Text == "plain second" && e.ID == id {
					entry = e.ID
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the second prompt never became a rewind point")
		}
		time.Sleep(25 * time.Millisecond)
	}
	from, err := rt.ForkSource(entry, rt.RewindToken())
	if err != nil {
		t.Fatal(err)
	}
	fork, err := ws.SpawnFork(src, from)
	if err != nil {
		t.Fatalf("SpawnFork: %v", err)
	}
	p := ws.Pane(fork)
	if p == nil || p.Label != ForkLabel {
		t.Fatalf("fork pane = %+v", p)
	}
	together := false
	for _, tab := range ws.Tabs {
		if tab.Tree.Find(src) != nil && tab.Tree.Find(fork) != nil {
			together = true
		}
	}
	if !together {
		t.Error("the fork is not beside the original")
	}
	statusWithin(t, ws, fork, session.StatusIdle)
	got := ws.StructuredRuntime(fork).Snapshot().SessionID
	if !strings.HasPrefix(got, "fork-of-") {
		t.Fatalf("the fork's session is %q, not a fork", got)
	}
	if p.forkFrom != nil {
		t.Error("the fork source is still held after the pane started")
	}
	restartAndWait(t, ws, fork)
	if again := ws.StructuredRuntime(fork).Snapshot().SessionID; strings.HasPrefix(again, "fork-of-") {
		t.Errorf("a restart forked again: %q", again)
	}
	if _, err := ws.SpawnFork("nope", from); err == nil {
		t.Error("a fork of a pane that is not there was made")
	}
}

// A pane keeps at most maxForks forks open; the next one is refused, and
// closing one makes room.
func TestAPaneHasAtMostFourForksOpen(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	useFakeAgentWith(ws, "rewind")
	src := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, src, session.StatusIdle)
	from := structured.ForkFrom{Session: "fork-of-x"}
	var forks []string
	for i := 0; i < maxForks; i++ {
		id, err := ws.SpawnFork(src, from)
		if err != nil {
			t.Fatalf("fork %d: %v", i+1, err)
		}
		forks = append(forks, id)
	}
	if _, err := ws.SpawnFork(src, from); !errors.Is(err, ErrTooManyForks) {
		t.Fatalf("fork %d: err = %v, want ErrTooManyForks", maxForks+1, err)
	}
	if !ws.ClosePaneByID(forks[0]) {
		t.Fatal("could not close a fork")
	}
	if _, err := ws.SpawnFork(src, from); err != nil {
		t.Errorf("after closing one: %v", err)
	}
}
