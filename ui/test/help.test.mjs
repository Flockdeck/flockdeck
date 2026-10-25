// The help of the new interface: the pages as nodes (checked again on this side),
// the search, the key table of the window, and the dialog an F1 opens, drawn on a
// fake DOM. The real pages are read from NEXT_HELP_JSON when the Go test that
// runs these has written them.
import { test } from "node:test";
import { focusables } from "../src/shell/focus.js";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { render } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Shell, IMPLEMENTED, reduce, initial } from "../src/shell/app.js";
import { Help, Tree, NOT_YET, PALETTE_ONLY } from "../src/shell/Help.js";
import { TAGS, cleanNodes, helpUrl, keySections, keysEntry, loadHelp, pagesFrom, safeLink, searchPages } from "../src/lib/help.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeKeys, fakeState, realKeys } from "./util.mjs";

const realPages = () => (process.env.NEXT_HELP_JSON ? JSON.parse(readFileSync(process.env.NEXT_HELP_JSON, "utf8")) : null);
const skip = realPages() ? false : "NEXT_HELP_JSON is not set";

const p = (c) => ({ t: "p", c: [{ x: c }] });
const page = (slug, title, text, tree) => ({ slug, title, summary: "About " + title, text, tree: tree || [p(text)] });
const PAGES = [
  page("panes", "Panes", "A pane is one agent or shell."),
  page("worktrees", "Worktrees", "Each agent can work in its own worktree.", [
    { t: "h1", c: [{ x: "Worktrees" }] },
    { t: "p", c: [{ x: "See " }, { t: "a", h: "#panes", c: [{ x: "Panes" }] }, { x: " and " }, { t: "a", h: "https://example.com/x", c: [{ x: "the web" }] }, { x: "." }] },
  ]),
];

// ---------------------------------------------------------------- the nodes

test("a page keeps its tags, its text and its links", () => {
  const got = cleanNodes(PAGES[1].tree);
  assert.deepEqual(got, PAGES[1].tree);
});

test("a tag off the list is not drawn, and its words stay", () => {
  const got = cleanNodes([{ t: "script", c: [{ x: "alert(1)" }] }, { t: "p", c: [{ t: "img", c: [{ x: "alt" }] }] }]);
  assert.deepEqual(got, [{ x: "alert(1)" }, { t: "p", c: [{ x: "alt" }] }]);
});

test("a link that cannot be followed is its words", () => {
  for (const h of ["javascript:alert(1)", "JaVaScRiPt:x", "data:text/html,x", "/etc/passwd", "//evil.example/x", "#", "#a b", "", undefined, 5, "file:///c:/x"]) {
    assert.deepEqual(cleanNodes([{ t: "a", h, c: [{ x: "go" }] }]), [{ x: "go" }], String(h));
  }
  for (const h of ["#panes", "http://a.example/", "https://a.example/b?c=d"]) {
    assert.equal(cleanNodes([{ t: "a", h, c: [{ x: "go" }] }])[0].h, h);
  }
});

test("a node of the wrong shape is left out and nothing throws", () => {
  const got = cleanNodes([null, 5, "s", [], { t: 5, c: "x" }, { x: 5 }, { x: "" }, { t: "p", c: null }, { t: "p" }]);
  assert.deepEqual(got, [{ t: "p", c: [] }, { t: "p", c: [] }]);
  assert.deepEqual(cleanNodes("not a list"), []);
});

test("a page nested without end stops at a depth", () => {
  let n = [{ x: "deep" }];
  for (let i = 0; i < 5000; i++) n = [{ t: "blockquote", c: n }];
  let depth = 0;
  for (let g = cleanNodes(n); g.length && g[0].t === "blockquote"; g = g[0].c) depth++;
  assert.ok(depth > 0 && depth <= 42, "depth " + depth);
});

test("the pages of an answer are checked, and a bad answer says so", () => {
  assert.equal(pagesFrom(null).error.includes("older version"), true);
  assert.equal(pagesFrom({ pages: "x" }).error.includes("older version"), true);
  assert.equal(pagesFrom({ pages: [] }).error, "There are no help pages.");
  const got = pagesFrom({ pages: [PAGES[0], PAGES[0], { slug: "", title: "x", tree: [p("a")] }, { slug: "b", title: "B", tree: [] }, null, { slug: "c", title: "C", tree: [p("c")], text: 5, summary: 7 }] });
  assert.deepEqual(got.pages.map((x) => x.slug), ["panes", "c"]);
  assert.equal(got.pages[1].text, "");
  assert.equal(got.pages[1].summary, "");
});

