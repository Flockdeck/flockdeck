// L6: helper apps, baton and projects, through the real page on a fake control
// socket. The pure parts (what is read, what is sent) are tested first, and the
// dialogs after. Everything the machine sends (names, paths, log lines, the text
// of a baton) is drawn as text, and a window reached through the relay gets none
// of the dialogs that act on the machine.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live, OPEN_AN_AGENT_WORDS } from "../assets/shell/Live.js";
import { approvalOf, LOST_APPROVAL_WORDS, MAX_APPROVALS } from "../assets/lib/store.js";
import * as H from "../assets/lib/helpers.js";
import * as B from "../assets/lib/baton.js";
import * as P from "../assets/lib/projects.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const CART = "6fb40971";

// ---------------------------------------------------------------- helpers, pure

test("helper rows are read as text, and a row without an id is dropped", () => {
  const rows = H.rowsOf({ rows: [{ id: "a", name: 4, summary: "s", log: ["x", 3], allows: "no", hasData: "yes" }, { name: "x" }, null] });
  assert.equal(rows.length, 1);
  assert.equal(rows[0].name, "a", "a name that is not text falls back to the id");
  assert.deepEqual(rows[0].log, ["x"]);
  assert.deepEqual(rows[0].allows, []);
  assert.equal(rows[0].hasData, false);
  assert.equal(rows[0].state, "notinstalled");
  assert.equal(H.rowsOf(null), null);
  assert.equal(H.rowsOf({ rows: "x" }), null);
});

test("each state offers its own buttons", () => {
  const verbs = (row) => H.buttonsFor(row).map((b) => b.verb);
  assert.deepEqual(verbs({ state: "notinstalled", hasData: false }), ["install"]);
  assert.deepEqual(verbs({ state: "notinstalled", hasData: true }), ["install", "delete"]);
  assert.deepEqual(verbs({ state: "stopped", update: "", needsRepair: false }), ["start", "remove"]);
  assert.deepEqual(verbs({ state: "failed", update: "2", needsRepair: true }), ["start", "update", "repair", "remove"]);
  assert.deepEqual(verbs({ state: "running" }), ["open", "stop"]);
  assert.deepEqual(verbs({ state: "starting" }), ["stop"]);
  assert.deepEqual(verbs({ state: "unresponsive" }), ["stop"]);
  assert.deepEqual(verbs({ state: "installing" }), []);
  assert.deepEqual(verbs({ state: "stopping" }), []);
  assert.equal(H.buttonsFor({ state: "failed" })[0].label, "Try again");
});

test("a state change is announced once, and the first sight of a helper is not", () => {
  const heard = new Map();
  const row = (state, error = "") => ({ id: "x", name: "Fooer", state, error });
  assert.equal(H.announcement([row("stopped")], heard), "");
  assert.equal(H.announcement([row("stopped")], heard), "");
  assert.equal(H.announcement([row("running")], heard), "Fooer is running");
  assert.equal(H.announcement([row("failed", "boom")], heard), "Fooer is failed: boom");
});

test("an install carries the version and hash that were shown", () => {
  const plan = H.planOf({ id: "h", version: "1.2.3", sha256: "abc", name: "H", allows: ["x", 4] });
  assert.deepEqual(H.installCommand(plan), { cmd: "helperInstall", id: "h", text: "1.2.3", sha256: "abc", confirmed: true });
  assert.deepEqual(plan.allows, ["x"]);
  assert.equal(H.planOf({ nope: 1 }), null);
  assert.deepEqual(H.uninstallCommand("h", 1), { cmd: "helperUninstall", id: "h", purge: true });
  assert.deepEqual(H.uninstallCommand("h"), { cmd: "helperUninstall", id: "h", purge: false });
  assert.deepEqual(H.planCommand("h", "2"), { cmd: "helperPlan", id: "h", text: "2" });
  assert.deepEqual(H.listCommand(true), { cmd: "helpers", force: true });
  assert.deepEqual(H.listCommand(false), { cmd: "helpers" });
});

// ---------------------------------------------------------------- baton, pure

const DRAFT = {
  type: "batonDraft", paneId: CART, req: "b1", name: "cart", agent: "claude", model: "opus", provider: "Anthropic", text: "# Baton\n\nDo the thing. [REDACTED: aws-key]",
  notes: ["Some notes"], dirty: true, isRepo: true, markKinds: ["aws-key"],
  agents: [
    { id: "claude", name: "Claude Code", provider: "Anthropic", models: [{ id: "opus", name: "Opus" }, { id: "sonnet", name: "Sonnet" }], default: "opus" },
    { id: "codex", name: "Codex", provider: "OpenAI", models: [] },
    { id: "mystery", name: "Mystery", provider: "", models: [] },
    { id: "gone", name: "Gone", provider: "X", models: [], unavailable: "not installed" },
  ],
};

test("a draft is read as text, flags and lists", () => {
  const d = B.draftOf({ ...DRAFT, name: 4, dirty: "yes", notes: ["a", 3], agents: [null, { id: "" }, { id: "a", name: 3, models: [{ id: "m" }, 5] }] });
  assert.equal(d.name, "");
  assert.equal(d.dirty, false);
  assert.deepEqual(d.notes, ["a"]);
  assert.equal(d.agents.length, 1);
  assert.equal(d.agents[0].name, "a");
  assert.deepEqual(d.agents[0].models, [{ id: "m", name: "" }]);
  assert.equal(B.draftOf(null), null);
  assert.equal(B.draftOf({ text: "x" }), null);
});

test("the marks are counted with the draft's own kinds", () => {
  assert.equal(B.marksIn("a [REDACTED: aws-key] b [REDACTED: aws-key] [REDACTED: other]", ["aws-key"]), 2);
  assert.equal(B.marksIn("[REDACTED: whatever-1]", []), 1);
  assert.equal(B.marksIn("no marks", ["a.b"]), 0);
  assert.equal(B.marksIn("[REDACTED: axb]", ["a.b"]), 0, "a kind is matched as written, not as a pattern");
  assert.equal(B.marksWords(1), "1 secret removed");
  assert.equal(B.marksWords(3), "3 secrets removed");
});

test("another company, or one not known, needs a tick", () => {
  const d = B.draftOf(DRAFT);
  assert.equal(B.providerCheck(d, "claude").differs, false);
  assert.equal(B.providerCheck(d, "").differs, false);
  const codex = B.providerCheck(d, "codex");
  assert.deepEqual([codex.differs, codex.unknown, codex.there], [true, false, "OpenAI"]);
  assert.match(B.providerWords(codex), /to OpenAI, not Anthropic/);
  const mystery = B.providerCheck(d, "mystery");
  assert.deepEqual([mystery.differs, mystery.unknown], [true, true]);
  assert.match(B.providerWords(mystery), /whose company Flockdeck does not know/);
  // The pane's own company not being known makes any other agent a stranger.
  assert.equal(B.providerCheck({ ...d, provider: "" }, "codex").unknown, true);
  // The same agent is never a change, whatever is known of it.
  assert.equal(B.providerCheck({ ...d, provider: "" }, "claude").differs, false);
});

