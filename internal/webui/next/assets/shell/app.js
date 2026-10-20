import { useEffect, useMemo, useReducer, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Rail } from "./rail.js";
import { Palette } from "./palette.js";
import { actionFor, bindingsFrom } from "./keys.js";

/** NARROW_QUERY is the phone width. app.css repeats it in its narrow rules, and
 *  a test holds the two together. */
export const NARROW_QUERY = "(max-width: 640px)";

/** IMPLEMENTED are the actions of the key table this window runs. The palette
 *  lists every other action too, marked as not in this window yet. */
export const IMPLEMENTED = ["palette", "toggleRail", "projects", "nextRegion", "prevRegion"];

/** REGIONS are the parts of the page the keyboard moves between, in order. The
 *  drawer is one only while it holds something. */
const REGIONS = ["rail", "main", "drawer"];

/** initial is the shell's own state: the open palette, the rail's width, which
 *  project is watched and which region was asked to take the keyboard. */
export function initial({ state } = {}) {
  const projects = (state && state.projects) || [];
  const first = (state && state.root) || (projects[0] && projects[0].root) || "";
  return { paletteOpen: false, railCollapsed: false, selected: first, focus: "", focusSeq: 0 };
}

/** regionsOf is the regions that can take the keyboard now. */
export function regionsOf({ drawerOpen = false } = {}) {
  return REGIONS.filter((r) => r !== "drawer" || drawerOpen);
}

/** reduce is every change the shell makes to its own state, as a pure function:
 *  an action of the key table that this window runs, a project chosen in the
 *  rail, or the palette closing. */
export function reduce(s, ev, ctx = {}) {
  switch (ev.type) {
    case "select":
      return s.selected === ev.root ? s : { ...s, selected: ev.root };
    case "closePalette":
      return s.paletteOpen ? { ...s, paletteOpen: false } : s;
    case "action":
      switch (ev.id) {
        case "palette":
          return { ...s, paletteOpen: !s.paletteOpen };
        case "toggleRail":
          return { ...s, railCollapsed: !s.railCollapsed };
        case "projects":
          return { ...s, paletteOpen: false, focus: "rail", focusSeq: s.focusSeq + 1 };
        case "nextRegion":
        case "prevRegion": {
          const list = regionsOf(ctx);
          const at = list.indexOf(s.focus);
          const step = ev.id === "nextRegion" ? 1 : -1;
          const to = list[at < 0 ? (step > 0 ? 0 : list.length - 1) : (at + step + list.length) % list.length];
          return { ...s, focus: to, focusSeq: s.focusSeq + 1 };
        }
        default:
          return s;
      }
    default:
      return s;
  }
}

/** useNarrow follows the phone-width query. matchMedia is the window's, passed
 *  in so a test can give a fake one; the first render is the query's answer, so
 *  a narrow window never draws wide first. */
export function useNarrow(matchMedia, override) {
  const mq = useMemo(() => (matchMedia ? matchMedia(NARROW_QUERY) : null), [matchMedia]);
  const [narrow, setNarrow] = useState(mq ? mq.matches : false);
  useEffect(() => {
    if (!mq) return undefined;
    const on = () => setNarrow(mq.matches);
    on();
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [mq]);
  return override === undefined ? narrow : override;
}

/** Shell is the page frame: the rail with the open projects, a strip along the
 *  top for the manager bar, the main area, a region for the drawer and a place
 *  for notices.
 *
 *  hello and state are the server's two first messages (the key table; the
 *  projects). onWatch is called with the root of a project the rail chose, which
 *  is how the graph for it is asked for. top, children and drawer are what the
 *  views put in the regions. */
export function Shell({ hello, state, notice, onWatch, narrow: narrowProp, matchMedia, top, children, drawer, window: win }) {
  const keys = (hello && hello.keys) || [];
  const bindings = useMemo(() => bindingsFrom(keys), [keys]);
  const narrow = useNarrow(matchMedia, narrowProp);
  const drawerOpen = !!drawer;
  const [s, dispatch] = useReducer((cur, ev) => reduce(cur, ev, { drawerOpen }), { state }, initial);

  const regions = { rail: useRef(null), main: useRef(null), drawer: useRef(null) };

  // Keys: the table says what a keydown is; this only runs it.
  useEffect(() => {
    const target = win || (typeof window !== "undefined" ? window : null);
    if (!target) return undefined;
    const onKey = (e) => {
      const id = actionFor(bindings, e);
      if (!id || !IMPLEMENTED.includes(id)) return;
      e.preventDefault();
      e.stopPropagation();
      dispatch({ type: "action", id });
    };
    target.addEventListener("keydown", onKey, true);
    return () => target.removeEventListener("keydown", onKey, true);
  }, [bindings, win]);

  useEffect(() => {
    const el = s.focus && regions[s.focus] && regions[s.focus].current;
    if (el && el.focus) el.focus();
  }, [s.focusSeq]);

  const select = (root) => {
    dispatch({ type: "select", root });
    if (onWatch) onWatch(root);
  };
  const run = (id) => {
    dispatch({ type: "closePalette" });
    if (IMPLEMENTED.includes(id)) dispatch({ type: "action", id });
  };

  const projects = (state && state.projects) || [];
  const current = projects.find((p) => p.root === s.selected);
  const cls = "shell" + (narrow ? " shell--narrow" : "") + (s.railCollapsed && !narrow ? " shell--rail-collapsed" : "");

  return html`
    <div class=${cls}>
      <${Rail}
        projects=${projects}
        active=${s.selected}
        collapsed=${s.railCollapsed}
        narrow=${narrow}
        onSelect=${select}
        onToggle=${() => dispatch({ type: "action", id: "toggleRail" })}
        railRef=${regions.rail}
      />
      <header class="top" aria-label="Manager">
        ${top || html`<span class="top-title">${current ? current.name : "Flockdeck"}</span>`}
      </header>
      <main class=${"main" + (narrow ? " main--list" : "")} tabindex="-1" ref=${regions.main}>${children}</main>
      <aside class="drawer" aria-label="Agent" tabindex="-1" ref=${regions.drawer} hidden=${!drawerOpen}>${drawer}</aside>
      <div class="notices" role="status" aria-live="polite">
        ${notice ? html`<p class="notice">${notice}</p>` : null}
      </div>
      ${s.paletteOpen
        ? html`<${Palette} keys=${keys} implemented=${IMPLEMENTED} onRun=${run} onClose=${() => dispatch({ type: "closePalette" })} />`
        : null}
    </div>
  `;
}
