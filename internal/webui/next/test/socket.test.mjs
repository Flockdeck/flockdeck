import { test } from "node:test";
import assert from "node:assert/strict";
import { createSocket, controlUrl, retryDelay } from "../assets/lib/socket.js";
import { FakeWebSocket, FakeClock } from "./fakes.mjs";

function make() {
  FakeWebSocket.reset();
  const clock = new FakeClock();
  const frames = [];
  const phases = [];
  const drops = [];
  const sock = createSocket({
    url: "ws-test", WebSocket: FakeWebSocket,
    setTimeout: clock.setTimeout, clearTimeout: clock.clearTimeout, now: () => clock.now,
    onFrame: (m) => frames.push(m),
    onPhase: (p, info) => phases.push([p, info.retryInMs]),
    onDrop: (raw) => drops.push(raw),
  });
  return { sock, clock, frames, phases, drops };
}

test("the delay doubles from one second to a cap of thirty", () => {
  assert.deepEqual([0, 1, 2, 3, 4, 5, 6, 10].map((n) => retryDelay(n)), [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000]);
});

test("a dropped socket is retried after 1 s, 2 s and 4 s, by the clock", () => {
  const { sock, clock, phases } = make();
  sock.start();
  assert.equal(FakeWebSocket.instances.length, 1);
  FakeWebSocket.last.open();
  FakeWebSocket.last.drop();
  assert.equal(sock.phase, "waiting");
  clock.advance(999);
  assert.equal(FakeWebSocket.instances.length, 1, "retried early");
  clock.advance(1);
  assert.equal(FakeWebSocket.instances.length, 2);
  FakeWebSocket.last.drop(); // never said hello, so the delay grows
  clock.advance(1999);
  assert.equal(FakeWebSocket.instances.length, 2);
  clock.advance(1);
  assert.equal(FakeWebSocket.instances.length, 3);
  FakeWebSocket.last.drop();
  clock.advance(3999);
  assert.equal(FakeWebSocket.instances.length, 3);
  clock.advance(1);
  assert.equal(FakeWebSocket.instances.length, 4);
  assert.deepEqual(phases.filter(([p]) => p === "waiting").map(([, ms]) => ms), [1000, 2000, 4000]);
});

// waits is the retry delays the socket announced, in order.
const waits = (phases) => phases.filter(([p]) => p === "waiting").map(([, ms]) => ms);

test("opening alone does not start the backoff over: the third wait is 4 s", () => {
  const { sock, clock, phases } = make();
  sock.start();
  for (const wait of [1000, 2000]) {
    FakeWebSocket.last.open(); // opened, even for a long time, but no hello
    clock.advance(60000);
    FakeWebSocket.last.drop();
    clock.advance(wait);
  }
  FakeWebSocket.last.open();
  clock.advance(60000);
  FakeWebSocket.last.drop();
  assert.deepEqual(waits(phases), [1000, 2000, 4000]);
});

test("a hello that is followed by a drop at once does not start the backoff over", () => {
  const { sock, clock, phases } = make();
  sock.start();
  for (const wait of [1000, 2000]) {
    FakeWebSocket.last.open();
    FakeWebSocket.last.frame({ type: "hello" });
    clock.advance(4999); // not yet a connection that has lasted
    FakeWebSocket.last.drop();
    clock.advance(wait);
  }
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame({ type: "hello" });
  FakeWebSocket.last.drop();
  assert.deepEqual(waits(phases), [1000, 2000, 4000]);
});

test("a connection that said hello and then lasted 5 s starts the backoff over", () => {
  const { sock, clock, phases } = make();
  sock.start();
  for (const wait of [1000, 2000]) {
    FakeWebSocket.last.open();
    FakeWebSocket.last.drop();
    clock.advance(wait);
  }
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame({ type: "hello" });
  clock.advance(5000);
  FakeWebSocket.last.drop();
  assert.deepEqual(waits(phases), [1000, 2000, 1000]);
  clock.advance(999);
  assert.equal(FakeWebSocket.instances.length, 3);
  clock.advance(1);
  assert.equal(FakeWebSocket.instances.length, 4);
});

