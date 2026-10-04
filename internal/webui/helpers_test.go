package webui

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/helpers"
)

// The Helpers dialog: the palette entry, one row per helper with its status as
// a glyph and a word, the install confirmation, and the buttons each state
// offers. The harness has no CSS, so what is pinned is what the script draws.

const helperRows = `
const row = (over) => Object.assign({ id: "lens", name: "lens", summary: "Reads transcripts.", installed: "", signed: true,
  state: "notinstalled", allows: ["Runs as your user"] }, over || {});
const recvRows = (...rows) => h.recv({ type: "helpers", rows });
const openHelpers = () => { paletteRun("helper apps"); };
`

func TestThePaletteOpensTheHelpersDialog(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
assert.ok(!h.$("overlay").hidden, "the palette did not open the helpers dialog");
assert.strictEqual(h.$("overlay-title").textContent, "Helper apps");
assert.deepStrictEqual(h.commands().pop(), { cmd: "helpers" });
assert.ok(h.$("overlay-body").textContent.includes("Loading"));
recvRows(row());
const hint = h.$("overlay-body").textContent;
assert.ok(hint.includes("not sandboxed") && hint.includes("no sign-in") && hint.includes("Any program running as you"), "the dialog does not give the threat model: " + hint);
assert.ok(h.$("helper-install-lens"), "a helper that is not installed has no Install button");
`)
}

// Every state the server can report has a glyph and a word, and the word is
// in the text of the row: status is never colour alone.
func TestHelperStatusIsAGlyphAndAWord(t *testing.T) {
	// The words come from the Go side's own list, so a state added there and
	// forgotten here fails this test.
	states := []string{"notinstalled", "installing"}
	for _, s := range []helpers.State{helpers.StateStopped, helpers.StateStarting, helpers.StateRunning, helpers.StateStopping, helpers.StateUnresponsive, helpers.StateFailed} {
		states = append(states, string(s))
	}
	var list []string
	for _, s := range states {
		list = append(list, `"`+s+`"`)
	}
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
const words = { notinstalled: "Not installed", installing: "Installing", stopped: "Stopped", starting: "Starting",
  running: "Running", stopping: "Stopping", unresponsive: "Unresponsive", failed: "Failed" };
const glyphs = new Set();
for (const state of [`+strings.Join(list, ", ")+`]) {
  recvRows(row({ state, installed: state === "notinstalled" || state === "installing" ? "" : "0.4.0", url: "http://127.0.0.1:5000/", error: state === "failed" ? "it exited" : "" }));
  const text = h.$("helper-status-lens").textContent;
  assert.ok(words[state], "no word for " + state);
  assert.ok(text.endsWith(words[state]), state + ": the status is " + JSON.stringify(text));
  const glyph = text.slice(0, text.length - words[state].length).trim();
  assert.ok(glyph.length > 0 && !/[A-Za-z]/.test(glyph), state + ": no glyph in " + JSON.stringify(text));
  glyphs.add(glyph);
}
assert.ok(glyphs.size >= 4, "the glyphs do not tell the states apart: " + [...glyphs].join(" "));
// Trouble uses the same triangle an agent that needs you does.
recvRows(row({ state: "failed", installed: "0.4.0", error: "it exited" }));
assert.ok(h.$("helper-status-lens").textContent.startsWith("▲"));
recvRows(row({ state: "running", installed: "0.4.0", url: "http://127.0.0.1:5000/" }));
assert.ok(h.$("helper-status-lens").textContent.startsWith("●"));
`)
}

