import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
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
export function Rail({ projects = [], active = "", collapsed = false, narrow = false, width = null, onWidth = null, onDrag = null, onSelect, onToggle, onSettings, onProjects, railRef, foot = null }) {
  const cls = "rail" + (narrow ? " rail--bar" : "");
  return html`
    <nav class=${cls} aria-label="Projects" tabindex="-1" ref=${railRef}>
      <ul class="rail-list">
        ${projects.length === 0
          ? html`<li class="rail-empty">No projects open</li>`
          : projects.map((p) => html`<${RailItem} key=${p.root} project=${p} active=${p.root === active} onSelect=${onSelect} />`)}
      </ul>
      ${onProjects ? html`<button type="button" class="rail-projects" onClick=${onProjects}>Manage projects</button>` : null}
      ${onSettings ? html`<button type="button" class="rail-settings" onClick=${onSettings}>Settings</button>` : null}
      ${foot}
      ${!narrow && !collapsed && width !== null && onWidth ? html`<${Resize} width=${width} onWidth=${onWidth} onDrag=${onDrag} />` : null}
      ${narrow
        ? null
        : html`<button type="button" class="rail-toggle" aria-pressed=${collapsed ? "true" : "false"} onClick=${onToggle}>
            ${collapsed ? "Widen" : "Narrow"}
          </button>`}
    </nav>
  `;
}

function RailItem({ project, active, onSelect }) {
  const { root, name, waiting = 0, working = 0 } = project;
  // The name is hidden when the rail is narrow, so the button carries it, and
  // the counts, in its label.
  const label = [name, waiting > 0 ? waiting + " waiting" : "", working > 0 ? working + " working" : ""].filter(Boolean).join(", ");
  return html`
    <li>
      <button
        type="button"
        class=${"rail-item" + (active ? " is-active" : "")}
        aria-current=${active ? "true" : undefined}
        aria-label=${label}
        title=${name}
        data-root=${root}
        onClick=${() => onSelect && onSelect(root)}
      >
        <span class="rail-initial" aria-hidden="true">${initialOf(name)}</span>
        <span class="rail-name">${name}</span>
        ${waiting > 0 ? html`<span class="badge badge--waiting" aria-hidden="true">${waiting}</span>` : null}
        ${working > 0 ? html`<span class="badge badge--working" aria-hidden="true">${working}</span>` : null}
      </button>
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
