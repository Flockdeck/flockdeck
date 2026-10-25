// The files and diffs of a branch in the Changes view, mounted: what it asks the store for,
// what it draws for each kind of window, and that a comment goes out with the head the diff was read at.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { changesTeamOf } from "../src/lib/changesteam.js";
import { diffKey } from "../src/lib/changesdiffs.js";
import { BranchDiff } from "../src/changes/BranchDiff.js";
import { createDom, install, settle } from "./dom.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", "built-changes-" + name + ".json"), "utf8"));
const team = changesTeamOf(fixture("01-team-mixed"));
const api = team.branches.find((b) => b.pane === "5dc771c1");
const FILE = "internal/limit/limit.go";
const hunks = fixture("04-diff-hunks");
const FULL = { viewer: false, relay: false, caps: ["changesDiff", "changesComments", "changesChecks", "changesGate"] };

function fakeStore() {
  const reads = [];
  const commands = [];
  return { reads, commands, readChangesDiff: (f, gap) => { reads.push({ ...f, gap }); return "r" + reads.length; }, send: (f) => { commands.push(f); return true; } };
}

async function mount(props) {
  const dom = createDom();
  const restore = install(dom);
  const box = dom.document.createElement("div");
  dom.body.appendChild(box);
  const draw = (p) => render(html`<${BranchDiff} branch=${api} names=${{ "5dc771c1": "api" }} ...${p} />`, box);
  draw(props);
  await settle();
  return { box, draw, dom, done: () => { render(null, box); restore(); } };
}
const plusFor = (box, re) => box.findAll(".dv-plus").find((b) => re.test(b.getAttribute("aria-label")));

test("picking a file asks the store for its diff once, and a held diff is drawn", async () => {
  const store = fakeStore();
  const m = await mount({ win: FULL, store, diffs: {} });
  try {
    assert.equal(store.reads.length, 1);
    assert.deepEqual([store.reads[0].cmd, store.reads[0].id], ["diffHunks", "5dc771c1"]);
    assert.ok(api.files.some((f) => f.path === store.reads[0].file));
    const file = store.reads[0].file;
    m.draw({ win: FULL, store, diffs: { [diffKey("5dc771c1", file)]: { hunks: { ...hunks, path: file }, drawnSha: "drawn0", context: {}, seq: 1 } } });
    await settle();
    assert.equal(store.reads.length, 1, "no second read for a held diff");
    assert.ok(m.box.find(".dv-tbl"), "the diff is drawn");
  } finally {
    m.done();
  }
});

test("a window without the diff cap, or a relay window, draws the file list and asks for nothing", async () => {
  for (const win of [{ ...FULL, caps: ["changesTeam"] }, { ...FULL, relay: true }]) {
    const store = fakeStore();
    const m = await mount({ win, store, diffs: {} });
    try {
      assert.equal(store.reads.length, 0);
      assert.equal(m.box.find(".dv-tbl"), null);
      assert.ok(m.box.find(".dv-filebtn"));
    } finally {
      m.done();
    }
  }
});

test("without the comments cap, or for a viewer, the diff is read-only", async () => {
  for (const win of [{ ...FULL, caps: ["changesDiff"] }, { ...FULL, viewer: true }]) {
    const store = fakeStore();
    const file = api.files[0].path;
    const diffs = { [diffKey("5dc771c1", file)]: { hunks: { ...hunks, path: file }, drawnSha: "d", context: {}, seq: 1 } };
    const m = await mount({ win, store, diffs });
    try {
      assert.ok(m.box.find(".dv-tbl"));
      assert.equal(m.box.findAll(".dv-plus").length, 0);
      assert.equal(m.box.find(".dv-filecm-btn"), null);
    } finally {
      m.done();
    }
  }
});

test("a comment is sent through the store with the head the diff was read at and its own req", async () => {
  const store = fakeStore();
  const file = api.files[0];
  const sha = "readAtThisHead1";
  const diffs = { [diffKey("5dc771c1", file.path)]: { hunks: { ...hunks, path: file.path }, drawnSha: sha, context: {}, seq: 1 } };
  const m = await mount({ win: FULL, store, diffs });
  try {
    const plus = m.box.findAll(".dv-plus")[0];
    assert.ok(plus, "a + to comment on");
    plus.click();
    await settle();
    const ta = m.box.find(".dv-composer-text");
    ta.enter("Cover the boundary.");
    await settle();
    ta.keydown("Enter", { ctrlKey: true });
    await settle();
    assert.equal(store.commands.length, 1);
    const c = store.commands[0];
    assert.deepEqual([c.cmd, c.id, c.expectSha, c.text], ["commentAdd", "5dc771c1", sha, "Cover the boundary."]);
    assert.ok(c.req, "the flow's own req, for matching the answer");
  } finally {
    m.done();
  }
});
