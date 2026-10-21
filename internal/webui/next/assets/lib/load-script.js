// Loads the classic scripts the terminal needs, when they are needed and not at
// page load: xterm.js, its fit addon and e2e.js (the end-to-end layer). They
// are 131 KB gzipped, and as blocking scripts in the page they were about 1.1 s
// of the first paint on a phone's link (docs/first-load.md).
//
// Each is added as a <script src> from this origin, so the relay's
// script-src 'self' allows it. Nothing is inline and nothing is evaluated from
// text. A script is added once: a load in flight is shared, a load that worked
// is remembered, and a load that failed is forgotten, with its element
// removed, so that asking again tries again.
//
// A script that loads but does not define what it should (an error page served
// with a 200, a file cut short) counts as a failed load.
//
// e2e.js has two ways in: loadE2E (a window reached through the relay needs it
// on its hello) and loadTerminalLibs (the first terminal). They share one load,
// so the file is run once whichever comes first.

/** The files, in the order a terminal needs them, and the global each defines. */
export const SCRIPTS = {
  xterm: { src: "assets/vendor/xterm.js", global: "Terminal" },
  fit: { src: "assets/vendor/addon-fit.js", global: "FitAddon" },
  e2e: { src: "assets/e2e.js", global: "FlockdeckE2E" },
};

// What has loaded or is loading, by document and then file. A WeakMap keyed on
// the document keeps a test's own fake page from sharing state with another.
const state = new WeakMap();

function slot(doc) {
  let s = state.get(doc);
  if (!s) {
    s = { inflight: new Map(), libs: null };
    state.set(doc, s);
  }
  return s;
}

/**
 * loadScript adds one script and resolves when it has run and defined its
 * global. env: { document, global } (the page's by default).
 */
export function loadScript(def, env = {}) {
  const doc = env.document || globalThis.document;
  const G = env.global || globalThis;
  if (!doc || typeof doc.createElement !== "function") {
    return Promise.reject(new Error("there is no page to load " + def.src + " into"));
  }
  // Already there: the page has it, or an earlier load of this file worked.
  if (G[def.global] !== undefined) return Promise.resolve();
  const s = slot(doc);
  if (s.inflight.has(def.src)) return s.inflight.get(def.src);
  const p = new Promise((resolve, reject) => {
    const el = doc.createElement("script");
    const fail = (why) => {
      s.inflight.delete(def.src);
      try { if (el.parentNode) el.parentNode.removeChild(el); } catch { /* already gone */ }
      reject(new Error("could not load " + def.src + (why ? ": " + why : "")));
    };
    // Not async, so scripts added one after another also run in that order.
    el.async = false;
    el.onload = () => {
      if (G[def.global] === undefined) { fail("it did not define " + def.global); return; }
      s.inflight.delete(def.src);
      resolve();
    };
    el.onerror = () => fail("");
    el.src = def.src;
    (doc.head || doc.documentElement).appendChild(el);
  });
  s.inflight.set(def.src, p);
  return p;
}

/**
 * loadE2E loads e2e.js and resolves with FlockdeckE2E. One load however often
 * it is called, and the file runs once.
 */
export function loadE2E(env = {}) {
  const G = env.global || globalThis;
  return loadScript(SCRIPTS.e2e, env).then(() => G.FlockdeckE2E);
}

/**
 * loadTerminalLibs loads xterm, the fit addon and e2e.js, which run in that
 * order, and resolves when all three have run. One load however many terminals ask: they
 * share the promise. After a failure the next call starts again, and files that
 * did load are not fetched twice.
 */
export function loadTerminalLibs(env = {}) {
  const doc = env.document || globalThis.document;
  if (!doc) return Promise.reject(new Error("there is no page to load the terminal into"));
  const s = slot(doc);
  if (s.libs) return s.libs;
  // All three are asked for at once: they download side by side, and as
  // scripts added with async off they run in the order they were added, which is
  // the order a terminal needs. A failure of any one fails the lot, and the next
  // call asks again for what has not loaded.
  const p = Promise.all([SCRIPTS.xterm, SCRIPTS.fit, SCRIPTS.e2e].map((d) => loadScript(d, env))).then(() => {});
  s.libs = p;
  p.then(
    () => {},
    () => { if (s.libs === p) s.libs = null; },
  );
  return p;
}
