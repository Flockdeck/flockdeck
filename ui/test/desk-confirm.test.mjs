import test from "node:test";
import assert from "node:assert/strict";
import { applyDeskConfirm, answerCommand, deskConfirmWords, MAX_ASKS } from "../src/lib/deskconfirm.js";

const ask = (over = {}) => ({ type: "deskConfirm", id: "a1", state: "ask", nonce: "n1", action: "commit", device: "Pixel", summary: "Commit in repo on branch main", ...over });

test("a question is kept until the host says it is done", () => {
  let list = applyDeskConfirm([], ask());
  assert.equal(list.length, 1);
  assert.deepEqual(answerCommand(list[0], true), { cmd: "deskConfirmAnswer", id: "a1", nonce: "n1", on: true });
  assert.deepEqual(answerCommand(list[0], false), { cmd: "deskConfirmAnswer", id: "a1", nonce: "n1", on: false });
  list = applyDeskConfirm(list, { type: "deskConfirm", id: "a1", state: "done", outcome: "denied" });
  assert.deepEqual(list, []);
});

test("a question with no nonce or no id is not kept", () => {
  assert.deepEqual(applyDeskConfirm([], ask({ nonce: "" })), []);
  assert.deepEqual(applyDeskConfirm([], ask({ id: "" })), []);
  assert.deepEqual(applyDeskConfirm([], ask({ state: "other" })), []);
  assert.deepEqual(applyDeskConfirm(undefined, null), []);
});

test("the same id replaces its question and at most eight are kept", () => {
  let list = applyDeskConfirm([], ask());
  list = applyDeskConfirm(list, ask({ nonce: "n2" }));
  assert.equal(list.length, 1);
  assert.equal(list[0].nonce, "n2");
  for (let i = 0; i < 12; i++) list = applyDeskConfirm(list, ask({ id: "x" + i }));
  assert.equal(list.length, MAX_ASKS);
});

test("the words name the device and the action", () => {
  const w = deskConfirmWords(applyDeskConfirm([], ask({ action: "commit and push" }))[0]);
  assert.equal(w.title, "Pixel wants to commit and push");
  assert.ok(w.words[0].startsWith("Commit in repo"));
  assert.equal(deskConfirmWords({ id: "x", nonce: "n" }).title, "A paired device wants to change a repository");
});
