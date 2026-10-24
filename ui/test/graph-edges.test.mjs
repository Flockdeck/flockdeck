// The edges of the graph: waiting-on, message by delivery state, and sent; the
// cap notice; no edge to a pane that is not a node; and edges that follow the
// boxes when the layout changes.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { buildGraphModel } from "../src/graph/model.js";
import { layoutGraph } from "../src/graph/layout.js";
import { Graph } from "../src/graph/Graph.js";
import { edgesOf, deliveryWord, GRAPH_EDGE_CAP, MAX_DRAWN } from "../src/graph/Edges.js";

const here = dirname(fileURLToPath(import.meta.url));
const fixtures = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures");
const fixture = (name) => JSON.parse(readFileSync(join(fixtures, name + ".json"), "utf8"));

const node = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, name: pane, status: "idle", activity: "idle", waitingOn: { kind: "none" }, ...over });
const graph = (nodes, edges = []) => ({ type: "graph", root: "/r", project: "p", teams: [], nodes, edges, solo: nodes.map((n) => n.pane), soloNeeds: [] });
const msg = (from, to, delivery, id = 1) => ({ kind: "message", from, to, id, time: "2026-10-21T08:00:00Z", text: "hello", delivery });
const sent = (from, to) => ({ kind: "sent", from, to, time: "2026-10-21T08:00:00Z" });
const edges = (m, opts) => {
  const model = buildGraphModel(m);
  return edgesOf(model, layoutGraph(model), opts);
};
const draw = (m) => renderToString(html`<${Graph} model=${buildGraphModel(m)} />`);

test("fixture 5: the undelivered message is an amber edge that says it has not been told yet", () => {
  const m = fixture("05-six-helpers-undelivered-message");
  const e = edges(m);
  assert.equal(e.message.length, 1);
  assert.equal(e.message[0].word, "not told yet");
  const svg = draw(m);
  assert.match(svg, /class="gedge gedge--message gedge--undelivered"/);
  assert.match(svg, /gedge-label--amber[^>]*>.*?not told yet/);
  assert.match(svg, /marker-end="url\(#gedge-arrow-amber\)"/);
});

test("each delivery state is drawn in words, and only a queued message is amber", () => {
  const nodes = [node("a"), node("b")];
  const states = { queued: "not told yet", notified: "told", woken: "woken", read: "read" };
  for (const [state, word] of Object.entries(states)) {
    const m = graph(nodes, [msg("a", "b", state)]);
    assert.equal(edges(m).message[0].word, word, state);
    const svg = draw(m);
    assert.equal(svg.includes("gedge--undelivered"), state === "queued", state);
    assert.ok(svg.includes(`>${word}<`), `${state} has its word on the edge`);
  }
});

test("a delivery state or a missing one this file does not know is told as unknown, not as queued", () => {
  for (const delivery of ["", "teleported", undefined]) {
    const m = graph([node("a"), node("b")], [{ ...msg("a", "b", delivery) }]);
    const svg = draw(m);
    assert.equal(edges(m).message[0].word, "delivery unknown");
    assert.ok(!svg.includes("gedge--undelivered"));
    assert.ok(!svg.includes(">read<"));
  }
  assert.equal(deliveryWord("read"), "read");
});

test("a sent edge is dotted, says sent, and is never drawn as read", () => {
  const m = graph([node("a"), node("b")], [sent("a", "b"), { ...sent("b", "a"), delivery: "read" }]);
  const e = edges(m);
  assert.equal(e.sent.length, 2);
  assert.deepEqual(e.sent.map((x) => x.word), ["sent", "sent"]);
  const svg = draw(m);
  assert.equal(svg.split("gedge--sent").length - 1, 2);
  assert.ok(!svg.includes(">read<"), "a sent edge was drawn as read");
  assert.ok(!svg.includes("gedge--undelivered"));
});

test("waiting-on: a line to a helper that is a pane, none for a person, a reviewer, a task, none or down", () => {
  const m = graph([
    node("m", { waitingOn: { kind: "helpers", text: "1 helper is busy", ref: "h" } }),
    node("h"),
    node("u", { waitingOn: { kind: "user", text: "npm test" } }),
    node("r", { waitingOn: { kind: "reviewer", text: "T1 is in review", ref: "T1" } }),
    node("t", { waitingOn: { kind: "task", text: "the vendor's key", ref: "T2" } }),
    node("d", { waitingOn: { kind: "down", text: "exited", ref: "d" } }),
    node("x", { waitingOn: { kind: "somethingnew", text: "?", ref: "z" } }),
    node("lost", { waitingOn: { kind: "helpers", text: "gone", ref: "not-a-pane" } }),
  ]);
  const e = edges(m);
  assert.deepEqual(e.waiting.map((x) => [x.from, x.to]), [["m", "h"]]);
  assert.equal(e.waiting.length, 1, "only a holder that is a pane of the graph has a line; the box says the rest");
  assert.equal(Object.prototype.hasOwnProperty.call(e, "tags"), false);
});

