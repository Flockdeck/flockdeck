package webui

import "testing"

// The Team panel: a project's roster, task board and messages, kept current by
// the server's pushes. The harness has no CSS, so what is pinned is what the
// script draws, sends and keeps; the layout rules are read from the style sheet
// where a case needs them.

const teamPrelude = paletteRun + `
const roles = [
  { id: "manager", name: "Manager", description: "Breaks a request into tasks.", spawns: ["worker", "reviewer"] },
  { id: "worker", name: "Worker" },
  { id: "reviewer", name: "Reviewer" },
];
const MOVES = {
  planned: [["assigned"], ["in-progress"], ["blocked"], ["cancelled"]],
  assigned: [["planned"], ["in-progress"], ["blocked"], ["cancelled"]],
  "in-progress": [["in-review"], ["done"], ["blocked"], ["cancelled"]],
  "in-review": [["in-progress", true], ["done"], ["blocked"], ["cancelled"]],
  blocked: [["planned"], ["assigned"], ["in-progress"], ["cancelled"]],
  done: [["in-progress", true]],
  cancelled: [["planned", true]],
};
const tk = (id, title, state, over) => Object.assign({
  id, title, state, owner: "unassigned", notes: 0, creator: { name: "the user", user: true },
  updated: "2030-01-02T10:00:00Z", canAssign: state !== "done" && state !== "cancelled",
  canEdit: state !== "done" && state !== "cancelled",
  moves: (MOVES[state] || []).map(([to, needNote]) => ({ to, needNote: !!needNote })),
}, over || {});
const msg = (id, from, to, text, over) => Object.assign({
  id, from, to: to.pane, toName: to.name, toRole: to.role || "", time: "2030-01-02T10:0" + id + ":00Z", text, read: false,
}, over || {});
const BOSS = { pane: "p1", name: "boss", role: "manager", agent: "claude", model: "opus", status: "working", activity: "working", branch: "main", open: 0 };
const DEV = { pane: "p2", name: "dev", role: "worker", agent: "claude", status: "idle", activity: "idle", branch: "feat/parser", finished: true,
  task: { id: "T1", title: "Write the parser", state: "in-progress" }, open: 1, unread: 2 };
const EYES = { pane: "p3", name: "eyes", role: "reviewer", agent: "codex", status: "exited", activity: "exited", finished: true, locked: true, open: 0 };
const team = (over) => Object.assign({
  type: "team", root: "C:/repo", project: "repo", hasManager: true,
  roster: [BOSS, DEV, EYES],
  counts: { panes: 3, live: 2, tasks: { planned: 1, "in-progress": 1, done: 1, blocked: 1 }, open: 3, messages: 2, unread: 1 },
  tasks: [
    tk("T1", "Write the parser", "in-progress", { owner: "worker dev", ownerRole: "worker", ownerPane: "p2", ownerOpen: true, branch: "feat/parser", notes: 2 }),
    tk("T2", "Document the flags", "planned", { owner: "any worker", ownerRole: "worker" }),
    tk("T3", "Fix the cache", "blocked", { owner: "worker dev", ownerPane: "p2", notes: 1 }),
    tk("T4", "Old chore", "done"),
  ],
  messages: [
    msg(2, { name: "dev", role: "worker", pane: "p2" }, { pane: "p1", name: "boss", role: "manager" }, "Which flag wins?"),
    msg(1, { name: "the user", user: true }, { pane: "p2", name: "dev", role: "worker" }, "Start on T1.", { read: true }),
  ],
}, over || {});
const withRoles = () => fixture({ agents: catalog({ roles }) });
const opened = (over) => {
  h.hello();
  h.recv(withRoles());
  paletteRun("roster");
  h.recv(team(over));
};
const sent = (cmd) => h.commands().filter((c) => c.cmd === cmd);
// The harness keeps an element in its id index after it has left the page, so
// a case that asks whether something is gone asks live().
const live = (id) => { const n = h.$(id); return n && n.isConnected ? n : null; };
const tab = (id) => h.$("team-tab-" + id);
const texts = (nodes) => nodes.map((n) => n.textContent);
`

func TestTheTeamPanelOpensFromThePaletteAndWatchesTheProject(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
h.hello();
h.recv(withRoles());
paletteRun("roster");
assert.ok(!live("overlay").hidden, "the palette did not open the panel");
assert.strictEqual(h.$("overlay-title").textContent, "Team");
assert.deepStrictEqual(h.commands().pop(), { cmd: "teamWatch", root: "C:/repo" });
assert.ok(h.$("overlay-body").textContent.includes("Reading the team"), "no waiting text before the first picture");

// It is a sheet, as the Agents overview is.
assert.strictEqual(h.$("overlay-panel").getAttribute("data-present"), "sheet");

h.recv(team());
assert.strictEqual(h.$("overlay-scope").textContent, "repo", "got: " + h.$("overlay-scope").textContent);
const tabs = [...h.$("overlay-tabs").children];
assert.deepStrictEqual(texts(tabs), ["Roster3", "Tasks3", "Messages1"], "tab labels with counts");
assert.deepStrictEqual(tabs.map((b) => b.getAttribute("role")), ["tab", "tab", "tab"]);
assert.strictEqual(h.$("overlay-body").getAttribute("role"), "tabpanel");
assert.strictEqual(h.$("overlay-body").getAttribute("aria-labelledby"), "team-tab-roster");
assert.strictEqual(tab("roster").getAttribute("aria-selected"), "true");

// Putting it away stops the pushes.
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden);
assert.deepStrictEqual(h.commands().pop(), { cmd: "teamUnwatch" });
`)
}

func TestTheRosterListsEveryAgentPane(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
const rows = [...h.$("overlay-body").querySelectorAll("div.team-pane")];
assert.strictEqual(rows.length, 3);
const dev = h.$("team-pane-p2");
const text = dev.textContent;
for (const want of ["Worker", "dev", "claude", "feat/parser", "T1", "In progress", "Write the parser", "idle", "2 unread messages"]) {
  assert.ok(text.includes(want), want + " is missing from " + text);
}
assert.ok(h.$("team-pane-p1").textContent.includes("no task"), "a pane without a task says so");
assert.ok(h.$("team-pane-p1").textContent.includes("claude · opus"));
assert.ok(h.$("team-pane-p1").querySelector("span.agent-role"), "no role tag");

// A click goes to the pane and puts the panel away.
h.click(dev);
assert.deepStrictEqual(h.commands().filter((c) => c.cmd === "revealPane").pop(), { cmd: "revealPane", id: "p2" });
assert.ok(h.$("overlay").hidden);
`)
}

