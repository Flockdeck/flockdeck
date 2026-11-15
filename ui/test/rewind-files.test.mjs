// Putting files back with a rewind: the model, the panel's confirm step, and Esc Esc.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { rewindMessage, resultWords, filesMessage, filesOffered, filesWhy, filesOf, restoreWords, FILES_NONE_WORDS, FILES_EMPTY_WORDS } from "../src/drawer/rewindModel.js";
import { RewindPanel } from "../src/drawer/RewindPanel.js";
import { escEsc, DOUBLE_ESC_MS } from "../src/drawer/escEscModel.js";
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
  { id: "e3", kind: "prompt", text: "second" },
];
const st = (over = {}) => ({ status: "idle", capabilities: { rewind: true }, rewindPoints: ["e1", "e3"], rewindToken: "4.0", ...over });

test("the file commands carry the confirmed list, and a rewind without one is as before", () => {
  assert.deepEqual(filesMessage("p1", "e3", "r1", "4.0"), { cmd: "structuredRewindFiles", id: "p1", req: "r1", entry: "e3", rewindToken: "4.0" });
  assert.equal(filesMessage("p1", "", "r1", "4.0"), null);
  assert.deepEqual(rewindMessage("p1", "e3", "r1", "4.0", ["a.txt", 3, "b/c.txt"]), { cmd: "structuredRewind", id: "p1", req: "r1", entry: "e3", confirm: true, rewindToken: "4.0", restoreFiles: true, filePaths: ["a.txt", "b/c.txt"] });
  assert.equal(rewindMessage("p1", "e3", "r1", "4.0").restoreFiles, undefined);
  assert.equal(rewindMessage("p1", "e3", "r1", "4.0", []).restoreFiles, true, "an empty confirmed list is still a list");
});

test("saved files are offered per prompt, with the server's reason when there are none", () => {
  assert.equal(filesOffered(st({ rewindFiles: ["e3"] }), "e3"), true);
  assert.equal(filesOffered(st({ rewindFiles: ["e3"] }), "e1"), false);
  assert.equal(filesOffered(st(), "e3"), false);
  assert.equal(filesWhy(st({ rewindFilesWhy: "Not a repo.\n" })), "Not a repo.");
  assert.equal(filesWhy(st()), "");
  assert.deepEqual(filesOf([{ path: "a.txt", action: "remove" }, { path: "b", action: "chmod" }, { path: "", action: "remove" }, null]), [{ path: "a.txt", action: "remove" }]);
});

test("answers about files are plain words with the git ref", () => {
  const r = resultWords({ ok: true, op: "structuredRewind", text: "x", restore: { written: 2, removed: 1, undo: "refs/flockdeck/rewind-undo/u1", failed: ["a.txt: busy"], error: "Some files were not put back." } });
  assert.ok(r.words.includes("2 put back, 1 deleted"));
  assert.ok(r.words.includes("refs/flockdeck/rewind-undo/u1"));
  assert.ok(r.words.includes("Not restored: a.txt: busy"));
  assert.ok(!resultWords({ ok: true, op: "structuredRewind", text: "x" }).words.includes("Files:"));
  assert.equal(restoreWords(null), "");
  assert.deepEqual(resultWords({ ok: true, op: "structuredRewindFiles", files: [{ path: "a", action: "overwrite" }] }).files, [{ path: "a", action: "overwrite" }]);
  assert.deepEqual(resultWords({ ok: false, error: "changed", files: [{ path: "a", action: "remove" }] }).files, [{ path: "a", action: "remove" }]);
});