func TestHelperButtonsFollowTheState(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
const buttons = () => h.$("overlay-body").querySelectorAll("button").map((b) => b.id.replace(/^helper-/, "").replace(/-lens$/, ""));
const expect = (state, want, extra) => {
  recvRows(row(Object.assign({ state, installed: "0.4.0" }, extra || {})));
  assert.deepStrictEqual(buttons(), want, state + ": " + buttons().join(","));
};
recvRows(row());
assert.deepStrictEqual(buttons(), ["install"]);
expect("installing", [], { installed: "" });
expect("stopped", ["start", "repair", "remove"]);
expect("stopped", ["start", "update", "repair", "remove"], { update: "0.5.0" });
expect("starting", ["stop"]);
expect("running", ["open", "stop"]);
expect("stopping", []);
expect("unresponsive", ["stop"]);
expect("failed", ["start", "repair", "remove"], { error: "x", log: ["boom"] });
assert.ok(h.$("overlay-body").textContent.includes("boom"), "the log tail of a failure is not shown");
assert.ok(h.$("helper-start-lens").textContent === "Try again");

// Each is a real button, in the tab order, with a label.
recvRows(row({ state: "running", installed: "0.4.0" }));
for (const b of h.$("overlay-body").querySelectorAll("button")) {
  assert.strictEqual(b.tagName, "BUTTON");
  assert.ok(b.textContent.trim().length > 0, "a button has no label");
  assert.ok(b.tabIndex !== -1, "a button is out of the tab order");
}

h.click(h.$("helper-open-lens"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "helperOpen", id: "lens" });
h.click(h.$("helper-stop-lens"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "helperStop", id: "lens" });
recvRows(row({ state: "stopped", installed: "0.4.0" }));
h.click(h.$("helper-start-lens"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "helperStart", id: "lens" });
`)
}

func TestHelperNotesBesideTheStatus(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
recvRows(row({ state: "stopped", installed: "0.4.0", update: "0.5.0", signed: false }));
const text = h.$("overlay-body").textContent;
assert.ok(text.includes("Update available 0.5.0"), "no update note");
assert.ok(text.includes("Unsigned"), "no unsigned note");
assert.ok(text.includes("0.4.0"));
`)
}

func TestInstallShowsWhatItWillDoBeforeDownloading(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
recvRows(row());
h.click(h.$("helper-install-lens"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "helperPlan", id: "lens", text: "" });
assert.ok(h.$("helper-install-lens").disabled, "the button does not show a lookup is under way");

const url = "https://github.com/Flockdeck/lens/releases/download/v0.4.0/lens_0.4.0_windows_amd64.zip";
const sha = "a".repeat(64);
const before = h.commands().length;
h.recv({ type: "helperPlan", id: "lens", name: "lens", summary: "Reads transcripts.", version: "0.4.0", url, sha256: sha, signed: true,
  allows: ["Runs as your user", "Listens on 127.0.0.1 only"] });
const text = h.$("overlay-body").textContent;
for (const want of ["Install lens 0.4.0", "lens", "0.4.0", url, sha, "is checked against it when it is downloaded", "What it may do", "Runs as your user", "Listens on 127.0.0.1 only", "not sandboxed", "no sign-in"]) {
  assert.ok(text.includes(want), "the confirmation does not show " + want + ": " + text);
}
assert.strictEqual(h.commands().length, before, "something was sent before the install was confirmed");
assert.ok(!text.includes("archive matches it"), "the plan claims the archive matches before it is downloaded");
assert.ok(!h.$("helper-unsigned"), "a signed release offers the unsigned override");
assert.ok(!h.$("helper-confirm").disabled);

h.click(h.$("helper-confirm"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "helperInstall", id: "lens", text: "0.4.0", sha256: sha, unsigned: false, confirmed: true });
assert.ok(h.$("helper-status-lens"), "the dialog did not return to the list");
`)
}

func TestCancelInstallSendsNoInstall(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
recvRows(row());
h.click(h.$("helper-install-lens"));
h.recv({ type: "helperPlan", id: "lens", name: "lens", summary: "s", version: "0.4.0", url: "u", sha256: "b".repeat(64), signed: true, allows: [] });
h.click(h.$("helper-cancel"));
assert.ok(!h.commands().some((c) => c.cmd === "helperInstall"), "cancel installed");
assert.deepStrictEqual(h.commands().pop(), { cmd: "helpers" });
`)
}

