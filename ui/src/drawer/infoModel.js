// The agent info view of a structured pane, without the drawing. The desktop
// answers structuredInfo with plain text (names only, never an MCP server's
// command, arguments or environment); this file builds the ask and cleans the
// answer. Everything shown is text, never markup.

export const INFO_NOT_SENT_WORDS = "Not connected to Flockdeck, so the ask was not sent.";
export const INFO_WAITING_WORDS = "Asking the agent's facts.";
export const INFO_LOGOUT_WORDS = "Flockdeck does not hold the agent's login. To sign out or to sign in as someone else, use the agent's own tool, then restart the agent here.";
export const INFO_RESTART_NOT_SENT_WORDS = "Not connected to Flockdeck, so the restart was not sent.";

const TEXT_MAX = 6200;
const LINE_MAX = 400;

/** infoMessage is the command for a pane, or null when there is none to ask. */
export function infoMessage(pane, req) {
  if (typeof pane !== "string" || pane === "" || typeof req !== "string" || req === "") return null;
  return { cmd: "structuredInfo", id: pane, req };
}

/** restartMessage is the command that starts the agent again, which is how a sign-in done elsewhere is picked up. */
export function restartMessage(pane) {
  return typeof pane === "string" && pane !== "" ? { cmd: "restartPane", id: pane } : null;
}

/** infoRows reads the answer's text as rows { name, value }, at most 40, each cut. Control characters are spaces. */
export function infoRows(text) {
  const rows = [];
  for (const line of String(text == null ? "" : text).slice(0, TEXT_MAX).split("\n")) {
    const at = line.indexOf(": ");
    if (at < 1) continue;
    const clean = (s, n) => s.replace(/[\p{C}]/gu, " ").replace(/\s+/g, " ").trim().slice(0, n);
    const name = clean(line.slice(0, at), 60);
    if (name && rows.length < 40) rows.push({ name, value: clean(line.slice(at + 2), LINE_MAX) });
  }
  return rows;
}

/** answerFor is the store's infoResult when it is the answer to this pane's ask req, else null. */
export function answerFor(result, pane, req) {
  return result && req && result.req === req && result.id === pane ? result : null;
}

/** needsSignIn says the push has the agent waiting for a login. */
export function needsSignIn(st) {
  return !!st && st.phase === "setup" && !!st.setup && st.setup.kind === "login";
}

// Signing in from the pane (API 1.24, cap structuredSignIn). The words are ours:
// nothing the agent or its login command wrote is shown, and the only string
// that came from there is the address, which is checked again before it becomes
// a link.
export const SIGNIN_START_WORDS = "Sign in";
export const SIGNIN_CANCEL_WORDS = "Cancel the sign-in";
export const SIGNIN_STARTING_WORDS = "Starting the sign-in. The sign-in page link appears here in a moment.";
export const SIGNIN_WAITING_WORDS = "Open the sign-in page, sign in there, then come back. Flockdeck restarts the agent when the sign-in is done.";
export const SIGNIN_DONE_WORDS = "Signed in. Restarting the agent.";
export const SIGNIN_FAILED_WORDS = "The sign-in did not finish. You can try again.";
export const SIGNIN_NOT_SENT_WORDS = "Not connected to Flockdeck, so the sign-in was not started.";
export const SIGNIN_LINK_WORDS = "Open the sign-in page";

/** signInMessage starts the sign-in of a pane; signInCancelMessage ends it. Each is null without a real pane. */
export function signInMessage(pane) {
  return typeof pane === "string" && pane !== "" ? { cmd: "structuredSignIn", id: pane } : null;
}
export function signInCancelMessage(pane) {
  return typeof pane === "string" && pane !== "" ? { cmd: "structuredSignInCancel", id: pane } : null;
}

/** safeSignInUrl returns the address as a normalised https URL, or null: https only, no user name, password or port, ASCII, at most 2048 characters. */
export function safeSignInUrl(raw) {
  if (typeof raw !== "string" || raw.length === 0 || raw.length > 2048 || !/^[\x21-\x7e]+$/.test(raw)) return null;
  let u;
  try {
    u = new URL(raw);
  } catch (_) {
    return null;
  }
  if (u.protocol !== "https:" || u.username || u.password || u.port || !u.hostname || u.hostname.endsWith(".")) return null;
  return u.href;
}

/** signInView reads the push's signIn block as { available, state, url, host }, with url and host null unless the address is safe. Null when the pane has no block. */
export function signInView(st) {
  const b = st && st.signIn;
  if (!b || typeof b !== "object") return null;
  const state = ["starting", "waiting", "done", "failed"].includes(b.state) ? b.state : "";
  const url = state === "waiting" ? safeSignInUrl(b.url) : null;
  return { available: b.available === true, state, url, host: url ? new URL(url).hostname : null };
}
