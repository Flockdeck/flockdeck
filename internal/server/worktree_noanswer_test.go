package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRemovingAWorktreeWithoutACountRemovesNothing covers removing a worktree
// when the workspace could not say which panes are working in it. Since ask
// gives up on an answer that panics (52ab703), the count came back empty, and
// an empty count read as no agent being there -- so a forced removal went on
// and deleted the directory, where before it had merely hung.
func TestRemovingAWorktreeWithoutACountRemovesNothing(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	wt := filepath.Join(t.TempDir(), "idle")
	gitCmd(t, repo, "worktree", "add", "-b", "idle", wt)

	was := panesIn
	t.Cleanup(func() { panesIn = was })
	panesIn = func(*Server, []string) map[string]int { panic("the count went wrong") }

	c := &controlClient{out: make(chan []byte, 8)}
	srv.removeWorktree(c, "", wt, true)
	var note noticeMsg
	select {
	case raw := <-c.out:
		if err := json.Unmarshal(raw, &note); err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("removing the worktree was never answered")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("the worktree was removed without knowing whether an agent was working in it (told %q): %v", note.Text, err)
	}
	if !note.Error {
		t.Errorf("the refusal was %+v; want an error notice", note)
	}
}
