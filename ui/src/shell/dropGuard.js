// A file dropped on a browser window that nothing handles opens in that window, which would replace
// Flockdeck with the picture or PDF. The prompt box takes drops on its drawer (Composer.js), but only
// while a structured pane's box is on screen; a drop on the graph, a terminal drawer or the rail would
// navigate away. This one guard is installed once for the page and cancels the default of any drag that
// carries files. A drop that a handler below it took has already been cancelled, so it is left alone.

const carriesFiles = (e) => !!e && !!e.dataTransfer && Array.from(e.dataTransfer.types || []).includes("Files");

/** guardFileDrops installs the guard on a window and returns what removes it. */
export function guardFileDrops(win) {
  if (!win || !win.addEventListener) return () => {};
  const guard = (e) => {
    if (!carriesFiles(e)) return;
    const taken = e.defaultPrevented;
    e.preventDefault();
    // Over a place that takes nothing, the pointer says so; over the drawer it was already a copy.
    if (!taken && e.type === "dragover") { try { e.dataTransfer.dropEffect = "none"; } catch (err) { /* a read-only transfer */ } }
  };
  win.addEventListener("dragover", guard);
  win.addEventListener("drop", guard);
  return () => {
    win.removeEventListener("dragover", guard);
    win.removeEventListener("drop", guard);
  };
}
