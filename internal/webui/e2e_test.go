package webui

import "testing"

// End-to-end encryption for a pane's terminal socket, once a window is
// reached through the relay and the hello says this desktop has a key of
// its own (help.go's helloMsg.E2EPublicKey): FlockdeckE2E (assets/e2e.js)
// registering this window's own identity with the relay, and connectPTY's
// handshake wrapping every terminal frame in a sealed one. The scheme
// itself is e2e.js's own, byte for byte the same one flockdeck-remote's own
// copy of it and internal/e2e (Go) implement; these hold app.js to its side
// of using it -- the same relationship webui_test.go's other pty tests hold
// connectPTY to for the plain terminal.

// remoteHello is a hello message for a window reached through the relay,
// shaped as h.hello() would give one but with remote and e2ePublicKey set,
// neither of which h.hello() itself offers a way to.
func remoteHello(e2ePublicKey string) string {
	extra := ""
	if e2ePublicKey != "" {
		extra = `, e2ePublicKey: "` + e2ePublicKey + `"`
	}
	return `h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true` + extra + ` });`
}

// TestARemoteWindowRegistersItsEndToEndKey covers a window reached through
// the relay: the hello alone -- whether or not it also names this desktop's
// own key -- is enough for the window to make (or load) its own identity
// and register its public half with the relay, so that a handshake has
// something to answer once this desktop's own key does arrive. Registering
// the same key again is a no-op, so a reconnect's own hello does not spend
// the relay's write-rate limit for nothing.
func TestARemoteWindowRegistersItsEndToEndKey(t *testing.T) {
	runFrontEnd(t, `
`+remoteHello("")+`
await h.waitFor(() => h.e2eKeyPosts().length > 0);

const posts = h.e2eKeyPosts();
assert.strictEqual(posts.length, 1, "the window did not register its own key");
const identity = await h.win.FlockdeckE2E.getIdentity();
assert.strictEqual(posts[0].publicKey, h.win.FlockdeckE2E.encodePublicKey(identity.publicKeyRaw),
  "the key registered was not this window's own identity");

`+remoteHello("")+`
await h.sleep(20);
assert.strictEqual(h.e2eKeyPosts().length, 1, "the same key was registered again on a second hello");
`)
}

// TestALocalWindowRegistersNoKey covers a window opened on the desktop
// itself: local, with nothing through the relay to register a key with and
// nothing of its own to encrypt against, it must never call
// FlockdeckE2E.ensureRegistered at all.
func TestALocalWindowRegistersNoKey(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
await h.sleep(20);
assert.strictEqual(h.e2eKeyPosts().length, 0, "a local window registered an end-to-end key");
`)
}

// e2eHostSetup is the JS this file's own handshake tests share: a P-256 host
// identity of the test's own making (standing in for the desktop's real
// one, kept by internal/remote/e2ekey.go), and the hello that gives its
// public half to the page the way help.go's sendHello does -- built from the
// JS variable it just made, so (unlike remoteHello) it cannot be a Go-side
// string literal.
const e2eHostSetup = `
const E2E = h.win.FlockdeckE2E;
const crypto = h.win.crypto;
const hostPair = await crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
const hostPubB64 = E2E.encodePublicKey(new Uint8Array(await crypto.subtle.exportKey("raw", hostPair.publicKey)));
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true, e2ePublicKey: hostPubB64 });
`

// TestAPtyHandshakeEndToEndEncryptsTheTerminal covers a pane's terminal
// socket once hostE2EPublicKey names a key: connectPTY sends the handshake
// hello before anything else, completes it against a real desktop identity
// played by this test (FlockdeckE2E.respondHostHandshake, made for exactly
// this), and every terminal message after -- in both directions -- is a
// sealed frame indistinguishable from noise to anything that has not
// completed the same handshake, and correct plaintext to the one that has.
func TestAPtyHandshakeEndToEndEncryptsTheTerminal(t *testing.T) {
	runFrontEnd(t, e2eHostSetup+`
