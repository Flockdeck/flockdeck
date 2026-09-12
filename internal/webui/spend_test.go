package webui

import (
	"regexp"
	"testing"
)

// A chat pane's header says what the conversation has cost, as an estimate:
// "~$" in the header, "estimate" and the price table's date in what a screen
// reader and the tooltip say. With no price it says tokens instead, and a
// figure with unpriced tokens in it is marked as a floor.
func TestPaneHeaderShowsSpendAsAnEstimate(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const priced = { usd: 1.2371, source: "table", checked: "2026-06-24", tokens: 84210, in: 83000, out: 1210, cacheRead: 60000 };
h.recv(fixture({ panes: { p1: pane("p1", { agent: "anthropic", spend: priced }), p2: pane("p2") } }));
const head = (id) => h.$("workspace").querySelectorAll(".pane-header")[id === "p1" ? 0 : 1];
const spend = head("p1").querySelector(".pane-spend");
assert.strictEqual(spend.textContent, "~$1.24", "the header's money figure");
assert.ok(/estimate/.test(spend.getAttribute("aria-label")), "the figure is not said to be an estimate: " + spend.getAttribute("aria-label"));
assert.ok(/24 Jun 2026/.test(spend.dataset.tip), "the tooltip does not say how old the price is: " + spend.dataset.tip);
assert.ok(/not your bill/.test(spend.dataset.tip), "the tooltip does not say it is not the bill");
assert.ok(/60,000 cached/.test(spend.dataset.tip), "the tooltip does not break the tokens down: " + spend.dataset.tip);

// The second pane's agent has reported nothing, so it shows nothing at all.
assert.strictEqual(head("p2").querySelector(".pane-spend").textContent, "", "a pane that reported nothing shows a figure");
assert.strictEqual(head("p2").querySelector(".pane-limit").textContent, "", "a pane that reported nothing shows a limit");
assert.ok(!head("p2").querySelector(".pane-spend").dataset.tip, "a pane that reported nothing has a tooltip");

h.recv(fixture({ panes: { p1: pane("p1", { agent: "ollama", spend: { tokens: 84210, in: 80000, out: 4210 } }), p2: pane("p2") } }));
assert.strictEqual(spend.textContent, "84k tok", "with no price the header shows tokens");
assert.ok(!/~\$/.test(spend.getAttribute("aria-label")), "a pane with no price is given one");

h.recv(fixture({ panes: { p1: pane("p1", { spend: { usd: 0.0421, unpriced: true, source: "table", checked: "2026-06-24", tokens: 900 } }), p2: pane("p2") } }));
assert.strictEqual(spend.textContent, "~$0.042+", "a figure with unpriced tokens in it is not marked as a floor");
`)
}

// A Claude pane on a subscription leads with its tightest window, coloured at
// the thresholds, with a bar filling under it; its dollars are what the tokens
// would cost on the API, and go in the tooltip rather than the header.
func TestPaneHeaderLeadsWithTheLimitForASubscriber(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const resets = Math.floor(Date.now() / 1000) + 2 * 3600;
const sub = (pct, level) => ({ usd: 1.31, source: "agent", tokens: 85200, in: 84000, out: 1200,
  windows: [{ name: "five_hour", pct, resetsAt: resets, level, asOf: Math.floor(Date.now() / 1000) },
            { name: "seven_day", pct: 12, level: "" }] });
h.recv(fixture({ panes: { p1: pane("p1", { spend: sub(72, "") }), p2: pane("p2") } }));
const head = h.$("workspace").querySelectorAll(".pane-header")[0];
const spend = head.querySelector(".pane-spend"), limit = head.querySelector(".pane-limit");
assert.strictEqual(limit.querySelector(".limit-text").textContent, "5h 72%", "the tightest window");
assert.strictEqual(limit.querySelector(".limit-fill").style.width, "72%", "the bar is not filled to the window");
assert.ok(!limit.classList.contains("warn") && !limit.classList.contains("full"), "72% is coloured");
assert.strictEqual(spend.textContent, "85k tok", "a subscriber's header shows dollars: " + spend.textContent);
assert.ok(/would cost on the API/.test(spend.dataset.tip), "the dollars are not said to be the API price: " + spend.dataset.tip);
assert.ok(/Claude Code puts this session at ~\$1\.31/.test(spend.dataset.tip), "Claude Code's own figure is not named as its own");
assert.ok(/five-hour/.test(limit.dataset.tip) && /weekly/.test(limit.dataset.tip) && /resets/.test(limit.dataset.tip),
  "the tooltip does not list every window and its reset: " + limit.dataset.tip);
assert.ok(/share/.test(limit.dataset.tip), "the tooltip does not say the limit is the login's");
assert.ok(/72%/.test(limit.getAttribute("aria-label")), "the limit's aria-label");

h.recv(fixture({ panes: { p1: pane("p1", { spend: sub(82, "warn") }), p2: pane("p2") } }));
assert.ok(limit.classList.contains("warn"), "82% is not amber");
h.recv(fixture({ panes: { p1: pane("p1", { spend: sub(97, "full") }), p2: pane("p2") } }));
assert.ok(limit.classList.contains("full") && !limit.classList.contains("warn"), "97% is not red");

// On an API key there is no window, and the header shows Claude Code's own
// figure for the session.
h.recv(fixture({ panes: { p1: pane("p1", { spend: { usd: 0.5, source: "agent", tokens: 1000 } }), p2: pane("p2") } }));
assert.strictEqual(spend.textContent, "~$0.500", "an API-key Claude pane's session cost");
assert.strictEqual(limit.textContent, "", "a limit is shown with no window");
assert.ok(/Claude Code's own estimate/.test(spend.dataset.tip), "the source is not named: " + spend.dataset.tip);
`)
}

