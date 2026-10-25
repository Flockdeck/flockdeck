// L8, the Team dialog (a project's task board and messages) and the Agents dialog
// (every pane in every open project). The pure parts first, then the store, then
// the page on a fake control socket. The Team dialog is the desk's: a window
// reached through the relay is told so and sends nothing.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { PENDING_MS } from "../src/shell/Team.js";
import {
  TEAM_RELAY_WORDS, addTaskCommand, assignCommand, boardOf, branchCommand, mergeTasks, noteCommand, pruneCommand, resultOf, resultWords, seenCommand,
  sendMessageCommand, statusCommand, taskGetCommand, taskOf, teamOf,
} from "../src/lib/team.js";
import { filterLive, GROUPS } from "../src/shell/palette.js";
import { agentsOf, byProject, needsYou, revealAgentCommand } from "../src/lib/agents.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const MARKUP = '<img src=x onerror="alert(1)">';
const NOW = Date.now();
const iso = (ms) => new Date(ms).toISOString();

const task = (over = {}) => ({
  id: "T1", title: "Write the parser", state: "assigned", owner: "worker api", ownerRole: "worker", ownerPane: "p2", ownerOpen: true, branch: "feat/parser", notes: 1,
  creator: { name: "you", user: true }, updated: iso(NOW - 60000), moves: [{ to: "in-progress" }, { to: "cancelled", needNote: true }], canAssign: true, canEdit: true, ...over,
});
const message = (over = {}) => ({
  id: 7, from: { name: "api", role: "worker", pane: "p2" }, to: "p1", toName: "boss", toRole: "manager", time: iso(NOW - 120000), text: "Done with the parser.", ...over,
});
const teamPush = (over = {}) => ({
  type: "team", root: "/work/a", project: "alpha", hasManager: true,
  roster: [{ pane: "p1", name: "boss", role: "manager", status: "idle" }, { pane: "p2", name: "api", role: "worker", status: "working" }],
  counts: { panes: 2, live: 2, tasks: { assigned: 1 }, open: 1, messages: 1, unread: 0, unseen: 1 },
  tasks: [task()], messages: [message()], ...over,
});

// ---------------------------------------------------------------- pure parts

test("team: a push is read defensively, grouped by state, and the full list is laid over it", () => {
  assert.equal(teamOf(null), null);
  const t = teamOf(teamPush({ tasks: [task(), null, { id: 5 }, task({ id: "T3", state: "done", moves: "x" }), task({ id: "T2", state: "in-progress", notes: -3 })], boardError: 4 }));
  assert.deepEqual(t.tasks.map((x) => x.id), ["T1", "T3", "T2"], "a task with no string id is dropped");
  assert.equal(t.tasks[1].moves.length, 0);
  assert.equal(t.tasks[2].notes, 0);
  assert.equal(t.boardError, "");
  assert.deepEqual(boardOf(t.tasks).map((g) => g.state), ["assigned", "in-progress", "done"], "in the order of the states, empty groups left out");
  assert.deepEqual(mergeTasks([taskOf(task({ id: "T10" })), taskOf(task({ id: "T2" }))], [taskOf(task({ id: "T2" })), taskOf(task({ id: "T1", state: "done" }))]).map((x) => x.id), ["T1", "T2", "T10"]);
  assert.equal(t.messages[0].from.name, "api");
  assert.equal(t.roster[1].role, "worker");
});

test("team: commands carry the project and request, and one the desktop would refuse is not made", () => {
  const A = "/work/a";
  assert.deepEqual(addTaskCommand(A, { title: "  Fix it ", body: " why ", role: "", branch: " b " }, "u1"), { cmd: "taskAdd", root: A, req: "u1", title: "Fix it", spec: "why", branch: "b" });
  assert.equal(addTaskCommand(A, { title: "  " }, "u1"), null);
  const t = taskOf(task());
  assert.deepEqual(statusCommand(A, t, "in-progress", "", "u2"), { cmd: "taskStatus", root: A, req: "u2", id: "T1", state: "in-progress" });
  assert.equal(statusCommand(A, t, "cancelled", "  ", "u2"), null, "a move that needs a note");
  assert.deepEqual(statusCommand(A, t, "cancelled", "no longer wanted", "u2").text, "no longer wanted");
  assert.equal(statusCommand(A, t, "done", "x", "u2"), null, "a move the desktop did not offer");
  assert.deepEqual(noteCommand(A, "T1", " hi ", "u3"), { cmd: "taskNote", root: A, req: "u3", id: "T1", text: "hi" });
  assert.equal(noteCommand(A, "T1", " ", "u3"), null);
  assert.deepEqual(assignCommand(A, "T1", " worker ", "u4"), { cmd: "taskAssign", root: A, req: "u4", id: "T1", target: "worker" });
  assert.equal(assignCommand(A, "T1", "", "u4"), null);
  assert.deepEqual(branchCommand(A, "T1", " b ", "u5"), { cmd: "taskBranch", root: A, req: "u5", id: "T1", branch: "b" });
  assert.deepEqual(pruneCommand(A, "30", "u6"), { cmd: "taskPrune", root: A, req: "u6", size: 30 });
  for (const bad of ["0", "-2", "1.5", "abc", "", "99999"]) assert.equal(pruneCommand(A, bad, "u6"), null, bad);
  assert.deepEqual(sendMessageCommand(A, " worker ", " hello ", "u7"), { cmd: "msgSend", root: A, req: "u7", target: "worker", text: "hello" });
  assert.equal(sendMessageCommand(A, "worker", " ", "u7"), null);
  assert.deepEqual(seenCommand(A, { id: 7, to: "p1" }, "u8"), { cmd: "msgSeen", root: A, req: "u8", id: "p1", msgId: 7 });
  assert.equal(seenCommand(A, { id: 7, to: "" }, "u8"), null);
  assert.equal(taskGetCommand(A, "", "u9"), null);
});

