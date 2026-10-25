// What the projects dialog reads and sends, without a DOM: the open projects of
// the state push, the recent and archived ones the desktop remembers, the folder
// listing used to open one or add a directory to one, and the commands. A project
// is a folder (or, grouped, several) with its own tabs and agents.
//
// Names and paths are text from the machine; the view draws them as children of
// elements and never as markup. A project is named by its full path in every
// command, as the desktop requires.

/** SHOWN_RECENT is how many recent projects are shown before "Show more". */
export const SHOWN_RECENT = 8;

const text = (v) => (typeof v === "string" ? v : "");
const count = (v) => (Number.isFinite(v) && v > 0 ? Math.floor(v) : 0);

/** openOf reads the open projects out of the state push. A project without a
 *  root is dropped; a project of one folder carries itself as its only member. */
export function openOf(state) {
  const list = state && Array.isArray(state.projects) ? state.projects : [];
  return list
    .filter((p) => p && typeof p === "object" && typeof p.root === "string" && p.root)
    .map((p) => {
      const members = (Array.isArray(p.members) ? p.members : [])
        .filter((m) => m && typeof m.root === "string" && m.root)
        .map((m) => ({ root: m.root, name: text(m.name) || m.root }));
      return {
        root: p.root,
        name: text(p.name) || p.root,
        active: p.active === true,
        archived: p.archived === true,
        tabs: count(p.tabs),
        panes: count(p.panes),
        waiting: count(p.waiting),
        working: count(p.working),
        members: members.length ? members : [{ root: p.root, name: text(p.name) || p.root }],
      };
    });
}

/** recentsOf reads the recents answer into the projects that are not open, in
 *  the order the desktop keeps them: { recent, archived }. */
export function recentsOf(msg) {
  const items = msg && Array.isArray(msg.items) ? msg.items : null;
  if (!items) return null;
  const all = items
    .filter((r) => r && typeof r === "object" && typeof r.root === "string" && r.root)
    .map((r) => ({ root: r.root, name: text(r.name) || r.root, exists: r.exists !== false, open: r.open === true, archived: r.archived === true }))
    .filter((r) => !r.open);
  return { recent: all.filter((r) => !r.archived), archived: all.filter((r) => r.archived) };
}

/** browseOf reads a folder listing: where it is, the way up, the places to jump
 *  to and the folders in it, or the reason it could not be read. */
export function browseOf(msg) {
  if (!msg || typeof msg.path !== "string") return null;
  const dirs = (v) => (Array.isArray(v) ? v : [])
    .filter((d) => d && typeof d.path === "string" && d.path)
    .map((d) => ({ name: text(d.name) || d.path, path: d.path, isRepo: d.isRepo === true, hidden: d.hidden === true }));
  return { path: msg.path, parent: text(msg.parent), isRepo: msg.isRepo === true, entries: dirs(msg.entries), places: dirs(msg.places), error: text(msg.error) };
}

/** cleanPath is a path as typed or pasted: trimmed, and without the quotes a file manager puts round a path with spaces. */
export function cleanPath(p) {
  return String(p || "").trim().replace(/^"(.*)"$/, "$1").trim();
}

/** isAbsolute says a path is a full one, as the desktop requires of a project:
 *  a drive, a UNC share, or a rooted Unix path. */
export function isAbsolute(p) {
  const s = cleanPath(p);
  return /^[A-Za-z]:[\\/]/.test(s) || s.startsWith("\\\\") || s.startsWith("/");
}

/** closeWords are the sentences asked before a project is closed, which stops
 *  every agent in every tab of it, or none when nothing is running in it. */
export function closeWords(p) {
  if (p.panes === 0 && p.working === 0 && p.waiting === 0) return [];
  const lines = ["Every agent in every tab of " + p.name + " stops."];
  const busy = p.working + p.waiting;
  if (busy > 0) lines.push((busy === 1 ? "1 agent is" : busy + " agents are") + " still working or waiting on you there.");
  return lines;
}

/** moveCommand swaps a project with its neighbour in a list kept in an order
 *  chosen by hand, and gives the command with the whole list's new order, or
 *  null when it cannot move that way. reorderProjects places only the projects
 *  it is given, so what is not in the list keeps its place. */
export function moveCommand(list, root, delta) {
  const at = list.findIndex((r) => r.root === root);
  const to = at + delta;
  if (at < 0 || to < 0 || to >= list.length) return null;
  const roots = list.map((r) => r.root);
  [roots[at], roots[to]] = [roots[to], roots[at]];
  return { cmd: "reorderProjects", roots };
}

export const recentsCommand = () => ({ cmd: "recents" });
export const browseCommand = (path, req) => ({ cmd: "browse", path: String(path || ""), ...(req ? { req } : {}) });

/** browseReq is the req of the nth folder listing asked for; the desk repeats it in its answer. */
export const browseReq = (n) => "b" + n;

/** staleBrowse says a folder listing answers an ask older than the last one, so
 *  it must not replace what the person is looking at. An answer with no req (from
 *  a desk before 1.7) is never stale. */
export function staleBrowse(msg, lastAsk) {
  const m = msg && typeof msg.req === "string" ? /^b(\d+)$/.exec(msg.req) : null;
  return !!m && Number(m[1]) < lastAsk;
}
export const selectCommand = (root) => ({ cmd: "selectProject", root });
export const selectRepoCommand = (root) => ({ cmd: "selectRepo", root });
export const openCommand = (path) => ({ cmd: "openProject", path });
export const closeCommand = (root) => ({ cmd: "closeProject", root });
export const removeCommand = (root) => ({ cmd: "removeProject", root });
export const archiveCommand = (root, archived) => ({ cmd: "archiveProject", root, archived: !!archived });
/** renameCommand renames an open project (through the group, which any of its
 *  members' roots reaches) or a remembered one. An empty name goes back to the
 *  folder's own. */
export const renameCommand = (root, name, open) => {
  const text = String(name || "").trim();
  return open ? { cmd: "renameGroup", root, text } : { cmd: "renameProject", root, text };
};
export const groupCommand = (roots, name) => ({ cmd: "groupProjects", roots, text: String(name || "").trim() });
export const addRepoCommand = (root, path) => ({ cmd: "addRepoToGroup", root, path });
export const removeRepoCommand = (root) => ({ cmd: "removeRepoFromGroup", root });