func TestARosterRowClosesOnlyAFinishedUnlockedPane(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
assert.ok(h.$("team-close-p1").disabled, "a working pane can be closed from here");
assert.ok(h.$("team-close-p3").disabled, "a locked pane can be closed from here");
assert.ok(!live("team-close-p2").disabled, "a finished pane cannot be closed");
h.click(h.$("team-close-p2"));
assert.deepStrictEqual(sent("closePane"), [{ cmd: "closePane", id: "p2" }]);
assert.strictEqual(sent("revealPane").length, 0, "closing a pane also went to it");
assert.ok(!live("overlay").hidden);
// Closing every finished pane of the project is one button, and counts them.
assert.strictEqual(h.$("team-close-finished").textContent, "Close finished (1)");
h.click(h.$("team-close-finished"));
assert.strictEqual(sent("closePane").length, 2);
// A key on the nested button does not also go to the pane.
h.$("team-close-p2").onkeydown({ key: "Enter", stopPropagation() { this.stopped = true; } });
`)
}

func TestARosterStartsAnAgentInARoleThroughThePicker(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(h.$("team-start-agent"));
assert.ok(h.$("agent-role"), "the picker has no Role control");
h.$("agent-role").value = "worker";
h.$("agent-role").onchange();
h.$("agent-role-task").value = "add tests";
h.$("agent-role-task").oninput();
h.click(h.$("agent-role-start"));
const start = sent("startAgent").pop();
assert.strictEqual(start.role, "worker");
assert.strictEqual(start.task, "add tests");
// The started agent is shown, and the panel is what comes back.
h.recv({ type: "agentStarted", paneId: "p9", id: start.id });
assert.strictEqual(h.$("overlay-title").textContent, "Team", "got: " + h.$("overlay-title").textContent);
assert.ok(h.$("team-start-agent"));
`)
}

func TestARosterWithNoAgentsSaysWhatATeamIsAndHowToStartOne(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened({ roster: [], hasManager: false, counts: { panes: 0, live: 0, tasks: {}, open: 0, messages: 0, unread: 0 }, tasks: [], messages: [] });
const body = h.$("overlay-body").textContent;
assert.ok(body.includes("agents in roles") && body.includes("task board") && body.includes("message each other"), "got: " + body);
h.click(h.$("team-start-team"));
assert.strictEqual(h.$("overlay-title").textContent, "Start a team");
// Esc comes back to the panel.
h.key({ key: "Escape" });
assert.strictEqual(h.$("overlay-title").textContent, "Team");
`)
}

func TestARosterWithNoManagerOffersToStartATeam(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened({ hasManager: false });
assert.ok(h.$("team-start-team"));
h.recv(team({ hasManager: true }));
assert.ok(!live("team-start-team"), "Start a team is offered with a manager running");
`)
}

func TestTheBoardGroupsTasksByStateInColumnsWithCards(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("tasks"));
const cols = [...h.$("team-board").querySelectorAll("div.team-col")];
assert.deepStrictEqual(cols.map((c) => c.dataset.state), ["planned", "assigned", "in-progress", "in-review", "done"]);
for (const c of cols) {
  assert.strictEqual(c.getAttribute("role"), "group");
  const head = h.$(c.getAttribute("aria-labelledby"));
  assert.ok(head && head.tagName === "H3", "a column is not named by its heading");
}
const heading = (state) => h.$("team-col-" + state).textContent;
assert.strictEqual(heading("in-progress"), "In progress1");
assert.strictEqual(heading("done"), "Done1");
assert.strictEqual(heading("in-review"), "In review0");

const card = h.$("team-task-T1");
for (const want of ["T1", "Write the parser", "worker dev", "feat/parser", "2 notes"]) {
  assert.ok(card.textContent.includes(want), want + " is missing from " + card.textContent);
}
assert.ok(card.getAttribute("aria-label").includes("T1, Write the parser, In progress, worker dev, 2 notes"), card.getAttribute("aria-label"));
assert.strictEqual(card.getAttribute("role"), "button");
assert.ok(h.$("team-task-T2").textContent.includes("any worker"));
assert.ok(h.$("team-task-T4").parentElement.parentElement.dataset.state === "done");

// Blocked and cancelled are collapsed.
assert.ok(!live("team-task-T3"), "a blocked task is on the board before it is asked for");
const toggle = h.$("team-side-toggle");
assert.ok(toggle.textContent.includes("1 blocked, 0 cancelled"), toggle.textContent);
assert.strictEqual(toggle.getAttribute("aria-expanded"), "false");
h.click(toggle);
assert.ok(h.$("team-task-T3"), "the blocked task did not appear");
assert.strictEqual(h.$("team-side-toggle").getAttribute("aria-expanded"), "true");
h.click(h.$("team-side-toggle"));
assert.ok(!live("team-task-T3"));
`)
}

func TestTheBoardIsAListGroupedByStateOnANarrowWindow(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
const css = h.css();
// One column by default; five side by side from 900px, and the blocked and
// cancelled pair from there as two.
assert.ok(/\.team-board \{[^}]*grid-template-columns: minmax\(0, 1fr\);/.test(css), "the board is not one column by default");
assert.ok(/@media \(min-width: 900px\) \{[^}]*\.team-board \{ grid-template-columns: repeat\(5/.test(css), "the five columns are not for a wide window");
assert.ok(/#overlay-panel:where\(\[data-present="sheet"\]\),\s*#overlay-panel:where\(\[data-present="sheet"\]\[data-size="wide"\]\) \{ width: 100%; \}/.test(css), "the sheet is not the whole width at 640px");
`)
}

