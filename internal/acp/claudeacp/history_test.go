package claudeacp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const loadID = "0b7e1f0e-5c2a-4a3e-9d7a-6f1f3f0b9c11"

// loadHarness is a harness whose claude config directory is a temporary one.
// The stored session goes in projects/<dir>/<loadID>.jsonl under cfg.
func loadHarness(t *testing.T) (h *harness, projects string) {
	t.Helper()
	cfg := t.TempDir()
	h = newHarnessWith(t, mustExe(t), nil, "CLAUDE_CONFIG_DIR="+cfg)
	return h, filepath.Join(cfg, "projects")
}

// storeSession writes lines as the stored session of the cwd.
func storeSession(t *testing.T, projects, cwd string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(projects, nonWord.ReplaceAllString(cwd, "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, loadID+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func jl(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func userLine(content any, extra ...string) string {
	m := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = true
	}
	return jl(m)
}

func assistantLine(model string, blocks ...map[string]any) string {
	return jl(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "model": model, "content": blocks}})
}

func textBlock(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func toolUse(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func toolResult(id, text string, isErr bool) string {
	return userLine([]any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isErr}})
}

// loadSession calls session/load and returns the replayed updates, which have
// to be in front of the answer.
func loadSession(t *testing.T, h *harness) ([]map[string]any, rpcMsg) {
	t.Helper()
	if r := h.call("initialize", map[string]any{"protocolVersion": 1}); r.Error != nil {
		t.Fatalf("initialize: %v", r.Error)
	}
	r := h.call("session/load", map[string]any{"sessionId": loadID, "cwd": h.cwd, "mcpServers": []any{}})
	var replay []map[string]any
	for _, u := range h.updates() {
		switch u["sessionUpdate"] {
		case "user_message_chunk", "agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update":
			replay = append(replay, u)
		}
	}
	return replay, r
}

