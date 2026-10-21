// The drawer for one agent: a header, the card for what it is waiting on when it
// is waiting on the person (Prompt.js), and a Terminal tab and, for a pane with a
// stored conversation, a Conversation tab (Conversation.js). It shows the pane it
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
import { useEffect, useReducer, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Header } from "./Header.js";
import { Prompt } from "./Prompt.js";
import { Conversation } from "./Conversation.js";
import { Structured } from "./Structured.js";
import { TerminalView } from "../lib/terminal.js";
import { initialDrawer, reduceDrawer, focusNode, focusRow } from "./state.js";
import { LIST } from "../shell/keys.js";

/** useDrawer holds the drawer's state for a view. mainRef is the main area, where
 *  the graph's boxes are, for the keyboard to return to. paneIds is a Set of the
 *  panes in the graph now. */
export function useDrawer({ mainRef, paneIds }) {
  const [s, dispatch] = useReducer(reduceDrawer, undefined, initialDrawer);
  useEffect(() => {
    if (paneIds) dispatch({ type: "panes", ids: paneIds });
  }, [paneIds]);
  useEffect(() => {
    if (s.seq > 0 && s.pane === "") {
      const root = mainRef && mainRef.current;
      // Back to where it was opened from: the strip row, or the graph's box.
      if (!(s.returnVia === "strip" && focusRow(root, s.returnTo))) focusNode(root, s.returnTo);
    }
  }, [s.seq]);
  return {
    pane: s.pane,
    maximised: s.maximised,
    open: (pane, via = "") => dispatch({ type: "open", pane, via }),
    close: () => dispatch({ type: "close" }),
    toggleMax: () => dispatch({ type: "toggleMax" }),
  };
}

/** inTerminal says the keyboard is in the terminal's own input, where Esc is
 *  the program's. A button beside it (the size note, Try again) is not. */
const inTerminal = (target) => {
  if (!target || target.localName !== "textarea") return false;
  for (let n = target; n; n = n.parentNode) {
    if (n.getAttribute && /(^|\s)terminal-pane(\s|$)/.test(n.getAttribute("class") || "")) return true;
  }
  return false;
};

/** keysFor is the function that types into the terminal held in ref for a pane,
 *  or null when the ref holds none for that pane. The terminal is made and torn
 *  down by TerminalView's effects, which run after a render, so for a moment
 *  after the drawer moves to another pane the ref still holds the last pane's
 *  terminal. Checking the pane here, when the key is sent, is what keeps a choice
 *  from being typed into it. */
export function keysFor(ref, pane) {
  const held = ref && ref.current;
  if (!held || held.pane !== pane || !held.ctl || !held.ctl.sendKeys) return null;
  return (text, done) => held.ctl.sendKeys(text, done);
}

/** hasTerminal says whether a pane can have a terminal. A structured pane is
 *  driven over a protocol and has none. */
export function hasTerminal(node, pane) {
  return !(node && node.structured) && !(pane && pane.structured);
}

/** Drawer is the component. store is the store of store.js; node and pane are the
 *  graph node and the state push's entry for it. terminalOptions reach the
 *  terminal, which is how a test gives it fakes for xterm and the socket. */
