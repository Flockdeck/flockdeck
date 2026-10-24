// The team as an outline: one dense row per agent, helpers indented under the
// agent that started them, the rows that need the person first within a level.
// It reads the same model as the canvas (model.js) and draws no edges: who waits
// on whom is a chip on the row (PR 2), not a line.
//
// The outline is one tab stop (the row the keyboard was last on) and follows the
// tree pattern: arrows move between rows, Home and End go to the ends, Enter or
// Space opens the agent's drawer. Esc is the page's: it closes the drawer and
// puts the keyboard back on the row.
//
// Every text here came from an agent, a task or a person and is drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { Fragment } from "../vendor/preact.mjs";
import { html } from "../html.js";
import { composing } from "../shell/keys.js";
import { focusNode } from "../drawer/state.js";
import { stateOf } from "./Node.js";
import { reasonOf, isLoud } from "./Badge.js";
import { TreeRow, TreeHead } from "./TreeRow.js";
import { TreeDecisions } from "./TreeDecisions.js";
import { chipsOf } from "./chips.js";
import { rollupOf, labelOf } from "./model.js";

/** TICK_MS is how often the ages are worked out again. */
export const TICK_MS = 30000;

/** How early a state comes in a level: the person is asked, a pane ended, working, then the rest. */
export function rankOf(node, needs) {
  const key = stateOf(node).key;
  if (key === "waiting" || key === "blocked" || isLoud(needs)) return 0;
  if (key === "exited" || key === "failed" || (needs || []).some((n) => n.kind === "down")) return 1;
  if (key === "working") return 2;
  return 3;
}

// cmp is a compare that tells two Infinity apart as equal (Infinity - Infinity is NaN).
const cmp = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

function sinceOf(node) {
  const t = Date.parse(node.since);
  return Number.isFinite(t) && t >= Date.UTC(2000, 0, 1) ? t : Infinity;
}

/** ageOf is the time since a pane's state began as "now", "2m", "1h" or "3d", or "" when the push has none. */
export function ageOf(since, now) {
  const t = Date.parse(since);
  if (!Number.isFinite(t) || t < Date.UTC(2000, 0, 1)) return "";
  const m = Math.floor((now - t) / 60000);
  if (m < 1) return "now";
  if (m < 60) return m + "m";
  const h = Math.floor(m / 60);
  return h < 48 ? h + "h" : Math.floor(h / 24) + "d";
}

/**
 * rowsOf lists the panes of a model top down: each team's tree, then its members
 * the tree does not reach, then the panes in no team. Within a level the order is
 * rankOf, then the longest in its state first, then the graph's own order (the
 * sort is stable). A row has its pane, depth (0 for a top row), parent pane ("" at
 * the top) and kids, how many helpers it has. A pane in the outline is listed once.
 * It walks with a stack, so a chain of any length is listed.
 */
export function rowsOf(model, collapsed = new Set()) {
  const out = [];
  if (!model || !model.ok) return out;
  const seen = new Set();
  const order = (panes) =>
    panes
      .map((p, i) => ({ p, i, n: model.byPane.get(p.pane || p) }))
      .sort((a, b) => rankOf(a.n, needsOf(model, a.n.pane)) - rankOf(b.n, needsOf(model, b.n.pane)) || cmp(sinceOf(a.n), sinceOf(b.n)) || a.i - b.i)
      .map((x) => x.p);
  const top = [];
  for (const t of model.teams) if (t.tree && model.byPane.has(t.tree.pane)) top.push({ f: t.tree, team: t });
  const walk = (f, depth, parent, team) => {
    const stack = [{ f, depth, parent }];
    while (stack.length > 0) {
      const { f: cur, depth: d, parent: par } = stack.pop();
      if (seen.has(cur.pane) || !model.byPane.has(cur.pane)) continue;
      seen.add(cur.pane);
      const kids = cur.children.filter((c) => model.byPane.has(c.pane) && !seen.has(c.pane));
      const node = model.byPane.get(cur.pane);
      out.push({ pane: cur.pane, depth: d, parent: par, kids: kids.length, collapsed: kids.length > 0 && collapsed.has(cur.pane), team: team && team.manager === cur.pane ? team : null });
      if (collapsed.has(cur.pane)) {
        // Folded: its helpers are not rows, and are not listed later as strays either.
        const hidden = kids.slice();
        while (hidden.length > 0) {
          const h = hidden.pop();
          if (seen.has(h.pane)) continue;
          seen.add(h.pane);
          for (const c of h.children) hidden.push(c);
        }
        continue;
      }
      const sorted = order(kids);
      for (let i = sorted.length - 1; i >= 0; i--) stack.push({ f: sorted[i], depth: d + 1, parent: node.pane });
    }
  };
  for (const { f, team } of top) walk(f, 0, "", team);
  for (const t of model.teams) for (const n of order(t.unlinked)) if (!seen.has(n.pane)) walk({ pane: n.pane, children: [] }, 0, "", null);
  const lone = model.solo.concat(model.nodes.filter((n) => !seen.has(n.pane)));
  for (const n of order(lone)) if (!seen.has(n.pane)) walk({ pane: n.pane, children: [] }, 0, "", null);
  return out;
}

