package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// changeView is one modified file as the review panel shows it.
type changeView struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Label     string `json:"label"`
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	Untracked bool   `json:"untracked"`
}

type changesMsg struct {
	Type      string       `json:"type"`
	Cwd       string       `json:"cwd"`
	Branch    string       `json:"branch"`
	Upstream  string       `json:"upstream"`
	Ahead     int          `json:"ahead"`
	Behind    int          `json:"behind"`
	HasRemote bool         `json:"hasRemote"`
	Files     []changeView `json:"files"`
	// Omitted counts the changed files left out of Files, which happens
	// only on a checkout with more of them than a list can usefully hold.
	Omitted int    `json:"omitted,omitempty"`
	Error   string `json:"error,omitempty"`
}

type diffMsg struct {
	Type  string `json:"type"`
	Cwd   string `json:"cwd"`
	File  string `json:"file"`
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

// reviewDir resolves which working tree a review command applies to: the one
// given, or the focused pane's, falling back to the project root.
func (s *Server) reviewDir(path string) string {
	if path != "" {
		return path
	}
	done := make(chan string, 1)
	s.do(func() {
		if p := s.ws.FocusedPane(); p != nil && p.Cwd != "" {
			done <- p.Cwd
			return
		}
		done <- s.ws.ActiveRoot()
	})
	select {
	case dir := <-done:
		return dir
	case <-s.closed:
		// do drops the request once the server is shutting down, so waiting
		// on the reply here would strand the connection goroutine.
		return ""
	}
}

// repoRoot resolves the top of the working tree dir belongs to, falling back
// to dir itself when it is not in one.
//
// git names changed files relative to that root whichever directory it was
// asked from, so a review has to be conducted from the root as well. A pane
// usually sits somewhere inside the project rather than at the top of it, and
// joining a root-relative name onto that subdirectory names a file that is not
// there: every diff came back empty and was reported as possibly binary.
//
// It is usually the root already, though. The panel is told the root when it
// asks what changed and sends it back with every command after that, and a
// working tree's top level is the directory holding the .git entry -- which is
// free to look for, where asking git costs a process, on every click in the
// file list and on every commit.
func repoRoot(dir string) string {
	if root, err := treeRoot(dir); err == nil {
		return root
	}
	return dir
}

// treeRoot is repoRoot for the caller that needs to know when there is no
// working tree at all rather than carry on with the directory it was given.
func treeRoot(dir string) (string, error) {
	if dir != "" {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
	}
	return gitx.Root(dir)
}

// listChanges answers a request for what has changed in a working tree.
func (s *Server) listChanges(c *controlClient, path string) {
	dir := s.reviewDir(path)
	go func() {
		msg := collectChanges(dir)
		c.sendJSON(msg)
		if msg.Omitted > 0 {
			// Said out loud, because a list that stops at two thousand rows
			// looks exactly like a working tree with two thousand changes in
			// it, and the difference matters to someone about to commit.
			c.notify(fmt.Sprintf("showing %d of %d changed files — the rest are left out to keep the list usable",
				len(msg.Files), len(msg.Files)+msg.Omitted), false)
		}
	}()
}

// maxPanelFiles bounds how many entries the panel is sent.
//
// A checkout that has picked up a build directory nobody ignored can have
// tens of thousands of changed files. Every one becomes a row the window
// builds before it draws anything, so the panel stops being usable long
// before the list stops being complete.
const maxPanelFiles = 2000

// collectChanges gathers everything the panel shows about one checkout.
//
// The branch summary, the remote list and the file list come from three
// independent git commands -- five processes between them, since listing the
// files is itself three. They are asked for together rather than one after
// another: this runs on opening the panel and again after every commit, push,
// pull and fetch, and on Windows the process starts are most of that wait.
func collectChanges(dir string) changesMsg { return collectChangesUpTo(dir, maxPanelFiles) }

// collectChangesUpTo is collectChanges with the limit given, so a test can
// reach it without a working tree full of thousands of files.
func collectChangesUpTo(dir string, limit int) changesMsg {
	msg := changesMsg{Type: "changes", Cwd: dir}
	if !gitx.Available() {
		msg.Error = "git is not installed"
		return msg
	}
	root, err := treeRoot(dir)
	if err != nil {
		msg.Error = noRepoReason(dir)
		return msg
	}
	// The panel works in root-relative paths from here on, and echoes this
	// back as the directory its later commands carry.
	msg.Cwd = root

	var (
		st    gitx.Status
		files []gitx.FileChange
		ferr  error
		wg    sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); st = gitx.StatusOf(root) }()
	go func() { defer wg.Done(); files, ferr = gitx.Changes(root) }()
	msg.HasRemote = gitx.HasRemote(root)
	wg.Wait()

	msg.Branch, msg.Upstream = st.Branch, st.Upstream
	msg.Ahead, msg.Behind = st.Ahead, st.Behind
	if ferr != nil {
		msg.Error = ferr.Error()
		return msg
	}
	if len(files) > limit {
		msg.Omitted = len(files) - limit
		files = files[:limit]
	}
	for _, f := range files {
		msg.Files = append(msg.Files, changeView{
			Path: f.Path, Status: f.Status, Label: f.Label,
			Added: f.Added, Removed: f.Removed, Untracked: f.Untracked,
		})
	}
	return msg
}