test("team: an answer is said in a sentence", () => {
  assert.equal(resultWords(resultOf({ ok: false, error: "no such task" })), "no such task");
  assert.equal(resultWords(resultOf({ ok: true, op: "msgSend", sent: 2 })), "Sent to 2 panes.");
  assert.equal(resultWords(resultOf({ ok: true, op: "msgSend", sent: 1, warning: "Not delivered to p3" })), "Sent to 1 pane. Not delivered to p3");
  assert.equal(resultWords(resultOf({ ok: true, op: "taskPrune", pruned: 3 })), "Removed 3 finished tasks.");
  assert.equal(resultWords(resultOf({ ok: true, op: "taskStatus", told: 1 })), "1 pane was told.");
  assert.equal(resultWords(resultOf({ ok: true, op: "taskNote" })), "");
});

test("agents: the list is read defensively and grouped by project", () => {
  const items = agentsOf({ items: [
    { paneId: "p1", root: "/a", project: "alpha", name: "boss", activity: "waiting", waiting: "Run: ls", for: "2m", dirty: 2 },
    null, { name: "no id" },
    { paneId: "p2", root: "/b", project: "beta", name: "x", activity: "working", background: -1 },
    { paneId: "p3", root: "/a", project: "alpha", name: "y", activity: "idle", failed: true },
  ] });
  assert.deepEqual(items.map((a) => a.paneId), ["p1", "p2", "p3"]);
  assert.equal(needsYou(items[0]), true);
  assert.equal(needsYou(items[1]), false);
  assert.equal(items[1].background, 0);
  assert.deepEqual(byProject(items).map((g) => [g.project, g.panes.length]), [["alpha", 2], ["beta", 1]]);
  assert.deepEqual(revealAgentCommand({ root: "/a", tabId: "t1", paneId: "p1" }), { cmd: "revealPane", root: "/a", node: "t1", id: "p1" });
  assert.deepEqual(agentsOf(null), []);
});

// ---------------------------------------------------------------- the store

test("the store watches the team, sends it again after a reconnect, and keeps what comes back", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  assert.equal(store.send({ cmd: "taskAdd", title: "x" }), false, "nothing is sent before the hello says this is not a relay window");
  ws.frame(hello({}));
  assert.equal(store.watchTeam("/work/a"), true);
  assert.deepEqual(ws.commands("teamWatch").map((c) => c.root), ["/work/a"]);
  ws.frame(teamPush());
  assert.equal(store.getState().team.project, "alpha");
  assert.equal(store.getState().team.seq, 1);
  ws.frame({ type: "tasks", root: "/work/a", tasks: [task()] });
  ws.frame({ type: "messages", root: "/work/a", messages: [message()] });
  ws.frame({ type: "teamResult", op: "taskAdd", ok: true, req: "u1", task: task({ id: "T9" }) });
  assert.equal(store.getState().teamResult.task.id, "T9");
  assert.equal(store.getState().teamResult.seq, 1);
  assert.ok(store.getState().teamTasks && store.getState().teamMessages);
  ws.frame({ type: "teamResult", op: "taskAdd", ok: false, req: "u2", error: "no" });
  assert.equal(store.getState().teamResult.seq, 2);
  assert.equal(store.send({ cmd: "taskAdd", title: "x" }), true);
  ws.frame({ type: "agents", items: [{ paneId: "p1" }] });
  assert.equal(store.getState().agentsList.items.length, 1);
  // The project goes away: shown as gone, and not asked for again.
  ws.frame({ type: "teamGone", root: "/work/a", error: "project closed" });
  assert.equal(store.getState().team, null);
  assert.equal(store.getState().teamGone.error, "project closed");
  ws.frame(hello({}));
  assert.equal(ws.commands("teamWatch").length, 1, "a watch that was given up is not asked for again");
  store.watchTeam("/work/b");
  ws.frame(hello({}));
  assert.deepEqual(ws.commands("teamWatch").map((c) => c.root), ["/work/a", "/work/b", "/work/b"], "a live watch is sent again with the next hello");
  store.unwatchTeam();
  assert.equal(ws.commands("teamUnwatch").length, 1);
  assert.equal(store.getState().team, null);
  store.stop();
});

