// The Changes view across the team: the list of branches and, beside it, the one picked.
// On a phone the list and the detail are two screens with a back button. The detail is
// a plain summary here; the branch header, the merge gate and the diffs come with the
// parts that draw them.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { ChangesList, NO_BRANCHES_WORDS, READING_WORDS, nameOf } from "./ChangesList.js";
import { CHANGES_RELAY_WORDS } from "../lib/changesteam.js";

function Detail({ branch: b, names, onBack }) {
  return html`<section class="ct-detail" aria-label=${"Changes of " + nameOf(names, b.pane)}>
    <button type="button" class="ct-back drawer-btn" onClick=${onBack}>Back to the list</button>
    <h3 class="ct-detail-title">${nameOf(names, b.pane)} <span class="ct-branch">${b.branch}</span></h3>
    ${b.error
      ? html`<p class="view-note ct-error" role="alert">${b.error}</p>`
      : html`<ul class="ct-filelist">
        ${b.files.map((f) => html`<li key=${f.path}><span class="ct-status">${f.status}</span> <span class="ct-path">${f.oldPath ? f.oldPath + " to " : ""}${f.path}</span> <span class="ct-added">+${f.added}</span> <span class="ct-removed">-${f.removed}</span></li>`)}
      </ul>`}
    ${b.omitted ? html`<p class="view-note">${b.omitted} more files are not listed.</p>` : null}
  </section>`;
}

/** TeamChanges draws a changesTeam push (lib/changesteam.js). gone is the watch's refusal, if any. */
export function TeamChanges({ data, names, remote, gone }) {
  const [picked, setPicked] = useState("");
  const [detail, setDetail] = useState(false);
  if (remote) return html`<p class="view-note">${CHANGES_RELAY_WORDS}</p>`;
  if (gone) return html`<p class="view-note ct-error" role="alert">${gone.error || "The branches could not be read."}</p>`;
  if (!data) return html`<p class="view-note" role="status">${READING_WORDS}</p>`;
  if (!data.branches.length) return html`<p class="view-note">${NO_BRANCHES_WORDS}</p>`;
  const cur = data.branches.find((b) => b.pane === picked) || data.branches[0];
  const partial = data.partial && data.partial.read < data.partial.total;
  return html`<div class=${"ct-view" + (detail ? " is-detail" : "")}>
    ${partial ? html`<p class="view-note ct-partial" role="status">Read ${data.partial.read} of ${data.partial.total} working trees.</p>` : null}
    <${ChangesList} branches=${data.branches} names=${names} selected=${cur.pane} onPick=${(p) => { setPicked(p); setDetail(true); }} />
    <${Detail} branch=${cur} names=${names} onBack=${() => setDetail(false)} />
  </div>`;
}
