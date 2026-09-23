package webui

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// frontEndDeadline bounds one front-end case run under node.
const frontEndDeadline = 2 * time.Minute

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

// A list of elements from the page is not an array. querySelectorAll gives a
// NodeList, and children and getElementsBy* an HTMLCollection, and neither has
// some, filter, map or find. The harness hands back plain arrays, so a call
// like that passes every front-end test and throws in the window: that is how
// the conversations dialog shipped unable to draw its list, and so with no
// conversation that could be resumed. A list is spread into an array first,
// as [...list], wherever an array's methods are wanted of it.
func TestNoElementListIsTreatedAsAnArray(t *testing.T) {
	app := readAsset(t, "app.js")
	comment := regexp.MustCompile(`^\s*(//|\*|/\*)`)
	lists := regexp.MustCompile(`\b(querySelectorAll|getElementsByClassName|getElementsByTagName|getElementsByTagNameNS|getElementsByName)\s*\(|\.(children|childNodes)\b`)
	// What an array has and a NodeList or an HTMLCollection does not. The
	// chain may go on to the next line.
	arrayOnly := regexp.MustCompile(`^\s*\??\.\s*(some|filter|map|find|findIndex|findLast|findLastIndex|every|reduce|reduceRight|includes|indexOf|lastIndexOf|flatMap|flat|at|slice|concat|join)\s*\(`)

	lines := strings.Split(app, "\n")
	for _, m := range lists.FindAllStringIndex(app, -1) {
		line := strings.Count(app[:m[0]], "\n")
		if comment.MatchString(lines[line]) {
			continue
		}
		end := m[1]
		if app[end-1] == '(' {
			end = closingParen(app, end-1)
			if end < 0 {
				t.Fatalf("app.js:%d: the call's parenthesis is never closed", line+1)
			}
		}
		if call := arrayOnly.FindStringSubmatch(app[end:]); call != nil {
			t.Errorf("app.js:%d calls %s on a list of elements, which a browser's NodeList and HTMLCollection do not have; spread it into an array first:\n\t%s",
				line+1, call[1], strings.TrimSpace(lines[line]))
		}
	}
}