func TestArrowKeysWalkTheCardsAndTheColumns(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened({ tasks: [
  tk("T1", "a", "planned"), tk("T2", "b", "planned"), tk("T3", "c", "in-progress"), tk("T4", "d", "done"), tk("T5", "e", "done"),
], counts: { panes: 3, live: 2, tasks: { planned: 2, "in-progress": 1, done: 2 }, open: 3, messages: 0, unread: 0 } });
h.click(tab("tasks"));
h.$("team-task-T1").focus();
h.key({ key: "ArrowDown" });
assert.strictEqual(h.doc.activeElement.id, "team-task-T2");
h.key({ key: "ArrowUp" });
assert.strictEqual(h.doc.activeElement.id, "team-task-T1");
// Right goes to the next column that has a card, at the same height or the last.
h.key({ key: "ArrowRight" });
assert.strictEqual(h.doc.activeElement.id, "team-task-T3");
h.key({ key: "ArrowRight" });
assert.strictEqual(h.doc.activeElement.id, "team-task-T4");
h.key({ key: "ArrowLeft" });
assert.strictEqual(h.doc.activeElement.id, "team-task-T3");
h.key({ key: "ArrowLeft" });
assert.strictEqual(h.doc.activeElement.id, "team-task-T1");
// Enter opens the task.
h.key({ key: "Enter" });
assert.deepStrictEqual(sent("taskGet").pop(), { cmd: "taskGet", root: "C:/repo", id: "T1", req: "detail" });
`)
}

func TestACardsMoveMenuOffersOnlyTheMovesTheRulesAllow(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened({ tasks: [
  tk("T1", "in review", "in-review"), tk("T2", "done", "done"), tk("T3", "planned", "planned"),
  tk("T4", "stuck", "in-progress", { moves: [] }),
] });
h.click(tab("tasks"));
const menuOf = (id) => {
  h.click(h.$("team-move-" + id));
  const items = [...h.$("row-menu").querySelectorAll("button")];
  return items.map((b) => b.textContent.replace(/needs a note/, " [note]") + (b.disabled ? " (off)" : ""));
};
assert.deepStrictEqual(menuOf("T1"), ["Move to in progress [note] (off)", "Move to done", "Move to blocked", "Move to cancelled"]);
h.key({ key: "Escape" });
assert.ok(!live("row-menu"), "Escape left the menu open");
assert.ok(!live("overlay").hidden, "Escape closed the panel with a menu open");
assert.deepStrictEqual(menuOf("T2"), ["Move to in progress [note] (off)"]);
h.key({ key: "Escape" });
assert.deepStrictEqual(menuOf("T3"), ["Move to assigned", "Move to in progress", "Move to blocked", "Move to cancelled"]);
// A task the person has no move for has no Move button at all.
h.key({ key: "Escape" });
assert.ok(!live("team-move-T4"));

// A move sends the state and nothing else.
h.click(h.$("team-move-T3"));
const item = [...h.$("row-menu").querySelectorAll("button")].find((b) => b.textContent.includes("blocked"));
h.click(item);
const cmd = sent("taskStatus").pop();
assert.strictEqual(cmd.id, "T3");
assert.strictEqual(cmd.state, "blocked");
assert.strictEqual(cmd.text, "");
assert.strictEqual(cmd.root, "C:/repo");
assert.ok(/^team-/.test(cmd.req));
`)
}

func TestAnAddedTaskIsSentWithItsFieldsAndAnErrorIsShownInline(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("tasks"));
assert.ok(!live("team-add"));
h.click(h.$("team-add-open"));
assert.ok(h.$("team-add"));
assert.ok(h.doc.activeElement === h.$("team-title"), "the form did not take the keyboard");
const roleOptions = h.$("team-role").querySelectorAll("option").map((o) => o.textContent);
assert.deepStrictEqual(roleOptions, ["Any role", "Manager", "Worker", "Reviewer"]);
// A title is needed before anything is sent.
const before = h.commands().length;
h.click(h.$("team-add-go"));
assert.strictEqual(h.commands().length, before);
assert.ok(h.$("team-err-add").textContent.includes("title"), h.$("team-err-add").textContent);
assert.strictEqual(h.$("team-err-add").getAttribute("role"), "alert");

h.$("team-title").value = "Write docs";
h.$("team-title").oninput();
h.$("team-body").value = "for the flags";
h.$("team-body").oninput();
h.$("team-role").value = "worker";
h.$("team-role").onchange();
h.$("team-branch-new").value = "docs/flags";
h.$("team-branch-new").oninput();
h.click(h.$("team-add-go"));
const cmd = sent("taskAdd").pop();
assert.deepStrictEqual(Object.assign({}, cmd, { req: "x" }),
  { cmd: "taskAdd", root: "C:/repo", req: "x", title: "Write docs", spec: "for the flags", role: "worker", branch: "docs/flags" });
// Nothing is sent twice while the answer is out.
h.click(h.$("team-add-go"));
assert.strictEqual(sent("taskAdd").length, 1);

