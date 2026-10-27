// The prompt box of a structured pane: the send key, the history of what the pane was
// sent, the draft kept across visits, the @ file list and pictures.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured, FILES_DELAY_MS, SAVE_DELAY_MS, IMAGE_UNSUPPORTED_WORDS, IMAGE_WAITING_WORDS } from "../src/drawer/Structured.js";
import {
  sendKey, rowsFor, ROWS_MAX, pushHistory, HISTORY_MAX, ENTRY_MAX, DRAFT_MAX, PANES_MAX, recall, NO_RECALL,
  onFirstLine, onLastLine, mentionAt, mentionText, insertMention, cleanFiles, loadComposer, saveComposer, pruneComposer, forgetComposerMemory,
} from "../src/drawer/composerModel.js";
import { sizeOf, tooBig, DIM_REFUSE, planImage, imageName, attachNote, pickImages, prepareImage, SEND_MAX, SOURCE_MAX, ImageError } from "../src/drawer/imageAttach.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";

// ---------------------------------------------------------------- the model

// A PNG header that declares w by h, which is all sizeOf reads.
function pngHead(w, h) {
  const b = new Uint8Array(33);
  b.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52]);
  new DataView(b.buffer).setUint32(16, w);
  new DataView(b.buffer).setUint32(20, h);
  return b.buffer;
}

test("Enter and Ctrl+Enter send, Shift+Enter is a new line, and a composing key is not taken", () => {
  assert.equal(sendKey({ key: "Enter" }), "send");
  assert.equal(sendKey({ key: "Enter", ctrlKey: true }), "send");
  assert.equal(sendKey({ key: "Enter", metaKey: true }), "send");
  assert.equal(sendKey({ key: "Enter", shiftKey: true }), "newline");
  assert.equal(sendKey({ key: "Enter", altKey: true }), null);
  assert.equal(sendKey({ key: "Enter", isComposing: true }), null);
  assert.equal(sendKey({ key: "Enter", keyCode: 229 }), null);
  assert.equal(sendKey({ key: "a" }), null);
  assert.equal(sendKey(null), null);
});

test("the box has a row for each line, between two and ten", () => {
  assert.equal(rowsFor(""), 2);
  assert.equal(rowsFor("a\nb\nc\nd"), 4);
  assert.equal(rowsFor("\n".repeat(100)), ROWS_MAX);
  assert.equal(rowsFor(null), 2);
});

test("the history keeps the latest prompts, skips repeats, empty and over-long text", () => {
  let h = [];
  h = pushHistory(h, "one");
  h = pushHistory(h, "one");
  h = pushHistory(h, "   ");
  h = pushHistory(h, "x".repeat(ENTRY_MAX + 1));
  h = pushHistory(h, "two");
  assert.deepEqual(h, ["one", "two"]);
  for (let i = 0; i < HISTORY_MAX + 10; i++) h = pushHistory(h, "p" + i);
  assert.equal(h.length, HISTORY_MAX);
  assert.equal(h[h.length - 1], "p" + (HISTORY_MAX + 9));
  assert.deepEqual(pushHistory("nope", "a"), ["a"].slice(0, 0).concat([]) && pushHistory([], "a"));
});

test("recall steps back through the prompts and forward to the draft that was being written", () => {
  const h = ["first", "second", "third"];
  assert.equal(recall([], NO_RECALL, -1, "d"), null);
  assert.equal(recall(h, NO_RECALL, 1, "d"), null, "forward from a draft goes nowhere");
  let r = recall(h, NO_RECALL, -1, "my draft");
  assert.equal(r.text, "third");
  r = recall(h, r.nav, -1, "third");
  assert.equal(r.text, "second");
  r = recall(h, r.nav, -1, "second");
  assert.equal(r.text, "first");
  assert.equal(recall(h, r.nav, -1, "first"), null, "past the oldest");
  r = recall(h, r.nav, 1, "first");
  assert.equal(r.text, "second");
  r = recall(h, r.nav, 1, "second");
  r = recall(h, r.nav, 1, "third");
  assert.equal(r.text, "my draft");
  assert.deepEqual(r.nav, NO_RECALL);
});

