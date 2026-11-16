// The rail's counts are panes, not requests, and each says so: the orange one is a
// button of its own (a button inside the project's button is not valid), the teal
// one a labelled span, and both keep their names when the rail is collapsed.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { RailItem } from "../src/shell/rail.js";

const draw = (p, active = false) => renderToString(html`<${RailItem} project=${{ root: "/r", name: "api", ...p }} active=${active} />`);

test("the project button's label and tooltip carry both counts in words", () => {
  const out = draw({ waiting: 2, working: 10 });
  assert.match(out, /aria-label="api, 2 waiting for you, 10 working" title="api, 2 waiting for you, 10 working"/);
  assert.match(draw({ waiting: 0, working: 1 }), /aria-label="api, 1 working"/);
  assert.match(draw({}), /aria-label="api"/);
});

test("the orange count is a button with an accessible name and a tooltip, for 1 and for 2", () => {
  const two = draw({ waiting: 2 });
  assert.match(two, /<button type="button" class="badge badge--waiting rail-waiting"[^>]*aria-label="2 agents waiting for you. Open the list"[^>]*title="2 agents waiting for you. Open the project and list them \(panes, not requests\)">2<\/button>/);
  const one = draw({ waiting: 1 });
  assert.match(one, /aria-label="1 agent waiting for you. Open the list"/);
  assert.match(one, /title="1 agent waiting for you. Open the project and list them/);
});

test("the orange count is not inside the project's button", () => {
  const out = draw({ waiting: 2, working: 3 });
  const project = /<button[^>]*class="rail-item[^"]*"[\s\S]*?<\/button>/.exec(out)[0];
  assert.ok(!project.includes("badge--waiting"), "a button inside a button");
  assert.ok(project.includes("badge--working"), "the working count stays in the row's button");
});

test("on the open project the tooltip says it opens the list, not the project", () => {
  assert.match(draw({ waiting: 2 }, true), /title="2 agents waiting for you. Open the list \(panes, not requests\)"/);
});

test("the teal count is named for a reader and has a tooltip", () => {
  const out = draw({ working: 10 });
  assert.match(out, /<span class="badge badge--working" role="img" aria-label="10 agents working" title="10 agents working">10<\/span>/);
  assert.match(draw({ working: 1 }), /aria-label="1 agent working"/);
  assert.ok(!draw({}).includes("badge"));
});

test("a project with nothing waiting has no orange button", () => {
  assert.ok(!draw({ working: 4 }).includes("rail-waiting"));
});