test("the address is the page's own directory, so it works under the relay's prefix", () => {
  assert.equal(helpUrl(new URL("http://127.0.0.1:5/")), "http://127.0.0.1:5/assets/help.json");
  assert.equal(helpUrl(new URL("https://relay.example/d/h1/index.html")), "https://relay.example/d/h1/assets/help.json");
  assert.equal(helpUrl(new URL("https://relay.example/d/h1/")), "https://relay.example/d/h1/assets/help.json");
});

test("loading says what went wrong", async () => {
  const loc = new URL("http://127.0.0.1:5/");
  const seen = [];
  const ok = await loadHelp(async (u, o) => (seen.push([u, o]), { ok: true, json: async () => ({ pages: PAGES }) }), loc);
  assert.equal(ok.pages.length, 2);
  assert.equal(seen[0][0], "http://127.0.0.1:5/assets/help.json");
  assert.equal(seen[0][1].cache, "no-store");
  assert.match((await loadHelp(async () => ({ ok: false, status: 403 }), loc)).error, /HTTP 403/);
  assert.match((await loadHelp(async () => { throw new Error("offline"); }, loc)).error, /offline/);
  assert.match((await loadHelp(async () => ({ ok: true, json: async () => { throw new Error("bad json"); } }), loc)).error, /bad json/);
});

// ---------------------------------------------------------------- the search

test("a search needs every word, in the title or the text, and keeps the order", () => {
  assert.deepEqual(searchPages(PAGES, "").map((h) => h.page.slug), ["panes", "worktrees"]);
  assert.deepEqual(searchPages(PAGES, "WORKTREE agent").map((h) => h.page.slug), ["worktrees"]);
  assert.deepEqual(searchPages(PAGES, "agent").map((h) => h.page.slug), ["panes", "worktrees"]);
  assert.deepEqual(searchPages(PAGES, "agent zzz"), []);
  assert.match(searchPages(PAGES, "shell")[0].snippet, /agent or shell/);
});

test("the key table is grouped in the order it names its sections", () => {
  const s = keySections(fakeKeys);
  assert.deepEqual(s.map((x) => x.name), ["Finding your way", "The window", "Git", "Agents"]);
  assert.equal(s.reduce((n, x) => n + x.rows.length, 0), fakeKeys.length);
  assert.deepEqual(keySections(null), []);
  assert.equal(keySections([{ id: "x" }])[0].name, "Other");
});

test("the keys entry is found by a key's name and by its label", () => {
  const e = keysEntry(fakeKeys);
  assert.equal(searchPages([e], "ctrl+shift+g").length, 1);
  assert.equal(searchPages([e], "worktrees").length, 1);
  assert.equal(searchPages([e], "shortcuts").length, 1);
});

// ------------------------------------------------------------- what is drawn

test("a page is drawn as elements, and text that looks like markup stays text", () => {
  const nasty = "<img src=x onerror=alert(1)><script>boom()</script>";
  const out = renderToString(html`<${Tree} nodes=${[p(nasty), { t: "a", h: "https://a.example/", c: [{ x: "out" }] }, { t: "a", h: "#panes", c: [{ x: "in" }] }]} />`);
  assert.ok(!out.includes("<img"), out);
  assert.ok(!out.includes("<script"), out);
  assert.match(out, /&lt;img src=x onerror=alert\(1\)>/);
  assert.match(out, /<a href="https:\/\/a.example\/" target="_blank" rel="noopener noreferrer">out<\/a>/);
  assert.match(out, /<a href="#panes">in<\/a>/);
});

test("every real page passes the checks here unchanged, so the two lists agree on real content", { skip }, () => {
  const data = realPages();
  const got = pagesFrom(data);
  assert.equal(got.error, undefined);
  assert.equal(got.pages.length, data.pages.length);
  for (const [i, pg] of got.pages.entries()) assert.deepEqual(pg.tree, data.pages[i].tree, pg.slug);
  for (const pg of got.pages) {
    const out = renderToString(html`<${Tree} nodes=${pg.tree} />`);
    assert.ok(!/<script|<img|\son[a-z]+=|javascript:/i.test(out), pg.slug);
    for (const left of ["{{keys", "[[key:", "[[action:"]) assert.ok(!out.includes(left), pg.slug + " has " + left + " left");
    assert.ok(out.includes("<h1>"), pg.slug);
  }
});

test("the real pages use only tags on the list", { skip }, () => {
  const seen = new Set();
  const walk = (ns) => ns.forEach((n) => { if (n.t) seen.add(n.t); walk(n.c || []); });
  realPages().pages.forEach((pg) => walk(pg.tree));
  for (const t of seen) assert.ok(TAGS.includes(t), t);
});

