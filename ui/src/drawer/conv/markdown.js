// markdown.js -- turning an assistant reply's GitHub-flavoured Markdown into
// Preact nodes, safely. Every node is made with h() or is a string, which
// Preact draws as a text node, and nothing is ever parsed as HTML: a literal
// "<script>" in a reply is a literal "<script>" on the page, and a heading or
// a table cell that holds a tag shows the tag as text. A hand-written parser,
// on purpose: no third-party code runs here.
//
// It is the parser of the phone app's web/app/markdown.js (same owner, and no
// third-party code in it), moved from building DOM nodes to building Preact
// nodes. Beside that move it has three limits the phone's did not: a run of
// inline text over MAX_INLINE characters is drawn as plain text (the inline
// patterns can take quadratic time on a long line), blocks nest at most
// MAX_DEPTH deep (the parser recurses), and a code block's "Show all" and
// "Copy" are components with their own state.
//
// It understands what agents actually write: headings, paragraphs with soft
// line breaks, ordered, unordered and task lists (nested, tight or loose),
// block quotes (nested), horizontal rules, tables with alignment, fenced and
// indented code, and the inline set: code, bold, italic, bold-italic,
// strikethrough, links, autolinks, bare URLs and backslash escapes. A link
// becomes an <a> only when its address starts with http:// or https://, so a
// javascript: link, or anything else, stays the plain text it was.
//
// Allow-list of elements it can make: p, h1 to h6, ul, ol, li, blockquote, hr,
// table (thead, tbody, tr, th, td) in a div, pre, code, strong, em, del, a, br,
// span, div, button.
import { h } from "../../vendor/preact.mjs";
import { useContext, useState } from "../../vendor/hooks.mjs";
import { ChipsContext, chipify } from "./chips.js";

/** MAX_INLINE is the longest run of inline text that is parsed. Longer is text. */
export const MAX_INLINE = 5000;
/** MAX_DEPTH is how deep quotes and list items nest before the rest is a paragraph. */
export const MAX_DEPTH = 8;
const MAX_INLINE_DEPTH = 6;

// -------------------------------------------------------------- block parse

/** A list marker line: up to 3 leading spaces, then "-", "*", "+" or a
 *  1-9-digit number followed by "." or ")", then either one space/tab and
 *  the rest of the line, or nothing (an empty item). Only one separator
 *  character is ever consumed, so extra leading spaces on the first line
 *  stay part of the content, which keeps "how far must a continuation line
 *  be indented" to a single number. */
const LIST_MARKER_RE = /^( {0,3})(?:([-*+])|(\d{1,9})([.)]))(?:[ \t](.*)|)$/;

function isTableDelimRow(line) {
  return /^ {0,3}\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/.test(line) && line.includes("-");
}

/** isBlockStart says whether a line would begin a new block even in the
 *  middle of a paragraph: a fence, a heading, a rule, a quote, a list item, or
 *  a table's own header row. It is what makes a paragraph stop absorbing lines
 *  rather than swallow whatever comes after it. */
