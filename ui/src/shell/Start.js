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
// "Start in a worktree" cuts a fresh worktree and branch from the project for the
// agent, so two agents do not edit the same files; it needs a task, which names
// the branch.
//
// The dialog is modal. The keyboard goes to its first field when it opens and
// back to where it was when it closes, Tab stays inside it, and Escape closes
// it. What the person types is sent as text in a command and is never used as
// markup.
import { useEffect, useMemo, useRef, useState } from "../vendor/hooks.mjs";
import { useInert } from "./inert.js";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { trapTab } from "./focus.js";
import {
  defaultValue, managerRole, newTabCommand, optionsOf, presetTaskFor, roleOptions, roleTreeDefault, splitValue, startAgentCommand, startProblem, TEAM_HELPER_CAP, TEAM_PRESETS,
} from "../lib/lifecycle.js";

/** WORKTREE_WORDS is what the option says. */
export const WORKTREE_WORDS = "Ticked for a role that works on its own branch. Cuts a new worktree and branch from the project for this agent, so it cannot edit the files another agent is working on. It needs a task. The branch is kept when the worktree is removed.";

/** TITLES are the dialog's names. */
export const TITLES = { agent: "Start an agent", team: "Start a team" };

export { trapTab };

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
  const [preset, setPreset] = useState("");
  const [task, setTask] = useState("");
  const [tree, setTree] = useState(false);
  // Choosing a role sets the box to that role's default until the person has set it themselves.
  const treeSet = useRef(false);
  const [problem, setProblem] = useState("");
  const rootRef = useRef(null);
  useInert(rootRef);
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
    const inTree = !team && tree;
    const why = startProblem({ mode, task, role: team ? "manager" : roleId, root, picked: !!agent, worktree: inTree });
    if (why || (team && !role)) {
      setProblem(why || "There is no manager role in the catalog, so there is nothing to start a team with.");
      return;
    }
    if (!team && !roleId && !task.trim() && !inTree) {
      send(newTabCommand({ kind: "agent", agent, model }));
    } else {
      send(startAgentCommand({ root, agent, model, task: task.trim(), role: team ? "manager" : roleId, worktree: inTree }));
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
  const [mAgent, mModel] = splitValue(pick);
  const managerWords = (role ? role.name || role.id : "manager") + (mAgent ? " (" + mAgent + (mModel ? " · " + mModel : "") + ")" : "");
  const explain = team
    ? "This will start: one agent, a " + managerWords + ", in " + (project ? project.name : "this project") +
      ", with your goal as its task. Nothing else starts now. The manager then starts the helpers it needs, at most " + TEAM_HELPER_CAP + " open at a time."
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
          ? html`<label class="dialog-label" for="start-preset">Start from</label>
              <select id="start-preset" class="dialog-field" value=${preset} onChange=${(e) => {
                const p = TEAM_PRESETS.find((x) => x.id === e.target.value);
                setPreset(e.target.value);
                if (p) setTask((t) => presetTaskFor(t, p));
              }}>
                <option value="">A blank goal</option>
                ${TEAM_PRESETS.map((p) => html`<option key=${p.id} value=${p.id}>${p.name}</option>`)}
              </select>`
          : html`<label class="dialog-label" for="start-role">Role</label>
              <select id="start-role" class="dialog-field" value=${roleId} onChange=${(e) => { setRoleId(e.target.value); if (!treeSet.current) setTree(roleTreeDefault(catalog, e.target.value)); }}>
                <option value="">No role</option>
                ${roles.map((r) => html`<option key=${r.id} value=${r.id}>${r.name}</option>`)}
              </select>`}
        <label class="dialog-label" for="start-task">${team ? "Goal" : "Task (optional)"}</label>
        <textarea id="start-task" class="dialog-field dialog-task" rows="4" spellcheck="false" aria-describedby="start-explain"
          placeholder=${team ? "What should the team get done?" : "What should the agent do first?"}
          value=${task} onInput=${(e) => setTask(e.target.value)}></textarea>
        ${team
          ? null
          : html`<label class="dialog-check">
              <input type="checkbox" id="start-worktree" checked=${tree} aria-describedby="start-worktree-note" onChange=${(e) => { treeSet.current = true; setTree(e.target.checked); }} />
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
