package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The browse limit only holds while a window cannot make a folder known by
// other means: opening it as a project, adding it to a group, or pointing a
// worktree, git, gh, conversation or tab command at it. These tests drive a
// window dialled through the tunnel at each of those.

// openProject takes any absolute path, and the project it opens makes the
// whole folder browsable: the browse limit is undone in two commands.
func TestARelayWindowCannotOpenAProjectToBrowseWithIt(t *testing.T) {
	srv, _ := newTestServer(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "secret-folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := projectRoots(srv)
	phone := dialPhone(t, srv)

	sendCmd(t, phone, command{Cmd: "openProject", Path: outside})
	wantRefusal(t, phone, "openProject outside the known projects")
	if got := projectRoots(srv); len(got) != len(before) {
		t.Errorf("a relay window opened a project: %v, was %v", got, before)
	}
	if m := browseThrough(t, phone, outside); m.Error == "" || len(m.Entries) != 0 {
		t.Errorf("browse after openProject listed %d folders (error %q), want a refusal", len(m.Entries), m.Error)
	}

	// A folder inside a project already open is still opened, which is what
	// the picker does after a browse inside it.
	known := before[0]
	sub := filepath.Join(known, "inside-folder")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	sendCmd(t, phone, command{Cmd: "openProject", Path: sub})
	var note noticeMsg
	readQuick(t, phone, "notice", &note)
	if note.Error {
		t.Errorf("openProject of a folder inside a known project was refused: %+v", note)
	}

	// The desk's window opens anything, as it always has.
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "openProject", Path: outside})
	readQuick(t, desk, "notice", &note)
	if note.Error {
		t.Errorf("the desk's openProject was refused: %+v", note)
	}
}

func TestARelayWindowCannotAddARepoOutsideToAGroup(t *testing.T) {
	srv, ws := newTestServer(t)
	outside := t.TempDir()
	known, _ := ask(srv, func() string { return ws.ActiveRoot() })
	phone := dialPhone(t, srv)
	memberCount := func() int {
		n, _ := ask(srv, func() int {
			n := 0
			for _, p := range ws.Projects() {
				n += len(p.Members)
			}
			return n
		})
		return n
	}
	before := memberCount()

	sendCmd(t, phone, command{Cmd: "addRepoToGroup", Root: known, Path: outside})
	wantRefusal(t, phone, "addRepoToGroup outside the known projects")
	if got := memberCount(); got != before {
		t.Errorf("a relay window added a repo to a group: %d members, was %d", got, before)
	}
	if m := browseThrough(t, phone, outside); m.Error == "" {
		t.Errorf("browse after addRepoToGroup listed %d folders, want a refusal", len(m.Entries))
	}
}

