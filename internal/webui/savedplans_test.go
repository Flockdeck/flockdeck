package webui

import "testing"

// The Todo feature was folded into Fan out: its saved checklists are saved
// plans, reached from Fan-out history and started from the Fan out dialog.

// savedPlan is a plan as the server sends it: one step done, one with a failed
// attempt behind it, one untouched.
const savedPlansRun = fanoutRows + `
const plan = { id: "t1", root: "C:/repo", title: "Ship health endpoint", spec: "", createdAt: "", updatedAt: "", steps: [
  { id: "s1", text: "Add the route", done: true, attempts: [{ paneId: "p9", startedAt: "2026-10-01T09:00:00Z", endedAt: "2026-10-01T09:05:00Z", outcome: "done" }] },
  { id: "s2", text: "Write the tests", done: false, attempts: [{ paneId: "p8", startedAt: "2026-10-02T09:00:00Z", endedAt: "2026-10-02T09:05:00Z", outcome: "failed" }] },
  { id: "s3", text: "Document it", done: false },
] };
const button = (label) => body.querySelectorAll("button").find((b) => b.textContent === label);
const openHistory = (items) => {
  h.click(h.$("btn-fanout-history"));
  h.recv({ type: "fanoutHistory", root: "C:/repo", items: [] });
  h.recv({ type: "todos", root: "C:/repo", items });
};
const openPlan = () => {
  openHistory([plan]);
  h.click(button("Open in Fan out"));
  h.recv(preview());
};
`

// None of the Todo feature's own entry points is left: no rail button, no
// header button, no palette entry.
func TestThereIsNoTodoEntryPointLeft(t *testing.T) {
	runFrontEnd(t, savedPlansRun+`
assert.strictEqual(h.$("btn-todos"), null, "the rail still has a Todos button");
h.press("palette");
const labels = h.$("palette-list").children.filter((r) => r.classList.contains("pal-row")).map((r) => r.querySelector(".pal-label").textContent);
assert.ok(!labels.some((l) => /todo/i.test(l)), "the palette still offers a todo: " + labels.join(", "));
assert.ok(labels.includes("Fan out"), "Fan out is gone from the palette");
`)
}

// Fan-out history lists the saved plans above the past jobs, with their
// progress and each step's last outcome, and waits for both answers.
func TestFanOutHistoryListsSavedPlans(t *testing.T) {
	runFrontEnd(t, savedPlansRun+`
h.click(h.$("btn-fanout-history"));
h.recv({ type: "todos", root: "C:/repo", items: [plan] });
assert.ok(body.textContent.includes("Reading saved plans"), "the dialog drew before both answers had arrived");
h.recv({ type: "fanoutHistory", root: "C:/repo", items: [] });
const text = body.textContent;
assert.ok(text.includes("Ship health endpoint"), "the plan's name is not shown");
assert.ok(text.includes("1 of 3 done"), "the plan's progress is not shown");
assert.ok(text.includes("✓ Add the route") && text.includes("○ Write the tests"), "the steps are not marked done or not");
assert.ok(text.includes("last failed"), "a step's last outcome is not shown");
assert.ok(button("Open in Fan out") && button("Delete"), "a plan has no Open or Delete");

const sent = h.commands().length;
h.win._confirm = true;
h.click(button("Delete"));
const del = h.commands().slice(sent).find((c) => c.cmd === "todoDelete");
assert.ok(del && del.todoId === "t1", "Delete did not ask for the plan to be removed");
assert.ok(!body.textContent.includes("Ship health endpoint"), "a deleted plan is still listed");
`)
}

// A plan from the old Todo feature that is empty, or has an attempt that never
// finished, is drawn rather than crashing the dialog.
func TestFanOutHistoryDrawsOddSavedPlans(t *testing.T) {
	runFrontEnd(t, savedPlansRun+`
const empty = { id: "t2", root: "C:/repo", title: "", steps: null };
const running = { id: "t3", root: "C:/repo", title: "Mid-run", steps: [{ id: "a", text: "one", attempts: [{ paneId: "p1", startedAt: "2026-10-01T09:00:00Z" }] }] };
openHistory([empty, running]);
assert.ok(body.textContent.includes("Untitled plan") && body.textContent.includes("0 of 0 done"), "an empty plan is not drawn");
assert.ok(body.textContent.includes("running or not finished"), "an attempt with no outcome is not drawn");
h.click(button("Open in Fan out"));
h.recv(preview());
assert.ok(h.$("overlay-body").querySelector("textarea.fan-tasks"), "an empty plan did not open Fan out");
assert.ok(body.textContent.includes("Every step of this saved plan is done."), "the empty plan did not say there is nothing to start");
`)
}

// Opening a plan puts its steps that are not done in the list, its done steps
// aside, and starting sends the plan and each row's step with it.
func TestOpeningASavedPlanLoadsItsOpenSteps(t *testing.T) {
	runFrontEnd(t, savedPlansRun+`
openPlan();
assert.deepStrictEqual(fields().map((f) => f.value), ["Write the tests", "Document it"], "the open steps are not the rows");
assert.strictEqual(h.$("overlay-scope").textContent, "from repo · saved plan “Ship health endpoint”");
assert.ok(body.textContent.includes("Done") && body.textContent.includes("Add the route"), "the done step is not set aside");
assert.strictEqual(body.querySelector("input.fo-keep-name").value, "Ship health endpoint", "the plan's name is not in the name field");
assert.ok(button("Update plan"), "a reopened plan is saved as a new one");

type(fields()[1], "Document the endpoint");
h.click(start());
const fan = h.commands().filter((c) => c.cmd === "fanout").pop();
assert.ok(fan, "Start sent no fan-out");
assert.deepStrictEqual(fan.tasks, ["Write the tests", "Document the endpoint"]);
assert.strictEqual(fan.todoId, "t1");
assert.deepStrictEqual(fan.stepIds, ["s2", "s3"], "an edited row lost its step");
`)
}