// The server's refusal is shown beside the form, with what was typed kept.
h.recv({ type: "teamResult", req: cmd.req, op: "taskAdd", ok: false, error: "the title is longer than 200 characters" });
assert.ok(h.$("team-err-add").textContent.includes("longer than 200"), h.$("team-err-add").textContent);
assert.ok(!live("team-err-add").hidden);
assert.strictEqual(h.$("team-title").value, "Write docs");
// Accepted: the form closes and is empty next time.
h.click(h.$("team-add-go"));
const again = sent("taskAdd").pop();
h.recv({ type: "teamResult", req: again.req, op: "taskAdd", ok: true, task: tk("T5", "Write docs", "planned") });
assert.ok(!live("team-add"));
h.click(h.$("team-add-open"));
assert.strictEqual(h.$("team-title").value, "");
`)
}

func TestEscapeClosesTheAddFormThenTheOpenTaskThenThePanel(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("tasks"));
h.click(h.$("team-add-open"));
h.key({ key: "Escape" });
assert.ok(!live("team-add"), "Escape left the form open");
assert.ok(!live("overlay").hidden);
h.$("team-task-T1").focus();
h.click(h.$("team-task-T1"));
assert.ok(h.$("team-detail"));
h.$("team-note").focus();
h.key({ key: "Escape" });
assert.ok(!live("team-detail"), "Escape left the task open");
assert.ok(h.$("team-board"), "the board did not come back");
assert.ok(!live("overlay").hidden, "Escape closed the panel with a task open");
assert.strictEqual(h.doc.activeElement.id, "team-task-T1", "the keyboard did not go back to the card");
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden);
`)
}

const teamDetail = `
const detail = (over) => tk("T1", "Write the parser", "in-progress", Object.assign({
  owner: "worker dev", ownerRole: "worker", ownerPane: "p2", ownerOpen: true, branch: "feat/parser", notes: 2,
  body: "Parse the flags.\nSecond line.", baton: "", created: "2030-01-01T09:00:00Z",
  noteList: [
    { author: { name: "dev", role: "worker", pane: "p2" }, time: "2030-01-01T10:00:00Z", text: "Started." },
    { author: { name: "the user", user: true }, time: "2030-01-01T11:00:00Z", text: "Use the stdlib." },
  ],
  historyList: [
    { actor: { name: "the user", user: true }, time: "2030-01-01T09:00:00Z", what: "added the task" },
    { actor: { name: "boss", role: "manager", pane: "p1" }, time: "2030-01-01T09:30:00Z", what: "assigned the task to worker dev" },
  ],
}, over || {}));
const openDetail = (over) => {
  h.click(tab("tasks"));
  h.click(h.$("team-task-T1"));
  h.recv({ type: "teamResult", req: "detail", op: "taskGet", ok: true, task: detail(over) });
};
`

func TestAnOpenTaskShowsItsDetailNotesAndHistory(t *testing.T) {
	runFrontEnd(t, teamPrelude+teamDetail+`
opened();
h.click(tab("tasks"));
h.click(h.$("team-task-T1"));
assert.deepStrictEqual(sent("taskGet").pop(), { cmd: "taskGet", root: "C:/repo", id: "T1", req: "detail" });
// Until the whole task comes the summary stands in.
assert.ok(h.$("team-detail").textContent.includes("Write the parser"));
assert.ok(h.$("team-notes").textContent.includes("Reading the notes"));
h.recv({ type: "teamResult", req: "detail", op: "taskGet", ok: true, task: detail() });

const text = h.$("team-detail").textContent;
for (const want of ["T1", "Write the parser", "In progress", "worker dev", "feat/parser", "Added by", "you", "Parse the flags.", "Notes (2)", "History"]) {
  assert.ok(text.includes(want), want + " is missing from " + text);
}
const notes = [...h.$("team-notes").children];
assert.strictEqual(notes.length, 2);
assert.ok(notes[0].textContent.includes("dev (Worker)") && notes[0].textContent.includes("Started."), notes[0].textContent);
assert.ok(notes[1].textContent.includes("you") && !notes[1].textContent.includes("the user"), "the user's own note is not by 'you': " + notes[1].textContent);
assert.ok(h.$("team-detail").textContent.includes("you added the task"));
assert.ok(h.$("team-detail").textContent.includes("boss (Manager) assigned the task to worker dev"));
// The owner's pane is one click away.
h.click(h.$("team-owner-go"));
assert.deepStrictEqual(sent("revealPane").pop(), { cmd: "revealPane", id: "p2" });
`)
}

