// The keyboard in the team graph: arrows along the tree, the wrap rules, the
// jump to the next agent that needs the person and the manager bar, and that
// every one of them works with no pointer in the real page.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import { LIST } from "../assets/shell/keys.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { navOf, stepFrom, nextNeedsYou, onGraphKey, paneAt } from "../assets/graph/keys.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { realKeys } from "./util.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures");
const FIXTURES = [
  "01-six-helpers-one-permission",
  "02-eight-helpers-permission-and-question",
  "03-nested-helper-question-and-returned-task",
  "04-eight-helpers-three-waiting",
  "05-six-helpers-undelivered-message",
];
const load = (name) => ({ ...JSON.parse(readFileSync(join(dir, name + ".json"), "utf8")), root: "/work/a" });

// A graph of teams and loose panes written as { pane, parent } rows.
function synth({ teams, solo = [], loose = {} }) {
  const nodes = [];
  const kids = new Map();
  for (const t of teams) for (const n of t.nodes) if (n.parent) kids.set(n.parent, [...(kids.get(n.parent) || []), n.pane]);
  for (const t of teams) for (const n of t.nodes) nodes.push({ pane: n.pane, name: n.pane, parent: n.parent || "", children: kids.get(n.pane) || [], team: t.id, status: "idle" });
  for (const p of solo) nodes.push({ pane: p, name: p, status: "idle" });
  for (const [team, panes] of Object.entries(loose)) for (const p of panes) nodes.push({ pane: p, name: p, team, status: "idle" });
  return {
    type: "graph", root: "/work/a", nodes,
    teams: teams.map((t) => ({ id: t.id, manager: t.nodes[0].pane, members: [...t.nodes.map((n) => n.pane), ...(loose[t.id] || [])] })),
    edges: [], solo,
  };
}
const model = (msg) => buildGraphModel(msg);

// m1 > (a, b > (c, d, e), f); team 2: m2 > g; solo s1, s2, s3.
const MANY = synth({
  teams: [
    { id: "t1", nodes: [{ pane: "m1" }, { pane: "a", parent: "m1" }, { pane: "b", parent: "m1" }, { pane: "c", parent: "b" }, { pane: "d", parent: "b" }, { pane: "e", parent: "b" }, { pane: "f", parent: "m1" }] },
    { id: "t2", nodes: [{ pane: "m2" }, { pane: "g", parent: "m2" }] },
  ],
  solo: ["s1", "s2", "s3"],
});
const step = (nav, pane, key) => stepFrom(nav, pane, key);

test("Up goes to the manager and Down to the first helper, across nested rows", () => {
  const nav = navOf(model(MANY));
  assert.equal(step(nav, "m1", LIST.down), "a");
  assert.equal(step(nav, "b", LIST.down), "c");
  assert.equal(step(nav, "e", LIST.up), "b");
  assert.equal(step(nav, "b", LIST.up), "m1");
  assert.equal(step(nav, "g", LIST.up), "m2");
});

test("Up from the top of a tree and Down from a leaf stay where they are", () => {
  const nav = navOf(model(MANY));
  assert.equal(step(nav, "m1", LIST.up), "");
  assert.equal(step(nav, "a", LIST.down), "");
  assert.equal(step(nav, "s1", LIST.up), "");
  assert.equal(step(nav, "s1", LIST.down), "");
});

test("Left and Right move along the row of one parent's helpers and wrap at both ends", () => {
  const nav = navOf(model(MANY));
  assert.equal(step(nav, "a", LIST.right), "b");
  assert.equal(step(nav, "b", LIST.right), "f");
  assert.equal(step(nav, "f", LIST.right), "a", "past the last is the first");
  assert.equal(step(nav, "a", LIST.left), "f", "before the first is the last");
  assert.equal(step(nav, "c", LIST.right), "d");
  assert.equal(step(nav, "e", LIST.right), "c", "the nested row wraps inside itself");
  assert.equal(step(nav, "c", LIST.left), "e");
});

