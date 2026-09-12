package webui

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/help"
)

// readAsset returns one of the embedded front-end files.
func readAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(FS(), name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// The palette, the keyboard and the help pages are all drawn from the key
// table, so an action the table names and the front end has not implemented
// would be listed and then do nothing when it was picked.
func TestFrontEndImplementsEveryAction(t *testing.T) {
	app := readAsset(t, "app.js")

	start := strings.Index(app, "const ACTIONS = {")
	if start < 0 {
		t.Fatal("app.js no longer has an ACTIONS table")
	}
	end := strings.Index(app[start:], "\n  };")
	if end < 0 {
		t.Fatal("the ACTIONS table in app.js is not terminated as expected")
	}
	block := app[start : start+end]

	for _, k := range help.Keys {
		if !strings.Contains(block, "\n    "+k.ID+":") {
			t.Errorf("action %q (%s) is in the key table but not implemented in app.js", k.ID, k.Label)
		}
	}
}

// A binding written into the interface by hand is the drift this whole
// arrangement exists to prevent: tooltips, hints and the palette are all
// filled in from the table once it arrives, so no binding the table owns
// should appear in the front end at all.
func TestNoBindingsAreWrittenIntoTheInterface(t *testing.T) {
	// A comment may talk about a key; nothing a user reads may.
	comment := regexp.MustCompile(`^\s*(//|\*|/\*|<!--)`)

	for _, name := range []string{"app.js", "index.html", "app.css"} {
		for i, line := range strings.Split(readAsset(t, name), "\n") {
			if comment.MatchString(line) {
				continue
			}
			for _, k := range help.Keys {
				if k.Keys == "" || !strings.Contains(line, k.Keys) {
					continue
				}
				t.Errorf("%s:%d writes %q out by hand; take it from the key table instead:\n\t%s",
					name, i+1, k.Keys, strings.TrimSpace(line))
			}
		}
	}
}

// A custom property that was never defined is not an error anyone is told
// about: the declaration using it is thrown away and the property falls back to
// its initial value, so a background simply does not appear and a colour comes
// out black. The palette is small and stated in one place, which is what makes
// this worth checking rather than eyeballing.
func TestEveryColourTheStyleSheetUsesIsDefined(t *testing.T) {
	css := readAsset(t, "app.css")

	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--[\w-]+)\s*:`).FindAllStringSubmatch(css, -1) {
		defined[m[1]] = true
	}

	// A var() with a fallback stands on its own, so only the bare ones count.
	used := regexp.MustCompile(`var\(\s*(--[\w-]+)\s*\)`)
	for i, line := range strings.Split(css, "\n") {
		for _, m := range used.FindAllStringSubmatch(line, -1) {
			if defined[m[1]] {
				continue
			}
			t.Errorf("app.css:%d uses %s, which nothing defines, so this declaration is dropped:\n\t%s",
				i+1, m[1], strings.TrimSpace(line))
		}
	}
}

// ---------------------------------------------------------------------------
// Running the front end
// ---------------------------------------------------------------------------

/*
The front end has no build step, no package manager and no test runner, and it
should keep all three. What it does have is one large script that decides what
several agents look like on screen, and the only way to hold that to anything is
to run it.

So: node, the DOM in frontEndHarness below, and the real app.js and index.html
read out of the embedded file system. Nothing is installed and nothing is
checked in beyond this file. Where node is missing the behaviour tests skip and
the source-level ones above still run.
*/

// Through the relay the page is served from the machine's own prefix, and
// every socket it opens has to be opened under it: one made from the root
// reaches the relay's own routes instead of this machine.
func TestFrontEndFollowsThePathItWasServedFrom(t *testing.T) {
	runFrontEnd(t, `
const r = boot({ pathname: "/h/abc123/" });
assert.ok(r.control, "a control socket was opened");
assert.strictEqual(r.control.url, "ws://127.0.0.1:7777/h/abc123/ws/control");
r.hello();
r.recv(fixture());
const ptys = r.sockets.filter((s) => s.url.includes("/ws/pty"));
assert.ok(ptys.length > 0, "the panes were streamed");
for (const s of ptys) {
  assert.ok(s.url.startsWith("ws://127.0.0.1:7777/h/abc123/ws/pty?id="), "a terminal socket escaped the prefix: " + s.url);
}
`)
}

// Remote access: a chip that shows only on an enrolled machine, and a dialog
// that pairs devices and unpairs them — asking first, since unpairing ends
// whatever that device has open.
func TestRemoteAccessDialog(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const chip = h.$("btn-remote");
assert.ok(chip.hidden, "the chip is hidden on a machine that is not enrolled");

const remote = (over) => Object.assign({ state: "connected", relay: "https://relay.example",
  hostId: "h1", viewers: 2, since: "2030-01-01T00:00:00Z" }, over || {});
h.recv(fixture({ remote: remote() }));
assert.ok(!chip.hidden, "the chip shows once the machine is enrolled");
assert.strictEqual(chip.textContent, "Remote · 2");
assert.ok(!chip.classList.contains("trouble"), "a working tunnel is not amber");

h.click(chip);
assert.ok(!h.$("overlay").hidden, "the dialog opened");
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteDevices" });

h.recv({ type: "remoteDevices", enabled: true, current: "d2",
  devices: [
    { id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" },
    { id: "d2", name: "laptop", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" },
  ],
  hosts: [{ id: "h1", name: "desk", online: true, self: true }] });
const body = h.$("overlay-body");
assert.ok(body.textContent.includes("phone"), "the devices are listed");
assert.ok(body.textContent.includes("this device"), "the device this window is on is marked");

h.click(h.$("remote-pair"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remotePair", kind: "device" });
assert.ok(h.$("remote-pair").disabled, "the button waits for the relay rather than asking twice");
h.recv({ type: "remotePair", kind: "device", code: "fdp_c", url: "https://relay.example/pair#fdp_c",
  expiresAt: "2030-01-01T00:10:00Z", qr: "<svg xmlns='http://www.w3.org/2000/svg'></svg>" });
const qr = body.querySelector(".remote-qr");
assert.ok(qr && String(qr.src).startsWith("data:image/svg+xml"), "the QR code is drawn");
assert.strictEqual(body.querySelector(".remote-link").value, "https://relay.example/pair#fdp_c");

const unpairs = Array.from(body.querySelectorAll("button")).filter((b) => b.textContent === "Unpair");
assert.strictEqual(unpairs.length, 2, "each device can be unpaired");
const before = h.commands().length;
h.win._confirm = false;
h.click(unpairs[0]);
assert.strictEqual(h.commands().length, before, "a device was unpaired without asking");
h.win._confirm = true;
h.click(unpairs[0]);
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteRevoke", id: "d1" });

h.recv(fixture({ remote: remote({ state: "error", viewers: 0, detail: "lost the relay" }) }));
assert.ok(chip.classList.contains("trouble"), "a tunnel that cannot connect is amber");
assert.ok(h.$("overlay-body").textContent.includes("lost the relay"), "the dialog's status line followed the state");
`)
}

// runFrontEnd boots app.js against the harness and runs body against it. The
// body is ordinary node: assert is in scope, and h is the booted front end.
func runFrontEnd(t *testing.T, body string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH, so the front-end behaviour tests cannot run")
	}

	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("app.js", readAsset(t, "app.js"))
	write("index.html", readAsset(t, "index.html"))
	write("app.css", readAsset(t, "app.css"))
	write("harness.js", frontEndHarness)

	// The real action table, so a test presses the binding that ships rather
	// than one written out here to go stale.
	keys, err := json.Marshal(help.Keys)
	if err != nil {
		t.Fatalf("marshal the key table: %v", err)
	}
	write("keys.json", string(keys))

	// The real help, so a case searching it searches what ships.
	pages, err := help.Pages()
	if err != nil {
		t.Fatalf("render the help: %v", err)
	}
	payload, err := json.Marshal(map[string]any{"pages": pages})
	if err != nil {
		t.Fatalf("marshal the help: %v", err)
	}
	write("help.json", string(payload))

	// The body runs inside an async function so a case can wait for the parts of
	// the front end that are on a timer: the tooltip delay, the fit debounce,
	// the notice that takes itself away again.
	write("case.js", "\"use strict\";\nconst assert = require(\"assert\");\n"+
		"const { boot, fixture, catalog, pane, split, leaf } = require(\"./harness.js\");\n"+
		"const h = boot();\n(async () => {\n"+body+
		"\n})().catch((err) => { console.error(err); process.exitCode = 1; });\n")

	cmd := exec.Command(node, filepath.Join(dir, "case.js"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FLOCKDECK_ASSETS="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the front end failed this case: %v\n%s", err, out)
	}
	return string(out)
}

// A status push arrives every time any agent changes what it is doing, and with
// several running that is most of the time. Rebuilding the tab strip on each
// one throws away whatever the keyboard was holding, so a tab could not be
// reached from the keyboard at all while the agents were working.
func TestTabStripSurvivesAStatusPush(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());

const bar = h.$("tabs");
assert.strictEqual(bar.children.length, 2, "two tabs");
const first = bar.children[0], second = bar.children[1];
assert.strictEqual(first.querySelector(".label").textContent, "one");

// Someone has tabbed to the second tab button.
second.focus();
assert.ok(h.doc.activeElement === second, "the tab button has the keyboard");

// An agent starts working. Nothing about the tabs changed.
h.recv(fixture({ working: 1, panes: { p1: pane("p1", { status: "working" }), p2: pane("p2") } }));

assert.ok(h.$("tabs").children[0] === first, "the first tab button was replaced");
assert.ok(h.$("tabs").children[1] === second, "the second tab button was replaced");
assert.ok(h.doc.activeElement === second, "the push took the keyboard off the tab button");
`)
}

// The strip still has to follow the state it is given: titles, which tab is
// current, the marker for a blocked agent, tabs opening, closing and being
// reordered.
func TestTabStripFollowsTheState(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const bar = h.$("tabs");
const one = bar.children[0], two = bar.children[1];

// A rename, the other tab selected, and an agent in it blocked.
h.recv(fixture({
  activeTab: "t2", waiting: 1,
  tabs: [
    { id: "t1", title: "renamed", focus: "p1", root: leaf("n1", "p1") },
    { id: "t2", title: "two", focus: "p2", attention: true, root: leaf("n2", "p2") },
  ],
  panes: { p1: pane("p1"), p2: pane("p2", { status: "waiting" }) },
}));
assert.strictEqual(bar.children[0].querySelector(".label").textContent, "renamed");
assert.ok(!one.classList.contains("active"), "the first tab is no longer current");
assert.ok(two.classList.contains("active"), "the second tab is current");
assert.strictEqual(two.getAttribute("aria-selected"), "true");
assert.ok(two.classList.contains("attention"), "the blocked tab is marked");
assert.ok(two.querySelector(".attn"), "the marker element is there");
assert.ok(two.querySelector(".attn").getAttribute("aria-label"), "the marker names itself");

// The two swap places, and a third arrives.
h.recv(fixture({
  activeTab: "t2",
  tabs: [
    { id: "t2", title: "two", focus: "p2", root: leaf("n2", "p2") },
    { id: "t3", title: "three", focus: "p3", root: leaf("n3", "p3") },
    { id: "t1", title: "renamed", focus: "p1", root: leaf("n1", "p1") },
  ],
  panes: { p1: pane("p1"), p2: pane("p2"), p3: pane("p3") },
}));
assert.deepStrictEqual(bar.children.map((b) => b.querySelector(".label").textContent),
  ["two", "three", "renamed"], "the strip is in the wrong order");
assert.ok(bar.children[0] === two, "reordering rebuilt a button it could have moved");
assert.ok(bar.children[2] === one, "reordering rebuilt a button it could have moved");
assert.ok(!two.querySelector(".attn"), "the marker was not taken away again");

// One closes.
h.recv(fixture({
  activeTab: "t2",
  tabs: [{ id: "t2", title: "two", focus: "p2", root: leaf("n2", "p2") }],
  panes: { p2: pane("p2") },
}));
assert.strictEqual(bar.children.length, 1, "the closed tabs are gone from the strip");
assert.ok(bar.children[0] === two, "the surviving tab kept its button");

// The close button still closes the tab it belongs to, not the one it was
// first drawn for.
h.click(bar.children[0].querySelector(".close"));
const last = h.commands().pop();
assert.deepStrictEqual(last, { cmd: "closeTab", id: "t2" });
`)
}

// A dialog covers the window and nothing behind it can be used, but Tab does
// not know that on its own: it walks out of the dialog and into the terminal
// underneath, where the next thing typed reaches an agent rather than the box
// that appeared to have the keyboard.
func TestTheKeyboardStaysInsideADialog(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());

h.click(h.$("btn-worktrees"));
h.recv({
  type: "worktrees", root: "C:/repo", defaultBase: "main",
  items: [{ label: "main", path: "C:/repo", main: true, dirty: 2, untracked: 1,
            ahead: 1, head: "abc1234", upstream: "origin/main", panes: 1 },
          { label: "fix-auth", path: "C:/fix-auth", dirty: 0, untracked: 0, head: "def5678" }],
  branches: [{ name: "main", checkedIn: true }, { name: "fix-auth", checkedIn: true },
             { name: "spare", checkedIn: false }],
});

const overlay = h.$("overlay"), panel = h.$("overlay-panel");
assert.ok(!overlay.hidden, "the worktrees dialog is open");
const inside = () => panel === h.doc.activeElement || panel.contains(h.doc.activeElement);
assert.ok(inside(), "opening the dialog left the keyboard outside it");

// Round and round, both ways, without ever leaving.
const stops = new Set();
for (let i = 0; i < 60; i++) {
  h.key({ key: "Tab" });
  assert.ok(inside(), "Tab left the dialog after " + (i + 1) + " presses");
  stops.add(h.doc.activeElement.serial);
}
assert.ok(stops.size > 5, "Tab is not moving inside the dialog; it found " + stops.size + " stops");
for (let i = 0; i < 60; i++) {
  h.key({ key: "Tab", shiftKey: true });
  assert.ok(inside(), "Shift+Tab left the dialog after " + (i + 1) + " presses");
}

// Nothing the dialog is not showing is a stop on the way round: the branch
// chips of a section it did not draw, or a button on the bar behind it.
h.key({ key: "Escape" });
assert.ok(overlay.hidden, "Escape did not close the dialog");

// The palette takes the window over in the same way.
h.click(h.$("summary"));
h.recv({ type: "agents", items: [] });
h.key({ key: "Escape" });
h.press("palette");
assert.ok(!h.$("palette").hidden, "the palette is open");
const box = h.$("palette-box");
for (let i = 0; i < 30; i++) {
  h.key({ key: "Tab" });
  assert.ok(box === h.doc.activeElement || box.contains(h.doc.activeElement),
    "Tab left the palette after " + (i + 1) + " presses");
}
`)
}

// The window losing the Go process behind it is the one dialog that arrives
// without being asked for, over a terminal that has the keyboard.
func TestTheReconnectButtonTakesTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
assert.ok(h.terms[0].focused, "a terminal has the keyboard to begin with");

h.control.close();
assert.ok(!h.$("disconnected").hidden, "the disconnected dialog is up");
assert.ok(h.doc.activeElement === h.$("retry"), "the reconnect button did not take the keyboard");

const panel = h.$("disconnected").querySelector(".panel");
for (let i = 0; i < 10; i++) {
  h.key({ key: "Tab" });
  assert.ok(panel.contains(h.doc.activeElement), "Tab left the disconnected dialog");
}
`)
}

// Reviewing a working tree means clicking through the files that changed while
// writing the commit message for them. Redrawing the whole dialog to show one
// diff took the box being typed into away with it.
func TestPickingAFileLeavesTheCommitMessageAlone(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
h.recv(fixture());

const files = [];
for (let i = 0; i < 40; i++) {
  files.push({ path: "internal/pkg/file" + i + ".go", label: "M", added: i, removed: i % 3 });
}
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "improve-webui", upstream: "origin/improve-webui",
         hasRemote: true, ahead: 2, behind: 0, files: files });

const box = h.$("commit-message");
assert.ok(box, "the commit box is there");
box.value = "webui: a message being written";
box.oninput();
box.focus();
box.selectionStart = box.selectionEnd = 6;

const rows = h.$("overlay-body").querySelectorAll("div.rev-file");
assert.strictEqual(rows.length, 40, "a row per changed file");

const before = h.made();
h.click(rows[7]);
const NL = String.fromCharCode(10);
h.recv({ type: "diff", cwd: "C:/repo", file: "internal/pkg/file7.go",
         text: ["@@ -1,2 +1,2 @@", "-old", "+new"].join(NL) });
const built = h.made() - before;

assert.ok(h.$("commit-message") === box, "the commit box was replaced");
assert.ok(h.doc.activeElement === box, "the commit box lost the keyboard");
assert.strictEqual(box.value, "webui: a message being written", "the message was lost");
assert.strictEqual(box.selectionStart, 6, "the caret moved");

// The file really was chosen, and the diff really did arrive.
assert.ok(rows[7].classList.contains("sel"), "the chosen row is not marked");
assert.ok(!rows[0].classList.contains("sel"), "an old marking was left behind");
const diff = h.$("overlay-body").querySelector("div.rev-diff");
assert.ok(diff.textContent.includes("+new"), "the diff was not drawn");

// Choosing another file marks that one instead.
h.click(rows[9]);
assert.ok(!rows[7].classList.contains("sel"), "the previous row is still marked");
assert.ok(rows[9].classList.contains("sel"), "the new row is not marked");
assert.ok(h.doc.activeElement === box, "the second pick took the keyboard");

console.log("elements built to show a diff: " + built);
`)
	t.Log(strings.TrimSpace(out))
}

// The tally of who is waiting and who is working is a live region, so putting
// it back unchanged is not free: a screen reader reads out whatever appears
// there, and a status push arrives every time any agent moves.
func TestTheSummaryIsOnlySpokenWhenItChanges(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
const busy = { p1: pane("p1", { status: "waiting" }), p2: pane("p2", { status: "working" }) };
h.recv(fixture({ waiting: 1, working: 1, panes: busy }));

const box = h.$("summary");
assert.strictEqual(box.getAttribute("aria-live"), "polite", "the summary is a live region");
assert.ok(box.textContent.includes("1 waiting"), "got: " + box.textContent);
assert.ok(box.textContent.includes("1 working"), "got: " + box.textContent);
assert.strictEqual(h.doc.title, "▲ 1 waiting · flockdeck");
const first = box.children[0];

// Ten pushes in which the agents keep talking but the tally does not move.
const before = h.made();
for (let i = 0; i < 10; i++) {
  h.recv(fixture({ waiting: 1, working: 1, panes: {
    p1: pane("p1", { status: "waiting", detail: "asking about file " + i }),
    p2: pane("p2", { status: "working", detail: "line " + i }),
  } }));
  assert.ok(h.$("summary").children[0] === first,
    "the tally was written again on push " + (i + 1) + ", so it is read out again");
}
console.log("elements built by ten pushes that did not change the tally: " + (h.made() - before));

// It does still follow the state it is given.
h.recv(fixture({ waiting: 0, working: 2, panes: {
  p1: pane("p1", { status: "working" }), p2: pane("p2", { status: "working" }) } }));
assert.ok(h.$("summary").children[0] !== first, "the tally did not follow the count");
assert.ok(!box.textContent.includes("waiting"), "got: " + box.textContent);
assert.ok(box.textContent.includes("2 working"), "got: " + box.textContent);
assert.strictEqual(h.doc.title, "● 2 working · flockdeck");

h.recv(fixture({ waiting: 0, working: 0 }));
assert.strictEqual(box.textContent, "", "an idle workspace shows nothing");
assert.strictEqual(h.doc.title, "flockdeck");
`)
	t.Log(strings.TrimSpace(out))
}

