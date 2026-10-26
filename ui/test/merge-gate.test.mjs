import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { render } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { MergeGate, useGateActions } from "../src/drawer/diff/MergeGate.js";
import {
  gateActsFor, actsFor, gateOf, checksSummary, checksWords, reviewSummary, commentsSummary, conflictSummary, otherBlockers, approveState,
  approveCommand, unapproveCommand, checksCommand, reviewCommand, gateRefusalWords, clipText, checksListOf,
  mergeCommand, mergeState, gateConfirmWords, warningWords, WARNING_CODES,
} from "../src/drawer/diff/gateModel.js";
import { createDom, install, settle } from "./dom.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", "built-changes-" + name + ".json"), "utf8"));
const team = fixture("01-team-mixed");
const [api, tax, cart, base, broken] = team.branches;
const approved = fixture("10-result-approved");
const headMoved = fixture("11-result-head-moved");
const blocked = fixture("12-result-blocked");
const deskOnly = fixture("14-result-desk-only");

test("the gate is read from the desk's gate, and a missing one is blocked with no reasons", () => {
  assert.equal(gateOf(api).state, "blocked");
  assert.equal(gateOf(cart).state, "ready");
  assert.deepEqual(gateOf(broken), { state: "blocked", blockers: [] });
  assert.equal(gateOf(null).state, "blocked");
  assert.equal(gateOf({ gate: { state: "weird" } }).state, "blocked");
});

test("checks: a failing one is named, a running one waits, and one for another head is out of date", () => {
  assert.equal(checksSummary(api).tone, "fail");
  assert.equal(checksWords(checksSummary(api)), "go test ./internal/limit is failing");
  assert.equal(checksSummary(tax).tone, "wait");
  assert.equal(checksSummary(cart).tone, "pass");
  assert.equal(checksWords(checksSummary(cart)), "The check is passing");
  const old = { headSha: "bbb", checks: [{ name: "t", state: "pass", atSha: "aaa" }] };
  assert.equal(checksSummary(old).tone, "wait", "never pass for a head it did not run on");
  assert.match(checksWords(checksSummary(old)), /0 of 1 passing, 1 waiting or out of date/);
  assert.equal(checksSummary({}).tone, "none");
  assert.match(checksWords(checksSummary({})), /No checks have run/);
  assert.match(checksWords(checksSummary({ headSha: "h", checks: [{ name: "a", state: "fail", atSha: "h" }, { name: "b", state: "fail", atSha: "h" }] })), /a and 1 more are failing/);
});

test("reviews: a stale sign-off does not count, changes win over approval, and none does not block", () => {
  assert.deepEqual([reviewSummary(api).state, reviewSummary(api).tone], ["stale", "none"]);
  assert.equal(reviewSummary(tax).state, "changes");
  assert.equal(reviewSummary(cart).state, "approved");
  assert.equal(reviewSummary(base).state, "none");
  assert.equal(reviewSummary({ reviews: [{ reviewer: "r", state: "requested" }] }).state, "requested");
  assert.equal(reviewSummary({ reviews: [{ reviewer: "r", state: "approved" }, { reviewer: "q", state: "changes" }] }).state, "changes");
});

test("comments and conflicts are counted from the listing and the blockers", () => {
  assert.equal(commentsSummary(api).open, 1, "the fixed one is not open");
  assert.equal(commentsSummary(cart).open, 0);
  assert.match(conflictSummary(api).text, /search also changes cfg\/limits\.yaml/);
  assert.equal(conflictSummary(cart).tone, "pass");
  assert.deepEqual(otherBlockers(tax).map((b) => b.kind), ["unpushed"]);
  assert.deepEqual(otherBlockers(base).map((b) => b.kind), ["empty"]);
});

test("the approve control: ready can be pressed, blocked says why, approved can be withdrawn, merged has none", () => {
  assert.deepEqual(approveState(cart, true), { kind: "ready", enabled: true, label: "Approve merge", why: "" });
  assert.equal(approveState(cart, false).enabled, false, "read-only");
  const b = approveState(api, true);
  assert.deepEqual([b.kind, b.enabled], ["blocked", false]);
  assert.match(b.why, /^Blocked: go test .* is failing; search also changes cfg\/limits\.yaml; 1 open comment$/);
  assert.match(approveState({ gate: { state: "blocked" } }, true).why, /has not said why/);
  assert.deepEqual([approveState({ gate: { state: "approved" } }, true).label, approveState({ gate: { state: "approved" } }, true).enabled], ["Withdraw approval", true]);
  assert.equal(approveState({ gate: { state: "merged" } }, true).kind, "merged");
});

test("the commands carry the head the person looked at", () => {
  assert.deepEqual(approveCommand({ pane: "p", branch: "b", expectSha: "abc", req: "r" }), { cmd: "approveMerge", id: "p", branch: "b", expectSha: "abc", req: "r" });
  assert.equal(unapproveCommand({ pane: "p", branch: "b", req: "r" }).cmd, "unapproveMerge");
  assert.deepEqual(checksCommand({ pane: "p", branch: "b", req: "r" }).names, []);
  assert.equal(reviewCommand({ pane: "p", branch: "b", reviewer: "q", req: "r" }).reviewer, "q");
  assert.match(gateRefusalWords(headMoved), /new commit since you looked, so nothing was approved/);
  assert.match(gateRefusalWords(deskOnly), /not from this window/);
  assert.match(gateRefusalWords(blocked), /conflicts with its base/, "its own sentence for the code");
  for (const code of ["unpushed", "empty", "checksNotPassing", "openComments", "reviewChangesRequested"]) {
    assert.doesNotMatch(gateRefusalWords({ code, error: "raw" }), /raw/, code);
  }
  assert.equal(gateRefusalWords({ code: "failed", error: "raw words" }), "raw words", "the desk's error is the fallback");
  assert.equal(reviewCommand({ pane: "p", branch: "b", reviewer: "q", req: "r" }).branch, undefined, "requestReview takes no branch");
});