test("the managers of the teams are one row, and the panes in no team another", () => {
  const nav = navOf(model(MANY));
  assert.equal(step(nav, "m1", LIST.right), "m2");
  assert.equal(step(nav, "m2", LIST.right), "m1");
  assert.equal(step(nav, "s1", LIST.right), "s2");
  assert.equal(step(nav, "s3", LIST.right), "s1");
  assert.equal(step(nav, "s1", LIST.left), "s3");
});

test("an only child has nowhere to go sideways", () => {
  const nav = navOf(model(MANY));
  assert.equal(step(nav, "g", LIST.right), "");
  assert.equal(step(nav, "g", LIST.left), "");
  const one = navOf(model(synth({ teams: [{ id: "t", nodes: [{ pane: "m" }] }] })));
  assert.equal(step(one, "m", LIST.left), "");
});

test("a member the tree does not reach is a row of its own, and Up goes to its team's manager", () => {
  const msg = synth({ teams: [{ id: "t1", nodes: [{ pane: "m1" }, { pane: "a", parent: "m1" }] }], loose: { t1: ["x", "y"] } });
  const nav = navOf(model(msg));
  assert.equal(step(nav, "x", LIST.right), "y");
  assert.equal(step(nav, "y", LIST.right), "x");
  assert.equal(step(nav, "x", LIST.up), "m1");
  assert.equal(step(nav, "a", LIST.right), "", "a is the only helper, and x and y are not its row");
});

test("every pane of every fixture can be reached from the manager by the arrows alone", () => {
  for (const name of FIXTURES) {
    const m = model(load(name));
    const nav = navOf(m);
    const seen = new Set();
    const queue = [m.teams[0].manager];
    while (queue.length) {
      const p = queue.pop();
      if (seen.has(p)) continue;
      seen.add(p);
      for (const key of [LIST.up, LIST.down, LIST.left, LIST.right]) {
        const to = step(nav, p, key);
        if (to) queue.push(to);
      }
    }
    assert.deepEqual([...seen].sort(), m.nodes.map((n) => n.pane).sort(), name);
  }
});

test("a chain of 20000 panes is walked, not thrown on", () => {
  const nodes = [{ pane: "p0" }];
  for (let i = 1; i < 20000; i++) nodes.push({ pane: "p" + i, parent: "p" + (i - 1) });
  const nav = navOf(model(synth({ teams: [{ id: "t", nodes }] })));
  assert.equal(step(nav, "p0", LIST.down), "p1");
  assert.equal(step(nav, "p19999", LIST.up), "p19998");
});

test("the jump goes in the strip's order, not the picture's, and wraps", () => {
  const m = model(load("04-eight-helpers-three-waiting"));
  // The strip: notes, build, qa. The boxes: build, notes, qa.
  assert.equal(nextNeedsYou(m, ""), "160d47b0");
  assert.equal(nextNeedsYou(m, "160d47b0"), "e8c9aa4e");
  assert.equal(nextNeedsYou(m, "e8c9aa4e"), "262e0b2b");
  assert.equal(nextNeedsYou(m, "262e0b2b"), "160d47b0", "past the last is the first");
});

test("the jump starts at the first from a pane the strip does not hold", () => {
  const m = model(load("04-eight-helpers-three-waiting"));
  assert.equal(nextNeedsYou(m, "d1faf784"), "160d47b0");
  assert.equal(nextNeedsYou(m, "not-a-pane"), "160d47b0");
});

test("a conflict puts both its panes in the jump, each once", () => {
  const m = model(load("01-six-helpers-one-permission"));
  const conflict = m.needs.find((n) => n.kind === "conflict");
  assert.ok(conflict && conflict.other, "the fixture has a conflict");
  const visited = [];
  let at = "";
  do {
    at = nextNeedsYou(m, at);
    visited.push(at);
  } while (at !== visited[0] || visited.length === 1);
  const cycle = visited.slice(0, -1);
  assert.equal(new Set(cycle).size, cycle.length, "each pane once in a round");
  assert.ok(cycle.includes(conflict.pane) && cycle.includes(conflict.other));
});

