package webui

import "testing"

// The conflict radar's chip names the other pane, in the plain text colour's
// own element beside the git marks, and its bubble lists the files and says it
// is a prediction. It is not there until the server sends a conflict, comes down
// when the server stops sending it, and is not rebuilt by pushes that say
// nothing new.
func TestThePaneHeaderShowsAPredictedConflict(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));
const wrap = h.terms[0].host.parentElement.parentElement;
const chip = () => wrap.querySelector(".pane-conflict");
assert.strictEqual(chip().textContent, "", "the chip showed with no conflict reported");

const conflicts = [{ id: "p2", name: "billing", branch: "feature/billing", paths: ["internal/server/control.go", "app.js"] }];
h.recv(fixture({ panes: { p1: pane("p1", { conflicts }) } }));
assert.ok(chip().textContent.includes("⚠") && chip().textContent.includes("billing"),
  "the chip does not name the other pane: " + chip().textContent);
const tip = chip().dataset.tip || "";
assert.ok(tip.includes("internal/server/control.go") && tip.includes("app.js") && tip.includes("feature/billing"),
  "the bubble does not list the files and the branch: " + tip);
assert.ok(/prediction|predicts|predicted/i.test(tip), "the bubble does not say it is a prediction: " + tip);
assert.ok(!/safe to merge/i.test(tip), "the bubble promises a merge is safe: " + tip);
assert.strictEqual(chip().getAttribute("role"), "button");
assert.ok(chip().getAttribute("aria-label").includes("billing"));

// The chip sits beside the git marks in the header, and is not the status dot.
const header = wrap.querySelector(".pane-header");
const kids = [...header.children];
assert.strictEqual(kids.indexOf(chip()), kids.indexOf(wrap.querySelector(".pane-git")) + 1, "the chip is not right after the git marks");

// Several panes: the first named, the rest counted, all in the bubble.
h.recv(fixture({ panes: { p1: pane("p1", { conflicts: conflicts.concat([{ id: "p3", name: "search", paths: ["x.go"], more: 4 }]) }) } }));
assert.ok(chip().textContent.includes("billing") && chip().textContent.includes("+1"), "got: " + chip().textContent);
assert.ok(chip().dataset.tip.includes("search") && chip().dataset.tip.includes("and 4 more"), chip().dataset.tip);

// A pane told of the first five and that there are three more.
h.recv(fixture({ panes: { p1: pane("p1", { conflicts, conflictsMore: 3 }) } }));
assert.ok(chip().textContent.includes("+3"), "got: " + chip().textContent);
assert.ok(chip().dataset.tip.includes("3 more panes are not listed"), chip().dataset.tip);
assert.strictEqual(chip().getAttribute("tabindex"), "0", "the chip cannot be reached from the keyboard");
assert.ok(!/no textual conflicts predicted|safe to merge/i.test(chip().dataset.tip), chip().dataset.tip);
assert.ok(!/clears by itself/i.test(chip().dataset.tip), "the bubble says it clears by itself: " + chip().dataset.tip);
assert.ok(/Git LFS/.test(chip().dataset.tip) && /merge driver/.test(chip().dataset.tip), "the bubble lacks the filter and merge driver caveat: " + chip().dataset.tip);
assert.ok(/history does not reach the base/.test(chip().dataset.tip), "the bubble does not say a pane whose history does not reach the base is not checked: " + chip().dataset.tip);
assert.ok(/modification time/.test(chip().dataset.tip) && /split index/.test(chip().dataset.tip), "the bubble says nothing is changed without the mtime caveat: " + chip().dataset.tip);
assert.ok(!/so nothing in either checkout is changed/.test(chip().dataset.tip), "the bubble says nothing is changed: " + chip().dataset.tip);

// A push that says the same again builds nothing.
h.recv(fixture({ panes: { p1: pane("p1", { conflicts }) } }));
const before = h.made();
for (let i = 0; i < 5; i++) h.recv(fixture({ panes: { p1: pane("p1", { conflicts }) } }));
assert.strictEqual(h.made() - before, 0, "unchanged pushes redrew the chip");