test("no edge is drawn to or from a pane that is not a node, and none from a pane to itself", () => {
  const m = graph(
    [node("a"), node("b")],
    [msg("a", "ghost", "queued", 1), msg("ghost", "b", "read", 2), sent("a", "ghost"), sent("ghost", "a"), msg("a", "a", "read", 3), msg("a", "b", "read", 4)],
  );
  const e = edges(m);
  assert.equal(e.message.length, 1);
  assert.deepEqual([e.message[0].from, e.message[0].to], ["a", "b"]);
  assert.equal(e.sent.length, 0);
  const svg = draw(m);
  assert.ok(!svg.includes("ghost"));
  for (const x of [...e.message, ...e.sent, ...e.waiting]) assert.ok(!/NaN|undefined/.test(x.d), x.d);
});

test("edges follow the boxes when the layout changes", () => {
  const m = graph([node("a"), node("b")], [msg("a", "b", "read")]);
  const model = buildGraphModel(m);
  const wide = edgesOf(model, layoutGraph(model, { nodeW: 300 }));
  const narrow = edgesOf(model, layoutGraph(model, { nodeW: 150 }));
  assert.notEqual(wide.message[0].d, narrow.message[0].d);
  // The line starts on the edge of the first box and ends on the edge of the second.
  for (const [e, w] of [[wide, 300], [narrow, 150]]) {
    const lay = layoutGraph(model, { nodeW: w });
    const a = lay.byPane.get("a");
    const b = lay.byPane.get("b");
    const nums = e.message[0].d.match(/-?\d+(\.\d+)?/g).map(Number);
    const [sx, sy, , , ex, ey] = nums;
    const onEdge = (box, x, y) => {
      const inX = x >= box.x - 0.2 && x <= box.x + box.w + 0.2;
      const inY = y >= box.y - 0.2 && y <= box.y + box.h + 0.2;
      const touches = Math.abs(x - box.x) < 0.2 || Math.abs(x - box.x - box.w) < 0.2 || Math.abs(y - box.y) < 0.2 || Math.abs(y - box.y - box.h) < 0.2;
      return inX && inY && touches;
    };
    assert.ok(onEdge(a, sx, sy), `starts on a's box (${sx},${sy})`);
    assert.ok(onEdge(b, ex, ey), `ends on b's box (${ex},${ey})`);
  }
});

test("several edges between one pair are spread, not drawn over each other", () => {
  const m = graph([node("a"), node("b")], [msg("a", "b", "read", 1), msg("b", "a", "queued", 2), sent("a", "b")]);
  const e = edges(m);
  const ds = [...e.message, ...e.sent].map((x) => x.d);
  assert.equal(new Set(ds).size, 3);
});

test("when a kind reaches the graph's cap the corner says so", () => {
  const many = (n, make) => Array.from({ length: n }, (_, i) => make(i));
  const nodes = [node("a"), node("b")];
  const full = graph(nodes, many(GRAPH_EDGE_CAP, (i) => msg("a", "b", "read", i + 1)));
  const e = edges(full);
  assert.equal(e.message.length, MAX_DRAWN);
  assert.equal(e.notices.length, 1);
  assert.match(e.notices[0], /messages/);
  assert.match(e.notices[0], new RegExp(`newest ${GRAPH_EDGE_CAP}`));
  assert.match(e.notices[0], new RegExp(`${MAX_DRAWN} of ${GRAPH_EDGE_CAP} drawn`));
  assert.match(draw(full), /class="gedge-notice"[^>]*><text[^>]*>Edges: messages/);

  const sentFull = graph(nodes, many(GRAPH_EDGE_CAP, () => sent("a", "b")));
  const s = edges(sentFull);
  assert.equal(s.notices.length, 1);
  assert.match(s.notices[0], /sent notes/);
});

