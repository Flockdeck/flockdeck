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
// A failed load or registration is reported and tried again with the next hello
// (the control socket reconnecting), or by the first terminal, which loads
// e2e.js too. It is never retried on every state push.
import { loadE2E as defaultLoadE2E } from "./load-script.js";

/**
 * watchRemoteHello subscribes to the store and registers the key on each hello
 * that says remote. Returns a function that stops it.
 * opts: { loadE2E, onError }.
 */
export function watchRemoteHello(store, opts = {}) {
  const load = opts.loadE2E || (() => defaultLoadE2E());
  const report = opts.onError || ((err) => console.error("e2e: could not register this window's key", err));
  let seen = null;
  let stopped = false;
  const check = () => {
    const hello = store.getState().hello;
    if (stopped || !hello || hello.remote !== true || hello === seen) return;
    seen = hello;
    load()
      .then((E) => E.ensureRegistered())
      .catch(report);
  };
  const unsub = store.subscribe(check);
  check();
  return () => {
    stopped = true;
    unsub();
  };
}
