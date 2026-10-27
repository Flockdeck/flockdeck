// Parity J: the usage meter and its detail, Copy last reply and the reply Copy button,
// and the export panel of a structured pane.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { usageOf, meterText, detailRows, NOT_REPORTED_WORDS } from "../src/drawer/usageModel.js";
import { exportMessage, resultWords, loadDir, saveDir, EXPORT_NOT_SENT_WORDS, EXPORT_NO_DIR_WORDS } from "../src/drawer/exportModel.js";
import { lastReply, CopyReply } from "../src/drawer/conv/CopyReply.js";
import { UsageMeter } from "../src/drawer/UsageMeter.js";
import { ExportPanel } from "../src/drawer/ExportPanel.js";
import { Toolbar } from "../src/drawer/conv/Toolbar.js";
import { Structured } from "../src/drawer/Structured.js";
import { fakeStore } from "./termfakes.mjs";
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

// ---------------------------------------------------------------- usage

test("usageOf reports only what the agent said and never invents a figure", () => {
  assert.deepEqual(usageOf({ used: 50, size: 200, cost: 0.5, currency: "USD" }), { used: 50, size: 200, left: 150, pct: 25, cost: 0.5, currency: "USD" });
  const none = usageOf({ used: 0, size: 0 });
  assert.deepEqual([none.used, none.size, none.left, none.pct, none.cost], [null, null, null, null, null]);
  assert.equal(usageOf({ used: 10, size: 0 }).used, 10, "a used count with no window is still the agent's figure");
  assert.equal(usageOf({ used: 0, size: 100 }).pct, 0);
  for (const bad of [{ used: "9", size: "9" }, { used: -1, size: -5 }, { used: Infinity, size: NaN }, { size: 1e15, used: 1 }, { cost: -3 }, { cost: Infinity }, { cost: 1e12 }, null, "x", 7]) {
    const u = usageOf(bad);
    assert.deepEqual([u.size, u.pct, u.cost], [null, null, null], JSON.stringify(bad));
  }
  assert.equal(usageOf({ size: 100, used: 400 }).pct, 100, "over the window is capped at 100%");
  assert.equal(usageOf({ size: 100, used: 400 }).left, 0);
  assert.equal(usageOf({ cost: 1, currency: "U<S>D 9" }).currency, "USD");
});

test("the meter line and the detail rows", () => {
  assert.equal(meterText({ used: 50, size: 200 }), "25% of context");
  assert.equal(meterText({ cost: 2 }), "Usage details");
  assert.equal(meterText({ used: 0, size: 0 }), "");
  assert.equal(meterText(null), "");
  const rows = Object.fromEntries(detailRows({ used: 1234, size: 200000, cost: 0.0042, currency: "USD" }));
  assert.equal(rows["Context used"], "1,234 tokens");
  assert.equal(rows["Context window"], "200,000 tokens");
  assert.equal(rows["Left in the window"], "198,766 tokens");
  assert.equal(rows["Share used"], "1%");
  assert.equal(rows["Cost for this session"], "0.00 USD");
  const bare = Object.fromEntries(detailRows({ used: 0, size: 0 }));
  assert.equal(bare["Context used"], "Not reported");
  assert.equal(bare["Cost for this session"], "Not reported");
  assert.ok(!("Share used" in bare));
  assert.ok(!detailRows({ used: 5, size: 10 }).some(([k]) => /in and out|input|output/i.test(k)), "a token split was invented");
});