test("thinning keeps the newest, which the graph sends first", () => {
  const nodes = [node("a"), node("b")];
  const list = Array.from({ length: 60 }, (_, i) => ({ ...msg("a", "b", "read", 60 - i), text: "m" + (60 - i) }));
  const e = edges(graph(nodes, list), { maxDrawn: 5 });
  assert.equal(e.message.length, 5);
  assert.match(e.notices[0], /5 of 60 drawn/);
  assert.equal(e.message.map((x) => x.text).join(","), "m60,m59,m58,m57,m56");
});

test("below the cap there is no notice", () => {
  const e = edges(graph([node("a"), node("b")], [msg("a", "b", "read"), sent("a", "b")]));
  assert.deepEqual(e.notices, []);
  assert.ok(!draw(graph([node("a"), node("b")], [msg("a", "b", "read")])).includes("gedge-notice"));
});

test("edges do not hide a box: every pane is still drawn once, after the edges", () => {
  const m = fixture("04-eight-helpers-three-waiting");
  const svg = draw(m);
  for (const n of m.nodes) assert.equal(svg.split(`data-pane="${n.pane}"`).length - 1 >= 1, true);
  const firstBox = svg.indexOf('class="gnode ');
  const lastEdge = svg.lastIndexOf('class="gedge ');
  assert.ok(lastEdge < firstBox || firstBox < 0, "an edge is drawn over a box");
});

test("text from an agent is drawn as text on an edge", () => {
  const evil = '"><script>alert(1)</script><g a="';
  const m = graph(
    [node("a", { waitingOn: { kind: "user", text: evil } }), node("b")],
    [{ ...msg("a", "b", "queued"), text: evil }],
  );
  const svg = draw(m);
  assert.ok(!svg.includes("<script"));
  assert.ok(svg.includes("&lt;script>"));
});

test("an empty or unreadable model has no edges", () => {
  assert.deepEqual(edgesOf(null, null), { waiting: [], message: [], sent: [], notices: [] });
  const model = buildGraphModel({});
  assert.equal(edgesOf(model, layoutGraph(model)).message.length, 0);
});

test("every fixture draws edges without a bad number", () => {
  for (const name of ["01-six-helpers-one-permission", "02-eight-helpers-permission-and-question", "03-nested-helper-question-and-returned-task", "04-eight-helpers-three-waiting", "05-six-helpers-undelivered-message"]) {
    const model = buildGraphModel(fixture(name));
    const e = edgesOf(model, layoutGraph(model));
    for (const x of [...e.waiting, ...e.message, ...e.sent]) assert.ok(!/NaN|undefined|Infinity/.test(x.d + JSON.stringify(x.mid)), name);
  }
});

// ------------------------------------------------------------ pins

const css = readFileSync(join(here, "..", "src", "graph", "graph.css"), "utf8");
// The declarations of the rule whose selector list is exactly sel.
const rule = (sel) => {
  const at = css.replaceAll("\r\n", "\n").split("\n" + sel + " {");
  assert.ok(at.length === 2, `no single rule for ${sel}`);
  return at[1].slice(0, at[1].indexOf("}"));
};

test("the graph's cap is the server's, and the default draws 40", () => {
  assert.equal(GRAPH_EDGE_CAP, 100);
  assert.equal(MAX_DRAWN, 40);
  const list = Array.from({ length: 45 }, (_, i) => msg("a", "b", "read", 45 - i));
  const e = edges(graph([node("a"), node("b")], list));
  assert.equal(e.message.length, 40);
  assert.match(e.notices[0], /40 of 45 drawn/);
  assert.ok(!/newest 100/.test(e.notices[0]), "45 edges are not the graph's cap");
});

test("the line styles: sent dotted, waiting-on dashed, a queued message amber", () => {
  assert.match(rule(".gedge--sent path"), /stroke-dasharray:\s*1 5/);
  assert.match(rule(".gedge--sent path"), /stroke-linecap:\s*round/);
  assert.match(rule(".gedge--waiting path"), /stroke-dasharray:\s*6 3/);
  assert.match(rule(".gedge--undelivered path"), /stroke:\s*var\(--waiting\)/);
  assert.match(rule(".gedge-head--amber"), /fill:\s*var\(--waiting\)/);
  // A message edge by itself is solid.
  assert.ok(!/dasharray/.test(rule(".gedge path")));
  // Told but not read is the same line, dimmer.
  assert.match(rule(".gedge--told path"), /stroke-opacity/);
  const svg = draw(graph([node("a"), node("b")], [msg("a", "b", "notified"), msg("b", "a", "queued", 2)]));
  assert.ok(svg.includes("gedge--told"));
});

