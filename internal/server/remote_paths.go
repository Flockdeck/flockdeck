package server

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/store"
)

// remotePathsDeskOnly is what a window reached through the relay is told when a
// command names a folder that is not part of a project Flockdeck knows.
const remotePathsDeskOnly = "That folder is not part of a project Flockdeck knows. Folders outside them are chosen on the machine it runs on, not from a window reached through the relay"

// remotePathFields says which of a command's Root and Path name a folder the
// command acts in. The browse limit is only as good as the commands that can
// make a folder known or act in one: a window that may open any path as a
// project, point git at it or start an agent in it has the whole machine, so
// each of these is held to the known projects when it comes through the relay.
//
// Left alone, and why: commands whose Root or ID is resolved against the open
// projects or panes by the workspace itself (selectProject, closeProject,
// team and graph commands, startAgent, spawn, changes' team contract), a pane
// id (closePane, paneSend), and browse, which has its own rule.
var remotePathFields = map[string]struct{ root, path bool }{
	"openProject":        {path: true},
	"addRepoToGroup":     {path: true},
	"worktrees":          {root: true},
	"worktreeAdd":        {root: true, path: true},
	"worktreeRemove":     {root: true, path: true},
	"worktreePrune":      {root: true},
	"changes":            {path: true},
	"changesSnapshot":    {root: true},
	"diff":               {path: true},
	"commit":             {path: true},
	"gitPush":            {path: true},
	"gitPull":            {path: true},
	"gitFetch":           {path: true},
	"ghStatus":           {path: true},
	"ghLogout":           {path: true},
	"ghPRs":              {path: true},
	"ghPR":               {path: true},
	"ghPRCreate":         {path: true},
	"ghPRComment":        {path: true},
	"ghIssues":           {path: true},
	"ghIssue":            {path: true},
	"ghIssueCreate":      {path: true},
	"ghIssueComment":     {path: true},
	"ghChecks":           {path: true},
	"conversations":      {path: true},
	"resumeConversation": {path: true},
	"newTab":             {path: true},
	"splitPane":          {root: true, path: true},
}

// refuseRemotePaths refuses a command from a window reached through the relay
// that names a folder outside the known projects, and reports whether it did.
func (s *Server) refuseRemotePaths(c *controlClient, cmd command) bool {
	if !c.remote {
		return false
	}
	f, ok := remotePathFields[cmd.Cmd]
	if !ok {
		return false
	}
	deny := func() bool {
		c.notify(remotePathsDeskOnly, true)
		return true
	}
	root := unquotePath(cmd.Root)
	path := unquotePath(cmd.Path)
	if f.root && root != "" && !s.remotePathKnown(root) {
		return deny()
	}
	if cmd.Cmd == "addRepoToGroup" && root != "" && !s.remotePathKnown(root) {
		return deny()
	}
	switch cmd.Cmd {
	case "worktreeAdd", "worktreeRemove":
		// The repo the worktree belongs to: the one named, else the active
		// project's own, as worktreeTarget chooses.
		repo := s.worktreeTarget(root)
		if !s.remoteWorktreeOK(cmd.Cmd, repo, strings.TrimSpace(cmd.Text), path) {
			return deny()
		}
		return false
	}
	if f.path && path != "" && !s.remotePathKnown(path) {
		return deny()
	}
	if cmd.Cmd == "diff" && !s.remoteDiffFileInside(path, cmd.Text) {
		return deny()
	}
	return false
}

// remoteDiffFileInside reports whether the file a diff names is, where it really
// is, inside the working tree it is asked of. gitx already refuses a name that
// leaves the tree by its spelling; a junction or link inside the tree that leads
// out is only seen once links are followed, and git then reads the file through it.
func (s *Server) remoteDiffFileInside(path, file string) bool {
	if file == "" {
		return true
	}
	// A rooted name or one with a drive is no name inside the tree, however
	// Join would spell it.
	if filepath.IsAbs(file) || filepath.VolumeName(file) != "" || strings.HasPrefix(file, "/") || strings.HasPrefix(file, string(filepath.Separator)) {
		return false
	}
	top := repoRoot(s.reviewDir(path))
	target := store.MetaKey(realPath(filepath.Join(top, filepath.FromSlash(file))))
	return underPath(target, store.MetaKey(realPath(top)))
}