test("the meter draws a bar and opens the detail on press, as plain text", async () => {
  const m = await mount();
  try {
    await m.draw(html`<${UsageMeter} usage=${{ used: 50, size: 200, cost: 1, currency: "<img src=x>" }} />`);
    assert.equal(m.container.find(".usage-line").textContent, "25% of context");
    assert.ok(m.container.find(".usage-bar-fill"));
    assert.equal(m.container.find(".usage-detail"), null);
    m.container.find(".usage-meter-btn").click();
    await settle();
    assert.ok(m.container.find(".usage-detail"));
    assert.ok(m.container.find(".usage-detail").textContent.includes(NOT_REPORTED_WORDS));
    assert.equal(m.container.find("img"), null);
    await m.draw(html`<${UsageMeter} usage=${{ used: 0, size: 0 }} />`);
    assert.equal(m.container.find(".usage-meter"), null, "a meter with nothing reported was drawn");
  } finally {
    await m.done();
  }
});

// ---------------------------------------------------------------- copy

test("lastReply is the newest reply's own text", () => {
  assert.equal(lastReply([{ kind: "reply", markdown: "old" }, { kind: "prompt", text: "p" }, { kind: "reply", markdown: "new **text**" }, { kind: "tool" }]), "new **text**");
  assert.equal(lastReply([{ kind: "reply", text: "plain" }]), "plain");
  assert.equal(lastReply([{ kind: "prompt", text: "p" }, { kind: "reply", markdown: "" }]), "");
  assert.equal(lastReply([]), "");
});

test("Copy last reply copies the last reply and the reply button copies its own reply", async () => {
  const m = await mount();
  try {
    const copied = [];
    const copy = (t) => { copied.push(t); return Promise.resolve(true); };
    const bar = (entries) => html`<${Toolbar} entries=${entries} onSearch=${() => true} onClear=${() => {}} onJump=${() => {}} copy=${copy} />`;
    await m.draw(bar([{ id: "e1", kind: "reply", markdown: "first" }, { id: "e2", kind: "prompt", text: "q" }, { id: "e3", kind: "reply", markdown: "second" }]));
    m.container.find(".conv-copy-last").click();
    await settle();
    assert.deepEqual(copied, ["second"]);
    assert.ok(m.container.find(".conv-copied").textContent.includes("last reply"));
    await m.draw(bar([{ id: "e2", kind: "prompt", text: "q" }]));
    assert.ok(m.container.find(".conv-copy-last").disabled, "Copy last reply was on with no reply");
    await m.draw(html`<${CopyReply} text="the whole reply" copy=${copy} />`);
    m.container.find(".conv-copy-reply").click();
    await settle();
    assert.deepEqual(copied, ["second", "the whole reply"]);
    await m.draw(html`<${CopyReply} text="x" copy=${() => Promise.resolve(false)} />`);
    m.container.find(".conv-copy-reply").click();
    await settle();
    assert.equal(m.container.find(".conv-copy-reply").textContent, "Couldn't copy");
  } finally {
    await m.done();
  }
});

// ---------------------------------------------------------------- export

test("exportMessage needs a folder and sends only the fields the desk reads", () => {
  assert.deepEqual(exportMessage("p1", "  C:\\out  ", true, "x1"), { cmd: "structuredExport", id: "p1", req: "x1", dir: "C:\\out", thinking: true });
  assert.equal(exportMessage("p1", "d", "yes", "x1").thinking, false);
  assert.equal(exportMessage("p1", "   ", false, "x1"), null);
  assert.equal(exportMessage("", "d", false, "x1"), null);
  assert.equal(exportMessage("p1", "d".repeat(1025), false, "x1"), null);
});

test("resultWords says where the file went and what was replaced, or the desk's refusal", () => {
  const ok = resultWords({ ok: true, path: "C:\\out\\f.md", entries: 12, redacted: 2, warning: "" });
  assert.ok(ok.ok && ok.words.includes("Saved 12 entries to C:\\out\\f.md") && ok.words.includes("2 values were replaced") && ok.words.includes("Read it before you share it"));
  assert.ok(resultWords({ ok: true, path: "p", entries: 1, redacted: 0 }).words.includes("Nothing was replaced"));
  const no = resultWords({ ok: false, error: "Name a folder\u0000 to save into" });
  assert.deepEqual([no.ok, no.words], [false, "Name a folder to save into"]);
  assert.equal(resultWords(null), null);
});

