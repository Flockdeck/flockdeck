// The graph view model is pure, so it is tested with the fixtures the server
// writes (internal/server/testdata/graph-fixtures) and a few made-up messages.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import {
  buildGraphModel,
  attentionOf,
  panesNeedingYou,
  messagesOf,
  promptOf,
  needLabel,
  deliveryRank,
  emptyModel,
  DETAILS_ON_DESKTOP,
} from "../assets/graph/model.js";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures");

const names = readdirSync(dir)
  .filter((f) => f.endsWith(".json"))
  .map((f) => f.slice(0, -5))
  .sort();

const load = (name) => JSON.parse(readFileSync(join(dir, name + ".json"), "utf8"));

// The first lines of a fixture's .md say who needs the person and why:
// "- name (id8): reason", or "- a (id8) and b (id8): reason" for a conflict.
function expectedNeeds(name) {
  const md = readFileSync(join(dir, name + ".md"), "utf8");
  const section = md.split("## Needs the person")[1].split("\n## ")[0];
  const out = [];
  for (const line of section.split("\n")) {
    if (!line.startsWith("- ")) continue;
    const panes = [...line.matchAll(/\(([0-9a-f]{8})\)/g)].map((m) => m[1]);
    const reason = line.slice(line.indexOf("): ") + 3);
    let kind = "";
    if (/^asks to run /.test(reason)) kind = "permission";
    else if (/^asks: /.test(reason)) kind = "ask";
    else if (/^task returned from review/.test(reason)) kind = "returned";
    else if (/ both change /.test(line)) kind = "conflict";
    else if (/^exited with /.test(reason)) kind = "down";
    else if (/ unread: /.test(reason)) kind = "stale-unread";
    out.push({ kind, panes, line });
  }
  return out;
}

test("there are five fixtures", () => {
  assert.equal(names.length, 5);
});

for (const name of names) {
  test(`fixture ${name}: the needs-you panes and reasons come out of the model`, () => {
    const g = load(name);
    const m = buildGraphModel(g);
    assert.equal(m.ok, true);
    const want = expectedNeeds(name);
    assert.ok(want.length > 0);
    for (const w of want) assert.notEqual(w.kind, "", `the test does not know the reason in: ${w.line}`);

    // The .md lists the needs in prose, which is not always the graph's order (a
    // question that has waited longer comes before a command). So the set is
    // compared here, and the order is checked against the graph's own rule.
    const key = (x) => x.kind + ":" + [...x.panes].sort().join("+");
    const got = m.needs.map((n) => ({ kind: n.kind, panes: [n.pane, n.other].filter(Boolean).map((p) => p.slice(0, 8)) }));
    assert.deepEqual(got.map(key).sort(), want.map(key).sort(), "the needs are the .md's");
    assert.deepEqual(
      m.needs.map((n) => n.id),
      g.teams.flatMap((t) => t.needs).concat(g.soloNeeds).map((n) => n.id),
      "in the order the graph gave",
    );
    const waits = m.needs.filter((n) => n.kind === "permission" || n.kind === "ask").map((n) => Date.parse(n.time));
    assert.deepEqual(waits, [...waits].sort((a, b) => a - b), "permissions and questions by waiting time, longest first");

    // Every node is there once, in the graph's order, and every team has a tree
    // that holds each of its members once.
    assert.deepEqual(
      m.nodes.map((n) => n.pane),
      g.nodes.map((n) => n.pane),
    );
    for (const t of m.teams) {
      const seen = [];
      const walk = (n) => {
        seen.push(n.pane);
        n.children.forEach(walk);
      };
      walk(t.tree);
      assert.deepEqual([...seen].sort(), [...t.members].sort(), `the tree of ${t.id} holds its members`);
      assert.equal(t.tree.pane, t.manager);
    }

    // Each pane that needs the person is marked, and nothing else is.
    for (const id of panesNeedingYou(m)) assert.equal(attentionOf(m, id).level, "needs-you");
    const marked = m.nodes.filter((n) => attentionOf(m, n.pane).level === "needs-you").map((n) => n.pane);
    assert.deepEqual([...marked].sort(), [...panesNeedingYou(m)].sort());
  });
}

