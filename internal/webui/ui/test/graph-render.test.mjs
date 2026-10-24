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
import { DEFAULTS } from "../assets/graph/layout.js";
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
      assert.equal(out.split(`class="gnode gnode--`).filter((g) => g.includes(`data-pane="${n.pane}"`) && g.indexOf(`data-pane="${n.pane}"`) < g.indexOf(">")).length, 1, `${n.pane} is not drawn once`);
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

test("a node shows the live line, task and spend, and its title has the branch and the changed files", () => {
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
  for (const want of ["boss", "manager · claude", "Reading basket.go", "T4", "Basket", "$3.50 partial", "Ship it", "branch main, 3 changed, 1 new", "task T4 Basket in-progress"]) {
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

test("an empty project offers one action, Start a team, when the window can start one", () => {
  const empty = buildGraphModel({ type: "graph", teams: [], nodes: [], solo: [] });
  assert.ok(!renderToString(html`<${Graph} model=${empty} />`).includes("Start a team"), "a window that cannot start one offered it");
  const out = renderToString(html`<${Graph} model=${empty} onStartTeam=${() => {}} />`);
  assert.match(out, /<button[^>]*graph-empty-start[^>]*>Start a team<\/button>/);
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

// The words each state is drawn with, pinned one by one, so that a change of a
// word fails here and not only in a snapshot nobody reads.
test("each state is drawn with its own word", () => {
  const want = { working: "working", idle: "idle", waiting: "waiting", blocked: "blocked", starting: "starting", exited: "down", failed: "down" };
  for (const [activity, word] of Object.entries(want)) {
    const out = draw({
      type: "graph",
      teams: [],
      nodes: [{ pane: "p", depth: 0, name: "p", status: activity === "failed" ? "exited" : activity, activity }],
      edges: [],
      solo: ["p"],
    });
    assert.match(out, new RegExp(`<text class="gnode-state"[^>]*>${word}</text>`), activity);
    assert.equal(stateOf({ activity, status: activity }).word, word, activity);
  }
});

// A width estimate for every text run in a box, from the typefaces' average
// character widths (the mono face is 0.6 em wide; the sans faces are a little
// narrower than 0.6 em on average, bold more so). Each run must fit inside its
// box with a margin, and runs on one line must not meet.
const EM = { mono: 0.6, sans: 0.56, bold: 0.58 };
const RUNS = {
  "gnode-name": { size: 12.5, face: "mono" },
  "gnode-state": { size: 11, face: "bold" },
  "gnode-role": { size: 11, face: "sans" },
  "gnode-live": { size: 11, face: "mono" },
  "gnode-wait": { size: 11, face: "sans" },
  "gnode-spend": { size: 11, face: "mono" },
  "gnode-cap": { size: 12, face: "mono" },
  "gnode-msgs": { size: 11, face: "sans" },
};
const BOX_W = 216;
const BOX_H = 152;
const unescape = (s) => s.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&amp;/g, "&");

// The runs of every node drawn: { pane, kind, x0, x1, y }, and each chip.
function runsOf(out) {
  const found = [];
  for (const g of out.split('<g class="gnode ').slice(1)) {
    const pane = /data-pane="([^"]*)"/.exec(g)[1];
    const box = g;
    for (const m of box.matchAll(/<text class="(gnode-[a-z]+)" x="(-?[\d.]+)" y="([\d.]+)"( text-anchor="end")?>([^<]*)<\/text>/g)) {
      const spec = RUNS[m[1]];
      const text = unescape(m[5]);
      const width = Array.from(text).length * EM[spec.face] * spec.size;
      const x = Number(m[2]);
      const anchoredEnd = m[4] !== undefined;
      found.push({ pane, kind: m[1], y: Number(m[3]), text, x0: anchoredEnd ? x - width : x, x1: anchoredEnd ? x : x + width });
    }
  }
  return found;
}

// Boxes that stress every line: long text everywhere, long states, partial spend.
function crowded() {
  const long = (c, n) => c.repeat(n);
  const states = ["waiting", "starting", "blocked", "working", "exited", "idle"];
  const nodes = [
    {
      pane: "m",
      depth: 0,
      team: "m",
      children: states.map((_, i) => "w" + i),
      name: "flockdeck-planning-manager",
      role: "manager",
      agent: "claude",
      status: "working",
      activity: "working",
      live: long("e", 80),
      branch: long("b", 80),
      dirty: 12,
      untracked: 3,
      task: { id: "T1234", title: "x", state: "in-progress" },
    },
  ];
  states.forEach((st, i) => {
    nodes.push({
      pane: "w" + i,
      parent: "m",
      depth: 1,
      team: "m",
      name: "flockdeck-planning-worker",
      role: "reviewer-and-tester",
      agent: "claude-sonnet",
      status: st,
      activity: st,
      live: long("e", 80),
      branch: "work/" + long("b", 60),
      dirty: 120,
      task: { id: "T12345678", title: "x", state: "in-progress" },
    });
  });
  return { type: "graph", teams: [{ id: "m", manager: "m", members: nodes.map((n) => n.pane), spendUSD: 12.4, spendPartial: true }], nodes, edges: [], solo: [] };
}

function assertFits(runs, label) {
  assert.ok(runs.length > 0, label + ": no text runs found");
  for (const r of runs) {
    assert.ok(r.x0 >= 4 && r.x1 <= BOX_W - 4, `${label}: ${r.pane} ${r.kind} "${r.text}" runs from ${r.x0.toFixed(0)} to ${r.x1.toFixed(0)} in a ${BOX_W} box`);
    if (r.kind === "gnode-spend") assert.ok(r.y <= BOX_H - 2, `${label}: spend is below the box`);
    if (r.textFits !== undefined) assert.ok(r.textFits, `${label}: ${r.pane} chip text is wider than its pill`);
  }
  // Runs that share a line, or a chip and the line it could meet, must not touch.
  const byPane = new Map();
  for (const r of runs) {
    if (!byPane.has(r.pane)) byPane.set(r.pane, []);
    byPane.get(r.pane).push(r);
  }
  for (const [pane, list] of byPane) {
    for (let i = 0; i < list.length; i++) {
      for (let j = i + 1; j < list.length; j++) {
        const a = list[i];
        const b = list[j];
        // A text spans from 12 above its baseline to 3 below; a chip is its pill.
        const span = (r) => (r.kind === "chip" ? [r.y, r.y + 14] : [r.y - 12, r.y + 3]);
        const [a0, a1] = span(a);
        const [b0, b1] = span(b);
        if (a1 <= b0 || b1 <= a0) continue;
        assert.ok(a.x1 + 4 <= b.x0 || b.x1 + 4 <= a.x0, `${label}: ${pane} ${a.kind} "${a.text}" meets ${b.kind} "${b.text}"`);
      }
    }
  }
}

test("every text in a crowded box fits the box and keeps clear of its neighbours", () => {
  assertFits(runsOf(draw(crowded())), "crowded");
});

test("every text in the fixtures fits its box", () => {
  for (const name of names) assertFits(runsOf(draw(load(name))), name);
});

test("a partial spend is on the foot line, at the right edge, clear of the message count", () => {
  const msg = crowded();
  msg.edges = [{ kind: "message", from: "w0", to: "m", id: 1, time: "2026-10-21T08:00:00Z", text: "x", delivery: "read" }];
  const runs = runsOf(draw(msg)).filter((r) => r.pane === "m");
  const spend = runs.find((r) => r.kind === "gnode-spend");
  assert.ok(spend, "no spend run");
  assert.equal(spend.y, DEFAULTS.nodeH - 6, "the manager has nothing waiting on the person, so its box is the quiet height");
  assert.ok(spend.x1 <= BOX_W - 12 + 0.01);
});
test("a long name is cut so that it stops before the state word", () => {
  const runs = runsOf(draw(crowded()));
  for (const w of ["w0", "w1"]) {
    const name = runs.find((r) => r.pane === w && r.kind === "gnode-name");
    const state = runs.find((r) => r.pane === w && r.kind === "gnode-state");
    assert.ok(name.x1 + 4 <= state.x0, `${w}: "${name.text}" meets "${state.text}"`);
    assert.ok(name.text.endsWith("…"));
  }
});

test("the estimate in this test fails for a line that is too long", () => {
  // The same estimate applied to an uncut 31-character mono line, which the first version drew.
  const out = '<g class="gnode x" data-pane="q"><text class="gnode-live" x="12" y="67">' + "e".repeat(31) + "</text></g>";
  assert.throws(() => assertFits(runsOf(out), "uncut"));
});

test("a manager's box says how many helpers it has against its cap", () => {
  const out = draw(load("02-eight-helpers-permission-and-question"));
  assert.match(out, /<text class="gnode-cap"[^>]*>8 of 8, at the cap<\/text>/);
  const six = draw(load("01-six-helpers-one-permission"));
  assert.match(six, /<text class="gnode-cap"[^>]*>6 of 8 helpers<\/text>/);
  assertFits(runsOf(out), "capped");
});
