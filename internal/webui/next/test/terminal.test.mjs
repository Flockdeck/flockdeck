import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import {
  createTerminal, TerminalChrome, isTerminalReply, stripPasteControls, terminalKey, safeLink, STRUCTURED_STUB,
} from "../assets/lib/terminal.js";
import { FakeClock } from "./fakes.mjs";
import {
  PtySocket, FakeTerm, FakeFit, FakeObserver, FakeHost, fakeStore, paneState, loadE2E, hostSide, waitFor,
} from "./termfakes.mjs";

const loc = { href: "http://127.0.0.1:5000/", pathname: "/", protocol: "http:" };
const hello = (over = {}) => ({ type: "hello", ...over });

// build makes a terminal on fakes, with a store that already holds a hello
// and a state, unless told otherwise.
function build({ snap, ...over } = {}) {
  PtySocket.reset();
  FakeTerm.reset();
  FakeObserver.instances.length = 0;
  FakeFit.box = { cols: 100, rows: 30 };
  const store = fakeStore(snap || { hello: hello(), state: paneState({ p1: { status: "idle" } }) });
  const host = new FakeHost();
  const changes = [];
  const notices = [];
  const ctl = createTerminal({
    host, paneId: "p1", store, location: loc,
    Terminal: FakeTerm, FitAddon: FakeFit, ResizeObserver: FakeObserver, WebSocket: PtySocket,
    isFocused: () => false,
    onChange: (s) => changes.push(s),
    onNotice: (t, e) => notices.push([t, e]),
    ...over,
  });
  return { ctl, store, host, changes, notices, term: () => FakeTerm.last };
}

const resizesSent = (ws) => ws.plainJson.filter((m) => m.resize);

test("a pane with a terminal opens the socket for it once the hello and the state are in", () => {
  const h = build({ snap: { hello: null, state: null } });
  h.ctl.start();
  assert.equal(PtySocket.instances.length, 0);
  assert.equal(h.ctl.state.phase, "waiting for the page");
  h.store.set({ hello: hello() });
  assert.equal(PtySocket.instances.length, 0, "opened with no state to say whether the pane is structured");
  h.store.set({ state: paneState({ p1: { status: "idle" } }) });
  assert.equal(PtySocket.instances.length, 1);
  assert.match(PtySocket.last.url, /\/ws\/pty\?id=p1&size=1&from=-1$/);
  h.ctl.dispose();
  assert.equal(h.store.subscribers, 0);
});

test("a pane that is not in the state is not opened", () => {
  const h = build({ snap: { hello: hello(), state: paneState({ other: {} }) } });
  h.ctl.start();
  assert.equal(PtySocket.instances.length, 0);
});

test("a structured pane shows the stub and opens nothing", () => {
  const h = build({ snap: { hello: hello(), state: paneState({ p1: { structured: true } }) } });
  h.ctl.start();
  assert.equal(PtySocket.instances.length, 0);
  assert.equal(h.ctl.state.phase, "structured");
  assert.ok(h.term().text().includes(STRUCTURED_STUB));
  h.term().type("anything");
  assert.equal(PtySocket.instances.length, 0);
});

test("a viewer that may not watch panes does not open the socket", () => {
  const h = build({ snap: { hello: hello({ role: "viewer", watchPanes: false }), state: paneState({ p1: {} }) } });
  h.ctl.start();
  assert.equal(PtySocket.instances.length, 0);
  assert.equal(h.ctl.state.phase, "blocked");
  assert.match(h.ctl.state.reason, /may not watch panes/);
});

test("a viewer that may watch panes opens it, and its keystrokes are not sent", () => {
  const h = build({ snap: { hello: hello({ role: "viewer", watchPanes: true }), state: paneState({ p1: {} }) } });
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  h.term().type("rm -rf\r");
  assert.deepEqual(ws.sent, []);
});

test("keystrokes are sent as the old window sends them", () => {
  const h = build();
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  h.term().type("ls\r");
  assert.equal(new TextDecoder().decode(ws.sent[0]), "ls\r");
  // A binary report (xterm's onBinary) is the string's low bytes.
  for (const fn of h.term().binaryHandlers) fn("\x80\xff");
  assert.deepEqual([...ws.sent[1]], [0x80, 0xff]);
});

