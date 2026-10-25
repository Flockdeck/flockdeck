// The drawer for one agent: its state as a function, its header, and its
// behaviour in a fake DOM beside the graph and the shell.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { render } from "../src/vendor/preact.mjs";
import { useMemo, useRef } from "../src/vendor/hooks.mjs";
import { html } from "../src/html.js";
import { Shell, keysOf } from "../src/shell/app.js";
import { Graph } from "../src/graph/Graph.js";
import { buildGraphModel } from "../src/graph/model.js";
import { Drawer, useDrawer, hasTerminal, keysFor } from "../src/drawer/Drawer.js";
import { Header, sinceOf, spendText, countsOf } from "../src/drawer/Header.js";
import { initialDrawer, reduceDrawer } from "../src/drawer/state.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeKeys, fakeState } from "./util.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver, fakeStore, paneState } from "./termfakes.mjs";

// ------------------------------------------------------------------- state

test("opening a pane shows it, and opening another replaces it", () => {
  let s = initialDrawer();
  s = reduceDrawer(s, { type: "open", pane: "p1" });
  assert.equal(s.pane, "p1");
  const same = reduceDrawer(s, { type: "open", pane: "p1" });
  assert.equal(same, s, "opening the open pane made a new state");
  s = reduceDrawer(s, { type: "open", pane: "p2" });
  assert.equal(s.pane, "p2");
  assert.equal(reduceDrawer(s, { type: "open", pane: "" }), s, "an empty pane id opened a drawer");
});

test("closing returns the keyboard to the pane, each time, and restores the size", () => {
  let s = reduceDrawer(initialDrawer(), { type: "open", pane: "p1" });
  s = reduceDrawer(s, { type: "toggleMax" });
  assert.equal(s.maximised, true);
  const closed = reduceDrawer(s, { type: "close" });
  assert.deepEqual({ ...closed }, { pane: "", via: "", maximised: false, returnTo: "p1", returnVia: "", seq: 1 });
  assert.equal(reduceDrawer(closed, { type: "close" }), closed, "closing a closed drawer asked for the keyboard again");
  assert.equal(reduceDrawer(closed, { type: "toggleMax" }), closed, "a closed drawer was maximised");
  const again = reduceDrawer(reduceDrawer(closed, { type: "open", pane: "p1" }), { type: "close" });
  assert.equal(again.seq, 2);
});

test("a drawer whose pane has left the graph closes, and one whose pane is there stays", () => {
  const s = reduceDrawer(initialDrawer(), { type: "open", pane: "p1" });
  assert.equal(reduceDrawer(s, { type: "panes", ids: new Set(["p1", "p2"]) }), s);
  const gone = reduceDrawer(s, { type: "panes", ids: new Set(["p2"]) });
  assert.equal(gone.pane, "");
  assert.equal(gone.returnTo, "");
});

// ------------------------------------------------------------------ header

const NOW = Date.parse("2026-10-20T09:20:00Z");
const node = (over = {}) => ({
  pane: "p2", kind: "terminal", structured: false, name: "shop-w1", role: "worker", agent: "claude", shell: false,
  status: "waiting", activity: "waiting", since: "2026-10-20T09:14:02Z", branch: "basket", dirty: 3, untracked: 1, ahead: 2, behind: 0,
  ...over,
});

test("the header carries name, role, agent, status and since, branch with its counts, model, spend and recording", () => {
  const pane = { model: "opus-5", recording: true, spend: { usd: 1.4, unpriced: true } };
  const out = renderToString(html`<${Header} node=${node()} pane=${pane} now=${NOW} />`);
  assert.match(out, />shop-w1</);
  assert.match(out, /<dd>worker · claude<\/dd>/);
  assert.match(out, /<dd>waiting for 5m<\/dd>/);
  assert.match(out, /<dd>basket · 3 changed, 1 new, 2 ahead<\/dd>/);
  assert.match(out, /<dd>opus-5<\/dd>/);
  assert.match(out, /<dd>\$1\.40 partial<\/dd>/);
  assert.match(out, /fact--recording/);
});

