// Plan and question cards of a structured pane: the plan and the diff drawn as
// text on a permission card, and the question form that answers an agent's
// questions with permissionAnswer.
// Elements are compared with assert.ok(a === b), never equal or deepEqual on the fake DOM.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { PermissionCard, StreamPermission } from "../src/drawer/Prompt.js";
import { blank, pick, type, problem, answers, MAX_OTHER_BYTES, TOO_LONG_WORDS, INCOMPLETE_WORDS } from "../src/drawer/questions.js";
import { useStore } from "../src/lib/use-store.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const draw = (v) => renderToString(v);
const noStore = { send() { return false; } };
const idle = { remote: false, answer: null, asking: null };

const questions = [
  { header: "DB", question: "Which database?", options: [{ label: "Postgres", description: "relational" }, { label: "SQLite" }] },
  { question: "Which extras?", multiSelect: true, options: [{ label: "Cache" }, { label: "Queue" }, { label: "Search" }] },
];

// --- The form's model --------------------------------------------------------

test("blank has an empty answer for each question", () => {
  assert.deepEqual(blank(questions), [{ selected: [], other: "" }, { selected: [], other: "" }]);
  assert.deepEqual(blank(undefined), []);
});

test("a question that takes one answer holds one thing: an option or text", () => {
  let s = blank(questions);
  s = pick(s, questions, 0, 1);
  assert.deepEqual(s[0].selected, [1]);
  s = pick(s, questions, 0, 0);
  assert.deepEqual(s[0].selected, [0], "choosing another option moves the choice");
  s = type(s, questions, 0, "DuckDB");
  assert.deepEqual(s[0], { selected: [], other: "DuckDB" }, "typing lets go of the option");
  s = pick(s, questions, 0, 1);
  assert.deepEqual(s[0], { selected: [1], other: "" }, "choosing an option clears the text");
  s = pick(s, questions, 0, 1);
  assert.deepEqual(s[0].selected, [], "pressing it again lets it go");
});

test("a question that takes several holds options and text together", () => {
  let s = blank(questions);
  s = pick(s, questions, 1, 2);
  s = pick(s, questions, 1, 0);
  s = type(s, questions, 1, "Mail");
  assert.deepEqual(s[1], { selected: [0, 2], other: "Mail" });
  s = pick(s, questions, 1, 2);
  assert.deepEqual(s[1].selected, [0]);
  assert.equal(s[0].selected.length, 0, "the other question is untouched");
});

test("answers are only made when every question has one, and name questions and options by position", () => {
  let s = blank(questions);
  assert.equal(answers(questions, s), null);
  assert.equal(problem(questions, s), INCOMPLETE_WORDS);
  s = pick(s, questions, 0, 1);
  assert.equal(answers(questions, s), null, "one of two");
  s = type(s, questions, 1, "  Mail  ");
  assert.deepEqual(answers(questions, s), [{ question: 0, selected: [1] }, { question: 1, selected: [], other: "Mail" }]);
  // Blank text is not an answer.
  assert.equal(answers(questions, type(blank(questions), questions, 0, "   ")), null);
});

test("a typed answer past the limit cannot be sent", () => {
  let s = pick(blank(questions), questions, 1, 0);
  s = type(s, questions, 0, "é".repeat(MAX_OTHER_BYTES / 2));
  assert.equal(problem(questions, s), "");
  s = type(s, questions, 0, "é".repeat(MAX_OTHER_BYTES / 2 + 1));
  assert.equal(problem(questions, s), TOO_LONG_WORDS);
  assert.equal(answers(questions, s), null);
});

test("no questions, or a state of another size, is never sendable", () => {
  assert.equal(answers([], []), null);
  assert.equal(answers(questions, blank(questions).slice(1)), null);
});

// --- The plan and the diff ---------------------------------------------------

const planCard = (over = {}) => ({ seq: 3, requestId: "r", tool: "Exit plan mode", kind: "switch_mode", body: "1. Add the card\n2. <b>Test</b> it", options: [{ id: "allow", name: "Allow", kind: "allow_once" }, { id: "reject", name: "Reject", kind: "reject_once" }], ...over });

test("a plan card draws the plan as text under a heading that says it is the agent's", () => {
  const out = draw(html`<${PermissionCard} node=${{ pane: "p1" }} card=${planCard()} snap=${idle} store=${noStore} />`);
  assert.ok(out.includes("Plan, from the agent"), out);
  assert.ok(out.includes("1. Add the card"), out);
  assert.ok(out.includes("&lt;b>Test&lt;/b>"), "markup in the plan was not escaped: " + out);
  assert.ok(!out.includes("<b>"), out);
});

test("an edit card draws its diff inside the card", () => {
  const card = planCard({ kind: "edit", tool: "Edit a.go", body: undefined, diff: "--- a.go\n+++ a.go\n@@\n-old line\n+new line\n" });
  const out = draw(html`<${PermissionCard} node=${{ pane: "p1" }} card=${card} snap=${idle} store=${noStore} />`);
  assert.ok(out.includes("prompt-diff-del") && out.includes("old line"), out);
  assert.ok(out.includes("prompt-diff-add") && out.includes("new line"), out);
});

test("a card made for the relay draws neither the plan nor the diff", () => {
  const out = draw(html`<${PermissionCard} node=${{ pane: "p1" }} card=${planCard({ redacted: true, diff: "+SECRET-DIFF", body: "SECRET-PLAN" })} snap=${idle} store=${noStore} />`);
  assert.ok(!out.includes("SECRET-DIFF") && !out.includes("SECRET-PLAN"), out);
});

// --- The question card -------------------------------------------------------