test("the arrows leave the text only from its first or last line, and not with a selection", () => {
  assert.equal(onFirstLine("ab\ncd", 1), true);
  assert.equal(onFirstLine("ab\ncd", 4), false);
  assert.equal(onLastLine("ab\ncd", 4), true);
  assert.equal(onLastLine("ab\ncd", 1), false);
  assert.equal(onFirstLine("ab", 0, 2), false);
  assert.equal(onLastLine("ab", 0, 2), false);
});

test("an @ word opens a mention only at the start or after white space, and only a short one", () => {
  assert.deepEqual(mentionAt("@ma", 3), { start: 0, end: 3, query: "ma" });
  assert.deepEqual(mentionAt("see @src/ma now", 11), { start: 4, end: 11, query: "src/ma" });
  assert.deepEqual(mentionAt("see @src/ma now", 8), { start: 4, end: 11, query: "src/ma" }, "the caret in the middle of the word");
  assert.deepEqual(mentionAt("a\n@x", 4), { start: 2, end: 4, query: "x" });
  assert.equal(mentionAt("mail me@example.com", 19), null);
  assert.equal(mentionAt("@a @b", 5).query, "b");
  assert.equal(mentionAt("no mention", 5), null);
  assert.equal(mentionAt("@" + "x".repeat(300), 301), null);
  assert.equal(mentionAt("@a@b", 4), null);
  assert.equal(mentionAt("@ma ", 4), null, "the word ended");
  assert.equal(mentionAt(null, 0), null);
});

test("choosing a file writes its relative path as text, in quotes when it has a space", () => {
  assert.equal(mentionText("src/main.go"), "@src/main.go ");
  assert.equal(mentionText("my dir/a b.txt"), '@"my dir/a b.txt" ');
  const text = "see @ma now";
  const m = mentionAt(text, 7);
  const r = insertMention(text, m, "src/main.go");
  assert.equal(r.text, "see @src/main.go  now");
  assert.equal(r.caret, 17);
});

test("the names of a reply are plain relative paths, a few and short", () => {
  const got = cleanFiles(["ok.go", "../up.go", "/abs.go", "a/../b", "new\nline", "esc\u001b[31m", "", 5, null, "x".repeat(301), "dir/é.txt"]);
  assert.deepEqual(got, ["ok.go", "dir/é.txt"]);
  assert.equal(cleanFiles(Array.from({ length: 200 }, (_, i) => "f" + i)).length, 50);
  assert.deepEqual(cleanFiles("nope"), []);
});

const fakeStorage = (over = {}) => {
  const data = new Map();
  return {
    data,
    getItem: (k) => (data.has(k) ? data.get(k) : null),
    setItem: (k, v) => { data.set(k, String(v)); },
    removeItem: (k) => { data.delete(k); },
    ...over,
  };
};

test("a pane's draft and history are kept in storage and come back after the memory is gone", () => {
  forgetComposerMemory();
  const s = fakeStorage();
  saveComposer("p1", { draft: "half a thought", history: ["a", "b"] }, s);
  forgetComposerMemory();
  assert.deepEqual(loadComposer("p1", s), { draft: "half a thought", history: ["a", "b"] });
  assert.deepEqual(loadComposer("p2", s), { draft: "", history: [] });
  saveComposer("p1", { draft: "", history: [] }, s);
  forgetComposerMemory();
  assert.deepEqual(loadComposer("p1", s), { draft: "", history: [] });
  assert.equal(s.data.has("flockdeck.composer.v1:p1"), false, "an empty record was kept");
});

