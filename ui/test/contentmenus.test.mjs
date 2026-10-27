// The content menus (src/shell/contentMenus.js): what each lists for the real
// component it is on, what a click on an item does, and that a part with no button
// for an action does not list it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { build, ctxElement } from "../src/shell/menus.js";
import { Team } from "../src/shell/Team.js";
import { FileList } from "../src/drawer/diff/FileList.js";
import { Row } from "../src/drawer/conv/rows.js";
import { Markdown } from "../src/drawer/conv/markdown.js";
import { Thread } from "../src/drawer/diff/Comments.js";
import { createDom, install, settle } from "./dom.mjs";

const iso = (ms) => new Date(ms).toISOString();
const NOW = Date.now();
const teamPush = {
  type: "team", root: "/work/a", project: "alpha", hasManager: true,
  roster: [{ pane: "p2", name: "api", role: "worker", status: "idle", finished: true }],
  counts: { panes: 1, live: 1, tasks: { assigned: 1 }, open: 1, messages: 1, unread: 0, unseen: 1 },
  tasks: [{ id: "T1", title: "Write the parser", state: "assigned", owner: "api", branch: "feat/parser", notes: 0, creator: { name: "you", user: true }, updated: iso(NOW), moves: [{ to: "in-progress" }], canAssign: true, canEdit: true }],
  messages: [{ id: 7, from: { name: "api", role: "worker", pane: "p2" }, to: "p1", toName: "boss", toRole: "manager", time: iso(NOW), text: "Done with the parser.", cut: true }],
};

async function page(view) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  render(view, container);
  await settle();
  const copied = [];
  const ctx = { copy: (v, what) => copied.push([v, what]), keys: [], implemented: [] };
  const menu = (sel) => {
    const el = ctxElement(container.find(sel));
    return { el, items: build(el.getAttribute("data-ctx"), el, ctx) };
  };
  return { container, copied, menu, done: () => { render(null, container); restore(); } };
}
const labels = (items) => items.filter((i) => !i.separator).map((i) => i.label);

test("a task lists Open and its moves, and a move clicks the move button", async () => {
  const sent = [];
  const p = await page(html`<${Team} team=${teamPush} root="/work/a" send=${(c) => { sent.push(c); return true; }} onClose=${() => {}} />`);
  try {
    let m = p.menu(".team-task-head");
    assert.deepEqual(labels(m.items), ["Open", "Copy task id"]);
    m.items[0].run();
    await settle();
    m = p.menu(".team-task-head");
    assert.deepEqual(labels(m.items), ["Close details", "Move to in progress", "Copy task id"]);
    const before = sent.length;
    m.items.find((i) => i.id === "move-in-progress").run();
    await settle();
    assert.ok(sent.length > before, "the move was sent");
    m.items.find((i) => i.id === "copy-id").run();
    assert.deepEqual(p.copied, [["T1", "the task id"]]);
  } finally {
    p.done();
  }
});

test("a message offers Show all only when it was cut, and copies its text", async () => {
  const p = await page(html`<${Team} team=${teamPush} root="/work/a" send=${() => true} onClose=${() => {}} />`);
  try {
    p.container.find("#team-tab-messages").click();
    await settle();
    const m = p.menu(".team-body");
    assert.deepEqual(labels(m.items), ["Show all", "Mark seen", "Copy message"]);
    m.items[2].run();
    assert.equal(p.copied[0][0], "Done with the parser.");
  } finally {
    p.done();
  }
});

test("an agent in the roster can be closed or copied", async () => {
  const p = await page(html`<${Team} team=${teamPush} root="/work/a" send=${() => true} onClose=${() => {}} />`);
  try {
    p.container.find("#team-tab-agents").click();
    await settle();
    const m = p.menu(".team-close");
    assert.deepEqual(labels(m.items), ["Close", "Copy pane id"]);
    assert.equal(m.items[0].danger, true);
  } finally {
    p.done();
  }
});

test("a changed file copies its path, and its old path when it was renamed", async () => {
  const files = [{ path: "b.go", oldPath: "a.go", status: "R" }, { path: "c.go", status: "M" }];
  const p = await page(html`<${FileList} files=${files} selected="" onPick=${() => {}} />`);
  try {
    const renamed = p.menu(".dv-filebtn");
    assert.deepEqual(labels(renamed.items), ["Open diff", "Copy path", "Copy old path"]);
    renamed.items.find((i) => i.id === "copy-old").run();
    assert.deepEqual(p.copied, [["a.go", "the old path"]]);
    const plain = p.container.findAll(".dv-fileitem")[1];
    assert.deepEqual(labels(build("diffFile", plain, { copy: () => {}, keys: [], implemented: [] })), ["Open diff", "Copy path"]);
  } finally {
    p.done();
  }
});

test("conversation rows: words are copied, a tool row expands and copies its title", async () => {
  const entries = [
    { kind: "prompt", id: "e1", text: "Fix the parser" },
    { kind: "tool", id: "e2", name: "Bash", label: "go test", status: "ok", summary: "ran" },
  ];
  const p = await page(html`<ul>${entries.map((e) => html`<${Row} key=${e.id} entry=${e} open=${e.id === "e2"} onToggle=${() => {}} />`)}</ul>`);
  try {
    const prompt = p.menu(".conv-text");
    assert.deepEqual(labels(prompt.items), ["Copy text"]);
    prompt.items[0].run();
    assert.equal(p.copied[0][0], "Fix the parser");
    const tool = p.menu(".conv-tool-title");
    assert.deepEqual(labels(tool.items), ["Collapse", "Copy title", "Copy summary"]);
  } finally {
    p.done();
  }
});

test("a Markdown link opens and copies its address; a review comment copies its text", async () => {
  const p = await page(html`<div><${Markdown} text="See [the docs](https://example.com/docs)." />
    <${Thread} comments=${[{ id: "c1", text: "Rename this", author: "you", state: "open", path: "a.go", line: 3 }]} /></div>`);
  try {
    const a = p.menu("a");
    assert.deepEqual(labels(a.items), ["Open link", "Copy link address"]);
    a.items[1].run();
    assert.deepEqual(p.copied[0], ["https://example.com/docs", "the link"]);
    const c = p.menu(".dv-cmt-text");
    assert.deepEqual(labels(c.items), ["Copy comment"]);
  } finally {
    p.done();
  }
});

test("nothing to copy and no clipboard leave no items", () => {
  const el = { getAttribute: (k) => (k === "data-path" ? "x.go" : null), querySelector: () => null };
  assert.deepEqual(build("diffFile", el, { keys: [], implemented: [] }), []);
});

test("an open tool row with no summary does not offer to copy the placeholder", async () => {
  const e = { kind: "tool", id: "e3", name: "Bash", label: "go vet", status: "ok" };
  const p = await page(html`<ul><${Row} entry=${e} open=${true} onToggle=${() => {}} /></ul>`);
  try {
    assert.deepEqual(labels(p.menu(".conv-tool-title").items), ["Collapse", "Copy title"]);
  } finally {
    p.done();
  }
});
