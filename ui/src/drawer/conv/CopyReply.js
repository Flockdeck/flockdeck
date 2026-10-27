// A small Copy button for one reply: puts the reply's text on the clipboard as
// plain text, the Markdown the agent wrote, and says whether it worked.
import { useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { copyText } from "./markdown.js";

export function CopyReply({ text, copy = copyText }) {
  const [said, setSaid] = useState("");
  const go = () => {
    copy(String(text || "")).then((ok) => {
      setSaid(ok ? "Copied" : "Couldn't copy");
      setTimeout(() => setSaid(""), 1500);
    });
  };
  return html`<button type="button" class="conv-copy-reply" aria-label="Copy this reply" onClick=${go}>${said || "Copy"}</button>`;
}

/** lastReply is the text of the newest reply among the entries, or "". */
export function lastReply(entries) {
  for (let i = entries.length - 1; i >= 0; i--) {
    const e = entries[i];
    if (e && e.kind === "reply") {
      const t = typeof e.markdown === "string" && e.markdown ? e.markdown : typeof e.text === "string" ? e.text : "";
      if (t) return t;
    }
  }
  return "";
}
