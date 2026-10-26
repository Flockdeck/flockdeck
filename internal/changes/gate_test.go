package changes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const gateHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func zero() *int { n := 0; return &n }

// readyInput is a branch with nothing in the way: pushed, one passing check at the
// head on a clean tree, a commit of its own, no comments or reviews.
func readyInput() GateInput {
	return GateInput{
		Branch:     Branch{Pane: "p1", Branch: "feat", Base: "main", HeadSha: gateHead, Upstream: "origin/feat", Ahead: zero()},
		Required:   []string{"unit"},
		Checks:     []Check{{Name: "unit", State: "pass", AtSha: gateHead}},
		OwnCommits: 2,
	}
}

func kinds(g Gate) string {
	var k []string
	for _, b := range g.Blockers {
		k = append(k, b.Kind)
	}
	return strings.Join(k, ",")
}

func TestAReadyBranchIsReadyAndApprovedOnlyAtTheApprovedHead(t *testing.T) {
	in := readyInput()
	if g := ComputeGate(in); g.State != "ready" || len(g.Blockers) != 0 || g.Blockers == nil {
		t.Fatalf("gate = %+v", g)
	}
	in.Approval = &Approval{Branch: "feat", Sha: gateHead, By: "you", At: "2026-10-26T09:00:00Z"}
	g := ComputeGate(in)
	if g.State != "approved" || g.ApprovedSha != gateHead || g.ApprovedBy != "you" {
		t.Errorf("approved = %+v", g)
	}
	in.Approval.Sha = strings.Repeat("b", 40)
	if g := ComputeGate(in); g.State != "ready" || g.ApprovedSha != "" {
		t.Errorf("an approval of another head: %+v", g)
	}
}

func TestEachBlockerBlocksAndAnApprovalDoesNotOutliveOne(t *testing.T) {
	approval := &Approval{Branch: "feat", Sha: gateHead, By: "you", At: "2026-10-26T09:00:00Z"}
	for name, c := range map[string]struct {
		edit func(*GateInput)
		kind string
		text string
	}{
		"failing check":      {func(in *GateInput) { in.Checks[0].State = "fail" }, "checks", "unit failed"},
		"running check":      {func(in *GateInput) { in.Checks[0].State = "running" }, "checks", "unit is running"},
		"queued check":       {func(in *GateInput) { in.Checks[0] = Check{Name: "unit", State: "queued", AtSha: gateHead} }, "checks", "has not run at this head"},
		"check of old head":  {func(in *GateInput) { in.Checks[0].AtSha = strings.Repeat("c", 40) }, "checks", "has not run at this head"},
		"check never run":    {func(in *GateInput) { in.Checks = nil }, "checks", "unit has not run"},
		"pass on dirty tree": {func(in *GateInput) { in.Checks[0].Worktree = true }, "checks", "uncommitted files"},
		"another branch": {func(in *GateInput) {
			in.Branch.Files = []File{{Path: "cfg/x.yaml", ConflictWith: []ConflictWith{{Pane: "p2", Path: "cfg/x.yaml"}}}}
		}, "conflict", "p2 also changes cfg/x.yaml"},
		"does not merge":   {func(in *GateInput) { in.Unmergeable = true }, "conflict", "does not merge into main"},
		"review asks":      {func(in *GateInput) { in.Reviews = []Review{{Reviewer: "p3", State: "changes"}} }, "review", "p3 asked for changes"},
		"open comment":     {func(in *GateInput) { in.Comments = []Comment{{State: "open"}, {State: "resolved"}} }, "comments", "1 comment is open"},
		"open comments":    {func(in *GateInput) { in.Comments = []Comment{{State: "open"}, {State: "open"}} }, "comments", "2 comments are open"},
		"no upstream":      {func(in *GateInput) { in.Branch.Upstream = "" }, "unpushed", "no upstream"},
		"unpushed commits": {func(in *GateInput) { n := 2; in.Branch.Ahead = &n }, "unpushed", "2 commits are not on origin/feat"},
		"empty":            {func(in *GateInput) { in.OwnCommits = 0 }, "empty", "no commits of its own"},
	} {
		in := readyInput()
		in.Approval = approval
		c.edit(&in)
		g := ComputeGate(in)
		if g.State != "blocked" || kinds(g) != c.kind || !strings.Contains(g.Words(), c.text) || g.ApprovedSha != "" {
			t.Errorf("%s: %+v, want blocked by %s with %q and no approval shown", name, g, c.kind, c.text)
		}
	}
	// A stale review, a fixed or dismissed comment, an unknown commit count and a
	// closed check list do not block.
	in := readyInput()
	in.Reviews = []Review{{Reviewer: "p3", State: "changes", Stale: true}, {Reviewer: "p4", State: "approved"}}
	in.Comments = []Comment{{State: "fixed"}, {State: "dismissed"}}
	in.OwnCommits = -1
	if g := ComputeGate(in); g.State != "ready" {
		t.Errorf("non-blocking things blocked: %+v", g)
	}
}

