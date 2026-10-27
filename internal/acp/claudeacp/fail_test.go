package claudeacp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Result and assistant lines as claude 2.1.312 wrote them for a model that does
// not exist (recorded on 27 Oct 2026). The other failures use the same fields
// with the values claude's own code gives them: an assistant line with "error",
// and a result with api_error_status and terminal_reason.
const (
	lResultBadModel = `{"type":"result","subtype":"success","is_error":true,"api_error_status":404,"terminal_reason":"api_error","stop_reason":"stop_sequence","result":"SECRET-text selected model","session_id":"SID","total_cost_usd":0,"num_turns":1,"usage":{"input_tokens":0,"output_tokens":0},"modelUsage":{}}`
)

func apiErrorAssistant(code string) string {
	return `{"type":"assistant","message":{"id":"m2","model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"SECRET-text"}],"usage":{"input_tokens":0,"output_tokens":0}},"parent_tool_use_id":null,"error":"` + code + `","is_api_error_message":true}`
}

func errResult(extra string) string {
	return `{"type":"result","subtype":"success","is_error":true,` + extra + `"result":"SECRET-text","session_id":"SID","total_cost_usd":0,"num_turns":1,"usage":{"input_tokens":0,"output_tokens":0},"modelUsage":{}}`
}

func dataClass(e *rpcErr) string {
	m, _ := e.Data.(map[string]any)
	c, _ := m["class"].(string)
	return c
}

func TestFailureClasses(t *testing.T) {
	tests := []struct {
		name  string
		res   string
		facts turnFacts
		class string
		code  int
	}{
		{"bad model", lResultBadModel, turnFacts{apiError: "model_not_found"}, classOther, codeInternal},
		{"rate limit", errResult(`"api_error_status":429,"terminal_reason":"api_error",`), turnFacts{apiError: "rate_limit"}, classUsageLimit, codeInternal},
		{"rate limit code alone", errResult(`"terminal_reason":"api_error",`), turnFacts{apiError: "rate_limit"}, classUsageLimit, codeInternal},
		{"billing", errResult(`"terminal_reason":"api_error",`), turnFacts{apiError: "billing_error"}, classUsageLimit, codeInternal},
		{"rejected limit event only", errResult(`"terminal_reason":"api_error",`), turnFacts{rejected: true, resetsAt: 1}, classUsageLimit, codeInternal},
		{"prompt too long", errResult(`"terminal_reason":"prompt_too_long",`), turnFacts{apiError: "invalid_request"}, classContext, codeInternal},
		{"blocking limit", errResult(`"terminal_reason":"blocking_limit",`), turnFacts{}, classContext, codeInternal},
		{"413", errResult(`"api_error_status":413,`), turnFacts{}, classContext, codeInternal},
		{"login", errResult(`"api_error_status":401,"terminal_reason":"api_error",`), turnFacts{apiError: "authentication_failed"}, classAuth, codeAuthRequired},
		{"403 is not a login problem", errResult(`"api_error_status":403,`), turnFacts{}, classOther, codeInternal},
		{"org not allowed", errResult(``), turnFacts{apiError: "oauth_org_not_allowed"}, classAuth, codeAuthRequired},
		{"server error", errResult(`"terminal_reason":"api_error",`), turnFacts{apiError: "server_error"}, classOutage, codeInternal},
		{"overloaded 529", errResult(`"api_error_status":529,"terminal_reason":"api_error",`), turnFacts{}, classOutage, codeInternal},
		// the cases are tried in order: a rejected limit event wins over a refusal
		{"rejected event before a refusal", errResult(`"stop_reason":"refusal",`), turnFacts{rejected: true}, classUsageLimit, codeInternal},
		{"refusal", errResult(`"stop_reason":"refusal",`), turnFacts{}, classRefused, codeInternal},
		{"nothing structured", errResult(``), turnFacts{}, classOther, codeInternal},
		// Text is never read for a class, except the old login wording when claude
		// sent no code at all.
		{"limit wording in text", strings.Replace(errResult(``), "SECRET-text", "You have hit your usage limit, resets 3pm", 1), turnFacts{}, classOther, codeInternal},
		{"context wording in text", strings.Replace(errResult(``), "SECRET-text", "Prompt is too long", 1), turnFacts{}, classOther, codeInternal},
		{"old login text", strings.Replace(errResult(``), "SECRET-text", "Not logged in - Please run /login", 1), turnFacts{}, classAuth, codeAuthRequired},
		{"login text with a code", strings.Replace(errResult(`"api_error_status":500,`), "SECRET-text", "please run /login", 1), turnFacts{apiError: "server_error"}, classOutage, codeInternal},
	}
	for _, tc := range tests {
		var l line
		if err := json.Unmarshal([]byte(tc.res), &l); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		stop, e := classify(l, tc.facts, false)
		if stop != "" || e == nil {
			t.Errorf("%s: stop %q err %v", tc.name, stop, e)
			continue
		}
		if e.Code != tc.code {
			t.Errorf("%s: code %d, want %d", tc.name, e.Code, tc.code)
		}
		if tc.code == codeInternal && dataClass(e) != tc.class {
			t.Errorf("%s: class %q, want %q", tc.name, dataClass(e), tc.class)
		}
		if strings.Contains(e.Message, "SECRET") || e.Message != classMessages[tc.class] {
			t.Errorf("%s: message %q", tc.name, e.Message)
		}
	}
}

