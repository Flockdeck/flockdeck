// Driving claude's AskUserQuestion screen with keys. The screens in the first tests are
// copied from claude 2.1.325 run in a PTY; FakeTui below does what that capture showed
// the keys do (docs/terminal-questions.md), so the driver is tested without claude.
import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeTui } from "./tuifake.mjs";
import { parseScreen, sameQuestion, drive, Mismatch, MISMATCH, KEY, cleanText, fitsRow, cellWidth } from "../src/drawer/tuiQuestion.js";

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

// --- One question, drift, width, abort, two windows ------------------------------

const ONE = `
 ☐ Color

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

Enter to select · ↑/↓ to navigate · Esc to cancel`.split("\n");

test("a single-choice question asked alone has a lone header and is read", () => {
  const s = parseScreen(ONE);
  assert.equal(s.kind, "question");
  assert.equal(s.one, true);
  assert.equal(s.tabs, 1);
  assert.ok(sameQuestion(s, questions[0]));
  // The same rows with no header line are not a question screen we know.
  assert.equal(parseScreen(ONE.filter((l) => !l.includes("☐"))), null);
});

test("a question asked alone is answered with its Enter, checked just before it, and nothing waits for a review", async () => {
  const tui = new FakeTui([questions[0]]);
  await drive([questions[0]], [ans([2])], tui.io());
  assert.equal(tui.submitted, true);
  assert.equal(tui.answerOf(0), "Blue");
  const own = new FakeTui([questions[0]]);
  await drive([questions[0]], [ans([], "Violet")], own.io());
  assert.equal(own.answerOf(0), "Violet");
  assert.ok(own.submitted);
});

test("a multiple-choice question asked alone has the usual bar and review", async () => {
  const tui = new FakeTui([questions[1]]);
  await drive([questions[1]], [ans([0, 2], "mix")], tui.io());
  assert.equal(tui.submitted, true);
  assert.equal(tui.answerOf(0), "Salt, Oil, mix");
});

test("a lone question's Enter is not pressed when the screen changed under it", async () => {
  const tui = new FakeTui([questions[0]]);
  tui.onSend = (keys) => { if (keys === KEY.down) tui.press("down"); }; // something else moved the cursor one row further
  await assert.rejects(drive([questions[0]], [ans([1])], tui.io()), Mismatch);
  assert.equal(tui.submitted, false);
});

test("a permission prompt that replaces the question after the first key stops the run without another key", async () => {
  const tui = new FakeTui(questions);
  let after = 0;
  tui.onSend = () => { if (++after === 1) tui.drift = () => ["Allow Bash to run rm -rf build?", "❯ 1. Yes", "  2. No"]; };
  await assert.rejects(drive(questions, [ans([1]), ans([0]), ans([0])], tui.io(), { stepMs: 80 }), Mismatch);
  assert.equal(tui.sent.length, 1, "keys went to a screen that was not the question: " + JSON.stringify(tui.sent));
  assert.equal(tui.submitted, false);
});

for (const [name, drift] of [
  ["a different cursor glyph", (l) => l.map((x) => x.replace("❯", "→"))],
  ["a footer and a tab bar that changed", (l) => l.map((x) => x.replace("✔ Submit  →", "Submit").replace("Enter to select", "Press enter"))],
  ["rows with no numbers", (l) => l.map((x) => x.replace(/^(.) (\d)\. /, "$1 ($2) "))],
]) {
  test("a claude that draws differently in the middle of a run (" + name + ") stops it, and Submit is not pressed", async () => {
    const tui = new FakeTui(questions);
    let n = 0;
    tui.onSend = () => { if (++n === 3) tui.drift = drift; };
    await assert.rejects(drive(questions, [ans([2]), ans([0, 1]), ans([1])], tui.io(), { stepMs: 80 }), Mismatch);
    assert.equal(tui.submitted, false);
    assert.equal(tui.cancelled, false);
    assert.ok(tui.sent.length <= 4, "kept typing: " + tui.sent.length);
  });
}

test("typed text that cannot stay on one row of the terminal is refused before any key", async () => {
  const tui = new FakeTui(questions);
  const long = "w".repeat(120);
  await assert.rejects(drive(questions, [ans([], long), ans([0]), ans([0])], tui.io()), (e) => e.reason === MISMATCH.wide);
  assert.deepEqual(tui.sent, []);
  tui.cols = 140;
  await drive(questions, [ans([], long), ans([0]), ans([0])], tui.io());
  assert.equal(tui.answerOf(0), long);
  assert.ok(fitsRow("é".repeat(40), 100) && !fitsRow("日".repeat(50), 100), "a wide character takes two columns");
});