// And it goes when the server stops sending it.
h.recv(fixture({ panes: { p1: pane("p1") } }));
assert.strictEqual(chip().textContent, "", "the chip stayed after the conflict cleared");
assert.ok(!chip().dataset.tip, "its bubble stayed");
`)
}

// Settings has the one switch, off until it is turned on, and sends the command
// when it is.
func TestSettingsHasTheConflictRadarSwitch(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-general"));
const sw = h.$("set-radar");
assert.ok(sw, "no conflict radar switch in General");
assert.strictEqual(sw.getAttribute("aria-checked"), "false", "the radar starts on");
const row = sw.closest(".set-row").textContent;
assert.ok(/off by default/i.test(row), "the row does not say it is off by default: " + row);
assert.ok(row.length < 1100, "the row is " + row.length + " characters; the detail belongs on the Worktrees help page");
assert.ok(!/no textual conflicts predicted/i.test(row) && !/safe to merge/i.test(row), "the wording promises that no chip means no conflict: " + row);
assert.ok(/no chip is not a promise/i.test(row), "the row does not say what no chip can mean: " + row);
assert.ok(/relay/i.test(row) && /branches/i.test(row) && /20 file paths/.test(row) && /never file contents/.test(row), "the row does not say what reaches windows through the relay: " + row);
assert.ok(/phone/i.test(row), "the row does not say a paired phone sees them: " + row);
assert.ok(/100 MB/.test(row) && /25 MB/.test(row), "the row does not say which panes are skipped for size: " + row);
assert.ok(/15 seconds/.test(row) && !/every few seconds/i.test(row), "the row's timing is wrong: " + row);
assert.ok(/2\.38/.test(row), "the row does not say which git it needs: " + row);
assert.ok(/Worktrees/.test(row), "the row does not point at the help page that has the detail: " + row);

const before = h.commands().length;
h.click(sw);
assert.deepStrictEqual(h.commands().slice(before).pop(), { cmd: "conflictRadar", kind: "on" });
`)
}

// Where git is missing or too old the switch is dead and says why.
func TestTheConflictRadarSwitchSaysWhyItIsUnavailable(t *testing.T) {
	runFrontEnd(t, `
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [], conflictRadar: true },
  radarUnavailable: "it needs git 2.38 or newer, and this is git 2.34" });
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-general"));
const sw = h.$("set-radar");
assert.ok(sw.disabled, "the switch can be pressed with no git to do it");
assert.strictEqual(sw.getAttribute("aria-checked"), "false", "it shows on though it cannot run");
const why = h.$("set-radar-why");
assert.ok(why && why.textContent.includes("git 2.38"), "the reason is not shown: " + (why && why.textContent));
const before = h.commands().length;
h.click(sw);
assert.strictEqual(h.commands().length, before, "a dead switch sent a command");
`)
}

// A hello sent before the version of git was read shows the switch as being
// checked, and the message that follows settles it either way.
func TestTheConflictRadarSwitchIsCheckingUntilTheVersionIsKnown(t *testing.T) {
	runFrontEnd(t, `
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, radarChecking: true });
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-general"));
assert.ok(h.$("set-radar").disabled, "the switch can be pressed before git has been read");
assert.ok(/checking/i.test(h.$("set-radar-why").textContent), "it does not say it is checking");

h.recv({ type: "radarSupport", unavailable: "it needs git 2.38 or newer, and this is git 2.34" });
assert.ok(h.$("set-radar").disabled, "the switch is live on a git that cannot do it");
assert.ok(h.$("set-radar-why").textContent.includes("git 2.38"), h.$("set-radar-why").textContent);

// And on a git that can, the same message frees it.
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, radarChecking: true });
h.recv({ type: "radarSupport" });
assert.ok(!h.$("set-radar").disabled, "the switch stayed dead after git was found fit");
assert.ok(!h.$("set-radar-why") || !h.$("settings-pane").contains(h.$("set-radar-why")), "the checking note stayed");
`)
}

