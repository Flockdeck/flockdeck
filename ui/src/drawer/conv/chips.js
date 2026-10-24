// Helper names in a conversation, drawn as chips that open that agent's drawer.
// The manager's replies name its helpers ("planner is done, api owns T4"); with
// the team's names in a context, each whole-word occurrence in plain text becomes
// a button. Code, code blocks, links and buttons are left as written, and a name
// shorter than MIN_NAME is not a chip, so a helper called "a" does not turn every
// article into one.
//
// Names are what agents and people called their panes: they are put in a pattern
// escaped, and the chip's text is the text found, never markup.
import { h, createContext } from "../../vendor/preact.mjs";

/** ChipsContext holds { names: [{ pane, name }], open(pane) }, or null for no chips. */
export const ChipsContext = createContext(null);

export const MIN_NAME = 3;
export const MAX_NAMES = 60;

const SKIP = new Set(["code", "pre", "a", "button"]);

const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, (c) => "\\" + c);

/** patternOf is the regex that finds any of the names as whole words, or null when none can be a chip. Longer names come first, so "api-gateway" wins over "api". */
export function patternOf(names) {
  const usable = [];
  const seen = new Set();
  for (const n of names || []) {
    const name = n && typeof n.name === "string" ? n.name.trim() : "";
    if (name.length < MIN_NAME || seen.has(name) || !n.pane) continue;
    seen.add(name);
    usable.push(name);
    if (usable.length >= MAX_NAMES) break;
  }
  if (usable.length === 0) return null;
  usable.sort((a, b) => b.length - a.length);
  return new RegExp("(?<![\\w-])(" + usable.map(escapeRe).join("|") + ")(?![\\w-])", "g");
}

/** splitText is a string as strings and chips. */
function splitText(text, re, byName, open) {
  const out = [];
  let last = 0;
  re.lastIndex = 0;
  let m;
  while ((m = re.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const pane = byName.get(m[1]);
    out.push(h("button", { type: "button", class: "conv-chip", title: "Open " + m[1], onClick: () => open(pane) }, m[1]));
    last = m.index + m[1].length;
  }
  if (out.length === 0) return [text];
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/** chipify returns the nodes with the helper names in their text made chips. */
export function chipify(nodes, chips) {
  const re = chips && patternOf(chips.names);
  if (!re) return nodes;
  const byName = new Map(chips.names.filter((n) => n && n.pane && typeof n.name === "string").map((n) => [n.name.trim(), n.pane]));
  const walk = (list) => list.flatMap((c) => {
    if (typeof c === "string") return splitText(c, re, byName, chips.open);
    if (c && typeof c === "object" && typeof c.type === "string" && !SKIP.has(c.type) && c.props && c.props.children != null) {
      const kids = Array.isArray(c.props.children) ? c.props.children : [c.props.children];
      const { children, ...rest } = c.props;
      return [h(c.type, rest, ...walk(kids))];
    }
    return [c];
  });
  return walk(nodes);
}