func TestUnsignedReleaseNeedsTheOverrideChosen(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
recvRows(row());
h.click(h.$("helper-install-lens"));
const sha = "c".repeat(64);
h.recv({ type: "helperPlan", id: "lens", name: "lens", summary: "s", version: "0.4.0", url: "u", sha256: sha, signed: false, allows: ["x"] });
const text = h.$("overlay-body").textContent;
assert.ok(text.includes("Not signed"), "the signature state is not shown");
assert.ok(text.includes("does not show who built it"), "the override does not say what the hash proves");
assert.ok(text.includes(sha), "the override screen does not show the hash");
const box = h.$("helper-unsigned");
assert.ok(box, "no override checkbox");
assert.ok(h.$("helper-confirm").disabled, "Install is available before the override is chosen");
const sent = h.commands().length;
h.click(h.$("helper-confirm"));
assert.strictEqual(h.commands().length, sent, "a disabled Install sent something");
box.checked = true;
box.onchange();
assert.ok(!h.$("helper-confirm").disabled, "choosing the override did not enable Install");
h.click(h.$("helper-confirm"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "helperInstall", id: "lens", text: "0.4.0", sha256: sha, unsigned: true, confirmed: true });
`)
}

func TestARefusedLookupHasNoInstall(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
recvRows(row());
h.click(h.$("helper-install-lens"));
h.recv({ type: "helperPlan", id: "lens", error: "checksums.txt does not carry a valid signature. Nothing was installed and this cannot be overridden.", fatal: true });
assert.ok(h.$("overlay-body").textContent.includes("cannot be overridden"));
assert.ok(!h.$("helper-confirm"), "a refused release offers Install");
assert.ok(!h.$("helper-unsigned"), "a bad signature offers the override");
h.click(h.$("helper-back"));
assert.ok(h.$("helper-install-lens"), "Back did not return to the list");
`)
}

func TestRemoveAsksAndDataNeedsASecondConfirmation(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
recvRows(row({ state: "stopped", installed: "0.4.0", hasData: true, dataDir: "/state/apps/lens/data" }));
h.click(h.$("helper-remove-lens"));
let text = h.$("overlay-body").textContent;
assert.ok(text.includes("Remove lens") && text.includes("/state/apps/lens/data") && text.includes("kept"), text);
assert.ok(!h.commands().some((c) => c.cmd === "helperUninstall"), "removed without asking");
h.click(h.$("helper-cancel"));
assert.ok(!h.commands().some((c) => c.cmd === "helperUninstall"));

recvRows(row({ state: "stopped", installed: "0.4.0", hasData: true, dataDir: "/state/apps/lens/data" }));
h.click(h.$("helper-remove-lens"));
h.click(h.$("helper-remove-confirm"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "helperUninstall", id: "lens", purge: false });

recvRows(row({ state: "stopped", installed: "0.4.0", hasData: true, dataDir: "/state/apps/lens/data" }));
h.click(h.$("helper-remove-lens"));
h.click(h.$("helper-remove-purge"));
text = h.$("overlay-body").textContent;
assert.ok(text.includes("cannot be undone"), "deleting data does not warn: " + text);
assert.ok(!h.commands().slice(-1).some((c) => c.cmd === "helperUninstall" && c.purge), "the data was deleted on the first click");
h.click(h.$("helper-remove-confirm"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "helperUninstall", id: "lens", purge: true });
`)
}

func TestAStatusChangeIsAnnounced(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
const said = () => h.$("announcer").children.map((c) => c.textContent).join("|");
recvRows(row({ state: "stopped", installed: "0.4.0" }));
assert.ok(!said().includes("lens"), "the first sight of a helper was announced");
recvRows(row({ state: "running", installed: "0.4.0" }));
assert.ok(said().includes("lens is running"), said());
recvRows(row({ state: "running", installed: "0.4.0" }));
assert.strictEqual(said().split("lens is running").length, 2, "an unchanged state was announced again");
recvRows(row({ state: "failed", installed: "0.4.0", error: "it exited" }));
assert.ok(said().includes("lens is failed: it exited"), said());
`)
}

