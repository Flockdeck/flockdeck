package webui

import "testing"

// Fan out and Todo are gone from the window. These cover what is left of
// them: no button, no palette entry, no shortcut, and settings and shortcut
// tables saved while they existed do not break the page.

func TestFanOutAndTodoHaveNoEntryPointLeft(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
for (const id of ["btn-fanout-history", "btn-todos"]) {
  assert.strictEqual(h.$(id), null, id + " is still in the rail");
}
const headerButtons = h.terms[0].host.parentElement.parentElement.querySelectorAll("button").map((b) => b.textContent);
assert.ok(!headerButtons.includes("⑂") && !headerButtons.includes("☑"), "a pane header still has a fan out or todo button: " + headerButtons.join(" "));

h.press("palette");
const labels = h.$("palette-list").children.filter((r) => r.classList.contains("pal-row")).map((r) => r.querySelector(".pal-label").textContent);
h.key({ key: "Escape" });
assert.ok(!labels.some((l) => /fan.?out|todo/i.test(l)), "the palette still offers: " + labels.filter((l) => /fan.?out|todo/i.test(l)).join(", "));

// Ctrl+Shift+X used to open the dialog.
const sent = h.commands().length;
h.key({ key: "X", code: "KeyX", ctrlKey: true, shiftKey: true });
assert.ok(h.$("overlay").hidden, "Ctrl+Shift+X opened a dialog");
assert.strictEqual(h.commands().slice(sent).length, 0, "Ctrl+Shift+X sent a command");
`)
}

// Preferences saved while Fan out existed carry a fanOut section, and a
// message the server no longer sends is not one the page has a handler for.
func TestSavedFanOutSettingsAndOldMessagesAreIgnored(t *testing.T) {
	runFrontEnd(t, `
h.hello({ dismissedTips: ["fanout"], fanOut: { sameTab: true } });
h.recv(fixture());
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: ["fanout"], fanOut: { sameTab: true } } });
for (const msg of [{ type: "fanoutPreview", paneId: "p1", tasks: ["one"] }, { type: "fanoutHistory", root: "C:/repo", items: [] },
  { type: "todos", root: "C:/repo", items: [] }, { type: "todoSaved", todo: { id: "t1" } }, { type: "routes", routes: [] }]) {
  h.recv(msg);
  assert.ok(h.$("overlay").hidden, msg.type + " opened a dialog");
}
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-general"));
assert.strictEqual(h.$("set-fanout-sametab"), null, "the Fan out setting is still offered");
assert.ok(h.$("set-auto-review-default"), "General lost its other settings");
`)
}