test("a header leaves out what the pane does not have", () => {
  const out = renderToString(html`<${Header} node=${node({ branch: "", agent: "", role: "" })} pane=${{}} now=${NOW} />`);
  assert.ok(!out.includes("fact--branch"));
  assert.ok(!out.includes("fact--model"));
  assert.ok(!out.includes("fact--spend"));
  assert.ok(!out.includes("fact--recording"));
  assert.ok(!out.includes("fact--who"));
});

test("a header draws text from an agent as text", () => {
  const evil = "<img src=x onerror=alert(1)>";
  const out = renderToString(html`<${Header} node=${node({ name: evil, branch: evil })} pane=${{ model: evil }} now=${NOW} />`);
  assert.ok(!out.includes("<img"), out);
  assert.match(out, /&lt;img src=x onerror=alert\(1\)>/);
});

test("since, spend and counts read as the old header did", () => {
  assert.equal(sinceOf("2026-10-20T09:19:30Z", NOW), "30s");
  assert.equal(sinceOf("2026-10-20T08:15:00Z", NOW), "1h 5m");
  assert.equal(sinceOf("2026-10-20T08:20:00Z", NOW), "1h");
  assert.equal(sinceOf("2026-10-17T09:20:00Z", NOW), "3d");
  assert.equal(sinceOf("not a time", NOW), "");
  assert.equal(sinceOf("2026-10-21T09:20:00Z", NOW), "", "a time in the future was given a length");
  assert.equal(spendText({ usd: 0.5 }), "$0.50");
  assert.equal(spendText({ usd: 0.5, unpriced: true }), "$0.50 partial");
  assert.equal(spendText({ unpriced: true }), "unpriced");
  assert.equal(spendText({ usd: Infinity }), "");
  assert.equal(spendText(null), "");
  assert.equal(countsOf(node({ dirty: 0, untracked: 0, ahead: 0, behind: 4 })), "4 behind");
});

// ------------------------------------------------------------ the component

const store = fakeStore({ hello: { type: "hello" }, state: paneState({ p1: {}, p2: {}, p3: { structured: true } }) });

test("a structured pane shows its own view and no terminal", () => {
  const out = renderToString(html`<${Drawer} store=${store} node=${node({ structured: true })} now=${NOW} />`);
  assert.ok(out.includes("data-structured-pane") || out.includes("Loading the agent."), "the structured view was not drawn");
  assert.ok(out.includes("structured</span>"), "the kind label was not drawn");
  assert.ok(!out.includes("terminal-pane"), "a terminal was made for a structured pane");
  assert.match(out, /role="tab"[^>]*aria-selected="true"[^>]*>Transcript</, "the first tab is the transcript");
  assert.ok(!/>Terminal</.test(out), "a structured pane offers a Terminal tab");
  assert.equal(hasTerminal(node({ structured: true }), null), false);
  assert.equal(hasTerminal(node(), { structured: true }), false, "the state push's word was ignored");
  assert.equal(hasTerminal(node(), {}), true);
});

test("a terminal pane has the Terminal tab and its terminal", () => {
  const out = renderToString(html`<${Drawer} store=${store} node=${node()} now=${NOW} />`);
  assert.match(out, /role="tab"[^>]*aria-selected="true"[^>]*>Terminal</);
  assert.match(out, /terminal-pane/);
  assert.ok(!out.includes("Loading the agent."));
});

test("keysOf gives the same list for a hello with no keys, and the hello's own when it has them", () => {
  assert.equal(keysOf({}), keysOf(null));
  assert.equal(keysOf({}), keysOf({ type: "hello" }), "a new empty list on each call remakes the bindings on each render");
  assert.equal(keysOf({ keys: fakeKeys }), fakeKeys);
});

// -------------------------------------------------------------- behaviour