const draw = (props) => renderToString(html`<${MergeGate} ...${props} />`);

test("a blocked branch shows four rows in words, what else blocks it, and a disabled control with the reason beside it", () => {
  const flow = { busy: "", error: "", said: "", gate: null };
  const out = draw({ branch: tax, canAct: true, canCheck: true, flow, reviewer: "ec776492", nameOf: (p) => (p === "ec776492" ? "docs" : p) });
  assert.match(out, /role="group" tabindex="-1" aria-label="Merge gate for agent\/tax-rounding"/);
  assert.match(out, /Waiting/);
  assert.match(out, /asked for changes|Changes/);
  assert.match(out, /3 commits are not on a remote/);
  assert.match(out, /dv-gate-approve" disabled aria-describedby="dv-gate-why-366c9143"/);
  assert.match(out, /Blocked: 3 commits are not on a remote; docs asked for changes; go test \.\/internal\/tax is still running/);
  assert.match(out, /<p class="dv-live" role="status"><\/p>/);
});

test("a ready branch has an enabled Approve, and without the cap the gate is read-only with no buttons", () => {
  const flow = { busy: "", error: "", said: "", gate: null };
  const ready = draw({ branch: cart, canAct: true, flow });
  assert.match(ready, /Ready/);
  assert.doesNotMatch(ready, /dv-gate-approve" disabled/);
  assert.match(ready, />Approve merge</);
  const ro = draw({ branch: cart, canAct: false, flow });
  assert.match(ro, /Read-only here/);
  assert.doesNotMatch(ro, /<button/);
  assert.doesNotMatch(draw({ branch: cart, canAct: true }), /<button/, "no flow, no buttons");
  assert.equal(draw({ branch: null }), "");
});

test("an approved gate says by whom and at which head, and that it does not merge", () => {
  const out = draw({ branch: { ...cart, gate: approved.gate }, canAct: true, flow: { busy: "", error: "", said: "", gate: null }, nameOf: (p) => (p === "you" ? "You" : p) });
  assert.match(out, /Approved by You at b2c3d4e5/);
  assert.doesNotMatch(out, /manager merges it/);
  assert.match(out, />Merge into main</);
  assert.match(out, />Withdraw approval</);
  assert.match(draw({ branch: { ...cart, gate: { state: "merged", blockers: [], mergeSha: "0123456789abcdef" } }, canAct: true, flow: { busy: "" } }), /Merged[\s\S]*01234567/);
});

// Approve and Merge ask first; the press on the question's button is the one that sends.
async function confirmGate(root) {
  root.find(".confirm").find(".dialog-btn--danger").click();
  await settle();
}

function Host({ send, result, branch, checks, connected = true, session = null }) {
  const flow = useGateActions({ branch, send, result, checks, connected, session });
  return html`<${MergeGate} branch=${branch} canAct=${true} canCheck=${true} flow=${flow} reviewer="ec776492" nameOf=${(p) => p} project="shop" />`;
}

async function mountHost(props) {
  const dom = createDom();
  const restore = install(dom);
  const box = dom.document.createElement("div");
  dom.body.appendChild(box);
  const draw2 = (p) => render(html`<${Host} ...${p} />`, box);
  draw2(props);
  await settle();
  return { box, draw: draw2, done: () => { render(null, box); restore(); } };
}

test("Approve sends approveMerge with the head, shows the answer, and the next push is the truth again", async () => {
  const sent = [];
  const send = (c) => { sent.push(c); return true; };
  const m = await mountHost({ send, branch: cart });
  try {
    m.box.find(".dv-gate-approve").click();
    await settle();
    assert.equal(sent.length, 0, "the press only asks");
    await confirmGate(m.box);
    const { req, ...cmd } = sent[0];
    assert.deepEqual(cmd, { cmd: "approveMerge", id: "6fb40971", branch: "agent/cart-totals", expectSha: cart.headSha });
    assert.equal(m.box.find(".dv-gate-approve").disabled, true, "no second press while the first is out");
    m.draw({ send, branch: cart, result: { ...approved, req } });
    await settle();
    assert.match(m.box.textContent, /Approved\. The manager has been told\./);
    assert.match(m.box.textContent, /Withdraw approval/);
    m.draw({ send, branch: { ...cart, gate: { state: "blocked", blockers: [{ kind: "comments", text: "1 open comment" }] } } });
    await settle();
    assert.match(m.box.textContent, /Blocked/, "the desk's push replaces the answer");
  } finally {
    m.done();
  }
});

test("a refused approval says why and keeps what the desk answered with; a lost connection says so", async () => {
  const sent = [];
  const send = (c) => { sent.push(c); return true; };
  const m = await mountHost({ send, branch: cart });
  try {
    m.box.find(".dv-gate-approve").click();
    await settle();
    await confirmGate(m.box);
    m.draw({ send, branch: cart, result: { ...headMoved, req: sent[0].req } });
    await settle();
    assert.match(m.box.find(".dv-live").textContent, /new commit since you looked/);
    assert.equal(m.box.find(".dv-gate-approve").disabled, true, "off until the branch's head changes");
    assert.match(m.box.textContent, /The branch changed; waiting for the update\./);
    m.draw({ send, branch: { ...cart, headSha: "newhead" } });
    await settle();
    assert.equal(m.box.find(".dv-gate-approve").disabled, false, "the next update is the truth again");
  } finally {
    m.done();
  }
  const off = await mountHost({ send: () => false, branch: cart });
  try {
    off.box.find(".dv-gate-approve").click();
    await settle();
    await confirmGate(off.box);
    assert.match(off.box.find(".dv-live").textContent, /Not connected/);
  } finally {
    off.done();
  }
});

test("Run again and Ask to review send their commands, and Withdraw sends unapproveMerge", async () => {
  const sent = [];
  const send = (c) => { sent.push(c); return true; };
  const m = await mountHost({ send, branch: api });
  try {
    m.box.find(".dv-gate-run").click();
    await settle();
    assert.equal(sent[0].cmd, "confirmChecks", "Run checks asks to see the commands first");
    assert.equal(sent.some((c) => c.cmd === "runChecks"), false);
    m.draw({ send, branch: api, result: { type: "changesResult", req: sent[0].req, op: "runChecks", ok: true } });
    await settle();
    m.box.find(".dv-gate-ask").click();
    await settle();
    assert.deepEqual([sent[1].cmd, sent[1].reviewer], ["requestReview", "ec776492"]);
  } finally {
    m.done();
  }
  const w = await mountHost({ send, branch: { ...cart, gate: approved.gate } });
  try {
    w.box.find(".dv-gate-withdraw").click();
    await settle();
    assert.equal(sent[2].cmd, "unapproveMerge");
  } finally {
    w.done();
  }
});

test("a gate text from an agent is drawn as text", () => {
  const evil = { ...api, gate: { state: "blocked", blockers: [{ kind: "conflict", text: "<img src=x onerror=1>" }] } };
  assert.doesNotMatch(draw({ branch: evil, canAct: true, flow: { busy: "" } }), /<img/);
});

test("the read-only rule is one function: only a full desk window with the gate cap acts", () => {
  assert.equal(gateActsFor({ viewer: false, relay: false, caps: ["changesTeam", "changesGate"] }), true);
  assert.equal(gateActsFor({ viewer: true, relay: false, caps: ["changesGate"] }), false, "a viewer");
  assert.equal(gateActsFor({ viewer: false, relay: true, caps: ["changesGate"] }), false, "a relay window");
  assert.equal(gateActsFor({ caps: ["changesTeam", "changesDiff"] }), false, "a desk without the cap");
  assert.equal(gateActsFor({}), false);
  assert.equal(gateActsFor(), false);
  assert.equal(actsFor("changesDiff", { caps: ["changesDiff"] }), true);
  assert.equal(gateActsFor({ caps: "changesGate" }), false, "caps must be a list");
});

test("a dropped connection ends the wait, with words to check the gate; nothing is sent again", async () => {
  const sent = [];
  const send = (c) => { sent.push(c); return true; };
  const hostWith = (p) => html`<${HostC} ...${p} />`;
  function HostC({ session }) {
    const flow = useGateActions({ branch: cart, send, session });
    return html`<${MergeGate} branch=${cart} canAct=${true} flow=${flow} />`;
  }
  const dom = createDom();
  const restore = install(dom);
  const box = dom.document.createElement("div");
  dom.body.appendChild(box);
  try {
    render(hostWith({ session: 1 }), box);
    await settle();
    box.find(".dv-gate-approve").click();
    await settle();
    await confirmGate(box);
    assert.equal(box.find(".dv-gate-approve").disabled, true);
    render(hostWith({ session: 2 }), box);
    await settle();
    assert.equal(box.find(".dv-gate-approve").disabled, false);
    assert.match(box.find(".dv-live").textContent, /connection dropped; check the gate before trying again/);
    assert.equal(sent.length, 1);
  } finally {
    render(null, box);
    restore();
  }
});

test("approve with no head on the branch sends nothing", async () => {
  const sent = [];
  const m = await mountHost({ send: (c) => { sent.push(c); return true; }, branch: { ...cart, headSha: "" } });
  try {
    m.box.find(".dv-gate-approve").click();
    await settle();
    assert.equal(sent.length, 0);
    assert.match(m.box.find(".dv-live").textContent, /no known head/);
  } finally {
    m.done();
  }
});

test("a check with no head to show it ran on this one is out of date, and skipped is not a state", () => {
  assert.equal(checksSummary({ headSha: "h", checks: [{ name: "t", state: "pass" }] }).tone, "wait");
  assert.equal(checksSummary({ checks: [{ name: "t", state: "pass", atSha: "h" }] }).tone, "wait");
  assert.equal(checksSummary({ headSha: "h", checks: [{ name: "t", state: "skipped", atSha: "h" }] }).tone, "wait");
});

test("agent text in the gate shows hidden characters and is isolated", () => {
  const branch = { ...api, reviews: [{ reviewer: "r1", state: "changes", note: "no‮way" }], gate: { state: "blocked", blockers: [{ kind: "conflict", text: "x‮y" }] } };
  const out = draw({ branch, canAct: true, flow: { busy: "" }, nameOf: () => "bo‮b" });
  assert.doesNotMatch(out, /‮/);
  assert.match(out, /no(&lt;|<)U\+202E>way/);
  assert.match(out, /bo(&lt;|<)U\+202E>b/);
  assert.match(out, /<bdi>/);
});

test("a push with the new head that lands before the headMoved answer does not lock Approve", async () => {
  const sent = [];
  const send = (c) => { sent.push(c); return true; };
  const m = await mountHost({ send, branch: cart });
  try {
    m.box.find(".dv-gate-approve").click();
    await settle();
    await confirmGate(m.box);
    const moved = { ...cart, headSha: "newhead" };
    m.draw({ send, branch: moved });
    await settle();
    m.draw({ send, branch: moved, result: { ...headMoved, req: sent[0].req } });
    await settle();
    assert.equal(m.box.find(".dv-gate-approve").disabled, false, "the window already has the new head");
    assert.doesNotMatch(m.box.textContent, /waiting for the update/);
  } finally {
    m.done();
  }
});

test("Run checks and Ask to review need the checks cap, not the gate's", () => {
  const flow = { busy: "", error: "", said: "", gate: null };
  const out = draw({ branch: api, canAct: true, flow, reviewer: "ec776492" });
  assert.doesNotMatch(out, /dv-gate-run|dv-gate-ask/);
  assert.match(draw({ branch: api, canAct: true, canCheck: true, flow, reviewer: "ec776492" }), /dv-gate-run/);
  assert.equal(actsFor("changesChecks", { caps: ["changesGate"] }), false);
});

// ---- the confirm step for the commands of the check file (docs/changes-api.md, section 6)

// The shapes are the fixtures the desk is held to (api/fixtures 15 to 17); a test changes only the req and what it is about.
const checksFx = fixture("15-checks-to-confirm");
const notConfirmedFx = fixture("16-result-not-confirmed");
const changedFx = fixture("17-result-changed");
const listing = (req, over = {}) => ({ ...checksFx, req, ...over });
const lastOf = (sent, cmd) => sent.filter((c) => c.cmd === cmd).pop();

async function reading(extra = {}) {
  const sent = [];
  const send = (c) => { sent.push(c); return true; };
  const m = await mountHost({ send, branch: api, ...extra });
  m.box.find(".dv-gate-run").click();
  await settle();
  return { m, sent, send };
}

test("Run checks shows the commands, the project and where they run, and sends nothing more until the person confirms", async () => {
  const { m, sent, send } = await reading();
  try {
    assert.deepEqual(sent.map((c) => c.cmd), ["confirmChecks"]);
    assert.equal(sent[0].confirm, undefined);
    assert.match(m.box.textContent, /Working/);
    m.draw({ send, branch: api, checks: listing(sent[0].req) });
    await settle();
    const q = m.box.find(".confirm");
    assert.match(q.textContent, /Run these checks\?/);
    assert.match(q.textContent, /Project: shop/);
    assert.match(q.textContent, /unit/);
    assert.match(q.textContent, /go test \.\/\.\.\./);
    assert.equal(q.findAll(".confirm-cmd").length, 2, "each command is in a monospace element");
    assert.match(q.textContent, /go vet \.\/\.\.\./);
    assert.match(q.textContent, /run code from that checkout/);
    assert.deepEqual(sent.map((c) => c.cmd), ["confirmChecks"], "reading is not confirming");
  } finally {
    m.done();
  }
});

test("only a press on Confirm sends the hash, once, and the checks run when the desk took it", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, checks: listing(sent[0].req) });
    await settle();
    const btn = m.box.find(".dialog-btn--danger");
    btn.click();
    btn.click();
    await settle();
    const confirms = sent.filter((c) => c.confirm);
    assert.equal(confirms.length, 1, "a second press in the same tick sends nothing");
    assert.equal(confirms[0].hash, "6d1f0c2a9b4e8f3d5a7c1e0b2d4f6a8c9e1b3d5f7a9c0e2b4d6f8a1c3e5b7d90");
    assert.deepEqual([confirms[0].id, confirms[0].branch], [api.pane, api.branch]);
    assert.equal(m.box.find(".confirm"), null, "the question is gone once it is answered for");
    assert.equal(sent.some((c) => c.cmd === "runChecks"), false, "not before the desk took it");
    m.draw({ send, branch: api, checks: listing(sent[0].req), result: { type: "changesResult", req: confirms[0].req, op: "confirmChecks", ok: true } });
    await settle();
    assert.equal(sent.filter((c) => c.cmd === "runChecks").length, 1);
    m.draw({ send, branch: api, checks: listing(sent[0].req), result: { type: "changesResult", req: lastOf(sent, "runChecks").req, op: "runChecks", ok: true } });
    await settle();
    assert.match(m.box.textContent, /Checks started\./);
  } finally {
    m.done();
  }
});