test("a start says what stands in its way", () => {
  const ok = { differs: false };
  assert.match(B.startProblem({ body: "  ", worktree: false, branch: "", check: ok, ack: false }), /empty/);
  assert.match(B.startProblem({ body: "x", worktree: true, branch: " ", check: ok, ack: false }), /branch/);
  assert.match(B.startProblem({ body: "x", worktree: false, branch: "", check: { differs: true }, ack: false }), /Send it there/);
  assert.equal(B.startProblem({ body: "x", worktree: true, branch: "b", check: { differs: true }, ack: true }), "");
});

test("the answer to a command is said with the baton's id and what was removed", () => {
  assert.equal(B.savedWords({ cmd: "saveBaton", id: "b-1", scrubbed: 0 }), "Saved baton b-1.");
  assert.equal(B.savedWords({ cmd: "startFromBaton", id: "b-1", scrubbed: 1 }), "Started a new pane from baton b-1 (1 secret removed).");
  assert.equal(B.savedWords({ cmd: "restartWithBaton", id: "b-1", scrubbed: 2 }), "Restarted this pane from baton b-1 (2 secrets removed).");
});

test("a command stays out until it is answered", () => {
  const f = B.createInflight();
  const r = f.next();
  f.add(r, "startFromBaton", "p1");
  assert.equal(f.same("startFromBaton", "p1"), true);
  assert.equal(f.same("startFromBaton", "p2"), false);
  assert.equal(f.same("saveBaton", "p1"), false);
  assert.notEqual(f.next(), r);
  f.delete(r);
  assert.equal(f.same("startFromBaton", "p1"), false);
  f.add("x", "saveBaton", "p1");
  f.clear();
  assert.equal(f.size(), 0);
});

test("the commands carry the text whole, the trimmed task and the choices", () => {
  const d = B.draftOf(DRAFT);
  assert.deepEqual(B.saveCommand(d, "r", " body ", " task "), { cmd: "saveBaton", id: CART, req: "r", text: " body ", task: "task" });
  assert.equal(B.restartCommand(d, "r", "b", "").cmd, "restartWithBaton");
  assert.deepEqual(B.startCommand(d, "r", "b", "t", { agent: "codex", model: "", beside: 1, branch: " fix ", confirmed: true }),
    { cmd: "startFromBaton", id: CART, req: "r", text: "b", task: "t", agent: "codex", model: "", split: true, branch: "fix", confirmed: true });
  assert.deepEqual(B.makeCommand(CART, "q"), { cmd: "makeBaton", id: CART, req: "q" });
});

test("the picker offers the agents of the draft and starts on the pane's own", () => {
  const d = B.draftOf(DRAFT);
  const { options, choosable } = B.choices(d);
  assert.equal(choosable, true);
  assert.equal(options.find((o) => o.value === B.startValue(d)).label, "Opus");
  assert.equal(options.find((o) => o.value.startsWith("gone")).disabled, true);
  const one = B.choices(B.draftOf({ ...DRAFT, agents: [DRAFT.agents[1]] }));
  assert.equal(one.choosable, false);
  assert.deepEqual(B.pickOf(d, "", true), { agent: "claude", model: "opus" });
  assert.deepEqual(B.pickOf(d, "codex\n", true), { agent: "codex", model: "" });
  assert.deepEqual(B.pickOf(d, "codex\n", false), { agent: "claude", model: "opus" });
});

// ---------------------------------------------------------------- projects, pure

test("open projects are read from the state, each with at least itself as a member", () => {
  const open = P.openOf({ projects: [{ root: "/a", name: "alpha", tabs: 2, waiting: 1, active: true }, { root: "", name: "x" }, null, { root: "/g", name: "G", members: [{ root: "/g1", name: "one" }, { root: "" }] }] });
  assert.equal(open.length, 2);
  assert.deepEqual(open[0].members, [{ root: "/a", name: "alpha" }]);
  assert.equal(open[0].waiting, 1);
  assert.equal(open[0].working, 0);
  assert.deepEqual(open[1].members, [{ root: "/g1", name: "one" }]);
  assert.deepEqual(P.openOf(null), []);
});

test("recent projects exclude the open ones and split off the archived", () => {
  const r = P.recentsOf({ items: [{ root: "/a", name: "a", open: true }, { root: "/b", name: "b", exists: false }, { root: "/c", archived: true }, { name: "no root" }] });
  assert.deepEqual(r.recent.map((x) => [x.root, x.exists]), [["/b", false]]);
  assert.deepEqual(r.archived.map((x) => x.root), ["/c"]);
  assert.equal(P.recentsOf(null), null);
  assert.equal(P.recentsOf({ items: 3 }), null);
});

test("only a full path can open a project", () => {
  for (const ok of ["/home/me/x", "C:\\work\\x", "c:/work/x", "\\\\host\\share\\x", '"/quoted/path"']) assert.equal(P.isAbsolute(ok), true, ok);
  for (const bad of ["", "x", "./x", "~/x", "..\\x", null]) assert.equal(P.isAbsolute(bad), false, String(bad));
});

test("moving a project sends the whole new order, and nothing at the ends", () => {
  const list = [{ root: "/a" }, { root: "/b" }, { root: "/c" }];
  assert.deepEqual(P.moveCommand(list, "/b", -1), { cmd: "reorderProjects", roots: ["/b", "/a", "/c"] });
  assert.deepEqual(P.moveCommand(list, "/b", 1), { cmd: "reorderProjects", roots: ["/a", "/c", "/b"] });
  assert.equal(P.moveCommand(list, "/a", -1), null);
  assert.equal(P.moveCommand(list, "/c", 1), null);
  assert.equal(P.moveCommand(list, "/zz", 1), null);
});

test("closing a project asks only when something runs in it", () => {
  assert.deepEqual(P.closeWords({ name: "p", panes: 0, working: 0, waiting: 0 }), []);
  assert.equal(P.closeWords({ name: "p", panes: 2, working: 0, waiting: 0 }).length, 1);
  assert.match(P.closeWords({ name: "p", panes: 3, working: 1, waiting: 1 })[1], /2 agents are still working or waiting/);
  assert.match(P.closeWords({ name: "p", panes: 1, working: 1, waiting: 0 })[1], /1 agent is still/);
});

