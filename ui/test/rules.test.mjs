// Allow-always rules in the agent info panel: the commands are built only for a
// real pane and a well-formed rule id, a rule is removed by id, and the list is
// asked again after every removal.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { rulesMessage, ruleRevokeMessage, ruleRows, rulesAvailable } from "../src/drawer/infoModel.js";
import { InfoPanel } from "../src/drawer/InfoPanel.js";
import { forgetComposerMemory } from "../src/drawer/composerModel.js";
import { createDom, install, settle } from "./dom.mjs";

const ID1 = "r0123456789abcdef";
const ID2 = "rfedcba9876543210";

async function mount() {
  forgetComposerMemory();
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
const ready = (permRules) => ({ type: "structured", id: "p1", kind: "structured", plan: [], usage: { used: 0, size: 0 }, phase: "ready", capabilities: { permRules } });

test("the commands are built only for a real pane and a well-formed id", () => {
  assert.deepEqual(rulesMessage("p1", "r1"), { cmd: "structuredRules", id: "p1", req: "r1" });
  assert.equal(rulesMessage("", "r1"), null);
  assert.deepEqual(ruleRevokeMessage("p1", ID1, "r2"), { cmd: "structuredRuleRevoke", id: "p1", entry: ID1, req: "r2" });
  for (const bad of ["", "3", "r12", ID1 + "0", "R0123456789abcdef", null, 4]) assert.equal(ruleRevokeMessage("p1", bad, "r2"), null, String(bad));
});

test("the list is cleaned, cut and limited, and an entry without a good id is left out", () => {
  const rows = ruleRows([{ id: ID1, tool: "Bash", text: "ls\n-la\u0007", dir: "/work" }, { id: "x", text: "no id" }, { id: ID2, text: "" }, { id: ID2, text: "a".repeat(500) }, null]);
  assert.equal(rows.length, 2);
  assert.equal(rows[0].text, "ls -la");
  assert.equal(rows[1].text.length, 300);
  assert.equal(rows[1].cut, true);
  assert.equal(ruleRows(undefined).length, 0);
  assert.equal(ruleRows(Array.from({ length: 150 }, (_, i) => ({ id: "r" + String(i).padStart(16, "0"), text: "x" }))).length, 100);
});

test("the section shows only for an agent that lists rules", () => {
  assert.equal(rulesAvailable(ready(true)), true);
  assert.equal(rulesAvailable(ready(false)), false);
  assert.equal(rulesAvailable({ phase: "ready" }), false);
  assert.equal(rulesAvailable(null), false);
});

test("the panel lists the rules, removes one by id, and asks again", async () => {
  const m = await mount();
  try {
    const sent = [];
    const store = { send: (c) => { sent.push(c); return true; } };
    const draw = (st, rulesResult) => m.draw(html`<${InfoPanel} pane="p1" st=${st} store=${store} result=${null} rulesResult=${rulesResult} onClose=${() => {}} />`);
    await draw(ready(false), null);
    assert.deepEqual(sent.map((c) => c.cmd), ["structuredInfo"], "no rules ask for an agent that has none");
    assert.equal(m.container.find("[data-info-rules]"), null);

    sent.length = 0;
    await draw(ready(true), null);
    const ask = sent.find((c) => c.cmd === "structuredRules");
    assert.ok(ask, "the list was asked for");
    await draw(ready(true), { id: "p1", req: ask.req, op: "structuredRules", ok: true, rules: [{ id: ID1, tool: "Bash", text: "npm test" }, { id: ID2, tool: "Bash", text: "make <b>x</b>" }] });
    assert.equal(m.container.findAll(".info-rule").length, 2);
    assert.equal(m.container.find("b"), null, "markup in a rule is shown as text");

    sent.length = 0;
    m.container.findAll(".info-rule-remove")[1].click();
    await settle();
    assert.deepEqual(sent, [{ cmd: "structuredRuleRevoke", id: "p1", entry: ID2, req: sent[0].req }]);

    await draw(ready(true), { id: "p1", req: sent[0].req, op: "structuredRuleRevoke", ok: true, revoked: ID2 });
    assert.equal(sent.filter((c) => c.cmd === "structuredRules").length, 1, "the list is read again after a removal");
  } finally {
    await m.done();
  }
});

test("an answer for another pane or another ask is not shown", async () => {
  const m = await mount();
  try {
    const sent = [];
    const store = { send: (c) => { sent.push(c); return true; } };
    const draw = (rulesResult) => m.draw(html`<${InfoPanel} pane="p1" st=${ready(true)} store=${store} result=${null} rulesResult=${rulesResult} onClose=${() => {}} />`);
    await draw(null);
    const ask = sent.find((c) => c.cmd === "structuredRules");
    await draw({ id: "p2", req: ask.req, op: "structuredRules", ok: true, rules: [{ id: ID1, text: "other pane" }] });
    await draw({ id: "p1", req: "stale", op: "structuredRules", ok: true, rules: [{ id: ID1, text: "stale" }] });
    assert.equal(m.container.findAll(".info-rule").length, 0);
  } finally {
    await m.done();
  }
});
