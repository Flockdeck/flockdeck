// The Worktrees dialog: the checkouts of the project's repository that agents work
// in, which pane is in which, and making and removing them. It leads with why they
// exist: each agent on its own worktree and branch cannot overwrite another's files.
//
// Every command is answered by the listing again, which is what clears the busy
// state, so the dialog opens at once and draws the answer when it comes. Removing a
// worktree always asks first: a worktree with uncommitted work names how much and
// says it is discarded, one an agent is working in is not sent at all (the desktop
// would refuse it) and the person is told to close the pane, and the branch is kept
// whatever happens. The desktop's own notices (created, removed, refused) reach the
// page through the notice bar. Every string in a listing came from git or the person
// and is drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Dialog } from "./Dialog.js";
import { Confirm } from "./Confirm.js";
import {
  BRANCH_KEPT_WORDS, WHY_WORDS, addCommand, changesWords, listCommand, membersOf, openHereCommand, pruneCommand, removalOf, removeCommand, sharedOf, sharedWords,
  worktreesOf,
} from "../lib/worktrees.js";

const FREE_SHOWN = 14;
const plural = (n, one, many) => n + " " + (n === 1 ? one : many);

/** Worktrees is the dialog. listing is the store's worktrees answer ({ ... , seq } or
 *  null), send the store's send, onClose closes the dialog. */