test("edges to panes that are not nodes are counted in the corner, not lost unsaid", () => {
  const m = graph([node("a"), node("b")], [msg("a", "b", "read", 1), msg("a", "hidden", "queued", 2), msg("hidden", "b", "read", 3), sent("a", "hidden")]);
  const e = edges(m);
  assert.equal(e.message.length, 1);
  assert.equal(e.notices.length, 2);
  assert.match(e.notices[0], /messages.*2 not drawn, a pane is not in the graph/);
  assert.match(e.notices[0], /1 of 3 drawn/);
  assert.match(e.notices[1], /sent notes.*1 not drawn/);
  // A self edge is not a dropped edge.
  assert.deepEqual(edges(graph([node("a")], [msg("a", "a", "read")])).notices, []);
});

test("the waiting-on edge's title carries the graph's own text", () => {
  const m = graph([node("m", { waitingOn: { kind: "helpers", text: "3 helpers are busy", ref: "h" } }), node("h")]);
  const svg = draw(m);
  assert.match(svg, /<title>waiting on a helper: 3 helpers are busy<\/title>/);
});

// ------------------------------------------------------------ the stylesheets are well formed

// A rule that is never closed nests every rule after it in a browser, which
// drops them. The checks above read each rule's text and cannot see that.
// Reports the problems of a stylesheet's braces, outside comments and strings:
// a close with nothing open, a rule inside a rule (these files use no
// @media nesting in graph.css, so depth above 1 is a missing brace), a comment
// that starts inside a rule, and braces left open at the end.
export function braceProblems(text, { maxDepth = 1, topLevelComments = true } = {}) {
  const problems = [];
  let depth = 0;
  let line = 1;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === "\n") line++;
    else if (c === "/" && text[i + 1] === "*") {
      if (topLevelComments && depth !== 0) problems.push(`line ${line}: a comment starts inside a rule`);
      const end = text.indexOf("*/", i + 2);
      if (end < 0) { problems.push(`line ${line}: a comment is never closed`); break; }
      for (const ch of text.slice(i, end)) if (ch === "\n") line++;
      i = end + 1;
    } else if (c === '"' || c === "'") {
      const end = text.indexOf(c, i + 1);
      if (end < 0) break;
      i = end;
    } else if (c === "{") {
      depth++;
      if (depth > maxDepth) problems.push(`line ${line}: a rule inside a rule`);
    } else if (c === "}") {
      depth--;
      if (depth < 0) { problems.push(`line ${line}: a closing brace with nothing open`); depth = 0; }
    }
  }
  if (depth !== 0) problems.push(`${depth} brace(s) left open at the end`);
  return problems;
}

test("the graph's stylesheet and the shell's are well formed: braces balance and no rule sits inside another", () => {
  for (const file of ["graph/graph.css", "graph/tree.css", "graph/deps.css", "app.css"]) {
    const text = readFileSync(join(here, "..", "src", file), "utf8");
    // app.css has @media blocks and graph.css an @container one, which hold rules one level down.
    // graph.css keeps its comments between rules; app.css also has some inside.
    const problems = braceProblems(text, { maxDepth: 2, topLevelComments: file !== "app.css" });
    assert.deepEqual(problems, [], file);
  }
});

test("the brace check sees the defect it is for", () => {
  const broken = ".a {\n  stroke: x;\n\n/* Edges. */\n.b {\n  fill: y;\n}\n";
  const problems = braceProblems(broken);
  assert.ok(problems.some((p) => /comment starts inside a rule/.test(p)), problems.join("; "));
  assert.ok(problems.some((p) => /rule inside a rule/.test(p)), problems.join("; "));
  assert.ok(problems.some((p) => /left open/.test(p)), problems.join("; "));
  assert.deepEqual(braceProblems(".a { x: y; }\n/* c */\n.b { z: w; }\n"), []);
  assert.ok(braceProblems("}\n").some((p) => /nothing open/.test(p)));
});

test("only a told message is dimmed: not a read one, a queued one or an unknown one", () => {
  const dimmed = (delivery) => draw(graph([node("a"), node("b")], [msg("a", "b", delivery)])).includes("gedge--told");
  assert.equal(dimmed("notified"), true);
  assert.equal(dimmed("woken"), true);
  for (const d of ["read", "queued", "", "teleported"]) assert.equal(dimmed(d), false, d || "(none)");
});
