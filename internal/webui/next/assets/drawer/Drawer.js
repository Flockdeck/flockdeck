// The drawer for one agent: a header and a Terminal tab. It shows the pane it
// is given and nothing else, and the shell puts it beside the graph, which keeps
// drawing and keeps updating behind it.
//
// Esc closes it and the keyboard returns to the pane's box in the graph, except
// while the keyboard is in the terminal: there Esc belongs to the program in it.
// The Close button and the region keys work from there.
//
// Maximise fills the main area with the drawer and Restore puts it back. Neither
// touches the terminal's size: the pty is resized only by fitToBox, which
// nothing here calls.
import { useEffect, useReducer, useRef } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Header } from "./Header.js";
import { TerminalView, STRUCTURED_STUB } from "../lib/terminal.js";
import { initialDrawer, reduceDrawer, focusNode } from "./state.js";

/** useDrawer holds the drawer's state for a view. mainRef is the main area, where
 *  the graph's boxes are, for the keyboard to return to. paneIds is a Set of the
 *  panes in the graph now. */
export function useDrawer({ mainRef, paneIds }) {
  const [s, dispatch] = useReducer(reduceDrawer, undefined, initialDrawer);
  useEffect(() => {
    if (paneIds) dispatch({ type: "panes", ids: paneIds });
  }, [paneIds]);
  useEffect(() => {
    if (s.seq > 0 && s.pane === "") focusNode(mainRef && mainRef.current, s.returnTo);
  }, [s.seq]);
  return {
    pane: s.pane,
    maximised: s.maximised,
    open: (pane) => dispatch({ type: "open", pane }),
    close: () => dispatch({ type: "close" }),
    toggleMax: () => dispatch({ type: "toggleMax" }),
  };
}

const inTerminal = (target) => {
  for (let n = target; n; n = n.parentNode) {
    if (n.getAttribute && /(^|\s)terminal-pane(\s|$)/.test(n.getAttribute("class") || "")) return true;
  }
  return false;
};

/** hasTerminal says whether a pane can have a terminal. A structured pane is
 *  driven over a protocol and has none. */
export function hasTerminal(node, pane) {
  return !(node && node.structured) && !(pane && pane.structured);
}

/** Drawer is the component. store is the store of store.js; node and pane are the
 *  graph node and the state push's entry for it. terminalOptions reach the
 *  terminal, which is how a test gives it fakes for xterm and the socket. */
export function Drawer({ store, node, pane = null, now, maximised = false, onClose, onToggleMax, terminalOptions = null }) {
  const rootRef = useRef(null);
  // Opening, or opening another pane, takes the keyboard into the drawer.
  useEffect(() => {
    if (rootRef.current && rootRef.current.focus) rootRef.current.focus();
  }, [node.pane]);
  const onKeyDown = (e) => {
    if (e.key !== "Escape" || e.defaultPrevented || inTerminal(e.target)) return;
    e.preventDefault();
    e.stopPropagation();
    if (onClose) onClose();
  };
  const terminal = hasTerminal(node, pane);
  return html`
    <section class=${"drawer-body" + (maximised ? " drawer-body--max" : "")} data-drawer-pane=${node.pane} tabindex="-1" ref=${rootRef} onKeyDown=${onKeyDown}>
      <${Header} node=${node} pane=${pane} now=${now} maximised=${maximised} onClose=${onClose} onToggleMax=${onToggleMax} />
      <div class="drawer-tabs" role="tablist" aria-label="Views of this agent">
        <button type="button" role="tab" id="drawer-tab-terminal" aria-selected="true" aria-controls="drawer-panel-terminal" class="drawer-tab is-active">Terminal</button>
      </div>
      <div class="drawer-panel" role="tabpanel" id="drawer-panel-terminal" aria-labelledby="drawer-tab-terminal">
        ${terminal
          ? html`<${TerminalView} key=${node.pane} store=${store} paneId=${node.pane} ...${terminalOptions || {}} />`
          : html`<p class="drawer-stub">${STRUCTURED_STUB}</p>`}
      </div>
    </section>
  `;
}
