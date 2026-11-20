// The desk's "Control agents" switch for a paired device (tier B of the sealed
// connection): the command it sends and when it is offered.
import { test } from "node:test";
import assert from "node:assert/strict";
import { CONTROL_ACK_WORDS, approveCommand, challengeCommand, controlCommand, controlView } from "../src/lib/remote.js";

test("controlCommand: on carries the confirmation and the host's nonce, off needs none, no id sends nothing", () => {
  const ch = { id: "d1", kind: "control", nonce: "n1", v: 3 };
  assert.deepEqual(controlCommand("d1", true, ch), { cmd: "setDeviceControl", id: "d1", on: true, confirmed: true, nonce: "n1", v: 3 });
  // Without the host's challenge for this device and of this kind, nothing is sent: the host would refuse it.
  assert.equal(controlCommand("d1", true), null);
  assert.equal(controlCommand("d1", true, { ...ch, id: "d2" }), null);
  assert.equal(controlCommand("d1", true, { ...ch, nonce: "" }), null);
  assert.equal(controlCommand("d1", true, { ...ch, kind: undefined }), null, "the approving question's nonce is not this one's");
  assert.equal(approveCommand("d1", true, ch), null, "and the control nonce does not turn approving on");
  assert.deepEqual(controlCommand("d1", false), { cmd: "setDeviceControl", id: "d1", on: false });
  assert.equal(controlCommand("", true), null);
  assert.equal(controlCommand(undefined, false), null);
});

test("challengeCommand asks the control question only when told to", () => {
  assert.deepEqual(challengeCommand("d1", "control"), { cmd: "approveChallenge", id: "d1", kind: "control" });
  assert.deepEqual(challengeCommand("d1"), { cmd: "approveChallenge", id: "d1" });
  assert.equal(challengeCommand("", "control"), null);
});

test("controlView: only a full device with a verified key can be switched on", () => {
  assert.deepEqual(controlView({ role: "full", approveKey: "verified" }), { on: false, offered: true, canTurnOn: true });
  assert.deepEqual(controlView({ role: "full", approveKey: "" }), { on: false, offered: true, canTurnOn: false });
  assert.deepEqual(controlView({ role: "full", approveKey: "changed" }), { on: false, offered: true, canTurnOn: false });
  assert.deepEqual(controlView({ role: "viewer", approveKey: "verified" }), { on: false, offered: false, canTurnOn: false });
  // A device that holds it stays visible so the desk can turn it off.
  assert.equal(controlView({ role: "viewer", control: true }).offered, true);
  assert.equal(controlView({ role: "full", control: true, approveKey: "verified" }).on, true);
});

test("the wording says what the device gets and what it does not", () => {
  for (const w of ["messages", "stop", "questions", "plans once", "shell", "start new agents", "past conversation", "worktrees", "commit, push or pull", "one request at a time", "allow always", "merge"]) {
    assert.ok(CONTROL_ACK_WORDS.toLowerCase().includes(w), w);
  }
});
