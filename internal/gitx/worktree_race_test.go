package gitx

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// TestWorktreesAddedAtTheSameTimeDoNotFailEachOther covers a fan-out, which
// makes its worktrees side by side. Each git worktree add reads what every
// other one is writing under .git/worktrees, and on macOS one that read
// another's half-written record stopped with "failed to read
// .git/worktrees/<name>/commondir" and made nothing, about once in a few
// hundred fan-outs.
func TestWorktreesAddedAtTheSameTimeDoNotFailEachOther(t *testing.T) {
	repo := newRepo(t)
	const rounds, together = 15, 6
	for round := range rounds {
		base := t.TempDir()
		errs := make([]error, together)
		var wg sync.WaitGroup
		for i := range together {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = AddNewBranch(repo, filepath.Join(base, fmt.Sprintf("wt%d", i)), fmt.Sprintf("agent/r%d-%d", round, i))
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("round %d, worktree %d: %v", round, i, err)
			}
		}
	}
}

// TestALostWorktreeRaceIsToldFromAnyOtherFailure checks what is retried: git's
// own words for reading another worktree's record, and nothing else.
func TestALostWorktreeRaceIsToldFromAnyOtherFailure(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{errors.New("git worktree add: Preparing worktree (new branch 'agent/fix')\nfailed to read .git/worktrees/001-agent-fix-2-2/commondir: Undefined error: 0"), true},
		{errors.New("git worktree add: fatal: '/tmp/wt' already exists"), false},
		{errors.New("git worktree add: fatal: a branch named 'agent/fix' already exists"), false},
		{errors.New("failed to read config"), false},
		{nil, false},
	} {
		if got := lostWorktreeRace(tc.err); got != tc.want {
			t.Errorf("lostWorktreeRace(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
