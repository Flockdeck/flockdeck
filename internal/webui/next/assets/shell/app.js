import { useEffect, useMemo, useReducer, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Rail } from "./rail.js";
import { Palette } from "./palette.js";
import { Help } from "./Help.js";
import { loadHelp } from "../lib/help.js";
import { bindingsFrom, keyAction, unreadable } from "./keys.js";

/** NARROW_QUERY is the phone width. app.css repeats it in its narrow rules, and
 *  a test holds the two together. */
export const NARROW_QUERY = "(max-width: 640px)";

/** IMPLEMENTED are the actions of the key table this window runs. The palette
 *  lists every other action too, marked as not in this window yet. */
export const IMPLEMENTED = ["palette", "toggleRail", "projects", "nextRegion", "prevRegion", "help"];

/** REGIONS are the parts of the page the keyboard moves between, in order. The
 *  drawer is one only while it holds something. */
const REGIONS = ["rail", "main", "drawer"];

const NONE = [];
const NO_ACTIONS = Object.freeze({});

/** NO_KEYS stands for a hello with no key table, so the bindings are not made
 *  again on each render for want of one array being the same as the last. */
const NO_KEYS = Object.freeze([]);

/** keysOf is the key table of a hello, or NO_KEYS. */
export function keysOf(hello) {
  return (hello && hello.keys) || NO_KEYS;
}
const firstRoot = (state) => (state && state.projects && state.projects[0] && state.projects[0].root) || "";

/** initial is the shell's own state: the open palette and help, the rail's width, which
 *  project is watched (and the root the state last named, so a change of it can
 *  be told from a repeat) and which region was asked to take the keyboard. */
export function initial({ state } = {}) {
  const root = (state && state.root) || "";
  return { paletteOpen: false, helpOpen: false, railCollapsed: false, selected: root || firstRoot(state), seenRoot: root, focus: "", focusSeq: 0 };
}

/** regionsOf is the regions that can take the keyboard now. */
export function regionsOf({ drawerOpen = false } = {}) {
  return REGIONS.filter((r) => r !== "drawer" || drawerOpen);
}

/** reduce is every change the shell makes to its own state, as a pure function:
 *  an action of the key table that this window runs, a project chosen in the
 *  rail, a state push, or the palette closing. */
export function reduce(s, ev, ctx = {}) {
  switch (ev.type) {
    case "select":
      return s.selected === ev.root ? s : { ...s, selected: ev.root };
    case "state": {
      // The state names the project the desk has active. The window follows it
      // when that changes, and when the project it was watching is gone or it
      // was watching none; a push that repeats the same root leaves a project
      // chosen in the rail alone.
      const root = ev.root || "";
      const projects = ev.projects || [];
      const known = projects.some((p) => p.root === s.selected);
      let selected = s.selected;
      if (root && root !== s.seenRoot) selected = root;
      else if (!known) selected = root || firstRoot(ev);
      return selected === s.selected && root === s.seenRoot ? s : { ...s, selected, seenRoot: root };
    }
    case "closePalette":
      return s.paletteOpen ? { ...s, paletteOpen: false } : s;
    case "closeHelp":
      return s.helpOpen ? { ...s, helpOpen: false } : s;
    case "action":
      // While the help is open the keyboard stays in it: the key that opens it
      // closes it, and no other action runs underneath.
      if (s.helpOpen && ev.id !== "help") return s;
      switch (ev.id) {
        case "help":
          return { ...s, paletteOpen: false, helpOpen: !s.helpOpen };
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
          return { ...s, paletteOpen: false, focus: to, focusSeq: s.focusSeq + 1 };
        }
        default:
          return s;
      }
    default:
      return s;
  }
}

/** attachKeys listens on target for the keys the table binds to an action this
 *  window runs, and hands each to dispatch as an action. It returns what takes
 *  the listener off again. The table says what a keydown is; this only runs it,
 *  and leaves every other key alone. */
export function attachKeys(target, bindings, implemented, dispatch) {
  const onKey = (e) => {
    const id = keyAction(bindings, e, implemented);
    if (!id) return;
    e.preventDefault();
    e.stopPropagation();
    dispatch({ type: "action", id });
  };
  target.addEventListener("keydown", onKey, true);
  return () => target.removeEventListener("keydown", onKey, true);
}

/** focusRegion puts the keyboard on one of the regions, by the refs holding
 *  their elements. */
export function focusRegion(refs, name) {
  const el = name && refs[name] && refs[name].current;
  if (el && el.focus) el.focus();
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
 *  projects). The key table is read when it changes and not on each render, and
 *  a binding it holds that this reader cannot turn into a key is said in the
 *  notice area. onWatch is called with the root of the project being watched:
 *  when the window first has one, when the rail chooses another, and when the
 *  state names a different active one. top, children and drawer are what the
 *  views put in the regions. maximised, with a drawer, gives the drawer the main
 *  area; mainRef is the main area's element for a view that returns the keyboard
 *  to a box in it. window is the page's, passed in for a test. helpLoad fetches
 *  the help pages (lib/help.js); the default asks the page's own address. blocked
 *  says a view's modal dialog is open, and no key then reaches an action. */
