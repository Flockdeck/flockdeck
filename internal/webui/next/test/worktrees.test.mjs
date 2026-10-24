// C7, worktrees: the dialog that lists the checkouts agents work in and makes and
// removes them, the conflict radar's switch, and the start flow's worktree option
// (that one is in lifecycle.test.mjs). The pure parts first, then the store, then
// the page on a fake control socket.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import {
  BRANCH_KEPT_WORDS, addCommand, changesWords, listCommand, membersOf, openHereCommand, pruneCommand, removalOf, removeCommand, sharedOf, sharedWords, worktreesOf,
} from "../assets/lib/worktrees.js";
import { prefsOf, setting } from "../assets/lib/prefs.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const MARKUP = '<img src=x onerror="alert(1)">';

const wt = (over = {}) => ({
  path: "/repo-feat", label: "feat", branch: "feat/login", head: "abc1234", upstream: "origin/feat/login", main: false, locked: false, detached: false, prunable: false,
  dirty: 0, untracked: 0, ahead: 0, behind: 0, panes: 0, ...over,
});
const listing = (over = {}) => ({
  type: "worktrees", root: "/repo", defaultBase: "main",
  items: [wt({ path: "/repo", label: "repo", branch: "main", main: true, panes: 1 }), wt()],
  branches: [{ name: "main", current: true, checkedIn: "/repo" }, { name: "feat/login", checkedIn: "/repo-feat" }, { name: "spare" }], ...over,
});

// ---------------------------------------------------------------- pure parts

test("worktrees: a listing is read defensively", () => {
  const w = worktreesOf(listing({ items: [wt(), null, { label: "no path" }, wt({ path: "/b", dirty: -1, panes: "2", label: "" })], branches: [{ name: "" }, { name: "x", repo: 4 }] }));
  assert.deepEqual(w.items.map((x) => x.path), ["/repo-feat", "/b"]);
  assert.equal(w.items[1].dirty, 0);
  assert.equal(w.items[1].panes, 0);
  assert.equal(w.items[1].label, "/b", "a worktree with no label is called by its path");
  assert.deepEqual(w.branches.map((b) => b.name), ["x"]);
  assert.equal(worktreesOf(null).items.length, 0);
  assert.deepEqual(worktreesOf({ error: "not a repository", repos: [{ path: "/p/a", name: "a" }, { name: "x" }] }).repos, [{ path: "/p/a", name: "a" }]);
});