test("the remembered folder survives a store that throws", () => {
  const bad = { getItem() { throw new Error("no"); }, setItem() { throw new Error("no"); } };
  assert.equal(loadDir(bad), "");
  saveDir("x", bad);
  const mem = new Map();
  const ok = { getItem: (k) => mem.get(k) ?? null, setItem: (k, v) => mem.set(k, v) };
  saveDir("C:\\a", ok);
  assert.equal(loadDir(ok), "C:\\a");
});

test("the panel sends the command with the folder and the choice, and shows only its own answer", async () => {
  const m = await mount();
  try {
    const sent = [];
    let sendOk = true;
    const store = { send: (c) => { sent.push(c); return sendOk; } };
    const draw = (result) => m.draw(html`<${ExportPanel} pane="p1" store=${store} result=${result} onClose=${() => {}} />`);
    await draw(null);
    m.container.find("form").dispatch("submit");
    await settle();
    assert.equal(sent.length, 0, "sent with no folder");
    assert.equal(m.container.find(".structured-note").textContent, EXPORT_NO_DIR_WORDS);
    m.container.find(".export-dir").enter("C:\\out");
    const box = m.container.find(".export-thinking");
    box.checked = true;
    box.dispatch("change");
    await settle();
    m.container.find("form").dispatch("submit");
    await settle();
    assert.equal(sent.length, 1);
    assert.deepEqual([sent[0].cmd, sent[0].id, sent[0].dir, sent[0].thinking], ["structuredExport", "p1", "C:\\out", true]);
    const req = sent[0].req;
    await draw({ id: "p1", req: "other", ok: true, path: "elsewhere", entries: 1, redacted: 0 });
    assert.equal(m.container.find("[data-export]"), null, "another request's answer was shown");
    await draw({ id: "p2", req, ok: true, path: "elsewhere", entries: 1, redacted: 0 });
    assert.equal(m.container.find("[data-export]"), null, "another pane's answer was shown");
    await draw({ id: "p1", req, ok: true, path: "C:\\out\\f.md", entries: 3, redacted: 1 });
    assert.ok(m.container.find('[data-export="ok"]').textContent.includes("C:\\out\\f.md"));
    await draw({ id: "p1", req, ok: false, error: "That folder is not a full path to a folder that exists" });
    assert.ok(m.container.find('[data-export="failed"]').textContent.includes("not a full path"));
    sendOk = false;
    await draw(null);
    m.container.find("form").dispatch("submit");
    await settle();
    assert.ok(m.container.find(".structured-note").textContent.includes(EXPORT_NOT_SENT_WORDS));
  } finally {
    await m.done();
  }
});

// ---------------------------------------------------------------- in the drawer

const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", status: "idle", plan: [], usage: { used: 50, size: 200 }, ...over });

test("the drawer draws the meter, and Export in the conversation toolbar opens the panel on the desk only", async () => {
  for (const desk of [true, false]) {
    const m = await mount();
    try {
      const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {}, files: null, attached: null });
      await m.draw(html`<${Structured} pane="p3" snap=${store.getState()} st=${push()} store=${store} desk=${desk} conv=${{ supported: true, entries: [], atStart: true }} onSearch=${() => true} />`);
      assert.equal(m.container.find(".usage-line").textContent, "25% of context");
      assert.equal(m.container.find(".export-panel"), null);
      const btn = m.container.find(".conv-export");
      assert.equal(!!btn, desk, "the Export button is on the desk only");
      if (btn) {
        btn.click();
        await settle();
        assert.ok(m.container.find(".export-panel"), "the panel did not open");
        m.container.find(".export-close").click();
        await settle();
        assert.equal(m.container.find(".export-panel"), null);
      }
    } finally {
      await m.done();
    }
  }
});
