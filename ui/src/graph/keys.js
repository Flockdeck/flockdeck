// The jumps between agents that need the person, and the pane an element belongs to.
//
// Two actions of the key table reach the next agent that needs the person, in the
// order of the outline's strip, and the manager bar. The bindings come from the
// hello's table (shell/keys.js). Everything here is a function of the model, with
// no DOM except the walk up from an element.
import { panesNeedingYou } from "./model.js";

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
