package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestConversationExists guards the check that decides between --resume and a
// fresh --session-id. Getting it wrong the optimistic way kills the pane:
// `claude --resume` exits immediately when there is no transcript.
func TestConversationExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	const withTranscript = "11111111-2222-3333-4444-555555555555"
	const withoutTranscript = "99999999-8888-7777-6666-555555555555"

	// Transcripts live in a per-working-directory folder whose name is derived
	// from that directory; the id is what identifies the conversation.
	projectDir := filepath.Join(home, "projects", "C--Users-someone-code-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, withTranscript+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	if !ConversationExists(withTranscript) {
		t.Error("a session with a transcript should be resumable")
	}
	if ConversationExists(withoutTranscript) {
		t.Error("a session with no transcript must not be resumed")
	}
	if ConversationExists("") {
		t.Error("an empty session id is never resumable")
	}

	// A session interrupted before it recorded anything leaves the file
	// behind with nothing in it, which `claude --resume` rejects as roundly
	// as a missing one.
	const emptyTranscript = "12121212-3434-5656-7878-909090909090"
	if err := os.WriteFile(filepath.Join(projectDir, emptyTranscript+".jsonl"), nil, 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if ConversationExists(emptyTranscript) {
		t.Error("an empty transcript is not something to resume")
	}
}

// TestConversationExistsWithoutClaudeState covers a machine where Claude Code
// has never run.
func TestConversationExistsWithoutClaudeState(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	if ConversationExists("11111111-2222-3333-4444-555555555555") {
		t.Error("no state directory means nothing to resume")
	}
}

// TestClaudeArgsResumeVsFresh pins the two argument shapes.
func TestClaudeArgsResumeVsFresh(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"

	fresh := strings.Join(ClaudeArgs(id, "/tmp/s.json", false, nil), " ")
	if !strings.Contains(fresh, "--session-id "+id) {
		t.Errorf("a new pane should pin its session id, got %q", fresh)
	}
	if strings.Contains(fresh, "--resume") {
		t.Errorf("a new pane must not resume, got %q", fresh)
	}

	resumed := strings.Join(ClaudeArgs(id, "/tmp/s.json", true, nil), " ")
	if !strings.Contains(resumed, "--resume "+id) {
		t.Errorf("a restored pane should resume its conversation, got %q", resumed)
	}
	if strings.Contains(resumed, "--session-id") {
		t.Errorf("resuming must not also pin a session id, got %q", resumed)
	}
	for _, args := range []string{fresh, resumed} {
		if !strings.Contains(args, "--settings /tmp/s.json") {
			t.Errorf("lifecycle hooks must always be installed, got %q", args)
		}
	}
}

// TestHookSettingsRegisterSessionStart checks the generated settings subscribe
// to the event whose reply tells a pane's agent where it is running. It fires
// again on a compaction, which is what keeps that description from being
// summarised away.
//
// A Claude Code known to start a hook as a program and its arguments is given
// it that way, with no shell to quote for; an older one, a command line.
func TestHookSettingsRegisterSessionStart(t *testing.T) {
	was := claudeVersion
	t.Cleanup(func() { claudeVersion = was })
	wantArgs := []string{"hook", "--endpoint", "http://127.0.0.1:1/hook", "--session", "pane-id", "--event", "SessionStart"}
	for _, version := range []string{"", "2.0.0 (Claude Code)", "2.1.269 (Claude Code)"} {
		claudeVersion = func(string) string { return version }
		path, err := WriteHookSettings(t.TempDir(), "pane-id", "/bin/flockdeck", "http://127.0.0.1:1/hook")
		if err != nil {
			t.Fatalf("write settings: %v", err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read settings: %v", err)
		}
		var got settingsFile
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode settings: %v", err)
		}
		matchers, ok := got.Hooks["SessionStart"]
		if !ok || len(matchers) == 0 || len(matchers[0].Hooks) == 0 {
			t.Fatalf("%q: no SessionStart hook in %s", version, raw)
		}
		h := matchers[0].Hooks[0]
		execForm := h.Command == "/bin/flockdeck" && slices.Equal(h.Args, wantArgs)
		lineForm := len(h.Args) == 0 && strings.Contains(h.Command, "--event SessionStart")
		if want := version == "2.1.269 (Claude Code)"; execForm != want || (!want && !lineForm) {
			t.Errorf("%q: SessionStart hook = %q %q, want exec form %v", version, h.Command, h.Args, want)
		}
		// A command's arguments are there for anybody on the machine to read
		// while it runs, so the secret is not among them: the hook reads it
		// from the pane's environment.
		for ev, matchers := range got.Hooks {
			if h := matchers[0].Hooks[0]; strings.Contains(h.Command, "--token") || slices.Contains(h.Args, "--token") {
				t.Errorf("%q: the %s hook carries the secret on its command line: %q %q", version, ev, h.Command, h.Args)
			}
		}
	}
}

