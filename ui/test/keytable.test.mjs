import { test } from "node:test";
import assert from "node:assert/strict";
import { KEYS_PATH, defaultsFrom, keysUrl, loadKeyDefaults, mergeKeys, readsKeyFiles } from "../src/lib/keytable.js";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const DEFAULTS = [
  { id: "closePane", keys: "Ctrl+W", label: "Close pane", section: "Panes" },
  { id: "quit", keys: "Ctrl+Q", label: "Quit", section: "App" },
  { id: "help", keys: "F1", label: "Help", section: "App" },
];
const withCap = (over = {}) => hello({ api: { major: 1, minor: 2, caps: ["keyOverrides"] }, ...over });
const settle = () => new Promise((r) => setImmediate(r));

async function up(keyDefaults) {
  const h = await harness({ keyDefaults });
  h.store.start();
  FakeWebSocket.last.open();
  return { ...h, ws: FakeWebSocket.last };
}

test("keys.json sits next to the page's other assets, under the relay's prefix too", () => {
  assert.equal(KEYS_PATH, "assets/keys.json");
  assert.equal(keysUrl(new URL("http://127.0.0.1:5/")), "http://127.0.0.1:5/assets/keys.json");
  assert.equal(keysUrl(new URL("https://relay.example/d/h1/index.html")), "https://relay.example/d/h1/assets/keys.json");
});

test("defaultsFrom keeps entries that name an action and refuses what is not a list", () => {
  assert.equal(defaultsFrom({ keys: [] }), null);
  assert.equal(defaultsFrom([]), null);
  assert.deepEqual(defaultsFrom([null, 3, { id: "", label: "x" }, { id: "a" }, DEFAULTS[0]]), [DEFAULTS[0]]);
});

test("loadKeyDefaults resolves to the list, or to null for every way it can fail", async () => {
  const loc = new URL("http://127.0.0.1:5/");
  const seen = [];
  assert.deepEqual(await loadKeyDefaults(async (u, o) => (seen.push([u, o]), { ok: true, json: async () => DEFAULTS }), loc), DEFAULTS);
  assert.equal(seen[0][0], "http://127.0.0.1:5/assets/keys.json");
  assert.equal(seen[0][1].credentials, "same-origin");
  assert.equal(await loadKeyDefaults(async () => ({ ok: false, status: 403 }), loc), null);
  assert.equal(await loadKeyDefaults(async () => { throw new Error("offline"); }, loc), null);
  assert.equal(await loadKeyDefaults(async () => ({ ok: true, json: async () => ({}) }), loc), null);
});

test("mergeKeys lays the remaps over the defaults and leaves the hidden ones out", () => {
  const got = mergeKeys(DEFAULTS, { closePane: "Ctrl+Alt+W", nowhere: "X", help: 5 }, ["quit", 7]);
  assert.deepEqual(got, [{ ...DEFAULTS[0], keys: "Ctrl+Alt+W", overridden: true }, DEFAULTS[2]]);
  assert.deepEqual(mergeKeys(DEFAULTS, undefined, undefined), DEFAULTS);
  assert.deepEqual(mergeKeys(DEFAULTS, { __proto__: null, toString: "x" }, null).map((k) => k.id), ["closePane", "quit", "help"]);
});

test("readsKeyFiles needs the keyOverrides cap", () => {
  assert.equal(readsKeyFiles(withCap()), true);
  assert.equal(readsKeyFiles(hello()), false);
});

test("a hello with the cap gets its table from keys.json plus the changes", async () => {
  const { store, ws } = await up(async () => DEFAULTS);
  ws.frame(withCap({ keys: [], keyOverrides: { closePane: "Ctrl+Alt+W" }, keysHidden: ["quit"] }));
  await settle();
  assert.deepEqual(store.getState().hello.keys, [{ ...DEFAULTS[0], keys: "Ctrl+Alt+W", overridden: true }, DEFAULTS[2]]);
  // A remap pushed later is merged the same way, and a reset takes the override away.
  ws.frame({ type: "keyTable", keys: [], keyOverrides: { help: "F2" }, keysHidden: ["quit"] });
  assert.deepEqual(store.getState().hello.keys.map((k) => [k.id, k.keys, !!k.overridden]), [["closePane", "Ctrl+W", false], ["help", "F2", true]]);
  ws.frame({ type: "keyTable", keys: [] });
  assert.deepEqual(store.getState().hello.keys.map((k) => k.id), ["closePane", "quit", "help"]);
});

test("keys.json is asked for once, however many hellos come", async () => {
  let asked = 0;
  const { ws } = await up(async () => (asked++, DEFAULTS));
  ws.frame(withCap());
  ws.frame(withCap());
  await settle();
  ws.frame(withCap());
  assert.equal(asked, 1);
});

test("without keys.json the keys the hello carries are used, and the next hello asks again", async () => {
  let asked = 0;
  const { store, ws } = await up(async () => (asked++, asked === 1 ? null : DEFAULTS));
  ws.frame(withCap({ keys: [{ id: "sent" }] }));
  await settle();
  assert.deepEqual(store.getState().hello.keys, [{ id: "sent" }]);
  ws.frame(withCap({ keys: [{ id: "sent" }] }));
  await settle();
  assert.equal(asked, 2);
  assert.deepEqual(store.getState().hello.keys.map((k) => k.id), ["closePane", "quit", "help"]);
});

test("a server without the cap keeps sending the table whole and keys.json is not read", async () => {
  let asked = 0;
  const { store, ws } = await up(async () => (asked++, DEFAULTS));
  ws.frame(hello({ keys: [{ id: "a" }] }));
  ws.frame({ type: "keyTable", keys: [{ id: "b" }] });
  await settle();
  assert.equal(asked, 0);
  assert.deepEqual(store.getState().hello.keys, [{ id: "b" }]);
});
