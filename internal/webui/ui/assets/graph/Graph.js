// The team graph as one SVG: a frame for each team with its boxes inside, and
// the strip of panes in no team along the bottom. The positions come from
// layout.js and each box from Node.js. The edges are drawn by
// Edges.js, under the boxes. The needs-you marks and the drawer are other views
// over the same layout.
//
// Team goals, names and every other text are untrusted and are drawn as text.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { layoutGraph } from "./layout.js";
import { Node, clip } from "./Node.js";
import { Badge } from "./Badge.js";
import { NeedsYou } from "./NeedsYou.js";
import { Edges, EdgeDefs, EdgeNotice, countsOf, edgesOf, focusOf } from "./Edges.js";
import { navOf, onGraphKey } from "./keys.js";

/**
 * Graph draws a graph model. layout may be given when the caller has laid the
 * model out already (the same model always gives the same layout). onOpen, if
 * given, is called with a pane id when its box is clicked or opened from the
 * keyboard; open is the pane whose drawer is open.
 */
export function Graph({ model, layout = null, onOpen = null, open = "" }) {
  // The pane the pointer or the keyboard is on. Its lines are drawn, the panes it
  // has no line to fade, and the one it waits on is outlined; with nothing under
  // the pointer the open pane's lines are the ones shown.
  const [hot, setHot] = useState("");
  const lay = layout || layoutGraph(model);
  if (!lay.ok || lay.nodes.length === 0) {
    return html`<p class="graph-empty">No agents in this project yet.</p>`;
  }
  const edges = edgesOf(model, lay);
  const focus = hot && model.byPane.has(hot) ? hot : open;
  const near = focusOf(edges, focus);
  const counts = countsOf(model);
  const nav = navOf(model);
  const teamOf = new Map(model.teams.map((t) => [t.id, t]));
  return html`
    <svg
      class="graph"
      viewBox=${`0 0 ${lay.width} ${lay.height}`}
      width=${lay.width}
      height=${lay.height}
      role="group"
      aria-label=${"Team graph of " + (model.project || "this project")}
      onKeyDown=${onOpen ? (e) => onGraphKey(nav, e) : undefined}
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
      <${EdgeDefs} />
      <${Edges} e=${edges} focus=${focus} />
      ${lay.nodes.map((b) => {
        const node = model.byPane.get(b.pane);
        if (!node) return null;
        const needs = model.needsByPane.get(b.pane);
        return html`
          <g class="gslot" key=${b.pane}>
            <${Node}
              node=${node}
              box=${b}
              team=${teamOf.get(b.team) || null}
              onOpen=${onOpen}
              open=${open === b.pane}
              counts=${counts.get(b.pane) || null}
              dim=${!!focus && b.pane !== focus && !near.related.has(b.pane)}
              rel=${b.pane === near.target}
              onHot=${setHot}
            />
            ${needs && needs.length > 0
              ? html`<g class="gneed" data-pane=${b.pane} transform=${`translate(${b.x} ${b.y})`}><${Badge} needs=${needs} /></g>`
              : null}
          </g>
        `;
      })}
      <${EdgeNotice} e=${edges} width=${lay.width} />
    </svg>
  `;
}

/** GraphView is the strip of what needs the person above the graph, drawn from one model, so that a new model changes both in the same frame. */
export function GraphView({ model, layout = null, onSelect, onOpen = null, open = "" }) {
  return html`
    <div class="graph-view">
      <${NeedsYou} model=${model} onSelect=${onSelect} />
      <${Graph} model=${model} layout=${layout} onOpen=${onOpen} open=${open} />
    </div>
  `;
}