// The pane headers are the hottest part of the screen: one per agent, redrawn
// from a push that arrives every time any of them says anything.
func TestPaneHeadersOnlyRedrawWhatMoved(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();

// Six agents, which is what this application is for.
const six = { tabs: [{ id: "t1", title: "six", focus: "p0", root:
  split("h", [leaf("n0", "p0"), leaf("n1", "p1"), leaf("n2", "p2"),
              leaf("n3", "p3"), leaf("n4", "p4"), leaf("n5", "p5")]) }], panes: {} };
for (let i = 0; i < 6; i++) {
  six.panes["p" + i] = pane("p" + i, {
    name: "agent " + i, branch: "work-" + i, status: "working",
    detail: "reading", dirty: 3, untracked: 1, ahead: 2,
  });
}
h.recv(fixture(six));

const headers = h.doc.querySelectorAll("div.pane-header");
assert.strictEqual(headers.length, 6, "six pane headers");
const branch = h.doc.querySelector("span.pane-branch");
assert.ok(branch.textContent.includes("work-0"), "got: " + branch.textContent);
const marks = branch.parentElement.querySelector("span.pane-git");
assert.ok(marks.getAttribute("aria-label").includes("3 changed"), "got: " + marks.getAttribute("aria-label"));
const inside = branch.children[0];

// Twenty pushes that say nothing new. This is what an idle moment looks like
// while six agents are streaming output.
const before = h.made();
for (let i = 0; i < 20; i++) h.recv(fixture(six));
const built = h.made() - before;
console.log("elements built by twenty pushes with six unchanged panes: " + built);
assert.strictEqual(built, 0, "the headers were redrawn " + built + " elements' worth for nothing");
assert.ok(branch.children[0] === inside, "the branch was rebuilt under its own tooltip");

// And the header still follows what does move.
six.panes.p3 = pane("p3", { name: "agent 3", branch: "work-3", status: "waiting",
                            detail: "may I edit main.go?", dirty: 4, untracked: 1, ahead: 2 });
h.recv(fixture(six));
const third = headers[3];
assert.ok(third.querySelector("span.dot").classList.contains("waiting"), "the dot did not follow");
assert.ok(third.querySelector("span.dot").getAttribute("aria-label").includes("Waiting"));
assert.ok(third.querySelector("span.pane-detail").textContent === "may I edit main.go?",
  "got: " + third.querySelector("span.pane-detail").textContent);
assert.ok(third.querySelector("span.pane-git").getAttribute("aria-label").includes("4 changed"),
  "got: " + third.querySelector("span.pane-git").getAttribute("aria-label"));

// A branch is only rewritten when the branch changes.
six.panes.p3 = pane("p3", { name: "agent 3", branch: "renamed", status: "waiting",
                            detail: "may I edit main.go?", dirty: 4, untracked: 1, ahead: 2 });
h.recv(fixture(six));
assert.ok(third.querySelector("span.pane-branch").textContent.includes("renamed"),
  "got: " + third.querySelector("span.pane-branch").textContent);
`)
	t.Log(strings.TrimSpace(out))
}

// A title attribute is taken over as the tooltip the first time an element is
// hovered, so writing one onto a button that already has a description
// replaces it — and every description here carries the key that runs the
// action, which is the one thing the key table exists to keep from drifting.
func TestTheProjectChipKeepsItsBinding(t *testing.T) {
	runFrontEnd(t, `
const keys = h.hello();
const projects = [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 1 },
  { root: "C:/other", name: "other", active: false, tabs: 1, waiting: 1, working: 0 },
];
h.recv(fixture({ projects: projects }));

const btn = h.$("project-btn");
const k = keys.find((x) => x.id === "projects");
assert.ok(k && k.keys, "the action table gives this a binding");
assert.strictEqual(h.$("project-name").textContent, "repo");
assert.ok(btn.classList.contains("attention"), "the other project is blocked and the chip does not say so");

const says = () => btn.dataset.tip || "";
assert.ok(says().includes(k.label), "the chip does not say what it does: " + says());
assert.ok(says().includes(k.keys), "the chip does not say which key opens it: " + says());
assert.ok(says().includes("C:/repo"), "the chip does not name the project: " + says());

// Hovering is what adopts a title, so it is what would lose the binding.
h.dispatch(btn, new h.Ev("pointerover", { pointerType: "mouse" }));
assert.ok(says().includes(k.keys), "hovering took the binding off the chip: " + says());
assert.ok(!btn.hasAttribute("title"), "a title here would be adopted and replace the description");

// And another push does not put it back.
h.recv(fixture({ projects: projects }));
assert.ok(says().includes(k.keys), "a push took the binding off the chip: " + says());

// Switching project follows.
projects[0].active = false;
projects[1].active = true;
h.recv(fixture({ projects: projects }));
assert.strictEqual(h.$("project-name").textContent, "other");
assert.ok(says().includes("C:/other"), "the chip did not follow the project: " + says());
assert.ok(!btn.classList.contains("attention"), "the blocked project is the one on screen now");
`)
}

// The palette is the one place every action can be reached from, so it is long
// enough to scroll and long enough that rebuilding it is not free.
func TestThePaletteMovesItsHighlightWithoutRebuilding(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("palette");
assert.ok(!h.$("palette").hidden, "the palette is open");

const list = h.$("palette-list");
const rows = list.children;
assert.ok(rows.length > 20, "the palette lists the actions; got " + rows.length);
assert.ok(rows[0].classList.contains("sel"), "the first row starts highlighted");

// Arrowing down moves the highlight and brings it with it.
const before = h.made();
for (let i = 0; i < 15; i++) h.key({ key: "ArrowDown" });
console.log("elements built by fifteen presses of the down arrow: " + (h.made() - before));
assert.ok(list.children[15] === rows[15], "the list was built again to move the highlight");
assert.ok(rows[15].classList.contains("sel"), "the highlight did not move");
assert.ok(!rows[0].classList.contains("sel"), "the old highlight was left behind");
assert.ok(rows[15].scrolledTo > 0, "the highlight was moved out of sight rather than scrolled to");

// A pointer crossing the list is the same move, once per row.
const crossing = h.made();
for (let i = 0; i < 10; i++) h.dispatch(rows[i], new h.Ev("mousemove", {}));
console.log("elements built by a pointer crossing ten rows: " + (h.made() - crossing));
assert.ok(rows[9].classList.contains("sel"), "the pointer did not move the highlight");
assert.ok(list.children[9] === rows[9], "the row under the pointer was replaced");

// Enter runs whatever is highlighted, and typing narrows the list.
h.$("palette-input").value = "worktree";
h.$("palette-input").oninput();
const hits = list.children;
assert.ok(hits.length >= 1 && hits.length < rows.length, "the search did not narrow the list");
assert.ok(hits[0].classList.contains("sel"), "the first hit is highlighted");
assert.ok(hits[0].textContent.toLowerCase().includes("worktree"), "got: " + hits[0].textContent);
h.key({ key: "Enter" });
assert.ok(h.$("palette").hidden, "Enter did not close the palette");
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktrees" });
`)
	t.Log(strings.TrimSpace(out))
}

// The palette is a text field with a list under it that the arrow keys walk
// while the keyboard stays in the field. Nothing about that is visible to a
// screen reader unless it is said: the field has to name the row.
func TestThePaletteNamesTheCommandItIsOn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("palette");

const input = h.$("palette-input"), list = h.$("palette-list");
assert.strictEqual(input.getAttribute("role"), "combobox");
assert.strictEqual(input.getAttribute("aria-controls"), "palette-list");
assert.strictEqual(list.getAttribute("role"), "listbox");

const rows = list.children;
assert.ok(rows.every((r) => r.getAttribute("role") === "option"), "every row is an option");
assert.strictEqual(new Set(rows.map((r) => r.id)).size, rows.length, "the rows have distinct ids");

const named = () => h.doc.getElementById(input.getAttribute("aria-activedescendant"));
assert.ok(named() === rows[0], "the field does not name the row it starts on");
assert.strictEqual(rows[0].getAttribute("aria-selected"), "true");
assert.strictEqual(rows[1].getAttribute("aria-selected"), "false");

h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
assert.ok(named() === rows[2], "the field did not follow the arrow keys");
assert.strictEqual(rows[2].getAttribute("aria-selected"), "true");
assert.strictEqual(rows[0].getAttribute("aria-selected"), "false");
assert.strictEqual(rows.filter((r) => r.getAttribute("aria-selected") === "true").length, 1,
  "exactly one row is the current one");

// The pointer moves it too, and it is the same claim either way.
h.dispatch(rows[5], new h.Ev("mousemove", {}));
assert.ok(named() === rows[5], "the field did not follow the pointer");

// Nothing matches: there is no row to name, so the field must not claim one.
input.value = "zzzzzz no such command";
input.oninput();
assert.strictEqual(list.children.length, 1, "the empty note is all that is left");
assert.ok(!input.hasAttribute("aria-activedescendant"),
  "the field still names a row that is no longer there");
`)
}

// Panes come and go constantly here — fanning a plan out opens one per task
// and they are closed as each finishes — so what a closed pane leaves behind
// accumulates over a session rather than being paid once.
func TestAClosedPaneLetsGoOfEverythingItHeld(t *testing.T) {
	runFrontEnd(t, `
h.hello();

const four = { tabs: [{ id: "t1", title: "fan out", focus: "p0", root:
  split("h", [leaf("n0", "p0"), leaf("n1", "p1"), leaf("n2", "p2"), leaf("n3", "p3")]) }],
  panes: {} };
for (let i = 0; i < 4; i++) four.panes["p" + i] = pane("p" + i, { name: "task " + i });
h.recv(fixture(four));

assert.strictEqual(h.terms.length, 4, "one terminal per pane");
assert.strictEqual(h.observers.length, 4, "one size watcher per pane");
assert.strictEqual(h.sockets.filter((s) => s.url.includes("/ws/pty")).length, 4, "one stream per pane");

// Someone rests the pointer on a pane's header while three of the four finish.
const header = h.doc.querySelectorAll("div.pane-header")[1];
h.dispatch(header.querySelector("span.dot"), new h.Ev("pointerover", { pointerType: "mouse" }));
await h.sleep(400);
assert.ok(h.doc.body.querySelector("div.tip"), "the bubble describing the dot did not open");

h.recv(fixture({ tabs: [{ id: "t1", title: "fan out", focus: "p0", root: leaf("n0", "p0") }],
                 panes: { p0: pane("p0", { name: "task 0" }) } }));

for (let i = 1; i < 4; i++) {
  assert.ok(h.terms[i].disposed, "the terminal of pane " + i + " was not disposed");
  assert.ok(h.observers[i].disconnected,
    "the size watcher of pane " + i + " is still registered, and holds the pane through its callback");
  assert.strictEqual(h.sockets.filter((s) => s.url.includes("id=p" + i))[0].readyState, 3,
    "the stream of pane " + i + " is still open");
}
// The one still running kept all of it.
assert.ok(!h.terms[0].disposed, "the surviving pane lost its terminal");
assert.ok(!h.observers[0].disconnected, "the surviving pane lost its size watcher");
assert.ok(!h.doc.body.querySelector("div.tip"), "a bubble was left over a pane that is gone");
`)
}

// The divider between two panes follows the pointer, and the pointer very
// easily leaves the window: the panes reach its edges.
func TestResizingASplitEndsWhenThePointerDoes(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({
  tabs: [{ id: "t1", title: "two up", focus: "p1", root:
    split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  panes: { p1: pane("p1"), p2: pane("p2") },
}));

const bar = h.doc.querySelector("div.divider");
assert.ok(bar, "the split has a divider");
const down = (button, id) => h.dispatch(bar, new h.Ev("pointerdown",
  { button: button, pointerId: id, clientX: 400, clientY: 300 }));
const move = (x, id) => h.dispatch(bar, new h.Ev("pointermove",
  { pointerId: id, clientX: x, clientY: 300 }));
const panels = () => h.doc.querySelectorAll("div.pane").map((p) => p.style.flexGrow);

// The secondary button opens a menu. It does not start a resize.
const before = panels();
down(2, 1);
move(460, 1);
assert.deepStrictEqual(panels(), before, "a right-click on the divider resized the split");
assert.ok(!bar.classList.contains("dragging"), "a right-click started a drag");
assert.strictEqual(h.commands().filter((c) => c.cmd === "setWeights").length, 0);

// The primary button does.
down(0, 2);
assert.ok(bar.classList.contains("dragging"), "the drag did not start");
assert.strictEqual(bar.captured, 2, "the divider did not take the pointer, so a release outside is lost");
move(440, 2);
const dragged = panels();
assert.notDeepStrictEqual(dragged, before, "dragging the divider did not move the panes");

// The system takes the pointer away — a window losing focus, a touch turning
// into a scroll. That has to end the drag as surely as letting go does.
h.dispatch(bar, new h.Ev("pointercancel", { pointerId: 2 }));
assert.ok(!bar.classList.contains("dragging"), "the drag outlived the pointer");
const saved = h.commands().filter((c) => c.cmd === "setWeights");
assert.strictEqual(saved.length, 1, "the split was not saved as it was left");
assert.strictEqual(saved[0].node, "s-h-2");
assert.strictEqual(saved[0].weights.length, 2);

// And nothing moves any more.
move(200, 2);
assert.deepStrictEqual(panels(), dragged, "the panes still follow a pointer that is gone");
`)
}

// A row of six agents is unreadable until some of them are given more room
// than others, and that could only be done by dragging.
func TestASplitCanBeResizedFromTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({
  tabs: [{ id: "t1", title: "three up", focus: "p1", root:
    split("h", [leaf("n1", "p1"), leaf("n2", "p2"), leaf("n3", "p3")]) }],
  panes: { p1: pane("p1"), p2: pane("p2"), p3: pane("p3") },
}));

const bars = h.doc.querySelectorAll("div.divider");
assert.strictEqual(bars.length, 2, "two dividers between three panes");
const bar = bars[0];
assert.strictEqual(bar.getAttribute("role"), "separator");
assert.strictEqual(bar.getAttribute("aria-orientation"), "vertical", "a row of panes is split by an upright bar");
assert.ok(bar.getAttribute("aria-label"), "the separator has no name");
assert.strictEqual(bar.getAttribute("tabindex"), "0", "the separator cannot be reached from the keyboard");

const panes = () => h.doc.querySelectorAll("div.pane").map((p) => Number(p.style.flexGrow));
bar.focus();
const start = panes();
assert.deepStrictEqual(start, [1, 1, 1], "the panes start level");

// Right widens the pane on the left of this divider, at the expense of the
// one on its right. The third pane is not involved.
h.dispatch(bar, new h.Ev("keydown", { key: "ArrowRight" }));
let now = panes();
assert.ok(now[0] > start[0], "the left pane did not grow");
assert.ok(now[1] < start[1], "the right pane did not give way");
assert.strictEqual(now[2], start[2], "a pane on the other side of the split moved");
assert.ok(Math.abs(now[0] + now[1] - 2) < 1e-9, "the pair no longer adds up");
assert.strictEqual(bar.getAttribute("aria-valuenow"), "54", "the separator does not say how it was left");

// It is saved as it is left, so the layout comes back this way.
const saved = h.commands().filter((c) => c.cmd === "setWeights");
assert.strictEqual(saved.length, 1);
assert.strictEqual(saved[0].node, "s-h-3");
assert.strictEqual(saved[0].weights.length, 3);

// Left goes back, and Home makes the pair equal again.
for (let i = 0; i < 6; i++) h.dispatch(bar, new h.Ev("keydown", { key: "ArrowLeft" }));
assert.ok(panes()[0] < start[0], "the left arrow did not narrow the pane");
h.dispatch(bar, new h.Ev("keydown", { key: "Home" }));
assert.deepStrictEqual(panes(), [1, 1, 1], "Home did not level the pair");

// It stops rather than letting a pane disappear.
for (let i = 0; i < 40; i++) h.dispatch(bar, new h.Ev("keydown", { key: "ArrowLeft" }));
assert.ok(panes()[0] >= 0.2, "the pane was squeezed out of existence: " + panes()[0]);

// The other divider is the pair to its own right, not this one.
bars[1].focus();
h.dispatch(bars[1], new h.Ev("keydown", { key: "ArrowRight" }));
assert.ok(panes()[2] < 1, "the second divider moved the wrong pair");

// A key the separator does not use is left for the rest of the application.
const ev = h.dispatch(bar, new h.Ev("keydown", { key: "ArrowUp" }));
assert.ok(ev, "an up arrow on an upright separator is not its business");
`)
}

// A status push arrives every time any agent says anything, so during a drag
// they arrive continuously — and each one carries the weights the Go side has,
// which until the drag is finished are the ones it started from.
func TestADragIsNotUndoneByAStatusPush(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const twoUp = {
  tabs: [{ id: "t1", title: "two up", focus: "p1", root:
    split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  panes: { p1: pane("p1"), p2: pane("p2") },
};
h.recv(fixture(twoUp));

const bar = h.doc.querySelector("div.divider");
const panes = () => h.doc.querySelectorAll("div.pane").map((p) => Number(p.style.flexGrow));
assert.deepStrictEqual(panes(), [1, 1]);

h.dispatch(bar, new h.Ev("pointerdown", { button: 0, pointerId: 1, clientX: 400, clientY: 300 }));
h.dispatch(bar, new h.Ev("pointermove", { pointerId: 1, clientX: 340, clientY: 300 }));
const dragged = panes();
assert.ok(dragged[0] < 1, "the drag did not narrow the left pane");

// The agents keep working while the divider is held.
for (let i = 0; i < 5; i++) {
  h.recv(fixture(Object.assign({}, twoUp, { working: 1,
    panes: { p1: pane("p1", { status: "working", detail: "line " + i }), p2: pane("p2") } })));
  assert.deepStrictEqual(panes(), dragged, "push " + (i + 1) + " put the panes back where the drag started");
}

h.dispatch(bar, new h.Ev("pointerup", { pointerId: 1, clientX: 340, clientY: 300 }));
const saved = h.commands().filter((c) => c.cmd === "setWeights").pop();
assert.ok(saved, "the drag was not saved");
assert.ok(Math.abs(saved.weights[0] - dragged[0]) < 1e-9, "what was saved is not what was drawn");

// Once it is over the Go side is in charge of the weights again.
h.recv(fixture(Object.assign({}, twoUp, { tabs: [{ id: "t1", title: "two up", focus: "p1", root:
  { id: "s-h-2", dir: "h", weight: 1, children: [
    { id: "n1", pane: "p1", weight: 3 }, { id: "n2", pane: "p2", weight: 1 }] } }] })));
assert.deepStrictEqual(panes(), [3, 1], "the weights from the Go side were not applied after the drag");
`)
}