test("what is kept is bounded: the draft, each prompt, the prompts and the panes", () => {
  forgetComposerMemory();
  const s = fakeStorage();
  saveComposer("p1", { draft: "d".repeat(DRAFT_MAX + 500), history: ["x".repeat(ENTRY_MAX + 1), ...Array.from({ length: HISTORY_MAX + 20 }, (_, i) => "h" + i)] }, s);
  forgetComposerMemory();
  const got = loadComposer("p1", s);
  assert.equal(got.draft.length, DRAFT_MAX);
  assert.equal(got.history.length, HISTORY_MAX);
  assert.ok(got.history.every((h) => h.length <= ENTRY_MAX));
  for (let i = 0; i < PANES_MAX + 5; i++) saveComposer("q" + i, { draft: "d" + i, history: [] }, s);
  forgetComposerMemory();
  assert.equal(loadComposer("q0", s).draft, "", "the least recently used pane was kept");
  assert.equal(loadComposer("q" + (PANES_MAX + 4), s).draft, "d" + (PANES_MAX + 4));
  const keys = [...s.data.keys()].filter((k) => k.startsWith("flockdeck.composer.v1:"));
  assert.ok(keys.length <= PANES_MAX, keys.length + " panes kept");
  // A record written by something else, or damaged, reads as empty.
  s.data.set("flockdeck.composer.v1:bad", "{not json");
  s.data.set("flockdeck.composer.v1:odd", JSON.stringify({ draft: 5, history: [1, "ok", null] }));
  forgetComposerMemory();
  assert.deepEqual(loadComposer("bad", s), { draft: "", history: [] });
  assert.deepEqual(loadComposer("odd", s), { draft: "", history: ["ok"] });
  assert.deepEqual(loadComposer("x".repeat(500), s), { draft: "", history: [] });
});

test("storage that throws or is missing leaves the pane's record in memory", () => {
  forgetComposerMemory();
  const broken = fakeStorage({ getItem() { throw new Error("blocked"); }, setItem() { throw new Error("full"); } });
  saveComposer("p1", { draft: "kept", history: ["a"] }, broken);
  assert.deepEqual(loadComposer("p1", broken), { draft: "kept", history: ["a"] });
  saveComposer("p2", { draft: "kept2", history: [] }, null);
  assert.equal(loadComposer("p2", null).draft, "kept2");
});

test("pictures: the type and size decide the route, and names are cut to a file name", async () => {
  assert.equal(planImage("image/png", 1000), "send");
  assert.equal(planImage("image/png", SEND_MAX + 1), "scale");
  assert.equal(planImage("image/png", SOURCE_MAX + 1), "refuse");
  assert.equal(planImage("image/svg+xml", 10), "refuse");
  assert.equal(planImage("text/html", 10), "refuse");
  assert.equal(planImage("image/png", 0), "refuse");
  assert.equal(imageName("C:\\Users\\me\\shot.png"), "shot.png");
  assert.equal(imageName("a/b/../c.png"), "c.png");
  assert.equal(imageName("x\u0007y.png"), "xy.png");
  assert.equal(imageName(""), "image");
  assert.ok(imageName("n".repeat(500)).length <= 80);
  assert.equal(attachNote("C:\\s\\a.png"), "[image: C:\\s\\a.png]");
  assert.equal(attachNote("a\nb"), "[image: ab]");
  assert.deepEqual(pickImages({ files: [{ type: "text/plain" }, { type: "image/png", n: 1 }, { type: "image/jpeg", n: 2 }] }).map((f) => f.n), [1]);
  assert.deepEqual(pickImages(null), []);
  const small = { type: "image/png", size: 3, name: "s.png", arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer };
  assert.deepEqual(await prepareImage(small, null), { name: "s.png", mediaType: "image/png", data: "AQID" });
  await assert.rejects(prepareImage({ type: "image/png", size: SEND_MAX + 1, name: "big.png", arrayBuffer: async () => pngHead(4000, 3000) }, null), ImageError);
  await assert.rejects(prepareImage({ type: "application/pdf", size: 5, name: "x" }, null), ImageError);
  // A large one is scaled down by the browser's own calls; a result still too large is refused.
  const env = (size) => ({
    createImageBitmap: async () => ({ width: 4000, height: 3000, close() {} }),
    makeCanvas: () => ({ draw() {}, toBlob: async () => ({ size, arrayBuffer: async () => new Uint8Array(size).buffer }) }),
  });
  const big = { type: "image/png", size: SEND_MAX + 1, name: "big.png", arrayBuffer: async () => pngHead(4000, 3000) };
  assert.equal((await prepareImage(big, env(10))).mediaType, "image/jpeg");
  await assert.rejects(prepareImage(big, env(SEND_MAX + 1)), ImageError);
});

