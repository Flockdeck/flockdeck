// The needs-you badges and the strip: who needs the person and why, from the
// graph alone. The fixtures' .md files say who needs the person; the badges and
// the strip must say the same, in the graph's order, and no one else.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { buildGraphModel } from "../src/graph/model.js";
import { reasonOf, firstLine } from "../src/graph/Badge.js";
import { NeedsYou, rowOf, ageOf } from "../src/graph/NeedsYou.js";
import { summaryOf } from "../src/graph/Teams.js";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures");
const names = readdirSync(dir)
  .filter((f) => f.endsWith(".json"))
  .map((f) => f.slice(0, -5))
  .sort();
const load = (name) => JSON.parse(readFileSync(join(dir, name + ".json"), "utf8"));
const MAX_CARDS = 3;
const draw = (msg) => renderToString(html`<${NeedsYou} model=${buildGraphModel(msg)} onSelect=${() => {}} max=${100} />`);

// "- name (id8): reason", or "- a (id8) and b (id8): reason" for a conflict.
function expectedNeeds(name) {
  const md = readFileSync(join(dir, name + ".md"), "utf8").replace(/\r\n/g, "\n");
  const section = md.split("## Needs the person")[1].split("\n## ")[0];
  const out = [];
  for (const line of section.split("\n")) {
    if (!line.startsWith("- ")) continue;
    const panes = [...line.matchAll(/\(([0-9a-f]{8})\)/g)].map((m) => m[1]);
    const reason = line.slice(line.indexOf("): ") + 3);
    let kind = "";
    if (/^asks to run /.test(reason)) kind = "permission";
    else if (/^asks: /.test(reason)) kind = "ask";
    else if (/^task returned from review/.test(reason)) kind = "returned";
    else if (/ both change /.test(line)) kind = "conflict";
    else if (/^exited with /.test(reason)) kind = "down";
    else if (/ unread: /.test(reason)) kind = "stale-unread";
    else if (/^no output for /.test(reason)) kind = "stuck";
    out.push({ kind, panes, reason });
  }
  return out;
}

const WORDS = {
  permission: "asks to run a command",
  ask: "asks a question",
  returned: "task returned from review",
  conflict: "files overlap",
  "stale-unread": "has unread mail",
  stuck: "has gone quiet",
  setup: "needs setting up",
};

const unescape = (s) => s.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&amp;/g, "&");

// The strip the list draws, which the graph no longer carries.
const stripOf = (msg) => renderToString(html`<${NeedsYou} model=${buildGraphModel(msg)} onSelect=${() => {}} max=${MAX_CARDS} />`);

// The strip's rows, as [pane, kind, who, word].
function rowsOf(out) {
  const found = [];
  for (const m of out.matchAll(/<li class="needs-row[^"]*" data-pane="([^"]*)" data-kind="([^"]*)">(.*?)<\/li>/g)) {
    const who = /<span class="needs-who">([^<]*)</.exec(m[3]);
    const word = /<span class="needs-word">([^<]*)</.exec(m[3]);
    found.push([unescape(m[1]), m[2], who ? unescape(who[1]) : "", word ? unescape(word[1]) : ""]);
  }
  return found;
}

for (const name of names) {
  test(`fixture ${name}: the strip has a row for each item that needs the person, with the right word`, () => {
    const msg = load(name);
    const expected = expectedNeeds(name);
    assert.ok(expected.length > 0, "the fixture has needs");
    const rows = rowsOf(draw(msg));

    const wantPanes = new Set(expected.flatMap((e) => e.panes));
    assert.ok(rows.length > 0, "the strip has rows");
    for (const r of rows) assert.ok(wantPanes.has(r[0]), `a row for ${r[0]}, which does not need the person`);
    for (const e of expected) {
      const row = rows.find((r) => e.panes.includes(r[0]) && r[1] === e.kind);
      assert.ok(row, `no row for ${e.panes.join(" and ")}`);
      if (expected.filter((x) => x.panes.includes(e.panes[0])).length === 1) {
        if (e.kind === "down") assert.match(row[3], /^exited with code \d+$/);
        else assert.equal(row[3], WORDS[e.kind]);
      }
    }
  });
}

test("every reason has the phrase the task names", () => {
  for (const [kind, word] of Object.entries(WORDS)) assert.equal(reasonOf({ kind, text: "x" }).word, word);
  assert.equal(reasonOf({ kind: "down", text: "exited with code 3" }).word, "exited with code 3");
  assert.equal(reasonOf({ kind: "down", text: "failed: no such program" }).word, "failed");
  assert.equal(reasonOf({ kind: "down", text: "failed: no such program" }).detail, "no such program");
  assert.equal(reasonOf({ kind: "down", text: "failed" }).word, "failed");
});

test("a need for a pane that is not a node is in the strip, with no error", () => {
  const msg = load("01-six-helpers-one-permission");
  msg.teams[0].needs.push({ id: "ask:ghost", kind: "ask", pane: "ghost123", time: "2026-10-21T08:00:00Z", text: "Who am I?" });
  const rows = rowsOf(stripOf(msg));
  const ghost = rows.find((r) => r[0] === "ghost123");
  assert.ok(ghost, "a row for the missing pane");
  assert.equal(ghost[2], "ghost123", "named by its id");
  assert.equal(ghost[3], "asks a question");
  assert.equal(rows.length, expectedNeeds("01-six-helpers-one-permission").length + 1);
  // It is not a button, because there is no pane to open.
  const picked = [];
  const strip = renderToString(html`<${NeedsYou} model=${buildGraphModel(msg)} onSelect=${(p) => picked.push(p)} />`);
  assert.equal(strip.split("<button").length - 1, rows.length - 1);
});

