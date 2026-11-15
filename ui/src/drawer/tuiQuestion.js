// Answers an AskUserQuestion prompt in a terminal pane by driving claude's own
// question screen with keys.
//
// The terminal is the only way in, so the keys go into claude's TUI. What the
// TUI does with them (captured with claude 2.1.325, see docs/terminal-questions.md):
//
//   Down / Up        move the cursor (the "❯") one row; the rows are the options,
//                    then "Type something", then "Chat about this"
//   Enter, single    picks the row under the cursor and goes to the next question;
//                    on "Type something" it takes the typed text instead
//   Space, multi     toggles the option under the cursor; Enter toggles too
//   Right            goes to the next question, from an option row (on the text
//                    row it moves the text cursor and does nothing else)
//   Left             goes back; a question answered before shows "✔" on its choice
//   a digit          jumps the cursor to that row in a single-select question but
//                    toggles it in a multi-select one, and types into the text row,
//                    so no digit is ever sent here
//   after the last   a "Review your answers" screen lists "question -> answer" and
//                    asks "1. Submit answers / 2. Cancel", the cursor on Submit
//
// The screen is not known: the person may have moved the cursor or answered a
// question by hand. So no key is sent without reading the screen first. Every
// move is checked against the screen it should produce before the next one, the
// question on screen must be the card's question, and the final Submit is sent
// only when the review screen lists exactly the answers the form holds. Anything
// else stops the run with a reason and leaves the terminal as it is.

export const KEY = Object.freeze({ down: "\x1b[B", up: "\x1b[A", left: "\x1b[D", right: "\x1b[C", enter: "\r", space: " " });

export const MISMATCH = Object.freeze({
  noScreen: "The terminal could not be read, so nothing was sent.",
  notQuestion: "The terminal is not showing the question, so nothing was sent.",
  otherQuestion: "The terminal is showing a different question, so nothing was sent.",
  shape: "The question in the terminal does not match the one on this card, so nothing was sent.",
  moved: "The terminal did not do what was expected. Check it and answer there.",
  oldText: "The terminal already has text typed in a question. Answer there.",
  review: "The terminal's review does not match these answers, so nothing was submitted. Check it and answer there.",
  notSent: "The keys did not reach the terminal.",
});

/** Mismatch is thrown when the screen is not what the next key needs. */
export class Mismatch extends Error {
  constructor(reason) {
    super(reason);
    this.reason = reason;
  }
}

const norm = (s) => String(s == null ? "" : s).replace(/\s+/g, " ").trim();
const OPTION_ROW = /^(❯|>| ) (\d+)\. (.*)$/;
const TAB_BAR = /^\s*←\s+(.*?)\s*Submit\s+→\s*$/;

/** cleanText is a typed answer as one line of printable text, which is all the TUI's text row can be given. */
export function cleanText(s) {
  return norm(String(s).replace(/[\u0000-\u001f\u007f-\u009f]/g, " "));
}

/**
 * parseScreen reads claude's question screen out of the terminal's rows (plain text, one string a row).
 * It returns null when none is showing, else
 *   { kind: "question", tabs, question, rows: [{ n, label, cursor, checked }], cursor, multi }
 *   { kind: "review", tabs, entries: [{ question, answer }], rows, cursor }
 * rows are the numbered rows. For a question the last row is "Chat about this" and the one before it is the text row.
 */