// ---------------------------------------------------------------- the box

const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, ...over });
let seq = 0;

async function mount(st = push(), { pane = "p3", desk = true, keep = false } = {}) {
  if (!keep) forgetComposerMemory();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {}, files: null, attached: null });
  const draw = () => render(html`<${Structured} pane=${pane} snap=${store.getState()} st=${st} store=${store} desk=${desk} />`, container);
  draw();
  await settle();
  const m = {
    container, store,
    box: () => container.find(".structured-input"),
    set: async (patch) => { store.set(patch); draw(); await settle(); },
    took: (cmd) => m.set({ sent: { seq: ++seq, id: cmd.id, req: cmd.req } }),
    wait: async (ms) => { await new Promise((r) => setTimeout(r, ms)); draw(); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); },
  };
  return m;
}

const sentTexts = (m) => m.store.sent.filter((c) => c.cmd === "paneSend").map((c) => c.text);

test("Enter sends the text, Shift+Enter leaves a new line to the box, and an empty box sends nothing", async () => {
  const m = await mount();
  try {
    assert.equal(m.box().keydown("Enter").defaultPrevented, true);
    assert.deepEqual(m.store.sent, [], "an empty box was sent");
    m.box().enter("line one\nline two");
    await settle();
    const nl = m.box().keydown("Enter", { shiftKey: true });
    assert.equal(nl.defaultPrevented, false, "Shift+Enter was taken from the box");
    assert.deepEqual(m.store.sent, []);
    assert.equal(m.box().getAttribute("rows"), "2");
    const ev = m.box().keydown("Enter");
    assert.equal(ev.defaultPrevented, true);
    assert.deepEqual(sentTexts(m), ["line one\nline two"]);
    m.box().keydown("Enter");
    assert.equal(sentTexts(m).length, 1, "a second Enter while one is out sent again");
  } finally {
    await m.done();
  }
});

test("Ctrl+Enter sends and a composing Enter does not", async () => {
  const m = await mount();
  try {
    m.box().enter("go");
    await settle();
    m.box().keydown("Enter", { isComposing: true });
    m.box().keydown("Enter", { keyCode: 229 });
    assert.deepEqual(m.store.sent, []);
    m.box().keydown("Enter", { ctrlKey: true });
    assert.deepEqual(sentTexts(m), ["go"]);
  } finally {
    await m.done();
  }
});

test("Up and Down walk this pane's earlier prompts, and Down comes back to the draft", async () => {
  const m = await mount();
  try {
    for (const t of ["first", "second"]) {
      m.box().enter(t);
      await settle();
      m.box().keydown("Enter");
      await settle();
      await m.took(m.store.sent[m.store.sent.length - 1]);
      assert.equal(m.box().value, "");
    }
    m.box().enter("unsent draft");
    await settle();
    assert.equal(m.box().keydown("ArrowUp").defaultPrevented, true);
    await settle();
    assert.equal(m.box().value, "second");
    m.box().keydown("ArrowUp");
    await settle();
    assert.equal(m.box().value, "first");
    assert.equal(m.box().keydown("ArrowUp").defaultPrevented, false, "past the oldest the arrow is the box's");
    m.box().keydown("ArrowDown");
    await settle();
    assert.equal(m.box().value, "second");
    m.box().keydown("ArrowDown");
    await settle();
    assert.equal(m.box().value, "unsent draft");
    assert.equal(m.box().keydown("ArrowDown").defaultPrevented, false);
    // A recalled prompt that is then edited is a new draft: Down does not step from it.
    m.box().keydown("ArrowUp");
    await settle();
    m.box().enter("second, changed");
    await settle();
    assert.equal(m.box().keydown("ArrowDown").defaultPrevented, false);
  } finally {
    await m.done();
  }
});

