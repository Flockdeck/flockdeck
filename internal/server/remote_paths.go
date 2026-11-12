package server

import (
	"path/filepath"
	"strings"

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
		if path == "" && cmd.Cmd == "worktreeAdd" {
			return false
		}
		// The repo the worktree belongs to: the one named, else the active
		// project's own, as worktreeTarget chooses.
		repo := s.worktreeTarget(root)
		if path == "" || !s.remoteWorktreePathOK(repo, path) {
			return deny()
		}
		return false
	}
	if f.path && path != "" && !s.remotePathKnown(path) {
		return deny()
	}
	return false
}

// remotePathKnown reports whether path is, or is inside, an open or recent
// project, or is a worktree one of those repos has: a pane working in one has
// a folder that is not under the project's root.
func (s *Server) remotePathKnown(path string) bool {
	if s.insideKnownProject(path) {
		return true
	}
	target := store.MetaKey(realPath(path))
	for _, root := range s.knownRoots() {
		wts, err := gitx.List(root)
		if err != nil {
			continue
		}
		for _, wt := range wts {
			if underPath(target, store.MetaKey(realPath(wt.Path))) {
				return true
			}
		}
	}
	return false
}

// remoteWorktreePathOK reports whether a window may make or remove a worktree
// of repo at path: inside the project, one git already lists for it, or where
// a new one is put by default, beside the repo and named after it.
func (s *Server) remoteWorktreePathOK(repo, path string) bool {
	if !s.insideKnownProject(repo) {
		return false
	}
	path = filepath.Clean(path)
	real := store.MetaKey(realPath(path))
	if underPath(real, store.MetaKey(realPath(repo))) {
		return true
	}
	if wts, err := gitx.List(repo); err == nil {
		for _, wt := range wts {
			if !wt.Main && samePath(store.MetaKey(realPath(wt.Path)), real) {
				return true
			}
		}
	}
	top := repo
	if r, err := gitx.Root(repo); err == nil {
		top = r
	}
	top = filepath.Clean(top)
	return samePath(filepath.Dir(path), filepath.Dir(top)) &&
		strings.HasPrefix(strings.ToLower(filepath.Base(path)), strings.ToLower(filepath.Base(top))+"-")
}
