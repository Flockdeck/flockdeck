// The person's own shell commands in a structured pane, without the drawing: what
// a "!" in the prompt box means, the commands that run, stop, remove and send
// one, and the rows of the push (`shell`). They are not the agent's. Text in a
// row is a program's output and is shown as text only, with control and format
// characters removed again here, though the desk has already removed them.

export const SHELL_MAX = 8;
export const SHELL_CMD_MAX = 500;
export const SHELL_TEXT_MAX = 8 * 1024;

const STATES = ["running", "exited", "stopped", "timeout", "failed"];

const cutLine = (s, n) => {
  const t = typeof s === "string" ? s.slice(0, n + 1).replace(/\p{Cf}/gu, "").replace(/[\p{C}\s]+/gu, " ").trim() : "";
  const cps = Array.from(t);
  return cps.length > n ? cps.slice(0, n - 1).join("") + "…" : t;
};

// Output keeps its line breaks and tabs; every other control character and every format character goes.
const cutText = (s, n) => {
  const t = typeof s === "string" ? s.slice(-n).replace(/\p{Cf}/gu, "").replace(/[^\P{C}\n\t]/gu, "") : "";
  return t;
};

/** SHELL_EMPTY_WORDS is said when "!" has nothing after it. */
export const SHELL_EMPTY_WORDS = "Type a command after the !";
/** SHELL_ONE_LINE_WORDS is said when the text after "!" has more than one line. */
export const SHELL_ONE_LINE_WORDS = "A command is one line";
/** shellWait is how long the box waits for the desk's answer to a "!" command. ms is a field so a test can shorten it. */
export const shellWait = { ms: 10000 };
export const SHELL_NO_ANSWER_WORDS = "No answer from Flockdeck, so the command may not have run. Check the list before sending it again.";
export const SHELL_NOT_SENT_WORDS = "Not connected to Flockdeck, so that was not run.";

/** shellText reads the prompt box. It is null when the text is for the agent: a "!" must be the first character,
 *  so a leading space sends a prompt that starts with "!" to the agent as it is. Otherwise it is the command after
 *  the "!", trimmed (empty when there is none). */
export function shellText(draft) {
  if (typeof draft !== "string" || !draft.startsWith("!")) return null;
  return draft.slice(1).trim();
}

/** shellRunMessage is the command that runs text for pane, or null when text is empty or is not one line
 *  (the desk would refuse it). */
export function shellRunMessage(pane, text, req) {
  if (!pane || typeof text !== "string") return null;
  const t = text.trim();
  if (t === "" || /[\p{C}\u2028\u2029]/u.test(t.replace(/\t/g, " "))) return null;
  return { cmd: "structuredShellRun", id: pane, text: t, req };
}

/** shellOf reads the commands of a push, the oldest first. A row that is not well formed is left out. */
export function shellOf(st) {
  const list = st && Array.isArray(st.shell) ? st.shell.slice(0, SHELL_MAX) : [];
  const out = [];
  const seen = new Set();
  for (const r of list) {
    if (!r || !Number.isInteger(r.id) || r.id <= 0 || seen.has(r.id) || !STATES.includes(r.state)) continue;
    seen.add(r.id);
    out.push({
      id: r.id,
      cmd: cutLine(r.cmd, SHELL_CMD_MAX),
      state: r.state,
      running: r.state === "running",
      code: r.state === "exited" && Number.isInteger(r.code) ? r.code : null,
      text: cutText(r.text, SHELL_TEXT_MAX),
      cut: r.cut === true,
    });
  }
  return out;
}

/** shellStateWords says in a few words where a run is. */
export function shellStateWords(r) {
  switch (r.state) {
    case "running": return "Running";
    case "exited": return r.code === 0 ? "Done" : "Exited with code " + (r.code === null ? "?" : r.code);
    case "stopped": return "Stopped";
    case "timeout": return "Ended after ten minutes";
    default: return "Did not start";
  }
}

/** shellMessage makes the command for op ("stop", "remove" or "send") about the run id of rows, the list the
 *  person is looking at. It is null for a run that is not listed, for Stop of one that has ended and for Remove of
 *  one that is running: the desk would refuse them. */
export function shellMessage(pane, rows, op, id, req) {
  if (!pane) return null;
  const row = rows.find((r) => r.id === id);
  if (!row) return null;
  if (op === "stop") return row.running ? { cmd: "structuredShellStop", id: pane, item: id, req } : null;
  if (op === "remove") return row.running ? null : { cmd: "structuredShellRemove", id: pane, item: id, req };
  if (op === "send") return { cmd: "structuredShellSend", id: pane, item: id, req };
  return null;
}
