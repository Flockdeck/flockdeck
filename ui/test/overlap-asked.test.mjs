// A file overlap the manager was asked about: kept per pair, quiet while no new
// file joins it, out of the needs-you count, and sent again only by Ask again.
import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { useStore } from "../src/lib/use-store.js";
import { buildGraphModel, isLoud } from "../src/graph/model.js";
import { Decisions } from "../src/graph/DecisionBlock.js";
import { chipsOf } from "../src/graph/chips.js";
import { NeedsYou } from "../src/graph/NeedsYou.js";
import { overlapFiles, isCovered, markAsked, clearAsked, askedRecords, resetAskedForTest } from "../src/graph/overlapAsked.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const node = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, name: pane, status: "idle", activity: "idle", waitingOn: { kind: "none" }, ...over });
const conflict = (text) => ({ id: "conflict:api:cfg", kind: "conflict", pane: "api", other: "cfg", time: new Date(Date.now() - 120000).toISOString(), text });
const graph = (needs) => ({
  type: "graph", root: "C:/repo", project: "repo",
  teams: [{ id: "t1", manager: "m", members: ["m", "api", "cfg"], goal: "g", needs }],
  nodes: [node("m", { name: "mgr", team: "t1", children: ["api", "cfg"] }), node("api", { team: "t1", parent: "m" }), node("cfg", { team: "t1", parent: "m" })],
  edges: [], solo: [], soloNeeds: [],
});

beforeEach(() => resetAskedForTest());

test("overlapFiles reads the radar's text and isCovered notices a new file", () => {
  assert.deepEqual(overlapFiles("a.go, b/c.go and 3 more"), { paths: ["a.go", "b/c.go"], more: 3 });
  assert.deepEqual(overlapFiles(""), { paths: [], more: 0 });
  const rec = { paths: ["a.go", "b.go"], more: 0 };
  assert.ok(isCovered(rec, "b.go, a.go"));
  assert.ok(isCovered(rec, "a.go"), "fewer files is still covered");
  assert.ok(!isCovered(rec, "a.go, b.go, c.go"), "a new file is not");
  assert.ok(!isCovered(rec, "a.go, b.go and 2 more"), "more files behind the cap are not");
  assert.ok(!isCovered(undefined, "a.go"));
});

test("an asked overlap is not counted as needing the person, and comes back when it grows", () => {
  const g = graph([conflict("a.go, b.go")]);
  assert.equal(buildGraphModel(g).needs.length, 1);
  markAsked("conflict:api:cfg", "a.go, b.go");
  const quiet = buildGraphModel(g);
  assert.equal(quiet.needs.length, 0);
  assert.equal(quiet.teams[0].needs.length, 0);
  assert.ok(quiet.needsByPane.get("api")[0].asked, "the pane still carries it, for the chip and the block");
  assert.ok(!isLoud(quiet.needsByPane.get("api")));
  assert.match(chipsOf(quiet, quiet.byPane.get("api"), quiet.needsByPane.get("api"), Date.now()).find((c) => c.kind === "overlap").text, /^waiting on the workers: overlaps cfg/);
  const grown = buildGraphModel(graph([conflict("a.go, b.go, c.go")]));
  assert.equal(grown.needs.length, 1);
  assert.ok(isLoud(grown.needsByPane.get("api")));
});

test("the strip drops an asked overlap from its count", () => {
  markAsked("conflict:api:cfg", "a.go");
  const model = buildGraphModel(graph([conflict("a.go")]));
  const dom = createDom();
  const restore = install(dom);
  try {
    const c = dom.document.createElement("div");
    dom.body.appendChild(c);
    render(html`<${NeedsYou} model=${model} />`, c);
    assert.match(c.textContent, /Nothing needs you/);
    render(null, c);
  } finally {
    restore();
  }
});

test("the record is stored per pair and can be cleared", () => {
  markAsked("conflict:api:cfg", "a.go and 2 more");
  assert.deepEqual(askedRecords()["conflict:api:cfg"].paths, ["a.go"]);
  assert.equal(askedRecords()["conflict:api:cfg"].more, 2);
  clearAsked("conflict:api:cfg");
  assert.deepEqual(askedRecords(), {});
});

async function mount(model, ctxOver = {}) {
  const h = await harness();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  h.store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello());
  function App() {
    const snap = useStore(h.store);
    return html`<${Decisions} model=${model} pane="api" ctx=${{ snap, store: h.store, manager: { pane: "m", name: "mgr" }, ...ctxOver }} />`;
  }
  render(html`<${App} />`, container);
  await settle();
  return { ...h, ws, container, remount: async () => { render(null, container); await settle(); render(html`<${App} />`, container); await settle(); }, done: async () => { render(null, container); await settle(); restore(); } };
}

test("after the ask the block is a quiet chip that survives a remount, and a second send is Ask again", async () => {
  const model = buildGraphModel(graph([conflict("a.go, b.go")]));
  const a = await mount(model);
  try {
    const block = () => a.container.find('[data-decision="conflict"]');
    block().find(".dec-btn--primary").click();
    await settle();
    const sent = a.ws.commands("paneSend");
    assert.equal(sent.length, 1);
    assert.equal(askedRecords()["conflict:api:cfg"], undefined, "nothing is recorded until the desk takes the line");
    a.ws.frame({ type: "paneSend", id: "m", queued: true, shell: false, req: sent[0].req });
    await settle();
    assert.deepEqual(askedRecords()["conflict:api:cfg"].paths, ["a.go", "b.go"]);
    assert.match(block().textContent, /Manager asked, waiting on the workers/);
    await a.remount();
    assert.match(block().textContent, /Manager asked, waiting on the workers/, "a remount keeps it");
    assert.ok(!block().findAll(".dec-btn").some((x) => /sort it out/.test(x.textContent)));
    assert.equal(a.ws.commands("paneSend").length, 1, "the remount sent nothing");
  } finally {
    await a.done();
  }
});

test("a recorded ask draws the quiet block on a fresh mount and sends nothing until Ask again", async () => {
  markAsked("conflict:api:cfg", "a.go, b.go");
  const model = buildGraphModel(graph([conflict("a.go, b.go")]));
  const a = await mount(model);
  try {
    const block = a.container.find('[data-decision="conflict"]');
    assert.equal(block.getAttribute("data-asked"), "true");
    assert.match(block.textContent, /Manager asked, waiting on the workers/);
    assert.ok(!block.findAll(".dec-btn").some((b) => /sort it out/.test(b.textContent)), "the first-ask button is gone");
    assert.equal(a.ws.commands("paneSend").length, 0);
    block.findAll(".dec-btn").find((b) => b.textContent === "Ask again").click();
    await settle();
    assert.equal(a.ws.commands("paneSend").length, 1);
  } finally {
    await a.done();
  }
});

test("a recorded ask does not cover a file that joined the overlap", async () => {
  markAsked("conflict:api:cfg", "a.go");
  const model = buildGraphModel(graph([conflict("a.go, z.go")]));
  const a = await mount(model);
  try {
    const block = a.container.find('[data-decision="conflict"]');
    assert.equal(block.getAttribute("data-asked"), null);
    assert.ok(block.findAll(".dec-btn").some((b) => /sort it out/.test(b.textContent)));
  } finally {
    await a.done();
  }
});