// The worktree commands take a Root and a Path from the window: git is run in
// the one and a directory is made or deleted at the other.
func TestARelayWindowWorktreesStayInKnownProjects(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	other := plainRepo(t, false)
	phone := dialPhone(t, srv)
	stray := filepath.Join(t.TempDir(), "stray-wt")

	// A repo that is not a known project: every command is refused, and no
	// directory appears.
	for _, cmd := range []command{
		{Cmd: "worktrees", Root: other},
		{Cmd: "worktreeAdd", Root: other, Text: "stage0-a"},
		{Cmd: "worktreeAdd", Root: other, Text: "stage0-b", Path: stray},
		{Cmd: "worktreePrune", Root: other},
		{Cmd: "worktreeRemove", Root: other, Path: other, Force: true},
	} {
		sendCmd(t, phone, cmd)
		wantRefusal(t, phone, cmd.Cmd+" at a repo that is not a known project")
	}
	if _, err := os.Stat(stray); err == nil {
		t.Error("a relay window made a worktree outside the known projects")
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a relay window removed a repo outside the known projects: %v", err)
	}

	// A known repo with a path elsewhere is refused too.
	sendCmd(t, phone, command{Cmd: "worktreeAdd", Root: repo, Text: "stage0-c", Path: stray})
	wantRefusal(t, phone, "worktreeAdd at a path outside the project")
	if _, err := os.Stat(stray); err == nil {
		t.Error("a relay window made a worktree at a path outside the project")
	}
	victim := plainRepo(t, false)
	sendCmd(t, phone, command{Cmd: "worktreeRemove", Root: repo, Path: victim, Force: true})
	wantRefusal(t, phone, "worktreeRemove of a folder that is not a worktree of the project")
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("a relay window removed a folder that is not a worktree: %v", err)
	}

	// The usual way still works: the default place beside the repo.
	sendCmd(t, phone, command{Cmd: "worktreeAdd", Root: repo, Text: "stage0-ok"})
	var note noticeMsg
	readQuick(t, phone, "notice", &note)
	if note.Error {
		t.Fatalf("worktreeAdd at the default path was refused: %+v", note)
	}
	made := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-stage0-ok")
	t.Cleanup(func() { os.RemoveAll(made) })
	if _, err := os.Stat(made); err != nil {
		t.Fatalf("the worktree was not made at %s: %v", made, err)
	}
	sendCmd(t, phone, command{Cmd: "worktreeRemove", Root: repo, Path: made, Force: true})
	readQuick(t, phone, "notice", &note)
	if note.Error {
		t.Errorf("worktreeRemove of the project's own worktree was refused: %+v", note)
	}
}

// Every other command that names a folder is held to the same rule.
func TestARelayWindowPathCommandsStayInKnownProjects(t *testing.T) {
	srv, _, _ := newRepoServer(t)
	other := plainRepo(t, false)
	phone := dialPhone(t, srv)
	tabs := tabCount(srv)

	for _, name := range []string{
		"changes", "diff", "commit", "gitPush", "gitPull", "gitFetch",
		"ghStatus", "ghLogout", "ghPRs", "ghPR", "ghPRCreate", "ghPRComment",
		"ghIssues", "ghIssue", "ghIssueCreate", "ghIssueComment", "ghChecks",
		"resumeConversation", "newTab", "splitPane",
		// "conversations" is refused outright to a relay window until the
		// scrub task is done (relayscrub.go), in its own words.
	} {
		sendCmd(t, phone, command{Cmd: name, Path: other, ID: "x", Text: "x"})
		wantRefusal(t, phone, name+" at a folder that is not a known project")
	}
	sendCmd(t, phone, command{Cmd: "splitPane", Root: other})
	wantRefusal(t, phone, "splitPane into a project that is not known")
	if got := tabCount(srv); got != tabs {
		t.Errorf("a relay window opened %d tabs outside the known projects", got-tabs)
	}
}

// A link out of a project is no more openable than browsable. On Windows it is
// a directory junction, which filepath.EvalSymlinks does not follow.
func TestARelayWindowCannotOpenAProjectThroughALink(t *testing.T) {
	srv, ws := newTestServer(t)
	known := ws.ActiveRoot()
	outside := t.TempDir()
	link := filepath.Join(known, "way-out")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
			t.Skipf("mklink /J: %v: %s", err, out)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	phone := dialPhone(t, srv)
	sendCmd(t, phone, command{Cmd: "openProject", Path: link})
	wantRefusal(t, phone, "openProject through a link out of a project")
}

// Opening the project that is open, spelt in other case, switches to it.
func TestARelayWindowOpenProjectFoldsCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case folding is Windows' rule")
	}
	srv, ws := newTestServer(t)
	known := ws.ActiveRoot()
	phone := dialPhone(t, srv)
	before := projectRoots(srv)
	sendCmd(t, phone, command{Cmd: "openProject", Path: strings.ToLower(known)})
	var note noticeMsg
	readQuick(t, phone, "notice", &note)
	if note.Error || len(projectRoots(srv)) != len(before) {
		t.Errorf("openProject of the open project in other case: %+v, projects %v", note, projectRoots(srv))
	}
}
