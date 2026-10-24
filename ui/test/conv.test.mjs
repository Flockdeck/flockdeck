// The conversation renderer: Markdown to nodes (what it understands, what it will
// not make, what it does with hostile and huge input), the rows, the arithmetic of
// the list, and the list in a page (paging, keys, tool rows).
// Elements are compared with assert.ok(a === b), never equal or deepEqual: a
// failing deepEqual on the fake DOM prints the whole tree.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Markdown, renderMarkdown, MAX_INLINE, MAX_HIGHLIGHT, MAX_DEPTH, COLLAPSE_LINES, COLLAPSE_SHOW_LINES, copyText } from "../src/drawer/conv/markdown.js";
import { Row, NO_SUMMARY_WORDS, MAX_REPLY } from "../src/drawer/conv/rows.js";
import { Log, OLDER_LIMIT_WORDS } from "../src/drawer/conv/Log.js";
import {
  startIndex, earlierStart, atBottom, anchored, moveTo, newBelow, START_ROWS, PAGE_ROWS, MAX_ROWS,
} from "../src/drawer/conv/scroll.js";
import { createDom, install, settle } from "./dom.mjs";

const md = (text) => renderToString(html`<${Markdown} text=${text} />`);

/** tags are every element type under nodes, a function component by its name. */
function tags(nodes, out = new Set()) {
  for (const n of [].concat(nodes)) {
    if (n == null || typeof n !== "object") continue;
    if (Array.isArray(n)) { tags(n, out); continue; }
    if (typeof n.type === "string") out.add(n.type);
    else if (typeof n.type === "function") out.add("<" + n.type.name + ">");
    const kids = n.props && n.props.children;
    if (kids != null) tags(kids, out);
  }
  return out;
}

const ALLOWED = new Set([
  "p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "blockquote", "hr", "table", "thead", "tbody", "tr", "th", "td",
  "pre", "code", "strong", "em", "del", "a", "br", "span", "div", "button", "<CodeBlock>",
]);

// --- Markdown: what it understands -------------------------------------------

test("headings, paragraphs, lists, quotes, rules and tables become their elements", () => {
  const out = md([
    "# One", "", "Some **bold**, _it_, ~~gone~~ and `code`.", "",
    "- a", "- b", "  - nested", "", "1. first", "2. second", "",
    "- [x] done", "- [ ] todo", "", "> quoted", "", "---", "",
    "| h1 | h2 |", "| :-- | --: |", "| a | b |",
  ].join("\n"));
  for (const want of ["<h1", "<strong>bold</strong>", "<em>it</em>", "<del>gone</del>", "<code>code</code>", "<ul", "<ol", "<blockquote", "<hr", "<table", "<th", "md-align-left", "md-align-right", "md-task"]) {
    assert.ok(out.includes(want), "no " + want + " in " + out);
  }
  assert.ok(out.includes("nested"), "the nested item is gone");
  assert.ok(out.includes("☑") && out.includes("☐"), "task boxes");
});

test("a link becomes a link only for http and https, and opens safely", () => {
  const out = md("[ok](https://example.test/a) [bad](javascript:alert(1)) [data](data:text/html,x) see https://example.test/y. and <https://auto.test/z>");
  assert.ok(out.includes('href="https://example.test/a"'));
  assert.ok(out.includes('rel="noopener noreferrer"') && out.includes('target="_blank"'));
  assert.ok(!out.includes('href="javascript'), "a javascript: link was made");
  assert.ok(!out.includes('href="data:'), "a data: link was made");
  assert.ok(out.includes("[bad](javascript:alert(1))"), "the unlinked text was lost");
  assert.ok(out.includes('href="https://example.test/y"'), "the full stop was swept into the link");
  assert.ok(out.includes('href="https://auto.test/z"'));
});

test("a fenced block names its language, highlights, and keeps its text as text", () => {
  const out = md("```go\nfunc main() { return \"<b>x</b>\" } // c\n```");
  assert.ok(out.includes("md-code-lang") && out.includes(">go<"));
  assert.ok(out.includes("tok-kw") && out.includes("tok-str") && out.includes("tok-com"));
  assert.ok(out.includes("&lt;b>x&lt;/b>") || out.includes("&lt;b&gt;x&lt;/b&gt;"), "markup in code was not escaped");
  assert.ok(!out.includes("<b>x"), "markup in code was drawn");
  const plain = md("```unknownlang\n<i>x</i>\n```");
  assert.ok(!plain.includes("tok-") && !plain.includes("<i>"));
});

