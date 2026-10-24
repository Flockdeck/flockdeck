// The page's side of the API version: a clientHello once after each hello, the
// features the server offers, and the notices for apiMismatch and unsupported.
import { test } from "node:test";
import assert from "node:assert/strict";
import { CLIENT_LABEL, UI_API, capsOf, clientHello, hasCap, mismatchNotice, unsupportedNotice } from "../src/lib/apiversion.js";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

test("the clientHello says which version the page speaks and is not a command", () => {
  const f = clientHello();
  assert.deepEqual(f, { type: "clientHello", api: { major: UI_API.major, minor: UI_API.minor }, client: CLIENT_LABEL });
  assert.equal(f.cmd, undefined);
});

test("caps are read defensively", () => {
  assert.deepEqual(capsOf(null), []);
  assert.deepEqual(capsOf({}), []);
  assert.deepEqual(capsOf({ api: { caps: "x" } }), []);
  assert.deepEqual(capsOf({ api: { caps: ["clientHello", 3, "", "x".repeat(65), "unsupported"] } }), ["clientHello", "unsupported"]);
  assert.equal(hasCap({ api: { caps: ["a"] } }, "a"), true);
  assert.equal(hasCap({ api: { caps: ["a"] } }, "b"), false);
  assert.equal(hasCap({}, "a"), false, "a server that sends no block has no features");
});

test("the notices are the server's sentence, cut, and none for an empty one", () => {
  assert.deepEqual(mismatchNotice({ text: "Reload it." }), { text: "Reload it.", error: true });
  assert.equal(mismatchNotice({ text: "" }), null);
  assert.equal(mismatchNotice({}), null);
  assert.equal(unsupportedNotice({ text: "x".repeat(900) }).text.length, 200);
  assert.equal(unsupportedNotice(null), null);
});

test("the store sends a clientHello after each hello and shows the answers as a notice", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({ api: { major: 1, minor: 0, caps: ["clientHello"] } }));
  assert.deepEqual(ws.sent.filter((f) => f.type === "clientHello"), [clientHello()]);
  ws.frame({ type: "apiMismatch", server: { major: 2, minor: 0 }, client: { major: 1, minor: 0 }, text: "Reload it." });
  assert.equal(store.getState().notice.text, "Reload it.");
  assert.equal(store.getState().notice.error, true);
  ws.frame({ type: "unsupported", cmd: "x", text: "This version of Flockdeck does not know the x command." });
  assert.match(store.getState().notice.text, /does not know the x command/);
  ws.frame({ type: "unsupported", cmd: "x" });
  assert.match(store.getState().notice.text, /does not know/, "one with no sentence leaves the notice as it was");
  ws.frame(hello());
  assert.equal(ws.sent.filter((f) => f.type === "clientHello").length, 2, "again after the next hello");
  store.stop();
});
