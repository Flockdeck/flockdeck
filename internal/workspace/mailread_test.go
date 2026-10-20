package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/review"
	"github.com/jmwri/flockdeck/internal/session"
)

func bashEvent(t *testing.T, ws *Workspace, p *Pane, cmd string) hooks.Event {
	t.Helper()
	in, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	return hooks.Event{Tool: "Bash", ToolInput: string(in), Cwd: t.TempDir(), Launch: p.launch}
}

// fakeApp writes an executable called flockdeck in a new directory and returns
// the directory and the path to it.
func fakeApp(t *testing.T) (dir, exe string) {
	t.Helper()
	dir = t.TempDir()
	name := "flockdeck"
	if runtime.GOOS == "windows" {
		name = "flockdeck.exe"
	}
	exe = filepath.Join(dir, name)
	if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, exe
}

// Reading Flockdeck mail is allowed in a pane whether or not auto-approve is
// on, without counting as an auto-approval, and nothing else is let through by
// it.
func TestReviewToolAllowsReadingMailInEveryPane(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	appDir, app := fakeApp(t)
	ws, err := New(Options{Root: root, HookBinary: app})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	tab := ws.NewTab(session.KindShell, root, "shell")
	p := ws.Pane(tab.Tree.Panes()[0])
	// The pane's PATH is read when a call is checked, so it is set once the
	// pane is running, and has the application on it.
	t.Setenv("PATH", appDir)
	// An auto-approve reviewer that would allow anything it was asked, so
	// that a pane with the switch on is seen to reach it for other commands.
	ws.reviewer = func(tool, input, cwd string) review.Decision {
		return review.Decision{Allow: true, Reason: "auto-approve read-only: test"}
	}

	for _, on := range []bool{false, true} {
		ws.mu.Lock()
		p.AutoReview = on
		ws.mu.Unlock()
		before := ws.AutoApprovedOf(p)

		for _, cmd := range []string{"flockdeck inbox", "flockdeck inbox read 12"} {
			ok, reason := ws.reviewTool(p.ID, bashEvent(t, ws, p, cmd))
			if !ok || reason != "allow: reading Flockdeck mail" {
				t.Errorf("auto-approve %v, %q: got %v %q, want allowed as mail", on, cmd, ok, reason)
			}
		}
		if got := ws.AutoApprovedOf(p); got != before {
			t.Errorf("auto-approve %v: AutoApproved went from %d to %d on mail reads", on, before, got)
		}

		ok, reason := ws.reviewTool(p.ID, bashEvent(t, ws, p, "cat file"))
		if ok != on {
			t.Errorf("auto-approve %v, cat file: allowed = %v", on, ok)
		}
		if on && reason != "auto-approve read-only: test" {
			t.Errorf("cat file reason = %q, want the reviewer's", reason)
		}
		if on && ws.AutoApprovedOf(p) != before+1 {
			t.Errorf("an auto-approved call was not counted")
		}
		ws.mu.Lock()
		p.AutoApproved = 0
		ws.mu.Unlock()
	}

	// Calls near the mail forms are asked about with the switch off.
	ws.mu.Lock()
	p.AutoReview = false
	ws.mu.Unlock()
	for _, cmd := range []string{"flockdeck inbox -wait 30s", "flockdeck msg send x y", "./flockdeck inbox", "flockdeck inbox; id"} {
		if ok, _ := ws.reviewTool(p.ID, bashEvent(t, ws, p, cmd)); ok {
			t.Errorf("%q was allowed", cmd)
		}
	}
}

// A call from a session that is not a known pane, or from an earlier launch of
// one, is asked about even for a mail read.
func TestReviewToolAsksAboutMailFromAnUnknownOrStaleSession(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	tab := ws.NewTab(session.KindShell, root, "shell")
	p := ws.Pane(tab.Tree.Panes()[0])

	ev := bashEvent(t, ws, p, "flockdeck inbox")
	if ok, _ := ws.reviewTool("no-such-session", ev); ok {
		t.Error("a mail read from an unknown session was allowed")
	}
	ev.Launch = "an-earlier-launch"
	if ok, _ := ws.reviewTool(p.ID, ev); ok {
		t.Error("a mail read from a stale launch was allowed")
	}
}

// The check is replaceable in a test the way reviewer is, and defaults to
// review.MailRead.
func TestMailReaderDefaultsToMailRead(t *testing.T) {
	isolateConfig(t)
	ws, err := New(Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	if ws.mailReader == nil {
		t.Fatal("no mail check by default")
	}
	in := `{"command":"flockdeck inbox"}`
	appDir, app := fakeApp(t)
	ws.selfExe = app
	t.Setenv("PATH", appDir)
	env := review.MailEnv{Path: appDir, Self: app}
	if got, want := ws.mailReader("Bash", in, t.TempDir()), review.MailRead("Bash", in, t.TempDir(), env); got != want || !got.Allow {
		t.Errorf("default = %+v, want review.MailRead's %+v, allowed", got, want)
	}
}

// A flockdeck earlier on the pane's PATH than the application, or none on it,
// is asked about, with auto-approve off, and the application's own is not.
func TestReviewToolAsksAboutMailWhenFlockdeckOnThePathIsNotTheApp(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	appDir, app := fakeApp(t)
	ws, err := New(Options{Root: root, HookBinary: app})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	tab := ws.NewTab(session.KindShell, root, "shell")
	p := ws.Pane(tab.Tree.Panes()[0])
	planted, _ := fakeApp(t)
	sep := string(os.PathListSeparator)

	for name, c := range map[string]struct {
		path  string
		allow bool
	}{
		"the application":           {appDir, true},
		"a planted program first":   {planted + sep + appDir, false},
		"a planted program only":    {planted, false},
		"no flockdeck on the PATH":  {t.TempDir(), false},
		"the application then more": {appDir + sep + planted, true},
	} {
		t.Setenv("PATH", c.path)
		ok, _ := ws.reviewTool(p.ID, bashEvent(t, ws, p, "flockdeck inbox"))
		if ok != c.allow {
			t.Errorf("%s: allowed = %v, want %v", name, ok, c.allow)
		}
	}
}