test("a long block folds to its first lines, with Show all and Copy", () => {
  const lines = Array.from({ length: COLLAPSE_LINES + 8 }, (_, i) => "line" + i);
  const out = md("```\n" + lines.join("\n") + "\n```");
  assert.ok(out.includes("Show all " + lines.length + " lines"));
  assert.ok(out.includes("line" + (COLLAPSE_SHOW_LINES - 1)) && !out.includes("line" + COLLAPSE_SHOW_LINES + "\n") && !out.includes("line" + (lines.length - 1)));
  assert.ok(out.includes("md-clamped") && out.includes("Copy") && out.includes("Wrap"));
  const short = md("```\na\nb\n```");
  assert.ok(!short.includes("Show all"));
});

test("copyText says whether the clipboard took the text", async () => {
  let got = "";
  assert.equal(await copyText("x", { clipboard: { writeText: async (t) => { got = t; } } }), true);
  assert.equal(got, "x");
  assert.equal(await copyText("x", { clipboard: { writeText: async () => { throw new Error("no"); } } }), false);
  assert.equal(await copyText("x", null), false);
  assert.equal(await copyText("x", {}), false);
});

// --- Markdown: hostile and huge input ----------------------------------------

test("markup, scripts and hostile links in a reply are text, and only allow-listed elements are made", () => {
  const evil = [
    "<img src=x onerror=alert(1)>", "<script>alert(1)</script>", "<a href=\"javascript:alert(1)\">x</a>",
    "[x](javascript:alert(1))", "![i](https://example.test/p.png)", "<iframe src=//evil></iframe>",
    "| <svg onload=alert(1)> | b |", "| - | - |", "| <b>c</b> | d |", "# <u>head</u>", "> <style>x</style>", "- <embed src=x>",
    "```html\n<img src=x onerror=alert(2)>\n```",
  ].join("\n");
  const out = md(evil);
  for (const bad of ["<img", "<script", "<iframe", "<svg", "<style", "<embed", "<u>", "<b>c", 'href="javascript']) {
    assert.ok(!out.includes(bad), bad + " was drawn: " + out);
  }
  assert.ok(out.includes("&lt;img src=x onerror=alert(1)>") || out.includes("&lt;img src=x onerror=alert(1)&gt;"));
  const used = tags(renderMarkdown(evil));
  for (const t of used) assert.ok(ALLOWED.has(t), "made an element outside the allow-list: " + t);
});

test("a 200 KB line is text and takes bounded time, whatever it is made of", () => {
  const started = Date.now();
  for (const unit of ["a", "*a", "`a", "_a_ ", "[a](", "<b>", "~~a", "||"]) {
    const line = unit.repeat(Math.ceil(200000 / unit.length));
    const nodes = renderMarkdown(line);
    assert.ok(nodes.length >= 1);
    const used = tags(nodes);
    if (unit === "a") assert.deepEqual([...used], ["p"], "a plain line made elements");
    for (const t of used) assert.ok(ALLOWED.has(t), unit + " made " + t);
  }
  assert.ok(Date.now() - started < 15000, "took " + (Date.now() - started) + " ms");
  assert.ok(MAX_INLINE < 200000);
});

test("deep nesting of quotes and lists does not overflow the stack", () => {
  const quote = ">".repeat(5000) + " deep";
  assert.ok(md(quote).includes("deep"));
  const list = Array.from({ length: 300 }, (_, i) => " ".repeat(i * 2) + "- item" + i).join("\n");
  assert.ok(md(list).includes("item0"));
  assert.ok(MAX_DEPTH <= 10);
  const emphasis = "**a_".repeat(3000);
  assert.ok(md(emphasis).includes("a"));
});

test("an unclosed fence, an empty reply and a null reply are drawn without failing", () => {
  assert.ok(md("```\nnever closed").includes("never closed"));
  assert.equal(renderMarkdown("").length, 0);
  assert.equal(renderMarkdown(null).length, 0);
  assert.ok(md("a\r\nb").includes("<br"));
});

