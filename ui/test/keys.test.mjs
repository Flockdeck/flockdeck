import { test } from "node:test";
import assert from "node:assert/strict";
import { signatureOf, signatureOfEvent, bindingsFrom, actionFor, keyAction, unreadable, keycaps, listMove, LIST } from "../src/shell/keys.js";
import { fakeKeys, realKeys } from "./util.mjs";

const ev = (key, mods = {}) => ({ key, ctrlKey: false, shiftKey: false, altKey: false, ...mods });

test("a binding written for a reader becomes the signature of its keydown", () => {
  assert.equal(signatureOf("Ctrl+Shift+K"), signatureOfEvent(ev("K", { ctrlKey: true, shiftKey: true })));
  assert.equal(signatureOf("F6"), signatureOfEvent(ev("F6")));
  assert.equal(signatureOf("Shift+F6"), signatureOfEvent(ev("F6", { shiftKey: true })));
  assert.equal(signatureOf("Ctrl+,"), signatureOfEvent(ev(",", { ctrlKey: true })));
  assert.equal(signatureOf("Ctrl+Shift+←"), signatureOfEvent(ev("ArrowLeft", { ctrlKey: true, shiftKey: true })));
});

test("Ctrl++ and Ctrl+= are one gesture", () => {
  assert.equal(signatureOf("Ctrl+="), signatureOfEvent(ev("+", { ctrlKey: true, shiftKey: true })));
});

test("a range, an empty binding and an unknown modifier have no signature", () => {
  assert.equal(signatureOf("Alt+1 … Alt+9"), "");
  assert.equal(signatureOf(""), "");
  assert.equal(signatureOf(undefined), "");
  assert.equal(signatureOf("Hyper+K"), "");
});

test("the table, and only the table, says what a keydown is", () => {
  const b = bindingsFrom(fakeKeys);
  assert.equal(actionFor(b, ev("k", { ctrlKey: true, shiftKey: true })), "palette");
  assert.equal(actionFor(b, ev("b", { ctrlKey: true })), "toggleRail");
  assert.equal(actionFor(b, ev("F6", { shiftKey: true })), "prevRegion");
  assert.equal(actionFor(b, ev("k")), "");
  // Change the binding in the table and the same keydown means something else.
  const moved = bindingsFrom(fakeKeys.map((k) => (k.id === "palette" ? { ...k, keys: "Ctrl+Shift+J" } : k)));
  assert.equal(actionFor(moved, ev("k", { ctrlKey: true, shiftKey: true })), "");
  assert.equal(actionFor(moved, ev("j", { ctrlKey: true, shiftKey: true })), "palette");
});

test("a key pressed while composing text belongs to the input method", () => {
  const b = bindingsFrom(fakeKeys);
  assert.equal(actionFor(b, { ...ev("k", { ctrlKey: true, shiftKey: true }), isComposing: true }), "");
  assert.equal(actionFor(b, { ...ev("k", { ctrlKey: true, shiftKey: true }), keyCode: 229 }), "");
});

test("where two actions share a binding the first in the table wins", () => {
  const b = bindingsFrom([
    { id: "one", keys: "Ctrl+Q" },
    { id: "two", keys: "Ctrl+Q" },
  ]);
  assert.equal(b.get(signatureOf("Ctrl+Q")), "one");
});

test("a keydown on a bare key matches no modified binding", () => {
  const b = bindingsFrom(fakeKeys);
  assert.equal(actionFor(b, ev("F6", { ctrlKey: true })), "");
});

test("keycaps splits a binding into its keys", () => {
  assert.deepEqual(keycaps("Ctrl+Shift+K"), ["Ctrl", "Shift", "K"]);
  assert.deepEqual(keycaps(""), []);
});

test("list keys move, wrap, accept and cancel", () => {
  assert.equal(listMove(ev(LIST.down), 0, 3), 1);
  assert.equal(listMove(ev(LIST.down), 2, 3), 0);
  assert.equal(listMove(ev(LIST.up), 0, 3), 2);
  assert.equal(listMove(ev(LIST.accept), 1, 3), "accept");
  assert.equal(listMove(ev(LIST.cancel), 1, 3), "cancel");
  assert.equal(listMove(ev("x"), 1, 3), "");
  assert.equal(listMove(ev(LIST.down), 0, 0), 0);
});

test("every binding of the server's own table reads, and no two actions clash", { skip: realKeys() ? false : "NEXT_KEYS_JSON is not set" }, () => {
  const table = realKeys();
  const seen = new Map();
  for (const k of table) {
    const sig = signatureOf(k.keys);
    if (!k.keys || k.keys.includes("…")) continue;
    assert.notEqual(sig, "", `${k.id} has binding ${k.keys}, which has no signature`);
    assert.ok(!seen.has(sig), `${k.id} and ${seen.get(sig)} share ${k.keys}`);
    seen.set(sig, k.id);
  }
  assert.equal(bindingsFrom(table).get(signatureOf("Ctrl+Shift+K")), "palette");
});

test("keyAction runs only the actions this window implements", () => {
  const b = bindingsFrom(fakeKeys);
  const k = ev("k", { ctrlKey: true, shiftKey: true });
  assert.equal(keyAction(b, k, ["palette"]), "palette");
  assert.equal(keyAction(b, k, ["toggleRail"]), null);
  assert.equal(keyAction(b, ev("g", { ctrlKey: true, shiftKey: true }), ["palette"]), null, "worktrees is bound but not implemented");
  assert.equal(keyAction(b, ev("q"), ["palette"]), null);
});

test("a binding the reader cannot turn into a key is reported, a range or an unbound action is not", () => {
  const t = [
    { id: "ok", keys: "Ctrl+B" },
    { id: "range", keys: "Alt+1 … Alt+9" },
    { id: "none", keys: "" },
    { id: "hyper", keys: "Hyper+X" },
  ];
  assert.deepEqual(unreadable(t), ["hyper"]);
  assert.deepEqual(unreadable(fakeKeys), []);
});

test("Tab is a list key the dialog keeps", () => {
  assert.equal(listMove(ev(LIST.tab), 0, 3), "tab");
});

test("the server's own table has no binding this reader cannot read", { skip: realKeys() ? false : "NEXT_KEYS_JSON is not set" }, () => {
  assert.deepEqual(unreadable(realKeys()), []);
});
