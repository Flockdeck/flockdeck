package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/baton"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// What decides whether a baton may go to another company is what the application
// recorded about where it came from. A baton's own header is text, and a header
// that says "agent: gitcli" is a claim by whoever wrote the file.
func TestAForgedHeaderDoesNotMakeABatonOKToSend(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	spawn := func(ref string, elsewhere bool) error {
		_, err := hooks.Spawn(hook.BaseURL(), hook.Token(), pane,
			hooks.SpawnRequest{Task: "x", Agent: "gitcli", Baton: ref, BatonElsewhere: elsewhere})
		return err
	}

	// A notes file whose front matter names the very agent it is going to.
	forged := filepath.Join(t.TempDir(), "forged.md")
	text := "---\nid: 20261001-090000-0a1b2c\nagent: gitcli\npane: p\n---\n\n# Baton: forged\n\n## Goal\n\nfinish\n"
	if err := os.WriteFile(forged, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := spawn("path:"+forged, false); err == nil || !strings.Contains(err.Error(), "not known") || !strings.Contains(err.Error(), "-baton-send-elsewhere") {
		t.Errorf("a file naming its own destination went through: %v", err)
	}

	// A stored baton the application has no record of, with the same claim.
	loose := baton.Baton{ID: baton.NewID(time.Now()), Title: "t", FromAgent: "gitcli", Sections: map[baton.Section]string{baton.Goal: "do it"}}
	if err := workspace.SaveBaton(loose); err != nil {
		t.Fatal(err)
	}
	if err := spawn(loose.ID, false); err == nil || !strings.Contains(err.Error(), "-baton-send-elsewhere") {
		t.Errorf("a stored baton with a forged header went through: %v", err)
	}
	// Once the application records where it came from, that is what counts.
	st, err := baton.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSource(loose.ID, "gocli"); err != nil {
		t.Fatal(err)
	}
	if err := spawn(loose.ID, false); err == nil || !strings.Contains(err.Error(), "it would go from go to git") {
		t.Errorf("the recorded source was not used: %v", err)
	}
	// With the flag the refusal is not what stops it.
	if err := spawn(loose.ID, true); err != nil && strings.Contains(err.Error(), "different company") {
		t.Errorf("the flag did not lift the refusal: %v", err)
	}
}

// A baton saved from the dialog is recorded against the agent of the pane it
// was made from.
func TestABatonSavedFromAPaneRecordsWhereItCameFrom(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialControl(t, srv)
	pane := spawnGoPane(t, srv, ws)
	sendCmd(t, conn, command{Cmd: "makeBaton", ID: pane})
	var draft batonDraftMsg
	readUntil(t, conn, "batonDraft", &draft)
	if len(draft.MarkKinds) == 0 {
		t.Error("the draft does not say which marks the scrubber makes")
	}
	sendCmd(t, conn, command{Cmd: "saveBaton", ID: pane, Text: draft.Text})
	var saved batonSavedMsg
	readUntil(t, conn, "batonSaved", &saved)
	if saved.Cmd != "saveBaton" {
		t.Errorf("batonSaved.Cmd = %q", saved.Cmd)
	}
	st, _ := baton.Open()
	if got := st.Source(saved.ID); got != "gocli" {
		t.Errorf("recorded source = %q, want gocli", got)
	}
}

func TestAFailureToRecordASourceIsReported(t *testing.T) {
	newTestServer(t)
	st, err := baton.Open()
	if err != nil {
		t.Fatal(err)
	}
	// A folder where the sources file should be: the file cannot be replaced.
	if err := os.MkdirAll(filepath.Join(st.Dir(), "sources.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	var logged []string
	old := logf
	logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { logf = old })
	recordSource(baton.NewID(time.Now()), "claude")
	if len(logged) != 1 || !strings.Contains(logged[0], "could not record") {
		t.Errorf("logged = %q", logged)
	}
}

func TestALinkToANetworkPathIsNotFollowed(t *testing.T) {
	for _, p := range []string{`\\server\share\notes.md`, `\\?\UNC\server\share\notes.md`, `\\?\C:\notes.md`} {
		if err := resolvedLocal(p); err == nil {
			t.Errorf("resolvedLocal(%q) accepted it", p)
		}
	}
	for _, p := range []string{`C:\Users\me\notes.md`, `/home/me/notes.md`} {
		if err := resolvedLocal(p); err != nil {
			t.Errorf("resolvedLocal(%q) = %v", p, err)
		}
	}
}

// The window matches an answer to its command by the id it sent, which the server
// repeats, and a panic in the work still answers it.
func TestAPanicInBatonWorkStillAnswersTheCommand(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialControl(t, srv)
	pane := spawnGoPane(t, srv, ws)

	sendCmd(t, conn, command{Cmd: "makeBaton", ID: pane, Req: "r1"})
	var draft batonDraftMsg
	readUntil(t, conn, "batonDraft", &draft)
	if draft.Req != "r1" {
		t.Errorf("the draft did not repeat the request id: %q", draft.Req)
	}

	batonPanicHook = func() { panic("boom") }
	t.Cleanup(func() { batonPanicHook = nil })
	sendCmd(t, conn, command{Cmd: "saveBaton", ID: pane, Text: draft.Text, Req: "r2"})
	var e batonErrorMsg
	readUntil(t, conn, "batonError", &e)
	if e.Req != "r2" || e.Cmd != "saveBaton" || e.PaneID != pane {
		t.Errorf("batonError = %+v", e)
	}
	sendCmd(t, conn, command{Cmd: "makeBaton", ID: pane, Req: "r3"})
	var e2 batonErrorMsg
	readUntil(t, conn, "batonError", &e2)
	if e2.Req != "r3" || e2.Cmd != "makeBaton" {
		t.Errorf("batonError for makeBaton = %+v", e2)
	}
}

// A baton saved from the dialog whose source could not be recorded is said to the
// window, and the error is also written to error.log.
func TestASourceThatCouldNotBeRecordedIsSaidToTheWindow(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialControl(t, srv)
	pane := spawnGoPane(t, srv, ws)
	sendCmd(t, conn, command{Cmd: "makeBaton", ID: pane, Req: "r1"})
	var draft batonDraftMsg
	readUntil(t, conn, "batonDraft", &draft)
	st, _ := baton.Open()
	if err := os.MkdirAll(filepath.Join(st.Dir(), "sources.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	sendCmd(t, conn, command{Cmd: "saveBaton", ID: pane, Text: draft.Text, Req: "r2"})
	var n noticeMsg
	readUntil(t, conn, "notice", &n)
	if !n.Error || !strings.Contains(n.Text, "source could not be recorded") || !strings.Contains(n.Text, "-baton-send-elsewhere") {
		t.Errorf("notice = %+v", n)
	}
	var saved batonSavedMsg
	readUntil(t, conn, "batonSaved", &saved)
	if saved.Req != "r2" {
		t.Errorf("batonSaved.Req = %q", saved.Req)
	}
	dir, _ := store.Dir()
	data, err := os.ReadFile(filepath.Join(dir, "error.log"))
	if err != nil || !strings.Contains(string(data), "could not record") {
		t.Errorf("error.log = %q, %v", data, err)
	}
}
