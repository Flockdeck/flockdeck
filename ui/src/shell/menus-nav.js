// The navigation menus: goal tabs, rail projects, the rows of the Worktrees and
// Resume dialogs, and the empty part of the page. They are added to MENUS in
// menus.js. What they read comes from the "nav" block of the ctx bag (Live.js):
// the tabs, and the functions a part of the page registers on it while it is
// drawn (GoalStrip its rename and archive, a dialog the actions of its rows).
// Each reads nav when the menu opens, so a part that is not drawn has no menu.

const label = (text) => String(text == null ? "" : text);

export const NAV_MENUS = {
  // A goal's tab, or an archived goal's entry in the "+" menu (data-archived).
  tab(el, ctx) {
    const id = el.getAttribute("data-tab");
    const nav = ctx.nav || {};
    const archived = el.getAttribute("data-archived") === "1";
    const tab = (archived ? nav.archived : nav.tabs || []).find((t) => t.id === id);
    if (!tab) return [];
    if (archived) return [{ id: "restore", label: "Show this goal", run: () => ctx.tabDo.restore(id) }];
    const goal = tab.kind === "team";
    const step = (dir) => ({
      id: dir < 0 ? "left" : "right",
      label: dir < 0 ? "Move left" : "Move right",
      run: () => ctx.tabDo.step(id, dir),
      disabled: !nav.canStep || !nav.canStep(id, dir),
    });
    return [
      { id: "open", label: "Open", run: () => nav.select && nav.select(id), hidden: id === nav.active || !nav.select },
      { id: "rename", label: "Rename…", run: () => nav.rename && nav.rename(id), hidden: !goal || !nav.rename },
      { id: "archive", label: "Archive", run: () => (nav.archive || ctx.tabDo.archive)(id), hidden: !goal },
      { ...step(-1), hidden: !goal },
      { ...step(1), hidden: !goal },
      { separator: true },
      { action: "newAgentTab" },
      { action: "newAgentTabChoose" },
      { action: "newShellTab" },
      { action: "history" },
      { action: "findPane" },
    ];
  },

  // A project in the rail. Worktrees and the history are of the project on screen.
  rail(el, ctx) {
    const root = el.getAttribute("data-root");
    const here = root === ctx.selected;
    return [
      { id: "switch", label: "Switch to this project", run: () => ctx.selectProject && ctx.selectProject(root), hidden: here || !ctx.selectProject },
      { action: "worktrees", hidden: !here },
      { action: "history", hidden: !here },
      { separator: true },
      { id: "copy-path", label: "Copy path", run: () => ctx.copy && ctx.copy(root, "the project path"), hidden: !root || !ctx.copy },
      { id: "projects", label: "Manage projects…", run: () => ctx.nav && ctx.nav.projects && ctx.nav.projects(), hidden: !(ctx.nav && ctx.nav.projects) },
    ];
  },

  // A row of the Worktrees dialog. The dialog registers nav.worktree while it is open.
  worktree(el, ctx) {
    const path = el.getAttribute("data-path");
    const dlg = ctx.nav && ctx.nav.worktree;
    const wt = dlg && dlg.find(path);
    if (!wt) return [];
    return [
      { id: "agent", label: "Open an agent here", run: () => dlg.here(wt, "agent"), hidden: wt.prunable, disabled: dlg.busy },
      { id: "shell", label: "Open a shell here", run: () => dlg.here(wt, "shell"), hidden: wt.prunable, disabled: dlg.busy },
      { id: "prune", label: "Prune", run: () => dlg.prune(wt), hidden: !wt.prunable, disabled: dlg.busy },
      { separator: true },
      { id: "copy-path", label: "Copy path", run: () => ctx.copy && ctx.copy(path, "the path"), hidden: !path || !ctx.copy },
      { id: "copy-branch", label: "Copy branch name", run: () => ctx.copy(wt.branch, "the branch name"), hidden: !wt.branch || !ctx.copy },
      { separator: true },
      { id: "remove", label: "Remove…", danger: true, run: () => dlg.remove(wt), hidden: wt.main, disabled: dlg.busy },
    ];
  },

  // A row of the Resume dialog. The dialog registers nav.history while it is open.
  history(el, ctx) {
    const id = el.getAttribute("data-conv");
    const dlg = ctx.nav && ctx.nav.history;
    const c = dlg && dlg.find(id);
    if (!c) return [];
    return [
      { id: "resume", label: "Resume", run: () => dlg.resume(c), hidden: c.open },
      { id: "copy-id", label: "Copy conversation id", run: () => ctx.copy && ctx.copy(id, "the conversation id"), hidden: !ctx.copy },
      { id: "copy-summary", label: "Copy summary", run: () => ctx.copy && ctx.copy(label(c.summary), "the summary"), hidden: !c.summary || !ctx.copy },
    ];
  },

  // The empty part of the page: the main area itself or a part flagged
  // data-background, not what is drawn in it (links, text and dialogs keep the
  // window's menu). Its actions do nothing under a modal dialog, so it has none then.
  blank(el, ctx, target) {
    const bare = target === el || (target && target.getAttribute && target.getAttribute("data-background") !== null);
    if (!bare || ctx.blocked) return [];
    return [
      { action: "palette" },
      { separator: true },
      { action: "newAgentTab" },
      { action: "newAgentTabChoose" },
      { action: "newShellTab" },
      { separator: true },
      { action: "settings" },
    ];
  },
};
