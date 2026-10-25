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
  gateOf, checksSummary, checksWords, reviewSummary, commentsSummary, conflictSummary, otherBlockers, approveState,
  approveCommand, unapproveCommand, checksCommand, reviewCommand, gateRefusalWords,
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
  assert.match(checksWords(checksSummary({ checks: [{ name: "a", state: "fail" }, { name: "b", state: "fail" }] })), /a and 1 more are failing/);
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
  assert.match(gateRefusalWords(blocked), /search also changes/, "the desk's own sentence for a blocked approval");
});

const draw = (props) => renderToString(html`<${MergeGate} ...${props} />`);

test("a blocked branch shows four rows in words, what else blocks it, and a disabled control with the reason beside it", () => {
  const flow = { busy: "", error: "", said: "", gate: null };
  const out = draw({ branch: tax, canAct: true, flow, reviewer: "ec776492", nameOf: (p) => (p === "ec776492" ? "docs" : p) });
  assert.match(out, /role="group" aria-label="Merge gate for agent\/tax-rounding"/);
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
  assert.match(out, /The manager merges it; this does not\./);
  assert.match(out, />Withdraw approval</);
  assert.match(draw({ branch: { ...cart, gate: { state: "merged", blockers: [], mergeSha: "0123456789abcdef" } }, canAct: true, flow: { busy: "" } }), /Merged[\s\S]*01234567/);
});

function Host({ send, result, branch }) {
  const flow = useGateActions({ branch, send, result });
  return html`<${MergeGate} branch=${branch} canAct=${true} flow=${flow} reviewer="ec776492" nameOf=${(p) => p} />`;
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
    m.draw({ send, branch: cart, result: { ...headMoved, req: sent[0].req } });
    await settle();
    assert.match(m.box.find(".dv-live").textContent, /new commit since you looked/);
    assert.equal(m.box.find(".dv-gate-approve").disabled, false, "it can be tried again");
  } finally {
    m.done();
  }
  const off = await mountHost({ send: () => false, branch: cart });
  try {
    off.box.find(".dv-gate-approve").click();
    await settle();
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
    assert.equal(sent[0].cmd, "runChecks");
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
    w.box.find(".dv-gate-approve").click();
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
