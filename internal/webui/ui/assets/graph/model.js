// The view model of the team graph: pure functions from a graph message to what
// the views draw. There is no DOM, no socket and no clock here. The message is
// described by docs/team-graph.md and api/schema/team-graph.schema.json.
//
// Rules this file keeps:
//
// - Order is the graph's. Teams, nodes, children, edges and needs-you items come
//   out in the order the message has them. team.NeedsYou is the one place that
//   orders the needs, so nothing here sorts them.
// - A field or a value this file does not know is ignored, never an error. The
//   schema says new values may appear (a node kind, an edge kind, a need kind, a
//   waitingOn kind, a delivery state).
// - Every text is untrusted. It is copied as a string and never read as markup or
//   code. A value of the wrong type becomes an empty string or a zero.
// - A message that is not a graph gives an empty model, not a throw.
// - Nothing is recursive and no list of the message is spread into a call, so a
//   chain of 20000 panes or 300000 needs is built, not thrown on.
// - A team whose manager is not a node has tree null. A member the tree does not
//   reach (that case, or a member not linked to its manager) is in the team's
//   unlinked list, so it does not vanish.
//
// Gap in the backend: the graph carries the team of a pane (team, parent,
// children) but not the tab a helper was started in with `spawn -tab`, so there
// is no tab grouping to draw. The model does not guess one.

/** The words a scrubbed prompt is given when a graph is sent through the relay (internal/server/relayscrub.go). */
export const DETAILS_ON_DESKTOP = "details on the desktop";

/** The kinds of needs-you item the graph can carry, in the order the schema lists them. */
export const NEED_KINDS = ["permission", "ask", "returned", "conflict", "down", "setup", "stale-unread", "stuck"];

const NEED_LABELS = {
  permission: "Wants to run something",
  ask: "Asks a question",
  returned: "Task came back from review",
  conflict: "Changes the same file as another pane",
  down: "Ended",
  setup: "Needs setting up",
  "stale-unread": "Has unread mail and is idle",
  stuck: "Is working but has written nothing for a while",
};

/** The kinds drawn without the amber: mail left unread by a pane that is idle, and a pane gone quiet. They are in the box's block and do not make the box loud. */
export const QUIET_KINDS = ["stale-unread", "stuck"];

/** isLoud says a list of needs holds one that asks something of the person now. A pane that ended has its block in red and its box is not drawn loud. */
export function isLoud(needs) {
  return !!needs && needs.some((n) => !QUIET_KINDS.includes(n.kind) && n.kind !== "down");
}

/** A short reason for a kind of need. A kind this file does not know is shown as it is. */
export function needLabel(kind) {
  return Object.prototype.hasOwnProperty.call(NEED_LABELS, kind) ? NEED_LABELS[kind] : str(kind);
}

/** How far a message has got, lowest first. A state not in the list ranks -1. */
export const DELIVERY_ORDER = ["queued", "notified", "woken", "read"];

export function deliveryRank(state) {
  return DELIVERY_ORDER.indexOf(state);
}

function str(v) {
  return typeof v === "string" ? v : "";
}

