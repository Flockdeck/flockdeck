package server

import (
	"encoding/json"
	"testing"
	"time"
)

// TestARefusedWorktreeChangeStillEndsOnAListing covers the worktree panel
// waiting on its listing when a change it asked for is refused. Adding,
// removing and pruning each number a listing of their own as they are asked
// for, which leaves any listing already on its way out of date -- and a
// refusal returned without sending one, so the panel was never answered and
// sat on what it had until it was opened again.
func TestARefusedWorktreeChangeStillEndsOnAListing(t *testing.T) {
	srv, _ := newTestServer(t)
	held, release := make(chan struct{}), make(chan struct{})
	var first bool
	was := readWorktrees
	t.Cleanup(func() { readWorktrees = was })
	readWorktrees = func(root string) worktreesMsg {
		// Only the panel's own listing is held back; a later one is not.
		if !first {
			first = true
			close(held)
			<-release
		}
		return worktreesMsg{Type: "worktrees", Root: root}
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.listWorktrees(c)
	<-held
	// No branch named: refused before git is asked anything.
	srv.addWorktree(c, "  ", "", "")
	close(release)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case raw := <-c.out:
			var msg struct{ Type string }
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatalf("got %s", raw)
			}
			if msg.Type == "worktrees" {
				return
			}
		case <-deadline:
			t.Fatal("the panel was never sent a listing after its change was refused")
		}
	}
}
