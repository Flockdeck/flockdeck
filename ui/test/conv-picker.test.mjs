// The conversation picker: the model, the panel, and the slash commands that open it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { sessionsOffered, busyWords, sessionsMessage, resumeMessage, newConversationMessage, sessionsOf, timeWords, convResultWords, PICKER_NOT_OFFERED_WORDS, PICKER_NOT_SENT_WORDS } from "../src/drawer/convPickerModel.js";
import { ConvPicker } from "../src/drawer/ConvPicker.js";
import { menuOf, flatten, OWN_COMMANDS } from "../src/drawer/slashModel.js";
import { ownCommandsFor } from "../src/drawer/settingsModel.js";
import { createDom, install, settle } from "./dom.mjs";

async function mount() {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  return {
    container,
    draw: async (vnode) => { render(vnode, container); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); },
  };
}

const st = (over = {}) => ({ status: "idle", capabilities: { listSessions: true, loadSession: true, resumeSession: false }, queue: [], ...over });
const sess = [
  { id: "s2", title: "newer one", updated: "2026-01-02T10:00:00Z" },
  { id: "s1", title: "\u001b[31mred\u0007 title\nwith a second line " + "x".repeat(300), updated: "" },
  { id: "cur", title: "this one", updated: "2026-01-01T10:00:00Z", current: true },
];

test("the picker is offered only for an agent that lists conversations and can reopen one", () => {
  assert.equal(sessionsOffered(st()), true);
  assert.equal(sessionsOffered(st({ capabilities: { listSessions: true, loadSession: false, resumeSession: true } })), true);
  assert.equal(sessionsOffered(st({ capabilities: { listSessions: false, loadSession: true } })), false);
  assert.equal(sessionsOffered(st({ capabilities: { listSessions: true } })), false);
  assert.equal(sessionsOffered(null), false);
});

test("the commands carry the id, and confirm only when asked", () => {
  assert.deepEqual(sessionsMessage("p1", "c1"), { cmd: "structuredSessions", id: "p1", req: "c1" });
  assert.deepEqual(resumeMessage("p1", "s2", "c2"), { cmd: "structuredResume", id: "p1", req: "c2", sessionId: "s2" });
  assert.deepEqual(resumeMessage("p1", "s2", "c2", true), { cmd: "structuredResume", id: "p1", req: "c2", sessionId: "s2", confirm: true });
  assert.equal(resumeMessage("p1", "", "c2"), null);
  assert.deepEqual(newConversationMessage("p1", "c3", true), { cmd: "structuredNewConversation", id: "p1", req: "c3", confirm: true });
});

test("what a move ends is said in words", () => {
  assert.equal(busyWords(st()), "");
  assert.equal(busyWords(st({ status: "working" })), "This ends the turn that is running.");
  assert.equal(busyWords(st({ status: "waiting" })), "This ends the turn that is running, and the permission card it has open.");
  assert.equal(busyWords(st({ queue: [{ id: 1, kind: "user", text: "a" }] })), "This drops the 1 prompt waiting.");
  assert.equal(busyWords(st({ status: "working", queue: [{ id: 1, kind: "user", text: "a" }, { id: 2, kind: "user", text: "b" }] })), "This ends the turn that is running and drops the 2 prompts waiting.");
});

test("titles from the agent are cleaned and cut again, and the list is capped", () => {
  const out = sessionsOf(sess);
  assert.equal(out[1].title.includes("\u001b"), false);
  assert.equal(out[1].title.includes("\n"), false);
  assert.ok(Array.from(out[1].title).length <= 120);
  assert.equal(sessionsOf([{ id: "" }, { id: "x".repeat(200) }, null, { id: "ok" }]).length, 1);
  assert.equal(sessionsOf(Array.from({ length: 80 }, (_, i) => ({ id: "s" + i, title: "t" }))).length, 30);
  assert.equal(sessionsOf([{ id: "a", title: "" }])[0].title, "(no title)");
  assert.deepEqual(sessionsOf("nope"), []);
});

test("times read as words, and a bad one as nothing", () => {
  const now = Date.parse("2026-01-02T10:30:00Z");
  assert.equal(timeWords("2026-01-02T10:29:40Z", now), "just now");
  assert.equal(timeWords("2026-01-02T10:00:00Z", now), "30 min ago");
  assert.equal(timeWords("2026-01-02T07:30:00Z", now), "3 h ago");
  assert.equal(timeWords("garbage", now), "");
  assert.equal(timeWords("", now), "");
});

