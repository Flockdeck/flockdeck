// The merge gate in one small pane, for a branch: four rows (checks, conflicts,
// reviewer, comments), what else blocks it, and the control that approves the
// merge. It takes `{ branch }`, one entry of a `changesTeam` push, and reads the
// desk's `gate`; the desk decides, this says it (gateModel.js).
//
// - canAct: false (the default) draws the gate read-only, with no buttons: that is
//   a window whose desk has no gate caps, or one that may not write. It is the
//   caller's to know (gateActsFor in gateModel.js). canCheck is the same for the
//   "Run checks" and "Ask to review" buttons, which are the changesChecks cap's,
//   not the gate's: actsFor("changesChecks", win). It is false unless given.
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
import { Confirm } from "../../shell/Confirm.js";
import { clip } from "../../graph/Node.js";
import { showControls } from "./diffModel.js";
import {
  gateOf, checksSummary, checksWords, reviewSummary, commentsSummary, conflictSummary, otherBlockers, approveState,
  approveCommand, unapproveCommand, checksCommand, confirmChecksCommand, reviewCommand, gateRefusalWords, checksListOf, confirmWords,
} from "./gateModel.js";

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));
const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const NOT_CONNECTED = "Not connected to Flockdeck. Nothing was sent.";
const DROPPED = "The connection dropped; check the gate before trying again.";
const NO_HEAD = "This branch has no known head, so nothing was approved. Wait for the next update.";
const MOVED = "The branch changed; waiting for the update.";
const CHANGED_NOTE = "The check file changed after it was shown. These are its commands now; nothing was confirmed.";
const STALE = "The branch changed, so the question about its checks was dropped. Press Run checks to ask again.";
const NO_COMMANDS = "The check file has no command to run.";
const TONE_WORDS = { pass: "Passing", fail: "Failing", wait: "Waiting", none: "None" };
let counter = 0;
const newReq = () => "gt" + Date.now().toString(36) + (++counter).toString(36);

/**
 * useGateActions: { branch, send, result, connected, session }. send writes a
 * command and answers whether it did; result is the latest changesResult. If the
 * connection drops while a command is out (connected goes false, or session
 * changes) the wait ends with a message to check the gate: after a drop the person
 * cannot tell whether an approval was recorded, so the gate of the next push is
 * what they see, and nothing is sent again by itself. Approve is refused when the
 * branch has no head to send as expectSha. After a headMoved refusal the control
 * stays off while the branch's head is still the one that was sent (the sha that went out, not the one the window has when the answer lands: a push with the new head may arrive first). It answers { busy (the command being
 * waited on, or ""), error, said (the last outcome in words), gate (the gate to
 * draw: the desk's answer until the next push), moved, approve, withdraw,
 * runChecks(), askReview(reviewer), ask, confirm(), cancel() }.
 *
 * Run checks is a question first. The desk runs the commands of the check file only
 * when the person confirmed those very commands, so runChecks sends confirmChecks to
 * read them (checks is the store's latest changesChecks), and `ask` is the question
 * to draw once they are here: { phase: "asking", commands, hash, note }.
 * confirm() sends confirmChecks with the hash that was shown, on the person's press
 * only, and runs the checks when the desk took it. If the file changed in between (the
 * desk answers `changed`) the commands are read again and shown with a note, and
 * nothing is confirmed. A question is dropped when the branch changes, the connection
 * drops or the person cancels. Commands that are already confirmed run without a question.
 * One press sends once: a second press before the answer is ignored.
 */