// ------------------------------------------------------------ the size rule

test("opening, a box that changes, and closing never send a resize; fitToBox sends one", () => {
  const h = build();
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  ws.text(JSON.stringify({ size: { cols: 120, rows: 40 } }));
  assert.deepEqual([h.term().cols, h.term().rows], [120, 40], "the terminal did not take the pty's size");
  // The drawer opens and closes: the box is measured again and again.
  FakeFit.box = { cols: 60, rows: 20 };
  for (const o of FakeObserver.instances) o.fire();
  FakeFit.box = { cols: 140, rows: 50 };
  for (const o of FakeObserver.instances) o.fire();
  assert.deepEqual(resizesSent(ws), [], "a resize went out on a change of the box");
  assert.deepEqual([h.term().cols, h.term().rows], [120, 40], "the terminal followed the box and not the pty");
  // The person maximises.
  FakeFit.box = { cols: 200, rows: 55 };
  assert.equal(h.ctl.fitToBox(), true);
  assert.deepEqual(resizesSent(ws), [{ resize: { cols: 200, rows: 55 } }]);
  h.ctl.dispose();
  assert.equal(resizesSent(ws).length, 1, "closing sent a resize");
  assert.equal(h.term().disposed, true);
  assert.equal(FakeObserver.instances[0].disconnected, true);
});

test("a box smaller than the pty says so, and the note goes when the box fits", () => {
  const h = build();
  h.ctl.start();
  PtySocket.last.open();
  PtySocket.last.text(JSON.stringify({ size: { cols: 120, rows: 40 } }));
  FakeFit.box = { cols: 80, rows: 24 };
  for (const o of FakeObserver.instances) o.fire();
  assert.deepEqual(h.ctl.state.sizeNote, { cols: 120, rows: 40 });
  FakeFit.box = { cols: 120, rows: 40 };
  for (const o of FakeObserver.instances) o.fire();
  assert.equal(h.ctl.state.sizeNote, null);
  // A box too small to be a terminal offers nothing.
  FakeFit.box = { cols: 10, rows: 3 };
  for (const o of FakeObserver.instances) o.fire();
  assert.equal(h.ctl.state.sizeNote, null);
});

test("taking over the size sends one resize and a focus notice", () => {
  const h = build();
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  ws.text(JSON.stringify({ size: { cols: 120, rows: 40 } }));
  FakeFit.box = { cols: 80, rows: 24 };
  assert.equal(h.ctl.takeOver(), true);
  assert.deepEqual(ws.plainJson, [{ resize: { cols: 80, rows: 24 } }, { focus: true }]);
});

test("a size outside what the server allows is ignored, and one above the cap is cut", () => {
  const h = build();
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  ws.text(JSON.stringify({ size: { cols: 5, rows: 2 } }));
  assert.equal(h.ctl.state.ptySize, null);
  ws.text(JSON.stringify({ size: { cols: 9000, rows: 9000 } }));
  assert.deepEqual(h.ctl.state.ptySize, { cols: 500, rows: 200 });
});

// ------------------------------------------------------------ the replay

test("the terminal's own answers are kept from the program while history is drawn", () => {
  const h = build();
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  ws.text(JSON.stringify({ epoch: 1, offset: 0, end: 10, resumed: false }));
  assert.ok(h.term().text().startsWith("\x1bc"), "a fresh run did not reset the screen");
  ws.bin(new TextEncoder().encode("12345"));
  h.term().type("\x1b[1;1R"); // a cursor report, in answer to history
  h.term().type("a"); // a person typing is not held
  assert.deepEqual(ws.sent.map((f) => new TextDecoder().decode(f)), ["a"]);
  ws.bin(new TextEncoder().encode("67890")); // the last of the history
  h.term().type("\x1b[1;1R");
  assert.deepEqual(ws.sent.map((f) => new TextDecoder().decode(f)), ["a", "\x1b[1;1R"]);
});