test("answers are plain words", () => {
  assert.equal(convResultWords(null), null);
  assert.deepEqual(convResultWords({ ok: false, error: "The agent could not list its conversations" }), { ok: false, words: "The agent could not list its conversations" });
  assert.equal(convResultWords({ ok: true, op: "structuredResume" }).ok, true);
});

test("/resume and /new are Flockdeck's own commands", () => {
  const own = ownCommandsFor(OWN_COMMANDS, [], false);
  assert.ok(own.some((c) => c.name === "resume" && c.id === "resume"));
  assert.ok(own.some((c) => c.name === "new" && c.id === "new"));
  const m = menuOf("/res", [{ name: "resume", description: "the agent's own" }], own);
  assert.deepEqual(flatten(m).map((i) => i.group + ":" + i.name), ["own:resume"]);
});

function fakeStore() {
  const sent = [];
  return { sent, send: (m) => { sent.push(m); return true; } };
}

test("the panel asks for the list, shows it, and resumes without a confirm when nothing runs", async () => {
  const m = await mount();
  const store = fakeStore();
  let moved = 0;
  try {
    await m.draw(html`<${ConvPicker} pane="p1" store=${store} st=${st()} result=${null} onMoved=${() => moved++} onClose=${() => {}} />`);
    assert.equal(store.sent.length, 1);
    assert.equal(store.sent[0].cmd, "structuredSessions");
    const req = store.sent[0].req;
    await m.draw(html`<${ConvPicker} pane="p1" store=${store} st=${st()} result=${{ id: "p1", req, op: "structuredSessions", ok: true, sessions: sess }} onMoved=${() => moved++} onClose=${() => {}} />`);
    assert.equal(m.container.querySelectorAll(".conv-item").length, 3);
    assert.equal(m.container.querySelectorAll('.conv-item')[2].querySelector('.conv-resume'), null);
    assert.ok(m.container.querySelectorAll('.conv-item')[2].querySelector('.conv-here'));
    m.container.querySelectorAll('.conv-item')[0].querySelector('.conv-resume').click();
    await settle();
    const sent = store.sent[1];
    assert.equal(sent.cmd, "structuredResume");
    assert.equal(sent.sessionId, "s2");
    assert.equal(sent.confirm, undefined);
    await m.draw(html`<${ConvPicker} pane="p1" store=${store} st=${st()} result=${{ id: "p1", req: sent.req, op: "structuredResume", ok: true }} onMoved=${() => moved++} onClose=${() => {}} />`);
    assert.equal(moved >= 1, true);
  } finally { await m.done(); }
});

test("a running turn makes the first press ask and the second confirm", async () => {
  const m = await mount();
  const store = fakeStore();
  try {
    const props = { pane: "p1", store, st: st({ status: "working" }), onClose: () => {} };
    await m.draw(html`<${ConvPicker} ...${props} result=${null} />`);
    const req = store.sent[0].req;
    await m.draw(html`<${ConvPicker} ...${props} result=${{ id: "p1", req, op: "structuredSessions", ok: true, sessions: sess }} />`);
    const btn = () => m.container.querySelectorAll('.conv-item')[0].querySelector('.conv-resume');
    btn().click();
    await settle();
    assert.equal(store.sent.length, 1, "the first press sent nothing");
    const warn = m.container.querySelector(".conv-confirm");
    assert.ok(warn && warn.textContent.includes("ends the turn that is running"));
    btn().click();
    await settle();
    assert.equal(store.sent[1].cmd, "structuredResume");
    assert.equal(store.sent[1].confirm, true);
  } finally { await m.done(); }
});

test("an agent that cannot list says so, and New conversation is still there", async () => {
  const m = await mount();
  const store = fakeStore();
  try {
    await m.draw(html`<${ConvPicker} pane="p1" store=${store} st=${st({ capabilities: { listSessions: false } })} result=${null} onClose=${() => {}} />`);
    assert.equal(store.sent.length, 0, "no list was asked for");
    assert.ok(m.container.textContent.includes(PICKER_NOT_OFFERED_WORDS));
    m.container.querySelector(".conv-new").click();
    await settle();
    assert.equal(store.sent[0].cmd, "structuredNewConversation");
  } finally { await m.done(); }
});

test("a request that could not be sent is said so", async () => {
  const m = await mount();
  const store = { send: () => false };
  try {
    await m.draw(html`<${ConvPicker} pane="p1" store=${store} st=${st({ capabilities: { listSessions: false } })} result=${null} onClose=${() => {}} />`);
    m.container.querySelector(".conv-new").click();
    await settle();
    assert.ok(m.container.textContent.includes(PICKER_NOT_SENT_WORDS));
  } finally { await m.done(); }
});
