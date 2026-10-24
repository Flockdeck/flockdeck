// The bar above the graph when two agents have changed the same files: who, which
// files, and two actions. "Ask the manager to decide now" sends the manager a line
// naming both agents and the files (paneSend, as the manager bar does). "See in
// Changes" opens the first agent's drawer on its Changes tab. When an overlap
// ends, the bar says so for RESOLVED_MS, so a person who looked away still learns
// it was dealt with.
//
// The overlaps are the graph's conflict items (the server's radar, which the
// conflict-radar setting turns on); nothing here reads files. A team-wide list of
// changed files with owners needs the server and is not drawn.
//
// Every name and path is from an agent or a repository and is drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "./Node.js";
import { labelOf } from "./model.js";

export const RESOLVED_MS = 14000;
export const NO_MANAGER_WORDS = "This team has no manager to ask.";
export const NOT_SENT_WORDS = "Not connected to Flockdeck, so the manager was not asked.";

const nameOf = labelOf;

/** overlapsOf is the conflict items of a model as the bar shows them. */
export function overlapsOf(model) {
  return model.needs
    .filter((n) => n.kind === "conflict" && n.other)
    .map((n) => ({
      id: n.id || n.pane + "+" + n.other,
      a: n.pane,
      b: n.other,
      who: nameOf(model, n.pane) + " and " + nameOf(model, n.other),
      files: String(n.text || ""),
    }));
}

/** askText is the line the manager is sent. */
export function askText(o) {
  return "Two agents have changed the same files: " + o.who + (o.files ? " (" + o.files + ")" : "") + ". Please decide now who keeps which changes, and tell them both.";
}

/**
 * OverlapBar: model is the graph model, manager is { pane, name } or null,
 * send sends a control command and says whether it went, onSee(pane) opens that
 * agent's Changes.
 */
export function OverlapBar({ model, manager = null, send, onSee = null }) {
  const overlaps = overlapsOf(model);
  const before = useRef(new Map()); // id to the overlap, as last drawn
  const [resolved, setResolved] = useState([]);
  const [asked, setAsked] = useState({}); // id to "sent" or a failure line
  const key = overlaps.map((o) => o.id).join("\u0001");
  useEffect(() => {
    const now = new Map(overlaps.map((o) => [o.id, o]));
    const gone = [...before.current.values()].filter((o) => !now.has(o.id));
    before.current = now;
    if (gone.length === 0) return;
    setResolved((r) => r.concat(gone.map((o) => ({ ...o, until: Date.now() + RESOLVED_MS }))));
    const timer = setTimeout(() => setResolved((r) => r.filter((x) => x.until > Date.now())), RESOLVED_MS + 50);
    return () => clearTimeout(timer);
  }, [key]);
  if (overlaps.length === 0 && resolved.length === 0) return null;
  const ask = (o) => {
    if (!manager) return;
    setAsked((m) => ({ ...m, [o.id]: send({ cmd: "paneSend", id: manager.pane, text: askText(o) }) ? "sent" : NOT_SENT_WORDS }));
  };
  return html`<div class="overlap" aria-label="Files changed by two agents">
    ${overlaps.map((o) => html`<div class="overlap-bar" role="group" aria-label="Overlap" key=${o.id} data-overlap=${o.id}>
      <p class="overlap-text"><strong>${o.who}</strong> have both changed ${o.files ? html`<code>${clip(o.files, 120)}</code>` : "the same files"}.</p>
      <div class="overlap-actions">
        <button type="button" class="overlap-ask" disabled=${!manager || asked[o.id] === "sent"} title=${manager ? "" : NO_MANAGER_WORDS} onClick=${() => ask(o)}>${asked[o.id] === "sent" ? "Manager asked" : "Ask the manager to decide now"}</button>
        ${onSee ? html`<button type="button" class="overlap-see" onClick=${() => onSee(o.a)}>See in Changes</button>` : null}
      </div>
      ${asked[o.id] && asked[o.id] !== "sent" ? html`<p class="overlap-note" role="status">${asked[o.id]}</p>` : null}
    </div>`)}
    ${resolved.map((o) => html`<p class="overlap-resolved" role="status" key=${"r" + o.id + o.until}>Overlap resolved: ${o.who}.</p>`)}
  </div>`;
}