test("fixture 03 nests a helper under the helper that started it", () => {
  const m = buildGraphModel(load("03-nested-helper-question-and-returned-task"));
  const t = m.teams[0];
  const indexer = t.tree.children.find((c) => c.children.length > 0);
  assert.ok(indexer, "one helper has a helper of its own");
  assert.equal(indexer.depth, 1);
  assert.equal(indexer.children[0].depth, 2);
  assert.equal(m.byPane.get(indexer.children[0].pane).parent, indexer.pane);
});

test("fixture 05 has a message that has not been read, and a pane it waits on", () => {
  const m = buildGraphModel(load("05-six-helpers-undelivered-message"));
  assert.equal(m.edges.message.length, 1);
  assert.equal(m.undelivered.length, 1);
  assert.equal(m.undelivered[0].delivery, "queued");
  const manager = m.teams[0].manager;
  assert.equal(messagesOf(m, manager).inbound.length, 1);
  assert.equal(messagesOf(m, manager).outbound.length, 0);
  assert.equal(attentionOf(m, manager).needs[0].kind, "stale-unread");
});

test("the needs are in the graph's order and the model does not sort them", () => {
  const g = load("02-eight-helpers-permission-and-question");
  // Reverse the order the graph gave. The model must give the new order back.
  g.teams[0].needs.reverse();
  const m = buildGraphModel(g);
  assert.deepEqual(
    m.needs.map((n) => n.id),
    g.teams[0].needs.map((n) => n.id),
  );
});

test("a conflict is under both of its panes", () => {
  const m = buildGraphModel(load("01-six-helpers-one-permission"));
  const c = m.needs.find((n) => n.kind === "conflict");
  assert.ok(c && c.other);
  assert.equal(m.needsByPane.get(c.pane).includes(c), true);
  assert.equal(m.needsByPane.get(c.other).includes(c), true);
  assert.equal(panesNeedingYou(m).filter((p) => p === c.pane).length, 1);
});

const nodeOf = (pane, extra = {}) => ({ pane, kind: "terminal", depth: 0, waitingOn: { kind: "none" }, name: pane, status: "idle", activity: "idle", ...extra });

test("values the model does not know are ignored without an error", () => {
  const g = {
    type: "graph",
    root: "/r",
    project: "p",
    futureField: { a: 1 },
    teams: [{ id: "m", manager: "m", members: ["m", "h"], spendUSD: 1, caps: [], needs: [{ id: "next:m", kind: "next-kind", pane: "m", time: "2026-10-21T09:00:00Z", text: "x" }], more: true }],
    nodes: [
      nodeOf("m", { children: ["h"], team: "m", waitingOn: { kind: "a-new-kind", text: "t" }, status: "a-new-status", kind: "a-new-node-kind" }),
      nodeOf("h", { parent: "m", depth: 1, team: "m", extra: 1 }),
    ],
    edges: [
      { kind: "spawn", from: "m", to: "h" },
      { kind: "a-new-edge", from: "m", to: "h" },
      { kind: "message", from: "h", to: "m", id: 1, time: "2026-10-21T09:00:00Z", delivery: "a-new-state" },
    ],
    solo: [],
    soloNeeds: [],
  };
  const m = buildGraphModel(g);
  assert.equal(m.ok, true);
  assert.equal(m.needs[0].known, false);
  assert.equal(m.needs[0].label, "next-kind");
  const a = attentionOf(m, "m");
  assert.equal(a.level, "needs-you");
  assert.equal(attentionOf(m, "h").level, "none");
  assert.equal(m.byPane.get("m").waitingOn.kind, "a-new-kind");
  assert.equal(m.edges.spawn.length, 1);
  assert.equal(m.edges.message.length, 1);
  assert.equal(m.undelivered.length, 1);
  assert.equal(deliveryRank("a-new-state"), -1);
});