test("text from an agent in a need is drawn as text, in the badge and in the strip", () => {
  const evil = '<img src=x onerror="alert(1)"><script>alert(2)</script>';
  const msg = {
    type: "graph",
    teams: [{ id: "m", manager: "m", members: ["m"], needs: [{ id: evil, kind: "permission", pane: "m", text: evil }, { id: "k", kind: evil, pane: "m", text: evil }] }],
    nodes: [{ pane: "m", depth: 0, team: "m", name: evil, status: "waiting", activity: "waiting" }],
    edges: [],
    solo: [],
  };
  const out = draw(msg);
  assert.ok(!out.includes("<img"), out);
  assert.ok(!out.includes("<script"), out);
  assert.ok(!/ onerror=/.test(out.replace(/&quot;/g, "")) || !out.includes('"alert(1)"'), out);
});

test("firstLine skips blank lines and trims", () => {
  assert.equal(firstLine("\n  \n  go test ./...  \nsecond"), "go test ./...");
  assert.equal(firstLine(null), "");
  assert.equal(firstLine("a\r\nb"), "a");
});

test("the strip says so when nothing needs the person, and counts what the graph left out", () => {
  const none = renderToString(html`<${NeedsYou} model=${buildGraphModel({ type: "graph", teams: [], nodes: [], solo: [] })} />`);
  assert.match(none, /Nothing needs you\./);
  const some = buildGraphModel({
    type: "graph",
    teams: [{ id: "m", manager: "m", members: ["m"], needsHidden: 4, needs: [{ id: "1", kind: "ask", pane: "m", text: "x" }] }],
    nodes: [{ pane: "m", depth: 0, team: "m", name: "boss", status: "idle", activity: "idle" }],
    solo: [],
  });
  assert.match(renderToString(html`<${NeedsYou} model=${some} />`), /and 4 more not shown/);
});

test("rowOf names a pane by its id when it has no node", () => {
  const model = buildGraphModel({ type: "graph", teams: [], nodes: [], solo: [], soloNeeds: [{ id: "x", kind: "down", pane: "zz", text: "exited with code 2" }] });
  const row = rowOf(model, model.needs[0]);
  assert.equal(row.who, "zz");
  assert.equal(row.word, "exited with code 2");
  assert.equal(row.shown, false);
});

// A width estimate for each text in a badge, as the node tests make for a box.
const EM = { mono: 0.6, bold: 0.58 };

test("ageOf says how long ago, and nothing for an unknown or future time", () => {
  const now = Date.parse("2026-10-24T12:00:00Z");
  assert.equal(ageOf("2026-10-24T11:59:30Z", now), "now");
  assert.equal(ageOf("2026-10-24T11:51:00Z", now), "9m");
  assert.equal(ageOf("2026-10-24T09:00:00Z", now), "3h");
  assert.equal(ageOf("2026-10-21T12:00:00Z", now), "3d");
  assert.equal(ageOf("0001-01-01T00:00:00Z", now), "", "an unknown time");
  assert.equal(ageOf("2026-10-24T12:05:00Z", now), "", "a time ahead of the clock");
  assert.equal(ageOf("", now), "");
});

test("the left column draws at most three cards, loud first, counts the rest and keeps old unread mail quiet", () => {
  const msg = load("04-eight-helpers-three-waiting");
  const model = buildGraphModel(msg);
  const mk = (kind, i) => ({ id: kind + ":x" + i, kind, pane: model.nodes[0].pane, time: "2026-10-24T11:00:00Z", text: "t" + i });
  model.needs = [mk("stale-unread", 0), mk("ask", 1), mk("permission", 2), mk("down", 3), mk("ask", 4), mk("conflict", 5)];
  const out = renderToString(html`<${NeedsYou} model=${model} max=${MAX_CARDS} />`);
  const kinds = [...out.matchAll(/data-kind="([^"]*)"/g)].map((m) => m[1]);
  assert.deepEqual(kinds, ["ask", "permission", "down", "stale-unread"], "three loud cards in the graph's order, then the quiet row");
  assert.match(out, /needs-row--stale-unread needs-row--quiet/);
  assert.match(out, /and 2 more, marked on the graph/);
  assert.match(out, /Needs you \(6\)/);
});

test("a stuck pane is a known, quiet kind with its own word", () => {
  const msg = {
    type: "graph",
    teams: [{ id: "m", manager: "m", members: ["m", "a"], needs: [{ id: "stuck:a", kind: "stuck", pane: "a", text: "no output for 12m" }] }],
    nodes: [
      { pane: "m", depth: 0, team: "m", children: ["a"], name: "boss", status: "idle", activity: "idle" },
      { pane: "a", parent: "m", depth: 1, team: "m", name: "alpha", status: "working", activity: "working" },
    ],
    edges: [],
    solo: [],
  };
  const model = buildGraphModel(msg);
  assert.equal(model.needs[0].known, true);
  const r = reasonOf(model.needs[0]);
  assert.equal(r.word, "has gone quiet");
  assert.equal(r.detail, "no output for 12m");
  assert.ok(!draw(msg).includes("gnode--loud"), "a quiet pane was drawn loud");
});
