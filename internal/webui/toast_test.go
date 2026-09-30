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
