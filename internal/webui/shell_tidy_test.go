package webui

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/help"
)

// API keys, Remote and GitHub each had a rail button, a palette action and a
// Settings section drawing the same dialog. The rail buttons are gone; the
// other two routes stay, and each still opens.
func TestAPIKeysRemoteAndGitHubAreReachedWithoutARailButton(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
for (const id of ["btn-apikeys", "btn-remote", "btn-github"]) {
  assert.ok(!h.$(id), id + " is still in the rail");
}
const railButtons = h.$("rail").querySelectorAll("button").filter((b) => !b.hidden && !b.classList.contains("rail-tile"));
assert.ok(railButtons.length <= 12, "the rail has " + railButtons.length + " buttons, from " + railButtons.map((b) => b.id).join(", "));

// The palette route: each opens its own sheet.
for (const [words, sent] of [["api keys", { cmd: "keys" }], ["flockdeck remote", { cmd: "remoteDevices" }]]) {
  h.palette(words);
  assert.ok(!h.$("overlay").hidden, words + " opened nothing from the palette");
  assert.deepStrictEqual(h.commands().pop(), sent);
  h.key({ key: "Escape" });
}
h.palette("github");
assert.ok(!h.$("overlay").hidden, "github opened nothing from the palette");
h.key({ key: "Escape" });

// The Settings route: each has a section that draws the same dialog.
h.click(h.$("btn-settings"));
for (const [section, host] of [["keys", "settings-host"], ["remote", "settings-host"], ["github", "settings-host"]]) {
  h.click(h.$("settings-tab-" + section));
  assert.ok(h.$(host) && h.$("settings-pane").contains(h.$(host)), "Settings has no " + section + " section");
}
`)
}

// The TypeSafe key is typed in API keys; Status detection says whether it is
// set and leads there.
func TestTheTypeSafeKeyIsSetInAPIKeys(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-keys"));
assert.ok(h.$("settings-pane").contains(h.$("set-jev-key-field")), "API keys has no field for the TypeSafe key");
h.click(h.$("settings-tab-status"));
assert.ok(!h.$("settings-pane").contains(h.$("set-jev-key-field")), "Status detection still has the field");
h.recv({ type: "jevKey", set: true, env: false });
assert.ok(h.$("settings-pane").textContent.includes("Status: set."), h.$("settings-pane").textContent);
h.click(h.$("set-jev-key-link"));
assert.strictEqual(h.$("settings-tab-keys").getAttribute("aria-selected"), "true");
assert.ok(h.$("settings-pane").contains(h.$("set-jev-key-clear")), "the key is not shown as set where it is edited");
`)
}

// Updates and versions have a section of their own, apart from the plan.
func TestUpdatesAreInAboutAndUpdatesNotAccountAndPlan(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ update: { version: "9.9.9" } }));
h.click(h.$("btn-settings"));
const names = h.$("settings-tabs").querySelectorAll("button").map((b) => b.textContent);
assert.ok(names.includes("Account & plan") && names.includes("About & updates"), names.join(", "));
h.click(h.$("settings-tab-plan"));
for (const id of ["set-updates", "set-check-update", "set-versions", "set-install"]) {
  assert.ok(!h.$(id) || !h.$("settings-pane").contains(h.$(id)), id + " is still under Account & plan");
}
assert.ok(h.$("settings-pane").contains(h.$("set-plan-link")), "the plan section lost the Enterprise link");
h.click(h.$("settings-tab-about"));
for (const id of ["set-updates", "set-check-update", "set-versions", "set-install"]) {
  assert.ok(h.$(id) && h.$("settings-pane").contains(h.$(id)), id + " is not under About & updates");
}
`)
}

// The Team panel has a shortcut and a rail button, and opens from the Agents
// overview.
func TestTheTeamPanelHasAShortcutARailButtonAndALinkFromAgents(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const k = h.keyTable().find((x) => x.id === "team");
assert.ok(k && k.keys, "the Team action has no binding");
assert.ok((h.$("btn-team").dataset.tip || "").includes(k.keys), "the rail button does not give the key: " + h.$("btn-team").dataset.tip);

h.press("team");
assert.ok(!h.$("overlay").hidden, "the shortcut did not open the Team panel");
assert.strictEqual(h.$("overlay-title").textContent, "Team");
assert.deepStrictEqual(h.commands().filter((c) => c.cmd === "teamWatch").length, 1);
h.press("team");
assert.ok(h.$("overlay").hidden, "the shortcut did not close the panel again");

h.click(h.$("btn-team"));
assert.strictEqual(h.$("overlay-title").textContent, "Team", "the rail button did not open the Team panel");
h.key({ key: "Escape" });

h.click(h.$("btn-agents"));
h.recv({ type: "agents", items: [{ paneId: "p1", project: "repo", name: "one", status: "idle", agent: "claude" }] });
assert.ok(h.$("agents-team"), "the Agents overview has no way to the Team panel");
h.click(h.$("agents-team"));
assert.strictEqual(h.$("overlay-title").textContent, "Team", "the link in Agents did not open the Team panel");
`)
}

