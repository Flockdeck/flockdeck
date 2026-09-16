package webui

import "testing"

// The remote access dialog is where a person compares this desktop's and a
// paired device's out-of-band fingerprint (internal/e2e.Fingerprint,
// internal/server/remote.go's remoteFingerprint): the check that catches a
// relay which swapped a registered key, which internal/e2e's own doc says
// the handshake's own mutual authentication cannot catch by itself.

// A device with a fingerprint gets a Verify button; pressing it shows the
// code, in place, without asking the relay for anything more -- the whole
// point being that it can be checked from what the dialog already has.
// Pressing it again hides the code, and a device with no fingerprint (an
// older browser, or one that has not registered a key) gets no button at
// all, since there is nothing yet to compare.
func TestAPairedDevicesFingerprintIsShownOnRequest(t *testing.T) {
	runFrontEnd(t, `
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] } });
h.recv(fixture({ remote: { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z" } }));
h.click(h.$("btn-remote"));
h.recv({ type: "remoteDevices", enabled: true, current: "d1",
  devices: [
    { id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z",
      fingerprint: "99506 91610 13506 43956 60391 19129" },
    { id: "d2", name: "old browser", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" },
  ],
  hosts: [{ id: "h1", name: "desk", online: true, self: true }] });
const body = h.$("overlay-body");
const findButton = (text) => Array.from(body.querySelectorAll("button")).find((b) => b.textContent === text);

assert.ok(findButton("Verify"), "a device with a fingerprint got no way to verify it");
assert.ok(!body.textContent.includes("99506 91610"), "the code is shown before anyone asked to see it");
assert.ok(!Array.from(body.querySelectorAll("button")).some((b) => b.textContent === "Hide code"),
  "the code started open");

h.click(findButton("Verify"));
assert.ok(body.textContent.includes("99506 91610 13506 43956 60391 19129"),
  "the code did not appear once Verify was pressed: " + body.textContent);
assert.ok(findButton("Hide code"), "the button did not say it would hide the code once pressed");

h.click(findButton("Hide code"));
assert.ok(!body.textContent.includes("99506 91610"), "the code stayed shown after Hide code was pressed");

// "old browser" registered no end-to-end key, so there is nothing to
// compare it against and no button offering to.
const rows = Array.from(body.querySelectorAll(".wt-row")).filter((r) => r.textContent.includes("old browser"));
assert.equal(rows.length, 1, "the row for the device with no key went missing");
assert.ok(!Array.from(rows[0].querySelectorAll("button")).some((b) => b.textContent === "Verify"),
  "a device with no registered key was still offered a code to verify");
`)
}

// A code shown open survives the dialog being redrawn for an unrelated
// reason -- another device's roster refreshing, say -- the same way an open
// rename or cascade row on the Devices page does.
func TestAnOpenFingerprintSurvivesTheDialogRedrawing(t *testing.T) {
	runFrontEnd(t, `
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] } });
h.recv(fixture({ remote: { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z" } }));
h.click(h.$("btn-remote"));
const devices = [{ id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z",
  fingerprint: "99506 91610 13506 43956 60391 19129" }];
h.recv({ type: "remoteDevices", enabled: true, current: "d1", devices, hosts: [{ id: "h1", name: "desk", self: true }] });
const body = h.$("overlay-body");
const findButton = (text) => Array.from(body.querySelectorAll("button")).find((b) => b.textContent === text);
h.click(findButton("Verify"));
assert.ok(body.textContent.includes("99506 91610"), "the code never appeared");

// The roster arrives again, as it does on a timer while the dialog is open.
h.recv({ type: "remoteDevices", enabled: true, current: "d1", devices, hosts: [{ id: "h1", name: "desk", self: true }] });
assert.ok(h.$("overlay-body").textContent.includes("99506 91610"),
  "the code closed itself when the roster was refreshed for an unrelated reason");
`)
}
