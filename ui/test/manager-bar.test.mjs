// The manager bar: what it sends, what it shows of the answer, and when it is off.
// The store runs on a fake socket, so the commands are the ones the desk would get.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { useStore } from "../src/lib/use-store.js";
import { buildGraphModel } from "../src/graph/model.js";
import {
  ManagerBar, managerOf, checkText, sentWords, SENT_SHOWN,
  MAX_BYTES, EMPTY_WORDS, TOO_LONG_WORDS, NO_MANAGER_WORDS, NOT_CONNECTED_WORDS, LOST_WORDS, outcomeOf,
} from "../src/graph/ManagerBar.js";
import { VIEWER_WORDS } from "../src/lib/role.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
// The fake DOM keeps a boolean attribute as an attribute, a browser as a property.
const ro = (el) => el.readOnly === true || (el.attributes && (el.attributes.readOnly !== undefined || el.attributes.readonly !== undefined));
const off = (el) => el.disabled === true || (el.attributes && el.attributes.disabled !== undefined && el.attributes.disabled !== "false");

const node = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, name: pane, status: "idle", activity: "idle", waitingOn: { kind: "none" }, ...over });
const teamGraph = (manager = "p1") => ({
  type: "graph", root: "C:/repo", project: "repo",
  teams: [{ id: "t1", manager, members: ["p1", "p2"], goal: "g", needs: [] }],
  nodes: [node("p1", { name: "Lead", team: "t1", children: ["p2"] }), node("p2", { name: "Helper", team: "t1", parent: "p1" })],
  edges: [], solo: [], soloNeeds: [],
});
const soloGraph = () => ({ type: "graph", root: "C:/repo", project: "repo", teams: [], nodes: [node("p1"), node("p2")], edges: [], solo: ["p1", "p2"], soloNeeds: [] });

// Every refusal in docs/team-graph.md section 6, in the desk's words
// (internal/workspace/panesend.go).
const REFUSALS = [
  "That pane is no longer open",
  "That pane's process has exited, so the text was not sent. Restart the pane and send it again",
  "That pane is a shell, and a shell runs whatever it is sent, so the text was not sent",
  "That pane is waiting on a permission prompt, which is answered with its choice buttons and not by text, so the text was not sent",
  "The pane is still taking in the last text sent to it, so this was not sent. Try again in a moment",
  "That pane already has as many prompts waiting as it takes, so the text was not sent. Try again when it has worked through them",
];

let shownFor = SENT_SHOWN;
function App({ store, model }) {
  const snap = useStore(store);
  return html`<${ManagerBar} snap=${snap} manager=${managerOf(model)} send=${(c) => store.send(c)} shownFor=${shownFor} />`;
}

async function mount(graph = teamGraph(), { open = true, helloOver = {} } = {}) {
  const h = await harness();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  h.store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  if (open) ws.frame(hello(helloOver));
  const model = buildGraphModel(graph);
  render(html`<${App} store=${h.store} model=${model} />`, container);
  await settle();
  const api = {
    ...h, ws, dom, container, model,
    input: () => container.find(".mbar-input"),
    send: () => container.find(".mbar-send"),
    status: () => (container.find(".mbar-status") || { textContent: "" }).textContent,
    state: () => (container.find(".mbar") || { getAttribute: () => null }).getAttribute("data-state"),
    type: async (text) => { api.input().enter(text); await settle(); },
    submit: async () => { api.send().click(); await settle(); },
    frame: async (m) => { ws.frame(m); await settle(); },
    sends: () => ws.commands("paneSend"),
    redraw: async (m) => { render(html`<${App} store=${h.store} model=${m} />`, container); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); },
  };
  return api;
}

async function withBar(fn, graph, opts) {
  const a = await mount(graph, opts);
  try {
    await fn(a);
  } finally {
    await a.done();
  }
}

test("managerOf is the first team's manager when it is a node, and null otherwise", () => {
  assert.deepEqual(managerOf(buildGraphModel(teamGraph())), { pane: "p1", name: "Lead" });
  assert.equal(managerOf(buildGraphModel(soloGraph())), null);
  assert.equal(managerOf(buildGraphModel(teamGraph("p9"))), null, "a manager that is not a node");
  assert.equal(managerOf(null), null);
  assert.equal(managerOf(buildGraphModel({ type: "nope" })), null);
});