// Whether Claude Code's limits are read is chosen in Settings, beside the
// agents, and sent as the setting the server keeps.
func TestTheStatusLineIsChosenInSettings(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("settings");
h.click(h.$("settings-tab-agents"));
const pick = h.$("set-statusline");
assert.ok(pick, "there is no choice for Claude Code's limits");
assert.strictEqual(pick.value, "auto", "the default is not only where a status line is kept");
pick.value = "on";
pick.onchange();
assert.deepStrictEqual(h.commands().pop(), { cmd: "statusLine", text: "on" });
`)
}

// The new figures are clipped with an ellipsis like the usage figure beside
// them, so a cut-off amount does not read as a smaller one.
func TestSpendFiguresAreClippedWithAnEllipsis(t *testing.T) {
	css := readAsset(t, "app.css")
	if !regexp.MustCompile(`(?m)^\.pane-spend\s*\{[^}]*text-overflow:\s*ellipsis`).MatchString(css) {
		t.Error("the spend figure is clipped without an ellipsis")
	}
	if !regexp.MustCompile(`(?m)^\.pane-limit \.limit-text\s*\{[^}]*text-overflow:\s*ellipsis`).MatchString(css) {
		t.Error("the limit figure is clipped without an ellipsis")
	}
}

// A routed pane's arrow is the end of its agent label, which is where a header
// also carrying spend and a limit cuts it short. The arrow is kept out of the
// cut, so a narrow pane still says its model was routed.
func TestTheRoutingArrowOutlastsACutAgentLabel(t *testing.T) {
	css := readAsset(t, "app.css")
	if !regexp.MustCompile(`(?m)^\.pane-agent \.agent-name\s*\{[^}]*text-overflow:\s*ellipsis`).MatchString(css) {
		t.Error("the agent label is not cut on its own, apart from the arrow")
	}
	if !regexp.MustCompile(`(?m)^\.pane-agent \.agent-route\s*\{[^}]*flex:\s*none`).MatchString(css) {
		t.Error("the routing arrow shrinks with the agent label, so a narrow pane loses it first")
	}
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ panes: { p1: pane("p1", { agent: "claude", model: "haiku", routed: "run the tests", routedFrom: "sonnet", route: "down",
  spend: { usd: 0.5, source: "agent", tokens: 84210, windows: [{ name: "five_hour", pct: 72 }] } }), p2: pane("p2") } }));
const head = h.$("workspace").querySelectorAll(".pane-header")[0];
const agent = head.querySelector("span.pane-agent");
assert.strictEqual(agent.querySelector("span.agent-name").textContent, "claude · haiku");
assert.strictEqual(agent.querySelector("span.agent-route").textContent, " ↘");
assert.strictEqual(head.querySelector(".pane-spend").textContent, "84k tok", "a routed pane lost its spend");
assert.ok(head.querySelector(".pane-limit").textContent.startsWith("5h 72%"), "a routed pane lost its limit");
`)
}