function num(v) {
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

function list(v) {
  return Array.isArray(v) ? v : [];
}

function ids(v) {
  return list(v).filter((x) => typeof x === "string" && x !== "");
}

function isObject(v) {
  return v !== null && typeof v === "object" && !Array.isArray(v);
}

function needOf(raw, team) {
  const n = isObject(raw) ? raw : {};
  const kind = str(n.kind);
  const text = str(n.text);
  const item = {
    id: str(n.id),
    kind,
    known: NEED_KINDS.includes(kind),
    label: needLabel(kind),
    pane: str(n.pane),
    time: str(n.time),
    text,
    // The text was replaced before it left the desk; there is nothing to show but the fact.
    textHidden: text === DETAILS_ON_DESKTOP,
    team,
  };
  if (str(n.other)) item.other = str(n.other);
  if (str(n.task)) item.task = str(n.task);
  return item;
}

function waitingOf(raw) {
  const w = isObject(raw) ? raw : {};
  return { kind: str(w.kind) || "none", text: str(w.text), ref: str(w.ref) };
}

function nodeOf(raw) {
  const n = isObject(raw) ? raw : {};
  const t = isObject(n.task) ? n.task : null;
  return {
    pane: str(n.pane),
    kind: str(n.kind) || "terminal",
    // A structured pane is driven over a protocol. Its detail stays on the desk.
    structured: n.kind === "structured",
    parent: str(n.parent),
    children: ids(n.children),
    depth: num(n.depth),
    team: str(n.team),
    orphaned: n.orphaned === true,
    waitingOn: waitingOf(n.waitingOn),
    name: str(n.name),
    peer: str(n.peer),
    role: str(n.role),
    agent: str(n.agent),
    shell: n.shell === true,
    status: str(n.status) || "unknown",
    activity: str(n.activity),
    since: str(n.since),
    live: str(n.live),
    branch: str(n.branch),
    dirty: num(n.dirty),
    untracked: num(n.untracked),
    ahead: num(n.ahead),
    behind: num(n.behind),
    task: t ? { id: str(t.id), title: str(t.title), state: str(t.state) } : null,
    unread: num(n.unread),
    autoReview: n.autoReview === true,
    autoApproved: num(n.autoApproved),
    spendUSD: num(n.spendUSD),
    spendPartial: n.spendPartial === true,
    // Messages this pane sent that are still only queued (the push counts them all).
    undelivered: num(n.undelivered),
    lastOutput: str(n.lastOutput),
    // The desk's own figures. stuckOf and rollupOf work these out from the tree
    // and the times and use these only when that is not possible.
    stuck: n.stuck === true,
    below: num(n.below),
    needYou: num(n.needYou),
  };
}

function edgeOf(raw) {
  const e = isObject(raw) ? raw : {};
  return {
    kind: str(e.kind),
    from: str(e.from),
    to: str(e.to),
    id: num(e.id),
    time: str(e.time),
    text: str(e.text),
    delivery: str(e.delivery),
    deliveryRank: deliveryRank(str(e.delivery)),
  };
}

function teamOf(raw, byPane) {
  const t = isObject(raw) ? raw : {};
  const id = str(t.id);
  const team = {
    id,
    manager: str(t.manager),
    members: ids(t.members),
    goal: str(t.goal),
    started: str(t.started),
    spendUSD: num(t.spendUSD),
    spendPartial: t.spendPartial === true,
    spendIncludesPartial: t.spendIncludesPartial === true,
    caps: list(t.caps)
      .filter(isObject)
      .map((c) => ({ pane: str(c.pane), used: num(c.used), max: num(c.max) })),
    needs: list(t.needs).map((n) => needOf(n, id)),
    needsHidden: num(t.needsHidden),
    tree: null,
  };
  const built = treeOf(team.manager || team.id, byPane, team.members);
  team.tree = built.tree;
  // A member the tree does not reach: the manager is not a node, or a member is
  // not linked to it. It is listed here so that it does not vanish.
  team.unlinked = [];
  for (const m of team.members) {
    const n = byPane.get(m);
    if (n && !built.seen.has(m)) team.unlinked.push(n);
  }
  return team;
}

// The tree of a team, built from each node's children, with a pane that names
// the node as its parent added when the node does not list it. A pane appears
// once: a loop or a second parent in a message that is not as the schema says
// cannot repeat a node or run forever. It is built with a loop and a stack, not
// by recursion, so a chain of any length is built. tree is null when the root is
// not a node; seen is every pane the tree holds.
function treeOf(rootId, byPane, members) {
  const seen = new Set();
  const childrenOf = new Map();
  for (const id of members) {
    const n = byPane.get(id);
    if (n && n.parent && byPane.has(n.parent)) {
      if (!childrenOf.has(n.parent)) childrenOf.set(n.parent, []);
      childrenOf.get(n.parent).push(id);
    }
  }
  const frameOf = (id, depth) => {
    const n = byPane.get(id);
    const order = n.children.slice();
    const extra = childrenOf.get(id);
    if (extra) {
      const listed = new Set(order);
      for (const e of extra) if (!listed.has(e)) order.push(e);
    }
    return { node: { pane: id, depth, children: [] }, order, next: 0 };
  };
  if (!byPane.has(rootId)) return { tree: null, seen };
  seen.add(rootId);
  const root = frameOf(rootId, 0);
  const stack = [root];
  while (stack.length > 0) {
    const top = stack[stack.length - 1];
    if (top.next >= top.order.length) {
      stack.pop();
      continue;
    }
    const c = top.order[top.next++];
    if (!byPane.has(c) || seen.has(c)) continue;
    seen.add(c);
    const f = frameOf(c, top.node.depth + 1);
    top.node.children.push(f.node);
    stack.push(f);
  }
  return { tree: root.node, seen };
}

/** A model of nothing, for a message that is not a graph. */
export function emptyModel() {
  return {
    ok: false,
    root: "",
    project: "",
    teams: [],
    nodes: [],
    byPane: new Map(),
    solo: [],
    soloNeeds: [],
    soloNeedsHidden: 0,
    needs: [],
    needsByPane: new Map(),
    edges: { spawn: [], message: [], sent: [] },
    undelivered: [],
    panesHidden: 0,
    sendMessageUnread: 0,
    boardError: "",
  };
}

/**
 * buildGraphModel turns a graph message into what the views draw.
 *
 * - teams: each team with its needs, a tree (pane, depth, children) and the
 *   members the tree does not reach (unlinked).
 * - nodes and byPane: every node, in the message's order, by pane id.
 * - solo: the panes in no team, as nodes. soloNeeds: their needs.
 * - needs: every need, the teams' lists in the teams' order and then the solo
 *   list, each list as the graph gave it.
 * - needsByPane: pane id to its needs, a conflict under both of its panes.
 * - edges: spawn, message and sent edges apart, each in the graph's order.
 * - undelivered: message edges that have not been read, so queued, notified and
 *   woken ones. It is not the amber rule: only "queued" is "not told yet" (see
 *   Edges.js).
 */
export function buildGraphModel(msg) {
  if (!isObject(msg) || msg.type !== "graph") return emptyModel();
  const nodes = list(msg.nodes).map(nodeOf).filter((n) => n.pane !== "");
  const byPane = new Map();
  for (const n of nodes) if (!byPane.has(n.pane)) byPane.set(n.pane, n);

  const teams = list(msg.teams).map((t) => teamOf(t, byPane));
  const soloNeeds = list(msg.soloNeeds).map((n) => needOf(n, ""));
  const needs = [];
  // Loops, not needs.push(...list): a spread of a very long list throws.
  for (const t of teams) for (const n of t.needs) needs.push(n);
  for (const n of soloNeeds) needs.push(n);

  const needsByPane = new Map();
  const note = (id, item) => {
    if (!id) return;
    if (!needsByPane.has(id)) needsByPane.set(id, []);
    needsByPane.get(id).push(item);
  };
  for (const item of needs) {
    note(item.pane, item);
    if (item.other && item.other !== item.pane) note(item.other, item);
  }

  const edges = { spawn: [], message: [], sent: [] };
  for (const raw of list(msg.edges)) {
    const e = edgeOf(raw);
    if (Object.prototype.hasOwnProperty.call(edges, e.kind)) edges[e.kind].push(e);
  }

  return {
    ok: true,
    root: str(msg.root),
    project: str(msg.project),
    teams,
    nodes,
    byPane,
    solo: ids(msg.solo)
      .map((id) => byPane.get(id))
      .filter(Boolean),
    soloNeeds,
    soloNeedsHidden: num(msg.soloNeedsHidden),
    needs,
    needsByPane,
    edges,
    undelivered: edges.message.filter((e) => e.delivery !== "read"),
    panesHidden: num(msg.panesHidden),
    sendMessageUnread: num(msg.sendMessageUnread),
    boardError: str(msg.boardError),
  };
}

/**
 * attentionOf says how much a pane asks of the person, from its needs and what
 * it waits on.
 *
 * level is "needs-you" when a needs-you item names the pane (or the graph says
 * it waits on the user), "down" when it ended, "held" when it waits on a
 * reviewer, a task or helpers, and "none" otherwise, which includes a
 * waitingOn kind this file does not know.
 */
export function attentionOf(model, paneId) {
  const node = model.byPane.get(paneId);
  const needs = model.needsByPane.get(paneId) || [];
  const wait = node ? node.waitingOn : { kind: "none", text: "", ref: "" };
  let level = "none";
  if (needs.length > 0 || wait.kind === "user") level = "needs-you";
  else if (wait.kind === "down") level = "down";
  else if (wait.kind === "reviewer" || wait.kind === "task" || wait.kind === "helpers") level = "held";
  return {
    level,
    needs,
    waitingOn: wait,
    // The question or command is not in the graph; it is on the desktop.
    textHidden: needs.some((n) => n.textHidden) || (wait.kind === "user" && wait.text === DETAILS_ON_DESKTOP),
    structured: node ? node.structured : false,
  };
}

/** The panes that ask something of the person, in the order of the needs-you list. */
export function panesNeedingYou(model) {
  const out = [];
  const seen = new Set();
  for (const item of model.needs) {
    for (const id of [item.pane, item.other]) {
      if (id && !seen.has(id)) {
        seen.add(id);
        out.push(id);
      }
    }
  }
  return out;
}

/** The message edges to and from a pane, in the graph's order (newest first). */
export function messagesOf(model, paneId) {
  const inbound = [];
  const outbound = [];
  for (const e of model.edges.message) {
    if (e.to === paneId) inbound.push(e);
    if (e.from === paneId) outbound.push(e);
  }
  return { inbound, outbound };
}

/**
 * promptOf reads the permission or question a pane view of the state push
 * carries, for a pane drawn in a window. It copes with every shape the push
 * has: a full card, a card scrubbed for the relay (redacted, no buttons), a
 * question that was left out (detailsOnDesktop with no ask) and a structured
 * pane that sends its roster fields only.
 *
 * kind is "permission", "ask", "waiting" (the pane waits and what for was left
 * out) or "none". hidden says what was asked is not in
 * the push and is on the desktop. canAnswerHere says the card has choices to
 * press: false for a scrubbed or structured card, which is answered at the desk.
 */
export function promptOf(pane) {
  const p = isObject(pane) ? pane : {};
  const perm = isObject(p.permission) ? p.permission : null;
  const ask = isObject(p.ask) ? p.ask : null;
  const hidden = p.detailsOnDesktop === true || (perm !== null && perm.redacted === true);
  const options = perm ? list(perm.options).filter(isObject) : [];
  if (perm) {
    return {
      kind: "permission",
      hidden,
      tool: str(perm.tool),
      summary: str(perm.summary),
      options,
      canAnswerHere: !hidden && options.length > 0 && p.structured !== true,
    };
  }
  if (ask) {
    return { kind: "ask", hidden, tool: "", summary: "", options: [], canAnswerHere: false };
  }
  // Waiting with the details left out: the pane waits and says nothing more.
  if (hidden) {
    return { kind: str(p.status) === "waiting" ? "waiting" : "none", hidden, tool: "", summary: "", options: [], canAnswerHere: false };
  }
  return { kind: "none", hidden: false, tool: "", summary: "", options: [], canAnswerHere: false };
}

/** STUCK_MS is how long a working terminal pane may write nothing before it is called stuck. It is the desk's own rule (team.StuckAfter, internal/team/graph.go); keep the two equal. */
export const STUCK_MS = 10 * 60 * 1000;

function timeOf(iso) {
  const t = Date.parse(iso);
  return Number.isFinite(t) && t >= Date.UTC(2000, 0, 1) ? t : NaN;
}

/**
 * stuckOf says a pane is working and has written nothing to its terminal for
 * STUCK_MS: { minutes } (how long it has been quiet), or null. A structured pane
 * has no output stream and is never stuck. A pane with no last-output time is
 * stuck only when the desk said so, and then its minutes are 0 ("" is drawn).
 */
export function stuckOf(node, now) {
  if (!node || node.structured || stateKey(node) !== "working") return null;
  const t = timeOf(node.lastOutput);
  if (Number.isNaN(t)) return node.stuck ? { minutes: 0 } : null;
  const quiet = now - t;
  return quiet >= STUCK_MS ? { minutes: Math.floor(quiet / 60000) } : null;
}

// What a pane is doing, as the outline ranks it: its activity, else its status.
function stateKey(node) {
  return node.activity && node.activity !== "unknown" ? node.activity : node.status;
}

/** needsYou says a pane waits on the person or ended: a need that is not quiet, or a waiting, blocked or ended state. */
export function needsYouOf(model, pane) {
  const n = model.byPane.get(pane);
  if (!n) return false;
  const state = stateKey(n);
  return (model.needsByPane.get(pane) || []).some((x) => !QUIET_KINDS.includes(x.kind)) || state === "waiting" || state === "blocked" || state === "exited" || state === "failed";
}

// The frames of every team's tree by pane, built once per model. The outline's rows
// (Tree.js rowsOf) walk the same frames, so a roll-up counts exactly the rows it hides.
const FRAMES = new WeakMap();
function framesOf(model) {
  let m = FRAMES.get(model);
  if (m) return m;
  m = new Map();
  for (const t of model.teams) {
    if (!t.tree) continue;
    const stack = [t.tree];
    while (stack.length > 0) {
      const f = stack.pop();
      if (m.has(f.pane)) continue;
      m.set(f.pane, f);
      for (const c of f.children) stack.push(c);
    }
  }
  FRAMES.set(model, m);
  return m;
}

/**
 * rollupOf counts the panes below one: { below, needYou }, over the team's tree
 * (the same panes the outline lists under it). It walks with a stack, so a chain
 * of any length is counted. A pane the tree does not hold, or with no helpers in
 * it, is the desk's figures when it sent some.
 */
export function rollupOf(model, pane) {
  const node = model.byPane.get(pane);
  if (!node) return { below: 0, needYou: 0 };
  const frame = framesOf(model).get(pane);
  let below = 0;
  let needYou = 0;
  if (frame) {
    const seen = new Set([pane]);
    const stack = frame.children.slice();
    while (stack.length > 0) {
      const f = stack.pop();
      if (seen.has(f.pane) || !model.byPane.has(f.pane)) continue;
      seen.add(f.pane);
      below++;
      if (needsYouOf(model, f.pane)) needYou++;
      for (const c of f.children) stack.push(c);
    }
  }
  if (below === 0 && node.below > 0) return { below: node.below, needYou: node.needYou };
  return { below, needYou };
}
