// The list of every pane in every open project, as the desktop sends it for the
// Agents dialog. The desktop sorts it so that what needs a person comes first;
// this reads it defensively and groups it by project when asked. Every string in
// it came from an agent or a person and is drawn as text. A window reached
// through the relay is sent the list with the detail text and tab titles scrubbed.

const str = (v) => (typeof v === "string" ? v : "");

/** agentsOf reads the desktop's agents answer. */
export function agentsOf(msg) {
  const items = [];
  for (const a of msg && Array.isArray(msg.items) ? msg.items : []) {
    if (!a || typeof a !== "object" || typeof a.paneId !== "string" || !a.paneId) continue;
    items.push({
      paneId: a.paneId, tabId: str(a.tabId), tab: str(a.tab), root: str(a.root), project: str(a.project), name: str(a.name),
      branch: str(a.branch), kind: str(a.kind), role: str(a.role), status: str(a.status), activity: str(a.activity),
      detail: str(a.detail), waiting: str(a.waiting), failed: a.failed === true, since: str(a.for),
      dirty: Number.isInteger(a.dirty) && a.dirty > 0 ? a.dirty : 0, parent: str(a.parent),
      background: Number.isInteger(a.background) && a.background > 0 ? a.background : 0,
    });
  }
  return items;
}

/** needsYou says a pane is waiting on, or blocked for, the person. */
export const needsYou = (a) => a.activity === "waiting" || a.activity === "blocked";

/** byProject groups the panes under their project, keeping the order of the list
 *  within each group and putting the project with the most urgent pane first. */
export function byProject(items) {
  const groups = [];
  const at = new Map();
  for (const a of items) {
    const key = a.root + "\n" + a.project;
    if (!at.has(key)) {
      at.set(key, groups.length);
      groups.push({ root: a.root, project: a.project || a.root || "Project", panes: [] });
    }
    groups[at.get(key)].panes.push(a);
  }
  return groups;
}

/** activityWords is what a row says a pane is doing. */
export function activityWords(a) {
  if (a.waiting) return a.waiting;
  if (a.failed) return "failed";
  return a.activity || a.status || "";
}

/** revealAgentCommand brings a pane to the front on the desktop. */
export const revealAgentCommand = (a) => ({ cmd: "revealPane", root: a.root, node: a.tabId, id: a.paneId });
export const AGENTS_COMMAND = { cmd: "agents" };
