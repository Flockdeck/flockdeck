package webui

import (
	"regexp"
	"strings"
	"testing"
)

// The read-only recordings viewer (app.js, "Remote artifacts viewer"): a window
// reached through the relay reads the desk's recordings over /ws/artifacts, which
// is end-to-end encrypted or not made at all. These hold the window to the
// rendering rules of a read-only view: text only, nothing to save or
// open, nothing kept once the view is gone.

// artifactsDeskSetup plays the desk's end of the artifacts socket with the same
// handshake the real one runs (FlockdeckE2E.respondHostHandshake): connectDesk()
// finishes the handshake on the socket the window just opened, and returns next()
// (the window's next request, opened with the desk's session) and say(reply)
// (a sealed reply, tagged as a text message the way the desk's e2eConn tags it).
const artifactsDeskSetup = `
const E2E = h.win.FlockdeckE2E;
const hostPair = await h.win.crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
const hostPubB64 = E2E.encodePublicKey(new Uint8Array(await h.win.crypto.subtle.exportKey("raw", hostPair.publicKey)));
const helloWith = (extra) => h.recv(Object.assign({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true }, extra));
const artSockets = () => h.sockets.filter((s) => s.url.includes("/ws/artifacts"));
const connectDesk = async (opened) => {
  const ws = artSockets().pop();
  if (!opened) ws.onopen();
  await h.waitFor(() => ws.sent.length > 0);
  const identity = await E2E.getIdentity();
  const { session, response } = await E2E.respondHostHandshake(hostPair.privateKey, hostPair.publicKey, identity.publicKey, ws.sent[0]);
  ws.onmessage({ data: response.buffer });
  let taken = 1;
  const next = async () => {
    await h.waitFor(() => ws.sent.length > taken);
    const plain = await session.open(ws.sent[taken++]);
    assert.strictEqual(plain[0], 1, "a request was not tagged as text");
    return JSON.parse(new TextDecoder().decode(plain.subarray(1)));
  };
  const say = async (reply) => {
    const body = new TextEncoder().encode(JSON.stringify(reply));
    const tagged = new Uint8Array(body.length + 1);
    tagged[0] = 1;
    tagged.set(body, 1);
    const frame = await session.seal(tagged);
    ws.onmessage({ data: frame.buffer });
  };
  return { ws, next, say };
};
const offered = () => helloWith({ e2ePublicKey: hostPubB64, artifacts: { v: 1 } });
const body = () => h.$("overlay-body");
const onScreen = (id) => walk(body()).find((n) => n.id === id) || null;
const ITEM = { id: "AAAAAAAAAAAAAAAAAAAAAA", kind: "recordings", name: "20261001T101530Z-0123abcd.jsonl", project: "shop", agent: "claude", started: "2026-10-01T10:15:01.5Z", size: 2048, mtime: 1, viewable: true };
const HELLO = { op: "hello", v: 1, desk: "work-pc", device: "Pixel 8", kinds: ["recordings"], limits: { chunk: 262144, request: 65536, listItems: 500 } };
// Opens the viewer and brings it as far as a list on screen.
const showList = async (items, resume) => {
  if (!resume) {
    offered();
    h.recv(fixture());
    h.click(h.$("btn-recordings"));
  }
  const desk = await connectDesk(resume);
  assert.deepStrictEqual(await desk.next(), { op: "hello", v: 1 });
  await desk.say(HELLO);
  assert.deepStrictEqual(await desk.next(), { op: "list", kind: "recordings" });
  await desk.say({ op: "list", kind: "recordings", items: items || [ITEM], next: "" });
  await h.waitFor(() => body().textContent.includes("shop"));
  return desk;
};
// Every element under n, to look for anything that is not plain text holders.
function walk(n, out) { out = out || []; for (const c of n.childNodes) { if (c.nodeType === 1) { out.push(c); walk(c, out); } } return out; }
const ALLOWED_TAGS = new Set(["DIV", "SPAN", "PRE", "BUTTON", "P", "H2", "H3"]);
const assertOnlyText = (root, where) => {
  for (const n of walk(root)) {
    assert.ok(ALLOWED_TAGS.has(n.tagName), where + ": a " + n.tagName + " element was made");
    for (const attr of ["href", "src", "srcdoc", "download", "target", "action", "style", "onclick", "onerror"]) {
      if (attr === "onclick") continue; // set as a property by the page's own buttons
      assert.ok(!n.hasAttribute(attr), where + ": a " + n.tagName + " has " + attr);
    }
  }
};
`