test("an unknown waitingOn kind does not throw and asks nothing of the person", () => {
  const m = buildGraphModel({ type: "graph", root: "/r", project: "", teams: [], nodes: [nodeOf("a", { waitingOn: { kind: "someday" } })], edges: [], solo: ["a"], soloNeeds: [] });
  assert.equal(attentionOf(m, "a").level, "none");
  assert.equal(m.solo.length, 1);
});

test("waiting on a reviewer, a task or helpers is held, and down is down", () => {
  const nodes = ["reviewer", "task", "helpers", "down", "user"].map((k) => nodeOf(k, { waitingOn: { kind: k } }));
  const m = buildGraphModel({ type: "graph", root: "/r", project: "", teams: [], nodes, edges: [], solo: nodes.map((n) => n.pane), soloNeeds: [] });
  const level = (id) => attentionOf(m, id).level;
  assert.deepEqual(["reviewer", "task", "helpers", "down", "user"].map(level), ["held", "held", "held", "down", "needs-you"]);
});

test("empty teams, an empty graph and a message that is not a graph", () => {
  const m = buildGraphModel({ type: "graph", root: "/r", project: "", teams: [], nodes: [], edges: [], solo: [], soloNeeds: [] });
  assert.equal(m.ok, true);
  assert.deepEqual([m.teams.length, m.nodes.length, m.needs.length, m.solo.length], [0, 0, 0, 0]);
  for (const bad of [null, undefined, 5, "x", [], {}, { type: "state" }, { type: "graph", teams: "no", nodes: 3, edges: {}, solo: null }]) {
    const e = buildGraphModel(bad);
    assert.equal(typeof e.ok, "boolean");
    assert.deepEqual(e.needs, []);
  }
  assert.equal(buildGraphModel(null).ok, false);
  assert.equal(emptyModel().ok, false);
});

test("a pane that names a parent loop or lists itself as a child appears once", () => {
  const nodes = [nodeOf("a", { children: ["b", "a"], team: "a" }), nodeOf("b", { parent: "a", children: ["a"], team: "a", depth: 1 })];
  const m = buildGraphModel({ type: "graph", root: "/r", project: "", teams: [{ id: "a", manager: "a", members: ["a", "b"], spendUSD: 0, caps: [], needs: [] }], nodes, edges: [], solo: [], soloNeeds: [] });
  const seen = [];
  const walk = (n) => {
    seen.push(n.pane);
    n.children.forEach(walk);
  };
  walk(m.teams[0].tree);
  assert.deepEqual(seen, ["a", "b"]);
});

test("text is kept as text and a value of the wrong type is not text", () => {
  const evil = "<img src=x onerror=alert(1)>";
  const m = buildGraphModel({
    type: "graph",
    root: "/r",
    project: evil,
    teams: [],
    nodes: [nodeOf("a", { name: evil, live: 5, dirty: "many", children: [1, null, "b"] })],
    edges: [],
    solo: ["a"],
    soloNeeds: [],
  });
  const n = m.byPane.get("a");
  assert.equal(n.name, evil);
  assert.equal(n.live, "");
  assert.equal(n.dirty, 0);
  assert.deepEqual(n.children, ["b"]);
  assert.equal(m.project, evil);
});

test("a need whose text was replaced for the relay says so", () => {
  const g = {
    type: "graph",
    root: "/r",
    project: "",
    teams: [{ id: "m", manager: "m", members: ["m"], spendUSD: 0, caps: [], needs: [{ id: "permission:m", kind: "permission", pane: "m", time: "2026-10-21T09:00:00Z", text: DETAILS_ON_DESKTOP }] }],
    nodes: [nodeOf("m", { team: "m", waitingOn: { kind: "user", text: DETAILS_ON_DESKTOP }, status: "waiting" }), nodeOf("s", { kind: "structured", team: "m" })],
    edges: [],
    solo: [],
    soloNeeds: [],
  };
  const m = buildGraphModel(g);
  assert.equal(m.needs[0].textHidden, true);
  assert.equal(attentionOf(m, "m").textHidden, true);
  assert.equal(attentionOf(m, "s").structured, true);
  assert.equal(attentionOf(m, "nobody").level, "none");
});