test("a file edited while the question was open shows its new commands and sends nothing", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, checks: listing(sent[0].req) });
    await settle();
    m.box.find(".dialog-btn--danger").click();
    await settle();
    const confirmReq = sent.filter((c) => c.confirm)[0].req;
    m.draw({ send, branch: api, checks: listing(sent[0].req), result: { ...changedFx, req: confirmReq } });
    await settle();
    const reread = sent[sent.length - 1];
    assert.deepEqual([reread.cmd, reread.confirm], ["confirmChecks", undefined], "it asks to read the file again");
    m.draw({ send, branch: api, checks: listing(reread.req, { commands: [{ name: "unit", cmd: "go test ./... -race" }], hash: "f".repeat(64) }) });
    await settle();
    const q = m.box.find(".confirm");
    assert.match(q.textContent, /check file changed after it was shown/);
    assert.match(q.textContent, /-race/);
    assert.equal(sent.filter((c) => c.confirm).length, 1, "the old confirmation is not sent again");
    assert.equal(sent.some((c) => c.cmd === "runChecks"), false);
    m.box.find(".dialog-btn--danger").click();
    await settle();
    assert.equal(sent.filter((c) => c.confirm).pop().hash, "f".repeat(64), "only the new list is confirmed, by its own hash");
  } finally {
    m.done();
  }
});

