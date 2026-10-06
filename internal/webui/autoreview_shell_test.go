package webui

import "testing"

// A shell makes no PreToolUse calls, so auto-review has nothing to act on there
// and the server refuses the command. The toggle is hidden for a shell pane, as
// the record toggle is, and shown for an agent pane.
func TestAutoReviewToggleIsHiddenForAShellPane(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const toggle = () => [...h.doc.querySelector("div.pane-header").querySelector("div.pane-actions").children].find((b) => b.textContent === "✓");
h.recv(fixture({ panes: { p1: pane("p1") } }));
assert.ok(toggle(), "the pane has no auto-review toggle");
assert.ok(!toggle().hidden, "an agent pane hides the auto-review toggle");

h.recv(fixture({ panes: { p1: pane("p1", { kind: "shell" }) } }));
assert.ok(toggle().hidden, "a shell pane offers the auto-review toggle");

h.recv(fixture({ panes: { p1: pane("p1") } }));
assert.ok(!toggle().hidden, "the toggle stayed hidden after the pane became an agent pane");
`)
}
