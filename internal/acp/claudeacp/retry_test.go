package claudeacp

import (
	"encoding/json"
	"strings"
	"testing"
)

// claude's system/api_retry lines become flockdeck_notice updates that carry the
// attempt numbers and a class from the closed set, and nothing claude wrote.
func TestAPIRetryBecomesANoticeWithACodeAndClass(t *testing.T) {
	retry := func(extra string) string {
		return `{"type":"system","subtype":"api_retry",` + extra + `,"uuid":"u","session_id":"SID"}`
	}
	h := newHarness(t, []step{expect(`"go"`),
		emit(retry(`"attempt":1,"max_retries":10,"retry_delay_ms":500,"error_status":529,"error":"overloaded"`)),
		emit(retry(`"attempt":2,"max_retries":10,"retry_delay_ms":1000,"error_status":429,"error":"rate_limit"`)),
		emit(retry(`"attempt":3,"max_retries":10,"retry_delay_ms":2000,"error_status":null,"error":"unknown"`)),
		emit(retry(`"attempt":4,"max_retries":10,"retry_delay_ms":2000,"error_status":503,"error":"SECRET error text"`)),
		emit(retry(`"attempt":11,"max_retries":10,"error_status":529,"error":"overloaded"`)),    // past its own limit
		emit(retry(`"attempt":"two","max_retries":10,"error_status":529,"error":"overloaded"`)), // not a number
		emit(`{"type":"result","subtype":"success","is_error":false,"result":"","num_turns":1,"session_id":"SID","usage":{"input_tokens":1,"output_tokens":1},"modelUsage":{}}`),
	})
	sid := h.start()
	if r := h.await(h.promptText(sid, "go")); r.Error != nil {
		t.Fatalf("prompt: %+v", r.Error)
	}
	var got []string
	for _, u := range h.updates() {
		if u["sessionUpdate"] != "flockdeck_notice" || u["code"] != "api_retry" {
			continue
		}
		b, _ := json.Marshal(u)
		got = append(got, string(b))
	}
	want := []string{
		`{"attempt":1,"class":"overloaded","code":"api_retry","max":10,"sessionUpdate":"flockdeck_notice"}`,
		`{"attempt":2,"class":"rate_limit","code":"api_retry","max":10,"sessionUpdate":"flockdeck_notice"}`,
		`{"attempt":3,"class":"network","code":"api_retry","max":10,"sessionUpdate":"flockdeck_notice"}`,
		`{"attempt":4,"class":"other","code":"api_retry","max":10,"sessionUpdate":"flockdeck_notice"}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("notices:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, g := range got {
		if strings.Contains(g, "SECRET") {
			t.Errorf("claude's error text reached an update: %s", g)
		}
	}
}

func TestRetryClass(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	for _, c := range []struct {
		code, status, noResp, want string
	}{
		{"overloaded", "529", "", retryOverloaded},
		{"rate_limit", "429", "", retryRateLimit},
		{"server_error", "500", "", retryServer},
		{"unknown", "null", "", retryNetwork},
		{"unknown", "", "", retryNetwork},
		{"server_error", "null", `{"waited_ms":1}`, retryServer},
		{"authentication_failed", "401", "", retryOther},
		{"unknown", "404", "", retryOther},
		{"whatever", "null", `{"waited_ms":1}`, retryNetwork},
	} {
		if got := retryClass(c.code, raw(c.status), raw(c.noResp)); got != c.want {
			t.Errorf("retryClass(%q, %s, %s) = %s, want %s", c.code, c.status, c.noResp, got, c.want)
		}
	}
}

func TestAPIRetryWithOddNumbersSendsNothing(t *testing.T) {
	retry := func(a, m string) string {
		return `{"type":"system","subtype":"api_retry","attempt":` + a + `,"max_retries":` + m + `,"error_status":529,"error":"overloaded","session_id":"SID"}`
	}
	h := newHarness(t, []step{expect(`"go"`),
		emit(retry("99999999999999999999", "10")),
		emit(retry("-1", "10")),
		emit(retry("1.5", "10")),
		emit(retry("1", "1e3")),
		emit(retry("1", "1000")),
		emit(retry("0", "10")),
		emit(retry("null", "10")),
		emit(retry("[1]", "{}")),
		emit(`{"type":"result","subtype":"success","is_error":false,"result":"","num_turns":1,"session_id":"SID","usage":{"input_tokens":1,"output_tokens":1},"modelUsage":{}}`),
	})
	sid := h.start()
	if r := h.await(h.promptText(sid, "go")); r.Error != nil {
		t.Fatalf("prompt: %+v", r.Error)
	}
	for _, u := range h.updates() {
		if u["code"] == "api_retry" {
			t.Errorf("an odd retry line made an update: %v", u)
		}
	}
}
