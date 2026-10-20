// pty.js is the client of one pane's terminal socket, /ws/pty. It is the socket
// half of what connectPTY does in the old window's app.js, without the
// terminal: it opens the socket, asks only for the output it has not already
// got, seals every frame when the window is reached through the relay and the
// desktop has a key, and reconnects with a backoff.
//
// The end-to-end layer is e2e.js, loaded as a classic script that puts
// FlockdeckE2E on the global object; it is passed in as `e2e`, and is the old
// window's file, unmodified. Once a key is given (hostKey), nothing is ever
// sent or read in the clear on the socket: a handshake that cannot start or
// finish, a frame that does not open, and any frame that is not sealed all
// close the socket, which is then dialled again from a fresh handshake. It is
// never served unencrypted.
//
// Nothing here resizes the pane. sendResize is called by whoever decides the
// size should change (terminal.js does so only when the person drags or
// maximises), and the socket asks for size=1 so that the server tells this
// window the size of the pty instead of being told ours.
//
// Options (all but paneId and url are optional):
//   paneId, url        the pane and the address of /ws/pty (ptyUrl builds it)
//   hostKey            the desktop's end-to-end public key, or null for a plain socket
//   e2e                FlockdeckE2E, needed when hostKey is set
//   viewOnly()         true for a viewer: its keystrokes are not sent
//   onSize(cols,rows)  the server says the pty is this size
//   onRun({reset, replayEnd})   a run of output opens; reset says to start the
//                      screen again; replayEnd is the stream offset where history ends
//   onBytes(bytes, endsReplay)  output; endsReplay marks the last of the history
//   onState(state)     { phase: "idle"|"connecting"|"open"|"waiting"|"refused", reason, retryInMs }
//   onOpen()           the socket opened (before any handshake)
//   WebSocket, setTimeout, clearTimeout, now   for tests

/** INPUT_FRAME is the most input one frame carries. The server takes a frame
 *  of up to 4 MiB from a terminal and drops the connection over a bigger
 *  one, so a larger paste is cut into frames of this size. */
export const INPUT_FRAME = 1 << 20;

/** PTY_REFUSED is websocket.StatusPolicyViolation (1008), the one close the
 *  server gives for a reason that retrying never fixes: a device the desk has
 *  limited, or an end-to-end handshake that failed. */
export const PTY_REFUSED = 1008;

/** STABLE_MS is how long a socket must have stayed open for the next drop to
 *  start the retry backoff over. */
export const STABLE_MS = 1000;

/** HANDSHAKE_MS is how long the desktop's handshake response may take, the same
 *  as the desktop's own e2eHandshakeTimeout. */
export const HANDSHAKE_MS = 10000;

/** The most sealed frames, and bytes, held while the handshake response is
 *  still being processed. The desktop writes its stream header and a replay
 *  right behind the response; a peer sending more is not waiting for the
 *  handshake and is cut off. */
export const EARLY_MAX_FRAMES = 2048;
export const EARLY_MAX_BYTES = 8 * 1024 * 1024;

/** The one-byte tag every sealed plaintext starts with: pty bytes or a JSON
 *  header or control notice. They match the desktop's e2eTagBinary and
 *  e2eTagText (internal/server/e2e.go). */
export const FRAME_BINARY = 0;
export const FRAME_TEXT = 1;

const OPEN = 1; // WebSocket.OPEN, which a fake socket need not carry

/** retryDelay is the wait before reconnect attempt number n, counting from 0:
 *  250 ms, 500 ms, and so on up to 3 s. */
export function retryDelay(n) {
  return Math.min(250 * 2 ** n, 3000);
}

/** ptyUrl is the address of the pane's terminal socket for the page at `loc`.
 *  held says how much output the terminal already has: a reconnect asks only
 *  for what it missed. There is no token in it; the socket is authorised by
 *  the page's own session. */
