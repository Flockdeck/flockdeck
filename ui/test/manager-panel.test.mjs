// The manager's conversation tab holds its mail and team events; other panes' tabs do not.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Drawer } from "../src/drawer/Drawer.js";
import { Row } from "../src/drawer/conv/rows.js";
import { entryText } from "../src/drawer/conv/text.js";
import { buildGraphModel } from "../src/graph/model.js";
import { fakeStore, paneState } from "./termfakes.mjs";

const gnode = (pane, name, extra = {}) => ({ pane, name, role: pane === "m" ? "manager" : "worker", status: "working", activity: "working", ...extra });
const model = buildGraphModel({
  type: "graph", root: "/r",
  teams: [{ id: "t", name: "T", manager: "m", members: ["m", "a"], goal: "g" }],
  nodes: [gnode("m", "mgr"), gnode("a", "api", { parent: "m" })],
  edges: [{ kind: "message", id: 1, from: "a", to: "m", time: "2026-10-25T10:05:00Z", text: "API is done", delivery: "queued" }],
  needs: [],
});
const NOW = Date.parse("2026-10-25T11:00:00Z");
const conv = (id) => ({ id, supported: true, entries: [{ id: "e1", kind: "reply", ts: "2026-10-25T10:00:00Z", text: "On it." }], atStart: true, older: "idle", refused: "" });
const draw = (pane, team) => {
  const st = fakeStore({ hello: { type: "hello" }, conversation: conv(pane), state: paneState({ [pane]: {} }) });
  const node = { pane, kind: "terminal", structured: false, name: pane === "m" ? "mgr" : "api", role: pane === "m" ? "manager" : "worker", agent: "claude", shell: false, status: "working", activity: "working", since: "2026-10-25T09:00:00Z" };
  return renderToString(html`<${Drawer} store=${st} node=${node} now=${NOW} team=${team} />`);
};

test("a manager's conversation shows the mail sent to it and the team's events", () => {
  const events = [{ kind: "ship", pane: "a", text: "api finished T1.", at: Date.parse("2026-10-25T10:10:00Z") }];
  const out = draw("m", { model, events });
  assert.match(out, /From api/);
  assert.match(out, /API is done/);
  assert.match(out, /not read yet/);
  assert.match(out, /api finished T1\./);
  assert.match(out, /On it\./);
});

test("another pane's conversation is its own, and so is a manager's without a team", () => {
  assert.doesNotMatch(draw("a", { model, events: [] }), /API is done/);
  assert.doesNotMatch(draw("m", null), /API is done/);
});

test("the rows say what they are, and read-mail is not marked waiting", () => {
  const mail = (over) => renderToString(html`<${Row} entry=${{ id: "m:1", kind: "mail", from: "a", label: "api", text: "hi", waiting: false, maybeCut: false, ...over }} />`);
  assert.doesNotMatch(mail({}), /not read yet|is-waiting/);
  assert.match(mail({}), /First line only/);
  assert.match(mail({ maybeCut: true, waiting: true }), /May be cut/);
  const ev = renderToString(html`<${Row} entry=${{ id: "e:1", kind: "event", tone: "down", pane: "a", text: "api exited." }} />`);
  assert.match(ev, /conv-event--down/);
});

test("copied text for mail and events", () => {
  assert.match(entryText({ kind: "mail", label: "api", text: "hi" }), /api.*hi/s);
  assert.match(entryText({ kind: "event", text: "api exited." }), /api exited\./);
});

test("a mail or event row with missing fields draws without throwing", () => {
  assert.match(renderToString(html`<${Row} entry=${{ id: "m:2", kind: "mail" }} />`), /From an agent/);
  assert.doesNotThrow(() => renderToString(html`<${Row} entry=${{ id: "e:2", kind: "event" }} />`));
});

test("the drawer says when its own message box is showing, so the home bar can step aside", async () => {
  const { createDom, install, settle } = await import("./dom.mjs");
  const { render } = await import("../src/vendor/preact.mjs");
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const st = fakeStore({ hello: { type: "hello" }, conversation: conv("m"), state: paneState({ m: {} }) });
    const node = { pane: "m", kind: "terminal", structured: false, name: "mgr", role: "manager", agent: "claude", shell: false, status: "working", activity: "working", since: "2026-10-25T09:00:00Z" };
    const seen = [];
    render(html`<${Drawer} store=${st} node=${node} now=${NOW} team=${{ model, events: [] }} onBox=${(p) => seen.push(p)} />`, box);
    await settle();
    assert.equal(seen[seen.length - 1], "", "the terminal tab has no box");
    box.querySelector("#drawer-tab-conversation").click();
    await settle();
    assert.equal(seen[seen.length - 1], "m");
    render(null, box);
    await settle();
    assert.equal(seen[seen.length - 1], "", "closing the drawer gives the bar back");
  } finally {
    restore();
  }
});
