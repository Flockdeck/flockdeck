// The drawer's view of a structured pane: the activity line, the usage, the
// last 50 entries, the prompt box and Stop, and what a relay window is not given.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Drawer } from "../assets/drawer/Drawer.js";
import { Header } from "../assets/drawer/Header.js";
import { MAX_ENTRIES, STOP_NOT_SENT_WORDS, SEND_NOT_SENT_WORDS, contextText, costText, fileOf, activityOf, lastEntries } from "../assets/drawer/Structured.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore, paneState } from "./termfakes.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { Structured } from "../assets/drawer/Structured.js";

const NOW = Date.parse("2026-10-21T09:20:00Z");
const node = { pane: "p3", kind: "structured", structured: true, name: "shop-w2", role: "worker", agent: "gemini", status: "working", activity: "working", since: "2026-10-21T09:14:02Z" };
const entry = (i, over = {}) => ({ id: "e" + i, kind: "reply", ts: "2026-10-21T09:00:00Z", markdown: "reply " + i, ...over });
const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", entries: [], plan: [], usage: { used: 0, size: 0 }, ...over });

async function mount(st, { remote = false } = {}) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ hello: { type: "hello" }, state: paneState({ p3: { structured: true } }), remote, structured: st, structuredError: null, cancelError: null, conversation: null });
  let shown = node;
  const draw = () => render(html`<${Drawer} store=${store} snap=${store.getState()} node=${shown} now=${NOW} />`, container);
  draw();
  await settle();
  return {
    container, store,
    set: async (patch, to) => { if (to) shown = to; store.set(patch); draw(); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); },
  };
}

async function with_(st, opts, fn) {
  const m = await mount(st, opts);
  try {
    await fn(m);
  } finally {
    await m.done();
  }
}

test("the header says the pane is structured, next to its name", () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const c = dom.document.createElement("div");
    dom.body.appendChild(c);
    render(html`<${Header} node=${node} now=${NOW} />`, c);
    assert.equal(c.find(".drawer-kind").textContent, "structured");
    render(html`<${Header} node=${{ ...node, kind: "terminal", structured: false }} now=${NOW} />`, c);
    assert.equal(c.find(".drawer-kind"), null, "a terminal pane was labelled structured");
  } finally {
    restore();
  }
});

test("usage reads as used of size, with the cost only when one was reported", () => {
  assert.equal(contextText({ used: 12400, size: 200000 }), "12,400 of 200,000 tokens used");
  assert.equal(contextText({ used: 5, size: 0 }), "");
  assert.equal(contextText(null), "");
  assert.equal(costText({ cost: 0.5, currency: "USD" }), "0.50 USD");
  assert.equal(costText({ cost: 0 }), "");
  assert.equal(costText({ cost: "free" }), "");
});

test("the activity line is the running tool with its kind and first file, else the push's own text", () => {
  const tool = (i, over) => entry(i, { kind: "tool", name: "Edit", label: "Edit main.go", status: "running", summary: "changing the handler in internal/x/main.go now", ...over });
  const a = activityOf(push({ activity: "Edit main.go", entries: [entry(1), tool(2)] }));
  assert.deepEqual(a, { title: "Edit main.go", kind: "edit", file: "internal/x/main.go" });
  assert.deepEqual(activityOf(push({ activity: "thinking about it", entries: [] })), { title: "thinking about it", kind: "other", file: "" });
  assert.equal(activityOf(push({ entries: [tool(1, { status: "done" })] })), null, "a finished tool was shown as the activity");
  assert.equal(fileOf("nothing here"), "");
  assert.equal(fileOf("open a.txt"), "a.txt");
});

test("a structured pane's drawer draws the activity, usage and entries from the push", () =>
  with_(
    push({
      activity: "Run go test", usage: { used: 1000, size: 8000, cost: 1.25, currency: "USD" },
      entries: [entry(1, { kind: "prompt", text: "fix it" }), entry(2), entry(3, { kind: "tool", name: "Bash", label: "Run go test", status: "running", summary: "go test ./internal/x/y_test.go" })],
    }),
    {},
    async (m) => {
      assert.equal(m.container.find(".structured-title").textContent, "Run go test");
      assert.equal(m.container.find(".structured-file").textContent, "./internal/x/y_test.go");
      assert.ok(m.container.find(".conv-icon--run"), "no icon of the tool's kind");
      assert.equal(m.container.find(".structured-context").textContent, "1,000 of 8,000 tokens used");
      assert.equal(m.container.find(".structured-cost").textContent, "1.25 USD");
      assert.equal(m.container.findAll(".conv-row").length, 3);
      assert.equal(m.container.find(".drawer-kind").textContent, "structured");
    },
  ));

