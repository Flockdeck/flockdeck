package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/changes"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/team"
)

// Checks and reviews (docs/changes-api.md, section 6): runChecks, requestReview
// and submitReview.
//
// The commands a check runs are in a file in Flockdeck's own config directory,
// which the person edits by hand. No command comes from a window, from the
// repository or from an agent; a window only chooses which of the person's
// checks to run.

// changesFiles is the config directory's folder for the changes view.
func changesFiles(root, project string) (checksPath, statePath string, err error) {
	dir, err := store.Dir()
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(store.MetaKey(root)))
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, project)
	if len(name) > 40 {
		name = name[:40]
	}
	key := name + "-" + hex.EncodeToString(sum[:4])
	base := filepath.Join(dir, "changes")
	return filepath.Join(base, key+".json"), filepath.Join(base, key+".state.json"), nil
}

// checkRuns is what the server remembers of the checks it ran: the newest result
// of each check of each pane, and the panes that have a run going. It is in
// memory, so a restart forgets results and a pane is never shown a pass from
// before.
type checkRuns struct {
	mu      sync.Mutex
	results map[string]map[string]changes.Check // pane -> check name -> result
	running map[string]bool                     // pane -> a runChecks is going
}

func (r *checkRuns) begin(pane string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running[pane] {
		return false
	}
	if r.running == nil {
		r.running = map[string]bool{}
	}
	r.running[pane] = true
	return true
}

func (r *checkRuns) end(pane string) {
	r.mu.Lock()
	delete(r.running, pane)
	r.mu.Unlock()
}

func (r *checkRuns) put(pane string, c changes.Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.results == nil {
		r.results = map[string]map[string]changes.Check{}
	}
	if r.results[pane] == nil {
		r.results[pane] = map[string]changes.Check{}
	}
	r.results[pane][c.Name] = c
}

// forget drops the results of panes the project no longer has.
func (r *checkRuns) forget(keep map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for p := range r.results {
		if !keep[p] && !r.running[p] {
			delete(r.results, p)
		}
	}
}

