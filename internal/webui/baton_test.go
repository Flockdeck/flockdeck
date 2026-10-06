package webui

import "testing"

// The baton dialog: asked for from the palette on an agent pane, answered with a
// draft that is edited as text, then used three ways from the footer.

const batonSetup = paletteRun + `
h.hello();
h.recv(fixture());
const draftText = "---\nid: 20261001-090000-0a1b2c\n---\n\n# Baton: Add a retry\n\n## Goal\n\nAdd a retry.\n";
const agents = [
  { id: "claude", name: "Claude Code", provider: "anthropic", default: "", models: [{ id: "", name: "Default" }, { id: "haiku", name: "Haiku" }] },
  { id: "codex", name: "Codex", provider: "openai", default: "", models: [{ id: "", name: "Default" }] }];
// The id the page sent with the last command of a kind, which the server repeats in its answer.
const lastReq = (cmd) => { const l = h.commands().filter((c) => c.cmd === cmd); return l.length ? l[l.length - 1].req : undefined; };
const bsaved = (f) => h.recv(Object.assign({ type: "batonSaved", paneId: "p1", req: lastReq(f.cmd), id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 0 }, f));
const berror = (f) => h.recv(Object.assign({ type: "batonError", paneId: "p1", req: lastReq(f.cmd), error: "refused" }, f));
const draft = (over) => Object.assign({ type: "batonDraft", paneId: "p1", req: lastReq("makeBaton"), name: "api", agent: "claude", provider: "anthropic",
  model: "", isRepo: true, dirty: false, text: draftText, scrubbed: 0, agents }, over || {});
const body = h.$("overlay-body");
const box = () => body.querySelector("textarea.bt-text");
const btn = (label) => body.querySelectorAll("button").find((b) => b.textContent === label);
const note = () => body.querySelector("div.bt-scrub").textContent;
`

func TestTheBatonDialogOpensFromTheKeyTableAndShowsTheDraft(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
const asked = h.commands().pop();
assert.ok(asked.cmd === "makeBaton" && asked.id === "p1" && asked.req, "the request has no id: " + JSON.stringify(asked));
h.recv(draft());
assert.strictEqual(h.$("overlay-title").textContent, "Make baton");
assert.strictEqual(box().value, draftText, "the draft is not in the editor as it came");
assert.strictEqual(h.$("overlay-scope").textContent, "from api · Claude Code");
assert.ok(note().includes("0 secrets removed") && note().includes("filter, not a guarantee"), "the scrub note is not there: " + note());
// An answer for another pane than the one asked about is not drawn over it.
h.recv(draft({ paneId: "p9", text: "other" }));
assert.strictEqual(box().value, draftText);
`)
}

// The count is of the marks in the text as it stands, so a mark deleted from the
// draft stops being counted, and one typed in is.
func TestTheScrubCountFollowsTheText(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft({ text: draftText + "\nkey [REDACTED: aws-key] and [REDACTED: jwt]\n" }));
assert.ok(note().includes("2 secrets removed"), note());
assert.ok(body.querySelector("span.bt-count").textContent === "2 secrets removed", "the footer does not count them");
box().value = draftText + "\nkey [REDACTED: aws-key]\n";
box().oninput();
assert.ok(note().includes("1 secret removed."), note());
`)
}

// Each footer button sends the text as it is in the editor, so what was edited
// is what is sent; the server reads the baton out of it.
func TestTheBatonFooterSendsTheEditedText(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft());
box().value = draftText + "\n## Constraints\n\nDo not touch the migration.\n";
box().oninput();
body.querySelector("input.bt-task").value = "carry on with the cache";

h.click(btn("Save only"));
let sent = h.commands().pop();
assert.strictEqual(sent.cmd, "saveBaton");
assert.ok(sent.text.includes("Do not touch the migration."), "an edit was not sent");
assert.strictEqual(sent.task, "carry on with the cache");
assert.ok(btn("Save only").disabled && btn("New pane").disabled, "the dialog can be used twice while a command is out");