/**
 * stepOf is where a key takes the keyboard: the pane to go to, or "" when the
 * key is not one the outline moves on or there is nowhere to go. rows is rowsOf,
 * from the pane the keyboard is on.
 */
export function stepOf(rows, pane, key) {
  const at = rows.findIndex((r) => r.pane === pane);
  if (at < 0 || rows.length === 0) return "";
  switch (key) {
    case "ArrowDown":
      return rows[Math.min(at + 1, rows.length - 1)].pane;
    case "ArrowUp":
      return rows[Math.max(at - 1, 0)].pane;
    case "Home":
      return rows[0].pane;
    case "End":
      return rows[rows.length - 1].pane;
    case "ArrowRight":
      // A collapsed parent is opened by foldOf; an open one goes to its first helper.
      return !rows[at].collapsed && rows[at + 1] && rows[at + 1].parent === pane ? rows[at + 1].pane : "";
    case "ArrowLeft":
      // An open parent is folded by foldOf; any other row goes to its parent.
      return rows[at].kids > 0 && !rows[at].collapsed ? "" : rows[at].parent;
    default:
      return "";
  }
}

/** foldOf says what a key does to the row it is on: "expand" (Right on a collapsed parent), "collapse" (Left on an open one) or "". */
export function foldOf(rows, pane, key) {
  const r = rows.find((x) => x.pane === pane);
  if (!r || r.kids === 0) return "";
  if (key === "ArrowRight" && r.collapsed) return "expand";
  if (key === "ArrowLeft" && !r.collapsed) return "collapse";
  return "";
}

/** costOf is a pane's own spend as "$3.82"; "" when there is none. A team's spend is not shown on its manager's row. */
export function costOf(node) {
  if (node.spendUSD > 0) return "$" + node.spendUSD.toFixed(2);
  return "";
}

/** totalOf is the spend of every pane of a model, as one plain "$18.70" for the header; "" when nothing is spent. */
export function totalOf(model) {
  if (!model || !model.ok) return "";
  const sum = model.nodes.reduce((t, n) => t + (n.spendUSD > 0 ? n.spendUSD : 0), 0);
  return sum > 0 ? "$" + sum.toFixed(2) : "";
}

/** needsOf is the needs a row is drawn for: those that name the pane first. A conflict is under both of its panes in the model, and the row of the other pane carries a chip, not the block and the tint. */
export function needsOf(model, pane) {
  return (model.needsByPane.get(pane) || []).filter((n) => n.pane === pane);
}

/**
 * doingOf is the line a row shows in "Doing now". A working pane shows its live
 * line (the tool or activity it is in). A pane that waits shows what it waits on,
 * one that ended why. An idle pane shows "idle" and how long, never the last thing
 * it said: that is the row's tooltip (tipOf).
 */
export function doingOf(node, needs, now = Date.now()) {
  const key = stateOf(node).key;
  const ask = (needs || []).find((n) => n.kind !== "stale-unread" && n.kind !== "stuck");
  if (ask && key !== "working") {
    const r = reasonOf(ask);
    return r.detail ? r.word + ": " + r.detail : r.word;
  }
  if (key === "idle") {
    const age = ageOf(node.since, now);
    return age && age !== "now" ? "idle " + age : "idle";
  }
  if (key === "waiting" || key === "blocked") return (node.waitingOn && node.waitingOn.text) || stateOf(node).word;
  return (key === "working" && node.live) || stateOf(node).word;
}

/** tipOf is the row's tooltip for "Doing now": the pane's last line when the column shows something else. */
export function tipOf(node, doing) {
  return node.live && node.live !== doing ? node.live : doing;
}

/** rowNameOf is the label of a row split in two: { role, name }. The role part ("worker · ") is only there when panes share a name; the page shows it where the Role column is gone. */
export function rowNameOf(model, node) {
  const l = labelOf(model, node.pane);
  const lead = node.role ? node.role + " · " : "";
  return lead && l.startsWith(lead) && l.length > lead.length ? { role: lead, name: l.slice(lead.length) } : { role: "", name: l };
}

