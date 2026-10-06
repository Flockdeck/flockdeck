package webui

import (
	"strings"
	"testing"
)

// The toast (part C3): a column of messages at the bottom centre, each with
// its own timer, an error with a lead-in, an × and a stay, and a button that
// can go beside the words. #notice stays the place, and what it says of
// itself (aria-live, .error, hidden, its text) is what the older pins read.

func toastCSS(t *testing.T) string {
	t.Helper()
	css := readAsset(t, "app.css")
	return css
}

func TestToastsStackAndPlainNewsReplacesPlainNews(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
const toasts = () => n.children.filter((c) => c.classList.contains("toast"));

h.recv({ type: "notice", text: "Font size 13px", error: false });
h.recv({ type: "notice", text: "Font size 14px", error: false });
assert.strictEqual(toasts().length, 1, "plain news piled up instead of replacing plain news");
assert.ok(n.textContent.includes("14px") && !n.textContent.includes("13px"), "got: " + n.textContent);

// An error is not replaced by what follows it, and the place speaks for the newest.
h.recv({ type: "notice", text: "The remote rejected it", error: true });
h.recv({ type: "notice", text: "Theme: Light", error: false });
assert.strictEqual(toasts().length, 2, "the error did not stay under the news that followed it");
assert.strictEqual(n.getAttribute("aria-live"), "polite");
assert.ok(!n.classList.contains("error"), "the place says error for a newest message that is not one");
assert.ok(n.textContent.includes("rejected"));

// Never more than three: a fourth takes the oldest away.
h.recv({ type: "notice", text: "Second problem", error: true });
h.recv({ type: "notice", text: "Third problem", error: true });
assert.strictEqual(toasts().length, 3, "the stack is not capped");
assert.ok(!n.textContent.includes("rejected") && n.textContent.includes("Theme"), "the oldest was not the one to go");
assert.strictEqual(n.getAttribute("aria-live"), "assertive");
assert.ok(n.classList.contains("error"));
`)
}

func TestAnErrorToastHasADismissCrossAndALeadIn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
h.recv({ type: "notice", text: "The remote rejected it: pull first.", error: true, lead: "Could not push." });
h.recv({ type: "notice", text: "Fetched origin", error: false });

const err = n.children[0];
assert.ok(err.classList.contains("error"));
const lead = err.querySelector(".toast-lead");
assert.ok(lead && lead.textContent === "Could not push.", "no lead-in");
const x = err.querySelector(".toast-x");
assert.ok(x, "an error has no way to be put away by name");
assert.strictEqual(x.getAttribute("aria-label"), "Dismiss");
assert.strictEqual(x.tagName, "BUTTON");
assert.ok(!n.children[1].querySelector(".toast-x"), "plain news has a cross");

// The cross takes only its own toast, and the click does not reach the
// body's own dismissal a second time.
h.click(x);
assert.strictEqual(n.children.length, 1);
assert.ok(n.textContent.includes("Fetched"), "the cross took the wrong message");
assert.ok(!n.hidden);
// A click on a toast takes that toast and the place goes away with the last.
h.click(n.children[0]);
assert.ok(n.hidden, "the last toast went and the place stayed");
`)
}

func TestAToastCanCarryAnActionThatSendsACommand(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
h.recv({ type: "notice", text: "Removed worktree feat/search-api", error: false,
  action: { label: "Undo", send: { cmd: "worktreeRestore", path: "/code/x" } } });
const act = n.querySelector(".toast-act");
assert.ok(act && act.textContent === "Undo", "no action button");
// Plain news after it does not take it away.
h.recv({ type: "notice", text: "Theme: Light", error: false });
assert.strictEqual(n.children.length, 2, "news took an action offering a way back");
h.click(act);
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeRestore", path: "/code/x" }, "the action sent nothing");
assert.strictEqual(n.children.length, 1, "the toast stayed after its action was used");
assert.ok(n.textContent.includes("Theme"), "the action took the wrong toast");

// An action with no command is not offered.
h.recv({ type: "notice", text: "Nothing to undo", error: false, action: { label: "Undo" } });
assert.ok(!n.textContent.includes("Undo"), "a button that does nothing was drawn");
`)
}

func TestAToastAnnouncesWithoutReadingTheWholeStack(t *testing.T) {
	html := readAsset(t, "index.html")
	if !strings.Contains(html, `id="notice" role="status" aria-live="polite" aria-atomic="false"`) {
		t.Error("#notice is atomic, so each toast read the whole column again")
	}
}

func TestToastStyle(t *testing.T) {
	css := toastCSS(t)
	for _, want := range []string{".toast {", ".toast-act", ".toast-x", ".toast.error", "pointer-events: none"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css has no %q", want)
		}
	}
}

// A request for the user to allow a baton to go to another company is a notice
// with a lead, a button that sends the token back, and a life of its own.
func TestABatonApprovalNoticeSendsItsTokenBack(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
h.recv({ type: "notice", text: "An agent asked to send a baton to a different company.", error: false,
  lead: "Baton leaving its provider.", lifeMs: 45000,
  action: { label: "Send it", send: { cmd: "approveBaton", text: "tok123" } } });
assert.ok(n.textContent.includes("Baton leaving its provider."), "no lead");
const act = n.querySelector(".toast-act");
assert.ok(act && act.textContent === "Send it", "no button");
h.click(act);
assert.deepStrictEqual(h.commands().pop(), { cmd: "approveBaton", text: "tok123" }, "the token was not sent back");
`)
}