test("Cancel and Escape drop the question and send nothing, and Run checks can be asked again", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, checks: listing(sent[0].req) });
    await settle();
    const cancel = m.box.findAll(".dialog-btn").find((b) => b.textContent === "Cancel");
    assert.equal(m.box.ownerDocument.activeElement, cancel, "the keyboard starts on Cancel, so a stray Enter does not confirm");
    m.box.find(".confirm").dispatch("keydown", { key: "Escape", ctrlKey: false, shiftKey: false, altKey: false });
    await settle();
    assert.equal(m.box.find(".confirm"), null);
    assert.equal(sent.filter((c) => c.confirm || c.cmd === "runChecks").length, 0);
    assert.equal(m.box.find(".dv-gate-run").disabled, false, "Run checks can be asked again");
  } finally {
    m.done();
  }
});

test("a question is dropped when the branch changes or the connection drops", async () => {
  const a = await reading();
  try {
    a.m.draw({ send: a.send, branch: api, checks: listing(a.sent[0].req) });
    await settle();
    a.m.draw({ send: a.send, branch: { ...api, branch: "agent/other" }, checks: listing(a.sent[0].req) });
    await settle();
    assert.equal(a.m.box.find(".confirm"), null);
    assert.match(a.m.box.textContent, /question about its checks was dropped/);
    assert.equal(a.sent.filter((c) => c.confirm).length, 0);
  } finally {
    a.m.done();
  }
  const b = await reading();
  try {
    b.m.draw({ send: b.send, branch: api, checks: listing(b.sent[0].req) });
    await settle();
    b.m.draw({ send: b.send, branch: api, checks: listing(b.sent[0].req), connected: false });
    await settle();
    assert.equal(b.m.box.find(".confirm"), null);
    assert.match(b.m.box.textContent, /connection dropped/);
  } finally {
    b.m.done();
  }
});