test("sending reaches paneSend with the manager's id and the exact text, and no allowShell", () =>
  withBar(async (a) => {
    const text = "  /review the diff, then !ls  \tend  ";
    await a.type(text);
    await a.submit();
    assert.deepEqual(a.sends().map(({ req, ...c }) => c), [{ cmd: "paneSend", id: "p1", text }]);
    assert.match(a.sends()[0].req, /^ps/, "the send carries a token the desk can echo");
    assert.ok(!("allowShell" in a.sends()[0]));
  }));

test("Enter sends, Shift+Enter does not, and a key in an input method does not", () =>
  withBar(async (a) => {
    await a.type("hello");
    a.input().keydown("Enter", { shiftKey: true });
    a.input().keydown("Enter", { isComposing: true });
    a.input().keydown("Enter", { keyCode: 229 });
    await settle();
    assert.equal(a.sends().length, 0);
    const ev = a.input().keydown("Enter");
    await settle();
    assert.equal(a.sends().length, 1);
    assert.ok(ev.defaultPrevented);
  }));

test("a queued send says the manager reads it when its turn ends, and the box is cleared", () =>
  withBar(async (a) => {
    await a.type("do the thing");
    await a.submit();
    assert.equal(a.state(), "sending");
    assert.ok(ro(a.input()) && off(a.send()), "the bar took a second send while one was open");
    await a.frame({ type: "paneSend", id: "p1", queued: true, shell: false });
    assert.equal(a.status(), "Sent. The manager reads it when its turn ends.");
    assert.equal(a.state(), "sent");
    assert.equal(a.input().value, "");
  }));

test("the line about an accepted send goes after a few seconds", () =>
  withBar(async (a) => {
    shownFor = 120;
    try {
      await a.type("do the thing");
      await a.submit();
      await a.frame({ type: "paneSend", id: "p1", queued: true, shell: false });
      assert.equal(a.state(), "sent");
      await settle(400);
      assert.equal(a.state(), "idle");
      assert.equal(a.status(), "");
    } finally {
      shownFor = SENT_SHOWN;
    }
  }));

test("a line about a send stays for SENT_SHOWN by default", () => {
  assert.ok(SENT_SHOWN >= 3000 && SENT_SHOWN <= 10000);
});

test("the other answers: plain, waiting on a question, routed", () => {
  assert.equal(sentWords({ queued: false }), "Sent.");
  assert.match(sentWords({ queued: false, waiting: true }), /waiting on a question.*answer/);
  assert.match(sentWords({ queued: false, routed: true }), /another model/);
  return withBar(async (a) => {
    await a.type("answer");
    await a.submit();
    await a.frame({ type: "paneSend", id: "p1", queued: false, shell: false, waiting: true });
    assert.match(a.status(), /waiting on a question/);
  });
});

for (const words of REFUSALS) {
  test(`a refusal shows its notice and keeps the text: ${words.slice(0, 40)}`, () =>
    withBar(async (a) => {
      await a.type("please");
      await a.submit();
      await a.frame({ type: "notice", text: words, error: true });
      assert.equal(a.status(), words);
      assert.equal(a.state(), "refused");
      assert.equal(a.container.find(".mbar-status").getAttribute("role"), "alert");
      assert.equal(a.input().value, "please", "the text was lost");
      // The bar is free again.
      await a.submit();
      assert.equal(a.sends().length, 2);
    }));
}

test("an answer for another pane, or an approval notice, is not the answer to this send", () =>
  withBar(async (a) => {
    await a.type("x");
    await a.submit();
    await a.frame({ type: "paneSend", id: "p2", queued: false, shell: false });
    await a.frame({ type: "notice", text: "something else", error: true, kind: "approval" });
    assert.equal(a.state(), "sending", "an approval notice was read as the refusal");
    await a.frame({ type: "notice", text: "fine", error: false });
    assert.equal(a.state(), "sending");
    await a.frame({ type: "paneSend", id: "p1", queued: false, shell: false });
    assert.equal(a.state(), "sent");
  }));

test("an old refusal is not the answer to a new send", () =>
  withBar(async (a) => {
    await a.frame({ type: "notice", text: "left over", error: true });
    await a.type("x");
    await a.submit();
    assert.equal(a.state(), "sending");
  }));

