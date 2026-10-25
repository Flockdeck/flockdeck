import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { render } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { DiffViewer } from "../src/drawer/diff/DiffViewer.js";
import { Composer, Thread, CommentDrawer } from "../src/drawer/diff/Comments.js";
import { useCommentFlow, flowReducer, initialFlow, mergeComments } from "../src/drawer/diff/commentFlow.js";
import {
  checkComment, deliveryOf, stateOf, isOutdated, commentsAt, forDrawer, whereWords, addCommand, closeCommand, refusalWords, lineKey, MAX_COMMENT,
} from "../src/drawer/diff/commentModel.js";
import { createDom, install, settle } from "./dom.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", "built-changes-" + name + ".json"), "utf8"));
const hunksFile = fixture("04-diff-hunks");
const added = fixture("13-result-comment-added");
const deskOnly = fixture("14-result-desk-only");
const headMoved = fixture("11-result-head-moved");
const HEAD = added.comment.atSha;
const file = { path: hunksFile.path, status: "M", added: 2, removed: 1 };

const c = (over = {}) => ({ id: "c1", pane: "p", path: "a.go", side: "new", line: 5, atSha: "aaa", text: "fix", author: "you", to: "p2", state: "open", delivery: "queued", createdAt: "2026-10-21T08:00:00Z", ...over });

test("a comment is checked before it is sent", () => {
  assert.match(checkComment("  \n"), /Write a comment first/);
  assert.equal(checkComment("ok"), "");
  assert.equal(checkComment("x".repeat(MAX_COMMENT)), "");
  assert.match(checkComment("x".repeat(MAX_COMMENT + 1)), /at most 4,096 characters; this one has 4,097/);
  assert.equal(checkComment("😀".repeat(MAX_COMMENT)), "", "characters, not UTF-16 units");
});

test("delivery, state and outdated are read from the desk's fields", () => {
  assert.equal(deliveryOf(c({ delivery: undefined })), "sending");
  assert.equal(deliveryOf(c({ delivery: "queued" })), "delivered");
  assert.equal(deliveryOf(c({ delivery: "woken" })), "delivered");
  assert.equal(deliveryOf(c({ delivery: "read" })), "read");
  assert.equal(stateOf(c({ state: "resolved" })), "resolved");
  assert.equal(stateOf(c({ state: "weird" })), "open");
  assert.equal(stateOf(null), "open");
  assert.equal(isOutdated(c(), "bbb"), true);
  assert.equal(isOutdated(c(), "aaa"), false);
  assert.equal(isOutdated(c({ atSha: "" }), "bbb"), false);
});

test("comments group by place and the drawer's list puts open ones first", () => {
  const list = [c({ id: "a", line: 9 }), c({ id: "b", line: 2, state: "resolved" }), c({ id: "d", line: 9, createdAt: "2026-10-21T07:00:00Z" }), c({ id: "e", path: "z.go", line: 0 })];
  const at = commentsAt(list, "a.go");
  assert.deepEqual(at.get("new:9").map((x) => x.id), ["d", "a"], "oldest first");
  assert.deepEqual([...at.keys()].sort(), ["new:2", "new:9"]);
  assert.equal(commentsAt(list, "z.go").has("file"), true);
  assert.deepEqual(forDrawer(list).open.map((x) => x.id), ["d", "a", "e"], "by path, line, then age");
  assert.deepEqual(forDrawer(list).done.map((x) => x.id), ["b"]);
  assert.equal(lineKey("old", 3), "old:3");
  assert.equal(lineKey("new", 0), "file");
  assert.equal(whereWords(c({ side: "old", line: 4 })), "a.go, old line 4");
  assert.equal(whereWords(c({ line: 0 })), "a.go");
});

