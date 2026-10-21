// The drawer's view of a structured pane: the activity line, the usage, the
// last 50 entries, the prompt box and Stop, and what a relay window is not given.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Drawer } from "../assets/drawer/Drawer.js";
import { Header } from "../assets/drawer/Header.js";
import { MAX_ENTRIES, contextText, costText, fileOf, activityOf, lastEntries } from "../assets/drawer/Structured.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore, paneState } from "./termfakes.mjs";

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
  const draw = () => render(html`<${Drawer} store=${store} snap=${store.getState()} node=${node} now=${NOW} />`, container);
  draw();
  await settle();
  return {
    container, store,
    set: async (patch) => { store.set(patch); draw(); await settle(); },
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
    await m.set({ cancelError: { error: "That pane has nothing running to stop" } });
    assert.equal(m.container.find(".structured-note").textContent, "That pane has nothing running to stop");
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
