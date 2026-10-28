package claudeacp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func originLine(kind, text string) string {
	return jl(map[string]any{"type": "user", "origin": map[string]any{"kind": kind}, "message": map[string]any{"role": "user", "content": text}})
}

func queued(mode, kind string, isMeta bool, prompt any) string {
	a := map[string]any{"type": "queued_command", "commandMode": mode, "prompt": prompt, "isMeta": isMeta}
	if kind != "" {
		a["origin"] = map[string]any{"kind": kind}
	}
	return jl(map[string]any{"type": "attachment", "attachment": a})
}

func texts(replay []map[string]any) []string {
	var out []string
	for _, u := range replay {
		if c, ok := u["content"].(map[string]any); ok {
			out = append(out, fmt.Sprint(u["sessionUpdate"], ": ", c["text"]))
		}
	}
	return out
}

// One case for each rule that keeps a line out of the replay, or in it. The
// line "keep" follows each case and is always replayed.
func TestSessionLoadRules(t *testing.T) {
	keep := userLine("keep")
	cases := []struct {
		name  string
		lines []string
		want  []string
	}{
		{"isMeta", []string{userLine("meta text", "isMeta", "")}, nil},
		{"sidechain", []string{userLine("helper text", "isSidechain", "")}, nil},
		{"compact summary", []string{userLine("summary text", "isCompactSummary", "")}, nil},
		{"visible in transcript only", []string{userLine("ui note", "isVisibleInTranscriptOnly", "")}, nil},
		{"command name", []string{userLine("<command-name>/review</command-name>\n<command-message>review</command-message>\n<command-args>the diff</command-args>")}, []string{"user_message_chunk: /review the diff"}},
		{"command without args", []string{userLine("<command-name>/clear</command-name>")}, []string{"user_message_chunk: /clear"}},
		{"command output", []string{userLine("<local-command-stdout>done</local-command-stdout>"), userLine("<local-command-caveat>Caveat</local-command-caveat>")}, nil},
		{"bash input and output", []string{userLine("<bash-input>ls</bash-input>"), userLine("<bash-stdout>a</bash-stdout><bash-stderr></bash-stderr>")}, nil},
		{"hook tag", []string{userLine("<user-prompt-submit-hook>blocked</user-prompt-submit-hook>")}, nil},
		{"task notification tag", []string{userLine("<task-notification>\n<task-id>x</task-id>\n</task-notification>")}, nil},
		{"unclosed wrapper", []string{userLine("<system-reminder>never closed")}, nil},
		{"wrapper then text", []string{userLine("<system-reminder>x</system-reminder>\nreal words")}, []string{"user_message_chunk: real words"}},
		{"paste keeps its text", []string{userLine("<pasted_content id=1>some paste</pasted_content>")}, []string{"user_message_chunk: <pasted_content id=1>some paste</pasted_content>"}},
		{"html is the person's", []string{userLine("<div>hi</div>")}, []string{"user_message_chunk: <div>hi</div>"}},
		{"peer origin", []string{originLine("peer", "message from another session")}, nil},
		{"task origin", []string{originLine("task-notification", "a task finished")}, nil},
		{"human origin", []string{originLine("human", "typed")}, []string{"user_message_chunk: typed"}},
		{"interrupt marker", []string{userLine("[Request interrupted by user for tool use]")}, nil},
		{"queued prompt", []string{queued("prompt", "human", false, "typed during a turn")}, []string{"user_message_chunk: typed during a turn"}},
		{"queued prompt without origin", []string{queued("prompt", "", false, "older claude")}, []string{"user_message_chunk: older claude"}},
		{"queued blocks", []string{queued("prompt", "human", false, []any{textBlock("with a picture"), map[string]any{"type": "image"}})}, []string{"user_message_chunk: with a picture\n[image]"}},
		{"queued from a peer", []string{queued("prompt", "peer", false, "from elsewhere")}, nil},
		{"queued reminder", []string{queued("prompt", "", true, "reminder")}, nil},
		{"queued command that is not a prompt", []string{queued("task-notification", "human", false, "mode decides")}, nil},
		{"other attachment with a prompt", []string{jl(map[string]any{"type": "attachment", "attachment": map[string]any{"type": "nested_memory", "commandMode": "prompt", "prompt": "type decides", "note": "user"}})}, nil},
		{"queued task notification", []string{queued("task-notification", "task-notification", false, "<task-notification>x</task-notification>")}, nil},
		{"queue operation line", []string{jl(map[string]any{"type": "queue-operation", "operation": "enqueue", "content": "typed during a turn"})}, nil},
		{"other attachment", []string{jl(map[string]any{"type": "attachment", "attachment": map[string]any{"type": "hook_success", "content": "user text"}})}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, projects := loadHarness(t)
			storeSession(t, projects, h.cwd, append(c.lines, keep)...)
			replay, r := loadSession(t, h)
			if r.Error != nil {
				t.Fatalf("session/load = %+v", r.Error)
			}
			want := append(append([]string(nil), c.want...), "user_message_chunk: keep")
			if got := texts(replay); strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("replayed %q, want %q", got, want)
			}
		})
	}
}