// The renamed features carry one name wherever a person reads it.
func TestRenamedFeaturesUseTheirNewNames(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
assert.strictEqual(h.$("btn-history").querySelector(".rail-label").textContent, "Resume");
h.click(h.$("btn-history"));
assert.strictEqual(h.$("overlay-title").textContent, "Resume");
h.key({ key: "Escape" });

// Auto-approve read-only: the pane header control and the default in General.
const pane = h.doc.querySelectorAll("button").find((b) => (b.dataset.tip || "").includes("Auto-approve read-only"));
assert.ok(pane, "no pane control is named Auto-approve read-only");
assert.ok(!/auto-review/i.test(pane.dataset.tip), "the pane control still says auto-review: " + pane.dataset.tip);
h.click(h.$("btn-settings"));
const text = h.$("settings-pane").textContent;
assert.ok(text.includes("Auto-approve read-only in new panes"), text);
assert.ok(!/auto-review/i.test(text), "Settings still says auto-review: " + text);
// The old name still finds it.
const find = h.$("settings-find");
find.value = "auto review";
find.oninput();
assert.ok(h.$("settings-tab-general"), "the old name no longer finds the setting");
`)
}

// Terminal font and scrollback are set in Settings and with the font keys,
// not from the palette.
func TestTerminalFontAndScrollbackLeftThePalette(t *testing.T) {
	runFrontEnd(t, palRows+`
h.hello();
h.recv(fixture());
h.press("palette");
for (const words of ["scrollback", "terminal font"]) {
  palType(words);
  assert.ok(!palLabels().some((l) => /terminal (scrollback|font)/i.test(l)), words + " is still a palette command: " + palLabels().join(", "));
}
palType("font size");
assert.ok(palLabels().includes("Increase font size"), "the font size commands left the palette too");
h.key({ key: "Escape" });
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-terminal"));
assert.ok(h.$("set-font-family") && h.$("set-scrollback"), "Settings lost the font or the scrollback");
const before = h.commands().length;
h.key({ key: "=", ctrlKey: true });
assert.ok(h.commands().length > before, "Ctrl+= no longer changes the font size");
`)
}

// The labels the help pages and docs/keys.md are written from.
func TestRenamedKeysAndPages(t *testing.T) {
	h, ok := help.Lookup("history")
	if !ok || !strings.HasPrefix(h.Label, "Resume") || strings.Contains(strings.ToLower(h.Label+h.Short), "history") {
		t.Errorf("the history action is %+v, want a label starting Resume", h)
	}
	tm, ok := help.Lookup("team")
	if !ok || tm.Keys == "" {
		t.Fatalf("the team action has no binding: %+v", tm)
	}
	for _, k := range help.Keys {
		if k.ID != "team" && k.Keys == tm.Keys {
			t.Errorf("%s already uses %s", k.ID, tm.Keys)
		}
	}
	pages, err := help.Pages()
	if err != nil {
		t.Fatal(err)
	}
	var approve, status, resume, team bool
	for _, p := range pages {
		switch p.Slug {
		case "auto-approve":
			approve = p.Title == "Auto-approve read-only"
		case "status":
			status = strings.Contains(p.HTML, "#auto-approve")
		case "history":
			resume = p.Title == "Resume"
		case "agents":
			team = strings.Contains(p.HTML, "<kbd>"+tm.Keys+"</kbd>")
		}
	}
	if !approve {
		t.Error("no help page titled Auto-approve read-only")
	}
	if !status {
		t.Error("the status page does not link to Auto-approve read-only")
	}
	if !resume {
		t.Error("the history page is not titled Resume")
	}
	if !team {
		t.Errorf("the Team panel's section in the agents page does not show %s", tm.Keys)
	}
}

// Remote has no rail button, so the Settings button carries the tunnel: a
// filled circle, a hollow one or a triangle, never colour alone, with the
// words in the tooltip and the accessible name.
func TestTheSettingsButtonShowsTheTunnel(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const b = h.$("btn-settings");
const badge = b.querySelector(".rail-badge");
const marks = () => ["on", "pending", "trouble"].filter((c) => badge.classList.contains(c));
const remote = (over) => Object.assign({ state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z" }, over || {});
assert.deepStrictEqual(marks(), [], "remote access that is off marks Settings");
assert.ok(!b.getAttribute("aria-label"), "an unmarked Settings button has a name of its own: " + b.getAttribute("aria-label"));
assert.ok(badge.getAttribute("aria-hidden") === "true", "the badge is read out as well as the button");

h.recv(fixture({ remote: remote({ state: "connecting" }) }));
assert.deepStrictEqual(marks(), ["pending"]);
assert.strictEqual(b.getAttribute("aria-label"), "Settings, Flockdeck Remote: connecting");
assert.ok(b.dataset.tip.includes("connecting"), b.dataset.tip);

h.recv(fixture({ remote: remote({ viewers: 2 }) }));
assert.deepStrictEqual(marks(), ["on"]);
assert.strictEqual(b.getAttribute("aria-label"), "Settings, Flockdeck Remote: 2 viewers connected");
assert.ok(b.dataset.tip.includes("2 viewers connected"), b.dataset.tip);
h.recv(fixture({ remote: remote({ viewers: 1 }) }));
assert.strictEqual(b.getAttribute("aria-label"), "Settings, Flockdeck Remote: 1 viewer connected");
h.recv(fixture({ remote: remote() }));
assert.strictEqual(b.getAttribute("aria-label"), "Settings, Flockdeck Remote: connected");

h.recv(fixture({ remote: remote({ state: "error", detail: "the relay refused the connection" }) }));
assert.deepStrictEqual(marks(), ["trouble"]);
assert.ok(/^Settings, Flockdeck Remote needs you: /.test(b.getAttribute("aria-label")), b.getAttribute("aria-label"));
assert.ok(b.dataset.tip.includes("needs you"), b.dataset.tip);

// The key table arrives again (a rebind): the tunnel stays in the tooltip.
const tip = b.dataset.tip, name = b.getAttribute("aria-label");
h.recv({ type: "keyTable", keys: h.keyTable() });
assert.strictEqual(b.dataset.tip, tip, "a key table redrew the Settings tooltip without the tunnel");
assert.strictEqual(b.getAttribute("aria-label"), name, "a key table took the tunnel out of the name");

// The folded rail's menu button carries trouble too.
const toggle = h.$("rail-toggle");
assert.ok(toggle.querySelector(".rail-badge").classList.contains("waiting"), "the menu button does not show a tunnel in trouble");
assert.ok(toggle.getAttribute("aria-label").includes("Flockdeck Remote needs you"), toggle.getAttribute("aria-label"));
h.recv(fixture({ remote: remote() }));
assert.ok(!toggle.querySelector(".rail-badge").classList.contains("waiting"), "the menu button stays marked after the tunnel is back");
h.recv(fixture());
assert.deepStrictEqual(marks(), []);
assert.ok(!b.getAttribute("aria-label"));
`)
}

