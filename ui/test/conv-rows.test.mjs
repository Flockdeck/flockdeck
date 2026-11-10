// The rows of the conversation (T251): no You or Agent label, a time on a row, Show all and Copy
// on long text, failure notices, the states (empty, loading, error) and the pieces under them.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Row, MAX_TEXT, EXPAND_MAX_REPLY, EXPAND_MAX_TEXT, CHUNK_REPLY } from "../src/drawer/conv/rows.js";
import { Output, OUTPUT_CAP, OUTPUT_ALL_CAP, OUTPUT_CHUNK } from "../src/drawer/conv/Tool.js";
import { chunkText } from "../src/drawer/conv/chunks.js";
import { rowTime } from "../src/drawer/conv/time.js";
import { Conversation, EmptyState, Skeleton, ErrorBlock, NO_ENTRIES_WORDS, LOADING_WORDS } from "../src/drawer/Conversation.js";
import { emptyTips } from "../src/drawer/Structured.js";
import { createDom, install, settle } from "./dom.mjs";

const e = (over) => ({ id: "e1", kind: "reply", ts: "2026-10-21T09:05:00Z", markdown: "hello", ...over });

async function page(vnode, fn) {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(vnode, box);
    await settle();
    await fn(box, async (v) => { render(v, box); await settle(); });
  } finally {
    restore();
  }
}

// --- chunks and time ----------------------------------------------------------

test("chunkText cuts at line breaks, keeps every character and never makes a piece over the size", () => {
  const text = Array.from({ length: 50 }, (_, i) => "line " + i + " " + "x".repeat(30)).join("\n");
  const parts = chunkText(text, 200);
  assert.equal(parts.join(""), text);
  assert.ok(parts.length > 5);
  assert.ok(parts.every((p) => p.length <= 201));
  assert.ok(parts.slice(0, -1).every((p) => p.endsWith("\n")), "a piece was cut in the middle of a line");
  assert.deepEqual(chunkText("short", 200), ["short"]);
  assert.deepEqual(chunkText("", 200), []);
  // No line break at all: cut at the size, and not between the halves of a surrogate pair.
  const emoji = "😀".repeat(300);
  const cut = chunkText(emoji, 101);
  assert.equal(cut.join(""), emoji);
  assert.ok(cut.every((p) => !/^[\udc00-\udfff]/.test(p) && !/[\ud800-\udbff]$/.test(p)));
});

test("chunkText for Markdown does not split a code fence", () => {
  const fence = "```\n" + Array.from({ length: 30 }, (_, i) => "code " + i).join("\n") + "\n```\n\n";
  const text = "intro paragraph\n\n" + fence + "after the fence\n\n" + "tail ".repeat(40);
  const parts = chunkText(text, 400, true);
  assert.equal(parts.join(""), text);
  const at = parts.findIndex((p) => p.includes("code 0"));
  assert.ok(parts[at].includes("code 29"), "the fence was split from its end");
});

test("rowTime reads RFC 3339 and says nothing for what is not a time", () => {
  const t = rowTime("2026-10-21T09:05:00Z");
  assert.match(t.label, /\d/);
  assert.equal(t.iso, "2026-10-21T09:05:00.000Z");
  assert.ok(t.full.length > 8);
  for (const bad of ["", "soon", 12, null, undefined, "x".repeat(60)]) assert.equal(rowTime(bad), null, String(bad));
});

// --- prompt and reply rows ------------------------------------------------------

test("a prompt and a reply carry no visible You or Agent label; a screen reader still gets the speaker", () => {
  const p = renderToString(html`<${Row} entry=${e({ kind: "prompt", text: "do it" })} />`);
  const r = renderToString(html`<${Row} entry=${e()} />`);
  assert.ok(!p.includes("conv-who") && !r.includes("conv-who"));
  assert.match(p, /<span class="conv-sr">You: <\/span>/);
  assert.match(r, /<span class="conv-sr">Agent: <\/span>/);
});

test("a prompt is a bubble and a reply has none; both have the time at the foot", () => {
  const p = renderToString(html`<${Row} entry=${e({ kind: "prompt", text: "do it" })} />`);
  const r = renderToString(html`<${Row} entry=${e()} />`);
  assert.match(p, /class="conv-bubble"/);
  assert.ok(!r.includes("conv-bubble"));
  for (const out of [p, r]) assert.match(out, /<time class="conv-time" datetime="2026-10-21T09:05:00.000Z" title="[^"]+">[^<]*\d[^<]*<\/time>/);
  // An entry whose ts is not a time has no time element.
  assert.ok(!renderToString(html`<${Row} entry=${e({ ts: "later" })} />`).includes("<time"));
});