h.recv(fixture());

const ws = h.sockets.find((s) => s.url.includes("/ws/pty?id=p1"));
assert.strictEqual(ws.sent.length, 0, "something was sent before the socket opened");
ws.onopen();
// beginHandshake awaits FlockdeckE2E.getIdentity() and
// startTerminalHandshake -- real WebCrypto work -- before sending the hello,
// so a fixed sleep here is a race against however fast the machine running
// it happens to be; h.waitFor exists for exactly this (see its own doc).
await h.waitFor(() => ws.sent.length > 0);

assert.strictEqual(ws.sent.length, 1, "the handshake hello was not this socket's first message");
const hello = ws.sent[0];

const identity = await E2E.getIdentity();
const { session: hostSession, response } = await E2E.respondHostHandshake(
  hostPair.privateKey, hostPair.publicKey, identity.publicKey, hello);
ws.onmessage({ data: response.buffer });
// finishHandshake runs unawaited from ws.onmessage and itself awaits
// handshake.finish (WebCrypto) before flushing what was queued -- the same
// race as above.
await h.waitFor(() => ws.sent.length > 1);

// The resize and focus notice connectPTY queued while the handshake was
// under way went out sealed, once it finished -- never plain.
assert.ok(ws.sent.length > 1, "nothing was sent once the handshake completed");
for (const frame of ws.sent.slice(1)) {
  assert.strictEqual(typeof frame.byteLength, "number", "a post-handshake frame was not binary: " + frame);
}

// Traffic the browser sends is readable, in order, by the desktop's own
// session -- and only by it, not as JSON or plain bytes. Every frame since
// the hello shares hostSession's one strictly-increasing counter, resize
// and focus included, so they are opened in order rather than picked out.
const sentBeforeKeystroke = ws.sent.length;
h.terms[0]._data("echo hi\r");
// Sealing the keystroke is WebCrypto work queued on the socket's send path
// too -- wait for it to land rather than a fixed sleep.
await h.waitFor(() => ws.sent.length > sentBeforeKeystroke);
const deviceFrames = ws.sent.slice(1);
for (const frame of deviceFrames) {
  assert.ok(!String.fromCharCode(...new Uint8Array(frame).slice(0, 20)).includes("echo"),
    "a keystroke crossed the socket in the clear");
}
let keystroke = null;
for (const frame of deviceFrames) {
  const opened = await hostSession.open(frame);
  if (opened[0] === 0 && new TextDecoder().decode(opened.subarray(1)) === "echo hi\r") keystroke = opened;
}
assert.ok(keystroke, "the desktop's session did not open a keystroke reading what was typed");

// Traffic the desktop sends -- a stream header, then output -- is sealed
// the same way, and connectPTY draws it exactly as it draws the plain
// terminal's own header and output.
const seal = async (tag, plain) => {
  const tagged = new Uint8Array(plain.length + 1);
  tagged[0] = tag;
  tagged.set(plain, 1);
  return hostSession.seal(tagged);
};
const header = await seal(1, new TextEncoder().encode(JSON.stringify({ epoch: 1, offset: 0, end: 0, resumed: false })));
ws.onmessage({ data: header.buffer });
const out = await seal(0, new TextEncoder().encode("hello from the desktop"));
ws.onmessage({ data: out.buffer });
// Each incoming frame is opened on p.recvQueue -- a promise chain around
// session.open, also WebCrypto -- before it reaches the terminal, so this
// waits for the draw rather than sleeping a fixed amount and hoping.
const drew = (t) => t.written.some((w) => new TextDecoder().decode(w) === "hello from the desktop");
await h.waitFor(() => h.terms.some(drew));