// --- Rows --------------------------------------------------------------------

test("a reply is Markdown, a prompt is text, and a kind the page does not know is said", () => {
  const reply = renderToString(html`<${Row} entry=${{ id: "r", kind: "reply", markdown: "**bold** <b>x</b>" }} />`);
  assert.ok(reply.includes("<strong>bold</strong>") && !reply.includes("<b>x"));
  const prompt = renderToString(html`<${Row} entry=${{ id: "p", kind: "prompt", text: "**not bold** <i>x</i>" }} />`);
  assert.ok(prompt.includes("**not bold**") && !prompt.includes("<strong>") && !prompt.includes("<i>"));
  const odd = renderToString(html`<${Row} entry=${{ id: "o", kind: "hologram" }} />`);
  assert.ok(odd.includes("Unknown entry (hologram)"));
  const none = renderToString(html`<${Row} entry=${{ id: "o" }} />`);
  assert.ok(none.includes("Unknown entry"));
});

test("a tool row opens to its summary, or says it has none", () => {
  const e = { id: "t", kind: "tool", name: "Bash", label: "go test", status: "ok", summary: "go test ./internal/... <b>x</b>" };
  const closed = renderToString(html`<${Row} entry=${e} />`);
  assert.ok(closed.includes('aria-expanded="false"') && !closed.includes("conv-tool-summary"));
  const open = renderToString(html`<${Row} entry=${e} open=${true} />`);
  assert.ok(open.includes('aria-expanded="true"') && open.includes("go test ./internal/...") && !open.includes("<b>x"));
  const bare = renderToString(html`<${Row} entry=${{ ...e, summary: "" }} open=${true} />`);
  assert.ok(bare.includes(NO_SUMMARY_WORDS));
  const sub = renderToString(html`<${Row} entry=${{ id: "s", kind: "subagent", description: "tests", status: "running", summary: "why" }} open=${true} />`);
  assert.ok(sub.includes("Helper: tests") && sub.includes("why"));
});

// --- The arithmetic ----------------------------------------------------------

const idsOf = (n) => Array.from({ length: n }, (_, i) => "e" + i);

test("the first row drawn stays put while entries are added below", () => {
  assert.equal(startIndex(idsOf(10), "", true), 0);
  assert.equal(startIndex(idsOf(500), "", true), 500 - START_ROWS);
  const top = "e" + (500 - START_ROWS);
  assert.equal(startIndex(idsOf(520), top, false), 500 - START_ROWS, "a list being read slid");
  assert.equal(startIndex(idsOf(520), top, true), 500 - START_ROWS, "under the cap it slid");
  assert.equal(startIndex(idsOf(MAX_ROWS + 300), "e0", true), 300, "following past the cap kept too many rows");
  assert.equal(startIndex(idsOf(MAX_ROWS + 300), "e0", false), 0, "a list being read lost its place");
  assert.equal(startIndex(idsOf(50), "gone", false), 0, "an entry that is gone");
});

test("one step up reveals a page, and never more than the most rows", () => {
  assert.equal(earlierStart(200, 400), 200 - PAGE_ROWS);
  assert.equal(earlierStart(20, 400), 0);
  assert.equal(earlierStart(1000, 2000), 1000, "past the cap");
  assert.equal(earlierStart(500, MAX_ROWS + 480), 480);
});

test("the position moves by what was added above, and the bottom is near the bottom", () => {
  assert.equal(anchored(300, 2000, 2800), 1100);
  assert.equal(anchored(0, 2000, 2000), 0);
  assert.equal(atBottom(2000, 1500, 480), true);
  assert.equal(atBottom(2000, 1400, 480), false);
  assert.equal(atBottom(400, 0, 400), true, "a list that fits");
});

test("j and k move one row, stop at the ends, and start from the ends", () => {
  assert.equal(moveTo(5, -1, 1), 0);
  assert.equal(moveTo(5, -1, -1), 4);
  assert.equal(moveTo(5, 2, 1), 3);
  assert.equal(moveTo(5, 2, -1), 1);
  assert.equal(moveTo(5, 4, 1), 4);
  assert.equal(moveTo(5, 0, -1), 0);
  assert.equal(moveTo(0, -1, 1), -1);
});