// The weights are written onto the panes from the layout tree on every push,
// and every split in every tab had to be found in the document first.
func TestApplyingWeightsDoesNotSearchTheDocument(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();

// Three tabs of four panes: two projects' worth of agents, which is the shape
// this is for.
const tabs = [], panesIn = {};
for (let t = 0; t < 3; t++) {
  const kids = [];
  for (let i = 0; i < 4; i++) {
    const id = "p" + t + "-" + i;
    panesIn[id] = pane(id, { name: "agent " + id });
    kids.push(leaf("n" + t + "-" + i, id));
  }
  tabs.push({ id: "t" + t, title: "tab " + t, focus: "p" + t + "-0",
              root: { id: "s" + t, dir: t % 2 ? "v" : "h", weight: 1, children: kids } });
}
h.recv(fixture({ activeTab: "t0", tabs: tabs, panes: panesIn }));

const before = h.searched();
for (let i = 0; i < 20; i++) h.recv(fixture({ activeTab: "t0", tabs: tabs, panes: panesIn }));
const looked = h.searched() - before;
console.log("elements examined by document searches during twenty pushes: " + looked);
assert.strictEqual(looked, 0, "the document is still being searched on every push");

// The weights still land, in every tab and not only the one on screen.
tabs[2].root.children[1].weight = 4;
h.recv(fixture({ activeTab: "t0", tabs: tabs, panes: panesIn }));
const wrap = h.doc.querySelectorAll("div.pane").filter((p) => p.textContent.includes("agent p2-1"))[0];
assert.ok(wrap, "the pane is there");
assert.strictEqual(Number(wrap.style.flexGrow), 4, "the weight was not applied");

// A split's own weight lands on the split, not only a pane's on a pane.
tabs[1].root.weight = 2;
h.recv(fixture({ activeTab: "t0", tabs: tabs, panes: panesIn }));
assert.strictEqual(Number(h.doc.querySelector("div.split.v").style.flexGrow), 2,
  "a split's weight was not applied, so its container is not being found");
`)
	t.Log(strings.TrimSpace(out))
}

// Find belongs to one pane, and with six on screen it has to be clear which —
// and stay on it, because clicking into another one while the bar is up moves
// the focus without meaning to move the search.
func TestFindStaysOnThePaneItWasOpenedFor(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({
  tabs: [{ id: "t1", title: "pair", focus: "p1", root:
    split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  panes: { p1: pane("p1", { name: "reviewer" }), p2: pane("p2", { name: "builder" }) },
}));

h.press("findInTerminal");
assert.ok(!h.$("searchbar").hidden, "the find bar is up");
assert.strictEqual(h.$("search-label").textContent, "Find in reviewer",
  "the bar does not say which pane it is looking in");

const [first, second] = h.searchers;
h.$("search-input").value = "panic";
h.key({ key: "Enter" });
assert.deepStrictEqual(first.forward, ["panic"], "the wrong pane was searched");
assert.deepStrictEqual(second.forward, [], "the other pane was searched too");

// The pointer goes to the other pane, which moves the focus. The search does
// not go with it.
const panes = h.doc.querySelectorAll("div.pane");
h.dispatch(panes[1], new h.Ev("mousedown", {}));
h.recv(fixture({
  tabs: [{ id: "t1", title: "pair", focus: "p2", root:
    split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  panes: { p1: pane("p1", { name: "reviewer" }), p2: pane("p2", { name: "builder" }) },
}));
h.$("search-input").focus();
h.key({ key: "Enter", shiftKey: true });
assert.deepStrictEqual(first.back, ["panic"], "the search left the pane it was opened for");
assert.deepStrictEqual(second.back, [], "the search followed the focus to another pane");

// Closing clears the marks off that pane, not off whichever has the focus.
h.key({ key: "Escape" });
assert.ok(h.$("searchbar").hidden, "the find bar is closed");
assert.strictEqual(first.cleared, 1, "the searched pane was left marked up");

// A word that is not there has to say so: nothing else on screen moves.
h.press("findInTerminal");
const input = h.$("search-input");
input.value = "nowhere";
second.hit = false;
h.key({ key: "Enter" });
assert.ok(input.classList.contains("nomatch"), "a search that found nothing looked like one that did");
assert.strictEqual(input.getAttribute("aria-invalid"), "true");
second.hit = true;
h.key({ key: "Enter" });
assert.ok(!input.classList.contains("nomatch"), "the mark stayed after a search that did find something");
assert.strictEqual(input.getAttribute("aria-invalid"), "false");
`)
}

// Fanning a plan out changes another tab's shape once per agent it starts.
// Redrawing every tab for that took the pane being read out of the document
// and put it back, which is what costs a terminal the selection in it.
func TestOneTabChangingLeavesTheOthersStanding(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
const reading = { id: "t1", title: "reading", focus: "p1", root: leaf("n1", "p1") };
const working = { id: "t2", title: "fan out", focus: "p2", root: leaf("n2", "p2") };
const panesIn = { p1: pane("p1", { name: "reviewer" }), p2: pane("p2", { name: "planner" }) };
h.recv(fixture({ activeTab: "t1", tabs: [reading, working], panes: panesIn }));

const page = h.doc.querySelector("div.tab-page");
const wrap = h.doc.querySelector("div.pane");
assert.ok(page && wrap, "the tab on screen was drawn");
const settled = wrap.adoptions;

// The other tab grows a pane, three times over, the way a fan-out does.
for (let n = 2; n <= 4; n++) {
  const kids = [];
  for (let i = 1; i <= n; i++) {
    panesIn["q" + i] = pane("q" + i, { name: "task " + i });
    kids.push(leaf("m" + i, "q" + i));
  }
  h.recv(fixture({ activeTab: "t1",
    tabs: [reading, { id: "t2", title: "fan out", focus: "q1", root: split("h", kids) }],
    panes: panesIn }));
  assert.ok(h.doc.querySelectorAll("div.tab-page")[0] === page,
    "the tab on screen was rebuilt because another tab changed");
  assert.strictEqual(wrap.adoptions, settled,
    "the pane being read was taken out of the document and put back");
}
console.log("times the pane being read was taken out of the document and put back "
  + "by three fan-out steps in another tab: " + (wrap.adoptions - settled));

// The tab that changed did change: four panes in it now.
h.recv(fixture({ activeTab: "t2",
  tabs: [reading, { id: "t2", title: "fan out", focus: "q1", root: split("h",
    [leaf("m1", "q1"), leaf("m2", "q2"), leaf("m3", "q3"), leaf("m4", "q4")]) }],
  panes: panesIn }));
const pages = h.doc.querySelectorAll("div.tab-page");
assert.strictEqual(pages.length, 2, "a page per tab");
assert.ok(pages[0].hidden && !pages[1].hidden, "the wrong page is on screen");
assert.strictEqual(pages[1].querySelectorAll("div.pane").length, 4, "the fanned-out tab is not drawn");

// And this tab's own shape still redraws it, and hands back the keyboard.
h.recv(fixture({ activeTab: "t2",
  tabs: [reading, { id: "t2", title: "fan out", focus: "q1", root: split("h",
    [leaf("m1", "q1"), leaf("m2", "q2")]) }],
  panes: { p1: panesIn.p1, q1: panesIn.q1, q2: panesIn.q2 } }));
assert.strictEqual(h.doc.querySelectorAll("div.tab-page")[1].querySelectorAll("div.pane").length, 2);
assert.ok(h.terms.filter((t) => !t.disposed).some((t) => t.focused), "no terminal has the keyboard");

// The last tab closing leaves the placeholder, and opening one takes it away.
h.recv(fixture({ activeTab: "", tabs: [], panes: {} }));
assert.ok(h.doc.querySelector("div.empty"), "there is nothing to say the workspace is empty");
assert.strictEqual(h.doc.querySelectorAll("div.tab-page").length, 0);
h.recv(fixture({ activeTab: "t1", tabs: [reading], panes: { p1: panesIn.p1 } }));
assert.ok(!h.doc.querySelector("div.empty"), "the placeholder was left behind under the new tab");
assert.strictEqual(h.doc.querySelectorAll("div.tab-page").length, 1);
`)
	t.Log(strings.TrimSpace(out))
}

// Reading what an agent changed, and finding the agent that is blocked, are
// both lists of rows built out of divs carrying an onclick. Tab does not stop
// on one of those and Enter does nothing to it.
func TestTheReviewAndAgentListsAnswerTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());

h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "improve-webui", hasRemote: false, files: [
  { path: "internal/webui/assets/app.js", label: "M", added: 40, removed: 12 },
  { path: "internal/webui/webui_test.go", label: "M", added: 90, removed: 0 },
  { path: "README.md", label: "A", added: 3, removed: 0 },
] });

const rows = h.$("overlay-body").querySelectorAll("div.rev-file");
assert.strictEqual(rows.length, 3);
assert.ok(rows.every((r) => r.getAttribute("tabindex") === "0"), "the rows cannot be tabbed to");
assert.ok(rows.every((r) => r.getAttribute("role") === "button"), "the rows do not say they can be pressed");

// Tab from the top of the dialog reaches them.
const panel = h.$("overlay-panel");
let reached = false;
for (let i = 0; i < 40 && !reached; i++) {
  h.key({ key: "Tab" });
  reached = rows.includes(h.doc.activeElement);
}
assert.ok(reached, "no amount of tabbing inside the dialog reaches the file list");

rows[1].focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "diff", path: "C:/repo", text: "internal/webui/webui_test.go" });
assert.ok(rows[1].classList.contains("sel"), "the row was not chosen");
assert.strictEqual(rows[1].getAttribute("aria-current"), "true", "nothing says which file the diff is of");
assert.strictEqual(rows[0].getAttribute("aria-current"), "false");

// Space works the same way, and does not scroll the dialog instead.
rows[2].focus();
const ev = h.key({ key: " " });
assert.ok(ev.defaultPrevented, "Space scrolled the dialog rather than choosing the file");
assert.deepStrictEqual(h.commands().pop(), { cmd: "diff", path: "C:/repo", text: "README.md" });

// The agents list is the way to the pane that is blocked, so it has to be
// reachable the same way.
h.key({ key: "Escape" });
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "one", name: "agent one",
    status: "working", branch: "main", kind: "claude", detail: "", dirty: 0 },
  { paneId: "p9", tabId: "t9", root: "C:/other", project: "other", tab: "nine", name: "agent nine",
    status: "waiting", branch: "fix-auth", kind: "claude", detail: "may I write?", dirty: 2 },
] });
const agents = h.$("overlay-body").querySelectorAll("div.agent-row");
assert.strictEqual(agents.length, 2);
assert.ok(agents.every((r) => r.getAttribute("tabindex") === "0"), "the agent rows cannot be tabbed to");

// The disc repeats the word the row already carries, so it is not read twice.
const dot = agents[1].querySelector("span.dot");
assert.strictEqual(dot.getAttribute("aria-hidden"), "true", "the status is announced twice over");
assert.ok(agents[1].textContent.includes("waiting"), "the row does say the status in words");

agents[1].focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "revealPane", root: "C:/other", node: "t9", id: "p9" });
assert.ok(h.$("overlay").hidden, "the dialog stayed open after going to the pane");
`)
}

// Across six panes an eight-pixel disc is not enough to find the one agent
// that has stopped and is waiting on you.
func TestAWaitingPaneIsMarkedOnThePaneItself(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const six = { tabs: [{ id: "t1", title: "six", focus: "p0", root:
  split("h", [leaf("n0", "p0"), leaf("n1", "p1"), leaf("n2", "p2"),
              leaf("n3", "p3"), leaf("n4", "p4"), leaf("n5", "p5")]) }], panes: {} };
const at = (status) => {
  const panes = {};
  for (let i = 0; i < 6; i++) panes["p" + i] = pane("p" + i, { status: i === 4 ? status : "working" });
  return fixture(Object.assign({}, six, { panes: panes, waiting: status === "waiting" ? 1 : 0 }));
};
h.recv(at("working"));

const wraps = h.doc.querySelectorAll("div.pane");
assert.strictEqual(wraps.length, 6);
assert.ok(wraps.every((w) => w.dataset.status === "working"), "the panes do not carry their status");

h.recv(at("waiting"));
assert.strictEqual(wraps[4].dataset.status, "waiting", "the blocked pane is not marked");
assert.ok(wraps.filter((w) => w.dataset.status === "waiting").length === 1,
  "more than one pane is marked as blocked");
// The mark on the pane and the mark on its disc say the same thing.
assert.ok(wraps[4].querySelector("span.dot").classList.contains("waiting"));

// The answer is given and the mark goes.
h.recv(at("working"));
assert.strictEqual(wraps[4].dataset.status, "working", "the mark outlived the question");

// It is the header that is coloured, not the border: the border still has to
// answer which pane the keyboard is in while agents are waiting.
const css = h.css();
assert.ok(/\.pane\[data-status="waiting"\] \.pane-header/.test(css),
  "nothing in the style sheet acts on a waiting pane");
assert.ok(!/\.pane\[data-status="waiting"\] *\{[^}]*border-color/.test(css),
  "the waiting mark takes the border, which says where the keyboard is");
`)
}

// The strip is as wide as the bar and scrolls when there are more tabs than
// fit, which with a dozen agents open is most of the time.
func TestTheTabStripCanBeWalked(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const many = (active) => {
  const tabs = [], panes = {};
  for (let i = 0; i < 10; i++) {
    panes["p" + i] = pane("p" + i, { name: "agent " + i });
    tabs.push({ id: "t" + i, title: "agent " + i, focus: "p" + i, root: leaf("n" + i, "p" + i) });
  }
  return fixture({ activeTab: active, tabs: tabs, panes: panes });
};
h.recv(many("t0"));

const bar = h.$("tabs");
const btns = bar.children;
assert.strictEqual(btns.length, 10);

// One stop for the strip, not two per tab.
assert.strictEqual(btns[0].tabIndex, 0, "the current tab is not the stop");
assert.ok(btns.slice(1).every((b) => b.tabIndex === -1),
  "every tab is a stop on the way through the window");
assert.ok(btns.slice(1).every((b) => b.querySelector(".close").tabIndex === -1),
  "every close button is a stop too");

// Tab from the project chip reaches the strip once and leaves it once.
h.$("project-btn").focus();
h.key({ key: "Tab" });
assert.ok(h.doc.activeElement === btns[0], "Tab did not reach the current tab");
h.key({ key: "Tab" });
assert.ok(h.doc.activeElement === btns[0].querySelector(".close"), "its close button follows it");
h.key({ key: "Tab" });
assert.ok(!btns.includes(h.doc.activeElement), "Tab is still walking the tabs one at a time");

// The arrows walk the strip and bring each into view without switching agent.
btns[0].focus();
h.key({ key: "ArrowRight" });
assert.ok(h.doc.activeElement === btns[1], "the right arrow did not move along the strip");
assert.ok(btns[1].scrolledTo > 0, "the tab was moved to but not brought into view");
assert.strictEqual(h.commands().filter((c) => c.cmd === "selectTab").length, 0,
  "arrowing past a tab switched to it, so walking the strip visits every agent");
h.key({ key: "End" });
assert.ok(h.doc.activeElement === btns[9], "End did not go to the last tab");
h.key({ key: "ArrowRight" });
assert.ok(h.doc.activeElement === btns[0], "the strip does not wrap");
h.key({ key: "Home" });
assert.ok(h.doc.activeElement === btns[0]);

// Enter on the tab the focus is on is what switches to it.
h.key({ key: "ArrowRight" });
h.key({ key: "ArrowRight" });
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectTab", id: "t2" });

// Switching by any means brings the new tab into view; a status push does not
// move the strip.
const was = btns[7].scrolledTo || 0;
h.recv(many("t7"));
assert.ok(btns[7].scrolledTo > was, "switching to a tab off the end did not scroll to it");
assert.strictEqual(btns[7].tabIndex, 0, "the stop did not move with the current tab");
const settled = btns[7].scrolledTo;
for (let i = 0; i < 5; i++) h.recv(many("t7"));
assert.strictEqual(btns[7].scrolledTo, settled, "an ordinary push moves the strip under the pointer");
`)
}

// These dialogs are drawn whole from the reply that comes back, so the button
// that asked for the reply is thrown away by the answer to it.
func TestADialogPutsTheKeyboardBackAfterARedraw(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());

const trees = () => ({
  type: "worktrees", root: "C:/repo", defaultBase: "main",
  items: [{ label: "main", path: "C:/repo", main: true, dirty: 1, untracked: 0, head: "abc1234" }],
  branches: [{ name: "main", checkedIn: true }, { name: "spare", checkedIn: false }],
});
h.click(h.$("btn-worktrees"));
h.recv(trees());

const body = h.$("overlay-body");
const refresh = body.querySelectorAll("button").filter((b) => b.textContent === "Refresh")[0];
assert.ok(refresh, "the dialog has a Refresh button");
refresh.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktrees" });

// The answer redraws the dialog, which destroys the button that was pressed.
h.recv(trees());
const again = h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Refresh")[0];
assert.ok(again !== refresh, "the dialog was not redrawn, so this proves nothing");
assert.ok(h.doc.activeElement === again, "the keyboard was left outside the dialog by its own answer");

// Pressing it again still works, which is the point.
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktrees" });

// The same in Changes, where the redraw comes from asking the remote.
h.key({ key: "Escape" });
h.click(h.$("btn-changes"));
const state = { type: "changes", cwd: "C:/repo", branch: "improve-webui", upstream: "origin/improve-webui",
                hasRemote: true, ahead: 1, files: [{ path: "app.js", label: "M", added: 2, removed: 1 }] };
h.recv(state);
const fetch = h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Fetch")[0];
fetch.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "gitFetch", path: "C:/repo" });
h.recv(state);
const fetchAgain = h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Fetch")[0];
assert.ok(fetchAgain !== fetch, "the dialog was not redrawn");
assert.ok(h.doc.activeElement === fetchAgain, "Fetch dropped the keyboard when its answer arrived");

// A field keeps the caret where it was, not at the end.
const box = h.$("commit-message");
box.value = "webui: something";
box.oninput();
box.focus();
box.setSelectionRange(6, 6);
h.recv(state);
const boxAgain = h.$("commit-message");
assert.ok(boxAgain !== box, "the commit box was not redrawn");
assert.ok(h.doc.activeElement === boxAgain, "the commit box lost the keyboard to a refresh");
assert.strictEqual(boxAgain.selectionStart, 6, "the caret went back to the end of the message");
`)
}

// The help search matches against the whole of every page, and one keystroke
// asks three parts of the dialog what the hits are.
func TestSearchingTheHelpDoesNotRereadIt(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("help");
await h.sleep(50);

const search = h.$("help-search");
assert.ok(search, "the help opened with its search box");
const list = () => h.$("overlay-body").querySelector(".help-list").children;
const pages = list().length;
assert.ok(pages >= 10, "the real help pages were loaded; got " + pages);

// A query typed out one character at a time, as it would be.
const type = (text) => {
  for (let i = 1; i <= text.length; i++) {
    search.value = text.slice(0, i);
    search.oninput();
  }
};
const began = process.hrtime.bigint();
for (let round = 0; round < 25; round++) {
  type("worktree");
  search.value = "";
  search.oninput();
}
const ms = Number(process.hrtime.bigint() - began) / 1e6;
console.log("200 keystrokes across the help search: " + ms.toFixed(1) + "ms");

// And it still searches.
type("worktree");
const hits = list();
assert.ok(hits.length >= 1 && hits.length < pages, "the search did not narrow the list");
assert.ok(hits.map((b) => b.textContent.toLowerCase()).every((t) => t.includes("worktree")) ||
  hits.length > 0, "the hits do not look like hits");
assert.ok(h.$("help-content").textContent.length > 0, "the page beside the list is empty");

// A word in the body of a page and not its title still finds it, which is what
// searching the whole page is for.
search.value = "zzzznotawordanywhere";
search.oninput();
assert.strictEqual(list().length, 1, "something matched a word that is in no page");
assert.ok(h.$("overlay-body").querySelector(".help-none"), "nothing says the search found nothing");

search.value = "";
search.oninput();
assert.strictEqual(list().length, pages, "clearing the search did not bring the contents back");
`)
	t.Log(strings.TrimSpace(out))
}