// closingParen returns the index just past the parenthesis that closes the
// one at open, stepping over quoted strings, or -1 if it is never closed.
func closingParen(src string, open int) int {
	depth := 0
	var quote byte
	for i := open; i < len(src); i++ {
		c := src[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
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
	runFrontEnd(t, relayPromise+`
h.hello();
h.recv(fixture());
const chip = h.$("btn-remote");
const badge = chip.querySelector(".rail-badge");
const badged = () => ["on", "pending", "trouble"].filter((c) => badge.classList.contains(c));
// Found before it is turned on: the button stays in the rail, with no badge.
assert.ok(!chip.hidden, "the button is hidden on a machine that is not enrolled, so remote access cannot be found");
assert.deepStrictEqual(badged(), [], "remote access that is off wears a badge");
assert.ok((chip.dataset.tip || "").includes("off"), "the button does not say remote access is off: " + chip.dataset.tip);

const remote = (over) => Object.assign({ state: "connected", relay: "https://relay.example",
  hostId: "h1", viewers: 2, since: "2030-01-01T00:00:00Z" }, over || {});
h.recv(fixture({ remote: remote({ state: "connecting" }) }));
assert.deepStrictEqual(badged(), ["pending"], "a tunnel still connecting is not grey");
h.recv(fixture({ remote: remote() }));
assert.ok(!chip.hidden, "the chip shows once the machine is enrolled");
assert.strictEqual(chip.textContent, "Remote · 2");
assert.deepStrictEqual(badged(), ["on"], "a working tunnel is not green");
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
assert.ok(body.textContent.includes("Coming soon, for companies: run the relay on your own infrastructure, with SSO and support."),
  "an enrolled machine is not told Enterprise is coming: " + body.textContent);
assert.ok(!relayPromise(body.textContent), "an enrolled machine is promised what the shared relay will cost: " + relayPromise(body.textContent));
assert.ok(!/private relay/i.test(body.textContent), "an enrolled machine is still told of private relays");

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
assert.deepStrictEqual(badged(), ["trouble"], "a tunnel that cannot connect has no amber badge");
assert.ok(h.$("overlay-body").textContent.includes("lost the relay"), "the dialog's status line followed the state");
`)
}

// A tunnel waiting out its backoff says when it will try the relay again, as
// a clock time, so that "trying again shortly" is not left to sit however
// long the wait actually is; once that moment has passed it says "shortly"
// rather than naming a time already gone by.
func TestRemoteAccessShowsWhenItWillRetry(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const clockOf = (iso) => {
  const d = new Date(iso);
  const two = (n) => String(n).padStart(2, "0");
  return two(d.getHours()) + ":" + two(d.getMinutes()) + ":" + two(d.getSeconds());
};
const retryAt = new Date(Date.now() + 45000).toISOString();
h.recv(fixture({ remote: { state: "error", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z", detail: "lost the relay", retryAt } }));
h.click(h.$("btn-remote"));
h.recv({ type: "remoteDevices", enabled: true, devices: [], hosts: [{ id: "h1", name: "desk", online: false, self: true }] });
const body = h.$("overlay-body");
assert.ok(body.textContent.includes("Trying again at " + clockOf(retryAt) + "."),
  "the retry clock is not shown: " + body.textContent);

const past = new Date(Date.now() - 1000).toISOString();
h.recv(fixture({ remote: { state: "error", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z", detail: "lost the relay", retryAt: past } }));
assert.ok(body.textContent.includes("Trying again shortly."), "a retry already due is shown as a time already past: " + body.textContent);

h.recv(fixture({ remote: { state: "error", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z", detail: "lost the relay" } }));
assert.ok(!body.textContent.includes("Trying again"), "nothing to retry is not shown as something to wait on: " + body.textContent);

const reconnecting = new Date(Date.now() + 5000).toISOString();
h.recv(fixture({ remote: { state: "connecting", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z", retryAt: reconnecting } }));
assert.ok(body.textContent.includes("Reconnecting to relay.example."), "a short retry mid-backoff still says connecting, not dialling: " + body.textContent);
assert.ok(body.textContent.includes("Trying again at " + clockOf(reconnecting) + "."), "the retry clock is not shown while connecting: " + body.textContent);

h.recv(fixture({ remote: { state: "connecting", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z" } }));
assert.ok(body.textContent.includes("Connecting to relay.example…"), "a tunnel actively dialling is not said to be connecting: " + body.textContent);
`)
}

// A pairing link left on screen is not left stale: the roster is asked again
// every few seconds while the link is still good to scan, so a device paired
// while nobody touched the dialog shows up without it being closed and
// reopened. The asking stops once the dialog closes.
func TestRemoteAccessPollsWhileAPairingLinkIsShown(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ remote: { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z" } }));
h.click(h.$("btn-remote"));
h.recv({ type: "remoteDevices", enabled: true, devices: [], hosts: [{ id: "h1", name: "desk", online: true, self: true }] });

h.click(h.$("remote-pair"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remotePair", kind: "device" });
h.recv({ type: "remotePair", kind: "device", code: "fdp_c", url: "https://relay.example/pair#fdp_c",
  expiresAt: new Date(Date.now() + 600000).toISOString() });

await h.sleep(4300);
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteDevices" }, "the roster was not asked again while the link sat on screen");
h.recv({ type: "remoteDevices", enabled: true, devices: [{ id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" }],
  hosts: [{ id: "h1", name: "desk", online: true, self: true }] });
assert.ok(h.$("overlay-body").textContent.includes("phone"), "the device paired mid-wait is not shown without reopening the dialog");

// Closing the dialog ends the asking.
h.click(h.$("overlay-close"));
const before = h.commands().length;
await h.sleep(4300);
assert.strictEqual(h.commands().length, before, "the roster went on being asked after the dialog closed");
`)
}

// relayPromise finds a sentence that speaks of the relay or remote access and
// promises what it will cost from now on, which nothing may say while that is
// not settled. A sentence about the app alone is left be.
const relayPromise = `
const relayPromise = (text) => text.split(/[.!?]/).find((s) => /relay|remote/i.test(s) &&
  /\b(stays?|remains?|always( be)?) free\b|\bfree (forever|for ever|for good|for life)\b|never commits you to paying/i.test(s));
`

// This machine and each paired device are renamed from the dialog, starting
// from the name each has; a cancelled or unchanged answer sends nothing.
// Another machine is renamed only by itself or a paired device, so it is
// offered no rename here, and one that is offline is said to be removed from
// a paired device.
func TestRemoteAccessRenames(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ remote: { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z" } }));
h.click(h.$("btn-remote"));
h.recv({ type: "remoteDevices", enabled: true,
  devices: [{ id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" }],
  hosts: [{ id: "h1", name: "desk", online: true, self: true },
    { id: "h2", name: "old laptop", online: false, lastSeen: "2029-06-01T00:00:00Z" }] });
const body = h.$("overlay-body");
const renames = Array.from(body.querySelectorAll("button")).filter((b) => b.textContent === "Rename");
assert.strictEqual(renames.length, 2, "the device and this machine are each offered a rename, and only they are");

h.win._prompt = "  Work PC ";
h.click(h.$("remote-rename"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteRename", kind: "host", name: "Work PC" });
h.win._prompt = "Sam's phone";
h.click(renames[0]);
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteRename", kind: "device", id: "d1", name: "Sam's phone" });

const before = h.commands().length;
h.win._prompt = null;
h.click(renames[0]);
h.win._prompt = "phone";
h.click(renames[0]);
assert.strictEqual(h.commands().length, before, "a cancelled or unchanged rename was sent");
assert.ok(body.textContent.includes("Devices page of a paired device"), "an offline machine is not said to be removed from a paired device");
`)
}

// Remote access is turned on and off from the dialog as well as a terminal:
// "try again" for a tunnel that is not up, turning it off only once asked, a
// relay that could not be told offered again before it is forgotten anyway,
// and a machine that is not enrolled given a form whose answer is shown under
// it, with what was typed still there.
func TestRemoteAccessCanBeTurnedOnAndOff(t *testing.T) {
	runFrontEnd(t, relayPromise+`
h.hello();
const remote = { state: "error", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z", detail: "no route to host" };
h.recv(fixture({ remote }));
h.click(h.$("btn-remote"));
h.recv({ type: "remoteDevices", enabled: true,
  devices: [{ id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" }],
  hosts: [{ id: "h1", name: "desk", online: false, self: true }] });

h.click(h.$("remote-retry"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteReconnect" });
h.recv({ type: "remoteOutcome", action: "reconnect" });

const before = h.commands().length;
h.win._confirm = false;
h.click(h.$("remote-disable"));
assert.strictEqual(h.commands().length, before, "remote access was turned off without asking");
h.win._confirm = true;
h.click(h.$("remote-disable"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteDisable", force: false });
h.recv({ type: "remoteOutcome", action: "disable", untold: true, error: "could not reach the relay" });
assert.ok(h.$("overlay-body").textContent.includes("could not reach the relay"), "why it was not turned off is shown");
h.click(h.$("remote-forget"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteDisable", force: true });
h.recv({ type: "remoteOutcome", action: "disable", warning: "The relay could not be told, so it will go on listing this machine" });

h.recv(fixture());
h.recv({ type: "remoteDevices", enabled: false, devices: [], hosts: [] });
const relay = h.$("remote-relay");
assert.ok(relay, "a machine that is not enrolled is offered the form, not a command to type");
assert.ok(h.$("overlay-body").textContent.includes("go on listing this machine"), "a relay left untold is still said");
assert.ok(h.$("overlay-body").textContent.includes("Coming soon, for companies"), "a machine not enrolled is not told Enterprise is coming");
assert.ok(!relayPromise(h.$("overlay-body").textContent), "a machine not enrolled is promised what the shared relay will cost");
assert.ok(!/private relay/i.test(h.$("overlay-body").textContent), "a machine not enrolled is still told of private relays");
relay.value = "relay.example";
h.$("remote-name").value = "desk";
h.click(h.$("remote-enable"));
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "remoteEnable", relay: "relay.example", name: "desk", join: "", invite: "" });
assert.ok(h.$("remote-enable").disabled, "the button waits for the answer rather than asking twice");
h.recv({ type: "remoteOutcome", action: "enable", error: "the relay's own address is https://relay.example" });
assert.ok(h.$("overlay-body").textContent.includes("the relay's own address"), "the refusal is shown under the form");
assert.strictEqual(h.$("remote-relay").value, "relay.example", "what was typed survives the answer");
assert.ok(!h.$("remote-enable").disabled, "the form can be sent again");
`)
}

// A machine is moved to another relay from the dialog as well as a terminal:
// the form is folded away until asked for, says before its button that every
// device will pair again, asks again before sending, keeps what was typed
// across a refusal, and folds away once the move has gone ahead.
func TestRemoteAccessCanMoveToAnotherRelay(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const remote = { state: "connected", relay: "https://remote.flockdeck.ai", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z" };
h.recv(fixture({ remote }));
h.click(h.$("btn-remote"));
h.recv({ type: "remoteDevices", enabled: true,
  devices: [{ id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" }],
  hosts: [{ id: "h1", name: "desk", online: true, self: true }] });

assert.ok(!h.$("remote-move-relay"), "the move form is shown before it is asked for");
h.click(h.$("remote-move"));
assert.ok(h.$("remote-move-relay"), "asking to move does not show the form");
assert.ok(h.$("overlay-body").textContent.includes("have to pair again"), "what moving costs is not said before the button");

h.click(h.$("remote-move-go"));
assert.ok(h.$("overlay-body").textContent.includes("Name the relay"), "a move with no relay is not told why it went nowhere");

h.$("remote-move-relay").value = "relay.company.example";
h.$("remote-move-invite").value = "fdi_abc";
const before = h.commands().length;
h.win._confirm = false;
h.click(h.$("remote-move-go"));
assert.strictEqual(h.commands().length, before, "the machine was moved without asking");
assert.ok(/pair again/.test(h.win._confirmed), "the question does not say every device pairs again: " + h.win._confirmed);
assert.ok(/only machine/.test(h.win._confirmed), "the question does not say the account on the old relay goes: " + h.win._confirmed);
h.win._confirm = true;
h.click(h.$("remote-move-go"));
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "remoteMove", relay: "relay.company.example", invite: "fdi_abc", join: "" });
assert.ok(h.$("remote-move-go").disabled, "the button waits for the answer rather than asking twice");

h.recv({ type: "remoteOutcome", action: "move", error: "the relay said: this relay needs an invite code" });
assert.ok(h.$("overlay-body").textContent.includes("needs an invite code"), "the refusal is not shown");
assert.strictEqual(h.$("remote-move-relay").value, "relay.company.example", "what was typed is lost with the answer");
assert.ok(!h.$("remote-move-go").disabled, "the form cannot be sent again");

h.recv({ type: "remoteOutcome", action: "move", warning: "The old relay could not be told, so it will go on listing this machine" });
// The harness keeps every id it has seen, so the button, drawn afresh, is
// what says whether the form is still open.
assert.strictEqual(h.$("remote-move").textContent, "Move to another relay…", "a move that went ahead leaves its form open");
assert.ok(h.$("overlay-body").textContent.includes("old relay could not be told"), "an old relay left untold is not said");
`)
}

// runFrontEnd boots app.js against the harness and runs body against it. The
// body is ordinary node: assert is in scope, and h is the booted front end.
func runFrontEnd(t *testing.T, body string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		// Node is optional on a developer's machine, but CI is where these tests
		// are meant to run, and a skip there passes every one of them unrun.
		if os.Getenv("CI") != "" {
			t.Fatal("node is not on PATH, and CI is set: the front-end behaviour tests cannot run where they are meant to")
		}
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
	write("e2e.js", readAsset(t, "e2e.js"))
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

	// A case that never settles -- a timer that keeps rearming, a promise
	// nothing resolves -- leaves node running for ever, and the whole package
	// then stops at go test's own timeout with nothing said about which case
	// it was. A case takes a second or two; this names the one that did not.
	ctx, cancel := context.WithTimeout(context.Background(), frontEndDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join(dir, "case.js"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FLOCKDECK_ASSETS="+dir)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the front end did not finish this case within %v:\n%s", frontEndDeadline, out)
	}
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
const secondButton = second.querySelector("button.tab-btn");
secondButton.focus();
assert.ok(h.doc.activeElement === secondButton, "the tab button has the keyboard");

// An agent starts working. Nothing about the tabs changed.
h.recv(fixture({ working: 1, panes: { p1: pane("p1", { status: "working" }), p2: pane("p2") } }));

assert.ok(h.$("tabs").children[0] === first, "the first tab button was replaced");
assert.ok(h.$("tabs").children[1] === second, "the second tab button was replaced");
assert.ok(h.doc.activeElement === secondButton, "the push took the keyboard off the tab button");
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
assert.strictEqual(two.querySelector("button.tab-btn").getAttribute("aria-selected"), "true");
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

// The tally of who is waiting and who is working is rebuilt only when the
// counts move: a status push arrives every time any agent moves. It is not a
// live region any more - that read the counts out on every change of them,
// and never said which agent; announceStatus says what matters instead.
func TestTheSummaryIsOnlySpokenWhenItChanges(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
const busy = { p1: pane("p1", { status: "waiting" }), p2: pane("p2", { status: "working" }) };
h.recv(fixture({ waiting: 1, working: 1, panes: busy }));

const box = h.$("summary");
assert.strictEqual(box.getAttribute("aria-live"), null, "the summary reads its counts out on every change of them");
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

// The mark at the top of the rail is the application's own icon, drawn twice
// more: once for the taskbar (the favicon links) and once here, inline in the
// page, so that whoever is waiting on you shows even to somebody who never
// looks at the taskbar because the window already has the keyboard.
func TestTheRailMarkFollowsStatus(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
const mark = h.$("rail-mark");
assert.ok(mark.getAttribute("src").includes("icon.svg"), "starts on the mark the page shipped with: " + mark.getAttribute("src"));

h.recv(fixture({ waiting: 1, working: 0, panes: { p1: pane("p1", { status: "waiting" }) } }));
assert.ok(mark.getAttribute("src").includes("icon-waiting.svg"), "an agent waiting on you: " + mark.getAttribute("src"));

h.recv(fixture({ waiting: 0, working: 1, panes: { p1: pane("p1", { status: "working" }) } }));
assert.ok(mark.getAttribute("src").includes("icon.svg") && !mark.getAttribute("src").includes("icon-waiting"),
  "an agent working: " + mark.getAttribute("src"));

h.recv(fixture({ waiting: 0, working: 0 }));
assert.ok(mark.getAttribute("src").includes("icon-idle.svg"), "nothing under way: " + mark.getAttribute("src"));
`)
	t.Log(strings.TrimSpace(out))
}

// The tally in the top bar was a live region, so every agent that started or
// stopped working had the counts read out, and none of it said which agent.
// What a screen reader is told now is the news that needs a person, by name:
// an agent that has started waiting on you, or one whose process has exited.
func TestAnAgentThatStartsWaitingIsAnnouncedByName(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const region = h.$("announcer");
const said = () => region.textContent;
assert.strictEqual(region.getAttribute("role"), "status", "there is no status region to announce anything in");
assert.strictEqual(said(), "", "something was announced before anything happened");

// Working and idle are not news.
h.recv(fixture({ working: 1, panes: { p1: pane("p1", { status: "working" }), p2: pane("p2") } }));
h.recv(fixture({ panes: { p1: pane("p1"), p2: pane("p2") } }));
assert.strictEqual(said(), "", "an agent starting and stopping work was announced: " + said());

const waiting = { status: "waiting", name: "fix the parser" };
h.recv(fixture({ waiting: 1, panes: { p1: pane("p1", waiting), p2: pane("p2") } }));
assert.ok(said().endsWith("fix the parser is waiting on you"), "the agent that stopped to wait was not named: " + said());

// Still waiting is not news again.
const lines = region.children.length;
h.recv(fixture({ waiting: 1, panes: { p1: pane("p1", Object.assign({ detail: "asking again" }, waiting)), p2: pane("p2") } }));
assert.strictEqual(region.children.length, lines, "an agent still waiting was announced again");

h.recv(fixture({ panes: { p1: pane("p1", { status: "exited", name: "fix the parser" }), p2: pane("p2") } }));
assert.ok(said().endsWith("fix the parser has exited"), "the agent whose process exited was not named: " + said());
`)
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
// The tab strip has a size watcher of its own; these are the panes'. Picked
// out now, while each still watches something.
const watchers = h.observers.filter((o) => !o.targets.includes(h.$("tabs")));
assert.strictEqual(watchers.length, 4, "one size watcher per pane");
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
  assert.ok(watchers[i].disconnected,
    "the size watcher of pane " + i + " is still registered, and holds the pane through its callback");
  assert.strictEqual(h.sockets.filter((s) => s.url.includes("id=p" + i))[0].readyState, 3,
    "the stream of pane " + i + " is still open");
}
// The one still running kept all of it.
assert.ok(!h.terms[0].disposed, "the surviving pane lost its terminal");
assert.ok(!watchers[0].disconnected, "the surviving pane lost its size watcher");
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
assert.ok(pages[0].classList.contains("tab-page-off") && !pages[1].classList.contains("tab-page-off"),
  "the wrong page is on screen");
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

// A tab not on screen is taken out with a class, not the hidden attribute:
// [hidden] drops it out of layout entirely, and every terminal in it loses
// its character-size measurement along with it, paid for again the moment it
// is shown -- a cost real enough to profile, not just to guess at. The class
// is what app.css hides it by instead, keeping the layout (and with it the
// measurement) intact underneath.
func TestATabNotOnScreenKeepsItsLayout(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const one = { id: "t1", title: "one", focus: "p1", root: leaf("n1", "p1") };
const two = { id: "t2", title: "two", focus: "p2", root: leaf("n2", "p2") };
h.recv(fixture({ activeTab: "t1", tabs: [one, two], panes: { p1: pane("p1"), p2: pane("p2") } }));

const pages = h.doc.querySelectorAll("div.tab-page");
assert.strictEqual(pages.length, 2);
assert.ok(!pages[0].hidden && !pages[1].hidden,
  "a tab page still uses the hidden attribute, which drops its layout");
assert.ok(!pages[0].classList.contains("tab-page-off") && pages[1].classList.contains("tab-page-off"),
  "the tab not on screen does not carry the class that hides it");

h.recv(fixture({ activeTab: "t2", tabs: [one, two], panes: { p1: pane("p1"), p2: pane("p2") } }));
assert.ok(pages[0].classList.contains("tab-page-off") && !pages[1].classList.contains("tab-page-off"),
  "switching tabs did not move the class to the one now off screen");
`)
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

// Space works the same way, and does not scroll the dialog instead. A diff
// asked for hard on the heels of another waits out a short gap.
await h.sleep(200);
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
const btns = bar.children.map((n) => n.querySelector("button.tab-btn"));
assert.strictEqual(btns.length, 10);

// One stop for the strip, not two per tab.
assert.strictEqual(btns[0].tabIndex, 0, "the current tab is not the stop");
assert.ok(btns.slice(1).every((b) => b.tabIndex === -1),
  "every tab is a stop on the way through the window");
assert.ok(btns.slice(1).every((b) => b.parentElement.querySelector(".close").tabIndex === -1),
  "every close button is a stop too");

// Tab from the button before the strip reaches it once and leaves it once.
h.$("rail-toggle").focus();
h.key({ key: "Tab" });
assert.ok(h.doc.activeElement === btns[0], "Tab did not reach the current tab");
h.key({ key: "Tab" });
assert.ok(h.doc.activeElement === btns[0].parentElement.querySelector(".close"), "its close button follows it");
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

// A tab is draggable, so an ordinary click on it -- a mousedown and a mouseup
// with a pixel or two of drift between them, which any real pointer has -- is
// read by the browser as the start of a drag, and the click that would
// otherwise have switched to it never fires at all. A drag that ends without
// reordering, merging or moving anything is answered as the click it was: it
// selects the tab it began on.
func TestADragThatReorderedNothingSelectsTheTabInstead(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const dt = () => ({ effectAllowed: null, setData: () => {} });
const item1 = h.$("tab-t1").parentElement;
const item2 = h.$("tab-t2").parentElement;

// Pressed, drifted a couple of pixels, and released back over itself --
// nothing to reorder or merge -- is switched to all the same.
h.dispatch(item2, new h.Ev("dragstart", { dataTransfer: dt() }));
h.dispatch(item2, new h.Ev("dragend", {}));
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectTab", id: "t2" },
  "a drag that went nowhere did not select the tab it began on");

// Already the active tab: nothing to send for it.
h.recv(fixture({ activeTab: "t2" }));
const before = h.commands().length;
h.dispatch(item2, new h.Ev("dragstart", { dataTransfer: dt() }));
h.dispatch(item2, new h.Ev("dragend", {}));
assert.strictEqual(h.commands().length, before, "the already-active tab sent a command for nothing");

// A drag that actually reordered a tab is the real thing, not a swallowed
// click, and is not also read as one.
h.recv(fixture({ activeTab: "t1" }));
const sinceReorder = h.commands().length;
h.dispatch(item1, new h.Ev("dragstart", { dataTransfer: dt() }));
h.dispatch(item2, new h.Ev("dragover", { dataTransfer: dt(), clientX: 0 }));
h.dispatch(item2, new h.Ev("drop", { dataTransfer: dt(), clientX: 0 }));
h.dispatch(item1, new h.Ev("dragend", {}));
const sent = h.commands().slice(sinceReorder);
assert.deepStrictEqual(sent.pop(), { cmd: "moveTab", id: "t1", target: "t2" },
  "the reorder itself was not sent");
assert.ok(!sent.some((c) => c.cmd === "selectTab"),
  "a drag that reordered a tab also sent a redundant selectTab");
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
assert.strictEqual(bars[0].children.length, 7, "seven things to do with a pane");

// The arrows walk the row and take the stop with them.
const buttons = bars[0].children;
buttons[0].focus();
h.key({ key: "ArrowRight" });
assert.ok(h.doc.activeElement === buttons[1], "the right arrow did not move along the row");
assert.strictEqual(buttons[1].tabIndex, 0, "the stop did not move with the focus");
assert.strictEqual(buttons[0].tabIndex, -1);
h.key({ key: "End" });
assert.ok(h.doc.activeElement === buttons[6], "End did not go to the last button");
h.key({ key: "ArrowRight" });
assert.ok(h.doc.activeElement === buttons[0], "the row does not wrap");

// Coming back returns to the button last used, not to the start.
h.key({ key: "End" });
h.doc.body.focus();
h.key({ key: "Tab" });
h.key({ key: "Tab" });
assert.strictEqual(stops(bars[0]), 1, "the row grew a second stop");
assert.strictEqual(buttons[6].tabIndex, 0, "the stop did not stay where it was left");

// And they still do what they say.
buttons[6].focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "closePane", id: "p0" });
buttons[5].focus();
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
h.click(h.$("rail-open"));
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
    tabs: [{ id: "t9", title: "fan out", focus: "q0", delegated: true, root: split("h", kids) }],
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
// They are one job (see jobOf), so the body reads as one sentence about the
// fan-out rather than each task named on its own.
assert.strictEqual(all.body, "fan out: 3 want your input", "got: " + all.body);

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

// A settled tab only rolls up into a summary card, and only groups its
// prompts into one notification, when the server says a fan-out actually
// filled it (tab.delegated) -- never merely because it happens to hold two
// or more idle agent panes. A person is free to build exactly that shape by
// hand with an ordinary split, and it must go on showing its real terminals
// and naming each agent on its own, the way it always has.
func TestAnOrdinaryMultiPaneTabIsNeverMistakenForAFanOut(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }] }));
assert.strictEqual(h.terms.length, 2, "an ordinary two-agent split was rolled up into a summary card");
assert.ok(!h.$("workspace").querySelector(".summary-card"), "a card was drawn for a tab no fan-out filled");

h.doc._hasFocus = false;
h.recv(fixture({ tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  panes: { p1: pane("p1", { name: "one", status: "waiting" }), p2: pane("p2", { name: "two", status: "waiting" }) },
  waiting: 2 }));
assert.strictEqual(h.notifications.length, 1, "two agents stopping together sent more than one notification");
assert.strictEqual(h.notifications[0].title, "2 agents need you", "got: " + h.notifications[0].title);
assert.ok(!/^one: /.test(h.notifications[0].body), "an ordinary split's prompts were grouped under the tab's own name: " + h.notifications[0].body);
`)
}

// Dismissing a settled fan-out's summary card with "Back to grid" used to be
// permanent: cardHidden suppressed the card until the tab started working
// again, and nothing offered a way back to it before then. A chip floated
// over the grid brings it back, for as long as the tab stays settled.
func TestADismissedSummaryCardCanBeShownAgain(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const settled = () => fixture({
  activeTab: "t9",
  tabs: [{ id: "t9", title: "fan out", focus: "q0", delegated: true,
    root: split("h", [leaf("m0", "q0"), leaf("m1", "q1")]) }],
  panes: { q0: pane("q0", { name: "task 0", branch: "task-0" }),
           q1: pane("q1", { name: "task 1", branch: "task-1", status: "waiting" }) },
  waiting: 1,
});
h.recv(settled());
assert.ok(h.$("workspace").querySelector(".summary-card"), "the settled fan-out did not collapse to a card");
assert.ok(!h.$("workspace").querySelector(".show-summary"), "the chip is showing over the card itself");

// Dismissed: the grid comes back, with the chip floated over it.
const back = [...h.$("workspace").querySelectorAll("button")].filter((b) => b.textContent === "Back to grid")[0];
assert.ok(back, "no way to dismiss the card in the first place");
h.click(back);
h.recv(settled());
assert.ok(!h.$("workspace").querySelector(".summary-card"), "the card is still showing after it was dismissed");
const chip = h.$("workspace").querySelector(".show-summary");
assert.ok(chip, "nothing offers a way back to the dismissed card");
assert.strictEqual(chip.textContent, "Show summary");

// Pressing it brings the card back.
h.click(chip);
h.recv(settled());
assert.ok(h.$("workspace").querySelector(".summary-card"), "the chip did not bring the card back");
assert.ok(!h.$("workspace").querySelector(".show-summary"), "the chip is still showing once the card is back");

// The tab starting work again clears the dismissal, the same as it always
// has, so the chip does not survive to show a card that is gone.
h.recv(settled());
const back2 = [...h.$("workspace").querySelectorAll("button")].filter((b) => b.textContent === "Back to grid")[0];
h.click(back2);
h.recv(fixture({
  activeTab: "t9",
  tabs: [{ id: "t9", title: "fan out", focus: "q0", delegated: true,
    root: split("h", [leaf("m0", "q0"), leaf("m1", "q1")]) }],
  panes: { q0: pane("q0", { name: "task 0", status: "working" }), q1: pane("q1", { name: "task 1" }) },
  working: 1,
}));
assert.ok(!h.$("workspace").querySelector(".show-summary"), "the chip survived the tab going back to work");
`)
}

// The history panel draws a closed job the same one-line-per-pane way the
// live summary card does, from whatever the Go side has kept for this
// project in memory.
func TestFanOutHistoryListsPastJobs(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-fanout-history"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "fanoutHistory", root: "C:/repo" });
assert.ok(h.$("overlay-body").textContent.includes("Reading past jobs"));

h.recv({ type: "fanoutHistory", root: "C:/repo", items: [] });
assert.ok(h.$("overlay-body").textContent.includes("No fan-out jobs finished in this project yet"));

h.recv({
  type: "fanoutHistory", root: "C:/repo",
  items: [{
    id: "j1", title: "Fan out", at: "2024-01-01T00:00:00Z", ago: "3 hours ago",
    panes: [
      { name: "task 0", branch: "task-0", task: "first task", kind: "done", detail: "wrapped it up" },
      { name: "task 1", branch: "task-1", task: "second task", kind: "needs", detail: "Wants a question." },
    ],
  }],
});
const body = h.$("overlay-body");
assert.ok(body.textContent.includes("Fan out"), "the job's title is missing");
assert.ok(body.textContent.includes("3 hours ago"), "when the job finished is missing");
assert.ok(body.textContent.includes("2 agents"), "the pane count is missing");

const rows = [...body.querySelectorAll(".summary-card-row")];
assert.strictEqual(rows.length, 2, "want one row per pane");
assert.ok(rows[0].textContent.includes("task 0") && rows[0].textContent.includes("task-0"));
assert.ok(rows[0].textContent.includes("first task"), "the task that was fanned out is missing");
assert.ok(rows[0].textContent.includes("wrapped it up"), "the agent's last reply is missing");
assert.strictEqual(rows[0].querySelector(".summary-card-outcome").textContent, "done");
assert.ok(rows[0].querySelector(".summary-card-outcome").classList.contains("done"));
assert.strictEqual(rows[1].querySelector(".summary-card-outcome").textContent, "needs input");
assert.ok(rows[1].querySelector(".summary-card-outcome").classList.contains("needs"));
assert.ok(rows[1].textContent.includes("Wants a question."));

// Refresh asks again, for this project.
const refresh = [...body.querySelectorAll("button")].filter((b) => b.textContent === "Refresh")[0];
assert.ok(refresh, "no way to ask again");
h.click(refresh);
assert.deepStrictEqual(h.commands().pop(), { cmd: "fanoutHistory", root: "C:/repo" });
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
  { cmd: "commit", path: "C:/repo", text: "webui: a change", push: false, files: ["app.js"], omitted: 0 });
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

h.click(h.$("rail-open"));
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
h.click(h.$("rail-open"));
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

// The multi-repo grouping commands (groupProjects, addRepoToGroup,
// removeRepoFromGroup, renameGroup) existed on the server with nothing in
// the dialog reaching them. This covers all four, end to end through the
// picker rather than the control socket directly.
func TestGroupingProjectsFromTheDialog(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/api", name: "api", active: true, tabs: 1, waiting: 0, working: 0, members: [{ root: "C:/api", name: "api" }] },
  { root: "C:/ui", name: "ui", active: false, tabs: 1, waiting: 0, working: 0, members: [{ root: "C:/ui", name: "ui" }] },
] }));
h.click(h.$("rail-open"));
h.recv({ type: "recents", items: [] });
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", places: [], entries: [] });

// "Group open projects…" turns every open row into a checkbox, and the
// confirm button counts what is ticked and refuses fewer than two.
const start = Array.from(h.$("overlay-body").querySelectorAll("button")).find((b) => b.textContent === "Group open projects\u2026");
assert.ok(start, "no way into grouping mode from the dialog");
h.click(start);
const picks = () => h.$("overlay-body").querySelectorAll("input[type=checkbox]");
assert.strictEqual(picks().length, 2, "one checkbox per open project");
let confirm = Array.from(h.$("overlay-body").querySelectorAll("button")).find((b) => b.textContent.startsWith("Group"));
assert.ok(confirm.disabled, "grouping fewer than two is refused");
// Each tick redraws the dialog, since the count shown has to follow it, so
// the boxes are found afresh rather than kept from before the redraw.
picks()[0].checked = true;
h.dispatch(picks()[0], new h.Ev("change", { target: picks()[0] }));
picks()[1].checked = true;
h.dispatch(picks()[1], new h.Ev("change", { target: picks()[1] }));
confirm = Array.from(h.$("overlay-body").querySelectorAll("button")).find((b) => b.textContent === "Group 2 selected");
assert.ok(confirm, "the confirm button did not count both picks");
h.win._prompt = "platform";
h.click(confirm);
assert.deepStrictEqual(h.commands().pop(), { cmd: "groupProjects", roots: ["C:/api", "C:/ui"], text: "platform" });

// The server's own answer to that: one merged project, two members.
h.recv(fixture({ projects: [
  { root: "C:/api", name: "platform", active: true, tabs: 2, waiting: 0, working: 0,
    members: [{ root: "C:/api", name: "api" }, { root: "C:/ui", name: "ui" }] },
] }));
h.click(h.$("rail-open"));
h.recv({ type: "recents", items: [] });
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", places: [], entries: [] });
const goes = h.$("overlay-body").querySelectorAll("button.proj-go");
assert.strictEqual(goes.length, 1, "two open roots read as one project now");
assert.ok(goes[0].textContent.includes("platform"), "the group's own name did not reach the row");
assert.ok(goes[0].textContent.includes("2 directories"), "a grouped project's row still claimed a single path: " + goes[0].textContent);

// Renaming an open project reaches the live group by any of its members,
// not the store's per-root name a closed project uses.
h.win._prompt = "renamed";
h.click(h.$("overlay-body").querySelector('[aria-label="Rename this project"]'));
assert.deepStrictEqual(h.commands().pop(), { cmd: "renameGroup", root: "C:/api", text: "renamed" });

// A project just grouped by hand opens already showing its members, so the
// action reads as having done something rather than needing a second click
// to see what it did.
let members = h.$("overlay-body").querySelectorAll(".proj-member-row");
assert.strictEqual(members.length, 2, "both members should be listed once grouped");
assert.ok(members[0].textContent.includes("api") && members[1].textContent.includes("ui"), "got: " + h.$("overlay-body").querySelector(".proj-members").textContent);

// The same toggle folds it away and back again.
const expand = h.$("overlay-body").querySelector("button.proj-expand");
assert.ok(expand, "a grouped project offers no way to hide its members");
h.click(expand);
assert.ok(!h.$("overlay-body").querySelector(".proj-members"), "the toggle did not fold the member list away");
h.click(h.$("overlay-body").querySelector("button.proj-expand"));
members = h.$("overlay-body").querySelectorAll(".proj-member-row");
assert.strictEqual(members.length, 2, "the toggle did not bring the members back");

// Picking a member reaches it directly, rather than wherever the group was
// last left.
h.click(members[0].querySelector("button.proj-member-go"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectRepo", root: "C:/api" });
assert.ok(h.$("overlay").hidden, "picking a member left the dialog open");

// Splitting one back out. Reopening the dialog does not forget that this
// project was left expanded, so its members are already there to act on.
h.click(h.$("rail-open"));
h.recv({ type: "recents", items: [] });
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", places: [], entries: [] });
h.click(h.$("overlay-body").querySelectorAll(".proj-member-row")[1].querySelector("button.icon-btn"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "removeRepoFromGroup", root: "C:/ui" });

// "Add directory…" hands the existing folder browser over to
// addRepoToGroup instead of openProject, and back to the project list
// rather than closing the whole dialog once one is picked.
const addRepo = Array.from(h.$("overlay-body").querySelectorAll("button")).find((b) => b.textContent === "Add directory\u2026");
assert.ok(addRepo, "no way to add a directory to an open project");
h.click(addRepo);
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", places: [], entries: [
  { name: "billing", path: "C:/repos/billing", isRepo: true },
] });
assert.ok(h.$("overlay-body").textContent.includes("Add a directory to platform"), "the browser did not say which project it was adding to");
const addBtn = h.$("overlay-body").querySelectorAll("div.dir-row")[0].querySelector("button.chip");
assert.strictEqual(addBtn.textContent, "Add", "the per-row button still said Open while adding a directory");
h.click(addBtn);
assert.deepStrictEqual(h.commands().pop(), { cmd: "addRepoToGroup", root: "C:/api", path: "C:/repos/billing" });
assert.ok(!h.$("overlay").hidden, "adding a repo closed the whole dialog rather than returning to the project list");
assert.ok(h.$("overlay-body").querySelector("button.proj-go"), "adding a repo left the browser up instead of the project list");
`)
}

// A new tab has always opened wherever the implicit default was, which was
// never a question for a project of one repo -- but a project spanning more
// than one had no way at all to land a fresh tab in a member that was not
// the active one, short of switching to it first. Each member row in its
// own project's expanded list offers Agent and Shell directly, the same two
// actions the Worktrees panel already offers per checkout.
func TestNewTabFromAProjectsMemberRow(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/api", name: "platform", active: true, tabs: 2, waiting: 0, working: 0,
    members: [{ root: "C:/api", name: "api" }, { root: "C:/ui", name: "ui" }] },
] }));
h.click(h.$("rail-open"));
h.recv({ type: "recents", items: [] });
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", places: [], entries: [] });
h.click(h.$("overlay-body").querySelector("button.proj-expand"));
const rows = h.$("overlay-body").querySelectorAll(".proj-member-row");
assert.strictEqual(rows.length, 2, "both members should be listed");

const uiRow = Array.from(rows).find((r) => r.textContent.includes("ui"));
const agentBtn = Array.from(uiRow.querySelectorAll("button")).find((b) => b.textContent === "Agent");
assert.ok(agentBtn, "the member row offers no way to open an agent tab there");
h.click(agentBtn);
assert.deepStrictEqual(h.commands().pop(), { cmd: "newTab", kind: "agent", path: "C:/ui" });
assert.ok(h.$("overlay").hidden, "opening a tab from the member row left the dialog open");

// Reopening does not forget the project was left expanded.
h.click(h.$("rail-open"));
h.recv({ type: "recents", items: [] });
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", places: [], entries: [] });
const rows2 = h.$("overlay-body").querySelectorAll(".proj-member-row");
const uiRow2 = Array.from(rows2).find((r) => r.textContent.includes("ui"));
const shellBtn = Array.from(uiRow2.querySelectorAll("button")).find((b) => b.textContent === "Shell");
h.click(shellBtn);
assert.deepStrictEqual(h.commands().pop(), { cmd: "newTab", kind: "shell", path: "C:/ui" });

// A project of one repo has no member list at all, so nothing here applies
// to it -- the common case stays exactly as it was.
h.recv(fixture());
h.click(h.$("rail-open"));
h.recv({ type: "recents", items: [] });
h.recv({ type: "browse", path: "C:/repos", parent: "C:/", places: [], entries: [] });
assert.ok(!h.$("overlay-body").querySelector(".proj-member-row"), "a project of one repo should show no member rows");
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

// Choosing a model by hand is better informed for knowing how capable each is
// and, where it means something, what it costs: each model row carries its
// tier, and an API agent's model its price with the day it was read.
func TestThePickerShowsTiersAndDatedPrices(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const checked = "2026-09-12";
h.recv(fixture({ agents: catalog({ items: [
  { id: "anthropic", name: "Claude API", runner: "api", available: true, defaultModel: "claude-opus-5",
    models: [{ id: "claude-opus-5", name: "Opus 5", tier: "top", price: { in: 5, out: 25, checked } },
             { id: "claude-haiku-4-5", name: "Haiku 4.5", tier: "small", price: { in: 1, out: 5, checked } },
             { id: "odd", name: "Unpriced" }] },
] }) }));
h.click(h.$("new-tab-pick"));
h.key({ key: "ArrowRight" });
const rows = h.$("agent-list").querySelectorAll("div.pick-row.model");
assert.strictEqual(rows.length, 3, "the models did not appear");
const tier = rows[0].querySelector("span.pick-tier");
assert.ok(tier && tier.textContent === "top", "the model's tier is not shown: " + rows[0].textContent);
assert.ok(tier.dataset.tip, "the tier chip does not say what a tier is");
assert.ok(rows[1].textContent.includes("$1 / $5 per M tokens, checked 2026-09-12"),
  "the price, or the day it was read, is not shown: " + rows[1].textContent);
assert.ok(!rows[2].querySelector("span.pick-tier") && !rows[2].textContent.includes("$"),
  "a model with no tier or price was given one: " + rows[2].textContent);

// The fan-out's selects have no room for chips, so they say the same in words.
h.key({ key: "Escape" });
h.press("fanout");
h.recv({ type: "fanoutPreview", paneId: "p1", tasks: ["one"], isRepo: false, cwd: "C:/repo", agent: "anthropic",
  agents: [{ id: "anthropic", name: "Claude API", default: "claude-sonnet-5", models: [
    { id: "claude-sonnet-5", name: "Sonnet 5", note: "the everyday one", tier: "mid", price: { in: 2, out: 10, checked } },
    { id: "claude-haiku-4-5", name: "Haiku 4.5", tier: "small" },
    { id: "odd", name: "Unpriced" }] }] });
const opts = h.$("overlay-body").querySelector("select.fan-agent-sel").querySelectorAll("option").map((o) => o.textContent);
assert.ok(opts.includes("Sonnet 5 — the everyday one (mid, $2/$10)"), "got: " + JSON.stringify(opts));
assert.ok(opts.includes("Haiku 4.5 (small)"), "got: " + JSON.stringify(opts));
assert.ok(opts.includes("Unpriced"), "got: " + JSON.stringify(opts));
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

// The OpenAI-compatible endpoint is no use until it has an address, and that
// could only be given by editing agents.json. The picker asks for it where the
// endpoint is picked, keeps what was typed when it is refused, shows the
// refusal beside it, and starts the endpoint once the address is in.
func TestAnEndpointIsGivenItsAddressInThePicker(t *testing.T) {
	runFrontEnd(t, `
h.hello();
// The harness finds an element by id after it has left the document, which a
// browser does not, so whether the field is still there is asked this way.
const live = (id) => { const n = h.$(id); return n && n.isConnected ? n : null; };
const endpoint = { id: "openai-compatible", name: "OpenAI-compatible endpoint", runner: "api",
  available: false, defaultModel: "", models: [], addressable: true, install: "give it an address" };
const base = catalog().items;
h.recv(fixture({ agents: catalog({ items: base.concat([endpoint]) }) }));
h.click(h.$("new-tab-pick"));

const rows = () => h.$("agent-list").querySelectorAll("div.pick-row");
assert.ok(rows()[2].textContent.includes("needs an address"), "got: " + rows()[2].textContent);

// Enter on it asks for the address rather than saying it is not installed.
h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
h.key({ key: "Enter" });
const field = h.$("agent-address");
assert.ok(field, "Enter on an endpoint with no address did not offer a field for one");
assert.ok(h.doc.activeElement === field, "the address field did not take the keyboard");
assert.ok(!h.$("overlay").hidden, "the picker closed");

// What a model server prints, without its scheme.
field.value = "localhost:11434";
field.oninput();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "setAgentAddress", id: "openai-compatible", text: "localhost:11434" });
h.recv({ type: "agentAddress", id: "openai-compatible", address: "localhost:11434",
  error: '"localhost:11434" has no http:// in front: type http://localhost:11434' });
const why = h.$("agent-address-error");
assert.ok(why && why.textContent.includes("type http://localhost:11434"), "the refusal is not shown under the field");
assert.strictEqual(why.getAttribute("role"), "alert", "the refusal is not announced");
assert.strictEqual(h.$("agent-address").value, "localhost:11434", "what was typed went with the refusal");
assert.ok(h.doc.activeElement === h.$("agent-address"), "the refusal took the keyboard off the field");

// A catalog arriving while it is put right does not take it away.
h.recv(fixture({ agents: catalog({ items: base.concat([Object.assign({}, endpoint, { install: "moved" })]) }) }));
assert.strictEqual(h.$("agent-address").value, "localhost:11434", "a catalog push lost what was typed");
assert.ok(h.doc.activeElement === h.$("agent-address"), "a catalog push took the keyboard off the field");

h.$("agent-address").value = "http://127.0.0.1:11434/v1";
h.$("agent-address").oninput();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "setAgentAddress", id: "openai-compatible", text: "http://127.0.0.1:11434/v1" });
h.recv({ type: "agentAddress", id: "openai-compatible", address: "http://127.0.0.1:11434/v1" });
assert.ok(!live("agent-address"), "the field stayed after the address was saved");
assert.ok(h.doc.activeElement === h.$("agent-filter"), "the keyboard did not go back to the filter");

// On loopback it needs no key, so the catalog that follows offers it, and
// Enter starts it.
h.recv(fixture({ agents: catalog({ items: base.concat([Object.assign({}, endpoint,
  { available: true, address: "http://127.0.0.1:11434/v1" })]) }) }));
assert.deepStrictEqual(h.$("overlay-body").querySelectorAll("div.pick-head").map((n) => n.textContent),
  ["Installed", "Not installed"]);
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "newTab", kind: "agent", agent: "openai-compatible", model: "" });
`)
}

// An API agent that has an address shows it, and it is changed from the row
// the arrows reach it by. Escape puts the field away rather than closing the
// picker, and an address left as it was is not written back.
func TestAnAgentsAddressIsChangedFromItsRow(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const live = (id) => { const n = h.$(id); return n && n.isConnected ? n : null; };
const local = { id: "local", name: "Local llama", runner: "api", available: true, defaultModel: "qwen",
  addressable: true, address: "http://127.0.0.1:11434/v1", models: [{ id: "qwen", name: "qwen" }] };
h.recv(fixture({ agents: catalog({ items: [local] }) }));
h.click(h.$("new-tab-pick"));
const rows = () => h.$("agent-list").querySelectorAll("div.pick-row");
assert.ok(rows()[0].textContent.includes("http://127.0.0.1:11434/v1"), "the address is not shown: " + rows()[0].textContent);

h.key({ key: "ArrowRight" });
assert.strictEqual(rows().length, 3, "the agent did not open onto its address and its model");
assert.ok(rows()[1].textContent.includes("Address: http://127.0.0.1:11434/v1"), "got: " + rows()[1].textContent);

h.key({ key: "ArrowDown" });
h.key({ key: "Enter" });
assert.strictEqual(h.$("agent-address").value, "http://127.0.0.1:11434/v1", "the field does not start from the address");
h.key({ key: "Escape" });
assert.ok(!live("agent-address"), "Escape did not put the field away");
assert.ok(!h.$("overlay").hidden, "Escape closed the picker along with the field");
assert.ok(h.doc.activeElement === h.$("agent-filter"), "the keyboard did not go back to the filter");

const before = h.commands().length;
h.key({ key: "Enter" });
assert.ok(live("agent-address"), "Enter on the address row did not open the field");
h.key({ key: "Enter" });
assert.strictEqual(h.commands().length, before, "an unchanged address was sent to be saved");
assert.ok(!live("agent-address"), "the field stayed open");
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

// The version picker, reached from Settings › General, lists what
// listVersions answers and installs whichever row is chosen -- forward,
// back, or a reinstall of the version already running, each button worded
// for what it would actually do.
func TestTheVersionPickerListsAndInstallsAChoice(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("set-versions"));
assert.ok(!h.$("overlay").hidden, "the version picker did not open");
assert.deepStrictEqual(h.commands().pop(), { cmd: "listVersions" });
assert.ok(h.$("overlay-body").textContent.includes("Loading"), "the picker does not say it is loading");

h.recv({ type: "versions", items: [
  { version: "v1.6.0", relation: "newer", published: "2026-09-10T00:00:00Z", notes: "adds things" },
  { version: "v1.5.0", relation: "current" },
  { version: "v1.4.0", relation: "older", notes: "an older release" },
] });
const body = h.$("overlay-body");
assert.ok(body.textContent.includes("v1.6.0"), "the newer release is not listed");
assert.ok(body.textContent.includes("running now"), "the running version is not marked");
assert.strictEqual(h.$("version-install-v1.6.0").textContent, "Update to…");
assert.strictEqual(h.$("version-install-v1.5.0").textContent, "Reinstall");

const install = h.$("version-install-v1.4.0");
assert.strictEqual(install.textContent, "Roll back to…", "the older release's button does not say roll back");
h.click(install);
assert.deepStrictEqual(h.commands().pop(), { cmd: "installVersion", text: "v1.4.0" });
assert.ok(install.disabled, "the button clicked does not show it is working");
assert.strictEqual(install.textContent, "Downloading…");
`)
}

// A dialog closed while its answer is still on the way must not come back on
// its own -- see TestAClosedDialogStaysClosedWhenItsAnswerArrives for the
// worktree and keys dialogs' own version of this.
func TestTheVersionPickerStaysClosedWhenItsAnswerArrives(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("set-versions"));
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape did not close the picker");
h.recv({ type: "versions", items: [{ version: "v1.4.0", relation: "current" }] });
assert.ok(h.$("overlay").hidden, "the answer reopened the picker after it was closed");
`)
}

// An error listing recent releases -- the site and GitHub both out of reach,
// say -- is shown in the picker rather than left loading forever.
func TestTheVersionPickerShowsAFailureToList(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("set-versions"));
h.recv({ type: "versions", error: "could not reach dl.flockdeck.ai or GitHub" });
assert.ok(h.$("overlay-body").textContent.includes("could not reach"), "the picker does not say what went wrong");
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

// Changes reviewed one repo's working tree with no way to reach another's,
// even though a grouped project's whole point is agents in several repos at
// once -- reviewing the second one meant closing the dialog, switching
// projects, and opening it again.
func TestChangesOffersEveryRepoInAGroupedProject(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/api", name: "platform", active: true, tabs: 2, waiting: 0, working: 0,
    members: [{ root: "C:/api", name: "api" }, { root: "C:/ui", name: "ui" }] },
] }));
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/api", branch: "main", hasRemote: false, files: [] });

const chip = (name) => Array.from(h.$("overlay-body").querySelector(".rev-repo-picker").querySelectorAll("button"))
  .find((b) => b.textContent === name);
assert.ok(chip("api") && chip("ui"), "not every member of the project is offered");
assert.ok(chip("api").disabled, "the repo already shown is not marked current");
assert.ok(!chip("ui").disabled, "the other repo cannot be switched to");

h.click(chip("ui"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "changes", path: "C:/ui" });
h.recv({ type: "changes", cwd: "C:/ui", branch: "main", hasRemote: false, files: [] });
assert.ok(chip("ui").disabled && !chip("api").disabled, "switching did not move which repo reads as current");

// A project with only the one repo offers nothing to switch to.
h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 1, waiting: 0, working: 0,
    members: [{ root: "C:/repo", name: "repo" }] },
] }));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false, files: [] });
assert.ok(!h.$("overlay-body").querySelector(".rev-repo-picker"), "a single-repo project offers a picker anyway");
`)
}

// state.recall said a running version had been withdrawn since it was
// installed, with nothing in the window ever showing it -- the server
// computed the warning and nobody saw it.
func TestTheRecallBannerWarnsOfAWithdrawnVersion(t *testing.T) {
	runFrontEnd(t, `
h.hello();
assert.ok(h.$("recall-banner").hidden, "a banner with nothing recalled");

h.recv(fixture({ recall: { version: "9.9.8", reason: "corrupts settings on first launch", upgrade: "9.9.9" } }));
assert.ok(!h.$("recall-banner").hidden, "the recall was never shown");
assert.ok(h.$("recall-banner").textContent.includes("9.9.8"), "got: " + h.$("recall-banner").textContent);
assert.ok(h.$("recall-banner").textContent.includes("corrupts settings on first launch"), "got: " + h.$("recall-banner").textContent);

// No update downloaded yet: the banner offers to look for one.
let btn = h.$("recall-banner").querySelector("button");
assert.strictEqual(btn.textContent, "Check for updates");
h.click(btn);
assert.deepStrictEqual(h.commands().pop(), { cmd: "checkForUpdate" });

// One arrives: the same banner now offers to install it, through the
// ordinary update dialog rather than a recall-specific one.
h.recv(fixture({ recall: { version: "9.9.8", reason: "corrupts settings on first launch", upgrade: "9.9.9" },
  update: { version: "9.9.9", notes: "Fixes the settings corruption." } }));
btn = h.$("recall-banner").querySelector("button.primary");
assert.strictEqual(btn.textContent, "Install 9.9.9…");
h.click(btn);
assert.strictEqual(h.$("overlay-title").textContent, "Update to 9.9.9");

// The watcher clearing it (moved off the version, most likely) hides it
// again.
h.recv(fixture({ update: { version: "9.9.9", notes: "Fixes the settings corruption." } }));
assert.ok(h.$("recall-banner").hidden, "the banner outlived its own recall");
`)
}

// Checking for an update reaches GitHub, which is not instant, and neither
// button that asks for one said so while it waited -- a press that had
// registered looked exactly like one that had not.
func TestCheckingForAnUpdateSaysItIsRunning(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ recall: { version: "9.9.8", reason: "corrupts settings on first launch", upgrade: "9.9.9" } }));
h.click(h.$("btn-settings"));

h.click(h.$("set-check-update"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "checkForUpdate" });
assert.strictEqual(h.$("set-check-update").textContent, "Checking…", "the settings button did not say it was working");
assert.ok(h.$("set-check-update").disabled, "the settings button still invites a press");
// The recall banner's own button, elsewhere on screen, says the same thing:
// only one check is ever out at a time.
assert.strictEqual(h.$("recall-check-update").textContent, "Checking…", "the recall banner did not follow along");

// A second press while the first is in flight asks nothing more.
const sent = h.commands().length;
h.click(h.$("set-check-update"));
assert.strictEqual(h.commands().length, sent, "a second check was sent while the first was in flight");

// Nothing new was found: the server's only word on that is a notice, and it
// releases both buttons.
h.recv({ type: "notice", text: "flockdeck is up to date", error: false });
assert.strictEqual(h.$("set-check-update").textContent, "Check for updates now", "the settings button was left saying it was working");
assert.ok(!h.$("set-check-update").disabled, "the settings button was left disabled");
assert.strictEqual(h.$("recall-check-update").textContent, "Check for updates", "the recall banner was left saying it was working");
`)
}

// A connection dropped while a manual update check was out took its answer
// with it, and nothing was ever going to ask again on the button's behalf --
// there is no dialog to reopen, unlike Changes or the worktrees panel.
// Reconnecting is what releases it instead.
func TestACheckForUpdateDroppedWithTheConnectionIsReleased(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("set-check-update"));
assert.ok(h.$("set-check-update").disabled, "the button did not say it was working");

h.controls()[0].close();
h.click(h.$("retry"));
h.controls().pop().onopen();
assert.ok(!h.$("set-check-update").disabled, "reconnecting did not release the button");
assert.strictEqual(h.$("set-check-update").textContent, "Check for updates now");
`)
}

// The release notes and a file's diff each scroll inside a box of their own,
// and neither box could be focused, so what was past its bottom edge was out
// of reach from the keyboard. Each is a named stop on Tab's way round its
// dialog now, and drawing the rest of a long diff keeps it one.
func TestTheDiffAndTheReleaseNotesCanBeScrolledFromTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ update: { version: "9.9.9", notes: "Faster." } }));
const stops = () => {
  const seen = [];
  for (let i = 0; i < 40; i++) { h.key({ key: "Tab" }); seen.push(h.doc.activeElement); }
  return seen;
};

h.click(h.$("btn-update"));
const notes = h.$("overlay-body").querySelector("div.update-notes");
assert.strictEqual(notes.getAttribute("tabindex"), "0", "the release notes cannot be focused, so they cannot be scrolled from the keyboard");
assert.strictEqual(notes.getAttribute("role"), "region");
assert.strictEqual(notes.getAttribute("aria-label"), "Release notes");
assert.ok(stops().includes(notes), "Tab never reaches the release notes");
h.key({ key: "Escape" });

h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "a/one.go", label: "M", added: 1, removed: 0 }, { path: "a/two.go", label: "M", added: 1, removed: 0 }] });
const rows = h.$("overlay-body").querySelectorAll("div.rev-file");
h.click(rows[0]);
const NL = String.fromCharCode(10);
const long = ["@@ -1,4000 +1,4000 @@"];
for (let i = 0; i < 4000; i++) long.push("+line " + i);
h.recv({ type: "diff", cwd: "C:/repo", file: "a/one.go", text: long.join(NL) });
const diff = h.$("overlay-body").querySelector("div.rev-diff");
assert.strictEqual(diff.getAttribute("tabindex"), "0", "the diff cannot be focused, so it cannot be scrolled from the keyboard");
assert.strictEqual(diff.getAttribute("role"), "region");
assert.strictEqual(diff.getAttribute("aria-label"), "Diff of a/one.go");
assert.ok(stops().includes(diff), "Tab never reaches the diff");

// Drawing the rest from the keyboard puts the keyboard on the diff, which
// stays a stop on the way round.
const more = diff.querySelectorAll("button").find((b) => b.textContent === "Show them");
more.focus();
h.key({ key: "Enter" });
assert.ok(h.doc.activeElement === diff, "the keyboard did not go to the diff");
assert.strictEqual(diff.getAttribute("tabindex"), "0", "drawing the rest took the diff off Tab's way round");

h.click(rows[1]);
assert.strictEqual(diff.getAttribute("aria-label"), "Diff of a/two.go", "the diff is still named after the file before");
`)
}

// Curated release notes are Markdown-ish prose — a title, headings, bullet
// lists, **bold** — and used to show up in the update dialog as literal
// text, "##" and "-" characters included, because the box rendered them as a
// flat preformatted block. renderNotes turns the shapes docs/releases/*.md
// actually uses into real elements instead.
func TestReleaseNotesRenderAsMarkdown(t *testing.T) {
	runFrontEnd(t, `
const NL = String.fromCharCode(10);
const notesText = [
  "# Flockdeck v9.9.9",
  "",
  "## Faster fan-out",
  "",
  "Starting several agents at once now takes **half the time** it used to.",
  "",
  "- Say so in Settings › Agents › Routing.",
  "- Reload the phone view to see it there too.",
].join(NL);
h.hello();
h.recv(fixture({ update: { version: "9.9.9", notes: notesText } }));
h.click(h.$("btn-update"));
const box = h.$("overlay-body").querySelector("div.update-notes");
assert.ok(box, "the notes box is no longer a div");
assert.ok(!box.textContent.includes("##"), "a heading marker leaked into the rendered text");
assert.ok(!box.textContent.includes("- Say so"), "a bullet marker leaked into the rendered text");

const headings = box.querySelectorAll("h3, h4").map((n) => n.textContent);
assert.deepStrictEqual(headings, ["Flockdeck v9.9.9", "Faster fan-out"], "the title and the theme did not become headings");

const items = box.querySelectorAll("li").map((n) => n.textContent);
assert.deepStrictEqual(items, [
  "Say so in Settings › Agents › Routing.",
  "Reload the phone view to see it there too.",
], "the bullets did not become a list");

const bold = box.querySelector("strong");
assert.strictEqual(bold && bold.textContent, "half the time", "**bold** did not render as emphasis");
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
// A key that says no position still picks by the digit it typed.
h.key({ key: "1", code: "", altKey: true });
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectTab", id: "t1" });
`)
}

// On a Mac, Option with a digit types a character on most layouts - [ is
// Option+5 on a German Mac, | is Option+7 - and it was read by position as
// Alt+1 … Alt+9, so it switched tab and the character never reached the pane.
// There Cmd with a digit picks the tab, as it does in every Mac application.
func TestOptionWithADigitOnAMacTypesItsCharacter(t *testing.T) {
	runFrontEnd(t, `
const m = boot({ platform: "MacIntel" });
m.hello();
m.recv(fixture());
const typing = m.doc.createElement("textarea");
typing.className = "xterm-helper-textarea";
m.terms[0].host.append(typing);
const reached = [];
typing.addEventListener("keydown", (e) => reached.push(e.key));
typing.focus();
const before = m.commands().length;
const ev = m.key({ key: "[", code: "Digit5", altKey: true });
assert.ok(!ev.defaultPrevented, "Option+5 was kept from typing [");
assert.deepStrictEqual(reached, ["["], "Option+5 did not reach the terminal as [");
m.key({ key: "|", code: "Digit7", altKey: true });
assert.deepStrictEqual(m.commands().slice(before), [], "typing [ and | switched tabs");

m.key({ key: "2", code: "Digit2", metaKey: true });
assert.deepStrictEqual(m.commands().pop(), { cmd: "selectTab", id: "t2" }, "Cmd+2 did not pick the second tab");
// A French Mac's digit row types & é " ' unless Shift is held, and Cmd+é is
// still the second key along.
m.key({ key: "&", code: "Digit1", metaKey: true });
assert.deepStrictEqual(m.commands().pop(), { cmd: "selectTab", id: "t1" }, "Cmd and the first key did not pick the first tab");
`)
}

// On AZERTY the 0 key types à unless Shift is held, so Ctrl+0 arrived as
// Ctrl+à, matched nothing, and a font size made bigger could not be put back
// from the keyboard.
func TestCtrlZeroResetsTheFontSizeOnAZERTY(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("fontUp");
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontSize", size: 14 });
h.key({ key: "à", code: "Digit0", ctrlKey: true });
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontSize", size: 13 }, "Ctrl+0 on AZERTY did not reset the font size");
`)
}

// The window's shortcuts went on running behind an open dialog: Ctrl+Shift+W
// pressed while writing a commit message closed a pane nobody could see, and
// Alt+2 switched the tab under it. While a dialog is up only the font keys,
// the palette and the help still act.
func TestShortcutsWaitBehindAnOpenDialog(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("settings");
assert.ok(!h.$("overlay").hidden, "the settings did not open");
const before = h.commands().length;
const closing = h.press("closePane");
h.press("newAgentTab");
h.key({ key: "2", code: "Digit2", altKey: true });
const sent = h.commands().slice(before).map((c) => c.cmd);
assert.ok(!sent.includes("closePane"), "Ctrl+Shift+W closed a pane behind the dialog");
assert.ok(!sent.includes("newTab"), "a tab was opened behind the dialog");
assert.ok(!sent.includes("selectTab"), "Alt+2 switched tab behind the dialog");
assert.ok(closing.defaultPrevented, "Ctrl+Shift+W was left to the browser, which closes the window with it");
assert.ok(!h.$("overlay").hidden, "the dialog went");

h.press("fontUp");
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontSize", size: 14 }, "the font keys stopped working in a dialog");
h.press("help");
assert.strictEqual(h.$("overlay-title").textContent, "Help", "F1 did not open the help from a dialog");
h.press("palette");
assert.ok(!h.$("palette").hidden, "the palette did not open from a dialog");
h.key({ key: "Escape" });

// Closed, the keys are the window's again.
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "the dialog did not close");
h.press("closePane");
assert.deepStrictEqual(h.commands().pop(), { cmd: "closePane", id: "p1" });
`)
}

// termKey is a keydown as xterm hands it to a terminal's own key handler.
const termKey = `
const termKey = (init) => Object.assign({ type: "keydown", key: "", code: "", ctrlKey: false, shiftKey: false,
  altKey: false, metaKey: false, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; } }, init);
`

// Ctrl+C with a selection sent ^C and Ctrl+V sent ^V, so text could only be
// copied out of a pane or pasted into one with the mouse. They work the way
// Windows Terminal has them: Ctrl+C copies when something is selected and is
// ^C otherwise, Ctrl+Shift+C copies, and Ctrl+V and Ctrl+Shift+V paste.
func TestCopyAndPasteInATerminalGoTheWindowsTerminalWay(t *testing.T) {
	runFrontEnd(t, termKey+`
h.hello();
h.recv(fixture());
const t = h.terms[0];
const keys = t._keys;
assert.ok(keys, "the terminal has no key handler of its own");

let e = termKey({ key: "c", code: "KeyC", ctrlKey: true });
assert.strictEqual(keys(e), true, "Ctrl+C with nothing selected did not go to the program as ^C");

t.selection = "some output";
e = termKey({ key: "c", code: "KeyC", ctrlKey: true });
assert.strictEqual(keys(e), false, "Ctrl+C with a selection went to the program");
assert.ok(e.defaultPrevented, "Ctrl+C with a selection was left to the browser");
await h.sleep(0);
assert.strictEqual(h.win._copied, "some output", "Ctrl+C did not copy the selection");
assert.strictEqual(t.selection, "", "the selection stayed, so the next Ctrl+C copied again instead of interrupting");

// A layout without Latin letters reports the key by its own letter.
t.selection = "по-русски";
assert.strictEqual(keys(termKey({ key: "с", code: "KeyC", ctrlKey: true })), false, "Ctrl+C on a Russian layout did not copy");
await h.sleep(0);
assert.strictEqual(h.win._copied, "по-русски");

t.selection = "more";
e = termKey({ key: "C", code: "KeyC", ctrlKey: true, shiftKey: true });
assert.strictEqual(keys(e), false, "Ctrl+Shift+C went to the program");
await h.sleep(0);
assert.strictEqual(h.win._copied, "more", "Ctrl+Shift+C did not copy");
e = termKey({ key: "C", code: "KeyC", ctrlKey: true, shiftKey: true });
assert.strictEqual(keys(e), false, "Ctrl+Shift+C with nothing selected went to the program");
assert.ok(e.defaultPrevented, "Ctrl+Shift+C was left to the browser, which opens its inspector with it");

for (const shiftKey of [false, true]) {
  e = termKey({ key: shiftKey ? "V" : "v", code: "KeyV", ctrlKey: true, shiftKey });
  assert.strictEqual(keys(e), false, (shiftKey ? "Ctrl+Shift+V" : "Ctrl+V") + " went to the program as ^V");
  assert.ok(!e.defaultPrevented, (shiftKey ? "Ctrl+Shift+V" : "Ctrl+V") + " was kept from the browser, which pastes");
}
assert.strictEqual(keys(termKey({ key: "d", code: "KeyD", ctrlKey: true })), true, "Ctrl+D no longer reaches the program");
assert.strictEqual(keys(termKey({ type: "keyup", key: "v", code: "KeyV", ctrlKey: true })), true, "a key going up was taken");

// On a Mac, Cmd+C and Cmd+V copy and paste already, and Ctrl+C and Ctrl+V
// are the program's.
const m = boot({ platform: "MacIntel" });
m.hello();
m.recv(fixture());
m.terms[0].selection = "x";
assert.strictEqual(m.terms[0]._keys(termKey({ key: "c", code: "KeyC", ctrlKey: true })), true, "Ctrl+C on a Mac did not reach the program");
assert.strictEqual(m.terms[0]._keys(termKey({ key: "v", code: "KeyV", ctrlKey: true })), true, "Ctrl+V on a Mac did not reach the program");
`)
}

// A plain Enter typed straight into a terminal is one \r, and \r is Enter to
// whatever is reading it -- a shell, or an agent's own CLI -- with no way for
// a bare byte stream to tell "Enter" and "Enter, but Shift was held" apart.
// Sending Shift+Enter through paste() instead carries it inside the same
// bracketed-paste markers a real paste gets, which is what tells a program
// that has turned bracketed paste on (Claude Code has) that this \r is not
// the one that submits -- rather than it reaching the program indistinguishable
// from a plain Enter and submitting early.
func TestShiftEnterInATerminalIsSentAsAPaste(t *testing.T) {
	runFrontEnd(t, termKey+`
h.hello();
h.recv(fixture());
const t = h.terms[0];
const keys = t._keys;

assert.strictEqual(keys(termKey({ key: "Enter" })), true, "a plain Enter no longer reaches the program as a plain Enter");
assert.deepStrictEqual(t.pasted, [], "a plain Enter was sent as though it were Shift+Enter");

let e = termKey({ key: "Enter", shiftKey: true });
assert.strictEqual(keys(e), false, "Shift+Enter was left for xterm's own Enter handling too, sending a bare \\r");
assert.ok(e.defaultPrevented, "Shift+Enter's bare \\r reached the program as well as the paste");
assert.deepStrictEqual(t.pasted, ["\n"], "Shift+Enter did not reach the program as a paste");

// Held with another modifier, it is that binding's, not this one's.
t.pasted = [];
assert.strictEqual(keys(termKey({ key: "Enter", shiftKey: true, ctrlKey: true })), true, "Ctrl+Shift+Enter was taken as this Shift+Enter");
assert.strictEqual(keys(termKey({ key: "Enter", shiftKey: true, altKey: true })), true, "Alt+Shift+Enter was taken as this Shift+Enter");
assert.strictEqual(keys(termKey({ key: "Enter", shiftKey: true, metaKey: true })), true, "Meta+Shift+Enter was taken as this Shift+Enter");
assert.deepStrictEqual(t.pasted, [], "a held modifier still went through as a paste");

// Unlike Ctrl+C and Ctrl+V, this is not a Mac exception: a terminal has no
// separate code for Shift+Enter on any platform.
const m = boot({ platform: "MacIntel" });
m.hello();
m.recv(fixture());
assert.strictEqual(m.terms[0]._keys(termKey({ key: "Enter", shiftKey: true })), false, "Shift+Enter on a Mac reached the program as a plain Enter");
assert.deepStrictEqual(m.terms[0].pasted, ["\n"], "Shift+Enter on a Mac did not reach the program as a paste");
`)
}

// A paste is sent between the markers of bracketed paste, so that the program
// knows it is text rather than keys, and the markers were sent as they were
// found in the text: a copied ESC[201~ ended the paste early, and whatever
// followed it was typed at the program as keys - a command and its Enter.
func TestAPasteCannotCloseTheBracketsItIsSentIn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const t = h.terms[0];
const typing = h.doc.createElement("textarea");
typing.className = "xterm-helper-textarea";
t.host.append(typing);
const reached = [];
typing.addEventListener("paste", () => reached.push("xterm"));
const paste = (text) => {
  const ev = new h.Ev("paste", { clipboardData: { getData: (type) => (type === "text/plain" ? text : "") } });
  h.dispatch(typing, ev);
  return ev;
};

const ev = paste("echo safe\x1b[201~rm -rf ~\n\x1b[200~\x9b201~");
assert.deepStrictEqual(t.pasted, ["echo safe[201~rm -rf ~\n[200~201~"], "the paste kept its escape characters");
assert.deepStrictEqual(reached, [], "xterm pasted the text as it was as well");
assert.ok(ev.defaultPrevented, "the browser pasted the text as it was as well");

// Ordinary text is xterm's to paste, as it always was.
paste("git status\n");
assert.deepStrictEqual(reached, ["xterm"], "an ordinary paste did not reach xterm");
assert.strictEqual(t.pasted.length, 1, "an ordinary paste was pasted twice");
`)
}

// The server takes a frame of up to 4 MiB from a terminal, and a paste bigger
// than that went as one frame and was dropped with the connection, silently.
// Input is sent a mebibyte at a time.
func TestAHugePasteGoesInFramesTheServerTakes(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const ws = h.sockets.find((s) => s.url.includes("/ws/pty?id=p1"));
const before = ws.sent.length;
const MiB = 1 << 20;
h.terms[0]._data("x".repeat(3 * MiB + 5));
const frames = ws.sent.slice(before);
assert.strictEqual(frames.length, 4, "three and a bit mebibytes went as " + frames.length + " frames");
assert.ok(frames.every((f) => typeof f !== "string" && f.byteLength <= MiB), "a frame was bigger than a mebibyte");
assert.strictEqual(frames.reduce((n, f) => n + f.byteLength, 0), 3 * MiB + 5, "some of the paste went missing");
h.terms[0]._data("ls\r");
assert.strictEqual(new TextDecoder().decode(ws.sent.pop()), "ls\r", "a keystroke is not one frame any more");
`)
}

// The one close pty.go gives for a reason retrying can never fix -- its
// end-to-end handshake failing, which by design never falls back to
// plaintext for whoever is in the middle to strip -- covers the terminal
// with why, instead of reconnecting forever with the pane left blank and no
// word of what is wrong.
func TestAPtyRefusedForEndToEndFailureIsNotRetried(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const before = h.sockets.length;
const ws = h.sockets.find((s) => s.url.includes("/ws/pty?id=p1"));
ws.onopen();
ws.onclose({ code: 1008, reason: "end-to-end handshake failed" });
await h.sleep(50);
assert.strictEqual(h.sockets.length, before, "a refused handshake reconnected anyway");
const box = h.terms[0].host.parentElement.querySelector("div.pane-error");
assert.ok(box, "nothing said the connection was refused");
assert.match(box.textContent, /end-to-end handshake failed/);

// Pressing Try again does what its name says, and only then.
const retry = [...box.querySelectorAll("button")].find((b) => b.textContent === "Try again");
assert.ok(retry, "no way to try again was offered");
h.click(retry);
assert.strictEqual(h.sockets.length, before + 1, "Try again did not open a fresh socket");
assert.equal(h.terms[0].host.parentElement.querySelector("div.pane-error"), null, "the cover stayed up after trying again");
`)
}

// An ordinary drop -- no special code, the tunnel itself blinking or the
// session behind the pane restarting -- is retried exactly as it always
// was, unaffected by the refusal above being left uncovered instead.
func TestAnOrdinaryPtyDropIsStillRetried(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const before = h.sockets.length;
const ws = h.sockets.find((s) => s.url.includes("/ws/pty?id=p1"));
ws.onopen();
ws.onclose({});
await h.sleep(400);
assert.strictEqual(h.sockets.length, before + 1, "an ordinary drop was not reconnected");
assert.equal(h.terms[0].host.parentElement.querySelector("div.pane-error"), null, "an ordinary drop covered the terminal");
`)
}

// Restart on an exited pane's cover had the keyboard, and when the cover went
// with the process back, the keyboard went with it - onto nothing, so what was
// typed next reached no pane until something was clicked.
func TestTheKeyboardGoesBackToATerminalItsRestartBrought(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const exited = fixture({ panes: { p1: pane("p1", { status: "exited" }), p2: pane("p2") } });
const restart = () => h.terms[0].host.parentElement.querySelector("div.pane-error").querySelectorAll("button")
  .find((b) => b.textContent === "Restart");
h.recv(exited);
assert.ok(h.doc.activeElement === restart(), "Restart did not take the keyboard");
h.terms[0].focused = false;
h.click(restart());
assert.deepStrictEqual(h.commands().pop(), { cmd: "restartPane", id: "p1" });
h.recv(fixture());
assert.ok(h.terms[0].focused, "the keyboard went with the cover instead of back to the terminal");

// Only where the cover had it: elsewhere, the keyboard stays where it is.
h.recv(exited);
h.$("rail-toggle").focus();
h.terms[0].focused = false;
h.recv(fixture());
assert.ok(!h.terms[0].focused && h.doc.activeElement === h.$("rail-toggle"), "the cover going took the keyboard from the top bar");
`)
}

// A program can print a link whose text is not its address - an OSC 8
// hyperlink, which is how gh and ls --hyperlink print theirs - and nothing
// here opened one: xterm's own fallback asks with the browser's confirm box
// and then navigates, which in an app window is the application gone. It
// opens the way every other link out of the window does, in a window of its
// own, and only for a web address.
func TestALinkATerminalProgramPrintsOpensOutsideTheWindow(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const opened = [];
h.win.open = (url, target, features) => { opened.push([url, target, features]); return null; };
const link = h.terms[0].options.linkHandler;
assert.ok(link && typeof link.activate === "function", "a terminal link has nothing to open it");
link.activate(new h.Ev("click"), "https://example.com/pull/1");
assert.deepStrictEqual(opened, [["https://example.com/pull/1", "_blank", "noopener,noreferrer"]], "the link did not open in a window of its own");
link.activate(new h.Ev("click"), "javascript:alert(1)");
link.activate(new h.Ev("click"), "file:///C:/Windows/win.ini");
assert.strictEqual(opened.length, 1, "a link that is not a web address was opened");
`)
}

// A pane whose process has gone is covered, and its terminal takes no typing.
// Sent to it - a click on the tab on screen, F6, a dialog closing - the
// keyboard went into the terminal nobody could see, and nothing typed went
// anywhere. It lands on the cover's Restart instead.
func TestTheKeyboardSentToACoveredPaneLandsOnRestart(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.recv(fixture({ panes: { p1: pane("p1", { status: "exited" }), p2: pane("p2") } }));
const restart = h.terms[0].host.parentElement.querySelector("div.pane-error").querySelectorAll("button")
  .find((b) => b.textContent === "Restart");
h.$("rail-toggle").focus();
h.terms[0].focused = false;
// The tab on screen, clicked again, hands the keyboard back to its pane.
h.click(h.$("tab-t1"));
assert.ok(h.doc.activeElement === restart, "the keyboard went to the covered terminal rather than to Restart");
assert.ok(!h.terms[0].focused, "the covered terminal took the keyboard");

// With the process back, the terminal is where it goes again.
h.recv(fixture());
h.$("rail-toggle").focus();
h.click(h.$("tab-t1"));
assert.ok(h.terms[0].focused, "the terminal no longer takes the keyboard once its cover has gone");
`)
}

// Windows high contrast replaces every colour with a system one, and much of
// this window is told apart by colour alone: the chosen row of a list, whether
// a switch is on, the focused pane's border, a pressed toggle, the tab and the
// project on screen, and each pane's status dot all came out like their
// neighbours. Only the focus ring was looked after. Each is drawn in the
// system's own colours there, after the rules it overrides.
func TestHighContrastStillShowsWhatIsChosenAndWhatIsOn(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	forced := mediaBlock(t, css, `\(forced-colors:\s*active\)`)
	for _, want := range []string{
		`\.pal-row\.sel[^{]*\{[^}]*background:\s*Highlight`,
		`\.pick-row\.sel[^{]*\{[^}]*background:\s*Highlight`,
		`\.rev-file\.sel[^{]*\{[^}]*background:\s*Highlight`,
		`\.switch\.on\s*\{[^}]*background:\s*Highlight`,
		`\.switch \.knob\s*\{[^}]*background:\s*ButtonText`,
		`\.pane\.focused\s*\{[^}]*border-color:\s*Highlight`,
		`\[aria-pressed="true"\][^{]*\{[^}]*Highlight`,
		`\.tab\.active[^{]*\{[^}]*Highlight`,
		`\.rail-tile\.current[^{]*\{[^}]*Highlight`,
		`\.dot\s*\{[^}]*forced-color-adjust:\s*none`,
	} {
		if !regexp.MustCompile(want).MatchString(forced) {
			t.Errorf("in high contrast app.css does not give %s", want)
		}
	}
	// Written earlier in the sheet, a rule of the same weight is overridden by
	// the ordinary one after it, and high contrast changes nothing.
	last := strings.LastIndex(css, "@media (forced-colors: active)")
	for _, ordinary := range []string{".switch .knob {", ".pane.focused {", ".pal-row.sel {"} {
		if at := strings.Index(css, ordinary); at < 0 || at > last {
			t.Errorf("%s comes after the high-contrast rules, so it wins over them", ordinary)
		}
	}
}

// F6 and Shift+F6 move the keyboard round the window's parts, and in the prompt
// bar they did nothing: the bar kept every key but Escape and Enter, so the
// only way out of it without the mouse was closing it.
func TestF6LeavesThePromptBar(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
// A wide window, where the rail is on screen rather than folded into a menu.
Object.defineProperty(h.$("rail-toggle"), "offsetParent", { get: () => null });
h.press("promptAll");
assert.ok(h.doc.activeElement === h.$("prompt-input"), "the prompt bar did not take the keyboard");
const ev = h.press("nextRegion");
assert.ok(h.$("rail").contains(h.doc.activeElement), "F6 in the prompt bar went nowhere");
assert.ok(ev.defaultPrevented, "F6 was left to the browser as well");
assert.ok(!h.$("promptbar").hidden, "F6 closed the prompt bar");

h.$("prompt-input").focus();
h.terms[0].focused = false;
h.press("prevRegion");
assert.ok(h.terms[0].focused, "Shift+F6 in the prompt bar did not reach the focused terminal");
`)
}

// The hello comes again with every reconnect, carrying the preferences as they
// are now, and settings left open through the drop went on showing them as
// they were before it: a switch turned off in another window meanwhile still
// read on here.
func TestSettingsLeftOpenFollowTheHelloAfterAReconnect(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("settings");
for (const tab of h.$("settings-tabs").querySelectorAll("button")) {
  if (h.$("set-cursor-blink")) break;
  h.click(tab);
}
assert.ok(h.$("set-cursor-blink"), "no section of the settings has the cursor's blink");
assert.strictEqual(h.$("set-cursor-blink").getAttribute("aria-checked"), "true");
h.hello({ cursorSteady: true });
assert.strictEqual(h.$("set-cursor-blink").getAttribute("aria-checked"), "false",
  "the settings went on saying the cursor blinks after the hello said it does not");
`)
}

// The server answers every change of a preference with all of them, and a
// change made twice in quick succession - the font made bigger twice, the
// blink turned off and on - had the answer to the first arrive after the
// second was made, and put the first back: the font shrank a size, the cursor
// stopped, until the second answer came. A change from somewhere else, once
// this window's own have been answered, is still taken.
func TestAnEchoOfAnEarlierPreferenceDoesNotUndoALaterOne(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const prefs = (over) => ({ type: "prefs", prefs: Object.assign({ helpSeen: true, dismissedTips: [] }, over) });
h.press("fontUp");
h.press("fontUp");
assert.strictEqual(h.terms[0].options.fontSize, 15);
h.recv(prefs({ fontSize: 14 }));
assert.strictEqual(h.terms[0].options.fontSize, 15, "the answer to the first change put the font back a size");
h.recv(prefs({ fontSize: 15 }));
assert.strictEqual(h.terms[0].options.fontSize, 15);
h.recv(prefs({ fontSize: 16 }));
assert.strictEqual(h.terms[0].options.fontSize, 16, "a size chosen in another window was not taken");

h.press("settings");
for (const tab of h.$("settings-tabs").querySelectorAll("button")) {
  if (h.$("set-cursor-blink")) break;
  h.click(tab);
}
h.click(h.$("set-cursor-blink"));
h.click(h.$("set-cursor-blink"));
assert.deepStrictEqual(h.commands().slice(-2), [{ cmd: "cursorBlink", kind: "off" }, { cmd: "cursorBlink", kind: "on" }]);
h.recv(prefs({ fontSize: 16, cursorSteady: true }));
assert.strictEqual(h.terms[0].options.cursorBlink, true, "the answer to turning the blink off stopped the cursor again");
assert.strictEqual(h.$("set-cursor-blink").getAttribute("aria-checked"), "true", "the switch went back to off");
h.recv(prefs({ fontSize: 16, cursorSteady: false }));
assert.strictEqual(h.terms[0].options.cursorBlink, true);
`)
}

// Every pane had a WebGL renderer of its own, in every tab, and Chromium keeps
// about sixteen WebGL contexts before it takes the oldest away: with a dozen
// agents across the tabs, panes on screen lost theirs to panes nobody could
// see. Only the panes of the tab on screen have one; a pane going out of sight
// gives its up, and gets one again when it comes back.
func TestOnlyThePanesOnScreenDrawWithWebGL(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const live = (i) => h.webgls.filter((g) => g.term === h.terms[i] && !g.disposed).length;
// Giving a pane a context is real GPU work, so it is asked for rather than
// done outright: see TestATabSwitchBuildsWebGLOnePaneAtATime for that.
h.settleWebgl();
assert.strictEqual(live(0), 1, "the pane on screen draws without WebGL");
assert.strictEqual(live(1), 0, "a pane in a tab out of sight holds a WebGL renderer");

h.recv(fixture({ activeTab: "t2" }));
assert.strictEqual(live(0), 0, "a pane that went out of sight kept its WebGL renderer");
h.settleWebgl();
assert.strictEqual(live(1), 1, "a pane that came on screen draws without WebGL");
h.recv(fixture({ activeTab: "t2" }));
h.settleWebgl();
assert.strictEqual(h.webgls.length, 2, "a push that changed nothing made another renderer");

h.recv(fixture());
h.settleWebgl();
assert.strictEqual(live(0), 1, "a pane back on screen did not get WebGL again");

// A renderer whose context the browser takes away is given up, and the pane
// gets another the next time it comes on screen.
h.webgls.find((g) => g.term === h.terms[0] && !g.disposed)._lost();
assert.strictEqual(live(0), 0, "a renderer that lost its context was kept");
h.recv(fixture({ activeTab: "t2" }));
h.recv(fixture());
h.settleWebgl();
assert.strictEqual(live(0), 1, "a pane whose renderer lost its context never got another");
`)
}

// Creating a WebGL context is real work for the GPU driver, shader
// compilation included, and every pane of a tab paid for one synchronously
// on every switch: with several panes fanned out, that made the switch
// itself - which page is on screen, the one thing about a switch that has
// to be instant - wait on the driver. So a pane's context is asked for
// here, but built one at a time, starting with whichever pane will be
// focused, well after the switch itself has already gone to screen; and a
// pane that leaves the queue again before its turn comes is never built.
func TestATabSwitchBuildsWebGLOnePaneAtATime(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const fan = {};
for (let i = 0; i < 4; i++) fan["f" + i] = pane("f" + i, { name: "task " + i });
const withFanOut = (active) => fixture({
  activeTab: active,
  tabs: [
    { id: "t1", title: "one", focus: "p1", zoom: false, attention: false, root: leaf("n1", "p1") },
    { id: "t2", title: "fan out", focus: "f2", zoom: false, attention: false,
      root: split("h", [leaf("n0", "f0"), leaf("n1x", "f1"), leaf("n2x", "f2"), leaf("n3x", "f3")]) },
  ],
  panes: Object.assign({ p1: pane("p1") }, fan),
});
// h.terms[0] is p1's, the tab shown first; h.terms[1..4] are f0..f3's, in
// the order the fan-out's split lists them.
h.recv(withFanOut("t1"));
assert.strictEqual(h.terms.length, 5, "one terminal per pane");
const live = (i) => h.webgls.filter((g) => g.term === h.terms[i] && !g.disposed).length;
h.settleWebgl();
assert.strictEqual(live(0), 1, "the one pane on screen never drew with WebGL");

h.recv(withFanOut("t2"));
// The push that switched tabs is what shows the fan-out on screen; nothing
// about that waited for a single WebGL context to be built.
assert.strictEqual(live(0), 0, "the pane that left screen kept its WebGL renderer");
for (let i = 1; i <= 4; i++) assert.strictEqual(live(i), 0, "pane " + i + " already drew with WebGL when the switch itself had only just gone to screen");

// The first frame only lets the switch's own paint happen; the pane about
// to be focused (f2, h.terms[3]) is the first one actually built.
h.raf();
for (let i = 1; i <= 4; i++) assert.strictEqual(live(i), 0, "pane " + i + " was built before the tab had even had a frame to paint");
h.raf();
assert.strictEqual(live(3), 1, "the pane about to be focused was not the first one built");
assert.ok([1, 2, 4].some((i) => live(i) === 0), "every pane of the fan-out was built together rather than staggered");

// Given the frames it asks for, every pane on screen ends up with one.
h.settleWebgl();
for (let i = 1; i <= 4; i++) assert.strictEqual(live(i), 1, "pane " + i + " never got a WebGL context");

// Switching away and back before a pane's turn comes never builds it: the
// fan-out is left, entered and left again with no frame run in between, so
// none of its panes' queued contexts should ever be built.
h.recv(withFanOut("t1"));
h.recv(withFanOut("t2"));
h.recv(withFanOut("t1"));
h.settleWebgl();
for (let i = 1; i <= 4; i++) assert.strictEqual(live(i), 0, "pane " + i + " was built after it left screen before its turn came");
assert.strictEqual(live(0), 1, "the pane actually left on screen did not get WebGL again");
`)
}

// The agents overview is asked for again whenever a push shows the counts it
// lists moving, and with agents busy in several projects that is several times
// a second, each answer rebuilding the whole list under the pointer and the
// keyboard. The first change is asked for at once; after that it is at most
// once a second, and the last change is not lost.
func TestTheAgentsOverviewIsAskedForAtMostOnceASecond(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("agents");
const asked = () => h.commands().filter((c) => c.cmd === "agents").length;
const opened = asked();
assert.ok(opened >= 1, "opening the overview did not ask for it");
for (let i = 1; i <= 5; i++) {
  h.recv(fixture({ working: i, projects: [{ root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: i }] }));
}
assert.strictEqual(asked(), opened + 1, "the overview was asked for again on every push");
await h.sleep(1150);
assert.strictEqual(asked(), opened + 2, "the last change was not asked for once the second had passed");
`)
}

// With screen reader support on, xterm keeps a live region in every terminal
// and makes each one assertive, so in a tab of several agents they all read out
// what they were sent at once, over each other and over what was being typed.
// Only the focused pane's terminal speaks; the others' are off until the
// keyboard goes to them.
func TestOnlyTheFocusedTerminalSpeaks(t *testing.T) {
	runFrontEnd(t, `
h.hello({ screenReader: true });
const both = (focus) => fixture({ tabs: [{ id: "t1", title: "one", focus, zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }] });
h.recv(both("p1"));
// xterm puts a live region in each terminal it reads out.
const regions = h.terms.map((t) => {
  const r = h.doc.createElement("div");
  r.className = "live-region";
  r.setAttribute("aria-live", "assertive");
  t.host.append(r);
  return r;
});
h.recv(both("p1"));
assert.strictEqual(regions[0].getAttribute("aria-live"), "assertive", "the focused terminal no longer speaks");
assert.strictEqual(regions[1].getAttribute("aria-live"), "off", "a terminal without the keyboard reads out what it is sent");
h.recv(both("p2"));
assert.strictEqual(regions[0].getAttribute("aria-live"), "off", "the terminal the keyboard left went on speaking");
assert.strictEqual(regions[1].getAttribute("aria-live"), "assertive", "the terminal the keyboard went to does not speak");
`)
}

// A window reached through the relay was told, when its connection went, that
// the flockdeck process was no longer running and to start it again - advice
// for somebody at the machine. From a phone or another computer it is the
// machine, or the relay, that is out of reach, and that is what it says.
func TestARelayWindowsDisconnectedPanelSpeaksOfTheMachine(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.controls().pop().close();
assert.ok(/start it again/i.test(h.$("disconnected-desc").textContent), "a window at the desk is no longer told to start flockdeck");

// Through the relay the page lives under the machine's own prefix, and says
// so before any hello has arrived.
const r = boot({ pathname: "/m/desk/" });
r.controls().pop().close();
const said = r.$("disconnected-desc").textContent;
assert.ok(!r.$("disconnected").hidden, "the panel did not go up");
assert.ok(!/start it again|no longer running/i.test(said), "a window reached through the relay was told to start flockdeck: " + said);
assert.ok(/relay/i.test(said) && /machine/i.test(said), "the panel does not say the machine or the relay is out of reach: " + said);
`)
}

// A tab's close button was inside the tab, a control inside a control: a
// screen reader read it as part of the tab, and some could not reach it at
// all. It sits beside the tab, in a box that is nothing to a screen reader,
// and still closes its own tab; a click on the tab still selects it.
func TestATabsCloseButtonSitsBesideIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const tab = h.$("tab-t1");
assert.strictEqual(tab.getAttribute("role"), "tab");
assert.ok(!tab.querySelector("button"), "the tab holds its close button, a control inside a control");
const box = tab.parentElement;
assert.ok(box.parentElement === h.$("tabs"), "the tab's box is not in the strip");
assert.strictEqual(box.getAttribute("role"), "presentation", "the box around the tab and its close button is something to a screen reader");
const close = box.querySelector(".close");
assert.ok(close && close.parentElement === box, "the close button is not beside the tab");
h.click(close);
assert.deepStrictEqual(h.commands().pop(), { cmd: "closeTab", id: "t1" });
h.click(h.$("tab-t2"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectTab", id: "t2" });
`)
}

// Alt held while digits are typed on the keypad is how Windows types a
// character by its code: Alt+0233 is é. The keypad was read as Alt+1 … Alt+9,
// so typing é switched to tab 2 and then tab 3, and the character never
// arrived.
func TestAltCodesOnTheKeypadAreNotTabNumbers(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const before = h.commands().length;
for (const d of ["0", "2", "3", "3"]) {
  const ev = h.key({ key: d, code: "Numpad" + d, altKey: true });
  assert.ok(!ev.defaultPrevented, "Alt+Numpad" + d + " was kept from the input method");
}
assert.deepStrictEqual(h.commands().slice(before), [], "typing an Alt code switched tabs");
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
assert.deepStrictEqual(h.commands().pop(), { cmd: "sendPrompt", id: "p1", text: "\u4f60\u597d" });

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
  { cmd: "commit", path: "C:/repo", text: "webui: a message worth keeping", push: false, files: ["a.go"], omitted: 0 });

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
const tab = h.$("tab-t1");
assert.ok((tab.dataset.tip || "").startsWith(long), "the whole title cannot be read anywhere");
assert.ok(/rename/.test(tab.dataset.tip), "nothing says how to rename the tab");

h.recv(fixture());
assert.ok(h.$("tab-t1").dataset.tip.startsWith("one"), "the bubble kept the old title");
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
await h.sleep(200); // past the gap a diff asked for straight after another waits out
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

// A worktree with agents working in it is not removed from under them: the
// server refuses it, forced or not, and the dialog says so before sending
// anything, naming what to do instead. Force is only for uncommitted work in
// a worktree nothing is running in.
func TestAWorktreeAgentsAreInIsNotRemovedFromUnderThem(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [], items: [
  { label: "main", path: "C:/repo", main: true },
  { label: "fix-auth", path: "C:/repo-fix-auth", panes: 2, dirty: 3, untracked: 0 },
  { label: "spare", path: "C:/repo-spare", panes: 0, dirty: 1, untracked: 0 },
] });
const removes = h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Remove");
let asked = "";
h.win.confirm = (q) => { asked = q; return true; };
const before = h.commands().length;
h.click(removes[0]);
assert.strictEqual(h.commands().length, before, "a worktree was removed from under the agents working in it");
assert.strictEqual(asked, "", "the dialog offered to go ahead past the agents working in it");
assert.ok(/2 agents are working in fix-auth\. Close those panes first/.test(h.$("notice").textContent),
  "nothing said why, or what to do: " + h.$("notice").textContent);

h.click(removes[1]);
assert.ok(/uncommitted changes/.test(asked), "uncommitted work was discarded without asking");
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeRemove", path: "C:/repo-spare", force: true });
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
paletteRun("rename");
assert.ok(!h.$("overlay").hidden, "the rename dialog did not open");
const field = h.$("tab-name");
assert.ok(h.doc.activeElement === field, "the name field does not have the keyboard");
assert.strictEqual(field.selectionEnd - field.selectionStart, field.value.length,
  "the old name is not selected, so typing adds to it");
field.value = "api work";
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "renameTab", id: "t1", text: "api work" });
assert.ok(h.$("overlay").hidden, "the dialog stayed open once the tab was renamed");
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
assert.deepStrictEqual(h.commands().pop(), { cmd: "commit", path: "C:/repo", text: "webui: something", push: false, files: ["a.go"], omitted: 0 });
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
h.recv({ type: "diff", cwd: "C:/repo", file: "f20.go", text: Array.from({ length: 500 }, (_, i) => "+line " + i).join("\n") });
const diff = () => h.$("overlay-body").querySelector("div.rev-diff");
const list = () => h.$("overlay-body").querySelector("div.rev-files");
diff().scrollTop = 900;
list().scrollTop = 400;

h.recv(tree);
assert.strictEqual(diff().scrollTop, 900, "the diff went back to its first line when the tree was read again");
assert.strictEqual(list().scrollTop, 400, "the file list went back to its top when the tree was read again");
`)
}

// The review reads the tree again when an agent changes it, and the list came
// back current while the diff beside it - of the file being read - stayed as
// it was first read.
func TestTheDiffOnShowFollowsTheTree(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const tree = (added) => ({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "a.go", label: "M", added, removed: 0 }, { path: "b.go", label: "M", added: 1, removed: 0 }] });
h.recv(tree(1));
h.click(h.$("overlay-body").querySelectorAll("div.rev-file")[0]);
const lines = (n) => Array.from({ length: n }, (_, i) => "+line " + i).join("\n");
h.recv({ type: "diff", cwd: "C:/repo", file: "a.go", text: lines(300) });
const diff = () => h.$("overlay-body").querySelector("div.rev-diff");
diff().scrollTop = 900;
const diffs = () => h.commands().filter((c) => c.cmd === "diff");
const before = diffs().length;

h.recv(tree(40));
assert.strictEqual(diffs().length, before + 1, "the tree changed and the diff on show was not read again");
assert.deepStrictEqual(diffs().pop(), { cmd: "diff", path: "C:/repo", text: "a.go" });
assert.ok(!/Loading/.test(diff().textContent), "the diff on show was taken away while the new one was read");
h.recv({ type: "diff", cwd: "C:/repo", file: "a.go", text: lines(340) });
assert.ok(/line 339/.test(diff().textContent), "the new diff is not shown");
assert.strictEqual(diff().scrollTop, 900, "the new diff lost the reader's place");

// Another file chosen starts at its top.
h.click(h.$("overlay-body").querySelectorAll("div.rev-file")[1]);
h.recv({ type: "diff", cwd: "C:/repo", file: "b.go", text: lines(300) });
assert.strictEqual(diff().scrollTop, 0, "a file just chosen did not start at its top");
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

// A split adds an idle pane and closing one in a tab of several takes one
// away, and neither moved the tabs, waiting or working counts the overview was
// asked for again by: the open list went stale, and a closed pane's row
// answered a click with an error.
func TestTheAgentsOverviewFollowsASplit(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const projects = (panes) => [{ root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0, panes }];
h.recv(fixture({ projects: projects(2) }));
h.click(h.$("summary"));
const asked = () => h.commands().filter((c) => c.cmd === "agents").length;
const before = asked();
h.recv(fixture({ projects: projects(3) }));
assert.strictEqual(asked(), before + 1, "a split did not bring the overview up to date");
h.recv(fixture({ projects: projects(2) }));
// Asked for at most once a second, so the second change waits for it.
await h.sleep(1150);
assert.strictEqual(asked(), before + 2, "a closed pane did not bring the overview up to date");
`)
}

// mediaBlock returns the bodies of every @media rule whose condition matches
// cond, braces balanced and joined, so a test can look for what the style
// sheet does in that case and nowhere else. The rail and the dialogs each
// keep their narrow rules beside their other rules, so there are several.
func mediaBlock(t *testing.T, css, cond string) string {
	t.Helper()
	var out strings.Builder
	for _, loc := range regexp.MustCompile(`@media\s*`+cond+`\s*\{`).FindAllStringIndex(css, -1) {
		depth := 1
		for i := loc[1]; i < len(css) && depth > 0; i++ {
			switch css[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					out.WriteString(css[loc[1]:i])
					out.WriteString("\n")
				}
			}
		}
		if depth != 0 {
			t.Fatalf("an @media %s rule in app.css is not closed", cond)
		}
	}
	if out.Len() == 0 {
		t.Fatalf("app.css has no @media %s rule", cond)
	}
	return out.String()
}

// This page is opened on phones through the relay. At 400px a column of icons
// down the left would take an eighth of the terminal's width, and the old bar
// ran off the edge with the help button on it. There the rail folds into a
// menu opened from the top bar, where each button says what it is - a touch
// screen has no hover to show a tooltip - and the tabs get a row of their own;
// on a touch screen every control in either is at least a fingertip wide.
func TestANarrowScreenFoldsTheRailIntoAMenu(t *testing.T) {
	css := readAsset(t, "app.css")
	narrow := mediaBlock(t, css, `\(max-width:\s*640px\)`)
	for _, want := range []string{
		`#rail\s*\{[^}]*position:\s*fixed[^}]*transform:\s*translateX\(-100%\);\s*visibility:\s*hidden`,
		`body\.rail-open #rail\s*\{[^}]*transform:\s*none;\s*visibility:\s*visible`,
		`#rail-toggle\s*\{\s*display:\s*flex`,
		`body\.rail-open #rail-toggle \.rail-badge\s*\{\s*display:\s*none`,
		`#topbar\s*\{[^}]*flex-wrap:\s*wrap`,
		`#topbar > #tabbar\s*\{[^}]*flex:\s*1 1 100%`,
		`\.rail-btn \.rail-label\s*\{[^}]*position:\s*static`,
	} {
		if !regexp.MustCompile(want).MatchString(narrow) {
			t.Errorf("a narrow screen does not get %s", want)
		}
	}
	coarse := mediaBlock(t, css, `\(pointer:\s*coarse\)`)
	for _, want := range []string{
		`\.rail-btn\s*\{\s*width:\s*44px;\s*height:\s*44px`,
		`\.bar-btn, #summary, \.commands, \.update, \.tab\s*\{\s*min-height:\s*44px`,
		`\.bar-btn, \.commands\s*\{\s*min-width:\s*44px`,
		`\.tab \.close\s*\{\s*width:\s*44px;\s*height:\s*44px`,
	} {
		if !regexp.MustCompile(want).MatchString(coarse) {
			t.Errorf("a touch screen does not get %s", want)
		}
	}

	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 1, waiting: 1, working: 0 },
] }));
const toggle = h.$("rail-toggle");
const open = () => h.doc.body.classList.contains("rail-open");
assert.strictEqual(toggle.getAttribute("aria-controls"), "rail");
assert.ok(toggle.getAttribute("aria-label"), "the menu button has no name");

h.click(toggle);
assert.ok(open(), "the menu did not open");
assert.strictEqual(toggle.getAttribute("aria-expanded"), "true");
const tiles = h.$("rail-projects").children;
assert.ok(h.doc.activeElement === tiles[0], "the keyboard is not on the project on screen");
h.key({ key: "Tab" });
assert.ok(h.$("rail").contains(h.doc.activeElement), "Tab walked out of the open menu");
h.key({ key: "Escape" });
assert.ok(!open(), "Escape left the menu open");
assert.strictEqual(toggle.getAttribute("aria-expanded"), "false");
assert.ok(h.doc.activeElement === toggle, "the keyboard did not go back to the menu button");

h.click(toggle);
h.click(tiles[1]);
assert.ok(!open(), "choosing a project left the menu open");
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectProject", root: "C:/api" });
h.click(toggle);
h.click(h.$("btn-help"));
assert.ok(!open(), "opening a dialog from the menu left the menu open");
assert.ok(!h.$("overlay").hidden, "Help did not open from the menu");
h.key({ key: "Escape" });
h.click(toggle);
h.click(h.$("rail-scrim"));
assert.ok(!open(), "a tap beside the menu left it open");
`)
}

// The menu opens with the keyboard on the project on screen, and keyboard
// focus brings a tooltip - which, over the folded rail, lay across the tiles
// below and hid the projects the menu had been opened to show, while saying
// nothing the menu's own words did not. Unfolded, the rail's icons still
// explain themselves in a bubble.
func TestTheRailMenuRaisesNoTooltipOverItself(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 1, waiting: 1, working: 0 },
] }));
const tip = () => h.doc.body.querySelector("div.tip");
const focus = (b) => { b.focus(); h.dispatch(b, new h.Ev("focusin", { target: b })); };

h.click(h.$("rail-toggle"));
const tile = h.$("rail-projects").children[0];
focus(tile);
await h.sleep(320);
assert.ok(!tip(), "a bubble opened over the menu: " + (tip() && tip().textContent));
focus(h.$("btn-history"));
await h.sleep(320);
assert.ok(!tip(), "a bubble opened over the menu's History button");

h.key({ key: "Escape" });
focus(h.$("btn-history"));
await h.sleep(320);
assert.ok(tip(), "the unfolded rail's History button no longer explains itself");
`)
}

// The rail is the open projects, one tile each: two letters, the whole name
// and folder in the tooltip and the accessible name, the project on screen
// marked, and an amber badge where an agent is waiting on you - which is what
// the rail is for, since it is seen from any project without opening
// anything. A click switches; the tiles outlive the pushes, as the tabs do.
func TestTheRailShowsTheOpenProjects(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const projects = [
  { root: "C:/code/agent-wrapper", name: "agent-wrapper", active: true, tabs: 2, waiting: 0, working: 1 },
  { root: "C:/code/flockdeck-relay", name: "flockdeck-relay", active: false, tabs: 1, waiting: 0, working: 0 },
  { root: "C:/code/flockdeck-remote", name: "flockdeck-remote", active: false, tabs: 1, waiting: 2, working: 0 },
];
h.recv(fixture({ root: "C:/code/agent-wrapper", projects,
  panes: { p1: pane("p1", { cwd: "C:/code/agent-wrapper", branch: "main" }), p2: pane("p2", { cwd: "C:/wt/fix", branch: "fix" }) } }));
const tiles = () => h.$("rail-projects").children;
assert.strictEqual(tiles().length, 3, "one tile per open project");
assert.deepStrictEqual(tiles().map((t) => t.querySelector(".mono").textContent), ["AW", "FR", "FM"]);
const [aw, fr, fm] = tiles();
assert.ok(aw.classList.contains("current") && aw.getAttribute("aria-current") === "true", "the project on screen is not marked");
assert.ok(!fr.classList.contains("current") && !fr.hasAttribute("aria-current"), "a project not on screen is marked");
const name = aw.getAttribute("aria-label");
assert.ok(name.includes("agent-wrapper") && name.includes("C:/code/agent-wrapper"), "the tile's name lacks the project or its folder: " + name);
assert.ok((aw.dataset.tip || "").includes("C:/code/agent-wrapper"), "the tooltip does not give the folder");
const badged = (t) => t.querySelector(".rail-badge").classList.contains("waiting");
assert.ok(badged(fm) && !badged(aw) && !badged(fr), "only the project with an agent waiting carries the badge");
assert.ok(fm.getAttribute("aria-label").includes("2 agents are waiting on you"), "the badge is not in the tile's name");

h.click(fr);
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectProject", root: "C:/code/flockdeck-relay" });
const before = h.commands().length;
h.terms.forEach((t) => t.blur());
h.click(aw);
assert.strictEqual(h.commands().length, before, "clicking the project on screen sent something");
assert.ok(h.terms.some((t) => t.focused), "clicking the project on screen did not hand the keyboard to its terminal");

h.recv(fixture({ projects: [
  Object.assign({}, projects[0], { active: false }),
  Object.assign({}, projects[1], { active: true }),
  Object.assign({}, projects[2], { waiting: 0 }),
] }));
assert.ok(tiles()[0] === aw && tiles()[1] === fr && tiles()[2] === fm, "a push rebuilt the tiles");
assert.ok(fr.classList.contains("current") && !aw.classList.contains("current"), "the mark did not follow the switch");
assert.ok(!badged(fm), "the badge stayed after the agent was answered");
h.recv(fixture({ projects: [projects[0]] }));
assert.strictEqual(tiles().length, 1, "a closed project kept its tile");
`)
}

// On a narrow window the rail is out of sight until its toggle is pressed, so
// its tiles' own badges - the rail's usual way of saying an agent is waiting
// in a project that is not on screen - are out of sight with it. The toggle
// carries the same amber dot itself, so the one thing the rail is most for is
// not lost to a narrow window. An agent waiting in the project already open
// does not count: its tab already shows that without opening anything.
func TestTheRailToggleBadgesWhenAnotherProjectWaits(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const badge = () => h.$("rail-toggle").querySelector(".rail-badge");
const tip = () => h.$("rail-toggle").dataset.tip || "";

h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 1, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 1, waiting: 0, working: 0 },
] }));
assert.ok(!badge().classList.contains("waiting"), "the project on screen's own waiting agent lit the toggle");
assert.ok(!/waiting/.test(tip()), "the tooltip mentioned waiting with nothing waiting elsewhere: " + tip());

h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 1, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 1, waiting: 1, working: 0 },
] }));
assert.ok(badge().classList.contains("waiting"), "an agent waiting in another project did not light the toggle");
assert.ok(/waiting on you in api/.test(tip()), "the toggle does not say where: " + tip());

h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 1, waiting: 1, working: 0 },
  { root: "C:/web", name: "web", active: false, tabs: 1, waiting: 2, working: 0 },
] }));
assert.ok(/waiting on you in api, web/.test(tip()), "two other projects waiting are not both named: " + tip());

h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 1, waiting: 0, working: 0 },
] }));
assert.ok(!badge().classList.contains("waiting"), "the badge stayed after every other project was answered");
assert.ok(!/waiting/.test(tip()), "the tooltip kept saying an agent was waiting");
`)
}

// Two letters are few, and projects are often named alike: flockdeck-relay and
// flockdeck-remote, two checkouts of one repository. No two tiles may show the
// same letters, or the rail stops saying which project is which.
func TestTheRailsMonogramsStayDistinct(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const names = ["agent-wrapper", "flockdeck-relay", "flockdeck-remote", "flockdeck-site", "FlippingRS", "jmwri", "repos", "api", "api"];
h.recv(fixture({ projects: names.map((name, i) =>
  ({ root: "C:/p" + i + "/" + name, name, active: i === 0, tabs: 1, waiting: 0, working: 0 })) }));
const monos = h.$("rail-projects").children.map((t) => t.querySelector(".mono").textContent);
assert.deepStrictEqual(monos, ["AW", "FR", "FM", "FS", "FL", "JM", "RE", "AP", "AI"]);
assert.strictEqual(new Set(monos).size, monos.length, "two projects share a monogram");
`)
}

// Every control that moved into the rail kept its id and what it does, so the
// keys, the palette and every test that presses one still reach it; each says
// its name and its key; and the rail is one stop for Tab, walked with the
// arrows, as the tab strip and the pane toolbars are.
func TestTheRailKeepsTheControlsAndTheirKeys(t *testing.T) {
	runFrontEnd(t, `
const keys = h.hello();
h.recv(fixture());
for (const id of ["new-tab", "new-tab-pick", "summary", "btn-agents", "btn-broadcast", "btn-changes",
  "btn-history", "btn-worktrees", "btn-apikeys", "btn-update", "btn-remote", "btn-help", "btn-settings",
  "btn-palette", "rail-open"]) {
  assert.ok(h.$(id), id + " is gone");
}
const rail = h.$("rail");
const tools = { "btn-agents": "agents", "btn-broadcast": "toggleBroadcast", "btn-changes": "changes",
  "btn-history": "history", "btn-worktrees": "worktrees", "btn-apikeys": "apiKeys", "btn-help": "help",
  "btn-settings": "settings", "rail-open": "projects", "btn-palette": "palette" };
for (const [id, action] of Object.entries(tools)) {
  const k = keys.find((x) => x.id === action);
  const b = h.$(id);
  assert.strictEqual(rail.contains(b), id !== "btn-palette", id + " is not where the layout puts it");
  assert.ok((b.dataset.tip || "").includes(k.keys), id + "'s tooltip does not give its key: " + b.dataset.tip);
  assert.ok(b.textContent.trim() || b.getAttribute("aria-label"), id + " has no name");
}

// Commands shows the palette's key, from the table, and opens it.
const pk = keys.find((x) => x.id === "palette");
assert.deepStrictEqual(h.$("palette-keys").children.map((n) => n.textContent), pk.keys.split("+"));
h.click(h.$("btn-palette"));
assert.ok(!h.$("palette").hidden, "Commands did not open the palette");
h.key({ key: "Escape" });

h.recv(fixture({ broadcast: true }));
assert.ok(h.$("btn-broadcast").classList.contains("on"), "broadcast is on and its button does not say so");
assert.strictEqual(h.$("btn-broadcast").getAttribute("aria-pressed"), "true");

const buttons = () => rail.querySelectorAll("button");
const stops = buttons().filter((b) => b.getAttribute("tabindex") !== "-1");
assert.strictEqual(stops.length, 1, "the rail is " + stops.length + " stops for Tab");
stops[0].focus();
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement !== stops[0] && rail.contains(h.doc.activeElement), "ArrowDown did not move within the rail");
assert.strictEqual(h.doc.activeElement.getAttribute("tabindex"), "0", "the stop for Tab did not follow the arrows");
h.key({ key: "End" });
assert.ok(h.doc.activeElement === buttons().pop(), "End did not reach the foot of the rail");
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === buttons()[0], "the arrows do not come round");

