package server

import (
	"strings"

	"github.com/jmwri/agent-wrapper/internal/gitx"
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
	Error     string       `json:"error,omitempty"`
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
func repoRoot(dir string) string {
	if root, err := gitx.Root(dir); err == nil {
		return root
	}
	return dir
}

// listChanges answers a request for what has changed in a working tree.
func (s *Server) listChanges(c *controlClient, path string) {
	dir := s.reviewDir(path)
	go func() {
		msg := changesMsg{Type: "changes", Cwd: dir}
		if !gitx.Available() {
			msg.Error = "git is not installed"
			c.sendJSON(msg)
			return
		}
		root, err := gitx.Root(dir)
		if err != nil {
			msg.Error = dir + " is not a git repository"
			c.sendJSON(msg)
			return
		}
		// The panel works in root-relative paths from here on, and echoes this
		// back as the directory its later commands carry.
		dir = root
		msg.Cwd = dir

		st := gitx.StatusOf(dir)
		msg.Branch, msg.Upstream = st.Branch, st.Upstream
		msg.Ahead, msg.Behind = st.Ahead, st.Behind
		msg.HasRemote = gitx.HasRemote(dir)

		files, err := gitx.Changes(dir)
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		for _, f := range files {
			msg.Files = append(msg.Files, changeView{
				Path: f.Path, Status: f.Status, Label: f.Label,
				Added: f.Added, Removed: f.Removed, Untracked: f.Untracked,
			})
		}
		c.sendJSON(msg)
	}()
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
			msg.Text = "(no textual differences — the file may be binary)"
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
				c.notify(pushSummary(out), false)
			}
		}
		s.listChanges(c, dir)
		// The pane headers show the same counts, so refresh them too.
		s.ws.RefreshGit(s.do)
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
			c.notify(pushSummary(out), false)
		}
		s.listChanges(c, dir)
		s.ws.RefreshGit(s.do)
	}()
}

// pushSummary reduces git's output to the line worth showing.
func pushSummary(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
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
