package claudeacp

import "strings"

// rulesIgnoredForTrust is true for claude's stderr line about project
// permission rules dropped in a folder it has not trusted, e.g. "Ignoring 1
// permissions.allow entry from .claude/settings.json: this workspace has not
// been trusted. Run Claude Code interactively here once ...". claude shows its
// trust dialog only in a terminal, never under -p.
func rulesIgnoredForTrust(line string) bool {
	return strings.HasPrefix(line, "Ignoring ") && strings.Contains(line, "permissions.allow") &&
		strings.Contains(line, "has not been trusted")
}

// noticeRulesIgnored tells the client once, when claude has said it and the
// client knows the session.
func (ss *session) noticeRulesIgnored() {
	if ss.rulesIgnored.Load() && ss.opened.Load() && ss.rulesNoticed.CompareAndSwap(false, true) {
		ss.srv.update(ss.id, map[string]any{"sessionUpdate": "flockdeck_notice", "code": "project_rules_ignored"})
	}
}