h.click(h.$("btn-history"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "conversations" });
h.key({ key: "Escape" });
h.click(h.$("rail-open"));
assert.ok(!h.$("overlay").hidden, "Open a project did not open the projects");
assert.ok(h.doc.activeElement === h.$("browse-path"), "Open a project did not put the keyboard on the folder");
`)
}

// The rail starts as icons alone, the shape it has always had. Widened, it
// is a panel of icons and names instead, at whatever width a drag or the
// keyboard last left it, and both survive a restart with the other
// preferences — kept on the Go side, since the browser forgets local storage
// on the fresh port every run binds.
func TestTheRailCanBeWidenedAndResized(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const rail = h.$("rail"), collapse = h.$("rail-collapse"), resize = h.$("rail-resize");
assert.strictEqual(h.doc.body.classList.contains("rail-expanded"), false, "the rail starts widened");
assert.strictEqual(collapse.getAttribute("aria-pressed"), "false");
assert.strictEqual(rail.style.width, "", "a collapsed rail carries a width of its own");

h.click(collapse);
assert.deepStrictEqual(h.commands().pop(), { cmd: "railExpanded", kind: "on" });
assert.ok(h.doc.body.classList.contains("rail-expanded"), "clicking the collapse button did not widen the rail");
assert.strictEqual(collapse.getAttribute("aria-pressed"), "true");
assert.strictEqual(rail.style.width, "220px", "a rail nobody has resized is not the default width");

// Ctrl+B, from the key table rather than written out here, folds it back.
h.press("toggleRail");
assert.deepStrictEqual(h.commands().pop(), { cmd: "railExpanded", kind: "off" });
assert.ok(!h.doc.body.classList.contains("rail-expanded"), "the binding did not fold the rail back");
assert.strictEqual(rail.style.width, "", "folded, the rail kept the width it had been widened to");

h.press("toggleRail");
h.commands();

// The keyboard steps the width once the handle has it, and stops short of
// what the Go side would refuse.
assert.strictEqual(resize.getAttribute("role"), "separator");
assert.strictEqual(resize.getAttribute("aria-orientation"), "vertical");
resize.focus();
h.key({ key: "ArrowRight" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "railWidth", size: 236 });
assert.strictEqual(rail.style.width, "236px");
assert.strictEqual(resize.getAttribute("aria-valuenow"), "236");
for (let i = 0; i < 30; i++) h.key({ key: "ArrowRight" });
assert.strictEqual(rail.style.width, "480px", "the keyboard widened the rail past its maximum");
for (let i = 0; i < 40; i++) h.key({ key: "ArrowLeft" });
assert.strictEqual(rail.style.width, "180px", "the keyboard narrowed the rail past its minimum");

// Dragging redraws the rail as the pointer moves, and tells the Go side only
// once, when the drag is let go — as a split's own divider already does.
h.dispatch(resize, new h.Ev("pointerdown", { button: 0, pointerId: 1, clientX: 300 }));
assert.ok(resize.classList.contains("dragging"), "the handle did not mark itself as being dragged");
assert.ok(h.doc.body.classList.contains("rail-resizing"), "the body did not say a drag was under way");
const before = h.commands().length;
h.dispatch(resize, new h.Ev("pointermove", { pointerId: 1, clientX: 340 }));
assert.strictEqual(rail.style.width, "220px", "the rail did not follow the pointer");
assert.strictEqual(h.commands().length, before, "a move in the middle of the drag told the Go side already");
h.dispatch(resize, new h.Ev("pointerup", { pointerId: 1, clientX: 340 }));
assert.deepStrictEqual(h.commands().pop(), { cmd: "railWidth", size: 220 });
assert.ok(!resize.classList.contains("dragging"), "letting go left the handle marked as dragged");
assert.ok(!h.doc.body.classList.contains("rail-resizing"), "letting go left the body saying a drag was under way");

// What arrives with the preferences is clamped the same way a drag is, and a
// choice made in another window is followed here too.
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [], railExpanded: true, railWidth: 5000 } });
assert.strictEqual(rail.style.width, "480px", "a width from the preferences was not clamped to the maximum");
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [], railExpanded: false, railWidth: 480 } });
assert.ok(!h.doc.body.classList.contains("rail-expanded"), "folding the rail from another window was not followed");
`)
}