// A notice about something else is not the answer: the command is still out,
// and pressing again would send it twice.
h.recv({ type: "notice", text: "some other error", error: true });
assert.ok(btn("Save only").disabled && btn("New pane").disabled, "an unrelated error notice gave the dialog back");
const sentBefore = h.commands().length;
h.click(btn("Save only"));
assert.strictEqual(h.commands().length, sentBefore, "the command was sent a second time");

// The command's own refusal gives the dialog back, edits and all.
berror({ paneId: "p1", cmd: "saveBaton", error: "the baton's header is missing" });
assert.ok(!btn("Save only").disabled, "a refusal left the dialog stuck");
assert.ok(box().value.includes("Do not touch the migration."), "a refusal lost the edits");

h.click(btn("Restart this pane"));
sent = h.commands().pop();
assert.strictEqual(sent.cmd, "restartWithBaton");
assert.strictEqual(sent.id, "p1");
bsaved({ paneId: "p1", cmd: "restartWithBaton", id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 1, started: "p1" });
assert.ok(h.$("overlay").hidden, "the dialog stayed open after the baton was used");
`)
}

// With no answer at all the dialog is given back, rather than left disabled for
// good, and says so.
func TestTheBatonDialogIsGivenBackWhenNoAnswerComes(t *testing.T) {
	runFrontEnd(t, batonSetup+`
h.win.batonWaitMs = 40;
paletteRun("make baton");
h.recv(draft());
box().value = draftText + "\nan edit\n";
box().oninput();
h.click(btn("Save only"));
assert.ok(btn("Save only").disabled);
await h.sleep(150);
assert.ok(!btn("Save only").disabled, "the dialog stayed disabled with no answer");
assert.ok(box().value.includes("an edit"), "the edit was lost");
`)
}

// Going to Help and back draws the dialog again, from what it held.
func TestTheBatonDialogKeepsItsEditsThroughHelpAndBack(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft());
box().value = draftText + "\nan edit that matters\n";
box().oninput();
body.querySelector("input.bt-task").value = "my task";
body.querySelector("input.bt-task").oninput();
const asked = h.commands().filter((c) => c.cmd === "makeBaton").length;
h.click(h.$("overlay-help"));
h.click(h.$("overlay-back"));
assert.strictEqual(h.$("overlay-title").textContent, "Make baton");
assert.ok(box().value.includes("an edit that matters"), "the edit was lost: " + box().value);
assert.strictEqual(body.querySelector("input.bt-task").value, "my task", "the task was lost");
assert.strictEqual(h.commands().filter((c) => c.cmd === "makeBaton").length, asked, "the draft was asked for again");
`)
}

// An agent whose company the catalog does not name is not let through as if it
// were the same one.
func TestAnAgentWithNoKnownCompanyNeedsTheTickToo(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft({ agents: agents.concat([{ id: "mystery", name: "Mystery", default: "", models: [{ id: "", name: "Default" }] }]) }));
const start = btn("New pane");
const sel = body.querySelector("select");
sel.value = "mystery\n";
h.dispatch(sel, new h.Ev("change", { target: sel }));
assert.ok(body.textContent.includes("company Flockdeck does not know"), "an unknown company was not said");
assert.ok(start.disabled, "an unknown company was let through");
const ack = body.querySelectorAll("input").find((i) => i.type === "checkbox");
ack.checked = true;
h.dispatch(ack, new h.Ev("change", { target: ack }));
h.click(start);
const sent = h.commands().pop();
assert.strictEqual(sent.cmd, "startFromBaton");
assert.strictEqual(sent.confirmed, true, "the tick was not sent with the command");
`)
}

func TestNewPaneFromABatonChoosesAgentWorktreeAndPlace(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft({ dirty: true }));
const start = btn("New pane");
assert.ok(!start.disabled);
const sel = body.querySelector("select");
sel.value = "claude\nhaiku";
h.dispatch(sel, new h.Ev("change", { target: sel }));

// A worktree needs a branch, and starts without the uncommitted changes, which
// the dialog says.
const worktree = body.querySelectorAll("button.switch")[0];
h.click(worktree);
assert.ok(start.disabled, "a worktree was allowed with no branch named");
const dirtyNote = body.querySelectorAll("div.fan-note").find((d) => d.textContent.includes("uncommitted"));
assert.ok(dirtyNote && !dirtyNote.hidden, "uncommitted work in a new worktree was not warned of");
const branch = body.querySelector("input.bt-branch");
branch.value = "fix-auth";
h.dispatch(branch, new h.Ev("input", { target: branch }));
assert.ok(!start.disabled);
h.click(body.querySelectorAll("button.switch")[1]);

h.click(start);
const sent = h.commands().pop();
assert.strictEqual(sent.cmd, "startFromBaton");
assert.strictEqual(sent.agent, "claude");
assert.strictEqual(sent.model, "haiku");
assert.strictEqual(sent.branch, "fix-auth");
assert.strictEqual(sent.split, true);
`)
}

