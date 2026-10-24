// What the outline puts under a row: the decision blocks of that agent
// (DecisionBlock.js). The rows of the outline are flat siblings with role treeitem,
// so the blocks sit in a wrapper that is not part of the tree's own structure
// (role presentation). The row whose drawer is open does not repeat its permission,
// question or down: the drawer shows them.
import { html } from "../html.js";
import { Decisions, decisionsOf } from "./DecisionBlock.js";

export const OPEN_SKIPS = ["permission", "ask", "down"];

/** TreeDecisions draws the blocks under the row of pane, or nothing when it has none to show. ctx is the one DecisionBlock.js takes. */
export function TreeDecisions({ model, pane, open = "", stop = false, onGone = null, ctx = {} }) {
  const skip = open === pane ? OPEN_SKIPS : [];
  if (decisionsOf(model, pane).every((n) => skip.includes(n.kind))) return null;
  return html`<div class="tree-decs" role="presentation" data-pane=${pane}><${Decisions} model=${model} pane=${pane} skip=${skip} ctx=${{ ...ctx, tab: stop, onGone }} /></div>`;
}
