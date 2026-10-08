package webui

import "testing"

// A device the desk limited to viewing is told so, and is not offered what the
// server would refuse. One that may not watch panes opens no terminal socket,
// which would only be refused and retried.
func TestAViewerThatMayNotWatchPanesIsToldThereIsNothingToShow(t *testing.T) {
	runFrontEnd(t, `
h.recv({ type: "hello", keys: h.keyTable(), prefs: {}, remote: true, role: "viewer" });
h.recv(fixture());
const banner = h.$("view-only-banner");
assert.ok(banner && !banner.hidden, "no banner says this device can only view");
assert.ok(banner.textContent.includes("View only") && banner.textContent.includes("nothing to show"), banner.textContent);
assert.ok(h.doc.body.classList.contains("view-only"));
await h.sleep(300);
assert.strictEqual(h.sockets.filter((s) => s.url.includes("/ws/pty")).length, 0,
  "a viewer that may not watch panes opened a terminal socket");
`)
}

// A watcher sees the panes, and what it types goes nowhere. A hang-up for a
// changed role is not retried; an ordinary drop still is.
func TestAWatcherSeesPanesAndSendsNoKeystrokes(t *testing.T) {
	runFrontEnd(t, `
h.recv({ type: "hello", keys: h.keyTable(), prefs: {}, remote: true, role: "viewer", watchPanes: true });
h.recv(fixture());
const banner = h.$("view-only-banner");
assert.ok(banner && !banner.hidden && banner.textContent.includes("View only"), "no banner for a watcher");
const ws = h.sockets.find((s) => s.url.includes("/ws/pty?id=p1"));
assert.ok(ws, "a watcher was not given the terminal");
ws.onopen();
await h.sleep(50);
const before = ws.sent.length;
h.terms[0]._data("rm -rf /\r");
await h.sleep(100);
const typed = ws.sent.slice(before).filter((m) => typeof m !== "string" || !m.includes("focus"));
assert.strictEqual(typed.filter((m) => typeof m !== "string").length, 0, "a watcher's keystrokes were sent");

const count = h.sockets.length;
ws.onclose({ code: 1008, reason: "this device can only view" });
await h.sleep(500);
assert.strictEqual(h.sockets.length, count, "a terminal the server hung up on for a changed role was redialled");
assert.equal(h.terms[0].host.parentElement.querySelector("div.pane-error"), null, "a hang-up for a changed role was shown as an error over the terminal");

// Made full again, the banner goes and the terminal comes back.
h.recv({ type: "hello", keys: h.keyTable(), prefs: {}, remote: true });
assert.ok(h.$("view-only-banner").hidden, "the banner stayed after the device was made full");
assert.ok(!h.doc.body.classList.contains("view-only"));
`)
}

// A full device, and the desk's own window, see no banner and have no buttons
// taken away.
func TestAFullWindowHasNoViewOnlyBanner(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
assert.ok(!h.$("view-only-banner") || h.$("view-only-banner").hidden);
assert.ok(!h.doc.body.classList.contains("view-only"));
const ws = h.sockets.find((s) => s.url.includes("/ws/pty?id=p1"));
assert.ok(ws, "the terminal was not opened");
`)
}
