// The right-click menus for what the page shows rather than what it runs: a task
// or message on the team board, a changed file, a row of a conversation, a link
// and a review comment. They are merged into MENUS (menus.js).
//
// Each reads its element, and where the part already has a button for an action
// (open a task, show all of a message, pick a file) the item clicks that button,
// so a disabled button, a guard and a viewer's refusal are the ones the button has.
// An item that copies reads the text from the element or an attribute on it.

const text = (el) => String((el && el.textContent) || "").trim();
const first = (el, sel) => (el && el.querySelector ? el.querySelector(sel) : null);
const all = (el, sel) => (el && el.querySelectorAll ? Array.from(el.querySelectorAll(sel)) : []);
const attr = (el, name) => (el && el.getAttribute && el.getAttribute(name)) || "";

/** pressing is an item that clicks a button the element holds, or nothing when it holds none. */
function pressing(id, label, btn, more = {}) {
  if (!btn) return { hidden: true };
  return { id, label, run: () => btn.click(), disabled: !!btn.disabled, ...more };
}

/** copying is an item that copies a value, or nothing when there is no value or no clipboard. */
function copying(ctx, id, label, value, what) {
  return { id, label, run: () => ctx.copy(value, what), hidden: !value || !ctx.copy };
}

/** Menus for the team board, in Team.js. */
const task = (el, ctx) => {
  const id = attr(el, "data-task");
  const head = first(el, ".team-task-head");
  const open = !!head && attr(head, "aria-expanded") === "true";
  const moves = all(el, ".team-move");
  return [
    head ? pressing("toggle", open ? "Close details" : "Open", head) : { hidden: true },
    ...moves.map((b) => pressing("move-" + attr(b, "data-to"), text(b), b)),
    { separator: true },
    copying(ctx, "copy-id", "Copy task id", id, "the task id"),
  ];
};

const message = (el, ctx) => [
  pressing("whole", "Show all", first(el, ".team-whole")),
  pressing("seen", "Mark seen", first(el, ".team-seen")),
  copying(ctx, "copy-text", "Copy message", text(first(el, ".team-body")), "the message"),
];

const rosterRow = (el, ctx) => {
  const pane = attr(el, "data-pane");
  const close = first(el, ".team-close");
  return [
    close ? pressing("close", "Close", close, { danger: true }) : { hidden: true },
    { separator: true },
    copying(ctx, "copy-id", "Copy pane id", pane, "the pane id"),
  ];
};

/** A changed file in the diff's list (FileList.js). */
const diffFile = (el, ctx) => {
  const path = attr(el, "data-path");
  const old = attr(el, "data-old-path");
  return [
    pressing("open", "Open diff", first(el, ".dv-filebtn")),
    { separator: true },
    copying(ctx, "copy-path", "Copy path", path, "the path"),
    copying(ctx, "copy-old", "Copy old path", old && old !== path ? old : "", "the old path"),
  ];
};

/** convText is what a row says, without the name that heads it. */
function convText(el) {
  return all(el, ".conv-text").map(text).join("\n") || Array.from(el.children || []).filter((c) => !(attr(c, "class") || "").split(/\s+/).includes("conv-who")).map(text).join("\n").trim();
}

/** A row of a conversation (conv/rows.js): the words of a prompt, a reply or a mail, or a tool call. */
const convRow = (el, ctx) => {
  const head = first(el, ".conv-tool-head");
  if (head) {
    const open = attr(head, "aria-expanded") === "true";
    return [
      pressing("toggle", open ? "Collapse" : "Expand", head),
      { separator: true },
      copying(ctx, "copy-title", "Copy title", text(first(el, ".conv-tool-title")), "the title"),
      copying(ctx, "copy-summary", "Copy summary", text(first(el, ".conv-tool-summary")), "the summary"),
    ];
  }
  return [copying(ctx, "copy-text", "Copy text", convText(el), "the text")];
};

/** A link in Markdown (conv/markdown.js). Open clicks the link, so it goes where a click would. */
const link = (el, ctx) => {
  const href = attr(el, "href");
  return [
    { id: "open", label: "Open link", run: () => el.click(), hidden: !href },
    copying(ctx, "copy-link", "Copy link address", href, "the link"),
  ];
};

/** A review comment (diff/Comments.js). */
const comment = (el, ctx) => [
  copying(ctx, "copy-text", "Copy comment", text(first(el, ".dv-cmt-text")), "the comment"),
  copying(ctx, "copy-where", "Copy location", text(first(el, ".dv-cmt-where")), "the location"),
];

export const CONTENT = { task, message, rosterRow, diffFile, convRow, link, comment };
