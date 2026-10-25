import { useEffect, useMemo, useRef, useState } from "../vendor/hooks.mjs";
import { useInert } from "./inert.js";
import { html } from "../html.js";
import { composing, keycaps, listMove } from "./keys.js";

/** NOT_YET is what a row says when the action is in the key table and this
 *  window does not implement it. */
export const NOT_YET = "not in this window yet";

/** nameOf is the action as the palette calls it: the short name if the table
 *  gives one, otherwise the label up to the dash that introduces its gloss. */
export function nameOf(k) {
  if (k.short) return k.short;
  const i = (k.label || "").indexOf(" — ");
  return i > 0 ? k.label.slice(0, i) : k.label || k.id;
}

function subsequence(needle, hay) {
  let i = 0;
  for (const ch of hay) if (ch === needle[i] && ++i === needle.length) return true;
  return needle.length === 0;
}

/** score ranks how well a query finds an action, lower being better, or -1 for
 *  not at all: the name itself, the start of the name, the start of a word in
 *  it, anywhere in the name, anywhere in the label or id, then the letters in
 *  order in the name. */
function score(k, q) {
  const name = nameOf(k).toLowerCase();
  if (name === q) return 0;
  if (name.startsWith(q)) return 1;
  if (name.split(/[\s\-—]+/).some((w) => w.startsWith(q))) return 2;
  if (name.includes(q)) return 3;
  if ((k.label || "").toLowerCase().includes(q) || (k.id || "").toLowerCase().includes(q)) return 4;
  if (subsequence(q, name)) return 5;
  return -1;
}

/** filterActions is the palette's rows for a query: the actions of the key
 *  table that are meant for a palette, the ones this window implements marked
 *  as such, and the rest kept in and marked, never hidden. An empty query gives
 *  all of them in table order; otherwise the best match comes first and ties
 *  keep table order. */
export function filterActions(table, query, implemented) {
  const q = (query || "").trim().toLowerCase();
  const have = new Set(implemented || []);
  const rows = [];
  (table || []).forEach((k, order) => {
    if (k.noPalette) return;
    const s = q ? score(k, q) : 0;
    if (s < 0) return;
    rows.push({ key: k, name: nameOf(k), implemented: have.has(k.id), score: s, order });
  });
  rows.sort((a, b) => a.score - b.score || a.order - b.order);
  return rows;
}

/** GROUPS are the headings the palette's rows sit under, in order, when the window
 *  has live things to offer: what needs the person, the agents, the views, and the
 *  actions of the key table. */
export const GROUPS = ["Needs you", "Agents", "Views", "Actions"];

/** LIVE_MAX is the most live rows the palette lists, so a project with very many
 *  agents does not push the actions out of sight. */
export const LIVE_MAX = 40;

/** filterLive is the rows for the live items (id, group, name, detail, run) a
 *  window offers: the ones the query finds in their name or their detail, in the
 *  order of GROUPS and then best match first. Every one of them can be run. */
export function filterLive(items, query) {
  const q = (query || "").trim().toLowerCase();
  const rows = [];
  (items || []).forEach((it, order) => {
    const name = String(it.name || "").toLowerCase();
    const detail = String(it.detail || "").toLowerCase();
    let s = 0;
    if (q) {
      if (name === q) s = 0;
      else if (name.startsWith(q)) s = 1;
      else if (name.includes(q)) s = 2;
      else if (detail.includes(q)) s = 3;
      else if (subsequence(q, name)) s = 4;
      else return;
    }
    rows.push({ live: it, name: it.name, detail: it.detail || "", implemented: true, score: s, order, group: it.group });
  });
  const rank = (g) => (GROUPS.indexOf(g) < 0 ? GROUPS.length - 1 : GROUPS.indexOf(g));
  rows.sort((a, b) => rank(a.group) - rank(b.group) || a.score - b.score || a.order - b.order);
  return rows.slice(0, LIVE_MAX);
}

/** rowKey is the id of a row's action or live item. */
const rowKey = (r) => (r.live ? r.live.id : r.key.id);

