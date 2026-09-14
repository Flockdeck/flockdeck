package server

import (
	"context"
	"sync"

	"github.com/jmwri/flockdeck/internal/ghcli"
	"github.com/jmwri/flockdeck/internal/gitx"
)

// GitHub panel: gh installed?, signed in?, and the pull requests, issues and
// CI status of whatever checkout is on screen, all without leaving Flockdeck.
// It is built on internal/ghcli the way the review panel (changes.go) is
// built on internal/gitx -- gh, run in the background, answered with a JSON
// message the panel renders.
//
// Two things gh does are interactive by nature -- installing itself through a
// package manager, and gh auth login, which waits on a person approving in
// their browser -- and both stream their progress back as a run of messages
// rather than one answer, so the panel can show it happening rather than a
// spinner with nothing to say. ghState below is what keeps a second attempt
// from starting a redundant install or login while one is already running.

// The rest of this file calls gh only through the package-level variables
// below, the same seam readWorktrees gives gitx in worktrees.go: a test fakes
// gh's answers by reassigning one of these, rather than needing the real
// binary on the machine running the tests.
var (
	ghInstalled       = ghcli.Installed
	ghDetectInstaller = ghcli.DetectInstaller
	ghTryInstall      = ghcli.TryInstall
	ghGetAuthStatus   = ghcli.GetAuthStatus
	ghLoginFn         = ghcli.Login
	ghLogoutFn        = ghcli.Logout
	ghListPRs         = ghcli.ListPRs
	ghViewPR          = ghcli.ViewPR
	ghCurrentPR       = ghcli.CurrentPR
	ghCreatePR        = ghcli.CreatePR
	ghCommentPR       = ghcli.CommentPR
	ghListIssues      = ghcli.ListIssues
	ghViewIssue       = ghcli.ViewIssue
	ghCreateIssue     = ghcli.CreateIssue
	ghCommentIssue    = ghcli.CommentIssue
	ghPRChecks        = ghcli.PRChecks
	ghListRuns        = ghcli.ListRuns
	ghSummarize       = ghcli.Summarize
	ghCurrentBranch   = gitx.CurrentBranch
)

// ghState is the server's part in the GitHub panel: whether an install or a
// login is running right now, so a second click on the button does not start
// a second one, and how to cancel the one that is.
type ghState struct {
	mu     sync.Mutex
	busy   bool
	cancel context.CancelFunc
}

// begin claims ghState for an install or a login, returning the function to
// call when it ends and whether the claim succeeded -- it fails when one is
// already running. The caller sets g.cancel itself, under g.mu, once it has
// something worth cancelling (Login does; TryInstall is not told to, since
// gh has no way to ask a package manager to stop cleanly partway through).
func (g *ghState) begin() (done func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy {
		return nil, false
	}
	g.busy = true
	return func() {
		g.mu.Lock()
		g.busy, g.cancel = false, nil
		g.mu.Unlock()
	}, true
}

// ---------------------------------------------------------------------------
// Status, install and login
// ---------------------------------------------------------------------------

// ghStatusMsg answers the panel's own header: whether gh is on this machine
// and signed in, and, when it is not installed, how it could be.
type ghStatusMsg struct {
	Type string `json:"type"`
	// Cwd is the working tree this status was read for, echoed back the same
	// way changesMsg.Cwd is: the panel sends it on every later request, so an
	// install or a login started against one project answers about that
	// project even if another has come to the front by the time it finishes.
	Cwd       string `json:"cwd"`
	Installed bool   `json:"installed"`
	LoggedIn  bool   `json:"loggedIn"`
	Account   string `json:"account,omitempty"`
	Host      string `json:"host,omitempty"`
	// InstallManager and InstallURL are DetectInstaller's answer, for the
	// panel's install button and its "or get it yourself" fallback link.
	InstallManager string `json:"installManager,omitempty"`
	InstallURL     string `json:"installUrl,omitempty"`
	Busy           bool   `json:"busy"`
	Error          string `json:"error,omitempty"`
}

// ghStatus answers the panel's own header.
func (s *Server) ghStatus(c *controlClient, path string) {
	dir := s.reviewDir(path)
	go func() {
		defer s.surviveFor(c, "reading GitHub CLI status")
		c.sendJSON(s.buildGHStatus(dir))
	}()
}

func (s *Server) buildGHStatus(dir string) ghStatusMsg {
	msg := ghStatusMsg{Type: "ghStatus", Cwd: dir}
	s.gh.mu.Lock()
	msg.Busy = s.gh.busy
	s.gh.mu.Unlock()
	if !ghInstalled() {
		in := ghDetectInstaller()
		msg.InstallManager, msg.InstallURL = in.Manager, in.URL
		return msg
	}
	msg.Installed = true
	st, err := ghGetAuthStatus(dir)
	if err != nil {
		msg.Error = err.Error()
		return msg
	}
	msg.LoggedIn, msg.Account, msg.Host = st.LoggedIn, st.Account, st.Host
	return msg
}

