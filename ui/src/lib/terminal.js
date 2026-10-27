// terminal.js shows one pane's terminal: xterm from the shared vendor tree,
// fed by pty.js. It keeps what the old window's terminal does: the terminal is
// the size of the pty and not of its box, a replay is drawn without the
// terminal answering the questions in it, a paste is stripped of control
// characters, Ctrl+C copies when something is selected, Shift+Enter is a
// newline that does not submit, and the sealed /ws/pty path is used when the
// hello says the window is reached through the relay and carries the
// desktop's key.
//
// The size rule, which the drawer depends on: the terminal is the size of the
// pty, and the server tells this window that size (the socket asks for size=1).
// A window on the desk says what its box measures, when the socket opens and
// whenever the box changes (the drawer opening, maximising, a window resize, the
// tab being shown); that is a measurement, not a demand: the server sizes the
// pane for the window last used, or, while none has been, for the least of the
// windows' boxes, so a pane another window is using is not taken from it. A box
// of another size than the pty shows the pane cropped or with room over, and
// says so (sizeNote in the state); takeOver() is the button that makes this
// window the one the pane is sized for. A device reached through the relay says
// nothing until asked (a glance from a phone must not shrink the desk's terminal),
// and neither does a viewer.
//
// A pane that is structured has no terminal: the terminal shows a one-line
// stub and nothing is opened. A viewer that may not watch panes does not open
// the socket either, because the server would refuse it with a 403 that a page
// sees only as a socket that failed, and the retry would never stop.
//
// xterm and e2e.js are classic scripts that put Terminal, FitAddon and
// FlockdeckE2E on the global object. They are not in the page: the first
// terminal loads them (load-script.js), shows "loading terminal" meanwhile, and
// shows an error with Try again if they cannot be had. They can also be passed
// in, so a test can supply its own.

import { html } from "../html.js";
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { createPty, ptyUrl } from "./pty.js";
import { DEFAULT_FONT } from "./prefs.js";
import { SEARCH_SCRIPT, loadScript, loadTerminalLibs } from "./load-script.js";

/** STRUCTURED_STUB is what a structured pane's terminal says if one is ever drawn for it;
 *  its conversation is in the drawer. */
/** NO_KEY_REASON is said of a terminal refused under the desktop's key when
 *  the desktop then stops offering one. */
export const NO_KEY_REASON = "The desktop no longer offers an encrypted terminal.";

export const STRUCTURED_STUB = "Structured pane: its conversation is in the drawer";

/** The smallest and largest sizes taken from the server for a terminal: a
 *  terminal of thousands of columns costs every window showing it. */
const MIN_COLS = 20;
const MIN_ROWS = 5;
const MAX_COLS = 500;
const MAX_ROWS = 200;

/** isTerminalReply says what the terminal sends is it answering its program
 *  and not somebody typing: an OSC, DCS, APC or PM string, a CSI ending in R,
 *  c, n or t, a mode state ending in $y, and the focus reports ESC [ I and
 *  ESC [ O. The server has the same rule. */
export function isTerminalReply(s) {
  if (s.length < 3 || s[0] !== "\x1b") return false;
  if ("]P_^".includes(s[1])) return true;
  if (s[1] !== "[") return false;
  const last = s[s.length - 1];
  if ("Rcnt".includes(last)) return true;
  if (last === "I" || last === "O") return s.length === 3;
  return last === "y" && s.includes("$");
}

/** The control characters a paste is stripped of: C0 except tab, line feed and
 *  carriage return, DEL, and the C1 range. Not global, so test() keeps no
 *  position. */
export const pasteControls = /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/;
export function stripPasteControls(text) {
  return String(text).replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/g, "");
}

/** terminalKey is a terminal's say over its own keys, and answers false for a
 *  key the terminal is to leave alone. Ctrl+C copies when something is
 *  selected and is ^C otherwise; Ctrl+Shift+C always copies; Ctrl+V is left
 *  to the browser, which pastes through xterm. Shift+Enter is sent through
 *  paste() so a program that asked for bracketed paste sees a newline that
 *  does not submit. A Mac copies and pastes with Cmd, and its Ctrl keys stay
 *  the program's. clipboard and notify are passed in. */
