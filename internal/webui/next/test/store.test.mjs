import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { useStore } from "../assets/lib/use-store.js";
import { DESK_ONLY_PHRASE } from "../assets/lib/store.js";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

// up starts the store and brings the fake socket to an open one.
async function up(extra) {
  const h = await harness(extra);
  h.store.start();
  FakeWebSocket.last.open();
  return { ...h, ws: FakeWebSocket.last };
}

const graph = (root = "C:/repo", more = {}) => ({ type: "graph", root, project: "repo", teams: [], nodes: [], edges: [], solo: [], soloNeeds: [], ...more });

test("hello, then state, then graph fill the store", async () => {
  const { store, ws } = await up();
  assert.equal(store.getState().connection, "open");
  assert.equal(store.watchGraph("C:/repo"), true);
  ws.frame(hello({ keys: [{ id: "a" }], prefs: { theme: "dark" }, e2ePublicKey: "k" }));
  assert.equal(store.getState().hello.e2ePublicKey, "k");
  assert.equal(store.getState().remote, false);
  ws.frame({ type: "state", panes: {} });
  assert.deepEqual(store.getState().state, { type: "state", panes: {} });
  ws.frame(graph());
  assert.equal(store.getState().graph.project, "repo");
  assert.equal(store.getState().graphRefused, false);
});

test("a graph watch asked for before the hello is sent with it, and with the asked root", async () => {
  const { store, ws } = await up();
  store.watchGraph("C:/repo");
  assert.equal(ws.commands("graphWatch").length, 0, "sent before the hello said whether this window is remote");
  ws.frame(hello());
  assert.deepEqual(ws.commands("graphWatch").map((c) => c.root), ["C:/repo"]);
  assert.ok(ws.commands("graphWatch")[0].req, "no request token");
});

test("a reconnect sends graphWatch again for the open project", async () => {
  const { store, clock, ws } = await up();
  ws.frame(hello());
  store.watchGraph("C:/repo");
  ws.frame(graph());
  ws.frame({ type: "state", n: 1 });
  ws.drop();
  assert.equal(store.getState().connection, "waiting");
  assert.equal(store.getState().hello, null, "the old hello is still shown");
  assert.equal(store.getState().state, null, "the old state is still shown");
  assert.notEqual(store.getState().graph, null, "the graph is kept, for a view to show as out of date");
  clock.advance(1000);
  const next = FakeWebSocket.last;
  assert.notEqual(next, ws);
  next.open();
  assert.equal(next.commands("graphWatch").length, 0, "asked before the new hello");
  next.frame(hello());
  assert.deepEqual(next.commands("graphWatch").map((c) => c.root), ["C:/repo"]);
});

test("watching another project replaces the first, and unwatching stops the reconnect watch", async () => {
  const { store, clock, ws } = await up();
  ws.frame(hello());
  store.watchGraph("C:/a");
  store.watchGraph("C:/b");
  assert.deepEqual(ws.commands("graphWatch").map((c) => c.root), ["C:/a", "C:/b"]);
  store.unwatchGraph();
  assert.equal(ws.commands("graphUnwatch").length, 1);
  ws.frame(graph("C:/b")); // a push already on its way
  assert.equal(store.getState().graph, null);
  ws.drop();
  clock.advance(1000);
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  assert.equal(FakeWebSocket.last.commands("graphWatch").length, 0);
});

test("the project on screen is watched with an empty root, and the graph's own root is kept", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  store.watchGraph("");
  assert.equal(ws.commands("graphWatch")[0].root, "");
  ws.frame(graph("C:/Real/Spelling"));
  assert.equal(store.getState().graph.root, "C:/Real/Spelling");
});

test("teamGone clears the graph and sets the state, and gives up the watch", async () => {
  const { store, clock, ws } = await up();
  ws.frame(hello());
  store.watchGraph("C:/repo");
  ws.frame(graph());
  ws.frame({ type: "teamGone", root: "C:/other", error: "not ours" });
  assert.notEqual(store.getState().graph, null, "another project's teamGone cleared this graph");
  ws.frame({ type: "teamGone", root: "C:/repo", error: "The project is not open any more." });
  const s = store.getState();
  assert.equal(s.graph, null);
  assert.deepEqual(s.graphGone, { root: "C:/repo", error: "The project is not open any more." });
  ws.drop();
  clock.advance(1000);
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  assert.equal(FakeWebSocket.last.commands("graphWatch").length, 0, "asked again for a project that is gone");
  store.watchGraph("C:/repo");
  assert.equal(store.getState().graphGone, null);
});

