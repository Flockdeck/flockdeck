// Telling the person an agent needs them, and telling the desktop the person is
// there.
//
// Two things use the browser's own notifications: a notification raised here when
// an agent stops to wait while this window is behind something else, and the
// push to paired devices, which the desktop sends and this page only sets. The
// two settings are separate: the first is the window's (notificationsOff), the
// second the account's (push). The desktop does not push to a phone while the
// person is at the desk, and says so by what it sees of the machine's input;
// where it cannot see that, it relies on the presence this page reports.
//
// A notification says the pane's name, its branch, and that it is waiting. It
// never carries what the agent asked: that is a permission prompt, which a window
// reached through the relay is not sent in full, and a notification can be read
// from a lock screen. The name and the branch are cleaned and cut (labelOf) before
// they are used, since an agent or a repository can set them.
//
// A structured pane (an agent driven over ACP) is in the same table: an open
// permission request is status "waiting", a pane that needs setup is "blocked",
// and both notify as a terminal pane does. A structured pane also tells when it
// finishes a turn (working, then idle): that is "finished" below, raised only while
// this window is not in front, and never counted as waiting.

/** PUSH_DELAYS are the waits offered before the devices are told, in seconds. */
export const PUSH_DELAYS = [[15, "15 seconds"], [30, "30 seconds"], [60, "1 minute"], [120, "2 minutes"], [300, "5 minutes"], [600, "10 minutes"]];
export const PUSH_DELAY_DEFAULT = 30;
export const PUSH_DELAY_MIN = 5;
export const PUSH_DELAY_MAX = 3600;

/** pushOf reads the push preferences. The delay is the default when it is not set. */
export function pushOf(raw) {
  const p = raw && typeof raw === "object" ? raw : {};
  const d = p.delaySeconds;
  return {
    off: p.off === true,
    anonymous: p.anonymous === true,
    delaySeconds: Number.isInteger(d) && d >= PUSH_DELAY_MIN && d <= PUSH_DELAY_MAX ? d : PUSH_DELAY_DEFAULT,
  };
}

/** pushPrefs is the push preference as the desktop keeps it: only what is not the
 *  default, and null when everything is (the desktop leaves it out). */
export function pushPrefs(p) {
  const out = {};
  if (p.off) out.off = true;
  if (p.anonymous) out.anonymous = true;
  if (p.delaySeconds !== PUSH_DELAY_DEFAULT) out.delaySeconds = p.delaySeconds;
  return Object.keys(out).length ? out : null;
}

/** delayLabel names a delay in words. */
export function delayLabel(seconds) {
  const known = PUSH_DELAYS.find(([s]) => s === seconds);
  return known ? known[1] : seconds + " seconds";
}

/** permissionOf says where the browser's notification permission stands:
 *  "granted", "denied", "default" (not asked yet) or "unsupported". */
export function permissionOf(win) {
  try {
    const N = win && win.Notification;
    if (!N) return "unsupported";
    return N.permission === "granted" || N.permission === "denied" ? N.permission : "default";
  } catch {
    return "unsupported";
  }
}

/** permissionWords says what the browser's answer means for notifications here. */
export function permissionWords(state) {
  switch (state) {
    case "unsupported": return "This browser does not offer them.";
    case "denied": return "The browser has blocked them for this window: allow them in its site settings for this address.";
    case "default": return "The browser has not been asked yet. Turn this on and it will ask.";
    default: return "";
  }
}

export const LABEL_MAX = 60;
/** FINISHED_MIN_MS is how long a turn has to have run for its end to be announced. */
export const FINISHED_MIN_MS = 10000;

/** labelOf is a pane name or branch fit for a notification: format and control characters
 *  gone, white space one space, cut to LABEL_MAX characters. It is empty when nothing is left. */
export function labelOf(v) {
  const t = typeof v === "string" ? v.slice(0, LABEL_MAX * 4).replace(/\p{Cf}/gu, "").replace(/[\p{C}\s]+/gu, " ").trim() : "";
  const cps = Array.from(t);
  return cps.length > LABEL_MAX ? cps.slice(0, LABEL_MAX - 1).join("") + "…" : t;
}

/** waitsOf compares the panes now with the status each had last time. fresh are
 *  the panes that just started waiting (or were blocked) and were not before;
 *  next is the table to compare with the next time. A pane seen for the first
 *  time is never fresh: arriving at a workspace where three agents are already
 *  waiting is not news that they just stopped. finished are the structured panes
 *  whose turn just ended (working before, idle now); they are kept apart because
 *  they do not wait on anyone, and only a turn that ran FINISHED_MIN_MS or more is
 *  announced. began (id to the time the turn was first seen working; the page keeps it
 *  between calls) times that, from the pane's statusSince when it has one. */
export function waitsOf(before, panes, now = Date.now(), began = new Map()) {
  const next = new Map();
  const fresh = [];
  const finished = [];
  for (const [id, v] of Object.entries(panes && typeof panes === "object" ? panes : {})) {
    if (!v || typeof v !== "object") continue;
    const status = typeof v.status === "string" ? v.status : "";
    next.set(id, status);
    const was = before.get(id);
    const needs = status === "waiting" || status === "blocked";
    const row = { id, name: labelOf(v.name) || "An agent", branch: labelOf(v.branch), blocked: status === "blocked", structured: v.structured === true };
    if (v.structured === true) {
      if (status === "working" && !began.has(id)) {
        const at = typeof v.statusSince === "string" ? Date.parse(v.statusSince) : NaN;
        began.set(id, Number.isFinite(at) && at <= now ? at : now);
      }
      const ran = began.has(id) ? now - began.get(id) : 0;
      if (was === "working" && status === "idle" && ran >= FINISHED_MIN_MS) finished.push({ ...row, finished: true });
      if (status !== "working" && status !== "waiting" && status !== "blocked") began.delete(id);
    }
    if (!needs || was === undefined || was === "waiting" || was === "blocked") continue;
    fresh.push(row);
  }
  return { fresh, finished, next };
}

