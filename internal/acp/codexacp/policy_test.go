package codexacp

import (
	"strings"
	"testing"
)

func TestSessionFailsWhenCodexReportsALooserPolicy(t *testing.T) {
	for name, echo := range map[string]string{
		"no approvals":       `{"approvalPolicy":"never"}`,
		"unknown approvals":  `{"approvalPolicy":{"granular":{}}}`,
		"no sandbox":         `{"sandbox":{"type":"dangerFullAccess"}}`,
		"external sandbox":   `{"sandbox":"danger-full-access"}`,
		"unreadable sandbox": `{"sandbox":42}`,
		"network":            `{"sandbox":{"type":"workspaceWrite","networkAccess":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil, hopts{extraEnv: []string{envEcho + "=" + echo}})
			h.call("initialize", map[string]any{"protocolVersion": 1})
			r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{}})
			if r.Error == nil {
				t.Fatal("the session opened with a looser policy than the mode")
			}
			if strings.Contains(string(r.Error.Message), "never") {
				t.Errorf("error repeats codex's answer: %s", r.Error.Message)
			}
		})
	}
}

func TestSessionOpensWhenCodexReportsTheSamePolicyOrStricter(t *testing.T) {
	for name, echo := range map[string]string{
		"same":     `{"approvalPolicy":"on-request","sandbox":{"type":"workspaceWrite","networkAccess":false}}`,
		"stricter": `{"approvalPolicy":"untrusted","sandbox":"read-only"}`,
		"absent":   `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil, hopts{extraEnv: []string{envEcho + "=" + echo}})
			if h.start() == "" {
				t.Fatal("no session")
			}
		})
	}
}

func TestReviewAfterAModeChangeNeedsATurnFirst(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(), endTurn("completed"),
		expect(`"method":"review/start"`), startedTurn(), endTurn("completed"),
	})
	sid := h.start()
	if r := h.call("session/set_mode", map[string]any{"sessionId": sid, "modeId": "read-only"}); r.Error != nil {
		t.Fatal(r.Error)
	}
	if r := h.await(h.promptText(sid, "/review")); r.Error == nil {
		t.Fatal("/review ran under the previous mode's policy")
	}
	if logFind(readLog(h.log), `"method":"review/start"`) != "" {
		t.Error("review/start was sent")
	}
	if got := stopReason(t, h.await(h.promptText(sid, "hello"))); got != "end_turn" {
		t.Fatalf("stop = %s", got)
	}
	if got := stopReason(t, h.await(h.promptText(sid, "/review"))); got != "end_turn" {
		t.Fatalf("stop = %s", got)
	}
}

func TestPolicyExceeds(t *testing.T) {
	m := modes[0]
	if why := policyExceeds(m, []byte(`"on-request"`), []byte(`"workspace-write"`)); why != "" {
		t.Errorf("equal policy rejected: %s", why)
	}
	if why := policyExceeds(m, []byte(`"never"`), nil); why == "" {
		t.Error("never accepted")
	}
}