func TestTheViewerIsOfferedOnlyWhenTheDeskSaysSo(t *testing.T) {
	runFrontEnd(t, artifactsDeskSetup+`
const btn = h.$("btn-recordings");
assert.ok(btn.hidden, "the viewer is offered before the desk has said anything");

// A window on the desk itself is never offered it, whatever the hello says.
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, artifacts: { v: 1 } });
assert.ok(btn.hidden, "a window on the desk is offered the relay viewer");

// A window reached through the relay, with nothing allowed for its device.
helloWith({ e2ePublicKey: hostPubB64 });
assert.ok(btn.hidden, "offered without the desk saying the device may use it");

offered();
assert.ok(!btn.hidden, "not offered although the desk allows the device");

// The desk can take it away, and the window follows without a reload.
h.recv({ type: "artifacts", available: false });
assert.ok(btn.hidden, "still offered after the desk took it away");
h.recv({ type: "artifacts", available: true, v: 1 });
assert.ok(!btn.hidden);
`)
}

func TestTheViewerEncryptsEverythingAndShowsTheBanner(t *testing.T) {
	runFrontEnd(t, artifactsDeskSetup+`
offered();
h.recv(fixture());
h.click(h.$("btn-recordings"));
assert.ok(!h.$("overlay").hidden, "the viewer did not open");
assert.strictEqual(artSockets().length, 1, "no artifacts socket was opened");
const ws = artSockets()[0];
assert.ok(ws.url.endsWith("/ws/artifacts"), "the socket is not the artifacts socket: " + ws.url);
ws.onopen();
await h.waitFor(() => ws.sent.length === 1);
// The one thing sent before the handshake is done is the handshake itself, raw
// bytes and not JSON.
assert.strictEqual(typeof ws.sent[0], "object", "the handshake hello was not binary");
// An uncompressed P-256 point: 0x04 and 64 bytes.
assert.ok(ws.sent[0].length === 65 && ws.sent[0][0] === 4, "the first message is not the handshake");

// Carry on from there.
const desk = await showList(null, true);
for (const frame of ws.sent) {
  const text = new TextDecoder("latin1").decode(new Uint8Array(frame));
  assert.ok(!text.includes("recordings") && !text.includes('"op"'), "a request crossed the socket in the clear");
}
assert.ok(body().textContent.includes("Read-only from work-pc - Pixel 8"), "the banner is missing or wrong: " + body().textContent);
assert.ok(body().textContent.includes("shop") && body().textContent.includes("claude"));
assertOnlyText(body(), "the list");
`)
}

func TestTheViewerDrawsHostileTextAsTextAndNothingElse(t *testing.T) {
	runFrontEnd(t, artifactsDeskSetup+`
const desk = await showList();
h.win.pwned = 0;
h.click(h.$("art-item-0"));
const req = await desk.next();
assert.deepStrictEqual(req, { op: "open", kind: "recordings", id: ITEM.id, cursor: 0, max: 50 });

const hostile = '<script>window.pwned = 1</script><img src=x onerror="window.pwned = 1"><a href="javascript:window.pwned=1">x</a><iframe srcdoc="<script>1</script>"></iframe>';
await desk.say({ op: "data", id: ITEM.id, kind: "recordings",
  header: { project: "shop", agent: "claude", started: "2026-10-01T10:15:01.5Z" },
  entries: [
    { seq: 2, time: "2026-10-01T10:15:02Z", type: "assistant_message", text: hostile },
    { seq: 3, time: "2026-10-01T10:15:03Z", type: "tool_use", tool: '<b onmouseover=1>Bash</b>', input: { command: hostile, nested: { a: [hostile] } } },
    { seq: 4, time: "2026-10-01T10:15:04Z", type: "tool_result", output: hostile, isError: true },
    { seq: 5, time: hostile, type: hostile, text: "x" },
  ],
  next: 400, done: true, skipped: 1 });
await h.waitFor(() => body().textContent.includes("#5"));

// The markup is on screen as the characters it is made of.
assert.ok(body().textContent.includes("<script>window.pwned = 1</script>"), "the text was not shown as written");
assert.ok(body().textContent.includes("<b onmouseover=1>Bash</b>"));
// And it made nothing: no elements beyond the viewer's own, no attributes that load or navigate.
assertOnlyText(body(), "an entry");
assert.strictEqual(h.win.pwned, 0, "something ran");
// Nothing on screen offers to save, open or copy it.
const labels = walk(body()).filter((n) => n.tagName === "BUTTON").map((n) => n.textContent.toLowerCase());
for (const l of labels) assert.ok(!/download|save|open in|new tab|copy|share|export/.test(l), "a control offers: " + l);
assert.ok(body().textContent.includes("1 line was not shown"));
`)
}