// The baton goes to the agent's company, so a different one has to be agreed to.
func TestAnotherCompanysAgentNeedsAnAgreementToReceiveTheBaton(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft());
const start = btn("New pane");
const sel = body.querySelector("select");
sel.value = "codex\n";
h.dispatch(sel, new h.Ev("change", { target: sel }));
assert.ok(body.textContent.includes("sends the baton to openai, not anthropic"), "a change of company was not said");
assert.ok(start.disabled, "the baton can go to another company with no agreement");
const ack = body.querySelectorAll("input").find((i) => i.type === "checkbox");
ack.checked = true;
h.dispatch(ack, new h.Ev("change", { target: ack }));
assert.ok(!start.disabled, "agreeing did not allow it");
// Back to the source's own agent, the warning goes and the agreement with it.
sel.value = "claude\n";
h.dispatch(sel, new h.Ev("change", { target: sel }));
assert.ok(!body.textContent.includes("sends the baton to"), "the warning stayed for the same company");
assert.ok(!start.disabled);
// Restarting this pane stays with its own agent, so it never asks.
sel.value = "codex\n";
h.dispatch(sel, new h.Ev("change", { target: sel }));
ack.checked = false;
h.dispatch(ack, new h.Ev("change", { target: ack }));
assert.ok(!btn("Restart this pane").disabled && !btn("Save only").disabled);
`)
}

// A shell has no conversation to make a baton from, so the command is not sent.
func TestAShellIsNotOfferedABaton(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture({ panes: { p1: pane("p1", { kind: "shell", agent: "" }) } }));
const before = h.commands().length;
paletteRun("make baton");
assert.strictEqual(h.commands().slice(before).filter((c) => c.cmd === "makeBaton").length, 0, "a shell was asked for a baton");
`)
}

// The busy state is held, not drawn: Help and back draws the dialog again, and it
// must come back still waiting, not free to send the command a second time.
func TestHelpAndBackKeepsTheDialogWaiting(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft());
h.click(btn("Save only"));
assert.ok(btn("Save only").disabled);
const sent = h.commands().length;
h.click(h.$("overlay-help"));
h.click(h.$("overlay-back"));
assert.ok(btn("Save only").disabled && btn("New pane").disabled && btn("Restart this pane").disabled,
  "Help and back gave the buttons back while the command was out");