test("the panel shows the files, sends the list the person saw, and shows a new list after a refusal", async () => {
  const m = await mount();
  try {
    const sent = [];
    const store = { send: (c) => { sent.push(c); return true; } };
    const s = st({ rewindFiles: ["e3"] });
    const draw = (result) => m.draw(html`<${RewindPanel} pane="p1" store=${store} st=${s} entries=${entries} result=${result} onText=${() => {}} onClose=${() => {}} />`);
    await draw(null);
    // e1 has no saved files: the step says so.
    m.container.findAll(".rewind-back")[0].dispatch("click");
    await settle();
    assert.equal(m.container.find(".rewind-files-show"), null);
    assert.ok(m.container.find(".rewind-files-note").textContent.includes(FILES_NONE_WORDS));
    m.container.find(".rewind-no").dispatch("click");
    await settle();
    // e3 has: ask what would change.
    m.container.findAll(".rewind-back")[1].dispatch("click");
    await settle();
    m.container.find(".rewind-files-show").dispatch("click");
    await settle();
    assert.deepEqual([sent[0].cmd, sent[0].entry, sent[0].rewindToken], ["structuredRewindFiles", "e3", "4.0"]);
    assert.equal(sent.length, 1, "nothing was rewound by asking");
    await draw({ id: "p1", req: sent[0].req, op: "structuredRewindFiles", ok: true, files: [{ path: "a.txt", action: "overwrite" }, { path: "new.txt", action: "remove" }] });
    const rows = m.container.findAll(".rewind-file");
    assert.equal(rows.length, 2);
    assert.ok(rows[1].textContent.includes("new.txt") && rows[1].textContent.includes("deleted"));
    m.container.find(".rewind-yes-files").dispatch("click");
    await settle();
    assert.deepEqual(sent[1], { cmd: "structuredRewind", id: "p1", req: sent[1].req, entry: "e3", confirm: true, rewindToken: "4.0", restoreFiles: true, filePaths: ["a.txt", "new.txt"] });
    await draw({ id: "p1", req: sent[1].req, op: "structuredRewind", ok: true, text: "second", restore: { written: 1, removed: 1, undo: "refs/flockdeck/rewind-undo/u1" } });
    assert.equal(m.container.find(".rewind-confirm"), null, "the confirm step closes when the rewind went through");
    assert.ok(m.container.find(".structured-note").textContent.includes("1 put back, 1 deleted"));
    // The desk says the files changed: the new list is shown to confirm again.
    m.container.findAll(".rewind-back")[1].dispatch("click");
    await settle();
    m.container.find(".rewind-files-show").dispatch("click");
    await settle();
    await draw({ id: "p1", req: sent[2].req, op: "structuredRewindFiles", ok: true, files: [{ path: "a.txt", action: "overwrite" }] });
    m.container.find(".rewind-yes-files").dispatch("click");
    await settle();
    await draw({ id: "p1", req: sent[3].req, op: "structuredRewind", ok: false, error: "The files changed since the list was shown.", files: [{ path: "a.txt", action: "overwrite" }, { path: "late.txt", action: "remove" }] });
    assert.ok(m.container.find(".structured-note--unsent").textContent.includes("changed"));
    assert.equal(m.container.findAll(".rewind-file").length, 2, "the new list is shown to confirm again");
  } finally {
    await m.done();
  }
});

test("a list with nothing in it says so, and the conversation alone can still go back", async () => {
  const m = await mount();
  try {
    const sent = [];
    const store = { send: (c) => { sent.push(c); return true; } };
    const s = st({ rewindFiles: ["e3"] });
    const draw = (result) => m.draw(html`<${RewindPanel} pane="p1" store=${store} st=${s} entries=${entries} result=${result} onText=${() => {}} onClose=${() => {}} />`);
    await draw(null);
    m.container.findAll(".rewind-back")[1].dispatch("click");
    await settle();
    m.container.find(".rewind-files-show").dispatch("click");
    await settle();
    await draw({ id: "p1", req: sent[0].req, op: "structuredRewindFiles", ok: true, files: [] });
    assert.ok(m.container.find(".rewind-files-note").textContent.includes(FILES_EMPTY_WORDS));
    assert.equal(m.container.find(".rewind-yes-files"), null);
    m.container.find(".rewind-yes").dispatch("click");
    await settle();
    assert.equal(sent[1].cmd, "structuredRewind");
    assert.equal(sent[1].restoreFiles, undefined);
  } finally {
    await m.done();
  }
});

test("Esc Esc opens rewind only for an idle pane that can go back, and not from text", () => {
  const key = (over = {}) => ({ key: "Escape", target: { localName: "div" }, ...over });
  const ctx = (over = {}) => ({ desk: true, offered: true, working: false, draft: "", menuOpen: false, dialogOpen: false, ...over });
  const first = escEsc(key(), 0, 1000, ctx());
  assert.deepEqual(first, { open: false, last: 1000 });
  assert.deepEqual(escEsc(key(), first.last, 1000 + DOUBLE_ESC_MS, ctx()), { open: true, last: 0 });
  assert.deepEqual(escEsc(key(), first.last, 1000 + DOUBLE_ESC_MS + 1, ctx()), { open: false, last: 1000 + DOUBLE_ESC_MS + 1 }, "too slow starts again");
  const cases = [
    ["a running turn", key(), ctx({ working: true })],
    ["no rewind", key(), ctx({ offered: false })],
    ["a phone", key(), ctx({ desk: false })],
    ["an open menu", key(), ctx({ menuOpen: true })],
    ["an open panel", key(), ctx({ dialogOpen: true })],
    ["a select", key({ target: { localName: "select" } }), ctx()],
    ["a box with text", key({ target: { localName: "textarea" } }), ctx({ draft: "hi" })],
    ["a modifier", key({ ctrlKey: true }), ctx()],
    ["a held key", key({ repeat: true }), ctx()],
    ["another key", key({ key: "a" }), ctx()],
  ];
  for (const [why, k, c] of cases) assert.deepEqual(escEsc(k, 1000, 1100, c), { open: false, last: 0 }, why);
  assert.equal(escEsc(key({ target: { localName: "textarea" } }), 1000, 1100, ctx({ draft: "" })).open, true, "an empty box counts as no text");
});