test("Up in a draft of several lines moves the caret first, and a refused send is not remembered", async () => {
  const m = await mount();
  try {
    m.box().enter("a\nb");
    await settle();
    assert.equal(m.box().keydown("ArrowUp").defaultPrevented, false, "the caret is on the last line");
    m.box().keydown("Enter");
    await settle();
    await m.set({ notice: { seq: ++seq, error: true, kind: "send", text: "gone", req: m.store.sent[0].req } });
    assert.equal(m.box().value, "a\nb", "the refused text stayed");
    m.box().enter("");
    await settle();
    assert.equal(m.box().keydown("ArrowUp").defaultPrevented, false, "a refused prompt is in the history");
  } finally {
    await m.done();
  }
});

test("the draft and the history stay with the pane when the drawer is closed and opened again", async () => {
  let m = await mount();
  m.box().enter("one");
  await settle();
  m.box().keydown("Enter");
  await settle();
  await m.took(m.store.sent[0]);
  m.box().enter("half written");
  await settle();
  await m.done();
  m = await mount(push(), { keep: true });
  try {
    assert.equal(m.box().getAttribute("value"), "half written");
    m.box().enter("");
    await settle();
    m.box().keydown("ArrowUp");
    await settle();
    assert.equal(m.box().value, "one");
  } finally {
    await m.done();
  }
  // Another pane has its own.
  m = await mount(push({ id: "p4" }), { pane: "p4", keep: true });
  try {
    assert.equal(m.box().getAttribute("value"), "");
    assert.equal(m.box().keydown("ArrowUp").defaultPrevented, false);
  } finally {
    await m.done();
  }
});

const files = (req, list, over = {}) => ({ type: "structuredFiles", id: "p3", req, q: "", files: list, ...over });
const lastAsk = (m) => m.store.sent.filter((c) => c.cmd === "structuredFiles").pop();
const names = (m) => m.container.findAll("[data-mention]").map((e) => e.getAttribute("data-mention"));

test("an @ word asks the desk for names after a short wait and lists the reply to that ask", async () => {
  const m = await mount();
  try {
    m.box().enter("see @ma");
    await settle();
    assert.equal(lastAsk(m), undefined, "asked before the wait");
    await m.wait(FILES_DELAY_MS + 80);
    const ask = lastAsk(m);
    assert.deepEqual([ask.cmd, ask.id, ask.q], ["structuredFiles", "p3", "ma"]);
    assert.ok(ask.req);
    // A reply to another ask, or for another pane, is not shown.
    await m.set({ files: files("other", ["wrong.go"]) });
    assert.deepEqual(names(m), []);
    await m.set({ files: { ...files(ask.req, ["x.go"]), id: "p9" } });
    assert.deepEqual(names(m), []);
    await m.set({ files: files(ask.req, ["src/main.go", "src/util.go", "../up.go", "bad\nname"]) });
    assert.deepEqual(names(m), ["src/main.go", "src/util.go"]);
    assert.equal(m.box().getAttribute("role"), "combobox");
    // Keys move and choose; a choice is text in the box and nothing is sent.
    m.box().keydown("ArrowDown");
    await settle();
    const ev = m.box().keydown("Enter");
    await settle();
    assert.equal(ev.defaultPrevented, true);
    assert.equal(m.box().value, "see @src/util.go ");
    assert.deepEqual(sentTexts(m), []);
    assert.deepEqual(names(m), []);
  } finally {
    await m.done();
  }
});