// remotePathKnown reports whether path is, or is inside, an open or recent
// project, or is a worktree one of those repos has: a pane working in one has
// a folder that is not under the project's root.
func (s *Server) remotePathKnown(path string) bool {
	if s.insideKnownProject(path) {
		return true
	}
	target := store.MetaKey(realPath(path))
	for _, wt := range s.knownWorktrees() {
		if underPath(target, wt) {
			return true
		}
	}
	return false
}

// worktreeCacheTTL is how long the worktrees of the known projects are kept. A
// path outside every project used to run git in every known root on each
// command, on the read loop of the window that sent it; a window sending
// made-up paths made the desk run git as fast as it could send frames.
const worktreeCacheTTL = 5 * time.Second

// worktreeCache is the worktree folders of the known roots, when they were
// listed, and the listing in progress, if any.
type worktreeCache struct {
	mu   sync.Mutex
	at   time.Time
	list []string
	// gen counts forgetWorktrees, so a listing that began before one is not
	// kept as current.
	gen uint64
	// flight is closed when the listing now running ends; callers that arrive
	// meanwhile wait for it and share its answer rather than run git again.
	flight chan struct{}
}

// knownWorktrees lists the worktrees of the known roots, at most once in
// worktreeCacheTTL however many commands ask. The git runs happen outside the
// lock, one listing at a time (the callers that arrive during it wait for it),
// so a slow repository does not hold every other command that needs the cache.
// It is dropped when a worktree is added or removed from a window
// (forgetWorktrees).
func (s *Server) knownWorktrees() []string {
	c := &s.wtCache
	for {
		c.mu.Lock()
		if !c.at.IsZero() && time.Since(c.at) < worktreeCacheTTL {
			list := c.list
			c.mu.Unlock()
			return list
		}
		if wait := c.flight; wait != nil {
			c.mu.Unlock()
			<-wait
			continue
		}
		done := make(chan struct{})
		c.flight = done
		gen := c.gen
		c.mu.Unlock()

		out := s.listKnownWorktrees()

		c.mu.Lock()
		if c.gen == gen {
			c.at, c.list = time.Now(), out
		}
		c.flight = nil
		c.mu.Unlock()
		close(done)
		return out
	}
}

// listKnownWorktrees runs git in every known root. It takes no lock of the cache.
func (s *Server) listKnownWorktrees() []string {
	var out []string
	for _, root := range s.knownRoots() {
		wts, err := gitx.List(root)
		if err != nil {
			continue
		}
		for _, wt := range wts {
			out = append(out, store.MetaKey(realPath(wt.Path)))
		}
	}
	return out
}

// forgetWorktrees drops the cached list, after a worktree was added or removed.
func (s *Server) forgetWorktrees() {
	s.wtCache.mu.Lock()
	s.wtCache.at = time.Time{}
	s.wtCache.gen++
	s.wtCache.mu.Unlock()
}

// remoteWorktreeOK reports whether a window may add or remove a worktree of
// repo. The repo must be a known project. An added worktree goes where
// addWorktree puts it when no path is sent, or at exactly that place named: a
// sibling that merely looks like it, or a folder inside the repo, is refused. A
// removed one must be a worktree git lists for the repo, and not the main one.
func (s *Server) remoteWorktreeOK(cmd, repo, branch, path string) bool {
	if !s.insideKnownProject(repo) {
		return false
	}
	if cmd == "worktreeAdd" {
		return path == "" || samePath(filepath.Clean(path), filepath.Clean(gitx.DefaultWorktreePath(repo, branch)))
	}
	if path == "" {
		return false
	}
	real := store.MetaKey(realPath(path))
	wts, err := gitx.List(repo)
	if err != nil {
		return false
	}
	for _, wt := range wts {
		if !wt.Main && samePath(store.MetaKey(realPath(wt.Path)), real) {
			return true
		}
	}
	return false
}
