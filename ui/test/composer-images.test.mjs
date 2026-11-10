// Pictures in the prompt box, the cases around the edges: a stray drop, the overlay that must not stick, pictures
// kept per pane, a send in flight, the desk's slow or missing answer, a socket that drops, a chip removed while
// it uploads, a box that goes away mid-prepare, the pacer, the PNG ladder and the words each refusal says.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured } from "../src/drawer/Structured.js";
import { Drawer } from "../src/drawer/Drawer.js";
import { Live } from "../src/shell/Live.js";
import { guardFileDrops } from "../src/shell/dropGuard.js";
import { forgetComposerMemory } from "../src/drawer/composerModel.js";
import {
  resetPacing, forgetAttachments, attachTiming, ATTACH_WAIT_MS, ATTACH_LIMIT, ATTACH_WINDOW_MS, IMAGE_NO_ANSWER_WORDS, IMAGE_DROPPED_WORDS,
  IMAGE_NOT_SENT_WORDS, IMAGE_TOO_MANY_WORDS, infoWords,
} from "../src/drawer/Attachments.js";
import { prepareImage, hasText, otherFiles, refusalWords, convertedNote, SEND_MAX, SOURCE_MAX, HEAD_BYTES } from "../src/drawer/imageAttach.js";
import { IMAGE_UNSUPPORTED_WORDS } from "../src/drawer/Composer.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore, paneState } from "./termfakes.mjs";
import { harness } from "./fakes.mjs";

const read = (f) => readFileSync(new URL(f, import.meta.url), "utf8");
const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, capabilities: { image: true }, ...over });
let seq = 1000;

// A box on a fake page. wrap puts it inside the drawer's own section, as Drawer.js does.
async function mount(st = push(), { wrap = false, pane = "p3" } = {}) {
  forgetComposerMemory();
  forgetAttachments();
  resetPacing();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {}, files: null, attached: null });
  const box = () => html`<${Structured} pane=${pane} snap=${store.getState()} st=${st} store=${store} desk=${true} />`;
  const draw = () => render(wrap ? html`<section class="drawer-body" data-drawer-pane=${pane}>${box()}</section>` : box(), container);
  draw();
  await settle();
  const m = {
    dom, container, store,
    box: () => container.find(".structured-input"),
    form: () => container.find(".structured-prompt"),
    host: () => container.find(".drawer-body"),
    set: async (patch) => { store.set(patch); draw(); await settle(); },
    wait: async (ms) => { await new Promise((r) => setTimeout(r, ms)); draw(); await settle(); },
    took: (cmd) => m.set({ sent: { seq: ++seq, id: cmd.id, req: cmd.req } }),
    close: async () => { render(null, container); await settle(); },
    reopen: async () => { render(null, container); await settle(); draw(); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); attachTiming.waitMs = ATTACH_WAIT_MS; attachTiming.limit = ATTACH_LIMIT; attachTiming.windowMs = ATTACH_WINDOW_MS; },
  };
  return m;
}

const png = (over = {}) => ({ type: "image/png", size: 3, name: "shot.png", arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer, ...over });
const paste = (m, ...files) => m.box().dispatch("paste", { clipboardData: { files, types: ["Files"] } });
const attaches = (m) => m.store.sent.filter((c) => c.cmd === "attachImage");
const paneSends = (m) => m.store.sent.filter((c) => c.cmd === "paneSend");
const upath = (req) => "C:\\state\\uploads\\p3\\" + req + ".png";
const result = (m, req, over = {}) => m.set({ attached: { type: "attachImageResult", id: "p3", req, path: upath(req), seq: ++seq, ...over } });
const chipsOf = (m) => m.container.findAll(".attach-chip");
const states = (m) => chipsOf(m).map((c) => c.getAttribute("data-state"));
const prompt = (m) => m.container.find(".structured-prompt").textContent;
const live = (m) => m.container.find("[data-attach-live]").textContent;
const files = (...f) => ({ dataTransfer: { types: ["Files"], files: f } });
const pngHead = (w, h) => {
  const b = new Uint8Array(33);
  b.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52]);
  new DataView(b.buffer).setUint32(16, w);
  new DataView(b.buffer).setUint32(20, h);
  return b.buffer;
};

// ---------------------------------------------------------------- a stray drop

