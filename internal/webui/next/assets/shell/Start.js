// The dialog that starts an agent or a team.
//
// - "agent": the agent and model, a role if one is wanted, and a task. With no
//   task and no role it opens an agent tab, as the plain new-tab key does but on
//   the agent chosen. With a task or a role it starts the agent in the project
//   the window is on (startAgent), and the page opens its drawer when it arrives.
// - "team": a goal, and the manager agent and model. It starts one agent in the
//   manager role with the goal as its task, as `flockdeck spawn -role manager`
//   would; that agent starts the rest.
//
// "Start in a worktree" is drawn and switched off. The worktree view will turn
// it on; until then nothing here can cut one.
//
// The dialog is modal. The keyboard goes to its first field when it opens and
// back to where it was when it closes, Tab stays inside it, and Escape closes
// it. What the person types is sent as text in a command and is never used as
// markup.
import { useEffect, useMemo, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import {
  defaultValue, managerRole, newTabCommand, optionsOf, roleOptions, splitValue, startAgentCommand, startProblem,
} from "../lib/lifecycle.js";

/** WORKTREE_WORDS is what the switched-off option says. */
export const WORKTREE_WORDS = "Start in a worktree is not available in this window yet. The agent starts in the project's own directory.";

/** TITLES are the dialog's names. */
export const TITLES = { agent: "Start an agent", team: "Start a team" };

/** focusables are the controls Tab moves between inside the dialog. */
const focusables = (root) => (root && root.querySelectorAll ? [...root.querySelectorAll("select, textarea, input, button")].filter((n) => !n.disabled) : []);

/** trapTab keeps Tab and Shift+Tab inside the dialog. */
function trapTab(e, root) {
  const list = focusables(root);
  if (list.length === 0) return;
  const doc = root.ownerDocument;
  const at = list.indexOf(doc.activeElement);
  const to = e.shiftKey ? (at <= 0 ? list.length - 1 : at - 1) : (at < 0 || at === list.length - 1 ? 0 : at + 1);
  e.preventDefault();
  list[to].focus();
}

/**
 * Start is the dialog. mode is "agent" or "team"; catalog is the agent catalog
 * of the state push (catalogOf); project is { root, name } of the project the
 * window is on, or null; send is the store's send; onClose closes it.
 */
export function Start({ mode, catalog, project, send, onClose }) {
  const team = mode === "team";
  const role = team ? managerRole(catalog) : null;
  const options = useMemo(() => optionsOf(catalog), [catalog]);
  const [pick, setPick] = useState(() => defaultValue(catalog));
  const [roleId, setRoleId] = useState("");
  const [task, setTask] = useState("");
  const [problem, setProblem] = useState("");
  const rootRef = useRef(null);
  const first = useRef(null);
  const root = project ? project.root : "";

  useEffect(() => {
    const el = rootRef.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    if (first.current && first.current.focus) first.current.focus();
    return () => {
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);

  const roles = useMemo(() => roleOptions(catalog), [catalog]);
  const submit = () => {
    const [agent, model] = splitValue(pick);
    const why = startProblem({ mode, task, role: team ? "manager" : roleId, root, picked: !!agent });
    if (why || (team && !role)) {
      setProblem(why || "There is no manager role in the catalog, so there is nothing to start a team with.");
      return;
    }
    if (!team && !roleId && !task.trim()) {
      send(newTabCommand({ kind: "agent", agent, model }));
    } else {
      send(startAgentCommand({ root, agent, model, task: task.trim(), role: team ? "manager" : roleId }));
    }
    onClose();
  };
  const onKeyDown = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      onClose();
    } else if (e.key === LIST.tab) {
      trapTab(e, rootRef.current);
    } else if (e.key === LIST.accept && (e.ctrlKey || e.metaKey) && e.target && e.target.localName === "textarea") {
      e.preventDefault();
      submit();
    }
  };

  const noAgents = options.length === 0;
  const groups = [];
  for (const o of options) {
    let g = groups.find((x) => x.name === o.group);
    if (!g) groups.push((g = { name: o.group, items: [] }));
    g.items.push(o);
  }
  const explain = team
    ? "Starts one agent, " + (role ? "a " + (role.name || role.id) : "a manager") + ", in " + (project ? project.name : "this project") +
      " with your goal as its task. It then starts the agents it needs. Nothing else starts now."
    : "Opens an agent tab on the agent you choose. Write a task, or choose a role, to start the agent in " +
      (project ? project.name : "the project") + " with that task instead.";

  return html`
    <div class="dialog-scrim" onMouseDown=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="start-title" ref=${rootRef} onKeyDown=${onKeyDown}>
        <h2 id="start-title" class="dialog-title">${TITLES[mode] || TITLES.agent}</h2>
        <p class="dialog-note" id="start-explain">${explain}</p>
        ${catalog.err ? html`<p class="dialog-note dialog-note--warn" role="status">${catalog.err}</p>` : null}
        ${noAgents ? html`<p class="dialog-note dialog-note--warn" role="status">${catalog.err ? "" : "The desktop has not sent the list of agents, so none can be started from this window."}</p>` : null}
        <label class="dialog-label" for="start-agent">${team ? "Manager agent and model" : "Agent and model"}</label>
        <select id="start-agent" class="dialog-field" ref=${first} value=${pick} onChange=${(e) => setPick(e.target.value)} disabled=${noAgents}>
          ${groups.map((g) => html`<optgroup key=${g.name} label=${g.name}>
            ${g.items.map((o) => html`<option key=${o.value} value=${o.value} disabled=${o.disabled}>${o.label}</option>`)}
          </optgroup>`)}
        </select>
        ${team
          ? null
          : html`<label class="dialog-label" for="start-role">Role</label>
              <select id="start-role" class="dialog-field" value=${roleId} onChange=${(e) => setRoleId(e.target.value)}>
                <option value="">No role</option>
                ${roles.map((r) => html`<option key=${r.id} value=${r.id}>${r.name}</option>`)}
              </select>`}
        <label class="dialog-label" for="start-task">${team ? "Goal" : "Task (optional)"}</label>
        <textarea id="start-task" class="dialog-field dialog-task" rows="4" spellcheck="false" aria-describedby="start-explain"
          placeholder=${team ? "What should the team get done?" : "What should the agent do first?"}
          value=${task} onInput=${(e) => setTask(e.target.value)}></textarea>
        ${team
          ? null
          : html`<label class="dialog-check dialog-check--off">
              <input type="checkbox" disabled checked=${false} aria-describedby="start-worktree-note" />
              <span>Start in a worktree</span>
            </label>
            <p class="dialog-note" id="start-worktree-note">${WORKTREE_WORDS}</p>`}
        ${problem ? html`<p class="dialog-note dialog-note--error" role="alert">${problem}</p>` : null}
        <div class="dialog-foot">
          <button type="button" class="dialog-btn dialog-btn--primary" onClick=${submit} disabled=${noAgents}>Start</button>
          <button type="button" class="dialog-btn" onClick=${onClose}>Cancel</button>
          <span class="dialog-hint">Ctrl+Enter in the box starts it.</span>
        </div>
      </div>
    </div>
  `;
}
