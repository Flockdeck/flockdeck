// The Agents & models section of the settings: the agent and model a pane starts
// with when nobody chooses, for every project and for the one on screen, and the
// address of an agent that talks to one (a local model, a gateway). Each control
// sends its change at once; the catalog that comes back shows it. Agent names and
// addresses came from agents.json and are drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Row } from "./Controls.js";
import { NO_CHOICE, RELAY_ADDRESS_WORDS, addressCommand, answerOf, defaultCommand, groupsOf, modelsOf } from "../lib/agentsettings.js";

/** AgentSettings is the section. agents is the state's agent catalog, project the
 *  name of the active project ("" when none is open), answer the latest answer to
 *  an address, remote that this window came through the relay, and send the store's. */
export function AgentSettings({ agents, project, answer, remote, send }) {
  const m = modelsOf(agents);
  // What is typed into an address field, by agent, until the catalog shows it.
  const [typed, setTyped] = useState({});
  const [waiting, setWaiting] = useState("");
  const [refused, setRefused] = useState({});
  const floor = useRef(answer ? answer.seq : 0);
  // The agents are read again as the section opens: a program installed while this
  // window was open is what the person is about to look for.
  useEffect(() => {
    if (!remote) send({ cmd: "refreshAgents" });
  }, []);
  useEffect(() => {
    if (!answer || answer.seq <= floor.current) return;
    floor.current = answer.seq;
    const a = answerOf(answer);
    if (!a) return;
    setWaiting((w) => (w === a.id ? "" : w));
    setRefused((r) => ({ ...r, [a.id]: a.error }));
    if (!a.error) setTyped((t) => { const { [a.id]: _gone, ...rest } = t; return rest; });
  }, [answer && answer.seq]);

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
  const save = (a) => {
    const text = typed[a.id] !== undefined ? typed[a.id] : a.address;
    const cmd = addressCommand(a.id, text, a.address);
    if (!cmd) {
      setTyped((t) => { const { [a.id]: _gone, ...rest } = t; return rest; });
      return;
    }
    if (send(cmd) === false) return;
    setRefused((r) => ({ ...r, [a.id]: "" }));
    setWaiting(a.id);
  };
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
    ${m.addressable.length
      ? html`<h4 class="set-sub">Addresses</h4>
          <p class="set-intro">Where an agent that talks to a server sends its requests, for example http://127.0.0.1:11434/v1. Its key goes to this address, so it is changed on this machine only.</p>
          ${m.addressable.map((a) => html`
            <${Row} key=${a.id} id=${"set-address-" + a.id} label=${a.name} about=${remote ? RELAY_ADDRESS_WORDS : undefined} error=${refused[a.id] || undefined}>
              <input type="text" class="dialog-field" id=${"set-address-field-" + a.id} autocomplete="off" spellcheck="false" placeholder="http://127.0.0.1:11434/v1"
                aria-labelledby=${"set-address-" + a.id + "-label"} aria-invalid=${refused[a.id] ? "true" : undefined} disabled=${remote || waiting === a.id}
                value=${typed[a.id] !== undefined ? typed[a.id] : a.address} onInput=${(e) => setTyped({ ...typed, [a.id]: e.target.value })}
                onKeyDown=${(e) => { if (!composing(e) && e.key === LIST.accept) { e.preventDefault(); save(a); } }} />
              <button type="button" class="dialog-btn" id=${"set-address-save-" + a.id} disabled=${remote || waiting === a.id} onClick=${() => save(a)}>Save</button>
            <//>`)}`
      : null}
  `;
}
