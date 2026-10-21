// What the page does with its store when the browser hides or shows it again.
// stop() unwatches the graph, so the desktop does not keep building it for a
// window that has gone. A page kept in the back-forward cache comes back with
// its socket stopped, so showing it again starts the store again (start is
// idempotent, and Live asks for its graph once more). A pageshow on a page that
// was loaded fresh, not restored, has a store running already and is left alone.

/** watchPageLifecycle wires a window's pagehide and pageshow to a store, and
 *  returns a function that removes both. */
export function watchPageLifecycle(win, store) {
  const hide = () => store.stop();
  const show = (e) => {
    if (e.persisted) store.start();
  };
  win.addEventListener("pagehide", hide);
  win.addEventListener("pageshow", show);
  return () => {
    win.removeEventListener("pagehide", hide);
    win.removeEventListener("pageshow", show);
  };
}
