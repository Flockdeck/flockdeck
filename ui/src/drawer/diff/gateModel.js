// The merge gate, as data (docs/changes-api.md, sections 6 and 7): what the four
// checks of a branch say, whether the person may approve, and the commands. It
// reads one entry of a `changesTeam` push (a branch) and sends nothing. The desk
// decides the gate; this only reads its `gate` and says it in words.
//
// A branch is { pane, branch, base, headSha, checks, reviews, comments, gate, pr }.
// gate is { state ("blocked" | "ready" | "approved" | "merged"), blockers
// [{ kind, text }], approvedBy, approvedAt, approvedSha, mergeSha }.

import { showControls } from "./diffModel.js";

const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const list = (v) => (Array.isArray(v) ? v : []);
const text = (v) => (typeof v === "string" ? v : "");

/** gateOf is the branch's gate, or a blocked one with no reasons when the desk sent none. */
export function gateOf(branch) {
  const g = isObject(branch) && isObject(branch.gate) ? branch.gate : {};
  const state = ["blocked", "ready", "approved", "merged"].includes(g.state) ? g.state : "blocked";
  return { ...g, state, blockers: list(g.blockers).filter(isObject) };
}

/**
 * checksSummary reads the checks for the branch's head. A check that ran against
 * another head is out of date and counts as waiting, never as passing.
 */
export function checksSummary(branch) {
  const head = isObject(branch) ? text(branch.headSha) : "";
  const checks = list(isObject(branch) ? branch.checks : []).filter(isObject);
  // A check with no head to show it ran on this one is out of date too.
  const now = checks.map((c) => ({ ...c, current: !!c.atSha && !!head && c.atSha === head }));
  const state = (c) => (!c.current ? "stale" : c.state === "pass" || c.state === "fail" || c.state === "running" || c.state === "queued" ? c.state : "queued");
  const count = (s) => now.filter((c) => state(c) === s).length;
  const failing = now.filter((c) => state(c) === "fail");
  return {
    total: checks.length,
    passing: count("pass"),
    failing: failing.length,
    firstFailing: failing.length ? showControls(text(failing[0].name)) : "",
    waiting: count("running") + count("queued") + count("stale"),
    tone: !checks.length ? "none" : failing.length ? "fail" : count("running") + count("queued") + count("stale") ? "wait" : "pass",
  };
}

/** checksWords is the summary as one line. */
export function checksWords(s) {
  if (!s.total) return "No checks have run for this branch";
  if (s.failing) return s.firstFailing + (s.failing > 1 ? " and " + (s.failing - 1) + " more are" : " is") + " failing";
  if (s.waiting) return s.passing + " of " + s.total + " passing, " + s.waiting + " waiting or out of date";
  return s.total === 1 ? "The check is passing" : "All " + s.total + " checks are passing";
}

/** reviewSummary reads the sign-offs that count: a stale one does not. */
export function reviewSummary(branch) {
  const reviews = list(isObject(branch) ? branch.reviews : []).filter((r) => isObject(r) && !r.stale);
  const asked = reviews.find((r) => r.state === "changes");
  if (asked) return { tone: "fail", who: text(asked.reviewer), note: showControls(text(asked.note)), state: "changes" };
  const ok = reviews.find((r) => r.state === "approved");
  if (ok) return { tone: "pass", who: text(ok.reviewer), note: showControls(text(ok.note)), state: "approved" };
  const wait = reviews.find((r) => r.state === "requested");
  if (wait) return { tone: "wait", who: text(wait.reviewer), note: "", state: "requested" };
  const stale = list(isObject(branch) ? branch.reviews : []).some((r) => isObject(r) && r.stale);
  return { tone: "none", who: "", note: "", state: stale ? "stale" : "none" };
}

/** commentsSummary counts the comments still open. */
export function commentsSummary(branch) {
  const open = list(isObject(branch) ? branch.comments : []).filter((c) => isObject(c) && (c.state === undefined || c.state === "open")).length;
  return { open, tone: open ? "fail" : "pass" };
}

/** conflictSummary reads the conflict blocker, if there is one. */
export function conflictSummary(branch) {
  const b = gateOf(branch).blockers.find((x) => x.kind === "conflict");
  return b ? { tone: "fail", text: showControls(text(b.text)) } : { tone: "pass", text: "" };
}

/** otherBlockers are the blockers the four rows do not show: unpushed and empty. */
export function otherBlockers(branch) {
  return gateOf(branch).blockers.filter((b) => b.kind === "unpushed" || b.kind === "empty").map((b) => ({ ...b, text: showControls(text(b.text)) }));
}

/**
 * approveState says what the approve control is: { kind, enabled, label, why }.
 *   ready: "Approve merge", enabled;
 *   blocked: the same, disabled, and why lists what blocks it;
 *   approved: "Withdraw approval", enabled (the person can take it back);
 *   merged: no control.
 * canAct false (no gate in this build, or a window that may not write) disables it.
 */