export function Shell({ hello, state, notice, onDismissNotice, onWatch, narrow: narrowProp, matchMedia, top, children, drawer, maximised = false, mainRef, window: win, actions = NO_ACTIONS, blocked = false, onSettings, helpLoad, railFoot = null }) {
  const keys = keysOf(hello);
  const bindings = useMemo(() => bindingsFrom(keys), [keys]);
  const broken = useMemo(() => unreadable(keys), [keys]);
  const narrow = useNarrow(matchMedia, narrowProp);
  const drawerOpen = !!drawer;
  const [s, dispatch] = useReducer((cur, ev) => reduce(cur, ev, { drawerOpen }), { state }, initial);

  // The actions a view runs itself, by id (the graph's), kept in a ref so the
  // key listener always runs the latest. Their ids are implemented as well.
  const own = useRef(actions);
  own.current = actions;
  const ids = Object.keys(actions).sort().join(",");
  const implemented = useMemo(() => (ids ? IMPLEMENTED.concat(ids.split(",")) : IMPLEMENTED), [ids]);
  // A view's modal dialog is open: no key reaches an action under it.
  const isBlocked = useRef(blocked);
  isBlocked.current = blocked;
  const act = (ev) => {
    if (isBlocked.current) return;
    const fn = ev.type === "action" ? own.current[ev.id] : null;
    if (fn) fn();
    else dispatch(ev);
  };

  const load = useMemo(() => {
    if (helpLoad) return helpLoad;
    const w = win || (typeof window !== "undefined" ? window : null);
    return () => (w && w.fetch ? loadHelp(w.fetch.bind(w), w.location) : Promise.resolve({ error: "The help pages could not be loaded." }));
  }, [helpLoad, win]);

  const ownMain = useRef(null);
  const regions = { rail: useRef(null), main: mainRef || ownMain, drawer: useRef(null) };

  useEffect(() => {
    const target = win || (typeof window !== "undefined" ? window : null);
    return target ? attachKeys(target, bindings, implemented, act) : undefined;
  }, [bindings, win, implemented]);

  useEffect(() => {
    if (s.focusSeq > 0) focusRegion(regions, s.focus);
  }, [s.focusSeq]);

  // The state arrives after the first render, and again as the desk changes.
  const root = (state && state.root) || "";
  const projects = (state && state.projects) || NONE;
  useEffect(() => {
    dispatch({ type: "state", root, projects });
  }, [root, projects]);

  useEffect(() => {
    if (s.selected && onWatch) onWatch(s.selected);
  }, [s.selected]);

  // A view's own action chosen in the palette runs once the palette is gone: the
  // palette puts the keyboard back where it was as it closes, and would take it
  // from an action that had moved it.
  const afterPalette = useRef("");
  useEffect(() => {
    const id = afterPalette.current;
    if (s.paletteOpen || !id) return;
    afterPalette.current = "";
    if (own.current[id]) own.current[id]();
  }, [s.paletteOpen]);
  const run = (id) => {
    dispatch({ type: "closePalette" });
    if (!implemented.includes(id)) return;
    if (own.current[id]) afterPalette.current = id;
    else act({ type: "action", id });
  };

  const current = projects.find((p) => p.root === s.selected);
  const cls = "shell" + (narrow ? " shell--narrow" : "") + (s.railCollapsed && !narrow ? " shell--rail-collapsed" : "") + (maximised && drawerOpen ? " shell--max" : "");
  const said = notice || (broken.length ? "These key bindings could not be read, so they do nothing: " + broken.join(", ") : "");

  return html`
    <div class=${cls}>
      <${Rail}
        projects=${projects}
        active=${s.selected}
        collapsed=${s.railCollapsed}
        narrow=${narrow}
        onSelect=${(r) => dispatch({ type: "select", root: r })}
        onToggle=${() => dispatch({ type: "action", id: "toggleRail" })}
        onSettings=${onSettings}
        railRef=${regions.rail}
        foot=${railFoot}
      />
      <header class="top" aria-label="Manager">
        ${top || html`<span class="top-title">${current ? current.name : "Flockdeck"}</span>`}
      </header>
      <main class=${"main" + (narrow ? " main--list" : "")} tabindex="-1" ref=${regions.main}>${children}</main>
      <aside class="drawer" aria-label="Agent" tabindex="-1" ref=${regions.drawer} hidden=${!drawerOpen}>${drawer}</aside>
      <div class="notices" role="status" aria-live="polite">
        ${said
          ? html`<p class="notice">${said}${notice && onDismissNotice
              ? html` <button type="button" class="notice-dismiss" onClick=${onDismissNotice}>Dismiss</button>`
              : null}</p>`
          : null}
      </div>
      ${s.helpOpen
        ? html`<${Help} keys=${keys} implemented=${implemented} load=${load} onClose=${() => dispatch({ type: "closeHelp" })} />`
        : null}
      ${s.paletteOpen
        ? html`<${Palette} keys=${keys} implemented=${implemented} onRun=${run} onClose=${() => dispatch({ type: "closePalette" })} />`
        : null}
    </div>
  `;
}