test("a remote hello never produces a graphWatch, before or after it", async () => {
  const { store, clock, ws } = await up();
  store.watchGraph("C:/repo"); // asked before the hello
  ws.frame(hello({ remote: true }));
  assert.equal(store.getState().remote, true);
  assert.equal(store.getState().graphRefused, true);
  assert.equal(store.watchGraph("C:/repo"), false);
  assert.equal(store.send({ cmd: "graphWatch", root: "x" }), false);
  assert.equal(store.send({ cmd: "graphSnapshot" }), false);
  store.unwatchGraph();
  ws.frame(graph());
  assert.equal(store.getState().graph, null, "a graph was kept in a remote window");
  assert.equal(ws.sent.filter((c) => String(c.cmd).startsWith("graph")).length, 0);
  ws.drop();
  clock.advance(1000);
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello({ remote: true }));
  assert.equal(FakeWebSocket.last.sent.filter((c) => String(c.cmd).startsWith("graph")).length, 0);
  // Other commands still go.
  assert.equal(store.send({ cmd: "listAgents" }), true);
});

test("stop() in a remote window sends no unwatch for a watch asked for before the hello", async () => {
  const { store, ws } = await up();
  store.watchGraph("C:/repo");
  store.watchStructured("p1");
  ws.frame(hello({ remote: true }));
  store.stop();
  const bad = ws.sent.filter((c) => c.cmd === "graphUnwatch" || c.cmd === "structuredUnwatch" || String(c.cmd).startsWith("graph"));
  assert.deepEqual(bad, [], "a graph command went to the relay on stop");
});

test("an unwatch is sent only for a watch that went out: nothing between open and the hello", async () => {
  const { store, ws } = await up();
  store.watchGraph("C:/repo");
  store.watchStructured("p1");
  store.watchStructured("p2");
  store.unwatchStructured();
  store.unwatchGraph();
  store.watchGraph("C:/repo");
  store.watchStructured("p3");
  store.stop();
  assert.deepEqual(ws.sent, [], "a command went out before the hello");
});

test("a local window still unwatches on stop and when the watch is replaced", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  store.watchGraph("C:/repo");
  store.watchStructured("p1");
  store.watchStructured("p2");
  assert.equal(ws.commands("structuredUnwatch").length, 1);
  store.stop();
  assert.equal(ws.commands("graphUnwatch").length, 1);
  assert.equal(ws.commands("structuredUnwatch").length, 2);
});

test("a desk-only command waits for the hello, and other commands do not", async () => {
  const { store, ws } = await up();
  for (const cmd of [{ cmd: "graphWatch", root: "x" }, { cmd: "graphSnapshot" }, { cmd: "permissionAnswer", id: "p1" }, { cmd: "structuredWatch", id: "p1" }, { cmd: "paneSend", id: "p1", text: "x" }]) {
    assert.equal(store.send(cmd), false, cmd.cmd + " went out before the hello");
  }
  assert.deepEqual(ws.sent, []);
  assert.equal(store.send({ cmd: "listAgents" }), true);
  ws.frame(hello());
  assert.equal(store.send({ cmd: "graphSnapshot" }), true);
  assert.equal(store.send({ cmd: "paneSend", id: "p1", text: "x" }), true);
});

test("a conversation never opened is not closed: nothing goes out between open and the hello", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  store.watchConversation("p2");
  store.unwatchConversation();
  store.watchConversation("p3");
  store.stop();
  assert.deepEqual(ws.sent, []);
});

test("a conversation that was opened is closed when it is replaced, unwatched and on stop", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  store.watchConversation("p1");
  store.watchConversation("p2");
  assert.equal(ws.commands("conversationClose").length, 1);
  store.unwatchConversation();
  assert.equal(ws.commands("conversationClose").length, 2);
  store.watchConversation("p3");
  store.stop();
  assert.equal(ws.commands("conversationClose").length, 3);
});

