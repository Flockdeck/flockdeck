// The Changes view across the team: the list of branches and, beside it, the one picked.
// On a phone the list and the detail are two screens with a back button; opening a
// branch moves the keyboard to Back and going back returns it to the row. The detail is
// a plain summary here; the branch header, the merge gate and the diffs come with the
// parts that draw them.
//
// One status line stays in the page, empty first, and is filled with what the view is
// doing ("Reading what changed...", "Read 4 of 5 working trees."): a line added with its
// text already in it is not announced by a screen reader.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { ChangesList, NO_BRANCHES_WORDS, READING_WORDS, nameOf, words } from "./ChangesList.js";
import { BranchDiff } from "./BranchDiff.js";
import { BranchHeader } from "./BranchHeader.js";
import { MergeGate, useGateActions } from "../drawer/diff/MergeGate.js";
import { actsFor, gateActsFor } from "../drawer/diff/gateModel.js";
import { CHANGES_RELAY_WORDS, CHANGES_VIEWER_WORDS } from "../lib/changesteam.js";

/** BranchGate is the merge gate of one branch: the drawer's MergeGate with the actions it sends through the store. win says what this window may do ({ viewer, relay, caps }). */
function BranchGate({ branch, names, win, reviewers, flowIn }) {
  const flow = useGateActions({ branch, send: flowIn.send, result: flowIn.result, connected: flowIn.connected, session: flowIn.session });
  const others = reviewers.filter((r) => r.pane !== branch.pane);
  return html`<${MergeGate} branch=${branch} canAct=${gateActsFor(win)} canCheck=${actsFor("changesChecks", win)} flow=${flow}
    nameOf=${(p) => nameOf(names, p)} reviewer=${others.length ? others[0].pane : ""} />`;
}

function Detail({ branch: b, names, onBack, backRef, win, reviewers, flowIn, git, onRemote, onDismiss, diffs, results, store }) {
  return html`<section class="ct-detail" aria-label=${"Changes of " + nameOf(names, b.pane)}>
    <button type="button" class="ct-back drawer-btn" ref=${backRef} onClick=${onBack}>Back to the list</button>
    <h3 class="ct-detail-title">${nameOf(names, b.pane)} <span class="ct-branch">${words(b.branch, 120)}</span></h3>
    ${b.error
      ? html`<p class="view-note ct-error" role="alert">${words(b.error, 200)}</p>`
      : html`<${BranchHeader} branch=${b} names=${names} git=${git} onRemote=${onRemote} onDismiss=${onDismiss} />
        ${b.gate ? html`<${BranchGate} key=${b.pane} branch=${b} names=${names} win=${win} reviewers=${reviewers} flowIn=${flowIn} />` : null}
        <${BranchDiff} key=${b.pane} branch=${b} names=${names} win=${win} diffs=${diffs} results=${results} session=${flowIn.session} connected=${flowIn.connected} store=${store} />`}
    ${b.omitted ? html`<p class="view-note">${b.omitted} more files are not listed.</p>` : null}
  </section>`;
}

/** statusOf is what the view says it is doing, or "" when it has drawn the branches. */
export function statusOf({ data, remote, viewer, gone }) {
  if (remote || viewer || gone) return "";
  if (!data) return READING_WORDS;
  if (data.partial && data.partial.read < data.partial.total) return "Read " + data.partial.read + " of " + data.partial.total + " working trees.";
  return "";
}

/** TeamChanges draws a changesTeam push (lib/changesteam.js). gone is the watch's refusal, if any; viewer is a window that may not use the desk's commands. */
export function TeamChanges({ data, names, remote, viewer, gone, caps = [], reviewers = [], result = null, connected = true, session = null, git = null, store = null, diffs, results }) {
  const [picked, setPicked] = useState("");
  const [detail, setDetail] = useState(false);
  const root = useRef(null);
  const backRef = useRef(null);
  const asked = useRef("");
  // Opening a branch on a narrow screen puts the keyboard on Back; Back puts it on the row that was opened.
  useEffect(() => {
    if (asked.current === "open" && detail && backRef.current && backRef.current.offsetParent !== null) backRef.current.focus();
    if (asked.current === "back" && !detail && root.current) {
      const row = root.current.querySelector('.ct-row[data-pane="' + String(picked).replace(/["\\]/g, "") + '"]');
      if (row) row.focus();
    }
    asked.current = "";
  }, [detail]);
  const status = statusOf({ data, remote, viewer, gone });
  const live = html`<p class="view-note ct-live" role="status" aria-live="polite">${status}</p>`;
  let body = null;
  if (remote) body = html`<p class="view-note">${CHANGES_RELAY_WORDS}</p>`;
  else if (viewer) body = html`<p class="view-note">${CHANGES_VIEWER_WORDS}</p>`;
  else if (gone) body = html`<p class="view-note ct-error" role="alert">${words(gone.error, 200) || "The branches could not be read."}</p>`;
  else if (data && !data.branches.length) body = html`<p class="view-note">${NO_BRANCHES_WORDS}</p>`;
  else if (data) {
    const cur = data.branches.find((b) => b.pane === picked) || data.branches[0];
    body = html`<div class=${"ct-view" + (detail ? " is-detail" : "")} ref=${root}>
      <${ChangesList} branches=${data.branches} names=${names} selected=${cur.pane} onPick=${(p) => { asked.current = "open"; setPicked(p); setDetail(true); }} />
      <${Detail} branch=${cur} names=${names} backRef=${backRef} onBack=${() => { asked.current = "back"; setDetail(false); }}
        win=${{ viewer: !!viewer, relay: !!remote, caps }} reviewers=${reviewers} flowIn=${{ send: store ? (c) => store.send(c) : null, result, connected, session }}
        git=${git} diffs=${diffs} results=${results} store=${store} onRemote=${store ? (a) => store.branchRemote(a, cur.pane, cur.cwd) : null} onDismiss=${store ? () => store.clearBranchGit() : null} />
    </div>`;
  }
  return html`<div class="ct-root">${live}${body}</div>`;
}
