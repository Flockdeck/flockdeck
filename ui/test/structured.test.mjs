// The drawer's view of a structured pane: the activity line, the usage, the
// conversation (the same list a terminal agent's drawer has), the prompt box and
// Stop, and what a relay window is not given.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Drawer } from "../src/drawer/Drawer.js";
import { Header } from "../src/drawer/Header.js";
import { STOP_NOT_SENT_WORDS, SEND_NOT_SENT_WORDS, contextText, costText, fileOf, activityOf } from "../src/drawer/Structured.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore, paneState } from "./termfakes.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { Structured } from "../src/drawer/Structured.js";
import { UNASKED_WORDS } from "../src/drawer/Header.js";

const NOW = Date.parse("2026-10-21T09:20:00Z");
const node = { pane: "p3", kind: "structured", structured: true, name: "shop-w2", role: "worker", agent: "gemini", status: "working", activity: "working", since: "2026-10-21T09:14:02Z" };
const entry = (i, over = {}) => ({ id: "e" + i, kind: "reply", ts: "2026-10-21T09:00:00Z", markdown: "reply " + i, ...over });
const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, ...over });
const convOf = (entries) => (entries ? { id: "p3", supported: true, entries, atStart: true, older: "idle", refused: "" } : null);

async function mount(st, { remote = false, pane = null, entries = null } = {}) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: paneState({ p3: { structured: true } }), remote, structured: st, structuredError: null, cancelError: null, conversation: convOf(entries), details: {}, search: null });
  let shown = node;
  const draw = () => render(html`<${Drawer} store=${store} snap=${store.getState()} node=${shown} pane=${pane} now=${NOW} />`, container);
  draw();
  await settle();
  return {
    container, store,
    set: async (patch, to) => { if (to) shown = to; store.set(patch); draw(); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); },
  };
}

// The desk's answer to a send, as the store keeps it: the latest accepted send, or a refusal notice.
let answerSeq = 0;
const taken = (m, cmd, over = {}) => m.set({ sent: { seq: ++answerSeq, id: cmd.id, req: cmd.req, ...over } });
const refused = (m, cmd, text) => m.set({ notice: { seq: ++answerSeq, error: true, kind: "send", text, req: cmd.req } });
const plain = (c) => ({ cmd: c.cmd, id: c.id, text: c.text });

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
  const a = activityOf(push({ activity: "Edit main.go" }), [entry(1), tool(2)]);
  assert.deepEqual(a, { title: "Edit main.go", kind: "edit", file: "internal/x/main.go" });
  assert.deepEqual(activityOf(push({ activity: "thinking about it" }), []), { title: "thinking about it", kind: "other", file: "" });
  assert.equal(activityOf(push(), [tool(1, { status: "done" })]), null, "a finished tool was shown as the activity");
  assert.equal(fileOf("nothing here"), "");
  assert.equal(fileOf("open a.txt"), "a.txt");
});