export function Worktrees({ listing, send, onClose, nav = null }) {
  const [picked, setPicked] = useState("");
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState({ branch: "", base: "", root: "" });
  const [asking, setAsking] = useState(null);
  const [problem, setProblem] = useState("");
  const floor = useRef(listing ? listing.seq : 0);
  const seen = useRef(floor.current);
  const w = listing && listing.seq > floor.current ? worktreesOf(listing) : null;

  const ask = (root = "") => {
    if (send(listCommand(root)) === false) setProblem("The request could not be sent. Check the connection.");
    else setProblem("");
  };
  useEffect(() => {
    ask();
  }, []);
  // Every answer to a command is the listing again: the work is over.
  useEffect(() => {
    if (listing && listing.seq !== seen.current) {
      seen.current = listing.seq;
      setBusy(false);
    }
  }, [listing && listing.seq]);

  const run = (cmd) => {
    if (!cmd) return false;
    if (send(cmd) === false) {
      setProblem("The request could not be sent. Check the connection.");
      return false;
    }
    setProblem("");
    setBusy(true);
    return true;
  };
  const here = (wt, kind) => {
    const cmd = openHereCommand(wt, kind);
    if (cmd && send(cmd) !== false) onClose();
    else setProblem("The request could not be sent. Check the connection.");
  };
  const remove = (wt) => {
    const r = removalOf(wt);
    if (r.refuse) {
      setProblem(r.refuse);
      return;
    }
    setProblem("");
    setAsking(wt);
  };

  const members = w ? membersOf(w) : [];
  const repoRoot = members.length ? (members.some((m) => m.root === draft.root) ? draft.root : w.root) : "";
  const create = () => {
    if (busy) return;
    const cmd = addCommand(draft.branch, draft.base || (w ? w.defaultBase : ""), repoRoot);
    if (!cmd) {
      setProblem("Name the branch for the new worktree first.");
      return;
    }
    if (run(cmd)) setDraft({ ...draft, branch: "" });
  };
  const onEnter = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.accept) {
      e.preventDefault();
      create();
    }
  };

  const row = (wt) => {
    const changes = changesWords(wt);
    const sub = [];
    if (wt.prunable) sub.push(html`<span class="wt-gone" key="g">only git's record of it is left</span>`);
    else if (!changes) sub.push(html`<span class="wt-clean" key="c">clean</span>`);
    else sub.push(html`<span class="wt-dirty" key="d">${changes}, not committed</span>`);
    if (wt.ahead) sub.push(html`<span key="a" title=${"Commits ahead of " + (wt.upstream || "the upstream branch")}>↑${wt.ahead}</span>`);
    if (wt.behind) sub.push(html`<span key="b" title=${"Commits behind " + (wt.upstream || "the upstream branch")}>↓${wt.behind}</span>`);
    if (wt.upstream) sub.push(html`<span key="u">${wt.upstream}</span>`);
    if (wt.head) sub.push(html`<span key="h">${wt.head}</span>`);
    return html`<li key=${wt.path} class=${"wt-row" + (wt.prunable ? " gone" : "") + (wt.panes > 1 ? " shared" : "")} data-path=${wt.path} data-ctx="worktree">
      <div class="wt-main">
        <div class="wt-title">
          <span class="wt-label">${wt.label}</span>
          ${wt.branch ? html`<span class="wt-flag">${wt.branch}</span>` : null}
          ${wt.repo ? html`<span class="wt-flag" title=${wt.repoRoot}>${wt.repo}</span>` : null}
          ${wt.main ? html`<span class="wt-flag">main</span>` : null}
          ${wt.locked ? html`<span class="wt-flag">locked</span>` : null}
          ${wt.detached ? html`<span class="wt-flag">detached</span>` : null}
          ${wt.prunable ? html`<span class="wt-flag gone">folder gone</span>` : null}
          ${wt.panes ? html`<span class="wt-flag agents">${plural(wt.panes, "agent", "agents")}</span>` : null}
        </div>
        <div class="wt-meta">${sub}</div>
        <div class="wt-path">${wt.path}</div>
      </div>
      <div class="wt-actions">
        ${wt.prunable
          ? html`<button type="button" class="dialog-btn wt-prune-row" disabled=${busy} onClick=${() => run(pruneCommand(repoRootOf(wt)))}>Prune</button>`
          : html`<button type="button" class="dialog-btn dialog-btn--primary wt-agent" disabled=${busy} onClick=${() => here(wt, "agent")}>Open agent</button>
              <button type="button" class="dialog-btn wt-shell" disabled=${busy} onClick=${() => here(wt, "shell")}>Open shell</button>`}
        ${wt.main ? null : html`<button type="button" class="dialog-btn dialog-btn--danger wt-remove" disabled=${busy} onClick=${() => remove(wt)}>Remove…</button>`}
      </div>
    </li>`;
  };
  const repoRootOf = (wt) => wt.repoRoot;
  // The right-click menu of a row runs these, through the page's nav block.
  useEffect(() => {
    if (!nav) return undefined;
    nav.worktree = { find: (path) => (w ? w.items.find((x) => x.path === path) : null), here, remove, prune: (wt) => run(pruneCommand(repoRootOf(wt))), busy };
    return () => { nav.worktree = null; };
  });

  const free = w ? w.branches.filter((b) => !b.checkedIn && (!b.repoRoot || !repoRoot || b.repoRoot === repoRoot)) : [];
  const bases = w ? w.branches.filter((b) => !b.repoRoot || !repoRoot || b.repoRoot === repoRoot) : [];
  const gone = w ? w.items.filter((x) => x.prunable).length : 0;
  const shared = w ? sharedOf(w.items) : [];

  return html`
    <${Dialog} id="worktrees" title="Worktrees" onClose=${onClose} focus=".wt-agent" wide>
      <p class="dialog-note" id="wt-why">${WHY_WORDS}</p>
      ${problem ? html`<p class="dialog-note dialog-note--error" id="wt-problem" role="alert">${problem}</p>` : null}
      ${!w && !problem ? html`<p class="dialog-note" id="wt-loading" role="status">Reading the worktrees…</p>` : null}
      ${w && w.error
        ? html`<div id="wt-error">
            <p class="dialog-note dialog-note--error" role="alert">${w.error}</p>
            <p class="dialog-note">${w.repos.length ? "Pick the repository whose worktrees you want." : "Open a project that is a git repository to see its worktrees."}</p>
            <div class="team-row">${w.repos.map((r) => html`<button type="button" class="dialog-btn wt-repo" key=${r.path} title=${r.path} onClick=${() => { setPicked(r.path); ask(r.path); }}>${r.name}</button>`)}</div>
          </div>`
        : null}
      ${w && !w.error
        ? html`
          ${shared.map((wt) => html`<p class="dialog-note dialog-note--warn wt-conflict" role="alert" key=${wt.path}>${sharedWords(wt)}</p>`)}
          <div class="team-row">
            <strong>Worktrees</strong><span class="dialog-note">${w.items.length}</span>
            <button type="button" class="dialog-btn" id="wt-refresh" disabled=${busy} onClick=${() => ask(picked)}>Refresh</button>
            ${gone ? html`<button type="button" class="dialog-btn" id="wt-prune-all" disabled=${busy} title="Drop git's records of worktrees whose folders are gone" onClick=${() => run(pruneCommand(picked))}>Prune gone</button>` : null}
          </div>
          ${busy ? html`<p class="dialog-note" role="status">Working…</p>` : null}
          ${w.items.length === 0 ? html`<p class="dialog-note" id="wt-none">No worktrees yet. Create one below.</p>` : null}
          <ul class="wt-list" aria-label="Worktrees">${w.items.map(row)}</ul>
          ${asking ? html`<${Confirm} ...${confirmProps(asking, (wt) => { setAsking(null); run(removeCommand(wt, wt.repoRoot)); }, () => setAsking(null))} />` : null}
          ${free.length
            ? html`<h4 class="team-group-head">Branches without a worktree</h4>
                <div class="team-row">${free.slice(0, FREE_SHOWN).map((b) => html`<button type="button" class="dialog-btn wt-checkout" key=${b.name} disabled=${busy}
                  title=${"Create a worktree checking out " + b.name} onClick=${() => run(addCommand(b.name, "", repoRoot))}>${b.name}</button>`)}</div>
                ${free.length > FREE_SHOWN ? html`<p class="dialog-note">${plural(free.length - FREE_SHOWN, "more branch", "more branches")} not shown. Type a branch name below to check it out.</p>` : null}`
            : null}
          <h4 class="team-group-head">New worktree</h4>
          <p class="dialog-note">Made next to the repository. A branch name that exists is checked out; a new one starts from the base.</p>
          ${members.length
            ? html`<div class="team-row" role="group" aria-label="Repository">${members.map((m) => html`<button type="button" class="dialog-btn wt-member" key=${m.root} title=${m.root}
                aria-pressed=${m.root === repoRoot ? "true" : "false"} onClick=${() => setDraft({ ...draft, root: m.root })}>${m.name}</button>`)}</div>`
            : null}
          <div class="team-row">
            <input type="text" class="dialog-field" id="wt-branch" placeholder="Branch, for example fix-auth" aria-label="Branch name" autocomplete="off" spellcheck="false"
              value=${draft.branch} onInput=${(e) => setDraft({ ...draft, branch: e.target.value })} onKeyDown=${onEnter} />
            <input type="text" class="dialog-field" id="wt-base" list="wt-bases" placeholder="Base" aria-label="Base branch" title="The commit or branch the new branch begins at" autocomplete="off" spellcheck="false"
              value=${draft.base || w.defaultBase} onInput=${(e) => setDraft({ ...draft, base: e.target.value })} onKeyDown=${onEnter} />
            <button type="button" class="dialog-btn dialog-btn--primary" id="wt-create" disabled=${busy} onClick=${create}>Create</button>
            <datalist id="wt-bases">${bases.map((b) => html`<option key=${b.name} value=${b.name}></option>`)}</datalist>
          </div>
          <p class="dialog-note">${BRANCH_KEPT_WORDS}</p>`
        : null}
      <div class="dialog-foot">
        <button type="button" class="dialog-btn" onClick=${onClose}>Close</button>
      </div>
    <//>
  `;
}

// confirmProps is the question before a removal: the worktree's own words, and the
// action it runs. The title and lines are written here, from numbers and a path.
function confirmProps(wt, onYes, onNo) {
  const r = removalOf(wt);
  return { title: r.title, words: r.words, label: r.label, onConfirm: () => onYes(wt), onCancel: onNo };
}