test("the desk-only refusal sets graphRefused and stops the retry; another failure sets graphError", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  store.watchGraph("C:/nowhere");
  ws.frame({ type: "teamResult", op: "graphWatch", ok: false, error: "no project is open at C:/nowhere" });
  assert.deepEqual(store.getState().graphError, { op: "graphWatch", error: "no project is open at C:/nowhere" });
  assert.equal(store.getState().graphRefused, false);
  ws.frame({ type: "teamResult", op: "graphWatch", ok: false, error: "The Team panel is used on the " + DESK_ONLY_PHRASE + ", not from a window reached through the relay" });
  assert.equal(store.getState().graphRefused, true);
  assert.equal(store.getState().graph, null);
  // A result for an unrelated command changes nothing.
  const before = store.getState();
  ws.frame({ type: "teamResult", op: "teamTaskAdd", ok: false, error: "no" });
  assert.equal(store.getState(), before);
});

test("scrubbed shapes pass through as they are", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  store.watchGraph("C:/repo");
  const g = graph("C:/repo", {
    teams: [{ id: "p1", needs: [{ id: "permission:p2", kind: "permission", pane: "p2", text: "Run a command" }] }],
    nodes: [{ pane: "p2", kind: "structured", detailsOnDesktop: true }],
  });
  ws.frame(g);
  assert.deepEqual(store.getState().graph, g);
  ws.frame({ type: "state", panes: { p2: { status: "waiting", permission: { tool: "Bash", summary: "Run a command", redacted: true, options: [] } } } });
  assert.equal(store.getState().state.panes.p2.permission.redacted, true);
});

test("prefs and keyTable updates amend the hello; notices count up", async () => {
  const { store, ws } = await up();
  ws.frame({ type: "prefs", prefs: { theme: "x" } }); // before a hello: nothing to amend
  assert.equal(store.getState().hello, null);
  ws.frame(hello({ prefs: { theme: "dark" } }));
  ws.frame({ type: "prefs", prefs: { theme: "light" } });
  ws.frame({ type: "keyTable", keys: [{ id: "k" }] });
  assert.equal(store.getState().hello.prefs.theme, "light");
  assert.deepEqual(store.getState().hello.keys, [{ id: "k" }]);
  ws.frame({ type: "notice", text: "same", error: true });
  const first = store.getState().notice.seq;
  ws.frame({ type: "notice", text: "same", error: true });
  assert.equal(store.getState().notice.seq, first + 1);
  assert.equal(store.getState().notice.text, "same");
});

test("a malformed frame is dropped without closing the socket or touching the store", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  ws.frame({ type: "state", n: 1 });
  const before = store.getState();
  ws.raw("{broken");
  ws.raw("[]");
  assert.equal(ws.closed, false);
  assert.equal(store.getState().state, before.state);
  assert.equal(store.getState().dropped, 2);
  assert.equal(store.getState().connection, "open");
});

test("subscribers are told of each change, a failing one does not stop the others, and unsubscribe works", async () => {
  const { store, ws } = await up();
  const seen = [];
  const quiet = console.error;
  console.error = () => {};
  try {
    store.subscribe(() => { throw new Error("bad listener"); });
    const off = store.subscribe((s) => seen.push(s.state && s.state.n));
    ws.frame({ type: "state", n: 1 });
    ws.frame({ type: "state", n: 2 });
    off();
    ws.frame({ type: "state", n: 3 });
  } finally {
    console.error = quiet;
  }
  assert.deepEqual(seen, [1, 2]);
});

test("every change is a new snapshot and the old one is untouched", async () => {
  const { store, ws } = await up();
  const a = store.getState();
  ws.frame({ type: "state", n: 1 });
  const b = store.getState();
  assert.notEqual(a, b);
  assert.equal(a.state, null);
});

test("stop sends graphUnwatch for a watch, closes the socket and empties the store", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  store.watchGraph("C:/repo");
  ws.frame(graph());
  store.stop();
  assert.equal(ws.commands("graphUnwatch").length, 1);
  assert.equal(ws.closed, true);
  const s = store.getState();
  assert.equal(s.connection, "idle");
  assert.equal(s.graph, null);
  assert.equal(s.hello, null);
});

test("useStore draws the current snapshot", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  ws.frame({ type: "notice", text: "<b>hi</b>" });
  function Line() {
    const s = useStore(store);
    return html`<p>${s.connection}: ${s.notice.text}</p>`;
  }
  const out = renderToString(html`<${Line} />`);
  assert.equal(out, "<p>open: &lt;b>hi&lt;/b></p>");
});

test("the refusal's code is enough, and a refusal gives up the watch for good", async () => {
  const { store, clock, ws } = await up();
  ws.frame(hello());
  store.watchGraph("C:/repo");
  ws.frame({ type: "teamResult", op: "graphWatch", ok: false, error: "worded differently", code: "deskOnly" });
  assert.equal(store.getState().graphRefused, true);
  ws.drop();
  clock.advance(1000);
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  assert.equal(FakeWebSocket.last.commands("graphWatch").length, 0, "asked again after a refusal");
});

