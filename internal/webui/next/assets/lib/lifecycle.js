// What starting an agent and ending, restarting or locking a pane need that is
// not drawing: reading the agent catalog the state push carries, building the
// commands the desktop takes, and the sentences a person is shown before
// something is stopped. Everything here is a pure function of what the desktop
// sent, so a test can run it without a page.
//
// The words in a warning are written here and not taken from the agent: a
// pane's name, task or tool is never put into one.

/** NO_AGENT is what a catalog with nothing usable in it chooses. */
export const NO_AGENT = Object.freeze({ agent: "", model: "" });

/** catalogOf is the catalog of a state push, or an empty one. A push that lacks
 *  it (a viewer is sent a cut-down state) has no agents to choose from. */
export function catalogOf(state) {
  const c = state && state.agents && typeof state.agents === "object" ? state.agents : {};
  return {
    items: Array.isArray(c.items) ? c.items.filter((a) => a && typeof a === "object" && typeof a.id === "string" && a.id) : [],
    roles: Array.isArray(c.roles) ? c.roles.filter((r) => r && typeof r === "object" && typeof r.id === "string" && r.id) : [],
    default: c.default && typeof c.default === "object" ? c.default : NO_AGENT,
    project: c.project && typeof c.project === "object" ? c.project : null,
    err: typeof c.err === "string" ? c.err : "",
  };
}

/** valueOf and splitValue write and read the one string a select holds for an
 *  agent and a model. A newline cannot be in either. */
export const valueOf = (agent, model) => String(agent || "") + "\n" + String(model || "");
export function splitValue(value) {
  const v = String(value || "");
  const i = v.indexOf("\n");
  return i < 0 ? ["", ""] : [v.slice(0, i), v.slice(i + 1)];
}

/** optionsOf is every agent and model of the catalog as the entries of a select:
 *  { value, label, group, disabled }. An agent not installed is listed and
 *  cannot be chosen, with where to get it in its group name, so a person who has
 *  not installed it still learns it exists. An agent that names no models stands
 *  as one entry for whatever model it is set to. */
export function optionsOf(catalog) {
  const out = [];
  for (const a of catalog.items) {
    const name = String(a.name || a.id);
    const group = a.available === false ? name + " (not installed" + (a.install ? ", see " + a.install : "") + ")" : name;
    const models = Array.isArray(a.models) && a.models.length ? a.models : [{ id: a.defaultModel || "" }];
    for (const m of models) {
      const id = String((m && m.id) || "");
      out.push({
        value: valueOf(a.id, id),
        label: String((m && (m.name || m.id)) || "Default"),
        group,
        disabled: a.available === false,
      });
    }
  }
  return out;
}

/** defaultValue is the entry a picker starts on: the project's own choice, then
 *  the one for every project, then the first agent that can be started. An
 *  agent the catalog no longer lists, or that is not installed, is passed over. */
export function defaultValue(catalog) {
  const options = optionsOf(catalog);
  const usable = (agent, model) => {
    const o = options.find((x) => x.value === valueOf(agent, model) && !x.disabled);
    if (o) return o.value;
    // An empty model means the agent's own default.
    const a = catalog.items.find((x) => x.id === agent && x.available !== false);
    return a ? valueOf(a.id, model || a.defaultModel || "") : "";
  };
  for (const pick of [catalog.project, catalog.default]) {
    if (pick && pick.agent) {
      const v = usable(pick.agent, pick.model);
      if (v) return v;
    }
  }
  const first = options.find((o) => !o.disabled);
  return first ? first.value : "";
}

/** roleOptions are the roles a pane can be started in: the id and the name. */
export const roleOptions = (catalog) => catalog.roles.map((r) => ({ id: r.id, name: String(r.name || r.id) }));

/** The manager role is the one a team starts with. */
export const MANAGER_ROLE = "manager";
export const managerRole = (catalog) => catalog.roles.find((r) => r.id === MANAGER_ROLE) || null;

/** newTabCommand is the command that opens a tab: an agent tab on the agent and
 *  model chosen, or on the project's default when none is, or a shell tab. */
export function newTabCommand({ kind = "agent", agent = "", model = "", title = "", path = "" } = {}) {
  const cmd = { cmd: "newTab", kind: kind === "shell" ? "shell" : "agent" };
  if (cmd.kind === "agent" && agent) {
    cmd.agent = agent;
    if (model) cmd.model = model;
  }
  if (title) cmd.text = title;
  if (path) cmd.path = path;
  return cmd;
}

/** startAgentCommand is the command that starts one agent with a task in the
 *  project at root, or in a fresh worktree cut from it when worktree is true. */
export function startAgentCommand({ root, agent = "", model = "", task, role = "", id = "", worktree = false }) {
  const cmd = { cmd: "startAgent", root: String(root || ""), task: String(task || ""), worktree: worktree === true };
  if (agent) cmd.agent = agent;
  if (model) cmd.model = model;
  if (role) cmd.role = role;
  if (id) cmd.id = id;
  return cmd;
}

/** startProblem is why a start cannot be sent yet, or "". A task is needed for
 *  anything that goes through startAgent, which is what a role and a team use. */
export function startProblem({ mode, task, role, root, picked, worktree = false }) {
  if (mode === "team") {
    if (!String(task || "").trim()) return "Write the goal first.";
    if (!root) return "There is no project open to start a team in.";
    if (!picked) return "There is no agent that can be started.";
    return "";
  }
  if (!picked) return "There is no agent that can be started.";
  if (worktree && !String(task || "").trim()) return "A worktree is made for a task. Write the task first.";
  if ((role || String(task || "").trim() || worktree) && !root) return "There is no project open to start an agent in.";
  if (role && !String(task || "").trim()) return "An agent started in a role needs a task.";
  return "";
}

