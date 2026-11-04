// A window reached through the relay registers its own end-to-end key when its
// hello arrives, before any terminal is opened, so the relay holds the key by the
// time a terminal's handshake needs it. e2e.js is not in the page any more, so
// this loads it first, with loadE2E, which shares its one load with the first
// terminal's: the file runs once, whichever asks first.
//
// ensureRegistered (in e2e.js) does nothing once the relay is known to hold the
// key, so a second hello costs no request. A window on the desktop itself has no
// relay to register with, and its hello is not remote: nothing is loaded for it
// here.
//
// A failed load is reported and tried again with the next hello (the control
// socket reconnecting), or by the first terminal, which loads e2e.js too. A
// registration the relay did not take (ensureRegistered says false) is tried
// again on a backoff for as long as the connection lasts, because nothing else
// would: every terminal's handshake is refused until the relay has the key. It
// is never retried on every state push.
import { loadE2E as defaultLoadE2E } from "./load-script.js";

const RETRY_FIRST_MS = 1000;
const RETRY_MAX_MS = 30000;

/**
 * watchRemoteHello subscribes to the store and registers the key on each hello
 * that says remote. Returns a function that stops it.
 * opts: { loadE2E, onError, setTimeout, clearTimeout }.
 */
export function watchRemoteHello(store, opts = {}) {
  const load = opts.loadE2E || (() => defaultLoadE2E());
  const report = opts.onError || ((err) => console.error("e2e: could not register this window's key", err));
  // One run for each connection: the store empties the hello when the socket
  // drops, and the next connection's hello is a new one. A later change to the
  // hello (the prefs, the key table) is the same connection.
  let seen = false;
  let stopped = false;
  const setT = opts.setTimeout || ((fn, ms) => setTimeout(fn, ms));
  const clearT = opts.clearTimeout || ((id) => clearTimeout(id));
  let retryTimer = null;
  // Counts connections, so a retry waiting out its delay is dropped when the
  // socket it was for has gone.
  let epoch = 0;
  const register = (mine, delay) => {
    load()
      .then((E) => E.ensureRegistered())
      .then((ok) => {
        if (ok !== false || stopped || mine !== epoch) return;
        retryTimer = setT(() => { retryTimer = null; if (!stopped && mine === epoch) register(mine, Math.min(delay * 2, RETRY_MAX_MS)); }, delay);
      })
      .catch(report);
  };
  const check = () => {
    const hello = store.getState().hello;
    if (stopped) return;
    if (!hello) {
      if (seen) { epoch++; if (retryTimer !== null) { clearT(retryTimer); retryTimer = null; } }
      seen = false;
      return;
    }
    if (hello.remote !== true || seen) return;
    seen = true;
    register(epoch, RETRY_FIRST_MS);
  };
  const unsub = store.subscribe(check);
  check();
  return () => {
    stopped = true;
    if (retryTimer !== null) { clearT(retryTimer); retryTimer = null; }
    unsub();
  };
}