export function approveState(branch, canAct) {
  const g = gateOf(branch);
  if (g.state === "merged") return { kind: "merged", enabled: false, label: "", why: "" };
  if (g.state === "approved") return { kind: "approved", enabled: !!canAct, label: "Withdraw approval", why: "" };
  const blocked = g.state !== "ready";
  const why = blocked ? "Blocked: " + (g.blockers.length ? g.blockers.map((b) => showControls(text(b.text))).filter(Boolean).join("; ") : "the desk has not said why") : "";
  return { kind: blocked ? "blocked" : "ready", enabled: !!canAct && !blocked, label: "Approve merge", why };
}

/**
 * mergeState says whether the merge control is on: the gate is approved and the approval is for the head the window
 * shows. { enabled, label, why }. The desk checks again; this keeps a press from being offered that it would refuse.
 */
export function mergeState(branch, canAct) {
  const g = gateOf(branch);
  const base = isObject(branch) ? oneLine(branch.base, 60) : "";
  const label = "Merge into " + (base || "the base");
  if (g.state !== "approved") return { enabled: false, label, why: "" };
  const head = isObject(branch) ? text(branch.headSha) : "";
  if (!head || text(g.approvedSha) !== head) return { enabled: false, label, why: "The approval is for an earlier commit than the branch has now, so it cannot be merged. Withdraw it and approve again." };
  if (!base) return { enabled: false, label, why: "The desk did not say which branch this merges into." };
  return { enabled: !!canAct, label, why: "" };
}

export const approveCommand = ({ pane, branch, expectSha, req }) => ({ cmd: "approveMerge", id: pane, branch, expectSha, req });
export const mergeCommand = ({ pane, branch, expectSha, req }) => ({ cmd: "mergeBranch", id: pane, branch, expectSha, req });
export const unapproveCommand = ({ pane, branch, req }) => ({ cmd: "unapproveMerge", id: pane, branch, req });
export const checksCommand = ({ pane, branch, names = [], req }) => ({ cmd: "runChecks", id: pane, branch, names, req });
// confirmChecks without confirm only asks for the commands; with confirm and the hash that was shown it records them (section 6).
export const confirmChecksCommand = ({ pane, branch, confirm = false, hash = "", req }) => ({ cmd: "confirmChecks", id: pane, branch, ...(confirm ? { confirm: true, hash } : {}), req });
// requestReview does not take a branch (docs/changes-api.md, section 3).
export const reviewCommand = ({ pane, reviewer, note = "", req }) => ({ cmd: "requestReview", id: pane, reviewer, text: note, req });

const NAME_CLIP = 60;

/** clipText cuts a text to at most max code points, ending in an ellipsis, so a surrogate pair is never split. */
export function clipText(v, max) {
  const chars = Array.from(String(v == null ? "" : v));
  return chars.length <= max ? chars.join("") : chars.slice(0, Math.max(0, max - 1)).join("") + "…";
}

/** oneLine shows a command or a name as a single line of text: the hidden characters the page marks everywhere
 *  (showControls), line breaks, other control characters and the spaces that look like a space but are not one
 *  (no-break, en and em spaces, the ideographic space) are drawn as marks rather than acted on. The odd spaces are
 *  marked here and not in showControls because a command is where a space that is not one matters; in prose they are ordinary. */
function oneLine(v, max = Infinity) {
  const t = showControls(text(v))
    .replace(/[\r\n]+/g, " <LF> ")
    .replace(/[\u0000-\u001f\u007f-\u009f\u00a0\u1680\u2000-\u200a\u202f\u205f\u3000]/g, (c) => "<U+" + c.charCodeAt(0).toString(16).toUpperCase().padStart(4, "0") + ">");
  return clipText(t, max);
}

/** checksListOf reads a changesChecks: the commands the check file would run, the hash to send back to confirm them,
 *  and whether they are already confirmed. A name is bounded; a command is shown whole (the desk caps it).  null when there is nothing to confirm (no
 *  command, or no hash to send back: { fault: "commands" } or { fault: "hash" } says which). The text of a command is the file's, written by whoever could write the file: it is
 *  shown as it is, bounded, and never run or acted on here. */
export function checksListOf(msg) {
  if (!isObject(msg) || msg.type !== "changesChecks") return null;
  const commands = list(msg.commands).filter((c) => isObject(c) && text(c.cmd) !== "")
    .map((c) => ({ name: oneLine(c.name, NAME_CLIP), cmd: oneLine(c.cmd) }));
  const hash = text(msg.hash);
  if (!commands.length) return { fault: "commands" };
  if (!hash) return { fault: "hash" };
  return { commands, hash, pinned: msg.pinned === true };
}

