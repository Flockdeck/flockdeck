// Driving claude's AskUserQuestion screen with keys. The screens in the first tests are
// copied from claude 2.1.325 run in a PTY; FakeTui below does what that capture showed
// the keys do (docs/terminal-questions.md), so the driver is tested without claude.
import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeTui } from "./tuifake.mjs";
import { parseScreen, sameQuestion, drive, Mismatch, MISMATCH, KEY, cleanText } from "../src/drawer/tuiQuestion.js";

const questions = [
  { header: "Color", question: "Pick a color?", options: [{ label: "Red", description: "warm" }, { label: "Green", description: "leafy" }, { label: "Blue", description: "cool" }] },
  { header: "Extras", question: "Pick extras?", multiSelect: true, options: [{ label: "Salt", description: "s" }, { label: "Pepper", description: "p" }, { label: "Oil", description: "o" }] },
  { header: "Size", question: "Which size?", options: [{ label: "Small", description: "s" }, { label: "Large", description: "l" }] },
];

const SINGLE = `←  ☐ Color  ☐ Extras  ☐ Size  ✔ Submit  →

Pick a color?

❯ 1. Red
     warm
  2. Green
     leafy
  3. Blue
     cool
  4. Type something.
──────────────────────────────────────────────────────────────────────────────────────────────────────────────
  5. Chat about this

Enter to select · Tab/Arrow keys to navigate · Esc to cancel`.split("\n");

const MULTI = `←  ☒ Color  ☒ Extras  ☐ Size  ✔ Submit  →

Pick extras?

  1. [✔] Salt
         s
❯ 2. [ ] Pepper
         p
  3. [ ] Oil
         o
  4. [✔] xyz
     Next
──────────────────────────────────────────────────────────────────────────────────────────────────────────────
  5. Chat about this

Enter to select · Tab/Arrow keys to navigate · Esc to cancel`.split("\n");

const REVIEW = `←  ☒ Color  ☒ Extras  ☒ Size  ✔ Submit  →

Review your answers

 ● Pick a color?
   → Red
 ● Pick extras?
   → Salt, Pepper, xyz
 ● Which size?
   → Large

Ready to submit your answers?

❯ 1. Submit answers
  2. Cancel`.split("\n");

test("a single-select screen of claude is read: tabs, question, rows and the cursor", () => {
  const s = parseScreen(SINGLE);
  assert.equal(s.kind, "question");
  assert.equal(s.tabs, 3);
  assert.equal(s.question, "Pick a color?");
  assert.deepEqual(s.rows.map((r) => r.label), ["Red", "Green", "Blue", "Type something.", "Chat about this"]);
  assert.equal(s.cursor, 1);
  assert.equal(s.multi, false);
  assert.ok(sameQuestion(s, questions[0]));
  assert.ok(!sameQuestion(s, questions[2]));
});

test("a multi-select screen shows the boxes and the text typed in its row", () => {
  const s = parseScreen(MULTI);
  assert.equal(s.multi, true);
  assert.deepEqual(s.rows.map((r) => r.checked), [true, false, false, true, false]);
  assert.equal(s.rows[3].label, "xyz");
  assert.equal(s.cursor, 2);
  assert.ok(sameQuestion(s, questions[1]));
});

test("the review screen is read as one entry for each question", () => {
  const s = parseScreen(REVIEW);
  assert.equal(s.kind, "review");
  assert.deepEqual(s.entries, [
    { question: "Pick a color?", answer: "Red" },
    { question: "Pick extras?", answer: "Salt, Pepper, xyz" },
    { question: "Which size?", answer: "Large" },
  ]);
  assert.equal(s.rows[s.cursor - 1].label, "Submit answers");
});

test("a screen that is not a question is not read, and a question of another shape is not the card's", () => {
  assert.equal(parseScreen(["$ ls", "file.txt"]), null);
  assert.equal(parseScreen(null), null);
  const s = parseScreen(SINGLE);
  assert.ok(!sameQuestion(s, { ...questions[0], multiSelect: true }));
  assert.ok(!sameQuestion(s, { ...questions[0], options: questions[0].options.slice(1) }));
  assert.ok(!sameQuestion(s, { ...questions[0], question: "Pick a shade?" }));
});

const ans = (selected, other) => ({ selected, ...(other ? { other } : {}) });

test("answers are keyed in, and Submit is pressed once the review matches", async () => {
  const tui = new FakeTui(questions);
  await drive(questions, [ans([1]), ans([0, 1], "xyz"), ans([], "Huge")], tui.io());
  assert.equal(tui.submitted, true);
  assert.equal(tui.answerOf(0), "Green");
  assert.equal(tui.answerOf(1), "Salt, Pepper, xyz");
  assert.equal(tui.answerOf(2), "Huge");
  assert.ok(!tui.sent.some((k) => /^\d$/.test(k)), "a digit was sent: " + JSON.stringify(tui.sent));
});

