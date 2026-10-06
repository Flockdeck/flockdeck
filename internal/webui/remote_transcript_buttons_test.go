package webui

import "testing"

// Export transcript and Show in folder write and open files on the machine
// Flockdeck runs on, and the server refuses both from a window reached through
// the relay. Such a window is not offered either button; the desk's own window
// still is.
func TestARemoteWindowIsNotOfferedExportOrShowInFolder(t *testing.T) {
	runFrontEnd(t, paletteRun+`
const exportBtn = () => [...h.doc.querySelector("div.pane-header").querySelector("div.pane-actions").children].find((b) => b.textContent === "⤓");
const fields = [{ label: "Pane id", value: "p1" }, { label: "Transcript file", value: "C:/rec/p1.jsonl" }];
const showInFolder = () => [...h.$("overlay-body").querySelectorAll("button")].find((b) => b.textContent === "Show in folder");

// The desk's own window has both.
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));
assert.ok(!exportBtn().hidden, "the desk window has no export button");
paletteRun("pane info");
h.recv({ type: "paneInfo", id: "p1", name: "shop", fields });
assert.ok(showInFolder(), "the desk window has no Show in folder button");
h.key({ key: "Escape" });
`)
	runFrontEnd(t, paletteRun+`
const exportBtn = () => [...h.doc.querySelector("div.pane-header").querySelector("div.pane-actions").children].find((b) => b.textContent === "⤓");
const fields = [{ label: "Pane id", value: "p1" }, { label: "Transcript file", value: "C:/rec/p1.jsonl" }];
const showInFolder = () => [...h.$("overlay-body").querySelectorAll("button")].find((b) => b.textContent === "Show in folder");

h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true });
h.recv(fixture({ panes: { p1: pane("p1") } }));
assert.ok(exportBtn().hidden, "a window reached through the relay is offered the export button");
paletteRun("pane info");
h.recv({ type: "paneInfo", id: "p1", name: "shop", fields });
assert.ok(!showInFolder(), "a window reached through the relay is offered Show in folder");
assert.ok(h.$("overlay-body").textContent.includes("C:/rec/p1.jsonl"), "the transcript file row went with the button");
`)
}