const term = h.terms.find(drew);
assert.ok(term, "the desktop's sealed output was not drawn");
`)
}

// TestAPtyHandshakeThatDoesNotCheckOutClosesRatherThanFallingBack covers a
// handshake response that does not decode as a valid ephemeral key --
// tampered with, or from a relay stripping the real one to force a
// downgrade. The socket is closed rather than ever read as a plain
// terminal: see connectPTY's own beginHandshake doc for why.
func TestAPtyHandshakeThatDoesNotCheckOutClosesRatherThanFallingBack(t *testing.T) {
	runFrontEnd(t, e2eHostSetup+`
h.recv(fixture());

const ws = h.sockets.find((s) => s.url.includes("/ws/pty?id=p1"));
ws.onopen();
// The hello is sent once WebCrypto has made the handshake's key, which is
// not a matter of 30ms on a slow machine.
await h.waitFor(() => ws.sent.length > 0);
assert.strictEqual(ws.sent.length, 1, "the handshake hello was not sent");

let closed = false;
const realClose = ws.close.bind(ws);
ws.close = () => { closed = true; realClose(); };
ws.onmessage({ data: new Uint8Array([1, 2, 3]).buffer });
await h.waitFor(() => closed);
assert.ok(closed, "a handshake response that did not check out was not closed");
`)
}

// e2eOpenedPty is the setup the next tests share: a pane's terminal socket
// that has sent its hello, plus the desktop's side of the session it is
// about to be given. seal builds a frame the way internal/server/pty.go's
// e2eConn does, from the one session, so frames must be delivered in the
// order they were sealed. The first response is built but not delivered.
func openedPty(before string) string {
	return e2eHostSetup + e2eGateSetup + before + e2eOpenedBody
}

// e2eGateSetup lets a test hold one handshake's key derivation open: newGate()
// makes the next handshake started wait in finish() until gate.open() (or
// gate.open(err), which makes it fail), so a test can deliver frames, or
// reconnect, at exactly the moment the bug lived in. opens counts the calls
// made to the gated session's open.
const e2eGateSetup = `
const realStart = E2E.startTerminalHandshake.bind(E2E);
const gates = [];
let opens = 0;
const newGate = () => {
  let release;
  const g = { promise: new Promise((r) => { release = r; }), error: null, open(err) { g.error = err || null; release(); } };
  gates.push(g);
  return g;
};
E2E.startTerminalHandshake = async (...args) => {
  const started = await realStart(...args);
  const g = gates.shift();
  if (!g) return started;
  const realFinish = started.handshake.finish.bind(started.handshake);
  started.handshake.finish = async (response) => {
    await g.promise;
    if (g.error) throw g.error;
    const session = await realFinish(response);
    const realOpen = session.open.bind(session);
    session.open = (frame) => { opens++; return realOpen(frame); };
    return session;
  };
  return started;
};
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
`

const e2eOpenedBody = `
h.recv(fixture());
const ws = h.sockets.find((s) => s.url.includes("/ws/pty?id=p1"));
let closed = false;
// A browser closes a socket asynchronously: onclose, which would drop the pane's
// hold on it, fires some time after close() is called, not during it.
ws.close = () => { closed = true; };
ws.onopen();
await h.waitFor(() => ws.sent.length > 0);
const identity = await E2E.getIdentity();
const { session: hostSession, response } = await E2E.respondHostHandshake(
  hostPair.privateKey, hostPair.publicKey, identity.publicKey, ws.sent[0]);
const seal = async (tag, plain) => {
  const tagged = new Uint8Array(plain.length + 1);
  tagged[0] = tag;
  tagged.set(plain, 1);
  return hostSession.seal(tagged);
};
const header = () => seal(1, new TextEncoder().encode(JSON.stringify({ epoch: 1, offset: 0, end: 0, resumed: false })));
const output = (s) => seal(0, new TextEncoder().encode(s));
const drawn = () => h.terms.flatMap((t) => t.written.map((w) => new TextDecoder().decode(w)));
`

var e2eOpenedPty = openedPty("")

// TestSealedFramesRightBehindTheHandshakeResponseAreKept covers what the
// desktop actually does: it writes the handshake response and then, with
// nothing in between, the sealed stream header and the replay. The browser
// is still awaiting the key derivation in handshake.finish when those
// messages fire, so they used to find neither a handshake nor a session and
// close the socket. They are now held and opened, in order, once the
// session exists.
func TestSealedFramesRightBehindTheHandshakeResponseAreKept(t *testing.T) {
	runFrontEnd(t, e2eOpenedPty+`
