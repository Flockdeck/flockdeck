package webui

import "testing"

// The Agents overview and the Team roster draw a pane from the same pieces
// (paneDot, paneRoleTag, paneBranchChip, paneAgentBadge, paneLink, paneRow).
// What differs between the lists is which chips a row carries and their order;
// what a chip looks like, says to a screen reader and shows in its bubble must
// not differ for the same pane.
func TestPaneListsDrawTheSameChipsTheSameWay(t *testing.T) {
	runFrontEnd(t, teamPrelude+`
const sig = (n) => n ? [n.tagName, n.className, n.dataset.tip, n.getAttribute("aria-label"), n.getAttribute("role"), n.getAttribute("aria-hidden"), n.textContent].join("|") : "(none)";
const chips = (row) => ({
  dot: sig(row.querySelector("span.dot")),
  role: sig(row.querySelector("span.agent-role")),
  agent: sig([...row.querySelector(".agent-meta").children].filter(Boolean).find((n) => n.textContent === "claude \u00b7 opus")),
  branch: sig([...row.querySelector(".agent-meta").children].filter(Boolean).find((n) => n.textContent.startsWith("\u2387"))),
  status: sig(row.querySelector("span.agent-status")),
});

h.hello();
h.recv(withRoles());
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "boss", name: "boss", status: "working", activity: "working",
    branch: "main", agent: "claude", model: "opus", role: "manager" },
] });
const fromAgents = chips(h.$("agent-p1"));
for (const [k, v] of Object.entries(fromAgents)) assert.ok(v !== "(none)" && !v.startsWith("undefined"), "agents row has no " + k);

h.key({ key: "Escape" });
paletteRun("roster");
h.recv(team());
const fromTeam = chips(h.$("team-pane-p1"));
assert.deepStrictEqual(fromTeam, fromAgents, "one pane drew differently in the two lists");

// The decoration dot is hidden from a screen reader in both.
assert.strictEqual(h.$("team-pane-p1").querySelector("span.dot").getAttribute("aria-hidden"), "true");
assert.strictEqual(h.$("team-pane-p1").className, "agent-row team-pane");
`)
}

// The by-project view and the flat view use one row builder; a row in either
// keeps its id, its click target and its parent link.
func TestBothAgentViewsUseOneRowBuilder(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("summary"));
const items = [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "planner", name: "planner", status: "working", branch: "main", dirty: 2 },
  { paneId: "p2", tabId: "t1", root: "C:/repo", project: "repo", tab: "helper", name: "helper", status: "waiting", waiting: "a question", parent: "p1", for: 90 },
];
h.recv({ type: "agents", items });
const flat = [...h.$("overlay-body").querySelectorAll("div.agent-row")].map((r) => r.outerHTML || r.textContent);
h.click(h.$("agent-view-project"));
const tree = [...h.$("overlay-body").querySelectorAll("div.agent-row")];
assert.deepStrictEqual(tree.map((r) => r.id).sort(), ["agent-p1", "agent-p2"]);
assert.ok(tree.every((r) => r.className.split(" ")[0] === "agent-row"));
assert.ok(h.$("agent-p2").textContent.includes("needs: a question"));
assert.ok(h.$("agent-p1").textContent.includes("2 uncommitted"));
assert.strictEqual(flat.length, 2);
h.click(h.$("agent-p2"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "revealPane", root: "C:/repo", node: "t1", id: "p2" });
`)
}
