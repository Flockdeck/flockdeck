// Feeds every fixture to a front end the way a window gets it, and prints one
// JSON line per fixture: {"fixture", "ok", "error"}.
//
//   node run.mjs <root> <fixturesDir>
//
// <root> holds the front end as src/ and the render helper as
// test/vendor/render-to-string.mjs, which imports ../../src/vendor/preact.mjs.
// Nothing here is imported from the tree under test but its own modules, so the
// same script runs against any version of them.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [root, fixtures] = process.argv.slice(2);
const load = (rel) => import(pathToFileURL(join(root, rel)).href);

class FakeWebSocket {
  constructor(url) { this.url = url; this.readyState = 0; this.sent = []; FakeWebSocket.last = this; }
  send(data) { this.sent.push(data); }
  close() { this.readyState = 3; if (this.onclose) this.onclose({}); }
  open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
  frame(obj) { if (this.onmessage) this.onmessage({ data: JSON.stringify(obj) }); }
}

const { createStore } = await load("src/lib/store.js");
const { buildGraphModel } = await load("src/graph/model.js");
// The canvas Graph was removed from the current front end, and the previous one
// still has it: draw what a tree has, the canvas where there is one and the Team
// outline where there is not.
const canvas = await load("src/graph/Graph.js").then((m) => m.Graph, () => null);
const { Tree } = canvas ? { Tree: null } : await load("src/graph/Tree.js");
const { html } = await load("src/html.js");
const { renderToString } = await load("test/vendor/render-to-string.mjs");

const names = readdirSync(fixtures).filter((f) => f.endsWith(".json")).sort();
for (const name of names) {
  const out = { fixture: name, ok: true, error: "" };
  try {
    const msg = JSON.parse(readFileSync(join(fixtures, name), "utf8"));
    const store = createStore({ url: "ws-compat", WebSocket: FakeWebSocket, setTimeout: () => 0, clearTimeout: () => {}, now: () => 0 });
    store.start();
    const ws = FakeWebSocket.last;
    store.watchGraph(msg.root || "");
    ws.open();
    ws.frame({ type: "hello", keys: [], prefs: {} });
    ws.frame({ type: "graph", ...msg });
    const g = store.getState().graph;
    if (!g || !Array.isArray(g.nodes) || g.nodes.length !== msg.nodes.length) throw new Error("the store did not keep the graph push");
    const model = buildGraphModel(g);
    if (canvas) {
      if (!renderToString(html`<${canvas} model=${model} />`).includes("<svg")) throw new Error("the graph did not draw");
    } else if (!renderToString(html`<${Tree} model=${model} />`).includes('class="tree')) {
      throw new Error("the team outline did not draw");
    }
    store.stop();
  } catch (err) {
    out.ok = false;
    out.error = String(err && err.stack ? err.stack.split("\n").slice(0, 3).join(" | ") : err);
  }
  console.log(JSON.stringify(out));
}
