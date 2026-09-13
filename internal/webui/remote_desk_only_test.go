package webui

import "testing"

// TestARemoteWindowIsNotOfferedWhatOnlyTheDeskMayDo covers the Remote access
// dialog and the API keys on a phone. Turning remote access off from there
// cuts the way in the phone came by, with nothing left at the far end to turn
// it on again; turning it on, from a window already in, is against another
// relay; and a key pasted there passes through the relay, which can read it.
// The server refuses all of them from a window reached through the relay, so
// such a window is not offered them, and is told where they are done instead.
func TestARemoteWindowIsNotOfferedWhatOnlyTheDeskMayDo(t *testing.T) {
	runFrontEnd(t, `
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true });
h.recv(fixture({ remote: { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 1,
  since: "2030-01-01T00:00:00Z" } }));
h.click(h.$("btn-remote"));
h.recv({ type: "remoteDevices", enabled: true, current: "d1",
  devices: [{ id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" }],
  hosts: [{ id: "h1", name: "desk", online: true, self: true }] });
const body = h.$("overlay-body");
assert.ok(!h.$("remote-disable"), "a window reached through the relay is offered to turn remote access off, cutting its own way in");
assert.ok(body.textContent.includes("on the machine itself"),
  "a window reached through the relay is not told where remote access is turned off: " + body.textContent);
assert.ok(h.$("remote-pair"), "pairing another device went along with turning remote access off");
h.recv({ type: "remoteOutcome", action: "disable", untold: true, error: "could not reach the relay" });
assert.ok(!h.$("remote-forget") && !h.$("remote-disable-again"),
  "a window reached through the relay is offered to forget remote access anyway");

h.recv(fixture());
h.recv({ type: "remoteDevices", enabled: false, devices: [], hosts: [] });
assert.ok(!h.$("remote-enable") && !h.$("remote-relay"),
  "a window reached through the relay is offered the form that turns remote access on, against a relay of its choosing");
assert.ok(h.$("overlay-body").textContent.includes("on the machine itself"),
  "a window reached through the relay is not told where remote access is turned on: " + h.$("overlay-body").textContent);
h.key({ key: "Escape" });

h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-keys"));
h.recv({ type: "keys", items: [{ agent: "anthropic", name: "Anthropic API", set: true, source: "store", stored: true,
  vars: ["ANTHROPIC_API_KEY"] }] });
const pane = h.$("settings-pane");
assert.ok(!h.$("key-set-anthropic"), "a window reached through the relay is offered to paste a key, which would pass through the relay");
assert.ok(!Array.from(pane.querySelectorAll("button")).some((b) => b.textContent === "Clear"),
  "a window reached through the relay is offered to clear a key");
assert.ok(pane.textContent.includes("on the machine itself"),
  "a window reached through the relay is not told where keys are set: " + pane.textContent);
`)
}
