package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/baton"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// batonAgent adds an agent that runs `go`, which says it has no such command and
// names the session it was started under, so a test can see what a start gave
// it with no real agent and no network. It writes into the test's own state
// folder, which newTestServer has already pointed away from the real one.
func batonAgent(t *testing.T, ws *workspace.Workspace) {
	t.Helper()
	path, err := agent.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed, so there is no second company's agent to start")
	}
	body := `{"version": 1, "agents": [{"id": "gocli", "name": "Go", "exe": "go", "args": [{"value": "{{session}}"}], "caps": {"resume": true}},
		{"id": "gitcli", "name": "Git as an agent", "exe": "git", "args": [{"value": "{{session}}"}]}]}`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ws.ReloadAgents()
}

// paneDirs is the folder each test pane works in, by pane id: a notes file an agent
// names has to be inside it (see Workspace.BatonPathInScope).
var paneDirs sync.Map

func spawnGoPane(t *testing.T, srv *Server, ws *workspace.Workspace) string {
	t.Helper()
	lead := firstPane(t, srv, ws)
	id, ok := ask(srv, func() string {
		id, err := ws.Spawn(lead, workspace.SpawnOptions{Task: "port the parser", Agent: "gocli"})
		if err != nil {
			t.Error(err)
		}
		return id
	})
	if !ok || id == "" {
		t.Fatal("the agent pane did not start")
	}
	if cwd, ok := ask(srv, func() string { return ws.Pane(id).Cwd }); ok {
		paneDirs.Store(id, cwd)
	}
	return id
}

func TestMakingEditingAndRestartingFromABaton(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialControl(t, srv)
	pane := spawnGoPane(t, srv, ws)
	before, _ := ask(srv, func() string { return ws.ConversationOf(pane) })

	sendCmd(t, conn, command{Cmd: "makeBaton", ID: pane})
	var draft batonDraftMsg
	readUntil(t, conn, "batonDraft", &draft)
	if draft.PaneID != pane || draft.Agent != "gocli" || !strings.Contains(draft.Text, "# Baton: port the parser") {
		t.Fatalf("draft = %+v", draft)
	}
	// `go` has no reader for its conversation, and the window is told so.
	if len(draft.Notes) != 1 || !strings.Contains(draft.Notes[0], "no conversation") {
		t.Errorf("notes = %v", draft.Notes)
	}
	if draft.Provider != "go" || len(draft.Agents) == 0 {
		t.Errorf("provider = %q, agents = %d", draft.Provider, len(draft.Agents))
	}

	// The person edits it, and pastes a token without noticing.
	edited := strings.Replace(draft.Text, "## Decisions and why\n\n"+baton.Placeholder,
		"## Decisions and why\n\n- Use the cache. The deploy token is gh"+"p_0123456789abcdefghijklmnopqrstuvwxyz", 1)
	if edited == draft.Text {
		t.Fatalf("the draft has no decisions section to edit:\n%s", draft.Text)
	}
	sendCmd(t, conn, command{Cmd: "restartWithBaton", ID: pane, Text: edited, Task: "carry on with the cache"})
	var saved batonSavedMsg
	readUntil(t, conn, "batonSaved", &saved)
	if saved.Started != pane || saved.Scrubbed != 1 || !baton.ValidID(saved.ID) {
		t.Fatalf("saved = %+v", saved)
	}
	kept, err := os.ReadFile(saved.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(kept), "gh"+"p_0123") || !strings.Contains(string(kept), "Use the cache.") || !strings.Contains(string(kept), "[REDACTED: github-token]") {
		t.Errorf("the kept baton:\n%s", kept)
	}
	after, _ := ask(srv, func() string { return ws.ConversationOf(pane) })
	if after == before || after == pane {
		t.Errorf("conversation %q after the restart, was %q", after, before)
	}
	// Saving the same text again gives a second baton, not a changed first.
	sendCmd(t, conn, command{Cmd: "saveBaton", ID: pane, Text: string(kept)})
	var again batonSavedMsg
	readUntil(t, conn, "batonSaved", &again)
	if again.ID == saved.ID || again.Started != "" {
		t.Errorf("a saved baton was reused or started something: %+v", again)
	}
}