test("safeLink", () => {
  assert.equal(safeLink("#panes"), true);
  assert.equal(safeLink("mailto:a@b.c"), false);
});

// ------------------------------------------------------------ the dialog

const noop = () => {};

async function mount(props = {}) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  let loads = 0;
  const helpLoad = props.helpLoad || (() => { loads++; return Promise.resolve({ pages: PAGES }); });
  const keys = props.keys || fakeKeys.concat([{ id: "help", keys: "F1", label: "Help", section: "Finding your way", page: "panes" }]);
  const draw = () => render(html`<${Shell} hello=${{ keys }} state=${fakeState} window=${dom.window} narrow=${false} helpLoad=${helpLoad} actions=${props.actions}>main<//>`, container);
  draw();
  await settle();
  return {
    dom, container, keys,
    loads: () => loads,
    dialog: () => container.find(".help"),
    search: () => container.find(".help-search"),
    content: () => container.find(".help-content"),
    items: () => container.findAll(".help-item"),
    press: async (key, init = {}) => {
      const ev = dom.window.keydown(key, init);
      await settle();
      return ev;
    },
    done: () => { render(null, container); restore(); },
  };
}

// The binding is the table's: F1 is read from the key table the window was sent.
const F1 = { ctrlKey: false };

test("help is an action this window runs", () => {
  assert.ok(IMPLEMENTED.includes("help"));
  const ids = realKeys() && new Set(realKeys().map((k) => k.id));
  if (ids) assert.ok(ids.has("help"));
});

test("the reducer opens and closes the help with its action, and holds other actions while it is open", () => {
  let s = initial({ state: fakeState });
  s = reduce(s, { type: "action", id: "palette" });
  assert.equal(s.paletteOpen, true);
  s = reduce(s, { type: "action", id: "help" });
  assert.equal(s.helpOpen, true);
  assert.equal(s.paletteOpen, false, "opening the help closes the palette");
  const held = reduce(s, { type: "action", id: "palette" });
  assert.equal(held, s, "the palette does not open under the help");
  assert.equal(reduce(s, { type: "action", id: "toggleRail" }), s);
  assert.equal(reduce(s, { type: "action", id: "help" }).helpOpen, false);
  assert.equal(reduce(s, { type: "closeHelp" }).helpOpen, false);
  assert.equal(reduce(initial({}), { type: "closeHelp" }).helpOpen, false);
});

test("the help key opens the dialog on the keys entry, with the search box holding the keyboard", async () => {
  const m = await mount();
  try {
    assert.equal(m.dialog(), null);
    const ev = await m.press("F1");
    assert.equal(ev.defaultPrevented, true);
    const d = m.dialog();
    assert.ok(d);
    assert.equal(d.getAttribute("role"), "dialog");
    assert.equal(d.getAttribute("aria-modal"), "true");
    assert.equal(m.loads(), 1);
    assert.equal(m.dom.document.activeElement, m.search());
    assert.deepEqual(m.items().map((i) => i.getAttribute("data-page")), ["keys-here", "panes", "worktrees"]);
    assert.equal(m.items()[0].getAttribute("aria-current"), "page");
    assert.equal(m.content().getAttribute("data-page"), "keys-here");
  } finally { m.done(); }
});

test("the keys entry lists every action, its key as the table has it, and what this window runs", async () => {
  const m = await mount();
  try {
    await m.press("F1");
    const rows = m.container.findAll("tr").filter((r) => r.getAttribute("data-action"));
    assert.equal(rows.length, m.keys.length);
    const row = (id) => rows.find((r) => r.getAttribute("data-action") === id);
    assert.match(row("toggleRail").textContent, /Ctrl\+B/);
    assert.match(row("toggleRail").textContent, /yes/);
    assert.ok(row("toggleRail").textContent.includes(NOT_YET) === false);
    assert.match(row("worktrees").textContent, new RegExp(NOT_YET));
    assert.match(row("quit").textContent, new RegExp(PALETTE_ONLY));
    // An action that names a page the help has gets a way to it.
    assert.ok(row("help").findAll("a").length === 1);
    assert.equal(row("worktrees").findAll("a").length, 0, "worktrees names a page this fixture lacks");
    for (const t of IMPLEMENTED) {
      const r = row(t);
      if (r) assert.match(r.textContent, /yes/, t);
    }
  } finally { m.done(); }
});