// Pressing the chip opens a list of what is predicted: each pane, its branch, the
// files, and what the prediction is. Enter on it does the same, Escape puts the
// list away and gives the keyboard back to the chip, and a press elsewhere puts it
// away too. It follows the next push, and goes when the conflict does.
func TestTheConflictChipOpensAListOfTheFiles(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const conflicts = [
  { id: "p2", name: "billing", branch: "feature/billing", paths: ["internal/server/control.go", "app.js"], more: 3 },
  { id: "p3", name: "search", paths: ["x.go"] },
];
h.recv(fixture({ panes: { p1: pane("p1", { conflicts, conflictsMore: 2 }) } }));
const wrap = h.terms[0].host.parentElement.parentElement;
const chip = () => wrap.querySelector(".pane-conflict");
assert.ok(!h.$("conflict-pop"), "the list was open before the chip was pressed");

h.click(chip());
let pop = h.$("conflict-pop");
assert.ok(pop, "pressing the chip did not open a list");
assert.strictEqual(pop.getAttribute("role"), "dialog");
const text = pop.textContent;
for (const want of ["billing", "feature/billing", "internal/server/control.go", "app.js", "and 3 more", "search", "x.go", "2 more panes are not listed", "committed or not"]) {
  assert.ok(text.includes(want), "the list lacks " + want + ": " + text);
}
assert.ok(!/safe to merge|no textual conflicts predicted/i.test(text), "the list promises: " + text);
assert.strictEqual(chip().getAttribute("aria-expanded"), "true");
assert.ok(h.doc.activeElement === pop, "the keyboard did not go into the list");

// Escape puts it away and the chip has the keyboard again.
h.key({ key: "Escape" });
assert.ok(!h.$("conflict-pop") || !h.doc.body.contains(h.$("conflict-pop")), "Escape left the list open");
assert.ok(h.doc.activeElement === chip(), "the keyboard did not go back to the chip");
assert.strictEqual(chip().getAttribute("aria-expanded"), "false");

// Enter on the chip opens it, and pressing the chip again closes it.
h.key({ key: "Enter" });
assert.ok(h.$("conflict-pop") && h.doc.body.contains(h.$("conflict-pop")), "Enter on the chip did not open the list");
h.click(chip());
assert.ok(!h.doc.body.contains(h.$("conflict-pop") || h.doc.createElement("div")), "pressing the chip again did not close it");

// It follows a push that moves the conflict, and goes when the conflict does.
h.click(chip());
h.recv(fixture({ panes: { p1: pane("p1", { conflicts: [{ id: "p4", name: "billing2", paths: ["new.go"] }] }) } }));
assert.ok(h.$("conflict-pop").textContent.includes("new.go") && !h.$("conflict-pop").textContent.includes("app.js"), "the open list did not follow the push");
h.recv(fixture({ panes: { p1: pane("p1") } }));
assert.ok(!h.doc.body.contains(h.$("conflict-pop") || h.doc.createElement("div")), "the list stayed after the conflict cleared");
`)
}

func TestAPressElsewherePutsTheConflictListAway(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1", { conflicts: [{ id: "p2", name: "billing", paths: ["a.go"] }] }) } }));
const wrap = h.terms[0].host.parentElement.parentElement;
h.click(wrap.querySelector(".pane-conflict"));
assert.ok(h.doc.body.contains(h.$("conflict-pop")), "setup: the list is not open");
// A press on the list itself leaves it.
h.dispatch(h.$("conflict-pop"), new h.Ev("pointerdown", { target: h.$("conflict-pop") }));
assert.ok(h.doc.body.contains(h.$("conflict-pop")), "a press inside the list closed it");
const name = wrap.querySelector(".pane-name") || wrap.querySelector(".pane-header");
h.dispatch(name, new h.Ev("pointerdown", { target: name }));
assert.ok(!h.doc.body.contains(h.$("conflict-pop")), "a press elsewhere left the list open");
`)
}

const popSetup = `
h.hello();
const mk = (conflicts, more) => fixture({ panes: { p1: pane("p1", { conflicts, conflictsMore: more || 0 }), p2: pane("p2") } });
const base = [{ id: "p3", name: "billing", branch: "feature/billing", paths: ["a.go"] }];
h.recv(mk(base));
const wrap = h.terms[0].host.parentElement.parentElement;
const chip = () => wrap.querySelector(".pane-conflict");
const open = () => !!h.$("conflict-pop") && h.doc.body.contains(h.$("conflict-pop"));
`

// The list goes with its pane, and with the tab it was opened on.
func TestTheConflictListGoesWithItsPaneAndItsTab(t *testing.T) {
	runFrontEnd(t, popSetup+`
h.click(chip());
assert.ok(open(), "setup: the list is not open");
// Another tab comes on screen: the list is not for it.
h.recv(Object.assign(mk(base), { activeTab: "t2" }));
assert.ok(!open(), "the list stayed open over another tab");

h.recv(mk(base));
h.click(chip());
assert.ok(open(), "setup: the list did not open again");
// The pane is closed.
h.recv(fixture({ activeTab: "t2", tabs: [{ id: "t2", title: "two", focus: "p2", zoom: false, attention: false,
  root: { id: "n2", pane: "p2", weight: 1 } }], panes: { p2: pane("p2") } }));
assert.ok(!open(), "the list stayed open after its pane was closed");
`)
}