// Settings opens from the rail, from the palette and with its key - Ctrl+,,
// which nothing else answers to - and each way shows the one dialog: the
// sections on the left, walked with the arrows and narrowed by typing, and the
// section chosen on the right, where it was left last time.
// The account's section ends with whose Flockdeck is, under what licence, and
// the way to the site's privacy policy, terms and licences, opened in the
// browser as Enterprise's link is rather than in place of the app.
func TestThePlanSaysWhoseItIsAndLinksThePolicies(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-plan"));
const legal = h.$("set-legal");
assert.ok(legal && h.$("settings-pane").contains(legal), "the plan does not say whose Flockdeck is");
assert.ok(legal.textContent.startsWith("Flockdeck · © 2026 Jim Wright · PolyForm Noncommercial licence"), "the copyright line reads " + legal.textContent);
for (const [id, text, page] of [
  ["set-legal-privacy", "Privacy policy", "privacy.html"],
  ["set-legal-terms", "Terms", "terms.html"],
  ["set-legal-licences", "Licences", "licences.html"],
]) {
  const a = h.$(id);
  assert.ok(a && legal.contains(a), "there is no link to the " + text.toLowerCase());
  assert.strictEqual(a.textContent, text);
  assert.strictEqual(a.getAttribute("href"), "https://flockdeck.ai/" + page);
  assert.strictEqual(a.target, "_blank", text + " would open in place of the app");
}
`)
}

// Sponsoring is one quiet line under the plans, and nowhere else in the app:
// a link to GitHub Sponsors that opens in the browser and does nothing more.
// It is not a plan, so it is kept out of the cards; it counts nothing, asks
// nothing, and says that sponsoring buys nothing.
func TestTheSponsorLineOnlyLinks(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const url = "https://github.com/sponsors/jmwri";
const links = () => h.doc.querySelectorAll("a").filter((a) => a.getAttribute("href") === url);
assert.strictEqual(links().length, 0, "the app offers sponsoring before Settings › Account & plan is opened");

h.click(h.$("btn-settings"));
assert.ok(!h.$("set-sponsor"), "the sponsor line is drawn in a section other than Account & plan");
const find = h.$("settings-find");
find.value = "sponsor";
find.oninput();
assert.deepStrictEqual(h.$("settings-tabs").children.map((b) => b.textContent), ["Account & plan"],
  "Find a setting does not lead to the sponsor line");
find.value = "";
find.oninput();
h.click(h.$("settings-tab-plan"));

const line = h.$("set-sponsor"), a = h.$("set-sponsor-link");
assert.ok(line && h.$("settings-pane").contains(line), "Account & plan has no sponsor line");
assert.ok(!line.closest(".plan-card"), "the sponsor line is drawn as part of a plan");
assert.strictEqual(links().length, 1, "sponsoring is offered more than once");
assert.ok(a && line.contains(a) && a.tagName === "A", "the sponsor line is not a link");
assert.strictEqual(a.textContent, "Sponsor Flockdeck");
assert.strictEqual(a.target, "_blank", "the sponsor link would open in place of the app");
assert.ok(/noopener/.test(a.rel), "the sponsor page could reach back into the app: " + a.rel);
assert.ok(!line.querySelector("button"), "the sponsor line has a button");
assert.ok(!/\d/.test(line.textContent), "the sponsor line counts something: " + line.textContent);
assert.ok(line.textContent.includes("buys nothing"), "the sponsor line does not say sponsoring buys nothing: " + line.textContent);
const sent = h.commands().length;
h.click(a);
assert.strictEqual(h.commands().length, sent, "following the sponsor link told the app something");

h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden && links().every((l) => h.$("overlay").contains(l)),
  "the sponsor line stayed on screen when the settings closed");
`)
}

func TestTheSettingsOpenEveryWay(t *testing.T) {
	runFrontEnd(t, `
const keys = h.hello();
h.recv(fixture());
const k = keys.find((x) => x.id === "settings");
assert.ok(k && k.keys, "the action table has no key for the settings");
assert.strictEqual(keys.filter((x) => x.keys === k.keys).length, 1, k.keys + " runs something else as well");
const shown = () => !h.$("overlay").hidden && h.$("overlay-title").textContent === "Settings";
const sections = () => h.$("settings-tabs").children.map((b) => b.textContent);

h.click(h.$("btn-settings"));
assert.ok(shown(), "the rail's Settings button did not open the settings");
assert.deepStrictEqual(sections(), ["General", "Appearance", "Behaviour", "Keybindings", "Agents", "API keys", "Remote access", "GitHub", "Account & plan"]);
assert.strictEqual(h.$("settings-tab-general").getAttribute("aria-selected"), "true");
assert.ok(h.doc.activeElement === h.$("settings-tab-general"), "the keyboard is not on the sections");
assert.ok((h.$("btn-settings").dataset.tip || "").includes(k.keys), "the Settings button does not give its key");
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape left the settings open");

h.press("settings");
assert.ok(shown(), k.keys + " did not open the settings");
h.key({ key: "ArrowDown" });
assert.strictEqual(h.$("settings-tab-appearance").getAttribute("aria-selected"), "true", "the arrows do not walk the sections");
assert.ok(h.doc.activeElement === h.$("settings-tab-appearance"), "the keyboard did not follow the arrows");
assert.ok(h.$("settings-pane").contains(h.$("set-font-size")), "the section reached is not the one shown");
h.key({ key: "Escape" });

h.press("palette");
const input = h.$("palette-input");
input.value = "settings";
input.oninput();
const row = h.$("palette-list").children.find((r) => r.querySelector(".pal-label").textContent === "Settings");
assert.ok(row, "the palette has no Settings");
h.click(row);
assert.ok(shown(), "the palette's Settings did not open the settings");
assert.strictEqual(h.$("settings-tab-appearance").getAttribute("aria-selected"), "true", "the settings did not open where they were left");

const find = h.$("settings-find");
find.value = "relay";
find.oninput();
assert.deepStrictEqual(sections(), ["Remote access", "Account & plan"], "Find a setting did not narrow the sections");
assert.strictEqual(h.$("settings-tab-remote").getAttribute("aria-selected"), "true", "the first section found is not shown");
`)
}

// The window tells the desktop when it has seen real input -- a keystroke, a
// click, a scroll, the pointer moving -- which the desktop falls back on for
// who is at the desk where it cannot read the machine's own idle time.
// Coming to the front or gaining focus says nothing by itself, and input
// moments after the last report is not said again.
func TestTheWindowSaysWhenItIsUsed(t *testing.T) {
	runFrontEnd(t, `
h.hello();
assert.deepStrictEqual(h.presence(), [], "a window said something of itself before any input at all");
h.doc._hasFocus = false;
h.win.dispatchEvent(new h.Ev("blur"));
h.doc._hasFocus = true;
h.win.dispatchEvent(new h.Ev("focus"));
assert.deepStrictEqual(h.presence(), [], "coming to the front or going behind was reported as input");
h.key({ key: "a" });
assert.deepStrictEqual(h.presence().pop(), { cmd: "presence", kind: "used" }, "a keystroke did not say the window was used");
const said = h.presence().length;
h.key({ key: "b" });
assert.strictEqual(h.presence().length, said, "input moments after the last report was said again");
assert.ok(!h.commands().some((c) => c.cmd === "presence"), "what the window says of itself is among the commands");
`)
}

// Settings › Remote access has the switch for push notifications to paired
// devices, how long a wait lasts before they are told, and the option to send
// nothing identifying. Each reaches its setting at once, follows another
// window's, and the relay's refusal is said in its words.
func TestThePushSettingsReachTheirSettings(t *testing.T) {
	runFrontEnd(t, `
h.hello({ fontSize: 13 });
h.recv(fixture({ remote: { state: "connected", relay: "https://relay.example", hostId: "h1", name: "desk", viewers: 0,
  since: "2030-01-01T00:00:00Z", pushError: "the relay said: this relay does not send push notifications" } }));
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-remote"));
const on = (id) => h.$(id).getAttribute("aria-checked") === "true";
assert.ok(on("set-push"), "push notifications are off before anyone turned them off");
assert.ok(!on("set-push-anonymous"), "sending nothing identifying is on before anyone turned it on");
assert.strictEqual(h.$("set-push-delay").value, "30", "the delay is not 30 seconds until it is changed");
const text = h.$("settings-pane").textContent;
assert.ok(text.includes("does not send push notifications"), "the relay's refusal is not said");
assert.ok(text.includes("An agent on desk needs you"), "what an anonymous notification says is not said");

h.click(h.$("set-push"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "pushNotify", kind: "off" });
assert.ok(!on("set-push"), "the switch did not turn off at once");
assert.ok(h.$("set-push-delay").disabled, "the delay can be changed with nothing to delay");
h.click(h.$("set-push-anonymous"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "pushAnonymous", kind: "on" });
assert.ok(on("set-push-anonymous"));

h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [], push: { anonymous: true } } });
assert.ok(on("set-push"), "the switch did not follow notifications turned back on in another window");
const delay = h.$("set-push-delay");
delay.value = "120";
h.dispatch(delay, new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "pushDelay", size: 120 });
assert.strictEqual(h.$("set-push-delay").value, "120");

const find = h.$("settings-find");
find.value = "notifications phone";
find.oninput();
assert.strictEqual(h.$("settings-tab-remote").getAttribute("aria-selected"), "true", "Find a setting does not find push notifications");
`)
}

// Every control in the settings changes the real setting, through the command
// the palette and the keys send; it takes effect at once; and it shows what
// the palette, the keys or another window changed, so there is one state
// however it is changed. A key is never shown, and the plan invents no price.
func TestEachSettingReachesItsSetting(t *testing.T) {
	runFrontEnd(t, relayPromise+`
const keys = h.hello({ fontSize: 13, scrollback: 10000, dismissedTips: ["palette"] });
h.recv(fixture({ update: { version: "9.9.9" } }));
h.click(h.$("btn-settings"));
const pane = h.$("settings-pane");
const on = (id) => h.$(id).getAttribute("aria-checked") === "true";

// General.
assert.ok(on("set-notifications"), "notifications are on and the switch says otherwise");
h.click(h.$("set-notifications"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "notifications", kind: "off" });
assert.ok(!on("set-notifications"), "the switch did not turn off at once");
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: ["palette"], fontSize: 13 } });
assert.ok(on("set-notifications"), "the switch did not follow notifications turned back on in another window");
h.click(h.$("set-updates"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "updates", kind: "off" });
h.press("palette");
const labels = h.$("palette-list").children.map((r) => r.querySelector(".pal-label").textContent);
assert.ok(labels.includes("Turn update checks on"), "the palette does not know the settings turned update checks off");
h.key({ key: "Escape" });
assert.ok(h.$("set-install"), "the update already downloaded is not offered");
h.click(h.$("set-check-update"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "checkForUpdate" });
h.click(h.$("set-tips"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "resetTips" });
assert.ok(h.$("set-tips").disabled, "the hints can still be brought back with none sent away");

// Appearance: theme and accent.
h.click(h.$("settings-tab-appearance"));
assert.ok(on("set-theme-dark"), "dark is not shown as the theme by default");
h.click(h.$("set-theme-light"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "theme", text: "light" });
assert.ok(on("set-theme-light") && !on("set-theme-dark"), "the theme choice did not follow the click");
assert.strictEqual(h.doc.documentElement.dataset.theme, "light", "the document did not take the theme");
h.click(h.$("set-accent-purple"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "accentColor", text: "purple" });
assert.strictEqual(h.doc.documentElement.dataset.accent, "purple", "the document did not take the accent");
h.click(h.$("set-theme-dark"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "theme", text: "dark" });

// Appearance: terminal.
h.click(h.$("set-font-up"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontSize", size: 14 });
assert.strictEqual(h.$("set-font-size").textContent, "14 px");
assert.ok(h.terms.every((t) => t.options.fontSize === 14), "the terminals did not take the size");
h.press("fontDown");
assert.strictEqual(h.$("set-font-size").textContent, "13 px", "the key and the settings show different sizes");
assert.strictEqual(h.$("set-preview").style.fontSize, "13px", "the preview is not drawn at the terminals' size");
const font = h.$("set-font-family");
font.value = "Fira Code";
h.dispatch(font, new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontFamily", text: "Fira Code" });
assert.ok(h.terms[0].options.fontFamily.startsWith("Fira Code"), "the terminals did not take the font");
assert.ok(h.$("set-preview").style.fontFamily.startsWith("Fira Code"), "the preview did not take the font");
const lines = h.$("set-scrollback");
lines.value = "50000";
h.dispatch(lines, new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "scrollback", size: 50000 });
assert.strictEqual(h.terms[0].options.scrollback, 50000, "the terminals did not take the scrollback");
h.click(h.$("set-cursor-bar"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "cursorStyle", text: "bar" });
assert.strictEqual(h.terms[0].options.cursorStyle, "bar", "the terminals did not take the cursor's shape");
assert.ok(on("set-cursor-bar") && !on("set-cursor-block"), "the shapes do not say which is chosen");
assert.ok(h.$("set-preview").querySelector(".pv-cursor").classList.contains("bar"), "the preview's cursor is not a bar");
h.$("set-cursor-bar").focus();
h.key({ key: "ArrowRight" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "cursorStyle", text: "underline" });
assert.ok(h.doc.activeElement === h.$("set-cursor-underline"), "the arrows did not move to the shape they chose");
h.click(h.$("set-cursor-blink"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "cursorBlink", kind: "off" });
assert.strictEqual(h.terms[0].options.cursorBlink, false, "the terminals' cursors still blink");

// Behaviour.
h.click(h.$("settings-tab-behaviour"));
assert.strictEqual(h.$("set-fanout-sametab").checked, false, "fan out's own tab is offered as the default");
h.$("set-fanout-sametab").checked = true;
h.dispatch(h.$("set-fanout-sametab"), new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "fanOutSameTab", kind: "on" });
h.click(h.$("set-auto-review-default"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "autoReviewDefault", kind: "on" });
assert.ok(on("set-auto-review-default"), "the switch did not turn on at once");

// Status detection: sending terminal output to TypeSafe is off until asked for,
// and the setting says plainly what it sends.
assert.ok(!on("set-jev-status"), "sending terminal output to TypeSafe starts on");
const jevRow = h.$("settings-pane").textContent;
assert.ok(/SENT TO TYPESAFE/.test(jevRow) && /Off by default/.test(jevRow) && /TYPESAFE_API_KEY/.test(jevRow),
  "the setting does not say that terminal output is sent to TypeSafe, that it is off by default, and what turns it on: " + jevRow);
h.click(h.$("set-jev-status"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "jevStatus", kind: "on" });
assert.ok(on("set-jev-status"), "the switch did not turn on at once");
h.click(h.$("set-jev-status"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "jevStatus", kind: "off" });

// Keybindings.
h.click(h.$("settings-tab-keybindings"));
const closePane = h.$("set-keybind-closePane");
assert.ok(closePane, "closePane has no row in the keybindings settings");
assert.ok(h.$("set-keybind-reset-closePane").disabled, "an untouched binding offers to reset itself");
assert.ok(h.$("set-keybind-reset-all").disabled, "nothing has been remapped, and reset-all is offered anyway");
h.click(closePane);
h.key({ key: "w", ctrlKey: true, altKey: true });
assert.deepStrictEqual(h.commands().pop(), { cmd: "setKeybinding", id: "closePane", text: "Ctrl+Alt+W" });
h.recv({ type: "keyTable",
  keys: keys.map((k) => k.id === "closePane" ? Object.assign({}, k, { keys: "Ctrl+Alt+W", overridden: true }) : k) });
assert.strictEqual(h.$("set-keybind-closePane").textContent, "Ctrl+Alt+W", "the row did not take the new binding");
assert.ok(!h.$("set-keybind-reset-closePane").disabled, "a remapped binding still offers no reset");
assert.ok(!h.$("set-keybind-reset-all").disabled, "a remap did not enable resetting every shortcut");
h.click(h.$("set-keybind-reset-closePane"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "resetKeybinding", id: "closePane" });

// Agents.
h.click(h.$("settings-tab-agents"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "refreshAgents" });
const all = h.$("set-agent-all");
all.value = "claude\nopus";
h.dispatch(all, new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "setAgentDefault", agent: "claude", model: "opus", kind: "all" });
const mine = h.$("set-agent-project");
mine.value = "claude\nsonnet";
h.dispatch(mine, new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "setAgentDefault", agent: "claude", model: "sonnet" });
h.recv(fixture({ agents: catalog({ project: { agent: "claude", model: "sonnet" } }) }));
assert.strictEqual(h.$("set-agent-project").value, "claude\nsonnet", "the project's own choice is not shown");
h.$("set-agent-project").value = "";
h.dispatch(h.$("set-agent-project"), new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "setAgentDefault", agent: "", model: "" });

// API keys: the keys dialog's own list, drawn here.
h.click(h.$("settings-tab-keys"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "keys" });
h.recv({ type: "keys", items: [{ agent: "anthropic", name: "Anthropic", set: true, source: "store", vars: ["ANTHROPIC_API_KEY"] }] });
assert.ok(pane.textContent.includes("Anthropic"), "the keys are not listed in the settings");
const button = (text) => pane.querySelectorAll("button").find((b) => b.textContent === text);
h.click(button("Clear"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "keyClear", id: "anthropic" });
h.click(button("Replace…"));
const field = pane.querySelectorAll("input")[0];
assert.ok(field, "there is nowhere to type the key");
field.value = "sk-secret";
field.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "keySet", id: "anthropic", text: "sk-secret" });
assert.ok(!pane.textContent.includes("sk-secret") && !pane.querySelectorAll("input").length, "the key was left on screen");

// Remote access: the remote dialog's own parts, drawn here.
h.click(h.$("settings-tab-remote"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteDevices" });
h.recv({ type: "remoteDevices", enabled: false, devices: [], hosts: [] });
assert.ok(pane.contains(h.$("remote-relay")) && pane.contains(h.$("remote-name")), "the relay and the machine's name are not asked for");
assert.ok(!pane.textContent.includes("of your own"), "turning remote access on offers a relay of your own: " + pane.textContent);
h.$("remote-relay").value = "relay.example";
h.$("remote-name").value = "desk";
h.click(h.$("remote-enable"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "remoteEnable", relay: "relay.example", name: "desk", join: "", invite: "" });
h.recv({ type: "remoteOutcome", action: "enable" });
h.recv(fixture({ remote: { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 1, since: "2030-01-01T00:00:00Z" } }));
h.recv({ type: "remoteDevices", enabled: true,
  devices: [{ id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" }],
  hosts: [{ id: "h1", name: "desk", online: true, self: true }] });
assert.ok(pane.textContent.includes("phone") && pane.textContent.includes("desk"), "the paired devices and this machine are not listed");
assert.ok(pane.textContent.includes("relay.example"), "the relay is not named");
assert.ok(pane.contains(h.$("remote-disable")), "remote access cannot be turned off from the settings");

// Account & plan.
h.click(h.$("settings-tab-plan"));
const text = pane.textContent;
assert.ok(text.includes("Free") && text.includes("Current plan"), "the free plan is not shown as the one you are on");
// What the shared relay will cost is not settled, so the free plan says what
// it covers today and promises nothing about tomorrow.
assert.ok(text.includes("Every part of the desktop app, and remote access to your panes through the shared relay."),
  "the free plan does not say what it covers: " + text);
assert.ok(!relayPromise(text), "the plan promises what the shared relay will cost: " + relayPromise(text));
// Enterprise is for companies and not here yet: individuals keep the shared
// relay, and nothing offers them a relay.
assert.ok(text.includes("Enterprise") && text.includes("Coming soon"), "Enterprise is not announced");
assert.ok(text.includes("For companies: run the relay on your own infrastructure, with SSO and support."),
  "Enterprise is not said to be for companies: " + text);
assert.ok(!/private relay/i.test(text), "the plan still announces private relays: " + text);
assert.ok(!text.includes("of your own"), "the plan offers a relay of your own: " + text);
assert.ok(!/[$£€]\s?\d/.test(text), "a price was invented: " + text);
assert.strictEqual(h.$("set-plan-link").textContent, "Read about Enterprise");
assert.strictEqual(h.$("set-plan-link").getAttribute("href"), "https://flockdeck.ai/#enterprise", "the link does not go to the site's Enterprise card");
`)
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

// The prompt bar is how one instruction reaches every agent, and sending it
// again, or a variation on it, meant typing it out in full. Up recalls what
// was sent, as it does at a prompt, and Down comes back to what was being
// typed.
func TestThePromptBarRemembersWhatWasSent(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const input = h.$("prompt-input");
for (const text of ["run the tests", "commit what you have"]) {
  h.press("promptAll");
  input.value = text;
  h.key({ key: "Enter" });
}
h.press("promptAll");
input.value = "half typ";
h.key({ key: "ArrowUp" });
assert.strictEqual(input.value, "commit what you have", "Up did not recall the last prompt sent");
h.key({ key: "ArrowUp" });
assert.strictEqual(input.value, "run the tests");
h.key({ key: "ArrowUp" });
assert.strictEqual(input.value, "run the tests", "Up walked past the first prompt sent");
h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
assert.strictEqual(input.value, "half typ", "Down did not come back to what was being typed");
h.key({ key: "ArrowUp" });
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "sendPrompt", id: "p1", text: "commit what you have" });
`)
}

// A key that runs one of the window's actions went on to the terminal as well:
// xterm reads keydown on its own textarea without asking whether it was
// prevented. In headless Chrome against a real pane, Ctrl+Shift+Left moved the
// pane and sent ESC[1;6D to the program in it, and Alt+1 sent ESC 1.
func TestABoundKeyDoesNotAlsoReachTheTerminal(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const term = h.doc.createElement("textarea");
term.className = "xterm-helper-textarea";
h.terms[0].host.append(term);
const reached = [];
term.addEventListener("keydown", (e) => reached.push(e.key));
term.focus();
h.press("movePaneLeft");
h.key({ key: "1", code: "Digit1", altKey: true });
h.press("fontUp");
assert.deepStrictEqual(reached, [], "a key that ran an action reached the terminal too: " + reached.join(", "));
h.key({ key: "x" });
assert.deepStrictEqual(reached, ["x"], "an ordinary key no longer reaches the terminal");
`)
}

