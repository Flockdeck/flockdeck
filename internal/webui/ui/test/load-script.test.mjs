// The loader of xterm, its fit addon and e2e.js, and the registration of the
// end-to-end key on a remote hello. A fake page stands in for the document: a
// script element is "fetched" when the test says, and then loads or fails.
import { test } from "node:test";
import assert from "node:assert/strict";
import { loadScript, loadE2E, loadTerminalLibs, SCRIPTS } from "../assets/lib/load-script.js";
import { watchRemoteHello } from "../assets/lib/e2e-boot.js";
import { fakeStore, waitFor } from "./termfakes.mjs";

function fakePage() {
  const added = [];
  const doc = {
    head: {
      appendChild(el) { el.parentNode = doc.head; added.push(el); },
      removeChild(el) { el.parentNode = null; const i = added.indexOf(el); if (i >= 0) added.splice(i, 1); },
    },
    createElement(tag) { return { tag, parentNode: null, async: true, src: "" }; },
  };
  const G = {};
  const env = { document: doc, global: G };
  // arrive: the script at src runs (its global appears) and loads.
  const arrive = (src, define = true) => {
    const el = added.find((e) => e.src === src);
    assert.ok(el, `${src} was not requested`);
    if (define) {
      const def = Object.values(SCRIPTS).find((d) => d.src === src);
      if (def.global === "FitAddon") G.FitAddon = { FitAddon: class {} };
      else if (def.global === "FlockdeckE2E") G.FlockdeckE2E = { ensureRegistered: async () => { G.registered = (G.registered || 0) + 1; } };
      else G[def.global] = class {};
    }
    el.onload();
    return el;
  };
  const fail = (src) => added.find((e) => e.src === src).onerror();
  return { env, G, added, arrive, fail, srcs: () => added.map((e) => e.src) };
}

const tick = () => new Promise((r) => setImmediate(r));

test("the three files are the same-origin relative ones, in the order a terminal needs", () => {
  assert.deepEqual(Object.values(SCRIPTS).map((d) => d.src), ["assets/vendor/xterm.js", "assets/vendor/addon-fit.js", "assets/e2e.js"]);
});

test("xterm, the fit addon and e2e.js are asked for together, added in that order, none async", async () => {
  const p = fakePage();
  const done = loadTerminalLibs(p.env);
  await tick();
  assert.deepEqual(p.srcs(), ["assets/vendor/xterm.js", "assets/vendor/addon-fit.js", "assets/e2e.js"], "downloaded side by side, in the order they must run");
  assert.ok(p.added.every((e) => e.async === false), "a script added with async on can run out of order");
  // They may arrive in any order; the promise is for all three.
  p.arrive("assets/e2e.js");
  p.arrive("assets/vendor/xterm.js");
  let settled = false;
  done.then(() => { settled = true; });
  await tick();
  assert.equal(settled, false, "resolved before the addon had run");
  p.arrive("assets/vendor/addon-fit.js");
  await done;
});

test("however many terminals ask, the files load once", async () => {
  const p = fakePage();
  const all = [loadTerminalLibs(p.env), loadTerminalLibs(p.env), loadTerminalLibs(p.env)];
  assert.equal(all[0], all[1]);
  await tick();
  p.arrive("assets/vendor/xterm.js");
  await tick();
  p.arrive("assets/vendor/addon-fit.js");
  await tick();
  p.arrive("assets/e2e.js");
  await Promise.all(all);
  assert.equal(p.added.length, 3);
  // After they have loaded, asking again adds nothing.
  await loadTerminalLibs(p.env);
  assert.equal(p.added.length, 3);
});

test("e2e.js is run once whether the remote hello or the first terminal asks first", async () => {
  // The hello first.
  const p = fakePage();
  const e2e = loadE2E(p.env);
  await tick();
  assert.deepEqual(p.srcs(), ["assets/e2e.js"]);
  const libs = loadTerminalLibs(p.env);
  await tick();
  p.arrive("assets/vendor/xterm.js");
  await tick();
  p.arrive("assets/vendor/addon-fit.js");
  await tick();
  // The terminal's own chain reaches e2e.js and shares the one request.
  assert.equal(p.srcs().filter((s) => s === "assets/e2e.js").length, 1);
  p.arrive("assets/e2e.js");
  await Promise.all([e2e, libs]);
  assert.equal(p.srcs().filter((s) => s === "assets/e2e.js").length, 1);

  // The terminal first.
  const q = fakePage();
  const libs2 = loadTerminalLibs(q.env);
  await tick();
  q.arrive("assets/vendor/xterm.js");
  await tick();
  q.arrive("assets/vendor/addon-fit.js");
  await tick();
  const e2e2 = loadE2E(q.env);
  q.arrive("assets/e2e.js");
  await Promise.all([libs2, e2e2]);
  assert.equal(q.srcs().filter((s) => s === "assets/e2e.js").length, 1);
});