export function terminalKey(term, ev, env = {}) {
  if (ev.type === "keydown" && ev.key === "Enter" && ev.shiftKey && !ev.ctrlKey && !ev.altKey && !ev.metaKey) {
    ev.preventDefault();
    term.paste("\n");
    return false;
  }
  if (env.mac || ev.type !== "keydown" || !ev.ctrlKey || ev.altKey || ev.metaKey) return true;
  let k = (ev.key || "").toLowerCase();
  // A layout without Latin letters says which letter by the key's place.
  if (/^[^\x00-\x7f]$/.test(k)) {
    const m = /^Key([A-Z])$/.exec(ev.code || "");
    if (m) k = m[1].toLowerCase();
  }
  if (k === "v") return false;
  if (k !== "c" || (!ev.shiftKey && !term.hasSelection())) return true;
  ev.preventDefault();
  const text = term.getSelection();
  if (!text) return false;
  // The selection goes once it is copied, so the next Ctrl+C interrupts the
  // program rather than copying again.
  term.clearSelection();
  const failed = () => { if (env.notify) env.notify("The clipboard could not be reached, so nothing was copied", true); };
  try {
    const clip = env.clipboard;
    if (clip && clip.writeText) clip.writeText(text).catch(failed);
    else failed();
  } catch { failed(); }
  return false;
}

/** linkIn is the http or https address in text that covers column col, or "". */
export function linkIn(text, col) {
  const re = /https?:\/\/[^\s<>"'`]+/g;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    // A full stop or bracket that closes a sentence is not part of the address.
    const url = m[0].replace(/[.,;:!?)\]}]+$/, "");
    if (col >= m.index && col < m.index + url.length) return safeLink(url) ? url : "";
  }
  return "";
}

/** safeLink says a link a program printed may be opened: http and https only.
 *  The text of a link is whatever the program wrote, so a javascript: or file:
 *  address is not followed. */