func TestABatonWithoutItsHeaderIsRefused(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialControl(t, srv)
	pane := spawnGoPane(t, srv, ws)
	sendCmd(t, conn, command{Cmd: "saveBaton", ID: pane, Text: "# Baton: no header\n\n## Goal\n\nx\n"})
	var n noticeMsg
	readUntil(t, conn, "notice", &n)
	if !n.Error || !strings.Contains(n.Text, "header") {
		t.Errorf("notice = %+v", n)
	}
	// The dialog is told its own command failed, apart from any other notice.
	var e batonErrorMsg
	readUntil(t, conn, "batonError", &e)
	if e.Cmd != "saveBaton" || e.PaneID != pane || !strings.Contains(e.Error, "header") {
		t.Errorf("batonError = %+v", e)
	}
}

func TestAShellHasNoBatonToMake(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "makeBaton", ID: firstPane(t, srv, ws)})
	var n noticeMsg
	readUntil(t, conn, "notice", &n)
	if !n.Error || !strings.Contains(n.Text, "shell") {
		t.Errorf("notice = %+v", n)
	}
	var e batonErrorMsg
	readUntil(t, conn, "batonError", &e)
	if e.Cmd != "makeBaton" {
		t.Errorf("batonError = %+v", e)
	}
}

func TestStartingFromABatonRunsTheAgentInANewPane(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialControl(t, srv)
	pane := spawnGoPane(t, srv, ws)
	sendCmd(t, conn, command{Cmd: "makeBaton", ID: pane})
	var draft batonDraftMsg
	readUntil(t, conn, "batonDraft", &draft)

	sendCmd(t, conn, command{Cmd: "startFromBaton", ID: pane, Text: draft.Text, Agent: "gocli", Task: "the second half"})
	var saved batonSavedMsg
	readUntil(t, conn, "batonSaved", &saved)
	if saved.Started == "" || saved.Started == pane {
		t.Fatalf("saved = %+v", saved)
	}
	got, _ := ask(srv, func() [2]string {
		p := ws.Pane(saved.Started)
		return [2]string{p.BatonID, p.Task}
	})
	if got[0] != saved.ID || got[1] != "the second half" {
		t.Errorf("new pane has baton %q and task %q", got[0], got[1])
	}
	// A baton cannot be started on an agent that is not there.
	sendCmd(t, conn, command{Cmd: "startFromBaton", ID: pane, Text: draft.Text, Agent: "no-such-agent"})
	var n noticeMsg
	readUntil(t, conn, "notice", &n)
	if !n.Error {
		t.Errorf("notice = %+v", n)
	}
}

