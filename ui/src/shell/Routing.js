// The Routing section of the settings: whether a helper an agent starts, or the
// first prompt sent to a pane, starts on a model chosen for the work. See
// lib/routing.js for the policy and the settings.
//
// The desktop keeps the policy in agents.json and sends it with the agents
// catalogue; each control sends its change at once and the catalogue that comes
// back shows it. The rules are read here and edited in agents.json. The names and
// the sentences of a rule came from that file and are drawn as text.
import { html } from "../html.js";
import { Row } from "./Controls.js";
import { CLEAR_LOG_COMMAND, FLOORS, MODES, STRATEGIES, overriddenWords, policyOf, routingCommand, routingOf } from "../lib/routing.js";

function Pick({ id, label, options, value, disabled = false, onPick }) {
  return html`<select class="dialog-field" id=${id} aria-labelledby=${label} disabled=${disabled} value=${value} onChange=${(e) => onPick(e.target.value)}>
    ${options.map(([v, name]) => html`<option key=${v} value=${v}>${name}</option>`)}
  </select>`;
}

/** Routing is the section. agents is the state's agents catalogue, project the
 *  active project's name ("" when none is open), send the store's send. */
export function Routing({ agents, project, send }) {
  const r = routingOf(agents);
  if (!r) {
    return html`<h3 class="set-head">Routing</h3><p class="set-intro" role="status">${(agents && typeof agents.err === "string" && agents.err) || "Reading the agents…"}</p>`;
  }
  const own = r.project;
  const policy = policyOf(r);
  const all = !own;
  const set = (target, text, everyProject) => {
    const cmd = routingCommand(target, text, everyProject);
    if (cmd) send(cmd);
  };
  return html`
    <h3 class="set-head">Routing</h3>
    <p class="set-intro">Starts a helper or a first prompt on a smaller or stronger model, chosen for the work.</p>
    <${Row} id="set-route-all" label="Every project"
      about="Chooses a smaller model for mechanical work and a stronger one for hard work. Automatic picks the model for a helper an agent starts with flockdeck spawn and for the first prompt you send a pane (the pane restarts on the chosen model), with nothing to confirm first. Off until you turn it on. Kept in agents.json.">
      <${Pick} id="set-route-all-list" label="set-route-all-label" options=${MODES} value=${r.every.mode} onPick=${(v) => set("mode", v, true)} />
    <//>
    <${Row} id="set-route-project" label=${project ? "This project, " + project : "This project"}
      about=${own ? "This project's own policy, which replaces the one for every project." : "Routed as every project is until it is given a policy of its own."}>
      <${Pick} id="set-route-project-list" label="set-route-project-label" disabled=${!project} options=${[["", "Same as every project"], ...MODES]}
        value=${own ? own.mode : ""} onPick=${(v) => set("mode", v, false)} />
    <//>
    <${Row} id="set-route-floor" label="Never go below"
      about=${"The smallest models routing may choose, " + (own ? "for this project." : "for every project.") + " Mid keeps the smallest models off work that matters."}>
      <${Pick} id="set-route-floor-list" label="set-route-floor-label" options=${FLOORS} value=${policy.floor} onPick=${(v) => set("floor", v, all)} />
    <//>
    <${Row} id="set-route-strategy" label="Strategy"
      about="Minimise cost also routes work no rule recognises to the cheapest available model; quality-aware leaves unrecognised work where it was. Either way a rule that matches still decides first, and “Never go below” still holds.">
      <${Pick} id="set-route-strategy-list" label="set-route-strategy-label" options=${STRATEGIES} value=${policy.strategy} onPick=${(v) => set("strategy", v, all)} />
    <//>
    <${Row} id="set-route-cross" label="Routing across agents"
      about=${"A rule may name another agent's model as well as a smaller or stronger model of this one, such as a local model. That pane runs a different program, with its own login, tools and transcript. Off until you turn it on, " + (own ? "for this project." : "for every project.")}>
      <input type="checkbox" id="set-route-cross-box" aria-labelledby="set-route-cross-label" checked=${r.crossAgent}
        onChange=${(e) => set("crossAgent", e.target.checked ? "true" : "false", all)} />
    <//>
    ${r.note ? html`<p class="set-intro" id="set-route-note">${r.note}</p>` : null}
    <${Row} id="set-route-rules" label="Rules"
      about=${(r.builtIn ? "The built-in rules. " : "") + "The first to match a task decides. They are edited in agents.json" + (r.config ? ", at " + r.config : "") + "."}>
    <//>
    ${r.rules.length
      ? html`<ul class="route-rules" aria-label="Routing rules">
          ${r.rules.map((rule, i) => html`<li key=${i} class="route-rule">
            <span class="key-name">${rule.name}</span>
            <span class="key-id">${rule.choice}</span>
            <span class="key-note">when ${rule.when}</span>
            ${overriddenWords(rule) ? html`<span class="key-note">${overriddenWords(rule)}</span>` : null}
          </li>`)}
        </ul>`
      : html`<p class="set-intro">No rules, so routing changes nothing.</p>`}
    <${Row} id="set-route-log" label="Routing history"
      about="What routing chose, as an earlier version recorded it. Nothing is added to it now. It is kept on this machine, holds no task text, and is never sent anywhere.">
      <button type="button" class="dialog-btn" id="set-route-clear-log" onClick=${() => send(CLEAR_LOG_COMMAND)}>Clear routing history</button>
    <//>
  `;
}
