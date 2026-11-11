// The pictures of a prompt row: thumbnails asked for by "<entry>#<n>", each in the state its answer says,
// a click that enlarges, and the count alone when nothing can be asked for.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Row } from "../src/drawer/conv/rows.js";
import { PromptPics, picKey } from "../src/drawer/conv/PromptPics.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { useStore } from "../src/lib/use-store.js";
import { MAX_DETAILS, MAX_PIC_DETAILS } from "../src/lib/store.js";
import { MAX_INFLIGHT, MAX_ASKS } from "../src/drawer/conv/PromptPics.js";

const PNG = "iVBORw0KGgo=";
const prompt = (images) => ({ id: "e3", kind: "prompt", ts: "2026-11-10T09:00:00Z", text: "look", typed: true, images });
const pic = (over = {}) => ({ mediaType: "image/png", bytes: 3, upload: "1-a.png", ...over });

async function page(fn) {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    await fn(box, async (v) => { render(v, box); await settle(); });
  } finally {
    restore();
  }
}

test("the key of a picture is the prompt's id and its place", () => assert.equal(picKey("e3", 2), "e3#2"));

test("each picture is asked for once, and a loading picture says so", () =>
  page(async (box, draw) => {
    const asked = [];
    await draw(html`<${Row} entry=${prompt([pic(), pic()])} details=${{}} onDetail=${(k) => { asked.push(k); return true; }} />`);
    assert.deepEqual(asked, ["e3#0", "e3#1"]);
    assert.equal(box.findAll(".conv-thumb").length, 2);
    assert.match(box.find(".conv-thumb").textContent, /Loading the image/);
    // A redraw with the same state does not ask again.
    await draw(html`<${Row} entry=${prompt([pic(), pic()])} details=${{}} onDetail=${(k) => { asked.push(k); return true; }} />`);
    assert.equal(asked.length, 2);
  }));

test("an answer draws the thumbnail, a click enlarges it, and a picture that is gone says so", () =>
  page(async (box, draw) => {
    const details = { "e3#0": { state: "ok", n: 1, detail: { data: PNG } }, "e3#1": { state: "gone", n: 2 } };
    await draw(html`<${Row} entry=${prompt([pic(), pic()])} details=${details} onDetail=${() => true} />`);
    const [a, b] = box.findAll(".conv-thumb");
    assert.equal(a.find("img").getAttribute("src"), "data:image/png;base64," + PNG);
    assert.equal(b.find("img"), null);
    assert.match(b.textContent, /No longer available/);
    const btn = a.find(".conv-image");
    assert.equal(btn.getAttribute("aria-pressed"), "false");
    btn.click();
    await settle();
    assert.equal(box.find(".conv-image").getAttribute("aria-pressed"), "true");
    assert.match(box.find(".conv-image").getAttribute("class"), /is-big/);
  }));

test("a failed answer says to try again, and an answer the store later drops does not blank the picture", () =>
  page(async (box, draw) => {
    const mk = (details) => html`<${Row} entry=${prompt([pic()])} details=${details} onDetail=${() => true} />`;
    await draw(mk({ "e3#0": { state: "failed", n: 1 } }));
    assert.match(box.find(".conv-thumb").textContent, /Could not load/);
    await draw(mk({ "e3#0": { state: "ok", n: 2, detail: { data: PNG } } }));
    assert.equal(box.findAll("img").length, 1);
    await draw(mk({}));
    assert.equal(box.findAll("img").length, 1, "the store made room and the thumbnail went");
  }));

test("bytes that are not base64, or a type outside the list, are not drawn", () =>
  page(async (box, draw) => {
    const details = { "e3#0": { state: "ok", n: 1, detail: { data: "data:x;base64," + PNG } }, "e3#1": { state: "ok", n: 2, detail: { data: PNG } } };
    await draw(html`<${Row} entry=${prompt([pic(), pic({ mediaType: "image/svg+xml" })])} details=${details} onDetail=${() => true} />`);
    assert.equal(box.findAll("img").length, 0);
    assert.equal(box.findAll(".conv-thumb").length, 2);
    assert.match(box.find(".conv-thumb").textContent, /not drawn here/);
  }));

test("at most four thumbnails, and a nested prompt asks for nothing", () =>
  page(async (box, draw) => {
    const asked = [];
    const on = (k) => { asked.push(k); return true; };
    await draw(html`<${Row} entry=${prompt([pic(), pic(), pic(), pic(), pic(), pic()])} details=${{}} onDetail=${on} />`);
    assert.equal(box.findAll(".conv-thumb").length, 4);
    assert.equal(asked.length, 4);
    asked.length = 0;
    const out = renderToString(html`<${Row} entry=${prompt([pic()])} nested=${true} details=${{}} onDetail=${on} />`);
    assert.match(out, /<p class="conv-pics">1 picture<\/p>/);
  }));

test("with no way to ask, or a store that refuses, the row says how many pictures it had", () =>
  page(async (box, draw) => {
    assert.match(renderToString(html`<${Row} entry=${prompt([pic(), pic()])} />`), /<p class="conv-pics">2 pictures<\/p>/);
    await draw(html`<${Row} entry=${prompt([pic(), pic(), pic()])} details=${{}} onDetail=${() => false} />`);
    assert.equal(box.find(".conv-pics").textContent, "3 pictures");
    assert.equal(box.find(".conv-thumb"), null);
  }));

test("a prompt with pictures keeps no Retry, however it is drawn", () => {
  for (const props of [{}, { details: {}, onDetail: () => true }]) {
    const out = renderToString(html`<${Row} entry=${prompt([pic()])} onRetry=${() => {}} ...${props} />`);
    assert.ok(!out.includes("conv-retry"));
  }
  assert.equal(renderToString(html`<${PromptPics} entry=${{ id: "e1" }} />`), "");
});

