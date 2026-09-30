package webui

import "testing"

// The pop-over redesign's Fan out (part B3): the plan is shown once, as one
// editable row a task, with the textarea it is drawn from behind "Edit as
// text"; the options say where the agents run; and a footer counts them and
// starts them.

// fanoutRows opens Fan out on a plan of three tasks with a choice of models,
// and names the parts the cases below reach for.
const fanoutRows = `
h.hello();
h.recv(fixture());
const agents = [{ id: "claude", name: "Claude Code", default: "", models: [
  { id: "", name: "Default" }, { id: "opus", name: "Opus", tier: "top" }, { id: "haiku", name: "Haiku", tier: "small" }] }];
const preview = (over) => Object.assign({ type: "fanoutPreview", paneId: "p1", isRepo: true, trusted: true, project: "repo",
  cwd: "C:/repo", tasks: ["Add a health endpoint", "Write the parser tests", "Document the API"], agent: "claude",
  model: "opus", agents }, over || {});
const body = h.$("overlay-body");
const rows = () => body.querySelectorAll("div.fan-row");
const fields = () => rows().map((r) => r.querySelector("input.fan-row-task"));
const box = () => body.querySelector("textarea.fan-tasks");
const addField = () => body.querySelector("input.fan-add-task");
const start = () => body.querySelector("button.primary");
const type = (input, value) => { input.value = value; h.dispatch(input, new h.Ev("input", { target: input })); };
`

// Each task is a field of its own, drawn once: the textarea behind them is
// shown in their place only by "Edit as text", and the keyboard starts on
// the first task.
func TestTheFanOutShowsThePlanOnceAsRows(t *testing.T) {
	runFrontEnd(t, fanoutRows+`
h.press("fanout");
h.recv(preview());
assert.deepStrictEqual(fields().map((f) => f.value), ["Add a health endpoint", "Write the parser tests", "Document the API"]);
assert.ok(box().hidden, "the plan is drawn twice: the textarea is showing beside the rows");
assert.ok(h.doc.activeElement === fields()[0], "the keyboard did not start on the first task");
assert.strictEqual(fields()[1].getAttribute("aria-label"), "Task 2", "a task's field is not named");
assert.strictEqual(h.$("overlay-scope").textContent, "from repo · Claude Code", "the head does not say where the plan came from");
assert.ok(body.querySelector("span.fan-sec-num").textContent === "3 of 12", "the count against the cap is not shown");

const asText = body.querySelectorAll("button").find((b) => b.textContent === "Edit as text");
assert.ok(asText, "there is no way to edit the plan as text");
h.click(asText);
assert.ok(!box().hidden && body.querySelector("div.fan-rows").hidden, "Edit as text did not swap the rows for the textarea");
assert.strictEqual(asText.getAttribute("aria-pressed"), "true");
assert.ok(h.doc.activeElement === box(), "the keyboard did not go to the textarea");
box().value += "\nRename the config flag";
box().oninput();
h.click(asText);
assert.ok(box().hidden && !body.querySelector("div.fan-rows").hidden, "Edit as text did not come back to the rows");
assert.strictEqual(fields()[3].value, "Rename the config flag", "a line added as text is not a row");
`)
}

// A row is edited in place and the textarea follows it, which is what Start
// sends; the model chosen for the row goes with its words.
func TestAFanOutRowIsEditedInPlace(t *testing.T) {
	runFrontEnd(t, fanoutRows+`
h.press("fanout");
h.recv(preview());
const sel = rows()[1].querySelector("select");
sel.value = "claude\nhaiku";
h.dispatch(sel, new h.Ev("change", { target: sel }));
const row = rows()[1];
fields()[1].focus();
type(fields()[1], "Write the parser tests first");
assert.ok(rows()[1] === row, "the row being typed into was built again under the keyboard");
assert.ok(h.doc.activeElement === fields()[1], "the keyboard left the row being typed into");
assert.strictEqual(rows()[1].querySelector("select").value, "claude\nhaiku", "the row's model did not stay with its edited words");
assert.strictEqual(box().value.split("\n")[1], "Write the parser tests first", "the textarea behind the rows did not follow");
start().onclick();
const sent = h.commands().pop();
assert.deepStrictEqual(sent.tasks, ["Add a health endpoint", "Write the parser tests first", "Document the API"]);
assert.deepStrictEqual(sent.taskModels, ["", "haiku", ""]);
`)
}

