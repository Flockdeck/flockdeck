// The merge gate in one small pane, for a branch: four rows (checks, conflicts,
// reviewer, comments), what else blocks it, and the control that approves the
// merge. It takes `{ branch }`, one entry of a `changesTeam` push, and reads the
// desk's `gate`; the desk decides, this says it (gateModel.js).
//
// - canAct: false (the default) draws the gate read-only, with no buttons: that is
//   a window whose desk has no gate caps, or one that may not write. It is the
//   caller's to know.
// - flow: the state of the actions (useGateActions): busy, error, and the gate the
//   desk answered with. Without it the buttons are not drawn.
// - nameOf(pane): the name drawn for a reviewer; reviewer: the pane the "Ask to
//   review" button asks, when there is one to ask.
// "Approve merge" sends the approval and tells the manager; it does not merge. It
// is disabled with the reason beside it, named by aria-describedby, while the gate
// is blocked. Every state is a word as well as a colour. One status line stays in
// the page, empty first, and is filled with what the last action did.
import { useEffect, useRef, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { clip } from "../../graph/Node.js";
import {
  gateOf, checksSummary, checksWords, reviewSummary, commentsSummary, conflictSummary, otherBlockers, approveState,
  approveCommand, unapproveCommand, checksCommand, reviewCommand, gateRefusalWords,
} from "./gateModel.js";

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));
const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const NOT_CONNECTED = "Not connected to Flockdeck. Nothing was sent.";
const TONE_WORDS = { pass: "Passing", fail: "Failing", wait: "Waiting", none: "None" };
let counter = 0;
const newReq = () => "gt" + Date.now().toString(36) + (++counter).toString(36);

/**
 * useGateActions: { branch, send, result }. send writes a command and answers
 * whether it did; result is the latest changesResult. It answers { busy (the
 * command being waited on, or ""), error, said (the last outcome in words), gate
 * (the gate to draw: the desk's answer until the next push), approve, withdraw,
 * runChecks(), askReview(reviewer) }.
 */
export function useGateActions({ branch = null, send = null, result = null }) {
  const [s, setS] = useState({ busy: "", req: "", error: "", said: "", override: null });
  const seen = useRef(null);
  useEffect(() => {
    if (!result || result === seen.current || !s.req || result.req !== s.req) return;
    seen.current = result;
    const op = text(result.op);
    setS((p) => ({
      ...p, busy: "", req: "",
      error: result.ok ? "" : gateRefusalWords(result),
      said: result.ok ? (op === "approveMerge" ? "Approved. The manager has been told." : op === "unapproveMerge" ? "Approval withdrawn." : op === "runChecks" ? "Checks started." : op === "requestReview" ? "Review asked for." : "Done.") : "",
      override: isObject(result.gate) ? result.gate : p.override,
    }));
  }, [result]);
  // The desk's next push is the truth again.
  const gate = branch && branch.gate;
  useEffect(() => { setS((p) => (p.override ? { ...p, override: null } : p)); }, [gate]);

  const go = (cmd, busy) => {
    if (s.busy) return false;
    if (!send || !send(cmd)) {
      setS((p) => ({ ...p, error: NOT_CONNECTED, said: "" }));
      return false;
    }
    setS((p) => ({ ...p, busy, req: cmd.req, error: "", said: "" }));
    return true;
  };
  const ids = () => ({ pane: branch ? branch.pane : "", branch: branch ? branch.branch : "", req: newReq() });
  return {
    busy: s.busy,
    error: s.error,
    said: s.said,
    gate: s.override,
    approve: () => go(approveCommand({ ...ids(), expectSha: branch ? branch.headSha : "" }), "approveMerge"),
    withdraw: () => go(unapproveCommand(ids()), "unapproveMerge"),
    runChecks: () => go(checksCommand(ids()), "runChecks"),
    askReview: (reviewer) => go(reviewCommand({ ...ids(), reviewer }), "requestReview"),
  };
}

function Row({ label, tone, children, action }) {
  return html`<li class=${"dv-gate-row dv-gate-row--" + tone}>
    <span class=${"dv-chip dv-tone dv-tone--" + tone}>${TONE_WORDS[tone] || "None"}</span>
    <span class="dv-gate-k">${label}</span>
    <span class="dv-gate-v">${children}</span>
    ${action || null}
  </li>`;
}