test("a relay window sends no Team command, is told so, and still has the Agents list", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({ remote: true }));
  for (const c of ["teamSnapshot", "teamWatch", "teamUnwatch", "taskList", "taskGet", "taskAdd", "taskAssign", "taskStatus", "taskNote", "taskBranch", "taskPrune", "msgList", "msgSend", "msgSeen", "teamClose"]) {
    assert.equal(store.send({ cmd: c }), false, c);
  }
  assert.equal(store.watchTeam("/work/a"), false);
  assert.equal(store.getState().teamRefused, true);
  assert.equal(ws.commands("teamWatch").length, 0);
  ws.frame(teamPush());
  assert.equal(store.getState().team, null, "a team push is not taken in a relay window");
  assert.equal(store.send({ cmd: "agents" }), true);
  store.stop();
});

test("a refusal from the desktop is the same as a refused window", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({}));
  store.watchTeam("/work/a");
  ws.frame(teamPush());
  ws.frame({ type: "teamResult", op: "teamWatch", ok: false, code: "deskOnly", error: "The Team panel is used on the machine Flockdeck runs on" });
  assert.equal(store.getState().team, null);
  assert.equal(store.getState().teamRefused, true);
  store.stop();
});

// ---------------------------------------------------------------- the page

const KEYS = [
  { id: "team", keys: "Ctrl+Shift+M", label: "Team", section: "Agents" },
  { id: "agents", keys: "Ctrl+Shift+A", label: "Agents", section: "Agents" },
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Window" },
];
const graph = () => ({ ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" });
const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0, active: true }];
const stateOf = (g) => ({
  type: "state", root: "/work/a", projects, agents: { items: [], roles: [], default: {} }, activeTab: "", tabs: [],
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured", status: "working" }])),
});

async function mount({ helloOver = {}, treeView = StubTree } = {}) {
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
  render(html`<${Live} store=${store} window=${dom.window} terminalOptions=${terminalOptions} treeView=${treeView} />`, container);
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

const valueIn = (el) => (el.value !== undefined ? el.value : el.attributes.value);
const closeBtn = (m) => m.container.findAll(".dialog-btn").find((b) => b.textContent === "Close");
const reqOf = (cmds) => cmds[cmds.length - 1].req;

test("Team: the key opens it, asks for the project, shows the board and the messages as text, and closing lets go", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    assert.deepEqual(m.sent("teamWatch").map((c) => c.root), ["/work/a"]);
    assert.ok(m.container.find("#team-loading"), "until the desktop answers");
    await m.push(teamPush({ tasks: [task({ title: MARKUP }), task({ id: "T2", title: "Review", state: "in-review", owner: "", branch: "", notes: 0 })], messages: [message({ text: MARKUP })] }));
    assert.equal(m.container.find("#team-loading"), null);
    assert.match(m.container.find("#team-title").textContent, /Team, alpha/);
    assert.equal(m.container.findAll(".team-group").length, 2);
    assert.match(m.container.find("#team-tab-tasks").textContent, /1 open/);
    assert.match(m.container.find("#team-tab-messages").textContent, /1 new/);
    assert.ok(m.text().includes(MARKUP));
    assert.equal(m.container.findAll("img").length, 0, "nothing an agent wrote is markup");
    await m.click(m.container.find("#team-tab-messages"));
    assert.ok(m.container.find("#team-messages").textContent.includes(MARKUP));
    assert.match(m.container.find(".team-who").textContent, /api \(worker\) to boss \(manager\), 2 minutes ago/);
    m.clear();
    await m.click(closeBtn(m));
    assert.equal(m.container.find("#team-title"), null);
    assert.equal(m.sent("teamUnwatch").length, 1);
  }));

