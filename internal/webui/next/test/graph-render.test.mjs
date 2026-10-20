// The graph drawn to a string: a snapshot of each fixture, the status words and
// shapes, and the escaping of text from agents. Snapshots are in
// test/snapshots; run with UPDATE_SNAPSHOTS=1 to write them again after a change
// that is meant.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, writeFileSync, mkdirSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { Graph } from "../assets/graph/Graph.js";
import { clip, stateOf, spendOf } from "../assets/graph/Node.js";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures");
const snapDir = join(here, "snapshots");
const names = readdirSync(dir)
  .filter((f) => f.endsWith(".json"))
  .map((f) => f.slice(0, -5))
  .sort();
const load = (name) => JSON.parse(readFileSync(join(dir, name + ".json"), "utf8"));
const draw = (msg) => renderToString(html`<${Graph} model=${buildGraphModel(msg)} />`);

// One element per line, so a snapshot's diff is readable.
const pretty = (svg) => svg.replace(/></g, ">\n<") + "\n";

for (const name of names) {
  test(`fixture ${name}: the SVG is the snapshot`, () => {
    const out = pretty(draw(load(name)));
    const file = join(snapDir, name + ".svg");
    if (process.env.UPDATE_SNAPSHOTS === "1") {
      mkdirSync(snapDir, { recursive: true });
      writeFileSync(file, out);
    }
    assert.ok(existsSync(file), `no snapshot ${file}; run with UPDATE_SNAPSHOTS=1`);
    assert.equal(out, readFileSync(file, "utf8").replace(/\r\n/g, "\n"));
  });

  test(`fixture ${name}: the same input draws the same SVG`, () => {
    assert.equal(draw(load(name)), draw(load(name)));
  });

  test(`fixture ${name}: every pane is drawn once with its status as a word`, () => {
    const msg = load(name);
    const out = draw(msg);
    for (const n of msg.nodes) {
      assert.equal(out.split(`data-pane="${n.pane}"`).length - 1, 1, `${n.pane} is not drawn once`);
    }
    const words = { waiting: "waiting", blocked: "blocked", exited: "down", failed: "down" };
    for (const n of msg.nodes) {
      const w = words[n.activity];
      if (!w) continue;
      const g = out.slice(out.indexOf(`data-pane="${n.pane}"`));
      const end = g.indexOf("</g></g>") >= 0 ? g.indexOf("</g></g>") : g.length;
      assert.match(g.slice(0, end), new RegExp(`class="gnode-state"[^>]*>${w}<`), `${n.pane} (${n.activity}) has no "${w}" word`);
    }
  });
}

test("each state has its own shape and word", () => {
  const seen = new Map();
  for (const s of ["working", "idle", "waiting", "blocked", "starting", "exited"]) {
    const st = stateOf({ activity: s, status: s });
    seen.set(st.shape, s);
    assert.ok(st.word, s);
  }
  assert.equal(seen.size, 6, "two states share a shape");
  assert.equal(stateOf({ activity: "exited", status: "exited" }).word, "down");
  assert.equal(stateOf({ activity: "failed", status: "exited" }).word, "down");
  // An activity this file does not know falls back to the status.
  assert.equal(stateOf({ activity: "", status: "waiting" }).word, "waiting");
  assert.equal(stateOf({ activity: "dreaming", status: "dreaming" }).shape, "unknown");
});

test("a node shows the live line, branch, changed files, task and spend", () => {
  const msg = {
    type: "graph",
    teams: [{ id: "m", manager: "m", members: ["m"], spendUSD: 3.5, spendPartial: true, goal: "Ship it" }],
    nodes: [
      {
        pane: "m",
        depth: 0,
        team: "m",
        name: "boss",
        role: "manager",
        agent: "claude",
        status: "working",
        activity: "working",
        live: "Reading basket.go",
        branch: "main",
        dirty: 3,
        untracked: 1,
        task: { id: "T4", title: "Basket", state: "in-progress" },
      },
    ],
    edges: [],
    solo: [],
  };
  const out = draw(msg);
  for (const want of ["boss", "manager · claude", "Reading basket.go", "main  3 changed, 1 new", "T4 in-progress", "$3.50 partial", "Ship it"]) {
    assert.ok(out.includes(want), `missing ${want}\n${out}`);
  }
});

test("text from an agent is drawn as text, in every place it can appear", () => {
  const evil = '<script>alert(1)</script>"\'&';
  const msg = {
    type: "graph",
    project: evil,
    teams: [{ id: "m", manager: "m", members: ["m", "w"], goal: evil }],
    nodes: [
      { pane: "m", depth: 0, team: "m", children: ["w"], name: evil, role: evil, agent: evil, status: "working", activity: "working", live: evil, branch: evil },
      { pane: "w", parent: "m", depth: 1, team: "m", name: "w", status: evil, activity: evil, task: { id: evil, title: evil, state: evil } },
      { pane: '"><script>x</script>', depth: 0, name: "solo", status: "idle", activity: "idle" },
    ],
    edges: [],
    solo: ['"><script>x</script>'],
  };
  const out = draw(msg);
  assert.ok(!out.includes("<script"), out);
  assert.ok(!/ on[a-z]+=/.test(out), out);
  // The renderer escapes the opening angle bracket, which is all that ends text.
  assert.ok(out.includes("&lt;script>"), out);
  assert.ok(out.includes("&quot;"), out);
  assert.ok(!/data-pane="[^"]*"[^>]*><script/.test(out), out);
});

test("a pane name with markup is a child of a text element", () => {
  const out = draw({
    type: "graph",
    teams: [],
    nodes: [{ pane: "p", depth: 0, name: "<b>x</b>", status: "idle", activity: "idle" }],
    edges: [],
    solo: ["p"],
  });
  assert.match(out, /<text class="gnode-name"[^>]*>&lt;b>x&lt;\/b><\/text>/);
});

test("clip counts code points and ends in an ellipsis", () => {
  assert.equal(clip("abc", 5), "abc");
  assert.equal(clip("abcdef", 4), "abc…");
  assert.equal(clip("\u{1F600}\u{1F600}\u{1F600}", 2), "\u{1F600}…");
  assert.equal(clip(null, 5), "");
  assert.equal(clip(12345, 3), "12…");
});

test("spend is shown with a partial mark when the graph says it is incomplete", () => {
  assert.equal(spendOf({ spendUSD: 1.5 }), "$1.50");
  assert.equal(spendOf({ spendUSD: 1.5, spendPartial: true }), "$1.50 partial");
  assert.equal(spendOf({ spendUSD: 0, spendIncludesPartial: true }), "$0.00 partial");
  assert.equal(spendOf(null), "");
});

test("a model with nothing to draw says so", () => {
  const out = renderToString(html`<${Graph} model=${buildGraphModel({ type: "graph", teams: [], nodes: [], solo: [] })} />`);
  assert.match(out, /No agents in this project yet\./);
});

// The marks are drawn in the node's own coordinates; a number made into a string
// would put a shape far from its box.
test("every mark stays inside the corner of its box", () => {
  for (const name of names) {
    const out = draw(load(name));
    for (const m of out.matchAll(/<(?:polygon|path)[^>]*class="gmark[^"]*"[^>]*(?:points|d)="([^"]*)"/g)) {
      for (const n of m[1].match(/-?\d+(?:\.\d+)?/g)) assert.ok(Number(n) >= 0 && Number(n) <= 40, `${name}: ${m[1]}`);
    }
  }
});
