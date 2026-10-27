// The navigation menus (shell/menus-nav.js): what each lists for the part of the
// page it is on, and what choosing an item calls.
import { test } from "node:test";
import assert from "node:assert/strict";
import { MENUS, resolve } from "../src/shell/menus.js";

const el = (attrs) => ({ getAttribute: (k) => (k in attrs ? attrs[k] : null) });
const KEYS = [
  { id: "newAgentTab", keys: "Ctrl+Shift+T", label: "New agent tab" },
  { id: "newShellTab", keys: "", label: "New shell tab" },
  { id: "worktrees", keys: "", label: "Worktrees" },
  { id: "history", keys: "", label: "Resume a conversation", short: "Resume…" },
];
const labels = (items) => items.map((i) => (i.separator ? "-" : i.label));

function tabCtx(calls) {
  return {
    keys: KEYS,
    implemented: ["newAgentTab", "newShellTab", "history"],
    act: (id) => calls.push(["act", id]),
    tabDo: { step: (id, d) => calls.push(["step", id, d]), restore: (id) => calls.push(["restore", id]), archive: (id) => calls.push(["archive", id]) },
    nav: {
      tabs: [{ id: "a", kind: "team" }, { id: "solo", kind: "solo" }],
      archived: [{ id: "z", kind: "team" }],
      active: "solo",
      select: (id) => calls.push(["select", id]),
      canStep: (id, d) => d > 0,
      rename: (id) => calls.push(["rename", id]),
    },
  };
}

test("a goal tab can be opened, renamed, archived and moved; a disabled step is not run", () => {
  const calls = [];
  const ctx = tabCtx(calls);
  const items = resolve(MENUS.tab(el({ "data-tab": "a" }), ctx), ctx);
  assert.deepEqual(labels(items), ["Open", "Rename…", "Archive", "Move left", "Move right", "-", "New agent tab", "New shell tab", "Resume…"]);
  assert.equal(items.find((i) => i.id === "left").disabled, true);
  for (const id of ["open", "rename", "archive", "right"]) items.find((i) => i.id === id).run();
  assert.deepEqual(calls, [["select", "a"], ["rename", "a"], ["archive", "a"], ["step", "a", 1]]);
});

test("the Solo tab offers no goal edits and, being open, no Open", () => {
  const ctx = tabCtx([]);
  const items = resolve(MENUS.tab(el({ "data-tab": "solo" }), ctx), ctx);
  assert.deepEqual(labels(items), ["New agent tab", "New shell tab", "Resume…"]);
});

test("an archived goal can be shown again, and an unknown tab has no menu", () => {
  const calls = [];
  const ctx = tabCtx(calls);
  const items = resolve(MENUS.tab(el({ "data-tab": "z", "data-archived": "1" }), ctx), ctx);
  assert.deepEqual(labels(items), ["Show this goal"]);
  items[0].run();
  assert.deepEqual(calls, [["restore", "z"]]);
  assert.deepEqual(MENUS.tab(el({ "data-tab": "gone" }), ctx), []);
});

test("a rail project switches, copies its path and opens the project list; the open one offers worktrees", () => {
  const calls = [];
  const ctx = {
    keys: KEYS, implemented: ["worktrees", "history"], act: (id) => calls.push(["act", id]), selected: "/p/one",
    selectProject: (r) => calls.push(["select", r]), copy: (t, w) => calls.push(["copy", t, w]), nav: { projects: () => calls.push(["projects"]) },
  };
  const other = resolve(MENUS.rail(el({ "data-root": "/p/two" }), ctx), ctx);
  assert.deepEqual(labels(other), ["Switch to this project", "-", "Copy path", "Manage projects…"]);
  other.filter((i) => !i.separator).forEach((i) => i.run());
  assert.deepEqual(calls, [["select", "/p/two"], ["copy", "/p/two", "the project path"], ["projects"]]);
  const here = resolve(MENUS.rail(el({ "data-root": "/p/one" }), ctx), ctx);
  assert.deepEqual(labels(here), ["Worktrees", "Resume…", "-", "Copy path", "Manage projects…"]);
});

test("a worktree row opens an agent or shell, removes, copies; a gone row prunes; the main one cannot be removed", () => {
  const calls = [];
  const rows = {
    "/w/x": { path: "/w/x", branch: "feat/x" },
    "/w/gone": { path: "/w/gone", prunable: true },
    "/w/main": { path: "/w/main", main: true, branch: "main" },
  };
  const ctx = {
    copy: (t, w) => calls.push(["copy", t, w]),
    nav: { worktree: { find: (p) => rows[p], here: (wt, k) => calls.push(["here", wt.path, k]), remove: (wt) => calls.push(["remove", wt.path]), prune: (wt) => calls.push(["prune", wt.path]), busy: false } },
  };
  const menu = (p) => resolve(MENUS.worktree(el({ "data-path": p }), ctx), ctx);
  assert.deepEqual(labels(menu("/w/x")), ["Open an agent here", "Open a shell here", "-", "Copy path", "Copy branch name", "-", "Remove…"]);
  menu("/w/x").filter((i) => !i.separator).forEach((i) => i.run());
  assert.deepEqual(calls, [["here", "/w/x", "agent"], ["here", "/w/x", "shell"], ["copy", "/w/x", "the path"], ["copy", "feat/x", "the branch name"], ["remove", "/w/x"]]);
  assert.deepEqual(labels(menu("/w/gone")), ["Prune", "-", "Copy path", "-", "Remove…"]);
  assert.equal(menu("/w/x").at(-1).danger, true);
  assert.equal(labels(menu("/w/main")).includes("Remove…"), false);
  assert.deepEqual(MENUS.worktree(el({ "data-path": "/w/none" }), ctx), []);
  assert.deepEqual(MENUS.worktree(el({ "data-path": "/w/x" }), {}), []);
});

test("a history row resumes and copies; one that is already open cannot be resumed", () => {
  const calls = [];
  const list = { c1: { id: "c1", summary: "Fix the thing" }, c2: { id: "c2", summary: "Open one", open: true } };
  const ctx = { copy: (t, w) => calls.push(["copy", t, w]), nav: { history: { find: (id) => list[id], resume: (c) => calls.push(["resume", c.id]) } } };
  const menu = (id) => resolve(MENUS.history(el({ "data-conversation": id }), ctx), ctx);
  assert.deepEqual(labels(menu("c1")), ["Resume", "Copy conversation id", "Copy summary"]);
  menu("c1").forEach((i) => i.run());
  assert.deepEqual(calls, [["resume", "c1"], ["copy", "c1", "the conversation id"], ["copy", "Fix the thing", "the summary"]]);
  assert.deepEqual(labels(menu("c2")), ["Copy conversation id", "Copy summary"]);
});

test("the blank menu lists the palette, new tabs and settings that this window runs", () => {
  const ctx = { keys: KEYS, implemented: ["newAgentTab", "newShellTab"], act() {} };
  assert.deepEqual(labels(resolve(MENUS.blank(el({}), ctx), ctx)), ["New agent tab", "New shell tab"]);
});