func TestTheDetailStepsAssignBranchNoteAndMoveAreSentAsTheRulesAllow(t *testing.T) {
	runFrontEnd(t, teamPrelude+teamDetail+`
opened();
openDetail();

// Assign: roles and the project's live panes; a pane that has exited is not offered.
const options = h.$("team-assign").querySelectorAll("option").map((o) => o.textContent);
assert.deepStrictEqual(options, ["Choose a role or a pane", "Any Manager", "Any Worker", "Any Reviewer", "boss (Manager)", "dev (Worker)"]);
h.click(h.$("team-assign-go"));
assert.ok(h.$("team-err-detail").textContent.includes("Choose who"), "no word on choosing");
h.$("team-assign").value = "p2";
h.$("team-assign").onchange();
h.click(h.$("team-assign-go"));
const assign = sent("taskAssign").pop();
assert.deepStrictEqual(Object.assign({}, assign, { req: "x" }), { cmd: "taskAssign", root: "C:/repo", req: "x", id: "T1", target: "p2" });
h.recv({ type: "teamResult", req: assign.req, op: "taskAssign", ok: true, task: detail({ notes: 2 }) });

// Branch.
h.$("team-branch").value = "feat/other";
h.$("team-branch").oninput();
h.click(h.$("team-branch-go"));
const br = sent("taskBranch").pop();
assert.strictEqual(br.branch, "feat/other");
assert.strictEqual(br.id, "T1");
h.recv({ type: "teamResult", req: br.req, op: "taskBranch", ok: true, task: detail({ branch: "feat/other" }) });
assert.ok(h.$("team-detail").textContent.includes("feat/other"));

// A note.
assert.ok(h.$("team-note-go").disabled, "an empty note can be added");
h.$("team-note").value = "looks right";
h.$("team-note").oninput();
assert.ok(!live("team-note-go").disabled);
h.click(h.$("team-note-go"));
const note = sent("taskNote").pop();
assert.strictEqual(note.text, "looks right");
h.recv({ type: "teamResult", req: note.req, op: "taskNote", ok: true, task: detail({ notes: 3 }) });
assert.strictEqual(h.$("team-note").value, "", "the note box kept a note that was added");

// Moves: the ones the server says, with a note-needing one off until there is a note.
h.recv(team({ tasks: team().tasks.map((t) => t.id === "T1" ? tk("T1", "Write the parser", "in-review", { owner: "worker dev", ownerPane: "p2", notes: 2 }) : t) }));
h.recv({ type: "teamResult", req: "detail", op: "taskGet", ok: true, task: detail({ state: "in-review", moves: tk("x", "x", "in-review").moves, notes: 2 }) });
h.click(h.$("team-move-go"));
let items = [...h.$("row-menu").querySelectorAll("button")];
assert.deepStrictEqual(items.map((b) => b.textContent.replace("needs a note", " [note]") + (b.disabled ? " (off)" : "")),
  ["Move to in progress [note] (off)", "Move to done", "Move to blocked", "Move to cancelled"]);
h.key({ key: "Escape" });
h.$("team-note").value = "missing a test";
h.$("team-note").oninput();
h.click(h.$("team-move-go"));
items = [...h.$("row-menu").querySelectorAll("button")];
assert.ok(!items[0].disabled, "a move with its note is still off");
h.click(items[0]);
const move = sent("taskStatus").pop();
assert.strictEqual(move.state, "in-progress");
assert.strictEqual(move.text, "missing a test");
// Refused by the server: shown beside the box, and the note is not lost.
h.recv({ type: "teamResult", req: move.req, op: "taskStatus", ok: false, error: "moving T1 from in-review to in-progress is for a manager" });
assert.ok(h.$("team-err-detail").textContent.includes("is for a manager"));
assert.strictEqual(h.$("team-note").value, "missing a test");
`)
}

func TestAFinishedTaskCannotBeAssignedOrEdited(t *testing.T) {
	runFrontEnd(t, teamPrelude+teamDetail+`
opened();
openDetail({ state: "done", canAssign: false, canEdit: false, moves: tk("x", "x", "done").moves });
assert.ok(h.$("team-assign").disabled && h.$("team-assign-go").disabled);
assert.ok(h.$("team-branch").disabled && h.$("team-branch-go").disabled);
// The note box still works on any task.
assert.ok(!live("team-note").disabled);
`)
}

func TestATaskThatLeavesTheBoardWhileOpenSaysSo(t *testing.T) {
	runFrontEnd(t, teamPrelude+teamDetail+`
opened();
openDetail();
h.recv(team({ tasks: [], counts: { panes: 3, live: 2, tasks: {}, open: 0, messages: 0, unread: 0 } }));
// The detail is still what was open; with the task gone from the board the
// last whole copy stays, and the back button is there.
assert.ok(h.$("team-back"));
h.click(h.$("team-back"));
assert.ok(h.$("overlay-body").textContent.includes("No tasks on this project's board"), h.$("overlay-body").textContent);
`)
}

func TestPruneAsksFirstAndReportsWhatItRemoved(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("tasks"));
h.click(h.$("team-prune"));
assert.strictEqual(h.$("overlay-title").textContent, "Prune finished tasks?");
assert.strictEqual(sent("taskPrune").length, 0, "pruned before it was confirmed");
const yes = h.$("overlay-body").querySelectorAll("button").find((b) => b.textContent === "Prune");
h.click(yes);
const cmd = sent("taskPrune").pop();
assert.strictEqual(cmd.size, 30);
h.recv({ type: "teamResult", req: cmd.req, op: "taskPrune", ok: true, pruned: 3 });
assert.ok(h.$("notice").textContent.includes("Removed 3 finished tasks"), h.$("notice").textContent);
assert.strictEqual(h.$("overlay-title").textContent, "Team");
`)
}

func TestOlderFinishedTasksAreShownOnRequest(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened({ finishedHidden: 2 });
h.click(tab("tasks"));
assert.ok(h.$("team-show-all").textContent.includes("2 older finished tasks"));
h.click(h.$("team-show-all"));
assert.deepStrictEqual(sent("taskList").pop(), { cmd: "taskList", root: "C:/repo", state: "all" });
h.recv({ type: "tasks", root: "C:/repo", state: "all", tasks: [tk("T9", "Ancient", "done"), tk("T8", "Older", "cancelled")] });
assert.ok(h.$("team-task-T9"), "an older done task is not on the board");
assert.ok(!live("team-show-all"));
`)
}

