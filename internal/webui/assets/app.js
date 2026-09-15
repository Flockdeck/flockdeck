/* flockdeck front end.
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

  /** ICON_PATHS is the drawing for each icon in the rail, the top bar and the
   *  settings, on a 16-unit grid and stroked in the text colour. A glyph in a
   *  font is drawn however that font draws it, and several of these had no
   *  glyph that said the thing at all. */
  const ICON_PATHS = {
    broadcast: '<circle cx="8" cy="8" r="1.4"></circle><path d="M5.2 5.2a4 4 0 0 0 0 5.6M10.8 5.2a4 4 0 0 1 0 5.6M3.1 3.1a7 7 0 0 0 0 9.8M12.9 3.1a7 7 0 0 1 0 9.8"></path>',
    changes: '<path d="M4 2.5h5.2L12 5.3v8.2H4z"></path><path d="M6 7h4M8 5v4M6 11h4"></path>',
    history: '<circle cx="8" cy="8" r="5.5"></circle><path d="M8 5v3.2l2.2 1.4"></path>',
    worktrees: '<circle cx="5" cy="3.5" r="1.5"></circle><circle cx="5" cy="12.5" r="1.5"></circle><circle cx="11" cy="5" r="1.5"></circle><path d="M5 5v6M11 6.5c0 2.8-6 2.2-6 4.5"></path>',
    github: '<circle cx="5" cy="3.2" r="1.4"></circle><circle cx="5" cy="12.8" r="1.4"></circle><circle cx="11.5" cy="8" r="1.4"></circle><path d="M5 4.6v6.8"></path><path d="M5 6.6c0 3.4 2.6 1.4 6.5 1.4"></path>',
    // Two branches meeting in one, the shape a pull request itself is drawn as
    // on github.com -- not the octocat, which is a filled mark and reads as a
    // smudge stroked at 16px the way every other glyph here is drawn.
    github: '<circle cx="4.5" cy="4" r="1.5"></circle><circle cx="4.5" cy="12" r="1.5"></circle><circle cx="11.5" cy="4" r="1.5"></circle><path d="M4.5 5.5v5M11.5 5.5v1.7a2.8 2.8 0 0 1-2.8 2.8H8"></path>',
    remote: '<rect x="4.8" y="2" width="6.4" height="12" rx="1.6"></rect><path d="M7.3 11.6h1.4"></path>',
    help: '<circle cx="8" cy="8" r="6"></circle><path d="M6.3 6.4a1.8 1.8 0 1 1 2.5 1.6c-.5.2-.8.6-.8 1.1v.3"></path><path d="M8 11.3v.2"></path>',
    settings: '<path d="M2.5 4.5h6.5M12.5 4.5h1M2.5 11.5h1.5M7.5 11.5h6"></path><circle cx="10.7" cy="4.5" r="1.6"></circle><circle cx="5.7" cy="11.5" r="1.6"></circle>',
    update: '<path d="M8 2.5v7.5M4.8 7 8 10.2 11.2 7M3.5 13.3h9"></path>',
    plus: '<path d="M8 3.2v9.6M3.2 8h9.6"></path>',
    minus: '<path d="M3.5 8h9"></path>',
    chev: '<path d="M4.8 6.4 8 9.6l3.2-3.2"></path>',
    search: '<circle cx="7" cy="7" r="4.2"></circle><path d="M10.2 10.2 13.3 13.3"></path>',
    folderplus: '<path d="M8 4.5v7M4.5 8h7"></path>',
    ext: '<path d="M6.5 3.5h-3v9h9v-3M9.5 2.5h4v4M13.5 2.5 8 8"></path>',
    menu: '<path d="M2.5 4.5h11M2.5 8h11M2.5 11.5h11"></path>',
  };

  /** iconSVG is the markup for one icon. It goes in as markup because a node
   *  made in the SVG namespace needs createElementNS for every part of it. */
  function iconSVG(name, size) {
    const s = size || 16;
    return '<svg width="' + s + '" height="' + s + '" viewBox="0 0 16 16" fill="none" stroke="currentColor" ' +
      'stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" focusable="false">' +
      (ICON_PATHS[name] || "") + "</svg>";
  }

  /** iconEl is an icon as an element, hidden from a screen reader: whatever
   *  it sits in carries the words. */
  function iconEl(name, size) {
    const n = el("span", "ico");
    n.setAttribute("aria-hidden", "true");
    n.innerHTML = iconSVG(name, size);
    return n;
  }

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
    gitLate:     "Git did not finish reading this checkout in time, so its changed files and ahead and behind counts are not shown: the last ones read may be out of date. It is asked again on the next refresh. A very large checkout, or one on a slow or network drive, can do this - running git status in a terminal there shows how long it takes.",
    folderGone:  "This worktree's folder was deleted outside git, so there is nothing in it to open or review - only git's record of it is left. Prune clears that record.",
    branch:      "The branch this checkout has in its working tree.",
    project:     "The project this agent is working in. It is shown because that is not the project of the tab it is sitting on — this tab holds agents from more than one.",
    splitHere:   "Splits the focused pane and starts an agent in this project, so both projects are worked on side by side in one tab.",
    // Not what is typed into a terminal: nothing mirrors that. It is the
    // prompt bar's message that goes to the set, and this used to say
    // otherwise.
    broadcast:   "Sends what you write in the prompt bar to every pane in the broadcast set, so one instruction reaches them all. Typing in a terminal still reaches only that terminal.",
    fanOut:      "Turns the plan this agent proposed into a set of agents that carry it out, one pane each.",
    restart:     "Relaunches the process in this pane. A Claude agent resumes the same conversation.",
    zoom:        "Fills the tab with this pane. Zoom again to bring the other panes back. Double-clicking the pane's header does the same.",
    close:       "Closes this pane and stops the process running in it.",
    autoReview:  "Lets a confident, read-only command through without asking, instead of stopping for a permission prompt. It only ever says yes: anything it is not sure of still asks, exactly as before. Off by default; a pane this one starts, by fan-out or by its own spawning, starts with the same setting this pane has.",
    usage:       "What this pane is costing the machine: processor share averaged over the last few readings, and memory, across the agent's process and everything it has started.",
    agent:       "The agent running in this pane, and the model it was asked for. A pane that was given no model runs whatever the agent is already set to.",
    chooseAgent: "Asks which agent and which model, instead of starting the one this project runs by default.",
    notInstalled: "Flockdeck would run this agent, but it is not on this machine yet.",
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

  /** tipSays returns the element whose tooltip a pointer or the keyboard
   *  arriving at node asks for, or null. In the rail folded into a menu every
   *  button already shows its words beside its icon, so a bubble there says
   *  nothing new - and the one the menu's first focus raised lay over the tiles
   *  below it, hiding the projects the menu had just been opened to show. */
  function tipSays(node) {
    const found = tipFind(node);
    if (found && railMenuOpen() && found.closest("#rail")) return null;
    return found;
  }

  document.addEventListener("pointerover", (e) => {
    const node = tipSays(e.target);
    if (node && node === tipFor) return; // already up for this element
    hideTip();
    // Touch has no hover to read the delay from, and a long press there is the
    // terminal's selection gesture.
    if (node && e.pointerType !== "touch") tipTimer = setTimeout(() => showTip(node), TIP_DELAY);
  }, true);

  // Slow in, instant out - and out for anything that means the question has
  // stopped being asked: the pointer leaving the window, a click, a scroll
  // moving the target out from under the bubble, Escape.
  // Arriving from the keyboard counts as asking too. The bubble was the only
  // place most of the glyph buttons - ⑂, ⇉, ⟳, ⤢ - say what they do, and it
  // came only for a pointer: a screen reader has their names, but somebody
  // who can see and is tabbing through the window was told nothing at all.
  // Only for keyboard focus, so a click does not leave a bubble behind it.
  document.addEventListener("focusin", (e) => {
    let keyboard = true;
    try { keyboard = e.target.matches(":focus-visible"); } catch { /* no such selector here */ }
    const node = keyboard ? tipSays(e.target) : null;
    if (node && node === tipFor) return;
    hideTip();
    if (node) tipTimer = setTimeout(() => showTip(node), TIP_DELAY);
  }, true);
  document.addEventListener("focusout", hideTip, true);
  document.addEventListener("pointerout", (e) => { if (!e.relatedTarget) hideTip(); }, true);
  document.addEventListener("pointerdown", hideTip, true);
  document.addEventListener("scroll", hideTip, true);
  window.addEventListener("keydown", (e) => { if (e.key === "Escape") hideTip(); }, true);
  window.addEventListener("blur", hideTip);

  const wsBase = (location.protocol === "https:" ? "wss://" : "ws://") + location.host;
  /** basePath is the directory the page was served from, ending in a slash.
   *  Locally, and through the relay at this machine's own address, that is
   *  "/"; through the relay under a path instead -- /h/<machine>/ -- it is
   *  that prefix, and everything asked for has to be asked for there, or it
   *  reaches the relay's own routes instead. So nothing here names a path
   *  from the root: every URL is built on this. */
  const basePath = (location.pathname || "/").replace(/[^/]*$/, "");

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
  /** A path typed into the folder browser and not yet gone to. */
  let browseDraft = null;
  /** The actions this application offers, sent by the Go side on connect.
   *  The palette, the keyboard and the help pages are all drawn from it, so a
   *  binding cannot be changed in one of them and left stale in the others. */
  let keyTable = [];
  /** remoteWindow is whether this window was reached through the relay, as
   *  the hello says. Such a window is not offered what the desk alone may
   *  do: restarting onto an update, turning remote access off or on, and
   *  setting API keys or an API agent's address. The server refuses each of
   *  them from here anyway. */
  let remoteWindow = false;
  /** RELAY_DISCONNECTED is what the disconnected panel says in a window
   *  reached through the relay. Its own words - the process has stopped,
   *  start it again - are advice for somebody at the machine; from a phone or
   *  another computer it is the machine, or the relay, that is out of reach. */
  const RELAY_DISCONNECTED = "This window reaches flockdeck through the relay, and the connection was lost: " +
    "the machine flockdeck runs on may be asleep or offline, flockdeck may have stopped there, or the relay " +
    "may be out of reach. This window tries again on its own every couple of seconds, and comes back as it " +
    "was once the machine answers.";
  /** Normalised binding → action id, built from the same table. */
  let bindings = new Map();
  /** What this person has already been shown. Kept by the Go side, because a
   *  fresh port each run means the browser treats every run as a new origin
   *  and forgets anything stored here. */
  let prefs = { helpSeen: false, dismissedTips: [] };
  /** The agent catalog, which rides along on every state push: which agents
   *  there are, which of them this machine could start, and what runs when
   *  nobody chooses. Kept rather than read out of `state` each time, so the
   *  picker survives a push that arrives while it is open. */
  let catalog = { items: [], default: {} };
  /** The catalog as it was last encoded, so a status push — which arrives
   *  whenever any agent changes what it is doing — is not mistaken for the
   *  catalog changing and does not redraw the picker under the keyboard. */
  let catalogKey = "";
  /** The size the terminals are drawn at. It is kept on the Go side with the
   *  other preferences and arrives with them, before the first pane is drawn:
   *  kept in local storage, it was forgotten on every run, for the reason
   *  given for prefs above. */
  let fontSize = 13;
  /** onMac is whether this window is on a Mac. Option there is how a layout
   *  types the characters it has no key for - [ ] { } | # on German, Nordic,
   *  French and UK Macs are Option with a digit - so it cannot be the
   *  modifier for the window's own keys the way Alt is elsewhere. */
  const onMac = (() => {
    try {
      const n = window.navigator || {};
      return /Mac|iPhone|iPad|iPod/.test(n.platform || n.userAgent || "");
    } catch { return false; }
  })();

  // ---------------------------------------------------------------- control

  function connectControl() {
    clearTimeout(reconnectTimer);
    // An attempt that has been superseded is abandoned rather than left to
    // finish: pressing Reconnect twice, which is what people do to a button
    // that has not visibly worked yet, would otherwise leave the first attempt
    // connecting with its handlers live. It would then open a second control
    // connection of its own, and when it eventually failed it would put the
    // disconnected panel back over a working one and start reconnecting again.
    const abandoned = control;
    control = null; // so the close below is seen for what it is
    if (abandoned) { try { abandoned.close(); } catch { /* already gone */ } }
    const ws = new WebSocket(wsBase + basePath + "ws/control");
    control = ws;

    // Every handler asks whether this is still the connection, the way the
    // terminal streams already do. Closing the old socket above is what
    // normally settles it; this is what settles the events already in flight.
    ws.onopen = () => {
      if (control !== ws) return;
      const panel = $("disconnected");
      // The panel took the keyboard as it went up, and hiding it left the
      // keyboard on a button nobody could see: nothing typed went anywhere
      // until something was clicked.
      const held = !panel.hidden && panel.contains(document.activeElement);
      panel.hidden = true;
      if (held) giveKeyboardBack();
      // Whatever a dialog is showing was read before the connection went, and
      // the working tree it describes has had time to move on. Asking again is
      // also what releases a button left waiting for a reply that the drop
      // took with it.
      refreshDialog();
    };
    ws.onmessage = (ev) => {
      if (control !== ws) return;
      let msg;
      try { msg = JSON.parse(ev.data); } catch { return; }
      if (msg.type === "state") applyState(msg);
      else if (msg.type === "hello") applyHello(msg);
      else if (msg.type === "prefs") { prefs = msg.prefs ? keepPending(msg.prefs) : prefs; applyPrefs(); applyTheme(); renderHints(); settingsChanged(); }
      // A remap saved here, or by another window, redraws the palette, the
      // tooltips and Settings › Keybindings in one go: they all read keyTable
      // and bindings, never a binding of their own. A capture in progress is
      // not disturbed -- another window's remap landing mid-keystroke here
      // would be a strange moment for its row to redraw under the pointer.
      else if (msg.type === "keyTable") { applyKeyTable(msg.keys || []); if (!keybindEditing) settingsChanged(); }
      // A button that went with its worktree leaves the keyboard on the list
      // - the first row's first button, which removes nothing - rather than
      // out of the dialog.
      else if (msg.type === "worktrees") {
        keepFocus(() => renderWorktrees(msg), (body) => {
          const bar = body.querySelector("div.wt-actions");
          return bar && bar.querySelector("button");
        });
      }
      else if (msg.type === "recents") {
        recents = msg.items || [];
        // A forgotten project's row is gone with the keyboard on it: the ×
        // of the row now in its place, or the folder field when none is.
        const inPlace = (body) => {
          const xs = body.querySelectorAll("button.proj-forget");
          return xs[Math.min(Math.max(forgotAt, 0), xs.length - 1)] || body.querySelector("input");
        };
        if (dialog === "projects") keepFocus(renderProjects, inPlace);
      }
      else if (msg.type === "browse") { browseState = msg; browseDraft = null; if (dialog === "projects") keepFocus(renderProjects, "button.dir-into"); }
      else if (msg.type === "conversations") keepFocus(() => renderHistory(msg));
      // A commit that worked answers with a tree with nothing to commit, and
      // the message box and its buttons go with the keyboard in them: to
      // Push, which is what comes next, or Refresh where there is no remote.
      else if (msg.type === "changes") {
        keepFocus(() => renderChanges(msg), (body) => body.querySelector("#rev-push") || body.querySelector("#rev-refresh"));
      }
      else if (msg.type === "agents") keepFocus(() => renderAgents(msg));
      // A Clear pressed goes with the key it cleared; the keyboard goes to
      // that row's own button rather than out of the dialog.
      else if (msg.type === "keys") keepFocus(() => renderKeys(msg), () => keySetButton(keyActed));
      else if (msg.type === "agentAddress") addressAnswered(msg);
      // An unpaired device's row goes with its Unpair button, and the
      // keyboard with it: to Pair a device, rather than out of the dialog.
      else if (msg.type === "remoteDevices") {
        remoteRoster = msg;
        if (remoteShown()) keepFocus(renderRemote, "#remote-pair");
        // The plan the relay reports is shown in the settings' Account & plan.
        else if (dialog === "settings" && settingsSection === "plan") renderSettings();
      }
      else if (msg.type === "remotePair") {
        remotePairing = msg;
        if (remoteShown()) keepFocus(renderRemote);
        // A fresh link starts the chain; one already going, for the link it
        // replaces, carries on and finds this one in remotePairing next.
        if (msg.url && !remotePairPollTimer) remotePairPollTimer = setTimeout(remotePairPoll, REMOTE_PAIR_POLL_MS);
      }
      else if (msg.type === "remoteOutcome") {
        remoteBusy = "";
        remoteOutcome = msg;
        // A machine that has just been enrolled starts from an empty form the
        // next time it is turned off and on, not from the codes used once.
        if (msg.action === "enable" && !msg.error) remoteDraft = newRemoteDraft();
        // A move that went ahead is done with its form, and its codes.
        if (msg.action === "move" && !msg.error) remoteMoveDraft = newRemoteMoveDraft();
        if (remoteShown()) keepFocus(renderRemote);
      }
      else if (msg.type === "fanoutPreview") renderFanout(msg);
      else if (msg.type === "routes") fanoutRoutes(msg);
      else if (msg.type === "diff") showDiff(msg);
      else if (msg.type === "detached") {
        // The agents keep running; this window is no longer needed.
        detaching = true;
        window.close();
      }
      else if (msg.type === "notice") { commitAnswered(msg.error); notice(msg.text, msg.error); }
      else if (msg.type === "ghStatus") {
        ghStatus = msg;
        if (dialog === "github") keepFocus(renderGithub);
        else if (dialog === "settings" && settingsSection === "github") keepFocus(renderGhSettings);
      }
      else if (msg.type === "ghProgress") {
        if (msg.line) ghRunLines.push(msg.line);
        if (msg.code) ghLoginCode = msg.code;
        if (msg.done) {
          ghRunning = "";
          if (msg.error) notice(msg.error, true);
        }
        if (dialog === "github") keepFocus(renderGithub);
        else if (dialog === "settings" && settingsSection === "github") keepFocus(renderGhSettings);
      }
      else if (msg.type === "ghPRs") {
        ghPRList = msg;
        if (msg.cwd) ghDir = msg.cwd;
        if (dialog === "github" && ghTab === "prs" && !ghSelected) keepFocus(renderGithub);
      }
      else if (msg.type === "ghPR") {
        if (ghCreating) {
          ghCreating = false;
          if (!msg.error && msg.item) {
            ghCreateOpen = false;
            ghSelected = { kind: "prs", number: msg.item.number, item: msg.item, error: "" };
            notice("Pull request opened", false);
          } else {
            notice(msg.error || "Could not open the pull request", true);
          }
        } else if (ghSelected && ghSelected.kind === "prs") {
          ghSelected.item = msg.item;
          ghSelected.error = msg.error || "";
        }
        if (dialog === "github") keepFocus(renderGithub);
      }
      else if (msg.type === "ghIssues") {
        ghIssueList = msg;
        if (msg.cwd) ghDir = msg.cwd;
        if (dialog === "github" && ghTab === "issues" && !ghSelected) keepFocus(renderGithub);
      }
      else if (msg.type === "ghIssue") {
        if (ghCreating) {
          ghCreating = false;
          if (!msg.error && msg.item) {
            ghCreateOpen = false;
            ghSelected = { kind: "issues", number: msg.item.number, item: msg.item, error: "" };
            notice("Issue opened", false);
          } else {
            notice(msg.error || "Could not open the issue", true);
          }
        } else if (ghSelected && ghSelected.kind === "issues") {
          ghSelected.item = msg.item;
          ghSelected.error = msg.error || "";
        }
        if (dialog === "github") keepFocus(renderGithub);
      }
      else if (msg.type === "ghChecks") {
        ghChecksData = msg;
        if (msg.cwd) ghDir = msg.cwd;
        if (dialog === "github" && ghTab === "checks") keepFocus(renderGithub);
      }
    };
    ws.onclose = () => {
      if (control !== ws) return; // an attempt that was given up on
      if (detaching) return; // the window is on its way out
      // Where the keyboard was, for the first failure only: the attempts
      // after it find the panel already up with the keyboard on its button.
      if ($("disconnected").hidden) beforeDisconnect = document.activeElement;
      // Through the relay the page lives under the machine's own prefix, which
      // says so before any hello has: see RELAY_DISCONNECTED.
      if (remoteWindow || basePath !== "/") $("disconnected-desc").textContent = RELAY_DISCONNECTED;
      $("disconnected").hidden = false;
      // The title is what the taskbar shows of a window behind others, and it
      // went on counting agents waiting in a Flockdeck that had stopped.
      document.title = "Disconnected · flockdeck";
      // Nothing behind this can be used and the terminal it is covering has
      // the keyboard, so typing would go nowhere until the pointer was used.
      $("retry").focus();
      reconnectTimer = setTimeout(connectControl, 1500);
    };
    ws.onerror = () => ws.close();
  }

  /** Where the keyboard was when the disconnected panel took it. */
  let beforeDisconnect = null;

  /** giveKeyboardBack returns the keyboard to where it was before the
   *  connection went: a field or a dialog still on screen, or else whatever
   *  dialog is open, or else the terminal. */
  function giveKeyboardBack() {
    const back = beforeDisconnect;
    beforeDisconnect = null;
    if (back && back.isConnected && back !== document.body && back.offsetParent !== null) { back.focus(); return; }
    const root = modalRoot();
    if (root) root.focus();
    else focusTerminal();
  }

  /** renderUpdate shows the chip when a release has been downloaded.
   *
   *  It is keyed on the version so the chip is not rebuilt on every snapshot,
   *  which arrive several times a second while agents are working. */
  let updateShown = null;
  function renderUpdate(s) {
    // Installing is a restart, which stops every agent at the desk; a window
    // reached through the relay is not offered it (see remoteWindow).
    const u = remoteWindow ? null : (s.update || null);
    const key = u ? u.version : "";
    if (key === updateShown) return;
    updateShown = key;
    const b = $("btn-update");
    // The settings say whether an update is waiting, beside the switch that
    // decides whether one is looked for.
    settingsChanged();
    if (!u) { b.hidden = true; return; }
    b.textContent = "";
    b.append(iconEl("update", 14), document.createTextNode("Update " + u.version));
    describe(b, "Version " + u.version + " has been downloaded and is ready to install");
    b.hidden = false;
  }

  /** renderNotes turns a release note's text into the handful of block and
   *  inline shapes docs/releases/*.md actually uses — headings, bullet
   *  lists, paragraphs, **bold** and `code` — rather than a general Markdown
   *  engine. Curated notes read as prose with bullets, not a flat commit
   *  list, and showing them as literal preformatted text put a line of stray
   *  "##" and "-" characters in front of a user instead of the heading and
   *  list they were meant to be.
   *
   *  Built as elements through el() and textContent, never innerHTML, so
   *  nothing a note contains is ever parsed as markup. Returns an array of
   *  nodes rather than a DocumentFragment, so the caller can pass it to
   *  append() (which takes any number of nodes) without either of them
   *  needing a document to make one in. */
  function renderNotes(text) {
    const nodes = [];
    const lines = text.replace(/\r\n?/g, "\n").split("\n");
    // inline splits **bold** and `code` out of a line's text, appending the
    // rest as plain text nodes so unmatched stretches are never lost.
    const inline = (into, s) => {
      const re = /\*\*(.+?)\*\*|`(.+?)`/g;
      let last = 0, m;
      while ((m = re.exec(s))) {
        if (m.index > last) into.append(document.createTextNode(s.slice(last, m.index)));
        into.append(m[1] !== undefined ? el("strong", "", m[1]) : el("code", "", m[2]));
        last = re.lastIndex;
      }
      if (last < s.length) into.append(document.createTextNode(s.slice(last)));
    };
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      if (!line.trim()) { i++; continue; }
      const heading = /^(#{1,6})\s+(.*)$/.exec(line);
      if (heading) {
        // # is the file's own title, kept as a heading rather than promoted
        // to h1: the dialog around it already has "Update to vX.Y.Z" as its
        // own title, and nothing in a note should outrank that.
        const node = el("h" + Math.min(6, heading[1].length + 2), "");
        inline(node, heading[2].trim());
        nodes.push(node);
        i++;
        continue;
      }
      if (/^[-*]\s+/.test(line)) {
        const ul = el("ul", "");
        while (i < lines.length && /^[-*]\s+/.test(lines[i])) {
          const li = el("li", "");
          inline(li, lines[i].replace(/^[-*]\s+/, ""));
          ul.append(li);
          i++;
        }
        nodes.push(ul);
        continue;
      }
      // A paragraph runs until the next blank line, list item or heading;
      // its own line breaks are folded to spaces the way Markdown reads them.
      const buf = [];
      while (i < lines.length && lines[i].trim() && !/^[-*]\s+/.test(lines[i]) && !/^#{1,6}\s+/.test(lines[i])) {
        buf.push(lines[i].trim());
        i++;
      }
      const p = el("p", "");
      inline(p, buf.join(" "));
      nodes.push(p);
    }
    return nodes;
  }

  /** openUpdate explains what installing costs before it is done.
   *
   *  What it costs is the running agents: the layout comes back, but a pane is
   *  a live process and restarting stops it. Saying so here is the difference
   *  between a restart the user chose and one they regret. */
  function openUpdate() {
    const u = !remoteWindow && state && state.update;
    if (!u) return;
    // Named like every other dialog, so that the one it replaced — whose answer
    // may still be on its way — no longer thinks the panel is its own.
    dialog = "update";
    // The ? every other dialog has: the page on the command line covers
    // updating, and how to stop the checks.
    openOverlay("Update to " + u.version, "cli");
    const body = $("overlay-body");
    body.append(el("p", "", "This version has been downloaded and checked against its published checksum. Installing it saves and reopens your layout, but the agents running in panes are stopped."));
    if (u.notes) {
      // The notes scroll inside a box of their own, and a box nothing can
      // focus cannot be scrolled from the keyboard: the part below its
      // bottom edge was out of reach without a mouse.
      const notes = el("div", "update-notes");
      notes.append(...renderNotes(u.notes));
      notes.tabIndex = 0;
      notes.setAttribute("role", "region");
      notes.setAttribute("aria-label", "Release notes");
      body.append(notes);
    }
    if (u.url) {
      const a = el("a", "", "Release notes on GitHub");
      a.href = u.url; a.target = "_blank"; a.rel = "noreferrer noopener";
      body.append(a);
    }
    const row = el("div", "update-row");
    const go = el("button", "chip primary", "Restart now");
    go.onclick = () => { closeOverlay(); send({ cmd: "restart" }); };
    const later = el("button", "chip", "Later");
    later.onclick = closeOverlay;
    row.append(go, later);
    body.append(row);
    // The keyboard starts on the choice that stops nothing. On Restart now,
    // a chip clicked by mistake while typing to an agent had the next Enter
    // stop every agent in every pane.
    later.focus();
  }

  // ----------------------------------------------------------------- remote

  /** What the remote access dialog last heard: the account's devices and
   *  machines, and the pairing link it is showing, if any. Both are dropped
   *  whenever the dialog is opened again, because a pairing link that has
   *  been shown once is not one to leave lying about on the screen. */
  let remoteRoster = null;
  let remotePairing = null;
  let remoteChipKey = null;

  /** How often the roster is asked for again while a pairing link is on
   *  screen, waiting on a scan: nothing else here asks again on its own, so
   *  a device paired while the link sat there showed up only once the
   *  dialog was closed and opened again. remotePairPollTimer is the chain's
   *  own handle, cleared whenever the dialog closes. */
  const REMOTE_PAIR_POLL_MS = 4000;
  let remotePairPollTimer = null;

  /** remotePairPoll keeps asking for the roster while remotePairing is a
   *  link still good to scan. It stops itself the moment that stops being
   *  true — spent for a fresh one, expired, or the dialog gone — rather
   *  than at every place that can happen to it. */
  function remotePairPoll() {
    remotePairPollTimer = null;
    const p = remotePairing;
    const live = p && p.url && (!p.expiresAt || Date.parse(p.expiresAt) > Date.now());
    if (!live || !remoteShown()) return;
    send({ cmd: "remoteDevices" });
    remotePairPollTimer = setTimeout(remotePairPoll, REMOTE_PAIR_POLL_MS);
  }

  /** remotePairPollStop ends the chain, if one is going. */
  function remotePairPollStop() {
    clearTimeout(remotePairPollTimer);
    remotePairPollTimer = null;
  }

  /** remoteDraft is what has been typed into the form that turns remote
   *  access on, kept across the redraws a state snapshot causes while the
   *  dialog is open. remoteBusy is which request is out, so its button waits
   *  rather than asking twice, and remoteOutcome the last answer to one. */
  const newRemoteDraft = () => ({ relay: "", name: "", join: "", invite: "", more: false });
  let remoteDraft = newRemoteDraft();
  /** remoteMoveDraft is the same for the form that moves this machine to
   *  another relay, folded away until it is asked for. */
  const newRemoteMoveDraft = () => ({ open: false, relay: "", invite: "", join: "" });
  let remoteMoveDraft = newRemoteMoveDraft();
  let remoteBusy = "";
  let remoteOutcome = null;

  /** renderRemoteChip keeps the rail's Remote button saying where the tunnel
   *  stands, and the dialog's status line current while it is open. The
   *  button is there whether or not remote access is on: hidden until the
   *  machine was enrolled, it could only be found by somebody who already
   *  knew to look in the palette. Like the update pill it is keyed on what it
   *  shows, so a snapshot that says nothing new about the tunnel does not
   *  rebuild it. */
  function renderRemoteChip(s) {
    const r = s.remote || null;
    // retryAt is part of the key too: a run of failures with the same detail
    // still moves it on each attempt, and the dialog's "trying again at…"
    // has to follow it rather than sit on the first one shown.
    const key = r ? [r.state, r.viewers, r.detail, r.relay, r.retryAt].join("|") : "off";
    if (key === remoteChipKey) return;
    remoteChipKey = key;
    const b = $("btn-remote");
    // Amber only when it needs somebody, which is what amber means everywhere
    // else here; green is a working tunnel, grey one still connecting, and
    // no badge at all is remote access turned off.
    const trouble = !!r && (r.state === "error" || r.state === "revoked" || r.state === "replaced" || r.state === "lapsed");
    const pending = !!r && r.state === "connecting";
    b.classList.toggle("trouble", trouble);
    b.classList.toggle("pending", pending);
    const badge = b.querySelector(".rail-badge");
    badge.classList.toggle("on", !!r && r.state === "connected");
    badge.classList.toggle("pending", pending);
    badge.classList.toggle("trouble", trouble);
    // The count says whether anybody is using it, and is part of the name.
    b.querySelector(".rail-label").textContent = r && r.viewers > 0 ? "Remote · " + r.viewers : "Remote";
    describe(b, r ? remoteSummary(r) : actionTip("remote",
      "off. Turn it on to open this window from another device, such as a tablet or a phone"));
    if (remoteShown()) keepFocus(renderRemote);
  }

  /** remoteShown is whether remote access is on screen to be drawn: its own
   *  dialog, or its section of the settings. */
  function remoteShown() { return !!remoteHost(); }

  /** remoteSummary says in a sentence where the tunnel stands. */
  function remoteSummary(r) {
    const where = String(r.relay || "the relay").replace(/^https?:\/\//, "");
    switch (r.state) {
      case "connected": {
        const n = r.viewers || 0;
        return "Reachable through " + where +
          (n ? " — " + n + (n === 1 ? " window is" : " windows are") + " open from another device" : "");
      }
      // A retry that has not yet been waited out shows when it is due, as a
      // clock time rather than a countdown: nothing here redraws on a timer,
      // so a countdown would sit still and go stale between state pushes,
      // where a clock time reads true until the next one arrives.
      case "connecting": return r.retryAt ? "Reconnecting to " + where + "." + remoteRetryClock(r.retryAt) :
        "Connecting to " + where + "…";
      case "error": return "Cannot reach " + where + (r.detail ? ": " + r.detail : "") + "." + remoteRetryClock(r.retryAt);
      case "revoked":
      case "replaced": return r.detail || "Remote access has stopped.";
      // The relay's own words: it is the relay that stopped remote access,
      // and nothing on this machine that decided to.
      case "lapsed": return r.detail || "The relay has stopped remote access for this account.";
      default: return "Not connected to " + where + ".";
    }
  }

  /** remoteRetryClock is " Trying again at 14:32:07." for when the tunnel's
   *  own backoff is due to try the relay again, or " Trying again shortly."
   *  once that time is at hand, so the line never claims a moment already
   *  past. Empty once there is nothing waiting to retry. */
  function remoteRetryClock(retryAt) {
    const t = Date.parse(retryAt || "");
    if (!(t > 0)) return "";
    if (t <= Date.now()) return " Trying again shortly.";
    const d = new Date(t);
    const two = (n) => String(n).padStart(2, "0");
    return " Trying again at " + two(d.getHours()) + ":" + two(d.getMinutes()) + ":" + two(d.getSeconds()) + ".";
  }

  /** remotePlanText says an account's plan in a sentence, as the relay
   *  reports it. The relay is what decides anything by a plan; the app only
   *  shows it, and every part of the app works the same whatever it says. */
  function remotePlanText(p) {
    if (p.active === false) {
      let s = p.message || "The relay has stopped remote access for this account.";
      if (p.deleteAt) s += " The account and its paired devices are kept until " + remoteDay(p.deleteAt) + ", then deleted.";
      return s;
    }
    if (p.plan === "trial") {
      const n = remoteDaysLeft(p.ends);
      return (p.name || "Free trial") + ": " + n + (n === 1 ? " day" : " days") + " left, until " + remoteDay(p.ends) + ".";
    }
    return (p.name || "Subscription") + (p.ends ? ", paid until " + remoteDay(p.ends) : "") + ".";
  }

  /** remoteDaysLeft is the days left until a time the relay reported, a part
   *  of one counting as one. */
  function remoteDaysLeft(when) {
    const t = Date.parse(when || "");
    if (!(t > 0)) return 0;
    return Math.max(0, Math.ceil((t - Date.now()) / 86400000));
  }

  /** remoteDay is a day as people write it: 12 October 2026. */
  function remoteDay(when) {
    return new Date(Date.parse(when)).toLocaleDateString(undefined, { day: "numeric", month: "long", year: "numeric" });
  }

  /** remotePlanSection is the account's plan in the remote access dialog. */
  function remotePlanSection(p) {
    const box = section("Plan");
    box.id = "remote-plan";
    box.append(el("p", p.active === false ? "remote-error" : null, remotePlanText(p)));
    box.append(el("p", "fan-hint", "Remote access through the relay is paid for from Devices on a paired phone or " +
      "browser. The desktop app is free, and works the same whatever the plan."));
    return box;
  }

  /** openRemote shows remote access: whether the relay can be reached, a way
   *  to pair a device, and what is paired already. */
  function openRemote() {
    dialog = "remote";
    remoteRoster = null;
    remotePairing = null;
    remoteOutcome = null;
    remoteBusy = "";
    remoteMoveDraft = newRemoteMoveDraft();
    openOverlay("Remote access", "remote");
    renderRemote();
    send({ cmd: "remoteDevices" });
  }

  /** remoteHost is where remote access is drawn, or null while it is not on
   *  screen. */
  function remoteHost() {
    if (dialog === "remote") return $("overlay-body");
    if (dialog === "settings" && settingsSection === "remote") return $("settings-host");
    return null;
  }

  /** DESK_ONLY_REMOTE is shown to a window reached through the relay in place
   *  of turning remote access on or off, which the server refuses from there:
   *  off cuts the way in that window came by, with nothing at its end to turn
   *  it on again, and on, from a window already in, is against another relay. */
  const DESK_ONLY_REMOTE = "Remote access is turned on, turned off and moved to another relay " +
    "on the machine itself, not from a window reached through the relay.";

  function renderRemote() {
    const body = remoteHost();
    if (!body) return;
    body.textContent = "";
    const r = state && state.remote;
    // Enrolling decides where the traffic goes, so the form says which relay
    // it will use — empty is the default, named — before anything is pressed.
    if (!r && remoteRoster && !remoteRoster.enabled) {
      body.append(el("div", "fan-hint",
        "Remote access opens this window from another device — a laptop, a tablet, a phone — " +
        "through a relay, without opening a port on this machine."));
      body.append(remoteWindow ? el("p", "fan-hint", DESK_ONLY_REMOTE) : remoteEnableForm());
      return;
    }
    if (r) body.append(el("div", "remote-status " + r.state, remoteSummary(r)));
    // Only a relay that asks for a subscription reports a plan.
    if (remoteRoster && remoteRoster.plan) body.append(remotePlanSection(remoteRoster.plan));

    const pair = section("Pair a device");
    const p = remotePairing;
    if (p && p.error) pair.append(el("p", "remote-error", p.error));
    if (p && p.url) {
      const box = el("div", "remote-pair");
      if (p.qr) {
        const img = el("img", "remote-qr");
        img.alt = "QR code for the pairing link";
        img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(p.qr);
        box.append(img);
      }
      const text = el("div", "remote-pair-text");
      text.append(el("p", null, "Scan this with the device you want to pair, or open the link on it. " +
        "It works once" + remoteUntil(p.expiresAt) + "."));
      const link = el("input", "remote-link");
      link.readOnly = true;
      link.value = p.url;
      link.setAttribute("aria-label", "Pairing link");
      link.onfocus = () => link.select();
      text.append(link);
      // Sending the link to the device, as a message to yourself, is the
      // usual way it gets there, and copying it meant clicking into the
      // field, selecting all of it and pressing the copy key.
      const copy = el("button", "chip", "Copy link");
      copy.id = "remote-copy";
      copy.onclick = () => copyText(p.url, link, "Copied the pairing link");
      text.append(copy);
      text.append(el("p", "fan-hint", "Whoever opens it can drive every agent here, so treat it like a password until then."));
      box.append(text);
      pair.append(box);
    } else if (!p || !p.pending) {
      pair.append(el("p", "fan-hint", "A pairing link works once and expires in a few minutes. " +
        "The device that opens it can open this window until you unpair it."));
    }
    const row = el("div", "update-row");
    const go = el("button", "chip primary",
      p && p.pending ? "Asking the relay…" : p && p.url ? "New link" : "Pair a device");
    // An id, because the wording changes while it works and keepFocus would
    // otherwise lose it across the redraw.
    go.id = "remote-pair";
    go.disabled = !!(p && p.pending);
    // These buttons draw the dialog again themselves, and drawn without
    // keepFocus it took the keyboard with the button that was pressed.
    go.onclick = () => {
      remotePairing = { pending: true };
      keepFocus(renderRemote);
      send({ cmd: "remotePair", kind: "device" });
    };
    row.append(go);
    pair.append(row);
    body.append(pair);

    const roster = remoteRoster;
    if (!roster) { body.append(el("div", "dir-empty", "Loading…")); return; }
    if (roster.error) body.append(el("p", "remote-error", roster.error));

    const devices = roster.devices || [];
    const dev = section(devices.length === 1 ? "1 paired device" : devices.length + " paired devices");
    if (!devices.length && !roster.error) dev.append(el("div", "dir-empty", "Nothing is paired yet."));
    devices.forEach((d) => {
      const item = el("div", "wt-row");
      item.dataset.key = "device:" + d.id;
      const main = el("div", "wt-main");
      const title = el("div", "wt-title");
      title.append(el("span", "wt-label", d.name || "Unnamed device"));
      const mine = !!roster.current && d.id === roster.current;
      if (mine) title.append(el("span", "wt-flag", "this device"));
      main.append(title);
      main.append(el("div", "wt-meta", "paired " + remoteAgo(d.created) + " · last seen " + remoteAgo(d.lastSeen)));
      item.append(main);
      const actions = el("div", "wt-actions");
      const rename = el("button", "chip", "Rename");
      rename.onclick = () => remoteRename("device", d.id, d.name || "");
      const drop = el("button", "chip danger", "Unpair");
      drop.onclick = () => {
        const q = mine
          ? "Unpair this device? This window will close, and it will need a new pairing link to come back."
          : "Unpair " + (d.name || "this device") + "? Any window it has open will close.";
        if (!window.confirm(q)) return;
        send({ cmd: "remoteRevoke", id: d.id });
      };
      actions.append(rename, drop);
      item.append(actions);
      dev.append(item);
    });
    body.append(dev);

    const hosts = roster.hosts || [];
    if (hosts.length) {
      const hw = section(hosts.length === 1 ? "1 machine" : hosts.length + " machines");
      hosts.forEach((h) => {
        const item = el("div", "wt-row");
        const main = el("div", "wt-main");
        const title = el("div", "wt-title");
        title.append(el("span", "wt-label", h.name || "Unnamed machine"));
        if (h.self) title.append(el("span", "wt-flag", "this one"));
        main.append(title);
        main.append(el("div", "wt-meta", h.online ? "online" : "offline — last seen " + remoteAgo(h.lastSeen)));
        item.append(main);
        // A machine renames itself here; the relay takes a new name for
        // another machine only from that machine, or from a paired device.
        if (h.self) {
          const actions = el("div", "wt-actions");
          const rename = el("button", "chip", "Rename");
          rename.id = "remote-rename";
          rename.onclick = () => remoteRename("host", "", h.name || "");
          actions.append(rename);
          item.append(actions);
        }
        hw.append(item);
      });
      // A machine wiped without turning remote access off is listed offline
      // for good unless somebody takes it off, and this is where it is seen.
      if (hosts.some((h) => !h.self && !h.online)) {
        hw.append(el("p", "fan-hint", "A machine that was wiped or lost, and so never turned remote access off, " +
          "is taken off the account from the Devices page of a paired device."));
      }
      body.append(hw);
    }
    body.append(remoteMachineSection(r, roster));
  }

  /** remoteEnableForm turns remote access on: what `flockdeck remote enable`
   *  asks for, as fields. Each may be left empty — the default relay, the host
   *  name — and joining an account or answering an invitation is folded away,
   *  since most machines do neither. */
  function remoteEnableForm() {
    const box = section("Turn it on");
    const d = remoteDraft;
    const o = remoteOutcome;
    box.append(el("p", null, "This machine is not enrolled with a relay. Leave the relay empty for " +
      "remote.flockdeck.ai, or name another."));
    const form = el("div", "wt-form");
    const fields = [];
    const field = (key, id, placeholder, label) => {
      const f = el("input");
      f.id = id;
      f.placeholder = placeholder;
      f.value = d[key];
      f.spellcheck = false;
      f.autocomplete = "off";
      f.setAttribute("aria-label", label);
      f.oninput = () => { d[key] = f.value; };
      f.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); enable(); } };
      fields.push([key, f]);
      form.append(f);
    };
    field("relay", "remote-relay", "Relay — empty for https://remote.flockdeck.ai", "Relay address");
    field("name", "remote-name", "Name for this machine — empty for its host name", "Machine name");
    if (d.more) {
      field("join", "remote-join", "Join code, from a machine already on the account", "Join code");
      field("invite", "remote-invite", "Invitation code, for a relay that asks for one", "Invitation code");
    }
    box.append(form);

    const enable = () => {
      // Only its own request holds the form up: one still out to turn remote
      // access off has nothing left to say to a machine that is not enrolled.
      if (remoteBusy === "enable") return;
      // What is in the fields now is what goes, typed or pasted, and codes
      // folded away are not sent.
      fields.forEach(([key, f]) => { d[key] = f.value; });
      remoteBusy = "enable";
      remoteOutcome = null;
      send({ cmd: "remoteEnable", relay: d.relay.trim(), name: d.name.trim(),
        join: d.more ? d.join.trim() : "", invite: d.more ? d.invite.trim() : "" });
      keepFocus(renderRemote);
    };
    const row = el("div", "update-row");
    const go = el("button", "chip primary", remoteBusy === "enable" ? "Turning it on…" : "Turn on remote access");
    go.id = "remote-enable";
    go.disabled = remoteBusy === "enable";
    go.onclick = enable;
    const more = el("button", "chip", d.more ? "No codes" : "Joining an account, or invited?");
    more.id = "remote-more";
    more.onclick = () => {
      d.more = !d.more;
      keepFocus(renderRemote);
      // Opened, the codes are what the press was for.
      if (d.more && $("remote-join")) $("remote-join").focus();
    };
    row.append(go, more);
    box.append(row);
    if (o && o.action === "enable" && o.error) box.append(el("p", "remote-error", o.error));
    if (o && o.warning) box.append(el("p", "remote-error", o.warning));
    box.append(el("p", "fan-hint", "The same from a terminal: flockdeck remote enable."));
    box.append(enterpriseNote());
    return box;
  }

  /** What Enterprise will be, in the words the remote dialog and the
   *  settings' plan both use. It is for companies, and it is not here yet:
   *  nothing here should suggest that anybody can run a relay today, or
   *  promise a price or a date, which are not settled. Nor does anything
   *  here promise what the shared relay will cost: that is not settled
   *  either, so the plan says what it covers today and no more. */
  const ENTERPRISE_WHAT = "run the relay on your own infrastructure, with SSO and support";

  /** enterpriseNote announces Enterprise, which is not here yet. */
  function enterpriseNote() {
    return el("p", "fan-hint", "Coming soon, for companies: " + ENTERPRISE_WHAT + ".");
  }

  /** remoteMachineSection is this machine's own place on the relay: trying
   *  a tunnel that is not up again now, and turning remote access off. */
  function remoteMachineSection(r, roster) {
    const box = section("This machine");
    const o = remoteOutcome;
    const row = el("div", "update-row");
    if (r && r.state !== "connected") {
      const retry = el("button", "chip", remoteBusy === "reconnect" ? "Trying…" : "Try again");
      retry.id = "remote-retry";
      retry.disabled = !!remoteBusy;
      retry.onclick = () => {
        remoteBusy = "reconnect";
        remoteOutcome = null;
        send({ cmd: "remoteReconnect" });
        keepFocus(renderRemote);
      };
      row.append(retry);
    }
    if (remoteWindow) {
      // Off, and on again against another relay, are the desk's: see
      // DESK_ONLY_REMOTE. Trying the relay again is not.
      box.append(row, el("p", "fan-hint", DESK_ONLY_REMOTE), enterpriseNote());
      return box;
    }
    const off = el("button", "chip danger", remoteBusy === "disable" ? "Turning it off…" : "Turn off remote access");
    off.id = "remote-disable";
    off.disabled = !!remoteBusy;
    off.onclick = () => {
      const where = String((r && r.relay) || "the relay").replace(/^https?:\/\//, "");
      const hosts = roster.hosts || [];
      const devices = roster.devices || [];
      let q = "Turn off remote access? This machine is taken off " + where +
        ", and no paired device can reach it until it is turned on again.";
      // Taking the account's only machine off deletes the account on the
      // relay, and every device paired with it, which nothing else here says.
      if (hosts.length === 1 && devices.length) {
        q += " It is the only machine on the account, so the account goes too, and " +
          (devices.length === 1 ? "its paired device is" : "its " + devices.length + " paired devices are") + " unpaired.";
      }
      if (!window.confirm(q)) return;
      remoteDisable(false);
    };
    // Moving is for a company taking its machines onto a relay of its own,
    // so it is offered beside turning it off, and its form folded away.
    const move = el("button", "chip", remoteMoveDraft.open ? "Keep it here" : "Move to another relay…");
    move.id = "remote-move";
    move.disabled = !!remoteBusy;
    move.onclick = () => {
      remoteMoveDraft.open = !remoteMoveDraft.open;
      remoteOutcome = null;
      renderRemote();
      const f = $("remote-move-relay");
      if (f && f.focus) f.focus();
    };
    row.append(move, off);
    box.append(row);
    if (remoteMoveDraft.open) box.append(remoteMoveForm(r, roster));
    if (o && o.action !== "enable" && o.action !== "move" && o.error) box.append(el("p", "remote-error", o.error));
    if (o && o.action === "move" && o.warning) box.append(el("p", "remote-error", o.warning));
    if (o && o.untold) {
      box.append(el("p", "fan-hint", "Try again first: a relay that cannot be reached is most often the " +
        "network, for now. Forgetting it here anyway turns remote access off on this machine, but the " +
        "relay goes on listing it, offline, since nothing else can take it off."));
      const again = el("div", "update-row");
      const retry = el("button", "chip primary", "Try again");
      retry.id = "remote-disable-again";
      retry.disabled = !!remoteBusy;
      retry.onclick = () => remoteDisable(false);
      const forget = el("button", "chip danger", "Forget it here anyway");
      forget.id = "remote-forget";
      forget.disabled = !!remoteBusy;
      forget.onclick = () => {
        if (!window.confirm("Forget remote access here without telling the relay? It will list this machine, offline, for good.")) return;
        remoteDisable(true);
      };
      again.append(retry, forget);
      box.append(again);
    }
    box.append(enterpriseNote());
    return box;
  }

  /** copyText puts text on the clipboard and says so. Where the browser will
   *  not - a page it does not count as secure, a permission refused - the
   *  text is selected in the field it is shown in, so the copy key does it. */
  function copyText(text, field, done) {
    const selectInstead = () => {
      if (field) { field.focus(); field.select(); }
      notice("The clipboard could not be reached, so the link is selected: copy it from there", true);
    };
    try {
      const clip = window.navigator && window.navigator.clipboard;
      if (!clip || !clip.writeText) { selectInstead(); return; }
      clip.writeText(text).then(() => notice(done, false), selectInstead);
    } catch { selectInstead(); }
  }

  /** remoteMoveForm moves this machine to another relay: what `flockdeck
   *  remote move` asks for, as fields. What moving costs, every device
   *  pairing again, is said here before the button, and again in the
   *  question the button asks, since moving back does not undo it. */
  function remoteMoveForm(r, roster) {
    const d = remoteMoveDraft;
    const o = remoteOutcome;
    const from = String((r && r.relay) || "the relay it is on").replace(/^https?:\/\//, "");
    const box = el("div", "remote-move");
    box.append(el("p", "fan-hint", "This machine is enrolled with the new relay first, and leaves " + from +
      " only once the new one answers. Every device paired with it will then have to pair again, with the new relay."));
    const form = el("div", "wt-form");
    const fields = [];
    const field = (key, id, placeholder, label) => {
      const f = el("input");
      f.id = id;
      f.placeholder = placeholder;
      f.value = d[key];
      f.spellcheck = false;
      f.autocomplete = "off";
      f.setAttribute("aria-label", label);
      f.oninput = () => { d[key] = f.value; };
      f.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); go(); } };
      fields.push([key, f]);
      form.append(f);
    };
    field("relay", "remote-move-relay", "New relay, e.g. https://relay.example.com", "New relay address");
    field("invite", "remote-move-invite", "Invitation code, if the new relay asks for one", "Invitation code");
    field("join", "remote-move-join", "Join code, to join an account already on the new relay", "Join code");
    box.append(form);

    const go = () => {
      if (remoteBusy) return;
      fields.forEach(([key, f]) => { d[key] = f.value; });
      const to = d.relay.trim();
      if (!to) {
        remoteOutcome = { action: "move", error: "Name the relay to move this machine to." };
        renderRemote();
        return;
      }
      const hosts = (roster && roster.hosts) || [];
      const devices = (roster && roster.devices) || [];
      let q = "Move this machine from " + from + " to " + to.replace(/^https?:\/\//, "") + "? " +
        "Every device paired with it will have to pair again, with the new relay.";
      // Leaving is taking the account's only machine off the old relay,
      // which takes the account and its devices there with it.
      if (hosts.length === 1 && devices.length) {
        q += " It is the only machine on its account on " + from + ", so the account goes too, and " +
          (devices.length === 1 ? "its paired device is" : "its " + devices.length + " paired devices are") + " unpaired there.";
      }
      if (!window.confirm(q)) return;
      remoteBusy = "move";
      remoteOutcome = null;
      send({ cmd: "remoteMove", relay: to, invite: d.invite.trim(), join: d.join.trim() });
      renderRemote();
    };
    const row = el("div", "update-row");
    const btn = el("button", "chip primary", remoteBusy === "move" ? "Moving…" : "Move this machine");
    btn.id = "remote-move-go";
    btn.disabled = !!remoteBusy;
    btn.onclick = go;
    row.append(btn);
    box.append(row);
    if (o && o.action === "move" && o.error) box.append(el("p", "remote-error", o.error));
    box.append(el("p", "fan-hint", "The same from a terminal: flockdeck remote move <relay>."));
    return box;
  }

  /** remoteRename asks for a new name for this machine or a paired device,
   *  starting from the one it has. The name is what every device and machine
   *  on the account lists it as. An answer left empty goes too, to be refused
   *  with a reason rather than dropped without one. */
  function remoteRename(kind, id, current) {
    const name = window.prompt(kind === "host" ? "Rename this machine" : "Rename " + (current || "this device"), current);
    if (name === null || name === undefined) return;
    const next = String(name).trim();
    if (next === current) return;
    send(kind === "host" ? { cmd: "remoteRename", kind, name: next } : { cmd: "remoteRename", kind, id, name: next });
  }

  function remoteDisable(force) {
    remoteBusy = "disable";
    remoteOutcome = null;
    send({ cmd: "remoteDisable", force });
    keepFocus(renderRemote);
  }

  /** remoteAgo says how long since a time the relay reported, roughly. A time
   *  it never had arrives as Go's zero time, which is long before anything. */
  function remoteAgo(when) {
    const t = Date.parse(when || "");
    if (!(t > 0)) return "never";
    const s = Math.max(0, (Date.now() - t) / 1000);
    if (s < 60) return "just now";
    const unit = (n, one) => n + " " + one + (n === 1 ? "" : "s") + " ago";
    if (s < 3600) return unit(Math.floor(s / 60), "minute");
    if (s < 172800) return unit(Math.floor(s / 3600), "hour");
    return unit(Math.floor(s / 86400), "day");
  }

  /** howLong says how long a pane has been as it is. The server words it as
   *  a list of recent things would, "just now" or "5 minutes ago", and after
   *  "for" that read "for just now" and "for 5 minutes ago". The phone's list
   *  says it the same way. */
  function howLong(since) {
    const s = String(since).trim();
    if (s === "just now") return s;
    const ago = s.match(/^(.+) ago$/);
    return "for " + (ago ? ago[1] : s);
  }

  /** remoteUntil is ", until 14:05" for a pairing link's expiry. */
  function remoteUntil(when) {
    const t = Date.parse(when || "");
    if (!(t > 0)) return "";
    const d = new Date(t);
    return ", until " + String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
  }

  function send(cmd) {
    if (control && control.readyState === WebSocket.OPEN) {
      control.send(JSON.stringify(cmd));
    }
  }

  // ------------------------------------------------------------------ state

  function applyState(s) {
    state = s;
    if (s.agents) {
      const encoded = JSON.stringify(s.agents);
      if (encoded !== catalogKey) {
        catalogKey = encoded;
        catalog = s.agents;
        if (dialog === "agentPicker") keepFocus(renderAgentPicker);
        settingsChanged();
      }
    }
    const rebuilt = rebuildChangedTabs(s);
    applyWeights(s);
    showActiveTab(s, rebuilt);
    renderTabs(s);
    renderRail(s);
    renderRailToggle(s);
    renderSummary(s);
    announceStatus(s);
    followAgents(s);
    followProjects(s);
    followWorktrees(s);
    followChanges(s);
    renderUpdate(s);
    renderRemoteChip(s);
    updatePaneChrome(s);
    if (!$("promptbar").hidden) labelPrompt(s);
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

  /** cardHidden is which settled tabs the boss has put back to the grid by
   *  hand, in place of the summary card this collapses to on its own --
   *  cleared the moment the tab starts working again (see tabSettleInfo),
   *  so the next time it finishes the card is offered fresh rather than
   *  staying dismissed for good. */
  const cardHidden = new Set();

  /** tabSettleInfo says whether tab is a delegated job that has stopped:
   *  nobody left in it working or starting, so there is nothing left to
   *  watch and everything to report. Only a tab a fan-out actually filled
   *  counts -- tab.delegated (see workspace.Tab.Delegated and
   *  server.tabView), set server-side exactly when Spawn gathered a
   *  fan-out's own children into this tab, never by an ordinary split --
   *  and then only with two or more agent panes in it, none of them a
   *  shell, which cannot "finish" a turn the way an agent does; a lone
   *  helper's tab of one pane has nothing to collapse in the first place.
   *  A person is free to build the exact same shape by hand, splitting
   *  several agents into one tab themselves, and that must never vanish
   *  into a card just because every one of them happens to have gone idle
   *  at the same moment -- tab.delegated is what tells the two apart,
   *  where the pane count and kind alone cannot. None of them may carry a
   *  parent, either: a helper split into its own manager's tab
   *  (SpawnOptions.Split) shares it with that manager's own pane, which is
   *  the boss's own conversation, not delegated work, even though Spawn
   *  does mark that placement delegated too. ids is every pane in the tab
   *  either way, for the caller to walk. */
  function tabSettleInfo(tab, s) {
    const idSet = new Set();
    collectPanes(tab.root, idSet);
    const ids = [...idSet];
    const views = ids.map((id) => s.panes && s.panes[id]).filter(Boolean);
    const settled = !!tab.delegated && views.length >= 2 && !views.some((v) => v.kind === "shell" || v.parent) &&
      views.every((v) => v.status !== "working" && v.status !== "starting");
    if (!settled) cardHidden.delete(tab.id);
    return { settled, ids };
  }

  /** shapeOf is what a tab has to be redrawn for, given info already worked
   *  out for it by tabSettleInfo. Weights are left out so a drag does not
   *  rebuild anything; zoom is in, because it changes which panes are on
   *  screen, and while zoomed so is the focused pane. card is in for the
   *  same reason: a settled fan-out collapses to a summary in its place,
   *  and each pane's own outcome -- its status, whether it failed, its
   *  latest reply -- has to redraw the card when any of them changes, even
   *  though the tree beneath it does not. */
  function shapeOf(tab, s, info) {
    const card = info.settled && !cardHidden.has(tab.id);
    return JSON.stringify({
      tree: structureOf(tab.root),
      zoom: tab.zoom,
      focus: tab.zoom ? tab.focus : "",
      card,
      outcomes: card ? info.ids.map((id) => outcomeKey(s.panes[id])).join("|") : "",
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
      // Built once and left standing. Pushes keep arriving while nothing is
      // open here - agents in other projects go on working - and building it
      // again for each took the keyboard off its buttons.
      if (emptyPage) return false;
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
      // Closing the last tab took the terminal the keyboard was in, and left
      // it on the page with nothing to act on; the obvious next step is here.
      if (!dialogOpen()) b.focus();
      return true;
    }
    // The "no tabs" placeholder is not a tab page, so it goes by hand.
    if (emptyPage) { emptyPage.remove(); emptyPage = null; }

    let activeRebuilt = false;
    s.tabs.forEach((tab, i) => {
      const info = tabSettleInfo(tab, s);
      const shape = shapeOf(tab, s, info);
      let page = tabPages.get(tab.id);
      if (!page || tabShapes.get(tab.id) !== shape) {
        if (page) page.remove();
        page = el("div", "tab-page");
        // The panel its tab controls, and labelled by it. The strip said
        // "tab 2 of 4" to a screen reader, and nothing tied that tab to
        // the panes it was showing.
        page.id = "page-" + tab.id;
        page.setAttribute("role", "tabpanel");
        page.setAttribute("aria-labelledby", "tab-" + tab.id);
        if (tab.zoom && tab.focus) {
          // A zoomed pane takes the whole tab. The others stay in the pane
          // registry with their terminals and connections intact, simply
          // detached from the document until the zoom is released.
          page.append(ensurePane(tab.focus).wrap);
        } else if (info.settled && !cardHidden.has(tab.id)) {
          // Nobody left working or starting: the grid of terminals nobody
          // is reading collapses to one card, a line per pane, the same way
          // Zoom already takes every pane but one out of the document --
          // the others stay alive in the registry, simply not on screen.
          page.append(buildSummaryCard(tab, s, info.ids));
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

  /** outcomeOf reads a settled pane's line for the summary card: done, needs
   *  input or failed (the three the plan this implements asks for), and the
   *  one line of detail that goes with it -- what a waiting pane wants, in
   *  the same words a permission prompt would show; why a pane failed, in
   *  its own words; or its agent's own latest reply, the same one-line
   *  preview the phone's inbox already shows, for one that simply finished.
   *  A pane the push has not caught up with yet reads as done rather than
   *  as nothing, since by the time the card is showing at all every member
   *  has already stopped one way or another. */
  function outcomeOf(v) {
    if (!v) return { kind: "done", label: "done", text: "" };
    if (v.status === "waiting") {
      const label = kindLabel(v);
      return { kind: "needs", label: "needs input", text: label ? "Wants " + label + "." : (v.detail || "") };
    }
    if (v.err) return { kind: "failed", label: "failed", text: v.err };
    return { kind: "done", label: "done", text: (v.last && v.last.text) || "" };
  }

  /** outcomeKey is outcomeOf's own text folded into shapeOf's cache key, so
   *  the card redraws when what it would say about a pane changes, without
   *  redrawing on every unrelated field a settled pane's view still carries
   *  (its CPU, its git counts, and the rest of a header nobody is looking
   *  at while the card is up). */
  function outcomeKey(v) {
    if (!v) return "";
    const out = outcomeOf(v);
    return out.kind + ":" + out.text;
  }

  /** buildSummaryCard is what a settled fan-out collapses to in place of its
   *  grid: one line per pane instead of a wall of terminals nobody is
   *  reading, pulled from data already on the push -- name, branch, status,
   *  latest reply -- rather than anything built new for this. A line
   *  expands to its own pane with the same toggleZoom a click on any pane's
   *  own ⤢ already sends; "Back to grid" is the same zoom taken all the way
   *  out, offered once instead of once per pane. */
  function buildSummaryCard(tab, s, ids) {
    const views = ids.map((id) => ({ id, v: s.panes && s.panes[id] })).filter((x) => x.v);
    const counts = { done: 0, needs: 0, failed: 0 };
    const outcomes = new Map();
    views.forEach(({ id, v }) => {
      const out = outcomeOf(v);
      outcomes.set(id, out);
      counts[out.kind]++;
    });

    const card = el("div", "summary-card");
    const head = el("div", "summary-card-head");
    head.append(el("div", "summary-card-title", (tab.title || "This job") + " finished"));
    const bits = [];
    if (counts.needs) bits.push(counts.needs + (counts.needs === 1 ? " needs input" : " need input"));
    if (counts.failed) bits.push(counts.failed + " failed");
    if (counts.done) bits.push(counts.done + " done");
    head.append(el("div", "summary-card-sub", bits.join(", ") + " — " + views.length + (views.length === 1 ? " agent" : " agents")));
    const back = el("button", "chip", "Back to grid");
    describe(back, "Show every pane again, running or not.");
    back.onclick = () => {
      cardHidden.add(tab.id);
      if (state) applyState(state);
    };
    head.append(back);
    card.append(head);

    const list = el("div", "summary-card-list");
    views.forEach(({ id, v }) => {
      const out = outcomes.get(id);
      const row = el("div", "summary-card-row");
      const dot = el("span", "dot " + (out.kind === "failed" ? "exited" : v.status));
      dot.setAttribute("aria-hidden", "true");
      row.append(describe(dot, TIPS[v.status] || v.status));
      const main = el("div", "summary-card-main");
      const title = el("div", "summary-card-name");
      title.append(el("span", null, v.name || ""));
      if (v.branch) title.append(el("span", "summary-card-branch", "⎇ " + v.branch));
      main.append(title);
      if (out.text) main.append(el("div", "summary-card-line", out.text));
      row.append(main);
      row.append(el("span", "summary-card-outcome " + out.kind, out.label));
      rowAction(row, () => send({ cmd: "toggleZoom", id }), false, v.name);
      list.append(row);
    });
    card.append(list);
    return card;
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
    // The separators say how each pair shares the space, which is how a
    // screen reader learns it. Written only by a drag or an arrow key, a
    // split restored with the layout had no value at all, and one resized
    // from another window kept the old one.
    if (!node.pane && target) {
      const kids = target.children;
      for (let i = 1; i < kids.length - 1; i++) {
        if (kids[i].classList.contains("divider")) sayShare(kids[i], kids[i - 1], kids[i + 1]);
      }
    }
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
    const now = String(Math.round((aGrow / (aGrow + bGrow)) * 100));
    if (d.getAttribute("aria-valuenow") !== now) d.setAttribute("aria-valuenow", now);
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
    if (!tab) {
      shownTab = ""; shownFocus = "";
      for (const p of panes.values()) drawWithWebgl(p, false);
      return;
    }
    const ids = new Set();
    collectPanes(tab.root, ids);
    for (const [id, p] of panes) drawWithWebgl(p, ids.has(id));
    // Only when the tab has just come on screen. A status push arrives several
    // times a second while agents work, and measuring every visible terminal
    // on each one made the browser lay the window out again for nothing: a
    // pane that changes size is refitted by its own observer.
    if (tab.id !== shownTab || rebuilt) {
      ids.forEach((id) => {
        const p = panes.get(id);
        if (p) scheduleFit(p);
      });
    }
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

  /** markStripEdges says which ends of the tab strip have tabs beyond them,
   *  for the style sheet to fade. The strip hides its scroll bar, so tabs
   *  past an end were out of sight with nothing to say they were there. It
   *  is measured when it scrolls, when its width changes, and when renderTabs
   *  changes what is in it. */
  function markStripEdges() {
    const strip = $("tabs");
    const left = (strip.scrollLeft || 0) > 1;
    const right = (strip.scrollLeft || 0) + (strip.clientWidth || 0) < (strip.scrollWidth || 0) - 1;
    strip.classList.toggle("more-left", left);
    strip.classList.toggle("more-right", right);
  }
  $("tabs").addEventListener("scroll", markStripEdges, { passive: true });
  if (window.ResizeObserver) new ResizeObserver(markStripEdges).observe($("tabs"));

  function renderTabs(s) {
    // Rebuilding the strip would destroy the element a drag is holding, and
    // the drag would end nowhere. Status pushes arrive constantly, so this is
    // not a rare case; the bar catches up when the drag finishes.
    if (dragging && dragging.kind === "tab") return;
    const bar = $("tabs");
    // Whether a tab came, went or changed width, for the fades at the ends.
    let changed = false;
    s.tabs.forEach((tab, i) => {
      const node = tabNode(tab.id);
      const title = tab.title || "tab " + (i + 1);
      if (node.label.textContent !== title) {
        changed = true;
        node.label.textContent = title;
        // A title is cut short at the tab's width, and the ones written from
        // an agent's task usually are. The bubble is the only place the rest
        // can be read, and the only thing saying how to change it.
        describe(node.btn, title + " — double-click, or press F2, to rename");
        // The close button said only "Close tab", a dozen times over across
        // the strip, and nothing said that closing one stops its agents.
        describe(node.close, "Close tab: " + title + ". Every agent in it stops.");
      }
      // Named by its title, and the agent waiting in it. Named from what it
      // holds, a tab said the button inside it as well - "one, Close tab" -
      // every time the keyboard reached it.
      const name = title + (tab.attention ? ", an agent here is waiting on you" : "");
      if (node.btn.getAttribute("aria-label") !== name) node.btn.setAttribute("aria-label", name);
      const active = tab.id === s.activeTab;
      // The look is the whole box's, close button and all; what a screen
      // reader is told is the tab's own.
      node.item.classList.toggle("active", active);
      node.btn.setAttribute("aria-selected", String(active));
      node.item.classList.toggle("attention", !!tab.attention);
      if (!!node.attn !== !!tab.attention) changed = true;
      setAttention(node, !!tab.attention);
      // One stop on the way through the window rather than two per tab. With
      // a dozen agents open, tabbing past the strip to reach the terminal
      // behind it took twenty-four presses; the arrow keys walk it instead,
      // which is what a tab strip is expected to answer to anyway.
      const stop = active ? 0 : -1;
      if (node.btn.tabIndex !== stop) { node.btn.tabIndex = stop; node.close.tabIndex = stop; }
      if (bar.childNodes[i] !== node.item) { bar.insertBefore(node.item, bar.childNodes[i] || null); changed = true; }
    });
    for (const [id, node] of tabNodes) {
      if (s.tabs.some((t) => t.id === id)) continue;
      node.item.remove();
      tabNodes.delete(id);
      changed = true;
    }
    // Measured only when a tab came, went or changed width: reading the
    // strip's width on every status push would lay the page out again
    // several times a second.
    if (changed) markStripEdges();
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
    // F2 renames the tab the keyboard is on, as it renames a file in nearly
    // every file manager and editor. From the strip, renaming was otherwise a
    // double-click or a trip through the palette, and the palette's entry is
    // for the tab on screen rather than the one arrowed to.
    if (ev.key === "F2") { ev.preventDefault(); ev.stopPropagation(); renameTab(id); return; }
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
    // The tab and its close button sit side by side in one box, rather than
    // the one inside the other: a button inside a tab is a control inside a
    // control, which a screen reader reads as part of the tab and some cannot
    // reach at all. The box is nothing to a screen reader, and is what is
    // clicked, dragged and dropped on, as the whole tab was.
    const item = el("div", "tab");
    item.setAttribute("role", "presentation");
    const btn = el("button", "tab-btn");
    btn.setAttribute("role", "tab");
    btn.id = "tab-" + id;
    btn.setAttribute("aria-controls", "page-" + id);
    const label = el("span", "label");
    const close = el("button", "close", "×");
    describe(close, "Close tab");
    close.onclick = (ev) => { ev.stopPropagation(); send({ cmd: "closeTab", id }); };
    btn.append(label);
    item.append(btn, close);
    item.onclick = () => {
      // Re-clicking the tab already on screen produces no state change for
      // showActiveTab to react to, so hand the keyboard back here.
      if (state && state.activeTab === id) focusTerminal();
      else send({ cmd: "selectTab", id });
    };
    item.ondblclick = () => renameTab(id);
    // The middle button closes a tab, as it does a browser's or an editor's;
    // here it did nothing, or began the browser's autoscroll over the strip.
    item.addEventListener("mousedown", (ev) => { if (ev.button === 1) ev.preventDefault(); });
    item.onauxclick = (ev) => {
      if (ev.button !== 1) return;
      ev.preventDefault();
      send({ cmd: "closeTab", id });
    };
    item.onkeydown = (ev) => tabStripKey(ev, id);
    // Draggable itself as well, since some browsers start no drag from a
    // button inside a draggable box.
    btn.draggable = true;
    makeTabDraggable(id, item);
    node = { item, btn, label, close, attn: null };
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
    node.btn.append(attn);
    node.attn = attn;
  }

  /** renameTab asks for a tab's new name in a dialog of its own. The browser's
   *  prompt had room for a name and nothing else, and a tab named once kept
   *  that name for good: nothing offered its automatic title back, or said
   *  that an emptied name would give it. */
  function renameTab(id) {
    const tab = (state ? state.tabs : []).find((t) => t.id === id);
    if (!tab) return;
    openOverlay("Rename tab", "panes");
    dialog = "renameTab";
    const body = $("overlay-body");
    const form = el("div", "wt-form");
    const field = el("input");
    field.id = "tab-name";
    field.value = tab.title || "";
    field.placeholder = "Leave it empty for the automatic title";
    field.setAttribute("aria-label", "Tab name");
    field.autocomplete = "off";
    field.spellcheck = false;
    // Every answer goes, and the server says what it made of it. An emptied
    // name gives the tab its automatic title back, which is what the button
    // below sends too.
    const done = (text) => { send({ cmd: "renameTab", id, text }); closeOverlay(); };
    field.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); done(field.value); } };
    const ok = el("button", "chip primary", "Rename");
    ok.onclick = () => done(field.value);
    form.append(field, ok);
    // Offered only where there is a name to give up: on a tab that already
    // names itself it would do nothing.
    if (tab.named) {
      const auto = el("button", "chip", "Use the automatic title");
      auto.id = "tab-auto-title";
      describe(auto, "Drop this name and call the tab what it would be called had it never been renamed");
      auto.onclick = () => done("");
      form.append(auto);
    }
    body.append(form, el("div", "wt-hint", tab.named
      ? "You named this tab, so nothing renames it. An empty name, or Use the automatic title, gives it back the title it gives itself from the first thing its agent is asked."
      : "This tab names itself after the first thing its agent is asked. A name you give it here stays until you empty it again."));
    field.focus();
    field.select();
  }

  /** tally builds one "3 waiting" count: the dot is decoration in the status's
   *  colour, the words after it are the reading, and the tip explains the
   *  state being counted. The word is its own element so that a narrow
   *  screen can keep it for a screen reader and show only the figure. */
  function tally(cls, n, tip) {
    const span = el("span", cls);
    const dot = el("span", "tally-dot");
    dot.setAttribute("aria-hidden", "true");
    span.append(dot, document.createTextNode(String(n)), el("span", "tally-word", " " + cls));
    return describe(span, tip);
  }

  /** ICON_SIZES are the raster sizes rendered for every state below. The
   *  window is a Chromium app-mode window with no --app-icon, so the OS gets
   *  the taskbar/Alt-Tab icon by asking the page's own favicon links for a
   *  bitmap at whatever size it needs; offering only one size forced it to
   *  stretch that one bitmap for every other size it was asked for, which is
   *  what made the taskbar icon blurry at anything but exactly 32px. */
  const ICON_SIZES = [16, 32, 48, 128, 192, 256];

  /** ICONS maps a workspace state to the marks that stand for it: the vector
   *  source (crisp at any size a browser rasterizes it at, in the tab strip)
   *  and one crisp raster per ICON_SIZES (what a taskbar/window-icon request
   *  that does not rasterize SVG falls back to). This is what puts "an agent
   *  is waiting" in the taskbar while the window is behind three others and
   *  the title bar cannot be read. */
  const ICONS = {
    waiting: { svg: basePath + "assets/icon-waiting.svg", prefix: basePath + "assets/icon-waiting-" },
    working: { svg: basePath + "assets/icon.svg", prefix: basePath + "assets/icon-" },
    idle: { svg: basePath + "assets/icon-idle.svg", prefix: basePath + "assets/icon-idle-" },
  };
  let iconState = "";

  /** setFavicon points every icon link — the vector one and each sized raster
   *  fallback — and the rail's own mark at the top of the window, at the marks
   *  for state. Assigning the same href again makes some browsers drop and
   *  refetch the icon, which shows up as the tab flickering on every state
   *  push, so an unchanged state is left alone — and the pushes are frequent. */
  function setFavicon(state) {
    if (state === iconState) return;
    const link = $("favicon");
    if (!link) return;
    iconState = state;
    const icons = ICONS[state];
    link.href = icons.svg;
    for (const size of ICON_SIZES) {
      const sized = $("favicon-" + size);
      if (sized) sized.href = icons.prefix + size + ".png";
    }
    // The rail's mark is inline in the page rather than a favicon a browser
    // rasterizes on request, so it only ever needs the one vector source.
    const mark = $("rail-mark");
    if (mark) mark.setAttribute("src", icons.svg);
  }

  /** The counts the summary is currently showing. A status push arrives every
   *  time any agent changes what it is doing, and rewriting the tally
   *  unconditionally rebuilt it over and over, which with several agents
   *  running is continuously. It was a live region as well, which spoke the
   *  counts on every change of them; announceStatus says what matters now. */
  let summaryShown = "";

  /** What each pane was doing at the last push, for announceStatus. */
  const announced = new Map();

  /** announceStatus tells a screen reader, by name, about an agent that has
   *  started waiting on you or whose process has exited: the two changes
   *  that need a person. Working and idle are not news. Never on the first
   *  sighting of a pane, since arriving at a workspace where agents already
   *  wait is not the moment they stopped. */
  function announceStatus(s) {
    const said = [];
    for (const [id, v] of Object.entries(s.panes || {})) {
      const before = announced.get(id);
      announced.set(id, v.status);
      if (before === undefined || before === v.status) continue;
      const name = v.name || "An agent";
      if (v.status === "waiting") said.push(name + " is waiting on you");
      else if (v.status === "exited") said.push(name + " has exited");
    }
    for (const id of [...announced.keys()]) {
      if (!s.panes || !s.panes[id]) announced.delete(id);
    }
    if (!said.length) return;
    // Each announcement is a line of its own, so the same words said twice
    // are still an addition and are read again; only the last few are kept.
    const box = $("announcer");
    box.append(el("div", null, said.join(". ")));
    while (box.children.length > 3) box.firstChild.remove();
  }

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
      if (s.waiting > 0) box.append(tally("waiting", s.waiting, TIPS.waiting));
      // The gap between the two is the eye's; a screen reader needs a pause.
      if (s.waiting > 0 && s.working > 0) box.append(el("span", "sr-only", ", "));
      if (s.working > 0) box.append(tally("working", s.working, TIPS.working));
      // The button was named from the action table while it was still empty,
      // and a name set that way outlasts the counts written into it after:
      // tabbed to, it said what it opens and never who was waiting.
      const counts = [s.waiting > 0 && s.waiting + " waiting", s.working > 0 && s.working + " working"]
        .filter(Boolean).join(", ");
      const name = [counts, actionTip("agents")].filter(Boolean).join(" — ");
      if (name) box.setAttribute("aria-label", name);
      else box.removeAttribute("aria-label");
      setFavicon(s.waiting > 0 ? "waiting" : (s.working > 0 ? "working" : "idle"));
    }
    // Outside the counts' own check, so a window that said it was
    // disconnected says what it holds again with the first push after.
    const title = s.waiting > 0
      ? `▲ ${s.waiting} waiting · flockdeck`
      : (s.working > 0 ? `● ${s.working} working · flockdeck` : "flockdeck");
    if (document.title !== title) document.title = title;
    $("btn-broadcast").classList.toggle("on", !!s.broadcast);
    $("btn-broadcast").setAttribute("aria-pressed", String(!!s.broadcast));
  }

  // ------------------------------------------------------------------- rail

  /** The tile for each open project, by its folder, kept across the pushes
   *  for the reason the tab buttons are: rebuilt on each, the keyboard and a
   *  tooltip resting on one would go with it. */
  const railTiles = new Map();
  /** What the tiles were last drawn from, so a push that changes nothing they
   *  show - which is nearly every push - is not drawn at all. */
  let railShown = "";
  /** The rail's one stop for Tab. The arrows move it. Until somebody does,
   *  it is the project on screen, and follows it from project to project. */
  let railStop = null;
  let railStopChosen = false;

  /** renderRail draws a tile for each open project: its monogram, which one
   *  is on screen, and an amber badge where an agent is waiting on you. That
   *  last is what the rail is for - a project with somebody blocked in it is
   *  seen from any other without opening anything. */
  function renderRail(s) {
    const projects = s.projects || [];
    const key = JSON.stringify(projects.map((p) => [p.root, p.name, !!p.active, p.waiting || 0]));
    if (key === railShown) return;
    railShown = key;
    const box = $("rail-projects");
    const monos = monograms(projects.map((p) => p.name));
    projects.forEach((p, i) => {
      let t = railTiles.get(p.root);
      if (!t) { t = railTile(p.root); railTiles.set(p.root, t); }
      t.mono.textContent = monos[i];
      t.name.textContent = p.name;
      t.btn.classList.toggle("current", !!p.active);
      if (p.active) t.btn.setAttribute("aria-current", "true");
      else t.btn.removeAttribute("aria-current");
      t.badge.classList.toggle("waiting", (p.waiting || 0) > 0);
      // Two letters say which project to somebody who already knows; the
      // name and the folder say it to everybody else, and two checkouts of
      // one repository have the same name and only the folder differs.
      const waiting = p.waiting
        ? ", " + (p.waiting === 1 ? "an agent is" : p.waiting + " agents are") + " waiting on you" : "";
      const says = p.name + " — " + p.root + waiting;
      t.btn.setAttribute("aria-label", says);
      describe(t.btn, p.active ? says + ". The project on screen." : says);
      if (box.children[i] !== t.btn) box.insertBefore(t.btn, box.children[i] || null);
    });
    for (const [root, t] of railTiles) {
      if (projects.some((p) => p.root === root)) continue;
      if (tipFor && t.btn.contains(tipFor)) hideTip();
      t.btn.remove();
      railTiles.delete(root);
    }
    placeRailStop();
  }

  /** What the toggle's badge was last drawn from, so a push that changes
   *  nothing it shows - which is nearly every push - does not search the
   *  document for it. */
  let railToggleShown = "";

  /** renderRailToggle badges the button that folds the rail away with the
   *  same amber dot a tile carries, for the one time a tile cannot be seen: a
   *  narrow window, where the rail itself is out of sight until this button
   *  is pressed. It counts every project but the one on screen, so an agent
   *  waiting in the project already open — visible without opening anything —
   *  does not light a button that has nothing new to show. Open, the tiles
   *  underneath already carry the badge, so the style sheet hides this copy
   *  of it rather than showing the same thing twice. */
  function renderRailToggle(s) {
    const elsewhere = (s.projects || []).filter((p) => !p.active && (p.waiting || 0) > 0);
    const key = elsewhere.map((p) => p.root + ":" + p.name).join("|");
    if (key === railToggleShown) return;
    railToggleShown = key;
    const btn = $("rail-toggle");
    btn.querySelector(".rail-badge").classList.toggle("waiting", elsewhere.length > 0);
    const why = elsewhere.length
      ? (elsewhere.length === 1 ? "An agent is" : "Agents are") + " waiting on you in " +
        elsewhere.map((p) => p.name).join(", ")
      : "";
    describe(btn, ["Projects, tools and settings", why].filter(Boolean).join(". "));
  }

  /** railTile makes the button for one project, bound to its folder rather
   *  than to the record it arrived in. */
  function railTile(root) {
    const btn = el("button", "rail-btn rail-tile");
    btn.dataset.root = root;
    const mono = el("span", "mono");
    mono.setAttribute("aria-hidden", "true");
    const badge = el("span", "rail-badge");
    badge.setAttribute("aria-hidden", "true");
    const name = el("span", "rail-label");
    btn.append(mono, badge, name);
    btn.onclick = () => {
      closeRailMenu(false);
      const p = ((state && state.projects) || []).find((x) => x.root === root);
      // The project already on screen: the click was for its terminal.
      if (p && p.active) focusTerminal();
      else send({ cmd: "selectProject", root });
    };
    return { btn, mono, badge, name };
  }

  /** monograms gives each name two letters, and no two the same: the first
   *  letter of the first word and of the last, and where that is taken, the
   *  first with a later consonant of the last word - flockdeck-relay and
   *  flockdeck-remote are FR and FM, not FR twice. A name of one word starts
   *  from its first two letters. Taken in the order the projects were opened,
   *  so a project keeps its letters while the ones after it come and go. */
  function monograms(names) {
    const taken = new Set();
    return names.map((name) => {
      const pick = monogramCandidates(name).find((c) => !taken.has(c)) || String(taken.size + 1);
      taken.add(pick);
      return pick;
    });
  }

  function monogramCandidates(name) {
    const words = String(name || "").replace(/([\p{Ll}\p{N}])(\p{Lu})/gu, "$1 $2")
      .split(/[^\p{L}\p{N}]+/u).filter(Boolean);
    const out = [];
    const add = (a, b) => {
      const m = (a + (b || "")).toUpperCase();
      if (a && !out.includes(m)) out.push(m);
    };
    if (!words.length) return ["?"];
    const first = [...words[0]][0];
    const last = [...words[words.length - 1]];
    if (words.length > 1) {
      add(first, last[0]);
      last.slice(1).filter((c) => !/[aeiouy]/i.test(c)).forEach((c) => add(first, c));
      words.slice(1, -1).forEach((w) => add(first, [...w][0]));
    }
    const letters = [...words.join("")];
    if (letters.length === 1) add(first);
    letters.slice(1).forEach((c) => add(first, c));
    for (let d = 2; d <= 9; d++) add(first, String(d));
    return out;
  }

  /** railButtons is every button in the rail, in the order the arrows walk.
   *  offsetParent leaves out the collapse button on a narrow screen, where
   *  the style sheet hides it rather than the rail folding into a menu that
   *  already takes its shape - display:none rather than the hidden
   *  attribute, so it takes a layout check to notice. */
  function railButtons() {
    return [...$("rail").querySelectorAll("button")].filter((b) => !b.hidden && !b.disabled && b.offsetParent !== null);
  }

  /** placeRailStop keeps exactly one of the rail's buttons as its stop for
   *  Tab: the one last used, or the project on screen to begin with. Ten
   *  projects and seven tools are otherwise seventeen presses of Tab between
   *  the top bar and anything past the rail. */
  function placeRailStop(to) {
    const all = railButtons();
    if (to) { railStop = to; railStopChosen = true; }
    if (!railStopChosen || !railStop || !railStop.isConnected || !all.includes(railStop)) {
      railStopChosen = false;
      railStop = all.find((b) => b.classList.contains("current")) || all[0] || null;
    }
    all.forEach((b) => {
      const stop = b === railStop ? 0 : -1;
      if (b.getAttribute("tabindex") !== String(stop)) b.tabIndex = stop;
    });
  }

  /** railKey walks the rail with the arrow keys, up and down as it stands, and
   *  left and right as well for anybody who reaches for those. */
  function railKey(ev) {
    const all = railButtons();
    const at = all.indexOf(document.activeElement);
    if (at < 0) return;
    let to;
    if (ev.key === "ArrowDown" || ev.key === "ArrowRight") to = (at + 1) % all.length;
    else if (ev.key === "ArrowUp" || ev.key === "ArrowLeft") to = (at - 1 + all.length) % all.length;
    else if (ev.key === "Home") to = 0;
    else if (ev.key === "End") to = all.length - 1;
    else return;
    ev.preventDefault();
    ev.stopPropagation();
    placeRailStop(all[to]);
    all[to].focus();
    all[to].scrollIntoView({ block: "nearest" });
  }

  /* On a narrow screen the rail is folded into a menu opened from the top bar
   * (the style sheet decides where; this only opens and closes it). While it
   * is open it holds the keyboard as a dialog does, and Escape, a tap beside
   * it or choosing anything in it puts it away. */
  function railMenuOpen() { return document.body.classList.contains("rail-open"); }

  function openRailMenu() {
    document.body.classList.add("rail-open");
    $("rail-toggle").setAttribute("aria-expanded", "true");
    // The menu opens on the project on screen: it is where the list of them
    // is read from.
    placeRailStop(railButtons().find((b) => b.classList.contains("current")));
    if (railStop) railStop.focus();
  }

  /** closeRailMenu puts the menu away, and the keyboard back on the button
   *  that opened it where nothing else is about to take it. */
  function closeRailMenu(refocus) {
    if (!railMenuOpen()) return;
    document.body.classList.remove("rail-open");
    $("rail-toggle").setAttribute("aria-expanded", "false");
    if (refocus) $("rail-toggle").focus();
  }

  /** railFolded is whether the rail is a menu at the window's width now: the
   *  button that opens it is only on screen while it is. */
  function railFolded() { return $("rail-toggle").offsetParent !== null; }

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
  // Whether this drag actually reordered, merged or moved something -- see
  // endDrag. Reset at the start of every drag.
  let dragHandled = false;

  function beginDrag(kind, id, ev, node) {
    dragging = { kind, id };
    dragHandled = false;
    node.classList.add("dragging");
    if (ev.dataTransfer) {
      ev.dataTransfer.effectAllowed = "move";
      // Some data must be set for a drag to start at all in some browsers.
      ev.dataTransfer.setData("text/plain", kind + ":" + id);
    }
  }

  function endDrag() {
    const wasTab = dragging && dragging.kind === "tab";
    const draggedTabId = wasTab ? dragging.id : null;
    const handled = dragHandled;
    dragging = null;
    dragHandled = false;
    document.querySelectorAll(".dragging").forEach((n) => n.classList.remove("dragging"));
    clearDropMarks();
    // The strip was left alone while a tab was being dragged; catch it up now
    // rather than waiting for a state push, which may not come if every agent
    // is idle.
    if (wasTab && state) renderTabs(state);
    // A tab is draggable, so the browser reads a click on it as the start of a
    // drag the moment the pointer moves a pixel or two between mousedown and
    // mouseup -- which a real mouse or trackpad does on almost every press.
    // Once it does, the click this would otherwise have been never fires at
    // all. A drag that neither reordered, merged nor moved anything is that
    // swallowed click, so it is answered as one: by selecting the tab it
    // began on, exactly as the click would have.
    if (draggedTabId && !handled && state && state.activeTab !== draggedTabId) {
      send({ cmd: "selectTab", id: draggedTabId });
    }
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

  /** moveTabBy moves the tab on screen one place along the strip, which was
   *  otherwise done only by dragging it. moveTab puts a tab in front of the
   *  one it names, or last for "". */
  function moveTabBy(step) {
    if (!state) return;
    const tabs = state.tabs;
    const at = tabs.findIndex((t) => t.id === state.activeTab);
    if (at < 0) return;
    const to = at + step;
    if (to < 0 || to >= tabs.length) {
      notice(step < 0 ? "This tab is already the first" : "This tab is already the last", false);
      return;
    }
    send({ cmd: "moveTab", id: tabs[at].id, target: step < 0 ? tabs[to].id : tabAfter(tabs[to].id) });
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
      dragHandled = true;
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
    // A file dragged in from outside is none of the above, and the browser's
    // own answer to one being dropped on a page that does not claim it is to
    // open the file in the page's place. In this window that took the whole
    // application away - there is no back button to return by - for a
    // gesture a terminal user makes expecting the path to be typed. The page
    // cannot learn a dropped file's path, so the drop is refused outright,
    // which the pointer shows while the file is still held over the window.
    const fromOutside = (ev) => !dragging && ev.dataTransfer &&
      Array.from(ev.dataTransfer.types || []).includes("Files");
    document.addEventListener("dragover", (ev) => {
      if (!fromOutside(ev)) return;
      ev.preventDefault();
      ev.dataTransfer.dropEffect = "none";
    });
    document.addEventListener("drop", (ev) => { if (fromOutside(ev)) ev.preventDefault(); });
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
      // The rule at the end of the bar means the same for either: a tab moves
      // there, and a pane arrives there as a tab of its own. Shown only for a
      // tab, dragging a pane to the bar looked like a gesture that does
      // nothing right up until it was released.
      strip.classList.add("drop-end");
    });
    strip.addEventListener("dragleave", (ev) => {
      if (!strip.contains(ev.relatedTarget)) strip.classList.remove("drop-end");
    });
    strip.addEventListener("drop", (ev) => {
      if (!dragging || ev.target.closest(".tab")) return;
      ev.preventDefault();
      const { kind, id } = dragging;
      clearDropMarks();
      dragHandled = true;
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
      dragHandled = true;
      if (kind === "tab") send({ cmd: "moveTab", id, target: "" });
      else send({ cmd: "movePaneToNewTab", id });
    });
  }

  // ------------------------------------------------------------------ panes

  function ensurePane(id) {
    let p = panes.get(id);
    if (p) return p;

    const wrap = el("div", "pane");
    // A group named after the pane. Every terminal announces itself alike,
    // and the header beside it names nothing, so in a tab of six agents a
    // screen reader landing in one was not told whose it was.
    wrap.setAttribute("role", "group");
    const header = el("div", "pane-header");
    const dot = el("span", "dot");
    const name = el("span", "pane-name");
    const project = el("span", "pane-project");
    const branch = el("span", "pane-branch");
    const agent = el("span", "pane-agent");
    // Shown while a phone -- this one's own, or a colleague's -- has this
    // pane open in its chat view or its terminal, so text appearing here
    // unbidden has an explanation instead of looking like the program on its
    // own. See renderPaneRemote.
    const remote = el("span", "pane-remote");
    const detail = el("span", "pane-detail");
    const git = el("span", "pane-git");
    // The counts say something changed, and nothing went from them to what
    // did: the review was a trip to the top bar, and for the focused pane
    // only. The checkout is read at the click, so a pane that moved is
    // reviewed where it is now.
    git.onclick = (ev) => {
      ev.stopPropagation();
      const v = state && state.panes && state.panes[id];
      if (v && v.cwd) openChanges(v.cwd);
    };
    const usage = el("span", "pane-usage");
    const spend = el("span", "pane-spend");
    const limit = el("span", "pane-limit");
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
    const castBtn = btn("⇉", "Adds this pane to the broadcast set, or takes it out again. " + TIPS.broadcast,
      () => send({ cmd: "toggleBroadcastMember", id }));
    const zoomBtn = btn("⤢", TIPS.zoom, () => send({ cmd: "toggleZoom", id }));
    const reviewBtn = btn("✓", TIPS.autoReview, () => {
      const v = state && state.panes && state.panes[id];
      send({ cmd: "autoReview", id, autoReview: !(v && v.autoReview) });
    });
    actions.append(
      btn("⑂", TIPS.fanOut, () => openFanout(id)),
      castBtn,
      reviewBtn,
      btn("⟳", TIPS.restart, () => send({ cmd: "restartPane", id })),
      zoomBtn,
      btn("×", TIPS.close, () => send({ cmd: "closePane", id })),
    );
    makeToolbar(actions, "What to do with this pane");
    header.append(dot, project, name, branch, agent, remote, git, detail, spend, limit, usage, cast, actions);

    const body = el("div", "pane-body");
    const host = el("div", "term-host");
    body.append(host);
    // The drop indicator sits above the terminal and takes no pointer events,
    // so a drag crossing a pane is not swallowed by the xterm canvas.
    const dropZone = el("div", "pane-drop");
    dropZone.hidden = true;
    wrap.append(header, body, dropZone);
    makePaneDraggable(id, wrap, header, dropZone);
    // Double-clicking the header zooms the pane, as double-clicking a title
    // bar does a window: the button for it is a small glyph at the far end of
    // the header, and the header itself did nothing. Not on the header's own
    // buttons, which have their own business.
    header.addEventListener("dblclick", (ev) => {
      // Nor on the change counts, whose click opens the review over the pane.
      if (ev.target.closest && (ev.target.closest("button") || ev.target.closest(".pane-git"))) return;
      send({ cmd: "toggleZoom", id });
    });

    const claimFocus = () => {
      if (state && currentTab() && currentTab().focus !== id) send({ cmd: "focusPane", id });
    };
    wrap.addEventListener("mousedown", claimFocus);
    // The keyboard arriving counts as much as the pointer. Tabbed into from
    // its own header buttons, a terminal took the typing while the server
    // went on treating the last pane clicked as the focused one: that one
    // kept the highlighted border, and it was the one Close pane closed.
    wrap.addEventListener("focusin", claimFocus);

    const term = new Terminal({
      allowProposedApi: true,
      cursorBlink: cursorBlinks(),
      cursorStyle: cursorShape(),
      fontFamily: terminalFont(),
      fontSize: fontSize,
      lineHeight: 1.15,
      scrollback: scrollback,
      // Drawn on a canvas, what an agent writes is nothing a screen reader
      // can see; this keeps an accessible copy of the lines beside it.
      screenReaderMode: !!prefs.screenReader,
      // A program can print a link whose text is not its address - an OSC 8
      // hyperlink, as gh and ls --hyperlink print theirs - and xterm's own
      // fallback asks with the browser's confirm box and then navigates,
      // which in an app window is the application gone. It opens the way
      // every other link out of the window does.
      linkHandler: { activate: (ev, uri) => openExternal(uri) },
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
      if (search.onDidChangeResults) {
        search.onDidChangeResults((r) => { if (searchPane === id) showMatchCount(r); });
      }
    } catch {
      /* Search is a convenience; the terminal works without it. */
    }
    term.open(host);
    term.attachCustomKeyEventHandler((ev) => terminalKey(term, ev));
    // A paste goes to a program that asked for bracketed paste between two
    // markers, so it can tell text from keys, and xterm sends the text between
    // them as it was. A copied ESC[201~ ended the paste early, and whatever
    // followed it - a command and its newline - was typed at the program as
    // keys. No escape character in a paste is anything a person means to
    // send, so text holding one is pasted without them. Caught on the way down
    // to xterm's own textarea, before its handler sees the event.
    host.addEventListener("paste", (ev) => {
      const text = ev.clipboardData && ev.clipboardData.getData("text/plain");
      if (!text || !/[\x1b\x9b]/.test(text)) return;
      ev.preventDefault();
      ev.stopPropagation();
      term.paste(text.replace(/[\x1b\x9b]/g, ""));
    }, true);
    // Scrolled back to read what an agent wrote earlier, a pane went on
    // showing the old lines with nothing to say newer ones had arrived below,
    // and the only way back down was the wheel, however far that was.
    const latest = el("button", "to-latest", "↓ Latest");
    latest.hidden = true;
    describe(latest, "Scroll to the newest output");
    latest.onclick = (ev) => { ev.stopPropagation(); term.scrollToBottom(); latest.hidden = true; term.focus(); };
    body.append(latest);
    const scrolledBack = () => {
      const b = term.buffer && term.buffer.active;
      const back = !!b && b.viewportY < b.baseY;
      if (latest.hidden === back) latest.hidden = !back;
    };
    if (term.onScroll) term.onScroll(scrolledBack);
    if (term.onWriteParsed) term.onWriteParsed(scrolledBack);
    // The WebGL renderer is loaded when the pane comes on screen, not here:
    // see drawWithWebgl.

    p = { id, wrap, header, dot, name, project, branch, agent, remote, git, detail, usage, spend, limit, cast, body, host, term, fit, ws: null,
          nodeId: "", fitTimer: 0, retryTimer: 0, retries: 0, flingTimer: 0, cols: 0, rows: 0, actions, castBtn, zoomBtn, reviewBtn, search, dropZone,
          // What each part of the header is currently showing. Empty to begin
          // with, so the first push draws all of it.
          shown: {} };
    panes.set(id, p);
    watchTouch(p);

    // Focusing or tapping the terminal is using the pane in this window.
    if (term.textarea) term.textarea.addEventListener("focus", () => sendFocus(p));
    host.addEventListener("pointerdown", () => sendFocus(p));
    // While the replay is being drawn, what the terminal says of its own
    // accord is its answer to history, not to the program running now.
    term.onData((data) => { if (!(p.replaying && isTerminalReply(data))) sendInput(p, data); });
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

  // wheelNotch is how far a finger has to move before another wheel event is
  // sent for it, and wheelGapMs how long a dispatch waits for the last one at
  // the earliest. The scrollback's own scrollbar animates each wheel event
  // it is sent rather than jumping straight there, and one sent before the
  // last has finished only restarts it: distance alone was not enough to
  // gate on, since a fast drag crosses 24px in far less time than that
  // animation takes to settle, and kept restarting it the same way a stream
  // of one-pixel events would have. Whatever is left over when a drag ends
  // before either bound is reached is sent anyway, in touchend below, so a
  // short drag still moves something.
  const wheelNotch = 24;
  const wheelGapMs = 60;

  /** watchTouch scrolls a pane's terminal under a dragging finger. xterm
   *  scrolls for a wheel but never for touch, so a phone or tablet reaching
   *  the desktop through the relay had no way to read anything above the
   *  screen at all. Decided once per drag, as the browser decides whether a
   *  touch is a pan: from the first real movement, a mostly vertical drag is
   *  the terminal's and a mostly horizontal one is left alone.
   *
   *  What the drag becomes is a wheel event, not a call into xterm's own
   *  scrollback: a program on the alternate screen - Claude Code, vim, less -
   *  has no scrollback of its own, and it is xterm's own wheel handling, not
   *  scrollLines, that turns a wheel there into the arrow keys such a
   *  program reads. Dispatched on the terminal's canvas, the one event
   *  reaches both that handling and the scrollback of a plain program, so
   *  one code path serves whatever is running. */
  function watchTouch(p) {
    const el = p.host;
    let drag = null;
    const wheelTarget = () => el.querySelector(".xterm-screen") || (p.term && p.term.element) || el;
    const sendWheel = (deltaY, x, y) => {
      const t = wheelTarget();
      if (!deltaY || !t || !t.isConnected) return;
      // wheelDeltaY as well as deltaY: the normal buffer's own scrollback is
      // drawn by a scrollbar widget that reads the legacy property, in its
      // legacy sign - the opposite of deltaY's - and a synthetic event does
      // not get one made up for it the way a real wheel's does. Without it
      // supplied, a drag scrolled the wrong buffer the wrong way as often as
      // not. The alternate screen's own handling - the arrow keys a program
      // there reads - looks only at deltaY, so this changes nothing for it.
      t.dispatchEvent(new WheelEvent("wheel", {
        deltaY, wheelDeltaY: -deltaY, clientX: x, clientY: y, bubbles: true, cancelable: true,
      }));
    };
    el.addEventListener("touchstart", (e) => {
      // A finger put down on a flung scroll catches it, as a hand on a page.
      clearTimeout(p.flingTimer);
      const t = e.touches[0];
      drag = e.touches.length === 1
        ? { x: t.clientX, y: t.clientY, mine: null, lastY: t.clientY, pending: 0, sentAt: 0, at: Date.now(), speed: 0 }
        : null;
    }, { passive: true });
    el.addEventListener("touchmove", (e) => {
      if (!drag || e.touches.length !== 1) return;
      const t = e.touches[0];
      const dx = t.clientX - drag.x;
      const dy = t.clientY - drag.y;
      if (drag.mine === null) {
        if (Math.abs(dx) < 6 && Math.abs(dy) < 6) return;
        // Decided once, on the first real movement, and kept for the drag.
        drag.mine = Math.abs(dy) >= Math.abs(dx);
      }
      if (!drag.mine) return;
      e.preventDefault();
      const now = Date.now();
      const step = t.clientY - drag.lastY;
      if (now > drag.at) {
        // Pixels a millisecond, weighted to the last few moves, so a flick
        // still reads as fast even when its final move happened to be small.
        drag.speed = 0.8 * (step / (now - drag.at)) + 0.2 * drag.speed;
        drag.at = now;
      }
      drag.lastY = t.clientY;
      drag.pending += step;
      if (Math.abs(drag.pending) >= wheelNotch && now - drag.sentAt >= wheelGapMs) {
        // A finger moving down brings earlier output into view, exactly as
        // dragging a page does; the wheel's own sign says the same thing.
        sendWheel(-drag.pending, t.clientX, t.clientY);
        drag.pending = 0;
        drag.sentAt = now;
      }
    }, { passive: false });
    el.addEventListener("touchend", (e) => {
      const d = drag;
      drag = null;
      if (!d) return;
      // What has not reached a whole notch, or the gap, is not lost: a short
      // or slow drag would otherwise scroll nothing at all.
      const t = e.changedTouches[0];
      if (d.mine && d.pending) sendWheel(-d.pending, t ? t.clientX : d.x, d.lastY);
      if (d.mine && Math.abs(d.speed) >= 0.3 && Date.now() - d.at < 100) fling(p, d.speed, d.lastY);
    });
    el.addEventListener("touchcancel", () => { drag = null; });
  }

  /** fling carries a scroll on after a flick, at speed pixels a millisecond,
   *  slowing until it is too slow to see or the pane it belongs to is gone.
   *  Ticking every wheelGapMs rather than every frame, for the same reason
   *  watchTouch waits that long between sends: a wheel event most every
   *  frame raced the scrollback's own animation and barely moved it. */
  function fling(p, speed, y) {
    clearTimeout(p.flingTimer);
    const tick = () => {
      if (!panes.has(p.id) || !p.host.isConnected) return;
      speed *= Math.pow(0.95, wheelGapMs / 16);
      if (Math.abs(speed) < 0.05) return;
      const deltaY = -speed * wheelGapMs;
      const t = p.host.querySelector(".xterm-screen") || (p.term && p.term.element) || p.host;
      if (t.isConnected) {
        // See sendWheel in watchTouch for why wheelDeltaY is set alongside it.
        t.dispatchEvent(new WheelEvent("wheel", { deltaY, wheelDeltaY: -deltaY, clientY: y, bubbles: true, cancelable: true }));
      }
      p.flingTimer = setTimeout(tick, wheelGapMs);
    };
    p.flingTimer = setTimeout(tick, wheelGapMs);
  }

  /** drawWithWebgl gives a pane's terminal a WebGL renderer while it is on
   *  screen, and takes it away when the pane goes out of sight. Every pane had
   *  one, in every tab, and Chromium keeps about sixteen WebGL contexts before
   *  it takes the oldest away: with a dozen agents across the tabs, panes on
   *  screen lost theirs to panes nobody could see. Without one a terminal
   *  draws in the DOM, which is what a hidden pane needs anyway. */
  function drawWithWebgl(p, on) {
    if (on === !!p.webgl) return;
    if (!on) {
      const g = p.webgl;
      p.webgl = null;
      try { g.dispose(); } catch { /* already gone with its context */ }
      return;
    }
    // A browser without WebGL says so once; asking again on every push
    // would build and throw away a renderer each time.
    if (p.noWebgl || typeof WebglAddon === "undefined") return;
    try {
      const g = new WebglAddon.WebglAddon();
      // A context the browser takes back is given up, and the pane asks for
      // another the next time it comes on screen.
      g.onContextLoss(() => {
        if (p.webgl === g) p.webgl = null;
        try { g.dispose(); } catch { /* already gone */ }
      });
      p.term.loadAddon(g);
      p.webgl = g;
    } catch {
      /* Canvas rendering is a fine fallback. */
      p.noWebgl = true;
    }
  }

  /** reducedMotion reports whether the system has been asked for less
   *  movement on screen. The style sheet already stills its animations for
   *  it, but a terminal's cursor blinks by script, not by CSS, so every one
   *  of them went on blinking for somebody who had asked for it to stop. */
  function reducedMotion() {
    try { return matchMedia("(prefers-reduced-motion: reduce)").matches; } catch { return false; }
  }

  /** cursorBlinks is whether the terminal cursors should blink: not where the
   *  system asks for reduced motion, and not where somebody has turned the
   *  blinking off from the palette, which was otherwise fixed in the source. */
  function cursorBlinks() { return !prefs.cursorSteady && !reducedMotion(); }
  function applyCursorBlink() {
    for (const p of panes.values()) p.term.options.cursorBlink = cursorBlinks();
  }

  /** cursorShape is the shape the terminal cursors are drawn in: a block
   *  unless a bar or an underline has been chosen in the settings. */
  function cursorShape() {
    return prefs.cursorStyle === "bar" || prefs.cursorStyle === "underline" ? prefs.cursorStyle : "block";
  }
  function applyCursorStyle() {
    const shape = cursorShape();
    for (const p of panes.values()) {
      if (p.term.options.cursorStyle !== shape) p.term.options.cursorStyle = shape;
    }
  }
  /** chooseCursorStyle makes a shape every terminal's, here at once and in
   *  every other window when the preference comes back round. */
  function chooseCursorStyle(shape) {
    prefs.cursorStyle = shape === "block" ? "" : shape;
    sentPref("cursorStyle");
    applyCursorStyle();
    send({ cmd: "cursorStyle", text: shape });
    settingsChanged();
  }
  // The system setting can change while the window is open, and the
  // terminals already drawn follow it.
  try {
    matchMedia("(prefers-reduced-motion: reduce)").addEventListener("change", applyCursorBlink);
  } catch { /* an engine without matchMedia keeps the blink */ }

  /** makeToolbar makes a row of buttons one stop on the way through the window,
   *  walked with the arrow keys.
   *
   *  Five buttons per pane is thirty tab stops in a tab of six agents, and they
   *  sit between the top bar and the terminals, so reaching a terminal from the
   *  keyboard meant pressing Tab past every one of them. A toolbar is the shape
   *  this already is; it just did not say so. */
  function makeToolbar(bar, label) {
    bar.setAttribute("role", "toolbar");
    bar.setAttribute("aria-label", label);
    const buttons = [...bar.children];
    buttons.forEach((b, i) => { b.tabIndex = i === 0 ? 0 : -1; });
    bar.addEventListener("keydown", (ev) => {
      const at = buttons.indexOf(document.activeElement);
      if (at < 0) return;
      let to;
      if (ev.key === "ArrowRight") to = (at + 1) % buttons.length;
      else if (ev.key === "ArrowLeft") to = (at - 1 + buttons.length) % buttons.length;
      else if (ev.key === "Home") to = 0;
      else if (ev.key === "End") to = buttons.length - 1;
      else return;
      ev.preventDefault();
      ev.stopPropagation();
      // The stop stays where it was left, so coming back to this pane's
      // buttons returns to the one last used rather than to the start.
      buttons[at].tabIndex = -1;
      buttons[to].tabIndex = 0;
      buttons[to].focus();
    });
  }

  /** isTerminalReply is the server's isTerminalReply: whether what the
   *  terminal sends is it answering its program rather than somebody typing.
   *  An OSC, DCS, APC or PM string answers a question about colours or
   *  settings; a CSI ending in R, c, n or t is a cursor position, the device
   *  attributes, a status or a window report; one ending in $y is a mode's
   *  state; ESC [ I and ESC [ O report focus. Shift+F3 and a cursor report
   *  can be the same bytes, and that key going nowhere while a replay is
   *  drawn is the price. */
  function isTerminalReply(s) {
    if (s.length < 3 || s[0] !== "\x1b") return false;
    if ("]P_^".includes(s[1])) return true;
    if (s[1] !== "[") return false;
    const last = s[s.length - 1];
    if ("Rcnt".includes(last)) return true;
    if (last === "I" || last === "O") return s.length === 3;
    return last === "y" && s.includes("$");
  }

  /** terminalKey is a terminal's say over its own keys, and answers false for
   *  a key the terminal is to leave alone. Ctrl+C with a selection sent ^C
   *  and Ctrl+V sent ^V, so text could be copied out of a pane and pasted
   *  into one only with the mouse. They go the way Windows Terminal has them:
   *  Ctrl+C copies when something is selected and is ^C otherwise, Ctrl+Shift+C
   *  copies, and Ctrl+V and Ctrl+Shift+V are left to the browser, which pastes
   *  through xterm's own paste handling. A Mac copies and pastes with Cmd, and
   *  its Ctrl keys stay the program's. */
  function terminalKey(term, ev) {
    if (onMac || ev.type !== "keydown" || !ev.ctrlKey || ev.altKey || ev.metaKey) return true;
    let k = (ev.key || "").toLowerCase();
    // A layout without Latin letters says which letter by the key's place.
    if (/^[^\x00-\x7f]$/.test(k)) {
      const m = /^Key([A-Z])$/.exec(ev.code || "");
      if (m) k = m[1].toLowerCase();
    }
    if (k === "v") return false;
    if (k !== "c" || (!ev.shiftKey && !term.hasSelection())) return true;
    // Kept from the browser too, whose Ctrl+Shift+C opens its inspector.
    ev.preventDefault();
    const text = term.getSelection();
    if (!text) return false;
    // The selection goes once it is copied, as it does in Windows Terminal,
    // so the next Ctrl+C interrupts the program rather than copying again.
    term.clearSelection();
    const failed = () => notice("The clipboard could not be reached, so nothing was copied", true);
    try {
      const clip = window.navigator && window.navigator.clipboard;
      if (clip && clip.writeText) clip.writeText(text).catch(failed);
      else failed();
    } catch { failed(); }
    return false;
  }

  function sendInput(p, data) {
    sendBytes(p, new TextEncoder().encode(data));
  }
  /** sendFocus tells the server this window's terminal is the one being used,
   *  so the pane is sized for it when more than one window is watching. The
   *  keystrokes that follow would say so too; this is for the glance, and the
   *  scroll, that comes before them. */
  function sendFocus(p) {
    if (p.ws && p.ws.readyState === WebSocket.OPEN) p.ws.send(JSON.stringify({ focus: true }));
  }
  /** INPUT_FRAME is the most input one frame carries. The server takes a
   *  frame of up to 4 MiB from a terminal and drops the connection over a
   *  bigger one, so a paste of more than that was lost without a word. */
  const INPUT_FRAME = 1 << 20;
  function sendBytes(p, bytes) {
    if (!p.ws || p.ws.readyState !== WebSocket.OPEN) return;
    if (bytes.length <= INPUT_FRAME) { p.ws.send(bytes); return; }
    for (let at = 0; at < bytes.length; at += INPUT_FRAME) p.ws.send(bytes.subarray(at, at + INPUT_FRAME));
  }

  function connectPTY(p) {
    if (p.ws) { try { p.ws.close(); } catch {} }
    clearTimeout(p.retryTimer);

    // A reconnect says how much of the pane's output this terminal already
    // holds, so it is sent only what it missed and keeps its scrollback and
    // the place the person had scrolled to. p.stream is what the server last
    // said about the stream; before it has said anything, this holds nothing.
    // A stream that has delivered nothing yet is not resumed: its header's
    // place counts the terminal modes put back ahead of the replay, and a
    // terminal that never received them would carry on without them.
    const held = p.stream && !p.stream.fresh ? "&epoch=" + p.stream.epoch + "&from=" + p.stream.offset : "&from=-1";
    const ws = new WebSocket(wsBase + basePath + "ws/pty?id=" + encodeURIComponent(p.id) + held);
    ws.binaryType = "arraybuffer";
    p.ws = ws;

    ws.onopen = () => {
      p.retries = 0;
      p.cols = p.rows = 0; // force the size to be re-reported
      scheduleFit(p);
      // A socket that reconnects is a new window as far as the server can
      // tell, so the terminal that has the keyboard says again that it is the
      // one in use.
      if (document.hasFocus() && p.host.contains(document.activeElement)) sendFocus(p);
    };
    ws.onmessage = (ev) => {
      // Text opens a run of the pane's output: where the bytes that follow
      // begin, and whether they carry on from what this terminal shows or it
      // has to start again -- the first time, after a restart, or when what it
      // missed is more than the server still holds.
      if (typeof ev.data === "string") {
        let h;
        try { h = JSON.parse(ev.data); } catch { return; }
        // Reset in the stream's own order, as RIS written to it. reset()
        // acts at once while write() only queues, so bytes of the old run
        // still queued - a slow window dropped and reconnected, or a restart
        // on the same socket - were drawn after it, on the fresh screen.
        if (!h.resumed) p.term.write("\x1bc");
        p.stream = { epoch: h.epoch, offset: h.offset, fresh: !h.resumed };
        // The bytes up to h.end are history, printed before this window
        // connected, and the terminal answers the questions in them - what
        // it is, where its cursor is, what colour it is - as though they had
        // just been asked. Until it has drawn the last of them, its answers
        // are kept from the program (see isTerminalReply). A server that does
        // not say where the replay ends leaves them all to go through.
        p.replayGen = (p.replayGen || 0) + 1;
        p.replayEnd = h.end > h.offset ? h.end : 0;
        p.replaying = p.replayEnd > 0;
        return;
      }
      if (p.stream) { p.stream.offset += ev.data.byteLength; p.stream.fresh = false; }
      const bytes = new Uint8Array(ev.data);
      if (p.replayEnd && p.stream && p.stream.offset >= p.replayEnd) {
        // The last of the replay. Once the terminal has drawn it, answers
        // are the program's again - unless another run has begun since.
        p.replayEnd = 0;
        const gen = p.replayGen;
        p.term.write(bytes, () => { if (p.replayGen === gen) p.replaying = false; });
        return;
      }
      p.term.write(bytes);
    };
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
    // Not while a divider is being dragged. A fit that lands mid-drag resizes
    // the agent's terminal, and an agent redraws its whole screen on every
    // resize, so a slow drag across a split was a stream of redraws in the
    // panes either side of it. The drag ends by fitting everything it moved.
    if (resizing) return;
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
    // The zoomed pane of each tab, and how many panes the zoom is hiding.
    const zoomed = new Map();
    for (const t of s.tabs) {
      if (!t.zoom || !t.focus) continue;
      const ids = new Set();
      collectPanes(t.root, ids);
      // A pane alone in its tab can be zoomed - by a stray double-click on
      // its header - and hides nothing; shown as zoomed, its bubble said
      // "0 other panes are hidden" and offered to bring them back.
      if (ids.size > 1) zoomed.set(t.focus, ids.size - 1);
    }
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
      // The name is cut short with an ellipsis in a narrow pane, and names
      // taken from an agent's task are long; the bubble has the whole of it.
      if (was.name !== v.name) {
        was.name = v.name;
        p.name.textContent = v.name;
        describe(p.name, v.name);
        p.wrap.setAttribute("aria-label", v.name);
      }

      const project = v.project || "";
      if (was.project !== project) { was.project = project; renderPaneProject(p, v); }

      const branch = v.branch || "";
      if (was.branch !== branch) { was.branch = branch; renderPaneBranch(p, v); }

      // Keyed on the pair, because a pane can change model without changing
      // agent and the badge writes both.
      const agent = (v.agent || "") + "\u0000" + (v.model || "");
      // And on the routing mark, which a pane restarted on a model chosen by
      // hand loses while keeping its agent and model.
      const badge = agent + "|" + (v.routed || "") + "|" + (v.routedFrom || "") + "|" + (v.route || "");
      if (was.agent !== badge) { was.agent = badge; renderPaneAgent(p, v); }

      const remoteViewers = (v.remoteViewers || []).join("\u0000");
      if (was.remote !== remoteViewers) { was.remote = remoteViewers; renderPaneRemote(p, v); }

      const detail = v.detail || "";
      if (was.detail !== detail) {
        was.detail = detail;
        p.detail.textContent = detail;
        // Cut short in a narrow pane - an MCP tool's name nearly always is -
        // and with no bubble, so the rest could be read nowhere.
        if (detail) describe(p.detail, detail);
        else delete p.detail.dataset.tip;
      }

      const git = [v.dirty, v.untracked, v.ahead, v.behind, v.gitTimedOut ? "late" : ""].join(" ");
      if (was.git !== git) { was.git = git; renderPaneGit(p, v); }

      // Keyed on what is actually displayed — the processor figure is drawn
      // rounded — so a pane whose usage is merely jittering does not redraw
      // its header on every sample.
      const usage = (v.procs || 0) + " " + Math.round(v.cpu || 0) + " " + (v.rss || 0);
      if (was.usage !== usage) { was.usage = usage; renderPaneUsage(p, v); }

      // The server rounds these to what is drawn, so the whole of it is the key.
      const spend = JSON.stringify(v.spend || null);
      if (was.spend !== spend) { was.spend = spend; renderPaneSpend(p, v); }

      const cast = (v.broadcast ? "1" : "0") + (s.broadcast ? "1" : "0");
      if (was.cast !== cast) { was.cast = cast; renderPaneCast(p, v, s); }

      const zoom = zoomed.has(id) ? String(zoomed.get(id)) : "";
      if (was.zoom !== zoom) { was.zoom = zoom; renderPaneZoom(p, zoomed.get(id)); }

      const review = (v.autoReview ? "1" : "0") + ":" + (v.autoApproved || 0);
      if (was.review !== review) { was.review = review; renderPaneReview(p, v); }

      const focused = !!tab && tab.focus === id;
      p.wrap.classList.toggle("focused", focused);
      speakIfFocused(p, focused);
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
      // In the set it receives the prompt bar's message whether broadcast is
      // on or not; this used to say it would only once broadcast was turned
      // on, which is not what happens.
      describe(p.cast, s.broadcast
        ? "What you type in the prompt bar is delivered to this pane. " + TIPS.broadcast
        : "This pane is in the broadcast set, which it was put in by hand, so what you send from the prompt bar reaches it now, broadcast on or off. " + TIPS.broadcast);
    } else {
      delete p.cast.dataset.tip;
    }
    p.cast.classList.toggle("active", !!(v.broadcast && s.broadcast));
    // The toggle says whether it is on, to the eye and to a screen reader. It
    // was found as the first button in the row, which is fan out, so it was
    // fan out that lit up for a pane in the set and the toggle never did.
    p.castBtn.classList.toggle("on", !!v.broadcast);
    p.castBtn.setAttribute("aria-pressed", String(!!v.broadcast));
  }

  /** renderPaneZoom says that a pane is zoomed, and what that is hiding. A
   *  zoomed pane looked exactly like the only pane in its tab, so the others
   *  seemed to have been closed, with nothing on screen saying where they had
   *  gone or that the button beside the close button brings them back.
   *  `hidden` is how many there are, and undefined when it is not zoomed. */
  function renderPaneZoom(p, hidden) {
    const on = hidden !== undefined;
    p.zoomBtn.classList.toggle("zoomed", on);
    p.zoomBtn.setAttribute("aria-pressed", String(on));
    describe(p.zoomBtn, on
      ? "Zoomed: " + (hidden === 1 ? "1 other pane is" : hidden + " other panes are") +
        " hidden, still running. Press to bring them back."
      : TIPS.zoom);
  }

  /** renderPaneReview says whether a pane's PreToolUse calls are put to
   *  auto-review before Claude Code would otherwise show its own permission
   *  prompt, and how many it has let through unasked so far -- see
   *  Pane.AutoReview and Pane.AutoApproved. A child a pane starts, by fan-out
   *  or by its own `flockdeck spawn`, comes up already showing whatever its
   *  parent had, since it inherits the same setting. */
  function renderPaneReview(p, v) {
    const on = !!v.autoReview;
    p.reviewBtn.classList.toggle("reviewing", on);
    p.reviewBtn.setAttribute("aria-pressed", String(on));
    describe(p.reviewBtn, (on
      ? "Auto-review is on" + (v.autoApproved ? ": " + v.autoApproved + " command" + (v.autoApproved === 1 ? "" : "s") + " let through unasked so far. " : ". ") +
        "Press to turn it off."
      : "Auto-review is off. Press to turn it on. ") + TIPS.autoReview);
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
    // The header cuts a long branch short, and the bubble said what a branch
    // is rather than which: the whole name could be read nowhere.
    describe(p.branch, v.branch + " — " + TIPS.branch);
  }

  /** renderPaneAgent names what is running in the pane: the agent, and the
   *  model it was asked for. It sits beside the branch and reads like it,
   *  because it answers the same sort of question — which of these six panes
   *  is the one I want. A shell has neither and shows nothing. */
  function renderPaneAgent(p, v) {
    p.agent.textContent = "";
    if (!v.agent) { delete p.agent.dataset.tip; return; }
    // A routed model says so, and which way it was moved: ↘ to a smaller
    // model than the pane would have had, ↗ to a stronger one. The arrow is a
    // span of its own that never shrinks, because a header also carrying
    // spend and a limit cuts the label short, and the end of the label is
    // where the cut falls: the arrow was the first thing lost.
    const arrow = v.route === "down" ? " ↘" : v.route === "up" ? " ↗" : "";
    p.agent.append(el("span", "agent-name", v.model ? v.agent + " · " + v.model : v.agent));
    if (arrow) p.agent.append(el("span", "agent-route", arrow));
    // A pane routed to another agent names it beside the model, since
    // "sonnet" on its own means nothing once it is not this pane's own
    // agent's model any more.
    const from = v.routedFromAgent && v.routedFromAgent !== v.agent
      ? v.routedFromAgent + " · " + (v.routedFrom || "Default")
      : (v.routedFrom || "Default");
    const routed = v.routed
      ? " Routed" + (v.route ? " " + v.route : "") + " from " + from + " by the rule '" + v.routed + "'."
      : "";
    // Cut short like the branch, and named in its bubble for the same reason.
    describe(p.agent, p.agent.textContent + " — " + TIPS.agent + routed);
  }

  /** renderPaneRemote shows a small phone glyph while a window reached
   *  through the relay has this pane open, in its chat view or its terminal —
   *  its own owner's phone, or a colleague's. Without it, text appearing in a
   *  pane nobody at the desk is touching looks like it came from nowhere,
   *  when it was typed on another device. Named devices only; nothing at all
   *  while no phone has the pane open. */
  function renderPaneRemote(p, v) {
    p.remote.textContent = "";
    const names = v.remoteViewers || [];
    if (!names.length) {
      delete p.remote.dataset.tip;
      p.remote.removeAttribute("aria-label");
      p.remote.removeAttribute("role");
      return;
    }
    p.remote.append(glyph("📱"));
    const label = "Open on " + names.map((n) => n || "an unnamed device").join(" and ");
    p.remote.setAttribute("role", "img");
    p.remote.setAttribute("aria-label", label);
    describe(p.remote, label);
  }

  /** renderPaneUsage shows what the pane is costing the machine: its share of a
   *  processor, averaged so the figure settles rather than flickering, and the
   *  memory resident across the agent's whole process tree - the `claude` CLI
   *  does its work in children, so its own process alone would say almost
   *  nothing. A pane with nothing to report, which includes every platform the
   *  server cannot read a process table on, shows nothing at all rather than a
   *  row of zeroes. */
  function renderPaneUsage(p, v) {
    p.usage.textContent = "";
    if (!v.procs) {
      p.usage.classList.remove("hot");
      p.usage.removeAttribute("role");
      p.usage.removeAttribute("aria-label");
      delete p.usage.dataset.tip;
      return;
    }
    const cpu = Math.round(v.cpu || 0);
    const mem = formatBytes(v.rss || 0);
    p.usage.textContent = cpu + "% " + mem;
    // A whole core is the point at which a pane is worth noticing, and this is
    // the only thing on screen saying which agent is the expensive one.
    p.usage.classList.toggle("hot", cpu >= 100);
    p.usage.setAttribute("role", "img");
    const text = cpu + "% of a processor and " + mem + " across " +
      v.procs + (v.procs === 1 ? " process" : " processes") + ". " + TIPS.usage;
    p.usage.setAttribute("aria-label", text);
    describe(p.usage, text);
  }

  /** SPEND_WINDOWS names the usage windows an agent reports: short for the
   *  header, in words for the tooltip. One not listed goes by its own name. */
  const SPEND_WINDOWS = {
    five_hour: ["5h", "five-hour limit"],
    seven_day: ["7d", "weekly limit"],
    seven_day_opus: ["7d Opus", "weekly Opus limit"],
    seven_day_sonnet: ["7d Sonnet", "weekly Sonnet limit"],
  };

  /** renderPaneSpend shows what the pane's agent has spent in its
   *  conversation, and the tightest limit it runs under. Every money figure
   *  is an estimate and is written "~$": Flockdeck's price table or the
   *  agent's own list price, never the bill. Somebody on a subscription pays
   *  the same whatever happens and is stopped by the window instead, so where
   *  a window is reported the header leads with it and shows tokens, and the
   *  dollars - what the tokens would cost on the API - go in the tooltip. An
   *  agent that reports nothing shows nothing. */
  function renderPaneSpend(p, v) {
    const sp = v.spend || null;
    const windows = (sp && sp.windows) || [];
    const quiet = (node) => {
      node.textContent = "";
      node.classList.remove("warn", "full");
      node.removeAttribute("role");
      node.removeAttribute("aria-label");
      delete node.dataset.tip;
    };
    quiet(p.spend);
    quiet(p.limit);
    if (!sp) return;

    const subscriber = windows.length > 0;
    const tokens = sp.tokens ? formatTokens(sp.tokens) + " tok" : "";
    const money = sp.usd ? formatUSD(sp.usd) + (sp.unpriced ? "+" : "") : "";
    const moneyWords = money ? spendMoneyWords(v, sp, money, subscriber) : "";

    if (subscriber) {
      const w = windows[0];
      const [short] = SPEND_WINDOWS[w.name] || [w.name];
      const pct = Math.max(0, Math.min(100, Math.round(w.pct)));
      const meter = el("span", "limit-meter");
      const fill = el("span", "limit-fill");
      fill.style.width = pct + "%";
      meter.append(fill);
      p.limit.append(el("span", "limit-text", short + " " + pct + "%"), meter);
      if (w.level) p.limit.classList.add(w.level);
      const lines = windows.map(spendWindowWords);
      lines.push("Every pane on the same login shares these limits, and so does use elsewhere on it.");
      // With no tokens to show, the dollars have nowhere else to be read.
      if (!tokens && moneyWords) lines.push(moneyWords);
      const text = lines.join(" ");
      p.limit.setAttribute("role", "img");
      p.limit.setAttribute("aria-label", text);
      describe(p.limit, text);
    }

    const shown = subscriber ? tokens : (money || tokens);
    if (!shown) return;
    p.spend.textContent = shown;
    const lines = [];
    if (sp.in || sp.out) {
      let t = "Tokens in this conversation: " + (sp.in || 0).toLocaleString("en-US") + " in";
      if (sp.cacheRead) t += " (" + sp.cacheRead.toLocaleString("en-US") + " cached)";
      t += ", " + (sp.out || 0).toLocaleString("en-US") + " out";
      if (sp.reasoning) t += ", " + sp.reasoning.toLocaleString("en-US") + " of them reasoning";
      lines.push(t + ".");
    } else if (sp.tokens) {
      lines.push(sp.tokens.toLocaleString("en-US") + " tokens in this conversation.");
    }
    if (moneyWords) lines.push(moneyWords);
    else if (sp.tokens) lines.push(sp.unpricedWhy === "gateway"
      ? "This agent talks to an address of your own, which charges its own prices, so it is shown in tokens."
      : "There is no price here for this model, so it is shown in tokens.");
    const text = lines.join(" ");
    p.spend.setAttribute("role", "img");
    p.spend.setAttribute("aria-label", shown === money
      ? "About " + money.slice(1) + " spent in this conversation, an estimate. " + text
      : text);
    describe(p.spend, text);
  }

  /** spendMoneyWords says where a money figure came from and what it is not. */
  function spendMoneyWords(v, sp, money, subscriber) {
    let words;
    if (sp.source === "agent") {
      const who = v.agent === "claude" ? "Claude Code" : (v.agent || "The agent");
      words = who + " puts this session at " + money + ": " + who + "'s own estimate, at list price";
    } else {
      words = money + (sp.checked ? " at prices checked " + formatChecked(sp.checked) : " at published prices");
    }
    if (subscriber) words += ", which is what these tokens would cost on the API, not what your plan charges";
    words += ".";
    if (sp.unpriced) words += sp.unpricedWhy === "gateway"
      ? " Some tokens went through an address of your own, which charges its own prices, so this is a floor."
      : " Some tokens were used by a model with no price here, so this is a floor.";
    return words + " An estimate at published prices, not your bill.";
  }

  /** spendWindowWords is one usage window as the tooltip says it. */
  function spendWindowWords(w) {
    const [, words] = SPEND_WINDOWS[w.name] || [w.name, w.name.replace(/_/g, " ") + " limit"];
    let s = "The " + words + " is " + Math.round(w.pct) + "% used";
    if (w.resetsAt) s += ", and resets " + formatWhen(w.resetsAt);
    const now = Date.now() / 1000;
    // A reading minutes old may no longer be true: use elsewhere on the same
    // login moves it between readings.
    if (w.asOf && now - w.asOf > 600) s += " (as of " + formatWhen(w.asOf, true) + ")";
    return s + ".";
  }

  /** formatWhen writes a time in Unix seconds as a clock time, with the day
   *  when it is not today. */
  function formatWhen(sec, bare) {
    const d = new Date(sec * 1000);
    const clock = String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
    if (d.toDateString() === new Date().toDateString()) return bare ? clock : "at " + clock;
    const day = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"][d.getDay()];
    return (bare ? "" : "on ") + day + " at " + clock;
  }

  /** formatChecked writes a price table's date, "2026-06-24", as "24 Jun 2026". */
  function formatChecked(iso) {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso || "");
    if (!m) return iso;
    const month = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"][Number(m[2]) - 1];
    return Number(m[3]) + " " + month + " " + m[1];
  }

  /** formatUSD writes an estimate with enough places to be worth showing, as
   *  the chat's own status line does: a turn of a few hundred tokens costs a
   *  fraction of a cent, and two places would show it as costing nothing. */
  function formatUSD(v) {
    // The places are chosen by the figure as it will be written, so an
    // amount a hair under a boundary is written like the one it rounds to:
    // $0.9999 is "~$1.00" beside $1.01's "~$1.01", not "~$1.000".
    if (Math.round(v * 100) >= 100) return "~$" + v.toFixed(2);
    if (Math.round(v * 1000) >= 10) return "~$" + v.toFixed(3);
    return "~$" + v.toFixed(4);
  }

  /** formatTokens writes a count the way somebody glancing at it reads it.
   *  Cut rather than rounded at every size, as the thousands already were,
   *  so 9,999 is "9.9k" beside 10,001's "10k" rather than "10.0k". */
  function formatTokens(n) {
    if (n >= 1e6) return (Math.floor(n / 1e5) / 10).toFixed(1) + "M";
    if (n >= 1e4) return Math.floor(n / 1000) + "k";
    if (n >= 1e3) return (Math.floor(n / 100) / 10).toFixed(1) + "k";
    return String(n);
  }

  /** formatBytes writes a size the way a person reads one, to three
   *  significant figures at most so the header does not jitter as the last
   *  digit moves. */
  function formatBytes(n) {
    const units = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    // Rounded before it is written, and the unit settled on what rounding
    // gives: 1023.8 KB is 1.0 MB rather than "1024 KB", and 99.97 MB is
    // 100 MB rather than a fourth figure.
    let r = n >= 100 || i === 0 ? Math.round(n) : Math.round(n * 10) / 10;
    if (r >= 1024 && i < units.length - 1) { r = Math.round((n / 1024) * 10) / 10; i++; }
    return (r >= 100 || i === 0 ? String(Math.round(r)) : r.toFixed(1)) + " " + units[i];
  }

  /** renderPaneOverlay covers the terminal when the pane has no live process. */
  function renderPaneOverlay(p, v) {
    const needed = !!v.err || v.status === "exited";
    if (!needed) {
      if (p.overlay) {
        // Restart on the cover had the keyboard, and it went with the cover
        // when the process came back - onto nothing, so what was typed next
        // reached no pane until something was clicked. It goes back to the
        // terminal the button brought back, and only from the cover.
        const held = p.overlay.contains(document.activeElement);
        p.overlay.remove();
        p.overlay = null;
        p.overlayRestart = null;
        if (held) p.term.focus();
      }
      return;
    }
    const text = v.err || "The process exited.";
    if (p.overlay) {
      // The error can change under a cover already up - a pane that exited
      // and then failed to restart, or failed again for another reason - and
      // the cover went on showing the first. Only the words change, so the
      // Restart button keeps the keyboard if it has it.
      if (p.overlayText.textContent !== text) p.overlayText.textContent = text;
      return;
    }
    const box = el("div", "pane-error");
    // It goes up on its own, over a terminal somebody may be reading or
    // typing in, so it is said as it appears.
    box.setAttribute("role", "alert");
    p.overlayText = el("div", null, text);
    box.append(p.overlayText);
    const row = el("div");
    const restart = el("button", "chip primary", "Restart");
    restart.onclick = () => send({ cmd: "restartPane", id: p.id });
    const close = el("button", "chip", "Close pane");
    close.onclick = () => send({ cmd: "closePane", id: p.id });
    row.append(restart, document.createTextNode(" "), close);
    box.append(row);
    p.body.append(box);
    p.overlay = box;
    p.overlayRestart = restart;
    // The keyboard was left in the terminal under the cover, where nothing
    // typed went anywhere. It moves to Restart once, as the cover goes up,
    // and only for the pane being typed in: not from the rail or the top bar,
    // and not out from under a dialog or the palette.
    const t = currentTab();
    const at = document.activeElement;
    const here = !at || at === document.body || p.wrap.contains(at);
    if (t && t.focus === p.id && here && !dialogOpen() && $("disconnected").hidden) restart.focus();
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
      clearTimeout(p.flingTimer);
      panes.delete(id); // stop the close handler from reconnecting
      // The find bar searches the pane it was opened for, and with that pane
      // gone it stayed open, still naming it, while Enter and F3 did nothing.
      if (searchPane === id && !$("searchbar").hidden) closeSearch();
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

  /** focusNeighbour moves the keyboard to the next pane in the tab, or the
   *  one before, in the order the layout holds them, coming round at either
   *  end. Tab inside a terminal belongs to the program in it, and nothing
   *  else moved the keyboard from one pane to another: in a tab of six
   *  agents, somebody without a mouse stayed in the terminal they were in. */
  function focusNeighbour(step) {
    const t = currentTab();
    if (!t) return;
    const ids = new Set();
    collectPanes(t.root, ids);
    const order = [...ids];
    if (order.length < 2) return;
    const at = Math.max(0, order.indexOf(t.focus));
    send({ cmd: "focusPane", id: order[(at + step + order.length) % order.length] });
  }
  function focusedPaneId() { const t = currentTab(); return t ? t.focus : ""; }

  /** regionOf says which part of the window a node is in, in the order
   *  cycleRegion walks them: the rail, the top bar, a pane's header and a
   *  pane's terminal. -1 is anywhere else: the page itself, or a bar along
   *  the bottom. */
  function regionOf(node) {
    if (!node || !node.closest) return -1;
    if (node.closest("#rail")) return 0;
    if (node.closest("#topbar")) return 1;
    if (node.closest(".pane-header")) return 2;
    if (node.closest(".pane-body")) return 3;
    return -1;
  }

  /** cycleRegion moves the keyboard to the next part of the window, or the
   *  one before, coming round at either end. Tab inside a terminal belongs to
   *  the program in it, so there was no way from a terminal to the rail, the
   *  tabs or the pane's own buttons without the mouse. A dialog keeps the
   *  keyboard, as it keeps Tab. */
  function cycleRegion(step) {
    if (modalRoot()) return;
    const t = currentTab();
    const p = t ? panes.get(t.focus) : null;
    const stops = [
      // The rail, unless a narrow window has folded it into a menu; the
      // button that opens the menu is in the top bar.
      () => { if (railFolded()) return null; placeRailStop(); return railStop; },
      // The top bar, at the tab on screen: the strip is most of what it is.
      // With no tab to land on, New agent tab is the bar's own first stop.
      () => { const n = state ? tabNodes.get(state.activeTab) : null; return n ? n.btn : $("new-tab"); },
      // The focused pane's buttons, at whichever of them is their stop.
      () => (p ? [...p.actions.children].find((b) => b.tabIndex === 0) || null : null),
      () => (p ? p.term : null),
    ];
    const at = regionOf(document.activeElement);
    // From outside all four, forwards starts at the first and back at the last.
    const from = at < 0 ? (step > 0 ? -1 : stops.length) : at;
    for (let i = 1; i <= stops.length; i++) {
      const to = (((from + step * i) % stops.length) + stops.length) % stops.length;
      const target = stops[to]();
      if (!target) continue;
      if (p && target === p.term) focusTerminal();
      else target.focus();
      return;
    }
  }

  // -------------------------------------------------------------- prompt bar

  /** What has been sent from the prompt bar this session, oldest first, and
   *  where Up and Down have got to in it. The bar is how one instruction
   *  reaches every agent, and sending it again, or a variation on it, meant
   *  typing it out again in full; Up recalls it, as it does at a prompt. */
  const promptHistory = [];
  let promptAt = 0;
  let promptDraft = "";
  /** What was left in the bar when it was closed without sending. The bar
   *  opened empty every time, so an instruction half-written for every agent
   *  went with an Escape pressed a moment too soon - and, never sent, it was
   *  not in the history either. */
  let promptUnsent = "";
  /** The pane the prompt bar was opened on, which is where what is written in
   *  it goes. Focus can move while it is open - the desk clicking another
   *  pane, a fan-out revealing its first agent - and a prompt sent to
   *  whichever pane had focus by then reached an agent it was not written
   *  for. */
  let promptPane = "";

  function promptKey(ev) {
    const input = $("prompt-input");
    // A prompt can run to several lines, and Up and Down move between them.
    // They recall a prompt only from the first line and the last, as a shell
    // does with a command of several lines.
    const from = input.selectionStart ?? input.value.length;
    const to = input.selectionEnd ?? from;
    if (ev.key === "ArrowUp" && promptAt > 0 && !input.value.slice(0, from).includes("\n")) {
      if (promptAt === promptHistory.length) promptDraft = input.value;
      input.value = promptHistory[--promptAt];
    } else if (ev.key === "ArrowDown" && promptAt < promptHistory.length && !input.value.slice(to).includes("\n")) {
      promptAt++;
      input.value = promptAt === promptHistory.length ? promptDraft : promptHistory[promptAt];
    } else {
      return;
    }
    ev.preventDefault();
    input.setSelectionRange(input.value.length, input.value.length);
    fitPrompt();
  }

  /** fitPrompt makes the prompt field as tall as the lines in it, up to the
   *  height the stylesheet allows, after which it scrolls. A field of fixed
   *  height showed one line of a prompt of five, and the four above it went
   *  to every agent unread. */
  function fitPrompt() {
    const input = $("prompt-input");
    input.style.height = "";
    if (!input.scrollHeight) return;
    // scrollHeight leaves out the border, which a border-box height includes.
    const border = (input.offsetHeight - input.clientHeight) || 0;
    input.style.height = input.scrollHeight + border + "px";
  }

  /** labelPrompt says how many panes the prompt bar's message will reach.
   *  Whether or not broadcast is on: the message goes to the focused pane and
   *  every pane in the set, and a pane picked by hand with ⇉ stays in the set
   *  with broadcast off. Counting only while it was on said "Prompt" over a
   *  message about to reach three agents. Kept current while the bar is open,
   *  since the set changes under it - a pane added with ⇉, a member closed. */
  function labelPrompt(s) {
    const n = s ? countBroadcast(s) : 1;
    const text = n > 1 ? `Prompt → ${n} panes` : "Prompt";
    if ($("prompt-label").textContent !== text) $("prompt-label").textContent = text;
  }

  function openPrompt() {
    const bar = $("promptbar");
    bar.hidden = false;
    const tab = activeTabOf(state);
    promptPane = (tab && tab.focus) || "";
    promptAt = promptHistory.length;
    promptDraft = "";
    labelPrompt(state);
    const input = $("prompt-input");
    input.value = promptUnsent;
    input.focus();
    fitPrompt();
  }
  function closePrompt() {
    promptUnsent = $("prompt-input").value;
    $("promptbar").hidden = true;
    focusTerminal();
  }
  function submitPrompt() {
    const text = $("prompt-input").value;
    if (text.trim()) {
      send({ cmd: "sendPrompt", id: promptPane, text });
      if (promptHistory[promptHistory.length - 1] !== text) promptHistory.push(text);
      if (promptHistory.length > 50) promptHistory.shift();
    }
    $("prompt-input").value = "";
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
    if (!p) return;
    // A pane whose process has gone is covered, and its terminal takes no
    // typing: sent there - by F6, a click on the tab on screen, a closed
    // dialog - the keyboard was in a terminal nobody could see and nothing
    // typed went anywhere. The cover's Restart is what can be used.
    if (p.overlayRestart) p.overlayRestart.focus();
    else p.term.focus();
  }

  /** dialogOpen reports whether anything is on screen that the keyboard could
   * reasonably belong to, so that a pane appearing behind a dialog does not
   * pull the focus out from under someone mid-sentence. It answers for the
   * dialog being open rather than for it holding the focus, because the state
   * push and the dialog's own focus() race: the button that opened the dialog
   * also moved the focus, and the answer must not depend on which won. */
  function dialogOpen() {
    if (typingInDialog()) return true;
    if (railMenuOpen()) return true;
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
    // The rail folded into a menu covers the window's edge, with the panes
    // dimmed behind it, and is left the way a dialog is.
    if (railMenuOpen() && railFolded()) return $("rail");
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
    $("overlay-panel").classList.remove("wide", "settings-panel");

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
  function keepFocus(draw, fallback) {
    const body = $("overlay-body");
    const was = document.activeElement;
    const key = was && body.contains(was) ? identify(was) : "";
    const at = was && was.selectionStart;
    // Emptying the body to draw it again takes its scroll position with it, so
    // a list scrolled to its twentieth worktree jumped back to the first when
    // one was removed, or when a refresh came back.
    const top = body.scrollTop;
    // The settings scroll their section rather than the whole body, and the
    // section has to stay where it was read to as well.
    const pane = $("settings-pane");
    const paneTop = pane ? pane.scrollTop : 0;
    draw();
    body.scrollTop = top;
    if (pane && pane.isConnected) pane.scrollTop = paneTop;
    if (!key) return;
    const all = [...body.querySelectorAll("button, input, textarea, select, [tabindex]")];
    for (const node of all) {
      if (identify(node) !== key) continue;
      // A control the redraw disabled - the font stepper at its limit, a
      // button whose work is done - cannot hold the keyboard: the browser
      // drops it onto nothing. It goes to the nearest control that can.
      if (node.disabled) {
        const near = nearestEnabled(all, node, body);
        if (near) near.focus();
        return;
      }
      node.focus();
      if (at != null && node.setSelectionRange) {
        try { node.setSelectionRange(at, at); } catch { /* not that kind of field */ }
      }
      return;
    }
    // The control went with what it acted on - the folder gone into, say - so
    // the keyboard goes to where the answer put things, where the caller has
    // named such a place, rather than falling out of the dialog altogether.
    // The place is a selector, or a function finding it in the body.
    const next = typeof fallback === "function" ? fallback(body) : fallback && body.querySelector(fallback);
    if (next) next.focus();
  }

  /** nearestEnabled is the control to hand the keyboard to in place of one
   *  that has been disabled: one beside it in the same group, else the next
   *  one along, else the one before. */
  function nearestEnabled(all, node, root) {
    const usable = (n) => {
      if (n === node || n.disabled || n.getAttribute("tabindex") === "-1") return false;
      for (let p = n; p && p !== root; p = p.parentElement) if (p.hidden) return false;
      return true;
    };
    const at = all.indexOf(node);
    return all.find((n) => n.parentElement === node.parentElement && usable(n)) ||
      all.slice(at + 1).find(usable) || all.slice(0, at).reverse().find(usable) || null;
  }

  /** identify is what makes a control the same control across a redraw. An id
   *  is that on its own; otherwise it is what the control is and what it says,
   *  which is how a person finds it again too. A control whose wording changes
   *  while it is working needs the id, and so does a row whose words carry a
   *  figure that moves on its own - a project's waiting count, a file's
   *  changed lines, how long ago a conversation was: the redraws come
   *  because those moved, and found by its words the row lost the keyboard
   *  each time. */
  function identify(node) {
    if (node.id) return "#" + node.id;
    // A control repeated on every row - Clear, Unpair, a project's close - is
    // told apart by the row it is on, where the row carries a key. By wording
    // alone one row's is the next row's, and a redraw after clearing one key,
    // which does not ask, left the keyboard on the next agent's Clear.
    let row = node;
    while (row && !(row.dataset && row.dataset.key)) row = row.parentElement;
    return [row ? row.dataset.key : "", node.tagName, node.className, (node.textContent || "").trim()].join("|");
  }

  /** refreshDialog asks again for whatever the open dialog is showing. */
  function refreshDialog() {
    if (dialog === "worktrees") send({ cmd: "worktrees" });
    else if (dialog === "changes") send({ cmd: "changes", path: (changes && changes.cwd) || "" });
    else if (dialog === "agents") send({ cmd: "agents" });
    else if (dialog === "agentPicker") send({ cmd: "refreshAgents" });
    else if (dialog === "history") send({ cmd: "conversations" });
    else if (dialog === "keys") send({ cmd: "keys" });
    else if (dialog === "remote") send({ cmd: "remoteDevices" });
    else if (dialog === "settings" && settingsSection === "keys") send({ cmd: "keys" });
    else if (dialog === "settings" && settingsSection === "remote") send({ cmd: "remoteDevices" });
    else if (dialog === "settings" && settingsSection === "github") send({ cmd: "ghStatus", path: ghDir });
    else if (dialog === "github") { send({ cmd: "ghStatus", path: ghDir }); ghRequestTab(); }
    else if (dialog === "projects") {
      send({ cmd: "recents" });
      send({ cmd: "browse", path: browseState ? browseState.path : "" });
    }
  }

  function closeOverlay() {
    picker = null;
    $("overlay").hidden = true;
    $("overlay-panel").classList.remove("wide", "settings-panel");
    dialog = null;
    remotePairPollStop();
    focusTerminal();
  }

  // ------------------------------------------------------------- worktrees

  let worktrees = null;
  /** What has been typed into the new-worktree form. The dialog is drawn whole
   *  from each reply, so without this a branch name half-typed when a worktree
   *  was removed, or Refresh was pressed, went with the redraw. It is kept for
   *  as long as the dialog is open and no longer. */
  let wtDraft = { branch: "", base: "" };
  /** The branch just asked for with the form, until the list comes back with
   *  it. Starting an agent in a new worktree is what nearly everybody does
   *  next, and the keyboard was left on Create with the new row's Agent
   *  button to be found among the others; it lands there instead. */
  let wtCreated = "";

  /** The panes the worktree list last counted. How many agents work in each
   *  worktree is counted when the list is read, and Remove refuses while any
   *  do; close those panes and the dialog went on counting them, refusing
   *  for agents that were no longer there until Refresh was pressed. The list
   *  is asked for again whenever panes open or close while it is up. */
  let worktreePanes = "";
  function followWorktrees(s) {
    const key = Object.keys(s.panes || {}).sort().join(",");
    if (key === worktreePanes) return;
    worktreePanes = key;
    if (dialog === "worktrees") send({ cmd: "worktrees" });
  }

  /** openWorktrees opens the dialog and asks for what goes in it. It opens at
   *  once rather than when the answer arrives, because the same answer comes
   *  back after every add, remove and prune, and those take seconds: opened
   *  by its answer, a dialog closed while one of them ran came back over the
   *  terminals on its own and took the keyboard from the pane being typed
   *  into. The other dialogs already opened this way; their answers are now
   *  drawn only into the dialog they belong to. */
  /** rootLabel is the line at the foot of a dialog naming the folder it is
   *  about. It is cut short with an ellipsis, paths differ at the end the
   *  ellipsis takes, and it had no bubble: which checkout a dialog was for
   *  could not be read. */
  function rootLabel(path) {
    const n = el("span", "wt-root", path);
    return path ? describe(n, path) : n;
  }

  function openWorktrees() {
    dialog = "worktrees";
    worktrees = null;
    wtDraft = { branch: "", base: "" };
    openOverlay("Worktrees", "worktrees");
    $("overlay-body").append(el("div", "dir-empty", "Reading the worktrees…"));
    send({ cmd: "worktrees" });
  }

  function renderWorktrees(msg) {
    if (msg) worktrees = msg;
    if (dialog !== "worktrees") return; // closed, or replaced by another
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
      if (wt.prunable) titleLine.append(describe(el("span", "wt-flag gone", "folder gone"), TIPS.folderGone));
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
      // A worktree whose folder is gone had no status to read, so its counts
      // are all nothing, and it was called clean.
      if (wt.prunable) meta.append(describe(el("span", "wt-gone", "only git's record of it is left"), TIPS.folderGone));
      else if (!wt.dirty && !wt.untracked) meta.append(describe(el("span", "wt-clean", "clean"), TIPS.clean));
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
      // Cut from the end with an ellipsis, and paths differ at the end.
      main.append(describe(el("div", "wt-path", wt.path), wt.path));
      row.append(main);

      const actions = el("div", "wt-actions");
      // Named by the worktree they act on. A redraw finds the control the
      // keyboard was on by what it is, and by wording alone "Remove" in one
      // row is "Remove" in the next: removing a clean worktree - which does
      // not ask - left the keyboard on the next one's Remove, and a second
      // Enter removed that as well.
      const key = "wt-" + encodeURIComponent(wt.path) + "-";
      if (wt.prunable) {
        // There is no folder for an agent, a shell or a review to start in,
        // and each of them failed there with nothing better to say than that
        // a path does not exist. What is left to do is the panel's own Prune,
        // offered on the row where the stale record is seen. git's prune is
        // not selective, which the bubble says.
        const prune = el("button", "chip primary", "Prune");
        prune.id = key + "prune";
        prune.title = "Clear git's record of this worktree. Like Prune below, it clears every worktree whose folder is gone.";
        prune.onclick = () => send({ cmd: "worktreePrune" });
        actions.append(prune);
      } else {
        const agent = el("button", "chip primary", "Agent");
        agent.title = "Open an agent tab in this worktree";
        agent.onclick = () => { send({ cmd: "newTab", kind: "agent", path: wt.path, text: wt.label }); closeOverlay(); };
        const shell = el("button", "chip", "Shell");
        shell.onclick = () => { send({ cmd: "newTab", kind: "shell", path: wt.path, text: wt.label }); closeOverlay(); };
        const split = el("button", "chip", "Split");
        split.title = "Add an agent for this worktree beside the current pane";
        split.onclick = () => {
          send({ cmd: "splitPane", id: focusedPaneId(), dir: "h", kind: "agent", path: wt.path });
          closeOverlay();
        };
        const review = el("button", "chip", "Review");
        review.title = "See what changed here, commit and push";
        review.onclick = () => openChanges(wt.path);
        actions.append(agent, shell, split, review);
        agent.id = key + "agent"; shell.id = key + "shell"; split.id = key + "split"; review.id = key + "review";
      }

      if (!wt.main && !wt.prunable) {
        const rm = el("button", "chip danger", "Remove");
        rm.id = key + "remove";
        const unsafe = wt.dirty || wt.untracked;
        rm.title = unsafe ? "This worktree has uncommitted work" : "Remove this worktree";
        rm.onclick = () => {
          // Not while agents are working in it: they would be left in a
          // directory that no longer exists, and the server refuses it,
          // forced or not. Saying so here, before anything is sent, is where
          // the person can do something about it. Force is only ever the
          // answer to uncommitted work in a worktree nothing is running in.
          if (wt.panes) {
            notice((wt.panes === 1 ? "An agent is" : wt.panes + " agents are") + " working in " + wt.label +
              ". Close " + (wt.panes === 1 ? "that pane" : "those panes") + " first, then remove it.", true);
            return;
          }
          if (unsafe && !window.confirm(
            wt.label + " has uncommitted changes.\n\nRemove it and discard them?")) return;
          send({ cmd: "worktreeRemove", path: wt.path, force: !!unsafe });
        };
        actions.append(rm);
      }
      // One stop per worktree, walked with the arrows, as a pane's header
      // is. Four or five stops a row put the new-worktree form fifty presses
      // of Tab past a list of ten.
      makeToolbar(actions, "What to do with " + wt.label);
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
    branch.value = wtDraft.branch;
    branch.oninput = () => { wtDraft.branch = branch.value; };
    const base = el("input");
    base.placeholder = "Base";
    base.value = wtDraft.base || m.defaultBase || "";
    base.oninput = () => { wtDraft.base = base.value; };
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
      // Enter in the base field, filled in last, with no branch named did
      // nothing and said nothing - and the branch is what the worktree is for.
      if (!branch.value.trim()) {
        notice("Name the branch for the new worktree first", true);
        branch.focus();
        return;
      }
      wtCreated = branch.value.trim();
      send({ cmd: "worktreeAdd", text: branch.value.trim(), base: base.value.trim() });
      branch.value = "";
      wtDraft.branch = "";
    };
    go.onclick = submit;
    // From either field: the base is the one filled in last, and Enter there
    // did nothing at all.
    branch.onkeydown = base.onkeydown = (ev) => { if (ev.key === "Enter") submit(); };
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
      // One stop, walked with the arrows, like the rows above it: up to
      // fourteen branches, each a Tab stop, stood between the list and the
      // rest of the dialog.
      makeToolbar(chips, "Branches without a worktree");
      bs.append(chips);
      // Only the first fourteen are offered as buttons, and the rest simply
      // were not there: a branch further down the list looked as though it
      // did not exist. Typing its name into the form above checks it out.
      if (free.length > 14) {
        const more = free.length - 14;
        bs.append(el("div", "wt-hint", (more === 1 ? "1 more branch" : more + " more branches") +
          " not shown - type a branch name into the form above to check it out."));
      }
      body.append(bs);
    }

    // --- maintenance -------------------------------------------------------
    const tools = el("div", "wt-tools");
    const refresh = el("button", "chip", "Refresh");
    refresh.onclick = () => send({ cmd: "worktrees" });
    const prune = el("button", "chip", "Prune");
    prune.title = "Drop records for worktrees whose folders are gone";
    prune.onclick = () => send({ cmd: "worktreePrune" });
    tools.append(refresh, prune, rootLabel(m.root || ""));
    body.append(tools);

    if (wtCreated) {
      const made = (m.items || []).findIndex((wt) => wt.label === wtCreated);
      if (made >= 0) {
        wtCreated = "";
        // After the redraw's own keyboard handling, which put it back on
        // Create: the row for the new worktree is where it is wanted.
        setTimeout(() => {
          const row = list.querySelectorAll("div.wt-row")[made];
          const agent = row && row.querySelector("button");
          if (agent && dialog === "worktrees") agent.focus();
        }, 0);
      }
    }
  }

  // -------------------------------------------------------------- projects

  /** Whether every recent project is listed, rather than the first eight. */
  let recentsAll = false;
  /** Whether the Archived section is expanded. It starts collapsed on every
   *  open of the dialog: an archived project is meant to be out of the way,
   *  not the first thing seen on opening Projects. */
  let archivedOpen = false;

  /** The projects dialog's Open section is drawn from the state, and was
   *  drawn only when a recents or folder listing arrived: closing a project
   *  from it - which answers with a state push and nothing else - left the
   *  project listed as open, with a close button that did nothing, and an
   *  agent starting to wait in another project did not show. It follows the
   *  pushes that change what it lists, and no others. */
  let projectsKey = "";
  function followProjects(s) {
    const key = (s.projects || [])
      .map((p) => [p.root, p.name, p.active, p.tabs, p.waiting, p.working, p.archived].join(":"))
      .join("|");
    if (key === projectsKey) return;
    projectsKey = key;
    // A closed project's row is gone with the keyboard on it: to the project
    // now in its place - its own button, never its ×, which an idle project
    // answers without asking.
    const inPlace = (body) => closedAt < 0 ? null :
      body.querySelectorAll("button.proj-go")[Math.min(closedAt, ((state && state.projects) || []).length - 1)] || null;
    if (dialog === "projects") keepFocus(renderProjects, inPlace);
  }

  /** Which recent project's × was last pressed, by its place in the list,
   *  so the keyboard can go to whichever row takes that place. */
  let forgotAt = -1;
  /** The same for an open project whose × was last pressed. */
  let closedAt = -1;

  /** openProjects opens the projects dialog. `browsing` is the rail's button
   *  for opening a project, which puts the keyboard in the folder field: the
   *  one question that button asks is which folder. */
  function openProjects(browsing) {
    dialog = "projects";
    browseDraft = null;
    recentsAll = false;
    archivedOpen = false;
    openOverlay("Projects", "projects");
    send({ cmd: "recents" });
    send({ cmd: "browse", path: browseState ? browseState.path : (state ? state.root : "") });
    renderProjects();
    if (browsing === true && $("browse-path")) $("browse-path").focus();
  }

  function renderProjects() {
    if (dialog !== "projects") return;
    const body = $("overlay-body");
    body.textContent = "";

    // Open projects: switch between them, or close one.
    const open = (state && state.projects) || [];
    const openSection = section("Open");
    open.forEach((p) => {
      const row = el("div", "proj-row" + (p.active ? " active" : "") + (p.archived ? " archived" : ""));
      row.dataset.key = "open:" + p.root;
      // The row's own action — switch to this project — is a real button
      // wrapping everything that describes it, rather than a click handler on
      // the row. A handler on a div cannot be tabbed to and does not answer
      // Enter, and the icon buttons beside it could not have been nested
      // inside something that claimed to be a button itself.
      const go = el("button", "proj-go");
      go.id = "open-" + encodeURIComponent(p.root);
      const main = el("span", "proj-main");
      main.append(el("span", "proj-name", p.name));
      // Cut from the end with an ellipsis, and paths differ at the end.
      main.append(describe(el("span", "proj-path", p.root), p.root));
      go.append(main);

      const badge = el("span", "proj-badge");
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
      go.append(badge);
      go.onclick = () => { send({ cmd: "selectProject", root: p.root }); closeOverlay(); };
      row.append(go);

      // Splitting into the project already on screen is just an ordinary
      // split, so the button is offered on the others.
      if (!p.active) {
        const split = el("button", "icon-btn", "⊞");
        describe(split, TIPS.splitHere);
        split.onclick = () => {
          send({ cmd: "splitPane", dir: "h", root: p.root });
          closeOverlay();
        };
        row.append(split);
      }

      // The default agent is a per-project setting already (agents.json's
      // own "set as default for this project"), and it is only worked out
      // for whichever project is active, so the shortcut to it is offered
      // only there; switching to another project first offers it on that
      // one's own row.
      if (p.active) {
        const agentBtn = el("button", "chip", "Default agent\u2026");
        describe(agentBtn, "Choose the agent this project opens new panes with when nobody chooses one.");
        agentBtn.onclick = () => chooseAgent("Default agent for " + p.name, () => {}, { setDefault: true });
        row.append(agentBtn);
      }

      row.append(projectActions(p.root, p.name, p.archived, null));

      if (open.length > 1) {
        const close = el("button", "icon-btn", "\u00d7");
        describe(close, "Close this project. The agents running in it stop.");
        close.onclick = () => {
          // Every agent in every tab of it stops, on one click of a small
          // cross - the one other thing that stops work on that scale, Quit,
          // asks first. Only agents with something under way are worth the
          // question; a project where every agent is idle closes at once.
          const busy = (p.working || 0) + (p.waiting || 0);
          if (busy && !window.confirm("Close " + p.name + "? " + (busy === 1 ? "1 agent is" : busy + " agents are") +
            " still working or waiting on you there, and every agent in it stops.")) return;
          closedAt = open.indexOf(p);
          send({ cmd: "closeProject", root: p.root });
        };
        row.append(close);
      }
      openSection.append(row);
    });
    body.append(openSection);

    // Recent projects that are not already open and not archived.
    const notOpen = recents.filter((r) => !r.open && !r.archived);
    if (notOpen.length) body.append(renderProjectSection("Recent", notOpen, "recent"));

    // Archived projects: out of the way behind their own toggle, rather than
    // the first thing seen on opening the dialog.
    const archived = recents.filter((r) => !r.open && r.archived);
    if (archived.length) {
      if (!archivedOpen) {
        const wrap = el("div", "proj-section");
        const btn = el("button", "chip", "Show " + archived.length + " archived project" + (archived.length === 1 ? "" : "s"));
        btn.onclick = () => { archivedOpen = true; renderProjects(); };
        wrap.append(btn);
        body.append(wrap);
      } else {
        body.append(renderProjectSection("Archived", archived, "archived"));
      }
    }

    body.append(renderBrowser());
  }

  /** renderProjectSection draws a list of not-open projects -- Recent or
   *  Archived -- each row offering to open it (when its folder still
   *  exists), rename it, move it, archive or unarchive it, and forget it.
   *  `prefix` tells the two sections' rows apart, and moving or forgetting
   *  a row works from its place in `list`, the section's own full list,
   *  rather than in whatever is on screen: it must come out the same
   *  whether the section is showing eight rows or eighty. */
  function renderProjectSection(title, list, prefix) {
    const rec = section(title);
    const shown = prefix === "recent" && !recentsAll ? list.slice(0, 8) : list;
    shown.forEach((r) => {
      const at = list.indexOf(r);
      const row = el("div", "proj-row" + (r.exists ? "" : " missing"));
      row.dataset.key = prefix + ":" + r.root;
      const main = el("span", "proj-main");
      main.append(el("span", "proj-name", r.name));
      main.append(describe(el("span", "proj-path", r.exists ? r.root : r.root + "  (missing)"), r.root));
      // A project whose folder has gone cannot be opened, so it stays text
      // rather than becoming a button that does nothing when it is pressed.
      if (r.exists) {
        const go = el("button", "proj-go");
        go.id = prefix + "-" + at;
        go.append(main);
        go.onclick = () => { send({ cmd: "openProject", path: r.root }); closeOverlay(); };
        row.append(go);
      } else {
        row.append(main);
      }
      row.append(projectActions(r.root, r.name, r.archived, { list, at }));
      const forget = el("button", "icon-btn proj-forget", "\u00d7");
      describe(forget, "Remove this project from the list. Nothing on disk is touched.");
      // removeProject rather than the plain forgetRecent: it is the same
      // drop from the list, plus closing the project first on the rare
      // chance it is open under a spelling this row's own !open filter
      // missed -- a second window's project, say -- so nothing is forgotten
      // out from under itself while still on screen.
      forget.onclick = () => { forgotAt = at; send({ cmd: "removeProject", root: r.root }); };
      row.append(forget);
      rec.append(row);
    });
    // Eight at first, and the rest - up to forty are remembered - simply
    // were not there, so an older project could be reached only by browsing
    // to its folder again. The Archived section, opened by its own toggle
    // rather than shown by default, lists all of itself at once: it is
    // already the "more" the Recent section's own button reaches for.
    if (prefix === "recent" && shown.length < list.length) {
      const more = el("button", "chip", "Show " + (list.length - shown.length) + " more");
      more.onclick = () => {
        recentsAll = true;
        renderProjects();
        const next = $(prefix + "-" + shown.length);
        if (next) next.focus();
      };
      rec.append(more);
    }
    return rec;
  }

  /** projectActions is the row of small controls every project offers,
   *  open or not: rename, and archive or unarchive. `moveCtx`, given as
   *  `{list, at}`, adds move-up and move-down for a project kept in a list
   *  ordered by hand (Recent and Archived; an open project's place among
   *  the others open is not reordered by hand). */
  function projectActions(root, name, archived, moveCtx) {
    const actions = el("span", "proj-actions");

    const rename = el("button", "icon-btn", "\u270e");
    describe(rename, "Rename this project");
    rename.onclick = () => renameProject(root, name);
    actions.append(rename);

    if (moveCtx) {
      const up = el("button", "icon-btn", "\u25b2");
      describe(up, "Move up");
      up.disabled = moveCtx.at <= 0;
      up.onclick = () => moveProject(moveCtx.list, root, -1);
      actions.append(up);
      const down = el("button", "icon-btn", "\u25bc");
      describe(down, "Move down");
      down.disabled = moveCtx.at >= moveCtx.list.length - 1;
      down.onclick = () => moveProject(moveCtx.list, root, 1);
      actions.append(down);
    }

    const arch = el("button", "chip", archived ? "Unarchive" : "Archive");
    describe(arch, archived
      ? "Bring this project back among your other projects."
      : "Keep this project out of the way, in the picker's own Archived section, until it is brought back. Nothing on disk is touched, and an open project stays open.");
    arch.onclick = () => send({ cmd: "archiveProject", root, archived: !archived });
    actions.append(arch);

    return actions;
  }

  /** renameProject asks for a project's display name, starting from the one
   *  it has now, the same way remoteRename asks for a device's. An answer
   *  left empty goes too, the same way an emptied tab name does: it clears
   *  the name chosen by hand and goes back to the one derived from its
   *  directory. */
  function renameProject(root, current) {
    const name = window.prompt("Rename this project. Leave it empty to use its folder name.", current);
    if (name === null || name === undefined) return;
    const next = String(name).trim();
    if (next === current) return;
    send({ cmd: "renameProject", root, text: next });
  }

  /** moveProject swaps a project with its neighbour in a list kept in an
   *  order chosen by hand, and sends the whole list's new order --
   *  reorderProjects only places the projects it is given, so whatever is
   *  not in `list` (an open project, or the other of Recent/Archived) keeps
   *  the place it already had. */
  function moveProject(list, root, delta) {
    const at = list.findIndex((r) => r.root === root);
    const to = at + delta;
    if (at < 0 || to < 0 || to >= list.length) return;
    const roots = list.map((r) => r.root);
    [roots[at], roots[to]] = [roots[to], roots[at]];
    send({ cmd: "reorderProjects", roots });
  }

  /** rowAction makes a whole row behave as the button it already is to a
   *  mouse. A div carrying an onclick cannot be reached with Tab and does not
   *  answer Enter, so a list built out of them is a list only a pointer can
   *  use — and these lists are how the file an agent changed gets read and how
   *  the agent that is blocked gets found.
   *
   *  The arrow keys, Home and End walk the list the row is in, as they do any
   *  list. Where the choice has nothing to undo - which file's diff is shown -
   *  `follow` makes arriving at a row choose it, so a review is read by
   *  pressing Down rather than by Tab and Enter for every file. */
  /** rowStep is where a key moves the keyboard in a list of rows, or
   *  undefined. Page Down on a row scrolled the box and left the keyboard on
   *  a row now out of sight, and the next arrow jumped the list back to it;
   *  the pages move the keyboard as the palette's do. */
  function rowStep(key, at, count) {
    return { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: count - 1,
      PageDown: Math.min(at + LIST_PAGE, count - 1), PageUp: Math.max(at - LIST_PAGE, 0) }[key];
  }

  function rowAction(row, fn, follow, label) {
    row.tabIndex = 0;
    row.setAttribute("role", "button");
    row.onclick = fn;
    // What typing the start of it finds the row by: a file's name rather
    // than the folders in front of it, a pane's tab, a conversation's first
    // words.
    row.dataset.label = (label || row.textContent || "").toLowerCase();
    row.onkeydown = (ev) => {
      if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); fn(ev); return; }
      // Not the rows a filter has hidden, which the arrows would walk into
      // and leave the keyboard on out of sight.
      const rows = [...row.parentElement.children].filter((n) => n.getAttribute("role") === "button" && !n.hidden);
      const at = rows.indexOf(row);
      let to = rowStep(ev.key, at, rows.length);
      if (to === undefined && ev.key.length === 1 && !ev.ctrlKey && !ev.altKey && !ev.metaKey) to = typeAhead(ev.key, rows, at, row.parentElement);
      if (to === undefined || to < 0 || !rows[to]) return;
      ev.preventDefault();
      rows[to].focus();
      if (follow && to !== at) rows[to].onclick(ev);
    };
    return row;
  }

  /* Typing the start of a row's name goes to it, as it does in nearly every
   * list: in a review of forty files, or an overview of a dozen agents, the
   * arrows were the only way to a row. Letters typed within a moment of each
   * other make one prefix; a pause starts a new one. */
  let typed = "";
  let typedAt = 0;
  /** The list the letters were typed in: a prefix belongs to one list, and
   *  one begun in another list a moment ago is not carried into this one. */
  let typedIn = null;
  function typeAhead(key, rows, at, list) {
    const now = Date.now();
    typed = now - typedAt < 700 && typedIn === list ? typed + key.toLowerCase() : key.toLowerCase();
    typedAt = now;
    typedIn = list;
    // A row that still matches as the prefix grows keeps the keyboard;
    // otherwise the search starts after it and comes round from the top.
    for (let i = typed.length > 1 ? 0 : 1; i <= rows.length; i++) {
      const j = (at + i) % rows.length;
      if (rows[j].dataset.label.startsWith(typed)) return j;
    }
    return -1;
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
    path.id = "browse-path";
    path.setAttribute("aria-label", "Folder");
    // A path being typed survives a redraw of the dialog — dropping a project
    // from the recent list redraws it — and gives way to wherever the browser
    // has actually been sent, which is what arriving somewhere new means.
    path.value = browseDraft !== null ? browseDraft : (b ? b.path : "");
    path.oninput = () => { browseDraft = path.value; };
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
      // One stop between the path field and the folder list, not one a place.
      makeToolbar(places, "Places");
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
        // Looking inside the folder is this row's own action, and the only way
        // to reach anywhere that is not already on the list, so it is a button
        // rather than a click handler on a div. Opening the folder as a
        // project is the chip beside it, which is why neither can contain the
        // other.
        const into = el("button", "dir-into");
        // One glyph for a repository and another for a plain folder: the whole
        // distinction lives in the shape, so it has to be spelled out.
        const icon = el("span", "dir-icon", e.isRepo ? "\u25c6" : "\u25b8");
        icon.setAttribute("role", "img");
        icon.setAttribute("aria-label", e.isRepo ? "Git repository" : "Folder");
        into.append(describe(icon, e.isRepo ? TIPS.repoFolder : TIPS.plainFolder));
        into.append(el("span", "dir-name", e.name));
        if (e.isRepo) into.append(el("span", "dir-repo", "git"));
        into.onclick = () => send({ cmd: "browse", path: e.path });
        into.dataset.label = e.name.toLowerCase();
        row.append(into);
        const openBtn = el("button", "chip", "Open");
        openBtn.onclick = () => { send({ cmd: "openProject", path: e.path }); closeOverlay(); };
        row.append(openBtn);
        list.append(row);
      });
      // The folders answer the arrows and the start of a name, as the other
      // lists do. Two buttons a folder, and nothing else, made a directory of
      // fifty repositories a hundred presses of Tab to the one wanted - and
      // opening a folder is how anybody new gets started. The arrows move
      // between the folders themselves; Open stays one Tab from its folder.
      list.onkeydown = (ev) => {
        const rows = [...list.querySelectorAll("button.dir-into")];
        const at = rows.indexOf(document.activeElement);
        if (at < 0) return;
        let to = rowStep(ev.key, at, rows.length);
        if (to === undefined && ev.key.length === 1 && ev.key !== " " && !ev.ctrlKey && !ev.altKey && !ev.metaKey) {
          to = typeAhead(ev.key, rows, at, list);
        }
        if (to === undefined || to < 0 || !rows[to]) return;
        ev.preventDefault();
        rows[to].focus();
        rows[to].scrollIntoView({ block: "nearest" });
      };
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
    if (v.gitTimedOut) {
      // The counts still in the push are the ones read before git stopped
      // answering. Drawn as usual they claimed to be current, and a checkout
      // that hung went on looking clean, or as far ahead as it last was.
      p.git.append(el("span", "late", "git timed out"));
      p.git.setAttribute("role", "img");
      p.git.setAttribute("aria-label", "Git status timed out.");
      describe(p.git, TIPS.gitLate + " Click to review the checkout.");
      return;
    }
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
    describe(p.git, text + " Click to review them.");
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
    applyFontSize(px);
    prefs.fontSize = fontSize;
    sentPref("fontSize");
    send({ cmd: "fontSize", size: fontSize });
    notice("Font size " + fontSize + "px", false);
    settingsChanged();
  }

  /** The preferences kept as "off", the command that changes each, and what
   *  is said when it is turned on and off. The palette's entries and the
   *  settings' switches both go through setOff, so they cannot say or do two
   *  different things. */
  const PREF_CMD = { notificationsOff: "notifications", cursorSteady: "cursorBlink", updatesOff: "updates" };
  const PREF_SAYS = {
    notificationsOff: ["Desktop notifications are on", "Desktop notifications are off"],
    cursorSteady: ["The terminal cursor blinks", "The terminal cursor is steady"],
    updatesOff: ["Flockdeck will check for new releases", "Flockdeck will not check for new releases"],
  };

  /** What this window has changed and not yet heard back, by preference: each
   *  value sent, oldest first, and when. The server answers every change with
   *  all of the preferences, and a change made twice in quick succession - the
   *  font made bigger twice, a switch pressed off and on - had the answer to
   *  the first arrive after the second was made here, and put the first back
   *  until the answer to the second came. Values are compared rather than
   *  answers counted, because a change the server finds is no change at all,
   *  or cannot save, is answered with nothing; and each is given up after a
   *  few seconds, so one that is never answered does not hold the field. */
  const pendingPrefs = new Map();
  const PREF_ECHO_MS = 3000;
  function sentPref(field) {
    const sent = pendingPrefs.get(field) || [];
    sent.push({ value: prefs[field], at: Date.now() });
    pendingPrefs.set(field, sent);
  }
  /** keepPending is a push of the preferences with this window's own latest
   *  changes kept where the push is the answer to an earlier one of them: it
   *  carries a value sent from here before the last. A push carrying the last
   *  value sent has caught up, and one carrying none of them is somebody
   *  else's choice, from another window, and is taken as it is. */
  function keepPending(incoming) {
    const now = Date.now();
    for (const [field, sent] of pendingPrefs) {
      const live = sent.filter((s) => now - s.at < PREF_ECHO_MS);
      const at = live.map((s) => s.value).lastIndexOf(incoming[field]);
      if (at >= 0 && at < live.length - 1) { incoming[field] = prefs[field]; pendingPrefs.set(field, live.slice(at + 1)); }
      else pendingPrefs.delete(field);
    }
    return incoming;
  }

  /** setOff turns one of those preferences on or off. It takes effect here at
   *  once rather than when the server's copy comes back, so a switch pressed
   *  twice in quick succession reads what it was last set to. */
  function setOff(field, off) {
    prefs[field] = off;
    sentPref(field);
    send({ cmd: PREF_CMD[field], kind: off ? "off" : "on" });
    notice(PREF_SAYS[field][off ? 1 : 0], false);
    if (field === "cursorSteady") applyCursorBlink();
    // Turned on in the window, the browser has to agree as well.
    if (field === "notificationsOff" && !off) askForNotifications();
    settingsChanged();
  }

  /** applyPrefs puts into effect the preferences that change how the
   *  terminals behave. They arrive before the first pane is drawn, and again
   *  whenever another window changes one. */
  function applyPrefs() {
    applyFontSize(prefs.fontSize);
    applyScrollback(prefs.scrollback);
    applyCursorBlink();
    applyCursorStyle();
    applyFontFamily();
    applyScreenReader();
    applyRail();
  }

  /** setScreenReader turns screen reader support on or off. It is kept as
   *  "on", unlike the preferences setOff looks after: it costs every terminal
   *  a copy of its lines, so it stays off until somebody asks for it. It takes
   *  effect here at once, as they do. */
  function setScreenReader(on) {
    prefs.screenReader = on;
    sentPref("screenReader");
    send({ cmd: "screenReader", kind: on ? "on" : "off" });
    notice(on ? "Screen reader support is on" : "Screen reader support is off", false);
    applyScreenReader();
    settingsChanged();
  }
  /** applyScreenReader makes every terminal follow the preference. A pane
   *  made later reads it as it is made. */
  function applyScreenReader() {
    const on = !!prefs.screenReader;
    for (const p of panes.values()) {
      if (p.term.options.screenReaderMode !== on) p.term.options.screenReaderMode = on;
    }
    // xterm has made each terminal's live region by now, all of them loud.
    for (const p of panes.values()) speakIfFocused(p, p.wrap.classList.contains("focused"));
  }

  /** speakIfFocused lets only the focused pane's terminal read out what it
   *  is sent. With screen reader support on, xterm keeps a live region in
   *  every terminal and makes each one assertive, so in a tab of several
   *  agents they all talked at once, over each other and over what was being
   *  typed. The others' are off until the keyboard goes to them. */
  function speakIfFocused(p, focused) {
    if (!prefs.screenReader) return;
    if (!p.liveRegion || !p.liveRegion.isConnected) p.liveRegion = p.host.querySelector(".live-region");
    const r = p.liveRegion;
    if (!r) return;
    const want = focused ? "assertive" : "off";
    if (r.getAttribute("aria-live") !== want) r.setAttribute("aria-live", want);
  }

  /** The typeface the terminals are drawn in where nobody has chosen one. */
  const DEFAULT_FONT = '"Cascadia Mono", "JetBrains Mono", Consolas, "SF Mono", Menlo, monospace';

  /** terminalFont is the font list the terminals use: the one chosen, with
   *  monospace behind it so a font this machine does not have falls back to
   *  a fixed-width one rather than to the page's own. */
  function terminalFont() {
    return prefs.fontFamily ? prefs.fontFamily + ", monospace" : DEFAULT_FONT;
  }

  function applyFontFamily() {
    const font = terminalFont();
    for (const p of panes.values()) {
      if (p.term.options.fontFamily === font) continue;
      p.term.options.fontFamily = font;
      scheduleFit(p);
    }
  }

  /** fontMissing is whether this machine has none of the fonts named: text
   *  measured in them, with each generic family behind, comes out exactly as
   *  wide as in the generic family alone. Three generics, so a font that is
   *  itself the machine's monospace is not taken for missing. Where nothing
   *  can be measured the fonts are taken to be there. */
  function fontMissing(family) {
    const canvas = document.createElement("canvas");
    const ctx = canvas.getContext && canvas.getContext("2d");
    if (!ctx) return false;
    // A generic family is always there, and quoted would name a font called
    // "monospace", which nobody has.
    if (/^(ui-)?(monospace|serif|sans-serif)$|^system-ui$/i.test(family)) return false;
    const named = /[,"']/.test(family) ? family : '"' + family + '"';
    const sample = "mmmmmmmmmmlli0O@#WW";
    return ["monospace", "serif", "sans-serif"].every((generic) => {
      ctx.font = "32px " + generic;
      const plain = ctx.measureText(sample).width;
      ctx.font = "32px " + named + ", " + generic;
      return ctx.measureText(sample).width === plain;
    });
  }

  /** askFontFamily asks which typeface the terminals should use. It was
   *  fixed in the source, and a terminal's font is among the first things
   *  anybody who works in one sets to their own. */
  function askFontFamily() {
    const answer = window.prompt("Which font should the terminals use? Leave it empty for the default.", prefs.fontFamily || "");
    if (answer === null || answer === undefined) return;
    const why = chooseFontFamily(answer);
    if (why) notice(why, true);
    settingsChanged();
  }

  /** chooseFontFamily makes a typeface the terminals' own, or says why it
   *  cannot. The palette's question and the settings' field both come here. */
  function chooseFontFamily(answer) {
    const family = String(answer).trim();
    // A name this machine has no font for was taken, announced as the
    // terminals' font, and drawn in the fallback: nothing changed on screen,
    // and nothing said why - a misspelling looked like the setting not working.
    if (family && fontMissing(family)) {
      return "No font called " + family + " was found on this machine, so the terminals keep " +
        (prefs.fontFamily || "the default font");
    }
    prefs.fontFamily = family;
    sentPref("fontFamily");
    applyFontFamily();
    send({ cmd: "fontFamily", text: family });
    notice(family ? "The terminals now use " + family : "The terminals use the default font again", false);
    return "";
  }

  /** How many lines a terminal keeps once they have scrolled off the top,
   *  where nobody has chosen, and what has been chosen. */
  const SCROLLBACK = 10000;
  let scrollback = SCROLLBACK;

  function applyScrollback(lines) {
    scrollback = lines || SCROLLBACK;
    for (const p of panes.values()) {
      if (p.term.options.scrollback !== scrollback) p.term.options.scrollback = scrollback;
    }
  }

  /** askScrollback asks how much each terminal should keep. The number was
   *  fixed in the source: too few lines for a long agent session to be read
   *  back through, and more than a dozen panes need where memory is short. */
  function askScrollback() {
    const answer = window.prompt("How many lines should each terminal keep once they scroll off the top? (1,000 to 200,000)", String(scrollback));
    if (answer === null || answer === undefined) return;
    const n = Number(String(answer).replace(/[,\s_]/g, ""));
    if (!Number.isInteger(n) || n < 1000 || n > 200000) {
      notice("Scrollback has to be a whole number of lines from 1,000 to 200,000.", true);
      return;
    }
    chooseScrollback(n);
  }

  /** chooseScrollback is what the palette's question and the settings' list
   *  both do with an answer. */
  function chooseScrollback(n) {
    applyScrollback(n);
    prefs.scrollback = n;
    sentPref("scrollback");
    send({ cmd: "scrollback", size: n });
    notice("Each terminal now keeps " + n.toLocaleString("en") + " lines", false);
    settingsChanged();
  }

  /** applyFontSize draws every terminal at a size without announcing it,
   *  which is how a size chosen on an earlier run, or in another window,
   *  arrives. Zero, for a person who has never chosen one, is the default. */
  function applyFontSize(px) {
    fontSize = Math.max(8, Math.min(28, px || 13));
    for (const p of panes.values()) {
      if (p.term.options.fontSize === fontSize) continue;
      p.term.options.fontSize = fontSize;
      scheduleFit(p);
    }
  }

  // ------------------------------------------------------------------- rail

  /** How wide the rail is drawn while it shows names beside its icons, in
   *  pixels, where nobody has dragged it, and the range a drag or the
   *  keyboard may leave it at. */
  const RAIL_WIDTH = 220;
  const RAIL_MIN = 180;
  const RAIL_MAX = 480;
  /** The width in effect now, kept so a drag has something to add its delta
   *  to and the keyboard something to step from. */
  let railWidth = RAIL_WIDTH;

  function clampRailWidth(px) {
    return Math.max(RAIL_MIN, Math.min(RAIL_MAX, px || RAIL_WIDTH));
  }

  /** applyRail puts the rail's expanded state and width into effect. They
   *  arrive before the first pane is drawn, and again whenever another
   *  window changes them. */
  function applyRail() {
    railWidth = clampRailWidth(prefs.railWidth);
    const expanded = !!prefs.railExpanded;
    document.body.classList.toggle("rail-expanded", expanded);
    // Collapsed, the width the style sheet already gives an icon rail is left
    // alone; only "expanded" asks for a width of its own.
    $("rail").style.width = expanded ? railWidth + "px" : "";
    $("rail-collapse").setAttribute("aria-pressed", String(expanded));
    $("rail-resize").setAttribute("aria-valuenow", String(railWidth));
  }

  /** setRailExpanded widens the rail into a panel of icons and names, or
   *  folds it back to icons alone. It takes effect here at once, as the
   *  other switches in the settings do. */
  function setRailExpanded(on) {
    prefs.railExpanded = on;
    sentPref("railExpanded");
    send({ cmd: "railExpanded", kind: on ? "on" : "off" });
    notice(on ? "The rail is expanded" : "The rail is collapsed", false);
    applyRail();
    settingsChanged();
  }

  /** liveRailWidth redraws the rail at a width mid-drag, without telling the
   *  Go side yet: that waits for the drag, or the key press, to be over,
   *  the way the split dividers already do it. */
  function liveRailWidth(px) {
    railWidth = clampRailWidth(px);
    $("rail").style.width = railWidth + "px";
    $("rail-resize").setAttribute("aria-valuenow", String(railWidth));
  }

  /** saveRailWidth draws the rail at a width and tells the Go side, so a
   *  drag let go or a key pressed both come back the same way next time. */
  function saveRailWidth(px) {
    liveRailWidth(px);
    prefs.railWidth = railWidth;
    sentPref("railWidth");
    send({ cmd: "railWidth", size: railWidth });
    settingsChanged();
  }

  /** One press of an arrow key moves the rail this many pixels. */
  const RAIL_RESIZE_STEP = 16;

  /** initRailResize wires the handle that drags the rail's width, the same
   *  way makeDivider wires a split's: pointer capture so a drag that leaves
   *  the window is still delivered, and the left and right arrow keys once
   *  the handle has the keyboard. */
  function initRailResize() {
    const handle = $("rail-resize");
    describe(handle, "Sets how wide the rail is drawn. Drag it, or use the left and right arrow keys.");
    handle.setAttribute("aria-valuemin", String(RAIL_MIN));
    handle.setAttribute("aria-valuemax", String(RAIL_MAX));

    handle.addEventListener("keydown", (ev) => {
      const less = ev.key === "ArrowLeft";
      const more = ev.key === "ArrowRight";
      if (!less && !more) return;
      ev.preventDefault();
      ev.stopPropagation();
      saveRailWidth(railWidth + (more ? RAIL_RESIZE_STEP : -RAIL_RESIZE_STEP));
    });

    handle.addEventListener("pointerdown", (ev) => {
      if (ev.button !== 0) return;
      ev.preventDefault();
      const startX = ev.clientX;
      const startWidth = railWidth;
      handle.classList.add("dragging");
      document.body.classList.add("rail-resizing");
      try { handle.setPointerCapture(ev.pointerId); } catch { /* older engines */ }
      const onMove = (m) => {
        if (m.pointerId !== ev.pointerId) return;
        liveRailWidth(startWidth + (m.clientX - startX));
      };
      const onUp = (u) => {
        if (u.pointerId !== ev.pointerId) return;
        handle.removeEventListener("pointermove", onMove);
        handle.removeEventListener("pointerup", onUp);
        handle.removeEventListener("pointercancel", onUp);
        handle.classList.remove("dragging");
        document.body.classList.remove("rail-resizing");
        saveRailWidth(railWidth);
      };
      handle.addEventListener("pointermove", onMove);
      handle.addEventListener("pointerup", onUp);
      handle.addEventListener("pointercancel", onUp);
    });
  }

  // ----------------------------------------------------------------- search

  /** The pane the find bar was opened on. It is not always the focused one by
   *  the time a search runs: clicking into another pane while the bar is up
   *  moves the focus, and a search that followed it would jump to a pane the
   *  user was not looking at and leave the first one marked for good, since
   *  only the focused pane's marks were ever cleared. */
  let searchPane = "";
  /** The last thing searched for. The bar opened empty every time, so
   *  looking for the same word again - after closing the bar, or in the next
   *  pane - meant typing it again; it comes back selected, so Enter repeats
   *  it and typing replaces it, as in a browser or an editor. */
  let lastSearch = "";

  function openSearch() {
    const input = $("search-input");
    const was = searchPane;
    const open = !$("searchbar").hidden;
    // Asked for again while the bar is up on the same pane, it only takes the
    // keyboard back: filling the box again replaced what had been typed with
    // the last search, and left the typed words' matches marked under it.
    if (open && was === focusedPaneId()) {
      input.focus();
      if (input.select) input.select();
      return;
    }
    // Moving to another pane, what was typed is the search it carries.
    if (open) lastSearch = input.value;
    searchPane = focusedPaneId();
    // Asked for again after the keyboard moved to another pane, the search
    // moves with it, and it left the first pane's matches marked for good:
    // only the pane being searched is ever cleared.
    if (was && was !== searchPane) {
      const old = panes.get(was);
      if (old && old.search) { try { old.search.clearDecorations(); } catch {} }
    }
    $("searchbar").hidden = false;
    // With six panes on screen, "Find" alone does not say where it is looking.
    const v = state && state.panes ? state.panes[searchPane] : null;
    $("search-label").textContent = v && v.name ? "Find in " + v.name : "Find";
    input.value = lastSearch;
    noMatch(false);
    showMatchCount(null);
    input.focus();
    if (input.select) input.select();
  }
  function closeSearch() {
    lastSearch = $("search-input").value;
    $("searchbar").hidden = true;
    const p = panes.get(searchPane);
    if (p && p.search) { try { p.search.clearDecorations(); } catch {} }
    searchPane = "";
    focusTerminal();
  }
  /** runSearch finds the next or previous match. `typing` is a search made
   *  as the words are typed rather than asked for with Enter, which keeps the
   *  match already found while it still matches instead of moving past it. */
  function runSearch(back, typing) {
    const p = panes.get(searchPane);
    const q = $("search-input").value;
    if (!p || !p.search || !q) {
      if (p && p.search) { try { p.search.clearDecorations(); } catch {} }
      noMatch(false);
      showMatchCount(null);
      return;
    }
    const opts = { incremental: !!typing, decorations: { activeMatchColorOverrideColor: "#4c9aff", matchOverviewRuler: "#4c9aff" } };
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

  /** showMatchCount says which match is showing and how many there are, as
   *  the search addon reports them. Stepping through the matches gave no idea
   *  how far there was to go, or whether Enter had wrapped round to the
   *  first. The addon stops counting past its highlight limit. */
  function showMatchCount(r) {
    const n = r ? r.resultCount : 0;
    $("search-count").textContent = !r || !$("search-input").value ? ""
      : n > 0 ? (r.resultIndex + 1) + " of " + n
      : n === 0 ? "No matches"
      : "Many matches";
  }

  // --------------------------------------------------------- notifications

  /** Remembering the last status per pane is what makes it possible to notify
   *  on the transition into "waiting" rather than repeatedly while it stays
   *  there. */
  const lastStatus = new Map();
  /** How many agents were waiting in each open project, by root. The panes a
   *  push carries are only the active project's, so an agent that stopped to
   *  wait in another open project raised no notification at all - when the
   *  window is least likely to be looked at. Its project's count does arrive,
   *  and a rise in it is the event. */
  const lastProjectWaiting = new Map();

  /** jobOf reports the unit of delegated work a waiting pane belongs to, so
   *  several prompts that are really one job (a fan-out's rows, or an
   *  agent's own helpers) can be told about together instead of as
   *  unrelated interruptions. A fan-out's children share a tab of their
   *  own (see fanout.go's Spawn), so a tab of two or more panes with no
   *  parent is one; a helper started by `flockdeck spawn` carries its
   *  starting pane as v.parent (Pane.Parent), walked up to the pane at the
   *  root of the chain, so a manager that itself spawned a helper still
   *  groups under the same name. A fan-out's own tab is told apart from an
   *  ordinary multi-agent tab a person built by hand by tab.delegated (see
   *  workspace.Tab.Delegated), not by pane count alone -- the same signal
   *  tabSettleInfo relies on, and for the same reason: a person is free to
   *  split several agents into one tab themselves, and that must not read
   *  as one job just because it looks like one from the pane count. Returns
   *  null for a pane that is nobody's fan-out row and started no chain of
   *  its own -- an ordinary pane, grouped with nothing. */
  function jobOf(id, s) {
    const v = s.panes && s.panes[id];
    if (!v) return null;
    if (v.parent) {
      let root = v.parent;
      const seen = new Set([id]);
      while (s.panes[root] && s.panes[root].parent && !seen.has(root)) {
        seen.add(root);
        root = s.panes[root].parent;
      }
      const name = (s.panes[root] && s.panes[root].name) || "";
      return { key: "spawn:" + root, name: name ? name + "’s helpers" : "a helper tree" };
    }
    const tabId = tabIdOfPane(id);
    const tab = tabId && (s.tabs || []).find((t) => t.id === tabId);
    if (!tab || !tab.delegated) return null;
    const ids = new Set();
    collectPanes(tab.root, ids);
    if (ids.size < 2) return null;
    return { key: "tab:" + tabId, name: tab.title || "" };
  }

  /** kindLabel is what a waiting pane wants, as a short noun phrase for a
   *  notification or a review list -- "a file write", "a command to run",
   *  "a question" -- built from the same tool and input a permission
   *  prompt itself would show (see waiting.go's permissionViewFor), so the
   *  boss can judge what several prompts amount to before opening any of
   *  them. */
  function kindLabel(v) {
    if (!v) return "";
    if (v.ask) return "a question";
    if (v.permission) {
      switch (v.permission.tool) {
        case "Bash": return "a command to run";
        case "Edit": case "MultiEdit": case "Write": return "a file write";
        default: return "to use " + v.permission.tool;
      }
    }
    return "";
  }

  /** kindBreakdown counts a group of waiting panes by kindLabel, worded the
   *  way "3 of 6 agents want to write files" reads in the plan this
   *  implements: "2 want a file write, 1 is asking a question". */
  function kindBreakdown(items, s) {
    const counts = new Map();
    for (const f of items) {
      const label = kindLabel(s.panes[f.id]) || "your input";
      counts.set(label, (counts.get(label) || 0) + 1);
    }
    return [...counts.entries()].map(([label, n]) => {
      if (label === "a question") return n + (n === 1 ? " is" : " are") + " asking a question";
      return n + " want " + label;
    }).join(", ");
  }

  /** groupedWaitingBody is the notification body for several panes that just
   *  started waiting at once: named by their shared job and broken down by
   *  what each wants when they are all the one job, so a fan-out of six
   *  reaching a permission question together reads as one sentence about
   *  that fan-out rather than six names with nothing to say what they have
   *  in common. Falls back to the plain list of names otherwise. */
  function groupedWaitingBody(fresh, s) {
    const groups = new Map();
    for (const f of fresh) {
      const job = jobOf(f.id, s);
      const key = job ? job.key : "solo:" + f.id;
      if (!groups.has(key)) groups.set(key, { name: job ? job.name : "", items: [] });
      groups.get(key).items.push(f);
    }
    if (groups.size === 1) {
      const only = [...groups.values()][0];
      if (only.name && only.items.length > 1) return only.name + ": " + kindBreakdown(only.items, s);
    }
    return fresh.map((f) => f.name).join(", ");
  }

  function notifyAttention(s) {
    // Nothing waiting anywhere: whatever the last notification asked has been
    // answered, from this window or another.
    if (!s.waiting && !(s.projects || []).some((p) => p.waiting)) closeNotification();
    const elsewhere = [];
    for (const p of s.projects || []) {
      const before = lastProjectWaiting.get(p.root);
      lastProjectWaiting.set(p.root, p.waiting || 0);
      if (!p.active && before !== undefined && (p.waiting || 0) > before) elsewhere.push(p);
    }
    for (const root of [...lastProjectWaiting.keys()]) {
      if (!(s.projects || []).some((p) => p.root === root)) lastProjectWaiting.delete(root);
    }
    const fresh = [];
    for (const [id, v] of Object.entries(s.panes || {})) {
      const before = lastStatus.get(id);
      lastStatus.set(id, v.status);
      // Never on the first sighting of a pane: arriving at a workspace where
      // three agents are already waiting is not news that they just stopped.
      if (v.status !== "waiting" || before === "waiting" || before === undefined) continue;
      fresh.push({ id, name: v.name, branch: v.branch });
    }
    for (const id of [...lastStatus.keys()]) {
      if (!s.panes || !s.panes[id]) lastStatus.delete(id);
    }
    // Only when the window is not in front: otherwise the marker on the tab is
    // enough and a notification would be noise.
    if ((!fresh.length && !elsewhere.length) || (document.hasFocus() && !document.hidden)) return;

    // One notification for however many stopped at once. They all carry the
    // same tag, so several sent together replace each other on the desktop and
    // only the last survives — and a plan fanned out into six agents is six of
    // them reaching the same permission question within a second of each
    // other, of which you would have been told about exactly one, chosen by
    // whatever order the panes happened to arrive in.
    if (!elsewhere.length) {
      const one = fresh.length === 1;
      const label = one && kindLabel(s.panes[fresh[0].id]);
      const oneBody = (fresh[0].branch ? fresh[0].branch + " — " : "") +
        (label ? (label === "a question" ? "is asking a question" : "wants " + label) : "waiting for input");
      showNotification(
        one ? fresh[0].name + " needs you" : fresh.length + " agents need you",
        one ? oneBody : groupedWaitingBody(fresh, s),
        fresh[0].id);
      return;
    }
    if (!fresh.length && elsewhere.length === 1) {
      showNotification("An agent in " + elsewhere[0].name + " needs you",
        "waiting for input, in another open project", "", elsewhere[0].root);
      return;
    }
    const names = fresh.map((f) => f.name).concat(elsewhere.map((p) => "in " + p.name));
    showNotification("Agents need you", names.join(", "),
      fresh.length ? fresh[0].id : "", fresh.length ? "" : elsewhere[0].root);
  }

  /** showNotification raises the desktop notification. Clicking it goes to
   *  the pane that asked, or - for an agent in another project, whose pane
   *  this window has not been told about - to that project. */
  function showNotification(title, body, paneID, root) {
    if (prefs.notificationsOff) return;
    if (!("Notification" in window) || Notification.permission !== "granted") return;
    try {
      // One tag, so a newer notification replaces the one before rather than
      // piling up; and renotify, because a replacement is otherwise put in
      // place silently: an agent stopping to wait while an earlier
      // notification was still up made no sound and showed no banner.
      const n = new Notification(title, { body, tag: "flockdeck", renotify: true });
      lastNotification = n;
      n.onclick = () => {
        window.focus();
        // Raising the window in front of whichever tab happened to be on
        // screen does not answer the question. The pane that asked it does,
        // and it may well be on a tab that is not the one showing.
        if (paneID) send({ cmd: "revealPane", node: tabIdOfPane(paneID), id: paneID });
        else if (root) send({ cmd: "selectProject", root });
        n.close();
        if (lastNotification === n) lastNotification = null;
      };
    } catch { /* notifications are best effort */ }
  }

  /** The notification last raised. It was closed only when it was clicked:
   *  answered from the window instead, it stayed in the Action Center, and
   *  clicking it later went to a pane that was no longer waiting. It goes
   *  when the window comes to the front, and when nothing waits any more. */
  let lastNotification = null;
  function closeNotification() {
    if (!lastNotification) return;
    try { lastNotification.close(); } catch { /* already gone */ }
    lastNotification = null;
  }
  window.addEventListener("focus", closeNotification);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) closeNotification(); });

  /** reportUsed tells the desktop that this window just saw real keyboard or
   *  mouse input -- a keystroke, a click, a scroll, the pointer moving --
   *  which a paired phone is not told of an agent waiting over if it happened
   *  in the last couple of minutes. Coming to the front or gaining focus does
   *  not count by itself: a window can sit there with nobody at the desk at
   *  all. This is only ever the fallback, for a machine the desktop cannot
   *  read its own idle time on; see push.go's deskInUse. Sent at most once
   *  every half a minute, and pointer moves are thinned out well below that
   *  before they are even offered to it. Nothing of what was typed goes with
   *  it. A window reached through the relay says it too, and the desktop pays
   *  it no mind. */
  let lastUsedReport = 0;
  function reportUsed() {
    const now = Date.now();
    if (now - lastUsedReport < 30000) return;
    lastUsedReport = now;
    send({ cmd: "presence", kind: "used" });
  }
  document.addEventListener("keydown", reportUsed, true);
  document.addEventListener("pointerdown", reportUsed, true);
  document.addEventListener("wheel", reportUsed, true);
  let lastMoveCheck = 0;
  document.addEventListener("pointermove", () => {
    const now = Date.now();
    if (now - lastMoveCheck < 1000) return;
    lastMoveCheck = now;
    reportUsed();
  }, true);

  function askForNotifications() {
    if (prefs.notificationsOff) return; // asked and answered, for every run
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
    splitRight: () => send({ cmd: "splitPane", id: focusedPaneId(), dir: "h", kind: "agent" }),
    splitDown: () => send({ cmd: "splitPane", id: focusedPaneId(), dir: "v", kind: "agent" }),
    splitRightShell: () => send({ cmd: "splitPane", id: focusedPaneId(), dir: "h", kind: "shell" }),
    splitRightChoose: () => chooseAgent("Split right", (agent, model) =>
      send({ cmd: "splitPane", id: focusedPaneId(), dir: "h", kind: "agent", agent, model })),
    movePaneLeft: () => send({ cmd: "movePaneDir", dir: "left" }),
    movePaneRight: () => send({ cmd: "movePaneDir", dir: "right" }),
    movePaneUp: () => send({ cmd: "movePaneDir", dir: "up" }),
    movePaneDown: () => send({ cmd: "movePaneDir", dir: "down" }),
    movePaneToNewTab: () => send({ cmd: "movePaneToNewTab", id: focusedPaneId() }),
    tilePanes: () => send({ cmd: "tilePanes" }),
    zoomPane: () => send({ cmd: "toggleZoom", id: focusedPaneId() }),
    focusNextPane: () => focusNeighbour(1),
    focusPrevPane: () => focusNeighbour(-1),
    restartPane: () => send({ cmd: "restartPane", id: focusedPaneId() }),
    closePane: () => send({ cmd: "closePane", id: focusedPaneId() }),

    newAgentTab: () => send({ cmd: "newTab", kind: "agent" }),
    newAgentTabChoose: () => chooseAgent("New agent tab", (agent, model) =>
      send({ cmd: "newTab", kind: "agent", agent, model })),
    newShellTab: () => send({ cmd: "newTab", kind: "shell" }),
    nextTab: () => send({ cmd: "nextTab" }),
    prevTab: () => send({ cmd: "prevTab" }),
    // The only action that takes an argument: which tab Alt+n named.
    selectTab: (n) => {
      const tab = state && state.tabs[Number(n) - 1];
      if (tab) send({ cmd: "selectTab", id: tab.id });
    },
    mergeAllTabs: () => send({ cmd: "mergeAllTabs", id: state ? state.activeTab : "", dir: "h" }),
    renameTab: () => { if (state) renameTab(state.activeTab); },
    moveTabLeft: () => moveTabBy(-1),
    moveTabRight: () => moveTabBy(1),

    toggleBroadcast: () => send({ cmd: "toggleBroadcast" }),
    toggleBroadcastMember: () => { const id = focusedPaneId(); if (id) send({ cmd: "toggleBroadcastMember", id }); },
    promptAll: () => openPrompt(),
    fanout: () => openFanout(),
    agents: () => openAgents(),
    apiKeys: () => openKeys(),

    worktrees: () => openWorktrees(),
    changes: () => openChanges(),

    palette: () => openPalette(),
    nextRegion: () => cycleRegion(1),
    prevRegion: () => cycleRegion(-1),
    findInTerminal: () => openSearch(),
    history: () => openHistory(),
    projects: () => openProjects(),
    help: () => openHelp(),

    settings: () => openSettings(),
    toggleRail: () => setRailExpanded(!prefs.railExpanded),
    fontUp: () => setFontSize(fontSize + 1),
    fontDown: () => setFontSize(fontSize - 1),
    fontReset: () => setFontSize(13),
    scrollback: () => askScrollback(),
    fontFamily: () => askFontFamily(),
    remote: () => openRemote(),
    detach: () => send({ cmd: "detach" }),
    quit: () => send({ cmd: "quit" }),
    update: openUpdate,
  };

  /** applyHello takes the action table and the preferences, which arrive
   *  together and before the first state, because the palette and the
   *  first-run hints are drawn from them. */
  function applyHello(msg) {
    applyKeyTable(msg.keys || []);
    remoteWindow = !!msg.remote;
    // The hello is what the server holds, after a drop that may have taken
    // changes sent from here with it, so nothing sent before it is waited on.
    pendingPrefs.clear();
    prefs = msg.prefs || prefs;
    applyPrefs();
    applyTheme();
    renderHints();
    // The hello comes again with every reconnect, carrying the preferences
    // as they are now - changed, perhaps, from another window while this one
    // was away - and settings left open went on showing them as they were.
    settingsChanged();
    // A terminal in a box does not advertise what is around it, and the
    // alternative to opening the help once is finding it by accident.
    if (!prefs.helpSeen) openHelp("getting-started");
  }

  /** applyKeyTable takes the key table -- every action, with its binding as
   *  keybindings.json currently leaves or remaps it -- and rebuilds bindings,
   *  the signature-to-id map every keydown is dispatched through. It is what
   *  the hello and a later keyTable broadcast, after a remap, both do; only
   *  the redraw and the rest of hello's own work differ between the two, so
   *  those stay with their own callers. */
  function applyKeyTable(keys) {
    keyTable = keys;
    bindings = new Map();
    keyTable.forEach((k) => {
      const s = signatureOf(k.keys);
      if (s) bindings.set(s, k.id);
    });
    describeChrome();
  }

  /** describeChrome labels the buttons that never change with the action they
   *  run and the key that runs it. Written into the page they would be one
   *  more copy of the bindings to go stale, so they are filled in from the
   *  table instead, once it has arrived. */
  function describeChrome() {
    const label = (id, node, extra) => {
      if (node && keyTable.some((k) => k.id === id)) describe(node, actionTip(id, extra));
    };
    label("newAgentTab", $("new-tab"), "or drop a pane here to give it a tab of its own");
    label("newAgentTabChoose", $("new-tab-pick"));
    label("agents", $("summary"));
    label("toggleBroadcast", $("btn-broadcast"), "on while it is lit");
    label("changes", $("btn-changes"));
    label("history", $("btn-history"));
    label("worktrees", $("btn-worktrees"));
    label("help", $("btn-help"));
    label("settings", $("btn-settings"));
    label("palette", $("btn-palette"), "every action there is, searchable");
    label("projects", $("rail-open"), "open a folder as a project");
    label("toggleRail", $("rail-collapse"));
    // The palette's key, drawn as keys on the button that opens it.
    const k = keyTable.find((x) => x.id === "palette");
    const keys = $("palette-keys");
    keys.textContent = "";
    if (k && k.keys) k.keys.split("+").forEach((part) => keys.append(el("kbd", null, part)));
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

  /** editsText reports whether a keydown belongs to the text field it was
   *  typed into rather than to the window. Ctrl+Shift with an arrow selects
   *  by the word, and Ctrl+Shift+Z is redo, in every text field there is. The
   *  table gives both to the panes, which is right in a terminal and wrong in
   *  a commit message: selecting a word there moved a pane behind the dialog
   *  instead. xterm types through a textarea of its own, which keeps them. */
  function editsText(e) {
    const t = e.target;
    if (!t || (t.tagName !== "TEXTAREA" && t.tagName !== "INPUT")) return false;
    if (t.classList.contains("xterm-helper-textarea") || /^(checkbox|radio|button)$/.test(t.type || "")) return false;
    const k = (e.key || "").toLowerCase();
    return e.ctrlKey && e.shiftKey && (k.startsWith("arrow") || k === "z");
  }

  /** actionFor reports which action a keydown is, if it is one. */
  function actionFor(e) {
    let key = (e.key || "").toLowerCase();
    let shift = e.shiftKey;
    // Ctrl+= and Ctrl++ are the same gesture on most layouts. On a US or UK
    // keyboard + is Shift and =, so it arrives with Shift held, and matched
    // nothing until the Shift was let go of here as well.
    if (key === "+") { key = "="; shift = false; }
    // A layout that does not type Latin letters - Russian, Greek, Hebrew -
    // reports the key marked D as "в", so every Ctrl+Shift binding was dead
    // on it. There the binding can only mean the physical key. The digit row
    // too: on AZERTY the 0 key types à unless Shift is held, so Ctrl+0
    // arrived as Ctrl+à and the font size could not be put back.
    if (/^[^\x00-\x7f]$/.test(key)) {
      const m = /^(?:Key([A-Z])|Digit([0-9]))$/.exec(e.code || "");
      if (m) key = (m[1] || m[2]).toLowerCase();
    }
    return bindings.get(signature(e.ctrlKey, shift, e.altKey, key)) || "";
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
    // The settings are commands like any other, and "settings" is the word
    // somebody looking for one types - it matched none of them.
    const SETTING = "settings preferences options";
    const SETTINGS = new Set(["settings", "fontUp", "fontDown", "fontReset", "scrollback", "fontFamily", "apiKeys"]);
    const also = (id) => SETTINGS.has(id) ? SETTING : "";
    // What a setting is now, beside the command that changes it: choosing a
    // scrollback or a font meant opening the question to find out.
    const value = {
      fontUp: fontSize + "px", fontDown: fontSize + "px", fontReset: fontSize + "px",
      scrollback: scrollback.toLocaleString("en") + " lines", fontFamily: prefs.fontFamily || "the default font",
      // One label for both ways a toggle goes, so the hint says which it is.
      toggleBroadcastMember: id && s.panes && s.panes[id]
        ? (s.panes[id].broadcast ? "in the broadcast set" : "out of the broadcast set") : "",
    };
    const now = (id, keys) => [keys, value[id] && "now " + value[id]].filter(Boolean).join(" · ");
    const cmds = keyTable
      .filter((k) => !k.noPalette && ACTIONS[k.id])
      .map((k) => ({ label: k.label, hint: now(k.id, k.keys), also: also(k.id), run: () => runAction(k.id) }));
    // The picker's own entries belong in the action table with everything
    // else, and are offered here only for as long as the table has not caught
    // up — so choosing an agent is reachable from the palette either way, and
    // never twice. The key dialog is in the same position: the table has no
    // entry for it, and without this there was no way to open it at all.
    [["newAgentTabChoose", "New agent tab (choose agent)…"],
     ["splitRightChoose", "Split right (choose agent)…"],
     ["apiKeys", "API keys…"],
     ["scrollback", "Terminal scrollback…"],
     ["fontFamily", "Terminal font…"],
     // Renaming was a double-click on the tab and nothing else: nothing a
     // keyboard could reach, and nothing that said it could be done.
     ["renameTab", "Rename this tab…"],
     // Moving the keyboard between panes had no key and no command at all.
     ["focusNextPane", "Focus the next pane"],
     ["focusPrevPane", "Focus the previous pane"]].forEach(([id, label]) => {
      if (keyTable.some((k) => k.id === id)) return;
      cmds.push({ label: label, hint: now(id), also: also(id), run: () => runAction(id) });
    });
    // A setting kept as "off": the entry turns it back on while it is off and
    // off while it is on, and says which it did - through setOff, as the
    // settings' switch for it does.
    const toggle = (field, [turnOn, turnOff]) => {
      const off = !!prefs[field];
      cmds.push({ label: off ? turnOn : turnOff, also: SETTING, run: () => setOff(field, !off) });
    };
    // Desktop notifications reach past the window, and the only way to stop
    // them was the browser's own permission - which belongs to the page's
    // origin, changes with the port on every run, and so was asked again, and
    // had to be refused again, every time the application started.
    toggle("notificationsOff", ["Turn desktop notifications on", "Turn desktop notifications off"]);
    // The cursor's blink was fixed in the source, and one blinking cursor
    // among a tab of still terminals is a distraction some people want gone.
    toggle("cursorSteady", ["Make the terminal cursor blink", "Stop the terminal cursor blinking"]);
    // What the agents write was out of a screen reader's reach, and the one
    // way to reach it has to be findable by somebody who cannot see the
    // settings: this entry is it.
    cmds.push(prefs.screenReader
      ? { label: "Turn screen reader support off", also: SETTING + " accessibility", run: () => setScreenReader(false) }
      : { label: "Turn screen reader support on", also: SETTING + " accessibility", run: () => setScreenReader(true) });
    // A hint sent away stays away, which is the point, but there was no way
    // back for one dismissed by mistake short of editing prefs.json.
    if ((prefs.dismissedTips || []).length) {
      cmds.push({
        label: "Show the tips again",
        also: SETTING,
        run: () => { send({ cmd: "resetTips" }); notice("The tips will show again where they apply", false); },
      });
    }
    // The background check for a new release could be turned off only with
    // an environment variable set before the application started.
    toggle("updatesOff", ["Turn update checks on", "Turn update checks off"]);
    (s.projects || []).forEach((p) => {
      if (p.active) return;
      // Whether anybody there is waiting on you is what decides where to go
      // next, and the entry showed only the folder.
      const waiting = p.waiting ? "▲ " + p.waiting + " waiting · " : "";
      cmds.push({ label: "Switch to project: " + p.name, hint: waiting + p.root, run: () => send({ cmd: "selectProject", root: p.root }) });
      cmds.push({ label: "Split into project: " + p.name, hint: p.root, run: () => send({ cmd: "splitPane", dir: "h", root: p.root }) });
    });
    (s.tabs || []).forEach((t) => {
      if (t.id === s.activeTab) return;
      // The strip marks a tab with an agent waiting; the palette did not.
      const hint = t.attention ? "▲ an agent here is waiting" : "";
      cmds.push({ label: "Go to tab: " + t.title, hint, run: () => send({ cmd: "selectTab", id: t.id }) });
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

  /** The commands last run from the palette, newest first, by label. Several
   *  have no key of their own and are reached only from here - tiling,
   *  restarting a pane, giving one a tab of its own, the settings - and one
   *  used a minute ago had to be typed for again every time. While nothing
   *  has been typed they come first. */
  const palRecent = [];
  function recentFirst(all) {
    const first = palRecent.map((label) => all.find((c) => c.label === label)).filter(Boolean);
    return first.concat(all.filter((c) => !first.includes(c)));
  }
  function runFromPalette(c) {
    closePalette();
    if (!c) return;
    const at = palRecent.indexOf(c.label);
    if (at >= 0) palRecent.splice(at, 1);
    palRecent.unshift(c.label);
    palRecent.length = Math.min(palRecent.length, 5);
    c.run();
  }

  /** Where the keyboard was when the palette opened, to go back to. The
   *  palette opens over a dialog as readily as over the terminals, and sending
   *  the keyboard to a terminal on the way out put it behind a dialog that was
   *  still open, where the next thing typed went to an agent. */
  let palReturn = null;

  /** paletteMatches is what a query finds: the commands whose name holds
   *  every word typed, and after them the ones whose words begin with the
   *  letters typed - "nat" for New agent tab, "rp" for Restart pane - which
   *  is how a command is typed for in most palettes and found nothing here.
   *  A single letter spells out nearly everything, so it is left to the
   *  first kind of match. */
  function paletteMatches(all, q) {
    const words = q.split(/\s+/);
    const initials = (label) => label.toLowerCase().split(/[^a-z0-9]+/).filter(Boolean).map((w) => w[0]).join("");
    const text = (c) => (c.also ? c.label + " " + c.also : c.label).toLowerCase();
    const named = all.filter((c) => words.every((w) => text(c).includes(w)));
    const spelled = all.filter((c) => !named.includes(c) &&
      words.every((w) => w.length > 1 && initials(c.label).includes(w)));
    return named.concat(spelled);
  }

  function openPalette() {
    palReturn = document.activeElement;
    $("palette").hidden = false;
    const input = $("palette-input");
    input.value = "";
    palIndex = 0;
    renderPalette();
    input.focus();
  }
  function closePalette() {
    $("palette").hidden = true;
    const back = palReturn;
    palReturn = null;
    if (back && back.isConnected && back !== document.body) back.focus();
    else focusTerminal();
  }
  function renderPalette() {
    const q = $("palette-input").value.trim().toLowerCase();
    const all = paletteCommands();
    palItems = q ? paletteMatches(all, q) : recentFirst(all);
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
      // On the pointer moving, not on its entering the row. The arrow keys
      // scroll the list, which slides a row under a pointer that has not
      // moved, and the browser reports that as the pointer entering it: the
      // highlight jumped back under the pointer each time the list scrolled,
      // and walking past the bottom of the box could not be done.
      row.onmousemove = () => selectPaletteRow(i);
      row.onclick = () => runFromPalette(c);
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
  /** listStep is where a key moves the highlight of a list walked from the
   *  field above it, or null for a key that is not the list's. The arrows
   *  move one row and Page Up and Page Down a boxful; Home and End go to the
   *  ends while the field is empty, and move its caret once there is text.
   *  With only the arrows, the last of the palette's forty-odd commands was
   *  forty presses away. */
  const LIST_PAGE = 8;
  function listStep(e, at, count, empty) {
    if (!count) return null;
    const last = count - 1;
    switch (e.key) {
      case "ArrowDown": return Math.min(at + 1, last);
      case "ArrowUp": return Math.max(at - 1, 0);
      case "PageDown": return Math.min(at + LIST_PAGE, last);
      case "PageUp": return Math.max(at - LIST_PAGE, 0);
      case "Home": return empty ? 0 : null;
      case "End": return empty ? last : null;
      default: return null;
    }
  }

  function paletteKey(e) {
    if (e.key === "Escape") { e.preventDefault(); closePalette(); return; }
    const to = listStep(e, palIndex, palRows.length, !$("palette-input").value);
    if (to !== null) { e.preventDefault(); selectPaletteRow(to); return; }
    if (e.key === "Enter") {
      e.preventDefault();
      runFromPalette(palItems[palIndex]);
    }
  }

  // --------------------------------------------------------------- history

  let history = null;
  let detaching = false;
  /** What the conversations are narrowed to, kept across the redraws a
   *  refresh brings and dropped when the dialog is opened again. */
  let historyQuery = "";

  function openHistory() {
    dialog = "history";
    historyQuery = "";
    openOverlay("Conversations", "history");
    $("overlay-body").textContent = "";
    $("overlay-body").append(el("div", "dir-empty", "Reading transcripts…"));
    send({ cmd: "conversations" });
  }

  /** renderHistory lists the project's stored Claude conversations so any of
   *  them can be picked up again, not just the ones a pane is attached to. */
  function renderHistory(msg) {
    if (msg) history = msg;
    if (dialog !== "history") return; // see openWorktrees
    const m = history || {};
    const body = $("overlay-body");
    body.textContent = "";

    if (m.error) { body.append(el("p", null, m.error)); return; }
    if (!m.items || !m.items.length) {
      body.append(el("div", "dir-empty", "No stored conversations for this project yet."));
      return;
    }

    // Summaries often begin alike, so typing the start of one - all the list
    // itself answers - does not tell them apart. The field narrows the list
    // to the conversations holding every word typed, hiding rows rather than
    // drawing them again, so the field keeps its caret.
    const holds = (hay) => historyQuery.trim().toLowerCase().split(/\s+/).filter(Boolean).every((w) => hay.includes(w));
    const field = el("input", "pick-filter");
    field.id = "history-filter";
    field.type = "text";
    field.autocomplete = "off";
    field.spellcheck = false;
    field.placeholder = "Type to narrow the list…";
    field.setAttribute("aria-label", "Find a conversation");
    field.value = historyQuery;
    field.oninput = () => {
      historyQuery = field.value;
      for (const row of wrap.querySelectorAll("div.conv-row")) row.hidden = !holds(row.dataset.hay);
      sayNone();
    };
    field.onkeydown = (ev) => {
      if (ev.key !== "ArrowDown") return;
      const first = [...wrap.querySelectorAll("div.conv-row")].find((r) => !r.hidden && r.getAttribute("role") === "button");
      if (first) { ev.preventDefault(); first.focus(); }
    };
    body.append(field);

    const wrap = section("Resume a conversation");
    m.items.forEach((c) => {
      const row = el("div", "conv-row" + (c.open ? " open" : ""));
      row.id = "conv-" + encodeURIComponent(c.id);
      row.dataset.hay = (c.summary + " " + c.id).toLowerCase();
      row.hidden = !holds(row.dataset.hay);
      const main = el("div", "conv-main");
      // Cut short at the row's width, and summaries often begin alike -
      // agents given the same brief open with the same words - so the part
      // that tells two apart was the part hidden. The whole of it is in the
      // bubble.
      main.append(describe(el("div", "conv-summary", c.summary), c.summary));
      const meta = el("div", "conv-meta");
      meta.append(el("span", null, c.ago));
      meta.append(el("span", null, c.messages + " entries"));
      if (c.kb) meta.append(el("span", null, c.kb + " KB"));
      meta.append(el("span", "conv-id", c.id.slice(0, 8)));
      if (c.open) meta.append(el("span", null, "already open"));
      main.append(meta);
      row.append(main);

      if (!c.open) {
        const resume = () => {
          const req = { cmd: "resumeConversation", id: c.id, path: m.cwd, text: c.summary };
          // The agent the listing says recorded it is the one it reopens
          // with: every API agent's conversations are kept in one folder.
          if (c.agent) req.agent = c.agent;
          send(req);
          closeOverlay();
        };
        const open = el("button", "chip primary", "Resume");
        open.onclick = (ev) => { ev.stopPropagation(); resume(); };
        // The row is what the keyboard walks - the arrows move through the
        // conversations and Enter resumes one, as in the other lists - so the
        // button is there for the pointer and is not a stop of its own. The
        // rows answered no key at all, and a long history was a button to
        // tab to for every conversation in it.
        open.tabIndex = -1;
        row.append(open);
        rowAction(row, resume, false, c.summary);
      }
      wrap.append(row);
    });
    // Narrowed to nothing, the list simply went blank, which reads the same
    // as a project with no conversations or a list still being read.
    const none = el("div", "dir-empty", "No conversation matches that.");
    none.id = "history-none";
    const sayNone = () => { none.hidden = [...wrap.querySelectorAll("div.conv-row")].some((r) => !r.hidden); };
    wrap.append(none);
    sayNone();
    body.append(wrap);

    const tools = el("div", "wt-tools");
    const refresh = el("button", "chip", "Refresh");
    refresh.onclick = () => send({ cmd: "conversations" });
    tools.append(refresh, rootLabel(m.cwd || ""));
    body.append(tools);
  }

  // ---------------------------------------------------------------- review

  let changes = null;
  let selectedFile = null;
  let diffText = "";

  /* The review is read once and the agents go on writing: a review left open
   * listed the files as they had been minutes before, until Refresh was
   * pressed. The pane headers are told each checkout's counts on every push,
   * so the review compares them for its own checkout and reads the tree again
   * when they move - but not while a fetch, pull, push or commit is under
   * way, whose buttons are disabled until its own answer comes back: drawing
   * the dialog again then would give them back, and a second push could be
   * sent while the first was still running. */
  let changesBusy = false;
  let changesCounts = "";

  /** countsIn is what the pushes say about one checkout: the changed, new,
   *  ahead and behind counts of every pane working in it. */
  function countsIn(s, cwd) {
    const norm = (p) => String(p || "").replace(/\\/g, "/").replace(/\/+$/, "");
    const root = norm(cwd);
    return Object.values((s && s.panes) || {})
      .filter((v) => { const c = norm(v.cwd); return c === root || c.startsWith(root + "/"); })
      .map((v) => [v.dirty || 0, v.untracked || 0, v.ahead || 0, v.behind || 0].join(","))
      .sort().join("|");
  }

  function followChanges(s) {
    if (dialog !== "changes" || !changes || !changes.cwd || changesBusy) return;
    const now = countsIn(s, changes.cwd);
    if (now === changesCounts) return;
    changesCounts = now;
    // follow, so the server knows nobody asked to see this one: its new rows
    // are marked rather than taken as looked at (see reviewSeen).
    send({ cmd: "changes", path: changes.cwd, follow: true });
  }

  /** What somebody has looked at in the review, as the commit made from it
   *  sends back: the checkout, each listed file's stamp by path, and how many
   *  more the list left out.
   *
   *  The panel reads the tree again by itself whenever an agent moves it, and
   *  the commit sent whatever the latest reading held, so a file an agent
   *  added a moment before the press joined the commit with nobody having
   *  seen it. The list the commit sends is the one shown when the panel
   *  opened or Refresh was pressed; the server refuses it when the tree has
   *  moved since, and answers with the tree as it is, which is then the list
   *  to check. reviewBase is the list the rows are marked against, which the
   *  refusal leaves alone so the rows that moved stay marked. */
  let reviewSeen = null;
  let reviewBase = null;

  function reviewOf(msg) {
    return { cwd: msg.cwd, stamps: new Map((msg.files || []).map((f) => [f.path, f.stamp || ""])), omitted: msg.omitted || 0 };
  }

  function noteReview(msg) {
    const listing = reviewOf(msg);
    if (!reviewSeen || reviewSeen.cwd !== msg.cwd || msg.reason === "asked" || msg.reason === "committed") {
      reviewSeen = reviewBase = listing;
    } else if (msg.reason === "refused") {
      reviewSeen = listing;
    }
  }

  /** How a file differs from the list somebody last looked at: "new", "edited"
   *  or "" for not at all. */
  function reviewMark(f) {
    if (!reviewBase || !changes || reviewBase.cwd !== changes.cwd) return "";
    if (!reviewBase.stamps.has(f.path)) return "new";
    const was = reviewBase.stamps.get(f.path);
    return was && f.stamp && was !== f.stamp ? "edited" : "";
  }
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
    // Where the file list and the diff were scrolled to, to put back below if
    // the same file is still the one shown.
    const was = changeView && { file: selectedFile, list: changeView.list.scrollTop, diff: changeView.diff.scrollTop,
      panel: changeView.diff };
    changeView = null;
    if (msg) {
      changes = msg;
      noteReview(msg);
      // Whatever was under way has answered, and this is the tree the counts
      // are now measured against.
      changesBusy = false;
      changesCounts = countsIn(state, msg.cwd);
      // Keep the selection if that file is still in the list.
      if (selectedFile && !(msg.files || []).some((f) => f.path === selectedFile)) {
        selectedFile = null;
        diffText = "";
      } else if (selectedFile && diffText) {
        // The tree is read again because it changed, and the file on show
        // may be what changed: its diff stayed as it was first read, beside
        // a list saying otherwise. The old one stays up until the new one
        // is back, rather than a "Loading" flashing under the reader.
        send({ cmd: "diff", path: msg.cwd, text: selectedFile });
      }
    }
    if (dialog !== "changes") return; // see openWorktrees
    const m = changes || {};
    const body = $("overlay-body");
    body.textContent = "";

    if (m.error) { body.append(el("p", null, m.error)); return; }

    // --- branch and remote state ------------------------------------------
    const head = el("div", "rev-head");
    // A rebase or a bisect runs on a detached HEAD, and the listing names
    // the branch it will go back to: shown as that branch, with "no upstream
    // yet" and a Push that could only fail.
    if (m.operation) {
      head.append(describe(el("span", "rev-branch", m.operation + " " + (m.branch || "a detached HEAD")),
        "This checkout is in the middle of " + (m.operation === "rebasing" ? "a rebase" : "a bisect") +
        ", with no branch checked out, so there is nothing to push. Finish or abort it in a terminal."));
    } else if (m.detached) {
      head.append(describe(el("span", "rev-branch", "detached at " + (m.head || "HEAD")),
        "No branch is checked out here, so there is nothing to push. Check out a branch in a terminal to push from it."));
    } else {
      head.append(describe(el("span", "rev-branch", m.branch || "detached"), TIPS.branch));
    }
    if (m.detached) {
      // Nothing about an upstream applies: there is no branch to have one.
    } else if (m.upstream) {
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
        // Pull only fast-forwards, so a branch that is ahead as well is not
        // offered one, and "Pull to catch up" would send the reader looking
        // for a button that is not there.
        parts.push(m.ahead
          ? "Both have moved on, so this branch cannot simply catch up: merge or rebase in a terminal, then push."
          : TIPS.behind);
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

    /* Fetching, pulling, pushing and committing all talk to a remote, which
     * takes seconds and sometimes considerably longer. Nothing on screen said
     * so: the button looked exactly as it had, so the natural reading was that
     * the press had not registered, and pressing it again sent a second push
     * while the first was still in flight.
     *
     * Every one of these ends by sending the working tree back, whether it
     * worked or not, which redraws this dialog with its buttons as they should
     * be. So saying so and refusing further presses until then needs nothing
     * to undo it. */
    const running = (btn, saying) => {
      btn.textContent = saying;
      for (const b of body.querySelectorAll("button")) b.disabled = true;
      changesBusy = true;
    };

    if (m.hasRemote) {
      const fetch = el("button", "chip", "Fetch");
      fetch.id = "rev-fetch";
      fetch.onclick = () => { running(fetch, "Fetching…"); send({ cmd: "gitFetch", path: m.cwd }); };
      actions.append(fetch);
      // Pull only ever fast-forwards, which a branch with commits of its own
      // as well cannot do: offered then, it could only answer with an error.
      if (m.behind && !m.ahead && !m.detached) {
        const pull = el("button", "chip", "Pull " + m.behind);
        pull.id = "rev-pull";
        pull.title = "Fast-forward from " + m.upstream;
        pull.onclick = () => { running(pull, "Pulling…"); send({ cmd: "gitPull", path: m.cwd }); };
        actions.append(pull);
      }
      // With no branch checked out there is nothing to push.
      if (!m.detached) {
        const push = el("button", "chip" + (m.ahead ? " primary" : ""), m.ahead ? "Push " + m.ahead : "Push");
        push.id = "rev-push";
        // The first push goes to origin, or to the only remote when there is
        // no origin; "to origin" was wrong for a repository without one.
        push.title = m.upstream ? "Push to " + m.upstream : "Push this branch for the first time, and track it on the remote from then on";
        push.onclick = () => { running(push, "Pushing…"); send({ cmd: "gitPush", path: m.cwd }); };
        actions.append(push);
      }
    }
    const refresh = el("button", "chip", "Refresh");
    refresh.id = "rev-refresh";
    refresh.onclick = () => { running(refresh, "Reading…"); send({ cmd: "changes", path: m.cwd }); };
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
      let moved = 0; // rows marked as changed since the list was looked at
      files.forEach((f) => {
        const mark = reviewMark(f);
        if (mark) moved++;
        const row = el("div", "rev-file" + (f.path === selectedFile ? " sel" : "") + (mark ? " moved" : ""));
        // A prefix of its own: the dialog's buttons are rev-push, rev-pull and
        // the rest, and a file called push at the top of the tree was named
        // as the Push button, which a redraw put the keyboard back on.
        row.id = "rev-file-" + encodeURIComponent(f.path);
        row.append(el("span", "rev-kind", f.label));
        if (mark) {
          row.append(describe(el("span", "rev-mark", mark), mark === "new"
            ? "Not in the list when you last looked at it."
            : "Written to again since you last looked at it."));
        }
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
        rowAction(row, () => selectChangedFile(f.path, m.cwd), true, f.path.replace(/^.*[\\/]/, ""));
        rows.set(f.path, row);
        list.append(row);
      });
      split.append(list);

      // The panel already showing this file's diff is kept rather than
      // drawn again. The tree is read again whenever an agent writes to it,
      // and the diff on show - up to three thousand lines - was built afresh
      // each time, while it was being read, for the same lines.
      const keep = was && selectedFile && was.file === selectedFile && diffText ? was.panel : null;
      const diff = keep || el("div", "rev-diff");
      // It scrolls on its own, three hundred pixels of a diff that can run
      // to thousands of lines, and a box that cannot be focused cannot be
      // scrolled from the keyboard. fillDiff names it after its file.
      if (!keep) {
        diff.tabIndex = 0;
        diff.setAttribute("role", "region");
      }
      split.append(diff);
      body.append(split);
      changeView = { rows, diff, list };
      if (!keep) fillDiff();
      // A refresh, a fetch or a push answers with the working tree again, and
      // drawing it afresh put a long diff back at its first line under the
      // person reading it, and the file list back at its top.
      if (was && was.file === selectedFile) { list.scrollTop = was.list; diff.scrollTop = was.diff; }

      // --- commit -----------------------------------------------------------
      const commit = el("div", "rev-commit");
      const box = el("textarea");
      box.id = "commit-message";
      box.placeholder = "Commit message (Ctrl+Enter commits)";
      // A draft belongs to the checkout it was written for: one draft for
      // them all carried a message into the review of another worktree.
      box.value = commitDrafts.get(m.cwd) || "";
      box.oninput = () => { commitDrafts.set(m.cwd, box.value); };
      const buttons = el("div", "rev-commit-buttons");
      const doCommit = (btn, push) => {
        // The buttons wait while anything is out, and the message box, which
        // commits on Ctrl+Enter, did not: pressed twice it sent the same
        // commit again while the first was still running.
        if (changesBusy) return;
        const message = box.value.trim();
        if (!message) { notice("A commit message is required", true); box.focus(); return; }
        running(btn, push ? "Committing and pushing…" : "Committing…");
        // What was looked at goes with the commit, stamps and all, rather
        // than whatever the latest reading of the tree holds: the server
        // refuses a tree that has moved since.
        const seen = reviewSeen && reviewSeen.cwd === m.cwd ? reviewSeen : reviewOf(m);
        const cmd = { cmd: "commit", path: m.cwd, text: message, push, files: [...seen.stamps.keys()], omitted: seen.omitted };
        // Only the files that were stamped: a server that stamps nothing is
        // sent the commit it always was.
        const stamps = [...seen.stamps].filter(([, s]) => s);
        if (stamps.length) cmd.stamps = Object.fromEntries(stamps);
        send(cmd);
        commitPending = true;
        commitPendingCwd = m.cwd;
      };
      // The server lists at most two thousand files and counts the rest, and
      // the button counted only the ones listed while the commit records every
      // one of them: "Commit 2000 files" committed thousands more. The count
      // takes them in, and a line that stays says they are there.
      const total = files.length + (m.omitted || 0);
      if (m.omitted) {
        body.append(el("div", "rev-omitted", m.omitted.toLocaleString("en") + " more not listed — the commit includes them"));
      }
      const gone = reviewBase && reviewBase.cwd === m.cwd
        ? [...reviewBase.stamps.keys()].filter((p) => !rows.has(p)).length : 0;
      if (moved || gone) {
        const parts = [];
        if (moved) parts.push(moved + (moved === 1 ? " marked file has" : " marked files have") + " changed since you looked");
        if (gone) parts.push(gone + (gone === 1 ? " file you saw is" : " files you saw are") + " no longer changed");
        body.append(el("div", "rev-moved", parts.join(", and ") + ". Check the list before committing."));
      }
      const c1 = el("button", "chip primary", "Commit " + total + " file" + (total === 1 ? "" : "s"));
      c1.id = "rev-commit";
      c1.onclick = () => doCommit(c1, false);
      // The message is the last thing written, and a box like this commits on
      // Ctrl+Enter nearly everywhere else; here it took the pointer, or Tab
      // past the box to the button.
      box.onkeydown = (ev) => {
        if (ev.key !== "Enter" || !(ev.ctrlKey || ev.metaKey)) return;
        ev.preventDefault();
        doCommit(c1, false);
      };
      buttons.append(c1);
      if (m.hasRemote && !m.detached) {
        const c2 = el("button", "chip", "Commit and push");
        c2.id = "rev-commit-push";
        c2.onclick = () => doCommit(c2, true);
        buttons.append(c2);
      }
      commit.append(box, buttons);
      body.append(commit);
    }

    const tools = el("div", "wt-tools");
    tools.append(rootLabel(m.cwd || ""));
    body.append(tools);
  }

  /** The commit message being written for each checkout, by its root. */
  const commitDrafts = new Map();
  /** Whether a commit has been asked for and not yet answered. The message
   *  stays in the box until it has: a commit that fails - a hook refusing it,
   *  an identity git has not been given - answers by redrawing the dialog, and
   *  clearing the draft when the commit was sent meant the message went with
   *  the redraw and had to be written again. The server's first word on a
   *  commit is a notice saying whether it was made. */
  let commitPending = false;
  let commitPendingCwd = "";

  function commitAnswered(isError) {
    if (!commitPending) return;
    commitPending = false;
    if (!isError) commitDrafts.delete(commitPendingCwd);
  }

  /** The diff of a row chosen within diffGap of the last one asked for waits
   *  for the rest of that gap, and only the row the selection is on by then
   *  is asked about. */
  const diffGap = 150;
  let diffTimer = 0;
  let diffAskedAt = 0;

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
    // Arrowing down the list asked for the diff of every row passed on the
    // way, each one a git process or two on the far side. A row chosen on
    // its own is still asked about at once; one chosen hard on the heels of
    // another waits out the gap, and only the row the selection has reached
    // by then is asked about.
    clearTimeout(diffTimer);
    const ask = () => {
      diffTimer = 0;
      if (dialog !== "changes" || selectedFile !== path) return;
      diffAskedAt = Date.now();
      send({ cmd: "diff", path: cwd, text: path });
    };
    const wait = diffAskedAt + diffGap - Date.now();
    if (wait <= 0) ask();
    else diffTimer = setTimeout(ask, wait);
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
  function fillDiff(keep) {
    if (!changeView) return;
    const diff = changeView.diff;
    const top = diff.scrollTop;
    // Named after the file it shows, since it is a stop of its own on the way
    // round the dialog and the list beside it is where the name was.
    const name = selectedFile ? "Diff of " + selectedFile : "Diff";
    if (diff.getAttribute("aria-label") !== name) diff.setAttribute("aria-label", name);
    diff.textContent = "";
    if (!selectedFile) diff.append(el("span", "meta", "Select a file to see what changed."));
    else if (!diffText) diff.append(el("span", "meta", "Loading diff…"));
    else renderDiffInto(diff, diffText);
    diff.scrollTop = keep ? top : 0;
  }

  function showDiff(msg) {
    // A stale reply for another file -- or for a file of the same name in
    // another checkout, reviewed a moment before this one.
    if (msg.file !== selectedFile || !changes || msg.cwd !== changes.cwd) return;
    // A diff already on show is being read again, and the reader's place in
    // it is kept; a file just chosen starts at its top.
    const again = !!diffText;
    const text = msg.error ? msg.error : msg.text;
    // The diff on show is read again whenever the tree moves, and it is
    // most often just as it was - an agent wrote to some other file - so
    // drawing its lines again would change nothing on screen.
    if (again && text === diffText && changeView && changeView.diff.childNodes.length) return;
    diffText = text;
    if (changeView) fillDiff(again);
    else renderChanges();
  }

  /** How many lines of a diff are drawn before the rest are offered rather than
   *  built. The Go side caps a diff at 400KB, which bounds the bytes it sends
   *  but says nothing about the lines: a generated file of short lines reaches
   *  that in something like two hundred thousand of them, and an element per
   *  line is then two hundred thousand elements built and laid out inside a
   *  panel three hundred pixels tall, while the window does nothing else.
   *  Three thousand is more than anyone reads in one panel. */
  const DIFF_LINES = 3000;

  /** renderDiffInto colours a unified diff without a syntax highlighter. */
  function renderDiffInto(host, text) {
    const lines = text.split("\n");
    const kinds = diffKinds(lines);
    const diffLine = (i) => el("div", kinds[i], lines[i] || " ");
    const shown = Math.min(lines.length, DIFF_LINES);
    for (let i = 0; i < shown; i++) host.append(diffLine(i));
    if (shown === lines.length) return;

    const rest = lines.length - shown;
    const note = el("div", "meta", rest + " more lines are not drawn.");
    const more = el("button", "chip", "Show them");
    more.onclick = () => {
      // The button goes with the note, and pressed from the keyboard it took
      // the keyboard with it. It goes to the diff instead, which is what the
      // press was for, where the arrow keys scroll the lines just drawn.
      const had = document.activeElement === more;
      note.remove();
      more.remove();
      for (let i = shown; i < lines.length; i++) host.append(diffLine(i));
      if (had) {
        // A stop on Tab's way round, as renderChanges made it: -1 would take
        // it off that way for as long as the dialog stayed open.
        if (host.getAttribute("tabindex") !== "0") host.tabIndex = 0;
        host.focus();
      }
    };
    host.append(note, more);
  }

  /** diffKinds says how each line of a unified diff is drawn. A line starting
   *  with --- or +++ is a file's header only above that file's first hunk:
   *  after it, "--- comment" is a removed SQL comment, "----" a removed
   *  Markdown rule and "+++i;" an added increment, and all of them were drawn
   *  grey as headers. A hunk's lines start with a space, + or -, so "diff "
   *  at the start of one always begins the next file. */
  function diffKinds(lines) {
    let header = true;
    return lines.map((line) => {
      if (line.startsWith("diff ")) { header = true; return "meta"; }
      if (line.startsWith("@@")) { header = false; return "hunk"; }
      if (header && (line.startsWith("+++") || line.startsWith("---") || line.startsWith("index "))) return "meta";
      if (line.startsWith("+")) return "add";
      if (line.startsWith("-")) return "del";
      return "";
    });
  }

  // ------------------------------------------------------------------ github

  /** gh's own status -- installed?, signed in? -- shared by the GitHub dialog's
   *  own header and the Settings section, which show the same thing. */
  let ghStatus = null;
  /** The working tree the GitHub panel is reading, echoed back on every answer
   *  the way changes.cwd is, so a request outlived by a project switch still
   *  lands on the project it was asked about. */
  let ghDir = "";
  /** Which of an install or a sign-in is running right now -- "install",
   *  "login", or "" when neither is -- and the lines gh has printed so far.
   *  Cleared each time one starts: this is a running attempt's own log, not
   *  a history kept once it is over. */
  let ghRunning = "";
  let ghRunLines = [];
  /** The one-time code a running sign-in has shown, until it ends. */
  let ghLoginCode = "";

  /** Which tab of the GitHub dialog is on show. */
  let ghTab = "prs";
  let ghPRState = "";
  let ghIssueState = "";
  let ghPRList = null;
  let ghIssueList = null;
  let ghChecksData = null;
  /** The pull request or issue open in the dialog, in place of the list, or
   *  null for the list itself. */
  let ghSelected = null;
  let ghCreateOpen = false;
  let ghCreateDraft = { title: "", body: "", base: "", draft: false };
  /** A comment being written, kept by "kind:number" for as long as the
   *  dialog is open, the same reason worktrees keeps wtDraft: the item is
   *  redrawn whole on every answer, and a comment half-typed would go with it. */
  const ghCommentDrafts = new Map();
  /** Whether a create just asked for is still out, so its answer is told
   *  from an ordinary read of the same kind of item. */
  let ghCreating = false;

  /** ghClass buckets any of gh's several words for a check or a run's outcome
   *  down to the four this panel actually draws differently. */
  function ghClass(s) {
    s = (s || "").toLowerCase();
    if (["success", "pass", "passing"].includes(s)) return "pass";
    if (["failure", "fail", "failing", "cancelled", "cancel", "timed_out", "startup_failure", "action_required"].includes(s)) return "fail";
    if (["pending", "queued", "in_progress", "requested", "waiting"].includes(s)) return "pending";
    return "neutral";
  }

  function ghInstallClick() {
    ghRunning = "install";
    ghRunLines = [];
    keepFocus(dialog === "github" ? renderGithub : renderGhSettings);
    send({ cmd: "ghInstall" });
  }
  function ghLoginClick() {
    ghRunning = "login";
    ghRunLines = [];
    ghLoginCode = "";
    keepFocus(dialog === "github" ? renderGithub : renderGhSettings);
    send({ cmd: "ghLogin" });
  }
  function ghLogoutClick() {
    if (!window.confirm("Sign out of GitHub?")) return;
    send({ cmd: "ghLogout", path: ghDir });
  }

  /** ghProgressBox draws the log of a running install or sign-in, and, for a
   *  sign-in, the one-time code the moment gh has printed it. Shared by the
   *  dialog and the settings section, which show the same run. */
  function ghProgressBox() {
    const box = el("div", "gh-run");
    if (ghRunning === "login" && ghLoginCode) {
      box.append(el("p", null, "Enter this code at github.com/login/device:"));
      box.append(el("div", "gh-code", ghLoginCode));
    } else {
      box.append(el("p", null, ghRunning === "install" ? "Installing the GitHub CLI…" : "Opening github.com/login/device…"));
    }
    if (ghRunLines.length) box.append(el("div", "gh-log", ghRunLines.join("\n")));
    if (ghRunning === "login") {
      const row = el("div", "wt-actions");
      const cancel = el("button", "chip", "Cancel");
      cancel.onclick = () => send({ cmd: "ghLoginCancel" });
      row.append(cancel);
      box.append(row);
    }
    return box;
  }

  // -------------------------------------------------------- settings section

  /** ghShown is where the GitHub status is drawn right now, or null while it
   *  is on neither screen -- the same idea as remoteHost. */
  function ghShown() {
    if (dialog === "github") return null; // renderGithub draws its own header
    if (dialog === "settings" && settingsSection === "github") return $("settings-host");
    return null;
  }

  function renderGhSettings() {
    const body = ghShown();
    if (!body) return;
    body.textContent = "";
    if (!ghStatus) { body.append(el("div", "dir-empty", "Checking…")); return; }
    if (ghRunning) { body.append(ghProgressBox()); return; }
    const st = ghStatus;
    if (st.error) body.append(el("p", "remote-error", st.error));

    if (!st.installed) {
      body.append(el("p", "fan-hint",
        "The GitHub CLI (gh) is what talks to GitHub for pull requests, issues and CI status here. It is not on this machine yet."));
      const row = el("div", "update-row");
      if (st.installManager) {
        const install = el("button", "chip primary", "Install with " + st.installManager);
        install.id = "gh-install";
        install.onclick = ghInstallClick;
        row.append(install);
      }
      const link = el("a", "chip", "Get it from cli.github.com");
      link.href = st.installUrl || "https://cli.github.com";
      link.target = "_blank";
      link.rel = "noopener";
      row.append(link);
      body.append(row);
      return;
    }
    if (!st.loggedIn) {
      body.append(el("p", "fan-hint",
        "gh is installed but not signed in. Signing in opens a one-time code to enter at github.com/login/device."));
      const row = el("div", "update-row");
      const login = el("button", "chip primary", "Sign in with GitHub");
      login.id = "gh-signin";
      login.onclick = ghLoginClick;
      row.append(login);
      body.append(row);
      return;
    }
    body.append(el("div", "remote-status", "Signed in to " + (st.host || "github.com") + " as " + st.account));
    const row = el("div", "update-row");
    const logout = el("button", "chip danger", "Sign out");
    logout.onclick = ghLogoutClick;
    row.append(logout);
    body.append(row);
  }

  // ----------------------------------------------------------------- dialog

  /** ghRequestTab asks for whatever the tab on show needs. */
  function ghRequestTab() {
    if (ghTab === "prs") send({ cmd: "ghPRs", path: ghDir, ghState: ghPRState });
    else if (ghTab === "issues") send({ cmd: "ghIssues", path: ghDir, ghState: ghIssueState });
    else send({ cmd: "ghChecks", path: ghDir });
  }

  function openGithub(path) {
    dialog = "github";
    ghSelected = null;
    ghCreateOpen = false;
    ghStatus = null;
    ghRunning = "";
    ghRunLines = [];
    ghLoginCode = "";
    if (path) ghDir = path;
    openOverlay("GitHub", "github");
    $("overlay-body").append(el("div", "dir-empty", "Reading GitHub status…"));
    send({ cmd: "ghStatus", path: ghDir });
    ghRequestTab();
  }

  function renderGithub() {
    if (dialog !== "github") return; // see openWorktrees
    const body = $("overlay-body");
    body.textContent = "";

    if (!ghStatus) { body.append(el("div", "dir-empty", "Reading GitHub status…")); return; }
    if (ghRunning) { body.append(ghProgressBox()); return; }
    if (!ghStatus.installed || !ghStatus.loggedIn) {
      body.append(el("p", "fan-hint", ghStatus.installed
        ? "Sign in to GitHub to see pull requests, issues and CI here."
        : "The GitHub CLI is not installed on this machine yet."));
      const go = el("button", "chip primary", "Open GitHub settings");
      go.onclick = () => openSettings("github");
      body.append(go);
      return;
    }

    const tabs = el("div", "gh-tabs");
    tabs.setAttribute("role", "tablist");
    tabs.setAttribute("aria-label", "GitHub");
    [["prs", "Pull requests"], ["issues", "Issues"], ["checks", "Checks"]].forEach(([id, label]) => {
      const on = ghTab === id;
      const b = el("button", "chip gh-tab" + (on ? " sel" : ""), label);
      b.setAttribute("role", "tab");
      b.setAttribute("aria-selected", String(on));
      b.onclick = () => {
        if (ghTab === id) return;
        ghTab = id;
        ghSelected = null;
        ghCreateOpen = false;
        keepFocus(renderGithub);
        ghRequestTab();
      };
      tabs.append(b);
    });
    body.append(tabs);

    if (ghSelected) body.append(ghDetailView());
    else if (ghTab === "checks") body.append(ghChecksView());
    else body.append(ghListView(ghTab));

    body.append(rootLabel(ghDir));
  }

  /** ghListView is the Pull requests and Issues tabs: a state filter, a way to
   *  open a new one, and the list itself. */
  function ghListView(kind) {
    const wrap = el("div", "gh-list-wrap");
    const list = kind === "prs" ? ghPRList : ghIssueList;
    const state = kind === "prs" ? ghPRState : ghIssueState;

    const bar = el("div", "wt-actions");
    const states = kind === "prs"
      ? [["", "Open"], ["closed", "Closed"], ["merged", "Merged"], ["all", "All"]]
      : [["", "Open"], ["closed", "Closed"], ["all", "All"]];
    states.forEach(([val, label]) => {
      const b = el("button", "chip" + (state === val ? " on" : ""), label);
      b.onclick = () => {
        if (kind === "prs") ghPRState = val; else ghIssueState = val;
        if (kind === "prs") ghPRList = null; else ghIssueList = null;
        keepFocus(renderGithub);
        ghRequestTab();
      };
      bar.append(b);
    });
    const create = el("button", "chip primary", kind === "prs" ? "New pull request" : "New issue");
    create.onclick = () => {
      ghCreateOpen = true;
      ghCreateDraft = { title: "", body: "", base: "", draft: false };
      keepFocus(renderGithub);
    };
    bar.append(create);
    wrap.append(bar);

    if (ghCreateOpen) wrap.append(ghCreateForm(kind));

    if (!list) { wrap.append(el("div", "dir-empty", "Reading…")); return wrap; }
    if (list.error) { wrap.append(el("p", "remote-error", list.error)); return wrap; }
    const items = list.items || [];
    if (!items.length) { wrap.append(el("div", "dir-empty", "Nothing here.")); return wrap; }
    items.forEach((item) => wrap.append(ghRow(kind, item)));
    return wrap;
  }

  function ghRow(kind, item) {
    const row = el("div", "wt-row");
    row.dataset.key = kind + ":" + item.number;
    const main = el("div", "wt-main");
    const title = el("div", "wt-title");
    const open = el("button", "gh-open", "#" + item.number + " " + item.title);
    open.onclick = () => ghOpenItem(kind, item.number);
    title.append(open);
    if (kind === "prs" && item.isDraft) title.append(el("span", "wt-flag", "draft"));
    title.append(el("span", "wt-flag", (item.state || "").toLowerCase()));
    main.append(title);
    main.append(el("div", "wt-meta", (item.author && item.author.login ? item.author.login + " · " : "") + "updated " + remoteAgo(item.updatedAt)));
    row.append(main);
    return row;
  }

  function ghOpenItem(kind, number) {
    ghSelected = { kind, number, item: null, error: "" };
    keepFocus(renderGithub);
    send({ cmd: kind === "prs" ? "ghPR" : "ghIssue", path: ghDir, ghNumber: number });
  }

  /** ghCreateForm is the new pull request / new issue form, opened above the
   *  list it will land in once gh has answered. */
  function ghCreateForm(kind) {
    const box = el("div", "gh-create");
    const title = el("input");
    title.placeholder = "Title";
    title.setAttribute("aria-label", "Title");
    title.value = ghCreateDraft.title;
    title.oninput = () => { ghCreateDraft.title = title.value; };
    box.append(title);
    const body = el("textarea");
    body.placeholder = "Description (optional)";
    body.setAttribute("aria-label", "Description");
    body.value = ghCreateDraft.body;
    body.oninput = () => { ghCreateDraft.body = body.value; };
    box.append(body);
    if (kind === "prs") {
      const row = el("div", "gh-create-row");
      const base = el("input");
      base.placeholder = "Base branch (default: the repository's own)";
      base.setAttribute("aria-label", "Base branch");
      base.value = ghCreateDraft.base;
      base.oninput = () => { ghCreateDraft.base = base.value; };
      row.append(base);
      const draftBtn = el("button", "chip" + (ghCreateDraft.draft ? " on" : ""), ghCreateDraft.draft ? "Draft: on" : "Draft: off");
      draftBtn.onclick = () => { ghCreateDraft.draft = !ghCreateDraft.draft; keepFocus(renderGithub); };
      row.append(draftBtn);
      box.append(row);
    }
    const buttons = el("div", "rev-commit-buttons");
    const submit = el("button", "chip primary", ghCreating ? "Opening…" : (kind === "prs" ? "Open pull request" : "Open issue"));
    submit.disabled = ghCreating;
    submit.onclick = () => {
      if (!ghCreateDraft.title.trim()) {
        notice(kind === "prs" ? "A pull request needs a title" : "An issue needs a title", true);
        title.focus();
        return;
      }
      ghCreating = true;
      keepFocus(renderGithub);
      if (kind === "prs") {
        send({ cmd: "ghPRCreate", path: ghDir, ghTitle: ghCreateDraft.title, ghBody: ghCreateDraft.body, ghBase: ghCreateDraft.base, ghDraft: ghCreateDraft.draft });
      } else {
        send({ cmd: "ghIssueCreate", path: ghDir, ghTitle: ghCreateDraft.title, ghBody: ghCreateDraft.body });
      }
    };
    const cancel = el("button", "chip", "Cancel");
    cancel.onclick = () => { ghCreateOpen = false; keepFocus(renderGithub); };
    buttons.append(submit, cancel);
    box.append(buttons);
    return box;
  }

  /** ghDetailView is one pull request or issue: its body, its checks when it
   *  is a pull request gh has already read the checks of, its comments, and a
   *  box to add one. */
  function ghDetailView() {
    const wrap = el("div", "gh-detail");
    const back = el("button", "chip", "← Back");
    back.onclick = () => { ghSelected = null; keepFocus(renderGithub); };
    wrap.append(back);

    const sel = ghSelected;
    if (!sel.item) {
      wrap.append(el("div", "dir-empty", sel.error || "Reading…"));
      return wrap;
    }
    const item = sel.item;
    const head = el("div", "gh-detail-head");
    head.append(el("h3", null, "#" + item.number + " " + item.title));
    const meta = el("div", "wt-meta");
    meta.append(el("span", "wt-flag", (item.state || "").toLowerCase()));
    if (sel.kind === "prs" && item.isDraft) meta.append(el("span", "wt-flag", "draft"));
    if (sel.kind === "prs" && item.headRefName) meta.append(el("span", null, item.headRefName + " → " + item.baseRefName));
    if (item.author && item.author.login) meta.append(el("span", null, item.author.login));
    head.append(meta);
    if (item.url) {
      const link = el("a", "chip", "Open on GitHub");
      link.href = item.url;
      link.target = "_blank";
      link.rel = "noopener";
      head.append(link);
    }
    wrap.append(head);

    if (item.body) wrap.append(el("div", "gh-body", item.body));

    if (sel.kind === "prs" && ghChecksData && ghChecksData.pr && ghChecksData.pr.number === item.number) {
      wrap.append(ghChecksSummaryLine(ghChecksData.summary));
    }

    const comments = item.comments || [];
    const commentsBox = section(comments.length === 1 ? "1 comment" : comments.length + " comments");
    comments.forEach((cm) => {
      const c = el("div", "gh-comment");
      c.append(el("div", "gh-comment-meta", ((cm.author && cm.author.login) || "") + " · " + remoteAgo(cm.createdAt)));
      c.append(el("div", "gh-comment-body", cm.body));
      commentsBox.append(c);
    });
    wrap.append(commentsBox);

    const key = sel.kind + ":" + item.number;
    const commentRow = el("div", "rev-commit");
    const box = el("textarea");
    box.placeholder = "Write a comment (Ctrl+Enter to post)";
    box.setAttribute("aria-label", "Comment");
    box.value = ghCommentDrafts.get(key) || "";
    box.oninput = () => ghCommentDrafts.set(key, box.value);
    const doComment = () => {
      const text = box.value.trim();
      if (!text) return;
      send({ cmd: sel.kind === "prs" ? "ghPRComment" : "ghIssueComment", path: ghDir, ghNumber: item.number, ghBody: text });
      ghCommentDrafts.delete(key);
      box.value = "";
    };
    box.onkeydown = (ev) => {
      if (ev.key !== "Enter" || !(ev.ctrlKey || ev.metaKey)) return;
      ev.preventDefault();
      doComment();
    };
    const post = el("button", "chip primary", "Comment");
    post.onclick = doComment;
    commentRow.append(box, post);
    wrap.append(commentRow);
    return wrap;
  }

  /** ghChecksView is the Checks tab: the open pull request's own checks, when
   *  there is one, and the branch's recent Actions runs regardless -- a
   *  checkout with no pull request yet still has CI worth showing. */
  function ghChecksView() {
    const wrap = el("div", "gh-checks");
    const data = ghChecksData;
    if (!data) { wrap.append(el("div", "dir-empty", "Reading…")); return wrap; }
    if (data.error) wrap.append(el("p", "remote-error", data.error));
    wrap.append(el("div", "wt-meta", "branch " + (data.branch || "—")));

    if (data.pr) {
      const prSec = section("Pull request #" + data.pr.number);
      const open = el("button", "gh-open", data.pr.title);
      open.onclick = () => ghOpenItem("prs", data.pr.number);
      prSec.append(open);
      prSec.append(ghChecksSummaryLine(data.summary));
      (data.checks || []).forEach((c) => prSec.append(ghCheckRow(c)));
      wrap.append(prSec);
    } else {
      wrap.append(el("p", "fan-hint", "This branch has no open pull request yet. The runs below are for the branch itself."));
    }

    const runsSec = section("Recent runs");
    const runs = data.runs || [];
    if (!runs.length) runsSec.append(el("div", "dir-empty", "No workflow runs found for this branch."));
    runs.forEach((r) => {
      const row = el("div", "wt-row");
      const main = el("div", "wt-main");
      const title = el("div", "wt-title");
      const link = el("a", "gh-open", r.displayTitle || r.workflowName);
      link.href = r.url;
      link.target = "_blank";
      link.rel = "noopener";
      title.append(link);
      title.append(el("span", "wt-flag gh-" + ghClass(r.conclusion || r.status), (r.conclusion || r.status || "").replace(/_/g, " ")));
      main.append(title);
      main.append(el("div", "wt-meta", r.workflowName + " · " + remoteAgo(r.updatedAt)));
      row.append(main);
      runsSec.append(row);
    });
    wrap.append(runsSec);
    return wrap;
  }

  function ghCheckRow(c) {
    const row = el("div", "wt-row");
    const main = el("div", "wt-main");
    const title = el("div", "wt-title");
    if (c.link) {
      const link = el("a", "gh-open", c.name);
      link.href = c.link;
      link.target = "_blank";
      link.rel = "noopener";
      title.append(link);
    } else {
      title.append(el("span", "wt-label", c.name));
    }
    title.append(el("span", "wt-flag gh-" + ghClass(c.bucket), (c.state || "").toLowerCase()));
    main.append(title);
    if (c.workflow) main.append(el("div", "wt-meta", c.workflow));
    row.append(main);
    return row;
  }

  function ghChecksSummaryLine(summary) {
    const s = summary || { pending: 0, passing: 0, failing: 0, overall: "none" };
    const text = s.overall === "none" ? "No checks reported"
      : [s.passing ? s.passing + " passing" : "", s.failing ? s.failing + " failing" : "", s.pending ? s.pending + " pending" : ""]
        .filter(Boolean).join(", ");
    return el("div", "gh-summary gh-" + ghClass(s.overall), text);
  }

  // ---------------------------------------------------------------- agents

  let agents = null;

  /** The overview is where you look to see which agent needs you, and it was
   *  a picture taken when it opened: an agent that stopped to wait while it
   *  was up went on being listed as working. It is asked for again whenever a
   *  push shows the counts it lists moving, in any project. */
  let agentsKey = "";
  function followAgents(s) {
    // The panes are counted too: a split adds an idle pane and closing one in
    // a tab of several takes one away, and neither moves the other counts, so
    // the list went on showing a pane that had gone.
    const key = (s.projects || []).map((p) => p.root + ":" + p.waiting + ":" + p.working + ":" + p.tabs + ":" + p.panes).join("|");
    if (key === agentsKey) return;
    agentsKey = key;
    askAgents();
  }

  /** When the overview was last asked for because something it lists moved,
   *  and the ask waiting for the second to pass. Each answer rebuilds the
   *  whole list, and with agents busy in several projects the counts move
   *  several times a second: asked for on every move, the list was rebuilt
   *  under the pointer and the keyboard as fast as it could be drawn. The
   *  first move is asked for at once, so an agent that stops to wait shows
   *  straight away; after that it is at most once a second, and a move inside
   *  the second is asked for when the second is up. */
  const AGENTS_EVERY_MS = 1000;
  let agentsAsked = 0;
  let agentsTimer = 0;
  function askAgents() {
    clearTimeout(agentsTimer);
    agentsTimer = 0;
    if (dialog !== "agents") return;
    const wait = agentsAsked + AGENTS_EVERY_MS - Date.now();
    if (wait > 0) { agentsTimer = setTimeout(askAgents, wait); return; }
    agentsAsked = Date.now();
    send({ cmd: "agents" });
  }

  /** openAgents lists every pane in every open project. The tab bar only shows
   *  the active project, so this is what answers "where is the one that needs
   *  me" once several are open. */
  function openAgents() {
    dialog = "agents";
    openOverlay("Agents", "status");
    $("overlay-body").textContent = "";
    $("overlay-body").append(el("div", "dir-empty", "Loading…"));
    clearTimeout(agentsTimer);
    agentsTimer = 0;
    send({ cmd: "agents" });
  }

  function renderAgents(msg) {
    if (msg) agents = msg;
    if (dialog !== "agents") return; // see openWorktrees
    const m = agents || {};
    const body = $("overlay-body");
    body.textContent = "";

    const items = m.items || [];
    if (!items.length) {
      body.append(el("div", "dir-empty", "No panes open."));
      return;
    }

    // So a helper's row can name the pane it belongs to -- the list stays
    // sorted by what needs a person first, which is the point of it, rather
    // than regrouped under its parent and losing that order.
    const byId = new Map(items.map((x) => [x.paneId, x]));

    const wrap = section(items.length + (items.length === 1 ? " pane" : " panes"));
    items.forEach((a) => {
      const row = el("div", "agent-row" + (a.active ? " active" : ""));
      // By the pane, because the list is drawn again as the agents change
      // what they are doing, and a row found again by its wording would lose
      // the keyboard the moment its status did.
      row.id = "agent-" + a.paneId;
      const main = el("div", "agent-main");

      const title = el("div", "agent-title");
      // The dot is the only thing carrying status here, and it has no text
      // at all, so it needs both the copy and a name of its own.
      // The row writes the status out in words further along, so the disc is
      // decoration here — unlike in a pane header, where it is all there is.
      const dot = el("span", "dot " + a.status);
      dot.setAttribute("aria-hidden", "true");
      title.append(describe(dot, TIPS[a.status] || a.status));
      // Cut short with an ellipsis, like the tab it names; the bubble has it all.
      title.append(describe(el("span", "agent-tab", a.tab || a.name), a.tab || a.name));
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
      // The same badge the pane header carries. With a dozen panes across
      // three projects, "waiting" means a different thing from Claude than
      // from a local model, and this is the list you scan to decide which to
      // go to first.
      if (a.agent) {
        meta.append(describe(el("span", null, a.model ? a.agent + " · " + a.model : a.agent), TIPS.agent));
      }
      if (a.kind === "shell") meta.append(el("span", null, "shell"));
      if (a.detail) meta.append(el("span", null, a.detail));
      // A helper's own pane, when it is still open, so it reads as part of
      // the job it was spawned for rather than an unrelated row -- the list
      // stays sorted by what needs a person first, so this is what says
      // which of them share a manager.
      if (a.parent) {
        const parent = byId.get(a.parent);
        meta.append(el("span", "agent-parent", "↳ helper of " + (parent ? (parent.tab || parent.name) : "another agent")));
      }
      // What a waiting pane's permission prompt or question wants, in the
      // same words the prompt itself would show -- so a list of several
      // waiting at once says which is worth a look first, without opening
      // any of them.
      if (a.status === "waiting" && a.waiting) {
        meta.append(describe(el("span", "agent-waiting", "needs: " + a.waiting), "What this pane is waiting on you for"));
      }
      main.append(meta);
      row.append(main);

      const status = el("span", "agent-status " + a.status, a.status);
      row.append(status);
      if (a.for) row.append(el("span", "agent-for", howLong(a.for)));

      rowAction(row, () => {
        send({ cmd: "revealPane", root: a.root, node: a.tabId, id: a.paneId });
        closeOverlay();
      }, false, a.tab || a.name);
      wrap.append(row);
    });
    body.append(wrap);

    const tools = el("div", "wt-tools");
    const refresh = el("button", "chip", "Refresh");
    refresh.onclick = () => send({ cmd: "agents" });
    tools.append(refresh);
    body.append(tools);
  }

  // ---------------------------------------------------------- agent picker

  /* Which agent runs in a pane is one keystroke away when it is the one this
   * project always runs, and this is the other way round: the whole catalog,
   * grouped by whether the machine has it, each agent opening onto its models.
   * It is worked from the keyboard like the palette — the filter keeps the
   * keyboard throughout and the arrows walk the list under it — because that
   * is how everything else here is worked.
   */

  /** picker holds what is open: what to do with the answer, what has been
   *  typed, which row is picked out, which agents are opened onto their
   *  models, and whether the answer is also to become this project's default. */
  let picker = null;
  /** The rows on screen, in the order the arrows walk them. */
  let pickRows = [];

  function chooseAgent(title, onPick, opts) {
    opts = opts || {};
    // address is the address being typed for one agent, while it is: which
    // agent, what has been typed, why the last try was refused, and whether
    // one is out to be saved. A caller that only wants to set the default --
    // the Projects dialog's own shortcut to it -- starts with the box
    // already ticked, since picking an agent here is the only thing it asked
    // for; ordinarily nobody has said anything yet.
    picker = {
      title, onPick, query: "", index: 0, open: new Set(),
      setDefault: !!opts.setDefault, scope: opts.scope || "", address: null,
    };
    // A fresh picker starts on its first row, not on the row the last one
    // left picked out, which renderAgentPicker would otherwise carry over.
    pickRows = [];
    dialog = "agentPicker";
    // Its ? is the page on agents and models, which is what this chooses
    // between; it opened the page on panes.
    openOverlay(title, "agents");
    // The probe behind `available` is a few seconds old at most, but an agent
    // installed while this window was open is exactly what somebody opening
    // this dialog is about to look for.
    send({ cmd: "refreshAgents" });
    renderAgentPicker();
  }

  /** defaultChoice is what a pane starts with when nobody chooses: this
   *  project's own answer where it has one, and the overall one otherwise. */
  function defaultChoice() {
    const c = catalog || {};
    if (c.project && (c.project.agent || c.project.model)) return c.project;
    return c.default || {};
  }

  /** pickerItems is the catalog as the dialog lists it: the agents that match
   *  what has been typed, in two groups, each opened agent followed by its
   *  models. Group headings are drawn from this too, so an empty group simply
   *  does not appear. */
  function pickerItems() {
    const q = (picker.query || "").trim().toLowerCase();
    const words = q ? q.split(/\s+/) : [];
    const matches = (a) => {
      const hay = (a.id + " " + a.name + " " + (a.address || "") + " " +
        (a.models || []).map((m) => m.id + " " + (m.name || "")).join(" ")).toLowerCase();
      return words.every((w) => hay.includes(w));
    };
    const items = (catalog.items || []).filter(matches);
    const out = [];
    [["Installed", true], ["Not installed", false]].forEach(([title, want]) => {
      const group = items.filter((a) => !!a.available === want);
      if (!group.length) return;
      out.push({ type: "head", title: title });
      group.forEach((a) => {
        out.push({ type: "agent", agent: a });
        if (!picker.open.has(a.id)) return;
        // The address comes first: it is what the models below it are asked
        // for at, and for an endpoint with none it is all there is.
        // A window reached through the relay is not offered it: an address
        // is changed at the desk, as the key that goes to it is.
        if (a.addressable && !remoteWindow) out.push({ type: "address", agent: a });
        (a.models || []).forEach((m) => out.push({ type: "model", agent: a, model: m }));
      });
    });
    return out;
  }

  /** markedAgent and markedModel say which entries carry the "default" mark. */
  function markedAgent(a) {
    const d = defaultChoice();
    return !!d.agent && d.agent === a.id;
  }
  function markedModel(a, m) {
    const d = defaultChoice();
    const want = markedAgent(a) && d.model !== undefined && d.model !== "" ? d.model : (a.defaultModel || "");
    return (m.id || "") === want;
  }

  /** The row picked out when the catalog redraws the picker, for the redraw
   *  to pick out again. The highlight is a place in the list, and the catalog
   *  the picker asks for as it opens can arrive after the arrows have moved
   *  it: an agent added, or moved between installed and not, shifted every
   *  row after it, and the highlight named another agent - which Enter then
   *  started. */
  let pickKeep = null;

  function renderAgentPicker() {
    if (dialog !== "agentPicker" || !picker) return;
    pickKeep = pickRows[picker.index] ? pickRows[picker.index].item : null;
    const body = $("overlay-body");
    body.textContent = "";

    if (catalog.err) body.append(el("div", "pick-warn", catalog.err));

    const field = el("input", "pick-filter");
    field.id = "agent-filter";
    field.type = "text";
    field.autocomplete = "off";
    field.spellcheck = false;
    field.placeholder = "Type to narrow the list…";
    field.value = picker.query;
    field.setAttribute("aria-label", "Which agent");
    field.setAttribute("role", "combobox");
    field.setAttribute("aria-expanded", "true");
    field.setAttribute("aria-autocomplete", "list");
    field.setAttribute("aria-controls", "agent-list");
    field.oninput = () => { picker.query = field.value; picker.index = 0; drawPickerList(); };
    field.onkeydown = pickerKey;
    body.append(field);

    const list = el("div", "pick-list");
    list.id = "agent-list";
    list.setAttribute("role", "listbox");
    list.setAttribute("aria-label", "Agents");
    body.append(list);

    const foot = el("div", "pick-foot");
    const box = el("label", "pick-default");
    const tick = el("input");
    tick.type = "checkbox";
    tick.checked = picker.setDefault;
    tick.onchange = () => { picker.setDefault = tick.checked; };
    box.append(tick, document.createTextNode(" Set as default for"));
    foot.append(describe(box, "Remembers this choice in agents.json, so every pane opened without choosing starts with it."));
    // Which default: this project's, or the one every project without a
    // choice of its own runs. The second could only be set by editing
    // agents.json, and a project's own choice could not be undone at all.
    // Outside the label, which would otherwise pass a click on it to the box.
    const scope = el("select", "fan-agent-sel pick-scope");
    scope.setAttribute("aria-label", "Which default");
    scope.append(agentOption("this project", ""), agentOption("every project", "all"));
    scope.value = picker.scope;
    scope.onchange = () => { picker.scope = scope.value; picker.setDefault = tick.checked = true; };
    foot.append(scope);
    if (catalog.project) {
      const forget = el("button", "chip", "Use the default for every project");
      describe(forget, "Forgets the agent chosen for this project, so it starts whatever every other project does.");
      forget.onclick = () => send({ cmd: "setAgentDefault", agent: "", model: "" });
      foot.append(forget);
    }
    const cancel = el("button", "chip", "Cancel");
    cancel.onclick = closeOverlay;
    foot.append(cancel);
    body.append(foot);

    drawPickerList();
    field.focus();
  }

  /** drawPickerList rebuilds the list under the filter. The field is left
   *  alone, because it has the keyboard and the caret in it. */
  function drawPickerList() {
    const list = $("agent-list");
    if (!list) return;
    // Taken now so that it is used by this drawing and no later one, however
    // this one ends.
    const keep = pickKeep;
    pickKeep = null;
    list.textContent = "";
    pickRows = [];
    const items = pickerItems();
    if (!items.length) {
      list.append(el("div", "dir-empty", "No agent matches that."));
      markPickerRow();
      return;
    }
    items.forEach((item) => {
      if (item.type === "head") {
        list.append(el("div", "pick-head", item.title));
        return;
      }
      const row = item.type === "agent" ? pickerAgentRow(item.agent)
        : item.type === "address" ? pickerAddressRow(item.agent)
        : pickerModelRow(item.agent, item.model);
      row.id = "pick-row-" + pickRows.length;
      row.setAttribute("role", "option");
      const at = pickRows.length;
      row.onmousemove = () => selectPickerRow(at); // as in the palette
      row.onclick = () => { picker.index = at; takePickerRow(); };
      row.item = item;
      pickRows.push(row);
      list.append(row);
      if (item.type === "address" && picker.address && picker.address.id === item.agent.id) {
        list.append(addressForm(item.agent));
      }
    });
    if (keep) {
      const at = pickRows.findIndex((r) => r.item.type === keep.type && r.item.agent.id === keep.agent.id &&
        (keep.type !== "model" || (r.item.model.id || "") === (keep.model.id || "")));
      if (at >= 0) picker.index = at;
    }
    if (picker.index >= pickRows.length) picker.index = Math.max(0, pickRows.length - 1);
    markPickerRow();
  }

  function pickerAgentRow(a) {
    const row = el("div", "pick-row agent" + (a.available ? "" : " unavailable"));
    const open = picker.open.has(a.id);
    row.append(glyph(open ? "▾" : "▸"));
    row.append(el("span", "pick-name", a.name || a.id));
    if (markedAgent(a)) row.append(el("span", "pick-mark", "default"));
    if (!a.available && a.addressable) {
      // An endpoint is not something to install. What it lacks is an
      // address, which this dialog takes, or a key for the one it has.
      row.append(el("span", "pick-note", a.address
        ? "needs a key for " + a.address + (remoteWindow ? " - set one at the desk" : " - set one under API keys…")
        : (remoteWindow ? "needs an address - give it one at the desk" : "needs an address - press Enter to give it one")));
    } else if (!a.available) {
      const why = el("span", "pick-note", a.install || "not installed");
      row.append(describe(why, TIPS.notInstalled));
    } else if (a.addressable && a.address) {
      row.append(el("span", "pick-note", a.address));
    } else if (a.models && a.models.length) {
      row.append(el("span", "pick-note", a.models.length + " models"));
    }
    row.setAttribute("aria-expanded", String(open));
    return row;
  }

  /** opensOnto says whether an agent's row opens onto rows of its own: its
   *  models, and the address of an endpoint. */
  function opensOnto(a) {
    return (a.models || []).length > 0 || (!!a.addressable && !remoteWindow);
  }

  /** pickerAddressRow is where an endpoint's address is changed. It is a row
   *  like the models beside it, so it is reached with the same arrows, and
   *  Enter on it opens the field. */
  function pickerAddressRow(a) {
    const row = el("div", "pick-row model address");
    row.append(el("span", "pick-name", a.address ? "Address: " + a.address : "Give it an address…"));
    row.append(el("span", "pick-note", a.address ? "Enter to change it" : "Enter to type one"));
    return row;
  }

  /** editAddress opens the field for an agent's address, holding what it has
   *  now, and gives it the keyboard. */
  function editAddress(a) {
    picker.open.add(a.id);
    picker.address = { id: a.id, value: a.address || "", error: "", busy: false };
    drawPickerList();
    const at = pickRows.findIndex((r) => r.item.type === "address" && r.item.agent.id === a.id);
    if (at >= 0) { picker.index = at; markPickerRow(); }
    const field = $("agent-address");
    if (field) field.focus();
  }

  /** closeAddress puts the field away and the keyboard back on the filter,
   *  with the highlight where it was. */
  function closeAddress() {
    if (!picker) return;
    picker.address = null;
    drawPickerList();
    const field = $("agent-filter");
    if (field) field.focus();
  }

  /** addressForm is the field an address is typed into. What was typed lives
   *  in picker.address rather than only in the input, so a catalog arriving
   *  while it is being typed redraws the list without losing it. */
  function addressForm(a) {
    const d = picker.address;
    const wrap = el("div", "pick-address");
    const form = el("div", "wt-form");
    const field = el("input");
    field.id = "agent-address";
    field.type = "text";
    field.autocomplete = "off";
    field.spellcheck = false;
    field.placeholder = "http://127.0.0.1:11434/v1";
    field.value = d.value;
    field.setAttribute("aria-label", "Address of " + (a.name || a.id));
    field.oninput = () => { d.value = field.value; };

    const save = () => {
      d.value = field.value;
      const value = field.value.trim();
      // Nothing changed is nothing to write. It also keeps a password masked
      // on the way here, or a variable the file names the address by, from
      // being written back over the real thing.
      if (value === (a.address || "")) { closeAddress(); return; }
      d.error = "";
      d.busy = true;
      send({ cmd: "setAgentAddress", id: a.id, text: value });
      drawPickerList();
      const again = $("agent-address");
      if (again) again.focus();
    };
    // Escape is the window's key handler's, which puts this field away rather
    // than closing the picker; it sees the key before this field does.
    field.onkeydown = (ev) => {
      if (ev.key === "Enter") { ev.preventDefault(); save(); }
    };

    const ok = el("button", "chip primary", d.busy ? "Saving…" : "Save");
    ok.disabled = d.busy;
    ok.onclick = save;
    const no = el("button", "chip", "Cancel");
    no.onclick = closeAddress;
    form.append(field, ok, no);
    wrap.append(form);
    wrap.append(el("div", "wt-hint",
      "Where the model server answers, starting http:// or https:// - Ollama is " +
      "http://127.0.0.1:11434/v1 and LM Studio http://127.0.0.1:1234/v1. One on this " +
      "machine needs no key. Saved empty, the address is taken away."));
    if (d.error) {
      const why = el("div", "pick-error", d.error);
      why.id = "agent-address-error";
      why.setAttribute("role", "alert");
      field.setAttribute("aria-invalid", "true");
      field.setAttribute("aria-describedby", why.id);
      wrap.append(why);
    }
    return wrap;
  }

  /** addressAnswered takes the server's answer to an address: a refusal goes
   *  under the field, beside what was typed, so it can be put right there; a
   *  saved address puts the field away and the highlight on the agent, so
   *  Enter starts it. The catalog that follows says whether it can be. */
  function addressAnswered(msg) {
    const d = picker && picker.address;
    if (dialog !== "agentPicker" || !d || d.id !== msg.id) {
      // The picker was closed or moved on before the answer came. A refusal
      // is still said, where it will be seen; a save is said by its notice.
      if (msg.error) notice(msg.error, true);
      return;
    }
    d.busy = false;
    if (msg.error) {
      d.error = msg.error;
      drawPickerList();
      const field = $("agent-address");
      if (field) field.focus();
      return;
    }
    closeAddress();
    const at = pickRows.findIndex((r) => r.item.type === "agent" && r.item.agent.id === msg.id);
    if (at >= 0) { picker.index = at; markPickerRow(); }
  }

  /** What each tier means, for the chip that names it. */
  const TIER_TIPS = {
    small: "Small: the least capable of this agent's models, and the cheapest. Enough for mechanical work.",
    mid: "Mid: this agent's everyday model.",
    top: "Top: the most capable of this agent's models, and the dearest.",
  };

  /** dollars writes a price per million tokens the way the providers do. */
  function dollars(v) { return "$" + (Number.isInteger(v) ? String(v) : v.toFixed(2)); }

  /** priceText is a model's price as the picker shows it, with the day it was
   *  read: a price is only ever true of the day somebody looked. */
  function priceText(p) {
    return dollars(p.in) + " / " + dollars(p.out) + " per M tokens, checked " + p.checked;
  }

  function pickerModelRow(a, m) {
    const row = el("div", "pick-row model");
    row.append(el("span", "pick-name", m.name || m.id || "Default"));
    if (markedModel(a, m)) row.append(el("span", "pick-mark", "default"));
    if (m.tier) row.append(describe(el("span", "pick-tier", m.tier), TIER_TIPS[m.tier] || m.tier));
    if (m.note) row.append(el("span", "pick-note", m.note));
    if (m.price) {
      row.append(describe(el("span", "pick-note pick-price", priceText(m.price)),
        "Input and output, per million tokens, as the provider published it. What you pay may differ."));
    }
    return row;
  }

  function selectPickerRow(i) {
    if (!picker || i === picker.index || i < 0 || i >= pickRows.length) return;
    picker.index = i;
    markPickerRow();
  }

  function markPickerRow() {
    pickRows.forEach((row, i) => {
      const on = i === picker.index;
      row.classList.toggle("sel", on);
      row.setAttribute("aria-selected", String(on));
    });
    const field = $("agent-filter");
    const sel = pickRows[picker.index];
    if (!field) return;
    // The keyboard never leaves the filter while the list is walked, so it is
    // the filter that has to name the row picked out.
    if (!sel) { field.removeAttribute("aria-activedescendant"); return; }
    field.setAttribute("aria-activedescendant", sel.id);
    sel.scrollIntoView({ block: "nearest" });
  }

  /** takePickerRow acts on the row picked out: an agent with models opens onto
   *  them if it is not open already, and starts with its own default if it is;
   *  a model starts with that model. An agent this machine does not have says
   *  so rather than starting a process that is not there. */
  function takePickerRow() {
    const row = pickRows[picker.index];
    if (!row) return;
    const item = row.item;
    const a = item.agent;
    if (item.type === "address") { editAddress(a); return; }
    if (!a.available && a.addressable && remoteWindow) {
      // The address and the key are both the desk's to give.
      notice(a.name + (a.address ? " needs a key for " + a.address : " needs an address") +
        ", which is given on the machine Flockdeck runs on.", true);
      return;
    }
    if (!a.available && a.addressable) {
      // An endpoint missing only its address is given one here, where the
      // question came up, rather than being sent off to edit a file.
      if (!a.address) { editAddress(a); return; }
      picker.open.add(a.id);
      drawPickerList();
      notice(a.name + " needs a key for " + a.address + ": set one under API keys… in the command palette, or give it another address.", true);
      return;
    }
    if (!a.available) {
      notice(a.name + " is not installed. " + (a.install || ""), true);
      return;
    }
    if (item.type === "agent" && opensOnto(a) && !picker.open.has(a.id)) {
      picker.open.add(a.id);
      drawPickerList();
      return;
    }
    startPicked(a.id, item.type === "model" ? (item.model.id || "") : (a.defaultModel || ""));
  }

  /** startPicked is what the whole dialog is for: hand the choice to whoever
   *  opened it, remembering it first if that was asked for. */
  function startPicked(agentID, model) {
    const run = picker.onPick;
    if (picker.setDefault) {
      const req = { cmd: "setAgentDefault", agent: agentID, model: model };
      if (picker.scope === "all") req.kind = "all";
      send(req);
    }
    closeOverlay();
    run(agentID, model);
  }

  function pickerKey(e) {
    if (!picker) return;
    const to = listStep(e, picker.index, pickRows.length, !picker.query);
    if (to !== null) { e.preventDefault(); selectPickerRow(to); return; }
    const row = pickRows[picker.index];
    const item = row && row.item;
    // Left and right open and close an agent — but only while nothing has been
    // typed. They belong to the text caret the moment there is text to move it
    // through, and taking them from someone correcting a typo in the filter
    // would be worse than making them press Enter to open a row.
    if (!picker.query && item) {
      if (e.key === "ArrowRight" && item.type === "agent" && opensOnto(item.agent)) {
        e.preventDefault();
        picker.open.add(item.agent.id);
        drawPickerList();
        return;
      }
      if (e.key === "ArrowLeft") {
        e.preventDefault();
        const id = item.agent.id;
        picker.open.delete(id);
        drawPickerList();
        // Left on one of its models closes the agent and goes to it, as a
        // tree does. Kept at the same place in the shorter list, the highlight
        // landed on whichever agent had moved up into it - and Enter started
        // that one.
        const parent = pickRows.findIndex((r) => r.item.type === "agent" && r.item.agent.id === id);
        if (parent >= 0) { picker.index = parent; markPickerRow(); }
        return;
      }
    }
    if (e.key === "Enter") { e.preventDefault(); takePickerRow(); }
  }

  // ------------------------------------------------------------------ keys

  let apiKeys = null;
  /** Which agent's key is being typed, and only which. The key itself lives in
   *  the input and nowhere else: parking it in a variable would keep it for the
   *  rest of the session, and losing a half-typed key to a redraw is the
   *  cheaper of the two mistakes. */
  let keyEditing = null;
  /** The agent whose key was last saved, put away or cleared. What the
   *  keyboard was on went with it - the field, its Cancel, the Clear - and
   *  it goes back to that row's Set or Replace button instead. */
  let keyActed = null;
  /** Puts the open key form away, for the window's Escape to call. */
  let keyCancel = null;
  function keySetButton(agent) {
    const b = agent ? $("key-set-" + encodeURIComponent(agent)) : null;
    return b && b.isConnected ? b : null;
  }

  /** openKeys shows which agents have an API key.
   *
   *  It never shows a key. What is worth knowing here is that there is one and
   *  where it came from; the answer to "what is it" is the vendor's own
   *  console, and a key that can be read back out of the interface is one more
   *  place it can be read out of. */
  function openKeys() {
    dialog = "keys";
    apiKeys = null;
    keyEditing = null;
    openOverlay("API keys", "agents"); // where keys are explained
    $("overlay-body").textContent = "";
    $("overlay-body").append(el("div", "dir-empty", "Loading…"));
    send({ cmd: "keys" });
  }

  /** keysHost is where the keys are drawn: their own dialog, or their section
   *  of the settings, or null while neither is on screen. */
  function keysHost() {
    if (dialog === "keys") return $("overlay-body");
    if (dialog === "settings" && settingsSection === "keys") return $("settings-host");
    return null;
  }

  function renderKeys(msg) {
    if (msg) apiKeys = msg;
    const body = keysHost();
    if (!body) return; // see openWorktrees
    const items = (apiKeys && apiKeys.items) || [];
    body.textContent = "";

    body.append(el("div", "fan-hint",
      "Agents that talk to a model API need a key. One already exported in your " +
      "environment is used where it is; anything set here is kept in flockdeck's own " +
      "file, readable only by you, and reaches nothing but the pane that needs it."));
    if (!apiKeys) { body.append(el("div", "dir-empty", "Loading…")); return; }

    if (!items.length) {
      body.append(el("div", "dir-empty", "None of the agents here use an API key."));
      return;
    }

    const wrap = section(items.length === 1 ? "1 agent" : items.length + " agents");
    items.forEach((k) => {
      const row = el("div", "wt-row");
      row.dataset.key = "key:" + k.agent;
      const main = el("div", "wt-main");

      const title = el("div", "wt-title");
      title.append(el("span", "wt-label", k.name || k.agent));
      if (k.name && k.name !== k.agent) title.append(el("span", "wt-flag", k.agent));
      main.append(title);

      const meta = el("div", "wt-meta");
      if (k.set) {
        meta.append(el("span", "wt-clean",
          k.source === "env" ? "set — from " + k.env : "set — stored by flockdeck"));
        // A key stored before the variable was exported is still held here,
        // and Clear below is for it, not for the variable.
        if (k.source === "env" && k.stored) meta.append(el("span", null, "a stored key is kept as well"));
      } else {
        meta.append(el("span", "wt-untracked", "not set"));
        // Somebody who keeps their keys in a shell profile or a secrets
        // manager should be told the names that are looked for rather than
        // being pushed into storing a second copy here.
        if (k.vars && k.vars.length) {
          meta.append(el("span", null, "or export " + k.vars.join(" or ")));
        }
      }
      main.append(meta);
      row.append(main);

      // Keys are the desk's to set and clear: see remoteWindow.
      if (!remoteWindow) {
        const actions = el("div", "wt-actions");
        const set = el("button", "chip", k.set ? "Replace…" : "Set…");
        // An id, because its wording changes when a key is saved.
        set.id = "key-set-" + encodeURIComponent(k.agent);
        set.onclick = () => { keyEditing = k.agent; renderKeys(); };
        actions.append(set);
        // Only a stored key can be forgotten. A key that came from the
        // environment is the user's own arrangement and this dialog has no
        // business unsetting a variable it did not set -- but a stored key that
        // a variable shadows is still Flockdeck's, and can be cleared.
        if (k.stored || (k.set && k.source === "store")) {
          const clear = el("button", "chip danger", "Clear");
          clear.onclick = () => { keyActed = k.agent; send({ cmd: "keyClear", id: k.agent }); };
          actions.append(clear);
        }
        row.append(actions);
      }
      wrap.append(row);

      if (keyEditing === k.agent && !remoteWindow) wrap.append(keyForm(k));
    });
    body.append(wrap);
    if (remoteWindow) {
      body.append(el("div", "fan-hint", "Keys are set and cleared on the machine itself: one typed here " +
        "would pass through the relay, which can read what passes through it."));
    }

    // Whatever was being typed is what the keyboard should be on, and after a
    // save or a clear the button that did it is gone with the redraw.
    const field = body.querySelector("input[type=password]");
    if (field) field.focus();
  }

  /** keyForm is the one place a key is typed. It starts empty every time,
   *  because there is nothing to prefill it from: the front end is never told
   *  what a key is, only that there is one. */
  function keyForm(k) {
    const form = el("div", "wt-form");
    const field = el("input");
    field.type = "password";
    field.placeholder = "Paste the key for " + k.agent;
    field.autocomplete = "off";
    field.spellcheck = false;

    // The form goes, and the keyboard with it unless it is put back: on the
    // row's own button, where the next Enter replaces the key again.
    const done = () => {
      keyActed = k.agent;
      renderKeys();
      const b = keySetButton(k.agent);
      if (b) b.focus();
    };
    const save = () => {
      const value = field.value.trim();
      field.value = "";
      keyEditing = null;
      if (value) send({ cmd: "keySet", id: k.agent, text: value });
      // Drawn again without waiting for the reply, so the field goes as
      // soon as it is sent rather than sitting there emptied.
      done();
    };
    const cancel = () => { field.value = ""; keyEditing = null; done(); };
    keyCancel = cancel;
    field.id = "key-field";

    // Escape closes the whole overlay everywhere else, which here would take
    // a half-typed key with it and leave the user wondering what was saved.
    // It is the window's key handler that puts the form away instead: it
    // sees the key before this field does, and an Escape handled here never
    // arrived - the dialog had already closed.
    field.onkeydown = (ev) => {
      if (ev.key === "Enter") { ev.preventDefault(); save(); }
    };

    const ok = el("button", "chip primary", "Save");
    ok.onclick = save;
    const no = el("button", "chip", "Cancel");
    no.onclick = cancel;
    form.append(field, ok, no);
    return form;
  }

  // --------------------------------------------------------------- settings

  /* One dialog for the settings that were scattered across the palette, the
   * keys and a prompt or two, and for two that had no home at all: the agent
   * every project starts, and the plan. Every control sends the command the
   * palette and the keys send and reads the preferences they change, so there
   * is one state however it is changed, and the dialog follows a change made
   * anywhere else - another window included - while it is open. The remote
   * access and API key sections are those dialogs' own parts, drawn here. */

  const SETTINGS_SECTIONS = [
    { id: "general", label: "General", words: "desktop notifications updates releases check hints tips" },
    { id: "appearance", label: "Appearance",
      words: "theme dark light system follow colour color accent swatch " +
        "font size family typeface scrollback lines cursor blink block bar underline preview screen reader accessibility" },
    { id: "behaviour", label: "Behaviour",
      words: "fan out fanout tab worktree new agents auto review approve approval default defaults" },
    { id: "keybindings", label: "Keybindings", words: "keyboard shortcuts shortcut remap rebind bindings keys chord conflict" },
    { id: "agents", label: "Agents", words: "default agent model project claude status line usage limits spend" },
    { id: "keys", label: "API keys", words: "api keys key token secret" },
    { id: "remote", label: "Remote access", words: "remote relay pair paired device devices machine name phone tablet push notify notification notifications waiting anonymous identifying" },
    { id: "github", label: "GitHub", words: "github gh pull request issue pr ci actions checks workflow sign in install auth login" },
    { id: "plan", label: "Account & plan", words: "account plan free enterprise self-hosted relay sso company companies licence license support paid sponsor sponsors sponsorship donate" },
  ];
  /** The section on show, kept from one opening to the next. */
  let settingsSection = "general";
  /** What has been typed into Find a setting. */
  let settingsQuery = "";
  /** A font typed and refused, kept in the field with the reason under it so
   *  it can be put right rather than typed again. */
  let fontDraft = null;
  let fontError = "";

  /** The action whose binding is being recorded -- the next keydown becomes
   *  its chord instead of running whatever it already does -- or "" while
   *  nothing is. keybindError is what to say about the last attempt, id and
   *  message together so it is only ever shown beside the row it is about. */
  let keybindEditing = "";
  let keybindError = null;

  /** The site's section on Enterprise, where what it costs and when it
   *  comes will be said once it is settled. v0.2.9 linked #private-relays,
   *  which the site keeps landing on the same card. */
  const ENTERPRISE_URL = "https://flockdeck.ai/#enterprise";

  /** The site's privacy policy, terms and licences, which the account's
   *  section links at its foot: each an id, what it reads as, and where. */
  const LEGAL_LINKS = [
    ["set-legal-privacy", "Privacy policy", "https://flockdeck.ai/privacy.html"],
    ["set-legal-terms", "Terms", "https://flockdeck.ai/terms.html"],
    ["set-legal-licences", "Licences", "https://flockdeck.ai/licences.html"],
  ];

  /** Where somebody who wants to give something back can: the GitHub Sponsors
   *  account .github/FUNDING.yml names, which a test keeps this level with. */
  const SPONSOR_URL = "https://github.com/sponsors/jmwri";

  /** A few monospaced fonts to suggest; any other installed one can be typed. */
  const FONT_SUGGESTIONS = ["Cascadia Mono", "Cascadia Code", "JetBrains Mono", "Fira Code", "Consolas",
    "SF Mono", "Menlo", "Source Code Pro", "Ubuntu Mono", "DejaVu Sans Mono"];

  function openSettings(section) {
    dialog = "settings";
    settingsQuery = "";
    openOverlay("Settings", "settings");
    $("overlay-panel").classList.add("settings-panel");
    buildSettingsShell();
    showSettingsSection(section || settingsSection, true);
    // On the section list, so the arrows walk it from the start.
    const tab = $("settings-tab-" + settingsSection);
    if (tab) tab.focus();
  }

  /** settingsChanged draws the section on show again, for a setting that has
   *  changed from here or from anywhere else. Not the keys or remote access,
   *  which draw themselves from their own answers: drawn again for a font
   *  size, the key half-typed into the one would go. */
  function settingsChanged() {
    if (dialog !== "settings") return;
    // Of remote access's section, only the push settings are preferences.
    if (settingsSection === "remote") keepFocus(drawPushSettings);
    else if (settingsSection !== "keys" && settingsSection !== "github") keepFocus(renderSettings);
  }

  function buildSettingsShell() {
    const body = $("overlay-body");
    body.textContent = "";
    const wrap = el("div", "settings");
    const nav = el("div", "settings-nav");
    const find = el("input", "settings-find");
    find.id = "settings-find";
    find.type = "search";
    find.placeholder = "Find a setting";
    find.autocomplete = "off";
    find.spellcheck = false;
    find.setAttribute("aria-label", "Find a setting");
    find.oninput = () => {
      settingsQuery = find.value;
      const hits = matchingSections();
      if (hits.length && !hits.some((s) => s.id === settingsSection)) showSettingsSection(hits[0].id);
      else drawSettingsNav();
    };
    find.onkeydown = (ev) => {
      if (ev.key !== "ArrowDown" && ev.key !== "Enter") return;
      const tab = $("settings-tab-" + settingsSection);
      if (tab && tab.isConnected) { ev.preventDefault(); tab.focus(); }
    };
    const tabs = el("div", "settings-tabs");
    tabs.id = "settings-tabs";
    tabs.setAttribute("role", "tablist");
    tabs.setAttribute("aria-orientation", "vertical");
    tabs.setAttribute("aria-label", "Sections");
    nav.append(find, tabs);
    const pane = el("div", "settings-pane");
    pane.id = "settings-pane";
    pane.setAttribute("role", "tabpanel");
    wrap.append(nav, pane);
    body.append(wrap);
  }

  /** matchingSections is the sections holding every word typed into Find. */
  function matchingSections() {
    const words = settingsQuery.trim().toLowerCase().split(/\s+/).filter(Boolean);
    return SETTINGS_SECTIONS.filter((s) => words.every((w) => (s.label + " " + s.words).toLowerCase().includes(w)));
  }

  function drawSettingsNav() {
    const list = $("settings-tabs");
    if (!list) return;
    const had = list.contains(document.activeElement);
    list.textContent = "";
    const hits = matchingSections();
    if (!hits.length) list.append(el("div", "help-none", "No setting matches that."));
    hits.forEach((s) => {
      const on = s.id === settingsSection;
      const b = el("button", "settings-tab" + (on ? " sel" : ""), s.label);
      b.id = "settings-tab-" + s.id;
      b.setAttribute("role", "tab");
      b.setAttribute("aria-selected", String(on));
      b.setAttribute("aria-controls", "settings-pane");
      b.tabIndex = on ? 0 : -1;
      b.onclick = () => showSettingsSection(s.id);
      b.onkeydown = (ev) => settingsTabKey(ev, s.id);
      list.append(b);
    });
    const tab = $("settings-tab-" + settingsSection);
    if (tab && tab.isConnected) {
      if (had) tab.focus();
      // On a narrow screen the sections are a strip across the top that
      // scrolls, and the one on show could be off its end.
      tab.scrollIntoView({ block: "nearest", inline: "nearest" });
    }
  }

  /** settingsTabKey walks the sections with the arrows, showing each as it is
   *  reached: nothing is lost by passing one, as it is by passing an agent. */
  function settingsTabKey(ev, id) {
    const tabs = [...$("settings-tabs").querySelectorAll("button")];
    const at = tabs.findIndex((b) => b.id === "settings-tab-" + id);
    const to = rowStep(ev.key, at, tabs.length);
    if (to === undefined || to < 0 || !tabs[to]) return;
    ev.preventDefault();
    ev.stopPropagation();
    const next = tabs[to].id.slice("settings-tab-".length);
    showSettingsSection(next);
    $("settings-tab-" + next).focus();
  }

  function showSettingsSection(id, fresh) {
    if (!SETTINGS_SECTIONS.some((s) => s.id === id)) id = "general";
    const moved = !!fresh || id !== settingsSection;
    settingsSection = id;
    drawSettingsNav();
    if (moved) {
      fontDraft = null;
      fontError = "";
      keybindEditing = "";
      keybindError = null;
      // What a section lists is asked for as it is shown, as its own dialog
      // does on opening.
      if (id === "keys") { apiKeys = null; keyEditing = null; send({ cmd: "keys" }); }
      else if (id === "remote") {
        remoteRoster = null; remotePairing = null; remoteOutcome = null; remoteBusy = "";
        send({ cmd: "remoteDevices" });
      } else if (id === "plan") send({ cmd: "remoteDevices" });
      else if (id === "agents") send({ cmd: "refreshAgents" });
      else if (id === "github") { ghStatus = null; send({ cmd: "ghStatus", path: ghDir }); }
    }
    renderSettings();
    const pane = $("settings-pane");
    if (moved && pane) pane.scrollTop = 0;
  }

  function renderSettings() {
    if (dialog !== "settings") return;
    const pane = $("settings-pane");
    if (!pane) return;
    pane.setAttribute("aria-labelledby", "settings-tab-" + settingsSection);
    pane.textContent = "";
    SETTINGS_DRAW[settingsSection](pane);
  }

  /** settingsHost is the box the keys and remote access draw themselves into,
   *  under the section's heading. */
  function settingsHost(pane) {
    const host = el("div", "settings-host");
    host.id = "settings-host";
    pane.append(host);
  }

  const SETTINGS_DRAW = {
    general: settingsGeneral,
    appearance: settingsAppearance,
    behaviour: settingsBehaviour,
    keybindings: settingsKeybindings,
    agents: settingsAgents,
    keys: (pane) => { settingsHead(pane, "API keys"); settingsHost(pane); renderKeys(); },
    remote: (pane) => { settingsHead(pane, "Remote access"); settingsPush(pane); settingsHost(pane); renderRemote(); },
    github: (pane) => {
      settingsHead(pane, "GitHub", "Create and review pull requests and issues, and see CI status, without leaving Flockdeck.");
      settingsHost(pane);
      renderGhSettings();
    },
    plan: settingsPlan,
  };

  /** How long a pane may wait before the paired devices are told, as offered:
   *  seconds, and what that reads as. */
  const PUSH_DELAYS = [[15, "15 seconds"], [30, "30 seconds"], [60, "1 minute"], [120, "2 minutes"], [300, "5 minutes"], [600, "10 minutes"]];

  /** settingsPush is what the paired devices are told of waits, above remote
   *  access's own part of the section, which draws itself from its answers. */
  function settingsPush(pane) {
    const box = el("div", "set-push");
    box.id = "set-push-box";
    pane.append(box);
    drawPushSettings();
  }

  function drawPushSettings() {
    const box = $("set-push-box");
    if (!box) return;
    box.textContent = "";
    const push = prefs.push || {};
    const r = state && state.remote;
    const machine = (r && r.name) || "this machine";
    // The relay's refusal, in its words: a relay that sends none, or a plan
    // that does not include them. The app decides nothing about either.
    const refused = r && r.pushError ? el("div", "set-desc", "The last one was not sent: " + r.pushError) : null;
    box.append(settingRow("Notify paired devices",
      "When an agent has been waiting on you for a while, the relay tells each phone or browser paired with this " +
      "machine that asked with its Notify me button — whether or not it is open there.",
      switchControl("set-push", !push.off, (on) => setPushOff(!on)), refused));

    const delay = el("select", "set-select");
    delay.id = "set-push-delay";
    const current = push.delaySeconds || 30;
    const delays = PUSH_DELAYS.slice();
    if (!delays.some(([s]) => s === current)) delays.push([current, current + " seconds"]);
    delays.sort((a, b) => a[0] - b[0]).forEach(([s, label]) => delay.append(agentOption(label, String(s))));
    delay.value = String(current);
    delay.disabled = !!push.off;
    delay.onchange = () => setPushDelay(Number(delay.value));
    box.append(settingRow("After waiting", "How long an agent has to have been waiting before the devices are told. Each wait is told once.", delay));

    box.append(settingRow("Send nothing identifying",
      "Notifications say only “An agent on " + machine + " needs you”, rather than naming the pane and its project: " +
      "for a lock screen others can see. Either way it is encrypted here for each device, and neither the relay nor the push service can read it.",
      switchControl("set-push-anonymous", !!push.anonymous, (on) => setPushAnonymous(on))));
  }

  /** The three push settings take effect here at once, as setOff's do, and
   *  are sent to be kept. */
  function setPushOff(off) {
    prefs.push = Object.assign({}, prefs.push, { off });
    send({ cmd: "pushNotify", kind: off ? "off" : "on" });
    notice(off ? "Paired devices will not be notified" : "Paired devices will be notified when an agent waits", false);
    settingsChanged();
  }

  function setPushAnonymous(on) {
    prefs.push = Object.assign({}, prefs.push, { anonymous: on });
    send({ cmd: "pushAnonymous", kind: on ? "on" : "off" });
    notice(on ? "Notifications will not name the pane" : "Notifications will name the pane and its project", false);
    settingsChanged();
  }

  function setPushDelay(seconds) {
    prefs.push = Object.assign({}, prefs.push, { delaySeconds: seconds === 30 ? 0 : seconds });
    send({ cmd: "pushDelay", size: seconds });
    const label = (PUSH_DELAYS.find(([s]) => s === seconds) || [0, seconds + " seconds"])[1];
    notice("Paired devices are told after " + label + " of waiting", false);
    settingsChanged();
  }

  function settingsHead(pane, title, lede) {
    pane.append(el("h3", "set-title", title));
    if (lede) pane.append(el("p", "set-lede", lede));
  }

  /** settingRow is one setting: what it is and what it does, and the control
   *  that changes it, named by the first and described by the second. */
  function settingRow(label, desc, control, extra) {
    const row = el("div", "set-row");
    const text = el("div", "set-text");
    text.append(el("div", "set-label", label));
    if (desc) {
      const d = el("div", "set-desc", desc);
      if (control.id) {
        d.id = control.id + "-desc";
        control.setAttribute("aria-describedby", d.id);
      }
      text.append(d);
    }
    if (extra) text.append(extra);
    if (!control.getAttribute("aria-label")) control.setAttribute("aria-label", label);
    const box = el("div", "set-control");
    box.append(control);
    row.append(text, box);
    return row;
  }

  /** switchControl is an on/off setting, pressed to change it. */
  function switchControl(id, on, change) {
    const b = el("button", "switch" + (on ? " on" : ""));
    b.id = id;
    b.setAttribute("role", "switch");
    b.setAttribute("aria-checked", String(on));
    b.append(el("span", "knob"));
    b.onclick = () => change(!on);
    return b;
  }

  /** keysFor is the binding the action table gives an action, or "". */
  function keysFor(id) {
    const k = keyTable.find((x) => x.id === id);
    return k && k.keys ? k.keys : "";
  }

  /** notificationsNote says why notifications may not come although they are
   *  on here: the browser has a say too. */
  function notificationsNote() {
    if (!("Notification" in window)) return " This browser does not offer them.";
    if (Notification.permission === "denied") {
      return " The browser has blocked them for this window: allow them in its site settings for this address.";
    }
    return "";
  }

  function settingsGeneral(pane) {
    settingsHead(pane, "General", "How Flockdeck tells you things, and how it keeps itself up to date.");
    pane.append(settingRow("Desktop notifications",
      "A notification when an agent stops to wait on you while this window is behind another." + notificationsNote(),
      switchControl("set-notifications", !prefs.notificationsOff, (on) => setOff("notificationsOff", !on))));

    // Neither is offered in a window reached through the relay: see
    // remoteWindow. Checking is the desk's to do, as restarting onto what it
    // finds is.
    const u = !remoteWindow && state && state.update;
    let extra = null;
    if (!remoteWindow) {
      extra = el("div", "update-actions");
      // The switch above only ever governed looking in the background; a
      // person pressing a button marked "Check for updates now" is asking
      // once, this moment, not turning that back on.
      const check = el("button", "chip", "Check for updates now");
      check.id = "set-check-update";
      check.onclick = () => send({ cmd: "checkForUpdate" });
      extra.append(check);
      if (u) {
        const install = el("button", "chip primary", "Install " + u.version + "…");
        install.id = "set-install";
        install.onclick = openUpdate;
        extra.append(install);
      }
    }
    pane.append(settingRow("Check for updates",
      "Looks for new releases in the background and downloads them, to go in when you choose to restart. " +
      "FLOCKDECK_UPDATE=off in the environment stops it whatever this says.",
      switchControl("set-updates", !prefs.updatesOff, (on) => setOff("updatesOff", !on)), extra));

    const dismissed = (prefs.dismissedTips || []).length;
    const tips = el("button", "chip", "Show them again");
    tips.id = "set-tips";
    tips.disabled = !dismissed;
    tips.onclick = () => {
      prefs.dismissedTips = [];
      send({ cmd: "resetTips" });
      notice("The tips will show again where they apply", false);
      renderHints();
      settingsChanged();
    };
    pane.append(settingRow("Hints",
      "One line under the tab bar for a gesture the window cannot show, each sent away for good with its ×. " +
      (dismissed ? (dismissed === 1 ? "One has" : dismissed + " have") + " been sent away." : "None has been sent away."),
      tips));
  }

  /** The theme choices offered: value, label. "" is Dark, the palette this
   *  window has always drawn in and still draws in until one of the others is
   *  chosen. */
  const THEMES = [["", "Dark"], ["light", "Light"], ["system", "Follow system"]];

  /** The fixed accent swatches: value, what a screen reader calls it. Not a
   *  free colour picker -- a colour picked by hand could go illegible against
   *  either palette, and these are chosen to keep contrast on both. Shown in
   *  the colour app.css gives them for the dark palette, which is close
   *  enough to place them by; the page itself redraws in whichever palette is
   *  actually on show. */
  const ACCENTS = [
    ["", "Blue (default)", "#4c9aff"],
    ["purple", "Purple", "#b48cf0"],
    ["green", "Green", "#4fd17a"],
    ["orange", "Orange", "#f0a552"],
    ["pink", "Pink", "#f28fc0"],
    ["teal", "Teal", "#4fd0c6"],
  ];

  /** applyTheme puts the chosen palette and accent on the document, which is
   *  what app.css's [data-theme] and [data-accent] rules key off. It runs
   *  before the first pane is drawn, and again whenever the preference
   *  changes from here or from another window. */
  function applyTheme() {
    const root = document.documentElement;
    if (prefs.theme === "light") root.dataset.theme = "light";
    else if (prefs.theme === "system") delete root.dataset.theme;
    else root.dataset.theme = "dark";
    if (prefs.accentColor) root.dataset.accent = prefs.accentColor;
    else delete root.dataset.accent;
  }

  function settingsAppearance(pane) {
    settingsHead(pane, "Appearance", "How the window looks: its palette, its accent, and every pane's terminal.");

    const seg = el("div", "set-theme");
    seg.id = "set-theme";
    seg.setAttribute("role", "radiogroup");
    seg.setAttribute("aria-label", "Theme");
    THEMES.forEach(([value, label], i) => {
      const on = (prefs.theme || "") === value;
      const b = el("button", "seg" + (on ? " on" : ""), label);
      b.id = "set-theme-" + (value || "dark");
      b.setAttribute("role", "radio");
      b.setAttribute("aria-checked", String(on));
      b.tabIndex = on ? 0 : -1;
      b.onclick = () => chooseTheme(value);
      b.onkeydown = (ev) => {
        const by = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[ev.key];
        if (!by) return;
        ev.preventDefault();
        ev.stopPropagation();
        const next = THEMES[(i + by + THEMES.length) % THEMES.length][0];
        chooseTheme(next);
        $("set-theme-" + (next || "dark")).focus();
      };
      seg.append(b);
    });
    pane.append(settingRow("Theme",
      "Follow system matches this computer's own light or dark setting, and changes with it.", seg));

    const sw = el("div", "swatches");
    sw.id = "set-accent";
    sw.setAttribute("role", "radiogroup");
    sw.setAttribute("aria-label", "Accent colour");
    ACCENTS.forEach(([value, label, colour], i) => {
      const on = (prefs.accentColor || "") === value;
      const b = el("button", "swatch" + (on ? " on" : ""));
      b.id = "set-accent-" + (value || "blue");
      b.style.background = colour;
      b.setAttribute("role", "radio");
      b.setAttribute("aria-checked", String(on));
      b.setAttribute("aria-label", label);
      b.tabIndex = on ? 0 : -1;
      b.onclick = () => chooseAccent(value);
      b.onkeydown = (ev) => {
        const by = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[ev.key];
        if (!by) return;
        ev.preventDefault();
        ev.stopPropagation();
        const next = ACCENTS[(i + by + ACCENTS.length) % ACCENTS.length][0];
        chooseAccent(next);
        $("set-accent-" + (next || "blue")).focus();
      };
      sw.append(b);
    });
    pane.append(settingRow("Accent colour", "A small fixed set, kept legible against either palette.", sw));

    pane.append(el("div", "set-sub", "Terminal"));
    appendTerminalSettings(pane);
  }

  function chooseTheme(value) {
    prefs.theme = value;
    sentPref("theme");
    applyTheme();
    send({ cmd: "theme", text: value || "dark" });
    notice("Theme: " + (THEMES.find(([v]) => v === value) || ["", "Dark"])[1], false);
    settingsChanged();
  }

  function chooseAccent(value) {
    prefs.accentColor = value;
    sentPref("accentColor");
    applyTheme();
    send({ cmd: "accentColor", text: value });
    notice("Accent: " + (ACCENTS.find(([v]) => v === value) || ACCENTS[0])[1], false);
    settingsChanged();
  }

  function appendTerminalSettings(pane) {
    const step = el("div", "stepper");
    step.setAttribute("role", "group");
    const down = el("button", "step-btn");
    down.id = "set-font-down";
    down.setAttribute("aria-label", "Smaller");
    down.append(iconEl("minus", 14));
    down.disabled = fontSize <= 8;
    down.onclick = () => runAction("fontDown");
    const size = el("output", "step-value", fontSize + " px");
    size.id = "set-font-size";
    const up = el("button", "step-btn");
    up.id = "set-font-up";
    up.setAttribute("aria-label", "Larger");
    up.append(iconEl("plus", 14));
    up.disabled = fontSize >= 28;
    up.onclick = () => runAction("fontUp");
    step.append(down, size, up);
    const [kUp, kDown, kReset] = ["fontUp", "fontDown", "fontReset"].map(keysFor);
    pane.append(settingRow("Font size",
      kUp && kDown && kReset ? "Also " + kUp + " and " + kDown + ", with " + kReset + " to go back." : "", step));

    const font = el("input", "set-input");
    font.id = "set-font-family";
    font.type = "text";
    font.placeholder = "Default";
    font.autocomplete = "off";
    font.spellcheck = false;
    font.value = fontDraft !== null ? fontDraft : (prefs.fontFamily || "");
    font.setAttribute("list", "set-fonts");
    font.oninput = () => { fontDraft = font.value; };
    const apply = () => {
      // Enter and leaving the field both apply it, and both happen.
      if (font.value.trim() === (prefs.fontFamily || "") && !fontError) { fontDraft = null; return; }
      fontError = chooseFontFamily(font.value);
      fontDraft = fontError ? font.value : null;
      settingsChanged();
    };
    font.onchange = apply;
    font.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); apply(); } };
    let why = null;
    if (fontError) {
      why = el("div", "set-error", fontError);
      why.setAttribute("role", "alert");
      font.setAttribute("aria-invalid", "true");
    }
    const fonts = el("datalist");
    fonts.id = "set-fonts";
    FONT_SUGGESTIONS.forEach((f) => { const o = el("option"); o.value = f; fonts.append(o); });
    pane.append(settingRow("Font", "Any monospaced font on this computer, by its name. Empty is the default.", font, why), fonts);

    const lines = el("select", "set-select");
    lines.id = "set-scrollback";
    const counts = [1000, 5000, 10000, 25000, 50000, 100000, 200000];
    if (!counts.includes(scrollback)) counts.push(scrollback);
    counts.sort((a, b) => a - b).forEach((n) => lines.append(agentOption(n.toLocaleString("en") + " lines", String(n))));
    lines.value = String(scrollback);
    lines.onchange = () => chooseScrollback(Number(lines.value));
    pane.append(settingRow("Scrollback", "Lines each pane keeps to scroll back through. More costs memory in every pane.", lines));

    const shape = cursorShape();
    const shapes = [["block", "Block"], ["bar", "Bar"], ["underline", "Underline"]];
    const seg = el("div", "segmented");
    seg.id = "set-cursor";
    seg.setAttribute("role", "radiogroup");
    shapes.forEach(([value, label], i) => {
      const b = el("button", "seg" + (value === shape ? " on" : ""), label);
      b.id = "set-cursor-" + value;
      b.setAttribute("role", "radio");
      b.setAttribute("aria-checked", String(value === shape));
      b.tabIndex = value === shape ? 0 : -1;
      b.onclick = () => chooseCursorStyle(value);
      // A group of radio buttons is one stop, and the arrows choose within it.
      b.onkeydown = (ev) => {
        const by = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[ev.key];
        if (!by) return;
        ev.preventDefault();
        ev.stopPropagation();
        const next = shapes[(i + by + shapes.length) % shapes.length][0];
        chooseCursorStyle(next);
        const n = $("set-cursor-" + next);
        if (n) n.focus();
      };
      seg.append(b);
    });
    pane.append(settingRow("Cursor", "The shape of the cursor in every terminal.", seg));
    pane.append(settingRow("Blinking cursor",
      reducedMotion() ? "Your system asks for less motion, so the cursors stay still whatever this says." : "",
      switchControl("set-cursor-blink", !prefs.cursorSteady, (on) => setOff("cursorSteady", !on))));
    pane.append(settingRow("Screen reader support",
      "Lets a screen reader read what the agents write. Each terminal keeps a copy of its lines for it, which slows them a little.",
      switchControl("set-screen-reader", !!prefs.screenReader, (on) => setScreenReader(on))));

    // Drawn in the terminals' own font and size, with a cursor of the chosen
    // shape, so a change is seen here before a pane is looked at.
    pane.append(el("div", "set-sub", "Preview"));
    const pv = el("div", "term-preview");
    pv.id = "set-preview";
    pv.setAttribute("aria-hidden", "true");
    pv.style.fontFamily = terminalFont();
    pv.style.fontSize = fontSize + "px";
    const prompt = () => el("span", "dim", "~/project $ ");
    pv.append(prompt(), document.createTextNode("go test ./...\nok   example.com/project   4.102s\n"), prompt(),
      el("span", "pv-cursor " + shape + (cursorBlinks() ? " blink" : "")));
    pane.append(pv);
  }

  function settingsBehaviour(pane) {
    settingsHead(pane, "Behaviour", "Defaults new panes and new fan-outs start from. Each can still be changed for one pane or one run.");

    const fanOut = prefs.fanOut || {};
    const sameTab = el("input");
    sameTab.type = "checkbox";
    sameTab.id = "set-fanout-sametab";
    sameTab.checked = !!fanOut.sameTab;
    sameTab.onchange = () => {
      prefs.fanOut = Object.assign({}, prefs.fanOut, { sameTab: sameTab.checked });
      send({ cmd: "fanOutSameTab", kind: sameTab.checked ? "on" : "off" });
      notice(sameTab.checked ? "Fanned-out agents will start beside the pane that planned them"
        : "Fanned-out agents will start in a new tab of their own", false);
      settingsChanged();
    };
    const sameTabLabel = el("label", "fan-opt");
    sameTabLabel.append(sameTab, document.createTextNode(" Put fanned-out agents in this tab, beside the agent that planned them"));
    pane.append(settingRow("Fan out",
      "Off, a fan-out's agents land in a new tab of their own. This is only the default the dialog opens on; " +
      "either way it can still be changed for one run.", sameTabLabel));

    pane.append(el("div", "set-sub", "Auto-review"));
    pane.append(settingRow("Start new panes with auto-review on",
      "A pane's own auto-review switch still starts off, whatever this says, unless this is turned on: only a " +
      "confidently read-only command is ever let through without asking, and it is never asked to say “deny.” " +
      "A pane opened by hand starts here; one fanned out from another agent starts however plan 3's inheritance " +
      "lands, once that exists.",
      switchControl("set-auto-review-default", !!prefs.autoReviewDefault, (on) => setAutoReviewDefault(on))));
  }

  function setAutoReviewDefault(on) {
    prefs.autoReviewDefault = on;
    sentPref("autoReviewDefault");
    send({ cmd: "autoReviewDefault", kind: on ? "on" : "off" });
    notice(on ? "New panes start with auto-review on" : "New panes start with auto-review off", false);
    settingsChanged();
  }

  // ------------------------------------------------------------ keybindings

  /** riskyBareKey reports whether a chord with no Ctrl and no Alt would still
   *  reach the focused agent's terminal instead of this window -- true for a
   *  bare letter, digit or punctuation key, and for Shift alone with one of
   *  them, which only types the shifted character. A bare function key, or
   *  Shift with one, is not: F1 and Shift+F6 are built-in bindings today, and
   *  no terminal program claims a function key. */
  function riskyBareKey(key, ctrl, alt) {
    if (ctrl || alt) return false;
    if (/^F([1-9]|1[0-9])$/.test(key)) return false;
    if (key === "Esc" || key === "Tab") return false;
    return true;
  }

  /** chordFromKeydown turns a keydown into the chord it would be written as,
   *  or null for one recording has nothing to do with -- a modifier pressed
   *  alone, waiting for the key that goes with it. */
  function chordFromKeydown(e) {
    if (["Control", "Shift", "Alt", "Meta", "OS"].includes(e.key)) return null;
    let key = e.key;
    let shift = e.shiftKey;
    // Ctrl+= and Ctrl++ are the same gesture on most layouts; matching
    // actionFor's own normalisation keeps a recorded chord able to fire.
    if (key === "+") { key = "="; shift = false; }
    const named = { ArrowLeft: "←", ArrowRight: "→", ArrowUp: "↑", ArrowDown: "↓", " ": "Space", Escape: "Esc" };
    if (named[key]) key = named[key];
    else if (key.length === 1) key = key.toUpperCase();
    const parts = [];
    if (e.ctrlKey) parts.push("Ctrl");
    if (shift) parts.push("Shift");
    if (e.altKey) parts.push("Alt");
    parts.push(key);
    return parts.join("+");
  }

  /** startKeybindCapture puts one action's row into "Press a key…", where the
   *  next keydown this window sees becomes its chord instead of running
   *  whatever it already does; see the keydown listener's own check of
   *  keybindEditing. */
  function startKeybindCapture(id) {
    keybindEditing = id;
    keybindError = null;
    // keepFocus, inside settingsChanged, finds this same button again by its
    // id once the redraw has it reading "Press a key…" and keeps it focused.
    settingsChanged();
  }

  function cancelKeybindCapture() {
    if (!keybindEditing) return;
    keybindEditing = "";
    settingsChanged();
  }

  /** finishKeybindCapture is what a recorded keydown, Backspace/Delete (which
   *  clears the binding) or a conflict local to this window comes to: the
   *  capture ends, and a chord that got this far is sent to be kept. A local
   *  conflict — this window's own key table already gives it to another
   *  action — is caught before the round trip a server refusal would cost,
   *  though the server is still asked to check again, since another window
   *  could have remapped the very thing in between. */
  function finishKeybindCapture(id, chord) {
    keybindEditing = "";
    if (chord) {
      const other = bindings.get(signatureOf(chord));
      if (other && other !== id) {
        const k = keyTable.find((x) => x.id === other);
        keybindError = { id, message: chord + " is already " + (k ? k.label : other) };
        settingsChanged();
        return;
      }
    }
    keybindError = null;
    send({ cmd: "setKeybinding", id, text: chord });
    settingsChanged();
  }

  /** keybindCaptureKey is every keydown this window sees while keybindEditing
   *  names an action, in place of the ordinary dispatch below: nothing else
   *  should run while a chord is being recorded, least of all the very
   *  binding being replaced. Tab is not handled here at all -- see the
   *  listener's own check, which cancels the capture and lets Tab go on
   *  moving the keyboard inside the dialog as it always does. */
  function keybindCaptureKey(e) {
    e.preventDefault();
    e.stopPropagation();
    if (e.key === "Escape") { cancelKeybindCapture(); return; }
    if (e.key === "Backspace" || e.key === "Delete") { finishKeybindCapture(keybindEditing, ""); return; }
    const chord = chordFromKeydown(e);
    if (chord === null) return; // a modifier alone; keep waiting for the key that goes with it
    const key = chord.includes("+") ? chord.slice(chord.lastIndexOf("+") + 1) : chord;
    if (riskyBareKey(key, e.ctrlKey, e.altKey)) {
      keybindError = { id: keybindEditing, message: chord + " would still reach the agent's terminal; add Ctrl or Alt, or use a function key." };
      keybindEditing = "";
      settingsChanged();
      return;
    }
    finishKeybindCapture(keybindEditing, chord);
  }

  function settingsKeybindings(pane) {
    settingsHead(pane, "Keybindings",
      "Shortcuts for this window's own chrome — panes, tabs, agents, the rest. Your terminal tool keeps its own " +
      "shortcuts separately, for what you type inside a pane; this does not touch those.");

    const resetAll = el("button", "chip", "Reset every shortcut to its default");
    resetAll.id = "set-keybind-reset-all";
    resetAll.disabled = !keyTable.some((k) => k.overridden);
    resetAll.onclick = () => {
      if (!window.confirm("Put every shortcut in this window back to its default?")) return;
      send({ cmd: "resetKeybindings" });
    };
    pane.append(settingRow("Every shortcut", "Undoes every remapping made here, for every action at once.", resetAll));

    let section = "";
    keyTable.forEach((k) => {
      if (k.section !== section) {
        section = k.section;
        pane.append(el("div", "set-sub", section));
      }
      pane.append(keybindRow(k));
    });
  }

  /** keybindRow draws one action: its label, its binding — a button that
   *  starts recording a new one when pressed — and a reset that only does
   *  anything once it has been remapped. selectTab's Alt+1 … Alt+9 is a range,
   *  not a chord, and is shown plainly rather than offered to edit; see
   *  keybindings.SetBinding, which refuses it for the same reason. */
  function keybindRow(k) {
    const row = el("div", "keybind-row");
    row.append(el("div", "keybind-label", k.label));
    const controls = el("div", "keybind-controls");
    const range = k.keys && k.keys.includes("…");
    if (range) {
      controls.append(el("span", "keybind-chord empty", k.keys));
    } else {
      const recording = keybindEditing === k.id;
      const btn = el("button", "keybind-chord" + (k.keys ? "" : " empty") + (k.overridden ? " changed" : "") + (recording ? " recording" : ""),
        recording ? "Press a key…" : (k.keys || "No binding"));
      btn.id = "set-keybind-" + k.id;
      btn.setAttribute("aria-label", k.label + (k.keys ? ", " + k.keys : ", no binding") + " — press to change");
      btn.onclick = () => startKeybindCapture(k.id);
      controls.append(btn);
      const reset = el("button", "keybind-reset", "Reset");
      reset.id = "set-keybind-reset-" + k.id;
      reset.setAttribute("aria-label", "Reset " + k.label + " to its default");
      reset.disabled = !k.overridden;
      reset.onclick = () => send({ cmd: "resetKeybinding", id: k.id });
      controls.append(reset);
    }
    row.append(controls);
    if (keybindError && keybindError.id === k.id) {
      const err = el("div", "keybind-error", keybindError.message);
      err.setAttribute("role", "alert");
      row.append(err);
    }
    return row;
  }

  function settingsAgents(pane) {
    const choose = keyTable.find((x) => x.id === "newAgentTabChoose");
    settingsHead(pane, "Agents", "The agent, and which of its models, a pane starts with when nobody chooses." +
      (choose ? " To choose for one pane, use " + choose.label + " in the command palette." : ""));
    // In the shape the agent control is drawn from, which is the fan-out's.
    const items = (catalog.items || []).map((a) => ({ id: a.id, name: a.name || a.id, unavailable: !a.available,
      install: a.install, models: a.models, default: a.defaultModel }));
    if (!items.length) {
      pane.append(el("div", "dir-empty", catalog.err || "Reading the agents…"));
      return;
    }
    const d = catalog.default || {};
    const all = agentSelect(items, (d.agent || "") + "\n" + (d.model || ""));
    all.className = "set-select";
    all.id = "set-agent-all";
    all.onchange = () => {
      const [agent, model] = pickParts(all.value);
      if (agent) send({ cmd: "setAgentDefault", agent, model, kind: "all" });
    };
    pane.append(settingRow("Every project", "What a project with no choice of its own starts. Kept in agents.json.", all));

    const active = ((state && state.projects) || []).find((p) => p.active);
    const own = catalog.project && (catalog.project.agent || catalog.project.model) ? catalog.project : null;
    const mine = agentSelect(items, own ? own.agent + "\n" + (own.model || "") : "", "Same as every project");
    mine.className = "set-select";
    mine.id = "set-agent-project";
    mine.disabled = !active;
    // Empty is the project giving up its own choice, which the server takes
    // as going back to what every project starts.
    mine.onchange = () => {
      const [agent, model] = pickParts(mine.value);
      send({ cmd: "setAgentDefault", agent, model });
    };
    pane.append(settingRow(active ? "This project, " + active.name : "This project",
      own ? "This project's own choice, which wins over the one for every project."
        : "Starts what every project does until it is given an agent of its own.", mine));
    settingsRouting(pane, active);

    const keys = el("button", "chip", "API keys…");
    keys.id = "set-agent-keys";
    keys.onclick = () => {
      showSettingsSection("keys");
      const tab = $("settings-tab-keys");
      if (tab) tab.focus();
    };
    pane.append(settingRow("API keys", "An agent that talks to a model API needs a key for it.", keys));

    // Claude Code hands its usage limits only to its status line command, so
    // reading them means Flockdeck's panes putting a command of their own
    // there, which runs yours. Where you have none, any status line costs the
    // footer's keyboard hints: hence the default, and the choice.
    const sl = el("select", "set-select");
    sl.id = "set-statusline";
    [["auto", "Only where I have a status line"], ["on", "Always"], ["off", "Never"]].forEach(([value, label]) => {
      const o = el("option", null, label);
      o.value = value;
      sl.append(o);
    });
    sl.value = (prefs.spend && prefs.spend.statusLine) || "auto";
    sl.onchange = () => {
      prefs.spend = Object.assign({}, prefs.spend, { statusLine: sl.value === "auto" ? "" : sl.value });
      send({ cmd: "statusLine", text: sl.value });
      notice("Claude panes started from now on will use this; restart a pane to apply it there", false);
    };
    pane.append(settingRow("Claude Code's usage limits",
      "Shows a Claude subscription's five-hour and weekly limits in the pane header, read from Claude Code's status line. " +
      "Your own status line is kept and runs as before. With none of your own, Claude Code hides its footer's keyboard hints " +
      "while any status line is set, so by default the limits are read only where you already have one.", sl));
  }

  /** settingsRouting is the Routing group of Settings › Agents: whether a
   *  fan-out's rows come pre-set to a model chosen for the work, for every
   *  project and for this one; the smallest tier that may be chosen; the
   *  rules, to read; and the history kept of it. */
  function settingsRouting(pane, active) {
    const r = catalog.routing;
    if (!r) return;
    pane.append(el("div", "set-sub", "Routing"));
    const MODES = [["off", "Off"], ["suggest", "Suggest"], ["auto", "Automatic"]];
    const select = (id, options, value) => {
      const sel = el("select", "set-select");
      sel.id = id;
      options.forEach(([v, label]) => sel.append(agentOption(label, v)));
      sel.value = value;
      return sel;
    };

    const every = select("set-route-all", MODES, (r.every && r.every.mode) || "off");
    every.onchange = () => send({ cmd: "setRouting", kind: "all", target: "mode", text: every.value });
    pane.append(settingRow("Every project",
      "Pre-sets a fan-out's rows to a smaller model for mechanical work and a stronger one for hard work, " +
      "marked, for you to change before anything starts. Off until you turn it on. Kept in agents.json.", every));

    const own = r.project || null;
    const mine = select("set-route-project", [["", "Same as every project"]].concat(MODES), own ? own.mode : "");
    mine.disabled = !active;
    mine.onchange = () => send({ cmd: "setRouting", target: "mode", text: mine.value });
    pane.append(settingRow(active ? "This project, " + active.name : "This project",
      own ? "This project's own policy, which replaces the one for every project."
        : "Routed as every project is until it is given a policy of its own.", mine));

    // The floor of whichever policy this project is routed by.
    const policy = own || r.every || {};
    const floor = select("set-route-floor", [["", "Small"], ["mid", "Mid"], ["top", "Top"]],
      policy.floor === "small" ? "" : (policy.floor || ""));
    floor.onchange = () => {
      const req = { cmd: "setRouting", target: "floor", text: floor.value };
      if (!own) req.kind = "all";
      send(req);
    };
    pane.append(settingRow("Never go below",
      "The smallest models routing may choose, " + (own ? "for this project." : "for every project.") +
      " Mid keeps the smallest models off work that matters.", floor));

    const strategy = select("set-route-strategy",
      [["", "Cost-first, quality-aware"], ["cost", "Minimise cost"]],
      policy.strategy === "cost" ? "cost" : "");
    strategy.onchange = () => {
      const req = { cmd: "setRouting", target: "strategy", text: strategy.value };
      if (!own) req.kind = "all";
      send(req);
    };
    pane.append(settingRow("Strategy",
      "Minimise cost also routes work no rule recognises to the cheapest available model; " +
      "quality-aware leaves unrecognised work where it was. Either way a rule that matches still decides first, " +
      "and “Never go below” still holds.", strategy));

    const cross = el("input");
    cross.type = "checkbox";
    cross.id = "set-route-cross";
    cross.checked = !!r.crossAgent;
    cross.onchange = () => {
      const req = { cmd: "setRouting", target: "crossAgent", text: cross.checked ? "true" : "false" };
      if (!own) req.kind = "all";
      send(req);
    };
    const crossLabel = el("label", "fan-opt");
    crossLabel.append(cross, document.createTextNode(" Let a rule send work to another agent"));
    pane.append(settingRow("Routing across agents",
      "A rule may name another agent's model, not only a smaller or stronger model of this one — a local model " +
      "through an OpenAI-compatible endpoint, say. That pane runs a different program, with its own login, tools " +
      "and transcript. Off until you turn it on, " + (own ? "for this project." : "for every project."), crossLabel));
    if (r.note) {
      const note = el("p", "set-lede", r.note);
      note.id = "set-route-note";
      pane.append(note);
    }

    const list = el("div", "route-rules");
    list.id = "set-route-rules";
    (r.rules || []).forEach((rule) => {
      const row = el("div", "route-rule");
      row.append(el("span", "route-rule-name", rule.name), el("span", "pick-tier", rule.choice),
        el("span", "route-rule-when", "when " + rule.when));
      // The log's own account of the rule, where it has decided at least
      // once: the evidence for hand-editing a rule that is overridden a lot,
      // read back rather than left for someone to count lines themselves.
      const total = (rule.kept || 0) + (rule.overridden || 0);
      if (total > 0) {
        const pct = Math.round((100 * (rule.overridden || 0)) / total);
        row.append(el("span", "route-rule-stat", "overridden " + pct + "% of the time (" + (rule.overridden || 0) + " of " + total + ")"));
      }
      list.append(row);
    });
    if (!(r.rules || []).length) list.append(el("div", "set-desc", "No rules, so routing changes nothing."));
    pane.append(settingRow("Rules",
      (r.builtIn ? "The built-in rules. " : "") + "The first to match a task decides. They are edited in agents.json" +
      (r.config ? ", at " + r.config : "") + ".", list));

    const clear = el("button", "chip", "Clear routing history");
    clear.id = "set-route-clear-log";
    clear.onclick = () => send({ cmd: "clearRoutingLog" });
    pane.append(settingRow("Routing history",
      "What routing chose and whether you kept it, for your own numbers. It is kept on this machine, " +
      "holds no task text, and is never sent anywhere.", clear));
  }

  function settingsPlan(pane) {
    settingsHead(pane, "Account & plan", "What you are on, and what is coming.");
    const card = (name, pill, soon, text) => {
      const c = el("div", "plan-card" + (soon ? "" : " current"));
      const head = el("div", "plan-head");
      head.append(el("span", "plan-name", name), el("span", "plan-pill" + (soon ? " soon" : ""), pill));
      c.append(head, el("p", "plan-text", text));
      return c;
    };
    // A relay that asks for a subscription reports the account's plan, and it
    // is shown here as the relay says it. Nothing in the app depends on it.
    const relayPlan = remoteRoster && remoteRoster.plan;
    pane.append(card("Free", "Current plan", false, relayPlan
      ? "Every part of the desktop app, for good. Remote access through the relay has a plan of its own."
      : "Every part of the desktop app, and remote access to your panes through the shared relay."));
    if (relayPlan) {
      const remote = card("Remote access", relayPlan.name || relayPlan.plan, false, remotePlanText(relayPlan));
      remote.id = "set-remote-plan";
      remote.append(el("p", "plan-text", "It is paid for from Devices on a paired phone or browser."));
      pane.append(remote);
    }
    const soon = card("Enterprise", "Coming soon", true, "For companies: " + ENTERPRISE_WHAT + ".");
    const link = el("a", "plan-link");
    link.id = "set-plan-link";
    link.setAttribute("href", ENTERPRISE_URL);
    link.target = "_blank";
    link.rel = "noreferrer noopener";
    link.append(document.createTextNode("Read about Enterprise"), iconEl("ext", 13));
    soon.append(link);
    pane.append(soon);
    pane.append(sponsorLine());
    // Whose it is and under what licence, and the pages that say what the
    // shared relay keeps and on what terms: quiet, at the foot of the section
    // about the account, where somebody looking for them looks. They open in
    // the browser, as the link above does.
    const legal = el("p", "plan-legal", "Flockdeck · © 2026 Jim Wright · MIT licence");
    legal.id = "set-legal";
    LEGAL_LINKS.forEach(([id, text, href]) => {
      const a = el("a", "", text);
      a.id = id;
      a.setAttribute("href", href);
      a.target = "_blank";
      a.rel = "noreferrer noopener";
      legal.append(document.createTextNode(" · "), a);
    });
    pane.append(legal);
  }

  /** sponsorLine is the one place the app mentions sponsorship: a line under
   *  the plans, drawn only when this section is, that links to GitHub Sponsors
   *  and does nothing else. It is not a plan, so it is kept apart from the
   *  cards; it counts nothing, asks nothing and never appears on its own, and
   *  sponsoring changes nothing in the app, which the line says. */
  function sponsorLine() {
    const line = el("p", "plan-sponsor");
    line.id = "set-sponsor";
    const a = el("a");
    a.id = "set-sponsor-link";
    a.setAttribute("href", SPONSOR_URL);
    a.target = "_blank";
    a.rel = "noreferrer noopener";
    a.append(document.createTextNode("Sponsor Flockdeck"), iconEl("ext", 12));
    line.append(a, document.createTextNode(" on GitHub, if it is useful to you. Sponsoring is a thank-you, and buys nothing."));
    return line;
  }

  // --------------------------------------------------------------- fan out

  let fanout = null;
  /** fanoutRoutes takes the server's answer to routeTasks; the open dialog
   *  puts its own in place, and with no dialog open it has nothing to do. */
  let fanoutRoutes = () => {};
  /** The most agents one fan-out starts: the server's workspace.MaxTasks,
   *  which a test keeps this level with. */
  const FANOUT_MAX = 12;

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
    fanoutAsked = paneID || focusedPaneId();
    send({ cmd: "fanoutPreview", id: fanoutAsked });
  }
  /** The pane the open dialog asked about. The server answers from a
   *  goroutine of its own, so a late answer for a dialog opened on one pane
   *  could arrive after it had been opened again on another, and replaced it:
   *  Start then used the first pane's plan and started in its tab. Empty
   *  when no pane was focused, and the server chose. */
  let fanoutAsked = "";

  /** agentOption is one entry of an agentSelect: what it reads as, and the
   *  agent and model it stands for. */
  function agentOption(text, value) {
    const o = el("option", null, text);
    o.value = value;
    return o;
  }

  /** agentSelect is the control that says which agent, and which of its
   *  models, does a piece of work.
   *
   *  Agent and model are one control rather than two because they are one
   *  decision: a model belongs to the agent that knows it, and a row of the
   *  task list is far too narrow to spend two controls on. The value carries
   *  both, with a newline between them — the one character neither an agent id
   *  nor a model id can contain.
   *
   *  An agent this machine does not have is offered greyed, with the line that
   *  says where to get it, rather than left out: somebody who has not installed
   *  Codex should still learn that Flockdeck would run it. */
  function agentSelect(agents, value, firstLabel) {
    const sel = el("select", "fan-agent-sel");
    if (firstLabel) sel.append(agentOption(firstLabel, ""));
    (agents || []).forEach((a) => {
      const group = document.createElement("optgroup");
      group.label = a.unavailable
        ? a.name + " — not installed" + (a.install ? ", see " + a.install : "")
        : a.name;
      // An agent that names no models still has to be choosable, so it stands
      // as a single entry asking for whatever model it is already set to.
      const models = a.models && a.models.length ? a.models : [{ id: a.default || "" }];
      models.forEach((mo) => {
        const o = agentOption(modelLabel(mo), a.id + "\n" + (mo.id || ""));
        o.disabled = !!a.unavailable;
        group.append(o);
      });
      sel.append(group);
    });
    sel.value = agentValue(agents, value);
    // A choice naming an agent the catalog no longer has leaves the control
    // showing nothing at all, which reads as a control that is broken rather
    // than as one whose agent has gone.
    if (sel.selectedIndex < 0) sel.selectedIndex = 0;
    return sel;
  }

  /** agentValue is the entry of an agentSelect that stands for value. An empty
   *  model is whatever the agent is set to, and for an agent whose models have
   *  no empty entry - the model APIs - it matched nothing, so the control fell
   *  back to its first entry: another agent, or one not installed. Settings
   *  then showed the wrong default and a fan-out could start the wrong agent.
   *  An empty model is the agent's default model where that is one of its
   *  entries, and an agent found without the model named is on its own first
   *  model rather than on somebody else's. */
  function agentValue(agents, value) {
    const [id, model] = pickParts(value);
    const a = (agents || []).find((x) => x.id === id);
    if (!a) return value || "";
    const ids = (a.models && a.models.length ? a.models : [{ id: a.default || "" }]).map((mo) => mo.id || "");
    let pick = ids[0];
    if (ids.includes(model)) pick = model;
    else if (!model && ids.includes(a.default || "")) pick = a.default || "";
    return a.id + "\n" + pick;
  }

  /** modelLabel is a model as one line of a select: its name and note, then in
   *  brackets its tier and price where they are known. A select has no room
   *  for the picker's chips, and choosing is exactly when they help. */
  function modelLabel(mo) {
    const name = mo.name || mo.id || "Default";
    const extra = [mo.tier, mo.price && dollars(mo.price.in) + "/" + dollars(mo.price.out)].filter(Boolean);
    return name + (mo.note ? " — " + mo.note : "") + (extra.length ? " (" + extra.join(", ") + ")" : "");
  }

  /** routeSummary is the fan-out's one line about routing: how many rows it
   *  moved to a smaller model, and how many to a stronger one. */
  function routeSummary(down, up, total) {
    const of = (n) => n + " of " + total + (total === 1 ? " task" : " tasks");
    const smaller = down === 1 ? "a smaller model" : "smaller models";
    if (down && up) return "Routing chose " + smaller + " for " + of(down) + " and " + (up === 1 ? "a stronger one" : "stronger ones") + " for " + up + ".";
    if (down) return "Routing chose " + smaller + " for " + of(down) + ".";
    if (up) return "Routing chose " + (up === 1 ? "a stronger model" : "stronger models") + " for " + of(up) + ".";
    return "";
  }

  /** pickParts splits an agentSelect value back into its agent and its model. */
  function pickParts(value) {
    const i = (value || "").indexOf("\n");
    return i < 0 ? ["", ""] : [value.slice(0, i), value.slice(i + 1)];
  }

  function renderFanout(msg) {
    if (msg) {
      // Only the answer to what the open dialog asked, and only the first:
      // another would redraw the list under whatever was being typed in it.
      if (dialog !== "fanout" || fanout || (fanoutAsked && msg.paneId !== fanoutAsked)) return;
      fanout = msg;
    }
    if (dialog !== "fanout") return; // see openWorktrees
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

    // The agent controls are drawn only where there is a decision to make. A
    // Flockdeck that knows one agent with one model has nothing to ask, and a
    // dialog that asks it anyway has grown for nobody.
    const catalog = m.agents || [];
    const picks = catalog.reduce((n, a) => n + Math.max(1, (a.models || []).length), 0);
    const choosable = catalog.length > 0 && picks > 1;

    let runSel = null;
    if (choosable) {
      const chosen = catalog.find((a) => a.id === m.agent) || catalog[0];
      runSel = agentSelect(catalog, chosen.id + "\n" + (m.model || chosen.default || ""));
      const line = el("div", "fan-agent");
      line.append(el("span", null, "Run every task with"), runSel);
      body.append(line);
    }

    // What routing chose for each row, keyed by the task's text like the
    // overrides below, so an edited line is routed afresh. A row somebody has
    // chosen for keeps their choice. None of it is drawn while routing is off
    // for the project, and the dialog is then what it always was.
    const routing = choosable && !!m.routing;
    const routed = new Map();
    let routeNote = m.routeNote || "";
    const takeRoutes = (tasks, routes) => {
      routed.clear();
      (tasks || []).forEach((t, i) => { if (routes && routes[i]) routed.set(t.trim(), routes[i]); });
    };
    if (routing) takeRoutes(m.tasks, m.routes);
    /** The routed choices changed before starting, for the routing log. */
    const overridden = [];
    let routeText = null;
    let routeClear = null;
    if (routing) {
      const line = el("div", "fan-route");
      routeText = el("span", "fan-route-text");
      // After the run's select in the tab order, since it is about that
      // select: it puts every routed row back on the model chosen there.
      routeClear = el("button", "chip", "Use the run's model for every task");
      routeClear.id = "fan-route-clear";
      routeClear.onclick = () => {
        const had = document.activeElement === routeClear;
        taskLines().forEach((t) => {
          const r = activeRoute(t);
          if (!r) return;
          overridden.push({ rule: r.rule, agent: r.agent || pickParts(runSel.value)[0], routed: r.model, chosen: pickParts(runSel.value)[1] });
          overrides.set(t, "");
        });
        renderRows();
        updateCount();
        // The button hides itself once nothing is left routed, and hid
        // itself with the keyboard on it, which then went nowhere. It goes to
        // the run's own select, which is what the button was about.
        if (had && routeClear.hidden) runSel.focus();
      };
      line.append(routeText, routeClear);
      body.append(line);
    }

    const box = el("textarea", "fan-tasks");
    box.value = (m.tasks || []).join("\n");
    box.placeholder = "One task per line, for example:\nAdd a health endpoint\nWrite tests for the parser\n\nCtrl+Enter starts them.";
    body.append(box);

    // Which agent each line gets, where it is not the one chosen for the run.
    //
    // Keyed by the text of the task rather than by its position, because the
    // box above is being edited: deleting the second line moves every line
    // below it up, and an override held by position would be left sitting on
    // somebody else's task. Editing a line that had been assigned does lose the
    // assignment, which is the honest answer — it is a different task now.
    const overrides = new Map();
    const rows = el("div", "fan-rows");
    if (choosable) body.append(rows);

    /** The route a row starts on, where routing chose one and nobody has
     *  chosen for the row since. */
    const activeRoute = (task) => (routing && !overrides.has(task) && routed.get(task)) || null;
    /** What a row's select holds: somebody's choice, else routing's, else
     *  the run's. A route that moved the row to another agent keeps that
     *  agent here; one that only chose a smaller or stronger model of the
     *  run's own agent still reads on the run's. */
    const effective = (task) => {
      if (overrides.has(task)) return overrides.get(task);
      const r = activeRoute(task);
      return r ? (r.agent || pickParts(runSel.value)[0]) + "\n" + r.model : "";
    };

    const opts = el("div", "fan-opts");
    const wt = el("label", "fan-opt");
    const wtBox = el("input");
    wtBox.type = "checkbox";
    wtBox.checked = !!m.isRepo;
    wtBox.disabled = !m.isRepo;
    wt.append(wtBox, document.createTextNode(
      m.isRepo ? "Give each agent its own git worktree" : "Not a git repository — agents share this directory"));
    opts.append(wt);

    // The children always end up in one tab, gridded; the only question is
    // whether that tab is this one or a new one.
    const sp = el("label", "fan-opt");
    const spBox = el("input");
    spBox.type = "checkbox";
    // Settings › Behaviour's own default, until this run's own choice changes it.
    spBox.checked = !!(prefs.fanOut && prefs.fanOut.sameTab);
    sp.append(spBox, document.createTextNode("Put them in this tab, beside the agent that planned them"));
    sp.title = "Off, the agents share a new tab of their own";
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

    /** tally is what the count line says: how many agents, and — when the run
     *  has been split between two of them — how many of each. A dozen panes
     *  divided on purpose is the easiest thing here to have got wrong, and the
     *  last moment to notice it is before any of them exist. */
    const tally = (lines) => {
      const text = agentTally(lines);
      const n = lines.filter((t) => activeRoute(t)).length;
      return n ? text + " · " + n + " routed" : text;
    };
    const agentTally = (lines) => {
      const plural = lines.length === 1 ? "1 agent" : lines.length + " agents";
      if (!choosable) return plural;
      const runID = pickParts(runSel.value)[0];
      const byAgent = new Map();
      lines.forEach((task) => {
        const id = pickParts(effective(task))[0] || runID;
        byAgent.set(id, (byAgent.get(id) || 0) + 1);
      });
      if (byAgent.size < 2) return plural;
      const parts = [];
      byAgent.forEach((n, id) => {
        const a = catalog.find((x) => x.id === id);
        parts.push(n + " × " + ((a && a.name) || id));
      });
      return plural + " — " + parts.join(", ");
    };

    /** Whether any agent this fan-out would start asks the folder-trust
     *  question the trust row is about. Only Claude Code does, and the row
     *  was drawn - in Claude's words - whichever agents had been chosen. The
     *  server says which agents ask it; until it does, the answer is Claude. */
    const asksTrust = (id) => {
      const a = catalog.find((x) => x.id === id);
      return catalog.some((x) => "askTrust" in x) ? !!(a && a.askTrust) : id === "claude";
    };
    const trustApplies = (lines) => {
      const runID = choosable ? pickParts(runSel.value)[0] : (m.agent || (catalog[0] && catalog[0].id) || "claude");
      if (asksTrust(runID) && lines.some((t) => !pickParts(effective(t))[0])) return true;
      return lines.some((t) => asksTrust(pickParts(effective(t))[0]));
    };

    const updateCount = () => {
      const lines = taskLines();
      tr.hidden = !trustApplies(lines.length ? lines : [""]);
      // The server stops at its cap and says so only afterwards, so a list
      // longer than that promised agents that were never going to start.
      const starts = Math.min(lines.length, FANOUT_MAX);
      count.textContent = lines.length > FANOUT_MAX
        ? lines.length + " tasks - only the first " + FANOUT_MAX + " start; run the rest as a second fan-out"
        : tally(lines);
      start.disabled = lines.length === 0;
      start.textContent = starts === 1 ? "Start 1 agent" : "Start " + starts + " agents";
      if (routing) {
        const chosen = lines.map(activeRoute).filter(Boolean);
        const up = chosen.filter((r) => r.up).length;
        routeText.textContent = routeSummary(chosen.length - up, up, lines.length) ||
          routeNote || "Routing has nothing to change in these tasks.";
        routeClear.hidden = chosen.length === 0;
      }
    };

    // The rules run on the server alone. The list is asked about again once
    // typing pauses, and whenever the run's agent or model changes, since a
    // route is a model of the agent the run is on.
    let routeTimer = 0;
    const askRoutes = () => {
      if (!routing) return;
      clearTimeout(routeTimer);
      routeTimer = setTimeout(() => {
        const [agent, model] = pickParts(runSel.value);
        send({ cmd: "routeTasks", tasks: taskLines(), agent, model });
      }, 250);
    };
    fanoutRoutes = (msg) => {
      if (dialog !== "fanout" || !routing) return;
      const [agent, model] = pickParts(runSel.value);
      // An answer about a run since changed would pre-fill another agent's
      // models; the answer to the question now being asked is on its way.
      if (msg.agent !== agent || msg.model !== model) return;
      takeRoutes(msg.tasks, msg.routes);
      routeNote = msg.routeNote || "";
      renderRows();
      updateCount();
    };

    /* The row drawn for each task, by its text, with what its select and tag
     * were drawn from. The box is typed into a line at a time, and building
     * every row again for each keystroke - each with a select holding every
     * model of every agent - was nearly all a keystroke cost: twelve tasks
     * over three agents of four models were 240 elements a key. A row is kept
     * while its task and what it shows are unchanged. */
    let drawn = new Map();
    const rowKey = (task) => {
      const r = activeRoute(task);
      return effective(task) + "\n" + (r ? [r.model, !!r.up, r.rule, r.reason].join("|") : "");
    };
    const renderRows = () => {
      const kept = new Map();
      const order = [];
      taskLines().forEach((task, i) => {
        const key = rowKey(task);
        const pool = drawn.get(task) || [];
        const at = pool.findIndex((d) => d.key === key);
        const d = at >= 0 ? pool.splice(at, 1)[0] : buildRow(task, key);
        if (d.n.textContent !== String(i + 1)) d.n.textContent = String(i + 1);
        if (!kept.has(task)) kept.set(task, []);
        kept.get(task).push(d);
        order.push(d.row);
      });
      drawn = kept;
      // Into place, moving only what is out of it; what is left over after
      // the last row is what went from the box.
      order.forEach((row, i) => { if (rows.children[i] !== row) rows.insertBefore(row, rows.children[i] || null); });
      while (rows.children.length > order.length) rows.children[rows.children.length - 1].remove();
    };
    const buildRow = (task, key) => {
      const row = el("div", "fan-row");
      const n = el("span", "fan-row-n");
      row.append(n);
      const d = { row, n, key };
      const text = el("span", "fan-row-task", task);
      text.title = task;
      row.append(text);
      const sel = agentSelect(catalog, effective(task), "Same as the run");
      const r = activeRoute(task);
      let tag = null;
      if (r) {
        tag = describe(el("span", "fan-routed" + (r.up ? " up" : ""), (r.up ? "↗" : "↘") + " routed"),
          r.reason + ". Choose another model here to decide this row yourself.");
      }
      sel.onchange = () => {
        // Changing a routed row makes it the user's: the tag goes, and
        // routing leaves the row alone from here on, even on "Same as the
        // run".
        const was = activeRoute(task);
        if (was) {
          overridden.push({ rule: was.rule, agent: was.agent || pickParts(runSel.value)[0], routed: was.model,
            chosen: pickParts(sel.value)[1] || pickParts(runSel.value)[1] });
        }
        if (sel.value || was) overrides.set(task, sel.value);
        else overrides.delete(task);
        if (tag) tag.remove();
        // What the row shows now, so the next drawing keeps it.
        d.key = rowKey(task);
        // The choice is held by the task's text, so every row with the same
        // text now has it. Only this row was redrawn, and the other went on
        // showing "Same as the run" while Start sent the new choice for both.
        renderRows();
        updateCount();
      };
      row.append(sel);
      if (tag) row.append(tag);
      return d;
    };

    const start = el("button", "chip primary", "Start agents");
    start.onclick = () => {
      const tasks = taskLines();
      if (!tasks.length) return;
      const req = {
        cmd: "fanout",
        id: m.paneId,
        tasks,
        worktrees: wtBox.checked && !wtBox.disabled,
        split: spBox.checked,
        trust: trBox.checked && !trBox.disabled && !tr.hidden,
      };
      // The agent fields are left off altogether where there was nothing to
      // choose, so a fan-out that made no decision about agents goes over the
      // wire looking exactly as it always has.
      if (choosable) {
        const run = pickParts(runSel.value);
        req.agent = run[0];
        req.model = run[1];
        // A routed row goes over the wire as any row chosen by hand does, so
        // what was shown is what runs; the rule's name only marks its pane.
        const perTask = tasks.map((t) => pickParts(effective(t))[0]);
        if (perTask.some(Boolean)) {
          req.taskAgents = perTask;
          req.taskModels = tasks.map((t) => pickParts(effective(t))[1]);
        }
        if (routing) {
          const rules = tasks.map((t) => (activeRoute(t) || {}).rule || "");
          if (rules.some(Boolean)) req.taskRouted = rules;
          if (overridden.length) req.routeOverrides = overridden;
        }
      }
      send(req);
      closeOverlay();
    };
    box.oninput = () => { if (choosable) renderRows(); updateCount(); askRoutes(); };
    // Enter is a new line here - the tasks go one to a line - so starting
    // them took the pointer, or Tab past every option to the button.
    box.onkeydown = (ev) => {
      if (ev.key !== "Enter" || !(ev.ctrlKey || ev.metaKey)) return;
      ev.preventDefault();
      start.onclick();
    };
    if (choosable) {
      runSel.onchange = () => {
        // The routes were models of the agent the run was on.
        if (routing) { routed.clear(); renderRows(); askRoutes(); }
        updateCount();
      };
      renderRows();
    }
    go.append(start, count);
    body.append(go);
    updateCount();

    const tools = el("div", "wt-tools");
    tools.append(rootLabel(m.cwd || ""));
    body.append(tools);
    box.focus();
  }

  // ----------------------------------------------------------------- hints

  /* One line under the tab bar, at most, and only for something the interface
   * cannot say for itself. Each is dismissed for good, and remembered on the
   * Go side: a hint that comes back after being sent away is worse than one
   * never shown.
   *
   * The bindings are not written out here either. A hint names the action and
   * the key is filled in from the same table as everything else.
   *
   * Two tiers. PRIORITY_HINTS explains something already on screen that needs
   * it — an amber dot, a pane that has died, work nobody has looked at yet —
   * and the first one that matches wins outright: a dot nobody can read
   * outranks anything else this bar could be saying. TIP_HINTS is the rest: a
   * pool of features worth knowing about that most people never stumble on,
   * several of which usually apply at once. Rather than whichever sits first
   * in the list winning forever, tipIndex below turns them over, so a
   * workspace left open a while surfaces more of the pool than the one entry
   * closest to the top — and since `when` still gates every entry, what turns
   * up still depends on what is actually true of the workspace right now: an
   * idle pane and a busy one are not offered the same tips.
   */
  const PRIORITY_HINTS = [
    {
      id: "waiting",
      text: "An amber dot means that agent is waiting on you — a permission prompt, or a question.",
      page: "status",
      // Only with an amber dot on screen to point at. The waiting count is
      // every project's, and an agent waiting in one not shown had this
      // explaining a dot that was nowhere to be seen.
      when: (s) => currentPanes(s).some((v) => v.status === "waiting"),
    },
    {
      id: "exited",
      text: "A pane whose agent has stopped shows Restart over its terminal rather than a dead screen — it starts the same agent again in the same place, and picks the conversation back up.",
      page: "panes",
      when: (s) => currentPanes(s).some((v) => v.status === "exited"),
    },
    {
      id: "dirty-pane",
      action: "changes",
      text: "reviews what has changed in this checkout — a coloured diff, then commit and push — before whichever agent wrote it gets any further ahead of you.",
      page: "changes",
      when: (s) => currentPanes(s).some((v) => (v.dirty || 0) + (v.untracked || 0) > 0),
    },
  ];

  const TIP_HINTS = [
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
      id: "shell-pane",
      text: "Split right (shell), in the command palette, opens an ordinary terminal beside your agent, in the same folder — for the git status or npm run you want to run yourself while it works.",
      page: "panes",
      when: () => true,
    },
    {
      id: "broadcast",
      action: "promptAll",
      text: "opens the prompt bar — write an instruction once and send it to several agents together. Turn on Broadcast to reach every pane in the tab, or add panes to the set by hand with their ⇉ button.",
      page: "broadcast",
      when: (s) => paneCount(s) > 1,
    },
    {
      id: "fanout",
      action: "fanout",
      text: "turns a plan an agent just wrote into one agent per task, up to twelve at once — each can get its own git worktree, and nothing starts until you say so.",
      page: "fanout",
      when: () => true,
    },
    {
      id: "worktrees",
      action: "worktrees",
      text: "gives each agent its own checkout and branch, which is what keeps several of them from fighting over one working tree.",
      page: "worktrees",
      when: (s) => paneCount(s) > 2,
    },
    {
      id: "history",
      action: "history",
      text: "lists every past conversation in this project — Claude Code's own transcripts included — and resumes any of them into a new tab, picked up exactly where it stopped.",
      page: "history",
      when: () => true,
    },
    {
      id: "zoom",
      action: "zoomPane",
      text: "gives the focused pane the whole tab. The others keep running, just off screen, until you press it again.",
      page: "panes",
      when: (s) => paneCount(s) > 1,
    },
    {
      id: "tidy-panes",
      text: "Tile these panes evenly and Merge every tab into this one, both in the command palette, straighten a lopsided split and fold a scattered workspace back into a single tab.",
      page: "rearranging",
      when: (s) => paneCount(s) > 2,
    },
    {
      id: "find-in-terminal",
      action: "findInTerminal",
      text: "searches the focused terminal's own scrollback — the error message that went by twenty tool calls ago is still in there.",
      page: "panes",
      when: () => true,
    },
    {
      id: "api-keys",
      text: "API keys…, in the command palette, stores a key for an API agent once, kept on this machine only — no more pasting it into every terminal it opens.",
      page: "agents",
      when: () => true,
    },
    {
      id: "remote-access",
      text: "Remote access…, in the command palette, pairs a phone or another desktop through a relay, so a waiting agent can be glanced at, or answered, away from this machine.",
      page: "remote",
      when: () => true,
    },
    {
      id: "detach",
      text: "Detach, in the command palette, closes the window but leaves every agent running — flockdeck from a terminal brings the window back to exactly where you left it.",
      page: "persistence",
      when: () => true,
    },
    {
      id: "split-project",
      text: "Split into project: …, in the command palette, puts an agent from another open project into this tab, so two repositories can sit side by side.",
      page: "projects",
      when: (s) => (s.projects || []).length > 1,
    },
    {
      id: "pane-header-git",
      text: "A pane's header keeps showing its branch and how many files are uncommitted even while it sits idle, so you can see whose work is worth a look without opening anything.",
      page: "worktrees",
      when: (s) => {
        const p = currentPanes(s);
        return p.length > 0 && p.every((v) => v.status === "idle");
      },
    },
    {
      id: "working-tool",
      text: "While a pane's dot pulses green, its header names the tool it is using right now — Read, Bash, Edit — so you can tell what it is actually doing, not only that it is busy.",
      page: "status",
      when: (s) => currentPanes(s).some((v) => v.status === "working"),
    },
  ];

  function currentPanes(s) { return Object.values((s && s.panes) || {}); }
  function dismissedHint(id) { return (prefs.dismissedTips || []).includes(id); }
  function paneCount(s) { return Object.keys((s && s.panes) || {}).length; }

  /** tipIndex turns TIP_HINTS from "whichever matches first, forever" into a
   *  rotation: scheduleTipRotate moves it on every so often, so a workspace
   *  left open surfaces more of the pool than the one entry nearest the top
   *  of it. Real time, not a state change, is what advances it — a tip
   *  nobody read is still worth trying again later, on a screen nothing else
   *  has changed on. */
  let tipIndex = 0;
  let tipRotateTimer = null;
  const TIP_ROTATE_MS = 45000;

  function scheduleTipRotate() {
    clearTimeout(tipRotateTimer);
    tipRotateTimer = setTimeout(() => {
      tipIndex++;
      renderHints();
      scheduleTipRotate();
    }, TIP_ROTATE_MS);
  }

  /** pickHint is what renderHints shows: whichever PRIORITY_HINTS entry
   *  matches, since something on screen needs explaining right now; failing
   *  that, whichever TIP_HINTS entry the rotation currently lands on, among
   *  those that both match the state and have not been sent away for good. */
  function pickHint(s) {
    const urgent = PRIORITY_HINTS.find((h) => !dismissedHint(h.id) && h.when(s));
    if (urgent) return urgent;
    const pool = TIP_HINTS.filter((h) => !dismissedHint(h.id) && h.when(s));
    return pool.length ? pool[tipIndex % pool.length] : null;
  }

  function renderHints() {
    const bar = $("hints");
    if (!bar) return;
    const hint = state ? pickHint(state) : null;
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
      // The button pressed went with the hint, and the keyboard with it, onto
      // nothing: back to the terminal, where the work is.
      if (!dialogOpen()) focusTerminal();
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
    fetch(basePath + "help.json", { credentials: "same-origin" })
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
    // A placeholder is no name: it is gone as soon as anything is typed, and
    // is not reliably read out as the field's label.
    search.setAttribute("aria-label", "Search the help");
    search.value = helpQuery;
    search.autocomplete = "off";
    search.spellcheck = false;
    search.oninput = () => { helpQuery = search.value; renderHelpList(); renderHelpContent(); };
    search.onkeydown = helpSearchKey;
    nav.append(search, el("div", "help-list"));

    const content = el("div", "help-content");
    content.id = "help-content";
    content.tabIndex = 0;
    // A page refers to another as a link to its slug - [Worktrees](#worktrees)
    // - and following one opens that page here. The references were bold
    // text before, with no way to get from one page to the next but the
    // contents list.
    content.onclick = (e) => {
      const a = e.target.closest && e.target.closest("a[href]");
      const href = a ? a.getAttribute("href") : "";
      if (!href.startsWith("#") || !pageFor(href.slice(1))) return;
      e.preventDefault();
      showHelpPage(href.slice(1));
      content.focus();
    };
    wrap.append(nav, content);
    body.append(wrap);
    search.focus();
  }

  function renderHelpList() {
    const list = $("overlay-body").querySelector(".help-list");
    if (!list) return;
    // Choosing a page rebuilds the list, which destroyed the item the choice
    // was made on and dropped the keyboard out of the dialog: reading the
    // help from the keyboard meant tabbing back to the list after every page.
    const hadKeyboard = list.contains(document.activeElement);
    list.textContent = "";
    const hits = helpMatches();
    if (!hits.length) {
      list.append(el("div", "help-none", "Nothing here matches."));
      return;
    }
    let sel = null;
    hits.forEach((hit) => {
      const item = el("button", "help-item" + (hit.page.slug === helpSlug ? " sel" : ""));
      item.append(el("span", "help-item-title", hit.page.title));
      item.append(el("span", "help-item-sub", hit.snippet || hit.page.summary));
      item.onclick = () => { showHelpPage(hit.page.slug); };
      list.append(item);
      // The colour was all that said which page is open; a screen reader was
      // told nothing.
      if (hit.page.slug === helpSlug) { sel = item; item.setAttribute("aria-current", "page"); }
    });
    // Rebuilding the list puts it back at the top, so walking it with the
    // arrow keys from the search box took the highlight past the bottom of
    // the box and out of sight; the page it named was shown, and nothing said
    // where in the list it was.
    if (sel) sel.scrollIntoView({ block: "nearest" });
    if (sel && hadKeyboard) sel.focus();
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

  /** The hits for the search as it currently reads. One keystroke asks for
   *  them from the contents list, from the page beside it and, on an arrow key,
   *  from the key handler as well; there is one answer between them. */
  let helpHits = null;
  let helpHitsFor = null;

  /** helpMatches is the contents list, filtered by the search box. Each hit
   *  carries the piece of the page the words were found in, so the list
   *  answers "which page is this in" without opening each one. */
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
    // The search box keeps the keyboard for as long as the help is open, and
    // the arrows walk the contents from it; Page Up and Page Down did
    // nothing, so the page being read could not be scrolled without the
    // mouse. They scroll it by a screenful, less a line of overlap.
    if (e.key === "PageDown" || e.key === "PageUp") {
      const content = $("help-content");
      if (!content) return;
      e.preventDefault();
      const step = Math.max(40, (content.clientHeight || 0) - 40);
      content.scrollTop = Math.max(0, (content.scrollTop || 0) + (e.key === "PageDown" ? step : -step));
      return;
    }
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

  /** How long a message stays. An error is not just news: something you asked
   *  for did not happen, and this toast is the only place it is ever said —
   *  there is no log to go back to. Four seconds is long enough for a message
   *  about the thing you are looking at and not long enough for one about a
   *  push you started before turning to another pane. */
  const NOTICE_MS = 4000;
  const ERROR_MS = 12000;
  /** How long a message stays once the pointer has left it. */
  const NOTICE_AFTER_MS = 1500;
  /** How long a message stays with the pointer still on it. Long enough to
   *  read any of them, and an end all the same, for a pointer simply left
   *  where the messages appear. */
  const NOTICE_HELD_MS = 30000;
  /** Whether the pointer is resting on the message. A message is read with
   *  the pointer on it as often as not, and it went on its own timer
   *  regardless: a long git error was taken away mid-sentence. It stays while
   *  the pointer is there, and for a moment after it leaves.
   *
   *  It is the pointer moving on the message that holds it, not the pointer
   *  arriving. Chromium says the pointer entered when a message appears under
   *  one parked there, and a mouse left in that corner kept every message up
   *  for as long as someone went on typing. */
  let noticeHeld = false;

  function notice(text, isError) {
    const n = $("notice");
    n.classList.toggle("error", !!isError);
    // Set before the text, because it is the text changing that a screen
    // reader acts on and it acts with whatever politeness is in force then. An
    // error interrupts: waiting for a pause to mention that a push was
    // rejected is waiting for the moment it stops mattering.
    n.setAttribute("aria-live", isError ? "assertive" : "polite");
    n.textContent = text;
    n.hidden = false;
    clearTimeout(notice.timer);
    notice.timer = setTimeout(hideNotice, noticeHeld ? NOTICE_HELD_MS : isError ? ERROR_MS : NOTICE_MS);
  }

  /** hideNotice takes the message away. It is also what a click on it does:
   *  the toast is drawn over the terminals and takes the pointer, so a click
   *  meant for the pane underneath was going nowhere at all. */
  function hideNotice() {
    clearTimeout(notice.timer);
    noticeHeld = false;
    $("notice").hidden = true;
  }

  /** holdNotice keeps the message while the pointer moves on it, and lets it
   *  go once the pointer has been still on it for NOTICE_HELD_MS. */
  function holdNotice() {
    noticeHeld = true;
    clearTimeout(notice.timer);
    notice.timer = setTimeout(hideNotice, NOTICE_HELD_MS);
  }

  // -------------------------------------------------------------- shortcuts

  /** claimKey keeps a key that has run one of the window's actions from going
   *  any further. Preventing its default is not enough: xterm reads the
   *  keydown on its own textarea without asking whether anything prevented
   *  it, so - pressed in headless Chrome against a real pane - Ctrl+Shift+Left
   *  moved the pane and also sent ESC[1;6D to the program in it, and Alt+1
   *  switched tab and sent ESC 1, a numeric argument to a shell. This listener
   *  runs first, on the window, so stopping the key here keeps it from the
   *  terminal altogether. */
  function claimKey(e) {
    e.preventDefault();
    e.stopPropagation();
  }

  window.addEventListener("keydown", (e) => {
    // A key pressed while an input method is composing belongs to it. Enter
    // there confirms the characters being composed, and taking it as Enter
    // sent a prompt, ran a palette command or created a worktree with a name
    // half-written. The dialog fields that act on Enter are kept from seeing
    // it too; xterm is not, because it reads these keys to follow the
    // composition itself.
    if (e.isComposing || e.keyCode === 229) {
      if (!(e.target.classList && e.target.classList.contains("xterm-helper-textarea"))) e.stopPropagation();
      return;
    }
    // Recording a new binding for Settings › Keybindings takes over the
    // keyboard entirely -- what is being replaced, most of all, must not run
    // while its own row is waiting to hear what replaces it. Tab is the one
    // key left to fall through: cancelling the capture and letting it go on
    // moving the keyboard inside the dialog, as it always does, reads better
    // than recording "Tab" by surprise on the way to the next field.
    if (keybindEditing) {
      if (e.key !== "Tab") { keybindCaptureKey(e); return; }
      cancelKeybindCapture();
    }
    if (e.key === "Tab" && trapTab(e)) return;
    // Nothing behind the disconnected panel can be used, and the shortcuts
    // went on running under it: the palette opened out of sight and took what
    // was typed into its hidden field, and Escape closed a dialog behind the
    // panel and put the keyboard in a terminal. Tab is kept inside the panel
    // above, and Enter is left to press its button.
    if (!$("disconnected").hidden) return;
    if (!$("palette").hidden) { paletteKey(e); return; }
    if (e.key === "Escape" && railMenuOpen() && $("overlay").hidden) { e.preventDefault(); closeRailMenu(true); return; }
    // Anywhere in the bar, not only in its field: after a click on one of its
    // arrows the keyboard is on that button, and Escape did nothing there.
    // F3 steps through the matches, as in nearly every Windows program, from
    // the box or from the terminal being searched. Left to the browser it
    // opened a find bar of its own, over the page's text.
    if (!$("searchbar").hidden && e.key === "F3") { claimKey(e); runSearch(e.shiftKey); return; }
    if (!$("searchbar").hidden && $("searchbar").contains(document.activeElement)) {
      if (e.key === "Escape") { e.preventDefault(); closeSearch(); return; }
      if (e.key === "Enter" && document.activeElement === $("search-input")) { e.preventDefault(); runSearch(e.shiftKey); return; }
    }
    if (!$("overlay").hidden && e.key === "Escape") {
      // A search field with something in it is emptied first, as search
      // fields are: closing the help, or the picker, took the page being
      // read or the dialog itself with it, when all that was wanted was to
      // start the search again.
      const f = e.target;
      if (f && (f.id === "help-search" || f.id === "agent-filter" || f.id === "history-filter" || f.id === "settings-find") && f.value) {
        e.preventDefault();
        f.value = "";
        f.dispatchEvent(new Event("input"));
        return;
      }
      // An agent's address being typed in the picker is put away, and the
      // picker kept: closing it took a half-typed address with it, and left
      // no way to tell whether anything had been saved.
      if (f && f.id === "agent-address") {
        e.preventDefault();
        closeAddress();
        return;
      }
      // So is an API key being typed: the form goes, with what was typed in
      // it, and the dialog stays.
      if (f && f.id === "key-field" && keyCancel) {
        e.preventDefault();
        keyCancel();
        return;
      }
      closeOverlay();
      return;
    }

    // Font size keeps working while the prompt bar is up, so the sentence
    // being composed can be made readable without abandoning it.
    const sizing = actionFor(e);
    if (sizing === "fontUp" || sizing === "fontDown" || sizing === "fontReset") {
      claimKey(e);
      runAction(sizing);
      return;
    }
    // Only while the keyboard is in the bar. The bar leaves the panes usable,
    // and one clicked into while it is open is being typed at: Escape there is
    // how an agent is interrupted, and it was closing the bar instead and
    // never reaching the agent, while every other binding did nothing at all.
    if (!$("promptbar").hidden && $("promptbar").contains(document.activeElement)) {
      if (e.key === "Escape") { e.preventDefault(); closePrompt(); }
      // Enter sends. Shift+Enter is left to the field, which starts a new
      // line with it, so a prompt of several lines can be written here.
      else if (e.key === "Enter" && !e.shiftKey && document.activeElement === $("prompt-input")) { e.preventDefault(); submitPrompt(); }
      // F6 and Shift+F6 leave the bar for the rest of the window, as they
      // leave a terminal. The bar took them and did nothing, so from here the
      // only way out without the mouse was closing it.
      else if (sizing === "nextRegion" || sizing === "prevRegion") { claimKey(e); runAction(sizing); }
      return;
    }

    // Every other binding is dispatched from the action table, so what the
    // help says a key does is what the key does.
    const id = actionFor(e);
    // Behind a dialog, the window's keys wait for it to close: they went on
    // acting on what the dialog covers, and Ctrl+Shift+W pressed in the middle
    // of a commit message closed a pane nobody could see. The font keys
    // (above), the palette and the help are the exceptions, since each is
    // about the window rather than a pane in it. The key is still kept from
    // the browser, whose own idea of Ctrl+Shift+W is closing the window.
    const covered = !!modalRoot();
    if (id && !editsText(e)) {
      if (covered && id !== "palette" && id !== "help") { e.preventDefault(); return; }
      claimKey(e);
      runAction(id);
      return;
    }
    if (covered) return;

    // Alt+1 … Alt+9 names a tab rather than being one binding, so it is the
    // one thing the table cannot express and this has to spell out.
    // The digit row is read by position where it can be: on AZERTY it types
    // & é " ' and gives digits only with Shift held, so Alt+1 arrived as Alt+&
    // and no tab could be picked by number. The keypad is left alone: Alt
    // held while digits are typed there is how Windows types a character by
    // its code, and Alt+0233 for é switched to tab 2 and then to tab 3.
    const row = /^Digit([1-9])$/.exec(e.code || "");
    const keypad = /^Numpad/.test(e.code || "");
    const typed = /^[1-9]$/.test(e.key || "") ? e.key : "";
    // On a Mac, Option with a digit is how most layouts type a character -
    // [ on a German Mac is Option+5, | is Option+7 - and read by position it
    // switched tab instead, so those characters could not be typed into a
    // pane at all. There it is a tab number only where it typed the digit,
    // and Cmd with a digit, which is how a Mac picks a tab anyway, does it.
    const n = onMac ? typed : row ? row[1] : (!keypad ? typed : "");
    if (e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey && n) {
      claimKey(e);
      runAction("selectTab", n);
      return;
    }
    const cmd = row ? row[1] : typed;
    if (onMac && e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey && cmd) {
      claimKey(e);
      runAction("selectTab", cmd);
    }
  }, true);

  // ------------------------------------------------------------------ wiring

  document.querySelectorAll("[data-icon]").forEach((n) => {
    n.innerHTML = iconSVG(n.dataset.icon, Number(n.dataset.size) || 16);
  });
  $("rail").addEventListener("keydown", railKey);
  // Wherever the keyboard or a click lands in the rail becomes its stop, so
  // Tab brings it back to the button last used.
  $("rail").addEventListener("focusin", (e) => {
    const b = e.target.closest && e.target.closest("button");
    if (b && b !== railStop && railButtons().includes(b)) placeRailStop(b);
  });
  // Choosing anything in the menu is done with it. Each button's own handler
  // has run by now, so the dialog it opened keeps the keyboard.
  $("rail").addEventListener("click", (e) => {
    if (e.target.closest && e.target.closest("button")) closeRailMenu(false);
  });
  $("rail-toggle").onclick = () => { if (railMenuOpen()) closeRailMenu(true); else openRailMenu(); };
  describe($("rail-toggle"), "Projects, tools and settings");
  $("rail-scrim").onclick = () => closeRailMenu(true);
  $("rail-collapse").onclick = () => runAction("toggleRail");
  initRailResize();
  // Widened past the point where the rail folds, an open menu would go on
  // holding the keyboard with nothing on screen to say so.
  try {
    matchMedia("(max-width: 640px)").addEventListener("change", (e) => { if (!e.matches) closeRailMenu(false); });
  } catch { /* no matchMedia, no folding */ }
  placeRailStop();
  $("rail-open").onclick = () => openProjects(true);
  $("btn-palette").onclick = () => runAction("palette");
  $("new-tab").onclick = () => send({ cmd: "newTab", kind: "agent" });
  // Described here rather than only in describeChrome, which writes what the
  // action table says and can only do so once the table names this one.
  describe($("new-tab-pick"), TIPS.chooseAgent);
  $("new-tab-pick").onclick = () => runAction("newAgentTabChoose");
  wireTabStripDrops();
  wireDragSafetyNet();
  // The strip scrolls sideways and shows no scrollbar, and a mouse wheel turns
  // the other way: with more tabs than fit, the ones past the edge could be
  // reached from the keyboard and not with the mouse at all. The wheel moves
  // the strip, as it does a browser's own. A trackpad already scrolls it
  // sideways and is left to.
  $("tabs").addEventListener("wheel", (e) => {
    const strip = $("tabs");
    if (strip.scrollWidth <= strip.clientWidth || Math.abs(e.deltaX) >= Math.abs(e.deltaY)) return;
    e.preventDefault();
    strip.scrollLeft += e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
  }, { passive: false });
  $("btn-broadcast").onclick = () => send({ cmd: "toggleBroadcast" });
  $("btn-worktrees").onclick = () => openWorktrees();
  $("btn-github").onclick = () => openGithub();
  $("btn-history").onclick = openHistory;
  $("btn-changes").onclick = () => openChanges();
  $("summary").onclick = openAgents;
  // Bound through a closure rather than passed straight in: the click event
  // would otherwise arrive as the page to open.
  $("btn-help").onclick = () => openHelp();
  $("btn-settings").onclick = () => openSettings();
  $("btn-update").onclick = () => openUpdate();
  $("btn-remote").onclick = () => openRemote();
  $("overlay-close").onclick = closeOverlay;
  $("overlay").addEventListener("mousedown", (e) => { if (e.target === $("overlay")) closeOverlay(); });
  $("prompt-send").onclick = submitPrompt;
  $("prompt-input").onkeydown = promptKey;
  // The field holds several lines, so an instruction pasted in several -
  // "1. add tests", "2. run them" - keeps them, and the field grows to show
  // them. A pane whose program takes pasted text receives them as one
  // message; see SendPrompt.
  $("prompt-input").addEventListener("input", fitPrompt);
  $("prompt-cancel").onclick = closePrompt;
  $("retry").onclick = connectControl;
  $("palette-input").oninput = () => { palIndex = 0; renderPalette(); };
  $("palette").addEventListener("mousedown", (e) => { if (e.target === $("palette")) closePalette(); });
  // The arrows hand the keyboard back to the field, where a search is refined
  // by typing more of it; left on the button, the next letters went nowhere.
  $("search-next").onclick = () => { runSearch(false); $("search-input").focus(); };
  $("search-prev").onclick = () => { runSearch(true); $("search-input").focus(); };
  $("search-close").onclick = closeSearch;
  // The search follows the typing, as it does in any terminal or editor:
  // waiting for Enter left the field saying nothing about whether the word
  // was there at all until it had been typed in full and sent. A short pause
  // first, so a long scrollback is not searched once for every letter.
  let searchTimer = 0;
  $("search-input").oninput = () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => runSearch(false, true), 120);
  };
  $("notice").onclick = hideNotice;
  $("notice").addEventListener("pointermove", holdNotice);
  $("notice").addEventListener("pointerleave", () => {
    noticeHeld = false;
    if ($("notice").hidden) return;
    clearTimeout(notice.timer);
    notice.timer = setTimeout(hideNotice, NOTICE_AFTER_MS);
  });
  // A link out of the application - the help has one, to where Claude Code is
  // installed from - followed in place replaces the application with the page
  // it points to. This is an app window, with no address bar and no back
  // button, so the agents were left running with nothing on screen to reach
  // them by. A link like that opens a window of its own instead.
  document.addEventListener("click", (e) => {
    const a = e.target.closest && e.target.closest("a[href]");
    if (!a || a.target === "_blank" || !/^https?:/i.test(a.getAttribute("href"))) return;
    e.preventDefault();
    openExternal(a.getAttribute("href"));
  });

  /** openExternal opens a web address in a window of its own, and nothing
   *  that is not one: a terminal's link can name any scheme at all. */
  function openExternal(url) {
    if (!/^https?:/i.test(String(url || ""))) return;
    window.open(url, "_blank", "noopener,noreferrer");
  }
  // Asking on the first interaction rather than at load avoids a permission
  // prompt before the user has done anything. A keystroke is an interaction as
  // much as a click, and this is an application built to be driven from the
  // keyboard: waiting for a pointer meant someone who never reaches for one was
  // never asked, and so never got the one signal that reaches them while the
  // window is behind something else.
  const askOnFirstUse = () => {
    window.removeEventListener("pointerdown", askOnFirstUse, true);
    window.removeEventListener("keydown", askOnFirstUse, true);
    askForNotifications();
  };
  window.addEventListener("pointerdown", askOnFirstUse, true);
  window.addEventListener("keydown", askOnFirstUse, true);

  window.addEventListener("beforeunload", () => send({ cmd: "save" }));

  scheduleTipRotate();
  connectControl();
})();
