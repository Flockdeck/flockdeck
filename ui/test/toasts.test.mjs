// The title and icon that say how many agents wait, and the toasts for agents that
// newly need the person while the window is in front.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { ICON_DEFAULT, ICON_WAITING, showAttention, titleOf, waitingCount } from "../src/lib/attention.js";
import { MAX_TOASTS, Toasts, addToasts, keepWaiting, toastOf } from "../src/shell/Toasts.js";

test("the title leads with how many agents wait, and is plain with none", () => {
  assert.equal(titleOf(0), "Flockdeck");
  assert.equal(titleOf(1), "(1) Flockdeck");
  assert.equal(titleOf(12), "(12) Flockdeck");
});

test("waitingCount counts waiting and blocked panes and nothing else", () => {
  assert.equal(waitingCount({ a: { status: "waiting" }, b: { status: "blocked" }, c: { status: "running" }, d: null, e: 3 }), 2);
  assert.equal(waitingCount(null), 0);
  assert.equal(waitingCount(undefined), 0);
});

function fakeDoc({ head = true } = {}) {
  const links = new Map();
  return {
    title: "Flockdeck",
    getElementById: (id) => links.get(id) || null,
    createElement: () => ({}),
    head: head ? { appendChild: (el) => links.set(el.id, el) } : null,
    links,
  };
}

test("showAttention sets the title and swaps the tab icon, making the link once", () => {
  const doc = fakeDoc();
  showAttention(doc, 2);
  assert.equal(doc.title, "(2) Flockdeck");
  const link = doc.links.get("flockdeck-icon");
  assert.equal(link.href, ICON_WAITING);
  assert.equal(link.rel, "icon");
  showAttention(doc, 0);
  assert.equal(doc.title, "Flockdeck");
  assert.equal(doc.links.get("flockdeck-icon"), link, "the same link");
  assert.equal(link.href, ICON_DEFAULT);
});

test("showAttention copes with no document and a page without a head", () => {
  showAttention(null, 1);
  const doc = fakeDoc({ head: false });
  showAttention(doc, 1);
  assert.equal(doc.title, "(1) Flockdeck");
});

const fresh = (id, name = id, blocked = false) => ({ id, name, blocked });

test("addToasts adds one per pane, leaves out the open pane and keeps the newest few", () => {
  let t = addToasts([], [fresh("a"), fresh("b")], "b");
  assert.deepEqual(t.map((x) => x.id), ["a"], "the pane whose drawer is open needs no toast");
  t = addToasts(t, [fresh("a", "a", true)]);
  assert.equal(t.length, 1, "a pane has at most one");
  assert.match(t[0].text, /blocked/);
  t = addToasts(t, [fresh("c"), fresh("d"), fresh("e")]);
  assert.equal(t.length, MAX_TOASTS);
  assert.deepEqual(t.map((x) => x.id), ["c", "d", "e"]);
});

test("keepWaiting drops the toasts of panes that stopped waiting, and returns the same list when none went", () => {
  const t = [toastOf(fresh("a")), toastOf(fresh("b"))];
  assert.equal(keepWaiting(t, new Set(["a", "b"])), t);
  assert.deepEqual(keepWaiting(t, new Set(["b"])).map((x) => x.id), ["b"]);
});

test("the toasts name the agent and offer Open and Dismiss, and draw nothing when empty", () => {
  assert.equal(renderToString(html`<${Toasts} toasts=${[]} />`), "");
  const out = renderToString(html`<${Toasts} toasts=${[toastOf(fresh("p1", "api <b>"))]} onOpen=${() => {}} onDismiss=${() => {}} />`);
  assert.match(out, /api &lt;b><\/b> needs you: waiting for input/, "the name is drawn as text");
  assert.match(out, />Open</);
  assert.match(out, />Dismiss</);
});
