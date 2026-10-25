// The team-wide Changes view's data: every agent's branch in a project, as the desktop
// sends them in a changesTeam push (docs/changes-api.md), read defensively. Every
// string in it came from an agent, a git repository or a person, so the views draw
// it as text. The commands are the desk's: the desktop refuses them to a window
// reached through the relay and the store does not send them from one.

export const CHANGES_TEAM_CAP = "changesTeam";
export const CHANGES_RELAY_WORDS = "The team-wide Changes view is used on the machine Flockdeck runs on, not from a window reached through the relay.";

const str = (v) => (typeof v === "string" ? v : "");
const count = (v) => (Number.isInteger(v) && v > 0 ? v : 0);
const list = (v) => (Array.isArray(v) ? v : []);
const obj = (v) => (v && typeof v === "object" ? v : {});

const CHECK_STATES = new Set(["queued", "running", "pass", "fail", "skipped"]);
const REVIEW_STATES = new Set(["requested", "approved", "changes", "dismissed"]);
const GATE_STATES = new Set(["blocked", "ready", "approved", "merged"]);

function fileOf(f) {
  const x = obj(f);
  return {
    path: str(x.path), oldPath: str(x.oldPath), status: str(x.status), added: count(x.added), removed: count(x.removed),
    binary: x.binary === true, generated: x.generated === true, staged: x.staged === true, owner: str(x.owner),
    similarity: count(x.similarity), sizeBefore: count(x.sizeBefore), sizeAfter: count(x.sizeAfter),
    conflictWith: list(x.conflictWith).filter((c) => c && typeof c.pane === "string").map((c) => ({ pane: c.pane, path: str(c.path) })),
  };
}

function commentOf(c) {
  const x = obj(c);
  return {
    id: str(x.id), pane: str(x.pane), path: str(x.path), side: x.side === "old" ? "old" : "new", line: count(x.line), text: str(x.text),
    author: str(x.author), to: str(x.to), delivery: str(x.delivery), state: str(x.state), createdAt: str(x.createdAt),
  };
}

/** branchOf reads one entry of a push. An entry with an error is a tree that could not be read. */
export function branchOf(b) {
  const x = obj(b);
  const pr = x.pr && typeof x.pr === "object"
    ? { number: count(x.pr.number), url: str(x.pr.url), state: str(x.pr.state), title: str(x.pr.title), mergeable: str(x.pr.mergeable), reviewDecision: str(x.pr.reviewDecision) }
    : null;
  const last = x.lastCommit && typeof x.lastCommit === "object" ? { sha: str(x.lastCommit.sha), subject: str(x.lastCommit.subject), at: str(x.lastCommit.at) } : null;
  const gate = x.gate && typeof x.gate === "object"
    ? {
      state: GATE_STATES.has(x.gate.state) ? x.gate.state : "blocked",
      blockers: list(x.gate.blockers).filter((k) => k && typeof k.kind === "string").map((k) => ({ kind: k.kind, text: str(k.text) })),
      approvedSha: str(x.gate.approvedSha), approvedAt: str(x.gate.approvedAt),
    }
    : null;
  return {
    pane: str(x.pane), cwd: str(x.cwd), error: str(x.error), branch: str(x.branch), base: str(x.base), baseSha: str(x.baseSha), headSha: str(x.headSha),
    upstream: str(x.upstream), ahead: count(x.ahead), behind: count(x.behind), uncommitted: count(x.uncommitted), untracked: count(x.untracked),
    lastCommit: last, taskId: str(x.taskId), pr, omitted: count(x.omitted),
    files: list(x.files).map(fileOf).filter((f) => f.path),
    checks: list(x.checks).filter((c) => c && CHECK_STATES.has(c.state)).map((c) => ({ name: str(c.name), state: c.state, summary: str(c.summary), source: str(c.source), atSha: str(c.atSha) })),
    reviews: list(x.reviews).filter((r) => r && REVIEW_STATES.has(r.state)).map((r) => ({ reviewer: str(r.reviewer), state: r.state, note: str(r.note), atSha: str(r.atSha), stale: r.stale === true })),
    comments: list(x.comments).map(commentOf).filter((c) => c.id),
    gate,
  };
}