test("the jump skips an item for a pane that has no box", () => {
  const g = load("04-eight-helpers-three-waiting");
  const needs = g.teams[0].needs.map((n, i) => (i === 0 ? { ...n, pane: "ghost" } : n));
  const m = model({ ...g, teams: [{ ...g.teams[0], needs }, ...g.teams.slice(1)] });
  assert.ok(m.needs.some((n) => n.pane === "ghost"), "the strip still holds the item");
  assert.equal(nextNeedsYou(m, ""), "e8c9aa4e");
  assert.equal(nextNeedsYou(m, "262e0b2b"), "e8c9aa4e");
});

test("the jump says nothing when nothing needs the person", () => {
  const m = model(synth({ teams: [{ id: "t", nodes: [{ pane: "m" }, { pane: "a", parent: "m" }] }] }));
  assert.equal(nextNeedsYou(m, ""), "");
});

test("the key handler leaves other keys, modifiers and composing alone", () => {
  const nav = navOf(model(MANY));
  const box = (pane) => ({ getAttribute: (k) => (k === "role" ? "button" : k === "data-pane" ? pane : null) });
  const ev = (key, over = {}) => ({ key, target: box("a"), currentTarget: { querySelectorAll: () => [] }, preventDefault() { this.prevented = true; }, ...over });
  const others = [
    ev("x"),
    ev(LIST.right, { ctrlKey: true }),
    ev(LIST.right, { shiftKey: true }),
    ev(LIST.right, { altKey: true }),
    ev(LIST.right, { metaKey: true }),
    ev(LIST.right, { isComposing: true }),
    ev(LIST.right, { defaultPrevented: true }),
    ev(LIST.right, { target: { getAttribute: () => null } }),
  ];
  for (const e of others) {
    onGraphKey(nav, e);
    assert.ok(!e.prevented, JSON.stringify(Object.keys(e)));
  }
});

test("the key handler moves only from a box, not from another element in the graph", () => {
  const nav = navOf(model(MANY));
  const e = { key: LIST.right, target: { getAttribute: (k) => (k === "role" ? "group" : k === "data-pane" ? "a" : null) }, currentTarget: { querySelectorAll: () => [] }, preventDefault() { this.prevented = true; } };
  assert.equal(onGraphKey(nav, e), false);
  assert.ok(!e.prevented);
});

test("paneAt finds the pane of an element inside a box", () => {
  const el = (id, parentNode) => ({ parentNode, getAttribute: (k) => (k === "data-pane" ? id : null) });
  const box = el("a", null);
  assert.equal(paneAt(el("", box)), "a");
  assert.equal(paneAt(null), "");
});

// --- in the page ---------------------------------------------------------

const FALLBACK = [
  { id: "nextNeedsYou", keys: "Ctrl+Shift+U", label: "Next agent that needs you", section: "Finding your way" },
  { id: "managerBar", keys: "Ctrl+Shift+H", label: "Message the manager", section: "Finding your way" },
];
const keysFor = () => {
  const real = realKeys();
  if (!real) return FALLBACK;
  for (const id of ["nextNeedsYou", "managerBar"]) assert.ok(real.some((k) => k.id === id && k.keys), id + " is in the key table with a binding");
  return real;
};
const press = (m, id) => {
  const k = keysFor().find((x) => x.id === id);
  const parts = k.keys.split("+");
  return m.dom.window.dispatch("keydown", { key: parts[parts.length - 1], ctrlKey: parts.includes("Ctrl"), shiftKey: parts.includes("Shift"), altKey: parts.includes("Alt") });
};

