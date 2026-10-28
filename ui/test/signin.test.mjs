// Signing in from a structured pane: the card's wording is ours, the address is
// checked again before it is a link, and the commands go out only on a press.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { signInMessage, signInCancelMessage, safeSignInUrl, signInView, SIGNIN_FAILED_WORDS, SIGNIN_LINK_WORDS, SIGNIN_COPIED_WORDS, SIGNIN_COPY_FAILED_WORDS, SIGNIN_REFUSED_WORDS } from "../src/drawer/infoModel.js";
import { InfoPanel } from "../src/drawer/InfoPanel.js";
import { forgetComposerMemory } from "../src/drawer/composerModel.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

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
const push = (signIn, extra = {}) => ({ type: "structured", id: "p1", kind: "structured", plan: [], usage: { used: 0, size: 0 }, phase: "setup", setup: { kind: "login", message: "AGENT-WORDS" }, signIn, ...extra });

test("the commands are built only for a real pane", () => {
  assert.deepEqual(signInMessage("p1"), { cmd: "structuredSignIn", id: "p1" });
  assert.deepEqual(signInCancelMessage("p1"), { cmd: "structuredSignInCancel", id: "p1" });
  assert.equal(signInMessage(""), null);
  assert.equal(signInCancelMessage(3), null);
});

test("only a plain https address without credentials or a port becomes a link", () => {
  assert.equal(safeSignInUrl("https://claude.ai/oauth/authorize?a=b"), "https://claude.ai/oauth/authorize?a=b");
  for (const bad of ["http://claude.ai/x", "javascript:alert(1)", "https://u:p@claude.ai/x", "https://claude.ai:8443/x", "https://claude.ai/a b", "https://claude.ai/‮", "data:text/html,x", "//claude.ai/x", "", null, 5, "https://" + "a".repeat(2100) + ".com/"]) {
    assert.equal(safeSignInUrl(bad), null, String(bad));
  }
  assert.deepEqual(signInView(push({ available: true, state: "waiting", url: "javascript:alert(1)" })), { available: true, state: "waiting", url: null, host: null });
  assert.equal(signInView(push({ available: true, state: "waiting", url: "https://claude.ai/x" })).host, "claude.ai");
  assert.equal(signInView(push({ available: true, state: "bogus" })).state, "");
  assert.equal(signInView(push(undefined)), null);
  assert.equal(signInView(push({ available: true, state: "starting", url: "https://claude.ai/x" })).url, null, "an address outside the waiting state");
});

test("the card offers the sign-in, shows the link and cancel while it waits, and sends only on a press", async () => {
  const m = await mount();
  try {
    const sent = [];
    const store = { send: (c) => { sent.push(c); return true; } };
    const draw = (st) => m.draw(html`<${InfoPanel} pane="p1" st=${st} store=${store} result=${null} onClose=${() => {}} />`);
    await draw(push({ available: true }));
    assert.deepEqual(sent.map((c) => c.cmd), ["structuredInfo"], "a sign-in started without a press");
    assert.equal(m.container.find(".info-restart"), null, "the manual restart is offered next to the sign-in");
    m.container.find(".signin-start").click();
    await settle();
    assert.deepEqual(sent.map((c) => c.cmd), ["structuredInfo", "structuredSignIn"]);

    await draw(push({ available: true, state: "starting" }));
    assert.ok(m.container.find(".signin-cancel") && !m.container.find(".signin-start"));
    await draw(push({ available: true, state: "waiting", url: "https://claude.ai/oauth/authorize?x=1" }));
    const a = m.container.find(".signin-link");
    assert.equal(a.getAttribute("href"), "https://claude.ai/oauth/authorize?x=1");
    assert.equal(a.getAttribute("rel"), "noopener noreferrer");
    assert.equal(a.getAttribute("target"), "_blank");
    assert.equal(a.textContent, SIGNIN_LINK_WORDS, "the link text is ours");
    assert.ok(m.container.find(".signin-host").textContent.includes("claude.ai"));
    m.container.find(".signin-cancel").click();
    await settle();
    assert.equal(sent[sent.length - 1].cmd, "structuredSignInCancel");

    await draw(push({ available: true, state: "waiting", url: "javascript:alert(1)" }));
    assert.equal(m.container.find(".signin-link"), null, "an unsafe address became a link");

    await draw(push({ available: true, state: "failed" }));
    assert.ok(m.container.find("[data-signin]").textContent.includes(SIGNIN_FAILED_WORDS));
    assert.ok(m.container.find(".signin-start"), "no way to try again");
  } finally {
    await m.done();
  }
});

