package server

import (
	"context"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/changes"
	"github.com/jmwri/flockdeck/internal/gitx"
)

const gateNow = "2026-10-26T09:00:00Z"

// pushedBranch is the branch of commentRepo as if it had an upstream that holds its
// head, so that only what a test changes can block it.
func pushedBranch(t *testing.T) (changes.Branch, string) {
	t.Helper()
	w, _ := commentRepo(t)
	b := w.branch
	zero := 0
	b.Upstream, b.Ahead = "origin/"+b.Branch, &zero
	return b, w.cwd
}

func TestTheGateOfARealBranchIsReadyApprovedBlockedAndMerged(t *testing.T) {
	srv := &Server{}
	ctx := context.Background()
	b, cwd := pushedBranch(t)
	var st changes.GateState

	if g := srv.gateOfBranch(ctx, b, nil, "", st); g.State != "ready" || len(g.Blockers) != 0 {
		t.Fatalf("a pushed branch with a commit and no checks: %+v", g)
	}
	st.Approve(b.Branch, b.HeadSha, "you", gateNow)
	if g := srv.gateOfBranch(ctx, b, nil, "", st); g.State != "approved" || g.ApprovedSha != b.HeadSha {
		t.Errorf("approved: %+v", g)
	}
	// An approval of another head is not an approval of this one.
	other := changes.GateState{}
	other.Approve(b.Branch, strings.Repeat("a", 40), "you", gateNow)
	if g := srv.gateOfBranch(ctx, b, nil, "", other); g.State != "ready" {
		t.Errorf("an approval of another head: %+v", g)
	}
	// A check the project defines that has not run blocks, and so does a check file
	// that cannot be read.
	if g := srv.gateOfBranch(ctx, b, []string{"unit"}, "", st); g.State != "blocked" || !strings.Contains(g.Words(), "unit has not run") || g.ApprovedSha != "" {
		t.Errorf("a check that has not run: %+v", g)
	}
	if g := srv.gateOfBranch(ctx, b, nil, "the check file is not valid JSON", st); g.State != "blocked" {
		t.Errorf("an unreadable check file: %+v", g)
	}
	// A pass that ran on uncommitted files does not count.
	dirtyPass := []changes.Check{{Name: "unit", State: "pass", AtSha: b.HeadSha, Worktree: true}}
	bd := b
	bd.Checks = &dirtyPass
	if g := srv.gateOfBranch(ctx, bd, []string{"unit"}, "", st); g.State != "blocked" {
		t.Errorf("a worktree pass counted: %+v", g)
	}
	// A branch with no commit of its own is empty.
	empty := b
	empty.BaseSha = b.HeadSha
	if g := srv.gateOfBranch(ctx, empty, nil, "", st); g.State != "blocked" || !strings.Contains(g.Words(), "no commits of its own") {
		t.Errorf("an empty branch: %+v", g)
	}

	// Merged: the recorded merge counts only once the head is in the base.
	st.RecordMerge(b.Branch, b.HeadSha, strings.Repeat("e", 40), gateNow)
	if g := srv.gateOfBranch(ctx, b, nil, "", st); g.State == "merged" {
		t.Errorf("a head that is not in the base is merged: %+v", g)
	}
	gitCmd(t, cwd, "checkout", "-q", "main")
	tip, err := gitx.MergeInto(ctx, cwd, "main", b.HeadSha, "merge")
	if err != nil {
		t.Fatal(err)
	}
	if g := srv.gateOfBranch(ctx, b, nil, "", st); g.State != "merged" || g.MergeSha != strings.Repeat("e", 40) || tip != b.HeadSha {
		t.Errorf("merged: %+v, tip %s", g, tip)
	}
}