func TestResetTimeOnlyFromTheLimitEvent(t *testing.T) {
	var l line
	_ = json.Unmarshal([]byte(errResult(`"api_error_status":429,`)), &l)
	_, e := classify(l, turnFacts{apiError: "rate_limit", rejected: true, resetsAt: 1793143200}, false)
	if m, _ := e.Data.(map[string]any); m["resetsAt"] != float64(1793143200) {
		t.Errorf("data = %v", e.Data)
	}
	_, e = classify(l, turnFacts{apiError: "rate_limit"}, false)
	if m, _ := e.Data.(map[string]any); m["resetsAt"] != nil {
		t.Errorf("a reset time without the event: %v", e.Data)
	}
	var l500 line
	_ = json.Unmarshal([]byte(errResult(`"api_error_status":500,`)), &l500)
	_, e = classify(l500, turnFacts{apiError: "server_error", rejected: true, resetsAt: 5}, false)
	if m, _ := e.Data.(map[string]any); m["resetsAt"] != nil {
		t.Errorf("a reset time on an outage: %v", e.Data)
	}
}

// The whole path: lines in the order claude writes them for a usage limit, and
// nothing carried over to the turns after it.
func TestUsageLimitOverTheWire(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`),
		emit(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1793143200,"rateLimitType":"five_hour"},"session_id":"SID"}`),
		emit(apiErrorAssistant("rate_limit")),
		emit(errResult(`"api_error_status":429,"terminal_reason":"api_error",`)),
		expect(`"second"`),
		emit(apiErrorAssistant("server_error")),
		emit(errResult(`"terminal_reason":"api_error",`)),
		expect(`"third"`),
		// an allowed rate limit event is not a limit
		emit(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1793143200,"rateLimitType":"five_hour"},"session_id":"SID"}`),
		emit(errResult(``)),
	})
	sid := h.start()
	r := h.await(h.promptText(sid, "first"))
	if r.Error == nil || r.Error.Message != "Usage limit reached" || dataClass(r.Error) != classUsageLimit {
		t.Fatalf("error = %+v", r.Error)
	}
	if m := r.Error.Data.(map[string]any); m["resetsAt"] != float64(1793143200) {
		t.Errorf("data = %v", m)
	}
	r = h.await(h.promptText(sid, "second"))
	if r.Error == nil || dataClass(r.Error) != classOutage || r.Error.Data.(map[string]any)["resetsAt"] != nil {
		t.Fatalf("second error = %+v", r.Error)
	}
	r = h.await(h.promptText(sid, "third"))
	if r.Error == nil || dataClass(r.Error) != classOther || r.Error.Data.(map[string]any)["resetsAt"] != nil {
		t.Fatalf("third error = %+v", r.Error)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.msgs {
		if m.Error != nil && strings.Contains(m.Error.Message, "SECRET") {
			t.Errorf("claude's text reached an error: %+v", m.Error)
		}
	}
}

// A sub-agent's API error is not the turn's.
func TestNestedErrorIsIgnored(t *testing.T) {
	nested := strings.Replace(apiErrorAssistant("rate_limit"), `"parent_tool_use_id":null`, `"parent_tool_use_id":"toolu_1"`, 1)
	h := newHarness(t, []step{expect(`"role":"user"`), emit(nested), emit(errResult(``))})
	sid := h.start()
	r := h.await(h.promptText(sid, "go"))
	if r.Error == nil || dataClass(r.Error) != classOther {
		t.Errorf("error = %+v", r.Error)
	}
}

func TestCompactAndClearAreShown(t *testing.T) {
	const idA, idB = "53d4fb62-c89c-4d48-bb84-475b525ae659", "7897cd7c-e846-419c-aad6-6f45e08894fc"
	initFor := func(id string) string { return strings.Replace(lInit, `"SID"`, `"`+id+`"`, 1) }
	reset := func(id string) string {
		return `{"type":"conversation_reset","new_conversation_id":"` + id + `","trigger":"clear","session_id":"SID"}`
	}
	clearResult := func(id string) string {
		return `{"type":"result","subtype":"success","is_error":false,"result":"","num_turns":0,"session_id":"` + id + `","usage":{"input_tokens":0,"output_tokens":0},"modelUsage":{}}`
	}
	h := newHarness(t, []step{expect(`"/compact"`),
		emit(`{"type":"system","subtype":"status","status":"compacting","session_id":"SID"}`),
		emit(`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"manual","pre_tokens":30874,"post_tokens":10612},"session_id":"SID"}`),
		emit(`{"type":"user","message":{"role":"user","content":"<local-command-stdout>SECRET Compacted </local-command-stdout>"},"isReplay":true}`),
		emit(`{"type":"result","subtype":"success","is_error":false,"result":"","num_turns":0,"session_id":"SID","usage":{"input_tokens":9000,"output_tokens":0},"modelUsage":{"m":{"contextWindow":1000000}}}`),
		expect(`"/clear"`),
		emit(reset(idA)), emit(initFor(idA)), emit(clearResult(idA)),
		expect(`"/clear2"`),
		emit(reset(idB)), emit(initFor(idB)), emit(clearResult(idB)),
		expect(`"viaB"`),
		emit(clearResult(idB)),
	})
	sid := h.start()
	if r := h.await(h.promptText(sid, "/compact")); r.Error != nil {
		t.Fatalf("compact: %+v", r.Error)
	}
	if r := h.await(h.promptText(sid, "/clear")); r.Error != nil {
		t.Fatalf("clear: %+v", r.Error)
	}
	if r := h.await(h.promptText(sid, "/clear2")); r.Error != nil {
		t.Fatalf("second clear: %+v", r.Error)
	}
	// Session ids after the second /clear: the one the client has and claude's
	// newest answer; the first new id was dropped when the second replaced it.
	if r := h.await(h.promptText(idA, "viaA")); r.Error == nil {
		t.Errorf("the earlier alias %s still reaches the session", idA)
	}
	if r := h.await(h.promptText(idB, "viaB")); r.Error != nil {
		t.Errorf("the newest id %s does not reach the session: %+v", idB, r.Error)
	}
	var compaction, notice map[string]any
	var usage []map[string]any
	for _, u := range h.updates() {
		switch u["sessionUpdate"] {
		case "compaction":
			compaction = u
		case "flockdeck_notice":
			if u["code"] == "new_conversation" {
				notice = u
			}
		case "usage_update":
			usage = append(usage, u)
		}
	}
	if compaction == nil || compaction["message"] != "Context compacted: 30,874 to 10,612 tokens." {
		t.Errorf("compaction = %v", compaction)
	}
	if notice == nil || len(notice) != 2 {
		t.Errorf("notice = %v", notice)
	}
	// The gauge follows: after /compact the size claude reports for the new
	// context, after /clear nothing.
	if len(usage) < 4 || usage[0]["used"] != float64(10612) || usage[1]["used"] != float64(0) || usage[2]["used"] != float64(0) {
		t.Errorf("usage updates = %v", usage)
	}
	for _, u := range h.updates() {
		if b, _ := json.Marshal(u); strings.Contains(string(b), "SECRET") {
			t.Errorf("claude's command output reached an update: %s", b)
		}
	}
}

func TestGroupDigits(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 30874: "30,874", 1234567: "1,234,567"} {
		if got := groupDigits(in); got != want {
			t.Errorf("groupDigits(%d) = %q, want %q", in, got, want)
		}
	}
}