test("new entries below are counted from the last one seen", () => {
  assert.equal(newBelow(idsOf(10), "e9"), 0);
  assert.equal(newBelow(idsOf(10), "e6"), 3);
  assert.equal(newBelow(idsOf(10), "gone"), 10);
  assert.equal(newBelow(idsOf(10), ""), 0);
});

// --- The list in a page ------------------------------------------------------

const entry = (n, over = {}) => ({ id: "e" + n, kind: "prompt", text: "say " + n, ...over });
const range = (from, to) => Array.from({ length: to - from + 1 }, (_, i) => entry(from + i));

async function mount(props) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const draw = (p) => render(html`<${Log} ...${p} />`, container);
  draw(props);
  await settle();
  return { dom, container, draw, rows: () => container.findAll(".conv-row"), done: () => { render(null, container); restore(); } };
}

test("a long conversation draws only the newest rows, and Older steps up from what is held before asking", async () => {
  const asked = [];
  const props = { entries: range(1, 300), atStart: false, older: "idle", onOlder: () => asked.push("older") };
  const m = await mount(props);
  try {
    assert.equal(m.rows().length, START_ROWS);
    assert.equal(m.rows()[0].getAttribute("data-entry"), "e101");
    m.container.find(".conv-older").click();
    await settle();
    assert.equal(m.rows().length, START_ROWS + PAGE_ROWS);
    assert.deepEqual(asked, [], "the desktop was asked while rows were held back");
    m.container.find(".conv-older").click();
    await settle();
    assert.equal(m.rows().length, 300, "the last step reveals what is left, not more");
    assert.deepEqual(asked, [], "the desktop was asked while rows were held back");
    m.container.find(".conv-older").click();
    await settle();
    assert.deepEqual(asked, ["older"], "the desktop is asked once all that is held is drawn");
  } finally {
    m.done();
  }
});

test("a page that arrives above what was drawn is drawn, and the rows stay bounded", async () => {
  const m = await mount({ entries: range(101, 160), atStart: false, older: "idle", onOlder() {} });
  try {
    assert.equal(m.rows().length, 60);
    m.draw({ entries: range(51, 160), atStart: false, older: "idle", onOlder() {} });
    await settle();
    assert.equal(m.rows().length, 110);
    m.draw({ entries: range(1, 160), atStart: true, older: "idle", onOlder() {} });
    await settle();
    assert.equal(m.rows().length, 160);
    assert.ok(!m.container.find(".conv-older"), "Older at the start");
    // 12 000 entries held, 3 000 more arriving: never more than MAX_ROWS elements.
    m.draw({ entries: range(1, 12000), atStart: true, older: "idle", onOlder() {} });
    await settle();
    assert.ok(m.rows().length <= MAX_ROWS, m.rows().length + " rows");
  } finally {
    m.done();
  }
});

test("at the row limit the list says earlier entries are not drawn, and does not ask for more", async () => {
  let asked = 0;
  const m = await mount({ entries: range(1, MAX_ROWS), atStart: false, older: "idle", onOlder: () => { asked++; } });
  try {
    // 1000 held: START_ROWS drawn, revealed up to all of them.
    for (let i = 0; i < 20 && m.container.find(".conv-older"); i++) {
      m.container.find(".conv-older").click();
      await settle();
    }
    assert.equal(m.rows().length, MAX_ROWS);
    assert.ok(m.container.find(".conv-limit"), "no limit note");
    assert.ok(m.container.find(".conv-limit").textContent.includes(OLDER_LIMIT_WORDS));
    assert.equal(asked, 0);
  } finally {
    m.done();
  }
});