h.click(btn("Save only"));
assert.strictEqual(h.commands().length, sent, "the command was sent again");
bsaved({ paneId: "p1", cmd: "saveBaton", id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 0 });
assert.ok(h.$("overlay").hidden, "the answer did not close the dialog");
`)
}

// An answer that is not for the dialog now on screen does nothing to it.
func TestALateOrForeignBatonAnswerDoesNotTouchTheDialog(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft());
box().value = draftText + "\nan edit that must survive\n";
box().oninput();
// Nothing is out, so an answer is a late one.
bsaved({ paneId: "p1", cmd: "saveBaton", id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 0 });
assert.ok(!h.$("overlay").hidden, "a late answer closed the dialog");
assert.ok(box().value.includes("an edit that must survive"), "a late answer threw the edits away");
berror({ paneId: "p1", cmd: "saveBaton", error: "late" });
assert.ok(box().value.includes("an edit that must survive"));
// An answer for another pane, or another command, is not this one's either.
h.click(btn("Save only"));
bsaved({ paneId: "p9", cmd: "saveBaton", req: "not-ours", id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 0 });
assert.ok(!h.$("overlay").hidden && btn("Save only").disabled, "an answer with an id that is not out was taken");
bsaved({ paneId: "p1", cmd: "startFromBaton", id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 0 });
assert.ok(!h.$("overlay").hidden && btn("Save only").disabled, "another command's answer was taken");
berror({ paneId: "p1", cmd: "restartWithBaton", error: "not ours" });
assert.ok(btn("Save only").disabled, "another command's refusal gave the dialog back");
bsaved({ paneId: "p1", cmd: "saveBaton", id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 0 });
assert.ok(h.$("overlay").hidden);
`)
}

// A mark is one of the kinds the scrubber makes; the count follows the draft's own
// list, so a mark that looks like one is not counted.
func TestTheScrubCountIgnoresMarksTheScrubberCouldNotMake(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft({ markKinds: ["aws-key", "jwt"], text: draftText + "\n[REDACTED: aws-key] [REDACTED: x] [REDACTED: sk`+`-abcdefghijklmnopqrstuvwxyz0123]\n" }));
assert.ok(note().includes("1 secret removed."), note());
`)
}

const batonNotices = `
const noticeText = () => h.$("notice").children.map((c) => c.textContent).join(" | ");
`

// The guard on the buttons is the dialog's own: another command, called past a
// disabled button, is not sent while one is out. (The same command is refused by
// the in-flight record, so a different one is what shows the guard.)
func TestAnotherCommandIsNotSentWhileOneIsOut(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft());
h.click(btn("Save only"));
const sent = h.commands().length;
btn("Restart this pane").onclick();
btn("New pane").onclick();
assert.strictEqual(h.commands().length, sent, "a second command was sent while one was out");
`)
}

// When the timer gives the dialog back, it is the dialog as drawn now: after Help
// and back the buttons drawn then are the ones that come free.
func TestTheTimerGivesBackTheDialogAsItIsDrawnNow(t *testing.T) {
	runFrontEnd(t, batonSetup+`
h.win.batonWaitMs = 40;
paletteRun("make baton");
h.recv(draft());
h.click(btn("Save only"));
h.click(h.$("overlay-help"));
h.click(h.$("overlay-back"));
assert.ok(btn("Save only").disabled, "the buttons were free after Help and back");
await h.sleep(150);
assert.ok(!btn("Save only").disabled && !btn("New pane").disabled, "the timer freed the old drawing, not this one");
`)
}

// After the dialog is given back the command may still be running; the same one
// is refused until the server answers, and then it is allowed again.
func TestTheSameCommandIsRefusedUntilItAnswers(t *testing.T) {
	runFrontEnd(t, batonSetup+batonNotices+`
h.win.batonWaitMs = 40;
paletteRun("make baton");
h.recv(draft());
h.click(btn("New pane"));
assert.strictEqual(h.commands().filter((c) => c.cmd === "startFromBaton").length, 1);
await h.sleep(150);
assert.ok(!btn("New pane").disabled, "the dialog was not given back");
h.click(btn("New pane"));
assert.strictEqual(h.commands().filter((c) => c.cmd === "startFromBaton").length, 1, "a second agent was started");
assert.ok(noticeText().includes("still being done"), noticeText());
// Another command is not the same one.
h.click(btn("Save only"));
assert.strictEqual(h.commands().filter((c) => c.cmd === "saveBaton").length, 1);
// Once it answers it can be sent again.
bsaved({ paneId: "p1", cmd: "startFromBaton", id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 0, started: "p2" });
paletteRun("make baton");
h.recv(draft());
h.click(btn("New pane"));
assert.strictEqual(h.commands().filter((c) => c.cmd === "startFromBaton").length, 2);
`)
}

