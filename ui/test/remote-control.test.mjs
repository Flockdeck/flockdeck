// The desk's "Control agents" switch for a paired device (tier B of the sealed
// connection): the command it sends and when it is offered.
import { test } from "node:test";
import assert from "node:assert/strict";
import { CONTROL_ACK_WORDS, controlCommand, controlView } from "../src/lib/remote.js";

test("controlCommand: on carries the confirmation, off needs none, no id sends nothing", () => {
  assert.deepEqual(controlCommand("d1", true), { cmd: "setDeviceControl", id: "d1", on: true, confirmed: true });
  assert.deepEqual(controlCommand("d1", false), { cmd: "setDeviceControl", id: "d1", on: false });
  assert.equal(controlCommand("", true), null);
  assert.equal(controlCommand(undefined, false), null);
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
  for (const w of ["messages", "stop", "questions", "plans once", "shell", "start new agents", "past conversation", "worktrees", "allow always", "merge"]) {
    assert.ok(CONTROL_ACK_WORDS.toLowerCase().includes(w), w);
  }
});