test("the commands carry expectSha and the ids the contract names", () => {
  const cmd = addCommand({ pane: "p", branch: "b", file: "a.go", side: "old", line: 7, text: "t", expectSha: "abc", req: "r1" });
  assert.deepEqual(cmd, { cmd: "commentAdd", id: "p", branch: "b", file: "a.go", side: "old", line: 7, text: "t", expectSha: "abc", req: "r1" });
  assert.equal(closeCommand("commentReopen", { pane: "p", branch: "b", commentId: "c1", req: "r" }).cmd, "commentReopen");
  assert.equal(closeCommand("anything", { pane: "p", branch: "b", commentId: "c1", req: "r" }).cmd, "commentResolve");
});

test("a refusal says what happened, from the fixtures' codes", () => {
  assert.match(refusalWords(headMoved), /new commit since you looked, so nothing was sent/);
  assert.match(refusalWords(deskOnly), /not from this window/);
  assert.match(refusalWords({ code: "unsupported" }), /does not take review comments yet/);
  assert.match(refusalWords({ code: "badRequest", error: "No such line." }), /No such line/);
  assert.match(refusalWords(null), /not sent/);
});

test("the flow: a comment is shown as sending, then replaced by the desk's, which stays until it is listed", () => {
  let s = flowReducer(initialFlow, { type: "open", target: { file: "a.go", side: "new", line: 5 } });
  s = flowReducer(s, { type: "draft", text: "fix it" });
  s = flowReducer(s, { type: "sent", req: "r1", pending: { kind: "add", target: s.composing, text: "fix it", at: "2026-10-21T09:00:00Z" } });
  const writing = mergeComments([], s);
  assert.equal(writing.length, 1);
  assert.equal(writing[0].pending, true);
  assert.equal(deliveryOf(writing[0]), "sending");
  assert.equal(flowReducer(s, { type: "cancel" }).composing !== null, true, "closing the box does not take a comment back");
  s = flowReducer(s, { type: "result", msg: { ...added, req: "r1" } });
  assert.equal(s.composing, null);
  assert.deepEqual(mergeComments([], s).map((x) => x.id), ["c4"]);
  assert.deepEqual(mergeComments([c({ id: "c4" })], s).map((x) => x.id), ["c4"], "no duplicate once listed");
  s = flowReducer(s, { type: "listed", ids: new Set(["c4"]) });
  assert.deepEqual(s.local, []);
});

test("the flow: a refusal keeps the text and the box, and says why", () => {
  let s = flowReducer(initialFlow, { type: "open", target: { file: "a.go", side: "new", line: 5 } });
  s = flowReducer(s, { type: "draft", text: "fix it" });
  s = flowReducer(s, { type: "sent", req: "r1", pending: { kind: "add", target: s.composing, text: "fix it", at: "t" } });
  s = flowReducer(s, { type: "result", msg: { ...headMoved, req: "r1", op: "commentAdd" } });
  assert.equal(s.draft, "fix it");
  assert.notEqual(s.composing, null);
  assert.match(s.error, /new commit since you looked/);
  assert.deepEqual(mergeComments([], s), []);
  assert.equal(flowReducer(s, { type: "result", msg: { req: "unknown", ok: true } }), s, "an answer to nothing is ignored");
});

test("the flow: resolve and reopen replace the listed comment until the listing catches up", () => {
  let s = flowReducer(initialFlow, { type: "sent", req: "r2", pending: { kind: "close", id: "c1" } });
  s = flowReducer(s, { type: "result", msg: { req: "r2", ok: true, comment: c({ state: "resolved" }) } });
  assert.equal(mergeComments([c()], s)[0].state, "resolved");
  s = flowReducer(s, { type: "listed", ids: new Set(["c1"]) });
  assert.equal(mergeComments([c({ state: "open" })], s)[0].state, "open", "the listing is the truth once it has the comment");
  s = flowReducer(s, { type: "sent", req: "r3", pending: { kind: "close", id: "c1" } });
  s = flowReducer(s, { type: "result", msg: { req: "r3", ok: false, code: "notFound", error: "x" } });
  assert.match(s.errors.c1, /gone/);
});

// ------------------------------------------------------------ in a page

function Host({ send, result, comments = [] }) {
  const flow = useCommentFlow({ pane: "5dc771c1", branch: "agent/cart", headSha: HEAD, send, result, comments });
  return html`<${DiffViewer} branch="agent/cart" file=${file} hunks=${hunksFile} flow=${flow} headSha=${HEAD} nameOf=${(p) => (p === "5dc771c1" ? "api" : p)} />`;
}