test("Team: opening a task asks for it, shows its notes and history, and the moves it is offered send a status", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush());
    m.clear();
    await m.click(m.container.find(".team-task-head"));
    const get = m.sent("taskGet");
    assert.deepEqual(get.map((c) => [c.id, c.root]), [["T1", "/work/a"]]);
    await m.push({ type: "teamResult", op: "taskGet", ok: true, req: reqOf(get), task: task({
      body: "Parse the file.", baton: "", noteList: [{ author: { name: "api", role: "worker" }, time: iso(NOW - 3600000), text: "started" }],
      historyList: [{ actor: { name: "you", user: true }, time: iso(NOW - 7200000), what: "created" }] }) });
    assert.match(m.container.find("#team-detail").textContent, /Parse the file\./);
    assert.match(m.container.find(".team-notes").textContent, /api \(worker\), 1 hour ago started/);
    assert.match(m.container.find(".team-history").textContent, /created you, 2 hours ago/);
    const moves = m.container.findAll(".team-move");
    assert.deepEqual(moves.map((b) => b.attributes["data-to"]), ["in-progress", "cancelled"]);
    assert.equal(!!moves[1].disabled || moves[1].attributes.disabled !== undefined, true, "a move that needs a note waits for one");
    m.clear();
    await m.click(moves[0]);
    const first = m.sent("taskStatus");
    assert.deepEqual(first.map(({ req, ...c }) => c), [{ cmd: "taskStatus", root: "/work/a", id: "T1", state: "in-progress" }]);
    // A move is sent one at a time: the next waits for this one's answer.
    await m.type("#team-note", "no longer wanted");
    await m.click(m.container.findAll(".team-move")[1]);
    assert.equal(m.sent("taskStatus").length, 1, "a second move was sent before the first was answered");
    await m.push({ type: "teamResult", op: "taskStatus", ok: true, req: reqOf(first), task: task({ state: "in-progress" }), told: 0 });
    await m.type("#team-note", "no longer wanted");
    m.clear();
    await m.click(m.container.findAll(".team-move")[1]);
    assert.deepEqual(m.sent("taskStatus").map(({ req, ...c }) => c), [{ cmd: "taskStatus", root: "/work/a", id: "T1", state: "cancelled", text: "no longer wanted" }]);
    // Note, assignment and branch.
    await m.type("#team-note", "a note");
    m.clear();
    await m.click(m.container.find("#team-note-add"));
    assert.equal(m.sent("taskNote")[0].text, "a note");
    await m.type("#team-assign", "worker");
    await m.click(m.container.find("#team-assign-go"));
    assert.equal(m.sent("taskAssign")[0].target, "worker");
    await m.type("#team-branch", "feat/other");
    await m.click(m.container.find("#team-branch-go"));
    assert.equal(m.sent("taskBranch")[0].branch, "feat/other");
  }));

test("Team: a task the person may not assign or edit has no controls for it, and a refusal is shown", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush({ tasks: [task({ canAssign: false, canEdit: false, moves: [] })] }));
    await m.click(m.container.find(".team-task-head"));
    assert.equal(m.container.find("#team-assign"), null);
    assert.equal(m.container.find("#team-branch"), null);
    assert.equal(m.container.findAll(".team-move").length, 0);
    await m.type("#team-note", "x");
    m.clear();
    await m.click(m.container.find("#team-note-add"));
    const req = reqOf(m.sent("taskNote"));
    await m.push({ type: "teamResult", op: "taskNote", ok: false, req, error: "only the manager may do that" });
    assert.match(m.container.find("#team-problem").textContent, /only the manager may do that/);
    assert.equal(valueIn(m.container.find("#team-note")), "x", "what was typed is kept for another try");
  }));

test("Team: a second click on Add task before the answer sends nothing more, and the button is ready after it", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush({}));
    await m.type("#team-new-title", "New thing");
    m.clear();
    await m.click(m.container.find("#team-add"));
    await m.click(m.container.find("#team-add"));
    const add = m.sent("taskAdd");
    assert.equal(add.length, 1, "the task was added twice");
    assert.equal(!!m.container.find("#team-add").disabled, true);
    await m.push({ type: "teamResult", op: "taskAdd", ok: true, req: reqOf(add), task: task({ id: "T5" }), told: 0 });
    await m.type("#team-new-title", "Another");
    await m.click(m.container.find("#team-add"));
    assert.equal(m.sent("taskAdd").length, 2);
  }));

// The 20 s wait that frees a send whose answer does not come, with setTimeout held so the test fires it.
async function withHeldTimers(fn) {
  const realSet = globalThis.setTimeout;
  const realClear = globalThis.clearTimeout;
  const held = new Map();
  let id = 9000;
  globalThis.setTimeout = (f, ms, ...rest) => {
    if (ms !== PENDING_MS) return realSet(f, ms, ...rest);
    held.set(++id, f);
    return id;
  };
  globalThis.clearTimeout = (t) => (held.has(t) ? held.delete(t) : realClear(t));
  try {
    await fn(held);
  } finally {
    globalThis.setTimeout = realSet;
    globalThis.clearTimeout = realClear;
  }
}

