// The settings dialog: General, Appearance and Terminal. Every control sends its
// change to the desktop at once and shows it at once; the desktop keeps it and
// every window follows.
//
// The other sections of the old settings page (the keyboard, API keys, Remote,
// GitHub, updates and the rest) belong to the tasks that port those features, and
// are not here.
//
// The dialog is modal. The keyboard goes to the first section tab when it opens
// and back to where it was when it closes, Tab stays inside it, and Escape
// closes it. Nothing a person types is used as markup.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { trapTab } from "./Start.js";
import {
  ACCENTS, CURSORS, FONT_DEFAULT, FONT_MAX, FONT_MIN, FONT_SUGGESTIONS, SCROLLBACK_COUNTS, SCROLLBACK_MAX, SCROLLBACK_MIN,
  THEMES, WHY, fontStack,
} from "../lib/prefs.js";

/** SECTIONS are the parts of the dialog. */
export const SECTIONS = [["general", "General"], ["appearance", "Appearance"], ["terminal", "Terminal"]];

/** Settings is the dialog. prefs are the values in use (prefsOf), change(name,
 *  value) sends one change (setting in lib/prefs.js), and onClose closes it. */
export function Settings({ prefs, change, onClose, section: first = "general" }) {
  const [section, setSection] = useState(SECTIONS.some(([id]) => id === first) ? first : "general");
  const rootRef = useRef(null);
  const tabRef = useRef(null);
  useEffect(() => {
    const el = rootRef.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    if (tabRef.current && tabRef.current.focus) tabRef.current.focus();
    return () => {
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);
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
  const chosen = SECTIONS.findIndex(([id]) => id === section);
  const onTabKey = (e) => {
    if (composing(e)) return;
    const by = e.key === LIST.down || e.key === LIST.right ? 1 : e.key === LIST.up || e.key === LIST.left ? -1 : 0;
    if (!by) return;
    e.preventDefault();
    e.stopPropagation();
    setSection(SECTIONS[(chosen + by + SECTIONS.length) % SECTIONS.length][0]);
    // The new tab takes the keyboard once it is the one in the tab order.
    const root = rootRef.current;
    const next = SECTIONS[(chosen + by + SECTIONS.length) % SECTIONS.length][0];
    const btn = root && [...root.querySelectorAll("button")].find((b) => b.getAttribute("data-section") === next);
    if (btn) btn.focus();
  };
  return html`
    <div class="dialog-scrim" onMouseDown=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="dialog dialog--wide" role="dialog" aria-modal="true" aria-labelledby="settings-title" ref=${rootRef} onKeyDown=${onKeyDown}>
        <h2 id="settings-title" class="dialog-title">Settings</h2>
        <div class="settings-body">
          <div class="settings-nav" role="tablist" aria-label="Settings sections" aria-orientation="vertical">
            ${SECTIONS.map(([id, label]) => html`
              <button type="button" role="tab" class=${"settings-tab" + (id === section ? " is-active" : "")} id=${"settings-tab-" + id}
                data-section=${id} aria-selected=${id === section ? "true" : "false"} aria-controls="settings-panel"
                tabindex=${id === section ? "0" : "-1"} ref=${id === section ? tabRef : null}
                onKeyDown=${onTabKey} onClick=${() => setSection(id)}>${label}</button>`)}
          </div>
          <div class="settings-panel" role="tabpanel" id="settings-panel" aria-labelledby=${"settings-tab-" + section}>
            ${section === "general" ? html`<${General} prefs=${prefs} change=${change} />` : null}
            ${section === "appearance" ? html`<${Appearance} prefs=${prefs} change=${change} />` : null}
            ${section === "terminal" ? html`<${Terminal} prefs=${prefs} change=${change} />` : null}
          </div>
        </div>
        <div class="dialog-foot">
          <button type="button" class="dialog-btn" onClick=${onClose}>Close</button>
        </div>
      </div>
    </div>
  `;
}

/** Row is a setting's name, a line about it, and its control. */
function Row({ id, label, about, children, error }) {
  return html`
    <div class="set-row">
      <div class="set-text">
        <div class="set-label" id=${id + "-label"}>${label}</div>
        ${about ? html`<div class="set-about" id=${id + "-about"}>${about}</div>` : null}
        ${error ? html`<div class="set-error" role="alert">${error}</div>` : null}
      </div>
      <div class="set-control">${children}</div>
    </div>
  `;
}

/** Switch is an on/off control. */
function Switch({ id, on, onChange, labelledBy }) {
  return html`<button type="button" role="switch" class=${"set-switch" + (on ? " is-on" : "")} id=${id}
    aria-checked=${on ? "true" : "false"} aria-labelledby=${labelledBy} onClick=${() => onChange(!on)}><span class="set-knob"></span></button>`;
}

/** Choice is a row of exclusive options, one in the tab order, moved with the
 *  arrow keys, each arrow choosing as it moves. */
function Choice({ id, label, options, value, onChoose, swatch = false }) {
  const at = Math.max(0, options.findIndex(([v]) => v === value));
  const onKey = (e) => {
    if (composing(e)) return;
    const by = e.key === LIST.right || e.key === LIST.down ? 1 : e.key === LIST.left || e.key === LIST.up ? -1 : 0;
    if (!by) return;
    e.preventDefault();
    e.stopPropagation();
    const next = options[(at + by + options.length) % options.length][0];
    onChoose(next);
    const group = e.currentTarget;
    const btn = group && [...group.querySelectorAll("button")].find((b) => b.getAttribute("data-value") === next);
    if (btn) btn.focus();
  };
  return html`
    <div class=${"set-choice" + (swatch ? " set-choice--swatch" : "")} role="radiogroup" aria-label=${label} id=${id} onKeyDown=${onKey}>
      ${options.map(([v, name, colour], i) => html`
        <button type="button" role="radio" class=${"set-opt" + (i === at ? " is-on" : "")} data-value=${v} aria-checked=${i === at ? "true" : "false"}
          tabindex=${i === at ? "0" : "-1"} aria-label=${swatch ? name : undefined} title=${swatch ? name : undefined} onClick=${() => onChoose(v)}>
          ${swatch ? html`<span class="set-swatch" style=${{ background: colour }} aria-hidden="true"></span>` : name}
        </button>`)}
    </div>
  `;
}

function General({ prefs, change }) {
  const n = prefs.dismissedTips;
  return html`
    <h3 class="set-head">General</h3>
    <p class="set-intro">How new panes start, and the hints.</p>
    <${Row} id="set-auto-review" label="Auto-approve read-only in new panes"
      about="Only a command it is confident is read-only is let through without asking; anything else still asks. A pane opened by hand starts with this setting; a pane started from another one starts the way that pane is set.">
      <${Switch} id="set-auto-review-default" on=${prefs.autoReviewDefault} labelledBy="set-auto-review-label" onChange=${(on) => change("autoReviewDefault", on)} />
    <//>
    <${Row} id="set-hints" label="Hints"
      about=${"One line under the tab bar about something on screen or a feature worth knowing, each sent away for good with its ×. " +
        (n ? (n === 1 ? "One has" : n + " have") + " been sent away." : "None has been sent away.")}>
      <button type="button" class="dialog-btn" id="set-tips" disabled=${!n} onClick=${() => change("resetTips", true)}>Show them again</button>
    <//>
  `;
}

function Appearance({ prefs, change }) {
  return html`
    <h3 class="set-head">Appearance</h3>
    <p class="set-intro">The window's palette and accent. The terminal has its own section.</p>
    <${Row} id="set-theme" label="Theme" about="Follow system uses the light or dark palette your system is set to.">
      <${Choice} id="set-theme-choice" label="Theme" options=${THEMES} value=${prefs.theme} onChoose=${(v) => change("theme", v)} />
    <//>
    <${Row} id="set-accent" label="Accent" about="Used for focus, links and the chosen row. It never changes what a status colour means.">
      <${Choice} id="set-accent-choice" label="Accent" swatch options=${ACCENTS} value=${prefs.accent} onChoose=${(v) => change("accent", v)} />
    <//>
  `;
}

function Terminal({ prefs, change }) {
  const [family, setFamily] = useState(null);
  const [familyError, setFamilyError] = useState("");
  const [lines, setLines] = useState(null);
  const [linesError, setLinesError] = useState("");
  const applyFamily = () => {
    if (family === null) return;
    const sent = change("fontFamily", family);
    setFamilyError(sent ? "" : WHY.fontFamily);
    if (sent) setFamily(null);
  };
  const applyLines = () => {
    if (lines === null) return;
    const n = Number(lines);
    const sent = lines.trim() !== "" && change("scrollback", n);
    setLinesError(sent ? "" : WHY.scrollback);
    if (sent) setLines(null);
  };
  const counts = SCROLLBACK_COUNTS.includes(prefs.scrollback) ? SCROLLBACK_COUNTS : [...SCROLLBACK_COUNTS, prefs.scrollback].sort((a, b) => a - b);
  const enter = (apply) => (e) => {
    if (composing(e)) return;
    if (e.key === LIST.accept) {
      e.preventDefault();
      apply();
    }
  };
  return html`
    <h3 class="set-head">Terminal</h3>
    <p class="set-intro">Every pane's terminal. Changes apply to open panes at once.</p>
    <div class="set-preview" aria-hidden="true" style=${{ fontFamily: fontStack(prefs.fontFamily), fontSize: prefs.fontSize + "px" }}>
      <div><span class="set-dim">~/project $ </span>go test ./...</div>
      <div>ok   example.com/project   4.102s</div>
      <div><span class="set-dim">~/project $ </span><span class=${"set-cursor set-cursor--" + prefs.cursorStyle + (prefs.cursorSteady ? "" : " is-blinking")}></span></div>
    </div>
    <${Row} id="set-font-size" label="Font size" about=${"Between " + FONT_MIN + " and " + FONT_MAX + " pixels."}>
      <div class="set-stepper" role="group" aria-labelledby="set-font-size-label">
        <button type="button" class="dialog-btn" id="set-font-down" aria-label="Smaller" disabled=${prefs.fontSize <= FONT_MIN}
          onClick=${() => change("fontSize", prefs.fontSize - 1)}>−</button>
        <output class="set-value" id="set-font-size-value">${prefs.fontSize} px</output>
        <button type="button" class="dialog-btn" id="set-font-up" aria-label="Larger" disabled=${prefs.fontSize >= FONT_MAX}
          onClick=${() => change("fontSize", prefs.fontSize + 1)}>+</button>
        <button type="button" class="dialog-btn" id="set-font-reset" disabled=${prefs.fontSize === FONT_DEFAULT}
          onClick=${() => change("fontSize", FONT_DEFAULT)}>Reset</button>
      </div>
    <//>
    <${Row} id="set-font-family" label="Font" about="Any monospaced font on this computer, by its name. Empty is the default." error=${familyError}>
      <input type="text" class="dialog-field" id="set-font-family-field" placeholder="Default" autocomplete="off" spellcheck="false"
        list="set-fonts" aria-labelledby="set-font-family-label" aria-describedby="set-font-family-about" aria-invalid=${familyError ? "true" : undefined}
        value=${family !== null ? family : prefs.fontFamily} onInput=${(e) => setFamily(e.target.value)}
        onKeyDown=${enter(applyFamily)} onChange=${applyFamily} onBlur=${applyFamily} />
      <datalist id="set-fonts">${FONT_SUGGESTIONS.map((f) => html`<option key=${f} value=${f}></option>`)}</datalist>
    <//>
    <${Row} id="set-scrollback" label="Scrollback" about="Lines each pane keeps to scroll back through. More costs memory in every pane." error=${linesError}>
      <select class="dialog-field" id="set-scrollback-list" aria-labelledby="set-scrollback-label" value=${String(prefs.scrollback)}
        onChange=${(e) => { setLines(null); setLinesError(""); change("scrollback", Number(e.target.value)); }}>
        ${counts.map((n) => html`<option key=${n} value=${String(n)}>${n.toLocaleString("en") + " lines"}</option>`)}
      </select>
      <input type="number" class="dialog-field" id="set-scrollback-custom" min=${SCROLLBACK_MIN} max=${SCROLLBACK_MAX} aria-label="Scrollback, a number of lines"
        placeholder="Other number of lines" value=${lines !== null ? lines : ""} onInput=${(e) => setLines(e.target.value)}
        onKeyDown=${enter(applyLines)} onChange=${applyLines} onBlur=${applyLines} />
    <//>
    <${Row} id="set-cursor" label="Cursor" about="The shape of the cursor in every terminal.">
      <${Choice} id="set-cursor-choice" label="Cursor shape" options=${CURSORS} value=${prefs.cursorStyle} onChoose=${(v) => change("cursorStyle", v)} />
    <//>
    <${Row} id="set-steady" label="Steady cursor" about="Stops the cursor blinking. Your system's reduced motion setting stops it too.">
      <${Switch} id="set-cursor-steady" on=${prefs.cursorSteady} labelledBy="set-steady-label" onChange=${(on) => change("cursorSteady", on)} />
    <//>
    <${Row} id="set-screen-reader" label="Screen reader support"
      about="Keeps a readable copy of each terminal's lines, which is what lets a screen reader read what the agents write. It costs every terminal some speed, so it is off until you ask for it.">
      <${Switch} id="set-screen-reader-switch" on=${prefs.screenReader} labelledBy="set-screen-reader-label" onChange=${(on) => change("screenReader", on)} />
    <//>
  `;
}
