package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/structured"
)

// These tests start structured panes against the fake agent, which is this test
// binary (see TestMain). No real agent is run.

// onPath puts a program called name on PATH for this test, so that the probe for
// an agent whose adapter is that program finds it. It is never run: the fake
// agent is started in its place.
func onPath(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	file, body := name, "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		file, body = name+".cmd", "@exit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	agent.Refresh()
}

// structuredCrew is a terminal manager in a project whose catalog offers
// structured agents, with the fake agent behind them.
func structuredCrew(t *testing.T) (ws *Workspace, lead, root string) {
	t.Helper()
	isolateConfig(t)
	teamAgents(t)
	if err := store.SavePrefs(store.Prefs{StructuredPanes: true}); err != nil {
		t.Fatal(err)
	}
	onPath(t, "opencode")
	root = t.TempDir()
	ws = newTestWorkspace(t, root)
	useFakeAgent(ws)
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "sleeper"}, root, "")
	lead = ws.FocusedPane().ID
	ws.Pane(lead).Role = "manager"
	return ws, lead, root
}

func useFakeAgent(ws *Workspace) {
	exe, _ := os.Executable()
	ws.UseStructuredCommand(func(agent.Spec) *structured.Command {
		return &structured.Command{Path: exe, Args: []string{acptest.ArgAgent}}
	})
}

