package webui

import "testing"

// TestThePromptGoesToThePaneTheBarWasOpenedOn covers the prompt bar while
// focus moves under it: the desk clicking another pane, a fan-out revealing
// its first agent. The bar sent no pane, so the prompt went to whichever had
// focus when it arrived; it now names the pane it was opened on.
func TestThePromptGoesToThePaneTheBarWasOpenedOn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const tab = (focus) => ({ id: "t1", title: "one", focus, zoom: false, attention: false,
  root: { id: "s1", dir: "h", weight: 1, children: [
    { id: "n1", pane: "p1", weight: 1 }, { id: "n2", pane: "p2", weight: 1 }] } });
h.recv(fixture({ tabs: [tab("p1")] }));
h.press("promptAll");
const input = h.$("prompt-input");
input.value = "run the tests";
// Focus moves to the other pane while the bar is open.
h.recv(fixture({ tabs: [tab("p2")] }));
input.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "sendPrompt", id: "p1", text: "run the tests" },
  "the prompt was not sent for the pane the bar was opened on");
`)
}