func TestTheMessagesAreListedNewestFirstAsPlainText(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
const hostile = '<img src=x onerror="alert(1)"> **not bold** <script>alert(2)</script>';
opened({ messages: [
  msg(3, { name: "dev", role: "worker", pane: "p2" }, { pane: "p1", name: "boss", role: "manager" }, hostile),
  msg(2, { name: "flockdeck" }, { pane: "p2", name: "dev", role: "worker" }, "task T1 assigned to you: Write the parser", { system: true }),
  msg(1, { name: "the user", user: true }, { pane: "p2", name: "dev", role: "worker" }, "Start on T1.", { read: true }),
] });
h.click(tab("messages"));
const items = [...h.$("team-msgs").children];
assert.deepStrictEqual(items.map((i) => i.id), ["team-msg-3", "team-msg-2", "team-msg-1"]);
// The text is text: no element came out of it.
const first = items[0];
assert.strictEqual(first.querySelectorAll("img, script").length, 0, "message text became markup");
assert.ok(first.querySelector("div.team-msg-text").textContent === hostile, "the text was changed");
assert.ok(first.textContent.includes("dev (Worker)") && first.textContent.includes("to boss (Manager)") && first.textContent.includes("unread"));
assert.ok(items[1].textContent.includes("Flockdeck"), items[1].textContent);
assert.ok(items[2].textContent.includes("you") && items[2].textContent.includes("to dev (Worker)") && items[2].textContent.includes("read") && !items[2].textContent.includes("unread"));
assert.ok(items[0].classList.contains("unread") && !items[2].classList.contains("unread"));
// Arrow keys walk the list.
items[0].focus();
h.key({ key: "ArrowDown" });
assert.strictEqual(h.doc.activeElement.id, "team-msg-2");
h.key({ key: "End" });
assert.strictEqual(h.doc.activeElement.id, "team-msg-1");
`)
}

func TestTheMessagesFilterByPaneOrRole(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("messages"));
const options = h.$("team-filter").querySelectorAll("option").map((o) => o.textContent);
assert.deepStrictEqual(options, ["All messages", "Role: Manager", "Role: Worker", "Role: Reviewer",
  "Pane: boss (Manager)", "Pane: dev (Worker)", "Pane: eyes (Reviewer)"]);
const ids = () => [...h.$("team-msgs").children].map((i) => i.id);
assert.deepStrictEqual(ids(), ["team-msg-2", "team-msg-1"]);
const pick = (v) => { h.$("team-filter").value = v; h.$("team-filter").onchange(); };
pick("pane:p1");
assert.deepStrictEqual(ids(), ["team-msg-2"], "to the manager");
pick("role:worker");
assert.deepStrictEqual(ids(), ["team-msg-2", "team-msg-1"], "from or to a worker");
pick("pane:p3");
assert.ok(h.$("overlay-body").textContent.includes("No message matches"));
pick("");
assert.deepStrictEqual(ids(), ["team-msg-2", "team-msg-1"]);
`)
}

func TestComposingAMessageSendsItAsTheUserWithinTheCap(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("messages"));
const to = h.$("team-to").querySelectorAll("option").map((o) => o.textContent);
// Roles with a live pane, then the live panes; the exited one is not offered.
assert.deepStrictEqual(to, ["Choose a pane or a role", "Every Manager", "Every Worker", "boss (Manager)", "dev (Worker)"]);
assert.ok(h.$("team-send").disabled, "an empty message can be sent");
h.$("team-compose").value = "hello";
h.$("team-compose").oninput();
assert.ok(!live("team-send").disabled);
assert.strictEqual(h.$("team-compose-count").textContent, "5 / 8192 bytes");
// A target is needed.
h.click(h.$("team-send"));
assert.strictEqual(sent("msgSend").length, 0);
assert.ok(h.$("team-err-compose").textContent.includes("Choose who"));

h.$("team-to").value = "role:worker";
h.$("team-to").onchange();
h.click(h.$("team-send"));
const cmd = sent("msgSend").pop();
assert.deepStrictEqual(Object.assign({}, cmd, { req: "x" }), { cmd: "msgSend", root: "C:/repo", req: "x", target: "role:worker", text: "hello" });
h.recv({ type: "teamResult", req: cmd.req, op: "msgSend", ok: true, sent: 1 });
assert.strictEqual(h.$("team-compose").value, "", "a sent message stayed in the box");
assert.ok(h.$("notice").textContent.includes("Message sent to 1 pane"));

// Multi-byte text is counted in bytes, and over the cap nothing sends.
h.$("team-compose").value = "é".repeat(4097);
h.$("team-compose").oninput();
assert.strictEqual(h.$("team-compose-count").textContent, "8194 / 8192 bytes");
assert.ok(h.$("team-compose-count").classList.contains("over"));
assert.ok(h.$("team-send").disabled, "a message over the cap can be sent");
h.$("team-compose").value = "é".repeat(4096);
h.$("team-compose").oninput();
assert.ok(!live("team-send").disabled, "a message of exactly the cap cannot be sent");

// The server's refusal is shown inline and the text stays.
h.click(h.$("team-send"));
const again = sent("msgSend").pop();
h.recv({ type: "teamResult", req: again.req, op: "msgSend", ok: false, error: "no pane, name or role matches \"x\" in this project" });
assert.ok(h.$("team-err-compose").textContent.includes("no pane, name or role"));
assert.strictEqual(h.$("team-compose").value.length, 4096);
// Ctrl+Enter sends.
h.$("team-compose").onkeydown({ key: "Enter", ctrlKey: true, preventDefault() {} });
assert.strictEqual(sent("msgSend").length, 3);
`)
}

func TestAMessageIsMarkedReadAndACutOneShownWhole(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened({ messages: [
  msg(2, { name: "dev", role: "worker", pane: "p2" }, { pane: "p1", name: "boss", role: "manager" }, "The start of a long one", { cut: true }),
  msg(1, { name: "dev", role: "worker", pane: "p2" }, { pane: "p1", name: "boss", role: "manager" }, "Seen", { read: true }),
] });
h.click(tab("messages"));
assert.ok(!live("team-msg-read-1"), "a read message offers Mark read");
h.click(h.$("team-msg-read-2"));
assert.deepStrictEqual(Object.assign({}, sent("msgRead").pop(), { req: "x" }), { cmd: "msgRead", root: "C:/repo", req: "x", id: "p1", msgId: 2 });
h.click(h.$("team-msg-more-2"));
assert.deepStrictEqual(sent("msgList").pop(), { cmd: "msgList", root: "C:/repo", msgId: 2 });
h.recv({ type: "messages", root: "C:/repo", messages: [Object.assign(msg(2, { name: "dev", role: "worker", pane: "p2" }, { pane: "p1", name: "boss" }, "The start of a long one, and the rest of it."), { cut: false })] });
assert.ok(h.$("team-msg-2").textContent.includes("and the rest of it."));
assert.ok(!live("team-msg-more-2"), "Show all stayed after the whole text came");
`)
}

