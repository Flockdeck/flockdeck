package webui

import "testing"

// TestARemoteWindowIsNotOfferedTheRestart covers the update badge on a phone.
// Snapshots go to every window alike, so a staged release put "Update" in a
// window reached through the relay as well, and its Restart now stopped every
// agent at the desk -- which the server now refuses from there, as it does
// Quit. The hello says the window came through the relay, and such a window
// is not offered the restart at all: not by the badge, not in the settings.
func TestARemoteWindowIsNotOfferedTheRestart(t *testing.T) {
	runFrontEnd(t, `
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true });
h.recv(fixture({ update: { version: "9.9.9", notes: "Faster." } }));
assert.ok(h.$("btn-update").hidden, "a window reached through the relay is offered the update, whose restart stops every agent at the desk");
h.click(h.$("btn-settings"));
assert.ok(!h.$("set-install"), "the settings offer a window reached through the relay the update's restart");
assert.ok(!h.$("set-check-update"), "the settings offer a window reached through the relay a check the desk should run");
h.key({ key: "Escape" });

// The desk is offered it as ever.
h.hello();
h.recv(fixture({ update: { version: "9.9.9", notes: "Faster." } }));
assert.ok(!h.$("btn-update").hidden, "the window on the desk is not offered the update");
`)
}
