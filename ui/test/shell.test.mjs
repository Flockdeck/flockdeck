import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Shell, initial, reduce, regionsOf, IMPLEMENTED, NARROW_QUERY } from "../src/shell/app.js";
import { Rail } from "../src/shell/rail.js";
import { expand, find, text, fakeKeys, fakeState } from "./util.mjs";
import { initialOf } from "../src/shell/rail.js";

const hello = { keys: fakeKeys };

const draw = (props) => renderToString(html`<${Shell} hello=${hello} state=${fakeState} ...${props}>main content<//>`);

test("the shell renders its regions from a fake hello and state", () => {
  const out = draw({ narrow: false });
  assert.match(out, /<nav class="rail"/);
  assert.match(out, /<header class="top"/);
  assert.match(out, /<main class="main"[^>]*>main content</);
  assert.match(out, /<aside class="drawer"[^>]*hidden/);
  assert.match(out, /role="status"/);
  assert.match(out, />alpha</);
  assert.match(out, />beta</);
  // The watched project, from the state's root, is current and named on the top strip.
  assert.match(out, /<span class="top-title">beta</);
  assert.match(out, /aria-current="true"[^>]*data-root="\/work\/b"/);
});

test("wide: the rail is a column, and the main area is not a list", () => {
  const out = draw({ narrow: false });
  assert.ok(!out.includes("shell--narrow"));
  assert.ok(!out.includes("rail--bar"));
  assert.ok(!out.includes("main--list"));
  assert.match(out, /class="rail-btn rail-toggle"/);
});

test("narrow: the rail collapses to a bar and the main area becomes a list", () => {
  const out = draw({ narrow: true });
  assert.match(out, /shell shell--narrow/);
  assert.match(out, /rail rail--bar/);
  assert.match(out, /main main--list/);
  assert.ok(!out.includes("rail-toggle"), "a bar has no widen button");
});

test("the width comes from the phone query when it is not forced", () => {
  const asked = [];
  const fake = (narrow) => (q) => {
    asked.push(q);
    return { matches: narrow, addEventListener() {}, removeEventListener() {} };
  };
  assert.match(draw({ matchMedia: fake(true) }), /shell--narrow/);
  assert.ok(!draw({ matchMedia: fake(false) }).includes("shell--narrow"));
  assert.ok(asked.every((q) => q === NARROW_QUERY));
});

test("a project name that holds markup is drawn as text", () => {
  const out = draw({ narrow: false });
  assert.ok(!out.includes("<img"), out);
  assert.match(out, /&lt;img src=x onerror=alert\(1\)>/);
});