func spawnStructured(t *testing.T, ws *Workspace, lead, task string) string {
	t.Helper()
	id, err := ws.Spawn(lead, SpawnOptions{Task: task, Role: "worker", Agent: "opencode-acp", Kind: session.KindClaude, SpawnedByAgent: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	return id
}

// statusWithin polls the pane's status until it is want, or fails.
func statusWithin(t *testing.T, ws *Workspace, id string, want session.Status) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if p := ws.Pane(id); p != nil {
			if st, _ := p.Status(); st == want {
				return
			}
		}
		if time.Now().After(deadline) {
			st := session.StatusExited
			if p := ws.Pane(id); p != nil {
				st, _ = p.Status()
			}
			t.Fatalf("pane status is %v, want %v", st, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// A structured helper is an ordinary spawned pane with a parent, a role and a
// name, whose state is told by its runtime and not read from a session, and it
// counts against the helper cap with the rule unchanged.
func TestAStructuredHelperIsAPaneWithNoSession(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	p := ws.Pane(id)
	if !p.Structured() || p.Sess != nil || p.Parent != lead || p.Role != "worker" || p.Agent != "opencode-acp" {
		t.Fatalf("structured %v, session %v, parent %q, role %q, agent %q", p.Structured(), p.Sess, p.Parent, p.Role, p.Agent)
	}
	rt := ws.Runtime(p)
	if rt.Transport() != TransportStructured || !rt.Caps().Structured || !rt.Caps().AnswerPermission || !rt.Caps().RunsUnasked {
		t.Errorf("transport %q, caps %+v", rt.Transport(), rt.Caps())
	}
	statusWithin(t, ws, id, session.StatusIdle)
	if rt.Activity() != ActivityIdle || !p.Alive() {
		t.Errorf("activity %q, alive %v", rt.Activity(), p.Alive())
	}
	if r, ok := rt.LastReply(); !ok || !strings.Contains(r.Text, "Hello world") {
		t.Errorf("last reply = %+v, %v", r, ok)
	}
	if !rt.HasRun() || rt.StartedAt().IsZero() || rt.StatusSince().IsZero() {
		t.Error("a pane that has run has no start or status time")
	}
	ws.mu.RLock()
	below := ws.helpersBelow(lead)
	ws.mu.RUnlock()
	if below != 1 {
		t.Errorf("helpers below the manager = %d, want 1", below)
	}
	if tab := ws.tabOf(id); tab == nil {
		t.Error("the structured pane is in no tab")
	}
}

// The list from mcpServersFor is what a started structured pane's session/new
// carries: the fake agent writes the mcpServers it received to a file.
func TestAStartedStructuredPaneSendsItsMCPServers(t *testing.T) {
	ws, lead, root := structuredCrew(t)
	ws.UseMCPToken(func(id string) string { return "tok-" + id })
	ws.UseMCPSources(func() (string, error) { return "/opt/fd/flockdeck", nil },
		func() []store.MCPServerPref { return []store.MCPServerPref{{Name: "files", Command: "mcp-files"}} })
	rec := filepath.Join(root, acptest.McpRecordFile)
	if err := os.WriteFile(rec, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		Env     []struct{ Name, Value string }
	}
	if err := json.Unmarshal(b, &got); err != nil || len(got) != 2 {
		t.Fatalf("recorded %q: %v", b, err)
	}
	if got[0].Name != "flockdeck" || len(got[0].Env) != 2 || got[0].Env[0].Value != "tok-"+id || got[0].Env[1].Name != MCPAPIEnv || got[0].Env[1].Value != ws.HookServer().BaseURL() || got[1].Name != "files" {
		t.Errorf("servers = %+v", got)
	}
}

// The ninth helper is refused whatever mix of kinds the first eight were.
func TestStructuredHelpersCountAgainstTheCap(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	for i := 0; i < MaxHelpers; i++ {
		task := "plain"
		if i%2 == 0 {
			if _, err := ws.Spawn(lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true}); err != nil {
				t.Fatalf("terminal helper %d: %v", i, err)
			}
			continue
		}
		spawnStructured(t, ws, lead, task)
	}
	_, err := ws.Spawn(lead, SpawnOptions{Task: "plain", Role: "worker", Agent: "opencode-acp", Kind: session.KindClaude, SpawnedByAgent: true})
	if err == nil {
		t.Fatal("a ninth helper was started")
	}
	if got := ws.HelperRoom(lead); got == nil {
		t.Error("HelperRoom says there is room")
	}
}

// With the preference off a structured agent is refused as a spawn, and a pane
// restored with it reports why in place.
func TestStructuredPanesAreOptIn(t *testing.T) {
	isolateConfig(t)
	teamAgents(t)
	onPath(t, "opencode")
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	useFakeAgent(ws)
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "sleeper"}, root, "")
	lead := ws.FocusedPane().ID
	_, err := ws.Spawn(lead, SpawnOptions{Task: "plain", Role: "worker", Agent: "opencode-acp", Kind: session.KindClaude, SpawnedByAgent: true})
	if err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Errorf("spawn with the preference off: %v", err)
	}
	// A pane that already names the agent does not start.
	tab := ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "opencode-acp"}, root, "")
	p := ws.Pane(tab.Focus)
	if p.Err == nil || p.Structured() || !strings.Contains(p.Err.Error(), "turned off") {
		t.Errorf("pane err %v, structured %v", p.Err, p.Structured())
	}
}

// Closing a pane answers its open permission request cancelled and ends the
// agent; so does quitting.
func TestClosingAStructuredPaneEndsItsOpenRequest(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	closed := spawnStructured(t, ws, lead, "hold-perm")
	quit := spawnStructured(t, ws, lead, "hold-perm")
	statusWithin(t, ws, closed, session.StatusWaiting)
	statusWithin(t, ws, quit, session.StatusWaiting)
	a, b := ws.StructuredRuntime(closed), ws.StructuredRuntime(quit)
	if a.Snapshot().Pending == nil {
		t.Fatal("no request is open")
	}

	if !ws.ClosePaneByID(closed) {
		t.Fatal("the pane was not closed")
	}
	if sn := a.Snapshot(); sn.Phase != structured.PhaseClosed || sn.Pending != nil {
		t.Errorf("after close: phase %q, pending %+v", sn.Phase, sn.Pending)
	}
	if ws.Pane(closed) != nil {
		t.Error("the pane is still there")
	}

	ws.Close()
	if sn := b.Snapshot(); sn.Phase != structured.PhaseClosed || sn.Pending != nil {
		t.Errorf("after quit: phase %q, pending %+v", sn.Phase, sn.Pending)
	}
}

