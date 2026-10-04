package webui

import "testing"

// Pane info opens from the palette and from the button in the pane header, asks
// the server for the pane's ids, draws every value as text and never as markup,
// copies one value or all of them, and says "not set" for what a pane lacks.
func TestPaneInfoOpensFromPaletteAndHeaderAndShowsValuesAsText(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));

// The header button is there, named, reachable, and does not replace any other.
const header = h.doc.querySelector("div.pane-header");
const buttons = [...header.querySelector("div.pane-actions").children];
const info = buttons.find((b) => b.getAttribute("aria-label") === "Pane info");
assert.ok(info, "the pane header has no Pane info button");
assert.strictEqual(info.dataset.tip, "Pane info");
assert.strictEqual(info.tagName, "BUTTON");

const evil = '<img src=x onerror="window.pwned=1">';
const fields = [
  { label: "Pane id", value: "p1", note: "What flockdeck commands take" },
  { label: "Pane name", value: evil },
  { label: "Conversation id", value: "0a1b2c3d-1111", note: "It changes after /clear" },
  { label: "Peer name", value: "" },
  { label: "Process id", value: "4242" },
];
for (const how of ["button", "palette"]) {
  if (how === "button") h.click(info); else paletteRun("pane info");
  assert.strictEqual(h.$("overlay-title").textContent, "Pane info", how);
  assert.deepStrictEqual(h.commands().pop(), { cmd: "paneInfo", id: "p1" }, how);
  h.recv({ type: "paneInfo", id: "p1", name: "shop", fields });
  const body = h.$("overlay-body");
  const text = body.textContent;
  assert.ok(text.includes(evil), "the value was not shown as text: " + text);
  assert.strictEqual(body.querySelectorAll("img").length, 0, "a value became markup");
  assert.strictEqual(h.win.pwned, undefined);
  assert.ok(text.includes("It changes after /clear"), "the note is missing");
  const rows = [...body.querySelectorAll(".pi-row")];
  assert.strictEqual(rows.length, fields.length);
  const peer = rows.find((r) => r.dataset.key === "Peer name");
  assert.strictEqual(peer.querySelector(".pi-value").textContent, "not set");
  assert.strictEqual(peer.querySelectorAll("button").length, 0, "an unset value has a copy button");
  h.key({ key: "Escape" });
}

// Copy one value.
h.click(info);
h.recv({ type: "paneInfo", id: "p1", name: "shop", fields });
const conv = [...h.$("overlay-body").querySelectorAll(".pi-row")].find((r) => r.dataset.key === "Conversation id");
const copy = conv.querySelector("button");
assert.strictEqual(copy.getAttribute("aria-label"), "Copy conversation id");
h.click(copy);
await h.sleep(0);
assert.strictEqual(h.win._copied, "0a1b2c3d-1111");

// Copy all, as plain text with not set where there is nothing.
h.click(h.$("pane-info-copy-all"));
await h.sleep(0);
assert.strictEqual(h.win._copied, "Pane id: p1\nPane name: " + evil + "\nConversation id: 0a1b2c3d-1111\nPeer name: not set\nProcess id: 4242");

// An answer for another pane is not drawn here.
h.recv({ type: "paneInfo", id: "other", name: "x", fields: [{ label: "Pane id", value: "zzz" }] });
assert.ok(!h.$("overlay-body").textContent.includes("zzz"));
`)
}

// Find pane is in the palette, sends what is typed to the server, draws the
// results as text with where each pane is and what matched, says so plainly when
// nothing matches, goes to the chosen pane in whatever project it is in, and
// closes on Escape.
func TestFindPaneSearchesAndGoesToThePane(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));
paletteRun("find pane");
assert.strictEqual(h.$("overlay-title").textContent, "Find pane");
assert.deepStrictEqual(h.commands().pop(), { cmd: "findPane", text: "" });
const input = h.$("find-pane-input");
assert.ok(input, "no search field");
assert.ok(h.doc.activeElement === input, "the keyboard is not in the search field");

input.value = "0A1B";
input.oninput();
assert.deepStrictEqual(h.commands().pop(), { cmd: "findPane", text: "0A1B" });

const evil = '<b onclick="x">shop</b>';
h.recv({ type: "paneMatches", query: "0A1B", items: [
  { paneId: "p9", tabId: "t7", root: "C:/other", name: evil, project: "Other", tab: "Fixes",
    matched: [{ label: "Conversation id", value: "0a1b2c3d-1111" }] },
] });
const body = h.$("overlay-body");
assert.ok(body.textContent.includes(evil), "the name was not shown as text");
assert.strictEqual(body.querySelectorAll("b").length, 0, "a name became markup");
const rows = body.querySelectorAll(".pf-row");
assert.strictEqual(rows.length, 1);
assert.ok(rows[0].textContent.includes("Other · Fixes"), rows[0].textContent);
assert.ok(rows[0].textContent.includes("Matched conversation id 0a1b2c3d-1111"), rows[0].textContent);

// A late answer to an earlier query is ignored.
h.recv({ type: "paneMatches", query: "0", items: [] });
assert.strictEqual(body.querySelectorAll(".pf-row").length, 1);

// Nothing found says so.
input.value = "zzz";
input.oninput();
h.recv({ type: "paneMatches", query: "zzz", items: [] });
assert.ok(body.textContent.includes("No pane matches “zzz”."), body.textContent);

// Choosing goes to the pane, in its own project, and closes the search.
input.value = "0A1B";
input.oninput();
h.recv({ type: "paneMatches", query: "0A1B", items: [
  { paneId: "p9", tabId: "t7", root: "C:/other", name: "shop", project: "Other", tab: "Fixes", matched: [] }] });
h.click(body.querySelector(".pf-row"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "revealPane", root: "C:/other", node: "t7", id: "p9" });
assert.ok(h.$("overlay").hidden, "choosing a result left the search open");

// Enter takes the first result; Escape closes.
paletteRun("find pane");
h.recv({ type: "paneMatches", query: "", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", name: "a", project: "repo", tab: "one" }] });
h.key({ key: "Enter", target: h.$("find-pane-input") });
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape did not close the search");
`)
}

