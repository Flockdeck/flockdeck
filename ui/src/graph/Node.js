// What the outline, the drawer and the other views share about one pane: the state
// it is in (its mark and word), a text cut to length, and the changed-file words of
// its branch line. The pane's box on the old canvas is gone.
//
// Every text here came from an agent, a task or a message, so it is only ever a
// child of an element (drawn as text) or an attribute value (escaped), never markup.

/** The status words and marks. The mark's shape differs per state, so a status never rests on colour alone. */
const STATES = {
  working: { word: "working", shape: "circle" },
  idle: { word: "idle", shape: "ring" },
  waiting: { word: "waiting", shape: "diamond" },
  blocked: { word: "blocked", shape: "square" },
  starting: { word: "starting", shape: "dashed" },
  exited: { word: "down", shape: "cross" },
  failed: { word: "down", shape: "cross" },
};

/** stateOf says which state a node is drawn in: its activity, else its status. A value not in the table is drawn as unknown, with its own words. */
export function stateOf(node) {
  const key = Object.prototype.hasOwnProperty.call(STATES, node.activity) ? node.activity : node.status;
  if (Object.prototype.hasOwnProperty.call(STATES, key)) return { key, ...STATES[key] };
  return { key: "unknown", word: key && key !== "unknown" ? clip(key, 10) : "unknown", shape: "unknown" };
}

/** clip cuts a text to at most max characters, ending in an ellipsis. It counts code points, so a pair is not split. */
export function clip(text, max) {
  const chars = Array.from(String(text == null ? "" : text));
  if (chars.length <= max) return chars.join("");
  return chars.slice(0, Math.max(0, max - 1)).join("") + "…";
}

/** dirtyOf is the changed-file part of the branch line, or an empty string. */
export function dirtyOf(node) {
  const parts = [];
  if (node.dirty > 0) parts.push(node.dirty + " changed");
  if (node.untracked > 0) parts.push(node.untracked + " new");
  return parts.join(", ");
}
