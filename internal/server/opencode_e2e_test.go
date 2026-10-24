package server

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/testiso/iso"
)

// A real opencode, end to end, through the control commands a window uses and
// nothing else: the test is the window. It is skipped unless
// -opencode names the opencode program (an absolute path; the test isolation clears the environment, so it is a flag), because it
// needs the network: the prompts below go to opencode's hosted free model, with
// no login. Nothing else is sent. -opencode-report, when set, is a file
// the observations are written to as JSON.
//
// Isolation: the workspace and its config are the usual temporary ones. The
// agent is started with a home, profile, local app data and XDG data, cache and
// state directories that are new directories at the root of the drive (outside
// the user profile), and works in another such directory. What Flockdeck gives an
// agent is the minimal environment plus those names, so nothing else of the real
// profile is passed. The script that runs this lists the real Flockdeck and
// opencode directories before and after.
//
// The agent is started through acptest's recording proxy, so the raw traffic is
// kept in a log in a temporary directory, to answer what the client does not
// keep: whether a request carries rawInput.command, and which updates the agent
// sends at all.

var (
	realOpencode   = flag.String("opencode", "", "absolute path of the opencode program, to run TestRealOpencodeEndToEnd")
	opencodeReport = flag.String("opencode-report", "", "a file TestRealOpencodeEndToEnd writes its observations to")
)

type observed struct {
	Version   string         `json:"opencode"`
	Steps     map[string]any `json:"steps"`
	RawLog    string         `json:"rawLog,omitempty"`
	Notes     []string       `json:"notes,omitempty"`
	PlanSeen  bool           `json:"planUpdate"`
	UsageSeen bool           `json:"usageUpdate"`
	Elicit    bool           `json:"elicitation"`
	AskSeen   bool           `json:"askTool"`
}

// until polls the pane through structuredSnapshot, as a window does, until done
// says so.
// convView is what a window sees of a structured pane: the pane's state, from
// structuredSnapshot, and its conversation, which a window reads with the
// conversation commands and the workspace holds as these entries.
type convView struct {
	structuredMsg
	Entries []transcript.Entry
}

func (e *structuredEnv) until(t *testing.T, what string, d time.Duration, done func(m convView) bool) convView {
	t.Helper()
	deadline := time.Now().Add(d)
	var m convView
	for {
		m = convView{e.snapshotOf(t, e.child), e.entries(t)}
		if done(m) {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached in %v; status %q phase %q err %q activity %q entries %d", what, d, m.Status, m.Phase, m.Err, m.Activity, len(m.Entries))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func lastReply(m convView) string {
	for i := len(m.Entries) - 1; i >= 0; i-- {
		if m.Entries[i].Kind == "reply" {
			return m.Entries[i].Markdown
		}
	}
	return ""
}

func (e *structuredEnv) prompt(t *testing.T, text string) {
	t.Helper()
	e.srv.handleCommand(e.c, command{Cmd: "paneSend", ID: e.child, Text: text, Req: "p"})
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-e.c.out:
			if typeOf(raw) == "paneSend" {
				return
			}
			if typeOf(raw) == "notice" {
				t.Fatalf("the prompt was refused: %s", raw)
			}
		case <-deadline:
			t.Fatal("paneSend was not answered")
		}
	}
}

// insideDir reports whether path is dir or below it.
func insideDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cardOf(m convView) map[string]any {
	if m.Card == nil {
		return nil
	}
	opts := []map[string]string{}
	for _, o := range m.Card.Options {
		opts = append(opts, map[string]string{"id": o.ID, "name": o.Name, "kind": o.Kind})
	}
	return map[string]any{"tool": m.Card.Tool, "kind": m.Card.Kind, "summary": m.Card.Summary, "file": m.Card.File,
		"commandPresent": m.Card.Command != "", "command": m.Card.Command, "options": opts}
}

func allowOnce(m convView) string {
	for _, o := range m.Card.Options {
		if o.Kind == "allow_once" {
			return o.ID
		}
	}
	return ""
}