func TestSessionLoadReplaysTheStoredConversation(t *testing.T) {
	h, projects := loadHarness(t)
	storeSession(t, projects, h.cwd,
		jl(map[string]any{"type": "permission-mode", "permissionMode": "default"}),
		userLine("a note claude wrote to itself", "isMeta", ""),
		userLine("fix the flaky test"),
		assistantLine("claude-sonnet-5-5", map[string]any{"type": "thinking", "thinking": "", "signature": "abc"}),
		assistantLine("claude-sonnet-5-5", textBlock("Looking at it."), toolUse("t1", "Bash", map[string]any{"command": "go test ./x"})),
		toolResult("t1", "FAIL x", true),
		userLine("[Request interrupted by user]"),
		userLine("<command-name>/compact</command-name>\n<command-args>keep tests</command-args>"),
		userLine("<local-command-stdout>Compacted</local-command-stdout>"),
		userLine("summary of the old conversation", "isCompactSummary", ""),
		jl(map[string]any{"type": "assistant", "isApiErrorMessage": true, "message": map[string]any{"role": "assistant", "model": "<synthetic>", "content": []any{textBlock("API Error: 529 overloaded")}}}),
		assistantLine("<synthetic>", textBlock("No response requested.")),
		jl(map[string]any{"type": "assistant", "isSidechain": true, "message": map[string]any{"role": "assistant", "content": []any{textBlock("subagent chatter")}}}),
		"this is not json {",
		assistantLine("claude-sonnet-5-5", textBlock("Fixed.")),
	)
	replay, r := loadSession(t, h)
	if r.Error != nil {
		t.Fatalf("session/load = %+v", r.Error)
	}
	var got []string
	for _, u := range replay {
		switch u["sessionUpdate"] {
		case "tool_call":
			got = append(got, fmt.Sprintf("tool_call %s %s", u["status"], u["kind"]))
		default:
			got = append(got, fmt.Sprintf("%s %s", u["sessionUpdate"], u["content"].(map[string]any)["text"]))
		}
	}
	want := []string{
		"user_message_chunk fix the flaky test",
		"agent_message_chunk Looking at it.",
		"tool_call failed execute",
		"user_message_chunk /compact keep tests",
		"agent_message_chunk Fixed.",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("replayed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if c, _ := replay[2]["content"].([]any); len(c) != 1 || !strings.Contains(fmt.Sprint(c[0]), "FAIL x") {
		t.Errorf("the tool result is not in the tool call: %v", replay[2])
	}
	// The updates are for the id the client asked for, and claude resumes it.
	for _, m := range h.msgs {
		if m.Method == "session/update" && !strings.Contains(string(m.Params), `"sessionId":"`+loadID+`"`) {
			t.Errorf("an update for another session: %s", m.Params)
		}
	}
	if args := readLog(h.log)[0]; !strings.Contains(args, "--resume "+loadID) {
		t.Errorf("claude was started with %q", args)
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(r.Result, &res)
	if res.SessionID != loadID {
		t.Errorf("answer = %s", r.Result)
	}
}

func TestSessionLoadWithNoStoredConversationStillOpens(t *testing.T) {
	h, _ := loadHarness(t)
	replay, r := loadSession(t, h)
	if r.Error != nil || len(replay) != 0 {
		t.Errorf("load = %+v, replayed %d", r.Error, len(replay))
	}
	if args := readLog(h.log)[0]; !strings.Contains(args, "--resume "+loadID) {
		t.Errorf("claude was started with %q", args)
	}
}

func TestSessionLoadFindsTheFileUnderAnotherProjectDirectory(t *testing.T) {
	h, projects := loadHarness(t)
	other := filepath.Join(projects, "some-other-spelling-of-the-cwd")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	line := userLine("hello again")
	if err := os.WriteFile(filepath.Join(other, loadID+".jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replay, r := loadSession(t, h)
	if r.Error != nil || len(replay) != 1 {
		t.Errorf("load = %+v, replayed %d", r.Error, len(replay))
	}
}

// A file that is not what claude writes must not stop the session from opening
// or the adapter from running.
func TestSessionLoadSurvivesAHostileFile(t *testing.T) {
	big := userLine(strings.Repeat("x", historyMaxLine+10))
	for name, lines := range map[string][]string{
		"binary":        {"\x00\x01\x02\xff\xfe", "\x1b[31m{", "{\"type\":"},
		"wrong shapes":  {`[]`, `"x"`, `{"type":"user","message":5}`, `{"type":"assistant","message":{"content":"text"}}`, `{"type":"user","message":{"content":[1,2,{"type":"tool_result"}]}}`, `null`},
		"one huge line": {big, userLine("after the huge line")},
		"no newline":    {strings.Repeat("a", 300<<10)},
	} {
		t.Run(name, func(t *testing.T) {
			h, projects := loadHarness(t)
			storeSession(t, projects, h.cwd, lines...)
			start := time.Now()
			replay, r := loadSession(t, h)
			if r.Error != nil {
				t.Fatalf("session/load = %+v", r.Error)
			}
			if time.Since(start) > historyMaxWait {
				t.Errorf("took %v", time.Since(start))
			}
			if name == "one huge line" && len(replay) != 1 {
				t.Errorf("the line after the huge one was not read: %d replayed", len(replay))
			}
			for _, u := range replay {
				if len(fmt.Sprint(u)) > historyMaxText+1024 {
					t.Errorf("an update of %d bytes", len(fmt.Sprint(u)))
				}
			}
		})
	}
}

func TestSessionLoadIgnoresAFileThatIsADirectory(t *testing.T) {
	h, projects := loadHarness(t)
	dir := filepath.Join(projects, nonWord.ReplaceAllString(h.cwd, "-"), loadID+".jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	replay, r := loadSession(t, h)
	if r.Error != nil || len(replay) != 0 {
		t.Errorf("load = %+v, replayed %d", r.Error, len(replay))
	}
}

func TestSessionLoadKeepsTheNewestEntries(t *testing.T) {
	h, projects := loadHarness(t)
	var lines []string
	for i := 0; i < historyMaxEntries+500; i++ {
		lines = append(lines, userLine(fmt.Sprintf("prompt %d", i)))
	}
	storeSession(t, projects, h.cwd, lines...)
	replay, r := loadSession(t, h)
	if r.Error != nil {
		t.Fatalf("session/load = %+v", r.Error)
	}
	if len(replay) != historyMaxEntries {
		t.Fatalf("replayed %d entries, want %d", len(replay), historyMaxEntries)
	}
	last := fmt.Sprintf("prompt %d", historyMaxEntries+499)
	if replay[len(replay)-1]["content"].(map[string]any)["text"] != last || replay[0]["content"].(map[string]any)["text"] != "prompt 500" {
		t.Errorf("first %v, last %v", replay[0]["content"], replay[len(replay)-1]["content"])
	}
}

func TestSessionLoadCapsTheBytes(t *testing.T) {
	h, projects := loadHarness(t)
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, assistantLine("claude-sonnet-5-5", textBlock(fmt.Sprintf("%03d", i)+strings.Repeat("y", historyMaxText))))
	}
	storeSession(t, projects, h.cwd, lines...)
	replay, _ := loadSession(t, h)
	total := 0
	for _, u := range replay {
		total += len(u["content"].(map[string]any)["text"].(string))
	}
	if total > historyMaxBytes || len(replay) < 10 {
		t.Errorf("replayed %d entries, %d bytes", len(replay), total)
	}
	if text := replay[len(replay)-1]["content"].(map[string]any)["text"].(string); !strings.HasPrefix(text, "199") {
		t.Errorf("the newest entry is %q", text[:3])
	}
}

func TestSessionLoadReadsOnlyTheEndOfALargeFile(t *testing.T) {
	old := historyMaxScan
	historyMaxScan = 4 << 10
	t.Cleanup(func() { historyMaxScan = old })
	h, projects := loadHarness(t)
	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines, userLine(fmt.Sprintf("prompt %03d %s", i, strings.Repeat("z", 40))))
	}
	storeSession(t, projects, h.cwd, lines...)
	replay, _ := loadSession(t, h)
	if len(replay) == 0 || len(replay) >= 100 {
		t.Fatalf("replayed %d entries", len(replay))
	}
	// The first line read is a whole one: the cut line is skipped.
	if text := replay[0]["content"].(map[string]any)["text"].(string); !strings.HasPrefix(text, "prompt ") || !strings.HasSuffix(text, "zzzz") {
		t.Errorf("first entry %q", text)
	}
	if text := replay[len(replay)-1]["content"].(map[string]any)["text"].(string); !strings.HasPrefix(text, "prompt 299") {
		t.Errorf("last entry %q", text)
	}
}

func TestSessionLoadKeepsAnEditsDiffAndDropsResultsWithoutACall(t *testing.T) {
	h, projects := loadHarness(t)
	file := filepath.Join(h.cwd, "a.txt")
	storeSession(t, projects, h.cwd,
		toolResult("orphan", "a result whose call is not in the file", false),
		assistantLine("claude-sonnet-5-5", toolUse("e1", "Edit", map[string]any{"file_path": file, "old_string": "one", "new_string": "two"})),
		toolResult("e1", "The file a.txt has been updated.", false),
		assistantLine("claude-sonnet-5-5", toolUse("w1", "TodoWrite", map[string]any{"todos": []any{map[string]any{"content": "x", "status": "pending", "activeForm": "x"}}})),
		toolResult("w1", "Todos modified", false),
	)
	replay, _ := loadSession(t, h)
	if len(replay) != 1 {
		t.Fatalf("replayed %d updates: %v", len(replay), replay)
	}
	u := replay[0]
	c, _ := u["content"].([]any)
	if u["sessionUpdate"] != "tool_call" || u["status"] != "completed" || len(c) != 1 || !strings.Contains(fmt.Sprint(c[0]), "diff") {
		t.Errorf("edit = %v", u)
	}
}

// A replay never reaches the person as something typed or as an error: this
// checks the raw claude lines for those two, which the bridge cannot tell
// apart afterwards.
func TestSessionLoadSendsNoAgentErrorText(t *testing.T) {
	h, projects := loadHarness(t)
	storeSession(t, projects, h.cwd,
		userLine("hello"),
		jl(map[string]any{"type": "assistant", "error": "rate_limit", "message": map[string]any{"role": "assistant", "model": "claude-sonnet-5-5", "content": []any{textBlock("You've hit your limit")}}}),
		jl(map[string]any{"type": "assistant", "isApiErrorMessage": true, "message": map[string]any{"role": "assistant", "model": "claude-sonnet-5-5", "content": []any{textBlock("API Error: 500")}}}),
		assistantLine("claude-sonnet-5-5", textBlock("hi")),
	)
	replay, _ := loadSession(t, h)
	var all strings.Builder
	for _, u := range replay {
		all.WriteString(fmt.Sprint(u["content"]))
	}
	if strings.Contains(all.String(), "limit") || strings.Contains(all.String(), "API Error") || len(replay) != 2 {
		t.Errorf("replayed %d: %s", len(replay), all.String())
	}
}

// The client marks updates for the id it asked for as replay, so they carry
// that spelling, though the file is found under the canonical one.
func TestSessionLoadUpdatesCarryTheIDAsAsked(t *testing.T) {
	h, projects := loadHarness(t)
	storeSession(t, projects, h.cwd, userLine("hello"))
	h.call("initialize", map[string]any{"protocolVersion": 1})
	asked := strings.ToUpper(loadID)
	r := h.call("session/load", map[string]any{"sessionId": asked, "cwd": h.cwd, "mcpServers": []any{}})
	if r.Error != nil {
		t.Fatalf("session/load = %+v", r.Error)
	}
	n := 0
	for _, m := range h.msgs {
		if m.Method == "session/update" && strings.Contains(string(m.Params), "user_message_chunk") {
			n++
			if !strings.Contains(string(m.Params), `"sessionId":"`+asked+`"`) {
				t.Errorf("update = %s", m.Params)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d replayed prompts", n)
	}
}

// linked gives a stored line the uuid and parent claude writes.
func linked(line, uuid, parent string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		panic(err)
	}
	m["uuid"] = uuid
	if parent != "" {
		m["parentUuid"] = parent
	}
	return jl(m)
}

// A session cut back with --resume-session-at keeps the dropped turns in the
// file, after which the new branch is appended. The replay shows the branch the
// newest line is on, and not the dropped turns.
func TestSessionLoadFollowsTheBranchTheNewestLineIsOn(t *testing.T) {
	h, projects := loadHarness(t)
	att := jl(map[string]any{"type": "attachment", "attachment": map[string]any{"type": "hook_success"}})
	storeSession(t, projects, h.cwd,
		linked(userLine("first"), "u1", ""),
		linked(assistantLine("claude-sonnet-5-5", textBlock("answer one")), "a1", "u1"),
		linked(userLine("dropped prompt"), "u2", "a1"),
		linked(assistantLine("claude-sonnet-5-5", textBlock("dropped answer")), "a2", "u2"),
		jl(map[string]any{"type": "last-prompt"}),
		linked(userLine("second, after the rewind"), "u3", "a1"),
		linked(att, "x3", "u3"),
		linked(assistantLine("claude-sonnet-5-5", textBlock("answer three")), "a3", "x3"),
		jl(map[string]any{"type": "cost-state"}),
	)
	replay, r := loadSession(t, h)
	if r.Error != nil {
		t.Fatalf("session/load = %+v", r.Error)
	}
	var got []string
	for _, u := range replay {
		got = append(got, fmt.Sprintf("%s %s", u["sessionUpdate"], u["content"].(map[string]any)["text"]))
	}
	want := "user_message_chunk first|agent_message_chunk answer one|user_message_chunk second, after the rewind|agent_message_chunk answer three"
	if strings.Join(got, "|") != want {
		t.Errorf("replayed:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// A chain that loops ends at the first line seen again.
func TestSessionLoadSurvivesABranchThatLoops(t *testing.T) {
	h, projects := loadHarness(t)
	storeSession(t, projects, h.cwd,
		linked(userLine("one"), "u1", "a1"),
		linked(assistantLine("claude-sonnet-5-5", textBlock("two")), "a1", "u1"),
	)
	replay, r := loadSession(t, h)
	if r.Error != nil || len(replay) != 2 {
		t.Fatalf("replay = %d entries, err %v", len(replay), r.Error)
	}
}

// bigLine is a user line too long to read, written in claude's order of members:
// the parent first, and the uuid after the message.
func bigLine(uuid, parent string) string {
	return `{"parentUuid":"` + parent + `","isSidechain":false,"type":"user","message":{"role":"user","content":"` +
		strings.Repeat("x", historyMaxLine+10) + `"},"uuid":"` + uuid + `","timestamp":"2026-10-28T00:00:00Z","cwd":"/x","sessionId":"s"}`
}

// A line too long to read that is on the chain does not end it: the lines
// before it are still replayed, and the dropped branch still is not.
func TestSessionLoadFollowsTheBranchThroughALineTooLongToRead(t *testing.T) {
	h, projects := loadHarness(t)
	storeSession(t, projects, h.cwd,
		linked(userLine("first"), "u1", ""),
		linked(assistantLine("claude-sonnet-5-5", textBlock("answer one")), "a1", "u1"),
		linked(userLine("dropped prompt"), "u2", "a1"),
		linked(assistantLine("claude-sonnet-5-5", textBlock("dropped answer")), "a2", "u2"),
		bigLine("b1", "a1"),
		linked(assistantLine("claude-sonnet-5-5", textBlock("answer after the big one")), "a3", "b1"),
	)
	replay, r := loadSession(t, h)
	if r.Error != nil {
		t.Fatalf("session/load = %+v", r.Error)
	}
	var got []string
	for _, u := range replay {
		got = append(got, fmt.Sprintf("%s %s", u["sessionUpdate"], u["content"].(map[string]any)["text"]))
	}
	want := "user_message_chunk first|agent_message_chunk answer one|agent_message_chunk answer after the big one"
	if strings.Join(got, "|") != want {
		t.Errorf("replayed:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// When the link of a line too long to read cannot be had, nothing is dropped
// for it: the file is replayed in order.
func TestSessionLoadReplaysInOrderWhenAChainLinkIsLost(t *testing.T) {
	h, projects := loadHarness(t)
	lost := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", historyMaxLine+10) + `"}}`
	storeSession(t, projects, h.cwd,
		linked(userLine("first"), "u1", ""),
		linked(assistantLine("claude-sonnet-5-5", textBlock("answer one")), "a1", "u1"),
		lost,
		linked(userLine("second"), "u2", "a1"),
	)
	replay, r := loadSession(t, h)
	if r.Error != nil || len(replay) != 3 {
		t.Fatalf("replay = %d entries, err %v", len(replay), r.Error)
	}
}
