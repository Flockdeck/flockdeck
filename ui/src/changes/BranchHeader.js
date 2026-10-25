// The header of one branch in the Changes view: whose it is, where it is based, how far
// it is from its base and its remote, and Fetch, Pull and Push for its checkout. What a
// fetch, pull or push said is shown under it as text. Every string is data from a git
// repository or an agent and is drawn as text with its hidden characters shown.
import { html } from "../html.js";
import { nameOf, words } from "./ChangesList.js";

const plural = (n, one, many) => n + " " + (n === 1 ? one : many);

/** distanceWords says how far a branch is from what it follows: "2 ahead, 3 behind", or that it is level. */
export function distanceWords(b) {
  const to = words(b.upstream || b.base, 120);
  if (!b.ahead && !b.behind) return "level with " + (to || "its base");
  return [b.ahead ? b.ahead + " ahead" : "", b.behind ? b.behind + " behind" : ""].filter(Boolean).join(", ") + (to ? " of " + to : "");
}

/** BranchHeader draws the branch from the changesTeam push. git is the store's branchGit
 *  ({ action, pane, note }) when a remote action is out or has just answered; onRemote gets
 *  "fetch", "pull" or "push", and onDismiss lets go of the note. */
export function BranchHeader({ branch: b, names, git, onRemote, onDismiss }) {
  const mine = !!git && git.pane === b.pane;
  const busy = !!git && !git.note;
  const dirty = b.uncommitted + b.untracked;
  return html`<header class="ct-header">
    <h3 class="ct-detail-title">${nameOf(names, b.pane)} <span class="ct-branch">${words(b.branch, 120) || "detached"}</span>
      ${b.pr && b.pr.number ? html` <a class="ct-pr" href=${/^https?:\/\//.test(b.pr.url) ? b.pr.url : undefined} target="_blank" rel="noopener noreferrer">#${b.pr.number} ${words(b.pr.state, 20)}</a>` : null}
    </h3>
    <p class="ct-meta">
      <span class="ct-base">${b.base ? "Based on " + words(b.base, 120) : "No base"}</span>
      <span class="ct-distance">${distanceWords(b)}</span>
      ${dirty ? html`<span class="ct-dirty">${plural(dirty, "file", "files")} not committed</span>` : null}
    </p>
    ${b.lastCommit && b.lastCommit.subject ? html`<p class="ct-last">${words(b.lastCommit.sha, 8)} ${words(b.lastCommit.subject, 160)}</p>` : null}
    ${onRemote && b.cwd
      ? html`<div class="ct-actions" role="group" aria-label="Remote">
          ${["fetch", "pull", "push"].map((a) => html`<button key=${a} type="button" class="drawer-btn ct-remote" data-action=${a} disabled=${busy} aria-busy=${mine && busy ? "true" : undefined} onClick=${() => onRemote(a)}>${mine && busy && git.action === a ? a[0].toUpperCase() + a.slice(1) + "..." : a[0].toUpperCase() + a.slice(1)}</button>`)}
        </div>`
      : null}
    ${mine && git.note
      ? html`<p class=${"ct-note" + (git.note.error ? " ct-error" : "")} role=${git.note.error ? "alert" : "status"}>${words(git.note.text, 300)} <button type="button" class="ct-dismiss" onClick=${onDismiss}>Dismiss</button></p>`
      : null}
  </header>`;
}
