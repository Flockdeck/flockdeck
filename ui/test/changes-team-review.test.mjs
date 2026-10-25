// The Changes view across the team, after review: unknown states, what the list says
// about a gate, hidden characters, the status line, the viewer window and reconnects.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { attentionOf, changesTeamOf } from "../src/lib/changesteam.js";
import { ChangesList } from "../src/changes/ChangesList.js";
import { TeamChanges } from "../src/changes/TeamChanges.js";
import { ChangesView } from "../src/shell/Views.js";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const mixed = () => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", "built-changes-01-team-mixed.json"), "utf8"));
const names = { "5dc771c1": "api", "366c9143": "tax", "6fb40971": "cart" };
const only = (b) => changesTeamOf({ branches: [b] }).branches[0];
const cart = () => changesTeamOf(mixed()).branches[2];

test("a check or review state the page does not know needs a look, and a gate blocked only by checks or the remote says so", () => {
  const base = cart();
  assert.equal(attentionOf(only({ ...base, gate: { state: "blocked", blockers: [] }, checks: [{ name: "x", state: "weird" }] })).text, "Check status unclear");
  assert.equal(attentionOf(only({ ...base, gate: { state: "ready", blockers: [] }, reviews: [{ reviewer: "a", state: "odd" }] })).text, "Review status unclear");
  assert.equal(attentionOf(only({ ...base, gate: { state: "blocked", blockers: [{ kind: "checks", text: "still running" }] } })).text, "Checks not passing");
  const unpushed = only({ ...base, gate: { state: "blocked", blockers: [{ kind: "unpushed", text: "3 commits" }] } });
  assert.equal(attentionOf(unpushed).text, "Not pushed");
  assert.ok(renderToString(html`<${ChangesList} branches=${[unpushed]} names=${names} selected="" onPick=${() => {}} />`).includes("Not pushed"));
});

test("text from the data is drawn with its hidden characters shown, and plus and minus have a reading", () => {
  const t = changesTeamOf(mixed());
  t.branches[0].branch = "evil‮gp.exe";
  t.branches[0].taskId = "T​1";
  const out = renderToString(html`<${ChangesList} branches=${t.branches} names=${names} selected="" onPick=${() => {}} />`);
  assert.ok(out.includes("U+202E") && out.includes("U+200B"));
  assert.ok(!out.includes("‮") && !out.includes("​"));
  assert.ok(out.includes(" added, "), "a reading for a screen reader");
});

test("the status line is in the page from the first draw and says what the view is doing", () => {
  const draw = (props) => renderToString(html`<${TeamChanges} names=${names} ...${props} />`);
  const done = draw({ data: changesTeamOf({ rev: 1, root: "/r", branches: [] }) });
  assert.ok(/role="status"[^>]*><\/p>/.test(done), "empty when there is nothing to say");
  assert.ok(/role="status"[^>]*>Reading what changed/.test(draw({ data: null })));
  assert.ok(/role="status"[^>]*>Read 4 of 5 working trees\./.test(draw({ data: changesTeamOf(mixed()) })));
  assert.ok(draw({ data: null, viewer: true }).includes("needs full access"));
});

test("the view is drawn when the desk offers the cap, says so in a viewer window, and an old desk gets the single tree", () => {
  const nodes = [{ pane: "5dc771c1", name: "api" }];
  const snap = (role) => ({ hello: { api: { caps: ["changesTeam"] }, role }, remote: false, changesTeam: null, changesTeamGone: null });
  const draw = (s) => renderToString(html`<${ChangesView} nodes=${nodes} statePanes=${{}} openPane="" snap=${s} store=${{}} />`);
  assert.ok(draw(snap()).includes("Reading what changed"));
  assert.ok(draw(snap("viewer")).includes("needs full access"));
  assert.ok(!draw({ hello: { api: { caps: [] } }, remote: false }).includes("Reading what changed"));
});

test("a reconnect starts the revisions again, and a push for another project is not drawn", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  const caps = { api: { major: 1, minor: 3, caps: ["changesTeam"] } };
  ws.frame(hello(caps));
  store.watchChangesTeam("/work/project");
  ws.frame({ ...mixed(), rev: 5 });
  assert.equal(store.getState().changesTeam.rev, 5);
  ws.frame(hello(caps));
  ws.frame({ ...mixed(), rev: 1, branches: [] });
  assert.equal(store.getState().changesTeam.rev, 1, "the desktop restarted its revisions, so the first push after a reconnect is drawn");
  ws.frame({ ...mixed(), rev: 1, project: "again", branches: [] });
  assert.equal(store.getState().changesTeam.project, "project", "and a repeat of that revision is skipped again");
  ws.frame({ ...mixed(), root: "/work/other", rev: 9 });
  assert.equal(store.getState().changesTeam.rev, 1, "another project's push is not taken");
  store.stop();
});
