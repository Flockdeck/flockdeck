package changes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// The merge gate (docs/changes-api.md, section 7). A branch's gate is derived from
// what is known about it: its checks, conflicts, reviews, comments, upstream and
// commits. What is kept is only the person's decisions: which head they approved,
// and which heads Flockdeck merged.

// Blocker is one reason a branch cannot be merged yet.
type Blocker struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Gate is where a branch stands against the merge gate.
type Gate struct {
	State       string    `json:"state"`
	Blockers    []Blocker `json:"blockers"`
	ApprovedBy  string    `json:"approvedBy,omitempty"`
	ApprovedAt  string    `json:"approvedAt,omitempty"`
	ApprovedSha string    `json:"approvedSha,omitempty"`
	MergeSha    string    `json:"mergeSha,omitempty"`
}

// Approval is the person's approval of one head of a branch.
type Approval struct {
	Branch string `json:"branch"`
	Sha    string `json:"sha"`
	By     string `json:"by"`
	At     string `json:"at"`
}

// Merge is a head that Flockdeck merged into the base, and the base's tip after.
type Merge struct {
	Branch   string `json:"branch"`
	Sha      string `json:"sha"`
	MergeSha string `json:"mergeSha"`
	At       string `json:"at"`
}

// GateState is the file of decisions. Only Flockdeck writes it.
type GateState struct {
	Approvals []Approval `json:"approvals"`
	Merges    []Merge    `json:"merges"`
}

var fullShaRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

const (
	maxGateEntries = 500
	gateFileLimit  = 1 << 20
)

// LoadGate reads the file at path and cleans it, as LoadReviews does: a missing
// file is an empty state, one that cannot be read or is not valid is an error with
// a copy kept beside it, and entries with a bad shape are dropped.
func LoadGate(path string) (GateState, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return GateState{}, nil
	}
	if err != nil {
		return GateState{}, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(f, gateFileLimit+1)); err != nil {
		return GateState{}, err
	}
	var raw GateState
	if buf.Len() > gateFileLimit || json.Unmarshal(buf.Bytes(), &raw) != nil {
		kept := ""
		if buf.Len() <= gateFileLimit {
			kept = keepBadCopy(path, buf.Bytes())
		}
		if kept == "" {
			return GateState{}, errors.New("the approval file is damaged; no copy of it could be kept")
		}
		return GateState{}, errors.New("the approval file is damaged; a copy is kept as " + kept)
	}
	var out GateState
	for _, a := range raw.Approvals {
		a.Branch, a.By = cleanLine(a.Branch, 256), cleanLine(a.By, 64)
		if a.Branch == "" || a.By == "" || !fullShaRe.MatchString(a.Sha) || !validTime(a.At) {
			continue
		}
		out.Approvals = append(out.Approvals, a)
	}
	for _, m := range raw.Merges {
		m.Branch = cleanLine(m.Branch, 256)
		if m.Branch == "" || !fullShaRe.MatchString(m.Sha) || !fullShaRe.MatchString(m.MergeSha) || !validTime(m.At) {
			continue
		}
		out.Merges = append(out.Merges, m)
	}
	out.Approvals = lastN(out.Approvals, maxGateEntries)
	out.Merges = lastN(out.Merges, maxGateEntries)
	return out, nil
}

