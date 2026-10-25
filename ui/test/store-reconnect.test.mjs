// What a dropped connection leaves half-done in the store: an answer sent, an older page, an entry's detail and a search asked for.
import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

// A conversation of p1 holding e5 and e6, on an open connection that has said hello.
async function open() {
  const { store, clock } = await harness();
  store.start();
  let ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello());
  store.watchConversation("p1");
  ws.frame({ type: "conversationPage", id: "p1", supported: true, reset: true, atStart: false, entries: [{ id: "e5", kind: "user" }, { id: "e6", kind: "reply" }] });
  // reconnect drops the socket and brings the next one up with its hello.
  const reconnect = () => {
    ws.drop();
    clock.advance(60000);
    ws = FakeWebSocket.last;
    ws.open();
    ws.frame(hello());
    return ws;
  };
  return { store, ws: () => ws, reconnect };
}

const card = { cmd: "permissionAnswer", id: "p1", cardSeq: 1, requestId: "r1", allow: true };

test("a permission answer sent on a connection that dropped can be sent again on the next", async () => {
  const { store, ws, reconnect } = await open();
  assert.equal(store.send(card), true);
  assert.equal(ws().commands("permissionAnswer").length, 1);
  assert.notEqual(store.getState().asking, null);
  const next = reconnect();
  assert.equal(store.getState().asking, null, "the card still says it was answered");
  assert.equal(store.send(card), true);
  assert.equal(next.commands("permissionAnswer").length, 1, "the click wrote nothing");
});

test("an older page asked for before a drop does not take the answer to the reconnect's open", async () => {
  const { store, reconnect } = await open();
  assert.equal(store.olderConversation(), true);
  assert.equal(store.getState().conversation.older, "loading");
  const next = reconnect();
  assert.equal(store.getState().conversation.older, "idle");
  assert.equal(next.commands("conversationOpen")[0].after, "e6");
  next.frame({ type: "conversationPage", id: "p1", supported: true, atStart: false, entries: [{ id: "e7", kind: "user" }, { id: "e8", kind: "reply" }] });
  assert.deepEqual(store.getState().conversation.entries.map((e) => e.id), ["e5", "e6", "e7", "e8"]);
});

test("an entry detail and a search left loading by a drop are asked again", async () => {
  const { store, reconnect } = await open();
  store.requestDetail("e6");
  store.searchConversation("limiter");
  const next = reconnect();
  assert.deepEqual(next.commands("conversationDetail").map((c) => c.entry), ["e6"]);
  assert.deepEqual(next.commands("conversationSearch").map((c) => c.q), ["limiter"]);
  next.frame({ type: "conversationDetail", id: "p1", entry: "e6", detail: { text: "x" } });
  assert.equal(store.getState().details.e6.state, "ok");
});

test("an answered detail is not asked again after a drop", async () => {
  const { store, ws, reconnect } = await open();
  store.requestDetail("e6");
  ws().frame({ type: "conversationDetail", id: "p1", entry: "e6", detail: { text: "x" } });
  const next = reconnect();
  assert.equal(next.commands("conversationDetail").length, 0);
});

test("a teamGone the team watch took does not also end a graph watch that has no graph yet", async () => {
  const { store, ws } = await open();
  store.watchTeam("C:/a");
  store.watchGraph("C:/b");
  ws().frame({ type: "teamGone", root: "C:/a", error: "gone" });
  assert.equal(store.getState().teamGone.root, "C:/a");
  assert.equal(store.getState().graphGone, null, "the graph watch of another project was given up");
  ws().frame({ type: "graph", root: "C:/b", project: "b", teams: [], nodes: [], edges: [], solo: [], soloNeeds: [] });
  assert.equal(store.getState().graph.project, "b");
});