// A refusal of some other command is not the failure of the draft.
func TestAnotherCommandsRefusalDoesNotCloseAnUnansweredDialog(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
berror({ paneId: "p1", cmd: "saveBaton", error: "not about the draft" });
assert.ok(!h.$("overlay").hidden, "another command's refusal closed the dialog waiting for its draft");
berror({ paneId: "p1", cmd: "makeBaton", error: "no draft" });
assert.ok(h.$("overlay").hidden, "the draft's own refusal did not close it");
`)
}

// The answer is said even when the dialog was closed while it was out.
func TestAnAnswerIsSaidEvenIfTheDialogWasClosed(t *testing.T) {
	runFrontEnd(t, batonSetup+batonNotices+`
paletteRun("make baton");
h.recv(draft());
box().value = draftText + "\nan edit\n";
box().oninput();
h.click(btn("Save only"));
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape did not close the dialog");
bsaved({ paneId: "p1", cmd: "saveBaton", id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 2 });
assert.ok(noticeText().includes("Saved baton 20261001-090000-0a1b2c") && noticeText().includes("2 secrets"), noticeText());
// And it does not touch a dialog opened since.
paletteRun("make baton");
h.recv(draft());
box().value = draftText + "\na newer edit\n";
box().oninput();
bsaved({ paneId: "p1", cmd: "saveBaton", id: "20261001-090000-0a1b2d", path: "/x", scrubbed: 0 });
assert.ok(!h.$("overlay").hidden && box().value.includes("a newer edit"), "a late answer cleared a newer dialog");
`)
}

// Help is open over the dialog when the timer fires: nothing is drawn to give back,
// but the held state must be, so the dialog is usable when it comes back.
func TestTheTimerFiringUnderHelpStillFreesTheDialog(t *testing.T) {
	runFrontEnd(t, batonSetup+batonNotices+`
h.win.batonWaitMs = 40;
paletteRun("make baton");
h.recv(draft());
h.click(btn("Save only"));
h.click(h.$("overlay-help"));
await h.sleep(150);
h.click(h.$("overlay-back"));
assert.strictEqual(h.$("overlay-title").textContent, "Make baton");
assert.ok(!btn("Save only").disabled && !btn("New pane").disabled, "the dialog came back dead after the timer fired under Help");
assert.ok(noticeText().includes("has not answered"), noticeText());
`)
}

// An answer that arrives while Help is open over the dialog is taken then: a
// success closes the dialog when it comes back, and a refusal leaves it usable.
func TestAnAnswerUnderHelpIsTakenThen(t *testing.T) {
	runFrontEnd(t, batonSetup+batonNotices+`
paletteRun("make baton");
h.recv(draft());
h.click(btn("Save only"));
h.click(h.$("overlay-help"));
bsaved({ cmd: "saveBaton" });
assert.ok(noticeText().includes("Saved baton"), noticeText());
h.click(h.$("overlay-back"));
assert.ok(h.$("overlay").hidden, "a dialog whose command finished under Help came back open");

paletteRun("make baton");
h.recv(draft());
box().value = draftText + "\nan edit\n";
box().oninput();
h.click(btn("Save only"));
h.click(h.$("overlay-help"));
berror({ cmd: "saveBaton" });
h.click(h.$("overlay-back"));
assert.ok(!btn("Save only").disabled && !btn("New pane").disabled, "a refusal under Help left the dialog dead");
assert.ok(box().value.includes("an edit"), "a refusal under Help lost the edits");
`)
}

// A late refusal of an earlier draft request must not close a dialog opened again.
func TestALateDraftRefusalDoesNotCloseAReopenedDialog(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
const first = lastReq("makeBaton");
h.key({ key: "Escape" });
paletteRun("make baton");
const second = lastReq("makeBaton");
assert.notStrictEqual(first, second, "the two requests have the same id");
h.recv({ type: "batonError", paneId: "p1", cmd: "makeBaton", req: first, error: "late" });
assert.ok(!h.$("overlay").hidden, "a late refusal of the first request closed the second dialog");
h.recv(draft());
assert.ok(box(), "the second draft was not drawn");
`)
}