// The dialog is a listed surface, so the generic machinery that closes it and
// returns the keyboard applies.
func TestTheHelpersDialogClosesWithEscape(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
recvRows(row());
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape did not close the helpers dialog");
// An answer that arrives after it was closed does not bring it back.
recvRows(row({ state: "running", installed: "0.4.0" }));
assert.ok(h.$("overlay").hidden, "a late answer reopened the dialog");
`)
}

// The markup the dialog is built from, read as text: the palette entry exists
// in the key table the front end is given, and the script has a word for every
// state the supervisor has.
func TestHelpersDialogSource(t *testing.T) {
	js := readAsset(t, "app.js")
	for _, state := range []helpers.State{helpers.StateStopped, helpers.StateStarting, helpers.StateRunning, helpers.StateStopping, helpers.StateUnresponsive, helpers.StateFailed} {
		if !strings.Contains(js, string(state)+`: [`) {
			t.Errorf("app.js has no glyph and word for the %s state", state)
		}
	}
	css := readAsset(t, "app.css")
	for _, class := range []string{".helper-warning", ".helper-facts", ".helper-log"} {
		if !strings.Contains(css, class) {
			t.Errorf("app.css has no %s", class)
		}
	}
}

// A window reached through the relay is not offered the dialog at all, and
// is not left on "Loading..." by a server that refuses it.
func TestARelayWindowHasNoHelperAppsEntry(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.recv({ type: "hello", keys: h.keyTable().filter((k) => k.id !== "helpers"), prefs: { helpSeen: true, dismissedTips: [] }, remote: true });
h.recv(fixture());
h.press("palette");
const input = h.$("palette-input");
input.value = "helper";
input.oninput();
const labels = h.$("palette-list").children.filter((r) => r.classList.contains("pal-row")).map((r) => r.querySelector(".pal-label").textContent);
assert.ok(!labels.some((l) => l.includes("Helper")), "a relay window is offered Helper apps: " + labels.join(", "));
h.key({ key: "Escape" });
// And if the action is reached anyway it says why, rather than waiting for an answer.
const before = h.commands().length;
paletteRun("helper apps");
assert.ok(h.$("overlay").hidden, "the dialog opened in a relay window");
assert.strictEqual(h.commands().length, before, "a relay window asked the server for helpers");
`)
}

// A repair is the installed version again, and says so before anything is
// downloaded.
func TestARepairIsShownAsOne(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
recvRows(row({ state: "stopped", installed: "0.4.0" }));
h.click(h.$("helper-repair-lens"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "helperPlan", id: "lens", text: "0.4.0" });
h.recv({ type: "helperPlan", id: "lens", name: "lens", summary: "s", version: "0.4.0", url: "u", sha256: "d".repeat(64), signed: true, repair: true, installed: "0.4.0", allows: [] });
const text = h.$("overlay-body").textContent;
assert.ok(text.includes("Repair lens 0.4.0"), text);
assert.ok(text.includes("replaced by a checked one"), text);
`)
}

// A helper whose port owner the platform could not confirm says so, in words.
func TestAnUnverifiedPortOwnerIsShown(t *testing.T) {
	runFrontEnd(t, paletteRun+helperRows+`
h.hello();
h.recv(fixture());
openHelpers();
recvRows(row({ state: "running", installed: "0.4.0", url: "http://127.0.0.1:5000/", owner: "verified" }));
assert.ok(!h.$("overlay-body").textContent.includes("port owner not verified"), "a verified owner is flagged");
recvRows(row({ state: "running", installed: "0.4.0", url: "http://127.0.0.1:5000/", owner: "unverified" }));
assert.ok(h.$("helper-owner-lens").textContent === "port owner not verified", h.$("overlay-body").textContent);
recvRows(row({ state: "stopped", installed: "0.4.0", owner: "unverified" }));
assert.ok(!h.$("overlay-body").textContent.includes("port owner not verified"), "a stopped helper is flagged");
`)
}