export function ptyUrl(loc, paneId, held) {
  const dir = new URL(loc.pathname.replace(/[^/]*$/, ""), loc.href);
  const url = new URL("ws/pty", dir);
  url.protocol = loc.protocol === "https:" ? "wss:" : "ws:";
  // Written as an object so that the content-policy scan, which reads the
  // word before a quote as an import, has nothing to mistake.
  const query = held && !held.fresh
    ? { id: paneId, size: "1", epoch: String(held.epoch), from: String(held.offset) }
    : { id: paneId, size: "1", from: "-1" };
  url.search = new URLSearchParams(query).toString();
  return url.href;
}

export function createPty(opts) {
  const WS = opts.WebSocket || globalThis.WebSocket;
  const setT = opts.setTimeout || ((fn, ms) => globalThis.setTimeout(fn, ms));
  const clearT = opts.clearTimeout || ((id) => globalThis.clearTimeout(id));
  const now = opts.now || (() => Date.now());
  const hostKey = opts.hostKey || null;
  const e2e = opts.e2e;
  const enc = new TextEncoder();
  const dec = new TextDecoder();

  let ws = null;
  let running = false;
  let retries = 0;
  let retryTimer = null;
  let connectedAt = 0;
  let phase = "idle";

  // What the server last said about the stream, so a reconnect can ask for
  // only what is missing. fresh: nothing has been delivered on this run yet.
  let stream = null;
  let replayEnd = 0;

  // Per socket. A fresh socket is a fresh session: the last one's keys are good
  // for that socket alone, and anything queued for it is stale.
  let handshake = null;
  let handshakeTimer = null;
  let finishing = false;
  let early = [];
  let earlyBytes = 0;
  let recvDead = false;
  let session = null;
  let pending = [];
  let sendQueue = Promise.resolve();
  let recvQueue = Promise.resolve();

  const setPhase = (next, extra) => {
    phase = next;
    if (opts.onState) opts.onState({ phase: next, reason: "", retryInMs: 0, ...extra });
  };

  function resetSocketState() {
    clearT(handshakeTimer);
    handshake = null;
    finishing = false;
    early = [];
    earlyBytes = 0;
    recvDead = false;
    session = null;
    pending = [];
    sendQueue = Promise.resolve();
    recvQueue = Promise.resolve();
  }

  function connect() {
    if (!running) return;
    const old = ws;
    ws = null; // so the old socket's close is seen for what it is
    if (old) { try { old.close(); } catch { /* already closed */ } }
    clearT(retryTimer);
    retryTimer = null;
    resetSocketState();
    setPhase("connecting");
    let sock;
    try {
      sock = new WS(opts.url(stream));
    } catch {
      scheduleRetry();
      return;
    }
    sock.binaryType = "arraybuffer";
    ws = sock;

    sock.onopen = () => {
      if (ws !== sock) return;
      connectedAt = now();
      setPhase("open");
      if (opts.onOpen) opts.onOpen();
      // The handshake is the very first thing this socket sends; send() holds
      // everything else until it has finished.
      if (hostKey) beginHandshake(sock);
    };
    sock.onmessage = (ev) => {
      if (ws !== sock) return;
      if (hostKey) { receiveEncrypted(sock, ev.data); return; }
      if (typeof ev.data === "string") handleHeader(ev.data);
      else handleBytes(new Uint8Array(ev.data));
    };
    sock.onclose = (ev) => {
      ev = ev || {};
      if (ws !== sock) return;
      ws = null;
      if (!running) return;
      // A device the desk has since limited is refused with 1008. It is let go
      // quietly: the control socket's new role is what reconnects it.
      if (ev.code === PTY_REFUSED && opts.viewOnly && opts.viewOnly()) { setPhase("idle"); return; }
      if (ev.code === PTY_REFUSED) {
        setPhase("refused", { reason: ev.reason || "the connection was refused" });
        return;
      }
      // A connection that held a good while and then dropped is a new problem,
      // not the next failure of one already failing.
      if (connectedAt && now() - connectedAt >= STABLE_MS) retries = 0;
      connectedAt = 0;
      scheduleRetry();
    };
    sock.onerror = () => {
      try { sock.close(); } catch { /* already closing */ }
    };
  }

  function scheduleRetry() {
    if (!running) return;
    const retryInMs = retryDelay(retries++);
    setPhase("waiting", { retryInMs });
    retryTimer = setT(connect, retryInMs);
  }

  // ----------------------------------------------------------- the end-to-end layer

  /** receiveEncrypted is every incoming frame on a socket that must be sealed.
   *  The desktop sends the one handshake response, then only sealed frames,
   *  ever again. Anything else, including a plain frame, which is what a relay
   *  stripping or corrupting the handshake to force a downgrade would have to
   *  substitute, ends the connection. */
  function receiveEncrypted(sock, data) {
    if (session) {
      if (typeof data === "string") { closeForFailedHandshake(sock, "a non-binary frame arrived on an encrypted terminal socket"); return; }
      receiveSealed(sock, session, new Uint8Array(data));
      return;
    }
    if (finishing) {
      // The response is accepted and the keys are being derived. A sealed
      // frame now is the desktop's own, written right behind its response:
      // hold it, bounded, and open it with the rest once the session exists.
      if (typeof data === "string") { closeForFailedHandshake(sock, "a non-binary frame arrived on an encrypted terminal socket"); return; }
      const frame = new Uint8Array(data);
      if (early.length >= EARLY_MAX_FRAMES || earlyBytes + frame.length > EARLY_MAX_BYTES) {
        closeForFailedHandshake(sock, "too many frames arrived before the handshake finished");
        return;
      }
      early.push(frame);
      earlyBytes += frame.length;
      return;
    }
    if (handshake && typeof data !== "string") { finishHandshake(sock, new Uint8Array(data)); return; }
    closeForFailedHandshake(sock, "an unexpected frame arrived before the handshake finished");
  }

  async function beginHandshake(sock) {
    let started;
    try {
      const identity = await e2e.getIdentity();
      if (!identity) { closeForFailedHandshake(sock, "this window has no identity of its own to encrypt with"); return; }
      started = await e2e.startTerminalHandshake(identity, hostKey);
    } catch (err) {
      console.error("e2e: could not start a handshake", err);
      closeForFailedHandshake(sock, "could not start a handshake");
      return;
    }
    if (ws !== sock) return; // reconnected while awaited above
    handshake = started.handshake;
    sock.send(started.hello);
    handshakeTimer = setT(() => {
      if (ws === sock && !session) closeForFailedHandshake(sock, "the desktop never answered the handshake");
    }, HANDSHAKE_MS);
  }

  async function finishHandshake(sock, response) {
    clearT(handshakeTimer);
    if (!handshake) return;
    const hs = handshake;
    handshake = null;
    // The desktop writes its sealed stream header right behind the response, so
    // frames can arrive while the keys are derived; they wait in `early`.
    finishing = true;
    let made;
    try {
      made = await hs.finish(response);
    } catch (err) {
      console.error("e2e: the desktop's handshake response did not check out", err);
      // A socket this has since moved on from has no state left to clear.
      if (ws !== sock) { try { sock.close(); } catch { /* already closing */ } return; }
      closeForFailedHandshake(sock, "the desktop's handshake response did not check out");
      return;
    }
    // finishing is cleared by anything that closed the socket while the keys
    // were derived: no session is installed on a socket already being closed.
    if (ws !== sock || !finishing) return;
    session = made;
    finishing = false;
    // Opened in arrival order on the same chain as every later frame, so each
    // is authenticated and the strict frame counter still rejects a reordered,
    // dropped or replayed one.
    const held = early;
    early = [];
    earlyBytes = 0;
    for (const frame of held) receiveSealed(sock, session, frame);
    const queued = pending.splice(0);
    for (const { payload, isText } of queued) queueSealed(sock, payload, isText);
  }

  function receiveSealed(sock, sess, frame) {
    recvQueue = recvQueue.then(() => (recvDead ? null : sess.open(frame))).then((plain) => {
      if (!plain || ws !== sock || !plain.length) return;
      if (plain[0] === FRAME_TEXT) handleHeader(dec.decode(plain.subarray(1)));
      else handleBytes(plain.subarray(1));
    }).catch((err) => {
      // Tampered with, reordered, dropped, or a bug on either end: there is no
      // telling which, and no carrying on with a stream whose next frame might
      // be any of those. Nothing after the failed frame is opened or drawn.
      console.error("e2e: a frame from the desktop did not check out", err);
      if (ws === sock) recvDead = true;
      try { sock.close(); } catch { /* already closing */ }
    });
  }

  function queueSealed(sock, payload, isText) {
    const bytes = isText ? enc.encode(payload) : (payload instanceof Uint8Array ? payload : new Uint8Array(payload));
    const tagged = new Uint8Array(bytes.length + 1);
    tagged[0] = isText ? FRAME_TEXT : FRAME_BINARY;
    tagged.set(bytes, 1);
    const sess = session;
    // Chained, so frames reach the socket in the order seal gave their
    // counters, whatever order WebCrypto's promises settle in.
    sendQueue = sendQueue.then(() => sess.seal(tagged)).then((frame) => {
      if (ws === sock && sock.readyState === OPEN) sock.send(frame);
    }).catch((err) => console.error("e2e: could not seal an outgoing frame", err));
  }

  function closeForFailedHandshake(sock, reason) {
    clearT(handshakeTimer);
    handshake = null;
    finishing = false;
    early = [];
    earlyBytes = 0;
    console.error("e2e: " + reason + " -- closing rather than falling back to an unencrypted terminal");
    try { sock.close(); } catch { /* already closing */ }
  }

  // ----------------------------------------------------------- the stream

  function handleHeader(text) {
    let h;
    try { h = JSON.parse(text); } catch { return; }
    if (!h || typeof h !== "object") return;
    // The size of the pty behind the pane, which is not the opening of a run.
    if (h.size) { if (opts.onSize) opts.onSize(h.size.cols, h.size.rows); return; }
    stream = { epoch: h.epoch, offset: h.offset, fresh: !h.resumed };
    // Bytes up to h.end are history. The terminal answers the questions in
    // them as though they had just been asked, so its answers are kept from the
    // program until it has drawn the last of them.
    replayEnd = h.end > h.offset ? h.end : 0;
    if (opts.onRun) opts.onRun({ reset: !h.resumed, replayEnd });
  }

  function handleBytes(bytes) {
    if (stream) { stream.offset += bytes.byteLength; stream.fresh = false; }
    let endsReplay = false;
    if (replayEnd && stream && stream.offset >= replayEnd) { replayEnd = 0; endsReplay = true; }
    if (opts.onBytes) opts.onBytes(bytes, endsReplay);
  }

  // ----------------------------------------------------------- sending

  /** send is every outgoing message: a resize or focus notice (JSON, isText)
   *  or keystrokes (bytes). Sealed when the desktop has a key, and held until
   *  a handshake in progress succeeds: nothing is sent plain once one is
   *  expected. */
  function send(payload, isText) {
    const sock = ws;
    if (!sock || sock.readyState !== OPEN) return false;
    if (!hostKey) { sock.send(payload); return true; }
    if (session) { queueSealed(sock, payload, isText); return true; }
    pending.push({ payload, isText });
    return true;
  }

  return {
    start() {
      if (running) return;
      running = true;
      retries = 0;
      connect();
    },
    stop() {
      running = false;
      clearT(retryTimer);
      retryTimer = null;
      resetSocketState();
      const sock = ws;
      ws = null;
      if (sock) { try { sock.close(); } catch { /* already closed */ } }
      setPhase("idle");
    },
    /** retryNow dials again at once, as "Try again" does after a refusal. */
    retryNow() {
      if (!running) return;
      retries = 0;
      connect();
    },
    sendInput(text) { return this.sendBytes(enc.encode(text)); },
    sendBytes(bytes) {
      if (opts.viewOnly && opts.viewOnly()) return false; // refused by the server anyway
      if (bytes.length <= INPUT_FRAME) return send(bytes, false);
      let ok = true;
      for (let at = 0; at < bytes.length; at += INPUT_FRAME) ok = send(bytes.subarray(at, at + INPUT_FRAME), false) && ok;
      return ok;
    },
    sendResize(cols, rows) { return send(JSON.stringify({ resize: { cols, rows } }), true); },
    sendFocus() { return send(JSON.stringify({ focus: true }), true); },
    get phase() { return phase; },
    get stream() { return stream; },
  };
}