test("Escape closes the file list until the text changes, and Enter then sends", async () => {
  const m = await mount();
  try {
    m.box().enter("@a");
    await m.wait(FILES_DELAY_MS + 80);
    await m.set({ files: files(lastAsk(m).req, ["a.go"]) });
    assert.deepEqual(names(m), ["a.go"]);
    const esc = m.box().keydown("Escape");
    await settle();
    assert.equal(esc.defaultPrevented, true);
    assert.deepEqual(names(m), []);
    m.box().keydown("Enter");
    assert.deepEqual(sentTexts(m), ["@a"]);
  } finally {
    await m.done();
  }
});

test("a mention in the middle of the text is replaced where it stands, and a click chooses", async () => {
  const m = await mount();
  try {
    m.box().selectionStart = m.box().selectionEnd = 7;
    m.box().enter("fix @ut in the loop");
    m.box().selectionStart = m.box().selectionEnd = 7;
    m.box().dispatch("keyup", { key: "x" });
    await m.wait(FILES_DELAY_MS + 80);
    await m.set({ files: files(lastAsk(m).req, ["pkg/util.go"], { more: true }) });
    assert.equal(m.container.find(".slash-group").textContent, "More files match. Type more of the name.");
    m.container.find('[data-mention="pkg/util.go"]').click();
    await settle();
    assert.equal(m.box().value, "fix @pkg/util.go  in the loop");
  } finally {
    await m.done();
  }
});

test("a refusal or an error from the desk is said in the list and the box still works", async () => {
  const m = await mount();
  try {
    m.box().enter("@z");
    await m.wait(FILES_DELAY_MS + 80);
    await m.set({ files: files(lastAsk(m).req, [], { error: "The pane's directory could not be read" }) });
    assert.match(m.container.find(".mention-note").textContent, /directory could not be read/);
    assert.equal(m.container.find(".mention-menu"), null, "an empty list was drawn");
    m.box().keydown("Enter");
    assert.deepEqual(sentTexts(m), ["@z"]);
  } finally {
    await m.done();
  }
});

test("a window reached through the relay has no box, no ask and no list", async () => {
  const m = await mount(push(), { desk: false });
  try {
    assert.equal(m.box(), null);
    await m.wait(FILES_DELAY_MS + 50);
    assert.deepEqual(m.store.sent, []);
  } finally {
    await m.done();
  }
});

test("text without an @ word asks for nothing", async () => {
  const m = await mount();
  try {
    m.box().enter("mail me@example.com please");
    await m.wait(FILES_DELAY_MS + 50);
    assert.equal(lastAsk(m), undefined);
  } finally {
    await m.done();
  }
});

const png = (over = {}) => ({ type: "image/png", size: 3, name: "shot.png", arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer, ...over });
const paste = (m, file) => m.box().dispatch("paste", { clipboardData: { files: [file] } });
const attaches = (m) => m.store.sent.filter((c) => c.cmd === "attachImage");

test("a picture is taken only when the agent takes pictures, and its path goes into the text", async () => {
  const no = await mount(push({ capabilities: { image: false } }));
  try {
    assert.equal(no.container.find(".structured-attach"), null);
    const ev = paste(no, png());
    await no.wait(20);
    assert.equal(ev.defaultPrevented, true);
    assert.deepEqual(attaches(no), []);
    assert.match(no.container.find(".structured-prompt").textContent, new RegExp(IMAGE_UNSUPPORTED_WORDS));
    // Text on the clipboard is left alone.
    assert.equal(no.box().dispatch("paste", { clipboardData: { files: [] } }).defaultPrevented, false);
  } finally {
    await no.done();
  }
  const m = await mount(push({ capabilities: { image: true } }));
  try {
    assert.ok(m.container.find(".structured-attach"));
    m.box().enter("look at this");
    await settle();
    paste(m, png());
    await m.wait(20);
    assert.deepEqual(attaches(m).map((c) => [c.id, c.name, c.mediaType, c.data]), [["p3", "shot.png", "image/png", "AQID"]]);
    // One at a time.
    paste(m, png());
    await m.wait(20);
    assert.equal(attaches(m).length, 1);
    assert.match(m.container.find(".structured-prompt").textContent, new RegExp(IMAGE_WAITING_WORDS));
    // The path of another pane's picture, or an old one, is not ours.
    await m.set({ attached: { type: "attachImageResult", id: "p9", path: "C:\\other.png", seq: 1 } });
    assert.equal(m.box().value, "look at this");
    await m.set({ attached: { type: "attachImageResult", id: "p3", path: "C:\\state\\a.png", seq: 2 } });
    assert.equal(m.box().value, "look at this\n[image: C:\\state\\a.png] ");
    // A result nobody asked for is not inserted.
    await m.set({ attached: { type: "attachImageResult", id: "p3", path: "C:\\state\\b.png", seq: 3 } });
    assert.equal(m.box().value, "look at this\n[image: C:\\state\\a.png] ");
  } finally {
    await m.done();
  }
});

