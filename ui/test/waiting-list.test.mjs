// The list behind a rail project's orange count. The count and the list are the
// same panes: the server tallies panes that are waiting or blocked (by activity),
// and the list is the panes of the open project with that activity, so the
// heading, the rows and the badge agree.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Shell } from "../src/shell/app.js";
import { waitingOf } from "../src/shell/WaitingList.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeKeys } from "./util.mjs";

const T = (min) => new Date(Date.now() - min * 60000).toISOString();
const perm = { summary: "Run go test ./...\nsecond line", tool: "Bash" };
const ask = { questions: [{ question: "Which branch?\nmore" }] };

// The server's tally: waiting or blocked, by activity.
const tally = (panes) => Object.values(panes).filter((p) => p.activity === "waiting" || p.activity === "blocked").length;

const panes = {
  p1: { id: "p1", name: "writer", status: "waiting", activity: "waiting", statusSince: T(5), permission: perm },
  p2: { id: "p2", name: "asker", status: "waiting", activity: "waiting", statusSince: T(60), ask },
  p3: { id: "p3", name: "stuck", status: "blocked", activity: "blocked", statusSince: T(30), detail: "needs setting up" },
  p4: { id: "p4", name: "bare", status: "waiting", activity: "waiting", statusSince: T(10) },
  p5: { id: "p5", name: "busy", status: "working", activity: "working", statusSince: T(1) },
  p6: { id: "p6", name: "bg", status: "idle", activity: "working", statusSince: T(2) },
};

const project = (root, name, ps) => ({ root, name, waiting: tally(ps), working: Object.values(ps).filter((p) => p.activity === "working").length });

async function mount(first) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const watched = [];
  const opened = [];
  const view = (state) => html`<${Shell} hello=${{ keys: fakeKeys }} state=${state} window=${dom.window} onWatch=${(r) => watched.push(r)} onOpenPane=${(id) => opened.push(id)}>main<//>`;
  render(view(first), container);
  await settle();
  return {
    dom, container, watched, opened, done: restore,
    set: async (state) => { render(view(state), container); await settle(); },
    badge: (root) => container.findAll(".rail-waiting").find((b) => b.parentNode && b.parentNode.find(".rail-item").attributes["data-root"] === root),
    rows: () => container.findAll(".needs-row"),
    head: () => container.find(".needs-head"),
  };
}
async function withRail(state, fn) {
  const m = await mount(state);
  try { await fn(m); } finally { m.done(); }
}

test("waitingOf: none, one permission, one question, one blocked, a waiting pane with no prompt", () => {
  assert.deepEqual(waitingOf({}), []);
  assert.deepEqual(waitingOf(null), []);
  assert.deepEqual(waitingOf({ p5: panes.p5, p6: panes.p6 }), [], "working and background-working panes are not waiting");
  const r = (k) => waitingOf({ [k]: panes[k] })[0];
  assert.deepEqual([r("p1").word, r("p1").detail, r("p1").kind], ["Permission", "Run go test ./...", "permission"]);
  assert.deepEqual([r("p2").word, r("p2").detail, r("p2").kind], ["Question", "Which branch?", "ask"]);
  assert.deepEqual([r("p3").word, r("p3").detail, r("p3").kind], ["Blocked", "needs setting up", "blocked"]);
  assert.deepEqual([r("p4").word, r("p4").detail, r("p4").kind], ["Waiting for input", "", "waiting"]);
});

test("waitingOf: oldest first, a pane with no start time last, ties in the push's order", () => {
  const out = waitingOf({
    a: { id: "a", name: "a", activity: "waiting" },
    b: { id: "b", name: "b", activity: "waiting", statusSince: T(5) },
    c: { id: "c", name: "c", activity: "waiting", statusSince: T(50) },
    d: { id: "d", name: "d", activity: "waiting" },
    e: { id: "e", name: "e", activity: "waiting", statusSince: "0001-01-01T00:00:00Z" },
  });
  assert.deepEqual(out.map((r) => r.id), ["c", "b", "a", "d", "e"]);
});

test("waitingOf falls back to status when a push has no activity", () => {
  assert.equal(waitingOf({ x: { id: "x", name: "x", status: "waiting" } }).length, 1);
  assert.equal(waitingOf({ x: { id: "x", name: "x", status: "idle", activity: "working" } }).length, 0);
});

