// What this window's device may do, from the hello. A viewer can read and not
// act: the desk refuses every command it sends with "This device can only view",
// so the controls that would send one are left out or disabled with that sentence.

/** VIEWER_WORDS is what a control a viewer may not use says. */
export const VIEWER_WORDS = "This device can only view.";

/** isViewer says the window's device is a viewer. snap is the store's snapshot. */
export const isViewer = (snap) => !!(snap && snap.hello && snap.hello.role === "viewer");