async function mountHost(props) {
  const dom = createDom();
  const restore = install(dom);
  const box = dom.document.createElement("div");
  dom.body.appendChild(box);
  const draw = (p) => render(html`<${Host} ...${p} />`, box);
  draw(props);
  await settle();
  return { dom, box, draw, done: () => { render(null, box); restore(); } };
}
const plusFor = (box, re) => box.findAll(".dv-plus").find((b) => re.test(b.getAttribute("aria-label")));

test("a + opens the composer on its line, Ctrl+Enter sends with expectSha, and the answer closes it and puts the keyboard back", async () => {
  const sent = [];
  const m = await mountHost({ send: (cmd) => { sent.push(cmd); return true; } });
  try {
    const plus = plusFor(m.box, /^Comment on line 57 of /);
    plus.click();
    await settle();
    const ta = m.box.find(".dv-composer-text");
    assert.ok(ta, "no composer");
    assert.equal(m.dom.document.activeElement, ta, "the box takes the keyboard");
    assert.match(ta.getAttribute("aria-label"), /Comment on line 57 of internal\/limit\/limit\.go/);
    ta.enter("Add a test for the boundary.");
    await settle();
    ta.keydown("Enter", { ctrlKey: true });
    await settle();
    assert.equal(sent.length, 1);
    const { req, ...cmd } = sent[0];
    assert.deepEqual(cmd, { cmd: "commentAdd", id: "5dc771c1", branch: "agent/cart", file: "internal/limit/limit.go", side: "new", line: 57, text: "Add a test for the boundary.", expectSha: HEAD });
    assert.match(m.box.textContent, /Sending/, "shown as sending until the desk answers");
    assert.equal(m.box.find(".dv-composer-text").disabled, true);
    m.draw({ send: () => true, result: { ...added, req } });
    await settle();
    assert.equal(m.box.find(".dv-composer-text"), null, "the composer closed");
    assert.match(m.box.textContent, /Add a test for the boundary\./);
    assert.match(m.box.textContent, /You to api/);
    assert.equal(m.dom.document.activeElement.getAttribute("data-rid"), plus.getAttribute("data-rid"), "the keyboard went back to the +");
  } finally {
    m.done();
  }
});

test("a refused comment stays in the box with the reason, and an empty one is not sent", async () => {
  const sent = [];
  const m = await mountHost({ send: (cmd) => { sent.push(cmd); return true; } });
  try {
    plusFor(m.box, /^Comment on line 57 of /).click();
    await settle();
    m.box.find(".dv-send").click();
    await settle();
    assert.equal(sent.length, 0);
    assert.match(m.box.find(".dv-composer-error").textContent, /Write a comment first/);
    m.box.find(".dv-composer-text").enter("hello");
    await settle();
    m.box.find(".dv-send").click();
    await settle();
    m.draw({ send: () => true, result: { ...headMoved, req: sent[0].req } });
    await settle();
    assert.equal(m.box.find(".dv-composer-text").value, "hello");
    assert.match(m.box.find(".dv-composer-error").textContent, /new commit since you looked/);
    m.box.find(".dv-cancel").click();
    await settle();
    assert.equal(m.box.find(".dv-composer-text"), null);
  } finally {
    m.done();
  }
});

test("a comment from the desk shows under its line with its delivery and state, and Resolve then Reopen send the commands", async () => {
  const sent = [];
  const send = (cmd) => { sent.push(cmd); return true; };
  const listed = [{ ...added.comment, delivery: "queued" }];
  const m = await mountHost({ send, comments: listed });
  try {
    assert.match(m.box.textContent, /Add a test for the boundary\./);
    assert.match(m.box.textContent, /Delivered/);
    assert.match(m.box.textContent, /Open/);
    assert.equal(m.box.findAll(".has-comment").length, 1);
    m.box.find(".dv-resolve").click();
    await settle();
    assert.equal(sent[0].cmd, "commentResolve");
    assert.equal(sent[0].commentId, "c4");
    m.draw({ send, comments: listed, result: { req: sent[0].req, op: "commentResolve", ok: true, comment: { ...added.comment, state: "resolved" } } });
    await settle();
    assert.match(m.box.textContent, /Resolved/);
    assert.equal(m.box.findAll(".has-comment").length, 0, "a resolved comment is not marked as open");
    m.box.find(".dv-reopen").click();
    await settle();
    assert.equal(sent[1].cmd, "commentReopen");
  } finally {
    m.done();
  }
});