/** MergeGate draws the gate of one branch. */
export function MergeGate({ branch = null, canAct = false, flow = null, nameOf = null, reviewer = "" }) {
  const [said, setSaid] = useState("");
  const status = flow ? flow.error || flow.said || (flow.busy ? "Working…" : "") : "";
  useEffect(() => { setSaid(status); }, [status]);
  if (!isObject(branch)) return null;
  const view = flow && flow.gate ? { ...branch, gate: flow.gate } : branch;
  const g = gateOf(view);
  const checks = checksSummary(view);
  const review = reviewSummary(view);
  const comments = commentsSummary(view);
  const conflict = conflictSummary(view);
  const approve = approveState(view, canAct && !!flow && !flow.busy);
  const name = (p) => clip(text(nameOf ? nameOf(p) : p), 40);
  const act = canAct && !!flow;
  const whyId = "dv-gate-why-" + text(branch.pane);
  const stateWords = g.state === "blocked" ? (g.blockers.length ? g.blockers.length + " blocking" : "Blocked") : g.state === "ready" ? "Ready" : g.state === "approved" ? "Approved" : "Merged";
  const btn = (cls, label, onClick, aria) => html`<button type="button" class=${"drawer-btn " + cls} disabled=${!!flow.busy} aria-label=${aria || label} onClick=${onClick}>${label}</button>`;

  return html`<section class="dv-gate" role="group" aria-label=${"Merge gate for " + clip(text(branch.branch), 80)}>
    <div class="dv-gate-h"><b>Merge gate</b><span class=${"dv-chip dv-gate-state dv-gate-state--" + g.state}>${stateWords}</span>
      ${canAct ? null : html`<span class="dv-gate-ro">Read-only here</span>`}</div>
    <ul class="dv-gate-rows" aria-label="What the gate checks">
      <${Row} label="Checks" tone=${checks.tone} action=${act && checks.tone !== "pass" ? btn("dv-gate-run", checks.total ? "Run again" : "Run checks", flow.runChecks, "Run the checks again") : null}>${checksWords(checks)}<//>
      <${Row} label="Conflicts" tone=${conflict.tone}>${conflict.text ? clip(conflict.text, 300) : "None with the base or another branch"}<//>
      <${Row} label="Reviewer" tone=${review.tone}
        action=${act && reviewer && (review.state === "none" || review.state === "stale") ? btn("dv-gate-ask", "Ask " + name(reviewer) + " to review", () => flow.askReview(reviewer)) : null}>${review.state === "changes" ? name(review.who) + " asked for changes" + (review.note ? ": " + clip(review.note, 200) : "")
          : review.state === "approved" ? name(review.who) + " approved"
          : review.state === "requested" ? "Waiting for " + name(review.who)
          : review.state === "stale" ? "The earlier sign-off is out of date" : "No reviewer yet. This does not block the merge."}<//>
      <${Row} label="Comments" tone=${comments.tone}>${comments.open ? comments.open + (comments.open === 1 ? " open comment" : " open comments") : "No open review comments"}<//>
    </ul>
    ${otherBlockers(view).map((b, i) => html`<p key=${i} class="dv-gate-also">${clip(text(b.text) || b.kind, 200)}</p>`)}
    <div class="dv-gate-f">
      ${approve.kind === "merged"
        ? html`<span class="dv-chip dv-tone dv-tone--pass">Merged</span>${g.mergeSha ? html` <span class="dv-mono">${text(g.mergeSha).slice(0, 8)}</span>` : null}`
        : act
          ? html`<button type="button" class=${"drawer-btn dv-gate-approve" + (approve.kind === "approved" ? " is-approved" : "")} disabled=${!approve.enabled} aria-describedby=${whyId}
              onClick=${() => (approve.kind === "approved" ? flow.withdraw() : flow.approve())}>${approve.label}</button>`
          : null}
      <span class="dv-gate-why" id=${whyId}>${approve.kind === "approved" ? "Approved" + (g.approvedBy ? " by " + name(g.approvedBy) : "") + (g.approvedSha ? " at " + text(g.approvedSha).slice(0, 8) : "") + ". The manager merges it; this does not." : approve.why}</span>
    </div>
    <p class="dv-live" role="status">${said}</p>
  </section>`;
}