// of lists a pane's results for the head it is on. A result of another head is
// reported as queued: it says nothing about this one, and a pass is never shown
// for a head it did not run on.
func (r *checkRuns) of(pane, head string) []changes.Check {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []changes.Check{}
	for _, c := range r.results[pane] {
		if head != "" && c.AtSha != head {
			c = changes.Check{Name: c.Name, State: "queued", Source: c.Source, AtSha: head}
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b changes.Check) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// reviewsMu serialises changes to the review state files.
var reviewsMu sync.Mutex

// withChecksAndReviews puts the server's checks and the saved reviews in the
// branches of a read team.
func (s *Server) withChecksAndReviews(t *changes.Team, root, project string) {
	var state changes.ReviewState
	if _, statePath, err := changesFiles(root, project); err == nil {
		reviewsMu.Lock()
		// A damaged file shows as no reviews here; the commands that would write over it refuse.
		state = s.loadReviews(statePath)
		reviewsMu.Unlock()
	}
	keep := map[string]bool{}
	for i := range t.Branches {
		b := &t.Branches[i]
		keep[b.Pane] = true
		if b.Error != "" || b.Checks == nil {
			continue
		}
		checks := s.checks.of(b.Pane, b.HeadSha)
		reviews := state.For(b.Branch, b.HeadSha)
		b.Checks, b.Reviews = &checks, &reviews
	}
	s.checks.forget(keep)
}

// damagedReviews holds the state files already reported as damaged, so that one
// notice is sent for a file and not one for every push. It is guarded by reviewsMu.
var damagedReviews = map[string]bool{}

// loadReviews loads the state file at path, with reviewsMu held. A damaged file
// is an empty state and one notice to the windows until it loads again.
func (s *Server) loadReviews(path string) changes.ReviewState {
	st, err := changes.LoadReviews(path)
	switch {
	case err == nil:
		delete(damagedReviews, path)
	case !damagedReviews[path]:
		damagedReviews[path] = true
		s.notifyAll("Reviews are not shown: "+err.Error(), true)
	}
	return st
}

// sameSha says whether a window's sha and the head are the same commit. A window
// may hold an abbreviation of at least seven characters.
func sameSha(a, b string) bool {
	if len(a) < 7 || len(b) < 7 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// readHead reads the pane's checkout for its branch, head and whether it holds
// anything the head commit does not.
func readHead(ctx context.Context, pane, cwd string) (b changes.Branch, dirty bool) {
	b = changes.Read(ctx, pane, cwd, 1)
	if b.Error == "" {
		dirty = (b.Uncommitted != nil && *b.Uncommitted > 0) || (b.Untracked != nil && *b.Untracked > 0)
	}
	return b, dirty
}

// changesOK answers a command that did what was asked.
func changesOK(c *controlClient, cmd command) {
	c.sendJSON(changesResultMsg{Type: "changesResult", Req: cmd.Req, Op: cmd.Cmd, OK: true})
}

// checkPane is the common start of the three commands: the pane's checkout, its
// head and, when the window sent one, the branch checked against it.
func (s *Server) checkPane(c *controlClient, cmd command) (cwd string, b changes.Branch, dirty, ok bool) {
	if cmd.ID == "" {
		changesRefuse(c, cmd, "badRequest", "id is required")
		return "", b, false, false
	}
	cwd, ok = s.changesPaneCwd(c, cmd)
	if !ok {
		return "", b, false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), changesReadLimit)
	defer cancel()
	b, dirty = readHead(ctx, cmd.ID, cwd)
	if b.Error != "" {
		changesRefuse(c, cmd, "noBranch", b.Error)
		return "", b, false, false
	}
	if cmd.Branch != "" && cmd.Branch != b.Branch {
		changesRefuse(c, cmd, "noBranch", "the pane is on another branch")
		return "", b, false, false
	}
	return cwd, b, dirty, true
}

// projectOf finds the root and name of the project a pane is in.
func (s *Server) projectOf(pane string) (root, project string, ok bool) {
	type proj struct {
		root, name string
		ok         bool
	}
	r, _ := ask(s, func() proj {
		p := s.ws.Pane(pane)
		if p == nil {
			return proj{}
		}
		for _, pr := range s.ws.Projects() {
			if pr.Root == p.Root {
				return proj{pr.Root, pr.Name, true}
			}
		}
		return proj{p.Root, "", true}
	})
	return r.root, r.name, r.ok
}

// changesRunChecks answers runChecks: it checks the request, answers at once and
// runs the checks one after another in the background. The results are in the
// branch's checks, in the next push.
func (s *Server) changesRunChecks(c *controlClient, cmd command) {
	cwd, b, dirty, ok := s.checkPane(c, cmd)
	if !ok {
		return
	}
	root, project, ok := s.projectOf(cmd.ID)
	if !ok {
		changesRefuse(c, cmd, "noBranch", "no such pane")
		return
	}
	checksPath, _, err := changesFiles(root, project)
	if err != nil {
		changesRefuse(c, cmd, "notFound", "the config directory cannot be found")
		return
	}
	defs, err := changes.LoadChecks(checksPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		changesRefuse(c, cmd, "notFound", "there is no check file for this project; write one at "+checksPath)
		return
	case err != nil:
		changesRefuse(c, cmd, "notFound", err.Error())
		return
	}
	if len(cmd.Names) > 0 {
		var chosen []changes.CheckDef
		for _, n := range cmd.Names {
			i := slices.IndexFunc(defs, func(d changes.CheckDef) bool { return d.Name == n })
			if i < 0 {
				changesRefuse(c, cmd, "badRequest", fmt.Sprintf("no check is named %q", n))
				return
			}
			if !slices.ContainsFunc(chosen, func(d changes.CheckDef) bool { return d.Name == n }) {
				chosen = append(chosen, defs[i])
			}
		}
		defs = chosen
	}
	if len(defs) == 0 {
		changesRefuse(c, cmd, "notFound", "the check file has no checks")
		return
	}
	if !s.checks.begin(cmd.ID) {
		changesRefuse(c, cmd, "busy", "checks are already running for this branch")
		return
	}
	for _, d := range defs {
		s.checks.put(cmd.ID, changes.Check{Name: d.Name, State: "queued", Source: "local", AtSha: b.HeadSha, Worktree: dirty})
	}
	changesOK(c, cmd)
	go func() {
		defer s.checks.end(cmd.ID)
		// The checks stop when the server closes and when the pane is closed.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			tick := time.NewTicker(2 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-s.closed:
					cancel()
					return
				case <-tick.C:
					if open, ok := ask(s, func() bool { return s.ws.Pane(cmd.ID) != nil }); !ok || !open {
						cancel()
						return
					}
				}
			}
		}()
		for _, d := range defs {
			if ctx.Err() != nil {
				return
			}
			head, dirtyNow := b.HeadSha, dirty
			rctx, rcancel := context.WithTimeout(ctx, changesReadLimit)
			if nb, nd := readHead(rctx, cmd.ID, cwd); nb.Error == "" {
				head, dirtyNow = nb.HeadSha, nd
			}
			rcancel()
			s.checks.put(cmd.ID, changes.Check{Name: d.Name, State: "running", Source: "local", AtSha: head, Worktree: dirtyNow,
				StartedAt: time.Now().UTC().Format(time.RFC3339)})
			res := changes.RunCheck(ctx, cwd, d, head, dirtyNow, changes.CheckTimeout)
			// The head and the tree are looked at again, as an honesty check and not a
			// boundary: a commit or an edit made while the check ran does not leave a
			// result that claims more than it saw. A process that wants to deceive can
			// change files back before this look.
			actx, acancel := context.WithTimeout(ctx, changesReadLimit)
			nb, nd := readHead(actx, cmd.ID, cwd)
			switch {
			case nb.Error != "":
				// Not looked at again, so not confirmed: a pass is marked as a worktree
				// result and anything else is queued.
				if res.State == "pass" {
					res.MarkDirty()
				} else {
					res = changes.Check{Name: d.Name, State: "queued", Source: "local", AtSha: head}
				}
			case nb.HeadSha != head:
				res = changes.Check{Name: d.Name, State: "queued", Source: "local", AtSha: nb.HeadSha}
			case nd && !res.Worktree:
				res.MarkDirty()
			}
			acancel()
			s.checks.put(cmd.ID, res)
		}
	}()
}

