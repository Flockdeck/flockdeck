// Four questions at once, as the screenshot of a manager pane showed them: claude on a Windows console
// draws its figures in ASCII ("[ ] Tier B", "√ Submit", ">" for the cursor) and puts the question text
// behind a bar. The tabbed flow is driven end to end against FakeTui in both drawings.
import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeTui } from "./tuifake.mjs";
import { parseScreen, sameQuestion, drive, Mismatch, MISMATCH } from "../src/drawer/tuiQuestion.js";
import { problem, answers, blank, pick, type } from "../src/drawer/questions.js";

const four = [
  { header: "Tier B", question: "Tier B (Control agents) lets a paired phone run shell commands, start agents and send prompts with no desk prompt, so with the switch on it is effectively remote code execution on the desk. Ship it in this release as designed?", options: [
    { label: "Ship as designed (Recommended)", description: "Off by default, switch turned on at the desk per phone with a challenge." },
    { label: "Ship with shell off the phone", description: "Keep send, Stop, answers and start agent in tier B." },
    { label: "Hold tier B back", description: "Ship tiers A and C only." }] },
  { header: "Viewer reads", question: "Should the sealed viewer device (watch-panes role) be able to see changes and diffs at all?", options: [
    { label: "No, full devices only (Recommended)", description: "Viewers see conversations they already watch." },
    { label: "Yes, show them", description: "Viewers can read changes and diffs of known projects." }] },
  { header: "2.0 release", question: "Which desktop release number carries API 2.0?", options: [
    { label: "0.10", description: "Continue the 0.x line." }, { label: "1.0", description: "Mark the board removal as 1.0." }, { label: "Decide later", description: "Leave the number open." }] },
  { header: "Board history", question: "What should happen to that history?", multiSelect: true, options: [
    { label: "Export once, keep the file (Recommended)", description: "I run flockdeck task list." }, { label: "Disposable", description: "No export." }] },
];

const RULE = "─".repeat(110);

// The screen of the screenshot, cut to the lines claude drew.
const SHOT = [
  "> Ask me specifics",
  RULE,
  "←  [ ] Tier B  [ ] Viewer reads  [ ] 2.0 release  [ ] Board history  √ Submit  →",
  "",
  "│ " + four[0].question,
  "",
  "> 1. Ship as designed (Recommended)",
  "     Off by default, switch turned on at the desk per phone with a challenge.",
  "  2. Ship with shell off the phone",
  "     Keep send, Stop, answers and start agent in tier B.",
  "  3. Hold tier B back",
  "     Ship tiers A and C only.",
  "  4. Type something.",
  RULE,
  "  5. Chat about this",
  "",
  "Enter to select · Tab/Arrow keys to navigate · Esc to cancel",
];

// A multiple-choice tab as the same console draws it: the checked box is "[×]".
const SHOT_MULTI = [
  "←  [×] Tier B  [×] Viewer reads  [×] 2.0 release  [ ] Board history  √ Submit  →",
  "",
  "│ What should happen to that history?",
  "",
  "  1. [×] Export once, keep the file (Recommended)",
  "         I run flockdeck task list.",
  "> 2. [ ] Disposable",
  "         No export.",
  "  3. [ ] Type something",
  "     Next",
  RULE,
  "  4. Chat about this",
  "",
  "Enter to select · Tab/Arrow keys to navigate · Esc to cancel",
];

const SHOT_REVIEW = [
  "←  [×] Tier B  [×] Viewer reads  [×] 2.0 release  [×] Board history  √ Submit  →",
  "",
  "Review your answers",
  "",
  " ● Tier B (Control agents) lets a paired phone run shell commands.",
  "   → Hold tier B back",
  "",
  "Ready to submit your answers?",
  "",
  "> 1. Submit answers",
  "  2. Cancel",
];

test("the screenshot's screen is read: four tabs in ASCII boxes, the barred question, the rows", () => {
  const s = parseScreen(SHOT);
  assert.equal(s.kind, "question");
  assert.equal(s.tabs, 4);
  assert.equal(s.one, false);
  assert.equal(s.question, four[0].question);
  assert.deepEqual(s.rows.map((r) => r.label), ["Ship as designed (Recommended)", "Ship with shell off the phone", "Hold tier B back", "Type something.", "Chat about this"]);
  assert.equal(s.cursor, 1);
  assert.equal(s.multi, false);
  assert.ok(sameQuestion(s, four[0]));
  assert.ok(!sameQuestion(s, four[1]));
});

