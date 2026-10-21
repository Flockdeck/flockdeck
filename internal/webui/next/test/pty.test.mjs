import { test } from "node:test";
import assert from "node:assert/strict";
import { createPty, ptyUrl, retryDelay, INPUT_FRAME, EARLY_MAX_FRAMES, PTY_REFUSED } from "../assets/lib/pty.js";
import { FakeClock } from "./fakes.mjs";
import { PtySocket, loadE2E, hostSide, waitFor, sealFor, sleep } from "./termfakes.mjs";

const loc = { href: "http://127.0.0.1:5000/", pathname: "/", protocol: "http:" };

function make(over = {}) {
  PtySocket.reset();
  const clock = new FakeClock();
  const log = { sizes: [], runs: [], bytes: [], states: [] };
  const pty = createPty({
    paneId: "p1",
    url: (held) => ptyUrl(loc, "p1", held),
    WebSocket: PtySocket,
    setTimeout: clock.setTimeout, clearTimeout: clock.clearTimeout, now: () => clock.now,
    onSize: (c, r) => log.sizes.push([c, r]),
    onRun: (r) => log.runs.push(r),
    onBytes: (b, end) => log.bytes.push([new TextDecoder().decode(b), end]),
    onState: (s) => log.states.push(s.phase),
    ...over,
  });
  return { pty, clock, log };
}

const header = (o = {}) => JSON.stringify({ epoch: 1, offset: 0, end: 0, resumed: false, ...o });

// ------------------------------------------------------------ the plain socket

test("the address names the pane and asks for the pty's size, with no token", () => {
  assert.equal(ptyUrl(loc, "p 1", null), "ws://127.0.0.1:5000/ws/pty?id=p+1&size=1&from=-1");
  const relay = { href: "https://relay.test/d/h1/", pathname: "/d/h1/", protocol: "https:" };
  assert.equal(ptyUrl(relay, "p1", { epoch: 3, offset: 99, fresh: false }), "wss://relay.test/d/h1/ws/pty?id=p1&size=1&epoch=3&from=99");
  // A stream that has delivered nothing yet is not resumed.
  assert.match(ptyUrl(loc, "p1", { epoch: 3, offset: 0, fresh: true }), /from=-1$/);
  assert.doesNotMatch(ptyUrl(loc, "p1", null), /token|[?&]t=/);
});

test("output is drawn, and a reconnect asks only for what the terminal missed", () => {
  const { pty, clock, log } = make();
  pty.start();
  const first = PtySocket.last;
  assert.match(first.url, /from=-1$/);
  first.open();
  first.text(header({ offset: 0, end: 5 }));
  first.bin(new TextEncoder().encode("hello"));
  assert.deepEqual(log.runs, [{ reset: true, replayEnd: 5 }]);
  assert.deepEqual(log.bytes, [["hello", true]], "the end of the replay was not marked");
  first.serverClose(1006);
  clock.advance(250);
  const second = PtySocket.last;
  assert.notEqual(second, first);
  assert.match(second.url, /epoch=1&from=5$/, "the reconnect did not resume from the offset");
  second.open();
  second.text(header({ epoch: 1, offset: 5, resumed: true }));
  assert.deepEqual(log.runs[1], { reset: false, replayEnd: 0 });
});

test("a size header is the pty's size and is not the opening of a run", () => {
  const { pty, log } = make();
  pty.start();
  PtySocket.last.open();
  PtySocket.last.text(JSON.stringify({ size: { cols: 120, rows: 40 } }));
  assert.deepEqual(log.sizes, [[120, 40]]);
  assert.deepEqual(log.runs, []);
  assert.equal(pty.stream, null);
});

test("keystrokes go out as bytes, a large paste in frames of at most 1 MiB", () => {
  const { pty } = make();
  pty.start();
  const ws = PtySocket.last;
  assert.equal(pty.sendInput("x"), false, "sent before the socket opened");
  ws.open();
  assert.equal(pty.sendInput("echo hi\r"), true);
  assert.equal(new TextDecoder().decode(ws.sent[0]), "echo hi\r");
  ws.sent.length = 0;
  const big = new Uint8Array(INPUT_FRAME * 2 + 10).fill(65);
  pty.sendBytes(big);
  assert.deepEqual(ws.sent.map((f) => f.length), [INPUT_FRAME, INPUT_FRAME, 10]);
  assert.equal(INPUT_FRAME, 1048576);
});