// A pane closed before its info arrived is said where "Loading…" was, rather
// than leaving the dialog on it.
func TestPaneInfoForAPaneThatWentSaysSo(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));
paletteRun("pane info");
assert.ok(h.$("overlay-body").textContent.includes("Loading"), "not loading");
h.recv({ type: "notice", text: "That pane is no longer open", error: true });
const text = h.$("overlay-body").textContent;
assert.ok(!text.includes("Loading"), "still loading: " + text);
assert.ok(text.includes("That pane is no longer open"), text);
`)
}

// Enter acts on the results of what is in the field, not on the ones still
// drawn for the query before it.
func TestFindPaneEnterWaitsForTheResultsOfWhatWasTyped(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));
paletteRun("find pane");
const input = h.$("find-pane-input");
const item = (id) => ({ paneId: id, tabId: "t" + id, root: "C:/repo", name: id, project: "repo", tab: "one", matched: [] });
h.recv({ type: "paneMatches", query: "", items: [item("old")] });
input.value = "new";
input.oninput();
h.commands().length = 0;
h.key({ key: "Enter", target: input });
assert.ok(!h.commands().some((c) => c.cmd === "revealPane"), "Enter took a row drawn for the earlier query");
assert.ok(!h.$("overlay").hidden, "the search closed on a stale Enter");
h.recv({ type: "paneMatches", query: "new", items: [item("fresh")] });
h.key({ key: "Enter", target: input });
assert.deepStrictEqual(h.commands().pop(), { cmd: "revealPane", root: "C:/repo", node: "tfresh", id: "fresh" });
`)
}

// Both views have a key of their own, taken from the key table.
func TestPaneInfoAndFindPaneHaveKeys(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));
h.press("paneInfo");
assert.strictEqual(h.$("overlay-title").textContent, "Pane info");
assert.deepStrictEqual(h.commands().pop(), { cmd: "paneInfo", id: "p1" });
h.key({ key: "Escape" });
h.press("findPane");
assert.strictEqual(h.$("overlay-title").textContent, "Find pane");
`)
}

// The transcript file row carries the action the pane header used to: it sends
// the existing reveal command for this pane, and with no file it is off and
// says why rather than being left on to fail.
func TestPaneInfoShowsTheTranscriptFileInTheFolder(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));
paletteRun("pane info");
const withFile = [
  { label: "Pane id", value: "p1" },
  { label: "Transcript file", value: "C:/rec/2026-10-01-0a1b2c3d.jsonl" },
];
h.recv({ type: "paneInfo", id: "p1", name: "shop", fields: withFile });
const row = () => [...h.$("overlay-body").querySelectorAll(".pi-row")].find((r) => r.dataset.key === "Transcript file");
const show = [...row().querySelectorAll("button")].find((b) => b.textContent === "Show in folder");
assert.ok(show, "the transcript row has no Show in folder button");
assert.ok(!show.disabled);
assert.ok(/file manager/.test(show.dataset.tip), show.dataset.tip);
assert.strictEqual([...h.$("overlay-body").querySelectorAll("button")].filter((b) => b.textContent === "Show in folder").length, 1,
  "another row has the button");
h.click(show);
assert.deepStrictEqual(h.commands().pop(), { cmd: "revealTranscript", id: "p1" });

// No file yet: off, with the reason in the row, and nothing sent.
h.recv({ type: "paneInfo", id: "p1", name: "shop", fields: [{ label: "Transcript file", value: "" }] });
const off = [...row().querySelectorAll("button")].find((b) => b.textContent === "Show in folder");
assert.ok(off.disabled, "the button is on with nothing to show");
assert.ok(row().textContent.includes("Nothing to show yet"), row().textContent);
const sent = h.commands().length;
h.click(off);
assert.strictEqual(h.commands().length, sent, "a disabled button sent a command");

// A shell has no transcript row, so no button.
h.recv({ type: "paneInfo", id: "p1", name: "sh", fields: [{ label: "Pane id", value: "p1" }] });
assert.ok(![...h.$("overlay-body").querySelectorAll("button")].some((b) => b.textContent === "Show in folder"));
`)
}