// ghProgressMsg is one line of an install or a login as it runs, and its
// last message says how it ended. Kind is "install" or "login" so one
// listener in the panel can drive both dialogs.
type ghProgressMsg struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
	Line string `json:"line,omitempty"`
	// Code is set only for a login, the moment gh has a one-time code to
	// show at github.com/login/device.
	Code  string `json:"code,omitempty"`
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`
}

// ghInstall runs the install this machine's package manager offers,
// streaming its output to the panel a line at a time, then answers with a
// fresh ghStatus either way -- installed and ready, or still not, with the
// manual URL to fall back on.
func (s *Server) ghInstall(c *controlClient) {
	done, ok := s.gh.begin()
	if !ok {
		c.notify("an install or a sign-in is already running", true)
		return
	}
	in := ghDetectInstaller()
	go func() {
		defer done()
		defer s.surviveFor(c, "installing the GitHub CLI")
		err := ghTryInstall(context.Background(), in, func(line string) {
			c.sendJSON(ghProgressMsg{Type: "ghProgress", Kind: "install", Line: line})
		})
		final := ghProgressMsg{Type: "ghProgress", Kind: "install", Done: true}
		if err != nil {
			final.Error = err.Error()
		}
		c.sendJSON(final)
		c.sendJSON(s.buildGHStatus(s.reviewDir("")))
	}()
}

// ghLogin runs gh auth login --web, streaming its progress -- above all, the
// one-time code -- to the panel as it happens, then answers with a fresh
// ghStatus.
func (s *Server) ghLogin(c *controlClient) {
	doneFn, ok := s.gh.begin()
	if !ok {
		c.notify("an install or a sign-in is already running", true)
		return
	}
	s.gh.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	s.gh.cancel = cancel
	s.gh.mu.Unlock()
	go func() {
		defer doneFn()
		defer s.surviveFor(c, "signing in to GitHub")
		err := ghLoginFn(ctx, func(ev ghcli.LoginEvent) {
			c.sendJSON(ghProgressMsg{Type: "ghProgress", Kind: "login", Code: ev.Code, Line: ev.Line, Done: ev.Done})
		})
		if err != nil {
			c.sendJSON(ghProgressMsg{Type: "ghProgress", Kind: "login", Done: true, Error: err.Error()})
		}
		c.sendJSON(s.buildGHStatus(s.reviewDir("")))
	}()
}

// ghLoginCancel stops a login in progress, for the panel's own Cancel button
// -- someone who decided against it partway through would otherwise have to
// wait out loginTimeout for the dialog to let go.
func (s *Server) ghLoginCancel(c *controlClient) {
	s.gh.mu.Lock()
	cancel := s.gh.cancel
	s.gh.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ghLogout signs this machine out of github.com.
func (s *Server) ghLogout(c *controlClient, path string) {
	dir := s.reviewDir(path)
	go func() {
		defer s.surviveFor(c, "signing out of GitHub")
		if err := ghLogoutFn(dir); err != nil {
			c.notify(err.Error(), true)
		}
		c.sendJSON(s.buildGHStatus(dir))
	}()
}

// ---------------------------------------------------------------------------
// Pull requests and issues
// ---------------------------------------------------------------------------

// ghPRsMsg lists pull requests for the panel's list view.
type ghPRsMsg struct {
	Type  string     `json:"type"`
	Items []ghcli.PR `json:"items"`
	Cwd   string     `json:"cwd"`
	Error string     `json:"error,omitempty"`
}

func (s *Server) ghPRs(c *controlClient, path, state string) {
	dir := s.reviewDir(path)
	go func() {
		defer s.surviveFor(c, "listing pull requests")
		msg := ghPRsMsg{Type: "ghPRs", Cwd: dir}
		items, err := ghListPRs(dir, state, 0)
		if err != nil {
			msg.Error = err.Error()
		}
		msg.Items = items
		c.sendJSON(msg)
	}()
}

// ghPRMsg answers a single pull request's detail, with its comments.
type ghPRMsg struct {
	Type  string    `json:"type"`
	Item  *ghcli.PR `json:"item,omitempty"`
	Error string    `json:"error,omitempty"`
}

func (s *Server) ghPR(c *controlClient, path string, number int) {
	dir := s.reviewDir(path)
	go func() {
		defer s.surviveFor(c, "reading a pull request")
		msg := ghPRMsg{Type: "ghPR"}
		item, err := ghViewPR(dir, number)
		if err != nil {
			msg.Error = err.Error()
		}
		msg.Item = item
		c.sendJSON(msg)
	}()
}

func (s *Server) ghPRCreate(c *controlClient, path, title, body, base string, draft bool) {
	dir := s.reviewDir(path)
	if title == "" {
		c.notify("a pull request needs a title", true)
		return
	}
	go func() {
		defer s.surviveFor(c, "opening a pull request")
		msg := ghPRMsg{Type: "ghPR"}
		item, err := ghCreatePR(dir, ghcli.PRCreateOptions{Title: title, Body: body, Base: base, Draft: draft})
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		msg.Item = item
		c.sendJSON(msg)
		s.ghPRs(c, dir, "")
	}()
}

func (s *Server) ghPRComment(c *controlClient, path string, number int, body string) {
	dir := s.reviewDir(path)
	if body == "" {
		return
	}
	go func() {
		defer s.surviveFor(c, "commenting on a pull request")
		if _, err := ghCommentPR(dir, number, body); err != nil {
			c.notify(err.Error(), true)
			return
		}
		s.ghPR(c, dir, number)
	}()
}

// ghIssuesMsg lists issues for the panel's list view.
type ghIssuesMsg struct {
	Type  string        `json:"type"`
	Items []ghcli.Issue `json:"items"`
	Cwd   string        `json:"cwd"`
	Error string        `json:"error,omitempty"`
}

func (s *Server) ghIssues(c *controlClient, path, state string) {
	dir := s.reviewDir(path)
	go func() {
		defer s.surviveFor(c, "listing issues")
		msg := ghIssuesMsg{Type: "ghIssues", Cwd: dir}
		items, err := ghListIssues(dir, state, 0)
		if err != nil {
			msg.Error = err.Error()
		}
		msg.Items = items
		c.sendJSON(msg)
	}()
}

// ghIssueMsg answers a single issue's detail, with its comments.
type ghIssueMsg struct {
	Type  string       `json:"type"`
	Item  *ghcli.Issue `json:"item,omitempty"`
	Error string       `json:"error,omitempty"`
}

func (s *Server) ghIssue(c *controlClient, path string, number int) {
	dir := s.reviewDir(path)
	go func() {
		defer s.surviveFor(c, "reading an issue")
		msg := ghIssueMsg{Type: "ghIssue"}
		item, err := ghViewIssue(dir, number)
		if err != nil {
			msg.Error = err.Error()
		}
		msg.Item = item
		c.sendJSON(msg)
	}()
}

func (s *Server) ghIssueCreate(c *controlClient, path, title, body string) {
	dir := s.reviewDir(path)
	if title == "" {
		c.notify("an issue needs a title", true)
		return
	}
	go func() {
		defer s.surviveFor(c, "opening an issue")
		msg := ghIssueMsg{Type: "ghIssue"}
		item, err := ghCreateIssue(dir, ghcli.IssueCreateOptions{Title: title, Body: body})
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		msg.Item = item
		c.sendJSON(msg)
		s.ghIssues(c, dir, "")
	}()
}

func (s *Server) ghIssueComment(c *controlClient, path string, number int, body string) {
	dir := s.reviewDir(path)
	if body == "" {
		return
	}
	go func() {
		defer s.surviveFor(c, "commenting on an issue")
		if _, err := ghCommentIssue(dir, number, body); err != nil {
			c.notify(err.Error(), true)
			return
		}
		s.ghIssue(c, dir, number)
	}()
}

// ---------------------------------------------------------------------------
// CI / Actions status
// ---------------------------------------------------------------------------

// ghChecksMsg is the CI panel's own: the pull request open from the checked
// out branch, if there is one, with its checks, and the branch's recent
// Actions runs regardless -- a checkout with no PR yet still has CI to show.
type ghChecksMsg struct {
	Type    string              `json:"type"`
	Cwd     string              `json:"cwd"`
	Branch  string              `json:"branch"`
	PR      *ghcli.PR           `json:"pr,omitempty"`
	Checks  []ghcli.Check       `json:"checks,omitempty"`
	Summary ghcli.Summary       `json:"summary"`
	Runs    []ghcli.WorkflowRun `json:"runs,omitempty"`
	Error   string              `json:"error,omitempty"`
}

func (s *Server) ghChecks(c *controlClient, path string) {
	dir := s.reviewDir(path)
	go func() {
		defer s.surviveFor(c, "reading CI status")
		msg := ghChecksMsg{Type: "ghChecks", Cwd: dir}
		msg.Branch = ghCurrentBranch(dir)

		if pr, err := ghCurrentPR(dir); err != nil {
			msg.Error = err.Error()
		} else if pr != nil {
			msg.PR = pr
			if checks, err := ghPRChecks(dir, pr.Number); err == nil {
				msg.Checks = checks
				msg.Summary = ghSummarize(checks)
			} else if msg.Error == "" {
				msg.Error = err.Error()
			}
		}
		if runs, err := ghListRuns(dir, msg.Branch, 10); err == nil {
			msg.Runs = runs
		} else if msg.Error == "" {
			msg.Error = err.Error()
		}
		c.sendJSON(msg)
	}()
}