// Ctrl++ makes the text bigger in anything with a font size, and the key table
// says Ctrl+= does it here. On a US or UK keyboard + is Shift and =, so Ctrl++
// arrived with Shift held and matched nothing.
func TestCtrlPlusMakesTheTextBigger(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.key({ key: "+", code: "Equal", ctrlKey: true, shiftKey: true });
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontSize", size: 14 }, "Ctrl++ did not make the text bigger");
h.key({ key: "+", code: "NumpadAdd", ctrlKey: true });
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontSize", size: 15 }, "Ctrl and the keypad's + did not either");
`)
}

// The fan-out's trust row is about Claude Code's folder-trust question, in
// Claude's words, and was drawn whichever agents had been chosen. It shows
// only while one that asks the question is among them: the server says which
// do, and until it does, that is Claude.
func TestTheFanOutOffersTrustOnlyForAgentsThatAsk(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const preview = (agents, agent) => ({ type: "fanoutPreview", paneId: "p1", tasks: ["one task"], isRepo: true,
  trusted: true, project: "repo", cwd: "C:/repo", agent, agents });
const trustRow = () => h.$("overlay-body").querySelectorAll("label.fan-opt").find((l) => l.textContent.includes("Trust"));
const runSel = () => h.$("overlay-body").querySelector("select.fan-agent-sel");
const pick = (value) => { const s = runSel(); s.value = value; h.dispatch(s, new h.Ev("change", { target: s })); };
const agents = [
  { id: "claude", name: "Claude Code", models: [{ id: "" }, { id: "opus" }] },
  { id: "codex", name: "Codex", models: [{ id: "gpt-5" }] },
];

h.press("fanout");
h.recv(preview(agents, "codex"));
assert.ok(trustRow().hidden, "the trust row is offered for an agent that asks no such question");
pick("claude\n");
assert.ok(!trustRow().hidden, "the trust row is not offered when Claude is chosen");
h.$("overlay-body").querySelector("button.primary").onclick();
assert.strictEqual(h.commands().pop().trust, true);

// Where the server says which agents ask, that is what counts.
h.key({ key: "Escape" });
h.press("fanout");
h.recv(preview([{ id: "claude", name: "Claude Code", models: [{ id: "" }, { id: "opus" }], askTrust: false },
                { id: "other", name: "Other", models: [{ id: "m" }], askTrust: true }], "other"));
assert.ok(!trustRow().hidden, "an agent the server says asks was not offered trust");
pick("claude\n");
assert.ok(trustRow().hidden, "the id was trusted over what the server said");
`)
}

// fanoutRouting opens the window and defines a fan-out preview with routing on:
// three tasks, one routed to a smaller model, one left alone, and one routed
// to a stronger model.
const fanoutRouting = `
h.hello();
h.recv(fixture());
const routedAgents = [{ id: "claude", name: "Claude Code", default: "", models: [
  { id: "", name: "Default" }, { id: "opus", name: "Opus", tier: "top" },
  { id: "sonnet", name: "Sonnet", tier: "mid" }, { id: "haiku", name: "Haiku", tier: "small" }] }];
const routedPreview = (over) => Object.assign({ type: "fanoutPreview", paneId: "p1", isRepo: false, cwd: "C:/repo",
  tasks: ["run the tests", "add a health endpoint", "fix the race"], agent: "claude", model: "sonnet",
  agents: routedAgents, routing: "suggest",
  routes: [{ model: "haiku", tier: "small", rule: "run the tests", reason: "rule 'run the tests' → small" }, null,
           { model: "opus", tier: "top", rule: "hard work", reason: "rule 'hard work' → top", up: true }] }, over || {});
const body = h.$("overlay-body");
const sels = () => body.querySelectorAll("div.fan-row").map((r) => r.querySelector("select"));
const tags = () => body.querySelectorAll("span.fan-routed");
`

// Routing pre-fills a fan-out's rows and marks them, and the dialog sends
// exactly what it shows: a routed row goes over the wire as a row chosen by
// hand does, and its rule only marks the pane it starts.
func TestTheFanOutShowsWhatRoutingChoseAndStartsWhatItShows(t *testing.T) {
	runFrontEnd(t, fanoutRouting+`
h.press("fanout");
h.recv(routedPreview());
assert.deepStrictEqual(sels().map((s) => s.value), ["claude\nhaiku", "", "claude\nopus"]);
assert.strictEqual(tags().length, 2, "the routed rows are not marked");
assert.ok(tags()[0].dataset.tip.includes("run the tests"), "the tag does not say why: " + tags()[0].dataset.tip);
assert.ok(tags()[1].textContent.includes("↗"), "a stronger model is not marked as one");
assert.strictEqual(body.querySelector("span.fan-route-text").textContent,
  "Routing chose a smaller model for 1 of 3 tasks and a stronger one for 1.");
assert.ok(body.querySelector("span.fan-count").textContent.includes("2 routed"),
  "the count does not say how many were routed: " + body.querySelector("span.fan-count").textContent);

// Changing a routed row makes it the user's.
const s = sels()[2];
s.value = "claude\nsonnet";
h.dispatch(s, new h.Ev("change", { target: s }));
assert.strictEqual(tags().length, 1, "the tag stayed on a row somebody chose for");

body.querySelector("button.primary").onclick();
const sent = h.commands().pop();
assert.deepStrictEqual(sent.taskAgents, ["claude", "", "claude"]);
assert.deepStrictEqual(sent.taskModels, ["haiku", "", "sonnet"]);
assert.deepStrictEqual(sent.taskRouted, ["run the tests", "", ""]);
assert.deepStrictEqual(sent.routeOverrides, [{ task: "fix the race", rule: "hard work", agent: "claude", routed: "opus", chosen: "sonnet" }]);
`)
}

// A row routed to another agent shows that agent's select, and starts it:
// what was shown is what runs, agent included.
func TestTheFanOutShowsARouteToAnotherAgent(t *testing.T) {
	runFrontEnd(t, fanoutRouting+`
h.press("fanout");
h.recv(routedPreview({
  agents: routedAgents.concat([{ id: "openai-compatible", name: "OpenAI-compatible endpoint",
    models: [{ id: "qwen2.5-coder", name: "Qwen 2.5 Coder", tier: "small" }] }]),
  routes: [{ model: "qwen2.5-coder", tier: "small", rule: "tests go local", agent: "openai-compatible",
             reason: "rule 'tests go local' → OpenAI-compatible endpoint · Qwen 2.5 Coder. Runs on your own machine, no per-token cost" },
           null, null],
}));
assert.deepStrictEqual(sels().map((s) => s.value), ["openai-compatible\nqwen2.5-coder", "", ""]);
assert.strictEqual(tags().length, 1, "the cross-agent row is not marked routed");
assert.ok(tags()[0].dataset.tip.includes("no per-token cost"), "the tag does not say what it costs: " + tags()[0].dataset.tip);

body.querySelector("button.primary").onclick();
const sent = h.commands().pop();
assert.deepStrictEqual(sent.taskAgents, ["openai-compatible", "", ""]);
assert.deepStrictEqual(sent.taskModels, ["qwen2.5-coder", "", ""]);
assert.deepStrictEqual(sent.taskRouted, ["tests go local", "", ""]);

// The count line says which agent the routed row actually runs on.
assert.ok(body.querySelector("span.fan-count").textContent.includes("OpenAI-compatible endpoint"),
  "the split does not name the agent a routed row runs on: " + body.querySelector("span.fan-count").textContent);
`)
}

// One button puts every routed row back on the run's model.
func TestTheRunsModelCanBeTakenForEveryTask(t *testing.T) {
	runFrontEnd(t, fanoutRouting+`
h.press("fanout");
h.recv(routedPreview());
const clear = h.$("fan-route-clear");
assert.ok(clear && !clear.hidden, "there is no way to take the run's model for every task");
h.click(clear);
assert.strictEqual(tags().length, 0, "rows are still marked routed");
assert.deepStrictEqual(sels().map((s) => s.value), ["", "", ""]);
assert.ok(h.$("fan-route-clear").hidden, "the button is offered with nothing left to undo");
body.querySelector("button.primary").onclick();
const sent = h.commands().pop();
assert.strictEqual(sent.taskAgents, undefined, "rows on the run's model were sent as chosen by hand");
assert.strictEqual(sent.taskRouted, undefined);
assert.strictEqual(sent.routeOverrides.length, 2, "the log is not told the routed choices were set aside");
`)
}

// With routing off the dialog is exactly what it was, and asks for nothing.
func TestTheFanOutWithRoutingOffIsUnchanged(t *testing.T) {
	runFrontEnd(t, fanoutRouting+`
h.press("fanout");
h.recv(routedPreview({ routing: undefined }));
assert.ok(!body.querySelector("div.fan-route"), "a routing line was drawn with routing off");
assert.strictEqual(tags().length, 0);
assert.deepStrictEqual(sels().map((s) => s.value), ["", "", ""]);
const box = body.querySelector("textarea.fan-tasks");
box.value += "\nrename foo";
box.oninput();
await h.sleep(300);
assert.ok(!h.commands().some((c) => c.cmd === "routeTasks"), "routes were asked for with routing off");
`)
}

// An edited list is routed afresh once typing pauses, and so is a run moved to
// another model; an answer about a run since changed is not taken.
func TestAnEditedFanOutIsRoutedAfresh(t *testing.T) {
	runFrontEnd(t, fanoutRouting+`
h.press("fanout");
h.recv(routedPreview());
const box = body.querySelector("textarea.fan-tasks");
box.value = "run the tests\nrename foo to bar";
box.oninput();
await h.sleep(300);
const ask = h.commands().pop();
assert.deepStrictEqual(ask, { cmd: "routeTasks", tasks: ["run the tests", "rename foo to bar"], agent: "claude", model: "sonnet" });
const route = (rule) => ({ model: "haiku", tier: "small", rule, reason: "rule '" + rule + "' → small" });
h.recv({ type: "routes", agent: "claude", model: "sonnet", tasks: ask.tasks, routes: [route("run the tests"), route("rename or move")] });
assert.deepStrictEqual(sels().map((s) => s.value), ["claude\nhaiku", "claude\nhaiku"]);
h.recv({ type: "routes", agent: "codex", model: "", tasks: ask.tasks, routes: [null, null] });
assert.strictEqual(tags().length, 2, "an answer about another run was taken");

const run = body.querySelector("div.fan-agent").querySelector("select");
run.value = "claude\nopus";
h.dispatch(run, new h.Ev("change", { target: run }));
assert.strictEqual(tags().length, 0, "routes for the old model stayed on the rows");
await h.sleep(300);
assert.deepStrictEqual(h.commands().pop(), { cmd: "routeTasks", tasks: ask.tasks, agent: "claude", model: "opus" });
`)
}

// A choice for one row is held by the task's text, so two rows with the same
// text share it. Only the row chosen on was redrawn: the other went on saying
// "Same as the run" while Start sent the new choice for both of them.
func TestTwoRowsWithTheSameTaskShowTheChoiceTheyShare(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("fanout");
h.recv({ type: "fanoutPreview", paneId: "p1", tasks: ["write the tests", "write the tests"], isRepo: false,
  cwd: "C:/repo", agent: "claude", agents: [{ id: "claude", name: "Claude Code", models: [{ id: "" }, { id: "opus" }] }] });
const body = h.$("overlay-body");
const sels = () => body.querySelectorAll("div.fan-row").map((r) => r.querySelector("select"));
const s = sels()[0];
s.value = "claude\nopus";
h.dispatch(s, new h.Ev("change", { target: s }));
assert.deepStrictEqual(sels().map((x) => x.value), ["claude\nopus", "claude\nopus"],
  "the second row does not show the choice Start will send for it");
assert.ok(sels()[0] === s, "the row being chosen on was built again under the keyboard");
body.querySelector("button.primary").onclick();
assert.deepStrictEqual(h.commands().pop().taskModels, ["opus", "opus"]);
`)
}

// The server reads a pane's plan off the workspace goroutine and answers when
// it has, so an answer can arrive late. One for the dialog opened on the
// first pane replaced the dialog opened again on the second, and Start then
// started the first pane's plan in the first pane's tab.
func TestAFanOutTakesOnlyTheAnswerItAskedFor(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const preview = (paneId, task) => ({ type: "fanoutPreview", paneId, tasks: [task], isRepo: false, cwd: "C:/repo",
  agent: "claude", agents: [{ id: "claude", name: "Claude Code", models: [{ id: "" }] }] });
const box = () => h.$("overlay-body").querySelector("textarea.fan-tasks");

h.press("fanout");
assert.deepStrictEqual(h.commands().pop(), { cmd: "fanoutPreview", id: "p1" });
h.key({ key: "Escape" });
h.recv(fixture({ activeTab: "t2" }));
h.press("fanout");
assert.deepStrictEqual(h.commands().pop(), { cmd: "fanoutPreview", id: "p2" });

h.recv(preview("p1", "the first pane's plan"));
assert.ok(!box(), "the answer about another pane was drawn in this dialog");
h.recv(preview("p2", "the second pane's plan"));
assert.strictEqual(box().value, "the second pane's plan");
h.recv(preview("p2", "a second answer"));
assert.strictEqual(box().value, "the second pane's plan", "a repeated answer replaced the one being edited");

h.$("overlay-body").querySelector("button.primary").onclick();
const sent = h.commands().pop();
assert.strictEqual(sent.id, "p2", "Start used the other pane");
assert.deepStrictEqual(sent.tasks, ["the second pane's plan"]);
`)
}

// An agent's control holds agent and model together, and an empty model means
// whichever model the agent is set to. For an agent whose models have no empty
// entry - the model APIs - nothing matched, and the control fell back to its
// first entry: another agent, or one not installed. Settings showed the wrong
// default, and a fan-out could start the wrong agent.
func TestAnAgentChosenWithoutAModelShowsItsOwnDefault(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const api = { id: "anthropic", name: "Anthropic API", runner: "api", available: true, defaultModel: "claude-sonnet",
  models: [{ id: "claude-opus", name: "Opus" }, { id: "claude-sonnet", name: "Sonnet" }] };
h.recv(fixture({ agents: catalog({ items: catalog().items.concat([api]),
  default: { agent: "anthropic", model: "" }, project: { agent: "anthropic", model: "retired" } }) }));
h.press("settings");
h.click(h.$("settings-tab-agents"));
assert.strictEqual(h.$("set-agent-all").value, "anthropic\nclaude-sonnet",
  "the default for every project is not shown as the agent's own default model");
assert.strictEqual(h.$("set-agent-project").value, "anthropic\nclaude-opus",
  "a model the agent no longer has was shown as another agent");

h.key({ key: "Escape" });
h.press("fanout");
h.recv({ type: "fanoutPreview", paneId: "p1", tasks: ["a task"], isRepo: false, cwd: "C:/repo", agent: "anthropic",
  agents: [{ id: "claude", name: "Claude Code", models: [{ id: "" }, { id: "opus" }] },
           { id: "anthropic", name: "Anthropic API", default: "retired", models: [{ id: "claude-opus" }, { id: "claude-sonnet" }] }] });
const run = h.$("overlay-body").querySelector("div.fan-agent").querySelector("select");
assert.strictEqual(run.value, "anthropic\nclaude-opus", "the run would start another agent");
`)
}

// A pane started on a routed model says so in its header, and which way.
func TestAPaneOnARoutedModelSaysSo(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const badge = () => h.$("workspace").querySelector("span.pane-agent");
h.recv(fixture({ panes: { p1: pane("p1", { agent: "claude", model: "haiku", routed: "run the tests", routedFrom: "sonnet", route: "down" }), p2: pane("p2") } }));
assert.strictEqual(badge().textContent, "claude · haiku ↘");
assert.ok(badge().dataset.tip.includes("from sonnet") && badge().dataset.tip.includes("run the tests"),
  "the tooltip does not say why: " + badge().dataset.tip);
h.recv(fixture({ panes: { p1: pane("p1", { agent: "claude", model: "opus", routed: "hard work", routedFrom: "sonnet", route: "up" }), p2: pane("p2") } }));
assert.strictEqual(badge().textContent, "claude · opus ↗");
h.recv(fixture({ panes: { p1: pane("p1", { agent: "claude", model: "opus" }), p2: pane("p2") } }));
assert.strictEqual(badge().textContent, "claude · opus", "a model chosen by hand is marked routed");
h.recv(fixture({ panes: { p1: pane("p1", { agent: "openai-compatible", model: "qwen2.5-coder",
  routed: "tests go local", routedFrom: "sonnet", routedFromAgent: "claude" }), p2: pane("p2") } }));
assert.ok(badge().dataset.tip.includes("from claude · sonnet"),
  "a route to another agent does not name where the model came from: " + badge().dataset.tip);
`)
}

// Settings › Agents › Routing: the mode for every project and for this one,
// the floor of whichever policy this project is routed by, the rules to read,
// and the history to clear.
func TestRoutingIsSetInSettings(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const routing = (over) => Object.assign({ every: { mode: "off", floor: "" },
  rules: [{ name: "run the tests", choice: "small", when: "the task matches /^run/" }], builtIn: true,
  note: "Routing leaves Claude Code's Default alone.", config: "C:/state/agents.json" }, over || {});
h.recv(fixture({ agents: catalog({ routing: routing() }) }));
h.press("settings");
h.click(h.$("settings-tab-agents"));
assert.strictEqual(h.$("set-route-all").value, "off", "routing is not shown off");

const pick = (id, value) => { const s = h.$(id); s.value = value; h.dispatch(s, new h.Ev("change")); return h.commands().pop(); };
assert.deepStrictEqual(pick("set-route-all", "suggest"), { cmd: "setRouting", kind: "all", target: "mode", text: "suggest" });
assert.deepStrictEqual(pick("set-route-project", "auto"), { cmd: "setRouting", target: "mode", text: "auto" });
assert.deepStrictEqual(pick("set-route-floor", "mid"), { cmd: "setRouting", target: "floor", text: "mid", kind: "all" });
assert.strictEqual(h.$("set-route-cross").checked, false, "cross-agent routing is shown on by default");
h.$("set-route-cross").checked = true;
h.dispatch(h.$("set-route-cross"), new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "setRouting", kind: "all", target: "crossAgent", text: "true" });
// Asking Jev sends task text to a third party: off until turned on, worded
// plainly, and its own setting per project like the rest.
assert.strictEqual(h.$("set-route-jev").checked, false, "asking Jev is shown on by default");
const jevRow = h.$("set-route-jev").parentElement.parentElement.parentElement;
assert.ok(/TypeSafe/.test(jevRow.textContent) && /third party/.test(jevRow.textContent) && /TYPESAFE_API_KEY/.test(jevRow.textContent),
  "the setting does not say where the text goes: " + jevRow.textContent);
assert.ok(jevRow.textContent.includes("it is not set, so nothing is sent"), "nothing says the key is missing");
h.$("set-route-jev").checked = true;
h.dispatch(h.$("set-route-jev"), new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "setRouting", kind: "all", target: "jev", text: "true" });
assert.ok(h.$("set-route-rules").textContent.includes("run the tests"), "the rules are not listed");
assert.ok(h.$("set-route-note").textContent.includes("Default"), "nothing says why routing would do nothing");
h.click(h.$("set-route-clear-log"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "clearRoutingLog" });

// A project with a policy of its own: that is the floor changed.
h.recv(fixture({ agents: catalog({ routing: routing({ project: { mode: "suggest", floor: "mid" }, note: "", crossAgent: true,
  jev: true, jevKey: true, fallback: [{ name: "fallback", kept: 3, overridden: 1 }, { name: "fallback (Jev-assisted)", kept: 4, overridden: 0 }] }) }) }));
assert.strictEqual(h.$("set-route-jev").checked, true, "the project's own Jev setting is not shown");
assert.ok(!h.$("set-route-jev").parentElement.parentElement.parentElement.textContent.includes("it is not set"), "a key that is set is said to be missing");
assert.ok(h.$("set-route-fallback").textContent.includes("fallback (Jev-assisted)") && h.$("set-route-fallback").textContent.includes("overridden 25% of the time (1 of 4)"),
  "the comparison of the fallback with and without Jev is not shown: " + h.$("set-route-fallback").textContent);
assert.strictEqual(h.$("set-route-project").value, "suggest");
assert.strictEqual(h.$("set-route-floor").value, "mid", "the project's own floor is not shown");
assert.deepStrictEqual(pick("set-route-floor", "top"), { cmd: "setRouting", target: "floor", text: "top" });
assert.strictEqual(h.$("set-route-cross").checked, true, "the project's own crossAgent is not shown");
h.$("set-route-cross").checked = false;
h.dispatch(h.$("set-route-cross"), new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "setRouting", target: "crossAgent", text: "false" },
  "a project with its own policy sent kind: all");
h.$("set-route-jev").checked = false;
h.dispatch(h.$("set-route-jev"), new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "setRouting", target: "jev", text: "false" },
  "a project with its own policy sent kind: all for Jev");
`)
}

// The agent picker's ? opened the help page on panes, not the one on agents
// and models, which is what the picker chooses between.
func TestThePickersHelpIsTheAgentsPage(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("new-tab-pick"));
h.click(h.$("overlay-help"));
await h.sleep(30);
assert.strictEqual(h.$("help-content").dataset.slug, "agents", "the picker's ? opened another page");
`)
}

// The fan-out dialog says how many agents will start, and the server stops at
// a cap it announces only afterwards. The dialog's number and the server's are
// the same number, written in two places, so this holds them level.
func TestTheFanOutCapIsTheServers(t *testing.T) {
	m := regexp.MustCompile(`const FANOUT_MAX = (\d+);`).FindStringSubmatch(readAsset(t, "app.js"))
	if m == nil || m[1] != strconv.Itoa(workspace.MaxTasks) {
		t.Fatalf("app.js caps a fan-out at %v, the server at %d", m, workspace.MaxTasks)
	}
}

// A fan-out of fifteen tasks offered to "Start 15 agents", and twelve started.
func TestTheFanOutDoesNotPromiseMoreThanItStarts(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("fanout");
const tasks = Array.from({ length: 15 }, (_, i) => "task " + (i + 1));
h.recv({ type: "fanoutPreview", paneId: "p1", tasks, isRepo: false, cwd: "C:/repo" });
const start = h.$("overlay-body").querySelector("button.primary");
assert.strictEqual(start.textContent, "Start 12 agents", "the button promised agents that will not start");
assert.ok(/only the first 12 start/.test(h.$("overlay-body").querySelector("span.fan-count").textContent),
  "nothing said the rest will not start");
`)
}

// The help's search box had a placeholder and no name, and a placeholder is
// gone as soon as anything is typed and is not reliably read out as a label.
func TestTheHelpSearchIsNamed(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("help");
await h.sleep(30);
assert.strictEqual(h.$("help-search").getAttribute("aria-label"), "Search the help", "the help's search box has no name");
`)
}

// Every dialog has a ? in its header for the page that explains it, except
// the update and API key dialogs, which had none although both are covered:
// updating on the command line page, keys on the page about agents.
func TestTheUpdateAndKeyDialogsHaveTheirHelp(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture({ update: { version: "9.9.9" } }));
h.click(h.$("btn-update"));
assert.ok(h.$("overlay-help"), "the update dialog has no ?");
h.click(h.$("overlay-help"));
await h.sleep(30);
assert.strictEqual(h.$("help-content").dataset.slug, "cli");

h.key({ key: "Escape" });
paletteRun("api keys");
assert.ok(h.$("overlay-help"), "the API key dialog has no ?");
h.click(h.$("overlay-help"));
await h.sleep(30);
assert.strictEqual(h.$("help-content").dataset.slug, "agents");
`)
}

// Every dialog's own header offers "Go to…" beside its ?, and that is the
// palette narrowed to the actions that open a dialog: it lists Settings
// without listing something like a pane split, and choosing one from there
// switches straight to it, no trip back out to the rail.
func TestGoToSwitchesDialogsFromThePalette(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("worktrees");
assert.ok(h.$("overlay-goto"), "the worktrees dialog has no Go to…");
h.click(h.$("overlay-goto"));
assert.strictEqual(h.$("palette").hidden, false, "Go to… did not open the palette");
assert.strictEqual(h.$("palette-input").placeholder, "Go to…");

const labels = h.$("palette-list").children.map((r) => r.querySelector(".pal-label").textContent);
assert.ok(labels.includes("Settings"), "Go to… does not offer Settings");
assert.ok(!labels.some((l) => l.startsWith("Split right")), "Go to… offers more than the dialogs");

const row = h.$("palette-list").children.find((r) => r.querySelector(".pal-label").textContent === "Settings");
assert.ok(row, "Go to… has no Settings");
h.click(row);
assert.strictEqual(h.$("overlay-title").textContent, "Settings", "Go to… did not switch dialogs");

// Opened as the palette proper, by contrast, it offers everything again.
h.press("worktrees");
h.press("palette");
const everything = h.$("palette-list").children.map((r) => r.querySelector(".pal-label").textContent);
assert.ok(everything.some((l) => l.startsWith("Split right")), "the ordinary palette lost commands");
`)
}

// The broadcast tooltip said it mirrors what you type into every pane in the
// set. Nothing mirrors typing: the set is where the prompt bar's message goes
// (BroadcastTargets is used by SendPrompt alone), and a terminal typed into
// still reaches only itself.
func TestTheBroadcastTipSaysWhatBroadcastDoes(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const cast = h.$("workspace").querySelectorAll("button").find((b) => b.textContent === "⇉");
const tip = cast.dataset.tip;
assert.ok(!/mirrors what you type/i.test(tip), "the tooltip still says typing is mirrored: " + tip);
assert.ok(/prompt bar/.test(tip), "the tooltip does not say it is the prompt bar's message that goes to the set: " + tip);
`)
}

// Choosing a page in the help's contents rebuilt the list, which destroyed the
// item the choice was made on and dropped the keyboard out of the dialog.
func TestChoosingAHelpPageKeepsTheKeyboardInTheContents(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("help");
await h.sleep(30);
const items = () => h.$("overlay-body").querySelectorAll("button.help-item");
const second = items()[1];
const title = second.querySelector(".help-item-title").textContent;
second.focus();
h.key({ key: "Enter" });
const now = h.doc.activeElement;
assert.ok(items().includes(now), "choosing a page dropped the keyboard out of the contents");
assert.strictEqual(now.querySelector(".help-item-title").textContent, title, "the keyboard landed on another page");
`)
}

// The page open in the help was marked in its contents by colour alone, so a
// screen reader walking the list was told nothing about which one it was.
func TestTheHelpSaysWhichPageIsOpen(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("help");
await h.sleep(30);
const items = h.$("overlay-body").querySelectorAll("button.help-item");
const current = items.filter((b) => b.getAttribute("aria-current") === "page");
assert.strictEqual(current.length, 1, "the contents do not say which page is open");
assert.ok(current[0].classList.contains("sel"), "the page said to be open is not the one shown");
`)
}

// The help's search box keeps the keyboard while the help is open, and Page Up
// and Page Down did nothing there, so the page being read could not be
// scrolled without the mouse.
func TestPageKeysScrollTheHelpBeingRead(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("help");
await h.sleep(30);
const content = h.$("help-content");
content.clientHeight = 400;
content.scrollTop = 0;
h.$("help-search").focus();
const down = h.key({ key: "PageDown" });
assert.ok(down.defaultPrevented);
assert.strictEqual(content.scrollTop, 360, "Page Down did not scroll the page being read");
h.key({ key: "PageUp" });
assert.strictEqual(content.scrollTop, 0, "Page Up did not scroll it back");
`)
}

// Help pages refer to each other, and the references were bold text with no
// way to follow them. A link to a page's slug opens that page in the viewer.
func TestAHelpPageLinksToAnother(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("help");
await h.sleep(30);
const content = h.$("help-content");
// The harness does not parse the page's HTML into elements, so the link a
// page renders is put there by hand.
const a = h.doc.createElement("a");
a.setAttribute("href", "#worktrees");
content.append(a);
const ev = new h.Ev("click", { target: a });
h.dispatch(a, ev);
assert.ok(ev.defaultPrevented, "the link was left to the browser");
assert.strictEqual(content.dataset.slug, "worktrees", "following the link did not open the page it names");

// A hash that names no page is left alone.
const b = h.doc.createElement("a");
b.setAttribute("href", "#no-such-page");
h.$("help-content").append(b);
const other = new h.Ev("click", { target: b });
h.dispatch(b, other);
assert.ok(!other.defaultPrevented);
`)
}

// The tooltip is the only place most glyph buttons say what they do, and it
// came only for a pointer: tabbing onto ⟳ or ⤢ told somebody who can see
// nothing at all. Keyboard focus brings it too, and taking the focus away
// takes it away.
func TestKeyboardFocusShowsTheTooltip(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const zoom = h.$("workspace").querySelectorAll("button").find((b) => b.textContent === "⤢");
zoom.focus();
h.dispatch(zoom, new h.Ev("focusin", { target: zoom }));
await h.sleep(320);
const tip = h.doc.body.querySelector("div.tip");
assert.ok(tip, "tabbing onto a glyph button showed nothing about what it does");
assert.strictEqual(tip.textContent, zoom.dataset.tip);
h.dispatch(zoom, new h.Ev("focusout", { target: zoom }));
assert.ok(!h.doc.body.querySelector("div.tip"), "the bubble stayed after the focus left");
`)
}