test("a multi-select tab with [×] boxes, and the review with a > cursor", () => {
  const m = parseScreen(SHOT_MULTI);
  assert.equal(m.tabs, 4);
  assert.equal(m.multi, true);
  assert.deepEqual(m.rows.map((r) => r.checked), [true, false, false, false]);
  assert.equal(m.cursor, 2);
  assert.ok(sameQuestion(m, four[3]));
  const r = parseScreen(SHOT_REVIEW);
  assert.equal(r.kind, "review");
  assert.equal(r.entries[0].answer, "Hold tier B back");
  assert.equal(r.rows[r.cursor - 1].label, "Submit answers");
});

for (const style of [{ name: "unicode", ascii: false, bars: false }, { name: "ascii with bars", ascii: true, bars: true }]) {
  const make = () => {
    const t = new FakeTui(four);
    t.ascii = style.ascii;
    t.bars = style.bars;
    return t;
  };
  test("four questions are answered through the tabs and submitted (" + style.name + ")", async () => {
    const tui = make();
    await drive(four, [{ selected: [2] }, { selected: [], other: "Only the iPad" }, { selected: [1] }, { selected: [0, 1], other: "and a note" }], tui.io());
    assert.equal(tui.submitted, true);
    assert.equal(tui.answerOf(0), "Hold tier B back");
    assert.equal(tui.answerOf(1), "Only the iPad");
    assert.equal(tui.answerOf(2), "1.0");
    assert.equal(tui.answerOf(3), "Export once, keep the file (Recommended), Disposable, and a note");
    assert.ok(!tui.sent.some((k) => /^\d$/.test(k)), "a digit was sent");
    assert.ok(!tui.cancelled);
  });

  test("a run that starts on the second tab goes back to the first (" + style.name + ")", async () => {
    const tui = make();
    tui.press("enter"); // the person answered question 1 by hand: now on question 2
    await drive(four, [{ selected: [0] }, { selected: [1] }, { selected: [2] }, { selected: [1] }], tui.io());
    assert.equal(tui.submitted, true);
    assert.equal(tui.answerOf(0), "Ship as designed (Recommended)");
  });
}

test("a screen that is not the card's question fails closed with a message and sends no key", async () => {
  const tui = new FakeTui(four);
  tui.drift = (rows) => rows.map((r) => r.replace("Hold tier B back", "Hold it"));
  await assert.rejects(drive(four, four.map(() => ({ selected: [0] })), tui.io()), (e) => e instanceof Mismatch && e.reason === MISMATCH.otherQuestion);
  assert.deepEqual(tui.sent, []);
  const wrongTabs = new FakeTui(four.slice(0, 3));
  await assert.rejects(drive(four, four.map(() => ({ selected: [0] })), wrongTabs.io()), (e) => e.reason === MISMATCH.shape);
  assert.deepEqual(wrongTabs.sent, []);
});

test("a review that lists another answer is not submitted", async () => {
  const tui = new FakeTui(four);
  tui.drift = (rows) => rows.map((r) => (r === "   → Hold tier B back" ? "   → Ship as designed (Recommended)" : r));
  await assert.rejects(drive(four, [{ selected: [2] }, { selected: [0] }, { selected: [0] }, { selected: [0] }], tui.io()), (e) => e.reason === MISMATCH.review);
  assert.equal(tui.submitted, false);
});

test("the form model: Submit needs every question answered, an Other per question counts", () => {
  let st = blank(four);
  assert.ok(problem(four, st));
  st = pick(st, four, 0, 2);
  st = type(st, four, 1, "Only the iPad");
  st = pick(st, four, 2, 1);
  assert.ok(problem(four, st), "question 4 is still open");
  st = pick(st, four, 3, 0);
  st = pick(st, four, 3, 1);
  assert.equal(problem(four, st), "");
  assert.deepEqual(answers(four, st), [
    { question: 0, selected: [2] }, { question: 1, selected: [], other: "Only the iPad" },
    { question: 2, selected: [1] }, { question: 3, selected: [0, 1] },
  ]);
});
