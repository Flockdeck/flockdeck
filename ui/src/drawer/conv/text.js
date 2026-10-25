// A conversation as plain text, for Copy. It is what the list holds, not what
// the desktop stored: the entries loaded so far, with a tool as its label and
// summary and an image as its description.
import { imageWords } from "./Image.js";

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));

/** entryText is one entry as a block of text, or "" for one with nothing to say. */
export function entryText(e) {
  switch (text(e.kind)) {
    case "prompt": return "You: " + text(e.text);
    case "reply": return "Agent: " + text(e.markdown || e.text);
    case "thinking": return "";
    case "tool": return "Tool: " + [text(e.label) || text(e.name), text(e.summary)].filter(Boolean).join(" - ") + (e.status ? " [" + text(e.status) + "]" : "");
    case "subagent": return "Helper: " + (text(e.description) || text(e.name)) + (e.status ? " [" + text(e.status) + "]" : "") + (e.report ? "\n" + text(e.report) : "");
    case "compaction": return "(conversation compacted)";
    case "mail": return "From " + (text(e.label) || text(e.from)) + ": " + text(e.text);
    case "event": return "(" + text(e.text) + ")";
    case "notice": return "(" + text(e.text) + ")";
    case "image": return "(" + imageWords(e) + ")";
    default: return "";
  }
}

/** conversationText is the entries as text, one blank line between blocks. */
export function conversationText(entries) {
  return entries.map(entryText).filter((t) => t !== "").join("\n\n");
}