// Escape closes it from wherever the keyboard is, and the chip has it again.
func TestEscapeClosesTheConflictListFromAnywhere(t *testing.T) {
	runFrontEnd(t, popSetup+`
h.click(chip());
assert.ok(open(), "setup: the list is not open");
assert.strictEqual(chip().getAttribute("aria-controls"), "conflict-pop");
// The keyboard is somewhere else entirely.
const elsewhere = h.$("btn-settings");
elsewhere.focus();
assert.ok(h.doc.activeElement === elsewhere, "setup: focus did not move");
h.key({ key: "Escape" });
assert.ok(!open(), "Escape with the keyboard elsewhere left the list open");
assert.ok(h.doc.activeElement === chip(), "the keyboard did not go back to the chip");
assert.strictEqual(chip().getAttribute("aria-expanded"), "false");
`)
}

// Names and paths are text: markup, quotes and a very long name are shown as they
// are, never as elements, and the chip's label stays short.
func TestTheConflictListShowsHostileNamesAsText(t *testing.T) {
	runFrontEnd(t, popSetup+`
const evil = "<img src=x onerror=alert(1)>\"'&<script>boom()</script>";
const long = "n".repeat(5000);
h.recv(mk([
  { id: "p3", name: evil, branch: "b<i>x</i>", paths: ["<b>file</b>.go", "a\"b'c.go"] },
  { id: "p4", name: long, paths: ["x.go"] },
]));
h.click(chip());
const pop = h.$("conflict-pop");
assert.ok(pop.textContent.includes(evil), "the name was not shown as the text it is");
assert.ok(pop.textContent.includes("<b>file</b>.go"), "a path was not shown as text");
assert.ok(pop.textContent.includes(long), "the long name was cut off in the list");
assert.ok(!pop.querySelector("img") && !pop.querySelector("script") && !pop.querySelector("b") && !pop.querySelector("i"),
  "markup in a name became an element");
const label = chip().getAttribute("aria-label");
assert.ok(label.includes("2 panes") && label.length < 200, "the chip's label is not a short summary: " + label.length);
assert.ok(!label.includes(long) && !label.includes("<b>file</b>"), "the label carries more than the first pane's name");
h.recv(mk([{ id: "p4", name: long, paths: ["x.go"] }]));
assert.ok(chip().getAttribute("aria-label").length < 200, "a very long name made a long label: " + chip().getAttribute("aria-label").length);
`)
}

// A pane with many conflicting panes is shown five, and told of the rest.
func TestTheConflictListWithManyPanes(t *testing.T) {
	runFrontEnd(t, popSetup+`
const five = [1, 2, 3, 4, 5].map((i) => ({ id: "q" + i, name: "pane " + i, paths: ["f" + i + ".go"] }));
h.recv(mk(five, 20));
assert.ok(chip().textContent.includes("+24"), "the chip does not count all twenty five: " + chip().textContent);
h.click(chip());
const pop = h.$("conflict-pop");
for (let i = 1; i <= 5; i++) assert.ok(pop.textContent.includes("pane " + i), "pane " + i + " is not listed");
assert.ok(pop.textContent.includes("20 more panes are not listed"), pop.textContent);
assert.ok(chip().getAttribute("aria-label").includes("25 panes"), chip().getAttribute("aria-label"));
`)
}

// A pane closed on the tab that is showing takes its list with it: nothing else
// changes, so only the pane's removal can close it.
func TestTheConflictListGoesWhenItsPaneIsClosedOnTheSameTab(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const two = (conflicts) => fixture({ tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }], panes: { p1: pane("p1", { conflicts }), p2: pane("p2") } });
h.recv(two([{ id: "p2", name: "other", paths: ["a.go"] }]));
const wrap = h.terms[0].host.parentElement.parentElement;
h.click(wrap.querySelector(".pane-conflict"));
assert.ok(h.$("conflict-pop") && h.doc.body.contains(h.$("conflict-pop")), "setup: the list is not open");
h.recv(fixture({ tabs: [{ id: "t1", title: "one", focus: "p2", zoom: false, attention: false,
  root: { id: "n2", pane: "p2", weight: 1 } }], panes: { p2: pane("p2") } }));