test("a remapped key is shown as remapped, not as the default", async () => {
  const keys = fakeKeys.map((k) => (k.id === "worktrees" ? { ...k, keys: "Ctrl+Alt+W" } : k)).concat([{ id: "help", keys: "F1", label: "Help", section: "Finding your way" }]);
  const m = await mount({ keys });
  try {
    await m.press("F1");
    const r = m.container.findAll("tr").find((x) => x.getAttribute("data-action") === "worktrees");
    assert.match(r.textContent, /Ctrl\+Alt\+W/);
    assert.ok(!r.textContent.includes("Ctrl+Shift+G"));
  } finally { m.done(); }
});

test("typing narrows the contents, and choosing a page shows it", async () => {
  const m = await mount();
  try {
    await m.press("F1");
    m.search().enter("its own worktree");
    await settle();
    assert.deepEqual(m.items().map((i) => i.getAttribute("data-page")), ["worktrees"]);
    assert.equal(m.content().getAttribute("data-page"), "worktrees", "the page that matched is shown, not the one the search hid");
    m.search().enter("zzzz");
    await settle();
    assert.equal(m.items().length, 0);
    assert.match(m.dialog().textContent, /Nothing here matches/);
    m.search().enter("");
    await settle();
    m.items().find((i) => i.getAttribute("data-page") === "panes").click();
    await settle();
    assert.equal(m.content().getAttribute("data-page"), "panes");
    assert.match(m.content().textContent, /A pane is one agent or shell/);
    assert.equal(m.search().value, "", "choosing a page clears a search left over");
  } finally { m.done(); }
});

test("the arrows walk the contents from the search box", async () => {
  const m = await mount();
  try {
    await m.press("F1");
    await settle();
    m.search().keydown("ArrowDown");
    await settle();
    assert.equal(m.content().getAttribute("data-page"), "panes");
    m.search().keydown("ArrowDown");
    await settle();
    assert.equal(m.content().getAttribute("data-page"), "worktrees");
    m.search().keydown("ArrowUp");
    await settle();
    assert.equal(m.content().getAttribute("data-page"), "panes");
  } finally { m.done(); }
});

test("a link to another page opens it here, and a link out opens elsewhere", async () => {
  const m = await mount();
  try {
    await m.press("F1");
    m.items().find((i) => i.getAttribute("data-page") === "worktrees").click();
    await settle();
    const links = m.content().findAll("a");
    assert.equal(links.length, 2);
    const ev = links[0].click();
    await settle();
    assert.equal(ev.defaultPrevented, true);
    assert.equal(m.content().getAttribute("data-page"), "panes");
    m.items().find((i) => i.getAttribute("data-page") === "worktrees").click();
    await settle();
    const out = m.content().findAll("a")[1];
    assert.equal(out.getAttribute("target"), "_blank");
    assert.equal(out.getAttribute("rel"), "noopener noreferrer");
  } finally { m.done(); }
});

test("Escape and the key close it, and the keyboard goes back where it was", async () => {
  const m = await mount();
  try {
    const main = m.container.find("main");
    main.focus();
    assert.equal(m.dom.document.activeElement, main);
    await m.press("F1");
    assert.equal(m.dom.document.activeElement, m.search());
    m.search().keydown("Escape");
    await settle();
    assert.equal(m.dialog(), null);
    assert.equal(m.dom.document.activeElement, main);

    await m.press("F1");
    assert.ok(m.dialog());
    await m.press("F1");
    assert.equal(m.dialog(), null, "the key that opens it closes it");
    assert.equal(m.dom.document.activeElement, main);

    await m.press("F1");
    m.container.find(".help-scrim").dispatch("mousedown");
    await settle();
    assert.equal(m.dialog(), null, "a press outside closes it");
    await m.press("F1");
    m.dialog().dispatch("mousedown");
    await settle();
    assert.ok(m.dialog(), "a press inside does not");
  } finally { m.done(); }
});

test("while it is open the other keys do nothing underneath it", async () => {
  const m = await mount();
  try {
    await m.press("F1");
    await m.press("K", { ctrlKey: true, shiftKey: true });
    assert.equal(m.container.find(".palette"), null);
    await m.press("B", { ctrlKey: true });
    assert.ok(!m.container.find(".shell").getAttribute("class").includes("rail-collapsed"));
  } finally { m.done(); }
});

test("Tab stays inside the dialog, with no hidden focusable sentinels", async () => {
  const m = await mount();
  try {
    await m.press("F1");
    assert.equal(m.container.findAll(".help-sentinel").length, 0, "an aria-hidden focusable is an ARIA error");
    const dialog = m.container.find(".help");
    const list = focusables(dialog);
    assert.ok(list.length > 2);
    list[list.length - 1].focus();
    dialog.keydown("Tab");
    assert.equal(m.dom.document.activeElement, list[0], "past the last control it comes back to the first");
    dialog.keydown("Tab", { shiftKey: true });
    assert.equal(m.dom.document.activeElement, list[list.length - 1], "before the first it goes to the last");
  } finally { m.done(); }
});