func TestAPushUpdatesInPlaceKeepingTheOpenTaskTheDraftsTheScrollAndTheKeyboard(t *testing.T) {
	runFrontEnd(t, teamPrelude+teamDetail+`
opened();
openDetail();
const body = h.$("overlay-body");
body.scrollTop = 240;
h.$("team-note").focus();
h.$("team-note").value = "half a thought";
h.$("team-note").oninput();
h.$("team-note").setSelectionRange(4, 4);
const detailRequests = sent("taskGet").length;

// A message arrives and a pane's status changes: the picture is pushed.
const next = team({
  roster: [BOSS, Object.assign({}, DEV, { status: "working", activity: "working" }), EYES],
  messages: [msg(3, { name: "dev", role: "worker", pane: "p2" }, { pane: "p1", name: "boss", role: "manager" }, "Done with T1")].concat(team().messages),
  counts: { panes: 3, live: 2, tasks: { planned: 1, "in-progress": 1, done: 1, blocked: 1 }, open: 3, messages: 3, unread: 2 },
});
h.recv(next);
assert.ok(h.$("team-detail"), "the open task was closed by a push");
assert.strictEqual(h.$("team-note").value, "half a thought", "the draft was lost");
assert.ok(h.doc.activeElement === h.$("team-note"), "the keyboard left the note box: " + h.doc.activeElement.id);
assert.strictEqual(h.doc.activeElement.selectionStart, 4, "the caret moved");
assert.strictEqual(body.scrollTop, 240, "the scroll position was lost");
assert.deepStrictEqual(texts([...h.$("overlay-tabs").children]), ["Roster3", "Tasks3", "Messages2"], "the tab counts did not follow");
assert.strictEqual(sent("taskGet").length, detailRequests, "an unchanged task was asked for again");

// The task itself changing is asked for in full, and shown without closing.
const moved = team().tasks.map((t) => t.id === "T1" ? Object.assign({}, t, { notes: 3, updated: "2030-01-02T11:00:00Z" }) : t);
h.recv(Object.assign({}, next, { tasks: moved }));
assert.strictEqual(sent("taskGet").length, detailRequests + 1, "a changed task was not asked for");
// Asked for once, however many pushes come before the answer.
h.recv(Object.assign({}, next, { tasks: moved }));
assert.strictEqual(sent("taskGet").length, detailRequests + 1, "the same change was asked for twice");
h.recv({ type: "teamResult", req: "detail", op: "taskGet", ok: true, task: detail({ notes: 3, updated: "2030-01-02T11:00:00Z",
  noteList: detail().noteList.concat([{ author: { name: "dev", role: "worker", pane: "p2" }, time: "2030-01-02T11:00:00Z", text: "A third note." }]) }) });
assert.strictEqual(h.$("team-notes").children.length, 3);
assert.ok(h.$("team-note").value === "half a thought" && h.doc.activeElement === h.$("team-note"));

// On the roster and the messages, too: the tab and the compose text stay.
h.click(tab("messages"));
h.$("team-compose").value = "typing";
h.$("team-compose").oninput();
h.$("team-compose").focus();
h.recv(Object.assign({}, next, { messages: next.messages.concat([msg(4, { name: "eyes", role: "reviewer", pane: "p3" }, { pane: "p1", name: "boss", role: "manager" }, "one more")]) }));
assert.strictEqual(h.$("team-compose").value, "typing");
assert.ok(h.doc.activeElement === h.$("team-compose"));
assert.strictEqual(h.$("team-msgs").children.length, 4);
assert.strictEqual(tab("messages").getAttribute("aria-selected"), "true");
`)
}

func TestAPushWhileAMenuIsOpenWaitsForTheMenu(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("tasks"));
h.click(h.$("team-move-T2"));
assert.ok(h.$("row-menu"));
h.recv(team({ tasks: team().tasks.concat([tk("T7", "A new one", "planned")]) }));
assert.ok(h.$("row-menu"), "a push took the menu away");
assert.ok(!live("team-task-T7"), "drawn under an open menu");
h.key({ key: "Escape" });
await h.waitFor(() => h.$("team-task-T7"), 3000);
`)
}

func TestAPushReportsWhatWasNewToAScreenReaderAndNothingElse(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
const said = () => [...h.$("announcer").children].map((n) => n.textContent);
const before = said().length;
// The same picture again is not news, nor is a status change.
h.recv(team());
h.recv(team({ roster: [BOSS, Object.assign({}, DEV, { status: "working", activity: "working" }), EYES] }));
assert.strictEqual(said().length, before, "said: " + JSON.stringify(said()));
// A new message from an agent is; a new message the user wrote is not.
const mine = msg(5, { name: "the user", user: true }, { pane: "p2", name: "dev", role: "worker" }, "hi");
h.recv(team({ messages: [mine].concat(team().messages) }));
assert.strictEqual(said().length, before);
const theirs = msg(6, { name: "dev", role: "worker", pane: "p2" }, { pane: "p1", name: "boss", role: "manager" }, "need help");
h.recv(team({ messages: [theirs, mine].concat(team().messages) }));
assert.ok(said().pop().includes("New message from dev (Worker)"), JSON.stringify(said()));
// A task that moved is.
h.recv(team({ messages: [theirs, mine].concat(team().messages), tasks: team().tasks.map((t) => t.id === "T2" ? tk("T2", "Document the flags", "in-review") : t) }));
assert.ok(said().pop().includes("T2 is now in review"), JSON.stringify(said()));
`)
}

