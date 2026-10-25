// The Changes tab of the drawer: what has changed in the pane's working tree, a
// diff per file, and commit, push and pull.
//
// The desktop does the work (the changes, diff, commit, gitPush, gitPull and
// gitFetch commands); this draws what it sends and asks again. Everything it
// sends is drawn as text: file names, diffs, git's own messages and the
// questions it asks about a repository's programs. A file's path goes to the
// desktop as a field of a command and nowhere else, never into a link or a
// command line.
//
// - The list is drawn LIST_FIRST rows at a time. The desktop lists at most two
//   thousand files and counts the rest; the tab says how many are not listed and
//   that a commit includes them.
// - A commit sends the files and stamps that were listed, so the desktop refuses
//   a tree that changed since, and answers with the tree as it is. A commit with
//   no message is refused here, with the reason, and sends nothing.
// - When the desktop asks about a repository's git settings or hooks (gitWarn)
//   the tab shows what would run and asks twice, as the old interface did: go on,
//   then remember or only this time. Nothing runs until it is answered.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { Diff } from "./conv/Diff.js";

export const LIST_FIRST = 200;
export const LIST_MORE = 500;
export const NO_TREE_WORDS = "This pane has no directory to look at.";

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));
const plural = (n, one, many) => n.toLocaleString("en-US") + " " + (n === 1 ? one : many);

function GitWarn({ warn, store }) {
  const [step, setStep] = useState(0);
  const items = Array.isArray(warn.items) ? warn.items : [];
  // The question takes the keyboard when it appears: it blocks the commit, and nothing else on the panel says so to a reader.
  const box = useRef(null);
  const before = useRef(null);
  useEffect(() => {
    if (!before.current && box.current) before.current = box.current.ownerDocument.activeElement;
    if (box.current && box.current.focus) box.current.focus();
  }, [warn.seq]);
  // Answered, the keyboard goes back to where it was.
  useEffect(() => () => {
    const el = before.current;
    if (el && el.ownerDocument && el !== el.ownerDocument.body && el.focus) el.focus();
  }, []);
  return html`<section class="chg-warn" role="alertdialog" tabindex="-1" ref=${box} aria-label="This repository runs programs you have not accepted">
    <p class="chg-warn-intro">${clip(text(warn.intro || warn.text), 1200)}</p>
    <ul class="chg-warn-items">${items.slice(0, 50).map((line, i) => html`<li key=${i}>${clip(text(line), 400)}</li>`)}</ul>
    ${step === 0
      ? html`<div class="chg-row">
          <button type="button" class="drawer-btn chg-warn-continue" onClick=${() => setStep(1)}>Continue</button>
          <button type="button" class="drawer-btn chg-warn-stop" onClick=${() => store.answerGitWarn("")}>Stop</button>
        </div>`
      : html`<p class="chg-warn-ask">Remember this for the repository? Flockdeck will ask again only if what runs changes.</p>
        <div class="chg-row">
          <button type="button" class="drawer-btn chg-warn-remember" onClick=${() => store.answerGitWarn("remember")}>Remember</button>
          <button type="button" class="drawer-btn chg-warn-once" onClick=${() => store.answerGitWarn("once")}>Just this time</button>
          <button type="button" class="drawer-btn chg-warn-stop" onClick=${() => store.answerGitWarn("")}>Stop</button>
        </div>`}
  </section>`;
}

function Files({ files, selected, onPick }) {
  const [shown, setShown] = useState(LIST_FIRST);
  const drawn = files.slice(0, shown);
  const left = files.length - drawn.length;
  return html`<ul class="chg-files" aria-label="Changed files">
      ${drawn.map((f) => html`<li key=${f.path} class="chg-file">
        <button type="button" class=${"chg-file-btn" + (selected === f.path ? " is-selected" : "")} aria-pressed=${selected === f.path ? "true" : "false"} onClick=${() => onPick(f.path)}>
          <span class="chg-status" data-status=${text(f.status)}>${clip(text(f.label || f.status), 20)}</span>
          <span class="chg-path">${clip(text(f.path), 400)}</span>
          ${f.added || f.removed ? html`<span class="chg-counts"><span class="chg-added">+${f.added | 0}</span> <span class="chg-removed">-${f.removed | 0}</span></span>` : null}
        </button>
      </li>`)}
    </ul>
    ${left > 0 ? html`<button type="button" class="drawer-btn chg-more" onClick=${() => setShown((n) => n + LIST_MORE)}>${"Show " + Math.min(LIST_MORE, left).toLocaleString("en-US") + " more files (" + left.toLocaleString("en-US") + " not drawn yet)"}</button>` : null}`;
}

/** Changes is the tab. cwd is the pane's directory; snap and store are the store's.
 *  git is a string that changes when the pane's git counts do (changed, new, ahead,
 *  behind): the tree has moved, so the list is read again, as a follow. */
