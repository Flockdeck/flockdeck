// The one status line under the composer: which note wins, and that nothing is drawn when there is none.
import { test } from "node:test";
import assert from "node:assert/strict";
import { composerNotes, LEVELS, SEND_NOT_SENT_WORDS, STOP_NOT_SENT_WORDS, SHELL_HINT_WORDS, STOPPING_WORDS } from "../src/drawer/composerNotes.js";
import { INTERRUPT_HINT, INTERRUPT_HINT_WAITING } from "../src/drawer/interruptModel.js";

test("nothing to say gives null, and so does a pane that is idle", () => {
  assert.equal(composerNotes(), null);
  assert.equal(composerNotes({ status: "idle" }), null);
  assert.equal(composerNotes({ image: { text: "", level: "error" } }), null);
});

test("the levels are error, then warning, then hint", () => {
  assert.deepEqual(LEVELS, ["error", "warning", "hint"]);
  const all = { refused: "r", image: { text: "wait", level: "warning" }, stopping: true, status: "working" };
  assert.equal(composerNotes(all).level, "error");
  assert.equal(composerNotes({ ...all, refused: "" }).level, "warning");
  assert.equal(composerNotes({ ...all, refused: "", image: null }).level, "hint");
});

test("an error beats a warning and a hint however many there are", () => {
  const n = composerNotes({ stopFailed: true, image: { text: "again", level: "warning" }, status: "working", stopping: true });
  assert.deepEqual([n.id, n.level, n.text], ["stop-failed", "error", STOP_NOT_SENT_WORDS]);
});

test("inside the error level the order is the one written down", () => {
  const facts = { refused: "gone", sendFailed: true, shellOn: true, shellNote: "no", stopFailed: true, setting: "s", settingError: "se", cancelError: "c", image: { text: "i", level: "error" } };
  const clear = { refused: "", sendFailed: false, shellNote: "", stopFailed: false, setting: "", settingError: "", cancelError: "", image: null };
  const keys = ["refused", "sendFailed", "shellNote", "stopFailed", "setting", "settingError", "cancelError", "image"];
  const ids = ["refused", "send-failed", "shell-note", "stop-failed", "setting", "setting-error", "cancel-error", "image"];
  // Dropping the winner each time walks the whole list in order.
  let cur = { ...facts };
  const seen = [];
  for (let i = 0; i < keys.length; i++) {
    const n = composerNotes(cur);
    seen.push(n.id);
    cur = { ...cur, [keys[i]]: clear[keys[i]] };
    // The "!" hint outranks what is below the shell note, so the box is no longer a "!" one from here on.
    if (keys[i] === "shellNote") cur.shellOn = false;
  }
  assert.deepEqual(seen, ids);
  assert.equal(composerNotes({ ...cur, shellOn: false }), null);
});

test("the refusal of the text is an alert about the text; the words for a failed send are fixed", () => {
  const n = composerNotes({ refused: "That agent is gone, so the text was not sent." });
  assert.deepEqual([n.role, n.send], ["alert", true]);
  const m = composerNotes({ sendFailed: true });
  assert.deepEqual([m.text, m.role, m.send], [SEND_NOT_SENT_WORDS, "alert", true]);
  const c = composerNotes({ cancelError: "x" });
  assert.deepEqual([c.role, c.send], ["status", false]);
});

test("a refused shell command is only said while the draft still starts with a bang", () => {
  assert.equal(composerNotes({ shellNote: "Too many commands", shellOn: false }), null);
  const n = composerNotes({ shellNote: "Too many commands", shellOn: true });
  assert.deepEqual([n.id, n.level, n.role], ["shell-note", "error", "alert"]);
});

test("a picture note is drawn at the level it came with", () => {
  for (const level of LEVELS) assert.equal(composerNotes({ image: { text: "t", level } }).level, level);
});

test("hints: stopping, then what a bang does, then how to stop a running turn", () => {
  assert.equal(composerNotes({ stopping: true, shellOn: true, status: "working" }).text, STOPPING_WORDS);
  assert.equal(composerNotes({ shellOn: true, status: "working" }).text, SHELL_HINT_WORDS);
  const w = composerNotes({ status: "working" });
  assert.equal(w.id, "working");
  assert.ok(w.text.startsWith(INTERRUPT_HINT) && /waits in the queue/.test(w.text));
  assert.equal(composerNotes({ status: "waiting" }).text, INTERRUPT_HINT_WAITING);
  assert.equal(composerNotes({ status: "working", stopping: true }).id, "stopping");
});

test("the shell hint outranks errors and warnings that are not about sending", () => {
  const others = { stopFailed: true, setting: "s", settingError: "se", cancelError: "c", image: { text: "i", level: "error" } };
  assert.equal(composerNotes({ ...others, shellOn: true }).id, "shell-hint");
  assert.equal(composerNotes({ shellOn: true, image: { text: "wait", level: "warning" } }).id, "shell-hint");
  assert.equal(composerNotes({ ...others, shellOn: false }).id, "stop-failed");
});

test("a send failure, a refusal and the shell command's own refusal stay above the shell hint", () => {
  assert.equal(composerNotes({ shellOn: true, refused: "gone", stopFailed: true }).id, "refused");
  assert.equal(composerNotes({ shellOn: true, sendFailed: true, cancelError: "c" }).id, "send-failed");
  assert.equal(composerNotes({ shellOn: true, shellNote: "no", cancelError: "c" }).id, "shell-note");
});