test("all seven kinds of need have a label, and an unknown one is shown as it is", () => {
  for (const k of ["permission", "ask", "returned", "conflict", "down", "setup", "stale-unread"]) {
    assert.notEqual(needLabel(k), k);
  }
  assert.equal(needLabel("next-kind"), "next-kind");
  assert.equal(needLabel(undefined), "");
  assert.equal(needLabel("toString"), "toString");
});

test("promptOf reads a full card, a scrubbed card, a withheld question and a structured pane", () => {
  const full = promptOf({ status: "waiting", permission: { tool: "Bash", summary: "Run: ls", command: "ls", options: [{ id: "yes" }, { id: "no" }] } });
  assert.deepEqual([full.kind, full.hidden, full.canAnswerHere, full.options.length], ["permission", false, true, 2]);

  const scrubbed = promptOf({ status: "waiting", detailsOnDesktop: true, permission: { tool: "Bash", summary: "Run a command, details on the desktop", redacted: true, options: [] } });
  assert.deepEqual([scrubbed.kind, scrubbed.hidden, scrubbed.canAnswerHere, scrubbed.summary], ["permission", true, false, "Run a command, details on the desktop"]);

  const noAsk = promptOf({ status: "waiting", detailsOnDesktop: true });
  assert.deepEqual([noAsk.kind, noAsk.hidden, noAsk.canAnswerHere], ["waiting", true, false]);

  const structured = promptOf({ status: "waiting", structured: true, detailsOnDesktop: true });
  assert.deepEqual([structured.kind, structured.hidden], ["waiting", true]);
  const idleStructured = promptOf({ status: "idle", structured: true, detailsOnDesktop: true });
  assert.equal(idleStructured.kind, "none");

  const ask = promptOf({ status: "waiting", ask: { questions: [{ question: "q" }] } });
  assert.deepEqual([ask.kind, ask.canAnswerHere], ["ask", false]);

  // A card a structured pane would carry is never answered from a window drawn from this push.
  assert.equal(promptOf({ structured: true, permission: { tool: "x", summary: "y", options: [{ id: "a" }] } }).canAnswerHere, false);
  assert.equal(promptOf(null).kind, "none");
  assert.equal(promptOf({ permission: "x" }).kind, "none");
});

test("500 nodes build in bounded time", () => {
  const nodes = [nodeOf("root", { team: "t", children: [] })];
  const members = ["root"];
  const edges = [];
  const needs = [];
  for (let i = 1; i < 500; i++) {
    const id = `p${i}`;
    const parent = i < 9 ? "root" : `p${Math.floor(i / 8)}`;
    nodes.push(nodeOf(id, { parent, depth: 1, team: "t", children: [], waitingOn: { kind: i % 7 === 0 ? "user" : "none" } }));
    members.push(id);
    edges.push({ kind: "spawn", from: parent, to: id });
    if (i % 7 === 0) needs.push({ id: `ask:${id}`, kind: "ask", pane: id, time: "2026-10-21T09:00:00Z" });
  }
  for (const n of nodes) n.children = nodes.filter((c) => c.parent === n.pane).map((c) => c.pane);
  const g = { type: "graph", root: "/r", project: "big", teams: [{ id: "t", manager: "root", members, spendUSD: 0, caps: [], needs }], nodes, edges, solo: [], soloNeeds: [] };

  const start = performance.now();
  const m = buildGraphModel(g);
  for (const n of m.nodes) attentionOf(m, n.pane);
  panesNeedingYou(m);
  const took = performance.now() - start;

  assert.equal(m.nodes.length, 500);
  assert.equal(m.needs.length, needs.length);
  let count = 0;
  const walk = (n) => {
    count++;
    n.children.forEach(walk);
  };
  walk(m.teams[0].tree);
  assert.equal(count, 500);
  assert.ok(took < 1000, `500 nodes took ${took} ms`);
});