test("a failed load rejects, removes its element, and the next ask tries again for what is missing", async () => {
  const p = fakePage();
  const first = loadTerminalLibs(p.env);
  await tick();
  p.arrive("assets/vendor/xterm.js");
  p.arrive("assets/e2e.js");
  p.fail("assets/vendor/addon-fit.js");
  await assert.rejects(first, (e) => e.message.startsWith("could not load assets/vendor/addon-fit.js"));
  assert.deepEqual(p.srcs(), ["assets/vendor/xterm.js", "assets/e2e.js"], "the failed element was left in the page");
  const second = loadTerminalLibs(p.env);
  await tick();
  // xterm and e2e.js are not fetched twice; the addon is asked for again.
  assert.deepEqual(p.srcs(), ["assets/vendor/xterm.js", "assets/e2e.js", "assets/vendor/addon-fit.js"]);
  p.arrive("assets/vendor/addon-fit.js");
  await second;
});

test("a script that loads but defines nothing is a failed load", async () => {
  const p = fakePage();
  const first = loadScript(SCRIPTS.xterm, p.env);
  p.arrive("assets/vendor/xterm.js", false);
  await assert.rejects(first, /did not define Terminal/);
  assert.equal(p.added.length, 0);
});

test("a global the page already has is not loaded again", async () => {
  const p = fakePage();
  p.G.Terminal = class {};
  await loadScript(SCRIPTS.xterm, p.env);
  assert.equal(p.added.length, 0);
});

test("there is no page: a rejection, not a throw", async () => {
  await assert.rejects(loadScript(SCRIPTS.xterm, { document: null, global: {} }), /no page/);
  await assert.rejects(loadTerminalLibs({ document: null }), /no page/);
});

// ------------------------------------------------------------ the key on a remote hello

const store = (hello) => fakeStore({ hello, state: null });

test("a remote hello loads e2e.js and registers the key", async () => {
  const p = fakePage();
  const st = store(null);
  watchRemoteHello(st, { loadE2E: () => loadE2E(p.env) });
  await tick();
  assert.equal(p.added.length, 0, "nothing is loaded before a hello");
  st.set({ hello: { type: "hello", remote: true, e2ePublicKey: "K" } });
  await tick();
  assert.deepEqual(p.srcs(), ["assets/e2e.js"]);
  p.arrive("assets/e2e.js");
  await waitFor(() => p.G.registered === 1, "the registration");
});

test("a window on the desktop loads nothing for the key", async () => {
  const p = fakePage();
  const st = store({ type: "hello", remote: false });
  watchRemoteHello(st, { loadE2E: () => loadE2E(p.env) });
  st.set({ hello: { type: "hello" } });
  await tick();
  assert.equal(p.added.length, 0);
});

test("state pushes do not register again; a new hello does, and the file is still run once", async () => {
  const p = fakePage();
  const st = store({ type: "hello", remote: true });
  let calls = 0;
  watchRemoteHello(st, { loadE2E: () => { calls++; return loadE2E(p.env); } });
  await tick();
  p.arrive("assets/e2e.js");
  await waitFor(() => p.G.registered === 1, "the first registration");
  for (let i = 0; i < 5; i++) st.set({ state: { type: "state", n: i } });
  await tick();
  assert.equal(calls, 1, "a state push asked again");
  st.set({ hello: null }); // the control socket dropped
  st.set({ hello: { type: "hello", remote: true } });
  await waitFor(() => p.G.registered === 2, "the registration on the new hello");
  assert.equal(p.added.length, 1, "e2e.js was fetched again");
});

test("a failed load is reported, is not retried on every push, and is retried with the next hello", async () => {
  const p = fakePage();
  const st = store({ type: "hello", remote: true });
  const errors = [];
  watchRemoteHello(st, { loadE2E: () => loadE2E(p.env), onError: (e) => errors.push(e) });
  await tick();
  p.fail("assets/e2e.js");
  await waitFor(() => errors.length === 1, "the report");
  st.set({ state: { type: "state" } });
  await tick();
  assert.equal(p.added.length, 0, "a push retried the load");
  st.set({ hello: null });
  st.set({ hello: { type: "hello", remote: true } });
  await tick();
  assert.deepEqual(p.srcs(), ["assets/e2e.js"]);
  p.arrive("assets/e2e.js");
  await waitFor(() => p.G.registered === 1, "the registration after the retry");
});

test("stopping the watch stops it", async () => {
  const p = fakePage();
  const st = store(null);
  const stop = watchRemoteHello(st, { loadE2E: () => loadE2E(p.env) });
  stop();
  assert.equal(st.subscribers, 0);
  st.set({ hello: { type: "hello", remote: true } });
  await tick();
  assert.equal(p.added.length, 0);
});
