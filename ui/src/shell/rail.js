import { useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Popover } from "./Popover.js";
import { WaitingList, waitingWords } from "./WaitingList.js";
import { RAIL_MAX, RAIL_MIN } from "../lib/prefs.js";

/** STEP is how far one arrow key press moves the rail, in pixels. */
const STEP = 16;
const clamp = (px) => Math.max(RAIL_MIN, Math.min(RAIL_MAX, Math.round(px)));

/** initialOf is the letter that stands for a project when the rail is narrow
 *  and its name is hidden. */
export function initialOf(name) {
  const first = [...String(name || "").trim()][0];
  return first ? first.toUpperCase() : "?";
}

/** Rail is the open projects, from the state push, one button each. Choosing one
 *  makes it the project the window watches. At phone width it is a bar along the
 *  top (the CSS lays it out; narrow only drops the collapse button, which has no
 *  use there). Project names are text. */
export function Rail({ projects = [], active = "", collapsed = false, narrow = false, width = null, onWidth = null, onDrag = null, onSelect, onToggle, onSettings, onProjects, railRef, foot = null, panes = null, paneRoot = "", onOpenPane = null, win = null, openOnly = false }) {
  const cls = "rail" + (narrow ? " rail--bar" : "");
  // The project whose waiting list is open, and the badge it came from. The list
  // is drawn from the push's panes once the push is for that project.
  const [ask, setAsk] = useState(null);
  const close = (back) => {
    // When the badge is gone (the count fell to nothing) the keyboard goes to the project's own button.
    const el = ask && (back === "gone" ? ask.home : ask.el);
    setAsk(null);
    if (back && el && el.focus) el.focus();
  };
  // A window that cannot choose the project the desk shows (a relay or a viewer)
  // only has the panes of the project it was sent, so only that project's count
  // can open a list; the others stay a count, with words.
  const listable = (root) => !openOnly || root === paneRoot;
  const onWaiting = (root, el) => {
    if (ask && ask.root === root) return close(true);
    if (!listable(root)) return;
    if (root !== active && onSelect) onSelect(root);
    setAsk({ root, el, home: el && el.parentNode && el.parentNode.querySelector ? el.parentNode.querySelector(".rail-item") : null });
  };
  const askedOf = ask ? projects.find((p) => p.root === ask.root) : null;
  return html`
    <nav class=${cls} aria-label="Projects" tabindex="-1" ref=${railRef}>
      <ul class="rail-list">
        ${projects.length === 0
          ? html`<li class="rail-empty">No projects open</li>`
          : projects.map((p) => html`<${RailItem} key=${p.root} project=${p} active=${p.root === active} onSelect=${onSelect} onWaiting=${onWaiting} listable=${listable(p.root)} open=${!!ask && ask.root === p.root} />`)}
      </ul>
      <div class="rail-foot">
        ${onProjects ? html`<${FootButton} cls="rail-projects" glyph="+" label="Manage projects" onClick=${onProjects} />` : null}
        ${onSettings ? html`<${FootButton} cls="rail-settings" glyph="⚙" label="Settings" onClick=${onSettings} />` : null}
        ${foot}
        ${narrow
          ? null
          : html`<${FootButton} cls="rail-toggle" glyph=${collapsed ? "»" : "«"} label=${collapsed ? "Widen" : "Narrow"} pressed=${collapsed ? "true" : "false"} onClick=${onToggle} />`}
      </div>
      ${ask && askedOf
        ? html`<${Popover} anchor=${ask.el} label=${"Agents waiting for you in " + askedOf.name} side=${narrow ? "below" : "right"} win=${win} onClose=${close}>
            <${WaitingList} panes=${paneRoot === ask.root ? panes || {} : null} onOpen=${(pane) => { close(false); if (onOpenPane) onOpenPane(pane); }} />
          <//>`
        : null}
      ${!narrow && !collapsed && width !== null && onWidth ? html`<${Resize} width=${width} onWidth=${onWidth} onDrag=${onDrag} />` : null}
    </nav>
  `;
}

/** FootButton is one of the buttons at the foot of the rail. It is a glyph when the
 *  rail is narrow and the glyph and its words when the rail is wide or a bar; the
 *  words are always its accessible name and its tooltip. */
export function FootButton({ cls, glyph, label, title = label, pressed, onClick }) {
  return html`<button type="button" class=${"rail-btn " + cls} aria-label=${label} title=${title} aria-pressed=${pressed} onClick=${onClick}>
    <span class="rail-glyph" aria-hidden="true">${glyph}</span><span class="rail-label">${label}</span>
  </button>`;
}

/** RailItem is one project: its button and, beside it, the orange count as a
 *  button of its own (a button inside a button is not valid). The count is panes
 *  that wait on the person or are blocked, in every tab and goal of the project;
 *  choosing it opens the project and lists them. The teal count is the panes at
 *  work, a span with a tooltip. */
export function RailItem({ project, active, onSelect, onWaiting = null, open = false, listable = true }) {
  const { root, name, waiting = 0, working = 0 } = project;
  // The name is hidden when the rail is narrow, so the button carries it, and
  // the counts, in its label.
  const label = [name, waiting > 0 ? waiting + " waiting for you" : "", working > 0 ? working + " working" : ""].filter(Boolean).join(", ");
  const words = waiting > 0 ? waitingWords(waiting) : "";
  return html`
    <li class="rail-row">
      <button
        type="button"
        class=${"rail-item" + (active ? " is-active" : "") + (waiting > 0 ? " has-waiting" : "")}
        aria-current=${active ? "true" : undefined}
        aria-label=${label}
        title=${label}
        data-root=${root}
        data-ctx="rail"
        onClick=${() => onSelect && onSelect(root)}
      >
        <span class="rail-initial" aria-hidden="true">${initialOf(name)}</span>
        <span class="rail-name">${name}</span>
        ${working > 0 ? html`<span class="badge badge--working" role="img" aria-label=${working + (working === 1 ? " agent working" : " agents working")} title=${working + (working === 1 ? " agent working" : " agents working")}>${working}</span>` : null}
      </button>
      ${waiting > 0 && !listable
        ? html`<span class="badge badge--waiting rail-waiting rail-waiting--count" role="img" aria-label=${words} title=${words + " (panes, not requests). Open the project on the desktop to see them"}>${waiting}</span>`
        : waiting > 0
        ? html`<button type="button" class="badge badge--waiting rail-waiting" aria-haspopup="dialog" aria-expanded=${open ? "true" : "false"}
            aria-label=${words + ". Open the list"}
            title=${words + (active ? ". Open the list" : ". Open the project and list them") + " (panes, not requests)"}
            onClick=${(e) => onWaiting && onWaiting(root, e.currentTarget)}>${waiting}</button>`
        : null}
    </li>
  `;
}

/** Resize is the handle that sets the rail's width: dragged with the pointer, or
 *  moved with the arrow keys, a step at a time. The width in effect is drawn while
 *  it is dragged and kept when the pointer is let go. */
function Resize({ width, onWidth, onDrag }) {
  const onKeyDown = (e) => {
    if (composing(e) || e.ctrlKey || e.altKey || e.metaKey) return;
    const by = e.key === LIST.right ? STEP : e.key === LIST.left ? -STEP : e.key === LIST.home ? RAIL_MIN - width : e.key === LIST.end ? RAIL_MAX - width : 0;
    if (!by) return;
    e.preventDefault();
    e.stopPropagation();
    const to = clamp(width + by);
    if (to !== width) onWidth(to);
  };
  const onPointerDown = (e) => {
    if (e.button) return;
    const el = e.currentTarget;
    e.preventDefault();
    const from = e.clientX;
    const start = width;
    let last = start;
    if (el.setPointerCapture) {
      try { el.setPointerCapture(e.pointerId); } catch { /* the pointer is already gone */ }
    }
    const move = (m) => {
      last = clamp(start + m.clientX - from);
      if (onDrag) onDrag(last);
    };
    const done = () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", done);
      el.removeEventListener("pointercancel", done);
      onWidth(last);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", done);
    el.addEventListener("pointercancel", done);
  };
  return html`<div class="rail-resize" role="separator" aria-orientation="vertical" aria-label="Rail width" tabindex="0"
    aria-valuemin=${RAIL_MIN} aria-valuemax=${RAIL_MAX} aria-valuenow=${width} aria-valuetext=${width + " pixels"}
    onKeyDown=${onKeyDown} onPointerDown=${onPointerDown}></div>`;
}