export function Changes({ cwd, git = "", snap, store }) {
  const [draft, setDraft] = useState("");
  const [problem, setProblem] = useState("");
  const [picked, setPicked] = useState("");
  useEffect(() => {
    if (!cwd) return undefined;
    setPicked("");
    store.requestChanges(cwd, false);
    return () => store.clearChanges();
  }, [store, cwd]);
  // The tree moved under the tab (the pane's git counts changed): read it again,
  // quietly. Not while an action is out, and not for the first draw, which asked.
  const drawn = useRef(git);
  useEffect(() => {
    if (drawn.current === git) return;
    drawn.current = git;
    const ch = snap.changes;
    // Not while the git-settings question is on screen: the answer sends the listing again, so a follow dropped here is not lost.
    if (cwd && ch && !ch.error && !snap.changesBusy && !snap.gitWarn) store.requestChanges(ch.cwd || cwd, true);
  }, [git]);
  // The open diff follows the listing: read again while its file is still listed, closed when it is not.
  useEffect(() => {
    const ch = snap.changes;
    if (!picked || !ch || ch.error) return;
    // A file beyond the listed ones (the desk lists a cap and counts the rest) may still be there: keep it open.
    if ((ch.files || []).some((f) => f.path === picked) || (ch.omitted | 0) > 0) store.requestDiff(picked, true);
    else setPicked("");
  }, [snap.changes]);
  // The draft goes when the commit is made, not when it is sent: a commit the
  // desktop refuses (the tree moved) keeps the words that were typed.
  useEffect(() => {
    if (snap.changes && snap.changes.reason === "committed") setDraft("");
  }, [snap.changes]);
  if (!cwd) return html`<p class="chg-empty">${NO_TREE_WORDS}</p>`;
  const ch = snap.changes;
  const busy = snap.changesBusy || "";
  const note = snap.changesNote;
  const diff = snap.diff && picked && snap.diff.file === picked && snap.diff.cwd === ch.cwd ? snap.diff : null;
  const pick = (path) => {
    if (path === picked) { setPicked(""); return; }
    setPicked(path);
    store.requestDiff(path);
  };
  const commit = (push) => {
    const why = store.commitChanges(draft, push);
    setProblem(why);
  };
  if (!ch) return html`<div class="chg"><p class="chg-empty chg-state" role="status"><strong>Reading what changed…</strong><span>This takes a moment on a large repository.</span></p></div>`;
  if (ch.error) {
    return html`<div class="chg">
      <p class="chg-empty chg-state chg-error" role="alert"><strong>Could not read this working tree</strong><span>${clip(text(ch.error), 600)}</span></p>
      ${(ch.repos || []).slice(0, 20).map((r) => html`<button type="button" key=${r.path} class="drawer-btn chg-repo" onClick=${() => store.requestChanges(r.path, false)}>${clip(text(r.name), 120)}</button>`)}
    </div>`;
  }
  const files = Array.isArray(ch.files) ? ch.files : [];
  const total = files.length + (ch.omitted | 0);
  const where = ch.detached ? "detached at " + text(ch.head) + (ch.operation ? " (" + text(ch.operation) + ")" : "") : text(ch.branch);
  return html`<div class="chg" aria-busy=${busy ? "true" : "false"}>
    <div class="chg-head">
      <span class="chg-branch">${clip(where, 120)}</span>
      ${ch.upstream ? html`<span class="chg-sync">${"↑" + (ch.ahead | 0) + " ↓" + (ch.behind | 0)}</span>` : html`<span class="chg-sync">no upstream yet</span>`}
      <span class="chg-actions">
        <button type="button" class="drawer-btn chg-refresh" disabled=${!!busy} onClick=${() => store.requestChanges(cwd, false)}>Refresh</button>
        ${ch.hasRemote
          ? html`<button type="button" class="drawer-btn chg-fetch" disabled=${!!busy} onClick=${() => store.remoteChanges("fetch")}>Fetch</button>
            <button type="button" class="drawer-btn chg-pull" disabled=${!!busy} onClick=${() => store.remoteChanges("pull")}>Pull</button>
            <button type="button" class="drawer-btn chg-push" disabled=${!!busy} onClick=${() => store.remoteChanges("push")}>Push</button>`
          : null}
      </span>
    </div>
    <p class="chg-busy" role="status">${busy ? "Working: " + clip(busy, 40) + "…" : ""}</p>
    ${note && note.text ? html`<p class=${"chg-note" + (note.error ? " chg-error" : "")} role=${note.error ? "alert" : "status"}>${clip(text(note.text), 2000)}</p>` : null}
    ${snap.gitWarn ? html`<${GitWarn} key=${snap.gitWarn.seq} warn=${snap.gitWarn} store=${store} />` : null}
    ${files.length === 0 && !ch.omitted ? html`<p class="chg-empty chg-state"><strong>Nothing has changed.</strong><span>Edits in this working tree appear here. Refresh to check again.</span></p>` : html`<${Files} files=${files} selected=${picked} onPick=${pick} />`}
    ${ch.omitted ? html`<p class="chg-omitted">${plural(ch.omitted, "more file is", "more files are") + " not listed. The commit includes " + (ch.omitted === 1 ? "it" : "them") + "."}</p>` : null}
    ${picked
      ? html`<div class="chg-diff" role="region" aria-label=${"Diff of " + clip(picked, 200)}>
          ${!diff || (diff.loading && !diff.text) ? html`<p class="chg-empty" role="status">Reading the diff…</p>` : diff.error ? html`<p class="chg-empty chg-error" role="alert">${clip(text(diff.error), 600)}</p>` : html`<${Diff} key=${picked} text=${diff.text} label=${"Diff of " + clip(picked, 200)} />`}
        </div>`
      : null}
    ${total > 0
      ? html`<div class="chg-commit">
          <label class="chg-label" for="chg-message">Commit message</label>
          <textarea id="chg-message" class="chg-message" rows="3" value=${draft} onInput=${(e) => { setDraft(e.target.value); if (problem) setProblem(""); }}></textarea>
          ${problem ? html`<p class="chg-error" role="alert">${problem}</p>` : null}
          <div class="chg-row">
            <button type="button" class="drawer-btn chg-commit-btn" disabled=${!!busy} onClick=${() => commit(false)}>${"Commit " + plural(total, "file", "files")}</button>
            ${ch.hasRemote ? html`<button type="button" class="drawer-btn chg-commit-push" disabled=${!!busy} onClick=${() => commit(true)}>Commit and push</button>` : null}
          </div>
        </div>`
      : null}
  </div>`;
}