test("waiting and working counts are on the rail", () => {
  const out = draw({ narrow: false });
  assert.match(out, /badge badge--waiting" aria-hidden="true">2</);
  assert.match(out, /badge badge--working" aria-hidden="true">3</);
  // The button carries the name and the counts, since the name can be hidden.
  assert.match(out, /aria-label="alpha, 2 waiting"/);
  assert.match(out, /aria-label="beta, 3 working"/);
});

test("an empty state says no project is open", () => {
  const out = renderToString(html`<${Shell} hello=${hello} state=${{ projects: [] }} narrow=${false} />`);
  assert.match(out, /No projects open/);
});

test("a notice is shown in the notice area", () => {
  assert.match(draw({ narrow: false, notice: "Reconnecting" }), /<p class="notice">Reconnecting</);
});

test("the drawer region shows only when something is put in it", () => {
  const out = renderToString(html`<${Shell} hello=${hello} state=${fakeState} narrow=${false} drawer=${html`<p>one agent</p>`} />`);
  assert.ok(!/<aside class="drawer"[^>]*hidden/.test(out), out);
  assert.match(out, /one agent/);
});

// The rail switches the project that is watched.
test("choosing a project in the rail reports its root", () => {
  const chosen = [];
  const tree = expand(Rail({ projects: fakeState.projects, active: "/work/b", onSelect: (r) => chosen.push(r) }));
  const buttons = find(tree, (n) => n.type === "button" && n.props["data-root"]);
  assert.equal(buttons.length, 3);
  buttons.find((b) => b.props["data-root"] === "/work/a").props.onClick();
  assert.deepEqual(chosen, ["/work/a"]);
  assert.equal(buttons.find((b) => b.props["data-root"] === "/work/b").props["aria-current"], "true");
  assert.equal(buttons.find((b) => b.props["data-root"] === "/work/a").props["aria-current"], undefined);
});

test("the shell starts on the project the state names, or the first", () => {
  assert.equal(initial({ state: fakeState }).selected, "/work/b");
  assert.equal(initial({ state: { projects: [{ root: "/x", name: "x" }] } }).selected, "/x");
  assert.equal(initial({}).selected, "");
});

test("selecting a project changes the watched one and nothing else", () => {
  const s0 = initial({ state: fakeState });
  const s1 = reduce(s0, { type: "select", root: "/work/a" });
  assert.equal(s1.selected, "/work/a");
  assert.deepEqual({ ...s1, selected: s0.selected }, s0);
  assert.equal(reduce(s1, { type: "select", root: "/work/a" }), s1);
});

test("the palette key opens it and again closes it", () => {
  const s0 = initial({});
  const s1 = reduce(s0, { type: "action", id: "palette" });
  assert.equal(s1.paletteOpen, true);
  assert.equal(reduce(s1, { type: "action", id: "palette" }).paletteOpen, false);
  assert.equal(reduce(s1, { type: "closePalette" }).paletteOpen, false);
});

test("the rail key widens and narrows the rail", () => {
  const s1 = reduce(initial({}), { type: "action", id: "toggleRail" });
  assert.equal(s1.railCollapsed, true);
  assert.equal(reduce(s1, { type: "action", id: "toggleRail" }).railCollapsed, false);
});

test("the projects action puts the keyboard on the rail", () => {
  const s1 = reduce({ ...initial({}), paletteOpen: true }, { type: "action", id: "projects" });
  assert.equal(s1.focus, "rail");
  assert.equal(s1.paletteOpen, false);
  assert.equal(s1.focusSeq, 1);
});

test("the region keys go round the parts of the window, the drawer only when it is open", () => {
  let s = initial({});
  const step = (id, ctx) => (s = reduce(s, { type: "action", id }, ctx));
  assert.equal(step("nextRegion", {}).focus, "rail");
  assert.equal(step("nextRegion", {}).focus, "main");
  assert.equal(step("nextRegion", {}).focus, "rail");
  assert.equal(step("nextRegion", { drawerOpen: true }).focus, "main");
  assert.equal(step("nextRegion", { drawerOpen: true }).focus, "drawer");
  assert.equal(step("nextRegion", { drawerOpen: true }).focus, "rail");
  assert.equal(step("prevRegion", { drawerOpen: true }).focus, "drawer");
  assert.deepEqual(regionsOf({}), ["rail", "main"]);
});

test("the notice area is there when it is empty, so a notice is read out when it comes", () => {
  assert.match(draw({ narrow: false }), /<div class="notices" role="status" aria-live="polite"><\/div>/);
});

test("a collapsed rail shows each project's initial and the button still has its name", () => {
  assert.equal(initialOf("alpha"), "A");
  assert.equal(initialOf("  éclair"), "É");
  assert.equal(initialOf(""), "?");
  const out = draw({ narrow: false });
  assert.match(out, /rail-initial" aria-hidden="true">A</);
  assert.match(out, /aria-label="alpha, 2 waiting"/);
});

test("a state push naming another active project moves the selection; a repeat does not", () => {
  const s0 = initial({ state: fakeState });
  const chosen = reduce(s0, { type: "select", root: "/work/a" });
  const same = reduce(chosen, { type: "state", root: "/work/b", projects: fakeState.projects });
  assert.equal(same, chosen);
  const moved = reduce(chosen, { type: "state", root: "/work/c", projects: fakeState.projects });
  assert.equal(moved.selected, "/work/c");
  assert.equal(moved.seenRoot, "/work/c");
});

test("state arriving after the first render sets the selection", () => {
  const s0 = initial({ state: { root: "", projects: [] } });
  assert.equal(s0.selected, "");
  assert.equal(reduce(s0, { type: "state", root: "/work/b", projects: fakeState.projects }).selected, "/work/b");
  // No active root: the first project.
  assert.equal(reduce(s0, { type: "state", root: "", projects: fakeState.projects }).selected, "/work/a");
});

test("a watched project that is closed gives way", () => {
  const s0 = reduce(initial({ state: fakeState }), { type: "select", root: "/work/a" });
  const gone = reduce(s0, { type: "state", root: "/work/b", projects: fakeState.projects.filter((p) => p.root !== "/work/a") });
  assert.equal(gone.selected, "/work/b");
});

test("the region keys close the palette", () => {
  const open = { ...initial({}), paletteOpen: true };
  assert.equal(reduce(open, { type: "action", id: "nextRegion" }).paletteOpen, false);
  assert.equal(reduce(open, { type: "action", id: "prevRegion" }).paletteOpen, false);
});

test("an action this window does not run changes nothing", () => {
  const s0 = initial({});
  assert.equal(reduce(s0, { type: "action", id: "worktrees" }), s0);
  assert.ok(!IMPLEMENTED.includes("worktrees"));
});

test("the foot buttons of the rail are named by their words, so a glyph-only rail is not unlabelled", () => {
  const out = renderToString(html`<${Rail} projects=${[]} onSettings=${() => {}} onProjects=${() => {}} onToggle=${() => {}} collapsed=${true} />`);
  for (const w of ["Manage projects", "Settings", "Widen"]) {
    assert.match(out, new RegExp(`class="rail-btn [^"]*"[^>]*aria-label="${w}" title="${w}"`));
  }
  assert.match(out, /rail-glyph" aria-hidden="true"/);
});
