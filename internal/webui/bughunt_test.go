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

// A release note with a heading line that carries U+2028 (a line separator
// JavaScript's "." and "$" treat as a line end but split("\n") does not) is
// neither a heading nor a paragraph as renderNotes read it, and the loop never
// moved past it: opening the update dialog hung the window.
func TestReleaseNotesWithALineSeparatorDoNotHangTheUpdateDialog(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ update: { version: "9.9.9", notes: "# Title\u2028more\n\n- item" } }));
h.click(h.$("btn-update"));
assert.ok(h.$("overlay-body").textContent.includes("Title"), "the note was not drawn");
assert.ok(h.$("overlay-body").textContent.includes("item"), "the list after it was not drawn");
`)
}

// A terminal socket that had held for a good while and then dropped reset the
// retry backoff, as it should; but connectedAt stayed on that old open, so
// every failed retry after it, which never opened, looked like it followed a
// good connection too and reset the backoff again: an outage was retried
// every 250ms for as long as it lasted, instead of backing off.
func TestAPaneKeepsBackingOffWhileItsSocketCannotConnect(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const ptySockets = () => h.sockets.filter((s) => s.url.includes("/ws/pty?id=p1"));
ptySockets()[0].onopen();
await h.sleep(1100); // long enough to count as a connection that held
ptySockets()[0].close();
await h.sleep(350);
assert.strictEqual(ptySockets().length, 2, "the first retry did not come after 250ms");
ptySockets()[1].close(); // refused without ever opening
await h.sleep(350);
assert.strictEqual(ptySockets().length, 2, "a second failure retried after 250ms again instead of backing off");
await h.sleep(300);
assert.strictEqual(ptySockets().length, 3, "the backed-off retry never came");
`)
}