test("worktrees: a project with more than one repository is told apart, and a shared checkout is warned about", () => {
  const w = worktreesOf(listing({ items: [wt({ repo: "api", repoRoot: "/api" }), wt({ path: "/web-x", repo: "web", repoRoot: "/web" })], branches: [] }));
  assert.deepEqual(membersOf(w), [{ name: "api", root: "/api" }, { name: "web", root: "/web" }]);
  assert.deepEqual(membersOf(worktreesOf(listing())), []);
  const shared = sharedOf(worktreesOf(listing({ items: [wt({ panes: 2 }), wt({ path: "/o", panes: 1 }), wt({ path: "/g", panes: 3, prunable: true })] })).items);
  assert.deepEqual(shared.map((x) => x.path), ["/repo-feat"]);
  assert.match(sharedWords(shared[0]), /2 agents are working in feat, so they can overwrite each other's files/);
  assert.equal(changesWords(wt({ dirty: 3, untracked: 1 })), "3 changed files and 1 new file");
  assert.equal(changesWords(wt({ dirty: 1 })), "1 changed file");
  assert.equal(changesWords(wt()), "");
});

test("worktrees: removing always asks, names what would be lost, keeps the branch, and is refused where it cannot be done", () => {
  const clean = removalOf(wt());
  assert.equal(clean.title, "Remove feat?");
  assert.ok(clean.words.some((l) => l.includes("/repo-feat")));
  assert.ok(clean.words.some((l) => l.includes("The branch feat/login is kept")));
  assert.equal(clean.label, "Remove it");
  const dirty = removalOf(wt({ dirty: 3, untracked: 1, ahead: 2 }));
  assert.ok(dirty.words.some((l) => /3 changed files and 1 new file that are not committed. Removing it discards them for good/.test(l)));
  assert.ok(dirty.words.some((l) => /kept, with its 2 commits not yet pushed/.test(l)));
  assert.equal(dirty.label, "Remove and discard");
  assert.match(removalOf(wt({ main: true })).refuse, /main checkout/);
  assert.match(removalOf(wt({ locked: true })).refuse, /is locked/);
  assert.match(removalOf(wt({ panes: 1 })).refuse, /An agent is working in feat. Close that pane first/);
  assert.match(removalOf(wt({ panes: 3 })).refuse, /3 agents are working in feat. Close those panes first/);
  assert.equal(removalOf(wt({ branch: "" })).words.at(-1), BRANCH_KEPT_WORDS);
});

test("worktrees: the commands carry the repository and force only for uncommitted work, and are not made when refused", () => {
  assert.deepEqual(listCommand(), { cmd: "worktrees" });
  assert.deepEqual(listCommand("/p/a"), { cmd: "worktrees", root: "/p/a" });
  assert.deepEqual(addCommand(" fix-auth ", " main ", "/api"), { cmd: "worktreeAdd", text: "fix-auth", base: "main", root: "/api" });
  assert.deepEqual(addCommand("fix", "", ""), { cmd: "worktreeAdd", text: "fix" });
  assert.equal(addCommand("  "), null);
  assert.deepEqual(removeCommand(wt()), { cmd: "worktreeRemove", path: "/repo-feat", force: false });
  assert.deepEqual(removeCommand(wt({ untracked: 2 }), "/api"), { cmd: "worktreeRemove", path: "/repo-feat", force: true, root: "/api" });
  assert.equal(removeCommand(wt({ panes: 1 })), null);
  assert.equal(removeCommand(wt({ main: true })), null);
  assert.deepEqual(pruneCommand(), { cmd: "worktreePrune" });
  assert.deepEqual(pruneCommand("/api"), { cmd: "worktreePrune", root: "/api" });
  assert.deepEqual(openHereCommand(wt(), "shell"), { cmd: "newTab", kind: "shell", path: "/repo-feat", text: "feat" });
  assert.equal(openHereCommand(wt({ prunable: true }), "agent"), null, "there is no folder to open");
});

test("conflict radar: a setting sends its switch", () => {
  assert.equal(prefsOf({ conflictRadar: true }).conflictRadar, true);
  assert.equal(prefsOf({ conflictRadar: "on" }).conflictRadar, false);
  assert.deepEqual(setting("conflictRadar", true).command, { cmd: "conflictRadar", kind: "on" });
  assert.deepEqual(setting("conflictRadar", false).patch, { conflictRadar: false });
});

// ---------------------------------------------------------------- the store

test("the store keeps the listing and the radar's answer", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({}));
  ws.frame(listing());
  ws.frame(listing({ root: "/other" }));
  assert.deepEqual([store.getState().worktrees.root, store.getState().worktrees.seq], ["/other", 2]);
  ws.frame({ type: "radarSupport", unavailable: "git is older than 2.38" });
  assert.equal(store.getState().radar.unavailable, "git is older than 2.38");
  ws.frame({ type: "radarSupport" });
  assert.equal(store.getState().radar.unavailable, "");
  ws.frame(hello({}));
  assert.equal(store.getState().radar, null, "a new hello carries its own answer");
  store.stop();
});

// ---------------------------------------------------------------- the page

const KEYS = [
  { id: "worktrees", keys: "Ctrl+Shift+G", label: "Worktrees", section: "Git" },
  { id: "settings", keys: "Ctrl+,", label: "Settings", section: "The window" },
];
const graph = () => ({ ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" });
const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0, active: true }];
const stateOf = (g) => ({
  type: "state", root: "/work/a", projects, agents: { items: [], roles: [], default: {} }, activeTab: "", tabs: [],
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured", status: "working" }])),
});