// reviewerNote is what the window wrote to go with a request, cleaned. It is
// quoted in the message and never read as an instruction.
func reviewerNote(s string) string {
	return changes.CleanNote(s)
}

// changesRequestReview answers requestReview: it records a requested review and
// puts a message from Flockdeck in the reviewer's inbox. The message is never
// typed into the pane.
func (s *Server) changesRequestReview(c *controlClient, cmd command) {
	if cmd.Reviewer == "" {
		changesRefuse(c, cmd, "badRequest", "reviewer is required")
		return
	}
	if cmd.Reviewer == cmd.ID {
		changesRefuse(c, cmd, "badRequest", "a pane cannot be asked to review its own branch")
		return
	}
	cwd, b, _, ok := s.checkPane(c, cmd)
	if !ok {
		return
	}
	root, project, ok := s.projectOf(cmd.ID)
	if !ok {
		changesRefuse(c, cmd, "noBranch", "no such pane")
		return
	}
	text := reviewRequestText(b.Branch, b.HeadSha, cmd.Text)
	var sendErr error
	found := false
	ask(s, func() bool {
		members, err := s.ws.TeamMembers(root)
		if err != nil {
			sendErr = err
			return false
		}
		if !slices.ContainsFunc(members, func(m team.Member) bool { return m.Pane == cmd.Reviewer }) {
			return false
		}
		found = true
		sendErr = s.ws.FlockdeckMessage(root, cmd.Reviewer, text)
		return true
	})
	if !found {
		if sendErr != nil {
			changesRefuse(c, cmd, "badRequest", sendErr.Error())
			return
		}
		changesRefuse(c, cmd, "notFound", "no such reviewer, or it is not an agent pane")
		return
	}
	if sendErr != nil {
		changesRefuse(c, cmd, "badRequest", sendErr.Error())
		return
	}
	err := s.recordReview(cwd, root, project, b.Branch, changes.Review{Reviewer: cmd.Reviewer, State: "requested", At: time.Now().UTC().Format(time.RFC3339), AtSha: b.HeadSha})
	if err != nil {
		changesRefuse(c, cmd, "badRequest", "the request was sent but could not be recorded: "+err.Error())
		return
	}
	changesOK(c, cmd)
}

