// The Agents & models section of the settings: the agent and model a pane starts
// with when nobody chooses, for every project and for the one on screen. Each
// control sends its change at once; the catalog that comes back shows it. Agent
// names came from agents.json and are drawn as text.
import { useEffect } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Row } from "./Controls.js";
import { NO_CHOICE, defaultCommand, groupsOf, modelsOf } from "../lib/agentsettings.js";

/** AgentSettings is the section. agents is the state's agent catalog, project the
 *  name of the active project ("" when none is open), remote that this window came
 *  through the relay, and send the store's. */
export function AgentSettings({ agents, project, remote, send }) {
  const m = modelsOf(agents);
  // The agents are read again as the section opens: a program installed while this
  // window was open is what the person is about to look for.
  useEffect(() => {
    if (!remote) send({ cmd: "refreshAgents" });
  }, []);
  if (!m.ready) {
    return html`<h3 class="set-head">Agents & models</h3><p class="set-intro" role="status">${m.err || "Reading the agents…"}</p>`;
  }
  const groups = groupsOf(m.options);
  const choose = (every) => (e) => {
    const cmd = defaultCommand(e.target.value, every);
    if (cmd) send(cmd);
  };
  const select = (id, label, value, every, extra) => html`
    <select class="dialog-field" id=${id} aria-labelledby=${label} aria-describedby=${label.replace("-label", "-about")} value=${value} disabled=${!every && !project} onChange=${choose(every)}>
      ${extra}
      ${groups.map((g) => html`<optgroup key=${g.name} label=${g.name}>
        ${g.items.map((o) => html`<option key=${o.value} value=${o.value} disabled=${o.disabled}>${o.label}</option>`)}
      </optgroup>`)}
    </select>`;
  return html`
    <h3 class="set-head">Agents & models</h3>
    <p class="set-intro">The agent, and which of its models, a pane starts with when nobody chooses. To choose for one pane, start it with Choose agent.</p>
    <${Row} id="set-agent-all" label="Every project" about="What a project with no choice of its own starts. Kept in agents.json.">
      ${select("set-agent-all-list", "set-agent-all-label", m.every, true, null)}
    <//>
    <${Row} id="set-agent-project" label=${project ? "This project, " + project : "This project"}
      about=${m.project === NO_CHOICE ? "Starts what every project does until it is given an agent of its own." : "This project's own choice, which wins over the one for every project."}>
      ${select("set-agent-project-list", "set-agent-project-label", m.project, false, html`<option value=${NO_CHOICE}>Same as every project</option>`)}
    <//>
  `;
}
