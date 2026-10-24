// The look of a box: loud for a pane that asks something of the person, quiet for
// the rest, what it waits on in its own words, and the task as one line.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { Graph } from "../assets/graph/Graph.js";
import { isLoud } from "../assets/graph/Badge.js";
import { taskLine, waitLine } from "../assets/graph/Node.js";

const node = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, name: pane, status: "idle", activity: "idle", waitingOn: { kind: "none" }, ...over });
const draw = (nodes, needs = []) => renderToString(html`<${Graph} model=${buildGraphModel({ type: "graph", root: "/r", project: "p", teams: [], nodes, edges: [], solo: nodes.map((n) => n.pane), soloNeeds: needs })} />`);
const need = (kind, pane) => ({ id: kind + pane, kind, pane, time: "2026-10-21T08:00:00Z", text: "ls" });
const boxOf = (svg, pane) => {
  const i = svg.indexOf(`data-pane="${pane}"`);
  return svg.slice(svg.lastIndexOf('<g class="gnode', i), svg.indexOf("</g>", i));
};

test("isLoud is true for a request of the person and false for unread mail, an ended pane and nothing", () => {
  assert.equal(isLoud([need("permission", "a")]), true);
  assert.equal(isLoud([need("stale-unread", "a"), need("ask", "a")]), true);
  assert.equal(isLoud([need("stale-unread", "a")]), false);
  assert.equal(isLoud([need("down", "a")]), false);
  assert.equal(isLoud([]), false);
  assert.equal(isLoud(undefined), false);
});

test("a pane that asks something has the loud look and no live line", () => {
  const svg = draw([node("a", { live: "Reading x", activity: "waiting" }), node("b", { live: "Reading y" })], [need("permission", "a")]);
  const a = boxOf(svg, "a");
  assert.match(a, /gnode--loud/);
  assert.match(a, /class="gnode-bar"/);
  assert.ok(!a.includes('class="gnode-live"'), "the block is the line");
  const b = boxOf(svg, "b");
  assert.ok(!b.includes("gnode--loud"));
  assert.ok(b.includes('class="gnode-live"'));
});

test("unread mail alone and an ended pane are in their block and not loud", () => {
  const svg = draw([node("a"), node("b", { activity: "exited", status: "exited" })], [need("stale-unread", "a"), need("down", "b")]);
  for (const p of ["a", "b"]) assert.ok(!boxOf(svg, p).includes("gnode--loud"), p);
  assert.match(svg, /gbadge--quiet/);
  assert.match(svg, /gbadge--down/);
});

test("waitLine names the helper a pane waits on, and says the rest in words", () => {
  const byPane = new Map([["h", node("h", { name: "helper" })]]);
  assert.equal(waitLine(node("m", { waitingOn: { kind: "helpers", ref: "h" } }), byPane), "waiting on helper");
  assert.equal(waitLine(node("m", { waitingOn: { kind: "helpers", ref: "gone" } }), byPane), "waiting on a helper");
  assert.equal(waitLine(node("m", { waitingOn: { kind: "helpers", ref: "m" } }), byPane), "waiting on a helper");
  assert.equal(waitLine(node("m", { waitingOn: { kind: "reviewer" } }), byPane), "waiting on a reviewer");
  assert.equal(waitLine(node("m", { waitingOn: { kind: "task" } }), byPane), "waiting on a task");
  for (const kind of ["none", "user", "down", "somethingnew"]) assert.equal(waitLine(node("m", { waitingOn: { kind } }), byPane), "", kind);
});

test("the drawn box has the waiting words with an arrow", () => {
  const svg = draw([node("a", { waitingOn: { kind: "reviewer", ref: "T1" } })]);
  assert.match(svg, /class="gnode-wait"[^>]*>→ waiting on a reviewer</);
});

test("taskLine keeps the id whole and cuts the title to what is left", () => {
  assert.deepEqual(taskLine({ id: "T4", title: "Limiter" }, 30), { id: "T4", title: "Limiter" });
  const t = taskLine({ id: "T4", title: "x".repeat(60) }, 30);
  assert.equal(t.id, "T4");
  assert.equal(Array.from(t.title).length, 27);
  assert.ok(t.title.endsWith("…"));
  assert.equal(taskLine({ id: "T4", title: "" }, 30).title, "");
  assert.equal(taskLine({ id: "T123456789012345", title: "abc" }, 14).title, "", "no room for a title");
});

test("a task title with markup is drawn as text", () => {
  const svg = draw([node("a", { task: { id: "T1", title: "<b>x</b>", state: "todo" } })]);
  assert.ok(!svg.includes("<b>"));
  assert.ok(svg.includes("&lt;b>x"));
});

test("the block over a box lets the pointer through to the box", () => {
  const css = readFileSync(fileURLToPath(new URL("../assets/graph/graph.css", import.meta.url)), "utf8");
  const m = /\.gneed\s*\{([^}]*)\}/.exec(css);
  assert.ok(m, "no .gneed rule");
  assert.match(m[1], /pointer-events:\s*none/);
});