func TestResolveBatonHoldsAnAgentsReferenceToTheStrictProfile(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	pane := spawnGoPane(t, srv, ws)

	// An empty reference is no baton at all.
	if b, err := srv.resolveBaton(pane, ""); b != nil || err != nil {
		t.Errorf("empty reference = %v, %v", b, err)
	}
	// self makes one from the caller, with command arguments dropped.
	self, err := srv.resolveBaton(pane, "self")
	if err != nil || self.Section(baton.Goal) != "port the parser" {
		t.Fatalf("self = %+v, %v", self, err)
	}
	// A shell is not an agent to make one from.
	if _, err := srv.resolveBaton(firstPane(t, srv, ws), "self"); err == nil {
		t.Error("a shell made a baton from itself")
	}

	// A stored baton named by id comes back with its commands cut to programs
	// and its secrets gone, under an id of its own.
	loose := baton.Baton{
		ID: baton.NewID(self.Created), Title: "t",
		Sections: map[baton.Section]string{
			baton.Goal:     "do it with gh" + "p_0123456789abcdefghijklmnopqrstuvwxyz",
			baton.Commands: "- `curl https://example.com/hook?k=1` - FAILED: 500",
		},
	}
	if err := workspace.SaveBaton(loose); err != nil {
		t.Fatal(err)
	}
	got, err := srv.resolveBaton(pane, loose.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == loose.ID || got.Derived[len(got.Derived)-1] != loose.ID {
		t.Errorf("a rewritten baton kept the id: %q, derived %v", got.ID, got.Derived)
	}
	if strings.Contains(got.Section(baton.Goal), "ghp_") || got.Section(baton.Commands) != "- `curl ...` - FAILED" {
		t.Errorf("baton = %+v", got.Sections)
	}

	// A file is read only when it is an absolute path to notes inside the project.
	notes := filepath.Join(paneDir(t, pane), "notes.md")
	if err := os.WriteFile(notes, []byte("finish the thing; key is gh"+"p_0123456789abcdefghijklmnopqrstuvwxyz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := srv.resolveBaton(pane, "path:"+notes)
	if err != nil || !baton.ValidID(fromFile.ID) || strings.Contains(fromFile.Section(baton.Standing), "ghp_") {
		t.Fatalf("file baton = %+v, %v", fromFile, err)
	}
	for _, ref := range []string{"path:notes.md", "path:" + filepath.Join(t.TempDir(), "x.sh"), "path:" + filepath.Join(t.TempDir(), "missing.md"), "no-such-pane"} {
		if _, err := srv.resolveBaton(pane, ref); err == nil {
			t.Errorf("%q was resolved", ref)
		}
	}
}

// The spawn an agent runs from its pane goes over the hook server's own
// address and token, which is how this test makes it.
func TestSpawnWithBatonOverTheCallbackAPI(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}

	res, err := hooks.Spawn(hook.BaseURL(), hook.Token(), pane, hooks.SpawnRequest{Task: "the refresh part", Agent: "gocli", Baton: "self"})
	if err != nil {
		t.Fatal(err)
	}
	if !baton.ValidID(res.BatonID) || res.BatonPath == "" {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(res.BatonPath); err != nil {
		t.Errorf("the baton is not where the result says: %v", err)
	}
	got, _ := ask(srv, func() [2]string {
		p := ws.Pane(res.PaneID)
		return [2]string{p.BatonID, p.Task}
	})
	if got[0] != res.BatonID || got[1] != "the refresh part" {
		t.Errorf("pane has baton %q, task %q", got[0], got[1])
	}

	// A shell is not an agent, and a reference that does not resolve starts
	// nothing.
	if _, err := hooks.Spawn(hook.BaseURL(), hook.Token(), pane, hooks.SpawnRequest{Task: "x", Shell: true, Baton: "self"}); err == nil {
		t.Error("a shell was started from a baton")
	}
	if _, err := hooks.Spawn(hook.BaseURL(), hook.Token(), pane, hooks.SpawnRequest{Task: "x", Agent: "gocli", Baton: "nothing-like-it"}); err == nil {
		t.Error("an unresolvable reference started an agent")
	}
}

func TestProviderChange(t *testing.T) {
	for _, c := range []struct {
		fromAgent, fromProv, toAgent, toProv string
		wantEmpty                            bool
	}{
		{"claude", "anthropic", "claude", "anthropic", true},
		{"claude", "anthropic", "claude-work", "anthropic", true},
		{"mine", "", "mine", "", true}, // the same agent, whatever it is
		{"claude", "anthropic", "codex", "openai", false},
		{"claude", "anthropic", "other", "", false},
		{"other", "", "claude", "anthropic", false},
	} {
		got := providerChange(c.fromAgent, c.fromProv, c.toAgent, c.toProv)
		if (got == "") != c.wantEmpty {
			t.Errorf("providerChange(%+v) = %q", c, got)
		}
	}
}

// The dialog asks for a tick before a baton goes to another company, and the
// server holds it to that: a command sent without it is refused.
func TestStartingFromABatonOnAnotherCompanysAgentNeedsTheTick(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialControl(t, srv)
	pane := spawnGoPane(t, srv, ws)
	sendCmd(t, conn, command{Cmd: "makeBaton", ID: pane})
	var draft batonDraftMsg
	readUntil(t, conn, "batonDraft", &draft)

	sendCmd(t, conn, command{Cmd: "startFromBaton", ID: pane, Text: draft.Text, Agent: "gitcli"})
	var e batonErrorMsg
	readUntil(t, conn, "batonError", &e)
	if e.Cmd != "startFromBaton" || !strings.Contains(e.Error, "different company") || !strings.Contains(e.Error, "from go to git") {
		t.Errorf("batonError = %+v", e)
	}
	// Nothing was started or kept.
	if st, err := baton.Open(); err != nil {
		t.Fatal(err)
	} else if list, _ := st.List(); len(list) != 0 {
		t.Errorf("%d batons were kept for a refused start", len(list))
	}
}

// An agent has no window to tick: a spawn that would send the baton to another
// company is refused unless it says so.
func TestSpawnWithBatonToAnotherCompanyNeedsTheFlag(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	for _, ref := range []string{"self", "path:" + writeNotesFor(t, pane)} {
		_, err := hooks.Spawn(hook.BaseURL(), hook.Token(), pane, hooks.SpawnRequest{Task: "x", Agent: "gitcli", Baton: ref})
		if err == nil || !strings.Contains(err.Error(), "-baton-send-elsewhere") || !strings.Contains(err.Error(), "different company") {
			t.Errorf("%s: err = %v, want the refusal naming the flag", ref, err)
		}
		// With the flag the refusal is not what stops it: starting the chat client
		// in a test may fail for other reasons.
		_, err = hooks.Spawn(hook.BaseURL(), hook.Token(), pane, hooks.SpawnRequest{Task: "x", Agent: "gitcli", Baton: ref, BatonElsewhere: true})
		if err != nil && strings.Contains(err.Error(), "different company") {
			t.Errorf("%s: the flag did not lift the refusal: %v", ref, err)
		}
	}
	// The same company needs no flag; the spawn from the earlier test shows it.
}

// writeNotesFor writes a notes file in the folder the pane works in.
func writeNotesFor(t *testing.T, pane string) string {
	t.Helper()
	path := filepath.Join(paneDir(t, pane), "notes-"+filepath.Base(t.TempDir())+".md")
	if err := os.WriteFile(path, []byte("finish the thing"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func paneDir(t *testing.T, pane string) string {
	t.Helper()
	dir, ok := paneDirs.Load(pane)
	if !ok {
		t.Fatalf("no folder is known for pane %s", pane)
	}
	return dir.(string)
}

func writeNotes(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("finish the thing"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckBatonPath(t *testing.T) {
	good := writeNotes(t)
	if err := checkBatonPath(good); err != nil {
		t.Errorf("an ordinary notes file was refused: %v", err)
	}
	dir := t.TempDir()
	for name, path := range map[string]string{
		"a relative path":  "notes.md",
		"a UNC path":       `\\server\share\notes.md`,
		"a device path":    `\\.\pipe\notes.md`,
		"a slashed UNC":    "//server/share/notes.md",
		"the wrong type":   filepath.Join(dir, "x.sh"),
		"a missing file":   filepath.Join(dir, "gone.md"),
		"a folder":         dir,
		"a folder by name": filepath.Join(dir, "sub.md"),
	} {
		if name == "a folder by name" {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := checkBatonPath(path); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(good, link); err != nil {
		t.Logf("no symbolic links here, so that case is not run: %v", err)
		return
	}
	if err := checkBatonPath(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a symbolic link was accepted: %v", err)
	}
	if _, err := readBatonFile(link, nil); err == nil {
		t.Error("readBatonFile followed a link")
	}
}

// Windows takes a mix of slashes and backslashes for a UNC path, and a device
// or NT object path begins differently again. None of them is a local drive.
func TestNetworkAndDevicePathsInEveryForm(t *testing.T) {
	for _, p := range []string{
		`\\server\share\x.md`, `//server/share/x.md`, `/\localhost\c$\Users\Public\x.md`,
		`\/localhost/c$/Users/Public/x.md`, `/\/localhost\c$\x.md`, `\??\UNC\localhost\c$\Users\x.md`,
		`\\?\C:\x.md`, `\\.\pipe\x.md`, `\\?\UNC\server\share\x.md`, `//?/C:/x.md`, `//./pipe/x.md`,
	} {
		if !networkPath(p) {
			t.Errorf("networkPath(%q) = false", p)
		}
		if err := checkBatonPathName(p); err == nil {
			t.Errorf("checkBatonPathName(%q) accepted it", p)
		}
		if _, err := readBatonFile(p, nil); err == nil {
			t.Errorf("readBatonFile(%q) read it", p)
		}
	}
	for _, p := range []string{`C:\Users\me\notes.md`, `C:/Users/me/notes.md`, `/home/me/notes.md`, `/tmp/x.md`} {
		if networkPath(p) {
			t.Errorf("networkPath(%q) = true", p)
		}
	}
	// A local path passes the name check on the system it is a path on.
	local := `/home/me/notes.md`
	if runtime.GOOS == "windows" {
		local = `C:\Users\me\notes.md`
	}
	if err := checkBatonPathName(local); err != nil {
		t.Errorf("checkBatonPathName(%q) = %v", local, err)
	}
}