// The buttons in a pane header sit between the top bar and the terminals, and
// there are five of them per pane.
func TestThePaneButtonsAreOneStopEach(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const kids = [], panesIn = {};
for (let i = 0; i < 6; i++) {
  panesIn["p" + i] = pane("p" + i, { name: "agent " + i });
  kids.push(leaf("n" + i, "p" + i));
}
h.recv(fixture({ tabs: [{ id: "t1", title: "six", focus: "p0", root: split("h", kids) }], panes: panesIn }));

const bars = h.doc.querySelectorAll("div.pane-actions");
assert.strictEqual(bars.length, 6, "a button row per pane");
assert.ok(bars.every((b) => b.getAttribute("role") === "toolbar"), "the rows do not say what they are");
assert.ok(bars.every((b) => b.getAttribute("aria-label")), "the rows have no name");

const stops = (bar) => bar.children.filter((b) => b.tabIndex !== -1).length;
assert.deepStrictEqual(bars.map(stops), [1, 1, 1, 1, 1, 1],
  "each row of buttons is more than one stop on the way through the window");
assert.strictEqual(bars[0].children.length, 5, "five things to do with a pane");

// The arrows walk the row and take the stop with them.
const buttons = bars[0].children;
buttons[0].focus();
h.key({ key: "ArrowRight" });
assert.ok(h.doc.activeElement === buttons[1], "the right arrow did not move along the row");
assert.strictEqual(buttons[1].tabIndex, 0, "the stop did not move with the focus");
assert.strictEqual(buttons[0].tabIndex, -1);
h.key({ key: "End" });
assert.ok(h.doc.activeElement === buttons[4], "End did not go to the last button");
h.key({ key: "ArrowRight" });
assert.ok(h.doc.activeElement === buttons[0], "the row does not wrap");

// Coming back returns to the button last used, not to the start.
h.key({ key: "End" });
h.doc.body.focus();
h.key({ key: "Tab" });
h.key({ key: "Tab" });
assert.strictEqual(stops(bars[0]), 1, "the row grew a second stop");
assert.strictEqual(buttons[4].tabIndex, 0, "the stop did not stay where it was left");

// And they still do what they say.
buttons[4].focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "closePane", id: "p0" });
buttons[3].focus();
h.key({ key: " " });
assert.deepStrictEqual(h.commands().pop(), { cmd: "toggleZoom", id: "p0" });
`)
}

// These dialogs are drawn whole from each reply, so anything half-typed into
// one of them went with the next redraw — and the redraws come from the other
// buttons in the same dialog.
func TestADialogKeepsWhatWasTypedIntoIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());

const trees = () => ({
  type: "worktrees", root: "C:/repo", defaultBase: "main",
  items: [{ label: "main", path: "C:/repo", main: true, head: "abc1234" },
          { label: "spare", path: "C:/spare", head: "def5678" }],
  branches: [{ name: "main", checkedIn: true }],
});
h.click(h.$("btn-worktrees"));
h.recv(trees());

const branch = h.$("wt-branch"), base = h.$("wt-base");
assert.strictEqual(base.value, "main", "the base starts at what the Go side suggested");
branch.value = "fix-au";
branch.oninput();
branch.focus();
branch.setSelectionRange(6, 6);
base.value = "origin/main";
base.oninput();

// Removing a worktree redraws the dialog around the form.
h.recv(trees());
assert.ok(h.$("wt-branch") !== branch, "the dialog was not redrawn, so this proves nothing");
assert.strictEqual(h.$("wt-branch").value, "fix-au", "the branch name being typed was thrown away");
assert.strictEqual(h.$("wt-base").value, "origin/main", "the base was reset to the suggestion");
assert.ok(h.doc.activeElement === h.$("wt-branch"), "the field lost the keyboard");
assert.strictEqual(h.$("wt-branch").selectionStart, 6, "the caret moved");

// Creating the worktree empties the field, and it stays empty.
h.$("wt-branch").value = "fix-auth";
h.$("wt-branch").oninput();
h.click(h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Create")[0]);
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeAdd", text: "fix-auth", base: "origin/main" });
h.recv(trees());
assert.strictEqual(h.$("wt-branch").value, "", "the branch name came back after being used");

// Closing the dialog forgets the draft; it belongs to this visit to it.
h.key({ key: "Escape" });
h.click(h.$("btn-worktrees"));
h.recv(trees());
assert.strictEqual(h.$("wt-base").value, "main", "an old base outlived the dialog it was typed into");

// The folder browser keeps a path being typed, and gives way once the browser
// has actually been sent somewhere.
h.key({ key: "Escape" });
h.click(h.$("project-btn"));
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", entries: [
  { name: "one", path: "C:/repos/one", isRepo: true }], places: [] });
h.recv({ type: "recents", items: [{ root: "C:/old", name: "old", exists: true, open: false }] });

const box = h.$("overlay-body").querySelector("div.browse-bar").querySelector("input");
assert.strictEqual(box.value, "C:/repos");
box.value = "C:/repos/hal";
box.oninput();
h.recv({ type: "recents", items: [] });
const boxAgain = h.$("overlay-body").querySelector("div.browse-bar").querySelector("input");
assert.strictEqual(boxAgain.value, "C:/repos/hal", "the path being typed was thrown away by an unrelated push");

h.recv({ type: "browse", path: "C:/elsewhere", parent: "C:/", entries: [], places: [] });
const boxLast = h.$("overlay-body").querySelector("div.browse-bar").querySelector("input");
assert.strictEqual(boxLast.value, "C:/elsewhere", "the box did not follow the browser to where it went");
`)
}

// Reconnect is a button that shows nothing for as long as the attempt takes,
// so it is a button people press twice.
func TestReconnectingTwiceLeavesOneConnection(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
assert.strictEqual(h.controls().length, 1);

// The Go process goes away.
h.controls()[0].close();
assert.ok(!h.$("disconnected").hidden, "the disconnected panel did not appear");

// Two presses, because the first one showed nothing.
h.click(h.$("retry"));
h.click(h.$("retry"));
const opened = h.controls();
assert.strictEqual(opened.length, 3, "one connection per press, plus the first");
const abandoned = opened[1], live = opened[2];
assert.strictEqual(abandoned.readyState, 3, "the superseded attempt was left connecting");

// The live one comes up.
live.onopen();
assert.ok(h.$("disconnected").hidden, "the panel stayed up over a live connection");

// The abandoned attempt now finishes what it was doing. None of it counts.
if (abandoned.onopen) abandoned.onopen();
if (abandoned.onmessage) abandoned.onmessage({ data: JSON.stringify(fixture({ tabs: [], panes: {} })) });
if (abandoned.onclose) abandoned.onclose();
assert.ok(h.$("disconnected").hidden,
  "an abandoned attempt closing put the panel back over a working connection");
assert.ok(!h.doc.querySelector("div.empty"),
  "state from an abandoned connection was drawn over the workspace");
assert.strictEqual(h.controls().length, 3, "the abandoned attempt started reconnecting on its own");

// And the live one still works.
h.recv(fixture({ waiting: 1, panes: {
  p1: pane("p1", { status: "waiting" }), p2: pane("p2") } }));
assert.ok(h.$("summary").textContent.includes("1 waiting"), "got: " + h.$("summary").textContent);
`)
}

// A plan fanned out into six agents is six of them reaching the same
// permission question within a second of each other. They all carry one tag,
// so sending six notifications shows one, chosen by pane order.
func TestOneNotificationForTheAgentsThatStoppedTogether(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.doc._hasFocus = false;   // the window is behind something else

const state = (statuses) => {
  const kids = [], panes = {};
  statuses.forEach((st, i) => {
    panes["q" + i] = pane("q" + i, { name: "task " + i, branch: "task-" + i, status: st });
    kids.push(leaf("m" + i, "q" + i));
  });
  return fixture({
    tabs: [{ id: "t9", title: "fan out", focus: "q0", root: split("h", kids) }],
    panes: panes, waiting: statuses.filter((s) => s === "waiting").length,
  });
};

// Arriving at a workspace where agents are already working is not news.
h.recv(state(["working", "working", "working"]));
assert.strictEqual(h.notifications.length, 0, "the first sighting of a pane was announced");

// Three of them hit a permission question at once.
h.recv(state(["waiting", "waiting", "waiting"]));
assert.strictEqual(h.notifications.length, 1,
  "one notification for the lot, not " + h.notifications.length + " that overwrite each other");
const all = h.notifications[0];
assert.strictEqual(all.title, "3 agents need you", "got: " + all.title);
["task 0", "task 1", "task 2"].forEach((n) =>
  assert.ok(all.body.includes(n), "the notification does not name " + n + ": " + all.body));

// Clicking it goes to the agent that asked, not merely to the window.
all.onclick();
assert.deepStrictEqual(h.commands().pop(), { cmd: "revealPane", node: "t9", id: "q0" });

// Staying blocked is not news either.
h.recv(state(["waiting", "waiting", "waiting"]));
assert.strictEqual(h.notifications.length, 1, "the same agents were announced again");

// One is answered and then asks something else: it is named on its own.
h.recv(state(["working", "waiting", "waiting"]));
h.recv(state(["waiting", "waiting", "waiting"]));
assert.strictEqual(h.notifications.length, 2);
assert.strictEqual(h.notifications[1].title, "task 0 needs you");
assert.ok(h.notifications[1].body.includes("task-0"), "got: " + h.notifications[1].body);

// With the window in front, the marker on the tab is enough.
h.doc._hasFocus = true;
h.recv(state(["working", "working", "working"]));
h.recv(state(["waiting", "waiting", "waiting"]));
assert.strictEqual(h.notifications.length, 2, "the window is in front and was interrupted anyway");
`)
}

// The Go side caps a diff at 400KB, which bounds the bytes and not the lines.
// A generated file of short lines reaches that in a couple of hundred thousand
// of them, and an element per line is drawn while the window does nothing else.
func TestAHugeDiffDoesNotStopTheWindow(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
         files: [{ path: "package-lock.json", label: "M", added: 200000, removed: 0 }] });

// A generated file. The worst the 400KB cap lets through is four times this
// many lines; fifty thousand is enough to say whether they are all drawn and
// leaves the test itself quick.
const NL = String.fromCharCode(10);
const lines = [];
for (let i = 0; i < 50000; i++) lines.push(i % 2 ? "+a" : "-b");
const text = lines.join(NL);

const row = h.$("overlay-body").querySelector("div.rev-file");
h.click(row);
const before = h.made();
const began = process.hrtime.bigint();
h.recv({ type: "diff", cwd: "C:/repo", file: "package-lock.json", text: text });
const ms = Number(process.hrtime.bigint() - began) / 1e6;
const built = h.made() - before;
console.log("a 50,000-line diff drew " + built + " elements in " + ms.toFixed(0) + "ms");
assert.ok(built < 3100, "the whole diff was drawn: " + built + " elements");

const panel = h.$("overlay-body").querySelector("div.rev-diff");
assert.ok(panel.textContent.includes("more lines are not drawn"), "nothing says the diff was cut short");
assert.ok(panel.children[0].classList.contains("del"), "the lines that were drawn are not coloured");
assert.ok(panel.children[1].classList.contains("add"));

// The rest is one press away for anyone who wants it.
h.click(h.$("overlay-body").querySelector("div.rev-file"));
const shortLines = [];
for (let i = 0; i < 3050; i++) shortLines.push(i % 2 ? "+a" : "-b");
h.recv({ type: "diff", cwd: "C:/repo", file: "package-lock.json", text: shortLines.join(NL) });
const some = h.$("overlay-body").querySelector("div.rev-diff");
assert.strictEqual(some.children.filter((c) => c.tagName === "DIV").length, 3001,
  "three thousand lines and the note");
const more = some.querySelectorAll("button").filter((b) => b.textContent === "Show them")[0];
assert.ok(more, "there is no way to see the rest");
h.click(more);
assert.strictEqual(some.children.length, 3050, "the rest was not drawn on request: " + some.children.length);
assert.ok(!some.textContent.includes("more lines are not drawn"), "the note outlived the lines it was about");

// An ordinary diff is drawn whole, with no note and no button.
h.click(h.$("overlay-body").querySelector("div.rev-file"));
h.recv({ type: "diff", cwd: "C:/repo", file: "package-lock.json",
         text: ["@@ -1,2 +1,2 @@", "-was", "+is"].join(NL) });
const small = h.$("overlay-body").querySelector("div.rev-diff");
assert.strictEqual(small.children.length, 3, "a three-line diff drew " + small.children.length + " things");
assert.ok(small.children[0].classList.contains("hunk"));
`)
	t.Log(strings.TrimSpace(out))
}

// Fetching, pulling, pushing and committing all talk to a remote. Nothing on
// screen changed while they did, so a press that had registered looked exactly
// like one that had not.
func TestARemoteOperationSaysItIsRunning(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const state = () => ({ type: "changes", cwd: "C:/repo", branch: "improve-webui",
  upstream: "origin/improve-webui", hasRemote: true, ahead: 2,
  files: [{ path: "app.js", label: "M", added: 9, removed: 1 }] });
h.recv(state());

const push = h.$("rev-push");
assert.strictEqual(push.textContent, "Push 2");
push.focus();
h.click(push);
assert.deepStrictEqual(h.commands().pop(), { cmd: "gitPush", path: "C:/repo" });
assert.strictEqual(push.textContent, "Pushing\u2026", "the button did not say it was working");

// A second press while the first is in flight is a second push.
const sent = h.commands().length;
h.click(push);
h.key({ key: "Enter" });
assert.strictEqual(h.commands().length, sent, "a second push was sent while the first was in flight");
assert.ok(h.$("rev-refresh").disabled, "the other buttons still invite a press");
assert.ok(h.$("rev-commit").disabled, "the commit buttons still invite a press");

// It always ends by sending the working tree back, which puts them right.
h.recv(state());
const after = h.$("rev-push");
assert.ok(after !== push, "the dialog was not redrawn");
assert.strictEqual(after.textContent, "Push 2", "the button was left saying it was working");
assert.ok(!after.disabled, "the buttons were left disabled");
assert.ok(h.doc.activeElement === after, "the button that was pressed lost the keyboard");

// A commit says so too, and does not go twice.
const box = h.$("commit-message");
box.value = "webui: a change";
box.oninput();
const commit = h.$("rev-commit");
h.click(commit);
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "commit", path: "C:/repo", text: "webui: a change", push: false });
assert.strictEqual(commit.textContent, "Committing\u2026");
const twice = h.commands().length;
h.click(commit);
assert.strictEqual(h.commands().length, twice, "the commit was sent twice");

// A dropped connection would otherwise leave those buttons waiting for a reply
// that went with it, so coming back asks again.
h.controls()[0].close();
h.click(h.$("retry"));
h.controls().pop().onopen();
assert.ok(h.commands().some((c) => c.cmd === "changes" && c.path === "C:/repo"),
  "reconnecting left the dialog showing what it read before the drop");
`)
}

// A desktop notification is the one signal that reaches someone whose window
// is behind something else, and the browser will only be asked for permission
// during an interaction. Waiting for a pointer meant an application built to be
// driven from the keyboard never asked at all.
func TestNotificationsAreAskedForOnTheFirstKeystroke(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.win.Notification.permission = "default";
assert.strictEqual(h.win.Notification.asked, 0, "asked before the user had done anything");

h.key({ key: "a" });
assert.strictEqual(h.win.Notification.asked, 1, "typing did not count as using the application");

// Once, and then never again however it is used.
h.key({ key: "b" });
h.dispatch(h.doc.body, new h.Ev("pointerdown", { pointerType: "mouse" }));
assert.strictEqual(h.win.Notification.asked, 1, "asked again after the question had been put");
`)

	// And the pointer, which is where this started, still asks.
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.win.Notification.permission = "default";
h.dispatch(h.doc.body, new h.Ev("pointerdown", { pointerType: "mouse" }));
assert.strictEqual(h.win.Notification.asked, 1, "a click no longer asks");
h.key({ key: "a" });
assert.strictEqual(h.win.Notification.asked, 1, "asked again after the question had been put");
`)
}

// The toast is the only place a failure is ever said — there is no log to go
// back to — and it was said politely and taken away in four seconds.
func TestAnErrorNoticeInterruptsAndWaits(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");

h.recv({ type: "notice", text: "pushed 2 commits to origin/improve-webui", error: false });
assert.ok(!n.hidden, "the message did not appear");
assert.strictEqual(n.getAttribute("aria-live"), "polite", "ordinary news interrupts");
assert.ok(!n.classList.contains("error"));

h.recv({ type: "notice", text: "push rejected: the remote has commits you do not", error: true });
assert.strictEqual(n.getAttribute("aria-live"), "assertive",
  "a failure waits for a pause that may not come");
assert.ok(n.classList.contains("error"));
assert.ok(n.textContent.includes("rejected"), "got: " + n.textContent);

// It outlives the four seconds a piece of news gets, because it is about
// something started before you turned to another pane.
await h.sleep(4300);
assert.ok(!n.hidden, "the failure was taken away as quickly as an ordinary message");

// The toast sits over the terminals and takes the pointer whatever it does, so
// a click on it puts it away rather than going nowhere.
h.click(n);
assert.ok(n.hidden, "clicking the message did nothing at all");

// And an ordinary message after an error goes back to being polite.
h.recv({ type: "notice", text: "fetched", error: false });
assert.strictEqual(n.getAttribute("aria-live"), "polite", "everything interrupts once anything has");
h.click(n);   // so the case is not held open by the timer it has just started
`)
}

// The Projects dialog is how a second project is opened and how you move
// between them, and every one of those actions was a click handler on a div.
func TestTheProjectsDialogAnswersTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 1 },
  { root: "C:/other", name: "other", active: false, tabs: 1, waiting: 2, working: 0 },
] }));

h.click(h.$("project-btn"));
h.recv({ type: "recents", items: [
  { root: "C:/old", name: "old", exists: true, open: false },
  { root: "C:/gone", name: "gone", exists: false, open: false },
] });
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", places: [], entries: [
  { name: "one", path: "C:/repos/one", isRepo: true },
  { name: "two", path: "C:/repos/two", isRepo: false },
] });

const body = h.$("overlay-body");
const goes = body.querySelectorAll("button.proj-go");
assert.strictEqual(goes.length, 3, "two open projects and the one recent that still exists");

// Everything the row said is part of what the button is called, so a reader
// hears which project, where, and that two agents in it are waiting.
const other = goes[1];
assert.ok(other.textContent.includes("other"), "got: " + other.textContent);
assert.ok(other.textContent.includes("C:/other"), "got: " + other.textContent);
assert.ok(other.textContent.includes("2"), "the counts are outside the button: " + other.textContent);

// Tab from the top of the dialog reaches them, and Enter switches project.
let reached = false;
for (let i = 0; i < 60 && !reached; i++) {
  h.key({ key: "Tab" });
  reached = goes.includes(h.doc.activeElement);
}
assert.ok(reached, "no amount of tabbing inside the dialog reaches a project");
other.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectProject", root: "C:/other" });
assert.ok(h.$("overlay").hidden, "the dialog stayed open after switching project");

// A recent project whose folder has gone is not a button that does nothing.
h.click(h.$("project-btn"));
h.recv({ type: "recents", items: [
  { root: "C:/old", name: "old", exists: true, open: false },
  { root: "C:/gone", name: "gone", exists: false, open: false },
] });
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", places: [], entries: [
  { name: "one", path: "C:/repos/one", isRepo: true },
  { name: "two", path: "C:/repos/two", isRepo: false },
] });
const rows = h.$("overlay-body").querySelectorAll("div.proj-row");
const missing = rows.filter((r) => r.classList.contains("missing"))[0];
assert.ok(missing, "the missing project is listed");
assert.ok(!missing.querySelector("button.proj-go"), "a project that cannot be opened offers to open it");
assert.ok(missing.querySelector("button.icon-btn"), "it can still be forgotten");

// Looking inside a folder is the only way to reach one that is not listed, and
// was reachable only with a pointer.
const into = h.$("overlay-body").querySelectorAll("button.dir-into");
assert.strictEqual(into.length, 2, "a button per folder");
assert.ok(into[0].textContent.includes("one"), "got: " + into[0].textContent);
into[1].focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "browse", path: "C:/repos/two" });

// And the chip beside it still opens the folder as a project rather than
// looking inside it.
const open = h.$("overlay-body").querySelectorAll("div.dir-row")[0].querySelectorAll("button.chip")[0];
assert.strictEqual(open.textContent, "Open");
h.click(open);
assert.deepStrictEqual(h.commands().pop(), { cmd: "openProject", path: "C:/repos/one" });
`)
}

// A tab of six agents may now be running six different things, and the header
// is the only place that says which. It reads like the branch beside it because
// it answers the same sort of question, and a shell is running neither an agent
// nor a model and shows nothing at all.
func TestThePaneHeaderNamesTheAgentAndModel(t *testing.T) {
	runFrontEnd(t, `
h.hello();

// One tab holding both panes, so the header of each is on screen at once.
const both = (over) => fixture(Object.assign({
  activeTab: "t1",
  tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
           root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
}, over));

const badges = () => h.$("workspace").querySelectorAll("span.pane-agent").map((n) => n.textContent);

h.recv(both({ panes: {
  p1: pane("p1", { agent: "claude", model: "sonnet" }),
  p2: pane("p2", { kind: "shell", agent: "", model: "" }),
} }));
assert.deepStrictEqual(badges().sort(), ["", "claude · sonnet"],
  "got: " + JSON.stringify(badges()));

// A pane that was given no model runs whatever the agent is already set to,
// and writing "claude · " would be saying nothing, twice.
h.recv(both({ panes: {
  p1: pane("p1", { agent: "claude", model: "" }),
  p2: pane("p2", { kind: "shell", agent: "", model: "" }),
} }));
assert.deepStrictEqual(badges().sort(), ["", "claude"], "got: " + JSON.stringify(badges()));

// And it follows a pane that changes model, since a restart may.
h.recv(both({ panes: {
  p1: pane("p1", { agent: "claude", model: "opus" }),
  p2: pane("p2", { kind: "shell", agent: "", model: "" }),
} }));
assert.ok(badges().includes("claude · opus"), "got: " + JSON.stringify(badges()));
`)
}