async function mount({ helloOver = {} } = {}) {
  PtySocket.reset();
  FakeTerm.reset();
  FakeObserver.instances.length = 0;
  const dom = createDom();
  const restore = install(dom);
  const { store } = await harness();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  const terminalOptions = {
    Terminal: FakeTerm, FitAddon: FakeFit, ResizeObserver: FakeObserver, WebSocket: PtySocket,
    location: { href: "http://127.0.0.1:5000/", pathname: "/", protocol: "http:" }, isFocused: () => false,
  };
  render(html`<${Live} store=${store} window=${dom.window} terminalOptions=${terminalOptions} />`, container);
  await settle();
  const ws = () => FakeWebSocket.last;
  const g = graph();
  const m = {
    dom, store, container, ws, g,
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    push: async (msg) => { ws().frame(msg); await settle(); },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    type: async (sel, value) => {
      const el = m.container.find(sel);
      assert.ok(el, "no field " + sel);
      el.value = value;
      el.dispatch("input");
      await settle();
    },
    key: async (spec) => {
      const parts = spec.split("+");
      dom.window.dispatch("keydown", { key: parts[parts.length - 1], ctrlKey: parts.includes("Ctrl"), shiftKey: parts.includes("Shift"), altKey: false });
      await settle();
    },
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys: KEYS, ...helloOver }));
      await settle();
      ws().frame(stateOf(g));
      await settle();
      if (!helloOver.remote) {
        ws().frame(g);
        await settle();
      }
    },
    byText: (sel, re) => container.findAll(sel).find((b) => re.test(b.textContent)),
    text: () => (container.find(".dialog") ? container.find(".dialog").textContent : ""),
    done: () => { render(null, container); store.stop(); restore(); },
  };
  return m;
}

async function withPage(opts, fn) {
  const m = await mount(opts);
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

const rowFor = (m, path) => m.container.findAll(".wt-row").find((r) => r.attributes["data-path"] === path);
const inRow = (row, sel) => row.findAll(sel)[0];
const isOff = (el) => !!el.disabled || el.attributes.disabled !== undefined;
const closeBtn = (m) => m.container.findAll(".dialog-btn").find((b) => b.textContent === "Close");

test("Worktrees: the key opens it at once and asks, the reason leads, and the listing is drawn as text", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    assert.deepEqual(m.sent("worktrees"), [{ cmd: "worktrees" }]);
    assert.ok(m.container.find("#wt-loading"));
    assert.match(m.container.find("#wt-why").textContent, /Each agent works in its own, on its own branch, so two agents never edit the same files/);
    await m.push(listing({ items: [wt({ path: MARKUP, label: MARKUP, branch: MARKUP, dirty: 2, ahead: 1, behind: 3 }), wt({ path: "/repo", label: "repo", main: true, panes: 1 })], branches: [{ name: MARKUP }] }));
    assert.equal(m.container.find("#wt-loading"), null);
    assert.equal(m.container.findAll("img").length, 0);
    assert.ok(m.text().includes(MARKUP));
    const row = m.container.findAll(".wt-row")[0];
    assert.match(row.textContent, /2 changed files, not committed/);
    assert.match(row.textContent, /↑1/);
    assert.match(row.textContent, /↓3/);
    assert.match(m.container.findAll(".wt-row")[1].textContent, /main.*1 agent/);
    assert.equal(m.container.findAll(".wt-remove").length, 1, "the main checkout has no Remove");
  }));

test("Worktrees: two agents in one checkout show the conflict warning", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    await m.push(listing({ items: [wt({ panes: 2 })] }));
    assert.match(m.container.find(".wt-conflict").textContent, /2 agents are working in feat, so they can overwrite each other's files/);
    await m.push(listing({ items: [wt({ panes: 1 })] }));
    assert.equal(m.container.find(".wt-conflict"), null);
  }));

test("Worktrees: Open agent and Open shell open a tab in that worktree and close the dialog", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    await m.push(listing());
    m.clear();
    await m.click(inRow(rowFor(m, "/repo-feat"), ".wt-agent"));
    assert.deepEqual(m.sent("newTab"), [{ cmd: "newTab", kind: "agent", path: "/repo-feat", text: "feat" }]);
    assert.equal(m.container.find("#worktrees-title"), null);
    await m.key("Ctrl+Shift+G");
    await m.push(listing());
    m.clear();
    await m.click(inRow(rowFor(m, "/repo-feat"), ".wt-shell"));
    assert.deepEqual(m.sent("newTab"), [{ cmd: "newTab", kind: "shell", path: "/repo-feat", text: "feat" }]);
  }));

