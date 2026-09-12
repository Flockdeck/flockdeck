package server

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/gitx"
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
	// Panes is how many open panes are working in this worktree, so it is
	// obvious which checkouts already have an agent on them.
	Panes int `json:"panes"`
}

// branchView is a local branch offered when creating a worktree.
type branchView struct {
	Name      string `json:"name"`
	Upstream  string `json:"upstream"`
	Current   bool   `json:"current"`
	CheckedIn string `json:"checkedIn"`
}

type worktreesMsg struct {
	Type        string         `json:"type"`
	Root        string         `json:"root"`
	DefaultBase string         `json:"defaultBase"`
	Items       []worktreeView `json:"items"`
	Branches    []branchView   `json:"branches"`
	Error       string         `json:"error,omitempty"`
}

// listWorktrees answers a window's request for the repository's worktrees.
//
// Git is slow enough that none of this belongs on the goroutine that owns the
// workspace; only the project root and the pane count are read from there.
func (s *Server) listWorktrees(c *controlClient) {
	root := s.activeRoot()
	go func() {
		msg := collectWorktrees(root)
		if msg.Error == "" {
			paths := make([]string, 0, len(msg.Items))
			for _, it := range msg.Items {
				paths = append(paths, it.Path)
			}
			counts := s.panesPerPath(paths)
			for i := range msg.Items {
				msg.Items[i].Panes = counts[msg.Items[i].Path]
			}
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
		})
	}
	return msg
}

// panesPerPath counts open panes working inside each of the given directories.
func (s *Server) panesPerPath(paths []string) map[string]int {
	counts, _ := ask(s, func() map[string]int {
		counts := map[string]int{}
		for _, t := range s.ws.Tabs {
			for _, id := range t.Tree.Panes() {
				p := s.ws.Pane(id)
				if p == nil {
					continue
				}
				// A worktree kept inside the repository it came from -- a
				// .worktrees directory, say -- is under the main checkout as
				// well as itself, so a pane in it matches both paths. The
				// deepest match is the checkout it is really working in;
				// taking the first would credit its panes to the parent and
				// leave the worktree looking unattended.
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
	})
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

// underPath reports whether cwd is base or inside it.
//
// The two paths reach here from different places -- one from git, the other
// from however the project was opened -- so on Windows they can name the same
// directory in different case, which filepath.Rel treats as unrelated even
// though the file system does not.
func underPath(cwd, base string) bool {
	c, b := filepath.Clean(cwd), filepath.Clean(base)
	if runtime.GOOS == "windows" {
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

// addWorktree creates a worktree and reports the outcome.
func (s *Server) addWorktree(c *controlClient, branch, base, path string) {
	root := s.activeRoot()
	go func() {
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
		s.listWorktrees(c)
	}()
}

func (s *Server) removeWorktree(c *controlClient, path string, force bool) {
	root := s.activeRoot()
	go func() {
		// Removing a worktree deletes its directory. An agent working in it
		// would be left in a path that no longer exists, with nothing to
		// explain why everything it does from then on fails, so this is worth
		// saying before the fact rather than discovering afterwards. git makes
		// the same kind of check for uncommitted work, and force is already
		// how the panel says it means it.
		if !force {
			if n := s.panesPerPath([]string{path})[path]; n > 0 {
				subject := "pane is"
				if n > 1 {
					subject = "panes are"
				}
				c.notify(fmt.Sprintf("%d %s still working in %s — close them first, or force the removal",
					n, subject, filepath.Base(path)), true)
				return
			}
		}
		if err := gitx.Remove(root, path, force); err != nil {
			c.notify(err.Error(), true)
			return
		}
		c.notify("removed "+filepath.Base(path), false)
		s.listWorktrees(c)
	}()
}

func (s *Server) pruneWorktrees(c *controlClient) {
	root := s.activeRoot()
	go func() {
		pruned, err := gitx.Prune(root)
		if err != nil {
			c.notify(err.Error(), true)
			return
		}
		c.notify(prunedSummary(pruned), false)
		s.listWorktrees(c)
	}()
}
