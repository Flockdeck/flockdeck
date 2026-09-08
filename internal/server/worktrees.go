package server

import (
	"path/filepath"
	"strings"

	"github.com/jmwri/agent-wrapper/internal/gitx"
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
		msg := worktreesMsg{Type: "worktrees", Root: root}
		switch {
		case !gitx.Available():
			msg.Error = "git is not installed"
			c.sendJSON(msg)
			return
		case !gitx.IsRepo(root):
			msg.Error = root + " is not a git repository"
			c.sendJSON(msg)
			return
		}

		wts, err := gitx.ListDetailed(root)
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		msg.DefaultBase = gitx.DefaultBase(root)
		if branches, err := gitx.Branches(root); err == nil {
			for _, b := range branches {
				msg.Branches = append(msg.Branches, branchView{
					Name: b.Name, Upstream: b.Upstream,
					Current: b.Current, CheckedIn: b.CheckedIn,
				})
			}
		}

		paths := make([]string, 0, len(wts))
		for _, wt := range wts {
			paths = append(paths, wt.Path)
		}
		counts := s.panesPerPath(paths)

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
				Panes:     counts[wt.Path],
			})
		}
		c.sendJSON(msg)
	}()
}

// panesPerPath counts open panes working inside each of the given directories.
func (s *Server) panesPerPath(paths []string) map[string]int {
	done := make(chan map[string]int, 1)
	s.do(func() {
		counts := map[string]int{}
		for _, t := range s.ws.Tabs {
			for _, id := range t.Tree.Panes() {
				p := s.ws.Pane(id)
				if p == nil {
					continue
				}
				for _, path := range paths {
					if underPath(p.Cwd, path) {
						counts[path]++
						break
					}
				}
			}
		}
		done <- counts
	})
	select {
	case counts := <-done:
		return counts
	case <-s.closed:
		return nil
	}
}

// underPath reports whether cwd is base or inside it.
func underPath(cwd, base string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(cwd))
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
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
		if err := gitx.Prune(root); err != nil {
			c.notify(err.Error(), true)
			return
		}
		c.notify("pruned stale worktree records", false)
		s.listWorktrees(c)
	}()
}
