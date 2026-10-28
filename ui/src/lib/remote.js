// Flockdeck Remote: whether this machine is reachable through the relay, the
// devices paired with the account, the pairing link, and the words for each.
//
// Everything here reads what the desktop sent defensively: a field of the wrong
// type is left out, never trusted. Every string that came from the relay or a
// person (device and machine names, the relay's own sentences, error text) is
// drawn as text by the dialog and is never used as markup. The pairing picture is
// an SVG the desktop drew, and is only ever the source of an image, which does
// not run what is in it.
//
// A window reached through the relay may list the devices, pair a device, rename
// and unpair. It may not turn remote access on or off, move it to another relay,
// remove the machine, ask for a join code, verify a device or limit one: the
// desktop refuses those, the store does not send them from such a window, and the
// dialog says which are the desk's.

/** The words for what a relay window is not offered. */
export const DESK_ONLY_REMOTE =
  "Flockdeck Remote is turned on, turned off and moved to another relay on the machine Flockdeck runs on, not from a window reached through the relay.";
export const DESK_ONLY_VERIFY =
  "A device is marked verified on the machine Flockdeck runs on, not from a window reached through the relay.";
export const DESK_ONLY_ACCESS =
  "What a device may do is set on the machine Flockdeck runs on, not from a window reached through the relay.";
export const DESK_ONLY_APPROVE =
  "Whether a device may approve agent requests is set on the machine Flockdeck runs on, not from a window reached through the relay.";
/** APPROVE_ACK_WORDS is shown before a device is allowed to approve. It is the host's own wording
 *  (approveAckText in internal/server/approvals_prefs.go); a Go test keeps the two the same. */
export const APPROVE_ACK_WORDS =
  "This device will be shown the commands and file paths your agents ask to run or change, and can allow or deny them. A relay that only passes traffic along cannot read them; a tampered relay, or a tampered page on the phone, could.";
/** APPROVE_ACK_VERSION is the wording version this page was built for; the host refuses a turn-on that names another. */
export const APPROVE_ACK_VERSION = 2;
export const DESK_ONLY_JOIN =
  "A code for another desktop to join this account is made on the machine Flockdeck runs on, not from a window reached through the relay.";

export const INTRO_WORDS =
  "Flockdeck Remote opens this window from a paired phone, tablet or browser, through Flockdeck's optional relay. " +
  "A paired device gets the full window with full control, terminals included. This machine connects out to the relay and listens for nothing from the network.";

export const PAIR_WORDS = "A pairing link works once and expires in a few minutes. The device that opens it can open this window until you unpair it.";
export const LINK_WARNING =
  "Whoever opens it can drive every agent on every machine on this account, and open a shell on any of them, so treat it like a password until then.";
export const VERIFY_HELP =
  "Press Verify and compare the code with what the device shows (its own Devices page, or this page's code in a window it opened from here). " +
  "If they differ, unpair it: the relay may have handed it the wrong key. Press Mark verified once they match.";
export const VERIFY_HELP_RELAY = "Press Verify to see the codes. " + DESK_ONLY_VERIFY;
export const PLAN_WORDS =
  "Flockdeck Remote through the relay is paid for from Devices on a paired phone or browser. The desktop app is free, and works the same whatever the plan.";
export const KEPT_WORDS =
  "The account is kept while anything on it is in use, and deleted if nothing on it is used for 90 days and it has no subscription.";
export const ENTERPRISE_WORDS = "Coming soon, for companies: Enterprise, a licence to run the relay on your own infrastructure, with SSO and support.";

const str = (v) => (typeof v === "string" ? v : "");
const time = (v) => {
  const t = Date.parse(typeof v === "string" ? v : "");
  return t > 0 ? t : 0;
};