// Each worktree row carried four or five buttons, each a Tab stop of its own,
// so with ten worktrees the form for a new one was fifty presses of Tab away.
// A row is one stop, walked with the arrow keys, as a pane's header is.
func TestAWorktreeRowIsOneStop(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
const items = [{ label: "main", path: "C:/repo", main: true }];
for (let i = 0; i < 3; i++) items.push({ label: "wt" + i, path: "C:/repo-wt" + i, dirty: 0, untracked: 0 });
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [], items });
const bars = h.$("overlay-body").querySelectorAll("div.wt-actions");
assert.strictEqual(bars.length, 4);
for (const bar of bars) {
  assert.strictEqual(bar.getAttribute("role"), "toolbar");
  const stops = bar.children.filter((b) => b.getAttribute("tabindex") === "0");
  assert.strictEqual(stops.length, 1, "a worktree row is still one Tab stop per button");
}
const first = bars[1].children[0];
first.focus();
h.key({ key: "ArrowRight" });
assert.ok(h.doc.activeElement === bars[1].children[1], "the arrows do not walk a worktree's buttons");
`)
}

// Escape in the help's search box, or in the agent picker's filter, closed the
// whole dialog even with something typed in it. It empties the field first,
// as a search field does, and closes the dialog from an empty one.
func TestEscapeEmptiesASearchBeforeClosingItsDialog(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("help");
await h.sleep(30);
const all = h.$("overlay-body").querySelectorAll("button.help-item").length;
const search = h.$("help-search");
search.value = "worktree";
search.oninput();
search.focus();
h.key({ key: "Escape" });
assert.ok(!h.$("overlay").hidden, "Escape with a search typed closed the help");
assert.strictEqual(search.value, "", "Escape did not empty the search");
assert.strictEqual(h.$("overlay-body").querySelectorAll("button.help-item").length, all, "the contents stayed narrowed");
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape in an empty search no longer closes the help");

h.click(h.$("new-tab-pick"));
const filter = h.$("agent-filter");
filter.value = "codex";
filter.oninput();
filter.focus();
h.key({ key: "Escape" });
assert.ok(!h.$("overlay").hidden, "Escape with a filter typed closed the picker");
assert.strictEqual(filter.value, "");
`)
}

// Double-clicking a pane's header did nothing, where double-clicking a title
// bar makes the thing it belongs to fill its space; zooming took the small
// glyph at the far end of the header. It zooms the pane now, but not when the
// double-click lands on one of the header's own buttons.
func TestDoubleClickingAPaneHeaderZoomsIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const header = h.$("workspace").querySelector("div.pane-header");
h.dispatch(header, new h.Ev("dblclick", { target: header }));
assert.deepStrictEqual(h.commands().pop(), { cmd: "toggleZoom", id: "p1" }, "double-clicking the header did not zoom the pane");

const restart = header.querySelectorAll("button").find((b) => b.textContent === "⟳");
const before = h.commands().length;
h.dispatch(restart, new h.Ev("dblclick", { target: restart }));
assert.ok(!h.commands().slice(before).some((c) => c.cmd === "toggleZoom"), "a double-click on a header button zoomed the pane");
`)
}

// The prompt bar's message goes to the focused pane and every pane in the
// broadcast set, and a pane picked by hand stays in the set with broadcast
// off. The bar counted its recipients only while broadcast was on, and a
// picked pane's tooltip said it would receive the message once broadcast was
// turned on: with it off, both said less was happening than was.
func TestThePromptBarSaysWhoItReachesWithBroadcastOff(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ broadcast: false,
  tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
    root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  panes: { p1: pane("p1"), p2: pane("p2", { broadcast: true }) } }));
h.press("promptAll");
assert.strictEqual(h.$("prompt-label").textContent, "Prompt → 2 panes",
  "with broadcast off the bar said it reaches one pane, and the message reaches two");
h.key({ key: "Escape" });

const cast = h.$("workspace").querySelectorAll("span.pane-cast").find((s) => s.dataset.tip);
assert.ok(!/once broadcast is on/.test(cast.dataset.tip), "a picked pane still says it waits for broadcast: " + cast.dataset.tip);
`)
}

// The style sheet stills its animations for somebody whose system asks for
// reduced motion, but a terminal's cursor blinks by script rather than CSS,
// so every terminal went on blinking for them regardless.
func TestReducedMotionStillsTheCursor(t *testing.T) {
	runFrontEnd(t, `
h.win.matchMedia = (q) => ({ matches: /reduce/.test(q), addEventListener() {} });
h.hello();
h.recv(fixture());
assert.strictEqual(h.terms[0].options.cursorBlink, false, "the cursor blinks although reduced motion was asked for");
`)
}

// Whether the terminal cursors blink was fixed in the source. It is chosen
// from the palette, kept with the other preferences, and followed by every
// terminal, including in another window.
func TestTheCursorBlinkCanBeTurnedOff(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello({ cursorSteady: true });
h.recv(fixture());
assert.strictEqual(h.terms[0].options.cursorBlink, false, "a steady cursor chosen on an earlier run blinks");
paletteRun("cursor blink");
assert.deepStrictEqual(h.commands().pop(), { cmd: "cursorBlink", kind: "on" });
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [] } });
assert.strictEqual(h.terms[0].options.cursorBlink, true, "the terminals did not follow the choice");
paletteRun("cursor blinking");
assert.deepStrictEqual(h.commands().pop(), { cmd: "cursorBlink", kind: "off" });
`)
}

// The terminals are drawn on a canvas, so without xterm's screen reader mode
// nothing an agent wrote could be read out. It is turned on from the palette
// or the settings, kept with the preferences, and followed by every terminal:
// those already open, those opened after, and those in another window.
func TestScreenReaderSupportReachesEveryTerminal(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
assert.strictEqual(h.terms[0].options.screenReaderMode, false, "screen reader support is on before anybody asked for it");
paletteRun("screen reader support on");
assert.deepStrictEqual(h.commands().pop(), { cmd: "screenReader", kind: "on" });
assert.strictEqual(h.terms[0].options.screenReaderMode, true, "an open terminal did not follow the choice");

// A pane opened afterwards reads it as it is made.
h.recv(fixture({ tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n3", "p3")]) },
  { id: "t2", title: "two", focus: "p2", zoom: false, attention: false, root: leaf("n2", "p2") }],
  panes: { p1: pane("p1"), p2: pane("p2"), p3: pane("p3") } }));
const late = h.terms[h.terms.length - 1];
assert.strictEqual(late.options.screenReaderMode, true, "a pane opened after the choice cannot be read out");

// Another window turning it off.
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [] } });
assert.ok(h.terms.every((t) => t.disposed || t.options.screenReaderMode === false), "the terminals did not follow another window");

// The settings have a switch for it.
h.press("settings");
h.click(h.$("settings-tab-appearance"));
assert.ok(h.$("set-screen-reader"), "the terminal settings have no switch for screen reader support");
h.click(h.$("set-screen-reader"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "screenReader", kind: "on" });
`)
}

// Inside a terminal Tab belongs to the program there, so nothing took the
// keyboard out of one to the rest of the window. F6 and Shift+F6 move it round
// the window's parts - the rail, the top bar, the focused pane's buttons, its
// terminal - and a dialog keeps it. Putting a pane in the broadcast set and
// moving a tab along the strip had no key and no command either, and are in
// the palette now.
func TestTheKeyboardCanLeaveATerminalForTheRestOfTheWindow(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
// A wide window, where the rail is on screen rather than folded into a menu.
Object.defineProperty(h.$("rail-toggle"), "offsetParent", { get: () => null });
const term = h.terms[0];
const buttons = term.host.parentElement.parentElement.querySelector("div.pane-actions");
// xterm types through a textarea of its own inside the terminal's host.
const typing = h.doc.createElement("textarea");
typing.className = "xterm-helper-textarea";
term.host.append(typing);
const inTerminal = () => { term.blur(); typing.focus(); };

inTerminal();
h.press("nextRegion");
assert.ok(h.$("rail").contains(h.doc.activeElement), "F6 in a terminal did not reach the rail");
h.press("nextRegion");
assert.ok(h.doc.activeElement === h.$("tab-t1"), "the second F6 did not reach the top bar's current tab");
h.press("nextRegion");
assert.ok(buttons.contains(h.doc.activeElement), "the third F6 did not reach the focused pane's buttons");
h.press("nextRegion");
assert.ok(term.focused, "the fourth F6 did not come back to the terminal");

inTerminal();
h.press("prevRegion");
assert.ok(buttons.contains(h.doc.activeElement), "Shift+F6 in a terminal did not reach its pane's buttons");
h.press("prevRegion");
assert.ok(h.doc.activeElement === h.$("tab-t1"), "Shift+F6 did not go back to the top bar");
h.press("prevRegion");
assert.ok(h.$("rail").contains(h.doc.activeElement), "Shift+F6 did not go back to the rail");
term.blur();
h.press("prevRegion");
assert.ok(term.focused, "Shift+F6 from the rail did not come round to the terminal");

// A dialog keeps the keyboard.
h.press("settings");
const panel = h.$("overlay-panel");
h.press("nextRegion");
assert.ok(panel === h.doc.activeElement || panel.contains(h.doc.activeElement), "F6 took the keyboard out of a dialog");
h.key({ key: "Escape" });

paletteRun("add this pane to broadcast");
assert.deepStrictEqual(h.commands().pop(), { cmd: "toggleBroadcastMember", id: "p1" });

const three = (active) => fixture({ activeTab: active, tabs: [
  { id: "t1", title: "one", focus: "p1", root: leaf("n1", "p1") },
  { id: "t2", title: "two", focus: "p2", root: leaf("n2", "p2") },
  { id: "t3", title: "three", focus: "p3", root: leaf("n3", "p3") }],
  panes: { p1: pane("p1"), p2: pane("p2"), p3: pane("p3") } });
h.recv(three("t1"));
paletteRun("move tab right");
assert.deepStrictEqual(h.commands().pop(), { cmd: "moveTab", id: "t1", target: "t3" });
let before = h.commands().length;
paletteRun("move tab left");
assert.strictEqual(h.commands().length, before, "the first tab was sent further left than the start");
h.recv(three("t3"));
paletteRun("move tab left");
assert.deepStrictEqual(h.commands().pop(), { cmd: "moveTab", id: "t3", target: "t2" });
before = h.commands().length;
paletteRun("move tab right");
assert.strictEqual(h.commands().length, before, "the last tab was sent further right than the end");
`)
}

// A pane whose process has gone is covered with what happened and a Restart
// button, and the cover was silent and left the keyboard in the terminal under
// it, where nothing typed went anywhere and nothing said why. It is an alert
// now, and takes the keyboard to Restart - once, as it goes up, for the pane
// being typed in, and never out of the palette or a dialog.
func TestAnExitedPaneSaysSoAndOffersRestartToTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
assert.ok(h.terms[0].focused, "the first pane's terminal has the keyboard to begin with");
const exited = (id) => fixture({ panes: { p1: pane("p1", { status: id === "p1" ? "exited" : "idle" }),
  p2: pane("p2", { status: id === "p2" ? "exited" : "idle" }) } });
const cover = () => h.terms[0].host.parentElement.querySelector("div.pane-error");
const restart = () => cover().querySelectorAll("button").find((b) => b.textContent === "Restart");

h.recv(exited("p1"));
assert.strictEqual(cover().getAttribute("role"), "alert", "the cover appears without a word to a screen reader");
assert.ok(h.doc.activeElement === restart(), "the keyboard stayed in the terminal under the cover");

// Once: a push that changes nothing about the exit does not take it back.
h.$("rail-toggle").focus();
h.recv(exited("p1"));
assert.ok(h.doc.activeElement === h.$("rail-toggle"), "another push took the keyboard back to Restart");

// Not out of the palette.
h.recv(fixture());
assert.ok(!cover(), "the cover stayed up with the process back");
h.press("palette");
h.recv(exited("p1"));
assert.ok(h.doc.activeElement === h.$("palette-input"), "an exit took the keyboard out of the palette");
h.key({ key: "Escape" });

// Nor for a pane in another tab.
h.recv(fixture());
h.$("rail-toggle").focus();
h.recv(exited("p2"));
assert.ok(h.doc.activeElement === h.$("rail-toggle"), "a pane in another tab took the keyboard as it exited");
`)
}

// The conversations list answered no key: each row had a Resume button to tab
// to, and the arrows did nothing. It is walked like the other lists now, and
// Enter on a row resumes that conversation.
func TestTheConversationsListIsWalkedFromTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("history");
h.recv({ type: "conversations", cwd: "C:/repo", items: [
  { id: "aaaaaaaa-1", summary: "fix the parser", ago: "1h", messages: 20 },
  { id: "bbbbbbbb-2", summary: "write the docs", ago: "2h", messages: 12 },
  { id: "cccccccc-3", summary: "already open", ago: "3h", messages: 5, open: true },
] });
const rows = h.$("overlay-body").querySelectorAll("div.conv-row");
assert.strictEqual(rows[0].getAttribute("role"), "button", "a conversation row cannot be reached from the keyboard");
rows[0].focus();
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === rows[1], "Down did not move to the next conversation");
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === rows[1], "Down walked onto a conversation that is already open");
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "resumeConversation", id: "bbbbbbbb-2", path: "C:/repo", text: "write the docs" });
assert.ok(h.$("overlay").hidden);
`)
}

// A conversation's summary is cut short at the row's width, and summaries often
// begin alike, so the part that tells two conversations apart was the part
// hidden, with nothing to read the rest by.
func TestAConversationsWholeSummaryCanBeRead(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("history");
const long = "Read the brief at C:/Users/someone/scratchpad/AUDIT-BRIEF.md and follow it exactly: your area is the relay";
h.recv({ type: "conversations", cwd: "C:/repo", items: [{ id: "aaaaaaaa-1", summary: long, ago: "1h", messages: 20 }] });
const summary = h.$("overlay-body").querySelector("div.conv-summary");
assert.strictEqual(summary.dataset.tip, long, "the whole summary cannot be read anywhere");
`)
}

// Names and paths are cut short with an ellipsis wherever there is not room
// for them - a pane's name in its header, a tab's title in the agents
// overview, a worktree's or a project's path - and paths differ at the end,
// which is the part cut. Each can be read whole in its tooltip.
func TestCutNamesAndPathsCanBeReadWhole(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const longName = "Identify and fix every bug in the desktop front end, one per commit";
h.recv(fixture({ panes: { p1: pane("p1", { name: longName }), p2: pane("p2") } }));
const name = h.$("workspace").querySelector("span.pane-name");
assert.strictEqual(name.dataset.tip, longName, "a pane's whole name cannot be read");
h.recv(fixture({ panes: { p1: pane("p1", { name: "renamed" }), p2: pane("p2") } }));
assert.strictEqual(name.dataset.tip, "renamed", "the pane's name tip kept its old name");

h.click(h.$("btn-worktrees"));
const path = "C:/Users/someone/code/flockdeck-worktrees/fix-the-parser-for-long-branch-names";
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [],
  items: [{ label: "fix-the-parser", path: path, dirty: 0, untracked: 0 }] });
assert.strictEqual(h.$("overlay-body").querySelector("div.wt-path").dataset.tip, path, "a worktree's whole path cannot be read");

h.key({ key: "Escape" });
h.press("projects");
assert.strictEqual(h.$("overlay-body").querySelector("span.proj-path").dataset.tip, "C:/repo", "a project's whole path cannot be read");

h.key({ key: "Escape" });
h.click(h.$("summary"));
h.recv({ type: "agents", items: [{ paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: longName, name: "a", status: "idle" }] });
assert.strictEqual(h.$("overlay-body").querySelector("span.agent-tab").dataset.tip, longName, "a tab's whole title cannot be read in the overview");
`)
}

// In a review of forty files or an overview of a dozen agents the arrows were
// the only way to a row. Typing the start of a row's name goes to it, as in
// nearly every list: letters typed together make one prefix, a pause starts
// again, and the search comes round from the top.
func TestTypingTheStartOfANameFindsTheRow(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("summary"));
h.recv({ type: "agents", items: ["alpha", "beta", "bravo"].map((tab, i) =>
  ({ paneId: "p" + i, tabId: "t" + i, root: "C:/repo", project: "repo", tab, name: tab, status: "idle" })) });
const rows = h.$("overlay-body").querySelectorAll("div.agent-row");
rows[0].focus();
h.key({ key: "b" });
assert.ok(h.doc.activeElement === rows[1], "typing b did not go to beta");
h.key({ key: "r" });
assert.ok(h.doc.activeElement === rows[2], "typing br did not go on to bravo");
await h.sleep(750);
h.key({ key: "a" });
assert.ok(h.doc.activeElement === rows[0], "after a pause, typing a did not come round to alpha");

// In the review the row found shows its diff, by the file's name, not its folders.
h.key({ key: "Escape" });
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false, files: [
  { path: "internal/webui/assets/app.css", label: "M", added: 1, removed: 0 },
  { path: "internal/webui/assets/app.js", label: "M", added: 1, removed: 0 },
  { path: "internal/server/server.go", label: "M", added: 1, removed: 0 },
] });
const files = h.$("overlay-body").querySelectorAll("div.rev-file");
files[0].focus();
h.key({ key: "s" });
assert.ok(h.doc.activeElement === files[2], "typing s did not find server.go");
assert.deepStrictEqual(h.commands().pop(), { cmd: "diff", path: "C:/repo", text: "internal/server/server.go" });
`)
}

// Opening a folder is how anybody new starts, and the folder list answered
// neither the arrows nor typing: two buttons a folder, so a directory of fifty
// repositories was a hundred presses of Tab. The arrows move between folders
// and typing the start of a name goes to it.
func TestTheFolderListIsWalkedFromTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("projects");
h.recv({ type: "browse", path: "C:/code", parent: "C:/", entries: ["api", "docs", "web", "worker"].map((name) =>
  ({ name, path: "C:/code/" + name })) });
const into = () => h.$("overlay-body").querySelectorAll("button.dir-into");
into()[0].focus();
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === into()[1], "Down did not move to the next folder");
h.key({ key: "w" });
assert.ok(h.doc.activeElement === into()[2], "typing w did not go to web");
h.key({ key: "o" });
assert.ok(h.doc.activeElement === into()[3], "typing wo did not go on to worker");
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "browse", path: "C:/code/worker" });
`)
}

// Page Down on a folder scrolled the list and left the keyboard on a folder
// now out of sight, so the next arrow jumped the list back to where it was.
func TestTheDialogListsPageWithTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("projects");
h.recv({ type: "browse", path: "C:/code", parent: "C:/", entries: Array.from({ length: 20 }, (_, i) =>
  ({ name: "repo" + String(i).padStart(2, "0"), path: "C:/code/repo" + i })) });
const into = () => h.$("overlay-body").querySelectorAll("button.dir-into");
into()[0].focus();
h.key({ key: "PageDown" });
const at = () => into().indexOf(h.doc.activeElement);
assert.ok(at() > 1, "Page Down did not move the keyboard a page: at " + at());
h.key({ key: "End" });
h.key({ key: "PageDown" });
assert.strictEqual(at(), 19, "Page Down at the end left the list");
h.key({ key: "PageUp" });
assert.ok(at() < 18, "Page Up did not move the keyboard a page: at " + at());
h.key({ key: "Home" });
h.key({ key: "PageUp" });
assert.strictEqual(at(), 0, "Page Up at the top left the list");

// The dialogs' other lists - the review's files here - page the same way.
h.key({ key: "Escape" });
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false, files: Array.from({ length: 20 }, (_, i) =>
  ({ path: "f" + String(i).padStart(2, "0") + ".go", label: "M", added: 1, removed: 0 })) });
const files = h.$("overlay-body").querySelectorAll("div.rev-file");
files[0].focus();
const page = h.key({ key: "PageDown" });
assert.ok(page.defaultPrevented, "Page Down scrolled the review instead");
assert.ok(files.indexOf(h.doc.activeElement) > 1, "Page Down did not move through the review's files");
`)
}

// The palette found a command only by words its name contains, so the
// abbreviations most palettes take - "nat" for New agent tab, "rp" for
// Restart pane - found nothing. They find the command now, after anything
// whose name holds the letters as they were typed.
func TestThePaletteFindsACommandByItsInitials(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("palette");
const input = h.$("palette-input");
const labels = (q) => {
  input.value = q;
  input.oninput();
  return h.$("palette-list").querySelectorAll("span.pal-label").map((n) => n.textContent);
};
assert.strictEqual(labels("nat")[0], "New agent tab", "nat did not find New agent tab");
assert.ok(labels("rp").includes("Restart pane"), "rp did not find Restart pane");
assert.strictEqual(labels("tile")[0], "Tile these panes evenly", "a word the name holds no longer comes first");
assert.ok(!labels("z").includes("Restart pane"), "a single letter matched by initials");
`)
}

// Two rows of chips were a Tab stop a chip: up to fourteen branches without a
// worktree between the worktree list and the rest of that dialog, and the
// places between the folder browser's path field and its list. Each row is one
// stop walked with the arrows, as the worktree rows and pane headers are.
func TestARowOfChipsIsOneStop(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const oneStop = (bar, what) => {
  assert.ok(bar, what + " is missing");
  assert.strictEqual(bar.getAttribute("role"), "toolbar", what + " is not a toolbar");
  assert.strictEqual(bar.children.filter((b) => b.getAttribute("tabindex") === "0").length, 1, what + " is a Tab stop a chip");
};
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main", items: [],
  branches: ["a", "b", "c", "d"].map((name) => ({ name, checkedIn: false })) });
oneStop(h.$("overlay-body").querySelector("div.places"), "the branches without a worktree");

h.key({ key: "Escape" });
h.press("projects");
h.recv({ type: "browse", path: "C:/code", parent: "C:/", entries: [],
  places: [{ name: "Home", path: "C:/Users/me" }, { name: "Desktop", path: "C:/Users/me/Desktop" }, { name: "Code", path: "C:/code" }] });
oneStop(h.$("overlay-body").querySelector("div.places"), "the places");
`)
}

// The terminals' typeface was fixed in the source. It is asked for from the
// palette, kept with the other preferences, and a font the machine does not
// have still falls back to a fixed-width one.
func TestTheTerminalFontCanBeChosen(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello({ fontFamily: "Fira Code" });
h.recv(fixture());
assert.strictEqual(h.terms[0].options.fontFamily, "Fira Code, monospace", "the font chosen on an earlier run was not used");

h.win._prompt = "Iosevka";
paletteRun("terminal font");
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontFamily", text: "Iosevka" });
assert.strictEqual(h.terms[0].options.fontFamily, "Iosevka, monospace", "the terminals did not take the new font");

h.win._prompt = "";
paletteRun("terminal font");
assert.deepStrictEqual(h.commands().pop(), { cmd: "fontFamily", text: "" });
assert.ok(/Cascadia Mono/.test(h.terms[0].options.fontFamily), "an empty answer did not go back to the default");
`)
}

// A font name the machine has no font for was saved and announced, and the
// terminals were drawn in the fallback: nothing changed, and nothing said why.
func TestAFontTheMachineLacksIsRefused(t *testing.T) {
	runFrontEnd(t, paletteRun+`
// A canvas on which only "Real Mono" is installed: any other name measures
// exactly as its generic family does.
const make = h.doc.createElement.bind(h.doc);
h.doc.createElement = (tag) => tag !== "canvas" ? make(tag) : { getContext: () => ({
  font: "",
  measureText() { return { width: /Real Mono/.test(this.font) ? 999 : (/sans-serif/.test(this.font) ? 300 : /serif/.test(this.font) ? 500 : 400) }; },
}) };
h.hello({ fontFamily: "Real Mono" });
h.recv(fixture());
const sent = () => h.commands().filter((c) => c.cmd === "fontFamily").length;

h.win._prompt = "Fira Cod";
paletteRun("terminal font");
assert.strictEqual(sent(), 0, "a font the machine does not have was saved");
assert.strictEqual(h.terms[0].options.fontFamily, "Real Mono, monospace", "the terminals were given a font the machine does not have");

// One that is there, alone or first in a list, is taken.
h.win._prompt = "Fira Cod, Real Mono";
paletteRun("terminal font");
assert.strictEqual(sent(), 1, "a list with an installed font in it was refused");
h.win._prompt = "";
paletteRun("terminal font");
assert.strictEqual(sent(), 2, "going back to the default was refused");
h.win._prompt = "monospace";
paletteRun("terminal font");
assert.strictEqual(sent(), 3, "a generic family was refused as a font the machine does not have");
`)
}

// The disconnected panel said the connection was lost and offered a button,
// and nothing about the window already trying again every couple of seconds,
// or about what to do if flockdeck had stopped - so nobody could tell whether
// to wait, press the button, or go and start something.
func TestTheDisconnectedPanelSaysWhatIsHappening(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.control.onclose();
assert.ok(!h.$("disconnected").hidden, "losing the connection did not show the panel");
const text = h.$("disconnected-desc").textContent;
assert.ok(/tries again on its own/.test(text), "the panel does not say it is already trying again: " + text);
assert.ok(/start it again/.test(text), "the panel does not say what to do if flockdeck has stopped: " + text);
`)
}

// Every terminal announces itself alike and the header beside it names
// nothing, so a screen reader landing in one of six agents' terminals was not
// told whose it was. Each pane is a group named after the pane, and the name
// follows a rename.
func TestAPaneIsNamedForAScreenReader(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const wrap = h.terms[0].host.parentElement.parentElement;
assert.strictEqual(wrap.getAttribute("role"), "group", "a pane is not a group a screen reader can name");
assert.strictEqual(wrap.getAttribute("aria-label"), "agent p1", "a pane does not say whose it is");
h.recv(fixture({ panes: { p1: pane("p1", { name: "api" }), p2: pane("p2") } }));
assert.strictEqual(wrap.getAttribute("aria-label"), "api", "the pane's name did not follow a rename");
`)
}

// Tab inside a terminal belongs to the program in it, and nothing moved the
// keyboard from one pane to another, so in a tab of six agents somebody
// without a mouse stayed in the terminal they were in. The palette moves it to
// the next pane or the previous one, coming round at either end.
func TestTheKeyboardCanMoveBetweenPanes(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture({ tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2"), leaf("n3", "p3")]) }],
  panes: { p1: pane("p1"), p2: pane("p2"), p3: pane("p3") } }));
paletteRun("focus the next pane");
assert.deepStrictEqual(h.commands().pop(), { cmd: "focusPane", id: "p2" }, "the keyboard cannot be moved to the next pane");
paletteRun("focus the previous pane");
assert.deepStrictEqual(h.commands().pop(), { cmd: "focusPane", id: "p3" }, "going back from the first pane did not come round to the last");
`)
}

// The tab strip's buttons are tabs, and the pages they show were not marked as
// the panels they control, so a screen reader heard "tab 2 of 4" and nothing
// tied it to the panes below.
func TestATabNamesThePanelItShows(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const tab = h.$("tab-t1");
const page = h.$("page-t1");
assert.ok(page, "the tab's page has no id to be controlled by");
assert.strictEqual(page.getAttribute("role"), "tabpanel", "the tab's page is not a tab panel");
assert.strictEqual(tab.getAttribute("aria-controls"), "page-t1", "the tab does not say which panel it shows");
assert.strictEqual(page.getAttribute("aria-labelledby"), tab.id, "the panel is not labelled by its tab");
`)
}

// The prompt bar opened empty every time, so an instruction half-written for
// every agent went with an Escape pressed a moment too soon, and never sent,
// it was not in the history either. It is there when the bar opens again, and
// sending it clears it.
func TestThePromptBarKeepsAnUnsentDraft(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const input = h.$("prompt-input");
h.press("promptAll");
input.value = "stop and run the tests";
h.key({ key: "Escape" });
h.press("promptAll");
assert.strictEqual(input.value, "stop and run the tests", "closing the bar threw away what was being written");
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "sendPrompt", id: "p1", text: "stop and run the tests" });
h.press("promptAll");
assert.strictEqual(input.value, "", "a prompt that was sent came back as an unsent draft");
`)
}

// The find bar opened empty every time, so looking for the same word again -
// after closing the bar, or in the next pane - meant typing it again. The
// last search comes back selected, so Enter repeats it and typing replaces it.
func TestTheFindBarRemembersTheLastSearch(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const input = h.$("search-input");
h.press("findInTerminal");
input.value = "panic";
h.key({ key: "Escape" });
h.press("findInTerminal");
assert.strictEqual(input.value, "panic", "the last search was not there when the bar opened again");
assert.deepStrictEqual([input.selectionStart, input.selectionEnd], [0, 5], "the last search is not selected, so typing adds to it");
h.key({ key: "Enter" });
assert.deepStrictEqual(h.searchers[0].forward.slice(-1), ["panic"], "Enter did not repeat the last search");
`)
}

// Starting an agent in a worktree just created is what nearly everybody does
// next, and the keyboard was left on Create with the new row's Agent button to
// be found among the others. When the list comes back with the new worktree,
// the keyboard is on its Agent button.
func TestANewWorktreeIsReadyForAnAgent(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
const list = (items) => ({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [], items });
h.recv(list([{ label: "main", path: "C:/repo", main: true }]));
const branch = h.$("wt-branch");
branch.value = "fix-auth";
branch.focus();
h.key({ key: "Enter" });
h.recv(list([{ label: "main", path: "C:/repo", main: true },
             { label: "fix-auth", path: "C:/repo-fix-auth", dirty: 0, untracked: 0 }]));
await h.sleep(10);
const row = h.$("overlay-body").querySelectorAll("div.wt-row")[1];
const agent = row.querySelector("button");
assert.strictEqual(agent.textContent, "Agent");
assert.ok(h.doc.activeElement === agent, "the keyboard was not left on the new worktree's Agent button");
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "newTab", kind: "agent", path: "C:/repo-fix-auth", text: "fix-auth" });
`)
}

// Creating a worktree in a project spanning more than one repo used to
// silently always land in whichever repo happened to be active, with no
// way to ask for another -- the repo picker beside the form is the same
// question Changes' own picker already answers for review, and the
// branches on offer follow whichever member is picked rather than mixing
// every member's branches into one list.
func TestWorktreeCreateOffersARepoPickerForAMultiRepoProject(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/api", name: "platform", active: true, tabs: 2, waiting: 0, working: 0,
    members: [{ root: "C:/api", name: "api" }, { root: "C:/ui", name: "ui" }] },
] }));
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/api", defaultBase: "main",
  items: [{ label: "main", path: "C:/api", main: true, repo: "api", repoRoot: "C:/api" }],
  branches: [
    { name: "main", checkedIn: "main", repo: "api", repoRoot: "C:/api" },
    { name: "feature-x", repo: "ui", repoRoot: "C:/ui" },
  ] });

// Defaults to the active repo, and only that repo's free branches show.
const chips = () => Array.from(h.$("overlay-body").querySelector(".rev-repo-picker").querySelectorAll("button"));
assert.deepStrictEqual(chips().map((b) => b.textContent), ["api", "ui"], "the picker did not list both members");
assert.ok(!h.$("overlay-body").textContent.includes("feature-x"), "a branch of the other repo showed before it was picked");

// Picking the other member switches which repo's free branches are on offer.
h.click(chips().find((b) => b.textContent === "ui"));
assert.ok(h.$("overlay-body").textContent.includes("feature-x"), "picking ui did not bring in its own branches");

const branch = h.$("wt-branch");
branch.value = "fix-auth";
branch.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeAdd", text: "fix-auth", base: "main", root: "C:/ui" },
  "the new worktree did not target the repo picked");
`)
}

// A project spanning more than one repo lists every member's worktrees
// together, so each row has to say which repo it belongs to -- the same
// information the create form's own picker uses, shown here as a plain tag
// beside the worktree's own label. Left out for a project of one, which is
// every project this list showed before one could span more than one repo.
func TestWorktreeRowsShowWhichRepoTheyBelongToInAMultiRepoProject(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/api", name: "platform", active: true, tabs: 2, waiting: 0, working: 0,
    members: [{ root: "C:/api", name: "api" }, { root: "C:/ui", name: "ui" }] },
] }));
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/api", defaultBase: "main",
  items: [
    { label: "main", path: "C:/api", main: true, repo: "api", repoRoot: "C:/api" },
    { label: "main", path: "C:/ui", main: true, repo: "ui", repoRoot: "C:/ui" },
  ], branches: [] });
const rows = h.$("overlay-body").querySelectorAll("div.wt-row");
assert.strictEqual(rows.length, 2, "both members' checkouts should be listed");
assert.ok(rows[0].textContent.includes("api"), "the first row did not say which repo it belongs to");
assert.ok(rows[1].textContent.includes("ui"), "the second row did not say which repo it belongs to");

// A project of one repo shows no such tag at all -- no other flag either,
// here, so any ".wt-flag" found could only be a spurious repo tag.
h.recv(fixture());
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main",
  items: [{ label: "solo", path: "C:/repo" }], branches: [] });
const soloRow = h.$("overlay-body").querySelector("div.wt-row");
assert.ok(!soloRow.querySelector(".wt-flag"), "a project of one repo showed a repo tag on its own checkout");
`)
}