function isBlockStart(line, nextLine) {
  if (/^ {0,3}(`{3,}|~{3,})/.test(line)) return true;
  if (/^ {0,3}#{1,6}(?:[ \t]|$)/.test(line)) return true;
  if (/^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/.test(line)) return true;
  if (/^ {0,3}>/.test(line)) return true;
  if (LIST_MARKER_RE.test(line)) return true;
  if (nextLine !== undefined && line.includes("|") && isTableDelimRow(nextLine)) return true;
  return false;
}

/** parseBlocks reads a run of lines (a whole reply, or the dedented lines of a
 *  quote or a list item) into a tree of blocks. Recursive: a quote's body and a
 *  list item's body are each parsed the same way, which is how nested quotes
 *  and nested lists happen without special-casing. depth counts the nesting. */
export function parseBlocks(lines, depth = 0) {
  if (depth > MAX_DEPTH) return [{ type: "para", text: lines.join("\n") }];
  const blocks = [];
  let i = 0;
  while (i < lines.length) {
    if (!lines[i].trim()) { i++; continue; }

    let m = /^ {0,3}(`{3,}|~{3,})[ \t]*(.*)$/.exec(lines[i]);
    if (m) {
      const fenceChar = m[1][0];
      const fenceLen = m[1].length;
      const lang = m[2].trim().split(/\s+/)[0] || "";
      const closeRe = new RegExp("^ {0,3}" + fenceChar + "{" + fenceLen + ",}[ \\t]*$");
      const body = [];
      i++;
      while (i < lines.length && !closeRe.test(lines[i])) { body.push(lines[i]); i++; }
      if (i < lines.length) i++;
      blocks.push({ type: "code", lang, text: body.join("\n") });
      continue;
    }

    m = /^ {0,3}(#{1,6})(?:[ \t]+(.*))?$/.exec(lines[i]);
    if (m) {
      const text = (m[2] || "").trim().replace(/(?:^|[ \t])#+[ \t]*$/, "").trimEnd();
      blocks.push({ type: "heading", level: m[1].length, text });
      i++;
      continue;
    }

    if (/^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/.test(lines[i])) {
      blocks.push({ type: "hr" });
      i++;
      continue;
    }

    if (/^ {0,3}>/.test(lines[i])) {
      const body = [];
      while (i < lines.length && /^ {0,3}>/.test(lines[i])) {
        body.push(lines[i].replace(/^ {0,3}> ?/, ""));
        i++;
      }
      blocks.push({ type: "quote", blocks: parseBlocks(body, depth + 1) });
      continue;
    }

    if (LIST_MARKER_RE.test(lines[i])) {
      const r = parseList(lines, i, depth);
      blocks.push(r.block);
      i = r.next;
      continue;
    }

    if (i + 1 < lines.length && lines[i].includes("|") && isTableDelimRow(lines[i + 1])) {
      const r = parseTable(lines, i);
      blocks.push(r.block);
      i = r.next;
      continue;
    }

    if (/^ {4,}\S/.test(lines[i])) {
      const body = [];
      while (i < lines.length && (!lines[i].trim() || /^ {4,}/.test(lines[i]))) {
        body.push(lines[i].replace(/^ {4}/, ""));
        i++;
      }
      while (body.length && !body[body.length - 1].trim()) body.pop();
      blocks.push({ type: "code", lang: "", text: body.join("\n") });
      continue;
    }

    {
      const body = [lines[i]];
      i++;
      while (i < lines.length && lines[i].trim() && !isBlockStart(lines[i], lines[i + 1])) {
        body.push(lines[i]);
        i++;
      }
      blocks.push({ type: "para", text: body.join("\n") });
    }
  }
  return blocks;
}

/** parseList consumes every item of one list starting at lines[i]: the same kind
 *  of marker (ordered, or the same bullet character) for as long as it keeps
 *  appearing, each item's own lines gathered by how far they are indented past
 *  that item's marker, which is how a nested list or a second paragraph inside
 *  an item is told apart from the next item. */
function parseList(lines, i, depth) {
  const first = LIST_MARKER_RE.exec(lines[i]);
  const ordered = first[3] !== undefined;
  const bulletChar = ordered ? null : first[2];
  const start = ordered ? parseInt(first[3], 10) : 1;
  const items = [];
  let loose = false;

  while (i < lines.length) {
    const m = LIST_MARKER_RE.exec(lines[i]);
    if (!m) break;
    const mOrdered = m[3] !== undefined;
    if (mOrdered !== ordered || (!ordered && m[2] !== bulletChar)) break;

    const indent = m[1].length;
    const markerLen = ordered ? m[3].length + m[4].length : m[2].length;
    const contentCol = indent + markerLen + 1;
    const pad = " ".repeat(contentCol);
    const itemLines = [m[5] !== undefined ? m[5] : ""];
    i++;

    while (i < lines.length) {
      const line = lines[i];
      if (!line.trim()) {
        const next = lines[i + 1];
        if (next !== undefined && (next.startsWith(pad) || !next.trim())) { itemLines.push(""); i++; continue; }
        break;
      }
      if (line.startsWith(pad)) { itemLines.push(line.slice(contentCol)); i++; continue; }
      if (!isBlockStart(line, lines[i + 1])) { itemLines.push(line.trim()); i++; continue; }
      break;
    }
    while (itemLines.length && !itemLines[itemLines.length - 1].trim()) itemLines.pop();
    if (itemLines.some((l) => !l.trim())) loose = true;

    let checked = null;
    const tm = itemLines.length ? /^\[([ xX])\][ \t]+(.*)$/.exec(itemLines[0]) : null;
    if (tm) { checked = tm[1].toLowerCase() === "x"; itemLines[0] = tm[2]; }

    items.push({ checked, blocks: parseBlocks(itemLines, depth + 1) });

    if (i < lines.length && !lines[i].trim()) {
      let k = i;
      while (k < lines.length && !lines[k].trim()) k++;
      // Only a blank line before another item of this same list makes it
      // loose: one before a different list (or anything else) just ends this
      // one, tight or not.
      const mk = k < lines.length ? LIST_MARKER_RE.exec(lines[k]) : null;
      if (mk && (mk[3] !== undefined) === ordered && (ordered || mk[2] === bulletChar)) loose = true;
      i = k;
    }
  }
  return { block: { type: "list", ordered, start, loose, items }, next: i };
}

function splitTableRow(line) {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) {
    let backslashes = 0;
    for (let k = s.length - 2; k >= 0 && s[k] === "\\"; k--) backslashes++;
    if (backslashes % 2 === 0) s = s.slice(0, -1);
  }
  const cells = [];
  let cur = "";
  let esc = false;
  for (const ch of s) {
    if (esc) { cur += ch; esc = false; continue; }
    if (ch === "\\") { cur += ch; esc = true; continue; }
    if (ch === "|") { cells.push(cur.trim()); cur = ""; continue; }
    cur += ch;
  }
  cells.push(cur.trim());
  return cells.map((c) => c.replace(/\\\|/g, "|"));
}

function parseTable(lines, i) {
  const header = splitTableRow(lines[i]);
  const delims = splitTableRow(lines[i + 1]);
  const align = delims.map((d) => {
    const left = d.startsWith(":");
    const right = d.endsWith(":");
    return left && right ? "center" : right ? "right" : left ? "left" : null;
  });
  let j = i + 2;
  const rows = [];
  while (j < lines.length && lines[j].trim() && lines[j].includes("|")) {
    rows.push(splitTableRow(lines[j]));
    j++;
  }
  return { block: { type: "table", header, align, rows }, next: j };
}

// ------------------------------------------------------------------ inline

/** Tried in this order at each position: an escaped punctuation character, a
 *  code span (any run of backticks, closed by the same run, so a single
 *  backtick inside a doubled-backtick span stays literal), bold-italic, bold,
 *  strikethrough, italic, a link, an autolink, then a bare URL. Underscore
 *  emphasis requires a non-word character (or the edge of the text) on both
 *  sides, so snake_case_names are left alone; asterisk emphasis has no such
 *  restriction, matching how agents actually write it. */
const INLINE_SRC =
  "\\\\(?<esc>[!\"#$%&'()*+,\\-./:;<=>?@\\[\\]^_`{|}~\\\\])" +
  "|(?<codef>`+)(?<code>[\\s\\S]*?)\\k<codef>" +
  "|\\*\\*\\*(?<bi1>[^*]+)\\*\\*\\*" +
  "|(?<!\\w)___(?<bi2>[^_]+)___(?!\\w)" +
  "|\\*\\*(?<b1>[^*]+)\\*\\*" +
  "|(?<!\\w)__(?<b2>[^_]+)__(?!\\w)" +
  "|~~(?<strike>[^~]+)~~" +
  "|\\*(?<i1>[^*]+)\\*" +
  "|(?<!\\w)_(?<i2>[^_]+)_(?!\\w)" +
  "|\\[(?<ltext>[^\\]]*)\\]\\((?<lurl>[^)\\s]+)\\)" +
  "|<(?<auto>https?:\\/\\/[^\\s<>]+)>" +
  "|(?<bare>https?:\\/\\/[^\\s<>]+)";

function normalizeCodeSpan(s) {
  s = s.replace(/\n/g, " ");
  if (s.length > 1 && s[0] === " " && s[s.length - 1] === " " && s.trim() !== "") s = s.slice(1, -1);
  return s;
}

/** trimBareUrlTail gives back trailing punctuation a bare URL swept up by
 *  accident: a full stop after an address is not part of the link and stays the
 *  plain text that follows it, unless the punctuation is a closing paren
 *  balancing one inside the address. */
function trimBareUrlTail(url) {
  let tail = "";
  while (url.length) {
    const last = url[url.length - 1];
    if (!/[.,!?;:'")\]}]/.test(last)) break;
    if (last === ")") {
      const opens = (url.match(/\(/g) || []).length;
      const closes = (url.match(/\)/g) || []).length;
      if (opens >= closes) break;
    }
    tail = last + tail;
    url = url.slice(0, -1);
  }
  return { url, tail };
}

/** breakLines turns a literal "\n" in a piece of plain text into a line break,
 *  a soft break in the source Markdown kept as one rather than left for the
 *  browser to collapse into a space. */
function breakLines(s) {
  const parts = s.split("\n");
  if (parts.length === 1) return [s];
  const out = [];
  parts.forEach((p, idx) => {
    if (idx) out.push(h("br", null));
    if (p) out.push(p);
  });
  return out;
}

/** HTTP_URL is an address that may become a link: http or https, nothing else. */
const HTTP_URL = /^https?:[/][/]/i;

const link = (href, kids) => h("a", { href, target: "_blank", rel: "noopener noreferrer", "data-ctx": "link" }, ...kids);

/** HOST_TEXT is link text that reads as a host, or a host and a path: "github.com/org/repo". The labels may be letters of any alphabet, so a look-alike such as "gіthub.com" (Cyrillic і) is one, and is compared by the punycode host `new URL` gives it. The last label is letters, so a version such as "v1.2" is not one. */
const HOST_TEXT = /^[\p{L}\p{N}_-]+(\.[\p{L}\p{N}_-]+)*\.(\p{L}{2,})(\/\S*)?$/iu;

/** HOST_ENDINGS are the endings a bare name (no path) must have to read as a host, so a file name such as main.go, README.md or Node.js does not. */
const HOST_ENDINGS = new Set(["com", "org", "net", "io", "dev", "app", "ai", "co", "gov", "edu", "info", "me", "xyz", "cloud", "uk", "de", "fr", "nl", "eu", "us", "ca", "au", "ru", "cn", "jp", "in", "br", "it", "es", "se", "tv", "so"]);

/** looksLikeHost says link text reads as a web address without its scheme. */
function looksLikeHost(t) {
  const m = HOST_TEXT.exec(t);
  return !!m && (m[3] !== undefined || HOST_ENDINGS.has(m[2].toLowerCase()));
}

/** disguisedHost is the target's host when the link's text is itself a web
 *  address, or looks like a host or a host and a path, for another host, so the
 *  real destination is shown beside it; "" when the text is not an address or
 *  names the same host. */
function disguisedHost(text, target) {
  const t = String(text || "").trim();
  const address = HTTP_URL.test(t) ? t : looksLikeHost(t) ? "https://" + t : "";
  if (!address) return "";
  try {
    const bare = (host) => host.toLowerCase().replace(/^www\./, "");
    const shown = bare(new URL(address).host);
    const real = new URL(target).host.toLowerCase();
    return shown === bare(real) ? "" : real;
  } catch {
    return "";
  }
}

/** renderInline turns one run of text into nodes and strings, safely: every
 *  branch below builds a node with h() or pushes a plain string, so nothing is
 *  parsed as HTML. A fresh RegExp is used on every call because bold, italic,
 *  strikethrough and link text are rendered by calling this function again on
 *  their own contents, and a shared regex's position would be clobbered by that
 *  inner call. */
export function renderInline(text, depth = 0) {
  if (text.length > MAX_INLINE || depth > MAX_INLINE_DEPTH) return breakLines(text);
  const re = new RegExp(INLINE_SRC, "g");
  const out = [];
  let last = 0;
  const pushPlain = (s) => { if (s) out.push(...breakLines(s)); };
  const inner = (s) => renderInline(s, depth + 1);
  let m;
  while ((m = re.exec(text))) {
    pushPlain(text.slice(last, m.index));
    const g = m.groups;
    if (g.esc !== undefined) out.push(g.esc);
    else if (g.code !== undefined) out.push(h("code", null, normalizeCodeSpan(g.code)));
    else if (g.bi1 !== undefined) out.push(h("strong", null, h("em", null, ...inner(g.bi1))));
    else if (g.bi2 !== undefined) out.push(h("strong", null, h("em", null, ...inner(g.bi2))));
    else if (g.b1 !== undefined) out.push(h("strong", null, ...inner(g.b1)));
    else if (g.b2 !== undefined) out.push(h("strong", null, ...inner(g.b2)));
    else if (g.strike !== undefined) out.push(h("del", null, ...inner(g.strike)));
    else if (g.i1 !== undefined) out.push(h("em", null, ...inner(g.i1)));
    else if (g.i2 !== undefined) out.push(h("em", null, ...inner(g.i2)));
    else if (g.lurl !== undefined) {
      if (HTTP_URL.test(g.lurl)) {
        out.push(link(g.lurl, g.ltext ? inner(g.ltext) : [g.lurl]));
        const real = disguisedHost(g.ltext, g.lurl);
        if (real) out.push(h("span", { class: "md-linkhost" }, " (" + real + ")"));
      }
      else out.push(m[0]);
    } else if (g.auto !== undefined) {
      out.push(link(g.auto, [g.auto]));
    } else if (g.bare !== undefined) {
      const { url, tail } = trimBareUrlTail(g.bare);
      out.push(link(url, [url]));
      pushPlain(tail);
    }
    last = re.lastIndex;
  }
  pushPlain(text.slice(last));
  return out;
}

// ------------------------------------------------------------------- code

/** Aliases for the language token a fence names to the small tokenizer that
 *  knows it; anything else is left as plain, unhighlighted text. */
const LANG_ALIASES = {
  go: "go", golang: "go",
  js: "js", jsx: "js", mjs: "js", cjs: "js", javascript: "js", ts: "js", tsx: "js", typescript: "js",
  json: "json",
  py: "python", python: "python",
  sh: "shell", bash: "shell", shell: "shell", zsh: "shell",
  yml: "yaml", yaml: "yaml",
  html: "html", htm: "html", xml: "html",
  css: "css", scss: "css", less: "css",
  sql: "sql",
  diff: "diff", patch: "diff",
};

const LANG_SPECS = {
  go: {
    comment: "//[^\\n]*|/\\*[\\s\\S]*?\\*/",
    string: "`[^`]*`|\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'",
    number: "\\b\\d+(?:\\.\\d+)?\\b",
    keywords: "func package import var const type struct interface map chan go defer return if else for range switch case default break continue nil true false iota select fallthrough goto".split(" "),
  },
  js: {
    comment: "//[^\\n]*|/\\*[\\s\\S]*?\\*/",
    string: "`(?:[^`\\\\]|\\\\.)*`|\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'",
    number: "\\b\\d+(?:\\.\\d+)?\\b",
    keywords: "function const let var return if else for while do switch case break continue class extends new this typeof instanceof in of try catch finally throw async await import export from default null undefined true false interface type enum implements public private protected readonly static void as".split(" "),
  },
  json: {
    comment: null,
    string: "\"(?:[^\"\\\\]|\\\\.)*\"",
    number: "-?\\b\\d+(?:\\.\\d+)?\\b",
    keywords: "true false null".split(" "),
  },
  python: {
    comment: "#[^\\n]*",
    string: "'''[\\s\\S]*?'''|\"\"\"[\\s\\S]*?\"\"\"|\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'",
    number: "\\b\\d+(?:\\.\\d+)?\\b",
    keywords: "def class import from as return if elif else for while in is not and or try except finally raise with lambda yield pass break continue global nonlocal None True False self async await".split(" "),
  },
  shell: {
    comment: "#[^\\n]*",
    string: "\"(?:[^\"\\\\]|\\\\.)*\"|'[^']*'",
    number: "\\b\\d+\\b",
    keywords: "if then else elif fi for in do done while until case esac function return exit local export echo set shift break continue trap readonly declare source".split(" "),
  },
  yaml: {
    comment: "#[^\\n]*",
    string: "\"(?:[^\"\\\\]|\\\\.)*\"|'[^']*'",
    number: "\\b-?\\d+(?:\\.\\d+)?\\b",
    keywords: "true false null yes no on off".split(" "),
  },
  css: {
    comment: "/\\*[\\s\\S]*?\\*/",
    string: "\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'",
    number: "#[0-9a-fA-F]{3,8}\\b|\\b\\d+(?:\\.\\d+)?(?:px|em|rem|%|vh|vw|s|ms)?\\b",
    keywords: [],
  },
  sql: {
    comment: "--[^\\n]*|/\\*[\\s\\S]*?\\*/",
    string: "'(?:[^'\\\\]|\\\\.)*'",
    number: "\\b\\d+(?:\\.\\d+)?\\b",
    keywords: "select from where insert into values update set delete join left right inner outer on group by order having limit as and or not null is in like create table alter drop index primary key foreign references default distinct union all case when then end".split(" "),
  },
};

function buildLangRegex(spec) {
  const parts = [];
  if (spec.comment) parts.push("(?<com>" + spec.comment + ")");
  parts.push("(?<str>" + spec.string + ")");
  if (spec.keywords.length) parts.push("\\b(?<kw>" + spec.keywords.join("|") + ")\\b");
  parts.push("(?<num>" + spec.number + ")");
  return new RegExp(parts.join("|"), "gi");
}

function highlightGeneric(code, spec) {
  const re = buildLangRegex(spec);
  const out = [];
  let last = 0;
  let m;
  while ((m = re.exec(code))) {
    if (m.index > last) out.push(code.slice(last, m.index));
    const g = m.groups;
    const cls = g.com !== undefined ? "tok-com" : g.str !== undefined ? "tok-str" : g.kw !== undefined ? "tok-kw" : "tok-num";
    out.push(h("span", { class: cls }, m[0]));
    last = re.lastIndex;
  }
  if (last < code.length) out.push(code.slice(last));
  return out;
}

function highlightHtml(code) {
  const re = /<!--[\s\S]*?-->|<\/?[a-zA-Z][^>]*>/g;
  const out = [];
  let last = 0;
  let m;
  while ((m = re.exec(code))) {
    if (m.index > last) out.push(code.slice(last, m.index));
    out.push(h("span", { class: m[0].startsWith("<!--") ? "tok-com" : "tok-tag" }, m[0]));
    last = re.lastIndex;
  }
  if (last < code.length) out.push(code.slice(last));
  return out;
}

function highlightDiffLang(code) {
  const lines = code.split("\n");
  const out = [];
  lines.forEach((line, idx) => {
    const cls = /^\+(?!\+\+)/.test(line) ? "tok-diff-add" : /^-(?!--)/.test(line) ? "tok-diff-del"
      : /^@@/.test(line) ? "tok-diff-hunk" : null;
    out.push(cls ? h("span", { class: cls }, line) : line);
    if (idx < lines.length - 1) out.push("\n");
  });
  return out;
}

/** MAX_HIGHLIGHT is the longest code that is highlighted; longer is plain. */
export const MAX_HIGHLIGHT = 20000;

/** highlightCode fails safe to plain text: an unrecognised language, code over
 *  MAX_HIGHLIGHT, or a tokenizer that trips on something odd all just show the
 *  code as it was written, never as markup. */
function highlightCode(code, lang) {
  try {
    if (code.length > MAX_HIGHLIGHT) return [code];
    const key = LANG_ALIASES[String(lang || "").toLowerCase()];
    if (key === "html") return highlightHtml(code);
    if (key === "diff") return highlightDiffLang(code);
    const spec = key && LANG_SPECS[key];
    if (spec) return highlightGeneric(code, spec);
  } catch { /* fall through to plain text below */ }
  return [code];
}

/** A block over COLLAPSE_LINES folds to its first COLLAPSE_SHOW_LINES, behind a
 *  "Show all N lines" button. */
export const COLLAPSE_LINES = 15;
export const COLLAPSE_SHOW_LINES = 12;

/** copyText puts text on the clipboard and says whether it did. */
export async function copyText(text, nav = typeof navigator === "undefined" ? null : navigator) {
  try {
    if (!nav || !nav.clipboard || !nav.clipboard.writeText) return false;
    await nav.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

/** CodeBlock is a fenced or indented block: its language, Wrap and Copy, and a
 *  fold over COLLAPSE_LINES. Copy always copies the whole block, whatever the
 *  fold hides. */
export function CodeBlock({ lang, code, copy = copyText }) {
  const lines = code.split("\n");
  const collapsible = lines.length > COLLAPSE_LINES;
  const [expanded, setExpanded] = useState(false);
  const [wrap, setWrap] = useState(false);
  const [copied, setCopied] = useState("");
  const folded = collapsible && !expanded;
  const shown = folded ? lines.slice(0, COLLAPSE_SHOW_LINES).join("\n") : code;
  const doCopy = () => {
    copy(code).then((ok) => {
      setCopied(ok ? "Copied" : "Couldn't copy");
      setTimeout(() => setCopied(""), 1500);
    });
  };
  return h("div", { class: "md-code-block" },
    h("div", { class: "md-code-bar" },
      h("span", { class: "md-code-lang" }, lang || "text"),
      h("div", { class: "md-code-actions" },
        h("button", { class: "md-code-wrap", type: "button", "aria-pressed": wrap ? "true" : "false", onClick: () => setWrap(!wrap) }, "Wrap"),
        h("button", { class: "md-code-copy", type: "button", onClick: doCopy }, copied || "Copy"))),
    h("pre", { class: "md-code" + (folded ? " md-clamped" : "") + (wrap ? " wrap" : "") }, h("code", null, ...highlightCode(shown, lang))),
    collapsible
      ? h("button", { class: "md-code-more", type: "button", onClick: () => setExpanded(!expanded) }, expanded ? "Show less" : `Show all ${lines.length} lines`)
      : null);
}

// ------------------------------------------------------------------ blocks

function renderList(b) {
  const items = b.items.map((item) => {
    const kids = [];
    if (item.checked !== null) kids.push(h("span", { class: "md-task-box", "aria-hidden": "true" }, item.checked ? "☑" : "☐"));
    kids.push(...item.blocks.map(renderBlock));
    return h("li", { class: "md-li" + (item.checked !== null ? " md-task" : "") }, ...kids);
  });
  const cls = "md-list " + (b.loose ? "md-list-loose" : "md-list-tight");
  return b.ordered
    ? h("ol", { class: cls, start: b.start !== 1 ? String(b.start) : null }, ...items)
    : h("ul", { class: cls }, ...items);
}

const alignClass = (a) => (a ? "md-align-" + a : null);

function renderTable(b) {
  const thead = h("thead", null, h("tr", null, ...b.header.map((cell, i) =>
    h("th", { class: alignClass(b.align[i]) }, ...renderInline(cell)))));
  const tbody = h("tbody", null, ...b.rows.map((row) =>
    h("tr", null, ...row.map((cell, i) => h("td", { class: alignClass(b.align[i]) }, ...renderInline(cell))))));
  return h("div", { class: "md-table-scroll" }, h("table", { class: "md-table" }, thead, tbody));
}

function renderBlock(b) {
  if (b.type === "heading") return h("h" + b.level, { class: "md-h md-h" + b.level }, ...renderInline(b.text));
  if (b.type === "para") return h("p", { class: "md-p" }, ...renderInline(b.text));
  if (b.type === "code") return h(CodeBlock, { lang: b.lang, code: b.text });
  if (b.type === "hr") return h("hr", { class: "md-hr" });
  if (b.type === "quote") return h("blockquote", { class: "md-quote" }, ...b.blocks.map(renderBlock));
  if (b.type === "list") return renderList(b);
  if (b.type === "table") return renderTable(b);
  return h("p", null);
}

/** renderMarkdown turns Markdown into an array of nodes. See the module comment
 *  for what is understood and what safety it keeps. */
export function renderMarkdown(text) {
  const src = String(text == null ? "" : text).replace(/\r\n/g, "\n");
  return parseBlocks(src.split("\n")).map(renderBlock);
}

/** Markdown is the component: a div holding the text as Markdown. */
export function Markdown({ text }) {
  const chips = useContext(ChipsContext);
  const nodes = renderMarkdown(text);
  return h("div", { class: "md" }, ...(chips ? chipify(nodes, chips) : nodes));
}
