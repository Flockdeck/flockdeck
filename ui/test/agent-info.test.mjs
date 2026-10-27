// Parity K: the agent info view of a structured pane and the sign-in restart.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { infoMessage, restartMessage, infoRows, answerFor, needsSignIn, INFO_NOT_SENT_WORDS } from "../src/drawer/infoModel.js";
import { InfoPanel } from "../src/drawer/InfoPanel.js";
import { Structured } from "../src/drawer/Structured.js";
import { forgetComposerMemory } from "../src/drawer/composerModel.js";
import { fakeStore } from "./termfakes.mjs";
import { createDom, install, settle } from "./dom.mjs";

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
const push = (extra = {}) => ({ type: "structured", id: "p1", kind: "structured", plan: [], usage: { used: 0, size: 0 }, phase: "ready", ...extra });

test("the ask and the restart are built only for a real pane and request", () => {
  assert.deepEqual(infoMessage("p1", "i1"), { cmd: "structuredInfo", id: "p1", req: "i1" });
  assert.equal(infoMessage("", "i1"), null);
  assert.equal(infoMessage("p1", ""), null);
  assert.equal(infoMessage(7, "i1"), null);
  assert.deepEqual(restartMessage("p1"), { cmd: "restartPane", id: "p1" });
  assert.equal(restartMessage(""), null);
});

test("the answer's text becomes plain, cut rows, and a line that is not a row is dropped", () => {
  const rows = infoRows("Agent: Claude 2.1\nnot a row\nMCP servers: a, b\x07c\n: no name\nSession: " + "x".repeat(900));
  assert.deepEqual(rows.slice(0, 2), [{ name: "Agent", value: "Claude 2.1" }, { name: "MCP servers", value: "a, b c" }]);
  assert.equal(rows.length, 3);
  assert.equal(rows[2].value.length, 400);
  assert.deepEqual(infoRows(null), []);
  assert.equal(infoRows("a: b\n".repeat(500)).length, 40);
});

test("only the answer to this pane's request is taken, and only a login wait asks for a restart", () => {
  const r = { id: "p1", req: "i2", ok: true };
  assert.equal(answerFor(r, "p1", "i2"), r);
  assert.equal(answerFor(r, "p2", "i2"), null);
  assert.equal(answerFor(r, "p1", "i3"), null);
  assert.equal(answerFor(r, "p1", ""), null);
  assert.equal(answerFor(null, "p1", "i2"), null);
  assert.equal(needsSignIn(push({ phase: "setup", setup: { kind: "login" } })), true);
  assert.equal(needsSignIn(push({ phase: "ready", setup: { kind: "login" } })), false);
  assert.equal(needsSignIn(push({ phase: "setup" })), false);
  assert.equal(needsSignIn(null), false);
});

test("the panel asks, shows only its own answer as text, and refreshes", async () => {
  const m = await mount();
  try {
    const sent = [];
    let ok = true;
    const store = { send: (c) => { sent.push(c); return ok; } };
    const draw = (result) => m.draw(html`<${InfoPanel} pane="p1" st=${push()} store=${store} result=${result} onClose=${() => {}} />`);
    await draw(null);
    assert.equal(sent.length, 1);
    assert.deepEqual([sent[0].cmd, sent[0].id], ["structuredInfo", "p1"]);
    const req = sent[0].req;
    await draw({ id: "p1", req: "other", ok: true, info: "Agent: wrong\n" });
    assert.equal(m.container.find(".info-row"), null, "another request's answer was shown");
    await draw({ id: "p1", req, ok: true, info: "Agent: <b>Claude</b> 2.1\nMCP servers: flockdeck, lint\n" });
    const rows = m.container.findAll(".info-row");
    assert.equal(rows.length, 2);
    assert.equal(rows[0].find("dd").textContent, "<b>Claude</b> 2.1");
    assert.equal(m.container.find(".info-row b"), null, "text became markup");
    assert.equal(m.container.find("[data-info-login]"), null, "a login offer for an agent that is signed in");
    m.container.find(".info-refresh").click();
    await settle();
    assert.equal(sent.length, 2);
    await draw({ id: "p1", req: sent[1].req, ok: false, error: "That pane is not a structured pane" });
    assert.ok(m.container.find('[data-info="failed"]').textContent.includes("not a structured pane"));
    ok = false;
    m.container.find(".info-refresh").click();
    await settle();
    assert.ok(m.container.find(".structured-note--unsent").textContent.includes(INFO_NOT_SENT_WORDS));
  } finally {
    await m.done();
  }
});

test("a pane waiting for a login says so and a restart is sent only when the person presses the button", async () => {
  const m = await mount();
  try {
    const sent = [];
    const store = { send: (c) => { sent.push(c); return true; } };
    const st = push({ phase: "setup", setup: { kind: "login", message: "Sign in to continue", fix: "Run claude once" } });
    await m.draw(html`<${InfoPanel} pane="p1" st=${st} store=${store} result=${null} onClose=${() => {}} />`);
    const box = m.container.find("[data-info-login]");
    assert.ok(box.textContent.includes("Sign in to continue") && box.textContent.includes("Run claude once"));
    assert.ok(box.textContent.includes("does not sign in for you"));
    assert.deepEqual(sent.map((c) => c.cmd), ["structuredInfo"], "a restart was sent without a press");
    m.container.find(".info-restart").click();
    await settle();
    assert.deepEqual(sent.map((c) => c.cmd), ["structuredInfo", "restartPane"]);
    assert.equal(sent[1].id, "p1");
    assert.ok(!JSON.stringify(sent).match(/key|token|password/i), "a credential field in a command");
  } finally {
    await m.done();
  }
});

test("Agent info opens from the button and from /info, on the desk only, and opens by itself for a login wait", async () => {
  for (const desk of [true, false]) {
    const m = await mount();
    try {
      const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {} });
      const draw = (st) => m.draw(html`<${Structured} pane="p1" snap=${store.getState()} st=${st} store=${store} desk=${desk} />`);
      await draw(push());
      assert.equal(!!m.container.find(".structured-info-btn"), desk);
      assert.equal(m.container.find(".info-panel"), null);
      if (desk) {
        m.container.find(".structured-info-btn").click();
        await settle();
        assert.ok(m.container.find(".info-panel"));
        assert.ok(store.sent.some((c) => c.cmd === "structuredInfo"));
        m.container.find(".info-close").click();
        await settle();
        assert.equal(m.container.find(".info-panel"), null);
        const box = m.container.find(".structured-input");
        box.enter("/info");
        await settle();
        box.keydown("Enter");
        await settle();
        assert.ok(m.container.find(".info-panel"), "/info did not open the panel");
      }
      await draw(push({ phase: "setup", setup: { kind: "login", message: "m" } }));
      assert.equal(!!m.container.find("[data-info-login]"), desk);
    } finally {
      await m.done();
    }
  }
});
