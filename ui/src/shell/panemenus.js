// The menus for an agent (a row in the graph, the title of the drawer) and for
// the drawer's terminal. They read ctx.pane, which Live.js fills in (see menuCtx):
//   of(id)        { node, state } of a pane, or nulls
//   run(name, id) an action of the key table, on the pane with that id
//   terminal(el)  the controller of the terminal drawn in el
//   clipboard()   the page's clipboard, or null
//   openLink(url) opens an address outside the app

import { canTranscribe } from "../lib/paneinfo.js";

/** agentMenu is the menu of an element carrying data-pane. */
export function agentMenu(el, ctx) {
  const id = el.getAttribute("data-pane");
  const p = ctx.pane || {};
  if (!id) return [];
  const { node, state } = p.of ? p.of(id) : { node: null, state: null };
  const on = (name) => () => p.run && p.run(name, id);
  const act = (name, extra) => ({ action: name, run: on(name), ...extra });
  const transcript = canTranscribe(node, state) && !ctx.remote;
  const edit = !ctx.viewer;
  return [
    { id: "open", label: "Open", run: () => ctx.drawer.open(id), hidden: !ctx.drawer },
    act("paneInfo"),
    act("changes", { hidden: !(state && state.cwd) }),
    { separator: true },
    act("restartPane", { hidden: !edit }),
    act("lockPane", { label: "Lock", checked: !!(state && state.locked), hidden: !edit }),
    act("makeBaton", { hidden: !edit || ctx.remote }),
    act("recordPane", { label: state && state.recording ? "Stop recording" : "Start recording", hidden: !edit || !transcript }),
    act("exportTranscript", { hidden: !edit || !transcript }),
    act("revealTranscript", { hidden: !edit || !transcript }),
    { separator: true },
    { id: "copy-id", label: "Copy pane id", run: () => ctx.copy(id, "the pane id"), hidden: !ctx.copy },
    { id: "copy-name", label: "Copy name", run: () => ctx.copy(node.name, "the name"), hidden: !ctx.copy || !node || !node.name },
    { separator: true },
    act("closePane", { danger: true, hidden: !edit }),
  ];
}

/** termMenu is the menu of the drawer's terminal, or nothing when the program in
 *  it has asked for the mouse (Shift with the click gets this menu anyway). ev is
 *  the click, which says where a link under it is. */
export function termMenu(el, ctx, ev) {
  const p = ctx.pane || {};
  const ctl = p.terminal ? p.terminal(el) : null;
  if (!ctl) return [];
  if (ctl.mouseTracking() && !(ev && ev.shiftKey)) return [];
  const link = ev && (ev.clientX || ev.clientY) ? ctl.linkAt(ev.clientX, ev.clientY) : "";
  const text = ctl.selection();
  const clip = p.clipboard ? p.clipboard() : null;
  const paste = async () => {
    try {
      ctl.pasteText(await clip.readText());
    } catch {
      ctx.say("The clipboard could not be read, so nothing was pasted");
    }
  };
  return [
    { id: "open-link", label: "Open link", run: () => p.openLink(link), hidden: !link || !p.openLink },
    { id: "copy-link", label: "Copy link", run: () => ctx.copy(link, "the link"), hidden: !link || !ctx.copy },
    { separator: true, hidden: !link },
    { id: "copy", label: "Copy", run: () => ctx.copy(text, "the selection"), disabled: !text, hidden: !ctx.copy },
    { id: "paste", label: "Paste", run: paste, hidden: ctx.viewer || !clip || !clip.readText },
    { id: "select-all", label: "Select all", run: () => ctl.selectAll() },
    { id: "clear", label: "Clear", run: () => ctl.clear() },
    { separator: true },
    { action: "findInTerminal", run: () => p.run("findInTerminal") },
    { action: "zoomPane", run: () => p.run("zoomPane") },
    { separator: true },
    { action: "restartPane", run: () => p.run("restartPane"), hidden: ctx.viewer },
    { action: "closePane", run: () => p.run("closePane"), danger: true, hidden: ctx.viewer },
  ];
}