function DrawerBody({ store, snap = null, node, pane = null, now, maximised = false, onClose, onToggleMax, terminalOptions = null }) {
  const rootRef = useRef(null);
  const terminalRef = useRef(null);
  const terminal = hasTerminal(node, pane);
  // Opening, or opening another pane, takes the keyboard into the drawer.
  useEffect(() => {
    if (rootRef.current && rootRef.current.focus) rootRef.current.focus();
  }, [node.pane]);
  // A structured pane's permission card comes with a number the answer must carry,
  // and only a structured watch brings it. Closing the drawer, or opening another
  // pane, gives the watch up.
  useEffect(() => {
    if (terminal) return undefined;
    store.watchStructured(node.pane);
    return () => store.unwatchStructured();
  }, [store, node.pane, terminal]);
  // The pane's conversation is asked for while its drawer is open, to know whether
  // it has one (a shell has none) and to keep the tab current. Closing the drawer,
  // or opening another pane, closes it.
  useEffect(() => {
    store.watchConversation(node.pane);
    return () => store.unwatchConversation();
  }, [store, node.pane]);
  const [tab, setTab] = useState("terminal");
  useEffect(() => {
    setTab("terminal");
  }, [node.pane]);
  const onKeyDown = (e) => {
    if (e.key !== LIST.cancel || e.defaultPrevented || inTerminal(e.target)) return;
    e.preventDefault();
    e.stopPropagation();
    if (onClose) onClose();
  };
  const state = snap || store.getState();
  const conv = state.conversation && state.conversation.id === node.pane ? state.conversation : null;
  // The tab is there for a pane with a conversation, and for one the desktop
  // refused this device, so the refusal is said where the conversation would be.
  const hasConv = !!conv && (conv.supported === true || !!conv.refused);
  const shown = tab === "conversation" && hasConv ? "conversation" : "terminal";
  const tabRefs = useRef({});
  const tabKeys = (e) => {
    if (!hasConv || (e.key !== "ArrowLeft" && e.key !== "ArrowRight") || e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) return;
    e.preventDefault();
    const next = shown === "terminal" ? "conversation" : "terminal";
    setTab(next);
    const el = tabRefs.current[next];
    if (el && el.focus) el.focus();
  };
  const sendKeys = (text, done) => {
    const send = keysFor(terminalRef, node.pane);
    return !!send && send(text, done);
  };
  return html`
    <section class=${"drawer-body" + (maximised ? " drawer-body--max" : "")} data-drawer-pane=${node.pane} tabindex="-1" ref=${rootRef} onKeyDown=${onKeyDown}>
      <${Header} node=${node} pane=${pane} now=${now} maximised=${maximised} onClose=${onClose} onToggleMax=${onToggleMax} />
      <${Prompt} node=${node} pane=${pane} snap=${state} store=${store} sendKeys=${sendKeys} terminal=${terminal} />
      <div class="drawer-tabs" role="tablist" aria-label="Views of this agent" onKeyDown=${tabKeys}>
        <button type="button" role="tab" id="drawer-tab-terminal" aria-selected=${shown === "terminal" ? "true" : "false"} aria-controls="drawer-panel-terminal" tabindex=${shown === "terminal" ? 0 : -1} ref=${(el) => { tabRefs.current.terminal = el; }} class=${"drawer-tab" + (shown === "terminal" ? " is-active" : "")} onClick=${() => setTab("terminal")}>Terminal</button>
        ${hasConv
          ? html`<button type="button" role="tab" id="drawer-tab-conversation" aria-selected=${shown === "conversation" ? "true" : "false"} aria-controls="drawer-panel-conversation" tabindex=${shown === "conversation" ? 0 : -1} ref=${(el) => { tabRefs.current.conversation = el; }} class=${"drawer-tab" + (shown === "conversation" ? " is-active" : "")} onClick=${() => setTab("conversation")}>Conversation</button>`
          : null}
      </div>
      <div class="drawer-panel" role="tabpanel" id="drawer-panel-terminal" aria-labelledby="drawer-tab-terminal" hidden=${shown !== "terminal"}>
        ${terminal
          ? html`<${TerminalView} key=${node.pane} store=${store} paneId=${node.pane} onReady=${(c) => { terminalRef.current = { pane: node.pane, ctl: c }; }} ...${terminalOptions || {}} />`
          : html`<${Structured} key=${node.pane} pane=${node.pane} st=${state.structured} error=${state.structuredError} cancelError=${state.cancelError} desk=${!state.remote} store=${store} />`}
      </div>
      ${hasConv
        ? html`<div class="drawer-panel" role="tabpanel" id="drawer-panel-conversation" aria-labelledby="drawer-tab-conversation" hidden=${shown !== "conversation"}>
            <${Conversation} conv=${conv} onOlder=${() => store.olderConversation()} />
          </div>`
        : null}
    </section>
  `;
}

/** Drawer draws nothing for a node that is not there: the graph can drop a pane
 *  a render before the state that closes its drawer is applied. */
export function Drawer(props) {
  return props.node ? html`<${DrawerBody} ...${props} />` : null;
}