/** bare is a relay address without its scheme, for a sentence. */
export function bare(url, fallback = "the relay") {
  return (str(url) || fallback).replace(/^https?:\/\//, "");
}

/** viewing is the tunnel state to a draw: null while remote access is off. */
export function viewOf(r) {
  if (!r || typeof r !== "object") return null;
  return {
    state: str(r.state),
    detail: str(r.detail),
    relay: str(r.relay),
    name: str(r.name),
    viewers: Number.isInteger(r.viewers) && r.viewers > 0 ? r.viewers : 0,
    retryAt: str(r.retryAt),
    pushError: str(r.pushError),
  };
}

const two = (n) => String(n).padStart(2, "0");

/** clock is a time of day as hours, minutes and seconds. */
export function clock(ms) {
  const d = new Date(ms);
  return two(d.getHours()) + ":" + two(d.getMinutes()) + ":" + two(d.getSeconds());
}

/** retryClock is " Trying again at 14:32:07." for the tunnel's wait, or " Trying
 *  again shortly." once that time is at hand, so the line never claims a moment
 *  already past. Empty when nothing waits. A clock time reads true until the next
 *  push, where a countdown would sit still between pushes. */
export function retryClock(retryAt, now = Date.now()) {
  const t = time(retryAt);
  if (!t) return "";
  if (t <= now) return " Trying again shortly.";
  return " Trying again at " + clock(t) + ".";
}

/** summaryOf says in a sentence where the tunnel stands. */
export function summaryOf(r, now = Date.now()) {
  const where = bare(r.relay);
  switch (r.state) {
    case "connected":
      return "Reachable through " + where + (r.viewers ? " — " + r.viewers + (r.viewers === 1 ? " window is" : " windows are") + " open from another device" : "");
    case "connecting":
      return r.retryAt ? "Reconnecting to " + where + "." + retryClock(r.retryAt, now) : "Connecting to " + where + "…";
    case "error":
      return "Cannot reach " + where + (r.detail ? ": " + r.detail : "") + "." + retryClock(r.retryAt, now);
    case "disabled":
      return "Flockdeck Remote is off on this machine. Its account is kept: paired devices see this machine as offline, and turning it on again reconnects to the same account.";
    case "revoked":
    case "replaced":
      return r.detail || "Flockdeck Remote has stopped.";
    case "lapsed":
      return r.detail || "The relay has stopped Flockdeck Remote for this account.";
    default:
      return "Not connected to " + where + ".";
  }
}

/** ago says how long since a time the relay reported, roughly. A time it never
 *  had arrives as Go's zero time, which is long before anything. */
export function ago(when, now = Date.now()) {
  const t = time(when);
  if (!t) return "never";
  const s = Math.max(0, (now - t) / 1000);
  if (s < 60) return "just now";
  const unit = (n, one) => n + " " + one + (n === 1 ? "" : "s") + " ago";
  if (s < 3600) return unit(Math.floor(s / 60), "minute");
  if (s < 172800) return unit(Math.floor(s / 3600), "hour");
  return unit(Math.floor(s / 86400), "day");
}

/** day is a day as people write it: 12 October 2026. */
export function day(when) {
  return new Date(time(when)).toLocaleDateString("en-GB", { day: "numeric", month: "long", year: "numeric" });
}

/** daysLeft is the days until a time the relay reported, a part of one counting as one. */
export function daysLeft(when, now = Date.now()) {
  const t = time(when);
  return t ? Math.max(0, Math.ceil((t - now) / 86400000)) : 0;
}

/** planOf reads the account's plan, or null when the relay reports none. */
export function planOf(p) {
  if (!p || typeof p !== "object") return null;
  return {
    plan: str(p.plan),
    name: str(p.name),
    active: p.active !== false,
    ends: str(p.ends),
    deleteAt: str(p.deleteAt),
    message: str(p.message),
  };
}

/** planText says the plan in a sentence. The relay decides anything by a plan;
 *  the app only shows it, and every part of it works the same whatever it says. */
export function planText(p, now = Date.now()) {
  if (!p.active) {
    let s = p.message || "The relay has stopped Flockdeck Remote for this account.";
    if (time(p.deleteAt)) s += " The account and its paired devices are kept until " + day(p.deleteAt) + ", then deleted.";
    return s;
  }
  if (p.plan === "trial") {
    const n = daysLeft(p.ends, now);
    return (p.name || "Free trial") + ": " + n + (n === 1 ? " day" : " days") + " left, until " + day(p.ends) + ".";
  }
  return (p.name || "Subscription") + (time(p.ends) ? ", paid until " + day(p.ends) : "") + ".";
}

/** rosterOf reads the desktop's remoteDevices answer. */
export function rosterOf(msg) {
  const m = msg && typeof msg === "object" ? msg : {};
  const devices = (Array.isArray(m.devices) ? m.devices : [])
    .filter((d) => d && typeof d === "object" && typeof d.id === "string" && d.id !== "")
    .map((d) => ({
      id: d.id,
      name: str(d.name),
      created: str(d.created),
      lastSeen: str(d.lastSeen),
      fingerprint: str(d.fingerprint),
      deskFingerprint: str(d.deskFingerprint),
      verify: str(d.verify),
      deskVerify: str(d.deskVerify),
      // A desk that sends no role means full access; a role this file does not know is shown as unknown, not as full access.
      role: d.role === "viewer" ? "viewer" : d.role === "full" || d.role === undefined || d.role === "" ? "full" : "unknown",
      watchPanes: d.watchPanes === true,
      // Whether the desk let the device approve agent requests, and the state of the key it would do that with (API 1.26).
      approve: d.approve === true,
      approveKey: str(d.approveKey),
    }));
  const hosts = (Array.isArray(m.hosts) ? m.hosts : [])
    .filter((h) => h && typeof h === "object")
    .map((h) => ({ id: str(h.id), name: str(h.name), online: h.online === true, lastSeen: str(h.lastSeen), self: h.self === true }));
  return {
    enabled: m.enabled === true,
    devices,
    hosts,
    current: str(m.current),
    error: str(m.error),
    plan: planOf(m.plan),
  };
}

/** deviceCodes lists the codes a device row can show: one for each of the
 *  device's two keys the roster has a code for. */
export function deviceCodes(d) {
  const out = [];
  if (d.fingerprint) out.push({ origin: "usual", label: "Remote app", code: d.fingerprint, state: d.verify });
  if (d.deskFingerprint) out.push({ origin: "desk", label: "This desktop's page", code: d.deskFingerprint, state: d.deskVerify });
  return out;
}

/** verifyText says in words what a verify state is. */
export function verifyText(state) {
  if (state === "verified") return "Verified on the desktop";
  if (state === "changed") return "The key changed since it was verified. Compare the code again";
  return "Not verified";
}

/** NAME_MAX is the most of a device's name that is drawn. */
export const NAME_MAX = 40;

/**
 * deviceName is a paired device's or machine's name as it is drawn, in a question, a label and the list. The device
 * chooses its own name, so control, line-break, zero-width and bidirectional characters are taken out (they can
 * reorder the words around the name or hide them), runs of space become one, and a long name is cut. Joiners
 * and variation selectors go too, on purpose, and this costs some honest names: a zero-width joiner sequence (a
 * family or a profession emoji) is drawn as its parts, the emoji presentation selector (U+FE0F) is lost, so a heart
 * may be drawn as text, a zero-width non-joiner (U+200C) is replaced by a space, which splits Persian and some Indic
 * spelling, and the Mongolian free variation selectors (U+180B to U+180F) are dropped from the middle of a word. A
 * name that cannot hide what it is matters more than one that looks exactly as the device chose.
 */
export function deviceName(name) {
  const clean = String(name == null ? "" : name)
    .replace(/[\u0000-\u001f\u007f-\u009f\u00ad\u034f\u061c\u115f-\u1160\u17b4-\u17b5\u180b-\u180f\u200b-\u200f\u2028-\u202e\u2060-\u206f\u2800\u3164\ufe00-\ufe0f\ufeff\uffa0\ufff9-\ufffc\u{e0000}-\u{e007f}\u{e0100}-\u{e01ef}]/gu, " ")
    .replace(/\s+/g, " ")
    .trim();
  const chars = Array.from(clean);
  return chars.length > NAME_MAX ? chars.slice(0, NAME_MAX - 1).join("").trimEnd() + "\u2026" : clean;
}

/** ACCESS are what a device may be allowed, in the order Access steps through. */
export const ACCESS = [
  { role: "full", watch: false, label: "Full access", rank: 2 },
  { role: "viewer", watch: false, label: "View artifacts only", rank: 0 },
  { role: "viewer", watch: true, label: "View artifacts and watch panes", rank: 1 },
];

/** UNKNOWN_ACCESS is what a device is shown with when its role is not one of ACCESS. It is never stepped from. */
export const UNKNOWN_ACCESS = { role: "", watch: false, label: "Unknown", rank: -1 };

/** accessIndex is the place of a device's level in ACCESS, or -1 when it is none of them. A full device is full whatever its watch flag says. */
export function accessIndex(d) {
  return ACCESS.findIndex((l) => l.role === d.role && (l.role === "full" || l.watch === d.watchPanes));
}

/** accessOf is the level a device has, or UNKNOWN_ACCESS. */
export function accessOf(d) {
  const i = accessIndex(d);
  return i < 0 ? UNKNOWN_ACCESS : ACCESS[i];
}

/** nextAccess is the level one press of the access button sets, or null from a level that is not known. */
export function nextAccess(d) {
  const i = accessIndex(d);
  return i < 0 ? null : ACCESS[(i + 1) % ACCESS.length];
}

/** addsAccess says a step gives a device more than it has: it is asked about first. */
export function addsAccess(from, to) {
  return to.rank > from.rank;
}

/** pairOf reads the desktop's remotePair answer. */
export function pairOf(msg) {
  const m = msg && typeof msg === "object" ? msg : {};
  return {
    kind: m.kind === "host" ? "host" : "device",
    code: str(m.code),
    url: str(m.url),
    expiresAt: str(m.expiresAt),
    qr: str(m.qr),
    error: str(m.error),
  };
}

/** PAIR_CAP_MS is how long a link or code with no expiry of its own is shown,
 *  counted from when it arrived. */
export const PAIR_CAP_MS = 10 * 60 * 1000;

/** pairLive says a pairing link or join code is still good: it has one and has
 *  not run out. One with no expiry is shown for PAIR_CAP_MS from arrival (arrived
 *  is set by the page), so it is never live for ever. */
export function pairLive(p, now = Date.now()) {
  if (!p || !(p.url || p.code)) return false;
  const t = time(p.expiresAt);
  if (t) return t > now;
  return typeof p.arrived === "number" ? now - p.arrived < PAIR_CAP_MS : true;
}

const CODE = /^[A-Za-z0-9_-]+$/;
const RELAY = /^(https?:\/\/)?[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:\d{1,5})?\/?$/;

/** joinLine is the command to run on the other machine, or "" when the code or
 *  the relay is not plain text a terminal would take literally: both came from
 *  the relay, and the line is meant to be pasted. */
export function joinLine(relay, code) {
  if (!CODE.test(code || "") || (relay && !RELAY.test(relay))) return "";
  return "flockdeck remote enable " + (relay ? "-relay " + relay + " " : "") + "-join " + code;
}

/** untilText is ", until 14:05" for a pairing link's expiry. */
export function untilText(when) {
  const t = time(when);
  if (!t) return "";
  const d = new Date(t);
  return ", until " + two(d.getHours()) + ":" + two(d.getMinutes());
}

/** leftText says how long a pairing link has left: "4 minutes 12 seconds left",
 *  or that it has run out. */
export function leftText(when, now = Date.now()) {
  const t = time(when);
  if (!t) return "";
  const s = Math.ceil((t - now) / 1000);
  if (s <= 0) return "expired";
  const m = Math.floor(s / 60);
  const sec = s % 60;
  const minutes = m ? m + (m === 1 ? " minute" : " minutes") : "";
  const seconds = sec || !m ? sec + (sec === 1 ? " second" : " seconds") : "";
  return [minutes, seconds].filter(Boolean).join(" ") + " left";
}

/** qrSource is the address an image takes the picture from. */
export function qrSource(svg) {
  return svg ? "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg) : "";
}

/** outcomeOf reads the desktop's remoteOutcome answer. */
export function outcomeOf(msg) {
  const m = msg && typeof msg === "object" ? msg : {};
  return { action: str(m.action), error: str(m.error), untold: m.untold === true, warning: str(m.warning) };
}

// The commands. Each returns null for what would not be accepted, so the dialog
// can say why instead of sending it.

export const POLL_MS = 4000;

export function devicesCommand() {
  return { cmd: "remoteDevices" };
}

export function pairCommand(kind) {
  return { cmd: "remotePair", kind: kind === "host" ? "host" : "device" };
}

export function enableCommand(d) {
  const more = !!d.more;
  return { cmd: "remoteEnable", relay: str(d.relay).trim(), name: str(d.name).trim(), join: more ? str(d.join).trim() : "", invite: more ? str(d.invite).trim() : "" };
}

/** resumeCommand turns a machine that was turned off back on, to the same account. */
export function resumeCommand() {
  return { cmd: "remoteEnable", relay: "", name: "", join: "", invite: "" };
}

export function moveCommand(d) {
  const relay = str(d.relay).trim();
  if (!relay) return null;
  return { cmd: "remoteMove", relay, invite: str(d.invite).trim(), join: str(d.join).trim() };
}

export function renameCommand(kind, id, name) {
  const next = str(name).trim();
  if (kind === "host") return { cmd: "remoteRename", kind: "host", name: next };
  return id ? { cmd: "remoteRename", kind: "device", id, name: next } : null;
}

export function revokeCommand(id) {
  return id ? { cmd: "remoteRevoke", id } : null;
}

/** verifyCommand sends the code that was on screen, which the desktop checks
 *  against the key as it is now, so a key replaced since is not verified by mistake. */
export function verifyCommand(id, origin, code) {
  return id && code ? { cmd: "remoteVerify", id, kind: origin === "desk" ? "desk" : "usual", text: code } : null;
}

export function unverifyCommand(id, origin) {
  return id ? { cmd: "remoteUnverify", id, kind: origin === "desk" ? "desk" : "usual" } : null;
}

/** challengeCommand opens the question for a device: the host answers with the wording and a one-time nonce for this window. */
export function challengeCommand(id) {
  return id ? { cmd: "approveChallenge", id } : null;
}

/** approveCommand turns a device's approving on or off. Turning it on carries the wording version the person was shown and the
 *  nonce the host gave this window for this device; without both the host refuses. */
export function approveCommand(id, on, challenge) {
  if (!id) return null;
  if (!on) return { cmd: "setDeviceApprove", id, on: false };
  if (!challenge || challenge.id !== id || typeof challenge.nonce !== "string" || !challenge.nonce) return null;
  return { cmd: "setDeviceApprove", id, on: true, nonce: challenge.nonce, v: challenge.v };
}

/** approveOffCommand is the kill switch: no device approves until it is allowed again, one at a time. */
export function approveOffCommand() {
  return { cmd: "setApprovalsOff" };
}

/** approveView is what the desk draws for a device's approving: whether it is on, whether it can be turned on now (its code
 *  is marked verified), and a line to show when it is on but cannot work because the code is not. */
export function approveView(d) {
  const on = !!d.approve;
  const verified = d.approveKey === "verified";
  return { on, canTurnOn: verified, note: on && !verified ? "Approving is on, but this device's code is not verified, so it will not work until you verify it again." : "" };
}

export function roleCommand(id, level) {
  return id && level ? { cmd: "setDeviceRole", id, kind: level.role, watch: level.watch } : null;
}