// An approval's notice stays for the whole of the wait whatever the pointer does,
// is not pushed out by other notices, and is taken away when the request ends.
func TestAnApprovalNoticeIsPinnedLongLivedAndWithdrawn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
const toasts = () => n.children.filter((c) => c.classList.contains("toast"));
h.recv({ type: "notice", text: "An agent asked to send a baton.", error: false, lead: "Baton leaving its provider.",
  lifeMs: 3000, pin: true, id: "ap1", action: { label: "Send it", send: { cmd: "approveBaton", text: "tok" } } });
// Other news does not push it out, however much of it there is.
for (let i = 0; i < 6; i++) h.recv({ type: "notice", text: "Error " + i, error: true });
assert.ok(toasts().some((c) => c.textContent.includes("Send it")), "a pinned approval was evicted by other notices");
// The pointer moving on it and leaving it does not shorten it.
const toast = toasts().find((c) => c.textContent.includes("Send it"));
h.dispatch(n, new h.Ev("pointermove", { target: toast }));
h.dispatch(n, new h.Ev("pointerleave", { target: toast }));
await h.sleep(1700);
assert.ok(toasts().includes(toast), "the pointer leaving shortened the approval notice");
// A withdrawal names it by id and takes it away.
h.recv({ type: "noticeWithdraw", id: "ap1" });
assert.ok(!toasts().some((c) => c.textContent.includes("Send it")), "a withdrawn approval is still on screen");
`)
}

// Approval toasts: pinned before room is made, so none is taken away by the ones
// after it; a pointer neither shortens nor stretches them; they go with the
// connection they were sent on; and what is said of them is not the answer to a
// commit that is waiting.
func TestApprovalToastsAreNotEvictedAndAFourthStacks(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
const toasts = () => n.children.filter((c) => c.classList.contains("toast"));
const ask = (id) => h.recv({ type: "notice", text: "Request " + id, error: false, lead: "Baton leaving its provider.", lifeMs: 45000,
  pin: true, id, kind: "approval", action: { label: "Send it " + id, send: { cmd: "approveBaton", text: "tok" + id } } });
ask("a1"); ask("a2"); ask("a3"); ask("a4");
for (let i = 0; i < 5; i++) h.recv({ type: "notice", text: "Error " + i, error: true });
const left = toasts().filter((c) => c.textContent.includes("Send it"));
assert.strictEqual(left.length, 4, "a fourth request, or the errors after them, took a request away: " + left.length);
// The later toasts are visible too: the newest error is on screen, and so are the two before it.
for (const i of [2, 3, 4]) assert.ok(toasts().some((c) => c.textContent.includes("Error " + i)), "Error " + i + " was dropped to make room");
assert.ok(!toasts().some((c) => c.textContent.includes("Error 0")), "the oldest error was kept past the cap");
// More requests than can be shown: the oldest is put away and that is said, visibly.
ask("a5"); ask("a6"); ask("a7");
assert.ok(toasts().some((c) => c.textContent.includes("Several baton requests are waiting")), "the notice that a request was put away is not on screen");
assert.strictEqual(toasts().filter((c) => c.textContent.includes("Send it")).length, 6, "more than six requests are on screen");
assert.ok(toasts().some((c) => c.textContent.includes("Send it a7")), "the newest request is not on screen");
`)
}

func TestAPointerNeitherShortensNorStretchesAnApprovalToast(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
const toasts = () => n.children.filter((c) => c.classList.contains("toast"));
const ask = (id, life) => h.recv({ type: "notice", text: "Request " + id, error: false, lifeMs: life, pin: true, id, kind: "approval",
  action: { label: "Send it " + id, send: { cmd: "approveBaton", text: "tok" + id } } });
const find = (id) => toasts().find((c) => c.textContent.includes("Request " + id));
ask("short", 700);
ask("long", 3000);
// Held on the short one: it still ends with its own life, and does not go on for 30 seconds.
h.dispatch(n, new h.Ev("pointermove", { target: find("short") }));
// Left from the long one: it is not cut to the 1.5 seconds a plain notice gets.
h.dispatch(n, new h.Ev("pointerleave", { target: find("long") }));
await h.sleep(1700);
assert.ok(!find("short"), "a pointer held on a request made it outlast the request");
assert.ok(find("long"), "a pointer leaving a request cut it short");
`)
}

func TestAnApprovalNoticeIsTakenAwayWithItsConnection(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
h.recv({ type: "notice", text: "Request", error: false, lifeMs: 45000, pin: true, id: "x", kind: "approval",
  action: { label: "Send it", send: { cmd: "approveBaton", text: "tok" } } });
assert.ok(n.textContent.includes("Send it"));
h.controls().pop().onclose();
assert.ok(!n.textContent.includes("Send it"), "a request stayed after the connection it was sent on went");
assert.ok(n.textContent.includes("Connection lost") && n.textContent.includes("Ask the agent again"), n.textContent);
`)
}

func TestAnApprovalNoticeDoesNotAnswerAWaitingCommit(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
const box = () => h.$("commit-message");
box().value = "webui: something";
box().oninput();
const commit = h.$("overlay-body").querySelectorAll("button").find((b) => /^Commit/.test(b.textContent));
h.click(commit);
// What is said of a request for the user's answer is not the commit's answer.
h.recv({ type: "notice", text: "Approved. Sending the baton.", error: false, kind: "approval" });
h.recv({ type: "notice", text: "Another window allowed that baton.", error: false, kind: "approval" });
// The commit fails, and the message that was typed is still there to try again.
h.recv({ type: "notice", text: "commit failed", error: true });
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
assert.strictEqual(box().value, "webui: something", "an approval notice dropped the commit message that was typed");
`)
}
