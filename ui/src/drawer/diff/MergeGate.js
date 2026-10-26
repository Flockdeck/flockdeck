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
// "Approve merge" asks first (branch, short head, base), then sends the approval and tells the manager; it does not
// merge. Once approved for the head shown, "Merge into <base>" asks again and sends mergeBranch with that head; a
// merge that left a checkout partly updated, or was made but not recorded, is drawn as a warning that stays until
// dismissed (it is not an error). Approve is disabled with the reason beside it, named by aria-describedby, while the gate
// is blocked. Every state is a word as well as a colour. One status line stays in
// the page, empty first, and is filled with what the last action did.
import { useEffect, useRef, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { Confirm } from "../../shell/Confirm.js";
import { clip } from "../../graph/Node.js";
import { showControls } from "./diffModel.js";
import {
  gateOf, checksSummary, checksWords, reviewSummary, commentsSummary, conflictSummary, otherBlockers, approveState,
  approveCommand, unapproveCommand, mergeCommand, mergeState, gateConfirmWords, warningWords, shortSha, WARNING_CODES, checksCommand, confirmChecksCommand, reviewCommand, gateRefusalWords, checksListOf, confirmWords,
} from "./gateModel.js";

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));
const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const NOT_CONNECTED = "Not connected to Flockdeck. Nothing was sent.";
const DROPPED = "The connection dropped; check the gate before trying again.";
const NO_HEAD = "This branch has no known head, so nothing was approved. Wait for the next update.";
const MOVED = "The branch changed; waiting for the update.";
const GATE_STALE = "The branch changed, so the question was closed. Nothing was sent.";
const RECORDED = "The branch was merged, but the gate has not recorded it. Look at the warning.";
const NOT_APPROVED = "The branch is not approved for the commit shown, so there is nothing to merge.";
const CHANGED_NOTE = "The check file changed after it was shown. These are its commands now; nothing was confirmed.";
const STALE = "The branch changed, so the question about its checks was dropped. Press Run checks to ask again.";
const NO_COMMANDS = "The check file has no command to run.";
const NO_HASH = "The desk sent the commands without a way to confirm them, so nothing was confirmed.";
const UNEXPECTED = "The desk answered in a way that was not asked for, so the checks were not run.";
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
  const [s, setS] = useState({ busy: "", req: "", error: "", said: "", override: null, movedFrom: "", sentSha: "", ask: null, gask: null, warn: "", warnCode: "", warnFor: "", sentFor: "" });
  // A press sets this before the state it causes is drawn, so a second press in the same tick finds it.
  const inflight = useRef(false);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      if (connected !== false) return;
    }
    inflight.current = false;
    setS((p) => (p.req || p.ask || p.gask ? { ...p, busy: "", req: "", error: DROPPED, said: "", override: null, ask: null, gask: null } : p));
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
  // A question about an approval or a merge belongs to the pane, the branch and the head it was asked for, and to
  // the gate state that made it worth asking: a merge while the approval holds for that head, an approval while the gate is ready.
  const headNow = text(branch && branch.headSha);
  const paneNow = text(branch && branch.pane);
  const gateNow = gateOf(branch).state;
  const baseNow = text(branch && branch.base);
  // A merge that was made but not recorded leaves the gate reading as approved; it is not offered again while that warning is up.
  const warnHere = s.warnFor === paneNow + "|" + branchName ? s.warn : "";
  const unrecorded = !!warnHere && s.warnCode === "mergedNotRecorded";
  const mergeOk = mergeState(branch, true).enabled && !unrecorded;
  const staleAsk = (a) => !a || a.pane !== paneNow || a.branch !== branchName || a.head !== headNow || a.base !== baseNow || (a.kind === "merge" ? !mergeOk : gateNow !== "ready");
  useEffect(() => {
    setS((p) => (p.gask && staleAsk(p.gask) ? { ...p, gask: null, error: GATE_STALE, said: "" } : p));
  }, [paneNow, branchName, headNow, baseNow, gateNow, mergeOk]);
  const seen = useRef(null);
  useEffect(() => {
    if (!result || result === seen.current || !s.req || result.req !== s.req) return;
    seen.current = result;
    inflight.current = false;
    const op = text(result.op);
    if (op === "confirmChecks") {
      if (result.ok && s.ask && s.ask.phase === "confirming") {
        // The person's press was taken: the checks run now.
        sendNow(checksCommand(ids()), "runChecks", { ask: null });
      } else if (result.ok) {
        // An ok that is not the answer to a press (the reading, or nothing asked): never a reason to run.
        setS((p) => ({ ...p, busy: "", req: "", ask: null, error: UNEXPECTED, said: "" }));
      } else if (result.code === "changed") {
        // The file is not the one that was shown: read it again, show what it says now, confirm nothing.
        sendNow(confirmChecksCommand(ids()), "confirmChecks", { ask: { phase: "reading", branch: branchName, note: CHANGED_NOTE } });
      } else {
        setS((p) => ({ ...p, busy: "", req: "", ask: null, error: gateRefusalWords(result), said: "" }));
      }
      return;
    }
    // A merge that went wrong in a way the person must act on is a warning, not a refusal: it stays until it is dismissed.
    const warning = op === "mergeBranch" && WARNING_CODES.includes(text(result.code));
    setS((p) => ({
      ...p, busy: "", req: "",
      error: result.ok || warning ? "" : gateRefusalWords(result),
      warn: warning ? warningWords(result) : p.warn,
      warnFor: warning ? p.sentFor : p.warnFor,
      warnCode: warning ? text(result.code) : p.warnCode,
      said: result.ok && !warning ? (op === "approveMerge" ? "Approved. The manager has been told." : op === "unapproveMerge" ? "Approval withdrawn." : op === "mergeBranch" ? "Merged." : op === "runChecks" ? "Checks started." : op === "requestReview" ? "Review asked for." : "Done.") : "",
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
    if (!found || found.fault) {
      setS((p) => ({ ...p, busy: "", req: "", ask: null, error: found && found.fault === "hash" ? NO_HASH : NO_COMMANDS, said: "" }));
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
    setS((p) => ({ ...p, busy, req: cmd.req, sentSha: text(cmd.expectSha), error: "", said: "", warn: cmd.cmd === "mergeBranch" ? "" : p.warn, sentFor: text(cmd.id) + "|" + text(cmd.branch), ...(extra || {}) }));
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
    // Approve and merge are questions first. The press that confirms sends the head the question was asked about.
    gask: s.gask,
    // The warning belongs to the branch the merge was sent for; another branch of the same pane does not show it.
    warn: warnHere,
    mergeHeld: unrecorded,
    dismissWarn: () => setS((p) => (p.warn ? { ...p, warn: "" } : p)),
    askApprove: () => {
      if (s.busy) return false;
      if (!branch || typeof branch.headSha !== "string" || !branch.headSha) {
        setS((p) => ({ ...p, error: NO_HEAD, said: "" }));
        return false;
      }
      setS((p) => ({ ...p, error: "", said: "", ask: null, gask: { kind: "approve", pane: paneNow, branch: branchName, head: branch.headSha, base: text(branch.base) } }));
      return true;
    },
    askMerge: () => {
      const m = mergeState(branch, true);
      if (s.busy || !m.enabled || unrecorded) {
        if (!s.busy) setS((p) => ({ ...p, error: m.why || (unrecorded ? RECORDED : NOT_APPROVED), said: "" }));
        return false;
      }
      setS((p) => ({ ...p, error: "", said: "", ask: null, gask: { kind: "merge", pane: paneNow, branch: branchName, head: branch.headSha, base: text(branch.base) } }));
      return true;
    },
    confirmGate: () => {
      const a = s.gask;
      if (!a || !a.head) return false;
      // The question may be a tick behind the props (its closing effect has not run yet): never send it for another branch.
      if (staleAsk(a)) {
        setS((p) => ({ ...p, gask: null, error: GATE_STALE, said: "" }));
        return false;
      }
      const cmd = (a.kind === "merge" ? mergeCommand : approveCommand)({ pane: a.pane, branch: a.branch, req: newReq(), expectSha: a.head });
      return sendNow(cmd, a.kind === "merge" ? "mergeBranch" : "approveMerge", { gask: null });
    },
    withdraw: () => go(unapproveCommand(ids()), "unapproveMerge"),
    ask: s.ask && s.ask.phase === "asking" ? s.ask : null,
    runChecks: () => go(confirmChecksCommand(ids()), "confirmChecks", { gask: null, ask: { phase: "reading", branch: branchName, note: "" } }),
    confirm: () => {
      const a = s.ask;
      if (!a || a.phase !== "asking" || !a.hash) return false;
      return sendNow(confirmChecksCommand({ ...ids(), confirm: true, hash: a.hash }), "confirmChecks", { ask: { ...a, phase: "confirming" } });
    },
    cancel: () => {
      inflight.current = false;
      setS((p) => (p.ask || p.gask ? { ...p, ...(p.ask ? { busy: "", req: "", ask: null } : {}), gask: null } : p));
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
  // When the question closes, the keyboard goes back to the button that opened it (once it is enabled again).
  // For an approval or a merge it goes to the main button of the footer, or to the gate itself when that button is gone (after a merge).
  const runRef = useRef(null);
  const mainRef = useRef(null);
  const boxRef = useRef(null);
  const wantFocus = useRef("");
  const asking = !!(flow && flow.ask);
  const gasking = !!(flow && flow.gask);
  const busy = !!(flow && flow.busy);
  useEffect(() => {
    if (asking) wantFocus.current = "run";
    else if (gasking) wantFocus.current = "main";
    else if (wantFocus.current && !busy) {
      const which = wantFocus.current;
      wantFocus.current = "";
      const el = (which === "run" ? runRef.current : mainRef.current) || boxRef.current;
      if (el && el.focus) el.focus();
    }
  }, [asking, gasking, busy]);
  // Withdraw and Dismiss remove the button that has the keyboard: it goes to the main button, or the gate.
  const warning = !!(flow && flow.warn);
  const hadWarning = useRef(false);
  useEffect(() => {
    if (hadWarning.current && !warning) {
      const el = mainRef.current || boxRef.current;
      if (el && el.focus) el.focus();
    }
    hadWarning.current = warning;
  }, [warning]);
  const withdraw = () => {
    wantFocus.current = "main";
    return flow.withdraw();
  };
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
  const held = !!(flow && flow.mergeHeld);
  const merge = { ...mergeState(view, canAct && !!flow && !flow.busy && !moved && !held), ...(moved ? { enabled: false } : {}), ...(held ? { enabled: false, why: "The branch was merged, but the gate has not recorded it. See the warning above." } : {}) };
  const name = (p) => showControls(clip(text(nameOf ? nameOf(p) : p), 40));
  const act = canAct && !!flow;
  const actChecks = canCheck && !!flow;
  const whyId = "dv-gate-why-" + text(branch.pane);
  const stateWords = g.state === "blocked" ? (g.blockers.length ? g.blockers.length + " blocking" : "Blocked") : g.state === "ready" ? "Ready" : g.state === "approved" ? "Approved" : "Merged";
  const btn = (cls, label, onClick, aria, ref = null) => html`<button type="button" class=${"drawer-btn " + cls} ref=${ref} disabled=${!!flow.busy} aria-label=${aria || label} onClick=${onClick}>${label}</button>`;

  return html`<section class="dv-gate" role="group" tabindex="-1" ref=${boxRef} aria-label=${"Merge gate for " + showControls(clip(text(branch.branch), 80))}>
    <div class="dv-gate-h"><b>Merge gate</b><span class=${"dv-chip dv-gate-state dv-gate-state--" + g.state}>${stateWords}</span>
      ${canAct ? null : html`<span class="dv-gate-ro">Read-only here</span>`}</div>
    <ul class="dv-gate-rows" aria-label="What the gate checks">
      <${Row} label="Checks" tone=${checks.tone} action=${actChecks && checks.tone !== "pass" ? btn("dv-gate-run", checks.total ? "Run again" : "Run checks", flow.runChecks, "Run the checks again", runRef) : null}><bdi>${checksWords(checks)}</bdi><//>
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
    ${act && flow.gask
      ? html`<${Confirm} title=${flow.gask.kind === "merge" ? "Merge this branch?" : "Approve this merge?"} words=${gateConfirmWords(flow.gask.kind, flow.gask)}
          label=${flow.gask.kind === "merge" ? "Confirm and merge" : "Confirm and approve"} onConfirm=${flow.confirmGate} onCancel=${flow.cancel} />`
      : null}
    ${flow && flow.warn
      ? html`<div class="dv-gate-warn" role="alert"><span class="dv-chip dv-chip--warn">Warning</span> <bdi>${showControls(clip(text(branch.branch), 80)) + ": " + flow.warn}</bdi>
          <button type="button" class="drawer-btn dv-gate-dismiss" onClick=${flow.dismissWarn}>Dismiss</button></div>`
      : null}
    <div class="dv-gate-f">
      ${approve.kind === "merged"
        ? html`<span class="dv-chip dv-tone dv-tone--pass">Merged</span>${g.mergeSha ? html` <span class="dv-mono">${text(g.mergeSha).slice(0, 8)}</span>` : null}`
        : act
          ? approve.kind === "approved"
            ? html`<button type="button" class="drawer-btn dv-gate-merge" ref=${mainRef} disabled=${!merge.enabled} aria-describedby=${whyId} onClick=${flow.askMerge}>${merge.label}</button>
                <button type="button" class="drawer-btn dv-gate-withdraw is-approved" disabled=${!approve.enabled} onClick=${withdraw}>${approve.label}</button>`
            : html`<button type="button" class="drawer-btn dv-gate-approve" ref=${mainRef} disabled=${!approve.enabled} aria-describedby=${whyId} onClick=${flow.askApprove}>${approve.label}</button>`
          : null}
      <span class="dv-gate-why" id=${whyId}>${approve.kind === "approved" ? "Approved" + (g.approvedBy ? " by " + name(g.approvedBy) : "") + (g.approvedSha ? " at " + shortSha(g.approvedSha) : "") + "." + (merge.why ? " " + merge.why : "") : approve.why}</span>
    </div>
    <p class="dv-live" role="status">${said}</p>
  </section>`;
}
