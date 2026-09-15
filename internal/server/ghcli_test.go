package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/ghcli"
)

// fakeGH points every gh seam this file uses back at its original function
// once the test ends, the same shape readWorktrees's tests use for gitx.
func fakeGH(t *testing.T) {
	t.Helper()
	installed, detect, tryInstall := ghInstalled, ghDetectInstaller, ghTryInstall
	authStatus, login, logout := ghGetAuthStatus, ghLoginFn, ghLogoutFn
	listPRs, viewPR, currentPR := ghListPRs, ghViewPR, ghCurrentPR
	createPR, commentPR := ghCreatePR, ghCommentPR
	listIssues, viewIssue := ghListIssues, ghViewIssue
	createIssue, commentIssue := ghCreateIssue, ghCommentIssue
	prChecks, listRuns, summarize, branch := ghPRChecks, ghListRuns, ghSummarize, ghCurrentBranch
	t.Cleanup(func() {
		ghInstalled, ghDetectInstaller, ghTryInstall = installed, detect, tryInstall
		ghGetAuthStatus, ghLoginFn, ghLogoutFn = authStatus, login, logout
		ghListPRs, ghViewPR, ghCurrentPR = listPRs, viewPR, currentPR
		ghCreatePR, ghCommentPR = createPR, commentPR
		ghListIssues, ghViewIssue = listIssues, viewIssue
		ghCreateIssue, ghCommentIssue = createIssue, commentIssue
		ghPRChecks, ghListRuns, ghSummarize, ghCurrentBranch = prChecks, listRuns, summarize, branch
	})
}

// recvJSON reads the next message sent to c and decodes it into v, failing
// the test if none arrives within a few seconds -- everything ghcli.go
// answers with runs on its own goroutine.
func recvJSON(t *testing.T, c *controlClient, v any) {
	t.Helper()
	select {
	case raw := <-c.out:
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("could not decode %s: %v", raw, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message arrived")
	}
}

func TestGhStatusWhenNotInstalled(t *testing.T) {
	fakeGH(t)
	ghInstalled = func() bool { return false }
	ghDetectInstaller = func() ghcli.Installer {
		return ghcli.Installer{Manager: "winget", URL: "https://cli.github.com"}
	}

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 4)}
	srv.ghStatus(c, srv.activeRoot())

	var msg ghStatusMsg
	recvJSON(t, c, &msg)
	if msg.Type != "ghStatus" || msg.Installed || msg.LoggedIn {
		t.Errorf("got %+v, want not installed and not signed in", msg)
	}
	if msg.InstallManager != "winget" || msg.InstallURL != "https://cli.github.com" {
		t.Errorf("got %+v, want the detected installer carried along", msg)
	}
}

func TestGhStatusWhenSignedIn(t *testing.T) {
	fakeGH(t)
	ghInstalled = func() bool { return true }
	ghGetAuthStatus = func(string) (ghcli.AuthStatus, error) {
		return ghcli.AuthStatus{LoggedIn: true, Account: "octocat", Host: "github.com"}, nil
	}

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 4)}
	srv.ghStatus(c, srv.activeRoot())

	var msg ghStatusMsg
	recvJSON(t, c, &msg)
	if !msg.Installed || !msg.LoggedIn || msg.Account != "octocat" || msg.Host != "github.com" {
		t.Errorf("got %+v, want signed in as octocat", msg)
	}
}

func TestGhStatusCarriesAnAuthError(t *testing.T) {
	fakeGH(t)
	ghInstalled = func() bool { return true }
	ghGetAuthStatus = func(string) (ghcli.AuthStatus, error) { return ghcli.AuthStatus{}, errors.New("boom") }

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 4)}
	srv.ghStatus(c, srv.activeRoot())

	var msg ghStatusMsg
	recvJSON(t, c, &msg)
	if msg.Error != "boom" {
		t.Errorf("Error = %q, want it to carry what gh said", msg.Error)
	}
}