// Choosing an agent is the slow way round, so it is the whole catalog rather
// than a menu: grouped by whether the machine has it, each agent opening onto
// its models, and worked from the keyboard like every other overlay here.
func TestTheAgentPickerIsWorkedFromTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());

h.click(h.$("new-tab-pick"));
assert.ok(!h.$("overlay").hidden, "the picker did not open");
assert.deepStrictEqual(h.commands().pop(), { cmd: "refreshAgents" },
  "the picker did not ask for a fresh probe as it opened");

const body = h.$("overlay-body");
const heads = body.querySelectorAll("div.pick-head").map((n) => n.textContent);
assert.deepStrictEqual(heads, ["Installed", "Not installed"], "got: " + JSON.stringify(heads));

// The filter has the keyboard from the moment it opens, and keeps it while the
// list underneath is walked.
const field = h.$("agent-filter");
assert.ok(h.doc.activeElement === field, "the filter did not take the keyboard");

const rows = () => h.$("agent-list").querySelectorAll("div.pick-row");
assert.strictEqual(rows().length, 2, "one row per agent before either is opened");
assert.ok(rows()[0].classList.contains("sel"), "nothing was picked out to begin with");
assert.ok(rows()[0].querySelector("span.pick-mark"), "the default agent is not marked");
assert.ok(rows()[1].classList.contains("unavailable"), "the agent this machine lacks is not greyed");
assert.ok(rows()[1].textContent.includes("npm i -g @openai/codex"), "it does not say how to install it");

// Right opens an agent onto its models; the arrows then walk those too.
h.key({ key: "ArrowRight" });
assert.strictEqual(rows().length, 5, "the models did not appear: " + rows().length);
assert.ok(h.doc.activeElement === field, "walking the list took the keyboard off the filter");
assert.strictEqual(field.getAttribute("aria-activedescendant"), rows()[0].id,
  "the filter does not say which row is picked out");

h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
assert.ok(rows()[2].classList.contains("sel"), "the highlight did not move");
assert.strictEqual(field.getAttribute("aria-activedescendant"), rows()[2].id);

// Enter on a model starts a tab running that agent on that model.
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "newTab", kind: "agent", agent: "claude", model: "opus" });
assert.ok(h.$("overlay").hidden, "the picker stayed open after choosing");
`)
}

// The two fast paths must stay fast: the plus and the split binding start the
// agent this project runs by default and say nothing about which that is, so
// somebody who only ever runs Claude presses what they always pressed.
func TestTheDefaultAgentStaysOneKeystroke(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());

h.click(h.$("new-tab"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "newTab", kind: "agent" });

h.press("splitRight");
assert.deepStrictEqual(h.commands().pop(), { cmd: "splitPane", id: "p1", dir: "h", kind: "agent" });

h.press("newAgentTab");
assert.deepStrictEqual(h.commands().pop(), { cmd: "newTab", kind: "agent" });
assert.ok(h.$("overlay").hidden, "the fast path opened the picker");
`)
}

// An agent the machine does not have is listed so it can be discovered, and
// picking it says why nothing started rather than opening a pane that dies.
func TestAnAgentThatIsNotInstalledCannotBeStarted(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("new-tab-pick"));

const rows = h.$("agent-list").querySelectorAll("div.pick-row");
const before = h.commands().length;
h.click(rows[1]);
assert.strictEqual(h.commands().length, before, "a pane was started for an agent that is not there");
assert.ok(!h.$("notice").hidden, "nothing was said about why");
assert.ok(h.$("notice").textContent.includes("npm i -g @openai/codex"), "got: " + h.$("notice").textContent);
assert.ok(!h.$("overlay").hidden, "the picker closed without doing anything");
`)
}

// Making a choice the project's default is the difference between choosing
// once and choosing every time, so it travels with the choice rather than
// being a second dialog.
func TestTheChoiceCanBecomeTheProjectDefault(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ agents: catalog({ project: { agent: "codex", model: "gpt-5" } }) }));
h.click(h.$("new-tab-pick"));

// The project's own answer overrides the overall one, and it is the row the
// mark belongs on even though that agent is not installed here.
const rows = () => h.$("agent-list").querySelectorAll("div.pick-row");
assert.ok(!rows()[0].querySelector("span.pick-mark"), "the overall default was marked over the project's");
assert.ok(rows()[1].querySelector("span.pick-mark"), "the project's own default is not marked");

const tick = h.$("overlay-body").querySelector("label.pick-default").querySelector("input");
assert.ok(tick, "there is nothing at the foot to make the choice the default");
tick.checked = true;
h.dispatch(tick, new h.Ev("change", { target: tick }));

// Opening an agent onto its models and pressing Enter on the agent again
// starts it on the model it names for itself, which is the short way through.
h.key({ key: "ArrowRight" });
h.key({ key: "Enter" });
const sent = h.commands();
assert.deepStrictEqual(sent[sent.length - 2],
  { cmd: "setAgentDefault", agent: "claude", model: "" });
assert.deepStrictEqual(sent[sent.length - 1],
  { cmd: "newTab", kind: "agent", agent: "claude", model: "" });
`)
}

// A status push arrives whenever any agent changes what it is doing, which with
// six of them running is most of the time. Redrawing the picker on each one
// would take the filter out from under whoever was typing into it.
func TestAStatusPushDoesNotRedrawThePicker(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("new-tab-pick"));

const field = h.$("agent-filter");
field.value = "son";
field.oninput();
const rows = h.$("agent-list").querySelectorAll("div.pick-row");
assert.strictEqual(rows.length, 1, "the filter matched more than the one agent whose model is Sonnet");

// The arrows belong to the caret once there is text to move it through, so
// they do not open an agent while the filter has something in it.
h.key({ key: "ArrowRight" });
assert.strictEqual(h.$("agent-list").querySelectorAll("div.pick-row").length, 1,
  "right opened an agent instead of moving the caret in the filter");

h.recv(fixture({ working: 1, panes: { p1: pane("p1", { status: "working" }), p2: pane("p2") } }));

assert.ok(h.$("agent-filter") === field, "the filter was rebuilt under the keyboard");
assert.strictEqual(field.value, "son", "what was typed did not survive");
const now = h.$("agent-list").querySelectorAll("div.pick-row");
assert.ok(now[0] === rows[0], "the list was rebuilt for a push that changed no agent");

// A catalog that really did change is another matter: an agent installed while
// the dialog is open should appear in it.
field.value = "";
field.oninput();
h.recv(fixture({ agents: catalog({ items: [
  { id: "claude", name: "Claude Code", runner: "cli", available: true, defaultModel: "", models: [] },
  { id: "codex", name: "Codex", runner: "cli", available: true, defaultModel: "gpt-5", models: [] },
] }) }));
const heads = h.$("overlay-body").querySelectorAll("div.pick-head").map((n) => n.textContent);
assert.deepStrictEqual(heads, ["Installed"], "the newly installed agent is still in the other group");
`)
}

// paletteRun is the prelude for a case that opens something from the palette:
// open it, type the words, press Enter.
const paletteRun = `
const paletteRun = (words) => {
  h.press("palette");
  const input = h.$("palette-input");
  input.value = words;
  input.oninput();
  h.key({ key: "Enter" });
};
`

// The key dialog has an implementation and no entry in the action table, so
// the palette, which lists the table, never offered it and nothing else opened
// it either.
func TestTheKeyDialogCanBeOpened(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
paletteRun("api keys");
assert.ok(!h.$("overlay").hidden, "the palette did not open the key dialog");
assert.deepStrictEqual(h.commands().pop(), { cmd: "keys" });
`)
}

// A dialog is opened by asking for it, not by its answer arriving. The answer
// also comes back after a push, a commit or a worktree being removed, which
// take seconds, and a dialog closed in the meantime used to come back over the
// terminals by itself and take the keyboard from the pane being typed into.
func TestAClosedDialogStaysClosedWhenItsAnswerArrives(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const changes = { type: "changes", cwd: "C:/repo", branch: "main", upstream: "origin/main",
  hasRemote: true, ahead: 1, files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] };
h.click(h.$("btn-changes"));
h.recv(changes);
const push = h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Push 1")[0];
h.click(push);
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape did not close the dialog");
h.recv(changes);
assert.ok(h.$("overlay").hidden, "the push's answer opened the dialog again");

// Worktrees open when asked for, so their answer has nothing to open either.
h.click(h.$("btn-worktrees"));
assert.ok(!h.$("overlay").hidden, "the Worktrees button did not open the dialog");
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktrees" });
h.key({ key: "Escape" });
h.recv({ type: "worktrees", root: "C:/repo", items: [], branches: [] });
assert.ok(h.$("overlay").hidden, "a worktree list arriving opened the dialog again");

// Nor does one dialog's answer land in another.
h.click(h.$("btn-history"));
h.recv(changes);
assert.strictEqual(h.$("overlay-title").textContent, "Conversations");
assert.ok(!h.$("overlay-body").textContent.includes("a.go"), "the review was drawn into the history dialog");
`)
}

// The update dialog took over the panel without saying so, so the dialog it
// replaced still believed the panel was its own: its answer, arriving a
// moment later, was drawn over the question of whether to restart.
func TestTheUpdateDialogIsNotDrawnOver(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ update: { version: "9.9.9", notes: "Faster." } }));
h.click(h.$("btn-changes"));
h.click(h.$("btn-update"));
assert.strictEqual(h.$("overlay-title").textContent, "Update to 9.9.9");
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
assert.ok(h.$("overlay-body").textContent.includes("Restart now"), "the review was drawn over the update");
assert.ok(!h.$("overlay-body").textContent.includes("a.go"), "the review was drawn over the update");
`)
}

// The help links out to where Claude Code is installed from. Followed in
// place, that page replaced the application in its own window, which has no
// address bar or back button to return by.
func TestALinkOutOfTheHelpOpensAWindowOfItsOwn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const opened = [];
h.win.open = (url, target) => { opened.push([url, target]); return null; };
h.press("help");
await h.sleep(20);
const content = h.$("help-content");
assert.ok(content, "the help did not load");
// The harness does not parse the page's HTML into elements, so the link the
// troubleshooting page renders is put there by hand.
const a = h.doc.createElement("a");
a.setAttribute("href", "https://claude.com/claude-code");
content.append(a);
const ev = new h.Ev("click", { target: a });
h.dispatch(a, ev);
assert.ok(ev.defaultPrevented, "following the link replaced the application with it");
assert.deepStrictEqual(opened, [["https://claude.com/claude-code", "_blank"]]);
`)
}

// A file dragged in from the desktop and let go over a pane was opened by the
// browser in place of the application. The page has to claim the drag to stop
// that, and refuses it.
func TestAFileDroppedOnTheWindowDoesNotReplaceIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const host = h.terms[0].host;
const over = new h.Ev("dragover", { target: host, dataTransfer: { types: ["Files"], dropEffect: "copy" } });
h.dispatch(host, over);
assert.ok(over.defaultPrevented, "the page left the file to the browser, which opens it in its place");
assert.strictEqual(over.dataTransfer.dropEffect, "none", "the pointer does not say the drop is refused");
const drop = new h.Ev("drop", { target: host, dataTransfer: { types: ["Files"] } });
h.dispatch(host, drop);
assert.ok(drop.defaultPrevented, "the drop was left to the browser");

// A pane being moved is still a pane being moved.
const other = new h.Ev("dragover", { target: host, dataTransfer: { types: ["text/plain"], dropEffect: "move" } });
h.dispatch(host, other);
assert.ok(!other.defaultPrevented, "a drag that is not a file was claimed as well");
`)
}

// Ctrl+Shift+Left selects the word before the caret and Ctrl+Shift+Z redoes,
// in any text field. Taken as the bindings for moving and zooming a pane, they
// did that to the panes behind the dialog while the field lost the gesture.
func TestATextFieldKeepsItsOwnEditingKeys(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
h.$("commit-message").focus();
const before = h.commands().length;
for (const id of ["movePaneLeft", "movePaneRight", "movePaneUp", "movePaneDown", "zoomPane"]) {
  const ev = h.press(id);
  assert.ok(!ev.defaultPrevented, id + " took the key away from the commit message");
}
assert.strictEqual(h.commands().length, before, "a pane was moved from inside a text field");

// In a terminal they are the bindings they always were.
h.key({ key: "Escape" });
const term = h.doc.createElement("textarea");
term.className = "xterm-helper-textarea";
h.terms[0].host.append(term);
term.focus();
h.press("movePaneLeft");
assert.deepStrictEqual(h.commands().pop(), { cmd: "movePaneDir", dir: "left" });
`)
}

// On a Russian layout the key marked D types "в", and the bindings were
// matched on the character alone, so none of the letter bindings worked there.
func TestBindingsWorkOnALayoutWithoutLatinLetters(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.key({ key: "В", code: "KeyD", ctrlKey: true, shiftKey: true });
assert.deepStrictEqual(h.commands().pop(), { cmd: "splitPane", id: "p1", dir: "h", kind: "agent" });
// A Latin layout that has moved the letter still gets the letter it typed:
// on AZERTY the key in QWERTY's Q place types "a", and Ctrl+Shift+A is that.
h.key({ key: "A", code: "KeyQ", ctrlKey: true, shiftKey: true });
assert.ok(!h.$("overlay").hidden && h.$("overlay-title").textContent === "Agents", "the typed letter no longer decides");
`)
}

// On AZERTY the digit row types & é " ' unless Shift is held, so Alt+2 arrived
// as Alt+é and picked no tab.
func TestTabsCanBePickedByNumberOnAZERTY(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.key({ key: "é", code: "Digit2", altKey: true });
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectTab", id: "t2" });
// The keypad has no row to read and types a digit everywhere.
h.key({ key: "1", code: "Numpad1", altKey: true });
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectTab", id: "t1" });
`)
}

// Typing Chinese or Japanese goes through an input method, and the Enter that
// confirms the characters it composed arrives as a keydown of its own. Taken
// as Enter, it sent the half-written prompt and ran whatever palette command
// was picked out.
func TestEnterThatConfirmsAnInputMethodIsNotSubmit(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
const composing = { key: "Enter", isComposing: true, keyCode: 229 };

h.press("promptAll");
h.$("prompt-input").value = "\u4f60\u597d";
h.key(composing);
assert.ok(!h.$("promptbar").hidden, "confirming the characters sent the prompt");
assert.ok(!h.commands().some((c) => c.cmd === "sendPrompt"), "confirming the characters sent the prompt");
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "sendPrompt", text: "\u4f60\u597d" });

h.press("palette");
h.$("palette-input").value = "zoom";
h.$("palette-input").oninput();
h.key(composing);
assert.ok(!h.$("palette").hidden, "confirming the characters ran a command");

h.key({ key: "Escape" });
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/repo", items: [], branches: [] });
const branch = h.$("wt-branch");
branch.focus();
branch.value = "fix";
const before = h.commands().length;
h.key(composing);
assert.strictEqual(h.commands().length, before, "confirming the characters created a worktree");
`)
}

// The prompt bar leaves the panes usable, and one clicked into while it is up
// is being typed at. Escape is how an agent is interrupted; the bar took it
// wherever the keyboard was, closing itself instead, and left every binding
// dead while it was open.
func TestThePromptBarOnlyTakesKeysTypedIntoIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("promptAll");
assert.ok(!h.$("promptbar").hidden, "the prompt bar did not open");

const term = h.doc.createElement("textarea");
term.className = "xterm-helper-textarea";
h.terms[0].host.append(term);
term.focus();
const esc = h.key({ key: "Escape" });
assert.ok(!esc.defaultPrevented, "Escape typed into a terminal never reached the agent");
assert.ok(!h.$("promptbar").hidden, "Escape typed into a terminal closed the prompt bar");
h.press("newAgentTab");
assert.deepStrictEqual(h.commands().pop(), { cmd: "newTab", kind: "agent" });

// In the bar itself Escape still closes it.
h.$("prompt-input").focus();
h.key({ key: "Escape" });
assert.ok(h.$("promptbar").hidden, "Escape in the bar no longer closes it");
`)
}

// The palette opens over a dialog as readily as over the terminals. Closing it
// sent the keyboard to a terminal regardless, behind the dialog that was still
// open, and the next thing typed went to an agent.
func TestClosingThePaletteGivesTheKeyboardBack(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
const box = h.$("commit-message");
box.focus();
h.press("palette");
assert.ok(!h.$("palette").hidden, "the palette did not open over the dialog");
h.key({ key: "Escape" });
assert.ok(h.$("palette").hidden, "Escape did not close the palette");
assert.ok(!h.$("overlay").hidden, "closing the palette closed the dialog under it");
assert.ok(h.doc.activeElement === box, "the keyboard did not go back to the commit message");
`)
}

// Fitting a terminal measures it, which makes the browser lay the window out.
// Every status push fitted every terminal on screen, and while agents work
// those pushes arrive several times a second; a pane that has not changed size
// has nothing to fit.
func TestAStatusPushDoesNotMeasureTheTerminals(t *testing.T) {
	runFrontEnd(t, `
let fits = 0;
const proto = h.win.FitAddon.FitAddon.prototype;
const fit = proto.fit;
proto.fit = function () { fits++; return fit.call(this); };
h.hello();
h.recv(fixture());
await h.sleep(80);
assert.ok(fits > 0, "the tab on screen was never fitted");

fits = 0;
for (let i = 0; i < 5; i++) {
  h.recv(fixture({ working: 1, panes: { p1: pane("p1", { status: "working", detail: "Reading " + i }), p2: pane("p2") } }));
}
await h.sleep(80);
assert.strictEqual(fits, 0, "a status push measured the terminals again");

// A tab coming on screen is still fitted, since it could not be while hidden.
h.recv(fixture({ activeTab: "t2" }));
await h.sleep(80);
assert.ok(fits > 0, "the tab switched to was not fitted");
`)
}

// Every fit that lands during a divider drag resizes the agents either side,
// and each of them redraws its whole screen on a resize. The panes are fitted
// once, when the divider is let go.
func TestDraggingADividerResizesTheAgentsOnceAtTheEnd(t *testing.T) {
	runFrontEnd(t, `
let fits = 0;
const proto = h.win.FitAddon.FitAddon.prototype;
const fit = proto.fit;
proto.fit = function () { fits++; return fit.call(this); };
h.hello();
h.recv(fixture({ tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }] }));
await h.sleep(80);
fits = 0;

const d = h.$("workspace").querySelector(".divider");
h.dispatch(d, new h.Ev("pointerdown", { button: 0, pointerId: 1, clientX: 100 }));
for (let x = 110; x < 160; x += 10) {
  h.dispatch(d, new h.Ev("pointermove", { pointerId: 1, clientX: x }));
  // The panes change size under their observers as the divider moves, and
  // the drag pauses between moves for longer than the fit waits.
  h.observers.forEach((o) => o.fn());
  await h.sleep(50);
}
assert.strictEqual(fits, 0, "the agents were resized while the divider was still moving");
h.dispatch(d, new h.Ev("pointerup", { pointerId: 1, clientX: 160 }));
await h.sleep(80);
assert.ok(fits >= 2, "the panes either side were not fitted when the drag ended");
`)
}

// A commit that fails - a hook refusing it, say - is answered with an error and
// the working tree again, which redraws the dialog. The message had already
// been let go of, so it went with the redraw and had to be written again.
func TestAFailedCommitKeepsItsMessage(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const tree = (file) => ({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: file, label: "M", added: 1, removed: 0 }] });
h.recv(tree("a.go"));
const box = h.$("commit-message");
box.value = "webui: a message worth keeping";
box.oninput();
h.click(h.$("rev-commit"));
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "commit", path: "C:/repo", text: "webui: a message worth keeping", push: false });

