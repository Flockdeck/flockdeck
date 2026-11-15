package claudeacp

import (
	"encoding/json"
	"strings"
	"testing"
)

func planOptions(t *testing.T, params json.RawMessage) (ids []string) {
	t.Helper()
	var p struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &p)
	for _, o := range p.Options {
		ids = append(ids, o.OptionID+"/"+o.Kind)
	}
	return ids
}

func runPlan(t *testing.T, answer string) (log []string, h *harness) {
	t.Helper()
	h = newHarness(t, []step{expect(`"role":"user"`), emit(exitPlanRequest(`{"plan":"1. Add the card"}`)),
		expect(`"request_id":"plan-1"`), emit(lResultOK)})
	h.onReq = func(rpcMsg) any { return outcome(answer) }
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	return readLog(h.log), h
}

func TestPlanCardOffersApproveAcceptEditsAndReject(t *testing.T) {
	_, h := runPlan(t, "reject")
	reqs := h.requests("session/request_permission")
	if len(reqs) != 1 {
		t.Fatalf("%d permission requests", len(reqs))
	}
	got := strings.Join(planOptions(t, reqs[0].Params), " ")
	if want := "allow/allow_once allow_accept_edits/allow_once reject/reject_once"; got != want {
		t.Errorf("options = %q, want %q", got, want)
	}
}

func TestPlanApprovedWithManualApprovalKeepsTheMode(t *testing.T) {
	log, h := runPlan(t, "allow")
	if !logHas(log, `"behavior":"allow"`) || logHas(log, "updatedPermissions") {
		t.Errorf("log = %v", log)
	}
	for _, u := range h.updates() {
		if u["sessionUpdate"] == "current_mode_update" {
			t.Errorf("mode update after a plain allow: %v", u)
		}
	}
}

func TestPlanApprovedWithAcceptEditsSetsTheMode(t *testing.T) {
	log, h := runPlan(t, "allow_accept_edits")
	if !logHas(log, `"behavior":"allow"`) || !logHas(log, `"updatedPermissions":[{"destination":"session","mode":"acceptEdits","type":"setMode"}]`) {
		t.Errorf("log = %v", log)
	}
	got := false
	for _, u := range h.updates() {
		got = got || (u["sessionUpdate"] == "current_mode_update" && u["currentModeId"] == "acceptEdits")
	}
	if !got {
		t.Errorf("updates = %v", kinds(h.updates()))
	}
}

func TestPlanRejectedDeniesWithKeepPlanningText(t *testing.T) {
	log, _ := runPlan(t, "reject")
	if logHas(log, `"behavior":"allow"`) || !logHas(log, `"behavior":"deny"`) || !logHas(log, "Stay in plan mode") {
		t.Errorf("log = %v", log)
	}
}

func TestPlanCardCancelledDeniesWithoutAMode(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`), emit(exitPlanRequest(`{"plan":"1. Add the card"}`)),
		expect(`"request_id":"plan-1"`), emit(lResultOK)})
	h.onReq = func(rpcMsg) any { return cancelledOutcome() }
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	log := readLog(h.log)
	if logHas(log, `"behavior":"allow"`) || logHas(log, "updatedPermissions") || !logHas(log, "The permission request was cancelled.") || logHas(log, "Stay in plan mode") {
		t.Errorf("log = %v", log)
	}
	for _, u := range h.updates() {
		if u["sessionUpdate"] == "current_mode_update" {
			t.Errorf("mode update after a cancelled plan card: %v", u)
		}
	}
}

// If claude does not keep acceptEdits, what it reports at the next turn
// replaces the mode shown after the answer.
func TestLaterInitReportReplacesAcceptEdits(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"role":"user"`), emit(exitPlanRequest(`{"plan":"1. Add the card"}`)), expect(`"request_id":"plan-1"`), emit(lResultOK),
		expect(`"role":"user"`), emit(lInit), emit(lResultOK),
	})
	h.onReq = func(rpcMsg) any { return outcome("allow_accept_edits") }
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	stopReason(t, h.await(h.promptText(sid, "again")))
	var modes []string
	for _, u := range h.updates() {
		if u["sessionUpdate"] == "current_mode_update" {
			modes = append(modes, u["currentModeId"].(string))
		}
	}
	if got := strings.Join(modes, ","); got != "acceptEdits,default" {
		t.Errorf("mode updates = %q, want acceptEdits,default", got)
	}
}

// A client that sends the plan option for another tool gets a denial: the
// option was not offered there.
func TestAcceptEditsOptionIsNotHonouredOnOtherCards(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`), emit(lToolUseWrite), emit(lPermWrite),
		expect(`"request_id":"perm-1"`), emit(lResultOK)})
	h.onReq = func(rpcMsg) any { return outcome("allow_accept_edits") }
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	log := readLog(h.log)
	if logHas(log, `"behavior":"allow"`) || logHas(log, "updatedPermissions") || !logHas(log, `"behavior":"deny"`) {
		t.Errorf("log = %v", log)
	}
}
