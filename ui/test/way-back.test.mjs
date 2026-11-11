// The way back to a terminal pane: a structured pane that is not running offers
// "Restart as terminal" when the desktop says its agent has a terminal version, and
// offers nothing when it does not.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Drawer } from "../src/drawer/Drawer.js";
import { WayBack, wayBackOf, wayBackCommand, WAY_BACK_WORDS } from "../src/drawer/WayBack.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore, paneState } from "./termfakes.mjs";

const NOW = Date.parse("2026-11-06T09:20:00Z");
const node = (over = {}) => ({
  pane: "p2", kind: "terminal", structured: false, name: "w1", role: "worker", agent: "claude-acp", shell: false,
  status: "exited", activity: "exited", since: "2026-11-06T09:14:02Z", branch: "", dirty: 0, untracked: 0, ahead: 0, behind: 0,
  ...over,
});
const failed = { err: "Claude Code (structured) is a structured agent, and new panes are set to start in a terminal.", terminalWayBack: "claude" };
const storeOf = (pane, top = {}, hello = {}) => fakeStore({ hello: { type: "hello", ...hello }, state: paneState({ p2: pane }), ...top });

test("wayBackOf and wayBackCommand take only what the desktop sent", () => {
  assert.equal(wayBackOf({ terminalWayBack: "claude" }), "claude");
  assert.equal(wayBackOf({}), "");
  assert.equal(wayBackOf(null), "");
  assert.equal(wayBackOf({ terminalWayBack: 3 }), "");
  assert.deepEqual(wayBackCommand("p2"), { cmd: "restartPane", id: "p2", kind: "terminal" });
  assert.equal(wayBackCommand(""), null);
});

test("the notice gives the reason as text and says what happens", () => {
  const out = renderToString(html`<${WayBack} paneId="p2" why="<b>boom</b>" onRestart=${() => {}} />`);
  assert.ok(out.includes("&lt;b>boom&lt;/b>") && !out.includes("<b>"), "the reason was not escaped");
  assert.ok(out.includes("Restart as terminal"));
  assert.ok(out.includes(WAY_BACK_WORDS));
  assert.doesNotMatch(out, /prefs\.json/);
});

test("a pane that failed to start is offered the way back in place of a terminal", () => {
  const out = renderToString(html`<${Drawer} store=${storeOf(failed)} node=${node()} pane=${failed} now=${NOW} />`);
  assert.ok(out.includes("Restart as terminal"));
  assert.ok(out.includes("new panes are set to start in a terminal"));
  assert.ok(!out.includes("terminal-pane"), "a terminal was drawn for a pane that has none");
});

test("a structured pane that is not running is offered it above its own view", () => {
  const pane = { structured: true, terminalWayBack: "opencode" };
  const out = renderToString(html`<${Drawer} store=${storeOf(pane)} node=${node({ structured: true, agent: "opencode-acp" })} pane=${pane} now=${NOW} />`);
  assert.ok(out.includes("Restart as terminal"));
  // The structured view is still drawn, here as its loading skeleton (the words "Loading the agent." are put in its
  // live region after it is in the page, so they are not in a first render), and the offer comes before it.
  const view = out.indexOf("conv-skeleton");
  assert.ok(view > 0, "the structured view went");
  assert.ok(out.indexOf("Restart as terminal") < view, "the offer is not above the view");
  assert.ok(!out.includes("terminal-pane"), "a terminal was drawn for a pane that has none");
});

test("no way back is offered where the desktop sent none, to a viewer, or through the relay", () => {
  const none = { err: "the agent is unavailable" };
  assert.ok(!renderToString(html`<${Drawer} store=${storeOf(none)} node=${node()} pane=${none} now=${NOW} />`).includes("Restart as terminal"));
  const running = { structured: true };
  assert.ok(!renderToString(html`<${Drawer} store=${storeOf(running)} node=${node({ structured: true })} pane=${running} now=${NOW} />`).includes("Restart as terminal"));
  const viewer = storeOf(failed, {}, { role: "viewer" });
  const remote = storeOf(failed, { remote: true });
  for (const [name, st] of [["viewer", viewer], ["relay", remote]]) {
    const out = renderToString(html`<${Drawer} store=${st} node=${node()} pane=${failed} now=${NOW} />`);
    assert.ok(!out.includes("Restart as terminal"), name + " was offered it");
  }
});

test("the button sends restartPane with kind terminal for that pane", async () => {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  try {
    const st = storeOf(failed);
    render(html`<${Drawer} store=${st} node=${node()} pane=${failed} now=${NOW} />`, container);
    await settle();
    const btn = container.find(".way-back-btn");
    assert.ok(btn, "no button");
    await btn.click();
    await settle();
    assert.deepEqual(st.sent.filter((c) => c.cmd === "restartPane"), [{ cmd: "restartPane", id: "p2", kind: "terminal" }]);
  } finally {
    render(null, container);
    await settle();
    restore();
  }
});
