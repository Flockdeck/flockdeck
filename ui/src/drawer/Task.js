// The task detail panel in the drawer: what the task a pane works on says.
//
// It opens from the task chip in the drawer's header. On the desk it asks the
// desktop for the task (taskGet) and shows its title, state, owner, branch, who
// made it and when it last changed, its text, and its notes. "Go to pane" opens
// the pane that owns the task, when that pane is on the roster.
//
// Task text and notes are written by agents and people. They are drawn as text
// (pre-wrapped), never as markup. A window reached through the relay is not sent
// task text: it shows the chip's id, title and state only, and says the rest is
// on the desktop. The notes list is bounded: NOTES_FIRST are drawn, and a button
// draws NOTES_MORE more at a time.
//
// The task data has no dependencies, so none are shown.
import { useEffect, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";

export const NOTES_FIRST = 20;
export const NOTES_MORE = 50;
export const MAX_BODY = 20000;
export const MAX_NOTE = 4000;
export const ON_DESKTOP_WORDS = "The task's text, notes and owner are on the desktop.";
export const LOADING_WORDS = "Loading the task…";
export const NO_OWNER_WORDS = "Nobody yet";
export const NOT_ASKED_WORDS = "Not connected to Flockdeck, so the task was not asked for.";

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));

function Notes({ notes, count }) {
  const [shown, setShown] = useState(NOTES_FIRST);
  const list = Array.isArray(notes) ? notes : [];
  if (list.length === 0) return count > 0 ? html`<p class="task-empty">${count} notes are not in this view.</p>` : html`<p class="task-empty">No notes.</p>`;
  const drawn = list.slice(0, shown);
  const left = list.length - drawn.length;
  return html`<ol class="task-notes" aria-label="Notes">
      ${drawn.map((n, i) => html`<li key=${i} class="task-note">
        <p class="task-note-head"><span class="task-note-who">${clip(text(n && n.author && n.author.name) || "unknown", 80)}</span> <span class="task-note-time">${clip(text(n && n.time), 40)}</span></p>
        <p class="task-note-text">${clip(text(n && n.text), MAX_NOTE)}</p>
      </li>`)}
    </ol>
    ${left > 0 ? html`<button type="button" class="drawer-btn task-more" onClick=${() => setShown((n) => n + NOTES_MORE)}>${"Show " + Math.min(NOTES_MORE, left) + " more notes (" + left + " not drawn yet)"}</button>` : null}`;
}

/** Task is the panel. node is the graph node whose chip opened it (node.task is
 *  { id, title, state }); snap is the store's snapshot, store the store; onOpenPane
 *  opens another pane's drawer; onClose hides the panel. */
export function Task({ node, snap, store, onOpenPane = null, onClose }) {
  const chip = node.task;
  const id = chip && chip.id;
  const remote = !!snap.remote;
  const [note, setNote] = useState("");
  useEffect(() => {
    if (!id || remote) return undefined;
    setNote(store.requestTask(id) ? "" : NOT_ASKED_WORDS);
    return () => store.clearTask();
  }, [store, id, remote]);
  if (!chip) return null;
  const d = !remote && snap.taskDetail && snap.taskDetail.id === id ? snap.taskDetail : null;
  const t = d && d.state === "ok" ? d.task : null;
  const ownerPane = t && t.ownerOpen && t.ownerPane && t.ownerPane !== node.pane ? t.ownerPane : "";
  return html`<section class="task" role="region" aria-label="Task" data-task=${id}>
    <header class="task-head">
      <h3 class="task-title"><span class="task-id">${clip(text(chip.id), 20)}</span> ${clip(text((t && t.title) || chip.title), 300)}</h3>
      <span class="task-state">${clip(text((t && t.state) || chip.state), 40)}</span>
      <button type="button" class="drawer-btn task-close" onClick=${onClose}>Hide task</button>
    </header>
    <p class="task-empty" role="status">${remote ? ON_DESKTOP_WORDS : !t && d && d.state === "loading" ? LOADING_WORDS : ""}</p>
    ${!remote && d && d.state === "error" ? html`<p class="task-empty task-error" role="alert">${clip(text(d.error), 300)}</p>` : null}
    ${!remote && !d && note ? html`<p class="task-empty task-error" role="alert">${note}</p>` : null}
    ${t
      ? html`<dl class="task-facts">
            <div class="fact"><dt>Owner</dt><dd>${clip(text(t.owner) || NO_OWNER_WORDS, 120)}</dd></div>
            ${t.branch ? html`<div class="fact"><dt>Branch</dt><dd>${clip(text(t.branch), 200)}</dd></div>` : null}
            ${t.creator && t.creator.name ? html`<div class="fact"><dt>Made by</dt><dd>${clip(text(t.creator.name), 80)}</dd></div>` : null}
            ${t.updated ? html`<div class="fact"><dt>Changed</dt><dd>${clip(text(t.updated), 40)}</dd></div>` : null}
          </dl>
          ${ownerPane
            ? html`<button type="button" class="drawer-btn task-goto" onClick=${() => onOpenPane && onOpenPane(ownerPane)}>Go to pane${t.ownerRole ? " (" + clip(text(t.ownerRole), 40) + ")" : ""}</button>`
            : null}
          ${t.body ? html`<div class="task-body" aria-label="Task text"><p class="task-text">${clip(text(t.body), MAX_BODY)}</p></div>` : html`<p class="task-empty">This task has no text.</p>`}
          <h4 class="task-notes-title">Notes${typeof t.notes === "number" ? " (" + t.notes + ")" : ""}</h4>
          <${Notes} notes=${t.noteList} count=${t.notes || 0} />`
      : null}
  </section>`;
}
