package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/changes"
	"github.com/jmwri/flockdeck/internal/gitx"
)

// The merge gate (docs/changes-api.md, section 7): approveMerge, unapproveMerge and
// mergeBranch. The gate of a branch is worked out on every push from what is known;
// what is kept in changes/<project>-<8 hex>.gate.json is only the person's
// decisions, an approval of one exact head and the heads that were merged. It is a
// process control among cooperating agents and not a boundary: an agent running as
// the user can still run git merge itself (section 6).

// gatePath is the gate file beside the review state at statePath.
func gatePath(statePath string) string {
	return strings.TrimSuffix(statePath, ".state.json") + ".gate.json"
}

// loadGate loads the gate file at path, with reviewsMu held. A damaged file is an
// empty state and one notice, and the commands that would write it refuse.
func (s *Server) loadGate(path string) changes.GateState {
	st, err := changes.LoadGate(path)
	if firstDamage(path, err) {
		s.notifyAllOr("Approvals are not shown: "+err.Error(), "The approval file is damaged; approvals are not shown", true)
	}
	return st
}

// gateOfBranch works out the gate of one branch read without error. It reads git
// for what the branch read does not hold, and takes the rest from what the push
// already has.
func (s *Server) gateOfBranch(ctx context.Context, b changes.Branch, required []string, checksErr string, st changes.GateState) changes.Gate {
	in := changes.GateInput{Branch: b, Required: required, ChecksError: checksErr, OwnCommits: -1}
	if b.Checks != nil {
		in.Checks = *b.Checks
	}
	if b.Reviews != nil {
		in.Reviews = *b.Reviews
	}
	if b.Comments != nil {
		in.Comments = *b.Comments
	}
	in.Approval = st.ApprovalOf(b.Branch)
	if m := st.MergeOf(b.Branch, b.HeadSha); m != nil && changes.MergedInto(ctx, b) {
		in.Merged = m
	} else {
		facts := s.facts.Facts(ctx, b)
		in.OwnCommits, in.Unmergeable = facts.OwnCommits, facts.Unmergeable
	}
	return changes.ComputeGate(in)
}