test("a run that is aborted types nothing more", async () => {
  const tui = new FakeTui(questions);
  const ctl = new AbortController();
  tui.onSend = () => { if (tui.sent.length === 2) ctl.abort(); };
  await assert.rejects(drive(questions, [ans([1]), ans([0]), ans([0])], tui.io(), { signal: ctl.signal }), (e) => e.reason === MISMATCH.aborted);
  assert.equal(tui.sent.length, 2);
  assert.equal(tui.submitted, false);
});

test("two questions that look the same are refused", async () => {
  const same = [questions[0], { ...questions[0], header: "Again" }];
  const tui = new FakeTui(same);
  await assert.rejects(drive(same, [ans([0]), ans([0])], tui.io()), (e) => e.reason === MISMATCH.duplicate);
  assert.deepEqual(tui.sent, []);
});

test("two windows driving one terminal never submit a mix of their answers", async () => {
  for (const [a, b] of [
    [[ans([0]), ans([0]), ans([0])], [ans([2]), ans([1]), ans([1])]],
    [[ans([1]), ans([2]), ans([0])], [ans([1]), ans([2]), ans([0])]],
  ]) {
    const tui = new FakeTui(questions);
    // Each window's reads and keys take turns, one step at a time, as two sockets would.
    let turn = 0;
    const io = () => ({ ...tui.io(), wait: async () => { await Promise.resolve(); }, send: async (k) => { await Promise.resolve(); return tui.send(k); }, screen: () => { turn++; return tui.lines(); } });
    const runs = await Promise.allSettled([drive(questions, a, io(), { stepMs: 200 }), drive(questions, b, io(), { stepMs: 200 })]);
    assert.ok(turn > 0);
    assert.equal(tui.cancelled, false);
    if (tui.submitted) {
      const got = questions.map((_, i) => tui.answerOf(i)).join("|");
      const label = (set) => set.map((x, i) => (questions[i].multiSelect ? x.selected.map((o) => questions[i].options[o].label).join(", ") : questions[i].options[x.selected[0]].label)).join("|");
      assert.ok(got === label(a) || got === label(b), "a mixed answer was submitted: " + got);
    }
    assert.ok(runs.every((r) => r.status === "fulfilled" || r.reason instanceof Mismatch));
  }
});

test("wide characters are counted as two columns: CJK extensions, emoji symbols, fullwidth forms", () => {
  for (const ch of ["\u{20000}", "\u{2A6DF}", "✅", "❌", "⚡", "⭐", "⌚", "☔", "\u{1F004}", "\u{1F0CF}", "\u{1F18E}", "\u{1F191}", "\u{1F200}", "￠", "￦", "\u{1F600}", "日"]) {
    assert.equal(cellWidth(ch), 2, "U+" + ch.codePointAt(0).toString(16));
  }
  assert.equal(cellWidth("✔"), 1, "a plain check mark is one column");
  assert.equal(cellWidth("✔️"), 2, "with an emoji selector it is two");
  assert.ok(!fitsRow("✅".repeat(45), 100), "45 check-mark emoji are 90 columns");
  assert.ok(fitsRow("✅".repeat(40), 100));
});

test("a typed check-mark emoji and a CJK extension character stay on the row they are typed on", async () => {
  const tui = new FakeTui(questions);
  tui.cols = 100;
  const text = "✅ done \u{20000}\u{20001}";
  await drive(questions, [ans([], text), ans([0]), ans([0])], tui.io());
  assert.equal(tui.answerOf(0), text);
});

test("a line with a box drawn below the question is not taken for its bar", () => {
  const below = [...ONE, "", "☐ write the tests", "☒ read the docs"];
  const s = parseScreen(below);
  assert.equal(s && s.one, true, "the task list below the footer was read as the bar");
  assert.ok(sameQuestion(s, questions[0]));
  const inside = ONE.slice();
  inside.splice(ONE.findIndex((l) => l.includes("Chat about")), 0, " ☐ stray line");
  assert.ok(sameQuestion(parseScreen(inside), questions[0]));
  assert.equal(parseScreen([...SINGLE, " ☐ not a bar either"]).tabs, 3);
});
