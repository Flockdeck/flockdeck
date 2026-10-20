import { html } from "../html.js";

/** Rail is the open projects, from the state push, one button each. Choosing one
 *  makes it the project the window watches. At phone width it is a bar along the
 *  top (the CSS lays it out; narrow only drops the collapse button, which has no
 *  use there). Project names are text. */
export function Rail({ projects = [], active = "", collapsed = false, narrow = false, onSelect, onToggle, railRef }) {
  const cls = "rail" + (narrow ? " rail--bar" : "");
  return html`
    <nav class=${cls} aria-label="Projects" tabindex="-1" ref=${railRef}>
      <ul class="rail-list">
        ${projects.length === 0
          ? html`<li class="rail-empty">No projects open</li>`
          : projects.map((p) => html`<${RailItem} key=${p.root} project=${p} active=${p.root === active} onSelect=${onSelect} />`)}
      </ul>
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
  return html`
    <li>
      <button
        type="button"
        class=${"rail-item" + (active ? " is-active" : "")}
        aria-current=${active ? "true" : undefined}
        title=${name}
        data-root=${root}
        onClick=${() => onSelect && onSelect(root)}
      >
        <span class="rail-name">${name}</span>
        ${waiting > 0 ? html`<span class="badge badge--waiting" aria-label=${waiting + " waiting"}>${waiting}</span>` : null}
        ${working > 0 ? html`<span class="badge badge--working" aria-label=${working + " working"}>${working}</span>` : null}
      </button>
    </li>
  `;
}
