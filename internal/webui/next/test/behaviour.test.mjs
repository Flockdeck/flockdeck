// The shell's behaviour: Preact renders it into a fake DOM, a test presses keys
// and clicks, and waits for the effects to run. What a key does, where the
// keyboard goes and what is reported to the caller are all observed here and not
// only read off the reducer.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Shell } from "../assets/shell/app.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeKeys, fakeState } from "./util.mjs";

const CTRL_SHIFT = { ctrlKey: true, shiftKey: true };

async function mount(props = {}) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const watched = [];
  const view = (extra = {}) =>
    html`<${Shell}
      hello=${props.hello || { keys: fakeKeys }}
      state=${props.state || fakeState}
      window=${dom.window}
      onWatch=${(r) => watched.push(r)}
      ...${{ ...props.extra, ...extra }}
    >main<//>`;
  render(view(), container);
  await settle();
  return {
    dom,
    container,
    watched,
    update: async (extra) => {
      render(view(extra), container);
      await settle();
    },
    unmount: async () => {
      render(null, container);
      await settle();
    },
    done: restore,
  };
}

async function withShell(props, fn) {
  const m = await mount(props);
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

const palette = (m) => m.container.find(".palette");
const input = (m) => m.container.find(".palette-input");
const focused = (m) => m.dom.document.activeElement;

test("the palette key, read from the table, opens the palette and is claimed", async () => {
  await withShell({}, async (m) => {
    assert.equal(palette(m), null);
    const ev = m.container.find("main").keydown("K", CTRL_SHIFT);
    assert.equal(ev.defaultPrevented, true);
    await settle();
    assert.ok(palette(m), "the palette did not open");
    assert.equal(focused(m), input(m), "the keyboard did not go to the field");
    m.container.find("main").keydown("K", CTRL_SHIFT);
    await settle();
    assert.equal(palette(m), null, "the key does not close it again");
  });
});

test("a key bound to an action this window does not run is left alone", async () => {
  await withShell({}, async (m) => {
    // Ctrl+Shift+G is Worktrees in the table, which the palette marks as not here yet.
    const ev = m.container.find("main").keydown("G", CTRL_SHIFT);
    assert.equal(ev.defaultPrevented, false, "a key for an action not in this window was claimed");
    await settle();
    assert.equal(palette(m), null);
  });
});

test("a key the table does not bind is left alone", async () => {
  await withShell({}, async (m) => {
    assert.equal(m.container.find("main").keydown("q", {}).defaultPrevented, false);
    assert.equal(m.container.find("main").keydown("K", {}).defaultPrevented, false);
  });
});

test("the key listener is on the window while the shell is, and gone after", async () => {
  await withShell({}, async (m) => {
    assert.equal((m.dom.window.listeners.keydown || []).length, 1);
    await m.unmount();
    assert.equal((m.dom.window.listeners.keydown || []).length, 0);
  });
});

test("changing a binding in the table moves the key", async () => {
  const moved = fakeKeys.map((k) => (k.id === "palette" ? { ...k, keys: "Ctrl+Shift+J" } : k));
  await withShell({ hello: { keys: moved } }, async (m) => {
    assert.equal(m.container.find("main").keydown("K", CTRL_SHIFT).defaultPrevented, false);
    assert.equal(m.container.find("main").keydown("J", CTRL_SHIFT).defaultPrevented, true);
  });
});

test("the region keys move the keyboard through the window", async () => {
  await withShell({}, async (m) => {
    const rail = m.container.find("nav");
    const main = m.container.find("main");
    main.keydown("F6", {});
    await settle();
    assert.equal(focused(m), rail);
    rail.keydown("F6", {});
    await settle();
    assert.equal(focused(m), main);
    main.keydown("F6", { shiftKey: true });
    await settle();
    assert.equal(focused(m), rail);
  });
});

test("the projects action puts the keyboard on the rail", async () => {
  await withShell({}, async (m) => {
    m.container.find("main").keydown("O", CTRL_SHIFT);
    await settle();
    assert.equal(focused(m), m.container.find("nav"));
  });
});

test("the rail key widens and narrows the rail", async () => {
  await withShell({}, async (m) => {
    assert.ok(!m.container.find(".shell").attributes.class.includes("shell--rail-collapsed"));
    m.container.find("main").keydown("b", { ctrlKey: true });
    await settle();
    assert.ok(m.container.find(".shell").attributes.class.includes("shell--rail-collapsed"));
    m.container.find(".rail-toggle").click();
    await settle();
    assert.ok(!m.container.find(".shell").attributes.class.includes("shell--rail-collapsed"));
  });
});

// --- the watched project --------------------------------------------------

test("the watched project is reported when the window first has one", async () => {
  await withShell({}, async (m) => {
    assert.deepEqual(m.watched, ["/work/b"]);
  });
});

test("choosing a project in the rail reports it, and marks it", async () => {
  await withShell({}, async (m) => {
    m.container.findAll(".rail-item").find((b) => b.attributes["data-root"] === "/work/a").click();
    await settle();
    assert.deepEqual(m.watched, ["/work/b", "/work/a"]);
    const active = m.container.findAll(".rail-item").filter((b) => b.attributes["aria-current"] === "true");
    assert.deepEqual(active.map((b) => b.attributes["data-root"]), ["/work/a"]);
    assert.equal(m.container.find(".top-title").textContent, "alpha");
  });
});

test("state that arrives after the first render is followed", async () => {
  await withShell({ state: { root: "", projects: [] } }, async (m) => {
    assert.deepEqual(m.watched, []);
    await m.update({ state: fakeState });
    assert.deepEqual(m.watched, ["/work/b"]);
    assert.equal(m.container.findAll(".rail-item").length, 3);
  });
});

test("the desk making another project active is followed, a repeated push is not", async () => {
  await withShell({}, async (m) => {
    m.container.findAll(".rail-item").find((b) => b.attributes["data-root"] === "/work/a").click();
    await settle();
    // The same push again: the project chosen in the rail stays.
    await m.update({ state: { ...fakeState, projects: [...fakeState.projects] } });
    assert.deepEqual(m.watched, ["/work/b", "/work/a"]);
    // The desk's active project changes.
    await m.update({ state: { ...fakeState, root: "/work/c" } });
    assert.deepEqual(m.watched, ["/work/b", "/work/a", "/work/c"]);
  });
});

test("a project that is closed while watched gives way to the active one", async () => {
  await withShell({}, async (m) => {
    m.container.findAll(".rail-item").find((b) => b.attributes["data-root"] === "/work/a").click();
    await settle();
    await m.update({ state: { root: "/work/b", projects: fakeState.projects.filter((p) => p.root !== "/work/a") } });
    assert.equal(m.watched.at(-1), "/work/b");
  });
});

// --- the palette is a modal dialog ----------------------------------------

async function open(m) {
  m.container.find("main").keydown("K", CTRL_SHIFT);
  await settle();
  assert.ok(palette(m));
}

test("Escape closes the palette from the field and from a row", async () => {
  await withShell({}, async (m) => {
    await open(m);
    assert.equal(input(m).keydown("Escape", {}).defaultPrevented, true);
    await settle();
    assert.equal(palette(m), null);
    await open(m);
    m.container.find(".palette-row").keydown("Escape", {});
    await settle();
    assert.equal(palette(m), null);
  });
});

test("Tab stays in the palette", async () => {
  await withShell({}, async (m) => {
    await open(m);
    assert.equal(input(m).keydown("Tab", {}).defaultPrevented, true);
    assert.equal(input(m).keydown("Tab", { shiftKey: true }).defaultPrevented, true);
    await settle();
    assert.ok(palette(m));
    assert.equal(focused(m), input(m));
  });
});

test("the keyboard goes back to where it was when the palette closes", async () => {
  await withShell({}, async (m) => {
    const main = m.container.find("main");
    main.focus();
    await open(m);
    assert.equal(focused(m), input(m));
    input(m).keydown("Escape", {});
    await settle();
    assert.equal(focused(m), main);
  });
});

test("a region key closes the palette and moves the keyboard behind it", async () => {
  await withShell({}, async (m) => {
    await open(m);
    input(m).keydown("F6", {});
    await settle();
    assert.equal(palette(m), null);
    assert.equal(focused(m), m.container.find("nav"));
  });
});

test("the projects key closes the palette and goes to the rail", async () => {
  await withShell({}, async (m) => {
    await open(m);
    input(m).keydown("O", CTRL_SHIFT);
    await settle();
    assert.equal(palette(m), null);
    assert.equal(focused(m), m.container.find("nav"));
  });
});

test("the field names the current row, and the arrows change it", async () => {
  await withShell({}, async (m) => {
    await open(m);
    const rows = m.container.findAll(".palette-row");
    assert.ok(rows.length > 2);
    for (const r of rows) assert.ok(r.attributes.id, "a row has no id");
    assert.equal(input(m).attributes["aria-activedescendant"], rows[0].attributes.id);
    assert.equal(rows[0].attributes["aria-selected"], "true");
    input(m).keydown("ArrowDown", {});
    await settle();
    assert.equal(input(m).attributes["aria-activedescendant"], rows[1].attributes.id);
    assert.equal(m.container.findAll(".palette-row").filter((r) => r.attributes["aria-selected"] === "true").length, 1);
    input(m).keydown("ArrowUp", {});
    await settle();
    input(m).keydown("ArrowUp", {});
    await settle();
    assert.equal(input(m).attributes["aria-activedescendant"], rows.at(-1).attributes.id, "the list does not wrap");
  });
});

test("typing finds an action by name, and Enter runs it", async () => {
  await withShell({}, async (m) => {
    await open(m);
    input(m).enter("rail");
    await settle();
    const rows = m.container.findAll(".palette-row");
    assert.equal(rows[0].attributes["data-action"], "toggleRail");
    input(m).keydown("Enter", {});
    await settle();
    assert.equal(palette(m), null);
    assert.ok(m.container.find(".shell").attributes.class.includes("shell--rail-collapsed"));
  });
});

test("Enter on an action not in this window does nothing, and the palette stays", async () => {
  await withShell({}, async (m) => {
    await open(m);
    input(m).enter("worktrees");
    await settle();
    const row = m.container.find(".palette-row");
    assert.equal(row.attributes["data-action"], "worktrees");
    assert.equal(row.attributes["aria-disabled"], "true");
    input(m).keydown("Enter", {});
    row.click();
    await settle();
    assert.ok(palette(m));
  });
});

test("clicking outside the dialog closes it", async () => {
  await withShell({}, async (m) => {
    await open(m);
    m.container.find(".palette-scrim").dispatch("mousedown");
    await settle();
    assert.equal(palette(m), null);
  });
});

// --- the table's own faults -----------------------------------------------

test("a binding that cannot be read is said, not silently dead", async () => {
  const keys = [...fakeKeys, { id: "remapped", keys: "Hyper+X", label: "Remapped", section: "The window" }];
  await withShell({ hello: { keys } }, async (m) => {
    assert.match(m.container.find(".notice").textContent, /could not be read.*remapped/);
  });
  await withShell({}, async (m) => {
    assert.equal(m.container.find(".notice"), null);
    assert.ok(m.container.find(".notices"), "the notice area has to exist before there is a notice to read out");
  });
});