assert.ok(!h.$("conflict-pop") || !h.doc.body.contains(h.$("conflict-pop")), "the list stayed after its pane was closed");
`)
}

// Zoom keeps the pane and its chip in the document but gives it another size, which
// moves the chip from where the list was put: the list follows that change by
// going away. It is not the tab, nor the pane being removed, that closes it here.
func TestTheConflictListGoesWhenZoomMovesItsChip(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const tab = (zoom) => ({ id: "t1", title: "one", focus: "p1", zoom, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) });
const mk = (zoom) => fixture({ tabs: [tab(zoom)], panes: { p1: pane("p1", { conflicts: [{ id: "p2", name: "other", paths: ["a.go"] }] }), p2: pane("p2") } });
h.recv(mk(false));
const wrap = h.terms[0].host.parentElement.parentElement;
const chip = wrap.querySelector(".pane-conflict");
h.click(chip);
assert.ok(h.doc.body.contains(h.$("conflict-pop")), "setup: the list is not open");
const watching = h.observers.filter((o) => o.targets.includes(chip) && !o.disconnected);
assert.strictEqual(watching.length, 1, "nothing watches the chip while the list is open");
watching[0].fn([{ target: chip }, { target: wrap }], watching[0]); // what a browser reports on starting to watch, both targets in one batch: not a change
assert.ok(h.doc.body.contains(h.$("conflict-pop")), "the first report, which is no change, closed the list");

h.recv(mk(true));
assert.ok(chip.isConnected, "setup: zoom took the chip out of the document, which is not the case this is about");
assert.ok(h.doc.body.contains(h.$("conflict-pop")), "setup: the list closed before the layout changed");
chip.getBoundingClientRect = () => ({ left: 0, top: 0, right: 160, bottom: 20, width: 160, height: 20, x: 0, y: 0 });
watching[0].fn([{ target: chip }], watching[0]); // the chip has another size now
assert.ok(!h.doc.body.contains(h.$("conflict-pop")), "the list stayed where it was put after zoom moved its chip");
assert.ok(watching[0].disconnected, "the watch outlived the list");
`)
}

// The list is placed once, so a window that changed size puts it away, and the
// keyboard goes back to the chip.
func TestTheConflictListGoesWhenTheWindowIsResized(t *testing.T) {
	runFrontEnd(t, popSetup+`
h.click(chip());
assert.ok(open(), "setup: the list is not open");
h.win.dispatchEvent(new h.Ev("resize", {}));
assert.ok(!open(), "the list stayed where it was after the window changed size");
assert.ok(h.doc.activeElement === chip(), "the keyboard did not go back to the chip");
`)
}

// When the chip goes while the keyboard is in the list, the keyboard goes to the
// focused pane's terminal, not nowhere.
func TestTheKeyboardGoesToTheTerminalWhenTheChipGoesFromUnderTheList(t *testing.T) {
	runFrontEnd(t, popSetup+`
h.click(chip());
assert.ok(h.doc.activeElement === h.$("conflict-pop"), "setup: the keyboard is not in the list");
h.terms.forEach((x) => { x.focused = false; });
h.recv(fixture({ panes: { p1: pane("p1"), p2: pane("p2") } }));
assert.ok(!open(), "the list stayed after the conflict cleared");
assert.ok(h.terms.some((x) => x.focused), "the keyboard went nowhere when the chip's conflict cleared");
`)
}

// Putting the list away because the layout moved gives the keyboard back to the chip
// only when it was in the list: a terminal that has it keeps it.
func TestPuttingTheConflictListAwayForLayoutNeverTakesTheKeyboardFromATerminal(t *testing.T) {
	runFrontEnd(t, popSetup+`
h.click(chip());
assert.ok(open(), "setup: the list is not open");
// The keyboard is in a terminal, not in the list.
const elsewhere = h.$("btn-settings");
elsewhere.focus();
h.win.dispatchEvent(new h.Ev("resize", {}));
assert.ok(!open(), "the list stayed after a resize");
assert.ok(h.doc.activeElement === elsewhere, "closing the list took the keyboard from where it was");

// And with no list open a resize does nothing at all.
elsewhere.focus();
h.win.dispatchEvent(new h.Ev("resize", {}));
assert.ok(h.doc.activeElement === elsewhere, "a resize with no list open moved the keyboard");

// A scroll under the list puts it away the same way; one inside the list does not.
h.click(chip());
h.dispatch(h.$("conflict-pop"), new h.Ev("scroll", { target: h.$("conflict-pop") }));
assert.ok(open(), "scrolling the list itself closed it");
// The page scrolled with the chip still where it was: the list stays.
h.dispatch(h.doc.body, new h.Ev("scroll", { target: h.doc.body }));
assert.ok(open(), "a scroll that did not move the chip closed the list");
chip().getBoundingClientRect = () => ({ left: 0, top: 40, right: 100, bottom: 60, width: 100, height: 20, x: 0, y: 40 });
h.dispatch(h.doc.body, new h.Ev("scroll", { target: h.doc.body }));
assert.ok(!open(), "a scroll that moved the chip left the list where it was");
`)
}

