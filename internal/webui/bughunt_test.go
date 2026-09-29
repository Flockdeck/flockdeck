package webui

import "testing"

// A shortcut being recorded takes every key the window sees. Closing Settings
// with the pointer while a row was still waiting for its key left that
// recording going with nothing on screen to say so, and the whole window
// swallowed what was typed.
func TestClosingSettingsEndsAKeyRecording(t *testing.T) {
	runFrontEnd(t, relayPromise+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-keybindings"));
h.click(h.$("set-keybind-closePane"));
h.click(h.$("overlay-close"));
assert.ok(h.$("overlay").hidden, "the dialog did not close");
const before = h.commands().length;
const ev = h.key({ key: "x" });
assert.ok(!ev.defaultPrevented, "a key typed after the dialog closed was still being taken by the recording");
h.key({ key: "w", ctrlKey: true, altKey: true });
assert.strictEqual(h.commands().slice(before).filter((c) => c.cmd === "setKeybinding").length, 0,
  "a key typed after the dialog closed was saved as a shortcut");
`)
}