// noRepoReason explains why a directory has nothing to review.
//
// Removing a worktree deletes its directory, and a pane that was working in it
// is left somewhere that is not there any more. Reporting that as "not a git
// repository" sends the reader looking for the wrong thing entirely -- a
// missing .git, a project opened at the wrong level -- when the answer is that
// the checkout is gone.
func noRepoReason(dir string) string {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return dir + " no longer exists"
	}
	return dir + " is not a git repository"
}

// showDiff sends the diff of one file.
func (s *Server) showDiff(c *controlClient, path, file string) {
	dir := s.reviewDir(path)
	go func() {
		dir = repoRoot(dir)
		msg := diffMsg{Type: "diff", Cwd: dir, File: file}
		text, err := gitx.Diff(dir, file)
		if err != nil {
			msg.Error = err.Error()
		} else if strings.TrimSpace(text) == "" {
			// Binary content is not the explanation it once looked like: git
			// says "Binary files differ" for a tracked one and gitx renders an
			// untracked one as a size. An empty diff now means the file agrees
			// with the last commit after all.
			msg.Text = "(nothing to show — this file matches the last commit, so it may have been changed back)"
		} else {
			msg.Text = text
		}
		c.sendJSON(msg)
	}()
}

// commitChanges stages everything in a working tree and commits it, optionally
// pushing afterwards.
func (s *Server) commitChanges(c *controlClient, path, message string, push bool) {
	dir := s.reviewDir(path)
	go func() {
		dir = repoRoot(dir)
		if err := gitx.CommitAll(dir, message); err != nil {
			c.notify(err.Error(), true)
			s.listChanges(c, dir)
			return
		}
		c.notify("committed in "+shortName(dir), false)
		if push {
			if out, err := gitx.Push(dir); err != nil {
				c.notify(err.Error(), true)
			} else {
				c.notify(remoteSummary("push", out), false)
			}
		}
		s.listChanges(c, dir)
		// The pane headers show the same counts, so refresh them too. Asking
		// the git loop rather than sweeping here keeps one sweep running at a
		// time: each one shells out to git for every open checkout, and a
		// commit that lands while the interval comes round would otherwise
		// start a second pass over all of them.
		s.RefreshGitNow()
	}()
}

// runRemote performs a push, pull or fetch and reports the result.
func (s *Server) runRemote(c *controlClient, action, path string) {
	dir := s.reviewDir(path)
	go func() {
		var (
			out string
			err error
		)
		switch action {
		case "push":
			out, err = gitx.Push(dir)
		case "pull":
			out, err = gitx.Pull(dir)
		case "fetch":
			out, err = gitx.Fetch(dir)
		default:
			return
		}
		if err != nil {
			c.notify(err.Error(), true)
		} else {
			c.notify(remoteSummary(action, out), false)
		}
		s.listChanges(c, dir)
		s.RefreshGitNow()
	}()
}

// remoteSummary reduces git's output to the line worth showing.
//
// A fetch that found nothing prints nothing at all, and a bare "done" against
// a button press leaves it unclear whether anything was even looked at, so the
// quiet cases say what happened instead.
func remoteSummary(action, out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		switch action {
		case "fetch":
			return "fetched — nothing new"
		case "pull":
			return "already up to date"
		}
		return "done"
	}
	lines := strings.Split(out, "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// shortName is the last element of a path, for messages.
func shortName(dir string) string {
	dir = strings.TrimRight(dir, `/\`)
	if i := strings.LastIndexAny(dir, `/\`); i >= 0 {
		return dir[i+1:]
	}
	return dir
}