export function useGateActions({ branch = null, send = null, result = null, connected = true, session = null, checks = null }) {
  const [s, setS] = useState({ busy: "", req: "", error: "", said: "", override: null, movedFrom: "", sentSha: "", ask: null });
  // A press sets this before the state it causes is drawn, so a second press in the same tick finds it.
  const inflight = useRef(false);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      if (connected !== false) return;
    }
    inflight.current = false;
    setS((p) => (p.req || p.ask ? { ...p, busy: "", req: "", error: DROPPED, said: "", override: null, ask: null } : p));
  }, [connected === false, session]);
  // A question about the checks belongs to the branch it was asked for.
  const branchName = text(branch && branch.branch);
  useEffect(() => {
    setS((p) => {
      if (!p.ask || p.ask.branch === branchName) return p;
      inflight.current = false;
      return { ...p, busy: "", req: "", ask: null, error: STALE, said: "" };
    });
  }, [branchName]);
  const seen = useRef(null);
  useEffect(() => {
    if (!result || result === seen.current || !s.req || result.req !== s.req) return;
    seen.current = result;
    inflight.current = false;
    const op = text(result.op);
    if (op === "confirmChecks") {
      if (result.ok) {
        // The person's press was taken: the checks run now.
        sendNow(checksCommand(ids()), "runChecks", { ask: null });
      } else if (result.code === "changed") {
        // The file is not the one that was shown: read it again, show what it says now, confirm nothing.
        sendNow(confirmChecksCommand(ids()), "confirmChecks", { ask: { phase: "reading", branch: branchName, note: CHANGED_NOTE } });
      } else {
        setS((p) => ({ ...p, busy: "", req: "", ask: null, error: gateRefusalWords(result), said: "" }));
      }
      return;
    }
    setS((p) => ({
      ...p, busy: "", req: "",
      error: result.ok ? "" : gateRefusalWords(result),
      said: result.ok ? (op === "approveMerge" ? "Approved. The manager has been told." : op === "unapproveMerge" ? "Approval withdrawn." : op === "runChecks" ? "Checks started." : op === "requestReview" ? "Review asked for." : "Done.") : "",
      override: result.code === "headMoved" ? p.override : isObject(result.gate) ? result.gate : p.override,
      movedFrom: result.code === "headMoved" ? p.sentSha : p.movedFrom,
    }));
  }, [result]);
  // The commands the desk would run, in answer to the question we asked.
  const seenChecks = useRef(null);
  useEffect(() => {
    if (!checks || checks === seenChecks.current || !s.ask || s.ask.phase !== "reading" || !s.req || checks.req !== s.req) return;
    seenChecks.current = checks;
    inflight.current = false;
    const found = checksListOf(checks);
    if (!found) {
      setS((p) => ({ ...p, busy: "", req: "", ask: null, error: NO_COMMANDS, said: "" }));
    } else if (found.pinned && !s.ask.note) {
      sendNow(checksCommand(ids()), "runChecks", { ask: null });
    } else {
      setS((p) => ({ ...p, busy: "", req: "", ask: { ...p.ask, phase: "asking", ...found } }));
    }
  }, [checks]);
  // The desk's next push is the truth again.
  const gate = branch && branch.gate;
  useEffect(() => { setS((p) => (p.override ? { ...p, override: null } : p)); }, [gate]);

  // sendNow sends one command unless one is already on its way. extra is state to set with it.
  const sendNow = (cmd, busy, extra = null) => {
    if (inflight.current) return false;
    inflight.current = true;
    if (!send || !send(cmd)) {
      inflight.current = false;
      setS((p) => ({ ...p, error: NOT_CONNECTED, said: "", ask: null }));
      return false;
    }
    setS((p) => ({ ...p, busy, req: cmd.req, sentSha: text(cmd.expectSha), error: "", said: "", ...(extra || {}) }));
    return true;
  };
  const go = (cmd, busy, extra = null) => (s.busy ? false : sendNow(cmd, busy, extra));
  const ids = () => ({ pane: branch ? branch.pane : "", branch: branch ? branch.branch : "", req: newReq() });
  return {
    busy: s.busy,
    error: s.error,
    said: s.said,
    gate: s.override,
    moved: !!s.movedFrom && s.movedFrom === text(branch && branch.headSha),
    approve: () => (!branch || typeof branch.headSha !== "string" || !branch.headSha
      ? (setS((p) => ({ ...p, error: NO_HEAD, said: "" })), false)
      : go(approveCommand({ ...ids(), expectSha: branch.headSha }), "approveMerge")),
    withdraw: () => go(unapproveCommand(ids()), "unapproveMerge"),
    ask: s.ask && s.ask.phase === "asking" ? s.ask : null,
    runChecks: () => go(confirmChecksCommand(ids()), "confirmChecks", { ask: { phase: "reading", branch: branchName, note: "" } }),
    confirm: () => {
      const a = s.ask;
      if (!a || a.phase !== "asking" || !a.hash) return false;
      return sendNow(confirmChecksCommand({ ...ids(), confirm: true, hash: a.hash }), "confirmChecks", { ask: { ...a, phase: "confirming" } });
    },
    cancel: () => {
      inflight.current = false;
      setS((p) => (p.ask ? { ...p, busy: "", req: "", ask: null } : p));
    },
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
export function MergeGate({ branch = null, canAct = false, canCheck = false, flow = null, nameOf = null, reviewer = "", project = "" }) {
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
  const moved = !!(flow && flow.moved);
  const approve = { ...approveState(view, canAct && !!flow && !flow.busy && !moved), ...(moved ? { enabled: false, why: MOVED } : {}) };
  const name = (p) => showControls(clip(text(nameOf ? nameOf(p) : p), 40));
  const act = canAct && !!flow;
  const actChecks = canCheck && !!flow;
  const whyId = "dv-gate-why-" + text(branch.pane);
  const stateWords = g.state === "blocked" ? (g.blockers.length ? g.blockers.length + " blocking" : "Blocked") : g.state === "ready" ? "Ready" : g.state === "approved" ? "Approved" : "Merged";
  const btn = (cls, label, onClick, aria) => html`<button type="button" class=${"drawer-btn " + cls} disabled=${!!flow.busy} aria-label=${aria || label} onClick=${onClick}>${label}</button>`;

  return html`<section class="dv-gate" role="group" aria-label=${"Merge gate for " + showControls(clip(text(branch.branch), 80))}>
    <div class="dv-gate-h"><b>Merge gate</b><span class=${"dv-chip dv-gate-state dv-gate-state--" + g.state}>${stateWords}</span>
      ${canAct ? null : html`<span class="dv-gate-ro">Read-only here</span>`}</div>
    <ul class="dv-gate-rows" aria-label="What the gate checks">
      <${Row} label="Checks" tone=${checks.tone} action=${actChecks && checks.tone !== "pass" ? btn("dv-gate-run", checks.total ? "Run again" : "Run checks", flow.runChecks, "Run the checks again") : null}><bdi>${checksWords(checks)}</bdi><//>
      <${Row} label="Conflicts" tone=${conflict.tone}>${conflict.text ? html`<bdi>${clip(conflict.text, 300)}</bdi>` : "None with the base or another branch"}<//>
      <${Row} label="Reviewer" tone=${review.tone}
        action=${actChecks && reviewer && (review.state === "none" || review.state === "stale") ? btn("dv-gate-ask", "Ask " + name(reviewer) + " to review", () => flow.askReview(reviewer)) : null}>${review.state === "changes" ? html`<bdi>${name(review.who)}</bdi> asked for changes${review.note ? html`: <bdi>${clip(review.note, 200)}</bdi>` : ""}`
          : review.state === "approved" ? html`<bdi>${name(review.who)}</bdi> approved`
          : review.state === "requested" ? html`Waiting for <bdi>${name(review.who)}</bdi>`
          : review.state === "stale" ? "The earlier sign-off is out of date" : "No reviewer yet. This does not block the merge."}<//>
      <${Row} label="Comments" tone=${comments.tone}>${comments.open ? comments.open + (comments.open === 1 ? " open comment" : " open comments") : "No open review comments"}<//>
    </ul>
    ${otherBlockers(view).map((b, i) => html`<p key=${i} class="dv-gate-also"><bdi>${clip(text(b.text) || b.kind, 200)}</bdi></p>`)}
    ${actChecks && flow.ask
      ? html`<${Confirm} title="Run these checks?" words=${confirmWords(flow.ask, project)} label="Confirm and run" onConfirm=${flow.confirm} onCancel=${flow.cancel} />`
      : null}
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