const frames = [await header(), await output("one "), await output("two "), await output("three")];
// All in the same tick, as one burst off the wire is.
ws.onmessage({ data: response.buffer });
for (const f of frames) ws.onmessage({ data: f.buffer });
await h.waitFor(() => drawn().join("") === "one two three");
assert.ok(!closed, "a sealed frame behind the handshake response closed the socket");
assert.deepStrictEqual(drawn(), ["one ", "two ", "three"], "frames were not drawn in the order they were sent");
`)
}

// TestAHeldFrameThatFailsToOpenStillCloses and the tests after it pin down that holding frames during the handshake did not become a
// way to skip authentication.
func TestAHeldFrameThatFailsToOpenStillCloses(t *testing.T) {
	runFrontEnd(t, e2eOpenedPty+`
const good = await header();
const bad = await output("tampered");
const flipped = new Uint8Array(bad);
flipped[flipped.length - 1] ^= 1;
ws.onmessage({ data: response.buffer });
ws.onmessage({ data: good.buffer });
ws.onmessage({ data: flipped.buffer });
await h.waitFor(() => closed);
assert.ok(closed, "a held frame that did not open was not closed");
assert.ok(!drawn().includes("tampered"), "a frame that failed to open was drawn");
`)
}

func TestAHeldFrameDeliveredTwiceIsAReplayAndCloses(t *testing.T) {
	runFrontEnd(t, e2eOpenedPty+`
const f = await output("once");
ws.onmessage({ data: response.buffer });
ws.onmessage({ data: f.buffer });
ws.onmessage({ data: f.buffer.slice(0) });
await h.waitFor(() => closed);
assert.ok(closed, "a replayed frame was not closed");
assert.deepStrictEqual(drawn().filter((s) => s === "once").length, 1, "a replayed frame was drawn twice");
`)
}

func TestAFrameBeforeTheHandshakeStartsIsRefused(t *testing.T) {
	runFrontEnd(t, e2eHostSetup+`
h.recv(fixture());
const ws = h.sockets.find((s) => s.url.includes("/ws/pty?id=p1"));
let closed = false;
const realClose = ws.close.bind(ws);
ws.close = () => { closed = true; realClose(); };
ws.onopen();
// No hello has gone out yet, so there is no handshake to answer.
assert.strictEqual(ws.sent.length, 0);
ws.onmessage({ data: new Uint8Array(40).buffer });
await h.waitFor(() => closed);
assert.ok(closed, "a frame before the handshake started was not refused");
`)
}

func TestATextFrameDuringTheHandshakeIsRefused(t *testing.T) {
	runFrontEnd(t, e2eOpenedPty+`
ws.onmessage({ data: response.buffer });
ws.onmessage({ data: JSON.stringify({ epoch: 1, offset: 0, end: 0, resumed: false }) });
await h.waitFor(() => closed);
assert.ok(closed, "a plain frame during the handshake was not refused");
`)
}

func TestTooManyFramesHeldDuringTheHandshakeCloses(t *testing.T) {
	runFrontEnd(t, e2eOpenedPty+`
