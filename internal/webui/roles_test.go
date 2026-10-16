package webui

import "testing"

// The agent team's roles in the window: the Role control in the agent picker,
// the tag that names a pane's role, the Start a team dialog and the roles list
// in Settings. The harness has no CSS, so what is pinned is what the script
// draws and sends.

const roleCatalog = `
const roles = [
  { id: "manager", name: "Manager", description: "Breaks a request into tasks.", spawns: ["worker", "reviewer"] },
  { id: "worker", name: "Worker", description: "Carries out one task.", agent: "claude", model: "opus" },
  { id: "fast", name: "Fast", tier: "small", agent: "codex" },
  { id: "reviewer", name: "Reviewer" },
];
const withRoles = (over) => fixture({ agents: catalog(Object.assign({ roles }, over || {})) });
const lastStart = () => h.commands().filter((c) => c.cmd === "startAgent").pop();
`

func TestThePickerOffersRolesAndStartsInOne(t *testing.T) {
	runFrontEnd(t, roleCatalog+`
h.hello();
h.recv(withRoles());
h.click(h.$("new-tab-pick"));

const sel = h.$("agent-role");
assert.ok(sel, "the picker has no Role control");
const names = sel.querySelectorAll("option").map((o) => o.textContent);
assert.deepStrictEqual(names, ["None", "Manager", "Worker", "Fast", "Reviewer"], "got: " + JSON.stringify(names));
assert.strictEqual(sel.value, "", "a role was chosen before anybody chose one");
assert.ok(!h.$("agent-role-task"), "None asks for a task");

// A role shows what it is for, and which agent and model it comes to.
sel.value = "worker";
sel.onchange();
assert.ok(h.$("agent-role-more").textContent.includes("Carries out one task."), "no description");
assert.ok(h.$("agent-role-result").textContent.includes("claude · opus, the Worker role's choice"),
  "got: " + h.$("agent-role-result").textContent);
// A role that names nothing runs what the project does, and says so.
sel.value = "manager";
sel.onchange();
assert.ok(h.$("agent-role-result").textContent.includes("claude, this project's default"),
  "got: " + h.$("agent-role-result").textContent);
// A tier picks that tier of the role's agent; this agent has no such model.
sel.value = "fast";
sel.onchange();
assert.ok(h.$("agent-role-result").textContent.includes("codex · gpt-5, the Fast role's choice"),
  "got: " + h.$("agent-role-result").textContent);

// A role needs a task, as the command line does; nothing starts without one.
sel.value = "worker";
sel.onchange();
const before = h.commands().length;
h.click(h.$("agent-role-start"));
assert.strictEqual(h.commands().length, before, "a role started with no task");
assert.ok(!h.$("notice").hidden && h.$("notice").textContent.includes("task"), "nothing said why");
assert.ok(!h.$("overlay").hidden, "the picker closed without starting");
assert.ok(h.doc.activeElement === h.$("agent-role-task"), "the task did not take the keyboard");

// With a task the role starts on its own choice: the agent and model are left
// empty for the server to fill in.
const task = h.$("agent-role-task");
task.value = "add a health endpoint";
task.oninput();
h.click(h.$("agent-role-start"));
const sent = lastStart();
assert.ok(/^role-/.test(sent.id), "no request id: " + JSON.stringify(sent));
assert.deepStrictEqual(Object.assign({}, sent, { id: "x" }),
  { cmd: "startAgent", id: "x", root: "C:/repo", role: "worker", agent: "", model: "", task: "add a health endpoint", worktree: false });
assert.ok(h.$("overlay").hidden, "the picker stayed open");
// The pane it starts is shown when the server says it has started.
h.recv({ type: "agentStarted", paneId: "p9", id: sent.id });
assert.deepStrictEqual(h.commands().pop(), { cmd: "revealPane", id: "p9" });
`)
}