test("an empty text is stopped in the page with the desk's words, and nothing is sent", () =>
  withBar(async (a) => {
    await a.submit();
    assert.equal(a.status(), EMPTY_WORDS);
    await a.type("  \n\t ");
    await a.submit();
    assert.equal(a.status(), EMPTY_WORDS);
    await a.type("\u0001\u0007");
    await a.submit();
    assert.equal(a.status(), EMPTY_WORDS, "control characters alone are no text");
    assert.equal(a.sends().length, 0);
    await a.type("x");
    assert.equal(a.status(), "", "the line stayed after the person changed the text");
  }));

test("a text over 16 KiB is stopped, counted in bytes", () =>
  withBar(async (a) => {
    assert.equal(MAX_BYTES, 16384);
    await a.type("a".repeat(MAX_BYTES + 1));
    await a.submit();
    assert.equal(a.status(), TOO_LONG_WORDS);
    await a.type("é".repeat(MAX_BYTES / 2 + 1)); // 16386 bytes, 8193 characters
    await a.submit();
    assert.equal(a.status(), TOO_LONG_WORDS);
    assert.equal(a.sends().length, 0);
    await a.type("a".repeat(MAX_BYTES));
    await a.submit();
    assert.equal(a.sends().length, 1, "exactly 16 KiB is allowed");
  }));

test("checkText counts the length before control characters are removed", () => {
  assert.equal(checkText("a".repeat(MAX_BYTES - 1) + "\u0001\u0001"), TOO_LONG_WORDS);
  assert.equal(checkText("ok"), "");
  assert.equal(checkText(undefined), EMPTY_WORDS);
});

test("a project with no manager says so and the bar is off", () =>
  withBar(async (a) => {
    assert.equal(a.state(), "no-manager");
    assert.equal(a.status(), NO_MANAGER_WORDS);
    assert.ok(off(a.input()));
    assert.ok(off(a.send()));
    a.send().click();
    await settle();
    assert.equal(a.sends().length, 0);
  }, soloGraph()));

test("a window through the relay shows no bar", () =>
  withBar(async (a) => {
    assert.equal(a.store.getState().remote, true);
    assert.equal(a.container.find(".mbar"), null);
    assert.equal(a.container.childNodes.length, 0);
  }, teamGraph(), { helloOver: { remote: true } }));

test("with the socket down the text is not sent and the bar says why", () =>
  withBar(async (a) => {
    await a.type("hello");
    a.ws.drop();
    await settle();
    await a.submit();
    assert.equal(a.sends().length, 0);
    assert.equal(a.status(), NOT_CONNECTED_WORDS);
  }));

test("a manager's name and a refusal are drawn as text, never as markup", () =>
  withBar(async (a) => {
    await a.type("x");
    await a.submit();
    await a.frame({ type: "notice", text: "<img src=x onerror=alert(1)>", error: true });
    assert.equal(a.status(), "<img src=x onerror=alert(1)>");
    assert.equal(a.container.find("img"), null);
  }, { ...teamGraph(), nodes: [node("p1", { name: "<b>Lead</b>", team: "t1" }), node("p2", { team: "t1" })] }));

test("the keyboard goes to the box once the manager is known, unless something holds it", async () => {
  await withBar(async (a) => {
    assert.equal(a.dom.document.activeElement, a.input());
  });
  const h = await harness();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  const other = dom.document.createElement("button");
  dom.body.appendChild(other);
  dom.body.appendChild(container);
  other.focus();
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  render(html`<${App} store=${h.store} model=${buildGraphModel(teamGraph())} />`, container);
  await settle();
  assert.equal(dom.document.activeElement, other, "the box took the keyboard from a control");
  render(null, container);
  await settle();
  restore();
});

test("the box can be reached by a ref for a view that moves the keyboard to it", async () => {
  const h = await harness();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const ref = { current: null };
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  const model = buildGraphModel(teamGraph());
  const Wrap = () => {
    const snap = useStore(h.store);
    return html`<${ManagerBar} snap=${snap} manager=${managerOf(model)} send=${(c) => h.store.send(c)} inputRef=${ref} autoFocus=${false} />`;
  };
  render(html`<${Wrap} />`, container);
  await settle();
  assert.equal(ref.current, container.find(".mbar-input"));
  assert.notEqual(dom.document.activeElement, ref.current, "autoFocus off still took the keyboard");
  render(null, container);
  await settle();
  restore();
});