test("Team: a send whose answer is lost frees its button after the wait", () =>
  withHeldTimers((held) => withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush({}));
    await m.type("#team-new-title", "New thing");
    m.clear();
    await m.click(m.container.find("#team-add"));
    assert.equal(m.sent("taskAdd").length, 1);
    assert.equal(!!m.container.find("#team-add").disabled, true, "it waits for the answer");
    assert.equal(held.size, 1);
    // The answer never comes: the wait ends.
    for (const f of [...held.values()]) f();
    held.clear();
    await settle();
    assert.equal(!!m.container.find("#team-add").disabled, false, "the button is ready again");
    await m.click(m.container.find("#team-add"));
    assert.equal(m.sent("taskAdd").length, 2);
  })));

test("Team: the wait is cleared by the answer, and by closing the dialog", () =>
  withHeldTimers((held) => withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush({}));
    await m.type("#team-new-title", "New thing");
    await m.click(m.container.find("#team-add"));
    const add = m.sent("taskAdd");
    assert.equal(held.size, 1);
    await m.push({ type: "teamResult", op: "taskAdd", ok: true, req: reqOf(add), task: task({ id: "T5" }), told: 0 });
    assert.equal(held.size, 0, "an answered send holds no timer");
    await m.type("#team-new-title", "Another");
    await m.click(m.container.find("#team-add"));
    assert.equal(held.size, 1);
    await m.click(closeBtn(m));
    assert.equal(m.container.find("#team-detail"), null);
    assert.equal(held.size, 0, "a closed dialog holds no timer");
  })));

test("Team: a dropped connection frees the buttons at once, not after the wait", () =>
  withHeldTimers((held) => withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush({}));
    await m.type("#team-new-title", "New thing");
    await m.click(m.container.find("#team-add"));
    assert.equal(!!m.container.find("#team-add").disabled, true);
    m.ws().drop();
    await settle();
    assert.equal(!!m.container.find("#team-add").disabled, false, "the answer went with the connection");
    assert.equal(held.size, 0);
  })));

test("Team: adding a task, messaging, marking seen, the whole text, finished tasks and pruning", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush({ finishedHidden: 4, messages: [message({ cut: true, text: "Begin" }), message({ id: 8, seen: true, from: { name: "you", user: true } })] }));
    assert.equal(!!m.container.find("#team-add").disabled, true, "no title yet");
    await m.type("#team-new-title", "New thing");
    await m.type("#team-new-body", "Do it");
    await m.type("#team-new-role", "worker");
    m.clear();
    await m.click(m.container.find("#team-add"));
    const add = m.sent("taskAdd");
    assert.deepEqual(add.map(({ req, ...c }) => c), [{ cmd: "taskAdd", root: "/work/a", title: "New thing", spec: "Do it", role: "worker" }]);
    await m.push({ type: "teamResult", op: "taskAdd", ok: true, req: reqOf(add), task: task({ id: "T5" }), told: 1 });
    assert.equal(valueIn(m.container.find("#team-new-title")), "", "the form is emptied when it worked");
    assert.match(m.container.find("#team-said").textContent, /1 pane was told\./);
    // Show finished.
    m.clear();
    await m.click(m.container.find("#team-show-finished"));
    assert.deepEqual(m.sent("taskList").map((c) => c.state), ["all"]);
    await m.push({ type: "tasks", root: "/work/a", state: "all", tasks: [task(), task({ id: "T0", title: "Old", state: "done", moves: [] })] });
    assert.ok(m.container.find("[data-state=done]") || m.container.findAll(".team-group").some((g) => g.attributes["data-state"] === "done"));
    assert.equal(m.container.find("#team-show-finished"), null);
    // Prune, after a question.
    await m.click(m.container.find("#team-prune"));
    assert.ok(m.container.find(".confirm"));
    m.clear();
    await m.click(m.container.findAll(".dialog-btn--danger").find((b) => b.textContent === "Remove them"));
    assert.deepEqual(m.sent("taskPrune").map((c) => c.size), [30]);
    // Messages.
    await m.click(m.container.find("#team-tab-messages"));
    await m.type("#team-to", "worker");
    await m.type("#team-text", "Please start");
    m.clear();
    await m.click(m.container.find("#team-send"));
    const sent = m.sent("msgSend");
    assert.deepEqual(sent.map(({ req, ...c }) => c), [{ cmd: "msgSend", root: "/work/a", target: "worker", text: "Please start" }]);
    await m.push({ type: "teamResult", op: "msgSend", ok: true, req: reqOf(sent), sent: 2 });
    assert.match(m.container.find("#team-said").textContent, /Sent to 2 panes\./);
    assert.equal(valueIn(m.container.find("#team-text")), "");
    m.clear();
    await m.click(m.container.find(".team-seen"));
    assert.deepEqual(m.sent("msgSeen").map(({ req, ...c }) => c), [{ cmd: "msgSeen", root: "/work/a", id: "p1", msgId: 7 }]);
    assert.equal(m.container.findAll(".team-seen").length, 1, "a message of your own is not marked seen");
    await m.click(m.container.find(".team-whole"));
    assert.deepEqual(m.sent("msgList").map((c) => c.msgId), [7]);
    await m.push({ type: "messages", root: "/work/a", messages: [message({ text: "Begin and the rest of it" })] });
    assert.ok(m.container.find("#team-messages").textContent.includes("Begin and the rest of it"));
    assert.equal(m.container.find(".team-whole"), null);
  }));