func TestARowPickedWithARoleNamesTheAgentAndModel(t *testing.T) {
	runFrontEnd(t, roleCatalog+`
h.hello();
h.recv(withRoles());
h.click(h.$("new-tab-pick"));
const sel = h.$("agent-role");
sel.value = "worker";
sel.onchange();
const task = h.$("agent-role-task");
task.value = "x";
task.oninput();
// A worktree is asked for with the box, and the agent and model picked in the
// list are the window's own, which the role's do not replace.
const tick = h.$("agent-role-more").querySelector("input");
tick.checked = true;
tick.onchange();
h.key({ key: "ArrowRight" });
h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
h.key({ key: "Enter" });
const sent = lastStart();
assert.strictEqual(sent.agent, "claude");
assert.strictEqual(sent.model, "opus");
assert.strictEqual(sent.role, "worker");
assert.strictEqual(sent.worktree, true);
assert.ok(!h.commands().some((c) => c.cmd === "newTab"), "the role also opened a plain tab");
`)
}

func TestThePickerKeepsWhatWasTypedWhenTheCatalogRedraws(t *testing.T) {
	runFrontEnd(t, roleCatalog+`
h.hello();
h.recv(withRoles());
h.click(h.$("new-tab-pick"));
h.$("agent-role").value = "worker";
h.$("agent-role").onchange();
h.$("agent-role-task").value = "half a task";
h.$("agent-role-task").oninput();
h.recv(withRoles({ default: { agent: "claude", model: "sonnet" } }));
assert.strictEqual(h.$("agent-role").value, "worker", "a redraw lost the role");
assert.strictEqual(h.$("agent-role-task").value, "half a task", "a redraw lost the task");
`)
}

// None is the picker as it was: it starts a plain tab and a catalog with no
// roles, as an older Flockdeck sends, has no Role control at all.
func TestThePickerWithNoRoleStartsAPlainTab(t *testing.T) {
	runFrontEnd(t, roleCatalog+`
h.hello();
h.recv(withRoles());
h.click(h.$("new-tab-pick"));
h.key({ key: "ArrowRight" });
h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "newTab", kind: "agent", agent: "claude", model: "opus" });
h.recv(fixture());
h.click(h.$("new-tab-pick"));
assert.ok(!h.$("overlay-body").querySelector("div.pick-role"), "a catalog with no roles has a Role control");
`)
}

func TestAPaneHeaderNamesItsRole(t *testing.T) {
	runFrontEnd(t, roleCatalog+`
h.hello();
const both = (over) => fixture(Object.assign({
  activeTab: "t1",
  tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
           root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  agents: catalog({ roles }),
}, over));
const tags = () => h.$("workspace").querySelectorAll("span.pane-role");
const shown = () => tags().filter((n) => !n.hidden).map((n) => n.textContent);

h.recv(both({ panes: { p1: pane("p1", { role: "manager" }), p2: pane("p2") } }));
assert.deepStrictEqual(shown(), ["Manager"], "got: " + JSON.stringify(shown()));
const tag = tags().find((n) => !n.hidden);
assert.strictEqual(tag.getAttribute("aria-label"), "Role: Manager", "the tag is not named for a screen reader");
assert.ok(tag.dataset.tip.includes("Breaks a request into tasks."), "no description: " + tag.dataset.tip);

// A pane in no role shows nothing, and so does a shell.
h.recv(both({ panes: { p1: pane("p1"), p2: pane("p2", { kind: "shell", agent: "" }) } }));
assert.deepStrictEqual(shown(), []);

// A role agents.json no longer has is shown by its id, and says so.
h.recv(both({ panes: { p1: pane("p1", { role: "gone" }), p2: pane("p2") } }));
assert.deepStrictEqual(shown(), ["gone"]);
assert.ok(tags().find((n) => !n.hidden).dataset.tip.includes("no longer in agents.json"));

// The name follows the catalog.
h.recv(both({ panes: { p1: pane("p1", { role: "worker" }), p2: pane("p2") },
  agents: catalog({ roles: [{ id: "worker", name: "Builder" }] }) }));
assert.deepStrictEqual(shown(), ["Builder"]);
`)
}

