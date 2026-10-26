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
// The fixture is a diff of uncommitted changes (worktree true); most tests here are about a committed one.
const hunksFile = { ...fixture("04-diff-hunks"), worktree: false };
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
  const cmd = addCommand({ pane: "p", file: "a.go", side: "old", line: 7, text: "t", expectSha: "abc", req: "r1" });
  assert.deepEqual(cmd, { cmd: "commentAdd", id: "p", file: "a.go", side: "old", line: 7, text: "t", expectSha: "abc", req: "r1" });
  assert.equal(closeCommand("commentReopen", { pane: "p", commentId: "c1", req: "r" }).cmd, "commentReopen");
  assert.equal(closeCommand("anything", { pane: "p", commentId: "c1", req: "r" }).cmd, "commentResolve");
});

test("a refusal says what happened, from the fixtures' codes", () => {
  assert.match(refusalWords(headMoved), /new commit since you looked, so nothing was sent/);
  assert.match(refusalWords(deskOnly), /not from this window/);
  assert.match(refusalWords({ code: "unsupported" }), /does not take review comments yet/);
  assert.match(refusalWords({ code: "badRequest", error: "No such line." }), /No such line/);
  assert.match(refusalWords(null), /not sent/);
});

test("every refusal the desk gives a comment is worded, and its own sentence is shown as clipped text", () => {
  assert.match(refusalWords({ code: "noBranch", error: "x" }), /no longer on a branch, so nothing was sent/);
  assert.match(refusalWords({ code: "notFound", error: "x" }), /comment is gone/);
  const long = "line past the end " + "é".repeat(600);
  const out = refusalWords({ code: "badRequest", error: long });
  assert.equal(Array.from(out).length, 400);
  assert.ok(out.endsWith("…"));
  assert.equal(refusalWords({ code: "badRequest", error: "a‮b" }), "a<U+202E>b");
  assert.equal(refusalWords({ code: "badRequest", error: "" }), "The desk did not accept that comment.");
  const astral = "😀".repeat(500);
  assert.ok(refusalWords({ code: "badRequest", error: astral }).isWellFormed());
  for (const e of ["500 open comments on this branch", "the inbox of api is full", "the comment file is damaged"]) {
    assert.equal(refusalWords({ code: "badRequest", error: e }), e);
  }
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

function Host({ send, result, results = null, comments = [], connected = true, session = null, readOnly = false, drawn }) {
  const flow = useCommentFlow({ pane: "5dc771c1", drawnSha: drawn === undefined ? HEAD : drawn, send, result, results, comments, connected, session });
  return html`<${DiffViewer} branch="agent/cart" file=${file} hunks=${hunksFile} flow=${flow} readOnly=${readOnly} headSha=${HEAD} nameOf=${(p) => (p === "5dc771c1" ? "api" : p)} />`;
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
    assert.deepEqual(cmd, { cmd: "commentAdd", id: "5dc771c1", file: "internal/limit/limit.go", side: "new", line: 57, text: "Add a test for the boundary.", expectSha: HEAD });
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
    assert.match(m.box.textContent, /on an earlier commit/);
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

// ------------------------------------------------------------ review findings on the flow

const OLD = "oldoldoldold";
const sentIn = () => { const sent = []; return { sent, send: (cmd) => { sent.push(cmd); return true; } }; };
async function writeOne(m, text = "Add a test.") {
  plusFor(m.box, /^Comment on line 57 of /).click();
  await settle();
  const ta = m.box.find(".dv-composer-text");
  ta.enter(text);
  await settle();
  ta.keydown("Enter", { ctrlKey: true });
  await settle();
}

test("a comment is sent with the head the diff was read at, not the branch's head now, and not at all without one", async () => {
  const { sent, send } = sentIn();
  const m = await mountHost({ send, drawn: OLD });
  try {
    await writeOne(m);
    assert.equal(sent[0].expectSha, OLD, "the head of the listing the diff was read at; the viewer's headSha is another value");
  } finally {
    m.done();
  }
  for (const bad of ["", null]) {
    const s2 = sentIn();
    const n = await mountHost({ send: s2.send, drawn: bad });
    try {
      await writeOne(n);
      assert.equal(s2.sent.length, 0, "nothing is sent");
      assert.match(n.box.find(".dv-composer-error").textContent, /not read at a known head/);
      assert.equal(n.box.find(".dv-composer-text").value, "Add a test.", "the text stays");
    } finally {
      n.done();
    }
  }
});

test("a dropped connection ends the wait, keeps the text and says to check; nothing is sent again", async () => {
  const { sent, send } = sentIn();
  const m = await mountHost({ send, session: 1 });
  try {
    await writeOne(m);
    assert.equal(m.box.find(".dv-composer-text").disabled, true);
    m.draw({ send, session: 2 });
    await settle();
    const ta = m.box.find(".dv-composer-text");
    assert.equal(ta.disabled, false, "not stuck on pending");
    assert.equal(ta.value, "Add a test.");
    assert.match(m.box.find(".dv-composer-error").textContent, /connection dropped; check whether the comment appears/);
    assert.equal(sent.length, 1);
  } finally {
    m.done();
  }
});

test("a dropped connection ends a resolve's wait with the same words", () => {
  let s = flowReducer(initialFlow, { type: "sent", req: "r1", pending: { kind: "close", id: "c1" } });
  s = flowReducer(s, { type: "dropped" });
  assert.deepEqual(s.pending, {});
  assert.match(s.errors.c1, /check whether the comment appears/);
  assert.equal(flowReducer(initialFlow, { type: "dropped" }), initialFlow, "nothing out, nothing to say");
});

test("two answers that land between renders are both seen", async () => {
  const { sent, send } = sentIn();
  const listed = [c({ id: "c1", path: file.path, line: 57 }), c({ id: "c2", path: file.path, line: 53 })];
  const m = await mountHost({ send, comments: listed });
  try {
    m.box.findAll(".dv-resolve")[0].click();
    m.box.findAll(".dv-resolve")[1].click();
    await settle();
    assert.equal(sent.length, 2);
    const a = { type: "changesResult", req: sent[0].req, ok: true, comment: c({ id: "c1", path: file.path, line: 57, state: "resolved" }) };
    const b = { type: "changesResult", req: sent[1].req, ok: true, comment: c({ id: "c2", path: file.path, line: 53, state: "resolved" }) };
    m.draw({ send, results: [a, b], comments: listed });
    await settle();
    assert.equal(m.box.findAll(".dv-resolve").length, 0, "both are resolved");
  } finally {
    m.done();
  }
});

test("Resolve is off while its command is out", async () => {
  const { sent, send } = sentIn();
  const m = await mountHost({ send, comments: [c({ id: "c1", path: file.path, line: 57 })] });
  try {
    m.box.find(".dv-resolve").click();
    await settle();
    assert.equal(m.box.find(".dv-resolve").disabled, true);
    m.box.find(".dv-resolve").click();
    assert.equal(sent.length, 1);
  } finally {
    m.done();
  }
});

test("a draft is kept for each place when another + is opened", () => {
  const a = { file: "f", side: "new", line: 1 };
  const b = { file: "f", side: "new", line: 2 };
  let s = flowReducer(initialFlow, { type: "open", target: a });
  s = flowReducer(s, { type: "draft", text: "first" });
  s = flowReducer(s, { type: "open", target: b });
  assert.equal(s.draft, "");
  s = flowReducer(s, { type: "draft", text: "second" });
  s = flowReducer(s, { type: "open", target: a });
  assert.equal(s.draft, "first");
  s = flowReducer(s, { type: "open", target: b });
  assert.equal(s.draft, "second");
  s = flowReducer(s, { type: "cancel" });
  s = flowReducer(s, { type: "open", target: b });
  assert.equal(s.draft, "", "cancel throws the draft away");
});

test("after a refusal the box has the keyboard again, and the error is an alert", async () => {
  const { sent, send } = sentIn();
  const m = await mountHost({ send });
  try {
    await writeOne(m);
    m.draw({ send, result: { ...headMoved, req: sent[0].req } });
    await settle();
    const ta = m.box.find(".dv-composer-text");
    assert.equal(m.dom.document.activeElement, ta);
    assert.equal(m.box.find(".dv-composer-error").getAttribute("role"), "alert");
  } finally {
    m.done();
  }
});

test("read-only draws no +, no file button, no Resolve and no Reopen, and still draws the comments", async () => {
  const m = await mountHost({ send: () => true, readOnly: true, comments: [c({ id: "c1", path: file.path, line: 57 }), c({ id: "c2", path: file.path, line: 53, state: "resolved" })] });
  try {
    assert.equal(m.box.findAll(".dv-plus").length, 0);
    assert.equal(m.box.find(".dv-filecm-btn"), null);
    assert.equal(m.box.find(".dv-resolve"), null);
    assert.equal(m.box.find(".dv-reopen"), null);
    assert.match(m.box.textContent, /fix/);
  } finally {
    m.done();
  }
  assert.doesNotMatch(renderToString(html`<${Thread} comments=${[c()]} headSha="aaa" />`), /dv-resolve/, "no callback, no button");
});

test("comments on lines that are not drawn, or on the old path, are not lost", async () => {
  const lost = c({ id: "gone", path: file.path, line: 9999, text: "where did it go" });
  const m = await mountHost({ send: () => true, comments: [lost] });
  try {
    const block = m.box.find(".dv-cm--unplaced");
    assert.match(block.textContent, /1 comment is on lines not shown here/);
    assert.match(block.textContent, /line 9999/);
    assert.match(block.textContent, /where did it go/);
  } finally {
    m.done();
  }
  const at = commentsAt([c({ path: "old.go", line: 0 })], "new.go", "old.go");
  assert.equal(at.has("file"), true);
  assert.equal(commentsAt([c({ path: "old.go" })], "new.go").size, 0);
});

test("an agent is marked as an agent, so no name reads as You; text and place show hidden characters", () => {
  const out = renderToString(html`<${Thread} comments=${[c({ author: "p9", text: "a‮b" })]} headSha="aaa" nameOf=${() => "You"} />`);
  assert.match(out, /dv-agent">agent</);
  assert.match(out, /a&lt;U\+202E>b|a<U\+202E>b/);
  assert.equal(whereWords(c({ path: "a‮.go" })), "a<U+202E>.go, line 5");
  assert.doesNotMatch(renderToString(html`<${Thread} comments=${[c()]} headSha="aaa" />`), /dv-agent/);
});

test("a comment whose atSha is not the head reads as being on an earlier commit, whatever its state", () => {
  assert.match(renderToString(html`<${Thread} comments=${[c()]} headSha="bbb" />`), /on an earlier commit/);
  assert.match(renderToString(html`<${Thread} comments=${[c({ state: "resolved" })]} headSha="bbb" />`), /on an earlier commit/);
  assert.doesNotMatch(renderToString(html`<${Thread} comments=${[c()]} headSha="aaa" />`), /earlier commit/);
  assert.doesNotMatch(renderToString(html`<${Thread} comments=${[c()]} headSha="" />`), /earlier commit/);
});

test("when the diff is read again the composer closes and its text is kept for that place", () => {
  const t = { file: "f", side: "new", line: 3 };
  let s = flowReducer(initialFlow, { type: "open", target: t });
  s = flowReducer(s, { type: "draft", text: "typed on old hunks" });
  s = flowReducer(s, { type: "stale" });
  assert.equal(s.composing, null);
  assert.equal(s.draft, "");
  s = flowReducer(s, { type: "open", target: t });
  assert.equal(s.draft, "typed on old hunks");
  assert.equal(flowReducer(initialFlow, { type: "stale" }), initialFlow);
});

test("the flow: each refusal of commentAdd keeps the text and the box, and nothing is listed", () => {
  const cases = [
    ["headMoved", "x", /new commit since you looked/],
    ["badRequest", "line 900 is past the end of the file", /line 900 is past the end/],
    ["badRequest", "this branch has 500 open comments", /500 open comments/],
    ["badRequest", "the inbox of api is full", /inbox of api is full/],
    ["noBranch", "x", /no longer on a branch/],
    ["deskOnly", "x", /not from this window/],
    ["unsupported", "x", /does not take review comments/],
  ];
  for (const [code, error, want] of cases) {
    let s = flowReducer(initialFlow, { type: "open", target: { file: "a.go", side: "new", line: 5 } });
    s = flowReducer(s, { type: "draft", text: "fix it" });
    s = flowReducer(s, { type: "sent", req: "r1", pending: { kind: "add", target: s.composing, text: "fix it", at: "t" } });
    s = flowReducer(s, { type: "result", msg: { type: "changesResult", req: "r1", op: "commentAdd", ok: false, code, error } });
    assert.match(s.error, want, code);
    assert.equal(s.draft, "fix it", code);
    assert.notEqual(s.composing, null, code);
    assert.deepEqual(mergeComments([], s), [], code);
    assert.deepEqual(s.pending, {}, code);
  }
});