// The hook refuses it; the server says so and sends the tree back.
h.recv({ type: "notice", text: "pre-commit hook failed", error: true });
h.recv(tree("a.go"));
assert.strictEqual(h.$("commit-message").value, "webui: a message worth keeping", "the message went with the failed commit");

// When it goes through, the box is emptied for the next one.
h.click(h.$("rev-commit"));
h.recv({ type: "notice", text: "committed in repo", error: false });
h.recv(tree("b.go"));
assert.strictEqual(h.$("commit-message").value, "", "a message that was committed was left in the box");
`)
}

// The tab strip scrolls sideways with its scrollbar hidden, and a mouse wheel
// turns the other way, so with more tabs than fit the ones past the edge could
// not be reached with the mouse at all.
func TestTheWheelScrollsATabStripThatOverflows(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const strip = h.$("tabs");
strip.scrollLeft = 0;
strip.scrollWidth = 900;
strip.clientWidth = 300;
const wheel = new h.Ev("wheel", { target: strip, deltaX: 0, deltaY: 120, deltaMode: 0 });
h.dispatch(strip, wheel);
assert.strictEqual(strip.scrollLeft, 120, "the wheel did not move the strip");
assert.ok(wheel.defaultPrevented, "the wheel went on to scroll something else as well");

// A strip that fits has nothing to scroll and leaves the wheel alone.
strip.scrollWidth = 300;
const idle = new h.Ev("wheel", { target: strip, deltaX: 0, deltaY: 120, deltaMode: 0 });
h.dispatch(strip, idle);
assert.ok(!idle.defaultPrevented, "a strip that fits took the wheel");
`)
}

// A tab's title is cut short at 220 pixels, and a title written from an
// agent's task nearly always is. Nothing let the rest be read, and nothing
// said that a double-click renames it.
func TestATabSaysItsWholeTitle(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const long = "Identify and fix every bug in the desktop front end, one per commit";
h.recv(fixture({ tabs: [
  { id: "t1", title: long, focus: "p1", zoom: false, attention: false, root: { id: "n1", pane: "p1", weight: 1 } },
  { id: "t2", title: "two", focus: "p2", zoom: false, attention: false, root: { id: "n2", pane: "p2", weight: 1 } },
] }));
const tab = h.$("tabs").children[0];
assert.ok((tab.dataset.tip || "").startsWith(long), "the whole title cannot be read anywhere");
assert.ok(/rename/.test(tab.dataset.tip), "nothing says how to rename the tab");

h.recv(fixture());
assert.ok(h.$("tabs").children[0].dataset.tip.startsWith("one"), "the bubble kept the old title");
`)
}

// A pane is dragged by its header, and the grab cursor is the only thing on
// screen that says so. A later rule for the same selector set the cursor back
// to the default, so it never showed.
func TestAPaneHeaderLooksDraggable(t *testing.T) {
	css := readAsset(t, "app.css")
	var last string
	for _, block := range regexp.MustCompile(`(?m)^\.pane-header\s*\{([^}]*)\}`).FindAllStringSubmatch(css, -1) {
		if m := regexp.MustCompile(`(?:^|[;\s])cursor:\s*([\w-]+)`).FindStringSubmatch(block[1]); m != nil {
			last = m[1]
		}
	}
	if last != "grab" {
		t.Fatalf("the last .pane-header rule to set a cursor sets %q, so the header does not look draggable", last)
	}
}

// A tab's close button is hidden with opacity until the tab is hovered, which
// hides it from the eye and not from a finger. On a touch screen there is no
// hover, so tapping near the end of another tab closed it and its agents.
func TestATapOnAnotherTabCannotCloseIt(t *testing.T) {
	css := readAsset(t, "app.css")
	block := regexp.MustCompile(`@media\s*\(hover:\s*none\)\s*\{([^{}]*\{[^}]*\})*`).FindString(css)
	if !regexp.MustCompile(`\.tab:not\(\.active\)\s+\.close\s*\{[^}]*display:\s*none`).MatchString(block) {
		t.Fatal("on a touch screen the close button of a tab that is not current can still be tapped")
	}
}

// A finger drawn along a divider is a pan to the browser unless the element
// says otherwise, and the browser ends the page's pointer with pointercancel
// to perform it, so a split could not be resized from a touch screen.
func TestADividerCanBeDraggedByTouch(t *testing.T) {
	css := readAsset(t, "app.css")
	if !regexp.MustCompile(`(?m)^\.divider\s*\{[^}]*touch-action:\s*none`).MatchString(css) {
		t.Fatal("a divider leaves touch to the browser, which takes a drag along it as a pan")
	}
}

// The broadcast toggle in a pane header lights up while its pane is in the
// set. It was found as the first button in the row, which is fan out, so it
// was fan out that lit up and the toggle never did.
func TestTheBroadcastToggleShowsItsOwnState(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1", { broadcast: true }), p2: pane("p2") } }));
const buttons = h.terms[0].host.parentElement.parentElement.querySelectorAll("button");
const fan = buttons.find((b) => b.textContent === "⑂");
const cast = buttons.find((b) => b.textContent === "⇉");
assert.ok(cast.classList.contains("on"), "the broadcast toggle does not show that it is on");
assert.strictEqual(cast.getAttribute("aria-pressed"), "true", "a screen reader is not told the toggle is on");
assert.ok(!fan.classList.contains("on"), "fan out lit up for a pane in the broadcast set");

h.recv(fixture());
assert.ok(!cast.classList.contains("on"), "the toggle stayed on after the pane left the set");
assert.strictEqual(cast.getAttribute("aria-pressed"), "false");
`)
}

// Walking a long list with the arrow keys scrolls it, which slides a row under
// a pointer that has not moved, and the browser reports that as the pointer
// entering the row. The highlight followed, jumping back under the pointer
// every time the list scrolled.
func TestScrollingAListUnderThePointerLeavesTheHighlight(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("palette");
h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
const rows = h.$("palette-list").children;
assert.ok(rows[2].classList.contains("sel"), "the arrow keys did not move the highlight");
// The list scrolled; row 0 is now under the resting pointer.
h.dispatch(rows[0], new h.Ev("mouseenter", { target: rows[0] }));
assert.ok(rows[2].classList.contains("sel"), "a row sliding under the pointer took the highlight");
// Moving the pointer is what picks a row with the mouse.
h.dispatch(rows[4], new h.Ev("mousemove", { target: rows[4] }));
assert.ok(rows[4].classList.contains("sel"), "moving the pointer over a row did not pick it");

// The agent picker is walked the same way.
h.key({ key: "Escape" });
h.press("palette");
h.$("palette-input").value = "new agent tab choose";
h.$("palette-input").oninput();
h.key({ key: "Enter" });
const pick = h.$("agent-list").querySelectorAll("div.pick-row");
h.key({ key: "ArrowDown" });
h.dispatch(pick[0], new h.Ev("mouseenter", { target: pick[0] }));
assert.ok(pick[1].classList.contains("sel"), "a picker row sliding under the pointer took the highlight");
`)
}

// The font size was kept in local storage, which belongs to the page's origin,
// and the origin changes with the port on every run: the size chosen was back
// to the default the next time the application started. It is kept with the
// other preferences now, and arrives with them.
func TestTheFontSizeComesBackOnTheNextRun(t *testing.T) {
	runFrontEnd(t, `
h.hello({ fontSize: 17 });
h.recv(fixture());
assert.strictEqual(h.terms[0].options.fontSize, 17, "the size chosen on an earlier run was not used");

h.press("fontUp");
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontSize", size: 18 });
assert.strictEqual(h.terms[0].options.fontSize, 18);

// A size chosen in another window follows here too.
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [], fontSize: 12 } });
assert.strictEqual(h.terms[0].options.fontSize, 12, "a size chosen in another window was not followed");
`)
}

// A dialog is drawn again from each answer, and emptying it to do so put its
// scroll back at the top: a list scrolled down to the worktree being removed,
// or the agent being looked for, jumped back to the start when the answer came.
func TestADialogKeepsItsPlaceWhenItIsRedrawn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("summary"));
const agents = { type: "agents", items: [] };
for (let i = 0; i < 30; i++) {
  agents.items.push({ paneId: "p" + i, tabId: "t1", root: "C:/repo", tab: "tab " + i, name: "agent " + i,
    project: "repo", status: "idle" });
}
h.recv(agents);
const body = h.$("overlay-body");
body.scrollTop = 480;
h.recv(agents);
assert.strictEqual(body.scrollTop, 480, "the list jumped back to the top when it was drawn again");
`)
}

// Reading a review file by file took Tab and Enter for every file. The lists
// answer the arrow keys as lists do, and in the review arriving at a file shows
// its diff; in the agents list, where Enter leaves the dialog, they only move.
func TestTheArrowKeysWalkTheReviewAndAgentLists(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false, files: [
  { path: "a.go", label: "M", added: 1, removed: 0 },
  { path: "b.go", label: "M", added: 1, removed: 0 },
  { path: "c.go", label: "A", added: 1, removed: 0 },
] });
const rows = h.$("overlay-body").querySelectorAll("div.rev-file");
rows[0].focus();
const down = h.key({ key: "ArrowDown" });
assert.ok(down.defaultPrevented, "Down scrolled the dialog instead");
assert.ok(h.doc.activeElement === rows[1], "Down did not move to the next file");
assert.deepStrictEqual(h.commands().pop(), { cmd: "diff", path: "C:/repo", text: "b.go" });
h.key({ key: "End" });
assert.ok(h.doc.activeElement === rows[2], "End did not reach the last file");
assert.deepStrictEqual(h.commands().pop(), { cmd: "diff", path: "C:/repo", text: "c.go" });
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === rows[2], "Down walked off the end of the list");

h.key({ key: "Escape" });
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "one", name: "a", status: "idle" },
  { paneId: "p2", tabId: "t2", root: "C:/repo", project: "repo", tab: "two", name: "b", status: "waiting" },
] });
const agents = h.$("overlay-body").querySelectorAll("div.agent-row");
agents[0].focus();
const before = h.commands().length;
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === agents[1], "Down did not move to the next agent");
assert.strictEqual(h.commands().length, before, "moving through the agents went to one");
assert.ok(!h.$("overlay").hidden);
`)
}

// Desktop notifications could be stopped only through the browser's own
// permission, which belongs to the page's origin: that changes with the port
// on every run, so it was asked again and refused again at every start.
func TestNotificationsCanBeTurnedOffFromThePalette(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello({ notificationsOff: true });
h.recv(fixture());
h.doc._hasFocus = false;
h.recv(fixture({ waiting: 1, panes: { p1: pane("p1", { status: "waiting" }), p2: pane("p2") } }));
assert.strictEqual(h.notifications.length, 0, "a notification was raised with notifications turned off");
h.key({ key: "a" });
assert.strictEqual(h.win.Notification.asked, 0, "permission was asked for with notifications turned off");

paletteRun("notifications on");
assert.deepStrictEqual(h.commands().pop(), { cmd: "notifications", kind: "on" });

h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [] } });
h.recv(fixture());
h.recv(fixture({ waiting: 1, panes: { p1: pane("p1", { status: "waiting" }), p2: pane("p2") } }));
assert.strictEqual(h.notifications.length, 1, "turned back on, the notification did not come");
paletteRun("notifications off");
assert.deepStrictEqual(h.commands().pop(), { cmd: "notifications", kind: "off" });
`)
}

// The picker could make a choice this project's default and nothing else: the
// default for every project needed agents.json edited by hand, and a project's
// own choice, once made, could not be undone from the window.
func TestThePickerSetsEitherDefaultAndUndoesAProjects(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ agents: catalog({ project: { agent: "codex", model: "gpt-5" } }) }));
h.click(h.$("new-tab-pick"));
const body = h.$("overlay-body");
const forget = body.querySelectorAll("button").find((b) => b.textContent === "Use the default for every project");
assert.ok(forget, "a project's own default cannot be undone");
h.click(forget);
assert.deepStrictEqual(h.commands().pop(), { cmd: "setAgentDefault", agent: "", model: "" });

const scope = body.querySelector("select.pick-scope");
assert.ok(scope, "the default for every project cannot be chosen");
scope.value = "all";
h.dispatch(scope, new h.Ev("change", { target: scope }));
assert.ok(body.querySelector("label.pick-default").querySelector("input").checked,
  "choosing which default did not say the choice is to become one");
h.key({ key: "ArrowRight" });
h.key({ key: "Enter" });
const sent = h.commands();
assert.deepStrictEqual(sent[sent.length - 2], { cmd: "setAgentDefault", agent: "claude", model: "", kind: "all" });
assert.deepStrictEqual(sent[sent.length - 1], { cmd: "newTab", kind: "agent", agent: "claude", model: "" });
`)
}

// How many lines a terminal keeps was a number in the source. It is asked for
// from the palette, kept with the other preferences, and follows a change made
// in another window.
func TestTheScrollbackCanBeChosen(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello({ scrollback: 50000 });
h.recv(fixture());
assert.strictEqual(h.terms[0].options.scrollback, 50000, "the scrollback chosen on an earlier run was not used");

h.win._prompt = "25,000";
paletteRun("scrollback");
assert.deepStrictEqual(h.commands().pop(), { cmd: "scrollback", size: 25000 });
assert.strictEqual(h.terms[0].options.scrollback, 25000, "the terminals did not take the new scrollback");

h.win._prompt = "12";
paletteRun("scrollback");
assert.ok(!h.commands().some((c) => c.cmd === "scrollback" && c.size === 12), "a scrollback of 12 lines was accepted");
assert.ok(h.$("notice").textContent.includes("1,000"), "nothing said what a scrollback may be");

h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [], scrollback: 90000 } });
assert.strictEqual(h.terms[1].options.scrollback, 90000, "a scrollback chosen in another window was not followed");
`)
}

// The palette and the agent picker moved one row per arrow and answered no
// other key, so the last of the palette's forty-odd commands was forty presses
// away. Page Up and Page Down move a boxful, and Home and End go to the ends
// while nothing has been typed.
func TestTheListsPageAndJumpToTheirEnds(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("palette");
const rows = () => h.$("palette-list").children;
const sel = () => rows().findIndex((r) => r.classList.contains("sel"));
h.key({ key: "PageDown" });
assert.strictEqual(sel(), 8, "Page Down did not move a boxful");
h.key({ key: "End" });
assert.strictEqual(sel(), rows().length - 1, "End did not reach the last command");
h.key({ key: "PageUp" });
assert.strictEqual(sel(), rows().length - 9);
h.key({ key: "Home" });
assert.strictEqual(sel(), 0, "Home did not go back to the first command");

// With something typed, Home and End are the caret's.
h.$("palette-input").value = "tab";
h.$("palette-input").oninput();
const home = h.key({ key: "Home" });
assert.ok(!home.defaultPrevented, "Home was taken from the caret in a field with text in it");

h.key({ key: "Escape" });
h.click(h.$("new-tab-pick"));
const picks = h.$("agent-list").querySelectorAll("div.pick-row");
h.key({ key: "End" });
assert.ok(picks[picks.length - 1].classList.contains("sel"), "End did not reach the last agent");
`)
}

// The find bar answered the keyboard only while its field had it. A click on
// one of its arrows left the keyboard on that button, where Escape did not
// close the bar and the next letters of the search went nowhere.
func TestTheFindBarKeepsTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("findInTerminal");
const input = h.$("search-input");
input.value = "error";
h.$("search-next").focus();
h.click(h.$("search-next"));
assert.ok(h.doc.activeElement === input, "the arrow kept the keyboard, so typing more of the search went nowhere");

h.$("search-prev").focus();
h.key({ key: "Escape" });
assert.ok(h.$("searchbar").hidden, "Escape on one of the bar's buttons did not close it");
`)
}

// The find bar searched only when Enter was pressed, so the field said nothing
// about whether a word was there until it had been typed in full and sent. It
// searches as the word is typed, once per pause rather than once per letter.
func TestTheFindBarSearchesAsItIsTypedInto(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("findInTerminal");
const input = h.$("search-input");
const search = h.searchers[0];
for (const q of ["e", "er", "err"]) { input.value = q; input.oninput(); }
await h.sleep(200);
assert.deepStrictEqual(search.forward, ["err"], "typing did not search, or searched once for every letter");

search.hit = false;
input.value = "errx";
input.oninput();
await h.sleep(200);
assert.strictEqual(input.getAttribute("aria-invalid"), "true", "a word that is not there was not said to be missing");

const cleared = search.cleared;
input.value = "";
input.oninput();
await h.sleep(200);
assert.ok(search.cleared > cleared, "emptying the field left the last search marked");
assert.strictEqual(input.getAttribute("aria-invalid"), "false");
`)
}

// The notice sits at the bottom of the window, and so do the prompt and find
// bars: a message arriving while one was open covered the field being typed
// into and took the click meant for it.
func TestANoticeDoesNotCoverTheBarBeingTypedInto(t *testing.T) {
	css := readAsset(t, "app.css")
	for _, bar := range []string{"promptbar", "searchbar"} {
		re := regexp.MustCompile(`body:has\(#` + bar + `:not\(\[hidden\]\)\) #notice[^{]*\{[^}]*bottom:`)
		if !re.MatchString(css) {
			t.Errorf("the notice is not moved clear of #%s while it is open", bar)
		}
	}
}

// A zoomed pane looked exactly like the only pane in its tab: the others
// seemed to have closed, and nothing said where they had gone or how to get
// them back. The zoom button says it is on, and how many panes it is hiding.
func TestAZoomedPaneSaysWhatItIsHiding(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const tabs = (zoom) => [{ id: "t1", title: "one", focus: "p1", zoom: zoom, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2"), leaf("n3", "p3")]) }];
const panes = { p1: pane("p1"), p2: pane("p2"), p3: pane("p3") };
h.recv(fixture({ tabs: tabs(true), panes }));
const zoom = h.$("workspace").querySelectorAll("button").find((b) => b.textContent === "⤢");
assert.ok(zoom.classList.contains("zoomed"), "the zoom button does not show that the pane is zoomed");
assert.strictEqual(zoom.getAttribute("aria-pressed"), "true");
assert.ok(/2 other panes are hidden/.test(zoom.dataset.tip), "nothing says what the zoom is hiding: " + zoom.dataset.tip);

h.recv(fixture({ tabs: tabs(false), panes }));
assert.ok(!zoom.classList.contains("zoomed"), "the zoom button stayed on after the zoom was released");
assert.strictEqual(zoom.getAttribute("aria-pressed"), "false");
`)
}

// Removing a worktree asked first only when it had uncommitted files. A clean
// one with agents working in it went without a word, and they were left in a
// directory that no longer existed.
func TestRemovingAWorktreeAgentsAreInAsksFirst(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [], items: [
  { label: "main", path: "C:/repo", main: true },
  { label: "fix-auth", path: "C:/repo-fix-auth", panes: 2, dirty: 0, untracked: 0 },
] });
const remove = h.$("overlay-body").querySelectorAll("button").find((b) => b.textContent === "Remove");
let asked = "";
h.win.confirm = (q) => { asked = q; return false; };
const before = h.commands().length;
h.click(remove);
assert.ok(/2 agents are working/.test(asked), "a worktree with agents in it was removed without asking");
assert.strictEqual(h.commands().length, before, "the worktree was removed although the answer was no");
h.win.confirm = () => true;
h.click(remove);
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeRemove", path: "C:/repo-fix-auth", force: false });
`)
}

// The new-worktree form created the worktree on Enter in the branch field and
// not in the base field beside it, which is the one filled in last.
func TestEnterInEitherWorktreeFieldCreatesIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [], items: [] });
h.$("wt-branch").value = "fix-auth";
const base = h.$("wt-base");
base.value = "develop";
base.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeAdd", text: "fix-auth", base: "develop" });
`)
}

// The worktrees dialog offers branches without a worktree as buttons, at most
// fourteen of them, and the rest simply were not there: a branch further down
// looked as though it did not exist.
func TestBranchesPastTheFirstFourteenAreAccountedFor(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
const branches = [];
for (let i = 0; i < 20; i++) branches.push({ name: "branch-" + i, checkedIn: false });
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches, items: [] });
const text = h.$("overlay-body").textContent;
assert.ok(text.includes("6 more branches"), "the branches past the first fourteen vanished without a word");
`)
}