test("commands that are already confirmed run without a question; an answer for another ask is ignored", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, checks: listing("someone-else") });
    await settle();
    assert.equal(m.box.find(".confirm"), null, "not ours");
    m.draw({ send, branch: api, checks: listing(sent[0].req, { pinned: true }) });
    await settle();
    assert.equal(m.box.find(".confirm"), null);
    assert.equal(lastOf(sent, "runChecks").cmd, "runChecks");
    assert.equal(sent.filter((c) => c.confirm).length, 0);
  } finally {
    m.done();
  }
});

test("a refusal from the desk is said in its words and ends the question", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, result: { type: "changesResult", req: sent[0].req, op: "confirmChecks", ok: false, code: "badRequest", error: 'the command of check "unit" holds the character U+202E, which the window cannot show as it would run; remove it from the check file' } });
    await settle();
    assert.match(m.box.find(".dv-live").textContent, /holds the character U\+202E/);
    assert.equal(m.box.find(".confirm"), null);
    assert.equal(sent.some((c) => c.cmd === "runChecks"), false);
  } finally {
    m.done();
  }
  const n = await reading();
  try {
    n.m.draw({ send: n.send, branch: api, checks: listing(n.sent[0].req, { pinned: true }) });
    await settle();
    n.m.draw({ send: n.send, branch: api, checks: listing(n.sent[0].req, { pinned: true }), result: { ...notConfirmedFx, req: lastOf(n.sent, "runChecks").req } });
    await settle();
    assert.match(n.m.box.find(".dv-live").textContent, /have not been confirmed/);
  } finally {
    n.m.done();
  }
});

test("the commands are drawn as text, with line breaks and hidden characters made visible", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, checks: listing(sent[0].req, { commands: [{ name: "<b>x</b>", cmd: "echo a" + String.fromCharCode(10) + "rm -rf /" + String.fromCharCode(0x202e) }] }) });
    await settle();
    const q = m.box.find(".confirm");
    assert.equal(q.find("b"), null);
    assert.match(q.textContent, /<b>x<\/b>/);
    assert.match(q.find("code").textContent, /^echo a <LF> rm -rf \/<U\+202E>$/);
  } finally {
    m.done();
  }
});

test("a command is shown whole: a thousand characters, and more than twenty commands, are not cut", async () => {
  const { m, sent, send } = await reading();
  try {
    const long = "go test " + "x".repeat(992);
    assert.equal(long.length, 1000);
    const many = Array.from({ length: 25 }, (_, i) => ({ name: "c" + i, cmd: "cmd" + i }));
    m.draw({ send, branch: api, checks: listing(sent[0].req, { commands: [{ name: "long", cmd: long }, ...many] }) });
    await settle();
    const codes = m.box.find(".confirm").findAll("code");
    assert.equal(codes.length, 26, "every command is listed");
    assert.equal(codes[0].textContent, long, "no truncation and no ellipsis");
    assert.equal(codes[25].textContent, "cmd24");
  } finally {
    m.done();
  }
});

test("an ok for the reading is never a reason to run: only a press on Confirm lets the checks run", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, result: { type: "changesResult", req: sent[0].req, op: "confirmChecks", ok: true } });
    await settle();
    assert.equal(sent.some((c) => c.cmd === "runChecks"), false, "an ok for the read's req sends nothing");
    assert.match(m.box.find(".dv-live").textContent, /not asked for, so the checks were not run/);
    assert.equal(m.box.find(".confirm"), null);
  } finally {
    m.done();
  }
});

test("when the question closes the keyboard goes back to Run checks", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, checks: listing(sent[0].req) });
    await settle();
    m.box.find(".confirm").dispatch("keydown", { key: "Escape", ctrlKey: false, shiftKey: false, altKey: false });
    await settle();
    assert.equal(m.box.ownerDocument.activeElement, m.box.find(".dv-gate-run"), "after Cancel");
  } finally {
    m.done();
  }
  const b = await reading();
  try {
    b.m.draw({ send: b.send, branch: api, checks: listing(b.sent[0].req) });
    await settle();
    b.m.box.find(".dialog-btn--danger").click();
    await settle();
    const confirmReq = b.sent.filter((c) => c.confirm)[0].req;
    b.m.draw({ send: b.send, branch: api, result: { type: "changesResult", req: confirmReq, op: "confirmChecks", ok: true } });
    await settle();
    b.m.draw({ send: b.send, branch: api, result: { type: "changesResult", req: lastOf(b.sent, "runChecks").req, op: "runChecks", ok: true } });
    await settle();
    assert.equal(b.m.box.ownerDocument.activeElement, b.m.box.find(".dv-gate-run"), "after Confirm, once the checks have started");
  } finally {
    b.m.done();
  }
});

