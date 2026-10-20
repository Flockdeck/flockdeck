// socket.js is the client of the control socket, /ws/control. It opens the
// socket, hands every well-formed frame to a callback, sends commands, and
// reconnects with a backoff when the socket drops. It knows nothing of what the
// frames mean, except that a "hello" is the sign of a good connection: the
// backoff can start over after one: when a connection that said hello has then
// lasted GOOD_AFTER_MS, the next retry waits the shortest time. A socket that
// merely opens, or says hello and drops at once, keeps the delay growing, so a
// server that does that is not retried once a second for ever.
//
// Everything it needs from outside (the WebSocket constructor and the timers)
// can be passed in, which is how the tests drive it with a fake socket and a
// fake clock.

/** GOOD_AFTER_MS is how long a connection must last after its hello for the
 *  backoff to start over when it drops. */
export const GOOD_AFTER_MS = 5000;

export const BACKOFF = Object.freeze({ baseMs: 1000, factor: 2, maxMs: 30000 });

/** retryDelay is how long to wait before reconnect attempt number `attempt`,
 *  counting from 0: 1 s, 2 s, 4 s, and so on up to 30 s. */
export function retryDelay(attempt, backoff = BACKOFF) {
  return Math.min(backoff.maxMs, backoff.baseMs * backoff.factor ** attempt);
}

/** controlUrl is the address of the control socket for the page at `loc`
 *  (the page's location). Through the relay the page lives under a prefix, so
 *  the path is the page's own directory and not the root. */
export function controlUrl(loc) {
  const dir = new URL(loc.pathname.replace(/[^/]*$/, ""), loc.href);
  const url = new URL("ws/control", dir);
  url.protocol = loc.protocol === "https:" ? "wss:" : "ws:";
  return url.href;
}

const OPEN = 1; // WebSocket.OPEN, which a fake socket need not carry

/** createSocket returns { start, stop, send, phase }.
 *
 *  Options:
 *    url          the socket's address (controlUrl)
 *    onFrame(msg) called with every frame that parsed to an object with a
 *                 string "type"; any other frame is dropped
 *    onPhase(phase, info) called when the phase changes; info has retryInMs
 *                 while the phase is "waiting"
 *    onDrop(raw)  optional, called with a frame that was dropped
 *    WebSocket, setTimeout, clearTimeout, now, backoff   for tests
 *
 *  The phases are "idle" (not started or stopped), "connecting", "open" and
 *  "waiting" (between attempts). */
export function createSocket(opts) {
  const WS = opts.WebSocket || globalThis.WebSocket;
  const setT = opts.setTimeout || ((fn, ms) => globalThis.setTimeout(fn, ms));
  const clearT = opts.clearTimeout || ((id) => globalThis.clearTimeout(id));
  const now = opts.now || (() => Date.now());
  const backoff = opts.backoff || BACKOFF;
  let helloAt = null; // when this connection said hello
  let ws = null;
  let timer = null;
  let attempt = 0;
  let running = false;
  let phase = "idle";

  const setPhase = (next, info) => {
    phase = next;
    if (opts.onPhase) opts.onPhase(next, info || {});
  };

  const retry = () => {
    if (!running) return;
    if (helloAt !== null && now() - helloAt >= GOOD_AFTER_MS) attempt = 0;
    helloAt = null;
    const retryInMs = retryDelay(attempt++, backoff);
    setPhase("waiting", { retryInMs });
    timer = setT(connect, retryInMs);
  };

  function connect() {
    timer = null;
    if (!running) return;
    setPhase("connecting");
    helloAt = null;
    let sock;
    try {
      sock = new WS(opts.url);
    } catch {
      retry();
      return;
    }
    ws = sock;
    // Every handler asks whether this is still the current socket, so an event
    // already in flight from one that was given up on does nothing.
    sock.onopen = () => {
      if (ws === sock) setPhase("open");
    };
    sock.onmessage = (ev) => {
      if (ws !== sock) return;
      const msg = parse(ev.data);
      if (!msg) {
        if (opts.onDrop) opts.onDrop(ev.data);
        return;
      }
      if (msg.type === "hello") helloAt = now();
      opts.onFrame(msg);
    };
    sock.onclose = () => {
      if (ws !== sock) return;
      ws = null;
      retry();
    };
    sock.onerror = () => {
      try { sock.close(); } catch { /* already closing */ }
    };
  }

  return {
    start() {
      if (running) return;
      running = true;
      attempt = 0;
      helloAt = null;
      connect();
    },
    stop() {
      running = false;
      if (timer !== null) { clearT(timer); timer = null; }
      const sock = ws;
      ws = null;
      if (sock) { try { sock.close(); } catch { /* already closed */ } }
      setPhase("idle");
    },
    /** send writes one command as JSON. It returns false, and sends nothing,
     *  when the socket is not open. */
    send(cmd) {
      if (!ws || ws.readyState !== OPEN) return false;
      ws.send(JSON.stringify(cmd));
      return true;
    },
    get phase() { return phase; },
  };
}

function parse(data) {
  if (typeof data !== "string") return null;
  let msg;
  try { msg = JSON.parse(data); } catch { return null; }
  if (msg === null || typeof msg !== "object" || Array.isArray(msg) || typeof msg.type !== "string") return null;
  return msg;
}