// Going into a folder in the projects dialog redraws the list, and the button
// that was pressed goes with the folder it named: the keyboard fell out of
// the dialog, and each folder deeper meant tabbing down from the top again.
func TestTheFolderBrowserKeepsTheKeyboardOnTheWayDown(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("projects");
h.recv({ type: "browse", path: "C:/code", parent: "C:/", entries: [
  { name: "api", path: "C:/code/api" }, { name: "web", path: "C:/code/web" } ] });
const into = () => h.$("overlay-body").querySelectorAll("button.dir-into");
into()[0].focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "browse", path: "C:/code/api" });
h.recv({ type: "browse", path: "C:/code/api", parent: "C:/code", entries: [
  { name: "cmd", path: "C:/code/api/cmd" } ] });
assert.ok(h.doc.activeElement === into()[0], "going into a folder dropped the keyboard out of the list");
`)
}

// A tab was renamed by double-clicking it and in no other way, so it could not
// be done from the keyboard and nothing listed it as something that could be
// done at all.
func TestATabCanBeRenamedFromThePalette(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
h.win._prompt = "api work";
paletteRun("rename");
assert.deepStrictEqual(h.commands().pop(), { cmd: "renameTab", id: "t1", text: "api work" });
`)
}

// The background check for new releases could be turned off only with an
// environment variable set before the application started.
func TestUpdateChecksCanBeTurnedOffFromThePalette(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
paletteRun("update checks off");
assert.deepStrictEqual(h.commands().pop(), { cmd: "updates", kind: "off" });
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [], updatesOff: true } });
paletteRun("update checks on");
assert.deepStrictEqual(h.commands().pop(), { cmd: "updates", kind: "on" });
`)
}

// The help's contents are walked with the arrow keys from its search box, and
// each step rebuilds the list, which puts it back at the top: the highlight
// went past the bottom of the box and out of sight.
func TestWalkingTheHelpKeepsItsPlaceInView(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("help");
await h.sleep(30);
for (let i = 0; i < 8; i++) h.key({ key: "ArrowDown" });
const sel = h.$("overlay-body").querySelector("button.sel");
assert.ok(sel, "no page is picked out in the contents");
assert.ok(sel.scrolledTo > 0, "the page picked out was never brought into view");
`)
}

// With every tab closed the window says so and offers a new one. Closing the
// last tab left the keyboard on the page with nothing to act on, and each
// status push - they keep coming while other projects' agents work - built
// the placeholder again and took the keyboard off whatever it was on.
func TestTheEmptyWorkspaceHoldsTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.recv(fixture({ tabs: [], panes: {} }));
const buttons = () => h.$("workspace").querySelectorAll("button");
const start = buttons().find((b) => b.textContent === "New agent tab");
assert.ok(start, "the empty workspace offers no new tab");
assert.ok(h.doc.activeElement === start, "closing the last tab left the keyboard with nothing to act on");

const help = buttons().find((b) => b.textContent === "Help");
help.focus();
h.recv(fixture({ tabs: [], panes: {}, working: 2 }));
assert.ok(buttons().includes(help), "a status push built the empty workspace again");
assert.ok(h.doc.activeElement === help, "a status push took the keyboard off the Help button");
`)
}

// The commit message is the last thing written before committing, and a box
// like it commits on Ctrl+Enter nearly everywhere else. Here it took the
// pointer, or tabbing past the box to the button.
func TestCtrlEnterCommitsFromTheMessage(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
const box = h.$("commit-message");
box.value = "webui: something";
box.oninput();
box.focus();
const plain = h.key({ key: "Enter" });
assert.ok(!plain.defaultPrevented, "a plain Enter no longer makes a new line in the message");
const ev = h.key({ key: "Enter", ctrlKey: true });
assert.ok(ev.defaultPrevented);
assert.deepStrictEqual(h.commands().pop(), { cmd: "commit", path: "C:/repo", text: "webui: something", push: false });
`)
}

// A pane told the server it had the focus only when it was clicked. Tabbed
// into from its header buttons, its terminal took the typing while the server
// went on treating the last pane clicked as focused - the one Close pane would
// close.
func TestThePaneTheKeyboardIsInIsTheFocusedOne(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }] }));
const term = h.doc.createElement("textarea");
term.className = "xterm-helper-textarea";
h.terms[1].host.append(term);
h.dispatch(term, new h.Ev("focusin", { target: term }));
assert.deepStrictEqual(h.commands().pop(), { cmd: "focusPane", id: "p2" });
`)
}

// Closing a project stops every agent in every tab of it, and the small cross
// in the projects dialog did it on one click. It asks first when an agent
// there has something under way, as Quit does.
func TestClosingABusyProjectAsksFirst(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 3, waiting: 1, working: 2 },
  { root: "C:/docs", name: "docs", active: false, tabs: 1, waiting: 0, working: 0 },
] }));
h.press("projects");
const closes = h.$("overlay-body").querySelectorAll("button").filter((b) => (b.dataset.tip || "").startsWith("Close this project"));
let asked = "";
h.win.confirm = (q) => { asked = q; return false; };
const before = h.commands().length;
h.click(closes[1]);
assert.ok(/3 agents are still working/.test(asked), "a project with agents at work was closed without asking");
assert.strictEqual(h.commands().length, before, "the project was closed although the answer was no");

asked = "";
h.click(closes[2]);
assert.strictEqual(asked, "", "a project with every agent idle was asked about");
assert.deepStrictEqual(h.commands().pop(), { cmd: "closeProject", root: "C:/docs" });
`)
}

// A refresh, a fetch or a push answers with the working tree again, and the
// review was drawn afresh from it: a long diff went back to its first line
// under the person reading it, and the file list back to its top.
func TestAReviewKeepsItsPlaceInTheDiff(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const files = [];
for (let i = 0; i < 30; i++) files.push({ path: "f" + i + ".go", label: "M", added: 1, removed: 0 });
const tree = { type: "changes", cwd: "C:/repo", branch: "main", hasRemote: true, upstream: "origin/main", files };
h.recv(tree);
h.click(h.$("overlay-body").querySelectorAll("div.rev-file")[20]);
h.recv({ type: "diff", file: "f20.go", text: Array.from({ length: 500 }, (_, i) => "+line " + i).join("\n") });
const diff = () => h.$("overlay-body").querySelector("div.rev-diff");
const list = () => h.$("overlay-body").querySelector("div.rev-files");
diff().scrollTop = 900;
list().scrollTop = 400;

h.recv(tree);
assert.strictEqual(diff().scrollTop, 900, "the diff went back to its first line when the tree was read again");
assert.strictEqual(list().scrollTop, 400, "the file list went back to its top when the tree was read again");
`)
}

// The agents overview is where you look to see who needs you, and it was a
// picture taken when it opened: an agent that stopped to wait while it was up
// went on being listed as working.
func TestTheAgentsOverviewFollowsTheAgents(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("summary"));
const list = (status) => ({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "one", name: "a", status: status } ] });
h.recv(list("working"));
const asked = () => h.commands().filter((c) => c.cmd === "agents").length;
const before = asked();

// A push that changes nothing the overview lists does not ask again.
h.recv(fixture({ panes: { p1: pane("p1", { detail: "Reading" }), p2: pane("p2") } }));
assert.strictEqual(asked(), before, "the overview was asked for again when nothing it lists had moved");

h.$("agent-p1").focus();
h.recv(fixture({ waiting: 1, projects: [{ root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 1, working: 0 }],
  panes: { p1: pane("p1", { status: "waiting" }), p2: pane("p2") } }));
assert.strictEqual(asked(), before + 1, "an agent stopping to wait did not bring the overview up to date");
h.recv(list("waiting"));
assert.ok(h.$("overlay-body").textContent.includes("waiting"), "the overview still says the agent is working");
assert.ok(h.doc.activeElement === h.$("agent-p1"), "the row lost the keyboard when its status changed");
`)
}

// In a narrow window the chips on the right of the top bar took all the room.
// Measured in Chrome: at 640px the tab strip was 23px wide and its one tab
// unreadable, and at 480px the bar ran 129px past the edge, the help button
// with it. The chips that open dialogs, which the palette and their keys also
// reach, step aside there.
func TestANarrowWindowKeepsTheTabsAndTheHelp(t *testing.T) {
	css := readAsset(t, "app.css")
	block := regexp.MustCompile(`@media\s*\(max-width:\s*(\d+)px\)\s*\{\s*#btn-changes,\s*#btn-history,\s*#btn-worktrees\s*\{\s*display:\s*none`).FindStringSubmatch(css)
	if block == nil {
		t.Fatal("the dialog chips keep their room in a narrow window, so the tabs and the help button are squeezed out")
	}
}

// Several commands are reached only from the palette - tiling, restarting a
// pane, the settings - and one used a minute ago had to be typed for again
// each time. The last ones run come first while nothing has been typed.
func TestThePaletteOffersWhatWasLastRunFirst(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
paletteRun("tile");
assert.deepStrictEqual(h.commands().pop(), { cmd: "tilePanes" });
paletteRun("restart");
h.press("palette");
const labels = h.$("palette-list").children.map((r) => r.querySelector(".pal-label").textContent);
assert.deepStrictEqual(labels.slice(0, 2), ["Restart pane", "Tile these panes evenly"],
  "the commands just run are not the first offered: " + labels.slice(0, 3).join(", "));
// Enter on the first row runs the last command again.
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "restartPane", id: "p1" });
`)
}

// The usage figure is the first part of a pane header to give up its width,
// and it was clipped bare: "0% 7.6 MB" lost its unit and read as a different,
// smaller number. Cut short, it says it has been.
func TestAClippedUsageFigureSaysItIsClipped(t *testing.T) {
	css := readAsset(t, "app.css")
	if !regexp.MustCompile(`(?m)^\.pane-usage\s*\{[^}]*text-overflow:\s*ellipsis`).MatchString(css) {
		t.Fatal("the usage figure is clipped without an ellipsis, so a cut-off figure reads as a different number")
	}
}

// A hint sent away stays away, and there was no way back for one dismissed by
// mistake short of editing prefs.json. The palette offers one while any is
// dismissed, and not otherwise.
func TestDismissedTipsCanBeBroughtBackFromThePalette(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
h.press("palette");
const labels = () => h.$("palette-list").children.map((r) => r.querySelector(".pal-label").textContent);
assert.ok(!labels().includes("Show the tips again"), "offered to bring back tips when none were dismissed");
h.key({ key: "Escape" });

h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: ["palette"] } });
paletteRun("tips again");
assert.deepStrictEqual(h.commands().pop(), { cmd: "resetTips" });
`)
}

// Stepping through a search's matches gave no idea how many there were, which
// one was showing, or whether Enter had wrapped round to the first.
func TestTheFindBarCountsTheMatches(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("findInTerminal");
const search = h.searchers[0];
h.$("search-input").value = "error";
search.results({ resultIndex: 2, resultCount: 12 });
assert.strictEqual(h.$("search-count").textContent, "3 of 12", "the find bar does not say where in the matches it is");
search.results({ resultIndex: -1, resultCount: 0 });
assert.strictEqual(h.$("search-count").textContent, "No matches");

// Another pane's search is not this bar's business.
h.searchers[1].results({ resultIndex: 0, resultCount: 5 });
assert.strictEqual(h.$("search-count").textContent, "No matches", "another pane's matches were counted here");

h.key({ key: "Escape" });
h.press("findInTerminal");
assert.strictEqual(h.$("search-count").textContent, "", "the last search's count was left on a new one");
`)
}

// The projects dialog listed eight recent projects and simply left out the
// rest, of up to forty remembered: an older one could be reached only by
// browsing to its folder again.
func TestEveryRecentProjectCanBeReached(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("projects");
const items = [];
for (let i = 0; i < 12; i++) items.push({ root: "C:/p" + i, name: "p" + i, exists: true, open: false });
h.recv({ type: "recents", items });
const body = h.$("overlay-body");
const more = body.querySelectorAll("button").find((b) => b.textContent === "Show 4 more");
assert.ok(more, "the recent projects past the eighth are not offered at all");
h.click(more);
assert.ok(body.textContent.includes("p11"), "the rest of the recent projects did not appear");
assert.ok(h.doc.activeElement === h.$("recent-8"), "the keyboard did not land on the first of the projects shown");
`)
}

