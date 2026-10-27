// The manager bar on the real page: the store on a fake socket, the Live shell
// over it, and the bar in the strip along the top.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render, options } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { ManagerBar, NO_MANAGER_WORDS, EMPTY_WORDS } from "../src/graph/ManagerBar.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeKeys } from "./util.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures");
const fixture = (name, root = "/work/a") => ({ ...JSON.parse(readFileSync(join(dir, name + ".json"), "utf8")), root });
const F1 = "01-six-helpers-one-permission";
const MANAGER = "0c96cbe9"; // the team's manager in fixture 1

const projects = [
  { root: "/work/a", name: "alpha", waiting: 0, working: 0 },
  { root: "/work/b", name: "beta", waiting: 0, working: 0 },
];
const stateMsg = (root = "/work/a") => ({ type: "state", root, projects, panes: {} });
const solo = (root) => ({ type: "graph", root, project: "p", teams: [], nodes: [{ pane: "s1", kind: "terminal", name: "s1", status: "idle", activity: "idle", waitingOn: { kind: "none" } }], edges: [], solo: ["s1"], soloNeeds: [] });

async function mount() {
  const dom = createDom();
  const restore = install(dom);
  const { store } = await harness();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  const ws = () => FakeWebSocket.last;
  render(html`<${Live} store=${store} window=${dom.window} />`, container);
  await settle();
  const api = {
    dom, store, container, ws,
    connect: async (over = {}) => { ws().open(); ws().frame(hello(over)); await settle(); },
    push: async (msg) => { ws().frame(msg); await settle(); },
    top: () => container.find(".top"),
    input: () => container.find(".mbar-input"),
    status: () => (container.find(".mbar-status--error") || container.find(".mbar-status") || { textContent: "" }).textContent,
    sends: () => ws().commands("paneSend"),
    done: () => { render(null, container); store.stop(); restore(); },
  };
  return api;
}

test("there is no bar until the graph of the project has been read", async () => {
  const m = await mount();
  assert.equal(m.container.find(".mbar"), null);
  await m.connect();
  await m.push(stateMsg());
  assert.equal(m.container.find(".mbar"), null, "the bar was drawn while the graph was being read");
  assert.match(m.top().textContent, /alpha/, "the strip lost its title");
  await m.push(fixture(F1));
  assert.ok(m.container.find(".composer").find(".mbar"), "no bar in the bottom bar");
  m.done();
});

test("text typed in the bar goes out as paneSend to the manager's id, exactly", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture(F1));
  const text = "  /status then !ls  ";
  m.input().enter(text);
  await settle();
  m.input().keydown("Enter");
  await settle();
  assert.deepEqual(m.sends().map(({ req, ...c }) => c), [{ cmd: "paneSend", id: MANAGER, text }]);
  await m.push({ type: "paneSend", id: MANAGER, queued: true, shell: false });
  assert.equal(m.status(), "Sent. The manager reads it when its turn ends.");
  m.done();
});

test("each refusal of the desk shows as its notice", async () => {
  const refusals = [
    "That pane is no longer open",
    "That pane's process has exited, so the text was not sent. Restart the pane and send it again",
    "That pane is a shell, and a shell runs whatever it is sent, so the text was not sent",
    "That pane is waiting on a permission prompt, which is answered with its choice buttons and not by text, so the text was not sent",
    "The pane is still taking in the last text sent to it, so this was not sent. Try again in a moment",
    "That pane already has as many prompts waiting as it takes, so the text was not sent. Try again when it has worked through them",
  ];
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture(F1));
  for (const words of refusals) {
    m.input().enter("go");
    await settle();
    m.input().keydown("Enter");
    await settle();
    await m.push({ type: "notice", text: words, error: true });
    assert.equal(m.status(), words);
  }
  assert.equal(m.sends().length, refusals.length);
  // The desk's wording for an empty text, stopped in the page.
  m.input().enter("   ");
  await settle();
  m.input().keydown("Enter");
  await settle();
  assert.equal(m.status(), EMPTY_WORDS);
  assert.equal(m.sends().length, refusals.length);
  m.done();
});