test("a teamGone with no graph yet is taken only while a watch is open", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  ws.frame({ type: "teamGone", root: "C:/x", error: "gone" });
  assert.equal(store.getState().graphGone, null, "a teamGone nobody watched for was taken");
  store.watchGraph("C:/x");
  ws.frame({ type: "teamGone", root: "C:/x", error: "gone" });
  assert.deepEqual(store.getState().graphGone, { root: "C:/x", error: "gone" });
});

test("a push of the project that was watched before is kept as it came, for a view to compare", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  store.watchGraph("C:/a");
  ws.frame(graph("C:/a"));
  store.watchGraph("C:/b");
  ws.frame(graph("C:/a")); // still on its way when the watch changed
  assert.equal(store.getState().graph.root, "C:/a");
  ws.frame(graph("C:/b"));
  assert.equal(store.getState().graph.root, "C:/b");
});

test("an accepted paneSend is kept as sent, with a sequence number that grows", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  assert.equal(store.getState().sent, null);
  ws.frame({ type: "paneSend", id: "p1", queued: true, shell: false });
  const a = store.getState().sent;
  assert.deepEqual({ id: a.id, queued: a.queued, shell: a.shell }, { id: "p1", queued: true, shell: false });
  ws.frame({ type: "paneSend", id: "p1", queued: false, shell: false, waiting: true });
  const b = store.getState().sent;
  assert.ok(b.seq > a.seq);
  assert.equal(b.waiting, true);
});

test("a structuredCancel refusal is kept for the drawer, the next one that works clears it, and other ops leave it alone", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  assert.equal(store.getState().cancelError, null);
  assert.equal(store.send({ cmd: "structuredCancel", id: "p3", req: "c1" }), true);
  assert.deepEqual(ws.sent.filter((c) => c.cmd === "structuredCancel"), [{ cmd: "structuredCancel", id: "p3", req: "c1" }]);
  ws.frame({ type: "teamResult", req: "c1", op: "structuredCancel", ok: false, error: "That pane has nothing running to stop" });
  assert.deepEqual(store.getState().cancelError, { id: "p3", error: "That pane has nothing running to stop" });
  ws.frame({ type: "teamResult", op: "graphWatch", ok: true });
  assert.notEqual(store.getState().cancelError, null, "another op cleared the cancel refusal");
  ws.frame({ type: "teamResult", req: "c2", op: "structuredCancel", ok: true });
  assert.equal(store.getState().cancelError, null);
  assert.equal(store.getState().graphError, null, "a cancel refusal was taken for a graph error");
});

test("a remote window cannot send structuredCancel", async () => {
  const { store, ws } = await up();
  ws.frame(hello({ remote: true }));
  assert.equal(store.send({ cmd: "structuredCancel", id: "p3", req: "c1" }), false);
  assert.equal(ws.sent.filter((c) => c.cmd === "structuredCancel").length, 0);
});

test("a cancel refusal is kept for the pane the cancel was for, by its req", async () => {
  const { store, ws } = await up();
  ws.frame(hello());
  store.send({ cmd: "structuredCancel", id: "p3", req: "c1" });
  store.send({ cmd: "structuredCancel", id: "p4", req: "c2" });
  ws.frame({ type: "teamResult", req: "c2", op: "structuredCancel", ok: false, error: "No" });
  assert.deepEqual(store.getState().cancelError, { id: "p4", error: "No" });
  ws.frame({ type: "teamResult", req: "c1", op: "structuredCancel", ok: false, error: "Nope" });
  assert.deepEqual(store.getState().cancelError, { id: "p3", error: "Nope" });
});

test("paneSend is desk only in the store, and goes in a window on the desk", async () => {
  const local = await up();
  local.ws.frame(hello());
  assert.equal(local.store.send({ cmd: "paneSend", id: "p1", text: "hi" }), true);
  assert.equal(local.ws.sent.filter((c) => c.cmd === "paneSend").length, 1);
  const remote = await up();
  remote.ws.frame(hello({ remote: true }));
  assert.equal(remote.store.send({ cmd: "paneSend", id: "p1", text: "hi" }), false);
  assert.equal(remote.ws.sent.filter((c) => c.cmd === "paneSend").length, 0);
});