test("Team: a project that can no longer be read is said, and a relay window is told it is the desk's", async () => {
  await withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush());
    await m.push({ type: "teamGone", root: "/work/a", error: "The project was closed." });
    assert.match(m.container.find("#team-gone").textContent, /The project was closed\./);
    assert.equal(m.container.find("#team-board"), null);
  });
  await withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    assert.equal(m.container.find("#team-title"), null, "no dialog");
    assert.deepEqual(m.sent("teamWatch"), []);
    assert.ok(m.container.textContent.includes(TEAM_RELAY_WORDS));
  });
});

test("Agents: the key opens the list, it is asked for, and choosing a pane reveals it and opens it", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+A");
    assert.equal(m.sent("agents").length, 1);
    assert.ok(m.container.find("#agents-title"));
    const first = m.g.nodes.find((n) => !n.shell).pane;
    await m.push({ type: "agents", items: [
      { paneId: first, tabId: "t1", root: "/work/a", project: "alpha", name: MARKUP, activity: "waiting", waiting: "Run: ls", for: "2m" },
      { paneId: "q9", tabId: "t2", root: "/work/b", project: "beta", name: "other", activity: "working", parent: first },
    ] });
    assert.equal(m.container.findAll(".ag-row").length, 2);
    assert.ok(m.text().includes(MARKUP));
    assert.equal(m.container.findAll("img").length, 0);
    assert.match(m.container.findAll(".ag-row")[0].textContent, /Run: ls · 2m/);
    assert.match(m.container.findAll(".ag-row")[1].textContent, /helper of /);
    await m.click(m.container.find("#agents-grouped"));
    assert.equal(m.container.findAll(".ag-group").length, 2);
    m.clear();
    await m.click(m.container.find("#agents-refresh"));
    assert.equal(m.sent("agents").length, 1);
    await m.push({ type: "agents", items: [{ paneId: first, tabId: "t1", root: "/work/a", project: "alpha", name: "boss", activity: "waiting" }] });
    await m.click(m.container.find(".ag-row"));
    assert.deepEqual(m.sent("revealPane"), [{ cmd: "revealPane", root: "/work/a", node: "t1", id: first }]);
    assert.equal(m.container.find("#agents-title"), null, "the dialog closes");
  }));

test("Agents: a relay window has the list too", () =>
  withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+A");
    assert.equal(m.sent("agents").length, 1);
    await m.push({ type: "agents", items: [] });
    assert.ok(m.container.find("#agents-empty"));
  }));

const viewButton = (m, id) => m.container.findAll(".tabs-view").find((b) => b.attributes["data-view"] === id);

test("Views: the Board view watches the team, draws the tasks in columns as text, opens the owner or the Team dialog, and lets go when left", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.click(viewButton(m, "board"));
    assert.deepEqual(m.sent("teamWatch").map((c) => c.root), ["/work/a"]);
    assert.equal(viewButton(m, "board").attributes["aria-pressed"], "true");
    assert.equal(viewButton(m, "tree").attributes["aria-pressed"], "false");
    await m.push(teamPush({ tasks: [task({ title: MARKUP }), task({ id: "T2", title: "Review", state: "in-review", owner: "", ownerOpen: false, ownerPane: "", branch: "", notes: 0 })] }));
    assert.equal(m.container.findAll(".board-col").length, 7 - 1, "every state but cancelled has a column");
    assert.ok(m.container.findAll(".board-col").find((c) => c.attributes["data-state"] === "assigned").textContent.includes(MARKUP));
    assert.equal(m.container.findAll("img").length, 0, "nothing an agent wrote is markup");
    // A card of an agent that is open opens it; one with no open owner opens the Team dialog.
    await m.click(m.container.findAll(".board-card").find((c) => c.attributes["data-task"] === "T2"));
    assert.ok(m.container.find("#team-title"), "the Team dialog");
    await m.click(closeBtn(m));
    m.clear();
    await m.click(viewButton(m, "tree"));
    assert.equal(m.sent("teamUnwatch").length, 1);
    assert.equal(m.container.find(".board"), null);
  }));