export function parseScreen(lines) {
  if (!Array.isArray(lines)) return null;
  let bar = -1;
  for (let i = lines.length - 1; i >= 0; i--) {
    if (TAB_BAR.test(lines[i])) {
      bar = i;
      break;
    }
  }
  if (bar < 0) return null;
  const tabs = (lines[bar].match(/[☐☒]/g) || []).length;
  const rows = [];
  const body = [];
  for (let i = bar + 1; i < lines.length; i++) {
    const l = lines[i];
    if (/^\s*(Enter to |Esc to )/.test(l)) break;
    if (/^\s*─{5,}\s*$/.test(l)) continue;
    const m = OPTION_ROW.exec(l);
    if (m) rows.push({ n: Number(m[2]), raw: m[3], cursor: m[1] !== " " });
    else if (rows.length === 0) body.push(l);
  }
  if (rows.length === 0 || rows.some((r, i) => r.n !== i + 1)) return null;
  const cursors = rows.filter((r) => r.cursor);
  if (cursors.length !== 1) return null;
  const cursor = cursors[0].n;
  const text = body.map((l) => l.trim()).filter(Boolean);
  if (text.length > 0 && /^Review your answers/.test(text[0])) {
    const entries = [];
    let cur = null;
    for (const t of text.slice(1)) {
      if (/^Ready to submit/.test(t)) break;
      if (t.startsWith("●")) {
        cur = { question: norm(t.slice(1)), answer: "" };
        entries.push(cur);
      } else if (cur) {
        cur.answer = norm(cur.answer + " " + t.replace(/^→\s*/, ""));
      }
    }
    return { kind: "review", tabs, entries, rows: rows.map((r) => ({ n: r.n, label: norm(r.raw), cursor: r.cursor })), cursor };
  }
  if (rows.length < 3 || text.length === 0) return null;
  const multi = /^\[[ ✔]\] /.test(rows[rows.length - 2].raw);
  const out = rows.map((r) => {
    const m = /^\[([ ✔])\] (.*)$/.exec(r.raw);
    if (m) return { n: r.n, label: norm(m[2]), cursor: r.cursor, checked: m[1] === "✔" };
    return { n: r.n, label: norm(r.raw.replace(/ ✔$/, "")), cursor: r.cursor, checked: / ✔$/.test(r.raw) };
  });
  return { kind: "question", tabs, question: norm(text.join(" ")), rows: out, cursor, multi };
}

const PLACEHOLDER = /^Type something\.?$/;

/** sameQuestion says whether a parsed question screen is for card question q: its text, and its rows (the options, then the text row and "Chat about this"). */
export function sameQuestion(scr, q) {
  if (!scr || scr.kind !== "question" || !q || scr.question !== norm(q.question)) return false;
  const opts = Array.isArray(q.options) ? q.options : [];
  if (opts.length === 0 || scr.rows.length !== opts.length + 2 || scr.multi !== !!q.multiSelect) return false;
  if (!/^Chat about this/.test(scr.rows[scr.rows.length - 1].label)) return false;
  return opts.every((o, i) => {
    const want = norm(o.label);
    const got = scr.rows[i].label;
    return want === got || (got.length > 0 && want.startsWith(got));
  });
}

/** otherRowEmpty says the text row of a question screen is still the TUI's "Type something". */
export const otherRowEmpty = (scr) => PLACEHOLDER.test(scr.rows[scr.rows.length - 2].label);

/** expectedReview is the text the review screen shows for the answer to question q: the option labels in the card's order, then the typed text, joined by ", " as the TUI does. */
export function expectedReview(q, a) {
  const opts = Array.isArray(q.options) ? q.options : [];
  const parts = (a.selected || []).slice().sort((x, y) => x - y).map((i) => norm(opts[i] && opts[i].label));
  if (a.other) parts.push(cleanText(a.other));
  return parts.join(", ");
}

/**
 * drive answers a card's questions in claude's TUI. answers is what the form sends
 * ([{ question, selected, other }], by position). io is
 *   screen()          the terminal's rows now, or null
 *   send(keys)        types keys, resolving true when they reached the socket
 *   wait(ms)          a delay (a test gives it a fake)
 * It resolves when the review was checked and Submit was sent, and rejects with a
 * Mismatch, saying why, as soon as the screen is not what the next key needs.
 */
