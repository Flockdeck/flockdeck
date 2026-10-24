// The lines above the outline: unread mail, and the teams when there are two or more.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { buildGraphModel } from "../src/graph/model.js";
import { TreeNotes, notesOf } from "../src/graph/TreeNotes.js";

const n = (pane, team, over = {}) => ({ pane, kind: "terminal", depth: 0, team, name: pane, status: "working", activity: "working", waitingOn: { kind: "none" }, children: [], ...over });
const graph = (teams, nodes, needs = []) => ({ type: "graph", root: "/r", project: "p", teams, nodes, edges: [], solo: [], soloNeeds: needs });
const NOW = Date.parse("2026-10-24T12:00:00Z");
const stale = { id: "s", kind: "stale-unread", pane: "cdx", time: "2026-10-24T09:00:00Z", text: "2 unread" };

test("unread mail is a line of agents with how long, and nothing when there is none", () => {
  const m = buildGraphModel(graph([{ id: "t", manager: "m", goal: "g", members: ["m", "cdx"], needs: [stale] }], [n("m", "t", { role: "manager" }), n("cdx", "t")]));
  assert.deepEqual(notesOf(m, NOW).unread, [{ pane: "cdx", name: "cdx", age: "3h" }]);
  const out = renderToString(html`<${TreeNotes} model=${m} now=${NOW} onOpen=${() => {}} />`);
  assert.match(out, /data-note="unread"/);
  assert.ok(!out.includes('data-note="teams"'), "one team is no list of teams");
  const none = buildGraphModel(graph([{ id: "t", manager: "m", goal: "g", members: ["m"], needs: [] }], [n("m", "t")]));
  assert.equal(renderToString(html`<${TreeNotes} model=${none} now=${NOW} />`), "");
});

test("two teams are listed with how many of their agents need the person", () => {
  const ask = { id: "a", kind: "permission", pane: "b1", time: "2026-10-24T11:00:00Z", text: "go test" };
  const m = buildGraphModel(graph(
    [{ id: "t1", manager: "m1", goal: "One", members: ["m1"], needs: [] }, { id: "t2", manager: "m2", goal: "Two", members: ["m2", "b1"], needs: [ask] }],
    [n("m1", "t1", { role: "manager" }), n("m2", "t2", { role: "manager" }), n("b1", "t2", { parent: "m2" })]));
  const t = notesOf(m, NOW).teams;
  assert.deepEqual(t.map((x) => [x.name, x.needs]), [["One", 0], ["Two", 1]]);
  const out = renderToString(html`<${TreeNotes} model=${m} now=${NOW} onOpen=${() => {}} />`);
  assert.match(out, /1 need you/);
});