// The old socket's close arrives after stop() and start() have made a new one.
// It must not close or retry the new one: one socket is open, and no retry is
// waiting.
test("a late close of an old socket does nothing to the new one", () => {
  const { sock, clock } = make();
  sock.start();
  const old = FakeWebSocket.last;
  old.open();
  sock.stop();
  sock.start();
  const fresh = FakeWebSocket.last;
  assert.notEqual(fresh, old);
  fresh.open();
  old.onclose({}); // the late event
  assert.equal(sock.phase, "open");
  assert.equal(clock.pending, 0, "a retry was scheduled for a socket that is gone");
  clock.advance(60000);
  assert.equal(FakeWebSocket.instances.length, 2);
  assert.equal(sock.send({ cmd: "x" }), true);
  assert.equal(fresh.sent.length, 1);
});

test("a malformed frame is dropped and the socket stays open", () => {
  const { sock, frames, drops } = make();
  sock.start();
  const ws = FakeWebSocket.last;
  ws.open();
  for (const bad of ["{not json", "null", "[1,2]", '"text"', "7", '{"no":"type"}', '{"type":5}']) ws.raw(bad);
  ws.onmessage({ data: new Uint8Array([1, 2, 3]) });
  assert.equal(drops.length, 8);
  assert.deepEqual(frames, []);
  assert.equal(ws.closed, false);
  assert.equal(sock.phase, "open");
  ws.frame({ type: "state", n: 1 });
  assert.deepEqual(frames, [{ type: "state", n: 1 }]);
});

test("send writes JSON only while the socket is open", () => {
  const { sock } = make();
  assert.equal(sock.send({ cmd: "x" }), false);
  sock.start();
  assert.equal(sock.send({ cmd: "x" }), false, "sent while connecting");
  FakeWebSocket.last.open();
  assert.equal(sock.send({ cmd: "x", n: 1 }), true);
  assert.deepEqual(FakeWebSocket.last.sent, [{ cmd: "x", n: 1 }]);
});

test("stop closes the socket, cancels the retry, and an old socket's events do nothing", () => {
  const { sock, clock, frames } = make();
  sock.start();
  const first = FakeWebSocket.last;
  first.open();
  first.drop();
  assert.equal(clock.pending, 1);
  sock.stop();
  assert.equal(clock.pending, 0);
  assert.equal(sock.phase, "idle");
  clock.advance(60000);
  assert.equal(FakeWebSocket.instances.length, 1);
  // Started again, a late frame from the first socket is ignored.
  sock.start();
  first.raw(JSON.stringify({ type: "state" }));
  assert.deepEqual(frames, []);
});

test("a socket that errors is closed and retried", () => {
  const { sock, clock } = make();
  sock.start();
  FakeWebSocket.last.onerror({});
  assert.equal(sock.phase, "waiting");
  clock.advance(1000);
  assert.equal(FakeWebSocket.instances.length, 2);
});

test("a constructor that throws is retried too", () => {
  FakeWebSocket.reset();
  const clock = new FakeClock();
  let n = 0;
  class Throws { constructor() { n++; throw new Error("bad url"); } }
  const sock = createSocket({ url: "x", WebSocket: Throws, setTimeout: clock.setTimeout, clearTimeout: clock.clearTimeout, onFrame() {} });
  sock.start();
  assert.equal(n, 1);
  clock.advance(1000);
  assert.equal(n, 2);
});

test("controlUrl follows the page's directory and scheme", () => {
  const loc = (href) => { const u = new URL(href); return { href: u.href, pathname: u.pathname, protocol: u.protocol }; };
  assert.equal(controlUrl(loc("http://127.0.0.1:5000/")), "ws://127.0.0.1:5000/ws/control");
  assert.equal(controlUrl(loc("http://127.0.0.1:5000/index.html?x=1")), "ws://127.0.0.1:5000/ws/control");
  assert.equal(controlUrl(loc("https://relay.test/d/host1/")), "wss://relay.test/d/host1/ws/control");
});
