// The keyboard in the team graph, with no pointer.
//
// - The arrows move between boxes along the tree: Up to the agent's manager,
//   Down to its first helper, Left and Right to the agent beside it in the same
//   row of helpers. Enter opens the drawer and Esc closes it (Node.js and
//   Drawer.js own those two).
// - Left and Right wrap: past the last agent beside it is the first, and the
//   other way round. Up from the top of a tree and Down from a leaf stay where
//   they are, since there is nowhere further in that direction.
// - The managers of the teams are one row, in the teams' order. The panes in no
//   team are a row of their own, and so are a team's members the tree does not
//   reach (Up from one of those goes to its team's manager).
// - Two actions of the key table reach what no arrow does: the next agent that
//   needs the person, in the order of the strip above the graph, and the manager
//   bar. The bindings come from the hello's table (shell/keys.js).
//
// Everything here is a function of the model, with no DOM except focus, and
// nothing recurses, so a chain of any length is walked.
import { LIST, composing } from "../shell/keys.js";
import { panesNeedingYou } from "./model.js";
import { focusNode } from "../drawer/state.js";

/** navOf is the graph's keyboard map, from a model: for each pane its parent, its
 *  first child and the row of panes it stands in. */
export function navOf(model) {
  const parent = new Map();
  const first = new Map();
  const row = new Map(); // pane -> { list, at }
  const addRow = (panes) => {
    panes.forEach((pane, at) => {
      if (!row.has(pane)) row.set(pane, { list: panes, at });
    });
  };
  const roots = [];
  for (const t of model.teams) {
    if (t.tree === null) continue;
    roots.push(t.tree.pane);
    const stack = [t.tree];
    while (stack.length > 0) {
      const f = stack.pop();
      if (f.children.length > 0) {
        first.set(f.pane, f.children[0].pane);
        addRow(f.children.map((c) => c.pane));
      }
      for (const c of f.children) {
        parent.set(c.pane, f.pane);
        stack.push(c);
      }
    }
  }
  addRow(roots);
  for (const t of model.teams) {
    const loose = t.unlinked.map((n) => n.pane);
    addRow(loose);
    if (t.tree !== null) for (const p of loose) parent.set(p, t.tree.pane);
  }
  const placed = new Set(row.keys());
  const rest = [];
  for (const n of model.solo) if (!placed.has(n.pane)) rest.push(n.pane);
  for (const n of model.nodes) if (!placed.has(n.pane) && !rest.includes(n.pane)) rest.push(n.pane);
  addRow(rest);
  return { parent, first, row };
}

/** stepFrom is the pane an arrow key moves to from a pane, or "" when the key
 *  is not an arrow or there is nowhere to go. */
export function stepFrom(nav, pane, key) {
  switch (key) {
    case LIST.up:
      return nav.parent.get(pane) || "";
    case LIST.down:
      return nav.first.get(pane) || "";
    case LIST.left:
    case LIST.right: {
      const r = nav.row.get(pane);
      if (!r || r.list.length < 2) return "";
      const n = r.list.length;
      return r.list[(r.at + (key === LIST.right ? 1 : n - 1)) % n];
    }
    default:
      return "";
  }
}

/** nextNeedsYou is the pane the jump goes to from a pane: the next one in the
 *  strip's order, wrapping from the last to the first. From a pane the strip
 *  does not hold (or none) it is the first. A pane in the strip with no box in the
 *  graph is skipped. "" when nothing needs the person. */
export function nextNeedsYou(model, from) {
  const list = panesNeedingYou(model).filter((p) => model.byPane.has(p));
  if (list.length === 0) return "";
  const at = list.indexOf(from);
  return list[at < 0 ? 0 : (at + 1) % list.length];
}

/** prevNeedsYou is the same walk the other way: the one before in the strip's
 *  order, wrapping from the first to the last. From a pane the strip does not
 *  hold (or none) it is the last. */
export function prevNeedsYou(model, from) {
  const list = panesNeedingYou(model).filter((p) => model.byPane.has(p));
  if (list.length === 0) return "";
  const at = list.indexOf(from);
  return list[at < 0 ? list.length - 1 : (at + list.length - 1) % list.length];
}

/** paneAt is the pane id of an element in the graph, or of the nearest one
 *  holding it, or "". */
export function paneAt(el) {
  for (let n = el; n; n = n.parentNode) {
    const id = n.getAttribute && n.getAttribute("data-pane");
    if (id) return id;
  }
  return "";
}

/** onGraphKey is the keydown handler of the graph's svg: an arrow on a box
 *  moves the keyboard to the box it leads to. Any other key, an arrow with a
 *  modifier, and an arrow while an input method is composing are left alone. */
export function onGraphKey(nav, e) {
  if (e.defaultPrevented || composing(e) || e.ctrlKey || e.altKey || e.shiftKey || e.metaKey) return false;
  const target = e.target;
  if (!target || !target.getAttribute || target.getAttribute("role") !== "button") return false;
  const to = stepFrom(nav, target.getAttribute("data-pane") || "", e.key);
  if (!to) return false;
  // The key is kept only when a box took it, so a target that cannot be focused
  // does not swallow it.
  const moved = focusNode(e.currentTarget, to);
  if (moved) e.preventDefault();
  return moved;
}