func notices(h *harness) []string {
	var out []string
	for _, u := range h.updates() {
		if u["sessionUpdate"] == "flockdeck_notice" {
			out = append(out, fmt.Sprint(u["code"]))
		}
	}
	return out
}

func TestSessionLoadSaysWhatIsCut(t *testing.T) {
	t.Run("nothing cut", func(t *testing.T) {
		h, projects := loadHarness(t)
		storeSession(t, projects, h.cwd, userLine("a"), assistantLine("claude-sonnet-5-5", textBlock("b")))
		loadSession(t, h)
		if n := notices(h); len(n) != 0 {
			t.Errorf("notices = %v", n)
		}
	})
	t.Run("earlier: file longer than the part read", func(t *testing.T) {
		old := historyMaxScan
		historyMaxScan = 2 << 10
		t.Cleanup(func() { historyMaxScan = old })
		h, projects := loadHarness(t)
		var lines []string
		for i := 0; i < 100; i++ {
			lines = append(lines, userLine(fmt.Sprintf("prompt %d %s", i, strings.Repeat("z", 30))))
		}
		storeSession(t, projects, h.cwd, lines...)
		loadSession(t, h)
		if n := notices(h); strings.Join(n, ",") != "history_cut_earlier" {
			t.Errorf("notices = %v", n)
		}
	})
	t.Run("earlier: entry cap", func(t *testing.T) {
		h, projects := loadHarness(t)
		var lines []string
		for i := 0; i < historyMaxEntries+1; i++ {
			lines = append(lines, userLine("p"))
		}
		storeSession(t, projects, h.cwd, lines...)
		loadSession(t, h)
		if n := notices(h); strings.Join(n, ",") != "history_cut_earlier" {
			t.Errorf("notices = %v", n)
		}
	})
	t.Run("later: time limit", func(t *testing.T) {
		old := historyMaxWait
		historyMaxWait = -time.Second
		t.Cleanup(func() { historyMaxWait = old })
		h, projects := loadHarness(t)
		storeSession(t, projects, h.cwd, userLine("a"))
		replay, r := loadSession(t, h)
		if r.Error != nil || len(replay) != 0 {
			t.Errorf("load = %+v, replayed %d", r.Error, len(replay))
		}
		if n := notices(h); strings.Join(n, ",") != "history_cut_later" {
			t.Errorf("notices = %v", n)
		}
	})
	t.Run("gap: a line too long", func(t *testing.T) {
		h, projects := loadHarness(t)
		storeSession(t, projects, h.cwd, userLine("a"), userLine(strings.Repeat("x", historyMaxLine+10)), userLine("c"))
		loadSession(t, h)
		if n := notices(h); strings.Join(n, ",") != "history_cut_gap" {
			t.Errorf("notices = %v", n)
		}
	})
}

func TestSessionLoadRefusesABadID(t *testing.T) {
	h, projects := loadHarness(t)
	storeSession(t, projects, h.cwd, userLine("secret"))
	h.call("initialize", map[string]any{"protocolVersion": 1})
	for _, bad := range []string{"../x", "..\\" + loadID, loadID + "/../../x", "", "not-a-uuid"} {
		r := h.call("session/load", map[string]any{"sessionId": bad, "cwd": h.cwd, "mcpServers": []any{}})
		if r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("load of %q = %+v", bad, r)
		}
	}
	for _, u := range h.updates() {
		t.Errorf("an update was sent for a refused id: %v", u)
	}
}

