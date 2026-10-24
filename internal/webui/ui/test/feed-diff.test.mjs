// The activity feed's entries, made by comparing one graph push with the next.
import { test } from "node:test";
import assert from "node:assert/strict";
import { buildGraphModel } from "../assets/graph/model.js";
import { FEED_CAP, agoOf, diffGraphs, feedStep, pushEntries } from "../assets/graph/FeedDiff.js";

const NOW = 1_000_000;
const node = (pane, extra = {}) => ({ pane, name: pane, team: "m", depth: pane === "m" ? 0 : 1, status: "working", role: "worker", ...(pane === "m" ? {} : { parent: "m" }), ...extra });
const graph = ({ nodes = [node("m"), node("a"), node("b")], needs = [], edges = [], root = "/w" } = {}) =>
  buildGraphModel({
    type: "graph", root, project: "p", solo: [], soloNeeds: [], edges,
    teams: [{ id: "m", manager: "m", members: nodes.map((n) => n.pane), needs, caps: [] }],
    nodes,
  });
const need = (kind, pane, extra = {}) => ({ id: kind + ":" + pane, kind, pane, time: "2026-10-21T10:00:00Z", text: "", ...extra });
const diff = (a, b) => diffGraphs(a, b, NOW);
const texts = (es) => es.map((e) => e.text.replace(/[.]$/, ""));

test("the first push, and a push for another project, are a starting point and give nothing", () => {
  assert.deepEqual(diff(buildGraphModel(null), graph()), []);
  assert.deepEqual(diff(graph({ root: "/x" }), graph()), []);
  assert.deepEqual(diff(graph(), graph()), []);
});

test("a helper that appears was started by its parent, and one that goes was closed", () => {
  const es = diff(graph(), graph({ nodes: [node("m"), node("a"), node("b"), node("c", { name: "cfg" })] }));
  assert.deepEqual(texts(es), ["m started cfg"]);
  assert.equal(es[0].pane, "c");
  assert.equal(es[0].kind, "info");
  assert.deepEqual(texts(diff(graph(), graph({ nodes: [node("m"), node("a")] }))), ["b was closed"]);
});

test("a pane that exits is one down entry, not two", () => {
  const was = graph();
  const now = graph({ nodes: [node("m"), node("a", { status: "exited" }), node("b")], needs: [need("down", "a")] });
  const es = diff(was, now);
  assert.deepEqual(texts(es), ["a exited"]);
  assert.equal(es[0].kind, "down");
});

test("status flips between working, idle and waiting are not entries", () => {
  assert.deepEqual(diff(graph(), graph({ nodes: [node("m"), node("a", { status: "idle" }), node("b", { status: "waiting" })] })), []);
});

test("a new need is an entry once, by kind, with the prompt when it was sent", () => {
  const was = graph();
  const now = graph({ needs: [need("permission", "a", { text: "go test ./pkg" }), need("ask", "b", { text: "which store?" }), need("stuck", "b"), need("conflict", "a", { other: "b", text: "cfg/limits.yaml" })] });
  const es = diff(was, now);
  assert.deepEqual(texts(es), ["a asks to run go test ./pkg", "b asks: which store?", "b is working but has written nothing for a while", "a and b both change cfg/limits.yaml"]);
  assert.deepEqual(es.map((e) => e.kind), ["need", "need", "warn", "need"]);
  assert.equal(es[3].other, "b");
  assert.deepEqual(diff(now, now), [], "a need that stands is not new again");
});

test("an entry ends with a full stop, and a conflict with no file says the same file", () => {
  const es = diff(graph(), graph({ needs: [need("conflict", "a", { other: "b" }), need("ask", "b", { text: "which?" })] }));
  assert.equal(es[0].text, "a and b both change the same file.");
  assert.equal(es[1].text, "b asks: which?", "a text that ends in a mark gets no second one");
});

test("a prompt hidden for the relay is not said", () => {
  const es = diff(graph(), graph({ needs: [need("permission", "a", { text: "details on the desktop" })] }));
  assert.deepEqual(texts(es), ["a asks to run something"]);
});