test("j and k move between rows, and Enter opens a tool row", async () => {
  const entries = [entry(1), entry(2, { kind: "tool", name: "Bash", label: "ls", status: "ok", summary: "ls -la" }), entry(3, { kind: "reply", text: "done" })];
  const m = await mount({ entries, atStart: true });
  try {
    const list = m.container.find(".conv");
    list.focus();
    const rows = m.rows();
    let ev = list.keydown("j");
    assert.equal(ev.defaultPrevented, true);
    assert.ok(m.dom.document.activeElement === rows[0], "j from the list did not reach the first row");
    rows[0].keydown("j");
    assert.ok(m.dom.document.activeElement === rows[1]);
    rows[1].keydown("j");
    assert.ok(m.dom.document.activeElement === rows[2]);
    rows[2].keydown("j");
    assert.ok(m.dom.document.activeElement === rows[2], "j went past the last row");
    rows[2].keydown("k");
    assert.ok(m.dom.document.activeElement === rows[1]);
    assert.ok(!m.container.find(".conv-tool-summary"));
    rows[1].keydown("Enter");
    await settle();
    assert.ok(m.container.find(".conv-tool-summary"), "Enter did not open the tool row");
    assert.ok(m.container.find(".conv-tool-head").getAttribute("aria-expanded") === "true");
    m.rows()[1].keydown("Enter");
    await settle();
    assert.ok(!m.container.find(".conv-tool-summary"), "Enter did not close it");
    rows[0].keydown("Enter");
    await settle();
    assert.ok(!m.container.find(".conv-tool-summary"), "Enter on a prompt opened something");
    // From a button inside a row the keys are the button's own.
    ev = m.container.find(".conv-tool-head").keydown("j");
    assert.equal(ev.defaultPrevented, false);
  } finally {
    m.done();
  }
});

test("clicking a tool row opens it", async () => {
  const m = await mount({ entries: [entry(1, { kind: "tool", name: "Read", label: "a.go", status: "ok", summary: "a.go" })], atStart: true });
  try {
    m.container.find(".conv-tool-head").click();
    await settle();
    assert.ok(m.container.find(".conv-tool-summary"));
  } finally {
    m.done();
  }
});

test("a reply with hostile markup in a drawn list makes no element but the allow-list's", async () => {
  const evil = "<img src=x onerror=alert(1)> [x](javascript:alert(1)) <script>alert(1)</script>";
  const m = await mount({ entries: [entry(1, { kind: "reply", markdown: evil })], atStart: true });
  try {
    const bad = m.container.findAll("img").length + m.container.findAll("script").length + m.container.findAll("a").length;
    assert.equal(bad, 0, "an element came from the reply's text");
    assert.ok(m.container.textContent.includes("<img src=x onerror=alert(1)>"));
  } finally {
    m.done();
  }
});

test("MAX_REPLY is the size the rows cut a reply at", () => {
  assert.ok(MAX_REPLY >= 10000 && MAX_REPLY <= 50000);
});

test("inline text over the limit is not parsed, and code over the highlight limit is not highlighted", () => {
  const unit = "**b** ";
  const under = md(unit.repeat(10));
  assert.ok(under.includes("<strong>b</strong>"));
  const over = md(unit.repeat(Math.ceil((MAX_INLINE + 100) / unit.length)));
  assert.ok(!over.includes("<strong>"), "a run over MAX_INLINE was parsed");
  assert.ok(over.includes("**b**"), "the text was lost");
  const code = "func x() { return 1 }\n";
  assert.ok(md("```go\n" + code + "```").includes("tok-kw"));
  // One line, so that the fold does not cut it to a short preview first.
  const oneLine = "func x() { return 1 } ";
  const big = oneLine.repeat(Math.ceil((MAX_HIGHLIGHT + 100) / oneLine.length));
  assert.ok(md("```go\n" + oneLine + "\n```").includes("tok-kw"));
  assert.ok(!md("```go\n" + big + "\n```").includes("tok-kw"), "huge code was highlighted");
});

test("a link to anything but http and https is not made, in a link and a bare form", () => {
  for (const url of ["javascript:alert(1)", "JAVASCRIPT:alert(1)", "vbscript:x", "file:///etc/passwd", "//evil.test/x", "ftp://x.test/y"]) {
    const out = md("[t](" + url + ")");
    assert.ok(!out.includes("<a "), url + " made a link: " + out);
  }
  assert.ok(md("[t](HTTPS://X.test/y)").includes("<a "), "an upper-case scheme");
});

// --- Following the bottom, the chip, and rows added above --------------------

/** geometry gives the list element a size the fake DOM does not compute. */
function geometry(el, { height, client, top }) {
  el.scrollHeight = height;
  el.clientHeight = client;
  el.scrollTop = top;
}