export async function drive(questions, answers, io, { stepMs = 2500, pollMs = 40 } = {}) {
  if (!Array.isArray(questions) || !Array.isArray(answers) || questions.length === 0 || answers.length !== questions.length) throw new Mismatch(MISMATCH.shape);
  const read = () => {
    const l = io.screen();
    if (!l) throw new Mismatch(MISMATCH.noScreen);
    return parseScreen(l);
  };
  const type = async (keys) => {
    if (!(await io.send(keys))) throw new Mismatch(MISMATCH.notSent);
  };
  // until polls the screen for one that satisfies ok, and gives up with why.
  const until = async (ok, why = MISMATCH.moved) => {
    let waited = 0;
    for (;;) {
      const s = read();
      if (s && ok(s)) return s;
      if (waited >= stepMs) throw new Mismatch(why);
      await io.wait(pollMs);
      waited += pollMs;
    }
  };
  const moveTo = async (scr, row, q) => {
    const by = row - scr.cursor;
    if (by === 0) return scr;
    await type((by > 0 ? KEY.down : KEY.up).repeat(Math.abs(by)));
    return until((s) => sameQuestion(s, q) && s.cursor === row);
  };

  let s = read();
  if (!s || s.kind !== "question") throw new Mismatch(MISMATCH.notQuestion);
  if (s.tabs !== questions.length) throw new Mismatch(MISMATCH.shape);
  let at = questions.findIndex((q) => sameQuestion(s, q));
  if (at < 0) throw new Mismatch(MISMATCH.otherQuestion);
  while (at > 0) {
    await type(KEY.left);
    at--;
    s = await until((x) => sameQuestion(x, questions[at]));
  }

  for (let qi = 0; qi < questions.length; qi++) {
    const q = questions[qi];
    const a = answers[qi];
    const opts = q.options.length;
    s = await until((x) => sameQuestion(x, q), MISMATCH.otherQuestion);
    const text = a.other ? cleanText(a.other) : "";
    if (a.other && !text) throw new Mismatch(MISMATCH.shape);
    const picked = Array.isArray(a.selected) ? a.selected : [];
    if (!q.multiSelect) {
      if (picked.length + (text ? 1 : 0) !== 1) throw new Mismatch(MISMATCH.shape);
      const row = text ? opts + 1 : picked[0] + 1;
      if (!(row >= 1 && row <= opts + 1)) throw new Mismatch(MISMATCH.shape);
      if (text && !otherRowEmpty(s)) throw new Mismatch(MISMATCH.oldText);
      s = await moveTo(s, row, q);
      if (text) {
        await type(text);
        s = await until((x) => sameQuestion(x, q) && x.cursor === row && x.rows[row - 1].label === text);
      }
      await type(KEY.enter);
    } else {
      const want = new Set(picked);
      if (want.size + (text ? 1 : 0) === 0 || picked.some((i) => !(i >= 0 && i < opts))) throw new Mismatch(MISMATCH.shape);
      for (let i = 0; i < opts; i++) {
        if (s.rows[i].checked === want.has(i)) continue;
        s = await moveTo(s, i + 1, q);
        await type(KEY.space);
        const now = want.has(i);
        s = await until((x) => sameQuestion(x, q) && x.rows[i].checked === now);
      }
      if (text) {
        if (!otherRowEmpty(s)) throw new Mismatch(MISMATCH.oldText);
        s = await moveTo(s, opts + 1, q);
        await type(text);
        s = await until((x) => sameQuestion(x, q) && x.cursor === opts + 1 && x.rows[opts].label === text && x.rows[opts].checked);
      } else if (!otherRowEmpty(s) || s.rows[opts].checked) {
        throw new Mismatch(MISMATCH.oldText);
      }
      // Right leaves a text row's cursor where it is, so step off it first.
      s = await moveTo(s, opts, q);
      for (let i = 0; i < opts; i++) if (s.rows[i].checked !== want.has(i)) throw new Mismatch(MISMATCH.moved);
      await type(KEY.right);
    }
  }

  const done = await until((x) => x.kind === "review");
  if (done.entries.length !== questions.length) throw new Mismatch(MISMATCH.review);
  for (let i = 0; i < questions.length; i++) {
    const a = answers[i];
    const want = expectedReview(questions[i], { selected: a.selected, other: a.other ? cleanText(a.other) : "" });
    if (done.entries[i].question !== norm(questions[i].question) || done.entries[i].answer !== want) throw new Mismatch(MISMATCH.review);
  }
  const submit = done.rows[done.cursor - 1];
  if (!submit || !/^Submit answers/.test(submit.label)) throw new Mismatch(MISMATCH.review);
  await type(KEY.enter);
}