test("a task that changes state is an entry, and a finished one is a ship", () => {
  const was = graph({ nodes: [node("m"), node("a", { task: { id: "T3", title: "x", state: "in-progress" } })] });
  const review = graph({ nodes: [node("m"), node("a", { task: { id: "T3", title: "x", state: "in-review" } })] });
  const done = graph({ nodes: [node("m"), node("a", { task: { id: "T3", title: "x", state: "done" } })] });
  assert.deepEqual(texts(diff(was, review)), ["a: T3 is in review"]);
  const es = diff(review, done);
  assert.deepEqual(texts(es), ["a: T3 is done"]);
  assert.equal(es[0].kind, "ship");
});

test("a message is an entry when it is new, and again when it gets further", () => {
  const edge = (delivery, id = 7) => ({ kind: "message", from: "a", to: "b", id, time: "2026-10-21T10:00:00Z", text: "T3 is ready", delivery });
  const queued = graph({ edges: [edge("queued")] });
  const es = diff(graph(), queued);
  assert.deepEqual(texts(es), ["a sent b a message: T3 is ready"]);
  assert.equal(es[0].pane, "b");
  assert.deepEqual(texts(diff(queued, graph({ edges: [edge("notified")] }))), ["b was told about a's message"]);
  assert.deepEqual(texts(diff(queued, graph({ edges: [edge("read")] }))), ["b read a's message"]);
  assert.deepEqual(diff(graph({ edges: [edge("read")] }), graph({ edges: [edge("queued")] })), [], "a state that goes back is not news");
  assert.deepEqual(diff(queued, graph({ edges: [edge("queued")] })), []);
});

test("text is one line and bounded", () => {
  const long = "x".repeat(500) + "\nsecond line";
  const es = diff(graph(), graph({ edges: [{ kind: "message", from: "a", to: "b", id: 1, text: long, delivery: "queued" }], needs: [need("ask", "a", { text: long })] }));
  for (const e of es) {
    assert.ok(e.text.length <= 140, e.text.length + "");
    assert.ok(!e.text.includes("\n"));
  }
});

test("the feed keeps the newest first, numbers every entry once and stops at the cap", () => {
  let feed = null;
  feed = pushEntries(feed, [{ text: "one" }, { text: "two" }]);
  assert.deepEqual(feed.entries.map((e) => e.text), ["two", "one"]);
  const same = pushEntries(feed, []);
  assert.equal(same, feed, "nothing added is the same feed");
  feed = pushEntries(feed, [{ text: "three" }]);
  assert.deepEqual(feed.entries.map((e) => e.id), [3, 2, 1]);
  const many = Array.from({ length: FEED_CAP + 50 }, (_, i) => ({ text: "e" + i }));
  const capped = pushEntries(feed, many);
  assert.equal(capped.entries.length, FEED_CAP);
  assert.equal(capped.entries[0].text, "e" + (FEED_CAP + 49), "newest first");
  assert.equal(capped.seq, 3 + many.length);
});

test("feedStep is the diff added to the feed", () => {
  const feed = feedStep(null, graph(), graph({ needs: [need("permission", "a")] }), NOW);
  assert.equal(feed.entries.length, 1);
  assert.equal(feed.entries[0].at, NOW);
});

test("ago is a few characters and never negative", () => {
  assert.equal(agoOf(NOW, NOW), "now");
  assert.equal(agoOf(NOW - 44_000, NOW), "now");
  assert.equal(agoOf(NOW - 60_000, NOW), "1m");
  assert.equal(agoOf(NOW - 42 * 60_000, NOW), "42m");
  assert.equal(agoOf(NOW - 2 * 3600_000, NOW), "2h");
  assert.equal(agoOf(NOW - 3 * 86400_000, NOW), "3d");
  assert.equal(agoOf(NOW + 5000, NOW), "now");
  assert.equal(agoOf(NaN, NOW), "now");
});