test("a picture the desk refuses, one that is not a picture and a dropped one are said or taken", async () => {
  const m = await mount(push({ capabilities: { image: true } }));
  try {
    const ev = m.box().dispatch("drop", { dataTransfer: { files: [png({ name: "dropped.png" })] } });
    await m.wait(20);
    assert.equal(ev.defaultPrevented, true);
    assert.equal(attaches(m)[0].name, "dropped.png");
    await m.set({ attached: { type: "attachImageResult", id: "p3", error: "too many pictures attached too quickly", seq: 10 } });
    assert.equal(m.box().getAttribute("value"), "");
    assert.match(m.container.find(".structured-prompt").textContent, /too many pictures/);
    const pdf = m.box().dispatch("paste", { clipboardData: { files: [{ type: "application/pdf", size: 5, name: "a.pdf" }] } });
    assert.equal(pdf.defaultPrevented, false, "a file that is not a picture was taken from the box");
    assert.equal(attaches(m).length, 1);
  } finally {
    await m.done();
  }
});

test("a picture whose header declares too many pixels is refused before anything decodes it", async () => {
  assert.deepEqual(sizeOf(pngHead(640, 480)), { w: 640, h: 480 });
  assert.deepEqual(sizeOf(new Uint8Array([0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x40, 0x01, 0xf0, 0x00])), { w: 320, h: 240 });
  assert.deepEqual(sizeOf(new Uint8Array([0xff, 0xd8, 0xff, 0xe0, 0, 4, 0, 0, 0xff, 0xc0, 0, 11, 8, 0x01, 0xe0, 0x02, 0x80, 3])), { w: 640, h: 480 });
  const webp = new Uint8Array(30);
  webp.set([0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x58]);
  webp.set([0x0f, 0x27, 0x00, 0x1f, 0x4e, 0x00], 24);
  assert.deepEqual(sizeOf(webp), { w: 10000, h: 20000 });
  assert.equal(sizeOf(new Uint8Array([1, 2, 3])), null);
  assert.equal(tooBig({ w: DIM_REFUSE + 1, h: 10 }), true);
  assert.equal(tooBig({ w: 7000, h: 7000 }), true, "49 megapixels");
  assert.equal(tooBig({ w: 6000, h: 6000 }), false);
  assert.equal(tooBig({ w: 0, h: 5 }), true);
  let decoded = 0;
  const env = { createImageBitmap: async () => { decoded++; return { width: 1, height: 1 }; }, makeCanvas: () => { throw new Error("drawn"); } };
  const file = (w, h, size) => ({ type: "image/png", size, name: "x.png", arrayBuffer: async () => pngHead(w, h), slice() { return this; } });
  await assert.rejects(prepareImage(file(30000, 30000, SEND_MAX + 1), env), /too many pixels/);
  await assert.rejects(prepareImage(file(30000, 30000, 100), env), /too many pixels/, "a small file declaring a huge picture");
  assert.equal(decoded, 0, "a picture was decoded before its size was checked");
  await assert.rejects(prepareImage({ type: "image/png", size: SEND_MAX + 1, name: "y", arrayBuffer: async () => new Uint8Array(40).buffer }, env), /size could not be read/);
});

