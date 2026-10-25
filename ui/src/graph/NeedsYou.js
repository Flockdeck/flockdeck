// The strip above the graph: every item that needs the person, one row each,
// in the order the graph gives them (model.needs; team.NeedsYou in the server is
// the one place that orders them, so nothing here sorts). A row has the pane's
// name, the reason and the first line of the item's text, so a person finds
// who needs them and why without searching the picture.
//
// An item for a pane that is not a node of the graph still has its row, under the
// pane's id, and no badge.
//
// Every text here is untrusted and is drawn as text.
import { html } from "../html.js";
import { Icon, reasonOf } from "./Badge.js";
import { clip } from "./Node.js";
import { shownName } from "./model.js";

/** The longest first line a row shows. */
const ROW_DETAIL = 40;

function nameOf(model, id) {
  const n = model.byPane.get(id);
  return n && shownName(n) ? shownName(n) : id;
}

/** ageOf is how long ago an item began, as "9m", "2h" or "3d", or "" when its time is not known (the graph writes year 1 for that) or is ahead of now. */
export function ageOf(time, now) {
  const t = Date.parse(time);
  if (!Number.isFinite(t) || t < Date.UTC(2000, 0, 1)) return "";
  const s = Math.floor((now - t) / 1000);
  if (s < 0) return "";
  if (s < 60) return "now";
  if (s < 3600) return Math.floor(s / 60) + "m";
  if (s < 86400) return Math.floor(s / 3600) + "h";
  return Math.floor(s / 86400) + "d";
}

/** rowOf is what the strip shows for one item: who, why and the first line. */
export function rowOf(model, need, now = Date.now()) {
  const r = reasonOf(need);
  const who = [nameOf(model, need.pane)];
  if (need.other) who.push(nameOf(model, need.other));
  return {
    id: need.id,
    pane: need.pane,
    kind: need.kind,
    shown: model.byPane.has(need.pane),
    who: who.join(" and "),
    word: r.word,
    detail: r.detail === "" ? "" : clip(r.detail, ROW_DETAIL),
    age: ageOf(need.time, now),
  };
}

/**
 * NeedsYou is the strip. model is a graph model. onSelect, when given, is called
 * with a pane id when its row is chosen, so that a window can open that pane; a
 * row for a pane the graph has no node for is not a button. At most max loud
 * rows (everything but old unread mail) are drawn, in the graph's order, and the
 * rest are counted; the quiet rows, old unread mail, follow them.
 */
export function NeedsYou({ model, onSelect, max = Infinity }) {
  const now = Date.now();
  const all = model.needs.map((n) => rowOf(model, n, now));
  const loud = all.filter((r) => r.kind !== "stale-unread");
  const quiet = all.filter((r) => r.kind === "stale-unread");
  const rows = loud.slice(0, max).concat(quiet);
  const hidden = model.teams.reduce((sum, t) => sum + t.needsHidden, 0) + model.soloNeedsHidden;
  const capped = Math.max(0, loud.length - max);
  if (all.length === 0 && hidden === 0) {
    return html`<section class="needs" aria-label="Needs you"><p class="needs-none">Nothing needs you.</p></section>`;
  }
  return html`
    <section class="needs" aria-label="Needs you">
      <h2 class="needs-head">Needs you (${all.length + hidden})</h2>
      <ol class="needs-list">
        ${rows.map((r, i) => {
          const body = html`
            <svg class="needs-icon" width="20" height="20" viewBox="-10 -10 20 20" aria-hidden="true"><${Icon} kind=${r.kind} x=${0} y=${0} /></svg>
            <span class="needs-who">${r.who}</span>
            ${r.age ? html`<span class="needs-age">${r.age}</span>` : null}
            <span class="needs-why">
              <span class="needs-word">${r.word}</span>
              ${r.detail ? html`<span class="needs-detail">${r.detail}</span>` : null}
            </span>
          `;
          return html`
            <li class=${"needs-row needs-row--" + r.kind + (r.kind === "stale-unread" ? " needs-row--quiet" : "")} data-pane=${r.pane} data-kind=${r.kind} key=${r.id || i}>
              ${onSelect && r.shown
                ? html`<button type="button" class="needs-pick" onClick=${() => onSelect(r.pane)}>${body}</button>`
                : html`<div class="needs-pick">${body}</div>`}
            </li>
          `;
        })}
      </ol>
      ${capped > 0 ? html`<p class="needs-more">and ${capped} more, marked on the graph</p>` : null}
      ${hidden > 0 ? html`<p class="needs-more">and ${hidden} more not shown</p>` : null}
    </section>
  `;
}