test("Views: Replay says what it will show and that there is nothing to replay yet", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.click(viewButton(m, "replay"));
    assert.match(m.container.find(".view--replay").textContent, /lane for each agent/);
    assert.match(m.container.find(".view--replay").textContent, /does not record that history yet/);
  }));

test("Views: the Changes view is the Changes tab as a view, for an agent that has a directory", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.click(viewButton(m, "changes"));
    assert.match(m.container.find(".view-note").textContent, /no directory/, "no agent has one yet");
    const st = stateOf(m.g);
    const first = m.g.nodes[0].pane;
    st.panes[first].cwd = "C:/work/a";
    m.clear();
    await m.push(st);
    assert.ok(m.container.find(".view-select"), "an agent to pick");
    assert.deepEqual(m.sent("changes").map((c) => c.path), ["C:/work/a"], "it asks for that tree");
  }));

test("Palette: live items sit under headings before the actions, find by name, open an agent, and nothing answers a prompt", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+K");
    const heads = () => m.container.findAll(".palette-group").map((h) => h.textContent);
    assert.deepEqual(heads(), ["Needs you", "Agents", "Views", "Actions"]);
    const names = () => m.container.findAll(".palette-row").map((r) => r.querySelector(".palette-name").textContent);
    assert.ok(names().includes("Show Board"));
    assert.ok(names().some((n) => /^Open tax: /.test(n)), "a needs-you row for the agent that waits");
    assert.ok(names().includes("Open tax"), "an agent row");
    assert.ok(!names().some((n) => /approve|deny/i.test(n.replace(/Auto-approve/i, ""))), "no row answers a permission");
    await m.type(".palette-input", "board");
    assert.equal(names()[0], "Show Board");
    await m.type(".palette-input", "open tax");
    const row = m.container.findAll(".palette-row").find((r) => r.querySelector(".palette-name").textContent === "Open tax");
    await m.click(row);
    assert.equal(m.container.find(".palette-scrim"), null, "the palette closes");
    assert.ok(m.container.find(".drawer-head, .drawer h2, .drawer"), "the drawer");
    assert.ok(!m.container.find(".drawer").attributes.hidden, "the drawer is shown");
  }));

test("Palette: choosing a view row switches the view", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+K");
    await m.type(".palette-input", "replay");
    await m.click(m.container.findAll(".palette-row").find((r) => r.querySelector(".palette-name").textContent === "Show Replay"));
    assert.ok(m.container.find(".view--replay"));
  }));

test("Palette: filterLive keeps the order of the groups, finds by name or detail and is capped", () => {
  const items = [
    { id: "a", group: "Agents", name: "Open api", detail: "worker · idle", run() {} },
    { id: "n", group: "Needs you", name: "Open tax: asks a question", detail: "which one", run() {} },
    { id: "v", group: "Views", name: "Show Board", detail: "", run() {} },
  ];
  assert.deepEqual(filterLive(items, "").map((r) => r.live.id), ["n", "a", "v"]);
  assert.deepEqual(filterLive(items, "idle").map((r) => r.live.id), ["a"], "found in the detail");
  assert.deepEqual(filterLive(items, "which").map((r) => r.live.id), ["n"]);
  assert.equal(filterLive(items, "zzz").length, 0);
  assert.deepEqual(GROUPS, ["Needs you", "Agents", "Views", "Actions"]);
  const many = Array.from({ length: 100 }, (_, i) => ({ id: "x" + i, group: "Agents", name: "Open x" + i, run() {} }));
  assert.equal(filterLive(many, "").length, 40);
});