test("a comment written at an earlier head says it is outdated, and a refused resolve says so on the comment", async () => {
  const m = await mountHost({ send: () => true, comments: [{ ...added.comment, atSha: "0000000" }] });
  try {
    assert.match(m.box.textContent, /outdated/);
  } finally {
    m.done();
  }
  const off = await mountHost({ send: () => false, comments: [added.comment] });
  try {
    off.box.find(".dv-resolve").click();
    await settle();
    assert.match(off.box.find(".dv-cmt-error").textContent, /Not connected/);
    plusFor(off.box, /^Comment on line 57 of /).click();
    await settle();
    off.box.find(".dv-composer-text").enter("x");
    await settle();
    off.box.find(".dv-send").click();
    await settle();
    assert.match(off.box.find(".dv-composer-error").textContent, /Not connected/);
  } finally {
    off.done();
  }
});

test("the file's own comment button opens a composer for the whole file", async () => {
  const sent = [];
  const m = await mountHost({ send: (cmd) => { sent.push(cmd); return true; } });
  try {
    m.box.find(".dv-filecm-btn").click();
    await settle();
    const ta = m.box.find(".dv-composer-text");
    assert.match(ta.getAttribute("aria-label"), /^Comment on internal\/limit\/limit\.go$/);
    ta.enter("whole file");
    await settle();
    m.box.find(".dv-send").click();
    await settle();
    assert.equal(sent[0].line, 0);
  } finally {
    m.done();
  }
});

test("the composer: Escape cancels, an input method's Enter is ignored, and the count shows near the limit", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const log = [];
    const target = { file: "a.go", side: "new", line: 3 };
    render(html`<${Composer} target=${target} draft=${"x".repeat(MAX_COMMENT - 100)} onSend=${() => log.push("send")} onCancel=${() => log.push("cancel")} />`, box);
    await settle();
    assert.match(box.find(".dv-count").textContent, /3,996 of 4,096/);
    const ta = box.find(".dv-composer-text");
    ta.keydown("Enter", { ctrlKey: true, isComposing: true });
    ta.keydown("Escape");
    ta.keydown("Enter", { ctrlKey: true });
    assert.deepEqual(log, ["cancel", "send"]);
  } finally {
    restore();
  }
});

test("the drawer lists open comments first, jumps to one, and offers Reopen on a done one", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const jumped = [];
    const reopened = [];
    const list = [c({ id: "x", state: "resolved", line: 1 }), c({ id: "y", line: 8, path: "b.go" })];
    render(html`<${CommentDrawer} comments=${list} headSha="aaa" onJump=${(k) => jumped.push(k.id)} onReopen=${(id) => reopened.push(id)} />`, box);
    await settle();
    const text = box.textContent;
    assert.ok(text.indexOf("Open (1)") < text.indexOf("Done (1)"));
    box.findAll(".dv-cd-where")[0].click();
    box.find(".dv-reopen").click();
    assert.deepEqual(jumped, ["y"]);
    assert.deepEqual(reopened, ["x"]);
    assert.match(renderToString(html`<${CommentDrawer} comments=${[]} />`), /No review comments on this branch/);
    assert.equal(renderToString(html`<${Thread} comments=${[]} />`), "");
  } finally {
    restore();
  }
});

test("comment text from an agent is drawn as text", () => {
  const out = renderToString(html`<${Thread} comments=${[c({ text: "<img src=x onerror=1>", author: "evil‮" })]} headSha="aaa" />`);
  assert.doesNotMatch(out, /<img/);
});