func TestAnApprovalThatNoLongerHoldsIsDroppedFromTheFile(t *testing.T) {
	srv, _ := newTestServer(t)
	b, _ := pushedBranch(t)
	_, statePath, err := changesFiles("r", "p")
	if err != nil {
		t.Fatal(err)
	}
	path := gatePath(statePath)
	moved := strings.Repeat("a", 40)
	var st changes.GateState
	st.Approve(b.Branch, moved, "you", gateNow)    // of a head that is not this one
	st.Approve("elsewhere", moved, "you", gateNow) // of a branch the team does not hold
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	checks, reviews, comments := []changes.Check{}, []changes.Review{}, []changes.Comment{}
	b.Checks, b.Reviews, b.Comments = &checks, &reviews, &comments
	unread := changes.Branch{Pane: "p9", Error: "the directory is gone"}
	team := changes.Team{Branches: []changes.Branch{b, unread}}
	srv.withGates(&team, "r", "p")

	if team.Branches[0].Gate == nil || team.Branches[0].Gate.State != "ready" {
		t.Errorf("gate = %+v", team.Branches[0].Gate)
	}
	if team.Branches[1].Gate != nil {
		t.Errorf("a checkout that could not be read has a gate: %+v", team.Branches[1].Gate)
	}
	got, err := changes.LoadGate(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovalOf(b.Branch) != nil {
		t.Error("the approval of another head was kept")
	}
	if got.ApprovalOf("elsewhere") == nil {
		t.Error("the approval of a branch that was not read was dropped")
	}

	// An approval of this head is kept while nothing blocks, and dropped when
	// something does.
	st.Approve(b.Branch, b.HeadSha, "you", gateNow)
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	team = changes.Team{Branches: []changes.Branch{b}}
	srv.withGates(&team, "r", "p")
	if g := team.Branches[0].Gate; g == nil || g.State != "approved" {
		t.Fatalf("gate = %+v", g)
	}
	if got, _ := changes.LoadGate(path); got.ApprovalOf(b.Branch) == nil {
		t.Fatal("a good approval was dropped")
	}
	open := []changes.Comment{{State: "open"}}
	b.Comments = &open
	team = changes.Team{Branches: []changes.Branch{b}}
	srv.withGates(&team, "r", "p")
	if g := team.Branches[0].Gate; g == nil || g.State != "blocked" {
		t.Fatalf("gate = %+v", g)
	}
	if got, _ := changes.LoadGate(path); got.ApprovalOf(b.Branch) != nil {
		t.Error("an approval outlived a blocker")
	}
}

func TestGateCommandsAreRefusedToARelayWindowAndToAConnectionThatIsNotAPage(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, name := range []string{"approveMerge", "unapproveMerge", "mergeBranch"} {
		cl := &controlClient{out: make(chan []byte, 8), remote: true, device: "d"}
		srv.handleCommand(cl, command{Cmd: name, Req: "r", ID: "p1", ExpectSha: "abcdef0"})
		if m := answerOf(t, cl); m.Code != "deskOnly" || m.Op != name {
			t.Errorf("%s from a relay window: %+v", name, m)
		}
		cl = &controlClient{out: make(chan []byte, 8)}
		srv.handleCommand(cl, command{Cmd: name, Req: "r", ID: "p1", ExpectSha: "abcdef0"})
		if m := answerOf(t, cl); m.Code != "deskOnly" || m.Op != name {
			t.Errorf("%s from a connection that is not a page: %+v", name, m)
		}
		cl = &controlClient{out: make(chan []byte, 8), page: true}
		srv.handleCommand(cl, command{Cmd: name, Req: "r", ID: "nope", ExpectSha: "abcdef0"})
		if m := answerOf(t, cl); m.OK || (m.Code != "noBranch" && m.Code != "badRequest") {
			t.Errorf("%s for no pane: %+v", name, m)
		}
	}
	// Both commands that act on a head need to be told which one.
	for _, name := range []string{"approveMerge", "mergeBranch"} {
		cl := &controlClient{out: make(chan []byte, 8), page: true}
		srv.handleCommand(cl, command{Cmd: name, Req: "r", ID: "p1"})
		if m := answerOf(t, cl); m.Code != "badRequest" || !strings.Contains(m.Error, "expectSha") {
			t.Errorf("%s without expectSha: %+v", name, m)
		}
	}
}

func TestOneLockForEveryCheckoutOfARepository(t *testing.T) {
	w, _ := commentRepo(t)
	if mergeLock(w.cwd) != mergeLock(w.cwd) {
		t.Error("two merges into one repository do not share a lock")
	}
	other, _ := commentRepo(t)
	if mergeLock(w.cwd) == mergeLock(other.cwd) {
		t.Error("two repositories share a lock")
	}
}