test("the bar is the default place the keyboard goes, once the graph names a manager", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  assert.notEqual(m.dom.document.activeElement, m.input());
  await m.push(fixture(F1));
  assert.equal(m.dom.document.activeElement, m.input());
  // A later push does not take it back.
  m.input().blur();
  await m.push(fixture(F1));
  assert.notEqual(m.dom.document.activeElement, m.input());
  m.done();
});

test("the region keys still move the keyboard from the bar to the rail and the main area", async () => {
  const m = await mount();
  await m.connect({ keys: fakeKeys });
  await m.push(stateMsg());
  await m.push(fixture(F1));
  assert.equal(m.dom.document.activeElement, m.input());
  const f6 = () => m.dom.window.dispatch("keydown", { key: "F6", ctrlKey: false, shiftKey: false, altKey: false });
  f6();
  await settle();
  const rail = m.container.find(".rail");
  assert.ok(rail.contains(m.dom.document.activeElement), "F6 from the bar did not reach the rail");
  f6();
  await settle();
  assert.equal(m.dom.document.activeElement, m.container.find(".main"), "the second F6 did not reach the main area");
  m.done();
});

test("a project with no manager has no bottom bar at all", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(solo("/work/a"));
  assert.equal(m.container.find(".mbar"), null);
  assert.equal(m.container.find(".composer"), null, "no empty strip either");
  assert.equal(m.sends().length, 0);
  m.done();
});

test("changing project drops the bar until the new project's graph is read, and it then sends to the new manager", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture(F1));
  const rail = m.container.findAll("button").find((b) => b.attributes["data-root"] === "/work/b");
  rail.click();
  await settle();
  assert.equal(m.container.find(".mbar"), null);
  // The old project's push, still in flight, does not bring it back.
  await m.push(fixture(F1, "/work/a"));
  assert.equal(m.container.find(".mbar"), null);
  const b = fixture("02-eight-helpers-permission-and-question", "/work/b");
  await m.push(b);
  m.input().enter("hi");
  await settle();
  m.input().keydown("Enter");
  await settle();
  assert.deepEqual(m.sends().map((c) => c.id), [b.teams[0].manager]);
  m.done();
});

test("a project that can no longer be read has no bar", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture(F1));
  await m.push({ type: "teamGone", root: "/work/a", error: "gone" });
  assert.equal(m.container.find(".mbar"), null);
  m.done();
});

test("a window through the relay shows no bar and sends no paneSend", async () => {
  const m = await mount();
  await m.connect({ remote: true });
  await m.push(stateMsg());
  await m.push(fixture(F1));
  assert.equal(m.container.find(".mbar"), null);
  assert.match(m.top().textContent, /alpha/);
  assert.equal(m.sends().length, 0);
  m.done();
});

test("after a switch the bar is never drawn with the old project's manager, even for one frame", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture(F1));
  assert.ok(m.input());
  // Every commit of the bar, with the props it was given. A commit with the
  // props it had before is Preact skipping an unchanged bar, not a new draw.
  const drawn = [];
  const before = options.diffed;
  let known = null;
  options.diffed = (vnode) => {
    if (vnode.type === ManagerBar) drawn.push(vnode.props);
    if (before) before(vnode);
  };
  try {
    await m.push(fixture(F1));
    known = drawn[drawn.length - 1];
    drawn.length = 0;
    const rail = m.container.findAll("button").find((b) => b.attributes["data-root"] === "/work/b");
    rail.click();
    await settle();
  } finally {
    options.diffed = before;
  }
  const fresh = drawn.filter((p) => p !== known).map((p) => p.manager && p.manager.pane);
  assert.ok(!fresh.includes(MANAGER), "the bar was drawn for project B with project A's manager");
  assert.equal(m.container.find(".mbar"), null);
  m.done();
});

test("the box keeps the keyboard while a send is open", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture(F1));
  m.input().enter("hi");
  await settle();
  m.input().keydown("Enter");
  await settle();
  assert.equal(m.sends().length, 1);
  assert.equal(m.dom.document.activeElement, m.input());
  // A browser drops the keyboard from a focused input that becomes disabled (the
  // fake document does not), so the box must be read-only, not disabled.
  assert.equal(m.input().attributes.disabled, undefined, "the box was disabled while a send was open");
  m.done();
});