const graphMsg = (over = {}) => ({
  type: "graph", root: "/work/b", project: "beta",
  teams: [{ id: "p1", manager: "p1", members: ["p1", "p2", "p3"], goal: "Basket", spendUSD: 1, spendPartial: false, needs: [] }],
  nodes: [
    { pane: "p1", kind: "terminal", depth: 0, team: "p1", children: ["p2", "p3"], name: "shop", role: "manager", agent: "claude", status: "working", activity: "working", since: "2026-10-20T09:10:00Z", branch: "main", ...over.p1 },
    { pane: "p2", kind: "terminal", parent: "p1", depth: 1, team: "p1", name: "shop-w1", role: "worker", agent: "claude", status: "waiting", activity: "waiting", since: "2026-10-20T09:14:02Z", branch: "basket", dirty: 3, ...over.p2 },
    { pane: "p3", kind: "structured", parent: "p1", depth: 1, team: "p1", name: "shop-w2", role: "worker", agent: "gemini", status: "idle", activity: "idle", branch: "tests", ...over.p3 },
  ],
  edges: [], solo: [], soloNeeds: [],
});

function App({ msg, store: st, dom, terminalOptions }) {
  const model = useMemo(() => buildGraphModel(msg), [msg]);
  const ids = useMemo(() => new Set(model.nodes.map((n) => n.pane)), [model]);
  const mainRef = useRef(null);
  const d = useDrawer({ mainRef, paneIds: ids });
  const n = d.pane ? model.byPane.get(d.pane) : null;
  const drawer = n
    ? html`<${Drawer} store=${st} node=${n} pane=${st.getState().state.panes[n.pane]} now=${NOW} maximised=${d.maximised}
        onClose=${d.close} onToggleMax=${d.toggleMax} terminalOptions=${terminalOptions} />`
    : null;
  return html`<${Shell} hello=${{ keys: fakeKeys }} state=${fakeState} window=${dom.window} mainRef=${mainRef} maximised=${d.maximised} drawer=${drawer}>
    <${Graph} model=${model} onOpen=${d.open} open=${d.pane} />
  <//>`;
}

async function mount(msg = graphMsg(), opts = {}) {
  PtySocket.reset();
  FakeTerm.reset();
  FakeObserver.instances.length = 0;
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const st = fakeStore({ hello: { type: "hello" }, state: paneState({ p1: {}, p2: { model: "opus-5" }, p3: { structured: true } }) });
  const terminalOptions = {
    Terminal: FakeTerm, FitAddon: FakeFit, ResizeObserver: FakeObserver, WebSocket: PtySocket,
    location: { href: "http://127.0.0.1:5000/", pathname: "/", protocol: "http:" }, isFocused: () => false,
  };
  const draw = (m) => render(html`<${App} msg=${m} store=${st} dom=${dom} terminalOptions=${terminalOptions} />`, container);
  draw(msg);
  await settle();
  const api = {
    dom, container, st,
    redraw: async (m) => { draw(m); await settle(); },
    box: (pane) => container.findAll("[data-pane]").find((e) => e.getAttribute("data-pane") === pane && e.localName === "g"),
    drawerBodies: () => container.findAll("[data-drawer-pane]"),
    drawerEl: () => container.find(".drawer"),
    name: () => { const e = container.find(".drawer-name"); return e ? e.textContent : ""; },
    done: async () => { render(null, container); await settle(); restore(); },
  };
  return api;
}

async function withApp(fn, msg, opts) {
  const a = await mount(msg, opts);
  try {
    await fn(a);
  } finally {
    await a.done();
  }
}

test("a click on a node opens the drawer for it, and the keyboard goes into the drawer", () =>
  withApp(async (a) => {
    assert.ok(a.drawerEl().attributes.hidden !== undefined, "the drawer showed with nothing in it");
    a.box("p2").click();
    await settle();
    assert.equal(a.name(), "shop-w1");
    assert.equal(a.drawerEl().attributes.hidden, undefined);
    assert.equal(a.dom.document.activeElement.getAttribute("data-drawer-pane"), "p2");
  }));