test("Enter sends while the list is open until a name is chosen with an arrow key; Tab takes the highlighted name", async () => {
  const m = await mount();
  try {
    m.box().enter("see @ma");
    await m.wait(FILES_DELAY_MS + 80);
    await m.set({ files: files(lastAsk(m).req, ["main.go", "make.go"]) });
    assert.deepEqual(names(m), ["main.go", "make.go"]);
    assert.equal(m.container.find(".mention-menu").childNodes.every((c) => c.getAttribute("role") === "option"), true, "something other than an option is in the listbox");
    assert.equal(m.box().keydown("Tab").defaultPrevented, true);
    await settle();
    assert.equal(m.box().value, "see @main.go ");
    // Without a choice Enter sends the prompt as it stands.
    m.box().enter("fix @ma");
    await m.wait(FILES_DELAY_MS + 80);
    await m.set({ files: files(lastAsk(m).req, ["main.go"]) });
    assert.deepEqual(names(m), ["main.go"]);
    assert.equal(m.box().keydown("Enter").defaultPrevented, true);
    assert.deepEqual(sentTexts(m), ["fix @ma"]);
  } finally {
    await m.done();
  }
});

test("in the wait after a keystroke the previous word's list is not shown and cannot be chosen from", async () => {
  const m = await mount();
  try {
    m.box().enter("@m");
    await m.wait(FILES_DELAY_MS + 80);
    await m.set({ files: files(lastAsk(m).req, ["main.go"]) });
    assert.deepEqual(names(m), ["main.go"]);
    m.box().enter("@ma");
    await settle();
    assert.deepEqual(names(m), [], "the list of the old word stayed");
    assert.equal(m.box().keydown("Tab").defaultPrevented, false);
    assert.equal(m.box().value, "@ma");
    // The reply to the old ask does not turn up for the new word either.
    await m.wait(FILES_DELAY_MS + 80);
    assert.deepEqual(names(m), []);
    await m.set({ files: files(lastAsk(m).req, ["make.go"]) });
    assert.deepEqual(names(m), ["make.go"]);
  } finally {
    await m.done();
  }
});

test("the draft is written to storage once after typing stops, and when the box goes away", async () => {
  const writes = [];
  const data = new Map();
  globalThis.localStorage = { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => { if (k.includes(":p3")) writes.push(v); data.set(k, v); }, removeItem: (k) => data.delete(k) };
  try {
    let m = await mount();
    for (const t of ["a", "ab", "abc", "abcd"]) {
      m.box().enter(t);
      await settle();
    }
    assert.equal(writes.length, 0, "written on each keystroke");
    await m.wait(SAVE_DELAY_MS + 100);
    assert.equal(writes.length, 1);
    assert.equal(JSON.parse(writes[0]).draft, "abcd");
    m.box().enter("abcde");
    await settle();
    await m.done();
    assert.equal(JSON.parse(writes[writes.length - 1]).draft, "abcde", "the draft in the box was lost when it went away");
  } finally {
    delete globalThis.localStorage;
  }
});

test("what is kept for a pane that is no longer open is dropped when the box opens", async () => {
  const data = new Map();
  const storage = { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => { data.set(k, v); }, removeItem: (k) => data.delete(k) };
  forgetComposerMemory();
  saveComposer("gone", { draft: "old", history: ["x"] }, storage);
  saveComposer("p3", { draft: "mine", history: [] }, storage);
  pruneComposer([], storage);
  assert.equal(data.has("flockdeck.composer.v1:gone"), true, "an empty list of panes pruned everything");
  pruneComposer(["p3", "other"], storage);
  assert.equal(data.has("flockdeck.composer.v1:gone"), false);
  assert.equal(data.has("flockdeck.composer.v1:p3"), true);
  forgetComposerMemory();
  assert.equal(loadComposer("gone", storage).draft, "");
  assert.equal(loadComposer("p3", storage).draft, "mine");
});
