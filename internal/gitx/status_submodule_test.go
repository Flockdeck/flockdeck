package gitx

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPaneHeadersLeaveWorkInsideASubmoduleToTheReviewPanel covers the status
// the pane headers read every refresh. Finding uncommitted work inside a
// submodule costs a git status of its own inside every submodule, every time,
// for work that cannot be committed from the repository around it; the review
// panel still counts it. A submodule moved to another commit is the outer
// repository's change, and the headers still count that.
func TestPaneHeadersLeaveWorkInsideASubmoduleToTheReviewPanel(t *testing.T) {
	inner := newRepo(t)
	outer := newRepo(t)
	add := exec.Command("git", "-c", "protocol.file.allow=always",
		"submodule", "add", "--", filepath.ToSlash(inner), "mod")
	add.Dir = outer
	if out, err := add.CombinedOutput(); err != nil {
		t.Skipf("submodules are not usable here: %v: %s", err, out)
	}
	gitRun(t, outer, "commit", "-m", "add the submodule")
	mod := filepath.Join(outer, "mod")
	gitRun(t, mod, "config", "user.email", "test@example.com")
	gitRun(t, mod, "config", "user.name", "Test")
	gitRun(t, mod, "config", "commit.gpgsign", "false")

	write(t, mod, "README.md", "hello\nchanged inside the submodule\n")
	if st := StatusOf(outer); st.Dirty != 1 {
		t.Fatalf("the review panel's status is %+v, want the work inside the submodule counted", st)
	}
	st, err := StatusWithin(outer, commandTimeout)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Dirty != 0 || st.Untracked != 0 {
		t.Errorf("the pane header's status is %+v, want the work inside the submodule left out", st)
	}

	gitRun(t, mod, "commit", "-am", "work inside the submodule")
	if st, err := StatusWithin(outer, commandTimeout); err != nil || st.Dirty != 1 {
		t.Errorf("the pane header's status is %+v (%v) once the submodule moved on, want it counted", st, err)
	}
}