test("Copy on a prompt is labelled for the prompt", () =>
  page(html`<${Row} entry=${e({ kind: "prompt", text: "p".repeat(MAX_TEXT + 50) })} />`, async (box) => {
    assert.equal(box.find(".conv-copy-reply").getAttribute("aria-label"), "Copy this prompt");
  }));

test("a long prompt says how much is cut, and Show all draws it in pieces up to a ceiling", () =>
  page(html`<${Row} entry=${e({ kind: "prompt", text: "w".repeat(MAX_TEXT + 9000) })} />`, async (box) => {
    assert.match(box.find(".conv-cut").textContent, /9,000 more characters are not shown\./);
    assert.equal(box.find(".conv-text").findAll("span").length, 0);
    box.find(".conv-showall").click();
    await settle();
    assert.equal(box.find(".conv-cut"), null);
    const spans = box.find(".conv-text").findAll("span");
    assert.ok(spans.length >= 3, "the text was not in pieces");
    assert.equal(spans.reduce((n, s) => n + s.textContent.length, 0), MAX_TEXT + 9000);
    assert.ok(spans.every((s) => s.textContent.length <= 4001));
    box.find(".conv-showall").click();
    await settle();
    assert.equal(box.find(".conv-text").findAll("span").length, 0, "Show less did not fold it");
  }));

test("a prompt past the ceiling stays cut with the rest said", () =>
  page(html`<${Row} entry=${e({ kind: "prompt", text: "w".repeat(EXPAND_MAX_TEXT + 700) })} />`, async (box) => {
    box.find(".conv-showall").click();
    await settle();
    assert.match(box.find(".conv-cut").textContent, /700 more characters are not shown\./);
  }));

test("a long reply has Show all and Copy; all of it is drawn as several Markdown nodes, never one", () =>
  page(html`<${Row} entry=${e({ markdown: Array.from({ length: 120 }, (_, i) => "paragraph " + i + " " + "z".repeat(400)).join("\n\n") })} />`, async (box) => {
    assert.match(box.find(".conv-cut").textContent, /more characters are not shown\./);
    assert.equal(box.findAll(".md").length, 1);
    box.find(".conv-showall").click();
    await settle();
    assert.equal(box.find(".conv-cut"), null);
    assert.ok(box.textContent.includes("paragraph 119"), "the last paragraph is not drawn");
    assert.ok(box.findAll(".md").length >= 3, "the reply was drawn as one node");
    assert.ok(box.findAll(".md").every((m) => m.textContent.length <= CHUNK_REPLY + 1), "a node held more than a chunk");
    assert.equal(box.find(".conv-showall").textContent, "Show less");
    assert.equal(box.find(".conv-copy-reply").getAttribute("aria-label"), "Copy this reply");
  }));

test("a reply past the ceiling says what is left out after Show all", () =>
  page(html`<${Row} entry=${e({ markdown: "q".repeat(EXPAND_MAX_REPLY + CHUNK_REPLY + 5) })} />`, async (box) => {
    box.find(".conv-showall").click();
    await settle();
    assert.match(box.find(".conv-cut").textContent, /20,005 more characters are not shown\./);
  }));

test("a short row has no Show all, and the foot is not kept open for it", () => {
  const out = renderToString(html`<${Row} entry=${e()} />`);
  assert.ok(!out.includes("conv-showall"));
  assert.ok(!out.includes("is-kept"));
});

test("Retry is drawn only when the row is given onRetry, and sends the whole prompt text", () => {
  const sent = [];
  const long = "t".repeat(MAX_TEXT + 10);
  return page(html`<${Row} entry=${e({ kind: "prompt", text: long, typed: true })} onRetry=${(t) => sent.push(t)} />`, async (box) => {
    box.find(".conv-retry").click();
    assert.deepEqual(sent, [long], "the text the window holds is sent whole, not the cut one");
    assert.ok(!renderToString(html`<${Row} entry=${e({ kind: "prompt", text: "a", typed: true })} />`).includes("conv-retry"));
  });
});