// A terminal streaming output scrolls all the time; that does not hold the chip and
// does not move it, so the list stays. A container that holds the chip closes it only
// when the chip moved.
func TestScrollingATerminalDoesNotCloseTheConflictList(t *testing.T) {
	runFrontEnd(t, popSetup+`
h.click(chip());
assert.ok(open(), "setup: the list is not open");
const viewport = h.terms[0].host;
assert.ok(!viewport.contains(chip()), "setup: the terminal holds the chip");
for (let i = 0; i < 5; i++) h.dispatch(viewport, new h.Ev("scroll", { target: viewport }));
assert.ok(open(), "a terminal scrolling closed the list");
// Even if the chip had moved, a scroll that does not hold it is not the cause.
chip().getBoundingClientRect = () => ({ left: 0, top: 90, right: 100, bottom: 110, width: 100, height: 20, x: 0, y: 90 });
h.dispatch(viewport, new h.Ev("scroll", { target: viewport }));
assert.ok(open(), "a scroll in an element that does not hold the chip closed the list");
const holder = chip().parentElement;
h.dispatch(holder, new h.Ev("scroll", { target: holder }));
assert.ok(!open(), "a scroll of the container that holds the chip, which moved it, left the list");
`)
}

// A scroll of the container that holds the chip, which left it within 2 px, is not a move.
func TestAScrollWithinTwoPixelsDoesNotCloseTheConflictList(t *testing.T) {
	runFrontEnd(t, popSetup+`
h.click(chip());
const holder = chip().parentElement;
chip().getBoundingClientRect = () => ({ left: 1, top: 1.5, right: 101, bottom: 21.5, width: 100, height: 20, x: 1, y: 1.5 });
h.dispatch(holder, new h.Ev("scroll", { target: holder }));
assert.ok(open(), "a 1.5 px shift closed the list");
`)
}

// The list refreshed in place follows the push: a chip whose label changed width with
// it is not the layout moving, and the observation that comes of it does not close
// the list. A later real change still does.
func TestRefreshingTheConflictListInPlaceDoesNotCloseIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const mk = (conflicts) => fixture({ panes: { p1: pane("p1", { conflicts }), p2: pane("p2") } });
h.recv(mk([{ id: "p2", name: "other", paths: ["a.go"] }]));
const wrap = h.terms[0].host.parentElement.parentElement;
const chip = wrap.querySelector(".pane-conflict");
h.click(chip);
assert.ok(h.doc.body.contains(h.$("conflict-pop")), "setup: the list is not open");
const w = h.observers.find((o) => o.targets.includes(chip) && !o.disconnected);
// The refresh adds a pane, and the chip's label is wider for it.
chip.getBoundingClientRect = () => ({ left: 0, top: 0, right: 150, bottom: 20, width: 150, height: 20, x: 0, y: 0 });
h.recv(mk([{ id: "p2", name: "other", paths: ["a.go"] }, { id: "p3", name: "third", paths: ["b.go"] }]));
assert.ok(h.doc.body.contains(h.$("conflict-pop")), "setup: the refresh closed the list itself");
w.fn([{ target: chip }], w); // what the browser reports of that width
assert.ok(h.doc.body.contains(h.$("conflict-pop")), "the observation of the refresh's own width change closed the list");
chip.getBoundingClientRect = () => ({ left: 0, top: 0, right: 200, bottom: 20, width: 200, height: 20, x: 0, y: 0 });
w.fn([{ target: chip }], w);
assert.ok(!h.doc.body.contains(h.$("conflict-pop")), "a real change after the refresh left the list open");
`)
}

// A chip with no size when the list opens does not hide the first real change.
func TestAChipWithNoSizeDoesNotSwallowTheFirstRealChange(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1", { conflicts: [{ id: "p2", name: "other", paths: ["a.go"] }] }), p2: pane("p2") } }));
const wrap = h.terms[0].host.parentElement.parentElement;
const chip = wrap.querySelector(".pane-conflict");
chip.getBoundingClientRect = () => ({ left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0, x: 0, y: 0 });
h.click(chip);
const w = h.observers.find((o) => o.targets.includes(chip) && !o.disconnected);
w.fn([{ target: chip }], w); // the first report: still no size, no change
assert.ok(h.doc.body.contains(h.$("conflict-pop")), "the first report closed the list");
chip.getBoundingClientRect = () => ({ left: 0, top: 0, right: 90, bottom: 20, width: 90, height: 20, x: 0, y: 0 });
w.fn([{ target: chip }], w);
assert.ok(!h.doc.body.contains(h.$("conflict-pop")), "the first real size was swallowed");
`)
}

