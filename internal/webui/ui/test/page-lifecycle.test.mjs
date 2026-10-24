// The page's store follows the browser hiding and showing it. A page restored
// from the back-forward cache starts its store again; a fresh load does not.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { watchPageLifecycle } from "../assets/lib/page-lifecycle.js";

const here = dirname(fileURLToPath(import.meta.url));

function fakeWindow() {
  const handlers = {};
  return {
    handlers,
    addEventListener: (type, fn) => { (handlers[type] ||= new Set()).add(fn); },
    removeEventListener: (type, fn) => { handlers[type]?.delete(fn); },
    fire: (type, ev = {}) => { for (const fn of [...(handlers[type] || [])]) fn(ev); },
  };
}
function fakeStore() {
  const calls = [];
  return { calls, start: () => calls.push("start"), stop: () => calls.push("stop"), send: () => true };
}

test("pagehide stops the store", () => {
  const win = fakeWindow();
  const store = fakeStore();
  watchPageLifecycle(win, store);
  win.fire("pagehide", { persisted: true });
  assert.deepEqual(store.calls, ["stop"]);
});

test("pageshow of a restored page starts the store again", () => {
  const win = fakeWindow();
  const store = fakeStore();
  watchPageLifecycle(win, store);
  win.fire("pageshow", { persisted: true });
  assert.deepEqual(store.calls, ["start"]);
});

test("pageshow of a fresh load leaves the running store alone", () => {
  const win = fakeWindow();
  const store = fakeStore();
  watchPageLifecycle(win, store);
  win.fire("pageshow", { persisted: false });
  win.fire("pageshow", {});
  assert.deepEqual(store.calls, []);
});

test("the returned function removes both handlers", () => {
  const win = fakeWindow();
  const store = fakeStore();
  const off = watchPageLifecycle(win, store);
  off();
  win.fire("pagehide");
  win.fire("pageshow", { persisted: true });
  assert.deepEqual(store.calls, []);
});

test("main.js wires the page through watchPageLifecycle", () => {
  const src = readFileSync(join(here, "..", "assets", "main.js"), "utf8");
  assert.match(src, /watchPageLifecycle\(window, store\);/);
  assert.ok(!/addEventListener\("page(show|hide)"/.test(src), "main.js handles page events itself again");
});