test("a viewer's keystrokes are not sent, but a resize and focus notice are JSON text", () => {
  const { pty } = make({ viewOnly: () => true });
  pty.start();
  const ws = PtySocket.last;
  ws.open();
  assert.equal(pty.sendInput("rm -rf\r"), false);
  assert.deepEqual(ws.sent, []);
  pty.sendResize(100, 30);
  pty.sendFocus();
  assert.deepEqual(ws.plainJson, [{ resize: { cols: 100, rows: 30 } }, { focus: true }]);
});

test("the retry waits 250 ms, 500 ms and so on to 3 s, and starts over after a socket that held", () => {
  assert.deepEqual([0, 1, 2, 3, 4, 8].map(retryDelay), [250, 500, 1000, 2000, 3000, 3000]);
  const { pty, clock } = make();
  pty.start();
  for (const wait of [250, 500]) {
    PtySocket.last.serverClose(1006); // never opened
    clock.advance(wait - 1);
    clock.advance(1);
  }
  assert.equal(PtySocket.instances.length, 3);
  PtySocket.last.open();
  clock.advance(1000);
  PtySocket.last.serverClose(1006);
  clock.advance(249);
  assert.equal(PtySocket.instances.length, 3);
  clock.advance(1);
  assert.equal(PtySocket.instances.length, 4, "a socket that held a second did not start the wait over");
});

test("a refusal (1008) is shown and is not retried; Try again dials at once", () => {
  const { pty, clock, log } = make();
  pty.start();
  const ws = PtySocket.last;
  ws.open();
  ws.serverClose(PTY_REFUSED, "end-to-end handshake failed");
  assert.equal(pty.phase, "refused");
  clock.advance(60000);
  assert.equal(PtySocket.instances.length, 1);
  pty.retryNow();
  assert.equal(PtySocket.instances.length, 2);
  assert.ok(log.states.includes("refused"));
});

test("a refusal of a viewer is let go quietly", () => {
  const { pty, clock } = make({ viewOnly: () => true });
  pty.start();
  PtySocket.last.open();
  PtySocket.last.serverClose(PTY_REFUSED, "limited");
  assert.equal(pty.phase, "idle");
  clock.advance(60000);
  assert.equal(PtySocket.instances.length, 1);
});

test("an old socket's late events do nothing, and stop leaves nothing running", () => {
  const { pty, clock, log } = make();
  pty.start();
  const old = PtySocket.last;
  old.open();
  pty.retryNow();
  const fresh = PtySocket.last;
  assert.notEqual(old, fresh);
  old.text(header());
  old.bin(new TextEncoder().encode("late"));
  assert.deepEqual(log.bytes, []);
  fresh.open();
  pty.stop();
  assert.equal(fresh.closed, true);
  clock.advance(60000);
  assert.equal(PtySocket.instances.length, 2);
  assert.equal(pty.phase, "idle");
});

// ------------------------------------------------------------ the sealed socket

// sealed starts a pty with a key on file and a stand-in desktop.
async function sealed(over = {}) {
  const E2E = loadE2E();
  const host = await hostSide(E2E);
  const made = make({ hostKey: host.pub, e2e: E2E, ...over });
  made.pty.start();
  const ws = PtySocket.last;
  ws.open();
  await waitFor(() => ws.sent.length > 0, "the handshake hello");
  return { ...made, E2E, host, ws };
}

// finish runs the desktop's side and returns its session.
async function finish(h) {
  const { session, response } = await h.host.answer(h.ws.sent[0]);
  h.ws.bin(response);
  return session;
}

test("with a key on file the handshake hello is the first thing sent, and nothing is sent plain", async () => {
  const h = await sealed();
  assert.equal(h.ws.sent.length, 1, "the hello was not the first message");
  assert.equal(typeof h.ws.sent[0].byteLength, "number");
  // Typed while the handshake is under way: held, and sent only sealed.
  h.pty.sendResize(100, 30);
  h.pty.sendInput("echo hi\r");
  assert.equal(h.ws.sent.length, 1);
  const session = await finish(h);
  await waitFor(() => h.ws.sent.length >= 3, "the held frames, sealed");
  const frames = h.ws.sent.slice(1);
  for (const f of frames) {
    assert.equal(typeof f, "object", "a post-handshake frame was text");
    assert.ok(!new TextDecoder().decode(f).includes("echo"), "a keystroke crossed in the clear");
  }
  const opened = [];
  for (const f of frames) opened.push(await session.open(f));
  assert.equal(opened[0][0], 1);
  assert.deepEqual(JSON.parse(new TextDecoder().decode(opened[0].subarray(1))), { resize: { cols: 100, rows: 30 } });
  assert.equal(opened[1][0], 0);
  assert.equal(new TextDecoder().decode(opened[1].subarray(1)), "echo hi\r");
});

