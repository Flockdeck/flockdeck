// The Notifications section of the settings: what this window raises when an agent
// stops to wait on you, and what the paired devices are told.
//
// The two are separate. The first is a notification from the browser, shown while
// this window is behind another, and needs the browser's leave. The second is a
// push the desktop sends through the relay to the phones paired with the account:
// it says, encrypted for each device, that an agent is waiting, after the delay
// chosen here and not while the person is at the desk.
//
// Every control sends its change at once and shows it at once, like the rest of
// the settings. The text of a notification is written by the page (lib/attention.js)
// and never carries what an agent asked.
import { html } from "../html.js";
import { Row, Switch } from "./Controls.js";
import { PUSH_DELAYS, delayLabel, permissionWords } from "../lib/attention.js";

/** Notify is the section. prefs are the values in use (prefsOf), change(name,
 *  value) sends one change, permission is where the browser's leave stands
 *  (lib/attention.js permissionOf), machine is this machine's name when it is
 *  enrolled with the relay, pushError is why the last push failed, and onAsk asks
 *  the browser for its leave. */
export function Notify({ prefs, change, permission, machine = "", pushError = "", onAsk }) {
  const push = prefs.push;
  const delays = PUSH_DELAYS.some(([s]) => s === push.delaySeconds) ? PUSH_DELAYS : [...PUSH_DELAYS, [push.delaySeconds, delayLabel(push.delaySeconds)]].sort((a, b) => a[0] - b[0]);
  const there = machine || "this machine";
  const askable = !prefs.notificationsOff && permission === "default";
  return html`
    <h3 class="set-head">Notifications</h3>
    <p class="set-intro">How you hear that an agent is waiting on you.</p>
    <${Row} id="set-notifications" label="Desktop notifications"
      about=${"A notification when an agent stops to wait on you while this window is behind another. " + permissionWords(permission)}>
      <${Switch} id="set-notifications-switch" on=${!prefs.notificationsOff} labelledBy="set-notifications-label"
        onChange=${(on) => { change("notificationsOff", !on); if (on && permission === "default") onAsk(); }} />
      ${askable ? html`<button type="button" class="dialog-btn" id="set-notifications-ask" onClick=${onAsk}>Ask the browser</button>` : null}
    <//>
    <h4 class="set-sub">Paired devices</h4>
    <${Row} id="set-push" label="Notify paired devices"
      about="A push to your paired phones and browsers when an agent has been waiting a while and you are not at this computer."
      error=${pushError ? "The last push failed: " + pushError : ""}>
      <${Switch} id="set-push-switch" on=${!push.off} labelledBy="set-push-label" onChange=${(on) => change("pushOff", !on)} />
    <//>
    <${Row} id="set-push-delay" label="After waiting"
      about="How long an agent has to have been waiting before the devices are told. Each wait is told once.">
      <select class="dialog-field" id="set-push-delay-list" aria-labelledby="set-push-delay-label" disabled=${push.off} value=${String(push.delaySeconds)}
        onChange=${(e) => change("pushDelay", Number(e.target.value))}>
        ${delays.map(([s, name]) => html`<option key=${s} value=${String(s)}>${name}</option>`)}
      </select>
    <//>
    <${Row} id="set-push-anonymous" label="Send nothing identifying"
      about=${"Notifications say only “An agent on " + there + " needs you” (or “3 agents on " + there + " need you”) instead of naming the panes and their projects, which suits a lock screen others can see. " +
        "Either way it is encrypted here for each device, and neither the relay nor the push service can read it."}>
      <${Switch} id="set-push-anonymous-switch" on=${push.anonymous} labelledBy="set-push-anonymous-label" onChange=${(on) => change("pushAnonymous", on)} />
    <//>
  `;
}