test("a listing with no hash is not the same fault as one with no command", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, checks: listing(sent[0].req, { hash: "" }) });
    await settle();
    assert.match(m.box.find(".dv-live").textContent, /without a way to confirm them/);
    assert.equal(m.box.find(".confirm"), null);
  } finally {
    m.done();
  }
  const n = await reading();
  try {
    n.m.draw({ send: n.send, branch: api, checks: listing(n.sent[0].req, { commands: [] }) });
    await settle();
    assert.match(n.m.box.find(".dv-live").textContent, /no command to run/);
  } finally {
    n.m.done();
  }
  assert.deepEqual(checksListOf({ type: "changesChecks", commands: [], hash: "h" }), { fault: "commands" });
  assert.deepEqual(checksListOf({ type: "changesChecks", commands: [{ name: "a", cmd: "b" }], hash: "" }), { fault: "hash" });
});

test("after notConfirmed, pressing Run checks reads the file again; nothing is retried by itself", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, checks: listing(sent[0].req, { pinned: true }) });
    await settle();
    const run = lastOf(sent, "runChecks");
    const before = sent.length;
    m.draw({ send, branch: api, checks: listing(sent[0].req, { pinned: true }), result: { ...notConfirmedFx, req: run.req } });
    await settle();
    assert.equal(sent.length, before, "nothing is sent by itself");
    m.box.find(".dv-gate-run").click();
    await settle();
    const again = sent[sent.length - 1];
    assert.deepEqual([again.cmd, again.confirm], ["confirmChecks", undefined], "a fresh read");
    assert.notEqual(again.req, sent[0].req);
  } finally {
    m.done();
  }
});

test("a list that is pinned after a changed note still asks", async () => {
  const { m, sent, send } = await reading();
  try {
    m.draw({ send, branch: api, checks: listing(sent[0].req) });
    await settle();
    m.box.find(".dialog-btn--danger").click();
    await settle();
    const confirmReq = sent.filter((c) => c.confirm)[0].req;
    m.draw({ send, branch: api, result: { ...changedFx, req: confirmReq } });
    await settle();
    const reread = sent[sent.length - 1];
    m.draw({ send, branch: api, checks: listing(reread.req, { pinned: true, hash: "e".repeat(64) }) });
    await settle();
    assert.ok(m.box.find(".confirm"), "it asks");
    assert.equal(sent.some((c) => c.cmd === "runChecks"), false);
  } finally {
    m.done();
  }
});

test("control characters that expand past a thousand characters leave the command whole, and 21 commands are all drawn", async () => {
  const { m, sent, send } = await reading();
  try {
    const cmd = "echo " + String.fromCharCode(0x202e).repeat(300);
    const twentyOne = Array.from({ length: 21 }, (_, i) => ({ name: "n" + i, cmd: "c" + i }));
    m.draw({ send, branch: api, checks: listing(sent[0].req, { commands: [{ name: "x", cmd }, ...twentyOne] }) });
    await settle();
    const codes = m.box.find(".confirm").findAll("code");
    assert.equal(codes.length, 22);
    assert.equal(codes[0].textContent, "echo " + "<U+202E>".repeat(300));
    assert.ok(codes[0].textContent.length > 2000 && !codes[0].textContent.includes("…"));
  } finally {
    m.done();
  }
});

test("a clip never splits a character made of two code units, and a long desk error is bounded", () => {
  const pair = String.fromCodePoint(0x1f600);
  assert.equal(clipText(pair.repeat(5), 3), pair + pair + "…");
  assert.equal(clipText("short", 10), "short");
  const words = gateRefusalWords({ code: "failed", error: pair.repeat(1000) });
  assert.equal(Array.from(words).length, 400);
  assert.ok(!/[\ud800-\udbff]$/.test(words.slice(0, -1)), "no lone half of a pair");
});

test("odd spaces in a command are drawn as marks, and the box does not hide its tail", async () => {
  const { m, sent, send } = await reading();
  try {
    const cmd = "echo a" + String.fromCharCode(0xa0).repeat(3) + "b" + String.fromCharCode(0x3000) + "c" + String.fromCharCode(0x202f) + "d";
    m.draw({ send, branch: api, checks: listing(sent[0].req, { commands: [{ name: "x", cmd }] }) });
    await settle();
    assert.equal(m.box.find("code").textContent, "echo a<U+00A0><U+00A0><U+00A0>b<U+3000>c<U+202F>d");
  } finally {
    m.done();
  }
  const css = readFileSync(join(here, "..", "src", "shell", "start.css"), "utf8");
  const rule = css.split("\n").find((l) => l.startsWith(".confirm-cmd")) || "";
  assert.ok(rule && !/max-height|overflow:\s*(auto|scroll|hidden)/.test(rule), "a long command makes the box taller; it is never scrolled out of sight");
});

// ---- the approval question and the merge (docs/changes-api.md, section 7)
const appr = { ...cart, gate: { state: "approved", blockers: [], approvedBy: "you", approvedAt: "2026-10-26T09:00:00Z", approvedSha: cart.headSha } };
const mergeResult = (req, over = {}) => ({ type: "changesResult", req, op: "mergeBranch", ok: false, ...over });
const esc = { key: "Escape", ctrlKey: false, shiftKey: false, altKey: false };
const asking = async (props = {}) => {
  const sent = [];
  const send = (c) => { sent.push(c); return true; };
  const m = await mountHost({ send, branch: appr, ...props });
  return { m, sent, send };
};
const pressMerge = async (m) => {
  m.box.find(".dv-gate-merge").click();
  await settle();
};
const cancelButton = (m) => m.box.findAll(".dialog-btn").find((b) => b.textContent === "Cancel");

