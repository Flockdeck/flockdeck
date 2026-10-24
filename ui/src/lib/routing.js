// Routing: whether a helper an agent starts, or the first prompt sent to a pane,
// starts on a smaller or stronger model chosen for the work. The desktop keeps the
// policy in agents.json and sends it with the agents catalogue; this page reads it
// defensively and sends the four settings that change it.
//
// A policy is "every project" or this project's own, which replaces it. The floor,
// the strategy and the switch for sending work to another agent are read from the
// policy that routes the active project and written to the same one.

export const MODES = [["off", "Off"], ["auto", "Automatic"]];
export const FLOORS = [["", "Small"], ["mid", "Mid"], ["top", "Top"]];
export const STRATEGIES = [["", "Cost-first, quality-aware"], ["cost", "Minimise cost"]];

const str = (v) => (typeof v === "string" ? v : "");

const choiceOf = (c) => (c && typeof c === "object" ? { mode: c.mode === "auto" ? "auto" : "off", floor: c.floor === "mid" || c.floor === "top" ? c.floor : "", strategy: c.strategy === "cost" ? "cost" : "" } : null);

/** routingOf reads the catalogue's routing, or null while the desktop has sent none. */
export function routingOf(agents) {
  const r = agents && typeof agents === "object" ? agents.routing : null;
  if (!r || typeof r !== "object") return null;
  const every = choiceOf(r.every) || { mode: "off", floor: "", strategy: "" };
  return {
    every,
    project: choiceOf(r.project),
    crossAgent: r.crossAgent === true,
    builtIn: r.builtIn === true,
    note: str(r.note),
    config: str(r.config),
    rules: (Array.isArray(r.rules) ? r.rules : [])
      .filter((x) => x && typeof x === "object" && typeof x.name === "string")
      .map((x) => ({
        name: x.name,
        choice: str(x.choice),
        when: str(x.when),
        kept: Number.isInteger(x.kept) && x.kept > 0 ? x.kept : 0,
        overridden: Number.isInteger(x.overridden) && x.overridden > 0 ? x.overridden : 0,
      })),
  };
}

/** policyOf is the policy that routes the active project: its own, or every project's. */
export function policyOf(r) {
  return r.project || r.every;
}

/** overriddenWords says how often a rule's choice was changed by hand. */
export function overriddenWords(rule) {
  const total = rule.kept + rule.overridden;
  if (!total) return "";
  return "overridden " + Math.round((100 * rule.overridden) / total) + "% of the time (" + rule.overridden + " of " + total + ")";
}

const ONE_OF = {
  mode: ["off", "auto", ""],
  floor: ["", "mid", "top"],
  strategy: ["", "cost"],
  crossAgent: ["true", "false"],
};

/** routingCommand changes one setting: for every project (all) or the active one.
 *  A value the desktop would refuse is null. */
export function routingCommand(target, text, all) {
  if (!ONE_OF[target] || !ONE_OF[target].includes(text)) return null;
  // Only the project's own policy can be "same as every project" (empty).
  if (target === "mode" && text === "" && all) return null;
  return all ? { cmd: "setRouting", kind: "all", target, text } : { cmd: "setRouting", target, text };
}

export const CLEAR_LOG_COMMAND = { cmd: "clearRoutingLog" };
