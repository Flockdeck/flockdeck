// The one line of words under a structured pane's composer. There used to be up to ten notes stacked
// there; this picks the single one worth reading now. A note that needs an action outranks one that
// asks for care, and that outranks a hint:
//
//   error:   the text was refused or not sent, the "!" command was refused, Stop was not sent, a
//            setting was refused, the desk answered a Stop with an error, a picture failed
//   warning: a picture that is not taken now (one is already attaching, the agent takes none)
//   hint:    the picture being attached, the turn being stopped, what "!" does, how to stop the turn
//
// Inside a level the order of the list above holds. The function is pure: the caller gives the facts
// as they stand and draws what comes back, or nothing when it returns null.
import { INTERRUPT_HINT, INTERRUPT_HINT_WAITING } from "./interruptModel.js";

/** SEND_NOT_SENT_WORDS is what the prompt box says when the store did not send the text; the draft stays. */
export const SEND_NOT_SENT_WORDS = "Not connected to Flockdeck, so the text was not sent.";
/** STOP_NOT_SENT_WORDS is what Stop says when the store did not send it: the socket is down or the hello is not in. */
export const STOP_NOT_SENT_WORDS = "Not connected to Flockdeck, so Stop was not sent.";
export const SHELL_HINT_WORDS = "Runs in this folder as you. The agent is not asked and does not see it unless you send the output.";
export const STOPPING_WORDS = "Stopping the turn.";
export const QUEUE_HINT_WORDS = "What you send now waits in the queue.";

/** LEVELS is the order of priority, first wins. */
export const LEVELS = ["error", "warning", "hint"];

/** composerNotes is the note to show, { id, level, text, role, send }, or null. The facts:
 *    refused      the words the desk refused the text with, or ""
 *    sendFailed   the store did not send the text
 *    shellOn      the draft starts with "!"; shellNote is what the last "!" command was refused with
 *    stopFailed   the store did not send Stop
 *    setting      what a refused or failed change of a setting said; settingError the desk's refusal
 *    cancelError  what the desk answered a Stop with
 *    image        { text, level } for the picture, level "error", "warning" or "hint", or null
 *    stopping     Stop was sent and the turn is still going
 *    status       the pane's status: "working", "waiting" or anything else
 *  role is "alert" for what the person must notice and "status" for the rest; send marks the two
 *  about the text that was not sent. */
export function composerNotes(f = {}) {
  const note = (id, level, text, role = "status", send = false) => ({ id, level, text, role, send });
  const at = (level) => {
    const out = [];
    if (level === "error") {
      if (f.refused) out.push(note("refused", level, f.refused, "alert", true));
      if (f.sendFailed) out.push(note("send-failed", level, SEND_NOT_SENT_WORDS, "alert", true));
      if (f.shellOn && f.shellNote) out.push(note("shell-note", level, f.shellNote, "alert"));
      if (f.stopFailed) out.push(note("stop-failed", level, STOP_NOT_SENT_WORDS));
      if (f.setting) out.push(note("setting", level, f.setting));
      if (f.settingError) out.push(note("setting-error", level, f.settingError));
      if (f.cancelError) out.push(note("cancel-error", level, f.cancelError));
    }
    if (f.image && f.image.text && f.image.level === level) out.push(note("image", level, f.image.text));
    if (level === "hint") {
      if (f.stopping) out.push(note("stopping", level, STOPPING_WORDS));
      if (f.shellOn) out.push(note("shell-hint", level, SHELL_HINT_WORDS));
      if (f.status === "waiting" && !f.stopping) out.push(note("working", level, INTERRUPT_HINT_WAITING));
      if (f.status === "working" && !f.stopping) out.push(note("working", level, INTERRUPT_HINT + " " + QUEUE_HINT_WORDS));
    }
    return out;
  };
  for (const level of LEVELS) {
    const found = at(level);
    if (found.length) return found[0];
  }
  return null;
}
