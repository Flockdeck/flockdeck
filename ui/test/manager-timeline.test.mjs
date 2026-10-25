import { test } from "node:test";
import assert from "node:assert/strict";
import { managerTimeline, cutOf, EDGE_CUT } from "../src/graph/ManagerTimeline.js";
import { buildGraphModel } from "../src/graph/model.js";

const node = (pane, name, extra = {}) => ({ pane, name, role: pane === "m" ? "manager" : "worker", status: "working", activity: "working", ...extra });
const graph = (edges) => buildGraphModel({
  type: "graph", root: "/r", teams: [{ id: "t", name: "T", manager: "m", members: ["m", "a", "b"], goal: "g" }],
  nodes: [node("m", "mgr"), node("a", "api", { parent: "m" }), node("b", "docs", { parent: "m" })], edges, needs: [],
});
const mail = (id, from, to, time, text = "hi", delivery = "read") => ({ kind: "message", id, from, to, time, text, delivery });
const T = (min) => new Date(Date.UTC(2026, 9, 25, 10, min)).toISOString();

test("conversation, mail to the manager and team events are one list, oldest first", () => {
  const model = graph([mail(1, "a", "m", T(5), "API is done"), mail(2, "a", "b", T(6), "not for the manager"), mail(3, "b", "m", T(20), "docs ready", "queued")]);
  const entries = [{ id: "e1", ts: T(1), kind: "prompt" }, { id: "e2", ts: T(10), kind: "reply" }, { id: "e3", ts: T(30), kind: "reply" }];
  const events = [{ kind: "ship", pane: "a", text: "api finished T1.", at: Date.parse(T(15)) }, { kind: "info", pane: "a", text: "x", at: Date.parse(T(16)) }, { kind: "down", pane: "b", text: "docs exited.", at: Date.parse(T(25)) }];
  const out = managerTimeline({ entries, model, manager: "m", events });
  assert.deepEqual(out.map((x) => (x.kind === "event" ? "event:" + x.tone : x.key)), ["c:e1", "m:1", "c:e2", "event:ship", "m:3", "event:down", "c:e3"]);
  const m3 = out.find((x) => x.key === "m:3");
  assert.equal(m3.waiting, true);
  assert.equal(m3.label, "docs");
  assert.equal(out.find((x) => x.key === "m:1").waiting, false);
  assert.equal(out.some((x) => x.key === "m:2"), false, "a message between two helpers is not the manager's");
});

test("an entry with no usable time stays between its neighbours", () => {
  const model = graph([mail(1, "a", "m", T(10))]);
  const entries = [{ id: "e1", ts: T(5) }, { id: "e2", ts: "" }, { id: "e3", ts: T(20) }];
  const out = managerTimeline({ entries, model, manager: "m" });
  assert.deepEqual(out.map((x) => x.key), ["c:e1", "c:e2", "m:1", "c:e3"]);
});

test("no model or no manager gives the conversation alone, and nothing throws", () => {
  const entries = [{ id: "e1", ts: T(1) }];
  assert.deepEqual(managerTimeline({ entries }).map((x) => x.key), ["c:e1"]);
  assert.deepEqual(managerTimeline({}), []);
  assert.deepEqual(managerTimeline({ entries, model: { ok: false }, manager: "m" }).length, 1);
});

test("a message the desk shortened says so", () => {
  assert.equal(cutOf("short"), false);
  assert.equal(cutOf("x".repeat(EDGE_CUT - 1)), true);
  assert.equal(cutOf("a report that was cut…"), true);
  assert.equal(cutOf(undefined), false);
});

test("the manager's own events are left out, and mail with no time is left out", () => {
  const model = graph([mail(1, "a", "m", "")]);
  const out = managerTimeline({ model, manager: "m", events: [{ kind: "ship", pane: "m", text: "x", at: 1 }] });
  assert.deepEqual(out, []);
});
