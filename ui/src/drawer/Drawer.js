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
// touches the terminal's size itself: the terminal measures its box when it
// changes and tells the server (lib/terminal.js), which sizes the pane for the
// window last used.
import { Rename, canRename } from "./Rename.js";
import { useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Header } from "./Header.js";
import { Prompt, StreamPermission, waitingOn } from "./Prompt.js";
import { StreamDecision } from "./StreamDecision.js";
import { Conversation } from "./Conversation.js";
import { ChipsContext } from "./conv/chips.js";
import { Structured } from "./Structured.js";
import { Message } from "./Message.js";
import { isViewer } from "../lib/role.js";
import { managerTimeline, timelineEntries, isManager, mailFloor, withFloor } from "../graph/ManagerTimeline.js";
import { Lifecycle } from "./Lifecycle.js";
import { Task } from "./Task.js";
import { Changes } from "./Changes.js";
import { canTranscribe } from "../lib/paneinfo.js";
import { Tools } from "./Tools.js";
import { FindBar } from "./FindBar.js";
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
function DrawerBody({ store, snap = null, node, pane = null, now, maximised = false, onClose, onToggleMax, onOpenPane = null, terminalOptions = null, life = null, tx = null, onInfo = null, findAsk = 0, tabAsk = null, order = [], onStartTeam = null, helpers = [], layout = "side", onLayout = null, team = null, onBox = null }) {
  const rootRef = useRef(null);
  const terminalRef = useRef(null);
  // The terminal's controller once it is made, for the search bar, and the search
  // bar's state: whether it is open, what was last searched for (kept across
  // closing it and across panes, as a browser keeps it) and the latest count.
  const [ctl, setCtl] = useState(null);
  const [finding, setFinding] = useState(false);
  const [lastFind, setLastFind] = useState("");
  const [matches, setMatches] = useState(null);
  const [askSeq, setAskSeq] = useState(0);
  const terminal = hasTerminal(node, pane);
  // The settings that reach the terminal (font, cursor, scrollback) follow the
  // window's settings while it is open.
  const termSettings = (terminalOptions && terminalOptions.termOptions) || null;
  const termKey = JSON.stringify(termSettings || {});
  useEffect(() => {
    const t = terminalRef.current;
    if (termSettings && t && t.ctl && t.pane === node.pane && t.ctl.setOptions) t.ctl.setOptions(termSettings);
  }, [termKey]);
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
  const [taskOpen, setTaskOpen] = useState(false);
  // The pane's actions (restart, lock, close, info, find, recording, export) are
  // behind one button in the header: eight buttons in three rows were 130 px of
  // the drawer before any of its content.
  const [more, setMore] = useState(false);
  useEffect(() => {
    setTab("terminal");
    setMore(false);
    setTaskOpen(false);
    setFinding(false);
    setMatches(null);
  }, [node.pane]);
  // Find in terminal, asked for from a key or the palette: the bar opens on the
  // terminal tab, or takes the keyboard back if it is already open.
  const seenAsk = useRef(findAsk);
  useEffect(() => {
    // Only a new ask counts: a drawer opened later does not act on an old one.
    if (findAsk === seenAsk.current) return;
    seenAsk.current = findAsk;
    if (terminal) {
      setTab("terminal");
      setFinding(true);
      setAskSeq((n) => n + 1);
    }
  }, [findAsk]);
  // A tab asked for from a key (Changes): only a new ask counts, and only a tab
  // this pane has.
  // A drawer opened by the ask itself (tabAsk.pane is its pane) acts on it once.
  const seenTab = useRef(tabAsk ? (tabAsk.pane && tabAsk.pane === node.pane ? tabAsk.n - 1 : tabAsk.n) : 0);
  useEffect(() => {
    if (!tabAsk || tabAsk.n === seenTab.current) return;
    seenTab.current = tabAsk.n;
    if (tabAsk.tab === "changes" && pane && pane.cwd) setTab("changes");
  }, [tabAsk && tabAsk.n]);
  // [ and ] open the previous and next agent of the graph. They are keys of the
  // drawer, so a box or the terminal that has the keyboard keeps them.
  const at = order.indexOf(node.pane);
  const step = (d) => {
    if (!onOpenPane || order.length < 2) return;
    onOpenPane(order[(Math.max(at, 0) + d + order.length) % order.length]);
  };
  const typing = (t) => !!t && (t.localName === "textarea" || t.localName === "input" || t.localName === "select" || t.isContentEditable === true);
  const onKeyDown = (e) => {
    if ((e.key === "[" || e.key === "]") && !e.defaultPrevented && !e.ctrlKey && !e.altKey && !e.metaKey && !typing(e.target)) {
      e.preventDefault();
      step(e.key === "]" ? 1 : -1);
      return;
    }
    if (e.key !== LIST.cancel || e.defaultPrevented || inTerminal(e.target)) return;
    e.preventDefault();
    e.stopPropagation();
    if (onClose) onClose();
  };
  const state = snap || store.getState();
  const own = state.conversation && state.conversation.id === node.pane ? state.conversation : null;
  // A team manager's conversation also holds what the team sent it and what happened to the work, in time order.
  const conv = useMemo(() => (own && own.supported === true && team && isManager(team.model, node.pane)
    ? { ...own, entries: withFloor(timelineEntries(managerTimeline({ entries: own.entries || [], model: team.model, manager: node.pane, events: team.events || [] })), mailFloor(team.model)) }
    : own), [own, team && team.model, team && team.events, node.pane]);
  // The tab is there for a pane with a conversation, and for one the desktop
  // refused this device, so the refusal is said where the conversation would be.
  // A structured pane's conversation is the body of its own panel, so the tab is for the others.
  const hasConv = terminal && !!conv && (conv.supported === true || !!conv.refused);
  // The Changes tab is for a pane that has a directory to look at.
  const hasChanges = !!(pane && pane.cwd);
  // A structured pane has no terminal: the first tab is its transcript, and the
  // tab's id stays the same so the keys and asks that name it still reach it.
  const tabs = ["terminal"].concat(hasConv ? ["conversation"] : [], hasChanges ? ["changes"] : []);
  const shown = tabs.includes(tab) ? tab : "terminal";
  // Whether the Conversation tab shows its own message box, so the page can drop the home bar for the same manager.
  const hasBox = hasConv && shown === "conversation" && isManager(team && team.model, node.pane) && terminal && !state.remote && !isViewer(state) && !node.shell;
  useLayoutEffect(() => {
    if (!onBox) return undefined;
    // The second argument takes the keyboard to the box, for the key that aims at the manager.
    onBox(hasBox ? node.pane : "", () => {
      const el = rootRef.current && rootRef.current.querySelector(".structured-input");
      if (el && el.focus) el.focus();
    });
    return () => onBox("", null);
  }, [hasBox, node.pane]);
  const tabRefs = useRef({});
  const tabKeys = (e) => {
    if (tabs.length < 2 || (e.key !== "ArrowLeft" && e.key !== "ArrowRight") || e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) return;
    e.preventDefault();
    const step = e.key === "ArrowRight" ? 1 : -1;
    const next = tabs[(tabs.indexOf(shown) + step + tabs.length) % tabs.length];
    setTab(next);
    const el = tabRefs.current[next];
    if (el && el.focus) el.focus();
  };
  // A structured pane's permission is drawn in the conversation too, where it was
  // asked, as the same card the strip has. Only the desk has the card to answer.
  const card = !terminal && !state.remote && state.structured && state.structured.id === node.pane && state.structured.card ? state.structured.card : null;
  // A terminal pane's question is drawn at the end of its conversation, as a decision for the person.
  const asked = terminal && !state.remote && waitingOn(node, pane) === "ask";
  const pending = card
    ? { card, node: html`<${StreamPermission} node=${node} card=${card} snap=${state} store=${store} />` }
    : asked ? { card: {}, node: html`<${StreamDecision} key=${"ask" + (pane && pane.statusSince) + "|" + pane.ask.questions.map((q) => q.question).join("")} node=${node} pane=${pane} snap=${state} store=${store} now=${now} />` } : null;
  // While the transcript is showing, the permission is asked there, in the flow, and
  // not a second time above the tabs. Another tab, or a transcript not loaded yet, has none to carry it.
  const inFlow = !!pending && !!conv && conv.supported === true && !conv.refused && shown === (asked ? "conversation" : "terminal");
  const details = state.details || null;
  const onDetail = (id) => store.requestDetail(id);
  const onOlder = () => store.olderConversation();
  const onSearch = (q) => store.searchConversation(q);
  const onClearSearch = () => store.clearSearch();
  const canExport = !!tx && !state.remote && canTranscribe(node, pane);
  // The actions show when asked for, and stay while a question about one of them is
  // open, so a confirmation cannot be hidden by closing the button's panel.
  const asking = (life && life.request && life.request.pane === node.pane) || (tx && tx.request && tx.request.pane === node.pane);
  const showMore = more || !!asking;
  // The names of the other agents, so a reply that names one gets a chip that opens it.
  const chips = useMemo(() => (onOpenPane ? { names: helpers.filter((n) => n.pane !== node.pane), open: onOpenPane } : null), [helpers, node.pane, onOpenPane]);
  const sendKeys = (text, done) => {
    const send = keysFor(terminalRef, node.pane);
    return !!send && send(text, done);
  };
  return html`
    <${ChipsContext.Provider} value=${chips}>
    <section class=${"drawer-body" + (maximised ? " drawer-body--max" : "")} data-drawer-pane=${node.pane} tabindex="-1" ref=${rootRef} onKeyDown=${onKeyDown}>
      <${Header} node=${node} pane=${pane} now=${now} maximised=${maximised} onClose=${onClose} onToggleMax=${onToggleMax}
        taskOpen=${taskOpen} onTask=${() => setTaskOpen(!taskOpen)} onStep=${order.length > 1 ? step : null} moreOpen=${showMore} onMore=${life || tx ? () => setMore(!more) : null}
        onStartTeam=${onStartTeam && !node.team && !node.shell ? onStartTeam : null} layout=${layout} onLayout=${onLayout} />
      ${taskOpen && node.task ? html`<${Task} key=${node.task.id} node=${node} snap=${state} store=${store} onOpenPane=${onOpenPane} onClose=${() => setTaskOpen(false)} />` : null}
      ${showMore
        ? html`<div class="drawer-more-panel" id="drawer-more">
            ${life ? html`<${Lifecycle} node=${node} pane=${pane} life=${life} />` : null}
            ${life && canRename(state) ? html`<${Rename} key=${node.pane} node=${node} send=${(c) => store.send(c)} />` : null}
            ${tx
              ? html`<${Tools} node=${node} pane=${pane} tx=${tx} remote=${!!state.remote} terminal=${terminal} finding=${finding} send=${(c) => store.send(c)}
                  onInfo=${onInfo} onFind=${() => { setTab("terminal"); setFinding(true); setAskSeq((n) => n + 1); }} />`
              : null}
          </div>`
        : null}
      ${inFlow ? null : html`<${Prompt} node=${node} pane=${pane} snap=${state} store=${store} sendKeys=${sendKeys} terminal=${terminal} />`}
      <div class="drawer-tabs" role="tablist" aria-label="Views of this agent" onKeyDown=${tabKeys}>
        <button type="button" role="tab" id="drawer-tab-terminal" aria-selected=${shown === "terminal" ? "true" : "false"} aria-controls="drawer-panel-terminal" tabindex=${shown === "terminal" ? 0 : -1} ref=${(el) => { tabRefs.current.terminal = el; }} class=${"drawer-tab" + (shown === "terminal" ? " is-active" : "")} onClick=${() => setTab("terminal")}>${terminal ? "Terminal" : "Transcript"}</button>
        ${hasConv
          ? html`<button type="button" role="tab" id="drawer-tab-conversation" aria-selected=${shown === "conversation" ? "true" : "false"} aria-controls="drawer-panel-conversation" tabindex=${shown === "conversation" ? 0 : -1} ref=${(el) => { tabRefs.current.conversation = el; }} class=${"drawer-tab" + (shown === "conversation" ? " is-active" : "")} onClick=${() => setTab("conversation")}>Conversation</button>`
          : null}
        ${hasChanges
          ? html`<button type="button" role="tab" id="drawer-tab-changes" aria-selected=${shown === "changes" ? "true" : "false"} aria-controls="drawer-panel-changes" tabindex=${shown === "changes" ? 0 : -1} ref=${(el) => { tabRefs.current.changes = el; }} class=${"drawer-tab" + (shown === "changes" ? " is-active" : "")} onClick=${() => setTab("changes")}>Changes</button>`
          : null}
      </div>
      <div class="drawer-panel" role="tabpanel" id="drawer-panel-terminal" aria-labelledby="drawer-tab-terminal" hidden=${shown !== "terminal"}>
        ${terminal && finding && ctl
          ? html`<${FindBar} key=${node.pane} ctl=${ctl} name=${node.name} matches=${matches} initial=${lastFind} askSeq=${askSeq}
              onClose=${(words) => { setLastFind(words); setFinding(false); setMatches(null); }} />`
          : null}
        ${terminal
          ? html`<${TerminalView} key=${node.pane} store=${store} paneId=${node.pane} onReady=${(c) => { terminalRef.current = { pane: node.pane, ctl: c }; setCtl(c); }} ...${terminalOptions || {}} onMatches=${setMatches} />`
          : html`<${Structured} key=${node.pane} pane=${node.pane} snap=${state} st=${state.structured} error=${state.structuredError} cancelError=${state.cancelError} settingError=${state.settingError} queueResult=${state.queueResult} shellResult=${state.shellResult} exportResult=${state.exportResult} infoResult=${state.infoResult} signInResult=${state.signInResult} desk=${!state.remote} store=${store} conv=${conv} onOlder=${onOlder} details=${details} onDetail=${onDetail} pending=${pending}
              search=${state.search} onSearch=${onSearch} onClearSearch=${onClearSearch} />`}
      </div>
      ${hasConv
        ? html`<div class="drawer-panel" role="tabpanel" id="drawer-panel-conversation" aria-labelledby="drawer-tab-conversation" hidden=${shown !== "conversation"}>
            <${Conversation} conv=${conv} onOlder=${onOlder} details=${details} onDetail=${onDetail} pending=${pending}
              search=${state.search} onSearch=${onSearch} onClearSearch=${onClearSearch} canExport=${canExport} onExport=${() => tx.exportTranscript(node.pane)} />
            ${terminal && !state.remote && !isViewer(state) && !node.shell
              ? html`<${Message} key=${node.pane} pane=${node.pane} name=${node.name} store=${store} />`
              : null}
          </div>`
        : null}
      ${hasChanges && shown === "changes"
        ? html`<div class="drawer-panel" role="tabpanel" id="drawer-panel-changes" aria-labelledby="drawer-tab-changes">
            <${Changes} key=${node.pane} cwd=${pane.cwd} git=${[pane.dirty, pane.untracked, pane.ahead, pane.behind].join("/")} snap=${state} store=${store} />
          </div>`
        : null}
    </section>
    <//>
  `;
}

/** Drawer draws nothing for a node that is not there: the graph can drop a pane
 *  a render before the state that closes its drawer is applied. */
export function Drawer(props) {
  return props.node ? html`<${DrawerBody} ...${props} />` : null;
}