/** isWaitingOnPerson says the pane is holding a permission request or a question
 *  for the person, from the graph node and the state push's entry. */
export function isWaitingOnPerson(node, pane) {
  if (pane && (pane.permission || pane.ask)) return true;
  return !!node && !!node.waitingOn && node.waitingOn.kind === "user";
}

/** closeWarnings are the sentences shown before a pane is closed, one for each
 *  thing closing it ends or leaves. Empty when there is nothing to say. */
export function closeWarnings(node, pane) {
  const out = [];
  if (isWaitingOnPerson(node, pane)) {
    out.push("This agent is waiting for your answer. Closing it cancels the request and the answer is lost.");
  }
  const changed = node ? (node.dirty || 0) + (node.untracked || 0) : 0;
  if (changed > 0) {
    out.push(
      "This agent's checkout has " + changed + " changed " + (changed === 1 ? "file" : "files") +
        " that are not committed. Closing the agent does not delete them; they stay on disk.",
    );
  }
  if (node && node.structured) {
    out.push("Flockdeck does not keep a structured agent's conversation, so it is gone once the pane is closed.");
  }
  const unread = pane && pane.unread > 0 ? pane.unread : 0;
  if (unread > 0) {
    out.push(unread + " unread " + (unread === 1 ? "message" : "messages") + " for this agent will be dropped.");
  }
  return out;
}

/** isRunning says the pane is an agent that has not ended, so closing it stops
 *  something. A shell, or an agent that exited or failed, is not. */
function isRunning(node, pane) {
  if ((node && node.shell) || (pane && pane.shell)) return false;
  const status = (node && node.status) || (pane && pane.status) || "";
  return !!(node || pane) && status !== "exited" && status !== "failed";
}

/** closeTabWarnings are the sentences shown before a tab is closed, over the
 *  panes it holds. nodes maps a pane id to its graph node and panes is the state
 *  push's entry for each; either may lack a pane. Empty when the tab holds
 *  nothing running and nothing to say. */
export function closeTabWarnings(tab, nodes, panes) {
  const ids = tab && Array.isArray(tab.panes) ? tab.panes : [];
  let running = 0, waiting = 0, changed = 0, structured = 0, unread = 0;
  for (const id of ids) {
    const node = (nodes && nodes.get && nodes.get(id)) || null;
    const pane = (panes && panes[id]) || null;
    if (isRunning(node, pane)) running++;
    if (isWaitingOnPerson(node, pane)) waiting++;
    if (node) changed += (node.dirty || 0) + (node.untracked || 0);
    if ((node && node.structured) || (pane && pane.structured)) structured++;
    if (pane && pane.unread > 0) unread += pane.unread;
  }
  const out = [];
  const many = (n, one, more) => n + " " + (n === 1 ? one : more);
  if (running > 0) out.push("Closing this tab ends " + many(running, "running agent.", "running agents."));
  if (waiting > 0) out.push(many(waiting, "agent in it is", "agents in it are") + " waiting for your answer. Closing the tab cancels the " + (waiting === 1 ? "request" : "requests") + ".");
  if (changed > 0) out.push("The checkouts in it have " + many(changed, "changed file", "changed files") + " that are not committed. They stay on disk.");
  if (structured > 0) out.push("Flockdeck does not keep the conversation of " + many(structured, "structured agent", "structured agents") + ", so it is gone once the tab is closed.");
  if (unread > 0) out.push(many(unread, "unread message", "unread messages") + " for these agents will be dropped.");
  return out;
}

/** restartWarnings are the sentences shown before a pane is restarted. */
export function restartWarnings(node, pane) {
  const out = [];
  if (isWaitingOnPerson(node, pane)) {
    out.push("This agent is waiting for your answer. Restarting cancels the request.");
  }
  if (node && node.structured) {
    out.push("The agent starts a new session. Its earlier conversation stays above a line that says it was restarted, but the new session does not remember it.");
  } else if (node && !node.shell && node.status !== "exited" && node.status !== "failed") {
    out.push("The agent's process is stopped and started again in the same directory.");
  }
  return out;
}

/** paneIdsOf lists the panes in a tab's layout tree. */
function paneIdsOf(node, out = []) {
  if (!node || typeof node !== "object") return out;
  if (node.pane) out.push(String(node.pane));
  if (Array.isArray(node.children)) for (const c of node.children) paneIdsOf(c, out);
  return out;
}

/** tabsOf is the tabs of a state push: id, title, whether the title was chosen by
 *  hand, what the tab is doing and whether it is the one on the desk. */
export function tabsOf(state) {
  const tabs = state && Array.isArray(state.tabs) ? state.tabs : [];
  const active = state && state.activeTab ? String(state.activeTab) : "";
  return tabs
    .filter((t) => t && typeof t === "object" && t.id)
    .map((t) => ({
      id: String(t.id),
      title: String(t.title || ""),
      named: !!t.named,
      activity: String(t.activity || ""),
      active: String(t.id) === active,
      panes: paneIdsOf(t.root),
    }));
}

/** moveTabCommand is the command that moves a tab one place left (dir -1) or
 *  right (dir 1), or null when it is already at that end. The desktop takes the
 *  tab the moved one goes in front of, and an empty one for the end. */
export function moveTabCommand(tabs, id, dir) {
  const at = tabs.findIndex((t) => t.id === id);
  if (at < 0) return null;
  if (dir < 0) return at === 0 ? null : { cmd: "moveTab", id, target: tabs[at - 1].id };
  if (at === tabs.length - 1) return null;
  const after = tabs[at + 2];
  return { cmd: "moveTab", id, target: after ? after.id : "" };
}
