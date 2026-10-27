// The agent and terminal right-click menus: which items a pane gets and what each
// one calls, and the terminal controller's parts the terminal menu reads.
import { test } from "node:test";
import assert from "node:assert/strict";
import { agentMenu, termMenu } from "../src/shell/panemenus.js";
import { resolve } from "../src/shell/menus.js";
import { linkIn } from "../src/lib/terminal.js";

const ACTIONS = ["paneInfo", "changes", "restartPane", "lockPane", "makeBaton", "recordPane", "exportTranscript", "revealTranscript", "closePane", "findInTerminal", "zoomPane"];
const KEYS = ACTIONS.map((id) => ({ id, keys: "", label: id }));
const el = (attrs) => ({ getAttribute: (k) => (k in attrs ? attrs[k] : null) });

function bag(extra = {}) {
  const calls = [];
  const ctx = {
    keys: KEYS,
    implemented: ACTIONS,
    act: (id) => calls.push(["act", id]),
    drawer: { open: (p) => calls.push(["open", p]) },
    copy: (t, w) => calls.push(["copy", t, w]),
    say: (t) => calls.push(["say", t]),
    viewer: false,
    remote: false,
    pane: {
      of: (id) => ({ node: { pane: id, name: "Ada" }, state: { cwd: "/w", locked: false, recording: false } }),
      run: (name, id) => calls.push(["run", name, id]),
    },
    ...extra,
  };
  return { ctx, calls };
}

const labels = (items) => items.filter((i) => !i.separator).map((i) => i.label);

test("an agent row gets the pane's actions, and each one acts on that row's pane", () => {
  const { ctx, calls } = bag();
  const items = resolve(agentMenu(el({ "data-pane": "p9" }), ctx), ctx);
  assert.deepEqual(labels(items), ["Open", "paneInfo", "changes", "restartPane", "Lock", "makeBaton", "Start recording", "exportTranscript", "revealTranscript", "Copy pane id", "Copy name", "closePane"]);
  for (const i of items.filter((x) => !x.separator)) i.run();
  const runs = calls.filter((c) => c[0] === "run");
  assert.ok(runs.length >= 6 && runs.every((c) => c[2] === "p9"));
  assert.ok(calls.some((c) => c[0] === "run" && c[1] === "closePane"));
  assert.deepEqual(calls.filter((c) => c[0] === "copy"), [["copy", "p9", "the pane id"], ["copy", "Ada", "the name"]]);
});

test("a locked pane shows Lock checked; a viewer gets no actions that change it", () => {
  const locked = bag();
  locked.ctx.pane.of = () => ({ node: { pane: "p", name: "A" }, state: { locked: true } });
  const lock = resolve(agentMenu(el({ "data-pane": "p" }), locked.ctx), locked.ctx).find((i) => i.id === "lockPane");
  assert.equal(lock.checked, true);
  const v = bag({ viewer: true });
  assert.deepEqual(labels(resolve(agentMenu(el({ "data-pane": "p" }), v.ctx), v.ctx)), ["Open", "paneInfo", "changes", "Copy pane id", "Copy name"]);
});

test("a pane with no folder has no Review changes", () => {
  const { ctx } = bag();
  ctx.pane.of = () => ({ node: { pane: "p", name: "A" }, state: {} });
  assert.ok(!resolve(agentMenu(el({ "data-pane": "p" }), ctx), ctx).some((i) => i.id === "changes"));
});

function fakeTerm(over = {}) {
  const log = [];
  return {
    log,
    mouseTracking: () => false,
    linkAt: () => "",
    selection: () => "",
    selectAll: () => log.push("selectAll"),
    clear: () => log.push("clear"),
    pasteText: (t) => log.push(["paste", t]),
    ...over,
  };
}

function termBag(ctl, extra = {}) {
  const b = bag(extra);
  b.ctx.pane.terminal = () => ctl;
  b.ctx.pane.clipboard = () => ({ readText: async () => "hello" });
  b.ctx.pane.openLink = (u) => b.calls.push(["link", u]);
  return b;
}

test("the terminal menu copies the selection, pastes, selects all and clears", async () => {
  const ctl = fakeTerm({ selection: () => "abc" });
  const { ctx, calls } = termBag(ctl);
  const items = resolve(termMenu(el({}), ctx, { clientX: 5, clientY: 5 }), ctx);
  assert.deepEqual(labels(items), ["Copy", "Paste", "Select all", "Clear", "findInTerminal", "zoomPane", "restartPane", "closePane"]);
  for (const id of ["copy", "paste", "select-all", "clear"]) await items.find((i) => i.id === id).run();
  assert.deepEqual(calls, [["copy", "abc", "the selection"]]);
  assert.deepEqual(ctl.log, [["paste", "hello"], "selectAll", "clear"]);
});

test("Copy is disabled with nothing selected, and Paste is left out for a viewer or without a clipboard", () => {
  const { ctx } = termBag(fakeTerm());
  const items = resolve(termMenu(el({}), ctx, null), ctx);
  assert.equal(items.find((i) => i.id === "copy").disabled, true);
  const v = termBag(fakeTerm(), { viewer: true });
  assert.ok(!resolve(termMenu(el({}), v.ctx, null), v.ctx).some((i) => i.id === "paste" || i.id === "restartPane"));
  const none = termBag(fakeTerm());
  none.ctx.pane.clipboard = () => null;
  assert.ok(!resolve(termMenu(el({}), none.ctx, null), none.ctx).some((i) => i.id === "paste"));
});

test("a failed paste says so", async () => {
  const { ctx, calls } = termBag(fakeTerm());
  ctx.pane.clipboard = () => ({ readText: async () => { throw new Error("no"); } });
  await resolve(termMenu(el({}), ctx, null), ctx).find((i) => i.id === "paste").run();
  assert.equal(calls[0][0], "say");
});

test("a link under the click is offered first", () => {
  const ctl = fakeTerm({ linkAt: () => "https://example.com/a" });
  const { ctx, calls } = termBag(ctl);
  const items = resolve(termMenu(el({}), ctx, { clientX: 3, clientY: 4 }), ctx);
  assert.deepEqual(labels(items).slice(0, 2), ["Open link", "Copy link"]);
  items[0].run();
  items[1].run();
  assert.deepEqual(calls, [["link", "https://example.com/a"], ["copy", "https://example.com/a", "the link"]]);
});

test("a program that asked for the mouse gets the click; Shift brings the menu back", () => {
  const { ctx } = termBag(fakeTerm({ mouseTracking: () => true }));
  assert.deepEqual(termMenu(el({}), ctx, { clientX: 1, clientY: 1 }), []);
  assert.ok(termMenu(el({}), ctx, { clientX: 1, clientY: 1, shiftKey: true }).length > 0);
});

test("a terminal that is not there has no menu", () => {
  const { ctx } = termBag(null);
  assert.deepEqual(termMenu(el({}), ctx, null), []);
});

test("linkIn finds the address under a column and drops closing punctuation", () => {
  const t = "see https://example.com/a?b=1, or (https://x.org/p).";
  assert.equal(linkIn(t, 6), "https://example.com/a?b=1");
  assert.equal(linkIn(t, 0), "");
  assert.equal(linkIn(t, t.indexOf("x.org")), "https://x.org/p");
  assert.equal(linkIn(t, t.length - 1), "");
});
