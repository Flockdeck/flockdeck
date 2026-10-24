// The Typed wake section of the settings. Typed wake is how Flockdeck tells an
// agent that a message has arrived: it types a short line into the agent's
// terminal. Here that is switched off for everything, for a kind of agent, or for
// one pane, and an agent that cannot run a command can be opted in to being typed
// the message itself.
//
// Each switch sends its command at once (wakeSwitch) and the desktop sends the
// preferences back, which is what the switches show. The desktop changes these
// only for a window on its own machine, so a window reached through the relay
// shows them and cannot move them. Agent and pane names came from the catalog and
// from agents, and are drawn as text.
//
// The desktop does not send the rule it uses to tell an agent is ready for the
// line, or a log of what was typed, so neither is shown.
import { html } from "../html.js";
import { Row, Switch } from "./Controls.js";
import { WAKE_RELAY_WORDS, wakeCommand, wakeEnabled } from "../lib/wake.js";

/** Wake is the section. wake is prefs.wake (wakeOf), agents the state's agent
 *  catalog, panes the open panes as { pane, name, agent }, remote that this window
 *  came through the relay, and send the store's send. */
export function Wake({ wake, agents, panes = [], remote = false, send }) {
  const kinds = agents && Array.isArray(agents.items) ? agents.items : [];
  const flip = (kind, id, on) => {
    const cmd = wakeCommand(kind, id, on);
    if (cmd) send(cmd);
  };
  // A muted pane that is not open now is still listed, so it can be unmuted.
  const open = new Set(panes.map((p) => p.pane));
  const rows = panes.concat(wake.panes.filter((id) => !open.has(id)).map((id) => ({ pane: id, name: id, agent: "", gone: true })));
  return html`
    <h3 class="set-head">Typed wake</h3>
    <p class="set-intro">When a message arrives for an agent that is idle, Flockdeck types a short line into its terminal so it looks. Any switch that is off wins.</p>
    ${remote ? html`<p class="set-intro" role="status">${WAKE_RELAY_WORDS}</p>` : null}
    <${Row} id="wake-global" label="Typed wake" about="Off stops it for every agent and every pane.">
      <${Switch} id="wake-global-switch" on=${!wake.off} labelledBy="wake-global-label" disabled=${remote} onChange=${(on) => flip("global", "", on)} />
    <//>
    ${kinds.length
      ? html`<h4 class="set-sub">Kinds of agent</h4>
          ${kinds.map((a) => html`<${Row} key=${a.id} id=${"wake-agent-" + a.id} label=${a.name || a.id}
            about=${wake.off ? "Typed wake is off for everything." : ""}>
            <${Switch} id=${"wake-agent-switch-" + a.id} on=${!wake.agents.includes(a.id)} labelledBy=${"wake-agent-" + a.id + "-label"} disabled=${remote}
              onChange=${(on) => flip("agent", a.id, on)} />
          <//>`)}
          <h4 class="set-sub">Typed the message itself</h4>
          <p class="set-intro">An agent that cannot run a command cannot read the message from the fixed line, so it can be typed the message text instead. The desktop allows this only for those agents and says so for the others.</p>
          ${kinds.map((a) => html`<${Row} key=${a.id} id=${"wake-text-" + a.id} label=${a.name || a.id}>
            <${Switch} id=${"wake-text-switch-" + a.id} on=${wake.textAgents.includes(a.id)} labelledBy=${"wake-text-" + a.id + "-label"} disabled=${remote}
              onChange=${(on) => flip("text", a.id, on)} />
          <//>`)}`
      : null}
    <h4 class="set-sub">Panes</h4>
    ${rows.length === 0 ? html`<p class="set-intro">No pane is open.</p>` : null}
    ${rows.map((p) => html`<${Row} key=${p.pane} id=${"wake-pane-" + p.pane} label=${p.name || p.pane}
      about=${p.gone ? "Not open now. Muted until you turn it back on." : !wakeEnabled(wake, p.agent, "") ? "Its kind of agent, or everything, is off." : ""}>
      <${Switch} id=${"wake-pane-switch-" + p.pane} on=${!wake.panes.includes(p.pane)} labelledBy=${"wake-pane-" + p.pane + "-label"} disabled=${remote}
        onChange=${(on) => flip("pane", p.pane, on)} />
    <//>`)}
  `;
}