func TestWithNoCheckFileOnlyTheResultsThatExistAreHeld(t *testing.T) {
	in := readyInput()
	in.Required, in.Checks = nil, nil
	if g := ComputeGate(in); g.State != "ready" {
		t.Errorf("no checks at all: %+v", g)
	}
	in.Checks = []Check{{Name: "unit", State: "fail", AtSha: gateHead}}
	if g := ComputeGate(in); g.State != "blocked" || kinds(g) != "checks" {
		t.Errorf("a failing result with no file: %+v", g)
	}
}

func TestAMergedHeadIsMergedWhateverElseIsTrue(t *testing.T) {
	in := readyInput()
	in.OwnCommits = 0
	in.Merged = &Merge{Branch: "feat", Sha: gateHead, MergeSha: strings.Repeat("d", 40), At: "2026-10-26T09:05:00Z"}
	g := ComputeGate(in)
	if g.State != "merged" || g.MergeSha != strings.Repeat("d", 40) || len(g.Blockers) != 0 {
		t.Errorf("merged = %+v", g)
	}
}

func TestTheRefusalOfAnApprovalIsTheFirstBlockerInTheDocumentsOrder(t *testing.T) {
	in := readyInput()
	in.OwnCommits = 0
	in.Branch.Upstream = ""
	in.Comments = []Comment{{State: "open"}}
	in.Reviews = []Review{{Reviewer: "p3", State: "changes"}}
	in.Checks[0].State = "fail"
	in.Unmergeable = true
	var codes []string
	for range 6 {
		g := ComputeGate(in)
		code, text := GateRefusal(g)
		if code == "" || text == "" {
			break
		}
		codes = append(codes, code)
		// Remove the blocker that was reported and look again.
		switch code {
		case "checksNotPassing":
			in.Checks[0].State = "pass"
		case "conflicts":
			in.Unmergeable = false
		case "openComments":
			in.Comments = nil
		case "reviewChangesRequested":
			in.Reviews = nil
		case "unpushed":
			in.Branch.Upstream = "origin/feat"
		case "empty":
			in.OwnCommits = 1
		}
	}
	if got := strings.Join(codes, ","); got != "checksNotPassing,conflicts,openComments,reviewChangesRequested,unpushed,empty" {
		t.Errorf("order = %s", got)
	}
	if code, _ := GateRefusal(ComputeGate(readyInput())); code != "" {
		t.Errorf("a ready branch was refused: %s", code)
	}
}

func TestApprovalsAreOnePerBranchAndAMergeDropsTheApproval(t *testing.T) {
	var st GateState
	st.Approve("feat", gateHead, "you", "2026-10-26T09:00:00Z")
	st.Approve("feat", strings.Repeat("b", 40), "you", "2026-10-26T09:01:00Z")
	st.Approve("other", gateHead, "you", "2026-10-26T09:02:00Z")
	if len(st.Approvals) != 2 || st.ApprovalOf("feat").Sha != strings.Repeat("b", 40) {
		t.Fatalf("approvals = %+v", st.Approvals)
	}
	if !st.Unapprove("other") || st.Unapprove("other") || st.ApprovalOf("other") != nil {
		t.Error("unapprove")
	}
	st.RecordMerge("feat", strings.Repeat("b", 40), strings.Repeat("e", 40), "2026-10-26T09:03:00Z")
	if st.ApprovalOf("feat") != nil || st.MergeOf("feat", strings.Repeat("b", 40)) == nil || st.MergeOf("feat", gateHead) != nil {
		t.Errorf("state = %+v", st)
	}
}

func TestLoadGateCleansAndKeepsADamagedCopy(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	p := filepath.Join(t.TempDir(), "g.json")
	body := `{"approvals":[
		{"branch":"feat","sha":"` + gateHead + `","by":"you","at":"` + now + `","extra":1},
		{"branch":"","sha":"` + gateHead + `","by":"you","at":"` + now + `"},
		{"branch":"b","sha":"abc","by":"you","at":"` + now + `"},
		{"branch":"c","sha":"` + gateHead + `","by":"you","at":"yesterday"}],
	 "merges":[{"branch":"feat","sha":"` + gateHead + `","mergeSha":"` + gateHead + `","at":"` + now + `"},
		{"branch":"feat","sha":"` + gateHead + `","mergeSha":"short","at":"` + now + `"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := LoadGate(p)
	if err != nil || len(st.Approvals) != 1 || len(st.Merges) != 1 {
		t.Fatalf("loaded %+v, %v", st, err)
	}
	if err := st.Save(p); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(p); strings.Contains(string(raw), "extra") {
		t.Errorf("an unknown field was kept: %s", raw)
	}
	if got, err := LoadGate(filepath.Join(t.TempDir(), "none.json")); err != nil || len(got.Approvals) != 0 {
		t.Errorf("a missing file: %+v, %v", got, err)
	}
	if err := os.WriteFile(p, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGate(p); err == nil || !strings.Contains(err.Error(), ".bad") {
		t.Errorf("a damaged file: %v", err)
	}
}