test("Enter or Space on a node opens its drawer from the keyboard", () =>
  withApp(async (a) => {
    const ev = a.box("p2").keydown("Enter");
    assert.equal(ev.defaultPrevented, true);
    await settle();
    assert.equal(a.name(), "shop-w1");
    a.box("p1").keydown(" ");
    await settle();
    assert.equal(a.name(), "shop");
    a.box("p1").keydown("a");
    await settle();
    assert.equal(a.name(), "shop", "another key opened another pane");
  }));

test("opening another node replaces the content, and there is one drawer", () =>
  withApp(async (a) => {
    a.box("p2").click();
    await settle();
    a.box("p1").click();
    await settle();
    assert.equal(a.drawerBodies().length, 1);
    assert.equal(a.name(), "shop");
    assert.equal(a.container.findAll(".drawer").length, 1);
    assert.equal(a.box("p1").getAttribute("aria-expanded"), "true");
    assert.equal(a.box("p2").getAttribute("aria-expanded"), "false");
    assert.equal(FakeTerm.instances.filter((t) => !t.disposed).length, 1, "the first pane's terminal was kept open");
  }));

test("Esc closes the drawer and the keyboard returns to the node", () =>
  withApp(async (a) => {
    a.box("p2").click();
    await settle();
    const body = a.drawerBodies()[0];
    const ev = body.keydown("Escape");
    assert.equal(ev.defaultPrevented, true);
    await settle();
    assert.equal(a.drawerBodies().length, 0);
    assert.ok(a.drawerEl().attributes.hidden !== undefined);
    assert.equal(a.dom.document.activeElement, a.box("p2"), "the keyboard did not go back to the node");
  }));

test("Esc typed into the terminal is the program's and does not close the drawer", () =>
  withApp(async (a) => {
    a.box("p2").click();
    await settle();
    const host = a.container.find(".terminal-host");
    assert.ok(host, "no terminal host");
    // xterm's own input is a textarea inside the host.
    const input = a.dom.document.createElement("textarea");
    host.appendChild(input);
    const ev = input.keydown("Escape");
    assert.equal(ev.defaultPrevented, false);
    await settle();
    assert.equal(a.drawerBodies().length, 1);
  }));

test("Esc from a button in the terminal pane (Try again, the size note) still closes the drawer", () =>
  withApp(async (a) => {
    a.box("p2").click();
    await settle();
    const button = a.dom.document.createElement("button");
    a.container.find(".terminal-pane").appendChild(button);
    const ev = button.keydown("Escape");
    assert.equal(ev.defaultPrevented, true);
    await settle();
    assert.equal(a.drawerBodies().length, 0);
  }));

test("a drawer given a node the graph just dropped draws nothing and does not throw", () => {
  assert.equal(renderToString(html`<${Drawer} store=${store} node=${null} now=${NOW} />`), "");
  assert.equal(renderToString(html`<${Drawer} store=${store} node=${undefined} now=${NOW} />`), "");
});

test("the Close button closes and returns the keyboard too", () =>
  withApp(async (a) => {
    a.box("p1").click();
    await settle();
    a.container.find(".drawer-close").click();
    await settle();
    assert.equal(a.drawerBodies().length, 0);
    assert.equal(a.dom.document.activeElement, a.box("p1"));
  }));

test("the header and the graph both follow a new push while the drawer is open", () =>
  withApp(async (a) => {
    a.box("p2").click();
    await settle();
    assert.match(a.container.find(".fact--status").textContent, /waiting/);
    assert.match(a.container.find(".fact--branch").textContent, /basket · 3 changed/);
    await a.redraw(graphMsg({ p2: { status: "working", activity: "working", branch: "basket-2", dirty: 0, ahead: 5 } }));
    assert.match(a.container.find(".fact--status").textContent, /working/);
    assert.match(a.container.find(".fact--branch").textContent, /basket-2 · 5 ahead/);
    // The graph behind it was redrawn too, and the drawer is still the same pane's.
    assert.match(a.box("p2").getAttribute("aria-label"), /working/);
    assert.equal(a.name(), "shop-w1");
    assert.equal(FakeTerm.instances.filter((t) => !t.disposed).length, 1, "the push remade the terminal");
  }));

