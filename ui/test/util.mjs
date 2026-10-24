// Helpers for the shell's tests: a walk over a tree of virtual nodes, with
// components called as they are met, so a handler can be found and run without
// a browser.
import { readFileSync } from "node:fs";

/** expand calls every function component in the tree and returns the tree of
 *  plain elements. A component that uses hooks cannot be expanded this way. */
export function expand(node) {
  if (node == null || typeof node === "boolean") return null;
  if (Array.isArray(node)) return node.map(expand).filter((n) => n !== null);
  if (typeof node !== "object") return node;
  if (typeof node.type === "function") return expand(node.type(node.props));
  const { children, ...props } = node.props || {};
  return { type: node.type, props, children: [].concat(children == null ? [] : children).map(expand).filter((n) => n !== null).flat() };
}

/** find returns every element for which pred is true. */
export function find(tree, pred, out = []) {
  if (Array.isArray(tree)) {
    tree.forEach((t) => find(t, pred, out));
  } else if (tree && typeof tree === "object") {
    if (pred(tree)) out.push(tree);
    (tree.children || []).forEach((c) => find(c, pred, out));
  }
  return out;
}

/** text is the text under a tree. */
export function text(tree) {
  if (Array.isArray(tree)) return tree.map(text).join("");
  if (tree && typeof tree === "object") return (tree.children || []).map(text).join("");
  return tree == null ? "" : String(tree);
}

/** realKeys is the server's own key table, written out by the Go test that runs
 *  these, or null when a test is run by hand. */
export function realKeys() {
  const path = process.env.NEXT_KEYS_JSON;
  return path ? JSON.parse(readFileSync(path, "utf8")) : null;
}

/** A small hello and state, as the server sends them. */
export const fakeKeys = [
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Finding your way", noPalette: true },
  { id: "toggleRail", keys: "Ctrl+B", label: "Expand or collapse the rail", section: "The window" },
  { id: "projects", keys: "Ctrl+Shift+O", label: "Projects", section: "Finding your way" },
  { id: "nextRegion", keys: "F6", label: "Move to the next part of the window", section: "Finding your way", noPalette: true },
  { id: "prevRegion", keys: "Shift+F6", label: "Move to the previous part of the window", section: "Finding your way", noPalette: true },
  { id: "worktrees", keys: "Ctrl+Shift+G", label: "Worktrees", section: "Git" },
  { id: "changes", keys: "Ctrl+Shift+S", label: "Review changes, commit and push", short: "Changes", section: "Git" },
  { id: "agents", keys: "Ctrl+Shift+A", label: "All agents across projects", short: "Agents", section: "Agents" },
  { id: "quit", keys: "", label: "Quit — stop every agent in every project", section: "The window" },
];

export const fakeState = {
  root: "/work/b",
  projects: [
    { root: "/work/a", name: "alpha", waiting: 2, working: 0 },
    { root: "/work/b", name: "beta", waiting: 0, working: 3 },
    { root: "/work/c", name: "<img src=x onerror=alert(1)>", waiting: 0, working: 0 },
  ],
};