test("project commands name the project by its path, and renaming picks the group or the memory", () => {
  assert.deepEqual(P.renameCommand("/a", " New ", true), { cmd: "renameGroup", root: "/a", text: "New" });
  assert.deepEqual(P.renameCommand("/a", "", false), { cmd: "renameProject", root: "/a", text: "" });
  assert.deepEqual(P.groupCommand(["/a", "/b"], " Both "), { cmd: "groupProjects", roots: ["/a", "/b"], text: "Both" });
  assert.deepEqual(P.addRepoCommand("/a", "/c"), { cmd: "addRepoToGroup", root: "/a", path: "/c" });
  assert.deepEqual(P.archiveCommand("/a", 1), { cmd: "archiveProject", root: "/a", archived: true });
});

// ---------------------------------------------------------------- the page

const KEYS = [
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Window" },
  { id: "helpers", keys: "", label: "Helper apps…", section: "Window" },
  { id: "makeBaton", keys: "", label: "Make baton", section: "Agents" },
];
const PROJECTS = [
  { root: "/work/a", name: "alpha", active: true, tabs: 2, panes: 3, waiting: 1, working: 1, members: [{ root: "/work/a", name: "alpha" }] },
  { root: "/work/b", name: "<img src=x onerror=1>", tabs: 1, panes: 0, members: [{ root: "/work/b", name: "b" }] },
  { root: "/work/g", name: "grouped", tabs: 1, panes: 1, members: [{ root: "/work/g1", name: "g-one" }, { root: "/work/g2", name: "g-two" }] },
];
const graph = () => ({ ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" });
const stateOf = (g, over = {}) => ({
  type: "state", root: "/work/a", projects: PROJECTS, tabs: [], panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name }])), ...over,
});

async function withPage(opts, fn) {
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
  render(html`<${Live} store=${store} window=${dom.window} terminalOptions=${terminalOptions} patchWaitMs=${50} />`, container);
  await settle();
  const g = graph();
  const ws = () => FakeWebSocket.last;
  const m = {
    dom, store, container, ws,
    box: (pane) => container.findAll("[data-pane]").find((e) => e.localName === "g" && e.getAttribute("data-pane") === pane && e.getAttribute("role") === "button"),
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    push: async (msg) => { ws().frame(msg); await settle(); },
    key: async (key, init = {}) => {
      dom.window.dispatch("keydown", { key, ctrlKey: false, shiftKey: false, altKey: false, ...init });
      await settle();
    },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    byText: (sel, re) => container.findAll(sel).find((e) => re.test(e.textContent)),
    fill: async (sel, value) => {
      const el = typeof sel === "string" ? container.find(sel) : sel;
      assert.ok(el, "no " + sel);
      el.value = value;
      el.dispatch("input");
      await settle();
    },
    tick: async (sel, on = true) => {
      const el = container.find(sel);
      assert.ok(el, "no " + sel);
      el.checked = on;
      el.dispatch("change");
      await settle();
    },
    choose: async (sel, value) => {
      const el = container.find(sel);
      el.value = value;
      el.dispatch("change");
      await settle();
    },
    pane: async (pane) => { await m.click(m.box(pane)); assert.ok(m.container.find(".drawer-more"), "no drawer"); },
    palette: async (re) => {
      await m.key("k", { ctrlKey: true, shiftKey: true });
      const row = container.findAll("li").concat(container.findAll("button")).find((e) => re.test(e.textContent) && e.closest && e.closest("li"));
      assert.ok(row, "not in the palette: " + re);
      await m.click(row);
    },
    done: () => { render(null, container); store.stop(); restore(); },
  };
  ws().open();
  ws().frame(hello({ keys: KEYS, remote: !!opts.remote }));
  await settle();
  ws().frame(stateOf(g, opts.state));
  await settle();
  if (!opts.remote) {
    ws().frame(g);
    await settle();
  }
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

const dlg = (m) => m.container.find(".dialog");
const valueIn = (el) => (el.value !== undefined ? el.value : el.attributes.value);
const headOf = (m) => m.dom.document.activeElement.attributes.class;

// ---- helper apps

const ROWS = [
  { id: "viewer", name: "Viewer", summary: "Shows things", state: "notinstalled", allows: [] },
  { id: "runner", name: "<b>Runner</b>", summary: "Runs <script>x</script>", state: "failed", installed: "1.0", update: "1.1", needsRepair: true, error: "it broke", log: ["line <1>", "line 2"], allows: [] },
  { id: "live", name: "Live", summary: "Always on", state: "running", installed: "2.0", url: "http://127.0.0.1:9/", owner: "unverified", allows: [] },
  { id: "kept", name: "Kept", summary: "Has data", state: "notinstalled", hasData: true, dataDir: "/data/kept", allows: [] },
  { id: "idle", name: "Idle", summary: "Stopped", state: "stopped", installed: "3.0", hasData: true, dataDir: "/data/idle", allows: [] },
];
const PLAN = { type: "helperPlan", id: "viewer", name: "Viewer", summary: "Shows things", version: "0.4.0", url: "https://example.test/viewer.tgz", sha256: "ab12", allows: ["read your files", "<i>listen</i>"] };

test("the helper apps dialog asks for the list, draws it as text, and offers each state's buttons", () =>
  withPage({}, async (m) => {
    m.clear();
    await m.click(m.byText(".rail-helpers", /Helper apps/));
    assert.deepEqual(m.sent("helpers"), [{ cmd: "helpers", force: true }]);
    assert.match(dlg(m).textContent, /Loading/);
    await m.push({ type: "helpers", rows: ROWS });
    const d = dlg(m);
    assert.equal(d.findAll(".helper-row").length, 5);
    assert.equal(d.find("b"), null);
    assert.equal(d.find("script"), null);
    assert.match(d.textContent, /<b>Runner<\/b>/);
    assert.match(d.find(".helper-log").textContent, /line <1>\nline 2/);
    assert.ok(d.find("#helper-install-viewer"));
    assert.ok(d.find("#helper-start-runner") && d.find("#helper-repair-runner") && d.find("#helper-update-runner") && d.find("#helper-remove-runner"));
    assert.equal(d.find("#helper-start-runner").textContent, "Try again");
    assert.ok(d.find("#helper-open-live") && d.find("#helper-stop-live"));
    assert.match(d.find("#helper-status-live").textContent, /Running/);
    assert.match(d.textContent, /port owner not verified/);
    assert.match(d.textContent, /not sandboxed/);
    assert.match(d.textContent, /Its data is kept in \/data\/kept/);
    assert.equal(d.find("a"), null, "the address is shown, not linked");
  }));

test("start, stop and open send the desk the helper's id", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-helpers", /Helper apps/));
    await m.push({ type: "helpers", rows: ROWS });
    m.clear();
    await m.click(m.container.find("#helper-start-runner"));
    await m.click(m.container.find("#helper-open-live"));
    await m.click(m.container.find("#helper-stop-live"));
    assert.deepEqual(m.sent("helperStart"), [{ cmd: "helperStart", id: "runner" }]);
    assert.deepEqual(m.sent("helperOpen"), [{ cmd: "helperOpen", id: "live" }]);
    assert.deepEqual(m.sent("helperStop"), [{ cmd: "helperStop", id: "live" }]);
  }));

