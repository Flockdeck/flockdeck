package server

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// worktreeView is one worktree as the panel shows it.
type worktreeView struct {
	Path      string `json:"path"`
	Label     string `json:"label"`
	Branch    string `json:"branch"`
	Head      string `json:"head"`
	Upstream  string `json:"upstream"`
	Main      bool   `json:"main"`
	Locked    bool   `json:"locked"`
	Detached  bool   `json:"detached"`
	Dirty     int    `json:"dirty"`
	Untracked int    `json:"untracked"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	// Prunable is a worktree whose folder was deleted outside git, so only
	// git's record of it is left. Its counts are zero because there was no
	// status to read, not because it is clean, and there is nowhere to open
	// an agent, a shell or a review in it; the panel offers to prune it
	// instead.
	Prunable bool `json:"prunable"`
	// Panes is how many open panes are working in this worktree, so it is
	// obvious which checkouts already have an agent on them.
	Panes int `json:"panes"`
	// Repo is which member repo of the project this worktree belongs to,
	// its own display label -- left out for a project nobody has grouped,
	// which is every project this panel showed before one could span more
	// than one repo, so the row reads exactly as it always has.
	Repo string `json:"repo,omitempty"`
	// RepoRoot is that repo's own root, alongside Repo, for a row's actions
	// to target the right checkout when the panel spans more than one.
	RepoRoot string `json:"repoRoot,omitempty"`
}

// branchView is a local branch offered when creating a worktree.
type branchView struct {
	Name      string `json:"name"`
	Upstream  string `json:"upstream"`
	Current   bool   `json:"current"`
	CheckedIn string `json:"checkedIn"`
	// Repo and RepoRoot are which member repo of the project this branch
	// belongs to, the same as worktreeView's own -- left out for a project
	// nobody has grouped. Without these a project spanning more than one
	// repo offered "Branches without a worktree" and a base-branch suggestion
	// as one merged list, with no way to tell a branch of one repo from a
	// same-named branch of another, or to know which repo checking one out
	// would even run git in.
	Repo     string `json:"repo,omitempty"`
	RepoRoot string `json:"repoRoot,omitempty"`
}

type worktreesMsg struct {
	Type        string         `json:"type"`
	Root        string         `json:"root"`
	DefaultBase string         `json:"defaultBase"`
	Items       []worktreeView `json:"items"`
	Branches    []branchView   `json:"branches"`
	Error       string         `json:"error,omitempty"`
}

// listWorktrees answers a window's request for the active project's
// worktrees -- every member repo's, for a project spanning more than one;
// see ProjectRepos.
//
// Git is slow enough that none of this belongs on the goroutine that owns the
// workspace; only the project's repos and the pane count are read from there.
func (s *Server) listWorktrees(c *controlClient) {
	asked := worktreeListings.asked(c)
	s.sendWorktrees(c, s.projectRepos(s.activeRoot()), asked)
}

// projectRepos reads root's own project's member repos on the workspace
// goroutine -- root itself, alone, for a project nobody has grouped.
func (s *Server) projectRepos(root string) []workspace.RepoSummary {
	repos, ok := ask(s, func() []workspace.RepoSummary { return s.ws.ProjectRepos(root) })
	if !ok || len(repos) == 0 {
		return []workspace.RepoSummary{{Root: root}}
	}
	return repos
}

// worktreeListings is the worktree listings each window has asked for.
//
// A repository's listing is a git command per worktree, and a large one takes
// a while, so two can finish out of order: open the panel, switch project, and
// the first project's listing lands on top of the second's. The panel draws
// whichever came last, so it showed the other project's worktrees under this
// one -- and a row's Agent or Shell then started work in that project.
var worktreeListings = newNewestAnswer()

// readWorktrees gathers a repository's listing. It is a variable so a test can
// hold one back.
var readWorktrees = collectWorktrees

// sendWorktrees lists every one of repos' worktrees for a window, merged
// into one listing, unless the window has asked for another since the
// request numbered asked.
func (s *Server) sendWorktrees(c *controlClient, repos []workspace.RepoSummary, asked uint64) {
	go func() {
		defer s.surviveFor(c, "listing worktrees")
		msg := collectGroupWorktrees(repos)
		if msg.Error == "" {
			paths := make([]string, 0, len(msg.Items))
			for _, it := range msg.Items {
				paths = append(paths, it.Path)
			}
			// Drawn as none when the workspace could not say.
			counts, _ := s.panesPerPath(paths)
			for i := range msg.Items {
				msg.Items[i].Panes = counts[msg.Items[i].Path]
			}
		}
		if !worktreeListings.answer(c, asked) {
			return
		}
		c.sendJSON(msg)
	}()
}

// collectWorktrees gathers what the panel shows about a repository, short of
// which panes are working where, which only the workspace knows.
//
// The worktree list, the branch list and the starting point a new branch would
// be offered all come from separate git commands that share nothing but the
// root, so they are asked for together. The "is this a repository" call is
// gone from the ordinary path entirely: it existed to phrase a friendlier
// error, and is now only made once something has actually failed.
func collectWorktrees(root string) worktreesMsg {
	msg := worktreesMsg{Type: "worktrees", Root: root}
	if !gitx.Available() {
		msg.Error = "git is not installed"
		return msg
	}

	var (
		wts      []gitx.Worktree
		wtErr    error
		branches []gitx.Branch
		wg       sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); wts, wtErr = gitx.ListDetailed(root) }()
	go func() { defer wg.Done(); branches, _ = gitx.Branches(root) }()
	msg.DefaultBase = gitx.DefaultBase(root)
	wg.Wait()

	if wtErr != nil {
		msg.Error = wtErr.Error()
		if !gitx.IsRepo(root) {
			msg.Error = noRepoReason(root)
		}
		return msg
	}
	for _, b := range branches {
		msg.Branches = append(msg.Branches, branchView{
			Name: b.Name, Upstream: b.Upstream,
			Current: b.Current, CheckedIn: b.CheckedIn,
		})
	}
	for _, wt := range wts {
		msg.Items = append(msg.Items, worktreeView{
			Path:      wt.Path,
			Label:     wt.Label(),
			Branch:    wt.Branch,
			Head:      wt.Status.Head,
			Upstream:  wt.Status.Upstream,
			Main:      wt.Main,
			Locked:    wt.Locked,
			Detached:  wt.Detached,
			Dirty:     wt.Status.Dirty,
			Untracked: wt.Status.Untracked,
			Ahead:     wt.Status.Ahead,
			Behind:    wt.Status.Behind,
			Prunable:  wt.Prunable,
		})
	}
	return msg
}

// collectGroupWorktrees fans collectWorktrees out across every repo in a
// project, merging the results into one list with each row tagged by which
// repo it came from -- see worktreeView.Repo. A project of one repo, which
// is every project nobody has grouped, runs it exactly as it always ran,
// on that one repo alone, and tags nothing: the panel reads exactly as it
// always has.
func collectGroupWorktrees(repos []workspace.RepoSummary) worktreesMsg {
	if len(repos) <= 1 {
		root := ""
		if len(repos) == 1 {
			root = repos[0].Root
		}
		return readWorktrees(root)
	}

	results := make([]worktreesMsg, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func(i int, root string) {
			defer wg.Done()
			results[i] = readWorktrees(root)
		}(i, repo.Root)
	}
	wg.Wait()

	out := worktreesMsg{Type: "worktrees", Root: repos[0].Root}
	var errs []string
	for i, r := range results {
		if i == 0 {
			out.DefaultBase = r.DefaultBase
		}
		for _, it := range r.Items {
			it.Repo = repos[i].Name
			it.RepoRoot = repos[i].Root
			out.Items = append(out.Items, it)
		}
		for _, b := range r.Branches {
			b.Repo = repos[i].Name
			b.RepoRoot = repos[i].Root
			out.Branches = append(out.Branches, b)
		}
		if r.Error != "" {
			errs = append(errs, repos[i].Name+": "+r.Error)
		}
	}
	// A repo git could not be asked about does not sink the whole listing --
	// the other members may have answered fine -- but is worth saying when
	// none of them did.
	if len(errs) > 0 && len(out.Items) == 0 {
		out.Error = strings.Join(errs, "; ")
	}
	return out
}

// panesPerPath counts open panes working inside each of the given directories,
// and reports whether the workspace answered at all. A count nobody could take
// is not a count of none: the worktree panel may draw it as none, but removing
// a worktree must not go on as though no agent were working there.
func (s *Server) panesPerPath(paths []string) (map[string]int, bool) {
	return ask(s, func() map[string]int { return panesIn(s, paths) })
}

// panesIn is panesPerPath's count, taken on the workspace goroutine. It is a
// variable so a test can have the count fail.
var panesIn = func(s *Server, paths []string) map[string]int {
	counts := map[string]int{}
	for _, t := range s.ws.Tabs {
		for _, id := range t.Tree.Panes() {
			p := s.ws.Pane(id)
			if p == nil {
				continue
			}
			// A worktree kept inside the repository it came from -- a
			// .worktrees directory, say -- is under the main checkout as well
			// as itself, so a pane in it matches both paths. The deepest match
			// is the checkout it is really working in; taking the first would
			// credit its panes to the parent and leave the worktree looking
			// unattended.
			best, bestLen := "", -1
			for _, path := range paths {
				if n := len(filepath.Clean(path)); n > bestLen && underPath(p.Cwd, path) {
					best, bestLen = path, n
				}
			}
			if best != "" {
				counts[best]++
			}
		}
	}
	return counts
}

// prunedSummary says what pressing prune actually did.
//
// It reported success either way before, so a button pressed on a repository
// with nothing stale in it said it had cleaned something up.
func prunedSummary(n int) string {
	switch n {
	case 0:
		return "nothing to prune — every worktree is where its record says it is"
	case 1:
		return "pruned 1 stale worktree record"
	default:
		return fmt.Sprintf("pruned %d stale worktree records", n)
	}
}

// foldPathCase is whether two paths differing only in case are the same
// directory. They are on Windows and on a Mac as it comes, as the transcript
// package also takes them to be; on Linux the other spelling is another
// directory. It is a variable so a test can hold both answers to account on
// any machine.
var foldPathCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// underPath reports whether cwd is base or inside it.
//
// The two paths reach here from different places -- one from git, the other
// from however the project was opened -- so they can name the same directory
// in different case, which filepath.Rel treats as unrelated even where the
// file system does not.
func underPath(cwd, base string) bool {
	c, b := filepath.Clean(cwd), filepath.Clean(base)
	if foldPathCase {
		c, b = strings.ToLower(c), strings.ToLower(b)
	}
	rel, err := filepath.Rel(b, c)
	if err != nil {
		return false
	}
	// Only a leading path element of ".." means the way out. Testing the
	// prefix alone put a directory genuinely called something like "..old"
	// outside the tree it is sitting in.
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// samePath reports whether two cleaned paths name the same directory, by the
// rule underPath follows: on Linux a directory spelled in other case is
// another directory, and two projects can sit side by side as App and app.
func samePath(a, b string) bool {
	if foldPathCase {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// worktreeTarget is the repo a worktree command runs git against: root when
// it names one, s.activeRoot() otherwise -- a row's own repo, for a project
// spanning more than one, and the ordinary single-repo project's only repo
// where nothing more particular was named.
func (s *Server) worktreeTarget(root string) string {
	if root != "" {
		return root
	}
	return s.activeRoot()
}

// addWorktree creates a worktree in root -- or the active project's own
// repo, when root names none -- and reports the outcome.
func (s *Server) addWorktree(c *controlClient, root, branch, base, path string) {
	root = s.worktreeTarget(root)
	// The listing this ends on counts from when it was asked for, so a panel
	// opened on another project meanwhile is not drawn over.
	asked := worktreeListings.asked(c)
	go func() {
		defer s.surviveFor(c, "creating a worktree")
		// The listing is sent however this ends, a refusal included. Asking
		// for it above made any listing the panel was still waiting on out of
		// date, and one that never came left the panel on "Loading…" -- or on
		// a list from before -- until it was opened again. It covers the
		// whole project, not just the one repo the worktree was made in.
		defer s.sendWorktrees(c, s.projectRepos(root), asked)
		branch = strings.TrimSpace(branch)
		if branch == "" {
			c.notify("a branch name is required", true)
			return
		}
		if path == "" {
			path = gitx.DefaultWorktreePath(root, branch)
		}
		if err := gitx.AddFrom(root, path, branch, strings.TrimSpace(base)); err != nil {
			c.notify(err.Error(), true)
			return
		}
		c.notify("created "+filepath.Base(path), false)
	}()
}

func (s *Server) removeWorktree(c *controlClient, root, path string, force bool) {
	root = s.worktreeTarget(root)
	asked := worktreeListings.asked(c) // as addWorktree's
	go func() {
		defer s.surviveFor(c, "removing a worktree")
		defer s.sendWorktrees(c, s.projectRepos(root), asked) // as addWorktree's
		// Removing a worktree deletes its directory. An agent working in it
		// would be left in a path that no longer exists, with nothing to
		// explain why everything it does from then on fails, and whatever it
		// had not yet committed would go with the directory. So a worktree
		// with panes in it is not removed, force or no force: force is how the
		// panel says to throw away uncommitted work, which is the question git
		// asks, and an agent still running there is not that.
		counts, ok := s.panesPerPath([]string{path})
		if !ok {
			c.notify(fmt.Sprintf("could not tell whether an agent is working in %s, so it was not removed", filepath.Base(path)), true)
			return
		}
		if n := counts[path]; n > 0 {
			subject, them := "pane is", "it"
			if n > 1 {
				subject, them = "panes are", "them"
			}
			c.notify(fmt.Sprintf("%d %s still working in %s — close %s first",
				n, subject, filepath.Base(path), them), true)
			return
		}
		if err := gitx.Remove(root, path, force); err != nil {
			c.notify(err.Error(), true)
			return
		}
		c.notify("removed "+filepath.Base(path), false)
	}()
}

func (s *Server) pruneWorktrees(c *controlClient, root string) {
	root = s.worktreeTarget(root)
	asked := worktreeListings.asked(c) // as addWorktree's
	go func() {
		defer s.surviveFor(c, "pruning worktrees")
		defer s.sendWorktrees(c, s.projectRepos(root), asked) // as addWorktree's
		pruned, err := gitx.Prune(root)
		if err != nil {
			c.notify(err.Error(), true)
			return
		}
		c.notify(prunedSummary(pruned), false)
	}()
}