test("a failed load says so and can be tried again", async () => {
  let n = 0;
  const helpLoad = () => (++n === 1 ? Promise.resolve({ error: "The help pages could not be loaded (HTTP 500)." }) : Promise.resolve({ pages: PAGES }));
  const m = await mount({ helpLoad });
  try {
    await m.press("F1");
    assert.match(m.dialog().textContent, /HTTP 500/);
    assert.equal(m.items().length, 0);
    const again = m.container.find(".help-retry");
    again.click();
    await settle();
    assert.equal(n, 2);
    assert.equal(m.container.find(".help-retry"), null);
    assert.equal(m.items().length, 3);
  } finally { m.done(); }
});

test("a page of text that looks like markup draws no element from it", async () => {
  const nasty = "<img src=x onerror=alert(1)>";
  const helpLoad = () => Promise.resolve({ pages: pagesFrom({ pages: [page("evil", "Evil", nasty, [p(nasty)])] }).pages });
  const m = await mount({ helpLoad });
  try {
    await m.press("F1");
    m.items().find((i) => i.getAttribute("data-page") === "evil").click();
    await settle();
    assert.equal(m.content().find("img"), null);
    assert.ok(m.content().textContent.includes(nasty));
  } finally { m.done(); }
});

test("the dialog can be given a page to open on", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const c = dom.document.createElement("div");
    dom.body.appendChild(c);
    render(html`<${Help} keys=${fakeKeys} implemented=${[]} load=${() => Promise.resolve({ pages: PAGES })} slug="worktrees" onClose=${noop} />`, c);
    await settle();
    assert.equal(c.find(".help-content").getAttribute("data-page"), "worktrees");
    render(null, c);
  } finally { restore(); }
});

test("the real key table lists in the keys entry", { skip: realKeys() ? false : "NEXT_KEYS_JSON is not set" }, async () => {
  const m = await mount({ keys: realKeys() });
  try {
    await m.press("F1");
    const rows = m.container.findAll("tr").filter((r) => r.getAttribute("data-action"));
    assert.equal(rows.length, realKeys().length);
    const help = rows.find((r) => r.getAttribute("data-action") === "help");
    assert.match(help.textContent, /F1/);
    assert.match(help.textContent, /yes/);
  } finally { m.done(); }
});

// A hostile answer goes through the loader and the dialog, not only through
// cleanNodes: the step that cleans a page has to be the one the dialog is behind.
const hostile = {
  pages: [{
    slug: "evil", title: "Evil", summary: "s", text: "evil words here",
    tree: [
      { t: "script", c: [{ x: "boom()" }] },
      { t: "p", c: [
        { t: "img", c: [{ x: "alt words" }] },
        { t: "a", h: "javascript:alert(1)", c: [{ x: "bad link" }] },
        { t: "a", h: "https://example.com/ok", c: [{ x: "good link" }] },
        { t: "marquee", c: [{ x: "odd tag" }] },
      ] },
    ],
  }],
};
const hostileFetch = async () => ({ ok: true, json: async () => JSON.parse(JSON.stringify(hostile)) });

test("a hostile page that goes through the loader comes out clean", async () => {
  const got = await loadHelp(hostileFetch, new URL("http://127.0.0.1:5/"));
  const tags = [];
  const hrefs = [];
  const walk = (ns) => ns.forEach((n) => { if (n.t) tags.push(n.t); if (n.h) hrefs.push(n.h); walk(n.c || []); });
  walk(got.pages[0].tree);
  assert.deepEqual(tags.sort(), ["a", "p"]);
  assert.deepEqual(hrefs, ["https://example.com/ok"]);
  assert.match(JSON.stringify(got.pages[0].tree), /bad link/, "the words of a dropped link stay");
});

test("a hostile page opened in the dialog puts no script, image or javascript: link in the DOM", async () => {
  const helpLoad = () => loadHelp(hostileFetch, new URL("http://127.0.0.1:5/"));
  const m = await mount({ helpLoad });
  try {
    await m.press("F1");
    m.items().find((i) => i.getAttribute("data-page") === "evil").click();
    await settle();
    const c = m.content();
    assert.match(c.textContent, /bad link/);
    for (const tag of ["script", "img", "marquee"]) assert.equal(c.find(tag), null, tag);
    const hrefs = c.findAll("a").map((a) => a.getAttribute("href"));
    assert.deepEqual(hrefs, ["https://example.com/ok"]);
  } finally { m.done(); }
});