// The page itself scrolling is a scroll whose target is the document, not an element
// that holds the chip: it carries the chip, and closes the list when the chip moved.
func TestScrollingTheDocumentClosesTheConflictListOnlyWhenTheChipMoved(t *testing.T) {
	runFrontEnd(t, popSetup+`
h.doc.nodeType = 9; // what a browser's document says it is
h.click(chip());
assert.ok(open(), "setup: the list is not open");
h.dispatch(h.doc, new h.Ev("scroll", { target: h.doc }));
assert.ok(open(), "the page scrolled and the chip did not move, and the list closed");
chip().getBoundingClientRect = () => ({ left: 0, top: 60, right: 100, bottom: 80, width: 100, height: 20, x: 0, y: 60 });
h.dispatch(h.doc, new h.Ev("scroll", { target: h.doc }));
assert.ok(!open(), "the page scrolled the chip away and the list stayed where it was");
`)
}

// A list refreshed in place is put under the chip where the chip is now, and then
// compared with the layout from there: a refresh that moved the chip neither closes the
// list nor leaves it where the chip was.
func TestRefreshingTheConflictListInPlaceMovesItToTheChip(t *testing.T) {
	runFrontEnd(t, popSetup+`
chip().getBoundingClientRect = () => ({ left: 50, top: 100, right: 150, bottom: 120, width: 100, height: 20, x: 50, y: 100 });
h.click(chip());
const pop = () => h.$("conflict-pop");
assert.strictEqual(pop().style.top, "124px", "setup: the list was not put under the chip");
assert.strictEqual(pop().style.left, "50px", "setup: the list was not put at the chip's left");
chip().getBoundingClientRect = () => ({ left: 70, top: 140, right: 190, bottom: 160, width: 120, height: 20, x: 70, y: 140 });
h.recv(mk([{ id: "p3", name: "billing", branch: "feature/billing", paths: ["a.go"] }, { id: "p4", name: "third", paths: ["b.go"] }]));
assert.ok(open(), "the refresh closed the list");
assert.strictEqual(pop().style.top, "164px", "the list stayed where the chip was and not where it is");
assert.strictEqual(pop().style.left, "70px", "the list stayed at the chip's old left");
const w = h.observers.find((o) => o.targets.includes(chip()) && !o.disconnected);
w.fn([{ target: chip() }], w);
assert.ok(open(), "the observation of the refresh's own move closed the list");
`)
}

// The pane the chip is in changing size, with the chip itself the same, closes the
// list: the comparison is of the pane's size too.
func TestThePaneChangingSizeClosesTheConflictList(t *testing.T) {
	runFrontEnd(t, popSetup+`
h.click(chip());
assert.ok(open(), "setup: the list is not open");
const paneEl = chip().closest(".pane");
assert.ok(paneEl, "setup: the chip is in no pane");
const w = h.observers.find((o) => o.targets.includes(paneEl) && !o.disconnected);
assert.ok(w, "nothing watches the pane");
w.fn([{ target: paneEl }], w);
assert.ok(open(), "a report with the pane the same size closed the list");
paneEl.getBoundingClientRect = () => ({ left: 0, top: 0, right: 500, bottom: 300, width: 500, height: 300, x: 0, y: 0 });
w.fn([{ target: paneEl }], w);
assert.ok(!open(), "the pane changed size and the list stayed");
`)
}