test("Feed: it starts empty, adds an entry when a later push brings news, goes while the drawer is open, and comes back", () =>
  withPage({}, async (m) => {
    await m.connect();
    const feed = () => m.container.find(".feed");
    assert.ok(feed(), "a team with a manager has the feed");
    assert.match(feed().textContent, /Nothing has happened yet/);
    const g2 = JSON.parse(JSON.stringify(m.g));
    const helper = g2.nodes.find((n) => n.parent);
    g2.teams[0].needs = [...g2.teams[0].needs, { id: "ask:" + helper.pane, kind: "ask", pane: helper.pane, time: iso(NOW), text: "which flag store?" }];
    await m.push(g2);
    assert.match(feed().textContent, /asks: which flag store\?/);
    const entry = m.container.findAll(".feed-btn").find((b) => b.getAttribute("data-feed-pane") === helper.pane);
    assert.ok(entry, "the entry names the pane to focus");
    await m.push(g2);
    assert.equal(m.container.findAll(".feed-btn").length, 1, "the same push again adds nothing");
    // The drawer takes the place of the feed.
    const open = m.container.findAll(".stub-row").find((el) => el.getAttribute("data-pane") === helper.pane);
    await m.click(open);
    assert.equal(feed(), null, "no feed while the drawer is open");
  }));

// ------------------------------------------------------- the Team view, on a stub tree

// A stand-in for the outline: one button per node, the model it was given, and the
// pane that is open. The real tree takes its place with the same props.
const StubTree = ({ model, open, onOpen }) => html`<ul class="stub-tree" data-open=${open || ""}>
  ${(model ? model.nodes : []).map((n) => html`<li key=${n.pane}><button type="button" class="stub-row" data-pane=${n.pane} onClick=${() => onOpen(n.pane)}>${n.name}</button></li>`)}
</ul>`;
const viewButtons = (m) => m.container.findAll(".tabs-view").map((b) => b.getAttribute("data-view"));

test("Team view: with no tree the switch has no Team and the page starts on the board", () =>
  withPage({ treeView: null }, async (m) => {
    await m.connect();
    assert.deepEqual(viewButtons(m), ["board", "changes", "replay"]);
    assert.equal(m.container.find(".stub-tree"), null);
  }));

test("Team view: with a tree the switch leads with Team, the page starts there, and the goal line, the tree and the feed share the home", () =>
  withPage({ treeView: StubTree }, async (m) => {
    await m.connect();
    assert.deepEqual(viewButtons(m), ["tree", "deps", "board", "changes", "replay"]);
    const team = m.container.findAll(".tabs-view")[0];
    assert.equal(team.textContent, "Team");
    assert.equal(team.getAttribute("aria-pressed"), "true");
    const rows = m.container.findAll(".stub-row");
    assert.equal(rows.length, m.g.nodes.length, "the tree was given the model");
    const tree = m.container.find(".view-tree");
    assert.ok(tree.find(".home-col").find(".goalline"), "the goal line is at the top of the column");
    assert.match(tree.find(".feed").find(".feed-head").textContent, /Activity/, "an empty feed still has its heading");
    assert.match(tree.find(".feed").find(".feed-empty").textContent, /Nothing has happened yet/);
    assert.match(tree.find(".home-split").getAttribute("class"), /home-split--feed-empty/, "and a narrow column");
  }));

test("Team view: choosing Board and back, a row opening the drawer (which takes the feed's place), and the palette", () =>
  withPage({ treeView: StubTree }, async (m) => {
    await m.connect();
    const choose = async (id) => m.click(m.container.findAll(".tabs-view").find((b) => b.getAttribute("data-view") === id));
    await choose("board");
    assert.equal(m.container.find(".stub-tree"), null, "the tree is not drawn under another view");
    await choose("tree");
    assert.ok(m.container.find(".stub-tree"));
    await m.click(m.container.findAll(".stub-row")[1]);
    assert.equal(m.container.find(".view-tree").find(".feed"), null, "no feed while the drawer is open");
    assert.ok(m.container.find(".stub-tree").getAttribute("data-open"), "the tree is told which pane is open");
    await m.key("Ctrl+Shift+K");
    const names = m.container.findAll(".palette-row").map((r) => r.textContent);
    assert.ok(names.some((t) => t.includes("Show Team")), names.join("|"));
  }));

test("Feed: eleven entries show and the rest scroll, and it stacks under the home on a narrow window", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(join(here, "..", "src", "graph", "graph.css"), "utf8");
  assert.ok(css.includes("max-height: calc(11 * var(--feed-row)); overflow-y: auto;"));
  const app = readFileSync(join(here, "..", "src", "app.css"), "utf8");
  assert.ok(app.includes("  .home-split.home-split--feed, .home-split.home-split--feed.home-split--feed-empty { grid-template-columns: minmax(0, 1fr); }"), "below 1000 px the feed stacks under the home, empty or not");
});

test("a viewer cannot open Team: the key says so and no dialog opens; the Board has no manage button", () =>
  withPage({ helloOver: { role: "viewer" } }, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await settle();
    assert.equal(m.container.find(".dialog"), null, "the Team dialog opened for a viewer");
    assert.equal(m.sent("team").length, 0);
  }));