func TestTheUnreadCountAndRosterShowInTheTabLabels(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
const labels = texts([...h.$("overlay-tabs").children]);
assert.deepStrictEqual(labels, ["Roster3", "Tasks3", "Messages1"]);
// The arrow keys walk the tabs, and the keyboard stays on the tab.
tab("roster").focus();
h.key({ key: "ArrowRight" });
assert.strictEqual(tab("tasks").getAttribute("aria-selected"), "true");
assert.ok(h.doc.activeElement === tab("tasks"), "the keyboard left the tabs: " + h.doc.activeElement.id);
h.key({ key: "ArrowRight" });
h.key({ key: "ArrowRight" });
assert.strictEqual(tab("roster").getAttribute("aria-selected"), "true");
assert.ok(h.doc.activeElement === tab("roster"));
`)
}

func TestAWindowThroughTheRelayDoesNotOpenThePanel(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true });
h.recv(withRoles());
paletteRun("roster");
assert.ok(h.$("overlay").hidden, "the panel opened in a window reached through the relay");
assert.ok(h.$("notice").textContent.includes("machine Flockdeck runs on"), h.$("notice").textContent);
assert.strictEqual(sent("teamWatch").length, 0);
`)
}

func TestTheProjectOnScreenChangingMovesThePanel(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("tasks"));
h.click(h.$("team-add-open"));
h.$("team-title").value = "draft";
h.$("team-title").oninput();
h.recv(withRoles());
assert.strictEqual(sent("teamWatch").length, 1, "an unchanged project was watched again");
h.recv(Object.assign(withRoles(), { root: "C:/other", projects: [{ root: "C:/other", name: "other", active: true, tabs: 1, waiting: 0, working: 0 }] }));
assert.deepStrictEqual(sent("teamWatch").pop(), { cmd: "teamWatch", root: "C:/other" });
assert.ok(h.$("overlay-body").textContent.includes("Reading the team"), "the other project's panel shows the last one's picture");
// A picture of the project that was left, arriving late, is not drawn.
h.recv(team());
assert.ok(h.$("overlay-body").textContent.includes("Reading the team"), "a late picture of another project was drawn");
h.recv(team({ root: "C:/other", project: "other", roster: [], tasks: [], messages: [], counts: { panes: 0, live: 0, tasks: {}, open: 0, messages: 0, unread: 0 } }));
h.click(tab("roster"));
assert.ok(h.$("overlay-body").textContent.includes("A team is"));
`)
}

func TestAnErrorFromTheServerForABoardThatCannotBeReadIsShown(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened({ boardError: "the task board at x was written by a newer Flockdeck", tasks: [], counts: { panes: 3, live: 2, tasks: {}, open: 0, messages: 0, unread: 0 } });
h.click(tab("tasks"));
assert.ok(h.$("overlay-body").textContent.includes("The task board could not be read: the task board at x was written by a newer Flockdeck"));
`)
}

func TestTheTeamActionIsInThePaletteAndGoTo(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
h.hello();
h.recv(withRoles());
h.press("palette");
const input = h.$("palette-input");
input.value = "team";
input.oninput();
const rows = h.$("palette-list").textContent;
assert.ok(rows.includes("Team") && rows.includes("Start a team"), "got: " + rows);
h.key({ key: "Escape" });
`)
}

func TestTheTeamPanelStylesUseOnlyTheSheetsTokens(t *testing.T) {
	runFrontEnd(t, `
const css = h.css();
const start = css.indexOf("/* ------------------------------------------------------------- team panel */");
assert.ok(start > 0, "no team panel section");
const mine = css.slice(start);
const colours = mine.match(/#[0-9a-fA-F]{3,8}\b|rgb\(|rgba\(|hsl\(/g);
assert.strictEqual(colours, null, "literal colours in the team styles: " + colours);
`)
}

func TestAPushThatDoesNotChangeWhatATabShowsReplacesNothingOnIt(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("messages"));
h.$("team-compose").focus();
h.$("team-compose").value = "mid-sentence";
h.$("team-compose").oninput();
const box = h.$("team-compose");
const list = h.$("team-msgs");
// The board moves, which the messages tab does not show: nothing there is
// drawn again, so the caret, the text and the scroll are the browser's own.
h.recv(team({ tasks: team().tasks.concat([tk("T9", "New", "planned")]),
  counts: { panes: 3, live: 2, tasks: { planned: 2, "in-progress": 1, done: 1, blocked: 1 }, open: 4, messages: 2, unread: 1 } }));
assert.ok(h.$("team-compose") === box, "the compose box was replaced by a push about the board");
assert.ok(h.$("team-msgs") === list, "the list was replaced by a push about the board");
assert.deepStrictEqual(texts([...h.$("overlay-tabs").children]), ["Roster3", "Tasks4", "Messages1"], "the tab counts did not follow");
// A new message does change it.
h.recv(team({ messages: [msg(7, { name: "dev", role: "worker", pane: "p2" }, { pane: "p1", name: "boss", role: "manager" }, "new")].concat(team().messages) }));
assert.strictEqual(h.$("team-msgs").children.length, 3);
assert.strictEqual(h.$("team-compose").value, "mid-sentence");
`)
}

func TestAPushWaitsWhileAListBoxIsBeingChosenFrom(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
opened();
h.click(tab("messages"));
const to = h.$("team-to");
to.onmousedown();
to.focus();
h.recv(team({ messages: [msg(7, { name: "dev", role: "worker", pane: "p2" }, { pane: "p1", name: "boss", role: "manager" }, "new")].concat(team().messages) }));
assert.ok(h.$("team-to") === to, "the list box was replaced while it was open");
assert.strictEqual(h.$("team-msgs").children.length, 2, "drawn under an open list");
// Chosen, it is let go and the picture is drawn.
h.$("team-to").value = "p2";
h.$("team-to").onchange();
await h.waitFor(() => h.$("team-msgs").children.length === 3, 3000);
assert.strictEqual(h.$("team-to").value, "p2", "the choice was lost");
`)
}