test("a pane whose adapter cannot sign in keeps the manual restart", async () => {
  const m = await mount();
  try {
    const store = { send: () => true };
    await m.draw(html`<${InfoPanel} pane="p1" st=${push(undefined)} store=${store} result=${null} onClose=${() => {}} />`);
    assert.ok(m.container.find(".info-restart") && !m.container.find(".signin-start"));
  } finally {
    await m.done();
  }
});

test("the link has a copy button, with fixed words for the outcome", async () => {
  const m = await mount();
  const real = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  try {
    const copied = [];
    let fail = false;
    Object.defineProperty(globalThis, "navigator", { configurable: true, value: { clipboard: { writeText: async (t) => { if (fail) throw new Error("denied: SECRET"); copied.push(t); } } } });
    const store = { send: () => true };
    await m.draw(html`<${InfoPanel} pane="p1" st=${push({ available: true, state: "waiting", url: "https://claude.ai/oauth/authorize?x=1" })} store=${store} result=${null} onClose=${() => {}} />`);
    assert.ok(m.container.find(".signin-link").parentNode.textContent.includes("If the browser did not open") || m.container.textContent.includes("If the browser did not open"));
    m.container.find(".signin-copy").click();
    await settle();
    assert.deepEqual(copied, ["https://claude.ai/oauth/authorize?x=1"]);
    assert.equal(m.container.find(".signin-copied").textContent, SIGNIN_COPIED_WORDS);
    fail = true;
    m.container.find(".signin-copy").click();
    await settle();
    assert.equal(m.container.find(".signin-copied").textContent, SIGNIN_COPY_FAILED_WORDS, "the error text was shown");
  } finally {
    if (real) Object.defineProperty(globalThis, "navigator", real);
    await m.done();
  }
});

test("a refused sign-in shows our words, not the server's, and only for the press that was refused", async () => {
  const m = await mount();
  try {
    const sent = [];
    const store = { send: (c) => { sent.push(c); return true; } };
    const draw = (signInResult) => m.draw(html`<${InfoPanel} pane="p1" st=${push({ available: true })} store=${store} result=${null} signInResult=${signInResult} onClose=${() => {}} />`);
    await draw(null);
    m.container.find(".signin-start").click();
    await settle();
    const req = sent.find((c) => c.cmd === "structuredSignIn").req;
    assert.ok(req, "no req on the command, so a refusal could not be matched");
    await draw({ id: "p1", req: "someone-else", op: "structuredSignIn", ok: false });
    assert.equal(m.container.find(".signin-refused"), null);
    await draw({ id: "p1", req, op: "structuredSignIn", ok: false, error: "SERVER-TEXT" });
    assert.equal(m.container.find(".signin-refused").textContent, SIGNIN_REFUSED_WORDS);
    assert.ok(!m.container.textContent.includes("SERVER-TEXT"));
    await draw({ id: "p1", req, op: "structuredSignIn", ok: true });
    assert.equal(m.container.find(".signin-refused"), null);
  } finally {
    await m.done();
  }
});

test("the store keeps the answer to a sign-in command for the pane it was sent for, without the server's text", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({ api: { major: 1, minor: 24, caps: ["structuredSignIn"] } }));
  store.send({ cmd: "structuredSignIn", id: "p9", req: "s1" });
  ws.frame({ type: "teamResult", op: "structuredSignIn", req: "s1", error: "SERVER-TEXT" });
  assert.deepEqual(store.getState().signInResult, { id: "p9", req: "s1", op: "structuredSignIn", ok: false });
});
