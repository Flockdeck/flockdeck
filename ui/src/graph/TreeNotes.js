// What the old Needs you column showed that the outline does not: the agents with
// mail nobody read for a while (quiet, so no row of their own is tinted for it), and
// the other teams of the project with how many of their agents need the person.
// Each is one line of buttons above the outline that open that agent's drawer;
// a line with nothing to say is not drawn.
//
// Every name is from an agent or a person and is drawn as text.
import { html } from "../html.js";
import { ageOf } from "./NeedsYou.js";
import { clip } from "./Node.js";
import { shownName } from "./model.js";
import { summaryOf, words } from "./Teams.js";

/** notesOf is { unread: [{ pane, name, age }], teams: [{ id, name, pane, words, needs }] } from a model. teams has the lines of Teams.js when the project has two or more, else is empty. */
export function notesOf(model, now = Date.now()) {
  const unread = model.needs
    .filter((n) => n.kind === "stale-unread" && model.byPane.has(n.pane))
    .map((n) => ({ pane: n.pane, name: shownName(model.byPane.get(n.pane)), age: ageOf(n.time, now) }));
  const lines = summaryOf(model);
  const teams = lines.length >= 2 ? lines.map((s) => ({ id: s.id, name: s.name, pane: s.pane, words: words(s), needs: s.needs })) : [];
  return { unread, teams };
}

/** TreeNotes draws the lines. onOpen(pane) opens a drawer. */
export function TreeNotes({ model, onOpen = null, now = Date.now() }) {
  if (!model || !model.ok) return null;
  const { unread, teams } = notesOf(model, now);
  if (unread.length === 0 && teams.length === 0) return null;
  const pick = (pane, body, cls) =>
    onOpen && pane
      ? html`<button type="button" class=${"tree-line-pick" + cls} onClick=${() => onOpen(pane)}>${body}</button>`
      : html`<span class=${"tree-line-pick" + cls}>${body}</span>`;
  return html`<div class="tree-notes">
    ${unread.length > 0
      ? html`<p class="tree-line" data-note="unread"><span class="tree-line-label">Unread mail</span>
          ${unread.map((u) => pick(u.pane, html`${clip(u.name, 24)}${u.age ? html` <span class="tree-line-age">${u.age}</span>` : null}`, ""))}</p>`
      : null}
    ${teams.length > 0
      ? html`<p class="tree-line" data-note="teams"><span class="tree-line-label">Teams</span>
          ${teams.map((t) => pick(t.pane, html`${t.name} <span class=${"tree-line-age" + (t.needs > 0 ? " tree-line-age--needs" : "")}>${t.words}</span>`, ""))}</p>`
      : null}
  </div>`;
}