// The row after the last is where a list is typed: Enter makes it a task and
// stays for the next, a paste of several lines is several tasks, and Enter
// and the arrows walk the rows. A row emptied is a task taken out.
func TestTasksAreTypedAndTakenOutFromTheKeyboard(t *testing.T) {
	runFrontEnd(t, fanoutRows+`
h.press("fanout");
h.recv(preview());
addField().focus();
addField().value = "Bump the version";
h.key({ key: "Enter" });
assert.strictEqual(fields().length, 4, "Enter in the last row did not add a task");
assert.strictEqual(fields()[3].value, "Bump the version");
assert.ok(h.doc.activeElement === addField() && addField().value === "", "the keyboard did not stay for the next task");
h.dispatch(addField(), new h.Ev("paste", { target: addField(), clipboardData: { getData: () => "Tag it\r\n\r\nShip it\n" } }));
assert.deepStrictEqual(fields().slice(4).map((f) => f.value), ["Tag it", "Ship it"], "a pasted list was not a task a line");

h.key({ key: "ArrowUp" });
assert.ok(h.doc.activeElement === fields()[5], "Up from the last row did not reach the task above it");
h.key({ key: "ArrowUp" });
h.key({ key: "Enter" });
assert.ok(h.doc.activeElement === fields()[5], "Enter in a task did not move to the next");

// Backspace in an empty task takes it out, and the keyboard goes up a row.
type(fields()[5], "");
assert.strictEqual(fields().length, 6, "a row was taken out while it was being typed into");
h.key({ key: "Backspace" });
assert.strictEqual(fields().length, 5, "Backspace in an empty task did not take it out");
assert.ok(h.doc.activeElement === fields()[4], "the keyboard did not go to the task above");

// A task emptied and left is taken out too.
fields()[0].focus();
type(fields()[0], "");
const emptied = fields()[0];
fields()[1].focus();
emptied.onblur();
assert.deepStrictEqual(fields().map((f) => f.value),
  ["Write the parser tests", "Document the API", "Bump the version", "Tag it"]);
assert.strictEqual(rows()[0].querySelector(".fan-row-n").textContent, "1", "the rows were not numbered again");

// Ctrl+Enter starts them from a task, with what is half typed in the last row.
addField().value = "Announce it";
fields()[0].focus();
h.key({ key: "Enter", ctrlKey: true });
const sent = h.commands().pop();
assert.strictEqual(sent.cmd, "fanout", "Ctrl+Enter in a task did not start them");
assert.deepStrictEqual(sent.tasks, ["Write the parser tests", "Document the API", "Bump the version", "Tag it", "Announce it"]);
`)
}

// The footer counts what will start, by model, and holds Cancel and the one
// primary, Start, last; Cancel sends nothing and gives the keyboard back to
// what opened the dialog.
func TestTheFanOutFooterCountsAndCancels(t *testing.T) {
	runFrontEnd(t, fanoutRows+`
const opener = h.$("workspace").querySelectorAll("button").find((b) => b.textContent === "⑂");
assert.ok(opener, "the pane header has no Fan out button");
opener.focus();
h.click(opener);
h.recv(preview());
const foot = body.children[body.children.length - 1];
assert.ok(foot.classList.contains("ov-foot"), "the footer is not the body's last child");
assert.ok(foot.querySelector("button.primary") === start(), "Start is not the footer's primary");
assert.strictEqual(start().textContent, "Start 3 agents");
assert.strictEqual(start().getAttribute("aria-keyshortcuts"), "Control+Enter");
const count = () => foot.querySelector("span.fan-count").textContent;
assert.strictEqual(count(), "3 agents · 3× Opus");
const sel = rows()[2].querySelector("select");
sel.value = "claude\nhaiku";
h.dispatch(sel, new h.Ev("change", { target: sel }));
assert.strictEqual(count(), "3 agents · 2× Opus, 1× Haiku", "the count does not say which models they start on");

const cancel = foot.querySelectorAll("button").find((b) => b.textContent === "Cancel");
assert.ok(cancel && !cancel.classList.contains("primary"), "there is no Cancel beside Start");
const before = h.commands().length;
h.click(cancel);
assert.ok(h.$("overlay").hidden, "Cancel did not close the dialog");
assert.strictEqual(h.commands().length, before, "Cancel sent something");
assert.ok(h.doc.activeElement === opener, "Cancel did not give the keyboard back to the pane's Fan out button");
`)
}