// A redraw finds the control the keyboard was on by what it is, and by
// wording alone one row's Remove is the next row's. Removing a clean worktree,
// which does not ask, left the keyboard on the next one's Remove, and a second
// Enter removed that one too.
func TestRemovingAWorktreeDoesNotAimAtTheNext(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
const list = (items) => ({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [], items });
const main = { label: "main", path: "C:/repo", main: true };
const a = { label: "a", path: "C:/repo-a", dirty: 0, untracked: 0 };
const b = { label: "b", path: "C:/repo-b", dirty: 0, untracked: 0 };
h.recv(list([main, a, b]));
const removeA = h.$("overlay-body").querySelectorAll("button").filter((x) => x.textContent === "Remove")[0];
removeA.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeRemove", path: "C:/repo-a", force: false });

h.recv(list([main, b]));
const now = h.doc.activeElement;
assert.ok(now.textContent !== "Remove", "the keyboard landed on another worktree's Remove button");
assert.ok(h.$("overlay-body").contains(now), "the keyboard fell out of the dialog");
const before = h.commands().length;
h.key({ key: "Enter" });
assert.ok(!h.commands().slice(before).some((c) => c.cmd === "worktreeRemove"), "a second Enter removed another worktree");
`)
}

// A worktree whose folder was deleted outside git came back with an empty
// status, and the panel drew it as clean, with an Agent, a Shell and a Review
// that could only fail in a folder that is not there. It says the folder is
// gone and offers the one thing left to do: prune git's record of it.
func TestAWorktreeWhoseFolderIsGoneOffersOnlyPrune(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [], items: [
  { label: "main", path: "C:/repo", main: true },
  { label: "old-fix", path: "C:/repo-old-fix", prunable: true, dirty: 0, untracked: 0 },
] });
const rows = h.$("overlay-body").querySelectorAll("div.wt-row");
const gone = rows[1];
assert.ok(/folder gone/.test(gone.textContent), "the row does not say its folder is gone: " + gone.textContent);
assert.ok(!gone.querySelector(".wt-clean"), "a worktree with no folder was called clean");
const offered = gone.querySelectorAll("button").map((b) => b.textContent);
assert.deepStrictEqual(offered, ["Prune"], "a worktree with no folder offers " + offered.join(", "));
const kept = rows[0].querySelectorAll("button").map((b) => b.textContent);
assert.deepStrictEqual(kept, ["Agent", "Shell", "Split", "Review"], "the main worktree lost its buttons");

const before = h.commands().length;
gone.querySelector("button").focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().slice(before), [{ cmd: "worktreePrune" }], "Enter on the row's Prune did not prune");
`)
}

// Adding, removing and pruning a worktree all run git, and none of them said
// so while they were out, so a press that had registered looked exactly like
// one that had not -- the same gap Changes' own fetch, pull, push and commit
// close.
func TestAWorktreeOperationSaysItIsRunning(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
const list = (items) => ({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [], items });
const main = { label: "main", path: "C:/repo", main: true };
const spare = { label: "spare", path: "C:/spare", dirty: 0, untracked: 0 };
h.recv(list([main, spare]));

h.$("wt-branch").value = "fix-auth";
h.$("wt-branch").oninput();
const create = h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Create")[0];
h.click(create);
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeAdd", text: "fix-auth", base: "main" });
assert.strictEqual(create.textContent, "Creating\u2026", "the button did not say it was working");
const remove = h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Remove")[0];
assert.ok(remove.disabled, "the other buttons still invite a press");

// A second press while the first is in flight creates nothing more.
const sent = h.commands().length;
h.click(create);
assert.strictEqual(h.commands().length, sent, "a second create was sent while the first was in flight");

// It always ends by sending the listing back, which puts the buttons right.
h.recv(list([main, spare]));
const removeAgain = h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Remove")[0];
assert.ok(!removeAgain.disabled, "the buttons were left disabled");

// Remove and Prune say so too, and do not go twice.
h.click(removeAgain);
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeRemove", path: "C:/spare", force: false });
assert.strictEqual(removeAgain.textContent, "Removing\u2026");
const twice = h.commands().length;
h.click(removeAgain);
assert.strictEqual(h.commands().length, twice, "the remove was sent twice");

h.recv(list([main, spare]));
const prune = h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Prune")[0];
h.click(prune);
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreePrune" });
assert.strictEqual(prune.textContent, "Pruning\u2026");
`)
}

// A redraw finds the control the keyboard was on by what it is and what it
// says, and by wording alone one row's Clear is the next row's. Clearing one
// stored API key, which does not ask, left the keyboard on the next agent's
// Clear, and a second Enter cleared that key as well.
func TestClearingOneKeyDoesNotAimAtTheNext(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
paletteRun("api keys");
const keys = (aSet) => ({ type: "keys", items: [
  { agent: "anthropic", name: "Anthropic API", set: aSet, source: aSet ? "store" : "", vars: ["ANTHROPIC_API_KEY"] },
  { agent: "openai", name: "OpenAI API", set: true, source: "store", vars: ["OPENAI_API_KEY"] },
] });
h.recv(keys(true));
const clears = () => h.$("overlay-body").querySelectorAll("button").filter((b) => b.textContent === "Clear");
const first = clears()[0];
first.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "keyClear", id: "anthropic" });
h.recv(keys(false));
assert.ok(h.doc.activeElement !== clears()[0], "the keyboard landed on another agent's Clear");
// The harness keeps the keyboard on the removed button where a browser would
// put it on the page, so only the other agent's key is the question here.
const before = h.commands().length;
h.key({ key: "Enter" });
assert.ok(!h.commands().slice(before).some((c) => c.cmd === "keyClear" && c.id === "openai"),
  "a second Enter cleared another agent's key");
`)
}

// The projects dialog's Open section was drawn only when a recents or folder
// listing arrived, and closing a project answers with a state push alone: the
// closed project stayed listed as open, its close button doing nothing.
func TestTheProjectsDialogFollowsTheOpenProjects(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const projects = (list) => list.map((name, i) =>
  ({ root: "C:/" + name, name, active: i === 0, tabs: 1, waiting: 0, working: 0 }));
h.recv(fixture({ projects: projects(["repo", "api", "docs"]) }));
h.press("projects");
assert.ok(h.$("overlay-body").textContent.includes("docs"), "the open projects are not listed");
h.recv(fixture({ projects: projects(["repo", "api"]) }));
assert.ok(!h.$("overlay-body").textContent.includes("docs"), "a closed project is still listed as open");

// A push that changes nothing the dialog lists leaves it alone.
const row = h.$("overlay-body").querySelector("div.proj-row");
h.recv(fixture({ projects: projects(["repo", "api"]), working: 1 }));
assert.ok(h.$("overlay-body").querySelector("div.proj-row") === row, "a status push redrew the projects dialog");
`)
}

// A worktree's count of agents is taken when the list is read, and Remove
// refuses while any are there. Close them and the dialog went on counting
// them, refusing for agents that were gone until Refresh was pressed. The list
// is asked for again when panes open or close while the dialog is up.
func TestTheWorktreesDialogFollowsThePanes(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main", branches: [],
  items: [{ label: "fix", path: "C:/repo-fix", panes: 1, dirty: 0, untracked: 0 }] });
const asked = () => h.commands().filter((c) => c.cmd === "worktrees").length;
const before = asked();

// A push with the same panes asks for nothing.
h.recv(fixture({ working: 1 }));
assert.strictEqual(asked(), before, "the list was read again although no pane had opened or closed");

// A pane closes: the counts are read again.
h.recv(fixture({ tabs: [{ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
  root: { id: "n1", pane: "p1", weight: 1 } }], panes: { p1: pane("p1") } }));
assert.strictEqual(asked(), before + 1, "closing a pane left the worktree counts as they were");
`)
}

// The review was read once while the agents went on writing, so a review left
// open listed files as they had been minutes before. It reads the tree again
// when the pushes say its checkout's counts moved - but not while a push is
// under way, when a redraw would give back the buttons disabled until it
// answers.
func TestTheReviewFollowsTheWorkingTree(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const panesWith = (dirty) => ({ p1: pane("p1", { cwd: "C:/repo", dirty }), p2: pane("p2", { cwd: "C:/other" }) });
h.recv(fixture({ panes: panesWith(1) }));
h.click(h.$("btn-changes"));
const tree = { type: "changes", cwd: "C:/repo", branch: "main", hasRemote: true, upstream: "origin/main", ahead: 1,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] };
h.recv(tree);
const asked = () => h.commands().filter((c) => c.cmd === "changes").length;
const before = asked();

h.recv(fixture({ panes: panesWith(1), working: 1 }));
assert.strictEqual(asked(), before, "the tree was read again although its counts had not moved");

h.recv(fixture({ panes: panesWith(3) }));
assert.strictEqual(asked(), before + 1, "an agent writing more files left the review as it was");
assert.deepStrictEqual(h.commands().filter((c) => c.cmd === "changes").pop(), { cmd: "changes", path: "C:/repo", follow: true });
h.recv(tree);

// While a push is under way its buttons stay disabled: no redraw is asked for.
h.click(h.$("rev-push"));
const during = asked();
h.recv(fixture({ panes: panesWith(5) }));
assert.strictEqual(asked(), during, "the review was read again in the middle of a push");
`)
}

// A push carries only the active project's panes, so an agent that stopped
// to wait in another open project raised no desktop notification at all -
// while the window was behind something, which is when one is needed. Its
// project's waiting count rising is the event; clicking goes to that project.
func TestAnAgentWaitingInAnotherProjectIsNotified(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const projects = (apiWaiting) => [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 1, waiting: apiWaiting, working: 0 },
];
h.recv(fixture({ projects: projects(0) }));
h.doc._hasFocus = false;
h.recv(fixture({ projects: projects(1) }));
assert.strictEqual(h.notifications.length, 1, "an agent waiting in another project raised no notification");
const n = h.notifications[0];
assert.ok(/api/.test(n.title), "the notification does not say which project: " + n.title);
n.onclick();
assert.deepStrictEqual(h.commands().pop(), { cmd: "selectProject", root: "C:/api" });

// The same count again is not news.
h.recv(fixture({ projects: projects(1) }));
assert.strictEqual(h.notifications.length, 1, "a count that did not rise raised another notification");
`)
}

// The hint explaining the amber dot came up whenever anything was waiting,
// and the waiting count is every project's: an agent waiting in a project not
// on screen had the hint explaining a dot that was nowhere to be seen.
func TestTheWaitingHintNeedsADotToPointAt(t *testing.T) {
	runFrontEnd(t, `
h.hello({ dismissedTips: ["palette", "drag-panes"] });
h.recv(fixture({ waiting: 1, projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 1, waiting: 1, working: 0 },
] }));
assert.ok(h.$("hints").hidden || !/amber dot/.test(h.$("hints").textContent),
  "the hint explained an amber dot that is not on screen");

h.recv(fixture({ waiting: 1, panes: { p1: pane("p1", { status: "waiting" }), p2: pane("p2") } }));
assert.ok(!h.$("hints").hidden && /amber dot/.test(h.$("hints").textContent),
  "the hint no longer shows when a waiting pane is on screen");
`)
}

// Choosing a project from the palette showed only each one's folder, when
// whether an agent there is waiting on you is what decides where to go next.
func TestThePaletteSaysWhichProjectsAreWaiting(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 },
  { root: "C:/api", name: "api", active: false, tabs: 1, waiting: 2, working: 0 },
  { root: "C:/docs", name: "docs", active: false, tabs: 1, waiting: 0, working: 0 },
] }));
h.press("palette");
const input = h.$("palette-input");
input.value = "switch to project";
input.oninput();
const rows = h.$("palette-list").children;
const hint = (name) => rows.find((r) => r.querySelector(".pal-label").textContent === "Switch to project: " + name)
  .querySelector(".pal-hint").textContent;
assert.ok(/2 waiting/.test(hint("api")), "the palette does not say agents are waiting in api: " + hint("api"));
assert.ok(!/waiting/.test(hint("docs")), "a project with nobody waiting says somebody is");
`)
}

// The tab strip marks a tab with an agent waiting in it, and the palette's
// entry for the same tab said nothing.
func TestThePaletteSaysWhichTabsAreWaiting(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({
  tabs: [
    { id: "t1", title: "one", focus: "p1", root: leaf("n1", "p1") },
    { id: "t2", title: "two", focus: "p2", attention: true, root: leaf("n2", "p2") },
    { id: "t3", title: "three", focus: "p3", root: leaf("n3", "p3") },
  ],
  panes: { p1: pane("p1"), p2: pane("p2", { status: "waiting" }), p3: pane("p3") },
}));
h.press("palette");
const input = h.$("palette-input");
input.value = "go to tab";
input.oninput();
const rows = h.$("palette-list").children;
const hint = (title) => {
  const row = rows.find((r) => r.querySelector(".pal-label").textContent === "Go to tab: " + title);
  const span = row.querySelector(".pal-hint");
  return span ? span.textContent : "";
};
assert.ok(/waiting/.test(hint("two")), "the palette does not say an agent is waiting in tab two");
assert.ok(!/waiting/.test(hint("three")), "a tab with nobody waiting says somebody is");
`)
}

// Every setting is a palette command, and typing "settings" - what somebody
// looking for one types - found none of them.
func TestThePaletteFindsTheSettingsByThatName(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("palette");
const input = h.$("palette-input");
const labels = (q) => {
  input.value = q;
  input.oninput();
  return h.$("palette-list").children.map((r) => r.querySelector(".pal-label")).filter(Boolean).map((l) => l.textContent);
};
for (const q of ["settings", "preferences", "options"]) {
  const found = labels(q);
  for (const want of ["Terminal scrollback…", "Terminal font…", "Increase font size", "Turn desktop notifications off", "Stop the terminal cursor blinking", "Turn update checks off"]) {
    assert.ok(found.includes(want), "typing " + q + " does not find " + want + ": " + JSON.stringify(found));
  }
  assert.ok(!found.includes("Rename this tab…"), "typing " + q + " offers a command that is not a setting");
}
`)
}

// Scrolled back, a pane showed the old lines with nothing saying newer ones
// had arrived, and the way back down was the wheel however far that was.
func TestAScrolledBackPaneOffersTheWayBackDown(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const term = h.terms[0];
const latest = term.host.parentElement.querySelector(".to-latest");
assert.ok(latest, "a pane has no way back to its newest output");
const buf = term.buffer.active;
buf.baseY = 100; buf.viewportY = 100;
term._parsed();
assert.ok(latest.hidden, "the way back down shows while the pane is at the bottom");
buf.viewportY = 40;
term._scroll(40);
assert.ok(!latest.hidden, "a pane scrolled back does not offer the way back down");
buf.baseY = 120;
term._parsed();
assert.ok(!latest.hidden, "new output hides the way back while the pane is still scrolled back");
latest.onclick({ stopPropagation() {} });
assert.strictEqual(buf.viewportY, buf.baseY, "the button does not go to the newest output");
assert.ok(latest.hidden, "the button stays after going to the bottom");
assert.ok(term.focused, "the keyboard is not back in the terminal");
`)
}

// The palette's settings said nothing of what each was set to: finding out
// the scrollback or the font meant opening the question that changes it.
func TestThePaletteSaysWhatEachSettingIsNow(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("palette");
const input = h.$("palette-input");
const hint = (label) => {
  input.value = label.toLowerCase();
  input.oninput();
  const row = h.$("palette-list").children.find((r) => r.querySelector(".pal-label") && r.querySelector(".pal-label").textContent === label);
  assert.ok(row, label + " is not in the palette");
  const span = row.querySelector(".pal-hint");
  return span ? span.textContent : "";
};
assert.ok(/Ctrl\+=/.test(hint("Increase font size")), "the font size command lost its key");
assert.ok(/13px/.test(hint("Increase font size")), "the font size command does not say the size: " + hint("Increase font size"));
assert.ok(/\d lines/.test(hint("Terminal scrollback…")), "the scrollback command does not say how many lines: " + hint("Terminal scrollback…"));
assert.ok(/default/.test(hint("Terminal font…")), "the font command does not say which font: " + hint("Terminal font…"));
`)
}

// The middle button closes a tab in a browser or an editor, and on this strip
// it did nothing.
func TestTheMiddleButtonClosesATab(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({
  tabs: [
    { id: "t1", title: "one", focus: "p1", root: leaf("n1", "p1") },
    { id: "t2", title: "two", focus: "p2", root: leaf("n2", "p2") },
  ],
  panes: { p1: pane("p1"), p2: pane("p2") },
}));
const two = h.$("tabs").children[1];
assert.ok(two.onauxclick, "a tab does not answer the middle button");
let stopped = false;
two.onauxclick({ button: 1, preventDefault() { stopped = true; } });
assert.deepStrictEqual(h.commands().pop(), { cmd: "closeTab", id: "t2" }, "the middle button did not close the tab");
assert.ok(stopped, "the browser was left to act on the middle button too");
const before = h.commands().length;
two.onauxclick({ button: 2, preventDefault() {} });
assert.strictEqual(h.commands().length, before, "the right button closed a tab");
`)
}

// The header cuts a long branch, or an agent with a long model, short, and
// their bubbles explained what a branch or an agent is rather than which one:
// the whole name could be read nowhere.
func TestThePaneHeaderBubblesNameTheBranchAndAgent(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const branch = "feature/retry-the-release-upload-when-the-relay-drops";
h.recv(fixture({ panes: { p1: pane("p1", { branch, agent: "claude", model: "claude-opus-5-with-a-long-name" }) } }));
// The terminal's host sits in the pane's body, which sits in the pane.
const wrap = h.terms[0].host.parentElement.parentElement;
const tip = (cls) => wrap.querySelector(cls).dataset.tip || "";
assert.ok(tip(".pane-branch").includes(branch), "the branch's bubble does not name the branch: " + tip(".pane-branch"));
assert.ok(tip(".pane-agent").includes("claude-opus-5-with-a-long-name"), "the agent's bubble does not name the model: " + tip(".pane-agent"));
assert.ok(/working tree/.test(tip(".pane-branch")), "the branch's bubble lost its explanation");
`)
}

// Someone at the desk can be surprised by text appearing in a pane because a
// phone -- their own, or a colleague's -- is also driving it. The header
// shows a small phone glyph while any window reached through the relay has
// the pane open, in its chat view or its terminal, naming the device in its
// tooltip and its aria-label; nothing at all while no phone has it open.
func TestThePaneHeaderShowsAPhoneHasItOpen(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));
const wrap = h.terms[0].host.parentElement.parentElement;
const remote = () => wrap.querySelector(".pane-remote");
assert.strictEqual(remote().textContent, "", "the glyph showed before any phone had this pane open");
assert.ok(!remote().getAttribute("aria-label"), "the glyph named a device before any phone had this pane open");

h.recv(fixture({ panes: { p1: pane("p1", { remoteViewers: ["Jim's iPhone"] }) } }));
assert.notStrictEqual(remote().textContent, "", "a phone has this pane open, but no glyph appeared");
assert.ok((remote().getAttribute("aria-label") || "").includes("Jim's iPhone"),
  "the glyph's aria-label does not name the device: " + remote().getAttribute("aria-label"));
assert.ok((remote().dataset.tip || "").includes("Jim's iPhone"),
  "the glyph's bubble does not name the device: " + remote().dataset.tip);

// Two phones on the same pane: both are named.
h.recv(fixture({ panes: { p1: pane("p1", { remoteViewers: ["Jim's iPhone", "Sam's iPad"] }) } }));
const label = remote().getAttribute("aria-label") || "";
assert.ok(label.includes("Jim's iPhone") && label.includes("Sam's iPad"),
  "the glyph does not name both devices: " + label);

// The phone leaves: the glyph goes with it.
h.recv(fixture({ panes: { p1: pane("p1") } }));
assert.strictEqual(remote().textContent, "", "the glyph stayed after every phone closed the pane");
assert.ok(!remote().getAttribute("aria-label"), "the glyph's name stayed after every phone closed the pane");
`)
}

// A pane's own name is a bare directory, the same for every pane working in
// it, and a tab's title is cut to a couple of words to fit the strip -- so
// neither says which of several fanned-out siblings a header belongs to.
// Task is the opening prompt underneath both, sent whole, and its bubble
// answers for the header as well as the name label, since most of the row
// carries no tip of its own.
func TestThePaneHeaderShowsItsFullTask(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1", { name: "repo" }) } }));
const header = h.doc.querySelector("div.pane-header");
const label = header.querySelector("span.pane-name");
assert.strictEqual(label.dataset.tip, "repo", "a task-less pane's bubble should say only its name");
assert.ok(!header.dataset.tip || header.dataset.tip === "repo",
  "a task-less pane's header should say no more than the name label does");

const task = "Investigate why the checkout pane keeps losing its focus after a restart.";
h.recv(fixture({ panes: { p1: pane("p1", { name: "repo", task }) } }));
assert.ok(label.dataset.tip.includes(task), "the name's bubble does not carry the full task: " + label.dataset.tip);
assert.ok(header.dataset.tip.includes(task), "the header's own bubble does not carry the full task: " + header.dataset.tip);

// Bounded, so an agent given an enormous task cannot fill the screen with it.
const huge = "word ".repeat(400);
h.recv(fixture({ panes: { p1: pane("p1", { name: "repo", task: huge } ) } }));
assert.ok(label.dataset.tip.length < huge.length, "an enormous task should be cut short in the bubble");
assert.ok(label.dataset.tip.endsWith("…"), "a cut-short task should say so: " + label.dataset.tip);

// And it goes with the task once the pane no longer has one -- a restart
// with a fresh conversation, say.
h.recv(fixture({ panes: { p1: pane("p1", { name: "repo" }) } }));
assert.strictEqual(label.dataset.tip, "repo", "the old task's bubble stayed after it was cleared");
`)
}

// Flockdeck cannot learn the name another Claude session would address a
// pane by on its own -- only the agent running inside it can, typically by
// calling its own ListAgents tool, and it hands that back with `flockdeck
// peer-name`. The header shows nothing until that happens, which is most
// panes, most of the time, and the badge names it in its bubble once it
// does.
func TestThePaneHeaderShowsAReportedPeerName(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1") } }));
const wrap = h.terms[0].host.parentElement.parentElement;
const peer = () => wrap.querySelector(".pane-peer");
assert.strictEqual(peer().textContent, "", "the badge showed before any name was reported");

h.recv(fixture({ panes: { p1: pane("p1", { peerName: "flockdeck-8d" }) } }));
assert.ok(peer().textContent.includes("flockdeck-8d"), "the badge does not show the reported name: " + peer().textContent);
assert.ok((peer().dataset.tip || "").includes("flockdeck-8d"),
  "the badge's bubble does not name it: " + peer().dataset.tip);

// Nothing else exposes this once it is reported: it is not offered as
// though Flockdeck itself could vouch for it.
h.recv(fixture({ panes: { p1: pane("p1") } }));
assert.strictEqual(peer().textContent, "", "the badge stayed after the pane no longer reported one");
`)
}

// A pane's header counts its checkout's changes, and nothing went from the
// counts to the changes: the review was a trip to the top bar.
func TestAPanesChangeCountsOpenItsReview(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1", { cwd: "C:/repo/sub", dirty: 3 }) } }));
const wrap = h.terms[0].host.parentElement.parentElement;
const git = wrap.querySelector("span.pane-git");
assert.ok(git.onclick, "a pane's changes cannot be clicked through to");
git.onclick({ stopPropagation() {} });
assert.deepStrictEqual(h.commands().pop(), { cmd: "changes", path: "C:/repo/sub" }, "the review is not of the pane's checkout");
assert.ok(!h.$("overlay").hidden, "the review did not open");
assert.ok(/review/i.test(git.dataset.tip || ""), "the bubble does not say the counts open the review: " + git.dataset.tip);
`)
}

// A checkout git stopped answering in kept its old counts in the header,
// drawn as though they were current: a pane on a checkout that had hung went
// on looking clean, or as far ahead as it last was, for as long as it hung.
func TestAPaneWhoseGitTimedOutSaysSoInsteadOfItsOldCounts(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const counts = { cwd: "C:/repo", dirty: 3, untracked: 1, ahead: 2 };
h.recv(fixture({ panes: { p1: pane("p1", Object.assign({ gitTimedOut: true }, counts)) } }));
const wrap = h.terms[0].host.parentElement.parentElement;
const git = wrap.querySelector("span.pane-git");
assert.ok(/timed out/.test(git.textContent), "the header does not say git timed out: " + git.textContent);
assert.ok(!/[0-9]/.test(git.textContent), "the header still shows the old counts: " + git.textContent);
assert.ok(/timed out/i.test(git.getAttribute("aria-label") || ""), "a screen reader is not told: " + git.getAttribute("aria-label"));
assert.ok(/out of date/.test(git.dataset.tip || ""), "the bubble does not say why nothing is counted: " + git.dataset.tip);
assert.ok(/review/i.test(git.dataset.tip || "") && git.onclick, "the checkout can no longer be reviewed from the header");

// The refresh that answers brings the counts back, though they are the ones
// that were there before.
h.recv(fixture({ panes: { p1: pane("p1", counts) } }));
assert.ok(!/timed out/.test(git.textContent), "the header still says git timed out: " + git.textContent);
assert.ok((git.getAttribute("aria-label") || "").includes("3 changed"), "got: " + git.getAttribute("aria-label"));
`)
}

// What a pane is doing is cut short in its header - an MCP tool's name
// nearly always is - and it had no bubble, so the rest could be read nowhere.
func TestThePaneDetailCanBeReadInFull(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const tool = "mcp__github__create_pull_request_review_comment";
h.recv(fixture({ panes: { p1: pane("p1", { status: "working", detail: tool }) } }));
const wrap = h.terms[0].host.parentElement.parentElement;
const detail = wrap.querySelector(".pane-detail");
assert.strictEqual(detail.dataset.tip, tool, "the detail cut short in the header has no bubble with the whole of it");
h.recv(fixture({ panes: { p1: pane("p1", { status: "idle", detail: "" }) } }));
assert.ok(!detail.dataset.tip, "a bubble is left on a detail that is no longer there");
`)
}

// The folder a dialog is about is set at its foot and cut short from the
// end, where paths differ, and it had no bubble: which checkout the dialog
// was for could not be read.
func TestTheFolderADialogIsAboutCanBeReadInFull(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const root = "C:/code/checkouts/flockdeck-review-second-copy";
h.click(h.$("btn-worktrees"));
h.recv({
  type: "worktrees", root, defaultBase: "main",
  items: [{ label: "main", path: root, main: true, dirty: 0, untracked: 0, head: "abc1234" }],
  branches: [{ name: "main", checkedIn: true }],
});
const line = h.$("overlay-body").querySelector(".wt-root");
assert.ok(line, "the worktrees dialog does not say which folder it is about");
assert.strictEqual(line.dataset.tip, root, "the folder cut short at the foot of the dialog has no bubble with the whole of it");
`)
}

// F3 and Shift+F3 step through what was found in nearly every Windows
// program; here they opened the browser's own find bar over the page.
func TestF3StepsThroughTheTerminalsMatches(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("findInTerminal");
const [s] = h.searchers;
h.$("search-input").value = "panic";
const next = h.key({ key: "F3" });
assert.deepStrictEqual(s.forward, ["panic"], "F3 did not go to the next match");
assert.ok(next.defaultPrevented, "F3 was left to the browser, which opens a find bar of its own");

// With the keyboard gone from the box, the bar still open.
h.$("tabs").children[0].focus();
h.key({ key: "F3", shiftKey: true });
assert.deepStrictEqual(s.back, ["panic"], "Shift+F3 away from the box did not go to the previous match");

// Closed, F3 is not the window's.
h.$("search-input").focus();
h.key({ key: "Escape" });
const after = h.key({ key: "F3" });
assert.deepStrictEqual(s.forward, ["panic"], "F3 searched with the find bar closed");
assert.ok(!after.defaultPrevented, "F3 was taken with the find bar closed");
`)
}

// A tab renamed by hand could never go back to naming itself, and nothing
// said how it might. The rename dialog sends an emptied name, which is the way
// back, offers "Use the automatic title" on a tab that has a name to give up,
// and says which of the two the tab is.
func TestARenamedTabCanGoBackToItsAutomaticTitle(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
const renames = () => h.commands().filter((c) => c.cmd === "renameTab");

// A tab that names itself has nothing to give up.
paletteRun("rename this tab");
assert.ok(!h.$("tab-auto-title"), "a tab that already names itself was offered its automatic title");
assert.ok(/names itself/.test(h.$("overlay-body").textContent), "the dialog does not say the tab names itself");
assert.ok(/empty/.test(h.$("tab-name").placeholder), "the field does not say what an empty name does");
h.$("tab-name").value = "";
h.key({ key: "Enter" });
assert.deepStrictEqual(renames().pop(), { cmd: "renameTab", id: "t1", text: "" },
  "an emptied name went nowhere, so nothing said why the tab kept its old one");

// A tab named by hand is offered the way back, from a double-click as much
// as from the palette.
const named = fixture();
named.tabs[0].named = true;
h.recv(named);
h.$("tabs").children[0].ondblclick();
assert.ok(h.doc.activeElement === h.$("tab-name"), "the name field does not have the keyboard");
const back = h.$("tab-auto-title");
assert.ok(back, "a tab named by hand was not offered its automatic title");
assert.ok(/You named this tab/.test(h.$("overlay-body").textContent), "the dialog does not say the tab was named by hand");
h.click(back);
assert.deepStrictEqual(renames().pop(), { cmd: "renameTab", id: "t1", text: "" });
assert.ok(h.$("overlay").hidden, "the dialog stayed open once the automatic title was chosen");

// Escape leaves the name as it was.
const before = renames().length;
paletteRun("rename this tab");
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape did not close the rename dialog");
assert.strictEqual(renames().length, before, "closing the dialog renamed the tab");
`)
}

// Installing an update stops every agent in every pane, and the dialog put
// the keyboard on Restart now: a chip clicked by mistake while typing to an
// agent had the next Enter stop them all.
func TestTheUpdateDialogStartsOnTheChoiceThatStopsNothing(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ update: { version: "9.9.9" } }));
h.click(h.$("btn-update"));
const on = h.doc.activeElement;
assert.ok(on && on.textContent === "Later", "the keyboard starts on " + (on && on.textContent) + ", not on Later");
`)
}

// Forgetting a recent project took away the row the keyboard was on, and the
// redraw had nowhere to put it: it fell out of the dialog, so clearing out a
// second old project meant tabbing all the way back to the list.
func TestForgettingARecentProjectKeepsTheKeyboardInTheList(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("projects");
const recent = (names) => ({ type: "recents", items: names.map((n) =>
  ({ root: "C:/old/" + n, name: n, exists: true, open: false })) });
h.recv(recent(["alpha", "beta", "gamma"]));
const forgets = () => h.$("overlay-body").querySelectorAll("button.proj-forget");
assert.strictEqual(forgets().length, 3, "each recent project has its ×");
forgets()[1].focus();
h.click(forgets()[1]);
assert.deepStrictEqual(h.commands().pop(), { cmd: "removeProject", root: "C:/old/beta" });
h.recv(recent(["alpha", "gamma"]));
assert.ok(h.doc.activeElement === forgets()[1], "the keyboard did not go to the project that took the forgotten one's place");

// The last one gone, the keyboard goes to the next row up.
h.click(forgets()[1]);
h.recv(recent(["alpha"]));
assert.ok(h.doc.activeElement === forgets()[0], "forgetting the last project lost the keyboard");
`)
}

// Closing an open project from the dialog took away the row the keyboard was
// on, and it fell out of the dialog. It goes to the project that takes the
// closed one's place - to its own button, not its ×: an idle project closes
// without asking, so a second Enter there would stop its agents too.
func TestClosingAProjectKeepsTheKeyboardInTheDialog(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const projects = (names) => names.map((n, i) =>
  ({ root: "C:/" + n, name: n, active: i === 0, tabs: 1, waiting: 0, working: 0 }));
h.recv(fixture({ projects: projects(["repo", "api", "docs"]) }));
h.press("projects");
const body = h.$("overlay-body");
const closes = () => body.querySelectorAll("button.icon-btn").filter((b) => b.textContent === "\u00d7");
closes()[1].focus();
h.click(closes()[1]);
assert.deepStrictEqual(h.commands().pop(), { cmd: "closeProject", root: "C:/api" });
h.recv(fixture({ projects: projects(["repo", "docs"]) }));
const on = h.doc.activeElement;
assert.ok(on === body.querySelectorAll("button.proj-go")[1],
  "the keyboard did not go to the project that took the closed one's place: " + (on && on.className));
`)
}

// The harness has no style sheet, so an element the front end hides is
// hidden in every test - while in a browser a display set on its class
// beats the hidden attribute and keeps it on screen. These are the classes
// app.js hides whose rules set a display.
func TestWhatTheFrontEndHidesLeavesTheScreen(t *testing.T) {
	css := readAsset(t, "app.css")
	for cls, what := range map[string]string{
		"conv-row": "the conversations a filter leaves out stay on screen",
		"fan-opt":  "the fan-out's trust option stays on screen while no agent chosen asks for it",
	} {
		if !regexp.MustCompile(`\.` + cls + `\[hidden\]\s*\{\s*display:\s*none`).MatchString(css) {
			t.Errorf("app.css gives .%s a display and nothing puts a hidden one back to none, so %s", cls, what)
		}
	}
}

// A long history could be searched only by the start of a summary, and
// summaries often begin alike. A field narrows the list to the conversations
// holding every word typed.
func TestTheConversationsCanBeNarrowedByTyping(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-history"));
const conv = (id, summary) => ({ id, summary, ago: "1h", messages: 3, open: false });
h.recv({ type: "conversations", cwd: "C:/repo", items: [
  conv("aaaa1111", "Fix the login bug in auth"),
  conv("bbbb2222", "Fix the login page layout"),
  conv("cccc3333", "Write docs for the auth relay"),
] });
const field = h.$("history-filter");
assert.ok(field, "the conversations cannot be narrowed");
const rows = () => h.$("overlay-body").querySelectorAll("div.conv-row");
const shown = () => rows().filter((r) => !r.hidden).map((r) => r.querySelector(".conv-summary").textContent);

field.value = "fix login";
field.oninput();
assert.deepStrictEqual(shown(), ["Fix the login bug in auth", "Fix the login page layout"]);
field.value = "layout";
field.oninput();
assert.deepStrictEqual(shown(), ["Fix the login page layout"]);

// Down from the field goes to the first conversation left.
field.focus();
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === rows()[1], "Down from the field did not go to the conversation left");