test("a structured pane's drawer draws the activity and usage from the push and the entries from the conversation", () =>
  with_(
    push({ activity: "Run go test", usage: { used: 1000, size: 8000, cost: 1.25, currency: "USD" } }),
    { entries: [entry(1, { kind: "prompt", text: "fix it" }), entry(2), entry(3, { kind: "tool", name: "Bash", label: "Run go test", status: "running", summary: "go test ./internal/x/y_test.go" })] },
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
  with_(push(), { entries: [] }, async (m) => {
    assert.equal(m.container.find(".structured-title").textContent, "Idle");
    assert.equal(m.container.find(".structured-usage"), null);
    assert.ok(m.container.find(".conv-empty"));
  }));

test("a long conversation draws as the newest rows with Older, the list a terminal agent's drawer has", () =>
  with_(push(), { entries: Array.from({ length: 500 }, (_, i) => entry(i)) }, async (m) => {
    const rows = m.container.findAll(".conv-row");
    assert.equal(rows.length, 200);
    assert.equal(rows[rows.length - 1].getAttribute("data-entry"), "e499");
    assert.ok(m.container.find(".conv-older"), "no Older button");
    assert.ok(m.container.find(".conv-toolbar"), "no search and copy row");
  }));

test("before the conversation has arrived the panel says it is loading, and the push draws no entries of its own", () =>
  with_(push({ entries: [entry(1)] }), {}, async (m) => {
    assert.equal(m.container.findAll(".conv-row").length, 0, "entries of the push were drawn");
    assert.match(m.container.find(".drawer-stub").textContent, /Loading the conversation/);
  }));

test("hostile reply markup renders as text", () =>
  with_(
    push({ activity: "<i>go</i>" }),
    { entries: [entry(1, { markdown: '<img src=x onerror="alert(1)"><script>alert(2)</script>' }), entry(2, { kind: "prompt", text: "<b>hi</b>" })] },
    async (m) => {
      assert.equal(m.container.findAll("img").length, 0);
      assert.equal(m.container.findAll("script").length, 0);
      assert.equal(m.container.findAll("b").length, 0);
      assert.equal(m.container.findAll("i").length, 0);
      assert.ok(m.container.find(".conv-reply").textContent.includes('<img src=x onerror="alert(1)">'));
      assert.equal(m.container.find(".structured-title").textContent, "<i>go</i>");
    },
  ));

test("the prompt box sends paneSend for this pane and clears once the desk took it; an empty box sends nothing", () =>
  with_(push(), {}, async (m) => {
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.deepEqual(m.store.sent, [], "an empty box was sent");
    m.container.find(".structured-input").enter("  run the tests  ");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.deepEqual(m.store.sent.map(plain), [{ cmd: "paneSend", id: "p3", text: "run the tests" }]);
    assert.ok(m.store.sent[0].req, "the send carries a req the desk echoes");
    assert.equal(m.container.find(".structured-input").value, "  run the tests  ", "the draft went before the desk answered");
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.equal(m.store.sent.length, 1, "a second Send while one is out sent again");
    await taken(m, m.store.sent[0]);
    assert.equal(m.container.find(".structured-input").value, "");
    assert.equal(m.container.find(".structured-note--send"), null);
  }));

test("a text the desk refuses is said and stays in the box, and can be sent again", () =>
  with_(push(), {}, async (m) => {
    m.container.find(".structured-input").enter("run the tests");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    await refused(m, m.store.sent[0], "That agent is gone, so the text was not sent.");
    assert.equal(m.container.find(".structured-note--send").textContent, "That agent is gone, so the text was not sent.");
    assert.equal(m.container.find(".structured-input").value, "run the tests", "the refused text was lost");
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.equal(m.store.sent.length, 2, "the refused text could not be sent again");
    assert.equal(m.container.find(".structured-note--send"), null, "the refusal stayed after a new send");
    await taken(m, m.store.sent[1]);
    assert.equal(m.container.find(".structured-input").value, "");
  }));

test("Send is off while a send is out, the refusal is an alert and goes when the text is edited", () =>
  with_(push(), {}, async (m) => {
    const send = () => m.container.find(".structured-send");
    const off = () => send().disabled === true || send().getAttribute("disabled") !== null;
    m.container.find(".structured-input").enter("run the tests");
    await settle();
    assert.equal(off(), false);
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.equal(off(), true, "Send stayed on while the desk had not answered");
    await refused(m, m.store.sent[0], "That agent is gone, so the text was not sent.");
    assert.equal(off(), false, "Send stayed off after a refusal");
    assert.equal(m.container.find(".structured-note--send").attributes.role, "alert");
    assert.ok(m.container.find(".structured-input").focusCalls >= 1, "the keyboard did not go back to the text after the refusal");
    m.container.find(".structured-input").enter("run the tests again");
    await settle();
    assert.equal(m.container.find(".structured-note--send"), null, "the refusal stayed after the text was edited");
    assert.equal(m.container.find(".structured-input").value, "run the tests again");
  }));

test("a refusal does not take the keyboard from something the person moved to meanwhile", () =>
  with_(push(), {}, async (m) => {
    m.container.find(".structured-input").enter("run the tests");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    const box = m.container.find(".structured-input");
    const before = box.focusCalls;
    m.container.find(".structured-stop").focus();
    await refused(m, m.store.sent[0], "That agent is gone, so the text was not sent.");
    assert.equal(box.focusCalls, before, "the refusal took the focus from Stop");
    assert.ok(m.container.find(".structured-note--send"), "the refusal was not said");
  }));

test("a text whose connection dropped before the desk answered is said and stays in the box", () =>
  with_(push(), {}, async (m) => {
    m.container.find(".structured-input").enter("run the tests");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    await m.set({ connection: "closed" });
    assert.match(m.container.find(".structured-note--send").textContent, /not sent|Not connected/);
    assert.equal(m.container.find(".structured-note--send").attributes.role, "alert");
    m.container.find(".structured-input").enter("run the tests, edited");
    await settle();
    assert.equal(m.container.find(".structured-note--send"), null, "the not-sent words stayed after the text was edited");
    assert.equal(m.container.find(".structured-input").value, "run the tests, edited");
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

test("a Stop refusal shows the characters that reorder text", () =>
  with_(push(), {}, async (m) => {
    await m.set({ cancelError: { id: "p3", error: "stop " + String.fromCharCode(0x202e) + "txet" } });
    assert.match(m.container.find(".structured-note").textContent, /stop <U[+]202E>txet/);
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
    await taken(m, m.store.sent[m.store.sent.length - 1]);
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
    assert.deepEqual(m.store.sent.filter((c) => c.cmd === "paneSend").map(plain), [{ cmd: "paneSend", id: "p4", text: "for p4" }]);
    await to("p3");
    assert.equal(m.container.find(".structured-note--send"), null);
    assert.equal(m.container.find(".structured-input").value || "", "");
  }));

test("a relay window draws the view but no prompt box and no Stop", () =>
  with_(push(), { remote: true }, async (m) => {
    // The conversation is not given to a relay window (the store says so in its refusal).
    assert.equal(m.container.findAll(".conv-row").length, 0);
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

test("rows keep their DOM elements when entries come in below", () =>
  with_(push(), { entries: Array.from({ length: 50 }, (_, i) => entry(i)) }, async (m) => {
    const byId = () => new Map(m.container.findAll(".conv-row").map((r) => [r.getAttribute("data-entry"), r]));
    const before = byId();
    assert.equal(before.size, 50);
    await m.set({ conversation: convOf(Array.from({ length: 51 }, (_, i) => entry(i))) });
    const after = byId();
    assert.equal(after.size, 51);
    for (let i = 0; i < 50; i++) {
      assert.equal(after.get("e" + i), before.get("e" + i), "row e" + i + " was made again");
    }
  }));

test("an agent the manifest records as running without asking carries a standing warning in the header, at the desk and through the relay", async () => {
  for (const remote of [false, true]) {
    await with_(push(), { entries: [entry(1)], remote, pane: { id: "p3", structured: true, runsUnasked: true } }, async (m) => {
      const warn = m.container.find(".drawer-warn");
      assert.ok(warn, "no warning in the " + (remote ? "relay" : "desk") + " window");
      assert.equal(warn.textContent, UNASKED_WORDS);
      assert.equal(UNASKED_WORDS, "This agent may run commands and edit files without asking you first: in this repository Flockdeck could not make it ask, or could not check. A project's opencode.json or .opencode folder can also load plugins and MCP servers that run code, so no permission setting makes an opencode pane safe in a repository you do not trust.");
      // It stays while the pane is open, whatever the pane does.
      await m.set({ structured: push({ card: null }), conversation: convOf([entry(1), entry(2)]) });
      assert.ok(m.container.find(".drawer-warn"), "the warning went away");
    });
  }
});

test("no warning for an agent the manifest does not flag, or for a flag that is not true", async () => {
  for (const pane of [null, { id: "p3", structured: true }, { id: "p3", structured: true, runsUnasked: false }, { id: "p3", structured: true, runsUnasked: "<b>yes</b>" }]) {
    await with_(push(), { pane }, async (m) => {
      assert.equal(m.container.find(".drawer-warn"), null, "a warning for " + JSON.stringify(pane));
      assert.equal(m.container.findAll("b").length, 0);
    });
  }
});


test("a structured pane has no Conversation tab: the conversation is the body of its own panel", () =>
  with_(push(), { entries: [entry(1)] }, async (m) => {
    assert.equal(m.container.findAll(".drawer-tab").length, 1);
    assert.equal(m.container.findAll(".conv").length, 1, "a second list");
    assert.equal(m.container.findAll(".conv-row").length, 1);
  }));