// A row typed in since the plan was saved starts with no step of the plan.
func TestARowAddedToASavedPlanHasNoStep(t *testing.T) {
	runFrontEnd(t, savedPlansRun+`
openPlan();
type(addField(), "Rename the flag");
addField().onchange();
h.click(start());
const fan = h.commands().filter((c) => c.cmd === "fanout").pop();
assert.deepStrictEqual(fan.tasks, ["Write the tests", "Document it", "Rename the flag"]);
assert.deepStrictEqual(fan.stepIds, ["s2", "s3", ""]);
`)
}

// Ticking a row done takes it off the list and tells the server; Reopen puts
// it back.
func TestTickingAStepDoneAndReopeningIt(t *testing.T) {
	runFrontEnd(t, savedPlansRun+`
openPlan();
const sent = h.commands().length;
h.click(rows()[0].querySelectorAll("button").find((b) => b.textContent === "Mark done"));
const done = h.commands().slice(sent).find((c) => c.cmd === "todoStepDone");
assert.ok(done && done.todoId === "t1" && done.stepId === "s2" && done.done === true, "ticking a row sent the wrong command: " + JSON.stringify(done));
assert.deepStrictEqual(fields().map((f) => f.value), ["Document it"], "a ticked row is still on the list");
assert.ok(body.textContent.includes("Write the tests"), "a ticked step is not in the done section");

h.click(button("Reopen"));
const back = h.commands().filter((c) => c.cmd === "todoStepDone").pop();
assert.ok(back.stepId === "s1" && back.done === false, "Reopen sent the wrong command: " + JSON.stringify(back));
assert.deepStrictEqual(fields().map((f) => f.value), ["Document it", "Add the route"]);
h.click(start());
const fan = h.commands().filter((c) => c.cmd === "fanout").pop();
assert.deepStrictEqual(fan.stepIds, ["s3", "s1"], "a reopened step lost its place in the plan");
`)
}

// A list from a pane is saved under a name; the answer binds the dialog to
// the saved plan, so the next save updates it instead of making another, and
// Start then sends the plan.
func TestSavingAFanOutListForLater(t *testing.T) {
	runFrontEnd(t, fanoutRows+`
h.press("fanout");
h.recv(preview());
assert.ok(body.textContent.includes("Save for later"), "the dialog has no way to save the list");
const save = body.querySelectorAll("button").find((b) => b.textContent === "Save plan");
assert.ok(save && !save.disabled, "Save plan is not available with three tasks");
type(body.querySelector("input.fo-keep-name"), "Health work");
h.click(save);
const cmd = h.commands().filter((c) => c.cmd === "todoSave").pop();
assert.ok(cmd, "Save plan sent nothing");
assert.strictEqual(cmd.title, "Health work");
assert.deepStrictEqual(cmd.tasks, ["Add a health endpoint", "Write the parser tests", "Document the API"]);
assert.ok(!cmd.todoId, "a new plan named an existing one");
assert.ok(cmd.stepIds.every((id) => id === ""), "a new plan sent step ids");
assert.ok(!h.$("overlay").hidden, "saving closed the dialog");

h.recv({ type: "todoSaved", todo: { id: "n1", root: "C:/repo", title: "Health work", steps: [
  { id: "x1", text: "Add a health endpoint" }, { id: "x2", text: "Write the parser tests" }, { id: "x3", text: "Document the API" }] } });
assert.ok(body.querySelectorAll("button").find((b) => b.textContent === "Update plan"), "the button did not become Update plan");
h.click(start());
const fan = h.commands().filter((c) => c.cmd === "fanout").pop();
assert.strictEqual(fan.todoId, "n1");
assert.deepStrictEqual(fan.stepIds, ["x1", "x2", "x3"]);
`)
}

// An ordinary fan-out, never saved, goes over the wire exactly as it did.
func TestAnOrdinaryFanOutSendsNoPlan(t *testing.T) {
	runFrontEnd(t, fanoutRows+`
h.press("fanout");
h.recv(preview());
h.click(start());
const fan = h.commands().filter((c) => c.cmd === "fanout").pop();
assert.ok(fan && !("todoId" in fan) && !("stepIds" in fan), "an unsaved fan-out named a plan: " + JSON.stringify(fan));
`)
}

// A save answered after the dialog was closed, or while another is open, is
// not drawn over it.
func TestASavedPlanAnswerDoesNotReopenAnything(t *testing.T) {
	runFrontEnd(t, savedPlansRun+`
const saved = { type: "todoSaved", todo: plan };
h.recv(saved);
assert.ok(h.$("overlay").hidden, "a save with nothing open brought a dialog up");
h.click(h.$("btn-settings"));
h.recv(saved);
assert.ok(!h.$("overlay-title").textContent.includes("Fan"), "a save replaced the dialog on screen");
`)
}