// TestGhInstallStreamsItsOutputThenAFreshStatus covers the whole point of
// ghProgressMsg: the panel is meant to watch an install happen, a line at a
// time, rather than stare at a spinner until it is over.
func TestGhInstallStreamsItsOutputThenAFreshStatus(t *testing.T) {
	fakeGH(t)
	ghDetectInstaller = func() ghcli.Installer {
		return ghcli.Installer{Manager: "winget", Command: []string{"winget", "install"}}
	}
	ghTryInstall = func(_ context.Context, _ ghcli.Installer, onLine func(string)) error {
		onLine("Downloading gh 33%")
		onLine("Downloading gh 100%, done.")
		return nil
	}
	ghInstalled = func() bool { return true }
	ghGetAuthStatus = func(string) (ghcli.AuthStatus, error) { return ghcli.AuthStatus{}, nil }

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	srv.ghInstall(c)

	var line1, line2, done ghProgressMsg
	recvJSON(t, c, &line1)
	recvJSON(t, c, &line2)
	recvJSON(t, c, &done)
	if line1.Line != "Downloading gh 33%" || line2.Line != "Downloading gh 100%, done." {
		t.Errorf("got lines %q, %q", line1.Line, line2.Line)
	}
	if !done.Done || done.Error != "" || done.Kind != "install" {
		t.Errorf("final progress = %+v, want a clean done", done)
	}
	var status ghStatusMsg
	recvJSON(t, c, &status)
	if status.Type != "ghStatus" || !status.Installed {
		t.Errorf("got %+v, want a fresh status after the install finished", status)
	}
}

func TestGhInstallRefusesASecondAttemptWhileOneIsRunning(t *testing.T) {
	fakeGH(t)
	started, release := make(chan struct{}), make(chan struct{})
	ghTryInstall = func(ctx context.Context, _ ghcli.Installer, _ func(string)) error {
		close(started)
		<-release
		return nil
	}
	ghInstalled = func() bool { return false }

	srv, _ := newTestServer(t)
	c1 := &controlClient{out: make(chan []byte, 8)}
	srv.ghInstall(c1)
	<-started

	c2 := &controlClient{out: make(chan []byte, 8)}
	srv.ghInstall(c2)
	var notice noticeMsg
	recvJSON(t, c2, &notice)
	if notice.Type != "notice" || !notice.Error {
		t.Errorf("got %+v, want an error notice refusing the second attempt", notice)
	}
	close(release)
}

// TestGhLoginCancelStopsTheFlow covers the panel's own Cancel button: without
// it, someone who backed out partway through a login would sit out
// loginTimeout for the dialog to let go.
func TestGhLoginCancelStopsTheFlow(t *testing.T) {
	fakeGH(t)
	ghLoginFn = func(ctx context.Context, onEvent func(ghcli.LoginEvent)) error {
		onEvent(ghcli.LoginEvent{Code: "1234-ABCD"})
		<-ctx.Done()
		return ctx.Err()
	}
	ghInstalled = func() bool { return true }
	ghGetAuthStatus = func(string) (ghcli.AuthStatus, error) { return ghcli.AuthStatus{}, nil }

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	srv.ghLogin(c)

	var withCode ghProgressMsg
	recvJSON(t, c, &withCode)
	if withCode.Code != "1234-ABCD" {
		t.Fatalf("got %+v, want the one-time code first", withCode)
	}

	srv.ghLoginCancel(c)

	var done ghProgressMsg
	recvJSON(t, c, &done)
	if !done.Done || done.Error == "" {
		t.Errorf("got %+v, want a cancelled login to end with an error", done)
	}
	var status ghStatusMsg
	recvJSON(t, c, &status)
	if status.Type != "ghStatus" {
		t.Errorf("got %+v, want a fresh status once the login ended", status)
	}
}

func TestGhInstallAndLoginRefuseEachOther(t *testing.T) {
	fakeGH(t)
	started, release := make(chan struct{}), make(chan struct{})
	ghLoginFn = func(ctx context.Context, onEvent func(ghcli.LoginEvent)) error {
		close(started)
		<-release
		return nil
	}
	ghInstalled = func() bool { return true }
	ghGetAuthStatus = func(string) (ghcli.AuthStatus, error) { return ghcli.AuthStatus{}, nil }

	srv, _ := newTestServer(t)
	c1 := &controlClient{out: make(chan []byte, 8)}
	srv.ghLogin(c1)
	<-started

	c2 := &controlClient{out: make(chan []byte, 8)}
	srv.ghInstall(c2)
	var notice noticeMsg
	recvJSON(t, c2, &notice)
	if !notice.Error {
		t.Errorf("got %+v, want the install refused while a login is running", notice)
	}
	close(release)
}