test("a pane that leaves the graph closes its drawer", () =>
  withApp(async (a) => {
    a.box("p2").click();
    await settle();
    const msg = graphMsg();
    msg.nodes = msg.nodes.filter((n) => n.pane !== "p2");
    msg.teams[0].members = ["p1", "p3"];
    await a.redraw(msg);
    assert.equal(a.drawerBodies().length, 0);
  }));

test("a structured pane's drawer shows the structured view and opens no terminal or socket", () =>
  withApp(async (a) => {
    a.box("p3").click();
    await settle();
    assert.ok(a.container.find(".drawer-stub").textContent.includes("Loading the agent"));
    assert.equal(a.container.find(".terminal-pane"), null);
    assert.equal(FakeTerm.instances.length, 0);
    assert.equal(PtySocket.instances.length, 0);
  }));

test("maximise fills the main area and restore puts the drawer back, with no resize sent either way", () =>
  withApp(async (a) => {
    a.box("p2").click();
    await settle();
    const ws = PtySocket.last;
    assert.ok(ws, "no socket for the terminal");
    ws.open();
    const resizes = () => ws.plainJson.filter((m) => m.resize).length;
    const before = resizes();
    const shell = () => a.container.find(".shell");
    assert.ok(!shell().attributes.class.includes("shell--max"));
    a.container.find(".drawer-max").click();
    await settle();
    assert.ok(shell().attributes.class.includes("shell--max"));
    assert.equal(a.container.find(".drawer-max").textContent.trim(), "Restore");
    assert.equal(a.drawerBodies().length, 1);
    a.container.find(".drawer-max").click();
    await settle();
    assert.ok(!shell().attributes.class.includes("shell--max"));
    assert.equal(resizes(), before, "maximise or restore sent a resize");
    assert.equal(PtySocket.instances.length, 1, "maximise remade the socket");
    assert.equal(FakeTerm.instances.length, 1, "maximise remade the terminal");
  }));

test("closing a maximised drawer brings the graph back", () =>
  withApp(async (a) => {
    a.box("p2").click();
    await settle();
    a.container.find(".drawer-max").click();
    await settle();
    a.drawerBodies()[0].keydown("Escape");
    await settle();
    assert.ok(!a.container.find(".shell").attributes.class.includes("shell--max"));
    a.box("p2").click();
    await settle();
    assert.equal(a.container.find(".drawer-max").textContent.trim(), "Maximise", "the drawer reopened maximised");
  }));

test("the drawer remembers a strip row opened it, and says so after it closes", () => {
  let s = reduceDrawer(initialDrawer(), { type: "open", pane: "p1", via: "strip" });
  assert.equal(s.via, "strip");
  const closed = reduceDrawer(s, { type: "close" });
  assert.equal(closed.returnVia, "strip");
  assert.equal(closed.via, "");
  s = reduceDrawer(reduceDrawer(closed, { type: "open", pane: "p1" }), { type: "close" });
  assert.equal(s.returnVia, "", "a box opened it, so the keyboard goes back to the box");
  assert.equal(reduceDrawer(initialDrawer(), { type: "open", pane: "p1", via: "other" }).via, "");
});

test("keys are typed into the terminal of the pane asked for and no other", () => {
  const typed = [];
  const ref = { current: { pane: "p1", ctl: { sendKeys: (t, done) => { typed.push([t, done]); return true; } } } };
  const send = keysFor(ref, "p1");
  assert.equal(typeof send, "function");
  const done = () => {};
  assert.equal(send("y", done), true);
  assert.deepEqual(typed, [["y", done]]);
  // The drawer has moved to another pane and the new terminal is not made yet.
  assert.equal(keysFor(ref, "p2"), null, "a choice for p2 would be typed into p1");
  assert.equal(keysFor({ current: null }, "p1"), null);
  assert.equal(keysFor({ current: { pane: "p1", ctl: null } }, "p1"), null);
  assert.equal(keysFor(null, "p1"), null);
});