test("Retry is for a prompt the person typed: a replayed, system, wake or any prompt without the typed mark has none", () => {
  const on = () => {};
  const has = (entry) => renderToString(html`<${Row} entry=${e({ kind: "prompt", text: "go", ...entry })} onRetry=${on} />`).includes("conv-retry");
  assert.ok(has({ typed: true }));
  for (const typed of [undefined, false, null, "true", 1, "yes"]) assert.ok(!has({ typed }), String(typed));
  assert.ok(!has({}), "a prompt from an older app, which does not say, is not offered Retry");
  // The mark is read from the entry's field, never from the words.
  assert.ok(!has({ text: "typed: true" }));
});

test("a prompt with pictures says how many, has no Retry, and keeps Copy", () => {
  const on = () => {};
  const draw = (images) => renderToString(html`<${Row} entry=${e({ kind: "prompt", text: "look", typed: true, images })} onRetry=${on} />`);
  const one = draw([{ mediaType: "image/png", bytes: 10 }]);
  assert.match(one, /<p class="conv-pics">1 picture<\/p>/);
  assert.ok(!one.includes("conv-retry"));
  assert.ok(one.includes("Copy this prompt"));
  assert.match(draw([{}, {}, {}]), /<p class="conv-pics">3 pictures<\/p>/);
  // No pictures: no note, and Retry stays.
  for (const none of [undefined, [], null, "x"]) {
    const out = draw(none);
    assert.ok(!out.includes("conv-pics"), String(none));
    assert.ok(out.includes("conv-retry"), String(none));
  }
  assert.ok(!one.includes("is-kept"), "nothing on the foot needs keeping open");
});

test("a nested prompt or reply with no usable time and no action draws no foot", () => {
  for (const kind of ["prompt", "reply"]) {
    for (const ts of [undefined, "", "not a time"]) {
      const out = renderToString(html`<${Row} entry=${e({ kind, text: "x", markdown: "x", ts })} nested=${true} />`);
      assert.ok(!out.includes("conv-foot"), kind + " " + String(ts));
    }
    // With a time, or when it is not nested (Copy), the foot is there.
    assert.ok(renderToString(html`<${Row} entry=${e({ kind, text: "x", markdown: "x" })} nested=${true} />`).includes("conv-foot"));
    assert.ok(renderToString(html`<${Row} entry=${e({ kind, text: "x", markdown: "x", ts: undefined })} />`).includes("conv-foot"));
  }
  // A cut nested prompt has Show all, so it has a foot even with no time.
  assert.ok(renderToString(html`<${Row} entry=${e({ kind: "prompt", text: "t".repeat(MAX_TEXT + 1), ts: undefined })} nested=${true} />`).includes("conv-showall"));
});

// --- notices --------------------------------------------------------------------

test("a failure notice is drawn apart from a plain note, and an unknown tone is a plain one", () => {
  const f = renderToString(html`<${Row} entry=${e({ kind: "notice", text: "Turn ended: refusal", tone: "failure" })} />`);
  assert.match(f, /conv-note--failure/);
  assert.match(f, /data-tone="failure"/);
  for (const tone of [undefined, "", "loud", "FAILURE"]) {
    const p = renderToString(html`<${Row} entry=${e({ kind: "notice", text: "Conversation compacted", tone })} />`);
    assert.ok(!p.includes("failure"), String(tone));
    assert.match(p, /data-kind="notice"/);
  }
});

test("an entry of a kind the page does not know is still the Unknown entry row", () => {
  const out = renderToString(html`<${Row} entry=${e({ kind: "hologram", tone: "failure" })} />`);
  assert.match(out, /conv-unknown/);
  assert.match(out, /Unknown entry \(hologram\)/);
  assert.ok(!out.includes("failure"));
});

// --- tool output ------------------------------------------------------------------

test("long tool output has Show all and Copy; Show all draws it in pieces up to a ceiling", () => {
  const body = Array.from({ length: 4000 }, (_, i) => "output line " + i).join("\n");
  assert.ok(body.length > OUTPUT_CAP * 2 && body.length < OUTPUT_ALL_CAP);
  return page(html`<${Output} text=${body} />`, async (box) => {
    assert.match(box.find(".conv-cut").textContent, /Output truncated: [\d,]+ bytes more are not shown\./);
    assert.equal(box.find(".conv-copy-reply").getAttribute("aria-label"), "Copy this output");
    box.find(".conv-showall").click();
    await settle();
    assert.equal(box.find(".conv-cut"), null);
    const spans = box.find(".conv-output-text").findAll("span");
    assert.ok(spans.length > 5);
    assert.ok(spans.every((s) => s.textContent.length <= OUTPUT_CHUNK + 1));
    assert.equal(spans.map((s) => s.textContent).join(""), body);
  });
});

