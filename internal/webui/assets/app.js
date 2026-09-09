/* perch front end.
 *
 * The Go process owns the panes, their processes and the layout tree; this
 * file renders that state and forwards input. Terminal emulation happens here
 * in xterm.js, so keystrokes are encoded for whatever modes the running
 * application has enabled and raw bytes pass through in both directions.
 */
(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  };

  // ------------------------------------------------------------------ tips

  /* Native title tooltips are wrong here twice over: they wait about a second,
   * by which time the pointer has usually moved on, and they draw in OS chrome
   * that has nothing to do with the palette around them. One delegated
   * listener replaces the mechanism without touching the call sites - anything
   * carrying data-tip gets our own bubble, and a title still sitting on an
   * element is moved across to data-tip the first time it is hovered, so every
   * existing `title=` keeps working and the browser's own tooltip never gets
   * the chance to fire.
   */

  /** TIPS is the copy for the glyphs that recur across the pane header, the
   *  projects list and the worktrees, agents and changes overlays. A symbol has
   *  to mean one thing everywhere it appears, so the wording lives here rather
   *  than being retyped, and drifting, at each call site. */
  const TIPS = {
    waiting:     "Waiting on you - the agent asked a question, or is holding for a permission.",
    working:     "Working - the agent is producing output and does not need you yet.",
    idle:        "Idle - the agent finished its turn and is waiting for a new prompt.",
    starting:    "Starting - the process is launching and has not reported in yet.",
    exited:      "Exited - the process in this pane has stopped.",
    ahead:       "Commits made here that the upstream branch does not have. Push to send them.",
    behind:      "Commits on the upstream branch that this checkout does not have. Pull to catch up.",
    // The dirty count: tracked files the agent has edited but not committed.
    changed:     "Tracked files with edits that have not been committed yet.",
    untracked:   "New files git is not tracking yet - they are in no commit, and a push leaves them behind.",
    clean:       "Nothing to commit - this working tree matches its last commit.",
    branch:      "The branch this checkout has in its working tree.",
    project:     "The project this agent is working in. It is shown because that is not the project of the tab it is sitting on — this tab holds agents from more than one.",
    splitHere:   "Splits the focused pane and starts an agent in this project, so both projects are worked on side by side in one tab.",
    broadcast:   "Mirrors what you type into every pane in the broadcast set, so one instruction reaches them all.",
    fanOut:      "Turns the plan this agent proposed into a set of agents that carry it out, one pane each.",
    restart:     "Relaunches the process in this pane. A Claude agent resumes the same conversation.",
    zoom:        "Fills the tab with this pane. Zoom again to bring the other panes back.",
    close:       "Closes this pane and stops the process running in it.",
    repoFolder:  "A folder with a git repository in it - opening it makes it a project.",
    plainFolder: "A folder with no git repository in it - open it to look further in.",
  };

  const TIP_DELAY = 250;
  /** The one bubble, made on first use and reused. */
  let tipEl = null;
  /** The element the bubble is currently describing, if it is up. */
  let tipFor = null;
  let tipTimer = 0;

  /** describe attaches tooltip copy to a node, and gives a glyph-only button
   *  the accessible name its label cannot carry. */
  function describe(node, text) {
    node.dataset.tip = text;
    // A button reading "⑂" tells a screen reader nothing, while one
    // reading "Broadcast" already names itself and would be read out twice.
    if (node.tagName === "BUTTON" && !/[\p{L}\p{N}]/u.test(node.textContent || "")) {
      node.setAttribute("aria-label", text);
    }
    return node;
  }

  /** tipFind returns the nearest element with tooltip copy, adopting the title
   *  of every element it passes on the way up. */
  function tipFind(node) {
    let found = null;
    for (let n = node instanceof Element ? node : null; n; n = n.parentElement) {
      // The browser shows an ancestor's title when a child is hovered, so the
      // whole chain has to be emptied rather than just the element under the
      // pointer. It is the attribute's presence that matters, not its value:
      // code that clears a tooltip assigns title = "", and that has to clear
      // ours too instead of leaving the old text behind.
      if (n.hasAttribute("title")) {
        const t = n.getAttribute("title");
        if (t) n.dataset.tip = t;
        else delete n.dataset.tip;
        n.removeAttribute("title");
      }
      if (!found && n.dataset.tip) found = n;
    }
    return found;
  }

  function hideTip() {
    clearTimeout(tipTimer);
    tipFor = null;
    if (tipEl) tipEl.remove();
  }

  function showTip(node) {
    const text = node.dataset.tip;
    if (!text || !node.isConnected) return;
    if (!tipEl) {
      tipEl = el("div", "tip");
      tipEl.setAttribute("role", "tooltip");
    }
    tipEl.textContent = text;
    // Hung off the body and positioned against the viewport, so none of the
    // containers a tip is born in - the tab strip, a pane header, the overlay
    // body - can clip it with their overflow. Measured hidden, because the
    // wrapped height decides whether there is room below.
    tipEl.style.visibility = "hidden";
    tipEl.style.left = tipEl.style.top = "0px";
    document.body.append(tipEl);

    const gap = 8, edge = 6;
    const r = node.getBoundingClientRect();
    const w = tipEl.offsetWidth, h = tipEl.offsetHeight;
    let top = r.bottom + gap;
    if (top + h > innerHeight - edge) top = r.top - gap - h; // no room below
    tipEl.style.top = Math.max(edge, top) + "px";
    tipEl.style.left = Math.max(edge, Math.min(r.left + (r.width - w) / 2, innerWidth - w - edge)) + "px";
    tipEl.style.visibility = "";
    tipFor = node;
  }

  document.addEventListener("pointerover", (e) => {
    const node = tipFind(e.target);
    if (node && node === tipFor) return; // already up for this element
    hideTip();
    // Touch has no hover to read the delay from, and a long press there is the
    // terminal's selection gesture.
    if (node && e.pointerType !== "touch") tipTimer = setTimeout(() => showTip(node), TIP_DELAY);
  }, true);

  // Slow in, instant out - and out for anything that means the question has
  // stopped being asked: the pointer leaving the window, a click, a scroll
  // moving the target out from under the bubble, Escape.
  document.addEventListener("pointerout", (e) => { if (!e.relatedTarget) hideTip(); }, true);
  document.addEventListener("pointerdown", hideTip, true);
  document.addEventListener("scroll", hideTip, true);
  window.addEventListener("keydown", (e) => { if (e.key === "Escape") hideTip(); }, true);
  window.addEventListener("blur", hideTip);

  const wsBase = (location.protocol === "https:" ? "wss://" : "ws://") + location.host;

  /** Live pane records, keyed by pane id. */
  const panes = new Map();
  /** The page each tab is drawn on, and the shape it was drawn with, both by
   *  tab id. Keeping them per tab is what lets one tab change without the
   *  others being taken apart. */
  const tabPages = new Map();
  const tabShapes = new Map();
  /** The "no tabs open" placeholder, which is not a tab page. */
  let emptyPage = null;
  /** Which tab is currently on screen, so a switch can be told from a redraw. */
  let shownTab = "";
  /** Which pane the keyboard was last handed to, so a new pane, a closed one
   *  or a focus moved from the keyboard can be told from an ordinary status
   *  push that leaves the focus exactly where it was. */
  let shownFocus = "";
  let state = null;
  let control = null;
  let reconnectTimer = null;
  /** Which dialog the overlay is currently showing, if any. */
  let dialog = null;
  let recents = [];
  let browseState = null;
  /** The actions this application offers, sent by the Go side on connect.
   *  The palette, the keyboard and the help pages are all drawn from it, so a
   *  binding cannot be changed in one of them and left stale in the others. */
  let keyTable = [];
  /** Normalised binding → action id, built from the same table. */
  let bindings = new Map();
  /** What this person has already been shown. Kept by the Go side, because a
   *  fresh port each run means the browser treats every run as a new origin
   *  and forgets anything stored here. */
  let prefs = { helpSeen: false, dismissedTips: [] };
  let fontSize = (() => {
    try { return Number(localStorage.getItem("fontSize")) || 13; } catch { return 13; }
  })();

  // ---------------------------------------------------------------- control

  function connectControl() {
    clearTimeout(reconnectTimer);
    control = new WebSocket(wsBase + "/ws/control");

    control.onopen = () => {
      $("disconnected").hidden = true;
    };
    control.onmessage = (ev) => {
      let msg;
      try { msg = JSON.parse(ev.data); } catch { return; }
      if (msg.type === "state") applyState(msg);
      else if (msg.type === "hello") applyHello(msg);
      else if (msg.type === "prefs") { prefs = msg.prefs || prefs; renderHints(); }
      else if (msg.type === "worktrees") keepFocus(() => renderWorktrees(msg));
      else if (msg.type === "recents") { recents = msg.items || []; if (dialog === "projects") keepFocus(renderProjects); }
      else if (msg.type === "browse") { browseState = msg; if (dialog === "projects") keepFocus(renderProjects); }
      else if (msg.type === "conversations") keepFocus(() => renderHistory(msg));
      else if (msg.type === "changes") keepFocus(() => renderChanges(msg));
      else if (msg.type === "agents") keepFocus(() => renderAgents(msg));
      else if (msg.type === "fanoutPreview") renderFanout(msg);
      else if (msg.type === "diff") showDiff(msg);
      else if (msg.type === "detached") {
        // The agents keep running; this window is no longer needed.
        detaching = true;
        window.close();
      }
      else if (msg.type === "notice") notice(msg.text, msg.error);
    };
    control.onclose = () => {
      if (detaching) return; // the window is on its way out
      $("disconnected").hidden = false;
      // Nothing behind this can be used and the terminal it is covering has
      // the keyboard, so typing would go nowhere until the pointer was used.
      $("retry").focus();
      reconnectTimer = setTimeout(connectControl, 1500);
    };
    control.onerror = () => control.close();
  }

  function send(cmd) {
    if (control && control.readyState === WebSocket.OPEN) {
      control.send(JSON.stringify(cmd));
    }
  }

  // ------------------------------------------------------------------ state

  function applyState(s) {
    state = s;
    const rebuilt = rebuildChangedTabs(s);
    applyWeights(s);
    showActiveTab(s, rebuilt);
    renderTabs(s);
    renderSummary(s);
    updatePaneChrome(s);
    prunePanes(s);
    notifyAttention(s);
    renderHints();
  }

  /** Structure ignores weights: a drag must not trigger a rebuild. */
  function structureOf(node) {
    if (!node) return null;
    if (node.pane) return node.pane;
    return { d: node.dir, c: (node.children || []).map(structureOf) };
  }

  function paneIdsIn(s) {
    const ids = new Set();
    for (const t of s.tabs) collectPanes(t.root, ids);
    return ids;
  }
  function collectPanes(node, out) {
    if (!node) return;
    if (node.pane) { out.add(node.pane); return; }
    (node.children || []).forEach((c) => collectPanes(c, out));
  }

  // ----------------------------------------------------------------- layout

  /** shapeOf is what a tab has to be redrawn for. Weights are left out so a
   *  drag does not rebuild anything; zoom is in, because it changes which
   *  panes are on screen, and while zoomed so is the focused pane. */
  function shapeOf(tab) {
    return JSON.stringify({
      tree: structureOf(tab.root),
      zoom: tab.zoom,
      focus: tab.zoom ? tab.focus : "",
    });
  }

  /** rebuildChangedTabs redraws the tabs whose shape has changed, leaves the
   *  rest standing, and reports whether the tab on screen was one of them.
   *
   *  The panes survive a rebuild — they are kept in the registry and put back —
   *  but being taken out of the document and returned is not nothing. A
   *  terminal loses the selection in it, which is how output is copied out of
   *  one, and anything part-typed through an input method. Rebuilding every tab
   *  because one of them changed meant that fanning a plan out, which changes
   *  another tab's shape once per agent it starts, did that to the pane you
   *  were reading, several times in a row. */
  function rebuildChangedTabs(s) {
    const host = $("workspace");

    if (!s.tabs.length) {
      host.textContent = "";
      tabPages.clear();
      tabShapes.clear();
      splitNodes.clear();
      const empty = el("div", "empty");
      empty.append(el("p", null, "No tabs open."));
      const b = el("button", "chip primary", "New agent tab");
      b.onclick = () => runAction("newAgentTab");
      const h = el("button", "chip", "Help");
      h.onclick = () => openHelp("getting-started");
      empty.append(b, h);
      host.append(empty);
      emptyPage = empty;
      return true;
    }
    // The "no tabs" placeholder is not a tab page, so it goes by hand.
    if (emptyPage) { emptyPage.remove(); emptyPage = null; }

    let activeRebuilt = false;
    s.tabs.forEach((tab, i) => {
      const shape = shapeOf(tab);
      let page = tabPages.get(tab.id);
      if (!page || tabShapes.get(tab.id) !== shape) {
        if (page) page.remove();
        page = el("div", "tab-page");
        if (tab.zoom && tab.focus) {
          // A zoomed pane takes the whole tab. The others stay in the pane
          // registry with their terminals and connections intact, simply
          // detached from the document until the zoom is released.
          page.append(ensurePane(tab.focus).wrap);
        } else {
          page.append(buildNode(tab.root, tab));
        }
        tabPages.set(tab.id, page);
        tabShapes.set(tab.id, shape);
        if (tab.id === s.activeTab) activeRebuilt = true;
      }
      if (host.childNodes[i] !== page) host.insertBefore(page, host.childNodes[i] || null);
    });

    for (const [id, page] of tabPages) {
      if (s.tabs.some((t) => t.id === id)) continue;
      page.remove();
      tabPages.delete(id);
      tabShapes.delete(id);
    }
    // Splits that went with a page that has been replaced.
    for (const [id, node] of splitNodes) if (!node.isConnected) splitNodes.delete(id);
    return activeRebuilt;
  }

  function buildNode(node, tab) {
    if (!node) return el("div");
    if (node.pane) {
      const p = ensurePane(node.pane);
      p.nodeId = node.id;
      return p.wrap;
    }

    const split = el("div", "split " + (node.dir === "h" ? "h" : "v"));
    split.dataset.node = node.id;
    splitNodes.set(node.id, split);
    const kids = node.children || [];
    kids.forEach((child, i) => {
      if (i > 0) split.append(makeDivider(split, node));
      split.append(buildNode(child, tab));
    });
    return split;
  }

  /** The divider being dragged, if one is. A state push carries the weights the
   *  Go side has stored, which while a drag is in progress are the ones from
   *  before it started: applying them snapped the panes back to where the drag
   *  began until the pointer moved again, and pushes arrive continuously while
   *  agents are working, so the whole drag fought itself. The structural
   *  signature already leaves weights out for the same reason. */
  let resizing = null;

  /** applyWeights sets flex-grow from the tree without rebuilding the DOM. */
  function applyWeights(s) {
    // A divider that is no longer in the document belongs to a layout that has
    // been rebuilt under it, so its drag is over whether or not it said so.
    if (resizing && resizing.isConnected) return;
    resizing = null;
    for (const tab of s.tabs) walkWeights(tab.root);
  }
  function walkWeights(node) {
    if (!node) return;
    const target = node.pane ? (panes.get(node.pane) || {}).wrap : findSplit(node.id);
    if (target) {
      target.style.flexGrow = String(node.weight > 0 ? node.weight : 1);
      target.style.flexBasis = "0";
    }
    (node.children || []).forEach(walkWeights);
  }
  /** The split containers by node id, recorded as they are built. Weights are
   *  applied on every state push, and every push arrives while agents are
   *  working, so looking each one up used to mean a search of the whole
   *  document — six panes deep in xterm's own elements — once per split. */
  const splitNodes = new Map();

  function findSplit(id) {
    const split = splitNodes.get(id);
    return split && split.isConnected ? split : null;
  }

  /** panelsAround returns the two panels a divider sits between, and every
   *  panel in the split — the weights are saved as a set. */
  function panelsAround(split, d) {
    const kids = [...split.children];
    const panels = kids.filter((c) => !c.classList.contains("divider"));
    // The divider follows the panel it belongs to, so the count of panels
    // before it, less one, is that panel's place in the list.
    const at = kids.slice(0, kids.indexOf(d)).filter((c) => !c.classList.contains("divider")).length - 1;
    const a = panels[at], b = panels[at + 1];
    return a && b ? { panels, a, b } : null;
  }

  /** saveWeights tells the Go side how the split was left, so it comes back
   *  the same way, and refits the terminals to their new size. */
  function saveWeights(node, panels) {
    send({ cmd: "setWeights", node: node.id, weights: panels.map((p) => parseFloat(p.style.flexGrow) || 1) });
    panels.forEach(refitWithin);
  }

  /** sayShare puts the split on the separator, which is the only way a screen
   *  reader can tell that an arrow key did anything. */
  function sayShare(d, a, b) {
    const aGrow = parseFloat(a.style.flexGrow) || 1;
    const bGrow = parseFloat(b.style.flexGrow) || 1;
    d.setAttribute("aria-valuenow", String(Math.round((aGrow / (aGrow + bGrow)) * 100)));
  }

  /** One press of an arrow key moves this much of the pair across. */
  const RESIZE_STEP = 0.04;

  /** makeDivider returns a separator that reweights its siblings, by dragging
   *  or from the keyboard. A row of six agents is unreadable until some of them
   *  are given more room than others, and until now that could only be done
   *  with a mouse. */
  function makeDivider(split, node) {
    const d = el("div", "divider");
    const acrossIsWidth = node.dir === "h";
    d.tabIndex = 0;
    d.setAttribute("role", "separator");
    // The bar itself lies across the split: a row of panes is divided by an
    // upright one, which is what a reader is told about.
    d.setAttribute("aria-orientation", acrossIsWidth ? "vertical" : "horizontal");
    d.setAttribute("aria-valuemin", "10");
    d.setAttribute("aria-valuemax", "90");
    const how = acrossIsWidth
      ? "Sets how the width is shared between the panes either side. Drag it, or use the left and right arrow keys; Home makes them equal."
      : "Sets how the height is shared between the panes either side. Drag it, or use the up and down arrow keys; Home makes them equal.";
    d.setAttribute("aria-label", how);
    describe(d, how);

    d.addEventListener("keydown", (ev) => {
      const less = ev.key === (acrossIsWidth ? "ArrowLeft" : "ArrowUp");
      const more = ev.key === (acrossIsWidth ? "ArrowRight" : "ArrowDown");
      if (!less && !more && ev.key !== "Home") return;
      const pair = panelsAround(split, d);
      if (!pair) return;
      ev.preventDefault();
      // The window handler dispatches bindings from the action table; a bare
      // arrow is not one, but stopping here says so rather than relying on it.
      ev.stopPropagation();
      const aGrow = parseFloat(pair.a.style.flexGrow) || 1;
      const bGrow = parseFloat(pair.b.style.flexGrow) || 1;
      const total = aGrow + bGrow;
      const share = ev.key === "Home"
        ? 0.5
        : Math.max(0.1, Math.min(0.9, aGrow / total + (more ? RESIZE_STEP : -RESIZE_STEP)));
      pair.a.style.flexGrow = String(total * share);
      pair.b.style.flexGrow = String(total * (1 - share));
      sayShare(d, pair.a, pair.b);
      saveWeights(node, pair.panels);
    });

    d.addEventListener("pointerdown", (ev) => {
      // The secondary button opens a menu; it does not start a resize. Without
      // this, a right-click on a divider began one and the panes then followed
      // the pointer around until something released the primary button.
      if (ev.button !== 0) return;
      // Only the two panels either side of this divider are affected.
      const pair = panelsAround(split, d);
      if (!pair) return;
      ev.preventDefault();
      const { panels, a, b } = pair;
      const horizontal = acrossIsWidth;
      resizing = d;

      const startPos = horizontal ? ev.clientX : ev.clientY;
      const aSize = horizontal ? a.offsetWidth : a.offsetHeight;
      const bSize = horizontal ? b.offsetWidth : b.offsetHeight;
      const total = aSize + bSize;
      const aGrow = parseFloat(a.style.flexGrow) || 1;
      const bGrow = parseFloat(b.style.flexGrow) || 1;
      const totalGrow = aGrow + bGrow;

      d.classList.add("dragging");
      // Capturing sends every move and the release to the divider itself, so a
      // drag that leaves the window — which is easy, the panes reach the edge
      // of it — is still delivered here. Listening on the document instead
      // meant a button released outside never arrived, and the panes went on
      // following the pointer with nothing held down.
      try { d.setPointerCapture(ev.pointerId); } catch { /* older engines */ }
      const onMove = (m) => {
        if (m.pointerId !== ev.pointerId) return;
        const delta = (horizontal ? m.clientX : m.clientY) - startPos;
        const aPx = Math.max(60, Math.min(total - 60, aSize + delta));
        const ratio = aPx / total;
        a.style.flexGrow = String(totalGrow * ratio);
        b.style.flexGrow = String(totalGrow * (1 - ratio));
      };
      // pointercancel is the system taking the pointer away — a touch turning
      // into a scroll, the window losing it. The drag has to end there too, or
      // it never ends at all.
      const onUp = (u) => {
        if (u.pointerId !== ev.pointerId) return;
        d.removeEventListener("pointermove", onMove);
        d.removeEventListener("pointerup", onUp);
        d.removeEventListener("pointercancel", onUp);
        d.classList.remove("dragging");
        resizing = null;
        sayShare(d, a, b);
        saveWeights(node, panels);
      };
      d.addEventListener("pointermove", onMove);
      d.addEventListener("pointerup", onUp);
      d.addEventListener("pointercancel", onUp);
    });
    return d;
  }

  function refitWithin(node) {
    for (const p of panes.values()) {
      if (node.contains(p.wrap)) scheduleFit(p);
    }
  }

  function showActiveTab(s, rebuilt) {
    let idx = s.tabs.findIndex((t) => t.id === s.activeTab);
    if (idx < 0) idx = 0;
    const tab = s.tabs[idx];
    for (const [id, page] of tabPages) page.hidden = !tab || id !== tab.id;
    // A terminal cannot measure itself while hidden, so refit on reveal.
    if (!tab) { shownTab = ""; shownFocus = ""; return; }
    const ids = new Set();
    collectPanes(tab.root, ids);
    ids.forEach((id) => {
      const p = panes.get(id);
      if (p) scheduleFit(p);
    });
    // Switching tab means switching agent, so the keyboard has to come along:
    // the click that caused the switch left the focus on the tab button, and
    // the revealed terminal would ignore everything typed at it.
    //
    // Rebuilding this tab needs the same treatment for a different reason: it
    // takes the panes out of the document and puts them back, which blurs
    // whatever the keyboard was in. Splitting, closing, zooming and dragging
    // all rebuild, so without this a fresh pane would arrive with nowhere to
    // type and the pane you were in would go deaf. A rebuild of some other
    // tab leaves this one alone and is none of its business.
    const moved = tab.id !== shownTab || tab.focus !== shownFocus || rebuilt;
    shownTab = tab.id;
    shownFocus = tab.focus;
    if (moved && !dialogOpen()) focusTerminal(tab, ids);
  }

  // ------------------------------------------------------------------- tabs

  /** glyph returns a symbol that carries meaning to the eye but nothing to a
   *  screen reader, so it is hidden from one. The count or name beside it stays
   *  ordinary text, and the tooltip on the enclosing span supplies the sense of
   *  the symbol to both. */
  function glyph(sym) {
    const n = el("span", null, sym);
    n.setAttribute("aria-hidden", "true");
    return n;
  }

  /** The button for each tab, by tab id. A status push arrives every time any
   *  agent changes what it is doing, which with six of them running is most of
   *  the time; throwing the strip away and building it again on each one loses
   *  whatever the keyboard was on, resets how far the strip is scrolled, and
   *  pulls the element out from under a tooltip that was about to open. So the
   *  buttons outlive the pushes and only what changed is written. */
  const tabNodes = new Map();
  /** The tab the strip was last scrolled to. */
  let scrolledTab = "";

  function renderTabs(s) {
    // Rebuilding the strip would destroy the element a drag is holding, and
    // the drag would end nowhere. Status pushes arrive constantly, so this is
    // not a rare case; the bar catches up when the drag finishes.
    if (dragging && dragging.kind === "tab") return;
    const bar = $("tabs");
    s.tabs.forEach((tab, i) => {
      const node = tabNode(tab.id);
      const title = tab.title || "tab " + (i + 1);
      if (node.label.textContent !== title) node.label.textContent = title;
      const active = tab.id === s.activeTab;
      node.btn.classList.toggle("active", active);
      node.btn.setAttribute("aria-selected", String(active));
      node.btn.classList.toggle("attention", !!tab.attention);
      setAttention(node, !!tab.attention);
      // One stop on the way through the window rather than two per tab. With
      // a dozen agents open, tabbing past the strip to reach the terminal
      // behind it took twenty-four presses; the arrow keys walk it instead,
      // which is what a tab strip is expected to answer to anyway.
      const stop = active ? 0 : -1;
      if (node.btn.tabIndex !== stop) { node.btn.tabIndex = stop; node.close.tabIndex = stop; }
      if (bar.childNodes[i] !== node.btn) bar.insertBefore(node.btn, bar.childNodes[i] || null);
    });
    for (const [id, node] of tabNodes) {
      if (s.tabs.some((t) => t.id === id)) continue;
      node.btn.remove();
      tabNodes.delete(id);
    }
    // The strip is only as wide as the bar and scrolls when there are more
    // tabs than fit, so switching to one that is off the end has to bring it
    // into view — otherwise walking the tabs from the keyboard or the palette
    // moves to a tab that cannot be seen. Only when the tab actually changes,
    // so an ordinary status push never moves the strip under the pointer.
    if (s.activeTab !== scrolledTab) {
      scrolledTab = s.activeTab;
      const node = tabNodes.get(s.activeTab);
      if (node) node.btn.scrollIntoView({ block: "nearest", inline: "nearest" });
    }
  }

  /** tabStripKey walks the strip with the arrow keys. Moving the focus does not
   *  switch tab: switching means switching agent, and arrowing past four of
   *  them to reach the fifth should not visit all four on the way. */
  function tabStripKey(ev, id) {
    const tabs = state ? state.tabs : [];
    const at = tabs.findIndex((t) => t.id === id);
    if (at < 0 || !tabs.length) return;
    let to;
    if (ev.key === "ArrowRight") to = (at + 1) % tabs.length;
    else if (ev.key === "ArrowLeft") to = (at - 1 + tabs.length) % tabs.length;
    else if (ev.key === "Home") to = 0;
    else if (ev.key === "End") to = tabs.length - 1;
    else return;
    ev.preventDefault();
    ev.stopPropagation();
    const node = tabNodes.get(tabs[to].id);
    if (!node) return;
    node.btn.focus();
    node.btn.scrollIntoView({ block: "nearest", inline: "nearest" });
  }

  /** tabNode returns the button for a tab, making it the first time. Every
   *  handler is bound to the tab's id rather than to the record it arrived in,
   *  because the record is replaced by each push and the button is not. */
  function tabNode(id) {
    let node = tabNodes.get(id);
    if (node) return node;
    const btn = el("button", "tab");
    btn.setAttribute("role", "tab");
    const label = el("span", "label");
    const close = el("button", "close", "×");
    describe(close, "Close tab");
    close.onclick = (ev) => { ev.stopPropagation(); send({ cmd: "closeTab", id }); };
    btn.append(label, close);
    btn.onclick = () => {
      // Re-clicking the tab already on screen produces no state change for
      // showActiveTab to react to, so hand the keyboard back here.
      if (state && state.activeTab === id) focusTerminal();
      else send({ cmd: "selectTab", id });
    };
    btn.ondblclick = () => renameTab(id);
    btn.onkeydown = (ev) => tabStripKey(ev, id);
    makeTabDraggable(id, btn);
    node = { btn, label, close, attn: null };
    tabNodes.set(id, node);
    return node;
  }

  /** setAttention adds or removes the marker saying an agent in this tab is
   *  blocked. The triangle used to be appended by CSS, where no tooltip could
   *  reach it and no screen reader was told what it meant. As a real element it
   *  can say, in both channels, why the tab is calling for you. */
  function setAttention(node, on) {
    if (on === !!node.attn) return;
    if (!on) { node.attn.remove(); node.attn = null; return; }
    const attn = el("span", "attn", "▲");
    attn.setAttribute("role", "img");
    attn.setAttribute("aria-label", TIPS.waiting);
    describe(attn, TIPS.waiting);
    node.btn.insertBefore(attn, node.close);
    node.attn = attn;
  }

  function renameTab(id) {
    const tab = (state ? state.tabs : []).find((t) => t.id === id);
    if (!tab) return;
    const name = window.prompt("Rename tab", tab.title || "");
    if (name) send({ cmd: "renameTab", id, text: name });
  }

  /** tally builds one "▲ 3 waiting" count: the glyph is decoration, the words
   *  after it are the reading, and the tip explains the state being counted. */
  function tally(cls, sym, n, tip) {
    const span = el("span", cls);
    span.append(glyph(sym), document.createTextNode(` ${n} ${cls}`));
    return describe(span, tip);
  }

  /** ICONS maps a workspace state to the mark that stands for it. The window is
   *  a Chromium app-mode window, so the favicon is the application icon: this
   *  is what puts "an agent is waiting" in the taskbar while the window is
   *  behind three others and the title bar cannot be read. */
  const ICONS = {
    waiting: "/assets/icon-waiting.svg",
    working: "/assets/icon.svg",
    idle: "/assets/icon-idle.svg",
  };
  let iconState = "";

  /** setFavicon points the icon link at the mark for state. Assigning the same
   *  href again makes some browsers drop and refetch the icon, which shows up
   *  as the tab flickering on every state push, so an unchanged state is left
   *  alone — and the pushes are frequent. */
  function setFavicon(state) {
    if (state === iconState) return;
    const link = $("favicon");
    if (!link) return;
    iconState = state;
    link.href = ICONS[state];
  }

  /** The counts the summary is currently showing. It is a live region, so
   *  rewriting it is not free the way rewriting an ordinary element is: a
   *  screen reader reads out whatever appears in it, and a status push arrives
   *  every time any agent changes what it is doing. Left to rewrite itself
   *  unconditionally it spoke the same tally over and over, which with several
   *  agents running is continuously. */
  let summaryShown = "";

  function renderSummary(s) {
    const shown = s.waiting + " " + s.working;
    if (shown !== summaryShown) {
      summaryShown = shown;
      const box = $("summary");
      box.textContent = "";
      // The button's own tooltip is set once from the action table, in
      // describeChrome; re-titling it here would put a stale binding back.
      // Each count says what its own glyph means: the button's tooltip explains
      // where clicking leads, which is not the same question.
      if (s.waiting > 0) box.append(tally("waiting", "▲", s.waiting, TIPS.waiting));
      if (s.waiting > 0 && s.working > 0) box.append(document.createTextNode("  ·  "));
      if (s.working > 0) box.append(tally("working", "●", s.working, TIPS.working));
      document.title = s.waiting > 0
        ? `▲ ${s.waiting} waiting · perch`
        : (s.working > 0 ? `● ${s.working} working · perch` : "perch");
      setFavicon(s.waiting > 0 ? "waiting" : (s.working > 0 ? "working" : "idle"));
    }
    $("btn-broadcast").classList.toggle("on", !!s.broadcast);
    renderProjectChip(s);
  }

  /** renderProjectChip labels the switcher with the active project. */
  function renderProjectChip(s) {
    const active = (s.projects || []).find((p) => p.active);
    const name = active ? active.name : "project";
    if ($("project-name").textContent !== name) $("project-name").textContent = name;
    // The path goes into the same bubble as what the button does and the key
    // that does it, rather than into a title of its own. A title is taken over
    // as the tooltip the first time an element is hovered, so writing one here
    // replaced the button's description with a bare path and took the binding
    // away with it — the one thing the action table exists to prevent.
    const btn = $("project-btn");
    const tip = actionTip("projects", active ? active.root : "");
    if (btn.dataset.tip !== tip) describe(btn, tip);
    // Highlight when another project needs attention, so switching away does
    // not hide the fact that an agent there is blocked.
    const elsewhere = (s.projects || []).some((p) => !p.active && p.waiting > 0);
    btn.classList.toggle("attention", elsewhere);
  }

  // ------------------------------------------------- rearranging the layout

  /* Panes and tabs are moved by dragging, and nothing is created or destroyed
   * by it: the Go side moves the existing session's leaf in the layout tree, so
   * the process, its conversation and its scrollback come along untouched.
   *
   * What is being dragged is kept here rather than read from the dataTransfer,
   * because a dragover handler is not allowed to look inside it — and the
   * indicator has to be drawn during dragover, not on drop.
   */
  let dragging = null; // { kind: "pane" | "tab", id }

  function beginDrag(kind, id, ev, node) {
    dragging = { kind, id };
    node.classList.add("dragging");
    if (ev.dataTransfer) {
      ev.dataTransfer.effectAllowed = "move";
      // Some data must be set for a drag to start at all in some browsers.
      ev.dataTransfer.setData("text/plain", kind + ":" + id);
    }
  }

  function endDrag() {
    const wasTab = dragging && dragging.kind === "tab";
    dragging = null;
    document.querySelectorAll(".dragging").forEach((n) => n.classList.remove("dragging"));
    clearDropMarks();
    // The strip was left alone while a tab was being dragged; catch it up now
    // rather than waiting for a state push, which may not come if every agent
    // is idle.
    if (wasTab && state) renderTabs(state);
  }

  /** tabIdOfPane reports which tab currently holds a pane. */
  function tabIdOfPane(paneId) {
    for (const t of (state ? state.tabs : [])) {
      const ids = new Set();
      collectPanes(t.root, ids);
      if (ids.has(paneId)) return t.id;
    }
    return "";
  }

  /** clearDropMarks removes every drop indicator left by a drag in progress. */
  function clearDropMarks() {
    for (const p of panes.values()) {
      if (p.dropZone) p.dropZone.hidden = true;
    }
    document.querySelectorAll(".tab.drop-into, .tab.drop-before, .tab.drop-after")
      .forEach((n) => n.classList.remove("drop-into", "drop-before", "drop-after"));
    $("tabs").classList.remove("drop-end");
    $("new-tab").classList.remove("drop-into");
  }

  /** edgeAt reports which part of a rectangle a point is in. The middle of a
   * pane means "swap these two", which is how two agents trade places without
   * the layout changing shape. */
  function edgeAt(rect, x, y) {
    const fx = (x - rect.left) / (rect.width || 1);
    const fy = (y - rect.top) / (rect.height || 1);
    if (fx > 0.3 && fx < 0.7 && fy > 0.3 && fy < 0.7) return "swap";
    // Whichever edge the pointer is nearest, measured as a fraction so that a
    // tall thin pane still splits sensibly.
    const dist = { left: fx, right: 1 - fx, top: fy, bottom: 1 - fy };
    return Object.keys(dist).reduce((a, b) => (dist[b] < dist[a] ? b : a));
  }

  /** makePaneDraggable turns a pane into both a drag source and a drop target. */
  function makePaneDraggable(id, wrap, header, dropZone) {
    header.draggable = true;
    header.addEventListener("dragstart", (ev) => {
      // Dragging from a header button would be a misfire.
      if (ev.target.closest("button")) { ev.preventDefault(); return; }
      beginDrag("pane", id, ev, wrap);
    });
    header.addEventListener("dragend", endDrag);

    wrap.addEventListener("dragover", (ev) => {
      if (!dragging || dragging.kind !== "pane" || dragging.id === id) return;
      ev.preventDefault();
      ev.dataTransfer.dropEffect = "move";
      const edge = edgeAt(wrap.getBoundingClientRect(), ev.clientX, ev.clientY);
      dropZone.className = "pane-drop " + edge;
      dropZone.hidden = false;
    });
    wrap.addEventListener("dragleave", (ev) => {
      // Moving between the pane's own children is not leaving it.
      if (!wrap.contains(ev.relatedTarget)) dropZone.hidden = true;
    });
    wrap.addEventListener("drop", (ev) => {
      if (!dragging || dragging.kind !== "pane" || dragging.id === id) return;
      ev.preventDefault();
      const edge = edgeAt(wrap.getBoundingClientRect(), ev.clientX, ev.clientY);
      const moved = dragging.id;
      clearDropMarks();
      if (edge === "swap") send({ cmd: "swapPanes", id: moved, target: id });
      else send({ cmd: "movePane", id: moved, target: id, edge });
    });
  }

  /** tabDropZone reports what releasing a dragged tab here would do, from
   * where along the target tab the pointer is: the ends reorder it, and the
   * middle merges the two tabs into one. It is the tab bar's version of
   * edgeAt — the middle means "combine these", there as here. */
  function tabDropZone(rect, x) {
    const fx = (x - rect.left) / (rect.width || 1);
    if (fx < 0.3) return "before";
    if (fx > 0.7) return "after";
    return "merge";
  }

  /** tabAfter returns the id of the tab following the given one, or "" when it
   * is the last — which is what moveTab reads as "put it at the end". */
  function tabAfter(tabId) {
    const tabs = state ? state.tabs : [];
    const i = tabs.findIndex((t) => t.id === tabId);
    return i >= 0 && i + 1 < tabs.length ? tabs[i + 1].id : "";
  }

  /** makeTabDraggable makes a tab reorderable, mergeable into another tab, and
   * a place to drop a pane. */
  function makeTabDraggable(tabId, node) {
    node.draggable = true;
    node.addEventListener("dragstart", (ev) => {
      if (ev.target.closest(".close")) { ev.preventDefault(); return; }
      beginDrag("tab", tabId, ev, node);
    });
    node.addEventListener("dragend", endDrag);

    // A pane always lands inside the tab it is dropped on; a tab depends on
    // where along it the pointer is.
    const zoneAt = (ev) => dragging.kind === "pane"
      ? "merge"
      : tabDropZone(node.getBoundingClientRect(), ev.clientX);

    node.addEventListener("dragover", (ev) => {
      if (!dragging || dragging.id === tabId) return;
      // Offering to move a pane into the tab it is already in would be a
      // no-op dressed up as an action.
      if (dragging.kind === "pane" && tabIdOfPane(dragging.id) === tabId) return;
      ev.preventDefault();
      ev.dataTransfer.dropEffect = "move";
      const zone = zoneAt(ev);
      node.classList.toggle("drop-before", zone === "before");
      node.classList.toggle("drop-after", zone === "after");
      node.classList.toggle("drop-into", zone === "merge");
    });
    node.addEventListener("dragleave", () => {
      node.classList.remove("drop-into", "drop-before", "drop-after");
    });
    node.addEventListener("drop", (ev) => {
      if (!dragging || dragging.id === tabId) return;
      ev.preventDefault();
      const { kind, id } = dragging;
      const zone = zoneAt(ev);
      clearDropMarks();
      if (kind === "pane") send({ cmd: "movePaneToTab", id, target: tabId });
      else if (zone === "merge") send({ cmd: "mergeTab", id, target: tabId, dir: "h" });
      else send({ cmd: "moveTab", id, target: zone === "before" ? tabId : tabAfter(tabId) });
    });
  }

  /** Anything that ends a drag clears the indicators, including a drop on a
   * part of the window that is not a target at all, and a drag whose source
   * element was replaced by a state push while it was in flight. */
  function wireDragSafetyNet() {
    document.addEventListener("dragend", endDrag);
    document.addEventListener("drop", endDrag);
  }

  /** wireTabStripDrops handles the space past the last tab and the + button:
   * a tab dropped there goes to the end, a pane dropped there gets a tab of
   * its own. */
  function wireTabStripDrops() {
    const strip = $("tabs");
    strip.addEventListener("dragover", (ev) => {
      // Only the bare strip; a drop on a tab is that tab's business.
      if (!dragging || ev.target.closest(".tab")) return;
      ev.preventDefault();
      ev.dataTransfer.dropEffect = "move";
      strip.classList.toggle("drop-end", dragging.kind === "tab");
    });
    strip.addEventListener("dragleave", (ev) => {
      if (!strip.contains(ev.relatedTarget)) strip.classList.remove("drop-end");
    });
    strip.addEventListener("drop", (ev) => {
      if (!dragging || ev.target.closest(".tab")) return;
      ev.preventDefault();
      const { kind, id } = dragging;
      clearDropMarks();
      if (kind === "tab") send({ cmd: "moveTab", id, target: "" });
      else send({ cmd: "movePaneToNewTab", id });
    });

    // The strip is only as wide as its tabs, so the + button is where the end
    // of the bar can actually be hit: a pane dropped on it gets a tab of its
    // own, a tab dropped on it goes last.
    const plus = $("new-tab");
    plus.addEventListener("dragover", (ev) => {
      if (!dragging) return;
      ev.preventDefault();
      ev.dataTransfer.dropEffect = "move";
      plus.classList.add("drop-into");
    });
    plus.addEventListener("dragleave", () => plus.classList.remove("drop-into"));
    plus.addEventListener("drop", (ev) => {
      if (!dragging) return;
      ev.preventDefault();
      const { kind, id } = dragging;
      clearDropMarks();
      if (kind === "tab") send({ cmd: "moveTab", id, target: "" });
      else send({ cmd: "movePaneToNewTab", id });
    });
  }

  // ------------------------------------------------------------------ panes

  function ensurePane(id) {
    let p = panes.get(id);
    if (p) return p;

    const wrap = el("div", "pane");
    const header = el("div", "pane-header");
    const dot = el("span", "dot");
    const name = el("span", "pane-name");
    const project = el("span", "pane-project");
    const branch = el("span", "pane-branch");
    const detail = el("span", "pane-detail");
    const git = el("span", "pane-git");
    const cast = el("span", "pane-cast");
    const actions = el("div", "pane-actions");

    // Every one of these reads as a bare symbol, so describe carries the
    // wording twice over: into the bubble, and into the aria-label the label
    // itself cannot supply. The copy comes from TIPS, so the pane header
    // explains a glyph the same way the overlays do.
    const btn = (label, tip, fn) => {
      const b = el("button", null, label);
      b.onclick = (ev) => { ev.stopPropagation(); fn(); };
      return describe(b, tip);
    };
    actions.append(
      btn("⑂", TIPS.fanOut, () => openFanout(id)),
      btn("⇉", "Adds this pane to the broadcast set, or takes it out again. " + TIPS.broadcast,
        () => send({ cmd: "toggleBroadcastMember", id })),
      btn("⟳", TIPS.restart, () => send({ cmd: "restartPane", id })),
      btn("⤢", TIPS.zoom, () => send({ cmd: "toggleZoom", id })),
      btn("×", TIPS.close, () => send({ cmd: "closePane", id })),
    );
    header.append(dot, project, name, branch, git, detail, cast, actions);

    const body = el("div", "pane-body");
    const host = el("div", "term-host");
    body.append(host);
    // The drop indicator sits above the terminal and takes no pointer events,
    // so a drag crossing a pane is not swallowed by the xterm canvas.
    const dropZone = el("div", "pane-drop");
    dropZone.hidden = true;
    wrap.append(header, body, dropZone);
    makePaneDraggable(id, wrap, header, dropZone);

    wrap.addEventListener("mousedown", () => {
      if (state && currentTab() && currentTab().focus !== id) send({ cmd: "focusPane", id });
    });

    const term = new Terminal({
      allowProposedApi: true,
      cursorBlink: true,
      fontFamily: '"Cascadia Mono", "JetBrains Mono", Consolas, "SF Mono", Menlo, monospace',
      fontSize: fontSize,
      lineHeight: 1.15,
      scrollback: 10000,
      theme: {
        background: "#0f1114",
        foreground: "#d8dee9",
        cursor: "#4c9aff",
        selectionBackground: "#2f4665",
      },
    });
    const fit = new FitAddon.FitAddon();
    term.loadAddon(fit);
    let search = null;
    try {
      search = new SearchAddon.SearchAddon();
      term.loadAddon(search);
    } catch {
      /* Search is a convenience; the terminal works without it. */
    }
    term.open(host);
    try {
      const webgl = new WebglAddon.WebglAddon();
      webgl.onContextLoss(() => webgl.dispose());
      term.loadAddon(webgl);
    } catch {
      /* Canvas rendering is a fine fallback. */
    }

    p = { id, wrap, header, dot, name, project, branch, git, detail, cast, body, host, term, fit, ws: null,
          nodeId: "", fitTimer: 0, retryTimer: 0, retries: 0, cols: 0, rows: 0, actions, search, dropZone,
          // What each part of the header is currently showing. Empty to begin
          // with, so the first push draws all of it.
          shown: {} };
    panes.set(id, p);

    term.onData((data) => sendInput(p, data));
    term.onBinary((data) => {
      const bytes = new Uint8Array(data.length);
      for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 255;
      sendBytes(p, bytes);
    });

    // Kept, because it has to be disconnected when the pane closes: an
    // observer with a live observation is held by the document whether or not
    // the element it is watching is still in it, and through its callback it
    // holds this whole record — the terminal, the socket and the header.
    p.resize = new ResizeObserver(() => scheduleFit(p));
    p.resize.observe(host);
    connectPTY(p);
    return p;
  }

  function sendInput(p, data) {
    sendBytes(p, new TextEncoder().encode(data));
  }
  function sendBytes(p, bytes) {
    if (p.ws && p.ws.readyState === WebSocket.OPEN) p.ws.send(bytes);
  }

  function connectPTY(p) {
    if (p.ws) { try { p.ws.close(); } catch {} }
    clearTimeout(p.retryTimer);

    const ws = new WebSocket(wsBase + "/ws/pty?id=" + encodeURIComponent(p.id));
    ws.binaryType = "arraybuffer";
    p.ws = ws;

    ws.onopen = () => {
      p.retries = 0;
      p.cols = p.rows = 0; // force the size to be re-reported
      // The stream restarts from the session's replay buffer, so clear
      // whatever this terminal was showing rather than interleaving the two.
      p.term.reset();
      scheduleFit(p);
    };
    ws.onmessage = (ev) => p.term.write(new Uint8Array(ev.data));
    ws.onclose = () => {
      if (p.ws !== ws) return;
      p.ws = null;
      // The server closes this stream whenever the session behind the pane
      // goes away - which includes restarting it. Reconnect so a restarted
      // pane comes back to life instead of sitting there dead.
      if (!panes.has(p.id)) return;
      const delay = Math.min(250 * Math.pow(2, p.retries++), 3000);
      p.retryTimer = setTimeout(() => { if (panes.has(p.id)) connectPTY(p); }, delay);
    };
  }

  function scheduleFit(p) {
    clearTimeout(p.fitTimer);
    p.fitTimer = setTimeout(() => doFit(p), 40);
  }

  function doFit(p) {
    if (!p.host.isConnected || p.host.offsetParent === null) return;
    try { p.fit.fit(); } catch { return; }
    const { cols, rows } = p.term;
    if (!cols || !rows || (cols === p.cols && rows === p.rows)) return;
    p.cols = cols; p.rows = rows;
    if (p.ws && p.ws.readyState === WebSocket.OPEN) {
      p.ws.send(JSON.stringify({ resize: { cols, rows } }));
    }
  }

  /* A status push carries the whole workspace and arrives every time any agent
   * changes what it is doing or prints another line of detail, so with several
   * running they are close to continuous. Almost none of a pane's header moves
   * between two of them: the name never changes, the branch and the project
   * hardly ever, the git counts only when a file is written. Redrawing all of
   * it anyway meant building the branch, the git markers and the broadcast
   * label from scratch several times a second per pane — and taking the
   * elements a tooltip was anchored to away with them. So each part remembers
   * what it is showing and is left alone until that differs.
   */
  function updatePaneChrome(s) {
    const tab = activeTabOf(s);
    for (const [id, p] of panes) {
      const v = s.panes[id];
      if (!v) continue;
      const was = p.shown;

      if (was.status !== v.status) {
        was.status = v.status;
        // On the pane as well as on its dot: a tab of six agents is six small
        // discs, and the one that has stopped and is waiting on you should not
        // have to be found by reading each header in turn.
        p.wrap.dataset.status = v.status;
        p.dot.className = "dot " + v.status;
        // The dot is nothing but a coloured circle, so it has to say the whole
        // sentence itself; the bare status word left the colour unexplained.
        p.dot.setAttribute("role", "img");
        p.dot.setAttribute("aria-label", TIPS[v.status] || v.status);
        describe(p.dot, TIPS[v.status] || v.status);
      }
      if (was.name !== v.name) { was.name = v.name; p.name.textContent = v.name; }

      const project = v.project || "";
      if (was.project !== project) { was.project = project; renderPaneProject(p, v); }

      const branch = v.branch || "";
      if (was.branch !== branch) { was.branch = branch; renderPaneBranch(p, v); }

      const detail = v.detail || "";
      if (was.detail !== detail) { was.detail = detail; p.detail.textContent = detail; }

      const git = [v.dirty, v.untracked, v.ahead, v.behind].join(" ");
      if (was.git !== git) { was.git = git; renderPaneGit(p, v); }

      const cast = (v.broadcast ? "1" : "0") + (s.broadcast ? "1" : "0");
      if (was.cast !== cast) { was.cast = cast; renderPaneCast(p, v, s); }

      p.wrap.classList.toggle("focused", !!tab && tab.focus === id);
      renderPaneOverlay(p, v);
    }
  }

  /** renderPaneCast says whether what is typed in the prompt bar reaches this
   *  pane. Membership is worth showing even when broadcast is off, otherwise
   *  the button that toggles it appears to do nothing at all. */
  function renderPaneCast(p, v, s) {
    p.cast.textContent = "";
    if (v.broadcast) {
      p.cast.append(glyph("⇉"), document.createTextNode(s.broadcast ? " broadcast" : " in set"));
      describe(p.cast, s.broadcast
        ? "What you type in the prompt bar is delivered to this pane. " + TIPS.broadcast
        : "This pane is in the broadcast set, so it receives what you type in the prompt bar once broadcast is on. " + TIPS.broadcast);
    } else {
      delete p.cast.dataset.tip;
    }
    p.cast.classList.toggle("active", !!(v.broadcast && s.broadcast));
    p.actions.firstChild.classList.toggle("on", !!v.broadcast);
  }

  /** renderPaneProject names the pane's own project, and is empty for the
   *  ordinary pane whose project is its tab's. The server only sends the field
   *  when the two differ, so the label appears exactly when it says something
   *  the tab bar does not already. */
  function renderPaneProject(p, v) {
    p.project.textContent = "";
    p.project.hidden = !v.project;
    if (!v.project) { delete p.project.dataset.tip; return; }
    p.project.textContent = v.project;
    describe(p.project, TIPS.project);
  }

  /** renderPaneBranch names the checkout the pane is sitting in. The fork glyph
   *  is the only thing marking it as a branch rather than a title, so it is the
   *  part that needs explaining; the name is already words. */
  function renderPaneBranch(p, v) {
    p.branch.textContent = "";
    if (!v.branch) { delete p.branch.dataset.tip; return; }
    p.branch.append(glyph("⎇"), document.createTextNode(" " + v.branch));
    describe(p.branch, TIPS.branch);
  }

  /** renderPaneOverlay covers the terminal when the pane has no live process. */
  function renderPaneOverlay(p, v) {
    const needed = !!v.err || v.status === "exited";
    if (!needed) {
      if (p.overlay) { p.overlay.remove(); p.overlay = null; }
      return;
    }
    if (p.overlay) return;
    const box = el("div", "pane-error");
    box.append(el("div", null, v.err || "The process exited."));
    const row = el("div");
    const restart = el("button", "chip primary", "Restart");
    restart.onclick = () => send({ cmd: "restartPane", id: p.id });
    const close = el("button", "chip", "Close pane");
    close.onclick = () => send({ cmd: "closePane", id: p.id });
    row.append(restart, document.createTextNode(" "), close);
    box.append(row);
    p.body.append(box);
    p.overlay = box;
  }

  /** prunePanes takes down the panes the workspace no longer has. Everything a
   *  pane holds that outlives its elements has to be given up here: the layout
   *  tree is the only record of which panes exist, so nothing else will ever
   *  come back to this one. */
  function prunePanes(s) {
    const live = paneIdsIn(s);
    for (const [id, p] of panes) {
      if (live.has(id)) continue;
      clearTimeout(p.retryTimer);
      clearTimeout(p.fitTimer);
      panes.delete(id); // stop the close handler from reconnecting
      try { p.ws && p.ws.close(); } catch {}
      p.resize.disconnect();
      p.term.dispose();
      // A pane can go while the pointer is resting on something in its header,
      // which would leave the bubble describing it hanging over the pane that
      // takes its place.
      if (tipFor && p.wrap.contains(tipFor)) hideTip();
      p.wrap.remove();
    }
  }

  /** activeTabOf resolves the focused tab by id. */
  function activeTabOf(s) {
    if (!s || !s.tabs) return null;
    return s.tabs.find((t) => t.id === s.activeTab) || s.tabs[0] || null;
  }

  function currentTab() { return activeTabOf(state); }
  function focusedPaneId() { const t = currentTab(); return t ? t.focus : ""; }

  // -------------------------------------------------------------- prompt bar

  function openPrompt() {
    const bar = $("promptbar");
    bar.hidden = false;
    const n = state ? countBroadcast(state) : 1;
    $("prompt-label").textContent = state && state.broadcast && n > 1 ? `Prompt → ${n} panes` : "Prompt";
    const input = $("prompt-input");
    input.value = "";
    input.focus();
  }
  function closePrompt() {
    $("promptbar").hidden = true;
    focusTerminal();
  }
  function submitPrompt() {
    const text = $("prompt-input").value;
    if (text.trim()) send({ cmd: "sendPrompt", text });
    closePrompt();
  }
  function countBroadcast(s) {
    const t = activeTabOf(s);
    if (!t) return 0;
    const ids = new Set(); collectPanes(t.root, ids);
    let n = 0;
    ids.forEach((id) => { const v = s.panes[id]; if (v && (v.broadcast || id === t.focus)) n++; });
    return n;
  }

  /** focusTerminal puts the keyboard in a tab's focused pane, falling back to
   * whatever pane the tab has if the server has not named one yet. */
  function focusTerminal(tab, ids) {
    const t = tab || currentTab();
    if (!t) return;
    let p = panes.get(t.focus);
    if (!p) {
      if (!ids) { ids = new Set(); collectPanes(t.root, ids); }
      for (const id of ids) {
        const q = panes.get(id);
        if (q) { p = q; break; }
      }
    }
    if (p) p.term.focus();
  }

  /** dialogOpen reports whether anything is on screen that the keyboard could
   * reasonably belong to, so that a pane appearing behind a dialog does not
   * pull the focus out from under someone mid-sentence. It answers for the
   * dialog being open rather than for it holding the focus, because the state
   * push and the dialog's own focus() race: the button that opened the dialog
   * also moved the focus, and the answer must not depend on which won. */
  function dialogOpen() {
    if (typingInDialog()) return true;
    return ["overlay", "palette", "promptbar", "searchbar"].some((id) => {
      const n = $(id);
      return n && !n.hidden;
    });
  }

  /** typingInDialog reports whether a dialog field has the keyboard, so that a
   * tab switch does not pull focus out from under someone mid-sentence. */
  function typingInDialog() {
    const a = document.activeElement;
    return !!(a && a.closest && a.closest("#overlay, #palette, #promptbar, #searchbar"));
  }

  /* A modal covers the window and nothing behind it can be used — but the Tab
   * key does not know that. Left alone it walks out of the dialog and into the
   * terminal underneath, where the box that appeared to have the keyboard no
   * longer does and the next thing typed is delivered to an agent. So Tab is
   * kept inside whichever of the three is on screen, and opening one puts the
   * keyboard in it to begin with.
   */

  /** FOCUSABLE names everything Tab can land on. Disabled controls and
   *  anything a dialog has hidden are filtered out afterwards. */
  const FOCUSABLE = "button, input, textarea, select, a[href], [tabindex]";

  /** modalRoot is the panel of the dialog that has taken the window over, if
   *  one has. The prompt and find bars are strips rather than modals: they
   *  leave the panes visible and usable, so Tab may leave them. */
  function modalRoot() {
    if (!$("disconnected").hidden) return $("disconnected").querySelector(".panel");
    if (!$("palette").hidden) return $("palette-box");
    if (!$("overlay").hidden) return $("overlay-panel");
    return null;
  }

  function focusablesIn(root) {
    return [...root.querySelectorAll(FOCUSABLE)].filter((n) => {
      if (n.disabled || n.getAttribute("tabindex") === "-1") return false;
      for (let p = n; p && p !== root.parentElement; p = p.parentElement) if (p.hidden) return false;
      return true;
    });
  }

  /** trapTab moves the focus on itself and reports that it has, so the key
   *  never reaches the browser's own idea of what follows this element. */
  function trapTab(e) {
    const root = modalRoot();
    if (!root) return false;
    e.preventDefault();
    const items = focusablesIn(root);
    if (!items.length) { root.focus(); return true; }
    const at = items.indexOf(document.activeElement);
    // Focus that has fallen outside — onto the body, because the dialog
    // redrew — comes back in at whichever end it was heading for.
    const next = at < 0
      ? (e.shiftKey ? items[items.length - 1] : items[0])
      : items[(at + (e.shiftKey ? -1 : 1) + items.length) % items.length];
    next.focus();
    return true;
  }

  // --------------------------------------------------------------- overlays

  /** openOverlay shows the dialog panel. `page` names the help page that
   *  explains what is in it, which becomes a `?` in the dialog's own header —
   *  the question is asked here, so this is where the answer belongs. */
  function openOverlay(title, page) {
    $("overlay-title").textContent = title;
    $("overlay-body").textContent = "";
    $("overlay-panel").classList.remove("wide");

    const old = $("overlay-help");
    if (old) old.remove();
    if (page) {
      const b = el("button", "icon-btn", "?");
      b.id = "overlay-help";
      describe(b, "What this is, and how it works");
      b.onclick = () => openHelp(page);
      $("overlay-head").insertBefore(b, $("overlay-close"));
    }
    $("overlay").hidden = false;
    // The dialog names itself through its heading, so landing here is what
    // announces which one opened; the pages that have a field of their own
    // take the keyboard off it a moment later.
    $("overlay-panel").focus();
  }
  /** keepFocus redraws the dialog's body and puts the keyboard back on the
   *  control it was on.
   *
   *  These dialogs are drawn whole from the reply that comes back, so pressing
   *  Refresh, or Fetch, or Remove throws away the button that was pressed along
   *  with everything else — and the keyboard with it, onto the body, with no way
   *  back into the dialog but tabbing from the top. The control is found again
   *  by what it is and what it says, which is how a person finds it too, and
   *  the text caret is put back where it was for a field.
   */
  function keepFocus(draw) {
    const body = $("overlay-body");
    const was = document.activeElement;
    const key = was && body.contains(was) ? identify(was) : "";
    const at = was && was.selectionStart;
    draw();
    if (!key) return;
    for (const node of body.querySelectorAll("button, input, textarea, select, [tabindex]")) {
      if (identify(node) !== key) continue;
      node.focus();
      if (at != null && node.setSelectionRange) {
        try { node.setSelectionRange(at, at); } catch { /* not that kind of field */ }
      }
      return;
    }
  }

  /** identify is what makes a control the same control across a redraw. */
  function identify(node) {
    return [node.id, node.tagName, node.className, (node.textContent || "").trim()].join("|");
  }

  function closeOverlay() {
    $("overlay").hidden = true;
    $("overlay-panel").classList.remove("wide");
    dialog = null;
    focusTerminal();
  }

  // ------------------------------------------------------------- worktrees

  let worktrees = null;

  function renderWorktrees(msg) {
    if (msg) worktrees = msg;
    if ($("overlay").hidden || dialog !== "worktrees") {
      dialog = "worktrees";
      openOverlay("Worktrees", "worktrees");
    }
    const m = worktrees || {};
    const body = $("overlay-body");
    body.textContent = "";

    if (m.error) {
      body.append(el("p", null, m.error));
      return;
    }

    // --- existing worktrees ------------------------------------------------
    const list = section("Checkouts");
    (m.items || []).forEach((wt) => {
      const row = el("div", "wt-row");

      const main = el("div", "wt-main");
      const titleLine = el("div", "wt-title");
      titleLine.append(el("span", "wt-label", wt.label));
      if (wt.main) titleLine.append(el("span", "wt-flag", "main"));
      if (wt.locked) titleLine.append(el("span", "wt-flag", "locked"));
      if (wt.detached) titleLine.append(el("span", "wt-flag", "detached"));
      if (wt.panes) titleLine.append(el("span", "wt-flag agents", wt.panes + (wt.panes === 1 ? " agent" : " agents")));
      main.append(titleLine);

      const meta = el("div", "wt-meta");
      // This line is all glyphs and bare numbers. Each mark takes the shared
      // copy for what the symbol means, and an aria-label that keeps the count
      // a screen reader would otherwise lose to the symbol.
      const upName = wt.upstream || "the upstream branch";
      const plural = (n, word) => n + " " + word + (n === 1 ? "" : "s");
      if (wt.dirty) {
        const n = el("span", "wt-dirty", "● " + wt.dirty + " changed");
        n.setAttribute("role", "img");
        n.setAttribute("aria-label", plural(wt.dirty, "tracked file") + " changed but not committed");
        meta.append(describe(n, TIPS.changed));
      }
      if (wt.untracked) {
        const n = el("span", "wt-untracked", "+" + wt.untracked + " new");
        n.setAttribute("role", "img");
        n.setAttribute("aria-label", plural(wt.untracked, "new file") + " not tracked by git");
        meta.append(describe(n, TIPS.untracked));
      }
      if (!wt.dirty && !wt.untracked) meta.append(describe(el("span", "wt-clean", "clean"), TIPS.clean));
      if (wt.ahead) {
        const n = el("span", "wt-ahead", "↑" + wt.ahead);
        n.setAttribute("role", "img");
        n.setAttribute("aria-label", plural(wt.ahead, "commit") + " ahead of " + upName);
        meta.append(describe(n, TIPS.ahead));
      }
      if (wt.behind) {
        const n = el("span", "wt-behind", "↓" + wt.behind);
        n.setAttribute("role", "img");
        n.setAttribute("aria-label", plural(wt.behind, "commit") + " behind " + upName);
        meta.append(describe(n, TIPS.behind));
      }
      if (wt.upstream) meta.append(describe(el("span", "wt-upstream", wt.upstream),
        "The branch this worktree tracks. Ahead and behind are counted against it."));
      if (wt.head) meta.append(describe(el("span", "wt-head", wt.head),
        "The commit this worktree has checked out."));
      main.append(meta);
      main.append(el("div", "wt-path", wt.path));
      row.append(main);

      const actions = el("div", "wt-actions");
      const agent = el("button", "chip primary", "Agent");
      agent.title = "Open an agent tab in this worktree";
      agent.onclick = () => { send({ cmd: "newTab", kind: "claude", path: wt.path, text: wt.label }); closeOverlay(); };
      const shell = el("button", "chip", "Shell");
      shell.onclick = () => { send({ cmd: "newTab", kind: "shell", path: wt.path, text: wt.label }); closeOverlay(); };
      const split = el("button", "chip", "Split");
      split.title = "Add an agent for this worktree beside the current pane";
      split.onclick = () => {
        send({ cmd: "splitPane", id: focusedPaneId(), dir: "h", kind: "claude", path: wt.path });
        closeOverlay();
      };
      const review = el("button", "chip", "Review");
      review.title = "See what changed here, commit and push";
      review.onclick = () => openChanges(wt.path);
      actions.append(agent, shell, split, review);

      if (!wt.main) {
        const rm = el("button", "chip danger", "Remove");
        const unsafe = wt.dirty || wt.untracked;
        rm.title = unsafe ? "This worktree has uncommitted work" : "Remove this worktree";
        rm.onclick = () => {
          if (unsafe && !window.confirm(
            wt.label + " has uncommitted changes.\n\nRemove it and discard them?")) return;
          send({ cmd: "worktreeRemove", path: wt.path, force: !!unsafe });
        };
        actions.append(rm);
      }
      row.append(actions);
      list.append(row);
    });
    if (!(m.items || []).length) list.append(el("div", "dir-empty", "No worktrees."));
    body.append(list);

    // --- create a new one --------------------------------------------------
    const create = section("New worktree");
    const form = el("div", "wt-form");

    const branch = el("input");
    branch.placeholder = "Branch name, e.g. fix-auth";
    branch.id = "wt-branch";
    const base = el("input");
    base.placeholder = "Base";
    base.value = m.defaultBase || "";
    base.id = "wt-base";
    base.title = "The commit or branch the new branch starts from";
    base.setAttribute("list", "wt-bases");

    const bases = el("datalist");
    bases.id = "wt-bases";
    (m.branches || []).forEach((b) => {
      const o = document.createElement("option");
      o.value = b.name;
      bases.append(o);
    });

    const go = el("button", "chip primary", "Create");
    const submit = () => {
      if (!branch.value.trim()) return;
      send({ cmd: "worktreeAdd", text: branch.value.trim(), base: base.value.trim() });
      branch.value = "";
    };
    go.onclick = submit;
    branch.onkeydown = (ev) => { if (ev.key === "Enter") submit(); };
    form.append(branch, base, go, bases);
    create.append(form);
    create.append(el("div", "wt-hint",
      "The worktree is created next to the repository. An existing branch name checks it out instead of creating one."));
    body.append(create);

    // --- existing branches without a worktree ------------------------------
    const free = (m.branches || []).filter((b) => !b.checkedIn);
    if (free.length) {
      const bs = section("Branches without a worktree");
      const chips = el("div", "places");
      free.slice(0, 14).forEach((b) => {
        const btn = el("button", "chip", b.name);
        btn.title = "Create a worktree checking out " + b.name;
        btn.onclick = () => send({ cmd: "worktreeAdd", text: b.name });
        chips.append(btn);
      });
      bs.append(chips);
      body.append(bs);
    }

    // --- maintenance -------------------------------------------------------
    const tools = el("div", "wt-tools");
    const refresh = el("button", "chip", "Refresh");
    refresh.onclick = () => send({ cmd: "worktrees" });
    const prune = el("button", "chip", "Prune");
    prune.title = "Drop records for worktrees whose folders are gone";
    prune.onclick = () => send({ cmd: "worktreePrune" });
    tools.append(refresh, prune, el("span", "wt-root", m.root || ""));
    body.append(tools);
  }

  // -------------------------------------------------------------- projects

  function openProjects() {
    dialog = "projects";
    openOverlay("Projects", "projects");
    send({ cmd: "recents" });
    send({ cmd: "browse", path: browseState ? browseState.path : (state ? state.root : "") });
    renderProjects();
  }

  function renderProjects() {
    if (dialog !== "projects") return;
    const body = $("overlay-body");
    body.textContent = "";

    // Open projects: switch between them, or close one.
    const open = (state && state.projects) || [];
    const openSection = section("Open");
    open.forEach((p) => {
      const row = el("div", "proj-row" + (p.active ? " active" : ""));
      const main = el("div", "proj-main");
      main.append(el("div", "proj-name", p.name));
      main.append(el("div", "proj-path", p.root));
      row.append(main);

      const badge = el("div", "proj-badge");
      // The triangle and the disc are the marks the pane headers already use,
      // so they take the same copy; the label keeps the count the glyph hides.
      if (p.waiting) {
        const n = el("span", "waiting", "\u25b2 " + p.waiting);
        n.setAttribute("role", "img");
        n.setAttribute("aria-label", p.waiting + (p.waiting === 1 ? " agent" : " agents") + " waiting on you");
        badge.append(describe(n, TIPS.waiting), document.createTextNode(" "));
      }
      if (p.working) {
        const n = el("span", "working", "\u25cf " + p.working);
        n.setAttribute("role", "img");
        n.setAttribute("aria-label", p.working + (p.working === 1 ? " agent" : " agents") + " working");
        badge.append(describe(n, TIPS.working), document.createTextNode(" "));
      }
      badge.append(document.createTextNode(p.tabs + (p.tabs === 1 ? " tab" : " tabs")));
      row.append(badge);

      // Splitting into the project already on screen is just an ordinary
      // split, so the button is offered on the others.
      if (!p.active) {
        const split = el("button", "icon-btn", "⊞");
        describe(split, TIPS.splitHere);
        split.onclick = (ev) => {
          ev.stopPropagation();
          send({ cmd: "splitPane", dir: "h", root: p.root });
          closeOverlay();
        };
        row.append(split);
      }

      if (open.length > 1) {
        const close = el("button", "icon-btn", "\u00d7");
        describe(close, "Close this project. The agents running in it stop.");
        close.onclick = (ev) => { ev.stopPropagation(); send({ cmd: "closeProject", root: p.root }); };
        row.append(close);
      }
      row.onclick = () => { send({ cmd: "selectProject", root: p.root }); closeOverlay(); };
      openSection.append(row);
    });
    body.append(openSection);

    // Recent projects that are not already open.
    const notOpen = recents.filter((r) => !r.open);
    if (notOpen.length) {
      const rec = section("Recent");
      notOpen.slice(0, 8).forEach((r) => {
        const row = el("div", "proj-row" + (r.exists ? "" : " missing"));
        const main = el("div", "proj-main");
        main.append(el("div", "proj-name", r.name));
        main.append(el("div", "proj-path", r.exists ? r.root : r.root + "  (missing)"));
        row.append(main);
        const forget = el("button", "icon-btn", "\u00d7");
        describe(forget, "Drop this project from the recent list. Nothing on disk is touched.");
        forget.onclick = (ev) => { ev.stopPropagation(); send({ cmd: "forgetRecent", root: r.root }); };
        row.append(forget);
        if (r.exists) row.onclick = () => { send({ cmd: "openProject", path: r.root }); closeOverlay(); };
        rec.append(row);
      });
      body.append(rec);
    }

    body.append(renderBrowser());
  }

  /** rowAction makes a whole row behave as the button it already is to a
   *  mouse. A div carrying an onclick cannot be reached with Tab and does not
   *  answer Enter, so a list built out of them is a list only a pointer can
   *  use — and these lists are how the file an agent changed gets read and how
   *  the agent that is blocked gets found. */
  function rowAction(row, fn) {
    row.tabIndex = 0;
    row.setAttribute("role", "button");
    row.onclick = fn;
    row.onkeydown = (ev) => {
      if (ev.key !== "Enter" && ev.key !== " ") return;
      ev.preventDefault();
      fn(ev);
    };
    return row;
  }

  function section(title) {
    const wrap = el("div", "proj-section");
    wrap.append(el("h3", null, title));
    return wrap;
  }

  /** renderBrowser is the directory picker. A browser cannot hand us a real
   *  path, so navigation is served by the Go side. */
  function renderBrowser() {
    const wrap = section("Open a folder");
    const b = browseState;

    const bar = el("div", "browse-bar");
    const up = el("button", "chip", "\u2191 Up");
    up.disabled = !b || !b.parent;
    up.onclick = () => send({ cmd: "browse", path: b.parent });
    const path = el("input");
    path.value = b ? b.path : "";
    path.placeholder = "Type or paste a path, then press Enter";
    path.onkeydown = (ev) => {
      if (ev.key !== "Enter") return;
      ev.preventDefault();
      send({ cmd: "browse", path: path.value });
    };
    const openHere = el("button", "chip primary", "Open this folder");
    openHere.onclick = () => {
      const target = path.value || (b && b.path);
      if (target) { send({ cmd: "openProject", path: target }); closeOverlay(); }
    };
    bar.append(up, path, openHere);
    wrap.append(bar);

    if (b && b.places && b.places.length) {
      const places = el("div", "places");
      b.places.forEach((pl) => {
        const btn = el("button", "chip", pl.name);
        btn.onclick = () => send({ cmd: "browse", path: pl.path });
        places.append(btn);
      });
      wrap.append(places);
    }

    const list = el("div", "dir-list");
    if (b && b.error) {
      list.append(el("div", "dir-empty", b.error));
    } else if (!b || !b.entries || !b.entries.length) {
      list.append(el("div", "dir-empty", b ? "No sub-folders here." : "Loading\u2026"));
    } else {
      b.entries.forEach((e) => {
        const row = el("div", "dir-row" + (e.isRepo ? " repo" : "") + (e.hidden ? " hidden-dir" : ""));
        // One glyph for a repository and another for a plain folder: the whole
        // distinction lives in the shape, so it has to be spelled out.
        const icon = el("span", "dir-icon", e.isRepo ? "\u25c6" : "\u25b8");
        icon.setAttribute("role", "img");
        icon.setAttribute("aria-label", e.isRepo ? "Git repository" : "Folder");
        row.append(describe(icon, e.isRepo ? TIPS.repoFolder : TIPS.plainFolder));
        row.append(el("span", "dir-name", e.name));
        if (e.isRepo) row.append(el("span", "dir-repo", "git"));
        const openBtn = el("button", "chip", "Open");
        openBtn.onclick = (ev) => { ev.stopPropagation(); send({ cmd: "openProject", path: e.path }); closeOverlay(); };
        row.append(openBtn);
        row.onclick = () => send({ cmd: "browse", path: e.path });
        list.append(row);
      });
    }
    wrap.append(list);
    return wrap;
  }

  // ------------------------------------------------------------- pane git

  /** renderPaneGit shows whether the pane's checkout has uncommitted work and
   *  how it stands against upstream, which is the thing you want to know
   *  before letting an agent loose in it. */
  function renderPaneGit(p, v) {
    p.git.textContent = "";
    const changed = (v.dirty || 0) + (v.untracked || 0);
    if (changed) p.git.append(mark("dirty", "●", changed));
    if (v.ahead) p.git.append(mark("ahead", "↑", v.ahead));
    if (v.behind) p.git.append(mark("behind", "↓", v.behind));
    // One description for the whole row, covering every marker showing in it.
    // Describing only the dirty count and clearing the tip otherwise left the
    // arrows unexplained, and wiped the explanation off a clean-but-ahead
    // checkout — the case where the arrows are all there is to see.
    const parts = [];
    if (v.dirty) parts.push(v.dirty + " changed. " + TIPS.changed);
    if (v.untracked) parts.push(v.untracked + " untracked. " + TIPS.untracked);
    if (v.ahead) parts.push(v.ahead + " ahead of upstream. " + TIPS.ahead);
    if (v.behind) parts.push(v.behind + " behind upstream. " + TIPS.behind);
    if (!parts.length) {
      // Nothing is drawn, so there is nothing to hover and nothing to announce.
      p.git.removeAttribute("role");
      p.git.removeAttribute("aria-label");
      delete p.git.dataset.tip;
      return;
    }
    const text = parts.join(" ");
    p.git.setAttribute("role", "img");
    p.git.setAttribute("aria-label", text);
    describe(p.git, text);
  }

  /** mark is one git marker: a glyph the row's description explains, and a
   *  count that reads as text on its own. */
  function mark(cls, sym, n) {
    const span = el("span", cls);
    span.append(glyph(sym), document.createTextNode(String(n)));
    return span;
  }

  // -------------------------------------------------------------- font size

  function setFontSize(px) {
    fontSize = Math.max(8, Math.min(28, px));
    try { localStorage.setItem("fontSize", String(fontSize)); } catch {}
    for (const p of panes.values()) {
      p.term.options.fontSize = fontSize;
      scheduleFit(p);
    }
    notice("Font size " + fontSize + "px", false);
  }

  // ----------------------------------------------------------------- search

  /** The pane the find bar was opened on. It is not always the focused one by
   *  the time a search runs: clicking into another pane while the bar is up
   *  moves the focus, and a search that followed it would jump to a pane the
   *  user was not looking at and leave the first one marked for good, since
   *  only the focused pane's marks were ever cleared. */
  let searchPane = "";

  function openSearch() {
    searchPane = focusedPaneId();
    $("searchbar").hidden = false;
    // With six panes on screen, "Find" alone does not say where it is looking.
    const v = state && state.panes ? state.panes[searchPane] : null;
    $("search-label").textContent = v && v.name ? "Find in " + v.name : "Find";
    const input = $("search-input");
    input.value = "";
    noMatch(false);
    input.focus();
  }
  function closeSearch() {
    $("searchbar").hidden = true;
    const p = panes.get(searchPane);
    if (p && p.search) { try { p.search.clearDecorations(); } catch {} }
    searchPane = "";
    focusTerminal();
  }
  function runSearch(back) {
    const p = panes.get(searchPane);
    const q = $("search-input").value;
    if (!p || !p.search || !q) { noMatch(false); return; }
    const opts = { decorations: { activeMatchColorOverrideColor: "#4c9aff", matchOverviewRuler: "#4c9aff" } };
    let found = false;
    try {
      found = back ? p.search.findPrevious(q, opts) : p.search.findNext(q, opts);
    } catch { /* the addon is optional */ }
    // Nothing else changes when a search fails — the terminal sits where it
    // was — so without this, pressing Enter on a word that is not there looks
    // exactly like pressing Enter on one that is.
    noMatch(!found);
  }
  function noMatch(on) {
    const input = $("search-input");
    input.classList.toggle("nomatch", on);
    input.setAttribute("aria-invalid", String(on));
  }

  // --------------------------------------------------------- notifications

  /** Remembering the last status per pane is what makes it possible to notify
   *  on the transition into "waiting" rather than repeatedly while it stays
   *  there. */
  const lastStatus = new Map();

  function notifyAttention(s) {
    for (const [id, v] of Object.entries(s.panes || {})) {
      const before = lastStatus.get(id);
      lastStatus.set(id, v.status);
      if (v.status !== "waiting" || before === "waiting" || before === undefined) continue;
      // Only when the window is not in front: otherwise the tab marker is
      // enough and a toast would be noise.
      if (document.hasFocus() && !document.hidden) continue;
      showNotification(v.name + " needs you", (v.branch ? v.branch + " — " : "") + "waiting for input");
    }
    for (const id of [...lastStatus.keys()]) {
      if (!s.panes || !s.panes[id]) lastStatus.delete(id);
    }
  }

  function showNotification(title, body) {
    if (!("Notification" in window) || Notification.permission !== "granted") return;
    try {
      const n = new Notification(title, { body, tag: "perch" });
      n.onclick = () => { window.focus(); n.close(); };
    } catch { /* notifications are best effort */ }
  }

  function askForNotifications() {
    if (!("Notification" in window) || Notification.permission !== "default") return;
    try { Notification.requestPermission(); } catch {}
  }

  // --------------------------------------------------------------- actions

  /* Every action the interface offers has exactly one implementation, here,
   * keyed by the id the Go side gives it. The palette lists them, the keyboard
   * dispatches to them and the help pages describe them, all from the one
   * table that arrives with the hello — so a binding cannot be changed in one
   * of the three and left stale in the other two.
   */
  const ACTIONS = {
    splitRight: () => send({ cmd: "splitPane", id: focusedPaneId(), dir: "h", kind: "claude" }),
    splitDown: () => send({ cmd: "splitPane", id: focusedPaneId(), dir: "v", kind: "claude" }),
    splitRightShell: () => send({ cmd: "splitPane", id: focusedPaneId(), dir: "h", kind: "shell" }),
    movePaneLeft: () => send({ cmd: "movePaneDir", dir: "left" }),
    movePaneRight: () => send({ cmd: "movePaneDir", dir: "right" }),
    movePaneUp: () => send({ cmd: "movePaneDir", dir: "up" }),
    movePaneDown: () => send({ cmd: "movePaneDir", dir: "down" }),
    movePaneToNewTab: () => send({ cmd: "movePaneToNewTab", id: focusedPaneId() }),
    zoomPane: () => send({ cmd: "toggleZoom", id: focusedPaneId() }),
    restartPane: () => send({ cmd: "restartPane", id: focusedPaneId() }),
    closePane: () => send({ cmd: "closePane", id: focusedPaneId() }),

    newAgentTab: () => send({ cmd: "newTab", kind: "claude" }),
    newShellTab: () => send({ cmd: "newTab", kind: "shell" }),
    nextTab: () => send({ cmd: "nextTab" }),
    prevTab: () => send({ cmd: "prevTab" }),
    // The only action that takes an argument: which tab Alt+n named.
    selectTab: (n) => {
      const tab = state && state.tabs[Number(n) - 1];
      if (tab) send({ cmd: "selectTab", id: tab.id });
    },
    mergeAllTabs: () => send({ cmd: "mergeAllTabs", id: state ? state.activeTab : "", dir: "h" }),

    toggleBroadcast: () => send({ cmd: "toggleBroadcast" }),
    promptAll: () => openPrompt(),
    fanout: () => openFanout(),
    agents: () => openAgents(),

    worktrees: () => send({ cmd: "worktrees" }),
    changes: () => openChanges(),

    palette: () => openPalette(),
    findInTerminal: () => openSearch(),
    history: () => openHistory(),
    projects: () => openProjects(),
    help: () => openHelp(),

    fontUp: () => setFontSize(fontSize + 1),
    fontDown: () => setFontSize(fontSize - 1),
    fontReset: () => setFontSize(13),
    detach: () => send({ cmd: "detach" }),
    quit: () => send({ cmd: "quit" }),
  };

  /** applyHello takes the action table and the preferences, which arrive
   *  together and before the first state, because the palette and the
   *  first-run hints are drawn from them. */
  function applyHello(msg) {
    keyTable = msg.keys || [];
    prefs = msg.prefs || prefs;
    bindings = new Map();
    keyTable.forEach((k) => {
      const s = signatureOf(k.keys);
      if (s) bindings.set(s, k.id);
    });
    describeChrome();
    renderHints();
    // A terminal in a box does not advertise what is around it, and the
    // alternative to opening the help once is finding it by accident.
    if (!prefs.helpSeen) openHelp("getting-started");
  }

  /** describeChrome labels the buttons that never change with the action they
   *  run and the key that runs it. Written into the page they would be one
   *  more copy of the bindings to go stale, so they are filled in from the
   *  table instead, once it has arrived. */
  function describeChrome() {
    const label = (id, node, extra) => {
      if (node && keyTable.some((k) => k.id === id)) describe(node, actionTip(id, extra));
    };
    // The project switcher is not in this list: its bubble names the project
    // as well, so renderProjectChip writes it as the project changes.
    label("newAgentTab", $("new-tab"), "or drop a pane here to give it a tab of its own");
    label("agents", $("summary"));
    label("toggleBroadcast", $("btn-broadcast"));
    label("changes", $("btn-changes"));
    label("history", $("btn-history"));
    label("worktrees", $("btn-worktrees"));
    label("help", $("btn-help"));
  }

  /** actionTip is the copy for a control that runs an action: what it does and
   *  the key that does it, both taken from the table, and whatever else this
   *  particular control has to say. */
  function actionTip(id, extra) {
    const k = keyTable.find((x) => x.id === id);
    if (!k) return extra || "";
    return k.label + (k.keys ? " (" + k.keys + ")" : "") + (extra ? " — " + extra : "");
  }

  /** runAction runs one action by id, asking first where the table says the
   *  action is worth asking about. */
  function runAction(id, arg) {
    const fn = ACTIONS[id];
    if (!fn) return;
    const k = keyTable.find((x) => x.id === id);
    if (k && k.confirm && !window.confirm(k.confirm)) return;
    fn(arg);
  }

  const KEY_NAMES = { "←": "arrowleft", "→": "arrowright", "↑": "arrowup", "↓": "arrowdown" };

  /** signatureOf turns a binding as it is written for a reader into the shape
   *  a keydown produces. A binding naming a range — Alt+1 … Alt+9 — has no
   *  single signature and is dispatched by its own branch instead. */
  function signatureOf(keys) {
    if (!keys || keys.includes("…")) return "";
    const parts = keys.split("+").map((p) => p.trim()).filter(Boolean);
    const name = parts.pop();
    const mods = { ctrl: false, shift: false, alt: false };
    for (const p of parts) {
      const m = p.toLowerCase();
      if (m !== "ctrl" && m !== "shift" && m !== "alt") return "";
      mods[m] = true;
    }
    return signature(mods.ctrl, mods.shift, mods.alt, KEY_NAMES[name] || name.toLowerCase());
  }

  function signature(ctrl, shift, alt, key) {
    return (ctrl ? "c" : "") + (shift ? "s" : "") + (alt ? "a" : "") + ":" + key;
  }

  /** actionFor reports which action a keydown is, if it is one. */
  function actionFor(e) {
    let key = (e.key || "").toLowerCase();
    // Ctrl+= and Ctrl++ are the same gesture on most layouts.
    if (key === "+") key = "=";
    return bindings.get(signature(e.ctrlKey, e.shiftKey, e.altKey, key)) || "";
  }

  // -------------------------------------------------------- command palette

  let palItems = [];
  let palIndex = 0;
  /** The rows on screen, so moving the highlight can move the highlight
   *  rather than building the list again. */
  let palRows = [];

  function paletteCommands() {
    const s = state || {};
    const id = focusedPaneId();
    // The fixed commands are the action table, in the order it gives them.
    // Anything the table names but the front end has not implemented is left
    // out rather than offered and found dead; a test keeps the two level.
    const cmds = keyTable
      .filter((k) => !k.noPalette && ACTIONS[k.id])
      .map((k) => ({ label: k.label, hint: k.keys, run: () => runAction(k.id) }));
    (s.projects || []).forEach((p) => {
      if (p.active) return;
      cmds.push({ label: "Switch to project: " + p.name, hint: p.root, run: () => send({ cmd: "selectProject", root: p.root }) });
      if (!p.active) {
        cmds.push({ label: "Split into project: " + p.name, hint: p.root, run: () => send({ cmd: "splitPane", dir: "h", root: p.root }) });
      }
    });
    (s.tabs || []).forEach((t) => {
      if (t.id === s.activeTab) return;
      cmds.push({ label: "Go to tab: " + t.title, run: () => send({ cmd: "selectTab", id: t.id }) });
      cmds.push({
        label: "Merge tab into this one: " + t.title,
        run: () => send({ cmd: "mergeTab", id: t.id, target: s.activeTab, dir: "h" }),
      });
      if (id) {
        cmds.push({
          label: "Move this pane to tab: " + t.title,
          run: () => send({ cmd: "movePaneToTab", id, target: t.id }),
        });
      }
    });
    return cmds;
  }

  function openPalette() {
    $("palette").hidden = false;
    const input = $("palette-input");
    input.value = "";
    palIndex = 0;
    renderPalette();
    input.focus();
  }
  function closePalette() {
    $("palette").hidden = true;
    focusTerminal();
  }
  function renderPalette() {
    const q = $("palette-input").value.trim().toLowerCase();
    const all = paletteCommands();
    palItems = q
      ? all.filter((c) => q.split(/\s+/).every((w) => c.label.toLowerCase().includes(w)))
      : all;
    if (palIndex >= palItems.length) palIndex = Math.max(0, palItems.length - 1);

    const list = $("palette-list");
    list.textContent = "";
    palRows = [];
    if (!palItems.length) {
      list.append(el("div", "pal-empty", "No matching command."));
      markPaletteRow();
      return;
    }
    palItems.forEach((c, i) => {
      const row = el("div", "pal-row");
      row.id = "pal-row-" + i;
      row.setAttribute("role", "option");
      row.append(el("span", "pal-label", c.label));
      if (c.hint) row.append(el("span", "pal-hint", c.hint));
      row.onmouseenter = () => selectPaletteRow(i);
      row.onclick = () => { closePalette(); c.run(); };
      palRows.push(row);
      list.append(row);
    });
    markPaletteRow();
  }

  /** selectPaletteRow moves the highlight. Building the list again to move it
   *  destroyed the row the pointer was resting on, so a mouse crossing the
   *  palette rebuilt every row in it for each row it passed; and forty rows
   *  were assembled for each press of an arrow key. */
  function selectPaletteRow(i) {
    if (i === palIndex || i < 0 || i >= palRows.length) return;
    palIndex = i;
    markPaletteRow();
  }

  function markPaletteRow() {
    palRows.forEach((row, i) => {
      const on = i === palIndex;
      row.classList.toggle("sel", on);
      row.setAttribute("aria-selected", String(on));
    });
    const sel = palRows[palIndex];
    // The keyboard never leaves the field while the list is being walked, so
    // it is the field that has to name the command currently picked out; a
    // colour on a row says nothing to anyone who cannot see it.
    const input = $("palette-input");
    if (!sel) { input.removeAttribute("aria-activedescendant"); return; }
    input.setAttribute("aria-activedescendant", sel.id);
    // The list is taller than the box it is in, so the highlight has to be
    // brought along or arrowing down walks it off the bottom and out of sight.
    sel.scrollIntoView({ block: "nearest" });
  }
  function paletteKey(e) {
    if (e.key === "Escape") { e.preventDefault(); closePalette(); return; }
    if (e.key === "ArrowDown") { e.preventDefault(); selectPaletteRow(Math.min(palIndex + 1, palRows.length - 1)); return; }
    if (e.key === "ArrowUp") { e.preventDefault(); selectPaletteRow(Math.max(palIndex - 1, 0)); return; }
    if (e.key === "Enter") {
      e.preventDefault();
      const c = palItems[palIndex];
      closePalette();
      if (c) c.run();
    }
  }

  // --------------------------------------------------------------- history

  let history = null;
  let detaching = false;

  function openHistory() {
    dialog = "history";
    openOverlay("Conversations", "history");
    $("overlay-body").textContent = "";
    $("overlay-body").append(el("div", "dir-empty", "Reading transcripts…"));
    send({ cmd: "conversations" });
  }

  /** renderHistory lists the project's stored Claude conversations so any of
   *  them can be picked up again, not just the ones a pane is attached to. */
  function renderHistory(msg) {
    if (msg) history = msg;
    if (dialog !== "history") {
      dialog = "history";
      openOverlay("Conversations", "history");
    }
    const m = history || {};
    const body = $("overlay-body");
    body.textContent = "";

    if (m.error) { body.append(el("p", null, m.error)); return; }
    if (!m.items || !m.items.length) {
      body.append(el("div", "dir-empty", "No stored conversations for this project yet."));
      return;
    }

    const wrap = section("Resume a conversation");
    m.items.forEach((c) => {
      const row = el("div", "conv-row" + (c.open ? " open" : ""));
      const main = el("div", "conv-main");
      main.append(el("div", "conv-summary", c.summary));
      const meta = el("div", "conv-meta");
      meta.append(el("span", null, c.ago));
      meta.append(el("span", null, c.messages + " entries"));
      if (c.kb) meta.append(el("span", null, c.kb + " KB"));
      meta.append(el("span", "conv-id", c.id.slice(0, 8)));
      if (c.open) meta.append(el("span", null, "already open"));
      main.append(meta);
      row.append(main);

      if (!c.open) {
        const open = el("button", "chip primary", "Resume");
        open.onclick = (ev) => {
          ev.stopPropagation();
          send({ cmd: "resumeConversation", id: c.id, path: m.cwd, text: c.summary });
          closeOverlay();
        };
        row.append(open);
        row.onclick = () => open.onclick(new Event("click"));
      }
      wrap.append(row);
    });
    body.append(wrap);

    const tools = el("div", "wt-tools");
    const refresh = el("button", "chip", "Refresh");
    refresh.onclick = () => send({ cmd: "conversations" });
    tools.append(refresh, el("span", "wt-root", m.cwd || ""));
    body.append(tools);
  }

  // ---------------------------------------------------------------- review

  let changes = null;
  let selectedFile = null;
  let diffText = "";
  /** The two parts of the dialog that change on their own: the file rows, so
   *  the chosen one can be marked, and the panel the diff is drawn in. Picking
   *  a file and the diff coming back leave the rest of the page alone — above
   *  all the commit message, which is being typed into. */
  let changeView = null;

  /** openChanges reviews a working tree: which files an agent touched, what it
   *  did to them, and committing or pushing the result without dropping to a
   *  shell. */
  function openChanges(path) {
    dialog = "changes";
    changes = null;
    selectedFile = null;
    diffText = "";
    openOverlay("Changes", "changes");
    $("overlay-body").textContent = "";
    $("overlay-body").append(el("div", "dir-empty", "Reading the working tree…"));
    send({ cmd: "changes", path: path || "" });
  }

  function renderChanges(msg) {
    changeView = null;
    if (msg) {
      changes = msg;
      // Keep the selection if that file is still in the list.
      if (selectedFile && !(msg.files || []).some((f) => f.path === selectedFile)) {
        selectedFile = null;
        diffText = "";
      }
    }
    if (dialog !== "changes") { dialog = "changes"; openOverlay("Changes", "changes"); }
    const m = changes || {};
    const body = $("overlay-body");
    body.textContent = "";

    if (m.error) { body.append(el("p", null, m.error)); return; }

    // --- branch and remote state ------------------------------------------
    const head = el("div", "rev-head");
    head.append(describe(el("span", "rev-branch", m.branch || "detached"), TIPS.branch));
    if (m.upstream) {
      let track = m.upstream;
      if (m.ahead) track += "  ↑" + m.ahead;
      if (m.behind) track += "  ↓" + m.behind;
      // One span carries the upstream and both arrows, so its copy is
      // assembled from the same wording those arrows use everywhere else.
      const parts = [];
      const label = ["Tracking " + m.upstream];
      if (m.ahead) {
        parts.push(TIPS.ahead);
        label.push(m.ahead + (m.ahead === 1 ? " commit ahead" : " commits ahead"));
      }
      if (m.behind) {
        parts.push(TIPS.behind);
        label.push(m.behind + (m.behind === 1 ? " commit behind" : " commits behind"));
      }
      const tr = el("span", "rev-track", track);
      tr.setAttribute("role", "img");
      tr.setAttribute("aria-label", label.join(", "));
      head.append(describe(tr, parts.length
        ? "Tracking " + m.upstream + ". " + parts.join(" ")
        : "Tracking " + m.upstream + ", and level with it."));
    } else if (m.hasRemote) {
      head.append(describe(el("span", "rev-track", "no upstream yet"),
        "This branch has no upstream on the remote. Push sets one up."));
    }

    const actions = el("div", "rev-actions");
    if (m.hasRemote) {
      const fetch = el("button", "chip", "Fetch");
      fetch.onclick = () => send({ cmd: "gitFetch", path: m.cwd });
      actions.append(fetch);
      if (m.behind) {
        const pull = el("button", "chip", "Pull " + m.behind);
        pull.title = "Fast-forward from " + m.upstream;
        pull.onclick = () => send({ cmd: "gitPull", path: m.cwd });
        actions.append(pull);
      }
      const push = el("button", "chip" + (m.ahead ? " primary" : ""), m.ahead ? "Push " + m.ahead : "Push");
      push.title = m.upstream ? "Push to " + m.upstream : "Push and set the upstream to origin";
      push.onclick = () => send({ cmd: "gitPush", path: m.cwd });
      actions.append(push);
    }
    const refresh = el("button", "chip", "Refresh");
    refresh.onclick = () => send({ cmd: "changes", path: m.cwd });
    actions.append(refresh);
    head.append(actions);
    body.append(head);

    const files = m.files || [];
    if (!files.length) {
      body.append(el("div", "dir-empty", "Nothing has changed in this working tree."));
    } else {
      // --- file list and diff ---------------------------------------------
      const split = el("div", "rev-body");
      const list = el("div", "rev-files");
      const rows = new Map();
      files.forEach((f) => {
        const row = el("div", "rev-file" + (f.path === selectedFile ? " sel" : ""));
        row.append(el("span", "rev-kind", f.label));
        // Long paths are elided from the left, keeping the file name visible.
        const name = el("span", "rev-name", f.path);
        name.title = f.path;
        row.append(name);
        // Lines, not files: a different count from the ones on the
        // worktree rows, so it says so rather than borrowing that copy.
        if (f.added) {
          const n = el("span", "rev-plus", "+" + f.added);
          n.setAttribute("role", "img");
          n.setAttribute("aria-label", f.added + (f.added === 1 ? " line added" : " lines added"));
          row.append(describe(n, "Lines added to this file since the last commit."));
        }
        if (f.removed) {
          const n = el("span", "rev-minus", "−" + f.removed);
          n.setAttribute("role", "img");
          n.setAttribute("aria-label", f.removed + (f.removed === 1 ? " line removed" : " lines removed"));
          row.append(describe(n, "Lines removed from this file since the last commit."));
        }
        rowAction(row, () => selectChangedFile(f.path, m.cwd));
        rows.set(f.path, row);
        list.append(row);
      });
      split.append(list);

      const diff = el("div", "rev-diff");
      split.append(diff);
      body.append(split);
      changeView = { rows, diff };
      fillDiff();

      // --- commit -----------------------------------------------------------
      const commit = el("div", "rev-commit");
      const box = el("textarea");
      box.id = "commit-message";
      box.placeholder = "Commit message";
      box.value = commitDraft;
      box.oninput = () => { commitDraft = box.value; };
      const buttons = el("div", "rev-commit-buttons");
      const doCommit = (push) => {
        const message = box.value.trim();
        if (!message) { notice("A commit message is required", true); box.focus(); return; }
        send({ cmd: "commit", path: m.cwd, text: message, push });
        commitDraft = "";
      };
      const c1 = el("button", "chip primary", "Commit " + files.length + " file" + (files.length === 1 ? "" : "s"));
      c1.onclick = () => doCommit(false);
      buttons.append(c1);
      if (m.hasRemote) {
        const c2 = el("button", "chip", "Commit and push");
        c2.onclick = () => doCommit(true);
        buttons.append(c2);
      }
      commit.append(box, buttons);
      body.append(commit);
    }

    const tools = el("div", "wt-tools");
    tools.append(el("span", "wt-root", m.cwd || ""));
    body.append(tools);
  }

  let commitDraft = "";

  /** selectChangedFile shows one file's diff. Only the marking on the rows and
   *  the diff panel change: rebuilding the dialog would take the commit box
   *  away from under the caret, and the message half-written in it with the
   *  caret. */
  function selectChangedFile(path, cwd) {
    if (selectedFile === path) return;
    selectedFile = path;
    diffText = "";
    markSelectedFile();
    fillDiff();
    send({ cmd: "diff", path: cwd, text: path });
  }

  function markSelectedFile() {
    if (!changeView) return;
    for (const [path, row] of changeView.rows) {
      const on = path === selectedFile;
      row.classList.toggle("sel", on);
      // The diff beside the list is of this row, which the colouring says to
      // the eye and nothing said otherwise.
      row.setAttribute("aria-current", String(on));
    }
  }

  /** fillDiff draws whatever the diff panel should be showing now. */
  function fillDiff() {
    if (!changeView) return;
    const diff = changeView.diff;
    diff.textContent = "";
    if (!selectedFile) diff.append(el("span", "meta", "Select a file to see what changed."));
    else if (!diffText) diff.append(el("span", "meta", "Loading diff…"));
    else renderDiffInto(diff, diffText);
    diff.scrollTop = 0;
  }

  function showDiff(msg) {
    if (msg.file !== selectedFile) return; // a stale reply for another file
    diffText = msg.error ? msg.error : msg.text;
    if (changeView) fillDiff();
    else renderChanges();
  }

  /** renderDiffInto colours a unified diff without a syntax highlighter. */
  function renderDiffInto(host, text) {
    text.split("\n").forEach((line) => {
      let cls = "";
      if (line.startsWith("+++") || line.startsWith("---")) cls = "meta";
      else if (line.startsWith("@@")) cls = "hunk";
      else if (line.startsWith("+")) cls = "add";
      else if (line.startsWith("-")) cls = "del";
      else if (line.startsWith("diff ") || line.startsWith("index ")) cls = "meta";
      host.append(el("div", cls, line || " "));
    });
  }

  // ---------------------------------------------------------------- agents

  let agents = null;

  /** openAgents lists every pane in every open project. The tab bar only shows
   *  the active project, so this is what answers "where is the one that needs
   *  me" once several are open. */
  function openAgents() {
    dialog = "agents";
    openOverlay("Agents", "status");
    $("overlay-body").textContent = "";
    $("overlay-body").append(el("div", "dir-empty", "Loading…"));
    send({ cmd: "agents" });
  }

  function renderAgents(msg) {
    if (msg) agents = msg;
    if (dialog !== "agents") { dialog = "agents"; openOverlay("Agents", "status"); }
    const m = agents || {};
    const body = $("overlay-body");
    body.textContent = "";

    const items = m.items || [];
    if (!items.length) {
      body.append(el("div", "dir-empty", "No panes open."));
      return;
    }

    const wrap = section(items.length + (items.length === 1 ? " pane" : " panes"));
    items.forEach((a) => {
      const row = el("div", "agent-row" + (a.active ? " active" : ""));
      const main = el("div", "agent-main");

      const title = el("div", "agent-title");
      // The dot is the only thing carrying status here, and it has no text
      // at all, so it needs both the copy and a name of its own.
      // The row writes the status out in words further along, so the disc is
      // decoration here — unlike in a pane header, where it is all there is.
      const dot = el("span", "dot " + a.status);
      dot.setAttribute("aria-hidden", "true");
      title.append(describe(dot, TIPS[a.status] || a.status));
      title.append(el("span", "agent-tab", a.tab || a.name));
      title.append(el("span", "agent-project", a.project));
      main.append(title);

      const meta = el("div", "agent-meta");
      if (a.branch) {
        const n = el("span", null, "⎇ " + a.branch);
        n.setAttribute("role", "img");
        n.setAttribute("aria-label", "On branch " + a.branch);
        meta.append(describe(n, TIPS.branch));
      }
      if (a.dirty) {
        const n = el("span", null, "●" + a.dirty + " uncommitted");
        n.setAttribute("role", "img");
        n.setAttribute("aria-label", a.dirty + (a.dirty === 1 ? " file" : " files") + " changed but not committed");
        meta.append(describe(n, TIPS.changed));
      }
      if (a.kind === "shell") meta.append(el("span", null, "shell"));
      if (a.detail) meta.append(el("span", null, a.detail));
      main.append(meta);
      row.append(main);

      const status = el("span", "agent-status " + a.status, a.status);
      row.append(status);
      if (a.for) row.append(el("span", "agent-for", "for " + a.for));

      rowAction(row, () => {
        send({ cmd: "revealPane", root: a.root, node: a.tabId, id: a.paneId });
        closeOverlay();
      });
      wrap.append(row);
    });
    body.append(wrap);

    const tools = el("div", "wt-tools");
    const refresh = el("button", "chip", "Refresh");
    refresh.onclick = () => send({ cmd: "agents" });
    tools.append(refresh);
    body.append(tools);
  }

  // --------------------------------------------------------------- fan out

  let fanout = null;

  /** openFanout turns what one agent proposed into a set of agents that do it.
   *
   *  The tasks are only ever a suggestion: they are extracted from the pane's
   *  output, shown in an editable box, and nothing starts until the user says
   *  so. */
  function openFanout(paneID) {
    dialog = "fanout";
    fanout = null;
    openOverlay("Fan out", "fanout");
    $("overlay-body").textContent = "";
    $("overlay-body").append(el("div", "dir-empty", "Reading this pane's output…"));
    send({ cmd: "fanoutPreview", id: paneID || focusedPaneId() });
  }

  function renderFanout(msg) {
    if (msg) fanout = msg;
    if (dialog !== "fanout") { dialog = "fanout"; openOverlay("Fan out", "fanout"); }
    const m = fanout || {};
    const body = $("overlay-body");
    body.textContent = "";

    const hint = el("div", "fan-hint");
    const found = m.tasks && m.tasks.length;
    hint.append(document.createTextNode(
      found
        ? (m.fromReply ? "Found these in what this agent last said. " : "Found these in the pane's output. ") +
          "Edit the list — one task per line — then start an agent for each."
        : "No plan was found in this pane. Type one task per line."));
    body.append(hint);

    const box = el("textarea", "fan-tasks");
    box.value = (m.tasks || []).join("\n");
    box.placeholder = "One task per line, for example:\nAdd a health endpoint\nWrite tests for the parser";
    body.append(box);

    const opts = el("div", "fan-opts");
    const wt = el("label", "fan-opt");
    const wtBox = el("input");
    wtBox.type = "checkbox";
    wtBox.checked = !!m.isRepo;
    wtBox.disabled = !m.isRepo;
    wt.append(wtBox, document.createTextNode(
      m.isRepo ? "Give each agent its own git worktree" : "Not a git repository — agents share this directory"));
    opts.append(wt);

    const sp = el("label", "fan-opt");
    const spBox = el("input");
    spBox.type = "checkbox";
    sp.append(spBox, document.createTextNode("Split into this tab instead of new tabs"));
    opts.append(sp);

    // Each worktree is a directory Claude has not seen, so it would stop and
    // ask whether the folder is trusted before doing any work. Offer to carry
    // over the answer already given for this project — never silently.
    const tr = el("label", "fan-opt");
    const trBox = el("input");
    trBox.type = "checkbox";
    trBox.checked = !!m.trusted;
    trBox.disabled = !m.trusted;
    tr.append(trBox, document.createTextNode(
      m.trusted
        ? "Trust the new worktrees, as " + (m.project || "this project") + " already is"
        : "This project is not trusted in Claude Code, so each agent will ask"));
    tr.title = m.trusted
      ? "Without this, every new worktree stops on Claude Code's folder-trust question"
      : "";
    opts.append(tr);
    body.append(opts);

    const go = el("div", "fan-go");
    const count = el("span", "fan-count");
    const taskLines = () => box.value.split("\n").map((l) => l.trim()).filter(Boolean);
    const updateCount = () => {
      const n = taskLines().length;
      count.textContent = n === 1 ? "1 agent" : n + " agents";
      start.disabled = n === 0;
      start.textContent = n === 1 ? "Start 1 agent" : "Start " + n + " agents";
    };
    const start = el("button", "chip primary", "Start agents");
    start.onclick = () => {
      const tasks = taskLines();
      if (!tasks.length) return;
      send({
        cmd: "fanout",
        id: m.paneId,
        tasks,
        worktrees: wtBox.checked && !wtBox.disabled,
        split: spBox.checked,
        trust: trBox.checked && !trBox.disabled,
      });
      closeOverlay();
    };
    box.oninput = updateCount;
    go.append(start, count);
    body.append(go);
    updateCount();

    const tools = el("div", "wt-tools");
    tools.append(el("span", "wt-root", m.cwd || ""));
    body.append(tools);
    box.focus();
  }

  // ----------------------------------------------------------------- hints

  /* One line under the tab bar, at most, and only for something the interface
   * cannot say for itself — a gesture with nothing on screen to suggest it.
   * Each is dismissed for good, and remembered on the Go side: a hint that
   * comes back after being sent away is worse than one never shown.
   *
   * The bindings are not written out here either. A hint names the action and
   * the key is filled in from the same table as everything else.
   */
  const HINTS = [
    {
      id: "palette",
      action: "palette",
      text: "opens the command palette — every action in the application, searchable.",
      page: "shortcuts",
      when: () => true,
    },
    {
      id: "drag-panes",
      text: "Panes are dragged by their header: onto another pane's edge, into another tab, or onto + for a tab of its own. Nothing restarts.",
      page: "rearranging",
      when: (s) => paneCount(s) > 1,
    },
    {
      id: "waiting",
      text: "An amber dot means that agent is waiting on you — a permission prompt, or a question.",
      page: "status",
      when: (s) => s.waiting > 0,
    },
    {
      id: "worktrees",
      action: "worktrees",
      text: "gives each agent its own checkout and branch, which is what keeps several of them from fighting over one working tree.",
      page: "worktrees",
      when: (s) => paneCount(s) > 2,
    },
  ];

  function dismissedHint(id) { return (prefs.dismissedTips || []).includes(id); }
  function paneCount(s) { return Object.keys((s && s.panes) || {}).length; }

  function renderHints() {
    const bar = $("hints");
    if (!bar) return;
    const hint = state ? HINTS.find((h) => !dismissedHint(h.id) && h.when(state)) : null;
    if (!hint) {
      bar.hidden = true;
      bar.textContent = "";
      bar.dataset.hint = "";
      return;
    }
    if (bar.dataset.hint === hint.id) return;
    bar.dataset.hint = hint.id;
    bar.textContent = "";

    const line = el("div", "hint-text");
    const k = hint.action ? keyTable.find((x) => x.id === hint.action) : null;
    if (k && k.keys) line.append(el("kbd", null, k.keys), document.createTextNode(" "));
    line.append(document.createTextNode(hint.text));
    bar.append(line);

    if (hint.page) {
      const more = el("button", "chip", "Show me");
      more.onclick = () => openHelp(hint.page);
      bar.append(more);
    }
    const close = el("button", "icon-btn", "×");
    describe(close, "Dismiss this hint for good");
    close.onclick = () => {
      prefs.dismissedTips = (prefs.dismissedTips || []).concat(hint.id);
      send({ cmd: "dismissTip", id: hint.id });
      renderHints();
    };
    bar.append(close);
    bar.hidden = false;
  }

  // ------------------------------------------------------------------ help

  /* The help is written as Markdown beside the Go code and rendered there, so
   * the pages arrive as HTML with their shortcuts already filled in from the
   * action table. They are fetched once: the content is compiled into the
   * binary and cannot change while it runs.
   */

  let helpPages = null;
  let helpSlug = "";
  let helpQuery = "";
  let helpError = "";
  let helpLoading = false;

  /** openHelp shows a page, and is what every `?` in the interface calls. */
  function openHelp(slug) {
    const first = dialog !== "help";
    dialog = "help";
    if (slug) {
      // A `?` somewhere in the interface asks for one page in particular, so
      // a search left over from last time must not hide it.
      helpSlug = slug;
      helpQuery = "";
      const box = $("help-search");
      if (box) box.value = "";
    }
    if (first) {
      openOverlay("Help");
      $("overlay-panel").classList.add("wide");
      // Opening it at all is what retires the first-run welcome.
      if (!prefs.helpSeen) { prefs.helpSeen = true; send({ cmd: "helpSeen" }); }
    }
    renderHelp();
    if (!helpPages && !helpError) loadHelp();
  }

  function loadHelp() {
    if (helpLoading) return;
    helpLoading = true;
    helpError = "";
    fetch("/help.json", { credentials: "same-origin" })
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error("HTTP " + r.status))))
      .then((data) => {
        helpPages = data.pages || [];
        // The search matches against the whole of every page. Folding the case
        // once here rather than on each keystroke is the difference between
        // searching a few kilobytes and rebuilding them for every character.
        helpPages.forEach((p) => { p.hay = (p.title + " " + p.text).toLowerCase(); });
        helpHitsFor = null;
      })
      .catch((err) => {
        helpError = "The help pages could not be loaded (" + err.message + ").";
      })
      .finally(() => {
        helpLoading = false;
        if (dialog === "help") renderHelp();
      });
  }

  /* The shell — the search box, the contents and the page — is built once and
   * then updated in place. Rebuilding it on every keystroke would take the
   * search box out from under the caret while it was being typed into. */
  function renderHelp() {
    if (dialog !== "help") return;
    const body = $("overlay-body");

    if (helpError || !helpPages || !helpPages.length) {
      body.textContent = "";
      body.append(el("div", "dir-empty",
        helpError || (helpPages ? "There are no help pages." : "Loading…")));
      if (helpError) {
        const again = el("button", "chip", "Try again");
        again.onclick = () => { helpError = ""; renderHelp(); loadHelp(); };
        body.append(again);
      }
      return;
    }
    if (!body.querySelector(".help")) buildHelpShell(body);
    renderHelpList();
    renderHelpContent();
  }

  function buildHelpShell(body) {
    body.textContent = "";
    const wrap = el("div", "help");

    const nav = el("div", "help-nav");
    const search = el("input", "help-search");
    search.id = "help-search";
    search.type = "search";
    search.placeholder = "Search the help";
    search.value = helpQuery;
    search.autocomplete = "off";
    search.spellcheck = false;
    search.oninput = () => { helpQuery = search.value; renderHelpList(); renderHelpContent(); };
    search.onkeydown = helpSearchKey;
    nav.append(search, el("div", "help-list"));

    const content = el("div", "help-content");
    content.id = "help-content";
    content.tabIndex = 0;
    wrap.append(nav, content);
    body.append(wrap);
    search.focus();
  }

  function renderHelpList() {
    const list = $("overlay-body").querySelector(".help-list");
    if (!list) return;
    list.textContent = "";
    const hits = helpMatches();
    if (!hits.length) {
      list.append(el("div", "help-none", "Nothing here matches."));
      return;
    }
    hits.forEach((hit) => {
      const item = el("button", "help-item" + (hit.page.slug === helpSlug ? " sel" : ""));
      item.append(el("span", "help-item-title", hit.page.title));
      item.append(el("span", "help-item-sub", hit.snippet || hit.page.summary));
      item.onclick = () => { showHelpPage(hit.page.slug); };
      list.append(item);
    });
  }

  function renderHelpContent() {
    const content = $("help-content");
    if (!content) return;
    // Searching narrows the contents; a page the search has hidden gives way
    // to the first thing that did match rather than being left on show.
    const hits = helpMatches();
    const stillListed = hits.some((h) => h.page.slug === helpSlug);
    const page = (stillListed && pageFor(helpSlug)) ||
      (hits.length ? hits[0].page : (pageFor(helpSlug) || helpPages[0]));
    if (page.slug !== helpSlug) {
      helpSlug = page.slug;
      renderHelpList();
    }
    if (content.dataset.slug === page.slug) return;
    content.dataset.slug = page.slug;
    // The pages are our own, compiled into the binary; nothing a project or an
    // agent produced reaches this.
    content.innerHTML = page.html;
    content.scrollTop = 0;
  }

  function showHelpPage(slug) {
    helpSlug = slug;
    renderHelpList();
    renderHelpContent();
  }

  function pageFor(slug) {
    return (helpPages || []).find((p) => p.slug === slug) || null;
  }

  /** helpMatches is the contents list, filtered by the search box. Each hit
   *  carries the piece of the page the words were found in, so the list
   *  answers "which page is this in" without opening each one. */
  /** The hits for the search as it currently reads. One keystroke asks for
   *  them from the contents list, from the page beside it and, on an arrow key,
   *  from the key handler as well; there is one answer between them. */
  let helpHits = null;
  let helpHitsFor = null;

  function helpMatches() {
    if (helpHitsFor === helpQuery && helpHits) return helpHits;
    const pages = helpPages || [];
    const q = helpQuery.trim().toLowerCase();
    let out;
    if (!q) {
      out = pages.map((p) => ({ page: p, snippet: "" }));
    } else {
      const words = q.split(/\s+/);
      out = [];
      pages.forEach((p) => {
        if (!words.every((w) => p.hay.includes(w))) return;
        out.push({ page: p, snippet: snippetFor(p.text, words[0]) });
      });
    }
    helpHitsFor = helpQuery;
    helpHits = out;
    return out;
  }

  /** snippetFor returns the words around the first match, so a hit shows why
   *  it is a hit. */
  function snippetFor(text, word) {
    const at = text.toLowerCase().indexOf(word);
    if (at < 0) return "";
    const from = Math.max(0, at - 40);
    return (from ? "…" : "") + text.slice(from, at + 80).trim() + "…";
  }

  function helpSearchKey(e) {
    const hits = helpMatches();
    if (!hits.length) return;
    const at = hits.findIndex((h) => h.page.slug === helpSlug);
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const next = e.key === "ArrowDown"
        ? Math.min(at + 1, hits.length - 1)
        : Math.max(at - 1, 0);
      showHelpPage(hits[Math.max(0, next)].page.slug);
    } else if (e.key === "Enter") {
      e.preventDefault();
      showHelpPage(hits[at < 0 ? 0 : at].page.slug);
    }
  }

  function notice(text, isError) {
    const n = $("notice");
    n.textContent = text;
    n.classList.toggle("error", !!isError);
    n.hidden = false;
    clearTimeout(notice.timer);
    notice.timer = setTimeout(() => { n.hidden = true; }, 4000);
  }

  // -------------------------------------------------------------- shortcuts

  window.addEventListener("keydown", (e) => {
    if (e.key === "Tab" && trapTab(e)) return;
    if (!$("palette").hidden) { paletteKey(e); return; }
    if (!$("searchbar").hidden && document.activeElement === $("search-input")) {
      if (e.key === "Escape") { e.preventDefault(); closeSearch(); return; }
      if (e.key === "Enter") { e.preventDefault(); runSearch(e.shiftKey); return; }
    }
    if (!$("overlay").hidden && e.key === "Escape") { closeOverlay(); return; }

    // Font size keeps working while the prompt bar is up, so the sentence
    // being composed can be made readable without abandoning it.
    const sizing = actionFor(e);
    if (sizing === "fontUp" || sizing === "fontDown" || sizing === "fontReset") {
      e.preventDefault();
      runAction(sizing);
      return;
    }
    if (!$("promptbar").hidden) {
      if (e.key === "Escape") { e.preventDefault(); closePrompt(); }
      else if (e.key === "Enter" && document.activeElement === $("prompt-input")) { e.preventDefault(); submitPrompt(); }
      return;
    }

    // Every other binding is dispatched from the action table, so what the
    // help says a key does is what the key does.
    const id = actionFor(e);
    if (id) { e.preventDefault(); runAction(id); return; }

    // Alt+1 … Alt+9 names a tab rather than being one binding, so it is the
    // one thing the table cannot express and this has to spell out.
    if (e.altKey && !e.ctrlKey && !e.shiftKey && e.key >= "1" && e.key <= "9") {
      e.preventDefault();
      runAction("selectTab", e.key);
    }
  }, true);

  // ------------------------------------------------------------------ wiring

  $("new-tab").onclick = () => send({ cmd: "newTab", kind: "claude" });
  wireTabStripDrops();
  wireDragSafetyNet();
  $("btn-broadcast").onclick = () => send({ cmd: "toggleBroadcast" });
  $("btn-worktrees").onclick = () => send({ cmd: "worktrees" });
  $("btn-history").onclick = openHistory;
  $("btn-changes").onclick = () => openChanges();
  $("summary").onclick = openAgents;
  $("project-btn").onclick = openProjects;
  // Bound through a closure rather than passed straight in: the click event
  // would otherwise arrive as the page to open.
  $("btn-help").onclick = () => openHelp();
  $("overlay-close").onclick = closeOverlay;
  $("overlay").addEventListener("mousedown", (e) => { if (e.target === $("overlay")) closeOverlay(); });
  $("prompt-send").onclick = submitPrompt;
  $("prompt-cancel").onclick = closePrompt;
  $("retry").onclick = connectControl;
  $("palette-input").oninput = () => { palIndex = 0; renderPalette(); };
  $("palette").addEventListener("mousedown", (e) => { if (e.target === $("palette")) closePalette(); });
  $("search-next").onclick = () => runSearch(false);
  $("search-prev").onclick = () => runSearch(true);
  $("search-close").onclick = closeSearch;
  // Asking on the first interaction rather than at load avoids a permission
  // prompt before the user has done anything.
  window.addEventListener("pointerdown", askForNotifications, { once: true });

  window.addEventListener("beforeunload", () => send({ cmd: "save" }));

  connectControl();
})();
