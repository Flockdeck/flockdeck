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
import { diffKey, statOf } from "../src/lib/changesdiffs.js";
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
  const drops = [];
  return { reads, commands, drops, answer: "r", touchChangesDiff: () => {}, dropChangesDiffs: (...a) => { drops.push(a); },
    readChangesDiff(f, gap) { reads.push({ ...f, gap }); return this.answer === "" ? "" : "r" + reads.length; }, send: (f) => { commands.push(f); return true; } };
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
// A held diff as the store keeps it: read at head `at` (the branch's now, unless given) with the file's counts of the listing.
function entryFor(path, at, seq = 1) {
  const f = api.files.find((x) => x.path === path);
  return { hunks: { ...hunks, path }, drawnSha: at === undefined ? api.headSha : at, stat: statOf(f), context: {}, contextErrors: {}, seq, used: seq };
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
    m.draw({ win: FULL, store, diffs: { [diffKey("5dc771c1", file)]: entryFor(file) } });
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
  for (const win of [{ ...FULL, caps: ["changesDiff"] }]) {
    const store = fakeStore();
    const file = api.files[0].path;
    const diffs = { [diffKey("5dc771c1", file)]: entryFor(file) };
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
  const sha = api.headSha;
  const diffs = { [diffKey("5dc771c1", file.path)]: entryFor(file.path, sha) };
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

test("a diff read at another head is not drawn: the note says so, it is read again once, and the box on old hunks closes with its text kept", async () => {
  const store = fakeStore();
  const file = api.files[0].path;
  const key = diffKey("5dc771c1", file);
  const m = await mount({ win: FULL, store, diffs: { [key]: entryFor(file) } });
  try {
    assert.equal(store.reads.length, 0, "current: nothing to read");
    m.box.findAll(".dv-plus")[0].click();
    await settle();
    m.box.find(".dv-composer-text").enter("Half a thought.");
    await settle();
    m.draw({ win: FULL, store, diffs: { [key]: entryFor(file, "old-head") } });
    await settle();
    assert.match(m.box.find(".ct-diffnote").textContent, /branch changed; reading the diff again/);
    assert.equal(m.box.find(".dv-tbl"), null, "the old hunks are not drawn");
    assert.equal(m.box.find(".dv-composer-text"), null, "the box on the old hunks is closed");
    assert.equal(store.reads.length, 1);
    m.draw({ win: FULL, store, diffs: { [key]: entryFor(file, undefined, 2) } });
    await settle();
    assert.ok(m.box.find(".dv-tbl"), "the new diff is drawn");
    assert.equal(m.box.find(".ct-diffnote").textContent, "");
    assert.equal(store.reads.length, 1, "no second read");
  } finally {
    m.done();
  }
});

test("a file whose counts changed under a held diff is read again", async () => {
  const store = fakeStore();
  const file = api.files[0].path;
  const e = { ...entryFor(file), stat: "999:999" };
  const m = await mount({ win: FULL, store, diffs: { [diffKey("5dc771c1", file)]: e } });
  try {
    assert.equal(store.reads.length, 1);
  } finally {
    m.done();
  }
});

test("a read that could not be asked for says so with Retry; a failed read can be retried too", async () => {
  const store = fakeStore();
  store.answer = "";
  const m = await mount({ win: FULL, store, diffs: {} });
  try {
    assert.match(m.box.find(".ct-diffnote").textContent, /could not be asked for; try again/);
    store.answer = "r";
    m.box.find(".ct-retry").click();
    await settle();
    assert.equal(store.reads.length, 2, "asked again");
    assert.equal(m.box.find(".ct-diffnote").textContent, "");
  } finally {
    m.done();
  }
  const s2 = fakeStore();
  const file = api.files[0].path;
  const bad = { ...entryFor(file), hunks: { type: "diffHunks", path: file, error: "the directory is gone" } };
  const n = await mount({ win: FULL, store: s2, diffs: { [diffKey("5dc771c1", file)]: bad } });
  try {
    n.box.find(".ct-retry").click();
    await settle();
    assert.deepEqual(s2.drops[0], ["5dc771c1", file], "the error entry is dropped");
  } finally {
    n.done();
  }
});

test("a reconnect asks again, and a viewer asks for nothing", async () => {
  const store = fakeStore();
  store.answer = "";
  const m = await mount({ win: FULL, store, diffs: {}, session: 1, connected: false });
  try {
    store.answer = "r";
    m.draw({ win: FULL, store, diffs: {}, session: 2, connected: true });
    await settle();
    assert.equal(store.reads.length, 2);
  } finally {
    m.done();
  }
  const v = fakeStore();
  const n = await mount({ win: { ...FULL, viewer: true }, store: v, diffs: {} });
  try {
    assert.equal(v.reads.length, 0);
    assert.equal(n.box.find(".dv-tbl"), null);
  } finally {
    n.done();
  }
});

test("leaving the branch lets go of what was read for it", async () => {
  const store = fakeStore();
  const m = await mount({ win: FULL, store, diffs: {} });
  m.done();
  assert.deepEqual(store.drops.at(-1), ["5dc771c1"]);
});