test("the Changes tab is there for a pane with a directory and not for one without", () => {
  const withDir = renderToString(html`<${Drawer} store=${store} node=${node()} pane=${{ cwd: "/work/b" }} now=${NOW} />`);
  assert.match(withDir, /role="tab"[^>]*id="drawer-tab-changes"[^>]*>Changes</);
  const without = renderToString(html`<${Drawer} store=${store} node=${node()} pane=${{}} now=${NOW} />`);
  assert.ok(!without.includes("drawer-tab-changes"));
  assert.ok(!renderToString(html`<${Drawer} store=${store} node=${node()} now=${NOW} />`).includes("drawer-tab-changes"));
});

test("a new ask for the Changes tab opens it, an old one does not, and a pane with no directory ignores it", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const quiet = { ...store, requestChanges() { return true; }, clearChanges() {} };
    const draw = (ask, pane) => render(html`<${Drawer} store=${quiet} node=${node()} pane=${pane} tabAsk=${ask} now=${NOW} />`, box);
    const selected = () => box.querySelector("#drawer-tab-changes").getAttribute("aria-selected");
    draw({ tab: "", n: 0 }, { cwd: "/work/b" });
    await settle();
    assert.notEqual(selected(), "true");
    draw({ tab: "changes", n: 1 }, { cwd: "/work/b" });
    await settle();
    assert.equal(selected(), "true");
    draw({ tab: "changes", n: 1 }, {});
    await settle();
    assert.equal(box.querySelector("#drawer-tab-changes"), null);
  } finally {
    restore();
  }
});

// ------------------------------------------- the message box and stepping

test("a terminal pane has a message box that sends paneSend by its id, and a structured pane keeps its own", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const st = fakeStore({ hello: { type: "hello" }, state: paneState({ p2: {} }) });
    render(html`<${Drawer} store=${st} node=${node()} now=${NOW} />`, box);
    await settle();
    const ta = box.querySelector(".structured-input");
    assert.ok(ta, "no message box on a terminal pane");
    assert.match(ta.getAttribute("aria-label"), /shop-w1/);
    const send = box.querySelector(".structured-send");
    assert.equal(send.disabled, true, "an empty box can send");
    ta.enter("  run the tests  ");
    await settle();
    box.querySelector(".drawer-message").dispatch("submit");
    await settle();
    assert.deepEqual(st.sent.filter((c) => c.cmd === "paneSend").map(({ req, ...c }) => c), [{ cmd: "paneSend", id: "p2", text: "run the tests" }]);
    assert.equal(box.querySelector(".structured-input").value, "  run the tests  ", "the draft went before the desk answered");
    st.set({ sent: { seq: 1, id: "p2" } });
    await settle();
    assert.equal(box.querySelector(".structured-input").value, "", "the draft stayed after the desk took the text");
    render(null, box);
    const out = renderToString(html`<${Drawer} store=${store} node=${node({ pane: "p3", structured: true })} now=${NOW} />`);
    assert.equal((out.match(/Message [^"]* directly/g) || []).length, 0, "a structured pane got a second box");
  } finally {
    restore();
  }
});

test("a shell has no message box: the desk refuses text into a shell", () => {
  assert.ok(!renderToString(html`<${Drawer} store=${store} node=${node({ shell: true })} now=${NOW} />`).includes("drawer-message"));
});

test("a message box is not drawn through the relay", () => {
  const remote = fakeStore({ hello: { type: "hello" }, remote: true, state: paneState({ p2: {} }) });
  assert.ok(!renderToString(html`<${Drawer} store=${remote} node=${node()} now=${NOW} />`).includes("drawer-message"));
});

test("[ and ] open the previous and next agent of the order, wrapping, and a box that has the keyboard keeps them", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const opened = [];
    const st = fakeStore({ hello: { type: "hello" }, state: paneState({ p2: {} }) });
    render(html`<${Drawer} store=${st} node=${node()} now=${NOW} order=${["p1", "p2", "p3"]} onOpenPane=${(p) => opened.push(p)} />`, box);
    await settle();
    const body = box.querySelector("section");
    body.keydown("]");
    body.keydown("[");
    assert.deepEqual(opened, ["p3", "p1"]);
    box.querySelector(".drawer-next").click();
    box.querySelector(".drawer-prev").click();
    assert.deepEqual(opened, ["p3", "p1", "p3", "p1"]);
    const ta = box.querySelector(".structured-input");
    ta.keydown("]");
    assert.equal(opened.length, 4, "a key typed in the message box stepped to another agent");
    render(html`<${Drawer} store=${st} node=${node()} now=${NOW} order=${["p2"]} onOpenPane=${(p) => opened.push(p)} />`, box);
    await settle();
    assert.equal(box.querySelector(".drawer-next"), null, "a single agent has nothing to step to");
  } finally {
    restore();
  }
});