/** changesTeamOf reads a changesTeam push, or null before the desktop has sent one. */
export function changesTeamOf(msg) {
  if (!msg || typeof msg !== "object") return null;
  const p = msg.partial && typeof msg.partial === "object" ? { read: count(msg.partial.read), total: count(msg.partial.total) } : null;
  return {
    root: str(msg.root), project: str(msg.project), rev: Number.isInteger(msg.rev) ? msg.rev : 0, partial: p,
    branches: list(msg.branches).map(branchOf).filter((b) => b.pane),
  };
}

/** RANK orders what a branch needs, the lowest first: a person's attention, then ready, then the rest. */
export const RANK = { conflict: 0, checks: 1, review: 2, comments: 3, ready: 4, rest: 5 };

/** attentionOf is the rank of a branch and the words for it. A branch without a gate (an older desktop) is ranked from its files, checks and reviews. */
export function attentionOf(b) {
  if (b.error) return { rank: RANK.rest, key: "error", text: b.error };
  const kinds = b.gate ? b.gate.blockers.map((k) => k.kind) : [];
  if (kinds.includes("conflict") || b.files.some((f) => f.conflictWith.length)) return { rank: RANK.conflict, key: "conflict", text: "Conflicts" };
  if (b.checks.some((c) => c.state === "fail")) return { rank: RANK.checks, key: "checks", text: "Checks failing" };
  if (b.reviews.some((r) => r.state === "changes" && !r.stale)) return { rank: RANK.review, key: "review", text: "Changes requested" };
  if (kinds.includes("comments") || b.comments.some((c) => c.state === "open")) return { rank: RANK.comments, key: "comments", text: "Open comments" };
  if (b.gate && (b.gate.state === "ready" || b.gate.state === "approved")) return { rank: RANK.ready, key: "ready", text: b.gate.state === "approved" ? "Approved" : "Ready" };
  return { rank: RANK.rest, key: "rest", text: "" };
}

/** sortBranches puts the branches that need attention first, keeping the desktop's order within a rank. */
export function sortBranches(branches) {
  return branches.map((b, i) => ({ b, i, r: attentionOf(b).rank })).sort((p, q) => p.r - q.r || p.i - q.i).map((p) => p.b);
}

export const LADDER_NAMES = ["Uncommitted", "Committed", "Pushed", "Merged"];

/** stageOf is the furthest step a branch's work has reached: 0 only changes in the tree, 1 committed, 2 pushed, 3 merged. The base branch itself has no stage beyond 0. */
export function stageOf(b) {
  if ((b.gate && b.gate.state === "merged") || (b.pr && b.pr.state === "merged")) return 3;
  if (b.branch !== "" && b.branch === b.base) return 0;
  if (b.upstream && b.ahead === 0) return 2;
  if (b.ahead > 0 || b.upstream) return 1;
  return 0;
}

/** ladderOf is the four steps as "done", "now" or "todo": those before the stage are done, the stage is now. */
export function ladderOf(b) {
  const at = stageOf(b);
  return LADDER_NAMES.map((_, i) => (i < at ? "done" : i === at ? "now" : "todo"));
}

/** totalsOf adds up the files and lines of the branches that were read. */
export function totalsOf(branches) {
  const t = { branches: 0, files: 0, added: 0, removed: 0, attention: 0 };
  for (const b of branches) {
    if (b.error) continue;
    t.branches++;
    t.files += b.files.length + b.omitted;
    for (const f of b.files) { t.added += f.added; t.removed += f.removed; }
    if (attentionOf(b).rank < RANK.ready) t.attention++;
  }
  return t;
}

/** barOf is how much of a branch's changed lines were added, 0 to 1, or -1 when none changed. */
export function barOf(b) {
  let a = 0, r = 0;
  for (const f of b.files) { a += f.added; r += f.removed; }
  return a + r === 0 ? -1 : a / (a + r);
}

/** conflictPanes names the other agents a branch's files clash with, once each. */
export function conflictPanes(b) {
  const out = [];
  for (const f of b.files) for (const c of f.conflictWith) if (!out.includes(c.pane)) out.push(c.pane);
  return out;
}

/** isStale says a push is no newer than the one drawn: the same project and a revision not above it. */
export const isStale = (cur, next) => !!cur && !!next && cur.root === next.root && next.rev > 0 && next.rev <= cur.rev;