// A restart starts a fresh session and keeps what the earlier one showed.
func TestRestartingAStructuredPaneKeepsItsHistory(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	first := ws.StructuredRuntime(id)
	before, _ := ws.StructuredEntries(id)
	if len(before) == 0 {
		t.Fatal("the first session showed nothing")
	}

	if !ws.RestartPaneByID(id) {
		t.Fatal("restart refused")
	}
	second := ws.StructuredRuntime(id)
	if second == nil || second == first {
		t.Fatal("the restart did not start a fresh session")
	}
	if sn := first.Snapshot(); sn.Phase != structured.PhaseClosed {
		t.Errorf("the old session is %q", sn.Phase)
	}
	statusWithin(t, ws, id, session.StatusIdle)
	entries, _ := ws.StructuredEntries(id)
	if len(entries) < len(before)+1 {
		t.Fatalf("%d entries after the restart, %d before", len(entries), len(before))
	}
	// The session's entries are renamed as they become history, so the next
	// session's e1... do not repeat them; what they said does not change.
	seen := map[string]bool{}
	for i := range before {
		if entries[i].Kind != before[i].Kind || entries[i].Text != before[i].Text || entries[i].Markdown != before[i].Markdown {
			t.Errorf("history entry %d changed: %+v then %+v", i, before[i], entries[i])
		}
		if want := "r0-" + before[i].ID; entries[i].ID != want {
			t.Errorf("history entry %d is %q, want %q", i, entries[i].ID, want)
		}
	}
	for _, en := range entries {
		if seen[en.ID] {
			t.Errorf("entry id %q is shown twice", en.ID)
		}
		seen[en.ID] = true
	}
	if note := entries[len(before)]; !strings.Contains(note.Text, "restarted") {
		t.Errorf("no line marks the restart: %+v", note)
	}
	if ws.Pane(id).Sess != nil {
		t.Error("a restarted structured pane gained a session")
	}
}

// restartAndWait restarts the structured pane and waits for the new session.
func restartAndWait(t *testing.T, ws *Workspace, id string) {
	t.Helper()
	old := ws.StructuredRuntime(id)
	if !ws.RestartPaneByID(id) {
		t.Fatal("restart refused")
	}
	if now := ws.StructuredRuntime(id); now == nil || now == old {
		t.Fatal("the restart did not start a fresh session")
	}
	statusWithin(t, ws, id, session.StatusIdle)
}

func noEntryIDTwice(t *testing.T, entries []transcript.Entry) {
	t.Helper()
	seen := map[string]bool{}
	for _, en := range entries {
		if seen[en.ID] {
			t.Errorf("entry id %q is shown twice in %d entries", en.ID, len(entries))
		}
		seen[en.ID] = true
	}
}

// Two restarts leave three sessions, in order, and the first one's entries are
// still there with the ids they were given at the first restart.
func TestRestartingTwiceKeepsEverySessionWithUniqueIds(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	first, _ := ws.StructuredEntries(id)
	restartAndWait(t, ws, id)
	// The restarted session has said nothing yet; give it something to become history.
	if _, err := ws.SendToPane(id, "plain", false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		es, _ := ws.StructuredEntries(id)
		if n := len(es); n >= len(first)+3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second session never answered")
		}
		time.Sleep(25 * time.Millisecond)
	}
	statusWithin(t, ws, id, session.StatusIdle)
	afterOne, _ := ws.StructuredEntries(id)
	restartAndWait(t, ws, id)
	all, _ := ws.StructuredEntries(id)

	notes := 0
	for _, en := range all {
		if en.Kind == transcript.KindNotice && strings.Contains(en.Text, "restarted") {
			notes++
		}
	}
	if notes != 2 {
		t.Errorf("%d restart notices after two restarts, want 2", notes)
	}
	if len(all) <= len(afterOne) {
		t.Errorf("%d entries after the second restart, %d after the first", len(all), len(afterOne))
	}
	for i := range first {
		if all[i].Kind != first[i].Kind || all[i].ID != "r0-"+first[i].ID {
			t.Errorf("the first session's entry %d is %+v, want %s %q", i, all[i], first[i].Kind, "r0-"+first[i].ID)
		}
	}
	noEntryIDTwice(t, all)
	// The second session became history at the second restart, as r1-.
	found := false
	for _, en := range all {
		found = found || strings.HasPrefix(en.ID, "r1-")
	}
	if !found {
		t.Error("no entry of the second session was renamed r1-")
	}
}