// Where they run: the worktree switch, the tab as two choices the arrows walk,
// and trust, which is about the worktrees and so goes with them.
func TestWhereTheFanOutRunsIsSaidPositively(t *testing.T) {
	runFrontEnd(t, fanoutRows+`
h.press("fanout");
h.recv(preview());
const sw = body.querySelector("button.switch");
assert.strictEqual(sw.getAttribute("role"), "switch");
assert.strictEqual(sw.getAttribute("aria-checked"), "true", "a repository does not start on worktrees");
const trust = body.querySelectorAll("label.fan-opt").find((l) => l.textContent.includes("Trust"));
assert.ok(trust.textContent.includes("as repo already is"), "trust does not say where the answer comes from: " + trust.textContent);
const radios = body.querySelectorAll("button.seg");
assert.deepStrictEqual(radios.map((b) => b.textContent), ["New tab", "Beside the planner"]);
assert.deepStrictEqual(radios.map((b) => b.getAttribute("aria-checked")), ["true", "false"]);
radios[0].focus();
h.key({ key: "ArrowRight" });
assert.deepStrictEqual(radios.map((b) => b.getAttribute("aria-checked")), ["false", "true"], "the arrows did not choose the tab");
assert.ok(h.doc.activeElement === radios[1]);

h.click(sw);
assert.strictEqual(sw.getAttribute("aria-checked"), "false");
assert.ok(trust.querySelector("input").disabled, "trust is offered for worktrees that will not be made");
start().onclick();
const sent = h.commands().pop();
assert.strictEqual(sent.worktrees, false);
assert.strictEqual(sent.split, true, "Beside the planner did not put them in this tab");
assert.strictEqual(sent.trust, false);

// Untrusted, it says so, as the reason the box cannot be ticked.
h.press("fanout");
h.recv(preview({ trusted: false }));
const t2 = body.querySelectorAll("label.fan-opt").find((l) => l.textContent.includes("Trust"));
assert.ok(t2.textContent.includes("repo is not trusted in Claude Code, so each agent will ask once"), t2.textContent);
assert.ok(t2.querySelector("input").disabled);
`)
}

// Nothing found says so, and says how to go on; a pane that ends in a question
// for you (a permission prompt, which the server reads no plan from) is not
// said to have no plan, but to be asking something.
func TestAnEmptyFanOutSaysWhy(t *testing.T) {
	runFrontEnd(t, fanoutRows+`
h.press("fanout");
h.recv(preview({ tasks: [], question: true }));
const note = body.querySelector("div.fan-note");
assert.ok(note && note.classList.contains("warn"), "a pane asking a question is not warned about");
assert.ok(note.textContent.includes("This looks like a question the agent is asking you, not a plan."), note.textContent);
assert.strictEqual(fields().length, 0);
assert.ok(h.doc.activeElement === addField(), "the keyboard is not where a task is typed");
assert.ok(start().disabled, "Start is offered with nothing to start");

h.key({ key: "Escape" });
h.press("fanout");
h.recv(preview({ tasks: [] }));
const none = body.querySelector("div.fan-note");
assert.ok(none && !none.classList.contains("warn"));
assert.ok(none.textContent.includes("No plan was found in this pane."), none.textContent);
`)
}
