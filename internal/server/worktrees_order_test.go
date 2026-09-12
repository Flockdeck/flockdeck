package server

import (
	"encoding/json"
	"testing"
	"time"
)

// TestAWorktreeListingDoesNotLandOnTopOfANewerOne covers the worktree panel
// asked for, the project switched, and the panel asked for again before the
// first listing has finished. A repository's listing is a git command per
// worktree: the first project's landed on top of the second's, the panel drew
// whichever came last, and a row's Agent or Shell then started work in the
// project nobody was looking at.
func TestAWorktreeListingDoesNotLandOnTopOfANewerOne(t *testing.T) {
	srv, ws := newTestServer(t)
	first, second := ws.ActiveRoot(), t.TempDir()
	if _, ok := ask(srv, func() bool {
		if err := ws.OpenProject(second); err != nil {
			t.Error(err)
		}
		ws.SelectProject(first)
		return true
	}); !ok {
		t.Fatal("server closed")
	}

	held, release := make(chan struct{}), make(chan struct{})
	was := readWorktrees
	t.Cleanup(func() { readWorktrees = was })
	readWorktrees = func(root string) worktreesMsg {
		if root == first {
			close(held)
			<-release
		}
		return worktreesMsg{Type: "worktrees", Root: root}
	}
	listed := func(c *controlClient, within time.Duration) (string, bool) {
		t.Helper()
		select {
		case raw := <-c.out:
			var msg worktreesMsg
			if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "worktrees" {
				t.Fatalf("got %s; want a listing", raw)
			}
			return msg.Root, true
		case <-time.After(within):
			return "", false
		}
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.listWorktrees(c)
	<-held
	ask(srv, func() bool { ws.SelectProject(second); return true })
	srv.listWorktrees(c)
	if got, ok := listed(c, 5*time.Second); !ok || got != second {
		t.Fatalf("the second project's listing was %q (arrived: %v); want %q", got, ok, second)
	}
	close(release)
	if got, ok := listed(c, 500*time.Millisecond); ok {
		t.Fatalf("the first project's listing (%q) landed after the second's", got)
	}
}