// The palette's API keys sheet has the TypeSafe key under the agents' keys.
func TestTheAPIKeysSheetHasTheTypeSafeKey(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.palette("api keys");
h.recv({ type: "keys", items: [{ agent: "anthropic", name: "Anthropic", set: false, vars: ["ANTHROPIC_API_KEY"] }] });
assert.ok(h.$("overlay-body").contains(h.$("set-jev-key-field")), "the sheet has no field for the TypeSafe key");
h.recv({ type: "jevKey", set: true, env: false });
assert.ok(h.$("overlay-body").textContent.includes("Key status: Set"), h.$("overlay-body").textContent);
h.click(h.$("btn-settings"));

const find = h.$("settings-find");
find.value = "jev";
find.oninput();
assert.ok(h.$("settings-tabs").querySelectorAll("button").some((x) => x.id === "settings-tab-keys"), "Find a setting does not lead to the TypeSafe key");
`)
}

// A person typing what the palette no longer asks for is pointed at Settings.
func TestThePaletteFindsScrollbackAndFontInSettings(t *testing.T) {
	runFrontEnd(t, palRows+`
h.hello();
h.recv(fixture());
h.press("palette");
for (const words of ["scrollback", "font", "typeface"]) {
  palType(words);
  assert.ok(palLabels().includes("Settings"), words + " does not lead to Settings: " + palLabels().join(", "));
}
`)
}

// The scrollback accepts any whole number in range, as the palette prompt did.
func TestAnyScrollbackInRangeCanBeChosen(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-terminal"));
const custom = h.$("set-scrollback-custom");
custom.value = "12";
h.dispatch(custom, new h.Ev("change"));
assert.ok(!h.commands().some((c) => c.cmd === "scrollback"), "a scrollback of 12 lines was accepted");
assert.ok(h.$("notice").textContent.includes("1,000"), "nothing said what a scrollback may be");
const field = h.$("set-scrollback-custom");
field.value = "123456";
h.dispatch(field, new h.Ev("change"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "scrollback", size: 123456 });
assert.strictEqual(h.terms[0].options.scrollback, 123456);
`)
}

// A window reached through the relay is refused the Team panel, so the rail
// does not offer it.
func TestARelayWindowHasNoTeamButton(t *testing.T) {
	runFrontEnd(t, `
h.recv({ type: "hello", keys: h.keyTable(), prefs: { helpSeen: true, dismissedTips: [] }, remote: true });
h.recv(fixture());
assert.ok(h.$("btn-team").hidden, "a relay window is offered the Team panel");
`)
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
assert.ok(!h.$("btn-team").hidden, "the desk's window has no Team button");
`)
}
