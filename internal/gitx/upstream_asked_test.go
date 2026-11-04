package gitx

import "testing"

// A branch with no upstream is an answer of "" and no error, which is what
// lets Push set one. Only a lookup that timed out returns an error.
func TestUpstreamAskedAnswersForABranchWithNoUpstream(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	if up, err := upstreamAsked(repo); up != "" || err != nil {
		t.Errorf("upstreamAsked = %q, %v", up, err)
	}
	gitRun(t, repo, "branch", "other")
	gitRun(t, repo, "branch", "--set-upstream-to=other")
	if up, err := upstreamAsked(repo); up != "other" || err != nil {
		t.Errorf("upstreamAsked = %q, %v", up, err)
	}
}