func lastN[T any](s []T, n int) []T {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func validTime(s string) bool {
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

// Save writes the state atomically.
func (s GateState) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// ApprovalOf is the approval recorded for branch, if there is one.
func (s GateState) ApprovalOf(branch string) *Approval {
	for i := range s.Approvals {
		if s.Approvals[i].Branch == branch {
			return &s.Approvals[i]
		}
	}
	return nil
}

// MergeOf is the recorded merge of exactly this head of branch, if there is one.
func (s GateState) MergeOf(branch, sha string) *Merge {
	for i := len(s.Merges) - 1; i >= 0; i-- {
		if s.Merges[i].Branch == branch && s.Merges[i].Sha == sha {
			return &s.Merges[i]
		}
	}
	return nil
}

// Approve records the approval of sha as branch's only one.
func (s *GateState) Approve(branch, sha, by, at string) {
	s.Unapprove(branch)
	s.Approvals = lastN(append(s.Approvals, Approval{Branch: branch, Sha: sha, By: by, At: at}), maxGateEntries)
}

// Unapprove drops branch's approval, and says whether there was one.
func (s *GateState) Unapprove(branch string) bool {
	found := false
	kept := s.Approvals[:0]
	for _, a := range s.Approvals {
		if a.Branch == branch {
			found = true
			continue
		}
		kept = append(kept, a)
	}
	s.Approvals = kept
	return found
}

// RecordMerge notes that sha of branch was merged, leaving the base at mergeSha,
// and drops the approval it used.
func (s *GateState) RecordMerge(branch, sha, mergeSha, at string) {
	s.Unapprove(branch)
	s.Merges = lastN(append(s.Merges, Merge{Branch: branch, Sha: sha, MergeSha: mergeSha, At: at}), maxGateEntries)
}

// GateInput is what a gate is worked out from.
type GateInput struct {
	Branch Branch
	// Required is the names of the checks the project defines; nil when it has no
	// check file. A defined check with no result counts as not run.
	Required []string
	// ChecksError is why the check file could not be read, when it exists and could
	// not: a project whose checks cannot be listed is not let through.
	ChecksError string
	Checks      []Check
	Reviews     []Review
	Comments    []Comment
	// Unmergeable is true when the branch does not merge into its base without a
	// conflict. It is false when that is not known, so a git that cannot say does
	// not block a branch; mergeBranch refuses a conflict itself.
	Unmergeable bool
	// OwnCommits is how many commits the branch has beyond its base; -1 when not known.
	OwnCommits int
	Approval   *Approval
	// Merged is the recorded merge of this head, when the head is still in the base.
	Merged *Merge
}

// ComputeGate works out the gate of a branch. The blockers are in the order the
// document lists them and approveMerge reports them.
func ComputeGate(in GateInput) Gate {
	b := in.Branch
	g := Gate{Blockers: []Blocker{}}
	if in.Merged != nil {
		g.State, g.MergeSha = "merged", in.Merged.MergeSha
		if in.Approval != nil && in.Approval.Sha == b.HeadSha {
			g.ApprovedBy, g.ApprovedAt, g.ApprovedSha = in.Approval.By, in.Approval.At, in.Approval.Sha
		}
		return g
	}
	add := func(kind, format string, args ...any) {
		g.Blockers = append(g.Blockers, Blocker{Kind: kind, Text: cleanLine(fmt.Sprintf(format, args...), 200)})
	}

	// Checks: every check the project defines must have passed at this head on a
	// clean tree. A result for another head, or one that ran on uncommitted files,
	// does not count.
	byName := map[string]Check{}
	for _, c := range in.Checks {
		byName[c.Name] = c
	}
	if in.ChecksError != "" {
		add("checks", "the check file cannot be read: %s", in.ChecksError)
	}
	names := in.Required
	if names == nil {
		for _, c := range in.Checks {
			names = append(names, c.Name)
		}
	}
	for _, n := range names {
		c, ok := byName[n]
		switch {
		case !ok:
			add("checks", "%s has not run", n)
		case c.State == "fail":
			add("checks", "%s failed", n)
		case c.State == "running":
			add("checks", "%s is running", n)
		case c.State != "pass" || (c.AtSha != "" && c.AtSha != b.HeadSha):
			add("checks", "%s has not run at this head", n)
		case c.Worktree:
			add("checks", "%s passed on uncommitted files, which does not count", n)
		}
	}

	// Conflicts: another branch changes a path this one does, or the branch does not
	// merge into its base.
	shown := 0
	seen := map[string]bool{}
	for _, f := range b.AllFiles() {
		for _, c := range f.ConflictWith {
			key := c.Pane + "\x00" + c.Path
			if seen[key] {
				continue
			}
			seen[key] = true
			if shown < 5 {
				add("conflict", "%s also changes %s", shortPane(c.Pane), c.Path)
			}
			shown++
		}
	}
	if shown > 5 {
		add("conflict", "and %d more paths changed by other branches", shown-5)
	}
	if in.Unmergeable {
		add("conflict", "does not merge into %s without a conflict", b.Base)
	}

	// Reviews: one that is not stale and asks for changes.
	for _, r := range in.Reviews {
		if r.State == "changes" && !r.Stale {
			add("review", "%s asked for changes", r.Reviewer)
		}
	}

	// Comments: any that is open.
	open := 0
	for _, c := range in.Comments {
		if c.State == "open" {
			open++
		}
	}
	if open == 1 {
		add("comments", "1 comment is open")
	} else if open > 1 {
		add("comments", "%d comments are open", open)
	}

	// Unpushed: the head must be on the upstream.
	switch {
	case b.Upstream == "":
		add("unpushed", "has no upstream, so the head is not pushed")
	case b.Ahead != nil && *b.Ahead > 0:
		add("unpushed", "%d commits are not on %s", *b.Ahead, b.Upstream)
	}

	// Empty: nothing of its own to merge.
	if in.OwnCommits == 0 {
		add("empty", "has no commits of its own")
	}

	switch {
	case len(g.Blockers) > 0:
		g.State = "blocked"
	case in.Approval != nil && in.Approval.Sha == b.HeadSha:
		g.State = "approved"
		g.ApprovedBy, g.ApprovedAt, g.ApprovedSha = in.Approval.By, in.Approval.At, in.Approval.Sha
	default:
		g.State = "ready"
	}
	return g
}

func shortPane(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// GateRefusal is the code approveMerge answers with for the first blocker, in the
// order the document lists: checksNotPassing, conflicts, openComments,
// reviewChangesRequested, unpushed, empty. It is "" when nothing blocks.
func GateRefusal(g Gate) (code, text string) {
	order := []struct{ kind, code string }{
		{"checks", "checksNotPassing"}, {"conflict", "conflicts"}, {"comments", "openComments"},
		{"review", "reviewChangesRequested"}, {"unpushed", "unpushed"}, {"empty", "empty"},
	}
	for _, o := range order {
		for _, b := range g.Blockers {
			if b.Kind == o.kind {
				return o.code, b.Text
			}
		}
	}
	return "", ""
}

// Words joins blocker texts for a message.
func (g Gate) Words() string {
	var parts []string
	for _, b := range g.Blockers {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "; ")
}