// The arrows pass over the rows hidden: "auth" leaves the first and the
// third, and Down from the first goes to the third.
field.value = "auth";
field.oninput();
assert.deepStrictEqual(shown(), ["Fix the login bug in auth", "Write docs for the auth relay"]);
rows()[0].focus();
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === rows()[2], "Down went to a hidden conversation");

// Escape empties the field first and leaves the dialog open.
field.value = "layout";
field.oninput();
field.focus();
h.key({ key: "Escape" });
assert.strictEqual(field.value, "", "Escape did not empty the field");
assert.ok(!h.$("overlay").hidden, "Escape closed the dialog instead of emptying the field");
`)
}

// Dismissing a hint took away the button pressed, and the keyboard with it:
// it was left on nothing, and typing went nowhere until a terminal was
// clicked.
func TestDismissingAHintGivesTheKeyboardBackToTheTerminal(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const close = () => h.$("hints").querySelector("button.icon-btn");
assert.ok(!h.$("hints").hidden && close(), "no hint is showing to dismiss");
h.terms[0].blur();
close().focus();
h.click(close());
assert.deepStrictEqual(h.commands().filter((c) => c.cmd === "dismissTip").pop(), { cmd: "dismissTip", id: "palette" });
assert.ok(h.terms[0].focused, "dismissing the hint left the keyboard on nothing");
`)
}

// A hint just dismissed flickered back for a moment whenever a "prefs" push
// that had left the server before the dismissal did arrived after it: that
// push's own dismissedTips did not have the tip in it yet, and overwrote the
// one just added here, until the server's real answer followed and hid it
// again. keepPending folds a dismissal not yet confirmed back into every push
// until one finally does carry it, the same protection every other
// preference already had.
func TestDismissingAHintSurvivesAStalePrefsPush(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
assert.strictEqual(h.$("hints").dataset.hint, "palette", "the fixture's usual first hint is not showing");
h.click(h.$("hints").querySelector("button.icon-btn"));
assert.notStrictEqual(h.$("hints").dataset.hint, "palette", "dismissing did not move off the hint at once");

// A push that left the server before the dismissal did: it still says
// dismissedTips is empty, and a hint dismissed only a moment ago flickered
// back until the server's real answer followed and hid it again.
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [] } });
assert.notStrictEqual(h.$("hints").dataset.hint, "palette", "a stale push brought the dismissed hint back");

// The server's own answer, once it arrives, is trusted as it is.
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: ["palette"] } });
assert.notStrictEqual(h.$("hints").dataset.hint, "palette", "the confirmed push did not keep the hint dismissed");

// Once the true answer has come back, an empty list is no longer taken for
// the same stale echo -- another window resetting the tips is believed.
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [] } });
assert.strictEqual(h.$("hints").dataset.hint, "palette", "an empty list from elsewhere was not believed after confirmation");
`)
}

// "Show them again" has the same race the other way: a push that left before
// the reset did still carries the old dismissedTips, and put the tips away
// again for a moment until the server's own empty list caught up.
func TestResetTipsSurvivesAStalePrefsPush(t *testing.T) {
	runFrontEnd(t, `
h.hello({ dismissedTips: ["palette"] });
h.recv(fixture());
assert.notStrictEqual(h.$("hints").dataset.hint, "palette", "the dismissed hint is showing before the reset");
h.click(h.$("btn-settings"));
h.click(h.$("set-tips"));
assert.strictEqual(h.$("hints").dataset.hint, "palette", "resetting the tips did not bring it back at once");

// A push that left the server before the reset did: it still carries the
// old dismissedTips, and put the tip away again for a moment until the
// server's own empty list caught up.
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: ["palette"] } });
assert.strictEqual(h.$("hints").dataset.hint, "palette", "a stale push dismissed the hint again after resetting");

h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [] } });
assert.strictEqual(h.$("hints").dataset.hint, "palette", "the confirmed empty list did not keep the hint showing");
`)
}

// A pane's status flips idle/working on every tool call a busy agent makes,
// and "working-tool" -- like several other tips -- gates on it. Once it is
// the only tip left undismissed, that flipping used to show it and take it
// away again on the very next state push: a hint popping up and immediately
// disappearing, over and over, for as long as the pane kept working.
func TestAStatusGatedTipDoesNotFlickerWithTheStatusItGatesOn(t *testing.T) {
	runFrontEnd(t, `
h.hello({ dismissedTips: ["palette", "drag-panes", "shell-pane", "broadcast", "fanout",
  "worktrees", "history", "zoom", "tidy-panes", "find-in-terminal", "api-keys",
  "remote-access", "detach", "split-project", "pane-header-git"] });
h.recv(fixture());
assert.ok(h.$("hints").hidden, "something is shown before any pane has worked at all");

h.recv(fixture({ panes: { p1: pane("p1", { status: "working" }), p2: pane("p2") } }));
assert.strictEqual(h.$("hints").dataset.hint, "working-tool", "showing the only tip left, from nothing, was held up");

// The tool call finishes: back to idle, on the very next push, is exactly
// the flip that used to hide the tip again at once.
h.recv(fixture({ panes: { p1: pane("p1", { status: "idle" }), p2: pane("p2") } }));
assert.strictEqual(h.$("hints").dataset.hint, "working-tool", "the tip vanished the instant its status went away");

// And a flip straight back to working, before that settles, must not have
// shown anything else in between either -- the bar never budged.
h.recv(fixture({ panes: { p1: pane("p1", { status: "working" }), p2: pane("p2") } }));
assert.strictEqual(h.$("hints").dataset.hint, "working-tool", "flipping back before it settled still touched the bar");

// Idle for good, and long enough to settle, does eventually clear it.
h.recv(fixture({ panes: { p1: pane("p1", { status: "idle" }), p2: pane("p2") } }));
await h.sleep(1300);
assert.ok(h.$("hints").hidden, "a status gone for good never cleared the tip");
`)
}

// The find bar searches the pane it was opened for. Closing that pane left
// the bar open, still naming it, while Enter and F3 quietly did nothing.
func TestTheFindBarGoesWithThePaneItSearches(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const both = { tabs: [{ id: "t1", title: "pair", focus: "p1", root:
    split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  panes: { p1: pane("p1", { name: "reviewer" }), p2: pane("p2", { name: "builder" }) } };
h.recv(fixture(both));
h.press("findInTerminal");
assert.strictEqual(h.$("search-label").textContent, "Find in reviewer");
h.recv(fixture({ tabs: [{ id: "t1", title: "pair", focus: "p2", root: leaf("n2", "p2") }],
  panes: { p2: pane("p2", { name: "builder" }) } }));
assert.ok(h.$("searchbar").hidden, "the find bar stayed open for a pane that is gone");

// Closing the other pane leaves it alone.
h.recv(fixture(both));
h.press("findInTerminal");
h.recv(fixture({ tabs: [{ id: "t1", title: "pair", focus: "p1", root: leaf("n1", "p1") }],
  panes: { p1: pane("p1", { name: "reviewer" }) } }));
assert.ok(!h.$("searchbar").hidden, "closing another pane closed the find bar");
`)
}

// The tab strip hides its scroll bar, so with more tabs than fit, the ones
// past an end were out of sight with nothing to say they were there.
func TestTheTabStripSaysWhenTabsRunPastItsEnds(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const strip = h.$("tabs");
const ends = () => [strip.classList.contains("more-left"), strip.classList.contains("more-right")];
const scrolled = (left) => { strip.scrollLeft = left; h.dispatch(strip, new h.Ev("scroll", {})); };
strip.scrollWidth = 900; strip.clientWidth = 300;
scrolled(0);
assert.deepStrictEqual(ends(), [false, true], "tabs past the right end are not marked");
scrolled(300);
assert.deepStrictEqual(ends(), [true, true], "tabs past both ends are not marked");
scrolled(600);
assert.deepStrictEqual(ends(), [true, false], "tabs past the left end are not marked");
strip.scrollWidth = 300;
scrolled(0);
assert.deepStrictEqual(ends(), [false, false], "a strip whose tabs all fit is marked");

// A tab arriving is measured then, without waiting for the strip to scroll.
// A tab of its own, so the push is sure to bring one the strip did not have.
strip.scrollWidth = 900;
const base = fixture();
h.recv(fixture({
  tabs: base.tabs.concat([{ id: "t-new", title: "new", focus: "p-new", root: leaf("n-new", "p-new") }]),
  panes: Object.assign({}, base.panes, { "p-new": pane("p-new") }),
}));
assert.deepStrictEqual(ends(), [false, true], "a new tab running the strip past its end is not marked: " +
  JSON.stringify({ scrollWidth: strip.scrollWidth, clientWidth: strip.clientWidth, scrollLeft: strip.scrollLeft }));
`)
}

// A pane alone in its tab can be zoomed, and the zoom hides nothing; shown
// as zoomed anyway, its bubble said "0 other panes are hidden" and offered
// to bring them back.
func TestALonePaneIsNotShownAsZoomed(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ tabs: [{ id: "t1", title: "one", focus: "p1", zoom: true, root: leaf("n1", "p1") }],
  panes: { p1: pane("p1") } }));
const zoom = h.terms[0].host.parentElement.parentElement.querySelector("button.zoomed");
assert.ok(!zoom, "a pane with nothing to hide is shown as zoomed: " + (zoom && zoom.dataset.tip));

// Zoomed past another pane, it says so.
h.recv(fixture({ tabs: [{ id: "t1", title: "two", focus: "p1", zoom: true,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  panes: { p1: pane("p1"), p2: pane("p2") } }));
const on = h.doc.querySelectorAll("button.zoomed");
assert.strictEqual(on.length, 1, "the zoomed pane is not shown as zoomed");
assert.ok(/1 other pane is hidden/.test(on[0].dataset.tip), on[0].dataset.tip);
`)
}

// The prompt bar's field was one line, and a pasted instruction of several
// had its line breaks turned into spaces. The field holds lines now, and a
// paste is left to keep them.
func TestAPastedMultiLinePromptKeepsItsLines(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("promptAll");
const input = h.$("prompt-input");
assert.strictEqual(input.tagName, "TEXTAREA", "the prompt field holds one line");
const ev = new h.Ev("paste", { clipboardData: { getData: () => "1. add tests\r\n2. run them" } });
h.dispatch(input, ev);
assert.ok(!ev.defaultPrevented, "a paste of several lines was taken over rather than kept as it is");
`)
}

// Enter sends what is in the prompt bar, so a prompt of several lines could
// not be written there. Shift+Enter is left to the field, which starts a new
// line with it, and Enter then sends every line as one message.
func TestShiftEnterStartsANewLineInThePrompt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("promptAll");
const input = h.$("prompt-input");
input.value = "add tests";
const shifted = h.key({ key: "Enter", shiftKey: true });
assert.ok(!shifted.defaultPrevented, "Shift+Enter was kept from the field, which starts a new line with it");
assert.ok(!h.$("promptbar").hidden, "Shift+Enter sent the prompt");
assert.ok(!h.commands().some((c) => c.cmd === "sendPrompt"), "Shift+Enter sent the prompt");
input.value = "add tests\nrun them";
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "sendPrompt", id: "p1", text: "add tests\nrun them" });
assert.ok(h.$("promptbar").hidden, "sending did not close the bar");
`)
}

// A prompt field of one line's height showed one line of a prompt of five, and
// the four above it went to every agent unread. It grows with its lines.
func TestThePromptFieldGrowsWithItsLines(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("promptAll");
const input = h.$("prompt-input");
input.clientHeight = 18; // the harness's offsetHeight is 20: 2px of border
input.scrollHeight = 64;
input.value = "one\ntwo\nthree";
h.dispatch(input, new h.Ev("input", {}));
assert.strictEqual(input.style.height, "66px", "the field did not grow to show every line");
`)
}

// Up and Down recall the prompts sent before, and in a prompt of several lines
// they are also how the caret moves between its lines. They recall only from
// the first line and the last.
func TestUpAndDownMoveBetweenAPromptsLines(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const input = h.$("prompt-input");
h.press("promptAll");
input.value = "run the tests";
h.key({ key: "Enter" });
h.press("promptAll");
input.value = "one\ntwo";
input.setSelectionRange(5, 5);
assert.ok(!h.key({ key: "ArrowUp" }).defaultPrevented, "Up on the second line did not move to the first");
assert.strictEqual(input.value, "one\ntwo", "Up on the second line recalled a prompt");
input.setSelectionRange(1, 1);
assert.ok(!h.key({ key: "ArrowDown" }).defaultPrevented, "Down on the first line did not move to the second");
h.key({ key: "ArrowUp" });
assert.strictEqual(input.value, "run the tests", "Up on the first line did not recall the last prompt");
h.key({ key: "ArrowDown" });
assert.strictEqual(input.value, "one\ntwo", "Down did not come back to what was being written");
`)
}

// The prompt bar says how many panes its message will reach, and said it once,
// when it opened: a pane leaving the set while it was open left it promising
// agents the message would not reach.
func TestThePromptBarKeepsCountOfWhoItReaches(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const two = (member) => fixture({ tabs: [{ id: "t1", title: "pair", focus: "p1", root:
    split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) }],
  panes: { p1: pane("p1"), p2: pane("p2", { broadcast: member }) } });
h.recv(two(true));
h.press("promptAll");
assert.strictEqual(h.$("prompt-label").textContent, "Prompt → 2 panes");
h.recv(two(false));
assert.strictEqual(h.$("prompt-label").textContent, "Prompt", "the bar still counts a pane that left the set");
h.recv(two(true));
assert.strictEqual(h.$("prompt-label").textContent, "Prompt → 2 panes", "the bar does not count a pane added to the set");
`)
}

// Left on one of an agent's models closed the agent but left the highlight at
// the same place in the now shorter list, on whichever agent had moved up into
// it - and Enter started that agent instead of the one being looked at.
func TestClosingAnAgentsModelsLandsOnTheAgent(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const agent = (id, name, models) => ({ id, name, runner: "cli", available: true, defaultModel: "",
  models: models.map((m) => ({ id: m, name: m })) });
h.recv(fixture({ agents: catalog({ items: [
  agent("alpha", "Alpha", ["a1", "a2", "a3"]), agent("beta", "Beta", []), agent("gamma", "Gamma", []),
] }) }));
// The caret beside the + opens the picker; the action has no key of its own.
h.click(h.$("new-tab-pick"));
const sel = () => h.$("agent-list").querySelector(".sel").querySelector(".pick-name").textContent;
assert.strictEqual(sel(), "Alpha");
h.key({ key: "ArrowRight" });
h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
assert.strictEqual(sel(), "a3", "the third model is not the one picked out");
h.key({ key: "ArrowLeft" });
assert.strictEqual(sel(), "Alpha", "closing the agent's models left the highlight on another agent");
// Enter acts on the agent closed - opening its models again, since it has
// some - and not on the one the highlight used to slide onto.
h.key({ key: "Enter" });
assert.ok(!h.commands().some((c) => JSON.stringify(c).includes('"gamma"')), "Enter started another agent");
assert.ok(h.$("agent-list").querySelectorAll(".pick-name").some((n) => n.textContent === "a1"),
  "Enter did not act on the agent that was closed");
`)
}

// The picker asks for the catalog again as it opens, and the answer can come
// after the arrows have moved: an agent added ahead of the one picked out
// shifted every row, and the highlight named another agent.
func TestTheAgentPickedOutSurvivesTheCatalogArriving(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const agent = (id, name) => ({ id, name, runner: "cli", available: true, defaultModel: "", models: [] });
const three = [agent("alpha", "Alpha"), agent("beta", "Beta"), agent("gamma", "Gamma")];
h.recv(fixture({ agents: catalog({ items: three }) }));
h.click(h.$("new-tab-pick"));
const sel = () => h.$("agent-list").querySelector(".sel").querySelector(".pick-name").textContent;
h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
assert.strictEqual(sel(), "Gamma");
h.recv(fixture({ agents: catalog({ items: [agent("aardvark", "Aardvark")].concat(three) }) }));
assert.strictEqual(sel(), "Gamma", "the catalog arriving moved the highlight to another agent");

// Narrowing the list still starts from the first match.
const field = h.$("agent-filter");
field.value = "a";
field.oninput();
assert.strictEqual(sel(), "Aardvark", "typing did not start from the first match");

// Opened afresh, the picker starts on its first row, not on the one the last
// picker left picked out.
field.value = "gam";
field.oninput();
assert.strictEqual(sel(), "Gamma");
h.key({ key: "Escape" });
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "the picker did not close");
h.click(h.$("new-tab-pick"));
assert.strictEqual(sel(), "Aardvark", "a fresh picker started on the row the last one left picked out");
`)
}

// Enter in the new-worktree form's base field, the one filled in last, with
// no branch named did nothing and said nothing.
func TestAWorktreeWithNoBranchSaysWhatIsMissing(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main",
  items: [{ label: "main", path: "C:/repo", main: true, dirty: 0, untracked: 0, head: "abc1234" }],
  branches: [{ name: "main", checkedIn: true }] });
const branch = h.$("wt-branch");
const base = branch.parentElement.querySelectorAll("input")[1];
base.value = "main";
base.focus();
h.key({ key: "Enter" });
assert.ok(!h.commands().some((c) => c.cmd === "worktreeAdd"), "a worktree was asked for with no branch");
assert.ok(/branch/i.test(h.$("notice").textContent) && !h.$("notice").hidden, "nothing said the branch was missing");
assert.ok(h.doc.activeElement === branch, "the keyboard was not put where the branch goes");
`)
}

// A helper's row already named the pane that spawned it, in plain text with
// nothing to click. Going to that pane meant closing the overview, hunting
// for it by eye, or waiting for it to need you too.
func TestAHelperLinksToItsParentPane(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "planner", name: "planner", status: "working" },
  { paneId: "p2", tabId: "t1", root: "C:/repo", project: "repo", tab: "helper", name: "helper", status: "working", parent: "p1" },
] });
const rows = h.$("overlay-body").querySelectorAll("div.agent-row");
assert.strictEqual(rows.length, 2);
const link = rows[1].querySelector("button.agent-parent");
assert.ok(link, "the helper's row does not link to its parent");
assert.strictEqual(link.textContent, "\u21B3 helper of planner");

h.click(link);
assert.deepStrictEqual(h.commands().pop(), { cmd: "revealPane", root: "C:/repo", node: "t1", id: "p1" },
  "the link did not reveal the parent, not the helper's own pane");
assert.strictEqual(h.commands().filter((c) => c.cmd === "revealPane").length, 1,
  "clicking the parent link also revealed the helper's own pane");
assert.ok(h.$("overlay").hidden, "revealing the parent did not close the overview");

// Reached from the keyboard, not only the mouse, and without also firing
// the row's own reveal underneath it.
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "planner", name: "planner", status: "working" },
  { paneId: "p2", tabId: "t1", root: "C:/repo", project: "repo", tab: "helper", name: "helper", status: "working", parent: "p1" },
] });
const before = h.commands().length;
const link2 = h.$("overlay-body").querySelectorAll("div.agent-row")[1].querySelector("button.agent-parent");
link2.focus();
h.key({ key: "Enter" });
assert.strictEqual(h.commands().length - before, 1, "Enter on the link fired more than one reveal");
assert.deepStrictEqual(h.commands().pop(), { cmd: "revealPane", root: "C:/repo", node: "t1", id: "p1" });

// A parent that has since closed leaves nothing to reveal, so the row falls
// back to plain text rather than a link to nowhere.
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p3", tabId: "t1", root: "C:/repo", project: "repo", tab: "orphan", name: "orphan", status: "idle", parent: "gone" },
] });
const orphan = h.$("overlay-body").querySelector("div.agent-row");
assert.ok(!orphan.querySelector("button.agent-parent"), "a closed parent was linked to anyway");
assert.ok(orphan.textContent.includes("helper of another agent"), "the orphaned helper lost its own wording");
`)
}

// The overview is a flat list sorted by what needs a person first on
// purpose -- see the comment above renderAgents' byId map -- so the tree is
// offered beside it rather than instead of it.
func TestAgentsCanBeSeenAsATreeByProject(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "planner", name: "planner", status: "waiting" },
  { paneId: "p2", tabId: "t1", root: "C:/repo", project: "repo", tab: "helper one", name: "helper one", status: "working", parent: "p1" },
  { paneId: "p3", tabId: "t1", root: "C:/repo", project: "repo", tab: "helper two", name: "helper two", status: "idle", parent: "p1" },
  { paneId: "p4", tabId: "t2", root: "C:/other", project: "other", tab: "solo", name: "solo", status: "idle" },
] });

// By urgency, the default, is the flat list the server already sorted --
// no tree, no project headings.
assert.ok(!h.$("overlay-body").querySelector("div.agent-tree"), "the flat list drew a tree anyway");
let tabs = h.$("overlay-body").querySelectorAll("button.agent-view-tab");
assert.strictEqual(tabs.length, 2);
assert.strictEqual(tabs[0].textContent, "By urgency");
assert.strictEqual(tabs[0].getAttribute("aria-selected"), "true");
assert.strictEqual(tabs[1].getAttribute("aria-selected"), "false");

h.click(tabs[1]);

// Grouped by project, each under its own heading, alphabetically.
const sections = h.$("overlay-body").querySelectorAll(".proj-section");
const headings = sections.map((s) => s.querySelector("h3").textContent);
assert.deepStrictEqual(headings, ["other \u00b7 1 pane", "repo \u00b7 3 panes"]);

const tree = h.$("overlay-body").querySelector("div.agent-tree");
assert.ok(tree, "By project did not draw a tree");
assert.strictEqual(tree.querySelectorAll("div.agent-row").length, 4, "By project lost a pane");

// The two helpers are nested under their parent's row, not beside it, and
// each still carries its own status the way the flat list's rows do.
const nested = tree.querySelectorAll(".agent-children").flatMap((n) => n.querySelectorAll("div.agent-row"));
assert.strictEqual(nested.length, 2, "the helpers were not nested under their parent");
assert.ok(nested.every((r) => r.textContent.includes("helper")), "a nested row belongs to someone else");
assert.ok(nested.some((r) => r.querySelector("span.agent-status.working")), "a nested row lost its own status");
assert.ok(nested.some((r) => r.querySelector("span.agent-status.idle")));

// Switching back leaves the flat list exactly as the server sorted it.
tabs = h.$("overlay-body").querySelectorAll("button.agent-view-tab");
h.click(tabs[0]);
assert.ok(!h.$("overlay-body").querySelector("div.agent-tree"), "the tree stayed after switching back");
const rows = h.$("overlay-body").querySelectorAll("div.agent-row");
assert.strictEqual(rows[0].id, "agent-p1", "the waiting agent no longer leads the flat list");

// A fresh open of the dialog starts on urgency again, not wherever the last
// visit left off.
h.key({ key: "Escape" });
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "planner", name: "planner", status: "idle" },
] });
assert.ok(!h.$("overlay-body").querySelector("div.agent-tree"), "the dialog remembered project view across a reopen");
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
const crypto = require("crypto").webcrypto;

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

  // Reflected as a browser reflects it, so input[type=password] finds a field
  // whose type was set as a property.
  get type() { return this.attributes.get("type") || ""; }
  set type(v) { this.attributes.set("type", String(v)); }

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
  select() { this.selectionStart = 0; this.selectionEnd = String(this.value || "").length; }
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
    // What has been drawn, and what has been written and not yet parsed.
    // xterm's write() only queues while its reset() acts at once, so the two
    // are kept apart: a reset reaches the screen ahead of bytes still queued.
    this.screen = []; this.queue = []; this.disposed = false; this.focused = false;
    this.buffer = { active: { viewportY: 0, baseY: 0 } };
    // What is selected with the mouse, and what has been pasted through
    // paste(), which is how xterm is given text that did not come from keys.
    this.selection = ""; this.pasted = [];
    terms.push(this);
  }
  attachCustomKeyEventHandler(fn) { this._keys = fn; }
  hasSelection() { return !!this.selection; }
  getSelection() { return this.selection; }
  clearSelection() { this.selection = ""; }
  paste(d) { this.pasted.push(d); }
  /** written is what the screen shows once everything written so far has
   *  been parsed: whatever follows the last full reset in the stream. */
  get written() {
    const all = this.screen.concat(this.queue.map((w) => w.data));
    const at = all.lastIndexOf("\x1bc");
    return at < 0 ? all : all.slice(at + 1);
  }
  /** parse is xterm getting round to what was written, calling back each
   *  write as it finishes with it, in order. */
  parse() {
    for (const w of this.queue.splice(0)) { this.screen.push(w.data); if (w.cb) w.cb(); }
  }
  loadAddon(a) { if (a instanceof FakeWebgl) a.term = this; }
  open(host) { this.host = host; }
  onScroll(fn) { this._scroll = fn; }
  onWriteParsed(fn) { this._parsed = fn; }
  scrollToBottom() { this.buffer.active.viewportY = this.buffer.active.baseY; if (this._scroll) this._scroll(this.buffer.active.viewportY); }
  onData(fn) { this._data = fn; }
  onBinary(fn) { this._bin = fn; }
  write(d, cb) { this.queue.push({ data: d, cb }); }
  reset() { this.screen.length = 0; }
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
/** Every WebGL renderer made, with the terminal it was loaded into and
 *  whether it has been given up since. */
const webgls = [];
class FakeWebgl {
  constructor() { this.disposed = false; this.term = null; webgls.push(this); }
  onContextLoss(fn) { this._lost = fn; }
  dispose() { this.disposed = true; }
}

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

// ---------------------------------------------------------------- indexedDB

/** A single-database stand-in for IndexedDB: just enough of it for e2e.js's
 *  getIdentity -- open, one object store, get/put by key -- to run in node.
 *  Real IndexedDB fires every callback asynchronously, which this keeps (via
 *  setImmediate); a case awaiting FlockdeckE2E's own promises already waits
 *  through that. Reset on every boot() (idbReset), the same as sockets and
 *  terms: a real browser's storage survives a page's own reconnects, but not
 *  a case that boots the front end again wanting a device with no identity
 *  yet. */
let idbDatabases = new Map();
function idbReset() { idbDatabases = new Map(); }

class FakeIDBRequest {
  constructor() { this.onsuccess = null; this.onerror = null; this.onupgradeneeded = null; this.result = undefined; this.error = null; }
}

class FakeIDBObjectStore {
  constructor(map) { this.map = map; }
  get(key) {
    const req = new FakeIDBRequest();
    setImmediate(() => { req.result = this.map.get(key); if (req.onsuccess) req.onsuccess({ target: req }); });
    return req;
  }
  put(value, key) {
    const req = new FakeIDBRequest();
    this.map.set(key, value);
    setImmediate(() => { req.result = key; if (req.onsuccess) req.onsuccess({ target: req }); });
    return req;
  }
}

class FakeIDBTransaction {
  constructor(store) {
    this.store = store;
    this.oncomplete = null;
    this.onerror = null;
    setImmediate(() => { if (this.oncomplete) this.oncomplete({ target: this }); });
  }
  objectStore() { return this.store; }
}

class FakeIDBDatabase {
  constructor(stores) { this.stores = stores; }
  createObjectStore(name) {
    const map = new Map();
    this.stores.set(name, map);
    return new FakeIDBObjectStore(map);
  }
  transaction(name) {
    let map = this.stores.get(name);
    if (!map) { map = new Map(); this.stores.set(name, map); }
    return new FakeIDBTransaction(new FakeIDBObjectStore(map));
  }
}

const fakeIndexedDB = {
  open(name) {
    const req = new FakeIDBRequest();
    setImmediate(() => {
      let entry = idbDatabases.get(name);
      const isNew = !entry;
      if (!entry) { entry = { stores: new Map() }; idbDatabases.set(name, entry); }
      req.result = new FakeIDBDatabase(entry.stores);
      if (isNew && req.onupgradeneeded) req.onupgradeneeded({ target: req, oldVersion: 0 });
      setImmediate(() => { if (req.onsuccess) req.onsuccess({ target: req }); });
    });
    return req;
  },
};

// -------------------------------------------------------------------- boot

function unref(t) { t.unref(); return t; }

/** boot starts the front end. opts.pathname is where the page was served
 *  from, which is "/" locally and a machine's own prefix through the relay. */
function boot(opts) {
  opts = opts || {};
  sockets.length = 0; terms.length = 0; observers.length = 0; searchers.length = 0;
  notifications.length = 0; webgls.length = 0;
  FakeNotification.permission = "granted";
  FakeNotification.asked = 0;
  idbReset();
  const doc = new Doc();
  parseInto(fs.readFileSync(path.join(ASSETS, "index.html"), "utf8"), doc);

  const store = new Map();
  const e2eKeyPosts = [];
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
    // e2e.js's own identity and handshake: real WebCrypto (node's own, which
    // implements the same SubtleCrypto a browser does) against the fake
    // IndexedDB above, and the base64 globals a browser gives a page for
    // free that a vm context, its own fresh realm, does not.
    crypto, indexedDB: fakeIndexedDB, atob, btoa,
    // The page's own timers - a notice taking itself away twelve seconds on,
    // a reconnect - are not what a case waits for, and holding the process
    // open they made every case last as long as the longest of them after its
    // last assertion had run. A case that waits does so with h.sleep.
    setTimeout: (fn, ms, ...args) => unref(setTimeout(fn, ms, ...args)),
    setInterval: (fn, ms, ...args) => unref(setInterval(fn, ms, ...args)),
    clearTimeout, clearInterval, queueMicrotask,
    // Not a real clock: a callback is held until a case asks for the next
    // frame with h.raf(), so staggering WebGL contexts across several frames
    // is exact to test rather than a race against real timers.
    _raf: [],
    requestAnimationFrame: (fn) => { win._raf.push(fn); return win._raf.length; },
    cancelAnimationFrame: () => {},
    localStorage: {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
    },
    // Only /help.json is fetched by GET, on disk beside the assets;
    // FlockdeckE2E.ensureRegistered's POST to /.flockdeck-e2e-key is the one
    // other fetch app.js makes, answered here rather than off disk, and
    // recorded to e2eKeyPosts for a case to read (h.e2eKeyPosts()).
    fetch: (url, init) => {
      if (String(url) === "/.flockdeck-e2e-key" && init && init.method === "POST") {
        let body = null;
        try { body = JSON.parse(init.body); } catch { /* recorded as null */ }
        e2eKeyPosts.push(body);
        return Promise.resolve({ ok: true, status: 204 });
      }
      const name = String(url).replace(/^.*\//, "");
      try {
        const body = fs.readFileSync(path.join(ASSETS, name), "utf8");
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(JSON.parse(body)) });
      } catch (err) {
        return Promise.resolve({ ok: false, status: 404 });
      }
    },
    confirm: (q) => { win._confirmed = q; return win._confirm === undefined ? true : win._confirm; },
    prompt: () => win._prompt,
    close: () => { win._closed = true; },
    focus: () => {},
    // The clipboard a Copy button writes to; a case reads what it was given
    // back as _copied.
    navigator: { platform: opts.platform || "Win32",
      clipboard: { writeText: (t) => { win._copied = String(t); return Promise.resolve(); } } },
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
  // e2e.js first, as index.html's own script order has it: app.js reads
  // window.FlockdeckE2E at the top of its own module body (applyHello,
  // connectPTY), the same as it already expects window.Terminal and the
  // rest of the vendor scripts to be there first.
  const e2eSrc = fs.readFileSync(path.join(ASSETS, "e2e.js"), "utf8");
  vm.runInContext(e2eSrc, ctx, { filename: "e2e.js" });
  const src = fs.readFileSync(path.join(ASSETS, "app.js"), "utf8");
  vm.runInContext(src, ctx, { filename: "app.js" });

  const h = {
    doc, win, sockets, terms, observers, searchers, notifications, webgls,
    control: sockets.find((s) => s.url.includes("/ws/control")),
    $: (id) => doc.getElementById(id),
    /** e2eKeyPosts is every body FlockdeckE2E.ensureRegistered has POSTed to
     *  /.flockdeck-e2e-key, in order -- what a case reads to check a window
     *  registered (or did not) once it knew it was reached through the
     *  relay. */
    e2eKeyPosts() { return e2eKeyPosts.slice(); },
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
    /** waitFor polls cond every few ms until it is truthy, or rejects once
     *  timeoutMs has passed without it -- for a case that needs to wait on
     *  real async work (WebCrypto, the fake IndexedDB's own setImmediate
     *  hops) with no event of its own to await, where a fixed h.sleep is a
     *  race against however fast the machine running it happens to be. */
    async waitFor(cond, timeoutMs) {
      const deadline = Date.now() + (timeoutMs || 1000);
      for (;;) {
        if (cond()) return;
        if (Date.now() >= deadline) throw new Error("harness: waitFor timed out");
        await h.sleep(5);
      }
    },
    /** raf runs the callbacks queued by requestAnimationFrame so far, as one
     *  frame: a callback that asks for another frame is queued for the next
     *  one, not run again by this call. See webglQueue in app.js. */
    raf() { win._raf.splice(0).forEach((fn) => fn()); },
    /** settleWebgl runs frames until nothing is waiting on one, which is as
     *  far as webglQueue ever goes on its own once given the time. */
    settleWebgl() { while (win._raf.length) h.raf(); },
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
      return h.controls().flatMap((c) => c.sent).map((s) => JSON.parse(s)).filter((c) => c.cmd !== "presence");
    },
    /** presence is what the page has said of itself -- that it was just used
     *  -- up the control connection, which commands leaves out: it is said
     *  as the window sees real input, and is not what a case pressing keys
     *  is asking about. */
    presence() {
      return h.controls().flatMap((c) => c.sent).map((s) => JSON.parse(s)).filter((c) => c.cmd === "presence");
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