test("sealed output and a sealed header are drawn", async () => {
  const h = await sealed();
  const session = await finish(h);
  h.ws.bin(await sealFor(session, 1, header({ end: 3 })));
  h.ws.bin(await sealFor(session, 0, "abc"));
  await waitFor(() => h.log.bytes.length === 1, "the output");
  assert.deepEqual(h.log.bytes, [["abc", true]]);
  assert.deepEqual(h.log.runs, [{ reset: true, replayEnd: 3 }]);
});

test("a plain frame on a sealed socket closes it, and the retry starts a new handshake", async () => {
  const h = await sealed();
  const session = await finish(h);
  const quiet = console.error;
  console.error = () => {};
  try {
    h.ws.text(header()); // an old-format frame
  } finally {
    console.error = quiet;
  }
  assert.equal(h.ws.closed, true);
  assert.deepEqual(h.log.runs, [], "an old-format frame was drawn");
  h.clock.advance(250);
  const next = PtySocket.last;
  assert.notEqual(next, h.ws);
  next.open();
  await waitFor(() => next.sent.length > 0);
  assert.equal(next.sent.length, 1, "something besides the hello went out first");
  assert.ok(session);
});

test("a handshake response that does not check out closes the socket and never falls back", async () => {
  const h = await sealed();
  h.pty.sendInput("secret\r");
  const quiet = console.error;
  console.error = () => {};
  try {
    h.ws.bin(new Uint8Array(65).fill(7)); // not a key
    await waitFor(() => h.ws.closed, "the socket to close");
  } finally {
    console.error = quiet;
  }
  assert.equal(h.ws.sent.length, 1, "something was sent after a failed handshake");
  assert.deepEqual(h.log.bytes, []);
});

test("a frame before the handshake has started is refused", async () => {
  const E2E = loadE2E();
  const host = await hostSide(E2E);
  const made = make({ hostKey: host.pub, e2e: E2E });
  made.pty.start();
  const ws = PtySocket.last;
  ws.open();
  const quiet = console.error;
  console.error = () => {};
  try {
    ws.bin(new Uint8Array(40)); // before beginHandshake has sent its hello
  } finally {
    console.error = quiet;
  }
  assert.equal(ws.closed, true);
});

test("a text frame while the handshake is pending is refused", async () => {
  const h = await sealed();
  const quiet = console.error;
  console.error = () => {};
  try {
    h.ws.text("{}");
  } finally {
    console.error = quiet;
  }
  assert.equal(h.ws.closed, true);
});

test("sealed frames right behind the response are kept and opened in order", async () => {
  const h = await sealed();
  const { session, response } = await h.host.answer(h.ws.sent[0]);
  // The desktop writes its header and output right behind the response.
  const a = await sealFor(session, 1, header({ end: 2 }));
  const b = await sealFor(session, 0, "hi");
  h.ws.bin(response);
  h.ws.bin(a);
  h.ws.bin(b);
  await waitFor(() => h.log.bytes.length === 1, "the held output");
  assert.deepEqual(h.log.bytes, [["hi", true]]);
});

test("more frames held than the cap closes the socket and installs no session", async () => {
  const h = await sealed();
  const { response } = await h.host.answer(h.ws.sent[0]);
  const quiet = console.error;
  console.error = () => {};
  const reasons = [];
  console.error = (m) => reasons.push(String(m));
  try {
    h.ws.bin(response);
    for (let i = 0; i <= EARLY_MAX_FRAMES + 1; i++) h.ws.bin(new Uint8Array(20));
    await waitFor(() => h.ws.closed, "the socket to close");
  } finally {
    console.error = quiet;
  }
  assert.ok(reasons.some((m) => m.includes("too many frames")), "closed for another reason: " + reasons.join(" | "));
  assert.deepEqual(h.log.bytes, []);
  await sleep(20);
  h.pty.sendInput("x");
  assert.equal(h.ws.sent.length, 1, "something was sent on a socket with no session");
});

test("a frame delivered twice is a replay and closes the socket", async () => {
  const h = await sealed();
  const session = await finish(h);
  const frame = await sealFor(session, 0, "once");
  const quiet = console.error;
  console.error = () => {};
  try {
    h.ws.bin(frame);
    h.ws.bin(frame);
    await waitFor(() => h.ws.closed, "the replayed frame to close the socket");
  } finally {
    console.error = quiet;
  }
  assert.deepEqual(h.log.bytes, [["once", false]], "the replayed frame was drawn too");
});

test("a handshake the desktop never answers closes the socket after 10 s", async () => {
  const h = await sealed();
  const quiet = console.error;
  console.error = () => {};
  try {
    h.clock.advance(9999);
    assert.equal(h.ws.closed, false);
    h.clock.advance(1);
  } finally {
    console.error = quiet;
  }
  assert.equal(h.ws.closed, true);
});

