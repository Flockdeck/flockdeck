package codexacp

import (
	"encoding/json"
	"testing"
)

func TestSessionFailsWhenCodexReportsAPolicyThatIsLooserOrMissing(t *testing.T) {
	for name, echo := range map[string]string{
		"no approvals":           `{"approvalPolicy":"never"}`,
		"unknown approvals":      `{"approvalPolicy":{"granular":{}}}`,
		"null approvals":         `{"approvalPolicy":null}`,
		"no sandbox":             `{"sandbox":{"type":"dangerFullAccess"}}`,
		"full access string":     `{"sandbox":"danger-full-access"}`,
		"unreadable sandbox":     `{"sandbox":42}`,
		"null sandbox":           `{"sandbox":null}`,
		"network":                `{"sandbox":{"type":"workspaceWrite","networkAccess":true}}`,
		"network as a string":    `{"sandbox":{"type":"workspaceWrite","networkAccess":"false"}}`,
		"network not reported":   `{"sandbox":{"type":"workspaceWrite"}}`,
		"network as null":        `{"sandbox":{"type":"workspaceWrite","networkAccess":null}}`,
		"read-only with network": `{"sandbox":{"type":"readOnly","networkAccess":true}}`,
	} {
		for _, method := range []string{"session/new", "session/resume"} {
			t.Run(name+" "+method, func(t *testing.T) {
				h := newHarness(t, nil, hopts{extraEnv: []string{envEcho + "=" + echo}})
				h.call("initialize", map[string]any{"protocolVersion": 1})
				params := map[string]any{"cwd": h.cwd, "mcpServers": []any{}}
				if method == "session/resume" {
					params["sessionId"] = "thr-1"
				}
				r := h.call(method, params)
				if r.Error == nil {
					t.Fatal("the session opened with a policy that is looser than the mode or missing")
				}
				if r.Error.Message != unavailable {
					t.Errorf("message = %q", r.Error.Message)
				}
			})
		}
	}
}

func TestSessionOpensWhenCodexReportsTheSamePolicyOrStricter(t *testing.T) {
	for name, echo := range map[string]string{
		"same":             `{"approvalPolicy":"on-request","sandbox":{"type":"workspaceWrite","networkAccess":false}}`,
		"stricter":         `{"approvalPolicy":"untrusted","sandbox":"read-only"}`,
		"read-only object": `{"sandbox":{"type":"readOnly"}}`,
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
	raw := func(s string) json.RawMessage {
		if s == "" {
			return nil
		}
		return json.RawMessage(s)
	}
	auto, readOnly, ask := modes[0], modes[1], modes[2]
	ww := `{"type":"workspaceWrite","networkAccess":false}`
	ro := `{"type":"readOnly"}`
	for _, c := range []struct {
		name      string
		m         modeDef
		app, sbox string
		bad       bool
	}{
		{"auto same", auto, `"on-request"`, ww, false},
		{"auto stricter approvals", auto, `"untrusted"`, ww, false},
		{"auto on-failure is stricter than on-request", auto, `"on-failure"`, ww, false},
		{"auto read-only sandbox", auto, `"on-request"`, ro, false},
		{"auto never", auto, `"never"`, ww, true},
		{"auto no approvals", auto, ``, ww, true},
		{"auto no sandbox", auto, `"on-request"`, ``, true},
		{"auto full access", auto, `"on-request"`, `"danger-full-access"`, true},
		{"auto network", auto, `"on-request"`, `{"type":"workspaceWrite","networkAccess":true}`, true},
		{"read-only same", readOnly, `"on-request"`, ro, false},
		{"read-only string", readOnly, `"on-request"`, `"read-only"`, false},
		{"read-only reported as workspace-write", readOnly, `"on-request"`, ww, true},
		{"read-only reported as string workspace-write", readOnly, `"on-request"`, `"workspace-write"`, true},
		{"ask same", ask, `"untrusted"`, ww, false},
		{"ask reported as on-failure", ask, `"on-failure"`, ww, true},
		{"ask reported as on-request", ask, `"on-request"`, ww, true},
		{"ask reported as never", ask, `"never"`, ww, true},
	} {
		why := policyExceeds(c.m, raw(c.app), raw(c.sbox))
		if (why != "") != c.bad {
			t.Errorf("%s: reason %q, want bad=%v", c.name, why, c.bad)
		}
	}
}