// procs counts running processes other than the query itself that match: by the
// program they run when byExe is true, otherwise by a word in their command
// line. (The command line of go test holds the program's path, so the path is
// not a good word.) Windows only; -1 elsewhere.
func procs(t *testing.T, needle string, byExe bool) int {
	t.Helper()
	if runtime.GOOS != "windows" {
		return -1
	}
	field, op := "CommandLine", "-like"
	pat := "*" + strings.ReplaceAll(needle, "'", "''") + "*"
	if byExe {
		field, op, pat = "ExecutablePath", "-eq", strings.ReplaceAll(needle, "'", "''")
	}
	q := fmt.Sprintf(`(@(Get-CimInstance Win32_Process | Where-Object { $_.%s %s '%s' -and $_.ProcessId -ne $PID })).Count`, field, op, pat)
	out, err := exec.Command("powershell", "-NoProfile", "-Command", q).Output()
	if err != nil {
		t.Logf("process query failed: %v", err)
		return -1
	}
	var n int
	_, _ = fmt.Sscan(strings.TrimSpace(string(out)), &n)
	return n
}

func TestRealOpencodeEndToEnd(t *testing.T) {
	exe := *realOpencode
	if exe == "" {
		t.Skip("-opencode is not set")
	}
	if !filepath.IsAbs(exe) {
		t.Fatalf("-opencode must be an absolute path, got %q", exe)
	}
	obs := observed{Steps: map[string]any{}}
	step := func(name string, v any) { obs.Steps[name] = v; t.Logf("%s: %v", name, v) }
	vctx, vcancel := context.WithTimeout(context.Background(), 20*time.Second)
	if out, err := exec.CommandContext(vctx, exe, "--version").Output(); err == nil {
		obs.Version = strings.TrimSpace(string(out))
	}
	vcancel()

	// Isolation. The agent's profile, local data and XDG data, cache and state
	// directories are new, outside the profile. The config home is left to the
	// workspace's temporary one.
	agentHome := acptest.SessionDir(t)
	work := acptest.SessionDir(t)
	// No opencode.json is written here: Flockdeck's own launch sets the agent to
	// ask (the manifest's launchEnv), and this test is what shows it does. The
	// first run of the test, with no configuration at all, saw no
	// session/request_permission.
	logDir := t.TempDir()
	rawLog := filepath.Join(logDir, "opencode-acp.log")
	// The test isolation has already pointed the home directory at a temporary
	// one, so the real profile is asked of it by name.
	profile := iso.RealHomeDir()
	if profile == "" {
		t.Fatal("the real home directory is not known, so the profile guard cannot be checked")
	}
	for _, d := range []string{agentHome, work} {
		if insideDir(d, profile) {
			t.Fatalf("%s is inside the user profile %s", d, profile)
		}
	}
	testExe, _ := os.Executable()
	before := procs(t, exe, true)
	teeBefore := procs(t, acptest.ArgTee, false)
	if before > 0 {
		t.Fatalf("%d opencode processes were already running before the test", before)
	}
	e := newStructuredEnvWith(t, "Reply with the single word pong", structuredEnvOpts{
		Program: testExe, ProgramArgs: []string{acptest.ArgTee, rawLog, exe, "acp"}, Cwd: work,
		BeforeSpawn: func() {
			t.Setenv("USERPROFILE", agentHome)
			t.Setenv("LOCALAPPDATA", filepath.Join(agentHome, "local"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(agentHome, "data"))
			t.Setenv("XDG_CACHE_HOME", filepath.Join(agentHome, "cache"))
			t.Setenv("XDG_STATE_HOME", filepath.Join(agentHome, "state"))
		},
	})
	write := func() {
		if p := *opencodeReport; p != "" {
			obs.RawLog = rawLog
			if b, err := json.MarshalIndent(obs, "", "  "); err == nil {
				_ = os.WriteFile(p, b, 0o600)
			}
			if b, err := os.ReadFile(rawLog); err == nil {
				_ = os.WriteFile(p+".rpc.log", b, 0o600)
			}
		}
	}
	defer write()

	// 1. Start, and the first prompt (the pane's task).
	m := e.until(t, "the first turn", 150*time.Second, func(m convView) bool {
		return m.Status == "idle" && lastReply(m) != "" || m.Phase == "exited" || m.Phase == "setup"
	})
	if m.Phase == "exited" || m.Phase == "setup" {
		t.Fatalf("the agent did not start: phase %q err %q setup %+v", m.Phase, m.Err, m.Setup)
	}
	step("start", map[string]any{"phase": m.Phase, "reply": lastReply(m), "usage": m.Usage})
	if !strings.Contains(strings.ToLower(lastReply(m)), "pong") {
		t.Errorf("the reply to the first prompt was %q", lastReply(m))
	}

	// 2. A tool that needs permission. The relay's answer is refused, the desk's
	// is taken.
	e.prompt(t, "Use your shell tool to run exactly this command and nothing else: echo e2e-ok . Then reply with the output only.")
	m = e.until(t, "a permission request", 150*time.Second, func(m convView) bool { return m.Card != nil })
	card := cardOf(m)
	step("permissionCard", card)
	if m.Card.Command != "echo e2e-ok" {
		t.Errorf("the request's rawInput.command did not reach the card: %q (T66 relies on it)", m.Card.Command)
	}
	if m.Status != "waiting" {
		t.Errorf("status with a request open = %q", m.Status)
	}
	opt := allowOnce(m)
	if opt == "" {
		t.Fatalf("no allow_once option: %+v", m.Card.Options)
	}
	remote := &controlClient{out: make(chan []byte, 32), remote: true, device: "e2e"}
	e.srv.permissionAnswer(remote, answerCmd(e.child, m.Card.Seq, m.Card.RequestID, opt))
	var refused permissionAnswerMsg
	for raw := range remote.out {
		if typeOf(raw) == "permissionAnswer" {
			_ = json.Unmarshal(raw, &refused)
			break
		}
	}
	step("relayAnswer", map[string]any{"ok": refused.OK, "error": refused.Error})
	if refused.OK || refused.Error != permissionAnswerDeskOnly {
		t.Errorf("the relay's answer: %+v", refused)
	}
	if again := e.snapshotOf(t, e.child); again.Card == nil {
		t.Fatal("the relay's refused answer closed the request")
	}
	reply, notice := send(t, e.srv, e.c, answerCmd(e.child, m.Card.Seq, m.Card.RequestID, opt))
	step("deskAnswer", map[string]any{"ok": reply.OK, "notice": notice})
	if !reply.OK {
		t.Fatalf("the desk's answer: %+v %q", reply, notice)
	}
	m = e.until(t, "the tool's turn to end", 150*time.Second, func(m convView) bool {
		return m.Status == "idle" && m.Card == nil && strings.Contains(lastReply(m), "e2e-ok")
	})
	tools := []map[string]string{}
	for _, en := range m.Entries {
		if en.Kind == "tool" {
			tools = append(tools, map[string]string{"name": en.Name, "label": en.Label, "status": en.Status, "summary": en.Summary})
		}
	}
	step("toolUse", map[string]any{"tools": tools, "reply": lastReply(m), "usage": m.Usage})

	// 3. Cancel in the middle of a command. The command is a node process with a
	// marker in its command line, so it can be found afterwards.
	marker := fmt.Sprintf("e2e-sleep-%d", time.Now().UnixNano())
	e.prompt(t, "Use your shell tool to run exactly this command and wait for it: node -e \"setTimeout(function(){},60000)\" "+marker)
	m = e.until(t, "the second permission request", 150*time.Second, func(m convView) bool { return m.Card != nil })
	step("cancelCard", cardOf(m))
	if !strings.Contains(m.Card.Command, marker) {
		t.Errorf("the second request's rawInput.command did not reach the card: %q", m.Card.Command)
	}
	if r, n := send(t, e.srv, e.c, answerCmd(e.child, m.Card.Seq, m.Card.RequestID, allowOnce(m))); !r.OK {
		t.Fatalf("answer: %+v %q", r, n)
	}
	m = e.until(t, "the command to be running", 90*time.Second, func(m convView) bool {
		for _, en := range m.Entries {
			if en.Kind == "tool" && en.Status == "running" {
				return true
			}
		}
		return false
	})
	runningBefore := procs(t, marker, false)
	res := e.cancel(t, e.c, e.child)
	step("cancel", map[string]any{"ok": res.OK, "error": res.Error, "markerProcessesBefore": runningBefore})
	if !res.OK {
		t.Errorf("cancel: %+v", res)
	}
	m = e.until(t, "the turn to end after the cancel", 60*time.Second, func(m convView) bool { return m.Status == "idle" })
	time.Sleep(3 * time.Second)
	left := procs(t, marker, false)
	step("afterCancel", map[string]any{"status": m.Status, "markerProcessesLeft": left, "lastReply": lastReply(m)})
	if left > 0 {
		t.Logf("the cancelled command's process is still running (%d); it is stopped below", left)
		_ = exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf(`Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like '*%s*' -and $_.ProcessId -ne $PID } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }`, marker)).Run()
		t.Errorf("the cancelled command's process was still running three seconds after the cancel (%d)", left)
	}

	// 4. Restart: the old agent ends, a new session starts, and it answers.
	oldRT, _ := ask(e.srv, func() any { return e.ws.StructuredRuntime(e.child) })
	e.srv.handleCommand(e.c, command{Cmd: "restartPane", ID: e.child})
	swapped := time.Now().Add(60 * time.Second)
	for {
		now, _ := ask(e.srv, func() any { return e.ws.StructuredRuntime(e.child) })
		if now != oldRT && now != nil {
			break
		}
		if time.Now().After(swapped) {
			t.Fatal("the pane kept its old runtime after restartPane")
		}
		time.Sleep(100 * time.Millisecond)
	}
	m = e.until(t, "the restarted agent", 150*time.Second, func(m convView) bool { return m.Phase == "ready" && m.Status == "idle" })
	e.prompt(t, "Reply with the single word pong2")
	m = e.until(t, "the reply after a restart", 150*time.Second, func(m convView) bool {
		return m.Status == "idle" && strings.Contains(strings.ToLower(lastReply(m)), "pong2")
	})
	// The workspace keeps the earlier sessions' entries and a notice ahead of the
	// new session's. structuredSnapshot, which a window watches, is read from the
	// new session alone: that is recorded as a finding, not asserted.
	all, _ := ask(e.srv, func() []string {
		es, _ := e.ws.StructuredEntries(e.child)
		var kinds []string
		for _, en := range es {
			kinds = append(kinds, en.Kind+":"+strings.TrimSpace(en.Text+en.Markdown))
		}
		return kinds
	})
	notices, oldKept := 0, false
	for _, k := range all {
		if strings.HasPrefix(k, "notice:") && strings.Contains(k, "restarted") {
			notices++
		}
		if strings.HasPrefix(k, "reply:") && strings.Contains(k, "e2e-ok") {
			oldKept = true
		}
	}
	snapKept := false
	for _, en := range m.Entries {
		if en.Kind == "reply" && strings.Contains(en.Markdown, "e2e-ok") {
			snapKept = true
		}
	}
	step("restart", map[string]any{"reply": lastReply(m), "workspaceEntries": all, "restartNotices": notices, "earlierRepliesKept": oldKept,
		"snapshotEntries": len(m.Entries), "snapshotHasEarlierSession": snapKept})
	if notices != 1 || !oldKept {
		t.Errorf("after the restart: %d notices, earlier entries kept %v", notices, oldKept)
	}

	// 5. Close with a request open: the pane goes and so does the agent.
	e.prompt(t, "Use your shell tool to run exactly this command and nothing else: echo close-test")
	m = e.until(t, "a request to leave open", 150*time.Second, func(m convView) bool { return m.Card != nil })
	e.srv.handleCommand(e.c, command{Cmd: "closePane", ID: e.child})
	gone := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if pane, _ := ask(e.srv, func() bool { return e.ws.StructuredRuntime(e.child) != nil }); !pane {
			gone = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	time.Sleep(3 * time.Second)
	after := procs(t, exe, true)
	teeAfter := procs(t, acptest.ArgTee, false)
	step("closeWithRequestOpen", map[string]any{"paneGone": gone, "opencodeProcessesBefore": before, "opencodeProcessesAfter": after, "proxyProcessesBefore": teeBefore, "proxyProcessesAfter": teeAfter})
	if !gone {
		t.Error("the pane is still there after closePane")
	}
	if after > 0 || teeAfter > teeBefore {
		t.Errorf("after the close: %d opencode processes and %d proxies left (proxies before: %d)", after, teeAfter, teeBefore)
	}

	// What the raw traffic held.
	if b, err := os.ReadFile(rawLog); err == nil {
		log := string(b)
		obs.PlanSeen = strings.Contains(log, `"sessionUpdate":"plan"`)
		obs.UsageSeen = strings.Contains(log, `"sessionUpdate":"usage_update"`)
		obs.Elicit = strings.Contains(log, "elicitation")
		obs.AskSeen = strings.Contains(log, `"question"`)
		obs.Notes = append(obs.Notes, fmt.Sprintf("raw log: %d bytes", len(b)))
		// The request left open by the close was answered cancelled, not selected.
		if !strings.Contains(log, `"outcome":{"outcome":"cancelled"}`) {
			t.Error("the agent was never answered a cancelled outcome for the request the close left open")
		}
	}
}

// The recording proxy passes a turn through unchanged and keeps both directions.
// This runs with the fake agent, so it needs no network.
func TestTheRecordingProxyRelaysAndRecordsATurn(t *testing.T) {
	testExe, _ := os.Executable()
	rawLog := filepath.Join(t.TempDir(), "proxy.log")
	e := newStructuredEnvWith(t, "plain", structuredEnvOpts{Program: testExe, ProgramArgs: []string{acptest.ArgTee, rawLog, testExe, acptest.ArgAgent}})
	m := e.until(t, "a turn through the proxy", 60*time.Second, func(m convView) bool { return m.Status == "idle" && lastReply(m) != "" })
	if !strings.Contains(lastReply(m), "Hello world") {
		t.Errorf("reply = %q", lastReply(m))
	}
	b, err := os.ReadFile(rawLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"> {", "< {", `"method":"session/prompt"`, `"stopReason"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the recording lacks %q", want)
		}
	}
}

// The profile guard compares with the real home directory. The test isolation
// has moved the home directory the process sees, so a guard that asked the
// process for it would compare with a temporary one and never fire.
func TestTheProfileGuardFiresForAPathUnderTheRealHome(t *testing.T) {
	real := iso.RealHomeDir()
	if real == "" {
		t.Skip("the real home directory is not known")
	}
	if seen, _ := os.UserHomeDir(); seen == real {
		t.Fatalf("the process home is the real one (%s), so the isolation is off", seen)
	}
	if !insideDir(filepath.Join(real, "scratch", "agent"), real) || !insideDir(real, real) {
		t.Error("a path under the real home was not caught")
	}
	if insideDir(real+"-sibling", real) || insideDir(filepath.Join(filepath.Dir(real), "other"), real) {
		t.Error("a path beside the real home was caught")
	}
	if insideDir(filepath.Join(filepath.VolumeName(real)+string(filepath.Separator), "flockdeck-acp-1"), real) {
		t.Error("a path at the root of the drive was caught")
	}
}
