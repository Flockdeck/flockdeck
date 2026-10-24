// The bar above the graph for two agents that changed the same files.
import { test } from "node:test";
import { readFileSync, writeFileSync, existsSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import assert from "node:assert/strict";
import { render } from "../assets/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { managerOf } from "../assets/graph/ManagerBar.js";
import { OverlapBar, overlapsOf, askText, RESOLVED_MS } from "../assets/graph/OverlapBar.js";
import { createDom, install, settle } from "./dom.mjs";

const graph = (needs) => ({
  type: "graph",
  teams: [{ id: "m", manager: "m", members: ["m", "a", "b"], needs }],
  nodes: [
    { pane: "m", depth: 0, team: "m", children: ["a", "b"], name: "boss", status: "idle", activity: "idle" },
    { pane: "a", parent: "m", depth: 1, team: "m", name: "alpha", status: "working", activity: "working" },
    { pane: "b", parent: "m", depth: 1, team: "m", name: "<img src=x>", status: "working", activity: "working" },
  ],
  edges: [],
  solo: [],
});
const conflict = { id: "c", kind: "conflict", pane: "a", other: "b", text: "limiter.go, store.go and 2 more" };
const modelOf = (needs) => buildGraphModel(graph(needs));

test("no conflict, no bar; a conflict names both agents and the files, as text", () => {
  assert.equal(renderToString(html`<${OverlapBar} model=${modelOf([])} send=${() => true} />`), "");
  const out = renderToString(html`<${OverlapBar} model=${modelOf([conflict])} manager=${{ pane: "m", name: "boss" }} send=${() => true} onSee=${() => {}} />`);
  assert.match(out, /alpha and &lt;img src=x>/);
  assert.match(out, /limiter\.go, store\.go and 2 more/);
  assert.ok(!out.includes("<img"));
  assert.match(out, /Ask the manager to decide now/);
  assert.match(out, /See in Changes/);
  assert.deepEqual(overlapsOf(modelOf([conflict])).map((o) => [o.a, o.b]), [["a", "b"]]);
});

test("asking sends one paneSend to the manager naming both agents and the files, and says so", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const sent = [];
    const model = modelOf([conflict]);
    render(html`<${OverlapBar} model=${model} manager=${managerOf(model)} send=${(c) => { sent.push(c); return true; }} />`, box);
    await settle();
    box.querySelector(".overlap-ask").click();
    await settle();
    assert.equal(sent.length, 1);
    assert.equal(sent[0].cmd, "paneSend");
    assert.equal(sent[0].id, "m");
    assert.equal(sent[0].text, askText(overlapsOf(model)[0]));
    assert.match(sent[0].text, /alpha and/);
    assert.equal(box.querySelector(".overlap-ask").disabled, true);
    assert.equal(box.querySelector(".overlap-ask").textContent, "Manager asked");
    render(null, box);
  } finally {
    restore();
  }
});

test("a failed send is said and can be tried again; a team with no manager cannot ask", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const model = modelOf([conflict]);
    render(html`<${OverlapBar} model=${model} manager=${managerOf(model)} send=${() => false} />`, box);
    await settle();
    box.querySelector(".overlap-ask").click();
    await settle();
    assert.match(box.querySelector(".overlap-note").textContent, /not asked/);
    assert.equal(box.querySelector(".overlap-ask").disabled, false);
    render(html`<${OverlapBar} model=${model} manager=${null} send=${() => true} />`, box);
    await settle();
    assert.equal(box.querySelector(".overlap-ask").disabled, true);
    render(null, box);
  } finally {
    restore();
  }
});

test("when the overlap ends the bar says it is resolved, for a while", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const draw = (needs) => render(html`<${OverlapBar} model=${modelOf(needs)} send=${() => true} />`, box);
    draw([conflict]);
    await settle();
    assert.ok(box.querySelector(".overlap-bar"));
    draw([]);
    await settle();
    assert.equal(box.querySelector(".overlap-bar"), null);
    assert.match(box.querySelector(".overlap-resolved").textContent, /Overlap resolved: alpha and/);
    assert.equal(RESOLVED_MS, 14000);
    render(null, box);
  } finally {
    restore();
  }
});

// Fixture 01 has two helpers on the same file (cart and tax, cart/total.go): the
// bar drawn for it is kept as a snapshot, UPDATE_SNAPSHOTS=1 to write it again.
test("fixture 01: the overlap bar is the snapshot", () => {
  const here = dirname(fileURLToPath(import.meta.url));
  const msg = JSON.parse(readFileSync(join(here, "../../../server/testdata/graph-fixtures/01-six-helpers-one-permission.json"), "utf8"));
  const model = buildGraphModel(msg);
  const out = renderToString(html`<${OverlapBar} model=${model} manager=${managerOf(model)} send=${() => true} onSee=${() => {}} />`).replace(/></g, ">\n<") + "\n";
  const file = join(here, "snapshots", "01-overlap-bar.html");
  if (process.env.UPDATE_SNAPSHOTS === "1") {
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, out);
  }
  assert.ok(existsSync(file), `no snapshot ${file}; run with UPDATE_SNAPSHOTS=1`);
  assert.equal(out, readFileSync(file, "utf8").replace(/\r\n/g, "\n"));
  assert.match(out, /tax and cart/);
});