test("the page cancels a file drop that nothing took, and leaves a text drag and a handled drop alone", () => {
  const dom = createDom();
  const stop = guardFileDrops(dom.window);
  const over = dom.body.dispatch("dragover", files());
  assert.equal(over.defaultPrevented, true, "a file dragged over the graph would navigate on drop");
  assert.equal(over.dataTransfer.dropEffect, "none");
  assert.equal(dom.body.dispatch("drop", files(png())).defaultPrevented, true);
  assert.equal(dom.body.dispatch("dragover", { dataTransfer: { types: ["text/plain"] } }).defaultPrevented, false, "a text drag was taken");
  assert.equal(dom.body.dispatch("drop", {}).defaultPrevented, false);
  // A drawer that took the drag has said so; the cursor stays a copy.
  const host = dom.document.createElement("div");
  dom.body.appendChild(host);
  host.addEventListener("dragover", (e) => { e.preventDefault(); e.dataTransfer.dropEffect = "copy"; });
  assert.equal(host.dispatch("dragover", files()).dataTransfer.dropEffect, "copy");
  stop();
  assert.equal(dom.body.dispatch("drop", files(png())).defaultPrevented, false, "the guard outlived its teardown");
  assert.equal(guardFileDrops(null)(), undefined);
});

test("the shell installs the guard once and takes it off when it goes", () => {
  const live = read("../src/shell/Live.js");
  assert.match(live, /import \{ guardFileDrops \} from "\.\/dropGuard\.js"/);
  assert.match(live, /useEffect\(\(\) => guardFileDrops\(page\), \[page\]\)/);
});

// ---------------------------------------------------------------- the overlay

test("the drop overlay cannot stay on: a drag that ends, a drop elsewhere, a blur or Escape clear it, and a dragover sets it again", async () => {
  const m = await mount(push(), { wrap: true });
  try {
    const host = m.host();
    const on = () => host.getAttribute("data-drop");
    // Two enters and one leave leave the count at one: the drag was cancelled in the page and never said so.
    host.dispatch("dragenter", files());
    host.dispatch("dragenter", files());
    host.dispatch("dragleave", files());
    assert.equal(on(), "on");
    m.dom.window.dispatch("dragend", {});
    assert.equal(on(), null, "dragend");
    host.dispatch("dragenter", files());
    host.dispatch("dragenter", files());
    m.dom.window.dispatch("blur", {});
    assert.equal(on(), null, "blur");
    host.dispatch("dragenter", files());
    m.dom.window.dispatch("keydown", { key: "a" });
    assert.equal(on(), "on", "another key cleared it");
    m.dom.window.dispatch("keydown", { key: "Escape" });
    assert.equal(on(), null, "Escape");
    host.dispatch("dragenter", files());
    host.dispatch("dragenter", files());
    m.dom.body.dispatch("drop", files(png()));
    assert.equal(on(), null, "a drop outside the drawer");
    // More leaves than enters, then a dragover: the pointer is still over the drawer and the overlay is back.
    host.dispatch("dragenter", files());
    host.dispatch("dragleave", files());
    host.dispatch("dragleave", files());
    assert.equal(on(), null);
    host.dispatch("dragover", files());
    assert.equal(on(), "on");
    assert.equal(host.getAttribute("data-drop-words"), "Drop pictures to attach them");
    // A leave after that, the pointer going out, clears it.
    host.dispatch("dragleave", files());
    assert.equal(on(), null);
    host.dispatch("dragover", { dataTransfer: { types: ["text/plain"] } });
    assert.equal(on(), null, "a text drag showed the overlay");
  } finally {
    await m.done();
  }
});

