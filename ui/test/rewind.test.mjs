// Going back and forking: the model, the panel, and the drawer's commands.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { pointsOf, rewindOffered, canAct, rewindMessage, forkMessage, resultWords, restoredDraft, REWIND_NOT_SENT_WORDS, REWIND_NOT_OFFERED_WORDS, REWIND_CUT_WORDS, REWIND_CONFIRM_WORDS } from "../src/drawer/rewindModel.js";
import { RewindPanel } from "../src/drawer/RewindPanel.js";
import { ownCommandsFor } from "../src/drawer/settingsModel.js";
import { OWN_COMMANDS } from "../src/drawer/slashModel.js";
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

const entries = [
  { id: "e1", kind: "prompt", text: "first" },
  { id: "e2", kind: "message", text: "reply" },
  { id: "e3", kind: "prompt", text: "second\nline" },
];
const st = (over = {}) => ({ status: "idle", capabilities: { rewind: true }, rewindPoints: ["e1", "e3"], rewindToken: "4.0", ...over });

test("points are found by entry id, only for prompts the conversation still holds", () => {
  assert.deepEqual(pointsOf(st(), entries), [{ id: "e1", text: "first" }, { id: "e3", text: "second line" }]);
  assert.deepEqual(pointsOf(st({ rewindPoints: ["e2", "gone", "e3"] }), entries), [{ id: "e3", text: "second line" }]);
  assert.deepEqual(pointsOf(st({ rewindPoints: [] }), entries), []);
  assert.deepEqual(pointsOf(null, entries), []);
});

test("rewind is offered only when the agent says so, and acts only when idle", () => {
  assert.equal(rewindOffered(st()), true);
  assert.equal(rewindOffered(st({ capabilities: { rewind: false } })), false);
  assert.equal(rewindOffered(st({ capabilities: null })), false);
  assert.equal(canAct(st()), true);
  assert.equal(canAct(st({ status: "working" })), false);
  assert.equal(canAct(st({ status: "waiting" })), false);
});

test("the commands name the entry and a rewind always confirms", () => {
  assert.deepEqual(rewindMessage("p1", "e3", "r1", "4.0"), { cmd: "structuredRewind", id: "p1", req: "r1", entry: "e3", confirm: true, rewindToken: "4.0" });
  assert.equal(rewindMessage("p1", "", "r1", "4.0"), null);
  assert.deepEqual(forkMessage("p1", "", "r2", "4.0"), { cmd: "structuredFork", id: "p1", req: "r2", entry: "", rewindToken: "4.0" });
});

test("answers are shown as plain words", () => {
  assert.equal(resultWords(null), null);
  assert.equal(resultWords({ ok: false, error: "The agent could not go back" }).words, "The agent could not go back");
  assert.equal(resultWords({ ok: true, op: "structuredRewind", text: "first" }).text, "first");
  assert.equal(resultWords({ ok: true, op: "structuredFork", pane: "p9" }).pane, "p9");
  assert.ok(!resultWords({ ok: true, op: "structuredRewind", text: "x" }).words.includes(REWIND_CUT_WORDS));
  assert.ok(resultWords({ ok: true, op: "structuredRewind", text: "x", cut: true }).words.includes(REWIND_CUT_WORDS));
});

test("a prompt put back never overwrites what the person was typing", () => {
  assert.equal(restoredDraft("", "first"), "first");
  assert.equal(restoredDraft("   \n", "first"), "first");
  assert.equal(restoredDraft("half a thought", "first"), "half a thought\n\nfirst");
  assert.equal(restoredDraft("half a thought\n\n", "first"), "half a thought\n\nfirst");
  assert.equal(restoredDraft("keep me", ""), "keep me");
  assert.equal(restoredDraft(undefined, "first"), "first");
});

test("the confirm text says mail and pictures are not kept", () => {
  assert.ok(REWIND_CONFIRM_WORDS.includes("messages other agents sent"));
  assert.ok(REWIND_CONFIRM_WORDS.includes("pictures"));
});

test("the panel asks before going back, then sends the command and hands over the text", async () => {
  const m = await mount();
  try {
    const sent = [];
    let ok = true;
    const store = { send: (c) => { sent.push(c); return ok; } };
    let got = null;
    const draw = (result, s = st()) => m.draw(html`<${RewindPanel} pane="p1" store=${store} st=${s} entries=${entries} result=${result} onText=${(t) => { got = t; }} onClose=${() => {}} />`);
    await draw(null);
    assert.equal(m.container.findAll(".rewind-point").length, 2);
    m.container.findAll(".rewind-back")[1].dispatch("click");
    await settle();
    assert.equal(sent.length, 0, "sent before the person confirmed");
    assert.ok(m.container.find(".rewind-warn").textContent.includes("everything after it"));
    m.container.find(".rewind-yes").dispatch("click");
    await settle();
    assert.deepEqual([sent[0].cmd, sent[0].id, sent[0].entry, sent[0].confirm, sent[0].rewindToken], ["structuredRewind", "p1", "e3", true, "4.0"]);
    const req = sent[0].req;
    await draw({ id: "p1", req: "other", op: "structuredRewind", ok: true, text: "wrong" });
    assert.equal(got, null, "another request's answer was used");
    await draw({ id: "p2", req, op: "structuredRewind", ok: true, text: "wrong" });
    assert.equal(got, null, "another pane's answer was used");
    await draw({ id: "p1", req, op: "structuredRewind", ok: true, text: "second\nline" });
    assert.equal(got, "second\nline");
    m.container.find(".rewind-fork-all").dispatch("click");
    await settle();
    assert.deepEqual([sent[1].cmd, sent[1].entry, sent[1].rewindToken], ["structuredFork", "", "4.0"]);
    ok = false;
    await draw(null);
    m.container.findAll(".rewind-fork")[0].dispatch("click");
    await settle();
    assert.ok(m.container.find(".structured-note--unsent").textContent.includes(REWIND_NOT_SENT_WORDS));
  } finally {
    await m.done();
  }
});

test("the panel offers nothing without the extension and nothing while a turn runs", async () => {
  const m = await mount();
  try {
    const store = { send: () => true };
    await m.draw(html`<${RewindPanel} pane="p1" store=${store} st=${st({ capabilities: { rewind: false } })} entries=${entries} onClose=${() => {}} />`);
    assert.equal(m.container.findAll(".rewind-point").length, 0);
    assert.equal(m.container.find(".rewind-fork-all"), null);
    assert.ok(m.container.find(".structured-note").textContent.includes(REWIND_NOT_OFFERED_WORDS));
    await m.draw(html`<${RewindPanel} pane="p1" store=${store} st=${st({ status: "working" })} entries=${entries} onClose=${() => {}} />`);
    assert.ok(m.container.findAll(".rewind-back").every((b) => b.disabled));
    assert.ok(m.container.find(".rewind-fork-all").disabled);
  } finally {
    await m.done();
  }
});

test("/rewind and /fork are offered only when the agent can go back", () => {
  const names = (on) => ownCommandsFor(OWN_COMMANDS, [], on).map((c) => c.name);
  assert.ok(names(true).includes("rewind") && names(true).includes("fork"));
  assert.ok(!names(false).includes("rewind") && !names(false).includes("fork"));
});
