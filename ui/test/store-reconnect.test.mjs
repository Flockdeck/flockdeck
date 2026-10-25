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

test("an older page is refused from the reconnect's hello until the open's page has come", async () => {
  const { store, reconnect } = await open();
  const next = reconnect();
  assert.equal(store.olderConversation(), false, "the open's answer could be taken for the older page");
  assert.equal(next.commands("conversationOlder").length, 0);
  next.frame({ type: "conversationPage", id: "p1", supported: true, atStart: false, entries: [{ id: "e7", kind: "user" }] });
  assert.equal(store.olderConversation(), true);
  const [cmd] = next.commands("conversationOlder");
  assert.equal(cmd.before, "e5");
});

test("a search that was done is asked again after a reconnect", async () => {
  const { store, ws, reconnect } = await open();
  store.searchConversation("limiter");
  ws().frame({ type: "conversationSearch", id: "p1", q: "limiter", matches: [{ entryId: "e6", kind: "reply", snippet: "limiter", offset: 0 }], more: false });
  assert.equal(store.getState().search.state, "done");
  const next = reconnect();
  assert.equal(store.getState().search.state, "loading");
  assert.deepEqual(next.commands("conversationSearch").map((c) => c.q), ["limiter"]);
});

test("a request the desk never answers is given up after a wait, not left loading", async () => {
  const { store } = await open();
  const real = globalThis.setTimeout;
  const timers = [];
  globalThis.setTimeout = (fn) => { timers.push(fn); return 0; };
  try {
    store.olderConversation();
    store.requestDetail("e6");
    store.searchConversation("x");
  } finally {
    globalThis.setTimeout = real;
  }
  assert.equal(store.getState().conversation.older, "loading");
  for (const fn of timers) fn();
  assert.equal(store.getState().conversation.older, "late");
  assert.equal(store.getState().details.e6.state, "failed");
  assert.equal(store.getState().search.state, "done");
  assert.equal(store.getState().search.failed, true);
});

// Timers the store sets while `fn` runs are held, to be fired by the test.
async function held(fn) {
  const real = globalThis.setTimeout;
  const timers = [];
  globalThis.setTimeout = (f) => { timers.push(f); return 0; };
  try {
    await fn();
  } finally {
    globalThis.setTimeout = real;
  }
  return timers;
}

test("an older page that comes after the wait was given up is put before the entries held, not after", async () => {
  const { store, ws } = await open();
  const timers = await held(() => store.olderConversation());
  for (const fn of timers) fn();
  assert.equal(store.getState().conversation.older, "late");
  ws().frame({ type: "conversationPage", id: "p1", supported: true, atStart: true, entries: [{ id: "e3", kind: "user" }, { id: "e4", kind: "reply" }] });
  const c = store.getState().conversation;
  assert.deepEqual(c.entries.map((e) => e.id), ["e3", "e4", "e5", "e6"]);
  assert.equal(c.atStart, true);
  assert.equal(c.older, "idle");
});

test("an open the desk never answers does not hold Older back for good", async () => {
  const { store, reconnect } = await open();
  let next;
  const timers = await held(() => { next = reconnect(); });
  assert.equal(next.commands("conversationOpen").length, 1);
  assert.equal(store.olderConversation(), false, "refused while the open is out");
  for (const fn of timers) fn();
  assert.equal(store.olderConversation(), true);
});

test("a wait begun before a drop does not cut the wait after the reconnect short", async () => {
  const { store, reconnect } = await open();
  let next;
  const timers = await held(() => {
    store.requestDetail("e6");
    next = reconnect();
  });
  assert.equal(next.commands("conversationDetail").length, 1, "asked again by the hello");
  assert.ok(timers.length >= 2);
  timers[0]();
  assert.equal(store.getState().details.e6.state, "loading", "the first wait is over; the second is still on");
  timers[timers.length - 1]();
  assert.equal(store.getState().details.e6.state, "failed");
});

test("the matches of a search are kept while it is asked again after a reconnect", async () => {
  const { store, ws, reconnect } = await open();
  store.searchConversation("limiter");
  ws().frame({ type: "conversationSearch", id: "p1", q: "limiter", matches: [{ entryId: "e6", kind: "reply", snippet: "limiter", offset: 0 }], more: false });
  reconnect();
  const got = store.getState().search;
  assert.equal(got.state, "loading");
  assert.equal(got.matches.length, 1);
});
