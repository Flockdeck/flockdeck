// The keyboard in the team view: the jump to the next agent that needs the person and the manager bar, and that
// every one of them works with no pointer in the real page.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { LIST } from "../src/shell/keys.js";
import { buildGraphModel } from "../src/graph/model.js";
import { nextNeedsYou, prevNeedsYou, paneAt } from "../src/graph/keys.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { realKeys } from "./util.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures");
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

test("the jump goes in the strip's order, not the picture's, and wraps", () => {
  const m = model(load("04-eight-helpers-three-waiting"));
  // The strip: notes, build, qa. The boxes: build, notes, qa.
  assert.equal(nextNeedsYou(m, ""), "160d47b0");
  assert.equal(nextNeedsYou(m, "160d47b0"), "e8c9aa4e");
  assert.equal(nextNeedsYou(m, "e8c9aa4e"), "262e0b2b");
  assert.equal(nextNeedsYou(m, "262e0b2b"), "160d47b0", "past the last is the first");
  assert.equal(prevNeedsYou(m, ""), "262e0b2b", "from nowhere the previous is the last");
  assert.equal(prevNeedsYou(m, "262e0b2b"), "e8c9aa4e");
  assert.equal(prevNeedsYou(m, "e8c9aa4e"), "160d47b0");
  assert.equal(prevNeedsYou(m, "160d47b0"), "262e0b2b", "before the first is the last");
  assert.equal(prevNeedsYou(m, "not-a-pane"), "262e0b2b");
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

test("paneAt finds the pane of an element inside a box", () => {
  const el = (id, parentNode) => ({ parentNode, getAttribute: (k) => (k === "data-pane" ? id : null) });
  const box = el("a", null);
  assert.equal(paneAt(el("", box)), "a");
  assert.equal(paneAt(null), "");
});

// --- in the page ---------------------------------------------------------

const FALLBACK = [
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Finding your way", noPalette: true },
  { id: "nextNeedsYou", keys: "Ctrl+Shift+U", label: "Next agent that needs you", section: "Finding your way" },
  { id: "prevNeedsYou", keys: "Ctrl+Shift+J", label: "Previous agent that needs you", section: "Finding your way" },
  { id: "managerBar", keys: "Ctrl+Shift+H", label: "Message the manager", section: "Finding your way" },
];
const keysFor = () => {
  const real = realKeys();
  if (!real) return FALLBACK;
  for (const id of ["nextNeedsYou", "prevNeedsYou", "managerBar"]) assert.ok(real.some((k) => k.id === id && k.keys), id + " is in the key table with a binding");
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
  // A fixed pause is not enough on a slow runner (the first page test on macOS
  // CI found no boxes after 160 ms), so wait for the graph, up to 10 s.
  for (let i = 0; i < 500 && container.findAll("[data-pane]").length === 0; i++) await settle(20);
  if (container.findAll("[data-pane]").length === 0) {
    render(null, container);
    store.stop();
    restore();
    throw new Error("the graph did not draw");
  }
  return {
    dom, container, ws,
    box: (pane) => container.findAll("[data-pane]").find((e) => e.getAttribute("data-pane") === pane && String(e.className || e.getAttribute("class")).includes("tree-row")),
    active: () => dom.document.activeElement,
    activePane: () => dom.document.activeElement.getAttribute("data-pane"),
    drawer: () => container.find("[data-drawer-pane]"),
    // The pane whose drawer the jump opened.
    landed: () => { const d = container.find("[data-drawer-pane]"); return d ? d.getAttribute("data-drawer-pane") : null; },
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

test("in the page the next-needs-you key follows the strip and opens each agent's drawer", () =>
  withPage("04-eight-helpers-three-waiting", async (m) => {
    for (const want of ["160d47b0", "e8c9aa4e", "262e0b2b", "160d47b0"]) {
      assert.equal(press(m, "nextNeedsYou").defaultPrevented, true);
      await settle();
      assert.equal(m.landed(), want);
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
    assert.equal(m.landed(), "262e0b2b", "the next after build, the open pane");
  }));

test("in the page the previous key walks the strip the other way and wraps", () =>
  withPage("04-eight-helpers-three-waiting", async (m) => {
    for (const want of ["262e0b2b", "e8c9aa4e", "160d47b0", "262e0b2b"]) {
      assert.equal(press(m, "prevNeedsYou").defaultPrevented, true);
      await settle();
      assert.equal(m.landed(), want);
    }
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
    assert.notEqual(m.active(), m.container.find("main"), "the keyboard was not pulled to the main area");
  }));

test("both graph actions are in the palette, and running one from it works", () =>
  withPage("04-eight-helpers-three-waiting", async (m) => {
    press(m, "palette");
    await settle();
    const row = (id) => m.container.findAll("li").find((e) => e.getAttribute("data-action") === id);
    assert.ok(row("managerBar"), "Message the manager is not in the palette");
    assert.equal(row("managerBar").getAttribute("aria-disabled"), null, "it is marked as not in this window");
    assert.equal(row("nextNeedsYou").getAttribute("aria-disabled"), null);
    row("nextNeedsYou").click();
    await settle();
    assert.equal(m.landed(), "160d47b0", "running it from the palette did not go to the first agent that needs you");
  }));

test("an action chosen from the palette runs once: opening and closing the palette again does not run it again", () =>
  withPage("04-eight-helpers-three-waiting", async (m) => {
    const row = (id) => m.container.findAll("li").find((e) => e.getAttribute("data-action") === id);
    press(m, "palette");
    await settle();
    row("nextNeedsYou").click();
    await settle();
    assert.equal(m.landed(), "160d47b0");
    m.box("d1faf784").focus();
    press(m, "palette");
    await settle();
    m.active().keydown("Escape");
    await settle();
    assert.equal(m.activePane(), "d1faf784", "closing the palette ran the earlier choice again");
  }));