test("a window with no identity of its own does not fall back to plain", async () => {
  const E2E = { getIdentity: async () => null, startTerminalHandshake: async () => { throw new Error("no"); } };
  const made = make({ hostKey: "k", e2e: E2E });
  made.pty.start();
  const ws = PtySocket.last;
  const quiet = console.error;
  console.error = () => {};
  try {
    ws.open();
    await waitFor(() => ws.closed, "the socket to close");
  } finally {
    console.error = quiet;
  }
  assert.deepEqual(ws.sent, []);
});

test("more held bytes than 8 MiB closes the socket, though there are few frames", async () => {
  const h = await sealed();
  const { response } = await h.host.answer(h.ws.sent[0]);
  const quiet = console.error;
  const reasons = [];
  console.error = (m) => reasons.push(String(m));
  try {
    h.ws.bin(response);
    for (let i = 0; i < 9; i++) h.ws.bin(new Uint8Array(1024 * 1024)); // 9 MiB in 9 frames
    await waitFor(() => h.ws.closed, "the socket to close");
  } finally {
    console.error = quiet;
  }
  assert.ok(reasons.some((m) => m.includes("too many frames")), reasons.join(" | "));
  assert.deepEqual(h.log.bytes, []);
});

test("a text frame on an established session closes the socket", async () => {
  const h = await sealed();
  await finish(h);
  const quiet = console.error;
  const reasons = [];
  console.error = (m) => reasons.push(String(m));
  try {
    h.ws.text(header());
  } finally {
    console.error = quiet;
  }
  assert.equal(h.ws.closed, true);
  assert.ok(reasons.some((m) => m.includes("non-binary frame")), reasons.join(" | "));
  assert.deepEqual(h.log.runs, []);
});

// ------------------------------------------------------- the outcome of typing

test("typing tells its callback once, with true, when the bytes are on the plain socket", () => {
  const { pty } = make();
  pty.start();
  PtySocket.last.open();
  const told = [];
  assert.equal(pty.sendInput("y\r", (ok) => told.push([ok, PtySocket.last.sent.length])), true);
  assert.deepEqual(told, [[true, 1]], "told before the bytes were written, or not once");
});

test("typing into a socket that is not open returns false and does not tell the callback", () => {
  const { pty } = make();
  pty.start();
  const told = [];
  assert.equal(pty.sendInput("y\r", (ok) => told.push(ok)), false);
  assert.deepEqual(told, []);
});

test("a paste in several frames tells its callback once, after the last", () => {
  const { pty } = make();
  pty.start();
  PtySocket.last.open();
  const told = [];
  assert.equal(pty.sendBytes(new Uint8Array(INPUT_FRAME * 2 + 1), (ok) => told.push([ok, PtySocket.last.sent.length])), true);
  assert.deepEqual(told, [[true, 3]]);
});

test("a sealed frame tells its callback after it is on the socket, not when it is queued", async () => {
  const h = await sealed();
  const told = [];
  h.pty.sendInput("y\r", (ok) => told.push([ok, h.ws.sent.length]));
  assert.deepEqual(told, [], "told while the handshake was still under way");
  await finish(h);
  await waitFor(() => told.length > 0, "the callback");
  assert.deepEqual(told, [[true, 2]]);
});

test("a sealed frame whose socket closed first tells its callback false", async () => {
  const h = await sealed();
  await finish(h);
  const told = [];
  h.pty.sendInput("y\r", (ok) => told.push(ok));
  h.ws.serverClose(1006);
  await waitFor(() => told.length > 0, "the callback");
  assert.deepEqual(told, [false]);
});

test("a frame held for a handshake that fails tells its callback false", async () => {
  const h = await sealed();
  const told = [];
  h.pty.sendInput("y\r", (ok) => told.push(ok));
  const quiet = console.error;
  console.error = () => {};
  try {
    h.ws.bin(new Uint8Array(65).fill(7));
    await waitFor(() => h.ws.closed, "the socket to close");
    h.pty.stop();
  } finally {
    console.error = quiet;
  }
  assert.deepEqual(told, [false], "the keys were reported as written");
});

test("a sealed frame queued on an open session whose socket closes before it is written tells false", async () => {
  const h = await sealed();
  await finish(h);
  await sleep(100);
  const told = [];
  h.pty.sendInput("y\r", (ok) => told.push([ok, h.ws.sent.length]));
  h.ws.serverClose(1006);
  await waitFor(() => told.length > 0, "the callback");
  assert.deepEqual(told, [[false, 1]], "only the hello was written");
});