test("a drawer opened by the ask for its own Changes tab opens on it; another pane's ask is old", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const quiet = { ...store, requestChanges() { return true; }, clearChanges() {} };
    const selected = (box) => box.querySelector("#drawer-tab-changes").getAttribute("aria-selected");
    const a = dom.document.createElement("div");
    dom.body.appendChild(a);
    render(html`<${Drawer} store=${quiet} node=${node()} pane=${{ cwd: "/work/b" }} tabAsk=${{ tab: "changes", n: 3, pane: "p2" }} now=${NOW} />`, a);
    await settle();
    assert.equal(selected(a), "true");
    const b = dom.document.createElement("div");
    dom.body.appendChild(b);
    render(html`<${Drawer} store=${quiet} node=${node()} pane=${{ cwd: "/work/b" }} tabAsk=${{ tab: "changes", n: 3, pane: "p9" }} now=${NOW} />`, b);
    await settle();
    assert.notEqual(selected(b), "true");
  } finally {
    restore();
  }
});

test("the message box keeps the draft and says so when the desk refuses the text, and sends it as one line", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const st = fakeStore({ hello: { type: "hello" }, connection: "open", state: paneState({ p2: {} }) });
    render(html`<${Drawer} store=${st} node=${node()} now=${NOW} />`, box);
    await settle();
    const ta = () => box.querySelector(".structured-input");
    ta().enter("line one\nline two");
    await settle();
    box.querySelector(".drawer-message").dispatch("submit");
    await settle();
    assert.equal(st.sent.filter((c) => c.cmd === "paneSend")[0].text, "line one line two");
    assert.equal(ta().getAttribute("aria-busy"), "true", "the box waits for the desk");
    box.querySelector(".drawer-message").dispatch("submit");
    await settle();
    assert.equal(st.sent.filter((c) => c.cmd === "paneSend").length, 1, "a second submit while sending went out");
    st.set({ notice: { seq: 1, error: true, kind: "send", text: "p2 is not taking text now." } });
    await settle();
    assert.equal(ta().value, "line one\nline two", "a refused text was thrown away");
    assert.match(box.querySelector(".structured-note--unsent").textContent, /not taking text/);
    assert.equal(ta().getAttribute("aria-busy"), null);
    ta().enter("line one line two!");
    await settle();
    assert.equal(box.querySelector(".structured-note--unsent"), null, "the refusal stayed after the text changed");
    render(null, box);
  } finally {
    restore();
  }
});

test("a viewer's drawer has no message box", () => {
  const viewer = fakeStore({ hello: { type: "hello", role: "viewer" }, state: paneState({ p2: {} }) });
  assert.ok(!renderToString(html`<${Drawer} store=${viewer} node=${node()} now=${NOW} />`).includes("drawer-message"));
});