for (const [name, ids] of [["none", []], ["one permission", ["p1"]], ["one question", ["p2"]], ["one blocked", ["p3"]], ["a pane with no prompt", ["p4"]], ["a mix", ["p1", "p2", "p3", "p4", "p5", "p6"]]]) {
  test("the badge, the heading and the rows are one number: " + name, async () => {
    const ps = Object.fromEntries(ids.map((id) => [id, panes[id]]));
    const state = { root: "/work/a", projects: [project("/work/a", "alpha", ps)], panes: ps };
    await withRail(state, async (m) => {
      const n = tally(ps);
      const badge = m.badge("/work/a");
      if (n === 0) {
        assert.equal(badge, undefined, "no orange count when nothing waits");
        return;
      }
      assert.equal(badge.textContent, String(n));
      badge.click();
      await settle();
      assert.equal(m.rows().length, n);
      assert.match(m.head().textContent, new RegExp("Waiting for you \\(" + n + "\\)"));
      assert.deepEqual(m.rows().map((r) => r.find(".needs-who").textContent), waitingOf(ps).map((r) => r.name), "oldest first");
    });
  });
}

test("rows show the reason word, the first line and the age", async () => {
  const state = { root: "/work/a", projects: [project("/work/a", "alpha", panes)], panes };
  await withRail(state, async (m) => {
    m.badge("/work/a").click();
    await settle();
    const first = m.rows()[0];
    assert.equal(first.find(".needs-who").textContent, "asker");
    assert.equal(first.find(".needs-word").textContent, "Question");
    assert.equal(first.find(".needs-detail").textContent, "Which branch?");
    assert.ok(first.find(".needs-age"));
    const bare = m.rows().find((r) => r.find(".needs-who").textContent === "bare");
    assert.equal(bare.find(".needs-word").textContent, "Waiting for input");
    assert.equal(bare.find(".needs-detail"), null);
  });
});

test("only the open project is listed: choosing another selects it and waits for its push", async () => {
  const a = { p1: panes.p1 };
  const b = { p2: panes.p2, p3: panes.p3 };
  const projects = [project("/work/a", "alpha", a), project("/work/b", "beta", b)];
  await withRail({ root: "/work/a", projects, panes: a }, async (m) => {
    m.badge("/work/b").click();
    await settle();
    assert.deepEqual(m.watched, ["/work/a", "/work/b"], "the project is chosen");
    assert.equal(m.rows().length, 0, "alpha's panes are not beta's list");
    assert.match(m.container.find(".popover").textContent, /Opening the project/);
    await m.set({ root: "/work/b", projects, panes: b });
    assert.equal(m.rows().length, 2);
    assert.match(m.head().textContent, /\(2\)/);
    assert.equal(m.rows().length, projects[1].waiting);
  });
});

test("a pane that stops waiting between pushes leaves the list, and the heading follows its own length", async () => {
  const state = { root: "/work/a", projects: [project("/work/a", "alpha", panes)], panes };
  await withRail(state, async (m) => {
    m.badge("/work/a").click();
    await settle();
    assert.equal(m.rows().length, 4);
    const after = { ...panes, p1: { ...panes.p1, status: "working", activity: "working" } };
    // The push carries the new panes before the project's tally moves.
    await m.set({ root: "/work/a", projects: state.projects, panes: after });
    assert.equal(m.rows().length, 3);
    assert.match(m.head().textContent, /\(3\)/);
    assert.equal(m.badge("/work/a").textContent, "4", "the badge follows the tally it was sent");
    await m.set({ root: "/work/a", projects: [project("/work/a", "alpha", after)], panes: after });
    assert.equal(m.badge("/work/a").textContent, "3");
    assert.equal(m.rows().length, 3);
  });
});

test("when the last waiting pane stops, the list says nothing waits", async () => {
  const one = { p1: panes.p1 };
  await withRail({ root: "/work/a", projects: [project("/work/a", "alpha", one)], panes: one }, async (m) => {
    m.badge("/work/a").click();
    await settle();
    await m.set({ root: "/work/a", projects: [project("/work/a", "alpha", one)], panes: { p1: { ...panes.p1, activity: "idle", status: "idle" } } });
    assert.equal(m.rows().length, 0);
    assert.match(m.container.find(".popover").textContent, /No agent is waiting for you now/);
  });
});

test("Escape closes the list and gives the keyboard back to the badge; choosing a row opens that pane", async () => {
  const state = { root: "/work/a", projects: [project("/work/a", "alpha", panes)], panes };
  await withRail(state, async (m) => {
    const doc = m.dom.document;
    const badge = m.badge("/work/a");
    assert.equal(badge.nodeName, "BUTTON", "a button, so Enter and Space open it");
    assert.equal(badge.attributes["aria-expanded"], "false");
    badge.click();
    await settle();
    assert.equal(m.badge("/work/a").attributes["aria-expanded"], "true");
    assert.ok(m.container.find(".popover").contains(doc.activeElement), "the keyboard went into the list");
    m.container.find(".popover").keydown("Escape", {});
    await settle();
    assert.equal(m.container.find(".popover"), null);
    assert.equal(doc.activeElement, m.badge("/work/a"), "Escape gives the keyboard back to the badge");
    m.badge("/work/a").click();
    await settle();
    const pick = m.rows().find((r) => r.find(".needs-who").textContent === "writer").find(".needs-pick");
    pick.click();
    await settle();
    assert.deepEqual(m.opened, ["p1"]);
    assert.equal(m.container.find(".popover"), null);
  });
});