test("an install shows what it would do first, and sends the version and hash it showed", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-helpers", /Helper apps/));
    await m.push({ type: "helpers", rows: ROWS });
    m.clear();
    await m.click(m.container.find("#helper-install-viewer"));
    assert.deepEqual(m.sent("helperPlan"), [{ cmd: "helperPlan", id: "viewer", text: "" }]);
    assert.ok(m.container.find("#helper-install-viewer").disabled, "the row waits for its answer");
    assert.equal(m.sent("helperInstall").length, 0, "nothing is installed by asking");
    await m.push(PLAN);
    const d = dlg(m);
    assert.match(d.textContent, /Install Viewer 0\.4\.0/);
    assert.match(d.textContent, /https:\/\/example\.test\/viewer\.tgz/);
    assert.match(d.textContent, /ab12/);
    assert.match(d.textContent, /signed with the Flockdeck release key/);
    assert.equal(d.find(".helper-allows").findAll("li").length, 2);
    assert.equal(d.find("i"), null, "what it may do is text");
    assert.match(headOf(m), /helper-head/, "the keyboard starts on the heading, so a reader starts at the top and Enter confirms nothing");
    await m.click(m.container.find("#helper-confirm"));
    assert.deepEqual(m.sent("helperInstall"), [{ cmd: "helperInstall", id: "viewer", text: "0.4.0", sha256: "ab12", confirmed: true }]);
    assert.ok(m.container.find("#helper-install-viewer"), "back at the list");
  }));

test("Cancel on a plan goes back to the list and asks for it again, installing nothing", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-helpers", /Helper apps/));
    await m.push({ type: "helpers", rows: ROWS });
    await m.click(m.container.find("#helper-install-viewer"));
    await m.push(PLAN);
    m.clear();
    await m.click(m.container.find("#helper-cancel"));
    assert.deepEqual(m.sent("helpers"), [{ cmd: "helpers" }]);
    assert.equal(m.sent("helperInstall").length, 0);
    assert.ok(m.container.find("#helper-install-viewer"));
  }));

test("a repair and an update look the release up by the version they mean", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-helpers", /Helper apps/));
    await m.push({ type: "helpers", rows: ROWS });
    m.clear();
    await m.click(m.container.find("#helper-repair-runner"));
    await m.click(m.container.find("#helper-cancel") || m.container.find("#helper-update-runner"));
    assert.equal(m.sent("helperPlan")[0].text, "1.0", "repair asks for the installed version");
    await m.push({ ...PLAN, id: "runner", repair: true, installed: "1.0", version: "1.0" });
    assert.match(dlg(m).textContent, /Repair Viewer 1\.0/);
    assert.match(dlg(m).textContent, /replaced by a checked one/);
  }));

test("a refused plan has only Back, and says why", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-helpers", /Helper apps/));
    await m.push({ type: "helpers", rows: ROWS });
    await m.click(m.container.find("#helper-install-viewer"));
    await m.push({ type: "helperPlan", id: "viewer", error: "The signature is wrong <b>x</b>", fatal: true });
    assert.equal(dlg(m).find("#helper-confirm"), null);
    assert.match(dlg(m).find('[role="alert"]').textContent, /signature is wrong <b>x<\/b>/);
    assert.equal(dlg(m).find("b"), null);
    await m.click(dlg(m).find("#helper-back"));
    assert.ok(m.container.find("#helper-install-viewer"));
  }));

test("a plan for another helper than the one asked for is not shown", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-helpers", /Helper apps/));
    await m.push({ type: "helpers", rows: ROWS });
    await m.click(m.container.find("#helper-install-viewer"));
    await m.push({ ...PLAN, id: "live" });
    assert.equal(dlg(m).find("#helper-confirm"), null);
  }));

test("removing asks first, keeps the data unless asked, and asks again before deleting it", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-helpers", /Helper apps/));
    await m.push({ type: "helpers", rows: ROWS });
    m.clear();
    await m.click(m.container.find("#helper-remove-idle"));
    assert.equal(m.sent("helperUninstall").length, 0);
    assert.match(dlg(m).textContent, /data folder \/data\/idle is kept/);
    assert.match(headOf(m), /helper-head/);
    await m.click(m.container.find("#helper-remove-confirm"));
    assert.deepEqual(m.sent("helperUninstall"), [{ cmd: "helperUninstall", id: "idle", purge: false }]);

    m.clear();
    await m.click(m.container.find("#helper-remove-idle"));
    await m.click(m.container.find("#helper-remove-purge"));
    assert.match(dlg(m).find('[role="alert"]').textContent, /deletes the folder \/data\/idle and everything in it/);
    assert.equal(m.sent("helperUninstall").length, 0);
    await m.click(m.container.find("#helper-remove-confirm"));
    assert.deepEqual(m.sent("helperUninstall"), [{ cmd: "helperUninstall", id: "idle", purge: true }]);

    m.clear();
    await m.click(m.container.find("#helper-delete-kept"));
    assert.match(dlg(m).textContent, /Delete Kept data/);
    await m.click(m.container.find("#helper-remove-confirm"));
    assert.deepEqual(m.sent("helperUninstall"), [{ cmd: "helperUninstall", id: "kept", purge: true }]);
  }));

test("a helper changing state is said once to a screen reader", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-helpers", /Helper apps/));
    await m.push({ type: "helpers", rows: ROWS });
    const live = () => dlg(m).findAll('[role="status"]').find((e) => e.attributes["aria-live"] === "polite");
    assert.equal(live().textContent, "");
    await m.push({ type: "helpers", rows: ROWS.map((r) => (r.id === "live" ? { ...r, state: "failed", error: "gone" } : r)) });
    assert.equal(live().textContent, "Live is failed: gone");
  }));

test("Escape closes the helper apps dialog and the keyboard goes back", () =>
  withPage({}, async (m) => {
    const button = m.byText(".rail-helpers", /Helper apps/);
    button.focus();
    await m.click(button);
    await m.push({ type: "helpers", rows: ROWS });
    dlg(m).dispatch("keydown", { key: "Escape", ctrlKey: false, shiftKey: false, altKey: false });
    await settle();
    assert.equal(dlg(m), null);
    assert.equal(m.dom.document.activeElement, button);
  }));