/** optionId is the id of a row, which the field names as the current one. */
export const optionId = (actionId) => "palette-opt-" + actionId;

/** Palette is the command palette: a field, and under it the actions that match
 *  what was typed. Enter runs the chosen one if this window implements it. A row
 *  for one it does not says so and does nothing.
 *
 *  It is a modal dialog. The keyboard goes to its field when it opens and back
 *  to where it was when it closes; Tab stays inside it; Escape closes it from
 *  the field or from a row; and the field names the current row for a screen
 *  reader without leaving the field. */
export function Palette({ keys, implemented, onRun, onClose, live = null, onRunLive = null }) {
  const [query, setQuery] = useState("");
  const [index, setIndex] = useState(0);
  const input = useRef(null);
  const box = useRef(null);
  useInert(box);
  const rows = useMemo(() => {
    const actions = filterActions(keys, query, implemented).map((r) => ({ ...r, group: "Actions" }));
    return live && live.length ? filterLive(live, query).concat(actions) : actions;
  }, [keys, query, implemented, live]);
  const grouped = !!(live && live.length);
  const at = Math.min(index, Math.max(rows.length - 1, 0));

  useEffect(() => {
    const el = input.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    if (el && el.focus) el.focus();
    return () => {
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);

  const run = (row) => {
    if (!row || !row.implemented) return;
    if (row.live) {
      if (onRunLive) onRunLive(row.live);
    } else if (onRun) onRun(row.key.id);
  };
  const onKeyDown = (e) => {
    // A key pressed while an input method is composing belongs to it.
    if (composing(e)) return;
    const move = listMove(e, at, rows.length);
    if (move === "") return;
    e.preventDefault();
    if (move === "tab") return; // the field is the only thing to reach: stay on it
    if (move === "cancel") onClose && onClose();
    else if (move === "accept") run(rows[at]);
    else setIndex(move);
  };

  return html`
    <div class="palette-scrim" onMouseDown=${(e) => e.target === e.currentTarget && onClose && onClose()}>
      <div class="palette" role="dialog" aria-modal="true" aria-label="Command palette" ref=${box} onKeyDown=${onKeyDown}>
        <input
          ref=${input}
          class="palette-input"
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          aria-activedescendant=${rows[at] ? optionId(rowKey(rows[at])) : undefined}
          aria-label="Find an action"
          placeholder="Find an action"
          autocomplete="off"
          spellcheck="false"
          value=${query}
          onInput=${(e) => {
            setQuery(e.target.value);
            setIndex(0);
          }}
        />
        ${rows.length === 0
          ? html`<p class="palette-empty">Nothing matches.</p>`
          : html`<ul id="palette-list" class="palette-list" role="listbox">
              ${rows.map((r, i) => html`${grouped && (i === 0 || rows[i - 1].group !== r.group) ? html`<li key=${"group-" + r.group} class="palette-group" role="presentation">${r.group}</li>` : null}<${Row} key=${rowKey(r)} row=${r} current=${i === at} onRun=${() => run(r)} />`)}
            </ul>`}
      </div>
    </div>
  `;
}

/** keepFocus stops a press on a row from taking the keyboard out of the field,
 *  which a row (not a control) would otherwise do. */
const keepFocus = (e) => e.preventDefault();

function Row({ row, current, onRun }) {
  const cls = "palette-row" + (current ? " is-current" : "") + (row.implemented ? "" : " palette-row--off");
  return html`
    <li id=${optionId(rowKey(row))} class=${cls} role="option" aria-selected=${current ? "true" : "false"} aria-disabled=${row.implemented ? undefined : "true"} data-action=${rowKey(row)} onMouseDown=${keepFocus} onClick=${onRun}>
      <span class="palette-name">${row.name}</span>
      ${row.live && row.detail ? html`<span class="palette-note">${row.detail}</span>` : null}
      ${row.implemented ? null : html`<span class="palette-note">${NOT_YET}</span>`}
      ${row.key && row.key.keys ? html`<span class="palette-keys">${keycaps(row.key.keys).join("+")}</span>` : null}
    </li>
  `;
}
