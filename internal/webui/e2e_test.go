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
await h.waitFor(() => h.e2eKeyPosts().length > 0, 1000);

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
await h.waitFor(() => ws.sent.length > 0, 1000);

assert.strictEqual(ws.sent.length, 1, "the handshake hello was not this socket's first message");
const hello = ws.sent[0];

const identity = await E2E.getIdentity();
const { session: hostSession, response } = await E2E.respondHostHandshake(
  hostPair.privateKey, hostPair.publicKey, identity.publicKey, hello);
ws.onmessage({ data: response.buffer });
// finishHandshake runs unawaited from ws.onmessage and itself awaits
// handshake.finish (WebCrypto) before flushing what was queued -- the same
// race as above.
await h.waitFor(() => ws.sent.length > 1, 1000);

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
await h.waitFor(() => ws.sent.length > sentBeforeKeystroke, 1000);
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
await h.waitFor(() => h.terms.some(drew), 1000);

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
await h.sleep(30);
assert.strictEqual(ws.sent.length, 1, "the handshake hello was not sent");

let closed = false;
const realClose = ws.close.bind(ws);
ws.close = () => { closed = true; realClose(); };
ws.onmessage({ data: new Uint8Array([1, 2, 3]).buffer });
await h.sleep(30);
assert.ok(closed, "a handshake response that did not check out was not closed");
`)
}
