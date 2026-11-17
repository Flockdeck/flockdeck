// A paired device's commit, push or pull is held until a person at the desk says
// yes to that one request (internal/server/sealed_confirm.go). The host sends a
// deskConfirm with state "ask" to each window on this machine, each with its own
// nonce, and one with state "done" when the request is settled by anyone or runs
// out. The yes or no goes back with the id and the nonce this window was given.
// Nothing here is remembered: the next request is a new question.

/** MAX_ASKS is the most questions a window keeps; the host holds at most eight at once. */
export const MAX_ASKS = 8;

/** applyDeskConfirm returns the list of open questions after one message. A malformed message changes nothing. */
export function applyDeskConfirm(list, msg) {
  const cur = Array.isArray(list) ? list : [];
  if (!msg || typeof msg.id !== "string" || !msg.id) return cur;
  const rest = cur.filter((q) => q.id !== msg.id);
  if (msg.state === "done") return rest;
  if (msg.state !== "ask" || typeof msg.nonce !== "string" || !msg.nonce) return cur;
  const ask = {
    id: msg.id,
    nonce: msg.nonce,
    action: typeof msg.action === "string" ? msg.action : "",
    device: typeof msg.device === "string" ? msg.device : "",
    summary: typeof msg.summary === "string" ? msg.summary : "",
  };
  return [...rest, ask].slice(-MAX_ASKS);
}

/** answerCommand is the command that answers a question: on true allows that one request. */
export function answerCommand(ask, on) {
  return { cmd: "deskConfirmAnswer", id: ask.id, nonce: ask.nonce, on: !!on };
}

/** deskConfirmWords is what the question says. The device name and the summary are text a device and a repository supplied and are drawn as text. */
export function deskConfirmWords(ask) {
  const who = ask.device || "A paired device";
  return {
    title: `${who} wants to ${ask.action || "change a repository"}`,
    words: [ask.summary, "It is run only if you allow it here, and asked again next time. You can deny it."].filter(Boolean),
  };
}