test("Worktrees: removing asks first, can be backed out of, and a clean one is sent without force", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    await m.push(listing());
    m.clear();
    await m.click(inRow(rowFor(m, "/repo-feat"), ".wt-remove"));
    assert.deepEqual(m.sent("worktreeRemove"), [], "nothing is sent yet");
    const q = m.container.find(".confirm");
    assert.ok(q);
    assert.match(q.textContent, /Remove feat\?/);
    assert.match(q.textContent, /The branch feat\/login is kept/);
    await m.click(m.byText(".dialog-btn", /^Cancel$/));
    assert.equal(m.container.find(".confirm"), null);
    assert.deepEqual(m.sent("worktreeRemove"), []);
    await m.click(inRow(rowFor(m, "/repo-feat"), ".wt-remove"));
    await m.click(m.byText(".dialog-btn--danger", /^Remove it$/));
    assert.deepEqual(m.sent("worktreeRemove"), [{ cmd: "worktreeRemove", path: "/repo-feat", force: false }]);
    // Busy until the desktop answers with the listing.
    assert.equal(isOff(inRow(rowFor(m, "/repo-feat"), ".wt-remove")), true);
    assert.equal(isOff(m.container.find("#wt-create")), true);
    await m.push(listing({ items: [wt({ path: "/repo", label: "repo", branch: "main", main: true })] }));
    assert.equal(rowFor(m, "/repo-feat"), undefined);
    assert.equal(isOff(m.container.find("#wt-create")), false);
  }));

test("Worktrees: a worktree with uncommitted work names it and is sent with force only after the question", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    await m.push(listing({ items: [wt({ dirty: 3, untracked: 1 })] }));
    m.clear();
    await m.click(m.container.find(".wt-remove"));
    assert.match(m.container.find(".confirm").textContent, /3 changed files and 1 new file that are not committed\. Removing it discards them for good/);
    assert.deepEqual(m.sent("worktreeRemove"), []);
    await m.click(m.byText(".dialog-btn--danger", /^Remove and discard$/));
    assert.deepEqual(m.sent("worktreeRemove"), [{ cmd: "worktreeRemove", path: "/repo-feat", force: true }]);
  }));

test("Worktrees: one an agent is working in, or a locked one, is not sent and the person is told why", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    await m.push(listing({ items: [wt({ panes: 1 }), wt({ path: "/lk", label: "lk", locked: true })] }));
    m.clear();
    await m.click(inRow(rowFor(m, "/repo-feat"), ".wt-remove"));
    assert.match(m.container.find("#wt-problem").textContent, /An agent is working in feat\. Close that pane first, then remove it\./);
    assert.equal(m.container.find(".confirm"), null);
    await m.click(inRow(rowFor(m, "/lk"), ".wt-remove"));
    assert.match(m.container.find("#wt-problem").textContent, /lk is locked/);
    assert.deepEqual(m.sent("worktreeRemove"), []);
  }));

test("Worktrees: a new worktree needs a branch, starts from the base, and a free branch checks out", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    await m.push(listing());
    m.clear();
    await m.click(m.container.find("#wt-create"));
    assert.match(m.container.find("#wt-problem").textContent, /Name the branch for the new worktree first/);
    assert.deepEqual(m.sent("worktreeAdd"), []);
    await m.type("#wt-branch", "  fix-auth ");
    await m.click(m.container.find("#wt-create"));
    assert.deepEqual(m.sent("worktreeAdd"), [{ cmd: "worktreeAdd", text: "fix-auth", base: "main" }]);
    await m.push(listing());
    m.clear();
    await m.type("#wt-base", "develop");
    await m.type("#wt-branch", "other");
    m.container.find("#wt-branch").keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("worktreeAdd"), [{ cmd: "worktreeAdd", text: "other", base: "develop" }]);
    await m.push(listing());
    m.clear();
    const free = m.container.findAll(".wt-checkout");
    assert.deepEqual(free.map((b) => b.textContent), ["spare"], "branches already in a worktree are not offered");
    await m.click(free[0]);
    assert.deepEqual(m.sent("worktreeAdd"), [{ cmd: "worktreeAdd", text: "spare" }]);
  }));

