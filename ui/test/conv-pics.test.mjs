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