func TestTheAgentsOverviewNamesARowsRole(t *testing.T) {
	runFrontEnd(t, roleCatalog+`
h.hello();
h.recv(withRoles());
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "one", name: "a", status: "idle", agent: "claude", role: "worker" },
  { paneId: "p2", tabId: "t1", root: "C:/repo", project: "repo", tab: "one", name: "b", status: "idle", agent: "claude" },
] });
const tags = h.$("overlay-body").querySelectorAll("span.agent-role");
assert.deepStrictEqual(tags.map((n) => n.textContent), ["Worker"]);
assert.strictEqual(tags[0].getAttribute("aria-label"), "Role: Worker");
`)
}

func TestStartATeamStartsAManagerWithTheGoal(t *testing.T) {
	runFrontEnd(t, roleCatalog+paletteRun+`
h.hello();
h.recv(withRoles());
paletteRun("start a team");
assert.ok(!h.$("overlay").hidden, "the palette did not open the dialog");
assert.strictEqual(h.$("overlay-title").textContent, "Start a team");
const explain = h.$("team-explain").textContent;
assert.ok(explain.includes("one agent") && explain.includes("repo") && explain.includes("Worker, Reviewer") &&
  explain.includes("Nothing else starts now"), "got: " + explain);
assert.ok(h.doc.activeElement === h.$("team-goal"), "the goal did not take the keyboard");
const choice = h.$("team-agent").querySelectorAll("option")[0].textContent;
assert.ok(choice.includes("manager role's choice") && choice.includes("claude"), "got: " + choice);

// Nothing starts without a goal.
const before = h.commands().length;
h.click(h.$("team-start"));
assert.strictEqual(h.commands().length, before, "a team started with no goal");
assert.ok(!h.$("notice").hidden, "nothing said why");

// With one, a manager starts in the project on screen, on the role's own
// agent and model, and nothing else is sent.
h.$("team-goal").value = "  ship the export feature ";
h.$("team-goal").oninput();
h.click(h.$("team-start"));
const sent = lastStart();
assert.deepStrictEqual(Object.assign({}, sent, { id: "x" }),
  { cmd: "startAgent", id: "x", root: "C:/repo", role: "manager", agent: "", model: "", task: "ship the export feature", worktree: false });
assert.strictEqual(h.commands().filter((c) => c.cmd === "startAgent").length, 1);
assert.ok(h.$("overlay").hidden, "the dialog stayed open");
`)
}

func TestStartATeamCanNameTheManagersAgentAndModel(t *testing.T) {
	runFrontEnd(t, roleCatalog+paletteRun+`
h.hello();
h.recv(withRoles());
paletteRun("start a team");
h.$("team-goal").value = "go";
h.$("team-goal").oninput();
const sel = h.$("team-agent");
sel.value = "claude\nsonnet";
sel.onchange();
h.$("team-goal").onkeydown({ key: "Enter", ctrlKey: true, preventDefault() {} });
const sent = lastStart();
assert.strictEqual(sent.role, "manager");
assert.strictEqual(sent.agent, "claude");
assert.strictEqual(sent.model, "sonnet");
`)
}

func TestStartATeamSaysWhenThereIsNoManagerRole(t *testing.T) {
	runFrontEnd(t, roleCatalog+paletteRun+`
h.hello();
h.recv(withRoles({ roles: [{ id: "worker", name: "Worker" }] }));
paletteRun("start a team");
assert.ok(!h.$("overlay").hidden);
assert.ok(h.$("team-missing").textContent.includes("no manager role"), "got: " + h.$("overlay-body").textContent);
assert.ok(!h.$("team-start"), "there is a Start button with nothing to start");
assert.ok(!h.commands().some((c) => c.cmd === "startAgent"));
`)
}

func TestSettingsListsTheRolesToRead(t *testing.T) {
	runFrontEnd(t, roleCatalog+`
h.hello();
h.recv(withRoles());
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-agents"));
const list = h.$("set-roles");
assert.ok(list, "Settings has no roles list");
const text = list.textContent;
assert.ok(text.includes("Manager") && text.includes("Breaks a request into tasks.") && text.includes("claude · opus"), "got: " + text);
assert.ok(h.$("settings-pane").textContent.includes("agents.json"), "no pointer to agents.json");
assert.strictEqual(list.querySelectorAll("button, input, select").length, 0, "the list is meant to be read-only");
`)
}