// A late success, after the timer gave the dialog back, says so and keeps the
// edits made in the returned dialog.
func TestALateSuccessAfterATimeoutKeepsTheEdits(t *testing.T) {
	runFrontEnd(t, batonSetup+batonNotices+`
h.win.batonWaitMs = 40;
paletteRun("make baton");
h.recv(draft());
h.click(btn("Save only"));
await h.sleep(150);
box().value = draftText + "\nan edit made after the timeout\n";
box().oninput();
bsaved({ cmd: "saveBaton" });
assert.ok(!h.$("overlay").hidden, "a late success closed the dialog");
assert.ok(box().value.includes("an edit made after the timeout"), "a late success threw the edits away");
assert.ok(noticeText().includes("Saved baton") && noticeText().includes("still open with its edits"), noticeText());
`)
}

// The connection dropping ends the wait: the dialog comes back, the in-flight
// record is cleared so the command can be sent again, and the person is told.
func TestADroppedConnectionEndsTheWait(t *testing.T) {
	runFrontEnd(t, batonSetup+batonNotices+`
paletteRun("make baton");
h.recv(draft());
h.click(btn("Save only"));
assert.ok(btn("Save only").disabled);
const old = lastReq("saveBaton");
h.controls().pop().onclose();
assert.ok(!btn("Save only").disabled, "the dialog stayed waiting on a connection that was gone");
assert.ok(noticeText().includes("connection"), noticeText());
// An answer for the command that was waiting is nobody's any more.
h.recv({ type: "batonSaved", paneId: "p1", cmd: "saveBaton", req: old, id: "20261001-090000-0a1b2c", path: "/x", scrubbed: 0 });
assert.ok(!h.$("overlay").hidden, "an answer after the drop closed the dialog");
// And the command can be sent again.
h.click(btn("Save only"));
assert.strictEqual(h.commands().filter((c) => c.cmd === "saveBaton").length, 2, "the command could not be sent again");
`)
}

// The refusal is matched by its id, so a different pane id cannot leave the dialog
// waiting.
func TestARefusalIsMatchedByItsIDNotItsPane(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft());
h.click(btn("Save only"));
berror({ cmd: "saveBaton", paneId: "some-other-id" });
assert.ok(!btn("Save only").disabled, "a refusal naming another pane id left the dialog waiting");
`)
}

// A draft that is waited for is waited for only so long, and a dropped connection
// ends the wait too: the dialog says so and does not sit on "Reading" for good.
func TestAMakeBatonThatNeverAnswersIsGivenUp(t *testing.T) {
	runFrontEnd(t, batonSetup+`
h.win.batonWaitMs = 40;
paletteRun("make baton");
assert.ok(body.textContent.includes("Reading"), body.textContent);
await h.sleep(150);
assert.ok(!body.textContent.includes("Reading") && body.textContent.includes("did not send the draft"), body.textContent);
// A draft that comes late is still drawn.
h.recv(draft());
assert.ok(box(), "a late draft was not drawn");
`)
}

func TestADroppedConnectionEndsTheWaitForADraft(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
assert.ok(body.textContent.includes("Reading"));
h.controls().pop().onclose();
assert.ok(body.textContent.includes("dropped") && !body.textContent.includes("Reading"), body.textContent);
`)
}

// With nothing out, a dropped connection leaves a timer that is not its own alone.
func TestADroppedConnectionWithNothingOutLeavesTheDialogAlone(t *testing.T) {
	runFrontEnd(t, batonSetup+`
paletteRun("make baton");
h.recv(draft());
h.click(btn("Save only"));
bsaved({ cmd: "saveBaton" });
paletteRun("make baton");
h.recv(draft());
const before = body.textContent;
h.controls().pop().onclose();
assert.strictEqual(body.textContent, before, "the dialog was changed with nothing out");
`)
}