func TestTheViewerPagesWithTheHostsCursorAndSendsNoPath(t *testing.T) {
	runFrontEnd(t, artifactsDeskSetup+`
const desk = await showList();
h.click(h.$("art-item-0"));
await desk.next();
await desk.say({ op: "data", id: ITEM.id, kind: "recordings", header: {}, entries: [{ seq: 2, time: "2026-10-01T10:15:02Z", type: "user_message", text: "first page" }], next: 777, done: false });
await h.waitFor(() => onScreen("art-more"));
h.click(onScreen("art-more"));
const req = await desk.next();
assert.deepStrictEqual(req, { op: "open", kind: "recordings", id: ITEM.id, cursor: 777, max: 50 });
await desk.say({ op: "data", id: ITEM.id, kind: "recordings", header: {}, entries: [{ seq: 3, time: "2026-10-01T10:15:03Z", type: "assistant_message", text: "second page" }], next: 900, done: true });
await h.waitFor(() => body().textContent.includes("second page"));
assert.ok(body().textContent.includes("first page") && body().textContent.includes("second page"));
assert.ok(!onScreen("art-more"), "more is offered at the end");

// An error from the desk is its code and nothing else, shown as a plain notice.
h.click(onScreen("art-back"));
h.click(onScreen("art-item-0"));
await desk.next();
await desk.say({ op: "error", code: "unavailable" });
await h.waitFor(() => body().textContent.includes("not available"));
assertOnlyText(body(), "an error");
`)
}

func TestTheViewerClearsWhatItShowedWhenItCloses(t *testing.T) {
	runFrontEnd(t, artifactsDeskSetup+`
const desk = await showList();
h.click(h.$("art-item-0"));
await desk.next();
await desk.say({ op: "data", id: ITEM.id, kind: "recordings", header: {}, entries: [{ seq: 2, time: "t", type: "assistant_message", text: "SECRET-WORDS" }], next: 5, done: true });
await h.waitFor(() => body().textContent.includes("SECRET-WORDS"));

h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape did not close the viewer");
assert.ok(!body().textContent.includes("SECRET-WORDS") && !body().textContent.includes("shop"), "what was shown is still in the page: " + body().textContent);
assert.strictEqual(desk.ws.readyState, 3, "the socket is still open");

// Opening it again starts from nothing: a new socket, no old list.
h.click(h.$("btn-recordings"));
assert.ok(!body().textContent.includes("SECRET-WORDS"));
assert.strictEqual(artSockets().length, 2, "the second view did not make a fresh connection");
`)
}

func TestTheViewerClearsWhenTheSocketDrops(t *testing.T) {
	runFrontEnd(t, artifactsDeskSetup+`
const desk = await showList();
h.click(h.$("art-item-0"));
await desk.next();
await desk.say({ op: "data", id: ITEM.id, kind: "recordings", header: {}, entries: [{ seq: 2, time: "t", type: "assistant_message", text: "SECRET-WORDS" }], next: 5, done: true });
await h.waitFor(() => body().textContent.includes("SECRET-WORDS"));

desk.ws.readyState = 3;
desk.ws.onclose({ code: 4403, reason: "verify" });
assert.ok(!body().textContent.includes("SECRET-WORDS"), "what was shown survived the socket dropping");
assert.ok(!body().textContent.includes("shop"), "the list survived the socket dropping");
assert.ok(body().textContent.includes("verified"), "the person is not told what to do: " + body().textContent);
assert.ok(body().textContent.includes("Read-only from work-pc"), "the banner went with the content");
assert.ok(onScreen("art-again"), "no way to try again");
`)
}

func TestTheViewerClearsAfterTheTabIsHiddenForTwoMinutes(t *testing.T) {
	runFrontEnd(t, artifactsDeskSetup+`
const desk = await showList();
h.click(h.$("art-item-0"));
await desk.next();
await desk.say({ op: "data", id: ITEM.id, kind: "recordings", header: {}, entries: [{ seq: 2, time: "t", type: "assistant_message", text: "SECRET-WORDS" }], next: 5, done: true });
await h.waitFor(() => body().textContent.includes("SECRET-WORDS"));

// The page's timers are held so the test decides when two minutes have passed.
const timers = [];
const cleared = new Set();
h.win.setTimeout = (fn, ms) => { timers.push({ fn, ms, id: timers.length + 1 }); return timers.length; };
h.win.clearTimeout = (id) => { cleared.add(id); };
const visibility = (hidden) => { h.doc.hidden = hidden; h.doc.dispatchEvent(new h.win.Event("visibilitychange")); };

// Hidden and shown again in time: nothing is lost.
visibility(true);
// (The harness delivers a document event to its listeners in both phases, so a
// timer that was replaced at once is told apart from the one that stands.)
const live = () => timers.filter((t) => t.ms === 120000 && !cleared.has(t.id));
assert.strictEqual(live().length, 1, "hiding the tab did not start one two minute timer");
visibility(false);
assert.strictEqual(live().length, 0, "showing the tab again did not cancel it");
assert.ok(body().textContent.includes("SECRET-WORDS"));

// Hidden for the whole time.
visibility(true);
assert.strictEqual(live().length, 1);
live()[0].fn();
assert.ok(!body().textContent.includes("SECRET-WORDS"), "what was shown survived two minutes hidden");
assert.ok(!body().textContent.includes("shop"));
assert.ok(body().textContent.includes("hidden for 2 minutes"), "the person is not told why: " + body().textContent);
assert.strictEqual(desk.ws.readyState, 3, "the socket stayed open while hidden");
assert.ok(onScreen("art-again"));
`)
}