test("the action opens the dialog too, and a window reached through the relay is told where to go", async () => {
  await withPage({}, async (m) => {
    await m.palette(/Helper apps/);
    assert.ok(dlg(m), "the palette opens it");
  });
  await withPage({ remote: true }, async (m) => {
    assert.equal(m.byText(".rail-helpers", /Helper apps/), undefined, "no button in a window reached through the relay");
    m.clear();
    await m.palette(/Helper apps/);
    assert.equal(dlg(m), null);
    assert.equal(m.sent("helpers").length, 0);
    assert.ok(m.container.find(".notice").textContent.includes(H.RELAY_WORDS));
  });
});

test("the store sends no helper command from a window reached through the relay", () =>
  withPage({ remote: true }, async (m) => {
    m.clear();
    for (const cmd of ["helpers", "helperPlan", "helperInstall", "helperStart", "helperStop", "helperOpen", "helperUninstall", "makeBaton", "approveBaton"]) {
      assert.equal(m.store.send({ cmd, id: "x" }), false, cmd);
    }
    assert.equal(m.ws().sent.length, 0);
  }));

// ---- baton

const openBaton = async (m) => {
  await m.pane(CART);
  m.clear();
  await m.palette(/Make baton/);
  const ask = m.sent("makeBaton");
  assert.equal(ask.length, 1);
  assert.equal(ask[0].id, CART);
  assert.ok(ask[0].req);
  return ask[0].req;
};

test("making a baton needs an agent in the drawer", () =>
  withPage({}, async (m) => {
    m.clear();
    await m.palette(/Make baton/);
    assert.ok(m.container.find(".notice").textContent.includes(OPEN_AN_AGENT_WORDS));
    assert.equal(m.sent("makeBaton").length, 0);
  }));

test("the draft is asked for, waited for, then shown as text to edit", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    assert.match(dlg(m).textContent, /Reading this pane's work/);
    await m.push({ ...DRAFT, req: "someone-elses" });
    assert.match(dlg(m).textContent, /Reading this pane's work/, "another dialog's draft is not this one's");
    await m.push({ ...DRAFT, req, text: "# Baton <b>x</b> [REDACTED: aws-key]" });
    const d = dlg(m);
    assert.equal(valueIn(d.find("#baton-text")), "# Baton <b>x</b> [REDACTED: aws-key]");
    assert.equal(d.find("b"), null);
    assert.match(d.textContent, /1 secret removed\./);
    assert.match(d.textContent, /not a guarantee/);
    assert.match(d.textContent, /Some notes/);
    assert.match(d.textContent, /from cart · Claude Code/);
    assert.equal(m.dom.document.activeElement.attributes.id, "baton-text");
  }));

test("the count of removed secrets follows the text", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    await m.fill("#baton-text", "nothing left");
    assert.match(dlg(m).textContent, /0 secrets removed\./);
    await m.fill("#baton-text", "[REDACTED: aws-key] [REDACTED: aws-key]");
    assert.match(dlg(m).textContent, /2 secrets removed\./);
  }));

test("save sends the text whole with a fresh request, and the answer closes the dialog and is said", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    await m.fill("#baton-text", "my edited baton");
    await m.fill("#baton-task", "  first do this ");
    m.clear();
    await m.click(m.container.find("#baton-save"));
    const [c] = m.sent("saveBaton");
    assert.deepEqual({ ...c, req: undefined }, { cmd: "saveBaton", id: CART, req: undefined, text: "my edited baton", task: "first do this" });
    assert.notEqual(c.req, req);
    assert.ok(m.container.find("#baton-save").disabled, "the buttons wait for the answer");
    await m.push({ type: "batonSaved", paneId: CART, req: c.req, cmd: "saveBaton", id: "20261024-1", path: "/x", scrubbed: 2 });
    assert.equal(dlg(m), null);
    assert.match(m.container.find(".notice").textContent, /Saved baton 20261024-1 \(2 secrets removed\)\./);
  }));

test("a refusal gives the dialog back with what was typed, and the same command can be tried again", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    await m.fill("#baton-text", "kept through a refusal");
    await m.click(m.container.find("#baton-restart"));
    const [c] = m.sent("restartWithBaton");
    await m.push({ type: "batonError", paneId: CART, req: c.req, cmd: "restartWithBaton", error: "could not" });
    assert.ok(dlg(m), "still open");
    assert.equal(valueIn(m.container.find("#baton-text")), "kept through a refusal");
    assert.equal(m.container.find("#baton-restart").disabled, false);
    await m.click(m.container.find("#baton-restart"));
    assert.equal(m.sent("restartWithBaton").length, 2);
  }));

test("an answer to some other request does not end the wait", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    await m.click(m.container.find("#baton-save"));
    const [c] = m.sent("saveBaton");
    await m.push({ type: "batonSaved", paneId: CART, req: "other", cmd: "saveBaton", id: "x", scrubbed: 0 });
    assert.ok(dlg(m));
    assert.ok(m.container.find("#baton-save").disabled);
    await m.push({ type: "batonSaved", paneId: CART, req: c.req, cmd: "saveBaton", id: "x", scrubbed: 0 });
    assert.equal(dlg(m), null);
  }));

test("a command still out when the dialog was closed is not sent a second time", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    await m.click(m.container.find("#baton-start"));
    const [c] = m.sent("startFromBaton");
    await m.click(m.container.find("#baton-cancel"));
    assert.equal(dlg(m), null);
    const req2 = await openBaton(m);
    await m.push({ ...DRAFT, req: req2 });
    m.clear();
    await m.click(m.container.find("#baton-start"));
    assert.equal(m.sent("startFromBaton").length, 0, "refused while the first is out");
    assert.match(m.container.find(".notice").textContent, /still being done/);
    // The answer to the first is said even though its dialog is gone.
    await m.push({ type: "batonSaved", paneId: CART, req: c.req, cmd: "startFromBaton", id: "b-9", started: "p9", scrubbed: 0 });
    assert.match(m.container.find(".notice").textContent, /Started a new pane from baton b-9\./);
    await m.click(m.container.find("#baton-start"));
    assert.equal(m.sent("startFromBaton").length, 1, "and after the answer it goes");
  }));

