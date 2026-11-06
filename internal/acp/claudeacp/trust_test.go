package claudeacp

import (
	"testing"
	"time"
)

const trustLine = `Ignoring 1 permissions.allow entry from .claude/settings.json: this workspace has not been trusted. Run Claude Code interactively here once and accept the trust dialog, or set projects["C:/x"].hasTrustDialogAccepted: true in C:\Users\u\.claude.json.`

func TestRulesIgnoredLineIsRecognised(t *testing.T) {
	for line, want := range map[string]bool{
		trustLine: true,
		"Ignoring 2 permissions.allow entries from .claude/settings.json and .claude/settings.local.json: this workspace has not been trusted. Run": true,
		"Ignoring 1 permissions.allow entry from .claude/settings.json: a session with no working directory is never trusted.":                      false,
		"Ignoring 1 hooks entry from .claude/settings.json: this workspace has not been trusted.":                                                   false,
		"something else": false,
	} {
		if got := rulesIgnoredForTrust(line); got != want {
			t.Errorf("%q = %v", line, got)
		}
	}
}

func countNotices(h *harness, code string) int {
	n := 0
	for _, u := range h.updates() {
		if u["sessionUpdate"] == "flockdeck_notice" && u["code"] == code {
			n++
		}
	}
	return n
}

// claude's stderr line about dropped allow rules becomes one notice in the pane.
func TestIgnoredAllowRulesGiveOneNotice(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`),
		{Stderr: trustLine}, {Stderr: trustLine}, {Stderr: "an unrelated line"},
		emit(lResultOK)})
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "hi")))
	deadline := time.Now().Add(3 * time.Second)
	for countNotices(h, "project_rules_ignored") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := countNotices(h, "project_rules_ignored"); n != 1 {
		t.Errorf("%d notices, want 1: %v", n, kinds(h.updates()))
	}
}

func TestNoNoticeWithoutTheLine(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`), {Stderr: "an unrelated line"}, emit(lResultOK)})
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "hi")))
	time.Sleep(100 * time.Millisecond)
	if n := countNotices(h, "project_rules_ignored"); n != 0 {
		t.Errorf("%d notices", n)
	}
}