test("a resumed run does not reset the screen", () => {
  const h = build();
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  ws.text(JSON.stringify({ epoch: 1, offset: 5, end: 5, resumed: true }));
  assert.ok(!h.term().text().includes("\x1bc"));
});

test("isTerminalReply tells an answer from a key", () => {
  for (const s of ["\x1b]11;rgb:0000/0000/0000\x07", "\x1b[12;40R", "\x1b[?62;c", "\x1b[8;24;80t", "\x1b[?2004;1$y", "\x1b[I", "\x1b[O"]) {
    assert.equal(isTerminalReply(s), true, JSON.stringify(s));
  }
  for (const s of ["a", "\x1b", "\x1b[A", "\x1b[1;2P", "hello", "\r", "\x1b[200~"]) {
    assert.equal(isTerminalReply(s), false, JSON.stringify(s));
  }
});

// ------------------------------------------------------------ paste and keys

test("a paste with a control character is pasted without it, and the event stops", () => {
  const h = build();
  h.ctl.start();
  let stopped = 0;
  const ev = (text) => ({
    clipboardData: { getData: () => text },
    preventDefault() { stopped++; },
    stopPropagation() { stopped++; },
  });
  h.host.emit("paste", ev("echo hi\x1b[201~; rm -rf /\r"));
  assert.deepEqual(h.term().pasted, ["echo hi[201~; rm -rf /\r"]);
  assert.equal(stopped, 2);
  h.host.emit("paste", ev("plain text\n"));
  assert.equal(h.term().pasted.length, 1, "a clean paste was taken over");
  h.host.emit("paste", ev("\x1b\x00"));
  assert.equal(h.term().pasted.length, 1, "a paste of nothing but controls sent something");
  assert.equal(stripPasteControls("a\tb\nc\rd\x7fe\x9bf"), "a\tb\nc\rdef");
});

test("Shift+Enter is a pasted newline; Ctrl+C copies a selection and is ^C otherwise", async () => {
  const written = [];
  const term = new FakeTerm({});
  const key = (o) => ({ type: "keydown", key: "", ctrlKey: false, shiftKey: false, altKey: false, metaKey: false, preventDefault() {}, ...o });
  assert.equal(terminalKey(term, key({ key: "Enter", shiftKey: true })), false);
  assert.deepEqual(term.pasted, ["\n"]);
  assert.equal(terminalKey(term, key({ key: "c", ctrlKey: true })), true, "^C with no selection must reach the program");
  term.selection = "copy me";
  const clipboard = { writeText: async (t) => { written.push(t); } };
  assert.equal(terminalKey(term, key({ key: "c", ctrlKey: true }), { clipboard }), false);
  assert.deepEqual(written, ["copy me"]);
  assert.equal(term.selection, "");
  assert.equal(terminalKey(term, key({ key: "v", ctrlKey: true })), false, "Ctrl+V is left to the browser");
  // A Mac's Ctrl keys stay the program's.
  term.selection = "x";
  assert.equal(terminalKey(term, key({ key: "c", ctrlKey: true }), { mac: true }), true);
  // Nothing to reach the clipboard with says so.
  const notices = [];
  term.selection = "y";
  terminalKey(term, key({ key: "c", ctrlKey: true, shiftKey: true }), { notify: (t, e) => notices.push([t, e]) });
  assert.equal(notices.length, 1);
  assert.equal(notices[0][1], true);
});

test("an OSC 8 link goes to openExternal, for http and https only", () => {
  const opened = [];
  const h = build({ openExternal: (u) => opened.push(u) });
  h.ctl.start();
  const go = (u) => h.term().options.linkHandler.activate({}, u);
  go("https://example.test/x");
  go("http://example.test/y");
  for (const bad of ["javascript:alert(1)", "JaVaScRiPt:alert(1)", "file:///etc/passwd", "data:text/html,x", "vbscript:x", "//example.test", "not a url", "", "ftp://example.test"]) go(bad);
  assert.deepEqual(opened, ["https://example.test/x", "http://example.test/y"]);
  assert.equal(safeLink("https://a.test"), true);
  assert.equal(safeLink("javascript:1"), false);
});