test("a start in a new worktree needs a branch, and says the checkout's changes are left behind", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    assert.equal(m.container.find("#baton-branch"), null);
    await m.tick("#baton-worktree");
    assert.ok(m.container.find("#baton-branch"));
    assert.match(dlg(m).textContent, /uncommitted changes/);
    assert.ok(m.container.find("#baton-start").disabled, "no branch yet");
    await m.fill("#baton-branch", " fix-auth ");
    await m.tick("#baton-beside");
    m.clear();
    await m.click(m.container.find("#baton-start"));
    const [c] = m.sent("startFromBaton");
    assert.equal(c.branch, "fix-auth");
    assert.equal(c.split, true);
    assert.equal(c.confirmed, false);
    assert.equal(c.agent, "claude");
    assert.equal(c.model, "opus");
  }));

test("a pane that is not in a repository offers no worktree", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req, isRepo: false });
    assert.ok(m.container.find("#baton-worktree").disabled);
    assert.match(dlg(m).textContent, /Not a git repository, so no worktree/);
  }));

test("handing the baton to another company needs a tick, and the desk is told it was given", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    assert.equal(m.container.find("#baton-ack"), null);
    await m.choose("#baton-agent", "codex\n");
    assert.match(dlg(m).find(".baton-provider").textContent, /to OpenAI, not Anthropic/);
    assert.ok(m.container.find("#baton-start").disabled, "not before it is ticked");
    assert.equal(m.container.find("#baton-save").disabled, false, "keeping it here sends it nowhere");
    await m.tick("#baton-ack");
    m.clear();
    await m.click(m.container.find("#baton-start"));
    const [c] = m.sent("startFromBaton");
    assert.deepEqual([c.agent, c.model, c.confirmed], ["codex", "", true]);
  }));

test("going back to the same company clears the tick", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    await m.choose("#baton-agent", "codex\n");
    await m.tick("#baton-ack");
    await m.choose("#baton-agent", "claude\nopus");
    assert.equal(m.container.find("#baton-ack"), null);
    await m.choose("#baton-agent", "codex\n");
    assert.ok(!m.container.find("#baton-ack").checked, "the old tick does not carry over");
  }));

test("an empty baton cannot be kept or started", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    await m.fill("#baton-text", "   ");
    for (const id of ["#baton-save", "#baton-restart", "#baton-start"]) assert.ok(m.container.find(id).disabled, id);
  }));

test("a draft that could not be made closes the dialog", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ type: "batonError", paneId: CART, req, cmd: "makeBaton", error: "no conversation" });
    assert.equal(dlg(m), null);
  }));

test("Escape closes the baton dialog and the edits are not sent", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    await m.fill("#baton-text", "unsent");
    m.clear();
    dlg(m).dispatch("keydown", { key: "Escape", ctrlKey: false, shiftKey: false, altKey: false });
    await settle();
    assert.equal(dlg(m), null);
    assert.equal(m.ws().sent.length, 0);
  }));

test("a dropped connection ends the wait and says to look before trying again", () =>
  withPage({}, async (m) => {
    const req = await openBaton(m);
    await m.push({ ...DRAFT, req });
    await m.click(m.container.find("#baton-start"));
    m.ws().drop();
    await settle();
    assert.match(m.container.find(".notice").textContent, /dropped while a baton command was out/);
  }));

test("a window reached through the relay is not offered a baton", () =>
  withPage({ remote: true }, async (m) => {
    m.clear();
    await m.palette(/Make baton/);
    assert.equal(dlg(m), null);
    assert.equal(m.sent("makeBaton").length, 0);
  }));

// ---- projects

const openProjects = async (m) => {
  m.clear();
  await m.click(m.byText(".rail-projects", /Manage projects/));
  assert.deepEqual(m.sent("recents"), [{ cmd: "recents" }]);
  assert.equal(m.sent("browse").length, 1);
  await m.push({ type: "recents", items: [
    { root: "/r/one", name: "one", exists: true }, { root: "/r/two", name: "two", exists: true }, { root: "/r/three", name: "three", exists: false },
    { root: "/r/old", name: "old", exists: true, archived: true },
  ] });
  await m.push({ type: "browse", path: "/home/me", parent: "/home", entries: [{ name: "app", path: "/home/me/app", isRepo: true }, { name: "docs", path: "/home/me/docs" }], places: [{ name: "Home", path: "/home/me" }] });
};

test("the projects dialog lists what is open, what is recent and what is archived, as text", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    const d = dlg(m);
    assert.equal(d.findAll(".proj-row").filter((e) => e.attributes["data-root"].startsWith("/work")).length, 3);
    assert.equal(d.find("img"), null, "a project's name is text");
    assert.match(d.textContent, /<img src=x onerror=1>/);
    assert.match(d.textContent, /1 waiting/);
    assert.match(d.textContent, /2 directories/);
    assert.match(d.textContent, /\/r\/three \(missing\)/);
    assert.equal(d.find("#recent-0").textContent.includes("one"), true);
    assert.equal(d.find("#recent-2"), null, "a project whose folder is gone cannot be opened");
    assert.equal(d.find("#archived-0"), null, "archived projects wait behind their own button");
    await m.click(d.find("#archived-show"));
    assert.ok(dlg(m).find("#archived-0"));
  }));

test("choosing an open project switches to it and closes the dialog", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.container.find("#open-1"));
    assert.deepEqual(m.sent("selectProject"), [{ cmd: "selectProject", root: "/work/b" }]);
    assert.equal(dlg(m), null);
  }));

test("choosing a directory of a grouped project selects that repo", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    await m.click(m.byText("button", /^Directories$/));
    m.clear();
    await m.click(m.byText(".proj-member-go", /g-two/));
    assert.deepEqual(m.sent("selectRepo"), [{ cmd: "selectRepo", root: "/work/g2" }]);
    assert.equal(dlg(m), null);
  }));

test("renaming an open project goes through its group, a remembered one through the memory, and empty is allowed", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.container.find('[aria-label="Rename alpha"]'));
    assert.equal(m.dom.document.activeElement.attributes["aria-label"], "New name for alpha");
    await m.fill(".proj-name-input", "  Alpha Two ");
    await m.click(m.container.find(".proj-save"));
    assert.deepEqual(m.sent("renameGroup"), [{ cmd: "renameGroup", root: "/work/a", text: "Alpha Two" }]);

    await m.click(m.container.find('[aria-label="Rename one"]'));
    await m.fill(".proj-name-input", "");
    m.container.find(".proj-name-input").keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("renameProject"), [{ cmd: "renameProject", root: "/r/one", text: "" }]);
  }));

test("Escape in a name being edited ends the edit and leaves the dialog", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.container.find('[aria-label="Rename alpha"]'));
    await m.fill(".proj-name-input", "x");
    m.container.find(".proj-name-input").keydown("Escape");
    await settle();
    assert.ok(dlg(m), "the dialog stays");
    assert.equal(m.container.find(".proj-name-input"), null);
    assert.equal(m.sent("renameGroup").length, 0);
  }));

