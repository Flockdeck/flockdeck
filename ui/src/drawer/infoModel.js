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