func TestSessionLoadDoesNotFollowLinks(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		h, projects := loadHarness(t)
		real := filepath.Join(t.TempDir(), "elsewhere.jsonl")
		if err := os.WriteFile(real, []byte(userLine("through a link")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(projects, nonWord.ReplaceAllString(h.cwd, "-"))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(dir, loadID+".jsonl")); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
		if replay, _ := loadSession(t, h); len(replay) != 0 {
			t.Errorf("replayed %d through a symbolic link", len(replay))
		}
	})
	t.Run("directory", func(t *testing.T) {
		h, projects := loadHarness(t)
		real := t.TempDir()
		if err := os.WriteFile(filepath.Join(real, loadID+".jsonl"), []byte(userLine("through a link")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(projects, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(projects, nonWord.ReplaceAllString(h.cwd, "-"))
		if err := os.Symlink(real, link); err != nil {
			// Windows lets anyone make a junction.
			if out, jerr := exec.Command("cmd", "/c", "mklink", "/J", link, real).CombinedOutput(); jerr != nil {
				t.Skipf("no links here: %v, %v %s", err, jerr, out)
			}
		}
		if replay, _ := loadSession(t, h); len(replay) != 0 {
			t.Errorf("replayed %d through a linked directory", len(replay))
		}
	})
}

func TestSessionLoadFallbackSearchIsOrderedAndBounded(t *testing.T) {
	write := func(projects, dir, text string) {
		if err := os.MkdirAll(filepath.Join(projects, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projects, dir, loadID+".jsonl"), []byte(userLine(text)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("first by name", func(t *testing.T) {
		h, projects := loadHarness(t)
		write(projects, "b-project", "from b")
		write(projects, "a-project", "from a")
		write(projects, "c-project", "from c")
		replay, _ := loadSession(t, h)
		if got := texts(replay); len(got) != 1 || got[0] != "user_message_chunk: from a" {
			t.Errorf("replayed %q", got)
		}
	})
	t.Run("too many directories", func(t *testing.T) {
		old := historyMaxDirs
		historyMaxDirs = 3
		t.Cleanup(func() { historyMaxDirs = old })
		h, projects := loadHarness(t)
		for _, d := range []string{"b", "c", "d"} {
			if err := os.MkdirAll(filepath.Join(projects, d), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		write(projects, "a", "found")
		if replay, _ := loadSession(t, h); len(replay) != 0 {
			t.Errorf("replayed %d from a root over the limit", len(replay))
		}
	})
}

// A tool result that makes the text too large pushes out the oldest entries,
// as an entry does.
func TestSessionLoadResultsCountAgainstTheBytes(t *testing.T) {
	h, projects := loadHarness(t)
	var lines []string
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("t%d", i)
		lines = append(lines, assistantLine("claude-sonnet-5-5", toolUse(id, "Bash", map[string]any{"command": "echo " + id})), toolResult(id, strings.Repeat("r", historyMaxText), false))
	}
	storeSession(t, projects, h.cwd, lines...)
	replay, _ := loadSession(t, h)
	total := 0
	for _, u := range replay {
		if c, ok := u["content"].([]any); ok {
			for _, x := range c {
				total += len(fmt.Sprint(x))
			}
		}
	}
	if total > historyMaxBytes+2*historyMaxText || len(replay) < 10 || len(replay) > 90 {
		t.Errorf("replayed %d tools, %d bytes", len(replay), total)
	}
	if id := replay[len(replay)-1]["toolCallId"]; id != "t99" {
		t.Errorf("the newest tool is %v", id)
	}
}

// The replay is over before claude is started, so what claude does on its own
// comes after it and is not taken for history.
func TestSessionLoadReplayComesBeforeAnythingClaudeDoes(t *testing.T) {
	cfg := t.TempDir()
	h := newHarnessWith(t, mustExe(t), []step{emit(lInit), emit(lMsgStart), emit(textDelta("live")), emit(resultWith("live"))}, "CLAUDE_CONFIG_DIR="+cfg)
	storeSession(t, filepath.Join(cfg, "projects"), h.cwd, userLine("old one"), assistantLine("claude-sonnet-5-5", textBlock("old two")))
	h.call("initialize", map[string]any{"protocolVersion": 1})
	r := h.call("session/load", map[string]any{"sessionId": loadID, "cwd": h.cwd, "mcpServers": []any{}})
	if r.Error != nil {
		t.Fatalf("session/load = %+v", r.Error)
	}
	h.waitFor(func(m rpcMsg) bool {
		return m.Method == "session/update" && strings.Contains(string(m.Params), `"live"`)
	}, 10*time.Second)
	var order []string
	for _, u := range h.updates() {
		if c, ok := u["content"].(map[string]any); ok {
			order = append(order, fmt.Sprint(c["text"]))
		}
	}
	if strings.Join(order, "|") != "old one|old two|live" {
		t.Errorf("order = %q", order)
	}
}