test("an unchanged name is not sent", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.container.find('[aria-label="Rename alpha"]'));
    await m.click(m.container.find(".proj-save"));
    assert.equal(m.sent("renameGroup").length, 0);
  }));

test("archive and unarchive say which, and touch nothing on disk", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.container.find('[aria-label="Archive alpha"]'));
    assert.deepEqual(m.sent("archiveProject"), [{ cmd: "archiveProject", root: "/work/a", archived: true }]);
    assert.match(m.container.find('[aria-label="Archive alpha"]').attributes.title, /Nothing on disk is touched/);
    await m.click(m.container.find("#archived-show"));
    await m.click(m.container.find('[aria-label="Unarchive old"]'));
    assert.deepEqual(m.sent("archiveProject")[1], { cmd: "archiveProject", root: "/r/old", archived: false });
  }));

test("closing a project that has agents running asks first; one with none closes at once", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.container.find('[aria-label="Close alpha"]'));
    assert.equal(m.sent("closeProject").length, 0);
    const ask = m.container.find(".confirm");
    assert.ok(ask);
    assert.match(ask.textContent, /Every agent in every tab of alpha stops/);
    assert.match(ask.textContent, /2 agents are still working or waiting/);
    assert.equal(m.dom.document.activeElement.textContent, "Cancel", "the keyboard starts on Cancel");
    await m.click(m.container.find(".confirm").findAll("button").find((b) => /^Cancel$/.test(b.textContent)));
    assert.equal(m.container.find(".confirm"), null);
    assert.equal(m.sent("closeProject").length, 0);
    await m.click(m.container.find('[aria-label="Close alpha"]'));
    await m.click(m.container.find(".confirm").findAll("button").find((b) => /^Close project$/.test(b.textContent)));
    assert.deepEqual(m.sent("closeProject"), [{ cmd: "closeProject", root: "/work/a" }]);

    m.clear();
    await m.click(m.container.findAll("button").find((b) => (b.attributes["aria-label"] || "").startsWith("Close <img")));
    assert.deepEqual(m.sent("closeProject"), [{ cmd: "closeProject", root: "/work/b" }], "no agents there, so no question");
  }));

test("the last open project cannot be closed from here", () =>
  withPage({ state: { projects: [PROJECTS[0]] } }, async (m) => {
    await openProjects(m);
    assert.equal(m.container.find('[aria-label="Close alpha"]'), null);
  }));

test("a recent project opens by its path, moves, and is removed from the list", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.container.find('[aria-label="Move two up"]'));
    assert.deepEqual(m.sent("reorderProjects"), [{ cmd: "reorderProjects", roots: ["/r/two", "/r/one", "/r/three"] }]);
    assert.ok(m.container.find('[aria-label="Move one up"]').disabled, "the first cannot go up");
    assert.ok(m.container.find('[aria-label="Move three down"]').disabled, "the last cannot go down");
    await m.click(m.container.find('[aria-label="Remove two from the list"]'));
    assert.deepEqual(m.sent("removeProject"), [{ cmd: "removeProject", root: "/r/two" }]);
    assert.match(m.container.find('[aria-label="Remove two from the list"]').attributes.title, /Nothing on disk is touched/);
    await m.click(m.container.find("#recent-0"));
    assert.deepEqual(m.sent("openProject"), [{ cmd: "openProject", path: "/r/one" }]);
    assert.equal(dlg(m), null);
  }));

test("only the first eight recent projects show until more are asked for", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    const items = Array.from({ length: 11 }, (_, i) => ({ root: "/r/p" + i, name: "p" + i, exists: true }));
    await m.push({ type: "recents", items });
    assert.equal(m.container.findAll("button").filter((e) => /^recent-\d+$/.test(e.attributes.id || "")).length, 8);
    await m.click(m.container.find("#recent-more"));
    assert.equal(m.container.findAll(".proj-row").filter((e) => e.attributes["data-root"].startsWith("/r/p")).length, 11);
  }));

test("a folder is opened from the browser by its full path only", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    assert.equal(valueIn(m.container.find("#browse-path")), "/home/me");
    m.clear();
    await m.fill("#browse-path", "relative/dir");
    await m.click(m.container.find("#browse-open"));
    assert.equal(m.sent("openProject").length, 0);
    assert.match(m.container.find('[role="alert"]').textContent, /full path/);
    await m.fill("#browse-path", "/home/me/app");
    await m.click(m.container.find("#browse-open"));
    assert.deepEqual(m.sent("openProject"), [{ cmd: "openProject", path: "/home/me/app" }]);
    assert.equal(dlg(m), null);
  }));

test("the browser walks folders, asks the desk for each, and opens one by its row", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.byText(".proj-dir-into", /docs/));
    assert.deepEqual(m.sent("browse"), [{ cmd: "browse", path: "/home/me/docs" }]);
    await m.click(m.container.find(".proj-places").findAll("button").find((b) => /^Home$/.test(b.textContent)));
    await m.click(m.container.findAll("button").filter((b) => b.textContent === "Up").pop());
    assert.equal(m.sent("browse")[2].path, "/home");
    await m.fill("#browse-path", "/elsewhere");
    m.container.find("#browse-path").keydown("Enter");
    await settle();
    assert.equal(m.sent("browse")[3].path, "/elsewhere");
    await m.click(m.container.find('[aria-label="Open app"]'));
    assert.deepEqual(m.sent("openProject"), [{ cmd: "openProject", path: "/home/me/app" }]);
    assert.equal(dlg(m), null, "the dialog closes");
  }));

test("a folder that cannot be listed says why", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    await m.push({ type: "browse", path: "/nope", error: "/nope does not exist <b>", entries: [], places: [] });
    assert.match(dlg(m).textContent, /\/nope does not exist <b>/);
    assert.equal(dlg(m).find("b"), null);
  }));

test("adding a directory to a project uses the browser and returns to the list", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.container.find('[aria-label="Add a directory to alpha"]'));
    assert.match(dlg(m).textContent, /Add a directory to alpha/);
    assert.equal(m.sent("browse").length, 1);
    await m.click(m.container.find('[aria-label="Add docs"]'));
    assert.deepEqual(m.sent("addRepoToGroup"), [{ cmd: "addRepoToGroup", root: "/work/a", path: "/home/me/docs" }]);
    assert.ok(dlg(m), "the dialog stays, so the group is there to see");
    assert.match(dlg(m).textContent, /Open a folder/);
  }));

test("a directory can be taken out of a group, with nothing on disk touched", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    await m.click(m.byText("button", /^Directories$/));
    m.clear();
    await m.click(m.container.find('[aria-label="Remove g-one out of grouped"]'));
    assert.deepEqual(m.sent("removeRepoFromGroup"), [{ cmd: "removeRepoFromGroup", root: "/work/g1" }]);
  }));

