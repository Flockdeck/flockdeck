// Worktrees: the other checkouts of a repository that agents work in, one each, so
// that two agents on the same project do not edit the same files. The desktop lists
// them (worktrees), makes one (worktreeAdd), removes one (worktreeRemove) and clears
// the records of ones whose folders are gone (worktreePrune), and answers each with
// the listing again.
//
// The desktop does the checks that matter: it will not remove a worktree an agent
// is working in, or a locked one, and it never deletes a branch. This page repeats
// the first of them before it sends anything, so the person is told in words what
// to do, and always asks before a removal. Every string in a listing (a branch, a
// path, a repository name) came from git or the person and is drawn as text.

export const WHY_WORDS =
  "A worktree is another checkout of this repository. Each agent works in its own, on its own branch, so two agents never edit the same files at once and you can merge their work one at a time.";
export const BRANCH_KEPT_WORDS = "Removing a worktree deletes its folder only. Its branch, and every commit on it, is kept.";
export const RADAR_WORDS =
  "Off by default. On, about every 15 seconds Flockdeck copies the changed files of the panes on screen that share a repository into a scratch folder in the system's temporary folder, " +
  "and git merges the copies in memory to predict whether two panes' work would conflict (a pane with over 100 MB of changed files, or one file over 25 MB, is skipped). " +
  "A pane in a predicted conflict shows a warning with the other pane's name. The names of panes, their branches and up to 20 file paths, never file contents, are sent to every open window, " +
  "including a paired phone or relay window. It is a prediction, and no warning is not a promise. It needs git 2.38 or newer.";

const str = (v) => (typeof v === "string" ? v : "");
const count = (v) => (Number.isInteger(v) && v > 0 ? v : 0);

/** worktreesOf reads the desktop's listing. error is set when the project is not a
 *  repository, and repos then lists repositories just inside it to choose from. */
export function worktreesOf(msg) {
  const m = msg && typeof msg === "object" ? msg : {};
  const items = (Array.isArray(m.items) ? m.items : [])
    .filter((x) => x && typeof x === "object" && typeof x.path === "string" && x.path)
    .map((x) => ({
      path: x.path, label: str(x.label) || x.path, branch: str(x.branch), head: str(x.head), upstream: str(x.upstream),
      main: x.main === true, locked: x.locked === true, detached: x.detached === true, prunable: x.prunable === true,
      dirty: count(x.dirty), untracked: count(x.untracked), ahead: count(x.ahead), behind: count(x.behind), panes: count(x.panes),
      repo: str(x.repo), repoRoot: str(x.repoRoot),
    }));
  const branches = (Array.isArray(m.branches) ? m.branches : [])
    .filter((b) => b && typeof b === "object" && typeof b.name === "string" && b.name)
    .map((b) => ({ name: b.name, current: b.current === true, checkedIn: str(b.checkedIn), repo: str(b.repo), repoRoot: str(b.repoRoot) }));
  const repos = (Array.isArray(m.repos) ? m.repos : [])
    .filter((r) => r && typeof r === "object" && typeof r.path === "string" && r.path)
    .map((r) => ({ path: r.path, name: str(r.name) || r.path }));
  return { root: str(m.root), defaultBase: str(m.defaultBase), items, branches, repos, error: str(m.error) };
}

/** membersOf lists the repositories of a project that spans more than one, as
 *  { name, root }, or [] for the ordinary project of one repository. */
export function membersOf(w) {
  const seen = new Map();
  for (const x of [...w.items, ...w.branches]) if (x.repoRoot && !seen.has(x.repoRoot)) seen.set(x.repoRoot, { name: x.repo || x.repoRoot, root: x.repoRoot });
  return seen.size > 1 ? [...seen.values()] : [];
}

/** changesWords says what is uncommitted in a worktree, or "" when nothing is. */
export function changesWords(wt) {
  const bits = [];
  if (wt.dirty) bits.push(wt.dirty + (wt.dirty === 1 ? " changed file" : " changed files"));
  if (wt.untracked) bits.push(wt.untracked + (wt.untracked === 1 ? " new file" : " new files"));
  return bits.join(" and ");
}

/** conflictsOf groups worktrees that more than one open pane is working in: two
 *  agents in one checkout can overwrite each other's files, which is what a
 *  worktree each is meant to prevent. */
export function sharedOf(items) {
  return items.filter((wt) => wt.panes > 1 && !wt.prunable);
}

export function sharedWords(wt) {
  return wt.panes + " agents are working in " + wt.label + ", so they can overwrite each other's files. Give each its own worktree.";
}

/** removalOf says what removing a worktree needs. It is { refuse } with the words
 *  to show when it cannot be done, or { title, words, label } for the question to
 *  ask first. A worktree with changes names how many and says they are discarded. */
export function removalOf(wt) {
  if (wt.main) return { refuse: "The main checkout is not removed from here." };
  if (wt.locked) return { refuse: wt.label + " is locked, so it is kept. Unlock it with git worktree unlock first." };
  if (wt.panes) {
    return { refuse: (wt.panes === 1 ? "An agent is" : wt.panes + " agents are") + " working in " + wt.label + ". Close " + (wt.panes === 1 ? "that pane" : "those panes") + " first, then remove it." };
  }
  const changes = changesWords(wt);
  const words = ["It deletes the folder " + wt.path + "."];
  if (changes) words.push("It has " + changes + " that are not committed. Removing it discards them for good.");
  words.push(wt.branch ? "The branch " + wt.branch + " is kept" + (wt.ahead ? ", with its " + wt.ahead + (wt.ahead === 1 ? " commit" : " commits") + " not yet pushed" : "") + "." : BRANCH_KEPT_WORDS);
  return { title: "Remove " + wt.label + "?", words, label: changes ? "Remove and discard" : "Remove it", force: !!changes };
}

// --- commands. Each is written out with its name, which is also how the parity
// scan finds it.

/** listCommand asks for the listing; root names a repository chosen from the
 *  offered ones, or is empty for the project on screen. */
export const listCommand = (root = "") => (root ? { cmd: "worktrees", root } : { cmd: "worktrees" });

/** addCommand makes a worktree on a branch, new or existing. base is where a new
 *  branch starts and root the repository of a project that spans more than one. */
export function addCommand(branch, base = "", root = "") {
  const text = str(branch).trim();
  if (!text) return null;
  const c = { cmd: "worktreeAdd", text };
  const b = str(base).trim();
  if (b) c.base = b;
  if (root) c.root = root;
  return c;
}

/** removeCommand removes a worktree, with force when it has uncommitted work. A
 *  worktree an agent is in, a locked one and the main one are not sent. */
export function removeCommand(wt, root = "") {
  if (removalOf(wt).refuse) return null;
  const c = { cmd: "worktreeRemove", path: wt.path, force: wt.dirty > 0 || wt.untracked > 0 };
  if (root) c.root = root;
  return c;
}

export const pruneCommand = (root = "") => (root ? { cmd: "worktreePrune", root } : { cmd: "worktreePrune" });

/** openHereCommand opens a tab in a worktree, with an agent or a shell. */
export function openHereCommand(wt, kind) {
  if (wt.prunable) return null;
  return { cmd: "newTab", kind: kind === "shell" ? "shell" : "agent", path: wt.path, text: wt.label };
}