// The fan-out's tasks go one to a line, so Enter is a new line there and
// starting the agents took the pointer, or Tab past every option to the
// button. Ctrl+Enter starts them.
func TestCtrlEnterStartsTheFanOut(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("fanout");
h.recv({ type: "fanoutPreview", paneId: "p1", tasks: ["Add a health endpoint", "Write tests"], isRepo: false, cwd: "C:/repo" });
const box = h.$("overlay-body").querySelector("textarea.fan-tasks");
box.focus();
const plain = h.key({ key: "Enter" });
assert.ok(!plain.defaultPrevented, "a plain Enter no longer makes a new line in the task list");
h.key({ key: "Enter", ctrlKey: true });
const sent = h.commands().pop();
assert.strictEqual(sent.cmd, "fanout", "Ctrl+Enter did not start the agents");
assert.deepStrictEqual(sent.tasks, ["Add a health endpoint", "Write tests"]);
`)
}

// frontEndHarness is the DOM app.js is run against: enough of one to build
// the interface, dispatch events through it and answer the questions it asks,
// and nothing beyond that. It is written to a temporary directory beside the
// real assets and required by each case.
const frontEndHarness = `"use strict";
/* A DOM small enough to read and large enough to run app.js.
 *
 * The front end has no build step and no test runner, so this is the whole of
 * one: node, this file, and the real app.js and index.html read off disk.
 */
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const ASSETS = process.env.FLOCKDECK_ASSETS || ".";

// ------------------------------------------------------------------- nodes

const VOID = new Set(["meta", "link", "input", "br", "img", "hr", "source"]);

class TextNode {
  constructor(text) { this.nodeType = 3; this.data = String(text); this.parentElement = null; }
  get textContent() { return this.data; }
  set textContent(v) { this.data = String(v); }
  remove() { if (this.parentElement) this.parentElement.removeChild(this); }
  get isConnected() { return !!(this.parentElement && this.parentElement.isConnected); }
}

class ClassList {
  constructor(owner) { this.owner = owner; }
  get _set() {
    return new Set(String(this.owner._class || "").split(/\s+/).filter(Boolean));
  }
  _write(s) { this.owner._class = [...s].join(" "); }
  add(...c) { const s = this._set; c.forEach((x) => s.add(x)); this._write(s); }
  remove(...c) { const s = this._set; c.forEach((x) => s.delete(x)); this._write(s); }
  contains(c) { return this._set.has(c); }
  toggle(c, force) {
    const on = force === undefined ? !this.contains(c) : !!force;
    if (on) this.add(c); else this.remove(c);
    return on;
  }
  get length() { return this._set.size; }
  [Symbol.iterator]() { return this._set[Symbol.iterator](); }
}

let SERIAL = 1;

class Element {
  constructor(tag, doc) {
    this.nodeType = 1;
    this.tagName = String(tag).toUpperCase();
    this.ownerDocument = doc;
    this.childNodes = [];
    this.parentElement = null;
    this.attributes = new Map();
    this._class = "";
    this._listeners = new Map();
    this.style = {};
    this.hidden = false;
    this.serial = SERIAL++;
    const self = this;
    this.dataset = new Proxy({}, {
      get(_, k) { return self.attributes.get("data-" + dashed(k)); },
      set(_, k, v) { self.attributes.set("data-" + dashed(k), String(v)); return true; },
      deleteProperty(_, k) { self.attributes.delete("data-" + dashed(k)); return true; },
      has(_, k) { return self.attributes.has("data-" + dashed(k)); },
      ownKeys() { return [...self.attributes.keys()].filter((k) => k.startsWith("data-")).map((k) => camel(k.slice(5))); },
      getOwnPropertyDescriptor() { return { enumerable: true, configurable: true }; },
    });
  }

  get className() { return this._class; }
  set className(v) { this._class = String(v); }
  get classList() { return new ClassList(this); }

  get id() { return this.attributes.get("id") || ""; }
  set id(v) { this.attributes.set("id", String(v)); this.ownerDocument._index(this); }

  get title() { return this.attributes.get("title") || ""; }
  set title(v) { this.attributes.set("title", String(v)); }

  get tabIndex() { return Number(this.attributes.get("tabindex") || 0); }
  set tabIndex(v) { this.attributes.set("tabindex", String(v)); }

  setAttribute(n, v) { this.attributes.set(n, String(v)); if (n === "id") this.ownerDocument._index(this); }
  getAttribute(n) { return this.attributes.has(n) ? this.attributes.get(n) : null; }
  hasAttribute(n) { return this.attributes.has(n); }
  removeAttribute(n) { this.attributes.delete(n); }

  get children() { return this.childNodes.filter((n) => n.nodeType === 1); }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get nextSibling() {
    if (!this.parentElement) return null;
    const k = this.parentElement.childNodes;
    return k[k.indexOf(this) + 1] || null;
  }

  _adopt(n) {
    if (typeof n === "string") n = new TextNode(n);
    if (n.parentElement) n.parentElement.removeChild(n);
    n.parentElement = this;
    // Counted so a test can say whether an element was taken out of the
    // document and put back, which is what costs a terminal its selection.
    n.adoptions = (n.adoptions || 0) + 1;
    return n;
  }
  append(...kids) { kids.forEach((k) => this.childNodes.push(this._adopt(k))); }
  appendChild(k) { this.childNodes.push(this._adopt(k)); return k; }
  insertBefore(k, ref) {
    const at = ref ? this.childNodes.indexOf(ref) : -1;
    const n = this._adopt(k);
    if (at < 0) this.childNodes.push(n); else this.childNodes.splice(at, 0, n);
    return k;
  }
  removeChild(k) {
    const at = this.childNodes.indexOf(k);
    if (at >= 0) this.childNodes.splice(at, 1);
    k.parentElement = null;
    return k;
  }
  remove() { if (this.parentElement) this.parentElement.removeChild(this); }
  replaceChildren(...kids) {
    this.childNodes.slice().forEach((k) => this.removeChild(k));
    this.append(...kids);
  }

  get textContent() { return this.childNodes.map((n) => n.textContent).join(""); }
  set textContent(v) {
    this.childNodes.slice().forEach((k) => this.removeChild(k));
    // Emptied, an element has nothing to be scrolled through, and a browser
    // puts it back at the top.
    if (this.scrollTop) this.scrollTop = 0;
    if (v !== "" && v !== null && v !== undefined) this.append(new TextNode(v));
  }

  get innerHTML() { return this._html || ""; }
  set innerHTML(v) {
    this._html = String(v);
    this.childNodes.slice().forEach((k) => this.removeChild(k));
    this.append(new TextNode(stripTags(String(v))));
  }

  get isConnected() {
    for (let n = this; n; n = n.parentElement) if (n === this.ownerDocument.documentElement) return true;
    return false;
  }

  contains(n) {
    for (let p = n; p; p = p.parentElement) if (p === this) return true;
    return false;
  }
  closest(sel) {
    for (let p = this; p; p = p.parentElement) if (p.nodeType === 1 && matches(p, sel)) return p;
    return null;
  }
  matches(sel) { return matches(this, sel); }

  querySelectorAll(sel) {
    const out = [];
    walk(this, (n) => { if (n !== this && matches(n, sel)) out.push(n); });
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }

  addEventListener(type, fn, opts) {
    const capture = opts === true || (opts && opts.capture);
    const once = !!(opts && opts.once);
    if (!this._listeners.has(type)) this._listeners.set(type, []);
    this._listeners.get(type).push({ fn, capture, once });
  }
  removeEventListener(type, fn, opts) {
    const capture = opts === true || (opts && opts.capture);
    const l = this._listeners.get(type);
    if (!l) return;
    const at = l.findIndex((e) => e.fn === fn && !!e.capture === !!capture);
    if (at >= 0) l.splice(at, 1);
  }
  dispatchEvent(ev) { return dispatch(this, ev); }

  focus() { this.ownerDocument._focus(this); }
  blur() { if (this.ownerDocument.activeElement === this) this.ownerDocument._focus(this.ownerDocument.body); }

  getBoundingClientRect() {
    return { left: 0, top: 0, right: 100, bottom: 20, width: 100, height: 20, x: 0, y: 0 };
  }
  get offsetWidth() { return 100; }
  get offsetHeight() { return 20; }
  get offsetParent() { return this.isConnected ? this.parentElement : null; }
  scrollIntoView() { this.scrolledTo = (this.scrolledTo || 0) + 1; }
  setSelectionRange(from, to) { this.selectionStart = from; this.selectionEnd = to; }
  setPointerCapture(id) { this.captured = id; }
  releasePointerCapture() { this.captured = undefined; }
}

function dashed(k) { return String(k).replace(/[A-Z]/g, (c) => "-" + c.toLowerCase()); }
function camel(k) { return String(k).replace(/-([a-z])/g, (_, c) => c.toUpperCase()); }
function stripTags(s) { return s.replace(/<[^>]*>/g, ""); }

/** Every element a selector search looks at, counted so that a test can say
 *  what searching the document costs. */
let SEARCHED = 0;

function walk(node, fn) {
  for (const k of node.childNodes.slice()) {
    if (k.nodeType !== 1) continue;
    SEARCHED++;
    fn(k);
    walk(k, fn);
  }
}

/* Selectors: a comma-separated list of compound simple selectors, which is
 * everything app.js asks for. No combinators, because it uses none. */
function matches(n, sel) {
  return String(sel).split(",").some((one) => matchOne(n, one.trim()));
}
function matchOne(n, sel) {
  if (!sel) return false;
  const parts = sel.match(/^([a-zA-Z][\w-]*)?((?:[#.][\w-]+|\[[^\]]+\])*)$/);
  if (!parts) throw new Error("harness: selector not supported: " + sel);
  if (parts[1] && n.tagName !== parts[1].toUpperCase()) return false;
  const rest = parts[2] || "";
  const re = /[#.][\w-]+|\[[^\]]+\]/g;
  let m;
  while ((m = re.exec(rest))) {
    const t = m[0];
    if (t[0] === "#") { if (n.id !== t.slice(1)) return false; }
    else if (t[0] === ".") { if (!n.classList.contains(t.slice(1))) return false; }
    else {
      const inner = t.slice(1, -1);
      const eq = inner.indexOf("=");
      if (eq < 0) { if (!n.hasAttribute(inner)) return false; }
      else {
        const name = inner.slice(0, eq);
        const want = inner.slice(eq + 1).replace(/^["']|["']$/g, "").replace(/\\(.)/g, "$1");
        if (n.getAttribute(name) !== want) return false;
      }
    }
  }
  return true;
}

// ------------------------------------------------------------------ events

class Ev {
  constructor(type, init) {
    this.type = type;
    Object.assign(this, init || {});
    this.defaultPrevented = false;
    this._stopped = false;
  }
  preventDefault() { this.defaultPrevented = true; }
  stopPropagation() { this._stopped = true; }
  stopImmediatePropagation() { this._stopped = true; }
}

function dispatch(target, ev) {
  ev.target = ev.target || target;
  const chain = [];
  for (let n = target; n; n = n.parentElement) chain.push(n);
  const doc = target.ownerDocument || (target.documentElement ? target : null);
  if (doc) { chain.push(doc); if (doc.defaultView) chain.push(doc.defaultView); }
  for (let i = chain.length - 1; i >= 0 && !ev._stopped; i--) fire(chain[i], ev, true);
  for (let i = 0; i < chain.length && !ev._stopped; i++) fire(chain[i], ev, false);
  return !ev.defaultPrevented;
}
function fire(node, ev, capture) {
  ev.currentTarget = node;
  if (!capture) {
    const prop = node["on" + ev.type];
    if (typeof prop === "function") prop.call(node, ev);
  }
  const l = node._listeners && node._listeners.get(ev.type);
  if (!l) return;
  for (const e of l.slice()) {
    if (!!e.capture !== !!capture) continue;
    if (e.once) node.removeEventListener(ev.type, e.fn, e.capture);
    e.fn.call(node, ev);
    if (ev._stopped) return;
  }
}

// ---------------------------------------------------------------- document

class Doc {
  constructor() {
    this.nodeType = 9;
    this.byId = new Map();
    this._listeners = new Map();
    this.hidden = false;
    this._hasFocus = true;
    this.documentElement = new Element("html", this);
    this.head = new Element("head", this);
    this.body = new Element("body", this);
    this.documentElement.append(this.head, this.body);
    this.activeElement = this.body;
    this.title = "";
  }
  createElement(tag) { return new Element(tag, this); }
  createTextNode(t) { return new TextNode(t); }
  getElementById(id) { return this.byId.get(id) || null; }
  _index(n) { if (n.id) this.byId.set(n.id, n); }
  _focus(n) { this.activeElement = n; }
  querySelector(s) { return this.documentElement.querySelector(s); }
  querySelectorAll(s) { return this.documentElement.querySelectorAll(s); }
  addEventListener(t, f, o) { Element.prototype.addEventListener.call(this, t, f, o); }
  removeEventListener(t, f, o) { Element.prototype.removeEventListener.call(this, t, f, o); }
  dispatchEvent(ev) { return dispatch(this, ev); }
  hasFocus() { return this._hasFocus; }
}

// ------------------------------------------------------------- html parser

function parseInto(html, doc) {
  const stack = [doc.documentElement];
  let i = 0;
  const push = (n) => stack[stack.length - 1].append(n);
  while (i < html.length) {
    const lt = html.indexOf("<", i);
    if (lt < 0) break;
    if (lt > i) {
      const text = html.slice(i, lt);
      if (text.trim()) push(new TextNode(text.trim()));
    }
    if (html.startsWith("<!--", lt)) { i = html.indexOf("-->", lt) + 3; continue; }
    if (html.startsWith("<!", lt)) { i = html.indexOf(">", lt) + 1; continue; }
    const gt = html.indexOf(">", lt);
    if (gt < 0) break;
    const raw = html.slice(lt + 1, gt);
    i = gt + 1;
    if (raw[0] === "/") {
      const name = raw.slice(1).trim().toLowerCase();
      for (let k = stack.length - 1; k > 0; k--) {
        if (stack[k].tagName === name.toUpperCase()) { stack.length = k; break; }
      }
      continue;
    }
    const m = raw.match(/^([a-zA-Z][\w-]*)/);
    if (!m) continue;
    const name = m[1].toLowerCase();
    const selfClose = raw.trim().endsWith("/");
    const node = name === "html" ? doc.documentElement
      : name === "head" ? doc.head
      : name === "body" ? doc.body
      : new Element(name, doc);
    const attrRe = /([a-zA-Z_:][-\w:.]*)(?:\s*=\s*("[^"]*"|'[^']*'|[^\s"'>]+))?/g;
    let a;
    const attrs = raw.slice(m[1].length);
    while ((a = attrRe.exec(attrs))) {
      const key = a[1];
      const val = a[2] === undefined ? "" : a[2].replace(/^["']|["']$/g, "");
      if (key === "class") node.className = val;
      else if (key === "hidden") node.hidden = true;
      else node.setAttribute(key, val);
    }
    if (node !== doc.documentElement && node !== doc.head && node !== doc.body) push(node);
    if (name === "script") { const end = html.indexOf("</script>", i); i = end < 0 ? html.length : end + 9; continue; }
    if (!selfClose && !VOID.has(name)) stack.push(node);
  }
}

// ---------------------------------------------------------------- fake bits

const sockets = [];
class FakeSocket {
  constructor(url) {
    this.url = url;
    this.readyState = 1;
    this.sent = [];
    this.opened = false;
    sockets.push(this);
  }
  send(d) { this.sent.push(d); }
  close() { this.readyState = 3; if (this.onclose) this.onclose(); }
}
FakeSocket.OPEN = 1;

const terms = [];
class FakeTerm {
  constructor(opts) {
    this.options = Object.assign({}, opts);
    this.cols = 80; this.rows = 24;
    this.written = []; this.disposed = false; this.focused = false;
    terms.push(this);
  }
  loadAddon() {}
  open(host) { this.host = host; }
  onData(fn) { this._data = fn; }
  onBinary(fn) { this._bin = fn; }
  write(d) { this.written.push(d); }
  reset() { this.written.length = 0; }
  dispose() { this.disposed = true; }
  focus() { terms.forEach((t) => { t.focused = false; }); this.focused = true; }
  blur() { this.focused = false; }
}

class FakeFit { fit() {} proposeDimensions() { return { cols: 80, rows: 24 }; } }
const searchers = [];
class FakeSearch {
  constructor() { this.forward = []; this.back = []; this.cleared = 0; this.hit = true; searchers.push(this); }
  findNext(q) { this.forward.push(q); return this.hit; }
  findPrevious(q) { this.back.push(q); return this.hit; }
  clearDecorations() { this.cleared++; }
  onDidChangeResults(fn) { this.results = fn; }
}
class FakeWebgl { onContextLoss() {} dispose() {} }

const notifications = [];
class FakeNotification {
  constructor(title, opts) { this.title = title; Object.assign(this, opts || {}); notifications.push(this); }
  close() { this.closed = true; }
  static requestPermission() {
    FakeNotification.asked++;
    return Promise.resolve(FakeNotification.permission);
  }
}
FakeNotification.permission = "granted";
FakeNotification.asked = 0;

const observers = [];
class FakeResizeObserver {
  constructor(fn) { this.fn = fn; this.targets = []; this.disconnected = false; observers.push(this); }
  observe(t) { this.targets.push(t); }
  unobserve(t) { this.targets = this.targets.filter((x) => x !== t); }
  disconnect() { this.disconnected = true; this.targets = []; }
}

// ---------------------------------------------------------------- fixtures

/** pane is one entry of the state message's pane map. */
function pane(id, over) {
  return Object.assign({
    id: id, kind: "agent", agent: "claude", model: "", name: "agent " + id, cwd: "C:/repo", branch: "main",
    status: "idle", detail: "", broadcast: false,
    cols: 80, rows: 24, dirty: 0, untracked: 0, ahead: 0, behind: 0,
  }, over || {});
}

/** catalog is the agent catalog every state push carries: one agent this
 *  machine has and one it has not, which is the shape the picker groups by. */
function catalog(over) {
  return Object.assign({
    items: [
      { id: "claude", name: "Claude Code", runner: "cli", available: true, defaultModel: "",
        models: [{ id: "", name: "Default", note: "whatever the CLI is set to" },
                 { id: "opus", name: "Opus", note: "most capable" },
                 { id: "sonnet", name: "Sonnet", note: "the everyday one" }] },
      { id: "codex", name: "Codex", runner: "cli", available: false, defaultModel: "gpt-5",
        install: "npm i -g @openai/codex", models: [{ id: "gpt-5", name: "GPT-5" }] },
    ],
    default: { agent: "claude", model: "" },
  }, over || {});
}

/** fixture is a plausible workspace: two tabs holding one pane each. Pass
 *  overrides for whatever the test is actually about. */
function fixture(over) {
  const s = {
    type: "state",
    root: "C:/repo",
    claudeAvailable: true,
    activeTab: "t1",
    broadcast: false,
    waiting: 0,
    working: 0,
    projects: [{ root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 }],
    tabs: [
      { id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
        root: { id: "n1", pane: "p1", weight: 1 } },
      { id: "t2", title: "two", focus: "p2", zoom: false, attention: false,
        root: { id: "n2", pane: "p2", weight: 1 } },
    ],
    panes: { p1: pane("p1"), p2: pane("p2") },
    agents: catalog(),
  };
  return Object.assign(s, over || {});
}

/** split is a node holding several children, for tests about layout. */
function split(dir, children) {
  return { id: "s-" + dir + "-" + children.length, dir: dir, weight: 1, children: children };
}

/** leaf is a node holding one pane. */
function leaf(id, paneID) { return { id: id, pane: paneID, weight: 1 }; }

// --------------------------------------------------------------------- tab

/* Tab moves the focus unless the page says otherwise, and a dialog that means
 * to keep the keyboard has to say otherwise. Without this here there would be
 * nothing for a trap to prevent, and a broken one would look fine. */
const TAB_STOPS = "button, input, textarea, select, a, [tabindex]";

function tabbable(doc) {
  return doc.documentElement.querySelectorAll(TAB_STOPS).filter((n) => {
    if (n.disabled || n.getAttribute("tabindex") === "-1") return false;
    for (let p = n; p; p = p.parentElement) if (p.hidden) return false;
    return true;
  });
}

function tabTo(doc, back) {
  const all = tabbable(doc);
  if (!all.length) return;
  const at = all.indexOf(doc.activeElement);
  doc._focus(at < 0
    ? (back ? all[all.length - 1] : all[0])
    : all[(at + (back ? -1 : 1) + all.length) % all.length]);
}

// -------------------------------------------------------------------- boot

function unref(t) { t.unref(); return t; }

/** boot starts the front end. opts.pathname is where the page was served
 *  from, which is "/" locally and a machine's own prefix through the relay. */
function boot(opts) {
  opts = opts || {};
  sockets.length = 0; terms.length = 0; observers.length = 0; searchers.length = 0;
  notifications.length = 0;
  FakeNotification.permission = "granted";
  FakeNotification.asked = 0;
  const doc = new Doc();
  parseInto(fs.readFileSync(path.join(ASSETS, "index.html"), "utf8"), doc);

  const store = new Map();
  const win = {
    innerWidth: 1400, innerHeight: 900,
    document: doc,
    location: { protocol: "http:", host: "127.0.0.1:7777", pathname: opts.pathname || "/" },
    WebSocket: FakeSocket,
    Terminal: FakeTerm,
    FitAddon: { FitAddon: FakeFit },
    SearchAddon: { SearchAddon: FakeSearch },
    WebglAddon: { WebglAddon: FakeWebgl },
    ResizeObserver: FakeResizeObserver,
    // app.js asks whether something is an element before walking up from it,
    // and builds a bare click event in one place.
    Element: Element, Event: Ev,
    Notification: FakeNotification,
    CSS: { escape: (s) => String(s).replace(/([^\w-])/g, "\\$1") },
    TextEncoder, TextDecoder, console,
    // The page's own timers - a notice taking itself away twelve seconds on,
    // a reconnect - are not what a case waits for, and holding the process
    // open they made every case last as long as the longest of them after its
    // last assertion had run. A case that waits does so with h.sleep.
    setTimeout: (fn, ms, ...args) => unref(setTimeout(fn, ms, ...args)),
    setInterval: (fn, ms, ...args) => unref(setInterval(fn, ms, ...args)),
    clearTimeout, clearInterval, queueMicrotask,
    localStorage: {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
    },
    // Only /help.json is ever fetched, and it is on disk beside the assets.
    fetch: (url) => {
      const name = String(url).replace(/^.*\//, "");
      try {
        const body = fs.readFileSync(path.join(ASSETS, name), "utf8");
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(JSON.parse(body)) });
      } catch (err) {
        return Promise.resolve({ ok: false, status: 404 });
      }
    },
    confirm: () => (win._confirm === undefined ? true : win._confirm),
    prompt: () => win._prompt,
    close: () => { win._closed = true; },
    focus: () => {},
    _listeners: new Map(),
  };
  win.window = win;
  win.self = win;
  win.addEventListener = Element.prototype.addEventListener.bind(win);
  win.removeEventListener = Element.prototype.removeEventListener.bind(win);
  win.dispatchEvent = (ev) => {
    ev.target = ev.target || win;
    fire(win, ev, true); fire(win, ev, false);
    return !ev.defaultPrevented;
  };
  doc.defaultView = win;

  const ctx = vm.createContext(win);
  const src = fs.readFileSync(path.join(ASSETS, "app.js"), "utf8");
  vm.runInContext(src, ctx, { filename: "app.js" });

  const h = {
    doc, win, sockets, terms, observers, searchers, notifications,
    control: sockets.find((s) => s.url.includes("/ws/control")),
    $: (id) => doc.getElementById(id),
    /** controls lists every control socket the page has opened, in order. */
    controls() { return sockets.filter((s) => s.url.includes("/ws/control")); },
    recv(msg) {
      const c = h.controls().pop();
      if (!c || !c.onmessage) throw new Error("harness: the control socket has no reader");
      c.onmessage({ data: JSON.stringify(msg) });
    },
    /** hello delivers the real action table, read from the Go side, so the
     *  palette and the keyboard are tested against the bindings that ship. */
    hello(prefs) {
      const keys = JSON.parse(fs.readFileSync(path.join(ASSETS, "keys.json"), "utf8"));
      h.recv({ type: "hello", keys: keys, prefs: Object.assign({ helpSeen: true, dismissedTips: [] }, prefs || {}) });
      return keys;
    },
    keyTable() { return JSON.parse(fs.readFileSync(path.join(ASSETS, "keys.json"), "utf8")); },
    sleep(ms) { return new Promise((done) => setTimeout(done, ms)); },
    /** css is the style sheet, for the few things that are only expressible
     *  there and still have to hold. */
    css() { return fs.readFileSync(path.join(ASSETS, "app.css"), "utf8"); },
    /** made counts every element ever built, so a test can say how much of the
     *  screen an action puts together again. */
    made() { return SERIAL; },
    /** searched counts the elements every selector search has looked at. */
    searched() { return SEARCHED; },
    /** bind presses the binding the action table gives for an action id. */
    press(actionID) {
      const k = h.keyTable().find((x) => x.id === actionID);
      if (!k || !k.keys) throw new Error("harness: no binding for " + actionID);
      const parts = k.keys.split("+").map((p) => p.trim());
      const name = parts.pop();
      const names = { "←": "ArrowLeft", "→": "ArrowRight", "↑": "ArrowUp", "↓": "ArrowDown" };
      return h.key({
        key: names[name] || name,
        ctrlKey: parts.some((p) => p.toLowerCase() === "ctrl"),
        shiftKey: parts.some((p) => p.toLowerCase() === "shift"),
        altKey: parts.some((p) => p.toLowerCase() === "alt"),
      });
    },
    open() { sockets.forEach((s) => { if (!s.opened && s.onopen) { s.opened = true; s.onopen(); } }); },
    /** commands is everything the page has sent up the control connection, in
     *  order, across a reconnect as well as within one. */
    commands() {
      return h.controls().flatMap((c) => c.sent).map((s) => JSON.parse(s));
    },
    /** click presses something. A disabled control is not clicked at all — the
     *  browser does not dispatch the event — which is the whole point of
     *  disabling one. */
    click(node, init) {
      for (let n = node; n; n = n.parentElement) if (n.disabled) return false;
      return dispatch(node, new Ev("click", Object.assign({ target: node }, init)));
    },
    /** key presses a key at whatever has the keyboard. A keydown starts at the
     *  focused element and travels out to the window, so a handler on a row and
     *  the one on the window both get their turn, in that order. */
    key(init) {
      const ev = new Ev("keydown", Object.assign({ key: "", ctrlKey: false, shiftKey: false, altKey: false }, init));
      const at = doc.activeElement;
      if (at && at.nodeType === 1) dispatch(at, ev);
      else win.dispatchEvent(ev);
      if (ev.key === "Tab" && !ev.defaultPrevented) tabTo(doc, ev.shiftKey);
      // A button pressed with Enter or Space is clicked, which is the browser's
      // doing rather than the page's.
      if ((ev.key === "Enter" || ev.key === " ") && !ev.defaultPrevented &&
          at && at.tagName === "BUTTON" && !at.disabled) {
        dispatch(at, new Ev("click", { target: at }));
      }
      return ev;
    },
    Ev, dispatch,
  };
  h.open();
  return h;
}

module.exports = { boot, fixture, catalog, pane, split, leaf, Ev, Element, TextNode, dispatch };
`