func TestGhPRsListsAndCarriesAnError(t *testing.T) {
	fakeGH(t)
	ghListPRs = func(dir, state string, limit int) ([]ghcli.PR, error) {
		if state == "closed" {
			return nil, errors.New("gh pr list: HTTP 401")
		}
		return []ghcli.PR{{Number: 7, Title: "Add gh support"}}, nil
	}

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 4)}
	srv.ghPRs(c, srv.activeRoot(), "")
	var msg ghPRsMsg
	recvJSON(t, c, &msg)
	if len(msg.Items) != 1 || msg.Items[0].Number != 7 {
		t.Errorf("got %+v, want the one open PR", msg)
	}

	srv.ghPRs(c, srv.activeRoot(), "closed")
	var errMsg ghPRsMsg
	recvJSON(t, c, &errMsg)
	if errMsg.Error == "" {
		t.Errorf("got %+v, want gh's error carried along", errMsg)
	}
}

func TestGhPRCreateThenListsAgain(t *testing.T) {
	fakeGH(t)
	ghCreatePR = func(dir string, opts ghcli.PRCreateOptions) (*ghcli.PR, error) {
		if opts.Title != "Add gh support" {
			t.Errorf("Title = %q", opts.Title)
		}
		return &ghcli.PR{Number: 9, Title: opts.Title}, nil
	}
	ghListPRs = func(string, string, int) ([]ghcli.PR, error) {
		return []ghcli.PR{{Number: 9, Title: "Add gh support"}}, nil
	}

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 4)}
	srv.ghPRCreate(c, srv.activeRoot(), "Add gh support", "body", "", false)

	var created ghPRMsg
	recvJSON(t, c, &created)
	if created.Item == nil || created.Item.Number != 9 {
		t.Fatalf("got %+v, want the new PR back", created)
	}
	var refreshed ghPRsMsg
	recvJSON(t, c, &refreshed)
	if len(refreshed.Items) != 1 {
		t.Errorf("got %+v, want the list refreshed after creating", refreshed)
	}
}

func TestGhPRCreateRefusesWithNoTitle(t *testing.T) {
	fakeGH(t)
	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 4)}
	srv.ghPRCreate(c, srv.activeRoot(), "", "body", "", false)

	var notice noticeMsg
	recvJSON(t, c, &notice)
	if !notice.Error {
		t.Errorf("got %+v, want a title required", notice)
	}
}

func TestGhPRCommentReReadsTheItem(t *testing.T) {
	fakeGH(t)
	var commented string
	ghCommentPR = func(dir string, number int, body string) (*ghcli.Comment, error) {
		commented = body
		return &ghcli.Comment{Body: body}, nil
	}
	ghViewPR = func(dir string, number int) (*ghcli.PR, error) {
		return &ghcli.PR{Number: number, Comments: []ghcli.Comment{{Body: commented}}}, nil
	}

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 4)}
	srv.ghPRComment(c, srv.activeRoot(), 5, "nice work")

	var msg ghPRMsg
	recvJSON(t, c, &msg)
	if msg.Item == nil || len(msg.Item.Comments) != 1 || msg.Item.Comments[0].Body != "nice work" {
		t.Errorf("got %+v, want the pull request re-read with the new comment", msg)
	}
}