test("the list follows new entries at the bottom, and counts them on a chip when it is not", async () => {
  const props = (entries) => ({ entries, atStart: true });
  const m = await mount(props(range(1, 5)));
  try {
    const list = () => m.container.find(".conv");
    // At the bottom: new entries are followed and there is no chip.
    geometry(list(), { height: 1000, client: 400, top: 580 });
    list().dispatch("scroll");
    m.draw(props(range(1, 6)));
    await settle();
    assert.equal(list().scrollTop, list().scrollHeight, "the list did not follow");
    assert.ok(!m.container.find(".conv-new"));
    // Scrolled up: they are counted, the position is kept.
    geometry(list(), { height: 1000, client: 400, top: 100 });
    list().dispatch("scroll");
    m.draw(props(range(1, 8)));
    await settle();
    assert.equal(list().scrollTop, 100, "the list moved while being read");
    assert.equal(m.container.find(".conv-new").textContent, "2 new below");
    m.draw(props(range(1, 9)));
    await settle();
    assert.equal(m.container.find(".conv-new").textContent, "3 new below");
    // The chip goes to the bottom and goes away.
    m.container.find(".conv-new").click();
    await settle();
    assert.ok(!m.container.find(".conv-new"));
    assert.equal(list().scrollTop, list().scrollHeight);
    m.draw(props(range(1, 10)));
    await settle();
    assert.equal(list().scrollTop, list().scrollHeight, "the list stopped following after the chip");
  } finally {
    m.done();
  }
});

test("scrolling back to the bottom clears the chip", async () => {
  const props = (entries) => ({ entries, atStart: true });
  const m = await mount(props(range(1, 5)));
  try {
    const list = () => m.container.find(".conv");
    geometry(list(), { height: 1000, client: 400, top: 100 });
    list().dispatch("scroll");
    m.draw(props(range(1, 7)));
    await settle();
    assert.ok(m.container.find(".conv-new"));
    geometry(list(), { height: 1000, client: 400, top: 590 });
    list().dispatch("scroll");
    await settle();
    assert.ok(!m.container.find(".conv-new"));
  } finally {
    m.done();
  }
});

test("rows added above what is being read do not move it", async () => {
  const props = (entries, atStart) => ({ entries, atStart, older: "idle", onOlder() {} });
  const m = await mount(props(range(101, 160), false));
  try {
    const list = () => m.container.find(".conv");
    geometry(list(), { height: 2000, client: 400, top: 500 });
    list().dispatch("scroll");
    m.draw(props(range(101, 160), false)); // the page lays out at this height
    await settle();
    list().scrollHeight = 2600; // what the page above adds
    m.draw(props(range(51, 160), false));
    await settle();
    assert.equal(list().scrollTop, 1100, "the same rows are not in view");
  } finally {
    m.done();
  }
});

test("scrolling to the top steps up, and asks the desktop only when all that is held is drawn", async () => {
  let asked = 0;
  const props = (entries) => ({ entries, atStart: false, older: "idle", onOlder: () => { asked++; } });
  const m = await mount(props(range(1, 260)));
  try {
    const list = () => m.container.find(".conv");
    assert.equal(m.rows().length, START_ROWS);
    geometry(list(), { height: 5000, client: 400, top: 10 });
    m.draw(props(range(1, 260)));
    await settle();
    list().scrollTop = 10;
    list().dispatch("scroll");
    await settle();
    assert.equal(m.rows().length, START_ROWS + PAGE_ROWS);
    assert.equal(asked, 0);
    list().scrollTop = 10;
    list().dispatch("scroll");
    await settle();
    assert.equal(m.rows().length, 260);
    assert.equal(asked, 0);
    list().scrollTop = 10;
    list().dispatch("scroll");
    await settle();
    assert.equal(asked, 1);
    // Not while a page is on its way.
    m.draw({ ...props(range(1, 260)), older: "loading" });
    await settle();
    list().scrollTop = 10;
    list().dispatch("scroll");
    assert.equal(asked, 1);
  } finally {
    m.done();
  }
});

test("a link whose text is a web address for another host shows the real host beside it", () => {
  const out = md("[https://github.com/x](https://evil.example/p) and [https://ok.test/a](https://ok.test/b) and [label](https://evil.example)");
  assert.ok(out.includes('href="https://evil.example/p"'));
  assert.ok(out.includes("(evil.example)"), "the real host was not shown: " + out);
  assert.equal(out.split("md-linkhost").length - 1, 1, "a same-host link or a plain label got a host");
});
