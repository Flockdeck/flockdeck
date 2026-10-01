package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// revealed swaps the program that shows a file for one that records what it
// was asked to, so no test opens a file manager.
func revealed(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := revealFile
	revealFile = func(path string) error { got = append(got, path); return nil }
	t.Cleanup(func() { revealFile = old })
	return &got
}

func lastNotice(t *testing.T, c *controlClient) noticeMsg {
	t.Helper()
	var note noticeMsg
	select {
	case data := <-c.out:
		if err := json.Unmarshal(data, &note); err != nil {
			t.Fatal(err)
		}
	default:
	}
	return note
}

// The file is shown on the machine itself only.
func TestRevealTranscriptIsRefusedForARemoteWindow(t *testing.T) {
	srv, ws := newTestServer(t)
	got := revealed(t)
	pane := firstPane(t, srv, ws)
	c := &controlClient{out: make(chan []byte, 8), remote: true}
	srv.revealTranscript(c, command{Cmd: "revealTranscript", ID: pane})
	if note := lastNotice(t, c); !note.Error || !strings.Contains(note.Text, "relay") {
		t.Errorf("notice = %+v", note)
	}
	if len(*got) != 0 {
		t.Errorf("a remote window had %v shown", *got)
	}
}

// A client names a pane and nothing else: a path in the command is not read, and
// a pane with no transcript, or a shell, shows nothing.
func TestRevealTranscriptShowsOnlyAFileOfThePanesOwn(t *testing.T) {
	srv, ws := newTestServer(t)
	got := revealed(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	shell := firstPane(t, srv, ws)
	agentPane, _ := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "agent").Tree.Panes()[0])
		p.Kind, p.Agent = session.KindAgent, "claude"
		return p
	})
	local := func(cmd command) noticeMsg {
		c := &controlClient{out: make(chan []byte, 8)}
		srv.revealTranscript(c, cmd)
		return lastNotice(t, c)
	}

	// Nothing recorded or exported yet: said so, and nothing shown.
	if note := local(command{Cmd: "revealTranscript", ID: agentPane.ID}); !note.Error || !strings.Contains(note.Text, "no transcript file yet") {
		t.Errorf("notice = %+v", note)
	}
	// A path is not a field of the command, so one in the text is ignored.
	evil := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(evil, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	local(command{Cmd: "revealTranscript", ID: agentPane.ID, Text: evil, Kind: evil})
	if len(*got) != 0 {
		t.Fatalf("shown: %v", *got)
	}

	// Exported: the export is what is shown, as the server resolves it.
	dir := filepath.Join(home, "projects", "C--work-shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"/work/shop","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, agentPane.ID+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := ws.ExportTranscript(agentPane.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if note := local(command{Cmd: "revealTranscript", ID: agentPane.ID}); note.Error {
		t.Fatalf("notice = %+v", note)
	}
	want, _ := filepath.EvalSymlinks(res.Path)
	if len(*got) != 1 || (*got)[0] != want {
		t.Errorf("shown %v, want [%s]", *got, want)
	}

	// A shell has no transcript.
	*got = nil
	if note := local(command{Cmd: "revealTranscript", ID: shell}); !note.Error || len(*got) != 0 {
		t.Errorf("a shell: %+v, shown %v", note, *got)
	}
}
