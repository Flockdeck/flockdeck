package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
	wantArgs := []string{"hook", "--endpoint", "http://127.0.0.1:1/hook", "--token", "tok", "--session", "pane-id", "--event", "SessionStart"}
	for _, version := range []string{"", "2.0.0 (Claude Code)", "2.1.269 (Claude Code)"} {
		claudeVersion = func(string) string { return version }
		path, err := WriteHookSettings(t.TempDir(), "pane-id", "/bin/flockdeck", "http://127.0.0.1:1/hook", "tok")
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
		path, err := WriteHookSettings(t.TempDir(), "pane-id", "/bin/flockdeck", "http://127.0.0.1:1/hook", "tok")
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
	path, err := Settings(spec, t.TempDir(), "pane-id", "/bin/flockdeck", "http://127.0.0.1:1/hook", "tok")
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
		path, err := WriteHookSettings(t.TempDir(), "pane-id", "/bin/flockdeck", "http://127.0.0.1:1/hook", "tok")
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

// A test binary started as `<binary> --version` with FLOCKDECK_SLOW_VERSION set
// stands in for a Claude Code that takes too long to say its version. This
// has to happen before the test flags are parsed, which would reject
// --version and exit at once.
func init() {
	if os.Getenv("FLOCKDECK_SLOW_VERSION") == "1" && len(os.Args) == 2 && os.Args[1] == "--version" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
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

// TestSessionStartDoesNotMoveTheStatusDot keeps a compaction, which reports as
// a session start of its own, from showing a busy agent as idle.
func TestSessionStartDoesNotMoveTheStatusDot(t *testing.T) {
	if _, _, ok := StatusForEvent("SessionStart", ""); ok {
		t.Error("SessionStart should not change a pane's status")
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