func TestTheViewerDoesNotConnectWithoutAKey(t *testing.T) {
	runFrontEnd(t, artifactsDeskSetup+`
// The desk has no key on file: nothing is sent, in the clear or otherwise.
helloWith({ artifacts: { v: 1 } });
h.recv(fixture());
h.click(h.$("btn-recordings"));
await h.sleep(20);
assert.strictEqual(artSockets().length, 0, "a plain socket was opened to the artifacts path");
assert.ok(body().textContent.includes("end-to-end"), "the person is not told why: " + body().textContent);
`)
}

func TestTheViewerEndsAHandshakeThatDoesNotCheckOut(t *testing.T) {
	runFrontEnd(t, artifactsDeskSetup+`
offered();
h.recv(fixture());
h.click(h.$("btn-recordings"));
const ws = artSockets()[0];
ws.onopen();
await h.waitFor(() => ws.sent.length > 0);
ws.onmessage({ data: new Uint8Array([1, 2, 3]).buffer });
await h.waitFor(() => ws.readyState === 3);
assert.strictEqual(ws.sent.length, 1, "something was sent after a handshake that failed");
assert.ok(body().textContent.includes("could not be set up"), body().textContent);

// A text frame where only sealed ones belong ends it as well.
h.click(onScreen("art-again"));
const ws2 = artSockets().pop();
ws2.onopen();
await h.waitFor(() => ws2.sent.length > 0);
ws2.onmessage({ data: "plain words" });
await h.waitFor(() => ws2.readyState === 3);
assert.strictEqual(ws2.sent.length, 1);
`)
}

// The rendering rules, held to the code itself: the viewer
// builds text and buttons and nothing else, from nothing but fields the desk sent.
func TestTheViewerCodeHasNoWayToRenderMarkupOrKeepContent(t *testing.T) {
	src := readAsset(t, "app.js")
	const begin = "/* ==== Remote artifacts viewer: begin ===="
	const end = "/* ==== Remote artifacts viewer: end ==== */"
	a, b := strings.Index(src, begin), strings.Index(src, end)
	if a < 0 || b < a {
		t.Fatal("the viewer's code is not between its markers")
	}
	code := src[a:b]
	// Comments are prose and may name what the code does not do.
	code = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(code, "")
	code = regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(code, "")
	code = regexp.MustCompile(`\s//\s.*$`).ReplaceAllString(code, "")

	for _, banned := range []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "insertAdjacentElement", "DOMParser", "createContextualFragment",
		"eval(", "new Function", "Function(", "setTimeout(\"", "setTimeout('", "document.write",
		"localStorage", "sessionStorage", "indexedDB", "caches", "cookie", "serviceWorker", "history.pushState",
		"createObjectURL", "Blob(", "FileReader", "showSaveFilePicker", "URL(",
		"window.open", "location.", "location =", ".href", ".src", "srcdoc", ".download", "download", "target=", "_blank",
		"createElement", "<a ", "<iframe", "<img", "<script", "<object", "<embed",
		"navigator.clipboard", "execCommand", "XMLHttpRequest", "fetch(", "sendBeacon", "postMessage",
		"import(", "importScripts", "Worker(",
	} {
		if strings.Contains(code, banned) {
			t.Errorf("the viewer's code contains %q", banned)
		}
	}
	// Every element is made by el(), which sets text with textContent.
	if !regexp.MustCompile(`const el = \(tag, cls, text\) => \{[^}]*n\.textContent = text;`).MatchString(src) {
		t.Error("el() no longer sets text with textContent")
	}
	if strings.Count(code, "el(") < 10 {
		t.Error("the viewer does not draw with el()")
	}
	// The only WebSocket is the artifacts one, the only path is the fixed one,
	// and the request is built from fixed fields and host-issued values.
	if n := strings.Count(code, "new WebSocket("); n != 1 || !strings.Contains(code, `"ws/artifacts"`) {
		t.Errorf("the viewer opens %d sockets, or not to ws/artifacts", n)
	}
	for _, field := range []string{"path:", "url:", "port:", "cwd:", "file:"} {
		if strings.Contains(code, field) {
			t.Errorf("a request could name %q", field)
		}
	}
	// The window's own CSP must not have been loosened for it.
	idx := readAsset(t, "index.html")
	if strings.Contains(idx, "unsafe-inline") || strings.Contains(idx, "unsafe-eval") {
		t.Error("index.html allows inline or eval script")
	}
}