// TestLaterHookEventsOnlyForAClaudeCodeKnownToHaveThem covers the events a
// Claude Code older than the one read may not know. A settings file it refused
// would take every hook of the pane with it, so they are only asked of one
// known to have them, and what every Claude Code has is always asked.
func TestLaterHookEventsOnlyForAClaudeCodeKnownToHaveThem(t *testing.T) {
	for _, c := range []struct {
		version string
		later   bool
	}{
		{"2.1.269 (Claude Code)", true},
		{"2.1.270 (Claude Code)", true},
		{"2.2.0 (Claude Code)", true},
		{"3.0.0", true},
		{"2.1.269-beta.1 (Claude Code)", true},
		{"2.1.268 (Claude Code)", false},
		{"2.0.300 (Claude Code)", false},
		{"1.0.128 (Claude Code)", false},
		{"", false},
		{"claude: command not found", false},
	} {
		events := hookEventsFor(c.version)
		for _, ev := range laterHookEvents {
			if slices.Contains(events, ev) != c.later {
				t.Errorf("%q: subscribes to %s = %v, want %v", c.version, ev, !c.later, c.later)
			}
		}
		for _, ev := range hookEvents {
			if !slices.Contains(events, ev) {
				t.Errorf("%q: %s is missing, and every Claude Code has it", c.version, ev)
			}
		}
	}

	// The settings file follows the version the installed Claude Code gives.
	was := claudeVersion
	t.Cleanup(func() { claudeVersion = was })
	for version, want := range map[string]bool{"2.1.269 (Claude Code)": true, "2.0.0 (Claude Code)": false} {
		claudeVersion = func(string) string { return version }
		path, err := WriteHookSettings(t.TempDir(), "pane-id", "/bin/flockdeck", "http://127.0.0.1:1/hook")
		if err != nil {
			t.Fatalf("write settings: %v", err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got settingsFile
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if _, ok := got.Hooks["PermissionRequest"]; ok != want {
			t.Errorf("%s: PermissionRequest subscribed = %v, want %v", version, ok, want)
		}
	}

	// A pane runs the program its Spec names, which a catalog entry can pin
	// somewhere other than the claude on PATH: that is the one asked.
	var asked []string
	claudeVersion = func(exe string) string {
		asked = append(asked, exe)
		return "2.0.0 (Claude Code)"
	}
	spec := claudeLaunchSpec()
	spec.Exe = filepath.Join(t.TempDir(), "pinned", "claude")
	path, err := Settings(spec, t.TempDir(), "pane-id", "/bin/flockdeck", "http://127.0.0.1:1/hook")
	if err != nil || path == "" {
		t.Fatalf("settings: %q, %v", path, err)
	}
	if len(asked) != 1 || asked[0] != spec.Exe {
		t.Errorf("asked %q for its version, want the pane's own program %q", asked, spec.Exe)
	}
}

// TestAnUnreadableVersionKeepsTheCommandLine covers the version question going
// wrong: no such program, one that answers with something else, one that does
// not answer in time. Each is an unknown version, and an unknown version gets
// the hook settings every Claude Code can read -- the command line, not exec
// form, which an older one would start Flockdeck from with no arguments.
func TestAnUnreadableVersionKeepsTheCommandLine(t *testing.T) {
	if got := installedClaudeVersion(filepath.Join(t.TempDir(), "no-such-claude")); got != "" {
		t.Errorf("a missing program reported version %q", got)
	}

	// The test binary itself stands in for a program that answers late.
	wait := versionTimeout
	versionTimeout = 200 * time.Millisecond
	t.Cleanup(func() { versionTimeout = wait })
	t.Setenv("FLOCKDECK_SLOW_VERSION", "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if got := installedClaudeVersion(self); got != "" {
		t.Errorf("a program that did not answer in time reported %q", got)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("the version question took %v; it is bounded by versionTimeout", took)
	} else if took < versionTimeout {
		t.Errorf("the version question gave up after %v, before the program could have been waited for", took)
	}

	was := claudeVersion
	t.Cleanup(func() { claudeVersion = was })
	for _, version := range []string{"", "claude: command not found", "2.1", "v2.1.269", "Claude Code"} {
		claudeVersion = func(string) string { return version }
		path, err := WriteHookSettings(t.TempDir(), "pane-id", "/bin/flockdeck", "http://127.0.0.1:1/hook")
		if err != nil {
			t.Fatalf("write settings: %v", err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got settingsFile
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		for ev, matchers := range got.Hooks {
			if h := matchers[0].Hooks[0]; len(h.Args) > 0 || !strings.Contains(h.Command, "--event "+ev) {
				t.Errorf("%q: %s hook = %q %q, want the command line", version, ev, h.Command, h.Args)
			}
		}
	}
}

// TestAFailedVersionQuestionIsAskedAgain covers a Claude Code that did not
// answer the first time -- starting cold under antivirus, or in the middle of
// updating itself. Keeping that failure for the rest of the run wrote every
// later pane the hooks for an unknown version; it is asked again once
// versionRetry has passed, and a real answer is kept from then on.
func TestAFailedVersionQuestionIsAskedAgain(t *testing.T) {
	wasAsk, wasRetry := askClaudeVersion, versionRetry
	claudeVersions.Lock()
	wasCache := claudeVersions.byExe
	claudeVersions.byExe = nil
	claudeVersions.Unlock()
	t.Cleanup(func() {
		askClaudeVersion, versionRetry = wasAsk, wasRetry
		claudeVersions.Lock()
		claudeVersions.byExe = wasCache
		claudeVersions.Unlock()
	})

	answers := []string{"", "2.1.269 (Claude Code)"}
	asked := 0
	askClaudeVersion = func(string) string {
		v := answers[min(asked, len(answers)-1)]
		asked++
		return v
	}

	versionRetry = time.Hour
	if got := cachedClaudeVersion("claude"); got != "" || asked != 1 {
		t.Fatalf("first question: %q after %d asks", got, asked)
	}
	if got := cachedClaudeVersion("claude"); got != "" || asked != 1 {
		t.Errorf("a failure was asked again straight away (%d asks); a hanging program would cost every pane start the timeout", asked)
	}

	versionRetry = 0
	if got := cachedClaudeVersion("claude"); got != "2.1.269 (Claude Code)" || asked != 2 {
		t.Errorf("once versionRetry had passed: %q after %d asks, want the version on the second", got, asked)
	}
	if got := cachedClaudeVersion("claude"); got != "2.1.269 (Claude Code)" || asked != 2 {
		t.Errorf("an answer was not kept: %q after %d asks", got, asked)
	}
}

// A test binary started as `<binary> --version` with FLOCKDECK_SLOW_VERSION set
// stands in for a Claude Code that takes too long to say its version. This
// has to happen before the test flags are parsed, which would reject
// --version and exit at once.
//
// Set to "orphan", it first starts a copy of itself that holds its standard
// output for four seconds, as node does when an npm shim's cmd.exe is killed:
// longer than the shortened timeout and WaitDelay together, which is what the
// tests need, and no longer, so as not to leave it running long after them.
// Set to "answered", it starts the same copy, then says its version and exits
// as a program should.
func init() {
	if len(os.Args) != 2 || os.Args[1] != "--version" {
		return
	}
	holdOutput := func() {
		held := exec.Command(os.Args[0], "--version")
		held.Env = append(os.Environ(), "FLOCKDECK_SLOW_VERSION=held")
		held.Stdout = os.Stdout
		_ = held.Start()
	}
	switch os.Getenv("FLOCKDECK_SLOW_VERSION") {
	case "1":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "held":
		time.Sleep(4 * time.Second)
		os.Exit(0)
	case "orphan":
		holdOutput()
		// Tell the test the copy is holding the pipe, and who to kill.
		if ready := os.Getenv("FLOCKDECK_ORPHAN_READY"); ready != "" {
			_ = os.WriteFile(ready+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o600)
			_ = os.Rename(ready+".tmp", ready)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "answered":
		holdOutput()
		fmt.Println("2.1.269 (Claude Code)")
		os.Exit(0)
	}
}

// TestTheVersionQuestionIsNotHeldByAnOrphan covers a Claude Code that leaves
// something behind holding its output when it is killed for taking too long:
// an npm install on Windows is claude.cmd, and killing its cmd.exe leaves node
// running. The answer was waited for until that let go as well, however long,
// and it is asked on the goroutine that owns the workspace.
//
// The program is killed by the test once it says it has started what it leaves
// behind, rather than by a timeout guessed to be long enough for that: a guess
// too short on a slow machine killed it first, and the test then passed without
// anything holding the pipe at all. Killed from outside, it ends as it would on
// the timeout, and the wait from then on is WaitDelay's second, not the four
// seconds the copy it left holds the pipe for.
func TestTheVersionQuestionIsNotHeldByAnOrphan(t *testing.T) {
	wait := versionTimeout
	// Only a backstop: the test ends the program long before this.
	versionTimeout = 30 * time.Second
	t.Cleanup(func() { versionTimeout = wait })
	ready := filepath.Join(t.TempDir(), "ready")
	t.Setenv("FLOCKDECK_SLOW_VERSION", "orphan")
	t.Setenv("FLOCKDECK_ORPHAN_READY", ready)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	killed := make(chan time.Time, 1)
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if raw, err := os.ReadFile(ready); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
					if p, err := os.FindProcess(pid); err == nil {
						killed <- time.Now()
						_ = p.Kill()
						return
					}
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		close(killed)
	}()

	if got := installedClaudeVersion(self); got != "" {
		t.Errorf("a program that did not answer in time reported %q", got)
	}
	ended := time.Now()
	at, ok := <-killed
	if !ok {
		t.Fatal("the program never said it had started what it leaves behind")
	}
	if took := ended.Sub(at); took > 3*time.Second {
		t.Errorf("the version question took %v after the program was killed, held open by what it left behind", took)
	}
}

// TestAVersionAnsweredBeforeSomethingHoldsThePipeIsKept covers the other side
// of that bound. A Claude Code that says its version and exits, leaving
// something it started holding its output, has answered: WaitDelay ends the
// wait with its answer already read, and throwing that away gave the pane the
// hooks for an unknown version, and asked again every minute.
func TestAVersionAnsweredBeforeSomethingHoldsThePipeIsKept(t *testing.T) {
	wait := versionTimeout
	versionTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { versionTimeout = wait })
	t.Setenv("FLOCKDECK_SLOW_VERSION", "answered")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if got := installedClaudeVersion(self); got != "2.1.269 (Claude Code)" {
		t.Errorf("a program that answered and exited reported %q, want its version", got)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("the version question took %v, held open by what the program left behind", took)
	}
}

// TestHookCommandQuotingIsLiteral covers the paths the hook command is written
// with. sh expands "$" and a backtick inside double quotes, so a binary under
// such a directory ran something else on macOS and Linux.
func TestHookCommandQuotingIsLiteral(t *testing.T) {
	cases := []struct{ goos, in, want string }{
		{"linux", "/usr/local/bin/flockdeck", "/usr/local/bin/flockdeck"},
		{"linux", "/home/me/$work/flockdeck", `'/home/me/$work/flockdeck'`},
		{"darwin", "/Users/me/`x`/flockdeck", "'/Users/me/`x`/flockdeck'"},
		{"linux", "/home/it's/flockdeck", `'/home/it'\''s/flockdeck'`},
		{"linux", "/home/a;b/flockdeck", `'/home/a;b/flockdeck'`},
		{"linux", "", `''`},
		{"windows", `C:\Program Files\flockdeck.exe`, `"C:\Program Files\flockdeck.exe"`},
		{"windows", "", `""`},
		{"windows", "0123abcdef", "0123abcdef"},
	}
	for _, c := range cases {
		if got := quoteArgFor(c.goos, c.in); got != c.want {
			t.Errorf("quoteArgFor(%s, %q) = %s, want %s", c.goos, c.in, got, c.want)
		}
	}
}

// TestTheFallbackHookLineRunsInPowerShellAndGitBash covers the command line a
// pane's hooks are written as when Claude Code's version is not known. On
// Windows Claude Code runs it through Git Bash, or PowerShell where there is
// none, and it was quoted for cmd.exe: a program in double quotes, which
// PowerShell takes for a string, so without Git Bash no hook of any pane
// arrived. Elsewhere it is what it always was.
func TestTheFallbackHookLineRunsInPowerShellAndGitBash(t *testing.T) {
	const exe = `C:\Program Files\Flockdeck\flockdeck.exe`
	const rest = " hook --endpoint http://127.0.0.1:1/hook --session pane-id --event Stop"
	noShort := func(string) string { return "" }
	cases := []struct {
		name    string
		short   func(string) string
		gitBash bool
		want    string
	}{
		{"PowerShell, no short name", noShort, false, `& 'C:\Program Files\Flockdeck\flockdeck.exe'` + rest},
		{"Git Bash, no short name", noShort, true, `'C:/Program Files/Flockdeck/flockdeck.exe'` + rest},
		{"either, with a short name", func(string) string { return `C:\PROGRA~1\FLOCKD~1\flockdeck.exe` }, false,
			`C:/PROGRA~1/FLOCKD~1/flockdeck.exe` + rest},
	}
	for _, c := range cases {
		if got := hookCommandLine("windows", exe, "http://127.0.0.1:1/hook", "pane-id", "Stop", c.short, c.gitBash); got != c.want {
			t.Errorf("%s: %s\nwant %s", c.name, got, c.want)
		}
	}
	if got, want := hookCommandLine("linux", "/opt/my apps/flockdeck", "http://127.0.0.1:1/hook", "pane-id", "Stop", noShort, false),
		`'/opt/my apps/flockdeck'`+rest; got != want {
		t.Errorf("sh: %s\nwant %s", got, want)
	}
}

// TestSessionStartDoesNotMoveTheStatusDot keeps a compaction, which reports as
// a session start of its own, from showing a busy agent as idle.
func TestSessionStartDoesNotMoveTheStatusDot(t *testing.T) {
	if _, _, ok := StatusForEvent("SessionStart", ""); ok {
		t.Error("SessionStart should not change a pane's status")
	}
}

// TestIsIdleReminder covers telling Claude Code's idle nudge apart from a
// real ask: only a Notification whose notification_type is idle_prompt is
// one, since that is the only kind a helper's idle parent should be allowed
// to swallow.
func TestIsIdleReminder(t *testing.T) {
	cases := []struct {
		event, notificationType string
		want                    bool
	}{
		{"Notification", "idle_prompt", true},
		{"Notification", "permission_prompt", false},
		{"Notification", "elicitation_dialog", false},
		{"Notification", "agent_needs_input", false},
		{"Notification", "", false},
		{"PreToolUse", "idle_prompt", false},
		{"PermissionRequest", "", false},
	}
	for _, c := range cases {
		if got := IsIdleReminder(c.event, c.notificationType); got != c.want {
			t.Errorf("IsIdleReminder(%q, %q) = %v, want %v", c.event, c.notificationType, got, c.want)
		}
	}
}

// TestClaudeArgsGuardsATaskThatLooksLikeAFlag covers the opening prompt a pane
// is spawned with. Nothing stops someone starting a task with a dash, and the
// CLI would take it for an option it does not have and exit at once.
func TestClaudeArgsGuardsATaskThatLooksLikeAFlag(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"

	got := ClaudeArgs(id, "", false, []string{"--verbose is the wrong flag; fix the parser"})
	last := got[len(got)-2:]
	if last[0] != "--" {
		t.Errorf("argv = %q; the task should be shielded with --", got)
	}

	// The common case is left exactly as it was.
	plain := ClaudeArgs(id, "", false, []string{"fix the parser"})
	for _, a := range plain {
		if a == "--" {
			t.Errorf("argv = %q; an ordinary task needs no --", plain)
		}
	}
	if plain[len(plain)-1] != "fix the parser" {
		t.Errorf("argv = %q; the task should come last", plain)
	}
}

// TestNoLifecycleEventCanMarkALivePaneExited walks every event the settings
// file subscribes to and applies what it maps to. A pane marked exited while
// its process is running is not a cosmetic mistake: it refuses what is typed
// into it and hands new viewers a closed stream, and nothing arrives later to
// put it right.
func TestNoLifecycleEventCanMarkALivePaneExited(t *testing.T) {
	for _, ev := range append(slices.Clip(hookEvents), laterHookEvents...) {
		st, detail, ok := StatusForEvent(ev, "Bash")
		if !ok {
			continue
		}

		f := newFakePTY()
		s := fakeSession(f)
		s.Kind = KindClaude
		s.SetStatus(st, detail)

		if s.Exited() {
			t.Errorf("%s marks a pane exited while its process is running", ev)
		}
		if err := s.WriteString("are you there?"); err != nil {
			t.Errorf("%s left the pane refusing input: %v", ev, err)
		}
		if id, _, out := s.Subscribe(); id < 0 {
			t.Errorf("%s left new viewers with a closed stream", ev)
		} else {
			s.Unsubscribe(id)
			_ = out
		}
		_ = f.Close()
	}
}