test("open projects are grouped by picking two or more and naming the group", () =>
  withPage({}, async (m) => {
    await openProjects(m);
    m.clear();
    await m.click(m.container.find("#group-start"));
    assert.ok(m.container.find("#group-make").disabled);
    await m.tick("#group-pick-0");
    assert.ok(m.container.find("#group-make").disabled, "one is not a group");
    await m.tick("#group-pick-1");
    await m.fill('[aria-label="Name for the grouped project"]', " Pair ");
    await m.click(m.container.find("#group-make"));
    assert.deepEqual(m.sent("groupProjects"), [{ cmd: "groupProjects", roots: ["/work/a", "/work/b"], text: "Pair" }]);
    assert.equal(m.container.find("#group-make"), null);
  }));

test("Escape closes the projects dialog and the keyboard goes back", () =>
  withPage({}, async (m) => {
    const button = m.byText(".rail-projects", /Manage projects/);
    button.focus();
    await m.click(button);
    dlg(m).dispatch("keydown", { key: "Escape", ctrlKey: false, shiftKey: false, altKey: false });
    await settle();
    assert.equal(dlg(m), null);
    assert.equal(m.dom.document.activeElement, button);
  }));

test("a project chosen in the rail is made the desk's active one, and the desk's own choice is not sent back", () =>
  withPage({}, async (m) => {
    m.clear();
    await m.click(m.container.findAll(".rail-item").find((e) => e.attributes["data-root"] === "/work/g"));
    assert.deepEqual(m.sent("selectProject"), [{ cmd: "selectProject", root: "/work/g" }]);
    m.clear();
    await m.push(stateOf(graph(), { root: "/work/b" }));
    assert.equal(m.sent("selectProject").length, 0, "following the desk sends nothing");
  }));

test("a window reached through the relay does not change the desk's active project from the rail", () =>
  withPage({ remote: true }, async (m) => {
    m.clear();
    await m.click(m.container.findAll(".rail-item").find((e) => e.attributes["data-root"] === "/work/g"));
    assert.equal(m.sent("selectProject").length, 0, "the rail only changes what the window watches there");
  }));

// ---- requests for an answer (approveBaton)

const ASK = (id, over = {}) => ({
  type: "notice", kind: "approval", id, lead: "Baton leaving its provider.", error: false, pin: true, lifeMs: 60000,
  text: "A phone asked to start Codex (OpenAI) from a baton that came from Claude <b>x</b>. Allow it only if you asked for this.",
  action: { label: "Send it", send: { cmd: "approveBaton", text: "tok-" + id } }, ...over,
});

test("only an approveBaton button makes a request for an answer", () => {
  assert.deepEqual(approvalOf(ASK("a")), { id: "a", lead: "Baton leaving its provider.", text: ASK("a").text, label: "Send it", token: "tok-a" });
  assert.equal(approvalOf(ASK("a", { action: { label: "Send it", send: { cmd: "quit" } } })), null, "no other command is run because a notice names it");
  assert.equal(approvalOf(ASK("a", { action: { label: "", send: { cmd: "approveBaton", text: "t" } } })), null);
  assert.equal(approvalOf(ASK("a", { action: { label: "Go", send: { cmd: "approveBaton", text: "" } } })), null);
  assert.equal(approvalOf(ASK("", {})), null);
  assert.equal(approvalOf(ASK("a", { kind: "other" })), null);
  assert.equal(approvalOf(null), null);
});

test("a request is a card with the desktop's words as text, and Send it sends the token it carried", () =>
  withPage({}, async (m) => {
    await m.push(ASK("r1"));
    const card = m.container.find(".approval");
    assert.ok(card);
    assert.match(card.textContent, /Baton leaving its provider\./);
    assert.match(card.textContent, /Claude <b>x<\/b>/);
    assert.equal(card.find("b"), null);
    assert.equal(m.container.find(".notice"), null, "it is not the latest notice as well");
    m.clear();
    await m.click(card.find(".approval-yes"));
    assert.deepEqual(m.sent("approveBaton"), [{ cmd: "approveBaton", text: "tok-r1" }]);
    assert.equal(m.container.find(".approval"), null);
  }));

test("nothing is allowed by Not now, and the card does not take the keyboard", () =>
  withPage({}, async (m) => {
    const before = m.dom.document.activeElement;
    await m.push(ASK("r1"));
    assert.equal(m.dom.document.activeElement, before);
    m.clear();
    await m.click(m.container.find(".approval-no"));
    assert.equal(m.container.find(".approval"), null);
    assert.equal(m.sent("approveBaton").length, 0);
  }));

test("the desktop withdrawing a request takes its card away, and only that one", () =>
  withPage({}, async (m) => {
    await m.push(ASK("r1"));
    await m.push(ASK("r2"));
    assert.equal(m.container.findAll(".approval").length, 2);
    await m.push({ type: "noticeWithdraw", id: "r1" });
    assert.equal(m.container.findAll(".approval").length, 1);
    await m.push({ type: "noticeWithdraw", id: "nope" });
    assert.equal(m.container.findAll(".approval").length, 1);
    m.clear();
    await m.click(m.container.find(".approval-yes"));
    assert.equal(m.sent("approveBaton")[0].text, "tok-r2");
  }));

test("a request sent again replaces itself, and the oldest goes when too many wait", () =>
  withPage({}, async (m) => {
    await m.push(ASK("r1"));
    await m.push(ASK("r1", { text: "again" }));
    assert.equal(m.container.findAll(".approval").length, 1);
    for (let i = 2; i <= MAX_APPROVALS + 1; i++) await m.push(ASK("r" + i));
    assert.equal(m.container.findAll(".approval").length, MAX_APPROVALS);
    assert.ok(!m.container.findAll(".approval").some((c) => c.textContent.includes("again")), "the oldest went");
    assert.match(m.container.find(".notice").textContent, /oldest was put away/);
  }));

test("a connection that drops takes the requests with it, since their tokens went too", () =>
  withPage({}, async (m) => {
    await m.push(ASK("r1"));
    m.ws().drop();
    await settle();
    assert.equal(m.container.find(".approval"), null);
    assert.ok(m.container.find(".notice").textContent.includes(LOST_APPROVAL_WORDS));
  }));

test("a plain notice of kind approval, with no button, is an ordinary notice", () =>
  withPage({}, async (m) => {
    await m.push({ type: "notice", kind: "approval", text: "Another window allowed that baton.", error: false });
    assert.equal(m.container.find(".approval"), null);
    assert.match(m.container.find(".notice").textContent, /Another window allowed that baton/);
  }));
