package webui

import "testing"

// Verifying a device (internal/remote/verified.go): the dialog shows a code
// for each of a device's two keys and, on the machine itself, a button to say
// a person compared it. The window reached through the relay shows the codes
// and its own, and has no such button.

const verifyDevicesFixture = `
const devices = [{ id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z",
  fingerprint: "11111 22222 33333 44444 55555 66666", verify: "",
  deskFingerprint: "77777 88888 99999 00000 12345 67890", deskVerify: "" }];
const roster = (devs) => ({ type: "remoteDevices", enabled: true, current: "d9", devices: devs,
  hosts: [{ id: "h1", name: "desk", online: true, self: true }] });
const findButton = (text) => Array.from(h.$("overlay-body").querySelectorAll("button")).find((b) => b.textContent === text);
const connected = { remote: { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0, since: "2030-01-01T00:00:00Z" } };
`

func TestTheDeskShowsBothCodesAndMarksOneVerified(t *testing.T) {
	runFrontEnd(t, verifyDevicesFixture+`
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] } });
h.recv(fixture(connected));
h.click(h.$("btn-remote"));
h.recv(roster(devices));
const body = h.$("overlay-body");
assert.ok(!body.textContent.includes("77777 88888"), "a code is shown before Verify is pressed");
h.click(findButton("Verify"));
assert.ok(body.textContent.includes("11111 22222 33333 44444 55555 66666"), "the app's code is missing");
assert.ok(body.textContent.includes("77777 88888 99999 00000 12345 67890"), "the desktop page's code is missing");
const marks = Array.from(body.querySelectorAll("button")).filter((b) => b.textContent === "Mark verified");
assert.strictEqual(marks.length, 2, "each key gets its own button");

// Declined: nothing is sent.
const before = h.commands().length;
h.win._confirm = false;
h.click(marks[1]);
assert.strictEqual(h.commands().length, before, "a device was marked verified without the question being answered");

// Accepted: the command carries the device, the origin and the code on screen.
h.win._confirm = true;
h.click(marks[1]);
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "remoteVerify", id: "d1", kind: "desk", text: "77777 88888 99999 00000 12345 67890" });
h.click(marks[0]);
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "remoteVerify", id: "d1", kind: "usual", text: "11111 22222 33333 44444 55555 66666" });
assert.ok(/not exactly|exactly what/.test(h.win._confirmed), "the question does not say what to compare: " + h.win._confirmed);

// After the desk answers, the state shows and the button flips.
devices[0].deskVerify = "verified";
h.recv(roster(devices));
assert.ok(body.textContent.includes("Verified on the desktop"), "the verified state is not shown");
h.click(findButton("Remove verification"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteUnverify", id: "d1", kind: "desk" });

// A key that moved says so, and offers to verify again.
devices[0].deskVerify = "changed";
h.recv(roster(devices));
assert.ok(body.textContent.includes("The key changed since it was verified"), "a changed key is not called out");
assert.strictEqual(Array.from(body.querySelectorAll("button")).filter((b) => b.textContent === "Mark verified").length, 2);
`)
}

func TestARemoteWindowCannotMarkAnythingVerified(t *testing.T) {
	runFrontEnd(t, verifyDevicesFixture+`
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true });
h.recv(fixture(connected));
h.click(h.$("btn-remote"));
devices[0].deskVerify = "verified";
h.recv(roster(devices));
h.click(findButton("Verify"));
const body = h.$("overlay-body");
assert.ok(body.textContent.includes("77777 88888 99999 00000 12345 67890"), "the codes are not shown");
const names = Array.from(body.querySelectorAll("button")).map((b) => b.textContent);
assert.ok(!names.includes("Mark verified") && !names.includes("Remove verification"),
  "a window through the relay was offered a way to verify: " + names);
`)
}

func TestARemoteWindowShowsItsOwnCode(t *testing.T) {
	runFrontEnd(t, verifyDevicesFixture+`
const E2E = h.win.FlockdeckE2E;
const hostPair = await h.win.crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
const hostRaw = new Uint8Array(await h.win.crypto.subtle.exportKey("raw", hostPair.publicKey));
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true,
  e2ePublicKey: E2E.encodePublicKey(hostRaw) });
h.recv(fixture(connected));
const identity = await E2E.getIdentity();
const want = await E2E.fingerprint(identity.publicKeyRaw, hostRaw);
h.click(h.$("btn-remote"));
h.recv(roster([{ ...devices[0], id: "d9", deskVerify: "" }]));
await h.waitFor(() => h.$("overlay-body").textContent.includes(want));
const body = h.$("overlay-body");
assert.ok(body.textContent.includes("This window's code"), "the window's own code is not labelled");
assert.ok(body.textContent.includes("Not verified"), "the window does not say it is unverified");

h.recv(roster([{ ...devices[0], id: "d9", deskVerify: "verified" }]));
assert.ok(h.$("overlay-body").textContent.includes("Verified on the desktop"), "the window does not say it is verified");
`)
}

func TestALocalWindowShowsNoCodeOfItsOwn(t *testing.T) {
	runFrontEnd(t, verifyDevicesFixture+`
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] } });
h.recv(fixture(connected));
h.click(h.$("btn-remote"));
h.recv(roster(devices));
assert.ok(!h.$("overlay-body").textContent.includes("This window's code"), "a window on the desktop showed a code of its own");
`)
}