/** noteOf is the title and body of one notification for the panes that just
 *  stopped. They all share one tag, so several sent together would replace each
 *  other and only the last would be seen; one notification says them all. */
export function noteOf(fresh) {
  if (fresh.length === 1) {
    const f = fresh[0];
    const where = f.branch ? f.branch + " — " : "";
    if (f.finished) return { title: f.name + " finished", body: where + "ready for your next message", paneId: f.id };
    const how = f.blocked ? (f.structured ? "needs setting up" : "a tool call was blocked") : f.structured ? "waiting for your answer" : "waiting for input";
    return { title: f.name + " needs you", body: where + how, paneId: f.id };
  }
  const names = fresh.map((f) => f.name).join(", ");
  if (fresh.every((f) => f.finished)) return { title: fresh.length + " agents finished", body: names, paneId: fresh[0].id };
  // Panes that need the person come first, so a click opens one of them.
  const first = fresh.find((f) => !f.finished) || fresh[0];
  return { title: fresh.length + " agents need you", body: names, paneId: first.id };
}

/** raise shows a notification, and answers whether it did. Clicking it brings the
 *  window forward and calls onClick(paneId). */
export function raise(win, note, onClick) {
  try {
    if (permissionOf(win) !== "granted") return null;
    // One tag, so a newer notification replaces the one before; renotify, because
    // a replacement is otherwise put in place silently.
    const n = new win.Notification(note.title, { body: note.body, tag: "flockdeck", renotify: true });
    n.onclick = () => {
      if (win.focus) win.focus();
      onClick(note.paneId);
      if (n.close) n.close();
    };
    return n;
  } catch {
    return null;
  }
}

/** askPermission asks the browser, once, when notifications are wanted and it has
 *  not been asked. It answers what the browser said. */
export function askPermission(win) {
  try {
    if (permissionOf(win) !== "default") return Promise.resolve(permissionOf(win));
    const asked = win.Notification.requestPermission();
    return Promise.resolve(asked).then(() => permissionOf(win), () => permissionOf(win));
  } catch {
    return Promise.resolve(permissionOf(win));
  }
}

/** PRESENCE_EVERY_MS is how often at most this page tells the desktop the person
 *  is there. Pointer moves are thinned out well below that before they are even
 *  offered. */
export const PRESENCE_EVERY_MS = 30000;
export const MOVE_EVERY_MS = 1000;

/** watchPresence reports real input to the desktop: a keystroke, a click, a
 *  scroll, the pointer moving. Coming to the front or gaining focus does not
 *  count, since a window can sit there with nobody at the desk. Nothing of what
 *  was typed goes with it. It answers a function that stops watching. */
export function watchPresence(win, send, now = () => Date.now()) {
  const doc = win && win.document;
  if (!doc || !doc.addEventListener) return () => {};
  let last = -Infinity;
  let lastMove = -Infinity;
  const report = () => {
    const t = now();
    if (t - last < PRESENCE_EVERY_MS) return;
    last = t;
    send({ cmd: "presence", kind: "used" });
  };
  const move = () => {
    const t = now();
    if (t - lastMove < MOVE_EVERY_MS) return;
    lastMove = t;
    report();
  };
  const events = [["keydown", report], ["pointerdown", report], ["wheel", report], ["pointermove", move]];
  for (const [name, fn] of events) doc.addEventListener(name, fn, true);
  return () => {
    for (const [name, fn] of events) doc.removeEventListener(name, fn, true);
  };
}

/** waitingCount is how many panes wait on the person or are blocked: what the tab title counts. */
export function waitingCount(panes) {
  let n = 0;
  for (const v of Object.values(panes && typeof panes === "object" ? panes : {})) {
    if (v && typeof v === "object" && (v.status === "waiting" || v.status === "blocked")) n++;
  }
  return n;
}

/** titleOf is the window title: with agents waiting, the count leads it. */
export function titleOf(count) {
  return count > 0 ? "(" + count + ") Flockdeck" : "Flockdeck";
}

export const ICON_DEFAULT = "assets/icon.svg";
export const ICON_WAITING = "assets/icon-waiting.svg";

/** showAttention sets the window title and the tab icon for count agents waiting. It touches nothing it cannot reach. */
export function showAttention(doc, count) {
  try {
    if (!doc) return;
    doc.title = titleOf(count);
    let link = doc.getElementById ? doc.getElementById("flockdeck-icon") : null;
    if (!link && doc.createElement && doc.head) {
      link = doc.createElement("link");
      link.id = "flockdeck-icon";
      link.rel = "icon";
      link.type = "image/svg+xml";
      doc.head.appendChild(link);
    }
    if (link) link.href = count > 0 ? ICON_WAITING : ICON_DEFAULT;
  } catch {
    /* a page without a head has nothing to change */
  }
}