test("Approve asks first, naming the branch, the short head and the base, and sends nothing until Confirm", async () => {
  const sent = [];
  const m = await mountHost({ send: (c) => { sent.push(c); return true; }, branch: cart });
  try {
    m.box.find(".dv-gate-approve").click();
    await settle();
    const q = m.box.find(".confirm");
    assert.match(q.textContent, /Approve this merge\?/);
    assert.match(q.textContent, new RegExp("Approve agent/cart-totals at " + cart.headSha.slice(0, 8) + " to be merged into main"));
    assert.match(q.textContent, /does not merge/);
    assert.equal(sent.length, 0);
    assert.equal(m.box.ownerDocument.activeElement, cancelButton(m));
    q.dispatch("keydown", esc);
    await settle();
    assert.equal(m.box.find(".confirm"), null);
    assert.equal(sent.length, 0, "Escape cancels");
    assert.equal(m.box.ownerDocument.activeElement, m.box.find(".dv-gate-approve"), "the keyboard is back on Approve");
    m.box.find(".dv-gate-approve").click();
    await settle();
    cancelButton(m).click();
    await settle();
    assert.equal(sent.length, 0, "Cancel sends nothing");
  } finally {
    m.done();
  }
});

test("Confirm sends the head the question was asked about, once; the question closes when the head moves first", async () => {
  const sent = [];
  const send = (c) => { sent.push(c); return true; };
  const m = await mountHost({ send, branch: cart });
  try {
    m.box.find(".dv-gate-approve").click();
    await settle();
    m.draw({ send, branch: { ...cart, headSha: "f".repeat(40) } });
    await settle();
    assert.equal(m.box.find(".confirm"), null, "a question about the old head is closed");
    assert.match(m.box.find(".dv-live").textContent, /branch changed, so the question was closed/);
    assert.equal(sent.length, 0);
  } finally {
    m.done();
  }
  const d = await mountHost({ send, branch: cart });
  try {
    d.box.find(".dv-gate-approve").click();
    await settle();
    const btn = d.box.find(".confirm").find(".dialog-btn--danger");
    btn.click();
    btn.click();
    await settle();
    assert.equal(sent.filter((c) => c.cmd === "approveMerge").length, 1, "two presses in one tick send once");
    assert.equal(sent[0].expectSha, cart.headSha);
  } finally {
    d.done();
  }
});

test("an approved gate offers Merge, asks in words that say what it does, and sends mergeBranch with the head once", async () => {
  const { m, sent } = await asking();
  try {
    assert.equal(m.box.find(".dv-gate-merge").textContent, "Merge into main");
    assert.equal(m.box.find(".dv-gate-merge").disabled, false);
    await pressMerge(m);
    const q = m.box.find(".confirm").textContent;
    assert.equal(sent.length, 0);
    assert.match(q, /Merge this branch\?/);
    assert.match(q, new RegExp("Merge agent/cart-totals at " + cart.headSha.slice(0, 8) + " into main"));
    assert.match(q, /exactly that commit into main, in the checkout that has main checked out, so the working tree of that checkout changes/);
    assert.match(q, /merge commit if main cannot be fast-forwarded/);
    assert.match(q, /pushes nothing/);
    const btn = m.box.find(".confirm").find(".dialog-btn--danger");
    assert.equal(btn.textContent, "Confirm and merge");
    btn.click();
    btn.click();
    await settle();
    const merges = sent.filter((c) => c.cmd === "mergeBranch");
    assert.equal(merges.length, 1);
    const { req, ...cmd } = merges[0];
    assert.deepEqual(cmd, { cmd: "mergeBranch", id: appr.pane, branch: appr.branch, expectSha: cart.headSha });
    assert.equal(m.box.find(".dv-gate-merge").disabled, true, "off while it is out");
    assert.equal(m.box.find(".confirm"), null);
  } finally {
    m.done();
  }
});

test("Merge is off when the approval is for another commit, and without the gate cap there is no control at all", async () => {
  const stale = { ...appr, gate: { ...appr.gate, approvedSha: "e".repeat(40) } };
  const { m, sent } = await asking({ branch: stale });
  try {
    assert.equal(m.box.find(".dv-gate-merge").disabled, true);
    assert.match(m.box.find(".dv-gate-why").textContent, /approval is for an earlier commit than the branch has now/);
    m.box.find(".dv-gate-merge").click();
    await settle();
    assert.equal(m.box.find(".confirm"), null);
    assert.equal(sent.length, 0);
  } finally {
    m.done();
  }
  const ro = draw({ branch: appr, canAct: false, flow: { busy: "" } });
  assert.doesNotMatch(ro, /<button/);
  assert.equal(mergeState(appr, false).enabled, false);
  assert.equal(mergeState({ ...cart, gate: { state: "ready", blockers: [] } }, true).enabled, false, "ready is not approved");
  assert.equal(mergeState({ ...appr, base: "" }, true).enabled, false, "no base, no merge");
  assert.equal(mergeState(appr, true).enabled, true);
});

test("a merge that went through shows the desk's gate as merged and says so", async () => {
  const { m, sent, send } = await asking();
  try {
    await pressMerge(m);
    await confirmGate(m.box);
    const merged = { state: "merged", blockers: [], mergeSha: "0123456789abcdef0123" };
    m.draw({ send, branch: appr, result: mergeResult(sent[0].req, { ok: true, gate: merged }) });
    await settle();
    assert.match(m.box.textContent, /Merged/);
    assert.match(m.box.textContent, /01234567/);
    assert.equal(m.box.find(".dv-gate-merge"), null);
    assert.match(m.box.find(".dv-live").textContent, /Merged\./);
    assert.equal(m.box.find(".dv-gate-warn"), null);
    assert.equal(m.box.ownerDocument.activeElement, m.box.find(".dv-gate"), "the buttons are gone, so the keyboard goes to the gate");
  } finally {
    m.done();
  }
});