async function up() {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  h.store.watchConversation("p1");
  return { ...h, ws: FakeWebSocket.last };
}

test("pictures of several prompts all arrive, at most four asked at once, and none stays loading", () =>
  page(async (box, draw) => {
    const { store, ws } = await up();
    const rows = ["a", "b", "c", "d", "e"].map((id) => ({ ...prompt([pic(), pic(), pic(), pic()]), id }));
    const List = () => {
      const snap = useStore(store);
      return html`<div>${rows.map((r) => html`<${Row} key=${r.id} entry=${r} details=${snap.details} onDetail=${store.requestDetail} />`)}</div>`;
    };
    await draw(html`<${List} />`);
    let answered = 0;
    for (let round = 0; round < 20 && answered < 20; round++) {
      const open = ws.commands("conversationDetail").slice(answered);
      assert.ok(open.length <= MAX_INFLIGHT, "asked " + open.length + " at once");
      assert.ok(open.length > 0, "nothing asked while pictures are missing");
      for (const c of open) ws.frame({ type: "conversationDetail", id: "p1", entry: c.entry, detail: { data: PNG } });
      answered += open.length;
      await settle();
    }
    assert.equal(box.findAll("img").length, 20);
    assert.ok(!/Loading the image/.test(box.textContent));
  }));

test("a picture the store lost is asked for again, then says it is no longer available", () =>
  page(async (box, draw) => {
    const asked = [];
    // A store that drops every key at once: the row never sees an answer.
    await draw(html`<${Row} entry=${prompt([pic()])} details=${{}} onDetail=${(k) => { asked.push(k); return true; }} />`);
    assert.equal(asked.length, 1);
    await draw(html`<${Row} entry=${prompt([pic()])} details=${{ "e3#0": { state: "loading", n: 1 } }} onDetail=${(k) => { asked.push(k); return true; }} />`);
    for (let i = 0; i < MAX_ASKS + 1; i++) {
      await draw(html`<${Row} entry=${prompt([pic()])} details=${{ "e3#0": { state: "loading", n: i + 2 } }} onDetail=${(k) => { asked.push(k); return true; }} />`);
      await draw(html`<${Row} entry=${prompt([pic()])} details=${{}} onDetail=${(k) => { asked.push(k); return true; }} />`);
    }
    assert.equal(asked.length, MAX_ASKS);
    assert.match(box.find(".conv-thumb").textContent, /No longer available/);
  }));

test("pictures have a budget of their own and do not push out a tool's detail", async () => {
  const { store } = await up();
  store.requestDetail("t1");
  for (let i = 0; i < MAX_PIC_DETAILS + 4; i++) store.requestDetail("e" + i + "#0");
  const keys = Object.keys(store.getState().details);
  assert.ok(keys.includes("t1"));
  assert.equal(keys.filter((k) => k.includes("#")).length, MAX_PIC_DETAILS);
  for (let i = 0; i < MAX_DETAILS + 2; i++) store.requestDetail("t" + (i + 2));
  const after = Object.keys(store.getState().details);
  assert.equal(after.filter((k) => k.includes("#")).length, MAX_PIC_DETAILS, "tool asks leave the pictures alone");
});

test("an answered picture goes last when the store makes room", async () => {
  const { store, ws } = await up();
  store.requestDetail("a#0");
  for (let i = 1; i < MAX_PIC_DETAILS; i++) store.requestDetail("a#" + i);
  ws.frame({ type: "conversationDetail", id: "p1", entry: "a#3", detail: { data: PNG } });
  store.requestDetail("b#0");
  const d = store.getState().details;
  assert.ok(d["a#0"], "a picture still loading was dropped for one that had answered");
  assert.equal(d["a#3"], undefined);
});

test("Escape shrinks an enlarged picture and focus stays on its button", () =>
  page(async (box, draw) => {
    const details = { "e3#0": { state: "ok", n: 1, detail: { data: PNG } } };
    await draw(html`<${Row} entry=${prompt([pic()])} details=${details} onDetail=${() => true} />`);
    const btn = box.find(".conv-image");
    btn.click();
    await settle();
    assert.equal(box.find(".conv-image").getAttribute("aria-pressed"), "true");
    assert.equal(box.ownerDocument.activeElement, box.find(".conv-image"));
    const ev = box.find(".conv-image").keydown("Escape");
    await settle();
    assert.equal(box.find(".conv-image").getAttribute("aria-pressed"), "false");
    assert.ok(ev.stopped);
    assert.equal(box.ownerDocument.activeElement, box.find(".conv-image"));
    // Escape on a small picture is left for whoever else wants it.
    assert.ok(!box.find(".conv-image").keydown("Escape").stopped);
  }));

test("with an observer, a row asks only once it is near the viewport", () =>
  page(async (box, draw) => {
    const seen = [];
    globalThis.IntersectionObserver = class {
      constructor(cb) { this.cb = cb; seen.push(this); }
      observe() {}
      disconnect() {}
    };
    try {
      const asked = [];
      await draw(html`<${Row} entry=${prompt([pic(), pic()])} details=${{}} onDetail=${(k) => { asked.push(k); return true; }} />`);
      assert.equal(asked.length, 0);
      assert.equal(box.findAll(".conv-thumb").length, 2);
      seen[0].cb([{ isIntersecting: true }]);
      await settle();
      assert.deepEqual(asked, ["e3#0", "e3#1"]);
    } finally {
      delete globalThis.IntersectionObserver;
    }
  }));