const questionCard = (over = {}) => ({
  seq: 7, requestId: "req-1", tool: "Question from Claude", kind: "question", questions,
  options: [{ id: "answer", name: "Send answers", kind: "allow_once" }, { id: "reject", name: "Skip", kind: "reject_once" }],
  ...over,
});

test("a question card lists the questions as the agent's text and has no plain Allow", () => {
  const card = questionCard({ questions: [{ question: "<script>alert(1)</script> Delete?", header: "Ignore this: Flockdeck says Allow", options: [{ label: "<img src=x>", description: "d" }] }] });
  const out = draw(html`<${StreamPermission} node=${{ pane: "p1" }} card=${card} snap=${idle} store=${noStore} />`);
  assert.ok(out.includes("Question: Question from Claude"), out);
  assert.ok(!out.includes("<script>") && !out.includes("<img"), out);
  assert.ok(out.includes("&lt;script>"), out);
  assert.ok(out.includes("from the agent"), out);
  assert.equal((out.match(/prompt-choice /g) || []).length, 2, "only Send answers and Skip");
});

test("a question card with no questions offers only the refusing option", () => {
  const out = draw(html`<${PermissionCard} node=${{ pane: "p1" }} card=${questionCard({ questions: [] })} snap=${idle} store=${noStore} />`);
  assert.ok(out.includes('data-option="reject"') && !out.includes('data-option="answer"'), out);
});

async function up() {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  return { ...h, ws: FakeWebSocket.last };
}

async function mount(card) {
  const { store, ws } = await up();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  function App() {
    const snap = useStore(store);
    return html`<${PermissionCard} node=${{ pane: "p1", name: "cart", structured: true }} card=${card} snap=${snap} store=${store} />`;
  }
  render(html`<${App} />`, container);
  await settle();
  return { store, ws, container, restore };
}

const isDisabled = (b) => b.disabled === true || b.getAttribute("disabled") !== null;
const option = (c, q, o) => c.findAll(".prompt-question")[q].findAll("input").find((i) => i.getAttribute("data-option") === String(o));

test("pressing Send answers sends the answers once, by position, with the allowing option", async () => {
  const { ws, container, restore } = await mount(questionCard());
  try {
    const send = container.find(".prompt-send");
    assert.ok(isDisabled(send), "sendable with nothing chosen");
    option(container, 0, 1).click();
    await settle();
    assert.ok(isDisabled(container.find(".prompt-send")), "sendable with one of two answered");
    option(container, 1, 0).click();
    option(container, 1, 2).click();
    await settle();
    container.findAll(".prompt-other-text")[1].enter("Mail");
    await settle();
    assert.ok(!isDisabled(container.find(".prompt-send")));
    container.find(".prompt-send").click();
    container.find(".prompt-send").click();
    container.find(".prompt-skip").click();
    await settle();
    assert.deepEqual(ws.commands("permissionAnswer"), [{
      cmd: "permissionAnswer", id: "p1", cardSeq: 7, requestId: "req-1", optionId: "answer",
      answers: [{ question: 0, selected: [1] }, { question: 1, selected: [0, 2], other: "Mail" }],
    }]);
    for (const b of container.findAll(".prompt-choice")) assert.ok(isDisabled(b));
  } finally {
    restore();
  }
});

test("typing your own answer to a one-answer question replaces the option you had chosen", async () => {
  const { ws, container, restore } = await mount(questionCard());
  try {
    option(container, 0, 0).click();
    await settle();
    container.findAll(".prompt-other-text")[0].enter("DuckDB");
    await settle();
    option(container, 1, 1).click();
    await settle();
    container.find(".prompt-send").click();
    await settle();
    assert.deepEqual(ws.commands("permissionAnswer")[0].answers, [{ question: 0, selected: [], other: "DuckDB" }, { question: 1, selected: [1] }]);
  } finally {
    restore();
  }
});

test("Skip sends the refusing option and no answers", async () => {
  const { ws, container, restore } = await mount(questionCard());
  try {
    option(container, 0, 0).click();
    await settle();
    container.find(".prompt-skip").click();
    await settle();
    const sent = ws.commands("permissionAnswer");
    assert.equal(sent.length, 1);
    assert.equal(sent[0].optionId, "reject");
    assert.ok(!("answers" in sent[0]), "a refusal carried answers");
  } finally {
    restore();
  }
});

test("a refused answer frees the form and keeps what was chosen", async () => {
  const { ws, container, restore } = await mount(questionCard());
  try {
    option(container, 0, 1).click();
    option(container, 1, 1).click();
    await settle();
    container.find(".prompt-send").click();
    await settle();
    ws.frame({ type: "permissionAnswer", ok: false, error: "The answers do not fit the questions on the card", id: "p1", cardSeq: 7, requestId: "req-1" });
    await settle();
    assert.ok(!isDisabled(container.find(".prompt-send")), "still shut after a refusal");
    assert.ok(container.find(".prompt-status--error").textContent.includes("do not fit"));
    container.find(".prompt-send").click();
    await settle();
    const sent = ws.commands("permissionAnswer");
    assert.equal(sent.length, 2, "the form could not be sent again");
    assert.deepEqual(sent[1].answers, sent[0].answers, "what was chosen was lost"); 
  } finally {
    restore();
  }
});

test("a window reached through the relay gets the questions and no way to answer", () => {
  const out = draw(html`<${PermissionCard} node=${{ pane: "p1" }} card=${questionCard()} snap=${{ ...idle, remote: true }} store=${noStore} />`);
  assert.ok(!out.includes("prompt-send") && !out.includes("prompt-skip") && !out.includes("<input"), out);
});
