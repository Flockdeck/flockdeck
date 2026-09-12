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
    // Not what is typed into a terminal: nothing mirrors that. It is the
    // prompt bar's message that goes to the set, and this used to say
    // otherwise.
    broadcast:   "Sends what you write in the prompt bar to every pane in the broadcast set, so one instruction reaches them all. Typing in a terminal still reaches only that terminal.",
    fanOut:      "Turns the plan this agent proposed into a set of agents that carry it out, one pane each.",
    restart:     "Relaunches the process in this pane. A Claude agent resumes the same conversation.",
    zoom:        "Fills the tab with this pane. Zoom again to bring the other panes back. Double-clicking the pane's header does the same.",
    close:       "Closes this pane and stops the process running in it.",
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
  // Arriving from the keyboard counts as asking too. The bubble was the only
  // place most of the glyph buttons - ⑂, ⇉, ⟳, ⤢ - say what they do, and it
  // came only for a pointer: a screen reader has their names, but somebody
  // who can see and is tabbing through the window was told nothing at all.
  // Only for keyboard focus, so a click does not leave a bubble behind it.
  document.addEventListener("focusin", (e) => {
    let keyboard = true;
    try { keyboard = e.target.matches(":focus-visible"); } catch { /* no such selector here */ }
    const node = keyboard ? tipFind(e.target) : null;
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
   *  Locally that is always "/", but through the relay the page lives under
   *  the machine's own prefix — /h/<machine>/ — and everything it asks for has
   *  to be asked for there, or it reaches the relay's own routes instead. So
   *  nothing here names a path from the root: every URL is built on this. */
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
      $("disconnected").hidden = true;
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
      else if (msg.type === "prefs") { prefs = msg.prefs || prefs; applyPrefs(); renderHints(); }
      // A button that went with its worktree leaves the keyboard on the list
      // - the first row's first button, which removes nothing - rather than
      // out of the dialog.
      else if (msg.type === "worktrees") {
        keepFocus(() => renderWorktrees(msg), (body) => {
          const bar = body.querySelector("div.wt-actions");
          return bar && bar.querySelector("button");
        });
      }
      else if (msg.type === "recents") { recents = msg.items || []; if (dialog === "projects") keepFocus(renderProjects); }
      else if (msg.type === "browse") { browseState = msg; browseDraft = null; if (dialog === "projects") keepFocus(renderProjects, "button.dir-into"); }
      else if (msg.type === "conversations") keepFocus(() => renderHistory(msg));
      else if (msg.type === "changes") keepFocus(() => renderChanges(msg));
      else if (msg.type === "agents") keepFocus(() => renderAgents(msg));
      else if (msg.type === "keys") keepFocus(() => renderKeys(msg));
      else if (msg.type === "remoteDevices") { remoteRoster = msg; if (dialog === "remote") keepFocus(renderRemote); }
      else if (msg.type === "remotePair") { remotePairing = msg; if (dialog === "remote") keepFocus(renderRemote); }
      else if (msg.type === "fanoutPreview") renderFanout(msg);
      else if (msg.type === "diff") showDiff(msg);
      else if (msg.type === "detached") {
        // The agents keep running; this window is no longer needed.
        detaching = true;
        window.close();
      }
      else if (msg.type === "notice") { commitAnswered(msg.error); notice(msg.text, msg.error); }
    };
    ws.onclose = () => {
      if (control !== ws) return; // an attempt that was given up on
      if (detaching) return; // the window is on its way out
      $("disconnected").hidden = false;
      // Nothing behind this can be used and the terminal it is covering has
      // the keyboard, so typing would go nowhere until the pointer was used.
      $("retry").focus();
      reconnectTimer = setTimeout(connectControl, 1500);
    };
    ws.onerror = () => ws.close();
  }

  /** renderUpdate shows the chip when a release has been downloaded.
   *
   *  It is keyed on the version so the chip is not rebuilt on every snapshot,
   *  which arrive several times a second while agents are working. */
  let updateShown = null;
  function renderUpdate(s) {
    const u = s.update || null;
    const key = u ? u.version : "";
    if (key === updateShown) return;
    updateShown = key;
    const b = $("btn-update");
    if (!u) { b.hidden = true; return; }
    b.textContent = "Update " + u.version;
    describe(b, "Version " + u.version + " has been downloaded and is ready to install");
    b.hidden = false;
  }

  /** openUpdate explains what installing costs before it is done.
   *
   *  What it costs is the running agents: the layout comes back, but a pane is
   *  a live process and restarting stops it. Saying so here is the difference
   *  between a restart the user chose and one they regret. */
  function openUpdate() {
    const u = state && state.update;
    if (!u) return;
    // Named like every other dialog, so that the one it replaced — whose answer
    // may still be on its way — no longer thinks the panel is its own.
    dialog = "update";
    // The ? every other dialog has: the page on the command line covers
    // updating, and how to stop the checks.
    openOverlay("Update to " + u.version, "cli");
    const body = $("overlay-body");
    body.append(el("p", "", "This version has been downloaded and checked against its published checksum. Installing it saves and reopens your layout, but the agents running in panes are stopped."));
    if (u.notes) body.append(el("pre", "update-notes", u.notes));
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
    go.focus();
  }

  // ----------------------------------------------------------------- remote

  /** What the remote access dialog last heard: the account's devices and
   *  machines, and the pairing link it is showing, if any. Both are dropped
   *  whenever the dialog is opened again, because a pairing link that has
   *  been shown once is not one to leave lying about on the screen. */
  let remoteRoster = null;
  let remotePairing = null;
  let remoteChipKey = null;

  /** renderRemoteChip shows the Remote chip on an enrolled machine, and keeps
   *  the dialog's status line current while it is open. Like the update chip
   *  it is keyed on what it shows, so a snapshot that says nothing new about
   *  the tunnel does not rebuild it. */
  function renderRemoteChip(s) {
    const r = s.remote || null;
    const key = r ? [r.state, r.viewers, r.detail, r.relay].join("|") : "";
    if (key === remoteChipKey) return;
    remoteChipKey = key;
    const b = $("btn-remote");
    if (!r) {
      b.hidden = true;
    } else {
      // Amber only when it needs somebody, which is what amber means
      // everywhere else here. A working tunnel is a plain chip, and the count
      // says whether anybody is using it.
      b.textContent = r.viewers > 0 ? "Remote · " + r.viewers : "Remote";
      b.classList.toggle("trouble", r.state === "error" || r.state === "revoked" || r.state === "replaced");
      b.classList.toggle("pending", r.state === "connecting");
      describe(b, remoteSummary(r));
      b.hidden = false;
    }
    if (dialog === "remote") keepFocus(renderRemote);
  }

  /** remoteSummary says in a sentence where the tunnel stands. */
  function remoteSummary(r) {
    const where = String(r.relay || "the relay").replace(/^https?:\/\//, "");
    switch (r.state) {
      case "connected": {
        const n = r.viewers || 0;
        return "Reachable through " + where +
          (n ? " — " + n + (n === 1 ? " window is" : " windows are") + " open from another device" : "");
      }
      case "connecting": return "Connecting to " + where + "…";
      case "error": return "Cannot reach " + where + (r.detail ? ": " + r.detail : "") + ". Trying again shortly.";
      case "revoked":
      case "replaced": return r.detail || "Remote access has stopped.";
      default: return "Not connected to " + where + ".";
    }
  }

  /** openRemote shows remote access: whether the relay can be reached, a way
   *  to pair a device, and what is paired already. */
  function openRemote() {
    dialog = "remote";
    remoteRoster = null;
    remotePairing = null;
    openOverlay("Remote access", "remote");
    renderRemote();
    send({ cmd: "remoteDevices" });
  }

  function renderRemote() {
    if (dialog !== "remote") return;
    const body = $("overlay-body");
    body.textContent = "";
    const r = state && state.remote;
    // Enrolling is a terminal command on purpose — it decides where the
    // traffic goes — so a machine that is not enrolled is told how, rather
    // than offered a button that would have to guess which relay.
    if (!r && remoteRoster && !remoteRoster.enabled) {
      body.append(el("div", "fan-hint",
        "Remote access opens this window from another device — a laptop, a tablet, a phone — " +
        "through a relay, without opening a port on this machine."));
      body.append(el("p", null, "This machine is not enrolled. To turn it on, run this in a terminal:"));
      body.append(el("pre", "remote-cmd", "flockdeck remote enable"));
      return;
    }
    if (r) body.append(el("div", "remote-status " + r.state, remoteSummary(r)));

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
    go.onclick = () => {
      remotePairing = { pending: true };
      renderRemote();
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
      const drop = el("button", "chip danger", "Unpair");
      drop.onclick = () => {
        const q = mine
          ? "Unpair this device? This window will close, and it will need a new pairing link to come back."
          : "Unpair " + (d.name || "this device") + "? Any window it has open will close.";
        if (!window.confirm(q)) return;
        send({ cmd: "remoteRevoke", id: d.id });
      };
      actions.append(drop);
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
        hw.append(item);
      });
      body.append(hw);
    }
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
      }
    }
    const rebuilt = rebuildChangedTabs(s);
    applyWeights(s);
    showActiveTab(s, rebuilt);
    renderTabs(s);
    renderSummary(s);
    followAgents(s);
    followProjects(s);
    followWorktrees(s);
    followChanges(s);
    renderUpdate(s);
    renderRemoteChip(s);
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
      const shape = shapeOf(tab);
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

  function renderTabs(s) {
    // Rebuilding the strip would destroy the element a drag is holding, and
    // the drag would end nowhere. Status pushes arrive constantly, so this is
    // not a rare case; the bar catches up when the drag finishes.
    if (dragging && dragging.kind === "tab") return;
    const bar = $("tabs");
    s.tabs.forEach((tab, i) => {
      const node = tabNode(tab.id);
      const title = tab.title || "tab " + (i + 1);
      if (node.label.textContent !== title) {
        node.label.textContent = title;
        // A title is cut short at the tab's width, and the ones written from
        // an agent's task usually are. The bubble is the only place the rest
        // can be read, and the only thing saying how to change it.
        describe(node.btn, title + " — double-click to rename");
      }
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
    btn.id = "tab-" + id;
    btn.setAttribute("aria-controls", "page-" + id);
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
    waiting: basePath + "assets/icon-waiting.svg",
    working: basePath + "assets/icon.svg",
    idle: basePath + "assets/icon-idle.svg",
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
        ? `▲ ${s.waiting} waiting · flockdeck`
        : (s.working > 0 ? `● ${s.working} working · flockdeck` : "flockdeck");
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
    // Highlight when another project needs attention, so switching away does
    // not hide the fact that an agent there is blocked - and say which, since
    // the amber on its own says only that something somewhere wants you.
    const elsewhere = (s.projects || []).filter((p) => !p.active && p.waiting > 0);
    const why = elsewhere.length ? "waiting on you in " + elsewhere.map((p) => p.name).join(", ") : "";
    const tip = actionTip("projects", [active ? active.root : "", why].filter(Boolean).join("; "));
    if (btn.dataset.tip !== tip) describe(btn, tip);
    btn.classList.toggle("attention", elsewhere.length > 0);
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
    const detail = el("span", "pane-detail");
    const git = el("span", "pane-git");
    const usage = el("span", "pane-usage");
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
    actions.append(
      btn("⑂", TIPS.fanOut, () => openFanout(id)),
      castBtn,
      btn("⟳", TIPS.restart, () => send({ cmd: "restartPane", id })),
      zoomBtn,
      btn("×", TIPS.close, () => send({ cmd: "closePane", id })),
    );
    makeToolbar(actions, "What to do with this pane");
    header.append(dot, project, name, branch, agent, git, detail, usage, cast, actions);

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
      if (ev.target.closest && ev.target.closest("button")) return;
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
      fontFamily: terminalFont(),
      fontSize: fontSize,
      lineHeight: 1.15,
      scrollback: scrollback,
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
    try {
      const webgl = new WebglAddon.WebglAddon();
      webgl.onContextLoss(() => webgl.dispose());
      term.loadAddon(webgl);
    } catch {
      /* Canvas rendering is a fine fallback. */
    }

    p = { id, wrap, header, dot, name, project, branch, agent, git, detail, usage, cast, body, host, term, fit, ws: null,
          nodeId: "", fitTimer: 0, retryTimer: 0, retries: 0, cols: 0, rows: 0, actions, castBtn, zoomBtn, search, dropZone,
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

  function sendInput(p, data) {
    sendBytes(p, new TextEncoder().encode(data));
  }
  function sendBytes(p, bytes) {
    if (p.ws && p.ws.readyState === WebSocket.OPEN) p.ws.send(bytes);
  }

  function connectPTY(p) {
    if (p.ws) { try { p.ws.close(); } catch {} }
    clearTimeout(p.retryTimer);

    const ws = new WebSocket(wsBase + basePath + "ws/pty?id=" + encodeURIComponent(p.id));
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
      zoomed.set(t.focus, ids.size - 1);
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
      if (was.agent !== agent) { was.agent = agent; renderPaneAgent(p, v); }

      const detail = v.detail || "";
      if (was.detail !== detail) { was.detail = detail; p.detail.textContent = detail; }

      const git = [v.dirty, v.untracked, v.ahead, v.behind].join(" ");
      if (was.git !== git) { was.git = git; renderPaneGit(p, v); }

      // Keyed on what is actually displayed — the processor figure is drawn
      // rounded — so a pane whose usage is merely jittering does not redraw
      // its header on every sample.
      const usage = (v.procs || 0) + " " + Math.round(v.cpu || 0) + " " + (v.rss || 0);
      if (was.usage !== usage) { was.usage = usage; renderPaneUsage(p, v); }

      const cast = (v.broadcast ? "1" : "0") + (s.broadcast ? "1" : "0");
      if (was.cast !== cast) { was.cast = cast; renderPaneCast(p, v, s); }

      const zoom = zoomed.has(id) ? String(zoomed.get(id)) : "";
      if (was.zoom !== zoom) { was.zoom = zoom; renderPaneZoom(p, zoomed.get(id)); }

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

  /** renderPaneAgent names what is running in the pane: the agent, and the
   *  model it was asked for. It sits beside the branch and reads like it,
   *  because it answers the same sort of question — which of these six panes
   *  is the one I want. A shell has neither and shows nothing. */
  function renderPaneAgent(p, v) {
    p.agent.textContent = "";
    if (!v.agent) { delete p.agent.dataset.tip; return; }
    p.agent.textContent = v.model ? v.agent + " · " + v.model : v.agent;
    describe(p.agent, TIPS.agent);
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

  /** formatBytes writes a size the way a person reads one, to three
   *  significant figures at most so the header does not jitter as the last
   *  digit moves. */
  function formatBytes(n) {
    const units = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return (n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)) + " " + units[i];
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

  function promptKey(ev) {
    const input = $("prompt-input");
    if (ev.key === "ArrowUp" && promptAt > 0) {
      if (promptAt === promptHistory.length) promptDraft = input.value;
      input.value = promptHistory[--promptAt];
    } else if (ev.key === "ArrowDown" && promptAt < promptHistory.length) {
      promptAt++;
      input.value = promptAt === promptHistory.length ? promptDraft : promptHistory[promptAt];
    } else {
      return;
    }
    ev.preventDefault();
    input.setSelectionRange(input.value.length, input.value.length);
  }

  function openPrompt() {
    const bar = $("promptbar");
    bar.hidden = false;
    promptAt = promptHistory.length;
    promptDraft = "";
    // Whether or not broadcast is on: the message goes to the focused pane and
    // every pane in the set, and a pane picked by hand with ⇉ stays in the set
    // with broadcast off. Counting only while it was on said "Prompt" over a
    // message about to reach three agents.
    const n = state ? countBroadcast(state) : 1;
    $("prompt-label").textContent = n > 1 ? `Prompt → ${n} panes` : "Prompt";
    const input = $("prompt-input");
    input.value = promptUnsent;
    input.focus();
  }
  function closePrompt() {
    promptUnsent = $("prompt-input").value;
    $("promptbar").hidden = true;
    focusTerminal();
  }
  function submitPrompt() {
    const text = $("prompt-input").value;
    if (text.trim()) {
      send({ cmd: "sendPrompt", text });
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
  function keepFocus(draw, fallback) {
    const body = $("overlay-body");
    const was = document.activeElement;
    const key = was && body.contains(was) ? identify(was) : "";
    const at = was && was.selectionStart;
    // Emptying the body to draw it again takes its scroll position with it, so
    // a list scrolled to its twentieth worktree jumped back to the first when
    // one was removed, or when a refresh came back.
    const top = body.scrollTop;
    draw();
    body.scrollTop = top;
    if (!key) return;
    for (const node of body.querySelectorAll("button, input, textarea, select, [tabindex]")) {
      if (identify(node) !== key) continue;
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

  /** identify is what makes a control the same control across a redraw. An id
   *  is that on its own; otherwise it is what the control is and what it says,
   *  which is how a person finds it again too. A control whose wording changes
   *  while it is working needs the id. */
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
    else if (dialog === "projects") {
      send({ cmd: "recents" });
      send({ cmd: "browse", path: browseState ? browseState.path : "" });
    }
  }

  function closeOverlay() {
    picker = null;
    $("overlay").hidden = true;
    $("overlay-panel").classList.remove("wide");
    dialog = null;
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
      // Cut from the end with an ellipsis, and paths differ at the end.
      main.append(describe(el("div", "wt-path", wt.path), wt.path));
      row.append(main);

      const actions = el("div", "wt-actions");
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
      // Named by the worktree they act on. A redraw finds the control the
      // keyboard was on by what it is, and by wording alone "Remove" in one
      // row is "Remove" in the next: removing a clean worktree - which does
      // not ask - left the keyboard on the next one's Remove, and a second
      // Enter removed that as well.
      const key = "wt-" + encodeURIComponent(wt.path) + "-";
      agent.id = key + "agent"; shell.id = key + "shell"; split.id = key + "split"; review.id = key + "review";

      if (!wt.main) {
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
      if (!branch.value.trim()) return;
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
    tools.append(refresh, prune, el("span", "wt-root", m.root || ""));
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

  /** The projects dialog's Open section is drawn from the state, and was
   *  drawn only when a recents or folder listing arrived: closing a project
   *  from it - which answers with a state push and nothing else - left the
   *  project listed as open, with a close button that did nothing, and an
   *  agent starting to wait in another project did not show. It follows the
   *  pushes that change what it lists, and no others. */
  let projectsKey = "";
  function followProjects(s) {
    const key = (s.projects || []).map((p) => [p.root, p.active, p.tabs, p.waiting, p.working].join(":")).join("|");
    if (key === projectsKey) return;
    projectsKey = key;
    if (dialog === "projects") keepFocus(renderProjects);
  }

  function openProjects() {
    dialog = "projects";
    browseDraft = null;
    recentsAll = false;
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
      row.dataset.key = "open:" + p.root;
      // The row's own action — switch to this project — is a real button
      // wrapping everything that describes it, rather than a click handler on
      // the row. A handler on a div cannot be tabbed to and does not answer
      // Enter, and the icon buttons beside it could not have been nested
      // inside something that claimed to be a button itself.
      const go = el("button", "proj-go");
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
          send({ cmd: "closeProject", root: p.root });
        };
        row.append(close);
      }
      openSection.append(row);
    });
    body.append(openSection);

    // Recent projects that are not already open.
    const notOpen = recents.filter((r) => !r.open);
    if (notOpen.length) {
      const rec = section("Recent");
      const shown = recentsAll ? notOpen : notOpen.slice(0, 8);
      shown.forEach((r, i) => {
        const row = el("div", "proj-row" + (r.exists ? "" : " missing"));
        row.dataset.key = "recent:" + r.root;
        const main = el("span", "proj-main");
        main.append(el("span", "proj-name", r.name));
        main.append(describe(el("span", "proj-path", r.exists ? r.root : r.root + "  (missing)"), r.root));
        // A project whose folder has gone cannot be opened, so it stays text
        // rather than becoming a button that does nothing when it is pressed.
        if (r.exists) {
          const go = el("button", "proj-go");
          go.id = "recent-" + i;
          go.append(main);
          go.onclick = () => { send({ cmd: "openProject", path: r.root }); closeOverlay(); };
          row.append(go);
        } else {
          row.append(main);
        }
        const forget = el("button", "icon-btn", "\u00d7");
        describe(forget, "Drop this project from the recent list. Nothing on disk is touched.");
        forget.onclick = () => send({ cmd: "forgetRecent", root: r.root });
        row.append(forget);
        rec.append(row);
      });
      // Eight at first, and the rest - up to forty are remembered - simply
      // were not there, so an older project could be reached only by browsing
      // to its folder again.
      if (shown.length < notOpen.length) {
        const more = el("button", "chip", "Show " + (notOpen.length - shown.length) + " more");
        more.onclick = () => {
          recentsAll = true;
          renderProjects();
          const next = $("recent-" + shown.length);
          if (next) next.focus();
        };
        rec.append(more);
      }
      body.append(rec);
    }

    body.append(renderBrowser());
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
      const rows = [...row.parentElement.children].filter((n) => n.getAttribute("role") === "button");
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
    applyFontSize(px);
    send({ cmd: "fontSize", size: fontSize });
    notice("Font size " + fontSize + "px", false);
  }

  /** applyPrefs puts into effect the preferences that change how the
   *  terminals behave. They arrive before the first pane is drawn, and again
   *  whenever another window changes one. */
  function applyPrefs() {
    applyFontSize(prefs.fontSize);
    applyScrollback(prefs.scrollback);
    applyCursorBlink();
    applyFontFamily();
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

  /** askFontFamily asks which typeface the terminals should use. It was
   *  fixed in the source, and a terminal's font is among the first things
   *  anybody who works in one sets to their own. */
  function askFontFamily() {
    const answer = window.prompt("Which font should the terminals use? Leave it empty for the default.", prefs.fontFamily || "");
    if (answer === null || answer === undefined) return;
    const family = String(answer).trim();
    prefs.fontFamily = family;
    applyFontFamily();
    send({ cmd: "fontFamily", text: family });
    notice(family ? "The terminals now use " + family : "The terminals use the default font again", false);
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
    applyScrollback(n);
    send({ cmd: "scrollback", size: n });
    notice("Each terminal now keeps " + n.toLocaleString("en") + " lines", false);
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
    searchPane = focusedPaneId();
    $("searchbar").hidden = false;
    // With six panes on screen, "Find" alone does not say where it is looking.
    const v = state && state.panes ? state.panes[searchPane] : null;
    $("search-label").textContent = v && v.name ? "Find in " + v.name : "Find";
    const input = $("search-input");
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

  function notifyAttention(s) {
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
      showNotification(
        one ? fresh[0].name + " needs you" : fresh.length + " agents need you",
        one ? (fresh[0].branch ? fresh[0].branch + " — " : "") + "waiting for input"
            : fresh.map((f) => f.name).join(", "),
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
      const n = new Notification(title, { body, tag: "flockdeck" });
      n.onclick = () => {
        window.focus();
        // Raising the window in front of whichever tab happened to be on
        // screen does not answer the question. The pane that asked it does,
        // and it may well be on a tab that is not the one showing.
        if (paneID) send({ cmd: "revealPane", node: tabIdOfPane(paneID), id: paneID });
        else if (root) send({ cmd: "selectProject", root });
        n.close();
      };
    } catch { /* notifications are best effort */ }
  }

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

    toggleBroadcast: () => send({ cmd: "toggleBroadcast" }),
    promptAll: () => openPrompt(),
    fanout: () => openFanout(),
    agents: () => openAgents(),
    apiKeys: () => openKeys(),

    worktrees: () => openWorktrees(),
    changes: () => openChanges(),

    palette: () => openPalette(),
    findInTerminal: () => openSearch(),
    history: () => openHistory(),
    projects: () => openProjects(),
    help: () => openHelp(),

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
    keyTable = msg.keys || [];
    prefs = msg.prefs || prefs;
    applyPrefs();
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
    label("newAgentTabChoose", $("new-tab-pick"));
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
    // on it. There the binding can only mean the physical key.
    if (/^[^\x00-\x7f]$/.test(key)) {
      const m = /^Key([A-Z])$/.exec(e.code || "");
      if (m) key = m[1].toLowerCase();
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
    const SETTINGS = new Set(["fontUp", "fontDown", "fontReset", "scrollback", "fontFamily", "apiKeys"]);
    const also = (id) => SETTINGS.has(id) ? SETTING : "";
    const cmds = keyTable
      .filter((k) => !k.noPalette && ACTIONS[k.id])
      .map((k) => ({ label: k.label, hint: k.keys, also: also(k.id), run: () => runAction(k.id) }));
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
      cmds.push({ label: label, also: also(id), run: () => runAction(id) });
    });
    // A setting kept as "off": the entry turns it back on while it is off and
    // off while it is on, and says which it did.
    const toggle = (off, cmd, [turnOn, turnOff], [isOn, isOff]) => cmds.push({
      label: off ? turnOn : turnOff,
      also: SETTING,
      run: () => { send({ cmd, kind: off ? "on" : "off" }); notice(off ? isOn : isOff, false); },
    });
    // Desktop notifications reach past the window, and the only way to stop
    // them was the browser's own permission - which belongs to the page's
    // origin, changes with the port on every run, and so was asked again, and
    // had to be refused again, every time the application started.
    toggle(!!prefs.notificationsOff, "notifications",
      ["Turn desktop notifications on", "Turn desktop notifications off"],
      ["Desktop notifications are on", "Desktop notifications are off"]);
    // The cursor's blink was fixed in the source, and one blinking cursor
    // among a tab of still terminals is a distraction some people want gone.
    toggle(!!prefs.cursorSteady, "cursorBlink",
      ["Make the terminal cursor blink", "Stop the terminal cursor blinking"],
      ["The terminal cursor blinks", "The terminal cursor is steady"]);
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
    toggle(!!prefs.updatesOff, "updates",
      ["Turn update checks on", "Turn update checks off"],
      ["Flockdeck will check for new releases", "Flockdeck will not check for new releases"]);
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
    if (dialog !== "history") return; // see openWorktrees
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
          send({ cmd: "resumeConversation", id: c.id, path: m.cwd, text: c.summary });
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
    send({ cmd: "changes", path: changes.cwd });
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
    const was = changeView && { file: selectedFile, list: changeView.list.scrollTop, diff: changeView.diff.scrollTop };
    changeView = null;
    if (msg) {
      changes = msg;
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
      if (m.behind) {
        const pull = el("button", "chip", "Pull " + m.behind);
        pull.id = "rev-pull";
        pull.title = "Fast-forward from " + m.upstream;
        pull.onclick = () => { running(pull, "Pulling…"); send({ cmd: "gitPull", path: m.cwd }); };
        actions.append(pull);
      }
      const push = el("button", "chip" + (m.ahead ? " primary" : ""), m.ahead ? "Push " + m.ahead : "Push");
      push.id = "rev-push";
      push.title = m.upstream ? "Push to " + m.upstream : "Push and set the upstream to origin";
      push.onclick = () => { running(push, "Pushing…"); send({ cmd: "gitPush", path: m.cwd }); };
      actions.append(push);
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
        rowAction(row, () => selectChangedFile(f.path, m.cwd), true, f.path.replace(/^.*[\\/]/, ""));
        rows.set(f.path, row);
        list.append(row);
      });
      split.append(list);

      const diff = el("div", "rev-diff");
      split.append(diff);
      body.append(split);
      changeView = { rows, diff, list };
      fillDiff();
      // A refresh, a fetch or a push answers with the working tree again, and
      // drawing it afresh put a long diff back at its first line under the
      // person reading it, and the file list back at its top.
      if (was && was.file === selectedFile) { list.scrollTop = was.list; diff.scrollTop = was.diff; }

      // --- commit -----------------------------------------------------------
      const commit = el("div", "rev-commit");
      const box = el("textarea");
      box.id = "commit-message";
      box.placeholder = "Commit message (Ctrl+Enter commits)";
      box.value = commitDraft;
      box.oninput = () => { commitDraft = box.value; };
      const buttons = el("div", "rev-commit-buttons");
      const doCommit = (btn, push) => {
        const message = box.value.trim();
        if (!message) { notice("A commit message is required", true); box.focus(); return; }
        running(btn, push ? "Committing and pushing…" : "Committing…");
        send({ cmd: "commit", path: m.cwd, text: message, push });
        commitPending = true;
      };
      const c1 = el("button", "chip primary", "Commit " + files.length + " file" + (files.length === 1 ? "" : "s"));
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
      if (m.hasRemote) {
        const c2 = el("button", "chip", "Commit and push");
        c2.id = "rev-commit-push";
        c2.onclick = () => doCommit(c2, true);
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
  /** Whether a commit has been asked for and not yet answered. The message
   *  stays in the box until it has: a commit that fails - a hook refusing it,
   *  an identity git has not been given - answers by redrawing the dialog, and
   *  clearing the draft when the commit was sent meant the message went with
   *  the redraw and had to be written again. The server's first word on a
   *  commit is a notice saying whether it was made. */
  let commitPending = false;

  function commitAnswered(isError) {
    if (!commitPending) return;
    commitPending = false;
    if (!isError) commitDraft = "";
  }

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
  function fillDiff(keep) {
    if (!changeView) return;
    const diff = changeView.diff;
    const top = diff.scrollTop;
    diff.textContent = "";
    if (!selectedFile) diff.append(el("span", "meta", "Select a file to see what changed."));
    else if (!diffText) diff.append(el("span", "meta", "Loading diff…"));
    else renderDiffInto(diff, diffText);
    diff.scrollTop = keep ? top : 0;
  }

  function showDiff(msg) {
    if (msg.file !== selectedFile) return; // a stale reply for another file
    // A diff already on show is being read again, and the reader's place in
    // it is kept; a file just chosen starts at its top.
    const again = !!diffText;
    diffText = msg.error ? msg.error : msg.text;
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
    const shown = Math.min(lines.length, DIFF_LINES);
    for (let i = 0; i < shown; i++) host.append(diffLine(lines[i]));
    if (shown === lines.length) return;

    const rest = lines.length - shown;
    const note = el("div", "meta", rest + " more lines are not drawn.");
    const more = el("button", "chip", "Show them");
    more.onclick = () => {
      note.remove();
      more.remove();
      for (let i = shown; i < lines.length; i++) host.append(diffLine(lines[i]));
    };
    host.append(note, more);
  }

  function diffLine(line) {
    let cls = "";
    if (line.startsWith("+++") || line.startsWith("---")) cls = "meta";
    else if (line.startsWith("@@")) cls = "hunk";
    else if (line.startsWith("+")) cls = "add";
    else if (line.startsWith("-")) cls = "del";
    else if (line.startsWith("diff ") || line.startsWith("index ")) cls = "meta";
    return el("div", cls, line || " ");
  }

  // ---------------------------------------------------------------- agents

  let agents = null;

  /** The overview is where you look to see which agent needs you, and it was
   *  a picture taken when it opened: an agent that stopped to wait while it
   *  was up went on being listed as working. It is asked for again whenever a
   *  push shows the counts it lists moving, in any project. */
  let agentsKey = "";
  function followAgents(s) {
    const key = (s.projects || []).map((p) => p.root + ":" + p.waiting + ":" + p.working + ":" + p.tabs).join("|");
    if (key === agentsKey) return;
    agentsKey = key;
    if (dialog === "agents") send({ cmd: "agents" });
  }

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
    if (dialog !== "agents") return; // see openWorktrees
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
      main.append(meta);
      row.append(main);

      const status = el("span", "agent-status " + a.status, a.status);
      row.append(status);
      if (a.for) row.append(el("span", "agent-for", "for " + a.for));

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

  function chooseAgent(title, onPick) {
    picker = { title, onPick, query: "", index: 0, open: new Set(), setDefault: false, scope: "" };
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
      const hay = (a.id + " " + a.name + " " + (a.models || []).map((m) => m.id + " " + (m.name || "")).join(" ")).toLowerCase();
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

  function renderAgentPicker() {
    if (dialog !== "agentPicker" || !picker) return;
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
      const row = item.type === "agent" ? pickerAgentRow(item.agent) : pickerModelRow(item.agent, item.model);
      row.id = "pick-row-" + pickRows.length;
      row.setAttribute("role", "option");
      const at = pickRows.length;
      row.onmousemove = () => selectPickerRow(at); // as in the palette
      row.onclick = () => { picker.index = at; takePickerRow(); };
      row.item = item;
      pickRows.push(row);
      list.append(row);
    });
    if (picker.index >= pickRows.length) picker.index = Math.max(0, pickRows.length - 1);
    markPickerRow();
  }

  function pickerAgentRow(a) {
    const row = el("div", "pick-row agent" + (a.available ? "" : " unavailable"));
    const open = picker.open.has(a.id);
    row.append(glyph(open ? "▾" : "▸"));
    row.append(el("span", "pick-name", a.name || a.id));
    if (markedAgent(a)) row.append(el("span", "pick-mark", "default"));
    if (!a.available) {
      const why = el("span", "pick-note", a.install || "not installed");
      row.append(describe(why, TIPS.notInstalled));
    } else if (a.models && a.models.length) {
      row.append(el("span", "pick-note", a.models.length + " models"));
    }
    row.setAttribute("aria-expanded", String(open));
    return row;
  }

  function pickerModelRow(a, m) {
    const row = el("div", "pick-row model");
    row.append(el("span", "pick-name", m.name || m.id || "Default"));
    if (markedModel(a, m)) row.append(el("span", "pick-mark", "default"));
    if (m.note) row.append(el("span", "pick-note", m.note));
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
    if (!a.available) {
      notice(a.name + " is not installed. " + (a.install || ""), true);
      return;
    }
    if (item.type === "agent" && (a.models || []).length && !picker.open.has(a.id)) {
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
      if (e.key === "ArrowRight" && item.type === "agent" && (item.agent.models || []).length) {
        e.preventDefault();
        picker.open.add(item.agent.id);
        drawPickerList();
        return;
      }
      if (e.key === "ArrowLeft") {
        e.preventDefault();
        picker.open.delete(item.agent.id);
        drawPickerList();
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

  function renderKeys(msg) {
    if (msg) apiKeys = msg;
    if (dialog !== "keys") return; // see openWorktrees
    const items = (apiKeys && apiKeys.items) || [];
    const body = $("overlay-body");
    body.textContent = "";

    body.append(el("div", "fan-hint",
      "Agents that talk to a model API need a key. One already exported in your " +
      "environment is used where it is; anything set here is kept in flockdeck's own " +
      "file, readable only by you, and reaches nothing but the pane that needs it."));

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

      const actions = el("div", "wt-actions");
      const set = el("button", "chip", k.set ? "Replace…" : "Set…");
      set.onclick = () => { keyEditing = k.agent; renderKeys(); };
      actions.append(set);
      // Only a stored key can be forgotten. A key that came from the
      // environment is the user's own arrangement and this dialog has no
      // business unsetting a variable it did not set.
      if (k.set && k.source === "store") {
        const clear = el("button", "chip danger", "Clear");
        clear.onclick = () => send({ cmd: "keyClear", id: k.agent });
        actions.append(clear);
      }
      row.append(actions);
      wrap.append(row);

      if (keyEditing === k.agent) wrap.append(keyForm(k));
    });
    body.append(wrap);

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

    const save = () => {
      const value = field.value.trim();
      field.value = "";
      keyEditing = null;
      if (!value) { renderKeys(); return; }
      send({ cmd: "keySet", id: k.agent, text: value });
      // Drawn again without waiting for the reply, so the field goes as
      // soon as it is sent rather than sitting there emptied.
      renderKeys();
    };
    const cancel = () => { field.value = ""; keyEditing = null; renderKeys(); };

    field.onkeydown = (ev) => {
      if (ev.key === "Enter") { ev.preventDefault(); save(); return; }
      // Escape closes the whole overlay everywhere else, which here would take
      // a half-typed key with it and leave the user wondering what was saved.
      if (ev.key === "Escape") { ev.preventDefault(); ev.stopPropagation(); cancel(); }
    };

    const ok = el("button", "chip primary", "Save");
    ok.onclick = save;
    const no = el("button", "chip", "Cancel");
    no.onclick = cancel;
    form.append(field, ok, no);
    return form;
  }

  // --------------------------------------------------------------- fan out

  let fanout = null;
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
    send({ cmd: "fanoutPreview", id: paneID || focusedPaneId() });
  }

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
        const name = mo.name || mo.id || "Default";
        const o = agentOption(mo.note ? name + " — " + mo.note : name, a.id + "\n" + (mo.id || ""));
        o.disabled = !!a.unavailable;
        group.append(o);
      });
      sel.append(group);
    });
    sel.value = value || "";
    // A choice naming an agent the catalog no longer has leaves the control
    // showing nothing at all, which reads as a control that is broken rather
    // than as one whose agent has gone.
    if (sel.selectedIndex < 0) sel.selectedIndex = 0;
    return sel;
  }

  /** pickParts splits an agentSelect value back into its agent and its model. */
  function pickParts(value) {
    const i = (value || "").indexOf("\n");
    return i < 0 ? ["", ""] : [value.slice(0, i), value.slice(i + 1)];
  }

  function renderFanout(msg) {
    if (msg) fanout = msg;
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
      const plural = lines.length === 1 ? "1 agent" : lines.length + " agents";
      if (!choosable) return plural;
      const runID = pickParts(runSel.value)[0];
      const byAgent = new Map();
      lines.forEach((task) => {
        const id = pickParts(overrides.get(task))[0] || runID;
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
      if (asksTrust(runID) && lines.some((t) => !pickParts(overrides.get(t))[0])) return true;
      return lines.some((t) => asksTrust(pickParts(overrides.get(t))[0]));
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
    };

    const renderRows = () => {
      rows.textContent = "";
      taskLines().forEach((task, i) => {
        const row = el("div", "fan-row");
        row.append(el("span", "fan-row-n", String(i + 1)));
        const text = el("span", "fan-row-task", task);
        text.title = task;
        row.append(text);
        const sel = agentSelect(catalog, overrides.get(task), "Same as the run");
        sel.onchange = () => {
          if (sel.value) overrides.set(task, sel.value);
          else overrides.delete(task);
          updateCount();
        };
        row.append(sel);
        rows.append(row);
      });
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
        const perTask = tasks.map((t) => pickParts(overrides.get(t))[0]);
        if (perTask.some(Boolean)) {
          req.taskAgents = perTask;
          req.taskModels = tasks.map((t) => pickParts(overrides.get(t))[1]);
        }
      }
      send(req);
      closeOverlay();
    };
    box.oninput = () => { if (choosable) renderRows(); updateCount(); };
    // Enter is a new line here - the tasks go one to a line - so starting
    // them took the pointer, or Tab past every option to the button.
    box.onkeydown = (ev) => {
      if (ev.key !== "Enter" || !(ev.ctrlKey || ev.metaKey)) return;
      ev.preventDefault();
      start.onclick();
    };
    if (choosable) {
      runSel.onchange = updateCount;
      renderRows();
    }
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
      // Only with an amber dot on screen to point at. The waiting count is
      // every project's, and an agent waiting in one not shown had this
      // explaining a dot that was nowhere to be seen.
      when: (s) => Object.values(s.panes || {}).some((v) => v.status === "waiting"),
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
    notice.timer = setTimeout(hideNotice, isError ? ERROR_MS : NOTICE_MS);
  }

  /** hideNotice takes the message away. It is also what a click on it does:
   *  the toast is drawn over the terminals and takes the pointer, so a click
   *  meant for the pane underneath was going nowhere at all. */
  function hideNotice() {
    clearTimeout(notice.timer);
    $("notice").hidden = true;
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
    if (e.key === "Tab" && trapTab(e)) return;
    if (!$("palette").hidden) { paletteKey(e); return; }
    // Anywhere in the bar, not only in its field: after a click on one of its
    // arrows the keyboard is on that button, and Escape did nothing there.
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
      if (f && (f.id === "help-search" || f.id === "agent-filter") && f.value) {
        e.preventDefault();
        f.value = "";
        f.dispatchEvent(new Event("input"));
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
      else if (e.key === "Enter" && document.activeElement === $("prompt-input")) { e.preventDefault(); submitPrompt(); }
      return;
    }

    // Every other binding is dispatched from the action table, so what the
    // help says a key does is what the key does.
    const id = actionFor(e);
    if (id && !editsText(e)) { claimKey(e); runAction(id); return; }

    // Alt+1 … Alt+9 names a tab rather than being one binding, so it is the
    // one thing the table cannot express and this has to spell out.
    // The digit row is read by position where it can be: on AZERTY it types
    // & é " ' and gives digits only with Shift held, so Alt+1 arrived as Alt+&
    // and no tab could be picked by number. The keypad has no position to
    // read and types its digits on every layout.
    const row = /^Digit([1-9])$/.exec(e.code || "");
    const n = row ? row[1] : (/^[1-9]$/.test(e.key || "") ? e.key : "");
    if (e.altKey && !e.ctrlKey && !e.shiftKey && n) {
      claimKey(e);
      runAction("selectTab", n);
    }
  }, true);

  // ------------------------------------------------------------------ wiring

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
  $("btn-history").onclick = openHistory;
  $("btn-changes").onclick = () => openChanges();
  $("summary").onclick = openAgents;
  $("project-btn").onclick = openProjects;
  // Bound through a closure rather than passed straight in: the click event
  // would otherwise arrive as the page to open.
  $("btn-help").onclick = () => openHelp();
  $("btn-update").onclick = () => openUpdate();
  $("btn-remote").onclick = () => openRemote();
  $("overlay-close").onclick = closeOverlay;
  $("overlay").addEventListener("mousedown", (e) => { if (e.target === $("overlay")) closeOverlay(); });
  $("prompt-send").onclick = submitPrompt;
  $("prompt-input").onkeydown = promptKey;
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
  // A link out of the application - the help has one, to where Claude Code is
  // installed from - followed in place replaces the application with the page
  // it points to. This is an app window, with no address bar and no back
  // button, so the agents were left running with nothing on screen to reach
  // them by. A link like that opens a window of its own instead.
  document.addEventListener("click", (e) => {
    const a = e.target.closest && e.target.closest("a[href]");
    if (!a || a.target === "_blank" || !/^https?:/i.test(a.getAttribute("href"))) return;
    e.preventDefault();
    window.open(a.getAttribute("href"), "_blank", "noopener,noreferrer");
  });
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

  connectControl();
})();