test("the badge toggles its own list", async () => {
  const state = { root: "/work/a", projects: [project("/work/a", "alpha", panes)], panes };
  await withRail(state, async (m) => {
    m.badge("/work/a").click();
    await settle();
    assert.ok(m.container.find(".popover"));
    m.badge("/work/a").click();
    await settle();
    assert.equal(m.container.find(".popover"), null);
  });
});

test("the list is walked from the keyboard: arrows move between rows and wrap, Tab does not close it", async () => {
  const state = { root: "/work/a", projects: [project("/work/a", "alpha", panes)], panes };
  await withRail(state, async (m) => {
    const doc = m.dom.document;
    m.badge("/work/a").click();
    await settle();
    const picks = m.rows().map((r) => r.find(".needs-pick"));
    assert.equal(picks.length, 4);
    assert.equal(doc.activeElement, picks[0], "the first row has the keyboard");
    const key = (k) => doc.activeElement.keydown(k);
    assert.equal(key("ArrowDown").defaultPrevented, true);
    assert.equal(doc.activeElement, picks[1]);
    key("ArrowDown");
    key("ArrowDown");
    assert.equal(doc.activeElement, picks[3]);
    key("ArrowDown");
    assert.equal(doc.activeElement, picks[0], "Down wraps to the first row");
    key("ArrowUp");
    assert.equal(doc.activeElement, picks[3], "Up wraps to the last row");
    key("Home");
    assert.equal(doc.activeElement, picks[0]);
    key("End");
    assert.equal(doc.activeElement, picks[3]);
    // Tab is left to the browser: it moves from row to row and the panel stays.
    assert.equal(key("Tab").defaultPrevented, false);
    assert.ok(m.container.find(".popover"), "Tab alone does not close it");
    assert.equal(m.badge("/work/a").attributes["aria-expanded"], "true");
  });
});

test("focus moving from one row to another keeps the list; focus leaving the panel closes it without taking the keyboard back", async () => {
  const state = { root: "/work/a", projects: [project("/work/a", "alpha", panes)], panes };
  await withRail(state, async (m) => {
    const doc = m.dom.document;
    m.badge("/work/a").click();
    await settle();
    const picks = m.rows().map((r) => r.find(".needs-pick"));
    picks[0].dispatch("focusout", { relatedTarget: picks[1] });
    picks[1].dispatch("focusout", { relatedTarget: null });
    await settle();
    assert.ok(m.container.find(".popover"), "a move inside the panel, or the window losing focus, keeps it");
    const outside = m.container.find(".rail-projects") || m.container.find(".rail-toggle");
    assert.ok(outside, "there is a control outside the panel to move to");
    outside.focus();
    picks[3].dispatch("focusout", { relatedTarget: outside });
    await settle();
    assert.equal(m.container.find(".popover"), null, "Tab out of the last row closes it");
    assert.equal(doc.activeElement, outside, "the keyboard stays where the person moved it");
  });
});

test("Escape from any row closes the list and returns to the badge", async () => {
  const state = { root: "/work/a", projects: [project("/work/a", "alpha", panes)], panes };
  await withRail(state, async (m) => {
    m.badge("/work/a").click();
    await settle();
    const doc = m.dom.document;
    doc.activeElement.keydown("ArrowDown");
    doc.activeElement.keydown("Escape");
    await settle();
    assert.equal(m.container.find(".popover"), null);
    assert.equal(doc.activeElement, m.badge("/work/a"));
  });
});

test("the rail's list closes when the count reaches zero and the keyboard goes to the project's button", async () => {
  const one = { p1: panes.p1 };
  await withRail({ root: "/work/a", projects: [project("/work/a", "alpha", one)], panes: one }, async (m) => {
    const doc = m.dom.document;
    m.badge("/work/a").click();
    await settle();
    assert.ok(m.container.find(".popover").contains(doc.activeElement));
    const none = { p1: { ...panes.p1, activity: "idle", status: "idle" } };
    await m.set({ root: "/work/a", projects: [project("/work/a", "alpha", none)], panes: none });
    assert.equal(m.badge("/work/a"), undefined, "the badge is gone");
    assert.equal(m.container.find(".popover"), null, "and so is the list");
    assert.equal(doc.activeElement, m.container.find(".rail-item"), "the keyboard is on the project, not lost");
    // A later wait does not bring the old list back.
    await m.set({ root: "/work/a", projects: [project("/work/a", "alpha", one)], panes: one });
    assert.equal(m.container.find(".popover"), null);
    assert.equal(m.badge("/work/a").attributes["aria-expanded"], "false");
  });
});