// withGates puts the gate in each branch of a team that has its checks, reviews and
// comments, and drops the approvals that no longer hold: one of another head, or of
// a branch that is blocked again. An approval is dropped only for a branch that was
// read, so a checkout that could not be read keeps what it had.
func (s *Server) withGates(t *changes.Team, root, project string) {
	checksPath, statePath, err := changesFiles(root, project)
	if err != nil {
		return
	}
	gp := gatePath(statePath)
	reviewsMu.Lock()
	st := s.loadGate(gp)
	reviewsMu.Unlock()

	var required []string
	var checksErr string
	if defs, err := changes.LoadChecks(checksPath); err == nil {
		required = []string{}
		for _, d := range defs {
			required = append(required, d.Name)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		checksErr = firstWords(err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), changesReadLimit)
	defer cancel()
	var stale []changes.Approval
	for i := range t.Branches {
		b := &t.Branches[i]
		if b.Error != "" || b.Checks == nil || b.Branch == "" {
			continue
		}
		g := s.gateOfBranch(ctx, *b, required, checksErr, st)
		b.Gate = &g
		if a := st.ApprovalOf(b.Branch); a != nil && g.ApprovedSha != a.Sha && g.State != "merged" {
			stale = append(stale, *a)
		}
	}
	if len(stale) > 0 {
		dropApprovals(gp, stale)
	}
}

// firstWords is a short, one-line form of an error for a blocker.
func firstWords(s string) string {
	return changes.CleanText(s, 120)
}

// dropApprovals removes the approvals listed, if the file still holds exactly them.
func dropApprovals(path string, list []changes.Approval) {
	reviewsMu.Lock()
	defer reviewsMu.Unlock()
	st, err := changes.LoadGate(path)
	if err != nil {
		return
	}
	changed := false
	for _, a := range list {
		if cur := st.ApprovalOf(a.Branch); cur != nil && cur.Sha == a.Sha {
			st.Unapprove(a.Branch)
			changed = true
		}
	}
	if changed {
		_ = st.Save(path)
	}
}

// mergeLocks has one lock for each repository, by its git directory, so that two
// merges into one repository never run together.
var mergeLocks sync.Map

func mergeLock(cwd string) *sync.Mutex {
	key := cwd
	if common, err := gitx.CommonDir(cwd); err == nil {
		key = common
	}
	l, _ := mergeLocks.LoadOrStore(key, &sync.Mutex{})
	return l.(*sync.Mutex)
}

// gateBranch reads the team of the pane's project the way a push does and returns
// the pane's branch with its gate, so that a command decides on exactly what a
// window is shown.
func (s *Server) gateBranch(c *controlClient, cmd command) (b changes.Branch, root, project string, ok bool) {
	if cmd.ID == "" {
		changesRefuse(c, cmd, "badRequest", "id is required")
		return b, "", "", false
	}
	if _, found := s.changesPaneCwd(c, cmd); !found {
		return b, "", "", false
	}
	root, project, found := s.projectOf(cmd.ID)
	if !found {
		changesRefuse(c, cmd, "noBranch", "no such pane")
		return b, "", "", false
	}
	t, open := ask(s, func() changesTarget { return s.changesTargetOf(root) })
	if !open {
		return b, "", "", false
	}
	if t.err != nil {
		changesRefuse(c, cmd, "noBranch", t.err.Error())
		return b, "", "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), changesReadLimit)
	defer cancel()
	team := changes.ReadTeam(ctx, t.root, t.project, t.panes, 0)
	s.withChecksAndReviews(&team, t.root, t.project)
	for _, br := range team.Branches {
		if br.Pane != cmd.ID {
			continue
		}
		switch {
		case br.Error != "":
			changesRefuse(c, cmd, "noBranch", br.Error)
		case br.Branch == "" || br.Gate == nil:
			changesRefuse(c, cmd, "noBranch", "the pane is not on a branch")
		case cmd.Branch != "" && cmd.Branch != br.Branch:
			changesRefuse(c, cmd, "noBranch", "the pane is on another branch")
		default:
			return br, t.root, t.project, true
		}
		return b, "", "", false
	}
	changesRefuse(c, cmd, "noBranch", "the pane's checkout was not read")
	return b, "", "", false
}

func gateResult(cmd command, g *changes.Gate) changesResultMsg {
	return changesResultMsg{Type: "changesResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Gate: g}
}

// changesApproveMerge answers approveMerge.
func (s *Server) changesApproveMerge(c *controlClient, cmd command) {
	if cmd.ExpectSha == "" {
		changesRefuse(c, cmd, "badRequest", "expectSha is required")
		return
	}
	b, root, project, ok := s.gateBranch(c, cmd)
	if !ok {
		return
	}
	if !sameSha(cmd.ExpectSha, b.HeadSha) {
		changesRefuse(c, cmd, "headMoved", fmt.Sprintf("%s has a new commit since you looked, so nothing was approved. Look at it again.", b.Branch))
		return
	}
	if b.Gate.State == "merged" {
		changesRefuse(c, cmd, "badRequest", "this head is already merged")
		return
	}
	if code, text := changes.GateRefusal(*b.Gate); code != "" {
		changesRefuse(c, cmd, code, text)
		return
	}
	_, statePath, err := changesFiles(root, project)
	if err != nil {
		changesRefuse(c, cmd, "badRequest", "the config directory cannot be found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	reviewsMu.Lock()
	st, err := changes.LoadGate(gatePath(statePath))
	if err == nil {
		st.Approve(b.Branch, b.HeadSha, "you", now)
		err = st.Save(gatePath(statePath))
	}
	reviewsMu.Unlock()
	if err != nil {
		changesRefuse(c, cmd, "badRequest", err.Error())
		return
	}
	g := *b.Gate
	g.State, g.ApprovedBy, g.ApprovedAt, g.ApprovedSha = "approved", "you", now, b.HeadSha
	s.tellManager(root, fmt.Sprintf("The person approved %s at %s for merge into %s. Nothing is merged yet; they or you can run mergeBranch.",
		changes.CleanText(b.Branch, 200), shortSha(b.HeadSha), changes.CleanText(b.Base, 200)))
	c.sendJSON(gateResult(cmd, &g))
}

// changesUnapproveMerge answers unapproveMerge. Withdrawing an approval that is not
// there is not an error.
func (s *Server) changesUnapproveMerge(c *controlClient, cmd command) {
	b, root, project, ok := s.gateBranch(c, cmd)
	if !ok {
		return
	}
	_, statePath, err := changesFiles(root, project)
	if err != nil {
		changesRefuse(c, cmd, "badRequest", "the config directory cannot be found")
		return
	}
	reviewsMu.Lock()
	st, err := changes.LoadGate(gatePath(statePath))
	if err == nil && st.Unapprove(b.Branch) {
		err = st.Save(gatePath(statePath))
	}
	reviewsMu.Unlock()
	if err != nil {
		changesRefuse(c, cmd, "badRequest", err.Error())
		return
	}
	g := *b.Gate
	if g.State == "approved" {
		g.State, g.ApprovedBy, g.ApprovedAt, g.ApprovedSha = "ready", "", "", ""
	}
	c.sendJSON(gateResult(cmd, &g))
}

// changesMergeBranch answers mergeBranch. It merges the exact commit that was
// approved, and only when the gate says that head is approved now.
func (s *Server) changesMergeBranch(c *controlClient, cmd command) {
	if cmd.ExpectSha == "" {
		changesRefuse(c, cmd, "badRequest", "expectSha is required")
		return
	}
	cwd, ok := s.changesPaneCwd(c, cmd)
	if !ok {
		return
	}
	// One merge at a time in a repository, from the look at the head to the end.
	lock := mergeLock(cwd)
	lock.Lock()
	defer lock.Unlock()

	b, root, project, ok := s.gateBranch(c, cmd)
	if !ok {
		return
	}
	if !sameSha(cmd.ExpectSha, b.HeadSha) {
		changesRefuse(c, cmd, "headMoved", fmt.Sprintf("%s has a new commit since it was approved, so nothing was merged.", b.Branch))
		return
	}
	if b.Gate.State != "approved" || b.Gate.ApprovedSha != b.HeadSha || !fullSha(b.HeadSha) {
		changesRefuse(c, cmd, "notApproved", "This head is not approved, or something has blocked it since: "+gateWhy(b.Gate))
		return
	}
	_, statePath, err := changesFiles(root, project)
	if err != nil {
		changesRefuse(c, cmd, "badRequest", "the config directory cannot be found")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if !gitx.BranchExists(cwd, b.Base) {
		changesRefuse(c, cmd, "notClean", fmt.Sprintf("%s is not a local branch, so there is nothing to merge into", changes.CleanText(b.Base, 200)))
		return
	}
	baseDir, found := gitx.CheckoutOf(ctx, cwd, b.Base)
	if !found {
		changesRefuse(c, cmd, "notClean", fmt.Sprintf("%s is not checked out in any worktree; check it out in one first", changes.CleanText(b.Base, 200)))
		return
	}
	msg := fmt.Sprintf("Merge branch %q into %s", changes.CleanText(b.Branch, 200), changes.CleanText(b.Base, 200))
	tip, err := gitx.MergeInto(ctx, baseDir, b.Base, b.HeadSha, msg)
	switch {
	case errors.Is(err, gitx.ErrNotClean), errors.Is(err, gitx.ErrWrongBranch):
		changesRefuse(c, cmd, "notClean", "The checkout of "+changes.CleanText(b.Base, 200)+" has uncommitted changes, an operation in progress, or is on another branch. Nothing was merged.")
		return
	case errors.Is(err, gitx.ErrNotFastForward):
		changesRefuse(c, cmd, "notFastForward", changes.CleanText(b.Base, 200)+" has moved in a way that neither a fast-forward nor a clean merge can take. Nothing was merged.")
		return
	case err != nil:
		changesRefuse(c, cmd, "badRequest", "The merge failed: "+firstWords(err.Error()))
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	reviewsMu.Lock()
	st, lerr := changes.LoadGate(gatePath(statePath))
	if lerr == nil {
		st.RecordMerge(b.Branch, b.HeadSha, tip, now)
		lerr = st.Save(gatePath(statePath))
	}
	reviewsMu.Unlock()
	s.tellManager(root, fmt.Sprintf("Merged %s at %s into %s; %s is now at %s.",
		changes.CleanText(b.Branch, 200), shortSha(b.HeadSha), changes.CleanText(b.Base, 200), changes.CleanText(b.Base, 200), shortSha(tip)))
	if lerr != nil {
		changesRefuse(c, cmd, "badRequest", "The branch was merged, but the merge could not be recorded: "+lerr.Error())
		return
	}
	g := changes.Gate{State: "merged", Blockers: []changes.Blocker{}, MergeSha: tip}
	c.sendJSON(gateResult(cmd, &g))
}

func fullSha(s string) bool { return len(s) == 40 || len(s) == 64 }

func gateWhy(g *changes.Gate) string {
	if w := g.Words(); w != "" {
		return w
	}
	return "it needs the person's approval"
}

// tellManager sends a message from Flockdeck to the project's manager, if it has
// one. A missing manager or a full inbox is not an error: the gate does not depend
// on being heard.
func (s *Server) tellManager(root, text string) {
	ask(s, func() bool {
		members, err := s.ws.TeamMembers(root)
		if err != nil {
			return false
		}
		for _, m := range members {
			if m.Role == "manager" {
				_ = s.ws.FlockdeckMessage(root, m.Pane, text)
				return true
			}
		}
		return false
	})
}