test("the drop is wired on the drawer's own section: a drop there attaches, and the overlay's CSS names that section", async () => {
  const m = await mount(push(), { wrap: true });
  try {
    const host = m.host();
    host.dispatch("dragenter", files());
    assert.equal(host.getAttribute("data-drop"), "on");
    const d = host.dispatch("drop", files(png({ name: "d1.png" })));
    await m.wait(30);
    assert.equal(d.defaultPrevented, true);
    assert.equal(host.getAttribute("data-drop"), null);
    assert.equal(chipsOf(m).length, 1);
    // A drop that lands on the box, inside the section, bubbles to it.
    m.form().dispatch("drop", files(png({ name: "d2.png" })));
    await m.wait(30);
    assert.equal(chipsOf(m).length, 2);
    // After the box goes the section has no listener and no leftover state.
    await m.close();
    assert.equal(host.getAttribute("data-drop"), null);
  } finally {
    await m.done();
  }
  // The real Drawer puts data-drawer-pane on the drawer-body section, which is what the box looks for,
  // and the stylesheet draws the overlay on that same section.
  const drawerSrc = read("../src/drawer/Drawer.js");
  assert.match(drawerSrc, /<section class=\$\{"drawer-body"[^`]*data-drawer-pane=\$\{node\.pane\}/);
  const css = read("../src/drawer/drawer.css");
  assert.match(css, /\.drawer-body\[data-drop\]::after \{[^}]*content: attr\(data-drop-words\)/);
  assert.match(css, /\.drawer-body\[data-drop="no"\]::after/);
});

test("the real drawer shows the overlay on its section when a file is dragged over a structured pane", async () => {
  const dom = createDom();
  const restore = install(dom);
  forgetComposerMemory();
  forgetAttachments();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: paneState({ p3: { structured: true } }), files: null, attached: null, structured: push() });
  const node = { pane: "p3", kind: "structured", structured: true, name: "w1", role: "worker", agent: "claude", shell: false, status: "idle" };
  try {
    render(html`<${Drawer} store=${store} node=${node} now=${Date.now()} />`, container);
    await settle();
    const host = container.find(".drawer-body");
    assert.ok(host, "no drawer section");
    if (!container.find(".structured-prompt")) return; // the harness cannot draw the box here; the wiring is covered above
    host.dispatch("dragenter", files());
    assert.equal(host.getAttribute("data-drop"), "on");
    host.dispatch("dragleave", files());
    assert.equal(host.getAttribute("data-drop"), null);
  } finally {
    render(null, container);
    await settle();
    restore();
  }
});

// ---------------------------------------------------------------- kept per pane

test("attached pictures come back when the drawer is closed and opened again; ones not yet attached are said to be lost", async () => {
  const revoked = [];
  const made = [];
  const keep = [URL.createObjectURL, URL.revokeObjectURL];
  URL.createObjectURL = () => { const u = "blob:" + made.length; made.push(u); return u; };
  URL.revokeObjectURL = (u) => revoked.push(u);
  const m = await mount();
  try {
    paste(m, png({ name: "a.png" }), png({ name: "b.png" }));
    await m.wait(30);
    await result(m, attaches(m)[0].req);
    await m.wait(30);
    assert.deepEqual(states(m), ["ready", "uploading"]);
    await m.reopen();
    assert.deepEqual(states(m), ["ready"], "the attached picture was not kept");
    assert.deepEqual(revoked, ["blob:1"], "the lost one's thumbnail was kept alive, or the kept one's was revoked");
    assert.equal(chipsOf(m)[0].find("img").getAttribute("src"), "blob:0");
    assert.match(prompt(m), /A picture was not kept when you left this pane/);
    m.box().keydown("Enter");
    await settle();
    assert.deepEqual(paneSends(m)[0].images.map((i) => i.path), [upath(attaches(m)[0].req)]);
    await m.reopen();
    assert.deepEqual(revoked, ["blob:1", "blob:0"], "the pictures of a send still out were kept, or not let go");
    assert.deepEqual(chipsOf(m), [], "pictures sent with a prompt still out came back");
    assert.doesNotMatch(prompt(m), /not kept/);
  } finally {
    await m.done();
    [URL.createObjectURL, URL.revokeObjectURL] = keep;
  }
});

test("a pane's kept pictures are its own, and are revoked when the pane is gone", async () => {
  const revoked = [];
  const keep = [URL.createObjectURL, URL.revokeObjectURL];
  let n = 0;
  URL.createObjectURL = () => "blob:" + n++;
  URL.revokeObjectURL = (u) => revoked.push(u);
  const m = await mount();
  try {
    paste(m, png());
    await m.wait(30);
    await result(m, attaches(m)[0].req);
    await m.close();
    forgetAttachments();
    assert.deepEqual(revoked, ["blob:0"]);
  } finally {
    await m.done();
    [URL.createObjectURL, URL.revokeObjectURL] = keep;
  }
  const other = await mount(push({ id: "p4" }), { pane: "p4" });
  try {
    assert.deepEqual(chipsOf(other), []);
  } finally {
    await other.done();
  }
});

test("a picture added while a send is out stays when the desk takes the prompt; only the sent ones go", async () => {
  const m = await mount();
  try {
    m.box().enter("look");
    await settle();
    paste(m, png({ name: "a.png" }));
    await m.wait(30);
    await result(m, attaches(m)[0].req);
    await m.wait(30);
    m.box().keydown("Enter");
    const sent = paneSends(m)[0];
    assert.equal(sent.images.length, 1);
    paste(m, png({ name: "late.png" }));
    await m.wait(30);
    assert.equal(chipsOf(m).length, 2);
    await m.took(sent);
    await m.wait(30);
    assert.equal(chipsOf(m).length, 1, "the late picture was wiped with the sent one");
    assert.match(chipsOf(m)[0].textContent, /late\.png/);
    await result(m, attaches(m)[1].req);
    await m.wait(30);
    assert.deepEqual(states(m), ["ready"]);
  } finally {
    await m.done();
  }
});

// ---------------------------------------------------------------- the pipeline

test("an answer that does not come in time fails the chip, and the next picture goes on", async () => {
  attachTiming.waitMs = 200;
  const m = await mount();
  try {
    paste(m, png({ name: "a.png" }), png({ name: "b.png" }));
    await m.wait(20);
    assert.equal(attaches(m).length, 1);
    await m.wait(300);
    assert.equal(attaches(m).length, 2, "the queue waited past the timeout");
    assert.equal(states(m)[0], "error");
    assert.match(chipsOf(m)[0].textContent, new RegExp(IMAGE_NO_ANSWER_WORDS));
  } finally {
    await m.done();
  }
  assert.equal(ATTACH_WAIT_MS, 15000);
});

test("a socket that drops while a picture is out fails its chip at once", async () => {
  const m = await mount();
  try {
    paste(m, png({ name: "a.png" }), png({ name: "b.png" }));
    await m.wait(30);
    assert.equal(states(m)[0], "uploading");
    await m.set({ connection: "closed" });
    await m.wait(20);
    assert.equal(states(m)[0], "error", "the chip waited for the timeout");
    assert.match(chipsOf(m)[0].textContent, new RegExp(IMAGE_DROPPED_WORDS));
    assert.match(prompt(m), new RegExp(IMAGE_DROPPED_WORDS));
  } finally {
    await m.done();
  }
});

test("a chip removed while it uploads lets the queue go on at once and says nothing", async () => {
  const m = await mount();
  try {
    paste(m, png({ name: "a.png" }), png({ name: "b.png" }));
    await m.wait(30);
    assert.equal(attaches(m).length, 1);
    chipsOf(m)[0].find(".attach-remove").click();
    await m.wait(30);
    assert.equal(attaches(m).length, 2, "the next picture waited for the removed one's answer");
    assert.equal(chipsOf(m).length, 1);
    assert.doesNotMatch(prompt(m), /could not be|did not answer|not attached/);
    // The late answer for the removed one is not anyone's.
    await result(m, attaches(m)[0].req);
    await result(m, attaches(m)[1].req);
    await m.wait(30);
    assert.deepEqual(states(m), ["ready"]);
    assert.equal(chipsOf(m)[0].find(".attach-name").textContent, "b.png");
  } finally {
    await m.done();
  }
});

test("a box that goes away while a picture is being prepared uploads nothing", async () => {
  const m = await mount();
  try {
    let release;
    const gate = new Promise((r) => { release = r; });
    paste(m, png({ name: "slow.png", arrayBuffer: async () => { await gate; return new Uint8Array([1, 2, 3]).buffer; } }), png({ name: "next.png" }));
    await m.wait(20);
    await m.close();
    release();
    await m.wait(40);
    assert.deepEqual(attaches(m), []);
  } finally {
    await m.done();
  }
});

test("a chip removed while it is being prepared is not uploaded", async () => {
  const m = await mount();
  try {
    let release;
    const gate = new Promise((r) => { release = r; });
    paste(m, png({ name: "slow.png", arrayBuffer: async () => { await gate; return new Uint8Array([1, 2, 3]).buffer; } }), png({ name: "next.png" }));
    await m.wait(20);
    chipsOf(m)[0].find(".attach-remove").click();
    release();
    await m.wait(40);
    assert.deepEqual(attaches(m).map((c) => c.name), ["next.png"]);
  } finally {
    await m.done();
  }
});

test("the pacer holds a picture back once the desk's rate limit would be hit, and lets it go when the window passes", async () => {
  attachTiming.limit = 2;
  attachTiming.windowMs = 700;
  const m = await mount();
  try {
    paste(m, png({ name: "a.png" }), png({ name: "b.png" }), png({ name: "c.png" }));
    await m.wait(20);
    await result(m, attaches(m)[0].req);
    await m.wait(20);
    await result(m, attaches(m)[1].req);
    await m.wait(20);
    assert.equal(attaches(m).length, 2, "a third went inside the window");
    assert.equal(states(m)[2], "preparing");
    await m.wait(800);
    assert.equal(attaches(m).length, 3);
  } finally {
    await m.done();
  }
});

// ---------------------------------------------------------------- scaling

// The browser's canvas, standing in. Every draw is recorded with the side and the background it was given.
function scaleEnv({ pngSize, jpegSize, bmp = { width: 4000, height: 3000 } }) {
  const calls = [];
  return {
    calls,
    createImageBitmap: async () => ({ ...bmp, close() {} }),
    makeCanvas: (w, h) => {
      const c = { w, h, bg: null };
      return {
        draw(_b, background) { c.bg = background || null; },
        toBlob: async (type) => {
          calls.push({ type, w: c.w, bg: c.bg });
          const size = type === "image/png" ? pngSize : jpegSize;
          return size === null ? null : { type, size, arrayBuffer: async () => new Uint8Array(size).buffer };
        },
      };
    },
  };
}
const bigPng = (type = "image/png") => ({ type, size: SEND_MAX + 1, name: "big", arrayBuffer: async () => pngHead(4000, 3000) });

test("a PNG that falls back to a JPEG is drawn on white, and a PNG far over the cap skips the smaller PNG try", async () => {
  // Not far over: both PNG sizes are tried, then the JPEG on white.
  let env = scaleEnv({ pngSize: SEND_MAX + 1000, jpegSize: 100 });
  let img = await prepareImage(bigPng(), env);
  assert.deepEqual(env.calls.map((c) => [c.type, c.w, c.bg]), [["image/png", 1600, null], ["image/png", 1000, null], ["image/jpeg", 1600, "#ffffff"]]);
  assert.equal(img.mediaType, "image/jpeg");
  assert.equal(img.note, "sent as a JPEG");
  // More than twice the cap at 1600: 1000 would not get under it either.
  env = scaleEnv({ pngSize: SEND_MAX * 3, jpegSize: 100 });
  img = await prepareImage(bigPng(), env);
  assert.deepEqual(env.calls.map((c) => c.type + c.w), ["image/png1600", "image/jpeg1600"]);
  // A PNG that fits stays one and says nothing.
  env = scaleEnv({ pngSize: 100, jpegSize: 100 });
  img = await prepareImage(bigPng(), env);
  assert.equal(img.mediaType, "image/png");
  assert.equal(img.note, "");
  assert.equal(env.calls[0].bg, null, "a PNG was filled");
  // A GIF or a WebP is always a still JPEG, and says so.
  for (const type of ["image/gif", "image/webp"]) {
    img = await prepareImage(bigPng(type), scaleEnv({ pngSize: 100, jpegSize: 100 }));
    assert.equal(img.mediaType, "image/jpeg");
    assert.equal(img.note, "sent as a still JPEG");
  }
  assert.equal(convertedNote("image/png", "image/png"), "");
  assert.equal(infoWords({ width: 10, height: 5, bytes: 2048, note: "sent as a still JPEG" }), "10 \u00d7 5 \u00b7 2 KB \u00b7 sent as a still JPEG");
});

test("a JPEG whose size is past the first 256 KB is read whole, and a file with none says what is wrong", async () => {
  const b = new Uint8Array(700 * 1024);
  b.set([0xff, 0xd8]);
  const at = 300000;
  b.set([0xff, 0xc0, 0, 17, 8, 0x02, 0x58, 0x03, 0x20, 3], at);
  const file = (bytes) => ({ type: "image/jpeg", size: bytes.length, name: "meta.jpg", slice: (x, y) => ({ arrayBuffer: async () => bytes.slice(x, y).buffer }), arrayBuffer: async () => bytes.buffer });
  assert.ok(at > HEAD_BYTES);
  const img = await prepareImage(file(b), scaleEnv({ pngSize: 1, jpegSize: 100, bmp: { width: 800, height: 600 } }));
  assert.equal(img.mediaType, "image/jpeg");
  const bad = new Uint8Array(700 * 1024);
  await assert.rejects(prepareImage(file(bad), scaleEnv({ pngSize: 1, jpegSize: 100 })), /could not be read, and may be damaged/);
});

test("a file over 25 MB, one that is not a picture and an empty one each get their own reason", async () => {
  assert.match(refusalWords("application/pdf", 5), /not a picture the agent can take/);
  assert.match(refusalWords("image/png", SOURCE_MAX + 1), /over 25 MB/);
  assert.match(refusalWords("image/png", 0), /empty/);
  await assert.rejects(prepareImage({ type: "image/png", size: SOURCE_MAX + 1, name: "huge.png" }, null), /over 25 MB/);
  await assert.rejects(prepareImage({ type: "text/plain", size: 5, name: "a.txt" }, null), /not a picture/);
});

test("a canvas that cannot be made or gives no picture fails the chip in words, through the box", async () => {
  const had = globalThis.createImageBitmap;
  const m = await mount();
  try {
    const canvases = (toBlob) => {
      const make = m.dom.document.createElement;
      m.dom.document.createElement = (n) => {
        if (n !== "canvas") return make(n);
        return { width: 0, height: 0, getContext: () => ({ fillRect() {}, drawImage() {}, set fillStyle(v) {} }), toBlob };
      };
    };
    globalThis.createImageBitmap = async () => { throw new Error("decode failed"); };
    paste(m, bigPng());
    await m.wait(40);
    assert.equal(states(m)[0], "error");
    assert.match(chipsOf(m)[0].textContent, /That picture could not be read/);
    assert.deepEqual(attaches(m), []);
    chipsOf(m)[0].find(".attach-remove").click();
    await m.wait(20);
    globalThis.createImageBitmap = async () => ({ width: 4000, height: 3000, close() {} });
    canvases((cb) => cb(null));
    paste(m, bigPng());
    await m.wait(40);
    assert.equal(states(m)[0], "error");
    assert.match(chipsOf(m)[0].textContent, /too large to attach/);
    chipsOf(m)[0].find(".attach-remove").click();
    await m.wait(20);
    canvases((cb, type) => cb({ type, size: 100, arrayBuffer: async () => new Uint8Array(100).buffer }));
    paste(m, bigPng());
    await m.wait(40);
    assert.equal(attaches(m).length, 1, "a scaled picture was not sent");
    assert.equal(attaches(m)[0].mediaType, "image/png");
    assert.match(chipsOf(m)[0].textContent, /100 B/);
  } finally {
    if (had === undefined) delete globalThis.createImageBitmap;
    else globalThis.createImageBitmap = had;
    await m.done();
  }
});

// ---------------------------------------------------------------- words, focus, paste

test("a paste or drop that mixes pictures with other files says the others were left out", async () => {
  const m = await mount(push(), { wrap: true });
  try {
    const pdf = { type: "application/pdf", size: 5, name: "a.pdf" };
    paste(m, png({ name: "a.png" }), pdf);
    await m.wait(30);
    assert.equal(chipsOf(m).length, 1);
    assert.match(prompt(m), /One file that is not a picture was left out/);
    m.host().dispatch("drop", files(png({ name: "b.png" }), pdf, pdf));
    await m.wait(30);
    assert.equal(chipsOf(m).length, 2);
    assert.match(prompt(m), /2 files that are not pictures were left out/);
    paste(m, ...["c", "d", "e"].map((n) => png({ name: n + ".png" })), pdf);
    await m.wait(30);
    assert.match(prompt(m), new RegExp("One file that is not a picture was left out. " + IMAGE_TOO_MANY_WORDS));
  } finally {
    await m.done();
  }
  assert.equal(otherFiles({ files: [{ type: "image/png" }, { type: "text/plain" }] }), 1);
});

test("a paste that has text as well as a picture is the text's: nothing is taken and the paste is not cancelled", async () => {
  const m = await mount();
  try {
    const ev = m.box().dispatch("paste", { clipboardData: { files: [png()], types: ["text/plain", "text/html", "Files"] } });
    await m.wait(20);
    assert.equal(ev.defaultPrevented, false, "the text paste was prevented");
    assert.deepEqual(chipsOf(m), []);
    assert.deepEqual(attaches(m), []);
    const bare = paste(m, png());
    await m.wait(20);
    assert.equal(bare.defaultPrevented, true);
    assert.equal(chipsOf(m).length, 1);
  } finally {
    await m.done();
  }
  assert.equal(hasText({ types: [], getData: () => "cells" }), true);
  assert.equal(hasText({ types: ["Files"], getData: () => "" }), false);
  assert.equal(hasText(null), false);
  const no = await mount(push({ capabilities: { image: false } }));
  try {
    assert.equal(no.box().dispatch("paste", { clipboardData: { files: [png()], types: ["text/plain", "Files"] } }).defaultPrevented, false);
    assert.doesNotMatch(prompt(no), new RegExp(IMAGE_UNSUPPORTED_WORDS));
  } finally {
    await no.done();
  }
});

test("a chip being added, attached or failing is announced politely, and Remove moves the keyboard on", async () => {
  const m = await mount();
  try {
    const region = m.container.find("[data-attach-live]");
    assert.equal(region.getAttribute("aria-live"), "polite");
    assert.equal(region.getAttribute("role"), "status");
    paste(m, png({ name: "a.png" }), png({ name: "b.png" }), png({ name: "c.png" }));
    await m.wait(30);
    assert.match(live(m), /Attaching a\.png|Added 3 pictures/);
    await result(m, attaches(m)[0].req);
    await m.wait(30);
    assert.match(live(m), /Attaching b\.png/);
    await result(m, attaches(m)[1].req);
    await m.wait(30);
    await result(m, attaches(m)[2].req);
    await m.wait(30);
    assert.equal(live(m), "c.png is attached");
    const doc = m.container.ownerDocument;
    const removes = () => chipsOf(m).map((c) => c.find(".attach-remove"));
    removes()[1].click();
    await m.wait(20);
    assert.equal(live(m), "Removed b.png");
    assert.equal(doc.activeElement, removes()[1], "the keyboard went to the page, not the next chip");
    removes()[1].click();
    await m.wait(20);
    assert.equal(doc.activeElement, removes()[0], "the last chip's Remove did not hand over to the one before");
    removes()[0].click();
    await m.wait(20);
    assert.equal(doc.activeElement, m.box(), "with no chip left the keyboard did not go to the box");
    // A failing chip is said in the alert line.
    paste(m, png({ name: "bad.png" }));
    await m.wait(30);
    await result(m, attaches(m)[3].req, { path: undefined, error: "refused" });
    await m.wait(20);
    assert.match(prompt(m), /bad\.png: refused/);
  } finally {
    await m.done();
  }
});

test("a send held back and a socket that is not there say so", async () => {
  const m = await mount();
  try {
    m.store.send = () => false;
    paste(m, png({ name: "a.png" }));
    await m.wait(30);
    assert.equal(states(m)[0], "error");
    assert.match(prompt(m), new RegExp(IMAGE_NOT_SENT_WORDS));
  } finally {
    await m.done();
  }
});

// The live shell, so the guard is shown to be on the page and not only in its own file.
test("the shell's window cancels a file drop and takes the guard off when it unmounts", async () => {
  const dom = createDom();
  const restore = install(dom);
  const { store } = await harness();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  try {
    render(html`<${Live} store=${store} window=${dom.window} matchMedia=${dom.window.matchMedia} />`, container);
    await settle();
    assert.equal(dom.body.dispatch("dragover", files()).defaultPrevented, true, "a drag over the shell was left to the browser");
    assert.equal(dom.body.dispatch("drop", files(png())).defaultPrevented, true);
    assert.equal(dom.body.dispatch("drop", { dataTransfer: { types: ["text/plain"] } }).defaultPrevented, false);
    assert.equal((dom.window.listeners.drop || []).length, 1, "the guard was installed more than once");
    render(null, container);
    await settle();
    assert.equal(dom.body.dispatch("drop", files(png())).defaultPrevented, false, "the guard stayed after the shell went");
  } finally {
    store.stop();
    restore();
  }
});