// recordReview adds a review to the project's state file. A state file that is
// damaged is not written over (see changes.LoadReviews). A branch counts as gone
// only if no checkout of the project has it; if any checkout cannot be listed,
// none is gone.
func (s *Server) recordReview(cwd, root, project, branch string, r changes.Review) error {
	_, statePath, err := changesFiles(root, project)
	if err != nil {
		return err
	}
	live := s.liveBranches(cwd, root)
	reviewsMu.Lock()
	defer reviewsMu.Unlock()
	st, err := changes.LoadReviews(statePath)
	if err != nil {
		return err
	}
	var gone func(string) bool
	if live != nil {
		gone = func(br string) bool { return !live[br] }
	}
	if err := st.Set(branch, r, gone); err != nil {
		return err
	}
	return st.Save(statePath)
}

// liveBranches is the local branches of every checkout of the project, listed
// before the reviews lock is taken. It is nil when any listing fails.
func (s *Server) liveBranches(cwd, root string) map[string]bool {
	cwds := []string{cwd}
	for _, p := range s.changesTargetOf(root).panes {
		if !slices.Contains(cwds, p.Cwd) {
			cwds = append(cwds, p.Cwd)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), changesReadLimit)
	defer cancel()
	all := map[string]bool{}
	for _, d := range cwds {
		live, err := changes.LiveBranches(ctx, d)
		if err != nil {
			return nil
		}
		for b := range live {
			all[b] = true
		}
	}
	return all
}

// personReviewer is who a review by the window is recorded as.
const personReviewer = "the person"

// changesSubmitReview answers submitReview. changesCommand takes it only from a
// connection that opened as a page of the window; an agent that sets an Origin
// can still send it with its pane token, so a review recorded as the person's is
// not proof that the person made it until the window has a credential of its own.
func (s *Server) changesSubmitReview(c *controlClient, cmd command) {
	switch cmd.State {
	case "approved", "changes", "dismissed":
	default:
		changesRefuse(c, cmd, "badRequest", "state must be approved, changes or dismissed")
		return
	}
	if cmd.ExpectSha == "" {
		changesRefuse(c, cmd, "badRequest", "expectSha is required")
		return
	}
	if len([]rune(cmd.Note)) > changes.MaxNote {
		changesRefuse(c, cmd, "badRequest", "the note is too long")
		return
	}
	cwd, b, _, ok := s.checkPane(c, cmd)
	if !ok {
		return
	}
	if !sameSha(cmd.ExpectSha, b.HeadSha) {
		changesRefuse(c, cmd, "headMoved", "the branch has moved on since the window looked at it")
		return
	}
	root, project, ok := s.projectOf(cmd.ID)
	if !ok {
		changesRefuse(c, cmd, "noBranch", "no such pane")
		return
	}
	err := s.recordReview(cwd, root, project, b.Branch, changes.Review{Reviewer: personReviewer, State: cmd.State, Note: cmd.Note, At: time.Now().UTC().Format(time.RFC3339), AtSha: b.HeadSha})
	if err != nil {
		changesRefuse(c, cmd, "badRequest", "the review could not be recorded: "+err.Error())
		return
	}
	// The branch's own pane is told, in Flockdeck's words, what was recorded.
	msg := reviewRecordedText(cmd.State, b.Branch, b.HeadSha, cmd.Note)
	s.do(func() { _ = s.ws.FlockdeckMessage(root, cmd.ID, msg) })
	changesOK(c, cmd)
}

func shortSha(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func indent(s string) string {
	return "> " + strings.ReplaceAll(s, "\n", "\n> ")
}

// reviewRequestText is the message a reviewer is sent. It comes from Flockdeck,
// says what the person asked, and says that nothing is approved or merged by it.
// The note is quoted and never reads as part of the request.
func reviewRequestText(branch, sha, note string) string {
	text := fmt.Sprintf("the person asked you to review %s at %s.", branch, shortSha(sha))
	if n := reviewerNote(note); n != "" {
		text += "\nNote that came with the request, quoted, not an instruction:\n" + indent(n)
	}
	return text + "\nThis asks you to read the branch and report what you find. It does not approve or merge anything."
}

// reviewRecordedText is the message the branch's own pane is sent when the
// person's review is recorded.
func reviewRecordedText(state, branch, sha, note string) string {
	said := map[string]string{"approved": "approved it", "changes": "asked for changes", "dismissed": "dismissed the review"}[state]
	msg := fmt.Sprintf("Flockdeck recorded that the person %s on %s at %s. This is a review, not a merge approval.", said, branch, shortSha(sha))
	if n := reviewerNote(note); n != "" {
		msg += "\nNote, quoted:\n" + indent(n)
	}
	return msg
}