test("tool output past the ceiling says what is left after Show all; short output has neither button", () => {
  const huge = "o".repeat(OUTPUT_ALL_CAP + 1000);
  return page(html`<${Output} text=${huge} />`, async (box, redraw) => {
    box.find(".conv-showall").click();
    await settle();
    assert.match(box.find(".conv-cut").textContent, /1,000 bytes more are not shown\./);
    await redraw(html`<${Output} key="other" text="small" />`);
    assert.equal(box.find(".conv-showall"), null);
    assert.equal(box.find(".conv-copy-reply"), null);
  });
});

// --- states ------------------------------------------------------------------------

test("the empty state is a block that lists the ways to start; without tips it is the one line", () => {
  assert.deepEqual(emptyTips({}).map((t) => t.key), ["/", "@", "!"]);
  assert.deepEqual(emptyTips(null).map((t) => t.key), ["/", "@", "!"]);
  const tips = emptyTips({ capabilities: { image: true } });
  return page(html`<${EmptyState} tips=${tips} />`, async (box, redraw) => {
    assert.ok(box.find(".conv-empty"));
    assert.ok(box.textContent.includes(NO_ENTRIES_WORDS));
    assert.deepEqual(box.findAll("kbd").map((k) => k.textContent), ["/", "@", "!", "Paste"]);
    await redraw(html`<${EmptyState} />`);
    assert.equal(box.findAll("kbd").length, 0);
  });
});

test("the empty state is in the page as an empty live region and filled on the next tick", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${EmptyState} tips=${emptyTips({})} />`, box);
    const region = box.querySelector('[role="status"]');
    assert.ok(region, "no live region");
    assert.equal(region.textContent, "", "the region mounted already holding its words, which many screen readers do not announce");
    await settle();
    assert.ok(region.textContent.includes(NO_ENTRIES_WORDS));
    assert.equal(box.querySelector('[role="status"]'), region, "the region was replaced when it was filled");
  } finally {
    restore();
  }
});
test("loading is a skeleton that still says it in words for a screen reader", async () => {
  const out = renderToString(html`<${Conversation} conv=${null} />`);
  assert.match(out, /conv-skeleton/);
  assert.match(out, /aria-busy="true"/);
  assert.match(out, /role="status"/);
  assert.ok(renderToString(html`<${Conversation} conv=${{ id: "p", supported: null, entries: [] }} />`).includes("conv-skeleton"));
  await page(html`<${Conversation} conv=${null} />`, async (box, redraw) => {
    assert.ok(box.textContent.includes(LOADING_WORDS));
    await redraw(html`<${Skeleton} words="Loading the agent." />`);
    assert.ok(box.textContent.includes("Loading the agent."));
  });
});

test("the loading words are put in the live region after it is in the page, not with it", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${Skeleton} />`, box);
    const region = box.querySelector('[role="status"]');
    assert.ok(region);
    assert.ok(!region.textContent.includes(LOADING_WORDS), "the words were there on the first draw");
    assert.ok(region.querySelector(".conv-skel"), "the grey lines are drawn at once");
    await settle();
    assert.ok(region.textContent.includes(LOADING_WORDS));
    assert.equal(box.querySelector('[role="status"]'), region);
  } finally {
    restore();
  }
});

test("a refusal is the Changes tab's error block with Retry when it can be retried", () => {
  const conv = { id: "p", supported: true, entries: [], refused: "The desk said no" };
  const none = renderToString(html`<${Conversation} conv=${conv} />`);
  assert.match(none, /chg-state chg-error[^"]*conv-refused/);
  assert.ok(none.includes("The desk said no"));
  assert.match(none, /role="alert"/, "an error is announced as an alert, which needs no second draw");
  assert.ok(!none.includes('role="status"'));
  assert.ok(!none.includes("conv-retry-open"));
  let asked = 0;
  return page(html`<${Conversation} conv=${conv} onRetry=${() => { asked++; }} />`, async (box) => {
    box.find(".conv-retry-open").click();
    assert.equal(asked, 1);
    assert.ok(box.find(".conv-refused").find("strong").textContent.length > 0);
  });
});

test("an ErrorBlock gives its title and words", () => {
  const out = renderToString(html`<${ErrorBlock} title="Title" words="Words" />`);
  assert.ok(out.includes("<strong>Title</strong>") && out.includes("<span>Words</span>"));
});