test("Worktrees: a project of more than one repository says which one a new worktree is made in", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    await m.push(listing({
      root: "/api", items: [wt({ path: "/api", label: "api", main: true, repo: "api", repoRoot: "/api" }), wt({ path: "/web", label: "web", main: true, repo: "web", repoRoot: "/web" })],
      branches: [{ name: "a1", repo: "api", repoRoot: "/api" }, { name: "w1", repo: "web", repoRoot: "/web" }],
    }));
    assert.deepEqual(m.container.findAll(".wt-checkout").map((b) => b.textContent), ["a1"], "the branches of the repository chosen");
    await m.click(m.byText(".wt-member", /^web$/));
    assert.deepEqual(m.container.findAll(".wt-checkout").map((b) => b.textContent), ["w1"]);
    await m.type("#wt-branch", "x");
    m.clear();
    await m.click(m.container.find("#wt-create"));
    assert.equal(m.sent("worktreeAdd")[0].root, "/web");
  }));

test("Worktrees: folders that are gone are pruned, and a project that is not a repository offers the ones inside it", async () => {
  await withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    await m.push(listing({ items: [wt({ prunable: true })] }));
    assert.equal(m.container.findAll(".wt-agent").length, 0, "nothing can be opened in a folder that is gone");
    m.clear();
    await m.click(m.container.find("#wt-prune-all"));
    assert.deepEqual(m.sent("worktreePrune"), [{ cmd: "worktreePrune" }]);
    await m.push(listing({ items: [wt({ prunable: true })] }));
    m.clear();
    await m.click(inRow(rowFor(m, "/repo-feat"), ".wt-prune-row"));
    assert.deepEqual(m.sent("worktreePrune"), [{ cmd: "worktreePrune" }]);
  });
  await withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    await m.push({ type: "worktrees", root: "/p", error: "p is not a git repository", items: [], branches: [], repos: [{ path: "/p/a", name: "a" }, { path: "/p/b", name: "b" }] });
    assert.match(m.container.find("#wt-error").textContent, /p is not a git repository/);
    m.clear();
    await m.click(m.byText(".wt-repo", /^b$/));
    assert.deepEqual(m.sent("worktrees"), [{ cmd: "worktrees", root: "/p/b" }]);
  });
});

test("Worktrees: a relay window may use it, as the desktop allows, and the listing is asked for", () =>
  withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+G");
    assert.deepEqual(m.sent("worktrees"), [{ cmd: "worktrees" }]);
    await m.push(listing());
    m.clear();
    await m.click(inRow(rowFor(m, "/repo-feat"), ".wt-remove"));
    assert.ok(m.container.find(".confirm"), "it still asks first");
    assert.deepEqual(m.sent("worktreeRemove"), []);
    await m.click(closeBtn(m));
  }));

test("Settings: the conflict radar switch sends its setting, and says why it is dead where git cannot", async () => {
  await withPage({}, async (m) => {
    await m.connect();
    await m.click(m.container.find(".rail-settings"));
    const sw = () => m.container.find("#set-radar-switch");
    assert.match(m.container.find("#set-radar-about").textContent, /Off by default\. On, about every 15 seconds/);
    assert.match(m.container.find("#set-radar-about").textContent, /sent to every open window, including a paired phone or relay window/);
    assert.equal(sw().attributes["aria-checked"], "false");
    m.clear();
    await m.click(sw());
    assert.deepEqual(m.sent("conflictRadar"), [{ cmd: "conflictRadar", kind: "on" }]);
    assert.equal(sw().attributes["aria-checked"], "true");
  });
  await withPage({ helloOver: { radarUnavailable: "git is older than 2.38" } }, async (m) => {
    await m.connect();
    await m.click(m.container.find(".rail-settings"));
    assert.equal(isOff(m.container.find("#set-radar-switch")), true);
    assert.match(m.container.find("#set-radar-about").textContent, /Not available on this machine: git is older than 2\.38\./);
  });
  await withPage({ helloOver: { radarChecking: true } }, async (m) => {
    await m.connect();
    await m.click(m.container.find(".rail-settings"));
    assert.match(m.container.find("#set-radar-about").textContent, /Checking the version of git\./);
    await m.push({ type: "radarSupport" });
    assert.equal(isOff(m.container.find("#set-radar-switch")), false, "settled when the desktop has read the version");
  });
});
