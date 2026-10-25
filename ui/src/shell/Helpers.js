// The helper apps dialog: the programs Flockdeck can download, check, start and
// stop for you, one row each, and the two questions before anything is changed
// on disk: what an install would do, and what a removal would delete.
//
// It is modal like the start dialog: the keyboard goes to the first control when
// it opens and back to where it was when it closes, Tab stays inside, Escape
// closes. Everything the desktop sends about a helper (a name, a log line, an
// address, a list of what it may do) is drawn as text; the address is shown, not
// linked, because Open asks the desktop to open it. An install carries the
// version and the archive hash the person was shown, and the desktop refuses it
// if either has changed. The dialog is not drawn in a window reached through the
// relay, which has RELAY_WORDS instead (Live decides).
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { useInert } from "./inert.js";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { trapTab } from "./focus.js";
import {
  INTRO_WORDS, SAFETY_WORDS, SIGNATURE_WORDS, announcement, buttonsFor, installCommand, listCommand, openCommand, planCommand, planOf, rowsOf,
  startCommand, stateWords, stopCommand, uninstallCommand,
} from "../lib/helpers.js";

/** Helpers is the dialog. helpers is the store's helpers answer ({ rows } or
 *  null), plan its helperPlan answer or null, send the store's send. */
export function Helpers({ helpers, plan: planMsg, send, onClose }) {
  const rootRef = useRef(null);
  useInert(rootRef);
  const first = useRef(null);
  const heard = useRef(new Map());
  const [view, setView] = useState({ kind: "list" });
  // The helper a lookup was asked for and has not answered, or "".
  const [pending, setPending] = useState("");
  const [said, setSaid] = useState("");
  const rows = rowsOf(helpers);

  useEffect(() => {
    const el = rootRef.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    send(listCommand(true));
    return () => {
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);
  // The first control takes the keyboard once the list is there, and again when
  // a view changes, on its heading: a screen reader reads the view from the top.
  const showing = rows ? view.kind : "loading";
  useEffect(() => {
    const el = rootRef.current;
    if (!el) return;
    const head = el.querySelector ? el.querySelector(".helper-head") : null;
    if (view.kind !== "list" && head && head.focus) head.focus();
    else if (first.current && first.current.focus) first.current.focus();
    else if (el.focus) el.focus();
  }, [showing]);
  // A state change is said once, as it happens.
  useEffect(() => {
    if (rows) setSaid(announcement(rows, heard.current));
  }, [helpers]);
  // The answer to a lookup replaces the list with the plan.
  useEffect(() => {
    const p = planOf(planMsg);
    if (!p || !pending || p.id !== pending) return;
    setPending("");
    setView({ kind: "plan", plan: p });
  }, [planMsg]);

  const back = () => {
    setView({ kind: "list" });
    send(listCommand(false));
  };
  const lookup = (id, version) => {
    if (send(planCommand(id, version)) !== false) setPending(id);
  };
  const onKeyDown = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      onClose();
    } else if (e.key === LIST.tab) {
      trapTab(e, rootRef.current);
    }
  };
  const act = (row, verb) => {
    switch (verb) {
      case "install": return lookup(row.id, "");
      case "update": return lookup(row.id, row.update);
      case "repair": return lookup(row.id, row.installed);
      case "start": return send(startCommand(row.id));
      case "stop": return send(stopCommand(row.id));
      case "open": return send(openCommand(row.id));
      case "remove": return setView({ kind: "remove", row, purge: false, dataOnly: false });
      case "delete": return setView({ kind: "remove", row, purge: true, dataOnly: true });
      default: return undefined;
    }
  };

  let body;
  if (!rows) {
    body = html`<p class="dialog-note" role="status">${helpers && helpers.error ? helpers.error : "Loading…"}</p>`;
  } else if (view.kind === "plan") {
    body = html`<${Plan} plan=${view.plan} first=${first} onBack=${back}
      onInstall=${() => { send(installCommand(view.plan)); setView({ kind: "list" }); }} />`;
  } else if (view.kind === "remove") {
    body = html`<${Remove} view=${view} first=${first} onBack=${back}
      onMore=${() => setView({ ...view, purge: true })}
      onRemove=${() => { send(uninstallCommand(view.row.id, view.purge)); setView({ kind: "list" }); }} />`;
  } else {
    body = html`
      <p class="dialog-note">${INTRO_WORDS}</p>
      <p class="dialog-note">${SAFETY_WORDS}</p>
      ${rows.length === 0 ? html`<p class="dialog-note">The desktop lists no helpers.</p>` : null}
      <ul class="helper-list" aria-label=${rows.length === 1 ? "1 helper" : rows.length + " helpers"}>
        ${rows.map((r, i) => html`<${Row} key=${r.id} row=${r} busy=${pending === r.id} first=${i === 0 ? first : null} onAct=${act} />`)}
      </ul>`;
  }

  return html`
    <div class="dialog-scrim" onMouseDown=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="dialog dialog--wide" role="dialog" aria-modal="true" aria-labelledby="helpers-title" tabindex="-1" ref=${rootRef} onKeyDown=${onKeyDown}>
        <h2 id="helpers-title" class="dialog-title">Helper apps</h2>
        ${body}
        <div class="sr-only" role="status" aria-live="polite">${said}</div>
        <div class="dialog-foot">
          <button type="button" class="dialog-btn" ref=${rows && view.kind === "list" && rows.length === 0 ? first : null} onClick=${onClose}>Close</button>
        </div>
      </div>
    </div>
  `;
}