// Once the history is cut to its cap the notices still have names of their own.
func TestRestartNoticesKeepUniqueIdsOncetheHistoryIsCut(t *testing.T) {
	defer func(n int) { maxPaneHistory = n }(maxPaneHistory)
	maxPaneHistory = 6
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	for i := 0; i < 9; i++ {
		restartAndWait(t, ws, id)
		entries, _ := ws.StructuredEntries(id)
		noEntryIDTwice(t, entries)
	}
	entries, _ := ws.StructuredEntries(id)
	var notices []string
	for _, en := range entries {
		if en.Kind == transcript.KindNotice {
			notices = append(notices, en.ID)
		}
	}
	if len(notices) < 2 {
		t.Fatalf("only %v are left of the notices", notices)
	}
}

// The pane's transport is saved with the layout, and a restored pane starts a
// fresh session.
func TestAStructuredPaneIsRestoredWithAFreshSession(t *testing.T) {
	ws, lead, root := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	if err := ws.SaveAll(); err != nil {
		t.Fatal(err)
	}
	old := ws.StructuredRuntime(id)
	ws.Close()
	if old.Snapshot().Phase != structured.PhaseClosed {
		t.Error("quitting left the agent running")
	}

	restored := newTestWorkspace(t, root)
	useFakeAgent(restored)
	if ok, err := restored.Restore(); err != nil || !ok {
		t.Fatalf("restore: %v, %v", ok, err)
	}
	p := restored.Pane(id)
	if p == nil || !p.Structured() || p.Sess != nil {
		t.Fatalf("the restored pane: %+v", p)
	}
	if rt := restored.StructuredRuntime(id); rt == nil || rt == old {
		t.Error("the restored pane has no fresh session")
	}
	// The terminal manager comes back as a terminal.
	if lp := restored.Pane(lead); lp == nil || lp.Structured() {
		t.Errorf("the manager came back as %+v", lp)
	}
}

// A pane saved as structured whose agent now runs in a terminal does not start
// as the wrong thing.
func TestASavedStructuredPaneWhoseAgentChangedReportsIt(t *testing.T) {
	ws, lead, root := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	ws.Pane(id).Agent = "sleeper"
	if err := ws.SaveAll(); err != nil {
		t.Fatal(err)
	}
	ws.Close()
	restored := newTestWorkspace(t, root)
	useFakeAgent(restored)
	if _, err := restored.Restore(); err != nil {
		t.Fatal(err)
	}
	p := restored.Pane(id)
	if p == nil || p.Err == nil || !strings.Contains(p.Err.Error(), "now runs in a terminal") {
		t.Errorf("pane = %+v", p)
	}
}