ws.onmessage({ data: response.buffer });
// Nothing yields between these, so the handshake cannot have finished.
for (let i = 0; i < 100000 && !closed; i++) ws.onmessage({ data: new Uint8Array(32).buffer });
assert.ok(closed, "an unbounded number of frames were held during the handshake");
`)
}

// reconnectPty is the JS the stale-socket tests share: the first socket
// closes, the pane dials a second, and that one sends its hello.
const e2eReconnect = `
ws.onclose({ code: 1006 });
await h.waitFor(() => h.sockets.filter((s) => s.url.includes("/ws/pty?id=p1")).length > 1);
const ws2 = h.sockets.filter((s) => s.url.includes("/ws/pty?id=p1")).pop();
ws2.onopen();
await h.waitFor(() => ws2.sent.length > 0);
const identity2 = await E2E.getIdentity();
const second = await E2E.respondHostHandshake(hostPair.privateKey, hostPair.publicKey, identity2.publicKey, ws2.sent[0]);
const seal2 = async (plain) => { const t = new Uint8Array(plain.length + 1); t.set(plain, 1); return second.session.seal(t); };
`

func TestTheByteCapOnHeldFramesClosesAndInstallsNoSession(t *testing.T) {
	runFrontEnd(t, openedPty("const gate = newGate();")+`
ws.onmessage({ data: response.buffer });
// 32 x 256 KiB is exactly the cap; the 33rd is over it.
for (let i = 0; i < 33 && !closed; i++) ws.onmessage({ data: new Uint8Array(256 << 10).buffer });
assert.ok(closed, "frames past the byte cap were held");
gate.open();
await pause(300);
// The derivation finishing after the close must not install a session and
// flush the queued resize and focus notices onto the closing socket.
assert.strictEqual(ws.sent.length, 1, "a session was installed on a socket already closed for the cap");
`)
}

func TestATextFrameWhileTheHandshakeFinishesClosesAtOnce(t *testing.T) {
	runFrontEnd(t, openedPty("const gate = newGate();")+`
ws.onmessage({ data: response.buffer });
ws.onmessage({ data: JSON.stringify({ epoch: 1, offset: 0, end: 0, resumed: false }) });
// Still deriving keys: a text frame is refused there and then, not held and
// refused when it fails to open.
assert.ok(closed, "a text frame while the handshake finished was held rather than refused");
gate.open();
`)
}

func TestNothingIsOpenedAfterAFrameFailsToOpen(t *testing.T) {
	runFrontEnd(t, openedPty("const gate = newGate();")+`
const a = await output("a");
const b = await output("b");
const c = await output("c");
const flipped = new Uint8Array(b);
flipped[flipped.length - 1] ^= 1;
ws.onmessage({ data: response.buffer });
ws.onmessage({ data: a.buffer });
ws.onmessage({ data: flipped.buffer });
ws.onmessage({ data: c.buffer });
gate.open();
await h.waitFor(() => closed);
await pause(300);
assert.strictEqual(opens, 2, "frames after the one that failed were still opened");
assert.ok(!drawn().includes("c"), "a frame after the one that failed was drawn");
`)
}

func TestFramesHeldForAnAbandonedSocketAreNotCarriedToTheNext(t *testing.T) {
	runFrontEnd(t, openedPty("const gate = newGate();")+`
const stale = await output("stale");
ws.onmessage({ data: response.buffer });
ws.onmessage({ data: stale.buffer });
`+e2eReconnect+`
ws2.onmessage({ data: second.response.buffer });
const fresh = await seal2(new TextEncoder().encode("fresh"));
ws2.onmessage({ data: fresh.buffer });
await h.waitFor(() => drawn().includes("fresh"));
gate.open();
await pause(200);
assert.deepStrictEqual(drawn(), ["fresh"], "the abandoned socket's frames reached the new one");
`)
}

func TestAnAbandonedSocketsFailedHandshakeLeavesTheNewOneAlone(t *testing.T) {
	runFrontEnd(t, openedPty("const gate = newGate();")+`
ws.onmessage({ data: response.buffer });
`+e2eReconnect+`
// The first socket's derivation fails only now, with the second one's
// handshake under way.
gate.open(new Error("derivation failed"));
await pause(200);
ws2.onmessage({ data: second.response.buffer });
const fresh = await seal2(new TextEncoder().encode("fresh"));
ws2.onmessage({ data: fresh.buffer });
await h.waitFor(() => drawn().includes("fresh"));
assert.deepStrictEqual(drawn(), ["fresh"]);
`)
}