test("an idle pane with no usage says Idle and draws no usage line", () =>
  with_(push(), {}, async (m) => {
    assert.equal(m.container.find(".structured-title").textContent, "Idle");
    assert.equal(m.container.find(".structured-usage"), null);
    assert.ok(m.container.find(".conv-empty"));
  }));

test("500 entries draw as the last 50, and nothing grows the list", () =>
  with_(push({ entries: Array.from({ length: 500 }, (_, i) => entry(i)) }), {}, async (m) => {
    const rows = m.container.findAll(".conv-row");
    assert.equal(rows.length, MAX_ENTRIES);
    assert.equal(rows[0].getAttribute("data-entry"), "e450");
    assert.equal(rows[rows.length - 1].getAttribute("data-entry"), "e499");
    assert.equal(m.container.find(".conv-older"), null, "an Older button that could grow the list");
    assert.equal(lastEntries(push({ entries: [entry(1)] })).length, 1);
    await m.set({ structured: push({ entries: Array.from({ length: 501 }, (_, i) => entry(i)) }) });
    assert.equal(m.container.findAll(".conv-row").length, MAX_ENTRIES);
  }));

test("hostile reply markup renders as text", () =>
  with_(
    push({ entries: [entry(1, { markdown: '<img src=x onerror="alert(1)"><script>alert(2)</script>' }), entry(2, { kind: "prompt", text: "<b>hi</b>" })], activity: "<i>go</i>" }),
    {},
    async (m) => {
      assert.equal(m.container.findAll("img").length, 0);
      assert.equal(m.container.findAll("script").length, 0);
      assert.equal(m.container.findAll("b").length, 0);
      assert.equal(m.container.findAll("i").length, 0);
      assert.ok(m.container.find(".conv-reply").textContent.includes('<img src=x onerror="alert(1)">'));
      assert.equal(m.container.find(".structured-title").textContent, "<i>go</i>");
    },
  ));

test("the prompt box sends paneSend for this pane and clears; an empty box sends nothing", () =>
  with_(push(), {}, async (m) => {
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.deepEqual(m.store.sent, [], "an empty box was sent");
    m.container.find(".structured-input").enter("  run the tests  ");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.deepEqual(m.store.sent, [{ cmd: "paneSend", id: "p3", text: "run the tests" }]);
    assert.equal(m.container.find(".structured-input").value, "");
  }));

test("Stop sends structuredCancel for this pane, and a refusal is shown", () =>
  with_(push(), {}, async (m) => {
    m.container.find(".structured-stop").click();
    await settle();
    assert.equal(m.store.sent.length, 1);
    assert.equal(m.store.sent[0].cmd, "structuredCancel");
    assert.equal(m.store.sent[0].id, "p3");
    assert.ok(m.store.sent[0].req);
    assert.equal(m.container.find(".structured-note"), null);
    await m.set({ cancelError: { id: "p3", error: "That pane has nothing running to stop" } });
    assert.equal(m.container.find(".structured-note").textContent, "That pane has nothing running to stop");
  }));

test("a Stop the store did not send says so, and the words go when a Stop is sent or another pane is opened", () =>
  with_(push(), {}, async (m) => {
    let up = false;
    m.store.send = (cmd) => { m.store.sent.push(cmd); return up; };
    m.container.find(".structured-stop").click();
    await settle();
    assert.equal(m.container.find(".structured-note--unsent").textContent, STOP_NOT_SENT_WORDS);
    up = true;
    m.container.find(".structured-stop").click();
    await settle();
    assert.equal(m.container.find(".structured-note--unsent"), null, "the words stayed after a Stop that was sent");
    up = false;
    m.container.find(".structured-stop").click();
    await settle();
    assert.ok(m.container.find(".structured-note--unsent"));
    await m.set({ structured: push({ id: "p4" }) }, { ...node, pane: "p4" });
    assert.equal(m.container.find(".structured-note--unsent"), null, "the words followed the drawer to another pane");
  }));

test("on the real store, a Stop pressed between the socket opening and the hello says it was not sent, and one after it is sent", async () => {
  const dom = createDom();
  const restore = install(dom);
  const { store } = await harness();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  try {
    render(html`<${Structured} pane="p3" st=${push()} store=${store} />`, container);
    await settle();
    container.find(".structured-stop").click();
    await settle();
    assert.equal(container.find(".structured-note--unsent").textContent, STOP_NOT_SENT_WORDS);
    assert.deepEqual(ws.sent, [], "a command went out before the hello");
    ws.frame(hello());
    await settle();
    container.find(".structured-stop").click();
    await settle();
    assert.equal(ws.commands("structuredCancel").length, 1);
    assert.equal(container.find(".structured-note--unsent"), null);
  } finally {
    render(null, container);
    store.stop();
    restore();
  }
});