// ------------------------------------------------------------ listeners and dispose

test("disposing takes every listener off the host, and a paste after it does nothing", () => {
  const h = build();
  const before = h.host.listenerCount;
  h.ctl.start();
  assert.ok(h.host.listenerCount > before);
  const paste = h.host.listeners.paste[0];
  h.ctl.dispose();
  assert.equal(h.host.listenerCount, before, "listeners are left on the host");
  // A handler that is still held somewhere must not throw with no terminal.
  assert.doesNotThrow(() => paste({
    clipboardData: { getData: () => "ab" }, preventDefault() {}, stopPropagation() {},
  }));
});

test("controllers made one after another on one host leave no listeners behind", () => {
  const host = new FakeHost();
  for (let i = 0; i < 3; i++) {
    const h = build({ host });
    h.ctl.start();
    h.ctl.dispose();
  }
  assert.equal(host.listenerCount, 0);
});

// ------------------------------------------------------------ the library and the role

test("a page whose terminal library did not load shows a reason and throws nothing", () => {
  const had = globalThis.Terminal;
  globalThis.Terminal = undefined;
  try {
    const h = build({ Terminal: null });
    assert.doesNotThrow(() => h.ctl.start());
    assert.equal(h.ctl.state.phase, "blocked");
    assert.equal(h.ctl.state.reason, "The terminal library is not loaded.");
    assert.equal(PtySocket.instances.length, 0);
    // Started again once the script has run.
    const h2 = build({ FitAddon: null });
    assert.doesNotThrow(() => h2.ctl.start());
    assert.equal(h2.ctl.state.phase, "blocked");
  } finally {
    globalThis.Terminal = had;
  }
});

test("a viewer later allowed to watch panes gets its terminal", () => {
  const h = build({ snap: { hello: hello({ role: "viewer", watchPanes: false }), state: paneState({ p1: {} }) } });
  h.ctl.start();
  assert.equal(PtySocket.instances.length, 0);
  assert.equal(h.ctl.state.phase, "blocked");
  h.store.set({ hello: hello({ role: "viewer", watchPanes: true }) });
  assert.equal(PtySocket.instances.length, 1);
  assert.notEqual(h.ctl.state.phase, "blocked");
  // And back: the desk takes it away again.
  h.store.set({ hello: hello({ role: "viewer", watchPanes: false }) });
  assert.equal(PtySocket.last.closed, true);
  assert.equal(h.ctl.state.phase, "blocked");
});

test("a device the desk limits to viewer is let go quietly, and dialled again when it may watch", () => {
  const h = build({ snap: { hello: hello(), state: paneState({ p1: {} }) } });
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  // The desk limits the device: the control socket's hello says so, and the
  // pty socket is closed with 1008.
  h.store.set({ hello: hello({ role: "viewer", watchPanes: true }) });
  ws.serverClose(1008, "limited");
  assert.equal(h.ctl.state.phase, "idle", "a limited device was shown refused with Try again");
  assert.equal(PtySocket.instances.length, 1);
  h.store.set({ hello: hello({ role: "viewer", watchPanes: true, prefs: {} }) });
  assert.equal(PtySocket.instances.length, 2, "not dialled again once the role was told");
});

test("the key is read when a socket opens, and kept while the control socket has no hello", async () => {
  const E2E = loadE2E();
  const host = await hostSide(E2E);
  const clock = new FakeClock();
  const h = build({
    snap: { hello: hello({ remote: true }), state: paneState({ p1: {} }) }, e2e: E2E,
    ptyOptions: { setTimeout: clock.setTimeout, clearTimeout: clock.clearTimeout, now: () => clock.now },
  });
  h.ctl.start();
  const first = PtySocket.last;
  first.open();
  h.term().type("a");
  assert.equal(new TextDecoder().decode(first.sent[0]), "a", "a socket with no key on file is plain");
  // The desktop's key arrives with a later hello: the next socket is sealed.
  h.store.set({ hello: hello({ remote: true, e2ePublicKey: host.pub }) });
  first.serverClose(1006);
  clock.advance(250);
  const second = PtySocket.last;
  assert.notEqual(second, first);
  second.open();
  await waitFor(() => second.sent.length > 0, "the handshake hello");
  assert.equal(second.sent.length, 1);
  assert.equal(typeof second.sent[0], "object", "the sealed socket's first message was text");
  // The control socket drops and has no hello for a while: the key is kept.
  h.store.set({ hello: null });
  second.serverClose(1006);
  clock.advance(500);
  const third = PtySocket.last;
  third.open();
  await waitFor(() => third.sent.length > 0, "a handshake hello without a hello in the store");
  assert.equal(typeof third.sent[0], "object");
  h.ctl.dispose();
});