export function safeLink(uri) {
  try {
    const u = new URL(String(uri));
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

/** createTerminal builds the controller of one pane's terminal.
 *
 *  Options:
 *    host           the element xterm is opened in
 *    paneId, store  the pane, and the store of store.js
 *    location       the page's location, for the socket's address
 *    termOptions    xterm options laid over the defaults (font size, scrollback)
 *    onChange(state) called with a new state object on every change
 *    onNotice(text, isError)  a short message for the person
 *    openExternal(uri)        what an OSC 8 link does
 *    Terminal, FitAddon, e2e, WebSocket, ResizeObserver, mac, clipboard,
 *    ptyOptions     globals and test seams (ptyOptions reaches createPty)
 *
 *  The state:
 *    phase     "loading terminal" while the libraries load, "load-failed" if they
 *              could not be had, "waiting for the page" before there is a hello
 *              and a state, then
 *              "structured", "blocked", or the socket's phase: "connecting",
 *              "open", "waiting" (between attempts), "refused", "idle"
 *    reason    why the socket was refused, or why it was not opened (blocked)
 *    ptySize   { cols, rows } of the pty, or null
 *    sizeNote  { cols, rows } when the box is smaller than the pty, else null
 *
 *  Methods: start(), dispose(), fitToBox(), takeOver(), tryAgain(), focus(),
 *  sendKeys(text, done). */
export function createTerminal(opts) {
  const G = globalThis;
  // Read again after the libraries have loaded: they are not in the page at
  // first, see load-script.js.
  let Terminal, FitAddon, e2e;
  const readLibs = () => {
    Terminal = opts.Terminal || G.Terminal;
    FitAddon = opts.FitAddon || (G.FitAddon && G.FitAddon.FitAddon);
    e2e = opts.e2e || G.FlockdeckE2E;
  };
  readLibs();
  const loadLibs = opts.loadLibs || loadTerminalLibs;
  let loading = false;
  let disposed = false;
  const store = opts.store;
  const host = opts.host;
  const paneId = opts.paneId;

  let state = { phase: "waiting for the page", reason: "", ptySize: null, sizeNote: null };
  let term = null;
  let fit = null;
  let pty = null;
  let unsub = null;
  let observer = null;
  let started = false;
  let structuredShown = false;
  // Listeners put on the host, kept so dispose can take them off.
  let hostListeners = [];
  let textareaFocus = null;
  let replaying = false;
  let replayGen = 0;
  let box = null; // the size the box would take, from the fit addon
  // Options set before the terminal was made (the libraries load late), laid over
  // the ones it was created with.
  let later = null;
  // The search addon, once it has loaded and been added to the terminal, and the
  // load in flight.
  let search = null;
  let searchLoad = null;
  const searchCtor = () => opts.SearchAddon || (G.SearchAddon && G.SearchAddon.SearchAddon);

  const set = (patch) => {
    state = { ...state, ...patch };
    if (opts.onChange) opts.onChange(state);
  };
  const notify = (text, isError) => { if (opts.onNotice) opts.onNotice(text, isError); };

  function open() {
    term = new Terminal({
      allowProposedApi: true,
      cursorBlink: true,
      fontFamily: DEFAULT_FONT,
      fontSize: 13,
      lineHeight: 1.15,
      scrollback: 10000,
      // A program can print a link whose text is not its address (OSC 8). The
      // default asks with the browser's confirm box and then navigates, which
      // in an app window is the application gone.
      linkHandler: { activate: (_ev, uri) => { if (safeLink(uri) && opts.openExternal) opts.openExternal(uri); } },
      theme: { background: "#0a0e11", foreground: "#d7dfe4", cursor: "#4fd1db", selectionBackground: "#1f4e53" },
      ...opts.termOptions,
      ...later,
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    term.attachCustomKeyEventHandler((ev) => terminalKey(term, ev, { mac: !!opts.mac, clipboard: opts.clipboard, notify }));
    // A paste goes to a program that asked for bracketed paste between two
    // markers. A copied ESC[201~ ended the paste early and what followed it
    // was typed at the program as keys. Text with a control character is
    // pasted without them; text with none is left to xterm. Caught on the way
    // down to xterm's own textarea.
    const onPaste = (ev) => {
      const text = ev.clipboardData && ev.clipboardData.getData("text/plain");
      if (!text || !pasteControls.test(text)) return;
      ev.preventDefault();
      ev.stopPropagation();
      const clean = stripPasteControls(text);
      if (clean && term) term.paste(clean);
    };
    host.addEventListener("paste", onPaste, true);
    hostListeners.push(["paste", onPaste, true]);
    // While the replay is drawn, what the terminal says of its own accord is
    // its answer to history, not to the program running now.
    term.onData((data) => {
      if (replaying && isTerminalReply(data)) return;
      if (pty) pty.sendInput(data);
    });
    term.onBinary((data) => {
      const bytes = new Uint8Array(data.length);
      for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 255;
      if (pty) pty.sendBytes(bytes);
    });
    // Focusing or tapping the terminal is using the pane in this window.
    const focus = () => { if (pty) pty.sendFocus(); };
    if (term.textarea) {
      textareaFocus = [term.textarea, focus];
      term.textarea.addEventListener("focus", focus);
    }
    host.addEventListener("pointerdown", focus);
    hostListeners.push(["pointerdown", focus, false]);
    // The box changing (the drawer opening, the window resized) measures the
    // box, updates the note and reports the measurement (see report).
    const RO = opts.ResizeObserver || G.ResizeObserver;
    if (RO) {
      observer = new RO(() => measure());
      observer.observe(host);
    }
  }

  /** addSearch adds the search addon to the terminal and reports whether it is
   *  there. Search is a convenience: an addon that will not add leaves the terminal
   *  as it was. */
  function addSearch() {
    if (search) return true;
    const Ctor = searchCtor();
    if (!term || !Ctor) return false;
    try {
      const addon = new Ctor();
      term.loadAddon(addon);
      if (addon.onDidChangeResults) {
        addon.onDidChangeResults((r) => {
          if (opts.onMatches) opts.onMatches(r ? { index: r.resultIndex, count: r.resultCount } : null);
        });
      }
      search = addon;
    } catch {
      search = null;
    }
    return !!search;
  }

  /** measure reads the size the box would take and brings the terminal to the
   *  pty's size once what has been received is drawn. */
  function measure() {
    if (!term || !fit) return;
    let d = null;
    try { d = fit.proposeDimensions(); } catch { d = null; }
    box = d && d.cols && d.rows ? { cols: d.cols, rows: d.rows } : null;
    updateNote();
    matchPtySize();
    scheduleReport();
  }

  // What this window last told the server its box measures on this socket.
  let reported = null;
  let reportTimer = null;
  const REPORT_MS = 120;

  /** reports says this window may tell the server its box: it is on the desk, not reached through the relay, and not a viewer. */
  function mayReport() {
    const h = store.getState().hello;
    return !!h && !h.remote && h.role !== "viewer";
  }

  function scheduleReport() {
    if (reportTimer !== null || !pty) return;
    // The drawer settling, a window dragged: one measurement for the burst.
    reportTimer = setTimeout(() => { reportTimer = null; report(); }, REPORT_MS);
  }

  /** report sends the box's size when it is one a terminal can be and is not the last one sent. */
  function report() {
    if (!term || !pty || !box || !mayReport()) return;
    if (box.cols < MIN_COLS || box.rows < MIN_ROWS) return;
    if (reported && reported.cols === box.cols && reported.rows === box.rows) return;
    if (pty.sendResize(box.cols, box.rows)) reported = { cols: box.cols, rows: box.rows };
  }

  function updateNote() {
    const want = state.ptySize;
    // A box too small to be a terminal is not one the server would size the
    // pane for, so there is nothing to offer.
    const cropped = !!want && !!box && box.cols >= MIN_COLS && box.rows >= MIN_ROWS &&
      (want.cols > box.cols || want.rows > box.rows);
    const next = cropped ? { cols: want.cols, rows: want.rows } : null;
    const prev = state.sizeNote;
    if ((next === null) !== (prev === null) || (next && prev && (next.cols !== prev.cols || next.rows !== prev.rows))) {
      set({ sizeNote: next });
    }
  }

  function matchPtySize() {
    const want = state.ptySize;
    if (!want || !term) return;
    term.write("", () => {
      if (term.cols !== want.cols || term.rows !== want.rows) term.resize(want.cols, want.rows);
    });
  }

  function setPtySize(cols, rows) {
    if (!(cols >= MIN_COLS && rows >= MIN_ROWS)) return;
    cols = Math.min(cols, MAX_COLS);
    rows = Math.min(rows, MAX_ROWS);
    set({ ptySize: { cols, rows } });
    updateNote();
    // Each size is drawn at its place in the stream, whatever else is still
    // waiting to be parsed.
    term.write("", () => {
      if (term.cols !== cols || term.rows !== rows) term.resize(cols, rows);
    });
  }

  /** plan says what this pane's terminal should be, from the store's hello and
   *  state as they are now, or null while either is missing (the control
   *  socket is reconnecting, or the state has not come yet). It is read again
   *  on every change, so a role or a key that changes is acted on. */
  function plan() {
    const s = store.getState();
    if (!s.hello || !s.state || !s.state.panes) return null;
    const pane = s.state.panes[paneId];
    if (!pane) return null; // not (yet) a pane of this project
    if (pane.structured) return { kind: "structured" };
    const hello = s.hello;
    if (hello.role === "viewer" && !hello.watchPanes) {
      return { kind: "blocked", reason: "This device may not watch panes." };
    }
    if (keyOf(hello) && !e2e) return { kind: "blocked", reason: "The end-to-end layer is not loaded." };
    return { kind: "connect" };
  }

  const keyOf = (hello) => (hello && hello.remote && hello.e2ePublicKey ? hello.e2ePublicKey : null);

  // The key the socket being made should use. Read when a socket opens, and
  // kept while the control socket has no hello, so a drop of that socket
  // does not turn a sealed terminal plain.
  let lastKey = null;
  // The key the latest dial used, so a refusal can be told from one the new key
  // would not have met.
  let dialKey = null;
  // The server's words for the last refusal, kept for when NO_KEY_REASON has
  // covered them and the key comes back.
  let refusedReason = "";
  const currentKey = () => {
    const h = store.getState().hello;
    if (h) lastKey = keyOf(h);
    dialKey = lastKey;
    return lastKey;
  };

  function stopPty() {
    if (pty) { pty.stop(); pty = null; }
  }

  /** reconcile brings the terminal in line with plan(). */
  function reconcile() {
    if (!term) return;
    const p = plan();
    if (!p) return;
    if (p.kind === "structured") {
      stopPty();
      if (!structuredShown) {
        structuredShown = true;
        term.write("[2m" + STRUCTURED_STUB + "[0m");
      }
      set({ phase: "structured", reason: "" });
      return;
    }
    if (p.kind === "blocked") {
      stopPty();
      set({ phase: "blocked", reason: p.reason });
      return;
    }
    if (pty) {
      // A socket let go quietly when the device was limited is dialled again
      // once the role says it may watch.
      if (pty.phase === "idle") pty.retryNow();
      // A refusal under one key is not final under another: the desktop's key
      // changes when it is reinstalled, and the hello that says so arrives after
      // the dial that was refused. Only a hello can get here, so a gap in the
      // control socket does not redial. A hello with no key is not a new key: a
      // socket made for it would be a plain one, and the refusal stands.
      else if (pty.phase === "refused") {
        const key = keyOf(store.getState().hello);
        if (key && key !== dialKey) pty.retryNow();
        else if (!key && dialKey) {
          if (state.reason !== NO_KEY_REASON) set({ reason: NO_KEY_REASON });
        } else if (key === dialKey && state.reason === NO_KEY_REASON) {
          // The key is back and the refusal under it stands, in the server's words.
          set({ reason: refusedReason });
        }
      }
      // The socket may have opened before the hello that says this window may report;
      // that size was dropped, and nothing else would send it.
      if (reported === null) scheduleReport();
      return;
    }
    pty = createPty({
      paneId,
      url: (held) => ptyUrl(opts.location || G.location, paneId, held),
      hostKey: currentKey,
      e2e,
      WebSocket: opts.WebSocket,
      // Read when it is asked, so a device the desk has limited since the
      // socket opened is let go quietly.
      viewOnly: () => {
        const h = store.getState().hello;
        return !!h && h.role === "viewer";
      },
      onSize: setPtySize,
      onRun({ reset, replayEnd }) {
        // Reset in the stream's own order, as RIS written to it: reset() acts at
        // once while write() only queues, so bytes of the old run still queued
        // would be drawn after it, on the fresh screen.
        if (reset) term.write("c");
        replayGen++;
        replaying = replayEnd > 0;
      },
      onBytes(bytes, endsReplay) {
        if (endsReplay) {
          // The last of the history. Once it is drawn, answers are the
          // program's again, unless another run has begun since.
          const gen = replayGen;
          term.write(bytes, () => { if (replayGen === gen) replaying = false; });
          return;
        }
        term.write(bytes);
      },
      onOpen() {
        // A new socket is a new window to the server: it has said nothing yet.
        reported = null;
        measure();
        if (focused() && pty) pty.sendFocus();
      },
      onState(st) {
        if (st.phase === "refused") refusedReason = st.reason;
        set({ phase: st.phase, reason: st.reason });
      },
      ...opts.ptyOptions,
    });
    pty.start();
  }

  function focused() {
    if (opts.isFocused) return opts.isFocused();
    const d = G.document;
    return !!d && typeof d.hasFocus === "function" && d.hasFocus() && host.contains(d.activeElement);
  }

  // go opens the terminal once the libraries are here.
  function go() {
    open();
    started = true;
    unsub = store.subscribe(reconcile);
    reconcile();
  }

  return {
    start() {
      if (started || loading || disposed) return;
      // The libraries are needed unless a caller handed in xterm and its addon
      // (a test), or all three are in the page already. xterm and the addon can
      // be there while e2e.js is still arriving for another terminal, so e2e.js
      // counts: a terminal started in that gap waits for the shared load too.
      const injected = !!(opts.Terminal && opts.FitAddon);
      if (!injected && !(Terminal && FitAddon && e2e)) {
        // The libraries are not in the page until the first terminal opens.
        // Say so while they load, and say so if they cannot be had, with a
        // button to try again. The socket is not opened until they have run,
        // so a sealed terminal never starts without the end-to-end layer.
        loading = true;
        set({ phase: "loading terminal", reason: "" });
        let asked;
        try { asked = Promise.resolve(loadLibs()); } catch (err) { asked = Promise.reject(err); }
        asked.then(
          () => {
            loading = false;
            if (disposed) return;
            readLibs();
            if (!Terminal || !FitAddon) {
              set({ phase: "load-failed", reason: "The terminal library did not load. Try again." });
              return;
            }
            go();
          },
          (err) => {
            loading = false;
            if (disposed) return;
            console.error("terminal: the libraries did not load", err);
            set({ phase: "load-failed", reason: "The terminal could not be loaded. Try again." });
          },
        );
        return;
      }
      go();
    },
    dispose() {
      disposed = true;
      started = false;
      if (unsub) { unsub(); unsub = null; }
      if (observer) { observer.disconnect(); observer = null; }
      for (const [type, fn, capture] of hostListeners) host.removeEventListener(type, fn, capture);
      hostListeners = [];
      if (textareaFocus) { textareaFocus[0].removeEventListener("focus", textareaFocus[1]); textareaFocus = null; }
      stopPty();
      search = null;
      searchLoad = null;
      if (reportTimer !== null) { clearTimeout(reportTimer); reportTimer = null; }
      if (term) { term.dispose(); term = null; }
    },
    /** fitToBox makes the pty the size of this terminal's box and tells the
     *  server, whatever the relay or a window used last. It is for the person
     *  dragging a divider or maximising. */
    fitToBox() {
      if (!term || !fit || !pty) return false;
      measure();
      if (!box || box.cols < MIN_COLS || box.rows < MIN_ROWS) return false;
      const sent = pty.sendResize(box.cols, box.rows);
      if (sent) reported = { cols: box.cols, rows: box.rows };
      return sent;
    },
    /** takeOver is the size note's button: size the pane for this window. */
    takeOver() {
      if (!this.fitToBox()) return false;
      pty.sendFocus();
      term.focus();
      return true;
    },
    tryAgain() {
      if (state.phase === "load-failed") { this.start(); return; }
      // A desktop that stopped offering its key is not tried again in plain: the
      // only way back is a hello that carries a key, which redials by itself.
      if (state.reason === NO_KEY_REASON) return;
      if (pty) pty.retryNow();
    },
    focus() { if (term) term.focus(); },
    /** selection is the text selected in the terminal, or "". */
    selection() { return term ? term.getSelection() : ""; },
    selectAll() { if (term) term.selectAll(); },
    /** clear empties the scrollback and the screen above the cursor. It is
     *  this window's view: the program is not told. */
    clear() { if (term) term.clear(); },
    /** pasteText pastes text as a paste does, without control characters. */
    pasteText(text) {
      const clean = stripPasteControls(String(text || ""));
      if (term && clean) term.paste(clean);
    },
    /** mouseTracking says the running program asked for the mouse, so xterm
     *  sends it a right-click and the page's own menu would only get in the way. */
    mouseTracking() { return !!term && !!term.modes && term.modes.mouseTrackingMode !== "none"; },
    /** linkAt is the http or https address printed under a point of the window,
     *  or "". It reads the text of the screen row: a link wrapped over two rows
     *  is read from the row the point is on. */
    linkAt(x, y) {
      const screen = term && term.element && term.element.querySelector ? term.element.querySelector(".xterm-screen") : null;
      if (!screen || !term.buffer) return "";
      const r = screen.getBoundingClientRect();
      if (!r.width || !r.height || x < r.left || y < r.top || x >= r.right || y >= r.bottom) return "";
      const col = Math.floor(((x - r.left) / r.width) * term.cols);
      const row = Math.floor(((y - r.top) / r.height) * term.rows);
      const buf = term.buffer.active;
      const line = buf.getLine(buf.viewportY + row);
      if (!line) return "";
      return linkIn(line.translateToString(true), col);
    },
    /** searchReady loads the search addon the first time, and resolves with
     *  whether Find in terminal can work. After a failure the next call tries again. */
    searchReady() {
      if (search) return Promise.resolve(true);
      if (!term) return Promise.resolve(false);
      if (!searchLoad) {
        const load = (async () => {
          try {
            if (!searchCtor()) await (opts.loadSearch || (() => loadScript(SEARCH_SCRIPT)))();
          } catch {
            return false;
          }
          return !disposed && addSearch();
        })();
        searchLoad = load;
        load.then((ok) => { if (!ok && searchLoad === load) searchLoad = null; });
      }
      return searchLoad;
    },
    /** find finds the next match of text, or the previous one with back. typing
     *  says it is a search made as the words are typed, which keeps the match
     *  already found while it still matches. It returns whether there is a match,
     *  or null when nothing was searched (no addon yet, or no text). */
    find(text, { back = false, typing = false } = {}) {
      if (!search || !term) return null;
      if (!text) {
        this.clearFind();
        return null;
      }
      const o = { incremental: !!typing, decorations: { activeMatchColorOverrideColor: "#4fd1db", matchOverviewRuler: "#4fd1db" } };
      try {
        return !!(back ? search.findPrevious(text, o) : search.findNext(text, o));
      } catch {
        return null;
      }
    },
    /** clearFind takes the marks of a search off the terminal. */
    clearFind() {
      if (search) { try { search.clearDecorations(); } catch { /* the addon is optional */ } }
      if (opts.onMatches) opts.onMatches(null);
    },
    /** setOptions changes xterm options on the open terminal (a font, the
     *  cursor, the scrollback), or on the one about to open. The box is measured
     *  again, since a font changes the size of a cell. It never sends a resize. */
    setOptions(o) {
      if (!o) return;
      if (!term) { later = { ...later, ...o }; return; }
      let changed = false;
      for (const [k, v] of Object.entries(o)) {
        if (term.options[k] !== v) {
          term.options[k] = v;
          changed = true;
        }
      }
      if (changed) measure();
    },
    /** sendKeys types text into the pane over the terminal socket, as keys the
     *  person pressed: the drawer's permission buttons send Enter and Esc this
     *  way, sealed when the desktop has a key. It returns false, and sends
     *  nothing, while the socket is not open. done, if given, is told later
     *  whether the keys reached the socket (true) or were lost (false). */
    sendKeys(text, done) { return !!pty && state.phase === "open" && pty.sendInput(String(text), done); },
    get state() { return state; },
  };
}

// The controller of each terminal, by the element xterm is opened in, so a
// right-click menu can reach the one it was opened on.
const controllers = new WeakMap();

/** terminalOf is the controller of the terminal drawn inside el, or null. */
export function terminalOf(el) {
  const host = el && el.querySelector ? el.querySelector(".terminal-host") : null;
  return (host && controllers.get(host)) || null;
}

/** TerminalView is the component. Props: store, paneId, and optionally
 *  onReady(controller), which is how a drawer reaches fitToBox for its
 *  maximise toggle, plus anything createTerminal takes. Text from the server
 *  (a refusal's reason) is drawn as text. */
export function TerminalView(props) {
  const { store, paneId, onReady, ...rest } = props;
  const hostRef = useRef(null);
  const ctlRef = useRef(null);
  const [state, setState] = useState({ phase: "waiting for the page", reason: "", ptySize: null, sizeNote: null });

  useEffect(() => {
    const ctl = createTerminal({ ...rest, store, paneId, host: hostRef.current, onChange: setState });
    ctlRef.current = ctl;
    if (hostRef.current) controllers.set(hostRef.current, ctl);
    ctl.start();
    if (onReady) onReady(ctl);
    return () => {
      ctl.dispose();
      if (hostRef.current) controllers.delete(hostRef.current);
      ctlRef.current = null;
      if (onReady) onReady(null);
    };
    // A different pane is a different terminal.
  }, [store, paneId]);

  return html`<${TerminalChrome} state=${state} hostRef=${hostRef}
    onTryAgain=${() => ctlRef.current && ctlRef.current.tryAgain()}
    onTakeOver=${() => ctlRef.current && ctlRef.current.takeOver()} />`;
}

/** TerminalChrome is what surrounds the terminal: the element xterm opens in,
 *  the cover that says why the socket was refused or not opened, and the note
 *  that offers to size the pane for this window. */
export function TerminalChrome({ state, hostRef, onTryAgain, onTakeOver }) {
  const note = state.sizeNote;
  return html`<div class="terminal-pane" data-phase=${state.phase} data-ctx="term">
    <div class="terminal-host" ref=${hostRef}></div>
    ${state.phase === "loading terminal" ? html`<div class="pane-loading" role="status">Loading terminal…</div>` : null}
    ${state.phase === "refused" || state.phase === "blocked" || state.phase === "load-failed"
      ? html`<div class="pane-error" role="alert">
          <div class="pane-error-text error">${state.reason}</div>
          ${(state.phase === "refused" && state.reason !== NO_KEY_REASON) || state.phase === "load-failed"
            ? html`<button type="button" class="chip primary" onClick=${onTryAgain}>Try again</button>`
            : null}
        </div>`
      : null}
    ${note
      ? html`<button type="button" class="size-note" onClick=${onTakeOver}>Viewing ${note.cols}×${note.rows} · fit to this window</button>`
      : null}
  </div>`;
}
