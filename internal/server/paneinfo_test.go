package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

func fieldValue(fs []paneInfoField, label string) (string, bool) {
	for _, f := range fs {
		if f.Label == label {
			return f.Value, true
		}
	}
	return "", false
}

func TestPaneInfoFieldsForAnAgentAndAShell(t *testing.T) {
	agent := workspace.PaneDetails{
		ID: "p1", Agent: true, Name: "shop", Cwd: "/work/shop", Root: "/work/shop", Branch: "main",
		Tab: "Fix checkout", AgentID: "claude", Model: "sonnet", Conversation: "0a1b2c3d-1111-2222-3333-444455556666",
		Pid: 4242, PeerName: "shop-agent", Status: "idle", Recording: true,
		TranscriptPath: "/rec/2026-10-01-0a1b2c3d.jsonl", StoredPath: "/home/.claude/projects/x/0a1b2c3d.jsonl",
	}
	want := map[string]string{
		"Pane id": "p1", "Pane name": "shop", "Type": "Agent", "Project": "Shop", "Project folder": "/work/shop",
		"Directory": "/work/shop", "Branch": "main", "Tab": "Fix checkout", "Agent": "claude", "Model": "sonnet",
		"Conversation id": agent.Conversation, "Process id": "4242", "Peer name": "shop-agent", "Status": "idle",
		"Recording": "On", "Transcript file": agent.TranscriptPath, "Stored conversation file": agent.StoredPath,
	}
	got := paneInfoFields(agent, "Shop")
	for label, v := range want {
		if g, ok := fieldValue(got, label); !ok || g != v {
			t.Errorf("%s = %q (present %v), want %q", label, g, ok, v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d fields, want %d", len(got), len(want))
	}
	if note := func() string {
		for _, f := range got {
			if f.Label == "Conversation id" {
				return f.Note
			}
		}
		return ""
	}(); !strings.Contains(note, "/clear") {
		t.Errorf("the conversation id does not say it changes after /clear: %q", note)
	}

	// A shell has no agent, conversation or transcript to show, and what it has
	// not got yet is left empty, not made up.
	shell := paneInfoFields(workspace.PaneDetails{ID: "p2", Name: "sh", Root: "/work/shop"}, "Shop")
	for _, label := range []string{"Agent", "Model", "Conversation id", "Recording", "Transcript file", "Stored conversation file"} {
		if _, ok := fieldValue(shell, label); ok {
			t.Errorf("a shell shows %s", label)
		}
	}
	if v, ok := fieldValue(shell, "Process id"); !ok || v != "" {
		t.Errorf("a shell with no process: %q, %v", v, ok)
	}
}

func TestSearchPanesMatchesEveryWordInAnyFieldIgnoringCase(t *testing.T) {
	all := []workspace.PaneDetails{
		{ID: "p1", Agent: true, Name: "shop", Root: "/work/shop", Tab: "Checkout", AgentID: "claude", Model: "Sonnet",
			Cwd: "/work/shop-wt/fix", Branch: "fix/cart", Conversation: "0a1b2c3d-1111", Pid: 4242, PeerName: "Cart-Agent"},
		{ID: "p2", Name: "docs", Root: "/work/docs", Tab: "Writing"},
	}
	names := map[string]string{"/work/shop": "Shop", "/work/docs": "Docs"}
	find := func(q string) []string {
		var ids []string
		for _, m := range searchPanes(all, names, q) {
			ids = append(ids, m.PaneID)
		}
		return ids
	}
	cases := []struct {
		q    string
		want string
		by   string
	}{
		{"SHOP", "p1", "Name"}, {"checkout", "p1", "Tab"}, {"sonnet", "p1", "Model"}, {"fix/cart", "p1", "Branch"},
		{"0A1B2C3D", "p1", "Conversation id"}, {"0a1b", "p1", "Conversation id"}, {"424", "p1", "Process id"},
		{"cart-agent", "p1", "Peer name"}, {"shop-wt", "p1", "Directory"}, {"p2", "p2", "Pane id"},
		{"docs", "p2", "Project"}, {"claude", "p1", "Agent"},
	}
	for _, c := range cases {
		got := searchPanes(all, names, c.q)
		found := false
		for _, m := range got {
			if m.PaneID == c.want {
				found = hasLabel(m.Matched, c.by)
			}
		}
		if !found {
			t.Errorf("%q did not find %s by %s: %+v", c.q, c.want, c.by, got)
		}
	}
	if got := find("shop sonnet"); len(got) != 1 || got[0] != "p1" {
		t.Errorf("two words: %v", got)
	}
	if got := find("shop writing"); len(got) != 0 {
		t.Errorf("words from different panes matched one: %v", got)
	}
	if got := find("zzz"); len(got) != 0 {
		t.Errorf("nonsense matched %v", got)
	}
	if got := find(""); len(got) != 2 {
		t.Errorf("an empty query lists every pane: %v", got)
	}
	if m := searchPanes(all, names, "shop")[0]; m.Project != "Shop" || m.Tab != "Checkout" || m.Root != "/work/shop" {
		t.Errorf("result lacks where the pane is: %+v", m)
	}
}

func TestPaneInfoAndFindPaneAnswerOnlyTheLocalWindow(t *testing.T) {
	srv, ws := newTestServer(t)
	pane := firstPane(t, srv, ws)

	remote := &controlClient{out: make(chan []byte, 8), remote: true}
	srv.paneInfo(remote, command{Cmd: "paneInfo", ID: pane})
	srv.findPane(remote, command{Cmd: "findPane", Text: ""})
	for i := 0; i < 2; i++ {
		select {
		case data := <-remote.out:
			var n noticeMsg
			if err := json.Unmarshal(data, &n); err != nil || !n.Error || !strings.Contains(n.Text, "relay") {
				t.Errorf("remote window got %s", data)
			}
		default:
			t.Fatalf("a remote window was not answered (%d)", i)
		}
	}

	local := &controlClient{out: make(chan []byte, 8)}
	srv.paneInfo(local, command{Cmd: "paneInfo", ID: pane})
	var info paneInfoMsg
	if err := json.Unmarshal(<-local.out, &info); err != nil || info.Type != "paneInfo" || info.ID != pane {
		t.Fatalf("pane info: %+v, %v", info, err)
	}
	if v, _ := fieldValue(info.Fields, "Pane id"); v != pane {
		t.Errorf("pane id = %q, want %q", v, pane)
	}

	srv.findPane(local, command{Cmd: "findPane", Text: pane})
	var found paneMatchesMsg
	if err := json.Unmarshal(<-local.out, &found); err != nil || found.Type != "paneMatches" || len(found.Items) != 1 || found.Items[0].PaneID != pane {
		t.Fatalf("find: %+v, %v", found, err)
	}
	if found.Items[0].TabID == "" {
		t.Errorf("a result does not say which tab the pane is in")
	}

	srv.paneInfo(local, command{Cmd: "paneInfo", ID: "nope"})
	var n noticeMsg
	if err := json.Unmarshal(<-local.out, &n); err != nil || !n.Error || n.Text != paneGone {
		t.Errorf("a pane that is gone: %+v, %v", n, err)
	}
}

// The conversation id of a fresh agent pane is the pane's own id, and the stored
// conversation's path is the agent's file for it, found only once it exists.
func TestPaneInfoNamesTheStoredConversationOfAnAgentPane(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	p, _ := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "agent").Tree.Panes()[0])
		p.Kind, p.Agent = session.KindAgent, "claude"
		return p
	})
	details := func() workspace.PaneDetails {
		d, _ := ask(srv, func() workspace.PaneDetails { d, _ := ws.PaneDetailsOf(p.ID, true); return d })
		return d
	}
	d := details()
	if d.Conversation != p.ID || d.StoredPath != "" || d.TranscriptPath != "" {
		t.Fatalf("before anything is stored: %+v", d)
	}
	dir := filepath.Join(home, "projects", "C--work-shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, p.ID+".jsonl")
	if err := os.WriteFile(file, []byte(`{"type":"user","cwd":"/work/shop","message":{"role":"user","content":"hi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d = details(); d.StoredPath != file {
		t.Errorf("stored path = %q, want %q", d.StoredPath, file)
	}
}

// A Flockdeck API agent keeps its conversation in a file of its own, which Pane
// info shows once it exists and not before. It has no exporter, so it still has
// no transcript file; a shell has neither.
func TestPaneInfoShowsAnAPIAgentsStoredConversationOnlyOnceItExists(t *testing.T) {
	srv, ws := newTestServer(t)
	p, _ := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "api").Tree.Panes()[0])
		p.Kind, p.Agent = session.KindAgent, "anthropic"
		return p
	})
	details := func(id string) workspace.PaneDetails {
		d, _ := ask(srv, func() workspace.PaneDetails { d, _ := ws.PaneDetailsOf(id, true); return d })
		return d
	}
	d := details(p.ID)
	if d.Conversation == "" || d.StoredPath != "" || d.TranscriptPath != "" {
		t.Fatalf("before the file exists: %+v", d)
	}
	for _, f := range paneInfoFields(d, "Shop") {
		if (f.Label == "Stored conversation file" || f.Label == "Transcript file") && f.Value != "" {
			t.Errorf("%s shows %q before there is a file", f.Label, f.Value)
		}
	}

	state, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "chats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, d.Conversation+".jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d = details(p.ID)
	if d.StoredPath != file {
		t.Errorf("stored path = %q, want %q", d.StoredPath, file)
	}
	if d.TranscriptPath != "" {
		t.Errorf("an API agent has a transcript path %q", d.TranscriptPath)
	}

	// A shell has no agent, so nothing is looked up for it.
	shell := details(addPane(t, srv, ws, "shell"))
	if shell.Agent || shell.StoredPath != "" || shell.Conversation != "" {
		t.Errorf("a shell: %+v", shell)
	}
}