/** Row is one helper: its name, state and what it is, the line it failed with,
 *  and the buttons its state allows. */
function Row({ row, busy, first, onAct }) {
  const [mark, word] = stateWords(row.state);
  const buttons = buttonsFor(row);
  return html`
    <li class="helper-row" data-state=${row.state}>
      <div class="helper-main">
        <div class="helper-title">
          <span class="helper-name">${row.name}</span>
          ${row.installed ? html`<span class="helper-meta">${row.installed}</span>` : null}
          <span class="helper-status" id=${"helper-status-" + row.id}>${mark} ${word}</span>
          ${row.update ? html`<span class="helper-flag">Update available ${row.update}</span>` : null}
        </div>
        <div class="helper-meta">${row.summary}</div>
        ${row.state === "running" && row.url ? html`<div class="helper-path">${row.url}</div>` : null}
        ${row.needsRepair && row.state !== "running"
          ? html`<div class="helper-meta helper-error">the installed program no longer matches what was installed</div>`
          : null}
        ${row.state === "running" && row.owner === "unverified"
          ? html`<div class="helper-meta" title="This platform cannot confirm which process holds the helper's port.">port owner not verified</div>`
          : null}
        ${row.hasData && row.state === "notinstalled" ? html`<div class="helper-path">Its data is kept in ${row.dataDir}</div>` : null}
        ${row.error && (row.state === "failed" || row.state === "starting")
          ? html`<div class="helper-meta helper-error" role="status">${row.error}</div>`
          : null}
        ${row.state === "failed" && row.log.length ? html`<pre class="helper-log" aria-label=${"The last lines " + row.name + " wrote"}>${row.log.join("\n")}</pre>` : null}
      </div>
      <div class="helper-actions">
        ${buttons.map((b, i) => html`<button type="button" key=${b.verb} id=${"helper-" + b.verb + "-" + row.id}
          class=${"dialog-btn" + (b.primary ? " dialog-btn--primary" : "")} disabled=${busy}
          ref=${i === 0 ? first : null} onClick=${() => onAct(row, b.verb)}>${b.label}</button>`)}
      </div>
    </li>
  `;
}

const fact = (label, value, code) => html`<div class="helper-fact"><dt>${label}</dt>${code ? html`<dd><code class="helper-code">${value}</code></dd>` : html`<dd>${value}</dd>`}</div>`;

/** Plan is the confirmation: name, version, where it comes from, how it was
 *  checked and what it may do. A refusal has only Back. */
function Plan({ plan, first, onBack, onInstall }) {
  if (plan.error) {
    return html`
      <h3 class="helper-head" tabindex="-1">Not installed</h3>
      <p class="dialog-note dialog-note--error" role="alert">${plan.error}</p>
      <div class="dialog-foot"><button type="button" id="helper-back" class="dialog-btn" ref=${first} onClick=${onBack}>Back</button></div>`;
  }
  const verb = plan.repair ? "Repair" : "Install";
  const replaces = plan.repair ? " (the installed copy is replaced by a checked one)" : plan.installed ? " (replaces " + plan.installed + ")" : "";
  return html`
    <h3 class="helper-head" tabindex="-1">${verb} ${plan.name} ${plan.version}</h3>
    <p class="dialog-note">${plan.summary}</p>
    <dl class="helper-facts">
      ${fact("Name", plan.name)}
      ${fact("Version", plan.version + replaces)}
      ${fact("Source", plan.url, true)}
      ${fact("Signature", SIGNATURE_WORDS)}
      ${fact("SHA-256", plan.sha256, true)}
    </dl>
    <h4 class="helper-sub">What it may do</h4>
    <ul class="helper-allows">${plan.allows.map((a, i) => html`<li key=${i}>${a}</li>`)}</ul>
    <p class="dialog-note">This describes what it does by design. ${SAFETY_WORDS}</p>
    <div class="dialog-foot">
      <button type="button" id="helper-confirm" class="dialog-btn dialog-btn--primary" onClick=${onInstall}>Install</button>
      <button type="button" id="helper-cancel" class="dialog-btn" ref=${first} onClick=${onBack}>Cancel</button>
    </div>`;
}

/** Remove asks before removing, and asks separately before deleting the data,
 *  which a plain removal keeps. The keyboard starts on Cancel. */
function Remove({ view, first, onBack, onMore, onRemove }) {
  const { row, purge, dataOnly } = view;
  const title = dataOnly ? "Delete " + row.name + " data" : purge ? "Delete " + row.name + " data too" : "Remove " + row.name;
  return html`
    <h3 class="helper-head" tabindex="-1">${title}</h3>
    ${purge
      ? html`<p class="dialog-note dialog-note--error" role="alert">This deletes the folder ${row.dataDir} and everything in it. It cannot be undone.</p>`
      : html`<p class="dialog-note">The program is removed. Its data folder ${row.dataDir} is kept, so installing it again finds it.</p>`}
    <div class="dialog-foot">
      <button type="button" id="helper-remove-confirm" class="dialog-btn dialog-btn--danger" onClick=${onRemove}>${purge ? "Delete data" : "Remove"}</button>
      <button type="button" id="helper-cancel" class="dialog-btn" ref=${first} onClick=${onBack}>Cancel</button>
      ${!purge && row.hasData ? html`<button type="button" id="helper-remove-purge" class="dialog-btn" onClick=${onMore}>Remove and delete its data…</button>` : null}
    </div>`;
}
