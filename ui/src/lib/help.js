// The help pages of the new interface: where they are fetched from, what shape
// is accepted, and how a search finds them. The pages come from the desktop as
// nodes (see ui/help/tree.go), never as markup, and this file checks the
// shape again before anything is drawn: a desktop that is older, newer or
// damaged gives a page that says so, not a page that breaks.

/** HELP_PATH is help.json, written by ui/help as nodes and not HTML, and served
 *  with the page's other assets (so through the relay it is compressed like
 *  them). The page asks for it only when help is opened. */
export const HELP_PATH = "assets/help.json";

/** helpUrl is the address of the pages for the page at `loc`. Through the relay
 *  the page lives under a prefix, so the path is the page's own directory. */
export function helpUrl(loc) {
  return new URL(HELP_PATH, new URL(loc.pathname.replace(/[^/]*$/, ""), loc.href)).href;
}

/** TAGS are the tags a page may use. ui/help/tree.go has the same list,
 *  and a test holds the two together. */
export const TAGS = [
  "p", "h1", "h2", "h3", "h4", "ul", "ol", "li", "a", "code", "pre", "em", "strong", "kbd", "del",
  "blockquote", "table", "thead", "tbody", "tr", "th", "td", "br", "hr",
];

const MAX_DEPTH = 40;
const MAX_PAGES = 200;

/** WEB are the protocols of an address that leaves the help for the web. */
const WEB = ["http:", "https:"];

/** safeLink is whether an address is one a page may link to: another page
 *  (#slug) or the web. Nothing else is a link. */
export function safeLink(dest) {
  if (typeof dest !== "string") return false;
  if (/^#[A-Za-z0-9_-]+$/.test(dest)) return true;
  try {
    return WEB.includes(new URL(dest).protocol);
  } catch {
    return false;
  }
}

/** cleanNodes keeps what a page may hold and drops the rest: a node that is not
 *  an object, a tag off the list (its children stay), a link that cannot be
 *  followed (its words stay), and anything deeper than MAX_DEPTH. */
export function cleanNodes(nodes, depth = 0) {
  const out = [];
  if (!Array.isArray(nodes) || depth > MAX_DEPTH) return out;
  for (const n of nodes) {
    if (!n || typeof n !== "object") continue;
    if (!n.t) {
      if (typeof n.x === "string" && n.x !== "") out.push({ x: n.x });
      continue;
    }
    const kids = cleanNodes(n.c, depth + 1);
    if (typeof n.t !== "string" || !TAGS.includes(n.t)) {
      out.push(...kids);
    } else if (n.t === "a") {
      if (safeLink(n.h)) out.push({ t: "a", h: n.h, c: kids });
      else out.push(...kids);
    } else {
      out.push({ t: n.t, c: kids });
    }
  }
  return out;
}

/** pagesFrom reads the answer of the desktop. It returns { pages } or { error }
 *  with the words to show. A page with no slug or title is left out. */
export function pagesFrom(data) {
  const list = data && Array.isArray(data.pages) ? data.pages : null;
  if (!list) return { error: "The help pages came back in a form this window cannot read. The desktop may be an older version." };
  const seen = new Set();
  const pages = [];
  for (const p of list.slice(0, MAX_PAGES)) {
    if (!p || typeof p.slug !== "string" || typeof p.title !== "string" || !p.slug || !p.title || seen.has(p.slug)) continue;
    seen.add(p.slug);
    const tree = cleanNodes(p.tree);
    if (tree.length === 0) continue;
    pages.push({
      slug: p.slug,
      title: p.title,
      summary: typeof p.summary === "string" ? p.summary : "",
      text: typeof p.text === "string" ? p.text : "",
      tree,
    });
  }
  if (pages.length === 0) return { error: "There are no help pages." };
  return { pages };
}

/** loadHelp fetches the pages. fetchFn and loc are the page's own, passed in so
 *  a test can give fakes. It resolves to { pages } or { error }; it does not
 *  reject. */
export async function loadHelp(fetchFn, loc) {
  try {
    const r = await fetchFn(helpUrl(loc), { credentials: "same-origin", cache: "no-store" });
    if (!r.ok) return { error: "The help pages could not be loaded (HTTP " + r.status + ")." };
    return pagesFrom(await r.json());
  } catch (err) {
    return { error: "The help pages could not be loaded (" + (err && err.message ? err.message : "no answer") + ")." };
  }
}

/** snippetFor is the words around the first match, so a hit shows why it is
 *  one. It is plain text. */
export function snippetFor(text, word) {
  const at = text.toLowerCase().indexOf(word);
  if (at < 0) return "";
  const from = Math.max(0, at - 40);
  return (from ? "…" : "") + text.slice(from, at + 80).trim() + "…";
}

/** searchPages is the contents for a query: every page when it is empty, and
 *  otherwise the pages that hold every word of it, each with the piece of text
 *  the first word was found in. Pages keep their order. */
export function searchPages(pages, query) {
  const words = (query || "").trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return pages.map((page) => ({ page, snippet: "" }));
  const out = [];
  for (const page of pages) {
    const hay = (page.title + " " + page.text).toLowerCase();
    if (words.every((w) => hay.includes(w))) out.push({ page, snippet: snippetFor(page.text, words[0]) });
  }
  return out;
}

/** keySections groups a hello's key table into the sections of its rows, in the
 *  order the table first names them. An action with no section is in "Other". */
export function keySections(table) {
  const order = [];
  const by = new Map();
  for (const k of table || []) {
    if (!k || typeof k.id !== "string") continue;
    const s = k.section || "Other";
    if (!by.has(s)) {
      by.set(s, []);
      order.push(s);
    }
    by.get(s).push(k);
  }
  return order.map((name) => ({ name, rows: by.get(name) }));
}

/** KEYS_SLUG is the entry the window makes itself, ahead of the pages. */
export const KEYS_SLUG = "keys-here";

/** keysEntry is the contents entry for the key table of this window: not a page
 *  from the desktop, since it is drawn from the table the window was sent, so a
 *  remapped key is shown as remapped. text is what the search matches. */
export function keysEntry(table) {
  const text = (table || []).map((k) => [k.label, k.keys, k.short].filter(Boolean).join(" ")).join(" ");
  return {
    slug: KEYS_SLUG,
    title: "Keys in this window",
    summary: "Every action and its key, as set on this desktop, and which of them this window runs yet.",
    text: "keys shortcuts bindings " + text,
    keys: true,
  };
}