test("a cursor the person moved by hand is read, not assumed", async () => {
  const tui = new FakeTui(questions);
  tui.press("down"); tui.press("down"); // on Blue
  await drive(questions, [ans([0]), ans([2]), ans([1])], tui.io());
  assert.equal(tui.submitted, true);
  assert.equal(tui.answerOf(0), "Red");
});

test("a question already answered by hand is gone back to and answered again", async () => {
  const tui = new FakeTui(questions);
  tui.press("enter"); // Red, by hand; now on Extras
  await drive(questions, [ans([2]), ans([1]), ans([0])], tui.io());
  assert.equal(tui.submitted, true);
  assert.equal(tui.answerOf(0), "Blue");
});

test("a multi-select question that already has boxes ticked is brought to what the form says", async () => {
  const tui = new FakeTui(questions);
  tui.press("enter"); // to Extras
  tui.press("space"); tui.press("down"); tui.press("down"); tui.press("space"); // Salt and Oil by hand
  await drive(questions, [ans([0]), ans([1]), ans([1])], tui.io());
  assert.equal(tui.answerOf(1), "Pepper");
  assert.equal(tui.submitted, true);
});

test("no key is sent when the screen is not a question", async () => {
  const io = { screen: () => ["$ ls", "a b c"], send: async () => { throw new Error("sent"); }, wait: async () => {} };
  await assert.rejects(drive(questions, [ans([0]), ans([0]), ans([0])], io), (e) => e instanceof Mismatch && e.reason === MISMATCH.notQuestion);
  await assert.rejects(drive(questions, [ans([0]), ans([0]), ans([0])], { ...io, screen: () => null }), (e) => e.reason === MISMATCH.noScreen);
});

test("a different question on screen, or a different count of questions, stops the run before any key", async () => {
  const tui = new FakeTui(questions.slice(1));
  await assert.rejects(drive(questions, [ans([0]), ans([0]), ans([0])], tui.io()), (e) => e.reason === MISMATCH.shape);
  assert.deepEqual(tui.sent, []);
  const other = new FakeTui([{ question: "Shall we?", options: [{ label: "Yes" }, { label: "No" }] }, ...questions.slice(1)]);
  await assert.rejects(drive(questions, [ans([0]), ans([0]), ans([0])], other.io()), (e) => e.reason === MISMATCH.otherQuestion);
  assert.deepEqual(other.sent, []);
});

test("text already typed in a question's text row stops the run", async () => {
  const tui = new FakeTui(questions);
  tui.cursor = 4; tui.send("old");
  await assert.rejects(drive(questions, [ans([], "new"), ans([0]), ans([0])], tui.io()), (e) => e.reason === MISMATCH.oldText);
  assert.equal(tui.submitted, false);
});

test("a terminal that does not move stops the run, and Submit is not pressed", async () => {
  const tui = new FakeTui(questions);
  const io = { ...tui.io(), send: async () => true };
  await assert.rejects(drive(questions, [ans([1]), ans([0]), ans([0])], io, { stepMs: 80 }), (e) => e.reason === MISMATCH.moved);
  assert.equal(tui.submitted, false);
});

test("a review that lists other answers than the form's is not submitted", async () => {
  const tui = new FakeTui(questions);
  const real = tui.answerOf.bind(tui);
  tui.answerOf = (i) => (i === 2 ? "Small" : real(i)); // the review says something the keys did not choose
  await assert.rejects(drive(questions, [ans([0]), ans([0]), ans([1])], tui.io()), (e) => e.reason === MISMATCH.review);
  assert.equal(tui.submitted, false);
  assert.equal(tui.cancelled, false);
});

test("the keys that are sent are arrows, space, enter and the typed text; a person who typed in the meantime fails the check", async () => {
  const tui = new FakeTui(questions);
  const io = tui.io();
  let n = 0;
  io.send = async (k) => {
    if (++n === 3) tui.press("down"); // the person moves the cursor mid-run
    return tui.send(k);
  };
  await assert.rejects(drive(questions, [ans([2]), ans([0]), ans([0])], io), Mismatch);
  assert.equal(tui.submitted, false);
});

test("typed text is cleaned to one line of printable characters", () => {
  assert.equal(cleanText("  two\nlines\t and \x1b[31m escape "), "two lines and [31m escape");
  assert.ok(Object.values(KEY).every((k) => typeof k === "string"));
});
