// The team graph as one SVG: a frame for each team with its boxes inside, and
// the strip of panes in no team along the bottom. The positions come from
// layout.js and each box from Node.js. This view draws the boxes only; the
// edges, the needs-you marks and the drawer are other views over the same
// layout.
//
// Team goals, names and every other text are untrusted and are drawn as text.
import { html } from "../html.js";
import { layoutGraph } from "./layout.js";
import { Node, clip } from "./Node.js";

/**
 * Graph draws a graph model. layout may be given when the caller has laid the
 * model out already (the same model always gives the same layout).
 */
export function Graph({ model, layout = null }) {
  const lay = layout || layoutGraph(model);
  if (!lay.ok || lay.nodes.length === 0) {
    return html`<p class="graph-empty">No agents in this project yet.</p>`;
  }
  const teamOf = new Map(model.teams.map((t) => [t.id, t]));
  return html`
    <svg
      class="graph"
      viewBox=${`0 0 ${lay.width} ${lay.height}`}
      width=${lay.width}
      height=${lay.height}
      role="group"
      aria-label=${"Team graph of " + (model.project || "this project")}
    >
      ${lay.teams.map((f) => {
        const team = teamOf.get(f.id);
        const title = clip((team && team.goal) || "Team", 60);
        return html`
          <g class="gteam" data-team=${f.id} key=${f.id}>
            <rect class="gteam-frame" x=${f.x} y=${f.y} width=${f.w} height=${f.h} rx="10" />
            <text class="gteam-title" x=${f.x + 16} y=${f.y + 24}>${title}</text>
          </g>
        `;
      })}
      ${lay.solo
        ? html`
            <g class="gteam gteam--solo">
              <rect class="gteam-frame" x=${lay.solo.x} y=${lay.solo.y} width=${lay.solo.w} height=${lay.solo.h} rx="10" />
              <text class="gteam-title" x=${lay.solo.x + 16} y=${lay.solo.y + 22}>Not in a team</text>
            </g>
          `
        : null}
      ${lay.nodes.map((b) => {
        const node = model.byPane.get(b.pane);
        if (!node) return null;
        return html`<${Node} key=${b.pane} node=${node} box=${b} team=${teamOf.get(b.team) || null} />`;
      })}
    </svg>
  `;
}
