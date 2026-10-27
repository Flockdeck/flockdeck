import { useEffect, useLayoutEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { menuMove } from "./keys.js";
import { build, ctxElement, edgeItem, nativeWanted, nextItem, place, pointOf, typeAhead, ATTR } from "./menus.js";

/** ContextMenu is the right-click menu: one component, drawn once by the Shell,
 *  that opens for any part of the page carrying data-ctx (see menus.js).
 *
 *  One contextmenu listener, in the capture phase, sees every right-click, the
 *  menu key and Shift+F10. It is on the window, which a capture listener there
 *  reaches before one on the document. A click on a field, or on selected text, or
 *  on a part with no data-ctx, is not touched, so the window's own menu opens.
 *
 *  The menu is role="menu". The keyboard goes to its first item as it opens and
 *  back to where it was when it closes. ctx is the bag the page gives its menus
 *  (see Live.js); keys and implemented are the Shell's. */
export function ContextMenu({ win, ctx }) {
  const [at, setAt] = useState(null);
  // The latest bag, so the listener below is put on once and still reads it.
  const latest = useRef(ctx);
  latest.current = ctx;
  const box = useRef(null);
  const opener = useRef(null);
  // A click on an item counts once the press that opened the menu is over (its
  // pointerup) or a frame has passed, so neither that press nor a menu shifted
  // by the clamp can choose the first item.
  const armed = useRef(false);

  const close = (refocus) => {
    const back = opener.current;
    opener.current = null;
    setAt(null);
    if (refocus && back && back.focus) back.focus();
  };

  useEffect(() => {
    if (!win) return undefined;
    const doc = win.document;
    const onMenu = (e) => {
      const el = ctxElement(e.target);
      const items = el && !nativeWanted(e.target, win, el) ? build(el.getAttribute(ATTR), el, latest.current) : [];
      if (!items.length) {
        // Not ours: the window's menu opens, and ours does not stay under it.
        opener.current = null;
        setAt(null);
        return;
      }
      e.preventDefault();
      e.stopPropagation();
      const p = pointOf(e, el);
      if (!opener.current) opener.current = doc && doc.activeElement;
      armed.current = false;
      const arm = () => {
        armed.current = true;
        win.removeEventListener("pointerup", arm, true);
      };
      win.addEventListener("pointerup", arm, true);
      (win.requestAnimationFrame ? win.requestAnimationFrame.bind(win) : (fn) => setTimeout(fn, 0))(arm);
      setAt({ ...p, items, current: -1, seq: Date.now() + Math.random() });
    };
    win.addEventListener("contextmenu", onMenu, true);
    return () => win.removeEventListener("contextmenu", onMenu, true);
  }, [win]);

  // While it is open, anything that is not the menu closes it. The press that
  // closes it is not stopped, so it still does what it was for.
  const open = !!at;
  useEffect(() => {
    if (!open || !win) return undefined;
    const away = (e) => {
      if (box.current && box.current.contains(e.target)) return;
      close(false);
    };
    const gone = (e) => {
      if (e && e.target && box.current && box.current.contains(e.target)) return;
      close(false);
    };
    win.addEventListener("pointerdown", away, true);
    win.addEventListener("mousedown", away, true);
    win.addEventListener("scroll", gone, true);
    win.addEventListener("resize", gone);
    win.addEventListener("blur", gone);
    return () => {
      win.removeEventListener("pointerdown", away, true);
      win.removeEventListener("mousedown", away, true);
      win.removeEventListener("scroll", gone, true);
      win.removeEventListener("resize", gone);
      win.removeEventListener("blur", gone);
    };
  }, [open, win]);

  // The first item takes the keyboard, and the menu is put where it fits once
  // it has a size.
  const [spot, setSpot] = useState(null);
  const seq = at ? at.seq : 0;
  useLayoutEffect(() => {
    setSpot(null);
    const el = box.current;
    if (!at || !el) return;
    const r = el.getBoundingClientRect ? el.getBoundingClientRect() : { width: 0, height: 0 };
    setSpot(place({ x: at.x, y: at.y, w: r.width, h: r.height, vw: win.innerWidth || 0, vh: win.innerHeight || 0 }));
  }, [seq]);
  // Hidden until it has a place, and an element that is hidden cannot take the keyboard.
  const placed = !!spot;
  useEffect(() => {
    if (!at || !placed) return;
    const first = edgeItem(at.items, 0);
    if (first >= 0) focusItem(first);
  }, [seq, placed]);

  const focusItem = (i) => {
    const el = box.current && box.current.querySelectorAll(".ctxmenu-item")[indexAmongItems(at.items, i)];
    if (el && el.focus) el.focus();
    setAt((cur) => (cur ? { ...cur, current: i } : cur));
  };

  const choose = (it) => {
    if (!it || it.disabled) return;
    // Closed first, with the keyboard back where it was, so an item that moves
    // it (opening a drawer, say) is not undone by the restore.
    close(true);
    it.run();
  };

  const onKeyDown = (e) => {
    if (!at) return;
    const move = menuMove(e, at.items.length);
    if (move === "") return;
    e.preventDefault();
    e.stopPropagation();
    if (move === "cancel" || move === "tab") close(true);
    else if (move === "accept") choose(at.items[at.current]);
    else if (move.step) {
      const to = nextItem(at.items, at.current, move.step);
      if (to >= 0) focusItem(to);
    } else if (move.to !== undefined) {
      const to = edgeItem(at.items, move.to);
      if (to >= 0) focusItem(to);
    } else if (move.type) {
      const to = typeAhead(at.items, at.current, move.type);
      if (to >= 0) focusItem(to);
    }
  };

  if (!at) return null;
  const style = spot
    ? "left:" + spot.left + "px;top:" + spot.top + "px;max-height:" + spot.maxHeight + "px"
    : "left:" + at.x + "px;top:" + at.y + "px;visibility:hidden";
  return html`<div class="ctxmenu" role="menu" aria-label="Actions" ref=${box} style=${style} onKeyDown=${onKeyDown} onContextMenu=${(e) => e.preventDefault()}>
    ${at.items.map((it, i) =>
      it.separator
        ? html`<hr class="ctxmenu-sep" role="separator" key=${"sep" + i} />`
        : html`<button type="button" key=${it.id} role=${it.checked === undefined ? "menuitem" : "menuitemcheckbox"} aria-checked=${it.checked === undefined ? undefined : it.checked ? "true" : "false"} aria-disabled=${it.disabled ? "true" : undefined} tabindex="-1" class=${"ctxmenu-item" + (it.danger ? " ctxmenu-item--danger" : "") + (i === at.current ? " is-current" : "")} data-item=${it.id} onClick=${() => armed.current && choose(it)} onMouseEnter=${() => !it.disabled && focusItem(i)}>
            <span class="ctxmenu-check" aria-hidden="true">${it.checked ? "✓" : ""}</span>
            <span class="ctxmenu-label">${it.label}</span>
            ${it.hint ? html`<span class="ctxmenu-hint">${it.hint}</span>` : null}
          </button>`
    )}
  </div>`;
}

/** indexAmongItems is where item i is among the buttons, which the rules are not. */
function indexAmongItems(items, i) {
  return items.slice(0, i).filter((x) => !x.separator).length;
}
