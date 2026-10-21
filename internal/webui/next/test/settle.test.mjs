// settle() is what every page test waits on. It must wait for Preact's work and
// not for a time: a chain of effects, each making the state change that starts
// the next, is finished when it returns, however slow the timers are.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../assets/vendor/preact.mjs";
import { useEffect, useState } from "../assets/vendor/hooks.mjs";
import { html } from "../assets/html.js";
import { createDom, install, settle, settleLimits } from "./dom.mjs";

function Chain({ depth }) {
  const [n, setN] = useState(0);
  useEffect(() => {
    if (n < depth) setN(n + 1);
  }, [n]);
  return html`<p>${n}</p>`;
}

async function drawChain(depth) {
  const dom = createDom();
  const restore = install(dom);
  const box = dom.document.createElement("div");
  dom.body.appendChild(box);
  try {
    render(html`<${Chain} depth=${depth} />`, box);
    await settle();
    return box.textContent;
  } finally {
    render(null, box);
    restore();
  }
}

test("settle waits for a chain of effects, each starting the next", async () => {
  assert.equal(await drawChain(8), "8");
});

test("settle does not depend on how late the timers fire", async () => {
  // A loaded machine runs timers late. Every timer here is held back by 60 ms,
  // longer than the 35 ms Preact waits for a frame, so a wait on a time would
  // return with the chain half done.
  const real = globalThis.setTimeout;
  globalThis.setTimeout = (fn, ms, ...rest) => real(fn, (ms || 0) + 60, ...rest);
  try {
    assert.equal(await drawChain(5), "5");
  } finally {
    globalThis.setTimeout = real;
  }
});

test("install puts Preact's frame hook back", async () => {
  const { options } = await import("../assets/vendor/preact.mjs");
  const before = options.requestAnimationFrame;
  const restore = install(createDom());
  assert.equal(typeof options.requestAnimationFrame, "function");
  restore();
  assert.equal(options.requestAnimationFrame, before);
});

test("settle says so when the page never goes quiet", async () => {
  // An effect that sets state on every render is a loop with no end.
  let stop = false;
  function Runaway() {
    const [n, setN] = useState(0);
    useEffect(() => {
      if (!stop) setN(n + 1);
    });
    return html`<p>${n}</p>`;
  }
  const dom = createDom();
  const restore = install(dom);
  const box = dom.document.createElement("div");
  dom.body.appendChild(box);
  const was = { ...settleLimits };
  settleLimits.turns = 20;
  settleLimits.ms = 5000;
  try {
    render(html`<${Runaway} />`, box);
    // The turns limit stops a runaway well before the time one does.
    const started = Date.now();
    await assert.rejects(settle(), /settle: still busy after \d+ turns/);
    assert.ok(Date.now() - started < 1000, "the first rejection came from the turns limit, not the clock");
    // And by time, when the turns are not reached first.
    settleLimits.turns = 1e9;
    settleLimits.ms = 50;
    await assert.rejects(settle(), /settle: still busy/);
  } finally {
    stop = true;
    Object.assign(settleLimits, was);
    await settle();
    render(null, box);
    restore();
  }
});

test("install puts the hook back when the last one is undone, in any order", async () => {
  const { options } = await import("../assets/vendor/preact.mjs");
  const before = options.requestAnimationFrame;
  const a = install(createDom());
  const b = install(createDom());
  a();
  assert.equal(typeof options.requestAnimationFrame, "function", "b is still installed");
  b();
  assert.equal(options.requestAnimationFrame, before);
  const c = install(createDom());
  const d = install(createDom());
  d();
  c();
  assert.equal(options.requestAnimationFrame, before);
});