/** confirmWords are the sentences of the question before the checks run: the project, what was asked, each command and
 *  where it runs. They are the page's words around the file's text. */
export function confirmWords(ask, project) {
  const a = isObject(ask) ? ask : {};
  const out = [];
  if (a.note) out.push(a.note);
  if (project) out.push("Project: " + oneLine(project, 120));
  // A command is never cut: the person confirms what will run, and a cut tail would hide part of it.
  for (const c of list(a.commands)) out.push({ name: c.name, cmd: c.cmd });
  out.push("These commands run on this machine, in this branch's checkout, and the checks run code from that checkout. Confirm only if you have read them.");
  return out;
}

/** WARNING_CODES are the answers to mergeBranch that are not plain refusals: something happened that the person has to know. */
export const WARNING_CODES = ["partlyUpdated", "mergedNotRecorded"];

/** warningWords is what to say for a mergeBranch answer in WARNING_CODES: the desk's own sentence, with a fallback. */
export function warningWords(msg) {
  const m = isObject(msg) ? msg : {};
  const said = clipText(showControls(text(m.error)), 600);
  if (said) return said;
  return m.code === "partlyUpdated"
    ? "The merge failed part way, so the checkout of the base branch may be partly updated. Look at that checkout before you try again."
    : "The branch was merged, but Flockdeck could not record it, so the gate will not show it as merged.";
}

/** shortSha is the first 8 characters of a commit id, as text. */
export const shortSha = (v) => oneLine(text(v).slice(0, 8));

/**
 * gateConfirmWords are the sentences of the question before an approval or a merge: the branch, the short head and the base,
 * and what the press does. They are the page's words around the names, which come from the repository and are marked.
 */
export function gateConfirmWords(kind, { branch = "", head = "", base = "" } = {}) {
  const b = oneLine(branch, 120) || "this branch";
  const into = oneLine(base, 120) || "its base";
  const sha = shortSha(head);
  if (kind === "merge") {
    return [
      "Merge " + b + " at " + sha + " into " + into + ".",
      "This merges exactly that commit into " + into + ", in the checkout that has " + into + " checked out, so the working tree of that checkout changes.",
      "It makes a merge commit if " + into + " cannot be fast-forwarded. It pushes nothing.",
      "Hooks do not run and the commit is not signed. The merge is refused if the repository's own configuration names a program to run.",
    ];
  }
  return [
    "Approve " + b + " at " + sha + " to be merged into " + into + ".",
    "This records your approval of that commit and tells the manager. It does not merge. A later commit on the branch drops the approval.",
  ];
}

/** gateRefusalWords is what to say for a changesResult that was not ok. For mergeBranch the desk's own sentence is the answer. */
export function gateRefusalWords(msg) {
  const m = isObject(msg) ? msg : {};
  if (text(m.op) === "mergeBranch") {
    const said = clipText(showControls(text(m.error)), 600);
    if (said) return said;
    switch (m.code) {
      case "headMoved": return "The branch has a new commit since you looked, so nothing was merged. Look at it again.";
      case "notApproved": return "The branch is not approved for this commit, so nothing was merged.";
      case "notClean": return "The checkout of the base branch is not clean, so nothing was merged.";
      case "notFastForward": return "The base branch has moved in a way that cannot be merged cleanly, so nothing was merged.";
      case "failed": return "Git refused the merge. Nothing changed.";
      default: break;
    }
  }
  switch (m.code) {
    case "headMoved": return "The branch has a new commit since you looked, so nothing was approved. Look at it again.";
    case "deskOnly": return "The gate is used on the machine Flockdeck runs on, not from this window.";
    case "unsupported": return "This version of Flockdeck has no merge gate yet.";
    case "noBranch": return "That branch is gone.";
    case "notFound": return "That reviewer is not an agent pane in this project.";
    case "unpushed": return "The branch has commits that are not on the remote; push them first.";
    case "empty": return "The branch has no changes to approve.";
    case "checksNotPassing": return "The checks are not all passing for this head.";
    case "conflicts": return "The branch conflicts with its base or another branch; sort that out first.";
    case "openComments": return "The branch still has open review comments.";
    case "reviewChangesRequested": return "A reviewer asked for changes on this head.";
    default: return clipText(showControls(text(m.error)), 400) || "The desk did not do that.";
  }
}

/**
 * actsFor says whether this window may use a cap's commands. It is true only for a
 * full desk window (not a viewer, not reached through the relay) whose desk lists
 * the cap in api.caps. Everything that draws a live control asks this, so a viewer
 * never sees an Approve that looks as if it works.
 */
export function actsFor(need, { viewer = false, relay = false, caps = [] } = {}) {
  return !viewer && !relay && Array.isArray(caps) && caps.includes(need);
}

/** gateActsFor is actsFor for the merge gate's commands. */
export const gateActsFor = (win) => actsFor("changesGate", win);