func TestGhIssuesCreateAndComment(t *testing.T) {
	fakeGH(t)
	ghCreateIssue = func(dir string, opts ghcli.IssueCreateOptions) (*ghcli.Issue, error) {
		return &ghcli.Issue{Number: 3, Title: opts.Title}, nil
	}
	ghListIssues = func(string, string, int) ([]ghcli.Issue, error) {
		return []ghcli.Issue{{Number: 3, Title: "gh support"}}, nil
	}
	var commented string
	ghCommentIssue = func(dir string, number int, body string) (*ghcli.Comment, error) {
		commented = body
		return &ghcli.Comment{Body: body}, nil
	}
	ghViewIssue = func(dir string, number int) (*ghcli.Issue, error) {
		return &ghcli.Issue{Number: number, Comments: []ghcli.Comment{{Body: commented}}}, nil
	}

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	srv.ghIssueCreate(c, srv.activeRoot(), "gh support", "please")

	var created ghIssueMsg
	recvJSON(t, c, &created)
	if created.Item == nil || created.Item.Number != 3 {
		t.Fatalf("got %+v, want the new issue back", created)
	}
	var refreshed ghIssuesMsg
	recvJSON(t, c, &refreshed)
	if len(refreshed.Items) != 1 {
		t.Errorf("got %+v, want the list refreshed after creating", refreshed)
	}

	srv.ghIssueComment(c, srv.activeRoot(), 3, "thanks")
	var withComment ghIssueMsg
	recvJSON(t, c, &withComment)
	if withComment.Item == nil || len(withComment.Item.Comments) != 1 || withComment.Item.Comments[0].Body != "thanks" {
		t.Errorf("got %+v, want the issue re-read with the new comment", withComment)
	}
}

// TestGhChecksAssemblesThePRAndTheRunHistory covers what the CI panel shows
// for a checkout with a PR open: its checks, summarised, alongside the
// branch's own recent Actions runs regardless.
func TestGhChecksAssemblesThePRAndTheRunHistory(t *testing.T) {
	fakeGH(t)
	ghCurrentBranch = func(string) string { return "feature" }
	ghCurrentPR = func(string) (*ghcli.PR, error) { return &ghcli.PR{Number: 4}, nil }
	ghPRChecks = func(dir string, number int) ([]ghcli.Check, error) {
		if number != 4 {
			t.Errorf("number = %d, want 4", number)
		}
		return []ghcli.Check{{Bucket: "pass"}, {Bucket: "fail"}}, nil
	}
	ghListRuns = func(dir, branch string, limit int) ([]ghcli.WorkflowRun, error) {
		if branch != "feature" {
			t.Errorf("branch = %q, want feature", branch)
		}
		return []ghcli.WorkflowRun{{WorkflowName: "CI"}}, nil
	}

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 4)}
	srv.ghChecks(c, srv.activeRoot())

	var msg ghChecksMsg
	recvJSON(t, c, &msg)
	if msg.Branch != "feature" || msg.PR == nil || msg.PR.Number != 4 {
		t.Fatalf("got %+v, want the branch and its PR", msg)
	}
	if msg.Summary.Passing != 1 || msg.Summary.Failing != 1 || msg.Summary.Overall != "failing" {
		t.Errorf("Summary = %+v, want one passing and one failing check", msg.Summary)
	}
	if len(msg.Runs) != 1 || msg.Runs[0].WorkflowName != "CI" {
		t.Errorf("got %+v, want the branch's own run history", msg.Runs)
	}
}

// TestGhChecksWithNoPRStillShowsRunHistory covers a checkout whose branch has
// not been opened as a pull request yet: there is nothing to summarise, but
// its own Actions runs, if any, are still worth showing.
func TestGhChecksWithNoPRStillShowsRunHistory(t *testing.T) {
	fakeGH(t)
	ghCurrentBranch = func(string) string { return "feature" }
	ghCurrentPR = func(string) (*ghcli.PR, error) { return nil, nil }
	ghListRuns = func(dir, branch string, limit int) ([]ghcli.WorkflowRun, error) {
		return []ghcli.WorkflowRun{{WorkflowName: "CI"}}, nil
	}

	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 4)}
	srv.ghChecks(c, srv.activeRoot())

	var msg ghChecksMsg
	recvJSON(t, c, &msg)
	if msg.PR != nil {
		t.Errorf("PR = %+v, want none", msg.PR)
	}
	if len(msg.Runs) != 1 {
		t.Errorf("got %+v, want the run history even with no PR", msg.Runs)
	}
}

func TestGhStateBeginRefusesASecondClaim(t *testing.T) {
	var g ghState
	done, ok := g.begin()
	if !ok {
		t.Fatal("the first claim should succeed")
	}
	if _, ok := g.begin(); ok {
		t.Error("a second claim should be refused while the first is running")
	}
	done()
	if _, ok := g.begin(); !ok {
		t.Error("a claim after done() should succeed again")
	}
}