test("the text does not leak into markup rules: the page file has no eval or innerHTML", () => {
  const src = readFileSync(join(here, "..", "src", "graph", "ManagerBar.js"), "utf8");
  assert.ok(!/eval\(|innerHTML|dangerouslySetInnerHTML|new Function/.test(src));
});

test("the box is one line: a line break typed or pasted becomes a space", () =>
  withBar(async (a) => {
    assert.equal(a.input().getAttribute("type"), "text");
    await a.type("one\ntwo\r\nthree");
    assert.equal(a.input().value, "one two three");
    await a.submit();
    assert.deepEqual(a.sends().map((c) => c.text), ["one two three"]);
    assert.match(a.input().getAttribute("placeholder"), /Enter sends/);
  }));

test("a second Enter before the page has redrawn does not send again", () =>
  withBar(async (a) => {
    await a.type("once");
    a.input().keydown("Enter");
    a.input().keydown("Enter");
    a.send().click();
    await settle();
    assert.equal(a.sends().length, 1);
    // And one that comes after the redraw, with the box off.
    a.input().keydown("Enter");
    await settle();
    assert.equal(a.sends().length, 1);
    // Once the answer is in, the bar sends again.
    await a.frame({ type: "paneSend", id: "p1", queued: false, shell: false });
    await a.type("twice");
    await a.submit();
    assert.equal(a.sends().length, 2);
  }));

test("the box takes the keyboard once: a manager that goes and comes back does not take it again", () =>
  withBar(async (a) => {
    assert.equal(a.dom.document.activeElement, a.input());
    a.input().blur();
    assert.notEqual(a.dom.document.activeElement, a.input());
    await a.redraw(buildGraphModel(soloGraph()));
    await a.redraw(buildGraphModel(teamGraph()));
    assert.notEqual(a.dom.document.activeElement, a.input(), "the box took the keyboard a second time");
  }));

test("outcomeOf: after a reconnect no answer is coming, so the open send is refused, not sending forever", () => {
  const hello = { type: "hello" };
  const pending = { pane: "m1", notice: 0, sent: 0, hello };
  const snap = { connection: "open", hello, sent: null, notice: null };
  assert.equal(outcomeOf(snap, pending).kind, "sending");
  assert.deepEqual(outcomeOf({ ...snap, connection: "waiting", hello: null }, pending), { kind: "refused", text: NOT_CONNECTED_WORDS });
  assert.deepEqual(outcomeOf({ ...snap, hello: { type: "hello" } }, pending), { kind: "refused", text: LOST_WORDS });
  // An answer that did arrive on the same connection still counts.
  assert.equal(outcomeOf({ ...snap, sent: { seq: 1, id: "m1" } }, pending).kind, "sent");
});

test("a viewer's manager bar is off and says so, and sends nothing", () =>
  withBar(async (a) => {
    assert.equal(a.state(), "viewer");
    assert.equal(a.input().getAttribute("disabled") !== null, true);
    assert.equal(a.send().getAttribute("disabled") !== null, true);
    assert.equal(a.status(), VIEWER_WORDS);
    a.send().click();
    await settle();
    assert.deepEqual(a.sends(), []);
  }, undefined, { helloOver: { role: "viewer" } }));

test("outcomeOf: an answer or refusal that carries another send's req is not this send's", () => {
  const hello = { type: "hello" };
  const pending = { pane: "m1", notice: 0, sent: 0, hello, req: "mine" };
  const snap = { connection: "open", hello, sent: null, notice: null };
  assert.equal(outcomeOf({ ...snap, notice: { seq: 1, error: true, text: "no", req: "other" } }, pending).kind, "sending", "another send's refusal");
  assert.deepEqual(outcomeOf({ ...snap, notice: { seq: 1, error: true, text: "no", req: "mine" } }, pending), { kind: "refused", text: "no" });
  assert.equal(outcomeOf({ ...snap, sent: { seq: 1, id: "m1", req: "other" } }, pending).kind, "sending", "another send's answer");
  assert.equal(outcomeOf({ ...snap, sent: { seq: 1, id: "m1", req: "mine" } }, pending).kind, "sent");
  // A desk that does not echo req: the old reading, any new error notice.
  assert.equal(outcomeOf({ ...snap, notice: { seq: 1, error: true, text: "no" } }, pending).kind, "refused");
});