test("a Send the store did not send says so and keeps the draft; the words go when a Send is sent or another pane is opened", () =>
  with_(push(), {}, async (m) => {
    let up = false;
    m.store.send = (cmd) => { m.store.sent.push(cmd); return up; };
    const words = () => m.container.find(".structured-note--send");
    m.container.find(".structured-input").enter("run the tests");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.equal(words().textContent, SEND_NOT_SENT_WORDS);
    assert.equal(SEND_NOT_SENT_WORDS, "Not connected to Flockdeck, so the text was not sent.");
    assert.equal(m.container.find(".structured-input").value, "run the tests", "the draft was lost");
    up = true;
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.equal(words(), null, "the words stayed after a Send that was sent");
    assert.equal(m.container.find(".structured-input").value, "");
    up = false;
    m.container.find(".structured-input").enter("again");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.ok(words());
    await m.set({ structured: push({ id: "p4" }) }, { ...node, pane: "p4" });
    assert.equal(words(), null, "the words followed the drawer to another pane");
  }));

test("the words and the draft stay with their pane: p3, p4, p3 shows nothing that was not pressed, and a draft is sent to the pane it was typed in", () =>
  with_(push(), {}, async (m) => {
    m.store.send = (cmd) => { m.store.sent.push(cmd); return cmd.cmd !== "structuredCancel" && cmd.id === "p4"; };
    const to = (id) => m.set({ structured: push({ id }) }, { ...node, pane: id });
    // Stop fails on p3, then p4 is opened, then p3 again: the words are not pressed for p3 the second time.
    m.container.find(".structured-stop").click();
    await settle();
    assert.equal(m.container.find(".structured-note--unsent").textContent, "Not connected to Flockdeck, so Stop was not sent.");
    await to("p4");
    assert.equal(m.container.find(".structured-note--unsent"), null);
    await to("p3");
    assert.equal(m.container.find(".structured-note--unsent"), null, "the words came back with nothing pressed");
    // A draft typed in p3 is not in p4's box, and what is typed in p4 goes to p4.
    m.container.find(".structured-input").enter("for p3");
    await settle();
    await to("p4");
    assert.equal(m.container.find(".structured-input").value || "", "", "p3's draft followed the drawer to p4");
    m.container.find(".structured-input").enter("for p4");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.deepEqual(m.store.sent.filter((c) => c.cmd === "paneSend"), [{ cmd: "paneSend", id: "p4", text: "for p4" }]);
    await to("p3");
    assert.equal(m.container.find(".structured-note--send"), null);
    assert.equal(m.container.find(".structured-input").value || "", "");
  }));

test("a relay window draws the view but no prompt box and no Stop", () =>
  with_(push({ entries: [entry(1)] }), { remote: true }, async (m) => {
    assert.equal(m.container.findAll(".conv-row").length, 1);
    assert.equal(m.container.find(".structured-prompt"), null);
    assert.equal(m.container.find(".structured-stop"), null);
    assert.equal(m.container.find(".structured-input"), null);
    assert.deepEqual(m.store.sent, []);
  }));

test("before the first push it says it is loading, and a refused watch says why", () =>
  with_(null, {}, async (m) => {
    assert.ok(m.container.find(".drawer-stub").textContent.includes("Loading the agent"));
    await m.set({ structuredError: { error: "Too many structured panes are being watched" } });
    assert.equal(m.container.find(".drawer-stub").textContent, "Too many structured panes are being watched");
  }));

test("a cancel refusal is shown for its own pane and not for another one opened after it", () =>
  with_(push(), {}, async (m) => {
    await m.set({ cancelError: { id: "p3", error: "That pane has nothing running to stop" } });
    assert.ok(m.container.find(".structured-note"));
    await m.set({ structured: push({ id: "p9" }), cancelError: { id: "p3", error: "That pane has nothing running to stop" } }, { ...node, pane: "p9" });
    assert.ok(m.container.find(".structured-stop"), "the other pane's view was not drawn");
    assert.equal(m.container.find(".structured-note"), null, "pane p3's refusal was shown on pane p9");
  }));

test("rows keep their DOM elements when the oldest entry drops off the 50-entry window", () =>
  with_(push({ entries: Array.from({ length: 50 }, (_, i) => entry(i)) }), {}, async (m) => {
    const byId = () => new Map(m.container.findAll(".conv-row").map((r) => [r.getAttribute("data-entry"), r]));
    const before = byId();
    assert.equal(before.size, 50);
    await m.set({ structured: push({ entries: Array.from({ length: 50 }, (_, i) => entry(i + 1)) }) });
    const after = byId();
    assert.equal(after.size, 50);
    assert.ok(!after.has("e0"), "the oldest entry stayed");
    for (let i = 1; i < 50; i++) {
      assert.equal(after.get("e" + i), before.get("e" + i), "row e" + i + " was made again");
    }
  }));
