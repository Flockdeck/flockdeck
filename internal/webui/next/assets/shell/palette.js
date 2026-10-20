import { useEffect, useMemo, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { keycaps, listMove } from "./keys.js";

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
export function Palette({ keys, implemented, onRun, onClose }) {
  const [query, setQuery] = useState("");
  const [index, setIndex] = useState(0);
  const input = useRef(null);
  const rows = useMemo(() => filterActions(keys, query, implemented), [keys, query, implemented]);
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
    if (row && row.implemented && onRun) onRun(row.key.id);
  };
  const onKeyDown = (e) => {
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
      <div class="palette" role="dialog" aria-modal="true" aria-label="Command palette" onKeyDown=${onKeyDown}>
        <input
          ref=${input}
          class="palette-input"
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          aria-activedescendant=${rows[at] ? optionId(rows[at].key.id) : undefined}
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
              ${rows.map((r, i) => html`<${Row} key=${r.key.id} row=${r} current=${i === at} onRun=${() => run(r)} />`)}
            </ul>`}
      </div>
    </div>
  `;
}

function Row({ row, current, onRun }) {
  const cls = "palette-row" + (current ? " is-current" : "") + (row.implemented ? "" : " palette-row--off");
  return html`
    <li id=${optionId(row.key.id)} class=${cls} role="option" aria-selected=${current ? "true" : "false"} aria-disabled=${row.implemented ? undefined : "true"} data-action=${row.key.id} onClick=${onRun}>
      <span class="palette-name">${row.name}</span>
      ${row.implemented ? null : html`<span class="palette-note">${NOT_YET}</span>`}
      ${row.key.keys ? html`<span class="palette-keys">${keycaps(row.key.keys).join("+")}</span>` : null}
    </li>
  `;
}
