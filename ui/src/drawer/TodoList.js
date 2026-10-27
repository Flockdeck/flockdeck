// The agent's plan, drawn as a compact checklist above the conversation of a
// structured pane. It is closed with one line (how many are done and the row in
// progress) and opens to every row with its status. It is open while something is
// left to do until the person closes it, and closed once all is done. The rows are
// the agent's words, drawn as text in a list of their own.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { todosOf, summaryText, STATUS_WORDS } from "./todoModel.js";

/** TodoList draws the plan of the push st; it draws nothing when the plan is empty.
 *  The person's choice to open or close it is kept until the pane changes. */
export function TodoList({ st, pane }) {
  const [choice, setChoice] = useState({ pane, open: null });
  const t = todosOf(st);
  if (!t.total) return null;
  const mine = choice.pane === pane ? choice.open : null;
  const open = mine === null ? t.done < t.total : mine;
  return html`
    <section class="structured-todos" aria-label="Plan from the agent" data-todos=${open ? "open" : "closed"}>
      <button type="button" class="structured-todos-head" aria-expanded=${open ? "true" : "false"} onClick=${() => setChoice({ pane, open: !open })}>
        <span class="structured-todos-title">Plan</span>
        <span class="structured-todos-sum">${clip(summaryText(t), 160)}</span>
      </button>
      ${open
        ? html`<ul class="structured-todos-list">
            ${t.rows.map((r, i) => html`<li key=${i} class="structured-todo" data-status=${r.status}>
              <span class="structured-todo-mark" aria-hidden="true"></span>
              <span class="sr-only">${STATUS_WORDS[r.status]}: </span><span class="structured-todo-text" dir="auto">${r.text}</span>
            </li>`)}
            ${t.more ? html`<li class="structured-todo structured-todo--more">${t.more === 1 ? "1 more not shown" : t.more + " more not shown"}</li>` : null}
          </ul>`
        : null}
    </section>`;
}
