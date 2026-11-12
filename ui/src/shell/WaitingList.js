// The list behind a rail project's orange count: the panes of the open project
// whose activity is waiting or blocked, which is what the server counted for the
// badge. The list is drawn from the state push and the count from the same
// project's tally, so a push in between can show them apart for a moment; the
// heading draws the list's own length.
//
// Every text here came from an agent and is drawn as text.
import { html } from "../html.js";
import { ageOf } from "../graph/NeedsYou.js";
import { clip } from "../graph/Node.js";

const DETAIL_MAX = 40;
const firstLine = (s) => String(s || "").split(/\r?\n/).find((l) => l.trim() !== "") || "";

/** waitingOf lists the panes of a state push that wait on the person or are
 *  blocked, oldest first (a pane with no known start goes last, in the push's
 *  order), as { id, name, kind, word, detail, since }. */
export function waitingOf(panes) {
  const out = [];
  for (const p of panes && typeof panes === "object" ? Object.values(panes) : []) {
    if (!p || typeof p !== "object" || !p.id) continue;
    const state = p.activity || p.status;
    if (state !== "waiting" && state !== "blocked") continue;
    const q = p.ask && Array.isArray(p.ask.questions) && p.ask.questions[0];
    let kind = "waiting", word = "Waiting for input", detail = "";
    if (state === "blocked") {
      kind = "blocked"; word = "Blocked"; detail = firstLine(p.detail);
    } else if (p.permission && typeof p.permission === "object") {
      kind = "permission"; word = "Permission"; detail = firstLine(p.permission.summary || p.permission.tool);
    } else if (q && typeof q === "object") {
      kind = "ask"; word = "Question"; detail = firstLine(q.question);
    }
    const t = typeof p.statusSince === "string" ? Date.parse(p.statusSince) : NaN;
    const known = Number.isFinite(t) && t > Date.UTC(2000, 0, 1);
    out.push({ id: String(p.id), name: String(p.name || p.id), kind, word, detail: clip(detail, DETAIL_MAX), since: known ? p.statusSince : "", at: known ? t : Infinity, order: out.length });
  }
  return out.sort((a, b) => (a.at === b.at ? a.order - b.order : a.at < b.at ? -1 : 1));
}

/** waitingWords is the badge's name and tooltip, for a count. */
export function waitingWords(n) {
  return n + (n === 1 ? " agent waiting for you" : " agents waiting for you");
}

/** WaitingList is the panel body. panes is the push's panes of the project, or
 *  null while that project's state has not arrived. */
export function WaitingList({ panes, onOpen, now = Date.now() }) {
  if (panes === null) return html`<section class="needs" aria-label="Waiting for you"><p class="needs-none">Opening the project. The list is here once its panes arrive.</p></section>`;
  const rows = waitingOf(panes);
  if (rows.length === 0) return html`<section class="needs" aria-label="Waiting for you"><p class="needs-none">No agent is waiting for you now.</p></section>`;
  return html`
    <section class="needs" aria-label="Waiting for you">
      <h2 class="needs-head">Waiting for you (${rows.length})</h2>
      <ol class="needs-list">
        ${rows.map((r) => {
          const age = ageOf(r.since, now);
          return html`<li class=${"needs-row needs-row--" + r.kind} data-pane=${r.id} data-kind=${r.kind} key=${r.id}>
            <button type="button" class="needs-pick needs-pick--plain" onClick=${() => onOpen && onOpen(r.id)}>
              <span class="needs-who">${r.name}</span>
              ${age ? html`<span class="needs-age">${age}</span>` : null}
              <span class="needs-why"><span class="needs-word">${r.word}</span>${r.detail ? html`<span class="needs-detail">${r.detail}</span>` : null}</span>
            </button>
          </li>`;
        })}
      </ol>
    </section>
  `;
}
