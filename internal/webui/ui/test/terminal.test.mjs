import { test } from "node:test";
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import {
  createTerminal, TerminalChrome, isTerminalReply, stripPasteControls, terminalKey, safeLink, STRUCTURED_STUB, NO_KEY_REASON,
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

const settle = (ms = 200) => new Promise((r) => setTimeout(r, ms));

test("the box is reported when the socket opens and when it changes, once for each size, and closing sends nothing", async () => {
  const h = build();
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  ws.text(JSON.stringify({ size: { cols: 120, rows: 40 } }));
  assert.deepEqual([h.term().cols, h.term().rows], [120, 40], "the terminal did not take the pty's size");
  await settle();
  assert.deepEqual(resizesSent(ws), [{ resize: { cols: 100, rows: 30 } }], "the box was not reported on opening");
  // The drawer opens, closes, settles: a burst of measurements is one report, of the last size.
  FakeFit.box = { cols: 60, rows: 20 };
  for (const o of FakeObserver.instances) o.fire();
  FakeFit.box = { cols: 140, rows: 50 };
  for (const o of FakeObserver.instances) o.fire();
  await settle();
  assert.deepEqual(resizesSent(ws).slice(1), [{ resize: { cols: 140, rows: 50 } }]);
  // The same size again says nothing.
  for (const o of FakeObserver.instances) o.fire();
  await settle();
  assert.equal(resizesSent(ws).length, 2);
  // The terminal follows the pty, not the box, until the server resizes the pty.
  assert.deepEqual([h.term().cols, h.term().rows], [120, 40]);
  ws.text(JSON.stringify({ size: { cols: 140, rows: 50 } }));
  assert.deepEqual([h.term().cols, h.term().rows], [140, 50]);
  // The person maximises: fitToBox sends at once and is not repeated.
  FakeFit.box = { cols: 200, rows: 55 };
  assert.equal(h.ctl.fitToBox(), true);
  await settle();
  assert.deepEqual(resizesSent(ws).slice(2), [{ resize: { cols: 200, rows: 55 } }]);
  h.ctl.dispose();
  assert.equal(resizesSent(ws).length, 3, "closing sent a resize");
  assert.equal(h.term().disposed, true);
  assert.equal(FakeObserver.instances[0].disconnected, true);
});

test("a box laid out while hidden is reported when it is shown", async () => {
  const h = build();
  FakeFit.box = null;
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  await settle();
  assert.deepEqual(resizesSent(ws), [], "an unmeasured box was reported");
  FakeFit.box = { cols: 100, rows: 30 };
  for (const o of FakeObserver.instances) o.fire();
  await settle();
  assert.deepEqual(resizesSent(ws), [{ resize: { cols: 100, rows: 30 } }]);
  FakeFit.box = { cols: 10, rows: 3 };
  for (const o of FakeObserver.instances) o.fire();
  await settle();
  assert.equal(resizesSent(ws).length, 1, "a box too small to be a terminal was reported");
});

test("a window reached through the relay, and a viewer, report nothing; a new socket reports again", async () => {
  FakeFit.box = { cols: 80, rows: 24 };
  for (const over of [{ remote: true }, { role: "viewer", watchPanes: true }]) {
    const h = build({ snap: { hello: hello(over), state: paneState({ p1: {} }) } });
    h.ctl.start();
    PtySocket.last.open();
    await settle();
    assert.deepEqual(resizesSent(PtySocket.last), [], JSON.stringify(over));
    h.ctl.dispose();
  }
  const h = build();
  h.ctl.start();
  PtySocket.last.open();
  await settle();
  assert.equal(resizesSent(PtySocket.last).length, 1);
  PtySocket.last.close ? PtySocket.last.close() : null;
  h.ctl.dispose();
});

test("dispose cancels a report not yet sent", async () => {
  FakeFit.box = { cols: 80, rows: 24 };
  const h = build();
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  h.ctl.dispose();
  await settle();
  assert.deepEqual(resizesSent(ws), []);
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

test("libraries that are not in the page are loaded, with a line saying so, and then the terminal opens", async () => {
  let release;
  let calls = 0;
  const h = build({
    Terminal: null, FitAddon: null,
    loadLibs: () => { calls++; return new Promise((r) => { release = r; }); },
  });
  h.ctl.start();
  assert.equal(h.ctl.state.phase, "loading terminal");
  assert.equal(PtySocket.instances.length, 0, "a socket opened before the libraries were here");
  assert.match(renderToString(html`<${TerminalChrome} state=${h.ctl.state} hostRef=${{ current: null }} />`), /Loading terminal/);
  h.ctl.start();
  assert.equal(calls, 1, "a second start loaded again");
  // They arrive: the globals the page has now are read.
  globalThis.Terminal = FakeTerm;
  globalThis.FitAddon = { FitAddon: FakeFit };
  try {
    release();
    await waitFor(() => h.ctl.state.phase !== "loading terminal", "the terminal to open");
    assert.equal(PtySocket.instances.length, 1);
    assert.equal(FakeTerm.instances.length, 1);
  } finally {
    delete globalThis.Terminal;
    delete globalThis.FitAddon;
  }
  h.ctl.dispose();
});

test("a failed load says so with Try again, tries again, and never opens a socket without the libraries", async () => {
  let fail = true;
  let calls = 0;
  const h = build({
    Terminal: null, FitAddon: null,
    loadLibs: () => { calls++; return fail ? Promise.reject(new Error("net")) : Promise.resolve(); },
  });
  const quiet = console.error;
  console.error = () => {};
  try {
    h.ctl.start();
    await waitFor(() => h.ctl.state.phase === "load-failed", "the failure");
    assert.match(h.ctl.state.reason, /could not be loaded/);
    assert.equal(PtySocket.instances.length, 0);
    const out = renderToString(html`<${TerminalChrome} state=${h.ctl.state} hostRef=${{ current: null }} onTryAgain=${() => {}} />`);
    assert.match(out, /could not be loaded/);
    assert.match(out, /Try again/);
    // Still failing: still no socket.
    h.ctl.tryAgain();
    await waitFor(() => calls === 2 && h.ctl.state.phase === "load-failed", "the second failure");
    assert.equal(PtySocket.instances.length, 0);
    // The network is back.
    fail = false;
    globalThis.Terminal = FakeTerm;
    globalThis.FitAddon = { FitAddon: FakeFit };
    h.ctl.tryAgain();
    await waitFor(() => h.ctl.state.phase !== "load-failed" && h.ctl.state.phase !== "loading terminal", "the terminal to open");
    assert.equal(calls, 3);
    assert.equal(PtySocket.instances.length, 1);
  } finally {
    console.error = quiet;
    delete globalThis.Terminal;
    delete globalThis.FitAddon;
  }
  h.ctl.dispose();
});

test("libraries that load but define nothing are a failed load", async () => {
  const h = build({ Terminal: null, FitAddon: null, loadLibs: () => Promise.resolve() });
  h.ctl.start();
  await waitFor(() => h.ctl.state.phase === "load-failed", "the failure");
  assert.match(h.ctl.state.reason, /did not load/);
  assert.equal(PtySocket.instances.length, 0);
});

test("a terminal disposed while its libraries load opens nothing when they arrive", async () => {
  let release;
  const h = build({ Terminal: null, FitAddon: null, loadLibs: () => new Promise((r) => { release = r; }) });
  h.ctl.start();
  h.ctl.dispose();
  globalThis.Terminal = FakeTerm;
  globalThis.FitAddon = { FitAddon: FakeFit };
  try {
    release();
    await new Promise((r) => setTimeout(r, 20));
    assert.equal(PtySocket.instances.length, 0);
    assert.equal(FakeTerm.instances.length, 0);
    assert.equal(h.store.subscribers, 0);
  } finally {
    delete globalThis.Terminal;
    delete globalThis.FitAddon;
  }
});

test("a terminal that opens while another's libraries are half loaded waits for e2e.js too", async () => {
  // xterm and the addon are in the page, e2e.js is still arriving: a second
  // terminal with a keyed hello must not decide the end-to-end layer is missing.
  const E2E = loadE2E();
  const host = await hostSide(E2E);
  let release;
  const had = globalThis.FlockdeckE2E;
  delete globalThis.FlockdeckE2E;
  globalThis.Terminal = FakeTerm;
  globalThis.FitAddon = { FitAddon: FakeFit };
  const h = build({
    snap: { hello: hello({ remote: true, e2ePublicKey: host.pub }), state: paneState({ p1: {} }) },
    Terminal: null, FitAddon: null, e2e: null,
    loadLibs: () => new Promise((r) => { release = r; }),
  });
  try {
    h.ctl.start();
    assert.equal(h.ctl.state.phase, "loading terminal", "started without waiting for e2e.js");
    assert.equal(PtySocket.instances.length, 0);
    globalThis.FlockdeckE2E = E2E;
    release();
    await waitFor(() => PtySocket.instances.length === 1, "the socket");
    assert.notEqual(h.ctl.state.phase, "blocked");
  } finally {
    delete globalThis.Terminal;
    delete globalThis.FitAddon;
    delete globalThis.FlockdeckE2E;
    if (had) globalThis.FlockdeckE2E = had;
  }
  h.ctl.dispose();
});

test("a sealed terminal whose end-to-end layer does not come with the libraries opens no socket", async () => {
  // Terminal and the addon arrive, e2e.js does not: the hello carries a key, so
  // the terminal is blocked, not plain.
  const h = build({
    snap: { hello: hello({ remote: true, e2ePublicKey: "AAAA" }), state: paneState({ p1: {} }) },
    Terminal: null, FitAddon: null, e2e: null,
    loadLibs: () => Promise.resolve(),
  });
  globalThis.Terminal = FakeTerm;
  globalThis.FitAddon = { FitAddon: FakeFit };
  const had = globalThis.FlockdeckE2E;
  delete globalThis.FlockdeckE2E;
  try {
    h.ctl.start();
    await waitFor(() => h.ctl.state.phase === "blocked", "blocked");
    assert.match(h.ctl.state.reason, /end-to-end layer is not loaded/);
    assert.equal(PtySocket.instances.length, 0);
  } finally {
    delete globalThis.Terminal;
    delete globalThis.FitAddon;
    if (had) globalThis.FlockdeckE2E = had;
  }
  h.ctl.dispose();
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

test("a refused socket is dialled again when a later hello brings another key, across a gap with no hello", async () => {
  const E2E = loadE2E();
  const host = await hostSide(E2E);
  const h = build({ snap: { hello: hello({ remote: true }), state: paneState({ p1: {} }) }, e2e: E2E });
  h.ctl.start();
  const first = PtySocket.last;
  first.open();
  first.serverClose(1008, "wrong key");
  assert.equal(h.ctl.state.phase, "refused");
  // A hello that repeats the same (no) key is no reason to try again.
  h.store.set({ hello: hello({ remote: true, prefs: {} }) });
  assert.equal(PtySocket.instances.length, 1, "redialled under the key that was refused");
  // The control socket drops; nothing is dialled while there is no hello.
  h.store.set({ hello: null });
  assert.equal(PtySocket.instances.length, 1);
  assert.equal(h.ctl.state.phase, "refused");
  // The next hello carries the desktop's new key.
  h.store.set({ hello: hello({ remote: true, e2ePublicKey: host.pub }) });
  assert.equal(PtySocket.instances.length, 2, "not dialled again with the new key");
  const second = PtySocket.last;
  second.open();
  await waitFor(() => second.sent.length > 0, "the handshake hello");
  assert.equal(typeof second.sent[0], "object", "the new socket was not sealed");
  h.ctl.dispose();
});

// A socket refused under the desktop's key K, ready for the hello that follows.
async function refusedUnderKey() {
  const E2E = loadE2E();
  const host = await hostSide(E2E);
  const h = build({ snap: { hello: hello({ remote: true, e2ePublicKey: host.pub }), state: paneState({ p1: {} }) }, e2e: E2E });
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  ws.serverClose(1008, "refused");
  assert.equal(h.ctl.state.phase, "refused");
  assert.equal(PtySocket.instances.length, 1);
  return { h, host, E2E };
}

test("a hello with no key after a refusal under a key does not make a plain socket, and says why", async () => {
  const { h } = await refusedUnderKey();
  h.store.set({ hello: hello({ remote: true }) });
  assert.equal(PtySocket.instances.length, 1, "a plain terminal was opened for a desktop that had a key");
  assert.equal(h.ctl.state.phase, "refused");
  assert.equal(h.ctl.state.reason, NO_KEY_REASON);
  h.ctl.dispose();
});

test("a refusal under a key is not dialled again by the same key, with or without a hello between", async () => {
  const { h, host } = await refusedUnderKey();
  h.store.set({ hello: hello({ remote: true, e2ePublicKey: host.pub, prefs: {} }) });
  assert.equal(PtySocket.instances.length, 1, "the same key redialled");
  h.store.set({ hello: null });
  h.store.set({ hello: hello({ remote: true, e2ePublicKey: host.pub }) });
  assert.equal(PtySocket.instances.length, 1, "the same key redialled after a gap");
  // And a null hello between does not make the key look new.
  h.store.set({ hello: hello({ remote: true }) });
  h.store.set({ hello: hello({ remote: true, e2ePublicKey: host.pub }) });
  assert.equal(PtySocket.instances.length, 1, "the same key redialled after a hello with none");
  h.ctl.dispose();
});

test("every class the terminal's chrome draws for its host and pane has a rule in app.css", () => {
  const css = readFileSync(new URL("../assets/app.css", import.meta.url), "utf8");
  const out = renderToString(html`<${TerminalChrome} state=${{ phase: "open", reason: "", ptySize: null, sizeNote: null }} hostRef=${{ current: null }} />`);
  const classes = [...out.matchAll(/class="([^"]+)"/g)].flatMap((m) => m[1].split(/\s+/));
  // The rule that lays each out, by its selector at the start of a line and what
  // its body says, so a rule for another selector that mentions the class does not count.
  const rules = [
    ["terminal-pane", /^\.terminal-pane \{[^}]*position: relative/m],
    ["terminal-host", /^\.terminal-pane \.terminal-host \{[^}]*position: absolute[^}]*inset: 0/m],
  ];
  for (const [c, rule] of rules) {
    assert.ok(classes.includes(c), `the chrome no longer draws ${c}`);
    assert.ok(rule.test(css), `app.css has no rule that lays out .${c}`);
  }
});

test("after a keyless hello the cover offers no Try again, and tryAgain dials nothing", async () => {
  const { h } = await refusedUnderKey();
  h.store.set({ hello: hello({ remote: true }) });
  assert.equal(h.ctl.state.reason, NO_KEY_REASON);
  const out = renderToString(html`<${TerminalChrome} state=${h.ctl.state} hostRef=${{ current: null }} />`);
  assert.ok(out.includes(NO_KEY_REASON));
  assert.ok(!out.includes("Try again"), "the cover offers a button that would dial in plain");
  h.ctl.tryAgain();
  assert.equal(PtySocket.instances.length, 1, "tryAgain made a plain socket");
  h.ctl.dispose();
});

test("a refusal under no key keeps the server's own reason through a keyless hello", () => {
  const h = build({ snap: { hello: hello({ remote: true }), state: paneState({ p1: {} }) } });
  h.ctl.start();
  const ws = PtySocket.last;
  ws.open();
  ws.serverClose(1008, "not allowed here");
  assert.equal(h.ctl.state.reason, "not allowed here");
  h.store.set({ hello: hello({ remote: true, prefs: {} }) });
  assert.equal(h.ctl.state.reason, "not allowed here");
  h.ctl.tryAgain();
  assert.equal(PtySocket.instances.length, 2, "Try again on an ordinary refusal stopped working");
  h.ctl.dispose();
});

test("when the key comes back the refusal's own reason is shown again, without a redial", async () => {
  const { h, host } = await refusedUnderKey();
  const own = h.ctl.state.reason;
  h.store.set({ hello: hello({ remote: true }) });
  assert.equal(h.ctl.state.reason, NO_KEY_REASON);
  h.store.set({ hello: hello({ remote: true, e2ePublicKey: host.pub }) });
  assert.equal(h.ctl.state.reason, own);
  assert.equal(PtySocket.instances.length, 1);
  h.ctl.dispose();
});

// ------------------------------------------------------------ setOptions

test("setOptions changes only what differs and measures the box again after a change", () => {
  const h = build({ termOptions: { fontSize: 15 } });
  h.ctl.start();
  let measured = 0;
  const real = FakeFit.prototype.proposeDimensions;
  FakeFit.prototype.proposeDimensions = function () { measured++; return real.call(this); };
  try {
    h.ctl.setOptions({ fontSize: 15, scrollback: 10000 });
    const same = measured;
    h.ctl.setOptions({ fontSize: 18, scrollback: 10000 });
    assert.equal(h.term().options.fontSize, 18);
    assert.equal(measured, same + 1, "a changed font measures the box once");
    h.ctl.setOptions({ fontSize: 18 });
    assert.equal(measured, same + 1, "no change, no measuring");
    h.ctl.setOptions(null);
    h.ctl.setOptions({ cursorStyle: "bar", cursorBlink: false });
    assert.equal(h.term().options.cursorStyle, "bar");
    assert.equal(h.term().options.cursorBlink, false);
    assert.equal(PtySocket.instances.length === 0 || resizesSent(PtySocket.last).length === 0, true, "it never sends a resize");
  } finally {
    FakeFit.prototype.proposeDimensions = real;
  }
});

test("options set while the libraries load are laid over the ones it was made with", async () => {
  let release;
  const h = build({ Terminal: null, FitAddon: null, termOptions: { fontSize: 15, scrollback: 123 }, loadLibs: () => new Promise((r) => { release = r; }) });
  h.ctl.start();
  h.ctl.setOptions({ fontSize: 19 });
  h.ctl.setOptions({ cursorStyle: "underline" });
  globalThis.Terminal = FakeTerm;
  globalThis.FitAddon = { FitAddon: FakeFit };
  try {
    release();
    await waitFor(() => FakeTerm.instances.length === 1, "the terminal");
    const o = h.term().options;
    assert.equal(o.fontSize, 19, "the later setting wins over the one it was made with");
    assert.equal(o.scrollback, 123, "one nobody changed stays");
    assert.equal(o.cursorStyle, "underline", "both calls were kept");
  } finally {
    delete globalThis.Terminal;
    delete globalThis.FitAddon;
    h.ctl.dispose();
  }
});
