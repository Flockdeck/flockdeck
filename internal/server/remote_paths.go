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