async function mount(name, edit = (g) => g) {
  PtySocket.reset();
  FakeTerm.reset();
  FakeObserver.instances.length = 0;
  const dom = createDom();
  const restore = install(dom);
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
  ws().open();
  ws().frame(hello({ keys: keysFor() }));
  await settle();
  ws().frame({ type: "state", root: "/work/a", projects: [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }], panes: {} });
  await settle();
  ws().frame(edit(load(name)));
  await settle();
  return {
    dom, container, ws,
    box: (pane) => container.findAll("[data-pane]").find((e) => e.localName === "g" && e.getAttribute("data-pane") === pane && e.getAttribute("role") === "button"),
    active: () => dom.document.activeElement,
    activePane: () => dom.document.activeElement.getAttribute("data-pane"),
    drawer: () => container.find("[data-drawer-pane]"),
    done: () => { render(null, container); store.stop(); restore(); },
  };
}
async function withPage(name, fn, edit) {
  const m = await mount(name, edit);
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

test("in the page the arrows move the keyboard from box to box, Enter opens the drawer and Esc closes it", () =>
  withPage("03-nested-helper-question-and-returned-task", async (m) => {
    m.box("6e7bb3af").focus();
    const arrow = (key) => m.active().keydown(key);
    assert.equal(arrow(LIST.down).defaultPrevented, true);
    assert.equal(m.activePane(), "025b0446", "Down: the manager's first helper");
    arrow(LIST.down);
    assert.equal(m.activePane(), "21c76d4a", "Down: its helper");
    arrow(LIST.up);
    arrow(LIST.right);
    assert.equal(m.activePane(), "839bba23", "Right: the next helper");
    arrow(LIST.left);
    arrow(LIST.left);
    assert.equal(m.activePane(), "ce2ebf6f", "Left from the first wraps to the last");
    m.active().keydown(LIST.accept);
    await settle();
    assert.ok(m.drawer(), "Enter opened the drawer");
    assert.equal(m.drawer().getAttribute("data-drawer-pane"), "ce2ebf6f");
    m.drawer().keydown(LIST.cancel);
    await settle();
    assert.equal(m.drawer(), null, "Esc closed it");
    assert.equal(m.activePane(), "ce2ebf6f", "and the keyboard is back on the box");
  }));

test("in the page the next-needs-you key follows the strip", () =>
  withPage("04-eight-helpers-three-waiting", async (m) => {
    for (const want of ["160d47b0", "e8c9aa4e", "262e0b2b", "160d47b0"]) {
      assert.equal(press(m, "nextNeedsYou").defaultPrevented, true);
      await settle();
      assert.equal(m.activePane(), want);
    }
  }));

test("in the page the jump goes on from the open drawer's pane", () =>
  withPage("04-eight-helpers-three-waiting", async (m) => {
    m.box("e8c9aa4e").click();
    await settle();
    assert.ok(m.drawer());
    m.drawer().focus();
    press(m, "nextNeedsYou");
    await settle();
    assert.equal(m.activePane(), "262e0b2b", "the next after build, the open pane");
  }));

test("in the page the manager-bar key takes the keyboard to the bar's box", () =>
  withPage("04-eight-helpers-three-waiting", async (m) => {
    m.box("d1faf784").focus();
    assert.equal(press(m, "managerBar").defaultPrevented, true);
    await settle();
    assert.equal(m.active(), m.container.find(".mbar-input"));
  }));

test("with nothing that needs the person the jump leaves the keyboard where it is", () =>
  withPage("03-nested-helper-question-and-returned-task", async (m) => {
    m.box("025b0446").focus();
    press(m, "nextNeedsYou");
    await settle();
    assert.equal(m.activePane(), "025b0446");
  }, (g) => ({ ...g, teams: g.teams.map((t) => ({ ...t, needs: [] })) })));

test("when the project can no longer be read the jump does nothing", () =>
  withPage("04-eight-helpers-three-waiting", async (m) => {
    m.box("d1faf784").focus();
    m.ws().frame({ type: "teamGone", root: "/work/a", error: "gone" });
    await settle();
    press(m, "nextNeedsYou");
    await settle();
    assert.equal(m.container.find(".graph"), null, "the graph is gone");
    assert.notEqual(m.active(), m.container.find("main"), "the keyboard was not pulled to the main area");
  }));
