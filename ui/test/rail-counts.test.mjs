// The rail's counts are panes, not requests, and say so.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { RailItem } from "../src/shell/rail.js";

const draw = (p) => renderToString(html`<${RailItem} project=${{ root: "/r", name: "api", ...p }} active=${false} />`);

test("the item's tooltip and each badge say what the number counts", () => {
  const out = draw({ waiting: 2, working: 10 });
  assert.match(out, /title="api, 2 waiting, 10 working"/);
  assert.match(out, /title="2 agents waiting \(panes, not requests\)"/);
  assert.match(out, /title="10 agents working"/);
  assert.match(draw({ waiting: 1, working: 1 }), /title="1 agent waiting \(panes, not requests\)"/);
  assert.ok(!draw({}).includes("badge"));
});