// Nothing a window can send types into a structured pane: a typed broadcast
// does not reach it, a baton restart is refused, and a message from the graph
// goes in as a prompt.
func TestNothingTypesIntoAStructuredPane(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	for _, p := range ws.BroadcastTargetsFor(id) {
		if p.ID == id {
			t.Error("a typed broadcast reaches the structured pane")
		}
	}
	ws.SendPromptTo(id, "typed text", true)
	if err := ws.RestartWithBaton(id, BatonPrompt{}, ""); err == nil {
		t.Error("a baton restart of a structured pane was allowed")
	}

	res, err := ws.SendToPane(id, "plain", false)
	if err != nil {
		t.Fatalf("SendToPane: %v", err)
	}
	if res.Shell {
		t.Error("a structured pane is not a shell")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		entries, _ := ws.StructuredEntries(id)
		prompts := 0
		for _, e := range entries {
			if e.Kind == "prompt" {
				prompts++
			}
		}
		if prompts >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sent prompt never reached the agent: %d prompts", prompts)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// The parent of a structured helper hears that it finished a turn, and that it
// ended on its own, as it does for a terminal helper.
func TestTheParentOfAStructuredHelperIsToldWhatItDid(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	var timers []func()
	ws.team.after = func(d time.Duration, fn func()) { timers = append(timers, fn) }
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	ws.noteStructured(id)
	// Whatever else arms a report, running it later finds the helper idle for at
	// least the dwell once the dwell has passed.
	ws.team.dwell = 0
	for i := 0; i < 5 && len(timers) > 0; i++ {
		ts := timers
		timers = nil
		for _, fn := range ts {
			fn()
		}
		ws.noteStructured(id)
	}
	msgs, err := ws.Inbox(lead, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Text, "finished its turn") {
			found = true
		}
	}
	if !found {
		t.Errorf("the manager was not told: %+v", msgs)
	}
}

// A restart whose new agent cannot start keeps what the earlier session showed,
// with the line that marks the restart and one that says why it failed, and a
// later restart that works shows all of it. The ways a start fails before the
// agent is run are the ones that used to lose the history: the directory the
// pane works in is gone, and structured panes turned off since.
func TestARestartThatCannotStartKeepsTheHistory(t *testing.T) {
	for _, c := range []struct {
		name    string
		break_  func(t *testing.T, ws *Workspace, p *Pane, root string)
		restore func(t *testing.T, ws *Workspace, p *Pane, cwd string)
	}{
		{"the directory is gone",
			func(t *testing.T, ws *Workspace, p *Pane, root string) { p.Cwd = filepath.Join(root, "gone") },
			func(t *testing.T, ws *Workspace, p *Pane, cwd string) { p.Cwd = cwd }},
		{"structured panes are turned off",
			func(t *testing.T, ws *Workspace, p *Pane, root string) {
				if err := store.SavePrefs(store.Prefs{}); err != nil {
					t.Fatal(err)
				}
				ws.ReloadAgents()
			},
			func(t *testing.T, ws *Workspace, p *Pane, cwd string) {
				if err := store.SavePrefs(store.Prefs{StructuredPanes: true}); err != nil {
					t.Fatal(err)
				}
				ws.ReloadAgents()
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ws, lead, root := structuredCrew(t)
			id := spawnStructured(t, ws, lead, "plain")
			statusWithin(t, ws, id, session.StatusIdle)
			before, _ := ws.StructuredEntries(id)
			if len(before) == 0 {
				t.Fatal("the first session showed nothing")
			}
			p := ws.Pane(id)
			cwd := p.Cwd

			c.break_(t, ws, p, root)
			if !ws.RestartPaneByID(id) {
				t.Fatal("restart refused")
			}
			if p.Err == nil {
				t.Fatal("the new agent started, so this test proves nothing")
			}
			if len(p.history) < len(before)+2 {
				t.Fatalf("%d history entries after the failed restart, want the %d before, the restart line and the failure", len(p.history), len(before))
			}
			for i := range before {
				if p.history[i].Text != before[i].Text || p.history[i].Kind != before[i].Kind {
					t.Errorf("history entry %d changed: %+v then %+v", i, before[i], p.history[i])
				}
			}
			if note := p.history[len(before)]; !strings.Contains(note.Text, "restarted") {
				t.Errorf("no line marks the restart: %+v", note)
			}
			if note := p.history[len(before)+1]; !strings.Contains(note.Text, "could not be started") {
				t.Errorf("no line says the start failed: %+v", note)
			}

			// Put right, and restarted again: the new session shows all of it.
			c.restore(t, ws, p, cwd)
			if !ws.RestartPaneByID(id) {
				t.Fatal("second restart refused")
			}
			statusWithin(t, ws, id, session.StatusIdle)
			if len(p.history) != 0 {
				t.Errorf("%d history entries are still held on the pane after the session started with them", len(p.history))
			}
			entries, _ := ws.StructuredEntries(id)
			if len(entries) < len(before)+2 {
				t.Fatalf("%d entries after the second restart, want at least %d", len(entries), len(before)+2)
			}
			for i := range before {
				if entries[i].Text != before[i].Text {
					t.Errorf("entry %d lost: %q then %q", i, before[i].Text, entries[i].Text)
				}
			}
			if !strings.Contains(entries[len(before)].Text, "restarted") || !strings.Contains(entries[len(before)+1].Text, "could not be started") {
				t.Errorf("the lines after the history are %q and %q", entries[len(before)].Text, entries[len(before)+1].Text)
			}
		})
	}
}

// fakeHistory is n entries standing for an earlier session.
func fakeHistory(n int) []transcript.Entry {
	var out []transcript.Entry
	for i := 0; i < n; i++ {
		out = append(out, transcript.Entry{ID: fmt.Sprintf("old-%d", i), Kind: transcript.KindNotice, Text: fmt.Sprintf("earlier %d", i)})
	}
	return out
}

func idsOf(entries []transcript.Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// The error structured.Start returns, which is only for an agent that does not
// speak the protocol and has no command given for it, also keeps the history.
func TestAStartErrorKeepsTheHistory(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	p := ws.Pane(id)
	p.history = fakeHistory(3)
	ws.UseStructuredCommand(nil)

	ws.startStructured(p, agent.Spec{Name: "not-acp", Runner: agent.RunnerCLI})
	if p.Err == nil {
		t.Fatal("the start did not fail, so this test proves nothing")
	}
	if len(p.history) != 4 || p.history[0].Text != "earlier 0" || !strings.Contains(p.history[3].Text, "could not be started") {
		t.Errorf("history after the failed start: %v", idsOf(p.history))
	}
}

// Two failures in a row leave one failure line, the later one, after the history.
func TestFailuresInARowLeaveOneLine(t *testing.T) {
	ws, lead, root := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	p := ws.Pane(id)
	p.history = fakeHistory(3)
	p.Cwd = filepath.Join(root, "gone")

	for i := 0; i < 4; i++ {
		ws.startStructured(p, agent.Spec{Name: "x", Runner: agent.RunnerACP})
	}
	if len(p.history) != 4 {
		t.Fatalf("%d entries after four failures in a row, want the 3 and one line: %v", len(p.history), idsOf(p.history))
	}
	if got := p.history[3].ID; got != "restart-failed-4" {
		t.Errorf("the line is %q, want the fourth failure's", got)
	}
}

// At the cap the history is cut from the front, and the failure lines still get
// ids of their own.
func TestFailureLinesAtTheCapKeepUniqueIDs(t *testing.T) {
	old := maxPaneHistory
	maxPaneHistory = 6
	t.Cleanup(func() { maxPaneHistory = old })

	ws, lead, root := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	p := ws.Pane(id)
	p.history = fakeHistory(6)
	p.Cwd = filepath.Join(root, "gone")

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		// A session that ran in between, so the failures are not in a row.
		p.history = append(p.history, transcript.Entry{ID: fmt.Sprintf("ran-%d", i), Kind: transcript.KindNotice, Text: "ran"})
		ws.startStructured(p, agent.Spec{Name: "x", Runner: agent.RunnerACP})
		if len(p.history) > maxPaneHistory {
			t.Fatalf("%d entries, the cap is %d", len(p.history), maxPaneHistory)
		}
		last := p.history[len(p.history)-1]
		if seen[last.ID] {
			t.Errorf("failure line id %q repeats", last.ID)
		}
		seen[last.ID] = true
	}
	if len(p.history) != 6 {
		t.Errorf("%d entries at the cap, want 6", len(p.history))
	}
	if p.history[0].Text == "earlier 0" {
		t.Error("the oldest entry was not cut")
	}
	ids := map[string]bool{}
	for _, e := range p.history {
		if ids[e.ID] {
			t.Errorf("entry id %q is shown twice: %v", e.ID, idsOf(p.history))
		}
		ids[e.ID] = true
	}
}