// ------------------------------------------------------------ through the relay

test("a remote hello with the desktop's key seals the terminal socket", async () => {
  const E2E = loadE2E();
  const host = await hostSide(E2E);
  const h = build({
    snap: { hello: hello({ remote: true, e2ePublicKey: host.pub }), state: paneState({ p1: {} }) },
    e2e: E2E,
  });
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  await waitFor(() => ws.sent.length > 0, "the handshake hello");
  h.term().type("typed before the handshake ended\r");
  assert.equal(ws.sent.length, 1, "a keystroke went out before the handshake finished");
  const { session, response } = await host.answer(ws.sent[0]);
  ws.bin(response);
  await waitFor(() => ws.sent.length >= 2, "the held keystroke, sealed");
  const opened = await session.open(ws.sent[1]);
  assert.equal(new TextDecoder().decode(opened.subarray(1)), "typed before the handshake ended\r");
  h.ctl.dispose();
});

test("a remote hello with a key and no end-to-end layer loaded opens nothing", () => {
  const had = globalThis.FlockdeckE2E;
  globalThis.FlockdeckE2E = undefined;
  try {
    const h = build({ snap: { hello: hello({ remote: true, e2ePublicKey: "k" }), state: paneState({ p1: {} }) } });
    h.ctl.start();
    assert.equal(PtySocket.instances.length, 0);
    assert.equal(h.ctl.state.phase, "blocked");
  } finally {
    globalThis.FlockdeckE2E = had;
  }
});

test("a remote hello with no key opens a plain socket, as the old window does", () => {
  const h = build({ snap: { hello: hello({ remote: true }), state: paneState({ p1: {} }) } });
  h.ctl.start();
  PtySocket.last.open();
  h.term().type("a");
  assert.equal(new TextDecoder().decode(PtySocket.last.sent[0]), "a");
});

// ------------------------------------------------------------ the chrome

test("the refusal's reason is drawn as text, with Try again", () => {
  const reason = '<img src=x onerror="alert(1)">';
  const out = renderToString(html`<${TerminalChrome} state=${{ phase: "refused", reason, ptySize: null, sizeNote: null }} hostRef=${{ current: null }} />`);
  assert.ok(!out.includes("<img"), out);
  assert.match(out, /&lt;img src=x onerror=/);
  assert.match(out, /Try again/);
  assert.match(out, /role="alert"/);
});

test("a blocked terminal has no Try again; a size note names both sizes; an open one has neither", () => {
  const blocked = renderToString(html`<${TerminalChrome} state=${{ phase: "blocked", reason: "no", sizeNote: null }} hostRef=${{ current: null }} />`);
  assert.doesNotMatch(blocked, /Try again/);
  const noted = renderToString(html`<${TerminalChrome} state=${{ phase: "open", reason: "", sizeNote: { cols: 120, rows: 40 } }} hostRef=${{ current: null }} />`);
  assert.match(noted, /Viewing 120×40/);
  const plain = renderToString(html`<${TerminalChrome} state=${{ phase: "open", reason: "", sizeNote: null }} hostRef=${{ current: null }} />`);
  assert.doesNotMatch(plain, /role="alert"|size-note/);
});

test("disposing closes the socket and stops the retry", () => {
  const h = build();
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  h.ctl.dispose();
  assert.equal(ws.closed, true);
  assert.equal(h.store.subscribers, 0);
});