test("each refusal of the merge shows the desk's sentence as clipped text, and the control comes back", async () => {
  const cases = [
    ["notClean", "The checkout of main has changes to tracked files: src/a.go."],
    ["notFastForward", "main has moved and cannot be merged cleanly."],
    ["notApproved", "The branch is not approved for this commit."],
    ["headMoved", "The branch has a new commit since you looked."],
    ["failed", "git refused: nothing changed."],
  ];
  for (const [code, error] of cases) {
    const { m, sent, send } = await asking();
    try {
      await pressMerge(m);
      await confirmGate(m.box);
      m.draw({ send, branch: appr, result: mergeResult(sent[0].req, { code, error }) });
      await settle();
      assert.equal(m.box.find(".dv-live").textContent, error, code);
      assert.equal(m.box.find(".dv-gate-warn"), null, code + " is an error and not a warning");
      if (code !== "headMoved") assert.equal(m.box.find(".dv-gate-merge").disabled, false, code);
    } finally {
      m.done();
    }
  }
  const long = "x".repeat(900) + "<img src=x onerror=1>";
  const { m, sent, send } = await asking();
  try {
    await pressMerge(m);
    await confirmGate(m.box);
    m.draw({ send, branch: appr, result: mergeResult(sent[0].req, { code: "failed", error: long }) });
    await settle();
    assert.equal(Array.from(m.box.find(".dv-live").textContent).length, 600);
    assert.equal(m.box.find("img"), null);
  } finally {
    m.done();
  }
  assert.match(gateRefusalWords({ op: "mergeBranch", code: "notClean" }), /not clean/);
  assert.match(gateRefusalWords({ op: "mergeBranch", code: "failed" }), /Nothing changed/);
  assert.match(gateRefusalWords({ op: "mergeBranch", code: "headMoved" }), /nothing was merged/);
});

test("partlyUpdated and mergedNotRecorded are warnings that stay until dismissed, not plain errors", async () => {
  for (const [code, error, ok] of [
    ["partlyUpdated", "The merge failed part way; main may be partly updated.", false],
    ["mergedNotRecorded", "The branch was merged but the merge could not be recorded.", true],
  ]) {
    const { m, sent, send } = await asking();
    try {
      await pressMerge(m);
      await confirmGate(m.box);
      m.draw({ send, branch: appr, result: mergeResult(sent[0].req, { code, error, ok }) });
      await settle();
      const w = m.box.find(".dv-gate-warn");
      assert.ok(w, code);
      assert.equal(w.getAttribute("role"), "alert");
      assert.ok(w.textContent.startsWith("Warning"), code);
      assert.ok(w.textContent.includes(error), code);
      assert.equal(m.box.find(".dv-live").textContent, "", code + " is not said as an error or as success");
      m.draw({ send, branch: { ...appr } });
      await settle();
      assert.ok(m.box.find(".dv-gate-warn"), "a push does not clear it");
      m.box.find(".dv-gate-dismiss").click();
      await settle();
      assert.equal(m.box.find(".dv-gate-warn"), null);
    } finally {
      m.done();
    }
  }
  assert.match(warningWords({ code: "partlyUpdated" }), /partly updated/);
  assert.match(warningWords({ code: "mergedNotRecorded" }), /could not record/);
  assert.deepEqual(WARNING_CODES, ["partlyUpdated", "mergedNotRecorded"]);
});

test("a second merge attempt clears an old warning, and a lost connection says so without sending again", async () => {
  const { m, sent, send } = await asking();
  try {
    await pressMerge(m);
    await confirmGate(m.box);
    m.draw({ send, branch: appr, result: mergeResult(sent[0].req, { code: "partlyUpdated", error: "partly" }) });
    await settle();
    assert.ok(m.box.find(".dv-gate-warn"));
    await pressMerge(m);
    await confirmGate(m.box);
    assert.equal(m.box.find(".dv-gate-warn"), null);
    assert.equal(sent.filter((c) => c.cmd === "mergeBranch").length, 2);
  } finally {
    m.done();
  }
  const off = await mountHost({ send: () => false, branch: appr });
  try {
    await pressMerge(off);
    await confirmGate(off.box);
    assert.match(off.box.find(".dv-live").textContent, /Not connected/);
  } finally {
    off.done();
  }
  const drop = await asking();
  try {
    await pressMerge(drop.m);
    await confirmGate(drop.m.box);
    drop.m.draw({ send: drop.send, branch: appr, connected: false });
    await settle();
    assert.match(drop.m.box.find(".dv-live").textContent, /connection dropped; check the gate/);
    assert.equal(drop.sent.length, 1);
    assert.equal(drop.m.box.find(".dv-gate-merge").disabled, false);
  } finally {
    drop.m.done();
  }
});

test("the question words mark hidden characters in names and keep long ones short enough to read", () => {
  const bidi = String.fromCharCode(0x202e);
  const w = gateConfirmWords("merge", { branch: "agent/a" + bidi + "b", head: "0123456789abcdef", base: "ma" + String.fromCharCode(10) + "in" });
  assert.ok(w.join(" ").includes("agent/a<U+202E>b at 01234567 into ma <LF> in"));
  assert.ok(!w.join(" ").includes(bidi));
  assert.equal(gateConfirmWords("approve", { branch: "x", head: "abcdef0123", base: "main" })[0], "Approve x at abcdef01 to be merged into main.");
  assert.ok(Array.from(gateConfirmWords("merge", { branch: "y".repeat(500), head: "h", base: "main" })[0]).length < 200);
  assert.equal(mergeCommand({ pane: "p", branch: "b", expectSha: "s", req: "r" }).cmd, "mergeBranch");
});