/** Tree draws a graph model as an outline. onOpen and open are the graph's: a row's drawer is opened by Enter, Space or a click, and open is the pane whose drawer is open. */
export function Tree({ model, onOpen = null, open = "", decisions = null }) {
  const [now, setNow] = useState(() => Date.now());
  const [focus, setFocus] = useState("");
  // The two rows a link chip lights while it is hovered or focused.
  const [lit, setLit] = useState(["", ""]);
  const ref = useRef(null);
  // The panes whose helpers are folded away.
  const [folded, setFolded] = useState(() => new Set());
  const rows = rowsOf(model, folded);
  // The ages move only while there is a row to show them on.
  const any = rows.length > 0;
  useEffect(() => {
    if (!any) return undefined;
    const id = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(id);
  }, [any]);
  // The stop is the row the keyboard was last on, else the first row.
  const stop = rows.some((r) => r.pane === focus) ? focus : rows.length > 0 ? rows[0].pane : "";
  // Folding a pane puts the keyboard on it, so the row it was on is never one that is hidden.
  const toggle = (pane) => {
    setFolded((f) => {
      const next = new Set(f);
      if (!next.delete(pane)) next.add(pane);
      return next;
    });
    setFocus(pane);
  };
  const onKeyDown = (e) => {
    if (e.defaultPrevented || composing(e) || e.ctrlKey || e.altKey || e.shiftKey || e.metaKey) return;
    // The row the key is on: the row itself, or the row a chip of it sits in.
    let el = null;
    for (let n = e.target; n && n !== e.currentTarget; n = n.parentNode) {
      if (n.getAttribute && n.getAttribute("role") === "treeitem") {
        el = n;
        break;
      }
    }
    if (!el) return;
    const pane = el.getAttribute("data-pane");
    // Enter and Space on a chip are the chip's own.
    if ((e.key === "Enter" || e.key === " ") && el !== e.target) return;
    if (e.key === "Enter" || e.key === " ") {
      if (onOpen) {
        e.preventDefault();
        onOpen(pane);
      }
      return;
    }
    const fold = foldOf(rows, pane, e.key);
    if (fold) {
      e.preventDefault();
      toggle(pane);
      return;
    }
    const to = stepOf(rows, pane, e.key);
    if (to === "") {
      if (["ArrowDown", "ArrowUp", "Home", "End"].includes(e.key)) e.preventDefault();
      return;
    }
    e.preventDefault();
    setFocus(to);
    focusNode(e.currentTarget, to);
  };
  // A link chip takes the keyboard to the row it names; focusing scrolls it into view.
  const go = (pane) => {
    setFocus(pane);
    focusNode(ref.current, pane);
  };
  if (rows.length === 0) return null;
  // One agent that leads no team is a lone agent: no header, nothing to fold.
  const solo = rows.length === 1 && !rows[0].team;
  // With panes that share a name the names are longer (what the pane is on), so the column is wider.
  const shared = rows.some((r) => labelOf(model, r.pane) !== (model.byPane.get(r.pane).name || r.pane));
  const noTask = !rows.some((r) => model.byPane.get(r.pane).task);
  const noCost = !rows.some((r) => costOf(model.byPane.get(r.pane)));
  return html`<div class=${"tree" + (shared ? " tree--shared-names" : "") + (noTask ? " tree--no-task" : "") + (noCost ? " tree--no-cost" : "")}>
    ${solo ? null : html`<${TreeHead} />`}
    <div class="tree-rows" role="tree" aria-label="Agents" ref=${ref} onKeyDown=${onKeyDown}>
      ${rows.map((r) => {
        const node = model.byPane.get(r.pane);
        return html`<${Fragment} key=${r.pane}><${TreeRow} row=${r} node=${node} name=${rowNameOf(model, node).name} nameRole=${rowNameOf(model, node).role} needs=${needsOf(model, r.pane)} doing=${doingOf(node, needsOf(model, r.pane), now)} tip=${tipOf(node, doingOf(node, needsOf(model, r.pane), now))} chips=${chipsOf(model, node, needsOf(model, r.pane), now)} roll=${r.collapsed ? rollupOf(model, r.pane) : null} onToggle=${toggle} lit=${lit[0] === r.pane || lit[1] === r.pane} onLink=${go} onLit=${(a, b) => setLit([a, b])} stop=${stop === r.pane}
          selected=${open === r.pane} age=${ageOf(node.since, now)} cost=${costOf(node)}
          onFocus=${() => setFocus(r.pane)} onOpen=${onOpen} />
          ${r.team && r.kids === 0 ? html`<div class="tree-note" role="treeitem" aria-disabled="true" aria-level=${r.depth + 2}>No helpers yet. The manager starts them as it plans.</div>` : null}
          ${decisions ? html`<${TreeDecisions} model=${model} pane=${r.pane} open=${open} stop=${stop === r.pane} onGone=${go} ctx=${decisions} />` : null}
        <//>`;
      })}
    </div>
  </div>`;
}
