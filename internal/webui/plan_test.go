package webui

import "testing"

// The account's plan is shown as the relay reports it: in the remote access
// dialog, and in the settings' Account & plan. A relay that has stopped
// remote access for the account is said to have, in its own words. Nothing
// is hidden or turned off for it: the app decides nothing by a plan.
func TestTheRelaysPlanIsShown(t *testing.T) {
	runFrontEnd(t, relayPromise+`
h.hello();
const remote = (over) => Object.assign({ state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z" }, over || {});
h.recv(fixture({ remote: remote() }));
h.click(h.$("btn-remote"));
const days = (n) => new Date(Date.now() + n * 86400000).toISOString();
const hosts = [{ id: "h1", name: "desk", online: true, self: true }];
h.recv({ type: "remoteDevices", enabled: true, devices: [], hosts,
  plan: { plan: "trial", name: "Free trial", active: true, ends: days(11.5) } });
const plan = () => h.$("remote-plan");
assert.ok(plan(), "the dialog does not show the account's plan");
assert.match(plan().textContent, /Free trial: 12 days left, until /);
assert.match(plan().textContent, /The desktop app is free, and works the same whatever the plan\./);
assert.ok(!relayPromise(h.$("overlay-body").textContent), "the plan promises what the relay will cost: " + relayPromise(h.$("overlay-body").textContent));

// Once the trial is over the relay refuses the tunnel and says why. The
// dialog says it in those words, amber, with a way to try again once the
// account is paid for, and everything else still there.
const said = "Your remote access trial has ended. Subscribe from Devices on a paired phone or browser.";
h.recv(fixture({ remote: remote({ state: "lapsed", detail: said }) }));
assert.ok(h.$("btn-remote").classList.contains("trouble"), "a relay that stopped remote access is not amber");
assert.ok(h.$("overlay-body").textContent.includes(said), "the relay's words are not shown: " + h.$("overlay-body").textContent);
assert.ok(h.$("remote-retry"), "there is no way to try again once the account is paid for");
h.recv({ type: "remoteDevices", enabled: true, devices: [], hosts,
  plan: { plan: "lapsed", name: "Lapsed", active: false, was: "trial", ends: days(-1), deleteAt: days(89), message: said } });
assert.match(plan().textContent, /Your remote access trial has ended\..* kept until .*, then deleted\./);
assert.ok(h.$("remote-pair") && !h.$("remote-pair").disabled, "pairing a device was taken away for the plan");
assert.ok(h.$("remote-disable"), "turning remote access off was taken away for the plan");

// Account & plan shows the same, asking the relay for it as it opens.
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-plan"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteDevices" });
h.recv({ type: "remoteDevices", enabled: true, devices: [], hosts,
  plan: { plan: "subscribed", name: "Subscription", active: true, ends: "2031-01-01T00:00:00Z" } });
const card = h.$("set-remote-plan");
assert.ok(card && h.$("settings-pane").contains(card), "Account & plan does not show the relay's plan");
assert.match(card.textContent, /Remote access/);
assert.match(card.textContent, /Subscription, paid until /);
const text = h.$("settings-pane").textContent;
assert.ok(text.includes("Every part of the desktop app, for good."), "the desktop app is not said to be whole: " + text);
assert.ok(!relayPromise(text), "the plan promises what the relay will cost: " + relayPromise(text));
assert.ok(!/[$£€]\s?\d/.test(text), "a price was invented: " + text);
`)
}
